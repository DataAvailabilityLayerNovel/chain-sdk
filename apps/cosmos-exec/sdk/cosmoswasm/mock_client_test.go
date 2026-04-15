package cosmoswasm

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMockClient_SubmitAndRetrieveBlob(t *testing.T) {
	mock := NewMockClient()
	ctx := context.Background()

	data := []byte("hello blob")
	res, err := mock.SubmitBlob(ctx, data)
	if err != nil {
		t.Fatalf("SubmitBlob: %v", err)
	}
	if res.Commitment == "" {
		t.Fatal("expected non-empty commitment")
	}
	if res.Size != len(data) {
		t.Fatalf("expected size %d, got %d", len(data), res.Size)
	}

	// Retrieve by commitment.
	retrieved, err := mock.RetrieveBlobData(ctx, res.Commitment)
	if err != nil {
		t.Fatalf("RetrieveBlobData: %v", err)
	}
	if string(retrieved) != string(data) {
		t.Fatalf("expected %q, got %q", data, retrieved)
	}
}

func TestMockClient_SubmitBlobEmpty(t *testing.T) {
	mock := NewMockClient()
	_, err := mock.SubmitBlob(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty blob")
	}
}

func TestMockClient_SubmitBatch(t *testing.T) {
	mock := NewMockClient()
	ctx := context.Background()

	blobs := [][]byte{[]byte("a"), []byte("b"), []byte("c")}
	res, err := mock.SubmitBatch(ctx, blobs)
	if err != nil {
		t.Fatalf("SubmitBatch: %v", err)
	}
	if res.Count != 3 {
		t.Fatalf("expected count 3, got %d", res.Count)
	}
	if len(res.Commitments) != 3 {
		t.Fatalf("expected 3 commitments, got %d", len(res.Commitments))
	}
	if res.Root == "" {
		t.Fatal("expected non-empty root")
	}

	// Each blob should be retrievable.
	for i, c := range res.Commitments {
		data, err := mock.RetrieveBlobData(ctx, c)
		if err != nil {
			t.Fatalf("RetrieveBlobData[%d]: %v", i, err)
		}
		if string(data) != string(blobs[i]) {
			t.Fatalf("blob[%d]: expected %q, got %q", i, blobs[i], data)
		}
	}
}

func TestMockClient_SubmitTxAndWait(t *testing.T) {
	mock := NewMockClient()
	ctx := context.Background()

	txBytes := []byte("fake-tx-bytes")
	submitRes, err := mock.SubmitTxBytes(ctx, txBytes)
	if err != nil {
		t.Fatalf("SubmitTxBytes: %v", err)
	}
	if submitRes.Hash == "" {
		t.Fatal("expected non-empty hash")
	}

	// WaitTxResult should return immediately since mock auto-creates results.
	result, err := mock.WaitTxResult(ctx, submitRes.Hash, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitTxResult: %v", err)
	}
	if result.Hash != submitRes.Hash {
		t.Fatalf("hash mismatch: %s vs %s", result.Hash, submitRes.Hash)
	}
	if result.Code != 0 {
		t.Fatalf("expected code 0, got %d", result.Code)
	}
}

func TestMockClient_SetTxResult(t *testing.T) {
	mock := NewMockClient()
	ctx := context.Background()

	mock.SetTxResult("custom-hash", &TxExecutionResult{
		Hash:   "custom-hash",
		Height: 42,
		Code:   5,
		Log:    "some error",
	})

	res, err := mock.GetTxResult(ctx, "custom-hash")
	if err != nil {
		t.Fatalf("GetTxResult: %v", err)
	}
	if !res.Found {
		t.Fatal("expected found=true")
	}
	if res.Result.Code != 5 {
		t.Fatalf("expected code 5, got %d", res.Result.Code)
	}
	if res.Result.Height != 42 {
		t.Fatalf("expected height 42, got %d", res.Result.Height)
	}
}

func TestMockClient_GetTxResultNotFound(t *testing.T) {
	mock := NewMockClient()
	res, err := mock.GetTxResult(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("GetTxResult: %v", err)
	}
	if res.Found {
		t.Fatal("expected found=false for unknown hash")
	}
}

func TestMockClient_OnQuery(t *testing.T) {
	mock := NewMockClient()
	ctx := context.Background()

	mock.OnQuery(func(contract string, msg any) (*QuerySmartResponse, error) {
		if contract == "wasm1test" {
			return &QuerySmartResponse{Data: map[string]any{"balance": 100}}, nil
		}
		return nil, errors.New("unknown contract")
	})

	result, err := mock.QuerySmart(ctx, "wasm1test", map[string]any{"get_balance": struct{}{}})
	if err != nil {
		t.Fatalf("QuerySmart: %v", err)
	}
	if result["balance"] != 100 {
		t.Fatalf("expected balance=100, got %v", result["balance"])
	}

	// Unknown contract should error.
	_, err = mock.QuerySmart(ctx, "wasm1unknown", nil)
	if err == nil {
		t.Fatal("expected error for unknown contract")
	}
}

func TestMockClient_OnSubmit(t *testing.T) {
	mock := NewMockClient()
	ctx := context.Background()

	mock.OnSubmit(func(txBytes []byte) (*SubmitTxResponse, error) {
		return &SubmitTxResponse{Hash: "custom-submit-hash"}, nil
	})

	res, err := mock.SubmitTxBytes(ctx, []byte("tx"))
	if err != nil {
		t.Fatalf("SubmitTxBytes: %v", err)
	}
	if res.Hash != "custom-submit-hash" {
		t.Fatalf("expected custom-submit-hash, got %s", res.Hash)
	}
}

func TestMockClient_CommitRoot(t *testing.T) {
	mock := NewMockClient()
	ctx := context.Background()

	blobs := [][]byte{[]byte("event-1"), []byte("event-2")}
	receipt, err := mock.CommitRoot(ctx, CommitRootRequest{
		Blobs: blobs,
		Tag:   "test-events",
	})
	if err != nil {
		t.Fatalf("CommitRoot: %v", err)
	}
	if receipt.Root == "" {
		t.Fatal("expected non-empty root")
	}
	if len(receipt.Refs) != 2 {
		t.Fatalf("expected 2 refs, got %d", len(receipt.Refs))
	}
	if receipt.Tag != "test-events" {
		t.Fatalf("expected tag 'test-events', got %q", receipt.Tag)
	}
	if receipt.TxHash == "" {
		t.Fatal("expected non-empty tx hash")
	}

	// Verify each blob is retrievable via its commitment.
	for i, ref := range receipt.Refs {
		data, err := mock.RetrieveBlobData(ctx, ref.Commitment)
		if err != nil {
			t.Fatalf("RetrieveBlobData[%d]: %v", i, err)
		}
		if string(data) != string(blobs[i]) {
			t.Fatalf("blob[%d]: expected %q, got %q", i, blobs[i], data)
		}
	}

	// Verify the tx result exists.
	txRes, err := mock.GetTxResult(ctx, receipt.TxHash)
	if err != nil {
		t.Fatalf("GetTxResult: %v", err)
	}
	if !txRes.Found {
		t.Fatal("expected tx to be found")
	}
}

func TestMockClient_CommitRootEmptyBlobs(t *testing.T) {
	mock := NewMockClient()
	_, err := mock.CommitRoot(context.Background(), CommitRootRequest{})
	if err == nil {
		t.Fatal("expected error for empty blobs")
	}
}

func TestMockClient_BatchBuilder(t *testing.T) {
	mock := NewMockClient()
	ctx := context.Background()

	bb := NewBatchBuilder(mock, BatchBuilderConfig{
		Contract: "wasm1contract",
		Tag:      "test",
		MaxBytes: 100,
	})

	flush := func(ctx context.Context, blobs [][]byte) (*CommitReceipt, error) {
		return mock.CommitRoot(ctx, CommitRootRequest{
			Blobs: blobs,
			Tag:   "test",
		})
	}

	// Add small blobs — should not flush yet.
	receipt, err := bb.Add(ctx, []byte("small"), flush)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if receipt != nil {
		t.Fatal("expected no flush for small blob")
	}
	if bb.Len() != 1 {
		t.Fatalf("expected 1 queued blob, got %d", bb.Len())
	}

	// Manual flush.
	receipt, err = bb.Flush(ctx, flush)
	if err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if receipt == nil {
		t.Fatal("expected receipt from flush")
	}
	if bb.Len() != 0 {
		t.Fatalf("expected 0 queued blobs after flush, got %d", bb.Len())
	}
}

func TestMockClient_WaitTxResultTimeout(t *testing.T) {
	mock := NewMockClient()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Wait for a hash that doesn't exist — should timeout.
	_, err := mock.WaitTxResult(ctx, "nonexistent", 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestMockClient_ImplementsInterface(t *testing.T) {
	// Compile-time check is in mock_client.go, but verify at runtime too.
	var _ ExecutorClient = NewMockClient()
}
