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

	ref, err := name.ParseReference(req.Source.Repository+"@"+req.Version.Digest, name.WeakValidation)
	if err != nil {
		logrus.Errorf("failed to resolve name: %s", err)
		os.Exit(1)
		return
	}

	fmt.Fprintf(os.Stderr, "fetching %s@%s\n", color.GreenString(req.Source.Repository), color.YellowString(req.Version.Digest))

	err = resource.RetryOnRateLimit(func() error {
		return get(req, ref, dest)
	})
	if err != nil {
		logrus.Errorf("fetching image failed: %s", err)
		os.Exit(1)
		return
	}

	json.NewEncoder(os.Stdout).Encode(InResponse{
		Version:  req.Version,
		Metadata: req.Source.Metadata(),
	})
}

func get(req InRequest, ref name.Reference, dest string) error {
	auth := &authn.Basic{
		Username: req.Source.Username,
		Password: req.Source.Password,
	}

	imageOpts := []remote.Option{}

	if auth.Username != "" && auth.Password != "" {
		imageOpts = append(imageOpts, remote.WithAuth(auth))
	}

	image, err := remote.Image(ref, imageOpts...)
	if err != nil {
		return fmt.Errorf("locate remote image: %w", err)
	}

	switch req.Params.Format() {
	case "oci":
		err := ociFormat(dest, req, image)
		if err != nil {
			return fmt.Errorf("write oci image: %w", err)
		}
	case "rootfs":
		err := rootfsFormat(dest, req, image)
		if err != nil {
			return fmt.Errorf("write rootfs: %w", err)
		}
	}

	err = ioutil.WriteFile(filepath.Join(dest, "tag"), []byte(req.Source.Tag()), 0644)
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

func ociFormat(dest string, req InRequest, image v1.Image) error {
	tag, err := name.NewTag(req.Source.Name(), name.WeakValidation)
	if err != nil {
		return fmt.Errorf("construct tag reference: %w", err)
	}

	err = tarball.WriteToFile(filepath.Join(dest, "image.tar"), tag, image)
	if err != nil {
		return fmt.Errorf("write OCI image: %s", err)
	}

	return nil
}

func rootfsFormat(dest string, req InRequest, image v1.Image) error {
	err := unpackImage(filepath.Join(dest, "rootfs"), image, req.Source.Debug)
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
