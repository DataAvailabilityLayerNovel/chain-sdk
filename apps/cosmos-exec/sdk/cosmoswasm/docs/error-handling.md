# Error Handling

## Cấu trúc lỗi

Public method của SDK trả `*SDKError` (hoặc `nil`). Nó bọc lỗi gốc kèm ngữ cảnh:

```go
type SDKError struct {
    Op    string // operation lỗi: "SubmitBlob", "SubmitBatch", ...
    Cause error  // lỗi gốc (match được bằng errors.Is)
    Hint  string // gợi ý 1 dòng để sửa
}
```

```go
_, err := bc.SubmitBlob(ctx, data)
if err != nil {
    var sdkErr *cosmoswasm.SDKError
    if errors.As(err, &sdkErr) {
        fmt.Println("operation:", sdkErr.Op)
        fmt.Println("cause:", sdkErr.Cause)
        fmt.Println("hint:", sdkErr.Hint)
    }
}
```

## Sentinel errors

Match bằng `errors.Is()`:

| Sentinel | Ý nghĩa | Retryable | Hành động |
|----------|---------|-----------|-----------|
| `ErrNotReachable` | Executor down / connection refused | Có | Retry với backoff; alert nếu kéo dài |
| `ErrBlobTooLarge` | Blob vượt `MaxBlobSize` (2 MB — cap của BlobClient) | Không | Nén bằng `CompressIfBeneficial` hoặc chia bằng `ChunkBlob` |
| `ErrBlobStoreFull` | Vượt dung lượng tổng | Không* | Giảm tần suất; restart executor; tăng `max_store_total_size` |
| `ErrTxFailed` | Tx đã thực thi nhưng fail (`Code != 0`) | Không | Xem `TxExecutionResult.Log` |
| `ErrContractMissing` | Thiếu địa chỉ contract | Không | Set field `Contract` |
| `ErrCommitMissing` | Thiếu commitment | Không | Truyền commitment hex từ `SubmitBlob` |

\* `ErrBlobStoreFull` chỉ retry được sau khi operator can thiệp (restart/tăng limit).

```go
if errors.Is(err, cosmoswasm.ErrNotReachable) {
    // retry với backoff
} else if errors.Is(err, cosmoswasm.ErrBlobTooLarge) {
    // chia nhỏ data
    chunks, meta := cosmoswasm.ChunkBlob(data, 512*1024)
    _ = meta
    for _, chunk := range chunks {
        bc.SubmitBlob(ctx, chunk)
    }
} else if errors.Is(err, cosmoswasm.ErrBlobStoreFull) {
    // alert operator, back off
}
```

## Phân loại lỗi

### 1. Validation error (không retry)

Do input sai. Sửa code caller.

| Message | Nguyên nhân | Cách sửa |
|---------|-------------|----------|
| `"tx bytes cannot be empty"` | Tx rỗng | Kiểm tra bước build tx |
| `"blob data is empty"` | Blob rỗng | Kiểm tra nguồn data |
| `"contract address required"` | Thiếu địa chỉ contract | Set `Contract` |
| `"code id is required"` | `CodeID=0` khi instantiate | Lấy `CodeID` từ kết quả store tx |
| `"msg must be valid json"` | JSON message hỏng | Validate JSON trước khi truyền |
| `"commitment required"` | Commitment rỗng | Dùng commitment từ `SubmitBlob` |

### 2. Network error (retryable)

Lỗi tạm thời — executor tạm thời không phục vụ.

| Lỗi chứa | Ý nghĩa | Retry? | Gợi ý |
|----------|---------|--------|-------|
| `"connection refused"` | Executor chưa chạy | Có | Start executor; retry exponential backoff |
| `"deadline exceeded"` | Request timeout | Có | Tăng `SDKConfig.Timeout`; check tải executor |
| `"context canceled"` | Caller huỷ | Không | Huỷ chủ động |

SDK tự retry các lỗi này khi `SDKConfig.RetryAttempts > 0`:

```go
client, _ := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
    ExecURL:       "http://127.0.0.1:50051",
    RetryAttempts: 3,    // retry tối đa 3 lần
    RetryDelay:    2 * time.Second,
})
```

### 3. Capacity error (retry có điều kiện)

Executor đang chạy nhưng không nhận request.

| Lỗi | Retry sau khi... |
|-----|------------------|
| `ErrBlobTooLarge` | Nén hoặc chunk data (đừng retry cùng payload) |
| `ErrBlobStoreFull` | Operator tăng limit / restart (back off, alert) |
| HTTP 429 (rate limited) | Chờ `RetryDelay` (tôn trọng rate limit) |

### 4. Execution error (không retry cùng tx)

Tx đã thực thi nhưng logic WASM từ chối.

```go
result, err := client.WaitTxResult(ctx, hash, time.Second)
if err != nil {
    log.Fatal(err) // network/timeout — có thể retry
}
if result.Code != 0 {
    // tx đã vào block nhưng fail — KHÔNG resubmit cùng tx
    fmt.Println("WASM error:", result.Log, "code:", result.Code)
}
```

Code lỗi thường gặp:

| Code | Ý nghĩa |
|------|---------|
| `0` | Success |
| `2` | Tx parse error (proto hỏng) |
| `5` | Insufficient funds |
| `11` | Out of gas |
| `18` | Contract execution failed (xem `Log`) |

### 5. API error (HTTP 4xx/5xx)

Trả về dạng `SDKError` kèm HTTP status + body:

```
SubmitBlob: api error (413): blob size 5242880 exceeds max 2097152
  hint: compress the data first or split with ChunkBlob()
```

## Chiến lược retry

### Backoff khuyến nghị

```go
func submitWithRetry(ctx context.Context, bc *cosmoswasm.BlobClient, data []byte) (*cosmoswasm.BlobSubmitResponse, error) {
    var lastErr error
    for attempt := 0; attempt < 5; attempt++ {
        res, err := bc.SubmitBlob(ctx, data)
        if err == nil {
            return res, nil
        }
        lastErr = err

        // chỉ retry lỗi tạm thời
        if !errors.Is(err, cosmoswasm.ErrNotReachable) {
            return nil, err // validation/capacity — đừng retry
        }

        // exponential backoff: 1s, 2s, 4s, 8s, 16s
        backoff := time.Duration(1<<attempt) * time.Second
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case <-time.After(backoff):
        }
    }
    return nil, fmt.Errorf("all retries exhausted: %w", lastErr)
}
```

Hoặc dùng retry built-in (`SDKConfig.RetryAttempts` + `RetryDelay`, delay cố định).

### Retry gì / không retry gì

| Tình huống | Retry? | Vì sao |
|------------|--------|--------|
| Connection refused | Có | Executor có thể đang restart |
| Deadline exceeded | Có | Network tạm thời |
| Blob too large | Không | Cùng payload luôn fail |
| Store full | Chờ + retry | Cần operator can thiệp |
| WASM execution failed | Không | Cùng tx sẽ fail lại |
| Invalid JSON | Không | Sửa message |
| Context cancelled | Không | Caller chủ động dừng |

## Map lỗi → hành động app

| App của bạn là... | Lỗi | Hành động |
|-------------------|-----|-----------|
| Game server submit event | `ErrNotReachable` | Buffer local, retry sau 5s |
| Game server submit event | `ErrBlobStoreFull` | Ghi file local, alert ops |
| Indexer poll tx result | `Found=false` | Chờ 1 block time (2s), poll lại |
| Indexer poll tx result | `ErrNotReachable` | Backoff 10s, kết nối lại |
| Contract deployer | `Code != 0` | Log lỗi, không retry cùng tx |
