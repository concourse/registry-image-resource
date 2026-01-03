package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	resource "github.com/concourse/registry-image-resource"
	"github.com/fatih/color"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/sirupsen/logrus"
)

type ImageMetadata struct {
	Cmd        []string `json:"cmd"`
	EntryPoint []string `json:"entrypoint"`
	Env        []string `json:"env"`
	User       string   `json:"user"`
}

type In struct {
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
) *In {
	return &In{
		stdin:  stdin,
		stderr: stderr,
		stdout: stdout,
		args:   args,
	}
}

func (i *In) Execute() error {
	setupLogging(i.stderr)

	var req resource.InRequest
	decoder := json.NewDecoder(i.stdin)
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&req)
	if err != nil {
		return fmt.Errorf("invalid payload: %s", err)
	}

	if req.Source.Debug {
		logrus.SetLevel(logrus.DebugLevel)
	}

	if len(i.args) < 2 {
		return fmt.Errorf("destination path not specified")
	}

	dest := i.args[1]

	isPublicECR := strings.Contains(req.Source.Repository, "public.ecr.aws")
	if !isPublicECR && req.Source.AwsRegion != "" {
		if !req.Source.AuthenticateToECR() {
			return fmt.Errorf("cannot authenticate with ECR")
		}
	}

	repo, err := req.Source.NewRepository()
	if err != nil {
		return fmt.Errorf("failed to resolve repository: %w", err)
	}

	tag := repo.Tag(req.Version.Tag)

	if !req.Params.SkipDownload {
		mirrorSource, hasMirror, err := req.Source.Mirror()
		if err != nil {
			return fmt.Errorf("failed to resolve mirror: %w", err)
		}

		usedMirror := false
		if hasMirror {
			err := downloadWithRetry(tag, mirrorSource, req.Params, req.Version, dest, i.stderr)
			if err != nil {
				logrus.Warnf("download from mirror %s failed: %s", mirrorSource.Repository, err)
			} else {
				usedMirror = true
			}
		}

		if !usedMirror {
			err := downloadWithRetry(tag, req.Source, req.Params, req.Version, dest, i.stderr)
			if err != nil {
				return fmt.Errorf("download failed: %w", err)
			}
		}
	}

	err = saveVersionInfo(dest, req.Version, req.Source.Repository)
	if err != nil {
		return fmt.Errorf("saving version info failed: %w", err)
	}

	err = json.NewEncoder(os.Stdout).Encode(resource.InResponse{
		Version: req.Version,
		Metadata: append(req.Source.Metadata(), resource.MetadataField{
			Name:  "tag",
			Value: req.Version.Tag,
		}),
	})
	if err != nil {
		return fmt.Errorf("could not marshal JSON: %s", err)
	}

	return nil
}

func downloadWithRetry(tag name.Tag, source resource.Source, params resource.GetParams, version resource.Version, dest string, stderr io.Writer) error {
	fmt.Fprintf(stderr, "fetching %s@%s\n", color.GreenString(source.Repository), color.YellowString(version.Digest))

	repo, err := source.NewRepository()
	if err != nil {
		return fmt.Errorf("resolve repository name: %w", err)
	}

	return resource.RetryOnRateLimit(func() error {
		opts, err := source.AuthOptions(repo, []string{transport.PullScope})
		if err != nil {
			return err
		}

		platform := source.Platform(params.RawPlatform)
		opts = append(opts, remote.WithPlatform(v1.Platform{
			Architecture: platform.Architecture,
			OS:           platform.OS,
		}))

		// In case anyone else wonders why we don't show a progress bar for
		// downloads, it's because go-containerregistry doesn't expose anything
		// for us to show the download progress:
		// https://github.com/google/go-containerregistry/issues/670
		// Maybe we should switch to using oras-go?
		switch params.Format() {
		case "oci-layout":
			return saveOciLayout(repo, version, dest, opts)
		case "oci":
			return saveOci(repo, tag, version, dest, opts)
		case "rootfs":
			return saveRootfs(repo, version, dest, opts, source.Debug, stderr)
		default:
			return fmt.Errorf("unknown format provided, must be one of 'rootfs', 'oci', 'oci-layout: %s", params.Format())
		}
	})
}

func saveOciLayout(repo name.Repository, version resource.Version, dest string, opts []remote.Option) error {
	remoteDesc, err := remote.Get(repo.Digest(version.Digest), opts...)
	if err != nil {
		return fmt.Errorf("remote get: %w", err)
	}

	ioi, err := NewIndexImageFromRemote(remoteDesc)
	if err != nil {
		return fmt.Errorf("remote index or image: %w", err)
	}

	err = ioi.WriteToPath(filepath.Join(dest, OciLayoutDirName))
	if err != nil {
		return fmt.Errorf("write oci layout: %w", err)
	}

	return nil
}

func saveOci(repo name.Repository, tag name.Tag, version resource.Version, dest string, opts []remote.Option) error {
	image, err := remote.Image(repo.Digest(version.Digest), opts...)
	if err != nil {
		return fmt.Errorf("get image: %w", err)
	}
	err = ociFormat(dest, tag, image)
	if err != nil {
		return fmt.Errorf("write oci image: %w", err)
	}
	return nil
}

func saveRootfs(repo name.Repository, version resource.Version, dest string, opts []remote.Option, debug bool, stderr io.Writer) error {
	image, err := remote.Image(repo.Digest(version.Digest), opts...)
	if err != nil {
		return fmt.Errorf("get image: %w", err)
	}
	err = rootfsFormat(dest, image, debug, stderr)
	if err != nil {
		return fmt.Errorf("write rootfs: %w", err)
	}
	return nil
}

func saveVersionInfo(dest string, version resource.Version, repo string) error {
	err := os.WriteFile(filepath.Join(dest, "tag"), []byte(version.Tag), 0644)
	if err != nil {
		return fmt.Errorf("write image tag: %w", err)
	}

	err = os.WriteFile(filepath.Join(dest, "digest"), []byte(version.Digest), 0644)
	if err != nil {
		return fmt.Errorf("write image digest: %w", err)
	}

	err = os.WriteFile(filepath.Join(dest, "repository"), []byte(repo), 0644)
	if err != nil {
		return fmt.Errorf("write image repository: %w", err)
	}

	return nil
}

func ociFormat(dest string, tag name.Tag, image v1.Image) error {
	err := tarball.WriteToFile(filepath.Join(dest, "image.tar"), tag, image)
	if err != nil {
		return fmt.Errorf("write OCI image: %s", err)
	}

	config, err := image.ConfigFile()
	if err != nil {
		return fmt.Errorf("extract OCI config file: %s", err)
	}

	err = writeLabels(dest, config.Config.Labels)
	if err != nil {
		return err
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

	err = json.NewEncoder(meta).Encode(ImageMetadata{
		Env:        cfg.Config.Env,
		User:       cfg.Config.User,
		EntryPoint: cfg.Config.Entrypoint,
		Cmd:        cfg.Config.Cmd,
	})
	if err != nil {
		return fmt.Errorf("write image metadata: %w", err)
	}

	err = meta.Close()
	if err != nil {
		return fmt.Errorf("close image metadata file: %w", err)
	}

	err = writeLabels(dest, cfg.Config.Labels)
	if err != nil {
		return err
	}

	return nil
}

func writeLabels(dest string, labelData map[string]string) error {
	if labelData == nil {
		labelData = map[string]string{}
	}

	labels, err := os.Create(filepath.Join(dest, "labels.json"))
	if err != nil {
		return fmt.Errorf("create image labels: %w", err)
	}

	err = json.NewEncoder(labels).Encode(labelData)
	if err != nil {
		return fmt.Errorf("write image labels: %w", err)
	}

	err = labels.Close()
	if err != nil {
		return fmt.Errorf("close image labels file: %w", err)
	}

	return nil
}
