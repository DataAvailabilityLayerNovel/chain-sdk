# Cosmos Execution + WASM Unified Architecture (Full Node model)

Tài liệu này thống nhất kiến trúc để **Cosmos execution (`apps/cosmos-exec`)** và **CosmWasm workflow** đi cùng một mô hình đầy đủ như `ev-node` full node (sequencer, execution, sync, DA, RPC).

## 1) Trạng thái hiện tại

Hiện repo có 2 đường chạy tách rời:

1. **Đường A (script WASM standalone)**
  - `scripts/run-wasm-full-node.sh` chạy trực tiếp 2 container `wasmd` (sequencer/fullnode).
  - Deploy/submit tx gọi CLI `wasmd` trong container.
  - Luồng này **không đi qua** pipeline `node -> block -> execution.Executor` của ev-node.

2. **Đường B (ev-node architecture)**
  - `node.NewNode` cần `core/execution.Executor` + `core/sequencer.Sequencer` + DA + P2P.
  - Runtime chuẩn đầy đủ: `Executor`, `Reaper`, `Syncer`, `Submitter`, RPC service.
  - `apps/cosmos-exec` hiện tại chỉ là **ABCI app/server độc lập**, chưa là adapter `core/execution.Executor`.

## 2) Mục tiêu kiến trúc thống nhất

Mục tiêu: Cosmos/WASM phải chạy trong **một pipeline thống nhất kiểu ev-node full node**:

```text
Tx ingress
  -> Reaper (GetTxs)
  -> Sequencer (single/based)
  -> Executor (CreateBlock/ApplyBlock)
  -> Cosmos Execution Engine (ABCI/Cosmos SDK + CosmWasm)
  -> Store + P2P broadcast
  -> Submitter (DA submit + inclusion/finality)
  -> Syncer (DA + P2P catchup on followers)
  -> RPC (Connect-RPC + HTTP health)
```

Trong mô hình này:
- **cosmos-exec** là execution engine thực thi state transition.
- **WASM contract flow** (store/instantiate/execute/query) đi qua execution layer đó, thay vì chạy `wasmd` standalone tách rời.
- sequencer/full node semantics bám đúng `ev-node` (`aggregator` và `sync` roles).

## 3) Thành phần cần có để đạt mục tiêu

### 3.1 Execution Adapter (bắt buộc)

Thiếu mảnh ghép chính là một implementation của `core/execution.Executor` cho Cosmos:

- `InitChain`
- `GetTxs`
- `ExecuteTxs`
- `SetFinal`
- `GetExecutionInfo`
- `FilterTxs`

Đề xuất 2 phương án:

1. **ABCI direct adapter** (`execution/cosmosabci`)  
  ev-node gọi trực tiếp ABCI app (in-process hoặc socket client).

2. **gRPC bridge adapter** (`execution/grpc` + cosmos executor service)  
  cosmos-exec expose gRPC executor API, ev-node dùng `execution/grpc.NewClient(...)`.

> Khuyến nghị: đi theo phương án 2 trước (reuse interface sẵn có, ít đụng core).

### 3.2 Cosmos app có CosmWasm module

`apps/cosmos-exec` hiện mới tối giản (`auth` + `bank`). Để chạy WASM thực sự cần:

- Thêm module `x/wasm` vào app wiring.
- Thêm keeper/config cần thiết cho wasm runtime.
- Bổ sung tx/query route tương ứng.

### 3.3 Runner thống nhất kiểu full node

Cần một app runner kiểu `apps/grpc` nhưng target Cosmos/WASM:

- parse config
- tạo execution client (ABCI/gRPC bridge)
- tạo sequencer (`single`/`based`)
- `node.NewNode(...)` để chạy đầy đủ `Executor + Syncer + Submitter + RPC`

## 4) Luồng runtime chuẩn sau khi thống nhất

### 4.1 Aggregator (sequencer node)

1. `Reaper` lấy tx từ execution mempool (`GetTxs`) và đẩy vào sequencer queue.
2. `Executor` gọi `sequencer.GetNextBatch` để lấy batch (mempool + forced-inclusion).
3. `Executor.ApplyBlock` gọi execution (`ExecuteTxs`) để cập nhật app hash.
4. Block commit vào store, broadcast qua P2P (header trước, data sau).
5. `Submitter` đẩy header/data lên DA, theo dõi inclusion, `SetFinal`.

### 4.2 Full node sync-only

1. `Syncer` nhận block từ DA follower + P2P.
2. Validate header/data + forced-inclusion checks.
3. Gọi execution `ExecuteTxs` để áp state local.
4. Persist height/state, theo kịp DA/P2P head.

### 4.3 RPC/health

- Connect-RPC services: block/state/p2p/config.
- HTTP endpoints: `/health/live`, `/health/ready`, `/da/*`.

## 5) Lộ trình triển khai đề xuất (thực tế)

### Phase 1 (MVP integration)

1. Tạo execution service cho cosmos-exec theo gRPC executor interface.
2. Tạo app runner `apps/cosmos-wasm` (hoặc mở rộng `apps/grpc`) để chạy ev-node với executor đó.
3. Giữ script hiện tại để smoke test nhanh, nhưng thêm script mới để chạy kiến trúc thống nhất.

Kết quả đạt được:
- Sequencer/full node chạy bằng ev-node pipeline chuẩn.
- Cosmos execution và WASM flow nằm chung một kiến trúc.

### Phase 2 (feature parity)

1. Bổ sung đầy đủ wasm module lifecycle và governance params.
2. Bổ sung observability metrics/tracing riêng cho cosmos executor.
3. E2E tests: tx -> block -> DA inclusion -> finality -> query state.

### Phase 3 (hardening)

1. Crash recovery + replay tests.
2. Based sequencer + forced inclusion tests cho cosmos/wasm tx.
3. Performance bench cho `FilterTxs`, mempool scrape, execute latency.

## 6) Runbook tạm thời hiện tại

Trong khi chưa có adapter execution thống nhất, bạn có 2 lựa chọn:

- **Nhanh để dev contract:** dùng script `wasmd` standalone (đường A).
- **Đúng kiến trúc full node ev-node:** dùng app kiểu `apps/grpc` với executor service tương thích (đường B).

## 7) Kết luận

Để “cosmos-exec, wasm đi cùng nhau và đầy đủ như ev-node full node”, cần biến Cosmos/WASM thành **execution backend chính thức của `core/execution.Executor`** (ưu tiên qua gRPC bridge), sau đó chạy qua `node.NewNode(...)` thay vì `wasmd` standalone script.
