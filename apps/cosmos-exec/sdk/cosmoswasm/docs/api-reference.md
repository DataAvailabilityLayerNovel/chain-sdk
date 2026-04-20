# API Reference

All methods below are public stable API. Breaking changes require a major version bump.

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

### DABridge

Combines `DAClient` + `ExecutorClient` under a namespace:

| Method | Description |
|--------|-------------|
| `Submit(ctx, blobs, opts)` | Submit blobs to DA layer under namespace |
| `GetBlobs(ctx, height)` | Retrieve blobs at DA height |
| `Watch(ctx)` | Subscribe to new blobs (WebSocket) |
| `PollBlobs(ctx, startHeight, interval, handler)` | HTTP polling fallback |
| `SubmitAndCommit(ctx, req)` | DA submit + on-chain commit in one call |
| `DAHeight(ctx)` | Latest DA layer height |

---

## Testing Mocks

### `NewMockClient() → *MockExecutorClient`

In-memory executor mock. Implements `ExecutorClient`.

```go
mock := cosmoswasm.NewMockClient()

// Custom query behavior
mock.OnQuery(func(contract string, msg any) (*cosmoswasm.QuerySmartResponse, error) {
    return &cosmoswasm.QuerySmartResponse{Data: map[string]any{"balance": "1000"}}, nil
})

// Pre-populate tx results
mock.SetTxResult("abc123", &cosmoswasm.TxExecutionResult{Code: 0, Height: 5})
```

### `NewMockDAClient() → *MockDAClient`

In-memory DA mock. Implements `DAClient`. Namespace-isolated, supports subscribe notifications.
