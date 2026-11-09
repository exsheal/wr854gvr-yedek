package main

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
)

// fileMD5 returns the hex md5 of a file, streaming it.
func fileMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
