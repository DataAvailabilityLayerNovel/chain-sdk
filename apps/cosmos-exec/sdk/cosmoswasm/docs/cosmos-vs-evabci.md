# Cosmos Chain vs ev-abci — So sánh chi tiết

So sánh giữa cosmos chain tự xây (`apps/cosmos-exec/` + `apps/cosmos-wasm/`) với [ev-abci](https://github.com/evstack/ev-abci) — adapter chính thức của Evolve cho ABCI applications.

> ⚠️ **Lưu ý refactor `ea844067`:** `DAClient`/`DABridge`, `BatchBuilder`,
> `EstimateCost`, `CommitRoot`, mock client và endpoint `/blob/submit` **đã bị
> gỡ**. Blob-first giờ qua `BlobClient` (JSON-RPC tới Celestia) +
> `SubmitBatch`/`BuildBatchRootTx`. Một số bảng dưới còn liệt kê các API cũ; xem
> [migration.md](migration.md) cho danh sách thay thế.

---

## Mục lục

1. [Tổng quan](#1-tổng-quan)
2. [Kiến trúc hệ thống](#2-kiến-trúc-hệ-thống)
3. [ABCI Integration — Cách gọi Cosmos SDK](#3-abci-integration--cách-gọi-cosmos-sdk)
4. [Executor Interface — 6 Core Methods](#4-executor-interface--6-core-methods)
5. [Optional Interfaces](#5-optional-interfaces)
6. [Mempool & Transaction Flow](#6-mempool--transaction-flow)
7. [RPC / API Layer](#7-rpc--api-layer)
8. [P2P Transaction Gossip](#8-p2p-transaction-gossip)
9. [State Management & Persistence](#9-state-management--persistence)
10. [Cosmos SDK Modules](#10-cosmos-sdk-modules)
11. [SDK Client Library](#11-sdk-client-library)
12. [BlobStore & Data Features](#12-blobstore--data-features)
13. [Validator & Signature Handling](#13-validator--signature-handling)
14. [Event System](#14-event-system)
15. [Security & Middleware](#15-security--middleware)
16. [Tổng kết — So sánh Matrix](#16-tổng-kết--so-sánh-matrix)

---

## 1. Tổng quan

| | ev-abci | Cosmos chain (của tôi) |
|---|---|---|
| **Repo** | [github.com/evstack/ev-abci](https://github.com/evstack/ev-abci) | `apps/cosmos-exec/` + `apps/cosmos-wasm/` |
| **Mục đích** | Adapter chung cho **bất kỳ** ABCI app nào chạy trên ev-node | Execution engine **chuyên biệt** cho CosmWasm + blob-first pattern |
| **Approach** | Generic ABCI bridge — wrap bất kỳ `abci.Application` | Direct Cosmos SDK integration — gọi trực tiếp keepers |
| **ABCI version** | ABCI 2.0 (FinalizeBlock) | ABCI 1.0 (BeginBlock/DeliverTx/EndBlock/Commit) |
| **Scope** | Adapter + Mempool + P2P + RPC + Store | Executor + BlobStore + SDK client + HTTP API + Persistence |
| **Target user** | Bất kỳ Cosmos SDK app (bank, staking, governance, IBC...) | CosmWasm smart contract apps (game, telemetry, DeFi) |
| **License** | Apache 2.0 | Proprietary |

### Khác biệt cốt lõi

**ev-abci** = **generic adapter** — nhận bất kỳ ABCI app, dịch ev-node executor calls sang ABCI calls. Không biết app bên trong chạy gì.

**Cosmos chain** = **specialized engine** — biết rõ bên trong là CosmWasm, xây thêm BlobStore, Merkle proofs, SDK client, HTTP API. Hy sinh tính generic để có features chuyên biệt.

```
ev-abci approach:
  ev-node → Adapter → [ANY ABCI App]
                       (black box)

Cosmos chain approach:
  ev-node → ExecutorClient → CosmosExecutor → App (CosmWasm)
                                  ↓
                             BlobStore, QuerySmart, InjectTx
                             (white box — biết bên trong)
```

---

## 2. Kiến trúc hệ thống

### ev-abci

```
┌──────────────────────────────────────────────────────────┐
│  ev-node (consensus + DA + P2P + block production)       │
│                                                          │
│  ┌─────────────────────────────────────────────────────┐ │
│  │  ev-abci Adapter (pkg/adapter/)                     │ │
│  │                                                     │ │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────────────┐  │ │
│  │  │ Mempool  │  │  Store   │  │  Event Bus       │  │ │
│  │  │ (CheckTx)│  │ (State,  │  │  (Block events,  │  │ │
│  │  │          │  │  ValSet) │  │   CometBFT-style)│  │ │
│  │  └──────────┘  └──────────┘  └──────────────────┘  │ │
│  └────────────────────┬────────────────────────────────┘ │
│                       │ ABCI 2.0                         │
│  ┌────────────────────┴────────────────────────────────┐ │
│  │  Any ABCI Application (Cosmos SDK, custom, ...)     │ │
│  └─────────────────────────────────────────────────────┘ │
│                                                          │
│  ┌──────────────┐  ┌──────────────┐                      │
│  │  P2P Gossip  │  │  RPC Server  │                      │
│  │  (tx gossip) │  │  (CometBFT   │                      │
│  │  (libp2p)    │  │   compatible)│                      │
│  └──────────────┘  └──────────────┘                      │
└──────────────────────────────────────────────────────────┘
```

**Đặc điểm:** Tất cả chạy trong **1 process**. ev-abci là library được embed vào ev-node binary.

### Cosmos chain

```
┌─────────────────────────────┐     ┌─────────────────────────────┐
│  cosmos-wasm (evcosmos)     │     │  cosmos-exec-grpc           │
│                             │     │                             │
│  ┌───────────┐              │     │  ┌───────────────────────┐  │
│  │ ev-node   │              │     │  │ CosmosExecutor        │  │
│  │ (sequencer│  ┌────────┐  │     │  │  ┌─────────────────┐  │  │
│  │  P2P, DA  │  │Executor│──┼─HTTP─┼──│  │ App (Cosmos SDK)│  │  │
│  │  block)   │  │Client  │  │     │  │  │  wasm, bank,    │  │  │
│  └───────────┘  └────────┘  │     │  │  │  auth, IBC      │  │  │
│                             │     │  │  └─────────────────┘  │  │
│                             │     │  │  ┌─────────────────┐  │  │
│                             │     │  │  │ BlobStore       │  │  │
│                             │     │  │  │ PersistStore    │  │  │
│                             │     │  │  └─────────────────┘  │  │
│                             │     │  └───────────────────────┘  │
│                             │     │  ┌───────────────────────┐  │
│                             │     │  │ HTTP API + Swagger    │  │
│                             │     │  │ Security middleware   │  │
│                             │     │  └───────────────────────┘  │
└─────────────────────────────┘     └─────────────────────────────┘
     Process 1                           Process 2
```

**Đặc điểm:** **2 processes** tách biệt, giao tiếp qua HTTP/Connect-RPC.

### So sánh kiến trúc

| Aspect | ev-abci | Cosmos chain |
|--------|---------|-------------|
| **Số process** | 1 (embedded) | 2 (tách biệt) |
| **Communication** | In-process function call | HTTP + Connect-RPC JSON |
| **Latency** | ~0 (direct call) | ~1-5ms (HTTP round-trip) |
| **Restart independence** | Không — restart = restart tất cả | Có — restart EL không mất P2P/DA state |
| **Resource isolation** | Không — share memory | Có — mỗi process resource riêng |
| **Debug** | 1 log stream | 2 log streams tách biệt |

---

## 3. ABCI Integration — Cách gọi Cosmos SDK

### ev-abci — ABCI 2.0 (FinalizeBlock)

ev-abci dùng **ABCI 2.0** interface — Cosmos SDK v0.50+ với `FinalizeBlock` thay cho BeginBlock/DeliverTx/EndBlock:

```go
// ev-abci: adapter.go — ExecuteTxs()
func (a *Adapter) ExecuteTxs(ctx context.Context, txs [][]byte, blockHeight uint64, timestamp time.Time, _ []byte) ([]byte, error) {
    // 1. PrepareProposal — let app reorder/filter txs
    prepareResp := a.app.PrepareProposal(abci.RequestPrepareProposal{
        Txs: txs,
        MaxTxBytes: maxBytes,
        Height: blockHeight,
    })

    // 2. ProcessProposal — validate proposed block
    processResp := a.app.ProcessProposal(abci.RequestProcessProposal{
        Txs: prepareResp.Txs,
        Height: blockHeight,
    })

    // 3. FinalizeBlock — execute all txs in one call
    finalizeResp := a.app.FinalizeBlock(abci.RequestFinalizeBlock{
        Txs: prepareResp.Txs,
        Height: blockHeight,
        Time: timestamp,
        Hash: headerHash,
        DecidedLastCommit: lastCommit,
    })

    // 4. Commit
    a.app.Commit()
    return finalizeResp.AppHash, nil
}
```

**Flow:** `PrepareProposal` → `ProcessProposal` → `FinalizeBlock` → `Commit`

### Cosmos chain — ABCI 1.0 (BeginBlock/DeliverTx)

Cosmos chain dùng **ABCI 1.0** — gọi trực tiếp `baseapp.BaseApp` methods:

```go
// cosmos chain: executor.go — ExecuteTxs()
func (e *CosmosExecutor) ExecuteTxs(ctx context.Context, txs [][]byte, blockHeight uint64, timestamp time.Time, prevStateRoot []byte) ([]byte, error) {
    header := tmproto.Header{
        ChainID: e.chainID,
        Height:  int64(blockHeight),
        Time:    timestamp,
    }

    // 1. BeginBlock
    e.app.BeginBlock(abci.RequestBeginBlock{Header: header})

    // 2. DeliverTx × N — one by one
    for _, tx := range txs {
        result := e.app.DeliverTx(abci.RequestDeliverTx{Tx: tx})
        // store result per tx
    }

    // 3. EndBlock
    e.app.EndBlock(abci.RequestEndBlock{Height: int64(blockHeight)})

    // 4. Commit
    commitRes := e.app.Commit()
    return commitRes.Data, nil  // IAVL state root
}
```

**Flow:** `BeginBlock` → `DeliverTx × N` → `EndBlock` → `Commit`

### So sánh ABCI

| Aspect | ev-abci (ABCI 2.0) | Cosmos chain (ABCI 1.0) |
|--------|-------------------|------------------------|
| **ABCI version** | v2 (FinalizeBlock) | v1 (BeginBlock/DeliverTx/EndBlock) |
| **Cosmos SDK version** | v0.50+ | v0.47 (legacy) |
| **PrepareProposal** | Có — app có thể reorder/filter txs | Không — txs giữ nguyên thứ tự từ sequencer |
| **ProcessProposal** | Có — app validate proposed block | Không |
| **Tx execution** | Batch trong FinalizeBlock | Từng tx qua DeliverTx |
| **Per-tx result** | Trả về trong FinalizeBlock response | Trả về từng DeliverTx response |
| **Vote Extensions** | Không hỗ trợ (ev-node single sequencer) | Không có (ABCI v1) |

**Hệ quả:**
- ev-abci: app có quyền **reorder transactions** (PrepareProposal). Mạnh hơn cho MEV protection, tx prioritization
- Cosmos chain: txs giữ **nguyên thứ tự** từ sequencer. Đơn giản hơn, predictable hơn

---

## 4. Executor Interface — 6 Core Methods

| Method | ev-abci | Cosmos chain | Khác biệt |
|--------|---------|-------------|-----------|
| `InitChain` | Gọi `app.InitChain()` + load genesis doc + init validators | Gọi `app.InitChain()` + idempotency check (skip nếu đã init) | ev-abci load genesis từ file, cosmos chain dùng default genesis |
| `GetTxs` | `mempool.ReapMaxBytesMaxGas()` — giống CometBFT mempool | `e.mempool = e.mempool[:0]` — drain in-memory slice | ev-abci dùng CometBFT mempool (mature), cosmos chain dùng simple slice |
| `ExecuteTxs` | PrepareProposal → ProcessProposal → FinalizeBlock → Commit | BeginBlock → DeliverTx × N → EndBlock → Commit | ABCI v2 vs v1 (xem section 3) |
| `SetFinal` | Publish queued block events | Update `finalizedHeight` + persist metadata | ev-abci dùng event system, cosmos chain dùng simple height tracking |
| `GetExecutionInfo` | Đọc `MaxGas` từ consensus params (app-defined) | Return `MaxGas: 0` (no gas limit) | ev-abci gas-aware, cosmos chain bỏ qua gas ở sequencer level |
| `FilterTxs` | Check gas + size + validity per tx | Chỉ check cumulative size | ev-abci filter chính xác hơn |

### Chi tiết: GetTxs — Mempool khác biệt lớn

**ev-abci:**
```go
// Dùng CometBFT-style mempool
func (a *Adapter) GetTxs(ctx context.Context) ([][]byte, error) {
    txs := a.mempool.ReapMaxBytesMaxGas(maxBytes, maxGas)
    return txs, nil
}
// Mempool có:
// - CheckTx validation (ABCI call)
// - Gas tracking per tx
// - Priority ordering
// - Duplicate detection
// - Eviction policy
```

**Cosmos chain:**
```go
// Simple in-memory slice
func (e *CosmosExecutor) GetTxs(ctx context.Context) ([][]byte, error) {
    e.mu.Lock()
    defer e.mu.Unlock()
    txs := make([][]byte, len(e.mempool))
    copy(txs, e.mempool)
    e.mempool = e.mempool[:0]  // drain
    return txs, nil
}
// Mempool chỉ là [][]byte
// - Không CheckTx
// - Không gas tracking
// - FIFO ordering
// - Không dedup
```

**Hệ quả:** ev-abci reject invalid tx sớm (CheckTx). Cosmos chain chấp nhận mọi tx vào mempool, chỉ reject tại DeliverTx (tx vẫn vào block nhưng fail).

---

## 5. Optional Interfaces

| Interface | ev-abci | Cosmos chain |
|-----------|---------|-------------|
| `HeightProvider` | **Không** implement | **Có** — `GetLatestHeight()` trả `e.lastHeight` |
| `Rollbackable` | **Không** implement | **Có** — `Rollback()` dùng IAVL `LoadVersion()` |
| `ExecPruner` | **Có** — `PruneExec()` | **Có** — `PruneExec()` delete old blocks/txResults maps |

**Hệ quả quan trọng:**

| Scenario | ev-abci | Cosmos chain |
|----------|---------|-------------|
| Crash recovery (EL ahead of CL) | Không auto-rollback — phải manual fix | Auto-rollback qua `Rollback()` |
| Height sync check | Không detect mismatch | Detect mismatch qua `GetLatestHeight()` |
| Replay after crash | Dựa vào ABCI app's internal state | Replayer gọi HeightProvider → detect → rollback/replay |

Cosmos chain có **crash recovery hoàn chỉnh** (HeightProvider + Rollbackable). ev-abci chỉ có pruning.

---

## 6. Mempool & Transaction Flow

### ev-abci — Full CometBFT Mempool

```
Client → P2P Gossip → Mempool
                        │
                        ├── CheckTx (ABCI call) → validate tx
                        ├── Store if valid
                        ├── Reject if invalid
                        │
                        └── ReapMaxBytesMaxGas() → GetTxs()
                                                    │
                                                    ▼
                                            PrepareProposal
                                                    │
                                                    ▼
                                            ProcessProposal
                                                    │
                                                    ▼
                                            FinalizeBlock
```

**Features:**
- CheckTx validation trước khi vào mempool
- P2P tx gossip giữa các nodes (libp2p)
- Gas-aware reaping (tính gas per tx)
- mempool_ids tracking cho P2P deduplication

### Cosmos chain — Simple Push/Pull

```
User → HTTP POST /tx/submit → InjectTx() → mempool [][]byte
                                                │
    ev-node Reaper → GetTxs() → drain ─────────┘
                      │
                      ▼
              Sequencer.SubmitTx()
                      │
                      ▼
              ProduceBlock → ExecuteTxs
                              │
                              ▼
                      BeginBlock → DeliverTx × N → EndBlock → Commit
```

**Features:**
- Không CheckTx — accept tất cả
- Không P2P tx gossip (dùng HTTP push)
- Simple FIFO (không priority)
- InjectTx = push-based, GetTxs = pull-based

### So sánh

| Feature | ev-abci | Cosmos chain |
|---------|---------|-------------|
| **CheckTx** | Có — ABCI validation | Không |
| **P2P tx gossip** | Có — libp2p GossipSub | Không — HTTP only |
| **Gas tracking** | Có — per-tx gas estimation | Không |
| **Priority** | CometBFT mempool priority | FIFO |
| **Dedup** | mempool_ids | Không |
| **Tx injection** | P2P gossip | HTTP endpoint |
| **Invalid tx handling** | Reject trước block | Vào block nhưng fail (Code ≠ 0) |

---

## 7. RPC / API Layer

### ev-abci — CometBFT-Compatible RPC

ev-abci cung cấp **CometBFT-compatible JSON-RPC server** — tương thích với các tool Cosmos ecosystem:

```
pkg/rpc/
  ├── server.go      ← HTTP + WebSocket RPC server
  └── core/          ← CometBFT RPC method implementations
```

**Endpoints (CometBFT standard):**

| Method | Mô tả |
|--------|-------|
| `broadcast_tx_sync` | Submit tx, wait for CheckTx |
| `broadcast_tx_async` | Submit tx, return immediately |
| `broadcast_tx_commit` | Submit tx, wait for block inclusion |
| `abci_query` | Query app state (ABCI Query) |
| `block` | Get block by height |
| `block_results` | Get block execution results |
| `status` | Node status |
| `tx` | Get tx by hash |
| `validators` | Get validator set |
| `health` | Health check |

**Lợi ích:** Tương thích với **Keplr wallet, CosmJS, Cosmos SDK CLI** — bất kỳ tool nào dùng CometBFT RPC đều work.

### Cosmos chain — Custom HTTP REST API

Cosmos chain xây HTTP server riêng:

```
cmd/cosmos-exec-grpc/
  ├── main.go         ← Handler registration
  ├── middleware.go   ← Security (auth, CORS, rate limit)
  ├── metrics.go      ← Prometheus
  └── swagger.go      ← OpenAPI docs
```

**Endpoints (custom):**

| Method | Path | Mô tả |
|--------|------|-------|
| POST | `/tx/submit` | Submit tx bytes |
| GET | `/tx/{hash}` | Get tx result |
| GET | `/tx/result?hash=...` | Get tx result (query param) |
| POST | `/wasm/query-smart` | WASM smart query |
| GET | `/auth/account/{address}` | Account number/sequence |
| GET | `/bank/balance/{address}` | Số dư |
| GET | `/status` | Chain status |
| GET | `/exec/height` | EL current height |
| POST | `/exec/rollback` | Manual rollback |
| POST | `/exec/prune` | Manual prune |
| GET | `/swagger` | Swagger UI |
| GET | `/metrics` | Prometheus metrics |

### So sánh

| Aspect | ev-abci RPC | Cosmos chain HTTP API |
|--------|-------------|----------------------|
| **Protocol** | CometBFT JSON-RPC | REST HTTP + Connect-RPC |
| **Compatibility** | Keplr, CosmJS, cosmos CLI | Custom SDK client only |
| **Swagger/OpenAPI** | Không | Có |
| **Auth** | Không | Bearer token |
| **Rate limiting** | Không | Có (per-IP) |
| **CORS** | Không | Configurable |
| **Blob endpoints** | Không (generic ABCI) | Có (submit, retrieve, batch) |
| **Smart query** | Qua `abci_query` (generic) | `POST /query/smart` (typed, gas-limited) |
| **Metrics** | Adapter metrics | Full Prometheus |
| **WebSocket** | Có (CometBFT events) | Không |

**Trade-off:**
- ev-abci: ecosystem compatibility (Keplr, CosmJS work out of the box)
- Cosmos chain: better DX (Swagger, typed endpoints, security), nhưng cần custom SDK client

---

## 8. P2P Transaction Gossip

### ev-abci — Built-in Tx Gossip

ev-abci có **P2P transaction gossip** riêng, tách biệt khỏi ev-node's block gossip:

```go
// pkg/p2p/ — Transaction gossip via libp2p
// Nodes gossip pending txs to each other
// → mempool stays in sync across nodes
// → CheckTx validation before gossip
```

**Flow:** Node A nhận tx → CheckTx valid → gossip tới Node B, C → Node B, C add vào mempool

### Cosmos chain — Không có Tx Gossip

Cosmos chain **không** có P2P tx gossip:
- Tx chỉ submit qua HTTP endpoint của aggregator node
- Full nodes không nhận tx — chỉ nhận blocks qua ev-node's P2P (header/data topics)

**Hệ quả:**
- ev-abci: multi-node mempool sync, tx availability cao hơn
- Cosmos chain: single point of tx submission (aggregator only), đơn giản hơn

---

## 9. State Management & Persistence

### ev-abci — Store Package

```go
// pkg/store/ — Persistent state
type Store struct {
    // Validator sets (per height)
    // Consensus parameters
    // Block metadata (BlockID, commits)
    // Last committed state
}
```

ev-abci lưu:
- Validator set per height (cho signature verification)
- Consensus parameters (block size, gas limit)
- Last commit info (cho FinalizeBlock)
- Block IDs (cho event publishing)

### Cosmos chain — PersistStore + IAVL

```
~/.cosmos-exec-grpc/data/
  ├── metadata.json     ← ChainMetadata (overwrite mỗi block)
  ├── tx_results.jsonl  ← append-only tx execution results
  └── blocks.jsonl      ← append-only block info
  # (không có blobs.jsonl — blob lưu trên Celestia qua BlobClient)

+ IAVL tree (LevelDB)   ← Cosmos SDK managed state
```

Cosmos chain lưu:
- Execution metadata (height, stateRoot, chainID, finalizedHeight)
- Tx results per hash (code, gas, events, log)
- Block info per height
- Blob data (content-addressed)
- IAVL versioned state tree (Cosmos SDK managed)

### So sánh

| Aspect | ev-abci | Cosmos chain |
|--------|---------|-------------|
| **Validator tracking** | Có (per-height) | Không (sovereign rollup, single sequencer) |
| **Consensus params** | Có (app-defined) | Không (MaxGas = 0, no params) |
| **Tx results** | Qua ABCI events | Custom `txResults map[string]TxResult` |
| **Blob storage** | Không | BlobStore (in-memory + JSONL persist) |
| **State tree** | ABCI app manages | IAVL (explicit, version-aware rollback) |
| **Persistence format** | Custom binary store | JSONL (human-readable, append-only) |

---

## 10. Cosmos SDK Modules

### ev-abci — Custom Modules

ev-abci cung cấp **optional Cosmos SDK modules** để thay thế modules không tương thích với sovereign rollup:

| Module | Mô tả |
|--------|-------|
| **Staking (wrapper)** | Wrap standard staking — no-op cho slashing, jailing, validator updates (không có validator set trong ev-node) |
| **Migration Manager** | Quản lý validator updates, define attesters/sequencers, hỗ trợ CometBFT → Evolve migration |
| **Network** | Network-related functionality |
| **Proto** | Protobuf definitions cho custom modules |

**Use case:** Migrate existing CometBFT chain sang Evolve — `migrationmngr` giúp chuyển validator set → sequencer config tại block height xác định.

### Cosmos chain — Stub Keepers

Cosmos chain dùng approach đơn giản hơn — stub keepers trong `wasm_deps.go`:

```go
// apps/cosmos-exec/app/wasm_deps.go
type noopStakingKeeper struct{}        // No-op staking
type noopDistributionKeeper struct{}   // No-op distribution
type ibcClientStakingKeeper struct{}   // Minimal IBC staking
type ibcClientUpgradeKeeper struct{}   // Minimal IBC upgrade
```

| Module | Cosmos chain |
|--------|-------------|
| auth | Full (AccountKeeper) |
| bank | Full (BankKeeper) |
| wasm | Full (WasmKeeper) — **core module** |
| IBC | Full (IBCKeeper + TransferKeeper) |
| params | Full (ParamsKeeper) |
| capability | Full (CapabilityKeeper) |
| staking | Stub (noopStakingKeeper) |
| distribution | Stub (noopDistributionKeeper) |

### So sánh

| Aspect | ev-abci | Cosmos chain |
|--------|---------|-------------|
| **Staking handling** | Wrapper module (disable slashing/jailing) | Simple stub (no-op) |
| **Migration support** | Có (CometBFT → Evolve migration) | Không |
| **Custom modules** | 4 modules (staking, migration, network, proto) | 0 custom modules (chỉ stubs) |
| **CosmWasm** | Tuỳ app bên trong | Core module, trực tiếp integrate |
| **IBC** | Tuỳ app | Full support |

---

## 11. SDK Client Library

### ev-abci — Không có SDK

ev-abci **không cung cấp SDK client library**. Users tương tác qua:
- CometBFT RPC (standard tools: CosmJS, cosmos CLI, Keplr)
- Direct ABCI query

### Cosmos chain — Full Go SDK

Cosmos chain có **SDK package hoàn chỉnh** (`sdk/cosmoswasm/`):

| Component | Mô tả |
|-----------|-------|
| Client | `NewClient(url)` → SubmitBlob, CommitRoot, QuerySmart |
| Tx builders | BuildStoreTx, BuildExecuteTx, BuildBlobCommitTx, ... |
| BatchBuilder | Auto-flush accumulator cho batch operations |
| Merkle proof | BuildMerkleProof, VerifyMerkleProof |
| DABridge | App-level DA integration (Submit + Watch) |
| DAClient | Celestia namespace access |
| Mock | MockExecutorClient, MockDAClient |
| Cost | EstimateCost (on-chain vs blob-first) |
| Chain | StartDALChain (programmatic chain runner) |
| 3 examples | my-counter → game-telemetry → forced-inclusion |

**33 Go files**, 5 internal packages, 12 test files, 3 examples.

### So sánh

| Aspect | ev-abci | Cosmos chain |
|--------|---------|-------------|
| **Client SDK** | Không (dùng CosmJS/CLI) | Full Go SDK (33 files) |
| **Tx builders** | Không (CosmJS handles) | 5 builders (Store, Instantiate, Execute, BlobCommit, BatchRoot) |
| **Mock testing** | Không | MockExecutorClient + MockDAClient |
| **Examples** | Không | 3 runnable examples (my-counter, forced-inclusion, game-telemetry) |
| **API tiers** | N/A | Tier 1 (Core) / Tier 2 (Power-user) / Tier 3 (Dev) |
| **Documentation** | README | 12 doc files |

---

## 12. BlobStore & Data Features

### ev-abci — Không có

ev-abci là generic adapter — **không có BlobStore, Merkle proofs, chunking, compression, DA bridge**.

App bên trong có thể tự implement, nhưng ev-abci không cung cấp.

### Cosmos chain — Full Data Layer

| Feature | File | Mô tả |
|---------|------|-------|
| BlobStore | `executor/blob_store.go` | Content-addressed (SHA-256), 4MB/blob, 256MB total |
| PersistStore | `executor/persist.go` | JSONL persistence cho blobs, txResults, blocks |
| Merkle proofs | `sdk/cosmoswasm/proof.go` | Binary SHA-256 Merkle tree, offline verification |
| Chunking | `sdk/cosmoswasm/chunk.go` | Split large blobs into DA-sized chunks |
| Compression | `sdk/cosmoswasm/compress.go` | Gzip, conditional compression |
| Cost estimation | `sdk/cosmoswasm/cost.go` | On-chain vs blob-first gas comparison |
| DABridge | `sdk/cosmoswasm/da_bridge.go` | App-level DA (Submit + Commit + Watch) |
| Batch | `sdk/cosmoswasm/batch.go` | Auto-flush batch accumulator |

**Đây là điểm khác biệt lớn nhất.** Cosmos chain xây toàn bộ data layer cho blob-first pattern — ev-abci không có concept này.

---

## 13. Validator & Signature Handling

### ev-abci — Full Validator Management

ev-abci có hệ thống validator/signature phức tạp:

```go
// pkg/adapter/providers.go

// Tạo signature bytes cho sequencer node (aggregator)
SequencerNodeSignatureBytesProvider(adapter) → func(header) → signatureBytes

// Tạo signature bytes cho sync node (full node)
SyncNodeSignatureBytesProvider(adapter) → func(header, txs) → signatureBytes

// Hash validator set (dùng CometBFT format)
ValidatorHasherProvider() → func(seqAddr, pubKey) → hash

// Hash từ multiple validators
ValidatorsHasher(pubKeys, seqAddr) → hash

// Hash từ store (persisted validators)
ValidatorHasherFromStoreProvider(store) → func() → hash
```

**Mục đích:** Compatibility với CometBFT's signature/validator model — cần cho `LastCommitInfo` trong FinalizeBlock.

### Cosmos chain — Minimal

Cosmos chain **không có validator management**:
- Single sequencer (aggregator key)
- Signature bởi ev-node framework (Ed25519)
- Không track validator set
- noopStakingKeeper

### So sánh

| Aspect | ev-abci | Cosmos chain |
|--------|---------|-------------|
| **Validator tracking** | Per-height validator set | Không |
| **Signature providers** | Sequencer + Sync node | Không (ev-node handles) |
| **Validator hashing** | CometBFT-compatible | Không |
| **Multi-validator** | Có support | Single sequencer only |
| **CometBFT migration** | Hỗ trợ (validator set transition) | Không |

---

## 14. Event System

### ev-abci — CometBFT Event Bus

ev-abci implement **CometBFT event bus** — publish block events sau SetFinal:

```go
// adapter.go
type StackedEvent struct {
    blockID           types.BlockID
    block             *types.Block
    abciResponse      *abci.ResponseFinalizeBlock
    validatorUpdates  []*types.Validator
}

// SetFinal() → publishQueuedBlockEvents() → fireEvents()
// Events: NewBlock, NewBlockHeader, Tx events
```

**Lợi ích:** WebSocket subscribers (Keplr, indexers) nhận real-time block events — CometBFT standard.

### Cosmos chain — Không có Event Bus

Cosmos chain **không** có event bus. Tx events lưu trong `txResults` map, query qua HTTP:
- `GET /tx/{hash}` → events trong response
- Không có WebSocket subscription
- Không có real-time notification

---

## 15. Security & Middleware

| Feature | ev-abci | Cosmos chain |
|---------|---------|-------------|
| **Auth** | Không | Bearer token |
| **CORS** | Không | Configurable |
| **Rate limiting** | Không | Per-IP RPS |
| **Max body size** | Không | Configurable (default 10MB) |
| **Read-only mode** | Không | Có |
| **Metrics** | Adapter metrics | Full Prometheus |
| **Health check** | Qua RPC `/health` | `GET /status` |
| **Swagger/OpenAPI** | Không | Có |
| **Config profiles** | Không | dev / test / prod |

---

## 16. Tổng kết — So sánh Matrix

| Capability | ev-abci | Cosmos chain | Ai tốt hơn? |
|------------|---------|-------------|-------------|
| **Generic ABCI support** | Bất kỳ ABCI app | Chỉ CosmWasm | ev-abci |
| **ABCI version** | v2 (FinalizeBlock) | v1 (BeginBlock/DeliverTx) | ev-abci (modern) |
| **PrepareProposal** | Có | Không | ev-abci |
| **ProcessProposal** | Có | Không | ev-abci |
| **CometBFT RPC** | Full compatible | Không | ev-abci |
| **Keplr/CosmJS support** | Out of the box | Cần custom client | ev-abci |
| **P2P tx gossip** | Có | Không | ev-abci |
| **Mempool (CheckTx)** | CometBFT mempool | Simple slice | ev-abci |
| **Validator management** | Full | Không | ev-abci |
| **Event bus (WebSocket)** | CometBFT standard | Không | ev-abci |
| **CometBFT migration** | Hỗ trợ | Không | ev-abci |
| | | | |
| **HeightProvider** | Không | Có | Cosmos chain |
| **Rollbackable** | Không | Có (IAVL) | Cosmos chain |
| **Crash recovery** | Partial | Hoàn chỉnh | Cosmos chain |
| **BlobStore** | Không | Full (SHA-256, Merkle) | Cosmos chain |
| **Merkle proofs** | Không | Full (build + verify offline) | Cosmos chain |
| **SDK client** | Không | 33 files, 3 examples | Cosmos chain |
| **Tx builders** | Không | 5 builders | Cosmos chain |
| **DABridge (app-level)** | Không | Full (Submit + Watch) | Cosmos chain |
| **Chunking + Compression** | Không | Có | Cosmos chain |
| **Cost estimation** | Không | Có | Cosmos chain |
| **HTTP API + Swagger** | Không | Full | Cosmos chain |
| **Security middleware** | Không | Auth, CORS, rate limit | Cosmos chain |
| **Prometheus metrics** | Adapter only | Full | Cosmos chain |
| **Config profiles** | Không | dev/test/prod | Cosmos chain |
| **Mock testing** | Không | MockExecutor + MockDA | Cosmos chain |
| **Process isolation** | 1 process | 2 processes | Cosmos chain |

### Kết luận

**ev-abci** mạnh ở **ecosystem compatibility** — CometBFT RPC, Keplr, CosmJS, P2P tx gossip, validator management, ABCI v2 — tất cả đều chuẩn Cosmos ecosystem. Phù hợp khi muốn chạy **existing Cosmos SDK app** trên ev-node mà **không cần modify**.

**Cosmos chain** mạnh ở **specialized features** — BlobStore, Merkle proofs, SDK client, DABridge, security middleware — tất cả phục vụ **blob-first pattern** cho game/telemetry use case. Có crash recovery hoàn chỉnh (HeightProvider + Rollbackable). Phù hợp khi xây **app mới từ đầu** với requirements cụ thể.

| Chọn | Khi nào |
|------|---------|
| **ev-abci** | Có sẵn Cosmos SDK app, muốn migrate sang ev-node. Cần Keplr/CosmJS support. Cần P2P tx gossip |
| **Cosmos chain** | Xây app mới, cần blob-first pattern. Cần off-chain data + on-chain commitment. Cần SDK cho developers. Cần security middleware |
| **Kết hợp** | Dùng ev-abci cho ABCI compatibility + thêm cosmos chain's BlobStore/SDK features |

---

## Source Code Reference

| | Location | Mô tả |
|---|---------|-------|
| **ev-abci** | | |
| Adapter | [`pkg/adapter/adapter.go`](https://github.com/evstack/ev-abci/blob/main/pkg/adapter/adapter.go) | Core executor + ABCI bridge |
| Providers | [`pkg/adapter/providers.go`](https://github.com/evstack/ev-abci/blob/main/pkg/adapter/providers.go) | Signature + validator providers |
| Convert | [`pkg/adapter/convert.go`](https://github.com/evstack/ev-abci/blob/main/pkg/adapter/convert.go) | ABCI type conversions |
| Mempool | [`pkg/adapter/mempool_ids.go`](https://github.com/evstack/ev-abci/blob/main/pkg/adapter/mempool_ids.go) | Mempool ID tracking |
| Store | [`pkg/store/`](https://github.com/evstack/ev-abci/tree/main/pkg/store) | State persistence |
| RPC | [`pkg/rpc/`](https://github.com/evstack/ev-abci/tree/main/pkg/rpc) | CometBFT-compatible RPC |
| P2P | [`pkg/p2p/`](https://github.com/evstack/ev-abci/tree/main/pkg/p2p) | Transaction gossip |
| Modules | [`modules/`](https://github.com/evstack/ev-abci/tree/main/modules) | Staking wrapper, migration manager |
| Server | [`server/`](https://github.com/evstack/ev-abci/tree/main/server) | Startup, CLI commands |
| **Cosmos chain** | | |
| Executor | [`apps/cosmos-exec/executor/executor.go`](apps/cosmos-exec/executor/executor.go) | 6 core + 3 optional + extras |
| BlobStore | [`apps/cosmos-exec/executor/blob_store.go`](apps/cosmos-exec/executor/blob_store.go) | Content-addressed storage |
| PersistStore | [`apps/cosmos-exec/executor/persist.go`](apps/cosmos-exec/executor/persist.go) | JSONL persistence |
| App | [`apps/cosmos-exec/app/app.go`](apps/cosmos-exec/app/app.go) | Cosmos SDK + CosmWasm |
| HTTP Server | [`apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go`](apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go) | REST API + middleware |
| SDK | [`apps/cosmos-exec/sdk/cosmoswasm/`](apps/cosmos-exec/sdk/cosmoswasm/) | Go client SDK |
| Client | [`apps/cosmos-wasm/cmd/executor_client.go`](apps/cosmos-wasm/cmd/executor_client.go) | Pure HTTP executor client |
