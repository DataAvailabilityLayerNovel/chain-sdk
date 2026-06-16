package cosmoswasm

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"
)

// ── compress ────────────────────────────────────────────────────────────────

func TestCompress_RoundTripBeneficial(t *testing.T) {
	// Highly compressible data → CompressIfBeneficial should shrink it.
	data := []byte(strings.Repeat("telemetry-frame-telemetry-frame-", 500))

	out, did := CompressIfBeneficial(data)
	if !did {
		t.Fatal("expected compression to be beneficial for repetitive data")
	}
	if len(out) >= len(data) {
		t.Errorf("compressed size %d not smaller than original %d", len(out), len(data))
	}
	if !IsGzipCompressed(out) {
		t.Error("compressed output should be detected as gzip")
	}

	back, err := MaybeDecompress(out)
	if err != nil {
		t.Fatalf("MaybeDecompress: %v", err)
	}
	if !bytes.Equal(back, data) {
		t.Error("round-trip mismatch")
	}
}

func TestCompress_NotBeneficialOnRandom(t *testing.T) {
	// Random data does not compress → IfBeneficial returns original + false.
	data := make([]byte, 4096)
	_, _ = rand.Read(data)

	out, did := CompressIfBeneficial(data)
	if did {
		t.Error("random data should not be beneficially compressible")
	}
	if !bytes.Equal(out, data) {
		t.Error("non-beneficial case must return original data unchanged")
	}

	// MaybeDecompress on non-gzip data returns it unchanged.
	back, err := MaybeDecompress(data)
	if err != nil {
		t.Fatalf("MaybeDecompress(plain): %v", err)
	}
	if !bytes.Equal(back, data) {
		t.Error("MaybeDecompress should pass plain data through unchanged")
	}
}

// ── chunk ───────────────────────────────────────────────────────────────────

func TestChunk_SmallFitsOnePiece(t *testing.T) {
	data := []byte("small payload")
	chunks, meta := ChunkBlob(data, 1024)
	if len(chunks) != 1 || meta != nil {
		t.Fatalf("small data should be 1 chunk with nil meta, got %d chunks meta=%v", len(chunks), meta)
	}
}

func TestChunk_SplitReassemble(t *testing.T) {
	data := make([]byte, 5000)
	for i := range data {
		data[i] = byte(i % 251)
	}

	chunks, meta := ChunkBlob(data, 1024)
	if meta == nil {
		t.Fatal("expected non-nil meta for oversized data")
	}
	if meta.TotalChunks != len(chunks) || len(chunks) != 5 {
		t.Fatalf("expected 5 chunks, got %d (meta.TotalChunks=%d)", len(chunks), meta.TotalChunks)
	}

	got, err := ReassembleChunks(chunks, meta)
	if err != nil {
		t.Fatalf("ReassembleChunks: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Error("reassembled data mismatch")
	}
}

func TestChunk_TamperDetected(t *testing.T) {
	data := make([]byte, 3000)
	for i := range data {
		data[i] = byte(i)
	}
	chunks, meta := ChunkBlob(data, 1024)

	// Corrupt one byte in the first chunk → integrity check must fail.
	chunks[0][0] ^= 0xff
	if _, err := ReassembleChunks(chunks, meta); err == nil {
		t.Error("expected integrity error after tampering a chunk")
	}
}

// ── merkle ──────────────────────────────────────────────────────────────────

func TestMerkle_ProofRoundTrip(t *testing.T) {
	// 32-byte hex commitments (the shape SubmitBatch produces).
	commitments := []string{
		strings.Repeat("11", 32),
		strings.Repeat("22", 32),
		strings.Repeat("33", 32),
		strings.Repeat("44", 32),
		strings.Repeat("55", 32),
	}

	var root string
	for i := range commitments {
		proof, err := BuildMerkleProof(commitments, i)
		if err != nil {
			t.Fatalf("BuildMerkleProof[%d]: %v", i, err)
		}
		if proof.Commitment != commitments[i] || proof.Index != i {
			t.Errorf("proof[%d] wrong commitment/index", i)
		}
		if i == 0 {
			root = proof.Root
		} else if proof.Root != root {
			t.Errorf("proof[%d] root %s != %s", i, proof.Root, root)
		}
		if err := VerifyMerkleProof(proof); err != nil {
			t.Errorf("VerifyMerkleProof[%d]: %v", i, err)
		}
	}
}

func TestMerkle_TamperedProofRejected(t *testing.T) {
	commitments := []string{
		strings.Repeat("aa", 32),
		strings.Repeat("bb", 32),
		strings.Repeat("cc", 32),
	}
	proof, err := BuildMerkleProof(commitments, 1)
	if err != nil {
		t.Fatalf("BuildMerkleProof: %v", err)
	}

	// Wrong commitment for the same path → must fail.
	bad := *proof
	bad.Commitment = strings.Repeat("ff", 32)
	if err := VerifyMerkleProof(&bad); err == nil {
		t.Error("expected verification failure for wrong commitment")
	}

	// Tampered root → must fail.
	bad2 := *proof
	bad2.Root = strings.Repeat("00", 32)
	if err := VerifyMerkleProof(&bad2); err == nil {
		t.Error("expected verification failure for tampered root")
	}
}

func TestMerkle_BuildProof_OutOfRange(t *testing.T) {
	if _, err := BuildMerkleProof([]string{strings.Repeat("11", 32)}, 5); err == nil {
		t.Error("expected error for out-of-range index")
	}
	if _, err := BuildMerkleProof(nil, 0); err == nil {
		t.Error("expected error for empty commitments")
	}
}
