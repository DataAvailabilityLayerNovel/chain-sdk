package cosmoswasm

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

const (
	// DefaultMaxChunkSize is the default per-chunk size limit (512 KiB).
	// Mirrors the 500 KiB reference in Celestia gas calculator docs, rounded
	// to a power-of-two for alignment.
	DefaultMaxChunkSize = 512 * 1024

	// chunkHeaderSize is the overhead per chunk (4-byte index + 4-byte total +
	// 32-byte original-data SHA-256).  Kept small so nearly all of MaxChunkSize
	// is usable payload.
	chunkHeaderSize = 40
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
	if maxChunkSize <= 0 {
		maxChunkSize = DefaultMaxChunkSize
	}
	if len(data) <= maxChunkSize {
		return [][]byte{data}, nil
	}

	chunks := splitBytes(data, maxChunkSize)

	h := sha256.Sum256(data)
	commitments := make([]string, len(chunks))
	for i, c := range chunks {
		ch := sha256.Sum256(c)
		commitments[i] = fmt.Sprintf("%x", ch[:])
	}

	return chunks, &ChunkMeta{
		OriginalHash:     fmt.Sprintf("%x", h[:]),
		TotalChunks:      len(chunks),
		ChunkCommitments: commitments,
	}
}

// ReassembleChunks concatenates ordered chunks back into the original blob.
// If meta is non-nil, a SHA-256 integrity check is performed.
func ReassembleChunks(chunks [][]byte, meta *ChunkMeta) ([]byte, error) {
	if len(chunks) == 0 {
		return nil, errors.New("no chunks to reassemble")
	}

	total := 0
	for _, c := range chunks {
		total += len(c)
	}

	buf := make([]byte, 0, total)
	for _, c := range chunks {
		buf = append(buf, c...)
	}

	if meta != nil {
		h := sha256.Sum256(buf)
		got := fmt.Sprintf("%x", h[:])
		if got != meta.OriginalHash {
			return nil, fmt.Errorf("integrity check failed: expected %s got %s", meta.OriginalHash, got)
		}
	}

	return buf, nil
}

// splitBytes divides data into slices of at most size bytes.
func splitBytes(data []byte, size int) [][]byte {
	var chunks [][]byte
	for len(data) > 0 {
		end := size
		if end > len(data) {
			end = len(data)
		}
		chunk := make([]byte, end)
		copy(chunk, data[:end])
		chunks = append(chunks, chunk)
		data = data[end:]
	}
	return chunks
}
