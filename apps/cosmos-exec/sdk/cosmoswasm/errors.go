// package cosmoswasm: định nghĩa hệ thống LỖI cấu trúc của SDK — sentinel
// errors + struct SDKError (op + cause + hint). Mục tiêu: lỗi vừa máy đọc
// được (errors.Is), vừa người đọc được (có gợi ý sửa).
package cosmoswasm

import (
	"errors"  // errors.New để tạo sentinel; errors.Is để so sánh.
	"fmt"     // bọc lỗi với context (%w).
	"strings" // strings.Builder + Contains để classify lỗi.
)

// Sentinel errors — callers can match with errors.Is().
//
// VI: "sentinel error" = biến lỗi cố định để so sánh. Dùng errors.Is(err, X)
// để check loại lỗi (không phụ thuộc message — message có thể đổi).
var (
	ErrNotReachable    = errors.New("executor not reachable")       // không kết nối được.
	ErrBlobTooLarge    = errors.New("blob exceeds max size")        // blob vượt giới hạn.
	ErrBlobStoreFull   = errors.New("blob store capacity exceeded") // store đầy.
	ErrTxFailed        = errors.New("transaction failed")           // tx bị reject.
	ErrContractMissing = errors.New("contract address required")    // thiếu địa chỉ contract.
	ErrCommitMissing   = errors.New("commitment required")          // thiếu commitment.
)

// SDKError wraps a root cause with human-readable context and a suggested
// action.  All SDK public methods return SDKError (or nil).
//
// VI: lỗi "giàu thông tin" của SDK — Op (đang làm gì), Cause (lỗi gốc),
// Hint (gợi ý sửa cho dev).
type SDKError struct {
	// Op is the SDK operation that failed (e.g. "SubmitBlob", "CommitRoot").
	Op string
	// Cause is the underlying error.
	Cause error
	// Hint is a one-line suggestion for the developer.
	Hint string
}

// Error: implement interface error (method bắt buộc cho mọi error type).
// VI: format dạng "Op: Cause\n  hint: Hint". strings.Builder hiệu quả
// hơn nối chuỗi bằng "+" khi có nhiều phần.
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

// Unwrap: implement interface để errors.Is/As "đi xuyên qua" SDKError tìm
// được lỗi gốc bên trong. Vd: errors.Is(sdkErr, ErrNotReachable) hoạt động.
func (e *SDKError) Unwrap() error { return e.Cause }

// sdkErr is the internal constructor.
// VI: helper riêng — nil cause → nil error (tránh trả SDKError "rỗng").
func sdkErr(op string, cause error, hint string) error {
	if cause == nil {
		return nil
	}
	return &SDKError{Op: op, Cause: cause, Hint: hint}
}

// classifyHTTPError inspects common network/api errors and returns an
// SDKError with a helpful hint.
//
// VI: nhận lỗi raw từ tầng HTTP, dò message để CHUẨN HOÁ thành SDKError có
// hint hữu ích. Dev đọc lỗi xong biết phải làm gì tiếp.
func classifyHTTPError(op string, err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()

	// switch không điều kiện: chạy nhánh đầu tiên có case đúng (giống if-else if).
	switch {
	case strings.Contains(msg, "connection refused"):
		// %w: bọc sentinel ErrNotReachable để errors.Is(err, ErrNotReachable) đúng.
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
		// Không khớp pattern nào → bọc nguyên, không kèm hint.
		return sdkErr(op, err, "")
	}
}
