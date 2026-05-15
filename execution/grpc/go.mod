module github.com/DataAvailabilityLayerNovel/chain-sdk/execution/grpc

go 1.25.6

replace (
	github.com/DataAvailabilityLayerNovel/chain-sdk => ../../
	github.com/DataAvailabilityLayerNovel/chain-sdk/core => ../../core
)

require (
	connectrpc.com/connect v1.19.2
	connectrpc.com/grpcreflect v1.3.0
	github.com/DataAvailabilityLayerNovel/chain-sdk v1.0.0
	github.com/DataAvailabilityLayerNovel/chain-sdk/core v1.0.0
	golang.org/x/net v0.54.0
	google.golang.org/protobuf v1.36.11
)

require golang.org/x/text v0.37.0 // indirect
