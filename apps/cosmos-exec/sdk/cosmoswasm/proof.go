package cosmoswasm

import (
	"errors"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/merkle"
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
	root, path, err := merkle.BuildProof(commitments, leafIndex)
	if err != nil {
		return nil, err
	}

	steps := make([]MerklePathStep, len(path))
	for i, s := range path {
		steps[i] = MerklePathStep{SiblingHash: s.SiblingHash, IsLeft: s.IsLeft}
	}

	return &MerkleProof{
		Root:       root,
		LeafIndex:  leafIndex,
		Commitment: commitments[leafIndex],
		Path:       steps,
	}, nil
}

// VerifyMerkleProof verifies that commitment is included in the batch whose
// Merkle root equals proof.Root.  Returns nil on success.
func VerifyMerkleProof(proof *MerkleProof) error {
	if proof == nil {
		return errors.New("proof is nil")
	}

	internalPath := make([]merkle.PathStep, len(proof.Path))
	for i, s := range proof.Path {
		internalPath[i] = merkle.PathStep{SiblingHash: s.SiblingHash, IsLeft: s.IsLeft}
	}

	return merkle.Verify(proof.Root, proof.Commitment, internalPath)
}
