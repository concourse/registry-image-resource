package resource

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	v2aws "github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/sirupsen/logrus"
)

const (
	defaultTag               = "latest"
	getCallerIdentityService = "sts"
	getCallerIdentityRegion  = "us-east-1"
	getCallerIdentityURL     = "https://sts.amazonaws.com?Action=GetCallerIdentity&Version=2011-06-15"

	gcpScope                = "https://www.googleapis.com/auth/cloud-platform"
	generateAccessTokenAPI  = "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/%s:generateAccessToken"
	identityPoolProviderAPI = "//iam.googleapis.com/projects/%s/locations/global/workloadIdentityPools/%s/providers/%s"
)

type Source struct {
	Repository string `json:"repository"`
	RawTag     Tag    `json:"tag,omitempty"`

	Username     string        `json:"username,omitempty"`
	Password     string        `json:"password,omitempty"`
	ContentTrust *ContentTrust `json:"content_trust,omitempty"`

	AwsAccessKeyID     string `json:"aws_access_key_id,omitempty"`
	AwsSecretAccessKey string `json:"aws_secret_access_key,omitempty"`
	AwsRegion          string `json:"aws_region,omitempty"`
	AwsRoleArn         string `json:"aws_role_arn,omitempty"`

	GcpIdentityPool         string `json:"gcp_identity_pool,omitempty"`
	GcpIdentityPoolProvider string `json:"gcp_identity_pool_provider,omitempty"`
	GcpProject              string `json:"gcp_project,omitempty"`
	GcpServiceAccount       string `json:"gcp_service_account,omitempty"`

	Debug bool `json:"debug,omitempty"`
}
type ContentTrust struct {
	Server               string `json:"server"`
	RepositoryKeyID      string `json:"repository_key_id"`
	RepositoryKey        string `json:"repository_key"`
	RepositoryPassphrase string `json:"repository_passphrase"`
	TLSKey               string `json:"tls_key"`
	TLSCert              string `json:"tls_cert"`
}
type AWSExchangeToken struct {
	URL     string              `json:"url"`
	Method  string              `json:"method"`
	Headers []map[string]string `json:"headers"`
}

type TokenExchangeRequest struct {
	Audience           string `json:"audience"`
	GrantType          string `json:"grantType"`
	RequestedTokenType string `json:"requestedTokenType"`
	Scope              string `json:"scope"`
	SubjectTokenType   string `json:"subjectTokenType"`
	SubjectToken       string `json:"subjectToken"`
}

type GenerateAccessTokenRequest struct {
	Scope []string `json:"scope"`
}

type TokenExchangeResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

type SaAccessTokenResponse struct {
	AccessToken string `json:"accessToken"`
	ExpireTime  string `json:"expireTime"`
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
	return fmt.Sprintf("%s:%s", source.Repository, source.Tag())
}

func (source *Source) Tag() string {
	if source.RawTag != "" {
		return string(source.RawTag)
	}

	return defaultTag
}

func (source *Source) Metadata() []MetadataField {
	return []MetadataField{
		{
			Name:  "repository",
			Value: source.Repository,
		},
		{
			Name:  "tag",
			Value: source.Tag(),
		},
	}
}

func (source *Source) MetadataWithAdditionalTags(tags []string) []MetadataField {
	return []MetadataField{
		{
			Name:  "repository",
			Value: source.Repository,
		},
		{
			Name:  "tags",
			Value: strings.Join(append(tags, source.Tag()), " "),
		},
	}
}

func (source *Source) AuthenticateToGCP() bool {
	logrus.Info("Using AWS Role to authenticate to GCP")

	var sessionConfig aws.Config
	if source.AwsAccessKeyID != "" && source.AwsSecretAccessKey != "" {
		logrus.Info("Authenticating AWS Role with AccessKeyID & SecretAccessKey")
		sessionConfig = aws.Config{
			Region:      aws.String(source.AwsRegion),
			Credentials: credentials.NewStaticCredentials(source.AwsAccessKeyID, source.AwsSecretAccessKey, ""),
		}
	} else {
		logrus.Info("Authenticating AWS Role with container credentials")
		sessionConfig = aws.Config{
			Region: aws.String(source.AwsRegion),
		}
	}
	mySession := session.Must(session.NewSession(&sessionConfig))

	credentialsProvider := stscreds.NewCredentials(mySession, source.AwsRoleArn)
	credentials, err := credentialsProvider.Get()
	if err != nil {
		logrus.Errorf("Failed to initialize AWS credentials for %s: %s", source.AwsRoleArn, err)
		return false
	}

	// Build inner AWS sts request
	callerIdentityRequest, err := source.buildGetCallerIdentityRequest(credentials)
	if err != nil {
		logrus.Error(err)
		return false
	}

	// Build GCP request to encapsulate AWS sts request
	tokenExchangeRequest, err := source.buildTokenExchangeRequest(callerIdentityRequest, credentialsProvider)
	if err != nil {
		logrus.Errorf("failed to build TokenExchangeRequest: %s", err)
		return false
	}

	// Authenticate to GCP with encapsualted AWS sts request
	resp, err := http.DefaultClient.Do(tokenExchangeRequest)
	errMessage := "Failed to authenticate to GCP with Token Exchange Request"
	if err != nil {
		logrus.Errorf("%s: %s", errMessage, err)
		return false
	}
	if resp.StatusCode != http.StatusOK {
		logrus.Errorf("%s: %s", errMessage, resp.Status)
		return false
	}
	bodyBytes, _ := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()
	tokenExchangeResponse := TokenExchangeResponse{}
	json.Unmarshal(bodyBytes, &tokenExchangeResponse)

	// Exchange GCP federated token for service account token
	saRequest := source.buildGetServiceAccountTokenRequest(tokenExchangeResponse.AccessToken)
	resp, err = http.DefaultClient.Do(saRequest)
	if err != nil {
		logrus.Errorf("Failed to exchange Federated token for GCP Service Account credentials: %s", err)
		return false
	}
	saAccessTokenBody, _ := ioutil.ReadAll(resp.Body)
	defer resp.Body.Close()
	saAccessTokenResponse := SaAccessTokenResponse{}
	json.Unmarshal(saAccessTokenBody, &saAccessTokenResponse)

	// Set SA access token as login
	source.Username = "oauth2accesstoken"
	source.Password = saAccessTokenResponse.AccessToken

	return true
}

func (source *Source) getGoogleResource() string {
	return fmt.Sprintf(identityPoolProviderAPI, source.GcpProject, source.GcpIdentityPool, source.GcpIdentityPoolProvider)
}

func (source *Source) buildTokenExchangeRequest(req *http.Request, credentialsProvider *credentials.Credentials) (*http.Request, error) {
	credentials, err := credentialsProvider.Get()
	if err != nil {
		return nil, fmt.Errorf("failed to get AWS credentials from credentialProvider: %s", err)
	}

	googleResource := source.getGoogleResource()
	headers := []map[string]string{
		{
			"key":   "Host",
			"value": req.Header.Get("Host"),
		},
		{
			"key":   "Authorization",
			"value": req.Header.Get("Authorization"),
		},
		{
			"key":   "x-amz-date",
			"value": req.Header.Get("X-Amz-Date"),
		},
		{
			"key":   "x-goog-cloud-target-resource",
			"value": googleResource,
		},
		{
			"key":   "x-amz-security-token",
			"value": credentials.SessionToken,
		},
	}

	googleToken := AWSExchangeToken{
		URL:     getCallerIdentityURL,
		Method:  http.MethodPost,
		Headers: headers}

	googleTokenBuffer := new(bytes.Buffer)
	encoder := json.NewEncoder(googleTokenBuffer)
	encoder.SetEscapeHTML(false)
	encoder.Encode(googleToken)

	googleTokenBytes, err := ioutil.ReadAll(googleTokenBuffer)

	if err != nil {
		logrus.Errorf("Failed to encode AWS exchange token: %s", err)
	}

	googleTokenEncoded := url.QueryEscape(string(googleTokenBytes))

	tokenExchangeRequest := TokenExchangeRequest{
		Audience:           googleResource,
		GrantType:          "urn:ietf:params:oauth:grant-type:token-exchange",
		RequestedTokenType: "urn:ietf:params:oauth:token-type:access_token",
		Scope:              gcpScope,
		SubjectTokenType:   "urn:ietf:params:aws:token-type:aws4_request",
		SubjectToken:       googleTokenEncoded,
	}

	tokenExchangeRequestBytes, _ := json.Marshal(tokenExchangeRequest)

	request, _ := http.NewRequest(http.MethodPost, "https://sts.googleapis.com/v1beta/token", bytes.NewReader(tokenExchangeRequestBytes))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("Host", "https://sts.googleapis.com")

	return request, nil
}

func (source *Source) buildGetCallerIdentityRequest(credentials credentials.Value) (*http.Request, error) {
	host := getCallerIdentityURL
	currentTime := time.Now()
	amzDate := currentTime.Format("2006012T150405Z")

	body := strings.NewReader("")
	req, _ := http.NewRequest(http.MethodPost, host, body)
	req.Header.Set("X-AMZ-Date", amzDate)
	req.Header.Set("Host", "sts.amazonaws.com")
	req.Header.Set("x-goog-cloud-target-resource", source.getGoogleResource())

	h := sha256.New()
	_, _ = io.Copy(h, body)
	payloadHash := hex.EncodeToString(h.Sum(nil))

	signer := v4.NewSigner()
	err := signer.SignHTTP(
		context.Background(),
		v2aws.Credentials{
			AccessKeyID:     credentials.AccessKeyID,
			SecretAccessKey: credentials.SecretAccessKey,
			SessionToken:    credentials.SessionToken,
		},
		req,
		payloadHash,
		getCallerIdentityService,
		getCallerIdentityRegion,
		currentTime,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to add the AWS v4 signature to the STS request: %s", err)
	}

	// Adding header post signing so it doesn't get added as part of the signed headers
	// AWS doesn't care but Google validates the signature without this header before sending
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	return req, nil
}

func (source *Source) buildGetServiceAccountTokenRequest(token string) *http.Request {
	body := GenerateAccessTokenRequest{
		Scope: []string{
			gcpScope,
		},
	}

	bodyBytes, _ := json.Marshal(body)

	saRequestURL := fmt.Sprintf(generateAccessTokenAPI, source.GcpServiceAccount)

	request, _ := http.NewRequest(http.MethodPost, saRequestURL, bytes.NewReader(bodyBytes))
	request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))

	return request
}

func (source *Source) AuthenticateToECR() bool {
	logrus.Warnln("ECR integration is experimental and untested")

	var sessionConfig aws.Config
	if source.AwsAccessKeyID != "" && source.AwsSecretAccessKey != "" {
		sessionConfig = aws.Config{
			Region:      aws.String(source.AwsRegion),
			Credentials: credentials.NewStaticCredentials(source.AwsAccessKeyID, source.AwsSecretAccessKey, ""),
		}
	} else {
		sessionConfig = aws.Config{
			Region: aws.String(source.AwsRegion),
		}
	}
	mySession := session.Must(session.NewSession(&sessionConfig))

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
	// source.Repository = strings.Join([]string{strings.TrimPrefix(*result.AuthorizationData[0].ProxyEndpoint, "https://"), source.Repository}, "/")

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

type Version struct {
	Digest string `json:"digest"`
}

type MetadataField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type GetParams struct {
	RawFormat string `json:"format"`
}

func (p GetParams) Format() string {
	if p.RawFormat == "" {
		return "rootfs"
	}

	return p.RawFormat
}

type PutParams struct {
	Image          string `json:"image"`
	AdditionalTags string `json:"additional_tags"`
}

func (p *PutParams) ParseTags(src string) ([]string, error) {
	if p.AdditionalTags == "" {
		return nil, nil
	}

	filepath := filepath.Join(src, p.AdditionalTags)

	content, err := ioutil.ReadFile(filepath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file at %q: %s", filepath, err)
	}

	return strings.Fields(string(content)), nil
}
