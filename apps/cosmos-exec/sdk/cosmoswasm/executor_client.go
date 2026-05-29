// package cosmoswasm: file định nghĩa interface ExecutorClient — abstraction
// tầng transport, cho phép code app phụ thuộc INTERFACE (dễ mock) thay vì
// phụ thuộc *Client HTTP cụ thể.
package cosmoswasm

import (
	"context" // truyền tín hiệu huỷ/timeout xuyên các method.
	"time"    // time.Duration cho pollInterval.
)

// ExecutorClient is the transport-level interface for interacting with a
// cosmos-exec-grpc endpoint. It abstracts away HTTP/gRPC details so that:
//   - Production code uses Client (HTTP transport)
//   - Tests use a mock implementing this interface
//   - Future gRPC transport can implement the same interface
//
// VI: interface "hợp đồng" — giấu chi tiết HTTP/gRPC. Production dùng Client,
// test dùng Mock, tương lai có thể thêm transport khác cùng cài đặt interface
// này mà không phải sửa code app.
type ExecutorClient interface {
	// Tx operations
	// VI: nhóm method gửi/tra tx.
	SubmitTxBytes(ctx context.Context, txBytes []byte) (*SubmitTxResponse, error)
	SubmitTxBase64(ctx context.Context, txBase64 string) (*SubmitTxResponse, error)
	GetTxResult(ctx context.Context, txHash string) (*GetTxResultResponse, error)
	WaitTxResult(ctx context.Context, txHash string, pollInterval time.Duration) (*TxExecutionResult, error)

	// WASM query
	// VI: query smart contract — Raw trả response thô, không Raw trả map đã parse.
	QuerySmartRaw(ctx context.Context, contract string, msg any) (*QuerySmartResponse, error)
	QuerySmart(ctx context.Context, contract string, msg any) (map[string]any, error)
}

// Compile-time check that *Client implements ExecutorClient.
// VI: trick "interface satisfaction check" — gán nil-typed *Client vào biến
// kiểu ExecutorClient. Nếu *Client thiếu method, code KHÔNG biên dịch được
// (catch lỗi sớm, không phải runtime). `_` = bỏ tên biến.
var _ ExecutorClient = (*Client)(nil)
