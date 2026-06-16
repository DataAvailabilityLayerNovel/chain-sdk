---
name: cosmoswasm
description: >-
  Build a CosmWasm sovereign rollup on ev-node with Celestia DA, exposed through
  one Go SDK. Use when building or extending the cosmoswasm SDK, the executor,
  the Cosmos app, or the HTTP server.
---

# cosmoswasm — what I want to build

## The goal

I want to build a **sovereign rollup on ev-node that runs CosmWasm smart
contracts**, with **Celestia as the data availability layer**, and wrap the
whole thing behind **one Go SDK** so a developer only has to:

```go
import cosmoswasm ".../apps/cosmos-exec/sdk/cosmoswasm"
```

…and then call simple methods (`SubmitBlob`, `CommitRoot`, `QuerySmart`,
`BuildStoreTx` / `BuildInstantiateTx` / `BuildExecuteTx`) instead of hand-wiring
Cosmos SDK modules, the Wasm VM, DA plumbing, Merkle proofs, and protobuf
encoding themselves.

## How the pieces fit

```
User app (Go)      →  import cosmoswasm SDK
   │ HTTP
cosmos-exec-grpc   →  HTTP API server (cmd/)
   │
executor           →  Wasm runtime + blob store + state (executor/)
   │
Cosmos SDK app     →  modules: auth, bank, wasm, IBC, params (app/)
   │
ev-node (evcosmos) →  sequencer, P2P, block production, DA sync
   │
Celestia DA        →  off-chain blob storage
```

The **SDK is the product**; the executor, server, and app exist to make that one
import work. CosmWasm gives us smart contracts; ev-node gives us the rollup
node; Celestia keeps big data off-chain and cheap.

## Run it locally

```bash
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go \
  --clean-on-start=true --block-time=2s
# wait for "Cosmos/WASM stack is running"; SDK targets http://127.0.0.1:50051
```

## A few rules to keep

- One import: users only ever touch the top-level `cosmoswasm` package.
- Keep real logic in `internal/`; public files stay thin wrappers over it.
- Errors are `SDKError{Op, Cause, Hint}` — the hint tells the user what to do.
- Tests use the mocks (`NewMockClient`, `NewMockDAClient`) — no live chain.

## TODO

- [ ] **Cosmos app** — wire modules (auth, bank, wasm, IBC, params) in `app/`
- [ ] **Executor** — Wasm runtime + blob store + persistent state in `executor/`
- [ ] **HTTP server** — `cmd/cosmos-exec-grpc/` with Swagger + auth/CORS/rate-limit
- [ ] **SDK client** — `NewClientFromConfig`, `SubmitBlob`, `QuerySmart`
- [ ] **Tx builders** — `BuildStoreTx`, `BuildInstantiateTx`, `BuildExecuteTx`
- [ ] **DA layer** — namespace, blob submit/retrieve, `DABridge` to Celestia
- [ ] **Data integrity** — Merkle proof (`GetProof`/`VerifyMerkleProof`), chunk, compress
- [ ] **Blob-first commit** — `SubmitBatch` → Merkle root → `CommitRoot` on-chain
- [ ] **Cost estimation** — `EstimateCost`: direct on-chain vs blob-first
- [ ] **Mocks + tests** — mock executor/DA, unit tests with no running chain
- [ ] **Examples** — quickstart, deploy-contract, dapp-chain
- [ ] **Docs** — README, getting-started, architecture, api-reference
- [ ] **Demo apps** — frontend, faucet, block/tx explorer (proof, not core)
