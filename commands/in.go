package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"

	resource "github.com/concourse/registry-image-resource"
	"github.com/fatih/color"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/sirupsen/logrus"
)

type ImageMetadata struct {
	Env  []string `json:"env"`
	User string   `json:"user"`
}

type In struct {
	stdin  io.Reader
	stderr io.Writer
	stdout io.Writer
	args   []string
}

func NewIn(
	stdin io.Reader,
	stderr io.Writer,
	stdout io.Writer,
	args []string,
) *In {
	return &In{
		stdin:  stdin,
		stderr: stderr,
		stdout: stdout,
		args:   args,
	}
}

func (i *In) Execute() error {
	setupLogging(i.stderr)

	var req resource.InRequest
	decoder := json.NewDecoder(i.stdin)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&req)
	if err != nil {
		return fmt.Errorf("invalid payload: %s", err)
	}

	if req.Source.Debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	if len(i.args) < 2 {
		return fmt.Errorf("destination path not specified")
	}

	dest := i.args[1]

	if req.Source.AwsRegion != "" {
		if !req.Source.AuthenticateToECR() {
			return fmt.Errorf("cannot authenticate with ECR")
		}
	}

	repo, err := req.Source.NewRepository()
	if err != nil {
		return fmt.Errorf("failed to resolve repository: %w", err)
	}

	tag := repo.Tag(req.Version.Tag)

	if !req.Params.SkipDownload {
		mirrorSource, hasMirror, err := req.Source.Mirror()
		if err != nil {
			return fmt.Errorf("failed to resolve mirror: %w", err)
		}

		usedMirror := false
		if hasMirror {
			err := downloadWithRetry(tag, mirrorSource, req.Params, req.Version, dest, i.stderr)
			if err != nil {
				logrus.Warnf("download from mirror %s failed: %s", mirrorSource.Repository, err)
			} else {
				usedMirror = true
			}
		}

		if !usedMirror {
			err := downloadWithRetry(tag, req.Source, req.Params, req.Version, dest, i.stderr)
			if err != nil {
				return fmt.Errorf("download failed: %w", err)
			}
		}
	}

	err = saveVersionInfo(dest, req.Version, req.Source.Repository)
	if err != nil {
		return fmt.Errorf("saving version info failed: %w", err)
	}

	err = json.NewEncoder(os.Stdout).Encode(resource.InResponse{
		Version: req.Version,
		Metadata: append(req.Source.Metadata(), resource.MetadataField{
			Name:  "tag",
			Value: req.Version.Tag,
		}),
	})
	if err != nil {
		return fmt.Errorf("could not marshal JSON: %s", err)
	}

	return nil
}

func downloadWithRetry(tag name.Tag, source resource.Source, params resource.GetParams, version resource.Version, dest string, stderr io.Writer) error {
	fmt.Fprintf(os.Stderr, "fetching %s@%s\n", color.GreenString(source.Repository), color.YellowString(version.Digest))

	repo, err := source.NewRepository()
	if err != nil {
		return fmt.Errorf("resolve repository name: %w", err)
	}

	return resource.RetryOnRateLimit(func() error {
		opts, err := source.AuthOptions(repo, []string{transport.PullScope})
		if err != nil {
			return err
		}

		image, err := remote.Image(repo.Digest(version.Digest), opts...)
		if err != nil {
			return fmt.Errorf("get image: %w", err)
		}

		err = saveImage(dest, tag, image, params.Format(), source.Debug, stderr)
		if err != nil {
			return fmt.Errorf("save image: %w", err)
		}

		return nil
	})
}

func saveImage(dest string, tag name.Tag, image v1.Image, format string, debug bool, stderr io.Writer) error {
	switch format {
	case "oci":
		err := ociFormat(dest, tag, image)
		if err != nil {
			return fmt.Errorf("write oci image: %w", err)
		}
	case "rootfs":
		err := rootfsFormat(dest, image, debug, stderr)
		if err != nil {
			return fmt.Errorf("write rootfs: %w", err)
		}
	}

	return nil
}

func saveVersionInfo(dest string, version resource.Version, repo string) error {
	err := ioutil.WriteFile(filepath.Join(dest, "tag"), []byte(version.Tag), 0644)
	if err != nil {
		return fmt.Errorf("write image tag: %w", err)
	}

	err = ioutil.WriteFile(filepath.Join(dest, "digest"), []byte(version.Digest), 0644)
	if err != nil {
		return fmt.Errorf("write image digest: %w", err)
	}

	err = ioutil.WriteFile(filepath.Join(dest, "repository"), []byte(repo), 0644)
	if err != nil {
		return fmt.Errorf("write image repository: %w", err)
	}

	return nil
}

func ociFormat(dest string, tag name.Tag, image v1.Image) error {
	err := tarball.WriteToFile(filepath.Join(dest, "image.tar"), tag, image)
	if err != nil {
		return fmt.Errorf("write OCI image: %s", err)
	}

	return nil
}

func rootfsFormat(dest string, image v1.Image, debug bool, stderr io.Writer) error {
	err := unpackImage(filepath.Join(dest, "rootfs"), image, debug, stderr)
	if err != nil {
		return fmt.Errorf("extract image: %w", err)
	}

	cfg, err := image.ConfigFile()
	if err != nil {
		return fmt.Errorf("inspect image config: %w", err)
	}

	meta, err := os.Create(filepath.Join(dest, "metadata.json"))
	if err != nil {
		return fmt.Errorf("create image metadata: %w", err)
	}

	env := cfg.Config.Env

	user := cfg.Config.User

	err = json.NewEncoder(meta).Encode(ImageMetadata{
		Env:  env,
		User: user,
	})
	if err != nil {
		return fmt.Errorf("write image metadata: %w", err)
	}

	err = meta.Close()
	if err != nil {
		return fmt.Errorf("close image metadata file: %w", err)
	}

	return nil
}
