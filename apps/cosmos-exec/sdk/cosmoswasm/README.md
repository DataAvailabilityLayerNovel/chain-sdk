# cosmoswasm — Go SDK for Modular Rollup on Celestia

Build CosmWasm app-chains on ev-node with Celestia DA. Submit transactions, store blobs off-chain, query smart contracts, and manage namespace-isolated data — all from Go.

## Quick Start

### Step 1 — Install SDK

```bash
go get github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm
```

Requires Go 1.25.6+. Module path:

```
github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm
```

### Step 2 — Start the chain

Chuẩn bị file `.env` ở project root:

```bash
DA_BRIDGE_RPC=http://localhost:26658
DA_AUTH_TOKEN=<celestia-auth-token>
DA_NAMESPACE=rollup
```

Start full stack (sequencer + full node + execution services):

```bash
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go \
  --clean-on-start=true \
  --block-time=2s
```

Chờ log:

```
Cosmos/WASM stack is running
- sequencer execution gRPC: http://127.0.0.1:50051
- full execution gRPC: http://127.0.0.1:50052
```

Kiểm tra node:

```bash
curl -s http://127.0.0.1:50051/status | python3 -m json.tool
# initialized: true, healthy: true, latest_height tăng dần
```

### Step 3 — Write your first app

Tạo file `main.go`:

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
)

func main() {
    // Connect to execution service
    client, err := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
        ExecURL:       "http://127.0.0.1:50051",
        Timeout:       20 * time.Second,
        RetryAttempts: 3,
        RetryDelay:    1 * time.Second,
    })
    if err != nil {
        log.Fatal(err)
    }
    ctx := context.Background()

    // 1. Store a blob off-chain
    res, err := client.SubmitBlob(ctx, []byte(`{"event":"player_moved","x":10,"y":20}`))
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("commitment:", res.Commitment)

    // 2. Retrieve it
    data, err := client.RetrieveBlobData(ctx, res.Commitment)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("data:", string(data))

    // 3. Batch upload + Merkle proof
    batch, err := client.SubmitBatch(ctx, [][]byte{
        []byte(`{"move":"e2e4"}`),
        []byte(`{"move":"e7e5"}`),
    })
    if err != nil {
        log.Fatal(err)
    }
    proof, _ := cosmoswasm.GetProof(batch.Commitments, 0)
    _ = cosmoswasm.VerifyMerkleProof(proof)
    fmt.Println("batch root:", batch.Root)
    fmt.Println("proof verified")

    // 4. Deploy contract + submit tx (requires E2E stack)
    wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d} // your .wasm file
    tx, _ := cosmoswasm.BuildStoreTx(wasmBytes, "")
    submitRes, err := client.SubmitTxBytes(ctx, tx)
    if err != nil {
        log.Fatal(err)
    }

    // Wait for tx execution
    result, err := client.WaitTxResult(ctx, submitRes.Hash, time.Second)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("tx executed at height %d, code=%d\n", result.Height, result.Code)
}
```

### Step 4 — Run

```bash
go run main.go
```

## What This SDK Does

| Capability | Description |
|------------|-------------|
| **Transaction** | Build, submit, and poll CosmWasm transactions (store code, instantiate, execute) |
| **Blob storage** | Store large data off-chain, get a SHA-256 commitment to record on-chain (32 bytes) |
| **Batch + Merkle** | Upload N blobs, get one Merkle root; prove any blob's inclusion offline |
| **Smart query** | Read contract state without submitting a transaction |
| **DA namespace** | Isolate your app-chain's blobs on Celestia via dedicated namespace |
| **Cost estimation** | Compare gas: direct on-chain embedding vs blob-first pattern |

## Examples

Runnable examples in [`examples/`](examples/):

| Example | What it shows |
|---------|--------------|
| [`quickstart`](examples/quickstart/main.go) | Blobs, proofs, cost estimation, compression |
| [`deploy-contract`](examples/deploy-contract/main.go) | Full contract lifecycle: store, instantiate, execute, query |
| [`contract-interaction`](examples/contract-interaction/main.go) | Hackatom + reflect, sub-messages, blob roots on-chain |
| [`game-telemetry`](examples/game-telemetry/main.go) | Batch submit, chunking, compression, cost comparison |
| [`dapp-chain`](examples/dapp-chain/main.go) | Start full DAL chain with DA + sequencer |
| [`dapp-chain-deploy`](examples/dapp-chain-deploy/main.go) | Start chain + auto-deploy contract |

Run any example (with E2E stack running):

```bash
cd apps/cosmos-exec
go run ./sdk/cosmoswasm/examples/quickstart
go run ./sdk/cosmoswasm/examples/deploy-contract
```

## Swagger API Docs

The executor has built-in Swagger UI:

```
http://127.0.0.1:50051/swagger        # Interactive UI
http://127.0.0.1:50051/swagger.json   # OpenAPI 3.0.3 spec (Postman/Insomnia import)
```

Quick curl check:

```bash
curl http://127.0.0.1:50051/status
curl -X POST http://127.0.0.1:50051/blob/submit \
  -H 'Content-Type: application/json' \
  -d '{"data_base64":"aGVsbG8gd29ybGQ="}'
```

## Documentation

| Guide | What's inside |
|-------|---------------|
| [Configuration](docs/configuration.md) | All SDK + server config fields, env vars, dev/staging/prod profiles |
| [API Reference](docs/api-reference.md) | Every public method: params, response, errors, example code |
| [Error Handling](docs/error-handling.md) | Error classification, retry policy, mapping errors to app actions |
| [Production Guide](docs/production-guide.md) | Timeout/retry tuning, auth, rate limiting, idempotency, SLOs |
| [Troubleshooting](docs/troubleshooting.md) | Common failures, diagnostic curl commands, debug checklist |
| [Migration Guide](docs/migration.md) | v0.2→v0.3 changes, internal separation, v1.0 plan |

## Testing

```bash
cd apps/cosmos-exec
go test ./sdk/cosmoswasm/...
```

Use mocks for unit tests (no running chain needed):

```go
mock := cosmoswasm.NewMockClient()
res, _ := mock.SubmitBlob(ctx, []byte("test"))
data, _ := mock.RetrieveBlobData(ctx, res.Commitment)

daMock := cosmoswasm.NewMockDAClient()
bridge := cosmoswasm.NewDABridge(daMock, mock, cosmoswasm.NamespaceFromString("test"))
```
