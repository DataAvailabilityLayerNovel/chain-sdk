package cosmoswasm

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func makeCommitments(n int) []string {
	commitments := make([]string, n)
	for i := range n {
		data := []byte(fmt.Sprintf("blob-%d", i))
		h := sha256.Sum256(data)
		commitments[i] = fmt.Sprintf("%x", h[:])
	}
	return commitments
}

func TestBuildAndVerifyMerkleProof(t *testing.T) {
	for _, n := range []int{1, 2, 3, 4, 5, 7, 8, 16, 17} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			commitments := makeCommitments(n)
			for leaf := range n {
				proof, err := BuildMerkleProof(commitments, leaf)
				if err != nil {
					t.Fatalf("BuildMerkleProof(n=%d, leaf=%d): %v", n, leaf, err)
				}
				if err := VerifyMerkleProof(proof); err != nil {
					t.Fatalf("VerifyMerkleProof(n=%d, leaf=%d): %v", n, leaf, err)
				}
			}
		})
	}
}

func TestMerkleProof_InvalidLeaf(t *testing.T) {
	commitments := makeCommitments(4)
	proof, err := BuildMerkleProof(commitments, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Tamper with the commitment (use commitment from a different blob).
	fakeData := []byte("totally-different-blob-data")
	h := sha256.Sum256(fakeData)
	proof.Commitment = fmt.Sprintf("%x", h[:])
	if err := VerifyMerkleProof(proof); err == nil {
		t.Fatal("expected verification to fail with wrong commitment")
	}
}

func TestMerkleProof_OutOfRange(t *testing.T) {
	commitments := makeCommitments(3)
	_, err := BuildMerkleProof(commitments, 5)
	if err == nil {
		t.Fatal("expected error for out-of-range leaf index")
	}
}

func BenchmarkBuildMerkleProof(b *testing.B) {
	commitments := makeCommitments(256)
	b.ResetTimer()
	for range b.N {
		BuildMerkleProof(commitments, 127) //nolint:errcheck
	}
}

func BenchmarkVerifyMerkleProof(b *testing.B) {
	commitments := makeCommitments(256)
	proof, _ := BuildMerkleProof(commitments, 127)
	b.ResetTimer()
	for range b.N {
		VerifyMerkleProof(proof) //nolint:errcheck
	}
}
