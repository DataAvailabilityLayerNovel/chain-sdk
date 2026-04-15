package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	db "github.com/cometbft/cometbft-db"
	"github.com/cometbft/cometbft/libs/log"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/app"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/executor"
)

func newTestExecutor(t *testing.T) *executor.CosmosExecutor {
	t.Helper()
	return executor.New(app.New(log.NewNopLogger(), db.NewMemDB()))
}

func initExecutor(t *testing.T, exec *executor.CosmosExecutor) []byte {
	t.Helper()
	stateRoot, err := exec.InitChain(context.Background(), time.Now(), 1, "test-chain")
	if err != nil {
		t.Fatalf("init chain: %v", err)
	}
	return stateRoot
}

func TestHealthHandler(t *testing.T) {
	exec := newTestExecutor(t)
	handler := healthHandler(exec)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["status"] != "ok" {
		t.Fatalf("expected status ok, got %v", resp["status"])
	}
}

func TestReadyHandler_NotInitialized(t *testing.T) {
	exec := newTestExecutor(t)
	handler := readyHandler(exec)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

func TestReadyHandler_Initialized(t *testing.T) {
	exec := newTestExecutor(t)
	initExecutor(t, exec)

	handler := readyHandler(exec)

	req := httptest.NewRequest(http.MethodGet, "/ready", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestStatusHandler(t *testing.T) {
	exec := newTestExecutor(t)
	initExecutor(t, exec)

	handler := statusHandler(exec)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["initialized"] != true {
		t.Fatalf("expected initialized=true, got %v", resp["initialized"])
	}
	if resp["chain_id"] != "test-chain" {
		t.Fatalf("expected chain_id=test-chain, got %v", resp["chain_id"])
	}
}

func TestBlobSubmitAndRetrieve(t *testing.T) {
	exec := newTestExecutor(t)

	// Submit blob.
	data := []byte("hello blob world")
	body, _ := json.Marshal(blobSubmitRequest{DataBase64: base64.StdEncoding.EncodeToString(data)})
	req := httptest.NewRequest(http.MethodPost, "/blob/submit", bytes.NewReader(body))
	w := httptest.NewRecorder()
	blobSubmitHandler(exec)(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("submit: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var submitResp blobSubmitResponse
	json.Unmarshal(w.Body.Bytes(), &submitResp)
	if submitResp.Commitment == "" {
		t.Fatal("expected commitment in response")
	}

	// Retrieve blob.
	req2 := httptest.NewRequest(http.MethodGet, "/blob/retrieve?commitment="+submitResp.Commitment, nil)
	w2 := httptest.NewRecorder()
	blobRetrieveHandler(exec)(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("retrieve: expected 200, got %d: %s", w2.Code, w2.Body.String())
	}

	var retrieveResp blobRetrieveResponse
	json.Unmarshal(w2.Body.Bytes(), &retrieveResp)
	decoded, _ := base64.StdEncoding.DecodeString(retrieveResp.DataBase64)
	if string(decoded) != string(data) {
		t.Fatalf("expected %q, got %q", string(data), string(decoded))
	}
}

func TestTxPendingHandler(t *testing.T) {
	exec := newTestExecutor(t)
	initExecutor(t, exec)

	handler := txPendingHandler(exec)

	// Initially 0 pending.
	req := httptest.NewRequest(http.MethodGet, "/tx/pending", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["pending_count"] != float64(0) {
		t.Fatalf("expected 0 pending, got %v", resp["pending_count"])
	}

	// Inject a tx.
	exec.InjectTx(context.Background(), []byte("fake-tx"))

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/tx/pending", nil)
	handler(w2, req2)

	var resp2 map[string]any
	json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2["pending_count"] != float64(1) {
		t.Fatalf("expected 1 pending, got %v", resp2["pending_count"])
	}
}

func TestBlocksLatestHandler_NoBlocks(t *testing.T) {
	exec := newTestExecutor(t)

	handler := blocksLatestHandler(exec)
	req := httptest.NewRequest(http.MethodGet, "/blocks/latest", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["found"] != false {
		t.Fatalf("expected found=false when no blocks")
	}
}

func TestMethodNotAllowed(t *testing.T) {
	exec := newTestExecutor(t)

	handlers := map[string]http.HandlerFunc{
		"/health":        healthHandler(exec),
		"/ready":         readyHandler(exec),
		"/status":        statusHandler(exec),
		"/blocks/latest": blocksLatestHandler(exec),
		"/tx/pending":    txPendingHandler(exec),
	}

	for path, handler := range handlers {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		w := httptest.NewRecorder()
		handler(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405, got %d", path, w.Code)
		}
	}
}
