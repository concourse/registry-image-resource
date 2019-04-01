package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	resource "github.com/concourse/registry-image-resource"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/fatih/color"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/logs"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	gcr "github.com/simonshyu/notary-gcr/pkg/gcr"
	"github.com/sirupsen/logrus"
)

type OutRequest struct {
	Source resource.Source    `json:"source"`
	Params resource.PutParams `json:"params"`
}

type OutResponse struct {
	Version  resource.Version         `json:"version"`
	Metadata []resource.MetadataField `json:"metadata"`
}

func main() {
	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors: true,
	})

	logs.Progress = log.New(os.Stderr, "", log.LstdFlags)
	logs.Warn = log.New(os.Stderr, "", log.LstdFlags)

	color.NoColor = false

	var req OutRequest
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&req)
	if err != nil {
		logrus.Errorf("invalid payload: %s", err)
		os.Exit(1)
		return
	}

	if req.Source.Debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	if len(os.Args) < 2 {
		logrus.Errorf("destination path not specified")
		os.Exit(1)
		return
	}

	src := os.Args[1]

	if req.Source.Ecr {
		if req.Source.AwsAccessKeyId != "" {
			os.Setenv("AWS_ACCESS_KEY_ID", req.Source.AwsAccessKeyId)
		}
		if req.Source.AwsSecretAccessKey != "" {
			os.Setenv("AWS_SECRET_ACCESS_KEY", req.Source.AwsSecretAccessKey)
		}
		if req.Source.AwsRegion != "" {
			os.Setenv("AWS_REGION", req.Source.AwsRegion)
		}
		mySession := session.Must(session.NewSession())
		client := ecr.New(mySession)
		// If a role arn has been supplied, then assume role and get a new session
		if req.Source.AwsRoleArn != "" {
			creds := stscreds.NewCredentials(mySession, req.Source.AwsRoleArn)
			client = ecr.New(mySession, &aws.Config{Credentials: creds})
		}
		input := &ecr.GetAuthorizationTokenInput{}
		result, err := client.GetAuthorizationToken(input)
		if err != nil {
			if aerr, ok := err.(awserr.Error); ok {
				switch aerr.Code() {
				case ecr.ErrCodeServerException:
					logrus.Errorf(ecr.ErrCodeServerException)
					logrus.Errorf(aerr.Error())
				case ecr.ErrCodeInvalidParameterException:
					logrus.Errorf(ecr.ErrCodeServerException)
					logrus.Errorf(aerr.Error())
				default:
					logrus.Errorf(aerr.Error())
				}
			} else {
				// Print the error, cast err to awserr.Error to get the Code and
				// Message from an error.
				logrus.Errorf(err.Error())
			}
			return
		}

		for _, data := range result.AuthorizationData {
			output, err := base64.StdEncoding.DecodeString(*data.AuthorizationToken)

			if err != nil {
				logrus.Errorf("Failed to decode credential (%s)", err.Error())
				return
			}

			split := strings.Split(string(output), ":")

			if len(split) == 2 {
				req.Source.Password = strings.TrimSpace(split[1])
			} else {
				logrus.Errorf("Failed to parse password.")
				return
			}
		}

		// Update username and repository
		req.Source.Username = "AWS"
		req.Source.Repository = strings.Join([]string{strings.Replace(*result.AuthorizationData[0].ProxyEndpoint, "https://", "", -1), req.Source.Repository}, "/")
	}

	ref, err := name.ParseReference(req.Source.Name(), name.WeakValidation)
	if err != nil {
		logrus.Errorf("could not resolve repository/tag reference: %s", err)
		os.Exit(1)
		return
	}

	tags, err := req.Params.ParseTags(src)
	if err != nil {
		logrus.Errorf("could not parse additional tags: %s", err)
		os.Exit(1)
		return
	}

	var extraRefs []name.Reference
	for _, tag := range tags {
		n := fmt.Sprintf("%s:%s", req.Source.Repository, tag)

		extraRef, err := name.ParseReference(n, name.WeakValidation)
		if err != nil {
			logrus.Errorf("could not resolve repository/tag reference: %s", err)
			os.Exit(1)
			return
		}

		extraRefs = append(extraRefs, extraRef)
	}

	imagePath := filepath.Join(src, req.Params.Image)
	matches, err := filepath.Glob(imagePath)
	if err != nil {
		logrus.Errorf("failed to glob path '%s': %s", req.Params.Image, err)
		os.Exit(1)
		return
	}
	if len(matches) == 0 {
		logrus.Errorf("no files match glob '%s'", req.Params.Image)
		os.Exit(1)
		return
	}
	if len(matches) > 1 {
		logrus.Errorf("too many files match glob '%s'", req.Params.Image)
		os.Exit(1)
		return
	}

	img, err := tarball.ImageFromPath(matches[0], nil)
	if err != nil {
		logrus.Errorf("could not load image from path '%s': %s", req.Params.Image, err)
		os.Exit(1)
		return
	}

	digest, err := img.Digest()
	if err != nil {
		logrus.Errorf("failed to get image digest: %s", err)
		os.Exit(1)
		return
	}

	logrus.Infof("pushing %s to %s", digest, ref.Name())

	err = resource.RetryOnRateLimit(func() error {
		return put(req, img, ref, extraRefs)
	})
	if err != nil {
		logrus.Errorf("pushing image failed: %s", err)
		os.Exit(1)
		return
	}

	json.NewEncoder(os.Stdout).Encode(OutResponse{
		Version: resource.Version{
			Digest: digest.String(),
		},
		Metadata: req.Source.MetadataWithAdditionalTags(tags),
	})
}

func put(req OutRequest, img v1.Image, ref name.Reference, extraRefs []name.Reference) error {
	auth := &authn.Basic{
		Username: req.Source.Username,
		Password: req.Source.Password,
	}

	err := remote.Write(ref, img, remote.WithAuth(auth))
	if err != nil {
		return fmt.Errorf("upload image: %w", err)
	}

	logrus.Info("pushed")

	var notaryConfigDir string
	if req.Source.ContentTrust != nil {
		notaryConfigDir, err = req.Source.ContentTrust.PrepareConfigDir()
		if err != nil {
			return fmt.Errorf("prepare notary-config-dir: %w", err)
		}

		trustedRepo, err := gcr.NewTrustedGcrRepository(notaryConfigDir, ref, auth)
		if err != nil {
			return fmt.Errorf("create TrustedGcrRepository: %w", err)
		}

		err = trustedRepo.SignImage(img)
		if err != nil {
			logrus.Errorf("failed to sign image: %s", err)
		}
	}

	for _, extraRef := range extraRefs {
		logrus.Infof("pushing as tag %s", extraRef.Identifier())

		err = remote.Write(extraRef, img, remote.WithAuth(auth), remote.WithTransport(http.DefaultTransport))
		if err != nil {
			return fmt.Errorf("tag image: %w", err)
		}

		logrus.Info("tagged")

		if notaryConfigDir != "" {
			trustedRepo, err := gcr.NewTrustedGcrRepository(notaryConfigDir, extraRef, auth)
			if err != nil {
				return fmt.Errorf("create TrustedGcrRepository: %w", err)
			}

			logrus.Info("signing image")

			err = trustedRepo.SignImage(img)
			if err != nil {
				logrus.Errorf("failed to sign image: %s", err)
			}
		}
	}

	return nil
}
