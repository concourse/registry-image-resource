package resource_test

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
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

	Context("pushing an OCI image tarball to dockerhub", func() {
		var randomImage v1.Image

		BeforeEach(func() {
			checkDockerPushUserConfigured()

			req.Source = resource.Source{
				Repository: dockerPushRepo,
				Tag:        resource.Tag(parallelTag("latest")),

				BasicCredentials: resource.BasicCredentials{
					Username: dockerPushUsername,
					Password: dockerPushPassword,
				},
			}

			tag, err := name.NewTag(req.Source.Name())
			Expect(err).ToNot(HaveOccurred())

			randomImage, err = random.Image(1024, 1)
			Expect(err).ToNot(HaveOccurred())

			err = tarball.WriteToFile(filepath.Join(srcDir, "image.tar"), tag, randomImage)
			Expect(err).ToNot(HaveOccurred())

			req.Params.Image = "image.tar"
		})

		It("works", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			name, err := name.ParseReference(req.Source.Name())
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

		Context("When using bump_aliases", func() {
			BeforeEach(func() {
				req.Params.BumpAliases = true
				req.Params.Version = "1.0.0"
			})

			It("Push the right tags", func() {
				Expect(actualErr).ToNot(HaveOccurred())

				tags := []string{"1.0.0", "1.0", "1", req.Source.Tag.String()}

				for _, tag := range tags {
					name, err := name.ParseReference(dockerPushRepo + ":" + tag)
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
				}
			})
		})

		Context("when the requested tarball is provided as a glob pattern", func() {
			var randomImage2 v1.Image

			BeforeEach(func() {
				var err error
				randomImage2, err = random.Image(1024, 1)
				Expect(err).ToNot(HaveOccurred())

				tag, err := name.NewTag(req.Source.Name())
				Expect(err).ToNot(HaveOccurred())

				err = tarball.WriteToFile(filepath.Join(srcDir, "image-glob.tar"), tag, randomImage2)
				Expect(err).ToNot(HaveOccurred())

				req.Params.Image = "image-*.tar"
			})

			It("works", func() {
				Expect(actualErr).ToNot(HaveOccurred())

				name, err := name.ParseReference(req.Source.Name())
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

					name, err := name.ParseReference(req.Source.Repository + ":" + tag)
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

	Context("pushing an OCI imageIndex (multi-arch) image to dockerhub", func() {
		var randomImageIndex v1.ImageIndex

		BeforeEach(func() {
			checkDockerPushUserConfigured()

			req.Source = resource.Source{
				Repository: dockerPushRepo,
				Tag:        resource.Tag(parallelTag("latest")),

				BasicCredentials: resource.BasicCredentials{
					Username: dockerPushUsername,
					Password: dockerPushPassword,
				},
			}

			var err error

			randomImageIndex, err = random.Index(1024, 1, 2)
			Expect(err).ToNot(HaveOccurred())

			path := filepath.Join(srcDir, "multi-arch")
			p, err := layout.Write(path, empty.Index)
			Expect(err).ToNot(HaveOccurred())

			err = p.AppendIndex(randomImageIndex)
			Expect(err).ToNot(HaveOccurred())

			req.Params.Image = "multi-arch"
		})

		It("works", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			name, err := name.ParseReference(req.Source.Name())
			Expect(err).ToNot(HaveOccurred())

			auth := &authn.Basic{
				Username: req.Source.Username,
				Password: req.Source.Password,
			}

			index, err := remote.Index(name, remote.WithAuth(auth))
			Expect(err).ToNot(HaveOccurred())

			pushedDigest, err := index.Digest()
			Expect(err).ToNot(HaveOccurred())

			randomDigest, err := randomImageIndex.Digest()
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

		Context("When using bump_aliases", func() {
			BeforeEach(func() {
				req.Params.BumpAliases = true
				req.Params.Version = "1.0.0"
			})

			It("Push the right tags", func() {
				Expect(actualErr).ToNot(HaveOccurred())

				tags := []string{"1.0.0", "1.0", "1", req.Source.Tag.String()}

				for _, tag := range tags {
					name, err := name.ParseReference(dockerPushRepo + ":" + tag)
					Expect(err).ToNot(HaveOccurred())

					auth := &authn.Basic{
						Username: req.Source.Username,
						Password: req.Source.Password,
					}

					index, err := remote.Index(name, remote.WithAuth(auth))
					Expect(err).ToNot(HaveOccurred())

					pushedDigest, err := index.Digest()
					Expect(err).ToNot(HaveOccurred())

					randomDigest, err := randomImageIndex.Digest()
					Expect(err).ToNot(HaveOccurred())

					Expect(pushedDigest).To(Equal(randomDigest))
				}
			})
		})
	})

	Context("when the registry returns 429 Too Many Requests", func() {
		var registry *ghttp.Server
		var randomImage v1.Image

		BeforeEach(func() {
			registry = ghttp.NewServer()

			req.Source = resource.Source{
				Repository: registry.Addr() + "/fake-image",
				Tag:        "some-tag",
			}

			tag, err := name.NewTag(req.Source.Name())
			Expect(err).ToNot(HaveOccurred())

			randomImage, err = random.Image(1024, 1)
			Expect(err).ToNot(HaveOccurred())

			err = tarball.WriteToFile(filepath.Join(srcDir, "image.tar"), tag, randomImage)
			Expect(err).ToNot(HaveOccurred())

			req.Params.Image = "image.tar"

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

			registry.RouteToHandler("HEAD", "/v2/fake-image/manifests/some-tag", func(w http.ResponseWriter, r *http.Request) {
				select {
				case checkBlobRateLimits <- struct{}{}:
					ghttp.RespondWith(http.StatusTooManyRequests, "check layer blob limited")(w, r)
				default:
					ghttp.RespondWith(http.StatusNotFound, "needs upload")(w, r)
				}
			})

			registry.RouteToHandler("PUT", "/v2/fake-image/manifests/some-tag", func(w http.ResponseWriter, r *http.Request) {
				select {
				case updateManifestRateLimits <- struct{}{}:
					ghttp.RespondWith(http.StatusTooManyRequests, "update manifest limited")(w, r)
				default:
					ghttp.RespondWith(http.StatusOK, "manifest updated")(w, r)
				}
			})
		})

		AfterEach(func() {
			registry.Close()
		})

		It("retries", func() {
			Expect(actualErr).ToNot(HaveOccurred())
		})
	})

	Context("using a registry with self-signed certificate", func() {
		var registry *ghttp.Server
		var randomImage v1.Image

		BeforeEach(func() {
			registry = ghttp.NewTLSServer()

			req.Source = resource.Source{
				Repository: registry.Addr() + "/fake-image",
				Tag:        "some-tag",
			}

			tag, err := name.NewTag(req.Source.Name())
			Expect(err).ToNot(HaveOccurred())

			randomImage, err = random.Image(1024, 1)
			Expect(err).ToNot(HaveOccurred())

			err = tarball.WriteToFile(filepath.Join(srcDir, "image.tar"), tag, randomImage)
			Expect(err).ToNot(HaveOccurred())

			req.Params.Image = "image.tar"

			layers, err := randomImage.Layers()
			Expect(err).ToNot(HaveOccurred())

			configDigest, err := randomImage.ConfigName()
			Expect(err).ToNot(HaveOccurred())

			registry.RouteToHandler("GET", "/v2/", func(w http.ResponseWriter, r *http.Request) {
				ghttp.RespondWith(http.StatusOK, "welcome to zombocom")(w, r)
			})

			registry.RouteToHandler("HEAD", "/v2/fake-image/blobs/"+configDigest.String(), func(w http.ResponseWriter, r *http.Request) {
				ghttp.RespondWith(http.StatusOK, "blob totally exists")(w, r)
			})

			registry.RouteToHandler("POST", "/v2/fake-image/blobs/uploads/", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("Location", "/upload/some-blob")
				w.WriteHeader(http.StatusAccepted)
			})

			registry.RouteToHandler("PATCH", "/upload/some-blob", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Add("Location", "/commit/some-blob")
				w.WriteHeader(http.StatusAccepted)
			})

			registry.RouteToHandler("PUT", "/commit/some-blob", func(w http.ResponseWriter, r *http.Request) {
				ghttp.RespondWith(http.StatusCreated, "upload complete")(w, r)

			})

			for _, l := range layers {
				layerDigest, err := l.Digest()
				Expect(err).ToNot(HaveOccurred())

				registry.RouteToHandler("HEAD", "/v2/fake-image/blobs/"+layerDigest.String(), func(w http.ResponseWriter, r *http.Request) {
					ghttp.RespondWith(http.StatusNotFound, "needs upload")(w, r)
				})
			}

			registry.RouteToHandler("HEAD", "/v2/fake-image/manifests/some-tag", func(w http.ResponseWriter, r *http.Request) {
				ghttp.RespondWith(http.StatusNotFound, "needs upload")(w, r)
			})

			registry.RouteToHandler("PUT", "/v2/fake-image/manifests/some-tag", func(w http.ResponseWriter, r *http.Request) {
				ghttp.RespondWith(http.StatusOK, "manifest updated")(w, r)
			})
		})

		AfterEach(func() {
			registry.Close()
		})

		When("the certificate is provided in 'source'", func() {
			BeforeEach(func() {
				certPem := pem.EncodeToMemory(&pem.Block{
					Type:  "CERTIFICATE",
					Bytes: registry.HTTPTestServer.Certificate().Raw,
				})
				Expect(certPem).ToNot(BeEmpty())

				req.Source.DomainCerts = []string{string(certPem)}
			})

			It("should not error", func() {
				Expect(actualErr).ToNot(HaveOccurred())
			})
		})

		When("the certificate is missing in 'source'", func() {
			It("exits non-zero and returns an error", func() {
				Expect(actualErr).To(HaveOccurred())
			})
		})
	})
})

func parallelTag(tag string) string {
	return fmt.Sprintf("%s-%d", tag, GinkgoParallelNode())
}

var _ = DescribeTable("pushing semver tags",
	(SemverTagPushExample).Run,
	Entry("semver tag with no variant",
		SemverTagPushExample{
			Variant: "",
			Version: "1.2.3",

			PushedTags: []string{"1.2.3"},
		},
	),
	Entry("semver tag with variant",
		SemverTagPushExample{
			Variant: "ubuntu",
			Version: "1.2.3",

			PushedTags: []string{"1.2.3-ubuntu"},
		},
	),
	Entry("non-semver tag",
		SemverTagPushExample{
			Variant: "",
			Version: "hoogily-boogily",

			Error: `invalid semantic version: "hoogily-boogily"`,
		},
	),
	Entry("no version provided",
		SemverTagPushExample{
			Variant: "",
			Version: "",

			Error: "no tag specified",
		},
	),
	Entry("bumping aliases with no existing image",
		SemverTagPushExample{
			TagsResponseError: &transport.Error{
				StatusCode: http.StatusNotFound,
				Errors: []transport.Diagnostic{
					{Code: "NAME_UNKNOWN"},
				},
			},

			Variant:     "",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3", "1.2", "1", "latest"},
		},
	),
	Entry("bumping aliases with no existing tags",
		SemverTagPushExample{
			Tags: []string{},

			Variant:     "",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3", "1.2", "1", "latest"},
		},
	),
	Entry("not bumping aliases if a prerelease is given",
		SemverTagPushExample{
			Tags: []string{},

			Variant:     "",
			Version:     "1.2.3-alpha.1",
			BumpAliases: true,

			PushedTags: []string{"1.2.3-alpha.1"},
		},
	),
	Entry("bumping aliases if only older versions exist",
		SemverTagPushExample{
			Tags: []string{"1.2.2"},

			Variant:     "",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3", "1.2", "1", "latest"},
		},
	),
	Entry("not bumping anything if a newer patch already exists",
		SemverTagPushExample{
			Tags: []string{"1.2.4"},

			Variant:     "",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3"},
		},
	),
	Entry("not bumping major if a newer minor already exists",
		SemverTagPushExample{
			Tags: []string{"1.3.0"},

			Variant:     "",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3", "1.2"},
		},
	),
	Entry("bumping minor and major, but not latest, if a newer major version exists",
		SemverTagPushExample{
			Tags: []string{"2.0.0"},

			Variant:     "",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3", "1.2", "1"},
		},
	),
	Entry("bumping everything even if a newer non-variant minor exists",
		// rationale: 'lts' variants, which are intentionally older
		SemverTagPushExample{
			Tags: []string{"1.3.0"},

			Variant:     "lts",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3-lts", "1.2-lts", "1-lts", "lts"},
		},
	),
	Entry("bumping everything if the only available version is a prerelease",
		SemverTagPushExample{
			Tags: []string{"2.0.0-rc.1"},

			Variant:     "",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3", "1.2", "1", "latest"},
		},
	),
	Entry("bumping everything if the only available version is a different variant",
		SemverTagPushExample{
			Tags: []string{"2.0.0-goodbye"},

			Variant:     "hello",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3-hello", "1.2-hello", "1-hello", "hello"},
		},
	),
	Entry("bumping variant aliases with no existing image",
		SemverTagPushExample{
			TagsResponseError: &transport.Error{
				StatusCode: http.StatusNotFound,
				Errors: []transport.Diagnostic{
					{Code: "NOT_FOUND"},
				},
			},

			Variant:     "hello",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3-hello", "1.2-hello", "1-hello", "hello"},
		},
	),
	Entry("bumping variant aliases with no existing tags",
		SemverTagPushExample{
			Tags: []string{},

			Variant:     "hello",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3-hello", "1.2-hello", "1-hello", "hello"},
		},
	),
	Entry("bumping minor and major, but not latest, if a newer major version exists with the same variant",
		SemverTagPushExample{
			Tags: []string{"2.0.0-hello"},

			Variant:     "hello",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3-hello", "1.2-hello", "1-hello"},
		},
	),
	Entry("bumping minor and major, but not latest, if a newer major version exists with the same variant",
		SemverTagPushExample{
			Tags: []string{"2.0.0-hello"},

			Variant:     "hello",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3-hello", "1.2-hello", "1-hello"},
		},
	),
	Entry("bumping aliases if only older versions exist of the same variant",
		SemverTagPushExample{
			Tags: []string{"1.2.2-hello"},

			Variant:     "hello",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3-hello", "1.2-hello", "1-hello", "hello"},
		},
	),
	Entry("not bumping anything if a newer patch already exists of the same variant",
		SemverTagPushExample{
			Tags: []string{"1.2.4-hello"},

			Variant:     "hello",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3-hello"},
		},
	),
	Entry("not bumping major if a newer minor already exists of the same variant",
		SemverTagPushExample{
			Tags: []string{"1.3.0-hello"},

			Variant:     "hello",
			Version:     "1.2.3",
			BumpAliases: true,

			PushedTags: []string{"1.2.3-hello", "1.2-hello"},
		},
	),
)

type SemverTagPushExample struct {
	Tags              []string
	TagsResponseError *transport.Error

	Variant string

	ImageDigest string
	Version     string
	BumpAliases bool

	PushedTags []string
	Error      string
}

func (example SemverTagPushExample) Run() {
	registry := ghttp.NewServer()
	defer registry.Close()

	repo, err := name.NewRepository(fmt.Sprintf("%s/test-image", registry.Addr()))
	Expect(err).ToNot(HaveOccurred())

	digestNames := map[string]string{}

	image, err := random.Image(1024, 1)
	Expect(err).ToNot(HaveOccurred())

	cfgDigest, err := partial.ConfigName(image)
	Expect(err).ToNot(HaveOccurred())

	digest, err := image.Digest()
	Expect(err).ToNot(HaveOccurred())

	layers, err := image.Layers()
	Expect(err).ToNot(HaveOccurred())

	imageDir, err := ioutil.TempDir("", "put-dir")
	Expect(err).ToNot(HaveOccurred())

	defer os.RemoveAll(imageDir)

	imagePath := filepath.Join(imageDir, "image.tar")

	err = tarball.WriteToFile(imagePath, repo.Tag("doesnt-matter"), image)
	Expect(err).ToNot(HaveOccurred())

	digestNames[digest.String()] = example.ImageDigest

	registry.RouteToHandler(
		"GET",
		"/v2/",
		ghttp.RespondWith(http.StatusOK, ""),
	)

	var response http.HandlerFunc
	if example.TagsResponseError == nil {
		response = ghttp.RespondWithJSONEncoded(http.StatusOK, registryTagsResponse{
			Name: "some-name",
			Tags: example.Tags,
		})
	} else {
		response = ghttp.RespondWithJSONEncoded(example.TagsResponseError.StatusCode, example.TagsResponseError)
	}

	registry.RouteToHandler(
		"GET",
		"/v2/"+repo.RepositoryStr()+"/tags/list",
		response,
	)

	registry.RouteToHandler("HEAD", "/v2/test-image/blobs/"+digest.String(), func(w http.ResponseWriter, r *http.Request) {
		ghttp.RespondWith(http.StatusOK, "blob totally exists")(w, r)
	})

	registry.RouteToHandler("HEAD", "/v2/test-image/blobs/"+cfgDigest.String(), func(w http.ResponseWriter, r *http.Request) {
		ghttp.RespondWith(http.StatusOK, "blob totally exists")(w, r)
	})

	for _, l := range layers {
		layerDigest, err := l.Digest()
		Expect(err).ToNot(HaveOccurred())

		registry.RouteToHandler("HEAD", "/v2/test-image/blobs/"+layerDigest.String(), func(w http.ResponseWriter, r *http.Request) {
			ghttp.RespondWith(http.StatusNotFound, "needs upload")(w, r)
		})
	}

	registry.RouteToHandler("POST", "/v2/test-image/blobs/uploads/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Location", "/upload/some-blob")
		w.WriteHeader(http.StatusAccepted)
	})

	registry.RouteToHandler("PATCH", "/upload/some-blob", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Location", "/commit/some-blob")
		w.WriteHeader(http.StatusAccepted)
	})

	registry.RouteToHandler("PUT", "/commit/some-blob", func(w http.ResponseWriter, r *http.Request) {
		ghttp.RespondWith(http.StatusCreated, "upload complete")(w, r)
	})

	pushedTags := new(sync.Map)

	registry.RouteToHandler("HEAD", regexp.MustCompile("/v2/test-image/manifests/.*"), func(w http.ResponseWriter, r *http.Request) {
		ghttp.RespondWith(http.StatusNotFound, "needs upload")(w, r)
	})

	registry.RouteToHandler("PUT", regexp.MustCompile("/v2/test-image/manifests/.*"), func(w http.ResponseWriter, r *http.Request) {
		tag := filepath.Base(r.URL.Path)

		actualDigest, _, err := v1.SHA256(r.Body)
		Expect(err).ToNot(HaveOccurred())
		Expect(actualDigest.String()).To(Equal(digest.String()))

		pushedTags.Store(tag, struct{}{})

		ghttp.RespondWith(http.StatusOK, "manifest updated")(w, r)
	})

	req := resource.OutRequest{
		Source: resource.Source{
			Repository: repo.Name(),
			Variant:    example.Variant,
		},
		Params: resource.PutParams{
			Image:       filepath.Base(imagePath),
			Version:     example.Version,
			BumpAliases: example.BumpAliases,
		},
	}

	res, err := example.put(req, imageDir)
	if example.Error != "" {
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(example.Error))
	} else {
		Expect(err).ToNot(HaveOccurred())

		actualTags := []string{}
		pushedTags.Range(func(key, val interface{}) bool {
			actualTags = append(actualTags, key.(string))
			return true
		})

		Expect(actualTags).To(ConsistOf(example.PushedTags))

		Expect(res.Version.Tag).To(BeElementOf(actualTags))
		Expect(res.Version.Digest).To(Equal(digest.String()))
	}
}

func (example SemverTagPushExample) put(req resource.OutRequest, dir string) (resource.OutResponse, error) {
	cmd := exec.Command(bins.Out, dir)
	cmd.Env = []string{"TEST=true"}

	payload, err := json.Marshal(req)
	Expect(err).ToNot(HaveOccurred())

	outBuf := new(bytes.Buffer)
	errBuf := new(bytes.Buffer)

	cmd.Stdin = bytes.NewBuffer(payload)
	cmd.Stdout = io.MultiWriter(GinkgoWriter, outBuf)
	cmd.Stderr = io.MultiWriter(GinkgoWriter, errBuf)

	err = cmd.Run()
	if err != nil {
		return resource.OutResponse{}, fmt.Errorf("%w\n\nstderr:\n\n%s", err, errBuf)
	}

	Expect(err).ToNot(HaveOccurred())

	var res resource.OutResponse
	err = json.Unmarshal(outBuf.Bytes(), &res)
	Expect(err).ToNot(HaveOccurred())

	return res, nil
}
