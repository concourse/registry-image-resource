package resource

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecr"
	"github.com/sirupsen/logrus"
)

const DefaultTag = "latest"

type Source struct {
	Repository string `json:"repository"`
	RawTag     Tag    `json:"tag,omitempty"`

	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

	AwsAccessKeyId     string `json:"aws_access_key_id,omitempty"`
	AwsSecretAccessKey string `json:"aws_secret_access_key,omitempty"`
	AwsRegion          string `json:"aws_region,omitempty"`
	AwsRoleArn         string `json:"aws_role_arn,omitempty"`

	Debug bool `json:"debug,omitempty"`
}

func (source *Source) Name() string {
	return fmt.Sprintf("%s:%s", source.Repository, source.Tag())
}

func (source *Source) Tag() string {
	if source.RawTag != "" {
		return string(source.RawTag)
	}

	return DefaultTag
}

func (source *Source) Metadata() []MetadataField {
	return []MetadataField{
		MetadataField{
			Name:  "repository",
			Value: source.Repository,
		},
		MetadataField{
			Name:  "tag",
			Value: source.Tag(),
		},
	}
}

func (source *Source) MetadataWithAdditionalTags(tags []string) []MetadataField {
	return []MetadataField{
		MetadataField{
			Name:  "repository",
			Value: source.Repository,
		},
		MetadataField{
			Name:  "tags",
			Value: strings.Join(append(tags, source.Tag()), " "),
		},
	}
}

func (source *Source) AuthenticateToECR() bool {
	os.Setenv("AWS_ACCESS_KEY_ID", source.AwsAccessKeyId)
	os.Setenv("AWS_SECRET_ACCESS_KEY", source.AwsSecretAccessKey)
	os.Setenv("AWS_REGION", source.AwsRegion)
	mySession := session.Must(session.NewSession())
	client := ecr.New(mySession)
	// If a role arn has been supplied, then assume role and get a new session
	if source.AwsRoleArn != "" {
		creds := stscreds.NewCredentials(mySession, source.AwsRoleArn)
		client = ecr.New(mySession, &aws.Config{Credentials: creds})
	}
	input := &ecr.GetAuthorizationTokenInput{}
	result, err := client.GetAuthorizationToken(input)
	if err != nil {
		logrus.Errorf("Failed to authenticate to ECR: %s", err)
		return false
	}

	for _, data := range result.AuthorizationData {
		output, err := base64.StdEncoding.DecodeString(*data.AuthorizationToken)

		if err != nil {
			logrus.Errorf("Failed to decode credential (%s)", err.Error())
			return false
		}

		split := strings.Split(string(output), ":")

		if len(split) == 2 {
			source.Password = strings.TrimSpace(split[1])
		} else {
			logrus.Errorf("Failed to parse password.")
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
func (tag *Tag) UnmarshalJSON(b []byte) error {
	var n json.Number
	err := json.Unmarshal(b, &n)
	if err != nil {
		return err
	}

	*tag = Tag(n.String())

	return nil
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
