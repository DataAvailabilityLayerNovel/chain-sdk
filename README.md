# EV-node Cosmos Chain Hub

EV-node là nền tảng để chạy chain Cosmos + WASM trên DA layer (Celestia) theo mô hình sequencer + fullnode.

<!-- markdownlint-disable MD013 -->
[![Go Report Card](https://goreportcard.com/badge/github.com/evstack/ev-node)](https://goreportcard.com/report/github.com/evstack/ev-node)
[![codecov](https://codecov.io/gh/evstack/ev-node/branch/main/graph/badge.svg?token=CWGA4RLDS9)](https://codecov.io/gh/evstack/ev-node)
[![GoDoc](https://godoc.org/github.com/evstack/ev-node?status.svg)](https://godoc.org/github.com/evstack/ev-node)
<!-- markdownlint-enable MD013 -->

> **Version note**: Không dùng tag/release trước `v1.*` cho môi trường production.

## 1) Tài liệu chính

- Runbook Cosmos + WASM: [cosmos.md](cosmos.md)
- Go SDK cho dApp chain + WASM tx/query: [apps/cosmos-exec/sdk/cosmoswasm/README.md](apps/cosmos-exec/sdk/cosmoswasm/README.md)
- Runner full stack: [scripts/run-cosmos-wasm-nodes.go](scripts/run-cosmos-wasm-nodes.go)
- RPC block/tx helper: [scripts/contracts/wasm-rpc.sh](scripts/contracts/wasm-rpc.sh)
- Deploy / interact contract: dùng SDK Go ([apps/cosmos-exec/sdk/cosmoswasm/docs/getting-started.md](apps/cosmos-exec/sdk/cosmoswasm/docs/getting-started.md)) hoặc FE my-dapp-web ([frontend-integration.md](apps/cosmos-exec/sdk/cosmoswasm/docs/frontend-integration.md))
- Chain SDK Go modules: [github.com/DataAvailabilityLayerNovel/chain-sdk](https://github.com/DataAvailabilityLayerNovel/chain-sdk)

## 2) Kiến trúc chain Cosmos trong repo

- `apps/cosmos-wasm`: node binary `evcosmos`
- `apps/cosmos-exec`: execution backend `cosmos-exec-grpc`
- `scripts/run-cosmos-wasm-nodes.go`: orchestration sequencer + fullnode + exec services
- DA submit: đi trực tiếp qua runtime evnode (aggregator), không cần sidecar riêng

Luồng tổng quát:

1. `evcosmos-sequencer` produce block
2. `cosmos-exec-grpc` execute tx / cập nhật state
3. Sequencer submit header/data lên DA
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

Chi tiết đầy đủ: [cosmos.md](cosmos.md)

## 4) Quickstart chạy chain bằng SDK

SDK package:

`github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm`

Ví dụ runnable:

- Start chain: [apps/cosmos-exec/sdk/cosmoswasm/examples/dapp-chain/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/dapp-chain/main.go)
- Start chain + deploy contract: [apps/cosmos-exec/sdk/cosmoswasm/examples/dapp-chain-deploy/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/dapp-chain-deploy/main.go)

Chạy nhanh:

```bash
cd apps/cosmos-exec

export EVNODE_PROJECT_ROOT=/absolute/path/to/ev-node
export CHAIN_NAME=my-dapp-chain
export DA_NAMESPACE=my-dapp-namespace
export DA_BRIDGE_RPC=https://<celestia-bridge-rpc>
export DA_AUTH_TOKEN=<token>

go run ./sdk/cosmoswasm/examples/dapp-chain
```

## 5) Lệnh thường dùng cho Cosmos chain (Scripts)

- RPC state/block/tx:
	- `./scripts/contracts/wasm-rpc.sh status`
	- `./scripts/contracts/wasm-rpc.sh latest-block`
	- `./scripts/contracts/wasm-rpc.sh tx --hash <HEX_TX_HASH>`
- Contract flow: dùng SDK Go ([getting-started.md](apps/cosmos-exec/sdk/cosmoswasm/docs/getting-started.md)) hoặc FE my-dapp-web — bash wrapper đã bỏ.

## 6) Biến môi trường & địa chỉ endpoint

### Biến DA (Data Availability)
- `DA_BRIDGE_RPC` hoặc `DA_RPC` — Celestia bridge RPC endpoint
- `DA_AUTH_TOKEN` — Authentication token cho DA layer
- `DA_NAMESPACE` — Namespace cho DA submit/query

### Biến Chain
- `CHAIN_NAME` — Tên dApp chain (SDK/examples)
- `EVNODE_PROJECT_ROOT` — Địa chỉ root folder ev-node (SDK/examples)

### Endpoint mặc định
- **Exec API (gRPC)**: `http://127.0.0.1:50051` — Dùng cho tx submit, contract operations
- **Cosmos REST**: `http://127.0.0.1:38331` — Dùng cho bank balance query
- **Cosmos RPC**: `http://127.0.0.1:38657` — State/block/tx queries

**Lưu ý**: Tránh set sai `DA_NAMESPACE_B64` thủ công. Namespace query/submit nên đồng bộ từ `DA_NAMESPACE`.

## 7) Go SDK Package

Dành cho dApp developers integrate với chain theo lập trình:

```bash
cd apps/cosmos-exec
go test ./sdk/cosmoswasm/...
```

**Go module**: `github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm`

API reference: [apps/cosmos-exec/sdk/cosmoswasm/README.md](apps/cosmos-exec/sdk/cosmoswasm/README.md)

## 8) Contributing

- Hướng dẫn đóng góp: [CONTRIBUTING.md](CONTRIBUTING.md)
- Bộ docs tổng quan: [docs/README.md](docs/README.md)
- Chain SDK repo: [github.com/DataAvailabilityLayerNovel/chain-sdk](https://github.com/DataAvailabilityLayerNovel/chain-sdk)
- Website Evolve: [ev.xyz](https://ev.xyz)
