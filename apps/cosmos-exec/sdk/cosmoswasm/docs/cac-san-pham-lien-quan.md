# Các giải pháp dùng để đối chiếu với đồ án

Tài liệu này mô tả chi tiết những giải pháp được dùng để **đối chiếu** với đồ án
— các cách tiếp cận khác để dựng và vận hành một chuỗi ứng dụng (app-chain hoặc
rollup), nhất là chuỗi có hợp đồng thông minh. Mỗi mục gồm bốn phần: *là gì*,
**cách hoạt động** (kèm sơ đồ minh hoạ), **số liệu và dẫn chứng** (có link nguồn
để trích dẫn), và **điểm khác biệt cốt lõi so với đồ án**.

> **Lưu ý về số liệu.** Các con số của bên thứ ba (cửa sổ thách thức, block time,
> kích thước blob, thông lượng…) đúng tại thời điểm biên soạn nhưng có thể thay
> đổi theo các bản nâng cấp. Trước khi trích vào báo cáo, hãy kiểm chứng lại tại
> đúng nguồn đã dẫn ở mỗi mục và mục [Tham chiếu](#12-tham-chiếu). Số liệu của
> đồ án lấy từ phép đo thực ở Chương 4 của báo cáo.

## Mục lục

- [0. Mốc của đồ án (để quy chiếu khi so sánh)](#0-mốc-của-đồ-án-để-quy-chiếu-khi-so-sánh)
- [1. ev-abci — adapter ABCI giữa Cosmos SDK và ev-node](#1-ev-abci--adapter-abci-giữa-cosmos-sdk-và-ev-node)
- [2. ev-node / Rollkit ở dạng khung trần](#2-ev-node--rollkit-ở-dạng-khung-trần)
- [3. Dymension RDK — RollApp neo trên hub](#3-dymension-rdk--rollapp-neo-trên-hub)
- [4. App-chain CosmWasm truyền thống (Juno, Neutron, Stargaze, Osmosis)](#4-app-chain-cosmwasm-truyền-thống-juno-neutron-stargaze-osmosis)
- [5. Bộ khung rollup hệ Ethereum (OP Stack, Arbitrum Orbit, Polygon CDK)](#5-bộ-khung-rollup-hệ-ethereum-op-stack-arbitrum-orbit-polygon-cdk)
- [6. Mạng sequencer dùng chung (Astria & Espresso)](#6-mạng-sequencer-dùng-chung-astria--espresso)
- [7. Các lớp sẵn sàng dữ liệu thay thế (EigenDA, Avail, EIP-4844)](#7-các-lớp-sẵn-sàng-dữ-liệu-thay-thế-eigenda-avail-eip-4844)
- [8. evstack/wasmd — wasmd thuần trên CometBFT](#8-evstackwasmd--wasmd-thuần-trên-cometbft)
- [9. Bảng tổng hợp các trục so sánh](#9-bảng-tổng-hợp-các-trục-so-sánh)
- [10. So sánh quy trình triển khai, phí giao dịch (USD) và thời gian quyết toán](#10-so-sánh-quy-trình-triển-khai-phí-giao-dịch-usd-và-thời-gian-quyết-toán)
- [11. Gợi ý cách dùng tài liệu này để viết phần so sánh](#11-gợi-ý-cách-dùng-tài-liệu-này-để-viết-phần-so-sánh)
- [12. Tham chiếu](#12-tham-chiếu)

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

Số liệu nền (đo ở Chương 4 trên **mạng Celestia private tự vận hành** —
`chain_id = private`, `DA_GAS_PRICE = 0,005 utia/gas` — bật ký + thu phí):

| Chỉ số | Giá trị đo |
|--------|-----------|
| Block time (xác nhận mềm) | 2,0 s, độ lệch ≈ 0 |
| Độ trễ DA-finalize sau soft head | ~7 block (~14 s) |
| Block time của Celestia (DA) | ~5,5 s (đo: 54,7 s / 10 block) |
| Chu kỳ submit blob lên Celestia | ~12–18 s/lần (mỗi ~3 block DA), gộp 7–9 header/blob |
| **Phí DA THẬT mỗi lần submit blob** | **TB ~330 utia ≈ 0,00033 TIA** (đo on-chain N=22 PFB; σ 32 utia; dải 320–473 utia; ~80k gas/PFB) |
| Khởi động nguội đến block đầu | ~11 s |
| Store cw20-base (317 KB) | ~4,18 triệu gas; phí DA cho block thường xem hàng trên (phí cho riêng blob store cần đo theo DA height của giao dịch store) |
| Instantiate / Execute | ~176k / ~126k gas |
| Query (chỉ đọc) | 0,6–1,0 ms |
| Tiết kiệm gas blob-first vs nhúng on-chain (1 MB) | ~90% |

Các con số này là chuẩn để đối chiếu với từng giải pháp bên dưới.

> **Cách đo phí DA thật.** Phí Celestia không do ev-node trả về (RPC `Submit`
> chỉ trả `Height` + `BlobSize`); số đúng nằm ở `fee.amount` của giao dịch
> **PayForBlobs** trên chuỗi. Script `scripts/measure_da_fees.mjs` (trong FE)
> quét block celestia-app qua CometBFT RPC, gỡ lớp bọc IndexWrapper/BlobTx,
> decode `AuthInfo.fee` và in bảng + thống kê (**phương pháp + code minh hoạ đầy
> đủ ở fee-economics §1c-D2**). Số trên đo ngày 2026-06-19,
> height DA 593051–593129 (**run cũ**). Lưu ý: giá thực tế ≈ 0,004 utia/gas
> (fee.amount / gas_limit), thấp hơn `DA_GAS_PRICE = 0,005` đã cấu hình — nên dùng
> số đo on-chain thay vì nhân thủ công gas × giá cấu hình. **Run sau** (height
> ~611–612k, gồm PFB blob-first 300 KiB) đo minfee đã lên **0,005 utia/gas**; các
> bảng §10.4–10.6 và báo cáo dùng mốc hiện hành 0,005 này (xem fee-economics §1c-D).

---

## 1. ev-abci — adapter ABCI giữa Cosmos SDK và ev-node

**Là gì.** Adapter chính thức của evstack, con đường khuyến nghị để chạy ứng dụng
Cosmos SDK trên ev-node. Đây là đối tượng so sánh gần nhất với đóng góp cốt lõi
của đồ án.

**Cách hoạt động.** Cosmos SDK được thiết kế để nói chuyện với một động cơ đồng
thuận qua giao thức ABCI (InitChain, PrepareProposal, ProcessProposal,
FinalizeBlock, Commit, Query). Bình thường động cơ đó là CometBFT. ev-abci **thay
chỗ CometBFT**: nó là một thư viện (library) được nhúng vào binary ev-node, hiện
thực giao diện `execution.Executor` của ev-node ở một đầu, và ở đầu kia đóng vai
một **động cơ đồng thuận giả lập** (consensus shim) nói ABCI với ứng dụng. Mỗi khi
ev-node cần một việc (khởi tạo chain, lấy tx, thực thi block, chốt block), nó gọi
executor; ev-abci **dịch** lời gọi đó sang một hoặc nhiều lời gọi ABCI tới
`app.App`, rồi **dịch kết quả ngược lại** cho ev-node. Ứng dụng bên trong tưởng
như đang chạy dưới CometBFT nên **không cần sửa gì** — đó là lý do ev-abci là con
đường tương thích phổ quát.

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

**Ánh xạ từng lời gọi executor → ABCI.** ev-node điều khiển vòng đời block qua 6
phương thức của `execution.Executor`; ev-abci ánh xạ chúng sang ABCI 2.0 như sau:

| Lời gọi của ev-node | ev-abci làm gì (ABCI 2.0) |
|---------------------|----------------------------|
| `InitChain` | Nạp genesis doc từ file, gọi `app.InitChain()`, khởi tạo validator set trong store |
| `GetTxs` | `mempool.ReapMaxBytesMaxGas()` — lấy tx từ mempool kiểu CometBFT (đã qua CheckTx, có tính gas) |
| `ExecuteTxs` | `PrepareProposal` → `ProcessProposal` → `FinalizeBlock` → `Commit`, trả về `AppHash` |
| `SetFinal` | Publish block events đã xếp hàng (NewBlock, Tx…) qua event bus kiểu CometBFT |
| `GetExecutionInfo` | Đọc `MaxGas` từ consensus params do app định nghĩa |
| `FilterTxs` | Lọc theo gas + kích thước + tính hợp lệ của từng tx |

Điểm cốt lõi ở `ExecuteTxs`: khác với việc gọi thẳng, ev-abci chạy **đủ nghi thức
đề xuất block của ABCI 2.0**. `PrepareProposal` cho phép ứng dụng **sắp xếp lại /
lọc** danh sách tx (hữu ích cho chống MEV, ưu tiên tx); `ProcessProposal` cho ứng
dụng **xác minh** block đề xuất trước khi thực thi; rồi `FinalizeBlock` thực thi
**cả lô tx trong một lời gọi** (thay cho BeginBlock/DeliverTx×N/EndBlock của ABCI
1.0), cuối cùng `Commit` chốt state và trả về `AppHash`.

**Các dịch vụ ev-abci gắn kèm (vì nó thay chỗ CometBFT).** Do đứng đúng vị trí
động cơ đồng thuận, ev-abci phải tái tạo những thứ CometBFT vốn cung cấp, tất cả
**chạy chung một tiến trình** với ev-node:

- **Mempool kiểu CometBFT** — có `CheckTx` (chạy ABCI để loại tx sai *trước* khi
  vào block), tính gas theo tx, ưu tiên, chống trùng (mempool_ids), chính sách
  eviction.
- **P2P tx gossip (libp2p)** — các node phát tán tx đang chờ cho nhau nên mempool
  đồng bộ giữa nhiều node; tách biệt với kênh gossip block của ev-node.
- **RPC tương thích CometBFT** — server JSON-RPC/WebSocket với `broadcast_tx_*`,
  `abci_query`, `block`, `tx`, `validators`, `status`… nên **Keplr, CosmJS, CLI
  Cosmos** dùng được ngay không cần client riêng.
- **Store + validator/signature providers** — lưu validator set theo height,
  consensus params, last-commit info và tạo chữ ký kiểu CometBFT cho
  sequencer/sync node (cần cho `LastCommitInfo` trong `FinalizeBlock`), đồng thời
  hỗ trợ **di trú (migration)** một chain CometBFT đang chạy sang Evolve.
- **Module Cosmos SDK thay thế** — bản staking dạng wrapper (no-op cho
  slashing/jailing/validator update vì ev-node không có validator set) và
  migration-manager để chuyển validator set → cấu hình sequencer tại một height
  xác định.

Tóm lại, ev-abci là **một lớp tương thích đầy đủ**: nó không chỉ "dịch một hàm"
mà giả lập gần như toàn bộ bề mặt CometBFT (mempool, P2P, RPC, event bus,
validator) để bất kỳ ABCI app nào chạy y nguyên. Đổi lại là **thêm một tầng** trên
đường đi giao dịch và kéo theo tập phụ thuộc lớn hơn. Đồ án đi hướng ngược lại:
CosmosExecutor hiện thực thẳng `execution.Executor` và gọi trực tiếp BaseApp trong
cùng tiến trình (ABCI 1.0: BeginBlock → DeliverTx×N → EndBlock → Commit), bỏ tầng
dịch và bỏ luôn những dịch vụ tương thích không cần cho use case blob-first —
xem so sánh chi tiết ở [cosmos-vs-evabci.md](cosmos-vs-evabci.md).

**Số liệu và dẫn chứng.**
- Mã nguồn ev-abci: <https://github.com/evstack/ev-abci> — cốt lõi ở
  `pkg/adapter/adapter.go` (executor + ABCI bridge), `pkg/adapter/providers.go`
  (chữ ký + validator), `pkg/rpc/` (RPC tương thích CometBFT), `pkg/p2p/` (tx
  gossip), `modules/` (staking wrapper + migration manager).
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

**Kinh tế token & phần thưởng (bộ ba `x/staking` + `x/mint` + `x/distribution`).**
Các chain PoS **chủ quyền** (Juno, Stargaze, Osmosis) chạy đúng mô hình Cosmos SDK
chuẩn:

- **`x/staking`** — delegator khoá (bond) token vào validator; **quyền biểu quyết ∝
  lượng stake**; validator gian lận/offline bị **slash** (cắt stake). Đây là nguồn an ninh.
- **`x/mint`** — **mint token mới mỗi block** theo lịch lạm phát (inflation rate thường
  tự điều chỉnh về một tỉ lệ bonded mục tiêu). Token mới sinh ra là "ngân sách phần thưởng".
- **`x/distribution`** — gom **lạm phát vừa mint + phí giao dịch** (trong `fee_collector`)
  rồi **chia** cho validator (hoa hồng) và delegator theo tỉ lệ stake; một phần trích vào
  **community pool** (community tax).

Nên vòng đời phần thưởng đúng như bạn mô tả: *mỗi block → mint lạm phát → distribute cho
người stake + cộng phí đã thu*. Người bảo vệ mạng (validator/delegator) được trả bằng
**token mới phát hành + phí**.

> **Ngoại lệ Neutron — KHÔNG mint để bảo mật.** Neutron là **consumer chain** (ICS/
> Replicated Security): validator là của **Cosmos Hub**, an ninh "thuê" từ ATOM stakers
> trên Hub, nên Neutron **không có `x/mint` lạm phát** cho an ninh. Phần thưởng đến từ
> **phí giao dịch + thưởng ICS**, dòng tiền đi về **DAO/treasury** của Neutron thay vì
> distribute kiểu lạm phát. Vì vậy "PoS + mint + distribute" **đúng với Juno/Stargaze/
> Osmosis nhưng không đúng với Neutron**.

> **Đối chiếu với đồ án:** rollup này **không có** `x/staking`/`x/mint`/`x/distribution` —
> không validator set, không lạm phát, chỉ **một sequencer**. Phí thu được **không**
> distribute kiểu Cosmos mà **sweep cuối block về một ví treasury** (xem
> [fee-economics §6d](fee-economics.md#sweep-phi-cuoi-block) — "Vì sao không dùng
> `x/distribution`"). An ninh không đến từ stake mà từ **DA + data availability sampling**.

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

**Optimistic vs ZK — cách hoạt động, ưu/nhược, thời gian & tiền bạc.**

*Optimistic (OP Stack, Arbitrum Orbit)* — **mặc định tin sequencer đúng**. Sequencer
đăng L2 block + state root lên L1 **không kèm bằng chứng**; ai cũng có thể **thách thức**
trong cửa sổ ~7 ngày bằng **fraud proof**. Nếu chứng minh state sai → L1 revert state root
đó và **phạt (slash) bond** của proposer.

- **Cách chốt:** hết cửa sổ 7 ngày không ai thách thức → coi là đúng → mới rút được về L1.
- **Thời gian:** rút "gốc" về L1 mất **~7 ngày**. Muốn nhanh phải qua **cầu thanh khoản
  (LP bridge)** — nhận ngay nhưng trả phí ~0,05–0,3%.
- **Tiền:** on-chain **rẻ** (không phải sinh proof đắt); chỉ tốn khi thực sự có tranh chấp.
- **Ưu:** đơn giản, rẻ khi post; **EVM-equivalent** dễ đạt → tương thích tool Ethereum tốt; trưởng thành nhất.
- **Nhược:** **finality-về-L1 chậm 7 ngày**; an ninh dựa trên **giả định có ≥1 watcher
  trung thực** kịp nộp fraud proof trong cửa sổ (giả định *liveness*); fraud proof tương tác, phức tạp.

*ZK (Polygon CDK / zkEVM)* — **chứng minh trước, không cần tin**. Mỗi batch kèm một
**validity proof** (zk-SNARK/STARK); **L1 verify proof ngay**. Verify đậu → state **final
lập tức**, không có cửa sổ chờ.

- **Cách chốt:** proof được L1 verify xong là final — không đợi 7 ngày.
- **Thời gian:** **sinh proof nặng** (phút → hàng giờ tùy hệ & kích thước batch), nhưng
  sau khi post + verify thì **rút nhanh** (~phút–giờ), không có 7 ngày.
- **Tiền:** **đắt hơn** — chi phí prover (phần cứng/điện chuyên dụng) + gas verify proof
  trên L1; bù lại **amortize** trên nhiều tx trong batch.
- **Ưu:** **finality nhanh**, **trustless** (không cần watcher trung thực), rút nhanh.
- **Nhược:** chi phí + độ phức tạp prover cao; **zkEVM khó làm** (EVM-equivalence khó); độ trễ sinh proof.

| Trục | Optimistic | ZK |
|------|-----------|----|
| Bằng chứng | fraud proof **khi bị thách thức** | validity proof **mọi batch** |
| Rút về L1 (native) | **~7 ngày** | **~phút–giờ** |
| Chi phí on-chain | thấp (post trần) | cao hơn (sinh + verify proof) |
| Giả định an ninh | có watcher trung thực trong cửa sổ | chỉ cần L1 verify đúng (toán học) |
| Độ khó xây dựng | dễ hơn (EVM-equiv) | khó (zkEVM) |

> **Đồ án khác cả hai:** rollup này **không neo L1**, nên **không có cửa sổ thách thức
> lẫn không sinh validity proof**. State ban đầu là **soft-final ~2s** (do sequencer sắp
> thứ tự), rồi **DA-final ~14s** khi data lên Celestia và được verify qua **data
> availability sampling**. Đánh đổi: an ninh **không** kế thừa từ Ethereum mà đến từ tính
> **khả dụng dữ liệu (DA)** + chủ quyền tự quản luật — rẻ và nhanh hơn, nhưng mô hình tin
> cậy khác (không có "L1 tối cao" đứng ra revert/quyết toán). Không rút chậm 7 ngày, cũng
> không gánh chi phí prover.

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

## 6. Mạng sequencer dùng chung (Astria & Espresso)

**Là gì.** **Shared sequencer** (sequencer dùng chung): một mạng độc lập chuyên
làm **một việc duy nhất là sắp thứ tự** giao dịch cho **nhiều rollup** cùng lúc,
rồi trả lại luồng đã sắp cho từng rollup **tự thực thi**. Điểm chung của cả nhóm:
**tách tầng sắp thứ tự (ordering/consensus) khỏi tầng thực thi (execution)** — hai
việc mà một chain nguyên khối gộp làm một. Astria và Espresso là hai đại diện
tiêu biểu; khác nhau chủ yếu ở **động cơ đồng thuận** dùng để chốt thứ tự.

Vì sao có mô hình này: nếu mỗi rollup tự chạy **một sequencer đơn** (như ev-node,
và như đồ án hiện nay) thì (1) người vận hành phải tự đảm bảo tính sống
(liveness) và chống kiểm duyệt, (2) các rollup **không chia sẻ một thứ tự chung**
nên không thể có **giao dịch nguyên tử xuyên rollup** (atomic cross-rollup): một
lệnh vừa mua ở rollup A vừa bán ở rollup B không thể "cùng thành công hoặc cùng
thất bại". Shared sequencer giải đúng hai điểm đó — đổi lại rollup phải **tin và
phụ thuộc** vào một mạng bên ngoài cho khâu sống còn là thứ tự giao dịch.

### 6.1 Astria — sequencer là một chain CometBFT, đẩy dữ liệu lên Celestia

**Nguyên lý.** Bản thân **Astria Sequencer là một blockchain riêng chạy
CometBFT** (đồng thuận Tendermint BFT, tập validator bỏ phiếu, chốt khi đạt trên
2/3 quyền biểu quyết — instant finality, không reorg ở tầng sequencer). Nhưng
chain này **cố ý "câm" về thực thi**: nó **không chạy máy ảo, không giữ state của
rollup**, chỉ nhận các gói tx của rollup như **dữ liệu mờ (opaque bytes)**, đóng
gói theo `rollup_id` và **cam kết một thứ tự**. Toàn bộ việc "tính toán tx nghĩa
là gì" để dành cho rollup. Bốn thành phần chạy quanh nó:

- **Composer** — chạy cạnh mỗi rollup, gom tx của rollup, bọc thành *sequence
  action* gắn `rollup_id`, gửi lên Astria Sequencer.
- **Sequencer (app CometBFT)** — chạy đồng thuận, tạo block chứa các sequence
  action đã sắp thứ tự. Đây là nơi phát sinh **soft commitment** (cam kết mềm:
  thứ tự đã được validator Astria chốt, rất nhanh nhưng chưa lên DA).
- **DA (Celestia)** — sau khi block sequencer chốt, dữ liệu được **công bố lên
  Celestia**. Khi blob đã sẵn có trên DA thì thành **firm commitment** (cam kết
  chắc: dữ liệu đã khả dụng, ai cũng tái dựng được).
- **Conductor** — chạy cạnh mỗi rollup, **đọc** block đã sắp (soft từ sequencer,
  firm từ Celestia), lọc đúng `rollup_id` của mình, rồi **đẩy luồng tx đó vào node
  thực thi của rollup** qua một Execution API (tương tự Engine API của Ethereum).

```
  Astria — hai mức cam kết (soft → firm)
  ──────────────────────────────────────
  Rollup A ─Composer─┐
  Rollup B ─Composer─┼─► Astria Sequencer ──► soft commit (CometBFT >2/3)
  Rollup C ─Composer─┘        (CometBFT)            │
                                                    ▼
                                             Celestia (DA) ──► firm commit
                                                    │
                    Conductor (mỗi rollup) ◄────────┘ đọc block đã sắp
                          │  lọc theo rollup_id
                          ▼
                    Node THỰC THI của rollup  (Astria KHÔNG làm khâu này)
```

Nhờ **mọi rollup chia sẻ đúng một chuỗi thứ tự của Astria**, có thể đặt lệnh
nguyên tử xuyên rollup: các action trong cùng một block sequencer hoặc cùng thành
công hoặc cùng bị loại. Astria cũng hướng tới **sắp thứ tự trung lập/không kiểm
duyệt** ở tầng chung thay vì để một sequencer đơn tùy ý.

### 6.2 Espresso — đồng thuận HotShot (họ HotStuff), tối ưu cho tập validator lớn

**Nguyên lý.** Espresso cũng là shared sequencer, nhưng động cơ đồng thuận là
**HotShot** — một biến thể của **HotStuff** (BFT do leader dẫn dắt, pipelined).
Espresso chọn HotStuff thay vì Tendermint/CometBFT vì mục tiêu **phi tập trung
hóa tập validator ở quy mô lớn** (nhiều bên cùng vận hành sequencer) mà vẫn giữ
thông lượng cao và độ trễ thấp. Điểm cốt lõi của HotStuff giúp điều đó:

- **Giao tiếp tuyến tính O(n)** — mỗi vòng, validator gửi phiếu **về một leader**;
  leader **gộp phiếu thành một Quorum Certificate (QC)** bằng chữ ký ngưỡng
  (threshold signature) rồi phát lại. So với Tendermint kiểu gossip O(n²), chi phí
  truyền thông không bùng nổ khi số validator tăng → **mở rộng được ra tập lớn**.
- **Pipelined (gối vòng) + xoay leader mỗi view** — các pha của những block liên
  tiếp chồng lên nhau (một QC vừa "chốt" block này vừa "đề xuất" block sau), nên
  thông lượng cao; leader đổi mỗi view nên không một node nào giữ đặc quyền lâu.
- **Optimistic responsiveness** — mạng chạy **theo tốc độ mạng thật** (độ trễ
  thực tế) chứ không phải chờ hết một timeout cố định như Tendermint; khi mạng
  khỏe, block chốt nhanh bám sát độ trễ vật lý.

Espresso còn **tách riêng đồng thuận và sẵn sàng dữ liệu**: HotShot lo *thứ tự +
finality*, còn một tầng DA riêng (thiết kế nhiều lớp, Espresso gọi là *Tiramisu*)
lo *phát tán dữ liệu*. Nó cấp cho rollup một **HotShot confirmation** — một xác
nhận finality nhanh (cỡ vài giây) mà rollup có thể tin để coi block là chắc,
**không lo sequencer tự đảo (reorg)**. Espresso được tích hợp cho các rollup hệ
Ethereum (OP Stack, Arbitrum Orbit…) như một "lớp xác nhận" đặt trước tầng quyết
toán chậm trên L1.

```
  Espresso — HotShot (HotStuff) tách consensus ↔ DA
  ─────────────────────────────────────────────────
  Rollup A ─┐        HotShot (leader gộp phiếu → QC, O(n), pipelined)
  Rollup B ─┼─► ordering ─► HotShot confirmation (finality nhanh, ~giây)
  Rollup C ─┘                 │
                              ├─► tầng DA riêng (Tiramisu) phát tán dữ liệu
                              ▼
                    node THỰC THI của mỗi rollup (Espresso KHÔNG làm)
```

### 6.3 Vì sao khác động cơ: HotStuff (Espresso) vs CometBFT/Tendermint (Astria)

| Trục | CometBFT/Tendermint (Astria) | HotStuff/HotShot (Espresso) |
|------|------------------------------|-----------------------------|
| Kiểu đồng thuận | BFT gossip, xoay proposer theo vòng | BFT do **leader** dẫn, xoay leader **mỗi view** |
| Chi phí truyền thông | ~**O(n²)** (validator nói với nhau) | ~**O(n)** (leader gộp phiếu → QC bằng threshold sig) |
| Mở rộng tập validator | tốt ở quy mô vừa (hàng chục–hơn trăm) | thiết kế cho **tập lớn hơn**, phi tập trung sâu |
| Đáp ứng theo mạng | tiến theo **timeout cố định** (Δ) | **optimistic responsiveness** — chạy theo tốc độ mạng thật |
| Finality | tức thì 1 block (>2/3) | tức thì theo QC, pipelined |
| Trong sản phẩm | Astria Sequencer là **một chain CometBFT** | Espresso dùng **HotShot** làm động cơ |

> **Tinh thần chung của cả hai.** Đều là *lớp sắp thứ tự dùng chung* — chỉ chốt
> **thứ tự** rồi đẩy dữ liệu xuống một DA, còn **thực thi giao/lệnh là việc của
> từng rollup**. Khác biệt kỹ thuật nằm ở động cơ đồng thuận (CometBFT vs
> HotStuff) và cách tổ chức DA, dẫn tới khác nhau về mức phi tập trung và đặc tính
> độ trễ, nhưng vai trò trong ngăn xếp là như nhau.

**Số liệu và dẫn chứng.**
- Astria: <https://astria.org> · Tài liệu: <https://docs.astria.org> — kiến trúc
  Composer / Sequencer (CometBFT) / Conductor, hai mức *soft* và *firm commitment*,
  công bố dữ liệu lên Celestia.
- Espresso: <https://docs.espressosys.com> — đồng thuận **HotShot**; giấy/đặc tả
  HotStuff gốc: <https://arxiv.org/abs/1803.05069>.
- Đặc tả CometBFT (để đối chiếu động cơ của Astria):
  <https://docs.cometbft.com/v1.0/spec/consensus/>.
- Trục đối chiếu: cả Astria lẫn Espresso làm việc ở **tầng sequencer**, đồ án ở
  **tầng thực thi** — bổ trợ chứ không trùng. Hướng phát triển multi-sequencer của
  đồ án là nơi hai ý tưởng gặp nhau.

**So với đồ án.** Cả hai **không cung cấp lớp thực thi** — đúng phần đồ án đóng
góp (CosmosExecutor + CosmWasm). Đồ án hiện dùng **single sequencer** của ev-node
(một bên sắp thứ tự), nên chưa có finality dùng chung hay nguyên tử xuyên rollup;
đổi lại vận hành đơn giản, không phụ thuộc một mạng đồng thuận ngoài. Nếu sau này
gắn multi-sequencer, ev-node về nguyên tắc có thể đặt bên dưới một shared sequencer
kiểu Astria/Espresso, còn tầng thực thi CosmWasm của đồ án giữ nguyên.

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
| Astria | Sequencer dùng chung; **chain CometBFT** chỉ ordering → Celestia | soft/firm commitment | Đồ án đóng góp tầng thực thi |
| Espresso | Sequencer dùng chung; đồng thuận **HotShot (HotStuff)** | O(n), tập validator lớn | Đồ án đóng góp tầng thực thi |
| Celestia (đồ án dùng) | DA + DAS theo namespace | block ~6s | (mốc) |
| EigenDA | Restaking + chứng thực operator | an ninh từ ETH restake | DA thay thế tiềm năng |
| Avail | KZG + DAS | light client lấy mẫu | DA thay thế tiềm năng |
| EIP-4844 | Blob tạm thời của Ethereum | 128 KiB/blob, giữ ~18 ngày | DA chuyên dụng thường rẻ hơn |

---

## 10. So sánh quy trình triển khai, phí giao dịch (USD) và thời gian quyết toán

Mục này gom ba trục định lượng hay bị hỏi nhất khi bảo vệ: (a) **số bước** để đưa
một ứng dụng lên Evolve, (b) **phí một giao dịch `increment`** quy ra USD, và
(c) **thời gian xác nhận mềm / quyết toán** của từng sản phẩm. Số của đồ án lấy
từ phép đo Chương 4 (tóm tắt ở mục 0); số của bên thứ ba luôn kèm **công thức**
để tự kiểm chứng giá token tại thời điểm viết, vì các con số này biến động theo
thị trường và bản nâng cấp.

### 10.1 Số bước đưa một ứng dụng lên Evolve

Evolve có hai con đường chính thức, đều thao tác ở **mức operator** của chain:
ignite app `evolve` để dựng/chuyển một chain
([docs.ev.xyz/guides/cometbft-to-evolve](https://docs.ev.xyz/guides/cometbft-to-evolve))
và adapter `ev-abci` để migrate một chain CometBFT đang chạy
([docs.ev.xyz/guides/migrating-to-ev-abci](https://docs.ev.xyz/guides/migrating-to-ev-abci)).
SDK của đồ án **tách vai trò operator khỏi lập trình viên dApp**: chain được dựng
sẵn một lần bởi `evcosmos` + `cosmos-exec`, lập trình viên chỉ làm việc qua một
thư viện Go có kiểu tĩnh.

| Tiêu chí | ignite `evolve` | `ev-abci` (migrate) | SDK `cosmoswasm` (đồ án) |
|----------|-----------------|---------------------|--------------------------|
| Vai trò người thực hiện | Operator | Operator | Lập trình viên dApp |
| Số bước | 4 lệnh CLI | **13 bước / 6 giai đoạn** | **4 lời gọi SDK** |
| Sửa mã nguồn `app.go` | Có (tự động hoá) | Có (thủ công) | Không |
| Upgrade handler | Không | Có | Không |
| Governance proposal + vote | Không | **Có (chờ quorum)** | Không |
| Build lại binary | Có | Có | Không |
| Trễ tới khi giao dịch được | Phút | Giờ/ngày (chờ quorum) | **< 10 s** (deploy + tương tác) |

Con đường `ev-abci` đầy đủ gồm **13 bước trên 6 giai đoạn**: sửa `app.go` để nhúng
migration-manager keeper và thay module staking bằng wrapper (3 bước), viết
upgrade handler (1 bước), **đệ trình software-upgrade proposal + chờ validator bỏ
phiếu đủ quorum** rồi gửi `MsgMigrateToEvolve` (3 bước), sửa entrypoint để dùng
start-handler của ev-abci và **build lại binary** (3 bước), chạy `evolve-migrate`,
rồi khởi động node mới (3 bước). Hai khâu nặng nhất là *can thiệp mã nguồn* (phải
biên dịch lại) và *governance on-chain* (độ trễ tính bằng giờ/ngày). Con đường
ignite gọn hơn (`install → add → init → start`) nhưng phục vụ *dựng chain* và tài
liệu hiện vẫn đang cập nhật. Ngược lại, để đi từ con số không tới một hợp đồng đã
triển khai và đang tương tác, lập trình viên dùng SDK đồ án chỉ cần **4 lời gọi
tuần tự** — `BuildStoreTx` → `BuildInstantiateTx` → `BuildExecuteTx` →
`QuerySmart` (mỗi lời gọi kèm một `SubmitTxBytes`/`WaitTxResult` thuần HTTP),
**không** sửa `app.go`, **không** upgrade handler, **không** governance, **không**
build lại binary.

> **Lưu ý công bằng.** Đây không phải so cùng một tác vụ tuyệt đối: `ev-abci`
> giải bài toán *migrate cả một chain CometBFT* (mạnh ở tương thích ecosystem —
> CometBFT RPC, Keplr/CosmJS, P2P tx gossip), còn SDK đồ án giải bài toán *triển
> khai và tương tác một dApp CosmWasm* trên rollup đã dựng. Lợi thế của đồ án là
> **gộp gánh nặng vận hành về phía operator một lần duy nhất**, để lập trình viên
> dApp chỉ còn đối mặt một API typed gọn nhẹ — đổi lại là hy sinh tính tương thích
> ecosystem chung mà `ev-abci` cung cấp. Chi tiết: [cosmos-vs-evabci.md](cosmos-vs-evabci.md).

### 10.2 Phí một giao dịch `increment` (quy ra USD)

Cần tách bạch hai loại phí:

- **Phí thực thi (gas)** — trả bằng **token gốc của chain**.
- **Phí dữ liệu (DA)** — chỉ rollup mô-đun mới có; trả Celestia bằng **TIA**.

Công thức quy đổi USD (dùng được cho mọi cột trong bảng dưới):

```
Phí gas (USD)  = gas_used × gas_price[token/gas] × giá_token[USD/token]
Phí DA  (USD)  = bytes_phân_bổ × đơn_giá_DA[TIA/byte] × giá_TIA[USD/TIA]
```

Số đo cho **đồ án** (tx `increment`):

- `gas_used` ≈ **126.000** — *proxy* lấy từ phép đo execute cw20 ở Chương 4;
  increment counter đơn giản hơn nên cùng bậc (~110–130k). **Nên đo lại** bằng
  chính contract của bạn (xem ghi chú cuối mục).
- `gas_price` = **0,000001 ustake/gas** (`MIN_GAS_PRICE` mặc định) → phí gas =
  **0,126 ustake**.
- `ustake` là **token nội bộ của rollup, không niêm yết trên sàn** → quy đổi USD
  ≈ **\$0**. Operator tự đặt giá phí thực thi (có thể để 0).
- Phí DA biên cho tx ~427 byte ≈ **0,0000427 TIA** (mô phỏng theo policy); chi phí
  DA *thật* được khấu hao — ~330 utia/blob cho 7–9 block (mục 0).

Bảng so sánh phí một giao dịch `increment` (điền giá token spot tại thời điểm
viết; cột USD của bên thứ ba **cần kiểm chứng**):

| Sản phẩm | Token phí | `gas_used` (~increment) | Đơn giá gas | Phí gas (token) | Phí gas (USD) | Phí DA thêm |
|----------|-----------|-------------------------|-------------|-----------------|---------------|-------------|
| **Đồ án** (sovereign CosmWasm/Celestia) | `ustake` (nội bộ) | ~126k | 0,000001 | 0,126 ustake | **≈ \$0** (token không niêm yết) | ~0,0000427 TIA = 0,0000427 × P_TIA |
| Juno (L1 CosmWasm) | JUNO | ~140k | ~0,075 ujuno¹ | ~0,0105 JUNO | 0,0105 × P_JUNO | — (data on-chain) |
| Neutron (L1 CosmWasm) | NTRN | ~140k | ~0,0053 untrn¹ | ~0,00074 NTRN | 0,00074 × P_NTRN | — |
| Osmosis (L1 CosmWasm) | OSMO | ~140k | ~0,0025 uosmo¹ | ~0,00035 OSMO | 0,00035 × P_OSMO | — |
| OP Stack / Arbitrum (EVM rollup) | ETH | ~45k (L2)² | gas L2 + phí dữ liệu L1 | — | thường \$0,001–0,01² | đã gộp trong phí L1 |
| Ethereum L1 (tham chiếu) | ETH | ~43k | base_fee (gwei) | 43k × base_fee | 43k × base_fee × 1e-9 × P_ETH | — |

¹ Đơn giá gas tối thiểu của các mạng L1 thay đổi theo cấu hình và bản nâng cấp —
**bắt buộc kiểm chứng** tại tài liệu mỗi mạng (mục 12) và điền giá token spot.
² Phí EVM rollup gồm phí thực thi L2 (rất nhỏ) cộng phí đăng dữ liệu lên L1
(thường chiếm phần lớn) — phụ thuộc giá blob EIP-4844 và độ bận của Ethereum;
nên trích từ một explorer (vd l2fees.info) tại thời điểm viết.

**Nhận xét.** Điểm mấu chốt không nằm ở con số tuyệt đối (gas của một `increment`
ở mọi nền đều nhỏ) mà ở **bản chất chi phí**. Rollup của đồ án **tự phát hành
token phí** nên operator kiểm soát hoàn toàn phí thực thi (đặt ≈ 0); chi phí biên
thực duy nhất là **phí DA trả Celestia bằng TIA**, lại được khấu hao nhờ gộp 7–9
header/blob (mục 0). Chain CosmWasm truyền thống (Juno/Neutron/Osmosis) trả phí
gas bằng token **có giá thị trường** và lưu **mọi byte on-chain** — chi phí tăng
tuyến tính theo kích thước dữ liệu, đối lập với hàng "tiết kiệm ~90%" của mô hình
blob-first.

> **Cách lấy `gas_used` chính xác cho `increment` của bạn.** Chạy example
> `examples/my-counter`, rồi: (1) đọc trường `gas_used` trong phản hồi
> `GET /tx/result?hash=...` của tx execute; hoặc (2) xem `/tx/estimate`. Nhân
> `gas_used × MIN_GAS_PRICE` (mặc định 0,000001 ustake) ra phí gas; phí DA lấy
> từ `scripts/measure_da_fees.mjs` (mục 0). **Tránh** suy ra từ
> `gas_wanted`/`GAS_LIMIT` (mặc định **80.000.000** trong example) — đó là *trần*
> chứ không phải gas thực dùng.

### 10.3 Thời gian xác nhận mềm (soft) và quyết toán (finalized)

| Sản phẩm | Xác nhận mềm (soft) | Quyết toán (finalized) | Cơ chế |
|----------|---------------------|------------------------|--------|
| **Đồ án** (sovereign CosmWasm/Celestia) | **~2 s** (1 block sequencer) | **~14 s** (~7 block — blob lên Celestia + DAS) | sequencer cam kết → DA công bố |
| ev-abci trên ev-node | ~ block time (cấu hình) | ~ chu kỳ submit DA (giây–chục giây) | cùng khung ev-node |
| Juno / Neutron / Osmosis (CometBFT) | = quyết toán | **1 block (~1–6 s)** | BFT >2/3 phiếu, không reorg (instant final) |
| Dymension RollApp | ~ block time | sau khi state cam kết & dàn xếp trên hub | hub-and-spoke |
| OP Stack / Arbitrum (optimistic) | ~1–2 s (sequencer) | **~7 ngày** (hết cửa sổ thách thức) | fraud proof |
| Polygon CDK (zk) | ~1–2 s (sequencer) | ~vài phút–1 giờ (sau khi proof verify trên L1) | validity proof |

**Đọc bảng.** Đồ án nằm giữa hai thái cực: **nhanh hơn nhiều** so với rollup
optimistic (14 giây so với 7 ngày để quyết toán) vì không neo L1 và không có cửa
sổ thách thức; **chậm hơn** chain CometBFT truyền thống ở mức quyết toán (14 s so
với 1 block) vì tách finality thành hai mức — đổi lại được tính sẵn sàng dữ liệu
mô-đun và chi phí lưu trữ thấp. Ba mức trễ của đồ án (đọc mili-giây → ghi ~2 s →
DA-final ~14 s) tương ứng ba mức bảo đảm, đúng mô hình thiết kế ở Chương 4.

### 10.4 Khi tính cả blob-first: chi phí lưu dữ liệu lớn

Một giao dịch `increment` quá nhỏ để blob-first lộ ưu thế — phần data của nó chỉ
vài trăm byte. **Blob-first chỉ thắng đậm khi ứng dụng phải gắn dữ liệu lớn vào
chain** (telemetry game, ảnh/NFT, snapshot, log IoT/audit). Vì vậy phải so trên
một tác vụ khác: *lưu N byte dữ liệu có thể xác minh được on-chain*.

Có hai cách:

- **Nhúng on-chain** (embed thẳng vào state WASM): mọi node lưu mọi byte **vĩnh
  viễn**, gas tỉ lệ kích thước.
- **Blob-first** (đồ án): đẩy data lên Celestia (trả phí DA bằng TIA), chỉ neo
  một **commitment ~32 byte + DA height (~40 byte)** on-chain.

Số minh hoạ cho **1 MB** (đo qua example `game-telemetry` / `/tx/simulate`; rate
DA hiệu dụng `1,71·10⁻⁷ TIA/byte` ở mục 0/1c):

| Cách lưu 1 MB dữ liệu verifiable | Gas on-chain | Footprint on-chain | Phí DA (Celestia) | Đặc tính |
|----------------------------------|--------------|--------------------|-------------------|----------|
| Nhúng on-chain (embed WASM state) | ~41 triệu gas | 1 MB, **vĩnh viễn** ở mọi node | 0 | state phình mãi; dễ vượt block gas limit |
| **Blob-first (đồ án)** | ~267k gas (cố định) | **~40 byte** (commitment+height) | ~0,045 TIA (hoá đơn thật; xem 10.5) | data off-chain, on-chain chỉ neo cam kết |
| **Chênh lệch** | **~99% ít gas hơn** (chỉ gas) | **~26.000× nhỏ hơn** | + 0,045 TIA | tiết kiệm **~90% tổng** khi cộng cả phí DA |

> **Vì sao hai con số 99% và 90%.** "~99%" là **chỉ phần gas** (267k so với 41
> triệu, getting-started). Khi cộng thêm **phí DA Celestia** mà blob-first phải
> trả (data không biến mất, chỉ chuyển sang DA rẻ hơn 1–2 bậc), tỉ lệ tiết kiệm
> *tổng chi phí* rút về **~90%** — đây là con số chuẩn của báo cáo (mục 0 và
> thesis-qa). Blob-first **không làm data miễn phí**; nó **dời chi phí từ gas
> on-chain đắt sang DA Celestia rẻ**, đồng thời giữ tính xác minh nhờ commitment.

**Đối chiếu khả năng blob-first giữa các sản phẩm.** Đây là trục mà đồ án có lợi
thế rõ, vì nhiều nền không có lớp DA gốc để dời data ra:

| Sản phẩm | Cách lưu dữ liệu lớn verifiable | Chi phí | Thời gian lưu giữ |
|----------|--------------------------------|---------|-------------------|
| **Đồ án** (sovereign CosmWasm/Celestia) | blob-first: blob Celestia + cam kết 32B on-chain | phí DA TIA (rẻ) + ~40B on-chain | theo retention Celestia, có cam kết neo on-chain |
| Juno / Neutron / Osmosis | **không có DA gốc** → buộc nhúng on-chain, hoặc đẩy ra IPFS/DB ngoài | gas đắt (token có giá) + state bloat vĩnh viễn | mọi node, vĩnh viễn (đắt) |
| OP Stack / Arbitrum | EIP-4844 blob trên Ethereum | phí blob L1 | **~18 ngày** rồi Ethereum xoá |
| Polygon CDK | EIP-4844 blob / DA tuỳ chọn | phí blob/DA | ~18 ngày (nếu dùng 4844) |

Lưu ý công bằng: blob-first của đồ án và blob EIP-4844 khác nhau ở **thời gian lưu
giữ** — Celestia giữ dữ liệu theo chính sách DA (dài hơn ~18 ngày của 4844), nên
với dữ liệu cần truy xuất lâu dài, mô hình của đồ án phù hợp hơn; ngược lại 4844
gắn sẵn an ninh Ethereum. Khi trích số "~90%" vào báo cáo, luôn nói rõ **đã gồm
phí DA** để tránh bị phản biện là bỏ sót chi phí Celestia (xem cách đo ở mục 0 và
[fee-economics.md](fee-economics.md) §1c).

### 10.5 Giá lưu blob-first so với các lớp DA / lưu trữ khác

Lưu ý nền: giá lưu blob-first của đồ án **chính là giá Celestia** — đồ án không tự
làm DA rẻ hơn, mà (a) đóng gói blob-first thành vài lời gọi SDK, và (b) chọn
Celestia vốn rẻ hơn các lựa chọn khác 1–3 bậc. Mục này so **chi phí lưu 1 MB dữ
liệu** giữa Celestia và các DA/lưu trữ thay thế.

**Giá Celestia thật (đo trên private net của đồ án).** Celestia tính gas theo
*share* 512 byte, không tuyến tính tuyệt đối theo byte (fee-economics §1c):

```
gas/byte ≈ 512 × 8 / 482 ≈ 8,5 gas/byte         (GasPerBlobByte = 8)
giá/byte  = 8,5 × gas_price[utia/gas]
1 MB     ≈ 1.048.576 × 8,5 × 0,005 utia ≈ 44.560 utia ≈ 0,045 TIA   (gas_price đo = 0,005 utia/gas)
```

→ **~0,045 TIA / MB** (lý thuyết share-based; *biên đo thật* ~0,05 utia/byte
trong fee-economics §1c-D đẩy lên ~**0,05 TIA/MB** — cùng bậc, chênh do tx-size
cost + làm tròn share). Lưu ý: rate dashboard `1,71·10⁻⁷ TIA/byte` (→ 0,18
TIA/MB) là rate **đo trên blob nhỏ** nên *gánh phần phí cố định* — nó over-count
~5× cho blob 1 MB; dùng số share-based ở trên cho dữ liệu lớn. Trên mainnet/mocha,
thay `gas_price` bằng `NetworkMinGasPrice` của mạng đó.

**Bảng so chi phí lưu 1 MB** (đơn giá thô là số đo/công bố; cột USD điền giá token
spot — tất cả giá token bên thứ ba **cần kiểm chứng**):

| Phương án lưu 1 MB | Đơn giá thô | Lượng cho 1 MB | Công thức USD | Ví dụ USD (giá giả định¹) |
|--------------------|-------------|----------------|----------------|----------------------------|
| **Đồ án = Celestia DA** | ~8,5 gas/byte × 0,005 utia/gas (đo) | **~0,045 TIA** | 0,045 × P_TIA | **~\$0,018** @ TIA \$0,395 |
| Avail DA | KZG + DAS, giá theo AVAIL (kiểm chứng) | ~ cùng bậc Celestia | × P_AVAIL | cùng bậc (kiểm chứng) |
| EigenDA | định giá theo throughput (kiểm chứng) | — | — | thường ≤ Celestia (kiểm chứng) |
| EIP-4844 blob (Ethereum) | 131.072 blob-gas/blob, 128 KiB/blob | 8 blob = 1.048.576 blob-gas | 8×131072 × blob_base_fee[gwei] × 1e-9 × P_ETH | **~\$0 (rảnh) → ~\$1,8 (1 gwei)** @ ETH \$1.723² |
| Ethereum calldata (L1, tham chiếu) | 40 gas/byte (sàn EIP-7623) | ~41,9 triệu gas | 41,9M × base_fee[gwei] × 1e-9 × P_ETH | **~\$720/MB** @ 10 gwei, ETH \$1.723 |
| Cosmos on-chain state (Juno…) | ~30 gas/byte (KVStore write) | ~31,4 triệu gas | 31,4M × 0,075 ujuno × P_JUNO | **~\$0,07/MB** @ JUNO \$0,0295 **+ bloat vĩnh viễn³** |
| S3 / IPFS (tham chiếu — **KHÔNG verifiable phi tập trung**) | ~\$0,023/GB/tháng (S3) | — | — | ~\$0,00002/MB/tháng (không phải DA) |

¹ Giá token \$0,395/TIA, \$1.723/ETH, \$0,0295/JUNO là **giá tại thời điểm viết** —
đo lại khi giá đổi; cột USD chỉ minh hoạ bậc độ lớn.
² Phí blob EIP-4844 biến động rất mạnh theo độ bận Ethereum: gần \$0 khi rảnh,
vài \$/MB khi nghẽn; trích từ explorer (vd blobscan.com) đúng thời điểm.
³ Chi phí thật của lưu on-chain **không phải khoản gas một lần** mà là **lưu trữ
vĩnh viễn ở MỌI node** — gas chỉ là cái giá vào cửa; state phình mãi là gánh nặng
dài hạn không phương án DA nào mắc phải.

**Đọc bảng.** Ở cùng tác vụ "lưu 1 MB có thể xác minh", Celestia (đồ án) rẻ hơn
**~3–4 bậc** so với nhúng vào state on-chain (Ethereum calldata ~\$500, Cosmos
state ~\$0,7 + bloat) và **ngang hoặc rẻ hơn** EIP-4844 (blob Ethereum đắt khi
nghẽn và chỉ giữ ~18 ngày). EigenDA/Avail cùng họ DA chuyên dụng nên cùng bậc với
Celestia — đây là lý do kiến trúc đồ án **trừu tượng hoá lớp DA** để có thể cắm
sang chúng (mục 7). Điểm mạnh định lượng của đồ án không phải "rẻ hơn Celestia" mà
là **đưa được mô hình chi phí rẻ-nhất-hạng đó vào tay lập trình viên CosmWasm chỉ
bằng vài lời gọi SDK**, điều không nền nào ở trên cung cấp sẵn.

> **Cách lấy số thật của bạn.** (1) Phí DA Celestia: chạy `scripts/measure_da_fees.mjs`
> trên blob lớn (gửi 1 MB qua `BlobClient.SubmitBlob`) để đọc `fee.amount` của PFB
> thật. (2) Gas nhúng on-chain để đối chứng: `/tx/simulate` một tx nhúng 1 MB vào
> WASM → `gas_used`. (3) Quy USD bằng giá token spot. Đừng trộn rate dashboard
> (1,71·10⁻⁷) với số share-based — nêu rõ bạn dùng cái nào.

---

### 10.6 Case study đo thật: lưu `makeReplay(300 KiB)` blob-first vs nhúng on-chain

Mục 10.5 dùng 1 MB làm ví dụ. Mục này chốt một **payload cụ thể, có thật trong
code** — `makeReplay(300 * 1024)` ở
[examples/game-telemetry/main.go:154](../examples/game-telemetry/main.go#L154):
một replay trận đấu **307.200 byte**, được `ChunkBlob(replay, 64 KiB)` cắt thành
**5 chunk** rồi `SubmitBatch` đẩy lên Celestia, chỉ neo **một Merkle root (~32B) +
DA height** on-chain qua `record_replay`.

**Quan trọng — batch = MỘT PFB.** `SubmitBatch` gọi `Blob.Submit` *một lần* với cả
5 chunk ([blob.go:214](../blob.go#L214)) → Celestia bọc thành **1 `MsgPayForBlobs`
chứa 5 blob** → phần phí cố định (~65k gas PFB) chỉ tính **1 lần**, không phải ×5.

#### Số đo thật làm đầu vào (KHÔNG phải giả định)

Đo bằng `scripts/measure_da_fees.mjs` trên **private DA net của đồ án**
(`131.153.224.169:26757`, `chain_id=private`), quét **99 PFB / 303 block Celestia**
(heights 610.479–611.979, đo 2026-06):

| Đại lượng đo | Giá trị |
|---|---|
| `gas_price` node áp (thực) | **0,005 utia/gas** (mạng hiện tại; docs cũ ghi 0,004 — đo lại khi đổi mạng) |
| Phí cố định mỗi PFB (hồi quy) | **~357 utia** (≈ 71k gas × 0,005) |
| Chi phí biên mỗi byte (hồi quy) | **~0,0338 utia/byte = 3,38·10⁻⁸ TIA/byte** |
| Rate hiệu dụng (Σfee/Σbyte) | 0,0847 utia/byte = 8,47·10⁻⁸ TIA/byte (*over-count blob lớn*) |
| Phí mỗi PFB (TB / dải) | 594 utia / 336–6.595 utia |

> Lệnh tái tạo: `node scripts/measure_da_fees.mjs http://131.153.224.169:26757 610479 611979`.
> Rate hiệu dụng `8,47·10⁻⁸` **gánh phí cố định** nên over-count ~2,5× cho blob 300 KiB —
> dùng hồi quy `fixed + marginal·bytes` hoặc share-based cho dữ liệu lớn (mục 10.5).

#### Phí DA blob-first cho 307.200 byte — ĐO THẬT trên chuỗi

Đã submit đúng `makeReplay(300 KiB)` qua example (`SubmitBatch` → 1 PFB / 5 blob)
và đọc PFB của chính nó bằng `measure_da_fees.mjs`:

| Đại lượng (PFB thật) | Giá trị đo |
|---|---|
| DA height | **612.074** |
| tx hash PFB | **`F19C468A906D82FA…`** |
| blob bytes (Σ `blob_sizes`) | **307.200** |
| `gas_limit` | **2.691.748 gas** |
| **fee thật** | **13.459 utia = 0,01346 TIA** |
| utia/byte (blob đơn lẻ này) | 0,0438 |

Đối chiếu với hai cách ước tính ở mục 10.5 — **số đo khớp share-based gần tuyệt đối**:

```
shares     = 1 + ⌈(307200 − 478)/482⌉ = 638 shares
gas (lý thuyết) = 638 × 512 × 8 + ~65.000 ≈ 2.678.000   ≈ gas_limit đo 2.691.748 ✓
① Share-based : 2.678.000 × 0,005 utia/gas  ≈ 13.400 utia ≈ 0,0134 TIA   ← khớp đo
② ĐO THẬT     : PFB 612074                  = 13.459 utia = 0,01346 TIA
③ Hồi quy     : 357 + 0,0338 × 307.200      ≈ 10.740 utia ≈ 0,0107 TIA  (under ~20%)
④ Rate dashboard (over-count): 0,0847 × 307.200 ≈ 26.020 utia ≈ 0,026 TIA  ✗
```

→ **0,01346 TIA đo thật** (≈ **$0,0053** @ TIA $0,395). Share-based là cách extrapolate
đáng tin cho blob lớn; rate dashboard ④ phóng đại ~2×. On-chain chỉ neo **~40 byte**
(root + height) + ~267k gas `record_replay` (≈ 0 ustake trong mô hình 0-fee).

#### Bảng so sánh — lưu `makeReplay(300 KiB)` = 307.200 byte

| Phương án lưu 300 KiB | Gas / DA-gas | Footprint on-chain | Phí token | USD (giá giả định¹) | Lưu giữ |
|---|---|---|---|---|---|
| **Đồ án (blob-first / Celestia)** | 2.691.748 gas *(đo)* | **~40 B** (root+height) | **0,01346 TIA** *(đo, PFB 612074)* | **~$0,0053** @ TIA $0,395 | retention Celestia, có neo on-chain |
| Ethereum L1 — **state (SSTORE)** | ~212M gas (vượt block ~36M → ~6 tx) | 300 KiB **vĩnh viễn/mọi node** | ~2,12·10⁸ gas | **~$3.660** @ 10 gwei, ETH $1.723 | vĩnh viễn |
| Ethereum L1 — calldata (tham chiếu) | ~12,3M gas (sàn EIP-7623) | không phải state bền | — | **~$212** @ 10 gwei | trong history |
| **OP Stack** — blob L1 (EIP-4844) | 393.216 blob-gas (3 blob × 128 KiB) | data off-chain | — | **~$0 → $0,68**² | **~18 ngày** rồi xoá |
| **Cosmos state (Juno…)** | ~9,22M gas (30 gas/byte) | 300 KiB **vĩnh viễn/mọi node** | 0,69 JUNO | **~$0,020** @ JUNO $0,0295 + bloat³ | vĩnh viễn |

¹ Giá token $0,395/TIA, $1.723/ETH, $0,0295/JUNO là **giá tại thời điểm viết** — đo lại
khi giá đổi. Phí DA blob-first cột này là **số đo thật** (private net), chỉ phần quy USD
dùng giá token spot ở trên.
² Phí blob EIP-4844 biến động mạnh theo độ bận Ethereum (gần $0 khi rảnh, vài $/MB
khi nghẽn); trích [blobscan.com](https://blobscan.com) đúng thời điểm.
³ Chi phí thật của lưu on-chain không phải gas một lần mà là **state bloat vĩnh viễn
ở mọi node** — gas chỉ là giá vào cửa.

**Đọc bảng.** Cùng tác vụ "lưu 300 KiB verifiable", blob-first (~$0,0053) rẻ hơn
**~5 bậc** so với nhúng vào state Ethereum (~$3.660, lại còn vượt block gas limit
nên không khả thi trong 1 tx), **~3–4×** so với state Cosmos, và **ngang/nhỉnh hơn**
OP blob (rẻ nhưng chỉ giữ ~18 ngày). Điểm mạnh không chỉ là giá mà là **không phình
state vĩnh viễn**: chi phí blob-first là khoản DA một lần, còn nhúng on-chain bắt
*mọi node lưu mãi mãi*.

> **Cách số này được đo (tái lập được).** (1) Chạy `examples/game-telemetry` (đẩy
> đúng `makeReplay(300 KiB)` qua `SubmitBatch`) — nó log `height` của batch. (2) Đọc
> PFB đó: `node scripts/measure_da_fees.mjs http://131.153.224.169:26757 <h-2> <h+2>`
> → lấy `fee` ở dòng `blob bytes = 307200`. Lần đo này: height **612.074**, tx
> **`F19C468A906D82FA…`**, fee **13.459 utia**. (3) Gas nhúng đối chứng: `/tx/simulate`
> một tx nhúng 307.200 byte vào WASM → `gas_used`. (4) Quy USD bằng giá token spot;
> blob 4844 lấy từ blobscan. Đổi mạng/giá TIA thì đo lại — chỉ phần USD là giả định.

---

## 11. Gợi ý cách dùng tài liệu này để viết phần so sánh

- **Trong Chương 4 (đánh giá):** ưu tiên đối chiếu **định lượng** mà đồ án sở
  hữu — bảng "tiết kiệm gas blob-first vs nhúng on-chain" (mục 0) so với mô hình
  lưu mọi byte on-chain của chain CosmWasm truyền thống (mục 4). Đây là so sánh
  bạn tự đo, mạnh nhất.
- **Trong Chương 6 (kết luận/đối chiếu):** dùng các sơ đồ và bảng ở đây cho so
  sánh **định tính** về mô hình (sovereign vs hub, EVM vs CosmWasm, tầng
  sequencer vs tầng thực thi).
- **Khi trích số của bên thứ ba** (cửa sổ 7 ngày, blob 128 KiB, block ~6s…):
  luôn dẫn nguồn ở mục [Tham chiếu](#12-tham-chiếu) theo chuẩn IEEE và ghi rõ
  thời điểm truy cập, vì các con số này thay đổi theo bản nâng cấp.
- **Giữ công bằng:** không so độ trễ/throughput đo trên máy dev của đồ án với số
  production của đối thủ; với rollup EVM chỉ so trục DA và chi phí, không so
  tính năng máy ảo.

---

## 12. Tham chiếu

- Hướng dẫn migrate CometBFT → Evolve (ignite app): <https://docs.ev.xyz/guides/cometbft-to-evolve>
- Hướng dẫn migrate sang ev-abci: <https://docs.ev.xyz/guides/migrating-to-ev-abci>
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
