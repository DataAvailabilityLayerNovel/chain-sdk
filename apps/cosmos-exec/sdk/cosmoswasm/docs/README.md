# cosmoswasm SDK — Tổng quan & Index tài liệu

SDK Go (`apps/cosmos-exec/sdk/cosmoswasm`) để tương tác với một rollup CosmWasm chạy trên ev-node + Celestia DA. Một import duy nhất:

```go
import cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
```

> **Lưu ý import path:** module là `github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec` (xem `go.mod`), **không** phải `evstack/ev-node`.

---

## Mục lục

- [SDK này dành cho ai?](#sdk-này-dành-cho-ai)
- [Index toàn bộ tài liệu](#index-toàn-bộ-tài-liệu)
- [Function map (public API thật)](#function-map-public-api-thật)
- [Quick start](#quick-start--20-dòng)
- [Các API đã bị gỡ trong refactor (đừng dùng)](#các-api-đã-bị-gỡ-trong-refactor-đừng-dùng)

---

## SDK này dành cho ai?

Có 3 nhóm user, mỗi nhóm chỉ cần đọc một nhánh nhỏ trong docs:

### 1. Contract / Backend Dev — viết & deploy CosmWasm contract

Người viết smart contract (Rust → `.wasm`), backend service ghi/đọc state, batch event lên DA.

- **Mục tiêu:** store code, instantiate, execute, query, submit blob, commit Merkle root.
- **Đọc theo thứ tự:** [getting-started.md](getting-started.md) → [api-reference.md](api-reference.md) → [blob-first.md](blob-first.md) (lưu data lớn off-chain) → [error-handling.md](error-handling.md) → [fee-economics.md](fee-economics.md).
- **Function chính:** `NewClient`, `BuildStoreTx`, `BuildInstantiateTx`, `BuildExecuteTx`, `SubmitTxBytes`, `WaitTxResult`, `QuerySmart`, và `NewBlobClient` → `SubmitBlob` / `SubmitBatch` / `RetrieveBlob` cho blob-first DA. Xem mục [Function map](#function-map-public-api-thật) bên dưới.

### 2. Frontend Dev — dApp web nói chuyện với rollup

Người build UI (Next.js/React) cho user cuối: connect Keplr, ký tx, hiển thị block/tx, faucet.

- **Mục tiêu:** ký tx ở browser, submit qua HTTP, đọc account/balance/block.
- **Đọc theo thứ tự:** [frontend-integration.md](frontend-integration.md) → [frontend-usecases.md](frontend-usecases.md) (call graph theo từng use case) → [auto-account-creation.md](auto-account-creation.md) → [api-reference.md](api-reference.md) (chỉ section *Transaction APIs* + *Query APIs*).
- **Lưu ý:** FE không import SDK Go trực tiếp — gọi HTTP endpoint của `cosmos-exec-grpc` (qua Next.js proxy). SDK Go phía backend dùng để encode tx khi cần, hoặc làm faucet/indexer.

### 3. Node Operator — chạy sequencer / full node

Người vận hành chain: start sequencer + full node, cấu hình DA, monitor, set fee policy.

- **Mục tiêu:** boot chain, kết nối Celestia, theo dõi health, tune timeout/retry/auth.
- **Đọc theo thứ tự:** [node-operations.md](node-operations.md) → [configuration.md](configuration.md) → [production-guide.md](production-guide.md) → [profiles-and-security.md](profiles-and-security.md) → [sequencer-security.md](sequencer-security.md) → [troubleshooting.md](troubleshooting.md).
- **Function chính (chủ yếu là config + dev tooling):** `NewClientFromConfig`, `DefaultSDKConfig`, `StartDALChain` (cho local dev / integration test).

---

## Index toàn bộ tài liệu

Đây là index chính (đã gộp `docs_reference.md` vào đây).

### Bắt đầu & API

| File | Nội dung |
|------|----------|
| [README.md](README.md) | (file này) Tổng quan SDK, 3 nhóm user, function map, index |
| [getting-started.md](getting-started.md) | End-to-end: compile `.wasm` → start chain → deploy contract → interact qua SDK |
| [api-reference.md](api-reference.md) | Mọi public method: tham số, response, error, ví dụ code |
| [architecture.md](architecture.md) | Cấu trúc project: từng folder/file, data flow, các interface chính |
| [configuration.md](configuration.md) | Toàn bộ field config SDK + server, env var, profile dev/staging/prod |
| [error-handling.md](error-handling.md) | `SDKError`, sentinel error, retry policy, map error → hành động app |
| [migration.md](migration.md) | Thay đổi giữa các version, tách internal, lộ trình v1.0 |

### Blob-first & DA

| File | Nội dung |
|------|----------|
| [blob-first.md](blob-first.md) | Lưu data lớn off-chain trên Celestia, chỉ ghi commitment on-chain |
| [p2p-da-blobs.md](p2p-da-blobs.md) | Tầng vận chuyển: P2P 2 topic (header/data), cách xác định DA height, format blob Celestia v0, retrieve |
| [fee-economics.md](fee-economics.md) | Cost model DA + gas, chi phí Celestia, điểm hoà vốn, LazyMode, treasury + faucet (`/faucet`), checklist production |

### Runtime chain

| File | Nội dung |
|------|----------|
| [chain-flow.md](chain-flow.md) | Vòng đời tx, block production, submit DA, node sync, P2P broadcast |
| [node-operations.md](node-operations.md) | Cách start sequencer + full node, 2 tiến trình/node, ports, data on-chain vs off-chain |
| [production-guide.md](production-guide.md) | Tune timeout/retry, auth, rate limiting, monitoring, SLO |
| [profiles-and-security.md](profiles-and-security.md) | Profile `dev`/`prod` của `cosmos-exec-grpc`, passphrase signer, hardening |
| [sequencer-security.md](sequencer-security.md) | Vì sao không cần validator set; single vs based sequencer; forced inclusion; chống kiểm duyệt |
| [troubleshooting.md](troubleshooting.md) | Lỗi thường gặp, lệnh curl chẩn đoán, checklist debug |

### Frontend

| File | Nội dung |
|------|----------|
| [frontend-integration.md](frontend-integration.md) | `my-dapp-web`: proxy, Keplr (`preferNoSetFee`), ký tx, explorer/DA view, faucet UI |
| [frontend-usecases.md](frontend-usecases.md) | Call graph theo từng use case (từ FE → handler → executor) |
| [auto-account-creation.md](auto-account-creation.md) | Tạo account permissionless ở tx đầu (Keplr), hành vi peek `/auth/account`, field `tx_hashes` |
| [transport-http-vs-grpc.md](transport-http-vs-grpc.md) | Đang dùng gì (connectrpc + REST/JSON, h2c); vì sao chọn HTTP/REST thay vì gRPC thuần |

### So sánh & nền tảng (cho luận văn / hiểu kiến trúc)

| File | Nội dung |
|------|----------|
| [cac-san-pham-lien-quan.md](cac-san-pham-lien-quan.md) | Các giải pháp dùng để **đối chiếu** với đồ án, mô tả kỹ **cách hoạt động** (ev-abci, ev-node/Rollkit trần, Dymension RDK, app-chain CosmWasm, OP Stack/Arbitrum/Polygon CDK, Astria, EigenDA/Avail/EIP-4844) |
| [cosmos-vs-evnode.md](cosmos-vs-evnode.md) | So sánh chain dự án vs framework ev-node |
| [cosmos-vs-evabci.md](cosmos-vs-evabci.md) | So sánh chain dự án vs adapter `ev-abci` |
| [ibc-integration.md](ibc-integration.md) | Trạng thái & cách tích hợp IBC |
| [thesis-technologies.md](thesis-technologies.md) | Giải thích kỹ thuật các công nghệ & thuật toán dùng trong dự án |
| [roadmap.md](roadmap.md) | Lộ trình học toàn bộ stack Cosmos/CosmWasm Chain |

> Tài liệu luận văn tiếng Việt rút gọn (đối chiếu code dự án) nằm ở thư mục [`thesis/`](thesis/).

---

## Function map (public API thật)

Toàn bộ public API hiện hành (sau refactor `ea844067`), gom theo 3 tier như trong `sdk.go`. Chi tiết tham số/response: [api-reference.md](api-reference.md).

### Tier 1 — Core (bắt đầu ở đây)

**Client setup**

| Function | Khi dùng |
|----------|----------|
| `NewClient(baseURL)` | Quick dev / script một file |
| `NewClientFromConfig(SDKConfig)` | Production — control timeout, retry, auth, TLS |
| `DefaultSDKConfig()` | Lấy struct config default |
| `Client.WithHTTPClient(httpClient)` | Inject custom TLS / proxy sau khi tạo client |

**Transaction (submit + chờ kết quả)**

| Function | Mô tả |
|----------|------|
| `Client.SubmitTxBytes(ctx, tx)` | Submit `TxRaw` đã encode (gửi body `{"tx_base64": ...}`) |
| `Client.SubmitTxBase64(ctx, b64)` | Nhận tx base64 (từ FE/CLI) |
| `Client.GetTxResult(ctx, hash)` | Poll một lần — `Found` + `Result` |
| `Client.WaitTxResult(ctx, hash, pollInterval)` | Block tới khi tx được include (soft confirmation) |
| `Client.Status(ctx)` | `NodeStatus` — `LatestHeight` (soft) + `FinalizedHeight` (DA) |
| `Client.GetTxFinality(ctx, hash)` | Trả `FinalityLevel` (`Unknown`/`Soft`/`DA`) + result |
| `Client.WaitTxFinality(ctx, hash, want, interval)` | Poll tới khi đạt mức finality `want` (vd `FinalityDA`) |
| `Client.FetchAccount(ctx, addr)` | Lấy `account_number` + `sequence` để ký tx |

**Query (read-only)**

| Function | Mô tả |
|----------|------|
| `Client.QuerySmart(ctx, contract, msg)` | Trả `map[string]any` đã parse |
| `Client.QuerySmartRaw(ctx, contract, msg)` | Trả raw response (`QuerySmartResponse`) |

**Blob-first DA (qua `BlobClient`, nói JSON-RPC thẳng với Celestia bridge)**

| Function | Mô tả |
|----------|------|
| `NewBlobClient(ctx, BlobClientConfig)` | Kết nối Celestia bridge + resolve namespace |
| `BlobClient.SubmitBlob(ctx, data)` | Upload 1 blob → `BlobSubmitResponse{Commitment, Height, Namespace, Size}` (commitment = NMT của Celestia) |
| `BlobClient.RetrieveBlob(ctx, height, commitmentHex)` | Lấy blob về — **cần cả `height` lẫn `commitment`** |
| `BlobClient.SubmitBatch(ctx, blobs)` | N blob → Merkle root + commitments (`BlobBatchResponse`) |
| `BlobClient.VerifyBlob(ctx, height, commitmentHex)` | Kiểm tra blob có tồn tại trên DA không |
| `BlobClient.Namespace()` / `BlobClient.Close()` | Namespace hex đang dùng / đóng kết nối |

**Ghi commitment/root on-chain**

| Function | Build |
|----------|-------|
| `BuildBlobCommitTx(BlobCommitTxRequest)` | Tx ghi một blob commitment lên contract |
| `BuildBatchRootTx(BatchRootTxRequest)` | Tx ghi Merkle root của một batch |

**Errors**

| Type / Var | Mô tả |
|------------|------|
| `*SDKError` | Wrapper với `Op`, `Cause`, `Hint` |
| `ErrNotReachable` | Executor down — retryable |
| `ErrBlobTooLarge` | Compress hoặc chunk |
| `ErrBlobStoreFull` | Restart executor / giảm tần suất |
| `ErrTxFailed` | Tx `code != 0` — xem `result.Log` |
| `ErrContractMissing` / `ErrCommitMissing` | Field bắt buộc trống |

Chi tiết: [error-handling.md](error-handling.md).

### Tier 2 — Power-user utilities

**Transaction builders (encode local, không gọi network)**

| Function | Build |
|----------|-------|
| `BuildStoreTx(wasm, sender)` | `MsgStoreCode` |
| `BuildInstantiateTx(req)` | `MsgInstantiateContract` |
| `BuildExecuteTx(req)` | `MsgExecuteContract` |
| `DefaultSender()` | Placeholder sender khi chưa có wallet |
| `EncodeTxBase64(tx)` / `EncodeTxHex(tx)` | Encode để pass cho FE / `tx_hex` |

**Signed builders (ký luôn — cần `Signer`)**

| Function | Mô tả |
|----------|------|
| `NewSignerFromHex(hexPriv, chainID)` | Tạo `Signer`; `.WithGasLimit(n)`, `.WithFee(coins)`, `.Address()` |
| `BuildSignedStoreTx` / `BuildSignedInstantiateTx` / `BuildSignedExecuteTx` | Build + ký tx CosmWasm (tự fetch account_number/sequence) |
| `BuildSignedBankSend` / `BuildSignedBankSendWithFee` | Build + ký tx bank send |

**Namespace & DA**

| Function / Type | Mô tả |
|-----------------|------|
| `NamespaceFromString(name)` | Cách khuyến nghị — hash SHA-256(name)[:10] thành namespace V0 |
| `NewNamespaceV0(data)` / `NamespaceFromHex(hex)` | Tạo namespace V0 từ ≤10 byte data / từ hex 29 byte |
| `Namespace.Bytes()` / `.Hex()` / `.String()` / `.Equal(other)` | Serialize / so sánh |

**Data integrity (Merkle / chunk / compress)**

| Function | Mô tả |
|----------|------|
| `BuildMerkleProof(commitments, index)` | Build inclusion proof (`*MerkleProof`) |
| `VerifyMerkleProof(proof)` | Verify proof |
| `ChunkBlob(data, maxSize)` / `ReassembleChunks(chunks, meta)` | Split / ghép blob lớn |
| `CompressGzip` / `DecompressGzip` / `IsGzipCompressed` / `MaybeDecompress` / `CompressIfBeneficial` | Gzip helpers |

**Transport & response types**

`ExecutorClient` (interface HTTP/gRPC); `SubmitTxResponse`, `GetTxResultResponse`, `TxExecutionResult`, `BlobSubmitResponse`, `BlobRetrieveResponse`, `BlobBatchResponse`, `QuerySmartResponse`, `AccountInfo`, `NodeStatus`.

### Tier 3 — Dev tooling (local integration test)

| Function | Mô tả |
|----------|------|
| `StartDALChain(ctx, DALChainConfig)` | Boot sequencer + full node + execution từ Go code |
| `DefaultDALChainConfig(projectRoot)` | Defaults (cần set `DABridgeRPC`) |
| `DALChainProcess.Stop()` | Dừng chain |

Chi tiết: [api-reference.md](api-reference.md), [node-operations.md](node-operations.md).

---

## Quick start (< 20 dòng)

```go
package main

import (
    "context"
    "fmt"
    "log"

    cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
)

func main() {
    ctx := context.Background()
    client := cosmoswasm.NewClient("http://127.0.0.1:50051")

    tx, _ := cosmoswasm.BuildExecuteTx(cosmoswasm.ExecuteTxRequest{
        Contract: "cosmos1abc...",
        Msg:      `{"increment":{}}`,
    })
    resp, err := client.SubmitTxBytes(ctx, tx)
    if err != nil { log.Fatal(err) }

    result, _ := client.WaitTxResult(ctx, resp.Hash, 0)
    fmt.Printf("included at height %d, code=%d\n", result.Height, result.Code)
}
```

Đầy đủ flow (compile contract → start chain → deploy → interact): [getting-started.md](getting-started.md).

---

## Các API đã bị gỡ trong refactor (đừng dùng)

Refactor `ea844067` đã thay tầng blob/DA bằng `BlobClient` + Merkle helpers. Các symbol sau **không còn tồn tại** trong code (nếu thấy trong tài liệu cũ là đã lỗi thời):

| Symbol cũ | Thay bằng |
|-----------|-----------|
| `DAClient` (interface), `DABridge`, `NewDABridge`, `DANamespaceConfig` | `BlobClient` (`NewBlobClient` + `SubmitBlob`/`RetrieveBlob`/`SubmitBatch`) |
| `BatchBuilder`, `NewBatchBuilder`, `DefaultBatchBuilderConfig` | `BlobClient.SubmitBatch` (gom blob thủ công + 1 lần submit) |
| `CommitRoot`, `CommitCritical` | `SubmitBatch` + `BuildBatchRootTx` (2 bước rõ ràng) |
| `EstimateCost`, `DefaultEstimateCostRequest` | (đã gỡ) — xem [fee-economics.md](fee-economics.md) để tự tính |
| `GetProof(commitments, i)` | `BuildMerkleProof(commitments, index)` |
| `RetrieveBlobData(commitment)` | `RetrieveBlob(ctx, height, commitmentHex)` (cần thêm `height`) |
| `NewMockClient`, `MockExecutorClient`, `NewMockDAClient`, `MockDAClient` | (đã gỡ) — test trực tiếp qua `httptest` / `StartDALChain` |
