package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/evstack/ev-node/core/execution"
)

// EnhancedExecutorClient implements execution.Executor plus the optional
// interfaces (HeightProvider, Rollbackable, ExecPruner) by talking to the
// cosmos-exec-grpc HTTP service directly.
//
// Core Executor methods use the Connect-RPC JSON protocol (POST with JSON body
// to /evnode.v1.ExecutorService/<Method>). Optional interfaces use custom
// HTTP endpoints (/exec/height, /exec/rollback, /exec/prune).
type EnhancedExecutorClient struct {
	baseURL    string
	httpClient *http.Client
}

// Compile-time interface checks.
var (
	_ execution.Executor       = (*EnhancedExecutorClient)(nil)
	_ execution.HeightProvider = (*EnhancedExecutorClient)(nil)
	_ execution.Rollbackable   = (*EnhancedExecutorClient)(nil)
	_ execution.ExecPruner     = (*EnhancedExecutorClient)(nil)
)

// NewEnhancedExecutorClient creates an executor client that communicates with
// the cosmos-exec-grpc service at the given URL.
func NewEnhancedExecutorClient(url string) *EnhancedExecutorClient {
	return &EnhancedExecutorClient{
		baseURL: strings.TrimRight(url, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// ── Connect-RPC helper ─────────────────────────────────────────────────────

// connectCall makes a Connect-RPC unary call using the JSON content type.
// Connect-RPC JSON: POST /<service>/<method>, Content-Type: application/json.
func (c *EnhancedExecutorClient) connectCall(ctx context.Context, method string, reqBody, respBody any) error {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + "/evnode.v1.ExecutorService/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: read response: %w", method, err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: status %d: %s", method, resp.StatusCode, string(body))
	}

	if respBody != nil {
		if err := json.Unmarshal(body, respBody); err != nil {
			return fmt.Errorf("%s: decode response: %w", method, err)
		}
	}
	return nil
}

// httpCall makes a plain HTTP call (for custom endpoints).
func (c *EnhancedExecutorClient) httpCall(ctx context.Context, httpMethod, path string, reqBody, respBody any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, httpMethod, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: read response: %w", path, err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: status %d: %s", path, resp.StatusCode, string(body))
	}

	if respBody != nil {
		if err := json.Unmarshal(body, respBody); err != nil {
			return fmt.Errorf("%s: decode response: %w", path, err)
		}
	}
	return nil
}

// ── Core Executor interface (Connect-RPC JSON) ────────────────────────────

func (c *EnhancedExecutorClient) InitChain(ctx context.Context, genesisTime time.Time, initialHeight uint64, chainID string) ([]byte, error) {
	var resp struct {
		StateRoot []byte `json:"stateRoot"`
	}
	err := c.connectCall(ctx, "InitChain", map[string]any{
		"genesisTime":   genesisTime.Format(time.RFC3339Nano),
		"initialHeight": fmt.Sprintf("%d", initialHeight),
		"chainId":       chainID,
	}, &resp)
	if err != nil {
		return nil, err
	}
	return resp.StateRoot, nil
}

func (c *EnhancedExecutorClient) GetTxs(ctx context.Context) ([][]byte, error) {
	var resp struct {
		Txs [][]byte `json:"txs"`
	}
	err := c.connectCall(ctx, "GetTxs", map[string]any{}, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Txs, nil
}

func (c *EnhancedExecutorClient) ExecuteTxs(ctx context.Context, txs [][]byte, blockHeight uint64, timestamp time.Time, prevStateRoot []byte) ([]byte, error) {
	var resp struct {
		UpdatedStateRoot []byte `json:"updatedStateRoot"`
	}
	err := c.connectCall(ctx, "ExecuteTxs", map[string]any{
		"txs":           txs,
		"blockHeight":   fmt.Sprintf("%d", blockHeight),
		"timestamp":     timestamp.Format(time.RFC3339Nano),
		"prevStateRoot": prevStateRoot,
	}, &resp)
	if err != nil {
		return nil, err
	}
	return resp.UpdatedStateRoot, nil
}

func (c *EnhancedExecutorClient) SetFinal(ctx context.Context, blockHeight uint64) error {
	return c.connectCall(ctx, "SetFinal", map[string]any{
		"blockHeight": fmt.Sprintf("%d", blockHeight),
	}, nil)
}

func (c *EnhancedExecutorClient) GetExecutionInfo(ctx context.Context) (execution.ExecutionInfo, error) {
	var resp struct {
		MaxGas uint64 `json:"maxGas,string"`
	}
	err := c.connectCall(ctx, "GetExecutionInfo", map[string]any{}, &resp)
	if err != nil {
		return execution.ExecutionInfo{}, err
	}
	return execution.ExecutionInfo{MaxGas: resp.MaxGas}, nil
}

func (c *EnhancedExecutorClient) FilterTxs(ctx context.Context, txs [][]byte, maxBytes, maxGas uint64, hasForceIncludedTransaction bool) ([]execution.FilterStatus, error) {
	var resp struct {
		Statuses []int `json:"statuses"`
	}
	err := c.connectCall(ctx, "FilterTxs", map[string]any{
		"txs":                         txs,
		"maxBytes":                    fmt.Sprintf("%d", maxBytes),
		"maxGas":                      fmt.Sprintf("%d", maxGas),
		"hasForceIncludedTransaction": hasForceIncludedTransaction,
	}, &resp)
	if err != nil {
		return nil, err
	}
	result := make([]execution.FilterStatus, len(resp.Statuses))
	for i, s := range resp.Statuses {
		result[i] = execution.FilterStatus(s)
	}
	return result, nil
}

// ── Optional interfaces (custom HTTP endpoints) ───────────────────────────

// GetLatestHeight returns the current block height from the execution layer.
// Implements execution.HeightProvider.
func (c *EnhancedExecutorClient) GetLatestHeight(ctx context.Context) (uint64, error) {
	var resp struct {
		Height uint64 `json:"height"`
	}
	if err := c.httpCall(ctx, http.MethodGet, "/exec/height", nil, &resp); err != nil {
		return 0, err
	}
	return resp.Height, nil
}

// Rollback resets the execution layer head to the specified height.
// Implements execution.Rollbackable.
func (c *EnhancedExecutorClient) Rollback(ctx context.Context, targetHeight uint64) error {
	return c.httpCall(ctx, http.MethodPost, "/exec/rollback", map[string]any{
		"target_height": targetHeight,
	}, nil)
}

// PruneExec removes execution metadata up to and including the given height.
// Implements execution.ExecPruner.
func (c *EnhancedExecutorClient) PruneExec(ctx context.Context, height uint64) error {
	return c.httpCall(ctx, http.MethodPost, "/exec/prune", map[string]any{
		"height": height,
	}, nil)
}
