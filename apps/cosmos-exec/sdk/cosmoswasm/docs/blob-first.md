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

### 6.1 `SubmitBatch` làm gì, từng bước ([blob.go:190](../blob.go#L190))

```
blobs [][]byte
  │  ① validate: không rỗng; mỗi blob không rỗng; TỔNG bytes ≤ MaxBatchTotal
  ▼
  │  ② mỗi data → jsonrpc.NewBlobV0(ns, data)  → blob.Commitment (NMT, 32B)
  │      thu commitments[] = hex của từng commitment (THỨ TỰ = thứ tự input)
  ▼
  │  ③ da.Blob.Submit(daBlobs, opts)  ← MỘT lần gọi cho CẢ N blob
  │      → trả về MỘT height duy nhất cho toàn batch
  ▼
  │  ④ merkle.BuildProof(commitments, 0) → lấy Root (path của leaf 0 bị bỏ)
  ▼
BlobBatchResponse{ Root, Commitments[], Count, Height }
```

Những điểm phải nắm:

- **Nguyên tử theo height.** Cả N blob vào Celestia trong **một** `Blob.Submit`, nên
  hoặc tất cả cùng land ở **một** `Height`, hoặc submit lỗi và **không** cái nào land.
  Không có chuyện batch "land một nửa".
- **`Height` là khóa dùng chung để retrieve từng blob.** Lấy blob thứ `i` về:
  `RetrieveBlob(ctx, res.Height, res.Commitments[i])` — cùng height, khác commitment.
  Mất `Height` = mất cả batch (giống blob lẻ, xem **Mục 4**).
- **Thứ tự `Commitments[]` = thứ tự `blobs[]` đầu vào, và là cố định.** Merkle root
  phụ thuộc thứ tự này; đảo thứ tự → root khác. `BuildMerkleProof(res.Commitments, i)`
  cũng đánh index theo đúng thứ tự đó — nên **đừng sort/reorder** `Commitments` trước khi tạo proof.
- **`Root` KHÁC bản chất với `Commitments[i]`.** `Commitments[i]` là NMT commitment do
  Celestia tính ("blob nằm trên Celestia"); `Root` là SHA-256 Merkle tree **của SDK**
  gom N commitment lại ("N blob này thuộc cùng một batch của tôi"). Ghi on-chain chỉ 1 `Root`.
- **Giới hạn `MaxBatchTotal`.** Vòng lặp cộng dồn `len(data)`; vượt trần → lỗi ngay
  (không submit gì). Đây là trần **tổng** của batch, khác `MaxBlobSize` (trần từng blob).
- **Lỗi từng bước đều fail-fast, chưa tốn DA.** Blob rỗng / vượt trần / `NewBlobV0` lỗi
  đều trả lỗi **trước** khi gọi `Blob.Submit` → không mất phí DA cho batch hỏng.

> **N = 1:** batch một phần tử hợp lệ — `Root` bằng luôn `Commitments[0]` (cây 1 lá),
> và `BuildMerkleProof(..., 0)` trả `Path` rỗng. Verify vẫn chạy đúng (xem [7b.c](#c-merkle-proof--chứng-minh-1-blob-thuộc-batch-đã-neo)).

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

#### Cây được dựng thế nào ([internal/merkle](../internal/merkle/merkle.go))

- **Lá (leaf) = chính commitment 32 byte**, KHÔNG hash lại. Danh sách lá đúng theo
  thứ tự `Commitments[]`.
- **Nút trong = `sha256(left ‖ right)`** — nối 32B trái + 32B phải rồi băm.
- **Số nút lẻ ở một tầng → nhân đôi nút cuối** (`right = left`). Vd 3 lá `[A,B,C]`:

```
tầng lá:   A      B      C
             \    /      | (C ghép với chính nó)
tầng 1:   H(A‖B)      H(C‖C)
              \          /
root:      H( H(A‖B) ‖ H(C‖C) )
```

#### `BuildMerkleProof` sinh ra gì

Trả `MerkleProof{Root, Commitment, Index, Path}`, trong đó `Path` là danh sách
`ProofStep{SiblingHash, IsLeft}` — mỗi bước từ lá đi lên là **hash anh em** cần ghép
và cờ **anh em nằm bên trái hay phải**. Độ dài `Path` ≈ `log₂(N)`. Đây là toàn bộ
thứ verifier cần: **không** phải cầm N commitment, chỉ ~log₂(N) hash.

#### `VerifyMerkleProof` kiểm thế nào ([Verify](../internal/merkle/merkle.go#L81))

```
current = proof.Commitment            // bắt đầu từ lá
với mỗi step trong proof.Path:
    if step.IsLeft:  current = sha256(step.SiblingHash ‖ current)
    else:            current = sha256(current ‖ step.SiblingHash)
return current == proof.Root ? OK : lỗi
```

Nó chỉ trả lời đúng một câu: *"đi từ `Commitment` theo `Path` có ra `Root` không?"*.

**Bắt buộc hiểu — VerifyMerkleProof KHÔNG đủ để tin:** nó **tự dựng** rồi so với
`proof.Root` **do chính proof mang theo**. Kẻ tấn công có thể bịa ra một cây giả
hoàn toàn nhất quán (commitment giả + path giả + root giả) và `VerifyMerkleProof`
vẫn trả `nil`. Muốn tin thật, verifier phải làm **hai việc**:

1. `VerifyMerkleProof(proof)` → path nội bộ nhất quán, **và**
2. **Tự đối chiếu** `proof.Root` **== root đã neo on-chain** (đọc từ contract nơi
   `BuildBatchRootTx` đã ghi). Bước 2 mới là chỗ "root nào đáng tin" đến từ chain.

Bỏ bước 2 = proof vô nghĩa về mặt bảo mật.

#### Vài cạnh cần lưu ý

- **`Index` chỉ để tham khảo.** Vị trí lá thực sự được mã hoá bằng cờ `IsLeft` trong
  từng `ProofStep`, không phải bằng `proof.Index`. `Verify` không đọc `Index`.
- **N = 1:** `Path` rỗng → `current` = `Commitment` = `Root` ngay → hợp lệ (khớp ghi
  chú ở [6.1](#61-submitbatch-làm-gì-từng-bước-blobgo190)).
- **⚠️ Malleability của kiểu "nhân đôi nút cuối" + không domain-separation.** Vì lá và
  nút trong đều băm bằng `sha256` **không có tiền tố phân biệt**, và tầng lẻ nhân đôi
  nút cuối, cây này có điểm yếu kinh điển (kiểu CVE-2012-2459): có thể tồn tại tập lá
  khác cho **cùng một root**. Với blob-first thì rủi ro thấp (verifier neo root
  on-chain và thường biết `Count`), nhưng **đừng** dùng cây SDK này làm bằng chứng
  chống-gian-lận độc lập cho giá trị lớn nếu không kèm ràng buộc `Count`/độ sâu. Cho
  nhu cầu mạnh hơn, dùng thẳng **DA-inclusion proof (NMT)** của Celestia ([Mục 7](#7-xác-minh-verifiable)).

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
