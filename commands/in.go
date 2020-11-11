package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	resource "github.com/concourse/registry-image-resource"
	"github.com/fatih/color"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/logs"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/sirupsen/logrus"
)

type InRequest struct {
	Source  resource.Source    `json:"source"`
	Params  resource.GetParams `json:"params"`
	Version resource.Version   `json:"version"`
}

type InResponse struct {
	Version  resource.Version         `json:"version"`
	Metadata []resource.MetadataField `json:"metadata"`
}

type ImageMetadata struct {
	Env  []string `json:"env"`
	User string   `json:"user"`
}

type in struct {
	stdin  io.Reader
	stderr io.Writer
	stdout io.Writer
	args   []string
}

func NewIn(
	stdin io.Reader,
	stderr io.Writer,
	stdout io.Writer,
	args []string,
) *in {
	return &in{
		stdin:  stdin,
		stderr: stderr,
		stdout: stdout,
		args:   args,
	}
}

func (i *in) Execute() error {
	setupLogging(i.stderr)

	var req InRequest
	decoder := json.NewDecoder(i.stdin)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&req)
	if err != nil {
		return fmt.Errorf("invalid payload: %s", err)
	}

	if req.Source.Debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	if req.Params.SkipDownload {
		logrus.Info("Skipping download because `skip_download` is set to `true``")
		return json.NewEncoder(i.stdout).Encode(InResponse{
			Version:  req.Version,
			Metadata: req.Source.Metadata(),
		})
	}

	if len(i.args) < 2 {
		return fmt.Errorf("destination path not specified")
	}

	dest := i.args[1]

	if req.Source.AwsAccessKeyId != "" && req.Source.AwsSecretAccessKey != "" && req.Source.AwsRegion != "" {
		if !req.Source.AuthenticateToECR() {
			return fmt.Errorf("cannot authenticate with ECR")
		}
	}

	repo, err := name.NewRepository(req.Source.Repository, name.WeakValidation)
	if err != nil {
		return fmt.Errorf("failed to resolve repository: %s", err)
	}

	fmt.Fprintf(i.stderr, "fetching %s@%s\n", color.GreenString(req.Source.Repository), color.YellowString(req.Version.Digest))

	var image v1.Image
	digest := new(name.Digest)

	// only use the RegistryMirror as the Registry if the repo doesn't use a different,
	// explicitly-declared, non-default registry, such as 'some-registry.com/foo/bar'.
	if req.Source.RegistryMirror != nil && repo.Registry.String() == name.DefaultRegistry {
		mirror, err := name.NewRepository(repo.String())
		if err != nil {
			return fmt.Errorf("could not resolve mirror repository: %s", err)
		}

		mirror.Registry, err = name.NewRegistry(req.Source.RegistryMirror.Host, name.WeakValidation)
		if err != nil {
			return fmt.Errorf("could not resolve registry reference: %s", err)
		}

		*digest = mirror.Digest(req.Version.Digest)

		image, err = getWithRetry(req.Source.RegistryMirror.BasicCredentials, *digest)
		if err != nil {
			logrus.Warnf("fetching mirror %s failed: %s", digest.RegistryStr(), err)
		}
	}

	if image == nil {
		*digest = repo.Digest(req.Version.Digest)
		image, err = getWithRetry(req.Source.BasicCredentials, *digest)
	}
	if err != nil {
		return fmt.Errorf("fetching origin %s failed: %s", digest.RegistryStr(), err)
	}

	tag := repo.Tag(req.Source.Tag())

	err = saveWithRetry(dest, tag, image, req.Params.Format(), req.Source.Debug, i.stderr)
	if err != nil {
		return fmt.Errorf("saving image: %s", err)
	}

	err = json.NewEncoder(i.stdout).Encode(InResponse{
		Version:  req.Version,
		Metadata: req.Source.Metadata(),
	})

	if err != nil {
		return fmt.Errorf("could not marshal JSON: %s", err)
	}

	return nil
}

func setupLogging(stderr io.Writer) {
	logrus.SetOutput(stderr)
	logrus.SetFormatter(&logrus.TextFormatter{
		ForceColors: true,
	})

	logs.Progress = log.New(stderr, "", log.LstdFlags)
	logs.Warn = log.New(stderr, "", log.LstdFlags)
}

func getWithRetry(principal resource.BasicCredentials, digest name.Digest) (v1.Image, error) {
	var image v1.Image
	err := resource.RetryOnRateLimit(func() error {
		var err error
		image, err = get(principal, digest)
		return err
	})
	return image, err
}

func get(principal resource.BasicCredentials, digest name.Digest) (v1.Image, error) {
	auth := &authn.Basic{
		Username: principal.Username,
		Password: principal.Password,
	}

	imageOpts := []remote.Option{}

	if auth.Username != "" && auth.Password != "" {
		imageOpts = append(imageOpts, remote.WithAuth(auth))
	} else {
		imageOpts = append(imageOpts, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	}

	image, err := remote.Image(digest, imageOpts...)
	if err != nil {
		return nil, fmt.Errorf("locate remote image: %w", err)
	}
	if image == empty.Image {
		return nil, fmt.Errorf("download image")
	}

	return image, err
}

func saveWithRetry(dest string, tag name.Tag, image v1.Image, format string, debug bool, stderr io.Writer) error {
	return resource.RetryOnRateLimit(func() error {
		return save(dest, tag, image, format, debug, stderr)
	})
}

func save(dest string, tag name.Tag, image v1.Image, format string, debug bool, stderr io.Writer) error {
	switch format {
	case "oci":
		err := ociFormat(dest, tag, image)
		if err != nil {
			return fmt.Errorf("write oci image: %w", err)
		}
	case "rootfs":
		err := rootfsFormat(dest, image, debug, stderr)
		if err != nil {
			return fmt.Errorf("write rootfs: %w", err)
		}
	}

	err := ioutil.WriteFile(filepath.Join(dest, "tag"), []byte(tag.TagStr()), 0644)
	if err != nil {
		return fmt.Errorf("save image tag: %w", err)
	}

	err = saveDigest(dest, image)
	if err != nil {
		return fmt.Errorf("save image digest: %w", err)
	}

	return err
}

func saveDigest(dest string, image v1.Image) error {
	digest, err := image.Digest()
	if err != nil {
		return fmt.Errorf("get image digest: %w", err)
	}

	digestDest := filepath.Join(dest, "digest")
	return ioutil.WriteFile(digestDest, []byte(digest.String()), 0644)
}

func ociFormat(dest string, tag name.Tag, image v1.Image) error {
	err := tarball.WriteToFile(filepath.Join(dest, "image.tar"), tag, image)
	if err != nil {
		return fmt.Errorf("write OCI image: %s", err)
	}

	return nil
}

func rootfsFormat(dest string, image v1.Image, debug bool, stderr io.Writer) error {
	err := unpackImage(filepath.Join(dest, "rootfs"), image, debug, stderr)
	if err != nil {
		return fmt.Errorf("extract image: %w", err)
	}

	cfg, err := image.ConfigFile()
	if err != nil {
		return fmt.Errorf("inspect image config: %w", err)
	}

	meta, err := os.Create(filepath.Join(dest, "metadata.json"))
	if err != nil {
		return fmt.Errorf("create image metadata: %w", err)
	}

	env := cfg.Config.Env

	user := cfg.Config.User

	err = json.NewEncoder(meta).Encode(ImageMetadata{
		Env:  env,
		User: user,
	})
	if err != nil {
		return fmt.Errorf("write image metadata: %w", err)
	}

	err = meta.Close()
	if err != nil {
		return fmt.Errorf("close image metadata file: %w", err)
	}

	return nil
}
