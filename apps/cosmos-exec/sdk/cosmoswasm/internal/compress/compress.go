// Package compress provides gzip compression utilities for blob data.
//
// This is an internal package — external consumers should use the public
// functions in the cosmoswasm package: CompressIfBeneficial, MaybeDecompress.
package compress

import (
	"bytes"
	"compress/gzip"
	"io"
)

// Gzip compresses data using gzip at best-speed level.
func Gzip(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Gunzip decompresses gzip data.
func Gunzip(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// IsGzipped returns true when data starts with the gzip magic bytes.
func IsGzipped(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

// IfBeneficial compresses and returns the compressed form only when it is
// strictly smaller than the original. Returns (original, false) otherwise.
func IfBeneficial(data []byte) ([]byte, bool) {
	compressed, err := Gzip(data)
	if err != nil {
		return data, false
	}
	if len(compressed) >= len(data) {
		return data, false
	}
	return compressed, true
}

// MaybeDecompress decompresses if gzipped, otherwise returns unchanged.
func MaybeDecompress(data []byte) ([]byte, error) {
	if !IsGzipped(data) {
		return data, nil
	}
	return Gunzip(data)
}
