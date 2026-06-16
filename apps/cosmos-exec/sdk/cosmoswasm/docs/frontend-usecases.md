# Luồng theo từng Use Case (từ Frontend)

Tài liệu này mô tả **từng use case (UC)** của frontend `my-dapp-web`: bắt đầu từ thao tác người dùng → đi qua hàm nào → gọi hàm nào → chạm endpoint backend nào → trả kết quả gì. Mục đích để đọc được "hàm A gọi hàm B gọi endpoint C" mà không phải lần mò code.

> Bổ trợ cho [frontend-integration.md](frontend-integration.md) (kiến trúc tổng thể, proxy, hợp đồng phí). File này tập trung vào **call graph theo use case**.
>
> Quy ước file: đường dẫn `src/...` thuộc repo **my-dapp-web**; tên hàm Go thuộc SDK `cosmoswasm` khi có nhắc tới.

## Bản đồ tầng (đọc trước)

```
UI page/component  →  lib/sign.ts | lib/backend.ts  →  proxy /api hoặc /ev  →  backend
   (React)              (encode/sign/poll)              (next.config.mjs)      (cosmos-exec / ev-node)
```

- **`src/lib/backend.ts`** — `api.*` (HTTP cosmos-exec) + `evApi.*` (ConnectRPC ev-node). Mọi GET/POST đi qua đây (có retry, qua proxy `/api`, `/ev`).
- **`src/lib/sign.ts`** — encode message + ký Keplr + submit + poll. Trái tim của mọi UC ghi (write).
- **`src/lib/keplr.ts`** — kết nối ví + `keplr.signDirect`.
- **`src/lib/config.ts`** — `requiredFeeAmount()`, `buildKeplrChainInfo()`, hằng phí/chain.

Hai "đường xương sống" được nhiều UC dùng lại:

- **Đường ghi (write)** = `signAndBroadcast()` — UC Deploy, Execute đều gọi nó.
- **Đường đọc (read)** = `api.getTx` / `api.getBlock` / `api.querySmart` — UC Query và toàn bộ Explorer dùng nó.

---

## UC0 — Kết nối ví (điều kiện tiên quyết cho mọi UC ghi)

**Ai dùng:** chain user (người ký & gửi tx).
**Bắt đầu:** nút Connect (`KeplrButton` ở `layout.tsx`), hoặc tự động re-connect khi load lại trang.

```
KeplrButton / WalletProvider.connect()         [src/components/WalletProvider.tsx]
   └─ connectKeplr()                            [src/lib/keplr.ts]
        ├─ window.keplr.experimentalSuggestChain( buildKeplrChainInfo() )   [config.ts → suggest chain động theo origin]
        ├─ window.keplr.enable(CHAIN_ID)
        ├─ window.getOfflineSigner(CHAIN_ID)
        └─ signer.getAccounts() → { address, pubkey }
   └─ (sau khi có address) fetchBalance(address)
        └─ api.getBalance(addr, GAS_DENOM)       [backend.ts]  → GET /bank/balance/{addr}?denom=
```

**Kết quả:** `useWallet()` cung cấp `{ address, pubkey, signer, balance }` cho toàn app. Cờ `keplr_connected_v1` lưu localStorage để lần sau **silent reconnect** (không hỏi lại).

> Mọi UC ghi bên dưới đều giả định `wallet.address / signer / pubkey` đã có. Nếu chưa → component hiện `ConnectHint`.

---

## UC1 — Xin token test (Faucet)

**Ai dùng:** chain user (cần vốn để trả phí trước khi gửi tx).
**Bắt đầu:** `FaucetButton` (đặt đầu trang `/explorer`).

```
FaucetButton.onClick()                          [src/components/FaucetButton.tsx]
   ├─ target = ô nhập || wallet.address          (tự điền ví đang connect)
   └─ api.requestFaucet(target)                  [backend.ts]
        └─ GET /faucet?addr=…   (qua proxy /api → backend cosmos-exec /faucet)
```

**Kết quả & nhánh lỗi (xử ở `friendlyError`):**
- 200 → `{ tx_hash, recipient, amount }` → hiện link `/explorer/tx/{tx_hash}`.
- 404 → "Faucet is disabled" (backend không bật `COSMOS_EXEC_TREASURY_PRIVKEY_HEX`).
- 429 → message cooldown.

> Faucet **không** đi qua `signAndBroadcast` — backend tự ký bằng treasury key, FE chỉ gọi GET.

---

## UC2 — Deploy contract (Store WASM → Instantiate)

**Ai dùng:** app developer.
**Bắt đầu:** tab **Deploy** ở `/contract` (`DeployTab`). Là **2 tx liên tiếp**, dùng chung `signAndBroadcast`.

### Bước 1 — Store bytecode

```
DeployTab.handleStoreCode()                     [src/app/contract/page.tsx]
   ├─ wasmBytes = await wasmFile.arrayBuffer()
   ├─ encodeStoreCode(address, wasmBytes)        [sign.ts] → { typeUrl:/cosmwasm…MsgStoreCode, value }
   └─ signAndBroadcast(signer, address, pubkey, [msg])   ← xương sống write (xem UC chung bên dưới)
   └─ (code===0) findEventAttr(events,"store_code","code_id")  → lưu codeId
```

### Bước 2 — Instantiate

```
DeployTab.handleRawInstantiate() | handleInstantiate(values)
   ├─ innerMsg = JSON.parse(rawInstJson)  |  buildMsg(instSchema, values)   [schema.ts]
   └─ runInstantiate(innerMsg)
        ├─ encodeInstantiate(address, codeId, label, innerMsg)   [sign.ts] → MsgInstantiateContract
        ├─ signAndBroadcast(...)                                  ← xương sống write
        └─ (code===0) dò địa chỉ contract từ events:
             findEventAttr("instantiate","_contract_address")
               ?? findFirstContractAddr(events)
               ?? collectAnyBech32(events)  (loại bỏ địa chỉ sender)
           → auto-fill vào ô Contract, người dùng chuyển sang tab Read/Write.
```

Nếu không dò được địa chỉ → render `ManualAddrFallback` để người dùng chọn/paste từ events.

---

## UC3 — Gọi hàm ghi của contract (Execute / Write)

**Ai dùng:** chain user / app developer.
**Bắt đầu:** tab **Write** ở `/contract` (`WriteTab`).

```
WriteTab.runRawExecute() | runExecute(values)   [src/app/contract/page.tsx]
   ├─ innerMsg = JSON.parse(rawJson)  |  buildMsg(selectedFn, values)   [schema.ts]
   ├─ encodeExecute(address, contractAddr, innerMsg)   [sign.ts] → MsgExecuteContract
   └─ signAndBroadcast(signer, address, pubkey, [msg]) ← xương sống write
   └─ setResult(res) → <TxResult/>   (hiện code, log, events, cost)
```

---

## Xương sống WRITE — `signAndBroadcast()` (dùng bởi UC2, UC3)

Đây là hàm mọi UC ghi đi qua. Nằm ở `src/lib/sign.ts`.

```
signAndBroadcast(signer, address, pubkey, msgs[])         [src/lib/sign.ts]
   1. api.getAccount(address)         [backend.ts] → GET /auth/account/{addr}   (lấy account_number, sequence)
   2. encode bodyBytes (TxBody) + pubKeyAny
   3. SIMULATE để đo gas:
        ├─ dựng TxRaw 0-fee, chữ ký dummy 64 byte
        ├─ api.simulateTx(base64)     [backend.ts] → POST /tx/simulate
        └─ gasLimit + feeCoins lấy từ kết quả; nếu lỗi → fallback DEFAULT_GAS + requiredFeeAmount()  [config.ts]
   4. dựng signDoc thật (gas + fee đã size)
   5. keplrSignDirect(address, signDoc)   [keplr.ts] → window.keplr.signDirect(preferNoSetFee:true)
        (ép Keplr ký đúng fee FE đặt, không để Keplr ghi đè về 0)
   6. encode TxRaw (bodyBytes + authInfoBytes + signature)
   7. api.submitTx(base64)            [backend.ts] → POST /tx/submit  → { hash }
   8. waitForTx(hash)                 [backend.ts] → poll GET /tx/result?hash=  tới khi found (timeout 60s)
   → trả TxExecutionResult { code, log, height, events, cost }
```

**Vì sao simulate trước:** client không biết gas thật cho tới khi chạy thử; backend cố tình **không** enforce fee ở chế độ simulate (tránh deadlock con-gà-quả-trứng). Chi tiết hợp đồng phí: [fee-economics.md](fee-economics.md), [frontend-integration.md](frontend-integration.md) mục 4 & 6.

---

## UC4 — Đọc state contract (Query / Read)

**Ai dùng:** chain user / app developer.
**Bắt đầu:** tab **Read** ở `/contract` (`ReadTab`). **Không ký, không tốn phí, không lên block.**

```
ReadTab.runRawQuery() | runQuery(values)        [src/app/contract/page.tsx]
   ├─ msg = JSON.parse(rawJson)  |  buildMsg(selectedFn, values)   [schema.ts]
   └─ api.querySmart(contractAddr, msg)          [backend.ts] → POST /wasm/query-smart
   → hiện JSON kết quả
```

---

## UC5 — Explorer: danh sách block + tx gần đây

**Ai dùng:** mọi người (chain user, node operator, observer).
**Bắt đầu:** trang `/explorer` (`ExplorerPage`), auto-refresh 3s khi đang ở trang mới nhất.

```
ExplorerPage tick() (mỗi 3s)                     [src/app/explorer/page.tsx]
   ├─ api.getLatestBlock()           [backend.ts] → GET /blocks/latest   (lấy height đỉnh)
   └─ với mỗi height trong cửa sổ:
        getBlockWithDA(h)            [backend.ts]
           ├─ api.getBlock(h)        → GET /blocks/{h}            (block từ cosmos-exec)
           └─ evApi.getBlock(h)      → POST /ev/StoreService/GetBlock   (DA height + signed header/data từ ev-node)
              (ev-node không reachable → fallback im lặng, chỉ trả block cosmos-exec)
   └─ Bảng tx: lazy-load chi tiết từng tx hiển thị
        api.getTx(hash)              [backend.ts] → GET /tx/{hash}   (gồm gas, bytes, cost breakdown)
```

`DAHeightBadge` hiển thị header/data DA height; cột "Fee · DA" lấy từ `tx.cost` (phí gas charge cho user + hoá đơn DA của sequencer).

---

## UC6 — Explorer: chi tiết block + nội dung DA + decode tx

**Ai dùng:** mọi người.
**Bắt đầu:** trang `/explorer/[height]` (`BlockDetailPage`).

```
BlockDetailPage                                  [src/app/explorer/[height]/page.tsx]
   └─ getBlockWithDA(h)              [backend.ts] (như UC5: cosmos-exec + ev-node)
        ├─ <DAInfo>     — header/data DA height, namespace "rollup"
        ├─ <DAContent>  — bytes thật đã publish lên Celestia (signed header blob + data blob)
        │     └─ <TxBlob> "Decode" → decodeTxBase64(b64)   [src/lib/decodeTx.ts]   (decode TxRaw ngay trên browser)
        └─ <TxList>     — mỗi hash: api.getTx(hash) → gas/bytes/cost/status
```

Điểm đáng chú ý: **DAContent là dữ liệu lấy trực tiếp từ ev-node StoreService** (đúng bytes lên DA), không phải suy ra từ node local.

---

## UC7 — Explorer: chi tiết transaction

**Ai dùng:** mọi người.
**Bắt đầu:** trang `/explorer/tx/[hash]` (`TxDetailPage`).

```
TxDetailPage tick() (poll 2s tới khi found)      [src/app/explorer/tx/[hash]/page.tsx]
   └─ api.getTx(hash)               [backend.ts] → GET /tx/{hash}
        ├─ "Fee accounting"     — từ tx.cost (execution fee vs DA cost)
        ├─ "Contracts touched"  — collectContractAddrs(tx.events)   [sign.ts] → link sang /contract?addr=
        ├─ "Log" + "Events"
        └─ chưa found → "pending", tự refresh 2s
```

---

## UC8 — Tab Info: quét tx liên quan tới một contract

**Ai dùng:** app developer (theo dõi contract của mình).
**Bắt đầu:** tab **Info** ở `/contract` (`InfoTab`), nút "Scan latest N blocks".

```
InfoTab.scan(page)                               [src/app/contract/page.tsx]
   ├─ api.getLatestBlock()          → GET /blocks/latest          (xác định cửa sổ quét)
   ├─ runPool(heights, 8, api.getBlock)          → GET /blocks/{h}  (gom tx_hashes, concurrency 8)
   ├─ runPool(hashes, 8, api.getTx)              → GET /tx/{hash}    (lấy events từng tx)
   └─ lọc tx mà events có attribute value === contractAddr  → bảng "Related transactions"
```

`runPool` giới hạn 8 request song song để backend local không nghẽn; lỗi lẻ được đếm chứ không huỷ cả lần quét.

---

## Phía Backend — endpoint chạm vào đâu trong repo `ev-node`

Phần FE ở trên dừng ở "gọi endpoint X". Phần này đi tiếp **vào trong repo hiện tại**: endpoint đó được đăng ký ở đâu, handler nào xử, rồi gọi xuống method nào của executor.

### Hai tiến trình backend (FE nói chuyện với cả hai)

| Proxy FE | Tiến trình | Cổng mặc định | Vai trò | Vào file nào trong repo |
|----------|-----------|---------------|---------|--------------------------|
| `/api/*` | **cosmos-exec-grpc** | `:50051` | Lớp thực thi (mempool, FinalizeBlock, query, account/balance) + REST cho FE | `apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go` |
| `/ev/*` | **ev-node node** (sequencer/full node) | `:38331` | Consensus: đóng block, publish lên DA, phục vụ block đã ký qua StoreService | `block/`, `pkg/sync/`, `pkg/rpc/` |

Điểm nối hai bên: cosmos-exec-grpc **không tự đóng block**. Nó chỉ giữ mempool + chạy tx. ev-node (sequencer) mới là bên gọi vào executor để lấy tx và thực thi. "Hợp đồng" giữa hai bên là interface `Executor` trong `core/execution/execution.go` (6 method, zero-dependency).

### Đăng ký route (một chỗ duy nhất)

Tất cả route REST mà FE gọi được đăng ký trong **`apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go:140-173`** (`NewExecutorServiceHandlerWithMux` — gắn vừa gRPC ExecutorService cho ev-node, vừa REST cho FE lên cùng một mux). Mỗi `mux.HandleFunc(path, handler)` → một hàm `xxxHandler(exec)` cùng file → gọi 1 method của `executor.CosmosExecutor`.

### Map endpoint → handler → method executor

| Endpoint FE | Handler (main.go) | Method executor (`apps/cosmos-exec/executor/executor.go`) | Làm gì |
|-------------|-------------------|-----------------------------------------------------------|--------|
| `POST /tx/submit` | `submitTxHandler` | `InjectTx` (line ~428) | Decode tx → **đẩy vào mempool** (chưa chạy), trả hash |
| `POST /tx/simulate` | `txSimulateHandler` | `SimulateTx` (line ~632) | Chạy thử (CheckTx/ante), trả `gas_used` → FE size gas_limit + fee |
| `GET /tx/result?hash=` | `txResultHandler` | `GetTxResult` (line ~448) | Tra map kết quả; kèm `cost` tính ở `cost.go` (`getCostPolicy().estimate`) |
| `GET /tx/{hash}` | `txByHashHandler` | `GetTxResult` | Như trên, kèm `status` success/failed/pending |
| `POST /tx/estimate` | `txEstimateHandler` | `GetTxResult` (nếu có hash) | Ước phí, **không thu tiền** (`cost.go`) |
| `POST /wasm/query-smart` | `querySmartHandler` | `QuerySmart` | Query read-only vào WASM, không lên block |
| `GET /blocks/latest` | `blocksLatestHandler` | `GetLatestBlock` | Block đỉnh |
| `GET /blocks/{height}` | `blockByHeightHandler` | `GetBlock` (line ~739) | Block tại height (height, time, app_hash, tx_hashes) |
| `GET /auth/account/{addr}` | `authAccountHandler` | `GetAccountInfo` | `account_number`, `sequence` (FE cần để ký) |
| `GET /bank/balance/{addr}` | `bankBalanceHandler` | `GetBalance` | Số dư; có alias LCD `/cosmos/bank/...` cho Keplr |
| `GET /faucet?addr=` | `faucetHandler` (`faucet.go`) | `GetAccountInfo` + `InjectTx` | Ký bằng treasury key (`cosmoswasm.BuildSignedBankSendWithFee`) rồi tự submit |

### Vòng đời 1 tx ghi — đi xuyên cả hai tiến trình

Đây là phần "BE thật" mà UC2/UC3 kích hoạt sau khi FE `POST /tx/submit`:

```
FE signAndBroadcast → POST /tx/submit
        │
        ▼  [cosmos-exec-grpc]
submitTxHandler → CosmosExecutor.InjectTx       → tx nằm trong mempool (e.mempool)
        ┊  (tx chờ ở đây, CHƯA có kết quả → /tx/result trả found:false)
        ▼  [ev-node sequencer — tiến trình khác, theo block_time]
reaper kéo tx:   block/internal/reaping/reaper.go      → gọi Executor.GetTxs  → lấy & xoá mempool
đóng & chạy block: block/internal/executing/executor.go → gọi Executor.ExecuteTxs
        │
        ▼  [quay lại cosmos-exec-grpc] CosmosExecutor.ExecuteTxs (executor.go ~326)
        ├─ app.FinalizeBlock(...)   → chạy ante (fee check) + message handler từng tx
        ├─ app.Commit()             → ghi state IAVL, ra app_hash mới
        └─ lưu txResults[hash] + blocks[height]   ← từ đây /tx/result trả found:true
        │
        ▼  [ev-node] publish block lên DA + phục vụ qua StoreService
pkg/sync/sync_service.go + DA submitter → blob (signed header + tx bytes) lên Celestia
StoreService (/ev …GetBlock) phục vụ signed header/data  ← UC5/UC6 đọc qua proxy /ev
        │
        ▼  [FE] waitForTx poll GET /tx/result tới khi found → trả code/log/events/cost
```

Hai mốc quan trọng để hiểu vì sao FE phải **poll**:
- `InjectTx` chỉ bỏ tx vào mempool → trả hash ngay, nhưng **chưa có kết quả**.
- Kết quả chỉ xuất hiện sau khi ev-node gọi `ExecuteTxs` ở block kế tiếp (`block_time`, mặc định 2s). Vì vậy `waitForTx` poll `/tx/result`.

### Fee được enforce ở đâu (liên quan hợp đồng phí FE↔BE)

- Luật phí nằm ở **`apps/cosmos-exec/app/ante.go`** (`feePolicy` đọc từ ENV `COSMOS_EXEC_MIN_GAS_PRICE` / `GAS_DENOM`). Chạy trong `FinalizeBlock` (qua ante handler) → fee thiếu/đặt sai denom thì tx `ErrInsufficientFee`.
- Cố tình **không** enforce ở `SimulateTx` (ExecModeSimulate) để FE đo được gas khi chưa có fee — đây là lý do FE simulate trước rồi mới ký. Khớp với [fee-economics.md](fee-economics.md) và [frontend-integration.md](frontend-integration.md) mục 6.
- `cost` hiển thị ở explorer tính tại **`apps/cosmos-exec/cmd/cosmos-exec-grpc/cost.go`** (gas × min_gas_price = phí user; bytes × tia_per_byte = hoá đơn DA của sequencer, **không** trừ ví user).

---

## Bảng tra nhanh: UC → hàm FE → endpoint backend

| UC | Hàm khởi đầu (FE) | Hàm lõi gọi qua | Endpoint backend | Handler + method BE (repo `ev-node`) |
|----|-------------------|------------------|------------------|--------------------------------------|
| UC0 Connect | `WalletProvider.connect` | `connectKeplr`, `api.getBalance` | `/bank/balance/{addr}` | `bankBalanceHandler` → `GetBalance` |
| UC1 Faucet | `FaucetButton.onClick` | `api.requestFaucet` | `GET /faucet?addr=` | `faucetHandler` (`faucet.go`) → `GetAccountInfo`+`InjectTx` |
| UC2 Deploy | `DeployTab.handleStoreCode` / `runInstantiate` | `encodeStoreCode`/`encodeInstantiate` → `signAndBroadcast` | `/auth/account`, `/tx/simulate`, `/tx/submit`, `/tx/result` | `submitTxHandler`→`InjectTx`; rồi ev-node `GetTxs`→`ExecuteTxs` |
| UC3 Execute | `WriteTab.runExecute` | `encodeExecute` → `signAndBroadcast` | `/auth/account`, `/tx/simulate`, `/tx/submit`, `/tx/result` | như UC2 |
| UC4 Query | `ReadTab.runQuery` | `api.querySmart` | `POST /wasm/query-smart` | `querySmartHandler` → `QuerySmart` |
| UC5 Explorer list | `ExplorerPage.tick` | `getBlockWithDA`, `api.getTx` | `/blocks/latest`, `/blocks/{h}`, `/ev/…GetBlock`, `/tx/{hash}` | `blocksLatestHandler`/`blockByHeightHandler`/`txByHashHandler`; `/ev`→`pkg/sync` StoreService |
| UC6 Block detail | `BlockDetailPage` | `getBlockWithDA`, `decodeTxBase64` | `/blocks/{h}`, `/ev/…GetBlock`, `/tx/{hash}` | `blockByHeightHandler`→`GetBlock`; StoreService cho DA content |
| UC7 Tx detail | `TxDetailPage.tick` | `api.getTx`, `collectContractAddrs` | `GET /tx/{hash}` | `txByHashHandler` → `GetTxResult` |
| UC8 Scan related | `InfoTab.scan` | `runPool` + `api.getBlock`/`api.getTx` | `/blocks/latest`, `/blocks/{h}`, `/tx/{hash}` | `blockByHeightHandler`+`txByHashHandler` |

> Mọi handler ở cột cuối nằm trong `apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go` (trừ ghi chú khác); method executor nằm trong `apps/cosmos-exec/executor/executor.go`; `GetTxs`/`ExecuteTxs` được ev-node gọi từ `block/internal/reaping/` và `block/internal/executing/`.

## Tham chiếu chéo

- Hợp đồng phí FE ↔ backend, Keplr, proxy: [frontend-integration.md](frontend-integration.md)
- Công thức phí backend (`feePolicy.requiredFee`): [fee-economics.md](fee-economics.md)
- Cổng & process (cosmos-exec 50051, ev-node RPC 38331): [node-operations.md](node-operations.md)
- Tự tạo account khi nhận token: [auto-account-creation.md](auto-account-creation.md)
</content>
</invoke>
