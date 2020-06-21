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
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/logs"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
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

	logs.Progress = log.New(os.Stderr, "", log.LstdFlags)
	logs.Warn = log.New(os.Stderr, "", log.LstdFlags)

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

	tag := repo.Tag(req.Version.Tag)

	if !req.Params.SkipDownload {
		fmt.Fprintf(os.Stderr, "fetching %s@%s\n", color.GreenString(req.Source.Repository), color.YellowString(req.Version.Digest))

		var image v1.Image
		digest := new(name.Digest)

		if req.Source.RegistryMirror != nil {
			origin := repo.Registry

			mirror, err := name.NewRegistry(req.Source.RegistryMirror.Host, name.WeakValidation)
			if err != nil {
				logrus.Errorf("could not resolve registry reference: %s", err)
				os.Exit(1)
				return
			}

			repo.Registry = mirror
			*digest = repo.Digest(req.Version.Digest)

			image, err = getWithRetry(req.Source.RegistryMirror.BasicCredentials, *digest)
			if err != nil {
				logrus.Warnf("fetching mirror %s failed: %s", digest.RegistryStr(), err)
			}

			repo.Registry = origin
		}

		if image == nil {
			*digest = repo.Digest(req.Version.Digest)
			image, err = getWithRetry(req.Source.BasicCredentials, *digest)
			if err != nil {
				logrus.Errorf("fetching origin %s failed: %s", digest.RegistryStr(), err)
				os.Exit(1)
				return
			}
		}

		err = saveWithRetry(dest, tag, image, req.Params.Format(), req.Source.Debug)
		if err != nil {
			logrus.Errorf("saving image: %s", err)
			os.Exit(1)
			return
		}
	}

	json.NewEncoder(os.Stdout).Encode(InResponse{
		Version: req.Version,
		Metadata: append(req.Source.Metadata(), resource.MetadataField{
			Name:  "tag",
			Value: tag.TagStr(),
		}),
	})
}

func getWithRetry(principal resource.BasicCredentials, digest name.Digest) (v1.Image, error) {
	var image v1.Image
	err := resource.RetryOnRateLimit(func() error {
		var err error
		image, err = get(principal, digest)
		return err
	})
	return image, err
}

func get(principal resource.BasicCredentials, digest name.Digest) (v1.Image, error) {
	auth := &authn.Basic{
		Username: principal.Username,
		Password: principal.Password,
	}

	imageOpts := []remote.Option{}

	if auth.Username != "" && auth.Password != "" {
		imageOpts = append(imageOpts, remote.WithAuth(auth))
	}

	image, err := remote.Image(digest, imageOpts...)
	if err != nil {
		return nil, fmt.Errorf("locate remote image: %w", err)
	}
	if image == empty.Image {
		return nil, fmt.Errorf("download image")
	}

	return image, err
}

func saveWithRetry(dest string, tag name.Tag, image v1.Image, format string, debug bool) error {
	return resource.RetryOnRateLimit(func() error {
		return save(dest, tag, image, format, debug)
	})
}

func save(dest string, tag name.Tag, image v1.Image, format string, debug bool) error {
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

	err := ioutil.WriteFile(filepath.Join(dest, "tag"), []byte(tag.TagStr()), 0644)
	if err != nil {
		return fmt.Errorf("save image tag: %w", err)
	}

	err = saveDigest(dest, image)
	if err != nil {
		return fmt.Errorf("save image digest: %w", err)
	}

	return err
}

func saveDigest(dest string, image v1.Image) error {
	digest, err := image.Digest()
	if err != nil {
		return fmt.Errorf("get image digest: %w", err)
	}

	digestDest := filepath.Join(dest, "digest")
	return ioutil.WriteFile(digestDest, []byte(digest.String()), 0644)
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
