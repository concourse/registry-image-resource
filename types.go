package resource

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/aws/aws-sdk-go/service/ecr/ecriface"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
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
	AwsAccessKeyId     string   `json:"aws_access_key_id,omitempty"`
	AwsSecretAccessKey string   `json:"aws_secret_access_key,omitempty"`
	AwsSessionToken    string   `json:"aws_session_token,omitempty"`
	AwsRegion          string   `json:"aws_region,omitempty"`
	AWSECRRegistryId   string   `json:"aws_ecr_registry_id,omitempty"`
	AwsRoleArn         string   `json:"aws_role_arn,omitempty"`
	AwsRoleArns        []string `json:"aws_role_arns,omitempty"`
	AwsAccountId       string   `json:"aws_account_id,omitempty"`
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

	PreReleases bool   `json:"pre_releases,omitempty"`
	Variant     string `json:"variant,omitempty"`

	SemverConstraint string `json:"semver_constraint,omitempty"`

	Tag Tag `json:"tag,omitempty"`

	Regex         string `json:"tag_regex,omitempty"`
	CreatedAtSort bool   `json:"created_at_sort,omitempty"`

	BasicCredentials
	AwsCredentials

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

	rt, err := transport.New(repo.Registry, auth, tr, scopes)
	if err != nil {
		return nil, fmt.Errorf("initialize transport: %w", err)
	}

	plat := source.Platform()
	v1plat := v1.Platform{
		Architecture: plat.Architecture,
		OS:           plat.OS,
	}

	return []remote.Option{remote.WithAuth(auth), remote.WithTransport(rt), remote.WithPlatform(v1plat)}, nil
}

func (source *Source) Platform() PlatformField {
	DefaultArchitecture := runtime.GOARCH
	DefaultOS := runtime.GOOS

	p := source.RawPlatform
	if p == nil {
		p = &PlatformField{}
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
	configDir, err := ioutil.TempDir("", "notary-config")
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

	err = ioutil.WriteFile(filepath.Join(configDir, "gcr-config.json"), configData, 0644)
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
	err = ioutil.WriteFile(filepath.Join(privateDir, repoKey), []byte(ct.RepositoryKey), 0600)
	if err != nil {
		return "", err
	}

	if u.Host != "" {
		certDir := filepath.Join(configDir, "tls", u.Host)
		err = os.MkdirAll(certDir, os.ModePerm)
		if err != nil {
			return "", err
		}
		err = ioutil.WriteFile(filepath.Join(certDir, "client.cert"), []byte(ct.TLSCert), 0644)
		if err != nil {
			return "", err
		}
		err = ioutil.WriteFile(filepath.Join(certDir, "client.key"), []byte(ct.TLSKey), 0644)
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
	logrus.Warnln("ECR integration is experimental and untested")

	if source.AwsRoleArn != "" && len(source.AwsRoleArns) != 0 {
		logrus.Errorf("`aws_role_arn` cannot be set at the same time as `aws_role_arns`")
		return false
	}

	awsConfig := aws.Config{
		Region: aws.String(source.AwsRegion),
	}

	if source.AwsAccessKeyId != "" && source.AwsSecretAccessKey != "" {
		awsConfig.Credentials = credentials.NewStaticCredentials(source.AwsAccessKeyId, source.AwsSecretAccessKey, source.AwsSessionToken)
	}

	mySession := session.Must(session.NewSession(&awsConfig))

	// Note: This implementation gives precedence to `aws_role_arn` since it
	// assumes that we've errored if both `aws_role_arn` and `aws_role_arns`
	// are set
	awsRoleArns := source.AwsRoleArns
	if source.AwsRoleArn != "" {
		awsRoleArns = []string{source.AwsRoleArn}
	}
	for _, roleArn := range awsRoleArns {
		logrus.Debugf("assuming new role: %s", roleArn)
		mySession = session.Must(session.NewSession(&aws.Config{
			Region:      aws.String(source.AwsRegion),
			Credentials: stscreds.NewCredentials(mySession, roleArn),
		}))
	}

	client := ecr.New(mySession)
	result, err := source.GetECRAuthorizationToken(client)
	if err != nil {
		logrus.Errorf("failed to authenticate to ECR: %s", err)
		return false
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

func (source *Source) GetECRAuthorizationToken(client ecriface.ECRAPI) (*ecr.GetAuthorizationTokenOutput, error) {
	input := &ecr.GetAuthorizationTokenInput{}
	if source.AWSECRRegistryId != "" {
		input.RegistryIds = append(input.RegistryIds, aws.String(source.AWSECRRegistryId))
	}
	return client.GetAuthorizationToken(input)
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
	RawFormat    string `json:"format"`
	SkipDownload bool   `json:"skip_download"`
}

func (p GetParams) Format() string {
	if p.RawFormat == "" {
		return "rootfs"
	}

	return p.RawFormat
}

type PutParams struct {
	// Path to an OCI image tarball to push.
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
}

func (p *PutParams) ParseAdditionalTags(src string) ([]string, error) {
	if p.AdditionalTags == "" {
		return []string{}, nil
	}

	filepath := filepath.Join(src, p.AdditionalTags)

	content, err := ioutil.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file at %q: %s", filepath, err)
	}

	return strings.Fields(string(content)), nil
}
