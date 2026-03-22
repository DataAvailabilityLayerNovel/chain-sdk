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

## Next work items

1. Implement Cosmos execution service exposing gRPC executor API.
2. Extend `apps/cosmos-exec` with CosmWasm (`x/wasm`) runtime.
3. Add end-to-end scripts to run:
   - execution service
   - aggregator node
   - full sync node
