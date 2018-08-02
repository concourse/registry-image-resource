package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	resource "github.com/concourse/registry-image-resource"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/sirupsen/logrus"
)

type InRequest struct {
	Source  resource.Source  `json:"source"`
	Version resource.Version `json:"version"`
}

type InResponse struct {
	Version  resource.Version `json:"version"`
	Metadata []MetadataField  `json:"metadata"`
}

type ImageMetadata struct {
	Env  []string `json:"env"`
	User string   `json:"user"`
}

type MetadataField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func main() {
	logrus.SetOutput(os.Stderr)

	var req InRequest
	err := json.NewDecoder(os.Stdin).Decode(&req)
	if err != nil {
		logrus.Errorf("invalid payload: %s", err)
		os.Exit(1)
		return
	}

	dest := os.Args[1]

	ref := req.Source.Repository + ":" + req.Source.Tag

	n, err := name.ParseReference(ref, name.WeakValidation)
	if err != nil {
		logrus.Errorf("failed to resolve name: %s", err)
		os.Exit(1)
		return
	}

	image, err := remote.Image(n)
	if err != nil {
		logrus.Errorf("Failed to locate remote image: %s", err)
		os.Exit(1)
		return
	}

	err = unpackImage(filepath.Join(dest, "rootfs"), image)
	if err != nil {
		logrus.Errorf("Failed to extract image: %s", err)
		os.Exit(1)
		return
	}

	meta, err := os.Create(filepath.Join(dest, "metadata.json"))
	if err != nil {
		logrus.Errorf("Failed to create image metadata: %s", err)
		os.Exit(1)
		return
	}

	cfg, err := image.ConfigFile()
	if err != nil {
		logrus.Errorf("Failed to inspect image config: %s", err)
		os.Exit(1)
		return
	}

	err = json.NewEncoder(meta).Encode(ImageMetadata{
		Env:  cfg.ContainerConfig.Env,
		User: cfg.ContainerConfig.User,
	})
	if err != nil {
		logrus.Errorf("Failed to write image metadata: %s", err)
		os.Exit(1)
		return
	}

	json.NewEncoder(os.Stdout).Encode(InResponse{
		Version:  req.Version,
		Metadata: []MetadataField{},
	})
}
