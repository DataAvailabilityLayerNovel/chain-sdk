module github.com/evstack/ev-node/apps/cosmos-wasm

go 1.25.6

replace (
	github.com/evstack/ev-node => ../../
	github.com/evstack/ev-node/execution/grpc => ../../execution/grpc
)

require (
	github.com/evstack/ev-node v1.0.0
	github.com/evstack/ev-node/core v1.0.0
	github.com/evstack/ev-node/execution/grpc v1.0.0-rc.1
	github.com/ipfs/go-datastore v0.9.1
	github.com/rs/zerolog v1.34.0
	github.com/spf13/cobra v1.10.2
)
