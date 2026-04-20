package cosmoswasm

import (
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/compress"
)

// CompressGzip compresses data using gzip at the best-speed level.
// Best-speed gives ~60-70 % of best-compression ratio at 5-10x the throughput,
// which is the right trade-off for a hot game-event path.
func CompressGzip(data []byte) ([]byte, error) {
	return compress.Gzip(data)
}

// DecompressGzip decompresses gzip data. Returns the raw bytes.
func DecompressGzip(data []byte) ([]byte, error) {
	return compress.Gunzip(data)
}

// IsGzipCompressed returns true when data starts with the gzip magic bytes.
func IsGzipCompressed(data []byte) bool {
	return compress.IsGzipped(data)
}

// MaybeDecompress decompresses data if it is gzip-compressed, otherwise returns
// it unchanged.  Safe to call on any blob — will never error on non-gzip input.
func MaybeDecompress(data []byte) ([]byte, error) {
	return compress.MaybeDecompress(data)
}

// CompressIfBeneficial compresses data and returns the compressed form only
// when the compressed size is strictly smaller than the original.  This avoids
// the pathological case where incompressible data (already compressed,
// encrypted, random) actually grows after gzip framing.
func CompressIfBeneficial(data []byte) ([]byte, bool) {
	return compress.IfBeneficial(data)
}
