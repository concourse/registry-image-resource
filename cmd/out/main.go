package main

import (
	"os"

	"github.com/sirupsen/logrus"
)

func main() {
	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors: true,
	})

	logrus.Error("not implemented")
	os.Exit(1)
}
