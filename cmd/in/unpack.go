package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
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

func unpackImage(dest string, img v1.Image) error {
	layers, err := img.Layers()
	if err != nil {
		return err
	}

	written := map[string]struct{}{}
	removed := map[string]struct{}{}

	chown := os.Getuid() == 0

	progress := mpb.New(mpb.WithOutput(os.Stderr))

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
	for i := len(layers) - 1; i >= 0; i-- {
		err := extractLayer(dest, layers[i], bars[i], written, removed, chown)
		if err != nil {
			return err
		}
	}

	progress.Wait()

	return nil
}

func extractLayer(dest string, layer v1.Layer, bar *mpb.Bar, written, removed map[string]struct{}, chown bool) error {
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

		if strings.HasPrefix(base, whiteoutPrefix) {
			// layer has marked a file as deleted
			name := strings.TrimPrefix(base, whiteoutPrefix)
			removed[filepath.Join(dir, name)] = struct{}{}
			continue
		}

		if pathIsRemoved(path, removed) {
			// path has been removed by lower layer
			continue
		}

		if _, ok := written[path]; ok {
			// path has already been written by lower layer
			continue
		}

		written[path] = struct{}{}

		if hdr.Typeflag == tar.TypeBlock || hdr.Typeflag == tar.TypeChar {
			// devices can't be created in a user namespace
			logrus.Debugf("skipping device %s", hdr.Name)
			continue
		}

		if err := tarfs.ExtractEntry(hdr, dest, tr, chown); err != nil {
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

func pathIsRemoved(path string, removed map[string]struct{}) bool {
	if _, ok := removed[path]; ok {
		return true
	}

	// check if parent dir has been removed
	for wd := range removed {
		if strings.HasPrefix(path, wd) {
			return true
		}
	}

	return false
}
