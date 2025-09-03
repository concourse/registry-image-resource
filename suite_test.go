package resource_test

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	. "github.com/onsi/ginkgo/v2"
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
const OLDER_STATIC_DIGEST = "sha256:f5e9f512c851ddcd9f1ff7ec2a9fbd65f137acfbb77857405cae7c1611129949"
const LATEST_STATIC_DIGEST = "sha256:f94899a29d502d939de76ddca23da4ce1d8223d201c86f02fcb684ed80a45b5e"

// see testdata/static.tagged/Dockerfile
const LATEST_TAGGED_STATIC_DIGEST = "sha256:61c5cda5ebbd602c732fd74c73626b8a4bb015b95e144238d6f2c091deb187f8"

// see testdata/static.oci-zstd/Dockerfile
const LATEST_ZSTD_STATIC_DIGEST = "sha256:418a7983f3eb1c3781963f06d5ef9be9b208428c9bf84ad0de49e6ce93421e98"

// a pre-configured, static private repo used for testing 'check' and 'in'
var dockerPrivateRepo = os.Getenv("DOCKER_PRIVATE_REPO")
var dockerPrivateUsername = os.Getenv("DOCKER_PRIVATE_USERNAME")
var dockerPrivatePassword = os.Getenv("DOCKER_PRIVATE_PASSWORD")

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
	bytes, err := os.ReadFile(path)
	Expect(err).ToNot(HaveOccurred())
	return string(bytes)
}

type registryTagsResponse struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}
