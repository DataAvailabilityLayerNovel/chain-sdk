# Migration Guide

## Refactor `ea844067` — tầng blob/DA mới

Refactor lớn (commit `ea844067`) thay tầng blob/DA cũ bằng `BlobClient` +
Merkle helpers, và tách chi tiết cài đặt vào `internal/`. Đây là điểm quan
trọng nhất khi nâng cấp code cũ.

### API đã GỠ → thay bằng gì

| API cũ (đã gỡ) | Thay bằng |
|----------------|-----------|
| `DAClient` (interface), `DABridge`, `NewDABridge`, `DANamespaceConfig` | `BlobClient` — `NewBlobClient(BlobClientConfig)` + `SubmitBlob` / `RetrieveBlob` / `SubmitBatch` / `VerifyBlob` |
| `BatchBuilder`, `NewBatchBuilder`, `DefaultBatchBuilderConfig` | Gom blob thủ công + `BlobClient.SubmitBatch` |
| `CommitRoot`, `CommitCritical` | `SubmitBatch` → `BuildBatchRootTx` → `SubmitTxBytes` (2 bước rõ ràng) |
| `EstimateCost`, `DefaultEstimateCostRequest` | Tự tính theo công thức trong [fee-economics.md](fee-economics.md) |
| `GetProof(commitments, i)` | `BuildMerkleProof(commitments, index)` |
| `RetrieveBlobData(commitment)` | `RetrieveBlob(ctx, height, commitmentHex)` (cần thêm `height`) |
| `NewMockClient`, `MockExecutorClient`, `NewMockDAClient`, `MockDAClient` | Test qua `httptest` hoặc `StartDALChain` |

> ⚠️ **Quan trọng về retrieve:** blob giờ nằm trên Celestia. Để lấy lại cần
> **cả `height` lẫn `commitment`** — vì vậy `BuildBlobCommitTx`/`BuildBatchRootTx`
> ghi thêm `Height` (và `Namespace`) on-chain.

### Tách package internal

Chi tiết cài đặt đã chuyển vào các subpackage `internal/`. **Nếu code chỉ import
`cosmoswasm`** thì không đổi gì. Go enforce: external module **không** import
được `internal/` — chủ đích, để refactor nội bộ không phá code bạn.

| Nội dung | Vị trí mới |
|----------|------------|
| Encode protobuf tx | `internal/txcodec` |
| Dựng cây Merkle | `internal/merkle` |
| gzip helpers (nội bộ) | `internal/compress` |
| Chunk blob (nội bộ) | `internal/chunk` |
| Chạy chain local | `internal/devchain` |

### Phân tầng API

Toàn bộ symbol export chia 3 tier (xem `go doc cosmoswasm` hoặc [README](README.md#function-map-public-api-thật)):

- **Tier 1 (Core)** — bắt đầu ở đây: `NewClient`/`NewClientFromConfig`, `SubmitTxBytes`/`WaitTxResult`, `QuerySmart`, `NewBlobClient`/`SubmitBlob`/`SubmitBatch`, `BuildBatchRootTx`.
- **Tier 2 (Power-user)** — khi cần: tx builder, `Signer` + signed builder, namespace, Merkle, chunk, compress.
- **Tier 3 (Dev tooling)** — có thể đổi giữa minor version: `StartDALChain` + local chain runner.

Tier 1 + 2 là hợp đồng ổn định. Tier 3 và `internal/` có thể đổi ở minor release.

## Lộ trình v1.0

Khi SDK đạt v1.0:

- **Tier 1 và Tier 2** thành API ổn định dài hạn — breaking change cần major version mới.
- **Tier 3** (dev chain runner) vẫn linh hoạt giữa các minor version.
- **`internal/`** đổi tự do — external không import được.
