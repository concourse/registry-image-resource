package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	resource "github.com/concourse/registry-image-resource"
	"github.com/google/go-containerregistry/pkg/authn"
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

	repo, err := name.NewRepository(req.Source.Repository, name.WeakValidation)
	if err != nil {
		logrus.Errorf("failed to resolve repository: %s", err)
		os.Exit(1)
		return
	}

	var response resource.CheckResponse

	if req.Source.Tag != "" {
		if req.Source.RegistryMirror != nil {
			origin := repo.Registry

			mirror, err := name.NewRegistry(req.Source.RegistryMirror.Host, name.WeakValidation)
			if err != nil {
				logrus.Errorf("could not resolve registry: %s", err)
				os.Exit(1)
				return
			}

			repo.Registry = mirror

			response, err = checkTagWithRetry(req.Source.RegistryMirror.BasicCredentials, req.Version, repo.Tag(req.Source.Tag.String()))
			if err != nil {
				logrus.Warnf("checking mirror %s failed: %s", repo, err)
			} else if len(response) == 0 {
				logrus.Warnf("checking mirror %s failed: tag not found", repo)
			}

			repo.Registry = origin
		}

		if len(response) == 0 {
			response, err = checkTagWithRetry(req.Source.BasicCredentials, req.Version, repo.Tag(req.Source.Tag.String()))
			if err != nil {
				logrus.Errorf("checking origin %s failed: %s", repo, err)
				os.Exit(1)
				return
			}
		}
	} else {
		if req.Source.RegistryMirror != nil {
			origin := repo.Registry

			mirror, err := name.NewRegistry(req.Source.RegistryMirror.Host, name.WeakValidation)
			if err != nil {
				logrus.Errorf("could not resolve registry: %s", err)
				os.Exit(1)
				return
			}

			repo.Registry = mirror

			response, err = checkRepositoryWithRetry(req.Source.RegistryMirror.BasicCredentials, req.Source.Variant, req.Version, repo)
			if err != nil {
				logrus.Warnf("checking mirror %s failed: %s", mirror.RegistryStr(), err)
			} else if len(response) == 0 {
				logrus.Warnf("checking mirror %s failed: no tags found", mirror.RegistryStr())
			}

			repo.Registry = origin
		}

		if len(response) == 0 {
			response, err = checkRepositoryWithRetry(req.Source.BasicCredentials, req.Source.Variant, req.Version, repo)
			if err != nil {
				logrus.Errorf("checking origin failed: %s", err)
				os.Exit(1)
				return
			}
		}
	}

	json.NewEncoder(os.Stdout).Encode(response)
}

func checkRepositoryWithRetry(principal resource.BasicCredentials, variant string, version *resource.Version, ref name.Repository) (resource.CheckResponse, error) {
	var response resource.CheckResponse
	err := resource.RetryOnRateLimit(func() error {
		var err error
		response, err = checkRepository(principal, variant, version, ref)
		return err
	})
	return response, err
}

func checkTagWithRetry(principal resource.BasicCredentials, version *resource.Version, ref name.Tag) (resource.CheckResponse, error) {
	var response resource.CheckResponse
	err := resource.RetryOnRateLimit(func() error {
		var err error
		response, err = checkTag(principal, version, ref)
		return err
	})
	return response, err
}

func checkRepository(principal resource.BasicCredentials, variant string, version *resource.Version, ref name.Repository) (resource.CheckResponse, error) {
	auth := &authn.Basic{
		Username: principal.Username,
		Password: principal.Password,
	}

	imageOpts := []remote.Option{}

	if auth.Username != "" && auth.Password != "" {
		imageOpts = append(imageOpts, remote.WithAuth(auth))
	}

	tags, err := remote.List(ref, imageOpts...)
	if err != nil {
		return resource.CheckResponse{}, fmt.Errorf("list repository tags: %w", err)
	}

	bareTag := "latest"
	if variant != "" {
		bareTag = variant
	}

	var latestTag string

	versions := []*semver.Version{}
	versionTags := map[*semver.Version]name.Tag{}
	tagDigests := map[string]string{}
	digestVersions := map[string]*semver.Version{}
	for _, identifier := range tags {
		var ver *semver.Version
		if identifier == bareTag {
			latestTag = identifier
		} else {
			verStr := identifier
			if variant != "" {
				if !strings.HasSuffix(identifier, "-"+variant) {
					continue
				}

				verStr = strings.TrimSuffix(identifier, "-"+variant)
			}

			ver, err = semver.NewVersion(verStr)
			if err != nil {
				// not a version
				continue
			}

			pre := ver.Prerelease()
			if pre != "" {
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
		}

		tagRef := ref.Tag(identifier)

		digestImage, err := remote.Image(tagRef, imageOpts...)
		if err != nil {
			return resource.CheckResponse{}, fmt.Errorf("get tag digest: %w", err)
		}

		digest, err := digestImage.Digest()
		if err != nil {
			return resource.CheckResponse{}, fmt.Errorf("get cursor image digest: %w", err)
		}

		tagDigests[identifier] = digest.String()

		if ver != nil {
			versionTags[ver] = tagRef

			existing, found := digestVersions[digest.String()]
			if !found || strings.Count(ver.Original(), ".") > strings.Count(existing.Original(), ".") {
				digestVersions[digest.String()] = ver
			}

			versions = append(versions, ver)
		}
	}

	sort.Sort(semver.Collection(versions))

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

func checkTag(principal resource.BasicCredentials, version *resource.Version, ref name.Tag) (resource.CheckResponse, error) {
	auth := &authn.Basic{
		Username: principal.Username,
		Password: principal.Password,
	}

	imageOpts := []remote.Option{}

	if auth.Username != "" && auth.Password != "" {
		imageOpts = append(imageOpts, remote.WithAuth(auth))
	}

	var missingTag bool
	image, err := remote.Image(ref, imageOpts...)
	if err != nil {
		missingTag = checkMissingManifest(err)
		if !missingTag {
			return resource.CheckResponse{}, fmt.Errorf("get remote image: %w", err)
		}
	}

	var digest v1.Hash
	if !missingTag {
		digest, err = image.Digest()
		if err != nil {
			return resource.CheckResponse{}, fmt.Errorf("get cursor image digest: %w", err)
		}
	}

	response := resource.CheckResponse{}
	if version != nil && !missingTag && version.Digest != digest.String() {
		digestRef := ref.Repository.Digest(version.Digest)

		digestImage, err := remote.Image(digestRef, imageOpts...)
		var missingDigest bool
		if err != nil {
			missingDigest = checkMissingManifest(err)
			if !missingDigest {
				return resource.CheckResponse{}, fmt.Errorf("get remote image: %w", err)
			}
		}

		if !missingDigest {
			_, err = digestImage.Digest()
			if err != nil {
				return resource.CheckResponse{}, fmt.Errorf("get cursor image digest: %w", err)
			}

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
		for _, e := range rErr.Errors {
			if e.Code == transport.ManifestUnknownErrorCode {
				missing = true
				break
			}
		}
	}
	return missing
}
