package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	resource "github.com/concourse/registry-image-resource"
	"github.com/google/go-containerregistry/pkg/logs"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/sirupsen/logrus"
)

func main() {
	logrus.SetOutput(os.Stderr)
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors: true,
	})

	logs.Progress = log.New(os.Stderr, "", log.LstdFlags)
	logs.Warn = log.New(os.Stderr, "", log.LstdFlags)

	var req resource.CheckRequest
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&req)
	if err != nil {
		logrus.Errorf("invalid payload: %s", err)
		os.Exit(1)
		return
	}

	if req.Source.AwsAccessKeyId != "" && req.Source.AwsSecretAccessKey != "" && req.Source.AwsRegion != "" {
		if !req.Source.AuthenticateToECR() {
			os.Exit(1)
			return
		}
	}

	mirrorSource, hasMirror, err := req.Source.Mirror()
	if err != nil {
		logrus.Errorf("failed to resolve mirror: %s", err)
		os.Exit(1)
		return
	}

	var response resource.CheckResponse

	if hasMirror {
		response, err = check(mirrorSource, req.Version)
		if err != nil {
			logrus.Warnf("checking mirror %s failed: %s", mirrorSource.Repository, err)
		} else if len(response) == 0 {
			logrus.Warnf("checking mirror %s failed: tag not found", mirrorSource.Repository)
		}
	}

	if len(response) == 0 {
		response, err = check(req.Source, req.Version)
		if err != nil {
			logrus.Errorf("checking origin %s failed: %s", req.Source.Repository, err)
			os.Exit(1)
			return
		}
	}

	json.NewEncoder(os.Stdout).Encode(response)
}

func check(source resource.Source, from *resource.Version) (resource.CheckResponse, error) {
	repo, err := name.NewRepository(source.Repository)
	if err != nil {
		return resource.CheckResponse{}, fmt.Errorf("resolve repository: %w", err)
	}

	opts, err := source.AuthOptions(repo)
	if err != nil {
		return resource.CheckResponse{}, err
	}

	if source.Tag != "" {
		return checkTag(repo.Tag(source.Tag.String()), source, from, opts...)
	} else {
		return checkRepository(repo, source, from, opts...)
	}
}

func checkRepository(repo name.Repository, source resource.Source, from *resource.Version, opts ...remote.Option) (resource.CheckResponse, error) {
	tags, err := remote.List(repo, opts...)
	if err != nil {
		return resource.CheckResponse{}, fmt.Errorf("list repository tags: %w", err)
	}

	bareTag := "latest"
	if source.Variant != "" {
		bareTag = source.Variant
	}

	versionTags := map[*semver.Version]name.Tag{}
	tagDigests := map[string]string{}
	digestVersions := map[string]*semver.Version{}

	var cursorVer *semver.Version
	var latestTag string

	if from != nil {
		// assess the 'from' tag first so we can skip lower version numbers
		sort.Slice(tags, func(i, j int) bool {
			return tags[i] == from.Tag
		})
	}

	for _, identifier := range tags {
		var ver *semver.Version
		if identifier == bareTag {
			latestTag = identifier
		} else {
			verStr := identifier
			if source.Variant != "" {
				if !strings.HasSuffix(identifier, "-"+source.Variant) {
					continue
				}

				verStr = strings.TrimSuffix(identifier, "-"+source.Variant)
			}

			ver, err = semver.NewVersion(verStr)
			if err != nil {
				// not a version
				continue
			}

			pre := ver.Prerelease()
			if pre != "" {
				// pre-releases not enabled; skip
				if !source.PreReleases {
					continue
				}

				// contains additional variant
				if strings.Contains(pre, "-") {
					continue
				}

				if !strings.HasPrefix(pre, "alpha.") &&
					!strings.HasPrefix(pre, "beta.") &&
					!strings.HasPrefix(pre, "rc.") {
					// additional variant, not a prerelease segment
					continue
				}
			}

			if cursorVer != nil && (cursorVer.GreaterThan(ver) || cursorVer.Equal(ver)) {
				// optimization: don't bother fetching digests for lesser (or equal but
				// less specific, i.e. 6.3 vs 6.3.0) version tags
				continue
			}
		}

		tagRef := repo.Tag(identifier)

		desc, err := remote.Head(tagRef, opts...)
		if err != nil {
			return resource.CheckResponse{}, fmt.Errorf("get tag digest: %w", err)
		}

		digest := desc.Digest

		tagDigests[identifier] = digest.String()

		if ver != nil {
			versionTags[ver] = tagRef

			existing, found := digestVersions[digest.String()]
			if !found || strings.Count(ver.Original(), ".") > strings.Count(existing.Original(), ".") {
				digestVersions[digest.String()] = ver
			}
		}

		if from != nil && identifier == from.Tag && digest.String() == from.Digest {
			// if the 'from' version exists and has the same digest, treat its
			// version as a cursor in the tags, only considering newer versions
			//
			// note: the 'from' version will always be the first one hit by this loop
			cursorVer = ver
		}
	}

	var tagVersions TagVersions
	for digest, version := range digestVersions {
		tagVersions = append(tagVersions, TagVersion{
			TagName: versionTags[version].TagStr(),
			Digest:  digest,
			Version: version,
		})
	}

	sort.Sort(tagVersions)

	response := resource.CheckResponse{}

	for _, ver := range tagVersions {
		response = append(response, resource.Version{
			Tag:    ver.TagName,
			Digest: ver.Digest,
		})
	}

	if latestTag != "" {
		digest := tagDigests[latestTag]

		_, existsAsSemver := digestVersions[digest]
		if !existsAsSemver {
			response = append(response, resource.Version{
				Tag:    latestTag,
				Digest: digest,
			})
		}
	}

	return response, nil
}

type TagVersion struct {
	TagName string
	Digest  string
	Version *semver.Version
}

type TagVersions []TagVersion

func (vs TagVersions) Len() int           { return len(vs) }
func (vs TagVersions) Less(i, j int) bool { return vs[i].Version.LessThan(vs[j].Version) }
func (vs TagVersions) Swap(i, j int)      { vs[i], vs[j] = vs[j], vs[i] }

func checkTag(tag name.Tag, source resource.Source, version *resource.Version, opts ...remote.Option) (resource.CheckResponse, error) {
	var missingTag bool
	var digest v1.Hash
	desc, err := remote.Head(tag, opts...)
	if err != nil {
		missingTag = checkMissingManifest(err)
		if !missingTag {
			return resource.CheckResponse{}, fmt.Errorf("get remote image: %w", err)
		}
	} else {
		digest = desc.Digest
	}

	response := resource.CheckResponse{}
	if version != nil && !missingTag && version.Digest != digest.String() {
		digestRef := tag.Repository.Digest(version.Digest)

		_, err := remote.Head(digestRef, opts...)
		var missingDigest bool
		if err != nil {
			missingDigest = checkMissingManifest(err)
			if !missingDigest {
				return resource.CheckResponse{}, fmt.Errorf("get remote image: %w", err)
			}
		}

		if !missingDigest {
			response = append(response, *version)
		}
	}

	if !missingTag {
		response = append(response, resource.Version{
			Digest: digest.String(),
		})
	}

	return response, nil
}

func checkMissingManifest(err error) bool {
	var missing bool
	if rErr, ok := err.(*transport.Error); ok {
		if rErr.StatusCode == http.StatusNotFound {
			return true
		}
	}

	return missing
}
