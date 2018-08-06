package resource

const DefaultTag = "latest"

type Source struct {
	Repository string `json:"repository"`
	RawTag     string `json:"tag"`

	Debug bool `json:"debug"`
}

func (s Source) Tag() string {
	if s.RawTag == "" {
		return DefaultTag
	}

	return s.RawTag
}

type Version struct {
	Digest string `json:"digest"`
}

type MetadataField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
