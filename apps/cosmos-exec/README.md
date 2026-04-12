# cosmos-exec

`cosmos-exec` currently provides two entrypoints:

- `cmd/cosmos-exec`: standalone ABCI socket server.
- `cmd/cosmos-exec-grpc`: gRPC execution service compatible with `core/execution.Executor` via `execution/grpc`.

## Hai chế độ chạy

### Mode 1: API-only (không có block production)

```bash
cd apps/cosmos-exec
go run ./cmd/cosmos-exec-grpc --address 0.0.0.0:50051 --in-memory
```

Chế độ này chạy **execution service đơn lẻ** — không có sequencer, không có block production.

**Dùng được:**
- Blob store: `/blob/submit`, `/blob/retrieve`, `/blob/batch`
- Cost estimate: `/blob/estimate-cost`
- WASM query: `/wasm/query-smart` (nếu có contract đã deploy)
- Swagger docs: `/swagger`

**Không hoạt động đầy đủ:**
- `/tx/submit` — tx vào mempool nhưng **không bao giờ được execute** (không ai gọi `ExecuteTxs`)
- `/tx/{hash}` — mãi trả `status=pending`, `found=false`
- `/blocks/latest` — không có block nào được tạo
- `/status` — `initialized=false` (chưa được sequencer init)

### Mode 2: Full stack — sequencer + full node + block production (E2E)

Chế độ này chạy **đầy đủ**: sequencer tạo block, full node đồng bộ, transaction được execute thật.

```
┌─────────┐    ┌──────────────────┐    ┌────────────────┐    ┌────────────┐
│ bạn     │───▸│ cosmos-exec-grpc │◂──▸│ evcosmos       │───▸│ Celestia   │
│ (curl)  │◂──▸│ :50051           │    │ (sequencer)    │    │ DA layer   │
└─────────┘    │ execution svc    │    │ :38331 RPC     │    └────────────┘
               └──────────────────┘    │ :7860 P2P      │
                                       └───────┬────────┘
               ┌──────────────────┐            │ P2P gossip
               │ cosmos-exec-grpc │    ┌───────▼────────┐
               │ :50052           │◂──▸│ evcosmos       │
               │ execution svc    │    │ (full node)    │
               └──────────────────┘    │ :48331 RPC     │
                                       │ :7861 P2P      │
                                       └────────────────┘
```

**Luồng dữ liệu chi tiết:**

```
 ①  bạn ── POST /tx/submit ──▸ cosmos-exec-grpc
      tx vào mempool (InjectTx)

 ②  evcosmos (sequencer) mỗi block-time (2s):
      ├─ GetTxs()         ← lấy tx từ mempool
      ├─ FilterTxs()      ← lọc tx hợp lệ (size, gas)
      ├─ ExecuteTxs()     ← ABCI: BeginBlock → DeliverTx → EndBlock → Commit
      │    ├─ tx result lưu vào txResults map
      │    └─ block info lưu vào blocks map
      ├─ SetFinal()       ← đánh dấu block finalized
      └─ DA submit        ← header + data → Celestia blob

 ③  evcosmos (full node):
      ├─ Nhận block qua P2P gossip từ sequencer
      ├─ Verify header DA proof từ Celestia
      ├─ ExecuteTxs()     ← re-execute cùng tx set
      └─ SetFinal()       ← finalize khi DA confirmed

 ④  bạn ── GET /tx/{hash} ──▸ cosmos-exec-grpc
      ├─ pending  (tx đang trong mempool, chưa execute)
      ├─ success  (execute xong, code=0)
      └─ failed   (execute xong, code≠0)
```

## E2E có execute thật (tx ra `found=true`)

> **Tại sao cần E2E?** — `/tx/submit` chỉ đưa tx vào mempool. Tx chỉ có result
> (`found=true`) khi sequencer gọi `ExecuteTxs` trong chu kỳ block production.
> Chạy `cosmos-exec-grpc --in-memory` đơn lẻ **không có block production** →
> tx mãi ở trạng thái `pending`.

### Chuẩn bị

Cần file `.env` ở project root với Celestia DA config:

```bash
# .env (ví dụ dùng Celestia light node local)
DA_BRIDGE_RPC=http://localhost:26658
DA_AUTH_TOKEN=<celestia-auth-token>
DA_NAMESPACE=rollup
```

### 1) Start full stack (sequencer + full node + 2 execution services)

**Terminal 1** — từ project root:

```bash
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go \
  --clean-on-start=true \
  --clean-on-exit=false \
  --block-time=2s
```

Chờ đến khi thấy log:
```
Cosmos/WASM stack is running
- sequencer execution gRPC: http://127.0.0.1:50051
- full execution gRPC: http://127.0.0.1:50052
```

### 2) Kiểm tra node đã sẵn sàng

**Terminal 2**:

```bash
# Node status — chờ initialized=true, healthy=true
curl -sS http://127.0.0.1:50051/status | python3 -m json.tool

# Block đang được produce — latest_height tăng dần
curl -sS http://127.0.0.1:50051/blocks/latest | python3 -m json.tool
```

Output mong đợi:
```json
{
    "initialized": true,
    "chain_id": "cosmos-wasm-local",
    "latest_height": 5,
    "finalized_height": 3,
    "healthy": true,
    "synced": true
}
```

### 3) Submit tx và theo dõi lifecycle

```bash
# Submit blob (đơn giản nhất, không cần contract)
TX_HASH=$(curl -sS -X POST http://127.0.0.1:50051/blob/submit \
  -H 'Content-Type: application/json' \
  -d '{"data_base64":"SGVsbG8gRTJFIQ=="}' | python3 -c "import sys,json; print(json.load(sys.stdin).get('commitment',''))")
echo "commitment: $TX_HASH"

# Hoặc submit raw tx (cần tx bytes hợp lệ từ SDK)
# curl -sS -X POST http://127.0.0.1:50051/tx/submit \
#   -H 'Content-Type: application/json' \
#   -d '{"tx_hex":"<hex-encoded-tx-bytes>"}'
```

### 4) Deploy contract thật + full lifecycle

```bash
cd apps/cosmos-exec

# Deploy reflect contract → execute → query → blob store → Merkle proof
go run ./sdk/cosmoswasm/examples/deploy-contract
```

Output mong đợi:
```
Step 1 — Store WASM code (reflect contract)
  submitted tx_hash=e3e728...
  waiting for execution...
  store tx success at height=26, code_id from events

Step 2 — Instantiate reflect contract (code_id=1)
  submitted tx_hash=fecf4b...
  waiting for execution...
  contract deployed: cosmos14hj2tav... (height=27)

Step 3 — Execute: change_owner
  submitted tx_hash=a5d123...
  waiting for execution...
  execute success at height=28

Step 4 — Query: owner
  query result: {"owner":"cosmos1..."}

Step 5 — Store 3 blobs off-chain + Merkle proof
  blob[0] → a1b2c3d4e5f6... (28 bytes)
  blob[1] → f6e5d4c3b2a1... (45 bytes)
  blob[2] → 1234567890ab... (39 bytes)
  retrieved blob[0]: {"event":"game_start","ts":1}
  Merkle proof verified for blob[1] in root=abcdef012345…

Step 6 — Cost estimate
  1 MB data:
    direct on-chain: 50598440 gas
    blob + commit:   4464360 gas (91% cheaper)

All steps passed.
```

### 5) Contract interaction — 2 contracts + blob + tx lifecycle

```bash
cd apps/cosmos-exec

# Deploy hackatom + reflect → execute → query → blob store → Merkle proof → tx lifecycle
go run ./sdk/cosmoswasm/examples/contract-interaction
```

Example này demo **đầy đủ**:

| Part | Nội dung |
|------|----------|
| A | **Hackatom contract**: store code → instantiate (verifier=alice, beneficiary=bob) → query verifier |
| B | **Reflect contract**: store code → instantiate → query owner → change_owner → verify owner changed |
| C | **Blob-first**: store 5 game events off-chain → batch submit → Merkle proof → retrieve → cost estimate |
| D | **Tx lifecycle**: successful tx (change_owner) + failed tx (unauthorized) → `pending` → `success`/`failed` |

### 6) Các example khác

```bash
cd apps/cosmos-exec

# Quickstart — blob store + Merkle proof + cost estimate (không cần contract)
go run ./sdk/cosmoswasm/examples/quickstart

# Game telemetry — BatchBuilder + auto-flush + compression
# (dùng CONTRACT_ADDR nếu có contract từ step 4)
CONTRACT_ADDR=cosmos14hj2tav... go run ./sdk/cosmoswasm/examples/game-telemetry
```

Ghi lại `txHash` từ log output.

### 7) Query tx result — chờ `found=true`

```bash
# Dùng API mới /tx/{hash} — trả status rõ ràng (pending/success/failed)
curl -sS "http://127.0.0.1:50051/tx/<TX_HASH>" | python3 -m json.tool
```

**Lifecycle của tx:**

```
submit → pending (trong mempool)
       → success (ExecuteTxs xong, code=0)
       → failed  (ExecuteTxs xong, code≠0)
```

Output khi thành công:
```json
{
    "hash": "a1b2c3...",
    "status": "success",
    "found": true,
    "height": 7,
    "code": 0,
    "log": "",
    "events": [...]
}
```

### 8) Query block chứa tx

```bash
# Block mới nhất
curl -sS http://127.0.0.1:50051/blocks/latest | python3 -m json.tool

# Block theo height (lấy height từ tx result)
curl -sS http://127.0.0.1:50051/blocks/7 | python3 -m json.tool
```

### Quick monitoring

```bash
# Xem mempool có bao nhiêu tx đang chờ
curl -sS http://127.0.0.1:50051/tx/pending | python3 -m json.tool

# So sánh sequencer vs full node
curl -sS http://127.0.0.1:50051/status | python3 -c "import sys,json; d=json.load(sys.stdin); print(f'seq: height={d[\"latest_height\"]} finalized={d[\"finalized_height\"]}')"
curl -sS http://127.0.0.1:50052/status | python3 -c "import sys,json; d=json.load(sys.stdin); print(f'full: height={d[\"latest_height\"]} finalized={d[\"finalized_height\"]}')"

# Swagger UI — mở trong browser
open http://127.0.0.1:50051/swagger
```

### Troubleshooting: tx vẫn `pending` / `found=false`

| Triệu chứng | Nguyên nhân | Fix |
|---|---|---|
| `found=false` mãi | Query nhầm instance (API-only thay vì E2E stack) | Đảm bảo dùng port `50051` của stack runner |
| `pending_count` không giảm | Sequencer chưa chạy hoặc đã crash | Kiểm tra terminal 1 — restart nếu cần |
| `initialized=false` | Execution service chưa được init bởi sequencer | Chờ sequencer connect (vài giây sau khi start) |
| `synced=false` | Full node đang catch up | Chờ `finalized_height` tiến gần `latest_height` |
| `status=failed`, `code≠0` | Tx bị reject bởi Cosmos SDK (gas, auth, v.v.) | Xem field `log` để biết lý do |
| DA preflight failed | Celestia node không chạy hoặc token sai | Kiểm tra `.env` — `DA_BRIDGE_RPC`, `DA_AUTH_TOKEN` |

### Dừng stack

`Ctrl+C` ở terminal 1. Runner sẽ SIGTERM tất cả process con.

Nếu process zombie còn sót:
```bash
pkill -f cosmos-exec-grpc; pkill -f evcosmos
```

## Notes

- Current app wiring in `app/app.go` includes `auth`, `bank`, `x/params`, `x/capability`, IBC core, IBC transfer, and `x/wasm` (`WasmKeeper` + `wasm AppModule`).
- `WasmKeeper` is wired to real IBC keepers (`IBCKeeper.ChannelKeeper`, `IBCKeeper.PortKeeper`, scoped capabilities, `TransferKeeper`).
- This is a bridge skeleton to unblock `apps/cosmos-wasm` full-node flow.
- Full production parity still requires wiring real staking/distribution keepers for wasm query plugin behavior and e2e verification of contract lifecycle.

## Go SDK for dApp users

Public Go SDK cho Cosmos WASM nằm tại [sdk/cosmoswasm](sdk/cosmoswasm/README.md).

SDK hỗ trợ:

- Build raw tx (`store`, `instantiate`, `execute`)
- Submit tx (`/tx/submit`) & query result (`/tx/{hash}`, `/tx/result`)
- Query smart contract (`/wasm/query-smart`)
- Blob store (`/blob/submit`, `/blob/retrieve`, `/blob/batch`)
- Cost estimation (`/blob/estimate-cost`)
- Node monitoring (`/status`, `/blocks/latest`, `/blocks/{height}`, `/tx/pending`)

## HTTP API Reference

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/status` | Node health, sync state, chain info |
| GET | `/blocks/latest` | Latest block (height, time, app_hash, num_txs) |
| GET | `/blocks/{height}` | Block by height |
| POST | `/tx/submit` | Submit signed tx to mempool |
| GET | `/tx/{hash}` | Tx detail with status (pending/success/failed) |
| GET | `/tx/result?hash=` | Tx result (legacy, found/not found) |
| GET | `/tx/pending` | Mempool pending tx count |
| POST | `/wasm/query-smart` | Query CosmWasm contract |
| POST | `/blob/submit` | Store single blob, get SHA-256 commitment |
| GET | `/blob/retrieve?commitment=` | Retrieve blob by commitment |
| POST | `/blob/batch` | Store N blobs, get Merkle root |
| POST | `/blob/estimate-cost` | Compare direct-tx vs blob+commit gas |
| GET | `/swagger` | Swagger UI (interactive docs) |
| GET | `/swagger.json` | OpenAPI 3.0.3 spec |
