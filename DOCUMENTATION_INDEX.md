# Documentation & Resources Index

Complete navigation guide để tìm SDK, CLI, CosmWasm, examples, docs, và các tools khác trong ev-node repo.

---

## 📚 Documentation & Guides

| Tài liệu | Đường dẫn | Mô tả |
|----------|----------|-------|
| **README chính** | [README.md](README.md) | Tổng quan ev-node, quickstart, chain architecture |
| **Cosmos + WASM Runbook** | [cosmos.md](cosmos.md) | Chi tiết chạy Cosmos chain, WASM contract operations |
| **Contributing** | [CONTRIBUTING.md](CONTRIBUTING.md) | Hướng dẫn đóng góp code, PR guidelines |
| **Changelog** | [CHANGELOG.md](CHANGELOG.md) | Release notes, version history |
| **Docs folder** | [docs/](docs/) | API docs, concepts, architecture diagrams |
| **Docs README** | [docs/README.md](docs/README.md) | Navigation cho docs folder |

---

## 🔧 SDK & Go Modules

### Cosmos SDK (cosmoswasm)

| Thành phần | Đường dẫn | Mô tả |
|----------|----------|-------|
| **SDK README** | [apps/cosmos-exec/sdk/cosmoswasm/README.md](apps/cosmos-exec/sdk/cosmoswasm/README.md) | Full SDK guide, API reference, examples, import guide |
| **SDK Examples** | [apps/cosmos-exec/sdk/cosmoswasm/examples/](apps/cosmos-exec/sdk/cosmoswasm/examples/) | Runnable examples |
| ├─ Start chain | [examples/dapp-chain/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/dapp-chain/main.go) | Minimal example: start DAL chain |
| ├─ Chain + Deploy | [examples/dapp-chain-deploy/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/dapp-chain-deploy/main.go) | Start chain + deploy contract |
| ├─ Submit Tx | [examples/submit-tx/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/submit-tx/main.go) | Build & submit transaction |
| ├─ Query Contract | [examples/query-contract/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/query-contract/main.go) | Smart query WASM contract |
| **SDK Package** | `github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm` | Go import path |
| **Go Module** | [apps/cosmos-exec/go.mod](apps/cosmos-exec/go.mod) | Dependencies |

**Cách import:**
```go
import "github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm"
```

---

## 💻 CLI Tool (dal-sdk)

| Thành phần | Đường dẫn | Mô tả |
|----------|----------|-------|
| **CLI README** | [apps/cosmos-exec/cmd/dal-sdk/README.md](apps/cosmos-exec/cmd/dal-sdk/README.md) | CLI commands, flags, usage examples |
| **CLI Source** | [apps/cosmos-exec/cmd/dal-sdk/main.go](apps/cosmos-exec/cmd/dal-sdk/main.go) | Implementation |
| **Build command** | `cd apps/cosmos-exec && go build -o dal-sdk ./cmd/dal-sdk` | Compile CLI |

**Available commands:**
- `chain start` — Start dApp chain
- `tx submit|result` — Submit & query transactions
- `contract store|instantiate|execute|query|deploy|deploy-cw20|balance|transfer` — Contract operations
- `bank send|balance` — Native coin operations

**Example:**
```bash
./dal-sdk contract deploy-cw20 --wasm ./cw20.wasm --name Token --symbol TOK --supply 1000000
```

---

## ⚙️ Chain Architecture

| Component | Path | Binary | Mô tả |
|-----------|------|--------|-------|
| **Cosmos WASM Node** | [apps/cosmos-wasm/](apps/cosmos-wasm/) | `evcosmos` | Sequencer + fullnode binary |
| **Execution Backend** | [apps/cosmos-exec/](apps/cosmos-exec/) | `cosmos-exec-grpc` | Execute tx, update state |
| **EVM Execution** | [apps/evm/](apps/evm/) | — | EVM compatibility layer |
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
| **Start DAL chain** | [apps/cosmos-exec/sdk/cosmoswasm/examples/dapp-chain/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/dapp-chain/main.go) | Minimal DAL chain startup |
| **Chain + Deploy** | [apps/cosmos-exec/sdk/cosmoswasm/examples/dapp-chain-deploy/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/dapp-chain-deploy/main.go) | Full flow: start + deploy |
| **Submit TX** | [apps/cosmos-exec/sdk/cosmoswasm/examples/submit-tx/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/submit-tx/main.go) | Build & submit transaction |
| **Query Contract** | [apps/cosmos-exec/sdk/cosmoswasm/examples/query-contract/main.go](apps/cosmos-exec/sdk/cosmoswasm/examples/query-contract/main.go) | Smart query WASM contract |

### Helper Scripts

| Script | Path | Dùng cho |
|--------|------|----------|
| **Run stack** | [scripts/run-cosmos-wasm-nodes.go](scripts/run-cosmos-wasm-nodes.go) | Orchestrate sequencer+fullnode+exec |
| **Contract helper** | [scripts/contracts/wasm-contract.sh](scripts/contracts/wasm-contract.sh) | Deploy, execute, query contract |
| **RPC queries** | [scripts/contracts/wasm-rpc.sh](scripts/contracts/wasm-rpc.sh) | Status, latest block, tx query |
| **DA query** | [scripts/query_celestia_blob.sh](scripts/query_celestia_blob.sh) | Query DA blob by height |
| **DA watch** | [scripts/watch_celestia_latest_blobs.sh](scripts/watch_celestia_latest_blobs.sh) | Real-time DA blob monitoring |
| **Base64 tool** | [scripts/base64-tool.sh](scripts/base64-tool.sh) | Encode/decode base64 |
| **Namespace tool** | [scripts/encode-namespace.sh](scripts/encode-namespace.sh) | Encode DA namespace |

---

## 📦 Tools & Utilities

| Tool | Path | Mô tả |
|------|------|-------|
| **dal-sdk** | [apps/cosmos-exec/cmd/dal-sdk/](apps/cosmos-exec/cmd/dal-sdk/) | CLI for chain/contract/tx/bank operations |
| **cosmos-da-submit** | [tools/cosmos-da-submit/](tools/cosmos-da-submit/) | Submit tx to Cosmos chain + DA |
| **cosmos-explorer** | [tools/cosmos-explorer/](tools/cosmos-explorer/) | Chain explorer |
| **da-debug** | [tools/da-debug/](tools/da-debug/) | Debug DA layer issues |
| **db-bench** | [tools/db-bench/](tools/db-bench/) | Database benchmark |
| **evnode-rpc** | [tools/evnode-rpc/](tools/evnode-rpc/) | gRPC client tool |
| **local-da** | [tools/local-da/](tools/local-da/) | Local DA mock |
| **blob-decoder** | [tools/blob-decoder/](tools/blob-decoder/) | Decode DA blob data |

---

## 🧪 Tests & Test Data

| Test Suite | Path | Mô tả |
|-----------|------|-------|
| **Unit tests** | `*_test.go` (throughout repo) | Unit test files |
| **E2E tests** | [test/e2e/](test/e2e/) | End-to-end test suite |
| **Docker E2E** | [test/docker-e2e/](test/docker-e2e/) | Docker-based E2E tests |
| **Mocks** | [test/mocks/](test/mocks/) | Mock implementations |
| **Test DA** | [test/testda/](test/testda/) | Test DA layer |
| **Test app** | [apps/testapp/](apps/testapp/) | Test application |

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
| **Exec API (gRPC)** | `http://127.0.0.1:50051` | 50051 | TX submit, contract operations |
| **Cosmos REST** | `http://127.0.0.1:38331` | 38331 | Bank balance query, chain info |
| **Cosmos RPC** | `http://127.0.0.1:38657` | 38657 | State, block, tx queries |
| **Sequencer REST** | `http://127.0.0.1:48331` | 48331 | Sequencer-specific queries |
| **Sequencer RPC** | `http://127.0.0.1:48657` | 48657 | Sequencer P2P |

---

## 🌍 External Resources

| Resource | Link | Mô tả |
|----------|------|-------|
| **Chain SDK Repo** | [github.com/DataAvailabilityLayerNovel/chain-sdk](https://github.com/DataAvailabilityLayerNovel/chain-sdk) | Upstream chain SDK (production version) |
| **Evolve Website** | [ev.xyz](https://ev.xyz) | Project website |
| **Cosmos Docs** | [docs.cosmos.network](https://docs.cosmos.network) | Cosmos SDK documentation |
| **CosmWasm Docs** | [docs.cosmwasm.com](https://docs.cosmwasm.com) | WASM contract documentation |
| **Celestia Bridge** | Celestia bridge endpoint | DA layer bridge |

---

## 📋 Quick Navigation

**I want to...**

| Goal | Go to | What to run |
|------|-------|------------|
| Start DAL chain quickly | [cosmos.md](cosmos.md) section 3 | `go run ./scripts/run-cosmos-wasm-nodes.go` |
| Use SDK to write Go app | [apps/cosmos-exec/sdk/cosmoswasm/README.md](apps/cosmos-exec/sdk/cosmoswasm/README.md) | `go get github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm` |
| Use CLI without coding | [apps/cosmos-exec/cmd/dal-sdk/README.md](apps/cosmos-exec/cmd/dal-sdk/README.md) | `go build -o dal-sdk ./cmd/dal-sdk` |
| Deploy a contract | [cosmos.md](cosmos.md) section 5 | `./dal-sdk contract deploy-cw20 ...` |
| Query contract state | [apps/cosmos-exec/sdk/cosmoswasm/README.md](apps/cosmos-exec/sdk/cosmoswasm/README.md) section 4 | `./dal-sdk contract query ...` |
| Submit transaction | [apps/cosmos-exec/cmd/dal-sdk/README.md](apps/cosmos-exec/cmd/dal-sdk/README.md) | `./dal-sdk tx submit ...` |
| Check DA blob | [scripts/query_celestia_blob.sh](scripts/query_celestia_blob.sh) | `./scripts/query_celestia_blob.sh` |
| Run full E2E tests | [test/e2e/](test/e2e/) | `go test ./test/e2e/...` |
| Understand architecture | [docs/overview/](docs/overview/) | Read concept docs |
| Contribute code | [CONTRIBUTING.md](CONTRIBUTING.md) | Follow guidelines |

---

## 📂 File Structure (High Level)

```
ev-node/
├── README.md                          # Main readme
├── DOCUMENTATION_INDEX.md             # This file (navigation)
├── cosmos.md                          # Cosmos chain runbook
├── CONTRIBUTING.md                    # Contribution guide
├── CHANGELOG.md                       # Release notes
│
├── apps/
│   ├── cosmos-wasm/                   # Cosmos WASM node (evcosmos)
│   ├── cosmos-exec/                   # Execution backend
│   │   ├── sdk/cosmoswasm/            # ★ Go SDK
│   │   │   ├── README.md
│   │   │   └── examples/              # Runnable examples
│   │   └── cmd/dal-sdk/               # ★ CLI tool
│   │       ├── README.md
│   │       └── main.go
│   └── evm/                           # EVM layer
│
├── tools/                             # Utility tools
│   ├── cosmos-da-submit/
│   ├── da-debug/
│   ├── evnode-rpc/
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
├── docs/                              # Documentation
│   ├── README.md
│   ├── concepts/
│   ├── guides/
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

Version: 31 tháng 3, 2026 | Updated: Auto-generated index
