# Error Handling

## Error Structure

All SDK methods return `*SDKError` (or `nil`). It wraps the root cause with context:

```go
type SDKError struct {
    Op    string // Operation that failed: "SubmitBlob", "CommitRoot", etc.
    Cause error  // Underlying error (matchable with errors.Is)
    Hint  string // One-line actionable suggestion
}
```

```go
_, err := client.SubmitBlob(ctx, data)
if err != nil {
    var sdkErr *cosmoswasm.SDKError
    if errors.As(err, &sdkErr) {
        fmt.Println("operation:", sdkErr.Op)
        fmt.Println("cause:", sdkErr.Cause)
        fmt.Println("hint:", sdkErr.Hint)
    }
}
```

## Sentinel Errors

Match with `errors.Is()` for programmatic handling:

| Sentinel | Meaning | Retryable | Typical Action |
|----------|---------|-----------|----------------|
| `ErrNotReachable` | Executor is down / connection refused | Yes | Retry with backoff; alert if persistent |
| `ErrBlobTooLarge` | Blob exceeds `max_blob_size` (4 MB default) | No | Compress with `CompressIfBeneficial` or split with `ChunkBlob` |
| `ErrBlobStoreFull` | Total blob store capacity exceeded | No* | Reduce submission rate; restart executor to reclaim; increase `max_store_total_size` |
| `ErrTxFailed` | Transaction executed but failed (Code != 0) | No | Check `TxExecutionResult.Log` for the WASM error |
| `ErrContractMissing` | Contract address not provided | No | Set the `Contract` field |
| `ErrCommitMissing` | Commitment string empty | No | Pass the hex commitment from `SubmitBlob`/`CommitRoot` |

\* `ErrBlobStoreFull` is retryable after the operator takes action (restart/increase limits).

```go
if errors.Is(err, cosmoswasm.ErrNotReachable) {
    // Retry with backoff
} else if errors.Is(err, cosmoswasm.ErrBlobTooLarge) {
    // Split the data
    chunks, meta := cosmoswasm.ChunkBlob(data, cosmoswasm.DefaultMaxChunkSize)
    for _, chunk := range chunks {
        client.SubmitBlob(ctx, chunk)
    }
} else if errors.Is(err, cosmoswasm.ErrBlobStoreFull) {
    // Alert operator, back off
}
```

## Error Categories

### 1. Validation Errors (never retry)

Caused by incorrect input. Fix the caller code.

| Error message | Cause | Fix |
|---------------|-------|-----|
| `"tx bytes cannot be empty"` | Empty tx | Check tx building step |
| `"blob data cannot be empty"` | Empty blob | Check data source |
| `"contract is required"` | Missing contract address | Set `Contract` field |
| `"code id is required"` | `CodeID=0` in instantiate | Set `CodeID` from store tx result |
| `"msg must be valid json"` | Malformed JSON message | Validate JSON before passing |
| `"commitment required"` | Empty commitment string | Use commitment from `SubmitBlob`/`CommitRoot` |

### 2. Network Errors (retryable)

Transient failures — executor is temporarily unavailable.

| Error contains | Meaning | Retry? | Hint |
|----------------|---------|--------|------|
| `"connection refused"` | Executor not running | Yes | Start executor; retry with exponential backoff |
| `"deadline exceeded"` | Request timed out | Yes | Increase `SDKConfig.Timeout`; check executor load |
| `"context canceled"` | Caller cancelled | No | Intentional cancellation |

SDK auto-retries these when `SDKConfig.RetryAttempts > 0`:

```go
client, _ := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
    ExecURL:       "http://127.0.0.1:50051",
    RetryAttempts: 3,    // retry up to 3 times
    RetryDelay:    2*time.Second,
})
```

### 3. Capacity Errors (conditional retry)

The executor is running but cannot accept the request.

| Error | Retry after... |
|-------|----------------|
| `ErrBlobTooLarge` | Compress or chunk the data (never retry same payload) |
| `ErrBlobStoreFull` | Operator increases limits or restarts (back off, alert) |
| HTTP 429 (rate limited) | `RetryDelay` (respect rate limit) |

### 4. Execution Errors (never retry same tx)

The transaction was executed but the WASM logic rejected it.

```go
result, err := client.WaitTxResult(ctx, hash, time.Second)
if err != nil {
    // Network/timeout error — may retry
    log.Fatal(err)
}
if result.Code != 0 {
    // Execution error — tx was included but failed
    // Do NOT resubmit the same tx
    fmt.Println("WASM error:", result.Log)
    fmt.Println("Code:", result.Code)
}
```

Common WASM error codes:

| Code | Meaning |
|------|---------|
| `0` | Success |
| `2` | Tx parse error (malformed proto) |
| `5` | Insufficient funds |
| `11` | Out of gas |
| `18` | Contract execution failed (check `Log` for details) |

### 5. API Errors (HTTP 4xx/5xx)

Returned as `SDKError` with the HTTP status and response body:

```
SubmitBlob: api error (413): blob size 5242880 exceeds max 4194304
  hint: compress the data first (enabled by default in BatchBuilder) or split with ChunkBlob()
```

## Retry Strategy

### Recommended Backoff

```go
func submitWithRetry(ctx context.Context, client *cosmoswasm.Client, data []byte) (*cosmoswasm.BlobSubmitResponse, error) {
    var lastErr error
    for attempt := 0; attempt < 5; attempt++ {
        res, err := client.SubmitBlob(ctx, data)
        if err == nil {
            return res, nil
        }
        lastErr = err

        // Only retry transient errors
        if !errors.Is(err, cosmoswasm.ErrNotReachable) {
            return nil, err // validation/capacity error — don't retry
        }

        // Exponential backoff: 1s, 2s, 4s, 8s, 16s
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

Or use the built-in retry:

```go
client, _ := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
    ExecURL:       "http://127.0.0.1:50051",
    RetryAttempts: 3,
    RetryDelay:    2 * time.Second, // fixed delay (not exponential)
})
```

### What to Retry vs Not

| Scenario | Retry? | Why |
|----------|--------|-----|
| Connection refused | Yes | Executor may be restarting |
| Deadline exceeded | Yes | Transient network issue |
| Blob too large | No | Same payload will always fail |
| Store full | Wait + retry | Need operator action first |
| WASM execution failed | No | Same tx will fail again |
| Invalid JSON | No | Fix the message |
| Context cancelled | No | Caller decided to stop |

## Error → App Action Mapping

| Your app is... | Error | Action |
|----------------|-------|--------|
| Game server submitting events | `ErrNotReachable` | Buffer events locally, retry in 5s |
| Game server submitting events | `ErrBlobStoreFull` | Switch to local file, alert ops |
| Indexer polling tx results | `Found=false` | Wait one block time (2s), poll again |
| Indexer polling tx results | `ErrNotReachable` | Backoff 10s, re-establish connection |
| Contract deployer | `Code != 0` | Log error, do not retry same tx |
| DA bridge watcher | `PollBlobs` handler error | Return error to stop polling; log and investigate |
