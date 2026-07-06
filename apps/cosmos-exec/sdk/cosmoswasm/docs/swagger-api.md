# Swagger API — cosmos-exec-grpc

Tài liệu này mô tả các endpoint HTTP **được khai báo trong Swagger/OpenAPI** của máy
chủ `cosmos-exec-grpc`, kèm ví dụ gọi bằng `curl` và method SDK Go tương đương. Spec
được sinh trong [`cmd/cosmos-exec-grpc/swagger.go`](../../../cmd/cosmos-exec-grpc/swagger.go).

> Tài liệu tham chiếu thư viện Go xem [api-reference.md](api-reference.md); kinh tế
> phí xem [fee-economics.md](fee-economics.md); danh sách đầy đủ 25 endpoint HTTP xem
> [thong-ke-ma-nguon.md](thesis/thong-ke-ma-nguon.md).

## Truy cập Swagger

Khi server đang chạy (mặc định nghe `0.0.0.0:50051`):

| Link | Nội dung |
|---|---|
| `http://localhost:50051/swagger` | Swagger UI (bấm thử API trực tiếp) |
| `http://localhost:50051/swagger.json` | File OpenAPI 3.0 thô (JSON) |

Cổng đổi được qua cờ `--address` hoặc profile (`test` dùng cổng ngẫu nhiên). Lúc khởi
động server log ra địa chỉ thật. Swagger UI tải `swagger-ui-dist` từ CDN nên cần mạng
để render; bản thân `/swagger.json` thì không.

## Quy ước chung

- Base URL trong ví dụ: `http://localhost:50051` (đặt biến `B=http://localhost:50051`).
- Body và response đều **JSON**.
- Nếu bật auth token (`COSMOS_EXEC_AUTH_TOKEN`), thêm header:
  `-H "Authorization: Bearer $TOKEN"`.
- Tx gửi đi là **`TxRaw` Cosmos đã ký**, mã hoá `base64` (hoặc `hex`). Dựng tx bằng
  SDK ([Transaction Builders](api-reference.md#transaction-builders)) hoặc CosmJS.

Spec hiện tài liệu hoá **10 endpoint** (nhóm `node`, `tx`, `wasm`). Các endpoint vận
hành (`/health`, `/metrics`, `/exec/*`, `/faucet`, `/auth/account`, `/bank/*`) cố ý
không đưa vào Swagger — xem [thong-ke-ma-nguon.md](thesis/thong-ke-ma-nguon.md).

---

## node — Trạng thái & block

### GET `/status`

Trạng thái node: đã khởi tạo chưa, healthy/synced, và chiều cao block.

**Response** (`StatusResponse`):

```json
{
  "initialized": true,
  "chain_id": "cosmos-wasm-local",
  "latest_height": 142,
  "finalized_height": 138,
  "healthy": true,
  "synced": false
}
```

```bash
curl -s $B/status | jq
```

SDK: [`Client.Status`](api-reference.md#m-status). Một tx ở height `h` là **soft** khi
`h ≤ latest_height`, **DA-final** khi `h ≤ finalized_height`.

### GET `/blocks/latest`

Block mới nhất. Chain chưa có block → trả `{"found": false}`.

**Response** (`BlockInfo`):

```json
{
  "height": 142,
  "time": "2026-06-26T10:21:33Z",
  "app_hash": "9f2c…",
  "num_txs": 2,
  "tx_hashes": ["a1b2…", "c3d4…"]
}
```

```bash
curl -s $B/blocks/latest | jq
```

SDK: [`Client.GetLatestBlock`](api-reference.md#m-getlatestblock).

### GET `/blocks/{height}`

Block tại một chiều cao. Không tồn tại → `404`.

```bash
curl -s $B/blocks/142 | jq
```

SDK: [`Client.GetBlockByHeight`](api-reference.md#m-getblockbyheight).

---

## tx — Giao dịch

### POST `/tx/submit`

Gửi tx đã ký vào mempool; tx vào block kế tiếp.

**Request** (`SubmitTxRequest`): một trong `tx_base64` / `tx_hex`.

```json
{ "tx_base64": "Cr0BCroB...." }
```

**Response** (`SubmitTxResponse`): `{ "hash": "<sha256 hex>" }`

```bash
curl -s -X POST $B/tx/submit \
  -H 'Content-Type: application/json' \
  -d '{"tx_base64":"'"$TX_B64"'"}' | jq
```

SDK: [`Client.SubmitTxBytes` / `SubmitTxBase64`](api-reference.md#m-submittxbytes).

### GET `/tx/result?hash=...`

Tra kết quả thực thi một lần. Chưa thấy → `{"found": false}`.

**Response** khi thấy:

```json
{
  "found": true,
  "result": {
    "hash": "a1b2…",
    "height": 142,
    "code": 0,
    "log": "",
    "gas_used": 125849,
    "gas_wanted": 163604,
    "bytes": 412,
    "events": [ { "type": "wasm", "attributes": [ /* … */ ] } ]
  },
  "cost": { /* TxCostBreakdown — xem /tx/estimate */ }
}
```

```bash
curl -s "$B/tx/result?hash=$HASH" | jq
```

SDK: [`Client.GetTxResult`](api-reference.md#m-gettxresult) (1 lần) hoặc
[`Client.WaitTxResult`](api-reference.md#m-waittxresult) (poll tới khi vào block).
`code = 0` là thành công; khác 0 → xem `log`.

### GET `/tx/{hash}`

Chi tiết một tx kèm `status` (pending/success/failed). Chưa thực thi thì
`status="pending"`, `found=false`.

```bash
curl -s $B/tx/$HASH | jq
```

### GET `/tx/pending`

Số tx đang chờ trong mempool.

**Response** (`TxPendingResponse`): `{ "pending_count": 3 }`

```bash
curl -s $B/tx/pending | jq
```

SDK: [`Client.GetPendingTxCount`](api-reference.md#m-getpendingtxcount).

### POST `/tx/simulate`

Chạy thử tx (ante + handler, **không commit**) để lấy **gas thật** + `gas_limit` đã
đệm + `fee` gợi ý. Gọi **trước khi ký** để phí khớp tx thực.

**Request** (`SubmitTxRequest`): `tx_base64` / `tx_hex` (nên là tx đã ký).

**Response** (`SimulateResponse`):

```json
{
  "gas_used": 125849,
  "gas_wanted": 0,
  "gas_limit": 163604,
  "fee": [ { "denom": "ustake", "amount": "1" } ],
  "fee_denom": "ustake",
  "fee_amount": "1"
}
```

```bash
curl -s -X POST $B/tx/simulate \
  -H 'Content-Type: application/json' \
  -d '{"tx_base64":"'"$TX_B64"'"}' | jq
```

SDK: [`Client.SimulateTx`](api-reference.md#m-simulatetx). `gas_limit =
ceil(gas_used × COSMOS_EXEC_GAS_ADJUSTMENT)`; `fee` theo đúng chính sách ante enforce.

### POST `/tx/estimate`

Ước lượng **tổng chi phí (DA + gas)** mà **không** chạy tx (trừ dạng `{hash}`). Dùng
cho dashboard/định giá; muốn gas chính xác để ký thì dùng `/tx/simulate`.

**Request** (`EstimateTxRequest`) — đúng MỘT trong: `{tx_base64|tx_hex, gas}`,
`{hash}`, hoặc `{bytes, gas}`.

**Response** (`TxCostBreakdown`):

```json
{
  "bytes": 1024,
  "gas": 200000,
  "est_da_amount": "0.000219",
  "est_da_denom": "TIA",
  "est_gas_amount": "0.2",
  "est_gas_denom": "ustake",
  "da_price_per_byte": "0.000000214",
  "min_gas_price": "0.000001"
}
```

```bash
# theo bytes + gas
curl -s -X POST $B/tx/estimate -H 'Content-Type: application/json' \
  -d '{"bytes":1024,"gas":200000}' | jq

# theo hash của tx đã chạy (lấy bytes + gas thật)
curl -s -X POST $B/tx/estimate -H 'Content-Type: application/json' \
  -d '{"hash":"'"$HASH"'"}' | jq
```

SDK: [`Client.EstimateCost`](api-reference.md#m-estimatecost). Phần DA tính bằng
`bytes × COSMOS_EXEC_TIA_PER_BYTE` — xem [fee-economics.md §1b](fee-economics.md#muc-1b).

---

## wasm — Hợp đồng thông minh

### POST `/wasm/query-smart`

Smart query **chỉ đọc** state hợp đồng (không tốn gas thật, không qua mempool).

**Request** (`QuerySmartRequest`): `{ "contract": "<bech32>", "msg": { … } }`

**Response**: `{ "data": { … } }` nếu kết quả là JSON; nếu không parse được thì
`{ "data_raw": "<chuỗi>" }`.

```bash
curl -s -X POST $B/wasm/query-smart \
  -H 'Content-Type: application/json' \
  -d '{"contract":"cosmos1abc...","msg":{"get_count":{}}}' | jq
```

SDK: [`Client.QuerySmart`](api-reference.md#m-querysmart) (trả `map`) hoặc
[`Client.QuerySmartRaw`](api-reference.md#m-querysmartraw).

---

## Luồng end-to-end (ví dụ gộp)

Triển khai + tương tác một hợp đồng, dùng đúng các endpoint trên:

```bash
B=http://localhost:50051

# 1) Mô phỏng để lấy gas/fee trước khi ký (TX_B64 = store-code tx đã ký)
curl -s -X POST $B/tx/simulate -H 'Content-Type: application/json' \
  -d '{"tx_base64":"'"$TX_B64"'"}' | jq '{gas_limit, fee_amount, fee_denom}'

# 2) Gửi tx → nhận hash
HASH=$(curl -s -X POST $B/tx/submit -H 'Content-Type: application/json' \
  -d '{"tx_base64":"'"$TX_B64"'"}' | jq -r .hash)

# 3) Chờ kết quả (poll vài lần tới khi found=true, code=0)
curl -s "$B/tx/result?hash=$HASH" | jq '{found, code:.result.code, height:.result.height}'

# 4) Truy vấn state hợp đồng
curl -s -X POST $B/wasm/query-smart -H 'Content-Type: application/json' \
  -d '{"contract":"'"$ADDR"'","msg":{"get_count":{}}}' | jq .data

# 5) Quan sát chain
curl -s $B/status        | jq '{latest_height, finalized_height}'
curl -s $B/blocks/latest | jq '{height, num_txs}'
curl -s $B/tx/pending    | jq
```

> Muốn làm trong Go thay vì `curl`, mỗi bước trên có method SDK tương ứng — xem
> [api-reference.md](api-reference.md) và ví dụ chạy được `examples/my-counter`.
