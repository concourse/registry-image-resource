module github.com/concourse/registry-image-resource

require (
	github.com/Masterminds/semver v1.5.0
	github.com/VividCortex/ewma v1.1.1 // indirect
	github.com/aws/aws-sdk-go v1.35.0
	github.com/cenkalti/backoff v2.2.1+incompatible
	github.com/concourse/go-archive v1.0.1
	github.com/fatih/color v1.13.0
	github.com/google/go-containerregistry v0.8.0
	github.com/onsi/ginkgo v1.14.1
	github.com/onsi/gomega v1.10.3
	github.com/simonshyu/notary-gcr v0.0.0-20211109021545-380a129b0e83
	github.com/sirupsen/logrus v1.8.1
	github.com/vbauerster/mpb v3.4.0+incompatible
)

replace github.com/simonshyu/notary-gcr => github.com/xtremerui/notary-gcr v0.0.0-20220307174448-84487b5997d2

go 1.16
