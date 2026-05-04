package cosmoswasm

import (
	ichunk "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/chunk"
)

const (
	// DefaultMaxChunkSize is the default per-chunk size limit (512 KiB).
	// Mirrors the 500 KiB reference in Celestia gas calculator docs, rounded
	// to a power-of-two for alignment.
	DefaultMaxChunkSize = ichunk.DefaultMaxSize
)

// ChunkMeta describes a set of chunks produced from a single oversized blob.
// Store this alongside the per-chunk commitments so the blob can be reassembled.
type ChunkMeta struct {
	// OriginalHash is the SHA-256 hex of the uncompressed, unsplit original data.
	OriginalHash string `json:"original_hash"`
	// TotalChunks is the number of pieces.
	TotalChunks int `json:"total_chunks"`
	// ChunkCommitments are the per-chunk SHA-256 commitments (same order).
	ChunkCommitments []string `json:"chunk_commitments"`
}

// ChunkBlob splits data into pieces of at most maxChunkSize bytes.
// If data fits in a single chunk it is returned as-is (one-element slice).
// maxChunkSize <= 0 defaults to DefaultMaxChunkSize.
//
// This mirrors the halving strategy from EVNode's da_submitter.go
// (limitBatchBySize) but operates on a single blob before it reaches the
// batch/DA layer.
func ChunkBlob(data []byte, maxChunkSize int) ([][]byte, *ChunkMeta) {
	chunks, meta := ichunk.Split(data, maxChunkSize)
	if meta == nil {
		return chunks, nil
	}
	return chunks, &ChunkMeta{
		OriginalHash:     meta.OriginalHash,
		TotalChunks:      meta.TotalChunks,
		ChunkCommitments: meta.ChunkCommitments,
	}
}

// ReassembleChunks concatenates ordered chunks back into the original blob.
// If meta is non-nil, a SHA-256 integrity check is performed.
func ReassembleChunks(chunks [][]byte, meta *ChunkMeta) ([]byte, error) {
	var imeta *ichunk.Meta
	if meta != nil {
		imeta = &ichunk.Meta{
			OriginalHash:     meta.OriginalHash,
			TotalChunks:      meta.TotalChunks,
			ChunkCommitments: meta.ChunkCommitments,
		}
	}
	return ichunk.Reassemble(chunks, imeta)
}
