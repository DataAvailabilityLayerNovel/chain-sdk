# Sovereign CosmWasm Rollup trên Celestia DA

Đồ án xây dựng một **sovereign rollup chạy CosmWasm**, dùng **Celestia** làm lớp sẵn sàng dữ liệu (DA), với **lớp thực thi cài trực tiếp vào ev-node** (không qua adapter ABCI). Mô hình vận hành: một sequencer sản xuất block + các full node sync qua P2P và DA fallback.

<!-- markdownlint-disable MD013 -->
[![Go Report Card](https://goreportcard.com/badge/github.com/DataAvailabilityLayerNovel/chain-sdk)](https://goreportcard.com/report/github.com/DataAvailabilityLayerNovel/chain-sdk)
[![GoDoc](https://godoc.org/github.com/DataAvailabilityLayerNovel/chain-sdk?status.svg)](https://godoc.org/github.com/DataAvailabilityLayerNovel/chain-sdk)
<!-- markdownlint-enable MD013 -->

> **Định vị.** Đây là một đồ án xây trên khung sovereign rollup [ev-node](https://github.com/evstack/ev-node). Đóng góp cốt lõi của đồ án là lớp thực thi `CosmosExecutor` (Cosmos SDK BaseApp + CosmWasm) cắm thẳng vào giao diện `execution.Executor` của ev-node. So sánh với các giải pháp khác: [cac-san-pham-lien-quan.md](apps/cosmos-exec/sdk/cosmoswasm/docs/cac-san-pham-lien-quan.md).

## 1) Tài liệu chính

- Index toàn bộ tài liệu & resource: [DOCUMENTATION_INDEX.md](DOCUMENTATION_INDEX.md)
- Go SDK cho dApp chain + WASM tx/query: [apps/cosmos-exec/sdk/cosmoswasm/README.md](apps/cosmos-exec/sdk/cosmoswasm/README.md)
- End-to-end (compile `.wasm` → start chain → deploy → interact): [getting-started.md](apps/cosmos-exec/sdk/cosmoswasm/docs/getting-started.md)
- Vận hành node (sequencer / full node): [node-operations.md](apps/cosmos-exec/sdk/cosmoswasm/docs/node-operations.md)
- Runner full stack: [scripts/run-cosmos-wasm-nodes.go](scripts/run-cosmos-wasm-nodes.go)
- RPC block/tx helper: [scripts/contracts/wasm-rpc.sh](scripts/contracts/wasm-rpc.sh)
- Frontend dApp web: [frontend-integration.md](apps/cosmos-exec/sdk/cosmoswasm/docs/frontend-integration.md)

## 2) Kiến trúc chain trong repo

- `apps/cosmos-wasm`: node binary `evcosmos`
- `apps/cosmos-exec`: execution backend `cosmos-exec-grpc` (chứa `CosmosExecutor` + `x/wasm`) và Go SDK `cosmoswasm`
- `scripts/run-cosmos-wasm-nodes.go`: orchestration sequencer + fullnode + exec services
- DA submit: đi trực tiếp qua runtime ev-node (aggregator), không cần sidecar riêng

Luồng tổng quát:

1. `evcosmos-sequencer` produce block
2. `cosmos-exec-grpc` execute tx / cập nhật state (CosmWasm)
3. Sequencer submit header/data lên Celestia DA (mỗi block → 2 blob: SignedHeader + SignedData)
4. `evcosmos-fullnode` sync qua P2P + DA fallback

## 3) Quickstart chạy chain (CLI)

Tại root repo:

```bash
set -a && source .env && set +a
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=true
```

Health check:

```bash
curl -sS http://127.0.0.1:38331/health/live
curl -sS http://127.0.0.1:48331/health/live
```

Dọn process nếu kẹt port:

```bash
pkill -f cosmos-exec-grpc || true
pkill -f evcosmos || true
pkill -f run-cosmos-wasm-nodes.go || true
```

Chi tiết đầy đủ: [node-operations.md](apps/cosmos-exec/sdk/cosmoswasm/docs/node-operations.md).

## 4) Quickstart chạy bằng SDK / examples

SDK package:

`github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm`

Ví dụ runnable (trong `apps/cosmos-exec/sdk/cosmoswasm/examples/`):

- Counter contract end-to-end: [examples/my-counter/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/my-counter/main.go)
- Batch event lên DA (blob-first): [examples/game-telemetry/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/game-telemetry/main.go)
- Forced inclusion (chống kiểm duyệt): [examples/forced-inclusion/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/forced-inclusion/main.go)

Chạy nhanh (cần chain đã chạy ở mục 3):

```bash
# 1. Boot chain ở terminal khác
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=true

# 2. Chạy example (tự đọc .env ở thư mục cha)
go run ./apps/cosmos-exec/sdk/cosmoswasm/examples/my-counter
```

## 5) Lệnh thường dùng (Scripts)

- RPC state/block/tx:
	- `./scripts/contracts/wasm-rpc.sh status`
	- `./scripts/contracts/wasm-rpc.sh latest-block`
	- `./scripts/contracts/wasm-rpc.sh tx --hash <HEX_TX_HASH>`
- Contract flow: dùng SDK Go ([getting-started.md](apps/cosmos-exec/sdk/cosmoswasm/docs/getting-started.md)) hoặc FE my-dapp-web.

## 6) Biến môi trường & endpoint

### Biến DA (Data Availability)
- `DA_BRIDGE_RPC` hoặc `DA_RPC` — Celestia bridge RPC endpoint
- `DA_AUTH_TOKEN` — Authentication token cho DA layer
- `DA_NAMESPACE` — Namespace cho DA submit/query

### Biến chain / exec
- `EVCOSMOS_PASSPHRASE` / `EVCOSMOS_PASSPHRASE_FILE` — passphrase cho signer của node
- `COSMOS_EXEC_CHAIN_ID`, `COSMOS_EXEC_GAS_DENOM`, `COSMOS_EXEC_MIN_GAS_PRICE` — cấu hình exec (examples)
- `ENABLE_FAUCET`, `FAUCET_AMOUNT`, `COSMOS_EXEC_TREASURY_PRIVKEY_HEX` — faucet dev (examples)

> Examples tự tìm file `.env` bằng cách đi ngược lên các thư mục cha — không cần set đường dẫn project root thủ công.

### Endpoint mặc định
- **Exec API (gRPC/HTTP)**: `http://127.0.0.1:50051` — tx submit, contract operations
- **Cosmos REST**: `http://127.0.0.1:38331` — bank balance query, health
- **Cosmos RPC**: `http://127.0.0.1:38657` — state/block/tx queries

**Lưu ý**: Tránh set sai `DA_NAMESPACE_B64` thủ công. Namespace query/submit nên đồng bộ từ `DA_NAMESPACE`.

## 7) Go SDK Package

Dành cho dApp developer integrate với chain theo lập trình:

```bash
cd apps/cosmos-exec
go test ./sdk/cosmoswasm/...
```

**Go module**: `github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec`

```go
import cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
```

API reference: [apps/cosmos-exec/sdk/cosmoswasm/README.md](apps/cosmos-exec/sdk/cosmoswasm/README.md)

## 8) Contributing & nguồn

- Hướng dẫn đóng góp: [CONTRIBUTING.md](CONTRIBUTING.md)
- Repo đồ án (chain SDK): [github.com/DataAvailabilityLayerNovel/chain-sdk](https://github.com/DataAvailabilityLayerNovel/chain-sdk)
- Khung nền tảng (upstream): [ev-node](https://github.com/evstack/ev-node) · [ev.xyz](https://ev.xyz)
