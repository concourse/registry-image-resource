package resource_test

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	resource "github.com/concourse/registry-image-resource"
)

var _ = Describe("In", func() {
	var destDir string

	var req struct {
		Source  resource.Source
		Version resource.Version
	}

	var res struct {
		Version  resource.Version
		Metadata []resource.MetadataField
	}

	rootfsPath := func(path ...string) string {
		return filepath.Join(append([]string{destDir, "rootfs"}, path...)...)
	}

	BeforeEach(func() {
		var err error
		destDir, err = ioutil.TempDir("", "docker-image-in-dir")
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(destDir)).To(Succeed())
	})

	JustBeforeEach(func() {
		cmd := exec.Command(bins.In, destDir)

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
})

func cat(path string) string {
	bytes, err := ioutil.ReadFile(path)
	Expect(err).ToNot(HaveOccurred())
	return string(bytes)
}
