package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/sirupsen/logrus"

	resource "github.com/concourse/registry-image-resource"
)

type OutRequest struct {
	Source resource.Source    `json:"source"`
	Params resource.PutParams `json:"params"`
}

type OutResponse struct {
	Version  resource.Version         `json:"version"`
	Metadata []resource.MetadataField `json:"metadata"`
}

type imageWithConfigAsLayer struct {
	v1.Image
}

func (i imageWithConfigAsLayer) LayerByDigest(h v1.Hash) (v1.Layer, error) {
	// Support returning the ConfigFile when asked for its hash.
	if cfgName, err := i.ConfigName(); err != nil {
		return nil, err
	} else if cfgName == h {
		return partial.ConfigLayer(i)
	}

	return i.Image.LayerByDigest(h)
}

func main() {
	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors: true,
	})

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

	logrus.Warnln("'put' is experimental, untested, and subject to change!")

	ref := req.Source.Repository + ":" + req.Source.Tag()

	n, err := name.ParseReference(ref, name.WeakValidation)
	if err != nil {
		logrus.Errorf("could not resolve repository/tag reference: %s", err)
		os.Exit(1)
		return
	}

	imagePath := filepath.Join(src, req.Params.Image)

	img, err := tarball.ImageFromPath(imagePath, nil)
	if err != nil {
		logrus.Errorf("could not load image from path '%s': %s", req.Params.Image, err)
		os.Exit(1)
		return
	}

	img = imageWithConfigAsLayer{
		Image: img,
	}

	auth := &authn.Basic{
		Username: req.Source.Username,
		Password: req.Source.Password,
	}

	logrus.Infof("pushing to %s", ref)

	err = remote.Write(n, img, auth, http.DefaultTransport, remote.WriteOptions{})
	if err != nil {
		logrus.Errorf("failed to upload image: %s", err)
		os.Exit(1)
		return
	}

	logrus.Info("pushed")
}
