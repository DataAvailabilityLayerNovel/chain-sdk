# IBC Integration

Tài liệu này mô tả chi tiết về IBC (Inter-Blockchain Communication) trong cosmos-exec: **những gì đã được wire sẵn**, **những gì còn thiếu**, và **các bước cụ thể** để kết nối rollup với một chain Cosmos khác (ví dụ Osmosis, Cosmos Hub, hoặc một rollup ev-node khác).

> ⚠️ **Trạng thái hiện tại:** IBC trong cosmos-exec đã được **scaffold** (capability module, IBC core keeper, transfer keeper, wasm IBC scoping đều đã wire). Tuy nhiên, light-client interop với rollup model **chưa hoàn thiện** — xem §5 và §7. Đừng coi đây là "IBC sẵn sàng cho mainnet".

---

## 0. ev-node có dùng được IBC không?

**Trả lời ngắn: được về mặt nguyên lý, nhưng chưa "cắm là chạy".** Một sovereign
rollup như ev-node vẫn là một chain Cosmos SDK đầy đủ, nên **mọi mảnh phần mềm của
IBC đều dùng được** và trong cosmos-exec đã **wire sẵn cả 4 lớp** (capability, IBC
core, transfer/ICS-20, và WasmKeeper có khả năng IBC — xem §2). Contract CosmWasm
cũng có thể mở channel và gửi/nhận packet. Cái **chưa xong** không nằm ở "IBC có
chạy trên Cosmos SDK không" (có), mà ở **chỗ tiếp giáp giữa mô hình rollup và mô
hình light client của IBC**.

Cụ thể, ba rào cản khiến nó chưa dùng được ngay (chi tiết ở §3, §7):

1. **Mô hình tin cậy lệch nhau.** IBC light client chuẩn `07-tendermint` kỳ vọng
   counterparty có **một tập validator** ký block. Rollup chỉ có **một sequencer**,
   và "finality" thật sự đến từ việc block **đã lên DA (Celestia)**, không phải từ
   chữ ký validator set. Hiện cosmos-exec đi đường tắt (**Approach A**): *trình bày
   rollup như một Tendermint chain 1-validator* để 07-tendermint chấp nhận — chạy
   được nhưng **counterparty phải tin hoàn toàn vào sequencer key**, và **bỏ qua**
   bằng chứng DA.

2. **Thiếu RPC kiểu Tendermint.** Relayer (Hermes, rly) nói chuyện qua **Tendermint
   RPC/WebSocket**, còn cosmos-exec chỉ phơi **REST/HTTP**. Nên cần một **adapter
   RPC** hoặc dùng chế độ polling của relayer (§4.6, §7).

3. **Vài chỗ còn stub.** `GetHistoricalInfo` trả rỗng và `UnbondingTime` hard-code
   24h (§3.2) → light client **không skip-verify được**, buộc relayer phải update
   đều và rollup phải **sản block đều** (kể cả block rỗng) kẻo client hết hạn.

Nói cách khác: **phần mềm IBC đã sẵn; thứ còn thiếu là một light client hiểu được
"header của rollup" một cách trustless.** Cách làm đúng chuẩn cho rollup là dùng
**08-wasm light client** (Approach B) verify *chữ ký sequencer + bằng chứng DA
inclusion* — xem §7.1.

> **Làm rõ (tránh nói nhầm tại hội đồng):**
> - **IBC không thuộc lõi ev-node.** ev-node là khung *execution-agnostic* — nó chỉ
>   lo sắp thứ tự, sản xuất block, P2P và DA. **IBC nằm ở tầng ứng dụng** (thư viện
>   `ibc-go` trong Cosmos SDK app), tức là ở **tầng thực thi mà đồ án dựng**, không
>   phải thứ ev-node cung cấp. Nên câu hỏi đúng là *"app Cosmos SDK trên ev-node có
>   dùng được IBC không"* — và câu trả lời là có (về nguyên lý), do chính đồ án wire.
> - **ev-node có "light client" — nhưng là loại khác.** ev-node/Celestia có
>   **light node cho DA** (light node lấy mẫu để kiểm tra dữ liệu sẵn có — data
>   availability sampling). Đây **không phải IBC light client**. IBC light client
>   (07-tendermint / 08-wasm) là một object *on-chain trong tầng ứng dụng* dùng để
>   verify header của **chain đối phương** — ev-node **không** ship sẵn cái này cho
>   mô hình rollup; muốn trustless phải tự viết 08-wasm (§7.1). Hai khái niệm trùng
>   tên "light client" nhưng khác tầng, khác việc.
> - **Vì sao đồ án chủ động không dùng IBC.** (1) **Ngoài phạm vi đề tài** — trọng
>   tâm là *tầng thực thi CosmWasm + blob-first*, không phải liên chuỗi; (2) để
>   *trustless thật* phải làm thêm hai phần nặng (08-wasm light client + adapter RPC
>   kiểu Tendermint cho relayer), vượt khối lượng đồ án; (3) **use case không cần** —
>   app-chain/blob-first của đồ án tự vận hành, không phụ thuộc chuyển tài sản xuyên
>   chain như một tính năng lõi. Vì vậy đồ án **wire sẵn (scaffold) để mở đường**
>   nhưng **cố ý dừng ở đó**, coi IBC trustless là hướng phát triển.

---

## 1. IBC là gì và tại sao một sovereign rollup cần nó

### 1.1 Bài toán

IBC là giao thức chuẩn để hai blockchain Cosmos **gửi gói tin lẫn nhau** mà không cần bên thứ ba tin cậy. Mỗi bên chạy một **light client** của bên kia ngay trong state của mình, và chỉ chấp nhận gói tin (packet) khi nó kèm proof rằng gói đó thực sự được commit trên chain đối phương.

Sovereign rollup (như ev-node) là một **chain độc lập về consensus** — không inherit security từ L1, chỉ dùng L1 làm DA layer. Vì thế nó cần IBC giống hệt một chain Cosmos thường: để chuyển token (ICS-20), gọi liên chain (ICA / interchain accounts), hoặc cho phép CosmWasm contract giao tiếp xuyên chain.

### 1.2 Bốn lớp giao thức IBC

```
ICS-20 (token transfer) │ ICA (interchain accounts) │ Custom app modules
─────────────────────────────────────────────────────────────────────────  Application
                                                                           ↑
Channel (ordered/unordered, packet routing, ack/timeout)                   │
─────────────────────────────────────────────────────────────────────────  Transport
Connection (versioned handshake giữa hai client)                           │
─────────────────────────────────────────────────────────────────────────  Connection
Light Client (07-tendermint, 08-wasm, ...) — verify header counterparty    │
─────────────────────────────────────────────────────────────────────────  Client
```

cosmos-exec hiện đã wire **toàn bộ 4 lớp** trên (capability + ibc core + transfer + router). Phần phức tạp duy nhất còn lại là **lớp Client phải hiểu được header của rollup** — xem §3, §5.

---

## 2. Hiện trạng — đã được wire trong [`app/app.go`](../../../app/app.go)

### 2.1 Modules đã thêm

| Module                | Import path                                            | Vai trò                                         |
| --------------------- | ------------------------------------------------------ | ----------------------------------------------- |
| `capability`          | `github.com/cosmos/ibc-go/modules/capability`          | Object capability — phân quyền port/channel     |
| `ibc` (core)          | `github.com/cosmos/ibc-go/v8/modules/core`             | Client / Connection / Channel state machine     |
| `ibc/transfer`        | `github.com/cosmos/ibc-go/v8/modules/apps/transfer`    | ICS-20 — chuyển token xuyên chain               |
| `wasm` (IBC-aware)    | `github.com/CosmWasm/wasmd/x/wasm`                     | CosmWasm + IBC-capable contracts                |

### 2.2 Keepers & store keys

```go
// app/app.go (lược gọn)
app.CapabilityKeeper = capabilitykeeper.NewKeeper(...)
app.ScopedIBCKeeper      = app.CapabilityKeeper.ScopeToModule(ibcexported.ModuleName)
app.ScopedTransferKeeper = app.CapabilityKeeper.ScopeToModule(ibctransfertypes.ModuleName)
app.ScopedWasmKeeper     = app.CapabilityKeeper.ScopeToModule(wasmtypes.ModuleName)
app.CapabilityKeeper.Seal()  // không cho scope thêm sau điểm này

app.IBCKeeper = ibckeeper.NewKeeper(
    appCodec, keys[ibcexported.StoreKey],
    app.GetSubspace(ibcexported.ModuleName),
    ibcStakingKeeper, ibcUpgradeKeeper,     // ← stubs, xem §3
    app.ScopedIBCKeeper,
)

app.TransferKeeper = ibctransferkeeper.NewKeeper(
    appCodec, keys[ibctransfertypes.StoreKey],
    app.GetSubspace(ibctransfertypes.ModuleName),
    app.IBCKeeper.ChannelKeeper, app.IBCKeeper.ChannelKeeper,
    app.IBCKeeper.PortKeeper,
    app.AccountKeeper, app.BankKeeper,
    app.ScopedTransferKeeper,
)
```

### 2.3 Router

```go
// Mọi packet đến port "transfer" được dispatch về transferStack.
transferStack := ibctransfermodule.NewIBCModule(app.TransferKeeper)
ibcRouter := porttypes.NewRouter().AddRoute(ibctransfertypes.ModuleName, transferStack)
app.IBCKeeper.SetRouter(ibcRouter)
```

Khi thêm app module IBC mới (ví dụ ICS-27, hoặc app-specific port), bạn thêm `.AddRoute("portname", yourIBCModule)` ở đây.

### 2.4 CosmWasm có thể nói IBC

`WasmKeeper` được khởi tạo với `app.IBCKeeper.ChannelKeeper`, `PortKeeper`, `ScopedWasmKeeper`, và `TransferKeeper`. Điều này có nghĩa: **một contract CosmWasm có thể mở channel IBC riêng, gửi/nhận packet, và xử lý ack/timeout** — bao gồm cả việc tự chuyển ICS-20 token (xem ví dụ `ibc-hooks`, `ibc-reflect` của wasmd).

---

## 3. Mô hình tin cậy — rollup ≠ Tendermint chain

Đây là phần dễ hiểu nhầm nhất.

### 3.1 Vấn đề

IBC light client gốc (`07-tendermint`) kỳ vọng: "header của counterparty được ký bởi một validator set, có thể verify qua merkle proof + signature aggregation". Sovereign rollup chỉ có **một sequencer**, không có validator set thực sự, và "finality" phụ thuộc vào việc block đó đã được commit lên DA layer (Celestia/...).

Có ba cách giải:

| Approach                                  | Trust model                                                            | Status trong cosmos-exec       |
| ----------------------------------------- | ---------------------------------------------------------------------- | ------------------------------ |
| **A. Sequencer-as-1-validator (07-tendermint)** | Counterparty tin sequencer ký block đúng. Đơn giản, nhưng tập trung.   | Scaffolded — stub keepers dùng cách này |
| **B. 08-wasm light client**               | Counterparty deploy LC dạng wasm, verify cả sequencer sig + DA inclusion. | Chưa wire — cần custom LC code     |
| **C. Sovereign IBC qua DA**               | Cả hai chain dùng cùng DA layer làm "trọng tài". | Research-stage             |

### 3.2 Stub keepers — manh mối về (A)

Trong [`app/wasm_deps.go`](../../../app/wasm_deps.go), hai keepers giả được tạo ra để thoả interface mà `ibckeeper.NewKeeper` đòi hỏi:

```go
type ibcClientStakingKeeper struct { enabled bool }
func (k) GetHistoricalInfo(...) (HistoricalInfo, error) { return HistoricalInfo{}, nil }
func (k) UnbondingTime(...) (time.Duration, error)     { return 24 * time.Hour, nil }

type ibcClientUpgradeKeeper struct { enabled bool }
// Tất cả method return zero/no-op.
```

Ý nghĩa:

- `UnbondingTime = 24h` được dùng làm **trusting period** trong light client. Counterparty sẽ refuse header nếu khoảng cách giữa hai cập nhật > 24h. Đây là hằng số cứng — đổi giá trị này = đổi consensus, cần ghi rõ trong genesis của counterparty.
- `GetHistoricalInfo` trả về **rỗng**. Có nghĩa: counterparty light client không thể verify một header đã quá cũ thông qua historical validator set — chỉ verify được trust-chain liên tục từ initial header. Hệ quả: **light client phải được update đều đặn** (chính là việc relayer làm), không được phép gap quá `UnbondingTime`.

Nói cách khác, hiện cosmos-exec đang đi **Approach A**: trình bày rollup như một Tendermint chain 1-validator để 07-tendermint client của counterparty chấp nhận. Đổi lại tính đơn giản, ta có một điểm tập trung — counterparty hoàn toàn tin sequencer key.

### 3.3 Verify phía rollup mình

Một câu hỏi khác: khi counterparty gửi packet xuống rollup, ai verify header của counterparty?

Phía rollup, light client của counterparty (ví dụ Cosmos Hub) là một state object trong `x/ibc-client` store, được cập nhật mỗi lần relayer gọi `MsgUpdateClient`. Tx update đó đi qua ante chain bình thường, ký bởi relayer's key, và `x/ibc-client` tự kiểm tra signature của validator set Cosmos Hub trong header. Phần này **đã hoạt động** out-of-the-box — `ibcKeeper.ClientKeeper` đã implement 07-tendermint verify logic.

---

## 4. Các bước tích hợp end-to-end

Giả sử bạn muốn kết nối rollup `cosmos-wasm-local` với chain `wasmd-counterparty` (cũng chạy Tendermint).

### 4.1 Cấu hình bắt buộc cả hai bên

| Setting           | Rollup (cosmos-exec)                     | Counterparty (wasmd / cosmos-sdk chain) |
| ----------------- | ---------------------------------------- | --------------------------------------- |
| `chain_id`        | Khớp với genesis (ví dụ `cosmos-wasm-local`) | Của riêng nó                            |
| Block time        | Đều, không gap > trusting period         | Block time bình thường                  |
| Public RPC        | HTTP endpoints để relayer poll           | RPC Tendermint chuẩn                    |
| Pubkey type       | Sequencer key (ed25519 / secp256k1)      | Tendermint validator pubkey             |

Trên cosmos-exec, expose ít nhất các endpoint sau cho relayer:

- `GET /status` — chain-id, latest block height, sequencer pubkey
- `GET /blocks/{height}` — block + sequencer signature
- `GET /tx/{hash}` — verify ack đã in
- `POST /tx/submit` — relayer gửi `MsgUpdateClient`, `MsgRecvPacket`, ...

### 4.2 Bước 1: Tạo client trên mỗi bên

Trên **rollup**, gửi `MsgCreateClient` cho counterparty (header gốc của counterparty):

```bash
hermes create client --host-chain cosmos-wasm-local --reference-chain wasmd-counterparty
```

Trên **counterparty**, gửi `MsgCreateClient` cho rollup. Đây là bước khó: counterparty cần một initial header của rollup ở dạng `07-tendermint` `ClientState` + `ConsensusState`. Phải đảm bảo:

- `trust_level = 1/1` (1 validator = sequencer, phải đồng thuận 100%)
- `trusting_period < unbonding_period` (mặc định 24h trong stub)
- `unbonding_period = UnbondingTime` từ stub keeper
- `latest_height = (revision_number, latest_rollup_height)`
- `chain_id` phải match đúng đến từng ký tự

### 4.3 Bước 2: Connection handshake

```
Rollup                                          Counterparty
   │  MsgConnectionOpenInit ───────────────►
   │                            ◄────────────── MsgConnectionOpenTry
   │  MsgConnectionOpenAck ────────────────►
   │                            ◄────────────── MsgConnectionOpenConfirm
   │
   └─ connection-0 ESTABLISHED ────────────────┘
```

Relayer (`hermes` hoặc `rly`) tự động hoá toàn bộ 4 bước trên:

```bash
hermes create connection --a-chain cosmos-wasm-local --b-chain wasmd-counterparty
```

### 4.4 Bước 3: Channel handshake (cho transfer)

```bash
hermes create channel \
  --a-chain cosmos-wasm-local --a-connection connection-0 \
  --a-port transfer --b-port transfer \
  --order unordered --channel-version ics20-1
```

Output: `channel-0` ở cả hai bên. ICS-20 dùng unordered channel.

### 4.5 Bước 4: Chuyển token

Từ rollup → counterparty:

```bash
# Trên rollup
cosmos-exec tx ibc-transfer transfer transfer channel-0 \
  cosmos1abc...counterparty 100stake \
  --from alice
```

Flow nội bộ:

1. `MsgTransfer` được route đến `TransferKeeper.SendTransfer`.
2. Token bị **escrow** vào module account `transfer` trên rollup.
3. Packet được commit vào `x/ibc-channel` state.
4. Relayer thấy commit (subscribe `send_packet` event), gọi `MsgRecvPacket` trên counterparty kèm proof.
5. Counterparty verify proof bằng light client của rollup → mint voucher token có denom `ibc/<HASH(channel-0/transfer/stake)>`.
6. Counterparty commit ack → relayer chuyển ack về rollup → rollup xoá packet commit, finalize.

Chiều ngược lại: gửi voucher quay về sẽ unescrow token gốc trên rollup.

### 4.6 Bước 5: Chạy relayer

Relayer là một process **off-chain** đứng giữa, không cần permission. Ví dụ với Hermes:

```toml
# ~/.hermes/config.toml (rút gọn)
[[chains]]
id = 'cosmos-wasm-local'
rpc_addr = 'http://localhost:26657'         # hoặc HTTP endpoint của cosmos-exec
grpc_addr = 'http://localhost:9090'
account_prefix = 'cosmos'
key_name = 'relayer-rollup'
gas_price = { price = 0.025, denom = 'stake' }
trusting_period = '23h'
unbonding_period = '24h'                     # phải khớp ibcClientStakingKeeper

[[chains]]
id = 'wasmd-counterparty'
rpc_addr = 'http://counterparty:26657'
# ...
```

Lưu ý: cosmos-exec **không nói WebSocket Tendermint chuẩn**. Bạn có thể cần một adapter (xem §7) hoặc dùng polling mode của relayer.

---

## 5. IBC trong CosmWasm contract

Vì `ScopedWasmKeeper` đã được pass vào `WasmKeeper`, contract có thể implement các entry points sau:

```rust
// Trong contract
#[cfg_attr(not(feature = "library"), entry_point)]
pub fn ibc_channel_open(deps, env, msg: IbcChannelOpenMsg) -> Result<...>;
pub fn ibc_channel_connect(...) -> Result<...>;
pub fn ibc_channel_close(...) -> Result<...>;
pub fn ibc_packet_receive(...) -> Result<IbcReceiveResponse, Never>;
pub fn ibc_packet_ack(...) -> Result<IbcBasicResponse, _>;
pub fn ibc_packet_timeout(...) -> Result<IbcBasicResponse, _>;
```

Mỗi contract IBC-capable được cấp một port riêng dạng `wasm.<contract_address>`. Để chain accept contract đó nói IBC, code cần build với feature `stargate,iterator,ibc3` và chain phải bật `cosmwasm_1_4` capability — **đã bật sẵn** trong [`app/app.go:270`](../../../app/app.go#L270):

```go
availableCapabilities := strings.Join([]string{
    "iterator", "staking", "stargate",
    "cosmwasm_1_1", "cosmwasm_1_2", "cosmwasm_1_3", "cosmwasm_1_4",
}, ",")
```

Tham khảo `ibc-reflect`, `ibc-hooks`, `cw-ics20-ics4` để thấy patterns mẫu.

---

## 6. Pitfalls / những điểm dễ sai

### 6.1 Sequencer key xoay → light client chết

Sequencer key chính là "validator set" trong mô hình A. Đổi key = đổi `ConsensusState` → counterparty light client từ chối mọi header mới. Phải có quy trình:

- Báo trước cho relayer + counterparty
- `MsgUpdateClient` với header transition
- Hoặc tạo client mới và migrate channel (đau hơn)

### 6.2 Block gap > trusting period

Nếu rollup không produce block trong > 24h (mặc định), counterparty light client expires. Recovery: phải submit governance proposal trên counterparty để reset client. **Luôn đảm bảo rollup produce empty block** nếu không có tx — đây cũng là lý do ev-node có cờ `aggregator.empty_blocks_max_time`.

### 6.3 Unbonding time = 24h là hằng số mềm

Stub trả `24h` cứng. Nếu sau này bạn muốn dài hơn (an toàn hơn) hoặc ngắn hơn (phục hồi nhanh hơn), phải:

1. Đổi giá trị trong [`app/wasm_deps.go`](../../../app/wasm_deps.go) `UnbondingTime`.
2. Coordinate với mọi counterparty đã có client (giá trị này pin trong `ClientState` của họ).
3. Cân nhắc tạo lại client thay vì đổi tại chỗ.

### 6.4 Stub `GetHistoricalInfo` rỗng

Vì historical info trả empty, **client của counterparty không thể skip-verify** (jump nhiều block). Relayer phải update client thường xuyên, không được lười. Hermes mặc định OK; rly cần check config `max_clock_drift` + `clock_drift`.

### 6.5 Replay protection cho relayer

Relayer gửi tx vào rollup qua `POST /tx/submit`. Tx đó đi qua ante chain — `IncrementSequenceDecorator` chống replay. Đảm bảo `COSMOS_EXEC_ENFORCE_SIGNATURES=true` ở production (xem [auto-account-creation.md §4](./auto-account-creation.md)).

### 6.6 Fee — relayer cần token để gửi tx

Relayer chi token để submit `MsgUpdateClient`, `MsgRecvPacket`, ... Nếu rollup bật phí (`COSMOS_EXEC_MIN_GAS_PRICE > 0`), relayer account phải có balance dương. Có thể chạy chương trình "fee grant" để dApp trả phí thay cho user (ICS-29 incentivized relaying chưa wire).

---

## 7. Roadmap — những gì còn thiếu để mainnet-ready

| Hạng mục                                          | Trạng thái            | Ghi chú                                                                |
| ------------------------------------------------- | --------------------- | ---------------------------------------------------------------------- |
| Capability + ibc core + transfer wired            | ✅ Done               | §2                                                                     |
| Tendermint-style RPC adapter (cho Hermes)         | ❌ Missing            | Hiện relayer cần adapter từ cosmos-exec REST → Tendermint RPC          |
| Sequencer header format khớp 07-tendermint        | ⚠️ Partial            | Cần verify thực tế signing scheme + commit hash compatibility          |
| 08-wasm light client (cho trust model B)          | ❌ Missing            | Khuyến nghị cho production thực sự không trust sequencer               |
| ICS-27 Interchain Accounts                        | ❌ Missing            | Cần thêm `icaModule` + scoped keeper + router                          |
| ICS-29 Fee Middleware                             | ❌ Missing            | Incentivize relayer; optional                                          |
| IBC hooks (auto-call wasm on packet receive)      | ❌ Missing            | Thêm middleware vào transferStack                                       |
| Real `GetHistoricalInfo`                          | ❌ Stub               | Để skip-verify hoạt động. Cần lưu historical sequencer-set             |
| Genesis migration cho IBC params                  | ❓ Untested           | Verify `ibcKeeper.InitGenesis` chạy đúng trong [`app.go:319`](../../../app/app.go#L319) |
| End-to-end test với một counterparty thật         | ❌ Missing            | Khuyến nghị: dựng wasmd local + hermes trong CI                        |

### 7.1 Để chuyển sang trust model B (08-wasm LC)

1. Viết một wasm light client (Rust) verify:
   - Sequencer signature trên header
   - DA inclusion proof (block hash đã commit lên Celestia)
2. Deploy LC code trên counterparty qua `MsgStoreCode` của `08-wasm`.
3. `MsgCreateClient` với `client_type = "08-wasm"` thay vì `"07-tendermint"`.
4. Phần còn lại (connection, channel, packet) không đổi.

Đây là approach chuẩn cho rollup IBC — xem Polymer Labs, Composable Finance, Union, hoặc reference của Celestia ecosystem.

---

## 8. Quick reference — file đụng tới IBC

| File                                                         | Vai trò                                       |
| ------------------------------------------------------------ | --------------------------------------------- |
| [`app/app.go`](../../../app/app.go)                          | Wire mọi keeper IBC + router + module manager |
| [`app/wasm_deps.go`](../../../app/wasm_deps.go)              | Stub staking/upgrade keepers cho IBC client   |
| [`app/ante.go`](../../../app/ante.go)                        | Ante chain — relayer tx đi qua đây            |
| `executor/executor.go`                                       | Endpoint `/tx/submit` mà relayer dùng         |

---

## 9. Dùng ev-abci thì IBC thế nào? Và vì sao vẫn cần 08-wasm + relayer

### 9.1 ev-abci gỡ 2/3 rào cản

ev-abci là con đường **thay chỗ CometBFT** và tái tạo gần đủ bề mặt CometBFT. Mà
hai thứ IBC cần ở counterparty lại **chính là** hai thứ ev-abci dựng lại sẵn:

| Rào cản IBC | Đường **cắm thẳng** (đồ án — cosmos-exec, REST) | Đường **ev-abci** |
| --- | --- | --- |
| Relayer cần **Tendermint RPC** (WebSocket + `block`/`tx`/`validators`/`status`) | ❌ chỉ REST → phải tự viết adapter (§7) | ✅ **có sẵn RPC tương thích CometBFT** → Hermes nối thẳng |
| Header khớp **`07-tendermint`** (block/commit/chữ ký kiểu CometBFT) | ⚠️ partial → tự lo định dạng | ✅ **sinh header + commit + chữ ký kiểu CometBFT** (sequencer đóng vai validator) |
| **Mô hình tin cậy** (1 sequencer, finality từ DA) | ❌ vẫn tin sequencer | ❌ **vẫn tin sequencer** — ev-abci không giải cái này |

Kết luận: **dùng ev-abci thì IBC 07-tendermint chạy được gần như out-of-the-box ở
Approach A** (tin sequencer), vì nó có sẵn RPC cho relayer và header đúng định dạng.
Đường cắm thẳng của đồ án đánh đổi đúng phần đó để nhẹ và nhanh hơn — muốn IBC thì
phải tự bù adapter. **Nhưng cả hai đường đều KHÔNG trustless** cho tới khi có
**08-wasm light client** — vì đó là bản chất của mô hình single-sequencer rollup,
không phải do adapter.

### 9.2 Vì sao BẮT BUỘC phải viết 08-wasm light client (nếu muốn trustless)

**Light client trong IBC là gì.** Là một **object on-chain nằm bên counterparty**,
đại diện cho rollup, có nhiệm vụ **verify header của rollup** trước khi chấp nhận
bất kỳ packet nào. Nó là "safety" của IBC: chỉ khi header được light client verify
đậu thì packet mới được tin.

**Vì sao `07-tendermint` (Approach A) chưa đủ.** LC 07-tendermint chỉ kiểm **một
thứ**: header có được **validator set ký đúng** không. Với rollup, "validator set"
= **đúng một sequencer**. Nên 07-tendermint chỉ trả lời được *"sequencer có ký cái
header này không"* — mà **không** trả lời được *"block này có thật sự được công bố
lên DA (Celestia) chưa"*. Hệ quả: nếu sequencer ký một header cho block **chưa từng
lên DA** hoặc **state sai**, 07-tendermint **không có cách nào từ chối**. Tức là
counterparty **tin sequencer hoàn toàn** — cả về thứ tự lẫn tính khả dụng/đúng đắn.

**08-wasm giải quyết bằng cách nào.** `08-wasm` **không phải một light client cụ
thể** — nó là một **khung cho phép nạp một light client viết bằng WASM** (Rust →
WASM) lên counterparty. Nghĩa là bạn **tự viết logic verify** đúng theo mô hình
ev-node + Celestia, rồi upload. LC 08-wasm cho rollup cần verify **hai lớp**:

1. **Chữ ký sequencer** trên header (giống 07-tendermint).
2. **Bằng chứng DA inclusion** — chứng minh dữ liệu của block đã **thật sự được
   công bố lên Celestia** ở một height nhất định (proof theo namespace đối chiếu
   với data root của Celestia — thường lấy data root đó qua **Blobstream**, cầu
   đưa data root Celestia sang chain đối phương).

Nhờ lớp (2), counterparty **chỉ chấp nhận header khi dữ liệu chứng minh được là sẵn
có trên DA** → không còn tin mù vào việc sequencer trung thực về tính khả dụng. Đây
mới là **trustless thật** cho rollup.

**Vì sao phải là BẠN viết.** `ibc-go` ship sẵn 07-tendermint (cho chain CometBFT),
nhưng **không** ship LC hiểu mô hình rollup + Celestia. 08-wasm chạy *bất kỳ* LC
nào bạn nạp, nhưng **bạn phải viết cái LC đó** — logic verify chữ ký sequencer +
DA proof là **đặc thù ev-node + Celestia**, chưa có bản drop-in.

**Các bước triển khai 08-wasm (chi tiết):**
1. **Viết LC (Rust):** parse `SignedHeader` của ev-node → verify chữ ký sequencer →
   verify DA inclusion proof (namespace proof so với Celestia data root / Blobstream).
2. **`MsgStoreCode`** lên module `08-wasm` của counterparty → nhận về một **code hash**.
3. **`MsgCreateClient`** với `client_type = "08-wasm"`, trỏ tới code hash + initial
   `ClientState`/`ConsensusState` của rollup.
4. **Connection / channel / packet handshake KHÔNG đổi** — chúng không quan tâm LC
   bên dưới là loại gì (xem §4.3–4.5). Chỉ **lớp Client** thay, các lớp trên tái dùng.

> **Quan trọng — ev-abci KHÔNG thay được bước này.** ev-abci làm rollup *trông
> giống* Tendermint để 07-tendermint chấp nhận (trust level A). Nhưng để **verify
> DA** thì vẫn phải có LC tùy biến (08-wasm), **bất kể** đi ev-abci hay cắm thẳng —
> vì DA-verification là logic của mô hình rollup, không phải thứ adapter cung cấp.

### 9.3 Relayer — nó là gì, làm gì, tin cậy ra sao (chi tiết)

**Là gì.** Relayer là **một tiến trình off-chain** — *không* phải smart contract,
*không* nằm trên chain nào. **Permissionless**: ai chạy cũng được, chạy bao nhiêu
cái cũng được. Nó là "người đưa thư" mang **packet + bằng chứng** qua lại giữa hai
chain. Ví dụ: **Hermes** (Rust, Informal Systems), **rly** (Go).

**Vì sao cần.** Hai chain IBC **không tự nói chuyện trực tiếp** với nhau. Chain A
chỉ **commit packet vào state của chính nó**, không có cách nào tự đẩy sang chain B.
Relayer đứng giữa: thấy packet trên A → lấy proof → nộp cho B.

**Vòng đời một packet (relayer làm gì):**
1. **Theo dõi** chain A, bắt sự kiện `send_packet`.
2. **Truy vấn** A lấy packet + **merkle proof** cam kết packet + height của header.
3. **`MsgUpdateClient`** lên B — cập nhật light client của A (nằm trên B) tới đúng
   height chứa packet.
4. **`MsgRecvPacket`** lên B kèm packet + proof → **light client trên B verify
   proof** đối chiếu consensus state vừa update.
5. B commit **acknowledgement** → relayer mang ack về A (`MsgAcknowledgement`).
6. Nếu quá hạn không nhận → relayer gửi **`MsgTimeout`** để A hoàn tác (unescrow).

**Tin cậy — vì sao relayer permissionless mà vẫn an toàn.** Relayer **không thể giả
mạo gì cả**: tất cả những gì nó làm là *mang dữ liệu + proof*; việc **verify là do
light client hai bên**. Relayer độc hại **chỉ có thể không chuyển (kiểm duyệt)**
hoặc tốn gas vô ích — **không thể ăn cắp hay ngụy tạo**. Đây chính là lý do "ai cũng
chạy relayer được".

**Quan hệ relayer ↔ light client (điểm cốt lõi):** relayer lo **liveness** (đồ được
chuyển đến), light client lo **safety** (không cái giả nào được chấp nhận). Relayer
**feed** light client qua `MsgUpdateClient`; light client **verify** cái relayer
mang tới. Tách bạch này là lý do IBC *trust-minimized*: **tin một relayer bất kỳ
cho việc giao hàng, vì không cần tin nó cho tính đúng đắn.**

**Trong ngữ cảnh đồ án:**
- Relayer nối vào rollup qua RPC. **Cắm thẳng (đồ án) = chỉ REST** → cần adapter
  hoặc polling mode. **ev-abci = CometBFT RPC** → Hermes nối thẳng (§9.1).
- Relayer **phải `MsgUpdateClient` đều đặn** vì LC hiện tại không skip-verify được
  (stub `GetHistoricalInfo` rỗng, §3.2) và trusting period 24h — rollup **gap >
  24h** thì client hết hạn, phải governance reset (§6.2). → luôn **sản block rỗng**.
- Relayer **cần token trả gas** trên cả hai chain (§6.6); ICS-29 để thưởng relayer
  thì chưa wire.

---

## 10. Tóm tắt khi bị hỏi (cheat-sheet bảo vệ)

**Hỏi: "ev-node / đồ án có dùng được IBC không?"**
> *"Được về nguyên lý — rollup vẫn là chain Cosmos SDK nên toàn bộ phần mềm IBC
> dùng được, và em đã wire sẵn cả 4 lớp (capability, IBC core, ICS-20 transfer,
> WasmKeeper IBC). Nhưng chưa 'cắm là chạy' vì phần tiếp giáp giữa mô hình rollup
> và light client của IBC còn thiếu — em để ở mức scaffold, chưa mainnet-ready."*

**Hỏi: "Tại sao chưa dùng được ngay?"** — ba lý do, nói gọn:
1. **Light client lệch mô hình tin cậy.** `07-tendermint` cần **một validator set**;
   rollup chỉ có **một sequencer** và finality đến từ **DA (Celestia)**, không phải
   chữ ký validator. Đường tắt hiện tại (Approach A) giả lập rollup như *Tendermint
   1-validator* → chạy được nhưng **phải tin sequencer key** và bỏ qua bằng chứng DA.
2. **Relayer cần Tendermint RPC**, còn cosmos-exec chỉ có **REST/HTTP** → cần adapter.
3. **Còn stub** (`GetHistoricalInfo` rỗng, `UnbondingTime` cứng 24h) → không
   skip-verify, buộc update client đều + sản block đều kẻo client hết hạn.

**Hỏi: "Vậy cắm vào thế nào?"** — hai đường:
- **Đường nhanh (Approach A, đã scaffold):** thêm **adapter RPC kiểu Tendermint**
  cho relayer, đảm bảo header rollup ký khớp định dạng `07-tendermint`, rồi chạy
  **Hermes** làm create-client → connection → channel handshake (§4). Bắt buộc
  **sản block rỗng** để light client không hết trusting period. → Có IBC/ICS-20
  ngay, đổi lại **tin sequencer** (tập trung).
- **Đường chuẩn cho rollup (Approach B, khuyến nghị):** viết một **08-wasm light
  client** (Rust) verify *chữ ký sequencer + DA inclusion proof*, deploy lên
  counterparty, tạo client `client_type = "08-wasm"`; connection/channel/packet
  giữ nguyên (§7.1). → **Trustless thật sự**, không phải tin sequencer. Đây là
  hướng mà Polymer, Composable, Union… đang làm cho rollup IBC.

**Hỏi: "Nếu dùng ev-abci thì IBC có chạy được không?"**
> *"Dễ hơn hẳn — ev-abci dựng lại bề mặt CometBFT nên có sẵn RPC cho relayer và
> header kiểu Tendermint, IBC 07-tendermint chạy gần như ngay ở mức tin sequencer.
> Đồ án em chọn cắm thẳng để nhẹ/nhanh nên đánh đổi đúng phần đó. Nhưng dù ev-abci
> hay cắm thẳng, muốn IBC trustless đều phải tự viết 08-wasm light client verify DA
> — đó là giới hạn của mô hình single-sequencer, không phải của adapter."*

**Hỏi: "08-wasm là gì, relayer là gì?"** (một câu mỗi cái)
> *"08-wasm là khung cho phép nạp một light client viết bằng WASM lên chain đối
> phương — em phải tự viết nó để verify chữ ký sequencer **và** bằng chứng dữ liệu
> đã lên Celestia, nhờ đó counterparty không phải tin mù sequencer. Relayer là tiến
> trình off-chain permissionless mang packet + proof qua lại; nó không verify gì
> (light client hai bên verify) nên không thể giả mạo, chỉ lo giao hàng."*

**Hỏi mẹo: "IBC là của ev-node hay của đồ án? ev-node có light client không?"**
> *"IBC không thuộc lõi ev-node — ev-node execution-agnostic, chỉ lo sequencing/DA.
> IBC nằm ở `ibc-go` trong tầng ứng dụng, tức phần em dựng. ev-node có light node
> nhưng là cho DA (data availability sampling), khác hoàn toàn IBC light client —
> loại IBC (07-tendermint/08-wasm) phải wire ở tầng app, và cho rollup trustless thì
> cần tự viết 08-wasm. Em chủ động không làm IBC vì ngoài phạm vi đề tài (trọng tâm
> là thực thi + blob-first), tốn thêm 08-wasm LC + adapter RPC, và use case không
> cần — nên em chỉ scaffold để mở đường."*

**Câu chốt một dòng:** *"IBC phần mềm đã sẵn; thứ còn thiếu là một light client
hiểu được header của rollup theo kiểu trustless — và đó là hướng phát triển, không
phải giới hạn kiến trúc."*

---

## Tham khảo

- [IBC Specification](https://github.com/cosmos/ibc) — spec gốc của giao thức
- [ibc-go v8 docs](https://ibc.cosmos.network/v8/) — implementation Go
- [Hermes relayer](https://hermes.informal.systems/) — relayer Rust được dùng nhiều nhất
- [08-wasm LC architecture](https://github.com/cosmos/ibc-go/tree/main/modules/light-clients/08-wasm) — cho trust model B
- [auto-account-creation.md §4](./auto-account-creation.md) — chi tiết signature verification cho relayer tx
- [architecture.md](./architecture.md) — vị trí của IBC trong tổng thể stack
