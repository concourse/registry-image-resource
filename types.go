package resource

import (
	"encoding/json"
	"fmt"
)

const DefaultTag = "latest"

type Source struct {
	Repository string `json:"repository"`
	Tag        Tag    `json:"tag"`

	Username string `json:"username"`
	Password string `json:"password"`

	Debug bool `json:"debug"`
}

func (source *Source) Name() string {
	return fmt.Sprintf("%s:%s", source.Repository, source.Tag)
}

type Tag string

func (tag *Tag) UnmarshalJSON(b []byte) error {
	var n json.Number

	err := json.Unmarshal(b, &n)
	if err != nil {
		return err
	}

	if n.String() == "" {
		*tag = Tag(DefaultTag)
		return nil
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
	Image string `json:"image"`
}
