package cosmoswasm

import (
	"context"
	"testing"
	"time"
)

// TestIntegration_FullSDKWorkflow exercises the complete SDK lifecycle using
// mocks — no running chain required. It mirrors what a real user would do:
//
//  1. Create client (mock)
//  2. Submit single blob → retrieve by commitment
//  3. Submit batch → verify Merkle proof
//  4. CommitRoot → verify on-chain receipt
//  5. Build + submit tx → wait for result
//  6. Query smart contract
//  7. BatchBuilder auto-flush
//  8. DA bridge: submit + commit + poll
//  9. Cost estimation
//  10. Chunk + reassemble large blob
//  11. Compress + decompress
func TestIntegration_FullSDKWorkflow(t *testing.T) {
	ctx := context.Background()
	mock := NewMockClient()

	// ── 1. Single blob round-trip ──────────────────────────────────────────
	t.Run("blob_roundtrip", func(t *testing.T) {
		data := []byte(`{"event":"player_joined","id":42}`)
		res, err := mock.SubmitBlob(ctx, data)
		if err != nil {
			t.Fatalf("SubmitBlob: %v", err)
		}
		got, err := mock.RetrieveBlobData(ctx, res.Commitment)
		if err != nil {
			t.Fatalf("RetrieveBlobData: %v", err)
		}
		if string(got) != string(data) {
			t.Fatalf("data mismatch: %q vs %q", got, data)
		}
	})

	// ── 2. Batch + Merkle proof ────────────────────────────────────────────
	t.Run("batch_and_proof", func(t *testing.T) {
		blobs := [][]byte{
			[]byte(`{"move":"e2e4"}`),
			[]byte(`{"move":"e7e5"}`),
			[]byte(`{"move":"d2d4"}`),
		}
		batch, err := mock.SubmitBatch(ctx, blobs)
		if err != nil {
			t.Fatalf("SubmitBatch: %v", err)
		}
		if batch.Count != 3 {
			t.Fatalf("expected 3, got %d", batch.Count)
		}
		if batch.Root == "" {
			t.Fatal("expected non-empty root")
		}

		// Verify proof for each leaf.
		for i := range blobs {
			proof, err := GetProof(batch.Commitments, i)
			if err != nil {
				t.Fatalf("GetProof[%d]: %v", i, err)
			}
			if err := VerifyMerkleProof(proof); err != nil {
				t.Fatalf("VerifyMerkleProof[%d]: %v", i, err)
			}
		}
	})

	// ── 3. CommitRoot (blobs + on-chain Merkle root) ───────────────────────
	t.Run("commit_root", func(t *testing.T) {
		blobs := [][]byte{[]byte("state-snapshot-1"), []byte("state-snapshot-2")}
		receipt, err := mock.CommitRoot(ctx, CommitRootRequest{
			Blobs:    blobs,
			Contract: "wasm1gamecontract",
			Tag:      "snapshots",
		})
		if err != nil {
			t.Fatalf("CommitRoot: %v", err)
		}
		if receipt.Root == "" {
			t.Fatal("expected Merkle root")
		}
		if len(receipt.Refs) != 2 {
			t.Fatalf("expected 2 refs, got %d", len(receipt.Refs))
		}
		if receipt.TxHash == "" {
			t.Fatal("expected tx hash")
		}

		// Verify tx was recorded.
		txRes, err := mock.GetTxResult(ctx, receipt.TxHash)
		if err != nil {
			t.Fatalf("GetTxResult: %v", err)
		}
		if !txRes.Found || txRes.Result.Code != 0 {
			t.Fatalf("tx not found or failed: found=%v code=%d", txRes.Found, txRes.Result.Code)
		}
	})

	// ── 4. Transaction lifecycle ───────────────────────────────────────────
	t.Run("tx_lifecycle", func(t *testing.T) {
		wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
		tx, err := BuildStoreTx(wasmBytes, "")
		if err != nil {
			t.Fatalf("BuildStoreTx: %v", err)
		}

		submitRes, err := mock.SubmitTxBytes(ctx, tx)
		if err != nil {
			t.Fatalf("SubmitTxBytes: %v", err)
		}

		result, err := mock.WaitTxResult(ctx, submitRes.Hash, 10*time.Millisecond)
		if err != nil {
			t.Fatalf("WaitTxResult: %v", err)
		}
		if result.Code != 0 {
			t.Fatalf("expected success, got code=%d", result.Code)
		}

		// Also test base64 encoding.
		b64 := EncodeTxBase64(tx)
		if b64 == "" {
			t.Fatal("expected non-empty base64")
		}
	})

	// ── 5. Smart query ─────────────────────────────────────────────────────
	t.Run("smart_query", func(t *testing.T) {
		mock.OnQuery(func(contract string, msg any) (*QuerySmartResponse, error) {
			return &QuerySmartResponse{Data: map[string]any{"count": 7}}, nil
		})

		result, err := mock.QuerySmart(ctx, "wasm1counter", map[string]any{"get_count": struct{}{}})
		if err != nil {
			t.Fatalf("QuerySmart: %v", err)
		}
		if result["count"] != 7 {
			t.Fatalf("expected count=7, got %v", result["count"])
		}
	})

	// ── 6. BatchBuilder ────────────────────────────────────────────────────
	t.Run("batch_builder", func(t *testing.T) {
		bb := NewBatchBuilder(mock, BatchBuilderConfig{
			Contract: "wasm1game",
			Tag:      "events",
			MaxBytes: 200, // low threshold to trigger auto-flush
		})

		flushCount := 0
		flush := func(ctx context.Context, blobs [][]byte) (*CommitReceipt, error) {
			flushCount++
			return mock.CommitRoot(ctx, CommitRootRequest{Blobs: blobs, Tag: "events"})
		}

		// Add blobs until auto-flush triggers.
		for i := 0; i < 10; i++ {
			bb.Add(ctx, []byte(`{"tick":true,"seq":`+string(rune('0'+i))+`}`), flush)
		}

		// Final flush for any remaining.
		bb.Flush(ctx, flush)

		if flushCount == 0 {
			t.Fatal("expected at least one flush")
		}
	})

	// ── 7. DA Bridge full cycle ────────────────────────────────────────────
	t.Run("da_bridge", func(t *testing.T) {
		da := NewMockDAClient()
		ns := NamespaceFromString("my-game")
		bridge := NewDABridge(da, mock, ns)

		// Submit blobs to DA.
		daResult, err := bridge.Submit(ctx, [][]byte{[]byte("da-event-1")}, nil)
		if err != nil {
			t.Fatalf("DA Submit: %v", err)
		}
		if daResult.Height == 0 {
			t.Fatal("expected DA height > 0")
		}

		// Retrieve from DA.
		blobs, err := bridge.GetBlobs(ctx, daResult.Height)
		if err != nil {
			t.Fatalf("GetBlobs: %v", err)
		}
		if len(blobs) != 1 || string(blobs[0].Data) != "da-event-1" {
			t.Fatal("DA blob mismatch")
		}

		// SubmitAndCommit (DA + on-chain).
		receipt, err := bridge.SubmitAndCommit(ctx, SubmitAndCommitRequest{
			Blobs:    [][]byte{[]byte("commit-me")},
			Contract: "wasm1game",
			Tag:      "da-commit",
		})
		if err != nil {
			t.Fatalf("SubmitAndCommit: %v", err)
		}
		if receipt.DAResult.BlobCount != 1 {
			t.Fatalf("expected 1 DA blob, got %d", receipt.DAResult.BlobCount)
		}
		if receipt.OnChainReceipt.Root == "" {
			t.Fatal("expected on-chain root")
		}

		// DA height tracking.
		h, err := bridge.DAHeight(ctx)
		if err != nil {
			t.Fatalf("DAHeight: %v", err)
		}
		if h == 0 {
			t.Fatal("expected non-zero DA height")
		}
	})

	// ── 8. Cost estimation ─────────────────────────────────────────────────
	t.Run("cost_estimation", func(t *testing.T) {
		est := EstimateCost(EstimateCostRequest{DataBytes: 500_000})
		if est.SavingsPercent <= 0 {
			t.Fatalf("expected positive savings for 500KB, got %.2f%%", est.SavingsPercent)
		}
		if est.BlobCommit.TotalGas >= est.DirectTx.TotalGas {
			t.Fatal("blob-first should be cheaper than direct on-chain")
		}
		if est.CompressedBytes >= est.DataBytes {
			t.Fatal("compressed should be smaller")
		}
	})

	// ── 9. Chunk + reassemble ──────────────────────────────────────────────
	t.Run("chunk_reassemble", func(t *testing.T) {
		// Create a blob larger than the chunk size.
		largeData := make([]byte, 1024*1024) // 1 MB
		for i := range largeData {
			largeData[i] = byte(i % 256)
		}

		chunks, meta := ChunkBlob(largeData, 256*1024) // 256 KB chunks
		if len(chunks) < 2 {
			t.Fatalf("expected multiple chunks, got %d", len(chunks))
		}

		reassembled, err := ReassembleChunks(chunks, meta)
		if err != nil {
			t.Fatalf("ReassembleChunks: %v", err)
		}
		if len(reassembled) != len(largeData) {
			t.Fatalf("size mismatch: %d vs %d", len(reassembled), len(largeData))
		}
		for i := range largeData {
			if reassembled[i] != largeData[i] {
				t.Fatalf("byte mismatch at %d", i)
				break
			}
		}
	})

	// ── 10. Compress + decompress ──────────────────────────────────────────
	t.Run("compress_decompress", func(t *testing.T) {
		original := []byte(`{"repeated":"data data data data data data data data data"}`)
		compressed, err := CompressGzip(original)
		if err != nil {
			t.Fatalf("CompressGzip: %v", err)
		}
		if !IsGzipCompressed(compressed) {
			t.Fatal("expected gzip-compressed data")
		}

		decompressed, err := DecompressGzip(compressed)
		if err != nil {
			t.Fatalf("DecompressGzip: %v", err)
		}
		if string(decompressed) != string(original) {
			t.Fatal("decompress mismatch")
		}

		// MaybeDecompress on non-compressed data should return as-is.
		plain := []byte("not compressed")
		result, err := MaybeDecompress(plain)
		if err != nil {
			t.Fatalf("MaybeDecompress: %v", err)
		}
		if string(result) != string(plain) {
			t.Fatal("MaybeDecompress should pass through non-gzip data")
		}
	})

	// ── 11. Namespace operations ───────────────────────────────────────────
	t.Run("namespace", func(t *testing.T) {
		ns := NamespaceFromString("test-app")
		if ns == nil {
			t.Fatal("expected non-nil namespace")
		}

		hexStr := ns.Hex()
		ns2, err := NamespaceFromHex(hexStr)
		if err != nil {
			t.Fatalf("NamespaceFromHex: %v", err)
		}
		if ns.Hex() != ns2.Hex() {
			t.Fatal("namespace hex round-trip failed")
		}
	})

	// ── 12. Tx builders ────────────────────────────────────────────────────
	t.Run("tx_builders", func(t *testing.T) {
		// InstantiateTx
		tx, err := BuildInstantiateTx(InstantiateTxRequest{
			CodeID: 1,
			Msg:    map[string]any{"count": 0},
			Label:  "counter",
		})
		if err != nil {
			t.Fatalf("BuildInstantiateTx: %v", err)
		}
		if len(tx) == 0 {
			t.Fatal("expected non-empty tx bytes")
		}

		// ExecuteTx
		tx, err = BuildExecuteTx(ExecuteTxRequest{
			Contract: "wasm1counter",
			Msg:      map[string]any{"increment": struct{}{}},
		})
		if err != nil {
			t.Fatalf("BuildExecuteTx: %v", err)
		}
		if len(tx) == 0 {
			t.Fatal("expected non-empty tx bytes")
		}

		// BlobCommitTx
		tx, err = BuildBlobCommitTx(BlobCommitTxRequest{
			Commitment: "abc123",
			Contract:   "wasm1store",
			Tag:        "blob-v1",
		})
		if err != nil {
			t.Fatalf("BuildBlobCommitTx: %v", err)
		}
		if len(tx) == 0 {
			t.Fatal("expected non-empty tx bytes")
		}

		// BatchRootTx
		tx, err = BuildBatchRootTx(BatchRootTxRequest{
			Root:     "deadbeef",
			Count:    5,
			Contract: "wasm1store",
		})
		if err != nil {
			t.Fatalf("BuildBatchRootTx: %v", err)
		}
		if len(tx) == 0 {
			t.Fatal("expected non-empty tx bytes")
		}
	})
}
