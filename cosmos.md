# Cosmos + WASM (Current Runbook)

Tài liệu này chỉ giữ **trạng thái hiện tại** và các lệnh có thể chạy ngay.

## 1) Những gì đang chạy được

- Chạy `evcosmos` full stack qua runner: sequencer + fullnode + `cosmos-exec-grpc` + DA submit sidecar.
- Health endpoint của 2 node.
- RPC đọc block/tx/state qua script `scripts/contracts/wasm-rpc.sh` (backend là `tools/evnode-rpc`).

## 2) Chuẩn bị `.env`

Yêu cầu các biến sau trong `.env`:

- `DA_BRIDGE_RPC` hoặc `DA_RPC`
- `DA_AUTH_TOKEN`
- `DA_NAMESPACE`
- `COSMOS_DA_UPLOAD_MODE=engram`
- `COSMOS_DA_SUBMIT_API` hoặc `ENGRAM_API_BASE`
- `ENGRAM_NAMESPACE`

## 3) Chạy full stack

Chạy các lệnh bên dưới tại thư mục root repo `ev-node`.

### 3.1 Trường hợp A: chạy mới (clean start)

Dùng khi muốn reset node data và chạy lại từ đầu:

```bash
set -a && source .env && set +a
export COSMOS_DA_UPLOAD_MODE=engram
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=true
```

Mẫu 1 dòng:

```bash
set -a && source .env && set +a && export COSMOS_DA_UPLOAD_MODE=engram && go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=true
```

### 3.2 Trường hợp B: chạy tiếp (resume data hiện có)

Dùng khi muốn tiếp tục từ dữ liệu đang có (không xóa `.evcosmos-*` / `.cosmos-exec-*`):

```bash
set -a && source .env && set +a
export COSMOS_DA_UPLOAD_MODE=engram
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=false
```

Mẫu 1 dòng:

```bash
set -a && source .env && set +a && export COSMOS_DA_UPLOAD_MODE=engram && go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=false
```

### 3.3 Nếu bị báo port đang dùng

Runner hiện fail-fast nếu các port chính đang bị process cũ giữ (`50051/50052/38331/48331/7860/7861`).

Khi gặp lỗi này, dọn process cũ rồi chạy lại:

```bash
pkill -f cosmos-exec-grpc || true
pkill -f evcosmos || true

set -a && source .env && set +a
export COSMOS_DA_UPLOAD_MODE=engram
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=false
```

Runner chạy foreground, mở đủ process cần thiết.

### 3.4 Cách dừng khi đang chạy

Nếu đang chạy foreground trong terminal:

```bash
# nhấn trong terminal đang chạy runner
Ctrl+C
```

Nếu terminal bị treo hoặc cần dừng cưỡng bức:

```bash
pkill -f run-cosmos-wasm-nodes.go || true
pkill -f cosmos-exec-grpc || true
pkill -f evcosmos || true
```

## 4) Verify nhanh sau khi start

```bash
curl http://127.0.0.1:38331/health/live
curl http://127.0.0.1:48331/health/live
```

Kỳ vọng: cả 2 trả về `OK`.

Trong log runner, kỳ vọng có dòng submit sidecar:

```text
[cosmos-da-submit] [ok][da-submitter] engram_submit ... status=200
```

## 5) API block/tx/state (không dùng `wasmd` standalone)

Script dùng:

```bash
./scripts/contracts/wasm-rpc.sh
```

Các lệnh chạy ngay:

```bash
# trạng thái chain
./scripts/contracts/wasm-rpc.sh status

# block mới nhất
./scripts/contracts/wasm-rpc.sh latest-block

# block theo height
./scripts/contracts/wasm-rpc.sh block --height 100

# tìm tx theo hash (scan ngược từ latest)
./scripts/contracts/wasm-rpc.sh tx --hash <HEX_TX_HASH>

# ví dụ hash thật
./scripts/contracts/wasm-rpc.sh tx --hash 7e9c7e32ebada67101adc9db71d2dc9e3a49e223ffb4707e4af1bd86febb9b7e

# list tx raw trong 1 block
./scripts/contracts/wasm-rpc.sh txs --height 100
```

Mẫu query tx vừa submit thành công:

```bash
./scripts/contracts/wasm-rpc.sh tx --hash 489cff18c1e56d2a112f35086bb148de84d445049dd96744937aa60984d254cf
```

Config endpoint RPC:

- `EVNODE_RPC_URL` (ưu tiên)
- hoặc `WASM_RPC_URL`
- hoặc `NODE`
- mặc định: `http://127.0.0.1:38331`

## 6) Deploy/submit/execute/query contract (full-stack)

Script dùng:

```bash
./scripts/contracts/wasm-contract.sh
```

Các lệnh chạy ngay:

```bash
# deploy (store + instantiate), tự lưu CONTRACT_ADDR vào DEPLOY_OUTPUT_FILE
./scripts/contracts/wasm-contract.sh deploy

# submit raw tx (base64)
./scripts/contracts/wasm-contract.sh submit --tx-base64 <TX_BASE64>

# execute contract
./scripts/contracts/wasm-contract.sh execute --contract <CONTRACT_ADDR> --msg '{"transfer":{"recipient":"cosmos1...","amount":"1"}}'

# query smart contract
./scripts/contracts/wasm-contract.sh query --contract <CONTRACT_ADDR> --msg '{"balance":{"address":"cosmos1..."}}'
```

Mẫu đầy đủ đã test: submit tx rồi query tx đó

```bash
# deploy contract
./scripts/contracts/wasm-contract.sh deploy

# lấy info deploy
source /tmp/ev-node-wasm/last-deploy.env

# lấy sender mặc định
SENDER=$(cd ./apps/cosmos-exec && go run ./cmd/cosmos-wasm-tx default-sender)

# build raw execute tx (base64)
TX_BASE64=$(cd ./apps/cosmos-exec && go run ./cmd/cosmos-wasm-tx execute \
	--sender "$SENDER" \
	--contract "$CONTRACT_ADDR" \
	--msg '{"transfer":{"recipient":"'"$CONTRACT_ADDR"'","amount":"1"}}' \
	--out base64)

# submit raw tx
SUBMIT_JSON=$(./scripts/contracts/wasm-contract.sh submit --tx-base64 "$TX_BASE64")
echo "$SUBMIT_JSON"

# query tx vừa submit bằng API block/tx
TX_HASH=$(echo "$SUBMIT_JSON" | jq -r '.hash')
./scripts/contracts/wasm-rpc.sh tx --hash "$TX_HASH"
```

Ví dụ hash thực tế từ lần test gần nhất:

```bash
./scripts/contracts/wasm-rpc.sh tx --hash 489cff18c1e56d2a112f35086bb148de84d445049dd96744937aa60984d254cf
```

Config endpoint contract API:

- `COSMOS_EXEC_API_URL` (mặc định: `http://127.0.0.1:50051`)

## 7) Lưu ý vận hành

- Nếu `wasm-rpc.sh status` báo `connection refused`, nghĩa là stack chưa chạy hoặc chưa listen ở `:38331`.
- Nếu `wasm-contract.sh` báo lỗi connect `:50051`, nghĩa là execution backend chưa chạy.
- Nếu thấy lỗi DA submit trực tiếp từ `evcosmos` kiểu `insufficient funds ... utia`, cần nạp thêm phí cho address submit lên Celestia.
- Dù vậy, sidecar Engram vẫn có thể hoạt động nếu log còn `engram_submit ... status=200`.

## 8) Legacy fallback

- Legacy `wasmd` standalone không còn là đường mặc định.
- Nếu cần fallback script cũ, dùng trực tiếp:

```bash
./scripts/deploy-sample-contract.sh
./scripts/submit-tx.sh
```