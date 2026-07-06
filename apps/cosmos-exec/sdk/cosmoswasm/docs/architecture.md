# Cosmos SDK Architecture

Tài liệu này mô tả kiến trúc toàn bộ Cosmos/WASM stack — từ SDK package cho đến các thành phần bên dưới mà SDK phụ thuộc để chạy được.

## Mục lục

- Tổng quan hệ thống
- 1. sdk/cosmoswasm/ — Public SDK Package
- 2. app/ — Cosmos SDK Application
- 3. executor/ — Execution Engine
- 4. config/ — Server Configuration
- 5. cmd/cosmos-exec-grpc/ — HTTP API Server
- 6. apps/cosmos-wasm/ — Full Node Binary (evcosmos)
- 7. scripts/ — Dev Scripts
- 8. ev-node framework — các folder gốc mà evcosmos build trên
- Quan hệ giữa các component
- API Tiers

## Tổng quan hệ thống

```
┌─────────────────────────────────────────────────────────────┐
│  User App (Go)                                              │
│  import cosmoswasm ".../sdk/cosmoswasm"                     │
│  client.SubmitTxBytes / QuerySmart / BuildExecuteTx         │
│  blobClient.SubmitBlob / SubmitBatch (JSON-RPC → Celestia)  │
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
│  WASM runtime + mempool + state persistence                  │
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
│  evcosmos                  apps/cosmos-wasm/                 │
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

> ⚠️ Cập nhật theo refactor `ea844067`: tầng blob/DA cũ (`commit.go`,
> `batch.go`, `proof.go`, `cost.go`, `da_client.go`, `da_bridge.go`,
> `mock_*.go`) **đã bị gỡ**. Blob giờ đi thẳng lên Celestia qua `BlobClient`
> (JSON-RPC), không qua HTTP executor.

### Core — Client + Config

| File | Vai trò |
|------|---------|
| `sdk.go` | Package doc — phân loại API tier (Tier 1/2/3) |
| `doc.go` | Doc tổng quan package |
| `sdk_config.go` | `SDKConfig`, `DefaultSDKConfig()`, `NewClientFromConfig()` — cấu hình timeout, retry, auth |
| `client.go` | `Client` struct — method HTTP tới cosmos-exec: `SubmitTxBytes`, `SubmitTxBase64`, `GetTxResult`, `WaitTxResult`, `Status`, `GetTxFinality`, `WaitTxFinality`, `QuerySmart`, `QuerySmartRaw`, `FetchAccount` |
| `client_extra.go` | Method `Client` bọc thêm endpoint dev-facing: `SimulateTx` (`/tx/simulate`), `EstimateCost` (`/tx/estimate`), `GetLatestBlock` / `GetBlockByHeight` (`/blocks/*`), `GetPendingTxCount` (`/tx/pending`). Kèm type `Coin`, `SimulateResponse`, `CostBreakdown`, `EstimateRequest`, `BlockInfo` |
| `executor_client.go` | `ExecutorClient` interface — transport abstraction (HTTP / gRPC) |
| `defaults.go` | Default constants: `DefaultPollInterval`, `DefaultTxTimeout` |
| `errors.go` | `SDKError` struct (Op, Cause, Hint) + sentinel: `ErrNotReachable`, `ErrBlobTooLarge`, `ErrBlobStoreFull`, `ErrTxFailed`, `ErrContractMissing`, `ErrCommitMissing` |
| `types.go` | Request/response types: `SubmitTxResponse`, `GetTxResultResponse`, `TxExecutionResult`, `TxEvent`, `TxEventAttribute`, `NodeStatus`, `FinalityLevel`, `QuerySmartResponse`, `InstantiateTxRequest`, `ExecuteTxRequest`, `BlobSubmitResponse`, `BlobRetrieveResponse`, `BlobCommitTxRequest`, `BlobBatchResponse`, `BatchRootTxRequest` |

### Blob-first DA (Celestia) & Data Integrity

| File | Vai trò |
|------|---------|
| `blob.go` | `BlobClient` + `BlobClientConfig`, `NewBlobClient()`, `SubmitBlob()`, `RetrieveBlob(height, commitment)`, `SubmitBatch()`, `VerifyBlob()` — **JSON-RPC thẳng tới Celestia bridge** (không qua executor) |
| `blob_record.go` | `StoreBlobAndRecord()` / `StoreBatchAndRecord()` — gộp 1 call: upload blob lên DA + ghi commitment on-chain, message do dev dựng qua callback (không ép convention `record_blob`) |
| `merkle.go` | `MerkleProof`, `ProofStep`. `BuildMerkleProof()`, `VerifyMerkleProof()`. Thin wrapper → `internal/merkle` |
| `chunk.go` | `ChunkBlob()` / `ReassembleChunks()` + `ChunkMeta`. Thin wrapper → `internal/chunk` |
| `compress.go` | `CompressGzip()`, `DecompressGzip()`, `CompressIfBeneficial()`, `MaybeDecompress()`, `IsGzipCompressed()`. Thin wrapper → `internal/compress` |
| `namespace.go` | `Namespace` type, `NamespaceFromString()`, `NamespaceFromHex()`, `NewNamespaceV0()` — Celestia namespace 29 bytes |

### Transaction Building

| File | Vai trò |
|------|---------|
| `tx.go` | Tx builders, delegate → `internal/txcodec`: `BuildStoreTx`, `BuildInstantiateTx`, `BuildExecuteTx`, `BuildBlobCommitTx` (`record_blob`), `BuildBatchRootTx` (`record_batch`), `EncodeTxBase64`, `EncodeTxHex`, `DefaultSender` |
| `signer.go` | `Signer`, `NewSignerFromHex`, `WithGasLimit`/`WithFee`/`Address`, và `BuildSignedStoreTx`/`BuildSignedInstantiateTx`/`BuildSignedExecuteTx`/`BuildSignedBankSend`/`BuildSignedBankSendWithFee` |

### Dev Tooling (Tier 3)

| File | Vai trò |
|------|---------|
| `chain.go` | `DALChainConfig`, `DALChainEndpoints`, `DALChainProcess`, `StartDALChain()`, `DefaultDALChainConfig()`. Thin wrapper → `internal/devchain`. Start full chain từ Go code (dev/test) |

### internal/ — Implementation details

Go compiler cấm external code import `internal/`. Refactor tự do mà không break user code.

| Package | Vai trò |
|---------|---------|
| `internal/merkle` | Binary SHA-256 Merkle tree |
| `internal/compress` | Gzip BestSpeed, magic byte detection, conditional compression |
| `internal/chunk` | Split blob theo max size, `ChunkMeta` cho reassembly |
| `internal/txcodec` | Protobuf tx encoding |
| `internal/devchain` | Local chain process: `Config`, `Start()` |

### Tests

`client_test.go`, `blob_test.go`, `tx_test.go`, `namespace_test.go`,
`dataintegrity_test.go` (Merkle/chunk/compress), `chain_test.go`.

### examples/

| Example | Chức năng |
|---------|-----------|
| `my-counter` | Vòng đời WASM đầy đủ: store → instantiate → execute → query (counter contract) |
| `forced-inclusion` | Demo chống censorship: post tx thẳng lên DA |
| `game-telemetry` | Blob-first: bulk telemetry lên Celestia, ghi commitment on-chain |

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
| `executor.go` | **`CosmosExecutor` struct** — orchestrator chính. Quản lý: App instance, mempool, tx results, block history, state persistence. (Không có blob store — blob đi thẳng Celestia qua SDK `BlobClient`.) |
| `persist.go` | **`PersistStore`** — disk persistence cho tx results, blocks, chain metadata. Files: `metadata.json` (overwrite), `tx_results.jsonl`, `blocks.jsonl` (append-only). Replay on startup |

### CosmosExecutor methods

| Method | Chức năng |
|--------|-----------|
| `InitChain(genesisTime, height, chainID)` | Gọi `app.InitChain()` với default genesis. Set chain identity |
| `ExecuteTxs(txs, height, timestamp, prevStateRoot)` | BeginBlock → DeliverTx cho mỗi tx → EndBlock → Commit. Lưu tx results + block info |
| `InjectTx(tx)` | Đưa tx vào mempool, trả tx hash |
| `GetTxs()` | Lấy tất cả tx từ mempool (drain) |
| `GetTxResult(hash)` | Lookup kết quả execution bằng tx hash |
| `QuerySmart(contract, queryMsg)` | Gọi `WasmKeeper.QuerySmart()` — read-only, có gas limit |
| `SimulateTx(txBytes)` | Chạy thử tx đo gas, không commit |
| `GetBalance(addr, denom)` | Số dư account |
| `FilterTxs(txs, maxBytes, ...)` | Lọc tx theo tổng byte (Remove/Postpone/OK) |
| `SetFinal(height)` | Mark block as finalized |
| `GetStatus()` | Trả `StatusInfo` (initialized, height, healthy) |
| `GetLatestBlock()` / `GetBlock(height)` | Block info — includes `tx_hashes` for txs included in that block. See [auto-account-creation.md](auto-account-creation.md#2-tx_hashes-on-blockinfo) |
| `GetAccountInfo(bech32Addr)` | Auth account state. For non-existent addresses returns the **peeked** next `account_number` so a client signing a first tx puts the correct value in `SignDoc`. See [auto-account-creation.md](auto-account-creation.md) |
| `GetStats()` | Runtime metrics (blob count, tx count, mempool size) |

### Options

| Option | Vai trò |
|--------|---------|
| `WithQueryGasMax(gas)` | Gas limit cho WASM smart queries (default 50M) |
| `WithGenesis(genesisJSON)` | Override genesis lúc InitChain (vd cấp tiền treasury) |
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

## 8. ev-node framework — các folder gốc mà evcosmos build trên

> **Có cần dùng không? — Có.** `evcosmos` (mục 6) không phải binary độc lập: nó
> là một rollup full node *build trên ev-node framework*. Các capability mà mục
> 6 liệt kê — "sequencer, P2P, block production, DA sync" — đều đến từ các folder
> gốc của repo (`block/`, `core/`, `node/`, `pkg/…`). `apps/cosmos-wasm/cmd/run.go`
> import trực tiếp các package này; `executor_client.go` implement interface
> `core/execution.Executor` rồi cắm nó vào framework.

Nói cách khác, toàn bộ stack ở mục 1–6 là **execution side** (chạy WASM, trả
state root); các folder dưới đây là **consensus/replication side** (sắp xếp tx,
nhân bản block qua P2P, đẩy/đọc DA). evcosmos là điểm khâu hai phía lại:

```
evcosmos (apps/cosmos-wasm/)
   │  implement core/execution.Executor  →  EnhancedExecutorClient
   │      └─[HTTP/Connect-RPC]→ cosmos-exec-grpc (execution side, mục 1–5)
   │                                 └─ serve bằng execution/grpc handler
   │  wire vào ev-node framework:
   ├──→ node/        assemble full/light node
   ├──→ block/       sản xuất + đồng bộ + validate block
   ├──→ pkg/sequencers/{single,based}   sắp xếp tx
   ├──→ pkg/p2p      libp2p gossip + DHT
   ├──→ pkg/store    block/state store (badger)
   ├──→ pkg/da       client JSON-RPC tới Celestia
   └──→ core/, types/, pkg/{config,genesis,cmd}
```

### Folder runtime — evcosmos phụ thuộc trực tiếp

| Folder | Vai trò | evcosmos dùng vào |
|--------|---------|-------------------|
| `core/` | Package **zero-dependency** (có `go.mod` riêng) chỉ chứa interface + type: `core/execution.Executor`, `core/sequencer.Sequencer`, `core/da`. Mọi module khác chỉ phụ thuộc `core` để tránh import vòng. | `EnhancedExecutorClient` implement `core/execution.Executor`; sequencer implement `core/sequencer.Sequencer` |
| `types/` | Cấu trúc dữ liệu block chạy qua toàn hệ: `Header`, `SignedHeader`, `Data`, `State`, hashing, serialization (`types/pb` = protobuf generated). | Block do `block/` sản xuất, `pkg/store` lưu, `pkg/p2p` gossip |
| `block/` | **Block manager**: aggregation (sản xuất block từ tx do sequencer trả), sync (kéo block từ peer/DA), validate, áp state transition bằng cách gọi `Executor`. `public.go` là API, `internal/` là chi tiết. | Lõi block production + DA sync của full node |
| `node/` | Lắp ráp **full node** (`full.go`) và **light node** (`light.go`): khâu executor + sequencer + P2P + store + DA + block manager thành một node chạy được; có failover, setup, helpers. | `run.go` gọi `node.NewNode(...)` |
| `pkg/sequencers/` | Hai sequencer impl: `single` (single-sequencer mặc định) và `based` (based sequencer — lấy thứ tự tx thẳng từ DA, chống censorship). | Chọn theo cờ khi `evcosmos start` |
| `pkg/p2p/` | Networking libp2p: GossipSub (phát tán header/block), Kademlia DHT (peer discovery), `key` (P2P identity). | Nhân bản block giữa các node |
| `pkg/da/` | Client DA: `jsonrpc` (gọi Celestia bridge), `types` (interface `DA`). Đây là đường đẩy/đọc blob block lên Celestia ở tầng consensus. | DA submit + sync của full node |
| `pkg/store/` | Block & state store trên đĩa (badger KV). | Lưu block đã commit, metadata chain |
| `pkg/config/` | Config node ev-node (`rollconf`): root-dir, DA address, P2P, sequencer mode. | Đọc config lúc `init`/`start` |
| `pkg/genesis/` | Load + validate genesis chung của ev-node. | Khởi tạo chain identity |
| `pkg/cmd/` | Helper CLI dùng chung (`rollcmd`): flags, lifecycle start/stop chuẩn. | `init.go` / `run.go` build trên đó |
| `execution/grpc/` | Handler + server + client Connect-RPC cho `core/execution.Executor`. | **Server-side dùng thật**: cosmos-exec-grpc (mục 5) gọi `execgrpc.NewExecutorServiceHandlerWithMux(cosmosExecutor, …)` ([main.go:140](../../cmd/cosmos-exec-grpc/main.go#L140)) để expose Executor; `EnhancedExecutorClient` của evcosmos là client nói chuyện với handler này |
| `proto/` | Nguồn protobuf (`proto/evnode/v1/*`, `proto/execution/*`). Sinh ra Go (`types/pb`) và Rust (`client/crates`). | Không import lúc runtime; là **source-of-truth** cho wire format |

`pkg/` còn các tiện ích hạ tầng dùng gián tiếp: `hash`, `os`, `raft` (sequencer HA), `rpc`, `service` (lifecycle base), `signer`, `sync`, `telemetry`.

### Tầng dưới của `block/` — `block/internal/*`

`block/public.go` chỉ là mặt API mỏng. Toàn bộ logic nằm trong `block/internal/`,
chia theo *vai trò trong vòng đời block* (mỗi sub-package là một "trạm" trên đường
đi của block). Compiler chặn import từ ngoài → các trạm này refactor tự do.

| Sub-package | Vai trò | File chính |
|-------------|---------|------------|
| `internal/executing` | **Aggregation / block production.** Sequencer (leader) sản xuất block mới: gom tx, dựng header+data, gọi `Executor.ExecuteTxs` để áp state transition rồi lấy state root. | `block_producer.go`, `executor.go`, `pending.go`, `utils.go` |
| `internal/reaping` | **Reaper** — định kỳ kéo tx từ mempool của execution layer (`Executor.GetTxs`) đẩy vào sequencer để chờ đóng block. | `reaper.go` |
| `internal/syncing` | **Sync path (follower).** Node không phải leader kéo block về và replay: lấy từ P2P (`p2p_handler.go`), từ DA (`da_retriever.go`, `da_follower.go`), hoặc từ Raft (`raft_retriever.go`); `syncer.go`/`block_syncer.go` điều phối, validate, áp state. | `syncer.go`, `block_syncer.go`, `da_follower.go`, `da_retriever.go`, `p2p_handler.go`, `raft_retriever.go` |
| `internal/submitting` | **DA submitter.** Đóng gói block đã commit thành blob và đẩy lên Celestia; `batching_strategy.go` quyết định gom bao nhiêu block / kích thước mỗi lần submit. | `submitter.go`, `da_submitter.go`, `batching_strategy.go` |
| `internal/da` | **Adapter `block ↔ pkg/da`.** Bọc client DA cho block manager: kéo block bất đồng bộ (`async_block_retriever.go`), lấy tx forced-inclusion thẳng từ DA (`forced_inclusion_retriever.go`), tracing. | `client.go`, `async_block_retriever.go`, `forced_inclusion_retriever.go`, `interface.go` |
| `internal/cache` | **Cache block/header pending.** Giữ header & data đã nhận nhưng *chưa* được DA xác nhận / chưa tới lượt apply, tránh fetch lại; `manager.go` điều phối, `pending_headers.go`/`pending_data.go` là hàng đợi pending, `generic_cache.go` là cache key→value tổng quát. | `manager.go`, `generic_cache.go`, `pending_headers.go`, `pending_data.go`, `pending_base.go` |
| `internal/pruner` | **Pruner** — xoá block/state cũ vượt ngưỡng giữ lại để giới hạn dung lượng đĩa. | `pruner.go` |
| `internal/common` | **Shared kit** cho tất cả trạm trên: hằng số, error, event, metrics, options, helper `raft`, `replay`, `retry`, và `expected_interfaces.go` (các interface mà block manager kỳ vọng từ executor/sequencer/DA/store). | `consts.go`, `errors.go`, `event.go`, `metrics.go`, `options.go`, `expected_interfaces.go`, `retry.go`, `replay.go` |

Dòng chảy gọn: `reaping` → `executing` (leader sản xuất) → `submitting` (đẩy DA);
song song, follower chạy `syncing` (kéo qua P2P/DA) → áp state; `da`/`cache` phục
vụ cả hai chiều, `pruner` dọn dẹp nền.

### Tầng dưới của `node/` và `pkg/*`

| Folder | Sub-folder / file | Vai trò |
|--------|-------------------|---------|
| `node/` | `full.go` / `light.go` | Lắp ráp full node (có block production + DA submit) vs light node (chỉ verify header). |
| `node/` | `node.go`, `setup.go`, `helpers.go`, `failover.go` | Interface `Node` chung, wiring khởi tạo, helper, và failover leader (Raft) cho HA. |
| `pkg/p2p/` | `client.go`, `peer.go`, `rpc.go`, `metrics.go` | Lõi libp2p: kết nối, quản lý peer, RPC giữa node, metrics. |
| `pkg/p2p/` | `key/` (`nodekey.go`) | P2P identity — sinh/đọc khoá định danh node trên mạng. |
| `pkg/da/` | `types/` (`types.go`, `blob.go`, `header.go`, `namespace.go`, `errors.go`) | Interface `DA` + kiểu blob/namespace/error — abstraction tầng DA (không gắn Celestia). |
| `pkg/da/` | `jsonrpc/` (`client.go`, `blob.go`, `submit_options.go`) | Triển khai cụ thể: client JSON-RPC nói chuyện với Celestia bridge. |
| `pkg/sequencers/` | `single/` (`sequencer.go`, `queue.go`) | Single-sequencer mặc định + hàng đợi tx in-memory. |
| `pkg/sequencers/` | `based/` (`sequencer.go`) | Based sequencer — lấy thứ tự tx thẳng từ DA (chống censorship). |
| `pkg/sequencers/` | `common/` (`checkpoint.go`) | Dùng chung: checkpoint, mock forced-inclusion. |
| `pkg/store/` | `store.go`, `cached_store.go`, `batch.go`, `kv.go`, `keys.go`, `badger_options.go` | Block/state store trên badger KV: store gốc, lớp cache, batch ghi, schema key, tuỳ chọn badger. |
| `pkg/signer/` | `local.go`, `noop/`, `file/` | Ký header/block: in-memory, no-op (test), và khoá lưu file. |

### Folder phụ trợ — KHÔNG nằm trong runtime path, nhưng KHÔNG nên xoá

| Folder | Vai trò | Tại sao giữ lại |
|--------|---------|------------------------|
| `client/` | Thư viện client đa ngôn ngữ cho **người dùng ngoài**: `crates/` (Rust: `ev-types`, `ev-client`), `go/`. | Không file Go nào trong repo import, nhưng là **public API** cho app ngoài gọi vào ev-node node (kế thừa từ upstream). Chỉ xoá khi xác nhận bỏ hỗ trợ multi-language client. Lưu ý: SDK Go ở mục 1 nằm tại `apps/cosmos-exec/sdk/cosmoswasm`, **khác** với `client/go/`. |
| `tools/` | CLI debug độc lập: `blob-decoder`, `cosmos-explorer`, `da-debug`, `cache-analyzer`, `db-bench`, `local-da`, `evnode-rpc`, `cosmos-wasm-tx`. | Không bị import, nhưng có **recipe build** trong [`.just/tools.just`](../../../../.just/tools.just) và chứa `cosmos-explorer` / `cosmos-wasm-tx` phục vụ trực tiếp stack này. Là tooling dev/ops chủ động. |

**Tóm tắt:** `core/`, `types/`, `block/`, `node/`, `pkg/*`, `execution/grpc/`, `proto/` là tầng framework bắt buộc nằm dưới (hoặc serve cho) evcosmos — giữ lại và đã giải thích ở trên. `client/` và `tools/` không thuộc đường chạy của stack nhưng là public API + tooling có recipe build, nên **không có folder nào nên xoá**.

---

## Quan hệ giữa các component

```
sdk/cosmoswasm/
├── sdk_config.go ──→ client.go ──[HTTP]──→ cmd/cosmos-exec-grpc/
│                         │                       │
│                         ├──→ tx.go (→ internal/txcodec), signer.go
│                         │
│                         └──→ blob.go ──[JSON-RPC]──→ Celestia bridge
│                                  ├──→ merkle.go  (→ internal/merkle)
│                                  ├──→ chunk.go   (→ internal/chunk)
│                                  └──→ compress.go(→ internal/compress)
│
├── chain.go ──→ internal/devchain ──→ scripts/run-cosmos-wasm-nodes.go
│
└── cmd/cosmos-exec-grpc/
        ├──→ executor/executor.go
        │        ├──→ app/app.go (Cosmos SDK + WASM runtime)
        │        └──→ executor/persist.go
        └──→ config/config.go
```

---

## API Tiers

| Tier | Stability | Bao gồm |
|------|-----------|----------|
| **Tier 1 — Core** | Stable | `NewClient`/`NewClientFromConfig`, `SubmitTxBytes`/`WaitTxResult`, `QuerySmart`, `NewBlobClient`/`SubmitBlob`/`SubmitBatch`, `BuildBatchRootTx`, `SDKError` |
| **Tier 2 — Power-user** | Stable | Tx builders, `Signer` + signed builders, Namespace, Merkle proof, Chunk, Compress |
| **Tier 3 — Dev tooling** | May change | `StartDALChain`, `DALChainConfig`, `DALChainProcess` |
| **internal/** | Private | merkle, compress, chunk, txcodec, devchain |
| **executor/** | Server-side | `CosmosExecutor`, `PersistStore` — không phải SDK public API |
| **app/** | Server-side | Cosmos SDK App + modules — không phải SDK public API |
| **config/** | Server-side | Server config — không phải SDK public API |

Tier 1 + 2 = stable contract. Breaking changes chỉ ở major version.
