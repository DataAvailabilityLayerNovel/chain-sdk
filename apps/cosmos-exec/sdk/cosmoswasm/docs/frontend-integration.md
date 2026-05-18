# Tích hợp Frontend (my-dapp-web)

Tài liệu này mô tả frontend tham chiếu `my-dapp-web` (repo riêng) nói chuyện với stack cosmos-exec thế nào: kiến trúc, proxy, Keplr, ký tx, explorer, faucet, và **hợp đồng cấu hình phí giữa FE ↔ backend** (điểm dễ sai nhất).

> Liên quan: [fee-economics.md](fee-economics.md) (feePolicy backend), [node-operations.md](node-operations.md) (ports), [auto-account-creation.md](auto-account-creation.md).

## 1. Stack & nguyên tắc

- **Next.js 15 + React 19 + TypeScript + Tailwind.** Deps gọn: `@keplr-wallet/types`, `cosmjs-types`, `long`.
- **Không dùng cosmjs `SigningClient`.** Backend expose HTTP `/tx/submit` (không phải Tendermint RPC `/broadcast_tx_sync`), nên FE tự encode `TxRaw` rồi POST. Xem `src/lib/sign.ts`.
- Mọi request đi qua **proxy Next.js**, không gọi thẳng backend (tránh CORS).

## 2. Proxy (`next.config.mjs`)

| Đường FE | Đích | Env override | Mặc định |
|----------|------|--------------|----------|
| `/api/:path*` | cosmos-exec-grpc HTTP | `BACKEND_URL` | `http://127.0.0.1:50051` |
| `/ev/:path*` | ev-node ConnectRPC (StoreService) | `EVNODE_RPC_URL` | `http://127.0.0.1:38331` |

→ Trùng với cổng sequencer ở [node-operations.md](node-operations.md) (exec 50051, ev-node RPC 38331). Đổi node khác cổng thì set 2 env này.

## 3. Lớp API (`src/lib/backend.ts`)

Wrapper có type cho mọi endpoint:

- `api.*` → backend cosmos-exec: `getAccount`, `getBalance`, `submitTx`, `simulateTx`, `getTxResult`, `querySmart`, `getBlock(s)`, `getTx`, `estimateTxCost`, `requestFaucet`.
- `evApi.getBlock` → ev-node StoreService (`/ev/...GetBlock`) lấy signed header + data + DA height.
- `getBlockWithDA(h)` gộp block từ backend + nội dung DA từ ev-node (fallback im lặng nếu ev-node không reachable).

## 4. Hợp đồng cấu hình phí FE ↔ backend (QUAN TRỌNG)

`src/lib/config.ts` **phải khớp** env `COSMOS_EXEC_*` của backend (xem [fee-economics.md](fee-economics.md) mục 6):

| FE (`NEXT_PUBLIC_*`) | Backend (`COSMOS_EXEC_*`) | Phải |
|----------------------|---------------------------|------|
| `NEXT_PUBLIC_MIN_GAS_PRICE` | `MIN_GAS_PRICE` | **bằng nhau** |
| `NEXT_PUBLIC_GAS_DENOM` | `GAS_DENOM` | **bằng nhau** |
| `NEXT_PUBLIC_CHAIN_ID` | chain id của node | bằng nhau |

`config.ts` có `requiredFeeAmount(gasLimit) = ceil(gasLimit × MIN_GAS_PRICE)` — tính **đúng công thức `feePolicy.requiredFee()`** của backend (`app/ante.go`). Nếu hai bên lệch:

- FE đặt phí thấp hơn → backend `ErrInsufficientFee` (khi đã bật fee).
- FE đặt denom khác → phí không được tính, tx rớt.

Hệ quả thiết kế: bật fee ở backend thì **phải** set lại `NEXT_PUBLIC_MIN_GAS_PRICE`/`GAS_DENOM` cho FE tương ứng, rebuild web.

## 5. Keplr (`src/lib/keplr.ts`, `WalletProvider`)

- Kết nối qua `window.keplr` — tương thích **Keplr / OKX / Leap** (ví nào "thắng" injection thì dùng ví đó).
- `buildKeplrChainInfo()` suggest chain động từ `window.location.origin` → chạy được trên mọi host (localhost:3000, domain deploy…). `rpc`/`rest` đều trỏ `/api`.
- **Bẫy phí Keplr — `signOptions.preferNoSetFee: true`**: bắt Keplr ký đúng fee FE nhét vào `authInfoBytes`, không để Keplr ghi đè bằng fee từ registry (thường `gasPriceStep=0` → zero fee → backend từ chối). Đây là lý do `gasPriceStep` trong chain info được set = `MIN_GAS_PRICE`.
- `WalletProvider`/`useWallet()` cung cấp `address` + `balance` (đọc `/bank/balance`, null nếu backend chưa có). `KeplrButton` ở `layout.tsx` (bọc toàn app).

## 6. Vòng đời tx (`src/lib/sign.ts`)

```
encode MsgStoreCode/Instantiate/Execute (cosmjs-types)
   │
   ├─ simulateTx (0-fee, dummy sign) → đo gas thật
   ├─ size gas_limit + fee = requiredFeeAmount(gas)   ← khớp feePolicy backend
   ├─ Keplr.signDirect(preferNoSetFee:true)
   ├─ encode TxRaw → POST /tx/submit
   └─ poll /tx/result tới khi found
```

Simulate trước rồi mới sizing là bắt buộc vì client không biết gas tới khi chạy thử — và backend cố tình **không enforce fee ở ExecModeSimulate** để tránh deadlock con-gà-quả-trứng (xem comment `txFeeChecker` trong `ante.go`).

## 7. Trang & component

| Route | Việc |
|-------|------|
| `/` | Landing |
| `/contract` | Deploy (store WASM + instantiate) + tab Read/Write theo schema |
| `/explorer` | Danh sách block (refresh 3s) + **FaucetButton** |
| `/explorer/[height]` | Chi tiết block: DA content (signed header + data), decode tx |
| `/explorer/tx/[hash]` | Chi tiết tx |

- **SchemaForm + `schema.ts`**: parse JSON Schema do `cargo schema` sinh (object / oneOf tagged-union kiểu cw20) → render form động cho execute/query, không cần hardcode ABI.
- **decodeTx.ts**: decode `TxRaw` ngay trên browser (cosmjs-types) cho explorer — không gọi thêm backend.
- **DAHeightBadge / DA content**: hiển thị chính xác bytes đã publish lên Celestia, lấy từ `evApi`.

## 8. Faucet UI (`src/components/FaucetButton.tsx`)

- Gọi `api.requestFaucet(addr)` → `/api/faucet?addr=` → backend `/faucet` (xem [fee-economics.md](fee-economics.md) mục 6c).
- Tự điền địa chỉ ví Keplr đang connect (`useWallet()`).
- Tự xử trạng thái backend: **404** → "Faucet is disabled"; **429** → message cooldown; success → `tx_hash` link sang `/explorer/tx/{hash}`.
- Đặt ở đầu `/explorer`. Không cần ẩn/hiện thủ công — backend tắt thì UI tự báo.

## 9. Chạy local

```bash
# backend (sequencer) — ví dụ bật fee + faucet
COSMOS_EXEC_ENFORCE_SIGNATURES=true \
COSMOS_EXEC_MIN_GAS_PRICE=0.000001 COSMOS_EXEC_GAS_DENOM=ustake \
COSMOS_EXEC_TREASURY_PRIVKEY_HEX=<hex> cosmos-exec-grpc --address 127.0.0.1:50051 ...

# frontend — phải mirror đúng giá phí
cd my-dapp-web
NEXT_PUBLIC_MIN_GAS_PRICE=0.000001 NEXT_PUBLIC_GAS_DENOM=ustake \
BACKEND_URL=http://127.0.0.1:50051 EVNODE_RPC_URL=http://127.0.0.1:38331 \
npm run dev   # http://localhost:3000
```

Checklist tích hợp:

- [ ] `NEXT_PUBLIC_MIN_GAS_PRICE` == `COSMOS_EXEC_MIN_GAS_PRICE`, denom khớp.
- [ ] `BACKEND_URL`/`EVNODE_RPC_URL` trỏ đúng node (cổng ở [node-operations.md](node-operations.md)).
- [ ] Connect ví → balance hiện (cần backend có `/bank/balance` + account có vốn → dùng faucet).
- [ ] Deploy thử contract: simulate → sign (Keplr hiện đúng fee ≠ 0) → /tx/submit → result success.
- [ ] Explorer hiện block + DA content; FaucetButton trả tx_hash.

## Tham chiếu file (repo my-dapp-web)

| Chủ đề | File |
|--------|------|
| Config phí/chain, Keplr chain info | `src/lib/config.ts` |
| API client (backend + ev-node) | `src/lib/backend.ts` |
| Keplr connect + signDirect | `src/lib/keplr.ts`, `src/components/WalletProvider.tsx` |
| Encode + sign + submit tx | `src/lib/sign.ts` |
| Schema → form | `src/lib/schema.ts`, `src/components/SchemaForm.tsx` |
| Decode tx client-side | `src/lib/decodeTx.ts` |
| Faucet UI | `src/components/FaucetButton.tsx` |
| Proxy | `next.config.mjs` |
