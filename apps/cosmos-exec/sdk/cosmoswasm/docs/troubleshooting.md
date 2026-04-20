# Troubleshooting

## Quick Diagnostics

Before debugging, run these curl commands to check the executor:

```bash
# 1. Is the executor running?
curl -s http://127.0.0.1:50051/health
# Expected: {"status":"ok"}

# 2. Is it initialized?
curl -s http://127.0.0.1:50051/status
# Expected: {"initialized":true, "chain_id":"...", "latest_height":N, ...}

# 3. Can it accept blobs?
curl -s -X POST http://127.0.0.1:50051/blob/submit \
  -H 'Content-Type: application/json' \
  -d '{"data_base64":"dGVzdA=="}'
# Expected: {"commitment":"9f86d08...","size":4}

# 4. Are blocks being produced?
curl -s http://127.0.0.1:50051/blocks/latest
# Expected: {"height":N, "time":"...", "app_hash":"...", "num_txs":N}

# 5. Is the mempool draining?
curl -s http://127.0.0.1:50051/tx/pending
# Expected: {"pending_count":0}  (or small number)
```

---

## Common Issues

### 1. `connection refused` / `ErrNotReachable`

**Symptom:** SDK returns `executor not reachable: connection refused`.

**Causes & fixes:**

| Cause | Fix |
|-------|-----|
| Executor not started | `cd apps/cosmos-exec && go run ./cmd/cosmos-exec-grpc --in-memory` |
| Wrong port | Check `--address` flag or `COSMOS_EXEC_LISTEN_ADDR`. Default: `50051` |
| Process crashed | Check logs for panic. Restart executor |
| Firewall/Docker network | Ensure the port is accessible. In Docker, use `--network host` or expose port |

```bash
# Verify process is running
lsof -i :50051
# or
curl -s http://127.0.0.1:50051/health
```

### 2. `SubmitTxBytes` succeeds but `GetTxResult` returns `found=false`

**Symptom:** Transaction hash is returned, but polling finds nothing.

**Causes & fixes:**

| Cause | Fix |
|-------|-----|
| Blocks not being produced | Check `curl /blocks/latest`. If height isn't advancing, the sequencer may be stalled |
| Tx still in mempool | Wait one block time (default 2s). Use `WaitTxResult` instead of manual polling |
| Wrong hash | Ensure you're using the hash from `SubmitTxResponse`, not computing your own |
| Executor restarted | In-memory mode loses all state on restart. Use persistence |

```go
// Correct pattern: submit + wait with timeout
ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
defer cancel()
resp, _ := client.SubmitTxBytes(ctx, txBytes)
result, err := client.WaitTxResult(ctx, resp.Hash, time.Second)
if err != nil {
    // context.DeadlineExceeded = blocks not advancing
    log.Fatal(err)
}
```

### 3. Transaction executed but `Code != 0`

**Symptom:** `WaitTxResult` returns a result but with non-zero code.

**Debug steps:**

```go
result, _ := client.WaitTxResult(ctx, hash, time.Second)
if result.Code != 0 {
    fmt.Println("Code:", result.Code)
    fmt.Println("Log:", result.Log)  // <-- WASM error message is here
}
```

Common error logs:

| Log contains | Cause | Fix |
|-------------|-------|-----|
| `"failed to execute message"` | WASM contract rejected the message | Check contract's execute handler |
| `"contract not found"` | Wrong contract address | Verify bech32 address |
| `"unknown message"` | Contract doesn't handle this message type | Check contract's supported messages |
| `"unauthorized"` | Sender doesn't have permission | Check contract's ownership/ACL |
| `"insufficient funds"` | Not enough balance | Fund the sender account |

### 4. `blob size ... exceeds max`

**Symptom:** `ErrBlobTooLarge` when submitting a blob.

**Fix:** Default max is 4 MB. Options:

```go
// Option 1: Compress first
compressed, ok := cosmoswasm.CompressIfBeneficial(data)
if ok {
    client.SubmitBlob(ctx, compressed)
}

// Option 2: Split into chunks
chunks, meta := cosmoswasm.ChunkBlob(data, cosmoswasm.DefaultMaxChunkSize)
for _, chunk := range chunks {
    client.SubmitBlob(ctx, chunk)
}

// Option 3: Increase server limit
// COSMOS_EXEC_MAX_BLOB_SIZE=8388608  (8 MB)
```

### 5. `store full` / `ErrBlobStoreFull`

**Symptom:** `blob store capacity exceeded`.

**Fix:** The in-memory blob store has a total size limit (default 256 MB dev, 1 GB prod).

```bash
# Check current usage
curl -s http://127.0.0.1:50051/metrics.json | jq '.blob_bytes, .blob_count'

# Increase limit
export COSMOS_EXEC_MAX_STORE_SIZE=2147483648  # 2 GB
```

Or restart the executor (in-memory store is cleared on restart).

### 6. `context deadline exceeded` (timeout)

**Symptom:** Requests are timing out.

**Causes & fixes:**

| Cause | Fix |
|-------|-----|
| SDK timeout too low | Increase `SDKConfig.Timeout` (default 20s) |
| Executor overloaded | Check `curl /metrics.json` — high `mempool_size` = block production bottleneck |
| Large blob upload on slow network | Increase timeout; consider chunking |
| WASM query too complex | Increase `query_gas_max` on executor |

### 7. Auth errors (HTTP 401)

**Symptom:** `api error (401): unauthorized`.

**Fix:**

```go
// Client must include the same token as the server
client, _ := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
    ExecURL:   "http://127.0.0.1:50051",
    AuthToken: "same-token-as-COSMOS_EXEC_AUTH_TOKEN",
})
```

```bash
# curl equivalent
curl -H "Authorization: Bearer same-token" http://127.0.0.1:50051/status
```

### 8. Rate limited (HTTP 429)

**Symptom:** `api error (429): rate limit exceeded`.

**Fix:** Reduce request rate or increase server limit:

```bash
export COSMOS_EXEC_RATE_LIMIT_RPS=200
```

Client side: add delay between requests or use `BatchBuilder` to reduce call frequency.

### 9. DA URL errors

**Symptom:** `DABridge.Submit` fails with connection errors.

**Checklist:**

```bash
# 1. Is DA node reachable?
curl -s http://localhost:26658/header/1

# 2. Is auth token valid?
curl -s -H "Authorization: Bearer $DA_AUTH_TOKEN" http://localhost:26658/header/1

# 3. Common URL mistakes:
#    Wrong: https://localhost:26658  (DA node is usually HTTP, not HTTPS)
#    Wrong: http://localhost:26657   (26657 is CometBFT RPC, not DA)
#    Right: http://localhost:26658   (DA bridge RPC)
```

### 10. Executor state lost after restart

**Symptom:** After restarting, `initialized=false`, no blocks, no blobs.

**Cause:** Running with `--in-memory` or persistence not enabled.

**Fix:**

```bash
# Enable persistence
go run ./cmd/cosmos-exec-grpc --profile prod --home /data/cosmos-exec

# Verify persistence is on
# Look for log line: "persistence enabled" dir="/data/cosmos-exec/data"
```

### 11. Port already in use

**Symptom:** `bind: address already in use`.

```bash
# Find what's using the port
lsof -i :50051

# Kill the old process
kill $(lsof -t -i :50051)

# Or use a different port
go run ./cmd/cosmos-exec-grpc --address 0.0.0.0:50052
```

---

## Debug Checklist

When something doesn't work, check in this order:

1. **Executor running?** → `curl /health`
2. **Executor initialized?** → `curl /status` (check `initialized`)
3. **Blocks advancing?** → `curl /blocks/latest` (check `height` increases)
4. **Auth OK?** → `curl -H "Authorization: Bearer $TOKEN" /status`
5. **Blob store OK?** → `curl -X POST /blob/submit -d '{"data_base64":"dGVzdA=="}'`
6. **Mempool draining?** → `curl /tx/pending` (should be 0 or small)
7. **Executor logs** → Look for `ERROR` or `panic` in stdout
8. **SDK error details** → Check `SDKError.Hint` for actionable advice

## Getting Help

If you've checked all the above and still stuck:

1. Capture the full `SDKError` output (Op, Cause, Hint)
2. Run the diagnostic curl commands above and save output
3. Check executor logs for errors around the same timestamp
4. Open an issue at the repo with the above information
