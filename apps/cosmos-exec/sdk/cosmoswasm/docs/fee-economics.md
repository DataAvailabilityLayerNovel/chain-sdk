# Kinh tế phí: rollup có fee vs Cosmos chain thường

Tài liệu này trả lời chi tiết câu hỏi: **nếu bật fee thật (bỏ chế độ 0-fee), tổng chi phí có còn rẻ hơn một Cosmos chain thường không — tính cả phần operator/sequencer phải trả cho Celestia?**

Câu trả lời ngắn: **ở throughput thực tế thì rẻ hơn rõ rệt; ở throughput rất thấp thì có thể đắt hơn.** Phải so **tổng chi phí kinh tế**, không so "user fee với user fee".

> Liên quan: [sequencer-security.md](sequencer-security.md) (mô hình không validator), [cosmos-vs-evnode.md](cosmos-vs-evnode.md), [configuration.md](configuration.md).

## Mục lục

- 1. Mô hình chi phí thật của rollup
- 1b. Hàm ước lượng phí: `/tx/estimate` vs `/tx/simulate`
- 1c. Hóa đơn Celestia THẬT được tính thế nào
- 2. Ai trả gì
- 3. Tổng chi phí vận hành rollup / tháng
- 4. Điểm hòa vốn
- 5. Đòn bẩy giảm phí
- 6. Cách bật fee thật
- 6b. Faucet / cấp vốn (A+B đã implement)
- 6c. Tích hợp Web/UI (nút "Get test tokens")
- 6d. Sweep phí cuối block về treasury
- 7. Checklist production có fee
- 8. Kết luận
- Tham chiếu code

---

## 1. Mô hình chi phí thật của rollup

Stack này đã có sẵn cost model thật trong `cmd/cosmos-exec-grpc/cost.go`. Mỗi tx có **hai thành phần phí**:

```
total_cost(tx) = DA_cost + gas_cost

DA_cost  = bytes(tx) × TIA_PER_BYTE          // trả cho Celestia, denom mặc định "TIA"
gas_cost = gas(tx)   × MIN_GAS_PRICE         // phí thực thi, denom mặc định "ustake"
```

Hằng số (env, đọc 1 lần lúc khởi động — `getCostPolicy()`):

| Env | Default | Ý nghĩa |
|-----|---------|---------|
| `COSMOS_EXEC_TIA_PER_BYTE` | `0.0000001` | Giá DA mỗi byte (TIA) |
| `COSMOS_EXEC_MIN_GAS_PRICE` | `0.000001` | Giá gas thực thi |
| `COSMOS_EXEC_DA_DENOM` | `TIA` | Denom phần DA |
| `COSMOS_EXEC_GAS_DENOM` | `ustake` | Denom phần gas |

Hiện chain chạy **ante 0-fee** (`NewPermissionlessAnteHandler`, `apps/cosmos-exec/app/ante.go`) nên con số này là **mô phỏng** — gọi `/tx/estimate` (`CostBreakdown`) để xem "production economics sẽ trông thế nào" mà không drain ví test. Bật fee thật = thay TxFeeChecker (mục 6).

**Điểm mấu chốt:** `DA_cost` không biến mất khi bật fee — nó luôn tồn tại, chỉ là hiện đang do **operator gánh ngầm** (operator trả Celestia bằng TIA khi publish blob). Bật fee chỉ là **chuyển khoản DA_cost đó sang user**.

---

## 1b. Hàm ước lượng phí: `/tx/estimate` vs `/tx/simulate` (cách tính chi tiết)

Có **hai** endpoint ước lượng, khác nhau ở chỗ *đo thật* hay *tính theo công thức*:

| | `/tx/estimate` | `/tx/simulate` |
|---|---|---|
| Trả lời | "Tx này *sẽ* tốn bao nhiêu (cả DA + gas)?" | "Gas THẬT là bao nhiêu → gas_limit + fee để ký?" |
| Chạy tx? | Không (trừ khi tra theo `hash`) | **Có** — chạy ante+handler, không commit |
| Thành phần | DA_cost **+** gas_cost (mô phỏng) | Chỉ gas/fee (đúng cái ante enforce) |
| Dùng khi | dashboard/operator xem "kinh tế production" | **ví gọi trước khi ký** để fee khớp tx thật |
| Code | `txEstimateHandler` (main.go) + `cmd/cosmos-exec-grpc/cost.go` | `txSimulateHandler` (main.go) + `feeForGas` (faucet.go) |

### A. `/tx/estimate` — phép tính chính sách áp lên số đo

Nhận 1 trong 3 dạng input (`estimateRequest`), suy ra `(bytes, gas)`:

| Input | `bytes` lấy từ | `gas` lấy từ |
|---|---|---|
| `{hash}` | tx đã chạy (`result.Bytes`) | `result.GasUsed` (đo thật) |
| `{tx_base64\|tx_hex}` | `len(raw)` | caller tự truyền (chưa chạy nên không tự biết) |
| `{bytes, gas}` | trực tiếp | trực tiếp |

Rồi `costPolicy.estimate(bytes, gas)` áp đúng công thức 2 dòng ở Mục 1:

```
EstDAAmount  = bytes × COSMOS_EXEC_TIA_PER_BYTE     // big.Float, in TIA
EstGasAmount = gas   × COSMOS_EXEC_MIN_GAS_PRICE     // big.Float, in ustake
```

Dùng `big.Float` (mantissa 80 bit) để giá/byte rất nhỏ × hàng triệu byte không
mất số lẻ; kết quả trả về dạng **chuỗi thập phân** (`trimZeros`) để không sai số
qua JSON. Response (`CostBreakdown`) phơi luôn `da_price_per_byte` + `min_gas_price`
đã dùng → dashboard biết vì sao số thay đổi khi operator retune.

> `/tx/estimate` **không** chạy tx (trừ dạng `hash`), nên với raw tx nó chỉ biết
> `bytes`; `gas` phải do caller cung cấp. Muốn `gas` chính xác → dùng `/tx/simulate`.

### B. `/tx/simulate` — đo gas thật rồi suy ra fee

Nhận raw tx, decode, rồi `exec.SimulateTx(ctx, raw)` chạy tx qua **ante + msg
handler nhưng KHÔNG commit** → `SimulateResult{GasUsed, GasWanted}`. Đây là gas
**thật** mà tx tiêu, không phải phỏng đoán theo byte.

Ba bước biến `gas_used` → `fee`:

```
1. gas_limit = ceil(gas_used × COSMOS_EXEC_GAS_ADJUSTMENT)
   // cài bằng số nguyên permille để tránh float:
   //   permille = 1300 (=1.3 mặc định)
   //   gas_limit = (gas_used × permille + 999) / 1000     // +999 = làm tròn LÊN
   //   nếu gas_limit == 0 → fallback gas_wanted

2. fee = feeForGas(gas_limit):
   price = COSMOS_EXEC_MIN_GAS_PRICE  (LegacyDec; rỗng/≤0 → KHÔNG fee, trả nil)
   amount = ceil(price × gas_limit)                         // RoundInt sau Ceil
   denom  = COSMOS_EXEC_GAS_DENOM (mặc định "ustake")
   → sdk.Coins{ amount denom }

3. response: { gas_used, gas_wanted, gas_limit, fee, fee_denom, fee_amount }
```

**Vì sao nhân 1.3?** Gas đo lúc simulate có thể lệch nhẹ so với lúc thực thi
(state đã đổi giữa hai thời điểm). Nhân hệ số đệm (gas adjustment, chuẩn Cosmos)
để tránh tx out-of-gas. Tinh chỉnh qua env:

| Env | Default | Ý nghĩa |
|---|---|---|
| `COSMOS_EXEC_GAS_ADJUSTMENT` | `1.3` | Hệ số đệm gas_used → gas_limit (clamp ≥ 1.0) |
| `COSMOS_EXEC_MIN_GAS_PRICE` | `0.000001` | Giá gas → fee (cùng giá ante enforce) |
| `COSMOS_EXEC_GAS_DENOM` | `ustake` | Denom của fee |

Vì `feeForGas` dùng đúng `COSMOS_EXEC_MIN_GAS_PRICE` mà ante kiểm tra, **fee do
`/tx/simulate` trả ra chính là mức ante sẽ yêu cầu** khi bật enforce (Mục 6) —
không lệch giữa "ước lượng" và "thực thu".

### C. Quy trình ví khuyến nghị

```
/tx/simulate(tx chưa ký) → gas_limit + fee
        ↓ (gắn gas_limit + fee vào tx)
ký tx → /tx/submit → /tx/result (đợi kết quả)
```

`/tx/estimate` để **quan sát/định giá** (gồm cả DA_cost cho bức tranh kinh tế đầy
đủ); `/tx/simulate` để **lấy số đi ký**. Hai cái bổ sung nhau, không thay thế.

> Liên hệ blob-first: `/tx/simulate` cho `gas_used` THẬT của một tx nhúng data lớn
> vào WASM — chính là con số "direct" để so với chi phí blob-first (chỉ ~40 byte
> commit). Xem [blob-first.md](blob-first.md) và mục cost-estimation.

---

## 1c. Hóa đơn Celestia THẬT được tính thế nào (API + công thức)

Mục 1 dùng `DA_cost = bytes × TIA_PER_BYTE` — đó là **xấp xỉ tuyến tính do
operator chỉnh**, tiện cho dashboard. Nhưng hóa đơn TIA operator thật sự trả cho
Celestia **không tuyến tính theo byte**: Celestia tính **gas** theo *share*
(khối 512 byte), rồi `fee = gas × gas_price`. Mục này nói rõ gọi API gì và công
thức ra sao, để khi đặt `TIA_PER_BYTE` (mục 7) bạn biết mình đang xấp xỉ cái gì.

### A. Gọi API nào

ev-node publish blob qua **celestia-node JSON-RPC**, method `blob.Submit`:

```
blob.Submit(blobs []*Blob, opts *SubmitOptions) → height
```

| Tầng | Code | Việc |
|------|------|------|
| ev-node DA client | [`block/internal/da/client.go`](../../../../../block/internal/da/client.go) `Submit` | đóng gói data thành `Blob` (namespace + share version 0), gọi `blobAPI.Submit` |
| JSON-RPC shape | [`pkg/da/jsonrpc/client.go`](../../../../../pkg/da/jsonrpc/client.go), [`submit_options.go`](../../../../../pkg/da/jsonrpc/submit_options.go) | bản sao JSON của `celestia-node/state.TxConfig` |
| Celestia node | (process ngoài) | bọc blob vào tx **`MsgPayForBlobs` (PFB)**, ước lượng gas, ký, broadcast tới celestia-app validator |

`SubmitOptions` (mirror `state.TxConfig`) điều khiển giá:

```go
type SubmitOptions struct {
    GasPrice      float64 // utia/gas; chỉ áp dụng khi IsGasPriceSet=true
    IsGasPriceSet bool
    MaxGasPrice   float64 // trần khi để node tự định giá
    Gas           uint64  // gas_limit; 0 = để node tự ước lượng
    ...
}
```

**Mặc định trong stack này:** `da_submitter` truyền `gasPrice = -1` nhưng client
**bỏ qua tham số đó** (`func (c *client) Submit(... _ float64, ...)`), giá lấy
hoàn toàn từ `config.DA.SubmitOptions` (JSON). Nếu không cấu hình → `Gas=0`,
`IsGasPriceSet=false` → **celestia-node tự ước lượng cả gas lẫn gas price** (theo
min-gas-price của node, chặn trên bởi `MaxGasPrice`). Operator muốn cố định thì
set `gas_price` / `max_gas_price` / `gas` trong `SubmitOptions`.

### B. Công thức gas (theo share, không theo byte)

Celestia xếp data thành **share = 512 byte** (`ShareSize`). Mỗi share mất chỗ cho
metadata nên không chứa đủ 512 byte payload:

| Hằng (go-square / celestia-app) | Giá trị | Ý nghĩa |
|---|---|---|
| `ShareSize` | 512 | kích thước 1 share |
| Namespace + info byte | 29 + 1 | header mỗi share |
| Sequence length | 4 | chỉ ở share **đầu** của blob |
| → payload share đầu | **478** | `512 − 29 − 1 − 4` |
| → payload share tiếp | **482** | `512 − 29 − 1` |
| `GasPerBlobByte` | 8 | gas mỗi byte-share |
| `PFBGasFixedCost` | ~65 000 | phí cố định mỗi tx PFB |

Số share cho 1 blob (làm tròn LÊN — đây là chỗ "không tuyến tính"):

```
shares(size) = 1 + ceil( max(0, size − 478) / 482 )      // blob ≤ 478B vẫn = 1 share
```

Gas của phần dữ liệu + phí cố định (≈ công thức `EstimateGas` của celestia-app):

```
gas_data  = Σ_blob ( shares(size) × 512 × 8 )            // GasPerBlobByte = 8
gas_total = gas_data  +  PFBGasFixedCost (+ tx-size cost nhỏ)
```

### C. Phí = gas × giá

```
fee_utia = ceil( gas_total × gas_price )                 // gas_price tính bằng utia/gas
```

`gas_price` ≥ min-gas-price của mạng Celestia (mainnet hiện ~**0.002 utia/gas**;
có thể cao hơn khi tắc nghẽn). `1 TIA = 1_000_000 utia`.

### D. Vì sao `TIA_PER_BYTE × bytes` chỉ là xấp xỉ

- **Lượng tử theo 512B:** blob 1 byte và blob 478 byte tốn **bằng nhau** (1 share).
  Per-byte cost của blob nhỏ rất cao, của blob lớn mới hội tụ.
- **Có phí cố định** (`PFBGasFixedCost`): blob càng nhỏ, phần cố định càng áp đảo
  → đây chính là lý do blob-first **gom batch** (mục 3, "amortize"): chia một PFB
  cho nhiều record.
- **Cận tuyến tính khi blob lớn:** với blob lớn, per-byte → `512 × 8 / 482 × gas_price`
  ≈ `8.5 gas/byte × gas_price`. Quy ra TIA:

  ```
  TIA_PER_BYTE_thực ≈ (8.5 × gas_price_utia) / 1_000_000          [TIA/byte]
  ví dụ gas_price = 0.002 utia → ≈ 8.5 × 0.002 / 1e6 ≈ 1.7e-8 TIA/byte
  ```

  Mặc định `COSMOS_EXEC_TIA_PER_BYTE = 1e-7` (mục 1) đặt **cao hơn** con số này →
  có biên an toàn cho phần cố định + biến động giá TIA. Đúng tinh thần checklist
  mục 7: "đặt `TIA_PER_BYTE` sao cho fee thu ≥ hóa đơn Celestia (cộng biên)".

> Lưu ý phiên bản: các hằng (`GasPerBlobByte=8`, `PFBGasFixedCost`,
> `ShareSize=512`) là default của celestia-app (mirror tại celestia-node v0.28.4
> trong [`pkg/da/jsonrpc`](../../../../../pkg/da/jsonrpc)). Có thể đổi theo
> upgrade Celestia — kiểm tra `appconsts` của phiên bản đang chạy trước khi chốt
> số cho production.

---

## 2. Ai trả gì

| Khoản | Cosmos chain thường (sovereign) | Rollup ev-node (bật fee) |
|-------|--------------------------------|--------------------------|
| Bảo mật consensus | **Lạm phát token** trả staking reward (≈5–20%/năm) — ẩn, mọi holder gánh qua pha loãng | Không có (không validator set) |
| Hạ tầng | N validator × full node | 1 sequencer (+ full node tùy chọn) |
| Lưu trữ data / DA | Validator tự replicate (không hiện thành phí) | **Blob fee Celestia** — operator trả TIA mỗi block, hiện rõ |
| Phí user | gas × min gas price (chảy về validator) | gas_cost + DA_cost (bù hóa đơn Celestia) |

→ Khác biệt cốt lõi: chi phí bảo mật của chain thường **ẩn trong lạm phát**; của rollup **hiện minh bạch thành hóa đơn TIA**. Đừng so phí user trực tiếp — phải cộng cả "thuế lạm phát" của chain thường vào.

### 2a. "Lạm phát" ở dòng Bảo mật consensus nghĩa là gì

**Vì sao chain thường buộc phải có lạm phát.** Cosmos chain dùng Proof-of-Stake: độ an toàn = tổng token được **stake** (khóa) bởi validator — kẻ tấn công phải mua ⅓–⅔ số token đó. Stake có chi phí cơ hội (khóa tiền, rủi ro slash) nên không ai stake miễn phí; chain phải **trả thưởng** để dụ stake. Một phần thưởng từ phí giao dịch, **phần lớn từ token mới đúc ra** — đó là lạm phát (module `x/mint`, tỷ lệ ~5–20%/năm tùy bonded ratio).

**Lạm phát cụ thể = tổng cung phình ra → pha loãng.** Ví dụ số:

- Năm 0: tổng cung 100 triệu; bạn giữ 1 triệu = **1%** mạng.
- Lạm phát 15%/năm → Năm 1: tổng cung 115 triệu. Bạn **vẫn** giữ 1 triệu nhưng chỉ còn **0,87%** (1/115). 15 triệu token mới chảy về **người đi stake**.

Không có dòng nào ghi "phí bảo mật", nhưng **tỷ trọng sở hữu của bạn bị bào mòn** để trả cho người giữ an toàn chain → đây là **thuế ẩn / pha loãng**:

- Người **stake**: nhận token mới, bù lại phần pha loãng.
- Người **chỉ giữ** (holder thụ động, user cầm token trả phí, sàn, cầu nối): gánh toàn bộ, không được bù.

Bản chất: lạm phát là cuộc **chuyển giao tài sản** từ người không stake sang người stake. Tổng chi phí bảo mật ≈ token in ra/năm × giá token — **rất thực và lớn**, chỉ không hiện thành "fee".

**Vì sao rollup ev-node không có khoản này.** Không validator set, không PoS (xem [sequencer-security.md](sequencer-security.md)): ordering do sequencer, bảo mật data thuê Celestia (trả **blob fee TIA, hiện rõ**), tính đúng state do sovereign verification. Không cần dụ ai khóa token → **không `x/mint`, không lạm phát, không pha loãng**. Chi phí bảo mật vẫn còn nhưng đổi hình thức: từ "lạm phát ẩn, holder gánh" → "hóa đơn Celestia minh bạch, operator gánh và tính vào fee user".

| | Cosmos chain thường | Rollup ev-node |
|---|---|---|
| Cơ chế an toàn | PoS — token stake | Celestia DA + sovereign verify |
| Trả cho ai | Validator/delegator | Celestia (TIA) |
| Hình thức chi phí | Token mới in ra (**lạm phát**) | Blob fee (**hóa đơn TIA**) |
| Ai gánh | Holder thụ động (pha loãng) | Operator (rồi tính vào fee user) |
| Nhìn thấy được? | Ẩn (tỷ trọng giảm dần) | Hiện (`/tx/estimate`, hóa đơn TIA) |

Một dòng: **chain thường "in tiền" để mua an ninh — bạn trả bằng tiền loãng dần; rollup "thuê Celestia" — trả bằng hóa đơn TIA rõ ràng.** Cùng là chi phí bảo mật, khác ở chỗ hiện/ẩn và ai gánh.

---

## 3. Tổng chi phí vận hành rollup / tháng

Operator trả Celestia theo **byte blob mỗi block**. Mỗi block publish ít nhất header blob (+ data blob nếu có tx):

```
blocks_per_month = (30 × 24 × 3600) / block_time_giây
chi_phí_DA/tháng ≈ blocks_per_month × bytes_blob_trung_bình × TIA_PER_BYTE × giá_TIA_USD
```

Nhận xét quan trọng:

- **Block rỗng vẫn tốn** — vẫn có header blob → vẫn trả Celestia. Đây là **chi phí cố định** không phụ thuộc tx volume.
- Phần data blob được **amortize**: gom nhiều tx vào 1 blob → DA_cost/tx ≈ `(bytes tx / tổng bytes blob) × giá`.
- Vậy: throughput càng cao → chi phí cố định càng được chia mỏng → per-tx càng rẻ.

---

## 4. Điểm hòa vốn (vì sao "không phải lúc nào cũng rẻ hơn")

| Vùng | Ai thắng | Lý do |
|------|----------|-------|
| Throughput cao (batch đầy đặn) | **Rollup thắng đậm** | Loại bỏ chi phí validator-set (lạm phát + N× hạ tầng), thay bằng DA dùng chung rẻ; cố định chia mỏng |
| Throughput trung bình | Rollup thắng nếu bật LazyMode | Bỏ được phần lớn block rỗng |
| Throughput rất thấp, block đều 2s | **Chain thường có thể rẻ hơn** | Rollup vẫn trả Celestia cho hàng loạt block gần rỗng; chi phí cố định/tx phình to |

Điểm hòa vốn phụ thuộc 4 biến: **giá TIA + Celestia gas + block time + tx/block**. Không có con số tuyệt đối — phải đo bằng `/tx/estimate` trên tải thật của bạn.

### Ví dụ minh họa (dùng default constants)

> Số dưới đây là minh họa cách tính, KHÔNG phải benchmark — thay giá TIA thực và đo blob size thật để ra số của bạn.

- `block_time = 2s` → ~1.3M block/tháng. Header blob giả định ~400 byte.
- Chi phí cố định DA ≈ `1.3M × 400 × 0.0000001 TIA` ≈ **~52 TIA/tháng** chỉ để giữ chain sống dù 0 tx.
- Bật `lazy_mode=true` với `lazy_block_interval=60s`: khi không có tx chỉ ra ~43k block/tháng → chi phí cố định ≈ **~1.7 TIA/tháng** (giảm ~30×).

→ `LazyMode` là đòn bẩy mạnh nhất để rollup luôn nằm ở vùng "rẻ hơn".

---

## 5. Đòn bẩy giảm phí (đều có sẵn trong stack)

| Lever | Cờ / Env | Tác dụng |
|-------|----------|----------|
| Lazy aggregation | `--evnode.node.lazy_mode=true` | Chỉ ra block khi có tx → diệt phần lớn chi phí block rỗng |
| Heartbeat thưa | `--evnode.node.lazy_block_interval=<dur>` | Khoảng tối đa giữa block khi rảnh (vẫn 1 block heartbeat/interval — không về 0 tuyệt đối) |
| Block time dài hơn | `--evnode.node.block_time=<dur>` | Ít block → ít blob cố định |
| Gộp namespace | `DataNamespace` = header namespace (DAConfig) | 1 blob/block thay vì 2 |
| Batch lớn | `MaxBytes` (`DefaultMaxBlobSize`) | Amortize DA_cost trên nhiều tx hơn |
| Tinh chỉnh giá | `COSMOS_EXEC_TIA_PER_BYTE`, `COSMOS_EXEC_MIN_GAS_PRICE` | Đặt fee đủ bù Celestia, không hơn |
| So đường rẻ | `/tx/estimate` (`CostBreakdown`) | Xem trước user trả bao nhiêu trước khi enforce |

Lưu ý ràng buộc: `lazy_block_interval` phải **lớn hơn** `block_time`, nếu không config validation fail (`pkg/config/config.go`).

---

## 6. Cách bật fee thật (đầy đủ, đúng với code hiện tại)

> ⚠️ Bật fee ≠ "đổi một dòng". Có **4 việc bắt buộc**. Bỏ sót bất kỳ việc nào → hoặc fee không được enforce, hoặc **mọi tx fail `insufficient funds`** và chain coi như chết.

### Bước 0 — Bật ante handler (điều kiện tiên quyết)

Ante handler hiện **opt-in**. Nếu không set env này thì **không có ante nào chạy** — `DeductFee` không tồn tại, nên dù có sửa `TxFeeChecker` cũng vô nghĩa:

```bash
export COSMOS_EXEC_ENFORCE_SIGNATURES=true   # app.go:295 — bật toàn bộ ante chain
```

Không có bước này, chain bỏ qua cả chữ ký, sequence, gas **và** fee.

### Bước 1 — Đặt giá (để `/tx/estimate` ra số đúng)

```bash
export COSMOS_EXEC_TIA_PER_BYTE=0.0000001   # đủ bù hóa đơn Celestia thực
export COSMOS_EXEC_MIN_GAS_PRICE=0.000001
export COSMOS_EXEC_GAS_DENOM=ustake
```

### Bước 2 — Enforce: **đã implement, bật bằng env**

Không còn phải sửa code. `ante.go` đã có sẵn `txFeeChecker(feePolicyFromEnv())` cắm vào `NewDeductFeeDecorator`. Hành vi:

- **`COSMOS_EXEC_MIN_GAS_PRICE` không set / ≤ 0** → policy OFF, chấp nhận tx 0-fee (mặc định dev cũ, test không vỡ).
- **set > 0** (bước 1 ở trên) → policy ON. Trong **CheckTx** (lúc nhận vào mempool), tx phải trả `≥ ceil(gas × MIN_GAS_PRICE)` tính bằng `COSMOS_EXEC_GAS_DENOM`, thiếu thì bị từ chối `ErrInsufficientFee`. `DeductFeeDecorator` tự chuyển phí vào module `fee_collector`.

Hai lưu ý quan trọng trong cài đặt:

- Chỉ enforce ở **CheckTx**, **không** re-check ở DeliverTx/replay → full node không bao giờ từ chối một tx mà sequencer đã đưa vào block (tránh config lệch gây fork). Đây là lý do bước 2 dùng chung 1 env với cost.go nhưng không ảnh hưởng tính đồng thuận.
- Vẫn cần **bước 0** (`COSMOS_EXEC_ENFORCE_SIGNATURES=true`) — không bật ante thì `txFeeChecker` không bao giờ chạy, set `MIN_GAS_PRICE` cũng vô nghĩa.

Test: `feePolicy` có unit test ở `app/ante_fee_test.go` (off mặc định, math ceil, giá rác → off).

### Bước 3 — Cấp token cho account (faucet) — **BẮT BUỘC**, xem mục 6b

Chain dùng `DefaultGenesis` (`app.go:321`) → **không có genesis balance, không có cung token nào cả**. Account tạo qua AutoCreate có **số dư = 0**. Ngay khi bước 2 enforce phí > 0, **mọi tx** (kể cả tx đầu của contract) fail:

```
insufficient funds: ... 0ustake < <fee>ustake
```

Nên phải có đường bơm token vào account trước khi nó ký tx tốn phí. Đây **không** phải tùy chọn — không có nó chain không dùng được. Chi tiết ở mục tiếp theo.

---

## 6b. Faucet / cấp vốn — A+B đã được implement

> **Trạng thái:** cách **A (genesis balances)** + **B (faucet endpoint)** đã có sẵn trong code, **opt-in qua env**. Mặc định không set env → giữ nguyên hành vi cũ (zero-balance, không faucet, fee = 0 — đúng thiết kế [auto-account-creation.md](auto-account-creation.md)). Cách **C (airdrop-on-create)** vẫn chỉ là phương án tương lai (xem cuối mục).

### Bật A+B

Set các env này trước khi start `cosmos-exec-grpc`:

```bash
export COSMOS_EXEC_TREASURY_PRIVKEY_HEX=<32-byte hex secp256k1>   # BẮT BUỘC — bật tính năng
export COSMOS_EXEC_TREASURY_AMOUNT=1000000000000ustake           # A: số dư genesis của treasury (default)
export COSMOS_EXEC_FAUCET_AMOUNT=1000000ustake                   # B: số phát mỗi request (default)
export COSMOS_EXEC_FAUCET_GAS=200000                             # gas limit cho tx faucet (default)
export COSMOS_EXEC_FAUCET_COOLDOWN_SECONDS=3600                  # cooldown mỗi địa chỉ (default 1h)
```

Chỉ cần `COSMOS_EXEC_TREASURY_PRIVKEY_HEX`. Không set → tính năng tắt hoàn toàn, không có route `/faucet`.

### Cơ chế

| Phần | Việc nó làm | Code |
|------|-------------|------|
| **A — genesis** | Lúc start, derive địa chỉ treasury từ privkey, vá `bank.balances` thêm `COSMOS_EXEC_TREASURY_AMOUNT` cho treasury. **Supply để trống** → bank `InitGenesis` tự tính lại (né invariant "supply = tổng balances"). | `app.GenesisWithBalances` ([app/genesis.go](../../../app/genesis.go)), `executor.WithGenesis` |
| **B — endpoint** | `POST/GET /faucet?addr=<bech32>` → ký `MsgSend{treasury→addr, COSMOS_EXEC_FAUCET_AMOUNT}` bằng treasury key → đẩy qua đường tx bình thường (`InjectTx`). | `cmd/cosmos-exec-grpc/faucet.go`, `cosmoswasm.BuildSignedBankSend` |

Đặc tính đã xử lý sẵn trong code:

- **Sequence của treasury được serialize** dưới mutex + theo dõi `nextSeq` cục bộ — vì tx trong mempool chưa phản ánh vào state, hai request liên tiếp không bị tái dùng sequence (lỗi sig khi đã bật ENFORCE_SIGNATURES).
- **Cooldown mỗi địa chỉ** (`lastSeen` map) chống spam cơ bản; từ chối `429` kèm thời gian retry.
- **Tự tạo account người nhận**: `MsgSend` tới địa chỉ mới khiến bank tạo account đó → sau tx faucet, địa chỉ vừa tồn tại vừa có vốn, tx kế tiếp của user trả phí được ngay. Khôi phục trải nghiệm "tx đầu chạy được" mà không cần dựa AutoCreate.
- Từ chối tự fund treasury, validate bech32, chỉ nhận GET/POST.

Lưu ý còn lại của vận hành:

- `COSMOS_EXEC_TREASURY_AMOUNT`/`FAUCET_AMOUNT` denom phải **khớp** `COSMOS_EXEC_GAS_DENOM` (mục 6 bước 1), mặc định cùng là `ustake`.
- Genesis chỉ apply **lần đầu**. Bật A sau khi chain đã có data → phải clean start (`--clean-on-start=true`), nếu không state cũ (treasury = 0) được load lại. Đây đúng cơ chế clean-on-start đã nói ở đầu loạt câu hỏi.
- **Bảo mật**: `COSMOS_EXEC_TREASURY_PRIVKEY_HEX` là hot key giữ toàn bộ cung token. Khuyến nghị pattern sub-treasury: genesis cấp phần lớn cho ví cold (không nạp vào server), chỉ đưa server một ví faucet hạn mức nhỏ; hết thì nạp tay. Key để qua env/KMS, không hardcode/commit.

### C — Airdrop-on-create (chưa implement, phương án tương lai)

Cấp token ngay trong `AutoCreateAccountDecorator` (`ante.go`) cho mọi account mới → giữ onboarding zero-step. Đánh đổi: mỗi địa chỉ mới = token free → **bắt buộc** chống Sybil (PoW nhẹ, captcha tầng trên, hoặc airdrop nhỏ tới mức spam không lời). Chỉ làm khi thực sự cần và đã có lớp chống Sybil.

Khuyến nghị: **A (treasury) + B (faucet phát lẻ)** — đã sẵn sàng, ít rủi ro nhất; chỉ thêm C nếu bắt buộc giữ onboarding zero-step.

---

## 6c. Tích hợp Web/UI (nút "Get test tokens")

Faucet đã được nối vào explorer web (`my-dapp-web`) — người dùng không cần `curl`:

| Tầng | File | Việc |
|------|------|------|
| API client | `src/lib/backend.ts` | `api.requestFaucet(addr)` → `GET /api/faucet?addr=…`, type `FaucetResponse` |
| Component | `src/components/FaucetButton.tsx` | Ô nhập + nút; tự điền địa chỉ ví Keplr đang connect (`useWallet()`); hiện `amount` + `tx_hash` (link `/explorer/tx/{hash}`) |
| Trang | `src/app/explorer/page.tsx` | `<FaucetButton/>` đặt đầu trang explorer |

Đường đi request: browser → `/api/faucet?addr=…` → Next.js rewrite (`next.config.mjs`: `/api/:path*` → `BACKEND_URL`) → `cosmos-exec-grpc` route `/faucet`. Không cần CORS, không gọi thẳng backend.

UI tự xử lý trạng thái backend (không cần ẩn/hiện thủ công):

- Backend **chưa bật** faucet (`COSMOS_EXEC_TREASURY_PRIVKEY_HEX` không set) → route `/faucet` không tồn tại → 404 → UI hiện "Faucet is disabled on this backend".
- Đang **cooldown** → backend trả `429 {"error":"… retry in …"}` → UI hiện nguyên message kèm thời gian retry.
- Thành công → hiện số đã gửi + `tx_hash` click sang trang chi tiết tx.

Luồng end-to-end cho dApp:

```
1. User bấm "Connect Keplr" (header) → có địa chỉ, số dư 0
2. Vào /explorer → ô "Get test tokens" tự điền địa chỉ đó
3. Bấm → /api/faucet → treasury ký MsgSend → tx vào mempool
4. UI hiện tx_hash; user theo link xem /explorer/tx/{hash} tới khi success
5. Ví có ustake → deploy/execute contract, tự trả phí
```

Test nhanh:

```bash
# backend
COSMOS_EXEC_TREASURY_PRIVKEY_HEX=<hex> cosmos-exec-grpc ...   # + clean start nếu chain đã có data
# web
cd my-dapp-web && npm run dev   # mở /explorer, connect Keplr, bấm nút
```

---

<a id="sweep-phi-cuoi-block"></a>
## 6d. Sweep phí cuối block về treasury (thay cho `x/distribution`)

> **Trạng thái:** đã implement, **opt-in qua env**. Không set → no-op, phí ở lại `fee_collector` (đúng mặc định dev).

Trong app-chain Cosmos truyền thống, phí trong `fee_collector` được module `x/distribution` chia cho validator set theo voting power. Sovereign rollup này **không có validator set**, nên `x/distribution` (cùng `x/staking` mà nó phụ thuộc) cũng không được nạp. Hệ quả là `fee_collector` phình mãi nếu không có cơ chế khác.

Giải pháp gọn nhẹ trong code hiện tại: `App.EndBlocker` ([`app/app.go:365-399`](../../../app/app.go#L365-L399)) gọi `sweepFeesToTreasury` *sau* `ModuleManager.EndBlock`. Hàm này quét **toàn bộ** balance của `fee_collector` về địa chỉ treasury cấu hình tĩnh qua env.

### Bật

```bash
export COSMOS_EXEC_TREASURY_ADDR=cosmos1...   # bech32 — không set / sai format → no-op
```

Treasury có thể là EOA, multisig, hay địa chỉ contract CosmWasm — chain không quan tâm, chỉ gọi `BankKeeper.SendCoinsFromModuleToAccount`.

### Cơ chế

| Bước | Hành động |
|------|-----------|
| 1 | `ModuleManager.EndBlock` chạy xong (mọi module có thể mint thêm vào `fee_collector`) |
| 2 | `treasuryAddrFromEnv()` parse `COSMOS_EXEC_TREASURY_ADDR`; rỗng/sai → return nil → no-op |
| 3 | `BankKeeper.GetAllBalances(feeCollector)` → balance toàn bộ trong block |
| 4 | `BankKeeper.SendCoinsFromModuleToAccount(FeeCollectorName, treasury, balance)` |
| 5 | Trả về `EndBlock` response cho ABCI |

### Lưu ý vận hành

- **Đồng bộ giữa các node:** `COSMOS_EXEC_TREASURY_ADDR` là đầu vào của state transition — sequencer và full node phải set **cùng giá trị**, lệch sẽ làm `app_hash` divergent và rollup fork. Cùng ràng buộc với `COSMOS_EXEC_MIN_GAS_PRICE` / `COSMOS_EXEC_GAS_DENOM` ([`ante.go:45-67`](../../../app/ante.go#L45-L67)).
- **Khác với `COSMOS_EXEC_TREASURY_PRIVKEY_HEX`:** `_ADDR` chỉ là *đích* nhận phí (public bech32, không cần private key). `_PRIVKEY_HEX` là ví ký faucet (mục 6b). Hai biến độc lập — có thể bật một, cả hai, hoặc không bật cái nào.
- **Mô hình treasury-DAO:** vì treasury chỉ cần là một địa chỉ, có thể trỏ về một contract CosmWasm chứa multisig / time-lock / governance logic mà không phải sửa chain.
- **Không atomic với fee deduction:** phí được `DeductFeeDecorator` đẩy vào `fee_collector` *trong* khi tx chạy; sweep xảy ra ở `EndBlocker` *sau* khi mọi tx của block đã chạy. Trong cùng một block, treasury vẫn nhận đủ tổng phí.

### Vì sao không dùng `x/distribution`?

`x/distribution` đòi `x/staking` (đọc validator set, voting power, delegations) → kéo theo hàng nghìn dòng state schema, query/msg handlers, genesis exporter — không ý nghĩa cho rollup single-sequencer. Pattern sweep chỉ tốn ~30 dòng Go (kèm cả comment + validate), và cho phép treasury được kiểm soát bởi cơ chế tuỳ ý (multisig, contract) thay vì validator set.

---

## 7. Checklist khi đưa lên production có fee

- [ ] **Bước 0**: set `COSMOS_EXEC_ENFORCE_SIGNATURES=true` (không có thì ante không chạy → fee không enforce được).
- [ ] **Bước 1**: set `COSMOS_EXEC_TIA_PER_BYTE` / `MIN_GAS_PRICE` / `GAS_DENOM`; xác nhận `/tx/estimate` ra số đúng.
- [ ] **Bước 2**: đã implement — chỉ cần `COSMOS_EXEC_MIN_GAS_PRICE` > 0 (bước 1); test tx trả thiếu bị từ chối `ErrInsufficientFee` trong CheckTx.
- [ ] **Bước 3 (faucet)**: set `COSMOS_EXEC_TREASURY_PRIVKEY_HEX` (+ tùy chỉnh amount/cooldown) để bật A+B (mục 6b); test `GET /faucet?addr=...` rồi xác nhận địa chỉ có số dư trả phí được.
- [ ] **Sweep phí (mục 6d)**: set `COSMOS_EXEC_TREASURY_ADDR` để phí không tích luỹ vô hạn trong `fee_collector`. Đảm bảo **mọi node** (sequencer + full node) set cùng giá trị, nếu không sẽ fork.
- [ ] Denom `TREASURY_AMOUNT`/`FAUCET_AMOUNT` khớp `GAS_DENOM`.
- [ ] Treasury key qua env/KMS; cân nhắc pattern sub-treasury (hot key hạn mức nhỏ).
- [ ] Đo blob size **thật** (header + data) trên tải dự kiến — đừng đoán.
- [ ] Lấy giá TIA + Celestia gas price hiện tại, tính `chi_phí_DA/tháng` ở mục 3; đặt `TIA_PER_BYTE` sao cho fee thu ≥ hóa đơn Celestia (cộng biên an toàn).
- [ ] Bật `lazy_mode` trừ khi app cần block đều (vd timestamp/oracle).
- [ ] Theo dõi tỉ lệ `fee thu / chi Celestia` qua thời gian (giá TIA biến động → có thể lỗ nếu không retune).

---

## 8. Kết luận

- **Throughput thực tế + LazyMode**: tổng chi phí (đã gồm hóa đơn Celestia của operator) **thấp hơn đáng kể** so với tự dựng Cosmos chain thường, vì bạn xóa hẳn "thuế bảo mật" dạng lạm phát + N× hạ tầng validator, thay bằng DA dùng chung.
- **Throughput rất thấp, block đều**: có thể **đắt hơn** — vì trả Celestia cho block gần rỗng. Khắc phục bằng LazyMode / block time dài.
- So với **deploy lên chain chia sẻ có sẵn** (không tự dựng): rollup chỉ thắng khi bạn cần chủ quyền / custom logic / throughput riêng; nếu không, chain chia sẻ có thể rẻ hơn vì bạn không nuôi hạ tầng nào.
- Luôn nhớ: chi phí rollup **hiện minh bạch** (hóa đơn TIA), chi phí chain thường **ẩn trong lạm phát** — so sánh công bằng phải tính cả phần ẩn đó.

---

## Tham chiếu code

| Thành phần | File |
|------------|------|
| Cost model (DA + gas) | `apps/cosmos-exec/cmd/cosmos-exec-grpc/cost.go` |
| Endpoint estimate | `cmd/cosmos-exec-grpc/main.go` → `txEstimateHandler` (`/tx/estimate`) |
| Ante opt-in switch | `apps/cosmos-exec/app/app.go:295` (`COSMOS_EXEC_ENFORCE_SIGNATURES`) |
| Ante: fee checker (env-gated) + AutoCreate | `apps/cosmos-exec/app/ante.go` (`feePolicyFromEnv`, `txFeeChecker`) |
| Genesis mặc định (không balance) | `apps/cosmos-exec/app/app.go:321` (`DefaultGenesis`) |
| A — genesis balances (treasury) | `apps/cosmos-exec/app/genesis.go` (`GenesisWithBalances`), `executor.WithGenesis` |
| B — faucet endpoint + env | `apps/cosmos-exec/cmd/cosmos-exec-grpc/faucet.go` (`/faucet`) |
| Ký MsgSend cho faucet | `apps/cosmos-exec/sdk/cosmoswasm/faucet_tx.go` (`BuildSignedBankSend`) |
| Sweep phí cuối block về treasury | `apps/cosmos-exec/app/app.go` (`sweepFeesToTreasury`), `apps/cosmos-exec/app/ante.go` (`treasuryAddrFromEnv`) |
| Lazy / block time / based flags | `pkg/config/config.go`, `pkg/config/defaults.go` |
| DA submit (blob lên Celestia) | `block/internal/submitting/submitter.go` |
| Sequencer & mô hình bảo mật | [sequencer-security.md](sequencer-security.md) |
