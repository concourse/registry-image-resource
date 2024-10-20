package resource_test

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
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

// sha256 of {"fake":"outdated"} and {"fake":"manifest"}
const OLDER_FAKE_DIGEST = "sha256:f5361183777fc8973760829d7cd24c37e3fab6d86c8fe6ae42851c305805c01b"
const LATEST_FAKE_DIGEST = "sha256:c4c25c2cd70e3071f08cf124c4b5c656c061dd38247d166d97098d58eeea8aa6"

var OLDER_FAKE_HEADERS = http.Header{
	"Docker-Content-Digest": {OLDER_FAKE_DIGEST},
	"Content-Length":        {strconv.Itoa(len(`{"fake":"manifest"}`))},
	"Content-Type":          {"dummy"},
}

var LATEST_FAKE_HEADERS = http.Header{
	"Docker-Content-Digest": {LATEST_FAKE_DIGEST},
	"Content-Length":        {strconv.Itoa(len(`{"fake":"outdated"}`))},
	"Content-Type":          {"dummy"},
}

const OLDER_LIBRARY_DIGEST = "sha256:2131f09e4044327fd101ca1fd4043e6f3ad921ae7ee901e9142e6e36b354a907"

// see testdata/static/Dockerfile
const OLDER_STATIC_DIGEST = "sha256:7dabedca9d367a71d1cd646bd8d79f14de7b07327e4417ab691f5f13be5647a9"
const LATEST_STATIC_DIGEST = "sha256:6d89d782e9c924098af48daa6af692d14aba9f4a92338ea04603d99ef68395df"

// see testdata/static.tagged/Dockerfile
const LATEST_TAGGED_STATIC_DIGEST = "sha256:ecfdc2527b0a5d7d134be55234590336209e7feafc2ec364a930adf4a9c722e2"

// a pre-configured, static private repo used for testing 'check' and 'in'
var dockerPrivateRepo = os.Getenv("DOCKER_PRIVATE_REPO")
var dockerPrivateUsername = os.Getenv("DOCKER_PRIVATE_USERNAME")
var dockerPrivatePassword = os.Getenv("DOCKER_PRIVATE_PASSWORD")

// testdata/static/Dockerfile, but pushed again (twice; old + latest) to the above private repo
const PRIVATE_OLDER_STATIC_DIGEST = "sha256:f30102682603ad47bb0a83270c82e9314f01a30054398ab803e718bd13304645"
const PRIVATE_LATEST_STATIC_DIGEST = "sha256:5b56fbe8463395bc9743ddc7799ea793cfbf819041a8eda4c1b87b376620add5"

// a repo to which random images will be pushed when testing 'out'
var dockerPushRepo = os.Getenv("DOCKER_PUSH_REPO")
var dockerPushUsername = os.Getenv("DOCKER_PUSH_USERNAME")
var dockerPushPassword = os.Getenv("DOCKER_PUSH_PASSWORD")

func checkDockerPrivateUserConfigured() {
	if dockerPrivateRepo == "" || dockerPrivateUsername == "" || dockerPrivatePassword == "" {
		Skip("must specify $DOCKER_PRIVATE_REPO, $DOCKER_PRIVATE_USERNAME, and $DOCKER_PRIVATE_PASSWORD")
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
	n, err := name.ParseReference(ref)
	Expect(err).ToNot(HaveOccurred())

	desc, err := remote.Head(n)
	Expect(err).ToNot(HaveOccurred())

	return desc.Digest.String()
}

func latestManifest(ref string) (string, *v1.Manifest) {
	n, err := name.ParseReference(ref)
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

type registryTagsResponse struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}
