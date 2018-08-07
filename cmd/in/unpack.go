package main

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
	"github.com/google/go-containerregistry/pkg/v1"
	"github.com/sirupsen/logrus"
	"github.com/vbauerster/mpb"
	"github.com/vbauerster/mpb/decor"
)

const whiteoutPrefix = ".wh."

func unpackImage(dest string, img v1.Image, debug bool) error {
	layers, err := img.Layers()
	if err != nil {
		return err
	}

	chown := os.Getuid() == 0

	var out io.Writer
	if debug {
		out = ioutil.Discard
	} else {
		out = os.Stderr
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

		if strings.HasPrefix(base, whiteoutPrefix) {
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
			log.Warnf("symlinking to %s", hdr.Linkname)
		}

		if hdr.Typeflag == tar.TypeLink {
			log.Warnf("hardlinking to %s", hdr.Linkname)
		}

		if err := tarfs.ExtractEntry(hdr, dest, tr, chown); err != nil {
			log.Infof("extracting")
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

	return nil
}
