package resource_test

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
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
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"

	resource "github.com/concourse/registry-image-resource"
)

var _ = Describe("In", func() {
	var destDir string

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
		destDir, err = ioutil.TempDir("", "docker-image-in-dir")
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

		err = cmd.Run()
		Expect(err).ToNot(HaveOccurred())

		err = json.Unmarshal(outBuf.Bytes(), &res)
		Expect(err).ToNot(HaveOccurred())
	})

	Describe("image metadata", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-metadata"
			req.Version.Digest = latestDigest(req.Source.Repository)
		})

		It("captures the env and user", func() {
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
			req.Version.Digest = latestDigest(req.Source.Repository)
		})

		It("returns metadata", func() {
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
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-file-perms-mtime"
			req.Version.Digest = latestDigest(req.Source.Repository)
		})

		It("keeps file ownership, permissions, and modified times", func() {
			stat, err := os.Stat(rootfsPath("home", "alex", "birthday"))
			Expect(err).ToNot(HaveOccurred())

			Expect(stat.Mode()).To(Equal(os.FileMode(0603)))
			Expect(stat.ModTime()).To(BeTemporally("==", time.Date(1991, 06, 03, 05, 30, 30, 0, time.UTC)))

			sys, ok := stat.Sys().(*syscall.Stat_t)
			Expect(ok).To(BeTrue())
			Expect(sys.Uid).To(Equal(uint32(1000)))
			Expect(sys.Gid).To(Equal(uint32(1000)))
		})
	})

	Describe("removed files in layers", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-whiteout"
			req.Version.Digest = latestDigest(req.Source.Repository)
		})

		It("does not restore files that were removed in later layers", func() {
			infos, err := ioutil.ReadDir(rootfsPath("top-dir-1"))
			Expect(err).ToNot(HaveOccurred())
			Expect(infos).To(HaveLen(2))

			stat, err := os.Stat(rootfsPath("top-dir-1", "nested-file"))
			Expect(err).ToNot(HaveOccurred())
			Expect(stat.IsDir()).To(BeFalse())

			stat, err = os.Stat(rootfsPath("top-dir-1", "nested-dir"))
			Expect(err).ToNot(HaveOccurred())
			Expect(stat.IsDir()).To(BeTrue())

			infos, err = ioutil.ReadDir(rootfsPath("top-dir-1", "nested-dir"))
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
		})
	})

	Describe("a hardlink that is later removed", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-removed-hardlinks"
			req.Version.Digest = latestDigest(req.Source.Repository)
		})

		It("works", func() {
			lstat, err := os.Lstat(rootfsPath("usr", "libexec", "git-core", "git"))
			Expect(err).ToNot(HaveOccurred())
			Expect(lstat.Mode() & os.ModeSymlink).To(BeZero())

			stat, err := os.Stat(rootfsPath("usr", "libexec", "git-core", "git"))
			Expect(err).ToNot(HaveOccurred())
			Expect(stat.Mode() & os.ModeSymlink).To(BeZero())
		})
	})

	Describe("layers that replace symlinks with regular files", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-symlinks"
			req.Version.Digest = latestDigest(req.Source.Repository)
		})

		It("removes the symlink and writes to a new file rather than trying to open and write to it (thereby overwriting its target)", func() {
			Expect(cat(rootfsPath("a"))).To(Equal("symlinked\n"))
			Expect(cat(rootfsPath("b"))).To(Equal("replaced\n"))
		})
	})

	Describe("fetching from a private repository with credentials", func() {
		BeforeEach(func() {
			req.Source = resource.Source{
				Repository: dockerPrivateRepo,
				RawTag:     "latest",

				Username: dockerPrivateUsername,
				Password: dockerPrivatePassword,
			}

			checkDockerPrivateUserConfigured()

			req.Version = resource.Version{
				Digest: PRIVATE_LATEST_STATIC_DIGEST,
			}
		})

		It("works", func() {
			Expect(cat(rootfsPath("Dockerfile"))).To(ContainSubstring("hello!"))
		})
	})

	Describe("fetching in OCI format", func() {
		var manifest *v1.Manifest

		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-static"
			req.Params.RawFormat = "oci"

			req.Version.Digest, manifest = latestManifest(req.Source.Repository)
		})

		It("saves the tagged image as image.tar instead of saving the rootfs", func() {
			_, err := os.Stat(filepath.Join(destDir, "rootfs"))
			Expect(os.IsNotExist(err)).To(BeTrue())

			_, err = os.Stat(filepath.Join(destDir, "manifest.json"))
			Expect(os.IsNotExist(err)).To(BeTrue())

			tag, err := name.NewTag("concourse/test-image-static:latest", name.WeakValidation)
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

	Describe("saving the digest", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-static"
			req.Version.Digest = LATEST_STATIC_DIGEST
		})

		It("saves the digest to a file", func() {
			digest, err := ioutil.ReadFile(filepath.Join(destDir, "digest"))
			Expect(err).ToNot(HaveOccurred())
			Expect(string(digest)).To(Equal(req.Version.Digest))
		})
	})

	Describe("saving the tag", func() {
		BeforeEach(func() {
			req.Source.Repository = "concourse/test-image-static"
			req.Version.Digest = LATEST_STATIC_DIGEST
		})

		Context("with no tag specified", func() {
			BeforeEach(func() {
				req.Source.RawTag = ""
			})

			It("assumes 'latest' and saves the tag to a file", func() {
				digest, err := ioutil.ReadFile(filepath.Join(destDir, "tag"))
				Expect(err).ToNot(HaveOccurred())
				Expect(string(digest)).To(Equal("latest"))
			})
		})

		Context("with a tag specified", func() {
			BeforeEach(func() {
				req.Source.RawTag = "tagged"
				req.Version.Digest = LATEST_TAGGED_STATIC_DIGEST
			})

			It("saves the tag to a file", func() {
				tag, err := ioutil.ReadFile(filepath.Join(destDir, "tag"))
				Expect(err).ToNot(HaveOccurred())
				Expect(string(tag)).To(Equal("tagged"))
			})
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

			config, err := fakeImage.RawConfigFile()
			Expect(err).ToNot(HaveOccurred())

			configDigest, err := fakeImage.ConfigName()
			Expect(err).ToNot(HaveOccurred())

			registry.AppendHandlers(
				// immediate 429
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

			req.Version.Digest = digest.String()
		})

		AfterEach(func() {
			registry.Close()
		})

		It("retries", func() {
			Expect(res.Version).To(Equal(req.Version))
		})
	})
})
