# Go Cosmos WASM SDK

SDK `cosmoswasm` dùng để:

1. Start dApp chain Cosmos WASM trên DAL với `chain name` + `namespace` riêng.
2. Build và submit tx WASM (`store`, `instantiate`, `execute`).
3. Query tx result và query smart contract.

Package:

`github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm`

---

### ⚡ Quick Note: CLI Alternative

Không muốn viết Go code? Sử dụng **CLI tool** thay vì SDK programmatically:

```bash
cd apps/cosmos-exec
go build -o dal-sdk ./cmd/dal-sdk

# Start chain
./dal-sdk chain start --name mychain --namespace myns --da-rpc <url> --project-root /path/to/ev-node

# Deploy contract (store + instantiate)
./dal-sdk contract deploy --wasm ./contract.wasm --init-msg '{"count":0}' --rpc http://127.0.0.1:50051

# Execute/query/check balance (CW20)
./dal-sdk contract execute --contract cosmos1... --msg '{"transfer":{"recipient":"cosmos1...","amount":"10"}}' --rpc http://127.0.0.1:50051
./dal-sdk contract query --contract cosmos1... --msg '{"token_info":{}}' --rpc http://127.0.0.1:50051
./dal-sdk contract balance --contract cosmos1... --address cosmos1... --rpc http://127.0.0.1:50051

# Submit signed tx and check result
./dal-sdk tx submit --tx-base64 "<tx_base64>" --rpc http://127.0.0.1:50051 --wait
./dal-sdk tx result --hash <tx_hash> --rpc http://127.0.0.1:50051
```

---

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

## 9) SDK này khác gì với npm package? Cách dùng như thế nào?

### 9.1 So sánh nhanh

| Đặc điểm | npm package | Go SDK này |
|----------|-------------|-----------|
| **Ngôn ngữ** | JavaScript/TypeScript | Go |
| **Import** | `npm install` + `import` | `go get` + `import` |
| **Cách dùng** | Thêm vào project sẵn | Go app riêng hoặc thư viện |
| **CLI** | Có (ví dụ ethers CLI) | Có (`dal-sdk`) |
| **Kích thước** | Nhỏ (JS files) | Nặng (binary Go executable) |
| **Performance** | Chậm hơn (interpreted) | Nhanh (compiled) |

**SDK này là Go module** — không phải npm package. Nếu bạn muốn JS/TS version, có thể:
- Dùng **CLI `dal-sdk`** (recommend nếu chỉ cần API call ngắn gọn).
- Wrap SDK này qua gRPC/HTTP để JS client call.
- Viết riêng JS SDK tương tự (future work).

### 9.2 Cách user dùng Go SDK (3 hình thức)

#### 🔹 **Hình thức 1: Dùng CLI (dễ nhất, không viết code)**

```bash
# Build CLI
cd apps/cosmos-exec
go build -o dal-sdk ./cmd/dal-sdk

# Chạy lệnh
./dal-sdk chain start --name mychain --namespace myns --da-rpc <url>
./dal-sdk contract deploy --wasm ./contract.wasm --init-msg '{}' --rpc http://127.0.0.1:50051
./dal-sdk contract query --contract cosmos1... --msg '{}' --rpc http://127.0.0.1:50051
```

**Dùng khi:** Người dùng chỉ cần nhanh chóng deploy/test contract, không cần app logic phức tạp.

#### 🔹 **Hình thức 2: Go app dùng SDK (viết code Go)**

```bash
# Step 1: Tạo Go app mới
mkdir my-dapp
cd my-dapp
go mod init github.com/myuser/my-dapp

# Step 2: Import SDK
cat > main.go << 'EOF'
package main

import (
	"context"
	"github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm"
)

func main() {
	ctx := context.Background()
	
	// Config chain
	cfg := cosmoswasm.DefaultDALChainConfig("./ev-node")
	cfg.ChainName = "my-chain"
	cfg.Namespace = "my-namespace"
	cfg.DABridgeRPC = "https://..."
	cfg.DAAuthToken = "<token>"
	
	// Start chain
	proc, _ := cosmoswasm.StartDALChain(ctx, cfg)
	defer proc.Stop()
	
	// Use SDK
	client := cosmoswasm.NewClient(proc.Endpoints.SequencerExecAPI)
	// ...
}
EOF

# Step 3: Download SDK dependency
go mod tidy

# Step 4: Chạy app
go run main.go
```

**Dùng khi:** Người dùng cần build app dApp (backend service, bot, dashboard) tích hợp logic riêng.

#### 🔹 **Hình thức 3: Dùng Go SDK trong project sẵn (thêm vào codebase)**

Nếu đã có Go project (service BE, API gateway):

```bash
# Thêm dependency vào go.mod
go get github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm

# Dùng trong code hiện tại
import "github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm"

// Tích hợp vào luồng app
func deployContractHandler(w http.ResponseWriter, r *http.Request) {
	client := cosmoswasm.NewClient("http://127.0.0.1:50051")
	result, err := client.SubmitTxBase64(r.Context(), txB64)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
```

**Dùng khi:** Người dùng có sẵn backend cần tích hợp DAL chain functionality.

### 9.3 Cách import chính xác trong Go

**Tùy nơi bạn import từ:**

1. **Nếu import từ EVNode repo (dev):**
   ```go
   import "github.com/evstack/ev-node/apps/cosmos-exec/sdk/cosmoswasm"
   ```
   - Require: `go.mod` có `require github.com/evstack/ev-node v0.x.x`

2. **Nếu publish lên GitHub (production):**
   ```go
   import "github.com/DataAvailabilityLayerNovel/chain-sdk/cosmoswasm"
   ```
   - (Cần setup `go.mod` ở chain-sdk repo riêng)

### 9.4 Vì sao là Go không phải JS?

- ✅ **Go** → compiled, fast, dùng được cho backend + CLI + daemon.
- ❌ **JS** → interpreted, slower, chỉ tốt cho browser/frontend.

**Nếu user cần JS:**
- Dùng **CLI `dal-sdk`** qua shell script hoặc HTTP wrapper.
- Hoặc gọi Go SDK qua **gRPC/REST gateway** từ JS code.

### 9.5 Tổng kết

| Mục đích | Dùng cái gì | Yêu cầu |
|----------|------------|--------|
| **Test nhanh** | CLI `dal-sdk` | Không cần code, chỉ cần shell |
| **Build backend service** | Go app + SDK | Go installed, dùng `go get` |
| **Thêm vào service sẵn** | Import SDK | Go project hoặc service |
| **Web app (JS/React)** | CLI via shell hoặc HTTP wrapper | JS exec shell / fetch API |
