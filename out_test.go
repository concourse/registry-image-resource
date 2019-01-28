package resource_test

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/concourse/registry-image-resource"
)

var _ = Describe("Out", func() {
	var srcDir string

	var req struct {
		Source resource.Source
		Params resource.PutParams
	}

	var res struct {
		Version  resource.Version
		Metadata []resource.MetadataField
	}

	BeforeEach(func() {
		var err error
		srcDir, err = ioutil.TempDir("", "docker-image-out-dir")
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(srcDir)).To(Succeed())
	})

	JustBeforeEach(func() {
		cmd := exec.Command(bins.Out, srcDir)

		payload, err := json.Marshal(req)
		Expect(err).ToNot(HaveOccurred())

		outBuf := new(bytes.Buffer)

		cmd.Stdin = bytes.NewBuffer(payload)
		cmd.Stdout = outBuf
		cmd.Stderr = GinkgoWriter

		err = cmd.Run()
		Expect(err).ToNot(HaveOccurred())

		err = json.Unmarshal(outBuf.Bytes(), &res)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("pushing an OCI image tarball", func() {
		var randomImage v1.Image

		BeforeEach(func() {
			req.Source = resource.Source{
				Repository: dockerPushRepo,
				Tag:        "latest",

				Username: dockerUsername,
				Password: dockerPassword,
			}

			checkDockerUserConfigured()

			tag, err := name.NewTag(req.Source.Name(), name.WeakValidation)
			Expect(err).ToNot(HaveOccurred())

			randomImage, err = random.Image(1024, 1)
			Expect(err).ToNot(HaveOccurred())

			err = tarball.WriteToFile(filepath.Join(srcDir, "image.tar"), tag, randomImage)
			Expect(err).ToNot(HaveOccurred())

			req.Params.Image = "image.tar"
		})

		It("works", func() {
			name, err := name.ParseReference(req.Source.Name(), name.WeakValidation)
			Expect(err).ToNot(HaveOccurred())

			image, err := remote.Image(name)
			Expect(err).ToNot(HaveOccurred())

			pushedDigest, err := image.Digest()
			Expect(err).ToNot(HaveOccurred())

			randomDigest, err := randomImage.Digest()
			Expect(err).ToNot(HaveOccurred())

			Expect(pushedDigest).To(Equal(randomDigest))
		})

		It("returns metadata", func() {
			Expect(res.Metadata).To(Equal([]resource.MetadataField{
				resource.MetadataField{
					Name:  "repository",
					Value: dockerPushRepo,
				},
				resource.MetadataField{
					Name:  "tag",
					Value: "latest",
				},
			}))
		})
	})
})
