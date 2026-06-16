// package cosmoswasm: file định nghĩa STRUCT Client + hằng số đường dẫn HTTP.
// (Tên file là doc.go nhưng thực tế chứa struct, không phải doc — doc thật
// nằm trong sdk.go.)
package cosmoswasm

import (
	"net/http" // http.Client để gọi REST.
	"time"     // time.Duration cho retry delay.
)

const (
	// DefaultExecAPIURL is the fallback URL used by NewClient when no URL is provided.
	// For production use, prefer NewClientFromConfig with an explicit ExecURL.
	// VI: URL mặc định khi NewClient("") — chỉ phục vụ dev local.
	DefaultExecAPIURL = "http://127.0.0.1:50051"
	// Các path REST mà Client gọi tới. Hằng đặt ở 1 chỗ → sửa 1 lần, áp dụng
	// mọi nơi (tránh "magic string" rải rác).
	txSubmitPath   = "/tx/submit"
	txResultPath   = "/tx/result"
	statusPath     = "/status"
	querySmartPath = "/wasm/query-smart"
)

// Client wraps the public HTTP endpoints exposed by cosmos-exec-grpc:
//   - POST /tx/submit
//   - GET  /tx/result
//   - GET  /status            (node finality: latest vs finalized height)
//   - POST /wasm/query-smart
//
// Blob-first data (large off-chain data on Celestia DA) is handled by the
// separate [BlobClient], which talks to a Celestia bridge directly — not
// through cosmos-exec-grpc.
//
// Create via NewClient(url) for quick use, or NewClientFromConfig(SDKConfig{})
// for full control over auth, retry, timeouts.
//
// VI: struct chính của SDK — bọc các REST endpoint của cosmos-exec-grpc.
// Field CHỮ THƯỜNG → không export (chỉ method trong package này truy cập).
// Dùng NewClient(url) cho dev, NewClientFromConfig(cfg) cho production.
type Client struct {
	baseURL    string        // URL gốc (đã trim "/" cuối).
	httpClient *http.Client  // HTTP client thật — có thể inject để test.
	authToken  string        // bearer token (rỗng = không gửi header Auth).
	retryMax   int           // số lần retry khi lỗi transient.
	retryDelay time.Duration // chờ giữa các retry.
	chainID    string        // chain id (dùng khi ký tx).
}
