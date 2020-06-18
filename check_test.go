package resource_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os/exec"

	"github.com/google/go-containerregistry/pkg/name"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/ghttp"

	resource "github.com/concourse/registry-image-resource"
)

var _ = Describe("Check", func() {
	var req struct {
		Source  resource.Source
		Version *resource.Version
	}

	var res []resource.Version

	BeforeEach(func() {
		req.Source = resource.Source{}
		req.Version = nil

		res = nil
	})

	JustBeforeEach(func() {
		cmd := exec.Command(bins.Check)
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

	Context("when invoked with no cursor version", func() {
		BeforeEach(func() {
			req.Source = resource.Source{
				Repository: "concourse/test-image-static",
				RawTag:     "latest",
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
					RawTag:     "latest",

					BasicCredentials: resource.BasicCredentials{
						Username: dockerPrivateUsername,
						Password: dockerPrivatePassword,
					},
				}

				checkDockerPrivateUserConfigured()
			})

			It("returns the current digest", func() {
				Expect(res).To(Equal([]resource.Version{
					{Digest: PRIVATE_LATEST_STATIC_DIGEST},
				}))
			})
		})

		Context("against a mirror", func() {
			Context("which has the image", func() {
				BeforeEach(func() {
					req.Source = resource.Source{
						Repository: "fakeserver.foo:5000/concourse/test-image-static",
						RawTag:     "latest",

						RegistryMirror: &resource.RegistryMirror{
							Host: name.DefaultRegistry,
						},
					}
				})

				It("returns the current digest", func() {
					Expect(res).To(Equal([]resource.Version{
						{Digest: LATEST_STATIC_DIGEST},
					}))
				})
			})

			Context("which is missing the image", func() {
				BeforeEach(func() {
					req.Source = resource.Source{
						Repository: "concourse/test-image-static",
						RawTag:     "latest",

						RegistryMirror: &resource.RegistryMirror{
							Host: "fakeserver.foo:5000",
						},
					}
				})

				It("returns the current digest", func() {
					Expect(res).To(Equal([]resource.Version{
						{Digest: LATEST_STATIC_DIGEST},
					}))
				})
			})
		})
	})

	Context("when invoked with an up-to-date cursor version", func() {
		BeforeEach(func() {
			req.Source = resource.Source{
				Repository: "concourse/test-image-static",
				RawTag:     "latest",
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
					RawTag:     "latest",

					BasicCredentials: resource.BasicCredentials{
						Username: dockerPrivateUsername,
						Password: dockerPrivatePassword,
					},
				}

				checkDockerPrivateUserConfigured()

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

		Context("against a mirror", func() {
			Context("which has the image", func() {
				BeforeEach(func() {
					req.Source = resource.Source{
						Repository: "fakeserver.foo:5000/concourse/test-image-static",
						RawTag:     "latest",

						RegistryMirror: &resource.RegistryMirror{
							Host: name.DefaultRegistry,
						},
					}

					req.Version = &resource.Version{
						Digest: LATEST_STATIC_DIGEST,
					}
				})

				It("returns the current digest", func() {
					Expect(res).To(Equal([]resource.Version{
						{Digest: LATEST_STATIC_DIGEST},
					}))
				})
			})

			Context("which is missing the image", func() {
				BeforeEach(func() {
					req.Source = resource.Source{
						Repository: "concourse/test-image-static",
						RawTag:     "latest",

						RegistryMirror: &resource.RegistryMirror{
							Host: "fakeserver.foo:5000",
						},
					}

					req.Version = &resource.Version{
						Digest: LATEST_STATIC_DIGEST,
					}
				})

				It("returns the current digest", func() {
					Expect(res).To(Equal([]resource.Version{
						{Digest: LATEST_STATIC_DIGEST},
					}))
				})
			})
		})
	})

	Context("when invoked with a valid but out-of-date cursor version", func() {
		BeforeEach(func() {
			req.Source = resource.Source{
				Repository: "concourse/test-image-static",
				RawTag:     "latest",
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
					RawTag:     "latest",

					BasicCredentials: resource.BasicCredentials{
						Username: dockerPrivateUsername,
						Password: dockerPrivatePassword,
					},
				}

				checkDockerPrivateUserConfigured()

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

		Context("against a mirror", func() {
			Context("which has the image", func() {
				BeforeEach(func() {
					req.Source = resource.Source{
						Repository: "fakeserver.foo:5000/concourse/test-image-static",
						RawTag:     "latest",

						RegistryMirror: &resource.RegistryMirror{
							Host: name.DefaultRegistry,
						},
					}

					req.Version = &resource.Version{
						// this was previously pushed to the 'latest' tag
						Digest: OLDER_STATIC_DIGEST,
					}
				})

				It("returns the current digest", func() {
					Expect(res).To(Equal([]resource.Version{
						{Digest: OLDER_STATIC_DIGEST},
						{Digest: LATEST_STATIC_DIGEST},
					}))
				})
			})

			Context("which is missing the image", func() {
				BeforeEach(func() {
					req.Source = resource.Source{
						Repository: "concourse/test-image-static",
						RawTag:     "latest",

						RegistryMirror: &resource.RegistryMirror{
							Host: "fakeserver.foo:5000",
						},
					}

					req.Version = &resource.Version{
						// this was previously pushed to the 'latest' tag
						Digest: OLDER_STATIC_DIGEST,
					}
				})

				It("returns the current digest", func() {
					Expect(res).To(Equal([]resource.Version{
						{Digest: OLDER_STATIC_DIGEST},
						{Digest: LATEST_STATIC_DIGEST},
					}))
				})
			})
		})
	})

	Context("when invoked with an invalid cursor version", func() {
		BeforeEach(func() {
			req.Source = resource.Source{
				Repository: "concourse/test-image-static",
				RawTag:     "latest",
			}

			req.Version = &resource.Version{
				Digest: "sha256:deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
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
					RawTag:     "latest",

					BasicCredentials: resource.BasicCredentials{
						Username: dockerPrivateUsername,
						Password: dockerPrivatePassword,
					},
				}

				checkDockerPrivateUserConfigured()
			})

			It("returns the current digest", func() {
				Expect(res).To(Equal([]resource.Version{
					{Digest: PRIVATE_LATEST_STATIC_DIGEST},
				}))
			})
		})

		Context("against a mirror", func() {
			Context("which has the image", func() {
				BeforeEach(func() {
					req.Source = resource.Source{
						Repository: "fakeserver.foo:5000/concourse/test-image-static",
						RawTag:     "latest",

						RegistryMirror: &resource.RegistryMirror{
							Host: name.DefaultRegistry,
						},
					}
				})

				It("returns the current digest", func() {
					Expect(res).To(Equal([]resource.Version{
						{Digest: LATEST_STATIC_DIGEST},
					}))
				})
			})

			Context("which is missing the image", func() {
				BeforeEach(func() {
					req.Source = resource.Source{
						Repository: "concourse/test-image-static",
						RawTag:     "latest",

						RegistryMirror: &resource.RegistryMirror{
							Host: "fakeserver.foo:5000",
						},
					}
				})

				It("returns the current digest", func() {
					Expect(res).To(Equal([]resource.Version{
						{Digest: LATEST_STATIC_DIGEST},
					}))
				})
			})
		})
	})

	Context("when invoked with not exist image", func() {
		BeforeEach(func() {
			req.Source = resource.Source{
				Repository: "concourse/test-image-static",
				RawTag:     "not-exist-image",
			}
			req.Version = nil
		})

		It("returns empty digest", func() {
			Expect(res).To(Equal([]resource.Version{}))
		})

		Context("against a private repo with credentials", func() {
			BeforeEach(func() {
				req.Source = resource.Source{
					Repository: dockerPrivateRepo,
					RawTag:     "not-exist-image",

					BasicCredentials: resource.BasicCredentials{
						Username: dockerPrivateUsername,
						Password: dockerPrivatePassword,
					},
				}

				checkDockerPrivateUserConfigured()
			})

			It("returns empty digest", func() {
				Expect(res).To(Equal([]resource.Version{}))
			})
		})
	})

	Context("when the registry returns 429 Too Many Requests", func() {
		var registry *ghttp.Server

		BeforeEach(func() {
			registry = ghttp.NewServer()

			registry.AppendHandlers(
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/"),
					ghttp.RespondWith(http.StatusTooManyRequests, "calm down"),
				),
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/"),
					ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
				),
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/fake-image/manifests/latest"),
					ghttp.RespondWith(http.StatusTooManyRequests, "calm down"),
				),
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/"),
					ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
				),
				ghttp.CombineHandlers(
					ghttp.VerifyRequest("GET", "/v2/fake-image/manifests/latest"),
					ghttp.RespondWith(http.StatusOK, `{"fake":"manifest"}`),
				),
			)

			req.Source = resource.Source{
				Repository: registry.Addr() + "/fake-image",
			}
		})

		AfterEach(func() {
			registry.Close()
		})

		It("retries", func() {
			Expect(res).To(Equal([]resource.Version{
				// sha256 of {"fake":"Manifest"}
				{Digest: "sha256:c4c25c2cd70e3071f08cf124c4b5c656c061dd38247d166d97098d58eeea8aa6"},
			}))
		})
	})
})
