package cosmoswasm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type submitTxRequest struct {
	TxBase64 string `json:"tx_base64,omitempty"`
	TxHex    string `json:"tx_hex,omitempty"`
}

type querySmartRequest struct {
	Contract string          `json:"contract"`
	Msg      json.RawMessage `json:"msg"`
}

type apiError struct {
	Error string `json:"error"`
}

func NewClient(baseURL string) *Client {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		trimmed = DefaultExecAPIURL
	}

	return &Client{
		baseURL: trimmed,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (c *Client) WithHTTPClient(httpClient *http.Client) *Client {
	if httpClient == nil {
		return c
	}

	cloned := *c
	cloned.httpClient = httpClient
	return &cloned
}

func (c *Client) SubmitTxBase64(ctx context.Context, txBase64 string) (*SubmitTxResponse, error) {
	txBase64 = strings.TrimSpace(txBase64)
	if txBase64 == "" {
		return nil, errors.New("tx base64 is required")
	}

	res := SubmitTxResponse{}
	err := c.doJSON(
		ctx,
		http.MethodPost,
		txSubmitPath,
		submitTxRequest{TxBase64: txBase64},
		&res,
	)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(res.Hash) == "" {
		return nil, errors.New("submit response missing tx hash")
	}

	return &res, nil
}

func (c *Client) SubmitTxBytes(ctx context.Context, txBytes []byte) (*SubmitTxResponse, error) {
	if len(txBytes) == 0 {
		return nil, errors.New("tx bytes cannot be empty")
	}

	return c.SubmitTxBase64(ctx, base64.StdEncoding.EncodeToString(txBytes))
}

func (c *Client) GetTxResult(ctx context.Context, txHash string) (*GetTxResultResponse, error) {
	txHash = strings.TrimSpace(txHash)
	if txHash == "" {
		return nil, errors.New("tx hash is required")
	}

	query := url.Values{}
	query.Set("hash", txHash)

	res := GetTxResultResponse{}
	err := c.doJSON(ctx, http.MethodGet, txResultPath+"?"+query.Encode(), nil, &res)
	if err != nil {
		return nil, err
	}

	return &res, nil
}

func (c *Client) WaitTxResult(ctx context.Context, txHash string, pollInterval time.Duration) (*TxExecutionResult, error) {
	if pollInterval <= 0 {
		pollInterval = 1 * time.Second
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		res, err := c.GetTxResult(ctx, txHash)
		if err != nil {
			return nil, err
		}

		if res.Found {
			if res.Result == nil {
				return nil, errors.New("tx is found but result payload is empty")
			}
			return res.Result, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Client) QuerySmartRaw(ctx context.Context, contract string, msg any) (*QuerySmartResponse, error) {
	contract = strings.TrimSpace(contract)
	if contract == "" {
		return nil, errors.New("contract is required")
	}

	msgJSON, err := normalizeJSONMsg(msg)
	if err != nil {
		return nil, fmt.Errorf("invalid query msg: %w", err)
	}

	res := QuerySmartResponse{}
	err = c.doJSON(
		ctx,
		http.MethodPost,
		querySmartPath,
		querySmartRequest{Contract: contract, Msg: msgJSON},
		&res,
	)
	if err != nil {
		return nil, err
	}

	return &res, nil
}

func (c *Client) QuerySmart(ctx context.Context, contract string, msg any) (map[string]any, error) {
	res, err := c.QuerySmartRaw(ctx, contract, msg)
	if err != nil {
		return nil, err
	}

	if res.Data != nil {
		obj, ok := res.Data.(map[string]any)
		if ok {
			return obj, nil
		}

		return map[string]any{"value": res.Data}, nil
	}

	if strings.TrimSpace(res.DataRaw) == "" {
		return map[string]any{}, nil
	}

	decoded := map[string]any{}
	if err := json.Unmarshal([]byte(res.DataRaw), &decoded); err != nil {
		return map[string]any{"raw": res.DataRaw}, nil
	}

	return decoded, nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, reqBody any, out any) error {
	var payload []byte
	if reqBody != nil {
		var err error
		payload, err = json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
	}

	var lastErr error
	attempts := 1 + c.retryMax
	if attempts < 1 {
		attempts = 1
	}

	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(c.retryDelay):
			}
		}

		var body io.Reader
		if payload != nil {
			body = bytes.NewReader(payload)
		}

		lastErr = c.doJSONOnce(ctx, method, path, body, out)
		if lastErr == nil {
			return nil
		}

		// Only retry on transient errors (connection refused, timeout).
		msg := lastErr.Error()
		if !strings.Contains(msg, "connection refused") && !strings.Contains(msg, "deadline exceeded") {
			return lastErr
		}
	}

	return lastErr
}

func (c *Client) doJSONOnce(ctx context.Context, method, path string, body io.Reader, out any) error {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if c.authToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer response.Body.Close()

	respPayload, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if response.StatusCode >= http.StatusBadRequest {
		apiErr := apiError{}
		if err := json.Unmarshal(respPayload, &apiErr); err == nil && strings.TrimSpace(apiErr.Error) != "" {
			return fmt.Errorf("api error (%d): %s", response.StatusCode, apiErr.Error)
		}
		return fmt.Errorf("api error (%d): %s", response.StatusCode, strings.TrimSpace(string(respPayload)))
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(respPayload, out); err != nil {
		return fmt.Errorf("decode response json: %w", err)
	}

	return nil
}
