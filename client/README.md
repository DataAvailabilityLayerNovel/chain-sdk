# Evolve Client Libraries

This directory contains client libraries for interacting with Evolve nodes in various programming languages.

## Structure

```txt
client/
├── crates/           # Rust client libraries
│   ├── types/    # Generated protobuf types for Rust
│   └── client/   # High-level Rust client for gRPC services
└── README.md
```

## Rust Client

The Rust client consists of two crates:

### ev-types

Contains all the protobuf-generated types and service definitions. This crate is automatically generated from the proto files in `/proto/evnode/v1/`.

### ev-client

A high-level client library that provides:

- Easy-to-use wrappers around the gRPC services
- Connection management with configurable timeouts
- Type-safe interfaces
- Comprehensive error handling
- Example usage code

See the [ev-client README](crates/client/README.md) for detailed usage instructions.

## Future Client Libraries

This directory is structured to support additional client libraries in the future:

- JavaScript/TypeScript client
- Python client
- More Go clients

## Go Cosmos WASM SDK

Go SDK cho Cosmos WASM hiện có tại [../apps/cosmos-exec/sdk/cosmoswasm](../apps/cosmos-exec/sdk/cosmoswasm/README.md).

SDK hiện hỗ trợ:

- Build raw tx (`store`, `instantiate`, `execute`)
- Submit tx qua `cosmos-exec-grpc`
- Wait tx result theo hash
- Query smart contract (`/wasm/query-smart`)

Each language will have its own subdirectory with generated types and high-level client implementations.
