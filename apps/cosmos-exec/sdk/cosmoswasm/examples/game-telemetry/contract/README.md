# telemetry-registry — game contract cho ví dụ game-telemetry

Contract CosmWasm **match-arena**: một game on-chain dùng mô hình blob-first.
Telemetry từng frame và replay cả trận được đẩy lên Celestia (off-chain); chỉ
commitment / Merkle root được ghi on-chain, gắn vào từng **match**. Client đọc
contract để lấy các con trỏ `(height, commitment)` rồi kéo data thật từ Celestia.

## Source ở đâu

Mã nguồn Rust (CosmWasm 2.0) nằm trong workspace **cw-plus**, không trong repo Go:

```
~/code/cw-plus/contracts/telemetry-registry/
    Cargo.toml
    src/{lib,contract,msg,state,error}.rs
```

Thư mục `./artifacts/` ở đây chỉ là **chỗ thả `.wasm`** đã build.

## Luồng game

```
RegisterPlayer → CreateMatch → JoinMatch → StartMatch
   → RecordTelemetry* (mỗi frame, blob-first)
   → RecordReplay     (Merkle root của replay)
   → FinishMatch (winner + scores)  → Leaderboard
```

## Execute messages

```jsonc
{ "register_player": { "handle": "demo-bot" } }
{ "create_match": {} }                                  // event "match_id"
{ "join_match":  { "match_id": 1 } }
{ "start_match": { "match_id": 1 } }                     // host only

// blob-first: commitment/height từ BlobClient.SubmitBlob
{ "record_telemetry": { "match_id":1, "commitment":"<hex>", "height":123, "namespace":"0x.." } }
// blob-first: root/height/count từ BlobClient.SubmitBatch
{ "record_replay":    { "match_id":1, "root":"<hex>", "height":123, "count":5, "namespace":"0x.." } }

{ "finish_match": { "match_id":1, "winner":"<addr>", "scores":[{"player":"<addr>","score":100}] } }

// admin
{ "set_max_players": { "max_players_per_match": 8 } }
{ "transfer_admin":  { "new_admin": "<addr>" } }
```

`height`/`namespace`/`tag` optional (SDK bỏ field rỗng/0). Ghi `height` vì
retrieve từ Celestia cần đủ `(height, namespace, commitment)`.

## Query messages

```jsonc
{ "config": {} }
{ "player": { "address": "<addr>" } }                   // → Option<PlayerProfile>
{ "match":  { "match_id": 1 } }                          // → Option<Match>
{ "list_matches": { "status": "active", "limit": 20 } } // status optional
{ "match_telemetry": { "match_id": 1 } }                 // → frames[] + replay_root (con trỏ Celestia)
{ "leaderboard": { "limit": 10 } }                       // → top theo wins, total_score
{ "stats": {} }                                          // → total_players/matches, active
```

`match_telemetry` là cốt lõi của vòng đọc: trả `frames: [{commitment,height,...}]`
→ client gọi `BlobClient.RetrieveBlob(height, commitment)` lấy data thật.

## 1. Build (trong cw-plus) — PHẢI build MVP

> Source là CosmWasm **2.0** (cw-plus). Chain chạy wasmd 0.50 / wasmvm 1.5 nên
> Gatekeeper **chặn "bulk memory ops"** — mà rustc 1.82+ tự phát khi build raw.
> Vì vậy PHẢI build với `target-cpu=mvp` (optimizer làm sẵn việc này). Đây là lý
> do mọi contract cw-plus đều build qua optimizer. **Không** phải hạ version.

```bash
cd ~/code/cw-plus
cargo test -p telemetry-registry                 # unit test logic game (2.0)

# Cách CHÍNH — optimizer (giống mọi contract cw-plus, ra wasm MVP + nhỏ):
bash scripts/optimizer.sh
# → artifacts/telemetry_registry.wasm
```

Cách nhanh không cần Docker (dùng `.cargo/config.toml` target-cpu=mvp đã thêm sẵn
trong crate — nhớ `cd` vào crate để cargo đọc config):

```bash
rustup target add wasm32-unknown-unknown         # 1 lần
cd ~/code/cw-plus/contracts/telemetry-registry
cargo build --release --target wasm32-unknown-unknown
# → ../../target/wasm32-unknown-unknown/release/telemetry_registry.wasm
```

> ⚠️ KHÔNG build bằng `cargo build -p telemetry-registry --target wasm32...` từ
> root cw-plus — cargo không đọc config của crate ở đó nên wasm dính bulk-memory
> và deploy sẽ fail (`Bulk memory operation detected`).

## 2. Thả wasm vào đây

```bash
cp ~/code/cw-plus/artifacts/telemetry_registry.wasm \
   apps/cosmos-exec/sdk/cosmoswasm/examples/game-telemetry/contract/artifacts/
```

## 3. Chạy — 2 bước (ví dụ tự load .env + tự deploy)

Ví dụ **tự đọc `.env`** ở project root (khớp DA bridge với chain) và **tự deploy**
wasm trong `./artifacts/` khi không truyền `--contract`. Nên chỉ cần:

> ⚠️ `apps/cosmos-exec` là **module Go riêng** — phải `cd` vào đó rồi chạy bằng
> đường dẫn package tương đối. Chạy `go run ./apps/cosmos-exec/...` từ root
> `ev-node` sẽ lỗi `main module ... does not contain package`.

```bash
# terminal 1 — dựng chain (từ root ev-node; đọc .env: DA_BRIDGE_RPC, DA_AUTH_TOKEN)
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go

# terminal 2 — chạy ví dụ TỪ TRONG module apps/cosmos-exec
cd apps/cosmos-exec
go run ./sdk/cosmoswasm/examples/game-telemetry
```

Ví dụ sẽ: **store + instantiate contract** → tạo match → ghi 5 frame telemetry
(blob-first) → ghi replay root → finish match → query
`match_telemetry`/`leaderboard`/`stats` và **kéo lại 1 frame từ Celestia qua con
trỏ on-chain**. (`.env` vẫn tự load — `findProjectRoot` lần ngược lên `ev-node`.)

Tùy chọn override (vẫn `cd apps/cosmos-exec` trước):

```bash
go run ./sdk/cosmoswasm/examples/game-telemetry \
    --contract cosmos1...   # dùng contract đã deploy sẵn (bỏ qua auto-deploy)
    --wasm /path/to.wasm    # chỉ định wasm khác để auto-deploy
    --frames 10 --namespace my-game --exec-url http://127.0.0.1:50051
```

Không có wasm trong `./artifacts` và không `--contract` → ví dụ chạy **chỉ phần
off-chain** (submit/retrieve/verify/chunk/merkle), bỏ qua phần on-chain.

## Lưu ý

- Ví dụ dùng tx **chưa ký** → mọi message xuất phát từ `DefaultSender()` (1 player,
  host = winner). Cần nhiều player thật thì dùng `BuildSignedExecuteTx` + nhiều signer.
- `height` gửi dạng JSON number → contract dùng `u64`.
- Contract chỉ *lưu sổ* commitment; data thật trên Celestia.
