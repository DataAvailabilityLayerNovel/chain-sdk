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
- `COSMOS_DA_SUBMIT_API` hoặc `ENGRAM_API_BASE`
- `ENGRAM_NAMESPACE`

Runner hiện submit song song lên:

- Engram API (`COSMOS_DA_SUBMIT_API` / `ENGRAM_API_BASE`)
- Celestia trực tiếp qua `DA_BRIDGE_RPC` (fallback `DA_RPC`)

Lưu ý: endpoint submit Celestia cần hỗ trợ blob JSON-RPC methods (`blob.*`), thường là bridge RPC.

## 3) Chạy full stack

Chạy các lệnh bên dưới tại thư mục root repo `ev-node`.

### 3.1 Trường hợp A: chạy mới (clean start)

Dùng khi muốn reset node data và chạy lại từ đầu:

```bash
set -a && source .env && set +a
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=true
```

Mẫu 1 dòng:

```bash
set -a && source .env && set +a && go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=true
```

### 3.2 Trường hợp B: chạy tiếp (resume data hiện có)

Dùng khi muốn tiếp tục từ dữ liệu đang có (không xóa `.evcosmos-*` / `.cosmos-exec-*`):

```bash
set -a && source .env && set +a
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=false
```

Mẫu 1 dòng:

```bash
set -a && source .env && set +a && go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=false
```

### 3.3 Nếu bị báo port đang dùng

Runner hiện fail-fast nếu các port chính đang bị process cũ giữ (`50051/50052/38331/48331/7860/7861`).

Khi gặp lỗi này, dọn process cũ rồi chạy lại:

```bash
pkill -f cosmos-exec-grpc || true
pkill -f evcosmos || true

set -a && source .env && set +a
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
[runner][blob-height] blob_height=<N> source=...
```

Với Celestia direct submit, cần kiểm tra thêm namespace thực tế của submitter:

```bash
grep -E 'cosmos-da-submit-celestia.*rpc mode|cosmos-da-submit-celestia.*da_height=' .logs/cosmos-wasm-chain.log | tail -n 20
```

Kỳ vọng có dạng:

```text
[cosmos-da-submit-celestia] [run][da-submitter] rpc mode ... namespace=...726f6c6c7570 ...
[cosmos-da-submit-celestia] [ok][da-submitter] seq=... da_height=...
```

Nếu thấy namespace kết thúc kiểu `...45fb8b...` thì đó là namespace hash của chuỗi `rollup`, query bằng namespace mặc định sẽ ra `result:null`. Khi đó cần restart runner với code mới để Celestia submitter dùng đúng `DA_NAMESPACE`.

`blob_height=<N>` là giá trị để query blob trên Celestia.

Bạn có thể lọc nhanh height mới nhất:

```bash
grep -E 'blob_height=' .logs/cosmos-wasm-chain.log | tail -n 1
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

## 5.1 Query Celestia blob (tự điền height)

Script:

```bash
./scripts/query_celestia_blob.sh
```

Luồng resolve height mặc định:

1. Đọc `blob_height=<N>` mới nhất từ `CHAIN_LOG_FILE` (mặc định `.logs/cosmos-wasm-chain.log`)
2. Nếu chưa có thì fallback sang `latest-block` RPC (`data_da_height/header_da_height`)

Lưu ý quan trọng với `COSMOS_DA_UPLOAD_MODE=engram`:

- Log `engram_submit ... status=200` chỉ xác nhận API Engram nhận request.
- Log này **không** trả trực tiếp `da_height` trên Celestia, nên không thể dùng `chain_height` để query `blob.GetAll`.
- Nếu cần query trực tiếp Celestia theo height, cần có nguồn trả về `da_height` thật (hoặc chạy mode submit trực tiếp Celestia).

Lưu ý namespace:

- Height chỉ query được khi namespace query đúng với namespace đã submit ở log `cosmos-da-submit-celestia`.
- Ưu tiên dùng `blob_height` được emit từ `source=cosmos-da-submit-celestia` trong cùng run hiện tại.

Các mẫu:

```bash
# auto (không cần truyền height)
./scripts/query_celestia_blob.sh

# theo tx hash
./scripts/query_celestia_blob.sh --tx-hash <HEX_TX_HASH>

# theo ev-node block height
./scripts/query_celestia_blob.sh --block-height 100

# chỉ định tay DA/blob height
./scripts/query_celestia_blob.sh --height 620070

# query theo khoảng DA height
./scripts/query_celestia_blob_range.sh --from-height 620000 --to-height 620020
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

## 9) Tiện ích encode/decode base64

Script dùng:

```bash
./scripts/base64-tool.sh
```

Các lệnh nhanh:

```bash
# encode từ text
./scripts/base64-tool.sh encode --text 'hello'

# encode từ stdin
echo -n 'hello' | ./scripts/base64-tool.sh encode

# decode từ text base64
./scripts/base64-tool.sh decode --text 'aGVsbG8='

# decode từ file
./scripts/base64-tool.sh decode --file /tmp/payload.b64

# decode raw (không format JSON)
./scripts/base64-tool.sh decode --file /tmp/payload.b64 --raw
```