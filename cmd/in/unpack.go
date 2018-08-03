package main

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/concourse/go-archive/tarfs"
	"github.com/google/go-containerregistry/pkg/v1"
	"github.com/sirupsen/logrus"
	pb "gopkg.in/cheggaaa/pb.v1"
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

	// iterate over layers in reverse order; no need to write things files that
	// are modified by later layers anyway
	for i := len(layers) - 1; i >= 0; i-- {
		err := extractLayer(dest, layers[i], written, removed, chown)
		if err != nil {
			return err
		}
	}

	return nil
}

func extractLayer(dest string, layer v1.Layer, written, removed map[string]struct{}, chown bool) error {
	size, err := layer.Size()
	if err != nil {
		return err
	}

	digest, err := layer.Digest()
	if err != nil {
		return err
	}

	r, err := layer.Compressed()
	if err != nil {
		return err
	}

	bar := pb.New64(size).SetUnits(pb.U_BYTES)
	bar.Output = os.Stderr
	bar.Prefix(digest.Hex[0:12])
	bar.SetWidth(98)

	bar.Start()
	defer bar.Finish()

	gr, err := gzip.NewReader(bar.NewProxyReader(r))
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
