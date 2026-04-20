# Production Guide

## Executor Deployment

### Start with Production Profile

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

### Production Checklist

| Item | Status |
|------|--------|
| `--profile prod` or equivalent env config | Required |
| `COSMOS_EXEC_AUTH_TOKEN` set | Required |
| `COSMOS_EXEC_CORS_ORIGIN` set to specific domain (not `*`) | Required |
| Persistence enabled (`persist_blobs=true`, `persist_tx_results=true`) | Required |
| `--home` on durable storage (not tmpfs) | Required |
| Rate limiting enabled (`rate_limit_rps > 0`) | Recommended |
| Metrics enabled | Recommended |
| TLS termination (reverse proxy or load balancer) | Recommended |
| Log level `info` (not `debug`) | Recommended |

### Persistence

Production profile enables persistence automatically. Data is stored in `$HOME/data/`:

```
$HOME/data/
├── metadata.json       # Chain state (atomic overwrite)
├── tx_results.jsonl    # Append-only tx results
├── blocks.jsonl        # Append-only block info
└── blobs.jsonl         # Append-only blob data
```

- `metadata.json` uses atomic write (temp + rename) — safe against crashes.
- JSONL files are append-only — no corruption risk from partial writes (line-level granularity).
- On startup, all files are replayed into memory. Corrupt lines are skipped with a warning count.
- Backup strategy: snapshot the entire `$HOME/data/` directory.

## SDK Client Configuration

### Recommended Production Config

```go
client, err := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
    ExecURL:       "https://exec.mychain.io",
    Timeout:       30 * time.Second,
    RetryAttempts: 3,
    RetryDelay:    2 * time.Second,
    AuthToken:     os.Getenv("EXEC_AUTH_TOKEN"),
    ChainID:       "my-chain-1",
})
if err != nil {
    log.Fatal(err)
}
```

### Timeout Tuning

| Operation | Typical Latency | Recommended Timeout |
|-----------|----------------|---------------------|
| `SubmitBlob` (< 1 MB) | < 10ms | 5s |
| `SubmitBlob` (1-4 MB) | 10-100ms | 10s |
| `SubmitBatch` (20 blobs) | 20-200ms | 15s |
| `SubmitTxBytes` | < 10ms | 5s |
| `WaitTxResult` | 2-10s (block time) | 30s (via context) |
| `QuerySmart` | 5-50ms | 10s |
| `CommitRoot` (blobs + on-chain) | 50-500ms | 20s |

Set the global timeout on `SDKConfig.Timeout` to the max expected operation time. For `WaitTxResult`, control duration via `context.WithTimeout`:

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
result, err := client.WaitTxResult(ctx, hash, time.Second)
```

### Retry Configuration

| Environment | RetryAttempts | RetryDelay |
|-------------|--------------|------------|
| Dev | 0 | — |
| Staging | 2 | 1s |
| Production | 3 | 2s |

The SDK retries only on transient errors (connection refused, timeout). It does **not** retry validation errors, capacity errors, or WASM execution failures.

For custom retry logic (exponential backoff, jitter), set `RetryAttempts=0` and implement your own — see [Error Handling](error-handling.md#retry-strategy).

## Authentication

### Server Side

Set `COSMOS_EXEC_AUTH_TOKEN` on the executor. All requests must include:

```
Authorization: Bearer <token>
```

Requests without a valid token receive HTTP 401.

### Client Side

```go
client, _ := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
    ExecURL:   "https://exec.mychain.io",
    AuthToken: os.Getenv("EXEC_AUTH_TOKEN"),
})
```

Token rotation: create a new client with the new token. The SDK does not cache tokens.

## CORS

Set `COSMOS_EXEC_CORS_ORIGIN` to your frontend domain:

```bash
# Single origin
export COSMOS_EXEC_CORS_ORIGIN=https://app.mychain.io

# Dev (allow all — NOT for production)
export COSMOS_EXEC_CORS_ORIGIN=*
```

## Rate Limiting

Server-side rate limiting via `COSMOS_EXEC_RATE_LIMIT_RPS`:

| Environment | Setting | Notes |
|-------------|---------|-------|
| Dev | `0` (disabled) | No limit |
| Staging | `50` | Match expected load |
| Production | `100` | Adjust based on hardware |

Client-side: if you hit HTTP 429, back off and retry after `RetryDelay`.

## Idempotency

### Blob Submissions

`SubmitBlob` is **idempotent** — submitting the same data returns the same commitment (content-addressed by SHA-256). Safe to retry on network errors without creating duplicates.

### Transaction Submissions

`SubmitTxBytes` is **NOT idempotent** — submitting the same tx bytes twice adds it to the mempool twice. If you get a network error after submission:

1. Call `GetTxResult(hash)` to check if the tx was already accepted.
2. Only resubmit if the tx was not found.

```go
resp, err := client.SubmitTxBytes(ctx, txBytes)
if err != nil {
    // Network error — check if tx was already accepted
    hash := fmt.Sprintf("%x", sha256.Sum256(txBytes))
    existing, _ := client.GetTxResult(ctx, hash)
    if existing != nil && existing.Found {
        // Tx already accepted, no need to resubmit
        return existing.Result, nil
    }
    // Tx not found — safe to retry
    resp, err = client.SubmitTxBytes(ctx, txBytes)
}
```

### Batch Submissions

`SubmitBatch` is **idempotent** — same input blobs produce the same root and commitments.

`CommitRoot` is **NOT fully idempotent** — the blob storage part is idempotent, but the on-chain tx submission is not. Use the same pattern as tx submissions.

## Monitoring

### Health Endpoints

```bash
curl http://executor:50051/health     # {"status":"ok"} or {"status":"error"}
curl http://executor:50051/healthz    # alias
curl http://executor:50051/ready      # readiness (initialized?)
```

### Metrics

Enable with `COSMOS_EXEC_METRICS=true`. Available at:

```bash
curl http://executor:50051/metrics        # Prometheus text format
curl http://executor:50051/metrics.json   # JSON format
```

Key metrics to monitor:

| Metric | Description | Alert threshold |
|--------|-------------|-----------------|
| `requests_total` | Total HTTP requests by endpoint | — |
| `requests_errors` | Error count by endpoint | > 1% of total |
| `blob_count` | Number of blobs in store | Approaching `max_store_total_size / avg_blob_size` |
| `blob_bytes` | Total blob store size | > 80% of `max_store_total_size` |
| `tx_result_count` | Number of executed txs | — |
| `block_count` | Number of executed blocks | Should increase monotonically |
| `mempool_size` | Pending txs in mempool | Sustained > 100 = bottleneck |

### Application-Level Monitoring

Track these in your app code:

```go
// After every SubmitBlob
if err != nil {
    metrics.IncrCounter("sdk.blob_submit.error", 1)
} else {
    metrics.IncrCounter("sdk.blob_submit.ok", 1)
    metrics.Histogram("sdk.blob_submit.bytes", float64(res.Size))
}

// After every WaitTxResult
metrics.Histogram("sdk.tx_wait.duration_ms", float64(elapsed.Milliseconds()))
if result.Code != 0 {
    metrics.IncrCounter("sdk.tx.failed", 1)
}
```

## SLO Recommendations

| Metric | Dev | Staging | Production |
|--------|-----|---------|------------|
| API availability | — | 99% | 99.9% |
| SubmitBlob p95 latency | — | < 200ms | < 100ms |
| WaitTxResult p95 (including block time) | — | < 10s | < 5s |
| Tx success rate (Code=0) | — | > 90% | > 95% |
| Block progress (blocks/minute) | — | > 20 | > 25 |
| Blob store utilization | — | < 90% | < 80% |

### Alerting Rules

| Condition | Severity | Action |
|-----------|----------|--------|
| `/health` returns non-200 for > 30s | Critical | Check executor process |
| `blob_bytes / max_store_total_size > 0.8` | Warning | Increase limit or rotate data |
| `mempool_size > 100` for > 60s | Warning | Check block production |
| No new blocks for > 30s | Critical | Check sequencer and DA layer |
| Error rate > 5% on any endpoint | Warning | Check logs for root cause |

## Environment Comparison

| Setting | Dev | Staging | Production |
|---------|-----|---------|------------|
| DB | `--in-memory` OK | Disk | Disk (durable) |
| Persistence | Optional | Enabled | **Required** |
| Auth token | None | Set | **Required** |
| CORS | `*` | Specific domain | **Specific domain** |
| Rate limit | 0 | 50 rps | 100 rps |
| Metrics | Off | On | **On** |
| TLS | None | Recommended | **Required** (via reverse proxy) |
| SDK timeout | 10s | 20s | 30s |
| SDK retry | 0 | 2 | 3 |
| Log level | debug | info | info |
