# cosmoswasm — Go SDK for Modular Rollup on Celestia

Build CosmWasm app-chains on ev-node with Celestia DA. Submit transactions, store blobs off-chain, query smart contracts, and manage namespace-isolated data — all from Go.

```
go get github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm
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

## Quick Start (5 minutes)

**Step 1** — Start the executor:

```bash
cd apps/cosmos-exec
go run ./cmd/cosmos-exec-grpc --in-memory
```

**Step 2** — Run this code (or copy into your `main.go`):

```go
package main

import (
    "context"
    "fmt"
    "log"

    cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
)

func main() {
    client := cosmoswasm.NewClient("http://127.0.0.1:50051")
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
    batch, _ := client.SubmitBatch(ctx, [][]byte{
        []byte(`{"move":"e2e4"}`),
        []byte(`{"move":"e7e5"}`),
    })
    proof, _ := cosmoswasm.GetProof(batch.Commitments, 0)
    _ = cosmoswasm.VerifyMerkleProof(proof)
    fmt.Println("batch root:", batch.Root)
    fmt.Println("proof verified")
}
```

**Step 3** — Run:

```bash
go run main.go
```

More runnable examples in [`examples/`](examples/):

| Example | What it shows | Requires |
|---------|--------------|----------|
| [`quickstart`](examples/quickstart/main.go) | Blobs, proofs, cost estimation, compression | Executor `--in-memory` |
| [`deploy-contract`](examples/deploy-contract/main.go) | Full contract lifecycle: store, instantiate, execute, query | Executor or E2E stack |
| [`contract-interaction`](examples/contract-interaction/main.go) | Hackatom + reflect, sub-messages, blob roots on-chain | Full E2E stack |
| [`game-telemetry`](examples/game-telemetry/main.go) | Batch submit, chunking, compression, cost comparison | Executor `--in-memory` |
| [`dapp-chain`](examples/dapp-chain/main.go) | Start full DAL chain with DA + sequencer | DA bridge endpoint |
| [`dapp-chain-deploy`](examples/dapp-chain-deploy/main.go) | Start chain + auto-deploy contract | DA bridge endpoint |

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

## Testing

```bash
cd apps/cosmos-exec
go test ./sdk/cosmoswasm/...
```

Use mocks for unit tests (no running executor needed):

```go
mock := cosmoswasm.NewMockClient()
res, _ := mock.SubmitBlob(ctx, []byte("test"))
data, _ := mock.RetrieveBlobData(ctx, res.Commitment)

daMock := cosmoswasm.NewMockDAClient()
bridge := cosmoswasm.NewDABridge(daMock, mock, cosmoswasm.NamespaceFromString("test"))
```
