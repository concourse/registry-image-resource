package resource_test

import (
	"bytes"
	"encoding/json"
	"os/exec"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	resource "github.com/concourse/registry-image-resource"
)

var _ = Describe("Check", func() {
	var req struct {
		Source  resource.Source
		Version *resource.Version
	}

	var res []resource.Version

	JustBeforeEach(func() {
		cmd := exec.Command(bins.Check)

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

	Context("when invoked with no cursor version", func() {
		BeforeEach(func() {
			req.Source = resource.Source{
				Repository: "concourse/test-image-static",
				Tag:        "latest",
			}

			req.Version = nil
		})

		It("returns the current digest", func() {
			Expect(res).To(Equal([]resource.Version{
				{Digest: LATEST_STATIC_DIGEST},
			}))
		})

		Context("against a private repo with credentials", func() {
			BeforeEach(func() {
				req.Source = resource.Source{
					Repository: dockerPrivateRepo,
					Tag:        "latest",

					Username: dockerUsername,
					Password: dockerPassword,
				}

				checkDockerUserConfigured()
			})

			It("returns the current digest", func() {
				Expect(res).To(Equal([]resource.Version{
					{Digest: PRIVATE_LATEST_STATIC_DIGEST},
				}))
			})
		})
	})

	Context("when invoked with an up-to-date cursor version", func() {
		BeforeEach(func() {
			req.Source = resource.Source{
				Repository: "concourse/test-image-static",
				Tag:        "latest",
			}

			req.Version = &resource.Version{
				Digest: LATEST_STATIC_DIGEST,
			}
		})

		It("returns the given digest", func() {
			Expect(res).To(Equal([]resource.Version{
				{Digest: LATEST_STATIC_DIGEST},
			}))
		})

		Context("against a private repo with credentials", func() {
			BeforeEach(func() {
				req.Source = resource.Source{
					Repository: dockerPrivateRepo,
					Tag:        "latest",

					Username: dockerUsername,
					Password: dockerPassword,
				}

				checkDockerUserConfigured()

				req.Version = &resource.Version{
					Digest: PRIVATE_LATEST_STATIC_DIGEST,
				}
			})

			It("returns the current digest", func() {
				Expect(res).To(Equal([]resource.Version{
					{Digest: PRIVATE_LATEST_STATIC_DIGEST},
				}))
			})
		})
	})

	Context("when invoked with a valid but out-of-date cursor version", func() {
		BeforeEach(func() {
			req.Source = resource.Source{
				Repository: "concourse/test-image-static",
				Tag:        "latest",
			}

			req.Version = &resource.Version{
				// this was previously pushed to the 'latest' tag
				Digest: OLDER_STATIC_DIGEST,
			}
		})

		It("returns the previous digest and the current digest", func() {
			Expect(res).To(Equal([]resource.Version{
				{Digest: OLDER_STATIC_DIGEST},
				{Digest: LATEST_STATIC_DIGEST},
			}))
		})

		Context("against a private repo with credentials", func() {
			BeforeEach(func() {
				req.Source = resource.Source{
					Repository: dockerPrivateRepo,
					Tag:        "latest",

					Username: dockerUsername,
					Password: dockerPassword,
				}

				checkDockerUserConfigured()

				req.Version = &resource.Version{
					// this was previously pushed to the 'latest' tag
					Digest: PRIVATE_OLDER_STATIC_DIGEST,
				}
			})

			It("returns the current digest", func() {
				Expect(res).To(Equal([]resource.Version{
					{Digest: PRIVATE_OLDER_STATIC_DIGEST},
					{Digest: PRIVATE_LATEST_STATIC_DIGEST},
				}))
			})
		})
	})

	Context("when invoked with an invalid cursor version", func() {
		BeforeEach(func() {
			req.Source = resource.Source{
				Repository: "concourse/test-image-static",
				Tag:        "latest",
			}

			req.Version = &resource.Version{
				// (note the end)
				Digest: "sha256:031567a617423a84ad68b62267c30693185bd2b92c2668732efc8c70deadbeef",
			}
		})

		It("returns only the current digest", func() {
			Expect(res).To(Equal([]resource.Version{
				{Digest: LATEST_STATIC_DIGEST},
			}))
		})

		Context("against a private repo with credentials", func() {
			BeforeEach(func() {
				req.Source = resource.Source{
					Repository: dockerPrivateRepo,
					Tag:        "latest",

					Username: dockerUsername,
					Password: dockerPassword,
				}

				checkDockerUserConfigured()
			})

			It("returns the current digest", func() {
				Expect(res).To(Equal([]resource.Version{
					{Digest: PRIVATE_LATEST_STATIC_DIGEST},
				}))
			})
		})
	})
})
