package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecr"
	resource "github.com/concourse/registry-image-resource"
	color "github.com/fatih/color"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/sirupsen/logrus"
)

type InRequest struct {
	Source  resource.Source    `json:"source"`
	Params  resource.GetParams `json:"params"`
	Version resource.Version   `json:"version"`
}

type InResponse struct {
	Version  resource.Version         `json:"version"`
	Metadata []resource.MetadataField `json:"metadata"`
}

type ImageMetadata struct {
	Env  []string `json:"env"`
	User string   `json:"user"`
}

func main() {
	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors: true,
	})

	color.NoColor = false

	var req InRequest
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

	dest := os.Args[1]

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

	ref := req.Source.Repository + "@" + req.Version.Digest

	n, err := name.ParseReference(ref, name.WeakValidation)
	if err != nil {
		logrus.Errorf("failed to resolve name: %s", err)
		os.Exit(1)
		return
	}

	fmt.Fprintf(os.Stderr, "fetching %s@%s\n", color.GreenString(req.Source.Repository), color.YellowString(req.Version.Digest))

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
		logrus.Errorf("failed to locate remote image: %s", err)
		os.Exit(1)
		return
	}

	switch req.Params.Format() {
	case "oci":
		ociFormat(dest, req, image)
	case "rootfs":
		rootfsFormat(dest, req, image)
	}

	err = ioutil.WriteFile(filepath.Join(dest, "tag"), []byte(req.Source.Tag()), 0644)
	if err != nil {
		logrus.Errorf("failed to save image tag: %s", err)
		os.Exit(1)
		return
	}

	err = saveDigest(dest, image)
	if err != nil {
		logrus.Errorf("failed to save image digest: %s", err)
		os.Exit(1)
		return
	}

	json.NewEncoder(os.Stdout).Encode(InResponse{
		Version:  req.Version,
		Metadata: req.Source.Metadata(),
	})
}

func saveDigest(dest string, image v1.Image) error {
	digest, err := image.Digest()
	if err != nil {
		return err
	}

	digestDest := filepath.Join(dest, "digest")
	return ioutil.WriteFile(digestDest, []byte(digest.String()), 0644)
}

func ociFormat(dest string, req InRequest, image v1.Image) {
	tag, err := name.NewTag(req.Source.Name(), name.WeakValidation)
	if err != nil {
		logrus.Errorf("failed to construct tag reference: %s", err)
		os.Exit(1)
		return
	}

	err = tarball.WriteToFile(filepath.Join(dest, "image.tar"), tag, image)
	if err != nil {
		logrus.Errorf("failed to write OCI image: %s", err)
		os.Exit(1)
		return
	}
}

func rootfsFormat(dest string, req InRequest, image v1.Image) {
	err := unpackImage(filepath.Join(dest, "rootfs"), image, req.Source.Debug)
	if err != nil {
		logrus.Errorf("failed to extract image: %s", err)
		os.Exit(1)
		return
	}

	cfg, err := image.ConfigFile()
	if err != nil {
		logrus.Errorf("failed to inspect image config: %s", err)
		os.Exit(1)
		return
	}

	meta, err := os.Create(filepath.Join(dest, "metadata.json"))
	if err != nil {
		logrus.Errorf("failed to create image metadata: %s", err)
		os.Exit(1)
		return
	}

	env := cfg.Config.Env
	if len(env) == 0 {
		env = cfg.ContainerConfig.Env
	}

	user := cfg.Config.User
	if user == "" {
		user = cfg.ContainerConfig.User
	}

	err = json.NewEncoder(meta).Encode(ImageMetadata{
		Env:  env,
		User: user,
	})
	if err != nil {
		logrus.Errorf("failed to write image metadata: %s", err)
		os.Exit(1)
		return
	}

	err = meta.Close()
	if err != nil {
		logrus.Errorf("failed to close image metadata file: %s", err)
		os.Exit(1)
		return
	}
}
