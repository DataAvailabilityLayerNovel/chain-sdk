# Blob-first: lưu dữ liệu lớn off-chain, cam kết on-chain

Tài liệu này mô tả cơ chế **blob-first** của SDK: đẩy dữ liệu LỚN thẳng lên
Celestia DA, chỉ ghi một **commitment 32 byte** (+ DA height) lên chain. Đây là
cách đưa dữ liệu nặng (telemetry game, ảnh, snapshot, log sự kiện) vào ứng dụng
rollup mà không phải trả phí lưu trữ vĩnh viễn trong state của contract.

> TL;DR: `BlobClient` nói chuyện **thẳng** với Celestia bridge (không qua
> cosmos-exec-grpc). `Client` (tx/query) hoàn toàn không đổi — blob-first là
> tính năng **cộng thêm**, bật khi cần.

---

## 1. Vấn đề: ghi dữ liệu lớn on-chain rất đắt

Trên blockchain, **mọi** validator lưu **mọi** byte **vĩnh viễn**. Ghi 1MB vào
state WASM:

- tốn gas khổng lồ (phí tỉ lệ kích thước data),
- phình state mãi mãi (ai chạy node cũng gánh).

Nhưng nhiều ứng dụng cần gắn dữ liệu lớn vào chain: game (replay trận, telemetry),
NFT/nội dung (ảnh, metadata), log sự kiện thời gian thực (IoT, audit).

## 2. Giải pháp: tách data ra DA, chỉ neo commitment

```
Data lớn ──────────────────────────────► Celestia DA (off-chain, rẻ)
   │
   └─ commitment (32 byte) + height ────► CosmWasm contract (on-chain, cố định)
```

- **Commitment** là một ràng buộc mật mã (NMT subtree root của Celestia). Cầm
  data về, tính lại commitment, so với cái đã neo on-chain → biết data có bị
  sửa không. Đây là tính **verifiable** (xác minh được).
- On-chain chỉ tốn ~32 byte bất kể data 1KB hay 1MB → gas cố định, state không phình.

## 3. API

| Hàm | Tác dụng |
|---|---|
| `NewBlobClient(ctx, cfg)` | Kết nối Celestia bridge + resolve namespace |
| `BlobClient.SubmitBlob(ctx, data)` | Đẩy 1 blob → trả `{Commitment, Height, Namespace, Size}` |
| `BlobClient.RetrieveBlob(ctx, height, commitment)` | Lấy data về (CẦN height) |
| `BlobClient.SubmitBatch(ctx, blobs)` | Đẩy N blob trong 1 lần → 1 height + 1 Merkle root |
| `BlobClient.VerifyBlob(ctx, height, commitment)` | Lấy về + tính lại commitment → tamper-evident |
| `BuildBlobCommitTx(req)` | Build tx ghi commitment vào contract (`record_blob`) |
| `BuildBatchRootTx(req)` | Build tx ghi Merkle root của batch (`record_batch`) |

### Khởi tạo

```go
bc, err := cosmoswasm.NewBlobClient(ctx, cosmoswasm.BlobClientConfig{
    BridgeRPC: "http://127.0.0.1:26658", // Celestia bridge JSON-RPC
    AuthToken: os.Getenv("DA_AUTH_TOKEN"),
    Namespace: "my-game",                // mỗi app NÊN có namespace riêng
})
if err != nil { /* ... */ }
defer bc.Close()
```

`Namespace` (chuỗi dễ đọc) được map deterministic sang namespace Celestia v0 qua
[`NamespaceFromString`](../namespace.go) — cùng tên → cùng namespace.

## 4. ⚠️ Điểm BẮT BUỘC nhớ: retrieve cần `height`, không chỉ commitment

Celestia `Blob.Get` cần đủ **(height, namespace, commitment)**. Commitment một
mình KHÔNG đủ để lấy data về. Vì vậy:

- `SubmitBlob` trả về **cả `Height`** — phải lưu lại.
- Khi ghi on-chain, `BuildBlobCommitTx` ghi **cả `height` và `namespace`** vào
  contract, để bất kỳ ai đọc chain cũng retrieve lại được.

```go
sub, _ := bc.SubmitBlob(ctx, data)
// sub.Height là KHÓA để lấy lại — đừng vứt đi.
got, _ := bc.RetrieveBlob(ctx, sub.Height, sub.Commitment)
```

## 5. Ghi commitment on-chain + đặc tả contract

Phần off-chain (`SubmitBlob`) đi thẳng Celestia. Phần on-chain là một tx
CosmWasm **bình thường** (qua `Client.SubmitTxBytes`):

```go
tx, _ := cosmoswasm.BuildBlobCommitTx(cosmoswasm.BlobCommitTxRequest{
    Contract:   "wasm1...",
    Commitment: sub.Commitment,
    Height:     sub.Height,
    Namespace:  sub.Namespace,
    Tag:        "telemetry",
})
client.SubmitTxBytes(ctx, tx)
```

Contract WASM đích PHẢI xử lý message `record_blob`:

```jsonc
{ "record_blob": {
    "commitment": "<hex>",
    "height":     123456,      // u64 — KHÓA để retrieve
    "namespace":  "0x...",
    "tag":        "telemetry"
}}
```

Phía Rust (ví dụ tối giản — lưu `commitment → meta`):

```rust
#[cw_serde]
pub enum ExecuteMsg {
    RecordBlob { commitment: String, height: u64, namespace: String, tag: Option<String> },
    RecordBatch { root: String, height: u64, count: u32, tag: Option<String> },
}
```

> Lưu ý: `height` gửi dưới dạng JSON number → contract nên dùng `u64` (không phải
> `Uint64`/string).

## 6. Batch: N blob, một root on-chain

`SubmitBatch` đẩy N blob trong **một** lần submit (cùng 1 DA height), và tính một
**Merkle root** gom N commitment (dùng [`internal/merkle`](../internal/merkle)).
Ghi 1 root on-chain = chi phí on-chain duy nhất cho cả batch.

```go
res, _ := bc.SubmitBatch(ctx, [][]byte{frame1, frame2, frame3})
// res.Root: 1 giá trị neo on-chain cho N blob
// res.Commitments[i] + res.Height: vẫn retrieve/verify từng blob lẻ được
tx, _ := cosmoswasm.BuildBatchRootTx(cosmoswasm.BatchRootTxRequest{
    Contract: "wasm1...", Root: res.Root, Height: res.Height,
    Namespace: bc.Namespace(), Count: res.Count, Tag: "telemetry-batch",
})
```

Membership của từng blob trong root chứng minh được bằng Merkle proof public:
`BuildMerkleProof(res.Commitments, i)` → `VerifyMerkleProof(proof)` (xem Mục 7b).

## 7. Xác minh (verifiable)

Hai mức:

1. **Tamper-evident ở SDK** — `VerifyBlob` lấy data về, tính lại commitment Celestia
   (`NewBlobV0`), so với commitment đã neo on-chain. Khớp → data nguyên vẹn & đúng
   cái đã cam kết.
   ```go
   ok, _ := bc.VerifyBlob(ctx, sub.Height, sub.Commitment) // true nếu chưa bị sửa
   ```
2. **DA-inclusion proof (sâu hơn)** — commitment chính là NMT subtree root của
   Celestia; bằng chứng inclusion lấy qua `GetCommitmentProof` của DA client
   (định hướng mở rộng, xem [roadmap](roadmap.md)).

## 7b. Tiện ích data-integrity: nén / chunk / merkle proof

`SubmitBlob` cơ bản xử lý 1 blob nhỏ, data thô. Ba nhóm hàm public dưới đây biến
blob-first thành production-grade: **nén** (rẻ hơn), **chunk** (vượt giới hạn kích
thước), **merkle proof** (chứng minh membership). Cả ba độc lập, dùng riêng hay
phối hợp đều được.

### a. Nén — giảm byte trước khi đẩy DA

`DA_cost = bytes × giá/byte`, nên nén = giảm trực tiếp khoản DA. Telemetry/log/JSON
nén 5–10×.

| Hàm | Tác dụng |
|---|---|
| `CompressIfBeneficial(data) ([]byte, bool)` | Nén **chỉ khi** nhỏ hơn thật; data không nén được → trả gốc + `false`. An toàn để gọi trước mọi submit. |
| `MaybeDecompress(data) ([]byte, error)` | Tự giải nén nếu là gzip, không thì trả nguyên. Cặp đôi ở chiều đọc. |
| `CompressGzip` / `DecompressGzip` | Nén/giải nén thô (khi cần kiểm soát tay). |
| `IsGzipCompressed(data) bool` | Check magic byte — blob lấy về có phải dạng nén không. |

```go
payload, _ := CompressIfBeneficial(raw)      // ghi: nén nếu lợi
sub, _ := bc.SubmitBlob(ctx, payload)
// ...
got, _ := bc.RetrieveBlob(ctx, sub.Height, sub.Commitment)
raw, _ := MaybeDecompress(got)               // đọc: tự khôi phục
```

### b. Chunk — data lớn hơn 1 blob

`SubmitBlob` chặn data > `MaxBlobSize` (2 MiB). Data lớn hơn → cắt nhỏ, batch lên,
ghép lại khi đọc (kèm verify toàn vẹn).

| Hàm | Tác dụng |
|---|---|
| `ChunkBlob(data, maxSize) ([][]byte, *ChunkMeta)` | Cắt thành mảnh ≤ maxSize (≤0 → `DefaultChunkSize` 512 KiB). Vừa 1 mảnh → `[data], nil`. |
| `ReassembleChunks(chunks, *ChunkMeta) ([]byte, error)` | Ghép theo thứ tự + verify `OriginalHash` (bắt lỗi thiếu/hỏng/lệch). |

`ChunkMeta` (`OriginalHash`, `TotalChunks`, `ChunkCommitments`) PHẢI được giữ lại
(ghi on-chain / metadata) để ghép đúng.

```go
chunks, meta := ChunkBlob(bigData, 0)        // cắt theo default 512 KiB
res, _ := bc.SubmitBatch(ctx, chunks)        // N mảnh → 1 height + 1 root
// đọc: RetrieveBlob từng mảnh theo res.Commitments → ReassembleChunks(parts, meta)
```

### c. Merkle proof — chứng minh 1 blob thuộc batch đã neo

`SubmitBatch` neo MỘT root cho N blob. Sau này muốn chứng minh "blob thứ k thuộc
batch này" cho một verifier mà không cần đưa cả N commitment → Merkle proof chỉ
~log₂(N) hash.

| Hàm / type | Tác dụng |
|---|---|
| `BuildMerkleProof(commitments, index) (*MerkleProof, error)` | Tạo proof cho blob thứ `index` trong batch. |
| `VerifyMerkleProof(*MerkleProof) error` | Kiểm `proof.Path` dẫn `proof.Commitment` lên `proof.Root`. |
| `MerkleProof{Root, Commitment, Index, Path}` | Bằng chứng độc lập (serialize gửi đi được). |

```go
proof, _ := BuildMerkleProof(res.Commitments, 7)   // chứng minh frame #7
err := VerifyMerkleProof(proof)                     // path hợp lệ?
// ⚠️ verifier PHẢI tự kiểm proof.Root == root đã neo on-chain — VerifyMerkleProof
// chỉ kiểm tính nhất quán của path, KHÔNG biết root nào đáng tin.
```

> Cây Merkle này là SHA-256 nhị phân CỦA SDK (gom các commitment), KHÁC với NMT
> commitment của từng blob do Celestia tính. NMT chứng minh "blob nằm trên
> Celestia"; merkle này chứng minh "blob thuộc batch của tôi".

### Pipeline đầy đủ

```
GHI:  data → CompressIfBeneficial → (nếu lớn) ChunkBlob → SubmitBatch → neo root
ĐỌC:  RetrieveBlob × N → ReassembleChunks → MaybeDecompress → data gốc
PROOF: BuildMerkleProof(commitments, k) → VerifyMerkleProof + so root on-chain
```

## 8. Chi phí

```go
onchain := len(sub.Commitment)/2 + 8 // 32 byte commitment + 8 byte height
// data off-chain: sub.Size byte
// → on-chain footprint cố định ~40 byte bất kể data lớn cỡ nào
```

Ví dụ telemetry 1 frame ~400 byte → on-chain ~40 byte (10x). Data càng lớn, tỉ lệ
tiết kiệm càng cao; với batch N frame, on-chain vẫn chỉ ~40 byte cho cả batch.

## 9. Hạn chế (đọc kỹ)

- **DA availability window**: Celestia bridge node thường **prune blob sau vài
  tuần**. "Khả dụng" đúng trong cửa sổ DA, không phải vĩnh cửu tuyệt đối. Ứng
  dụng cần lưu lâu hơn phải tự pin/archive (định hướng tương lai).
- **Cần height để retrieve**: nếu mất height (không ghi on-chain / không lưu) thì
  không lấy lại data được dù có commitment.
- **BlobClient ≠ Client**: hai kết nối khác nhau (Celestia bridge vs cosmos-exec).
  Lỗi submit blob không liên quan tới luồng tx.

## 10. Ví dụ chạy được

Xem [`examples/game-telemetry`](../examples/game-telemetry/main.go) — một game đẩy
telemetry lên Celestia, neo commitment on-chain, đọc lại + verify, và in so sánh
chi phí. Chạy:

```bash
go run ./apps/cosmos-exec/sdk/cosmoswasm/examples/game-telemetry \
    --da-rpc http://127.0.0.1:26658 \
    --da-token "$DA_AUTH_TOKEN" \
    --namespace my-game \
    --exec-url http://127.0.0.1:50051 \
    --contract wasm1...   # optional: bỏ thì chỉ chạy phần off-chain
```
