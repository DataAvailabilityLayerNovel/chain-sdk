# IBC Integration

Tài liệu này mô tả chi tiết về IBC (Inter-Blockchain Communication) trong cosmos-exec: **những gì đã được wire sẵn**, **những gì còn thiếu**, và **các bước cụ thể** để kết nối rollup với một chain Cosmos khác (ví dụ Osmosis, Cosmos Hub, hoặc một rollup ev-node khác).

> ⚠️ **Trạng thái hiện tại:** IBC trong cosmos-exec đã được **scaffold** (capability module, IBC core keeper, transfer keeper, wasm IBC scoping đều đã wire). Tuy nhiên, light-client interop với rollup model **chưa hoàn thiện** — xem §5 và §7. Đừng coi đây là "IBC sẵn sàng cho mainnet".

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

## Tham khảo

- [IBC Specification](https://github.com/cosmos/ibc) — spec gốc của giao thức
- [ibc-go v8 docs](https://ibc.cosmos.network/v8/) — implementation Go
- [Hermes relayer](https://hermes.informal.systems/) — relayer Rust được dùng nhiều nhất
- [08-wasm LC architecture](https://github.com/cosmos/ibc-go/tree/main/modules/light-clients/08-wasm) — cho trust model B
- [auto-account-creation.md §4](./auto-account-creation.md) — chi tiết signature verification cho relayer tx
- [architecture.md](./architecture.md) — vị trí của IBC trong tổng thể stack
