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

func TestStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != statusPath {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(NodeStatus{LatestHeight: 12, FinalizedHeight: 8, Healthy: true})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	st, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.LatestHeight != 12 || st.FinalizedHeight != 8 || !st.Healthy {
		t.Fatalf("unexpected status: %#v", st)
	}
}

// statusTxServer: httptest server route cả /tx/result và /status để test
// GetTxFinality/WaitTxFinality. finalizedHeight là con trỏ để test poll có thể
// nâng dần giữa các lần gọi.
func statusTxServer(t *testing.T, txHeight uint64, latestHeight uint64, finalizedHeight *uint64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case txResultPath:
			_ = json.NewEncoder(w).Encode(GetTxResultResponse{
				Found:  true,
				Result: &TxExecutionResult{Hash: "txhash", Height: txHeight},
			})
		case statusPath:
			_ = json.NewEncoder(w).Encode(NodeStatus{LatestHeight: latestHeight, FinalizedHeight: *finalizedHeight})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
}

func TestGetTxFinality(t *testing.T) {
	t.Run("soft when above finalized", func(t *testing.T) {
		fin := uint64(8)
		server := statusTxServer(t, 10, 10, &fin) // tx ở block 10 > finalized 8
		defer server.Close()

		level, res, err := NewClient(server.URL).GetTxFinality(context.Background(), "txhash")
		if err != nil {
			t.Fatalf("get tx finality: %v", err)
		}
		if level != FinalitySoft {
			t.Fatalf("expected soft, got %s", level)
		}
		if res == nil || res.Height != 10 {
			t.Fatalf("unexpected result: %#v", res)
		}
	})

	t.Run("da when at or below finalized", func(t *testing.T) {
		fin := uint64(10)
		server := statusTxServer(t, 10, 12, &fin) // tx ở block 10 <= finalized 10
		defer server.Close()

		level, _, err := NewClient(server.URL).GetTxFinality(context.Background(), "txhash")
		if err != nil {
			t.Fatalf("get tx finality: %v", err)
		}
		if level != FinalityDA {
			t.Fatalf("expected da, got %s", level)
		}
	})

	t.Run("unknown when tx not found", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != txResultPath {
				t.Fatalf("status must not be queried when tx not found, got %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(GetTxResultResponse{Found: false})
		}))
		defer server.Close()

		level, res, err := NewClient(server.URL).GetTxFinality(context.Background(), "txhash")
		if err != nil {
			t.Fatalf("get tx finality: %v", err)
		}
		if level != FinalityUnknown || res != nil {
			t.Fatalf("expected unknown/nil, got %s / %#v", level, res)
		}
	})
}

func TestWaitTxFinality(t *testing.T) {
	fin := uint64(8) // ban đầu chưa final cho block 10...
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case txResultPath:
			_ = json.NewEncoder(w).Encode(GetTxResultResponse{
				Found:  true,
				Result: &TxExecutionResult{Hash: "txhash", Height: 10},
			})
		case statusPath:
			calls++
			if calls >= 2 { // lần poll thứ 2 thì DA đã bắt kịp block 10.
				fin = 10
			}
			_ = json.NewEncoder(w).Encode(NodeStatus{LatestHeight: 12, FinalizedHeight: fin})
		default:
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	res, err := NewClient(server.URL).WaitTxFinality(ctx, "txhash", FinalityDA, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("wait tx finality: %v", err)
	}
	if res == nil || res.Height != 10 {
		t.Fatalf("unexpected result: %#v", res)
	}
}
