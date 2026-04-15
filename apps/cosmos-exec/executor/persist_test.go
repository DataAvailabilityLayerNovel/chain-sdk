package executor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	ps, err := NewPersistStore(dir)
	if err != nil {
		t.Fatalf("create persist store: %v", err)
	}

	// Write some data.
	txResult := TxExecutionResult{Hash: "abc123", Height: 5, Code: 0, Log: "ok"}
	if err := ps.AppendTxResult(txResult); err != nil {
		t.Fatalf("append tx result: %v", err)
	}

	block := BlockInfo{Height: 5, Time: "2026-01-01T00:00:00Z", AppHash: "deadbeef", NumTxs: 1}
	if err := ps.AppendBlock(block); err != nil {
		t.Fatalf("append block: %v", err)
	}

	if err := ps.AppendBlob("aabbcc", []byte("hello world")); err != nil {
		t.Fatalf("append blob: %v", err)
	}

	ps.Close()

	// Reopen and verify.
	ps2, err := NewPersistStore(dir)
	if err != nil {
		t.Fatalf("reopen persist store: %v", err)
	}
	defer ps2.Close()

	txResults, err := ps2.LoadTxResults()
	if err != nil {
		t.Fatalf("load tx results: %v", err)
	}
	if len(txResults) != 1 {
		t.Fatalf("expected 1 tx result, got %d", len(txResults))
	}
	if txResults["abc123"].Height != 5 {
		t.Fatalf("expected height 5, got %d", txResults["abc123"].Height)
	}

	blocks, err := ps2.LoadBlocks()
	if err != nil {
		t.Fatalf("load blocks: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[5].AppHash != "deadbeef" {
		t.Fatalf("expected app hash deadbeef, got %s", blocks[5].AppHash)
	}

	store := NewBlobStore()
	count, err := ps2.LoadBlobs(store)
	if err != nil {
		t.Fatalf("load blobs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 blob loaded, got %d", count)
	}
	if store.Count() != 1 {
		t.Fatalf("expected blob store count 1, got %d", store.Count())
	}
}

func TestPersistStore_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	ps, err := NewPersistStore(dir)
	if err != nil {
		t.Fatalf("create persist store: %v", err)
	}
	defer ps.Close()

	txResults, _ := ps.LoadTxResults()
	if len(txResults) != 0 {
		t.Fatalf("expected 0 tx results from empty store, got %d", len(txResults))
	}

	blocks, _ := ps.LoadBlocks()
	if len(blocks) != 0 {
		t.Fatalf("expected 0 blocks from empty store, got %d", len(blocks))
	}
}

func TestPersistStore_FilesExist(t *testing.T) {
	dir := t.TempDir()

	ps, err := NewPersistStore(dir)
	if err != nil {
		t.Fatalf("create persist store: %v", err)
	}
	ps.AppendTxResult(TxExecutionResult{Hash: "test"})
	ps.Close()

	for _, name := range []string{"tx_results.jsonl", "blocks.jsonl", "blobs.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, name)); os.IsNotExist(err) {
			t.Errorf("expected file %s to exist", name)
		}
	}
}
