# Cosmos Explorer (reuse evnode RPC)

Tool này cung cấp:

- P1: Explorer API ổn định cho block/tx/search + DA endpoints có sẵn
- P2: Indexer DB + lệnh reindex theo height

## Chạy Explorer API

```bash
go run ./tools/cosmos-explorer serve --addr :8090
```

Auto-index nền (mặc định bật):

```bash
go run ./tools/cosmos-explorer serve --addr :8090 --auto-index=true --sync-interval 3s --max-blocks-per-tick 50
```

API:

- `GET /health`
- `GET /api/v1/status`
- `GET /api/v1/blocks/latest`
- `GET /api/v1/blocks/{height}`
- `GET /api/v1/txs/{hash}`
- `GET /api/v1/search?q=<height_or_tx_hash>`
- `GET /api/v1/indexer/state`
- `GET /api/v1/da/*` (proxy sang evnode DA visualization endpoints)

## Reindex DB

```bash
# từ height cụ thể đến latest
go run ./tools/cosmos-explorer reindex --from 1

# theo range
go run ./tools/cosmos-explorer reindex --from 100 --to 500

# resume từ latest_indexed_height+1
go run ./tools/cosmos-explorer reindex
```

DB mặc định: `.data/cosmos-explorer/index.db` (BoltDB)

Ghi chú:

- `serve` có thể chạy auto-index nền để tự bắt kịp chain.
- `reindex` vẫn hữu ích khi cần backfill một range lớn theo batch chủ động.

## Cấu hình RPC

Ưu tiên env:

1. `EVNODE_RPC_URL`
2. `WASM_RPC_URL`
3. `NODE`
4. mặc định `http://127.0.0.1:38331`
