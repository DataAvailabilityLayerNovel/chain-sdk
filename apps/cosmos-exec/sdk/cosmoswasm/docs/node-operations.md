# Vận hành node: sequencer, full node, data on/off-chain, biến ở đâu

Tài liệu này mô tả **cách stack khởi chạy thực tế**, **dữ liệu nằm đâu** (on-chain Celestia vs local), và **mọi biến cấu hình đọc từ chỗ nào** — bám đúng `scripts/run-cosmos-wasm-nodes.go` và code.

> Liên quan: [sequencer-security.md](sequencer-security.md) (single vs based), [chain-flow.md](chain-flow.md) (vòng đời tx/block), [fee-economics.md](fee-economics.md).

## Mục lục

- [1. Mỗi node = 2 tiến trình](#1-mỗi-node--2-tiến-trình)
  - [Code của 2 tiến trình nằm ở đâu](#code-của-2-tiến-trình-nằm-ở-đâu)
- [2. Trình tự khởi chạy (từ run script)](#2-trình-tự-khởi-chạy-từ-run-script)
- [3. Cổng mặc định](#3-cổng-mặc-định)
- [4. Dữ liệu nằm đâu — on-chain vs off-chain](#4-dữ-liệu-nằm-đâu--on-chain-vs-off-chain)
  - [On-chain (Celestia DA) — nguồn chân lý](#on-chain-celestia-da--nguồn-chân-lý)
  - [Off-chain (local trên máy node) — cache/state để chạy](#off-chain-local-trên-máy-node--cachestate-để-chạy)
  - [4c. Off-chain lưu thế nào — persist / không, DB gì, hoạt động ra sao](#4c-off-chain-lưu-thế-nào--persist--không-db-gì-hoạt-động-ra-sao)
  - [4b. Tất cả thư mục `.` (dotfolder) có thể xuất hiện ở repo root](#4b-tất-cả-thư-mục--dotfolder-có-thể-xuất-hiện-ở-repo-root)
- [5. Mọi biến đọc từ đâu](#5-mọi-biến-đọc-từ-đâu)
- [5b. Code map: node nằm đâu, function nào làm gì](#5b-code-map-node-nằm-đâu-function-nào-làm-gì)
  - [5b.1 Điểm vào & dây nối node](#5b1-điểm-vào--dây-nối-node)
  - [5b.2 Block components — 4 cỗ máy chạy nền](#5b2-block-components--4-cỗ-máy-chạy-nền)
  - [5b.3 Lưu data bằng function nào](#5b3-lưu-data-bằng-function-nào)
  - [5b.4 P2P bằng file/function nào — hai tầng](#5b4-p2p-bằng-filefunction-nào--hai-tầng)
  - [5b.5 Sync lại từ Celestia chạy ra sao](#5b5-sync-lại-từ-celestia-chạy-ra-sao)
  - [5b.6 Node key & signer key — sinh ra thế nào, lấy key từ đâu](#5b6-node-key--signer-key--sinh-ra-thế-nào-lấy-key-từ-đâu)
- [6. Sequencer khác full node — tóm tắt](#6-sequencer-khác-full-node--tóm-tắt)
- [7. Có phải trả tiền cho sequencer không?](#7-có-phải-trả-tiền-cho-sequencer-không)
- [Tham chiếu code](#tham-chiếu-code)

## 1. Mỗi node = 2 tiến trình

```
┌─ node "sequencer" ──────────────┐   ┌─ node "fullnode" ───────────────┐
│ evcosmos (ev-node runtime)      │   │ evcosmos (ev-node runtime)      │
│   aggregator=TRUE               │   │   aggregator=FALSE              │
│   produce block, ký, submit DA  │   │   sync block từ DA + P2P        │
│        │ grpc-executor-url      │   │        │ grpc-executor-url      │
│        ▼                        │   │        ▼                        │
│ cosmos-exec-grpc (execution)    │   │ cosmos-exec-grpc (execution)    │
│   chạy WASM, state, mempool     │   │   chạy lại tx để verify state   │
└─────────────────────────────────┘   └─────────────────────────────────┘
        sequencer.P2P  ◄───── peers ─────►  fullnode.P2P
                  cả hai cùng đọc/ghi 1 Celestia DA
```

- **evcosmos** = binary ev-node runtime (block production/sync, ký header, submit/đọc DA, P2P). Khác nhau **chỉ ở cờ** `--evnode.node.aggregator`.
- **cosmos-exec-grpc** = lớp execution (CosmWasm runtime + state + mempool + persist). evcosmos gọi nó qua `--grpc-executor-url`.

Sequencer và full node chạy **cùng bộ binary**, chỉ khác: aggregator true/false và full node có thêm `--evnode.p2p.peers <sequencer>`.

### Code của 2 tiến trình nằm ở đâu

| Tiến trình | Build từ | Entrypoint (file) | Vai trò |
|-----------|----------|-------------------|---------|
| **evcosmos** (ev-node runtime) | `apps/cosmos-wasm` (package `.`) | `apps/cosmos-wasm/main.go` → lệnh `start` ở [`apps/cosmos-wasm/cmd/run.go`](../../../../cosmos-wasm/cmd/run.go) (`RunCmd.RunE`) | đồng thuận: tạo/sync block, ký header, đọc/ghi DA, P2P |
| **cosmos-exec-grpc** (execution) | `apps/cosmos-exec` | [`apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go`](../../../cmd/cosmos-exec-grpc/main.go) | execution: chạy WASM, state, mempool, persist |

> **Lưu ý quan trọng:** logic node "lõi" (block, node, store, p2p, da…) **không** nằm trong `apps/`. `apps/cosmos-wasm` chỉ là **lớp wiring mỏng**: nó import package lõi từ module `github.com/DataAvailabilityLayerNovel/chain-sdk` — chính là **repo root ev-node này** (`block/`, `node/`, `pkg/`, `core/`). Vì vậy mọi tham chiếu kiểu `node/full.go`, `block/components.go`, `pkg/store/...` ở [mục 5b](#5b-code-map-node-nằm-đâu-function-nào-làm-gì) là **đường dẫn từ repo root**, không phải dưới `apps/`. Chi tiết dây nối: [mục 5b.1](#5b1-điểm-vào--dây-nối-node).
>
> Code của **lớp execution** (CosmWasm app, ante/fee, faucet, persist) thì nằm trong `apps/cosmos-exec/` — xem [bảng tham chiếu cuối](#tham-chiếu-code).

## 2. Trình tự khởi chạy (từ run script)

`scripts/run-cosmos-wasm-nodes.go` → `run()`:

```
1. findProjectRoot + loadDotEnv(.env)
2. resolveDAFromEnv()      → đọc DA_* env
3. validateDAConfig + preflightDA   → kiểm Celestia reachable/authorized
4. preparePaths()          → tạo home dirs + passphrase file
5. ensureBinaries()        → go build evcosmos, cosmos-exec-grpc
6. initNodes()             → evcosmos init (mỗi node), copy genesis seq→full
7. startExecutionServices()→ cosmos-exec-grpc cho cả 2 node (chờ gRPC healthy)
8. startSequencer()        → evcosmos start --evnode.node.aggregator=true
9. startFullNode()         → evcosmos start --aggregator=false --p2p.peers=<seq>
10. waitForChainSync()     → chờ seq + full vào cửa sổ đồng bộ
11. monitorProcesses()
```

Thứ tự bắt buộc: **execution service phải sẵn trước** khi evcosmos start (evcosmos cần executor để ExecuteTxs ngay block đầu). Full node start **sau** sequencer vì nó cần địa chỉ P2P của sequencer (`getNodePeerAddress`).

## 3. Cổng mặc định

| | Sequencer | Full node |
|---|---|---|
| Execution gRPC (`cosmos-exec-grpc`) | 50051 | 50052 |
| ev-node RPC (`--evnode.rpc.address`) | 38331 | 48331 |
| P2P (`--evnode.p2p.listen_address`) | 7860 | 7861 |

(web app proxy mặc định trỏ `BACKEND_URL` → 50051, `EVNODE_RPC_URL` → 38331.)

## 4. Dữ liệu nằm đâu — on-chain vs off-chain

### On-chain (Celestia DA) — nguồn chân lý

Mỗi block, ev-node submit lên Celestia dưới namespace (mặc định `rollup`):

- **Header blob**: signed header (height, time, app_hash, data_hash, proposer + chữ ký sequencer).
- **Data blob**: tx bytes của block (rỗng → không có data blob).

Đây là **dữ liệu duy nhất mang tính đồng thuận**: bất kỳ ai cũng tải từ Celestia về chạy lại để verify (xem [sequencer-security.md](sequencer-security.md)).

### Off-chain (local trên máy node) — cache/state để chạy

Khi **chạy qua run-script** (`scripts/run-cosmos-wasm-nodes.go`), **tất cả** runtime state sống gọn dưới **một** base dir `.cosmos-wasm-runner/` ở project root (để repo root không bị rải rác dotfile). Mỗi node có **2 home tách biệt** vì 2 tiến trình (xem [mục 1](#1-mỗi-node--2-tiến-trình)): `evcosmos-<name>/` cho tầng đồng thuận, `cosmos-exec-<name>/` cho tầng execution. Layout đầy đủ ([preparePaths](../../../../../scripts/run-cosmos-wasm-nodes.go#L304-L353)):

```
.cosmos-wasm-runner/
├─ passphrase.txt                 # dev: "secret"; prod: từ --passphrase-file/ENV
├─ logs/cosmos-wasm-chain.log     # log gộp cả 4 tiến trình (append-only)
└─ nodes/
   ├─ evcosmos-sequencer/         # ① ev-node home (đồng thuận) — sequencer
   ├─ cosmos-exec-sequencer/      # ② cosmos-exec home (execution) — sequencer
   ├─ evcosmos-fullnode/          # ① ev-node home (đồng thuận) — full node
   └─ cosmos-exec-fullnode/       # ② cosmos-exec home (execution) — full node
```

`<name>` = `sequencer` hoặc `fullnode`. **Mỗi node = một cặp `evcosmos-<name>` + `cosmos-exec-<name>`** — không phải hai loại node khác nhau, mà là hai *tầng* của cùng một node.

**① `evcosmos-<name>/` — ev-node runtime (tầng đồng thuận)**, `--home` của `evcosmos`:

| Đường dẫn | Chứa gì |
|-----------|---------|
| `config/genesis.json` | Genesis ev-node (chain_id, DAStartHeight, proposer; sequencer patch forced-inclusion rồi copy sang full node) |
| `config/signer.json` | **Signer key đã mã hoá** (chỉ sequencer dùng để ký header; mở bằng passphrase từ `--evnode.signer.passphrase_file`) |
| `config/*.yaml` | Node config (P2P, RPC, DA, block_time…) |
| `data/cosmos-wasm/` | **Store đồng thuận** (badger): header, data (tx bytes), signature/commit, `State` (app_hash, **DAHeight đã xử lý**), index hash→height. Đây là store ở [mục 5b.3](#5b3-lưu-data-bằng-function-nào) |

#### `signer.json` & pubkey/proposer lấy từ đâu?

`signer.json` và proposer pubkey trong `genesis.json` **không phải file tĩnh** — chúng do `evcosmos init` sinh ra (hoặc được dùng lại), tùy trường hợp. `evcosmos init` chỉ chạy khi **chưa có** `signer.json`; nếu đã có thì runner **skip init** ([initNodes](../../../../../scripts/run-cosmos-wasm-nodes.go#L469-L510)).

| Trường hợp | `signer.json` (key) | Pubkey / `proposer` trong genesis | Passphrase |
|-----------|---------------------|-----------------------------------|------------|
| **Init mới** — `signer.json` chưa tồn tại (lần đầu, hoặc sau `--clean-on-start=true` xóa home) | `evcosmos init --evnode.node.aggregator=true` sinh **key ngẫu nhiên mới**, mã hoá rồi ghi ra (chỉ trên **sequencer**) | Pubkey suy ra từ key mới → ghi vào `proposer` của genesis **sequencer**, rồi [copy sang full node](../../../../../scripts/run-cosmos-wasm-nodes.go#L503-L507) | Mã hoá bằng passphrase đang dùng (xem dưới). Passphrase này về sau **bắt buộc khớp** để mở lại |
| **Dùng lại** — `--clean-on-start=false` và `signer.json` đã tồn tại | **Giữ nguyên** key cũ (skip init) | Giữ nguyên `proposer` đã pin trong genesis cũ | Passphrase truyền vào **phải khớp** cái đã mã hoá file cũ; lệch → `failed to decrypt private key (wrong passphrase?)` ⇒ phải `--clean-on-start=true` hoặc đưa đúng passphrase |
| **Full node** (`aggregator=false`) | **Không có signer ký** — init tạo genesis với `proposer=null` | Học pubkey proposer **từ genesis copy của sequencer** (đè lên genesis null); dùng nó để **verify** chữ ký header sequencer | Không ký nên không cần key riêng |

**Passphrase đến từ đâu** ([preparePaths](../../../../../scripts/run-cosmos-wasm-nodes.go#L304-L353)):
- `--profile dev` → chuỗi cố định **`secret`** (runner tự ghi `.cosmos-wasm-runner/passphrase.txt`).
- `--profile prod` → `--passphrase-file <path>`, hoặc env `EVCOSMOS_PASSPHRASE_FILE` / `EVCOSMOS_PASSPHRASE`. Bắt buộc, không có → lỗi.

> ⚠️ **Bẫy thường gặp:** tạo state bằng profile/passphrase này rồi chạy lại bằng profile/passphrase khác với `--clean-on-start=false`. Vd state cũ tạo ở dev (passphrase `secret`), sau chạy prod với passphrase file khác → key cũ không mở được → MAC fail. Khắc phục: hoặc `--clean-on-start=true` (tạo key+genesis mới đồng bộ), hoặc đưa đúng passphrase gốc.

**② `cosmos-exec-<name>/` — execution (`cosmos-exec-grpc --home`)**, data dir = `<home>/data` ([ResolveDataDir](../../../config/config.go#L212-L218)):

| Đường dẫn | Chứa gì |
|-----------|---------|
| `data/application/` | **State CosmWasm thật** (LevelDB tên `application`): balance account, contract state, store các module cosmos-sdk ([main.go openDatabase](../../../cmd/cosmos-exec-grpc/main.go#L255-L265)) |
| `data/metadata.json` | Checkpoint executor: `chainID`, `stateRoot`, heights — ghi **đè** atomic+fsync mỗi lần đổi state |
| `data/tx_results.jsonl` | Append-only: kết quả thực thi từng tx |
| `data/blocks.jsonl` | Append-only: block info (index off-chain) |

(File `metadata.json`/`*.jsonl` do `PersistStore` ghi — [persist.go](../../../executor/persist.go).)

> **`evcosmos-fullnode` khác gì `cosmos-exec-fullnode`?** Cùng một full node, hai vai:
> - `evcosmos-fullnode` = tầng **đồng thuận** — *block nào* (header đã ký, thứ tự tx, DAHeight đã sync). Đọc/ghi DA, P2P, verify chữ ký sequencer.
> - `cosmos-exec-fullnode` = tầng **execution** — *state ra sao sau khi chạy tx* (balance, contract). Không biết gì về DA/P2P; chỉ nhận tx từ evcosmos qua gRPC, chạy lại, persist state.
>
> evcosmos là "sổ cái thứ tự + chứng minh", cosmos-exec là "máy tính ra số dư". Cặp `sequencer` đối xứng y hệt, chỉ khác sequencer **tạo+ký+submit** còn full node **đọc+chạy lại để verify**.

> Hệ quả khi xóa:
> - Xóa **cả** base dir = mất toàn bộ cache local, **không mất chain** (sync lại từ Celestia, dựng lại cả 2 tầng — xem [mục 5b.5](#5b5-sync-lại-từ-celestia-chạy-ra-sao)).
> - Đổi genesis/treasury chỉ áp khi state local **chưa tồn tại** → phải `--clean-on-start` (xóa home) để genesis mới có hiệu lực. Prod mặc định `clean-on-start=false` nên **không** tự xóa.

### 4c. Off-chain lưu thế nào — persist / không, DB gì, hoạt động ra sao

Tầng execution (`cosmos-exec-grpc`) off-chain lưu **hai loại dữ liệu tách biệt**, dùng **hai cơ chế khác nhau**. Đừng gộp làm một:

| | ① State thật (CosmWasm/cosmos-sdk) | ② Checkpoint + index (executor) |
|---|---|---|
| Nằm ở | `data/application/` | `data/metadata.json`, `data/tx_results.jsonl`, `data/blocks.jsonl` |
| DB / định dạng | **LevelDB** (`goleveldb`) qua `cosmos-db`, bọc trong **IAVL** (cây có version) của BaseApp `CommitMultiStore` | File thường: 1 JSON (metadata) + 2 file **JSONL** (mỗi dòng 1 JSON) |
| Chứa gì | balance account, contract state, store module cosmos-sdk — **state đầy đủ** | `chain_id`, `state_root`, `last_height`, `finalized_height`; kết quả từng tx; info từng block |
| Ghi bằng | `app.Commit()` mỗi block (BaseApp) | [`PersistStore`](../../../executor/persist.go) |
| Ai là "nguồn" | Đây là state thi hành thật | **Chỉ là checkpoint + chỉ mục tra cứu**, suy lại được từ ①/DA |

**① `application/` — LevelDB + IAVL (state thật).** cosmos-sdk BaseApp giữ toàn bộ state trong `CommitMultiStore`; backend đĩa là **goleveldb** tên `application` ([main.go openDatabase](../../../cmd/cosmos-exec-grpc/main.go#L255-L275)). Điểm mấu chốt: IAVL lưu **theo từng version (height)** — mỗi block `Commit()` tạo một version mới, `stateRoot = cms.LastCommitID().Hash`. Nhờ versioned nên khi executor **đi trước** consensus (crash: executor commit tới height 10 nhưng ev-node mới persist tới 8), ev-node gọi `Rollback` → `app.LoadVersion(8)` nạp lại đúng version cũ ([executor.go Rollback](../../../executor/executor.go#L819-L844)). Đây là lý do state execution **phải** dùng KV-DB có version, không phải file phẳng.

**② `PersistStore` — metadata + JSONL (checkpoint/index).** Không phải state, mà là *sổ tay* của executor để khởi động nhanh và tra cứu ([persist.go](../../../executor/persist.go)):

- **`metadata.json`** — checkpoint tối thiểu (`initialized`, `chain_id`, `state_root`, `last_height`, `finalized_height`). Ghi **ĐÈ** (không append) sau `InitChain` / `ExecuteTxs` / `SetFinal`, và **ghi bền vững**: viết ra `.tmp` → `fsync` → `rename` đè → `fsync` cả thư mục cha ([writeFileAtomicSync](../../../executor/persist.go#L168-L201)). Vì sao cầu kỳ vậy: metadata là **mỏ neo phục hồi** — mất điện làm file cụt/hỏng sẽ hỏng luôn checkpoint restart. Rename nguyên tử đảm bảo hoặc thấy bản cũ nguyên vẹn, hoặc bản mới nguyên vẹn, không có bản dở dang.
- **`tx_results.jsonl` / `blocks.jsonl`** — **append-only** (`O_APPEND`), mỗi dòng 1 JSON có `type` ("tx"/"block"). Chỉ nối thêm nên không cần atomic; nếu dòng cuối bị cụt do crash, lúc load **bỏ qua và đếm số dòng hỏng** chứ không làm hỏng cả file ([LoadTxResults](../../../executor/persist.go#L230-L254)). Đây là **chỉ mục tra cứu nhanh** theo `hash` (tx) / `height` (block) — thứ mà lôi ra từ IAVL sẽ đắt.

**Khởi động (replay).** Lúc start, ① LevelDB/IAVL tự khôi phục state thật; ② `PersistStore` **đọc lại toàn bộ vào RAM**: `LoadMetadata` (thiếu file → struct rỗng, coi như chain mới, **không lỗi**), `LoadTxResults` → `map[hash]`, `LoadBlocks` → `map[height]`. Nhờ vậy executor biết ngay `last_height`/`state_root` mà không phải chạy lại từ đầu.

**Persist hay KHÔNG persist — quyết định bởi `--in-memory` / profile:**

| Chế độ | State ① | Checkpoint/index ② | Sống sót restart? | Dùng khi |
|--------|---------|--------------------|-------------------|----------|
| **dev / prod** (mặc định, không in-memory) | LevelDB trên đĩa | Bật: `if !cfg.InMemory { PersistTxResults = true }` ([main.go](../../../cmd/cosmos-exec-grpc/main.go#L117-L128)) | **Có** | chạy node thật |
| **`--in-memory`** hoặc **profile `test`** | `dbm.NewMemDB()` (RAM) | `persistStore = nil` — **không** ghi file | **Không** (mất khi tắt) | unit/integration test, tránh khoá file, chạy nhanh |

Nói cách khác: `persistStore` **có thể là `nil`** — mọi chỗ ghi đều `if e.persistStore != nil` ([executor.go](../../../executor/executor.go#L399-L425)). In-memory = không đĩa, không lock file, không tàn dư; đổi lại tắt là trắng.

**Tại sao tách hai cơ chế (không nhét hết vào một DB)?**
- State ① cần **versioned KV-DB** để rollback theo height (yêu cầu của cosmos-sdk/consensus) → IAVL/LevelDB là chuẩn.
- Checkpoint ② cần **ghi nhanh, atomic, đọc-lại-dễ** cho một nhúm số + hai luồng append → file JSON/JSONL nhẹ hơn nhiều so với mở transaction IAVL, và con người đọc/debug được trực tiếp.
- Khác backend với store *đồng thuận* của ev-node (badger, [mục 5b.3](#5b3-lưu-data-bằng-function-nào)) vì đây là **tiến trình khác** (`cosmos-exec` vs `evcosmos`), mỗi bên chọn DB theo hệ sinh thái của mình (cosmos-sdk → `cosmos-db`/goleveldb; ev-node → badger).

> **Off-chain KHÔNG phải nguồn chân lý.** Cả ① và ② đều tái tạo được: xoá sạch → sync lại từ Celestia rồi chạy lại tx dựng lại toàn bộ ([mục 5b.5](#5b5-sync-lại-từ-celestia-chạy-ra-sao)). Persist ở đây chỉ để **khỏi replay từ height 0 mỗi lần restart** và để **trả query nhanh**, không phải để "giữ chain".

### 4b. Tất cả thư mục `.` (dotfolder) có thể xuất hiện ở repo root

Ngoài `.cosmos-wasm-runner/` (do run-script quản, ở trên), khi bạn **chạy binary trực tiếp** (không qua run-script) hoặc **chạy test**, một số tiến trình tự tạo home **mặc định bằng đường dẫn tương đối** ngay tại **thư mục đang đứng (CWD)**. Đó là lý do bạn thấy các dotfolder "rải rác" như trong ảnh:

| Thư mục | Ai tạo | Là gì | Mất đi có sao không |
|---------|--------|-------|---------------------|
| `.cosmos-wasm-runner/` | run-script ([preparePaths](../../../../../scripts/run-cosmos-wasm-nodes.go#L304-L353)) | base dir gộp **đủ 4 home** + log khi chạy demo 2-node (xem [mục 4](#off-chain-local-trên-máy-node--cachestate-để-chạy)) | mất cache local, **không mất chain** |
| `.cosmos-exec-grpc/` | `cosmos-exec-grpc` khi **không** truyền `--home` | home **mặc định** của tiến trình execution; data ở `.cosmos-exec-grpc/data/application` (LevelDB) **và** cache WASM ở `.cosmos-exec-grpc/wasm/` — default từ [config.go:67](../../../config/config.go#L67) (`Home`) | mất state execution local; sync lại được |
| `.cosmos-exec-wasm/` | *(chỉ còn là fallback)* CosmWasm keeper khi `homeDir=""` | **cache WASM** (xem dưới). Từ bản cập nhật, cache nằm dưới `<Home>/wasm` nên dir này **không sinh ra nữa** khi chạy bình thường — chỉ xuất hiện nếu gọi `app.New` với home rỗng ([app.go](../../../app/app.go#L96)) | tự build/tải lại từ state — an toàn để xóa |
| `.data/` | tool **cosmos-explorer** (không phải node) | `.data/cosmos-explorer/index.db` = DB index của explorer; default ở [tools/cosmos-explorer/main.go:31](../../../../../tools/cosmos-explorer/main.go#L31) | xóa rồi `reindex` lại được |

> **Cache WASM giờ nằm dưới `<Home>/wasm` — hết cảnh rải theo CWD.** Trước đây `homePath` là đường dẫn tương đối cứng `".cosmos-exec-wasm"`, nên keeper tạo cache **tại CWD của tiến trình** → đẻ ra 3 bản trùng (`apps/cosmos-exec/`, `cmd/cosmos-exec-grpc/`, `executor/` khi chạy test). Nay [`app.New`](../../../app/app.go#L96) nhận tham số `homeDir` và cache đặt ở `<homeDir>/wasm`:
> - **Production:** [main.go](../../../cmd/cosmos-exec-grpc/main.go) truyền `cfg.Home` → cache ở `<Home>/wasm` (vd `.cosmos-exec-grpc/wasm`, hoặc `.cosmos-wasm-runner/nodes/cosmos-exec-<name>/wasm` khi qua run-script).
> - **Test:** truyền `t.TempDir()` → cache trong thư mục tạm, **tự xoá**, không còn rác `executor/.cosmos-exec-wasm` hay `cmd/.../.cosmos-exec-wasm`.
>
> Cache WASM là dữ liệu tái tạo được (build/tải lại từ state), xóa thoải mái dù nằm ở đâu.

**Bên trong `<Home>/wasm/` (cache CosmWasm):**

```
<Home>/                                 # vd .cosmos-exec-grpc/ hoặc .cosmos-wasm-runner/nodes/cosmos-exec-<name>/
├─ data/application/                    # state CosmWasm thật (LevelDB) — xem mục 4 ②
└─ wasm/
   ├─ cache/modules/v8-wasmer5/        # MODULE đã COMPILE sẵn (machine code Wasmer)
   │    └─ <hash>.wasm                  #   → nạp nhanh, khỏi compile lại mỗi lần gọi
   └─ state/wasm/                       # BYTECODE gốc đã upload (MsgStoreCode)
        └─ <codehash>.wasm              #   <codehash> = sha256 của bytecode (code_id ↔ hash)
```

- `state/wasm/<hash>.wasm` = **bytecode nguồn** mà ai đó đã `StoreCode` lên chain (nguồn chân lý của bytecode vẫn là DA; đây là bản sao cục bộ để chạy).
- `cache/modules/v8-wasmer5/` = **bản đã biên dịch** sang machine code (engine Wasmer v8), để execution không phải compile lại — chỉ là cache tăng tốc.
- Đây **không phải** state hợp đồng (balance/storage của contract). State đó nằm trong LevelDB `application` ở [mục 4 ②](#off-chain-local-trên-máy-node--cachestate-để-chạy).

> **Git:** mọi dotfolder trên đều là artefact runtime và **đã được `.gitignore` bỏ qua** — không bao giờ commit. Các rule tương ứng: `.cosmos-wasm-runner/` (dòng 39), `.cosmos-exec*/` + `**/.cosmos-exec*/` (dòng 38, 40 → bắt cả 3 bản `.cosmos-exec-wasm` lồng sâu), `.data/` (dòng 65). Nếu lỡ thấy chúng trong `git status`, kiểm tra lại `.gitignore`.

## 5. Mọi biến đọc từ đâu

Bốn nhóm cấu hình, bốn nguồn khác nhau:

| Nhóm | Ví dụ | Đọc ở đâu | Định nghĩa |
|------|-------|-----------|------------|
| **ev-node flags** | `--evnode.node.aggregator`, `--evnode.node.based_sequencer`, `--evnode.node.block_time`, `--evnode.node.lazy_mode`, `--evnode.da.namespace`, `--evnode.rpc.address`, `--evnode.p2p.peers`, `--evnode.signer.passphrase_file` | CLI cờ truyền cho `evcosmos` | `pkg/config/config.go` (tên cờ), `pkg/config/defaults.go` (default) |
| **DA env** | `DA_RPC` / `DA_BRIDGE_RPC`, `DA_AUTH_TOKEN`, `DA_NAMESPACE` (default `rollup`) | `.env` ở project root → `resolveDAFromEnv()` | `scripts/run-cosmos-wasm-nodes.go` |
| **cosmos-exec env** | `COSMOS_EXEC_ENFORCE_SIGNATURES`, `COSMOS_EXEC_TIA_PER_BYTE`, `COSMOS_EXEC_MIN_GAS_PRICE`, `COSMOS_EXEC_GAS_DENOM`, `COSMOS_EXEC_TREASURY_PRIVKEY_HEX`, `COSMOS_EXEC_TREASURY_AMOUNT`, `COSMOS_EXEC_FAUCET_AMOUNT`, `COSMOS_EXEC_FAUCET_GAS`, `COSMOS_EXEC_FAUCET_COOLDOWN_SECONDS` | Biến môi trường lúc start `cosmos-exec-grpc` | `app/ante.go` (enforce sig), `cmd/cosmos-exec-grpc/cost.go` (giá), `cmd/cosmos-exec-grpc/faucet.go` (treasury/faucet) |
| **cosmos-exec flags** | `--address`, `--home`, `--profile` (dev/test/prod), `--in-memory`, `--log-level` | CLI cờ truyền cho `cosmos-exec-grpc` | `cmd/cosmos-exec-grpc/main.go` |
| **run-script flags** | `--clean-on-start`, `--clean-on-exit`, `--block-time`, `--submit-interval`, `--chain_id`, `--log-level` | CLI cờ cho `run-cosmos-wasm-nodes.go` | `scripts/run-cosmos-wasm-nodes.go` |

Quy tắc nhớ nhanh:

- Cái gì thuộc **đồng thuận / mạng / DA / sequencer mode** → cờ `--evnode.*` (định nghĩa ở `pkg/config`).
- Cái gì thuộc **execution / kinh tế phí / faucet** → env `COSMOS_EXEC_*` (đọc trong package `app` và `cmd/cosmos-exec-grpc`).
- Cái gì thuộc **endpoint Celestia** → env `DA_*` (chỉ run-script đọc, rồi forward thành `--evnode.da.*`).
- Cái gì thuộc **lifecycle dev (xóa data, block time demo)** → cờ của run-script.

## 5b. Code map: node nằm đâu, function nào làm gì

Phần này trả lời trực tiếp: **code của node ở đâu**, **lưu data bằng function nào**, **P2P bằng file/function nào**, **sync lại từ Celestia chạy ra sao**. Mọi tham chiếu bám source `evcosmos` (build từ `apps/cosmos-wasm`, package `.`).

### 5b.1 Điểm vào & dây nối node

```
apps/cosmos-wasm/cmd/run.go : RunCmd.RunE
  ├─ store.NewDefaultKVStore(RootDir, DBPath, "cosmos-wasm")  → mở badger trên đĩa
  ├─ rollgenesis.LoadGenesis(...)                              → đọc config/genesis.json
  ├─ createSequencer(...)                                      → single/based sequencer
  └─ rollcmd.StartNode(...)                  → pkg/cmd/run_node.go:StartNode
        ├─ p2p.NewClient(P2P, nodeKey.PrivKey, datastore, ChainID, …)   (run_node.go:172)
        └─ node.NewNode(...)                 → node/node.go:NewNode
              ├─ aggregator? → node/full.go:newAggregatorMode
              └─ sync?       → node/full.go:newSyncMode
```

- **`node/full.go`** = full node runtime (cả sequencer lẫn full node đều là `FullNode`; chỉ khác mode). `FullNode.Run()` (full.go:279) start P2P client một lần rồi start block components.
- **`node/node.go:NewNode`** chọn light vs full; light node ở `node/light.go`.
- Store on-đĩa thật sự nằm ở `<RootDir>/<DBPath>/cosmos-wasm` = `…/evcosmos-<name>/data/cosmos-wasm` (badger). `RootDir` từ home node, `DBPath` default `data` (`pkg/config/defaults.go:58`).

### 5b.2 Block components — 4 cỗ máy chạy nền

Wiring ở **`block/components.go`** (`NewSyncComponents` cho full node, `newAggregatorComponents` cho sequencer). `Components.Start()` (components.go:45) bật theo thứ tự:

| Component | File | Vai trò | Bật khi |
|-----------|------|---------|---------|
| `Executor` | `block/internal/executing/executor.go` | **Sản xuất block** (sequencer) | aggregator |
| `Reaper` | `block/internal/reaping/` | Gom tx từ mempool execution | aggregator |
| `Submitter` | `block/internal/submitting/submitter.go` | **Submit header/data lên Celestia** | aggregator (có signer) |
| `Syncer` | `block/internal/syncing/syncer.go` | **Sync block từ DA + P2P**, apply, lưu | cả hai |

### 5b.3 Lưu data bằng function nào

Tầng store: **`pkg/store`** (badger qua `go-ds-badger4`).

- Mở DB: `store.NewDefaultKVStore` → `pkg/store/kv.go:18` (`badger4.NewDatastore(<root>/data/cosmos-wasm)`).
- Bọc store: `store.New` (`pkg/store/store.go:25`) = `DefaultStore`. Có thêm `NewCachedStore` (LRU) và `NewEvNodeKVStore` (prefix).
- **Ghi block là atomic qua batch** — không ghi lẻ. Quy trình chuẩn (cả sequencer lẫn syncer dùng chung):

  ```
  batch := store.NewBatch(ctx)
  batch.SaveBlockData(header, data, &header.Signature)  // pkg/store/batch.go:50
  batch.SetHeight(newHeight)                            // pkg/store/batch.go:36
  batch.UpdateState(newState)                           // ghi State (app_hash, DAHeight…)
  batch.Commit()                                        // 1 transaction badger
  ```

  - Sequencer ghi ở `executing/executor.go:566` (`ProduceBlock` → `SaveBlockData` → `Commit` tại :604).
  - Syncer ghi ở `syncing/syncer.go:745` (`SaveBlockData` → `SetHeight` :749 → `UpdateState` → `Commit` :757).

- `SaveBlockData` thực chất tách thành các key có prefix (`pkg/store/keys.go`):
  - header → prefix `h` (`getHeaderKey`), data → `d`, signature/commit → `c`, state → `s`, metadata → `m`, hash→height index → `i`, con trỏ height hiện tại → `t`.
- Đọc lại: `GetBlockData`, `GetHeader`, `GetState`, `Height` (`pkg/store/store.go:37–152`).
- **Con trỏ DA đã xử lý** lưu trong `State.DAHeight` (ghi qua `UpdateState`) → dùng để khôi phục sau restart (xem `syncer.go:311 initializeState`). Map height→DA height có key riêng (`HeightToDAHeightKey`, `keys.go:14`).

> Đây là store *đồng thuận* của ev-node (header/data/state). Khác với store *execution* off-chain (`metadata.json`/`tx_results.jsonl`/`blocks.jsonl` ở mục 4) do `cosmos-exec-grpc` ghi — `apps/cosmos-exec/executor/persist.go`.

### 5b.4 P2P bằng file/function nào — hai tầng

P2P trong stack này có **2 tầng tách biệt**:

**Tầng 1 — discovery & vận chuyển (libp2p):** `pkg/p2p/client.go`

- `p2p.NewClient` (`client.go:65`) tạo client; `Client.Start` (`client.go:124`) làm 3 việc:
  1. `listen()` (:248) — mở host libp2p trên `--evnode.p2p.listen_address` (7860/7861).
  2. `setupDHT()` (:257) — Kademlia DHT + bootstrap seed nodes.
  3. `setupGossiping()` (:361) — khởi tạo GossipSub (`pubsub.NewGossipSub`).
- Peer discovery: `peerDiscovery` / `advertise` / `findPeers` / `tryConnect` (:283–350). Full node lấy peer sequencer qua `--evnode.p2p.peers` (cờ → `pkg/config`).
- Cho phép/chặn peer: `setupAllowedPeers` / `setupBlockedPeers` qua `conngater`.

**Tầng 2 — phát/nhận header & block (go-header trên GossipSub):** `pkg/sync/sync_service.go`

- `HeaderSyncService` và `DataSyncService` (alias của `SyncService[H]`, `sync_service.go:35–38`) bọc thư viện `celestiaorg/go-header`:
  - `goheaderp2p.Subscriber` — nhận header/data mới qua gossip topic.
  - `goheaderp2p.Exchange` / `ExchangeServer` — request/response để kéo block còn thiếu từ peer.
  - `store header.Store[H]` — store go-header riêng, đồng bộ với ev-node store qua adapter (`store.NewDataStoreAdapter` / `NewHeaderStoreAdapter`).
- Sequencer **publish** header/data đã ký lên các service này; full node **subscribe** rồi đẩy vào syncer.
- Phía syncer tiêu thụ P2P: `block/internal/syncing/p2p_handler.go` — `P2PHandler.ProcessHeight` (:72) lấy `headerStore.GetByHeight` / `dataStore.GetByHeight`, kiểm proposer (`assertExpectedProposer` :125), rồi bắn `DAHeightEvent` vào `heightInCh`. Vòng lặp P2P: `syncer.go:447 p2pWorkerLoop`.

### 5b.5 Sync lại từ Celestia chạy ra sao

Đường DA là **nguồn chân lý** — full node (và cả sequencer khi khởi động lại) dựng lại chain bằng cách đọc blob từ Celestia. Luồng:

```
syncer.Start (syncer.go:165)
  → initializeState() : khôi phục State + daRetrieverHeight từ store (genesis.DAStartHeight nếu mới)
  → startSyncWorkers() : chạy các worker
       ├─ DA worker  → daRetriever.RetrieveFromDA(daHeight)        block/internal/syncing/da_retriever.go:109
       └─ P2P worker → p2pWorkerLoop (mục 5b.4)
```

Chi tiết DA retriever (`block/internal/syncing/da_retriever.go`):

1. `RetrieveFromDA(daHeight)` (:109) gọi `fetchBlobs` (:126).
2. `fetchBlobs` đọc **2 namespace**: header namespace và data namespace (:128, :135) qua `client.Retrieve(...)` — `share.NewNamespaceFromBytes` lọc đúng namespace `rollup`.
3. Client DA thật: **`block/internal/da/client.go`** — `client.Retrieve` / `client.Submit` (:72) bọc JSON-RPC tới Celestia node (`pkg/da/jsonrpc`, kết nối qua `DA_RPC`/`DA_BRIDGE_RPC` + `DA_AUTH_TOKEN`). Namespace bytes tính sẵn trong `NewClient` (:46).
4. `processBlobs` → parse blob thành `header`/`data`, phát ra `DAHeightEvent` (Source = `SourceDA`).
5. Mỗi event vào `processHeightEvent` (`syncer.go:527`):
   - verify chữ ký sequencer + state,
   - `VerifyForcedInclusionTxs` nếu bật (chỉ với block nguồn DA, :715),
   - `ApplyBlock` (:729) → chạy lại tx qua executor để tính `app_hash`,
   - cập nhật `newState.DAHeight` (:736), rồi `SaveBlockData`+`SetHeight`+`UpdateState`+`Commit` (mục 5b.3).
6. `daClient.GetLatestDAHeight` (`syncer.go:214`) cho biết đầu DA hiện tại để biết đã sync kịp đầu chưa (`HasReachedDAHead`, :416).

Khôi phục sau khi xóa local: store rỗng → `initializeState` đặt `daRetrieverHeight = genesis.DAStartHeight` → retriever quét từ đầu, apply tuần tự, dựng lại toàn bộ store. Không cần P2P (P2P chỉ giúp bắt kịp nhanh hơn trước khi blob lên DA).

Phía **ghi lên** Celestia (sequencer) — đối xứng: `submitting/submitter.go:163 daSubmissionLoop` (ticker) → `DASubmitter.SubmitHeaders` (`da_submitter.go:212`) và `SubmitData` → `client.Submit` (`da/client.go:72`) → JSON-RPC `blob.Submit`. `processDAInclusionLoop` (:308) theo dõi blob đã được DA include để `SetFinal`.

### 5b.6 Node key & signer key — sinh ra thế nào, lấy key từ đâu

Mỗi node có **2 cặp khoá tách biệt**, đều nằm trong `<home>/config/` và **đều sinh lúc `evcosmos init`** ([apps/cosmos-wasm/cmd/init.go](../../../../cosmos-wasm/cmd/init.go)). Đừng nhầm hai cái:

| | **signer key** (`signer.json`) | **node key** (`node_key.json`) |
|---|---|---|
| Là gì | **Danh tính đồng thuận** — ký header block | **Danh tính mạng** — peer ID libp2p (P2P) |
| Ai có | **Chỉ sequencer** (aggregator) | **Mọi node** (sequencer + full node) |
| Sinh bằng | [`CreateSigner`](../../../../../pkg/cmd/init.go#L15) → [`CreateFileSystemSigner`](../../../../../pkg/signer/file/local.go#L39) | [`LoadOrGenNodeKey`](../../../../../pkg/cmd/init.go#L53) → [`key.LoadOrGenNodeKey`](../../../../../pkg/p2p/key/nodekey.go#L132) |
| Thuật toán | **Ed25519**, sinh ngẫu nhiên (`crypto.GenerateKeyPair(Ed25519)`, dùng `crypto/rand`) | **Ed25519**, sinh ngẫu nhiên (cùng hàm) |
| Mã hoá ở đĩa? | **Có** — privkey mã hoá **AES‑256‑GCM**, khoá dẫn từ passphrase qua **Argon2id** (t=3, mem=32MB, threads=4) + salt 16B ngẫu nhiên ([local.go:240](../../../../../pkg/signer/file/local.go#L240)) | **Không** — privkey lưu **thô** (chỉ bảo vệ bằng quyền file `0600`) ([nodekey.go:83](../../../../../pkg/p2p/key/nodekey.go#L83)) |
| Cần passphrase? | **Có** — `--evnode.signer.passphrase_file` (dev: `passphrase.txt`="secret"; prod: file thật) | Không |
| Trường file | `priv_key_encrypted`, `nonce`, `pub_key`, `salt` | `priv_key`, `pub_key` (đều raw) |
| Sinh ra cái gì dùng tiếp | **`proposer_address`** trong genesis = `SumTruncated(pubkey)` = SHA256(pubkey) ([init.go:43](../../../../../pkg/cmd/init.go#L43)) | **Peer ID** = `hex(SumTruncated(pubkey))` ([nodekey.go:104](../../../../../pkg/p2p/key/nodekey.go#L104)) — phần định danh trong `--evnode.p2p.peers` |

**"Lấy key từ đâu?"** — **không** từ mnemonic/seed hay ví ngoài. Cả hai sinh **ngẫu nhiên tại chỗ** lúc init bằng `crypto/rand` (libp2p Ed25519). Hệ quả:

- Mỗi lần init mới (vd `--clean-on-start=true` xoá home rồi init lại) ⇒ **key mới** ⇒ `proposer_address` mới ⇒ genesis cũ giữ ngoài sẽ **lệch** (xem lý do nên/không nên giữ genesis ngoài). Muốn key cố định: `--clean-on-start=false` (init thấy `signer.json` đã có thì **skip**, giữ nguyên key — [run script initNodes](../../../../../scripts/run-cosmos-wasm-nodes.go#L473)).
- **Backup/khôi phục signer key:** [`ExportPrivateKey`](../../../../../pkg/signer/file/local.go#L110) / [`ImportPrivateKey`](../../../../../pkg/signer/file/local.go#L158) (privkey thô + passphrase). Mất `signer.json` của sequencer = mất quyền ký ⇒ phải đổi proposer (đổi genesis). Mất `node_key.json` chỉ làm node đổi peer ID — không mất chain.

**Luồng init đầy đủ** (run-script gọi `evcosmos init` cho từng node, [initNodes](../../../../../scripts/run-cosmos-wasm-nodes.go#L469)):

```
evcosmos init --home <home> --chain_id <id> [--evnode.node.aggregator] --evnode.signer.passphrase_file <pf>
  ├─ CreateSigner(...)        # CHỈ nếu aggregator + signer=file → tạo config/signer.json, trả proposerAddress
  ├─ LoadOrGenNodeKey(home)   # mọi node → tạo config/node_key.json
  └─ CreateGenesis(home, chainID, 1, proposerAddress)   # ghi config/genesis.json (proposer_address từ signer; full node = null)
```

Full node init với `aggregator=false` ⇒ không có signer ⇒ genesis của nó có `proposer_address: null`, rồi bị **đè bằng genesis copy từ sequencer** ([§4](#off-chain-local-trên-máy-node--cachestate-để-chạy)). Đó là lý do full node vẫn biết proposer hợp lệ để verify chữ ký dù không có signer key.

> Liên hệ: signer key ⇄ `proposer_address` ⇄ `signer.json` là cái [sequencer-security.md](sequencer-security.md) gọi là khoá ký của single sequencer; node key ⇄ peer ID là tầng P2P ở [§5b.4](#5b4-p2p-bằng-filefunction-nào--hai-tầng).

## 6. Sequencer khác full node — tóm tắt

| | Sequencer | Full node |
|---|---|---|
| `--evnode.node.aggregator` | `true` | `false` |
| Vai trò | Tạo block, ký, **submit lên DA** | Đọc block từ **DA + P2P**, chạy lại verify |
| `--evnode.p2p.peers` | (không) | trỏ tới sequencer |
| Mempool | có (single sequencer) | không (chỉ verify) |
| Mất nó thì | chain **ngừng ra block** | chỉ mất một bản sao verify; chain vẫn chạy |

Chi tiết bảo mật của mô hình này: [sequencer-security.md](sequencer-security.md).

## 7. Có phải trả tiền cho sequencer không?

**Không — không có khoản trả cho sequencer ở cấp giao thức.** Sequencer khác hẳn validator của Cosmos chain thường:

- **Không staking reward, không lạm phát, không sequencer tip.** Không có `x/mint`, không có code path phân phối thưởng cho sequencer (xem [fee-economics.md mục 2a](fee-economics.md)).
- Single sequencer = **hạ tầng của chính operator**: chỉ là tiến trình `evcosmos --aggregator=true` + `cosmos-exec-grpc` chạy trên máy bạn. Bạn **vận hành** nó, không **trả** nó.

Cái thực sự tốn tiền khi vận hành sequencer:

| Khoản | Trả cho ai |
|-------|-----------|
| Server/hạ tầng | Cloud/VPS chạy 2 tiến trình |
| **Blob fee Celestia** | Celestia (TIA) — sequencer submit header/data blob mỗi block; trả bằng key ký DA (`DA_AUTH_TOKEN` / signing address trong DA config) |

→ Không trả "phí sequencer", mà trả **hóa đơn Celestia do sequencer phát sinh**. Operator gánh (có thể tính vào fee user nếu bật fee).

Phí giao dịch user (khi bật fee) chảy vào module account `fee_collector` — **không** tự động payout cho sequencer như validator nhận block reward; operator quản khoản này để bù hóa đơn DA.

**Based sequencer** (`--evnode.node.based_sequencer=true`): không có tiến trình sequencer sắp xếp tx → càng không có khái niệm "trả cho sequencer", chỉ còn chi phí DA.

## Tham chiếu code

| Chủ đề | File / function |
|--------|------|
| Khởi chạy stack, ports, DA env, home dirs | `scripts/run-cosmos-wasm-nodes.go` |
| Tên cờ + default ev-node | `pkg/config/config.go`, `pkg/config/defaults.go` |
| **Điểm vào node** (mở store, sequencer, StartNode) | `apps/cosmos-wasm/cmd/run.go:RunE`, `pkg/cmd/run_node.go:StartNode` |
| **Tạo node / chọn mode** | `node/node.go:NewNode`, `node/full.go` (`newAggregatorMode`/`newSyncMode`/`Run`) |
| **Wiring block components** | `block/components.go` (`NewSyncComponents`, `newAggregatorComponents`) |
| **Mở badger / store đồng thuận** | `pkg/store/kv.go:NewDefaultKVStore`, `pkg/store/store.go:New` |
| **Lưu block (atomic batch)** | `pkg/store/batch.go` (`SaveBlockData`, `SetHeight`), key prefix ở `pkg/store/keys.go` |
| **Sản xuất block (sequencer)** | `block/internal/executing/executor.go` (`ProduceBlock`, `CreateBlock`, `ApplyBlock`) |
| **Submit lên Celestia** | `block/internal/submitting/submitter.go` (`daSubmissionLoop`), `submitting/da_submitter.go` (`SubmitHeaders`/`SubmitData`) |
| **Sync block từ DA** | `block/internal/syncing/syncer.go` (`Start`, `processHeightEvent`), `syncing/da_retriever.go` (`RetrieveFromDA`/`fetchBlobs`) |
| **DA client JSON-RPC (Celestia)** | `block/internal/da/client.go` (`Retrieve`/`Submit`), `pkg/da/jsonrpc` |
| **P2P discovery/transport** | `pkg/p2p/client.go` (`NewClient`, `Start`, `setupDHT`, `setupGossiping`) |
| **P2P header/data sync (go-header)** | `pkg/sync/sync_service.go`, tiêu thụ ở `block/internal/syncing/p2p_handler.go` |
| Persist off-chain (metadata/tx/blocks) | `apps/cosmos-exec/executor/persist.go` |
| Genesis (treasury) | `apps/cosmos-exec/app/genesis.go` |
| Env execution/phí/faucet | `apps/cosmos-exec/app/ante.go`, `cmd/cosmos-exec-grpc/cost.go`, `cmd/cosmos-exec-grpc/faucet.go` |
| Flags cosmos-exec-grpc | `apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go` |
