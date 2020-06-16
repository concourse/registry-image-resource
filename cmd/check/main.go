package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	resource "github.com/concourse/registry-image-resource"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/logs"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
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

	logs.Progress = log.New(os.Stderr, "", log.LstdFlags)
	logs.Warn = log.New(os.Stderr, "", log.LstdFlags)

	var req CheckRequest
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&req)
	if err != nil {
		logrus.Errorf("invalid payload: %s", err)
		os.Exit(1)
		return
	}

	if req.Source.AwsAccessKeyId != "" && req.Source.AwsSecretAccessKey != "" && req.Source.AwsRegion != "" {
		if !req.Source.AuthenticateToECR() {
			os.Exit(1)
			return
		}
	}

	repo, err := name.NewRepository(req.Source.Repository, name.WeakValidation)
	if err != nil {
		logrus.Errorf("failed to resolve repository: %s", err)
		os.Exit(1)
		return
	}

	var response CheckResponse

	origin := repo.Tag(req.Source.Tag())

	if req.Source.RegistryMirror != nil {
		registry, err := name.NewRegistry(req.Source.RegistryMirror.Host, name.WeakValidation)
		if err != nil {
			logrus.Errorf("could not resolve registry: %s", err)
			os.Exit(1)
			return
		}

		repo.Registry = registry
		mirror := repo.Tag(req.Source.Tag())

		response, err = checkWithRetry(req.Source.RegistryMirror.BasicCredentials, req.Version, mirror)
		if err != nil {
			logrus.Warnf("checking mirror %s failed: %s", mirror.RegistryStr(), err)
		} else if len(response) == 0 {
			logrus.Warnf("checking mirror %s failed: tag not found", mirror.RegistryStr())
		}
	}

	if response == nil || len(response) == 0 {
		response, err = checkWithRetry(req.Source.BasicCredentials, req.Version, origin)
	}
	if err != nil {
		logrus.Errorf("checking origin %s failed: %s", origin.RegistryStr(), err)
		os.Exit(1)
		return
	}

	json.NewEncoder(os.Stdout).Encode(response)
}

func checkWithRetry(principal resource.BasicCredentials, version *resource.Version, ref name.Tag) (CheckResponse, error) {
	var response CheckResponse
	err := resource.RetryOnRateLimit(func() error {
		var err error
		response, err = check(principal, version, ref)
		return err
	})
	return response, err
}

func check(principal resource.BasicCredentials, version *resource.Version, ref name.Tag) (CheckResponse, error) {
	auth := &authn.Basic{
		Username: principal.Username,
		Password: principal.Password,
	}

	imageOpts := []remote.Option{}

	if auth.Username != "" && auth.Password != "" {
		imageOpts = append(imageOpts, remote.WithAuth(auth))
	}

	var missingTag bool
	image, err := remote.Image(ref, imageOpts...)
	if err != nil {
		missingTag = checkMissingManifest(err)
		if !missingTag {
			return CheckResponse{}, fmt.Errorf("get remote image: %w", err)
		}
	}

	var digest v1.Hash
	if !missingTag {
		digest, err = image.Digest()
		if err != nil {
			return CheckResponse{}, fmt.Errorf("get cursor image digest: %w", err)
		}
	}

	response := CheckResponse{}
	if version != nil && !missingTag && version.Digest != digest.String() {
		digestRef := ref.Repository.Digest(version.Digest)

		digestImage, err := remote.Image(digestRef, imageOpts...)
		var missingDigest bool
		if err != nil {
			missingDigest = checkMissingManifest(err)
			if !missingDigest {
				return CheckResponse{}, fmt.Errorf("get remote image: %w", err)
			}
		}

		if !missingDigest {
			_, err = digestImage.Digest()
			if err != nil {
				return CheckResponse{}, fmt.Errorf("get cursor image digest: %w", err)
			}

			response = append(response, *version)
		}
	}

	if !missingTag {
		response = append(response, resource.Version{
			Digest: digest.String(),
		})
	}

	return response, nil
}

func checkMissingManifest(err error) bool {
	var missing bool
	if rErr, ok := err.(*transport.Error); ok {
		for _, e := range rErr.Errors {
			if e.Code == transport.ManifestUnknownErrorCode {
				missing = true
				break
			}
		}
	}
	return missing
}
