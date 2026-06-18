# Các giải pháp dùng để đối chiếu với đồ án

Tài liệu này mô tả chi tiết những giải pháp được dùng để **đối chiếu** với đồ án
— các cách tiếp cận khác để dựng và vận hành một chuỗi ứng dụng (app-chain hoặc
rollup), nhất là chuỗi có hợp đồng thông minh. Mỗi mục gồm bốn phần: *là gì*,
**cách hoạt động** (kèm sơ đồ minh hoạ), **số liệu và dẫn chứng** (có link nguồn
để trích dẫn), và **điểm khác biệt cốt lõi so với đồ án**.

> **Lưu ý về số liệu.** Các con số của bên thứ ba (cửa sổ thách thức, block time,
> kích thước blob, thông lượng…) đúng tại thời điểm biên soạn nhưng có thể thay
> đổi theo các bản nâng cấp. Trước khi trích vào báo cáo, hãy kiểm chứng lại tại
> đúng nguồn đã dẫn ở mỗi mục và mục [Tham chiếu](#11-tham-chiếu). Số liệu của
> đồ án lấy từ phép đo thực ở Chương 4 của báo cáo.

## Mục lục

- [0. Mốc của đồ án (để quy chiếu khi so sánh)](#0-mốc-của-đồ-án-để-quy-chiếu-khi-so-sánh)
- [1. ev-abci — adapter ABCI giữa Cosmos SDK và ev-node](#1-ev-abci--adapter-abci-giữa-cosmos-sdk-và-ev-node)
- [2. ev-node / Rollkit ở dạng khung trần](#2-ev-node--rollkit-ở-dạng-khung-trần)
- [3. Dymension RDK — RollApp neo trên hub](#3-dymension-rdk--rollapp-neo-trên-hub)
- [4. App-chain CosmWasm truyền thống (Juno, Neutron, Stargaze, Osmosis)](#4-app-chain-cosmwasm-truyền-thống-juno-neutron-stargaze-osmosis)
- [5. Bộ khung rollup hệ Ethereum (OP Stack, Arbitrum Orbit, Polygon CDK)](#5-bộ-khung-rollup-hệ-ethereum-op-stack-arbitrum-orbit-polygon-cdk)
- [6. Astria — mạng sequencer dùng chung](#6-astria--mạng-sequencer-dùng-chung)
- [7. Các lớp sẵn sàng dữ liệu thay thế (EigenDA, Avail, EIP-4844)](#7-các-lớp-sẵn-sàng-dữ-liệu-thay-thế-eigenda-avail-eip-4844)
- [8. evstack/wasmd — wasmd thuần trên CometBFT](#8-evstackwasmd--wasmd-thuần-trên-cometbft)
- [9. Bảng tổng hợp các trục so sánh](#9-bảng-tổng-hợp-các-trục-so-sánh)
- [10. Gợi ý cách dùng tài liệu này để viết phần so sánh](#10-gợi-ý-cách-dùng-tài-liệu-này-để-viết-phần-so-sánh)
- [11. Tham chiếu](#11-tham-chiếu)

---

## 0. Mốc của đồ án (để quy chiếu khi so sánh)

Định vị: bộ công cụ dựng **sovereign rollup** chạy **CosmWasm**, dùng **Celestia**
làm lớp sẵn sàng dữ liệu, lớp thực thi cài trực tiếp vào ev-node (không qua
adapter). Ngăn xếp và đường đi của một giao dịch:

```
   Người dùng / dApp web / SDK Go
              │  HTTP/JSON
              ▼
   cosmos-exec (máy chủ thực thi)
   ┌───────────────────────────────────────────┐
   │ CosmosExecutor  ──gọi trong tiến trình──>  │
   │   app.App = Cosmos SDK BaseApp + CosmWasm  │
   └───────────────────────────────────────────┘
              ▲ execution.Executor (giao diện ev-node)
              │
        ev-node  ── sequencer (sắp thứ tự) · P2P · submit DA
              │  mỗi block → 2 blob: SignedHeader + SignedData
              ▼
        Celestia (lớp sẵn sàng dữ liệu, theo namespace)
```

Số liệu nền (đo ở Chương 4, mạng Celestia Mocha thật, bật ký + thu phí):

| Chỉ số | Giá trị đo |
|--------|-----------|
| Block time (xác nhận mềm) | 2,0 s, độ lệch ≈ 0 |
| Độ trễ DA-finalize sau soft head | ~7 block (~14 s) |
| Chu kỳ submit blob lên Celestia | ~12–18 s/lần, gộp 7–9 header/blob |
| Khởi động nguội đến block đầu | ~11 s |
| Store cw20-base (317 KB) | ~4,18 triệu gas; phí DA mô phỏng ~0,0317 TIA |
| Instantiate / Execute | ~176k / ~126k gas |
| Query (chỉ đọc) | 0,6–1,0 ms |
| Tiết kiệm gas blob-first vs nhúng on-chain (1 MB) | ~90% |

Các con số này là chuẩn để đối chiếu với từng giải pháp bên dưới.

---

## 1. ev-abci — adapter ABCI giữa Cosmos SDK và ev-node

**Là gì.** Adapter chính thức của evstack, con đường khuyến nghị để chạy ứng dụng
Cosmos SDK trên ev-node. Đây là đối tượng so sánh gần nhất với đóng góp cốt lõi
của đồ án.

**Cách hoạt động.** Cosmos SDK được thiết kế để nói chuyện với một động cơ đồng
thuận qua giao thức ABCI (InitChain, FinalizeBlock, Commit, Query). ev-abci đứng
giữa và **dịch** lời gọi từ giao diện thực thi của ev-node sang ABCI, rồi dịch
kết quả ngược lại — đóng vai một động cơ đồng thuận giả lập trước ứng dụng.

```
  ev-abci (khuyến nghị)                 Đồ án (tự cài)
  ─────────────────────                 ─────────────────────
  ev-node                               ev-node
    │ execution.Executor                  │ execution.Executor
    ▼                                     ▼
  ev-abci  ── DỊCH ──┐                  CosmosExecutor
    │ ABCI           │ (thêm 1 tầng)      │ gọi thẳng (cùng tiến trình)
    ▼                                     ▼
  Cosmos SDK BaseApp                    Cosmos SDK BaseApp
  (InitChain/FinalizeBlock/Commit)      (FinalizeBlock/Commit)
```

**Số liệu và dẫn chứng.**
- Mã nguồn ev-abci: <https://github.com/evstack/ev-abci>
- Đặc tả ABCI 2.0 (PrepareProposal/ProcessProposal/FinalizeBlock): <https://docs.cometbft.com/v1.0/spec/abci/>
- Khác biệt định lượng có thể nêu: số tầng trên đường đi giao dịch (đồ án bỏ 1
  tầng dịch ABCI), và số dependency kéo theo — nên đối chiếu bằng cách đếm trực
  tiếp trên hai nhánh build.

**So với đồ án.** Đồ án bỏ tầng dịch: CosmosExecutor hiện thực thẳng giao diện
thực thi của ev-node và gọi trực tiếp BaseApp trong cùng tiến trình → bớt một
proxy, giảm phụ thuộc, đường đi ngắn hơn; đánh đổi là mất tính tương thích phổ
quát của một adapter ABCI. Chi tiết: [cosmos-vs-evabci.md](cosmos-vs-evabci.md).

---

## 2. ev-node / Rollkit ở dạng khung trần

**Là gì.** ev-node là khung sovereign rollup mà đồ án chạy bên trên; Rollkit là
thế hệ trước. Xét ở trạng thái **trần** (chưa gắn lớp thực thi) để làm mốc cho
phần đồ án bổ sung.

**Cách hoạt động.** Khung cung cấp sequencer, sản xuất block, P2P và submit/đọc
DA; nhưng lớp thực thi để ngỏ dưới dạng một giao diện — người vận hành phải tự
viết máy trạng thái.

```
  Khung trần (ev-node/Rollkit)            Đồ án = khung + phần lấp
  ──────────────────────────              ──────────────────────────
  [sequencer][P2P][submit DA]             [sequencer][P2P][submit DA]
        │ execution.Executor                    │ execution.Executor
        ▼                                        ▼
     ❓ BẠN TỰ VIẾT                          ✅ CosmosExecutor + CosmWasm
     (chưa có hợp đồng)                      (đóng gói sẵn)
```

**Số liệu và dẫn chứng.**
- ev-node: <https://github.com/evstack/ev-node> · Tài liệu: <https://ev.xyz> (evstack)
- Rollkit (tiền thân): <https://rollkit.dev>
- Quy mô phần đồ án bổ sung: ~7.600 dòng Go phía máy chủ (số ở Chương 4) chính
  là khối lượng mà người dùng khung trần phải tự đảm nhiệm để có lớp thực thi
  CosmWasm tương đương.

**So với đồ án.** Khung trần không kèm môi trường hợp đồng; muốn CosmWasm phải
tự hiện thực toàn bộ lớp thực thi. Đồ án lấp đúng chỗ trống đó. Chi tiết:
[cosmos-vs-evnode.md](cosmos-vs-evnode.md).

---

## 3. Dymension RDK — RollApp neo trên hub

**Là gì.** Bộ công cụ dựng RollApp trên nền tảng Dymension.

**Cách hoạt động.** RollApp tự sản xuất block nhưng định kỳ gửi trạng thái và
cam kết về **hub Dymension** để được quyết toán và dàn xếp tranh chấp; hub là
trọng tài và cầu nối thanh khoản. Mô hình hub-and-spoke.

```
   Đồ án (sovereign)                  Dymension (hub-and-spoke)
   ─────────────────                  ──────────────────────────
   Rollup tự quản                     RollApp 1 ─┐
       │ chỉ thuê DA                  RollApp 2 ─┼─> HUB Dymension
       ▼                              RollApp 3 ─┘   (quyết toán,
   Celestia (DA)                          │           trọng tài)
   (không hub, không neo)                 ▼
                                      DA layer
```

**Số liệu và dẫn chứng.**
- Dymension: <https://dymension.xyz> · Tài liệu: <https://docs.dymension.xyz>
- Trục đối chiếu: nơi quyết toán (hub vs không hub) và mô hình tin cậy.

**So với đồ án.** RollApp phụ thuộc hub về quyết toán → không sovereign. Đồ án
tự quản hoàn toàn, chỉ thuê DA Celestia. Khác biệt về **mô hình tin cậy và quyết
toán**.

---

## 4. App-chain CosmWasm truyền thống (Juno, Neutron, Stargaze, Osmosis)

**Là gì.** Các blockchain Lớp 1 độc lập trong hệ Cosmos có hỗ trợ CosmWasm —
minh chứng CosmWasm là môi trường trưởng thành, đồng thời đại diện cách chạy
CosmWasm theo mô hình chain truyền thống.

**Cách hoạt động.** Mỗi chain tự vận hành **tập validator** chạy đồng thuận BFT
(CometBFT): validator đề xuất và bỏ phiếu, đạt **trên 2/3** quyền biểu quyết thì
block được chốt ngay (instant finality, không reorg). Mọi node lưu **toàn bộ**
dữ liệu và state. An ninh đến từ token mà validator khoá; CosmWasm chạy như
module wasmd trong app.

```
  Chain CosmWasm truyền thống              Đồ án (rollup mô-đun)
  ────────────────────────────             ──────────────────────
  Validator set (BFT, >2/3 phiếu)          1 sequencer (sắp thứ tự)
  mỗi node lưu TOÀN BỘ data on-chain       data lớn → off-chain (Celestia)
  an ninh = token stake                    an ninh dữ liệu = DA + DAS
  finality tức thì (1 block)               soft (~2s) → DA-final (~14s)
```

**Số liệu và dẫn chứng.**
- Mô hình BFT/finality của CometBFT: <https://docs.cometbft.com/v1.0/spec/consensus/>
- Juno: <https://junonetwork.io> · Neutron: <https://docs.neutron.org> ·
  Stargaze: <https://stargaze.zone> · Osmosis: <https://docs.osmosis.zone>
- Nét riêng đáng dẫn: **Neutron** không tự bootstrap validator mà kế thừa an
  ninh từ Cosmos Hub qua Interchain Security (Replicated Security) — xem
  <https://cosmos.github.io/interchain-security/>. **Osmosis** dùng CosmWasm
  theo cơ chế cho phép có kiểm soát (permissioned) thay vì mở hoàn toàn.
- Trục định lượng: chi phí lưu dữ liệu. Chain truyền thống lưu mọi byte on-chain
  (gas tăng tuyến tính theo kích thước), trong khi đồ án đẩy ra DA — đối chiếu
  trực tiếp với hàng "tiết kiệm ~90%" ở mục 0.

**So với đồ án.** Mô hình truyền thống cần bootstrap và duy trì validator set +
token kinh tế, và lưu mọi dữ liệu on-chain (đắt khi data lớn). Đồ án không
validator set, thuê DA, data lớn off-chain. Khác biệt về **chi phí vận hành, mô
hình bảo mật và chi phí lưu trữ**.

---

## 5. Bộ khung rollup hệ Ethereum (OP Stack, Arbitrum Orbit, Polygon CDK)

**Là gì.** Ba bộ khung phóng rollup gắn Ethereum: OP Stack (Optimism), Arbitrum
Orbit (Offchain Labs), Polygon CDK (Polygon).

**Cách hoạt động.** Cả ba chạy EVM. Sequencer gom giao dịch, tạo block Lớp 2;
định kỳ đăng **dữ liệu giao dịch** + **cam kết state** lên Ethereum (Lớp 1) làm
nơi quyết toán. Khác nhau ở cách chứng minh state đúng:

```
  OPTIMISTIC (OP Stack, Arbitrum)        ZK (Polygon CDK)
  ───────────────────────────────        ─────────────────────────
  L2 block → đăng L1                      L2 batch + validity proof (zk)
       │                                       │
  cửa sổ thách thức ~7 ngày               L1 VERIFY proof ngay
  (ai có fraud proof thì phạt)            (không cần chờ)
       │                                       │
  hết cửa sổ mới rút được về L1           final sau khi proof được verify

  ĐỒ ÁN: CosmWasm, không neo L1; data lên Celestia; DA-final ~14s
```

**Số liệu và dẫn chứng.**
- OP Stack — cửa sổ rút/thách thức **7 ngày** (challenge period): tài liệu
  Optimism <https://docs.optimism.io> (mục withdrawals / fault proofs).
- Arbitrum Orbit — fraud proof tương tác, challenge period ~**6,4–7 ngày**:
  <https://docs.arbitrum.io>.
- Polygon CDK — zkEVM, validity proof, không có cửa sổ thách thức dài:
  <https://docs.polygon.technology/cdk/>.
- Chi phí dữ liệu của các rollup này theo cơ chế blob của Ethereum (EIP-4844,
  xem mục 7) — đây là điểm đối chiếu chi phí với DA Celestia của đồ án.

**So với đồ án.** Mặc định gắn EVM và neo trên Ethereum (an ninh, quyết toán và
phí dữ liệu đều theo Ethereum). Đồ án chạy CosmWasm, không neo L1, dùng Celestia
rẻ hơn, tự quản luật chain. Khác biệt về **máy ảo, nơi quyết toán, chi phí dữ
liệu**. Lưu ý công bằng: vì khác máy ảo, chỉ nên đối chiếu ở trục mô hình DA và
chi phí, không so trực tiếp tính năng EVM với CosmWasm.

---

## 6. Astria — mạng sequencer dùng chung

**Là gì.** Mạng sequencer dùng chung (shared sequencer): cung cấp dịch vụ sắp
thứ tự giao dịch cho nhiều rollup.

**Cách hoạt động.** Các rollup gửi giao dịch tới cùng mạng Astria; mạng đạt đồng
thuận về **thứ tự**, trả lại luồng tx đã sắp xếp cho từng rollup và công bố lên
một DA (thường là Celestia). Mỗi rollup **tự thực thi** luồng đó. Nhờ chung một
thứ tự, mô hình này hỗ trợ giao dịch nguyên tử xuyên rollup.

```
  Astria giải TẦNG SẮP THỨ TỰ           Đồ án đóng góp TẦNG THỰC THI
  ───────────────────────────           ────────────────────────────
  Rollup A ─┐                            ev-node single sequencer
  Rollup B ─┼─> Astria (ordering) ─┐         │ (sắp thứ tự)
  Rollup C ─┘                       │         ▼
       ▲ trả luồng tx đã sắp        ▼     CosmosExecutor (CosmWasm)
       └── mỗi rollup TỰ THỰC THI  Celestia (DA)   ← phần đồ án làm
```

**Số liệu và dẫn chứng.**
- Astria: <https://astria.org> · Tài liệu: <https://docs.astria.org>
- Trục đối chiếu: Astria làm việc ở tầng sequencer, đồ án ở tầng thực thi — bổ
  trợ chứ không trùng. Hướng phát triển của đồ án (multi-sequencer) là nơi hai
  ý tưởng gặp nhau.

**So với đồ án.** Astria không cung cấp lớp thực thi. Đồ án hiện dùng single
sequencer của ev-node và đóng góp ở lớp thực thi CosmWasm.

---

## 7. Các lớp sẵn sàng dữ liệu thay thế (EigenDA, Avail, EIP-4844)

**Là gì.** Đồ án dùng Celestia làm DA. Mục này so sánh với EigenDA, Avail (hai
DA chuyên dụng) và EIP-4844 (cách Ethereum lưu dữ liệu rollup).

**Cách hoạt động — làm mốc bằng Celestia.** Node đăng dữ liệu dưới dạng blob
theo namespace. Để bảo đảm dữ liệu thật sự sẵn có mà không tải hết, Celestia
dùng **Data Availability Sampling (DAS)**: light node tải ngẫu nhiên nhiều mảnh
nhỏ; lấy đủ mẫu thành công thì xác suất cao toàn bộ dữ liệu đã được công bố.

```
  Celestia / Avail (DAS)            EigenDA (restaking)         EIP-4844 (Ethereum)
  ───────────────────────          ─────────────────────       ────────────────────
  blob theo namespace              disperser chia mảnh +       tx mang "blob"
  erasure-code + KZT/KZG           erasure-code → operator     lưu TẠM ~18 ngày
  light node LẤY MẪU ngẫu nhiên    operator KÝ chứng thực      phí riêng, rẻ hơn
  → xác suất cao là sẵn có         an ninh = ETH restake       calldata
```

**Số liệu và dẫn chứng.**
- Celestia: block time ~6 s, blob theo namespace, DAS — <https://docs.celestia.org>.
  Mainnet từ 31/10/2023.
- Avail: KZG + DAS, light client xác minh bằng lấy mẫu — <https://docs.availproject.org>.
- EigenDA: dựa trên restaking EigenLayer; disperser + operator ký chứng thực
  (KZG), thông lượng mục tiêu cao (hàng chục MB/s theo lộ trình) —
  <https://docs.eigenda.xyz>.
- EIP-4844 (kích hoạt qua bản Dencun 13/03/2024): mỗi blob **128 KiB**
  (131.072 byte); ban đầu mục tiêu 3 / tối đa 6 blob mỗi block (được nâng ở các
  bản nâng cấp sau); dữ liệu blob chỉ giữ **~18 ngày** rồi xoá —
  <https://eips.ethereum.org/EIPS/eip-4844>.
- Trục đối chiếu chi phí: DA chuyên dụng (Celestia/Avail) thường rẻ hơn dùng
  blob Ethereum vì không cạnh tranh blockspace với toàn hệ Ethereum — đây là căn
  cứ cho lập luận "Celestia rẻ hơn EIP-4844" trong báo cáo.

**So với đồ án.** Kiến trúc đồ án đã trừu tượng hoá lớp DA nên về nguyên tắc cắm
được EigenDA/Avail (một hướng phát triển). Celestia được chọn vì có sẵn tích hợp
ev-node và chi phí thấp.

---

## 8. evstack/wasmd — wasmd thuần trên CometBFT

**Là gì.** Một fork wasmd do chính **evstack** (chủ ev-node) duy trì, gốc từ
CosmWasm/wasmd. Về bản chất nó là một chain CosmWasm "truyền thống" như mục 4,
nhưng đáng tách riêng vì hai lý do: (1) do **cùng tổ chức** với ev-node phát hành,
(2) dùng **đúng module `x/wasm`** mà đồ án nhúng — nên đây là điểm đối chiếu sạch
nhất theo trục "cùng tầng thực thi, khác tầng đồng thuận/DA".

**Cách hoạt động.** Là chain Cosmos SDK đầy đủ chạy CometBFT: validator đề xuất &
bỏ phiếu BFT, CometBFT gọi `app.App` qua ABCI, `x/wasm` là một module trong app.
Không có ev-node, không lớp DA ngoài, không executor — y hệt wasmd upstream.

```
  evstack/wasmd (CometBFT thuần)        Đồ án (cùng x/wasm, khác consensus/DA)
  ─────────────────────────────        ──────────────────────────────────────
  validator set ─ BFT (CometBFT)       ev-node sequencer + Celestia DA
        │ ABCI                                │ execution.Executor
        ▼                                     ▼
  app.App + x/wasm                      CosmosExecutor → app.App + x/wasm
  (mọi node lưu TOÀN BỘ data)           (data lớn → Celestia, off-chain)
```

**Số liệu và dẫn chứng.**
- evstack/wasmd: <https://github.com/evstack/wasmd> (fork của
  <https://github.com/CosmWasm/wasmd>).
- Kiểm chứng qua `go.mod`: module vẫn là `github.com/CosmWasm/wasmd`, dùng
  `cosmos-sdk v0.50.10`, `cometbft v0.38.15`; **không** có require/replace nào
  tới `evstack/ev-node` hay module DA/sequencer.
- Thư mục `app/` chuẩn (`app.go`, `ante.go`, `wasm.go`…), không có file tích hợp
  ev-node — xác nhận đây là wasmd CometBFT thuần, không phải lớp thực thi rollup.

**So với đồ án.** Cùng môi trường hợp đồng (`x/wasm`), nhưng evstack/wasmd giữ
nguyên CometBFT (cần validator set, mọi node lưu toàn bộ data on-chain), còn đồ án
**thay tầng đồng thuận** bằng ev-node sequencer + Celestia DA và đẩy data lớn
off-chain. Vì module path không đổi (`CosmWasm/wasmd`) và không tích hợp ev-node,
**không có lý do dùng fork này làm dependency** thay cho upstream. Giá trị thực
của nó với đồ án là **tham chiếu cách wiring `x/wasm`** (ante decorator riêng của
wasm, `WasmConfig`, capabilities). Trên thực tế đồ án đã mượn 2 ante decorator của
wasmd — `LimitSimulationGasDecorator` (chặn DoS khi simulate) và `CountTXDecorator`
(bộ đếm tx/block cho tính xác định của contract) — xem
[`app/ante.go`](../../../app/ante.go) và đối chiếu
[`app/ante.go` của wasmd](https://github.com/CosmWasm/wasmd/blob/main/app/ante.go).

---

## 9. Bảng tổng hợp các trục so sánh

| Giải pháp | Cơ chế cốt lõi | Số liệu/đặc trưng nổi bật | Trục khác biệt chính với đồ án |
|-----------|----------------|---------------------------|--------------------------------|
| ev-abci | Dịch ev-node ↔ ABCI Cosmos SDK | thêm 1 tầng proxy | Đồ án bỏ adapter, cài thực thi trực tiếp |
| ev-node/Rollkit (trần) | Khung sequencer+P2P+DA, để ngỏ thực thi | phần lấp ~7.600 dòng Go | Đồ án cung cấp sẵn lớp CosmWasm |
| Dymension RDK | RollApp neo & quyết toán trên hub | hub-and-spoke | Đồ án sovereign, không hub |
| Juno/Neutron/Stargaze/Osmosis | L1 validator set, BFT >2/3, data on-chain | finality 1 block; lưu mọi byte | Không validator set; data off-chain |
| evstack/wasmd | Fork wasmd thuần, CometBFT + x/wasm | cùng x/wasm; SDK 0.50.10 | Cùng tầng thực thi, khác consensus/DA |
| OP Stack/Arbitrum Orbit | Rollup EVM optimistic neo Ethereum | cửa sổ thách thức ~7 ngày | CosmWasm; DA Celestia; không neo L1 |
| Polygon CDK | Rollup EVM zk neo Ethereum | validity proof, không cửa sổ chờ | CosmWasm; DA Celestia; không neo L1 |
| Astria | Sequencer dùng chung (chỉ ordering) | tầng sắp thứ tự | Đồ án đóng góp tầng thực thi |
| Celestia (đồ án dùng) | DA + DAS theo namespace | block ~6s | (mốc) |
| EigenDA | Restaking + chứng thực operator | an ninh từ ETH restake | DA thay thế tiềm năng |
| Avail | KZG + DAS | light client lấy mẫu | DA thay thế tiềm năng |
| EIP-4844 | Blob tạm thời của Ethereum | 128 KiB/blob, giữ ~18 ngày | DA chuyên dụng thường rẻ hơn |

---

## 10. Gợi ý cách dùng tài liệu này để viết phần so sánh

- **Trong Chương 4 (đánh giá):** ưu tiên đối chiếu **định lượng** mà đồ án sở
  hữu — bảng "tiết kiệm gas blob-first vs nhúng on-chain" (mục 0) so với mô hình
  lưu mọi byte on-chain của chain CosmWasm truyền thống (mục 4). Đây là so sánh
  bạn tự đo, mạnh nhất.
- **Trong Chương 6 (kết luận/đối chiếu):** dùng các sơ đồ và bảng ở đây cho so
  sánh **định tính** về mô hình (sovereign vs hub, EVM vs CosmWasm, tầng
  sequencer vs tầng thực thi).
- **Khi trích số của bên thứ ba** (cửa sổ 7 ngày, blob 128 KiB, block ~6s…):
  luôn dẫn nguồn ở mục [Tham chiếu](#11-tham-chiếu) theo chuẩn IEEE và ghi rõ
  thời điểm truy cập, vì các con số này thay đổi theo bản nâng cấp.
- **Giữ công bằng:** không so độ trễ/throughput đo trên máy dev của đồ án với số
  production của đối thủ; với rollup EVM chỉ so trục DA và chi phí, không so
  tính năng máy ảo.

---

## 11. Tham chiếu

- ev-abci: <https://github.com/evstack/ev-abci>
- ev-node: <https://github.com/evstack/ev-node>
- evstack/wasmd: <https://github.com/evstack/wasmd> · upstream CosmWasm/wasmd: <https://github.com/CosmWasm/wasmd>
- Rollkit: <https://rollkit.dev>
- ABCI / CometBFT: <https://docs.cometbft.com>
- Interchain Security (Neutron): <https://cosmos.github.io/interchain-security/>
- Dymension: <https://docs.dymension.xyz>
- Juno: <https://junonetwork.io>
- Neutron: <https://docs.neutron.org>
- Stargaze: <https://stargaze.zone>
- Osmosis: <https://docs.osmosis.zone>
- OP Stack: <https://docs.optimism.io>
- Arbitrum: <https://docs.arbitrum.io>
- Polygon CDK: <https://docs.polygon.technology/cdk/>
- Astria: <https://docs.astria.org>
- Celestia: <https://docs.celestia.org>
- EigenDA: <https://docs.eigenda.xyz>
- Avail: <https://docs.availproject.org>
- EIP-4844: <https://eips.ethereum.org/EIPS/eip-4844>
