package resource_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"

	resource "github.com/concourse/registry-image-resource"
	"github.com/concourse/registry-image-resource/commands"
)

var _ = Describe("In", func() {
	var (
		actualErr error
		destDir   string
	)

	var req struct {
		Source  resource.Source    `json:"source"`
		Params  resource.GetParams `json:"params"`
		Version resource.Version   `json:"version"`
	}

	var res struct {
		Version  resource.Version         `json:"version"`
		Metadata []resource.MetadataField `json:"metadata"`
	}

	rootfsPath := func(path ...string) string {
		return filepath.Join(append([]string{destDir, "rootfs"}, path...)...)
	}

	BeforeEach(func() {
		var err error
		destDir, err = os.MkdirTemp("", "docker-image-in-dir")
		Expect(err).ToNot(HaveOccurred())

		req.Source = resource.Source{}
		req.Params = resource.GetParams{}
		req.Version = resource.Version{}

		res.Version = resource.Version{}
		res.Metadata = nil
	})

	AfterEach(func() {
		Expect(os.RemoveAll(destDir)).To(Succeed())
	})

	JustBeforeEach(func() {
		cmd := exec.Command(bins.In, destDir)
		cmd.Env = []string{"TEST=true"}

		payload, err := json.Marshal(req)
		Expect(err).ToNot(HaveOccurred())

		outBuf := new(bytes.Buffer)

		cmd.Stdin = bytes.NewBuffer(payload)
		cmd.Stdout = outBuf
		cmd.Stderr = GinkgoWriter

		actualErr = cmd.Run()
		if actualErr == nil {
			err = json.Unmarshal(outBuf.Bytes(), &res)
			Expect(err).ToNot(HaveOccurred())
		}
	})

	Describe("image metadata", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-metadata"
			req.Version = resource.Version{
				Tag:    "latest",
				Digest: latestDigest(req.Source.Repository),
			}
		})

		It("captures the env and user", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			var meta struct {
				User string   `json:"user"`
				Env  []string `json:"env"`
			}

			md, err := os.Open(filepath.Join(destDir, "metadata.json"))
			Expect(err).ToNot(HaveOccurred())

			defer md.Close()

			json.NewDecoder(md).Decode(&meta)
			Expect(meta.User).To(Equal("someuser"))
			Expect(meta.Env).To(Equal([]string{
				"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
				"FOO=1",
			}))
		})
	})

	Describe("response metadata", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-metadata"
			req.Version = resource.Version{
				Tag:    "latest",
				Digest: latestDigest(req.Source.Repository),
			}
		})

		It("returns metadata", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			Expect(res.Version).To(Equal(req.Version))
			Expect(res.Metadata).To(Equal([]resource.MetadataField{
				{
					Name:  "repository",
					Value: "concourse/test-image-metadata",
				},
				{
					Name:  "tag",
					Value: "latest",
				},
			}))
		})
	})

	Describe("file attributes", func() {
		var stat os.FileInfo

		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-file-perms-mtime"
			req.Version = resource.Version{
				Tag:    "latest",
				Digest: latestDigest(req.Source.Repository),
			}
		})

		JustBeforeEach(func() {
			var err error
			stat, err = os.Stat(rootfsPath("home", "alex", "birthday"))
			Expect(err).ToNot(HaveOccurred())
		})

		It("keeps file permissions and file modified times", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			Expect(stat.Mode()).To(Equal(os.FileMode(0603)))
			Expect(stat.ModTime()).To(BeTemporally("==", time.Date(1991, 06, 03, 05, 30, 30, 0, time.UTC)))
		})

		It("keeps file ownership", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			if os.Geteuid() != 0 {
				Skip("Must be run as root to validate file ownership")
			}

			sys, ok := stat.Sys().(*syscall.Stat_t)
			Expect(ok).To(BeTrue())
			Expect(sys.Uid).To(Equal(uint32(1000)))
			Expect(sys.Gid).To(Equal(uint32(1000)))
		})
	})

	Describe("removed files in layers", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-whiteout"
			req.Version = resource.Version{
				Tag:    "latest",
				Digest: latestDigest(req.Source.Repository),
			}
		})

		It("does not restore files that were removed in later layers", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			infos, err := os.ReadDir(rootfsPath("top-dir-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(infos).To(HaveLen(2))

			stat, err := os.Stat(rootfsPath("top-dir-1", "nested-file"))
			Expect(err).ToNot(HaveOccurred())
			Expect(stat.IsDir()).To(BeFalse())

			stat, err = os.Stat(rootfsPath("top-dir-1", "nested-dir"))
			Expect(err).ToNot(HaveOccurred())
			Expect(stat.IsDir()).To(BeTrue())

			infos, err = os.ReadDir(rootfsPath("top-dir-1", "nested-dir"))
			Expect(err).ToNot(HaveOccurred())
			Expect(infos).To(HaveLen(3))

			stat, err = os.Stat(rootfsPath("top-dir-1", "nested-dir", "file-gone"))
			Expect(err).To(HaveOccurred())

			stat, err = os.Stat(rootfsPath("top-dir-1", "nested-dir", "file-here"))
			Expect(err).ToNot(HaveOccurred())
			Expect(stat.IsDir()).To(BeFalse())

			stat, err = os.Stat(rootfsPath("top-dir-1", "nested-dir", "file-recreated"))
			Expect(err).ToNot(HaveOccurred())
			Expect(stat.IsDir()).To(BeFalse())

			stat, err = os.Stat(rootfsPath("top-dir-1", "nested-dir", "file-then-dir"))
			Expect(err).ToNot(HaveOccurred())
			Expect(stat.IsDir()).To(BeTrue())

			stat, err = os.Stat(rootfsPath("top-dir-2"))
			Expect(err).To(HaveOccurred())

			infos, err = os.ReadDir(rootfsPath("top-dir-3"))
			Expect(err).ToNot(HaveOccurred())
			Expect(infos).To(HaveLen(1))

			stat, err = os.Stat(rootfsPath("top-dir-3", "nested-dir"))
			Expect(err).ToNot(HaveOccurred())
			Expect(stat.IsDir()).To(BeTrue())

			stat, err = os.Stat(rootfsPath("top-dir-4"))
			Expect(err).ToNot(HaveOccurred())
			Expect(stat.IsDir()).To(BeTrue())

			if os.Geteuid() != 0 {
				Skip("Must be run as root to validate file ownership")
			}
			sys, ok := stat.Sys().(*syscall.Stat_t)
			Expect(ok).To(BeTrue())
			Expect(sys.Uid).To(Equal(uint32(1000)))
			Expect(sys.Gid).To(Equal(uint32(1000)))
		})
	})

	Describe("a hardlink that is later removed", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-removed-hardlinks"
			req.Version = resource.Version{
				Tag:    "latest",
				Digest: latestDigest(req.Source.Repository),
			}
		})

		It("works", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			lstat, err := os.Lstat(rootfsPath("hardlink-test", "hardlink-file"))
			Expect(err).ToNot(HaveOccurred())
			Expect(lstat.Mode() & os.ModeSymlink).To(BeZero())

			stat, err := os.Stat(rootfsPath("hardlink-test", "hardlink-file"))
			Expect(err).ToNot(HaveOccurred())
			Expect(stat.Mode() & os.ModeSymlink).To(BeZero())
		})
	})

	Describe("layers that replace symlinks with regular files", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-symlinks"
			req.Version = resource.Version{
				Tag:    "latest",
				Digest: latestDigest(req.Source.Repository),
			}
		})

		It("removes the symlink and writes to a new file rather than trying to open and write to it (thereby overwriting its target)", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			Expect(cat(rootfsPath("a"))).To(Equal("symlinked\n"))
			Expect(cat(rootfsPath("b"))).To(Equal("replaced\n"))
		})
	})

	Describe("fetching from a private repository with credentials", func() {
		BeforeEach(func() {
			req.Source = resource.Source{
				Repository: dockerPrivateRepo,
				Tag:        "latest",

				BasicCredentials: resource.BasicCredentials{
					Username: dockerPrivateUsername,
					Password: dockerPrivatePassword,
				},
			}

			checkDockerPrivateUserConfigured()

			req.Version = resource.Version{
				Tag:    "latest",
				Digest: PRIVATE_LATEST_STATIC_DIGEST,
			}
		})

		It("works", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			Expect(cat(rootfsPath("Dockerfile"))).To(ContainSubstring("hello!"))
		})
	})

	Describe("fetching in OCI format", func() {
		var manifest *v1.Manifest

		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-static"
			req.Params.RawFormat = "oci"

			req.Version.Tag = "latest"
			req.Version.Digest, manifest = latestManifest(req.Source.Repository)
		})

		It("saves the tagged image as image.tar instead of saving the rootfs", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			_, err := os.Stat(filepath.Join(destDir, "rootfs"))
			Expect(os.IsNotExist(err)).To(BeTrue())

			_, err = os.Stat(filepath.Join(destDir, "manifest.json"))
			Expect(os.IsNotExist(err)).To(BeTrue())

			tag, err := name.NewTag("concourse/test-image-static:latest")
			Expect(err).ToNot(HaveOccurred())

			img, err := tarball.ImageFromPath(filepath.Join(destDir, "image.tar"), &tag)
			Expect(err).ToNot(HaveOccurred())

			fetchedManifest, err := img.Manifest()
			Expect(err).ToNot(HaveOccurred())

			// cannot assert against digest because the saved image's manifest isn't
			// JSON-prettified, so it has a different sha256. so just assert against
			// digest within manifest, which is what ends up being the 'image id'
			// anyway.
			Expect(fetchedManifest.Config.Digest).To(Equal(manifest.Config.Digest))
		})
	})

	Describe("fetching index image in OCI layout format", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-static"
			req.Params.RawFormat = commands.OciLayoutFormatName

			req.Version.Tag = "latest"
			req.Version.Digest = LATEST_STATIC_DIGEST // this is a modern image index hash (e.g. it has multiple architectures)
		})

		It("saves the tagged image in oci/", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			_, err := os.Stat(filepath.Join(destDir, commands.OciLayoutDirName, "oci-layout"))
			Expect(err).ToNot(HaveOccurred())

			// for modern images, this file is not written out
			_, err = os.Stat(filepath.Join(destDir, commands.OciLayoutDirName, commands.OciLayoutSingleImageDigestFileName))
			Expect(os.IsNotExist(err)).To(BeTrue())

			img, err := commands.NewIndexImageFromPath(filepath.Join(destDir, commands.OciLayoutDirName))
			Expect(err).ToNot(HaveOccurred())

			imgDigest, err := img.Digest()
			Expect(err).ToNot(HaveOccurred())
			Expect(imgDigest.String()).To(Equal(req.Version.Digest))

			// for an image index, the hash of index.json should match the requested digest
			indexJson, err := os.ReadFile(filepath.Join(destDir, commands.OciLayoutDirName, "index.json"))
			Expect(err).ToNot(HaveOccurred())

			indexHash := sha256.Sum256(indexJson)
			Expect("sha256:" + hex.EncodeToString(indexHash[:])).To(Equal(req.Version.Digest))
		})
	})

	Describe("saving the digest", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-static"
			req.Version.Tag = "latest"
			req.Version.Digest = LATEST_STATIC_DIGEST
		})

		It("saves the digest to a file", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			digest, err := os.ReadFile(filepath.Join(destDir, "digest"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(digest)).To(Equal(req.Version.Digest))
		})
	})

	Describe("saving the tag", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-static"
			req.Version = resource.Version{
				Tag:    "tagged",
				Digest: LATEST_STATIC_DIGEST,
			}
		})

		It("saves the tag to a file", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			tag, err := os.ReadFile(filepath.Join(destDir, "tag"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(tag)).To(Equal("tagged"))
		})
	})

	Describe("saving the repository", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-static"
			req.Version.Tag = "latest"
			req.Version.Digest = LATEST_STATIC_DIGEST
		})

		It("saves the repository string to a file", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			repository, err := os.ReadFile(filepath.Join(destDir, "repository"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(repository)).To(Equal("concourse/test-image-static"))
		})
	})

	Describe("skipping the download", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-static"
			req.Params.SkipDownload = true
			req.Version.Tag = "latest"
			req.Version.Digest = LATEST_STATIC_DIGEST
		})

		It("does not download the image", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			_, err := os.Stat(filepath.Join(destDir, "rootfs"))
			Expect(os.IsNotExist(err)).To(BeTrue())

			_, err = os.Stat(filepath.Join(destDir, "manifest.json"))
			Expect(os.IsNotExist(err)).To(BeTrue())

			_, err = os.Stat(filepath.Join(destDir, "image.tar"))
			Expect(os.IsNotExist(err)).To(BeTrue())
		})

		It("saves the tag and digest files", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			digest, err := os.ReadFile(filepath.Join(destDir, "digest"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(digest)).To(Equal(LATEST_STATIC_DIGEST))

			tag, err := os.ReadFile(filepath.Join(destDir, "tag"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(tag)).To(Equal("latest"))
		})
	})

	Context("when the registry returns 429 Too Many Requests", func() {
		var registry *ghttp.Server

		BeforeEach(func() {
			registry = ghttp.NewServer()

			fakeImage := empty.Image

			digest, err := fakeImage.Digest()
			Expect(err).ToNot(HaveOccurred())

			manifest, err := fakeImage.RawManifest()
			Expect(err).ToNot(HaveOccurred())

			configDigest, err := fakeImage.ConfigName()
			Expect(err).ToNot(HaveOccurred())

			config, err := fakeImage.RawConfigFile()
			Expect(err).ToNot(HaveOccurred())

			registry.AppendHandlers(
				// immediate 429 on transport setup
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/"),
					ghttp.RespondWith(http.StatusTooManyRequests, "calm down"),
				),

				// 429 following transport setup
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/"),
					ghttp.RespondWith(http.StatusTooManyRequests, "calm down"),
				),

				// 429 on manifest fetch
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/"),
					ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
				),
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/fake-image/manifests/"+digest.String()),
					ghttp.RespondWith(http.StatusTooManyRequests, "calm down"),
				),

				// 429 on blob fetch
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/"),
					ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
				),
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/fake-image/manifests/"+digest.String()),
					ghttp.RespondWith(http.StatusOK, manifest),
				),
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/fake-image/blobs/"+configDigest.String()),
					ghttp.RespondWith(http.StatusTooManyRequests, "calm down"),
				),

				// successful sequence
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/"),
					ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
				),
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/fake-image/manifests/"+digest.String()),
					ghttp.RespondWith(http.StatusOK, manifest),
				),
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/fake-image/blobs/"+configDigest.String()),
					ghttp.RespondWith(http.StatusOK, config),
				),
			)

			req.Source = resource.Source{
				Repository: registry.Addr() + "/fake-image",
			}

			req.Version.Tag = "latest"
			req.Version.Digest = digest.String()
		})

		AfterEach(func() {
			registry.Close()
		})

		It("retries", func() {
			Expect(actualErr).ToNot(HaveOccurred())

			Expect(res.Version).To(Equal(req.Version))
		})
	})

	Describe("using a registry with self-signed certificate", func() {
		var registry *ghttp.Server

		BeforeEach(func() {
			registry = ghttp.NewTLSServer()

			fakeImage := empty.Image

			digest, err := fakeImage.Digest()
			Expect(err).ToNot(HaveOccurred())

			manifest, err := fakeImage.RawManifest()
			Expect(err).ToNot(HaveOccurred())

			config, err := fakeImage.RawConfigFile()
			Expect(err).ToNot(HaveOccurred())

			configDigest, err := fakeImage.ConfigName()
			Expect(err).ToNot(HaveOccurred())

			registry.AppendHandlers(
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/"),
					ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
				),
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/some/fake-image/manifests/"+digest.String()),
					ghttp.RespondWith(http.StatusOK, manifest),
				),
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/some/fake-image/blobs/"+configDigest.String()),
					ghttp.RespondWith(http.StatusOK, config),
				),
			)

			req.Source = resource.Source{
				Repository: registry.Addr() + "/some/fake-image",
			}

			req.Version.Digest = digest.String()
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

			It("pulls the image from the registry", func() {
				Expect(actualErr).ToNot(HaveOccurred())

				Expect(res.Version).To(Equal(req.Version))
			})
		})

		When("the certificate is missing in 'source'", func() {
			It("exits non-zero and returns an error", func() {
				Expect(actualErr).To(HaveOccurred())
			})
		})
	})

	Describe("using a mirror", func() {
		var mirror *ghttp.Server

		BeforeEach(func() {
			mirror = ghttp.NewServer()
		})

		AfterEach(func() {
			mirror.Close()
		})

		Context("when the repository contains a registry host name prefixed image", func() {
			var registry *ghttp.Server

			BeforeEach(func() {
				registry = ghttp.NewServer()

				fakeImage := empty.Image

				digest, err := fakeImage.Digest()
				Expect(err).ToNot(HaveOccurred())

				manifest, err := fakeImage.RawManifest()
				Expect(err).ToNot(HaveOccurred())

				config, err := fakeImage.RawConfigFile()
				Expect(err).ToNot(HaveOccurred())

				configDigest, err := fakeImage.ConfigName()
				Expect(err).ToNot(HaveOccurred())

				registry.AppendHandlers(
					ghttp.CombineHandlers(
						ghttp.VerifyRequest("GET", "/v2/"),
						ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
					),
					ghttp.CombineHandlers(
						ghttp.VerifyRequest("GET", "/v2/some/fake-image/manifests/"+digest.String()),
						ghttp.RespondWith(http.StatusOK, manifest),
					),
					ghttp.CombineHandlers(
						ghttp.VerifyRequest("GET", "/v2/some/fake-image/blobs/"+configDigest.String()),
						ghttp.RespondWith(http.StatusOK, config),
					),
				)

				req.Source = resource.Source{
					Repository: registry.Addr() + "/some/fake-image",
					RegistryMirror: &resource.RegistryMirror{
						Host: mirror.Addr(),
					},
				}

				req.Version.Digest = digest.String()
			})

			It("pulls the image from the registry declared in the repository and not from the mirror", func() {
				Expect(actualErr).ToNot(HaveOccurred())

				Expect(res.Version).To(Equal(req.Version))

				Expect(mirror.ReceivedRequests()).To(BeEmpty())
			})
		})

		Context("which has the image", func() {
			Context("in an explicit namespace", func() {
				BeforeEach(func() {
					req.Source.Repository = "concourse/test-image-static"

					// use Docker Hub as a "mirror"
					req.Source.RegistryMirror = &resource.RegistryMirror{
						Host: name.DefaultRegistry,
					}

					req.Version.Tag = "latest"
					req.Version.Digest = LATEST_STATIC_DIGEST
				})

				It("saves the rootfs and metadata", func() {
					Expect(actualErr).ToNot(HaveOccurred())

					_, err := os.Stat(rootfsPath("Dockerfile"))
					Expect(err).ToNot(HaveOccurred())

					_, err = os.ReadFile(filepath.Join(destDir, "digest"))
					Expect(err).ToNot(HaveOccurred())

					_, err = os.ReadFile(filepath.Join(destDir, "tag"))
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("in an implied namespace", func() {
				BeforeEach(func() {
					fakeImage := empty.Image

					digest, err := fakeImage.Digest()
					Expect(err).ToNot(HaveOccurred())

					manifest, err := fakeImage.RawManifest()
					Expect(err).ToNot(HaveOccurred())

					config, err := fakeImage.RawConfigFile()
					Expect(err).ToNot(HaveOccurred())

					configDigest, err := fakeImage.ConfigName()
					Expect(err).ToNot(HaveOccurred())

					mirror.AppendHandlers(
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("GET", "/v2/"),
							ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
						),
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("GET", "/v2/library/fake-image/manifests/"+digest.String()),
							ghttp.RespondWith(http.StatusOK, manifest),
						),
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("GET", "/v2/library/fake-image/blobs/"+configDigest.String()),
							ghttp.RespondWith(http.StatusOK, config),
						),
					)

					req.Source = resource.Source{
						Repository: "fake-image",
						RegistryMirror: &resource.RegistryMirror{
							Host: mirror.Addr(),
						},
					}

					req.Version.Digest = digest.String()
					req.Version.Tag = "latest"
				})

				It("pulls the image from the library", func() {
					Expect(actualErr).ToNot(HaveOccurred())

					Expect(res.Version).To(Equal(req.Version))
				})

				Context("saving an OCI tarball", func() {
					BeforeEach(func() {
						req.Params.RawFormat = "oci"
					})

					It("names the image with the original repository and tag, not the mirror", func() {
						Expect(actualErr).ToNot(HaveOccurred())

						tag, err := name.NewTag("fake-image:latest")
						Expect(err).ToNot(HaveOccurred())

						_, err = tarball.ImageFromPath(filepath.Join(destDir, "image.tar"), &tag)
						Expect(err).ToNot(HaveOccurred())
					})
				})
			})
		})

		Context("which is missing the image", func() {
			BeforeEach(func() {
				req.Source.RegistryMirror = &resource.RegistryMirror{
					Host: mirror.Addr(),
				}
			})

			Context("in an explicit namespace", func() {
				BeforeEach(func() {
					req.Source.Repository = "concourse/test-image-static"

					req.Version.Tag = "latest"
					req.Version.Digest = LATEST_STATIC_DIGEST

					mirror.AppendHandlers(
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("GET", "/v2/"),
							ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
						),
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("GET", "/v2/concourse/test-image-static/manifests/"+req.Version.Digest),
							ghttp.RespondWith(http.StatusNotFound, nil),
						),
					)
				})

				It("saves the rootfs and metadata", func() {
					Expect(actualErr).ToNot(HaveOccurred())

					_, err := os.Stat(rootfsPath("Dockerfile"))
					Expect(err).ToNot(HaveOccurred())

					_, err = os.ReadFile(filepath.Join(destDir, "digest"))
					Expect(err).ToNot(HaveOccurred())

					_, err = os.ReadFile(filepath.Join(destDir, "tag"))
					Expect(err).ToNot(HaveOccurred())
				})
			})

			Context("in an implied namespace", func() {
				BeforeEach(func() {
					req.Source.Repository = "busybox"

					req.Version.Tag = "latest"
					req.Version.Digest = latestDigest(req.Source.Repository)

					mirror.AppendHandlers(
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("GET", "/v2/"),
							ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
						),
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("GET", "/v2/library/busybox/manifests/"+req.Version.Digest),
							ghttp.RespondWith(http.StatusNotFound, nil),
						),
					)
				})

				It("pulls the image from the library", func() {
					Expect(actualErr).ToNot(HaveOccurred())

					Expect(res.Version).To(Equal(req.Version))
				})
			})
		})
	})
})
