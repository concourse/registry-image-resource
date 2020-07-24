package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
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
		logrus.Error("destination path not specified")
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
		ver, err := semver.NewVersion(req.Params.Version)
		if err != nil {
			if err == semver.ErrInvalidSemVer {
				logrus.Errorf("invalid semantic version: %q", req.Params.Version)
				os.Exit(1)
			}

			logrus.Errorf("failed to parse version: %s", err)
			os.Exit(1)
		}

		// vito: subtle gotcha here - if someone passes the version as v1.2.3, the
		// 'v' will be stripped, as *semver.Version parses it but does not preserve
		// it in .String().
		//
		// we could call .Original(), of course, but it seems common practice to
		// *not* have the v prefix in Docker image tags, so it might be better to
		// just enforce it until someone complains enough; it seems more likely to
		// be an accident than a legacy practice that must be preserved.
		//
		// if that's the person reading this: sorry! PR welcome! (maybe we should
		// add tag_prefix:?)
		tag := ver.String()
		if req.Source.Variant != "" {
			tag += "-" + req.Source.Variant
		}

		tagsToPush = append(tagsToPush, repo.Tag(tag))

		if req.Params.BumpAliases && ver.Prerelease() == "" {
			auth := &authn.Basic{
				Username: req.Source.Username,
				Password: req.Source.Password,
			}

			imageOpts := []remote.Option{}

			if auth.Username != "" && auth.Password != "" {
				imageOpts = append(imageOpts, remote.WithAuth(auth))
			}

			versions, err := remote.List(repo, imageOpts...)
			if err != nil {
				logrus.Error("failed to list repository tags: %w", err)
				os.Exit(1)
				return
			}

			bumpLatest := true
			bumpMajor := true
			bumpMinor := true
			for _, v := range versions {
				versionStr := v
				if req.Source.Variant != "" {
					versionStr = strings.TrimSuffix(versionStr, "-"+req.Source.Variant)
				}

				remoteVer, err := semver.NewVersion(versionStr)
				if err != nil {
					continue
				}

				// don't compare to prereleases or other variants
				if remoteVer.Prerelease() != "" {
					continue
				}

				if remoteVer.GreaterThan(ver) {
					bumpLatest = false
				}

				if remoteVer.Major() == ver.Major() && remoteVer.Minor() > ver.Minor() {
					bumpMajor = false
				}

				if remoteVer.Major() == ver.Major() && remoteVer.Minor() == ver.Minor() && remoteVer.Patch() > ver.Patch() {
					bumpMinor = false
					bumpMajor = false
				}
			}

			if bumpLatest {
				latestTag := "latest"
				if req.Source.Variant != "" {
					latestTag = req.Source.Variant
				}

				tagsToPush = append(tagsToPush, repo.Tag(latestTag))
			}

			if bumpMajor {
				tagName := fmt.Sprintf("%d", ver.Major())
				if req.Source.Variant != "" {
					tagName += "-" + req.Source.Variant
				}

				tagsToPush = append(tagsToPush, repo.Tag(tagName))
			}

			if bumpMinor {
				tagName := fmt.Sprintf("%d.%d", ver.Major(), ver.Minor())
				if req.Source.Variant != "" {
					tagName += "-" + req.Source.Variant
				}

				tagsToPush = append(tagsToPush, repo.Tag(tagName))
			}
		}
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
		logrus.Error("no tag specified - need either 'version:' in params or 'tag:' in source")
		os.Exit(1)
		return
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
