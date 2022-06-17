module github.com/concourse/registry-image-resource

require (
	github.com/Masterminds/semver v1.5.0
	github.com/VividCortex/ewma v1.1.1 // indirect
	github.com/aws/aws-sdk-go v1.44.5
	github.com/cenkalti/backoff v2.2.1+incompatible
	github.com/concourse/go-archive v1.0.1
	github.com/fatih/color v1.13.0
	github.com/go-sql-driver/mysql v1.5.0 // indirect
	github.com/google/go-containerregistry v0.8.0
	github.com/onsi/ginkgo v1.16.4
	github.com/onsi/gomega v1.19.0
	github.com/simonshyu/notary-gcr v0.0.0-20220601090547-d99a631aa58b
	github.com/sirupsen/logrus v1.8.1
	github.com/vbauerster/mpb v3.4.0+incompatible
)

go 1.16
