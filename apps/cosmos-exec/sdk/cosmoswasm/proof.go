package cosmoswasm

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

// MerkleProof is an inclusion proof showing that a blob with a given commitment
// is part of a batch whose Merkle root was committed on-chain.
//
// The proof path goes from the leaf (blob commitment) up to the root.
// Each SiblingHash is the hash of the sibling node at that level; IsLeft
// indicates whether the sibling is to the left of the current node.
type MerkleProof struct {
	// Root is the Merkle root of the full batch (what was committed on-chain).
	Root string `json:"root"`
	// LeafIndex is the zero-based position of this blob in the batch.
	LeafIndex int `json:"leaf_index"`
	// Commitment is the SHA-256 of the blob data (the leaf hash).
	Commitment string `json:"commitment"`
	// Path is the ordered list of (sibling, isLeft) pairs from leaf → root.
	Path []MerklePathStep `json:"path"`
}

// MerklePathStep is a single step in the Merkle proof path.
type MerklePathStep struct {
	// SiblingHash is the hash of the sibling node.
	SiblingHash string `json:"sibling_hash"`
	// IsLeft is true when the sibling is to the LEFT of the current node
	// (i.e., current node = right child).
	IsLeft bool `json:"is_left"`
}

// BuildMerkleProof constructs a Merkle inclusion proof for the blob at leafIndex
// within a batch identified by commitments.  commitments must be the same slice
// returned by CommitRoot / SubmitBatch.
func BuildMerkleProof(commitments []string, leafIndex int) (*MerkleProof, error) {
	if len(commitments) == 0 {
		return nil, errors.New("commitments cannot be empty")
	}
	if leafIndex < 0 || leafIndex >= len(commitments) {
		return nil, fmt.Errorf("leaf index %d out of range [0, %d)", leafIndex, len(commitments))
	}

	// Decode hex commitments to raw 32-byte nodes.
	layer := make([][]byte, len(commitments))
	for i, c := range commitments {
		b, err := hexTo32(c)
		if err != nil {
			return nil, fmt.Errorf("invalid commitment at index %d: %w", i, err)
		}
		layer[i] = b
	}

	root := computeMerkleRoot(layer)
	path, err := buildPath(commitments, leafIndex)
	if err != nil {
		return nil, err
	}

	return &MerkleProof{
		Root:       fmt.Sprintf("%x", root),
		LeafIndex:  leafIndex,
		Commitment: commitments[leafIndex],
		Path:       path,
	}, nil
}

// VerifyMerkleProof verifies that commitment is included in the batch whose
// Merkle root equals proof.Root.  Returns nil on success.
func VerifyMerkleProof(proof *MerkleProof) error {
	if proof == nil {
		return errors.New("proof is nil")
	}

	current, err := hexTo32(proof.Commitment)
	if err != nil {
		return fmt.Errorf("invalid commitment: %w", err)
	}

	for _, step := range proof.Path {
		sibling, err := hexTo32(step.SiblingHash)
		if err != nil {
			return fmt.Errorf("invalid sibling hash: %w", err)
		}
		var combined []byte
		if step.IsLeft {
			combined = append(sibling, current...)
		} else {
			combined = append(current, sibling...)
		}
		h := sha256.Sum256(combined)
		current = h[:]
	}

	computed := fmt.Sprintf("%x", current)
	if computed != proof.Root {
		return fmt.Errorf("proof invalid: computed root %s != expected %s", computed, proof.Root)
	}
	return nil
}

// buildPath returns the Merkle path for leafIndex over layer (raw 32-byte nodes).
func buildPath(commitments []string, leafIndex int) ([]MerklePathStep, error) {
	layer := make([][]byte, len(commitments))
	for i, c := range commitments {
		b, err := hexTo32(c)
		if err != nil {
			return nil, fmt.Errorf("invalid commitment at index %d: %w", i, err)
		}
		layer[i] = b
	}

	var path []MerklePathStep
	idx := leafIndex

	for len(layer) > 1 {
		var next [][]byte
		for i := 0; i < len(layer); i += 2 {
			left := layer[i]
			right := left
			if i+1 < len(layer) {
				right = layer[i+1]
			}

			// Record sibling for our target node.
			if i == idx || i+1 == idx {
				var step MerklePathStep
				if i == idx {
					// our node is left; sibling is right
					step = MerklePathStep{SiblingHash: fmt.Sprintf("%x", right), IsLeft: false}
				} else {
					// our node is right; sibling is left
					step = MerklePathStep{SiblingHash: fmt.Sprintf("%x", left), IsLeft: true}
				}
				path = append(path, step)
			}

			combined := append(left, right...) //nolint:gocritic
			h := sha256.Sum256(combined)
			next = append(next, h[:])
		}

		idx = idx / 2
		layer = next
	}

	return path, nil
}

// computeMerkleRoot mirrors executor/blob_store.go merkleRoot but operates on
// raw byte slices so the SDK doesn't depend on the executor package.
func computeMerkleRoot(layer [][]byte) []byte {
	if len(layer) == 0 {
		return nil
	}
	for len(layer) > 1 {
		var next [][]byte
		for i := 0; i < len(layer); i += 2 {
			left := layer[i]
			right := left
			if i+1 < len(layer) {
				right = layer[i+1]
			}
			combined := append(left, right...) //nolint:gocritic
			h := sha256.Sum256(combined)
			next = append(next, h[:])
		}
		layer = next
	}
	return layer[0]
}

// hexTo32 decodes a 64-char hex string to a 32-byte slice.
func hexTo32(h string) ([]byte, error) {
	if len(h) != 64 {
		return nil, fmt.Errorf("expected 64 hex chars, got %d", len(h))
	}
	b := make([]byte, 32)
	for i := 0; i < 32; i++ {
		var v byte
		if _, err := fmt.Sscanf(h[i*2:i*2+2], "%02x", &v); err != nil {
			return nil, fmt.Errorf("invalid hex at pos %d: %w", i*2, err)
		}
		b[i] = v
	}
	return b, nil
}
