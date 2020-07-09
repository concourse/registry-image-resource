package resource_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
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

	check := func() {
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
	}

	Describe("tracking a single tag", func() {
		JustBeforeEach(check)

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
							Tag:        "latest",

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
							Tag:        "latest",

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
							Tag:        "latest",

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
							Tag:        "latest",

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
							Tag:        "latest",

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
							Tag:        "latest",

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
					Tag:        "latest",
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
						Tag:        "latest",

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
							Tag:        "latest",

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
							Tag:        "latest",

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
					Tag:        "not-exist-image",
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
						Tag:        "not-exist-image",

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
					Tag:        "latest",
				}
			})

			AfterEach(func() {
				registry.Close()
			})

			It("retries", func() {
				Expect(res).To(Equal([]resource.Version{
					// sha256 of {"fake":"manifest"}
					{Digest: "sha256:c4c25c2cd70e3071f08cf124c4b5c656c061dd38247d166d97098d58eeea8aa6"},
				}))
			})
		})
	})
})

var _ = DescribeTable("tracking semver tags",
	(SemverTagExample).Run,
	Entry("no semver tags",
		SemverTagExample{
			Tags: map[string]string{
				"non-semver-tag": "random-1",
			},
			Versions: []string{},
		},
	),
	Entry("latest tag",
		SemverTagExample{
			Tags: map[string]string{
				"non-semver-tag": "random-1",
				"latest":         "random-2",
			},
			Versions: []string{"latest"},
		},
	),
	Entry("semver and non-semver tags",
		SemverTagExample{
			Tags: map[string]string{
				"1.0.0":          "random-1",
				"non-semver-tag": "random-2",
			},
			Versions: []string{"1.0.0"},
		},
	),
	Entry("semver tag ordering",
		SemverTagExample{
			Tags: map[string]string{
				"1.0.0": "random-1",
				"1.2.1": "random-3",
				"2.0.0": "random-5",
			},
			Versions: []string{"1.0.0", "1.2.1", "2.0.0"},
		},
	),
	Entry("semver tag ordering with cursor",
		SemverTagExample{
			Tags: map[string]string{
				"1.0.0": "random-1",
				"1.2.1": "random-3",
				"2.0.0": "random-5",
			},
			From: &resource.Version{
				Tag:    "1.2.1",
				Digest: "random-3",
			},
			Versions: []string{"1.2.1", "2.0.0"},
		},
	),
	Entry("semver tag ordering with cursor with different digest",
		SemverTagExample{
			Tags: map[string]string{
				"1.0.0": "random-1",
				"1.2.1": "random-3",
				"2.0.0": "random-5",
			},
			From: &resource.Version{
				Tag:    "1.2.1",
				Digest: "bogus",
			},
			Versions: []string{"1.0.0", "1.2.1", "2.0.0"},
		},
	),
	Entry("prereleases ignored by default",
		SemverTagExample{
			Tags: map[string]string{
				"1.0.0-alpha.1": "random-0",
				"1.0.0":         "random-1",
				"1.2.1-beta.1":  "random-2",
				"1.2.1":         "random-3",
				"2.0.0-rc.1":    "random-4",
				"2.0.0":         "random-5",
			},
			Versions: []string{"1.0.0", "1.2.1", "2.0.0"},
		},
	),
	Entry("prereleases opted in",
		SemverTagExample{
			Tags: map[string]string{
				"1.0.0-alpha.1": "random-0",
				"1.0.0":         "random-1",
				"1.2.1-beta.1":  "random-2",
				"1.2.1":         "random-3",
				"2.0.0-rc.1":    "random-4",
				"2.0.0":         "random-5",
			},
			PreReleases: true,
			Versions: []string{
				"1.0.0-alpha.1",
				"1.0.0",
				"1.2.1-beta.1",
				"1.2.1",
				"2.0.0-rc.1",
				"2.0.0",
			},
		},
	),
	Entry("prereleases do not include 'variants'",
		SemverTagExample{
			Tags: map[string]string{
				"1.0.0-alpha.1": "random-0",
				"1.0.0":         "random-1",
				"1.0.0-foo":     "random-2",
			},
			PreReleases: true,
			Versions:    []string{"1.0.0-alpha.1", "1.0.0"},
		},
	),
	Entry("mixed specificity semver tags",
		SemverTagExample{
			Tags: map[string]string{
				"1":      "random-1",
				"2":      "random-2",
				"2.1":    "random-2",
				"latest": "random-3",
				"3":      "random-3",
				"3.2":    "random-3",
				"3.2.1":  "random-3",
				"3.1":    "random-4",
				"3.1.0":  "random-4",
				"3.0":    "random-5",
				"3.0.0":  "random-5",
			},
			Versions: []string{"1", "2.1", "3.0.0", "3.1.0", "3.2.1"},
		},
	),
	Entry("semver tags with latest tag having unique digest",
		SemverTagExample{
			Tags: map[string]string{
				"1.0.0":          "random-1",
				"non-semver-tag": "random-2",
				"latest":         "random-3",
			},
			Versions: []string{"1.0.0", "latest"},
		},
	),
	Entry("latest tag pointing to latest version",
		SemverTagExample{
			Tags: map[string]string{
				"1":      "random-1",
				"2":      "random-2",
				"3":      "random-3",
				"latest": "random-3",
			},
			Versions: []string{"1", "2", "3"},
		},
	),
	Entry("latest tag pointing to older version",
		SemverTagExample{
			Tags: map[string]string{
				"1":      "random-1",
				"2":      "random-2",
				"latest": "random-2",
				"3":      "random-3",
			},
			Versions: []string{"1", "2", "3"},
		},
	),
	Entry("variants",
		SemverTagExample{
			Tags: map[string]string{
				"latest":    "random-1",
				"1.0.0":     "random-1",
				"0.9.0":     "random-2",
				"foo":       "random-3",
				"1.0.0-foo": "random-3",
				"0.9.0-foo": "random-4",
				"bar":       "random-5",
				"1.0.0-bar": "random-5",
				"0.9.0-bar": "random-6",
			},

			Variant: "foo",

			Versions: []string{"0.9.0-foo", "1.0.0-foo"},
		},
	),
	Entry("variant with bare variant tag pointing to unique digest",
		SemverTagExample{
			Tags: map[string]string{
				"latest":    "random-1",
				"1.0.0":     "random-1",
				"0.9.0":     "random-2",
				"foo":       "random-3",
				"0.8.0-foo": "random-4",
				"bar":       "random-5",
				"1.0.0-bar": "random-5",
				"0.9.0-bar": "random-6",
			},

			Variant: "foo",

			Versions: []string{"0.8.0-foo", "foo"},
		},
	),
	Entry("distinguishing additional variants from prereleases",
		SemverTagExample{
			Tags: map[string]string{
				"1.0.0-foo":             "random-1",
				"1.0.0-rc.1-foo":        "random-2",
				"1.0.0-alpha.1-foo":     "random-3",
				"1.0.0-beta.1-foo":      "random-4",
				"1.0.0-bar-foo":         "random-5",
				"1.0.0-rc.1-bar-foo":    "random-6",
				"1.0.0-alpha.1-bar-foo": "random-7",
				"1.0.0-beta.1-bar-foo":  "random-8",
			},

			Variant:     "foo",
			PreReleases: true,

			Versions: []string{
				"1.0.0-alpha.1-foo",
				"1.0.0-beta.1-foo",
				"1.0.0-rc.1-foo",
				"1.0.0-foo",
			},
		},
	),
)

type SemverTagExample struct {
	Tags map[string]string

	PreReleases bool
	Variant     string

	From *resource.Version

	Versions []string
}

type registryTagsResponse struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

func (example SemverTagExample) Run() {
	registryServer := ghttp.NewServer()
	defer registryServer.Close()

	registryServer.RouteToHandler(
		"GET",
		"/v2/",
		ghttp.RespondWith(http.StatusOK, ""),
	)

	var err error
	repo, err := name.NewRepository(fmt.Sprintf("%s/test-image", registryServer.Addr()))
	Expect(err).ToNot(HaveOccurred())

	req := resource.CheckRequest{
		Source: resource.Source{
			Repository:  repo.Name(),
			PreReleases: example.PreReleases,
			Variant:     example.Variant,
		},
	}

	tagNames := []string{}
	for name := range example.Tags {
		tagNames = append(tagNames, name)
	}

	registryServer.RouteToHandler(
		"GET",
		"/v2/"+repo.RepositoryStr()+"/tags/list",
		ghttp.RespondWithJSONEncoded(http.StatusOK, registryTagsResponse{
			Name: "some-name",
			Tags: tagNames,
		}),
	)

	images := map[string]v1.Image{}

	tagVersions := map[string]resource.Version{}
	for name, imageName := range example.Tags {
		image, found := images[imageName]
		if !found {
			var err error
			image, err = random.Image(1024, 1)
			Expect(err).ToNot(HaveOccurred())

			images[imageName] = image
		}

		manifest, err := image.RawManifest()
		Expect(err).ToNot(HaveOccurred())

		mediaType, err := image.MediaType()
		Expect(err).ToNot(HaveOccurred())

		registryServer.RouteToHandler(
			"GET",
			"/v2/"+repo.RepositoryStr()+"/manifests/"+name,
			ghttp.RespondWith(http.StatusOK, manifest, http.Header{"Content-Type": []string{string(mediaType)}}),
		)

		digest, err := image.Digest()
		Expect(err).ToNot(HaveOccurred())

		tagVersions[name] = resource.Version{
			Tag:    name,
			Digest: digest.String(),
		}
	}

	if example.From != nil {
		req.Version = &resource.Version{
			Tag: example.From.Tag,
		}

		image, found := images[example.From.Digest]
		if found {
			digest, err := image.Digest()
			Expect(err).ToNot(HaveOccurred())

			req.Version.Digest = digest.String()
		} else {
			// intentionally bogus digest
			req.Version.Digest = example.From.Digest
		}
	}

	res := example.check(req)

	expectedVersions := make(resource.CheckResponse, len(example.Versions))
	for i, ver := range example.Versions {
		expectedVersions[i] = tagVersions[ver]
	}

	Expect(res).To(Equal(expectedVersions))
}

func (example SemverTagExample) check(req resource.CheckRequest) resource.CheckResponse {
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

	var res resource.CheckResponse
	err = json.Unmarshal(outBuf.Bytes(), &res)
	Expect(err).ToNot(HaveOccurred())

	return res
}
