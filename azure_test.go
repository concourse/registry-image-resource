package resource_test

import (
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	resource "github.com/concourse/registry-image-resource"
)

var _ = Describe("Azure ACR Authentication", func() {

	Describe("parseACRChallengeTenant", func() {
		DescribeTable("extracts tenant from Www-Authenticate header",
			func(header string, expected string) {
				Expect(resource.ParseACRChallengeTenant(header)).To(Equal(expected))
			},
			Entry("standard ACR challenge with tenant",
				`Bearer realm="https://myregistry.azurecr.io/oauth2/exchange?tenant=72f988bf-86f1-41af-91ab-2d7cd011db47",service="myregistry.azurecr.io"`,
				"72f988bf-86f1-41af-91ab-2d7cd011db47",
			),
			Entry("challenge without tenant param",
				`Bearer realm="https://myregistry.azurecr.io/oauth2/exchange",service="myregistry.azurecr.io"`,
				"common",
			),
			Entry("empty header",
				"",
				"common",
			),
			Entry("non-Bearer header",
				`Basic realm="something"`,
				"common",
			),
			Entry("realm with multiple query params including tenant",
				`Bearer realm="https://myregistry.azurecr.io/oauth2/exchange?foo=bar&tenant=abc-123&baz=qux",service="myregistry.azurecr.io"`,
				"abc-123",
			),
			Entry("Bearer with no realm",
				`Bearer service="myregistry.azurecr.io"`,
				"common",
			),
		)
	})

	Describe("exchangeACRRefreshToken", func() {
		It("returns a refresh token on successful exchange", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal("/oauth2/exchange"))
				Expect(r.Method).To(Equal(http.MethodPost))

				err := r.ParseForm()
				Expect(err).ToNot(HaveOccurred())

				Expect(r.FormValue("grant_type")).To(Equal("access_token"))
				Expect(r.FormValue("access_token")).To(Equal("fake-aad-token"))
				Expect(r.FormValue("tenant")).To(Equal("test-tenant"))

				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"refresh_token": "fake-acr-refresh-token",
				})
			}))
			defer server.Close()

			host := strings.TrimPrefix(server.URL, "http://")

			token, err := resource.ExchangeACRRefreshToken(host, "test-tenant", "fake-aad-token", true, resource.PlainHTTPClient)
			Expect(err).ToNot(HaveOccurred())
			Expect(token).To(Equal("fake-acr-refresh-token"))
		})

		It("returns an error when the server returns an error status", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, "unauthorized")
			}))
			defer server.Close()

			host := strings.TrimPrefix(server.URL, "http://")

			_, err := resource.ExchangeACRRefreshToken(host, "test-tenant", "fake-token", true, resource.PlainHTTPClient)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("status 401"))
		})

		It("returns an error when the server returns invalid JSON", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, "not-json")
			}))
			defer server.Close()

			host := strings.TrimPrefix(server.URL, "http://")

			_, err := resource.ExchangeACRRefreshToken(host, "test-tenant", "fake-token", true, resource.PlainHTTPClient)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("decode"))
		})
	})

	Describe("acrChallengeTenant", func() {
		It("parses tenant from mock ACR v2 endpoint", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v2/" {
					w.Header().Set("Www-Authenticate",
						fmt.Sprintf(`Bearer realm="http://%s/oauth2/exchange?tenant=test-tenant-id",service="%s"`, r.Host, r.Host))
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()

			host := strings.TrimPrefix(server.URL, "http://")

			Expect(resource.AcrChallengeTenant(host, true, resource.PlainHTTPClient)).To(Equal("test-tenant-id"))
		})

		It("returns common when challenge has no tenant", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v2/" {
					w.Header().Set("Www-Authenticate",
						fmt.Sprintf(`Bearer realm="http://%s/oauth2/exchange",service="%s"`, r.Host, r.Host))
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()

			host := strings.TrimPrefix(server.URL, "http://")

			Expect(resource.AcrChallengeTenant(host, true, resource.PlainHTTPClient)).To(Equal("common"))
		})

		It("returns common when server returns 200", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			host := strings.TrimPrefix(server.URL, "http://")

			Expect(resource.AcrChallengeTenant(host, true, resource.PlainHTTPClient)).To(Equal("common"))
		})
	})

	Describe("newACRHTTPClient", func() {
		It("has a 30s timeout and nil transport when no ca_certs provided", func() {
			client := resource.NewACRHTTPClient(nil, false)
			Expect(client.Timeout).To(Equal(30 * time.Second))
			Expect(client.Transport).To(BeNil())
		})

		It("configures custom TLS when ca_certs are provided", func() {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			defer server.Close()

			cert := server.TLS.Certificates[0].Certificate[0]
			pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})

			client := resource.NewACRHTTPClient([]string{string(pemCert)}, false)
			Expect(client.Transport).ToNot(BeNil())

			tr, ok := client.Transport.(*http.Transport)
			Expect(ok).To(BeTrue())
			Expect(tr.TLSClientConfig).ToNot(BeNil())
			Expect(tr.TLSClientConfig.RootCAs).ToNot(BeNil())
		})

		It("ignores ca_certs when insecure is true", func() {
			client := resource.NewACRHTTPClient([]string{"some-cert-pem"}, true)
			Expect(client.Transport).To(BeNil())
		})
	})

	Describe("ACR token exchange with custom CA", func() {
		var (
			server *httptest.Server
			host   string
			pemCA  string
		)

		BeforeEach(func() {
			server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v2/":
					w.Header().Set("Www-Authenticate",
						fmt.Sprintf(`Bearer realm="https://%s/oauth2/exchange?tenant=firewall-tenant",service="%s"`, r.Host, r.Host))
					w.WriteHeader(http.StatusUnauthorized)
				case "/oauth2/exchange":
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]string{
						"refresh_token": "firewall-ca-refresh-token",
					})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))

			serverCert := server.TLS.Certificates[0].Certificate[0]
			pemCA = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert}))
			host = strings.TrimPrefix(server.URL, "https://")
		})

		AfterEach(func() {
			server.Close()
		})

		It("fails TLS verification without the custom CA", func() {
			client := resource.NewACRHTTPClient(nil, false)
			Expect(resource.AcrChallengeTenant(host, false, client)).To(Equal("common"))
		})

		It("succeeds with the correct custom CA", func() {
			client := resource.NewACRHTTPClient([]string{pemCA}, false)

			Expect(resource.AcrChallengeTenant(host, false, client)).To(Equal("firewall-tenant"))

			token, err := resource.ExchangeACRRefreshToken(host, "firewall-tenant", "fake-aad-token", false, client)
			Expect(err).ToNot(HaveOccurred())
			Expect(token).To(Equal("firewall-ca-refresh-token"))
		})

		It("fails TLS verification with the wrong CA", func() {
			wrongCA := `-----BEGIN CERTIFICATE-----
MIIBkTCB+wIJALRiMLAh0IRAMA0GCSqGSIb3DQEBCwUAMBExDzANBgNVBAMMBnRl
c3RjYTAeFw0yNTAxMDEwMDAwMDBaFw0yNjAxMDEwMDAwMDBaMBExDzANBgNVBAMM
BnRlc3RjYTBcMA0GCSqGSIb3DQEBAQUAA0sAMEgCQQC7o96b0RmOMBin+VlT3VYj
b0MjBMPTGXjfVCMksSm3Mzp0FQGenNTqveQOvBGCLP0bNTqveQOvBGCLP0b0QQID
AQABo1AwTjAdBgNVHQ4EFgQUmyMx0bRkCIclxRAvfANmOAbKiRowHwYDVR0jBBgw
FoAUmyMx0bRkCIclxRAvfANmOAbKiRowDAYDVR0TBAUwAwEB/zANBgkqhkiG9w0B
AQsFAANBAAtKnMpg0SN+cseqKYAcxM1Y7g/0kPDG8YVowvOmH2+1oQERB0R/mL/y
AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=
-----END CERTIFICATE-----`
			client := resource.NewACRHTTPClient([]string{wrongCA}, false)
			Expect(resource.AcrChallengeTenant(host, false, client)).To(Equal("common"))
		})
	})

	Describe("resolveAzureCloud", func() {
		DescribeTable("resolves cloud configuration and scope",
			func(registryHost, azureEnvironment, expectedScope, expectedAuthHost string) {
				cloudConfig, scope := resource.ResolveAzureCloud(registryHost, azureEnvironment)
				Expect(scope).To(Equal(expectedScope))
				Expect(cloudConfig.ActiveDirectoryAuthorityHost).To(Equal(expectedAuthHost))
			},
			Entry("commercial registry auto-detect",
				"myregistry.azurecr.io", "",
				"https://management.azure.com/.default", "https://login.microsoftonline.com/",
			),
			Entry("government registry auto-detect",
				"myregistry.azurecr.us", "",
				"https://management.usgovcloudapi.net/.default", "https://login.microsoftonline.us/",
			),
			Entry("china registry auto-detect",
				"myregistry.azurecr.cn", "",
				"https://management.chinacloudapi.cn/.default", "https://login.chinacloudapi.cn/",
			),
			Entry("explicit override takes precedence over domain",
				"myregistry.azurecr.io", "AzureGovernment",
				"https://management.usgovcloudapi.net/.default", "https://login.microsoftonline.us/",
			),
			Entry("explicit AzureChina override",
				"myregistry.azurecr.io", "AzureChina",
				"https://management.chinacloudapi.cn/.default", "https://login.chinacloudapi.cn/",
			),
			Entry("unknown environment falls back to domain detection",
				"myregistry.azurecr.us", "InvalidCloud",
				"https://management.usgovcloudapi.net/.default", "https://login.microsoftonline.us/",
			),
			Entry("unknown domain defaults to commercial",
				"myregistry.example.com", "",
				"https://management.azure.com/.default", "https://login.microsoftonline.com/",
			),
			Entry("case-insensitive domain detection",
				"MyRegistry.AzureCR.US", "",
				"https://management.usgovcloudapi.net/.default", "https://login.microsoftonline.us/",
			),
		)

		DescribeTable("cross-cloud mismatch returns the explicitly-requested cloud",
			func(registryHost, azureEnvironment, expectedScope, expectedAuthHost string) {
				cloudConfig, scope := resource.ResolveAzureCloud(registryHost, azureEnvironment)
				Expect(scope).To(Equal(expectedScope))
				Expect(cloudConfig.ActiveDirectoryAuthorityHost).To(Equal(expectedAuthHost))
			},
			Entry("gov registry with commercial override",
				"myregistry.azurecr.us", "AzurePublic",
				"https://management.azure.com/.default", "https://login.microsoftonline.com/",
			),
			Entry("commercial registry with gov override",
				"myregistry.azurecr.io", "AzureGovernment",
				"https://management.usgovcloudapi.net/.default", "https://login.microsoftonline.us/",
			),
			Entry("china registry with commercial override",
				"myregistry.azurecr.cn", "AzurePublic",
				"https://management.azure.com/.default", "https://login.microsoftonline.com/",
			),
			Entry("commercial registry with china override",
				"myregistry.azurecr.io", "AzureChina",
				"https://management.chinacloudapi.cn/.default", "https://login.chinacloudapi.cn/",
			),
		)
	})

	Describe("AuthenticateToACR with Workload Identity", func() {
		var savedEnv map[string]string
		wiEnvKeys := []string{"AZURE_FEDERATED_TOKEN_FILE", "AZURE_TENANT_ID", "AZURE_CLIENT_ID"}

		saveAndClearEnv := func() {
			savedEnv = map[string]string{}
			for _, key := range wiEnvKeys {
				savedEnv[key] = os.Getenv(key)
				os.Unsetenv(key)
			}
		}

		restoreEnv := func() {
			for k, v := range savedEnv {
				if v != "" {
					os.Setenv(k, v)
				} else {
					os.Unsetenv(k)
				}
			}
		}

		It("fails gracefully when env vars are not set", func() {
			saveAndClearEnv()
			defer restoreEnv()

			source := &resource.Source{
				Repository: "myregistry.azurecr.io/myimage",
				AzureCredentials: resource.AzureCredentials{
					AzureACR:      true,
					AzureAuthType: "workload_identity",
				},
			}
			Expect(source.AuthenticateToACR()).To(BeFalse())
			Expect(source.Username).To(BeEmpty())
			Expect(source.Password).To(BeEmpty())
		})

		It("fails gracefully when the token file env var is missing", func() {
			saveAndClearEnv()
			defer restoreEnv()

			os.Setenv("AZURE_TENANT_ID", "fake-tenant")

			source := &resource.Source{
				Repository: "myregistry.azurecr.io/myimage",
				AzureCredentials: resource.AzureCredentials{
					AzureACR:      true,
					AzureAuthType: "workload_identity",
					AzureClientId: "explicit-client-id",
				},
			}
			Expect(source.AuthenticateToACR()).To(BeFalse())
		})

		It("fails on token acquisition with fake env vars", func() {
			saveAndClearEnv()
			defer restoreEnv()

			tmpFile, err := os.CreateTemp("", "wi-token-*")
			Expect(err).ToNot(HaveOccurred())
			defer os.Remove(tmpFile.Name())
			tmpFile.WriteString("fake-sa-token")
			tmpFile.Close()

			os.Setenv("AZURE_FEDERATED_TOKEN_FILE", tmpFile.Name())
			os.Setenv("AZURE_TENANT_ID", "fake-tenant-id")
			os.Setenv("AZURE_CLIENT_ID", "fake-client-id")

			source := &resource.Source{
				Repository: "myregistry.azurecr.io/myimage",
				AzureCredentials: resource.AzureCredentials{
					AzureACR:      true,
					AzureAuthType: "workload_identity",
				},
			}
			Expect(source.AuthenticateToACR()).To(BeFalse())
		})

		It("normalizes azure_auth_type case and whitespace", func() {
			saveAndClearEnv()
			defer restoreEnv()

			source := &resource.Source{
				Repository: "myregistry.azurecr.io/myimage",
				AzureCredentials: resource.AzureCredentials{
					AzureACR:      true,
					AzureAuthType: "  Workload_Identity  ",
				},
			}
			Expect(source.AuthenticateToACR()).To(BeFalse())
		})
	})

	Describe("azure_tenant_id skips challenge", func() {
		It("does not call /v2/ when azure_tenant_id is set", func() {
			challengeCalled := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v2/":
					challengeCalled = true
					Fail("/v2/ challenge endpoint was called despite azure_tenant_id being set")
				case "/oauth2/exchange":
					err := r.ParseForm()
					Expect(err).ToNot(HaveOccurred())
					Expect(r.FormValue("tenant")).To(Equal("explicit-acr-tenant-id"))

					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]string{
						"refresh_token": "tenant-skip-refresh-token",
					})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			host := strings.TrimPrefix(server.URL, "http://")
			tenant := "explicit-acr-tenant-id"

			token, err := resource.ExchangeACRRefreshToken(host, tenant, "fake-aad-token", true, resource.PlainHTTPClient)
			Expect(err).ToNot(HaveOccurred())
			Expect(token).To(Equal("tenant-skip-refresh-token"))
			Expect(challengeCalled).To(BeFalse())
		})

		It("calls /v2/ to discover tenant when azure_tenant_id is empty", func() {
			challengeCalled := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v2/":
					challengeCalled = true
					w.Header().Set("Www-Authenticate",
						fmt.Sprintf(`Bearer realm="http://%s/oauth2/exchange?tenant=discovered-tenant",service="%s"`, r.Host, r.Host))
					w.WriteHeader(http.StatusUnauthorized)
				case "/oauth2/exchange":
					err := r.ParseForm()
					Expect(err).ToNot(HaveOccurred())
					Expect(r.FormValue("tenant")).To(Equal("discovered-tenant"))

					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]string{
						"refresh_token": "discovered-refresh-token",
					})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()

			host := strings.TrimPrefix(server.URL, "http://")

			tenant := resource.AcrChallengeTenant(host, true, resource.PlainHTTPClient)
			Expect(tenant).To(Equal("discovered-tenant"))
			Expect(challengeCalled).To(BeTrue())

			token, err := resource.ExchangeACRRefreshToken(host, tenant, "fake-aad-token", true, resource.PlainHTTPClient)
			Expect(err).ToNot(HaveOccurred())
			Expect(token).To(Equal("discovered-refresh-token"))
		})
	})
})
