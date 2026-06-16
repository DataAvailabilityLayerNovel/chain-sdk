# Configuration

## SDK Client Config (`SDKConfig`)

Dùng với `NewClientFromConfig()` cho client production.

| Field | Type | Bắt buộc | Default | Mô tả |
|-------|------|----------|---------|-------|
| `ExecURL` | `string` | Có | — | Base URL của service cosmos-exec-grpc |
| `Timeout` | `time.Duration` | Không | `20s` | Timeout HTTP mỗi request |
| `RetryAttempts` | `int` | Không | `0` | Số lần retry khi lỗi tạm thời (connection refused, timeout) |
| `RetryDelay` | `time.Duration` | Không | `1s` | Delay giữa các lần retry |
| `AuthToken` | `string` | Không | `""` | Gửi `Authorization: Bearer <token>` mỗi request |
| `ChainID` | `string` | Không | `""` | Chain id cho thao tác cần chain id (build tx ký) |
| `HTTPClient` | `*http.Client` | Không | auto | Custom HTTP client (TLS, proxy, tracing) |

### Client nhanh (Dev)

```go
// Không cần config — localhost:50051, no auth, no retry
client := cosmoswasm.NewClient("http://127.0.0.1:50051")
```

### Client production

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
    log.Fatal(err) // chỉ lỗi khi ExecURL rỗng
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

Executor (`cosmos-exec-grpc`) cấu hình qua CLI flag, environment variable, hoặc
profile. Code: [`apps/cosmos-exec/config/config.go`](../../../config/config.go)
(`config.ForProfile(profile)` + env override).

### CLI flags

| Flag | Mô tả |
|------|-------|
| `--profile` | `dev` (default) / `test` / `prod` |
| `--address` | Listen address (mặc định lấy từ profile) |
| `--home` | Home directory |
| `--in-memory` | Dùng DB in-memory (tránh file lock, không bền) |
| `--log-level` | `debug` / `info` / `error` |

Thứ tự ưu tiên: **CLI flag > env var > profile default.**

### Profiles

| Profile | `--profile` | Use case |
|---------|-------------|----------|
| **dev** | `dev` (default) | Dev local, default dễ dãi |
| **test** | `test` | CI/unit test, in-memory, port ngẫu nhiên |
| **prod** | `prod` | Production, bật persistence, yêu cầu auth |

### Toàn bộ field config (struct `config.Config`)

Mỗi field có 1 env var override tương ứng (`COSMOS_EXEC_*`).

| Field | Env Var | Dev Default | Prod Default | Mô tả |
|-------|---------|-------------|--------------|-------|
| `listen_addr` | `COSMOS_EXEC_LISTEN_ADDR` | `0.0.0.0:50051` | `0.0.0.0:50051` | Địa chỉ listen gRPC/HTTP |
| `home` | `COSMOS_EXEC_HOME` | `.cosmos-exec-grpc` | `.cosmos-exec-grpc` | Thư mục data |
| `in_memory` | `COSMOS_EXEC_IN_MEMORY` | `false` | `false` | DB in-memory (no disk) |
| `block_time` | `COSMOS_EXEC_BLOCK_TIME` | `2s` | `2s` | Chu kỳ sinh block |
| `query_gas_max` | `COSMOS_EXEC_QUERY_GAS_MAX` | `50,000,000` | `50,000,000` | Gas limit cho WASM query |
| `max_blob_size` | `COSMOS_EXEC_MAX_BLOB_SIZE` | `4 MB` | `4 MB` | Max size 1 blob (server-side) |
| `max_store_total_size` | `COSMOS_EXEC_MAX_STORE_SIZE` | `256 MB` | `1 GB` | Max tổng blob store |
| `persist_blobs` | `COSMOS_EXEC_PERSIST_BLOBS` | `false` | `true` | Lưu blob ra đĩa (JSONL) |
| `persist_tx_results` | `COSMOS_EXEC_PERSIST_TX_RESULTS` | `false` | `true` | Lưu tx result ra đĩa |
| `data_dir` | `COSMOS_EXEC_DATA_DIR` | `""` (auto: `$HOME/data`) | `""` (auto) | Override data dir |
| `log_level` | `COSMOS_EXEC_LOG_LEVEL` | `info` | `info` | `debug` / `info` / `error` |
| `auth_token` | `COSMOS_EXEC_AUTH_TOKEN` | `""` | *(phải set)* | Bearer token cho API auth |
| `cors_allow_origin` | `COSMOS_EXEC_CORS_ORIGIN` | `*` | `""` *(phải set)* | CORS allowed origin |
| `max_request_body_bytes` | `COSMOS_EXEC_MAX_BODY_BYTES` | `10 MB` | `10 MB` | Max request body |
| `rate_limit_rps` | `COSMOS_EXEC_RATE_LIMIT_RPS` | `0` (no limit) | `100` | Giới hạn request/giây |
| `read_only_mode` | `COSMOS_EXEC_READ_ONLY` | `false` | `false` | Từ chối mọi write |
| `metrics_enabled` | `COSMOS_EXEC_METRICS` | `false` | `true` | Bật Prometheus metrics |
| `metrics_addr` | `COSMOS_EXEC_METRICS_ADDR` | (theo profile) | (theo profile) | Địa chỉ scrape metrics |
| `read_timeout` | — | `30s` | `15s` | HTTP read timeout |
| `write_timeout` | — | `30s` | `15s` | HTTP write timeout |
| `idle_timeout` | — | `120s` | `60s` | HTTP idle timeout |

> Profile cũng chọn được qua env `COSMOS_EXEC_PROFILE`.

### Cấu hình theo môi trường (env)

```bash
# Dev (tối thiểu)
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

### Khuyến nghị theo môi trường

| Setting | Dev | Staging | Production |
|---------|-----|---------|------------|
| `SDKConfig.Timeout` | `10s` | `20s` | `30s` |
| `SDKConfig.RetryAttempts` | `0` | `2` | `3` |
| `SDKConfig.RetryDelay` | — | `1s` | `2s` |
| `SDKConfig.AuthToken` | rỗng | set | **bắt buộc** |
| Server `in_memory` | `true` OK | `false` | `false` |
| Server `persist_*` | optional | `true` | **bắt buộc** |
| Server `rate_limit_rps` | `0` | `50` | `100` |
| Server `cors_allow_origin` | `*` | domain cụ thể | domain cụ thể |
| Server `metrics_enabled` | optional | `true` | **bắt buộc** |

Faucet/treasury (chỉ bật khi set env) đọc thêm: `COSMOS_EXEC_TREASURY_PRIVKEY_HEX`,
`COSMOS_EXEC_TREASURY_AMOUNT`, `COSMOS_EXEC_FAUCET_AMOUNT`,
`COSMOS_EXEC_FAUCET_GAS`, `COSMOS_EXEC_FAUCET_COOLDOWN_SECONDS` — xem
[fee-economics.md](fee-economics.md).

## Cấu hình DA (blob-first)

> Refactor `ea844067` đã gỡ `DANamespaceConfig` + `DABridge`. Cấu hình DA giờ
> nằm trong **`BlobClientConfig`** (truyền cho `NewBlobClient`):

| Field | Bắt buộc | Mô tả |
|-------|----------|-------|
| `BridgeRPC` | Có | URL JSON-RPC Celestia bridge (vd `http://localhost:26658`) |
| `Namespace` | Có | Tên namespace app (map qua `NamespaceFromString`) |
| `AuthToken` | Không | Bearer token DA node |
| `GasPrice` | Không | Gas price DA (đặt > 0 để gửi giá tường minh) |

```go
bc, err := cosmoswasm.NewBlobClient(ctx, cosmoswasm.BlobClientConfig{
    BridgeRPC: "http://localhost:26658",
    Namespace: "my-game-chain",
    AuthToken: os.Getenv("DA_AUTH_TOKEN"),
})
```

> Gom nhiều blob để giảm chi phí on-chain: dùng `BlobClient.SubmitBatch` +
> `BuildBatchRootTx` (không còn `BatchBuilder`). Xem [api-reference.md](api-reference.md)
> và [blob-first.md](blob-first.md).
