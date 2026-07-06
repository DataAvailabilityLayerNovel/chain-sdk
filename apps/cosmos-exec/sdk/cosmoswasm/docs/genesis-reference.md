# genesis.json — các trường cần thiết & cách điền

File mẫu: [genesis.example.json](genesis.example.json). Tài liệu này giải thích **từng trường**, **giá trị nào tự sinh / phải tự điền**, và **cách lấy giá trị đúng**. Liên quan: [error-reference.md §9.1](error-reference.md#91-da-sẵn-sàng-dữ-liệu) (lỗi `da_start_height`/`height is equal to 0`), [node-operations.md](node-operations.md).

> **Quan trọng — bình thường bạn KHÔNG tự viết file này.** Lệnh `evcosmos init` tự tạo `genesis.json` đầy đủ ([init.go:75](../../../../cosmos-wasm/cmd/init.go#L75) → [CreateGenesis](../../../../../pkg/genesis/io.go#L23)). Trường duy nhất bạn thường phải **sửa tay sau khi init** là `da_start_height`. File mẫu chỉ để tham khảo định dạng và khi cần tạo thủ công.

## Vị trí file

`<home>/config/genesis.json` ([GenesisPath](../../../../../pkg/genesis/io.go#L14)). Ví dụ khi chạy qua run-script:

```
.cosmos-wasm-runner/nodes/evcosmos-sequencer/config/genesis.json   # nguồn (do sequencer tạo)
.cosmos-wasm-runner/nodes/evcosmos-fullnode/config/genesis.json    # bản copy sang full node
```

Sequencer tạo genesis rồi **copy nguyên si sang full node** → sửa ở **sequencer** rồi copy lại để hai bên khớp (lệch genesis = full node từ chối sync).

## Các trường (struct [Genesis](../../../../../pkg/genesis/genesis.go#L13))

| Trường (JSON) | Kiểu | Tự sinh? | Ý nghĩa & cách điền |
|---------------|------|----------|---------------------|
| `chain_id` | string | từ `--chain_id` lúc init | Tên chain, phải **giống nhau** trên mọi node. Đổi = chain khác. |
| `start_time` | RFC3339 time | tự (`time.Now()` lúc init) | Mốc thời gian genesis. Giữ nguyên giá trị `init` sinh ra; chỉ đặt tay nếu muốn lùi/hẹn giờ khởi chạy. |
| `initial_height` | uint64 | mặc định `1` | Height block đầu tiên của rollup. Hầu như luôn để `1`. |
| `proposer_address` | base64 (bytes) | **tự** (`CreateSigner`, [init.go:57](../../../../cosmos-wasm/cmd/init.go#L57)) | = `SHA256(pubkey sequencer)` (không phải pubkey thô), encode base64 — xem [node-operations.md §5b.6](node-operations.md#5b6-node-key--signer-key--sinh-ra-thế-nào-lấy-key-từ-đâu). **Không tự bịa** — phải khớp signer key trong `config/signer.json`. Full node để `null` (không ký). |
| `da_start_height` | uint64 | mặc định **`0`** ⚠️ | Height **Celestia** nơi blob đầu tiên của rollup được publish. **Đây là trường bạn cần sửa.** Xem cách lấy bên dưới. |
| `da_epoch_forced_inclusion` | uint64 | mặc định `50` ([genesis.go:39](../../../../../pkg/genesis/genesis.go#L39)) | Số block DA = 1 epoch cho forced-inclusion. Để mặc định nếu không dùng/không hiểu rõ forced inclusion. |

## Điền `da_start_height` cho đúng

Để `0` gây cảnh báo `da_start_height is not set...` và có thể đẻ lỗi `ERR failed to get blobs error="height is equal to 0"` (full node query DA từ height 0 — Celestia đánh số từ 1). Đặt nó = **head height hiện tại của Celestia** lúc bạn tạo genesis:

```bash
# Hỏi head height của Celestia node bạn đang dùng làm DA:
celestia header sync-state --node.store <path>     # xem "height"
# hoặc qua RPC (DA_RPC trong .env):
curl -s -X POST $DA_RPC -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"header.NetworkHead","params":[]}' | jq '.result.header.height'
```

Lấy số đó điền vào `da_start_height`. Node sẽ bắt đầu quét DA từ đúng chỗ thay vì dò từ 1 (rất chậm / sinh lỗi height 0).

> Nếu **không** sửa được genesis nhưng DA RPC reachable: node tự gọi `GetLatestDAHeight` để nhảy tới head ([syncer.go:214](../../../../../block/internal/syncing/syncer.go#L214)) — khi đó `0` vẫn chạy được, chỉ là kém tường minh.

## Cách tạo "chuẩn" (khuyến nghị) thay vì viết tay

```bash
# init sinh genesis.json + signer.json + node key, proposer_address tự điền:
evcosmos init --home <home> --chain_id cosmos-wasm-devnet
# rồi mở <home>/config/genesis.json sửa "da_start_height" thành head Celestia.
```
