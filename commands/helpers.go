package commands

import (
	"io"
	"log"

	resource "github.com/concourse/registry-image-resource"
	"github.com/google/go-containerregistry/pkg/logs"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/sirupsen/logrus"
)

func setupLogging(stderr io.Writer) {
	logrus.SetOutput(stderr)
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors: true,
	})

	logs.Progress = log.New(stderr, "", log.LstdFlags)
	logs.Warn = log.New(stderr, "", log.LstdFlags)
}

func repoOpts(source resource.Source) []name.Option {
	var opts []name.Option
	if source.Insecure {
		opts = append(opts, name.Insecure)
	}
	return opts
}
