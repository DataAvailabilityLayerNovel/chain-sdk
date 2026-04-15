package executor

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
)

const (
	// DefaultMaxBlobSize caps a single blob at 4 MB.
	DefaultMaxBlobSize = 4 * 1024 * 1024
	// DefaultMaxStoreTotalBytes caps the in-memory store at 256 MB.
	DefaultMaxStoreTotalBytes = 256 * 1024 * 1024
)

// BlobStore is a thread-safe, content-addressed in-memory store.
// Keys are hex-encoded SHA-256 digests of the stored data (commitments).
// Designed for the blob-first pattern: large data (game snapshots, event
// logs, assets) lands here; only the 32-byte commitment goes on-chain via
// a WASM contract message.
type BlobStore struct {
	mu         sync.RWMutex
	blobs      map[string][]byte // commitment → data
	totalBytes int

	maxBlobSize  int
	maxTotalSize int
}

// NewBlobStore returns a BlobStore with default size limits.
func NewBlobStore() *BlobStore {
	return NewBlobStoreWithLimits(DefaultMaxBlobSize, DefaultMaxStoreTotalBytes)
}

// NewBlobStoreWithLimits returns a BlobStore with custom size limits.
func NewBlobStoreWithLimits(maxBlobSize, maxTotalSize int) *BlobStore {
	if maxBlobSize <= 0 {
		maxBlobSize = DefaultMaxBlobSize
	}
	if maxTotalSize <= 0 {
		maxTotalSize = DefaultMaxStoreTotalBytes
	}
	return &BlobStore{
		blobs:        make(map[string][]byte),
		maxBlobSize:  maxBlobSize,
		maxTotalSize: maxTotalSize,
	}
}

// Put stores data and returns its SHA-256 commitment (hex string).
// Returns an error if the blob is too large or the store would exceed its
// total size budget.
func (s *BlobStore) Put(data []byte) (string, error) {
	if len(data) == 0 {
		return "", errors.New("blob data cannot be empty")
	}
	if len(data) > s.maxBlobSize {
		return "", fmt.Errorf("blob size %d exceeds max %d bytes", len(data), s.maxBlobSize)
	}

	commitment := blobCommitment(data)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Idempotent: already stored.
	if _, ok := s.blobs[commitment]; ok {
		return commitment, nil
	}

	if s.totalBytes+len(data) > s.maxTotalSize {
		return "", fmt.Errorf("blob store full: used %d / %d bytes", s.totalBytes, s.maxTotalSize)
	}

	buf := make([]byte, len(data))
	copy(buf, data)
	s.blobs[commitment] = buf
	s.totalBytes += len(data)

	return commitment, nil
}

// Get retrieves a blob by its commitment. Returns (nil, false) if not found.
func (s *BlobStore) Get(commitment string) ([]byte, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, ok := s.blobs[commitment]
	if !ok {
		return nil, false
	}
	out := make([]byte, len(data))
	copy(out, data)
	return out, true
}

// Count returns the number of blobs in the store.
func (s *BlobStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.blobs)
}

// TotalBytes returns total bytes currently stored.
func (s *BlobStore) TotalBytes() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalBytes
}

// PutBatch stores multiple blobs, computes a binary Merkle root over their
// commitments, and returns (root, perBlobCommitments).
// The root can be committed on-chain (32 bytes); individual commitments are
// used to retrieve each blob or generate inclusion proofs later.
func (s *BlobStore) PutBatch(blobs [][]byte) (root string, commitments []string, err error) {
	if len(blobs) == 0 {
		return "", nil, errors.New("batch cannot be empty")
	}

	commitments = make([]string, len(blobs))
	for i, blob := range blobs {
		c, putErr := s.Put(blob)
		if putErr != nil {
			return "", nil, fmt.Errorf("blob[%d]: %w", i, putErr)
		}
		commitments[i] = c
	}

	root = merkleRoot(commitments)
	return root, commitments, nil
}

// merkleRoot computes a binary Merkle root over a list of hex commitment strings.
// Leaves are the raw 32-byte SHA-256 digests; parents are SHA-256(left||right).
// When the leaf count is odd the last leaf is duplicated (standard padding).
func merkleRoot(commitments []string) string {
	if len(commitments) == 0 {
		return ""
	}

	// Decode commitments to 32-byte slices.
	layer := make([][]byte, len(commitments))
	for i, c := range commitments {
		b := make([]byte, 32)
		// commitments are lowercase hex from blobCommitment(), safe to decode.
		for j := 0; j < 32; j++ {
			var v byte
			fmt.Sscanf(c[j*2:j*2+2], "%02x", &v)
			b[j] = v
		}
		layer[i] = b
	}

	for len(layer) > 1 {
		var next [][]byte
		for i := 0; i < len(layer); i += 2 {
			left := layer[i]
			right := left // duplicate last node if odd
			if i+1 < len(layer) {
				right = layer[i+1]
			}
			combined := append(left, right...) //nolint:gocritic
			h := sha256.Sum256(combined)
			next = append(next, h[:])
		}
		layer = next
	}

	return fmt.Sprintf("%x", layer[0])
}

// blobCommitment returns a hex-encoded SHA-256 of the data.
func blobCommitment(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h[:])
}
