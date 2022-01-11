package commands

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/concourse/go-archive/tarfs"
	"github.com/fatih/color"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/sirupsen/logrus"
	"github.com/vbauerster/mpb"
	"github.com/vbauerster/mpb/decor"
)

const whiteoutPrefix = ".wh."
const whiteoutOpaqueDir = whiteoutPrefix + whiteoutPrefix + ".opq"

func unpackImage(dest string, img v1.Image, debug bool, out io.Writer) error {
	layers, err := img.Layers()
	if err != nil {
		return err
	}

	chown := os.Getuid() == 0

	if debug {
		out = ioutil.Discard
	}

	progress := mpb.New(mpb.WithOutput(out))

	bars := make([]*mpb.Bar, len(layers))

	for i, layer := range layers {
		size, err := layer.Size()
		if err != nil {
			return err
		}

		digest, err := layer.Digest()
		if err != nil {
			return err
		}

		bars[i] = progress.AddBar(
			size,
			mpb.PrependDecorators(decor.Name(color.HiBlackString(digest.Hex[0:12]))),
			mpb.AppendDecorators(decor.CountersKibiByte("%.1f/%.1f")),
		)
	}

	// iterate over layers in reverse order; no need to write things files that
	// are modified by later layers anyway
	for i, layer := range layers {
		logrus.Debugf("extracting layer %d of %d", i+1, len(layers))

		err := extractLayer(dest, layer, bars[i], chown)
		if err != nil {
			return err
		}
	}

	progress.Wait()

	return nil
}

func extractLayer(dest string, layer v1.Layer, bar *mpb.Bar, chown bool) error {
	r, err := layer.Compressed()
	if err != nil {
		return err
	}

	gr, err := gzip.NewReader(bar.ProxyReader(r))
	if err != nil {
		return err
	}

	tr := tar.NewReader(gr)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}

		if err != nil {
			return err
		}

		path := filepath.Join(dest, filepath.Clean(hdr.Name))
		base := filepath.Base(path)
		dir := filepath.Dir(path)

		log := logrus.WithFields(logrus.Fields{
			"Name": hdr.Name,
		})

		log.Debug("unpacking")

		if base == whiteoutOpaqueDir {
			fi, err := os.Lstat(dir)
			if err != nil && !os.IsNotExist(err) {
				return err
			}

			log.Debugf("removing contents of %s", dir)

			if err := os.RemoveAll(dir); err != nil {
				return err

			}
			if err := os.Mkdir(dir, fi.Mode()&os.ModePerm); err != nil {
				return err
			}

			continue
		} else if strings.HasPrefix(base, whiteoutPrefix) {
			// layer has marked a file as deleted
			name := strings.TrimPrefix(base, whiteoutPrefix)
			removedPath := filepath.Join(dir, name)

			log.Debugf("removing %s", removedPath)

			err := os.RemoveAll(removedPath)
			if err != nil {
				return nil
			}

			continue
		}

		if hdr.Typeflag == tar.TypeBlock || hdr.Typeflag == tar.TypeChar {
			// devices can't be created in a user namespace
			log.Debugf("skipping device %s", hdr.Name)
			continue
		}

		if hdr.Typeflag == tar.TypeSymlink {
			log.Debugf("symlinking to %s", hdr.Linkname)
		}

		if hdr.Typeflag == tar.TypeLink {
			log.Debugf("hardlinking to %s", hdr.Linkname)
		}

		if fi, err := os.Lstat(path); err == nil {
			if fi.IsDir() && hdr.Name == "." {
				continue
			}

			if !(fi.IsDir() && hdr.Typeflag == tar.TypeDir) {
				log.Debugf("removing existing path")
				if err := os.RemoveAll(path); err != nil {
					return err
				}
			}
		}

		if err := tarfs.ExtractEntry(hdr, dest, tr, chown); err != nil {
			log.Debugf("extracting")
			return err
		}
	}

	err = gr.Close()
	if err != nil {
		return err
	}

	err = r.Close()
	if err != nil {
		return err
	}

	bar.SetTotal(bar.Current(), true)

	return nil
}
