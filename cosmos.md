# Cosmos + WASM (Current Runbook)

Tài liệu này chỉ giữ **trạng thái hiện tại** và các lệnh có thể chạy ngay.

## 1) Những gì đang chạy được

- Chạy `evcosmos` full stack qua runner: sequencer + fullnode + `cosmos-exec-grpc`.
- DA submit đi trực tiếp qua evnode runtime (aggregator), không dùng sidecar `cosmos-da-submit`.
- Health endpoint của 2 node.
- RPC đọc block/tx/state qua script `scripts/contracts/wasm-rpc.sh` (backend là `tools/evnode-rpc`).

## 2) Chuẩn bị `.env`

Yêu cầu các biến sau trong `.env`:

- `DA_BRIDGE_RPC` hoặc `DA_RPC`
- `DA_AUTH_TOKEN`
- `DA_NAMESPACE`

Khuyến nghị:

- Không set `DA_NAMESPACE_B64` theo kiểu cũ (right-pad text như `rollup`) vì có thể lệch namespace runtime.
- Các script blob/watch hiện tự derive namespace từ `DA_NAMESPACE` theo đúng logic runtime evnode (v0 namespace từ `sha256(DA_NAMESPACE)`), nên chỉ cần giữ `DA_NAMESPACE`.
- Nếu vẫn muốn set tay `DA_NAMESPACE_B64`, phải dùng đúng giá trị runtime-derived; sai giá trị sẽ làm watch/query trả rỗng dù chain vẫn đang submit.

Runner hiện để chính evnode submit trực tiếp lên Celestia qua `DA_BRIDGE_RPC` (fallback `DA_RPC`).

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

Trong log runner, kỳ vọng có log submit từ evnode:

```text
[evcosmos-sequencer] ... component=da_submitter ...
[runner][blob-height] blob_height=<N> source=evcosmos-sequencer
```

Để kiểm tra nhanh DA height mới nhất từ evnode:

```bash
grep -E 'evcosmos-sequencer.*da_height=|\[runner\]\[blob-height\]' .logs/cosmos-wasm-chain.log | tail -n 20
```

Kỳ vọng có dạng:

```text
[evcosmos-sequencer] ... da_height=...
[runner][blob-height] blob_height=<N> source=evcosmos-sequencer
```

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

Lưu ý namespace:

- Height chỉ query được khi namespace query đúng với namespace mà evnode đang dùng (`DA_NAMESPACE`).
- Ưu tiên dùng `blob_height` được emit trong log hiện tại của runner/evnode.
- `watch_celestia_latest_blobs.sh` chỉ in khi height đó có blob thuộc namespace đang query; nếu namespace sai thì sẽ im lặng.

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

# watch blob mới nhất liên tục
./scripts/watch_celestia_latest_blobs.sh

# watch 1 vòng (dùng để smoke test)
./scripts/watch_celestia_latest_blobs.sh --once --backfill 5

# nếu cần hiện lỗi RPC để debug
./scripts/watch_celestia_latest_blobs.sh --show-errors
```

Config endpoint RPC:

- `CELESTIA_BRIDGE_RPC` (ưu tiên)
- hoặc `DA_BRIDGE_RPC`
- hoặc `DA_RPC`
- mặc định: `http://131.153.224.169:26758`

Debug nhanh khi watch không ra blob:

```bash
# 1) kiểm tra chain có đang submit DA không
grep -E 'evcosmos-sequencer.*da_height=' .logs/cosmos-wasm-chain.log | tail -n 10

# 2) chạy watch hiện lỗi RPC
./scripts/watch_celestia_latest_blobs.sh --show-errors

# 3) smoke test 1 height vừa submit
H=$(grep -E 'evcosmos-sequencer.*da_height=' .logs/cosmos-wasm-chain.log | grep -Eo 'da_height=[0-9]+' | tail -n 1 | cut -d= -f2)
./scripts/watch_celestia_latest_blobs.sh --once --start-height "$H" --show-errors
```

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

Lưu ý khi chạy trên `zsh`:

- Nếu paste cả block có dòng comment bắt đầu bằng `#`, `zsh` có thể báo `command not found: #` (và lỗi phụ như `unknown file attribute: b`).
- Cách 1: bật comment mode trước khi paste: `setopt interactivecomments`
- Cách 2: dùng block không comment bên dưới.

```bash
./scripts/contracts/wasm-contract.sh deploy

source /tmp/ev-node-wasm/last-deploy.env

SENDER=$(cd ./apps/cosmos-exec && go run ./cmd/cosmos-wasm-tx default-sender)

TX_BASE64=$(cd ./apps/cosmos-exec && go run ./cmd/cosmos-wasm-tx execute \
	--sender "$SENDER" \
	--contract "$CONTRACT_ADDR" \
	--msg '{"transfer":{"recipient":"'"$CONTRACT_ADDR"'","amount":"1"}}' \
	--out base64)

SUBMIT_JSON=$(./scripts/contracts/wasm-contract.sh submit --tx-base64 "$TX_BASE64")
echo "$SUBMIT_JSON"

TX_HASH=$(echo "$SUBMIT_JSON" | jq -r '.hash')
./scripts/contracts/wasm-rpc.sh tx --hash "$TX_HASH"
```

Nếu vừa submit mà `tx --hash` trả `found:false`, thường là tx chưa được index hoặc node chưa chạy đúng RPC. Retry nhanh:

```bash
for i in {1..10}; do ./scripts/contracts/wasm-rpc.sh tx --hash "$TX_HASH" && break; sleep 2; done
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