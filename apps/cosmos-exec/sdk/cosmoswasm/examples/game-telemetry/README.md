# Telemetry Registry — ví dụ game blob-first

Một **game match-arena on-chain** minh hoạ mẫu *blob-first* của SDK `cosmos-exec`.
Dữ liệu nặng — telemetry từng frame và replay cả trận — được đẩy **thẳng lên
[Celestia](https://celestia.org) DA** (off-chain); chỉ **commitment / Merkle root**
được ghi on-chain trong contract CosmWasm `telemetry-registry`, gắn vào từng match.

Client query `match_telemetry` để lấy các con trỏ `(height, namespace, commitment)`
rồi kéo data thật từ Celestia qua `Blob.Get` — đó chính là lý do `height` phải được
lưu on-chain cạnh mỗi commitment.

Một match đi qua ba trạng thái:

```
Lobby ──StartMatch──▶ Active ──FinishMatch──▶ Finished
  ▲                     │
CreateMatch / JoinMatch  RecordTelemetry* / RecordReplay
```

> **Cần JSON copy-paste cho từng message?** Xem
> [contract/INTERACT.md](./contract/INTERACT.md) — mọi execute/query kèm JSON sẵn
> dùng và lệnh `wasmd` CLI.

---

## Hai luồng dữ liệu: telemetry vs replay

Đây là phần dễ nhầm nhất, nên nói thẳng: **telemetry và replay là HAI dòng dữ
liệu khác nhau**, mỗi dòng có message on-chain riêng và cách đẩy lên Celestia
riêng. Đừng so kích thước của chúng với nhau.

| | Telemetry từng frame | Replay cả trận |
| --- | --- | --- |
| Sinh bởi | `makeFrame()` | `makeReplay()` |
| Nội dung | **state snapshot** (JSON) | **input/command log** (text) |
| Trả lời câu hỏi | "tại tick này mọi player ở đâu, máu bao nhiêu" | "những lệnh nào đã được nhập" |
| Kích thước | nhỏ — 1 frame (~177 byte) | lớn — cả trận (ví dụ 300 KiB) |
| Đẩy lên Celestia bằng | `SubmitBlob` (1 blob) | `SubmitBatch` (chunk + Merkle) |
| Ghi on-chain bằng | `record_telemetry` (commitment) | `record_replay` (Merkle root) |

### Telemetry lưu gì — ví dụ cụ thể

Mỗi frame là một JSON chụp **trạng thái** toàn bộ player tại một tick. Sinh ra từ
[`makeFrame()`](./main.go) trong example. Frame tại `tick: 0`:

```json
{
  "match": "match-42",
  "tick": 0,
  "ts": 1781883866852,
  "players": [
    { "id": "p1", "pos": [0, 0, 12.3], "hp": 100 },
    { "id": "p2", "pos": [0, 4.2, 9],  "hp": 87 },
    { "id": "p3", "pos": [0, 1.1, -3.7], "hp": 64 }
  ]
}
```

- `pos` là `[x, y, z]`; `hp` là máu của player tại tick đó.
- ⚠️ Telemetry **chỉ ghi kết quả (state), không ghi event**. Bạn sẽ KHÔNG thấy
  `shoot` hay `move` trong telemetry — chỉ thấy *hậu quả* của chúng: `p2.hp = 87`,
  `p3.hp = 64` là máu đã bị trừ. Muốn thấy event thì phải đọc **replay**.
- JSON được nén tuỳ chọn (`CompressIfBeneficial`), rồi `SubmitBlob` trả về
  `(commitment, height, namespace)` — đúng các field nạp vào `record_telemetry`.

### Replay lưu gì — ví dụ cụ thể

Replay là **event-log dạng text** phẳng, ghi lại chuỗi lệnh deterministic để có
thể *re-simulate* lại cả trận. Sinh ra từ [`makeReplay()`](./main.go). Một đoạn
đại diện:

```
tick:move(p1,1.5,0,12.3);move(p2,-3,4,9);shoot(p1,p3);
```

Ngữ pháp của dòng này:

| Token | Ý nghĩa |
| --- | --- |
| `tick:` | mốc bắt đầu một tick / frame mới |
| `move(p1,1.5,0,12.3)` | player `p1` di chuyển tới `x=1.5, y=0, z=12.3` |
| `move(p2,-3,4,9)` | player `p2` di chuyển tới `(-3, 4, 9)` |
| `shoot(p1,p3)` | player `p1` bắn player `p3` |
| `;` | dấu phân tách event |

> **Vì sao replay tận 307200 byte còn telemetry chỉ ~177 byte?** Trong example,
> `makeReplay(300 * 1024)` tạo đúng **307200 byte = 300 KiB** bằng cách lặp lại
> đoạn `tick:move(...);shoot(...)` ở trên cho đầy buffer (đây là replay *synthetic*
> để demo; game thật thì đây là log lệnh cả trận). Còn 177 byte là **một** frame
> telemetry — một tick. Hai con số không cùng đơn vị: 1 frame vs cả trận. Việc
> đoạn text lặp y hệt nhau cũng là do `makeReplay` lặp một chuỗi cố định.

### Replay hoạt động như thế nào (chunk → Merkle → on-chain)

300 KiB là quá lớn để nhét vào một blob/commitment đơn, nên replay được **chunk +
gộp Merkle**:

```
makeReplay(300 KiB)                 # 307200 byte "tick:move(...);shoot(...)"
  → ChunkBlob(replay, 64 KiB)       # cắt thành 5 chunk (4×64KiB + 1×44KiB) + meta
  → SubmitBatch(chunks)             # đẩy từng chunk lên Celestia DA
        ⇒ { root, height, count=5, commitments[] }
  → record_replay { match_id, root, height, count, namespace }
```

Chỉ **Merkle `root` 32 byte** (cộng `height`/`count`) lên on-chain — KHÔNG phải
300 KiB. `root` được lưu vào `Match.replay_root`. Có thể chứng minh một chunk
thuộc về root bằng `BuildMerkleProof` / `VerifyMerkleProof` mà không cần tải cả
batch.

### Đọc data ngược lại từ Celestia

Mục đích của việc giữ `height` + `namespace` on-chain chính là để truy xuất:

```
query match_telemetry / match  → (height, namespace, commitment | root + commitments)
  → RetrieveBlob(height, commitment)     # từng frame / từng chunk, từ Celestia
  → MaybeDecompress / ReassembleChunks   # phục hồi byte gốc
```

Với replay, các chunk được ghép lại thành đúng chuỗi byte `tick:move(...)` ban đầu
rồi verify lại với Merkle root; với một frame telemetry thì phục hồi lại JSON
snapshot ở trên.

---

## Contract `telemetry-registry`

Contract chỉ *lưu sổ* con trỏ; toàn bộ data thật nằm trên Celestia. Dưới đây là
tóm tắt; JSON đầy đủ cho từng message ở [contract/INTERACT.md](./contract/INTERACT.md).

### Instantiate

```rust
pub struct InstantiateMsg {
    pub max_players_per_match: Option<u32>, // mặc định 8
}
```

Sender trở thành `admin`. Khởi tạo seed `CONFIG` và reset bộ đếm match về 0.

### State

| Key           | Type                          | Mô tả                                              |
| ------------- | ----------------------------- | ------------------------------------------------- |
| `config`      | `Item<Config>`                | Địa chỉ admin + số player tối đa mỗi match.        |
| `match_count` | `Item<u64>`                   | Bộ đếm tăng dần; cũng là nguồn id match kế tiếp.   |
| `players`     | `Map<&Addr, PlayerProfile>`   | Player đã đăng ký, key theo address.               |
| `matches`     | `Map<u64, Match>`             | Mọi match, key theo match id.                      |

`Match` giữ `telemetry: Vec<BlobRef>` (con trỏ từng frame) và `replay_root /
replay_height / replay_count` (batch replay). `BlobRef` là bản ghi blob-first:

```rust
pub struct BlobRef {
    pub commitment: String,
    pub height: u64,
    pub namespace: String,
    pub kind: String, // "telemetry" | "replay" | tag tuỳ app
    pub recorded_at: u64,
}
```

### Execute

| Message | Ai gọi | Tác dụng |
| --- | --- | --- |
| `register_player { handle }` | bất kỳ | Đăng ký sender. `handle` 1–32 ký tự; một lần / address. |
| `create_match {}` | đã đăng ký | Tạo match `Lobby`; sender là host + player đầu tiên. Trả `match_id` trong event. |
| `join_match { match_id }` | đã đăng ký | Vào match `Lobby` (chưa đầy, chưa join). |
| `start_match { match_id }` | host | `Lobby → Active`. |
| `record_telemetry { match_id, commitment, height?, namespace?, tag? }` | participant | Thêm một `BlobRef` telemetry vào match `Active`. Field từ `SubmitBlob`. |
| `record_replay { match_id, root, height?, count?, namespace? }` | host | Ghi Merkle root replay vào match. Field từ `SubmitBatch`. |
| `finish_match { match_id, winner, scores }` | host | `Active → Finished`; cập nhật winner + score vào profile. |
| `set_max_players { max_players_per_match }` | admin | Đổi cap player mỗi match. |
| `transfer_admin { new_admin }` | admin | Chuyển quyền admin. |

### Query

| Query | Trả về |
| --- | --- |
| `config {}` | `Config` (admin + max players). |
| `player { address }` | `Option<PlayerProfile>`. |
| `match { match_id }` | `Option<Match>` (bản ghi match đầy đủ). |
| `list_matches { status?, limit? }` | Danh sách match, lọc theo `status` tuỳ chọn. |
| `match_telemetry { match_id }` | `frames[] + replay_root` — con trỏ blob-first để client kéo data từ Celestia. |
| `leaderboard { limit? }` | Player xếp theo wins, total_score. |
| `stats {}` | Tổng player / match, số match active. |

`match_telemetry` là cốt lõi của vòng đọc: trả `frames: [{commitment, height, ...}]`
→ client gọi `RetrieveBlob(height, commitment)` lấy data thật.

### Lỗi

`Unauthorized`, `PlayerNotRegistered`, `AlreadyRegistered`, `InvalidHandle`
(handle không 1–32 ký tự), `MatchNotFound`, `BadMatchState` (sai trạng thái vòng
đời cho hành động), `AlreadyJoined`, `MatchFull`, `NotParticipant`.

---

## 1. Build wasm (trong cw-plus) — PHẢI build MVP

Mã nguồn Rust (CosmWasm 2.0) nằm trong workspace **cw-plus**, không trong repo Go:

```
~/code/cw-plus/contracts/telemetry-registry/
    Cargo.toml
    src/{lib,contract,msg,state,error}.rs
```

> Chain chạy wasmd 0.50 / wasmvm 1.5 nên Gatekeeper **chặn "bulk memory ops"** —
> mà rustc 1.82+ tự phát khi build raw. Vì vậy PHẢI build với `target-cpu=mvp`
> (optimizer làm sẵn). Đây là lý do mọi contract cw-plus đều build qua optimizer.
> **Không** phải hạ version.

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

(Thư mục `./contract/artifacts/` chỉ là **chỗ thả `.wasm`** đã build.)

## 3. Chạy — 2 bước (ví dụ tự load .env + tự deploy)

Ví dụ **tự đọc `.env`** ở project root (khớp DA bridge với chain) và **tự deploy**
wasm trong `./contract/artifacts/` khi không truyền `--contract`.

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
(blob-first) → chunk replay 300 KiB → `SubmitBatch` → 1 Merkle root + proof → ghi
replay root → finish match → query `match_telemetry`/`leaderboard`/`stats` và
**kéo lại 1 frame từ Celestia qua con trỏ on-chain**. (`.env` tự load —
`findProjectRoot` lần ngược lên `ev-node`.)

Tùy chọn override (vẫn `cd apps/cosmos-exec` trước):

```bash
go run ./sdk/cosmoswasm/examples/game-telemetry \
    --contract cosmos1...   # dùng contract đã deploy sẵn (bỏ qua auto-deploy)
    --wasm /path/to.wasm    # chỉ định wasm khác để auto-deploy
    --frames 10 --namespace my-game --exec-url http://127.0.0.1:50051
```

Không có wasm trong `./contract/artifacts` và không `--contract` → ví dụ chạy **chỉ
phần off-chain** (submit/retrieve/verify/chunk/merkle), bỏ qua phần on-chain.

## Lưu ý

- Ví dụ dùng tx **đã ký** từ key trong env (`COSMOS_EXEC_TREASURY_PRIVKEY_HEX`) →
  mọi message xuất phát từ một sender (1 player, host = winner). Cần nhiều player
  thật thì dùng `BuildSignedExecuteTx` với nhiều signer.
- `height` gửi dạng JSON number → contract dùng `u64`.
- Contract chỉ *lưu sổ* commitment; data thật trên Celestia.
- JSON copy-paste cho từng message: [contract/INTERACT.md](./contract/INTERACT.md).
