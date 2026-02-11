package resource

import (
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// plainHTTPClient is a test helper client used for plain-HTTP test servers.
var plainHTTPClient = &http.Client{Timeout: 5 * time.Second}

func TestParseACRChallengeTenant(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected string
	}{
		{
			name:     "standard ACR challenge with tenant",
			header:   `Bearer realm="https://myregistry.azurecr.io/oauth2/exchange?tenant=72f988bf-86f1-41af-91ab-2d7cd011db47",service="myregistry.azurecr.io"`,
			expected: "72f988bf-86f1-41af-91ab-2d7cd011db47",
		},
		{
			name:     "challenge without tenant param",
			header:   `Bearer realm="https://myregistry.azurecr.io/oauth2/exchange",service="myregistry.azurecr.io"`,
			expected: "common",
		},
		{
			name:     "empty header",
			header:   "",
			expected: "common",
		},
		{
			name:     "non-Bearer header",
			header:   `Basic realm="something"`,
			expected: "common",
		},
		{
			name:     "realm with multiple query params including tenant",
			header:   `Bearer realm="https://myregistry.azurecr.io/oauth2/exchange?foo=bar&tenant=abc-123&baz=qux",service="myregistry.azurecr.io"`,
			expected: "abc-123",
		},
		{
			name:     "Bearer with no realm",
			header:   `Bearer service="myregistry.azurecr.io"`,
			expected: "common",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseACRChallengeTenant(tt.header)
			if result != tt.expected {
				t.Errorf("parseACRChallengeTenant(%q) = %q, want %q", tt.header, result, tt.expected)
			}
		})
	}
}

func TestExchangeACRRefreshToken(t *testing.T) {
	t.Run("successful token exchange", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/oauth2/exchange" {
				t.Errorf("unexpected path: %s", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
				return
			}
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}

			err := r.ParseForm()
			if err != nil {
				t.Fatal(err)
			}

			if r.FormValue("grant_type") != "access_token" {
				t.Errorf("expected grant_type=access_token, got %s", r.FormValue("grant_type"))
			}
			if r.FormValue("access_token") != "fake-aad-token" {
				t.Errorf("expected access_token=fake-aad-token, got %s", r.FormValue("access_token"))
			}
			if r.FormValue("tenant") != "test-tenant" {
				t.Errorf("expected tenant=test-tenant, got %s", r.FormValue("tenant"))
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"refresh_token": "fake-acr-refresh-token",
			})
		}))
		defer server.Close()

		// Extract host from test server URL (strip http://)
		host := strings.TrimPrefix(server.URL, "http://")

		token, err := exchangeACRRefreshToken(host, "test-tenant", "fake-aad-token", true, plainHTTPClient)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "fake-acr-refresh-token" {
			t.Errorf("expected fake-acr-refresh-token, got %s", token)
		}
	})

	t.Run("server returns error status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, "unauthorized")
		}))
		defer server.Close()

		host := strings.TrimPrefix(server.URL, "http://")

		_, err := exchangeACRRefreshToken(host, "test-tenant", "fake-token", true, plainHTTPClient)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "status 401") {
			t.Errorf("expected error about status 401, got: %s", err)
		}
	})

	t.Run("server returns invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, "not-json")
		}))
		defer server.Close()

		host := strings.TrimPrefix(server.URL, "http://")

		_, err := exchangeACRRefreshToken(host, "test-tenant", "fake-token", true, plainHTTPClient)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "decode") {
			t.Errorf("expected decode error, got: %s", err)
		}
	})
}

func TestACRChallengeTenantIntegration(t *testing.T) {
	t.Run("parses tenant from mock ACR v2 endpoint", func(t *testing.T) {
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

		tenant := acrChallengeTenant(host, true, plainHTTPClient)
		if tenant != "test-tenant-id" {
			t.Errorf("expected test-tenant-id, got %s", tenant)
		}
	})

	t.Run("returns common when challenge has no tenant", func(t *testing.T) {
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

		tenant := acrChallengeTenant(host, true, plainHTTPClient)
		if tenant != "common" {
			t.Errorf("expected common, got %s", tenant)
		}
	})

	t.Run("returns common when server returns 200", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		host := strings.TrimPrefix(server.URL, "http://")

		tenant := acrChallengeTenant(host, true, plainHTTPClient)
		if tenant != "common" {
			t.Errorf("expected common, got %s", tenant)
		}
	})
}

func TestNewACRHTTPClient(t *testing.T) {
	t.Run("client without ca_certs uses default transport", func(t *testing.T) {
		client := newACRHTTPClient(nil, false)
		if client.Timeout != 30*time.Second {
			t.Errorf("expected 30s timeout, got %s", client.Timeout)
		}
		if client.Transport != nil {
			t.Error("expected nil transport (default) when no ca_certs provided")
		}
	})

	t.Run("client with ca_certs configures custom TLS", func(t *testing.T) {
		// Use the test server's certificate as a custom CA
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		// Extract the PEM-encoded certificate from the test server
		cert := server.TLS.Certificates[0].Certificate[0]
		pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})

		client := newACRHTTPClient([]string{string(pemCert)}, false)
		if client.Transport == nil {
			t.Fatal("expected custom transport when ca_certs provided")
		}
		tr, ok := client.Transport.(*http.Transport)
		if !ok {
			t.Fatal("expected *http.Transport")
		}
		if tr.TLSClientConfig == nil || tr.TLSClientConfig.RootCAs == nil {
			t.Fatal("expected custom RootCAs in TLS config")
		}
	})

	t.Run("client with insecure ignores ca_certs", func(t *testing.T) {
		client := newACRHTTPClient([]string{"some-cert-pem"}, true)
		if client.Transport != nil {
			t.Error("expected nil transport when insecure is true, ca_certs should be ignored")
		}
	})
}

func TestACRTokenExchangeWithCustomCA(t *testing.T) {
	// This test verifies that the ACR HTTP client correctly uses custom CA
	// certificates, as required when Azure Firewall performs TLS inspection
	// and re-signs traffic with a corporate root CA.

	// Start a TLS test server (self-signed cert — not in system trust store)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer server.Close()

	// Extract the PEM-encoded CA certificate from the test server
	serverCert := server.TLS.Certificates[0].Certificate[0]
	pemCA := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert}))

	host := strings.TrimPrefix(server.URL, "https://")

	t.Run("without custom CA, HTTPS to self-signed server fails", func(t *testing.T) {
		client := newACRHTTPClient(nil, false)
		// acrChallengeTenant should fail TLS verification and fall back to "common"
		tenant := acrChallengeTenant(host, false, client)
		if tenant != "common" {
			t.Errorf("expected common (TLS failure fallback), got %s", tenant)
		}
	})

	t.Run("with custom CA, HTTPS to self-signed server succeeds", func(t *testing.T) {
		client := newACRHTTPClient([]string{pemCA}, false)

		tenant := acrChallengeTenant(host, false, client)
		if tenant != "firewall-tenant" {
			t.Errorf("expected firewall-tenant, got %s", tenant)
		}

		token, err := exchangeACRRefreshToken(host, "firewall-tenant", "fake-aad-token", false, client)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "firewall-ca-refresh-token" {
			t.Errorf("expected firewall-ca-refresh-token, got %s", token)
		}
	})

	t.Run("wrong CA cert still fails", func(t *testing.T) {
		// A bogus PEM that won't match the server's cert
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
		client := newACRHTTPClient([]string{wrongCA}, false)
		tenant := acrChallengeTenant(host, false, client)
		if tenant != "common" {
			t.Errorf("expected common (wrong CA should fail TLS), got %s", tenant)
		}
	})
}

func TestResolveAzureCloud(t *testing.T) {
	tests := []struct {
		name             string
		registryHost     string
		azureEnvironment string
		expectedScope    string
		expectedAuthHost string
	}{
		{
			name:             "commercial registry auto-detect",
			registryHost:     "myregistry.azurecr.io",
			azureEnvironment: "",
			expectedScope:    "https://management.azure.com/.default",
			expectedAuthHost: "https://login.microsoftonline.com/",
		},
		{
			name:             "government registry auto-detect",
			registryHost:     "myregistry.azurecr.us",
			azureEnvironment: "",
			expectedScope:    "https://management.usgovcloudapi.net/.default",
			expectedAuthHost: "https://login.microsoftonline.us/",
		},
		{
			name:             "china registry auto-detect",
			registryHost:     "myregistry.azurecr.cn",
			azureEnvironment: "",
			expectedScope:    "https://management.chinacloudapi.cn/.default",
			expectedAuthHost: "https://login.chinacloudapi.cn/",
		},
		{
			name:             "explicit override takes precedence over domain",
			registryHost:     "myregistry.azurecr.io",
			azureEnvironment: "AzureGovernment",
			expectedScope:    "https://management.usgovcloudapi.net/.default",
			expectedAuthHost: "https://login.microsoftonline.us/",
		},
		{
			name:             "explicit AzureChina override",
			registryHost:     "myregistry.azurecr.io",
			azureEnvironment: "AzureChina",
			expectedScope:    "https://management.chinacloudapi.cn/.default",
			expectedAuthHost: "https://login.chinacloudapi.cn/",
		},
		{
			name:             "unknown environment falls back to domain detection",
			registryHost:     "myregistry.azurecr.us",
			azureEnvironment: "InvalidCloud",
			expectedScope:    "https://management.usgovcloudapi.net/.default",
			expectedAuthHost: "https://login.microsoftonline.us/",
		},
		{
			name:             "unknown domain defaults to commercial",
			registryHost:     "myregistry.example.com",
			azureEnvironment: "",
			expectedScope:    "https://management.azure.com/.default",
			expectedAuthHost: "https://login.microsoftonline.com/",
		},
		{
			name:             "case-insensitive domain detection",
			registryHost:     "MyRegistry.AzureCR.US",
			azureEnvironment: "",
			expectedScope:    "https://management.usgovcloudapi.net/.default",
			expectedAuthHost: "https://login.microsoftonline.us/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cloudConfig, scope := resolveAzureCloud(tt.registryHost, tt.azureEnvironment)
			if scope != tt.expectedScope {
				t.Errorf("scope = %q, want %q", scope, tt.expectedScope)
			}
			if cloudConfig.ActiveDirectoryAuthorityHost != tt.expectedAuthHost {
				t.Errorf("authority host = %q, want %q", cloudConfig.ActiveDirectoryAuthorityHost, tt.expectedAuthHost)
			}
		})
	}
}

func TestResolveAzureCloudCrossCloudMismatch(t *testing.T) {
	// These tests verify the explicit behavior when a user configures a cloud
	// that doesn't match the registry domain. The resolver returns the
	// explicitly-requested cloud regardless of domain — the real error will
	// surface when Azure AD rejects the token from the wrong cloud.
	tests := []struct {
		name             string
		registryHost     string
		azureEnvironment string
		expectedScope    string
		expectedAuthHost string
	}{
		{
			name:             "gov registry with commercial override",
			registryHost:     "myregistry.azurecr.us",
			azureEnvironment: "AzurePublic",
			expectedScope:    "https://management.azure.com/.default",
			expectedAuthHost: "https://login.microsoftonline.com/",
		},
		{
			name:             "commercial registry with gov override",
			registryHost:     "myregistry.azurecr.io",
			azureEnvironment: "AzureGovernment",
			expectedScope:    "https://management.usgovcloudapi.net/.default",
			expectedAuthHost: "https://login.microsoftonline.us/",
		},
		{
			name:             "china registry with commercial override",
			registryHost:     "myregistry.azurecr.cn",
			azureEnvironment: "AzurePublic",
			expectedScope:    "https://management.azure.com/.default",
			expectedAuthHost: "https://login.microsoftonline.com/",
		},
		{
			name:             "commercial registry with china override",
			registryHost:     "myregistry.azurecr.io",
			azureEnvironment: "AzureChina",
			expectedScope:    "https://management.chinacloudapi.cn/.default",
			expectedAuthHost: "https://login.chinacloudapi.cn/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cloudConfig, scope := resolveAzureCloud(tt.registryHost, tt.azureEnvironment)
			if scope != tt.expectedScope {
				t.Errorf("scope = %q, want %q", scope, tt.expectedScope)
			}
			if cloudConfig.ActiveDirectoryAuthorityHost != tt.expectedAuthHost {
				t.Errorf("authority host = %q, want %q", cloudConfig.ActiveDirectoryAuthorityHost, tt.expectedAuthHost)
			}
		})
	}
}

func TestAuthenticateToACRWorkloadIdentity(t *testing.T) {
	// These tests verify the Workload Identity code path in AuthenticateToACR.
	// Since we cannot mock the Azure SDK credential easily, we test that:
	// 1) The correct credential type is attempted based on azure_auth_type
	// 2) Failure is clean and returns false with appropriate errors

	t.Run("workload_identity fails gracefully when env vars not set", func(t *testing.T) {
		// Ensure WI-related env vars are not set
		orig := map[string]string{}
		for _, key := range []string{"AZURE_FEDERATED_TOKEN_FILE", "AZURE_TENANT_ID", "AZURE_CLIENT_ID"} {
			orig[key] = os.Getenv(key)
			os.Unsetenv(key)
		}
		defer func() {
			for k, v := range orig {
				if v != "" {
					os.Setenv(k, v)
				}
			}
		}()

		source := &Source{
			Repository: "myregistry.azurecr.io/myimage",
			AzureCredentials: AzureCredentials{
				AzureACR:      true,
				AzureAuthType: "workload_identity",
			},
		}
		result := source.AuthenticateToACR()
		if result {
			t.Error("expected AuthenticateToACR to return false when WI env vars are missing")
		}
		// Username/Password should remain unset
		if source.Username != "" {
			t.Errorf("expected empty username, got %q", source.Username)
		}
		if source.Password != "" {
			t.Errorf("expected empty password, got %q", source.Password)
		}
	})

	t.Run("workload_identity with azure_client_id fails gracefully when token file missing", func(t *testing.T) {
		orig := map[string]string{}
		for _, key := range []string{"AZURE_FEDERATED_TOKEN_FILE", "AZURE_TENANT_ID", "AZURE_CLIENT_ID"} {
			orig[key] = os.Getenv(key)
		}
		// Set only tenant and client, but NO token file
		os.Setenv("AZURE_TENANT_ID", "fake-tenant")
		os.Unsetenv("AZURE_FEDERATED_TOKEN_FILE")
		defer func() {
			for k, v := range orig {
				if v != "" {
					os.Setenv(k, v)
				} else {
					os.Unsetenv(k)
				}
			}
		}()

		source := &Source{
			Repository: "myregistry.azurecr.io/myimage",
			AzureCredentials: AzureCredentials{
				AzureACR:      true,
				AzureAuthType: "workload_identity",
				AzureClientId: "explicit-client-id",
			},
		}
		result := source.AuthenticateToACR()
		if result {
			t.Error("expected AuthenticateToACR to return false when token file env var is missing")
		}
	})

	t.Run("workload_identity with all env vars set creates credential but fails on token acquisition", func(t *testing.T) {
		// Create a temporary token file
		tmpFile, err := os.CreateTemp("", "wi-token-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpFile.Name())
		tmpFile.WriteString("fake-sa-token")
		tmpFile.Close()

		orig := map[string]string{}
		for _, key := range []string{"AZURE_FEDERATED_TOKEN_FILE", "AZURE_TENANT_ID", "AZURE_CLIENT_ID"} {
			orig[key] = os.Getenv(key)
		}
		os.Setenv("AZURE_FEDERATED_TOKEN_FILE", tmpFile.Name())
		os.Setenv("AZURE_TENANT_ID", "fake-tenant-id")
		os.Setenv("AZURE_CLIENT_ID", "fake-client-id")
		defer func() {
			for k, v := range orig {
				if v != "" {
					os.Setenv(k, v)
				} else {
					os.Unsetenv(k)
				}
			}
		}()

		source := &Source{
			Repository: "myregistry.azurecr.io/myimage",
			AzureCredentials: AzureCredentials{
				AzureACR:      true,
				AzureAuthType: "workload_identity",
			},
		}
		// Credential creation should succeed (env vars present), but token
		// acquisition will fail because there is no real AAD endpoint serving
		// this tenant. AuthenticateToACR should return false.
		result := source.AuthenticateToACR()
		if result {
			t.Error("expected AuthenticateToACR to return false with fake WI env vars")
		}
	})

	t.Run("workload_identity case insensitive and trimmed", func(t *testing.T) {
		orig := map[string]string{}
		for _, key := range []string{"AZURE_FEDERATED_TOKEN_FILE", "AZURE_TENANT_ID", "AZURE_CLIENT_ID"} {
			orig[key] = os.Getenv(key)
			os.Unsetenv(key)
		}
		defer func() {
			for k, v := range orig {
				if v != "" {
					os.Setenv(k, v)
				}
			}
		}()

		// "Workload_Identity" should be normalized to "workload_identity"
		// and still take the WI code path (which fails because env vars are missing)
		source := &Source{
			Repository: "myregistry.azurecr.io/myimage",
			AzureCredentials: AzureCredentials{
				AzureACR:      true,
				AzureAuthType: "  Workload_Identity  ",
			},
		}
		result := source.AuthenticateToACR()
		if result {
			t.Error("expected AuthenticateToACR to return false")
		}
	})

	// NOTE: Tests for the default Managed Identity path (empty/unknown azure_auth_type)
	// are not included here because ManagedIdentityCredential.GetToken() contacts the
	// IMDS endpoint at 169.254.169.254, which hangs on non-Azure machines. The MI path
	// is validated via the live QA tests on an Azure VM. The struct parsing for
	// azure_auth_type is tested in types_test.go.
}

func TestAuthenticateToACRCrossCloudMismatch(t *testing.T) {
	// The resolveAzureCloud function is tested above (TestResolveAzureCloudCrossCloudMismatch)
	// to verify it returns the explicitly-requested cloud configuration regardless of domain.
	//
	// Full AuthenticateToACR cross-cloud tests are not included here because they require
	// ManagedIdentityCredential which contacts IMDS (169.254.169.254) and hangs on
	// non-Azure machines. Cross-cloud mismatch is validated via:
	//   1. TestResolveAzureCloudCrossCloudMismatch (pure function, no network)
	//   2. Live QA tests on an Azure Gov VM (see prompt8.md)

	t.Run("resolveAzureCloud applies explicit override regardless of domain", func(t *testing.T) {
		// Gov registry + Commercial override → should return Commercial scope
		_, scope := resolveAzureCloud("govregistry.azurecr.us", "AzurePublic")
		if scope != "https://management.azure.com/.default" {
			t.Errorf("expected Commercial scope, got %s", scope)
		}

		// Commercial registry + Gov override → should return Gov scope
		_, scope = resolveAzureCloud("myregistry.azurecr.io", "AzureGovernment")
		if scope != "https://management.usgovcloudapi.net/.default" {
			t.Errorf("expected Government scope, got %s", scope)
		}
	})
}
