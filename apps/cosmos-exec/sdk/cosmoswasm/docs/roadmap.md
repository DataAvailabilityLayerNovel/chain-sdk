# Learning Roadmap — Đọc hiểu toàn bộ Cosmos/CosmWasm Chain

Roadmap từ zero đến hiểu toàn bộ hệ thống cosmos chain trên ev-node framework. Mỗi phase có mục tiêu rõ ràng, file cần đọc, và kiến thức đạt được.

> ⚠️ **Lưu ý refactor `ea844067`:** các mục nhắc `commit.go`/`CommitRoot`,
> `batch.go`/`BatchBuilder`, `da_bridge.go`/`DABridge`, `da_client.go`/`DAClient`,
> `EstimateCost`, hay `/blob/submit` là **API/file cũ đã bị gỡ**. API blob-first
> hiện tại: `blob.go` → `BlobClient` (`SubmitBlob`/`SubmitBatch`/`RetrieveBlob`),
> `merkle.go` (`BuildMerkleProof`). Xem [migration.md](migration.md).

## Mục lục

- Tổng quan roadmap
- Phase 1: Foundation — Core Interfaces & Concepts
- Phase 2: Execution Layer — CosmosExecutor
- Phase 3: Framework Engine — ev-node Internals
- Phase 4: Integration — 2-Process Wiring
- Phase 5: SDK & Extensions
- Phase 6: Operations — Production Readiness
- Dependency Map
- Quick Reference — Tất cả docs hiện có
- Checklist — Tự đánh giá

---

## Tổng quan roadmap

```
Phase 1: Foundation          ▓▓░░░░░░░░  (~2 ngày)
  └─ ev-node core interfaces + Cosmos SDK basics

Phase 2: Execution Layer     ▓▓▓▓░░░░░░  (~3 ngày)
  └─ CosmosExecutor + App + ABCI flow

Phase 3: Framework Engine    ▓▓▓▓▓▓░░░░  (~4 ngày)
  └─ Block production, P2P, DA, Sync

Phase 4: Integration         ▓▓▓▓▓▓▓▓░░  (~2 ngày)
  └─ 2-process wiring, executor client, node startup

Phase 5: SDK & Extensions    ▓▓▓▓▓▓▓▓▓░  (~2 ngày)
  └─ Client SDK, BlobStore, DABridge, Merkle proofs

Phase 6: Operations          ▓▓▓▓▓▓▓▓▓▓  (~1 ngày)
  └─ Config, security, monitoring, crash recovery
```

**Tổng:** ~14 ngày đọc chuyên sâu (đọc code + chạy thử + hiểu nguyên lý).

---

## Phase 1: Foundation — Core Interfaces & Concepts

**Mục tiêu:** Hiểu kiến trúc tổng thể, biết sovereign rollup là gì, hiểu execution/consensus separation.

### 1.1. Sovereign Rollup Concept

**Đọc:**
- [chain-flow.md — Section 1](chain-flow.md) — Kiến trúc 2 process
- [cosmos-vs-evnode.md — Section 1](cosmos-vs-evnode.md) — Tổng quan

**Kiến thức cần nắm:**
- Sovereign rollup = rollup có full verification bởi full node, không dùng L1 smart contract verify
- ev-node = framework cung cấp: sequencer, P2P, DA, block production
- Execution layer = pluggable (Cosmos, EVM, custom)
- 2-process model: consensus process (cosmos-wasm) ↔ execution process (cosmos-exec-grpc)

### 1.2. Core Executor Interface — 6 Methods

**Đọc:**
- [`core/execution/execution.go`](../../../../core/execution/execution.go) — **ĐỌC KỸ TOÀN BỘ FILE**

**6 core methods cần hiểu:**

| Method | Một câu mô tả |
|--------|---------------|
| `InitChain` | Khởi tạo chain state lần đầu (genesis) |
| `GetTxs` | Lấy transactions từ mempool |
| `ExecuteTxs` | Chạy N transactions → trả stateRoot mới |
| `SetFinal` | Đánh dấu block là finalized |
| `GetExecutionInfo` | Trả MaxGas (cosmos = 0) |
| `FilterTxs` | Lọc tx theo size/gas trước khi vào block |

**Kiến thức cần nắm:**
- Mỗi method có contract gì (idempotency, determinism, error handling)
- `FilterStatus`: OK / Remove / Postpone
- `ExecutionInfo.MaxGas = 0` nghĩa là execution layer không enforce gas ở sequencer level

### 1.3. Optional Interfaces — 3 Methods

**Đọc:** Cùng file [`core/execution/execution.go`](../../../../core/execution/execution.go) — phần dưới

| Interface | Method | Một câu mô tả |
|-----------|--------|---------------|
| `HeightProvider` | `GetLatestHeight()` | Cho framework biết EL đang ở height nào (crash recovery) |
| `Rollbackable` | `Rollback(targetHeight)` | Rollback EL về height cũ nếu EL ahead sau crash |
| `ExecPruner` | `PruneExec(height)` | Dọn dẹp execution metadata cũ |

**Kiến thức cần nắm:**
- Đây là Go type assertion tại runtime (`exec.(HeightProvider)`) — không bắt buộc implement
- HeightProvider + Rollbackable = crash recovery hoàn chỉnh
- ExecPruner = garbage collection cho execution layer

### 1.4. Cosmos SDK Basics (nếu chưa biết)

**Đọc ngoài:**
- ABCI (Application BlockChain Interface) — cách Cosmos SDK nhận block từ consensus
- Module pattern: mỗi feature = 1 module (auth, bank, wasm)
- Keeper pattern: mỗi module có keeper giữ state
- IAVL tree: versioned Merkle tree cho state storage
- Protobuf transaction encoding

**Không cần đọc sâu:** Tendermint consensus (vì ev-node thay thế Tendermint hoàn toàn)

---

## Phase 2: Execution Layer — CosmosExecutor

**Mục tiêu:** Hiểu execution engine xử lý transactions như thế nào, ABCI flow, state management.

### 2.1. Cosmos SDK App

**Đọc:**
- [`apps/cosmos-exec/app/app.go`](../../../app/app.go) — App struct, module initialization
- [`apps/cosmos-exec/app/wasm_deps.go`](../../../app/wasm_deps.go) — Stub keepers cho sovereign rollup

**Kiến thức cần nắm:**
- App wrap `baseapp.BaseApp` + khởi tạo modules
- Modules: auth, bank, wasm, IBC, params, capability, transfer
- `wasm_deps.go` stub Staking/Distribution vì sovereign rollup không có validator set
- WASM capabilities: `iterator, staking, stargate, cosmwasm_1_1..1_4`
- Transaction routing: protobuf decode → MsgServer → keeper

### 2.2. CosmosExecutor — Core Implementation

**Đọc:**
- [`apps/cosmos-exec/executor/executor.go`](../../../executor/executor.go) — **ĐỌC KỸ TOÀN BỘ FILE**

**Theo thứ tự đọc các methods:**

```
1. New() / NewCosmosExecutor()    ← constructor, khởi tạo maps
2. InitChain()                     ← genesis state, idempotency check
3. InjectTx()                      ← push tx vào mempool
4. GetTxs()                        ← drain mempool
5. ExecuteTxs()                    ← ĐÂY LÀ PHẦN QUAN TRỌNG NHẤT
6. SetFinal()                      ← finalize block
7. GetExecutionInfo()              ← MaxGas = 0
8. FilterTxs()                     ← size-only filter
9. GetLatestHeight()               ← HeightProvider
10. Rollback()                     ← IAVL LoadVersion
11. PruneExec()                    ← delete old maps
```

**ExecuteTxs deep dive — ABCI flow:**

```go
// Simplified flow in executor.go:
BeginBlock(abci.RequestBeginBlock{Header: header})
for _, tx := range txs {
    result := DeliverTx(abci.RequestDeliverTx{Tx: tx})
    // store result (code, gas, events, log)
}
EndBlock(abci.RequestEndBlock{Height: height})
commitRes := Commit()
stateRoot = commitRes.Data  // IAVL Merkle root
```

**Kiến thức cần nắm:**
- `BeginBlock` → `DeliverTx × N` → `EndBlock` → `Commit` sequence
- DeliverTx trả: Code (0=ok), GasWanted, GasUsed, Events, Log
- Commit() persist IAVL tree → trả state root (Merkle hash)
- stateRoot = block's AppHash = IAVL commit ID
- mempool là `[][]byte` in-memory, drain bằng `GetTxs()`
- `txResults map[string]*TxResult` lưu execution result per tx hash

### 2.3. BlobStore — Content-Addressed Storage

**Đọc:**
- [`apps/cosmos-exec/executor/blob_store.go`](../../../executor/blob_store.go)

**Kiến thức cần nắm:**
- SHA-256(data) = commitment (content addressing)
- `Put(data) → commitment` / `Get(commitment) → data`
- `PutBatch(blobs) → (merkle_root, commitments)`
- Size limits: 4MB/blob, 256MB total (configurable)
- Thread-safe (mutex)
- In-memory — persist qua PersistStore

### 2.4. PersistStore — Disk Persistence

**Đọc:**
- [`apps/cosmos-exec/executor/persist.go`](../../../executor/persist.go)

**Kiến thức cần nắm:**
- 3 files: `metadata.json` (overwrite), `tx_results.jsonl`, `blocks.jsonl` (append). Không có `blobs.jsonl` — blob lưu trên Celestia qua `BlobClient`
- On startup: replay `.jsonl` files vào memory maps
- `metadata.json` chứa: ChainID, lastHeight, stateRoot, finalizedHeight
- Pattern: ABCI state (IAVL/LevelDB) + metadata (JSONL) = complete state

### 2.5. Rollback — IAVL Version Recovery

**Đọc:**
- `Rollback()` method trong [`executor.go`](../../../executor/executor.go)
- [cosmos-vs-evnode.md — Section 3](cosmos-vs-evnode.md) — Optional interfaces comparison

**Kiến thức cần nắm:**
- IAVL tree giữ history: mỗi `Commit()` = 1 version
- `LoadVersion(height)` = revert tree về version cũ
- Sau rollback: recalculate stateRoot từ reverted tree
- Trim in-memory maps (blocks, txResults) tới target height
- **Khi nào cần rollback:** EL crash sau `Commit()` nhưng trước CL persist block

---

## Phase 3: Framework Engine — ev-node Internals

**Mục tiêu:** Hiểu cách ev-node produce blocks, broadcast qua P2P, submit lên DA, và sync.

### 3.1. Block Production — Aggregator Node

**Đọc:**
- [`block/internal/executing/executor.go`](../../../../block/internal/executing/executor.go) — `executionLoop()`, `ProduceBlock()`
- [chain-flow.md — Section 4](chain-flow.md) — Block Production 14 bước

**ProduceBlock — 14 bước (simplified):**

```
1.  Lock mutex
2.  sequencer.GetNextBatch() → txs
3.  executor.FilterTxs(txs) → filtered
4.  executor.ExecuteTxs(filtered) → stateRoot
5.  Create SignedHeader (signed by aggregator key)
6.  Create Data (txs + metadata)
7.  Store header + data in block store
8.  P2P broadcast header (GossipSub topic 1)
9.  P2P broadcast data (GossipSub topic 2)
10. executor.SetFinal(height)
11. Update DA submission height
12. sequencer.SubmitBatchCompleted()
13. Update metrics
14. Unlock mutex
```

**Kiến thức cần nắm:**
- executionLoop: `ticker → ProduceBlock()` mỗi `blockTime` (default 1s)
- Block = SignedHeader + Data (tách riêng cho light client support)
- Broadcast order: header trước, data sau (cho phép light client verify sớm)
- aggregator key (Ed25519) ký mỗi block header

### 3.2. Sequencer — Transaction Ordering

**Đọc:**
- [`pkg/sequencers/single/sequencer.go`](../../../../pkg/sequencers/single/sequencer.go) — Single sequencer

**Kiến thức cần nắm:**
- Single sequencer = centralized ordering (aggregator node quyết định tx order)
- `SubmitTx(tx)` → internal batch buffer
- `GetNextBatch()` → return batch + forced inclusion txs from DA
- Forced inclusion: user submit tx trực tiếp lên DA namespace → sequencer phải include
- Based sequencer: alternative mode, tx ordering bởi DA layer (Celestia)

### 3.3. P2P Gossip — Block Propagation

**Đọc:**
- [`pkg/p2p/client.go`](../../../../pkg/p2p/client.go) — libp2p client
- [`pkg/sync/sync_service.go`](../../../../pkg/sync/sync_service.go) — Header/Data exchange
- [`block/internal/syncing/p2p_handler.go`](../../../../block/internal/syncing/p2p_handler.go) — P2P block processing
- [chain-flow.md — Section 6](chain-flow.md) — P2P Gossip

**Kiến thức cần nắm:**
- 2 GossipSub topics: `<chainID>/header/v1` + `<chainID>/data/v1`
- go-header library: handle P2P header exchange, store, sync
- Aggregator broadcast → all full nodes nhận qua GossipSub
- P2PHandler: validate proposer address + check header/data consistency (DataHash match)
- DA hints: mỗi P2P message kèm hint nói "data này sẽ nằm ở DA height nào" → giúp DA retrieval
- Kademlia DHT: peer discovery (tìm peers qua bootstrap nodes)

### 3.4. DA Submission — Celestia Blobs

**Đọc:**
- [`block/internal/submitting/submitter.go`](../../../../block/internal/submitting/submitter.go) — Submission loop
- [`block/internal/submitting/da_submitter.go`](../../../../block/internal/submitting/da_submitter.go) — Blob packaging
- [`block/internal/da/client.go`](../../../../block/internal/da/client.go) — Celestia client
- [chain-flow.md — Section 7](chain-flow.md) — DA Submission

**Kiến thức cần nắm:**
- 3 Celestia namespaces: header namespace, data namespace, forced inclusion namespace
- Batching strategy: submit N blocks per DA blob (configurable `DABlocksPerBatch`)
- Submission loop: `pendingDAHeight..currentHeight` → batch → submit → retry on failure
- Blob format: serialized protobuf (header or data), signed by aggregator key
- DA inclusion ≠ DA verification — DA chỉ store, verification bởi full nodes khi sync

### 3.5. Full Node Sync — DA + P2P

**Đọc:**
- [`block/internal/syncing/syncer.go`](../../../../block/internal/syncing/syncer.go) — 3 goroutines
- [`block/internal/syncing/da_follower.go`](../../../../block/internal/syncing/da_follower.go) — DA subscription
- [`block/internal/syncing/da_retriever.go`](../../../../block/internal/syncing/da_retriever.go) — DA blob retrieval
- [chain-flow.md — Section 8](chain-flow.md) — Full Node Sync

**3 goroutines:**

```
┌──────────────────┐
│ DA Follower      │ ← subscribe DA layer, sequential height scan
│ (daFollowerLoop) │ → emit events to heightInCh
└────────┬─────────┘
         │
┌────────┼─────────┐
│ P2P Worker       │ ← process P2P blocks, validate proposer
│ (p2pWorkerLoop)  │ → emit events to heightInCh (faster than DA)
└────────┼─────────┘
         │
         ▼
┌──────────────────┐
│ Main Sync Loop   │ ← consume heightInCh, TrySyncNextBlock
│ (syncLoop)       │ → ExecuteTxs → store → advance height
└──────────────────┘
```

**Kiến thức cần nắm:**
- P2P sync = fast (nhận block ngay khi aggregator broadcast) nhưng soft finality
- DA sync = slower (phải đợi DA inclusion) nhưng hard finality
- Main sync loop: xử lý tuần tự theo height (height N phải xong trước height N+1)
- ValidateBlock: check signature, proposer address, stateRoot consistency
- TrySyncNextBlock: `ExecuteTxs()` → verify stateRoot matches header → store

---

## Phase 4: Integration — 2-Process Wiring

**Mục tiêu:** Hiểu cách cosmos-wasm (CL) kết nối với cosmos-exec-grpc (EL), node startup flow.

### 4.1. HTTP Server — cosmos-exec-grpc

**Đọc:**
- [`apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go`](../../../cmd/cosmos-exec-grpc/main.go) — Entry point + handler registration
- [architecture.md — Section 5](architecture.md) — HTTP API server

**Startup flow:**

```
main()
  ├─ Config: ForProfile("dev") + LoadFromEnv() + CLI flags
  ├─ DB: openDatabase(dataDir, inMemory)
  ├─ App: app.New(logger, db)
  ├─ Executor: executor.New(app, opts...)
  │    ├─ WithPersistence(dir)  → replay JSONL files
  │    ├─ WithQueryGasMax(50M)
  │    └─ WithBlobStoreLimits(4MB, 256MB)
  ├─ Executor.InitChain(...)
  ├─ Register HTTP handlers:
  │    ├─ Connect-RPC: /evnode.v1.ExecutorService/* (6 core methods)
  │    ├─ Custom HTTP: /tx/*, /blob/*, /query/*, /status
  │    └─ Optional: /exec/height, /exec/rollback, /exec/prune
  ├─ Security: middleware(auth, CORS, rate-limit, body-size)
  └─ Listen: 0.0.0.0:50051
```

**Kiến thức cần nắm:**
- Connect-RPC JSON protocol cho 6 core methods
- Custom HTTP endpoints cho extras (blob, query, height, rollback, prune)
- 2 layers of handler: Connect-RPC (generated) + custom HTTP (hand-written)

### 4.2. Executor Client — cosmos-wasm side

**Đọc:**
- [`apps/cosmos-wasm/cmd/executor_client.go`](../../cosmos-wasm/cmd/executor_client.go) — Pure HTTP client

**Kiến thức cần nắm:**
- Pure HTTP+JSON client (không import generated code) — tránh module path conflicts
- `connectCall()`: POST `/evnode.v1.ExecutorService/<Method>` + JSON body
- `httpCall()`: standard HTTP cho optional interfaces
- Implement `execution.Executor` + `HeightProvider` + `Rollbackable` + `ExecPruner`
- Serialization: uint64 as JSON string (Connect-RPC convention), timestamps as RFC3339

### 4.3. Node Startup — cosmos-wasm

**Đọc:**
- [`apps/cosmos-wasm/cmd/run.go`](../../cosmos-wasm/cmd/run.go) — Node startup logic
- [`apps/cosmos-wasm/cmd/init.go`](../../cosmos-wasm/cmd/init.go) — Init command
- [`node/full.go`](../../../../node/full.go) — Full node lifecycle

**Startup flow:**

```
evcosmos start
  ├─ Parse config (TOML + CLI flags)
  ├─ Create EnhancedExecutorClient(grpc-executor-url)
  ├─ Create Single sequencer
  ├─ Create DA client (Celestia)
  ├─ node.NewFullNode(config, executor, sequencer, daClient, ...)
  │    ├─ Mode check: aggregator vs sync-only
  │    ├─ block.NewAggregatorComponents() or block.NewSyncComponents()
  │    │    ├─ Replayer: sync EL height with CL height
  │    │    ├─ Cache manager
  │    │    ├─ Executor / Syncer / Submitter / Pruner / Reaper
  │    │    └─ Error channel for critical failures
  │    ├─ P2P client setup (libp2p, GossipSub, DHT)
  │    └─ Sync services (header + data)
  └─ node.Start(ctx)
       ├─ Start P2P
       ├─ Start sync services
       ├─ Start block components
       └─ Block until ctx cancelled or critical error
```

### 4.4. Block Components Wiring

**Đọc:**
- [`block/components.go`](../../../../block/components.go) — Components struct, Start/Stop

**Kiến thức cần nắm:**
- `Components` struct: Executor, Pruner, Reaper, Syncer, Submitter, Cache
- Aggregator node: Executor + Reaper + Submitter + Pruner (produce blocks)
- Sync-only node: Syncer + Submitter + Pruner (only sync blocks)
- Error channel: critical executor failure → stop node
- Start order: Executor → Pruner → Reaper → Syncer → Submitter
- Stop: reverse order + save caches

---

## Phase 5: SDK & Extensions

**Mục tiêu:** Hiểu SDK client library, BlobStore workflow, DABridge, Merkle proofs.

### 5.1. SDK Client — User-Facing API

**Đọc:**
- [`sdk/cosmoswasm/client.go`](../client.go) — Client struct
- [`sdk/cosmoswasm/sdk_config.go`](../sdk_config.go) — SDKConfig
- [`sdk/cosmoswasm/types.go`](../types.go) — Request/Response types
- [`sdk/cosmoswasm/errors.go`](../errors.go) — Error handling
- [architecture.md — Section 1](architecture.md) — SDK Package overview

**Kiến thức cần nắm:**
- `NewClient(baseURL)` → `Client` struct
- Core methods: SubmitBlob, RetrieveBlob, SubmitTxBytes, QuerySmart, CommitRoot
- SDKConfig: timeout, retry count, auth token
- SDKError: Op (operation), Cause (underlying error), Hint (fix suggestion)

### 5.2. Transaction Building

**Đọc:**
- [`sdk/cosmoswasm/tx.go`](../tx.go) — 5 tx builders
- `internal/txcodec/txcodec.go` — Protobuf encoding

**5 tx builders:**

| Builder | Cosmos SDK Msg | Use case |
|---------|---------------|----------|
| `BuildStoreTx` | `MsgStoreCode` | Upload .wasm bytecode |
| `BuildInstantiateTx` | `MsgInstantiateContract` | Create contract instance |
| `BuildExecuteTx` | `MsgExecuteContract` | Call contract method |
| `BuildBlobCommitTx` | Custom `record_blob` | Record single blob commitment |
| `BuildBatchRootTx` | Custom `record_batch` | Record Merkle root of batch |

### 5.3. BlobStore Workflow — End-to-End

**Đọc:**
- [`sdk/cosmoswasm/blob.go`](../blob.go) — SubmitBlob, RetrieveBlob
- [`sdk/cosmoswasm/commit.go`](../commit.go) — CommitRoot
- [`sdk/cosmoswasm/batch.go`](../batch.go) — BatchBuilder
- [`sdk/cosmoswasm/proof.go`](../proof.go) — Merkle proofs

**Full workflow:**

```
1. SubmitBlob(data)
   └─ POST /blob/submit → commitment (SHA-256)

2. CommitRoot(commitments)
   └─ Build Merkle tree → root
   └─ BuildBatchRootTx(root) → tx bytes
   └─ POST /tx/submit → on-chain record

3. BuildMerkleProof(commitments, index)
   └─ Generate inclusion proof: leaf → root path

4. VerifyMerkleProof(proof)
   └─ Offline verification: no chain access needed

5. RetrieveBlob(commitment)
   └─ GET /blob/{commitment} → original data
```

**Pattern:** Data off-chain (BlobStore) + Commitment on-chain (WASM contract) + Proof (Merkle) = verifiable off-chain data.

### 5.4. DABridge — App-Level DA Integration

**Đọc:**
- [`sdk/cosmoswasm/da_bridge.go`](../da_bridge.go) — DABridge struct
- [`sdk/cosmoswasm/da_client.go`](../da_client.go) — DAClient interface

**Kiến thức cần nắm:**
- DABridge = DAClient + ExecutorClient (kết hợp 2 layers)
- `Submit()`: data → Celestia namespace → commitment
- `SubmitAndCommit()`: data → Celestia → commitment → on-chain tx (1 call)
- `Watch()`: subscribe incoming blobs from DA namespace
- Khác với framework-level DA (block submission): đây là **app-level DA** cho user data

### 5.5. Data Optimization

**Đọc:**
- [`sdk/cosmoswasm/chunk.go`](../chunk.go) — Large data chunking
- [`sdk/cosmoswasm/compress.go`](../compress.go) — Gzip compression
- [`sdk/cosmoswasm/cost.go`](../cost.go) — Cost estimation

**Kiến thức cần nắm:**
- ChunkBlob: split data > maxChunkSize thành smaller pieces
- CompressIfBeneficial: gzip chỉ khi compressed < original
- EstimateCost: so sánh cost giữa on-chain storage vs blob-first pattern

### 5.6. Examples — Hands-On

**Chạy thử:**

| Example | Path | Học được gì |
|---------|------|-------------|
| `my-counter` | [`examples/my-counter/main.go`](../examples/my-counter/main.go) | Full WASM lifecycle: store → instantiate → execute → query |
| `game-telemetry` | [`examples/game-telemetry/main.go`](../examples/game-telemetry/main.go) | Blob-first: batch, chunking, compression, ghi commitment on-chain |
| `forced-inclusion` | [`examples/forced-inclusion/main.go`](../examples/forced-inclusion/main.go) | Chống censorship: post tx thẳng lên DA |

**Recommend:** Bắt đầu với `my-counter` → `game-telemetry` → `forced-inclusion`.

---

## Phase 6: Operations — Production Readiness

**Mục tiêu:** Hiểu config, security, crash recovery, monitoring.

### 6.1. Configuration

**Đọc:**
- [`apps/cosmos-exec/config/config.go`](../../../config/config.go) — Server config
- [`pkg/config/`](../../../../pkg/config/) — ev-node config (TOML)
- [architecture.md — Section 4](architecture.md) — Config

**2 config systems:**

| Config | Thuộc về | Format |
|--------|---------|--------|
| `pkg/config/` | cosmos-wasm (ev-node) | TOML file |
| `config/config.go` | cosmos-exec-grpc | Go struct + env vars |

**Config profiles:** dev (no auth, in-memory) → test (random port, small store) → prod (auth, persistence, rate limit, metrics).

### 6.2. Security Middleware

**Đọc:**
- [`apps/cosmos-exec/cmd/cosmos-exec-grpc/middleware.go`](../../../cmd/cosmos-exec-grpc/middleware.go) — Security stack

**Security layers:**
1. CORS (configurable origins)
2. Auth (Bearer token)
3. Rate limiting (per-IP, configurable RPS)
4. Max request body size (default 10MB)
5. Read-only mode (block all write endpoints)

### 6.3. Crash Recovery — Replayer

**Đọc:**
- [`block/internal/common/replay.go`](../../../../block/internal/common/replay.go) — SyncToHeight
- [chain-flow.md — Section 11](chain-flow.md) — Crash Recovery

**Recovery flow:**

```
Node restart
  ├─ CL loads last persisted height from block store
  ├─ EL.GetLatestHeight() → check EL height
  │
  ├─ Case 1: EL == CL → all good
  │
  ├─ Case 2: EL > CL → EL committed blocks CL didn't persist
  │    └─ EL.Rollback(CL_height) → IAVL LoadVersion
  │
  ├─ Case 3: EL < CL → CL has blocks EL hasn't executed
  │    └─ Replay: for h in (EL+1..CL): ExecuteTxs(stored_block[h])
  │
  └─ Continue normal operation
```

### 6.4. Monitoring & Metrics

**Đọc:**
- [`apps/cosmos-exec/cmd/cosmos-exec-grpc/metrics.go`](../../../cmd/cosmos-exec-grpc/metrics.go) — Prometheus metrics

**Metrics endpoints:**
- cosmos-exec: `/metrics` (custom: tx_submit, blob_submit, query counts, latency)
- cosmos-wasm: ev-node built-in metrics (block production, P2P, DA, sync)

### 6.5. Pruning

**Đọc:**
- [`block/internal/pruner/pruner.go`](../../../../block/internal/pruner/pruner.go) — Pruning loop

**Kiến thức cần nắm:**
- Pruner chạy periodic (mỗi `prunePeriod`)
- Prune old blocks + headers + data từ block store
- Nếu executor implement `ExecPruner` → cũng gọi `PruneExec()` để dọn execution metadata
- Keep threshold: configurable số blocks giữ lại

---

## Dependency Map — Đọc file nào trước file nào

```
Level 0 (Đọc trước nhất):
  core/execution/execution.go           ← interface definitions

Level 1 (Execution layer):
  apps/cosmos-exec/app/app.go           ← Cosmos SDK app
  apps/cosmos-exec/executor/executor.go ← executor implementation
  apps/cosmos-exec/executor/blob_store.go
  apps/cosmos-exec/executor/persist.go

Level 2 (Framework engine):
  block/internal/executing/executor.go  ← block production
  block/internal/syncing/syncer.go      ← block sync
  block/internal/submitting/submitter.go ← DA submission
  block/components.go                    ← component wiring

Level 3 (Infrastructure):
  pkg/p2p/client.go                     ← P2P networking
  pkg/sync/sync_service.go             ← header/data exchange
  block/internal/da/client.go           ← Celestia client
  block/internal/common/replay.go       ← crash recovery
  pkg/sequencers/single/sequencer.go    ← tx ordering

Level 4 (Integration):
  apps/cosmos-wasm/cmd/executor_client.go ← HTTP client
  apps/cosmos-wasm/cmd/run.go             ← node startup
  apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go ← HTTP server
  node/full.go                            ← full node lifecycle

Level 5 (SDK):
  sdk/cosmoswasm/client.go              ← SDK client
  sdk/cosmoswasm/blob.go                ← blob operations
  sdk/cosmoswasm/commit.go              ← on-chain commitment
  sdk/cosmoswasm/da_bridge.go           ← app-level DA
  sdk/cosmoswasm/proof.go               ← Merkle proofs
  sdk/cosmoswasm/tx.go                  ← tx builders
```

---

## Quick Reference — Tất cả docs hiện có

| Doc | Nội dung | Đọc khi nào |
|-----|----------|-------------|
| [architecture.md](architecture.md) | Full component map, file-by-file | Phase 1 overview |
| [chain-flow.md](chain-flow.md) | Technical chain flow (12 sections) | Phase 3 deep dive |
| [cosmos-vs-evnode.md](cosmos-vs-evnode.md) | What cosmos built vs ev-node | After Phase 3 |
| [getting-started.md](getting-started.md) | Setup & first run | Before Phase 5 examples |
| [api-reference.md](api-reference.md) | HTTP endpoint reference | Phase 4-5 |
| [configuration.md](configuration.md) | Config reference | Phase 6 |
| [error-handling.md](error-handling.md) | Error patterns | Phase 5 SDK |
| [production-guide.md](production-guide.md) | Deployment guide | Phase 6 |
| [troubleshooting.md](troubleshooting.md) | Common issues | When stuck |
| [migration.md](migration.md) | Version migration | When upgrading |
| **This roadmap** | Learning path | Start here |

---

## Checklist — Tự đánh giá

Sau mỗi phase, kiểm tra bạn có thể trả lời được các câu hỏi:

### Phase 1
- [ ] ev-node Executor interface có bao nhiêu methods? Liệt kê.
- [ ] Optional interfaces có mấy cái? Khi nào dùng?
- [ ] Sovereign rollup khác gì L2 rollup?

### Phase 2
- [ ] ABCI flow: 4 bước chính trong ExecuteTxs?
- [ ] stateRoot tính từ đâu?
- [ ] Cosmos chain filter tx bằng gì? Tại sao MaxGas = 0?
- [ ] Rollback hoạt động thế nào? IAVL giữ history bao lâu?
- [ ] BlobStore dùng hash gì? Commitment là gì?

### Phase 3
- [ ] ProduceBlock có bao nhiêu bước chính?
- [ ] P2P dùng mấy GossipSub topic? Broadcast thứ tự nào?
- [ ] DA submission dùng mấy namespace? Khác nhau thế nào?
- [ ] Full node sync có mấy goroutine? P2P vs DA sync khác gì?
- [ ] Soft finality vs hard finality?

### Phase 4
- [ ] cosmos-wasm gọi cosmos-exec qua protocol gì?
- [ ] Tại sao dùng pure HTTP client thay vì generated code?
- [ ] Node startup: bao nhiêu component được tạo? Thứ tự start?
- [ ] Aggregator node vs sync-only node khác gì về components?

### Phase 5
- [ ] BlobStore workflow end-to-end: 5 bước?
- [ ] Merkle proof chứng minh gì? Verify offline được không?
- [ ] DABridge khác gì framework-level DA?
- [ ] SDK có bao nhiêu tx builder? Liệt kê.

### Phase 6
- [ ] Crash recovery xử lý 3 case nào?
- [ ] Config profile dev/test/prod khác gì?
- [ ] Security có mấy layer?
- [ ] Pruner dọn gì? Bao lâu chạy 1 lần?
