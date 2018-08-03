package main

import (
	"encoding/json"
	"os"

	resource "github.com/concourse/registry-image-resource"
	"github.com/google/go-containerregistry/pkg/name"
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
	err := json.NewDecoder(os.Stdin).Decode(&req)
	if err != nil {
		logrus.Errorf("invalid payload: %s", err)
		os.Exit(1)
		return
	}

	ref := req.Source.Repository + ":" + req.Source.Tag

	n, err := name.ParseReference(ref, name.WeakValidation)
	if err != nil {
		logrus.Errorf("could not resolve repository/tag reference: %s", err)
		os.Exit(1)
		return
	}

	image, err := remote.Image(n)
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

		digestImage, err := remote.Image(digestRef)
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
