# Go Cosmos WASM SDK

SDK `cosmoswasm` dùng để:

1. Start dApp chain Cosmos WASM trên DAL với `chain name` + `namespace` riêng.
2. Build và submit tx WASM (`store`, `instantiate`, `execute`).
3. Query tx result và query smart contract.

Package:

`github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm`

## 1) Cài đặt

```bash
go get github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm
```

## 2) Yêu cầu trước khi chạy

- Bạn đang ở repo `ev-node` (hoặc có `EVNODE_PROJECT_ROOT` trỏ đúng repo root).
- DA endpoint hợp lệ (`DA_BRIDGE_RPC`) và token (`DA_AUTH_TOKEN` nếu endpoint yêu cầu).
- Port local trống: `50051`, `50052`, `38331`, `48331`, `7860`, `7861`.

Dọn nhanh port trước khi chạy:

```bash
pkill -f cosmos-exec-grpc || true
pkill -f evcosmos || true
pkill -f run-cosmos-wasm-nodes.go || true
```

## 3) Danh sách API/lệnh trong SDK

### 3.1 Chain API (DAL dApp chain)

- `DefaultDALChainConfig(projectRoot)`
  - Tạo config mặc định để start chain.
- `StartDALChain(ctx, cfg)`
  - Start sequencer + fullnode + execution services qua runner.
  - Trả về `DALChainProcess` gồm endpoint runtime.
- `(*DALChainProcess).Stop()`
  - Stop process chain đã start.

`DALChainConfig` quan trọng:

- `ProjectRoot` → đường dẫn root repo `ev-node`
- `ChainName` → map vào `--chain-id`
- `Namespace` → map vào `DA_NAMESPACE`
- `DABridgeRPC` → map vào `DA_BRIDGE_RPC` + `DA_RPC`
- `DAAuthToken` → map vào `DA_AUTH_TOKEN`
- `CleanOnStart`, `CleanOnExit`, `LogLevel`, `BlockTime`, `SubmitInterval`

### 3.2 Contract API (WASM tx/query)

- Tx builders:
  - `BuildStoreTx(wasmBytes, sender)`
  - `BuildInstantiateTx(InstantiateTxRequest)`
  - `BuildExecuteTx(ExecuteTxRequest)`
- Submit/query:
  - `NewClient(execAPIURL)`
  - `SubmitTxBase64(ctx, txB64)`
  - `SubmitTxBytes(ctx, txBytes)`
  - `GetTxResult(ctx, txHash)`
  - `WaitTxResult(ctx, txHash, pollInterval)`
  - `QuerySmartRaw(ctx, contract, msg)`
  - `QuerySmart(ctx, contract, msg)`

### 3.3 Helpers

- `DefaultSender()`
- `EncodeTxBase64(tx)`
- `EncodeTxHex(tx)`

## 4) Quickstart chạy được ngay (copy-paste)

### 4.1 Start chain (không deploy contract)

File runnable:

- `sdk/cosmoswasm/examples/dapp-chain/main.go`

Lệnh chạy:

```bash
cd apps/cosmos-exec

export EVNODE_PROJECT_ROOT=/absolute/path/to/ev-node
export CHAIN_NAME=my-dapp-chain
export DA_NAMESPACE=my-dapp-namespace
export DA_BRIDGE_RPC=https://<celestia-bridge-rpc>
export DA_AUTH_TOKEN=<token>

go run ./sdk/cosmoswasm/examples/dapp-chain
```

Nếu bạn chạy từ trong repo `ev-node`, có thể bỏ `EVNODE_PROJECT_ROOT` (example tự dò root).

### 4.2 Start chain + deploy contract (1 lệnh)

File runnable:

- `sdk/cosmoswasm/examples/dapp-chain-deploy/main.go`

Lệnh chạy:

```bash
cd apps/cosmos-exec

export EVNODE_PROJECT_ROOT=/absolute/path/to/ev-node
export CHAIN_NAME=my-dapp-chain
export DA_NAMESPACE=my-dapp-namespace
export DA_BRIDGE_RPC=https://<celestia-bridge-rpc>
export DA_AUTH_TOKEN=<token>

# Optional
# export WASM_FILE=/absolute/path/to/contract.wasm
# export WASM_URL=https://.../contract.wasm
# export LABEL=my-contract-label
# export INIT_MSG='{"name":"Token","symbol":"TOK","decimals":6,...}'

go run ./sdk/cosmoswasm/examples/dapp-chain-deploy
```

Output kỳ vọng của deploy example:

- `store_tx_hash=...`
- `instantiate_tx_hash=...`
- `code_id=...`
- `contract_addr=...`

## 5) Ví dụ dùng SDK trong code app của bạn

```go
ctx := context.Background()

cfg := cosmoswasm.DefaultDALChainConfig("/absolute/path/to/ev-node")
cfg.ChainName = "my-dapp-chain"
cfg.Namespace = "my-dapp-namespace"
cfg.DABridgeRPC = "https://<celestia-bridge-rpc>"
cfg.DAAuthToken = "<token>"

proc, err := cosmoswasm.StartDALChain(ctx, cfg)
if err != nil {
	panic(err)
}
defer proc.Stop()

client := cosmoswasm.NewClient(proc.Endpoints.SequencerExecAPI)

execTx, err := cosmoswasm.BuildExecuteTx(cosmoswasm.ExecuteTxRequest{
	Sender:   cosmoswasm.DefaultSender(),
	Contract: "cosmos1...",
	Msg:      `{"transfer":{"recipient":"cosmos1...","amount":"1"}}`,
})
if err != nil {
	panic(err)
}

submit, err := client.SubmitTxBytes(ctx, execTx)
if err != nil {
	panic(err)
}

result, err := client.WaitTxResult(ctx, submit.Hash, time.Second)
if err != nil {
	panic(err)
}

_ = result
```

## 6) Health check / verify chain đang chạy

Sau khi start chain:

```bash
curl -sS http://127.0.0.1:38331/health/live
curl -sS http://127.0.0.1:48331/health/live
```

Xem latest block:

```bash
cd /absolute/path/to/ev-node
./scripts/contracts/wasm-rpc.sh latest-block
```

## 7) Troubleshooting nhanh

- `required port ... already in use`
  - Dọn process cũ bằng 3 lệnh `pkill` ở phần Yêu cầu.
- `DA_BRIDGE_RPC is required`
  - Chưa set env `DA_BRIDGE_RPC`.
- `DA preflight unauthorized (401)` hoặc permission error
  - Token sai/hết hạn/thiếu quyền đọc blob.
- Chain chạy nhưng fullnode có warn `height is equal to 0`
  - Đây là cảnh báo DA follower có thể xuất hiện theo điều kiện sync; kiểm tra thêm log `da_submitter` và `da_height` ở sequencer.

## 8) Test SDK

```bash
cd apps/cosmos-exec
go test ./sdk/cosmoswasm/...
```
