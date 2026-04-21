# API Reference

All methods below are public stable API. Breaking changes require a major version bump.

---

## Client Setup

### `NewClient(baseURL) → *Client`

**Purpose:** Create a Client for quick development use. Connects to a single executor endpoint with default settings (20s timeout, no retry, no auth).

**Params:**

| Param | Type | Required | Notes |
|-------|------|----------|-------|
| `baseURL` | `string` | No | Empty defaults to `http://127.0.0.1:50051` |

**Example:**

```go
client := cosmoswasm.NewClient("http://127.0.0.1:50051")
```

**When to use:** Local dev, quick scripts. For production, use `NewClientFromConfig`.

---

### `NewClientFromConfig(cfg) → (*Client, error)`

**Purpose:** Create a Client with full control over timeout, retry, auth, and HTTP transport. Recommended for production.

**Params: `SDKConfig`**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `ExecURL` | `string` | — | **Required.** Base URL of cosmos-exec-grpc |
| `Timeout` | `time.Duration` | `20s` | Per-request HTTP timeout |
| `RetryAttempts` | `int` | `0` | Number of retries for transient failures (connection refused, timeout) |
| `RetryDelay` | `time.Duration` | `1s` | Delay between retries |
| `AuthToken` | `string` | `""` | Sent as `Authorization: Bearer <token>` on every request |
| `ChainID` | `string` | `""` | Used for chain-aware tx building |
| `HTTPClient` | `*http.Client` | `nil` | Custom HTTP client (TLS, proxies). `nil` = auto-created with Timeout |

**Errors:**

| Error | Cause |
|-------|-------|
| `"ExecURL is required"` | Empty `ExecURL` |

**Example:**

```go
client, err := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
    ExecURL:       "http://127.0.0.1:50051",
    Timeout:       20 * time.Second,
    RetryAttempts: 3,
    RetryDelay:    1 * time.Second,
    AuthToken:     os.Getenv("COSMOS_EXEC_AUTH_TOKEN"),
})
```

---

### `DefaultSDKConfig() → SDKConfig`

Returns an `SDKConfig` with sensible defaults. You must set `ExecURL` before use.

```go
cfg := cosmoswasm.DefaultSDKConfig()
cfg.ExecURL = "http://my-executor:50051"
client, _ := cosmoswasm.NewClientFromConfig(cfg)
```

---

### `Client.WithHTTPClient(httpClient) → *Client`

Returns a shallow clone of the Client with a different `http.Client`. Useful for injecting custom TLS or proxy config after creation.

```go
tlsClient := &http.Client{Transport: customTLSTransport}
secureClient := client.WithHTTPClient(tlsClient)
```

---

## Transaction APIs

### `SubmitTxBytes(ctx, txBytes) → (*SubmitTxResponse, error)`

**Purpose:** Submit a signed Cosmos SDK transaction to the executor mempool.

**Params:**

| Param | Type | Required | Notes |
|-------|------|----------|-------|
| `ctx` | `context.Context` | Yes | Cancelled context aborts the request |
| `txBytes` | `[]byte` | Yes | Protobuf-encoded `TxRaw` bytes |

**Response: `SubmitTxResponse`**

| Field | Type | Description |
|-------|------|-------------|
| `Hash` | `string` | Hex-encoded SHA-256 of the tx bytes |

**Errors:**

| Error | Retryable | Cause |
|-------|-----------|-------|
| `ErrNotReachable` | Yes | Executor is down or unreachable |
| `"tx bytes cannot be empty"` | No | Empty input |
| HTTP 400 | No | Malformed transaction |

**Example:**

```go
tx, _ := cosmoswasm.BuildExecuteTx(cosmoswasm.ExecuteTxRequest{
    Contract: "cosmos1abc...",
    Msg:      `{"transfer":{"recipient":"cosmos1xyz...","amount":"100"}}`,
})
resp, err := client.SubmitTxBytes(ctx, tx)
if err != nil {
    log.Fatal(err)
}
fmt.Println("tx hash:", resp.Hash)
```

**Operational notes:** Typical latency < 10ms (mempool insert only). The tx is included in the next block (block_time, default 2s). For immediate confirmation, follow with `WaitTxResult`.

---

### `SubmitTxBase64(ctx, txBase64) → (*SubmitTxResponse, error)`

Same as `SubmitTxBytes` but accepts base64-encoded string. Useful when receiving tx from frontend/CLI.

---

### `GetTxResult(ctx, txHash) → (*GetTxResultResponse, error)`

**Purpose:** Check whether a transaction has been executed and get the result.

**Params:**

| Param | Type | Required | Notes |
|-------|------|----------|-------|
| `txHash` | `string` | Yes | Hex hash (with or without `0x` prefix) |

**Response: `GetTxResultResponse`**

| Field | Type | Description |
|-------|------|-------------|
| `Found` | `bool` | `true` if the tx has been executed |
| `Result` | `*TxExecutionResult` | Present only when `Found=true` |

**`TxExecutionResult`:**

| Field | Type | Description |
|-------|------|-------------|
| `Hash` | `string` | Tx hash |
| `Height` | `uint64` | Block height where tx was executed |
| `Code` | `uint32` | Result code. `0` = success, non-zero = failed |
| `Log` | `string` | Execution log (error message when Code != 0) |
| `Events` | `[]TxEvent` | Execution events (WASM events, transfers, etc.) |

**Errors:**

| Error | Retryable | Cause |
|-------|-----------|-------|
| `ErrNotReachable` | Yes | Executor down |
| `"tx hash is required"` | No | Empty input |

**Example:**

```go
res, err := client.GetTxResult(ctx, "a1b2c3...")
if err != nil {
    log.Fatal(err)
}
if !res.Found {
    fmt.Println("tx not yet executed")
} else if res.Result.Code == 0 {
    fmt.Println("success at height", res.Result.Height)
} else {
    fmt.Println("failed:", res.Result.Log)
}
```

**Operational notes:** Returns immediately. If `Found=false`, the tx is either still in mempool or was never submitted. Poll with `WaitTxResult` instead of manual loops.

---

### `WaitTxResult(ctx, txHash, pollInterval) → (*TxExecutionResult, error)`

**Purpose:** Block until a transaction is executed and return the result.

**Params:**

| Param | Type | Required | Notes |
|-------|------|----------|-------|
| `txHash` | `string` | Yes | Tx hash |
| `pollInterval` | `time.Duration` | No | Default `1s`. How often to poll |

**Response:** Returns `TxExecutionResult` directly (no `Found` wrapper).

**Errors:**

| Error | Retryable | Cause |
|-------|-----------|-------|
| `context.DeadlineExceeded` | — | Context timed out before tx was included |
| `context.Canceled` | — | Caller cancelled |
| `ErrNotReachable` | Yes | Executor down mid-poll |

**Example:**

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

resp, _ := client.SubmitTxBytes(ctx, txBytes)
result, err := client.WaitTxResult(ctx, resp.Hash, time.Second)
if err != nil {
    log.Fatal(err) // timeout or network error
}
if result.Code != 0 {
    log.Fatalf("tx failed: %s", result.Log)
}
fmt.Printf("success at height %d\n", result.Height)
```

**Operational notes:** Set a context timeout. Default block time is 2s, so `pollInterval=1s` with a 30s timeout is reasonable. For batch operations, submit all txs first, then wait for each.

---

## Blob APIs

### `SubmitBlob(ctx, data) → (*BlobSubmitResponse, error)`

**Purpose:** Store arbitrary data in the executor's content-addressed blob store. Returns a SHA-256 commitment to record on-chain.

**Params:**

| Param | Type | Required | Limits |
|-------|------|----------|--------|
| `data` | `[]byte` | Yes | Max `4 MB` (configurable via `max_blob_size`) |

**Response: `BlobSubmitResponse`**

| Field | Type | Description |
|-------|------|-------------|
| `Commitment` | `string` | Hex-encoded SHA-256 of the data |
| `Size` | `int` | Number of bytes stored |

**Errors:**

| Error | Retryable | Cause |
|-------|-----------|-------|
| `ErrBlobTooLarge` | No | Data exceeds `max_blob_size` — compress or use `ChunkBlob` |
| `ErrBlobStoreFull` | No* | Total store exceeded `max_store_total_size` — restart executor or reduce frequency |
| `ErrNotReachable` | Yes | Executor down |

**Example:**

```go
res, err := client.SubmitBlob(ctx, []byte(`{"event":"player_scored","score":42}`))
if err != nil {
    var sdkErr *cosmoswasm.SDKError
    if errors.As(err, &sdkErr) {
        fmt.Println("hint:", sdkErr.Hint)
    }
    log.Fatal(err)
}
fmt.Printf("commitment: %s (%d bytes)\n", res.Commitment, res.Size)
```

**Operational notes:** SubmitBlob is idempotent — submitting the same data returns the same commitment. Typical latency < 5ms. For data > 512 KB, consider `ChunkBlob` to split before submitting.

---

### `RetrieveBlob(ctx, commitment) → (*BlobRetrieveResponse, error)`

**Purpose:** Fetch a blob by its SHA-256 commitment.

**Params:**

| Param | Type | Required |
|-------|------|----------|
| `commitment` | `string` | Yes, hex SHA-256 |

**Response: `BlobRetrieveResponse`**

| Field | Type | Description |
|-------|------|-------------|
| `Commitment` | `string` | Echo of the requested commitment |
| `DataBase64` | `string` | Blob data as base64 |
| `Size` | `int` | Data size in bytes |

**Errors:**

| Error | Retryable | Cause |
|-------|-----------|-------|
| `ErrCommitMissing` | No | Empty commitment string |
| HTTP 404 | No | Blob not in store (never stored, or executor restarted without persistence) |

---

### `RetrieveBlobData(ctx, commitment) → ([]byte, error)`

Convenience wrapper — returns decoded `[]byte` directly instead of base64.

```go
data, err := client.RetrieveBlobData(ctx, "a1b2c3...")
```

---

### `SubmitBatch(ctx, blobs) → (*BlobBatchResponse, error)`

**Purpose:** Upload N blobs, compute a binary Merkle root over their SHA-256 commitments. Record the single root on-chain instead of N individual commitments.

**Params:**

| Param | Type | Required | Limits |
|-------|------|----------|--------|
| `blobs` | `[][]byte` | Yes | Each blob max `4 MB` |

**Response: `BlobBatchResponse`**

| Field | Type | Description |
|-------|------|-------------|
| `Root` | `string` | Hex Merkle root — this is what goes on-chain |
| `Commitments` | `[]string` | Per-blob SHA-256 commitments (for retrieval/proofs) |
| `Count` | `int` | Number of blobs |

**Example:**

```go
batch, err := client.SubmitBatch(ctx, [][]byte{event1, event2, event3})
if err != nil {
    log.Fatal(err)
}
// Record root on-chain (32 bytes regardless of N blobs)
tx, _ := cosmoswasm.BuildBatchRootTx(cosmoswasm.BatchRootTxRequest{
    Contract: "cosmos1abc...",
    Root:     batch.Root,
    Count:    batch.Count,
    Tag:      "game-events",
})
client.SubmitTxBytes(ctx, tx)
```

**Operational notes:** Batch is atomic — all blobs are stored or none. Commitment order is preserved. Use `GetProof(batch.Commitments, index)` to prove inclusion of any blob.

---

### `CommitRoot(ctx, req) → (*CommitReceipt, error)`

**Purpose:** One-call workflow: store blobs + build Merkle root + submit on-chain root tx.

**Params: `CommitRootRequest`**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `Blobs` | `[][]byte` | Yes | Data payloads |
| `Contract` | `string` | Yes | WASM contract bech32 address |
| `Sender` | `string` | No | Default: `DefaultSender()` |
| `Tag` | `string` | No | Application label |
| `Extra` | `map[string]any` | No | Extra fields in the on-chain message |

**Response: `CommitReceipt`**

| Field | Type | Description |
|-------|------|-------------|
| `Root` | `string` | Merkle root |
| `TxHash` | `string` | On-chain tx hash |
| `Count` | `int` | Number of blobs |
| `Refs` | `[]BlobRef` | Per-blob commitment + size |

**Errors:**

| Error | Retryable | Cause |
|-------|-----------|-------|
| `ErrContractMissing` | No | Empty `Contract` |
| `ErrNotReachable` | Yes | Executor down |
| `ErrBlobTooLarge` | No | Single blob too large |

---

### `CommitCritical(ctx, req) → (*CommitReceipt, error)`

Same as `CommitRoot` but intended for events that must be submitted immediately (purchases, rewards, settlements) rather than buffered in a `BatchBuilder`. Bypasses any pending batch and always flushes inline. Semantically identical to `CommitRoot` — the distinction is in call-site intent.

---

## Query APIs

### `QuerySmart(ctx, contract, msg) → (map[string]any, error)`

**Purpose:** Execute a read-only WASM smart query against contract state.

**Params:**

| Param | Type | Required | Notes |
|-------|------|----------|-------|
| `contract` | `string` | Yes | Bech32 contract address |
| `msg` | `any` | Yes | Query message — accepts `string`, `[]byte`, `map`, or struct (JSON-marshalled) |

**Response:** `map[string]any` — parsed JSON object from the contract.

**Example:**

```go
result, err := client.QuerySmart(ctx, "cosmos1abc...", `{"token_info":{}}`)
if err != nil {
    log.Fatal(err)
}
fmt.Println("name:", result["name"])
fmt.Println("symbol:", result["symbol"])
```

**Operational notes:** Queries do not modify state and are not included in blocks. Gas-limited to `query_gas_max` (default 50M). Response time depends on contract complexity, typically < 50ms.

---

### `QuerySmartRaw(ctx, contract, msg) → (*QuerySmartResponse, error)`

Returns the raw response without parsing:

| Field | Type | Description |
|-------|------|-------------|
| `Data` | `any` | Parsed result (if valid JSON object) |
| `DataRaw` | `string` | Raw string result (fallback) |

---

## Transaction Builders

These build protobuf-encoded `TxRaw` bytes locally (no network call).

### `BuildStoreTx(wasmBytes, sender) → ([]byte, error)`

Build a `MsgStoreCode` transaction to upload WASM bytecode.

| Param | Type | Required |
|-------|------|----------|
| `wasmBytes` | `[]byte` | Yes — compiled `.wasm` file content |
| `sender` | `string` | No — defaults to `DefaultSender()` |

### `BuildInstantiateTx(req) → ([]byte, error)`

Build a `MsgInstantiateContract` transaction.

| Field | Type | Required |
|-------|------|----------|
| `CodeID` | `uint64` | Yes |
| `Msg` | `any` | Yes — init message (string/map/struct, JSON-marshalled) |
| `Label` | `string` | No — defaults to `"wasm-via-sdk"` |
| `Sender` | `string` | No |
| `Admin` | `string` | No |

### `BuildExecuteTx(req) → ([]byte, error)`

Build a `MsgExecuteContract` transaction.

| Field | Type | Required |
|-------|------|----------|
| `Contract` | `string` | Yes — bech32 address |
| `Msg` | `any` | Yes — execute message |
| `Sender` | `string` | No |

### `BuildBlobCommitTx(req) → ([]byte, error)`

Build a tx that records a single blob commitment in a WASM contract. The contract must handle `{"record_blob": {"commitment": "...", "tag": "..."}}`.

### `BuildBatchRootTx(req) → ([]byte, error)`

Build a tx that records a Merkle batch root. The contract must handle `{"record_batch": {"root": "...", "count": N, "tag": "..."}}`.

### `DefaultSender() → string`

Returns a deterministic placeholder sender address (`cosmos1qqqqq...`). Used when `Sender` is empty in tx builders. For production, use real wallet addresses.

### `EncodeTxBase64(tx) → string`

Encode tx bytes as standard base64. Use when submitting via `SubmitTxBase64` or passing to frontends.

### `EncodeTxHex(tx) → string`

Encode tx bytes as hex. Use when submitting via `tx_hex` JSON field.

---

## Data Integrity

### `GetProof(commitments, leafIndex) → (*MerkleProof, error)`

Build a Merkle inclusion proof for blob at `leafIndex`.

**`MerkleProof`:**

| Field | Type | Description |
|-------|------|-------------|
| `Root` | `string` | Merkle root (matches what was committed on-chain) |
| `LeafIndex` | `int` | Blob position |
| `Commitment` | `string` | Blob's SHA-256 |
| `Path` | `[]MerklePathStep` | Sibling hashes from leaf to root |

### `BuildMerkleProof(commitments, leafIndex) → (*MerkleProof, error)`

Same as `GetProof`. Lower-level name used internally. Prefer `GetProof` in application code.

### `VerifyMerkleProof(proof) → error`

Verify a proof. Returns `nil` on success.

```go
proof, _ := cosmoswasm.GetProof(batch.Commitments, 5)
if err := cosmoswasm.VerifyMerkleProof(proof); err != nil {
    log.Fatal("proof invalid:", err)
}
```

### `ChunkBlob(data, maxChunkSize) → ([][]byte, *ChunkMeta)`

Split large blob into chunks. Returns `nil` meta if data fits in one chunk.

### `ReassembleChunks(chunks, meta) → ([]byte, error)`

Reassemble with SHA-256 integrity check.

### `CompressIfBeneficial(data) → ([]byte, bool)`

Gzip compress. Returns original if compression doesn't help (encrypted/random data).

### `CompressGzip(data) → ([]byte, error)`

Compress data using gzip at best-speed level. ~60-70% of best-compression ratio at 5-10x throughput.

### `DecompressGzip(data) → ([]byte, error)`

Decompress gzip data. Returns raw bytes. Errors if input is not valid gzip.

### `IsGzipCompressed(data) → bool`

Returns `true` when data starts with the gzip magic bytes (`0x1f 0x8b`).

### `MaybeDecompress(data) → ([]byte, error)`

Decompress if gzipped, otherwise passthrough. Safe on any input.

---

## Cost Estimation

### `EstimateCost(req) → *CostEstimate`

**Purpose:** Compare gas cost of storing data two ways: direct on-chain vs blob-first.

**Params: `EstimateCostRequest`**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `DataBytes` | `int` | — | Total data size |
| `GasPriceTIA` | `float64` | `0.002` | Celestia gas price (uTIA/gas) |
| `MaxBlobSize` | `int` | `4 MB` | Per-blob DA limit |

**Response: `CostEstimate`**

| Field | Type | Description |
|-------|------|-------------|
| `DataBytes` | `int` | Input size |
| `CompressedBytes` | `int` | Estimated compressed size (~50%) |
| `DirectTx` | `CostBreakdown` | Gas for embedding in WASM messages |
| `BlobCommit` | `CostBreakdown` | Gas for blob-first pattern |
| `SavingsPercent` | `float64` | `(1 - blob/direct) * 100` |
| `NumBatches` | `int` | Number of DA submissions needed |

**`CostBreakdown`:** `DAGas`, `OnChainGas`, `TotalGas`, `EstFeeTIA`

```go
est := cosmoswasm.EstimateCost(cosmoswasm.EstimateCostRequest{DataBytes: 1024 * 1024})
fmt.Printf("direct: %d gas, blob: %d gas, savings: %.0f%%\n",
    est.DirectTx.TotalGas, est.BlobCommit.TotalGas, est.SavingsPercent)
```

### `DefaultEstimateCostRequest() → EstimateCostRequest`

Returns an `EstimateCostRequest` with defaults pre-filled (`GasPriceTIA=0.002`, `MaxBlobSize=4MB`). Set `DataBytes` before calling `EstimateCost`.

```go
req := cosmoswasm.DefaultEstimateCostRequest()
req.DataBytes = 500_000
est := cosmoswasm.EstimateCost(req)
```

---

## Batch Builder

### `NewBatchBuilder(client, cfg) → *BatchBuilder`

**Purpose:** Create an accumulator that auto-flushes blobs as a single `CommitRoot` call when size threshold is reached or a timer fires.

**Params:**

| Param | Type | Description |
|-------|------|-------------|
| `client` | `ExecutorClient` | Any client (`*Client`, `*MockExecutorClient`) |
| `cfg` | `BatchBuilderConfig` | Configuration (see below) |

**`BatchBuilderConfig`:**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Contract` | `string` | — | **Required.** WASM contract bech32 address |
| `Sender` | `string` | `DefaultSender()` | Tx sender |
| `Tag` | `string` | `""` | Application label for each CommitRoot |
| `MaxBytes` | `int` | `3 MB` | Flush when accumulated size exceeds this |
| `Compress` | `*bool` | `true` | Gzip each blob before accumulating |
| `MaxChunkSize` | `int` | `512 KiB` | Auto-split blobs larger than this |
| `Extra` | `map[string]any` | `nil` | Extra fields in on-chain root message |

**Example:**

```go
cfg := cosmoswasm.DefaultBatchBuilderConfig()
cfg.Contract = "cosmos1abc..."
cfg.Tag = "game-events"
bb := cosmoswasm.NewBatchBuilder(client, cfg)
```

---

### `DefaultBatchBuilderConfig() → BatchBuilderConfig`

Returns a config with all defaults: `MaxBytes=3MB`, `Compress=true`, `MaxChunkSize=512KiB`. You must set `Contract`.

---

### `BatchBuilder.Add(ctx, data, fn) → (*CommitReceipt, error)`

**Purpose:** Append data to the batch. If adding would exceed `MaxBytes`, the existing batch is flushed first via `fn`, then data is added to a fresh batch.

| Param | Type | Description |
|-------|------|-------------|
| `data` | `[]byte` | Non-empty blob data |
| `fn` | `FlushFunc` | Called with accumulated blobs when flush is triggered |

**`FlushFunc`:** `func(ctx context.Context, blobs [][]byte) (*CommitReceipt, error)`

Returns `(nil, nil)` when no flush was triggered. Returns `(*CommitReceipt, nil)` when a flush occurred.

When `Compress=true`, data is gzip-compressed before accumulating (if beneficial). When data exceeds `MaxChunkSize`, it is automatically split into chunks.

---

### `BatchBuilder.Flush(ctx, fn) → (*CommitReceipt, error)`

Force-flush the current batch regardless of size. Returns `(nil, nil)` when empty.

---

### `BatchBuilder.StartAutoFlush(ctx, interval) → <-chan FlushResult`

Launch a background goroutine that flushes on every `interval` tick. Returns a channel of `FlushResult` (receipt + error). The goroutine exits and closes the channel when `ctx` is cancelled. On shutdown, performs a final flush.

```go
ctx, cancel := context.WithCancel(context.Background())
results := bb.StartAutoFlush(ctx, 5*time.Second)

// In game loop:
bb.Add(ctx, eventBytes, commitFn)

// On shutdown:
cancel()
for r := range results {
    if r.Err != nil {
        log.Println("flush error:", r.Err)
    }
}
```

**`FlushResult`:** `Receipt *CommitReceipt`, `Err error`

---

### `BatchBuilder.Len() → int`

Number of blobs currently queued.

### `BatchBuilder.Bytes() → int`

Total byte size of queued blobs.

---

## DA Layer

### Namespace

```go
ns := cosmoswasm.NamespaceFromString("my-game")          // deterministic from name
ns, _ := cosmoswasm.NewNamespaceV0([]byte("myapp"))       // from raw bytes (max 10)
ns, _ := cosmoswasm.NamespaceFromHex("0x00...deadbeef")   // from hex
ns.Hex()   // "0x00..."
ns.Bytes() // [29]byte
```

### `DAClient` interface

Transport interface for Data Availability layer operations (e.g. Celestia). Implementations: production `CelestiaDAClient`, testing `MockDAClient`.

| Method | Signature | Description |
|--------|-----------|-------------|
| `SubmitBlobs` | `(ctx, namespace, blobs, opts) → (*DASubmitResult, error)` | Submit blobs under namespace |
| `GetBlobs` | `(ctx, namespace, height) → ([]*DABlob, error)` | Retrieve blobs at DA height |
| `GetBlobByCommitment` | `(ctx, namespace, height, commitment) → (*DABlob, error)` | Get specific blob |
| `Subscribe` | `(ctx, namespace) → (<-chan *DABlobEvent, error)` | Watch for new blobs (real-time) |
| `GetHeight` | `(ctx) → (uint64, error)` | Latest DA layer height |

**`DASubmitOptions`:** Optional gas overrides — `GasPrice float64`, `GasLimit uint64`. Zero = node default.

**`DABlob`:** `Namespace`, `Data []byte`, `Commitment []byte`, `Height uint64`, `Index int`

**`DABlobEvent`:** `Height uint64`, `Blobs []*DABlob`, `Timestamp time.Time`

---

### `DANamespaceConfig`

Per-app-chain DA configuration. Set once, reuse across operations.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `Namespace` | `*Namespace` | Yes | App-chain's dedicated namespace |
| `DANodeAddr` | `string` | Yes | DA node RPC (e.g. `http://localhost:26658`) |
| `AuthToken` | `string` | No | Bearer token for DA node |
| `SubmitOptions` | `*DASubmitOptions` | No | Default gas overrides |

```go
daCfg := cosmoswasm.DANamespaceConfig{
    Namespace:  cosmoswasm.NamespaceFromString("my-game"),
    DANodeAddr: "http://localhost:26658",
    AuthToken:  os.Getenv("DA_AUTH_TOKEN"),
}
if err := daCfg.Validate(); err != nil {
    log.Fatal(err)
}
```

---

### `NewDABridge(da, exec, namespace) → *DABridge`

Combines `DAClient` + `ExecutorClient` under a namespace.

```go
bridge := cosmoswasm.NewDABridge(daClient, execClient, cosmoswasm.NamespaceFromString("my-game"))
```

### DABridge methods

| Method | Description |
|--------|-------------|
| `Submit(ctx, blobs, opts)` | Submit blobs to DA layer under namespace |
| `GetBlobs(ctx, height)` | Retrieve blobs at DA height |
| `Watch(ctx)` | Subscribe to new blobs (WebSocket) |
| `PollBlobs(ctx, startHeight, interval, handler)` | HTTP polling fallback for environments without WebSocket |
| `SubmitAndCommit(ctx, req)` | DA submit + on-chain commit in one call |
| `DAHeight(ctx)` | Latest DA layer height |
| `Namespace()` | Returns the bridge's namespace |

**`SubmitAndCommitRequest`:** `Blobs [][]byte`, `Contract string`, `Sender string`, `Tag string`, `DASubmitOptions *DASubmitOptions`

**`SubmitAndCommitReceipt`:** `DAResult *DASubmitResult`, `OnChainReceipt *CommitReceipt`

---

## Errors

### `SDKError`

All SDK public methods return `*SDKError` (or `nil`). It wraps a root cause with context and a suggested action.

| Field | Type | Description |
|-------|------|-------------|
| `Op` | `string` | SDK operation that failed (e.g. `"SubmitBlob"`, `"CommitRoot"`) |
| `Cause` | `error` | Underlying error — match with `errors.Is()` |
| `Hint` | `string` | One-line suggestion for the developer |

```go
res, err := client.SubmitBlob(ctx, data)
if err != nil {
    var sdkErr *cosmoswasm.SDKError
    if errors.As(err, &sdkErr) {
        fmt.Println("op:", sdkErr.Op)
        fmt.Println("hint:", sdkErr.Hint)
    }
    if errors.Is(err, cosmoswasm.ErrNotReachable) {
        // retry or alert
    }
}
```

### Sentinel Errors

Match with `errors.Is(err, cosmoswasm.ErrXxx)`:

| Error | Meaning | Retryable |
|-------|---------|-----------|
| `ErrNotReachable` | Executor is down or unreachable | Yes |
| `ErrBlobTooLarge` | Blob exceeds `max_blob_size` | No — compress or chunk first |
| `ErrBlobStoreFull` | Blob store capacity exceeded | No* — restart executor or reduce frequency |
| `ErrTxFailed` | Transaction failed (code != 0) | No — check `result.Log` |
| `ErrContractMissing` | Contract address required but empty | No — set `Contract` field |
| `ErrCommitMissing` | Commitment required but empty | No — pass commitment from `SubmitBlob` |

---

## Testing Mocks

### `NewMockClient() → *MockExecutorClient`

In-memory executor mock. Implements `ExecutorClient`. All methods work without network.

```go
mock := cosmoswasm.NewMockClient()
res, _ := mock.SubmitBlob(ctx, []byte("hello"))
data, _ := mock.RetrieveBlobData(ctx, res.Commitment)
```

**Mock helper methods:**

| Method | Description |
|--------|-------------|
| `OnQuery(fn)` | Set custom handler for `QuerySmartRaw`/`QuerySmart` calls |
| `OnSubmit(fn)` | Set custom handler for `SubmitTxBytes`/`SubmitTxBase64` calls |
| `SetTxResult(hash, result)` | Pre-populate a tx result so `WaitTxResult` returns it |

```go
// Custom query behavior
mock.OnQuery(func(contract string, msg any) (*cosmoswasm.QuerySmartResponse, error) {
    return &cosmoswasm.QuerySmartResponse{Data: map[string]any{"balance": "1000"}}, nil
})

// Custom submit behavior (simulate failure)
mock.OnSubmit(func(txBytes []byte) (*cosmoswasm.SubmitTxResponse, error) {
    return nil, fmt.Errorf("simulated failure")
})

// Pre-populate tx results
mock.SetTxResult("abc123", &cosmoswasm.TxExecutionResult{Code: 0, Height: 5})
```

---

### `NewMockDAClient() → *MockDAClient`

In-memory DA mock. Implements `DAClient`. Namespace-isolated, supports subscribe notifications.

```go
daMock := cosmoswasm.NewMockDAClient()
ns := cosmoswasm.NamespaceFromString("test")
bridge := cosmoswasm.NewDABridge(daMock, mock, ns)
```

**Mock helper methods:**

| Method | Description |
|--------|-------------|
| `SetHeight(h)` | Manually set the DA height |
| `InjectBlobs(namespace, height, blobs)` | Inject blobs without going through `SubmitBlobs` — simulate blobs from other chains |

```go
daMock.SetHeight(100)
daMock.InjectBlobs(ns, 50, []*cosmoswasm.DABlob{
    {Namespace: ns, Data: []byte("injected"), Height: 50, Index: 0},
})
```

---

## Dev Tooling

These are for local development and integration testing. May change between minor versions.

### `StartDALChain(ctx, cfg) → (*DALChainProcess, error)`

**Purpose:** Launch a local DAL chain (sequencer + full node + execution services) from Go code. Wraps the `run-cosmos-wasm-nodes.go` script.

**Params: `DALChainConfig`**

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `ProjectRoot` | `string` | — | **Required.** Absolute path to ev-node repo root |
| `ChainName` | `string` | `"cosmos-wasm-local"` | Chain ID |
| `Namespace` | `string` | `"rollup"` | DA namespace |
| `DABridgeRPC` | `string` | — | **Required.** Celestia DA node RPC |
| `DAAuthToken` | `string` | `""` | Celestia auth token |
| `CleanOnStart` | `bool` | `true` | Wipe data on start |
| `CleanOnExit` | `bool` | `false` | Wipe data on exit |
| `LogLevel` | `string` | `"info"` | Log level |
| `BlockTime` | `time.Duration` | `2s` | Block production interval |
| `SubmitInterval` | `time.Duration` | `8s` | DA submission interval |

**Example:**

```go
cfg := cosmoswasm.DefaultDALChainConfig("/path/to/ev-node")
cfg.DABridgeRPC = "http://localhost:26658"
cfg.DAAuthToken = os.Getenv("DA_AUTH_TOKEN")

proc, err := cosmoswasm.StartDALChain(ctx, cfg)
if err != nil {
    log.Fatal(err)
}
defer proc.Stop()

// Chain is running — connect SDK client
client := cosmoswasm.NewClient(proc.Endpoints.SequencerExecAPI)
```

**`DALChainProcess`:**

| Field/Method | Description |
|-------------|-------------|
| `Endpoints.SequencerRPC` | Sequencer RPC URL (default `:38331`) |
| `Endpoints.FullNodeRPC` | Full node RPC URL (default `:48331`) |
| `Endpoints.SequencerExecAPI` | Sequencer execution API (default `:50051`) |
| `Endpoints.FullNodeExecAPI` | Full node execution API (default `:50052`) |
| `Stop()` | Kill the chain process |

### `DefaultDALChainConfig(projectRoot) → DALChainConfig`

Returns a config with sensible defaults. You must set `DABridgeRPC`.
