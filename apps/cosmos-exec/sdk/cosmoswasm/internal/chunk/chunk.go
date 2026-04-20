// Package chunk splits oversized blobs into pieces for DA submission.
//
// This is an internal package — external consumers should use the public
// functions in the cosmoswasm package: ChunkBlob, ReassembleChunks.
package chunk

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

// DefaultMaxSize is the default per-chunk size limit (512 KiB).
const DefaultMaxSize = 512 * 1024

// Meta describes a set of chunks produced from a single oversized blob.
type Meta struct {
	OriginalHash     string   `json:"original_hash"`
	TotalChunks      int      `json:"total_chunks"`
	ChunkCommitments []string `json:"chunk_commitments"`
}

// Split divides data into pieces of at most maxSize bytes.
// Returns (chunks, nil) if data fits in one piece.
// maxSize <= 0 defaults to DefaultMaxSize.
func Split(data []byte, maxSize int) ([][]byte, *Meta) {
	if maxSize <= 0 {
		maxSize = DefaultMaxSize
	}
	if len(data) <= maxSize {
		return [][]byte{data}, nil
	}

	chunks := splitBytes(data, maxSize)

	h := sha256.Sum256(data)
	commitments := make([]string, len(chunks))
	for i, c := range chunks {
		ch := sha256.Sum256(c)
		commitments[i] = fmt.Sprintf("%x", ch[:])
	}

	return chunks, &Meta{
		OriginalHash:     fmt.Sprintf("%x", h[:]),
		TotalChunks:      len(chunks),
		ChunkCommitments: commitments,
	}
}

// Reassemble concatenates ordered chunks and verifies integrity if meta is provided.
func Reassemble(chunks [][]byte, meta *Meta) ([]byte, error) {
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

func splitBytes(data []byte, size int) [][]byte {
	var chunks [][]byte
	for len(data) > 0 {
		end := size
		if end > len(data) {
			end = len(data)
		}
		c := make([]byte, end)
		copy(c, data[:end])
		chunks = append(chunks, c)
		data = data[end:]
	}
	return chunks
}
