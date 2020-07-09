package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	resource "github.com/concourse/registry-image-resource"
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

func main() {
	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors: true,
	})

	logs.Progress = log.New(os.Stderr, "", log.LstdFlags)
	logs.Warn = log.New(os.Stderr, "", log.LstdFlags)

	color.NoColor = false

	var req resource.OutRequest
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

	if req.Source.AwsAccessKeyId != "" && req.Source.AwsSecretAccessKey != "" && req.Source.AwsRegion != "" {
		if !req.Source.AuthenticateToECR() {
			os.Exit(1)
			return
		}
	}

	tags, err := req.Params.ParseTags(src)
	if err != nil {
		logrus.Errorf("could not parse additional tags: %s", err)
		os.Exit(1)
		return
	}

	tagsToPush := []name.Tag{}

	repo, err := name.NewRepository(req.Source.Repository)
	if err != nil {
		logrus.Errorf("could not resolve repository: %s", err)
		os.Exit(1)
		return
	}

	if req.Source.Tag != "" {
		tagsToPush = append(tagsToPush, repo.Tag(req.Source.Tag.String()))
	}

	if req.Params.Version != "" {
		tag := req.Params.Version
		if req.Source.Variant != "" {
			tag += "-" + req.Source.Variant
		}

		tagsToPush = append(tagsToPush, repo.Tag(tag))
	}

	for _, tag := range tags {
		n := fmt.Sprintf("%s:%s", req.Source.Repository, tag)

		extraRef, err := name.NewTag(n, name.WeakValidation)
		if err != nil {
			logrus.Errorf("could not resolve repository/tag reference: %s", err)
			os.Exit(1)
			return
		}

		tagsToPush = append(tagsToPush, extraRef)
	}

	if len(tagsToPush) == 0 {
		panic("TODO: at least one tag must be specified")
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

	err = resource.RetryOnRateLimit(func() error {
		return put(req, img, tagsToPush)
	})
	if err != nil {
		logrus.Errorf("pushing image failed: %s", err)
		os.Exit(1)
		return
	}

	pushedTags := []string{}
	for _, tag := range tagsToPush {
		pushedTags = append(pushedTags, tag.TagStr())
	}

	json.NewEncoder(os.Stdout).Encode(resource.OutResponse{
		Version: resource.Version{
			Tag:    tagsToPush[0].TagStr(),
			Digest: digest.String(),
		},
		Metadata: append(req.Source.Metadata(), resource.MetadataField{
			Name:  "tags",
			Value: strings.Join(pushedTags, " "),
		}),
	})
}

func put(req resource.OutRequest, img v1.Image, refs []name.Tag) error {
	auth := &authn.Basic{
		Username: req.Source.Username,
		Password: req.Source.Password,
	}

	var notaryConfigDir string
	var err error
	if req.Source.ContentTrust != nil {
		notaryConfigDir, err = req.Source.ContentTrust.PrepareConfigDir()
		if err != nil {
			return fmt.Errorf("prepare notary-config-dir: %w", err)
		}
	}

	for _, extraRef := range refs {
		logrus.Infof("pushing to tag %s", extraRef.Identifier())

		err = remote.Write(extraRef, img, remote.WithAuth(auth))
		if err != nil {
			return fmt.Errorf("tag image: %w", err)
		}

		logrus.Info("pushed")

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
