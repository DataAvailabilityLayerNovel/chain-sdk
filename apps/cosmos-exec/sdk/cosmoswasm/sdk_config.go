package cosmoswasm

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// SDKConfig holds all configuration for the cosmoswasm SDK client.
// Every field has a sensible default — override only what you need.
type SDKConfig struct {
	// ExecURL is the base URL of the cosmos-exec-grpc service.
	// Required. No default — must be set explicitly.
	ExecURL string

	// Timeout is the HTTP client timeout for individual requests.
	// Default: 20s.
	Timeout time.Duration

	// RetryAttempts is the number of retry attempts for transient failures.
	// 0 means no retries. Default: 0.
	RetryAttempts int

	// RetryDelay is the delay between retry attempts.
	// Default: 1s.
	RetryDelay time.Duration

	// AuthToken, if set, is sent as "Authorization: Bearer <token>" on every request.
	AuthToken string

	// ChainID is used for building transactions that require a chain identifier.
	// Optional — only needed for chain-aware operations.
	ChainID string

	// HTTPClient allows injecting a custom http.Client (e.g. for TLS, proxies).
	// Default: a new http.Client with the configured Timeout.
	HTTPClient *http.Client
}

// DefaultSDKConfig returns an SDKConfig with sensible defaults.
// You must set ExecURL before using it.
func DefaultSDKConfig() SDKConfig {
	return SDKConfig{
		ExecURL:       "",
		Timeout:       20 * time.Second,
		RetryAttempts: 0,
		RetryDelay:    1 * time.Second,
	}
}

// Validate checks that required fields are set.
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
func NewClientFromConfig(cfg SDKConfig) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.Timeout}
	}

	return &Client{
		baseURL:    strings.TrimRight(strings.TrimSpace(cfg.ExecURL), "/"),
		httpClient: httpClient,
		authToken:  cfg.AuthToken,
		retryMax:   cfg.RetryAttempts,
		retryDelay: cfg.RetryDelay,
		chainID:    cfg.ChainID,
	}, nil
}
