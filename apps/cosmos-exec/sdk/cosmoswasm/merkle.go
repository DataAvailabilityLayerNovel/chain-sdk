// package cosmoswasm: lớp public mỏng cho MERKLE PROOF trên batch blob, bọc
// internal/merkle. Mục đích trong blob-first: SubmitBatch neo MỘT root cho N
// blob; merkle proof cho phép chứng minh "blob thứ k thuộc batch đã neo on-chain"
// chỉ bằng ~log2(N) hash, không cần đưa cả N commitment.
//
// Lưu ý: đây là cây SHA-256 nhị phân CỦA SDK (gom các commitment lại), KHÁC với
// NMT commitment của từng blob do Celestia tính. NMT chứng minh "blob nằm trên
// Celestia"; merkle này chứng minh "blob thuộc batch của tôi".
package cosmoswasm

import (
	"errors"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/merkle"
)

// ProofStep is one node on a Merkle inclusion path: the sibling hash to combine
// with, and whether that sibling sits on the left.
//
// VI: 1 bước trên đường đi từ leaf lên root — hash anh em + nằm trái hay phải.
type ProofStep struct {
	SiblingHash string `json:"sibling_hash"`
	IsLeft      bool   `json:"is_left"`
}

// MerkleProof is a self-contained inclusion proof: Commitment + Path hash up to
// Root. To trust it, a verifier must (1) call VerifyMerkleProof to check the
// path is internally consistent, AND (2) check Root equals the root they trust
// (e.g. the one recorded on-chain by BuildBatchRootTx).
//
// VI: bằng chứng inclusion độc lập. Verifier phải vừa VerifyMerkleProof (path
// đúng) vừa tự kiểm Root == root đã neo on-chain.
type MerkleProof struct {
	Root       string      `json:"root"`
	Commitment string      `json:"commitment"`
	Index      int         `json:"index"`
	Path       []ProofStep `json:"path"`
}

// BuildMerkleProof builds an inclusion proof for commitments[index] over the
// ordered list of batch commitments (e.g. BlobBatchResponse.Commitments).
//
// VI: tạo proof cho commitment thứ index trong batch.
func BuildMerkleProof(commitments []string, index int) (*MerkleProof, error) {
	root, path, err := merkle.BuildProof(commitments, index)
	if err != nil {
		return nil, err
	}
	steps := make([]ProofStep, len(path))
	for i, s := range path {
		steps[i] = ProofStep{SiblingHash: s.SiblingHash, IsLeft: s.IsLeft}
	}
	return &MerkleProof{
		Root:       root,
		Commitment: commitments[index],
		Index:      index,
		Path:       steps,
	}, nil
}

// VerifyMerkleProof checks that proof.Commitment, combined with proof.Path,
// hashes up to proof.Root. Returns nil if the proof is internally valid.
//
// IMPORTANT: this only proves the path is consistent. The caller must SEPARATELY
// verify proof.Root matches a root they trust (the on-chain batch root) —
// otherwise an attacker could present a proof against a fabricated root.
//
// VI: kiểm path dẫn Commitment lên Root. KHÔNG tự kiểm Root có đáng tin —
// caller phải tự so proof.Root với root đã neo on-chain.
func VerifyMerkleProof(proof *MerkleProof) error {
	if proof == nil {
		return errors.New("merkle proof is nil")
	}
	steps := make([]merkle.PathStep, len(proof.Path))
	for i, s := range proof.Path {
		steps[i] = merkle.PathStep{SiblingHash: s.SiblingHash, IsLeft: s.IsLeft}
	}
	return merkle.Verify(proof.Root, proof.Commitment, steps)
}
