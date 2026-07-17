package commands

import (
	"fmt"
	"regexp"
	"sort"

	resource "github.com/concourse/registry-image-resource"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

func buildImageMetadata(source resource.Source, version resource.Version, repo name.Repository, params resource.GetParams) ([]resource.MetadataField, error) {
	metadata := append(source.Metadata(), resource.MetadataField{
		Name:  "tag",
		Value: version.Tag,
	})

	if source.LabelRegex == "" && source.AnnotationRegex == "" {
		return metadata, nil
	}

	opts, err := source.AuthOptions(repo, []string{transport.PullScope})
	if err != nil {
		return nil, err
	}

	platform := source.Platform(params.RawPlatform)
	opts = append(opts, remote.WithPlatform(v1.Platform{
		Architecture: platform.Architecture,
		OS:           platform.OS,
	}))

	image, err := remote.Image(repo.Digest(version.Digest), opts...)
	if err != nil {
		return nil, fmt.Errorf("get image: %w", err)
	}

	fields, err := collectMetadataFields(source, image)
	if err != nil {
		return nil, err
	}

	return append(metadata, fields...), nil
}

func collectMetadataFields(
	source resource.Source,
	image v1.Image,
) ([]resource.MetadataField, error) {
	var fields []resource.MetadataField

	if regex := source.AnnotationRegex; regex != "" {
		annotations, err := fetchImageAnnotations(regex, image)
		if err != nil {
			return nil, fmt.Errorf("fetch annotations: %w", err)
		}

		fields = append(fields, metadataFieldsFromMap(annotations)...)
	}

	if regex := source.LabelRegex; regex != "" {
		labels, err := fetchImageLabels(regex, image)
		if err != nil {
			return nil, fmt.Errorf("fetch labels: %w", err)
		}

		fields = append(fields, metadataFieldsFromMap(labels)...)
	}

	return fields, nil
}

func fetchImageAnnotations(regex string, img v1.Image) (map[string]string, error) {
	manifest, err := img.Manifest()
	if err != nil {
		return nil, fmt.Errorf("get manifest: %w", err)
	}

	return filterMap(regex, manifest.Annotations, "annotation")
}

func fetchImageLabels(regex string, img v1.Image) (map[string]string, error) {
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("get config file: %w", err)
	}

	return filterMap(regex, cfg.Config.Labels, "label")
}

func filterMap(regex string, values map[string]string, kind string) (map[string]string, error) {
	re, err := regexp.Compile(regex)
	if err != nil {
		return nil, fmt.Errorf("invalid %s regex: %w", kind, err)
	}

	result := make(map[string]string)
	for k, v := range values {
		if re.MatchString(k) {
			result[k] = v
		}
	}

	return result, nil
}

func metadataFieldsFromMap(values map[string]string) []resource.MetadataField {
	fields := make([]resource.MetadataField, 0, len(values))

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		fields = append(fields, resource.MetadataField{
			Name:  key,
			Value: values[key],
		})
	}

	return fields
}
