# Vận hành node: sequencer, full node, data on/off-chain, biến ở đâu

Tài liệu này mô tả **cách stack khởi chạy thực tế**, **dữ liệu nằm đâu** (on-chain Celestia vs local), và **mọi biến cấu hình đọc từ chỗ nào** — bám đúng `scripts/run-cosmos-wasm-nodes.go` và code.

> Liên quan: [sequencer-security.md](sequencer-security.md) (single vs based), [chain-flow.md](chain-flow.md) (vòng đời tx/block), [fee-economics.md](fee-economics.md).

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

Hai thư mục home **riêng cho mỗi node**, đặt ở project root:

| Thư mục | Của | Chứa gì |
|---------|-----|---------|
| `.evcosmos-<name>/` | ev-node runtime | `config/genesis.json`, node config (yaml), signer key (passphrase từ `--evnode.signer.passphrase_file`), **store** (badger: headers, data, state, DA height đã xử lý) |
| `.cosmos-exec-<name>/data/` | execution (`cosmos-exec-grpc --home`) | `metadata.json` (chainID, stateRoot, heights — ghi atomic+fsync), `tx_results.jsonl` (append-only kết quả tx), `blocks.jsonl` (append-only block info), + CosmWasm state store |

`<name>` = `sequencer` hoặc `fullnode`. File bạn vừa mở `.cosmos-exec-fullnode/data/blocks.jsonl` chính là **block index off-chain của full node** (PersistStore, không phải dữ liệu đồng thuận — tái dựng được từ DA).

> Hệ quả: xóa các thư mục local = mất cache, **không mất chain** (sync lại từ Celestia). Nhưng đổi genesis/treasury chỉ áp khi **chưa có** state local — nên phải `--clean-on-start` (xóa home) để genesis mới có hiệu lực.

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
