package cosmoswasm

import (
	"context"
	"time"
)

// ExecutorClient is the transport-level interface for interacting with a
// cosmos-exec-grpc endpoint. It abstracts away HTTP/gRPC details so that:
//   - Production code uses Client (HTTP transport)
//   - Tests use a mock implementing this interface
//   - Future gRPC transport can implement the same interface
type ExecutorClient interface {
	// Tx operations
	SubmitTxBytes(ctx context.Context, txBytes []byte) (*SubmitTxResponse, error)
	SubmitTxBase64(ctx context.Context, txBase64 string) (*SubmitTxResponse, error)
	GetTxResult(ctx context.Context, txHash string) (*GetTxResultResponse, error)
	WaitTxResult(ctx context.Context, txHash string, pollInterval time.Duration) (*TxExecutionResult, error)

	// WASM query
	QuerySmartRaw(ctx context.Context, contract string, msg any) (*QuerySmartResponse, error)
	QuerySmart(ctx context.Context, contract string, msg any) (map[string]any, error)
}

// Compile-time check that *Client implements ExecutorClient.
var _ ExecutorClient = (*Client)(nil)
