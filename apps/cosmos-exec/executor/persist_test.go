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

	txResults, txSkipped, err := ps2.LoadTxResults()
	if err != nil {
		t.Fatalf("load tx results: %v", err)
	}
	if txSkipped != 0 {
		t.Fatalf("expected 0 skipped tx lines, got %d", txSkipped)
	}
	if len(txResults) != 1 {
		t.Fatalf("expected 1 tx result, got %d", len(txResults))
	}
	if txResults["abc123"].Height != 5 {
		t.Fatalf("expected height 5, got %d", txResults["abc123"].Height)
	}

	blocks, blockSkipped, err := ps2.LoadBlocks()
	if err != nil {
		t.Fatalf("load blocks: %v", err)
	}
	if blockSkipped != 0 {
		t.Fatalf("expected 0 skipped block lines, got %d", blockSkipped)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if blocks[5].AppHash != "deadbeef" {
		t.Fatalf("expected app hash deadbeef, got %s", blocks[5].AppHash)
	}

	store := NewBlobStore()
	loaded, blobSkipped, err := ps2.LoadBlobs(store)
	if err != nil {
		t.Fatalf("load blobs: %v", err)
	}
	if blobSkipped != 0 {
		t.Fatalf("expected 0 skipped blob lines, got %d", blobSkipped)
	}
	if loaded != 1 {
		t.Fatalf("expected 1 blob loaded, got %d", loaded)
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

	txResults, _, err := ps.LoadTxResults()
	if err != nil {
		t.Fatalf("load tx results: %v", err)
	}
	if len(txResults) != 0 {
		t.Fatalf("expected 0 tx results from empty store, got %d", len(txResults))
	}

	blocks, _, err := ps.LoadBlocks()
	if err != nil {
		t.Fatalf("load blocks: %v", err)
	}
	if len(blocks) != 0 {
		t.Fatalf("expected 0 blocks from empty store, got %d", len(blocks))
	}
}

func TestPersistStore_MetadataRoundTrip(t *testing.T) {
	dir := t.TempDir()

	ps, err := NewPersistStore(dir)
	if err != nil {
		t.Fatalf("create persist store: %v", err)
	}

	meta := ChainMetadata{
		Initialized:     true,
		ChainID:         "test-chain-1",
		StateRoot:       "deadbeef01020304",
		LastHeight:      42,
		FinalizedHeight: 40,
	}
	if err := ps.SaveMetadata(meta); err != nil {
		t.Fatalf("save metadata: %v", err)
	}
	ps.Close()

	// Reopen and verify.
	ps2, err := NewPersistStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer ps2.Close()

	loaded, err := ps2.LoadMetadata()
	if err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	if !loaded.Initialized {
		t.Fatal("expected initialized=true")
	}
	if loaded.ChainID != "test-chain-1" {
		t.Fatalf("expected chain ID 'test-chain-1', got %q", loaded.ChainID)
	}
	if loaded.StateRoot != "deadbeef01020304" {
		t.Fatalf("expected state root 'deadbeef01020304', got %q", loaded.StateRoot)
	}
	if loaded.LastHeight != 42 {
		t.Fatalf("expected last height 42, got %d", loaded.LastHeight)
	}
	if loaded.FinalizedHeight != 40 {
		t.Fatalf("expected finalized height 40, got %d", loaded.FinalizedHeight)
	}
}

func TestPersistStore_MetadataEmpty(t *testing.T) {
	dir := t.TempDir()

	ps, err := NewPersistStore(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer ps.Close()

	meta, err := ps.LoadMetadata()
	if err != nil {
		t.Fatalf("load metadata from fresh dir: %v", err)
	}
	if meta.Initialized {
		t.Fatal("expected initialized=false for fresh store")
	}
	if meta.ChainID != "" {
		t.Fatalf("expected empty chain ID, got %q", meta.ChainID)
	}
}

func TestPersistStore_MetadataOverwrite(t *testing.T) {
	dir := t.TempDir()

	ps, err := NewPersistStore(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Write first version.
	ps.SaveMetadata(ChainMetadata{Initialized: true, ChainID: "v1", LastHeight: 1})
	// Overwrite with second version.
	ps.SaveMetadata(ChainMetadata{Initialized: true, ChainID: "v1", LastHeight: 99})
	ps.Close()

	ps2, _ := NewPersistStore(dir)
	defer ps2.Close()

	meta, _ := ps2.LoadMetadata()
	if meta.LastHeight != 99 {
		t.Fatalf("expected last height 99 after overwrite, got %d", meta.LastHeight)
	}
}

func TestPersistStore_SkippedCorruptLines(t *testing.T) {
	dir := t.TempDir()

	ps, err := NewPersistStore(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Write valid data.
	ps.AppendTxResult(TxExecutionResult{Hash: "good", Height: 1})
	// Write corrupt line directly.
	ps.Close()

	// Append garbage to the file.
	f, _ := os.OpenFile(filepath.Join(dir, "tx_results.jsonl"), os.O_APPEND|os.O_WRONLY, 0o644)
	f.WriteString("this is not json\n")
	f.Close()

	ps2, _ := NewPersistStore(dir)
	defer ps2.Close()

	results, skipped, err := ps2.LoadTxResults()
	if err != nil {
		t.Fatalf("load should succeed with skipped lines: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 valid result, got %d", len(results))
	}
	if skipped != 1 {
		t.Fatalf("expected 1 skipped line, got %d", skipped)
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
