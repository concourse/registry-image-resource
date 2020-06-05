package resource_test

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

var bins struct {
	In    string `json:"in"`
	Out   string `json:"out"`
	Check string `json:"check"`
}

// see testdata/static/Dockerfile
const OLDER_STATIC_DIGEST = "sha256:7dabedca9d367a71d1cd646bd8d79f14de7b07327e4417ab691f5f13be5647a9"
const LATEST_STATIC_DIGEST = "sha256:fc484c7e21a5616c600778d7ee720b3adfe1373c896be4f068c3da4a205d4a2e"

// see testdata/static.tagged/Dockerfile
const LATEST_TAGGED_STATIC_DIGEST = "sha256:ecfdc2527b0a5d7d134be55234590336209e7feafc2ec364a930adf4a9c722e2"

// a pre-configured, static private repo used for testing 'check' and 'in'
var dockerPrivateRepo = os.Getenv("DOCKER_PRIVATE_REPO")
var dockerPrivateUsername = os.Getenv("DOCKER_PRIVATE_USERNAME")
var dockerPrivatePassword = os.Getenv("DOCKER_PRIVATE_PASSWORD")

// a pre-configured, private repo used for tag filtering
var dockerTagFilterPrivateRepo = os.Getenv("DOCKER_TAG_FILTER_PRIVATE_REPO")
var dockerTagFilterPrivateUsername = os.Getenv("DOCKER_TAG_FILTER_PRIVATE_USERNAME")
var dockerTagFilterPrivatePassword = os.Getenv("DOCKER_TAG_FILTER_PRIVATE_PASSWORD")
var dockerTagFilterExpectedDigest = os.Getenv("DOCKER_TAG_FILTER_EXPECTED_DIGEST")

// testdata/static/Dockerfile, but pushed again (twice; old + latest) to the above private repo
const PRIVATE_OLDER_STATIC_DIGEST = "sha256:4003f4c3fad6024467f7e59d8153c4c1ef3e2a749d0f234a326cd0ea6bd31359"
const PRIVATE_LATEST_STATIC_DIGEST = "sha256:2374201198a54c35ad03124f1218bb553eaa97368ce8d2359b43e0bc3b17e06f"

// a repo to which random images will be pushed when testing 'out'
var dockerPushRepo = os.Getenv("DOCKER_PUSH_REPO")
var dockerPushUsername = os.Getenv("DOCKER_PUSH_USERNAME")
var dockerPushPassword = os.Getenv("DOCKER_PUSH_PASSWORD")

func checkDockerPrivateUserConfigured() {
	if dockerPrivateRepo == "" || dockerPrivateUsername == "" || dockerPrivatePassword == "" {
		Skip("must specify $DOCKER_PRIVATE_REPO, $DOCKER_PRIVATE_USERNAME, and $DOCKER_PRIVATE_PASSWORD")
	}
}

func checkDockerTagFilterPrivateUserConfigured() {
	if dockerTagFilterPrivateRepo == "" || dockerTagFilterPrivateUsername == "" || dockerTagFilterPrivatePassword == "" {
		Skip("must specify $DOCKER_TAG_FILTER_PRIVATE_REPO, $DOCKER_TAG_FILTER_PRIVATE_USERNAME, $DOCKER_TAG_FILTER_PRIVATE_PASSWORD, and $DOCKER_TAG_FILTER_EXPECTED_DIGEST")
	}
}

func checkDockerPushUserConfigured() {
	if dockerPushRepo == "" || dockerPushUsername == "" || dockerPushPassword == "" {
		Skip("must specify $DOCKER_PUSH_REPO, $DOCKER_PUSH_USERNAME, and $DOCKER_PUSH_PASSWORD")
	}
}

var _ = SynchronizedBeforeSuite(func() []byte {
	var err error

	b := bins

	if _, err := os.Stat("/opt/resource/in"); err == nil {
		b.In = "/opt/resource/in"
	} else {
		b.In, err = gexec.Build("github.com/concourse/registry-image-resource/cmd/in")
		Expect(err).ToNot(HaveOccurred())
	}

	if _, err := os.Stat("/opt/resource/out"); err == nil {
		b.Out = "/opt/resource/out"
	} else {
		b.Out, err = gexec.Build("github.com/concourse/registry-image-resource/cmd/out")
		Expect(err).ToNot(HaveOccurred())
	}

	if _, err := os.Stat("/opt/resource/check"); err == nil {
		b.Check = "/opt/resource/check"
	} else {
		b.Check, err = gexec.Build("github.com/concourse/registry-image-resource/cmd/check")
		Expect(err).ToNot(HaveOccurred())
	}

	j, err := json.Marshal(b)
	Expect(err).ToNot(HaveOccurred())

	return j
}, func(bp []byte) {
	err := json.Unmarshal(bp, &bins)
	Expect(err).ToNot(HaveOccurred())
})

var _ = SynchronizedAfterSuite(func() {
}, func() {
	gexec.CleanupBuildArtifacts()
})

func TestRegistryImageResource(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "RegistryImageResource Suite")
}

func latestDigest(ref string) string {
	n, err := name.ParseReference(ref, name.WeakValidation)
	Expect(err).ToNot(HaveOccurred())

	image, err := remote.Image(n)
	Expect(err).ToNot(HaveOccurred())

	digest, err := image.Digest()
	Expect(err).ToNot(HaveOccurred())

	return digest.String()
}

func latestManifest(ref string) (string, *v1.Manifest) {
	n, err := name.ParseReference(ref, name.WeakValidation)
	Expect(err).ToNot(HaveOccurred())

	image, err := remote.Image(n)
	Expect(err).ToNot(HaveOccurred())

	manifest, err := image.Manifest()
	Expect(err).ToNot(HaveOccurred())

	digest, err := image.Digest()
	Expect(err).ToNot(HaveOccurred())

	return digest.String(), manifest
}

func cat(path string) string {
	bytes, err := ioutil.ReadFile(path)
	Expect(err).ToNot(HaveOccurred())
	return string(bytes)
}
