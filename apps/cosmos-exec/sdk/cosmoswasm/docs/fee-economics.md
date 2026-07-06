# Kinh tế phí: rollup có fee vs Cosmos chain thường

Tài liệu này trả lời chi tiết câu hỏi: **nếu bật fee thật (bỏ chế độ 0-fee), tổng chi phí có còn rẻ hơn một Cosmos chain thường không — tính cả phần operator/sequencer phải trả cho Celestia?**

Câu trả lời ngắn: **ở throughput thực tế thì rẻ hơn rõ rệt; ở throughput rất thấp thì có thể đắt hơn.** Phải so **tổng chi phí kinh tế**, không so "user fee với user fee".

> Liên quan: [sequencer-security.md](sequencer-security.md) (mô hình không validator), [cosmos-vs-evnode.md](cosmos-vs-evnode.md), [configuration.md](configuration.md).

Celestia sử dụng mempool ưu tiên theo giá gas tiêu chuẩn. Điều này có nghĩa là các giao dịch có phí cao hơn sẽ được các trình xác thực ưu tiên. Phí bao gồm một khoản phí cố định cho mỗi giao dịch và sau đó là một khoản phí thay đổi dựa trên kích thước của mỗi blob trong giao dịch.

## Mục lục

- [1. Mô hình chi phí thật của rollup](#muc-1)
- [1b. Hàm ước lượng phí: `/tx/estimate` vs `/tx/simulate`](#muc-1b)
- [1c. Hóa đơn Celestia THẬT được tính thế nào (API · minfee · số đo)](#muc-1c)
- [1d. Tính fee thực thi đầu-cuối: gas_limit, gas_price → fee (ví dụ số)](#muc-1d)
- [2. Ai trả gì](#muc-2)
- [3. Tổng chi phí vận hành rollup / tháng](#muc-3)
- [4. Điểm hòa vốn](#muc-4)
- [5. Đòn bẩy giảm phí](#muc-5)
- [6. Cách bật fee thật](#muc-6)
- [6b. Faucet / cấp vốn (A+B đã implement)](#muc-6b)
- [6c. Tích hợp Web/UI (nút "Get test tokens")](#muc-6c)
- [6d. Sweep phí cuối block về treasury](#sweep-phi-cuoi-block)
- [6e. AutoCreateAccount — tự tạo tài khoản khi ký lần đầu](#muc-6e)
- [7. Checklist production có fee](#muc-7)
- [8. Kết luận](#muc-8)
- [Tham chiếu code](#tham-chieu-code)

---

<a id="muc-1"></a>
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
| `COSMOS_EXEC_TIA_PER_BYTE` | `0.000000214` | Giá DA mỗi byte (TIA) — **rate hiệu dụng đo thực** (run 0,004) **scale lên minfee hiện hành 0,005** (xem Mục 1c) |
| `COSMOS_EXEC_MIN_GAS_PRICE` | `0.000001` | Giá gas thực thi |
| `COSMOS_EXEC_DA_DENOM` | `TIA` | Denom phần DA |
| `COSMOS_EXEC_GAS_DENOM` | `ustake` | Denom phần gas |

> **Lưu ý quan trọng — đây là MÔ HÌNH per-tx, không phải hoá đơn Celestia thật.**
> `cost.go` chỉ tính `bytes × TIA_PER_BYTE` để dashboard ước lượng "phần DA gán cho
> một tx". Hoá đơn TIA *thật* mà operator trả Celestia tính theo **gas/share** (Mục
> 1c) và **không tuyến tính theo byte**. Mặc định `TIA_PER_BYTE = 2,14·10⁻⁷` ở đây
> = **rate hiệu dụng đo thật** (tổng phí PFB / tổng byte blob qua 69 lần submit ở
> minfee 0,004 = 1,71·10⁻⁷) **scale lên minfee hiện hành 0,005** (×0,005/0,004),
> thay cho hằng số bịa `1·10⁻⁷` cũ.

Hiện chain chạy **ante 0-fee** (`NewPermissionlessAnteHandler`, `apps/cosmos-exec/app/ante.go`) nên con số này là **mô phỏng** — gọi `/tx/estimate` (`CostBreakdown`) để xem "production economics sẽ trông thế nào" mà không drain ví test. Bật fee thật = thay TxFeeChecker (mục 6).

**Điểm mấu chốt:** `DA_cost` không biến mất khi bật fee — nó luôn tồn tại, chỉ là hiện đang do **operator gánh ngầm** (operator trả Celestia bằng TIA khi publish blob). Bật fee chỉ là **chuyển khoản DA_cost đó sang user**.

---

<a id="muc-1b"></a>
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

<a id="muc-1c"></a>
## 1c. Hóa đơn Celestia THẬT được tính thế nào (API + công thức + số đo)

Mục 1 dùng `DA_cost = bytes × TIA_PER_BYTE` — đó là **mô hình tuyến tính per-tx do
operator chỉnh**, tiện cho dashboard. Nhưng hóa đơn TIA operator thật sự trả cho
Celestia **không tuyến tính theo byte**: Celestia tính **gas** theo *share*
(khối 512 byte) cộng **phí cố định mỗi PFB**, rồi `fee = gas × gas_price`. Mục này
nói rõ: gọi API gì, ai quyết gas price (liên quan **minfee**), công thức ra sao, và
**số đo thật** trên chuỗi của stack này.

### A. Gọi API nào — và gas price thực sự đến từ đâu

ev-node publish blob qua **celestia-node JSON-RPC**, method `blob.Submit`:

```
blob.Submit(blobs []*Blob, opts *SubmitOptions) → height
```

| Tầng | Code | Việc |
|------|------|------|
| ev-node submitter | [`block/internal/submitting/da_submitter.go:617`](../../../../../block/internal/submitting/da_submitter.go#L617) | `client.Submit(ctx, data, -1, namespace, options)` — `options` = `config.DA.SubmitOptions` (JSON) + signer |
| ev-node DA client | [`block/internal/da/client.go`](../../../../../block/internal/da/client.go) `Submit(... _ float64, ...)` | **bỏ qua tham số gasPrice `-1`**; parse `options` → `SubmitOptions`; đóng gói data thành `Blob` (share version 0), gọi `blobAPI.Submit` |
| Celestia node | (process ngoài) | bọc blob vào tx **`MsgPayForBlobs` (PFB)**, ước lượng gas, ký, broadcast tới celestia-app validator |

`SubmitOptions` (mirror `state.TxConfig`, [`submit_options.go`](../../../../../pkg/da/jsonrpc/submit_options.go)) điều khiển giá:

```go
type SubmitOptions struct {
    GasPrice      float64 // utia/gas; chỉ áp dụng khi IsGasPriceSet=true
    IsGasPriceSet bool
    MaxGasPrice   float64 // trần khi để node tự định giá
    Gas           uint64  // gas_limit; 0 = để node tự ước lượng
    ...
}
```

> **⚙️ Cấu hình ĐANG dùng trong stack này (đúng với code + .env hiện tại):**
> - `--evnode.da.submit_options` **không được set** ([run-cosmos-wasm-nodes.go](../../../../../scripts/run-cosmos-wasm-nodes.go) chỉ truyền `--evnode.da.address/auth_token/namespace`), nên `SubmitOptions` là **rỗng** → `IsGasPriceSet=false`, `Gas=0`.
> - ⇒ **celestia-node tự ước lượng cả gas lẫn gas price.** ev-node KHÔNG cố định giá.
> - **`DA_GAS_PRICE` trong `.env` KHÔNG liên quan đến đường này.** Nó chỉ được đọc bởi đường submit RIÊNG của cosmos-exec SDK ([`sdk/cosmoswasm/blob.go`](../blob.go)) dùng cho engram/telemetry — không phải header/data blob của rollup. Muốn cố định giá cho rollup phải set `--evnode.da.submit_options='{"gas_price":..,"is_gas_price_set":true}'`.

### B. minfee — sàn gas price do Celestia áp

Vì stack để node tự định giá, gas price thực bị chặn dưới bởi **minfee** của Celestia:

- **Module `x/minfee` (celestia-app)** giữ param `NetworkMinGasPrice` — **sàn gas
  price toàn mạng**, áp cho MỌI tx (kể cả PFB). Đây là tham số governance, đồng nhất
  giữa các validator để không ai nhận tx dưới sàn.
- **`minimum-gas-prices` cục bộ của từng node** (app.toml) — sàn riêng mỗi validator,
  có thể cao hơn sàn mạng.
- celestia-node khi `IsGasPriceSet=false` sẽ chọn một gas price **≥ max(sàn mạng,
  sàn node)**, chặn trên bởi `MaxGasPrice` (nếu set).

⇒ Trên private net này, gas price node áp **= 0,004 utia/gas tại run đo 593k** (đo,
xem mục C); **run hiện hành (611k+) đã = 0,005** — minfee mạng dịch theo thời gian.
Đó là sàn minfee/giá node của mạng private — không phải `DA_GAS_PRICE` hay `COSMOS_EXEC_*`. Muốn xác nhận chính xác param: query module minfee của celestia-app
(`celestia-appd query minfee params` hoặc gRPC `celestia.minfee.v1.Query/NetworkMinGasPrice`).

### C. Công thức: gas theo share, fee = gas × giá

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

```
shares(size) = 1 + ceil( max(0, size − 478) / 482 )      // blob ≤ 478B vẫn = 1 share
gas_data     = Σ_blob ( shares(size) × 512 × 8 )          // GasPerBlobByte = 8
gas_total    = gas_data + PFBGasFixedCost (+ tx-size cost nhỏ)
fee_utia     = ceil( gas_total × gas_price )              // gas_price [utia/gas]; 1 TIA = 1e6 utia
```

### D. SỐ ĐO THẬT trên chuỗi (đo bằng `scripts/measure_da_fees.mjs`)

Decode `fee.amount` + `blob_sizes` (field 3 của PFB) qua CometBFT RPC, **N = 69 PFB**:

| Đại lượng | Giá trị đo |
|---|---|
| Gas price node áp | **0,004 utia/gas** — `fee = ceil(gas_limit × 0,004)` khớp 100% mẫu |
| Phí cố định mỗi PFB | **~301 utia** (hồi quy `fee = a + b·bytes`) |
| Chi phí biên mỗi byte | **~0,05 utia/byte = 5,0·10⁻⁸ TIA/byte** (≈ `8,5 gas/byte × 0,004 / 1e6`) |
| Rate hiệu dụng (tổng fee / tổng byte) | **0,171 utia/byte = 1,71·10⁻⁷ TIA/byte** |
| Phí mỗi lần submit (TB) | ~427 utia ≈ 0,00043 TIA (dải 320–492) |

Con số biên `5,0·10⁻⁸` khớp công thức lý thuyết ở mục C: `512×8/482 × 0,004 / 1e6
≈ 8,5 × 0,004 / 1e6 ≈ 3,4·10⁻⁸` (cùng bậc; chênh do tx-size cost + làm tròn share).

> **Đối soát hai lần đo (minfee mạng đổi theo thời gian).** Bảng trên là run **N=69
> PFB tại height ~593k**, khi minfee mạng = **0,004 utia/gas**. Một run sau (**height
> ~611–612k**, gồm cả PFB blob-first `makeReplay(300 KiB)` = 13.459 utia tại 612074)
> đo minfee đã lên **0,005 utia/gas** — `fee/gas_limit = 13.459/2.691.748 = 0,005`
> khớp 100%. Cấu trúc phí (share 512B, 8 gas/byte, fixed ~65k gas PFB) **không đổi**;
> chỉ sàn gas_price dịch 0,004 → 0,005. **Báo cáo (Chương 4, Chương 6) và các bảng
> §10 dùng mốc hiện hành 0,005**; các con số 0,004 ở đây là bản ghi lịch sử của run
> 593k, giữ nguyên để truy vết.

### D2. Script `measure_da_fees.mjs` đo bằng cách nào (phương pháp)

> Script sống ở **repo FE `my-dapp-web`**: `scripts/measure_da_fees.mjs`. Nó không
> phụ thuộc ev-node — chỉ cần một CometBFT RPC của celestia-app. Chạy:
> ```bash
> node scripts/measure_da_fees.mjs <RPC> <startHeight> <endHeight>
> node scripts/measure_da_fees.mjs http://131.153.224.169:26757 592829 593129
> ```

**Vì sao phải quét on-chain.** API DA `Submit` chỉ trả `Height` + `BlobSize`, **không**
trả phí. Phí thật là `fee.amount` (denom `utia`) của giao dịch **`MsgPayForBlobs` (PFB)**
mà celestia-app ghi lên chuỗi. Nên cách duy nhất lấy số đúng là **quét block DA, tìm PFB,
decode fee**. Trên mạng DA private này rollup là bên **duy nhất** submit blob → mọi PFB tìm
thấy đều là submission của rollup.

**Luồng tổng quát** (`scanHeight` chạy song song `CONCURRENCY=12` height):

```
mỗi height:  GET /block?height=H            → block.data.txs[] (base64)
             GET /block_results?height=H    → gas_used[] (optional)
  mỗi tx:    unwrapTx(bytes)                 → gỡ vỏ Celestia
             TxRaw.decode → TxBody + AuthInfo
             lọc message typeUrl == /celestia.blob.v1.MsgPayForBlobs
             fee   = Σ AuthInfo.fee.amount[denom=utia]
             bytes = Σ MsgPayForBlobs.blob_sizes   (field 3)
             hash  = SHA256(outer bytes) hoa   (= tx hash CometBFT)
```

**Bước then chốt 1 — gỡ lớp bọc Celestia.** Tx blob trên celestia-app **không** phải
`TxRaw` cosmos trần: nó bị bọc trong `BlobTx` (khi broadcast) hoặc `IndexWrapper` (khi
đã vào block), nhận diện bằng một `type_id` = chuỗi `"BLOB"`/`"INDX"` nằm ở **field 3**;
tx cosmos thật nằm ở **field 1**. cosmjs-types không biết hai vỏ này, nên script **tự đọc
protobuf thô** để bóc:

```js
// Peel Celestia's IndexWrapper{tx=1,...,type_id=3="INDX"} / BlobTx(type_id="BLOB").
function unwrapTx(bytes) {
  const typeId = getLenField(bytes, 3);          // đọc field 3
  if (typeId) {
    const id = Buffer.from(typeId).toString("utf8");
    if (id === "INDX" || id === "BLOB") {
      const inner = getLenField(bytes, 1);        // field 1 = tx cosmos bên trong
      if (inner) return inner;
    }
  }
  return bytes;                                    // không phải vỏ → trả nguyên
}
```

`getLenField` là bộ đọc protobuf tối giản: duyệt từng field theo cặp `(tag, wire type)`,
với wire type 2 (length-delimited) thì đọc độ dài rồi cắt đúng field cần; các wire type
khác (varint/64-bit/32-bit) thì skip qua. Nhờ vậy không cần schema của `BlobTx`.

**Bước then chốt 2 — decode fee.** Sau khi có tx cosmos, dùng cosmjs-types decode chuẩn
rồi cộng các coin `utia` trong `fee.amount`:

```js
const raw  = TxRaw.decode(inner);
const body = TxBody.decode(raw.bodyBytes);
const auth = AuthInfo.decode(raw.authInfoBytes);
if (body.messages.every(m => m.typeUrl !== PFB)) return;   // không phải PFB → bỏ
let utia = 0n;
for (const c of auth.fee?.amount ?? []) if (c.denom === "utia") utia += BigInt(c.amount);
```

**Bước then chốt 3 — bytes blob CHÍNH XÁC.** Kích thước blob thật = tổng
`MsgPayForBlobs.blob_sizes` (repeated `uint32`, **field 3** của message), chứ không phải
kích thước tx. `blob_sizes` có thể mã hoá **packed** hoặc **non-packed**, nên script xử lý
cả hai:

```js
// đọc repeated uint32 field, cả packed lẫn non-packed
let blobBytes = 0;
for (const m of pfbMsgs) for (const s of getRepeatedUint32(m.value, 3)) blobBytes += s;
```

Đây là lý do phải parse proto thô lần nữa: cosmjs-types không có type của
`celestia.blob.v1.MsgPayForBlobs`, ta chỉ cần đúng 1 field nên tự đọc rẻ hơn generate type.

**Thống kê cuối.** Sau khi gom mọi PFB thành các dòng `{height, hash, utia, blobBytes,
gas_limit, gas_used}`, script in bảng per-PFB rồi tính:

- **mean / stdev / min–max** của `fee/PFB`, tổng phí, tổng byte.
- **Rate hiệu dụng** = `Σfee / Σbytes` — gánh cả phí cố định nên **nói quá** với blob lớn.
- **Hồi quy least-squares** `fee ≈ fixed + marginal·bytes` để **tách** phí cố định mỗi PFB
  khỏi chi phí biên mỗi byte — chính là hai con số `~301 utia` (fixed) và `~0,05 utia/byte`
  (marginal) ở bảng [mục D](#d-số-đo-thật-trên-chuỗi-đo-bằng-scriptsmeasure_da_feesmjs):

```js
const marginal = (n*sxy - sx*sum) / (n*sxx - sx*sx); // utia/byte
const fixed    = (sum - marginal*sx) / n;            // utia/PFB
// → 1 MiB: fixed + marginal*1048576  (DÙNG số này cho data lớn, không dùng rate hiệu dụng)
```

> Vì sao cần cả hai rate: blob nhỏ bị phí cố định áp đảo (blob 379B ~0,84 utia/byte) còn
> blob lớn tiệm cận `marginal`. Rate hiệu dụng đúng cho "trung bình tải hiện tại"; hồi quy
> đúng cho "chi phí thêm khi tăng 1 byte". Đây là cơ sở chọn `COSMOS_EXEC_TIA_PER_BYTE`
> ở [mục E](#e-vì-sao-tia_per_byte--bytes-chỉ-là-xấp-xỉ--và-đặt-bao-nhiêu) ngay dưới.

### E. Vì sao `TIA_PER_BYTE × bytes` chỉ là xấp xỉ — và đặt bao nhiêu

- **Phí cố định áp đảo blob nhỏ:** đo được fixed ~301 utia → blob 379B tốn 320 utia
  (≈0,84 utia/byte) nhưng blob 3411B chỉ ~0,14 utia/byte. Đây là lý do blob-first
  **gom batch** (mục 3): chia phần cố định cho nhiều record.
- **Hai lựa chọn rate cho `COSMOS_EXEC_TIA_PER_BYTE`:**
  - **Hiệu dụng `2,14·10⁻⁷`** (default hiện tại) = rate đo thật ở minfee 0,004
    (`1,71·10⁻⁷`) scale lên minfee hiện hành 0,005: tổng phí / tổng byte, đảm bảo
    `Σ(bytes × rate)` ≈ tổng hoá đơn — hợp lý khi muốn fee thu bù đúng chi phí.
  - **Biên `6,3·10⁻⁸`** (≈ `5,0·10⁻⁸` ở 0,004 scale lên 0,005): chỉ phần phí tăng
    thêm mỗi byte, KHÔNG gánh phần cố định — dùng nếu không muốn "nói quá" với tx nhỏ.
- Default đặt `2,14·10⁻⁷` (rate hiệu dụng đo thật ở 0,004 scale lên minfee 0,005)
  thay cho hằng `1·10⁻⁷` bịa trước đây.

> Lưu ý phiên bản: các hằng (`GasPerBlobByte=8`, `PFBGasFixedCost`, `ShareSize=512`)
> là default celestia-app (mirror tại celestia-node v0.28.4 trong
> [`pkg/da/jsonrpc`](../../../../../pkg/da/jsonrpc)); `gas_price`/minfee đổi theo mạng
> và upgrade. Số đo ở mục D là của **mạng Celestia private** (`chain_id=private`)
> đang chạy — đo lại bằng `measure_da_fees.mjs` khi đổi mạng.

---

<a id="muc-1d"></a>
## 1d. Tính fee thực thi đầu-cuối: `gas_limit`, `gas_price` → `fee` (ví dụ số)

Mục 1 cho công thức tổng `total_cost = DA_cost + gas_cost`. Mục này đi sâu vào **phần
`gas_cost`** — phí thực thi mà ante thực sự trừ khỏi ví — trả lời ba câu: chain lấy
`gas_limit` ở đâu, lấy `gas_price` ở đâu, và từ đó ra `fee` thế nào. Đây là phần
phí trả bằng **token nội bộ** (`ustake`), tách hẳn khỏi hoá đơn DA bằng TIA (§1c).

### A. Ba hằng số đầu vào (env, đọc lúc khởi động)

| Env | Default | Vai trò trong công thức |
|-----|---------|--------------------------|
| `COSMOS_EXEC_MIN_GAS_PRICE` | `0.000001` | **giá gas** `p` [ustake/gas] — ante enforce, cũng là giá `/tx/simulate` dùng |
| `COSMOS_EXEC_GAS_ADJUSTMENT` | `1.3` | hệ số đệm `gas_used → gas_limit` (permille 1300) |
| `COSMOS_EXEC_GAS_DENOM` | `ustake` | denom của fee |

### B. `gas_limit` đến từ đâu — ba nguồn

1. **Đo bằng simulate (khuyến nghị, ví/UI dùng).** `/tx/simulate` chạy tx qua
   ante + handler nhưng **không commit** → trả `gas_used` THẬT. Rồi đệm lên:

   ```
   gas_limit = ⌈ gas_used × GAS_ADJUSTMENT ⌉
             = (gas_used × 1300 + 999) / 1000      // số nguyên, +999 = làm tròn LÊN
   ```

   Nhân 1,3 vì state có thể đổi giữa lúc simulate và lúc thực thi → tránh
   out-of-gas. Đây là `gas_wanted` mà tx mang theo.

2. **Đặt tay (`GAS_LIMIT` env, vd trong example `my-counter`).** Một trần cố định
   lớn — example dùng `80 000 000`. Không cần simulate nhưng phí trả theo trần này
   (cận trên an toàn), không theo gas thật.

3. **Mức block (sequencer).** `GetExecutionInfo` trả `MaxGas = 0` → ev-node
   **không** giới hạn tổng gas mỗi block; giới hạn chỉ ở **per-tx** qua `gas_limit`
   ở trên. (Khác ev-abci vốn đọc `MaxGas` từ consensus params — xem
   [cosmos-vs-evabci.md](cosmos-vs-evabci.md) §4.)

### C. Công thức `fee`

`gas_price` lấy từ `MIN_GAS_PRICE`. Vì `feeForGas` (lúc ký) dùng **đúng** giá mà
ante kiểm tra (CheckTx), nên **không lệch** giữa ước lượng và thực thu:

```
fee_amount = ⌈ gas_price × gas_limit ⌉           // làm tròn LÊN, ra SỐ NGUYÊN
fee        = { fee_amount  GAS_DENOM }            // vd "6ustake"
```

> **Hai điểm dễ sai:**
> - `ustake` là **đơn vị nguyên nhỏ nhất** (micro). Phí < 1 ustake **làm tròn lên
>   thành 1 ustake** — không có "0,12 ustake" trong số dư thật.
> - Phí thực thu tính trên **`gas_limit`** (đã đệm ×1,3), không phải `gas_used`.
>   `gas_used × p` chỉ là *phí lý thuyết* (cận dưới mịn) tiện để so tương đối; số
>   bị trừ khỏi ví là `⌈gas_limit × p⌉`.

### D. Ví dụ số (giá mặc định `p = 10⁻⁶ ustake/gas`, đệm 1,3)

Dùng `gas_used` thật đo ở Chương 4 báo cáo:

| Thao tác | `gas_used` (đo) | `gas_limit = ⌈gas_used×1,3⌉` | Phí lý thuyết `gas_used×p` | **Phí THỰC THU `⌈gas_limit×p⌉`** |
|----------|-----------------|------------------------------|----------------------------|-----------------------------------|
| MsgSend (faucet) | 80 767 | 104 998 | 0,0808 ustake | **1 ustake** |
| Store code (cw20 317 KB) | 4 181 800 | 5 436 340 | 4,1818 ustake | **6 ustake** |
| Instantiate | 176 400 | 229 320 | 0,1764 ustake | **1 ustake** |
| Execute (cw20 transfer) | 125 849 | 163 604 | 0,1258 ustake | **1 ustake** |

Kiểm tra một dòng (Execute): `⌈125 849 × 1,3⌉ = 163 604`; `⌈163 604 × 10⁻⁶⌉ =
⌈0,163604⌉ = 1 ustake`. Triển khai trọn một hợp đồng (store + instantiate +
execute) tốn **8 ustake thực thu** — không đáng kể, đúng kết luận "phí gas
negligible" của báo cáo.

**Đường đặt-tay (example).** `GAS_LIMIT = 80 000 000`, `p = 10⁻⁶` →
`fee = ⌈80⌉ = 80 ustake` cho **mọi** tx (cận trên an toàn vì không simulate).
Faucet dùng `FAUCET_GAS = 200 000` → `fee = ⌈0,2⌉ = 1 ustake`.

### E. Tổng chi phí một tx (gộp cả DA)

```
total_cost(tx) = ⌈gas_limit × MIN_GAS_PRICE⌉ ustake     ← phần này (thực thi)
               + DA_share(bytes)              TIA        ← §1 (mô hình) / §1c (hoá đơn thật)
```

Hai khoản **khác token** (`ustake` nội bộ vs `TIA` trả Celestia) nên không cộng
trực tiếp — quy về USD bằng giá token tương ứng khi cần (xem
[cac-san-pham-lien-quan.md](cac-san-pham-lien-quan.md) §10.2). Với rollup này
`ustake` không niêm yết nên phần thực thi ≈ \$0; chi phí thực duy nhất là DA.

> **Lấy số của bạn:** `/tx/simulate` (raw tx chưa ký) → `{gas_used, gas_limit, fee}`;
> hoặc `/tx/estimate` (`CostBreakdown`) để xem cả `gas_cost` + `DA_cost` cùng lúc.
> Code: `feeForGas` ([`cmd/cosmos-exec-grpc/faucet.go`](../../../cmd/cosmos-exec-grpc/faucet.go)),
> ante enforce ([`app/ante.go`](../../../app/ante.go) `feePolicyFromEnv`/`txFeeChecker`).

---

<a id="muc-2"></a>
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

<a id="muc-3"></a>
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

<a id="muc-4"></a>
## 4. Điểm hòa vốn (vì sao "không phải lúc nào cũng rẻ hơn")

| Vùng | Ai thắng | Lý do |
|------|----------|-------|
| Throughput cao (batch đầy đặn) | **Rollup thắng đậm** | Loại bỏ chi phí validator-set (lạm phát + N× hạ tầng), thay bằng DA dùng chung rẻ; cố định chia mỏng |
| Throughput trung bình | Rollup thắng nếu bật LazyMode | Bỏ được phần lớn block rỗng |
| Throughput rất thấp, block đều 2s | **Chain thường có thể rẻ hơn** | Rollup vẫn trả Celestia cho hàng loạt block gần rỗng; chi phí cố định/tx phình to |

Điểm hòa vốn phụ thuộc 4 biến: **giá TIA + Celestia gas + block time + tx/block**. Không có con số tuyệt đối — phải đo bằng `/tx/estimate` trên tải thật của bạn.

### Ví dụ minh họa (dùng default constants)

> Số dưới đây là minh họa cách tính, KHÔNG phải benchmark — thay giá TIA thực và đo blob size thật để ra số của bạn.

- `block_time = 2s` → ~1.3M block/tháng. Header blob giả định ~400 byte; rate đo `1.71e-7 TIA/byte`.
- Chi phí cố định DA ≈ `1.3M × 400 × 1.71e-7 TIA` ≈ **~89 TIA/tháng** chỉ để giữ chain sống dù 0 tx.
- Bật `lazy_mode=true` với `lazy_block_interval=60s`: khi không có tx chỉ ra ~43k block/tháng → chi phí cố định ≈ **~2.9 TIA/tháng** (giảm ~30×).

> Lưu ý: con số trên dùng mô hình per-block tuyến tính. Thực tế stack **gom batch**
> (mỗi PFB ~3 block DA, đo ~427 utia/lần — Mục 1c-D), nên chi phí thật/tháng thấp
> hơn nhiều: ~`(30·24·3600 / 16s) × 427 utia` ≈ **~69 TIA/tháng** ở tải đo.

→ `LazyMode` là đòn bẩy mạnh nhất để rollup luôn nằm ở vùng "rẻ hơn".

---

<a id="muc-5"></a>
## 5. Đòn bẩy giảm phí (đều có sẵn trong stack)

| Lever | Cờ / Env | Tác dụng |
|-------|----------|----------|
| Lazy aggregation | `--evnode.node.lazy_mode=true` | Chỉ ra block khi có tx → diệt phần lớn chi phí block rỗng |
| Heartbeat thưa | `--evnode.node.lazy_block_interval=<dur>` | Khoảng tối đa giữa block khi rảnh (vẫn 1 block heartbeat/interval — không về 0 tuyệt đối) |
| Block time dài hơn | `--evnode.node.block_time=<dur>` | Ít block → ít blob cố định |
| Gộp namespace | `DataNamespace` = header namespace (DAConfig) | 1 blob/block thay vì 2 |
| Batch lớn | `MaxBytes` (`DefaultMaxBlobSize`) | Amortize DA_cost trên nhiều tx hơn |
| Tinh chỉnh giá (dashboard model) | `COSMOS_EXEC_TIA_PER_BYTE`, `COSMOS_EXEC_MIN_GAS_PRICE` | Đặt fee mô phỏng đủ bù Celestia, không hơn |
| Cố định DA gas price thật | `--evnode.da.submit_options='{"gas_price":0.004,"is_gas_price_set":true}'` | Khoá giá PFB thay vì để node tự định (mặc định để node tự ước lượng theo minfee) |
| So đường rẻ | `/tx/estimate` (`CostBreakdown`) | Xem trước user trả bao nhiêu trước khi enforce |

Lưu ý ràng buộc: `lazy_block_interval` phải **lớn hơn** `block_time`, nếu không config validation fail (`pkg/config/config.go`).

---

<a id="muc-6"></a>
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
export COSMOS_EXEC_TIA_PER_BYTE=0.000000214  # rate hiệu dụng ĐO THẬT (Mục 1c-D, scale lên minfee 0,005); đủ bù hóa đơn Celestia
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

<a id="muc-6b"></a>
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

<a id="muc-6c"></a>
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

<a id="muc-6e"></a>
## 6e. AutoCreateAccount — tự tạo tài khoản khi ký lần đầu

> **Trạng thái:** là một decorator trong chuỗi ante (`AutoCreateAccountDecorator`, [`app/ante.go:188-248`](../../../app/ante.go#L188-L248)), chạy ngay đầu chuỗi — **trước** các bước kiểm tra pubkey/chữ ký. Chỉ thực sự kích hoạt khi ante chain được bật (`COSMOS_EXEC_ENFORCE_SIGNATURES=true`, Mục 6 Bước 0).

### AutoCreateAccount để làm gì

Nó **tự tạo một `BaseAccount`** cho bất kỳ địa chỉ người ký nào **chưa tồn tại** trong state, ngay tại giao dịch ký đầu tiên. Cơ chế (`AnteHandle`):

1. Lấy danh sách chữ ký của tx (`GetSignaturesV2`), với mỗi chữ ký suy ra địa chỉ từ pubkey (`sdk.AccAddress(sig.PubKey.Address())`).
2. Nếu `!ak.HasAccount(ctx, addr)` → `NewAccountWithAddress` rồi `SetAccount` để ghi account vào state.
3. Gọi tiếp decorator kế (`SetPubKey`, `SigVerification`…) — giờ chúng đã có bản ghi account để đọc/ghi.

Mục đích: **bỏ bước onboarding**. Một khoá hoàn toàn mới có thể gửi tx ký đầu tiên (ví dụ `store code`) mà **không cần nhận token / không cần xin faucet trước**. Chính hành động ký tx đầu tiên là cái "đăng ký" tài khoản. Kết hợp với mặc định 0-fee, người dùng deploy hợp đồng ngay với một ví trắng tinh — đúng tinh thần permissionless.

### Nếu KHÔNG có AutoCreateAccount thì luồng bình thường ra sao

Đây là **hành vi Cosmos SDK chuẩn**: một địa chỉ phải **tồn tại trong state trước** thì mới ký tx được. Account chỉ được tạo khi nó **nhận token lần đầu** (qua genesis, hoặc một `MsgSend` gửi đến nó). Lý do: `SetPubKeyDecorator` và `SigVerificationDecorator` cần một bản ghi account sẵn có để lưu pubkey và đọc/đối chiếu `sequence`.

Hệ quả khi thiếu AutoCreate, với một địa chỉ mới chưa từng nhận token:

```
account <addr> does not exist: unknown address
```

→ tx ký đầu tiên **bị từ chối ngay ở ante**, dù chữ ký hoàn toàn hợp lệ. Đây là vấn đề "con gà — quả trứng": muốn ký tx thì phải có account, mà muốn có account thì phải nhận một khoản chuyển vào trước.

Luồng bình thường (không AutoCreate) buộc phải đi vòng:

```
1. Địa chỉ mới (chưa tồn tại trong state) — chưa ký được gì
2. Một bên KHÁC gửi token vào (faucet / ví khác) qua MsgSend
       → bank tạo BaseAccount cho địa chỉ đó (số account number được cấp)
3. Từ giờ địa chỉ mới ký tx được (sequence bắt đầu từ 0)
```

### Quan hệ với faucet (Mục 6b)

Hai cơ chế giải quyết hai việc khác nhau và bù cho nhau:

- **AutoCreate** giải quyết phần *tồn tại account* — cho phép ký tx đầu mà không cần ai chạm vào trước. Nhưng account vẫn **số dư 0**, nên khi đã bật fee > 0 (Mục 6), tx đầu vẫn fail `insufficient funds` — AutoCreate **không** cấp vốn.
- **Faucet** giải quyết phần *vốn* — `MsgSend` từ treasury vào địa chỉ. Bản thân `MsgSend` đến địa chỉ mới **cũng tự tạo account** (như luồng "bình thường" ở trên), nên ở chế độ có-fee thì faucet khôi phục được trải nghiệm "tx đầu chạy được" **mà không phụ thuộc AutoCreate** (xem ghi chú trong Mục 6b: "Khôi phục… mà không cần dựa AutoCreate").

Tóm lại: ở **chế độ 0-fee** (mặc định dev), AutoCreate là đủ để onboarding 0 bước. Ở **chế độ có-fee**, phải có faucet để cấp vốn; AutoCreate khi đó chỉ còn là tiện ích phụ (account nào ký mà chưa tồn tại thì được tạo, nhưng vẫn cần token để qua được bước trừ phí).

---

<a id="muc-7"></a>
## 7. Checklist khi đưa lên production có fee

- [ ] **Bước 0**: set `COSMOS_EXEC_ENFORCE_SIGNATURES=true` (không có thì ante không chạy → fee không enforce được).
- [ ] **Bước 1**: set `COSMOS_EXEC_TIA_PER_BYTE` / `MIN_GAS_PRICE` / `GAS_DENOM`; xác nhận `/tx/estimate` ra số đúng.
- [ ] **Bước 2**: đã implement — chỉ cần `COSMOS_EXEC_MIN_GAS_PRICE` > 0 (bước 1); test tx trả thiếu bị từ chối `ErrInsufficientFee` trong CheckTx.
- [ ] **Bước 3 (faucet)**: set `COSMOS_EXEC_TREASURY_PRIVKEY_HEX` (+ tùy chỉnh amount/cooldown) để bật A+B (mục 6b); test `GET /faucet?addr=...` rồi xác nhận địa chỉ có số dư trả phí được.
- [ ] **Sweep phí (mục 6d)**: set `COSMOS_EXEC_TREASURY_ADDR` để phí không tích luỹ vô hạn trong `fee_collector`. Đảm bảo **mọi node** (sequencer + full node) set cùng giá trị, nếu không sẽ fork.
- [ ] Denom `TREASURY_AMOUNT`/`FAUCET_AMOUNT` khớp `GAS_DENOM`.
- [ ] Treasury key qua env/KMS; cân nhắc pattern sub-treasury (hot key hạn mức nhỏ).
- [ ] Đo phí DA **thật** trên tải dự kiến bằng `scripts/measure_da_fees.mjs` (đọc `fee.amount` + `blob_sizes` của PFB) — đừng đoán theo byte.
- [ ] Xác nhận **gas price thật** của mạng: query `minfee` của celestia-app (sàn `NetworkMinGasPrice`) + giá node áp; nếu cần cố định thì set `--evnode.da.submit_options` (Mục 5), mặc định để node tự định theo minfee.
- [ ] Đặt `TIA_PER_BYTE` = rate hiệu dụng đo thật (Mục 1c-D) sao cho fee thu ≥ hóa đơn Celestia (cộng biên an toàn cho biến động giá TIA).
- [ ] Bật `lazy_mode` trừ khi app cần block đều (vd timestamp/oracle).
- [ ] Theo dõi tỉ lệ `fee thu / chi Celestia` qua thời gian (giá TIA biến động → có thể lỗ nếu không retune).

---

<a id="muc-8"></a>
## 8. Kết luận

- **Throughput thực tế + LazyMode**: tổng chi phí (đã gồm hóa đơn Celestia của operator) **thấp hơn đáng kể** so với tự dựng Cosmos chain thường, vì bạn xóa hẳn "thuế bảo mật" dạng lạm phát + N× hạ tầng validator, thay bằng DA dùng chung.
- **Throughput rất thấp, block đều**: có thể **đắt hơn** — vì trả Celestia cho block gần rỗng. Khắc phục bằng LazyMode / block time dài.
- So với **deploy lên chain chia sẻ có sẵn** (không tự dựng): rollup chỉ thắng khi bạn cần chủ quyền / custom logic / throughput riêng; nếu không, chain chia sẻ có thể rẻ hơn vì bạn không nuôi hạ tầng nào.
- Luôn nhớ: chi phí rollup **hiện minh bạch** (hóa đơn TIA), chi phí chain thường **ẩn trong lạm phát** — so sánh công bằng phải tính cả phần ẩn đó.

---

<a id="tham-chieu-code"></a>
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
| DA submit (blob lên Celestia) | `block/internal/submitting/da_submitter.go` (`Submit(...,-1,...,options)`) |
| DA client (bỏ qua gasPrice, đọc options) | `block/internal/da/client.go` (`Submit(... _ float64, ...)`) |
| SubmitOptions (gas_price / max_gas_price / gas) | `pkg/da/jsonrpc/submit_options.go` |
| DA gas price riêng của cosmos-exec SDK (engram/telemetry, KHÔNG phải rollup) | `apps/cosmos-exec/sdk/cosmoswasm/blob.go` (`DA_GAS_PRICE`) |
| Đo phí DA thật on-chain (fee.amount + blob_sizes) | `my-dapp-web/scripts/measure_da_fees.mjs` |
| Sequencer & mô hình bảo mật | [sequencer-security.md](sequencer-security.md) |
