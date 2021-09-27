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

func getRepoOpts(source resource.Source) []name.Option {
	var repoOpts []name.Option
	if source.Insecure {
		repoOpts = append(repoOpts, name.Insecure)
	}
	return repoOpts
}
