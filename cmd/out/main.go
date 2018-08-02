package main

import (
	"os"

	"github.com/sirupsen/logrus"
)

func main() {
	logrus.SetOutput(os.Stderr)
	logrus.Error("not implemented")
	os.Exit(1)
}
