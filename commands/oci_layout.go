package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const (
	// name of this format
	OciLayoutFormatName = "oci-layout"

	// name of directory that receives data in this format within dest
	OciLayoutDirName = "oci"

	// name of special marker file written to signify a legacy image
	OciLayoutSingleImageDigestFileName = "single-image-digest"
)

// represents either an ImageIndex (modern image) or a legacy image
// wrapped by an otherwise empty ImageIndex
type IndexOrImage struct {
	// image index object, wraps all child images
	v1.ImageIndex

	// if set, signifies this is legacy image, which can be
	// found via this hash in the imageIndex
	originalImageDigest *v1.Hash
}

// create new legacy-style IndexOrImage based on a v1.Image which
// may have been read from a tarball, or otherwise referenced directly
func NewIndexImageFromImage(img v1.Image) (*IndexOrImage, error) {
	digest, err := img.Digest()
	if err != nil {
		return nil, fmt.Errorf("digest: %w", err)
	}
	rv := mutate.AppendManifests(empty.Index, mutate.IndexAddendum{Add: img})

	// to work around a bug in the return value of AppendManifests(),
	// we call Digest() on it, which forces some internally flatten that otherwise
	// prevents us from being able to look up our image from inside it.
	// Specifally, this has the side-effect of calling "compute()" which populates
	// internal maps needed to later lookups
	_, err = rv.Digest()
	if err != nil {
		return nil, fmt.Errorf("digest: %w", err)
	}

	return &IndexOrImage{
		ImageIndex:          rv,
		originalImageDigest: &digest,
	}, nil
}

// create new IndexOrImage based on loading from a directory on disk directory
// must incldue the file "oci-layout" (as required by the spec) as a
// special-case, if the "single-image-digest" marker file is present, then
// ignore any other images and wrap that as a single image.
func NewIndexImageFromPath(path string) (*IndexOrImage, error) {
	// load layout into index
	ii, err := layout.ImageIndexFromPath(path)
	if err != nil {
		return nil, fmt.Errorf("loading %s as OCI layout: %w", path, err)
	}

	ii, err = unwrapNestedIndex(ii)
	if err != nil {
		return nil, fmt.Errorf("error unwrapping nested image indexes: %w", err)
	}

	return checkIfSingleImageDigest(ii, path)
}

// Detects if the ImageIndex contains a single Manifest. If that single Manifest
// is another ImageIndex then it fetches that file and repeats this process
// until it gets an ImageIndex that contains more than one Manifest, which
// should be the real underlying ImageIndex that contains a list of ImageManifests.
func unwrapNestedIndex(imageIndex v1.ImageIndex) (v1.ImageIndex, error) {
	manifest, err := imageIndex.IndexManifest()
	if err != nil {
		return nil, fmt.Errorf("error getting index manifest: %w", err)
	}

	if len(manifest.Manifests) == 1 {
		maybeIndex := manifest.Manifests[0]
		if maybeIndex.MediaType.IsIndex() {
			ii, err := imageIndex.ImageIndex(maybeIndex.Digest)
			if err != nil {
				return nil, fmt.Errorf("error unwrapping nested index manifest: %w", err)
			}
			return unwrapNestedIndex(ii)
		}
	}

	return imageIndex, nil
}

func checkIfSingleImageDigest(imageIndex v1.ImageIndex, path string) (*IndexOrImage, error) {
	// check if special marker file exists
	digestStrBytes, err := os.ReadFile(filepath.Join(path, OciLayoutSingleImageDigestFileName))
	if err != nil {
		// if this file doesn't exist, then we are done!
		if errors.Is(err, fs.ErrNotExist) {
			return &IndexOrImage{ImageIndex: imageIndex}, nil
		}
		return nil, fmt.Errorf("read %s: %w", OciLayoutSingleImageDigestFileName, err)
	}

	// read the digest for the single image we wish to push
	singleImageHash, err := v1.NewHash(string(digestStrBytes))
	if err != nil {
		return nil, fmt.Errorf("new hash: %w", err)
	}

	// get an image reference to the single image
	img, err := imageIndex.Image(singleImageHash)
	if err != nil {
		return nil, fmt.Errorf("image: %w", err)
	}

	// wrap the single image
	rv, err := NewIndexImageFromImage(img)
	if err != nil {
		return nil, fmt.Errorf("new index image from image: %w", err)
	}

	return rv, nil
}

// create new IndexOrImage based on a remote descriptor, which may
// be either a modern index of images, or a specific legacy image.
func NewIndexImageFromRemote(imgOrIndex *remote.Descriptor) (*IndexOrImage, error) {
	switch {
	case imgOrIndex.MediaType.IsIndex():
		// if it's an index (normal case), then easy, parse as such
		rv, err := imgOrIndex.ImageIndex()
		if err != nil {
			return nil, fmt.Errorf("image index: %w", err)
		}

		return &IndexOrImage{
			ImageIndex: rv,
		}, nil

	case imgOrIndex.MediaType.IsImage():
		// else parse as an image image
		img, err := imgOrIndex.Image()
		if err != nil {
			return nil, fmt.Errorf("image: %w", err)
		}

		// then wrap this image and return it
		rv, err := NewIndexImageFromImage(img)
		if err != nil {
			return nil, fmt.Errorf("new index image from image: %w", err)
		}

		return rv, nil

	default:
		return nil, fmt.Errorf("unsupported media type: %s", imgOrIndex.MediaType)
	}
}

// write out all assets in OCI Layout to the path specified.
// in addition to standard files, a special marker file is written
// if this object is based on a legacy specific image. The OCI
// Layout specification permits additional files to be present.
func (ioi *IndexOrImage) WriteToPath(dest string) error {
	// save all the assets out
	lp, err := layout.Write(dest, ioi.ImageIndex)
	if err != nil {
		return fmt.Errorf("layout write: %w", err)
	}

	// if not originally an image, then we are all done
	if !ioi.isAncientImage() {
		return nil
	}

	// else write out special marker file for consumers of this directory
	err = lp.WriteFile(OciLayoutSingleImageDigestFileName, []byte(ioi.originalImageDigest.String()), os.ModePerm)
	if err != nil {
		return fmt.Errorf("write %s: %w", OciLayoutSingleImageDigestFileName, err)
	}

	return nil
}

// does this wrap a legacy image?
func (ioi *IndexOrImage) isAncientImage() bool {
	return ioi.originalImageDigest != nil
}

// return the digest for this index (or image)
func (ioi *IndexOrImage) Digest() (v1.Hash, error) {
	if ioi.isAncientImage() {
		return *ioi.originalImageDigest, nil
	}
	return ioi.ImageIndex.Digest()
}

// return the object that should be tagged when pushing
// to a repo
func (ioi *IndexOrImage) Taggable() (remote.Taggable, error) {
	if ioi.isAncientImage() {
		rv, err := ioi.ImageIndex.Image(*ioi.originalImageDigest)
		if err != nil {
			return nil, fmt.Errorf("image: %w", err)
		}
		return rv, nil
	}
	return ioi.ImageIndex, nil
}

// iterate through each image inside of this IndexOrImage and call
// the specified callback
func (ioi *IndexOrImage) ForEachImage(f func(v1.Image) error) error {
	// use queue because our main index may contain nested indexes
	// per https://github.com/opencontainers/image-spec/blob/main/image-index.md
	for queue := []v1.ImageIndex{ioi.ImageIndex}; len(queue) != 0; {
		// get image index from and of queue
		var cii v1.ImageIndex
		cii, queue = queue[len(queue)-1], queue[:len(queue)-1]

		// get index manifest
		im, err := cii.IndexManifest()
		if err != nil {
			return fmt.Errorf("index manifest: %w", err)
		}

		// for each child manifest
		for _, m := range im.Manifests {
			switch {
			// if it's an image, then call callback
			case m.MediaType.IsImage():
				img, err := cii.Image(m.Digest)
				if err != nil {
					return fmt.Errorf("image: %w", err)
				}

				err = f(img)
				if err != nil {
					return fmt.Errorf("callback: %w", err)
				}

			// if it's an index, then add to queue to process
			case m.MediaType.IsIndex():
				cim, err := cii.ImageIndex(m.Digest)
				if err != nil {
					return fmt.Errorf("image index: %w", err)
				}
				queue = append(queue, cim)
			}
		}
	}
	return nil
}
