package cosmoswasm

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors — callers can match with errors.Is().
var (
	ErrNotReachable    = errors.New("executor not reachable")
	ErrBlobTooLarge    = errors.New("blob exceeds max size")
	ErrBlobStoreFull   = errors.New("blob store capacity exceeded")
	ErrTxFailed        = errors.New("transaction failed")
	ErrContractMissing = errors.New("contract address required")
	ErrCommitMissing   = errors.New("commitment required")
)

// SDKError wraps a root cause with human-readable context and a suggested
// action.  All SDK public methods return SDKError (or nil).
type SDKError struct {
	// Op is the SDK operation that failed (e.g. "SubmitBlob", "CommitRoot").
	Op string
	// Cause is the underlying error.
	Cause error
	// Hint is a one-line suggestion for the developer.
	Hint string
}

func (e *SDKError) Error() string {
	var b strings.Builder
	b.WriteString(e.Op)
	b.WriteString(": ")
	b.WriteString(e.Cause.Error())
	if e.Hint != "" {
		b.WriteString("\n  hint: ")
		b.WriteString(e.Hint)
	}
	return b.String()
}

func (e *SDKError) Unwrap() error { return e.Cause }

// sdkErr is the internal constructor.
func sdkErr(op string, cause error, hint string) error {
	if cause == nil {
		return nil
	}
	return &SDKError{Op: op, Cause: cause, Hint: hint}
}

// classifyHTTPError inspects common network/api errors and returns an
// SDKError with a helpful hint.
func classifyHTTPError(op string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()

	switch {
	case strings.Contains(msg, "connection refused"):
		return sdkErr(op, fmt.Errorf("%w: %s", ErrNotReachable, msg),
			"is cosmos-exec-grpc running? start it with: go run ./apps/cosmos-exec/cmd/cosmos-exec-grpc --in-memory")

	case strings.Contains(msg, "context deadline exceeded"):
		return sdkErr(op, err,
			"request timed out — the executor may be overloaded or the network is slow")

	case strings.Contains(msg, "blob size") && strings.Contains(msg, "exceeds max"):
		return sdkErr(op, fmt.Errorf("%w: %s", ErrBlobTooLarge, msg),
			"compress the data first (enabled by default in BatchBuilder) or split with ChunkBlob()")

	case strings.Contains(msg, "store full"):
		return sdkErr(op, fmt.Errorf("%w: %s", ErrBlobStoreFull, msg),
			"the in-memory blob store is at capacity — restart the executor or reduce batch frequency")

	default:
		return sdkErr(op, err, "")
	}
}
