# Production Guide

## Triển khai Executor

### Start với profile prod

```bash
export COSMOS_EXEC_AUTH_TOKEN="your-secret-token"
export COSMOS_EXEC_CORS_ORIGIN="https://app.mychain.io"
export COSMOS_EXEC_RATE_LIMIT_RPS=100
export COSMOS_EXEC_METRICS=true

go run ./cmd/cosmos-exec-grpc \
    --profile prod \
    --home /data/cosmos-exec \
    --address 0.0.0.0:50051
```

### Checklist production

| Item | Mức |
|------|-----|
| `--profile prod` hoặc env tương đương | Bắt buộc |
| `COSMOS_EXEC_AUTH_TOKEN` đã set | Bắt buộc |
| `COSMOS_EXEC_CORS_ORIGIN` = domain cụ thể (không `*`) | Bắt buộc |
| Persistence bật (`persist_blobs=true`, `persist_tx_results=true`) | Bắt buộc |
| `--home` trên storage bền (không tmpfs) | Bắt buộc |
| Rate limiting bật (`rate_limit_rps > 0`) | Khuyến nghị |
| Metrics bật | Khuyến nghị |
| TLS termination (reverse proxy / load balancer) | Khuyến nghị |
| Log level `info` (không `debug`) | Khuyến nghị |

### Persistence

Profile prod tự bật persistence. Data lưu ở `$HOME/data/`:

```
$HOME/data/
├── metadata.json       # chain state (ghi atomic)
├── tx_results.jsonl    # tx result (append-only)
└── blocks.jsonl        # block info (append-only)
```

(Không có `blobs.jsonl` — blob lưu trên Celestia qua `BlobClient`, không qua executor.)

- `metadata.json` ghi atomic (temp + rename) — an toàn khi crash.
- File JSONL append-only — không hỏng do ghi dở (granularity theo dòng).
- Khi start, mọi file replay vào RAM. Dòng hỏng bị skip kèm đếm warning.
- Backup: snapshot toàn bộ thư mục `$HOME/data/`.

## Cấu hình SDK Client

```go
client, err := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
    ExecURL:       "https://exec.mychain.io",
    Timeout:       30 * time.Second,
    RetryAttempts: 3,
    RetryDelay:    2 * time.Second,
    AuthToken:     os.Getenv("EXEC_AUTH_TOKEN"),
    ChainID:       "my-chain-1",
})
if err != nil { log.Fatal(err) }
```

### Tune timeout

| Operation | Latency thường | Timeout khuyến nghị |
|-----------|----------------|---------------------|
| `BlobClient.SubmitBlob` (< 1 MB) | < 50ms | 5s |
| `BlobClient.SubmitBlob` (1-2 MB) | 50-300ms | 10s |
| `BlobClient.SubmitBatch` (20 blob) | 100-500ms | 15s |
| `SubmitTxBytes` | < 10ms | 5s |
| `WaitTxResult` | 2-10s (block time) | 30s (qua context) |
| `QuerySmart` | 5-50ms | 10s |
| `SubmitBatch` + `BuildBatchRootTx` + submit | 100-800ms | 20s |

`WaitTxResult` điều khiển thời lượng qua `context.WithTimeout`:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
result, err := client.WaitTxResult(ctx, hash, time.Second)
```

### Retry

| Môi trường | RetryAttempts | RetryDelay |
|------------|---------------|------------|
| Dev | 0 | — |
| Staging | 2 | 1s |
| Production | 3 | 2s |

SDK chỉ retry lỗi tạm thời (connection refused, timeout). **Không** retry
validation error, capacity error, hay WASM execution failure. Cần backoff
tuỳ biến (exponential, jitter) → set `RetryAttempts=0` và tự code (xem
[error-handling.md](error-handling.md#chiến-lược-retry)).

## Authentication

Set `COSMOS_EXEC_AUTH_TOKEN` trên executor. Mọi request phải có header
`Authorization: Bearer <token>` — thiếu/sai → HTTP 401. Client truyền
`SDKConfig.AuthToken`. Xoay token: tạo client mới với token mới (SDK không cache).

## CORS

```bash
export COSMOS_EXEC_CORS_ORIGIN=https://app.mychain.io   # 1 origin
export COSMOS_EXEC_CORS_ORIGIN=*                         # dev — KHÔNG cho prod
```

## Rate Limiting

`COSMOS_EXEC_RATE_LIMIT_RPS`: Dev `0` (tắt), Staging `50`, Production `100`
(điều chỉnh theo phần cứng). Client gặp 429 → back off, retry sau `RetryDelay`.

## Idempotency

### Blob

`BlobClient.SubmitBlob`: commitment (NMT) **deterministic theo data + namespace**,
nhưng **mỗi lần submit là một blob mới trên Celestia** (DA height mới). Vì vậy
retry sau lỗi network có thể tạo blob trùng nội dung ở height khác — không tốn
gì on-chain (chỉ ghi commitment một lần), nhưng đừng coi là "idempotent tuyệt
đối". Ghi kèm `Height` khi commit on-chain (`BuildBlobCommitTx`).

### Transaction

`SubmitTxBytes` **KHÔNG idempotent** — submit cùng tx bytes 2 lần = vào mempool
2 lần. Nếu lỗi network sau submit:

```go
resp, err := client.SubmitTxBytes(ctx, txBytes)
if err != nil {
    // lỗi network — check tx đã được nhận chưa
    hash := fmt.Sprintf("%x", sha256.Sum256(txBytes)) // hash lowercase, khớp executor
    existing, _ := client.GetTxResult(ctx, hash)
    if existing != nil && existing.Found {
        return existing.Result, nil // đã nhận, không resubmit
    }
    resp, err = client.SubmitTxBytes(ctx, txBytes) // chưa thấy → retry an toàn
}
```

### Batch

`BlobClient.SubmitBatch` → cùng input blob cho cùng `Root` + commitments. Phần
ghi root on-chain (`BuildBatchRootTx` + `SubmitTxBytes`) thì không idempotent —
dùng pattern như tx ở trên.

## Monitoring

### Health endpoints

```bash
curl http://executor:50051/health     # 200 OK / lỗi
curl http://executor:50051/healthz    # alias
curl http://executor:50051/ready      # readiness (đã initialized?)
```

### Metrics

Bật bằng `COSMOS_EXEC_METRICS=true`:

```bash
curl http://executor:50051/metrics        # Prometheus text
curl http://executor:50051/metrics.json   # JSON
```

| Metric | Mô tả | Ngưỡng alert |
|--------|-------|--------------|
| `requests_total` | Tổng HTTP request theo endpoint | — |
| `requests_errors` | Số lỗi theo endpoint | > 1% tổng |
| `tx_result_count` | Số tx đã thực thi | — |
| `block_count` | Số block đã thực thi | Phải tăng đều |
| `mempool_size` | Tx pending trong mempool | Duy trì > 100 = nghẽn |

### Monitoring tầng app

```go
// sau mỗi WaitTxResult
metrics.Histogram("sdk.tx_wait.duration_ms", float64(elapsed.Milliseconds()))
if result.Code != 0 {
    metrics.IncrCounter("sdk.tx.failed", 1)
}
```

## SLO khuyến nghị

| Metric | Staging | Production |
|--------|---------|------------|
| API availability | 99% | 99.9% |
| `WaitTxResult` p95 (gồm block time) | < 10s | < 5s |
| Tx success rate (Code=0) | > 90% | > 95% |
| Block progress (blocks/phút) | > 20 | > 25 |

### Alerting

| Điều kiện | Mức | Hành động |
|-----------|-----|-----------|
| `/health` non-200 > 30s | Critical | Check tiến trình executor |
| `mempool_size > 100` > 60s | Warning | Check block production |
| Không có block mới > 30s | Critical | Check sequencer + DA |
| Error rate > 5% trên 1 endpoint | Warning | Check log |

## So sánh môi trường

| Setting | Dev | Staging | Production |
|---------|-----|---------|------------|
| DB | `--in-memory` OK | Disk | Disk (bền) |
| Persistence | Optional | Bật | **Bắt buộc** |
| Auth token | Không | Set | **Bắt buộc** |
| CORS | `*` | Domain cụ thể | **Domain cụ thể** |
| Rate limit | 0 | 50 rps | 100 rps |
| Metrics | Off | On | **On** |
| TLS | Không | Khuyến nghị | **Bắt buộc** (reverse proxy) |
| SDK timeout | 10s | 20s | 30s |
| SDK retry | 0 | 2 | 3 |
| Log level | debug | info | info |
