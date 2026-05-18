# Vận hành node: sequencer, full node, data on/off-chain, biến ở đâu

Tài liệu này mô tả **cách stack khởi chạy thực tế**, **dữ liệu nằm đâu** (on-chain Celestia vs local), và **mọi biến cấu hình đọc từ chỗ nào** — bám đúng `scripts/run-cosmos-wasm-nodes.go` và code.

> Liên quan: [sequencer-security.md](sequencer-security.md) (single vs based), [chain-flow.md](chain-flow.md) (vòng đời tx/block), [fee-economics.md](fee-economics.md).

## 1. Mỗi node = 2 tiến trình

```
┌─ node "sequencer" ──────────────┐   ┌─ node "fullnode" ───────────────┐
│ evcosmos (ev-node runtime)      │   │ evcosmos (ev-node runtime)      │
│   aggregator=TRUE               │   │   aggregator=FALSE              │
│   produce block, ký, submit DA  │   │   sync block từ DA + P2P        │
│        │ grpc-executor-url       │   │        │ grpc-executor-url       │
│        ▼                         │   │        ▼                         │
│ cosmos-exec-grpc (execution)    │   │ cosmos-exec-grpc (execution)    │
│   chạy WASM, state, mempool     │   │   chạy lại tx để verify state   │
└─────────────────────────────────┘   └─────────────────────────────────┘
        sequencer.P2P  ◄───── peers ─────►  fullnode.P2P
                  cả hai cùng đọc/ghi 1 Celestia DA
```

- **evcosmos** = binary ev-node runtime (block production/sync, ký header, submit/đọc DA, P2P). Khác nhau **chỉ ở cờ** `--evnode.node.aggregator`.
- **cosmos-exec-grpc** = lớp execution (CosmWasm runtime + state + mempool + persist). evcosmos gọi nó qua `--grpc-executor-url`.

Sequencer và full node chạy **cùng bộ binary**, chỉ khác: aggregator true/false và full node có thêm `--evnode.p2p.peers <sequencer>`.

## 2. Trình tự khởi chạy (từ run script)

`scripts/run-cosmos-wasm-nodes.go` → `run()`:

```
1. findProjectRoot + loadDotEnv(.env)
2. resolveDAFromEnv()      → đọc DA_* env
3. validateDAConfig + preflightDA   → kiểm Celestia reachable/authorized
4. preparePaths()          → tạo home dirs + passphrase file
5. ensureBinaries()        → go build evcosmos, cosmos-exec-grpc
6. initNodes()             → evcosmos init (mỗi node), copy genesis seq→full
7. startExecutionServices()→ cosmos-exec-grpc cho cả 2 node (chờ gRPC healthy)
8. startSequencer()        → evcosmos start --evnode.node.aggregator=true
9. startFullNode()         → evcosmos start --aggregator=false --p2p.peers=<seq>
10. waitForChainSync()     → chờ seq + full vào cửa sổ đồng bộ
11. monitorProcesses()
```

Thứ tự bắt buộc: **execution service phải sẵn trước** khi evcosmos start (evcosmos cần executor để ExecuteTxs ngay block đầu). Full node start **sau** sequencer vì nó cần địa chỉ P2P của sequencer (`getNodePeerAddress`).

## 3. Cổng mặc định

| | Sequencer | Full node |
|---|---|---|
| Execution gRPC (`cosmos-exec-grpc`) | 50051 | 50052 |
| ev-node RPC (`--evnode.rpc.address`) | 38331 | 48331 |
| P2P (`--evnode.p2p.listen_address`) | 7860 | 7861 |

(web app proxy mặc định trỏ `BACKEND_URL` → 50051, `EVNODE_RPC_URL` → 38331.)

## 4. Dữ liệu nằm đâu — on-chain vs off-chain

### On-chain (Celestia DA) — nguồn chân lý

Mỗi block, ev-node submit lên Celestia dưới namespace (mặc định `rollup`):

- **Header blob**: signed header (height, time, app_hash, data_hash, proposer + chữ ký sequencer).
- **Data blob**: tx bytes của block (rỗng → không có data blob).

Đây là **dữ liệu duy nhất mang tính đồng thuận**: bất kỳ ai cũng tải từ Celestia về chạy lại để verify (xem [sequencer-security.md](sequencer-security.md)).

### Off-chain (local trên máy node) — cache/state để chạy

Hai thư mục home **riêng cho mỗi node**, đặt ở project root:

| Thư mục | Của | Chứa gì |
|---------|-----|---------|
| `.evcosmos-<name>/` | ev-node runtime | `config/genesis.json`, node config (yaml), signer key (passphrase từ `--evnode.signer.passphrase_file`), **store** (badger: headers, data, state, DA height đã xử lý) |
| `.cosmos-exec-<name>/data/` | execution (`cosmos-exec-grpc --home`) | `metadata.json` (chainID, stateRoot, heights — ghi atomic+fsync), `tx_results.jsonl` (append-only kết quả tx), `blocks.jsonl` (append-only block info), + CosmWasm state store |

`<name>` = `sequencer` hoặc `fullnode`. File bạn vừa mở `.cosmos-exec-fullnode/data/blocks.jsonl` chính là **block index off-chain của full node** (PersistStore, không phải dữ liệu đồng thuận — tái dựng được từ DA).

> Hệ quả: xóa các thư mục local = mất cache, **không mất chain** (sync lại từ Celestia). Nhưng đổi genesis/treasury chỉ áp khi **chưa có** state local — nên phải `--clean-on-start` (xóa home) để genesis mới có hiệu lực.

## 5. Mọi biến đọc từ đâu

Bốn nhóm cấu hình, bốn nguồn khác nhau:

| Nhóm | Ví dụ | Đọc ở đâu | Định nghĩa |
|------|-------|-----------|------------|
| **ev-node flags** | `--evnode.node.aggregator`, `--evnode.node.based_sequencer`, `--evnode.node.block_time`, `--evnode.node.lazy_mode`, `--evnode.da.namespace`, `--evnode.rpc.address`, `--evnode.p2p.peers`, `--evnode.signer.passphrase_file` | CLI cờ truyền cho `evcosmos` | `pkg/config/config.go` (tên cờ), `pkg/config/defaults.go` (default) |
| **DA env** | `DA_RPC` / `DA_BRIDGE_RPC`, `DA_AUTH_TOKEN`, `DA_NAMESPACE` (default `rollup`) | `.env` ở project root → `resolveDAFromEnv()` | `scripts/run-cosmos-wasm-nodes.go` |
| **cosmos-exec env** | `COSMOS_EXEC_ENFORCE_SIGNATURES`, `COSMOS_EXEC_TIA_PER_BYTE`, `COSMOS_EXEC_MIN_GAS_PRICE`, `COSMOS_EXEC_GAS_DENOM`, `COSMOS_EXEC_TREASURY_PRIVKEY_HEX`, `COSMOS_EXEC_TREASURY_AMOUNT`, `COSMOS_EXEC_FAUCET_AMOUNT`, `COSMOS_EXEC_FAUCET_GAS`, `COSMOS_EXEC_FAUCET_COOLDOWN_SECONDS` | Biến môi trường lúc start `cosmos-exec-grpc` | `app/ante.go` (enforce sig), `cmd/cosmos-exec-grpc/cost.go` (giá), `cmd/cosmos-exec-grpc/faucet.go` (treasury/faucet) |
| **cosmos-exec flags** | `--address`, `--home`, `--profile` (dev/test/prod), `--in-memory`, `--log-level` | CLI cờ truyền cho `cosmos-exec-grpc` | `cmd/cosmos-exec-grpc/main.go` |
| **run-script flags** | `--clean-on-start`, `--clean-on-exit`, `--block-time`, `--submit-interval`, `--chain_id`, `--log-level` | CLI cờ cho `run-cosmos-wasm-nodes.go` | `scripts/run-cosmos-wasm-nodes.go` |

Quy tắc nhớ nhanh:

- Cái gì thuộc **đồng thuận / mạng / DA / sequencer mode** → cờ `--evnode.*` (định nghĩa ở `pkg/config`).
- Cái gì thuộc **execution / kinh tế phí / faucet** → env `COSMOS_EXEC_*` (đọc trong package `app` và `cmd/cosmos-exec-grpc`).
- Cái gì thuộc **endpoint Celestia** → env `DA_*` (chỉ run-script đọc, rồi forward thành `--evnode.da.*`).
- Cái gì thuộc **lifecycle dev (xóa data, block time demo)** → cờ của run-script.

## 6. Sequencer khác full node — tóm tắt

| | Sequencer | Full node |
|---|---|---|
| `--evnode.node.aggregator` | `true` | `false` |
| Vai trò | Tạo block, ký, **submit lên DA** | Đọc block từ **DA + P2P**, chạy lại verify |
| `--evnode.p2p.peers` | (không) | trỏ tới sequencer |
| Mempool | có (single sequencer) | không (chỉ verify) |
| Mất nó thì | chain **ngừng ra block** | chỉ mất một bản sao verify; chain vẫn chạy |

Chi tiết bảo mật của mô hình này: [sequencer-security.md](sequencer-security.md).

## 7. Có phải trả tiền cho sequencer không?

**Không — không có khoản trả cho sequencer ở cấp giao thức.** Sequencer khác hẳn validator của Cosmos chain thường:

- **Không staking reward, không lạm phát, không sequencer tip.** Không có `x/mint`, không có code path phân phối thưởng cho sequencer (xem [fee-economics.md mục 2a](fee-economics.md)).
- Single sequencer = **hạ tầng của chính operator**: chỉ là tiến trình `evcosmos --aggregator=true` + `cosmos-exec-grpc` chạy trên máy bạn. Bạn **vận hành** nó, không **trả** nó.

Cái thực sự tốn tiền khi vận hành sequencer:

| Khoản | Trả cho ai |
|-------|-----------|
| Server/hạ tầng | Cloud/VPS chạy 2 tiến trình |
| **Blob fee Celestia** | Celestia (TIA) — sequencer submit header/data blob mỗi block; trả bằng key ký DA (`DA_AUTH_TOKEN` / signing address trong DA config) |

→ Không trả "phí sequencer", mà trả **hóa đơn Celestia do sequencer phát sinh**. Operator gánh (có thể tính vào fee user nếu bật fee).

Phí giao dịch user (khi bật fee) chảy vào module account `fee_collector` — **không** tự động payout cho sequencer như validator nhận block reward; operator quản khoản này để bù hóa đơn DA.

**Based sequencer** (`--evnode.node.based_sequencer=true`): không có tiến trình sequencer sắp xếp tx → càng không có khái niệm "trả cho sequencer", chỉ còn chi phí DA.

## Tham chiếu code

| Chủ đề | File |
|--------|------|
| Khởi chạy stack, ports, DA env, home dirs | `scripts/run-cosmos-wasm-nodes.go` |
| Tên cờ + default ev-node | `pkg/config/config.go`, `pkg/config/defaults.go` |
| Persist off-chain (metadata/tx/blocks) | `apps/cosmos-exec/executor/persist.go` |
| Genesis (treasury) | `apps/cosmos-exec/app/genesis.go` |
| Env execution/phí/faucet | `apps/cosmos-exec/app/ante.go`, `cmd/cosmos-exec-grpc/cost.go`, `cmd/cosmos-exec-grpc/faucet.go` |
| Flags cosmos-exec-grpc | `apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go` |
