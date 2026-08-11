package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

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

type RemoteImage struct {
	image      v1.Image
	Descriptor *remote.Descriptor
}

func (r *RemoteImage) Image() (v1.Image, error) {
	if r.image != nil {
		return r.image, nil
	}

	if r.Descriptor == nil {
		return nil, fmt.Errorf("can't load image, no image descriptor set")
	}

	image, err := r.Descriptor.Image()
	if err != nil {
		return nil, fmt.Errorf("fetching image: %w", err)
	}
	r.image = image

	return image, nil
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

	if req.Source.AwsRegion != "" {
		if !req.Source.AuthenticateToECR() {
			return fmt.Errorf("cannot authenticate with ECR")
		}
	}

	if req.Source.AzureACR {
		if !req.Source.AuthenticateToACR() {
			return fmt.Errorf("cannot authenticate with ACR")
		}
	}

	repo, err := req.Source.NewRepository()
	if err != nil {
		return fmt.Errorf("failed to resolve repository: %w", err)
	}

	tag := repo.Tag(req.Version.Tag)

	remoteImage := &RemoteImage{}

	mirrorSource, hasMirror, err := req.Source.Mirror()
	if err != nil {
		return fmt.Errorf("failed to resolve mirror: %w", err)
	}

	usedMirror := false
	if hasMirror {
		remoteImage, err = downloadWithRetry(tag, mirrorSource, req.Params, req.Version, dest, i.stderr)
		if err != nil {
			logrus.Warnf("download from mirror %s failed: %s", mirrorSource.Repository, err)
		} else {
			usedMirror = true
		}
	}

	if !usedMirror {
		remoteImage, err = downloadWithRetry(tag, req.Source, req.Params, req.Version, dest, i.stderr)
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}
	}

	err = saveVersionInfo(dest, req.Version, req.Source.Repository)
	if err != nil {
		return fmt.Errorf("saving version info failed: %w", err)
	}

	metadata := buildBaseMetadata(req.Source, tag)
	metadata, err = enrichMetadataFromImage(metadata, req.Source, remoteImage)
	if err != nil {
		return fmt.Errorf("enriching metadata failed: %w", err)
	}

	err = json.NewEncoder(i.stdout).Encode(resource.InResponse{
		Version:  req.Version,
		Metadata: metadata,
	})
	if err != nil {
		return fmt.Errorf("could not marshal JSON: %s", err)
	}

	return nil
}

func fetchImgDescriptor(source resource.Source, version resource.Version, params resource.GetParams) (*remote.Descriptor, error) {
	repo, err := source.NewRepository()
	if err != nil {
		return nil, fmt.Errorf("resolve repository name: %w", err)
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

	remoteDesc, err := remote.Get(repo.Digest(version.Digest), opts...)
	if err != nil {
		return nil, fmt.Errorf("fetch descriptor: %w", err)
	}

	return remoteDesc, nil
}

func downloadWithRetry(tag name.Tag, source resource.Source, params resource.GetParams, version resource.Version, dest string, stderr io.Writer) (*RemoteImage, error) {
	fmt.Fprintf(stderr, "fetching %s@%s\n", color.GreenString(source.Repository), color.YellowString(version.Digest))

	image := &RemoteImage{}
	err := resource.RetryOnTransientError(func() error {
		desc, err := fetchImgDescriptor(source, version, params)
		if err != nil {
			return err
		}
		image.Descriptor = desc

		if params.SkipDownload {
			return nil
		}

		// In case anyone else wonders why we don't show a progress bar for
		// downloads, it's because go-containerregistry doesn't expose anything
		// for us to show the download progress:
		// https://github.com/google/go-containerregistry/issues/670
		// Maybe we should switch to using oras-go?
		switch params.Format() {
		case "oci-layout":
			return saveOciLayout(image, dest)
		case "oci":
			return saveOci(image, tag, dest)
		case "rootfs":
			return saveRootfs(image, dest, source.Debug, stderr)
		default:
			return fmt.Errorf("unknown format provided, must be one of 'rootfs', 'oci', 'oci-layout: %s", params.Format())
		}
	})
	return image, err
}

func saveOciLayout(remoteImg *RemoteImage, dest string) error {
	ioi, err := NewIndexImageFromRemote(remoteImg.Descriptor)
	if err != nil {
		return fmt.Errorf("remote index or image: %w", err)
	}

	err = ioi.WriteToPath(filepath.Join(dest, OciLayoutDirName))
	if err != nil {
		return fmt.Errorf("write oci layout: %w", err)
	}

	return nil
}

func saveOci(remoteImg *RemoteImage, tag name.Tag, dest string) error {
	image, err := remoteImg.Image()
	if err != nil {
		return err
	}

	err = ociFormat(dest, tag, image)
	if err != nil {
		return fmt.Errorf("write oci image: %w", err)
	}
	return nil
}

func saveRootfs(remoteImg *RemoteImage, dest string, debug bool, stderr io.Writer) error {
	image, err := remoteImg.Image()
	if err != nil {
		return err
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
