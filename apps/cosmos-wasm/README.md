# evcosmos

Full node binary for Cosmos/WASM rollup on ev-node (sequencer, P2P, DA sync, block production).

See [apps/cosmos-exec/README.md](../cosmos-exec/README.md) for full documentation including E2E setup, API reference, and examples.

## Quick start

```bash
go build -o evcosmos
./evcosmos init --root-dir ~/.evcosmos --chain-id cosmos-wasm-test-chain
./evcosmos start \
  --root-dir ~/.evcosmos \
  --grpc-executor-url http://localhost:50051 \
  --da.address http://localhost:7980
```
