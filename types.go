package resource

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	DefaultTag                  = "latest"
	DefaultPlatformArchitecture = runtime.GOARCH
	DefaultPlatformOS           = runtime.GOOS
)

type PlatformField struct {
	Architecture string `json:"architecture,omitempty"`
	OS           string `json:"os,omitempty"`
}

type Source struct {
	Repository  string        `json:"repository"`
	RawTag      Tag           `json:"tag,omitempty"`
	RawPlatform PlatformField `json:"platform,omitempty"`

	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

	Debug bool `json:"debug,omitempty"`
}

func (source *Source) Name() string {
	return fmt.Sprintf("%s:%s", source.Repository, source.Tag())
}

func (source *Source) Platform() PlatformField {
	p := source.RawPlatform

	if p.Architecture == "" {
		p.Architecture = DefaultPlatformArchitecture
	}

	if p.OS == "" {
		p.OS = DefaultPlatformOS
	}

	return p
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
