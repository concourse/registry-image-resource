package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	resource "github.com/concourse/registry-image-resource"
	color "github.com/fatih/color"
	"github.com/google/go-containerregistry/pkg/logs"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/sirupsen/logrus"
)

type ImageMetadata struct {
	Env  []string `json:"env"`
	User string   `json:"user"`
}

func main() {
	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors: true,
	})

	logs.Progress = log.New(os.Stderr, "", log.LstdFlags)
	logs.Warn = log.New(os.Stderr, "", log.LstdFlags)

	color.NoColor = false

	var req resource.InRequest
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

	if req.Source.AwsAccessKeyId != "" && req.Source.AwsSecretAccessKey != "" && req.Source.AwsRegion != "" {
		if !req.Source.AuthenticateToECR() {
			os.Exit(1)
			return
		}
	}

	repo, err := name.NewRepository(req.Source.Repository)
	if err != nil {
		logrus.Errorf("could not resolve repository: %s", err)
		os.Exit(1)
		return
	}

	tag := repo.Tag(req.Version.Tag)

	if !req.Params.SkipDownload {
		mirrorSource, hasMirror, err := req.Source.Mirror()
		if err != nil {
			logrus.Errorf("failed to resolve mirror: %s", err)
			os.Exit(1)
			return
		}

		usedMirror := false
		if hasMirror {
			err := downloadWithRetry(tag, mirrorSource, req.Params, req.Version, dest)
			if err != nil {
				logrus.Warnf("download from mirror %s failed: %s", mirrorSource.Repository, err)
			} else {
				usedMirror = true
			}
		}

		if !usedMirror {
			err := downloadWithRetry(tag, req.Source, req.Params, req.Version, dest)
			if err != nil {
				logrus.Errorf("download failed: %s", err)
				os.Exit(1)
				return
			}
		}
	}

	err = saveVersionInfo(dest, req.Version)
	if err != nil {
		logrus.Errorf("saving version info failed: %s", err)
		os.Exit(1)
		return
	}

	json.NewEncoder(os.Stdout).Encode(resource.InResponse{
		Version: req.Version,
		Metadata: append(req.Source.Metadata(), resource.MetadataField{
			Name:  "tag",
			Value: req.Version.Tag,
		}),
	})
}

func downloadWithRetry(tag name.Tag, source resource.Source, params resource.GetParams, version resource.Version, dest string) error {
	fmt.Fprintf(os.Stderr, "fetching %s@%s\n", color.GreenString(source.Repository), color.YellowString(version.Digest))

	repo, err := name.NewRepository(source.Repository)
	if err != nil {
		return fmt.Errorf("resolve repository name: %w", err)
	}

	return resource.RetryOnRateLimit(func() error {
		opts, err := source.AuthOptions(repo)
		if err != nil {
			return err
		}

		image, err := remote.Image(repo.Digest(version.Digest), opts...)
		if err != nil {
			return fmt.Errorf("get image: %w", err)
		}

		err = saveImage(dest, tag, image, params.Format(), source.Debug)
		if err != nil {
			return fmt.Errorf("save image: %w", err)
		}

		return nil
	})
}

func saveImage(dest string, tag name.Tag, image v1.Image, format string, debug bool) error {
	switch format {
	case "oci":
		err := ociFormat(dest, tag, image)
		if err != nil {
			return fmt.Errorf("write oci image: %w", err)
		}
	case "rootfs":
		err := rootfsFormat(dest, image, debug)
		if err != nil {
			return fmt.Errorf("write rootfs: %w", err)
		}
	}

	return nil
}

func saveVersionInfo(dest string, version resource.Version) error {
	err := ioutil.WriteFile(filepath.Join(dest, "tag"), []byte(version.Tag), 0644)
	if err != nil {
		return fmt.Errorf("write image tag: %w", err)
	}

	err = ioutil.WriteFile(filepath.Join(dest, "digest"), []byte(version.Digest), 0644)
	if err != nil {
		return fmt.Errorf("write image digest: %w", err)
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

func rootfsFormat(dest string, image v1.Image, debug bool) error {
	err := unpackImage(filepath.Join(dest, "rootfs"), image, debug)
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
