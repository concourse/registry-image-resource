package resource_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
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
	"github.com/onsi/gomega/ghttp"

	resource "github.com/concourse/registry-image-resource"
)

var _ = Describe("Out", func() {
	var (
		srcDir          string
		actualErr       error
		actualErrOutput string
	)

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
		cmd.Env = []string{"TEST=true"}

		payload, err := json.Marshal(req)
		Expect(err).ToNot(HaveOccurred())

		outBuf := new(bytes.Buffer)
		errBuf := new(bytes.Buffer)

		cmd.Stdin = bytes.NewBuffer(payload)
		cmd.Stdout = outBuf
		cmd.Stderr = io.MultiWriter(GinkgoWriter, errBuf)

		actualErr = cmd.Run()
		actualErrOutput = errBuf.String()
		if actualErr == nil {
			err = json.Unmarshal(outBuf.Bytes(), &res)
			Expect(err).ToNot(HaveOccurred())
		}
	})

	Context("pushing an OCI image tarball", func() {
		var randomImage v1.Image

		BeforeEach(func() {
			req.Source = resource.Source{
				Repository: dockerPushRepo,
				RawTag:     resource.Tag(parallelTag("latest")),

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
			Expect(actualErr).ToNot(HaveOccurred())

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
			Expect(actualErr).ToNot(HaveOccurred())

			Expect(res.Metadata).To(Equal([]resource.MetadataField{
				{
					Name:  "repository",
					Value: dockerPushRepo,
				},
				{
					Name:  "tags",
					Value: parallelTag("latest"),
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
				Expect(actualErr).ToNot(HaveOccurred())

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
				})

				It("exits non-zero and returns an error", func() {
					Expect(actualErr).To(HaveOccurred())
					Expect(actualErrOutput).To(ContainSubstring("too many files match glob"))
				})
			})

			Context("when the glob pattern matches no files", func() {
				BeforeEach(func() {
					req.Params.Image = "nomatch.tar"
				})

				It("exits non-zero and returns an error", func() {
					Expect(actualErr).To(HaveOccurred())
					Expect(actualErrOutput).To(ContainSubstring("no files match glob"))
				})
			})
		})

		Context("with additional_tags (newline separator)", func() {
			BeforeEach(func() {
				req.Params.AdditionalTags = "tags"

				err := ioutil.WriteFile(
					filepath.Join(srcDir, req.Params.AdditionalTags),
					[]byte(fmt.Sprintf("%s\n%s\n", parallelTag("additional"), parallelTag("tags"))),
					0644,
				)
				Expect(err).ToNot(HaveOccurred())
			})

			It("pushes provided tags in addition to the tag in 'source'", func() {
				randomDigest, err := randomImage.Digest()
				Expect(err).ToNot(HaveOccurred())

				for _, t := range []string{"latest", "additional", "tags"} {
					tag := parallelTag(t)

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

		Context("when the registry returns 429 Too Many Requests", func() {
			var registry *ghttp.Server

			BeforeEach(func() {
				registry = ghttp.NewServer()

				layers, err := randomImage.Layers()
				Expect(err).ToNot(HaveOccurred())

				configDigest, err := randomImage.ConfigName()
				Expect(err).ToNot(HaveOccurred())

				pingRateLimits := make(chan struct{}, 1)
				checkBlobRateLimits := make(chan struct{}, 1)
				createUploadRateLimits := make(chan struct{}, 1)
				uploadBlobRateLimits := make(chan struct{}, 1)
				commitBlobRateLimits := make(chan struct{}, 1)
				updateManifestRateLimits := make(chan struct{}, 1)

				registry.RouteToHandler("GET", "/v2/", func(w http.ResponseWriter, r *http.Request) {
					select {
					case pingRateLimits <- struct{}{}:
						ghttp.RespondWith(http.StatusTooManyRequests, "ping limited")(w, r)
					default:
						ghttp.RespondWith(http.StatusOK, "welcome to zombocom")(w, r)
					}
				})

				registry.RouteToHandler("HEAD", "/v2/fake-image/blobs/"+configDigest.String(), func(w http.ResponseWriter, r *http.Request) {
					select {
					case checkBlobRateLimits <- struct{}{}:
						ghttp.RespondWith(http.StatusTooManyRequests, "check config blob limited")(w, r)
					default:
						ghttp.RespondWith(http.StatusOK, "blob totally exists")(w, r)
					}
				})

				registry.RouteToHandler("POST", "/v2/fake-image/blobs/uploads/", func(w http.ResponseWriter, r *http.Request) {
					select {
					case createUploadRateLimits <- struct{}{}:
						ghttp.RespondWith(http.StatusTooManyRequests, "create upload limited")(w, r)
					default:
						w.Header().Add("Location", "/upload/some-blob")
						w.WriteHeader(http.StatusAccepted)
					}
				})

				registry.RouteToHandler("PATCH", "/upload/some-blob", func(w http.ResponseWriter, r *http.Request) {
					select {
					case uploadBlobRateLimits <- struct{}{}:
						ghttp.RespondWith(http.StatusTooManyRequests, "upload blob limited")(w, r)
					default:
						w.Header().Add("Location", "/commit/some-blob")
						w.WriteHeader(http.StatusAccepted)
					}
				})

				registry.RouteToHandler("PUT", "/commit/some-blob", func(w http.ResponseWriter, r *http.Request) {
					select {
					case commitBlobRateLimits <- struct{}{}:
						ghttp.RespondWith(http.StatusTooManyRequests, "commit blob limited")(w, r)
					default:
						ghttp.RespondWith(http.StatusCreated, "upload complete")(w, r)
					}
				})

				for _, l := range layers {
					layerDigest, err := l.Digest()
					Expect(err).ToNot(HaveOccurred())

					registry.RouteToHandler("HEAD", "/v2/fake-image/blobs/"+layerDigest.String(), func(w http.ResponseWriter, r *http.Request) {
						select {
						case checkBlobRateLimits <- struct{}{}:
							ghttp.RespondWith(http.StatusTooManyRequests, "check layer blob limited")(w, r)
						default:
							ghttp.RespondWith(http.StatusNotFound, "needs upload")(w, r)
						}
					})
				}

				registry.RouteToHandler("PUT", "/v2/fake-image/manifests/latest", func(w http.ResponseWriter, r *http.Request) {
					select {
					case updateManifestRateLimits <- struct{}{}:
						ghttp.RespondWith(http.StatusTooManyRequests, "update manifest limited")(w, r)
					default:
						ghttp.RespondWith(http.StatusOK, "manifest updated")(w, r)
					}
				})

				req.Source = resource.Source{
					Repository: registry.Addr() + "/fake-image",
				}
			})

			AfterEach(func() {
				registry.Close()
			})

			It("retries", func() {
				Expect(actualErr).ToNot(HaveOccurred())
			})
		})
	})
})

func parallelTag(tag string) string {
	return fmt.Sprintf("%s-%d", tag, GinkgoParallelNode())
}
