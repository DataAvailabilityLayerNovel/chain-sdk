# Tổng hợp lỗi & cảnh báo của cosmos-exec — nguyên nhân và cách fix

Tài liệu này liệt kê **mọi thông điệp lỗi/cảnh báo mà cosmos-exec có thể phát ra** — ở log khởi động, ở response HTTP, ở tầng thực thi và ở AnteHandler — kèm nguyên nhân và cách khắc phục. Mọi mục đều dẫn về dòng code thật để tra cứu.

> Liên quan: [error-handling.md](error-handling.md) (phân loại lỗi + chiến lược retry ở phía SDK), [troubleshooting.md](troubleshooting.md) (kịch bản debug từng tình huống), [profiles-and-security.md](profiles-and-security.md) (auth/rate limit), [configuration.md](configuration.md) (biến cấu hình).

## 0. Về mức log: không có WARN

cosmos-exec dùng `slog` và **chỉ log ở hai mức `INFO` và `ERROR`** — trong toàn bộ mã nguồn server **không có một lời gọi `.Warn()` nào**. Vì vậy:

- "Cảnh báo" theo nghĩa mức log WARN **không tồn tại** ở binary này.
- Mọi sự cố nghiêm trọng đều xuất hiện dưới dạng **`ERROR` (kèm thoát tiến trình)** hoặc **response HTTP 4xx/5xx** trả về cho client (không phải log server).
- Các "cảnh báo mềm" (như tx fail `code != 0`) **không được log ở mức ERROR** — chúng trả 200 OK kèm `code` trong kết quả, client phải tự kiểm. Xem mục [§6](#6-lỗi-thực-thi-tx-code--0--antehandler).

**Binary evcosmos / ev-node thì khác:** đó là tiến trình riêng (block production, sync, DA, P2P) và **có cả log mức `WRN` lẫn `ERR`**. Đây là nguồn gốc của những dòng kiểu:

```
WRN da_start_height is not set in genesis.json, ask your chain developer component=main
WRN entering catch-up mode: replaying missed epochs with forced inclusion txs only checkpoint_da_height=692232 current_epoch=13845 component=main
ERR failed to get blobs error="height is equal to 0" component=da_client height=0
```

Toàn bộ WARN của evcosmos/ev-node được tổng hợp ở [§9](#9-cảnh-báo-warn-từ-evcosmos--ev-node); các dòng `ERR` đáng chú ý nằm rải trong §9 ngay cạnh WARN cùng nguyên nhân (vd [§9.1](#91-da-sẵn-sàng-dữ-liệu)).

---

## 1. Lỗi khởi động — log `ERROR` + thoát (exit 1)

Các lỗi này xảy ra lúc `main()` dựng tiến trình; tiến trình **thoát ngay với exit code 1**, không phục vụ request nào. Nguồn: [main.go](../../../cmd/cosmos-exec-grpc/main.go).

| Thông điệp | Nguyên nhân | Cách fix |
|-----------|-------------|----------|
| `invalid config` | `Config.Validate()` thất bại (xem [§7](#7-lỗi-validate-cấu-hình)) | Sửa flag/env sai theo lỗi con đính kèm |
| `failed to create home directory` | Không tạo được thư mục `--home` (quyền, đĩa đầy) | Kiểm quyền ghi, dung lượng; đổi `COSMOS_EXEC_HOME` |
| `failed to open database` | Mở BadgerDB/MemDB lỗi (đường dẫn data hỏng, lock) | Xoá lock cũ, kiểm `--data-dir`; tiến trình khác đang giữ DB? |
| `faucet config invalid` | Biến faucet sai định dạng (xem [§5](#5-lỗi-faucet)) | Sửa `COSMOS_EXEC_*` faucet theo lỗi con |
| `faucet genesis build failed` | Không dựng được genesis có số dư treasury | Kiểm `COSMOS_EXEC_TREASURY_*`; xem lỗi con |
| `persistence replay failed` | Replay file `blocks.jsonl`/`tx_results.jsonl`/`metadata.json` lỗi khi khởi động | Data dir hỏng/không khớp chain_id — xem [§4](#4-lỗi-tầng-thực-thi-executor); cân nhắc xoá data dir nếu chấp nhận mất state |
| `failed to start server` | `ListenAndServe` lỗi (cổng bận, địa chỉ sai) | Đổi `--listen-addr`; kiểm cổng đang bị chiếm (`lsof -i :50051`) |
| `shutdown error` | Lỗi khi graceful shutdown (**không** thoát bất thường) | Thường vô hại; nếu lặp lại, kiểm request treo quá 10s |

---

## 2. Lỗi tầng bảo mật / middleware — response HTTP

Phát ra bởi `securityMiddleware` ([middleware.go](../../../cmd/cosmos-exec-grpc/middleware.go)) **trước khi** request tới handler. Trả JSON `{"error": "..."}`.

| HTTP | `error` | Nguyên nhân | Cách fix |
|------|---------|-------------|----------|
| 403 | `node is in read-only mode` | `ReadOnlyMode=true` mà client gửi POST (ghi) | Tắt read-only (`COSMOS_EXEC_READ_ONLY=false`) hoặc chỉ gọi endpoint đọc |
| 401 | `unauthorized` | Bật `AuthToken` nhưng thiếu/sai header `Authorization: Bearer <token>` | Gửi đúng token; hoặc bỏ `COSMOS_EXEC_AUTH_TOKEN` ở môi trường dev |
| 429 | `rate limit exceeded` | Vượt `RateLimitRPS` (token-bucket theo IP) | Giảm tần suất gọi/backoff; tăng `COSMOS_EXEC_RATE_LIMIT_RPS`; prod mặc định 100 |
| 413* | (body bị cắt) | Body vượt `MaxRequestBodyBytes` (mặc định 10 MB) | Giảm kích thước payload; tăng `COSMOS_EXEC_MAX_BODY_BYTES` |

\* Giới hạn body thực thi bằng `http.MaxBytesReader`; khi vượt, lần đọc body ở handler trả lỗi → thường biểu hiện thành `failed to read body` (400).

---

## 3. Lỗi validate ở handler — response HTTP 4xx

Mỗi handler kiểm method + body + tham số trước khi gọi executor. Nguồn: [main.go](../../../cmd/cosmos-exec-grpc/main.go) (các `*Handler`).

| HTTP | `error` | Nguyên nhân | Cách fix |
|------|---------|-------------|----------|
| 405 | `method not allowed` | Sai HTTP method (vd GET vào route PO‑only) | Dùng đúng method (xem [api-reference.md](api-reference.md)) |
| 400 | `failed to read body` | Đọc body lỗi / body vượt giới hạn | Kiểm kết nối, kích thước body |
| 400 | `invalid json body` | Body không phải JSON hợp lệ | Sửa JSON; đặt `Content-Type: application/json` |
| 400 | `tx_base64 or tx_hex is required` | `/tx/*` không có trường tx nào | Gửi `tx_hex` **hoặc** `tx_base64` |
| 400 | `invalid tx_hex` / `invalid tx_base64` / `odd hex length` | Chuỗi tx không decode được | Kiểm encoding tx (hex chẵn ký tự, base64 chuẩn) |
| 400 | `tx cannot be empty` | Tx decode ra rỗng | Dựng lại tx; signer/builder có trả bytes không? |
| 400 | `hash is required` / `hash too long` | `/tx/result` thiếu `?hash=` hoặc hash > 128 ký tự | Truyền hash hợp lệ (SHA‑256 hex) |
| 400 | `contract is required` / `msg is required` | `/wasm/query-smart` thiếu trường | Gửi đủ `contract` + `msg` |
| 400 | `invalid block height` | `/blocks/{height}` height không phải số > 0 | Dùng height nguyên dương |
| 400 | `supply one of {tx_base64\|tx_hex} + gas, {hash}, or {bytes, gas}` | `/tx/estimate` không đủ input | Cung cấp một trong ba dạng input hợp lệ |
| 404 | `tx not found` | `/tx/estimate?hash=` trỏ tx chưa tồn tại | Chờ tx được thực thi rồi tra lại |
| 500 | `query panicked: <...>` | Query hợp đồng gây panic (đã `recover`) | Msg query sai schema/hợp đồng lỗi; kiểm input và logic contract |

> Quy ước chung: HTTP **200 ở `/tx/submit` chỉ nghĩa "đã vào mempool"**, *không* phải đã thực thi thành công. Kết quả thật xem ở `/tx/result` (`code`).

---

## 4. Lỗi tầng thực thi (executor) — thường HTTP 500

Phát ra từ `CosmosExecutor` ([executor/](../../../executor/)) và nổi lên client qua `err.Error()` (thường 500, một số 400). Đây cũng là lỗi xuất hiện khi **replay lúc khởi động** ([§1](#1-lỗi-khởi-động--log-error--thoát-exit-1)).

| `error` | Nguyên nhân | Cách fix |
|---------|-------------|----------|
| `executor not initialized` | Gọi simulate/query trước khi `InitChain` | Chờ node init xong (`/status` → `initialized:true`) |
| `executor already initialized with chain id "X"` | Init lần hai với chain id khác | Dùng đúng chain id; xoá data dir nếu muốn chain mới |
| `chain id is required` / `initial height must be > 0` | Tham số init thiếu/sai | Sửa cấu hình genesis/khởi tạo |
| `unexpected block height N (expected M)` | ev-node gửi block sai thứ tự | Lỗi đồng bộ — kiểm sequencer/full node; không tự gọi ExecuteTxs |
| `prev state root mismatch: expected X got Y` | State root không khớp giữa các node | Data dir lệch — resync từ DA, hoặc xoá state hỏng |
| `cannot finalize future block N, last executed M` | Finalize block chưa thực thi | Lỗi luồng ev-node; kiểm thứ tự gọi SetFinal |
| `cannot rollback to future height N (current: M)` | Rollback tới height tương lai | Chỉ rollback về height ≤ hiện tại |
| `invalid address` / `invalid contract address` | Bech32 sai định dạng | Kiểm địa chỉ (prefix + checksum) |
| `block height must be > 0` / `contract address is required` / `query msg cannot be empty` | Tham số đọc thiếu | Truyền đủ tham số |
| `simulate: <...>` / `wasm query panicked: <...>` | Lỗi khi mô phỏng/truy vấn | Kiểm tx/msg theo schema hợp đồng |
| `commit: <...>` / `init chain: <...>` / `finalize block: <...>` | Lỗi tầng Cosmos SDK BaseApp | Thường do state/đĩa; xem lỗi con bọc kèm |
| `persist *: <...>` (block, tx result, metadata) | Ghi đĩa thất bại (`open/write/fsync/rename`) | Kiểm quyền & dung lượng data dir; đĩa đầy? |
| `read/parse metadata.json`, `load blocks/tx results`, `decode persisted state root` | File persist hỏng khi replay | Data dir bị sửa/đứt giữa chừng — khôi phục backup hoặc xoá để chain lại |

---

## 5. Lỗi faucet

Faucet chỉ bật khi cấu hình treasury hợp lệ. Nguồn: [faucet.go](../../../cmd/cosmos-exec-grpc/faucet.go).

**Lúc khởi động** (làm `faucet config invalid` ở [§1](#1-lỗi-khởi-động--log-error--thoát-exit-1)):

| Lỗi con | Cách fix |
|---------|----------|
| `COSMOS_EXEC_TREASURY_PRIVKEY_HEX: <...>` | Đặt private key hex hợp lệ của ví treasury |
| `COSMOS_EXEC_TREASURY_AMOUNT: <...>` | Số dư khởi tạo treasury phải parse được |
| `COSMOS_EXEC_FAUCET_AMOUNT: <...>` / `must be > 0` | Lượng phát mỗi lần phải là số > 0 |
| `COSMOS_EXEC_FAUCET_GAS: <...>` | Gas faucet phải hợp lệ |
| `COSMOS_EXEC_FAUCET_COOLDOWN_SECONDS: <...>` | Cooldown phải là số giây hợp lệ |
| `build genesis with treasury balance: <...>` | Treasury/denom không dựng được genesis — kiểm cấu hình chain |

**Lúc chạy** `POST /faucet`:

| HTTP | `error` | Cách fix |
|------|---------|----------|
| 405 | `method not allowed` | Dùng POST |
| 400 | `addr query param is required` | Truyền `?addr=<bech32>` |
| 400 | `invalid bech32 addr: <...>` | Sửa địa chỉ ví |
| 400 | `refusing to fund the treasury itself` | Dùng địa chỉ khác treasury |
| 429 | `address funded recently, retry in <d>` | Chờ hết cooldown |
| 500 | `treasury account lookup: <...>` / `status: <...>` / `sign: <...>` | Lỗi nội bộ — kiểm treasury account & trạng thái chain |
| 400 | `submit: <...>` | Tx faucet bị mempool từ chối — xem lỗi con |

---

## 6. Lỗi thực thi tx (`code != 0`) — AnteHandler

Đây **không phải lỗi HTTP**: tx đã vào block nhưng thực thi thất bại. `/tx/result` trả `code != 0` kèm `log`. Nguồn: [app/](../../../app/) (AnteHandler tuỳ biến). Tham chiếu sâu: [troubleshooting.md](troubleshooting.md#3-tx-thực-thi-nhưng-code--0).

| Triệu chứng (`log`) | code | Nguyên nhân | Cách fix |
|---------------------|------|-------------|----------|
| `insufficient fee: got X, need at least Y (gas G * min price P)` | 13 | Phí đính kèm < `gas * COSMOS_EXEC_MIN_GAS_PRICE` | Ký lại với phí đủ; chạy `/tx/simulate` để lấy `gas_limit`+`fee` chuẩn |
| `fee payer address <...> does not exist: unknown address` | 9 | Ví trả phí chưa tồn tại on-chain (giao dịch đầu của account mới) | AutoCreateAccount xử lý account đầu; nếu vẫn lỗi, xin token từ faucet trước |
| `signature verification failed; please verify account number ...` | 4 | Sai account number/sequence khi ký | Đọc lại account number/sequence hiện tại rồi ký lại |
| `tx must be a FeeTx` / `tx must be a SigVerifiableTx` | (ErrTxDecode) | Tx không đúng dạng Cosmos chuẩn | Dựng tx bằng SDK builder, đừng tự ghép bytes |

> Quy tắc phí phụ thuộc `COSMOS_EXEC_MIN_GAS_PRICE`: **không đặt → chấp nhận phí 0** (mặc định dev permissionless); **đặt → bắt buộc phủ `gas*minGasPrice`**. Simulate (`/tx/simulate`) **không bao giờ** bị từ chối phí.

---

## 7. Lỗi validate cấu hình

`Config.Validate()` ([config/config.go:198+](../../../config/config.go#L198)) — gây `invalid config` ([§1](#1-lỗi-khởi-động--log-error--thoát-exit-1)):

| Lỗi | Cách fix |
|-----|----------|
| `listen_addr is required` | Đặt `--listen-addr` / `COSMOS_EXEC_LISTEN_ADDR` (vd `0.0.0.0:50051`) |
| `query_gas_max must be > 0` | `COSMOS_EXEC_QUERY_GAS_MAX` > 0 |
| `max_blob_size must be > 0` | `COSMOS_EXEC_MAX_BLOB_SIZE` > 0 |
| `max_store_total_size must be > 0` | `COSMOS_EXEC_MAX_STORE_SIZE` > 0 |

---

## 8. Lỗi phía SDK / client (không phải log server)

Khi gọi qua thư viện Go `sdk/cosmoswasm`, các lỗi sentinel sau hay gặp (chi tiết: [error-handling.md](error-handling.md)):

| Lỗi | Nguyên nhân | Cách fix |
|-----|-------------|----------|
| `ErrNotReachable` / `connection refused` | Server cosmos-exec chưa chạy / sai URL | Khởi động `cosmos-exec-grpc`; đặt đúng `EXEC_URL` (mặc định cổng 50051) |
| `context deadline exceeded` | Timeout chờ tx/kết nối | Tăng timeout; kiểm chain có sinh block không |
| `blob size ... exceeds max` | Blob vượt `MaxBlobSize` | Chia nhỏ/nén dữ liệu trước khi submit |
| `store full` / `ErrBlobStoreFull` | Store đạt `MaxStoreTotalSize` | Bật prune / tăng giới hạn / dọn data |

Ví dụ `my-counter` có hàm `friendly()` ([main.go:516](../../../sdk/cosmoswasm/examples/my-counter/main.go#L516)) bọc các lỗi này thành gợi ý dễ đọc — tham khảo khi tự viết client.

---

## 9. Cảnh báo (WARN) từ evcosmos / ev-node

Binary **evcosmos** (runtime ev-node) log ở mức `WRN` và `ERR`. Khác với lỗi cosmos-exec, **đa số WARN ở đây là "thông tin/tự phục hồi", không phải lỗi chí mạng** — node vẫn chạy tiếp. Dưới đây gom theo nhóm; cột **Mức** ở §9.1 phân biệt `WRN` và `ERR` (một số sự cố DA nổi lên dưới dạng `ERR` nhưng cùng nguyên nhân với WARN cạnh nó).

### 9.1. DA (sẵn sàng dữ liệu)

| Mức | Thông điệp | Nguyên nhân | Cách xử lý |
|-----|------------|-------------|------------|
| WRN | `da_start_height is not set in genesis.json, ask your chain developer` | Full node, `da_start_height = 0` trong genesis ([run.go:66](../../../../../apps/cosmos-wasm/cmd/run.go#L66); field `da_start_height` ở [genesis.go:20](../../../../../pkg/genesis/genesis.go#L20)) | Đặt `da_start_height` trong `genesis.json` để node bắt đầu quét DA từ đúng height (xem ví dụ + đường dẫn file ngay dưới bảng). Nếu để 0, node phải dò/sync từ height 1 → khởi động chậm. **Không chí mạng.** |
| **ERR** | `failed to get blobs error="height is equal to 0" component=da_client height=0` | Có thành phần xin blob ở **DA height = 0**, nhưng DA (Celestia) đánh số height từ 1 nên từ chối. Gốc rễ y hệt dòng WRN trên: `da_start_height = 0` **và** auto-detect head DA cũng thất bại/trả 0, nên đường lấy blob rơi về height 0. Log tại [da/client.go:249](../../../../../block/internal/da/client.go#L249); height khởi tạo từ `DAStartHeight` ([syncer.go:123](../../../../../block/internal/syncing/syncer.go#L123)) | **Fix gốc:** đặt `da_start_height ≥ 1` trong `genesis.json` (ví dụ dưới). **Fix phụ:** đảm bảo DA RPC reachable để auto-detect head chạy được (`GetLatestDAHeight`, [syncer.go:214](../../../../../block/internal/syncing/syncer.go#L214)) — khi đó dù để 0 node vẫn tự nhảy tới head thay vì query height 0. Nếu chỉ thấy 1–2 dòng lúc khởi động rồi hết → vô hại; nếu lặp liên tục → chain đang kẹt không quét được DA |
| WRN | `failed to auto-detect DA latest height, will sync from height 1` | Không hỏi được latest height của DA ([syncer.go:217](../../../../../block/internal/syncing/syncer.go#L217)) | Sẽ sync từ đầu (chậm). Kiểm DA node có reachable + đúng RPC không. Đây cũng là tiền đề dẫn tới ERR `height is equal to 0` ở trên |
| WRN | `failed to fetch latest DA height` | Một lần gọi DA RPC lỗi | Tạm thời; nếu lặp liên tục → kiểm kết nối/cấu hình DA |
| WRN | `DA subscription failed, reconnecting` | Mất subscribe tới DA | Tự reconnect; kiểm mạng/DA node nếu lặp |
| WRN | `failed to create websocket DA client, falling back to HTTP client` | Không mở được websocket tới DA | Vẫn chạy bằng HTTP; nếu cần WS, kiểm endpoint hỗ trợ WS |
| WRN | `Get: blob not found` | Đọc blob chưa có/đã prune tại height đó | Thường vô hại lúc đang sync; nếu dữ liệu thật sự mất → kiểm DA retention |

**`genesis.json` nằm ở đâu:** `<home>/config/genesis.json` ([GenesisPath](../../../../../pkg/genesis/io.go#L14)) — vd `.cosmos-wasm-runner/nodes/evcosmos-fullnode/config/genesis.json` khi chạy qua run-script. Sequencer tạo genesis rồi **copy sang full node**, nên sửa ở nguồn (sequencer) để cả hai khớp.

**Ví dụ `genesis.json` (chú ý `da_start_height`):**

```json
{
  "chain_id": "cosmos-wasm-devnet",
  "start_time": "2026-06-28T00:00:00Z",
  "initial_height": 1,
  "proposer_address": "Fh...==",
  "da_start_height": 692000,
  "da_epoch_forced_inclusion": 50
}
```

- `da_start_height` = height của **Celestia** nơi blob **đầu tiên** của rollup được publish. Chain mới: đặt bằng **head height hiện tại của Celestia** lúc tạo genesis (vd lấy từ `celestia header sync-state` hoặc RPC head). Để `0` → node quét từ 1 (rất chậm trên DA chạy lâu) và có thể đẻ ERR `height is equal to 0` ở trên.
- `da_epoch_forced_inclusion` = số block DA tính là 1 epoch cho forced-inclusion (mặc định 50, [genesis.go:39](../../../../../pkg/genesis/genesis.go#L39)). Không liên quan lỗi DA height, nhưng có mặt trong file.

> File mẫu đầy đủ: [genesis.example.json](genesis.example.json). Giải thích từng trường + cách lấy `da_start_height` đúng + workflow `evcosmos init`: [genesis-reference.md](genesis-reference.md).

### 9.2. Đồng bộ / catch-up

| WRN | Nguyên nhân | Cách xử lý |
|-----|-------------|------------|
| `entering catch-up mode: replaying missed epochs with forced inclusion txs only` | Sequencer tụt sau DA, phải replay các epoch bị lỡ — chỉ dùng forced-inclusion txs ([sequencer.go:562](../../../../../pkg/sequencers/single/sequencer.go#L562)) | **Thông tin**, không phải lỗi. Node đang đuổi kịp DA. Theo dõi `checkpoint_da_height` tiến dần tới `latest_da_height` là ổn. Nếu kẹt mãi → kiểm DA reachable và tốc độ mạng |
| `catchup error, backing off` | Một vòng catch-up lỗi, sẽ thử lại | Tự backoff; nếu lặp dài → xem lỗi DA/executor đi kèm |
| `failed to get store height during catchup` | Đọc height từ store lỗi giữa lúc catch-up | Kiểm data dir/quyền; thường tự qua |

### 9.3. P2P

| WRN | Nguyên nhân | Cách xử lý |
|-----|-------------|------------|
| `P2P handler failed to process height` | Xử lý block nhận qua P2P lỗi | Thường tự lấy lại từ DA; nếu lặp → kiểm peer |
| `discarding inconsistent block from P2P` | Block từ peer không nhất quán (hash/sig sai) | Bảo vệ đúng: bỏ block xấu; nếu nhiều → có peer lỗi/độc hại |
| `apply channel full, dropping message` / `dropping event for slow subscriber` | Tiêu thụ chậm hơn sản xuất, buffer đầy | Tải cao tạm thời; nếu thường xuyên → tài nguyên node thiếu |
| `failed to broadcast` | Phát block/tx ra P2P lỗi | Tạm thời; kiểm kết nối peer |

### 9.4. Strict mode (lọc envelope)

| WRN | Nguyên nhân | Cách xử lý |
|-----|-------------|------------|
| `strict mode is enabled, rejecting non-envelope blob` | Bật strict, blob không đúng định dạng envelope bị loại | Đúng thiết kế. Tắt strict nếu muốn chấp nhận blob cũ/ngoài chuẩn |
| `strict mode: rejecting block that is not a fully valid envelope` | Block không phải envelope hợp lệ đầy đủ | Như trên |

### 9.5. Tự phục hồi (best-effort) — thường bỏ qua được

| WRN | Ý nghĩa |
|-----|---------|
| `failed to load/clear cache from disk, starting with empty cache` | Cache đĩa hỏng/thiếu → dựng lại cache rỗng, chạy bình thường (chậm hơn lúc đầu) |
| `failed to create envelope cache, continuing without caching` | Không bật được cache → chạy không cache |
| `failed to create batching strategy, using time-based default` | Dùng chiến lược gộp mặc định theo thời gian |
| `failed to filter transactions, proceeding with unfiltered` | Bỏ qua bước lọc tx |
| `failed to get execution info, proceeding without gas limit` | Không lấy được thông tin execution → chạy không đặt gas limit |
| `unregistered failure reason, metric not recorded` | Lý do lỗi chưa khai báo metric → chỉ thiếu số liệu, không ảnh hưởng chạy |

### 9.6. Lúc shutdown — vô hại

| WRN | Ý nghĩa |
|-----|---------|
| `submitter shutdown timed out waiting for goroutines, proceeding anyway` | Hết thời gian chờ goroutine khi tắt → tắt luôn |
| `timed out waiting for raft messages to land during shutdown` | Chờ raft message khi tắt quá hạn |
| `timeout draining height events during shutdown` | Xả nốt event height khi tắt quá hạn |

> Tóm tắt: WARN của evcosmos/ev-node hiếm khi cần can thiệp. Ưu tiên xử lý hai nhóm **§9.1 (DA)** và **§9.2 (catch-up)** nếu chúng lặp lại liên tục hoặc `checkpoint_da_height` không tăng — đó là dấu hiệu node không theo kịp/không tới được DA. Các nhóm còn lại (cache, shutdown, strict) hầu hết là hành vi đúng hoặc tự phục hồi.

---

## Tra cứu nhanh theo HTTP status

- **400** → input sai: JSON/tx/hash/tham số. Sửa request.
- **401** → thiếu/sai auth token.
- **403** → node read-only, từ chối ghi.
- **404** → không tìm thấy tài nguyên (tx/route).
- **405** → sai HTTP method.
- **413/“failed to read body”** → body quá lớn.
- **429** → rate limit hoặc faucet cooldown.
- **500** → lỗi nội bộ executor/persist/panic. Xem log server (`ERROR`) để biết lỗi con.
- **200 + `code != 0`** → tx thực thi thất bại, xem [§6](#6-lỗi-thực-thi-tx-code--0--antehandler).
