package cosmoswasm

import (
	"bytes"
	"testing"
)

func TestChunkBlob_Small(t *testing.T) {
	data := []byte("hello world")
	chunks, meta := ChunkBlob(data, 1024)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if meta != nil {
		t.Fatal("expected nil meta for data that fits in one chunk")
	}
	if !bytes.Equal(chunks[0], data) {
		t.Fatal("chunk data mismatch")
	}
}

func TestChunkBlob_Large(t *testing.T) {
	data := bytes.Repeat([]byte("A"), 2500)
	chunks, meta := ChunkBlob(data, 1000)

	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
	if meta.TotalChunks != 3 {
		t.Fatalf("expected TotalChunks=3, got %d", meta.TotalChunks)
	}
	if len(meta.ChunkCommitments) != 3 {
		t.Fatalf("expected 3 chunk commitments, got %d", len(meta.ChunkCommitments))
	}

	// Reassemble and verify.
	reassembled, err := ReassembleChunks(chunks, meta)
	if err != nil {
		t.Fatalf("ReassembleChunks: %v", err)
	}
	if !bytes.Equal(reassembled, data) {
		t.Fatal("reassembled data mismatch")
	}
}

func TestReassembleChunks_IntegrityFail(t *testing.T) {
	data := bytes.Repeat([]byte("B"), 2000)
	chunks, meta := ChunkBlob(data, 1000)

	// Corrupt a chunk.
	chunks[0][0] = 0xFF
	_, err := ReassembleChunks(chunks, meta)
	if err == nil {
		t.Fatal("expected integrity check to fail")
	}
}

func TestChunkBlob_DefaultMaxChunkSize(t *testing.T) {
	// Passing maxChunkSize=0 should use the default.
	data := bytes.Repeat([]byte("C"), DefaultMaxChunkSize+1)
	chunks, meta := ChunkBlob(data, 0)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks with default max, got %d", len(chunks))
	}
	if meta == nil {
		t.Fatal("expected non-nil meta")
	}
}

func BenchmarkChunkBlob(b *testing.B) {
	data := bytes.Repeat([]byte("D"), 4*1024*1024) // 4 MB
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for range b.N {
		ChunkBlob(data, DefaultMaxChunkSize)
	}
}
