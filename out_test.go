package resource_test

import (
	"bytes"
	"encoding/json"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	resource "github.com/concourse/registry-image-resource"
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

	var expectedErr string

	BeforeEach(func() {
		var err error
		srcDir, err = ioutil.TempDir("", "docker-image-out-dir")
		Expect(err).ToNot(HaveOccurred())

		req.Source = resource.Source{}
		req.Params = resource.PutParams{}

		res.Version = resource.Version{}
		res.Metadata = nil
	})

	AfterEach(func() {
		Expect(os.RemoveAll(srcDir)).To(Succeed())
	})

	JustBeforeEach(func() {
		cmd := exec.Command(bins.Out, srcDir)

		payload, err := json.Marshal(req)
		Expect(err).ToNot(HaveOccurred())

		outBuf := new(bytes.Buffer)
		errBuf := new(bytes.Buffer)

		cmd.Stdin = bytes.NewBuffer(payload)
		cmd.Stdout = outBuf
		cmd.Stderr = io.MultiWriter(GinkgoWriter, errBuf)

		err = cmd.Run()
		if len(expectedErr) == 0 {
			Expect(err).ToNot(HaveOccurred())
			err = json.Unmarshal(outBuf.Bytes(), &res)
			Expect(err).ToNot(HaveOccurred())
		} else {
			Expect(err).To(HaveOccurred())
			Expect(errBuf.String()).To(ContainSubstring(expectedErr))
		}
	})

	Context("pushing an OCI image tarball", func() {
		var randomImage v1.Image

		BeforeEach(func() {
			req.Source = resource.Source{
				Repository: dockerPushRepo,
				RawTag:     "latest",

				Username: dockerPushUsername,
				Password: dockerPushPassword,
			}

			checkDockerPushUserConfigured()

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

			auth := &authn.Basic{
				Username: req.Source.Username,
				Password: req.Source.Password,
			}

			image, err := remote.Image(name, remote.WithAuth(auth))
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
					Name:  "tags",
					Value: "latest",
				},
			}))
		})

		Context("when the requested tarball is provided as a glob pattern", func() {
			var randomImage2 v1.Image

			BeforeEach(func() {
				var err error
				randomImage2, err = random.Image(1024, 1)
				Expect(err).ToNot(HaveOccurred())

				tag, err := name.NewTag(req.Source.Name(), name.WeakValidation)
				Expect(err).ToNot(HaveOccurred())

				err = tarball.WriteToFile(filepath.Join(srcDir, "image-glob.tar"), tag, randomImage2)
				Expect(err).ToNot(HaveOccurred())

				req.Params.Image = "image-*.tar"
			})

			It("works", func() {
				name, err := name.ParseReference(req.Source.Name(), name.WeakValidation)
				Expect(err).ToNot(HaveOccurred())

				auth := &authn.Basic{
					Username: req.Source.Username,
					Password: req.Source.Password,
				}

				image, err := remote.Image(name, remote.WithAuth(auth))
				Expect(err).ToNot(HaveOccurred())

				pushedDigest, err := image.Digest()
				Expect(err).ToNot(HaveOccurred())

				randomDigest, err := randomImage2.Digest()
				Expect(err).ToNot(HaveOccurred())

				Expect(pushedDigest).To(Equal(randomDigest))
			})

			Context("when the glob pattern matches more than one file", func() {
				BeforeEach(func() {
					req.Params.Image = "imag*.tar"
					expectedErr = "too many files match glob"
				})

				It("exits non-zero and returns an error", func() {
					name, err := name.ParseReference(req.Source.Name(), name.WeakValidation)
					Expect(err).ToNot(HaveOccurred())

					auth := &authn.Basic{
						Username: req.Source.Username,
						Password: req.Source.Password,
					}

					_, err = remote.Image(name, remote.WithAuth(auth))
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("when the glob pattern matches no files", func() {
				BeforeEach(func() {
					req.Params.Image = "nomatch.tar"
					expectedErr = "no files match glob"
				})

				It("exits non-zero and returns an error", func() {
					name, err := name.ParseReference(req.Source.Name(), name.WeakValidation)
					Expect(err).ToNot(HaveOccurred())

					auth := &authn.Basic{
						Username: req.Source.Username,
						Password: req.Source.Password,
					}

					_, err = remote.Image(name, remote.WithAuth(auth))
					Expect(err).ToNot(HaveOccurred())
				})
			})
		})

		Context("with additional_tags (newline separator)", func() {

			BeforeEach(func() {
				req.Params.AdditionalTags = "tags"

				err := ioutil.WriteFile(
					filepath.Join(srcDir, req.Params.AdditionalTags),
					[]byte("additional\ntags\n"),
					0644,
				)
				Expect(err).ToNot(HaveOccurred())
			})

			It("pushes provided tags in addition to the tag in 'source'", func() {
				randomDigest, err := randomImage.Digest()
				Expect(err).ToNot(HaveOccurred())

				for _, tag := range []string{"latest", "additional", "tags"} {
					name, err := name.ParseReference(req.Source.Repository+":"+tag, name.WeakValidation)
					Expect(err).ToNot(HaveOccurred())

					auth := &authn.Basic{
						Username: req.Source.Username,
						Password: req.Source.Password,
					}

					image, err := remote.Image(name, remote.WithAuth(auth))
					Expect(err).ToNot(HaveOccurred())

					pushedDigest, err := image.Digest()
					Expect(err).ToNot(HaveOccurred())

					Expect(pushedDigest).To(Equal(randomDigest))
				}
			})
		})
	})
})
