package cosmoswasm

import (
	"context"
	"time"
)

// ExecutorClient is the transport-level interface for interacting with a
// cosmos-exec-grpc endpoint. It abstracts away HTTP/gRPC details so that:
//   - Production code uses Client (HTTP transport)
//   - Tests use MockExecutorClient (no network)
//   - Future gRPC transport can implement the same interface
//
// All SDK high-level functions (CommitRoot, BatchBuilder, etc.) should accept
// this interface rather than *Client directly.
type ExecutorClient interface {
	// Tx operations
	SubmitTxBytes(ctx context.Context, txBytes []byte) (*SubmitTxResponse, error)
	SubmitTxBase64(ctx context.Context, txBase64 string) (*SubmitTxResponse, error)
	GetTxResult(ctx context.Context, txHash string) (*GetTxResultResponse, error)
	WaitTxResult(ctx context.Context, txHash string, pollInterval time.Duration) (*TxExecutionResult, error)

	// Blob operations
	SubmitBlob(ctx context.Context, data []byte) (*BlobSubmitResponse, error)
	RetrieveBlob(ctx context.Context, commitment string) (*BlobRetrieveResponse, error)
	RetrieveBlobData(ctx context.Context, commitment string) ([]byte, error)
	SubmitBatch(ctx context.Context, blobs [][]byte) (*BlobBatchResponse, error)

	// WASM query
	QuerySmartRaw(ctx context.Context, contract string, msg any) (*QuerySmartResponse, error)
	QuerySmart(ctx context.Context, contract string, msg any) (map[string]any, error)

	// High-level commit
	CommitRoot(ctx context.Context, req CommitRootRequest) (*CommitReceipt, error)
}

// Compile-time check that *Client implements ExecutorClient.
var _ ExecutorClient = (*Client)(nil)
