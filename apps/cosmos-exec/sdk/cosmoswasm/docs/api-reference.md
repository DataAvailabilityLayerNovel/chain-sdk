# API Reference

Toàn bộ method dưới đây là **public API** của package `cosmoswasm`. Tài liệu này
đối chiếu trực tiếp với source sau refactor `ea844067` — các API cũ
(`DAClient`/`DABridge`, `BatchBuilder`, `CommitRoot`, `RetrieveBlobData`, mock
helpers) **đã bị gỡ**, không còn trong code. (`EstimateCost` đã được đưa lại với
hợp đồng mới — bọc `POST /tx/estimate`.)

> **Đây là bề mặt thư viện Go, KHÁC với bề mặt HTTP của máy chủ.** SDK `Client`
> wrap **10 / 25 endpoint** dev-facing của cosmos-exec (`/tx/submit`, `/tx/result`,
> `/tx/simulate`, `/tx/estimate`, `/tx/pending`, `/status`, `/wasm/query-smart`,
> `/auth/account/{address}`, `/blocks/latest`, `/blocks/{height}`); phần lớn method
> khác là local (builder, Merkle/chunk/compress) hoặc nói JSON-RPC thẳng với
> Celestia (`BlobClient`). Các endpoint còn lại (health, metrics, swagger, exec
> admin, faucet, LCD-alias cho Keplr) dành cho ops & frontend, không thuộc SDK.
> Danh sách đầy đủ 25 endpoint HTTP xem
> [thong-ke-ma-nguon.md](thesis/thong-ke-ma-nguon.md).

## Mục lục

- [API dành cho ai](#api-dành-cho-ai)
- [Client Setup](#client-setup)
- [Transaction APIs](#transaction-apis)
- [Finality APIs](#finality-apis)
- [Gas, chi phí, block & mempool](#gas-chi-phí-block--mempool)
- [Query APIs](#query-apis)
- [Transaction Builders](#transaction-builders)
- [Signed Builders & Signer](#signed-builders--signer)
- [Blob-first DA (BlobClient)](#blob-first-da-blobclient)
- [Ghi commitment / root on-chain](#ghi-commitment--root-on-chain)
- [Data Integrity (Merkle / chunk / compress)](#data-integrity-merkle--chunk--compress)
- [Namespace](#namespace)
- [Errors](#errors)
- [Dev Tooling (local chain)](#dev-tooling-local-chain)

## Bảng tra cứu API (tên · mô tả · file)

Bấm vào tên API để nhảy thẳng tới phần mô tả chi tiết. Cột **File** là file chứa
phần hiện thực trong package `cosmoswasm`.

**Client Setup**

| API | File | Mô tả ngắn |
|-----|------|------------|
| [`NewClient`](#m-newclient) | `client.go` | Tạo `Client` nhanh từ baseURL (timeout 20s, no retry/auth) |
| [`NewClientFromConfig`](#m-newclientfromconfig) | `client.go` · `sdk_config.go` | Tạo `Client` với toàn quyền timeout/retry/auth/transport |
| [`DefaultSDKConfig`](#m-defaultsdkconfig) | `sdk_config.go` | Trả `SDKConfig` default (phải set `ExecURL`) |
| [`Client.WithHTTPClient`](#m-withhttpclient) | `client.go` | Clone client với `http.Client` khác (inject TLS/proxy) |

**Transaction**

| API | File | Mô tả ngắn |
|-----|------|------------|
| [`Client.SubmitTxBytes`](#m-submittxbytes) | `client.go` | Submit `TxRaw` đã ký vào mempool (`POST /tx/submit`) |
| [`Client.SubmitTxBase64`](#m-submittxbase64) | `client.go` | Như trên nhưng nhận chuỗi base64 |
| [`Client.GetTxResult`](#m-gettxresult) | `client.go` | Tra kết quả tx 1 lần (`GET /tx/result`) |
| [`Client.WaitTxResult`](#m-waittxresult) | `client.go` | Poll tới khi tx vào block (soft confirmation) |
| [`Client.SimulateTx`](#m-simulatetx) | `client_extra.go` | Chạy thử tx → gas thật + gas_limit + fee để ký (`/tx/simulate`) |
| [`Client.EstimateCost`](#m-estimatecost) | `client_extra.go` | Ước lượng chi phí DA + gas, không chạy tx (`/tx/estimate`) |

**Block & mempool**

| API | File | Mô tả ngắn |
|-----|------|------------|
| [`Client.GetLatestBlock`](#m-getlatestblock) | `client_extra.go` | Block mới nhất (`/blocks/latest`); `found=false` nếu chưa có block |
| [`Client.GetBlockByHeight`](#m-getblockbyheight) | `client_extra.go` | Block theo chiều cao (`/blocks/{height}`) |
| [`Client.GetPendingTxCount`](#m-getpendingtxcount) | `client_extra.go` | Số tx đang chờ trong mempool (`/tx/pending`) |

**Finality & account**

| API | File | Mô tả ngắn |
|-----|------|------------|
| [`Client.Status`](#m-status) | `client.go` | Lấy `NodeStatus` (soft tip + finalized height) |
| [`Client.GetTxFinality`](#m-gettxfinality) | `client.go` | Mức finality của tx (unknown/soft/DA) |
| [`Client.WaitTxFinality`](#m-waittxfinality) | `client.go` | Poll tới khi tx đạt mức finality mong muốn |
| [`Client.FetchAccount`](#m-fetchaccount) | `client.go` | Lấy `account_number` + `sequence` để build SignDoc |

**Query**

| API | File | Mô tả ngắn |
|-----|------|------------|
| [`Client.QuerySmart`](#m-querysmart) | `client.go` | Smart query read-only, trả `map[string]any` đã parse |
| [`Client.QuerySmartRaw`](#m-querysmartraw) | `client.go` | Như trên nhưng trả raw (`Data`/`DataRaw`) |

**Transaction Builders** (dựng `TxRaw` local, chưa ký)

| API | File | Mô tả ngắn |
|-----|------|------------|
| [`BuildStoreTx`](#m-buildstoretx) | `tx.go` | Build `MsgStoreCode` (upload WASM) |
| [`BuildInstantiateTx`](#m-buildinstantiatetx) | `tx.go` | Build `MsgInstantiateContract` |
| [`BuildExecuteTx`](#m-buildexecutetx) | `tx.go` | Build `MsgExecuteContract` (req: `ExecuteTxRequest`) |
| [`DefaultSender`](#m-defaultsender) | `tx.go` | Địa chỉ placeholder khi `Sender` rỗng |
| [`EncodeTxBase64` / `EncodeTxHex`](#m-encodetx) | `tx.go` | Encode tx bytes sang base64 / hex |

**Signed Builders & Signer**

| API | File | Mô tả ngắn |
|-----|------|------------|
| [`NewSignerFromHex`](#m-newsignerfromhex) | `signer.go` | Tạo `Signer` từ private key hex + chainID |
| [`BuildSigned{Store,Instantiate,Execute}Tx`](#m-signedbuilders) | `tx.go` | Build tx đã ký, tự fetch account_number/sequence |
| [`BuildSignedBankSend{,WithFee}`](#m-banksend) | `faucet_tx.go` | Bank send đã ký (không cần `Signer`) |

**Blob-first DA (`BlobClient`)**

| API | File | Mô tả ngắn |
|-----|------|------------|
| [`NewBlobClient`](#m-newblobclient) | `blob.go` | Mở kết nối JSON-RPC tới Celestia bridge |
| [`BlobClient.SubmitBlob`](#m-submitblob) | `blob.go` | Upload 1 blob lên Celestia |
| [`BlobClient.RetrieveBlob`](#m-retrieveblob) | `blob.go` | Lấy blob về (cần height + commitment) |
| [`BlobClient.SubmitBatch`](#m-submitbatch) | `blob.go` | Upload N blob, build Merkle trên commitment |
| [`BlobClient.VerifyBlob`](#m-verifyblob) | `blob.go` | Kiểm tra blob tồn tại trên DA |
| [`BlobClient.Namespace` / `.Close`](#m-blobclient-ns) | `blob.go` | Namespace hex đang dùng / đóng kết nối |

**Ghi commitment / root on-chain**

| API | File | Mô tả ngắn |
|-----|------|------------|
| [`BuildBlobCommitTx`](#m-buildblobcommittx) | `tx.go` | Tx ghi 1 commitment vào contract (`record_blob`) |
| [`BuildBatchRootTx`](#m-buildbatchroottx) | `tx.go` | Tx ghi Merkle root của batch (`record_batch`) |
| [`StoreBlobAndRecord`](#m-storeblobandrecord) | `blob_record.go` | Gộp 1 call: upload blob lên DA + ghi commitment on-chain (message do dev dựng) |
| [`StoreBatchAndRecord`](#m-storebatchandrecord) | `blob_record.go` | Như trên cho N blob → 1 Merkle root |

**Data Integrity (Merkle / chunk / compress)**

| API | File | Mô tả ngắn |
|-----|------|------------|
| [`BuildMerkleProof`](#m-buildmerkleproof) | `merkle.go` | Build inclusion proof cho blob ở `index` |
| [`VerifyMerkleProof`](#m-verifymerkleproof) | `merkle.go` | Verify proof |
| [`ChunkBlob`](#m-chunkblob) | `chunk.go` | Chia blob lớn thành chunk |
| [`ReassembleChunks`](#m-reassemblechunks) | `chunk.go` | Ghép chunk + check SHA-256 |
| [Gzip helpers](#m-gzip) | `compress.go` | `CompressGzip` / `DecompressGzip` / … |

**Namespace · Errors · Dev Tooling**

| API | File | Mô tả ngắn |
|-----|------|------------|
| [`NamespaceFromString` / `NewNamespaceV0` / …](#namespace) | `namespace.go` | Tạo / parse namespace 29 byte |
| [`SDKError`](#m-sdkerror) | `errors.go` | Lỗi có cấu trúc (`Op`, `Cause`, `Hint`) |
| [Sentinel errors](#m-sentinel) | `errors.go` | `ErrNotReachable`, `ErrBlobTooLarge`, … |
| [`StartDALChain`](#m-startdalchain) | `chain.go` | Boot sequencer + full node + exec từ Go (dev) |
| [`DefaultDALChainConfig`](#m-defaultdalchainconfig) | `chain.go` | Config default cho `StartDALChain` |

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

<a id="m-newclient"></a>
### `NewClient(baseURL) → *Client`

Tạo `Client` cho dev nhanh. Kết nối tới 1 executor endpoint với mặc định
(timeout 20s, không retry, không auth). `baseURL` rỗng → mặc định
`http://127.0.0.1:50051`.

```go
client := cosmoswasm.NewClient("http://127.0.0.1:50051")
```

Production nên dùng `NewClientFromConfig`.

<a id="m-newclientfromconfig"></a>
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

<a id="m-defaultsdkconfig"></a>
### `DefaultSDKConfig() → SDKConfig`

Trả config với default hợp lý. Phải set `ExecURL` trước khi dùng.

<a id="m-withhttpclient"></a>
### `Client.WithHTTPClient(httpClient) → *Client`

Trả clone của `Client` với `http.Client` khác — tiện inject TLS/proxy sau khi
tạo client.

---

## Transaction APIs

> 👤 **App dev** — submit tx đã ký và theo dõi kết quả thực thi.

<a id="m-submittxbytes"></a>
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

<a id="m-submittxbase64"></a>
### `Client.SubmitTxBase64(ctx, txBase64 string) → (*SubmitTxResponse, error)`

Giống `SubmitTxBytes` nhưng nhận chuỗi base64 — tiện khi nhận tx từ FE/CLI.

<a id="m-gettxresult"></a>
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

<a id="m-waittxresult"></a>
### `Client.WaitTxResult(ctx, txHash string, pollInterval time.Duration) → (*TxExecutionResult, error)`

Block (poll `/tx/result`) tới khi tx được include rồi trả `TxExecutionResult`.
`pollInterval` = 0 → mặc định 1s. Đây là mức **soft confirmation** (chưa chờ
DA). Luôn set context timeout.

```go
// context.Background(): context "rỗng", gốc của mọi context — không bao giờ
//   tự huỷ, không deadline. Dùng làm điểm khởi đầu để bọc thêm timeout/cancel.
// context.WithTimeout(parent, 30s): tạo context con tự động bị huỷ sau 30 giây
//   (hoặc khi gọi cancel). Trả về 2 giá trị:
//     - ctx    : context con, truyền vào các lời gọi để chúng dừng khi hết hạn.
//     - cancel : hàm để huỷ context THỦ CÔNG (giải phóng tài nguyên nội bộ).
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
// defer cancel(): hoãn gọi cancel() tới khi hàm hiện tại return. BẮT BUỘC gọi
//   cancel dù timeout đã hết hay chưa — nếu không sẽ rò rỉ timer/goroutine của
//   context. "defer" đảm bảo nó luôn chạy ở mọi nhánh thoát hàm.
defer cancel()

// ctx mang theo deadline 30s: nếu WaitTxResult poll quá 30s mà tx chưa vào
// block, lời gọi tự dừng và trả về lỗi context deadline exceeded.
resp, _ := client.SubmitTxBytes(ctx, txBytes)
result, err := client.WaitTxResult(ctx, resp.Hash, time.Second)
if err != nil { log.Fatal(err) } // timeout / network error
if result.Code != 0 { log.Fatalf("tx failed: %s", result.Log) }
fmt.Printf("success at height %d\n", result.Height)
```

---

## Finality APIs

> 👤 **App dev** — phân biệt soft confirmation vs DA-finalized.

<a id="m-status"></a>
### `Client.Status(ctx) → (*NodeStatus, error)`

Wrap `GET /status`.

**`NodeStatus`:** `Initialized`, `ChainID`, `LatestHeight` (soft tip),
`FinalizedHeight` (đã DA-finalized), `Healthy`, `Synced`.

Một tx ở `Height=h` là **soft** khi `h ≤ LatestHeight`, **DA-final** khi
`h ≤ FinalizedHeight`.

<a id="m-gettxfinality"></a>
### `Client.GetTxFinality(ctx, txHash) → (FinalityLevel, *TxExecutionResult, error)`

Gọi `GetTxResult` + `Status`, so `Height` với `FinalizedHeight`.

**`FinalityLevel`** (tăng dần): `FinalityUnknown` (chưa thấy tx) <
`FinalitySoft` (đã vào block) < `FinalityDA` (block đã DA-finalized).

<a id="m-waittxfinality"></a>
### `Client.WaitTxFinality(ctx, txHash, want FinalityLevel, pollInterval) → (*TxExecutionResult, error)`

Poll tới khi tx đạt mức `>= want`. Vd `WaitTxFinality(ctx, hash, cosmoswasm.FinalityDA, time.Second)`
chặn tới khi block chứa tx được DA-finalized — dùng cho rút tiền/bridge.

<a id="m-fetchaccount"></a>
### `Client.FetchAccount(ctx, bech32Addr) → (*AccountInfo, error)`

Gọi `GET /auth/account/{address}` để lấy hai số **bắt buộc khi ký một tx Cosmos**:
`account_number` và `sequence`. Hai số này đi vào `SignDoc` — phần dữ liệu được ký —
nên nếu sai thì chữ ký bị từ chối. Vì vậy các signed builder
([`BuildSignedStoreTx`](#m-signedbuilders)…) tự gọi `FetchAccount` bên trong trước
khi ký; bạn chỉ cần gọi trực tiếp khi tự dựng `SignDoc`.

| Param | Type | Bắt buộc | Ghi chú |
|-------|------|----------|---------|
| `ctx` | `context.Context` | Có | Mang deadline/cancel cho lời gọi HTTP (xem giải thích ở [`WaitTxResult`](#m-waittxresult)) |
| `bech32Addr` | `string` | Có | Địa chỉ ví bech32 (`cosmos1...`); rỗng → lỗi `"address is required"` |

**`AccountInfo`** (`client.go`):

| Field | Type | Mô tả |
|-------|------|-------|
| `Address` | `string` | Địa chỉ bech32 đã truy vấn |
| `AccountNumber` | `uint64` | Số định danh account, **chain cấp một lần** khi account được tạo. Cố định suốt đời account |
| `Sequence` | `uint64` | Bộ đếm chống replay, **tăng 1 sau mỗi tx** đã include. Tx kế tiếp phải mang đúng số này |
| `Exists` | `bool` | `true` nếu account đã có trong state; **`false`** → account **chưa tồn tại**, chain trả về số account_number **dự kiến sẽ cấp** ở tx đầu tiên (kết hợp với [AutoCreateAccount](fee-economics.md#muc-6e), tx ký đầu vẫn build được SignDoc hợp lệ) |

Ý nghĩa `Exists=false`: một ví hoàn toàn mới chưa từng giao dịch sẽ **chưa có** bản
ghi account. Thay vì báo lỗi, endpoint vẫn trả về `account_number`/`sequence` "đoán
trước" (thường `sequence=0`) để client kịp dựng và ký tx đầu tiên — chính tx đó tạo
ra account thật trên chain.

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

acc, err := client.FetchAccount(ctx, signer.Address())
if err != nil { log.Fatal(err) }
if !acc.Exists {
    // Ví mới, chưa có account on-chain — tx đầu sẽ tự tạo (cần AutoCreate
    // hoặc đã được faucet cấp token trước đó). sequence lúc này = 0.
    fmt.Println("account chưa tồn tại, sẽ tạo ở tx đầu")
}
// Dùng acc.AccountNumber + acc.Sequence để dựng SignDoc rồi ký:
fmt.Printf("acc#=%d seq=%d\n", acc.AccountNumber, acc.Sequence)
```

> Sau khi ký và submit một tx thành công, **`Sequence` tăng 1**. Nếu gửi nhiều tx
> liên tiếp mà chưa chờ tx trước vào block, phải tự tăng `Sequence` cục bộ (xem cách
> faucet xử lý `nextSeq` trong [fee-economics.md §6b](fee-economics.md#muc-6b)) —
> `FetchAccount` chỉ phản ánh state đã commit, chưa tính tx đang nằm trong mempool.

---

## Gas, chi phí, block & mempool

> 👤 **App dev** — đo gas trước khi ký, ước lượng chi phí, đọc block và mempool.
> Các method này nằm ở `client_extra.go`.

<a id="m-simulatetx"></a>
### `Client.SimulateTx(ctx, txBytes []byte) → (*SimulateResponse, error)`

Chạy tx qua **ante + msg handler nhưng KHÔNG commit** (`POST /tx/simulate`), trả về
gas **thật** mà tx tiêu cùng `gas_limit` đã đệm và `fee` gợi ý. Đây là cách đúng để
lấy `gas_limit`/`fee` **trước khi ký**, thay cho việc đặt một hằng cố định.

**`SimulateResponse`:** `GasUsed`, `GasWanted`, `GasLimit uint64`,
`Fee []Coin` (mỗi `Coin{Denom, Amount string}`), `FeeDenom`, `FeeAmount string`.

`GasLimit = ceil(GasUsed × COSMOS_EXEC_GAS_ADJUSTMENT)` (server tính); `Fee` theo
đúng chính sách `ante` enforce nên **không lệch** giữa ước lượng và thực thu.

```go
// txBytes nên là tx đã ký (hoặc ký giả 0-fee) — server chỉ cần để chạy thử.
sim, err := client.SimulateTx(ctx, txBytes)
if err != nil { log.Fatal(err) }
fmt.Printf("gas_used=%d → gas_limit=%d, fee=%s%s\n",
    sim.GasUsed, sim.GasLimit, sim.FeeAmount, sim.FeeDenom)
// Gắn sim.GasLimit + sim.Fee vào tx rồi ký lại và submit.
```

> Ở chế độ bật enforce chữ ký, tx chưa ký có thể bị ante từ chối khi simulate —
> truyền tx đã ký để có số chính xác. Chi tiết công thức xem
> [fee-economics.md §1b](fee-economics.md#muc-1b).

<a id="m-estimatecost"></a>
### `Client.EstimateCost(ctx, req EstimateRequest) → (*CostBreakdown, error)`

Ước lượng **tổng chi phí (DA + gas)** mà KHÔNG chạy tx (trừ dạng `{hash}`). Dùng cho
dashboard/định giá; muốn gas chính xác để ký thì dùng `SimulateTx`.

**`EstimateRequest`** — cung cấp đúng MỘT trong: `{TxBase64|TxHex, Gas}` (raw tx),
`{Hash}` (tx đã chạy → server lấy bytes+gas thật), hoặc `{Bytes, Gas}` (trực tiếp).

**`CostBreakdown`:** `Bytes`, `Gas uint64`, `EstDAAmount`/`EstDADenom`,
`EstGasAmount`/`EstGasDenom`, và hằng chính sách `DAPricePerByte`, `MinGasPrice`.

```go
cost, err := client.EstimateCost(ctx, cosmoswasm.EstimateRequest{Bytes: 1024, Gas: 200_000})
if err != nil { log.Fatal(err) }
fmt.Printf("DA=%s%s gas=%s%s\n",
    cost.EstDAAmount, cost.EstDADenom, cost.EstGasAmount, cost.EstGasDenom)
```

<a id="m-getlatestblock"></a>
### `Client.GetLatestBlock(ctx) → (*BlockInfo, bool, error)`

Lấy block mới nhất (`GET /blocks/latest`). Trả `found=false` khi chain **chưa có
block nào** (không phải lỗi). **`BlockInfo`:** `Height uint64`, `Time string`
(RFC3339), `AppHash string`, `NumTxs int`, `TxHashes []string`.

```go
blk, found, err := client.GetLatestBlock(ctx)
if err != nil { log.Fatal(err) }
if found { fmt.Printf("height=%d num_txs=%d\n", blk.Height, blk.NumTxs) }
```

<a id="m-getblockbyheight"></a>
### `Client.GetBlockByHeight(ctx, height uint64) → (*BlockInfo, error)`

Lấy block tại một chiều cao (`GET /blocks/{height}`). Height không tồn tại → server
trả 404 → method trả error. `height = 0` → lỗi `"height must be > 0"`.

<a id="m-getpendingtxcount"></a>
### `Client.GetPendingTxCount(ctx) → (int, error)`

Trả số giao dịch đang chờ trong mempool của executor (`GET /tx/pending`).

---

## Query APIs

> 👤 **App dev** — đọc state contract không cần tx.

<a id="m-querysmart"></a>
### `Client.QuerySmart(ctx, contract string, msg any) → (map[string]any, error)`

Smart query read-only. `msg` nhận `string`, `[]byte`, `map`, hoặc struct
(tự JSON-marshal). Trả `map[string]any` đã parse. Gas giới hạn `query_gas_max`
(mặc định 50M).

```go
// ctx: context.Context điều khiển vòng đời lời gọi (deadline + huỷ). Tạo bằng
//   context.WithTimeout(...) như ví dụ WaitTxResult, hoặc context.Background()
//   nếu không cần timeout. Khi ctx hết hạn/bị huỷ, request HTTP tự dừng.
// Tham số 2: địa chỉ contract (bech32). Tham số 3: query message — ở đây là
//   chuỗi JSON; cũng nhận []byte / map / struct (tự JSON-marshal).
result, err := client.QuerySmart(ctx, "cosmos1abc...", `{"token_info":{}}`)
if err != nil { log.Fatal(err) }
fmt.Println("name:", result["name"]) // result là map[string]any đã parse
```

<a id="m-querysmartraw"></a>
### `Client.QuerySmartRaw(ctx, contract, msg) → (*QuerySmartResponse, error)`

Trả raw: `Data any` (parsed nếu là JSON object) hoặc `DataRaw string` (fallback).

---

## Transaction Builders

> 👤 **App dev** — dựng tx bytes (`TxRaw`) **local**, không gọi network. Chưa ký
> sẵn (sender mặc định = `DefaultSender()`); muốn ký xem [Signed Builders](#signed-builders--signer).

<a id="m-buildstoretx"></a>
### `BuildStoreTx(wasmBytes []byte, sender string) → ([]byte, error)`

Build `MsgStoreCode` upload WASM bytecode. `sender` rỗng → `DefaultSender()`.

<a id="m-buildinstantiatetx"></a>
### `BuildInstantiateTx(req InstantiateTxRequest) → ([]byte, error)`

Build `MsgInstantiateContract`.

| Field | Type | Bắt buộc |
|-------|------|----------|
| `CodeID` | `uint64` | Có |
| `Msg` | `any` | Có — init message |
| `Label` | `string` | Không — mặc định `"wasm-via-sdk"` |
| `Sender` | `string` | Không |
| `Admin` | `string` | Không |

<a id="m-buildexecutetx"></a>
### `BuildExecuteTx(req ExecuteTxRequest) → ([]byte, error)`

Build `MsgExecuteContract`. Field: `Contract` (bech32, bắt buộc), `Msg` (any,
bắt buộc), `Sender` (optional).

<a id="m-defaultsender"></a>
### `DefaultSender() → string`

Trả địa chỉ placeholder deterministic. Dùng khi `Sender` rỗng. Production dùng
địa chỉ ví thật.

<a id="m-encodetx"></a>
### `EncodeTxBase64(tx) → string` / `EncodeTxHex(tx) → string`

Encode tx bytes sang base64 / hex để pass cho FE hoặc field `tx_hex`.

---

## Signed Builders & Signer

> 👤 **App dev** — build tx đã ký luôn (backend/faucet/script có private key).

<a id="m-newsignerfromhex"></a>
### `NewSignerFromHex(hexPrivKey, chainID string) → (*Signer, error)`

Tạo `Signer` từ private key hex + chainID. Chainable:

- `Signer.WithGasLimit(limit uint64) *Signer`
- `Signer.WithFee(fee sdk.Coins) *Signer`
- `Signer.Address() string`

<a id="m-signedbuilders"></a>
### Build + ký (tự fetch account_number/sequence qua `client.FetchAccount`)

| Function | Build |
|----------|-------|
| `BuildSignedStoreTx(ctx, client, signer, wasmByteCode)` | `MsgStoreCode` đã ký |
| `BuildSignedInstantiateTx(ctx, client, signer, req)` | `MsgInstantiateContract` đã ký |
| `BuildSignedExecuteTx(ctx, client, signer, req)` | `MsgExecuteContract` đã ký |

<a id="m-banksend"></a>
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

<a id="m-newblobclient"></a>
### `NewBlobClient(ctx, cfg BlobClientConfig) → (*BlobClient, error)`

**`BlobClientConfig`:**

| Field | Type | Bắt buộc | Mô tả |
|-------|------|----------|-------|
| `BridgeRPC` | `string` | Có | URL JSON-RPC Celestia bridge (vd `http://localhost:26658`) |
| `Namespace` | `string` | Có | Tên namespace app — map qua `NamespaceFromString` |
| `AuthToken` | `string` | Không | Bearer token DA (rỗng nếu node mở) |
| `GasPrice` | `float64` | Không | Gas price DA. Đặt > 0 để gửi giá tường minh (để 0 node tự ước lượng — panic ở vài bản celestia-node) |

Caller **phải** gọi `BlobClient.Close()` khi xong.

<a id="m-submitblob"></a>
### `BlobClient.SubmitBlob(ctx, data []byte) → (*BlobSubmitResponse, error)`

Upload 1 blob lên Celestia.

**`BlobSubmitResponse`:** `Commitment string` (NMT subtree root của Celestia,
hex — **không** phải SHA-256 của data), `Height uint64` (DA height — **bắt buộc**
để retrieve), `Namespace string` (hex), `Size int`.

Lỗi: `ErrBlobTooLarge` (vượt `MaxBlobSize` = 2MB, safety cap của SDK — dùng
`SubmitBatch` hoặc `ChunkBlob`).

<a id="m-retrieveblob"></a>
### `BlobClient.RetrieveBlob(ctx, height uint64, commitmentHex string) → ([]byte, error)`

Lấy blob về. **Cần cả `height` lẫn `commitment`** — commitment một mình không
đủ (`Blob.Get` cần height + namespace + commitment).

<a id="m-submitbatch"></a>
### `BlobClient.SubmitBatch(ctx, blobs [][]byte) → (*BlobBatchResponse, error)`

Upload N blob trong 1 lần submit, build cây Merkle trên các commitment.

**`BlobBatchResponse`:** `Root string` (Merkle root hex — chỉ root này ghi
on-chain), `Commitments []string` (theo thứ tự gửi), `Count int`, `Height uint64`
(DA height của cả batch).

<a id="m-verifyblob"></a>
### `BlobClient.VerifyBlob(ctx, height, commitmentHex) → (bool, error)`

Kiểm tra blob có thực sự tồn tại trên DA ở `height` không.

<a id="m-blobclient-ns"></a>
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

<a id="m-buildblobcommittx"></a>
### `BuildBlobCommitTx(req BlobCommitTxRequest) → ([]byte, error)`

Build tx ghi 1 blob commitment vào contract. Contract phải handle
`{"record_blob": {...}}`. **`BlobCommitTxRequest`:** `Contract` (bắt buộc),
`Commitment`, `Height`, `Namespace`, `Sender`, `Tag`, `Extra map[string]any`.

<a id="m-buildbatchroottx"></a>
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

### Một-call: store blob + ghi on-chain

> Hai builder trên giả định contract theo convention `record_blob`/`record_batch`.
> Khi contract dùng **message riêng**, dùng hai helper dưới — chúng gộp toàn bộ luồng
> (DA → on-chain) nhưng để **bạn tự dựng message** qua callback.

<a id="m-storeblobandrecord"></a>
#### `StoreBlobAndRecord(ctx, bc, client, signer, contract, data, buildMsg) → (*BlobSubmitResponse, string, error)`

Làm trọn vòng cho **một** blob trong một lời gọi:

1. `bc.SubmitBlob(data)` → upload lên Celestia DA (commitment + height + namespace).
2. `buildMsg(blob, nsHex)` → **bạn** dựng execute message của contract.
3. Ký bằng `signer`, `SubmitTxBytes`, rồi `WaitTxResult` (chờ vào block).

Trả `(*BlobSubmitResponse, txHash, error)`. **Quan trọng:** `BlobSubmitResponse` được
trả về **kể cả khi bước on-chain lỗi** — blob đã nằm trên DA, nên bạn retry phần ghi
on-chain mà **không phải upload lại**. `txHash` rỗng nếu chưa kịp submit.

```go
blob, txHash, err := cosmoswasm.StoreBlobAndRecord(
    ctx, bc, client, signer, "cosmos1abc...", payload,
    func(b *cosmoswasm.BlobSubmitResponse, nsHex string) any {
        // contract của bạn xử lý message này — tên/format tuỳ ý:
        return map[string]any{"record_telemetry": map[string]any{
            "commitment": b.Commitment, "height": b.Height, "namespace": nsHex,
        }}
    })
if err != nil {
    // blob != nil ở đây → có thể retry ghi on-chain với blob.Commitment/Height
    log.Fatalf("store+record: %v", err)
}
fmt.Printf("blob=%s tx=%s\n", blob.Commitment, txHash)
```

<a id="m-storebatchandrecord"></a>
#### `StoreBatchAndRecord(ctx, bc, client, signer, contract, blobs, buildMsg) → (*BlobBatchResponse, string, error)`

Như trên nhưng cho **N blob gộp một batch**: `bc.SubmitBatch(blobs)` trả về **một
Merkle root** đại diện cả batch (chi phí on-chain = 1 lần), rồi `buildMsg(batch, nsHex)`
dựng message ghi root đó. Trả `*BlobBatchResponse` kể cả khi on-chain lỗi.

```go
batch, txHash, err := cosmoswasm.StoreBatchAndRecord(
    ctx, bc, client, signer, "cosmos1abc...", chunks,
    func(b *cosmoswasm.BlobBatchResponse, nsHex string) any {
        return map[string]any{"record_replay": map[string]any{
            "root": b.Root, "height": b.Height, "count": b.Count, "namespace": nsHex,
        }}
    })
```

> Cần `signer` (đây là đường có ký). Ví dụ chạy được: `examples/game-telemetry`
> dùng `StoreBlobAndRecord` cho từng frame telemetry.

---

## Data Integrity (Merkle / chunk / compress)

> 👤 **App dev** — build/verify Merkle proof và chunk/compress blob.

<a id="m-buildmerkleproof"></a>
### `BuildMerkleProof(commitments []string, index int) → (*MerkleProof, error)`

Build inclusion proof cho blob ở `index`. **`MerkleProof`** gồm `Root`, các
`ProofStep`, leaf index/commitment.

> Hàm cũ `GetProof(...)` đã bị gỡ — dùng `BuildMerkleProof`.

<a id="m-verifymerkleproof"></a>
### `VerifyMerkleProof(proof *MerkleProof) → error`

Verify proof, trả `nil` nếu hợp lệ.

```go
proof, _ := cosmoswasm.BuildMerkleProof(batch.Commitments, 5)
if err := cosmoswasm.VerifyMerkleProof(proof); err != nil {
    log.Fatal("proof invalid:", err)
}
```

<a id="m-chunkblob"></a>
### `ChunkBlob(data []byte, maxSize int) → ([][]byte, *ChunkMeta)`

Chia blob lớn thành chunk. Meta `nil` nếu data vừa 1 chunk.

<a id="m-reassemblechunks"></a>
### `ReassembleChunks(chunks [][]byte, meta *ChunkMeta) → ([]byte, error)`

Ghép lại + check SHA-256 integrity.

<a id="m-gzip"></a>
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

<a id="m-sdkerror"></a>
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

<a id="m-sentinel"></a>
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

<a id="m-startdalchain"></a>
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

<a id="m-defaultdalchainconfig"></a>
### `DefaultDALChainConfig(projectRoot) → DALChainConfig`

Config default. Phải set `DABridgeRPC`.
