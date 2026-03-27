# cosmos-exec

`cosmos-exec` currently provides two entrypoints:

- `cmd/cosmos-exec`: standalone ABCI socket server.
- `cmd/cosmos-exec-grpc`: gRPC execution service compatible with `core/execution.Executor` via `execution/grpc`.

## Run gRPC executor service (MVP)

```bash
cd apps/cosmos-exec
go run ./cmd/cosmos-exec-grpc --address 0.0.0.0:50051 --in-memory
```

## Notes

- Current app wiring in `app/app.go` includes `auth`, `bank`, `x/params`, `x/capability`, IBC core, IBC transfer, and `x/wasm` (`WasmKeeper` + `wasm AppModule`).
- `WasmKeeper` is wired to real IBC keepers (`IBCKeeper.ChannelKeeper`, `IBCKeeper.PortKeeper`, scoped capabilities, `TransferKeeper`).
- This is a bridge skeleton to unblock `apps/cosmos-wasm` full-node flow.
- Full production parity still requires wiring real staking/distribution keepers for wasm query plugin behavior and e2e verification of contract lifecycle.

## Go SDK for dApp users

Public Go SDK cho Cosmos WASM nằm tại [sdk/cosmoswasm](sdk/cosmoswasm/README.md).

SDK hỗ trợ:

- Build raw tx (`store`, `instantiate`, `execute`)
- Submit tx (`/tx/submit`)
- Query tx result (`/tx/result`)
- Query smart contract (`/wasm/query-smart`)
