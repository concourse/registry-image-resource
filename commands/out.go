package commands

import (
	"encoding/json"
	"fmt"
	resource "github.com/concourse/registry-image-resource"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/simonshyu/notary-gcr/pkg/gcr"
	"github.com/sirupsen/logrus"
	"io"
	"net/http"
	"path/filepath"
)

type OutRequest struct {
	Source resource.Source    `json:"source"`
	Params resource.PutParams `json:"params"`
}

type OutResponse struct {
	Version  resource.Version         `json:"version"`
	Metadata []resource.MetadataField `json:"metadata"`
}

type out struct {
	stdin  io.Reader
	stderr io.Writer
	stdout io.Writer
	args   []string
}

func NewOut(
	stdin io.Reader,
	stderr io.Writer,
	stdout io.Writer,
	args []string,
) *out {
	return &out{
		stdin:  stdin,
		stderr: stderr,
		stdout: stdout,
		args:   args,
	}
}

func (o *out) Execute() error {
	setupLogging(o.stderr)

	var req OutRequest
	decoder := json.NewDecoder(o.stdin)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&req)
	if err != nil {
		return fmt.Errorf("invalid payload: %s", err)
	}

	if req.Source.Debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	if len(o.args) < 2 {
		return fmt.Errorf("destination path not specified")
	}

	src := o.args[1]

	if req.Source.AwsAccessKeyId != "" && req.Source.AwsSecretAccessKey != "" && req.Source.AwsRegion != "" {
		if !req.Source.AuthenticateToECR() {
			return fmt.Errorf("cannot authenticate with ECR")
		}
	}

	ref, err := name.ParseReference(req.Source.Name(), name.WeakValidation)
	if err != nil {
		return fmt.Errorf("could not resolve repository/tag reference: %s", err)
	}

	tags, err := req.Params.ParseTags(src)
	if err != nil {
		return fmt.Errorf("could not parse additional tags: %s", err)
	}

	var extraRefs []name.Reference
	for _, tag := range tags {
		n := fmt.Sprintf("%s:%s", req.Source.Repository, tag)

		extraRef, err := name.ParseReference(n, name.WeakValidation)
		if err != nil {
			return fmt.Errorf("could not resolve repository/tag reference: %s", err)
		}

		extraRefs = append(extraRefs, extraRef)
	}

	imagePath := filepath.Join(src, req.Params.Image)
	matches, err := filepath.Glob(imagePath)
	if err != nil {
		return fmt.Errorf("failed to glob path '%s': %s", req.Params.Image, err)
	}
	if len(matches) == 0 {
		return fmt.Errorf("no files match glob '%s'", req.Params.Image)
	}
	if len(matches) > 1 {
		return fmt.Errorf("too many files match glob '%s'", req.Params.Image)
	}

	img, err := tarball.ImageFromPath(matches[0], nil)
	if err != nil {
		return fmt.Errorf("could not load image from path '%s': %s", req.Params.Image, err)
	}

	digest, err := img.Digest()
	if err != nil {
		return fmt.Errorf("failed to get image digest: %s", err)
	}

	logrus.Infof("pushing %s to %s", digest, ref.Name())

	err = resource.RetryOnRateLimit(func() error {
		return outPut(req, img, ref, extraRefs)
	})
	if err != nil {
		return fmt.Errorf("pushing image failed: %s", err)
	}

	err = json.NewEncoder(o.stdout).Encode(OutResponse{
		Version: resource.Version{
			Digest: digest.String(),
		},
		Metadata: req.Source.MetadataWithAdditionalTags(tags),
	})

	if err != nil {
		return fmt.Errorf("could not marshal JSON: %s", err)
	}

	return nil
}

func outPut(req OutRequest, img v1.Image, ref name.Reference, extraRefs []name.Reference) error {
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
