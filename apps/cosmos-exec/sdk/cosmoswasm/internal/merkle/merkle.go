// Package merkle implements a binary SHA-256 Merkle tree for blob batches.
//
// This is an internal package — external consumers should use the public
// functions in the cosmoswasm package: GetProof, BuildMerkleProof, VerifyMerkleProof.
package merkle

import (
	"crypto/sha256"
	"fmt"
)

// PathStep is a single step in a Merkle proof path.
type PathStep struct {
	SiblingHash string
	IsLeft      bool
}

// ComputeRoot computes the Merkle root over raw 32-byte leaf nodes.
func ComputeRoot(layer [][]byte) []byte {
	if len(layer) == 0 {
		return nil
	}
	// Work on a copy so we don't mutate the caller's slice.
	current := make([][]byte, len(layer))
	copy(current, layer)

	for len(current) > 1 {
		var next [][]byte
		for i := 0; i < len(current); i += 2 {
			left := current[i]
			right := left
			if i+1 < len(current) {
				right = current[i+1]
			}
			combined := append(left, right...) //nolint:gocritic
			h := sha256.Sum256(combined)
			next = append(next, h[:])
		}
		current = next
	}
	return current[0]
}

// BuildProof constructs a Merkle inclusion proof for leafIndex.
// Returns (root hex, path steps, error).
func BuildProof(commitments []string, leafIndex int) (root string, path []PathStep, err error) {
	if len(commitments) == 0 {
		return "", nil, fmt.Errorf("commitments cannot be empty")
	}
	if leafIndex < 0 || leafIndex >= len(commitments) {
		return "", nil, fmt.Errorf("leaf index %d out of range [0, %d)", leafIndex, len(commitments))
	}

	layer := make([][]byte, len(commitments))
	for i, c := range commitments {
		b, decErr := HexTo32(c)
		if decErr != nil {
			return "", nil, fmt.Errorf("invalid commitment at index %d: %w", i, decErr)
		}
		layer[i] = b
	}

	rootBytes := ComputeRoot(layer)

	// Rebuild layer for path extraction.
	pathLayer := make([][]byte, len(commitments))
	for i, c := range commitments {
		b, _ := HexTo32(c) // already validated above
		pathLayer[i] = b
	}

	path, err = buildPath(pathLayer, leafIndex)
	if err != nil {
		return "", nil, err
	}

	return fmt.Sprintf("%x", rootBytes), path, nil
}

// Verify checks a Merkle inclusion proof. Returns nil on success.
func Verify(root, commitment string, proofPath []PathStep) error {
	current, err := HexTo32(commitment)
	if err != nil {
		return fmt.Errorf("invalid commitment: %w", err)
	}

	for _, step := range proofPath {
		sibling, sErr := HexTo32(step.SiblingHash)
		if sErr != nil {
			return fmt.Errorf("invalid sibling hash: %w", sErr)
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
	if computed != root {
		return fmt.Errorf("proof invalid: computed root %s != expected %s", computed, root)
	}
	return nil
}

// HexTo32 decodes a 64-char hex string to a 32-byte slice.
func HexTo32(h string) ([]byte, error) {
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

// buildPath extracts the Merkle path for leafIndex from a layer of 32-byte nodes.
func buildPath(layer [][]byte, leafIndex int) ([]PathStep, error) {
	var path []PathStep
	idx := leafIndex

	for len(layer) > 1 {
		var next [][]byte
		for i := 0; i < len(layer); i += 2 {
			left := layer[i]
			right := left
			if i+1 < len(layer) {
				right = layer[i+1]
			}

			if i == idx || i+1 == idx {
				var step PathStep
				if i == idx {
					step = PathStep{SiblingHash: fmt.Sprintf("%x", right), IsLeft: false}
				} else {
					step = PathStep{SiblingHash: fmt.Sprintf("%x", left), IsLeft: true}
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
