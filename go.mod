module github.com/concourse/registry-image-resource

require (
	code.cloudfoundry.org/lager v2.0.0+incompatible
	github.com/VividCortex/ewma v1.1.1 // indirect
	github.com/concourse/go-archive v1.0.1
	github.com/concourse/retryhttp v0.0.0-20181126170240-7ab5e29e634f
	github.com/fatih/color v1.7.0
	github.com/google/go-containerregistry v0.0.0-20191018211754-b77a90c667af
	github.com/mattn/go-colorable v0.0.9 // indirect
	github.com/mattn/go-isatty v0.0.4 // indirect
	github.com/onsi/ginkgo v1.10.1
	github.com/onsi/gomega v1.7.0
	github.com/simonshyu/notary-gcr v0.0.0-20191008014436-475bb0dafd9a
	github.com/sirupsen/logrus v1.4.2
	github.com/vbauerster/mpb v3.4.0+incompatible
)

go 1.13
