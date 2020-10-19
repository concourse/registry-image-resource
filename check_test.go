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
			var mirror *ghttp.Server

			BeforeEach(func() {
				mirror = ghttp.NewServer()
			})

			AfterEach(func() {
				mirror.Close()
			})

			Context("when the repository contains a registry host name prefixed image", func() {
				BeforeEach(func() {
					mirror.AppendHandlers(
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("GET", "/v2/"),
							ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
						),
						ghttp.CombineHandlers(
							ghttp.VerifyRequest("HEAD", "/v2/some/fake-image/manifests/latest"),
							ghttp.RespondWith(http.StatusOK, `{"fake":"manifest"}`, http.Header{
								"Docker-Content-Digest": {LATEST_FAKE_DIGEST},
							}),
						),
					)

					req.Source.Repository = mirror.Addr() + "/some/fake-image"
					req.Source.RegistryMirror = &resource.RegistryMirror{
						Host: "thisregistrymirrordoesnotexist.nothing",
					}
				})

				It("it checks and returns the current digest using the registry declared in the repository and not using the mirror", func() {
					Expect(res).To(Equal([]resource.Version{
						{Digest: LATEST_FAKE_DIGEST},
					}))
				})
			})

			Context("which has the image", func() {
				Context("in an explicit namespace", func() {
					BeforeEach(func() {
						// use Docker Hub as a "mirror"
						req.Source.RegistryMirror = &resource.RegistryMirror{
							Host: name.DefaultRegistry,
						}
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							{Digest: LATEST_STATIC_DIGEST},
						}))
					})
				})

				Context("in an implied namespace", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/fake-image/manifests/latest"),
								ghttp.RespondWith(http.StatusOK, `{"fake":"manifest"}`, http.Header{
									"Docker-Content-Digest": {LATEST_FAKE_DIGEST},
								}),
							),
						)

						req.Source.Repository = "fake-image"
						req.Source.RegistryMirror = &resource.RegistryMirror{
							Host: mirror.Addr(),
						}
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							{Digest: LATEST_FAKE_DIGEST},
						}))
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
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/concourse/test-image-static/manifests/latest"),
								ghttp.RespondWith(http.StatusNotFound, nil),
							),
						)
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							{Digest: LATEST_STATIC_DIGEST},
						}))
					})
				})

				Context("in an implied namespace", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/busybox/manifests/1.32.0"),
								ghttp.RespondWith(http.StatusNotFound, nil),
							),
						)

						req.Source.Repository = "busybox"
						req.Source.RawTag = "1.32.0"
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							{Digest: latestDigest(req.Source.Name())},
						}))
					})
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
				*req.Version,
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
					*req.Version,
				}))
			})
		})

		Context("against a mirror", func() {
			var mirror *ghttp.Server

			BeforeEach(func() {
				mirror = ghttp.NewServer()
			})

			AfterEach(func() {
				mirror.Close()
			})

			Context("which has the image", func() {
				Context("in an explicit namespace", func() {
					BeforeEach(func() {
						// use Docker Hub as a "mirror"
						req.Source.RegistryMirror = &resource.RegistryMirror{
							Host: name.DefaultRegistry,
						}
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							*req.Version,
						}))
					})
				})

				Context("in an implied namespace", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/fake-image/manifests/latest"),
								ghttp.RespondWith(http.StatusOK, `{"fake":"manifest"}`, http.Header{
									"Docker-Content-Digest": {LATEST_FAKE_DIGEST},
								}),
							),
						)

						req.Source.Repository = "fake-image"
						req.Source.RegistryMirror = &resource.RegistryMirror{
							Host: mirror.Addr(),
						}

						req.Version = &resource.Version{
							Digest: LATEST_FAKE_DIGEST,
						}
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							*req.Version,
						}))
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
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/concourse/test-image-static/manifests/latest"),
								ghttp.RespondWith(http.StatusNotFound, nil),
							),
						)
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							*req.Version,
						}))
					})
				})

				Context("in an implied namespace", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/busybox/manifests/1.32.0"),
								ghttp.RespondWith(http.StatusNotFound, nil),
							),
						)

						req.Source.Repository = "busybox"
						req.Source.RawTag = "1.32.0"

						req.Version = &resource.Version{
							Digest: latestDigest(req.Source.Name()),
						}
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							*req.Version,
						}))
					})
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
			var mirror *ghttp.Server

			BeforeEach(func() {
				mirror = ghttp.NewServer()
			})

			AfterEach(func() {
				mirror.Close()
			})

			Context("which has the image", func() {
				Context("in an explicit namespace", func() {
					BeforeEach(func() {
						// use Docker Hub as a "mirror"
						req.Source.RegistryMirror = &resource.RegistryMirror{
							Host: name.DefaultRegistry,
						}
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							{Digest: OLDER_STATIC_DIGEST},
							{Digest: LATEST_STATIC_DIGEST},
						}))
					})
				})

				Context("in an implied namespace", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/fake-image/manifests/latest"),
								ghttp.RespondWith(http.StatusOK, `{"fake":"manifest"}`, http.Header{
									"Docker-Content-Digest": {LATEST_FAKE_DIGEST},
								}),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/fake-image/manifests/"+OLDER_FAKE_DIGEST),
								ghttp.RespondWith(http.StatusOK, `{"fake":"outdated"}`, http.Header{
									"Docker-Content-Digest": {OLDER_FAKE_DIGEST},
								}),
							),
						)

						req.Source.Repository = "fake-image"
						req.Source.RegistryMirror = &resource.RegistryMirror{
							Host: mirror.Addr(),
						}

						req.Version.Digest = OLDER_FAKE_DIGEST
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							{Digest: OLDER_FAKE_DIGEST},
							{Digest: LATEST_FAKE_DIGEST},
						}))
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
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/concourse/test-image-static/manifests/latest"),
								ghttp.RespondWith(http.StatusNotFound, nil),
							),
						)
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							{Digest: OLDER_STATIC_DIGEST},
							{Digest: LATEST_STATIC_DIGEST},
						}))
					})
				})

				Context("in an implied namespace", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/busybox/manifests/1.32.0"),
								ghttp.RespondWith(http.StatusNotFound, nil),
							),
						)

						req.Source.Repository = "busybox"
						req.Source.RawTag = "1.32.0"

						req.Version.Digest = OLDER_LIBRARY_DIGEST
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							{Digest: OLDER_LIBRARY_DIGEST},
							{Digest: latestDigest(req.Source.Name())},
						}))
					})
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
			var mirror *ghttp.Server

			BeforeEach(func() {
				mirror = ghttp.NewServer()
			})

			AfterEach(func() {
				mirror.Close()
			})

			Context("which has the image", func() {
				Context("in an explicit namespace", func() {
					BeforeEach(func() {
						// use Docker Hub as a "mirror"
						req.Source.RegistryMirror = &resource.RegistryMirror{
							Host: name.DefaultRegistry,
						}
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							{Digest: LATEST_STATIC_DIGEST},
						}))
					})
				})

				Context("in an implied namespace", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/fake-image/manifests/latest"),
								ghttp.RespondWith(http.StatusOK, `{"fake":"manifest"}`, http.Header{
									"Docker-Content-Digest": {LATEST_FAKE_DIGEST},
								}),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/fake-image/manifests/"+req.Version.Digest),
								ghttp.RespondWith(http.StatusNotFound, nil),
							),
						)

						req.Source.Repository = "fake-image"
						req.Source.RegistryMirror = &resource.RegistryMirror{
							Host: mirror.Addr(),
						}
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							{Digest: LATEST_FAKE_DIGEST},
						}))
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
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/concourse/test-image-static/manifests/latest"),
								ghttp.RespondWith(http.StatusNotFound, nil),
							),
						)
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							{Digest: LATEST_STATIC_DIGEST},
						}))
					})
				})

				Context("in an implied namespace", func() {
					BeforeEach(func() {
						mirror.AppendHandlers(
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("GET", "/v2/"),
								ghttp.RespondWith(http.StatusOK, `welcome to zombocom`),
							),
							ghttp.CombineHandlers(
								ghttp.VerifyRequest("HEAD", "/v2/library/busybox/manifests/1.32.0"),
								ghttp.RespondWith(http.StatusNotFound, nil),
							),
						)

						req.Source.Repository = "busybox"
						req.Source.RawTag = "1.32.0"
					})

					It("returns the current digest", func() {
						Expect(res).To(Equal([]resource.Version{
							{Digest: latestDigest(req.Source.Name())},
						}))
					})
				})
			})
		})
	})

	Context("when invoked with a tag that does not exist image", func() {
		BeforeEach(func() {
			req.Source = resource.Source{
				Repository: "concourse/test-image-static",
				RawTag:     "not-exist-image",
			}
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
})
