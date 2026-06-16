# Cosmos Chain vs ev-node Framework — So sánh chi tiết

So sánh giữa những gì cosmos chain (`apps/cosmos-exec/` + `apps/cosmos-wasm/`) đã xây dựng so với ev-node framework cung cấp sẵn, bao gồm các phần mở rộng, phần thiếu, và các thiết kế khác biệt.

## Mục lục

1. [Tổng quan so sánh](#1-tổng-quan-so-sánh)
2. [Executor Interface — 6 core methods](#2-executor-interface--6-core-methods)
3. [Optional Interfaces — 3 methods](#3-optional-interfaces--3-methods)
4. [Phần cosmos chain XÂY DỰNG THÊM](#4-phần-cosmos-chain-xây-dựng-thêm)
5. [Phần cosmos chain THIẾT KẾ KHÁC](#5-phần-cosmos-chain-thiết-kế-khác)
6. [Phần ev-node cung cấp — cosmos chain DÙNG NGUYÊN](#6-phần-ev-node-cung-cấp--cosmos-chain-dùng-nguyên)
7. [So sánh với EVM Executor](#7-so-sánh-với-evm-executor)
8. [So sánh với Testapp KV Executor](#8-so-sánh-với-testapp-kv-executor)
9. [Tổng kết — Maturity Matrix](#9-tổng-kết--maturity-matrix)

---

## 1. Tổng quan so sánh

```
┌─────────────────────────────────────────────────────────────────────┐
│                       ev-node Framework                             │
│                                                                     │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ │
│  │Sequencer │ │ Block    │ │ P2P      │ │ DA       │ │ Store    │ │
│  │ (Single/ │ │ Producer │ │ (libp2p  │ │ (Celestia│ │ (LevelDB │ │
│  │  Based)  │ │ Syncer   │ │  Gossip) │ │  Blobs)  │ │  Cache)  │ │
│  └────┬─────┘ └────┬─────┘ └──────────┘ └──────────┘ └──────────┘ │
│       │             │                                               │
│       │    ┌────────┴────────┐                                      │
│       │    │ execution.Executor interface                           │
│       │    │  6 core + 3 optional methods                          │
│       └────┤                                                        │
│            └────────┬────────┘                                      │
└─────────────────────┼───────────────────────────────────────────────┘
                      │ (pluggable)
        ┌─────────────┼─────────────┬─────────────────┐
        ▼             ▼             ▼                 ▼
  ┌──────────┐  ┌──────────┐  ┌──────────┐     ┌──────────┐
  │  EVM     │  │ Cosmos/  │  │ Testapp  │     │  Custom  │
  │ (geth)   │  │ CosmWasm │  │ (KV)     │     │  ...     │
  └──────────┘  └──────────┘  └──────────┘     └──────────┘
                     ▲
                     │
              Cosmos chain của bạn
```

**ev-node cung cấp:** Sequencer, Block Production, P2P, DA, Store, Sync, Pruning, Raft HA, Metrics.

**Cosmos chain cung cấp:** Execution layer (Cosmos SDK + CosmWasm), HTTP API, SDK client, BlobStore, Persistence, Security middleware.

---

## 2. Executor Interface — 6 core methods

Mỗi execution layer phải implement 6 methods. So sánh cách cosmos chain implement vs EVM:

| Method | Cosmos chain | EVM | Khác biệt |
|--------|-------------|-----|-----------|
| `InitChain` | ABCI `InitChain` + `Commit` nếu AppHash rỗng. Idempotent: return stateRoot nếu đã init | Engine API `forkchoiceUpdatedV3` | Cosmos dùng ABCI, EVM dùng Engine API. Cosmos tự handle idempotency |
| `GetTxs` | Drain mempool (`e.mempool = e.mempool[:0]`) | `eth_getEvnodeTxPool` RPC | Cosmos mempool là in-memory slice. EVM dùng geth's native txpool |
| `ExecuteTxs` | `BeginBlock` → `DeliverTx` × N → `EndBlock` → `Commit` | `forkchoiceUpdatedV3` → `getPayloadV3` → `newPayloadV3` | Cosmos là synchronous ABCI. EVM là async Engine API pipeline |
| `SetFinal` | Update `finalizedHeight` + persist metadata | `forkchoiceUpdatedV3` với `FinalizedBlockHash` | Cosmos lưu height number. EVM set block hash trong forkchoice |
| `GetExecutionInfo` | Return `{MaxGas: 0}` (no gas limit) | Return `{MaxGas: gasLimit}` từ latest block | **Cosmos không enforce gas limit ở sequencer level** — chỉ Cosmos SDK internal gas meter |
| `FilterTxs` | Chỉ filter by `maxBytes` (cumulative size) | Full filter: gas estimation + size + validity check | **Cosmos filter đơn giản hơn** — không pre-execute để check gas |

**File:** [`apps/cosmos-exec/executor/executor.go`](apps/cosmos-exec/executor/executor.go)

### Chi tiết: FilterTxs — Điểm khác biệt quan trọng

```go
// Cosmos chain: chỉ filter bằng size
func (e *CosmosExecutor) FilterTxs(ctx context.Context, txs [][]byte, maxBytes, _ uint64, _ bool) ([]execution.FilterStatus, error) {
    for i, tx := range txs {
        if len(tx) == 0 { statuses[i] = FilterRemove; continue }
        if maxBytes > 0 && cumulativeBytes+uint64(len(tx)) > maxBytes {
            statuses[i] = FilterPostpone; continue
        }
        statuses[i] = FilterOK
    }
}
```

```go
// EVM: full gas estimation per tx
func (c *EngineClient) FilterTxs(...) {
    for _, tx := range txs {
        gasEstimate := estimateGas(tx)        // pre-execute
        if cumulativeGas + gasEstimate > maxGas { FilterPostpone }
        // ... validity checks ...
    }
}
```

**Hệ quả:** Cosmos chain cho phép block chứa tx sử dụng gas vượt "giới hạn" vì không check gas ở filter stage. Tuy nhiên Cosmos SDK internal gas meter sẽ reject tx out-of-gas tại DeliverTx — tx vẫn nằm trong block nhưng execution fails (code != 0).

---

## 3. Optional Interfaces — 3 methods

ev-node check optional interfaces bằng Go type assertion tại runtime:

```go
// block/components.go:181-184
if p, ok := exec.(coreexecutor.ExecPruner); ok {
    execPruner = p
}
```

| Interface | Cosmos | EVM | Testapp KV | Mục đích |
|-----------|--------|-----|------------|----------|
| `HeightProvider` | **Yes** | **Yes** | No | Crash recovery: detect mismatch giữa consensus và execution |
| `Rollbackable` | **Yes** (IAVL `LoadVersion`) | **Yes** (forkchoice update) | **Yes** (delete keys > height) | Auto-rollback khi EL ahead |
| `ExecPruner` | **Yes** (delete blocks + txResults maps) | **Yes** (delete EVMStore entries) | No | Prune old execution metadata |

### Cosmos chain Rollback — Chi tiết kỹ thuật

```go
// apps/cosmos-exec/executor/executor.go — Rollback()
func (e *CosmosExecutor) Rollback(ctx context.Context, targetHeight uint64) error {
    // 1. IAVL rollback: load state tree at old version
    e.app.LoadVersion(int64(targetHeight))

    // 2. Recalculate state root from reverted IAVL tree
    cms := e.app.CommitMultiStore()
    e.stateRoot = cms.LastCommitID().Hash

    // 3. Trim in-memory maps (blocks, txResults)
    // 4. Adjust finalizedHeight
    // 5. Persist metadata
}
```

**So sánh với EVM:**
- Cosmos: `LoadVersion()` → IAVL tree revert (trực tiếp trên state)
- EVM: `forkchoiceUpdatedV3` → geth reorg canonical chain (block hash based)

**So sánh với Testapp:**
- Cosmos: version-based rollback (IAVL giữ history)
- Testapp: brute-force delete tất cả keys có height > target

---

## 4. Phần cosmos chain XÂY DỰNG THÊM

Các thành phần **không có trong ev-node framework**, hoàn toàn do cosmos chain tự xây:

### 4.1. BlobStore — Content-Addressed Data Storage

**File:** [`apps/cosmos-exec/executor/blob_store.go`](apps/cosmos-exec/executor/blob_store.go)

```go
type BlobStore struct {
    blobs      map[string][]byte  // SHA-256 commitment → data
    totalBytes int
    maxBlobSize  int              // default 4MB per blob
    maxTotalSize int              // default 256MB total
}
```

**Mục đích:** Cho phép app lưu data lớn (game events, telemetry, assets) **off-chain** nhưng có commitment on-chain. Pattern: `StoreBlob(data) → commitment → ghi commitment lên WASM contract (32 bytes thay vì full data)`.

**ev-node không có feature này.** EVM executor cũng không. Đây là pattern riêng của cosmos chain cho use case game/telemetry.

**API endpoints:**

| Endpoint | Chức năng |
|----------|-----------|
| `POST /blob/submit` | Store single blob → return commitment |
| `GET /blob/retrieve?commitment=...` | Fetch blob by commitment |
| `POST /blob/batch` | Store batch → return Merkle root + commitments |
| `GET /blob/estimate-cost` | Estimate DA cost cho blob size |

### 4.2. PersistStore — Append-Only Disk Persistence

**File:** [`apps/cosmos-exec/executor/persist.go`](apps/cosmos-exec/executor/persist.go)

```
~/.cosmos-exec-grpc/data/
  ├── metadata.json     ← ChainMetadata (overwrite mỗi block)
  ├── tx_results.jsonl  ← append-only tx execution results
  ├── blocks.jsonl      ← append-only block info
  └── blobs.jsonl       ← append-only blob data
```

**Mục đích:** Persist execution metadata survive across restarts. Khi cosmos-exec-grpc start lại, nó replay tất cả `.jsonl` files vào memory.

**ev-node không quản lý phần này.** ev-node chỉ lưu block headers/data/state trong block store riêng. Execution layer tự quản lý state riêng.

**EVM so sánh:** EVM dùng geth's built-in LevelDB — tất cả đã persistent sẵn. Cosmos chain phải tự build persistence vì ABCI state nằm trong `app.CommitMultiStore()` (IAVL/LevelDB) nhưng metadata phụ (txResults, blocks, blobs) nằm in-memory.

### 4.3. Merkle Proof System

**File:** [`apps/cosmos-exec/sdk/cosmoswasm/proof.go`](apps/cosmos-exec/sdk/cosmoswasm/proof.go)

```go
type MerkleProof struct {
    Root       string          // Merkle root committed on-chain
    LeafIndex  int             // position in batch
    Commitment string          // SHA-256 of blob data
    Path       []MerklePathStep // sibling hashes from leaf → root
}
```

**Mục đích:** Chứng minh một blob thuộc batch mà Merkle root đã commit on-chain. Cho phép lightweight verification mà không cần download toàn bộ batch.

**Flow:** `StoreBatch(blobs)` → `(root, commitments)` → commit root on-chain → `BuildMerkleProof(commitments, index)` → proof → `VerifyMerkleProof(proof)`.

### 4.4. SDK Client Library

**File:** [`apps/cosmos-exec/sdk/cosmoswasm/`](apps/cosmos-exec/sdk/cosmoswasm/)

Một Go SDK hoàn chỉnh cho developers tương tác với cosmos chain:

| Component | File | Mục đích |
|-----------|------|----------|
| Client | [`client.go`](apps/cosmos-exec/sdk/cosmoswasm/client.go) | HTTP client (SubmitTx, QuerySmart, SubmitBlob, ...) |
| TxBuilder | [`tx.go`](apps/cosmos-exec/sdk/cosmoswasm/tx.go) | Build Cosmos SDK transactions (MsgExecuteContract, MsgSend) |
| Chain | [`chain.go`](apps/cosmos-exec/sdk/cosmoswasm/chain.go) | High-level chain operations |
| DABridge | [`da_bridge.go`](apps/cosmos-exec/sdk/cosmoswasm/da_bridge.go) | App-level DA operations (Submit + Commit + Watch) |
| DAClient | [`da_client.go`](apps/cosmos-exec/sdk/cosmoswasm/da_client.go) | Direct Celestia namespace access |
| Namespace | [`namespace.go`](apps/cosmos-exec/sdk/cosmoswasm/namespace.go) | Namespace encoding/decoding cho DA |
| Proof | [`proof.go`](apps/cosmos-exec/sdk/cosmoswasm/proof.go) | Merkle inclusion proofs |
| Chunk | [`chunk.go`](apps/cosmos-exec/sdk/cosmoswasm/chunk.go) | Split large data into DA-sized chunks |
| Compress | [`compress.go`](apps/cosmos-exec/sdk/cosmoswasm/compress.go) | Gzip compression for blobs |
| Cost | [`cost.go`](apps/cosmos-exec/sdk/cosmoswasm/cost.go) | DA cost estimation |
| Batch | [`batch.go`](apps/cosmos-exec/sdk/cosmoswasm/batch.go) | Batch blob operations |
| Commit | [`commit.go`](apps/cosmos-exec/sdk/cosmoswasm/commit.go) | On-chain commitment recording |

**ev-node không có SDK client.** Framework cung cấp RPC server (Connect-RPC) nhưng không có client library cho app developers.

### 4.5. DABridge — App-Level DA Integration

**File:** [`apps/cosmos-exec/sdk/cosmoswasm/da_bridge.go`](apps/cosmos-exec/sdk/cosmoswasm/da_bridge.go)

```go
type DABridge struct {
    da        DAClient        // Celestia access
    exec      ExecutorClient  // On-chain recording
    namespace *Namespace      // App-specific DA namespace
}

// SubmitAndCommit: data → DA layer → commitment → on-chain record
func (b *DABridge) SubmitAndCommit(ctx context.Context, req SubmitAndCommitRequest) (*SubmitAndCommitResult, error)

// Watch: subscribe to incoming blobs in namespace
func (b *DABridge) Watch(ctx context.Context) (<-chan DABlobEvent, error)
```

**Mục đích:** Cho phép app submit data trực tiếp lên Celestia namespace riêng, rồi record commitment on-chain. Khác với ev-node's DA submission (block headers/data) — đây là **app-level DA** cho game data, telemetry, assets.

### 4.6. HTTP Server + Security Middleware

**File:** [`apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go`](apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go)

Cosmos chain xây HTTP server riêng với:

| Feature | Mô tả |
|---------|-------|
| Auth token | Bearer token authentication |
| CORS | Configurable cross-origin |
| Rate limiting | Requests per second limit |
| Read-only mode | Block write operations |
| Max request body | Prevent oversized requests |
| Metrics | Custom Prometheus metrics (tx_submit, blob_submit, query counts) |
| Health/Ready | Kubernetes-style health probes |
| Swagger | Auto-generated API docs |
| Config profiles | dev/test/prod profiles |

**ev-node không có security middleware cho execution layer.** Framework chỉ cung cấp Connect-RPC service — authentication/rate-limiting là trách nhiệm của execution layer.

### 4.7. WASM Smart Query

**File:** [`apps/cosmos-exec/executor/executor.go`](apps/cosmos-exec/executor/executor.go) — `QuerySmart()`

```go
func (e *CosmosExecutor) QuerySmart(ctx context.Context, contract string, queryMsg []byte) ([]byte, error) {
    queryCtx := e.app.BaseApp.NewContext(false, tmproto.Header{Height: int64(e.lastHeight)})
    queryCtx = queryCtx.WithGasMeter(sdk.NewGasMeter(e.queryGasMax))
    return e.app.WasmKeeper.QuerySmart(queryCtx, contractAddr, queryMsg)
}
```

**Endpoint:** `POST /wasm/query-smart`

Read-only query vào WASM contract state. Có gas limit riêng (`queryGasMax`, default 50M) để prevent infinite loop.

### 4.8. InjectTx — Direct Mempool Access

```go
func (e *CosmosExecutor) InjectTx(ctx context.Context, tx []byte) (string, error) {
    e.mempool = append(e.mempool, txCopy)
    return hashTx(txCopy), nil
}
```

ev-node's `Executor.GetTxs()` là pull-based (framework kéo tx từ EL). Cosmos chain thêm `InjectTx()` là push-based (user đẩy tx vào EL qua HTTP). Kết hợp:
- **Push:** User → HTTP → `InjectTx()` → mempool
- **Pull:** ev-node Reaper → `GetTxs()` → drain mempool → sequencer

---

## 5. Phần cosmos chain THIẾT KẾ KHÁC

Các phần ev-node cung cấp nhưng cosmos chain implement khác:

### 5.1. 2-Process Architecture

| | Cosmos chain | EVM | Testapp |
|---|---|---|---|
| **Architecture** | 2 processes (cosmos-wasm + cosmos-exec-grpc) | 2 processes (ev-node + geth) | 1 process (embedded) |
| **Communication** | HTTP + Connect-RPC JSON | Engine API (JWT authenticated) | In-process function call |
| **State ownership** | IAVL tree in cosmos-exec | geth's LevelDB | ev-node's datastore |

**Cosmos chain giống EVM** ở điểm tách 2 process. Nhưng khác ở protocol:
- EVM dùng Engine API (chuẩn Ethereum, JWT auth)
- Cosmos dùng Connect-RPC JSON + custom HTTP (đơn giản hơn, không JWT)

### 5.2. Executor Client — Pure HTTP vs v1connect

**File:** [`apps/cosmos-wasm/cmd/executor_client.go`](apps/cosmos-wasm/cmd/executor_client.go)

Cosmos-wasm dùng **pure HTTP+JSON client** thay vì import v1connect generated code:

```go
type EnhancedExecutorClient struct {
    baseURL    string
    httpClient *http.Client
}

// Core methods: Connect-RPC JSON protocol
func (c *EnhancedExecutorClient) connectCall(ctx context.Context, method string, reqBody, respBody any) error {
    url := c.baseURL + "/evnode.v1.ExecutorService/" + method
    // POST with JSON body
}

// Optional interfaces: custom HTTP endpoints
func (c *EnhancedExecutorClient) httpCall(ctx context.Context, httpMethod, path string, ...) error {
    // Standard HTTP call
}
```

**Tại sao dùng pure HTTP?** Để tránh dependency vào generated code (`v1connect/execution.connect.go`), giảm coupling giữa cosmos-wasm và protobuf layer. Pure HTTP client đơn giản hơn và dễ maintain.

**Trade-off:**
- Pro: Không dependency vào generated code, compile ổn định
- Con: Phải tự handle JSON serialization format (uint64 as string, timestamps, ...)

### 5.3. GetExecutionInfo — MaxGas = 0

```go
// Cosmos chain: no gas limit at sequencer level
func (e *CosmosExecutor) GetExecutionInfo(...) (ExecutionInfo, error) {
    return ExecutionInfo{MaxGas: 0}, nil
}

// EVM: real gas limit from block
func (c *EngineClient) GetExecutionInfo(...) (ExecutionInfo, error) {
    return ExecutionInfo{MaxGas: header.GasLimit}, nil
}
```

**Ý nghĩa:** Sequencer không filter tx theo gas cho cosmos chain. Gas chỉ enforce bên trong Cosmos SDK khi DeliverTx chạy. Tx out-of-gas vẫn vào block nhưng fail (code != 0).

**Đây là design choice hợp lý** vì:
1. Cosmos SDK gas metering phức tạp (per-module, per-msg) — khó predict trước
2. Sovereign rollup không cần fee market cứng như L1/L2
3. Failed tx vẫn charge gas fee (giống Ethereum behavior)

---

## 6. Phần ev-node cung cấp — cosmos chain DÙNG NGUYÊN

Cosmos chain **không modify** các component sau — dùng nguyên từ ev-node:

| Component | ev-node package | Cosmos chain sử dụng |
|-----------|----------------|---------------------|
| Single Sequencer | `pkg/sequencers/single/` | Ordering txs, forced inclusion, DA catch-up |
| Based Sequencer | `pkg/sequencers/based/` | Alternative sequencer mode (DA-based ordering) |
| Block Producer | `block/internal/executing/` | executionLoop, ProduceBlock, CreateBlock, ApplyBlock |
| DA Submitter | `block/internal/submitting/` | Batching strategy, retry, header+data submission |
| DA Client | `block/internal/da/` | Celestia blob submit/retrieve |
| Syncer | `block/internal/syncing/` | DA follower + P2P sync + block validation |
| Pruner | `block/internal/pruner/` | Periodic pruning of old blocks + exec metadata |
| Reaper | `block/internal/reaping/` | Poll txs from executor → sequencer |
| P2P Client | `pkg/p2p/` | GossipSub, DHT peer discovery |
| Sync Service | `pkg/sync/` | go-header P2P header/data exchange |
| Block Store | `pkg/store/` | LevelDB + LRU cache |
| Node | `node/` | Full node lifecycle, aggregator/sync modes |
| Raft | `pkg/raft/` | Leader election, HA cluster |
| Cache | `block/internal/cache/` | Pending headers/data, DA inclusion tracking |
| Replay | `block/internal/common/replay.go` | Crash recovery, sync execution layer |
| Genesis | `pkg/genesis/` | Genesis doc loading |
| Signer | `pkg/signer/` | Ed25519 block signing |
| Config | `pkg/config/` | Node configuration (TOML) |
| CLI | `pkg/cmd/` | `ParseConfig`, `StartNode`, `SetupLogger` |

**Tổng cộng:** ~18 packages, ~50+ files — tất cả đều chạy bên cosmos-wasm process, không cần modify.

---

## 7. So sánh với EVM Executor

| Feature | Cosmos chain | EVM Executor | Ai tốt hơn? |
|---------|-------------|--------------|-------------|
| **State model** | IAVL tree (versioned, Merkle) | MPT (Modified Patricia Trie) | Ngang — cả hai đều Merkle-based |
| **Rollback** | `LoadVersion(height)` — native IAVL | forkchoice update — geth reorg | Cosmos đơn giản hơn |
| **GetTxs** | Drain in-memory slice | RPC to geth txpool | EVM robust hơn (geth txpool đã battle-tested) |
| **FilterTxs** | Size-only | Gas + size + validity | EVM chính xác hơn |
| **Gas metering** | Cosmos SDK internal only | EVM gas limit + EIP-1559 | Khác paradigm — không so sánh được |
| **Smart contracts** | CosmWasm (Rust/WASM) | EVM (Solidity/Vyper) | Khác ecosystem |
| **Persistence** | Custom PersistStore (JSONL) | geth's built-in LevelDB | EVM tốt hơn (production-grade) |
| **Crash recovery** | HeightProvider + Rollback + PersistStore replay | HeightProvider + Rollback + EVMStore | Ngang |
| **BlobStore** | Yes (content-addressed, Merkle proofs) | No | Cosmos có feature riêng |
| **Query** | WASM QuerySmart (gas-limited) | `eth_call` | Khác paradigm |
| **SDK client** | Full Go SDK | Ethereum standard (ethers.js, web3.py) | EVM ecosystem lớn hơn nhiều |
| **Security** | Auth token, CORS, rate limit, read-only | JWT auth (Engine API) | Cosmos app-layer security tốt hơn |
| **ExecMeta tracking** | PersistStore | EVMStore (idempotent execution) | EVM phức tạp hơn nhưng robust hơn |

### EVM có mà Cosmos chưa có:

| Feature | File | Mô tả |
|---------|------|-------|
| Idempotent ExecuteTxs | [`execution/evm/execution.go:349-362`](https://github.com/evstack/ev-node/blob/main/execution/evm/execution.go#L349-L362) | Detect already-promoted blocks, resume in-progress payloads |
| EVMStore with metadata | [`execution/evm/store.go`](https://github.com/evstack/ev-node/blob/main/execution/evm/store.go) | Track payload IDs, execution stages per height |
| Gas estimation in FilterTxs | [`execution/evm/execution.go:862`](https://github.com/evstack/ev-node/blob/main/execution/evm/execution.go#L862) | Pre-execute to estimate gas |
| Tracing (OpenTelemetry) | [`execution/evm/engine_rpc_tracing.go`](https://github.com/evstack/ev-node/blob/main/execution/evm/engine_rpc_tracing.go) | Span tracing cho Engine API calls |

### Cosmos có mà EVM không có:

| Feature | File | Mô tả |
|---------|------|-------|
| BlobStore + Merkle proofs | [`executor/blob_store.go`](apps/cosmos-exec/executor/blob_store.go) | Off-chain data với on-chain commitment |
| SDK client library | [`sdk/cosmoswasm/`](apps/cosmos-exec/sdk/cosmoswasm/) | Go SDK cho app developers |
| DABridge | [`sdk/cosmoswasm/da_bridge.go`](apps/cosmos-exec/sdk/cosmoswasm/da_bridge.go) | App-level DA operations |
| WASM QuerySmart | [`executor/executor.go:445`](apps/cosmos-exec/executor/executor.go) | Direct contract state query |
| HTTP API + Swagger | [`cmd/cosmos-exec-grpc/main.go`](apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go) | Full REST API với docs |
| Config profiles | [`config/config.go`](apps/cosmos-exec/config/config.go) | dev/test/prod profiles |

---

## 8. So sánh với Testapp KV Executor

Testapp là **reference implementation đơn giản nhất** trong ev-node:

| Feature | Cosmos chain | Testapp KV |
|---------|-------------|-----------|
| **Runtime** | Cosmos SDK + CosmWasm | Key-value store đơn giản |
| **State tree** | IAVL (versioned Merkle) | go-datastore (flat KV) |
| **HeightProvider** | Yes | No |
| **Rollbackable** | Yes (IAVL LoadVersion) | Yes (delete keys > height) |
| **ExecPruner** | Yes | No |
| **Architecture** | 2 processes | 1 process (embedded) |
| **HTTP API** | Full REST + Swagger | Basic HTTP server |
| **Persistence** | PersistStore + IAVL LevelDB | go-datastore (LevelDB/Badger) |
| **Smart contracts** | CosmWasm | None |
| **SDK** | Full Go SDK | None |

**Testapp thiếu HeightProvider + ExecPruner** → không có crash recovery sync, không có automatic pruning. Cosmos chain đầy đủ hơn.

---

## 9. Tổng kết — Maturity Matrix

| Capability | ev-node | Cosmos chain builds | Cosmos total | EVM total |
|------------|---------|-------------------|-------------|-----------|
| **Core Executor (6 methods)** | Interface | Implementation | Complete | Complete |
| **HeightProvider** | Interface + Replayer | Implementation + HTTP endpoint | Complete | Complete |
| **Rollbackable** | Interface + Replayer | Implementation (IAVL) + HTTP endpoint | Complete | Complete |
| **ExecPruner** | Interface + Pruner loop | Implementation + HTTP endpoint | Complete | Complete |
| **Block Production** | Full | — (dùng nguyên) | Complete | Complete |
| **P2P Gossip** | Full | — (dùng nguyên) | Complete | Complete |
| **DA Submission** | Full | — (dùng nguyên) | Complete | Complete |
| **Node Sync** | Full | — (dùng nguyên) | Complete | Complete |
| **Crash Recovery** | Replayer framework | HeightProvider + Rollback | Complete | Complete |
| **Pruning** | Pruner loop | ExecPruner impl | Complete | Complete |
| **App HTTP API** | — | Full (20+ endpoints) | Complete | Partial (geth JSON-RPC) |
| **SDK Client** | — | Full Go SDK | Complete | External (ethers.js, ...) |
| **BlobStore** | — | Full (content-addressed + Merkle) | Complete | N/A |
| **DA Bridge (app-level)** | — | Full (Submit + Watch) | Complete | N/A |
| **Security Middleware** | — | Auth, CORS, rate limit | Complete | JWT only |
| **Raft HA** | Full | — (dùng nguyên) | Complete | Complete |
| **Gas estimation filter** | — | Not implemented | Missing | Complete |
| **Idempotent ExecuteTxs** | — | Not implemented | Missing | Complete |
| **OpenTelemetry tracing** | Framework support | Not implemented | Missing | Complete |

### Điểm mạnh cosmos chain:

1. **BlobStore + Merkle proof** — unique feature cho game/telemetry use case
2. **SDK client library** — developer experience tốt
3. **DABridge** — app-level DA integration (không chỉ block data)
4. **Security middleware** — production-ready HTTP server
5. **Full optional interfaces** — HeightProvider + Rollback + Prune — crash recovery hoàn chỉnh

### Còn thiếu / có thể cải thiện:

1. **FilterTxs gas estimation** — hiện chỉ filter by size, không estimate gas trước
2. **Idempotent ExecuteTxs** — nếu crash giữa ExecuteTxs, hiện phải rollback toàn bộ (không resume được)
3. **OpenTelemetry tracing** — cosmos-exec chưa có span tracing
4. **EVMStore-style metadata** — track execution stages per height cho robust crash recovery

---

## Source Code Reference

| | File | Mô tả |
|---|------|-------|
| **Cosmos chain** | | |
| Executor | [`apps/cosmos-exec/executor/executor.go`](apps/cosmos-exec/executor/executor.go) | 6 core + 3 optional + extras |
| BlobStore | [`apps/cosmos-exec/executor/blob_store.go`](apps/cosmos-exec/executor/blob_store.go) | Content-addressed storage |
| PersistStore | [`apps/cosmos-exec/executor/persist.go`](apps/cosmos-exec/executor/persist.go) | JSONL disk persistence |
| HTTP Server | [`apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go`](apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go) | 20+ HTTP endpoints |
| Config | [`apps/cosmos-exec/config/config.go`](apps/cosmos-exec/config/config.go) | Profile-based config |
| SDK | [`apps/cosmos-exec/sdk/cosmoswasm/`](apps/cosmos-exec/sdk/cosmoswasm/) | Full Go client SDK |
| Executor Client | [`apps/cosmos-wasm/cmd/executor_client.go`](apps/cosmos-wasm/cmd/executor_client.go) | Pure HTTP client |
| Node Entry | [`apps/cosmos-wasm/cmd/run.go`](apps/cosmos-wasm/cmd/run.go) | Node startup |
| **ev-node interfaces** | | |
| Executor Interface | [`core/execution/execution.go`](core/execution/execution.go) | 6 core + 3 optional interfaces |
| **Reference implementations** | | |
| EVM Executor | [`execution/evm/execution.go`](https://github.com/evstack/ev-node/blob/main/execution/evm/execution.go) | Engine API-based |
| EVM Store | [`execution/evm/store.go`](https://github.com/evstack/ev-node/blob/main/execution/evm/store.go) | ExecMeta tracking |
| Testapp KV | [`apps/testapp/kv/kvexecutor.go`](https://github.com/evstack/ev-node/blob/main/apps/testapp/kv/kvexecutor.go) | Simple KV store |
