package resource

type Source struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
}

type Version struct {
	Digest string `json:"digest"`
}
