# API Reference

Toàn bộ method dưới đây là **public API** của package `cosmoswasm`. Tài liệu này
đối chiếu trực tiếp với source sau refactor `ea844067` — các API cũ
(`DAClient`/`DABridge`, `BatchBuilder`, `CommitRoot`, `EstimateCost`,
`RetrieveBlobData`, mock helpers) **đã bị gỡ**, không còn trong code.

## Mục lục

- [API dành cho ai](#api-dành-cho-ai)
- [Client Setup](#client-setup)
- [Transaction APIs](#transaction-apis)
- [Finality APIs](#finality-apis)
- [Query APIs](#query-apis)
- [Transaction Builders](#transaction-builders)
- [Signed Builders & Signer](#signed-builders--signer)
- [Blob-first DA (BlobClient)](#blob-first-da-blobclient)
- [Ghi commitment / root on-chain](#ghi-commitment--root-on-chain)
- [Data Integrity (Merkle / chunk / compress)](#data-integrity-merkle--chunk--compress)
- [Namespace](#namespace)
- [Errors](#errors)
- [Dev Tooling (local chain)](#dev-tooling-local-chain)

## API dành cho ai

| Tag | Đối tượng | Việc chính |
|-----|-----------|------------|
| 👤 **App dev / chain user** | Lập trình viên app & user của chain | Submit tx, store/retrieve blob, query contract, build & ký tx |
| 🛠️ **Node operator** | Người vận hành executor, DA, chain local | Cấu hình namespace DA, chạy chain local cho test |

Phần lớn API dành cho **app dev**. *Dev Tooling* chủ yếu cho **node operator**.
*Client Setup* và *Errors* áp dụng cho cả hai.

---

## Client Setup

> 👤 **App dev** · 🛠️ **Node operator**

### `NewClient(baseURL) → *Client`

Tạo `Client` cho dev nhanh. Kết nối tới 1 executor endpoint với mặc định
(timeout 20s, không retry, không auth). `baseURL` rỗng → mặc định
`http://127.0.0.1:50051`.

```go
client := cosmoswasm.NewClient("http://127.0.0.1:50051")
```

Production nên dùng `NewClientFromConfig`.

### `NewClientFromConfig(cfg SDKConfig) → (*Client, error)`

Tạo `Client` với toàn quyền control timeout, retry, auth, HTTP transport.

**`SDKConfig`:**

| Field | Type | Default | Mô tả |
|-------|------|---------|-------|
| `ExecURL` | `string` | — | **Bắt buộc.** Base URL của cosmos-exec-grpc |
| `Timeout` | `time.Duration` | `20s` | Timeout HTTP mỗi request |
| `RetryAttempts` | `int` | `0` | Số lần retry khi lỗi tạm thời (connection refused, timeout) |
| `RetryDelay` | `time.Duration` | `1s` | Delay giữa các lần retry |
| `AuthToken` | `string` | `""` | Gửi `Authorization: Bearer <token>` mỗi request |
| `ChainID` | `string` | `""` | Dùng khi build tx cần chain id |
| `HTTPClient` | `*http.Client` | `nil` | Custom HTTP client (TLS, proxy). `nil` → tự tạo với `Timeout` |

Lỗi: trả lỗi khi `ExecURL` rỗng (`cfg.Validate()`).

```go
client, err := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
    ExecURL:       "http://127.0.0.1:50051",
    Timeout:       20 * time.Second,
    RetryAttempts: 3,
    RetryDelay:    1 * time.Second,
    AuthToken:     os.Getenv("COSMOS_EXEC_AUTH_TOKEN"),
})
```

### `DefaultSDKConfig() → SDKConfig`

Trả config với default hợp lý. Phải set `ExecURL` trước khi dùng.

### `Client.WithHTTPClient(httpClient) → *Client`

Trả clone của `Client` với `http.Client` khác — tiện inject TLS/proxy sau khi
tạo client.

---

## Transaction APIs

> 👤 **App dev** — submit tx đã ký và theo dõi kết quả thực thi.

### `Client.SubmitTxBytes(ctx, txBytes []byte) → (*SubmitTxResponse, error)`

Submit tx Cosmos SDK đã ký vào mempool của executor. Internally encode base64
và gửi body `{"tx_base64": "..."}` tới `POST /tx/submit`.

| Param | Type | Bắt buộc | Ghi chú |
|-------|------|----------|---------|
| `txBytes` | `[]byte` | Có | Protobuf-encoded `TxRaw` |

**`SubmitTxResponse`:** `Hash string` — SHA-256 hex (lowercase) của tx bytes.

Lỗi: `ErrNotReachable` (executor down, retryable); `"tx bytes cannot be empty"`;
HTTP 400 (tx hỏng).

```go
tx, _ := cosmoswasm.BuildExecuteTx(cosmoswasm.ExecuteTxRequest{
    Contract: "cosmos1abc...",
    Msg:      `{"transfer":{"recipient":"cosmos1xyz...","amount":"100"}}`,
})
resp, err := client.SubmitTxBytes(ctx, tx)
if err != nil { log.Fatal(err) }
fmt.Println("tx hash:", resp.Hash)
```

Latency mempool insert thường < 10ms. Tx vào block kế tiếp (block_time, mặc
định 2s). Muốn chờ kết quả → dùng `WaitTxResult`.

### `Client.SubmitTxBase64(ctx, txBase64 string) → (*SubmitTxResponse, error)`

Giống `SubmitTxBytes` nhưng nhận chuỗi base64 — tiện khi nhận tx từ FE/CLI.

### `Client.GetTxResult(ctx, txHash string) → (*GetTxResultResponse, error)`

Kiểm tra 1 lần xem tx đã thực thi chưa. Gọi `GET /tx/result?hash=...`. Hash
được chuẩn hoá (bỏ `0x`, lowercase) nên truyền hoa/thường đều khớp.

**`GetTxResultResponse`:** `Found bool`, `Result *TxExecutionResult` (chỉ có khi
`Found=true`).

**`TxExecutionResult`:** `Hash`, `Height uint64`, `Code uint32` (0=OK),
`Log string`, `Events []TxEvent`.

```go
res, err := client.GetTxResult(ctx, "a1b2c3...")
if err != nil { log.Fatal(err) }
if !res.Found {
    fmt.Println("tx chưa thực thi")
} else if res.Result.Code == 0 {
    fmt.Println("success at height", res.Result.Height)
} else {
    fmt.Println("failed:", res.Result.Log)
}
```

### `Client.WaitTxResult(ctx, txHash string, pollInterval time.Duration) → (*TxExecutionResult, error)`

Block (poll `/tx/result`) tới khi tx được include rồi trả `TxExecutionResult`.
`pollInterval` = 0 → mặc định 1s. Đây là mức **soft confirmation** (chưa chờ
DA). Luôn set context timeout.

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

resp, _ := client.SubmitTxBytes(ctx, txBytes)
result, err := client.WaitTxResult(ctx, resp.Hash, time.Second)
if err != nil { log.Fatal(err) } // timeout / network error
if result.Code != 0 { log.Fatalf("tx failed: %s", result.Log) }
fmt.Printf("success at height %d\n", result.Height)
```

---

## Finality APIs

> 👤 **App dev** — phân biệt soft confirmation vs DA-finalized.

### `Client.Status(ctx) → (*NodeStatus, error)`

Wrap `GET /status`.

**`NodeStatus`:** `Initialized`, `ChainID`, `LatestHeight` (soft tip),
`FinalizedHeight` (đã DA-finalized), `Healthy`, `Synced`.

Một tx ở `Height=h` là **soft** khi `h ≤ LatestHeight`, **DA-final** khi
`h ≤ FinalizedHeight`.

### `Client.GetTxFinality(ctx, txHash) → (FinalityLevel, *TxExecutionResult, error)`

Gọi `GetTxResult` + `Status`, so `Height` với `FinalizedHeight`.

**`FinalityLevel`** (tăng dần): `FinalityUnknown` (chưa thấy tx) <
`FinalitySoft` (đã vào block) < `FinalityDA` (block đã DA-finalized).

### `Client.WaitTxFinality(ctx, txHash, want FinalityLevel, pollInterval) → (*TxExecutionResult, error)`

Poll tới khi tx đạt mức `>= want`. Vd `WaitTxFinality(ctx, hash, cosmoswasm.FinalityDA, time.Second)`
chặn tới khi block chứa tx được DA-finalized — dùng cho rút tiền/bridge.

### `Client.FetchAccount(ctx, bech32Addr) → (*AccountInfo, error)`

Gọi `GET /auth/account/{address}` lấy `account_number` + `sequence` để build
SignDoc. **`AccountInfo`:** `Address`, `AccountNumber`, `Sequence`, `Exists`
(false → chain "đoán trước" số account sẽ cấp ở tx đầu).

---

## Query APIs

> 👤 **App dev** — đọc state contract không cần tx.

### `Client.QuerySmart(ctx, contract string, msg any) → (map[string]any, error)`

Smart query read-only. `msg` nhận `string`, `[]byte`, `map`, hoặc struct
(tự JSON-marshal). Trả `map[string]any` đã parse. Gas giới hạn `query_gas_max`
(mặc định 50M).

```go
result, err := client.QuerySmart(ctx, "cosmos1abc...", `{"token_info":{}}`)
if err != nil { log.Fatal(err) }
fmt.Println("name:", result["name"])
```

### `Client.QuerySmartRaw(ctx, contract, msg) → (*QuerySmartResponse, error)`

Trả raw: `Data any` (parsed nếu là JSON object) hoặc `DataRaw string` (fallback).

---

## Transaction Builders

> 👤 **App dev** — dựng tx bytes (`TxRaw`) **local**, không gọi network. Chưa ký
> sẵn (sender mặc định = `DefaultSender()`); muốn ký xem [Signed Builders](#signed-builders--signer).

### `BuildStoreTx(wasmBytes []byte, sender string) → ([]byte, error)`

Build `MsgStoreCode` upload WASM bytecode. `sender` rỗng → `DefaultSender()`.

### `BuildInstantiateTx(req InstantiateTxRequest) → ([]byte, error)`

Build `MsgInstantiateContract`.

| Field | Type | Bắt buộc |
|-------|------|----------|
| `CodeID` | `uint64` | Có |
| `Msg` | `any` | Có — init message |
| `Label` | `string` | Không — mặc định `"wasm-via-sdk"` |
| `Sender` | `string` | Không |
| `Admin` | `string` | Không |

### `BuildExecuteTx(req ExecuteTxRequest) → ([]byte, error)`

Build `MsgExecuteContract`. Field: `Contract` (bech32, bắt buộc), `Msg` (any,
bắt buộc), `Sender` (optional).

### `DefaultSender() → string`

Trả địa chỉ placeholder deterministic. Dùng khi `Sender` rỗng. Production dùng
địa chỉ ví thật.

### `EncodeTxBase64(tx) → string` / `EncodeTxHex(tx) → string`

Encode tx bytes sang base64 / hex để pass cho FE hoặc field `tx_hex`.

---

## Signed Builders & Signer

> 👤 **App dev** — build tx đã ký luôn (backend/faucet/script có private key).

### `NewSignerFromHex(hexPrivKey, chainID string) → (*Signer, error)`

Tạo `Signer` từ private key hex + chainID. Chainable:

- `Signer.WithGasLimit(limit uint64) *Signer`
- `Signer.WithFee(fee sdk.Coins) *Signer`
- `Signer.Address() string`

### Build + ký (tự fetch account_number/sequence qua `client.FetchAccount`)

| Function | Build |
|----------|-------|
| `BuildSignedStoreTx(ctx, client, signer, wasmByteCode)` | `MsgStoreCode` đã ký |
| `BuildSignedInstantiateTx(ctx, client, signer, req)` | `MsgInstantiateContract` đã ký |
| `BuildSignedExecuteTx(ctx, client, signer, req)` | `MsgExecuteContract` đã ký |

### Bank send đã ký (không cần `Signer`, truyền tham số trực tiếp)

```go
from, txBytes, err := cosmoswasm.BuildSignedBankSend(
    privHex, chainID, toBech32, amount /*sdk.Coins*/, accNum, seq, gas)
// hoặc với fee tường minh:
from, txBytes, err := cosmoswasm.BuildSignedBankSendWithFee(
    privHex, chainID, toBech32, amount, fee, accNum, seq, gas)
```

---

## Blob-first DA (BlobClient)

> 👤 **App dev** — upload data lớn off-chain thẳng lên **Celestia DA**, chỉ ghi
> commitment on-chain.

> ⚠️ `SubmitBlob`/`RetrieveBlob`/`SubmitBatch`/`VerifyBlob` nằm trên
> **`BlobClient`** (nói **JSON-RPC** thẳng với Celestia bridge), **không** phải
> `Client` (nói HTTP với cosmos-exec). **Không có** endpoint `/blob/*` trên
> `cosmos-exec-grpc`.

### `NewBlobClient(ctx, cfg BlobClientConfig) → (*BlobClient, error)`

**`BlobClientConfig`:**

| Field | Type | Bắt buộc | Mô tả |
|-------|------|----------|-------|
| `BridgeRPC` | `string` | Có | URL JSON-RPC Celestia bridge (vd `http://localhost:26658`) |
| `Namespace` | `string` | Có | Tên namespace app — map qua `NamespaceFromString` |
| `AuthToken` | `string` | Không | Bearer token DA (rỗng nếu node mở) |
| `GasPrice` | `float64` | Không | Gas price DA. Đặt > 0 để gửi giá tường minh (để 0 node tự ước lượng — panic ở vài bản celestia-node) |

Caller **phải** gọi `BlobClient.Close()` khi xong.

### `BlobClient.SubmitBlob(ctx, data []byte) → (*BlobSubmitResponse, error)`

Upload 1 blob lên Celestia.

**`BlobSubmitResponse`:** `Commitment string` (NMT subtree root của Celestia,
hex — **không** phải SHA-256 của data), `Height uint64` (DA height — **bắt buộc**
để retrieve), `Namespace string` (hex), `Size int`.

Lỗi: `ErrBlobTooLarge` (vượt `MaxBlobSize` = 2MB, safety cap của SDK — dùng
`SubmitBatch` hoặc `ChunkBlob`).

### `BlobClient.RetrieveBlob(ctx, height uint64, commitmentHex string) → ([]byte, error)`

Lấy blob về. **Cần cả `height` lẫn `commitment`** — commitment một mình không
đủ (`Blob.Get` cần height + namespace + commitment).

### `BlobClient.SubmitBatch(ctx, blobs [][]byte) → (*BlobBatchResponse, error)`

Upload N blob trong 1 lần submit, build cây Merkle trên các commitment.

**`BlobBatchResponse`:** `Root string` (Merkle root hex — chỉ root này ghi
on-chain), `Commitments []string` (theo thứ tự gửi), `Count int`, `Height uint64`
(DA height của cả batch).

### `BlobClient.VerifyBlob(ctx, height, commitmentHex) → (bool, error)`

Kiểm tra blob có thực sự tồn tại trên DA ở `height` không.

### `BlobClient.Namespace() → string` / `BlobClient.Close() → error`

Namespace hex đang dùng / đóng kết nối tới bridge (nil-safe).

```go
bc, err := cosmoswasm.NewBlobClient(ctx, cosmoswasm.BlobClientConfig{
    BridgeRPC: "http://localhost:26658",
    Namespace: "my-game",
})
if err != nil { log.Fatal(err) }
defer bc.Close()

res, _ := bc.SubmitBlob(ctx, []byte(`{"event":"scored","score":42}`))
data, _ := bc.RetrieveBlob(ctx, res.Height, res.Commitment) // cần height!
```

---

## Ghi commitment / root on-chain

> 👤 **App dev** — neo commitment/root từ `BlobClient` vào contract CosmWasm.

### `BuildBlobCommitTx(req BlobCommitTxRequest) → ([]byte, error)`

Build tx ghi 1 blob commitment vào contract. Contract phải handle
`{"record_blob": {...}}`. **`BlobCommitTxRequest`:** `Contract` (bắt buộc),
`Commitment`, `Height`, `Namespace`, `Sender`, `Tag`, `Extra map[string]any`.

### `BuildBatchRootTx(req BatchRootTxRequest) → ([]byte, error)`

Build tx ghi Merkle root của batch. Contract phải handle
`{"record_batch": {...}}`. **`BatchRootTxRequest`:** `Contract`, `Root`,
`Height`, `Namespace`, `Count`, `Sender`, `Tag`, `Extra`.

```go
batch, _ := bc.SubmitBatch(ctx, [][]byte{e1, e2, e3})
tx, _ := cosmoswasm.BuildBatchRootTx(cosmoswasm.BatchRootTxRequest{
    Contract:  "cosmos1abc...",
    Root:      batch.Root,
    Height:    batch.Height,
    Namespace: bc.Namespace(),
    Count:     batch.Count,
    Tag:       "game-events",
})
client.SubmitTxBytes(ctx, tx)
```

---

## Data Integrity (Merkle / chunk / compress)

> 👤 **App dev** — build/verify Merkle proof và chunk/compress blob.

### `BuildMerkleProof(commitments []string, index int) → (*MerkleProof, error)`

Build inclusion proof cho blob ở `index`. **`MerkleProof`** gồm `Root`, các
`ProofStep`, leaf index/commitment.

> Hàm cũ `GetProof(...)` đã bị gỡ — dùng `BuildMerkleProof`.

### `VerifyMerkleProof(proof *MerkleProof) → error`

Verify proof, trả `nil` nếu hợp lệ.

```go
proof, _ := cosmoswasm.BuildMerkleProof(batch.Commitments, 5)
if err := cosmoswasm.VerifyMerkleProof(proof); err != nil {
    log.Fatal("proof invalid:", err)
}
```

### `ChunkBlob(data []byte, maxSize int) → ([][]byte, *ChunkMeta)`

Chia blob lớn thành chunk. Meta `nil` nếu data vừa 1 chunk.

### `ReassembleChunks(chunks [][]byte, meta *ChunkMeta) → ([]byte, error)`

Ghép lại + check SHA-256 integrity.

### Gzip helpers

| Function | Mô tả |
|----------|------|
| `CompressGzip(data) → ([]byte, error)` | Nén gzip (best-speed) |
| `DecompressGzip(data) → ([]byte, error)` | Giải nén; lỗi nếu không phải gzip |
| `IsGzipCompressed(data) → bool` | True nếu bắt đầu bằng magic `0x1f 0x8b` |
| `MaybeDecompress(data) → ([]byte, error)` | Giải nén nếu gzip, ngược lại passthrough |
| `CompressIfBeneficial(data) → ([]byte, bool)` | Nén nếu có lợi; trả original nếu không |

---

## Namespace

> 👤 **App dev** · 🛠️ **Node operator**

```go
ns := cosmoswasm.NamespaceFromString("my-game")          // khuyến nghị: SHA-256(name)[:10] → V0
ns, _ := cosmoswasm.NewNamespaceV0([]byte("myapp"))       // từ raw bytes (≤ 10)
ns, _ := cosmoswasm.NamespaceFromHex("00...deadbeef")     // từ hex 29 byte
ns.Hex()      // "00..."
ns.Bytes()    // []byte 29 byte (1 version + 28 ID)
ns.String()   // dạng đọc được
ns.Equal(other)
```

Namespace dài 29 byte: 1 byte version (V0) + 28 byte ID (18 byte đầu = 0, 10
byte cuối là data).

---

## Errors

> 👤 **App dev** · 🛠️ **Node operator**

### `SDKError`

Public method của SDK trả `*SDKError` (hoặc `nil`): `Op` (operation lỗi),
`Cause` (lỗi gốc — match bằng `errors.Is`), `Hint` (gợi ý sửa).

```go
res, err := bc.SubmitBlob(ctx, data)
if err != nil {
    var sdkErr *cosmoswasm.SDKError
    if errors.As(err, &sdkErr) {
        fmt.Println("op:", sdkErr.Op, "hint:", sdkErr.Hint)
    }
    if errors.Is(err, cosmoswasm.ErrNotReachable) { /* retry / alert */ }
}
```

### Sentinel errors

| Error | Ý nghĩa | Retryable |
|-------|---------|-----------|
| `ErrNotReachable` | Executor down / không tới được | Có |
| `ErrBlobTooLarge` | Blob vượt max size | Không — compress / chunk |
| `ErrBlobStoreFull` | Blob store đầy | Không — restart / giảm tần suất |
| `ErrTxFailed` | Tx `code != 0` | Không — xem `result.Log` |
| `ErrContractMissing` | Thiếu địa chỉ contract | Không — set `Contract` |
| `ErrCommitMissing` | Thiếu commitment | Không — truyền commitment từ `SubmitBlob` |

---

## Dev Tooling (local chain)

> 🛠️ **Node operator** — khởi chạy chain local từ Go (dev / integration test).
> Tier 3 — có thể đổi giữa các minor version.

### `StartDALChain(ctx, cfg DALChainConfig) → (*DALChainProcess, error)`

Boot sequencer + full node + execution service từ Go (wrap script
`run-cosmos-wasm-nodes.go`). Block tới khi chain healthy. Caller phải gọi
`proc.Stop()`.

**`DALChainConfig`** (field chính):

| Field | Type | Default | Mô tả |
|-------|------|---------|-------|
| `ProjectRoot` | `string` | — | **Bắt buộc.** Đường dẫn gốc repo ev-node |
| `ChainName` | `string` | `"cosmos-wasm-local"` | Chain ID |
| `Namespace` | `string` | `"rollup"` | DA namespace |
| `DABridgeRPC` | `string` | — | **Bắt buộc.** Celestia DA node RPC |
| `DAAuthToken` | `string` | `""` | Celestia auth token |
| `CleanOnStart` / `CleanOnExit` | `bool` | `true` / `false` | Xoá data lúc start / exit |
| `LogLevel` | `string` | `"info"` | Log level |
| `BlockTime` | `time.Duration` | `2s` | Chu kỳ block |
| `SubmitInterval` | `time.Duration` | `8s` | Chu kỳ submit DA |

```go
cfg := cosmoswasm.DefaultDALChainConfig("/path/to/ev-node")
cfg.DABridgeRPC = "http://localhost:26658"
cfg.DAAuthToken = os.Getenv("DA_AUTH_TOKEN")

proc, err := cosmoswasm.StartDALChain(ctx, cfg)
if err != nil { log.Fatal(err) }
defer proc.Stop()

client := cosmoswasm.NewClient(proc.Endpoints.SequencerExecAPI)
```

**`DALChainProcess`:** `Config`, `Endpoints` (`SequencerRPC`, `FullNodeRPC`,
`SequencerExecAPI`, `FullNodeExecAPI`), `Stop()`.

### `DefaultDALChainConfig(projectRoot) → DALChainConfig`

Config default. Phải set `DABridgeRPC`.
