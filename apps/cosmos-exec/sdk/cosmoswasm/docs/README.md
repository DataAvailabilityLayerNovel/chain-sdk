# cosmoswasm SDK — Overview

SDK Go (`apps/cosmos-exec/sdk/cosmoswasm`) để tương tác với một rollup CosmWasm chạy trên ev-node + Celestia DA. Một import duy nhất:

```go
import "github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm"
```

---

## SDK này dành cho ai?

Có 3 nhóm user, mỗi nhóm chỉ cần đọc một nhánh nhỏ trong docs:

### 1. Contract / Backend Dev — viết & deploy CosmWasm contract

Người viết smart contract (Rust → `.wasm`), backend service ghi/đọc state, batch event lên DA.

- **Mục tiêu:** store code, instantiate, execute, query, submit blob, commit Merkle root.
- **Đọc theo thứ tự:** [getting-started.md](getting-started.md) → [api-reference.md](api-reference.md) → [error-handling.md](error-handling.md) → [fee-economics.md](fee-economics.md).
- **Function chính:** `NewClient`, `BuildStoreTx`, `BuildInstantiateTx`, `BuildExecuteTx`, `SubmitTxBytes`, `WaitTxResult`, `QuerySmart`, `SubmitBlob`, `CommitRoot`, `NewBatchBuilder`. Xem mục [Function map](#function-map) bên dưới.

### 2. Frontend Dev — dApp web nói chuyện với rollup

Người build UI (Next.js/React) cho user cuối: connect Keplr, ký tx, hiển thị block/tx, faucet.

- **Mục tiêu:** ký tx ở browser, submit qua HTTP, đọc account/balance/block.
- **Đọc theo thứ tự:** [frontend-integration.md](frontend-integration.md) → [auto-account-creation.md](auto-account-creation.md) → [api-reference.md](api-reference.md) (chỉ section *Transaction APIs* + *Query APIs*).
- **Lưu ý:** FE không import SDK Go trực tiếp — gọi HTTP endpoint của `cosmos-exec-grpc` (qua Next.js proxy). SDK Go phía backend dùng để encode tx khi cần, hoặc làm faucet/indexer.

### 3. Node Operator — chạy sequencer / full node

Người vận hành chain: start sequencer + full node, cấu hình DA, monitor, set fee policy.

- **Mục tiêu:** boot chain, kết nối Celestia, theo dõi health, tune timeout/retry/auth.
- **Đọc theo thứ tự:** [node-operations.md](node-operations.md) → [configuration.md](configuration.md) → [production-guide.md](production-guide.md) → [sequencer-security.md](sequencer-security.md) → [troubleshooting.md](troubleshooting.md).
- **Function chính (chủ yếu là config + dev tooling):** `NewClientFromConfig`, `DefaultSDKConfig`, `StartDALChain` (cho local dev / integration test).

---

## Function map

Toàn bộ public API gom về một chỗ. Mỗi entry là `func/type → file mô tả chi tiết`.

### Client setup

| Function | Khi dùng |
|----------|----------|
| `NewClient(baseURL)` | Quick dev / script một file |
| `NewClientFromConfig(SDKConfig)` | Production — control timeout, retry, auth, TLS |
| `DefaultSDKConfig()` | Lấy struct config default |
| `Client.WithHTTPClient(httpClient)` | Inject custom TLS / proxy sau khi tạo client |

Chi tiết: [api-reference.md § Client Setup](api-reference.md#client-setup), [configuration.md](configuration.md).

### Transaction (submit + chờ kết quả)

| Function | Mô tả |
|----------|------|
| `SubmitTxBytes(ctx, tx)` | Submit `TxRaw` đã encode |
| `SubmitTxBase64(ctx, b64)` | Nhận tx base64 (từ FE/CLI) |
| `GetTxResult(ctx, hash)` | Poll một lần — `Found` + `Result` |
| `WaitTxResult(ctx, hash, pollInterval)` | Block tới khi tx được include |

Chi tiết: [api-reference.md § Transaction APIs](api-reference.md#transaction-apis).

### Transaction builders (encode local, không gọi network)

| Function | Build |
|----------|-------|
| `BuildStoreTx(wasm, sender)` | `MsgStoreCode` |
| `BuildInstantiateTx(req)` | `MsgInstantiateContract` |
| `BuildExecuteTx(req)` | `MsgExecuteContract` |
| `BuildBlobCommitTx(req)` | Tx ghi một blob commitment lên contract |
| `BuildBatchRootTx(req)` | Tx ghi Merkle root của một batch |
| `DefaultSender()` | Placeholder sender khi chưa có wallet |
| `EncodeTxBase64(tx)` / `EncodeTxHex(tx)` | Encode để pass cho FE / `tx_hex` |

Chi tiết: [api-reference.md § Transaction Builders](api-reference.md#transaction-builders).

### Query (read-only)

| Function | Mô tả |
|----------|------|
| `QuerySmart(ctx, contract, msg)` | Trả `map[string]any` parsed |
| `QuerySmartRaw(ctx, contract, msg)` | Trả raw response (struct) |

Chi tiết: [api-reference.md § Query APIs](api-reference.md#query-apis).

### Blob store (off-chain data + commitment)

| Function | Mô tả |
|----------|------|
| `SubmitBlob(ctx, data)` | Store blob, trả SHA-256 commitment |
| `RetrieveBlob(ctx, commitment)` | Lấy blob (base64) |
| `RetrieveBlobData(ctx, commitment)` | Lấy blob (`[]byte`) |
| `SubmitBatch(ctx, blobs)` | N blob → Merkle root |
| `CommitRoot(ctx, req)` | Store blobs + build root + submit tx — one call |
| `CommitCritical(ctx, req)` | Như `CommitRoot`, không buffer (event quan trọng) |

Chi tiết: [api-reference.md § Blob APIs](api-reference.md#blob-apis), [fee-economics.md](fee-economics.md).

### Batch builder (auto-flush khi đủ size / hết interval)

| Function | Mô tả |
|----------|------|
| `NewBatchBuilder(client, cfg)` | Tạo accumulator |
| `DefaultBatchBuilderConfig()` | Config defaults (cần set `Contract`) |
| `BatchBuilder.Add(ctx, data, fn)` | Append blob — flush khi vượt `MaxBytes` |
| `BatchBuilder.Flush(ctx, fn)` | Force flush |
| `BatchBuilder.StartAutoFlush(ctx, interval)` | Goroutine flush theo timer |
| `BatchBuilder.Len()` / `Bytes()` | Trạng thái hiện tại |

Chi tiết: [api-reference.md § Batch Builder](api-reference.md#batch-builder).

### Data integrity (Merkle / chunk / compress)

| Function | Mô tả |
|----------|------|
| `GetProof(commitments, leafIndex)` | Build inclusion proof |
| `VerifyMerkleProof(proof)` | Verify proof |
| `ChunkBlob(data, maxChunkSize)` / `ReassembleChunks` | Split / ghép blob lớn |
| `CompressGzip` / `DecompressGzip` / `IsGzipCompressed` / `MaybeDecompress` / `CompressIfBeneficial` | Gzip helpers |

Chi tiết: [api-reference.md § Data Integrity](api-reference.md#data-integrity).

### Cost estimation

| Function | Mô tả |
|----------|------|
| `EstimateCost(req)` | So sánh gas: direct tx vs blob-first |
| `DefaultEstimateCostRequest()` | Defaults (`GasPriceTIA=0.002`, `MaxBlobSize=4MB`) |

Chi tiết: [api-reference.md § Cost Estimation](api-reference.md#cost-estimation), [fee-economics.md](fee-economics.md).

### DA layer

| Function / Type | Mô tả |
|-----------------|------|
| `NamespaceFromString` / `NewNamespaceV0` / `NamespaceFromHex` | Tạo namespace |
| `DAClient` (interface) | `SubmitBlobs`, `GetBlobs`, `GetBlobByCommitment`, `Subscribe`, `GetHeight` |
| `DANamespaceConfig` | Per-app config (namespace + DA node addr + token) |
| `NewDABridge(da, exec, ns)` | Bridge DA ↔ Executor |
| `DABridge.Submit` / `GetBlobs` / `Watch` / `PollBlobs` / `SubmitAndCommit` / `DAHeight` | Operations |

Chi tiết: [api-reference.md § DA Layer](api-reference.md#da-layer), [chain-flow.md](chain-flow.md).

### Errors

| Type / Var | Mô tả |
|------------|------|
| `*SDKError` | Wrapper với `Op`, `Cause`, `Hint` |
| `ErrNotReachable` | Executor down — retryable |
| `ErrBlobTooLarge` | Compress hoặc chunk |
| `ErrBlobStoreFull` | Restart executor / giảm tần suất |
| `ErrTxFailed` | Tx code != 0 — xem `result.Log` |
| `ErrContractMissing` / `ErrCommitMissing` | Field bắt buộc trống |

Chi tiết: [error-handling.md](error-handling.md).

### Testing mocks

| Function | Mô tả |
|----------|------|
| `NewMockClient()` | In-memory executor (no network) |
| `MockExecutorClient.OnQuery` / `OnSubmit` / `SetTxResult` | Stub behavior |
| `NewMockDAClient()` | In-memory DA |
| `MockDAClient.SetHeight` / `InjectBlobs` | Simulate state |

Chi tiết: [api-reference.md § Testing Mocks](api-reference.md#testing-mocks).

### Dev tooling (local integration tests)

| Function | Mô tả |
|----------|------|
| `StartDALChain(ctx, cfg)` | Boot sequencer + full node + execution từ Go code |
| `DefaultDALChainConfig(projectRoot)` | Defaults (cần set `DABridgeRPC`) |

Chi tiết: [api-reference.md § Dev Tooling](api-reference.md#dev-tooling), [node-operations.md](node-operations.md).

---

## Quick start (< 20 dòng)

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm"
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

## Index theo file

Xem [docs_reference.md](docs_reference.md) cho bảng index đầy đủ từng file docs.
