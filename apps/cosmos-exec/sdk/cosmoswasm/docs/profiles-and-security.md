# Profiles & Tuỳ chọn vận hành cosmos-exec-grpc

Tài liệu này giải thích chi tiết bảng cấu hình theo từng profile (`dev`, `test`, `prod`) của server `cosmos-exec-grpc`, cùng với mã nguồn liên quan và cách từng tuỳ chọn hoạt động dưới mui.

## Mục lục

- 1. Bảng tổng quan
- 2. Auth (Bearer Token)
- 3. Persistence (lưu state ra đĩa)
- 4. Rate limit
- 5. CORS
- 6. Faucet (treasury + cấp token test)
- 7. Mapping nhanh: tuỳ chọn → code
- 8. Recipe khởi động theo profile
- 9. Chạy cả chain (sequencer + full node) theo profile
- 10. Kiểm tra nhanh khi triển khai

## 1. Bảng tổng quan

| Profile | Auth | Persistence | Rate limit | CORS               | Faucet              |
| ------- | ---- | ----------- | ---------- | ------------------ | ------------------- |
| `dev`   | Tắt  | Tắt         | Không      | `*` (mọi origin)   | Bật mặc định        |
| `test`  | Tắt  | Tắt         | Không      | `*` (mọi origin)   | Tắt                 |
| `prod`  | Bật  | Bật         | Có (100 RPS)| Restricted (set ENV)| Tuỳ chọn (qua ENV) |

> Nguồn dữ liệu: [config/config.go](../config/config.go) — hai hàm [`DefaultConfig`](../config/config.go#L64) và [`ForProfile`](../config/config.go#L91); việc thực thi nằm trong [middleware.go](../cmd/cosmos-exec-grpc/middleware.go), [faucet.go](../cmd/cosmos-exec-grpc/faucet.go) và [main.go](../cmd/cosmos-exec-grpc/main.go).

Profile là khởi điểm: lệnh `--profile=...` chọn preset, sau đó biến môi trường `COSMOS_EXEC_*` ghi đè, cuối cùng cờ CLI ưu tiên cao nhất.

```text
preset (ForProfile)  ─►  ENV (LoadFromEnv)  ─►  CLI flags  ─►  Validate
```

Xem trình tự ưu tiên ở [main.go:46-66](../cmd/cosmos-exec-grpc/main.go#L46-L66).

---

## 2. Auth (xác thực bằng Bearer Token)

### Mặc định theo profile

- **dev / test**: `AuthToken = ""` → tắt. Mọi request POST đều được phép.
- **prod**: `AuthToken = ""` trong preset, **nhưng** thường set qua ENV `COSMOS_EXEC_AUTH_TOKEN`. Khi có giá trị → bật.

### Cách bật

```bash
export COSMOS_EXEC_AUTH_TOKEN="super-secret-string"
./cosmos-exec-grpc --profile prod
```

Nguồn nạp ENV: [config.go:162-164](../config/config.go#L162-L164).

### Cơ chế kiểm tra (cách hoạt động)

Logic nằm trong [middleware.go:54-60](../cmd/cosmos-exec-grpc/middleware.go#L54-L60):

```go
if cfg.AuthToken != "" && r.Method == http.MethodPost {
    auth := r.Header.Get("Authorization")
    if !strings.HasPrefix(auth, "Bearer ") ||
       strings.TrimPrefix(auth, "Bearer ") != cfg.AuthToken {
        writeJSON(w, http.StatusUnauthorized, ...)
        return
    }
}
```

Điểm quan trọng:

1. **Chỉ chặn POST** — GET (queries) vẫn mở để dapp/explorer đọc state mà không cần token.
2. Phải có header `Authorization: Bearer <token>`. Sai prefix hoặc sai token → trả `401 Unauthorized`.
3. So sánh là **exact match** (chuỗi). Không hash, không HMAC — coi token như "API key dùng chung".

### Khi nào cần Auth

| Tình huống | Có cần token? |
| ---------- | ------------- |
| Public RPC cho dapp đọc balance, query smart contract | Không (chỉ GET) |
| Endpoint POST `/tx/submit` cho user submit tx | Có thể đặt token để chỉ frontend tin cậy gọi được, tránh spam |
| `/exec/rollback`, `/exec/prune` (ops nội bộ) | Bắt buộc (đã là POST → có token) |

### Hạn chế cần biết

- Token là **plain string** trong ENV → đừng log ra.
- Không có rate limit theo token, chỉ theo IP (xem mục 4).
- Mọi POST đều dùng chung 1 token → không phân quyền per-user. Nếu cần multi-tenant, đặt API gateway trước.

---

## 3. Persistence (lưu state ra đĩa)

### Hai loại persistence khác nhau

#### 3.1 Database chính (LevelDB)

Bộ store của Cosmos SDK app chứa state (account, balance, contract storage…).

- **dev**: `InMemory = false`, mở LevelDB ở `Home/data` (mặc định `.cosmos-exec-grpc/data`).
- **test**: `InMemory = true` → dùng `dbm.NewMemDB()` ([main.go:258-260](../cmd/cosmos-exec-grpc/main.go#L258-L260)). Tốc độ cao, không khoá file, mất sạch khi tắt.
- **prod**: LevelDB, có khoá file. Lỗi `resource temporarily unavailable` ⇒ một process khác đang giữ DB; xem [main.go:266-274](../cmd/cosmos-exec-grpc/main.go#L266-L274).

```bash
# Test thoải mái nhiều process song song:
./cosmos-exec-grpc --profile test

# Prod, file-backed, persistent:
./cosmos-exec-grpc --profile prod --home /var/lib/cosmos-exec
```

#### 3.2 Persistent tx/block log (jsonl)

Lưu kết quả tx và block để khởi động lại không mất "lịch sử" view. Tệp nằm cùng `data_dir`:

- `metadata.json` — chain-id, state root, height hiện tại, height finalized.
- `tx_results.jsonl` — append-only, mỗi dòng = 1 `TxExecutionResult`.
- `blocks.jsonl` — append-only, mỗi dòng = 1 `BlockInfo`.

Tham khảo struct ở [executor/persist.go:24-54](../executor/persist.go#L24-L54).

Tự động bật/tắt:

- **dev**: trong `DefaultConfig` `PersistTxResults = false`. Nhưng `main.go` có một bước ép bật:
  ```go
  if !cfg.InMemory {
      cfg.PersistTxResults = true
  }
  ```
  ([main.go:119-121](../cmd/cosmos-exec-grpc/main.go#L119-L121)) → ngầm bật bất cứ khi nào không dùng in-memory DB.

- **test**: `InMemory = true` ⇒ điều kiện trên thất bại ⇒ tx/block log không ghi đĩa.

- **prod**: preset cũng đặt `PersistTxResults = true` rõ ràng ([config.go:103-104](../config/config.go#L103-L104)).

### Cơ chế ghi (cách hoạt động)

Khi `PersistTxResults` bật, executor được wrap thêm option `WithPersistence(dir, &persistErr)` ([main.go:122-129](../cmd/cosmos-exec-grpc/main.go#L122-L129)):

1. **Khởi động**: đọc lại 3 file trên, replay vào RAM để khôi phục view tx/block đã thực thi.
2. **Trong vận hành**: mỗi tx commit → append 1 dòng JSON vào `tx_results.jsonl`. Block tương tự. Mỗi lần state thay đổi → ghi đè `metadata.json`.
3. **Khoá**: `sync.Mutex` đảm bảo không có 2 goroutine ghi cùng lúc ([persist.go:25-30](../executor/persist.go#L25-L30)).
4. **Tắt sạch**: `cosmosExecutor.Close()` đóng file ([main.go:234](../cmd/cosmos-exec-grpc/main.go#L234)).

### Khi nào nên tắt persistence

- Test integration cần khởi động/tắt liên tục (đã tự tắt vì `InMemory=true`).
- Smoke test trên CI.
- Chạy nhiều instance song song trên cùng máy (mỗi instance cần `--home` khác hoặc `--in-memory`).

### Khi nào cần bật

- Bất kỳ deploy thật nào — bạn cần block explorer hoặc UI lịch sử tx hoạt động sau restart.
- Debug local mà muốn giữ lại tx đã chạy để xem lại.

---

## 4. Rate limit (giới hạn tốc độ)

### Mặc định

- **dev / test**: `RateLimitRPS = 0` → tắt hoàn toàn.
- **prod**: `RateLimitRPS = 100` ([config.go:108](../config/config.go#L108)) → mỗi IP tối đa 100 request/giây.

Có thể override:

```bash
export COSMOS_EXEC_RATE_LIMIT_RPS=50  # 50 rps mỗi IP
```

### Thuật toán: token bucket per-IP

Code ở [middleware.go:94-136](../cmd/cosmos-exec-grpc/middleware.go#L94-L136):

```go
type tokenBucket struct {
    tokens   float64
    lastTime time.Time
}

func (l *ipRateLimiter) allow(ip string) bool {
    // refill: tokens += elapsed * rps
    // capped at rps (burst = rps)
    if b.tokens < 1 { return false }
    b.tokens--
    return true
}
```

Tóm tắt:

1. Mỗi IP được cấp 1 "xô" với dung tích = `RPS` token.
2. Token tự đầy lại theo thời gian (`elapsed * RPS` mỗi giây), tối đa bằng dung tích.
3. Mỗi request tiêu thụ 1 token. Hết token → trả `429 Too Many Requests`.

→ Cho phép **burst** đúng bằng `RPS` (ví dụ 100 req trong 100ms đầu), sau đó tự đáy ổn định lại theo tốc độ tinh đến `RPS req/s`.

### Cách lấy IP

[middleware.go:80-91](../cmd/cosmos-exec-grpc/middleware.go#L80-L91):

```go
if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
    return strings.TrimSpace(parts[0])  // IP đầu trong chain
}
return r.RemoteAddr[:lastColon]          // fallback
```

Lưu ý: ưu tiên `X-Forwarded-For` để chạy đúng phía sau reverse proxy / load balancer. Nếu chưa có proxy, đảm bảo client không tự set XFF (có thể giả mạo IP để bypass).

### Hạn chế

- Bộ đếm trong RAM, mỗi instance giữ riêng map IP. Đa-instance cần đồng bộ qua proxy ngoài (Nginx limit_req, Envoy rate_limit).
- Map IP **không bao giờ bị xoá** trong code hiện tại — long-tail traffic có thể làm map phình ra. Nếu deploy long-running cần giám sát mem, hoặc thêm GC định kỳ.

---

## 5. CORS (cross-origin)

### Mặc định

- **dev / test**: `CORSAllowOrigin = "*"` → mọi domain web đều fetch được.
- **prod**: `CORSAllowOrigin = ""` rỗng trong preset ([config.go:107](../config/config.go#L107)). Operator **phải** set qua ENV trước khi expose ra public.

```bash
export COSMOS_EXEC_CORS_ORIGIN="https://app.mychain.io"
```

### Cách hoạt động

Middleware luôn set 3 header CORS cho mọi response ([middleware.go:32-40](../cmd/cosmos-exec-grpc/middleware.go#L32-L40)):

```go
origin := cfg.CORSAllowOrigin
if origin == "" {
    origin = "*"          // fallback nếu để rỗng
}
w.Header().Set("Access-Control-Allow-Origin", origin)
w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
w.Header().Set("Access-Control-Max-Age", "86400")  // cache preflight 24h
```

Preflight request (`OPTIONS`) được handle riêng và trả `204 No Content` ngay ([middleware.go:42-45](../cmd/cosmos-exec-grpc/middleware.go#L42-L45)).

### Cảnh báo cho prod

> Nếu để rỗng trong prod, fallback `origin = "*"` vẫn chạy — đây là **rủi ro**: bất cứ web nào cũng fetch được. Đặt rõ origin (hoặc danh sách qua reverse proxy) trước khi public.

Nếu cần nhiều origin: hiện code chỉ chấp nhận **1 chuỗi**. Multi-origin nên giải quyết tại tầng proxy (Nginx echo `Origin` header nếu match whitelist), không trong app.

---

## 6. Faucet (treasury + cấp token test)

### Trigger bật

Faucet bật khi ENV `COSMOS_EXEC_TREASURY_PRIVKEY_HEX` được set. Không liên quan trực tiếp tới profile — nhưng:

- **dev**: dev script thường tự tạo treasury → faucet bật theo mặc định.
- **test**: integration tests thường không set → tắt (kiểm soát thủ công balance ở genesis).
- **prod**: tuỳ chọn — chỉ bật cho testnet/devnet public, không nên cho mainnet.

Đọc cờ tại [main.go:98-115](../cmd/cosmos-exec-grpc/main.go#L98-L115), config tại [faucet.go:40-90](../cmd/cosmos-exec-grpc/faucet.go#L40-L90).

### Các biến môi trường

| ENV | Default | Mục đích |
| --- | ------- | -------- |
| `COSMOS_EXEC_TREASURY_PRIVKEY_HEX` | (bắt buộc) | secp256k1 hex key của tài khoản treasury |
| `COSMOS_EXEC_TREASURY_AMOUNT` | `1000000000000ustake` | Số dư genesis cấp cho treasury |
| `COSMOS_EXEC_FAUCET_AMOUNT` | `1000000ustake` | Số token gửi mỗi lần `/faucet` |
| `COSMOS_EXEC_FAUCET_GAS` | `200000` | Gas limit cho tx MsgSend của faucet |
| `COSMOS_EXEC_FAUCET_COOLDOWN_SECONDS` | `3600` (1h) | Khoảng cách tối thiểu giữa 2 lần cấp cho cùng địa chỉ |

### Cách hoạt động (3 bước)

#### Bước A — Genesis

Khi bật, executor được wrap với một genesis option nạp `genesisAmt` cho `treasury` ([faucet.go:95-103](../cmd/cosmos-exec-grpc/faucet.go#L95-L103)):

```go
genesis, _ := app.GenesisWithBalances([]banktypes.Balance{
    {Address: fc.treasury, Coins: fc.genesisAmt},
})
return executor.WithGenesis(genesis), nil
```

Không có bước này thì treasury balance = 0 → mọi `/faucet` thất bại "insufficient funds" khi fee policy bật.

#### Bước B — Xử lý request

`POST /faucet?addr=cosmos1...` (cũng accept GET) — [faucet.go:139-218](../cmd/cosmos-exec-grpc/faucet.go#L139-L218):

1. Validate địa chỉ bech32. Chặn nếu addr trùng treasury.
2. Check cooldown theo `lastSeen` map → trả `429` nếu chưa hết hạn.
3. Lấy account info của treasury (account_number + sequence).
4. **Pick sequence** = max(on-chain seq, `nextSeq` đã track) — quan trọng vì mempool chưa được commit nên seq on-chain có thể thấp hơn thực tế.
5. Build & sign tx `MsgSend(treasury → addr, payout)` với fee = `feeForGas(gas)`.
6. Inject tx vào mempool.
7. Cập nhật `nextSeq = seq+1` và `lastSeen[addr] = now`.

#### Bước C — Concurrency

Tất cả logic trong `faucetHandler` chạy dưới `fc.mu.Lock()` ([faucet.go:160-161](../cmd/cosmos-exec-grpc/faucet.go#L160-L161)) → giữ thứ tự sequence khi nhiều request đồng thời. Mặt trái: faucet là **single-threaded**, throughput thấp — nhưng đó là chấp nhận được cho mục đích cấp token test.

### Fee policy tương tác với faucet

Faucet không phá luật fee. Nếu chain bật `COSMOS_EXEC_MIN_GAS_PRICE`, faucet tự tính fee đúng theo công thức ([faucet.go:109-127](../cmd/cosmos-exec-grpc/faucet.go#L109-L127)):

```go
amt = ceil(gas * price)         // ceil để không under-pay
fee = sdk.NewCoins(coin{denom, amt})
```

→ Treasury phải đủ token để trả phí cho chính tx faucet, ngoài số token gửi user.

### Khi không nên bật trên prod

- Mainnet — token có giá trị, faucet sẽ bị abuse dù có cooldown.
- Production internal — không cần đường vào ngoài thiết kế.
- Bật chỉ cho devnet/testnet và đặt sau reverse proxy có thêm captcha/IP whitelist.

---

## 7. Mapping nhanh: tuỳ chọn → code

| Tuỳ chọn         | Field config            | ENV                        | Áp dụng tại                          |
| ---------------- | ----------------------- | -------------------------- | ------------------------------------ |
| Auth token       | `AuthToken`             | `COSMOS_EXEC_AUTH_TOKEN`   | [middleware.go:54](../cmd/cosmos-exec-grpc/middleware.go#L54) |
| Persistence DB   | `InMemory`              | `COSMOS_EXEC_IN_MEMORY`    | [main.go:257-277](../cmd/cosmos-exec-grpc/main.go#L257-L277) |
| Persistence log  | `PersistTxResults`      | `COSMOS_EXEC_PERSIST_TX_RESULTS` | [main.go:117-129](../cmd/cosmos-exec-grpc/main.go#L117-L129) |
| Rate limit       | `RateLimitRPS`          | `COSMOS_EXEC_RATE_LIMIT_RPS` | [middleware.go:62-69](../cmd/cosmos-exec-grpc/middleware.go#L62-L69) |
| CORS             | `CORSAllowOrigin`       | `COSMOS_EXEC_CORS_ORIGIN`  | [middleware.go:32-40](../cmd/cosmos-exec-grpc/middleware.go#L32-L40) |
| Faucet           | (chỉ qua ENV)           | `COSMOS_EXEC_TREASURY_PRIVKEY_HEX` + nhóm `COSMOS_EXEC_FAUCET_*` | [faucet.go](../cmd/cosmos-exec-grpc/faucet.go) |
| Body size limit  | `MaxRequestBodyBytes`   | `COSMOS_EXEC_MAX_BODY_BYTES` | [middleware.go:72-74](../cmd/cosmos-exec-grpc/middleware.go#L72-L74) |
| Read-only mode   | `ReadOnlyMode`          | `COSMOS_EXEC_READ_ONLY`    | [middleware.go:48-51](../cmd/cosmos-exec-grpc/middleware.go#L48-L51) |

---

## 8. Recipe khởi động theo profile

### dev (mặc định)

```bash
./cosmos-exec-grpc
# = --profile dev
# - LevelDB ở .cosmos-exec-grpc/data
# - Persistence bật (vì không in-memory)
# - CORS "*", không auth, không rate limit
# - Faucet bật nếu set COSMOS_EXEC_TREASURY_PRIVKEY_HEX
```

### test (integration)

```bash
./cosmos-exec-grpc --profile test
# - DB in-memory, listen 127.0.0.1:0 (random port)
# - Log level error
# - Không persist tx/block
# - Phù hợp khởi tạo trong test Go
```

### prod (tối thiểu)

```bash
export COSMOS_EXEC_AUTH_TOKEN="$(openssl rand -hex 32)"
export COSMOS_EXEC_CORS_ORIGIN="https://app.mychain.io"
./cosmos-exec-grpc --profile prod \
                   --home /var/lib/cosmos-exec \
                   --address 0.0.0.0:50051
# - LevelDB ở /var/lib/cosmos-exec/data
# - Persistence tx/block bật
# - Rate limit 100 RPS/IP
# - CORS chỉ origin chỉ định
# - Auth token bắt buộc cho POST
# - Faucet TẮT (không set treasury key)
```

---

## 9. Chạy cả chain (sequencer + full node) theo profile

Các mục 1–8 ở trên nói về **một** server `cosmos-exec-grpc` chạy riêng. Khi dựng một rollup hoàn chỉnh, ta dùng orchestrator [scripts/run-cosmos-wasm-nodes.go](../../../scripts/run-cosmos-wasm-nodes.go) — nó bật cả stack trên cùng host:

```text
2× evcosmos          (sequencer = aggregator, full node = non-aggregator)
2× cosmos-exec-grpc  (1 execution VM cho mỗi node)
```

Orchestrator có cờ `--profile` **riêng** và **tự truyền `--profile` xuống cả hai `cosmos-exec-grpc`** (xem `startExecutionServices`). Hai tiến trình executor được spawn **kế thừa biến môi trường của shell**, nên mọi `COSMOS_EXEC_*` bạn export trước khi chạy đều áp dụng cho chúng.

### 9.1 Hai profile ở tầng chain

Ở tầng chain chỉ có **`dev`** và **`prod`**. Profile **`test`** là của riêng executor (DB in-memory, dùng khi khởi tạo trong test Go) — **không** dùng để chạy chain; orchestrator sẽ báo lỗi nếu truyền `--profile test`.

| | `dev` | `prod` |
| --- | --- | --- |
| `--clean-on-start` mặc định | `true` (xoá sạch state mỗi lần chạy) | `false` (giữ state → restart **resume** từ height cũ) |
| Passphrase signer | tự tạo `"secret"`, xoá khi thoát | **bắt buộc** file thật, **không** xoá khi thoát |
| Profile của `cosmos-exec-grpc` | `dev` (CORS `*`, no auth) | `prod` (auth/CORS/rate-limit như mục 2–5) |
| Mục đích | dev local, làm lại từ đầu nhanh | vận hành thật, state bền vững |

> Mặc định prod tôn trọng lựa chọn tường minh: nếu bạn vẫn muốn re-init (ví dụ đổi chain-id hoặc đổi passphrase), truyền thẳng `--clean-on-start=true`.

### 9.2 Cần config những gì

**(a) DA layer** — đọc từ `.env` ở **gốc repo** (orchestrator tự nạp):

| ENV | Bắt buộc | Ý nghĩa |
| --- | --- | --- |
| `DA_BRIDGE_RPC` (hoặc `DA_RPC`) | Có | Endpoint RPC của Celestia node mà các node ev-node nói chuyện |
| `DA_AUTH_TOKEN` | Theo DA | Bearer token cho DA RPC |
| `DA_NAMESPACE` | Không (default `rollup`) | Namespace ghi blob lên DA |

**(b) Passphrase signer** (chỉ prod) — một trong:

```bash
--passphrase-file /path/to/passphrase.txt      # khuyến nghị
# hoặc
export EVCOSMOS_PASSPHRASE_FILE=/path/to/passphrase.txt
# hoặc (ghi giá trị trực tiếp)
export EVCOSMOS_PASSPHRASE="..."
```

Thiếu cả ba ở prod → orchestrator dừng với lỗi rõ ràng (không bao giờ tự dùng `"secret"` cho prod).

**(c) Config executor cho prod** — export trước khi chạy, chúng được kế thừa xuống cả hai `cosmos-exec-grpc`:

```bash
export COSMOS_EXEC_AUTH_TOKEN="$(openssl rand -hex 32)"
export COSMOS_EXEC_CORS_ORIGIN="https://app.mychain.io"
# (tuỳ chọn) COSMOS_EXEC_RATE_LIMIT_RPS, COSMOS_EXEC_MIN_GAS_PRICE, ...
```

**(d) Tuỳ chọn khác**: `--chain-id`, `--block-time`, `--forced-inclusion-namespace` + `--forced-inclusion-epoch`.

**Cổng mặc định** (đổi trong file orchestrator nếu cần):

| | Sequencer | Full node |
| --- | --- | --- |
| evcosmos JSON-RPC | `38331` | `48331` |
| cosmos-exec gRPC | `50051` | `50052` |
| libp2p | `7860` | `7861` |

### 9.3 Chạy — profile `dev`

```bash
# Cần .env ở gốc repo với DA_BRIDGE_RPC / DA_AUTH_TOKEN / DA_NAMESPACE
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go
# = --profile dev: clean-on-start=true, passphrase "secret",
#   cả 2 cosmos-exec-grpc chạy profile dev
```

### 9.4 Chạy — profile `prod` (native, không Docker)

```bash
# 1) Passphrase thật
openssl rand -hex 32 > /secure/evcosmos-pass.txt && chmod 600 /secure/evcosmos-pass.txt

# 2) Config executor (kế thừa xuống 2 node)
export COSMOS_EXEC_AUTH_TOKEN="$(openssl rand -hex 32)"
export COSMOS_EXEC_CORS_ORIGIN="https://app.mychain.io"

# 3) Bật stack — KHÔNG truyền --clean-on-start (prod mặc định = false)
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go \
    --profile prod \
    --passphrase-file /secure/evcosmos-pass.txt \
    --chain-id my-chain-1
```

State sống dưới `.cosmos-wasm-runner/nodes/` và **sống sót qua restart**: dừng (Ctrl-C) rồi chạy lại cùng lệnh → orchestrator skip init (signer đã có) và resume từ height đã lưu — đúng nghĩa "ghi đĩa" của profile prod (mục 3).

### 9.5 Chạy nền dài hạn (không trông coi)

Orchestrator chạy **fail-fast** (một process con chết → cả stack dừng, không tự restart) và block ở foreground. Với dev/thesis/single-host thì vậy là đủ. Nếu cần chạy nền lâu dài, bọc lệnh ở mục 9.4 trong một service `systemd` với `Restart=always` (hoặc `tmux`/`nohup` cho nhu cầu đơn giản) — không cần Docker.

---

## 10. Kiểm tra nhanh khi triển khai

- [ ] `--profile prod` → có set `COSMOS_EXEC_AUTH_TOKEN` chưa?
- [ ] `COSMOS_EXEC_CORS_ORIGIN` đã trỏ đúng domain frontend chưa? (đừng để fallback `*`)
- [ ] `--home` trỏ tới volume bền (không phải `/tmp`)?
- [ ] Reverse proxy đã chuyển `X-Forwarded-For` để rate limit đúng theo client?
- [ ] Treasury private key (nếu bật faucet) chỉ tồn tại trên devnet/testnet, không log?
- [ ] Healthcheck đường `/healthz` và `/ready` đã được gắn vào load balancer?
