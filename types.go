package resource

type Source struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`

	Debug bool `json:"debug"`
}

type Version struct {
	Digest string `json:"digest"`
}

type MetadataField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
