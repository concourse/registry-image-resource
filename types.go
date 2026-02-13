package resource

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/sirupsen/logrus"
)

type CheckRequest struct {
	Source  Source   `json:"source"`
	Version *Version `json:"version"`
}

type CheckResponse []Version

type InRequest struct {
	Source  Source    `json:"source"`
	Params  GetParams `json:"params"`
	Version Version   `json:"version"`
}

type InResponse struct {
	Version  Version         `json:"version"`
	Metadata []MetadataField `json:"metadata"`
}

type OutRequest struct {
	Source Source    `json:"source"`
	Params PutParams `json:"params"`
}

type OutResponse struct {
	Version  Version         `json:"version"`
	Metadata []MetadataField `json:"metadata"`
}

type AwsCredentials struct {
	AwsAccessKeyId     string `json:"aws_access_key_id,omitempty"`
	AwsSecretAccessKey string `json:"aws_secret_access_key,omitempty"`
	AwsSessionToken    string `json:"aws_session_token,omitempty"`
	AwsRegion          string `json:"aws_region,omitempty"`
	// Deprecated: No longer required for cross-account ECR access
	AWSECRRegistryId string   `json:"aws_ecr_registry_id,omitempty"`
	AwsRoleArn       string   `json:"aws_role_arn,omitempty"`
	AwsRoleArns      []string `json:"aws_role_arns,omitempty"`
	AwsAccountId     string   `json:"aws_account_id,omitempty"`
}

type AzureCredentials struct {
	AzureACR         bool   `json:"azure_acr,omitempty"`
	AzureClientId    string `json:"azure_client_id,omitempty"`
	AzureTenantId    string `json:"azure_tenant_id,omitempty"`
	AzureEnvironment string `json:"azure_environment,omitempty"`
	AzureAuthType    string `json:"azure_auth_type,omitempty"`
}

type BasicCredentials struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type RegistryMirror struct {
	Host string `json:"host,omitempty"`

	BasicCredentials
}

type PlatformField struct {
	Architecture string `json:"architecture,omitempty"`
	OS           string `json:"os,omitempty"`
}

type Source struct {
	Repository string `json:"repository"`

	Insecure bool `json:"insecure"`

	PreReleases        bool     `json:"pre_releases,omitempty"`
	PreReleasePrefixes []string `json:"pre_release_prefixes,omitempty"`
	Variant            string   `json:"variant,omitempty"`

	SemverConstraint string `json:"semver_constraint,omitempty"`

	Tag Tag `json:"tag,omitempty"`

	Regex         string `json:"tag_regex,omitempty"`
	CreatedAtSort bool   `json:"created_at_sort,omitempty"`

	BasicCredentials
	AwsCredentials
	AzureCredentials

	RegistryMirror *RegistryMirror `json:"registry_mirror,omitempty"`

	ContentTrust *ContentTrust `json:"content_trust,omitempty"`

	DomainCerts []string `json:"ca_certs,omitempty"`

	RawPlatform *PlatformField `json:"platform,omitempty"`

	Debug bool `json:"debug,omitempty"`
}

func (source Source) Mirror() (Source, bool, error) {
	if source.RegistryMirror == nil {
		return Source{}, false, nil
	}

	repo, err := name.NewRepository(source.Repository)
	if err != nil {
		return Source{}, false, fmt.Errorf("parse repository: %w", err)
	}

	if repo.Registry.String() != name.DefaultRegistry {
		// only use registry_mirror for the default registry so that a mirror can
		// be configured as a global default
		//
		// note that this matches the behavior of the `docker` CLI
		return Source{}, false, nil
	}

	// resolve implicit namespace by re-parsing .Name()
	mirror, err := name.NewRepository(repo.Name())
	if err != nil {
		return Source{}, false, fmt.Errorf("resolve implicit namespace: %w", err)
	}

	mirror.Registry, err = name.NewRegistry(source.RegistryMirror.Host)
	if err != nil {
		return Source{}, false, fmt.Errorf("parse mirror registry: %w", err)
	}

	copy := source
	copy.Repository = mirror.Name()
	copy.BasicCredentials = source.RegistryMirror.BasicCredentials
	copy.RegistryMirror = nil

	return copy, true, nil
}

type Options struct {
	Name       []name.Option
	Remote     []remote.Option
	Repository name.Repository
}

func (source Source) NewOptions() Options {
	return Options{}
}

func (source Source) SetOptions(opts *Options) error {
	opts.Name = source.RepositoryOptions()

	r, err := name.NewRepository(source.Repository, opts.Name...)
	if err != nil {
		return fmt.Errorf("resolve repository name: %w", err)
	}
	opts.Repository = r

	opts.Remote, err = source.AuthOptions(r, []string{transport.PushScope})
	if err != nil {
		return err
	}

	return nil
}

func (source Source) AuthOptions(repo name.Repository, scopeActions []string) ([]remote.Option, error) {
	ctx := context.Background()
	var auth authn.Authenticator
	if source.Username != "" && source.Password != "" {
		auth = &authn.Basic{
			Username: source.Username,
			Password: source.Password,
		}
	} else {
		auth = authn.Anonymous
	}

	tr := http.DefaultTransport.(*http.Transport)
	// a cert was provided
	if len(source.DomainCerts) > 0 {
		rootCAs, err := x509.SystemCertPool()
		if err != nil {
			return nil, err
		}
		if rootCAs == nil {
			rootCAs = x509.NewCertPool()
		}

		for _, cert := range source.DomainCerts {
			// append our cert to the system pool
			if ok := rootCAs.AppendCertsFromPEM([]byte(cert)); !ok {
				return nil, fmt.Errorf("failed to append registry certificate: %w", err)
			}
		}

		// trust the augmented cert pool in our client
		config := &tls.Config{
			RootCAs: rootCAs,
		}

		tr.TLSClientConfig = config
	}

	scopes := make([]string, len(scopeActions))
	for i, action := range scopeActions {
		scopes[i] = repo.Scope(action)
	}

	rt, err := transport.NewWithContext(ctx, repo.Registry, auth, tr, scopes)
	if err != nil {
		return nil, fmt.Errorf("initialize transport: %w", err)
	}

	return []remote.Option{remote.WithAuth(auth), remote.WithTransport(rt)}, nil
}

func (source *Source) Platform(stepOverride *PlatformField) PlatformField {
	DefaultArchitecture := runtime.GOARCH
	DefaultOS := runtime.GOOS

	p := source.RawPlatform
	if p == nil {
		p = &PlatformField{}
	}

	if stepOverride != nil {
		p = stepOverride
	}

	if p.Architecture == "" {
		p.Architecture = DefaultArchitecture
	}

	if p.OS == "" {
		p.OS = DefaultOS
	}

	return *p
}

func (source Source) NewRepository() (name.Repository, error) {
	return name.NewRepository(source.Repository, source.RepositoryOptions()...)
}

func (source Source) RepositoryOptions() []name.Option {
	var opts []name.Option
	if source.Insecure {
		opts = append(opts, name.Insecure)
	}
	return opts
}

type ContentTrust struct {
	Server               string `json:"server"`
	RepositoryKeyID      string `json:"repository_key_id"`
	RepositoryKey        string `json:"repository_key"`
	RepositoryPassphrase string `json:"repository_passphrase"`
	TLSKey               string `json:"tls_key"`
	TLSCert              string `json:"tls_cert"`
	Scopes               string `json:"scopes,omitempty"`

	BasicCredentials
}

/*
	Create notary config directory with following structure

├── gcr-config.json
└── trust

	└── private
		└── <private-key-id>.key

└── tls

	└── <notary-host>
		├── client.cert
		└── client.key
*/
func (ct *ContentTrust) PrepareConfigDir() (string, error) {
	configDir, err := os.MkdirTemp("", "notary-config")
	if err != nil {
		return "", err
	}

	configObj := make(map[string]string)
	configObj["server_url"] = ct.Server
	configObj["root_passphrase"] = ""
	configObj["repository_passphrase"] = ct.RepositoryPassphrase
	if ct.Scopes == "" {
		configObj["scopes"] = transport.PushScope
	} else {
		configObj["scopes"] = ct.Scopes
	}

	configData, err := json.Marshal(configObj)
	if err != nil {
		return "", err
	}

	err = os.WriteFile(filepath.Join(configDir, "gcr-config.json"), configData, 0644)
	if err != nil {
		return "", err
	}

	u, err := url.Parse(ct.Server)
	if err != nil {
		return "", err
	}

	privateDir := filepath.Join(configDir, "trust", "private")
	err = os.MkdirAll(privateDir, os.ModePerm)
	if err != nil {
		return "", err
	}

	repoKey := fmt.Sprintf("%s.key", ct.RepositoryKeyID)
	err = os.WriteFile(filepath.Join(privateDir, repoKey), []byte(ct.RepositoryKey), 0600)
	if err != nil {
		return "", err
	}

	if u.Host != "" {
		certDir := filepath.Join(configDir, "tls", u.Host)
		err = os.MkdirAll(certDir, os.ModePerm)
		if err != nil {
			return "", err
		}
		err = os.WriteFile(filepath.Join(certDir, "client.cert"), []byte(ct.TLSCert), 0644)
		if err != nil {
			return "", err
		}
		err = os.WriteFile(filepath.Join(certDir, "client.key"), []byte(ct.TLSKey), 0644)
		if err != nil {
			return "", err
		}
	}

	return configDir, nil
}

func (source *Source) Name() string {
	if source.Tag == "" {
		return source.Repository
	}

	return fmt.Sprintf("%s:%s", source.Repository, source.Tag)
}

func (source *Source) Metadata() []MetadataField {
	return []MetadataField{
		{
			Name:  "repository",
			Value: source.Repository,
		},
	}
}

func (source *Source) AuthenticateToECR() bool {
	if source.AwsRoleArn != "" && len(source.AwsRoleArns) != 0 {
		logrus.Errorf("`aws_role_arn` cannot be set at the same time as `aws_role_arns`")
		return false
	}

	awsConfig, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(source.AwsRegion))
	if err != nil {
		logrus.Error("error creating aws config:", err)
		return false
	}

	if source.AwsAccessKeyId != "" && source.AwsSecretAccessKey != "" {
		appCreds := aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(source.AwsAccessKeyId, source.AwsSecretAccessKey, source.AwsSessionToken))
		_, err := appCreds.Retrieve(context.TODO())
		if err != nil {
			logrus.Error("error using static credentials:", err)
			return false
		}

		awsConfig.Credentials = appCreds
	}

	// Note: This implementation gives precedence to `aws_role_arn` since it
	// assumes that we've errored if both `aws_role_arn` and `aws_role_arns`
	// are set
	awsRoleArns := source.AwsRoleArns
	if source.AwsRoleArn != "" {
		awsRoleArns = []string{source.AwsRoleArn}
	}
	for _, roleArn := range awsRoleArns {
		logrus.Debugf("assuming role: %s", roleArn)
		stsClient := sts.NewFromConfig(awsConfig)
		roleCreds := stscreds.NewAssumeRoleProvider(stsClient, roleArn)
		creds, err := roleCreds.Retrieve(context.Background())
		if err != nil {
			logrus.Errorf("error assuming role '%s': %s", roleArn, err.Error())
			return false
		}

		awsConfig.Credentials = aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(
			creds.AccessKeyID,
			creds.SecretAccessKey,
			creds.SessionToken),
		)
	}

	client := ecr.NewFromConfig(awsConfig)
	result, err := client.GetAuthorizationToken(context.TODO(), &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		logrus.Errorf("failed to authenticate to ECR: %s", err)
		return false
	}

	if source.AWSECRRegistryId != "" {
		logrus.Warn("aws_ecr_registry_id is no longer required. This param may be removed in a future version of this resource-type")
	}

	for _, data := range result.AuthorizationData {
		output, err := base64.StdEncoding.DecodeString(*data.AuthorizationToken)

		if err != nil {
			logrus.Errorf("failed to decode credential (%s)", err.Error())
			return false
		}

		split := strings.Split(string(output), ":")

		if len(split) == 2 {
			source.Password = strings.TrimSpace(split[1])
		} else {
			logrus.Errorf("failed to parse password.")
			return false
		}
	}

	// Update username and repository
	source.Username = "AWS"

	if source.AwsAccountId != "" {
		source.Repository = fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com/%s", source.AwsAccountId, source.AwsRegion, source.Repository)
	} else {
		source.Repository = fmt.Sprintf("%s/%s", strings.TrimPrefix(*result.AuthorizationData[0].ProxyEndpoint, "https://"), source.Repository)
	}

	return true
}

func (source *Source) AuthenticateToACR() bool {
	repo, err := name.NewRepository(source.Repository, source.RepositoryOptions()...)
	if err != nil {
		logrus.Errorf("failed to parse repository for ACR auth: %s", err)
		return false
	}

	registryHost := repo.RegistryStr()

	// Determine Azure cloud configuration from explicit override or registry domain
	azCloud, managementScope := resolveAzureCloud(registryHost, source.AzureEnvironment)
	logrus.Debugf("ACR auth using cloud with authority %s, scope %s", azCloud.ActiveDirectoryAuthorityHost, managementScope)

	// Step 1: Acquire Azure AD token via the configured credential type
	var cred azcore.TokenCredential
	authType := strings.ToLower(strings.TrimSpace(source.AzureAuthType))

	switch authType {
	case "workload_identity":
		logrus.Debugf("using Workload Identity credential for ACR auth")
		wiOpts := &azidentity.WorkloadIdentityCredentialOptions{}
		wiOpts.ClientOptions.Cloud = azCloud
		if source.AzureClientId != "" {
			wiOpts.ClientID = source.AzureClientId
		}
		cred, err = azidentity.NewWorkloadIdentityCredential(wiOpts)
		if err != nil {
			logrus.Errorf("failed to create Workload Identity credential: %s", err)
			return false
		}

	default:
		// Default: Managed Identity (covers System-Assigned, User-Assigned, and AKS Kubelet Identity)
		miOpts := &azidentity.ManagedIdentityCredentialOptions{}
		miOpts.ClientOptions.Cloud = azCloud
		if source.AzureClientId != "" {
			logrus.Debugf("using User-Assigned Managed Identity for ACR auth")
			miOpts.ID = azidentity.ClientID(source.AzureClientId)
		} else {
			logrus.Debugf("using System-Assigned Managed Identity for ACR auth")
		}
		cred, err = azidentity.NewManagedIdentityCredential(miOpts)
		if err != nil {
			logrus.Errorf("failed to create Managed Identity credential: %s", err)
			return false
		}
	}

	token, err := cred.GetToken(context.TODO(), policy.TokenRequestOptions{
		Scopes: []string{managementScope},
	})
	if err != nil {
		logrus.Errorf("failed to acquire Azure AD token: %s", err)
		return false
	}

	// Build HTTP client for ACR token operations, honouring ca_certs
	acrClient := newACRHTTPClient(source.DomainCerts, source.Insecure)

	// Step 2: Determine the ACR tenant.
	// If azure_tenant_id is explicitly configured, use it directly and skip the
	// challenge roundtrip to /v2/. This saves one HTTP request per check/get/put.
	// Do NOT fall back to the AZURE_TENANT_ID env var — that is the cluster's
	// tenant, which may differ from the ACR's tenant in cross-tenant scenarios.
	var tenant string
	if source.AzureTenantId != "" {
		tenant = source.AzureTenantId
		logrus.Debugf("using explicit azure_tenant_id for ACR auth: %s", tenant)
	} else {
		tenant = acrChallengeTenant(registryHost, source.Insecure, acrClient)
		logrus.Debugf("ACR challenge tenant: %s", tenant)
	}

	// Step 3: Exchange AAD token for ACR refresh token
	logrus.Debugf("exchanging AAD token for ACR refresh token at %s (tenant=%s)", registryHost, tenant)
	refreshToken, err := exchangeACRRefreshToken(registryHost, tenant, token.Token, source.Insecure, acrClient)
	if err != nil {
		logrus.Errorf("failed to exchange token for ACR refresh token: %s", err)
		if source.AzureTenantId != "" {
			logrus.Errorf("hint: azure_tenant_id must be the ACR registry's tenant ID, not the VM or cluster tenant ID. " +
				"Find it with: az acr show --name <registry> --query loginServer --output tsv or check the Azure Portal.")
		}
		return false
	}

	if refreshToken == "" {
		logrus.Errorf("received empty ACR refresh token")
		return false
	}

	// Step 4: Set credentials on source
	source.Username = "00000000-0000-0000-0000-000000000000"
	source.Password = refreshToken

	logrus.Debugf("successfully authenticated to ACR: %s", registryHost)
	return true
}

// resolveAzureCloud determines the Azure cloud configuration and management token
// scope from the registry hostname or an explicit environment override.
//
// Registry domain auto-detection:
//   - *.azurecr.io  → AzurePublic  (Commercial)
//   - *.azurecr.us  → AzureGovernment
//   - *.azurecr.cn  → AzureChina
//
// Explicit azure_environment values: "AzurePublic", "AzureGovernment", "AzureChina"
// The explicit value takes precedence over auto-detection.
func resolveAzureCloud(registryHost, azureEnvironment string) (cloud.Configuration, string) {
	type azureCloudInfo struct {
		cloud cloud.Configuration
		scope string
	}

	clouds := map[string]azureCloudInfo{
		"AzurePublic": {
			cloud: cloud.AzurePublic,
			scope: "https://management.azure.com/.default",
		},
		"AzureGovernment": {
			cloud: cloud.AzureGovernment,
			scope: "https://management.usgovcloudapi.net/.default",
		},
		"AzureChina": {
			cloud: cloud.AzureChina,
			scope: "https://management.chinacloudapi.cn/.default",
		},
	}

	// Explicit override takes precedence
	if azureEnvironment != "" {
		if info, ok := clouds[azureEnvironment]; ok {
			return info.cloud, info.scope
		}
		logrus.Warnf("unknown azure_environment %q, falling back to auto-detection", azureEnvironment)
	}

	// Auto-detect from registry domain suffix
	host := strings.ToLower(registryHost)
	switch {
	case strings.HasSuffix(host, ".azurecr.us"):
		return clouds["AzureGovernment"].cloud, clouds["AzureGovernment"].scope
	case strings.HasSuffix(host, ".azurecr.cn"):
		return clouds["AzureChina"].cloud, clouds["AzureChina"].scope
	default:
		// *.azurecr.io or any other domain defaults to Commercial
		return clouds["AzurePublic"].cloud, clouds["AzurePublic"].scope
	}
}

// newACRHTTPClient builds an HTTP client for ACR token operations with a
// 30-second timeout. When domainCerts are provided and insecure is false, the
// client's TLS configuration trusts both the system certificate pool and the
// supplied PEM certificates — matching the behaviour of AuthOptions() for
// registry operations. This is required when Azure Firewall or a similar
// TLS-inspecting proxy re-signs traffic with a custom root CA.
func newACRHTTPClient(domainCerts []string, insecure bool) *http.Client {
	client := &http.Client{Timeout: 30 * time.Second}

	if !insecure && len(domainCerts) > 0 {
		rootCAs, err := x509.SystemCertPool()
		if err != nil {
			logrus.Debugf("failed to load system cert pool, creating empty pool: %s", err)
			rootCAs = x509.NewCertPool()
		}
		if rootCAs == nil {
			rootCAs = x509.NewCertPool()
		}

		for _, cert := range domainCerts {
			if ok := rootCAs.AppendCertsFromPEM([]byte(cert)); !ok {
				logrus.Warnf("failed to append a CA certificate to ACR HTTP client pool")
			}
		}

		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: rootCAs,
			},
		}
	}

	return client
}

// acrChallengeTenant performs an unauthenticated request to the ACR /v2/
// endpoint and parses the tenant from the Www-Authenticate challenge header.
func acrChallengeTenant(registryHost string, insecure bool, client *http.Client) string {
	scheme := "https"
	if insecure {
		scheme = "http"
	}

	resp, err := client.Get(fmt.Sprintf("%s://%s/v2/", scheme, registryHost))
	if err != nil {
		logrus.Debugf("ACR challenge request failed, using default tenant: %s", err)
		return "common"
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		logrus.Debugf("ACR challenge returned status %d (expected 401), using default tenant", resp.StatusCode)
		return "common"
	}

	return parseACRChallengeTenant(resp.Header.Get("Www-Authenticate"))
}

// acrTenantRegexp extracts the tenant parameter from an ACR Www-Authenticate header.
var acrTenantRegexp = regexp.MustCompile(`tenant=([^"&]+)`)

// parseACRChallengeTenant extracts the tenant from a Www-Authenticate header.
// The header format is: Bearer realm="https://<host>/oauth2/exchange?tenant=<tid>",service="<host>"
// Returns "common" if the tenant cannot be parsed.
func parseACRChallengeTenant(wwwAuthenticate string) string {
	if wwwAuthenticate == "" || !strings.HasPrefix(wwwAuthenticate, "Bearer ") {
		return "common"
	}

	match := acrTenantRegexp.FindStringSubmatch(wwwAuthenticate)
	if len(match) < 2 || match[1] == "" {
		return "common"
	}

	return match[1]
}

// exchangeACRRefreshToken exchanges an Azure AD access token for an ACR refresh token.
func exchangeACRRefreshToken(registryHost, tenant, accessToken string, insecure bool, client *http.Client) (string, error) {
	scheme := "https"
	if insecure {
		scheme = "http"
	}

	exchangeURL := fmt.Sprintf("%s://%s/oauth2/exchange", scheme, registryHost)

	resp, err := client.PostForm(exchangeURL, url.Values{
		"grant_type":   {"access_token"},
		"service":      {registryHost},
		"tenant":       {tenant},
		"access_token": {accessToken},
	})
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", exchangeURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("ACR token exchange returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode ACR token exchange response: %w", err)
	}

	return result.RefreshToken, nil
}

// Tag refers to a tag for an image in the registry.
type Tag string

// UnmarshalJSON accepts numeric and string values.
func (tag *Tag) UnmarshalJSON(b []byte) (err error) {
	var s string
	if err = json.Unmarshal(b, &s); err == nil {
		*tag = Tag(s)
	} else {
		var n json.RawMessage
		if err = json.Unmarshal(b, &n); err == nil {
			*tag = Tag(n)
		}
	}
	return err
}

func (tag Tag) String() string {
	return string(tag)
}

type Version struct {
	Tag    string `json:"tag"`
	Digest string `json:"digest"`
}

type MetadataField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type GetParams struct {
	RawFormat    string         `json:"format"`
	RawPlatform  *PlatformField `json:"platform,omitempty"`
	SkipDownload bool           `json:"skip_download"`
}

func (p GetParams) Format() string {
	if p.RawFormat == "" {
		return "rootfs"
	}

	return p.RawFormat
}

type PutParams struct {
	// Path to an OCI image tarball or directory in OCI-layout format to push.
	Image string `json:"image"`

	// Version number to publish. If a variant is configured, it will be
	// appended to this value to form the tag.
	Version string `json:"version"`

	// Bump additional alias tags after pushing the version's tag.
	//
	// Given a version without a prerelease, say 1.2.3:
	//
	// * If 1.2.3 is the latest of the 1.2.x series, push to the 1.2 tag.
	//
	// * If 1.2.3 is the latest of the 1.x series, push to the 1 tag.
	//
	// * If 1.2.3 is the latest overall, bump the variant tag, or 'latest'
	//   if no variant is configured.
	BumpAliases bool `json:"bump_aliases"`

	// Path to a file containing line-separated tags to push.
	AdditionalTags string `json:"additional_tags"`

	// String that will be prefixed to all tags from AdditionalTags
	TagPrefix string `json:"tag_prefix"`
}

func (p *PutParams) ParseAdditionalTags(src string) ([]string, error) {
	if p.AdditionalTags == "" {
		return []string{}, nil
	}

	filepath := filepath.Join(src, p.AdditionalTags)

	content, err := os.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file at %q: %s", filepath, err)
	}

	return strings.Fields(string(content)), nil
}
