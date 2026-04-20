# Configuration

## SDK Client Config (`SDKConfig`)

Used with `NewClientFromConfig()` for production clients.

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `ExecURL` | `string` | Yes | — | Base URL of the cosmos-exec-grpc service |
| `Timeout` | `time.Duration` | No | `20s` | HTTP request timeout |
| `RetryAttempts` | `int` | No | `0` | Retry count for transient errors (connection refused, timeout) |
| `RetryDelay` | `time.Duration` | No | `1s` | Delay between retries |
| `AuthToken` | `string` | No | `""` | Sent as `Authorization: Bearer <token>` on every request |
| `ChainID` | `string` | No | `""` | Chain identifier for chain-aware operations |
| `HTTPClient` | `*http.Client` | No | auto | Custom HTTP client (TLS, proxy, tracing) |

### Quick Client (Dev)

```go
// No config needed — localhost:50051, no auth, no retry
client := cosmoswasm.NewClient("http://127.0.0.1:50051")
```

### Production Client

```go
client, err := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
    ExecURL:       "https://exec.mychain.io",
    Timeout:       30 * time.Second,
    RetryAttempts: 3,
    RetryDelay:    2 * time.Second,
    AuthToken:     os.Getenv("EXEC_AUTH_TOKEN"),
    ChainID:       "my-chain-1",
})
if err != nil {
    log.Fatal(err) // only fails if ExecURL is empty
}
```

### Custom HTTP Client

```go
client, _ := cosmoswasm.NewClientFromConfig(cosmoswasm.SDKConfig{
    ExecURL: "https://exec.internal:50051",
    HTTPClient: &http.Client{
        Timeout: 60 * time.Second,
        Transport: &http.Transport{
            TLSClientConfig: &tls.Config{RootCAs: certPool},
            MaxIdleConns:    10,
        },
    },
})
```

## Executor Server Config

The executor (`cosmos-exec-grpc`) is configured via CLI flags, environment variables, or profiles.

### Profiles

| Profile | `--profile` | Use case |
|---------|-------------|----------|
| **dev** | `dev` (default) | Local development, permissive defaults |
| **test** | `test` | CI/unit tests, in-memory, random port |
| **prod** | `prod` | Production, persistence on, auth required |

### All Config Fields

| Field | Env Var | Dev Default | Prod Default | Description |
|-------|---------|-------------|--------------|-------------|
| `listen_addr` | `COSMOS_EXEC_LISTEN_ADDR` | `0.0.0.0:50051` | `0.0.0.0:50051` | gRPC/HTTP listen address |
| `home` | `COSMOS_EXEC_HOME` | `.cosmos-exec-grpc` | `.cosmos-exec-grpc` | Home directory for data |
| `in_memory` | `COSMOS_EXEC_IN_MEMORY` | `false` | `false` | Use in-memory DB (no disk) |
| `block_time` | `COSMOS_EXEC_BLOCK_TIME` | `2s` | `2s` | Block production interval |
| `query_gas_max` | `COSMOS_EXEC_QUERY_GAS_MAX` | `50,000,000` | `50,000,000` | Gas limit for WASM queries |
| `max_blob_size` | `COSMOS_EXEC_MAX_BLOB_SIZE` | `4 MB` | `4 MB` | Max single blob size |
| `max_store_total_size` | `COSMOS_EXEC_MAX_STORE_SIZE` | `256 MB` | `1 GB` | Max total blob store size |
| `persist_blobs` | `COSMOS_EXEC_PERSIST_BLOBS` | `false` | `true` | Persist blobs to disk (JSONL) |
| `persist_tx_results` | `COSMOS_EXEC_PERSIST_TX_RESULTS` | `false` | `true` | Persist tx results to disk |
| `data_dir` | `COSMOS_EXEC_DATA_DIR` | `""` (auto: `$HOME/data`) | `""` (auto) | Override data directory |
| `log_level` | `COSMOS_EXEC_LOG_LEVEL` | `info` | `info` | Log level: debug, info, error |
| `auth_token` | `COSMOS_EXEC_AUTH_TOKEN` | `""` | *(must set)* | Bearer token for API auth |
| `cors_allow_origin` | `COSMOS_EXEC_CORS_ORIGIN` | `*` | `""` *(must set)* | CORS allowed origin |
| `max_request_body_bytes` | `COSMOS_EXEC_MAX_BODY_BYTES` | `10 MB` | `10 MB` | Max request body size |
| `rate_limit_rps` | `COSMOS_EXEC_RATE_LIMIT_RPS` | `0` (no limit) | `100` | Requests per second limit |
| `read_only_mode` | `COSMOS_EXEC_READ_ONLY` | `false` | `false` | Reject write operations |
| `metrics_enabled` | `COSMOS_EXEC_METRICS` | `false` | `true` | Enable Prometheus metrics |
| `read_timeout` | — | `30s` | `15s` | HTTP read timeout |
| `write_timeout` | — | `30s` | `15s` | HTTP write timeout |
| `idle_timeout` | — | `120s` | `60s` | HTTP idle timeout |

### Environment-Based Config

```bash
# Dev (minimal)
go run ./cmd/cosmos-exec-grpc --in-memory

# Staging
export COSMOS_EXEC_LISTEN_ADDR=0.0.0.0:50051
export COSMOS_EXEC_PERSIST_BLOBS=true
export COSMOS_EXEC_PERSIST_TX_RESULTS=true
export COSMOS_EXEC_AUTH_TOKEN=staging-token-xyz
export COSMOS_EXEC_RATE_LIMIT_RPS=50
go run ./cmd/cosmos-exec-grpc --profile dev

# Production
export COSMOS_EXEC_AUTH_TOKEN=prod-secret-token
export COSMOS_EXEC_CORS_ORIGIN=https://app.mychain.io
export COSMOS_EXEC_RATE_LIMIT_RPS=100
export COSMOS_EXEC_METRICS=true
go run ./cmd/cosmos-exec-grpc --profile prod --home /data/cosmos-exec
```

## Config Recommendations by Environment

| Setting | Dev | Staging | Production |
|---------|-----|---------|------------|
| `SDKConfig.Timeout` | `10s` | `20s` | `30s` |
| `SDKConfig.RetryAttempts` | `0` | `2` | `3` |
| `SDKConfig.RetryDelay` | — | `1s` | `2s` |
| `SDKConfig.AuthToken` | empty | set | **required** |
| Server `in_memory` | `true` OK | `false` | `false` |
| Server `persist_*` | optional | `true` | **required** |
| Server `rate_limit_rps` | `0` | `50` | `100` |
| Server `cors_allow_origin` | `*` | specific domain | specific domain |
| Server `metrics_enabled` | optional | `true` | **required** |

## DA Namespace Config (`DANamespaceConfig`)

For Celestia DA operations via `DABridge`:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `Namespace` | `*Namespace` | Yes | App-chain's dedicated namespace |
| `DANodeAddr` | `string` | Yes | Celestia node RPC address (e.g. `http://localhost:26658`) |
| `AuthToken` | `string` | No | Bearer token for Celestia node auth |
| `SubmitOptions` | `*DASubmitOptions` | No | Default gas price/limit for submissions |

```go
daCfg := &cosmoswasm.DANamespaceConfig{
    Namespace:  cosmoswasm.NamespaceFromString("my-game-chain"),
    DANodeAddr: "http://localhost:26658",
    AuthToken:  os.Getenv("DA_AUTH_TOKEN"),
}
if err := daCfg.Validate(); err != nil {
    log.Fatal(err)
}
```

## BatchBuilder Config

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `Contract` | `string` | — | WASM contract address for recording batch roots |
| `Sender` | `string` | `DefaultSender()` | Tx sender address |
| `Tag` | `string` | `""` | Application label on each batch root |
| `MaxBytes` | `int` | `3 MB` | Size threshold for auto-flush |
| `Extra` | `map[string]any` | `nil` | Extra fields in the on-chain root message |
