package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver"
	resource "github.com/concourse/registry-image-resource"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/simonshyu/notary-gcr/pkg/gcr"
	"github.com/sirupsen/logrus"
)

type Out struct {
	stdin  io.Reader
	stderr io.Writer
	stdout io.Writer
	args   []string
}

func NewOut(
	stdin io.Reader,
	stderr io.Writer,
	stdout io.Writer,
	args []string,
) *Out {
	return &Out{
		stdin:  stdin,
		stderr: stderr,
		stdout: stdout,
		args:   args,
	}
}

func (o *Out) Execute() error {
	setupLogging(o.stderr)

	var req resource.OutRequest
	decoder := json.NewDecoder(o.stdin)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&req)
	if err != nil {
		return fmt.Errorf("invalid payload: %s", err)
	}

	if req.Source.Debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	if len(o.args) < 2 {
		return fmt.Errorf("destination path not specified")
	}

	src := o.args[1]

	if req.Source.AwsRegion != "" {
		if !req.Source.AuthenticateToECR() {
			return fmt.Errorf("cannot authenticate with ECR")
		}
	}

	tagsToPush := []name.Tag{}

	repo, err := req.Source.NewRepository()
	if err != nil {
		return fmt.Errorf("could not resolve repository: %w", err)
	}

	if req.Source.Tag != "" {
		tagsToPush = append(tagsToPush, repo.Tag(req.Source.Tag.String()))
	}

	if req.Params.Version != "" {
		ver, err := semver.NewVersion(req.Params.Version)
		if err != nil {
			if err == semver.ErrInvalidSemVer {
				return fmt.Errorf("invalid semantic version: %q", req.Params.Version)
			}

			return fmt.Errorf("failed to parse version: %w", err)
		}

		// vito: subtle gotcha here - if someone passes the version as v1.2.3, the
		// 'v' will be stripped, as *semver.Version parses it but does not preserve
		// it in .String().
		//
		// we could call .Original(), of course, but it seems common practice to
		// *not* have the v prefix in Docker image tags, so it might be better to
		// just enforce it until someone complains enough; it seems more likely to
		// be an accident than a legacy practice that must be preserved.
		//
		// if that's the person reading this: sorry! PR welcome! (maybe we should
		// add tag_prefix:?)
		tag := ver.String()
		if req.Source.Variant != "" {
			tag += "-" + req.Source.Variant
		}

		tagsToPush = append(tagsToPush, repo.Tag(tag))

		if req.Params.BumpAliases && ver.Prerelease() == "" {
			aliasTags, err := aliasesToBump(req, repo, ver)
			if err != nil {
				return fmt.Errorf("determine aliases: %w", err)
			}

			tagsToPush = append(tagsToPush, aliasTags...)
		}
	}

	additionalTags, err := req.Params.ParseAdditionalTags(src)
	if err != nil {
		return fmt.Errorf("could not parse additional tags: %w", err)
	}

	for _, tagName := range additionalTags {
		tag, err := name.NewTag(fmt.Sprintf("%s:%s", req.Source.Repository, tagName))
		if err != nil {
			return fmt.Errorf("could not resolve repository/tag reference: %w", err)
		}

		tagsToPush = append(tagsToPush, tag)
	}

	if len(tagsToPush) == 0 {
		return fmt.Errorf("no tag specified - need either 'version:' in params or 'tag:' in source")
	}

	imagePath := filepath.Join(src, req.Params.Image)
	matches, err := filepath.Glob(imagePath)
	if err != nil {
		return fmt.Errorf("failed to glob path '%s': %w", req.Params.Image, err)
	}
	if len(matches) == 0 {
		return fmt.Errorf("no files match glob '%s'", req.Params.Image)
	}
	if len(matches) > 1 {
		return fmt.Errorf("too many files match glob '%s': %v", req.Params.Image, matches)
	}

	img, err := tarball.ImageFromPath(matches[0], nil)
	if err != nil {
		return fmt.Errorf("could not load image from path '%s': %w", req.Params.Image, err)
	}

	digest, err := img.Digest()
	if err != nil {
		return fmt.Errorf("failed to get image digest: %w", err)
	}

	err = resource.RetryOnRateLimit(func() error {
		return put(req, img, tagsToPush)
	})
	if err != nil {
		return fmt.Errorf("pushing image failed: %w", err)
	}

	pushedTags := []string{}
	for _, tag := range tagsToPush {
		pushedTags = append(pushedTags, tag.TagStr())
	}

	err = json.NewEncoder(os.Stdout).Encode(resource.OutResponse{
		Version: resource.Version{
			Tag:    tagsToPush[0].TagStr(),
			Digest: digest.String(),
		},
		Metadata: append(req.Source.Metadata(), resource.MetadataField{
			Name:  "tags",
			Value: strings.Join(pushedTags, " "),
		}),
	})
	if err != nil {
		return fmt.Errorf("could not marshal JSON: %s", err)
	}

	return nil
}

func put(req resource.OutRequest, img v1.Image, tags []name.Tag) error {
	images := map[name.Reference]remote.Taggable{}
	var identifiers []string
	for _, tag := range tags {
		images[tag] = img
		identifiers = append(identifiers, tag.Identifier())
	}

	repo, err := req.Source.NewRepository()
	if err != nil {
		return fmt.Errorf("resolve repository name: %w", err)
	}

	opts, err := req.Source.AuthOptions(repo, []string{transport.PushScope})
	if err != nil {
		return err
	}

	logrus.Infof("pushing tag(s) %s", strings.Join(identifiers, ", "))
	err = remote.MultiWrite(images, opts...)
	if err != nil {
		return fmt.Errorf("pushing tag(s): %w", err)
	}

	logrus.Info("pushed")

	if req.Source.ContentTrust != nil {
		err = signImages(req, img, tags)
		if err != nil {
			return fmt.Errorf("signing image(s): %w", err)
		}
	}

	return nil
}

func signImages(req resource.OutRequest, img v1.Image, tags []name.Tag) error {
	var notaryConfigDir string
	var err error
	notaryConfigDir, err = req.Source.ContentTrust.PrepareConfigDir()
	if err != nil {
		return fmt.Errorf("prepare notary-config-dir: %w", err)
	}

	for _, tag := range tags {
		trustedRepo, err := gcr.NewTrustedGcrRepository(notaryConfigDir, tag, createRegistryAuth(req), createNotaryAuth(req))
		if err != nil {
			return fmt.Errorf("create TrustedGcrRepository: %w", err)
		}

		logrus.Infof("signing image with tag: %s", tag.Identifier())

		err = trustedRepo.SignImage(img)
		if err != nil {
			logrus.Errorf("failed to sign image: %s", err)
		}
	}

	return nil
}

// It's okay if both are blank. It will become an Anonymous Authenticator in
// that case.
func createRegistryAuth(req resource.OutRequest) *authn.Basic {
	return &authn.Basic{
		Username: req.Source.Username,
		Password: req.Source.Password,
	}
}

func createNotaryAuth(req resource.OutRequest) *authn.Basic {
	if req.Source.ContentTrust.Username != "" || req.Source.ContentTrust.Password != "" {
		return &authn.Basic{
			Username: req.Source.ContentTrust.Username,
			Password: req.Source.ContentTrust.Password,
		}
	}
	// keep compatibility, fallback to using source.username & source.password
	return &authn.Basic{
		Username: req.Source.Username,
		Password: req.Source.Password,
	}
}

func aliasesToBump(req resource.OutRequest, repo name.Repository, ver *semver.Version) ([]name.Tag, error) {
	variant := req.Source.Variant

	repo, err := req.Source.NewRepository()
	if err != nil {
		return nil, fmt.Errorf("resolve repository name: %w", err)
	}

	opts, err := req.Source.AuthOptions(repo, []string{transport.PullScope})
	if err != nil {
		return nil, err
	}

	versions, err := remote.List(repo, opts...)
	if err != nil && !isNewImage(err) {
		return nil, fmt.Errorf("list repository tags: %w", err)
	}

	aliases := []name.Tag{}

	bumpLatest := true
	bumpMajor := true
	bumpMinor := true
	for _, v := range versions {
		versionStr := v
		if variant != "" {
			if !strings.HasSuffix(versionStr, "-"+variant) {
				// don't compare across variants
				continue
			}

			versionStr = strings.TrimSuffix(versionStr, "-"+variant)
		}

		remoteVer, err := semver.NewVersion(versionStr)
		if err != nil {
			continue
		}

		// don't compare to prereleases or other variants
		if remoteVer.Prerelease() != "" {
			continue
		}

		if remoteVer.GreaterThan(ver) {
			bumpLatest = false
		}

		if remoteVer.Major() == ver.Major() && remoteVer.Minor() > ver.Minor() {
			bumpMajor = false
		}

		if remoteVer.Major() == ver.Major() && remoteVer.Minor() == ver.Minor() && remoteVer.Patch() > ver.Patch() {
			bumpMinor = false
			bumpMajor = false
		}
	}

	if bumpLatest {
		latestTag := "latest"
		if variant != "" {
			latestTag = variant
		}

		aliases = append(aliases, repo.Tag(latestTag))
	}

	if bumpMajor {
		tagName := fmt.Sprintf("%d", ver.Major())
		if variant != "" {
			tagName += "-" + variant
		}

		aliases = append(aliases, repo.Tag(tagName))
	}

	if bumpMinor {
		tagName := fmt.Sprintf("%d.%d", ver.Major(), ver.Minor())
		if variant != "" {
			tagName += "-" + variant
		}

		aliases = append(aliases, repo.Tag(tagName))
	}

	return aliases, nil
}

func isNewImage(err error) bool {
	if e, ok := err.(*transport.Error); ok && e.StatusCode == http.StatusNotFound {
		return e.Errors[0].Code == transport.NameUnknownErrorCode || e.Errors[0].Code == "NOT_FOUND"
	}

	return false
}
