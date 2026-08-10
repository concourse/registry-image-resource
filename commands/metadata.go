package commands

import (
	"fmt"
	"regexp"
	"sort"

	resource "github.com/concourse/registry-image-resource"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
)

func buildBaseMetadata(source resource.Source, tag name.Tag) []resource.MetadataField {
	return append(source.Metadata(), resource.MetadataField{
		Name:  "tag",
		Value: tag.TagStr(),
	})
}

func enrichMetadataFromImage(metadata []resource.MetadataField, source resource.Source, image v1.Image) ([]resource.MetadataField, error) {
	if source.LabelRegex != "" {
		re, err := regexp.Compile(source.LabelRegex)
		if err != nil {
			return nil, fmt.Errorf("invalid label_regex: %w", err)
		}
		cfg, err := image.ConfigFile()
		if err != nil {
			return nil, fmt.Errorf("get config file: %w", err)
		}
		metadata = append(metadata, metadataFieldsFromMap(filterMap(re, cfg.Config.Labels))...)
	}

	if source.AnnotationRegex != "" {
		re, err := regexp.Compile(source.AnnotationRegex)
		if err != nil {
			return nil, fmt.Errorf("invalid annotation_regex: %w", err)
		}
		manifest, err := image.Manifest()
		if err != nil {
			return nil, fmt.Errorf("get manifest: %w", err)
		}
		metadata = append(metadata, metadataFieldsFromMap(filterMap(re, manifest.Annotations))...)
	}

	return metadata, nil
}

func filterMap(re *regexp.Regexp, values map[string]string) map[string]string {
	result := make(map[string]string)
	for k, v := range values {
		if re.MatchString(k) {
			result[k] = v
		}
	}
	return result
}

func metadataFieldsFromMap(values map[string]string) []resource.MetadataField {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	fields := make([]resource.MetadataField, len(keys))
	for i, k := range keys {
		fields[i] = resource.MetadataField{Name: k, Value: values[k]}
	}
	return fields
}
