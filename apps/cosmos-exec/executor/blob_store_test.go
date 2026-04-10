package executor

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestBlobStore_PutGet(t *testing.T) {
	s := NewBlobStore()

	data := []byte("hello blob world")
	commitment, err := s.Put(data)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if len(commitment) != 64 { // hex SHA-256
		t.Fatalf("expected 64-char hex commitment, got %d chars", len(commitment))
	}

	got, ok := s.Get(commitment)
	if !ok {
		t.Fatal("Get returned false for existing blob")
	}
	if !bytes.Equal(got, data) {
		t.Fatal("Get data mismatch")
	}
}

func TestBlobStore_PutIdempotent(t *testing.T) {
	s := NewBlobStore()
	data := []byte("same data")

	c1, _ := s.Put(data)
	c2, _ := s.Put(data)
	if c1 != c2 {
		t.Fatalf("expected same commitment, got %s vs %s", c1, c2)
	}
	if s.Count() != 1 {
		t.Fatalf("expected 1 blob, got %d", s.Count())
	}
}

func TestBlobStore_PutEmpty(t *testing.T) {
	s := NewBlobStore()
	_, err := s.Put(nil)
	if err == nil {
		t.Fatal("expected error for nil data")
	}
	_, err = s.Put([]byte{})
	if err == nil {
		t.Fatal("expected error for empty data")
	}
}

func TestBlobStore_PutExceedsMaxBlobSize(t *testing.T) {
	s := NewBlobStore()
	oversized := make([]byte, DefaultMaxBlobSize+1)
	_, err := s.Put(oversized)
	if err == nil {
		t.Fatal("expected error for oversized blob")
	}
	if !strings.Contains(err.Error(), "exceeds max") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBlobStore_PutExceedsTotalSize(t *testing.T) {
	s := NewBlobStore()
	// Override limits for a small test.
	s.maxBlobSize = 100
	s.maxTotalSize = 250

	// Each blob must be unique to avoid idempotent dedup.
	blob1 := bytes.Repeat([]byte{0x01}, 100)
	blob2 := bytes.Repeat([]byte{0x02}, 100)
	blob3 := bytes.Repeat([]byte{0x03}, 100)

	if _, err := s.Put(blob1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(blob2); err != nil {
		t.Fatal(err)
	}
	// Third blob should push over 250.
	_, err := s.Put(blob3)
	if err == nil {
		t.Fatal("expected store-full error")
	}
	if !strings.Contains(err.Error(), "store full") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBlobStore_GetMissing(t *testing.T) {
	s := NewBlobStore()
	_, ok := s.Get("nonexistent")
	if ok {
		t.Fatal("expected false for missing blob")
	}
}

func TestBlobStore_PutBatch(t *testing.T) {
	s := NewBlobStore()

	blobs := [][]byte{
		[]byte("blob-0"),
		[]byte("blob-1"),
		[]byte("blob-2"),
	}

	root, commitments, err := s.PutBatch(blobs)
	if err != nil {
		t.Fatalf("PutBatch: %v", err)
	}
	if len(root) != 64 {
		t.Fatalf("expected 64-char root, got %d", len(root))
	}
	if len(commitments) != 3 {
		t.Fatalf("expected 3 commitments, got %d", len(commitments))
	}

	// Verify every blob is retrievable.
	for i, c := range commitments {
		got, ok := s.Get(c)
		if !ok {
			t.Fatalf("blob[%d] not found by commitment %s", i, c)
		}
		if !bytes.Equal(got, blobs[i]) {
			t.Fatalf("blob[%d] data mismatch", i)
		}
	}
}

func TestBlobStore_PutBatch_Empty(t *testing.T) {
	s := NewBlobStore()
	_, _, err := s.PutBatch(nil)
	if err == nil {
		t.Fatal("expected error for empty batch")
	}
}

func TestBlobStore_PutBatch_Deterministic(t *testing.T) {
	s := NewBlobStore()
	blobs := [][]byte{[]byte("a"), []byte("b"), []byte("c")}

	root1, _, _ := s.PutBatch(blobs)
	root2, _, _ := s.PutBatch(blobs)
	if root1 != root2 {
		t.Fatalf("merkle root not deterministic: %s vs %s", root1, root2)
	}
}

func TestMerkleRoot_SingleLeaf(t *testing.T) {
	// Single leaf → root = leaf commitment.
	commitments := []string{blobCommitment([]byte("only"))}
	root := merkleRoot(commitments)
	if root != commitments[0] {
		t.Fatalf("single-leaf root should equal the commitment: %s vs %s", root, commitments[0])
	}
}

func TestMerkleRoot_TwoLeaves(t *testing.T) {
	c0 := blobCommitment([]byte("left"))
	c1 := blobCommitment([]byte("right"))
	root := merkleRoot([]string{c0, c1})
	if root == c0 || root == c1 {
		t.Fatal("root should differ from both leaves")
	}
	if len(root) != 64 {
		t.Fatalf("expected 64-char root, got %d", len(root))
	}
}

func TestBlobStore_CountAndTotalBytes(t *testing.T) {
	s := NewBlobStore()
	s.Put([]byte("aaa")) //nolint:errcheck
	s.Put([]byte("bb"))  //nolint:errcheck

	if s.Count() != 2 {
		t.Fatalf("expected 2, got %d", s.Count())
	}
	if s.TotalBytes() != 5 {
		t.Fatalf("expected 5, got %d", s.TotalBytes())
	}
}

func BenchmarkBlobStore_Put(b *testing.B) {
	s := NewBlobStore()
	data := bytes.Repeat([]byte("x"), 1024)
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for i := range b.N {
		// Vary data so each is unique (not idempotent shortcut).
		d := append(data, byte(i), byte(i>>8), byte(i>>16)) //nolint:gocritic
		s.Put(d)                                              //nolint:errcheck
	}
}

func BenchmarkBlobStore_PutBatch(b *testing.B) {
	blobs := make([][]byte, 100)
	for i := range blobs {
		blobs[i] = []byte(fmt.Sprintf("blob-%d-data-padding-xxxxxxxxxxxxxx", i))
	}
	b.ResetTimer()
	for range b.N {
		s := NewBlobStore()
		s.PutBatch(blobs) //nolint:errcheck
	}
}
