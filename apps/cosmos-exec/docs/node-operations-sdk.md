# Node Operations qua SDK (không dùng CLI)

Tài liệu này hướng dẫn gọi các **node operation** của `cosmos-exec-grpc` (rollback, prune, status, health, height, faucet, account, balance, mempool…) bằng Go SDK thay vì gõ `curl` hoặc CLI.

> SDK `cosmoswasm` (`apps/cosmos-exec/sdk/cosmoswasm`) hiện cung cấp Tier-1 cho tx & query. Các endpoint **ops** (`/exec/*`, `/status`, `/health`, `/faucet`…) chưa có wrapper sẵn — bạn dùng cùng `http.Client` + auth header của SDK để gọi REST.

Toàn bộ code dưới đây được thiết kế chạy độc lập, copy là chạy được.

---

## 1. Cấu trúc

Tạo helper `NodeOps` bọc một `*http.Client` chia sẻ cùng config của SDK (timeout, auth token, retry). Mỗi method = 1 endpoint REST.

```
apps/cosmos-exec/examples/node-ops-sdk/
├── go.mod
└── main.go        # toàn bộ code
```

---

## 2. Code đầy đủ (`main.go`)

```go
// Package main: ví dụ độc lập gọi tất cả node operation của cosmos-exec-grpc
// thông qua một HTTP client tái sử dụng config của SDK (auth/timeout).
//
// Build:   go build -o node-ops .
// Run:     EXEC_URL=http://127.0.0.1:50051 ./node-ops status
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// ── NodeOps: wrapper gọi các endpoint vận hành ──────────────────────────────

type NodeOps struct {
	baseURL    string
	httpClient *http.Client
	authToken  string // "Bearer <token>" cho POST khi server bật auth
}

// NewNodeOps khởi tạo từ ENV/flags. Trả lỗi nếu thiếu URL.
func NewNodeOps(baseURL, authToken string, timeout time.Duration) (*NodeOps, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil, errors.New("EXEC_URL is required")
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &NodeOps{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: timeout},
		authToken:  strings.TrimSpace(authToken),
	}, nil
}

// do gọi 1 endpoint JSON. method = GET/POST. reqBody nil = không gửi body.
// out nil = bỏ qua response body.
func (n *NodeOps) do(ctx context.Context, method, path string, reqBody, out any) error {
	var body io.Reader
	if reqBody != nil {
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, n.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Auth bắt buộc cho POST khi server bật COSMOS_EXEC_AUTH_TOKEN.
	if n.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+n.authToken)
	}

	resp, err := n.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		// Server trả {"error": "..."} cho mọi 4xx/5xx.
		var apiErr struct{ Error string `json:"error"` }
		if err := json.Unmarshal(respBytes, &apiErr); err == nil && apiErr.Error != "" {
			return fmt.Errorf("api %d: %s", resp.StatusCode, apiErr.Error)
		}
		return fmt.Errorf("api %d: %s", resp.StatusCode, strings.TrimSpace(string(respBytes)))
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBytes, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// ── Response types (chỉ field thường dùng) ──────────────────────────────────

type StatusResponse struct {
	ChainID         string `json:"chain_id"`
	LatestHeight    uint64 `json:"latest_height"`
	FinalizedHeight uint64 `json:"finalized_height"`
	Initialized     bool   `json:"initialized"`
}

type HealthResponse struct {
	Status     string `json:"status"`
	TxCount    uint64 `json:"tx_count"`
	BlockCount uint64 `json:"block_count"`
}

type ReadyResponse struct {
	Ready           bool   `json:"ready"`
	Reason          string `json:"reason,omitempty"`
	LatestHeight    uint64 `json:"latest_height,omitempty"`
	FinalizedHeight uint64 `json:"finalized_height,omitempty"`
}

type HeightResponse struct {
	Height uint64 `json:"height"`
}

type AccountResponse struct {
	Address       string `json:"address"`
	AccountNumber uint64 `json:"account_number"`
	Sequence      uint64 `json:"sequence"`
	Exists        bool   `json:"exists"`
}

type BalanceCoin struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

type BalanceResponse struct {
	Address  string        `json:"address"`
	Amount   string        `json:"amount,omitempty"`
	Balances []BalanceCoin `json:"balances,omitempty"`
}

type PendingResponse struct {
	PendingCount uint64 `json:"pending_count"`
}

type FaucetResponse struct {
	TxHash    string `json:"tx_hash"`
	Recipient string `json:"recipient"`
	Amount    string `json:"amount"`
	Treasury  string `json:"treasury"`
}

// ── Operation methods ───────────────────────────────────────────────────────

// Status — GET /status: chain id, height, finalized.
func (n *NodeOps) Status(ctx context.Context) (*StatusResponse, error) {
	out := &StatusResponse{}
	return out, n.do(ctx, http.MethodGet, "/status", nil, out)
}

// Health — GET /health: tx/block count.
func (n *NodeOps) Health(ctx context.Context) (*HealthResponse, error) {
	out := &HealthResponse{}
	return out, n.do(ctx, http.MethodGet, "/health", nil, out)
}

// Ready — GET /ready: trả 503 nếu chưa init xong.
func (n *NodeOps) Ready(ctx context.Context) (*ReadyResponse, error) {
	out := &ReadyResponse{}
	// Lưu ý: /ready trả 503 khi chưa sẵn sàng → do() coi là lỗi.
	// Tự handle 503 ở đây.
	err := n.do(ctx, http.MethodGet, "/ready", nil, out)
	if err != nil && strings.Contains(err.Error(), "503") {
		// Vẫn cố parse vì server có body JSON kèm reason.
		return out, nil
	}
	return out, err
}

// Height — GET /exec/height: block height hiện tại theo executor.
func (n *NodeOps) Height(ctx context.Context) (uint64, error) {
	out := &HeightResponse{}
	if err := n.do(ctx, http.MethodGet, "/exec/height", nil, out); err != nil {
		return 0, err
	}
	return out.Height, nil
}

// Rollback — POST /exec/rollback: lùi state về targetHeight.
// CẢNH BÁO: phá huỷ — cần auth token (nếu profile prod) và đảm bảo không có
// downstream nào đang theo dõi.
func (n *NodeOps) Rollback(ctx context.Context, targetHeight uint64) error {
	if targetHeight == 0 {
		return errors.New("target_height must be > 0")
	}
	body := map[string]uint64{"target_height": targetHeight}
	return n.do(ctx, http.MethodPost, "/exec/rollback", body, nil)
}

// Prune — POST /exec/prune: xoá tx/block bộ nhớ ở height <= h để tiết kiệm RAM.
func (n *NodeOps) Prune(ctx context.Context, height uint64) error {
	body := map[string]uint64{"height": height}
	return n.do(ctx, http.MethodPost, "/exec/prune", body, nil)
}

// PendingTxCount — GET /tx/pending: số tx đang chờ mempool.
func (n *NodeOps) PendingTxCount(ctx context.Context) (uint64, error) {
	out := &PendingResponse{}
	if err := n.do(ctx, http.MethodGet, "/tx/pending", nil, out); err != nil {
		return 0, err
	}
	return out.PendingCount, nil
}

// Account — GET /auth/account/{addr}: account_number + sequence để ký tx.
func (n *NodeOps) Account(ctx context.Context, bech32 string) (*AccountResponse, error) {
	bech32 = strings.TrimSpace(bech32)
	if bech32 == "" {
		return nil, errors.New("address is required")
	}
	out := &AccountResponse{}
	return out, n.do(ctx, http.MethodGet, "/auth/account/"+bech32, nil, out)
}

// Balance — GET /bank/balance/{addr}?denom=<>
// denom="" → trả mọi denom; ngược lại tách riêng số dư denom đó.
func (n *NodeOps) Balance(ctx context.Context, bech32, denom string) (*BalanceResponse, error) {
	path := "/bank/balance/" + bech32
	if denom != "" {
		path += "?denom=" + denom
	}
	out := &BalanceResponse{}
	return out, n.do(ctx, http.MethodGet, path, nil, out)
}

// LatestBlock — GET /blocks/latest. Trả map raw để giữ nguyên mọi field.
func (n *NodeOps) LatestBlock(ctx context.Context) (map[string]any, error) {
	out := map[string]any{}
	return out, n.do(ctx, http.MethodGet, "/blocks/latest", nil, &out)
}

// BlockByHeight — GET /blocks/{height}.
func (n *NodeOps) BlockByHeight(ctx context.Context, h uint64) (map[string]any, error) {
	out := map[string]any{}
	path := fmt.Sprintf("/blocks/%d", h)
	return out, n.do(ctx, http.MethodGet, path, nil, &out)
}

// Faucet — POST /faucet?addr=cosmos1...
// Chỉ hoạt động khi server đã bật COSMOS_EXEC_TREASURY_PRIVKEY_HEX.
func (n *NodeOps) Faucet(ctx context.Context, recipient string) (*FaucetResponse, error) {
	recipient = strings.TrimSpace(recipient)
	if recipient == "" {
		return nil, errors.New("recipient is required")
	}
	out := &FaucetResponse{}
	return out, n.do(ctx, http.MethodPost, "/faucet?addr="+recipient, nil, out)
}

// ── CLI entry point ─────────────────────────────────────────────────────────

func main() {
	url := flag.String("url", envOr("EXEC_URL", "http://127.0.0.1:50051"), "cosmos-exec-grpc base URL")
	token := flag.String("token", os.Getenv("EXEC_AUTH_TOKEN"), "Bearer token (POST chỉ cần khi server bật auth)")
	timeout := flag.Duration("timeout", 20*time.Second, "HTTP timeout")
	flag.Parse()

	ops, err := NewNodeOps(*url, *token, *timeout)
	if err != nil {
		die(err)
	}

	args := flag.Args()
	if len(args) == 0 {
		fmt.Println("Usage: node-ops <command> [args...]")
		fmt.Println("Commands:")
		fmt.Println("  status                 GET /status")
		fmt.Println("  health                 GET /health")
		fmt.Println("  ready                  GET /ready")
		fmt.Println("  height                 GET /exec/height")
		fmt.Println("  rollback <height>      POST /exec/rollback")
		fmt.Println("  prune <height>         POST /exec/prune")
		fmt.Println("  pending                GET /tx/pending")
		fmt.Println("  account <bech32>       GET /auth/account/{addr}")
		fmt.Println("  balance <bech32> [denom]  GET /bank/balance/{addr}")
		fmt.Println("  block latest           GET /blocks/latest")
		fmt.Println("  block <height>         GET /blocks/{height}")
		fmt.Println("  faucet <bech32>        POST /faucet?addr=...")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	switch args[0] {
	case "status":
		out, err := ops.Status(ctx)
		printOrDie(out, err)
	case "health":
		printOrDie(ops.Health(ctx))
	case "ready":
		printOrDie(ops.Ready(ctx))
	case "height":
		h, err := ops.Height(ctx)
		printOrDie(map[string]uint64{"height": h}, err)
	case "rollback":
		mustArg(args, 2)
		h := parseUint(args[1])
		printOrDie("rollback ok", ops.Rollback(ctx, h))
	case "prune":
		mustArg(args, 2)
		h := parseUint(args[1])
		printOrDie("prune ok", ops.Prune(ctx, h))
	case "pending":
		c, err := ops.PendingTxCount(ctx)
		printOrDie(map[string]uint64{"pending_count": c}, err)
	case "account":
		mustArg(args, 2)
		printOrDie(ops.Account(ctx, args[1]))
	case "balance":
		mustArg(args, 2)
		denom := ""
		if len(args) >= 3 {
			denom = args[2]
		}
		printOrDie(ops.Balance(ctx, args[1], denom))
	case "block":
		mustArg(args, 2)
		if args[1] == "latest" {
			printOrDie(ops.LatestBlock(ctx))
			return
		}
		printOrDie(ops.BlockByHeight(ctx, parseUint(args[1])))
	case "faucet":
		mustArg(args, 2)
		printOrDie(ops.Faucet(ctx, args[1]))
	default:
		die(fmt.Errorf("unknown command: %s", args[0]))
	}
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func parseUint(s string) uint64 {
	var n uint64
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		die(fmt.Errorf("invalid uint %q: %v", s, err))
	}
	return n
}

func mustArg(args []string, want int) {
	if len(args) < want {
		die(fmt.Errorf("missing argument; got %d, want %d", len(args), want))
	}
}

func printOrDie(v any, err error) {
	if err != nil {
		die(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
```

---

## 3. `go.mod`

```mod
module example.com/node-ops-sdk

go 1.25
```

Không cần thêm dependency — file dùng thuần `net/http` + `encoding/json`. Nếu bạn muốn dùng chính SDK `cosmoswasm` (cho tx/query) thì thêm:

```mod
require github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec v0.0.0
```

và đặt `replace` về path local nếu chưa publish.

---

## 4. Chạy thử

```bash
cd apps/cosmos-exec/examples/node-ops-sdk
go build -o node-ops .

# Đọc trạng thái
EXEC_URL=http://127.0.0.1:50051 ./node-ops status

# Số dư
./node-ops balance cosmos1abc... ustake

# Faucet (server phải bật COSMOS_EXEC_TREASURY_PRIVKEY_HEX)
./node-ops faucet cosmos1abc...

# Ops nguy hiểm — cần token nếu profile prod
EXEC_AUTH_TOKEN=$TOKEN ./node-ops rollback 100
EXEC_AUTH_TOKEN=$TOKEN ./node-ops prune 50
```

---

## 5. Tích hợp vào code production

Trong app thật, đừng dùng phần CLI — chỉ giữ struct `NodeOps`:

```go
package main

import (
	"context"
	"log"
	"time"

	cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
)

func main() {
	// SDK chính cho tx + query.
	client, err := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
		ExecURL:       "https://exec.mychain.io",
		Timeout:       30 * time.Second,
		RetryAttempts: 3,
		AuthToken:     "secret",
		ChainID:       "my-chain-1",
	})
	if err != nil {
		log.Fatal(err)
	}

	// NodeOps song song để gọi /exec/*, /status, /health…
	ops, err := NewNodeOps("https://exec.mychain.io", "secret", 30*time.Second)
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	// Kiểm tra ready trước khi submit tx.
	r, err := ops.Ready(ctx)
	if err != nil || !r.Ready {
		log.Fatalf("not ready: %v %+v", err, r)
	}

	// ... gọi client.SubmitTxBytes(...) v.v.
	_ = client
}
```

---

## 6. Map endpoint → method

| HTTP                             | NodeOps method      | Cần auth (prod)? |
| -------------------------------- | ------------------- | ---------------- |
| `GET  /status`                   | `Status()`          | Không            |
| `GET  /health`, `/healthz`       | `Health()`          | Không            |
| `GET  /ready`                    | `Ready()`           | Không            |
| `GET  /exec/height`              | `Height()`          | Không            |
| `POST /exec/rollback`            | `Rollback(h)`       | **Có**           |
| `POST /exec/prune`               | `Prune(h)`          | **Có**           |
| `GET  /tx/pending`               | `PendingTxCount()`  | Không            |
| `GET  /auth/account/{addr}`      | `Account(addr)`     | Không            |
| `GET  /bank/balance/{addr}`      | `Balance(addr, "")` | Không            |
| `GET  /blocks/latest`            | `LatestBlock()`     | Không            |
| `GET  /blocks/{h}`               | `BlockByHeight(h)`  | Không            |
| `POST /faucet?addr=...`          | `Faucet(addr)`      | **Có**           |

---

## 7. Best practices

- **Tách context timeout**: ops nhanh (`/status`) dùng 5s; rollback dài hơn (30s+).
- **Retry với backoff** cho transient: chỉ retry GET. Không retry rollback/prune (idempotency yếu).
- **Log token cẩn thận**: đừng log full Authorization header.
- **Pre-flight check**: gọi `Ready()` trước khi submit hàng loạt tx — tránh đập server lúc nó chưa replay xong file persist.
- **Quan sát `pending_count`**: tăng nhanh = mempool tắc → giảm tốc submit phía client.

Đối ứng endpoint REST đầy đủ xem [main.go](../cmd/cosmos-exec-grpc/main.go) (handlers), middleware xem [middleware.go](../cmd/cosmos-exec-grpc/middleware.go), faucet xem [faucet.go](../cmd/cosmos-exec-grpc/faucet.go).
