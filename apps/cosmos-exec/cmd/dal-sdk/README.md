# dal-sdk — CLI cho DAL Cosmos WASM chain

CLI Cobra wrapping SDK Go [`apps/cosmos-exec/sdk/cosmoswasm`](../../sdk/cosmoswasm). Dùng để **manage chain, submit tx, deploy/query contract, native bank ops mà không cần viết Go code** — phù hợp cho ops/script/demo.

Khi viết app trong Go, gọi thẳng SDK (xem [getting-started.md](../../sdk/cosmoswasm/docs/getting-started.md)) sẽ control nhiều hơn — `dal-sdk` chỉ là wrapper tiện cho terminal.

---

## Build

Tool không được install vào `$PATH`. Build local:

```bash
cd apps/cosmos-exec
go build -o dal-sdk ./cmd/dal-sdk
./dal-sdk info
```

Hoặc `go install ./cmd/dal-sdk` để đẩy vào `$GOBIN` (gọi `dal-sdk` từ mọi nơi).

Quick run không build: `go run ./cmd/dal-sdk <subcommand>`.

---

## Subcommand reference

| Command | Mục đích |
|---------|---------|
| `info` | In help |
| `chain start` | Boot sequencer + full node + execution stack |
| `tx submit` | Submit raw tx (base64 / hex / file) lên `/tx/submit`, optional chờ kết quả |
| `tx result --hash <h>` | Tra kết quả tx |
| `bank send` | `MsgSend` — chuyển native token |
| `bank balance` | Query balance native bank qua Cosmos REST |
| `contract store` | Upload `.wasm` bytecode (`MsgStoreCode`) |
| `contract instantiate` | Tạo instance từ `code-id` |
| `contract execute` | Gọi method của contract |
| `contract query` | Smart query (read-only) |
| `contract transfer` | Shortcut CW20 transfer |
| `contract balance` | Shortcut CW20 balance query |
| `contract deploy` | Store + instantiate trong 1 lệnh |
| `contract deploy-cw20` | Deploy CW20 với init message mặc định (name/symbol/decimals/supply) |

---

## Flag chung (lặp ở hầu hết subcommand)

| Flag | Default | Ý nghĩa |
|------|---------|---------|
| `--rpc` | `http://127.0.0.1:50051` | URL `cosmos-exec-grpc` |
| `--rest` | `http://127.0.0.1:38331` | Cosmos REST (chỉ `bank balance`) |
| `--sender` | SDK default (`cosmos1qqq...`) | Sender bech32 |
| `--wait` | `true` | Block đến khi tx có kết quả |
| `--poll-ms` | `1000` | Interval poll khi `--wait=true` |

---

## Examples

### 1. Start chain (sequencer + full node + exec)

```bash
./dal-sdk chain start \
  --name mycosmos \
  --namespace rollup \
  --da-rpc http://127.0.0.1:26658 \
  --project-root /path/to/ev-node
```

Flag bổ sung: `--block-time 1000` (ms), `--submit-interval 5000` (ms), `--clean=true`, `--clean-exit=true`, `--log-level info`.

### 2. Deploy CW20 token (1 lệnh)

```bash
./dal-sdk contract deploy-cw20 \
  --wasm ./cw20_base.wasm \
  --name MyToken \
  --symbol MTK \
  --decimals 6 \
  --supply 1000000
```

Output in ra `code-id`, `contract address`, `tx hash`.

### 3. Deploy generic contract

```bash
# Store + instantiate
./dal-sdk contract deploy \
  --wasm ./my_counter.wasm \
  --init-msg '{"count":0}' \
  --label counter

# Hoặc tách 2 bước
./dal-sdk contract store --wasm ./my_counter.wasm
./dal-sdk contract instantiate --code-id 1 --init-msg '{"count":0}' --label counter
```

### 4. Execute + query

```bash
./dal-sdk contract execute \
  --contract cosmos1abc... \
  --msg '{"increment":{}}'

./dal-sdk contract query \
  --contract cosmos1abc... \
  --msg '{"get_count":{}}'
```

### 5. Native bank

```bash
./dal-sdk bank send --to cosmos1xyz... --amount 1000stake
./dal-sdk bank balance --address cosmos1xyz...
```

### 6. Submit tx có sẵn (base64) + chờ result

```bash
./dal-sdk tx submit --tx-base64 "$TX_B64" --wait
./dal-sdk tx result --hash <txhash>
```

---

## Lưu ý

- **Unsigned tx trên dev chain.** Builder của SDK tạo `TxRaw` với `AuthInfo{}` rỗng, `Signatures: nil`. Chain production có fee thật + verify signer sẽ reject. Cho production xem [frontend-integration.md](../../sdk/cosmoswasm/docs/frontend-integration.md) (Keplr ký) hoặc viết signer code Go.
- **`--sender` mặc định = `cosmos1qqq...`** — placeholder deterministic, không phải wallet thật.
- **`chain start` cần `--da-rpc` và `--project-root`** trỏ tới ev-node repo. Internal gọi `cosmoswasm.StartDALChain` ([api-reference.md § Dev Tooling](../../sdk/cosmoswasm/docs/api-reference.md#dev-tooling)).
- **`tx submit` thực sự gọi gì:** POST body `{tx_base64|tx_hex}` lên `/tx/submit` → handler [submitTxHandler](../cosmos-exec-grpc/main.go) → `executor.InjectTx` → mempool. Có `--wait` thì sau khi nhận `hash`, CLI poll `GET /tx/result?hash=...` đến khi `found=true`.

---

## Tham chiếu

- SDK Go function tương đương: [docs/README.md § Function map](../../sdk/cosmoswasm/docs/README.md#function-map)
- API reference đầy đủ: [api-reference.md](../../sdk/cosmoswasm/docs/api-reference.md)
- Backend endpoint: [api-reference.md § Transaction APIs](../../sdk/cosmoswasm/docs/api-reference.md#transaction-apis)
