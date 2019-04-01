package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecr"
	resource "github.com/concourse/registry-image-resource"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/sirupsen/logrus"
)

type CheckRequest struct {
	Source  resource.Source   `json:"source"`
	Version *resource.Version `json:"version"`
}

type CheckResponse []resource.Version

func main() {
	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors: true,
	})

	var req CheckRequest
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&req)
	if err != nil {
		logrus.Errorf("invalid payload: %s", err)
		os.Exit(1)
		return
	}

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
					logrus.Errorf(ecr.ErrCodeInvalidParameterException)
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

	n, err := name.ParseReference(req.Source.Name(), name.WeakValidation)
	if err != nil {
		logrus.Errorf("could not resolve repository/tag reference: %s", err)
		os.Exit(1)
		return
	}

	auth := &authn.Basic{
		Username: req.Source.Username,
		Password: req.Source.Password,
	}

	imageOpts := []remote.ImageOption{
		remote.WithTransport(resource.RetryTransport),
	}

	if auth.Username != "" && auth.Password != "" {
		imageOpts = append(imageOpts, remote.WithAuth(auth))
	}

	image, err := remote.Image(n, imageOpts...)
	if err != nil {
		logrus.Errorf("failed to get remote image: %s", err)
		os.Exit(1)
		return
	}

	digest, err := image.Digest()
	if err != nil {
		logrus.Errorf("failed get image digest: %s", err)
		os.Exit(1)
		return
	}

	response := CheckResponse{}
	if req.Version != nil && req.Version.Digest != digest.String() {
		digestRef, err := name.ParseReference(req.Source.Repository+"@"+req.Version.Digest, name.WeakValidation)
		if err != nil {
			logrus.Errorf("could not resolve repository/digest reference: %s", err)
			os.Exit(1)
			return
		}

		var digestImage v1.Image
		if auth.Username != "" && auth.Password != "" {
			digestImage, err = remote.Image(digestRef, remote.WithAuth(auth))
		} else {
			digestImage, err = remote.Image(digestRef)
		}
		if err != nil {
			logrus.Errorf("failed to get remote image: %s", err)
			os.Exit(1)
			return
		}

		var missingDigest bool
		_, err = digestImage.Digest()
		if err != nil {
			if rErr, ok := err.(*remote.Error); ok {
				for _, e := range rErr.Errors {
					if e.Code == remote.ManifestUnknownErrorCode {
						missingDigest = true
						break
					}
				}
			}

			if !missingDigest {
				logrus.Errorf("failed to get cursor image digest: %s", err)
				os.Exit(1)
				return
			}
		}

		if !missingDigest {
			response = append(response, *req.Version)
		}
	}

	response = append(response, resource.Version{
		Digest: digest.String(),
	})

	json.NewEncoder(os.Stdout).Encode(response)
}
