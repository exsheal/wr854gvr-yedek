package main

import (
	"archive/tar"
	"encoding/base64"
	"io"
)

var b64 = base64.StdEncoding

// tarWriter wraps archive/tar with the small surface this program needs.
type tarWriter struct {
	tw *tar.Writer
}

func newTarWriter(w io.Writer) *tarWriter {
	return &tarWriter{tw: tar.NewWriter(w)}
}

func (t *tarWriter) writeFile(name string, data []byte, mode int64) error {
	hdr := &tar.Header{
		Name:     name,
		Mode:     mode,
		Size:     int64(len(data)),
		Typeflag: tar.TypeReg,
		ModTime:  modTime,
	}
	if err := t.tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := t.tw.Write(data)
	return err
}

func (t *tarWriter) close() error { return t.tw.Close() }
