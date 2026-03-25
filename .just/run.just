# Run 'n' nodes (default: 1). Usage: just run-n 3
[group('run')]
run-n nodes="1": build build-da
    @go run -tags=run scripts/run.go --nodes={{ nodes }}

# Run EVM nodes (one sequencer and one full node)
[group('run')]
run-evm-nodes nodes="1": build-da build-evm
    @echo "Starting EVM nodes..."
    @go run -tags=run_evm scripts/run-evm-nodes.go --nodes={{ nodes }}

# Run Cosmos/WASM sequencer + full node with local-da and cosmos-exec gRPC
[group('run')]
run-cosmos-wasm-nodes: build-cosmos-wasm build-cosmos-exec-grpc
    @echo "Starting Cosmos/WASM nodes..."
    @go run -tags=run_cosmos_wasm scripts/run-cosmos-wasm-nodes.go
