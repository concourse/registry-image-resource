package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/Masterminds/semver"
	resource "github.com/concourse/registry-image-resource"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/sirupsen/logrus"
)

type Check struct {
	stdin  io.Reader
	stderr io.Writer
	stdout io.Writer
	args   []string
}

func NewCheck(
	stdin io.Reader,
	stderr io.Writer,
	stdout io.Writer,
	args []string,
) *Check {
	return &Check{
		stdin:  stdin,
		stderr: stderr,
		stdout: stdout,
		args:   args,
	}
}

func (c *Check) Execute() error {
	setupLogging(c.stderr)

	var req resource.CheckRequest
	decoder := json.NewDecoder(c.stdin)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&req)
	if err != nil {
		return fmt.Errorf("invalid payload: %s", err)
	}

	if req.Source.AwsRegion != "" {
		if !req.Source.AuthenticateToECR() {
			return fmt.Errorf("cannot authenticate with ECR")
		}
	}

	mirrorSource, hasMirror, err := req.Source.Mirror()
	if err != nil {
		return fmt.Errorf("failed to resolve mirror: %w", err)
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
			return fmt.Errorf("checking origin %s failed: %w", req.Source.Repository, err)
		}
	}

	err = json.NewEncoder(c.stdout).Encode(response)
	if err != nil {
		return fmt.Errorf("could not marshal JSON: %s", err)
	}

	return nil
}

func check(source resource.Source, from *resource.Version) (resource.CheckResponse, error) {
	repo, err := source.NewRepository()
	if err != nil {
		return resource.CheckResponse{}, fmt.Errorf("resolve repository: %w", err)
	}

	opts, err := source.AuthOptions(repo, []string{transport.PullScope})
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

	var constraint *semver.Constraints
	if source.SemverConstraint != "" {
		constraint, err = semver.NewConstraint(source.SemverConstraint)
		if err != nil {
			return resource.CheckResponse{}, fmt.Errorf("parse semver constraint: %w", err)
		}
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

			if constraint != nil && !constraint.Check(ver) {
				// semver constraint not met
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

				if !strings.HasPrefix(pre, "alpha") &&
					!strings.HasPrefix(pre, "beta") &&
					!strings.HasPrefix(pre, "rc") {
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

		digest, found, err := headOrGet(tagRef, opts...)
		if err != nil {
			return resource.CheckResponse{}, fmt.Errorf("get tag digest: %w", err)
		}

		if !found {
			continue
		}

		tagDigests[identifier] = digest.String()

		if ver != nil {
			versionTags[ver] = tagRef

			existing, found := digestVersions[digest.String()]

			shouldSet := !found
			if found {
				if existing.Prerelease() == "" && ver.Prerelease() != "" {
					// favor final version over prereleases
					shouldSet = false
				} else if existing.Prerelease() != "" && ver.Prerelease() == "" {
					// favor final version over prereleases
					shouldSet = true
				} else if strings.Count(ver.Original(), ".") > strings.Count(existing.Original(), ".") {
					// favor more specific semver tag (i.e. 3.2.1 over 3.2, 1.0.0-rc.2 over 1.0.0-rc)
					shouldSet = true
				}
			}

			if shouldSet {
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
		if !existsAsSemver && constraint == nil {
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
	digest, found, err := headOrGet(tag, opts...)
	if err != nil {
		return resource.CheckResponse{}, fmt.Errorf("get remote image: %w", err)
	}

	response := resource.CheckResponse{}
	if version != nil && found && version.Digest != digest.String() {
		digestRef := tag.Repository.Digest(version.Digest)

		_, found, err := headOrGet(digestRef, opts...)
		if err != nil {
			return resource.CheckResponse{}, fmt.Errorf("get remote image: %w", err)
		}

		if found {
			response = append(response, resource.Version{
				Tag:    tag.TagStr(),
				Digest: version.Digest,
			})
		}
	}

	if found {
		response = append(response, resource.Version{
			Tag:    tag.TagStr(),
			Digest: digest.String(),
		})
	}

	return response, nil
}

func headOrGet(ref name.Reference, imageOpts ...remote.Option) (v1.Hash, bool, error) {
	v1Desc, err := remote.Head(ref, imageOpts...)
	if err != nil {
		if checkMissingManifest(err) {
			return v1.Hash{}, false, nil
		}

		remoteDesc, err := remote.Get(ref, imageOpts...)
		if err != nil {
			if checkMissingManifest(err) {
				return v1.Hash{}, false, nil
			}

			return v1.Hash{}, false, err
		}

		return remoteDesc.Digest, true, nil
	}

	return v1Desc.Digest, true, nil
}

func checkMissingManifest(err error) bool {
	if rErr, ok := err.(*transport.Error); ok {
		return rErr.StatusCode == http.StatusNotFound
	}

	return false
}
