# Cosmos/WASM Full Node App (Scaffold)

`apps/cosmos-wasm` is a Phase-1 scaffold for running `ev-node` full-node architecture with a Cosmos/WASM execution backend.

## Goal

Unify architecture so Cosmos execution and WASM contracts run through the same `ev-node` pipeline:

- Sequencer (`single` or `based`)
- Executor / block production
- Syncer (DA + P2P)
- Submitter (DA submission + inclusion/finality)
- RPC service

## Current status

This scaffold currently:

- Provides `init` and `start` commands similar to `apps/grpc`
- Wires `node.NewNode(...)` with:
  - gRPC execution client (`execution/grpc`)
  - `single` / `based` sequencer
  - DA client

It expects an external execution service implementing the Evolve gRPC executor interface (can be backed by Cosmos SDK + CosmWasm runtime).

## Commands

Build:

```bash
cd apps/cosmos-wasm
go build -o evcosmos
```

Init config:

```bash
./evcosmos init --root-dir ~/.evcosmos --chain-id cosmos-wasm-test-chain
```

Start node:

```bash
./evcosmos start \
  --root-dir ~/.evcosmos \
  --grpc-executor-url http://localhost:50051 \
  --da.address http://localhost:7980
```

## Run real stack (sequencer + full node + Celestia DA)

From repository root:

```bash
just run-cosmos-wasm-nodes
```

This runs a real end-to-end stack:

- `cosmos-exec-grpc` for sequencer execution
- `cosmos-exec-grpc` for full-node execution
- `evcosmos` sequencer node (aggregator)
- `evcosmos` full node (syncing from sequencer)

Default endpoints:

- Sequencer RPC: `http://127.0.0.1:38331`
- Full node RPC: `http://127.0.0.1:48331`
- Node DA endpoint: from `.env` (`DA_BRIDGE_RPC` or `DA_RPC`)

DA upload sidecar (`tools/cosmos-da-submit`) is controlled by `COSMOS_DA_UPLOAD_MODE`:

- `engram` (default): submit to `COSMOS_DA_SUBMIT_API`
- `celestia`: submit directly via Celestia JSON-RPC (`DA_BRIDGE_RPC`/`DA_RPC`)

The runner waits for RPC readiness, verifies sync window between sequencer and full node, and streams logs so DA submission events are visible.

## Next work items

1. Extend e2e assertions to automatically check DA blobs by namespace.
2. Expand contract-level tx tests through the gRPC executor path under dual-node topology.
