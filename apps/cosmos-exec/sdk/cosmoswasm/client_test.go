package cosmoswasm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSubmitTxBytes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != txSubmitPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(SubmitTxResponse{Hash: "abc123"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	res, err := client.SubmitTxBytes(context.Background(), []byte("tx"))
	if err != nil {
		t.Fatalf("submit tx bytes: %v", err)
	}
	if res.Hash != "abc123" {
		t.Fatalf("unexpected hash: %s", res.Hash)
	}
}

func TestWaitTxResult(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != txResultPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		callCount++
		if callCount < 2 {
			_ = json.NewEncoder(w).Encode(GetTxResultResponse{Found: false})
			return
		}

		_ = json.NewEncoder(w).Encode(GetTxResultResponse{
			Found: true,
			Result: &TxExecutionResult{
				Hash: "txhash",
				Code: 0,
			},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := client.WaitTxResult(ctx, "txhash", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("wait tx result: %v", err)
	}
	if res.Hash != "txhash" {
		t.Fatalf("unexpected hash: %s", res.Hash)
	}
}

func TestQuerySmart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != querySmartPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(QuerySmartResponse{Data: map[string]any{"balance": "10"}})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	res, err := client.QuerySmart(context.Background(), "cosmos1contract", map[string]any{"balance": map[string]any{"address": "cosmos1addr"}})
	if err != nil {
		t.Fatalf("query smart: %v", err)
	}
	if res["balance"] != "10" {
		t.Fatalf("unexpected data: %#v", res)
	}
}
