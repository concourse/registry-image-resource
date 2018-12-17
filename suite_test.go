package resource_test

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1"
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
const OLDER_STATIC_DIGEST = "sha256:031567a617423a84ad68b62267c30693185bd2b92c2668732efc8c70b036bd3a"
const LATEST_STATIC_DIGEST = "sha256:64a6988c58cbdd634198f56452e8f8945e5b54a4bbca4bff7e960e1c830671ff"

// same Dockerfile, but pushed again (twice; old + latest) to a private repo
const PRIVATE_OLDER_STATIC_DIGEST = "sha256:a5e6442b86fd5f555f528deea32326e9709851f6b18d490d6dfb290c22d6ff52"
const PRIVATE_LATEST_STATIC_DIGEST = "sha256:96c8ddb11d01b236fbf063e5a468d17f4c44ccffa19470471162dbd5bdc922a4"

var privateRepo = os.Getenv("PRIVATE_REPO")
var privateRepoUsername = os.Getenv("PRIVATE_REPO_USERNAME")
var privateRepoPassword = os.Getenv("PRIVATE_REPO_PASSWORD")

func checkPrivateRepoConfigured() {
	if privateRepo == "" || privateRepoUsername == "" || privateRepoPassword == "" {
		Skip("must specify $PRIVATE_REPO, $PRIVATE_REPO_USERNAME, and $PRIVATE_REPO_PASSWORD")
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
