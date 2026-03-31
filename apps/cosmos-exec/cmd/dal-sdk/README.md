# DAL SDK CLI

CLI để chạy DAL Cosmos WASM chain và thao tác tx/contract không cần viết Go code.

## Build

```bash
cd apps/cosmos-exec
go build -o dal-sdk ./cmd/dal-sdk
```

## Start chain

```bash
./dal-sdk chain start \
  --name mycosmos \
  --namespace rollup \
  --da-rpc http://127.0.0.1:26758 \
  --project-root /absolute/path/to/ev-node \
  --clean
```

Nếu không truyền `--project-root`, CLI sẽ thử tự detect từ thư mục hiện tại hoặc dùng `EVNODE_PROJECT_ROOT`.

## Contract commands

### Deploy nhanh CW20 (recommended)

```bash
./dal-sdk contract deploy-cw20 \
  --wasm ./cw20_base.wasm \
  --name Token \
  --symbol TOK \
  --supply 1000000 \
  --rpc http://127.0.0.1:50051
```

### Deploy generic (store + instantiate)

```bash
./dal-sdk contract deploy \
  --wasm ./contract.wasm \
  --init-msg '{"count":0}' \
  --rpc http://127.0.0.1:50051
```

### Store / Instantiate / Execute / Query

```bash
./dal-sdk contract store --wasm ./contract.wasm --rpc http://127.0.0.1:50051
./dal-sdk contract instantiate --code-id 1 --init-msg '{"count":0}' --rpc http://127.0.0.1:50051
./dal-sdk contract execute --contract cosmos1... --msg '{"increment":{}}' --rpc http://127.0.0.1:50051
./dal-sdk contract query --contract cosmos1... --msg '{"get_count":{}}' --rpc http://127.0.0.1:50051
```

### Check balance + transfer (CW20)

```bash
./dal-sdk contract balance --contract cosmos1... --address cosmos1... --rpc http://127.0.0.1:50051
./dal-sdk contract transfer --contract cosmos1... --to cosmos1... --amount 10 --rpc http://127.0.0.1:50051
```

## TX commands

### Submit tx

```bash
./dal-sdk tx submit --tx-base64 "<tx_base64>" --rpc http://127.0.0.1:50051 --wait
./dal-sdk tx submit --tx-hex "<tx_hex>" --rpc http://127.0.0.1:50051
./dal-sdk tx submit --tx-file ./tx.bin --rpc http://127.0.0.1:50051
```

### Query tx result

```bash
./dal-sdk tx result --hash <tx_hash> --rpc http://127.0.0.1:50051
```

## Bank commands (native operations)

### Send native tokens

```bash
./dal-sdk bank send \
  --to cosmos1... \
  --amount 1000stake \
  --rpc http://127.0.0.1:50051 \
  --wait
```

### Check native balance

```bash
./dal-sdk bank balance \
  --address cosmos1... \
  --rest http://127.0.0.1:38331
```

## Full command groups

- `dal-sdk chain start`
- `dal-sdk tx submit|result`
- `dal-sdk contract store|instantiate|execute|query|balance|transfer|deploy|deploy-cw20`
- `dal-sdk bank send|balance`

## SDK docs

Xem thêm SDK Go: [../../sdk/cosmoswasm/README.md](../../sdk/cosmoswasm/README.md)
