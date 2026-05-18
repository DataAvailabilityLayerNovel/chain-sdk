# Sequencer & Mô hình bảo mật (không có validator set)

Tài liệu này giải thích **vì sao một rollup ev-node không cần validator set kiểu Tendermint**, cái gì thay thế nó, và những đánh đổi bạn nhận khi build dApp trên stack cosmos-exec + cosmoswasm.

> Liên quan: [cosmos-vs-evnode.md](cosmos-vs-evnode.md) (so sánh kiến trúc), [chain-flow.md](chain-flow.md) (vòng đời tx/block), [auto-account-creation.md](auto-account-creation.md) (vì sao tx đầu tiên không cần funding).

## 1. Validator set lo việc gì — và ai lo thay ở đây

Một Cosmos chain truyền thống dùng validator set + Tendermint BFT + staking để lo **ba việc** cùng lúc:

1. **Ordering** — quyết thứ tự tx trong block.
2. **Finality** — chốt block bằng 2/3 phiếu validator.
3. **State correctness** — validator chạy state machine và đồng thuận trên kết quả.

ev-node **tách ba việc này ra**, không gộp vào một validator set:

| Việc | Cosmos thường | ev-node (rollup) |
|------|---------------|------------------|
| Ordering | Validator set + BFT | **Sequencer** (single hoặc based) |
| Data availability + bất biến | Validator set lưu | **Celestia DA** — header blob + data blob được publish lên Celestia |
| State correctness | 2/3 validator vote | **Sovereign verification** — bất kỳ full node nào tải data từ DA về *chạy lại và tự verify*, không cần tin sequencer |

Hệ quả mấu chốt: **sequencer không thể giả mạo state**. Nó chỉ ký `SignedHeader` rồi publish lên DA; full node khác chạy lại tx từ data blob, nếu `app_hash` không khớp thì block bị từ chối ngay. Bỏ validator set **không** đồng nghĩa "phải tin một bên về tính đúng".

## 2. Hai chế độ sequencer

ev-node hỗ trợ hai sequencer (xem `pkg/sequencers/single` và `pkg/sequencers/based`):

| Khía cạnh | **Single** (hybrid) | **Based** (pure DA) |
|-----------|---------------------|---------------------|
| Mempool | Có (`BatchQueue` persistent) | Không |
| Nguồn tx | Mempool **+** forced inclusion | **Chỉ** forced inclusion từ DA |
| `SubmitBatchTxs` | Lưu vào queue | No-op (bỏ qua mempool) |
| `VerifyBatch` | Validate proof | Luôn `true` (tx đều từ DA, đã verified) |
| Liveness | Phụ thuộc sequencer sống | Cao nhất — sống chừng nào DA sống |
| Use case | Rollup truyền thống (mặc định cho dApp) | Cần đảm bảo liveness/chống kiểm duyệt tối đa |

Bật qua `NodeConfig`:

```go
type NodeConfig struct {
    Aggregator     bool // bật block production
    BasedSequencer bool // dùng based sequencer (yêu cầu Aggregator)
    LazyMode       bool // chỉ ra block khi có tx
}
```

> **Based sequencer** chính là cách ev-node "bỏ luôn cả sequencer như một điểm tin": không có mempool, mọi tx phải đi qua DA, nên thứ tự do chính DA layer quyết. Đổi lại độ trễ cao hơn (chờ epoch DA) và không có mempool UX.

## 3. Forced Inclusion — chống kiểm duyệt khi vẫn dùng single sequencer

Đây là cơ chế khiến single sequencer **không** thể kiểm duyệt vĩnh viễn tx của bạn.

```
User gửi tx thẳng vào forced-inclusion namespace trên DA
        │
        ▼
DA lưu tx tại height H
        │
        ▼
Sequencer chạm epoch boundary (mặc định epoch = 50 DA block —
  Genesis.DAEpochForcedInclusion)
        │
        ▼
ForcedInclusionRetriever.Retrieve(epochStart, epochEnd)
  (AsyncBlockRetriever prefetch 2x epoch để giảm latency)
        │
        ▼
GetNextBatch trả tx kèm ForceIncludedMask[i] = true
        │
        ▼
Execution layer validate tx forced (skip validation cho tx mempool đã verified)
```

Điểm cần nhớ:

- Tx forced-inclusion được nhận diện qua **namespace riêng thứ ba** trên DA (ngoài header namespace và data namespace).
- `ForceIncludedMask` phân biệt tx "từ DA — phải validate" với tx "từ mempool — đã validate", vừa bảo mật vừa tối ưu hiệu năng.
- Nếu sequencer cố tình bỏ qua tx đã nằm trong forced-inclusion namespace quá grace period → sequencer bị coi là **malicious**, tx vẫn được đưa vào.
- Xử lý theo **epoch** (không query DA mỗi block) + **checkpoint** (`DAHeight` + `TxIndex`) để resume được sau crash.

→ Tức là kể cả single sequencer, người dùng luôn có "đường vòng" qua DA để ép tx vào chain. Censorship chỉ làm *chậm*, không *chặn vĩnh viễn*.

## 4. Vậy "không có validator" mất gì?

| Rủi ro | Ảnh hưởng thực tế | Giảm nhẹ trong ev-node |
|--------|-------------------|------------------------|
| **Liveness / SPOF** | Single sequencer chết → chain **ngừng ra block** (không sai state, chỉ treo) | Chạy **based sequencer** hoặc thêm cơ chế failover |
| **Censorship tạm thời** | Sequencer có thể trì hoãn tx | **Forced inclusion** đảm bảo cuối cùng vẫn vào |
| **Reorg ngắn** | Trước khi block lên DA, thứ tự về lý thuyết có thể đổi | Sau khi ghi lên Celestia coi như chốt |
| **Không BFT finality** | "Finality" = lúc data nằm trên Celestia, không phải vote validator | Đây là tính chất của sovereign rollup, không phải bug |

**Không mất:** tính đúng của state (sovereign verification), bất biến lịch sử (DA).

## 5. Liên hệ tới phí (0-fee) trên cosmos-exec

Vì không có validator set cần thưởng staking, stack cosmos-exec không bắt buộc phí. Lưu ý đúng theo code:

- Mặc định (`COSMOS_EXEC_ENFORCE_SIGNATURES` không set) → **không có ante handler nào chạy** (`app.go:295`): không verify chữ ký, không sequence, không gas, **không** fee.
- Khi bật `COSMOS_EXEC_ENFORCE_SIGNATURES=true` → ante chain chạy nhưng `TxFeeChecker` vẫn **chấp nhận tx phí 0**; `AutoCreateAccount` chạy trước `DeductFee` nên account Keplr mới gửi tx đầu tiên không cần nạp tiền.

Đánh đổi: **0-fee = không có lớp chống spam kinh tế**. Phù hợp app sovereign/permissioned/dev. Muốn bật fee > 0 **không chỉ** là đổi `TxFeeChecker` — còn phải bật ante (bước 0) và thêm cơ chế faucet/cấp vốn (account mặc định số dư 0). Xem đầy đủ ở [fee-economics.md](fee-economics.md) mục 6 & 6b. Chi phí không biến mất hẳn — **operator rollup vẫn trả blob fee cho Celestia** khi publish data.

## 6. Khi nào chọn gì (cho dApp của bạn)

- **Dev / demo / app sovereign nội bộ**: single sequencer + 0-fee. Đơn giản nhất, UX tốt nhất (vào là dùng, không phí). Chấp nhận sequencer là điểm tin về *liveness*.
- **Cần chống kiểm duyệt mạnh / liveness cao**: bật `BasedSequencer = true`. Mất mempool UX và độ trễ cao hơn, đổi lấy không có điểm tin ordering.
- **Public, giá trị cao**: single sequencer + forced inclusion bật sẵn + thêm fee token thật + kế hoạch multi-sequencer/failover.

## Tham chiếu code

| Thành phần | File |
|------------|------|
| Sequencer interface | `core/sequencer/sequencing.go` |
| Single (hybrid) sequencer | `pkg/sequencers/single/sequencer.go`, `queue.go` |
| Based (pure DA) sequencer | `pkg/sequencers/based/sequencer.go` |
| Checkpoint dùng chung | `pkg/sequencers/common/checkpoint.go` |
| Forced inclusion retrieval | `block/internal/da/forced_inclusion_retriever.go` |
| Async prefetch | `block/internal/da/async_block_retriever.go` |
| Block production | `block/internal/executing/executor.go` |
| Sync (DA + P2P + forced) | `block/internal/syncing/syncer.go` |
| 0-fee ante | `apps/cosmos-exec/app/ante.go` |
