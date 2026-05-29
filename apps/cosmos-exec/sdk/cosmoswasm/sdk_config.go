// package cosmoswasm: file định nghĩa SDKConfig — cấu hình tập trung cho
// Client (URL chain, timeout, retry, auth, chainID...). Production nên dùng
// NewClientFromConfig thay vì NewClient (chỉ phù hợp dev nhanh).
package cosmoswasm

import (
	"fmt"      // tạo lỗi validate.
	"net/http" // http.Client cho cấu hình HTTP (TLS, proxy...).
	"strings"  // TrimSpace để xử lý input.
	"time"     // time.Duration cho timeout/retry.
)

// SDKConfig holds all configuration for the cosmoswasm SDK client.
// Every field has a sensible default — override only what you need.
//
// VI: struct cấu hình. Mọi field có default hợp lý — caller chỉ cần override
// cái cần. ExecURL là BẮT BUỘC (không có default vì phụ thuộc môi trường).
type SDKConfig struct {
	// ExecURL is the base URL of the cosmos-exec-grpc service.
	// Required. No default — must be set explicitly.
	// VI: URL gốc của cosmos-exec-grpc (vd "http://localhost:36657").
	ExecURL string

	// Timeout is the HTTP client timeout for individual requests.
	// Default: 20s.
	Timeout time.Duration

	// RetryAttempts is the number of retry attempts for transient failures.
	// 0 means no retries. Default: 0.
	// VI: số lần RETRY khi lỗi tạm thời (connection refused / timeout).
	// 0 = không retry (fail-fast). Production nên đặt 2-3.
	RetryAttempts int

	// RetryDelay is the delay between retry attempts.
	// Default: 1s.
	RetryDelay time.Duration

	// AuthToken, if set, is sent as "Authorization: Bearer <token>" on every request.
	// VI: bearer token — server cosmos-exec-grpc kiểm tra trong middleware.
	// Trống → request đi không có header Authorization.
	AuthToken string

	// ChainID is used for building transactions that require a chain identifier.
	// Optional — only needed for chain-aware operations.
	// VI: chain id — bắt buộc khi ký tx, không cần khi chỉ query.
	ChainID string

	// HTTPClient allows injecting a custom http.Client (e.g. for TLS, proxies).
	// Default: a new http.Client with the configured Timeout.
	// VI: cho phép inject http.Client tuỳ chỉnh (vd: cấu hình TLS, proxy).
	// nil → SDK tự tạo http.Client mặc định với Timeout đã cấu hình.
	HTTPClient *http.Client
}

// DefaultSDKConfig returns an SDKConfig with sensible defaults.
// You must set ExecURL before using it.
//
// VI: trả config mặc định. Vẫn phải set ExecURL trước khi NewClientFromConfig.
func DefaultSDKConfig() SDKConfig {
	return SDKConfig{
		ExecURL:       "",                // bắt buộc tự điền.
		Timeout:       20 * time.Second,  // đủ dài cho upload WASM.
		RetryAttempts: 0,                 // dev: không retry để fail-fast.
		RetryDelay:    1 * time.Second,
	}
}

// Validate checks that required fields are set.
//
// VI: kiểm tra config & ÁP default cho field <= 0. Receiver là CON TRỎ
// (*SDKConfig) vì có sửa Timeout/RetryDelay khi <= 0.
func (c *SDKConfig) Validate() error {
	if strings.TrimSpace(c.ExecURL) == "" {
		return fmt.Errorf("ExecURL is required")
	}
	if c.Timeout <= 0 {
		c.Timeout = 20 * time.Second
	}
	if c.RetryDelay <= 0 {
		c.RetryDelay = 1 * time.Second
	}
	return nil
}

// NewClientFromConfig creates a Client from an SDKConfig.
// This is the recommended way to create a Client for production use.
//
// VI: KHUYẾN NGHỊ cho production. Validate config → khởi tạo http.Client →
// gắn mọi field vào *Client (kể cả retry, auth, chainID).
func NewClientFromConfig(cfg SDKConfig) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.Timeout}
	}

	return &Client{
		baseURL:    strings.TrimRight(strings.TrimSpace(cfg.ExecURL), "/"), // bỏ "/" cuối.
		httpClient: httpClient,
		authToken:  cfg.AuthToken,
		retryMax:   cfg.RetryAttempts,
		retryDelay: cfg.RetryDelay,
		chainID:    cfg.ChainID,
	}, nil
}
