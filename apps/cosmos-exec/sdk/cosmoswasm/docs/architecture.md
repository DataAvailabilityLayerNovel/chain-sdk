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
| `executor_client.go` | `ExecutorClient` interface — transport abstraction (HTTP / gRPC) |
| `defaults.go` | Default constants: `DefaultPollInterval`, `DefaultTxTimeout` |
| `errors.go` | `SDKError` struct (Op, Cause, Hint) + sentinel: `ErrNotReachable`, `ErrBlobTooLarge`, `ErrBlobStoreFull`, `ErrTxFailed`, `ErrContractMissing`, `ErrCommitMissing` |
| `types.go` | Request/response types: `SubmitTxResponse`, `GetTxResultResponse`, `TxExecutionResult`, `TxEvent`, `TxEventAttribute`, `NodeStatus`, `FinalityLevel`, `QuerySmartResponse`, `InstantiateTxRequest`, `ExecuteTxRequest`, `BlobSubmitResponse`, `BlobRetrieveResponse`, `BlobCommitTxRequest`, `BlobBatchResponse`, `BatchRootTxRequest` |

### Blob-first DA (Celestia) & Data Integrity

| File | Vai trò |
|------|---------|
| `blob.go` | `BlobClient` + `BlobClientConfig`, `NewBlobClient()`, `SubmitBlob()`, `RetrieveBlob(height, commitment)`, `SubmitBatch()`, `VerifyBlob()` — **JSON-RPC thẳng tới Celestia bridge** (không qua executor) |
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
