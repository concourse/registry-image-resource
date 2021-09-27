package commands

import (
	"io"
	"log"

	"github.com/google/go-containerregistry/pkg/logs"
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
