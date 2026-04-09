package cosmoswasm

import (
	"bytes"
	"compress/gzip"
	"io"
)

// CompressGzip compresses data using gzip at the best-speed level.
// Best-speed gives ~60-70 % of best-compression ratio at 5-10× the throughput,
// which is the right trade-off for a hot game-event path.
func CompressGzip(data []byte) ([]byte, error) {
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

// DecompressGzip decompresses gzip data. Returns the raw bytes.
func DecompressGzip(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// IsGzipCompressed returns true when data starts with the gzip magic bytes.
func IsGzipCompressed(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

// MaybeDecompress decompresses data if it is gzip-compressed, otherwise returns
// it unchanged.  Safe to call on any blob — will never error on non-gzip input.
func MaybeDecompress(data []byte) ([]byte, error) {
	if !IsGzipCompressed(data) {
		return data, nil
	}
	return DecompressGzip(data)
}

// CompressIfBeneficial compresses data and returns the compressed form only
// when the compressed size is strictly smaller than the original.  This avoids
// the pathological case where incompressible data (already compressed,
// encrypted, random) actually grows after gzip framing.
func CompressIfBeneficial(data []byte) ([]byte, bool) {
	compressed, err := CompressGzip(data)
	if err != nil {
		return data, false
	}
	if len(compressed) >= len(data) {
		return data, false
	}
	return compressed, true
}
