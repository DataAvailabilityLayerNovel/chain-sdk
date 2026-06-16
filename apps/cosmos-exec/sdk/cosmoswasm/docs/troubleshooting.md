# Troubleshooting

## Chẩn đoán nhanh

Trước khi debug, chạy các lệnh curl sau để kiểm tra executor:

```bash
# 1. Executor có chạy không?
curl -s http://127.0.0.1:50051/health
# Kỳ vọng: 200 OK

# 2. Đã initialized chưa?
curl -s http://127.0.0.1:50051/status
# Kỳ vọng: {"initialized":true, "chain_id":"...", "latest_height":N, ...}

# 3. Block có được sinh không?
curl -s http://127.0.0.1:50051/blocks/latest
# Kỳ vọng: {"height":N, "time":"...", "app_hash":"...", "num_txs":N}

# 4. Mempool có drain không?
curl -s http://127.0.0.1:50051/tx/pending
# Kỳ vọng: {"pending_count":0}  (hoặc số nhỏ)
```

> Lưu ý: **không có** endpoint `/blob/submit` trên `cosmos-exec-grpc`. Blob-first
> đi qua `BlobClient` (JSON-RPC thẳng tới Celestia bridge). Kiểm tra DA riêng ở
> mục [DA URL errors](#9-da-url-errors).

---

## Lỗi thường gặp

### 1. `connection refused` / `ErrNotReachable`

**Triệu chứng:** SDK trả `executor not reachable: connection refused`.

| Nguyên nhân | Cách sửa |
|-------------|----------|
| Executor chưa start | `cd apps/cosmos-exec && go run ./cmd/cosmos-exec-grpc --in-memory` |
| Sai port | Check `--address` hoặc `COSMOS_EXEC_LISTEN_ADDR`. Default `50051` |
| Process crash | Xem log panic. Restart executor |
| Firewall/Docker network | Đảm bảo port truy cập được (`--network host` hoặc expose port) |

```bash
lsof -i :50051            # tiến trình có đang chạy?
curl -s http://127.0.0.1:50051/health
```

### 2. `SubmitTxBytes` thành công nhưng `GetTxResult` trả `found=false`

| Nguyên nhân | Cách sửa |
|-------------|----------|
| Block không được sinh | Check `curl /blocks/latest`. Height không tăng → sequencer kẹt |
| Tx còn trong mempool | Chờ 1 block time (2s). Dùng `WaitTxResult` thay vì poll thủ công |
| Sai hash | Dùng hash từ `SubmitTxResponse`, không tự tính |
| Executor restart | In-memory mất state khi restart. Bật persistence |

```go
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
resp, _ := client.SubmitTxBytes(ctx, txBytes)
result, err := client.WaitTxResult(ctx, resp.Hash, time.Second)
if err != nil {
    log.Fatal(err) // DeadlineExceeded = block không tiến
}
```

### 3. Tx thực thi nhưng `Code != 0`

```go
result, _ := client.WaitTxResult(ctx, hash, time.Second)
if result.Code != 0 {
    fmt.Println("Code:", result.Code)
    fmt.Println("Log:", result.Log)  // <-- message lỗi WASM ở đây
}
```

| Log chứa | Nguyên nhân | Cách sửa |
|----------|-------------|----------|
| `"failed to execute message"` | Contract từ chối message | Check execute handler của contract |
| `"contract not found"` | Sai địa chỉ contract | Verify bech32 |
| `"unknown message"` | Contract không handle message type này | Check message contract hỗ trợ |
| `"unauthorized"` | Sender không có quyền | Check ownership/ACL của contract |
| `"insufficient funds"` | Không đủ số dư | Cấp tiền cho account sender |

### 4. `blob size ... exceeds max`

**Triệu chứng:** `ErrBlobTooLarge` khi `BlobClient.SubmitBlob`.

`MaxBlobSize` = 2 MB (safety cap của BlobClient). Options:

```go
// Option 1: nén trước
compressed, ok := cosmoswasm.CompressIfBeneficial(data)
if ok { bc.SubmitBlob(ctx, compressed) }

// Option 2: chia chunk
chunks, meta := cosmoswasm.ChunkBlob(data, 512*1024)
_ = meta
for _, chunk := range chunks { bc.SubmitBlob(ctx, chunk) }

// Option 3: nhiều blob 1 batch
batch, _ := bc.SubmitBatch(ctx, [][]byte{b1, b2, b3})
```

### 5. `store full` / `ErrBlobStoreFull`

**Triệu chứng:** `blob store capacity exceeded` (khi dùng blob store server-side).

```bash
export COSMOS_EXEC_MAX_STORE_SIZE=2147483648  # 2 GB
```

Hoặc restart executor (in-memory store xoá khi restart).

### 6. `context deadline exceeded` (timeout)

| Nguyên nhân | Cách sửa |
|-------------|----------|
| SDK timeout quá thấp | Tăng `SDKConfig.Timeout` (default 20s) |
| Executor quá tải | Check `curl /metrics.json` — `mempool_size` cao = nghẽn sinh block |
| Upload blob lớn mạng chậm | Tăng timeout; cân nhắc chunk |
| WASM query quá phức tạp | Tăng `query_gas_max` |

### 7. Auth error (HTTP 401)

```go
client, _ := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
    ExecURL:   "http://127.0.0.1:50051",
    AuthToken: "same-token-as-COSMOS_EXEC_AUTH_TOKEN",
})
```

```bash
curl -H "Authorization: Bearer same-token" http://127.0.0.1:50051/status
```

### 8. Rate limited (HTTP 429)

```bash
export COSMOS_EXEC_RATE_LIMIT_RPS=200
```

Client side: thêm delay giữa request, hoặc gom blob bằng `SubmitBatch` để giảm
số lần gọi.

### 9. DA URL errors

**Triệu chứng:** `NewBlobClient` / `SubmitBlob` lỗi kết nối.

```bash
# 1. DA node có tới được không?
curl -s http://localhost:26658/header/1

# 2. Auth token hợp lệ?
curl -s -H "Authorization: Bearer $DA_AUTH_TOKEN" http://localhost:26658/header/1

# 3. Lỗi URL thường gặp:
#    Sai:  https://localhost:26658  (DA node thường HTTP, không HTTPS)
#    Sai:  http://localhost:26657   (26657 là CometBFT RPC, không phải DA)
#    Đúng: http://localhost:26658   (DA bridge RPC)
```

### 10. Mất state sau restart

**Triệu chứng:** restart xong `initialized=false`, không block, không blob.
**Nguyên nhân:** chạy `--in-memory` hoặc chưa bật persistence.

```bash
go run ./cmd/cosmos-exec-grpc --profile prod --home /data/cosmos-exec
# Tìm log: "persistence enabled" dir="/data/cosmos-exec/data"
```

### 11. `fee payer address: <garbled bytes> does not exist: unknown address` (code 9)

**Triệu chứng:** Tx ký đầu tiên từ account Keplr/browser mới fail. Địa chỉ hiện
ra dạng byte non-ASCII (`��Yp�…`).

**Nguyên nhân:** Ante chain chạy `DeductFeeDecorator` **trước**
`AutoCreateAccountDecorator`, nên DeductFee tra fee payer trong state trước khi
AutoCreate kịp tạo account. `FeeTx.FeePayer()` trả `[]byte` (raw address) trong
cosmos-sdk v0.50 → format `%s` ra byte rác.

**Cách sửa:** Trong `apps/cosmos-exec/app/ante.go`, đảm bảo
`NewAutoCreateAccountDecorator` đứng trước `NewDeductFeeDecorator`. Xem
[auto-account-creation.md](auto-account-creation.md).

### 12. `signature verification failed; please verify account number ...` (code 4)

**Nguyên nhân:** Client ký `SignDoc` với `account_number = 0` (zero-value của
account chưa tồn tại), nhưng `AutoCreateAccountDecorator` gán số khác từ
`GlobalAccountNumber`. `SigVerificationDecorator` dựng lại `SignDoc` với số của
chain → không khớp → fail.

**Cách sửa:** `executor.GetAccountInfo` peek `AccountKeeper.AccountNumber.Peek(ctx)`
cho account chưa tồn tại và trả số đó — đúng số `NewAccountWithAddress` sẽ gán.
Client ký với số đó → verify thành công. Xem
[auto-account-creation.md](auto-account-creation.md).

> **Đừng** ghim account auto-created về `account_number = 0` để né lỗi — SDK
> enforce uniqueness index `account_number → address`, và module account đã
> chiếm các số thấp → sẽ panic `index uniqueness constrain violation: 0`.

### 13. Port already in use

```bash
lsof -i :50051
kill $(lsof -t -i :50051)
# hoặc dùng port khác:
go run ./cmd/cosmos-exec-grpc --address 0.0.0.0:50052
```

---

## Debug checklist

1. **Executor chạy?** → `curl /health`
2. **Initialized?** → `curl /status` (check `initialized`)
3. **Block tiến?** → `curl /blocks/latest` (check `height` tăng)
4. **Auth OK?** → `curl -H "Authorization: Bearer $TOKEN" /status`
5. **Mempool drain?** → `curl /tx/pending` (nên 0 hoặc nhỏ)
6. **DA OK?** (nếu dùng blob-first) → `curl http://localhost:26658/header/1`
7. **Log executor** → tìm `ERROR` / `panic` trong stdout
8. **Chi tiết lỗi SDK** → đọc `SDKError.Hint`
