# Cosmos SDK Architecture

Tài liệu này mô tả kiến trúc toàn bộ Cosmos/WASM stack — từ SDK package cho đến các thành phần bên dưới mà SDK phụ thuộc để chạy được.

## Tổng quan hệ thống

```
┌─────────────────────────────────────────────────────────────┐
│  User App (Go)                                              │
│  import cosmoswasm ".../sdk/cosmoswasm"                     │
│  client.SubmitBlob / CommitRoot / QuerySmart / BuildStoreTx │
└──────────────────────┬──────────────────────────────────────┘
                       │ HTTP
                       ▼
┌──────────────────────────────────────────────────────────────┐
│  cosmos-exec-grpc          cmd/cosmos-exec-grpc/             │
│  HTTP API server + Swagger + middleware (auth, CORS, rate)   │
└──────────────────────┬───────────────────────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────────────────────┐
│  CosmosExecutor            executor/                         │
│  WASM runtime + blob store + mempool + state persistence     │
└──────────────────────┬───────────────────────────────────────┘
                       │ uses
                       ▼
┌──────────────────────────────────────────────────────────────┐
│  App (Cosmos SDK)          app/                              │
│  BaseApp + modules (auth, bank, wasm, IBC, params)           │
└──────────────────────┬───────────────────────────────────────┘
                       │ DA submit (via evcosmos)
                       ▼
┌──────────────────────────────────────────────────────────────┐
│  evcosmos                  apps/cosmos-wasm/                  │
│  Full node: sequencer, P2P, block production, DA sync        │
└──────────────────────┬───────────────────────────────────────┘
                       │
                       ▼
┌──────────────────────────────────────────────────────────────┐
│  Celestia DA Layer                                           │
│  Namespace-isolated blob storage                             │
└──────────────────────────────────────────────────────────────┘
```

---

## 1. sdk/cosmoswasm/ — Public SDK Package

**Import path:** `github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm`

User chỉ cần import package này. Tất cả các component bên dưới chạy ở server side.

### Core — Client + Config

| File | Vai trò |
|------|---------|
| `sdk.go` | Package doc — phân loại API tier (Tier 1/2/3) |
| `sdk_config.go` | `SDKConfig`, `DefaultSDKConfig()`, `NewClientFromConfig()` — cấu hình timeout, retry, auth |
| `client.go` | `Client` struct — tất cả methods chính: `SubmitBlob`, `RetrieveBlob`, `RetrieveBlobData`, `SubmitTxBytes`, `SubmitTxBase64`, `GetTxResult`, `WaitTxResult`, `QuerySmart`, `QuerySmartRaw`, `CommitRoot`, `CommitCritical`, `SubmitBatch` |
| `executor_client.go` | `ExecutorClient` interface — transport abstraction (HTTP hoặc Mock). Client implement interface này |
| `defaults.go` | Tất cả default constants + `DefaultBatchBuilderConfig()`, `DefaultEstimateCostRequest()` |
| `errors.go` | `SDKError` struct (Op, Cause, Hint) + sentinel errors: `ErrNotReachable`, `ErrTxFailed`, `ErrBlobNotFound`, `ErrBlobTooLarge`, `ErrContractMissing` |
| `types.go` | Request/response types: `SubmitTxResponse`, `GetTxResultResponse`, `TxExecutionResult`, `TxEvent`, `TxEventAttribute`, `BlobRef`, `CommitReceipt`, `CommitRootRequest`, `BlobSubmitResponse`, `BlobRetrieveResponse`, `BlobBatchResponse`, `QuerySmartResponse` |

### Blob & Data Integrity

| File | Vai trò |
|------|---------|
| `blob.go` | `SubmitBlob()`, `RetrieveBlob()` — gọi HTTP tới executor blob store |
| `commit.go` | `CommitRoot()` — batch blobs → tính Merkle root → ghi root on-chain qua WASM tx. `CommitCritical()` — variant trả error nếu partial failure |
| `batch.go` | `BatchBuilder` — accumulate blobs, auto-flush khi đủ `MaxBytes` hoặc interval. `NewBatchBuilder()`, `Add()`, `Flush()`, `StartAutoFlush()`, `Len()`, `Bytes()` |
| `chunk.go` | `ChunkBlob()` — split blob lớn thành chunks nhỏ hơn `maxChunkSize`. `ReassembleChunks()` — ghép lại. Thin wrapper → `internal/chunk` |
| `compress.go` | `CompressGzip()`, `DecompressGzip()`, `CompressIfBeneficial()`, `MaybeDecompress()`, `IsGzipCompressed()`. Thin wrapper → `internal/compress` |
| `proof.go` | `MerkleProof`, `MerklePathStep` types. `GetProof()` / `BuildMerkleProof()` — tạo inclusion proof. `VerifyMerkleProof()` — verify offline. Thin wrapper → `internal/merkle` |
| `cost.go` | `EstimateCost()` — so sánh gas: direct on-chain vs blob-first. Dùng internal gas constants (celestia + cosmos) |
| `namespace.go` | `Namespace` type, `NamespaceFromString()`, `NamespaceFromHex()`, `NewNamespaceV0()` — Celestia namespace 29 bytes |

### Transaction Building

| File | Vai trò |
|------|---------|
| `tx.go` | 5 tx builders, tất cả delegate → `internal/txcodec`: |
| | `BuildStoreTx(wasmBytes, sender)` — MsgStoreCode (upload .wasm) |
| | `BuildInstantiateTx(req)` — MsgInstantiateContract (tạo contract instance) |
| | `BuildExecuteTx(req)` — MsgExecuteContract (gọi contract method) |
| | `BuildBlobCommitTx(req)` — ghi blob commitment on-chain (`record_blob` msg) |
| | `BuildBatchRootTx(req)` — ghi Merkle root on-chain (`record_batch` msg) |
| | `EncodeTxBase64()`, `EncodeTxHex()`, `DefaultSender()` |

### DA Layer

| File | Vai trò |
|------|---------|
| `da_client.go` | `DAClient` interface — abstraction cho Celestia hoặc Mock. Methods: `SubmitBlobs`, `GetBlobs`, `GetBlobByCommitment`, `Subscribe`, `GetHeight`. Types: `DASubmitOptions`, `DASubmitResult`, `DABlob`, `DABlobEvent`, `DANamespaceConfig` |
| `da_bridge.go` | `DABridge` struct — kết hợp `DAClient` + `ExecutorClient`. Methods: `Submit()` — gửi blobs lên DA. `GetBlobs()` — đọc blobs từ DA. `Watch()` — subscribe realtime. `SubmitAndCommit()` — DA + on-chain trong 1 call. `PollBlobs()` — polling thay cho WebSocket. `DAHeight()` — lấy latest DA height |

### Dev Tooling (Tier 3)

| File | Vai trò |
|------|---------|
| `chain.go` | `DALChainConfig`, `DALChainEndpoints`, `DALChainProcess`, `StartDALChain()`, `DefaultDALChainConfig()`. Thin wrapper → `internal/devchain`. Dùng để start full chain từ Go code (dev/test) |
| `mock_client.go` | `MockExecutorClient` — in-memory executor mock. `NewMockClient()`. Implement đầy đủ `ExecutorClient` interface. Hỗ trợ `OnSubmit()`, `OnQuery()`, `SetTxResult()`, `SetHeight()` |
| `mock_da_client.go` | `MockDAClient` — in-memory DA mock. `NewMockDAClient()`. Implement `DAClient` interface. Hỗ trợ `InjectBlobs()` |

### internal/ — Implementation details

Go compiler cấm external code import `internal/`. Refactor tự do mà không break user code.

| Package | File | Vai trò |
|---------|------|---------|
| `internal/merkle` | `merkle.go` | Binary SHA-256 Merkle tree: `BuildTree()`, `GenerateProof()`, `VerifyProof()` |
| `internal/compress` | `compress.go` | Gzip BestSpeed, magic byte detection, conditional compression |
| `internal/chunk` | `chunk.go` | Split blob theo max size, `ChunkMeta` cho reassembly |
| `internal/txcodec` | `txcodec.go` | Protobuf tx encoding: `BuildProtoTxBytes()`, `NormalizeJSONMsg()`, `DefaultSender()`, `WithDefaultSender()` |
| `internal/devchain` | `devchain.go` | Local chain process: `Config`, `Start()`, `buildRunnerArgs()`, `waitForLive()` |

### Tests

| File | Covers |
|------|--------|
| `integration_test.go` | Full SDK workflow — 12 subtests, không cần chain chạy |
| `client_test.go` | Client HTTP methods |
| `mock_client_test.go` | Mock executor (blob, batch, tx, query, CommitRoot, BatchBuilder) |
| `da_client_test.go` | Mock DA + DABridge (submit, namespace isolation, subscribe, poll) |
| `tx_test.go` | Transaction builders |
| `proof_test.go` | Merkle proof build + verify |
| `cost_test.go` | Cost estimator + benchmark |
| `compress_test.go` | Compression round-trip |
| `chunk_test.go` | Chunking round-trip |
| `namespace_test.go` | Namespace encode/decode |
| `chain_test.go` | DALChainConfig defaults |

### examples/

| Example | File | Chức năng |
|---------|------|-----------|
| `quickstart` | `examples/quickstart/main.go` | Blob submit/retrieve, Merkle proof, cost estimate. Không cần contract |
| `deploy-contract` | `examples/deploy-contract/main.go` | Full lifecycle: store .wasm → instantiate → execute → query → blob + proof |
| `contract-interaction` | `examples/contract-interaction/main.go` | Multi-contract (hackatom + reflect), sub-messages, blob-first pattern |
| `game-telemetry` | `examples/game-telemetry/main.go` | Batch submit 20 events, chunking, compression, cost comparison |
| `dapp-chain` | `examples/dapp-chain/main.go` | Start full DAL chain từ Go code (cần Celestia) |
| `dapp-chain-deploy` | `examples/dapp-chain-deploy/main.go` | Start chain + auto-deploy contract (one command) |

---

## 2. app/ — Cosmos SDK Application

SDK gọi HTTP tới executor, executor dùng `app.App` để chạy WASM.

| File | Vai trò |
|------|---------|
| `app.go` | **`App` struct** — wrap `baseapp.BaseApp` (Cosmos SDK). Khởi tạo tất cả modules + keepers. Implement `InitChainer`, `BeginBlocker`, `EndBlocker` |
| `wasm_deps.go` | Stub implementations cho các Cosmos SDK interfaces mà CosmWasm cần nhưng sovereign rollup không có: `noopStakingKeeper`, `noopDistributionKeeper`, `ibcClientStakingKeeper`, `ibcClientUpgradeKeeper` |
| `app_test.go` | Test khởi tạo App |
| `wasm_lifecycle_test.go` | Test store/instantiate/execute WASM contract qua App |

### Modules được khởi tạo trong App

| Module | Keeper | Vai trò |
|--------|--------|---------|
| `auth` | `AccountKeeper` | Quản lý accounts (address, sequence) |
| `bank` | `BankKeeper` | Chuyển token, check balance |
| `params` | `ParamsKeeper` | Module parameter store |
| `capability` | `CapabilityKeeper` | IBC capability management |
| `ibc` | `IBCKeeper` | IBC core (channel, connection, client) |
| `transfer` | `TransferKeeper` | IBC token transfer |
| **`wasm`** | **`WasmKeeper`** | **CosmWasm runtime** — store code, instantiate, execute, query smart contracts |

### WASM capabilities enabled

```
iterator, staking, stargate, cosmwasm_1_1, cosmwasm_1_2, cosmwasm_1_3, cosmwasm_1_4
```

### Cách App xử lý transaction

```
1. InitChain(genesis)     → khởi tạo state cho tất cả modules
2. BeginBlock(header)     → modules chuẩn bị cho block mới
3. DeliverTx(tx_bytes)    → ante chain → decode protobuf → route tới WasmKeeper
   ├─ MsgStoreCode        → lưu .wasm bytecode, trả code_id
   ├─ MsgInstantiateContract → tạo contract instance, trả contract_address
   ├─ MsgExecuteContract  → gọi contract method, thay đổi state
   └─ MsgIBCTransfer      → IBC token transfer
4. EndBlock(height)       → modules kết thúc block
5. Commit()               → persist state, trả app_hash (state root)
```

### AnteHandler chain (ante.go)

`NewPermissionlessAnteHandler` wires SDK decorators in this order:

```
SetUpContext → ExtensionOptions → ValidateBasic → TxTimeoutHeight
→ ValidateMemo → ConsumeGasForTxSize
→ AutoCreateAccount       (custom — creates missing signer accounts)
→ DeductFee               (0-fee checker, but still needs fee payer in state)
→ SetPubKey → ValidateSigCount → SigGasConsume → SigVerification
→ IncrementSequence
```

`AutoCreateAccount` **must** run before `DeductFee` and `SetPubKey`. This is what lets a browser dApp (Keplr, etc.) submit its very first signed tx without any prior funding step. See [auto-account-creation.md](auto-account-creation.md) for the full flow, the `/auth/account` peek behavior that makes it work, and what to swap in if you ever add a real fee token.

---

## 3. executor/ — Execution Engine

Bridge giữa HTTP API và Cosmos SDK App. Implement `core/execution.Executor` interface.

| File | Vai trò |
|------|---------|
| `executor.go` | **`CosmosExecutor` struct** — orchestrator chính. Quản lý: App instance, mempool, tx results, block history, blob store, state persistence |
| `blob_store.go` | **`BlobStore`** — in-memory content-addressed store (SHA-256). `Put()` → trả hex commitment. `Get()` → retrieve by commitment. `PutBatch()` → store N blobs + tính Merkle root. Thread-safe, có size limits (default 4MB/blob, 256MB total) |
| `persist.go` | **`PersistStore`** — disk persistence cho blobs, tx results, blocks, chain metadata. Files: `metadata.json` (overwrite), `tx_results.jsonl`, `blocks.jsonl`, `blobs.jsonl` (append-only). Replay on startup |

### CosmosExecutor methods

| Method | Chức năng |
|--------|-----------|
| `InitChain(genesisTime, height, chainID)` | Gọi `app.InitChain()` với default genesis. Set chain identity |
| `ExecuteTxs(txs, height, timestamp, prevStateRoot)` | BeginBlock → DeliverTx cho mỗi tx → EndBlock → Commit. Lưu tx results + block info |
| `InjectTx(tx)` | Đưa tx vào mempool, trả tx hash |
| `GetTxs()` | Lấy tất cả tx từ mempool (drain) |
| `GetTxResult(hash)` | Lookup kết quả execution bằng tx hash |
| `QuerySmart(contract, queryMsg)` | Gọi `WasmKeeper.QuerySmart()` — read-only, có gas limit |
| `StoreBlob(data)` | Lưu vào BlobStore, trả SHA-256 commitment |
| `RetrieveBlob(commitment)` | Lấy blob từ BlobStore |
| `StoreBatch(blobs)` | Store N blobs + Merkle root |
| `SetFinal(height)` | Mark block as finalized |
| `GetStatus()` | Trả `StatusInfo` (initialized, height, healthy) |
| `GetLatestBlock()` / `GetBlock(height)` | Block info — includes `tx_hashes` for txs included in that block. See [auto-account-creation.md](auto-account-creation.md#2-tx_hashes-on-blockinfo) |
| `GetAccountInfo(bech32Addr)` | Auth account state. For non-existent addresses returns the **peeked** next `account_number` so a client signing a first tx puts the correct value in `SignDoc`. See [auto-account-creation.md](auto-account-creation.md) |
| `GetStats()` | Runtime metrics (blob count, tx count, mempool size) |

### Options

| Option | Vai trò |
|--------|---------|
| `WithQueryGasMax(gas)` | Gas limit cho WASM smart queries (default 50M) |
| `WithBlobStoreLimits(maxBlob, maxTotal)` | Custom blob store size limits |
| `WithPersistence(dir, &err)` | Enable disk persistence + replay on startup |

---

## 4. config/ — Server Configuration

| File | Vai trò |
|------|---------|
| `config.go` | `Config` struct — tất cả tuneable fields cho cosmos-exec-grpc server |

### Config fields

| Category | Fields |
|----------|--------|
| **Server** | `ListenAddr` (default `0.0.0.0:50051`), `Home`, `InMemory` |
| **Execution** | `BlockTime` (2s), `QueryGasMax` (50M) |
| **Blob store** | `MaxBlobSize` (4MB), `MaxStoreTotalSize` (256MB) |
| **Persistence** | `PersistBlobs`, `PersistTxResults`, `DataDir` |
| **Security** | `AuthToken`, `CORSAllowOrigin`, `MaxRequestBodyBytes` (10MB), `RateLimitRPS`, `ReadOnlyMode` |
| **Metrics** | `MetricsEnabled`, `MetricsAddr` (127.0.0.1:9090) |
| **Timeouts** | `ReadTimeout` (30s), `WriteTimeout` (30s), `IdleTimeout` (120s) |

### Profiles

| Profile | Đặc điểm |
|---------|-----------|
| `dev` (default) | In-memory optional, no rate limit, CORS `*`, no persistence |
| `test` | In-memory, random port, error-only log, 16MB store |
| `prod` | Persistence on, rate limit 100 RPS, CORS restricted, metrics on, 1GB store |

### Environment variables

Tất cả có prefix `COSMOS_EXEC_`: `COSMOS_EXEC_LISTEN_ADDR`, `COSMOS_EXEC_BLOCK_TIME`, `COSMOS_EXEC_AUTH_TOKEN`, `COSMOS_EXEC_PROFILE`, etc.

---

## 5. cmd/cosmos-exec-grpc/ — HTTP API Server

Binary chính mà SDK kết nối tới.

| File | Vai trò |
|------|---------|
| `main.go` | Entry point: parse flags → load config → open DB → create App → create Executor → register HTTP handlers → start server + block producer goroutine |
| `middleware.go` | `securityMiddleware()` — CORS, auth (Bearer token), body size limit, rate limiting (per-IP), read-only mode |
| `metrics.go` | Prometheus metrics: request count, latency histogram, active connections |
| `swagger.go` | Swagger UI tại `/swagger`, OpenAPI spec tại `/swagger.json` |
| `handlers_test.go` | HTTP handler unit tests |
| `middleware_test.go` | Security middleware tests |
| `metrics_test.go` | Metrics tests |
| `integration_test.go` | Full API integration tests |

### HTTP Endpoints (SDK gọi tới đây)

| Method | Path | Chức năng |
|--------|------|-----------|
| GET | `/status` | Chain status (height, health) |
| POST | `/tx/submit` | Submit tx bytes |
| POST | `/tx/submit-base64` | Submit tx as base64 |
| GET | `/tx/{hash}` | Get tx execution result |
| POST | `/blob/submit` | Store blob, trả commitment |
| GET | `/blob/{commitment}` | Retrieve blob by commitment |
| POST | `/blob/batch` | Store batch, trả root + commitments |
| POST | `/query/smart` | WASM smart query (read-only) |
| POST | `/commit/root` | CommitRoot (batch + Merkle + on-chain tx) |
| GET | `/swagger` | Swagger UI |
| GET | `/metrics` | Prometheus metrics (nếu enabled) |

### Startup flow

```
main()
  ├─ config.ForProfile("dev") + LoadFromEnv() + CLI flags
  ├─ openDatabase(dataDir, inMemory)
  ├─ app.New(logger, db)                    ← Cosmos SDK app
  ├─ executor.New(app, opts...)             ← execution engine
  │    ├─ WithQueryGasMax(50M)
  │    ├─ WithBlobStoreLimits(4MB, 256MB)
  │    └─ WithPersistence(dataDir, &err)    ← replay từ disk
  ├─ executor.InitChain(...)                ← khởi tạo chain state
  ├─ registerHandlers(mux, executor)        ← mount HTTP endpoints
  ├─ securityMiddleware(mux, secCfg)        ← wrap auth/CORS/rate
  ├─ go blockProducer(executor, blockTime)  ← tạo blocks định kỳ
  └─ http.ListenAndServe(listenAddr)        ← start server
```

---

## 6. cmd/ — Other CLI Binaries

| Binary | Path | Vai trò |
|--------|------|---------|
| `cosmos-exec` | `cmd/cosmos-exec/main.go` | Standalone executor (không có HTTP server) |
| `cosmos-wasm-tx` | `cmd/cosmos-wasm-tx/main.go` | CLI tool: build + encode CosmWasm transactions (store, instantiate, execute). Debug tool |
| `dal-sdk` | `cmd/dal-sdk/main.go` | CLI tool: SDK operations (blob submit/retrieve, commit, query, start chain). Demo tool |

---

## 7. apps/cosmos-wasm/ — Full Node Binary (evcosmos)

Binary riêng chạy rollup full node. Kết nối tới executor qua gRPC.

| File | Vai trò |
|------|---------|
| `main.go` | Entry point |
| `cmd/init.go` | `evcosmos init` — khởi tạo chain data directory |
| `cmd/run.go` | `evcosmos start` — chạy full node (sequencer, P2P, DA sync, block production) |

### Commands

```bash
# Khởi tạo
./evcosmos init --root-dir ~/.evcosmos --chain-id cosmos-wasm-test-chain

# Chạy full node
./evcosmos start \
  --root-dir ~/.evcosmos \
  --grpc-executor-url http://localhost:50051 \
  --da.address http://localhost:7980
```

---

## 8. scripts/ — Dev Scripts

| Script | Vai trò |
|--------|---------|
| `run-cosmos-wasm-nodes.go` | **Start full E2E stack**: sequencer + full node + 2 execution services (ports 50051, 50052). Dùng build tag `run_cosmos_wasm` |
| `run-cosmos-chain.sh` | Start Cosmos chain (shell script) |
| `deploy-sample-contract.sh` | Deploy sample WASM contract |
| `submit-tx.sh` | Submit transaction via curl |
| `verify-da-submit.sh` | Verify DA blob submission |

---

## Quan hệ giữa các component

```
sdk/cosmoswasm/
├── sdk_config.go ──→ client.go ──→ blob.go      (HTTP POST /blob/submit)
│                         │
│                         ├──→ commit.go          (POST /commit/root)
│                         │        ├──→ proof.go   (→ internal/merkle)
│                         │        └──→ tx.go      (→ internal/txcodec)
│                         │
│                         ├──→ batch.go           (BatchBuilder → commit.go)
│                         │
│                         └──→ da_bridge.go       (DAClient + ExecutorClient)
│
├── chain.go ──→ internal/devchain ──→ scripts/run-cosmos-wasm-nodes.go
│
└── [HTTP calls] ──→ cmd/cosmos-exec-grpc/
                         │
                         ├──→ executor/executor.go
                         │        ├──→ app/app.go (Cosmos SDK + WASM runtime)
                         │        ├──→ executor/blob_store.go
                         │        └──→ executor/persist.go
                         │
                         └──→ config/config.go
```

---

## API Tiers

| Tier | Stability | Bao gồm |
|------|-----------|----------|
| **Tier 1 — Core** | Stable | `NewClient`, `SubmitBlob`, `CommitRoot`, `QuerySmart`, `BatchBuilder`, `SDKError` |
| **Tier 2 — Power-user** | Stable | Tx builders, Namespace, DAClient, DABridge, Merkle proof, Chunk, Compress, Cost |
| **Tier 3 — Dev tooling** | May change | `MockExecutorClient`, `MockDAClient`, `StartDALChain` |
| **internal/** | Private | merkle, compress, chunk, txcodec, devchain |
| **executor/** | Server-side | `CosmosExecutor`, `BlobStore`, `PersistStore` — không phải SDK public API |
| **app/** | Server-side | Cosmos SDK App + modules — không phải SDK public API |
| **config/** | Server-side | Server config — không phải SDK public API |

Tier 1 + 2 = stable contract. Breaking changes chỉ ở major version.
