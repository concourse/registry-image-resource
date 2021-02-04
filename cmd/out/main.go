package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	gcr "github.com/autonomic-ai/notary-gcr/pkg/gcr"
	resource "github.com/concourse/registry-image-resource"
	"github.com/fatih/color"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/logs"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
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

	if req.Source.GcpProject != "" {
		if !req.Source.AuthenticateToGCP() {
			os.Exit(1)
			return
		}
	} else if req.Source.AwsRegion != "" {
		if !req.Source.AuthenticateToECR() {
			os.Exit(1)
			return
		}
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
