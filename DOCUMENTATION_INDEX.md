# Documentation & Resources Index

Navigation guide để tìm SDK, CLI, CosmWasm, examples, docs, và các tools khác trong repo đồ án (sovereign CosmWasm rollup trên Celestia DA, xây trên ev-node).

---

## 📚 Documentation & Guides

| Tài liệu | Đường dẫn | Mô tả |
|----------|----------|-------|
| **README chính** | [README.md](README.md) | Tổng quan đồ án, quickstart, chain architecture |
| **SDK docs (chính)** | [apps/cosmos-exec/sdk/cosmoswasm/README.md](apps/cosmos-exec/sdk/cosmoswasm/README.md) | Index toàn bộ tài liệu kỹ thuật + function map |
| **Getting started** | [apps/cosmos-exec/sdk/cosmoswasm/docs/getting-started.md](apps/cosmos-exec/sdk/cosmoswasm/docs/getting-started.md) | End-to-end: compile `.wasm` → start chain → deploy → interact |
| **Node operations** | [apps/cosmos-exec/sdk/cosmoswasm/docs/node-operations.md](apps/cosmos-exec/sdk/cosmoswasm/docs/node-operations.md) | Chạy sequencer + full node, ports, data on/off-chain |
| **Contributing** | [CONTRIBUTING.md](CONTRIBUTING.md) | Hướng dẫn đóng góp code, PR guidelines |
| **Changelog** | [CHANGELOG.md](CHANGELOG.md) | Release notes, version history |

---

## 🔧 SDK & Go Modules

### Cosmos SDK (cosmoswasm)

| Thành phần | Đường dẫn | Mô tả |
|----------|----------|-------|
| **SDK README** | [apps/cosmos-exec/sdk/cosmoswasm/README.md](apps/cosmos-exec/sdk/cosmoswasm/README.md) | Full SDK guide, API reference, examples, import guide |
| **SDK Examples** | [apps/cosmos-exec/sdk/cosmoswasm/examples/](apps/cosmos-exec/sdk/cosmoswasm/examples/) | Runnable examples |
| ├─ Counter contract | [examples/my-counter/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/my-counter/main.go) | End-to-end: deploy + execute + query CosmWasm |
| ├─ Game telemetry | [examples/game-telemetry/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/game-telemetry/main.go) | Batch event lên DA (blob-first) |
| ├─ Forced inclusion | [examples/forced-inclusion/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/forced-inclusion/main.go) | Chống kiểm duyệt qua forced inclusion |
| **SDK Package** | `github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm` | Go import path |
| **Go Module** | [apps/cosmos-exec/go.mod](apps/cosmos-exec/go.mod) | Dependencies |

**Cách import:**
```go
import cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
```

---

## ⚙️ Chain Architecture

| Component | Path | Binary | Mô tả |
|-----------|------|--------|-------|
| **Cosmos WASM Node** | [apps/cosmos-wasm/](apps/cosmos-wasm/) | `evcosmos` | Sequencer + fullnode binary |
| **Execution Backend** | [apps/cosmos-exec/](apps/cosmos-exec/) | `cosmos-exec-grpc` | `CosmosExecutor` + `x/wasm`: execute tx, update state |
| **Orchestration** | [scripts/run-cosmos-wasm-nodes.go](scripts/run-cosmos-wasm-nodes.go) | — | Runner: start sequencer+fullnode+exec |
| **Node full** | [node/full.go](node/full.go) | — | Full node implementation |
| **Sequencer** | [node/node.go](node/node.go) | — | Sequencer logic |
| **Block operations** | [block/](block/) | — | Block production, validation |
| **Core types** | [types/](types/) | — | Epoch, header, serialization, hashing |

---

## 🧪 Examples & Scripts

### Executable Examples

| Example | Path | Dùng cho |
|---------|------|----------|
| **Counter contract** | [examples/my-counter/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/my-counter/main.go) | Deploy + execute + query CosmWasm end-to-end |
| **Game telemetry** | [examples/game-telemetry/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/game-telemetry/main.go) | Batch event lên DA (blob-first) |
| **Forced inclusion** | [examples/forced-inclusion/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/forced-inclusion/main.go) | Forced inclusion / chống kiểm duyệt |

### Helper Scripts

| Script | Path | Dùng cho |
|--------|------|----------|
| **Run stack** | [scripts/run-cosmos-wasm-nodes.go](scripts/run-cosmos-wasm-nodes.go) | Orchestrate sequencer+fullnode+exec |
| **RPC queries** | [scripts/contracts/wasm-rpc.sh](scripts/contracts/wasm-rpc.sh) | Status, latest block, tx query |
| **Deploy contract** | [scripts/deploy-sample-contract.sh](scripts/deploy-sample-contract.sh) | Deploy contract mẫu |
| **Submit tx** | [scripts/submit-tx.sh](scripts/submit-tx.sh) | Submit transaction |
| **Verify DA submit** | [scripts/verify-da-submit.sh](scripts/verify-da-submit.sh) | Kiểm tra blob đã lên DA |
| **Namespace tool** | [scripts/encode-namespace.sh](scripts/encode-namespace.sh) | Encode DA namespace |
| **Base64 tool** | [scripts/base64-tool.sh](scripts/base64-tool.sh) | Encode/decode base64 |
| **CI all modules** | [scripts/ci-all-modules.sh](scripts/ci-all-modules.sh) | Lint/test mọi go.mod |

---

## 📦 Tools & Utilities

| Tool | Path | Mô tả |
|------|------|-------|
| **cosmos-explorer** | [tools/cosmos-explorer/](tools/cosmos-explorer/) | Chain explorer |
| **cosmos-wasm-tx** | [tools/cosmos-wasm-tx/](tools/cosmos-wasm-tx/) | Build/submit tx CosmWasm |
| **da-debug** | [tools/da-debug/](tools/da-debug/) | Debug DA layer issues |
| **blob-decoder** | [tools/blob-decoder/](tools/blob-decoder/) | Decode DA blob data |
| **cache-analyzer** | [tools/cache-analyzer/](tools/cache-analyzer/) | Phân tích cache |
| **db-bench** | [tools/db-bench/](tools/db-bench/) | Database benchmark |
| **evnode-rpc** | [tools/evnode-rpc/](tools/evnode-rpc/) | gRPC client tool |
| **local-da** | [tools/local-da/](tools/local-da/) | Local DA mock |

---

## 🧪 Tests & Test Data

| Test Suite | Path | Mô tả |
|-----------|------|-------|
| **Unit tests** | `*_test.go` (throughout repo) | Unit test files |
| **E2E tests** | [test/e2e/](test/e2e/) | End-to-end test suite |
| **Docker E2E** | [test/docker-e2e/](test/docker-e2e/) | Docker-based E2E tests |
| **Mocks** | [test/mocks/](test/mocks/) | Mock implementations |
| **Test DA** | [test/testda/](test/testda/) | Test DA layer |

**Run tests:**
```bash
# SDK tests
cd apps/cosmos-exec && go test ./sdk/cosmoswasm/...

# All tests
go test ./...

# Specific test
go test ./node -run TestFullNode
```

---

## 📡 API & Endpoints

### Default Endpoints (Local)

| Service | Endpoint | Port | Dùng cho |
|---------|----------|------|----------|
| **Exec API (gRPC/HTTP)** | `http://127.0.0.1:50051` | 50051 | TX submit, contract operations |
| **Cosmos REST** | `http://127.0.0.1:38331` | 38331 | Bank balance query, chain info, health |
| **Cosmos RPC** | `http://127.0.0.1:38657` | 38657 | State, block, tx queries |
| **Sequencer REST** | `http://127.0.0.1:48331` | 48331 | Sequencer-specific queries |
| **Sequencer RPC** | `http://127.0.0.1:48657` | 48657 | Sequencer P2P |

---

## 🌍 External Resources

| Resource | Link | Mô tả |
|----------|------|-------|
| **Chain SDK Repo** | [github.com/DataAvailabilityLayerNovel/chain-sdk](https://github.com/DataAvailabilityLayerNovel/chain-sdk) | Repo đồ án |
| **ev-node (upstream)** | [github.com/evstack/ev-node](https://github.com/evstack/ev-node) · [ev.xyz](https://ev.xyz) | Khung sovereign rollup nền tảng |
| **Cosmos Docs** | [docs.cosmos.network](https://docs.cosmos.network) | Cosmos SDK documentation |
| **CosmWasm Docs** | [docs.cosmwasm.com](https://docs.cosmwasm.com) | WASM contract documentation |
| **Celestia Docs** | [docs.celestia.org](https://docs.celestia.org) | DA layer documentation |

---

## 📋 Quick Navigation

**I want to...**

| Goal | Go to | What to run |
|------|-------|------------|
| Start chain quickly | [README.md](README.md) section 3 | `go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=true` |
| Use SDK to write Go app | [apps/cosmos-exec/sdk/cosmoswasm/README.md](apps/cosmos-exec/sdk/cosmoswasm/README.md) | `import cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"` |
| Deploy a contract | [getting-started.md](apps/cosmos-exec/sdk/cosmoswasm/docs/getting-started.md) | `BuildStoreTx` + `BuildInstantiateTx` → `SubmitTxBytes` |
| Query contract state | [api-reference.md](apps/cosmos-exec/sdk/cosmoswasm/docs/api-reference.md) | `client.QuerySmart(ctx, contract, msg)` |
| Submit transaction | [api-reference.md](apps/cosmos-exec/sdk/cosmoswasm/docs/api-reference.md) | `client.SubmitTxBytes(ctx, tx)` |
| Check DA blob | [scripts/verify-da-submit.sh](scripts/verify-da-submit.sh) | `./scripts/verify-da-submit.sh` |
| Run full E2E tests | [test/e2e/](test/e2e/) | `go test ./test/e2e/...` |
| Understand architecture | [architecture.md](apps/cosmos-exec/sdk/cosmoswasm/docs/architecture.md) | Read concept docs |
| So sánh với giải pháp khác | [cac-san-pham-lien-quan.md](apps/cosmos-exec/sdk/cosmoswasm/docs/cac-san-pham-lien-quan.md) | Đối chiếu ev-abci, Dymension, OP Stack… |
| Contribute code | [CONTRIBUTING.md](CONTRIBUTING.md) | Follow guidelines |

---

## 📂 File Structure (High Level)

```
chain-sdk/
├── README.md                          # Main readme
├── DOCUMENTATION_INDEX.md             # This file (navigation)
├── CONTRIBUTING.md                    # Contribution guide
├── CHANGELOG.md                       # Release notes
│
├── apps/
│   ├── cosmos-wasm/                   # Cosmos WASM node (evcosmos)
│   ├── cosmos-exec/                   # Execution backend
│   │   ├── sdk/cosmoswasm/            # ★ Go SDK
│   │   │   ├── README.md
│   │   │   ├── docs/                  # Tài liệu kỹ thuật + luận văn
│   │   │   └── examples/              # Runnable examples
│   │   └── cmd/cosmos-exec-grpc/      # HTTP/gRPC API server (main binary)
│
├── tools/                             # Utility tools
│   ├── da-debug/
│   ├── cosmos-explorer/
│   └── ...
│
├── scripts/                           # Helper scripts
│   ├── run-cosmos-wasm-nodes.go       # Orchestrator
│   ├── contracts/
│   └── ...
│
├── test/                              # Tests
│   ├── e2e/
│   ├── docker-e2e/
│   └── ...
│
├── block/                             # Block operations
├── core/                              # Core logic
├── types/                             # Data types
├── node/                              # Node implementations
│   ├── full.go
│   └── node.go
│
└── ...

★ = Most commonly used
```

---

Updated: navigation index cho repo đồ án `DataAvailabilityLayerNovel/chain-sdk`.
