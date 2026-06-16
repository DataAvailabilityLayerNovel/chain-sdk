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
| `app.go` | **`App` struct** — wrap `baseapp.BaseApp` (Cosmos SDK). Khởi tạo tất cả modules + keepers. Implement `InitChainer`, `BeginBlocker`, `EndBlocker` (gồm cả `sweepFeesToTreasury` — xem mục bên dưới) |
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
4. EndBlock(height)       → ModuleManager.EndBlock → sweepFeesToTreasury
5. Commit()               → persist state, trả app_hash (state root)
```

### Sweep phí về treasury (thay cho `x/distribution`)

`App.EndBlocker` ([`app.go:365-399`](../../app.go#L365-L399)) gọi `sweepFeesToTreasury` *sau* `ModuleManager.EndBlock`. Hàm này đọc địa chỉ treasury từ env `COSMOS_EXEC_TREASURY_ADDR`, lấy toàn bộ balance của module account `fee_collector` qua `BankKeeper.GetAllBalances`, và chuyển trọn vẹn sang treasury bằng `BankKeeper.SendCoinsFromModuleToAccount`.

Quy ước:

- Env không set / bech32 sai → no-op, phí ở lại `fee_collector` (mặc định dev).
- Mọi node trong mạng **phải** set cùng giá trị `COSMOS_EXEC_TREASURY_ADDR` — config drift sẽ gây fork (`app_hash` lệch). Cùng ràng buộc với `COSMOS_EXEC_MIN_GAS_PRICE` / `COSMOS_EXEC_GAS_DENOM`.
- Treasury có thể là EOA, multisig, hoặc địa chỉ contract CosmWasm (mở đường cho treasury-DAO).

Pattern này thay vai trò của `x/distribution` + `x/staking` trong rollup không có validator set. Chi tiết thiết kế và đo lường trong [fee-economics.md §6 — "Sweep phí cuối block"](fee-economics.md#sweep-phi-cuoi-block).

### AnteHandler chain (ante.go)

`NewPermissionlessAnteHandler` ([`ante.go:257-285`](../../ante.go#L257-L285)) ghép các decorator theo đúng thứ tự:

```
SetUpContext → ExtensionOptions → ValidateBasic → TxTimeoutHeight
→ ValidateMemo → ConsumeGasForTxSize
→ AutoCreateAccount       (custom — tự tạo account chưa tồn tại)
→ DeductFee               (txFeeChecker đọc COSMOS_EXEC_MIN_GAS_PRICE)
→ SetPubKey → ValidateSigCount → SigGasConsume → SigVerification
→ IncrementSequence
```

Hai khía cạnh đáng chú ý:

- **`AutoCreateAccount`** *phải* chạy trước `DeductFee` và `SetPubKey` để fee payer luôn tồn tại trong state khi các decorator phía sau lookup. Đây là cơ chế cho phép browser dApp (Keplr/OKX/Leap) submit tx ký lần đầu mà không cần một bước fund trước. Chi tiết: [auto-account-creation.md](auto-account-creation.md).
- **`DeductFee`** sử dụng `txFeeChecker(feePolicyFromEnv())` ([`ante.go:138-180`](../../ante.go#L138-L180)). Khi `COSMOS_EXEC_MIN_GAS_PRICE` không set hoặc ≤ 0 → policy OFF, chấp nhận tx phí 0 (mặc định dev). Khi set > 0 → enforce `fee ≥ ceil(gas × minGasPrice)` tính theo `COSMOS_EXEC_GAS_DENOM`, thiếu thì `ErrInsufficientFee`. Simulate (`ExecModeSimulate`) luôn được skip để tránh chicken-and-egg với `/tx/simulate`. Chi tiết: [fee-economics.md](fee-economics.md).

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

Tất cả prefix `COSMOS_EXEC_`. Hai nhóm dưới đây *quan trọng nhất* — nhóm thứ hai có ảnh hưởng tới state transition nên **mọi node trong mạng phải set giá trị giống nhau**, lệch sẽ gây fork.

| Nhóm | Biến | Ảnh hưởng |
|------|------|-----------|
| Server | `LISTEN_ADDR`, `BLOCK_TIME`, `AUTH_TOKEN`, `CORS_ALLOW_ORIGIN`, `RATE_LIMIT_RPS`, `PROFILE` (`dev`/`test`/`prod`) | Vận hành (per-node) |
| Persistence | `PERSIST_TX_RESULTS`, `DATA_DIR` | Vận hành (per-node) |
| Metrics | `METRICS_ENABLED`, `METRICS_ADDR` | Vận hành (per-node) |
| **State transition (đồng bộ giữa các node)** | `MIN_GAS_PRICE`, `GAS_DENOM`, `TREASURY_ADDR`, `ENFORCE_SIGNATURES` | Đầu vào của AnteHandler và EndBlocker — lệch giữa các node sẽ làm `app_hash` divergent và rollup fork |
| Faucet | `TREASURY_PRIVKEY_HEX`, `TREASURY_AMOUNT`, `FAUCET_AMOUNT`, `FAUCET_COOLDOWN_SECS` | Mở route `/faucet`; xem [fee-economics.md](fee-economics.md) |

Lưu ý phân biệt **hai biến treasury khác nhau** mà tên dễ nhầm:

- `COSMOS_EXEC_TREASURY_PRIVKEY_HEX` — *private key* ký giao dịch faucet (đặt ở server faucet).
- `COSMOS_EXEC_TREASURY_ADDR` — *địa chỉ* nhận phí được EndBlocker quét từ `fee_collector` (deterministic, mọi node phải giống).

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

### HTTP Endpoints

Toàn bộ route được đăng ký tập trung tại [`cmd/cosmos-exec-grpc/main.go:143-172`](../../cmd/cosmos-exec-grpc/main.go#L143-L172). Khi đối chiếu với SDK, đây là tập endpoint mà `Client` gọi tới — và cũng là tập endpoint mà dApp web `my-dapp-web` dùng trực tiếp.

| Method | Path | Chức năng |
|--------|------|-----------|
| POST | `/tx/submit` | Submit tx bytes (base64/hex), trả tx hash |
| POST | `/tx/estimate` | Ước tính gas + chi phí DA mà không thực thi |
| POST | `/tx/simulate` | Chạy thử tx để đo gas, không persist state |
| GET  | `/tx/result?hash=...` | Lookup kết quả execution theo hash |
| GET  | `/tx/{hash}` | Như trên, dùng đường dẫn |
| GET  | `/tx/pending` | Danh sách tx đang ở mempool |
| POST | `/wasm/query-smart` | WASM smart query (read-only, có gas limit) |
| GET  | `/blocks/latest` | Block mới nhất kèm tx_hashes |
| GET  | `/blocks/{height}` | Chi tiết block theo height |
| GET  | `/status` | Chain status (height, DA height, healthy) |
| GET  | `/auth/account/{address}` | Account info (number, sequence). Trả "peek" cho địa chỉ chưa tồn tại — xem [auto-account-creation.md](auto-account-creation.md) |
| GET  | `/bank/balance/{address}` | Số dư theo address |
| GET  | `/cosmos/bank/v1beta1/balances/{address}` | LCD shim cho Keplr đọc balance |
| GET  | `/cosmos/bank/v1beta1/balances/{address}/by_denom` | LCD shim cho Keplr (theo denom) |
| GET  | `/exec/height` | Height đã thực thi (cho ev-node sync) |
| POST | `/exec/rollback` | Rollback state về height N (admin) |
| POST | `/exec/prune` | Prune lịch sử cũ |
| POST/GET | `/faucet?addr=...` | Cấp test token (chỉ mở khi `COSMOS_EXEC_TREASURY_PRIVKEY_HEX` set) |
| GET  | `/health`, `/healthz` | Liveness check |
| GET  | `/ready` | Readiness check |
| GET  | `/metrics` | Prometheus text metrics |
| GET  | `/metrics.json` | Metrics dạng JSON |
| GET  | `/swagger` | Swagger UI |
| GET  | `/swagger.json` | OpenAPI 3.0 spec |

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

## 6. apps/cosmos-wasm/ — Full Node Binary (evcosmos)

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

## 7. scripts/ — Dev Scripts

| Script | Vai trò |
|--------|---------|
| `run-cosmos-wasm-nodes.go` | **Start full E2E stack**: sequencer + full node + 2 execution services (ports 50051, 50052). Dùng build tag `run_cosmos_wasm` |
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
