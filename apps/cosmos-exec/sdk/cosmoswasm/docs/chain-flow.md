# Cosmos/CosmWasm Chain — Technical Chain Flow

Tài liệu kỹ thuật chuyên sâu về cách cosmos chain (apps/cosmos-exec + apps/cosmos-wasm) vận hành trên ev-node framework. Giải thích chi tiết từ nguyên lý đến implementation, trích dẫn code cụ thể.

## Mục lục

1. [Kiến trúc 2 process](#1-kiến-trúc-2-process)
2. [Transaction Lifecycle](#2-transaction-lifecycle)
3. [Sequencer — Ordering Transactions](#3-sequencer--ordering-transactions)
4. [Block Production — Aggregator Node](#4-block-production--aggregator-node)
5. [State Transition — ABCI Flow](#5-state-transition--abci-flow)
6. [P2P Gossip — Block Propagation](#6-p2p-gossip--block-propagation)
7. [DA Submission — Celestia Blobs](#7-da-submission--celestia-blobs)
8. [Full Node Sync — DA + P2P](#8-full-node-sync--da--p2p)
9. [Finality Model — Soft vs Hard](#9-finality-model--soft-vs-hard)
10. [Data Storage — Where Everything Lives](#10-data-storage--where-everything-lives)
11. [Crash Recovery — Replay & Rollback](#11-crash-recovery--replay--rollback)
12. [Pruning — Garbage Collection](#12-pruning--garbage-collection)

---

## 1. Kiến trúc 2 process

Cosmos chain chạy theo mô hình **2 process tách biệt**, giao tiếp qua HTTP/gRPC:

```
┌─────────────────────────────────┐     ┌─────────────────────────────────┐
│     cosmos-wasm (evcosmos)      │     │     cosmos-exec-grpc            │
│                                 │     │                                 │
│  ┌───────────┐  ┌────────────┐  │     │  ┌───────────────────────────┐  │
│  │ Sequencer │  │ Executor   │──┼─RPC─┼──│ CosmosExecutor            │  │
│  │ (Single)  │  │ Client     │  │     │  │  ┌─────────────────────┐  │  │
│  └───────────┘  └────────────┘  │     │  │  │ Cosmos SDK BaseApp  │  │  │
│  ┌───────────┐  ┌────────────┐  │     │  │  │  auth, bank, wasm   │  │  │
│  │ P2P       │  │ DA Client  │  │     │  │  │  keepers            │  │  │
│  │ (libp2p)  │  │ (Celestia) │  │     │  │  └─────────────────────┘  │  │
│  └───────────┘  └────────────┘  │     │  │  ┌─────────────────────┐  │  │
│  ┌───────────┐  ┌────────────┐  │     │  │  │ IAVL State Tree     │  │  │
│  │ Block     │  │ Submitter  │  │     │  │  │ (LevelDB)           │  │  │
│  │ Store     │  │ (to DA)    │  │     │  │  └─────────────────────┘  │  │
│  │ (LevelDB) │  └────────────┘  │     │  └───────────────────────────┘  │
│  └───────────┘                  │     │  ┌───────────────────────────┐  │
│                                 │     │  │ HTTP Server               │  │
│                                 │     │  │  /tx/submit,              │  │
│                                 │     │  │  /wasm/query-smart        │  │
│                                 │     │  └───────────────────────────┘  │
└─────────────────────────────────┘     └─────────────────────────────────┘
```

### Tại sao tách?

**Nguyên lý:** ev-node giữ execution layer là **pluggable**. Core framework (sequencer, P2P, DA, store) không biết execution layer dùng gì — có thể là EVM, CosmWasm, hay custom runtime. Framework chỉ gọi interface `execution.Executor`.

**Hệ quả thực tế:**
- cosmos-exec-grpc có thể restart độc lập (upgrade chain logic) mà không mất P2P connections
- cosmos-wasm có thể restart mà state vẫn nằm trên cosmos-exec
- Execution layer crash không kéo theo mất block store / P2P state
- Debug dễ hơn: mỗi process có log riêng, resource usage riêng

### Communication Protocol

cosmos-wasm gọi cosmos-exec qua 2 protocol:

**1. Connect-RPC JSON** — cho 6 core Executor methods:
```
POST /evnode.v1.ExecutorService/<Method>
Content-Type: application/json

Methods: InitChain, GetTxs, ExecuteTxs, SetFinal, GetExecutionInfo, FilterTxs
```

**File:** [`apps/cosmos-wasm/cmd/executor_client.go`](../../../../cosmos-wasm/cmd/executor_client.go) — `connectCall()`

```go
func (c *EnhancedExecutorClient) connectCall(ctx context.Context, method string, reqBody, respBody any) error {
    payload, _ := json.Marshal(reqBody)
    url := c.baseURL + "/evnode.v1.ExecutorService/" + method
    req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
    req.Header.Set("Content-Type", "application/json")
    // ...
}
```

**2. Custom HTTP** — cho 3 optional interfaces (HeightProvider, Rollbackable, ExecPruner):
```
GET  /exec/height                      → {"height": N}
POST /exec/rollback  {"target_height": N} → {"status": "ok"}
POST /exec/prune     {"height": N}     → {"status": "ok"}
```

**File:** [`apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go`](../../../cmd/cosmos-exec-grpc/main.go)

---

## 2. Transaction Lifecycle

### 2.1. User Submit Transaction

User (hoặc SDK) gửi Cosmos SDK transaction đã encode protobuf:

```
POST /tx/submit
{
  "tx_bytes_base64": "<base64 of protobuf MsgExecuteContract>"
}
```

**File:** [`apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go`](apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go) — `txSubmitHandler`

Transaction được inject vào **in-memory mempool**:

```go
// File: apps/cosmos-exec/executor/executor.go
func (e *CosmosExecutor) InjectTx(tx []byte) {
    e.mu.Lock()
    defer e.mu.Unlock()
    e.mempool = append(e.mempool, tx)
}
```

**Quan trọng:** Tại thời điểm này, transaction **chưa được execute**. Nó nằm trong mempool của cosmos-exec-grpc process, chờ ev-node (cosmos-wasm) đến lấy.

### 2.2. Reaper — Kéo Transaction từ Execution Layer

ev-node không biết khi nào có tx mới. Component **Reaper** poll định kỳ:

**File:** [`block/internal/reaping/reaper.go`](block/internal/reaping/reaper.go)

```go
// reaperLoop chạy mỗi scrapeInterval
func (r *Reaper) reaperLoop() {
    ticker := time.NewTicker(r.interval) // scrape_interval config
    for {
        select {
        case <-ticker.C:
            r.SubmitTxs()  // gọi executor.GetTxs() → submit vào sequencer
        }
    }
}
```

**Flow:**
```
Reaper.SubmitTxs()
  │
  ├─ 1. exec.GetTxs(ctx)
  │     → Gọi HTTP: POST /evnode.v1.ExecutorService/GetTxs
  │     → cosmos-exec drain mempool, trả về [][]byte
  │
  ├─ 2. Deduplicate: skip txs đã seen (SHA-256 cache)
  │
  └─ 3. sequencer.SubmitBatchTxs(batch)
        → Đưa txs vào sequencer queue
```

**File:** [`apps/cosmos-exec/executor/executor.go`](apps/cosmos-exec/executor/executor.go) — `GetTxs()`

```go
func (e *CosmosExecutor) GetTxs(ctx context.Context) ([][]byte, error) {
    e.mu.Lock()
    defer e.mu.Unlock()
    txs := e.mempool
    e.mempool = nil   // drain toàn bộ
    return txs, nil
}
```

Reaper cũng notify executor khi có tx mới → `txNotifyCh` → executionLoop biết để produce block ngay (lazy mode).

### 2.3. Full Lifecycle — Tổng quan

```
 User                cosmos-exec-grpc          cosmos-wasm (ev-node)
  │                       │                          │
  │──POST /tx/submit─────▶│                          │
  │◀──hash────────────────│                          │
  │                       │ mempool = [tx1, tx2]     │
  │                       │                          │
  │                       │◀──GetTxs()───────────────│ Reaper tick
  │                       │──[tx1,tx2]──────────────▶│
  │                       │                          │ → sequencer queue
  │                       │                          │
  │                       │                          │ executionLoop tick
  │                       │                          │ → GetNextBatch()
  │                       │                          │ → CreateBlock()
  │                       │◀──ExecuteTxs()───────────│
  │                       │   BeginBlock,DeliverTx,  │
  │                       │   EndBlock,Commit        │
  │                       │──newAppHash─────────────▶│
  │                       │                          │ → P2P broadcast
  │                       │                          │ → DA submit (async)
  │                       │                          │
  │──GET /tx/result/{h}──▶│                          │
  │◀──{code:0, logs:...}──│                          │
```

---

## 3. Sequencer — Ordering Transactions

### 3.1. Nguyên lý

Sequencer quyết định **thứ tự** transactions trong mỗi block. Trong sovereign rollup, sequencer có toàn quyền ordering (khác L2 rollup phải theo L1). Cosmos chain dùng **Single Sequencer** — một node duy nhất có quyền produce block.

### 3.2. Single Sequencer

**File:** [`pkg/sequencers/single/sequencer.go`](pkg/sequencers/single/sequencer.go)

```go
type Sequencer struct {
    queue     *BatchQueue   // transaction queue
    executor  execution.Executor
    daClient  block.FullDAClient
    fiRetriever block.ForcedInclusionRetriever  // forced inclusion
    // ...
}
```

### 3.3. GetNextBatch — Tạo batch cho block mới

Khi executionLoop cần tạo block mới, nó gọi:

```
Executor.RetrieveBatch(ctx)
  → sequencer.GetNextBatch(req)
```

**Flow bên trong GetNextBatch:**

```
GetNextBatch(req):
  │
  ├─ 1. Lấy forced inclusion txs từ DA
  │     └─ fiRetriever.RetrieveForcedIncludedTxs(daHeight)
  │        → Scan Celestia forced_inclusion namespace
  │        → Trả về txs user submit trực tiếp lên DA
  │
  ├─ 2. Lấy txs từ queue (đã được Reaper đưa vào)
  │     └─ queue.DequeueAll()
  │
  ├─ 3. Merge: forced_txs ++ queue_txs
  │     (forced txs LUÔN đi trước — censorship resistance)
  │
  ├─ 4. Filter qua executor.FilterTxs()
  │     → POST /evnode.v1.ExecutorService/FilterTxs
  │     → cosmos-exec kiểm tra: gas, nonce, signature
  │     → Trả về status cho mỗi tx: Include / Exclude / ForceDelay
  │
  └─ 5. Return Batch{Transactions: validTxs}
```

### 3.4. Forced Inclusion — Censorship Resistance

**Nguyên lý:** Nếu sequencer cố tình bỏ qua tx của user (censorship), user có thể bypass sequencer hoàn toàn bằng cách submit tx trực tiếp lên Celestia trong **forced_inclusion namespace**.

Sequencer **bắt buộc** phải include các tx này. Nếu không, full node sẽ phát hiện khi verify block từ DA → coi sequencer là malicious → halt chain.

**File:** [`block/internal/syncing/syncer.go`](block/internal/syncing/syncer.go) — `VerifyForcedInclusionTxs()`

```go
// VerifyForcedInclusionTxs checks that forced inclusion txs were actually included
// If not → errMaliciousProposer → node halts
```

### 3.5. Catch-up State Machine

Sequencer có state machine cho DA sync:

```
catchUpUnchecked (0) → catchUpInProgress (1) → catchUpDone (2)
```

Khi node start lại sau crash:
- **catchUpUnchecked:** Chưa biết DA đã tiến bao xa
- **catchUpInProgress:** Đang replay forced txs từ các DA epochs bỏ lỡ
- **catchUpDone:** Đã sync xong, hoạt động bình thường

Checkpoint persist `(DAHeight, TxIndex)` → restart không mất forced txs.

---

## 4. Block Production — Aggregator Node

### 4.1. Execution Loop

**File:** [`block/internal/executing/executor.go:371-440`](block/internal/executing/executor.go)

Aggregator node chạy **một goroutine duy nhất** produce blocks:

```go
func (e *Executor) executionLoop() {
    blockTimer := time.NewTimer(e.config.Node.BlockTime.Duration)  // vd: 2s

    for e.ctx.Err() == nil {
        select {
        case <-blockTimer.C:
            // Normal mode: produce block mỗi BlockTime
            // Lazy mode: chỉ produce khi txsAvailable == true
            e.blockProducer.ProduceBlock(e.ctx)
            blockTimer.Reset(blockTime - elapsed)

        case <-lazyTimerCh:
            // Lazy mode fallback: produce empty block sau LazyBlockInterval
            // Đảm bảo chain vẫn tiến dù không có tx
            e.blockProducer.ProduceBlock(e.ctx)

        case <-e.txNotifyCh:
            txsAvailable = true   // Reaper báo có tx mới
        }
    }
}
```

**Lazy mode** tiết kiệm DA cost: không produce empty blocks liên tục, chỉ produce khi có tx hoặc đến lazy interval.

### 4.2. ProduceBlock — Chi tiết từng bước

**File:** [`block/internal/executing/executor.go:443-649`](block/internal/executing/executor.go)

```
ProduceBlock(ctx):
  │
  ├─ 1. CHECK RAFT QUORUM
  │     Nếu chạy Raft HA cluster → kiểm tra có đủ quorum không
  │     Không đủ → skip block (tránh split brain)
  │
  ├─ 2. CHECK PENDING LIMIT
  │     Nếu pending headers/data > MaxPendingHeadersAndData
  │     → skip block (DA backlog quá lớn, chờ DA catch up)
  │
  ├─ 3. CHECK PENDING BLOCK (crash recovery)
  │     Nếu có pending block từ lần crash trước → dùng lại
  │     (block đã tạo nhưng chưa được apply)
  │
  ├─ 4. RETRIEVE BATCH
  │     sequencer.GetNextBatch() → forced_txs + mempool_txs
  │     Nếu ErrNoBatch → skip (không có gì để produce)
  │
  ├─ 5. CREATE BLOCK
  │     Tạo Header (height, time, chain_id, app_hash, proposer, ...)
  │     Tạo Data (transactions, metadata)
  │     DataHash = DACommitment(data)
  │
  ├─ 6. SAVE PENDING BLOCK
  │     Persist block vào store TRƯỚC khi execute
  │     → Crash safety: restart có thể resume
  │
  ├─ 7. APPLY BLOCK (state transition)
  │     → Gọi executor.ExecuteTxs() qua RPC
  │     → cosmos-exec: BeginBlock → DeliverTx × N → EndBlock ��� Commit
  │     → Nhận newAppHash (IAVL state root)
  │
  ├─ 8. SIGN HEADER
  │     Ed25519 sign bằng node key
  │     (trừ based sequencer mode → không sign)
  │
  ├─ 9. VALIDATE SEQUENCE
  │     Kiểm tra block hợp lệ trong chain:
  │     └─ AssertValidSequence: height+1, time monotonic, ...
  │
  ├─ 10. STORE: atomic batch write
  │      SaveBlockData(header, data, signature)
  │      SetHeight(newHeight)
  │      UpdateState(newState)
  │      Commit()   ← atomic
  │
  ├─ 11. RAFT BROADCAST (nếu HA cluster)
  │      Propose RaftBlockState cho followers
  │      Followers replicate → sẵn sàng takeover
  │
  ├─ 12. P2P BROADCAST ⚠️ THỨ TỰ QUAN TRỌNG
  │      headerBroadcaster.WriteToStoreAndBroadcast(P2PSignedHeader)
  │      dataBroadcaster.WriteToStoreAndBroadcast(P2PData)
  │      Header PHẢI broadcast trước Data (xem mục 6)
  │
  ├─ 13. SET FINAL (based sequencer only)
  │      exec.SetFinal(height) → finalize ngay vì DA-sourced
  │
  └─ 14. METRICS UPDATE
         height, txs_per_block, block_size, ...
```

### 4.3. CreateBlock — Cấu trúc block

**File:** [`block/internal/executing/executor.go:684-787`](block/internal/executing/executor.go)

```go
header := &types.SignedHeader{
    Header: types.Header{
        Version:         {Block: 11, App: stateVersion},  // Block=11 cho IBC compat
        BaseHeader:      {ChainID: "my-chain", Height: 42, Time: unixNano},
        LastHeaderHash:  hash(prevHeader),
        DataHash:        DACommitment(data),   // Merkle root of txs
        AppHash:         prevStateRoot,         // IAVL root TRƯỚC block này
        ProposerAddress: genesis.ProposerAddress,
        ValidatorHash:   hash(proposerAddr + pubKey),
    },
    Signature: signedAfterApply,
    Signer:    {PubKey: ed25519PubKey, Address: proposerAddr},
}

data := &types.Data{
    Txs: []Tx{tx1, tx2, ...},
    Metadata: &types.Metadata{
        ChainID:      "my-chain",
        Height:       42,
        Time:         unixNano,
        LastDataHash: hash(prevData),
    },
}
```

**Lưu ý về AppHash:** Header.AppHash là state root **trước** block này (input), không phải output. Output (newAppHash) từ ExecuteTxs được lưu vào `State.AppHash` và xuất hiện trong header block **tiếp theo**.

---

## 5. State Transition — ABCI Flow

### 5.1. Nguyên lý

ev-node gọi `ExecuteTxs()` trên execution layer. Với cosmos chain, đây là lệnh RPC → cosmos-exec-grpc → `CosmosExecutor.ExecuteTxs()`.

**File:** [`apps/cosmos-exec/executor/executor.go`](apps/cosmos-exec/executor/executor.go) — `ExecuteTxs()`

### 5.2. ABCI Flow chi tiết

```
ExecuteTxs(txs, blockHeight, timestamp, prevStateRoot):
  │
  ├─ BeginBlock(RequestBeginBlock{
  │       Header: {Height, Time, ChainID, AppHash: prevStateRoot}
  │   })
  │   → Cosmos SDK modules nhận thông báo block mới bắt đầu
  │   → auth module: reset per-block gas meter
  │   → wasm module: reset per-block state
  │
  ├─ DeliverTx(tx₁) → Response{Code: 0, Log: "ok", GasUsed: 50000}
  │   DeliverTx(tx₂) → Response{Code: 0, Log: "ok", GasUsed: 120000}
  │   DeliverTx(tx₃) → Response{Code: 11, Log: "out of gas"}  ← failed but included
  │   ...
  │   → Mỗi tx được:
  │     1. Decode protobuf → sdk.Msg (vd: MsgExecuteContract)
  │     2. Ante handler: verify signature, check nonce, deduct gas
  │     3. Route đến keeper tương ứng:
  │        • MsgStoreCode → wasm keeper → lưu .wasm bytecode
  │        • MsgInstantiateContract → wasm keeper → tạo contract instance
  │        • MsgExecuteContract → wasm keeper → chạy contract logic
  │        • MsgSend → bank keeper → chuyển token
  │     4. Emit events (tx_hash, code, logs)
  │   → Kết quả mỗi tx được lưu vào txResults map
  │
  ├─ EndBlock(RequestEndBlock{Height})
  │   → Finalize block logic
  │
  └─ Commit()
      → IAVL tree persist → LevelDB
      → Return app hash (32-byte IAVL root)
      → Đây là state root SAU block
```

### 5.3. State Root (AppHash)

```
AppHash = IAVL Merkle Root
        = Root of a versioned Merkle AVL tree
        = Commitment to toàn bộ state (accounts, balances, contracts, ...)

Mỗi version = 1 block height
IAVL giữ history → có thể LoadVersion(height) → rollback
```

IAVL tree trong LevelDB bên **cosmos-exec-grpc** process. Block store (headers, data) bên **cosmos-wasm** process. Hai stores độc lập.

---

## 6. P2P Gossip — Block Propagation

### 6.1. Nguyên lý

Sau khi aggregator produce block, nó broadcast qua **GossipSub** (libp2p pub/sub protocol) để full nodes nhận block nhanh nhất có thể — trước khi block xuất hiện trên DA layer.

### 6.2. Network Setup

**File:** [`pkg/p2p/client.go`](pkg/p2p/client.go)

```go
func (c *Client) Start(ctx context.Context) error {
    // 1. Listen on TCP/UDP (QUIC)
    // 2. Setup GossipSub pub/sub
    // 3. Setup Kademlia DHT cho peer discovery
    // 4. Active peer discovery qua rendezvous points
}
```

**Peer discovery:**
- Boot nodes (seed nodes) chạy Kademlia DHT
- Nodes mới connect → DHT → tìm peers cùng chain ID
- Rendezvous string = hash(chainID) → peers cùng chain tự tìm nhau

### 6.3. Hai Topic riêng biệt

P2P sử dụng go-header library với **2 GossipSub topics**:

```
Topic 1: Header topic
  → P2PSignedHeader = {SignedHeader, DAHint}
  → Nhẹ: chỉ header metadata + signature

Topic 2: Data topic  
  → P2PData = {Data, DAHint}
  → Nặng: chứa toàn bộ transactions
```

**File:** [`pkg/sync/sync_service.go`](pkg/sync/sync_service.go)

```go
type HeaderSyncService = SyncService[*types.P2PSignedHeader]
type DataSyncService   = SyncService[*types.P2PData]
```

Mỗi SyncService bao gồm:
- **Subscriber:** Nhận messages từ GossipSub topic
- **Exchange:** Request-response cho block catch-up (peer hỏi peer)
- **ExchangeServer:** Phục vụ các request catch-up từ peers khác
- **Syncer:** go-header syncer đảm bảo thứ tự đúng

### 6.4. Broadcast Order ⚠️

**File:** [`block/internal/executing/executor.go:622-631`](block/internal/executing/executor.go)

```go
// IMPORTANT: Header MUST be broadcast before data — the P2P layer validates
// incoming data against the current and previous header, so out-of-order
// delivery would cause validation failures on peers.
headerBroadcaster.WriteToStoreAndBroadcast(ctx, &types.P2PSignedHeader{...})
dataBroadcaster.WriteToStoreAndBroadcast(ctx, &types.P2PData{...})
```

**Tại sao header trước?** P2P data validation dựa trên header đã nhận:
- `data.DACommitment()` phải match `header.DataHash`
- `data.Metadata.Height` phải match `header.Height`
- Nếu data đến trước header → không có gì để validate → reject

### 6.5. DA Height Hints

P2P messages mang theo **DA height hints** — vị trí trên Celestia nơi block này sẽ xuất hiện:

```go
type P2PSignedHeader struct {
    *SignedHeader
    daHint uint64   // DA height nơi header đã/sẽ được submit
}

type P2PData struct {
    *Data
    daHint uint64   // DA height nơi data đã/sẽ được submit
}
```

Full node nhận hint → queue **priority DA retrieval** → fetch block từ Celestia nhanh hơn (không cần scan tuần tự).

**File:** [`block/internal/syncing/syncer.go:543-616`](block/internal/syncing/syncer.go)

### 6.6. P2P Worker trên Full Node

**File:** [`block/internal/syncing/p2p_handler.go`](block/internal/syncing/p2p_handler.go)

```go
func (h *P2PHandler) ProcessHeight(ctx context.Context, height uint64, ...) error {
    // 1. headerStore.GetByHeight(height) — block đến từ GossipSub
    // 2. assertExpectedProposer(header.ProposerAddress)
    // 3. dataStore.GetByHeight(height)
    // 4. Verify: header.DataHash == data.DACommitment()
    // 5. Emit event → syncer's heightInCh
}
```

---

## 7. DA Submission — Celestia Blobs

### 7.1. Nguyên lý

DA (Data Availability) layer đảm bảo rằng block data **có thể được bất kỳ ai download**. Đây là nền tảng của sovereign rollup: state chỉ đúng nếu mọi người đều có thể verify bằng cách replay transactions từ DA.

### 7.2. Ba Namespaces

Cosmos chain submit data vào **3 Celestia namespaces riêng biệt**:

```
┌──────────────────────────────────────┐
│            Celestia Block N          │
│                                      │
│  ┌─ Header Namespace ──────────────┐ │
│  │  DAHeaderEnvelope {             │ │
│  │    Header: SignedHeader (proto) │ │
│  │    Signature: Ed25519 sig       │ │
│  │  }                              │ │
│  │  (có thể batch nhiều headers)   │ │
│  └─────────────────────────────────┘ │
│                                      │
│  ┌─ Data Namespace ────────────────┐ │
│  │  SignedData {                   │ │
│  │    Data: {Txs, Metadata} (proto)│ │
│  │    Signature: Ed25519 sig       │ │
│  │  }                              │ │
│  │  (có thể batch nhiều data blobs)│ │
│  └─────────────────────────────────┘ │
│                                      │
│  ┌─ Forced Inclusion Namespace ────┐ │
│  │  Raw transactions user submit   │ │
│  │  trực tiếp lên Celestia         │ │
│  │  (bypass sequencer)             │ │
│  └─────────────────────────────────┘ │
└──────────────────────────────────────┘
```

**Config:**

```toml
[da]
namespace = "0x..."              # Header namespace
data_namespace = "0x..."         # Data namespace  
forced_inclusion_namespace = "0x..."  # Forced inclusion namespace
```

### 7.3. DA Submitter — Batching Strategy

**File:** [`block/internal/submitting/submitter.go:162-305`](block/internal/submitting/submitter.go)

Submitter **không** submit mỗi block ngay. Nó batch nhiều blocks lại:

```go
func (s *Submitter) daSubmissionLoop() {
    checkInterval := max(config.DA.BlockTime/4, 100ms)  // check thường xuyên
    ticker := time.NewTicker(checkInterval)

    for {
        <-ticker.C

        // Headers
        headersNb := cache.NumPendingHeaders()
        if headersNb > 0 && batchingStrategy.ShouldSubmit(count, size, maxBlobSize, timeSinceLastSubmit) {
            daSubmitter.SubmitHeaders(headers, marshalledHeaders, cache, signer)
        }

        // Data (tương tự)
        dataNb := cache.NumPendingData()
        if dataNb > 0 && batchingStrategy.ShouldSubmit(...) {
            daSubmitter.SubmitData(signedDataList, marshalledData, cache, signer, genesis)
        }
    }
}
```

**Batching Strategy** quyết định khi nào submit dựa trên:
- **Time-based:** Đã qua N giây kể từ lần submit cuối
- **Size-based:** Tổng size đạt X bytes (gần max blob size)
- **Count-based:** Đã tích đủ N blocks

### 7.4. Submit Process

**File:** [`block/internal/submitting/da_submitter.go:212-255`](block/internal/submitting/da_submitter.go)

```
SubmitHeaders(headers):
  │
  ├─ 1. Kiểm tra envelope cache (đã sign lần trước?)
  │     LRU cache tránh re-sign khi retry
  │
  ├─ 2. Sign headers → DAHeaderEnvelope
  │     Ed25519 sign, parallel worker pool (GOMAXPROCS workers)
  │
  ├─ 3. Submit lên Celestia
  │     client.Submit(envelopes, gasPrice, headerNamespace, options)
  │     → blobAPI.Submit(blobs, submitOptions)
  │
  ├─ 4. Retry policy nếu fail:
  │     reasonFailure  → exponential backoff (100ms → 200ms → 400ms → ...)
  │     reasonMempool  → wait MaxBackoff (DA mempool đầy)
  │     reasonTooBig   → split batch thành chunks nhỏ hơn
  │     reasonSuccess  → reset backoff
  │
  └─ 5. Update cache:
        cache.SetHeaderDAIncluded(hash, daHeight, blockHeight)
        cache.SetLastSubmittedHeaderHeight(lastHeight)
```

### 7.5. Blob Format trên Celestia

Mỗi blob chứa **protobuf-encoded** data:

```
Header blob = marshal(DAHeaderEnvelope{
    Header:    SignedHeader (protobuf bytes),
    Signature: Ed25519 signature over header bytes,
})

Data blob = marshal(SignedData{
    Data:      Data{Txs, Metadata} (protobuf bytes),
    Signature: Ed25519 signature,
    Signer:    {PubKey, Address},
})
```

**File:** [`block/internal/da/client.go:72-183`](block/internal/da/client.go)

```go
func (c *client) Submit(ctx context.Context, data [][]byte, _ float64, namespace []byte, options []byte) ResultSubmit {
    // Build Celestia blobs
    for _, raw := range data {
        blobs[i] = blobrpc.NewBlobV0(namespace, raw)  // Celestia v0 blob format
    }
    // Submit via Celestia blob API
    height, err := c.blobAPI.Submit(ctx, blobs, &submitOpts)
    // Return DA height + blob IDs (commitment)
}
```

---

## 8. Full Node Sync — DA + P2P

### 8.1. Nguyên lý

Full node **không produce blocks**. Nó sync từ 2 nguồn song song:

```
                   Full Node
                      │
        ┌─────────────┼─────────────┐
        ▼                           ▼
   P2P Network                  DA Layer
   (fast, trust)              (slow, trustless)
        │                           │
        │ GossipSub                 │ Poll/Subscribe
        │ ~100ms latency            │ ~12s latency (Celestia)
        │                           │
        ▼                           ▼
   Soft confirmation          Hard confirmation
   (block received)           (DA included = finalized)
```

**P2P nhanh hơn** vì block được broadcast ngay sau produce. DA chậm hơn vì phải chờ Celestia block time (~12s) + submission latency.

### 8.2. Syncer Architecture

**File:** [`block/internal/syncing/syncer.go`](block/internal/syncing/syncer.go)

```go
type Syncer struct {
    store       store.Store
    exec        coreexecutor.Executor
    daClient    da.Client
    cache       cache.CacheManager
    headerStore header.Store[*types.P2PSignedHeader]  // P2P header store
    dataStore   header.Store[*types.P2PData]           // P2P data store
    daFollower  DAFollower                              // DA subscription
    // ...
}
```

Syncer chạy **3 goroutines song song**:

```
1. processLoop      — Main loop xử lý events theo thứ tự
2. p2pWorkerLoop    — Đọc headers+data từ P2P stores
3. pendingWorkerLoop — Retry events chưa xử lý được
   + DAFollower (2 goroutines nội bộ):
     4. followLoop   — Subscribe DA namespace
     5. catchupLoop  — Sequential DA retrieval
```

### 8.3. DA Follower

**File:** [`block/internal/syncing/da_follower.go`](block/internal/syncing/da_follower.go)

```
DAFollower:
  │
  ├─ followLoop:
  │     Subscribe Celestia namespace (header + data)
  │     Khi có blob mới → process inline (fast path)
  │     Hoặc update highestSeenDAHeight → signal catchupLoop
  │
  └─ catchupLoop:
        Sequential fetch: localNextDAHeight → highestSeenDAHeight
        Mỗi DA height → RetrieveFromDA(height) → parse blobs
        → pipe events vào syncer.heightInCh
```

### 8.4. DA Retriever

**File:** [`block/internal/syncing/da_retriever.go`](block/internal/syncing/da_retriever.go)

```go
func (r *daRetriever) RetrieveFromDA(ctx context.Context, daHeight uint64) ([]DAHeightEvent, error) {
    // 1. Fetch header namespace blobs tại daHeight
    headerRes := r.client.Retrieve(ctx, daHeight, headerNamespace)

    // 2. Fetch data namespace blobs (nếu namespace khác)
    dataRes := r.client.Retrieve(ctx, daHeight, dataNamespace)

    // 3. Combine results
    // 4. Parse blobs: protobuf decode → SignedHeader, Data
    // 5. Match headers với data (cùng height)
    // 6. Return []DAHeightEvent
}
```

### 8.5. Process Height Event

**File:** [`block/internal/syncing/syncer.go:511-654`](block/internal/syncing/syncer.go)

Khi syncer nhận event (từ P2P hoặc DA):

```
processHeightEvent(event):
  │
  ├─ Skip nếu height <= currentHeight (đã xử lý)
  │
  ├─ Skip nếu height != currentHeight + 1 (chưa đến lượt)
  │     → store as pending event
  │
  ├─ Nếu P2P event có DA height hints:
  │     → Queue priority DA retrieval (fetch DA proof nhanh hơn)
  │
  └─ TrySyncNextBlock(event):
        │
        ├─ ValidateBlock(state, data, header):
        │     • Verify proposer address = genesis.ProposerAddress
        │     • Verify header signature (Ed25519)
        │     • Verify sequence (height, lastHeaderHash, ...)
        │     • Verify DataHash == data.DACommitment()
        │
        ├─ VerifyForcedInclusionTxs (DA-sourced blocks only):
        │     • Kiểm tra forced inclusion txs có được include
        │     • Nếu thiếu → errMaliciousProposer → halt
        │
        ├─ ApplyBlock(header, data, currentState):
        │     exec.ExecuteTxs(txs, height, timestamp, prevAppHash)
        │     → RPC → cosmos-exec → ABCI flow
        │     → newState = currentState.NextState(header, newAppHash)
        │
        ├─ Store: batch write
        │     SaveBlockData(header, data, signature)
        │     SetHeight(newHeight)
        │     UpdateState(newState)
        │
        └─ Mark as seen in cache
```

### 8.6. Aggregator Catch-up

Khi aggregator node restart, nó cần sync trước khi produce blocks:

**File:** [`block/components.go:327-380`](block/components.go) — `NewAggregatorWithCatchupComponents()`

```
1. Tạo Syncer + Executor components
2. Start Syncer → sync DA + P2P đến head
3. Wait HasReachedDAHead() + PendingCount() == 0
4. Stop Syncer
5. Start Executor → bắt đầu produce blocks
```

---

## 9. Finality Model — Soft vs Hard

### 9.1. Nguyên lý

Sovereign rollup có **2 mức finality**:

```
┌──────────────────────────────────────────────────────���──┐
│ Timeline:                                               │
│                                                         │
│  Block produced ──→ P2P broadcast ──→ DA included       │
│       │                  │                  │           │
│   Instant            ~100ms             ~12-30s         │
│       │                  │                  │           │
│  [pending]       [soft confirmed]   [hard finalized]    │
│                                                         │
│  Sequencer biết    Full nodes biết   Mọi người verify   │
│  state mới         state mới (trust) state (trustless)  │
└─────────────────────────────────────────────────────────┘
```

### 9.2. Soft Confirmation

Block được produce → store locally → broadcast P2P. Full nodes nhận qua P2P → apply → **soft confirmed**. Nhanh nhưng trust aggregator.

### 9.3. Hard Finality (DA Inclusion)

**File:** [`block/internal/submitting/submitter.go:307-368`](block/internal/submitting/submitter.go) — `processDAInclusionLoop()`

```go
func (s *Submitter) processDAInclusionLoop() {
    ticker := time.NewTicker(s.config.DA.BlockTime.Duration)

    for {
        <-ticker.C
        currentDAIncluded := s.GetDAIncludedHeight()

        for {
            nextHeight := currentDAIncluded + 1

            // Kiểm tra block có tồn tại
            _, data, err := s.store.GetBlockData(ctx, nextHeight)
            if err != nil { break }

            // Kiểm tra DA included (header + data đều có trên Celestia)
            if included, _ := s.IsHeightDAIncluded(nextHeight, data); !included {
                break
            }

            // Set mapping: node_height → DA_height
            s.setNodeHeightToDAHeight(ctx, nextHeight, data, ...)

            // FINALIZE: gọi executor.SetFinal(height)
            // → cosmos-exec cập nhật finalizedHeight
            s.setFinalWithRetry(nextHeight)

            // Persist DA included height
            putUint64Metadata(ctx, store, "da_included_height", nextHeight)

            // Advance
            s.SetDAIncludedHeight(nextHeight)
        }
    }
}
```

**IsHeightDAIncluded** checks:
- Header hash tồn tại trong DA (cache tracks da_height per header)
- Data commitment tồn tại trong DA (hoặc data rỗng → dùng header DA height)
- Cả header VÀ data phải trên DA → mới coi là finalized

### 9.4. SetFinal trên cosmos-exec

**File:** [`apps/cosmos-exec/executor/executor.go`](apps/cosmos-exec/executor/executor.go) — `SetFinal()`

```go
func (e *CosmosExecutor) SetFinal(ctx context.Context, blockHeight uint64) error {
    e.mu.Lock()
    defer e.mu.Unlock()
    if blockHeight > e.lastHeight { return error }
    e.finalizedHeight = blockHeight
    return e.saveMetadataLocked()
}
```

`finalizedHeight` là mốc: mọi state ≤ height này được coi là **không thể rollback**.

---

## 10. Data Storage — Where Everything Lives

### 10.1. Hai Store độc lập

```
cosmos-wasm (ev-node) process:
  └─ LevelDB (~/.evcosmos/data/cosmos-wasm)
     ├─ Block headers (per height)
     ├─ Block data / transactions (per height)
     ├─ Signatures (per height)
     ├─ State (chain state per height)
     ├─ DA height mappings
     ├─ P2P header/data stores
     └─ Metadata (da_included_height, store_height, ...)

cosmos-exec-grpc process:
  └─ LevelDB (~/.cosmos-exec/data)
     ├─ IAVL tree (versioned state — accounts, balances, contracts)
     ├─ Block info (height → {time, app_hash, num_txs})
     ├─ Tx results (hash → {code, log, gas_used, height})
     ├─ Executor metadata (lastHeight, finalizedHeight, stateRoot)
     └─ BlobStore (SHA-256 → blob data)
```

### 10.2. ev-node Store

**File:** [`pkg/store/`](pkg/store/)

```go
type Store interface {
    Height(ctx) (uint64, error)                    // current height
    SetHeight(ctx, height) error
    GetBlockData(ctx, height) (*SignedHeader, *Data, error)
    SaveBlockData(header, data, signature) error
    GetState(ctx) (State, error)
    UpdateState(state) error
    GetMetadata(ctx, key) ([]byte, error)           // arbitrary KV
    SetMetadata(ctx, key, value) error
    NewBatch(ctx) (Batch, error)                    // atomic writes
}
```

Store được wrap:
1. `baseStore` → raw LevelDB operations
2. `cachedStore` → LRU cache on top (headers, block data)
3. `tracingStore` → OpenTelemetry tracing (optional)

### 10.3. State struct

```go
type State struct {
    ChainID         string
    InitialHeight   uint64
    LastBlockHeight uint64
    LastBlockTime   time.Time
    DAHeight        uint64     // last known DA height
    AppHash         []byte     // IAVL state root SAU last block
    LastHeaderHash  Hash
    Version         Version
}
```

State được persist **mỗi block** vào store. Nó là snapshot cho crash recovery.

### 10.4. cosmos-exec State

cosmos-exec lưu state qua **IAVL versioned tree**:
- Mỗi block = 1 IAVL version
- Rollback = `app.LoadVersion(targetHeight)` → IAVL revert về version cũ
- Pruning = IAVL auto-prune old versions (configurable)

Metadata (lastHeight, finalizedHeight, stateRoot) được persist vào `PersistStore`:

**File:** [`apps/cosmos-exec/executor/executor.go`](apps/cosmos-exec/executor/executor.go) — `saveMetadataLocked()`

---

## 11. Crash Recovery — Replay & Rollback

### 11.1. Nguyên lý

Hai process có thể crash bất cứ lúc nào. Recovery phải đảm bảo:
- Không mất blocks đã commit
- Execution layer sync lại với block store
- Không execute lại blocks đã finalized

### 11.2. HeightProvider — Phát hiện mismatch

**File:** [`block/internal/common/replay.go`](block/internal/common/replay.go) — `Replayer.SyncToHeight()`

Khi node start:

```go
func (s *Replayer) SyncToHeight(ctx context.Context, targetHeight uint64) error {
    // 1. Hỏi execution layer: bạn đang ở height bao nhiêu?
    execHeight, _ := execHeightProvider.GetLatestHeight(ctx)

    // 2. So sánh với block store height
    if execHeight > targetHeight {
        // Execution layer AHEAD → cần rollback
        // (vd: cosmos-wasm crash giữa chừng, cosmos-exec đã commit)
        rollbackable.Rollback(ctx, targetHeight)

    } else if execHeight < targetHeight {
        // Execution layer BEHIND → cần replay
        // (vd: cosmos-exec restart, mất in-memory state)
        for height := execHeight + 1; height <= targetHeight; height++ {
            replayBlock(ctx, height)
            // → đọc block từ store → ExecuteTxs → verify AppHash
        }
    }
    // else: in sync, nothing to do
}
```

### 11.3. Rollback — cosmos-exec

**File:** [`apps/cosmos-exec/executor/executor.go`](apps/cosmos-exec/executor/executor.go) — `Rollback()`

```go
func (e *CosmosExecutor) Rollback(ctx context.Context, targetHeight uint64) error {
    // 1. IAVL rollback
    e.app.LoadVersion(int64(targetHeight))  // revert state tree

    // 2. Update state root
    cms := e.app.CommitMultiStore()
    e.stateRoot = cms.LastCommitID().Hash

    // 3. Cleanup in-memory maps
    for h := range e.blocks { if h > targetHeight { delete(e.blocks, h) } }
    for hash, result := range e.txResults { if result.Height > targetHeight { delete(e.txResults, hash) } }

    // 4. Update heights
    e.lastHeight = targetHeight
    if e.finalizedHeight > targetHeight { e.finalizedHeight = targetHeight }

    // 5. Persist
    e.saveMetadataLocked()
}
```

### 11.4. Pending Block Recovery

Nếu crash giữa `savePendingBlock()` và `ApplyBlock()`:

```go
// executor.go:496-506
if e.hasPendingBlock.Load() {
    pendingHeader, pendingData, err := e.getPendingBlock(ctx)
    if err == nil && pendingHeader.Height() == newHeight {
        // Resume: dùng lại block đã tạo, skip tạo mới
        header = pendingHeader
        data = pendingData
    }
}
```

Block được persist **trước** khi execute → crash an toàn.

---

## 12. Pruning — Garbage Collection

### 12.1. Nguyên lý

Rollup nodes tích luỹ data theo thời gian. Pruning xoá data cũ không cần thiết, giữ disk usage kiểm soát được.

### 12.2. Pruner Component

**File:** [`block/internal/pruner/pruner.go`](block/internal/pruner/pruner.go)

```go
type Pruner struct {
    store      store.Store
    execPruner coreexecutor.ExecPruner  // optional: prune execution metadata
    cfg        config.PruningConfig
}

func (p *Pruner) pruneLoop() {
    ticker := time.NewTicker(p.cfg.Interval)
    for {
        <-ticker.C
        switch p.cfg.Mode {
        case PruningModeMetadata:
            p.pruneMetadata()   // chỉ xoá metadata, giữ blocks
        case PruningModeAll:
            p.pruneBlocks()     // xoá cả blocks cũ
        }
    }
}
```

### 12.3. ExecPruner — cosmos-exec

ev-node gọi `PruneExec(height)` nếu executor implement `ExecPruner`:

**File:** [`block/components.go:181-185`](block/components.go)

```go
var execPruner coreexecutor.ExecPruner
if p, ok := exec.(coreexecutor.ExecPruner); ok {
    execPruner = p  // type assertion thành công → dùng pruning
}
pruner := pruner.New(logger, store, execPruner, ...)
```

cosmos-exec's `PruneExec()` xoá block info và tx results cũ:

```go
func (e *CosmosExecutor) PruneExec(ctx context.Context, height uint64) error {
    for h := range e.blocks { if h <= height { delete(e.blocks, h) } }
    for hash, result := range e.txResults { if result.Height <= height { delete(e.txResults, hash) } }
}
```

### 12.4. Pruning Config

```toml
[pruning]
enabled = true
mode = "metadata"        # "metadata" hoặc "all"
interval = "10m"         # chạy mỗi 10 phút
keep_recent = 1000       # giữ 1000 blocks gần nhất
```

---

## Source Code Quick Reference

| Component | File | Vai trò |
|-----------|------|---------|
| **cosmos-exec** | | |
| CosmosExecutor | [`apps/cosmos-exec/executor/executor.go`](apps/cosmos-exec/executor/executor.go) | ABCI execution, mempool, state |
| HTTP Server | [`apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go`](apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go) | HTTP handlers, gRPC service |
| Cosmos App | [`apps/cosmos-exec/app/app.go`](apps/cosmos-exec/app/app.go) | BaseApp + keepers |
| **cosmos-wasm** | | |
| Executor Client | [`apps/cosmos-wasm/cmd/executor_client.go`](apps/cosmos-wasm/cmd/executor_client.go) | HTTP client implementing Executor + optional interfaces |
| Run Command | [`apps/cosmos-wasm/cmd/run.go`](apps/cosmos-wasm/cmd/run.go) | Node startup, wiring |
| **ev-node core** | | |
| Executor Interface | [`core/execution/execution.go`](core/execution/execution.go) | Executor, HeightProvider, Rollbackable, ExecPruner |
| Block Components | [`block/components.go`](block/components.go) | NewSyncComponents, newAggregatorComponents |
| Block Producer | [`block/internal/executing/executor.go`](block/internal/executing/executor.go) | executionLoop, ProduceBlock, ApplyBlock |
| Reaper | [`block/internal/reaping/reaper.go`](block/internal/reaping/reaper.go) | Poll txs from executor → sequencer |
| DA Submitter | [`block/internal/submitting/submitter.go`](block/internal/submitting/submitter.go) | daSubmissionLoop, DA inclusion tracking |
| DA Client | [`block/internal/da/client.go`](block/internal/da/client.go) | Submit/Retrieve blobs from Celestia |
| Syncer | [`block/internal/syncing/syncer.go`](block/internal/syncing/syncer.go) | DA + P2P sync, block validation |
| DA Follower | [`block/internal/syncing/da_follower.go`](block/internal/syncing/da_follower.go) | DA subscription + catchup |
| DA Retriever | [`block/internal/syncing/da_retriever.go`](block/internal/syncing/da_retriever.go) | Fetch/parse blobs from DA |
| P2P Handler | [`block/internal/syncing/p2p_handler.go`](block/internal/syncing/p2p_handler.go) | Process P2P blocks |
| Replay/Recovery | [`block/internal/common/replay.go`](block/internal/common/replay.go) | SyncToHeight, rollback, replay |
| Pruner | [`block/internal/pruner/pruner.go`](block/internal/pruner/pruner.go) | Prune old blocks + exec metadata |
| Single Sequencer | [`pkg/sequencers/single/sequencer.go`](pkg/sequencers/single/sequencer.go) | Ordering, forced inclusion |
| P2P Client | [`pkg/p2p/client.go`](pkg/p2p/client.go) | libp2p, GossipSub, DHT |
| Sync Service | [`pkg/sync/sync_service.go`](pkg/sync/sync_service.go) | go-header P2P sync |
| Full Node | [`node/full.go`](node/full.go) | Aggregator/sync modes, Raft |
