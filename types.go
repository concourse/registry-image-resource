package resource

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecr"
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
	AwsRoleArn         string `json:"aws_role_arn,omitempty"`
}

type BasicCredentials struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

type RegistryMirror struct {
	Host string `json:"host,omitempty"`

	BasicCredentials
}

type Source struct {
	Repository string `json:"repository"`

	PreReleases bool   `json:"pre_releases,omitempty"`
	Variant     string `json:"variant,omitempty"`

	Tag Tag `json:"tag,omitempty"`

	BasicCredentials
	AwsCredentials

	RegistryMirror *RegistryMirror `json:"registry_mirror,omitempty"`

	ContentTrust *ContentTrust `json:"content_trust,omitempty"`

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

func (source Source) AuthOptions(repo name.Repository) ([]remote.Option, error) {
	var auth authn.Authenticator
	if source.Username != "" && source.Password != "" {
		auth = &authn.Basic{
			Username: source.Username,
			Password: source.Password,
		}
	} else {
		auth = authn.Anonymous
	}

	opts := []remote.Option{remote.WithAuth(auth)}

	rt, err := transport.New(repo.Registry, auth, http.DefaultTransport, []string{repo.Scope(transport.PullScope)})
	if err != nil {
		return nil, fmt.Errorf("initialize transport: %w", err)
	}

	opts = append(opts, remote.WithTransport(rt))

	return opts, nil
}

type ContentTrust struct {
	Server               string `json:"server"`
	RepositoryKeyID      string `json:"repository_key_id"`
	RepositoryKey        string `json:"repository_key"`
	RepositoryPassphrase string `json:"repository_passphrase"`
	TLSKey               string `json:"tls_key"`
	TLSCert              string `json:"tls_cert"`
}

/* Create notary config directory with following structure
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
	mySession := session.Must(session.NewSession(&aws.Config{
		Region:      aws.String(source.AwsRegion),
		Credentials: credentials.NewStaticCredentials(source.AwsAccessKeyId, source.AwsSecretAccessKey, source.AwsSessionToken),
	}))

	var config aws.Config

	// If a role arn has been supplied, then assume role and get a new session
	if source.AwsRoleArn != "" {
		config = aws.Config{Credentials: stscreds.NewCredentials(mySession, source.AwsRoleArn)}
	}

	client := ecr.New(mySession, &config)

	input := &ecr.GetAuthorizationTokenInput{}
	result, err := client.GetAuthorizationToken(input)
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
	source.Repository = strings.Join([]string{strings.TrimPrefix(*result.AuthorizationData[0].ProxyEndpoint, "https://"), source.Repository}, "/")

	return true
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
