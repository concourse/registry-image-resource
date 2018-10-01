package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	resource "github.com/concourse/registry-image-resource"
	color "github.com/fatih/color"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1"
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

	ref := req.Source.Repository + "@" + req.Version.Digest

	n, err := name.ParseReference(ref, name.WeakValidation)
	if err != nil {
		logrus.Errorf("failed to resolve name: %s", err)
		os.Exit(1)
		return
	}

	fmt.Fprintf(os.Stderr, "fetching %s@%s\n", color.GreenString(req.Source.Repository), color.YellowString(req.Version.Digest))

	image, err := remote.Image(n)
	if err != nil {
		logrus.Errorf("failed to locate remote image: %s", err)
		os.Exit(1)
		return
	}

	switch req.Params.Format() {
	case "oci":
		ociFormat(dest, req, image)
	case "rootfs":
		rootfsFormat(dest, req, image)
	}

	json.NewEncoder(os.Stdout).Encode(InResponse{
		Version:  req.Version,
		Metadata: []resource.MetadataField{},
	})
}

func ociFormat(dest string, req InRequest, image v1.Image) {
	tag, err := name.NewTag(req.Source.Repository+":"+req.Source.Tag(), name.WeakValidation)
	if err != nil {
		logrus.Errorf("failed to construct tag reference: %s", err)
		os.Exit(1)
		return
	}

	err = tarball.WriteToFile(filepath.Join(dest, "image.tar"), tag, image, nil)
	if err != nil {
		logrus.Errorf("failed to write OCI image: %s", err)
		os.Exit(1)
		return
	}
}

func rootfsFormat(dest string, req InRequest, image v1.Image) {
	err := unpackImage(filepath.Join(dest, "rootfs"), image, req.Source.Debug)
	if err != nil {
		logrus.Errorf("failed to extract image: %s", err)
		os.Exit(1)
		return
	}

	cfg, err := image.ConfigFile()
	if err != nil {
		logrus.Errorf("failed to inspect image config: %s", err)
		os.Exit(1)
		return
	}

	meta, err := os.Create(filepath.Join(dest, "metadata.json"))
	if err != nil {
		logrus.Errorf("failed to create image metadata: %s", err)
		os.Exit(1)
		return
	}

	env := append(cfg.Config.Env, cfg.ContainerConfig.Env...)
	user := cfg.Config.User
	if user == "" {
		user = cfg.ContainerConfig.User
	}

	err = json.NewEncoder(meta).Encode(ImageMetadata{
		Env:  env,
		User: user,
	})
	if err != nil {
		logrus.Errorf("failed to write image metadata: %s", err)
		os.Exit(1)
		return
	}

	err = meta.Close()
	if err != nil {
		logrus.Errorf("failed to close image metadata file: %s", err)
		os.Exit(1)
		return
	}
}
