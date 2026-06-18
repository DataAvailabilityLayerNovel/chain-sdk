# Getting Started: Deploy Chain & Deploy Your CosmWasm Contract

Hướng dẫn từ đầu đến cuối cho developer muốn:

1. Viết smart contract bằng Rust
2. Compile ra `.wasm`
3. Start rollup chain trên Celestia DA
4. Deploy contract lên chain
5. Interact qua SDK

---

## Mục lục

- Phase 1 — Chuẩn bị môi trường
- Phase 2 — Viết và compile CosmWasm contract
- Phase 3 — Tạo Go project sử dụng SDK
- Phase 4 — Start chain
- Phase 4.5 — Cấu hình DA Start Height & Query Celestia
- Phase 5 — Deploy contract + Interact
- Phase 6 (Optional) — Chạy không cần Celestia (dev mode)
- Phase 7 (Optional) — Swagger UI & API Explorer
- Tóm tắt: tất cả files cần tạo
- Tóm tắt flow
- Troubleshooting
- What's next

## Phase 1 — Chuẩn bị môi trường

### 1.1. Cài đặt tools

```bash
# Go 1.25.6+
go version

# Rust + wasm target (để compile contract)
rustup target add wasm32-unknown-unknown

# (Optional) wasm optimizer
docker pull cosmwasm/rust-optimizer:0.16.0
```

### 1.2. Clone ev-node repo

```bash
git clone https://github.com/DataAvailabilityLayerNovel/chain-sdk.git ev-node
cd ev-node
```

Verify:

```bash
ls scripts/run-cosmos-wasm-nodes.go   # phải tồn tại
ls apps/cosmos-exec/sdk/cosmoswasm/   # SDK package
```

### 1.3. Chạy Celestia light node

SDK cần một Celestia light node để submit data lên DA layer.

```bash
# Cài celestia node (xem https://docs.celestia.org)
celestia light start --p2p.network mocha

# Lấy auth token
celestia light auth admin --p2p.network mocha
# Output: eyJhbGciOiJIUzI1NiIs...  ← copy token này

# Verify node chạy
curl -s http://localhost:26658/header/1
```

---

## Phase 2 — Viết và compile CosmWasm contract

### 2.1. Tạo contract project

Nếu chưa có contract, tạo contract counter đơn giản:

```bash
# Tạo từ template
cargo generate --git https://github.com/CosmWasm/cw-template.git --name my-counter
cd my-counter
```
hoặc

```bash
# filepath:
git clone https://github.com/CosmWasm/cw-template.git my-counter
cd my-counter
```
Project structure:

```
my-counter/
├── Cargo.toml
├── src/
│   ├── contract.rs      # logic: instantiate, execute, query
│   ├── msg.rs           # InstantiateMsg, ExecuteMsg, QueryMsg
│   ├── state.rs         # contract state (count, owner)
│   ├── error.rs         # custom errors
│   └── lib.rs           # entry point
```

### 2.2. Ví dụ contract counter

**src/msg.rs:**

```rust
use cosmwasm_schema::{cw_serde, QueryResponses};

#[cw_serde]
pub struct InstantiateMsg {
    pub count: i32,
}

#[cw_serde]
pub enum ExecuteMsg {
    Increment {},
    Reset { count: i32 },
}

#[cw_serde]
#[derive(QueryResponses)]
pub enum QueryMsg {
    #[returns(CountResponse)]
    GetCount {},
}

#[cw_serde]
pub struct CountResponse {
    pub count: i32,
}
```

**src/state.rs:**

```rust
use cosmwasm_std::Addr;
use cw_storage_plus::Item;

pub const COUNT: Item<i32> = Item::new("count");
pub const OWNER: Item<Addr> = Item::new("owner");
```

**src/contract.rs:**

```rust
use cosmwasm_std::{
    entry_point, to_json_binary, Binary, Deps, DepsMut, Env, MessageInfo, Response, StdResult,
};
use crate::msg::{CountResponse, ExecuteMsg, InstantiateMsg, QueryMsg};
use crate::state::{COUNT, OWNER};

#[entry_point]
pub fn instantiate(
    deps: DepsMut, _env: Env, info: MessageInfo, msg: InstantiateMsg,
) -> StdResult<Response> {
    COUNT.save(deps.storage, &msg.count)?;
    OWNER.save(deps.storage, &info.sender)?;
    Ok(Response::new().add_attribute("method", "instantiate").add_attribute("count", msg.count.to_string()))
}

#[entry_point]
pub fn execute(
    deps: DepsMut, _env: Env, _info: MessageInfo, msg: ExecuteMsg,
) -> StdResult<Response> {
    match msg {
        ExecuteMsg::Increment {} => {
            COUNT.update(deps.storage, |c| -> StdResult<_> { Ok(c + 1) })?;
            Ok(Response::new().add_attribute("action", "increment"))
        }
        ExecuteMsg::Reset { count } => {
            COUNT.save(deps.storage, &count)?;
            Ok(Response::new().add_attribute("action", "reset"))
        }
    }
}

#[entry_point]
pub fn query(deps: Deps, _env: Env, msg: QueryMsg) -> StdResult<Binary> {
    match msg {
        QueryMsg::GetCount {} => {
            let count = COUNT.load(deps.storage)?;
            to_json_binary(&CountResponse { count })
        }
    }
}
```

### 2.3. Compile sang .wasm

```bash
cd my-counter

# Compile
cargo wasm
# Output: target/wasm32-unknown-unknown/release/my_counter.wasm

# (Recommended) Optimize — giảm size ~10x
docker run --rm -v "$(pwd)":/code cosmwasm/rust-optimizer:0.16.0
# Output: artifacts/my_counter.wasm  (~200KB thay vì ~2MB)
```

Verify file .wasm:

```bash
ls -lh artifacts/my_counter.wasm
file artifacts/my_counter.wasm
# Output: WebAssembly (wasm) binary module version 0x1 (MVP)
```

---

## Phase 3 — Tạo Go project sử dụng SDK

### 3.1. Tạo project structure

```bash
mkdir my-dapp && cd my-dapp
go mod init my-dapp
```

```bash
# Cài SDK
go get github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm
```

### 3.2. Tạo project structure — từng bước chi tiết

Mục tiêu tạo ra cấu trúc sau:

```
my-dapp/
├── go.mod                       # Go module file
├── go.sum                       # (tự generate khi go mod tidy)
├── .env                         # Celestia config
├── artifacts/
│   └── my_counter.wasm          # Contract compiled ở Phase 2
└── main.go                      # App code (deploy + interact)
```

#### Bước 1: Tạo thư mục project

```bash
# Tạo thư mục gốc
mkdir my-dapp
cd my-dapp
```

#### Bước 2: Khởi tạo Go module

```bash
go mod init my-dapp
```

Lệnh này tạo file `go.mod` với nội dung:

```
module my-dapp

go 1.25.6
```

#### Bước 3: Cài SDK dependency

```bash
go get github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm
```

Lệnh này:
- Thêm `require github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm vX.X.X` vào `go.mod`
- Tạo file `go.sum` (chứa checksums của tất cả dependencies)

> **Nếu develop local** (chưa publish lên GitHub), dùng `replace` directive thay thế:
>
> ```bash
> # Thêm vào cuối go.mod
> echo 'replace github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec => /path/to/ev-node/apps/cosmos-exec' >> go.mod
> go mod tidy
> ```

#### Bước 4: Tạo thư mục artifacts và copy contract

```bash
# Tạo thư mục chứa file .wasm
mkdir -p artifacts

# Copy file .wasm đã compile ở Phase 2
cp /path/to/my-counter/artifacts/my_counter.wasm artifacts/

# Verify
ls -lh artifacts/my_counter.wasm
# Output: -rw-r--r--  1 user  staff   200K  my_counter.wasm
```

> **Chưa có contract?** Có thể skip bước này và chỉ dùng blob/query features trước.
> Xem [examples/game-telemetry](../examples/game-telemetry/) — blob-first end-to-end.

#### Bước 5: Tạo file .env

```bash
cat > .env << 'EOF'
# Celestia light node RPC endpoint
DA_BRIDGE_RPC=http://localhost:26658

# Auth token (lấy từ: celestia light auth admin --p2p.network mocha)
DA_AUTH_TOKEN=eyJhbGciOiJIUzI1NiIs...

# Namespace riêng cho app (cô lập data trên DA layer)
DA_NAMESPACE=my-counter-app
EOF
```

| Variable | Mô tả | Lấy ở đâu |
|----------|-------|------------|
| `DA_BRIDGE_RPC` | Celestia light node RPC | Mặc định `http://localhost:26658` |
| `DA_AUTH_TOKEN` | Auth token cho Celestia | Chạy `celestia light auth admin --p2p.network mocha` |
| `DA_NAMESPACE` | Namespace riêng cho app (cô lập data trên DA layer) | Tự đặt, ví dụ `my-counter-app` |

> **Lưu ý:** Thêm `.env` vào `.gitignore` để không commit secret lên git:
> ```bash
> echo ".env" >> .gitignore
> ```

#### Bước 6: Tạo file main.go

```bash
touch main.go
```

Mở `main.go` bằng editor, paste code ở [Phase 5 — Section 5.1](#51-file-maingo-hoàn-chỉnh) bên dưới.

Hoặc tạo file tối giản để verify SDK hoạt động:

```go
// main.go — verify SDK connection
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
)

func main() {
    client := cosmoswasm.NewClient("http://127.0.0.1:50051")
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    // Test 1: smoke test — kết nối executor
    status, err := client.Status(ctx)
    if err != nil {
        log.Fatalf("status failed: %v", err)
    }
    fmt.Printf("Connected! chain=%s height=%d\n", status.ChainID, status.LatestHeight)

    // Test 2: blob-first (cần Celestia bridge) — SubmitBlob/RetrieveBlob nằm
    // trên BlobClient (JSON-RPC tới Celestia), KHÔNG phải Client.
    bc, err := cosmoswasm.NewBlobClient(ctx, cosmoswasm.BlobClientConfig{
        BridgeRPC: "http://localhost:26658",
        Namespace: "my-dapp",
    })
    if err != nil {
        log.Fatalf("blob client: %v", err)
    }
    defer bc.Close()

    res, err := bc.SubmitBlob(ctx, []byte("Hello from my-dapp!"))
    if err != nil {
        log.Fatalf("submit blob failed: %v", err)
    }
    fmt.Printf("Blob stored! commitment=%s height=%d\n", res.Commitment, res.Height)

    // Retrieve cần CẢ height lẫn commitment
    data, err := bc.RetrieveBlob(ctx, res.Height, res.Commitment)
    if err != nil {
        log.Fatalf("retrieve blob failed: %v", err)
    }
    fmt.Printf("Retrieved: %s\n", string(data))
}
```

#### Bước 7: Download dependencies và verify

```bash
# Download tất cả dependencies
go mod tidy

# Verify build thành công
go build .

# (Optional) Chạy thử — cần chain đang chạy (Phase 4)
go run main.go
```

#### Kết quả: cấu trúc project hoàn chỉnh

```bash
$ tree my-dapp/
my-dapp/
├── .env                         # ← Bước 5: Celestia config
├── .gitignore                   # ← Bước 5: ignore .env
├── artifacts/
│   └── my_counter.wasm          # ← Bước 4: contract bytecode (200KB)
├── go.mod                       # ← Bước 2+3: module + SDK dependency
├── go.sum                       # ← Bước 7: auto-generated checksums
└── main.go                      # ← Bước 6: app code

$ cat go.mod
module my-dapp

go 1.25.6

require (
    github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec v0.3.0
)
```

#### Tóm tắt lệnh (copy-paste chạy liền)

```bash
# === Tạo project trong 7 lệnh ===
mkdir my-dapp && cd my-dapp
go mod init my-dapp
go get github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm
mkdir -p artifacts
cp /path/to/my-counter/artifacts/my_counter.wasm artifacts/
cat > .env << 'EOF'
DA_BRIDGE_RPC=http://localhost:26658
DA_AUTH_TOKEN=<paste-token-here>
DA_NAMESPACE=my-counter-app
EOF
echo ".env" >> .gitignore
# → Tạo main.go (xem Phase 5)
# → go mod tidy && go run main.go
```

---

## Phase 4 — Start chain

Có 2 cách:

### Cách A: Start chain bằng script (terminal riêng)

Cần clone ev-node repo (Phase 1.2). Chạy ở terminal riêng:

```bash
cd /path/to/ev-node

# Load .env
export $(cat /path/to/my-dapp/.env | xargs)


pkill -f cosmos-exec-grpc; pkill -f evcosmos

# Start full E2E stack
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go \
  --clean-on-start=true \
  --block-time=2s
```

Chờ log:

```
Cosmos/WASM stack is running
- sequencer execution gRPC: http://127.0.0.1:50051
- full execution gRPC: http://127.0.0.1:50052
```

Verify:

```bash
curl -s http://127.0.0.1:50051/status | python3 -m json.tool
# "initialized": true, "healthy": true, "latest_height" tăng dần
```

### Cách B: Start chain từ trong Go code (programmatic)

Không cần terminal riêng — chain chạy cùng process với app:

```go
// Trong main.go
cfg := cosmoswasm.DefaultDALChainConfig("/path/to/ev-node")
cfg.ChainName    = "my-counter-chain"
cfg.Namespace    = os.Getenv("DA_NAMESPACE")
cfg.DABridgeRPC  = os.Getenv("DA_BRIDGE_RPC")
cfg.DAAuthToken  = os.Getenv("DA_AUTH_TOKEN")
cfg.CleanOnStart = true
cfg.BlockTime    = 2 * time.Second

proc, err := cosmoswasm.StartDALChain(ctx, cfg)
if err != nil {
    log.Fatal(err)
}
defer proc.Stop()

// Chain đã ready, dùng proc.Endpoints.SequencerExecAPI làm URL
client := cosmoswasm.NewClient(proc.Endpoints.SequencerExecAPI)
```

> **Lưu ý:** Cách B cần ev-node repo đã clone (vì `DefaultDALChainConfig` tham chiếu tới `scripts/run-cosmos-wasm-nodes.go`). Path truyền vào là đường dẫn tới thư mục gốc của ev-node repo.

---

## Phase 4.5 — Cấu hình DA Start Height & Query Celestia

Khi chạy fullnode để sync từ sequencer, fullnode cần biết **DA height mà sequencer bắt đầu submit blocks**. Nếu không set đúng, fullnode sẽ báo lỗi:

```
ERR failed to get blobs error="height is equal to 0"
WRN catchup error, backing off error="DA retrieval failed: failed to get blobs: height is equal to 0"
```

### 4.5.1. Tìm DA start height

DA start height = Celestia block height mà sequencer submit blob đầu tiên.

**Cách 1: Xem log sequencer**

```bash
grep "successfully submitted" .logs/cosmos-wasm-chain.log | head -1
# Output: ... da_height=620042 ...
```

**Cách 2: Dùng script `verify-da-submit.sh`** — scan tự động tìm blobs

```bash
# Scan 80 DA heights gần nhất tìm blobs thuộc namespace trong .env
./scripts/verify-da-submit.sh

# Output:
# [run][da-check] da_url=http://103.67.203.71:26758
# [run][da-check] namespace_input=rollup
# [run][da-check] scan_range=620000..620080
# [ok][da-check] blobs_found_at_da_height=620042
```

Tăng range nếu cần:

```bash
SCAN_RANGE=500 ./scripts/verify-da-submit.sh
```

**Cách 3: Dùng script `query_celestia_blob_range.sh`** — scan range cụ thể

```bash
./scripts/query_celestia_blob_range.sh --from-height 620000 --to-height 620100

# Output cho mỗi height có data:
# [h=620042] blobs=1
#   - blob[0] decoded:
#     {"header":{"height":1,"time":"2026-05-04T10:00:00Z",...}}
```

### 4.5.2. Set DA start height trong config

Khi start fullnode, truyền `da_start_height` = height tìm được ở bước trên:

```bash
# Trong genesis.json hoặc config
{
  "da_start_height": 620042
}
```

Hoặc qua script start node:

```bash
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go \
  --da-start-height=620042 \
  --clean-on-start=true \
  --block-time=2s
```

### 4.5.3. Query và verify data trên Celestia

Repo có sẵn các scripts trong `scripts/`. Tất cả đều tự đọc `.env` (lấy `DA_BRIDGE_RPC`, `DA_AUTH_TOKEN`, `DA_NAMESPACE`).

#### Script 1: `query_celestia_blob.sh` — Query blob tại 1 DA height

```bash
# Query theo DA height trực tiếp
./scripts/query_celestia_blob.sh --height 620042

# Query theo tx hash (tự resolve DA height từ chain)
./scripts/query_celestia_blob.sh --tx-hash C1AEC991E34C280429DE751ED7DDBBC202EF0C07

# Query theo ev-node block height
./scripts/query_celestia_blob.sh --block-height 42

# Query latest (lấy DA height mới nhất từ chain log hoặc chain RPC)
./scripts/query_celestia_blob.sh --latest
```

Output mẫu:

```
🔍 Querying Celestia blob...
   DA Height: 620042
   Source: explicit height
   Namespace: AAAAAAAAAAAAAAAAAAAAAAAAAAByb2xsdXA=
   RPC: http://103.67.203.71:26758

📦 Response:
{ "result": [{ "namespace": "...", "data": "eyJoZWFkZXI...", "share_version": 0 }] }

📝 Decoded data:
--- blob[0] ---
{
  "header": { "height": 1, "time": "2026-05-04T10:00:00Z", "chain_id": "cosmos-wasm-test" },
  "num_txs": 3
}
```

#### Script 2: `query_celestia_blob_range.sh` — Scan nhiều heights

```bash
# Tìm tất cả blobs trong range
./scripts/query_celestia_blob_range.sh --from-height 620040 --to-height 620060

# Override namespace (base64)
./scripts/query_celestia_blob_range.sh \
  --from-height 620040 --to-height 620060 \
  --namespace "AAAAAAAAAAAAAAAAAAAAAAAAAAByb2xsdXA="
```

#### Script 3: `watch_celestia_latest_blobs.sh` — Watch realtime

```bash
# Watch liên tục (mỗi 6s poll)
./scripts/watch_celestia_latest_blobs.sh

# Custom: poll mỗi 3s, backfill 30 heights ban đầu
./scripts/watch_celestia_latest_blobs.sh --interval 3 --backfill 30

# Bắt đầu từ height cụ thể
./scripts/watch_celestia_latest_blobs.sh --start-height 620040

# Chỉ query 1 lần rồi thoát
./scripts/watch_celestia_latest_blobs.sh --start-height 620042 --once
```

Output mẫu (realtime):

```
👀 Watching latest Celestia blobs
   RPC: http://103.67.203.71:26758
   Namespace: AAAAAAAAAAAAAAAAAAAAAAAAAAByb2xsdXA=
   Interval: 6s

[2026-05-04 17:30:12] [h=620042] blobs=1
  - blob[0] decoded:
    {"header":{"height":1},"num_txs":3}
[2026-05-04 17:30:18] [h=620048] blobs=1
  - blob[0] decoded:
    {"header":{"height":2},"num_txs":1}
```

#### Script 4: `query_celestia_proof.sh` — Get blob inclusion proof

```bash
# Get NMT inclusion proof cho blob đầu tiên tại height
./scripts/query_celestia_proof.sh --height 620042

# Get proof cho blob cụ thể (index 1)
./scripts/query_celestia_proof.sh --height 620042 --index 1

# Get proofs cho tất cả blobs tại height
./scripts/query_celestia_proof.sh --height 620042 --all

# Chỉ verify (exit 0 nếu proof valid, exit 1 nếu không)
./scripts/query_celestia_proof.sh --height 620042 --verify
```

Output mẫu:

```
🔐 Celestia Blob Inclusion Proof
   DA Height:  620042
   Namespace:  AAAAAAAAAAAAAAAAAAAAAAAAAAByb2xsdXA=
   Blobs:      1
   RPC:        http://103.67.203.71:26758

   Data Root:  a4f2c8e1b3d7...

--- Proof for blob[0] ---
  blob_index:    0
  commitment:    kLe3x/2B7qFd...
  blob_size:     1284 bytes (base64)
  proof_nodes:   3
  proof_valid:   true (non-empty NMT proof)
  share_range:   start=1 end=4
  proof_detail:
    - start=1 end=4 nodes=2

📋 Raw proof JSON (for programmatic use):
{ "jsonrpc": "2.0", "id": 1, "result": [...] }
```

#### Script 5: `query_celestia_status.sh` — Check Celestia node status

```bash
./scripts/query_celestia_status.sh
```

Output mẫu:

```
📡 Celestia DA Node Status
   RPC: http://103.67.203.71:26758

   Network Head:
     height: 620100
     time:   2026-05-04T17:30:00Z

   Local Head:
     height: 620098
     time:   2026-05-04T17:29:48Z

   Sync: ✅ synced (behind 2 blocks)

   Node:
     peer_id: 12D3KooWAbC1...
     addrs:   4

   DAS (Data Availability Sampling):
     sampled_head: 620095
     catchup_head: 620098
```

#### Script 6: `encode-namespace.sh` — Encode namespace

```bash
# Text → base64 namespace (29 bytes, zero-padded)
./scripts/encode-namespace.sh --text rollup
# Output: AAAAAAAAAAAAAAAAAAAAAAAAAAByb2xsdXA=

./scripts/encode-namespace.sh --text my-counter-app
# Output: AAAAAAAAAAAAAAAAAAG15LWNvdW50ZXItYXBw

# Hex → base64
./scripts/encode-namespace.sh --hex 00000000000000000000000000000000000000726F6C6C7570
# Output: AAAAAAAAAAAAAAAAAAAAAAAAAAByb2xsdXA=
```

#### Tổng hợp scripts

| Script | Mục đích |
|--------|----------|
| `scripts/verify-da-submit.sh` | Scan ngược từ head tìm DA height có blobs |
| `scripts/query_celestia_blob.sh` | Query 1 blob (theo height/tx-hash/block/latest) |
| `scripts/query_celestia_blob_range.sh` | Scan range nhiều heights |
| `scripts/watch_celestia_latest_blobs.sh` | Watch realtime blobs mới |
| `scripts/query_celestia_proof.sh` | Get NMT inclusion proof |
| `scripts/query_celestia_status.sh` | Check Celestia node sync status |
| `scripts/encode-namespace.sh` | Encode text/hex → base64 namespace |

### 4.5.4. Troubleshooting DA errors

| Lỗi | Nguyên nhân | Fix |
|-----|-------------|-----|
| `height is equal to 0` | `da_start_height` chưa set hoặc = 0 | Set đúng DA height (xem 4.5.1) |
| `Method not found (-32601)` | Celestia node không hỗ trợ method (wrong endpoint) | Dùng Bridge node RPC (`DA_BRIDGE_RPC`, port 26658), không phải Consensus RPC (port 26657) |
| `blob: not found` | Không có data tại height đó trong namespace | Chạy `./scripts/verify-da-submit.sh` kiểm tra |
| `header: given height is from the future` | DA height > chain head | Chờ Celestia sync thêm blocks |
| `context deadline exceeded` | Celestia node quá chậm hoặc không reachable | Kiểm tra network, tăng timeout |

### 4.5.5. Cấu hình .env cho DA

```bash
# === DA Layer Config ===
# Bridge RPC (dùng cho query blob) — port 26758 (bridge node)
DA_BRIDGE_RPC=http://103.67.203.71:26758

# Consensus RPC (dùng cho submit header) — port 14657/26657
DA_RPC=http://103.67.203.71:14657

# Auth token (lấy từ: celestia light auth admin)
DA_AUTH_TOKEN=eyJhbGciOiJIUzI1NiIs...

# Namespace — có thể dùng hex hoặc base64:
DA_NAMESPACE=00000000000000000000000000000000000000726F6C6C7570
DA_NAMESPACE_B64=AAAAAAAAAAAAAAAAAAAAAAAAAAByb2xsdXA=

# Encode namespace từ text:
# ./scripts/encode-namespace.sh --text rollup
# → AAAAAAAAAAAAAAAAAAAAAAAAAAByb2xsdXA=
```

---

## Phase 5 — Deploy contract + Interact

### 5.1. File main.go hoàn chỉnh

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"
    "strconv"
    "strings"
    "time"

    cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
)

func main() {
    client := cosmoswasm.NewClient("http://127.0.0.1:50051")
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
    defer cancel()

    // ──────────────────────────────────────────────────────────
    // Step 1: Store WASM code
    // ──────────────────────────────────────────────────────────
    fmt.Println("Step 1 — Store WASM code")

    wasmBytes, err := os.ReadFile("artifacts/my_counter.wasm")
    if err != nil {
        log.Fatalf("read wasm file: %v", err)
    }
    fmt.Printf("  wasm size: %d bytes\n", len(wasmBytes))

    storeTx, err := cosmoswasm.BuildStoreTx(wasmBytes, "")
    if err != nil {
        log.Fatalf("build store tx: %v", err)
    }

    storeRes, err := client.SubmitTxBytes(ctx, storeTx)
    if err != nil {
        log.Fatalf("submit store tx: %v", err)
    }
    fmt.Printf("  tx_hash: %s\n", storeRes.Hash)

    storeResult, err := client.WaitTxResult(ctx, storeRes.Hash, time.Second)
    if err != nil {
        log.Fatalf("wait store result: %v", err)
    }
    if storeResult.Code != 0 {
        log.Fatalf("store failed: code=%d log=%s", storeResult.Code, storeResult.Log)
    }

    codeIDStr := findEventValue(storeResult.Events, "code_id")
    codeID, _ := strconv.ParseUint(codeIDStr, 10, 64)
    fmt.Printf("  code_id: %d (height=%d)\n", codeID, storeResult.Height)

    // ──────────────────────────────────────────────────────────
    // Step 2: Instantiate contract
    // ──────────────────────────────────────────────────────────
    fmt.Println("\nStep 2 — Instantiate contract")

    initTx, err := cosmoswasm.BuildInstantiateTx(cosmoswasm.InstantiateTxRequest{
        CodeID: codeID,
        Label:  "my-counter-instance",
        Msg:    map[string]any{"count": 0},  // ← matches Rust InstantiateMsg
    })
    if err != nil {
        log.Fatalf("build instantiate tx: %v", err)
    }

    initRes, err := client.SubmitTxBytes(ctx, initTx)
    if err != nil {
        log.Fatalf("submit instantiate tx: %v", err)
    }

    initResult, err := client.WaitTxResult(ctx, initRes.Hash, time.Second)
    if err != nil {
        log.Fatalf("wait instantiate result: %v", err)
    }
    if initResult.Code != 0 {
        log.Fatalf("instantiate failed: code=%d log=%s", initResult.Code, initResult.Log)
    }

    contractAddr := findEventValue(initResult.Events, "_contract_address")
    if contractAddr == "" {
        contractAddr = findEventValue(initResult.Events, "contract_address")
    }
    fmt.Printf("  contract: %s (height=%d)\n", contractAddr, initResult.Height)

    // ──────────────────────────────────────────────────────────
    // Step 3: Execute — increment counter
    // ──────────────────────────────────────────────────────────
    fmt.Println("\nStep 3 — Execute: increment")

    execTx, err := cosmoswasm.BuildExecuteTx(cosmoswasm.ExecuteTxRequest{
        Contract: contractAddr,
        Msg:      map[string]any{"increment": struct{}{}},  // ← matches Rust ExecuteMsg
    })
    if err != nil {
        log.Fatalf("build execute tx: %v", err)
    }

    execRes, err := client.SubmitTxBytes(ctx, execTx)
    if err != nil {
        log.Fatalf("submit execute tx: %v", err)
    }

    execResult, err := client.WaitTxResult(ctx, execRes.Hash, time.Second)
    if err != nil {
        log.Fatalf("wait execute result: %v", err)
    }
    if execResult.Code != 0 {
        log.Fatalf("execute failed: code=%d log=%s", execResult.Code, execResult.Log)
    }
    fmt.Printf("  success at height=%d\n", execResult.Height)

    // ──────────────────────────────────────────────────────────
    // Step 4: Query — get count
    // ──────────────────────────────────────────────────────────
    fmt.Println("\nStep 4 — Query: get_count")

    result, err := client.QuerySmart(ctx, contractAddr, map[string]any{
        "get_count": struct{}{},  // ← matches Rust QueryMsg
    })
    if err != nil {
        log.Fatalf("query: %v", err)
    }
    fmt.Printf("  count = %v\n", result["count"])

    // ──────────────────────────────────────────────────────────
    // Step 5: Store data off-chain (blob-first pattern)
    // ──────────────────────────────────────────────────────────
    fmt.Println("\nStep 5 — Blob-first: store events off-chain")

    events := [][]byte{
        []byte(`{"event":"game_start","ts":1}`),
        []byte(`{"event":"player_move","ts":2,"x":10}`),
        []byte(`{"event":"game_end","ts":3,"score":9999}`),
    }

    // 5a. Upload N event lên Celestia trong 1 batch (qua BlobClient)
    bc, err := cosmoswasm.NewBlobClient(ctx, cosmoswasm.BlobClientConfig{
        BridgeRPC: "http://localhost:26658",
        Namespace: "game-events",
    })
    if err != nil {
        log.Fatalf("blob client: %v", err)
    }
    defer bc.Close()

    batch, err := bc.SubmitBatch(ctx, events)
    if err != nil {
        log.Fatalf("submit batch: %v", err)
    }
    fmt.Printf("  merkle_root: %s (height=%d, %d blobs)\n", batch.Root, batch.Height, batch.Count)

    // 5b. Ghi root on-chain (chỉ 1 tx cho N blob)
    rootTx, _ := cosmoswasm.BuildBatchRootTx(cosmoswasm.BatchRootTxRequest{
        Contract:  contractAddr,
        Root:      batch.Root,
        Height:    batch.Height,
        Namespace: bc.Namespace(),
        Count:     batch.Count,
        Tag:       "game-events",
    })
    rootResp, err := client.SubmitTxBytes(ctx, rootTx)
    if err != nil {
        log.Fatalf("commit root tx: %v", err)
    }
    fmt.Printf("  root recorded on-chain, tx_hash: %s\n", rootResp.Hash)

    // 5c. Verify Merkle proof (offline)
    proof, _ := cosmoswasm.BuildMerkleProof(batch.Commitments, 0)
    if err := cosmoswasm.VerifyMerkleProof(proof); err != nil {
        log.Fatalf("proof invalid: %v", err)
    }
    fmt.Println("  merkle proof verified")

    fmt.Println("\nAll steps passed.")
    // So sánh chi phí on-chain vs blob-first: xem fee-economics.md
}

func findEventValue(events []cosmoswasm.TxEvent, key string) string {
    for _, event := range events {
        for _, attr := range event.Attributes {
            if strings.TrimSpace(attr.Key) == key && strings.TrimSpace(attr.Value) != "" {
                return strings.TrimSpace(attr.Value)
            }
        }
    }
    return ""
}
```

### 5.2. Chạy

```bash
cd my-dapp

# Đảm bảo chain đang chạy (Phase 4)
curl -s http://127.0.0.1:50051/status

# Chạy app
go run main.go
```

Output kỳ vọng:

```
Step 1 — Store WASM code
  wasm size: 203847 bytes
  tx_hash: a1b2c3d4...
  code_id: 1 (height=3)

Step 2 — Instantiate contract
  contract: wasm1qg5ega6... (height=5)

Step 3 — Execute: increment
  success at height=7

Step 4 — Query: get_count
  count = 1

Step 5 — Blob-first: store events off-chain
  merkle_root: 9f8e7d6c...
  tx_hash: b2c3d4e5...
  blobs stored: 3 (off-chain)
  merkle proof verified

Step 6 — Cost comparison (1 MB data)
  direct on-chain: 41240000 gas
  blob + commit:   267240 gas (99% cheaper)

All steps passed.
```

---

## Phase 6 (Optional) — Chạy không cần Celestia (dev mode)

Nếu chưa setup Celestia, dùng quick-deploy với example có sẵn:

```bash
cd /path/to/ev-node

# Chạy example my-counter (deploy + tương tác counter contract)
export DA_BRIDGE_RPC=http://localhost:26658
export DA_AUTH_TOKEN=<token>
export DA_NAMESPACE=test

go run ./apps/cosmos-exec/sdk/cosmoswasm/examples/my-counter
```

Hoặc chạy một example có sẵn (`my-counter`, `forced-inclusion`,
`game-telemetry`):

```bash
# Start chain trước (terminal 1)
go run -tags run_cosmos_wasm ./scripts/run-cosmos-wasm-nodes.go --clean-on-start=true

# Chạy example (terminal 2)
go run ./apps/cosmos-exec/sdk/cosmoswasm/examples/game-telemetry
```

---

## Phase 7 (Optional) — Swagger UI & API Explorer

cosmos-exec-grpc có tích hợp sẵn **Swagger UI** (OpenAPI 3.0.3) để browse và test API trực tiếp từ trình duyệt.

### 7.1. Truy cập Swagger

Khi server đang chạy (mặc định port 50051):

```
Swagger UI:   http://127.0.0.1:50051/swagger
OpenAPI JSON: http://127.0.0.1:50051/swagger.json
```

Mở trình duyệt tại `http://127.0.0.1:50051/swagger` → giao diện Swagger UI hiện ra với tất cả endpoints.

### 7.2. Các nhóm API trên Swagger

| Tag | Endpoints | Mô tả |
|-----|-----------|-------|
| **node** | `GET /status`, `GET /blocks/latest`, `GET /blocks/{height}` | Trạng thái chain, thông tin block |
| **tx** | `POST /tx/submit`, `GET /tx/{hash}`, `GET /tx/result`, `GET /tx/pending` | Submit và tra cứu transaction |
| **wasm** | `POST /wasm/query-smart` | Query CosmWasm smart contract (read-only) |
| **account** | `GET /auth/account/{address}`, `GET /bank/balance/{address}` | Account number/sequence, số dư |

> Lưu ý: **không có** endpoint `/blob/*` trên cosmos-exec-grpc. Blob-first đi
> thẳng tới Celestia qua `BlobClient` (JSON-RPC), không qua HTTP của server này.

### 7.3. Test API từ Swagger UI

**Ví dụ 1 — Xem trạng thái chain:**
1. Mở Swagger UI
2. Tìm `GET /status` trong nhóm **node**
3. Click **Try it out** → **Execute**
4. Response:
```json
{
  "initialized": true,
  "chain_id": "cosmos-wasm-test-chain",
  "latest_height": 42,
  "finalized_height": 40,
  "healthy": true,
  "synced": true
}
```

**Ví dụ 2 — Tra account (peek number cho địa chỉ mới):**
1. Tìm `GET /auth/account/{address}` trong nhóm **account**
2. Nhập một bech32 address → **Execute**
3. Response:
```json
{
  "address": "cosmos1...",
  "account_number": 7,
  "sequence": 0,
  "exists": false
}
```

**Ví dụ 3 — Query smart contract:**
1. Tìm `POST /wasm/query-smart` trong nhóm **wasm**
2. Nhập body:
```json
{
  "contract": "cosmos14hj2tavq8fpesdwxxcu44rty3hh90vhujrvcmstl4zr3txmfvw9s4hmalr",
  "msg": {"get_count": {}}
}
```
3. Response:
```json
{
  "data": {"count": 5}
}
```

**Ví dụ 4 — Submit tx:**
1. Tìm `POST /tx/submit` trong nhóm **tx**
2. Nhập body `{"tx_base64": "..."}` (hoặc `{"tx_hex": "..."}`)
3. **Execute** → response `{"hash": "..."}`

### 7.4. Dùng Swagger JSON cho code generation

Download OpenAPI spec và generate client code tự động:

```bash
# Download spec
curl -s http://127.0.0.1:50051/swagger.json > openapi.json

# Generate TypeScript client (ví dụ dùng openapi-generator)
npx @openapitools/openapi-generator-cli generate \
  -i openapi.json \
  -g typescript-fetch \
  -o ./generated-client

# Generate Python client
npx @openapitools/openapi-generator-cli generate \
  -i openapi.json \
  -g python \
  -o ./generated-client-py
```

### 7.5. Test nhanh bằng curl (không cần Swagger UI)

```bash
# Status
curl -s http://127.0.0.1:50051/status | jq

# Account (peek number cho địa chỉ mới)
curl -s http://127.0.0.1:50051/auth/account/cosmos1... | jq

# Số dư
curl -s http://127.0.0.1:50051/bank/balance/cosmos1... | jq

# Query smart contract
curl -s -X POST http://127.0.0.1:50051/wasm/query-smart \
  -H "Content-Type: application/json" \
  -d '{"contract":"cosmos14hj...","msg":{"get_count":{}}}' | jq

# Blob-first: KHÔNG qua HTTP server này — dùng BlobClient (JSON-RPC tới Celestia).

# Submit transaction
curl -s -X POST http://127.0.0.1:50051/tx/submit \
  -H "Content-Type: application/json" \
  -d '{"tx_base64":"CpQBCp..."}' | jq

# Get tx result
curl -s "http://127.0.0.1:50051/tx/result?hash=abc123..." | jq

# Latest block
curl -s http://127.0.0.1:50051/blocks/latest | jq

# Block by height
curl -s http://127.0.0.1:50051/blocks/42 | jq

# Pending tx count
curl -s http://127.0.0.1:50051/tx/pending | jq
```

### 7.6. Swagger với auth token (production)

Khi server chạy với auth token (`--auth-token` hoặc `COSMOS_EXEC_AUTH_TOKEN`):

```bash
# Mọi request cần Bearer token
curl -s http://127.0.0.1:50051/status \
  -H "Authorization: Bearer my-secret-token" | jq

# Swagger UI vẫn accessible (GET endpoints)
# Nhưng POST endpoints cần thêm header Authorization
```

> **Lưu ý:** Swagger UI hiện chưa có ô nhập auth token tích hợp. Khi server bật auth, dùng curl hoặc Postman để test POST endpoints với header `Authorization: Bearer <token>`.

### 7.7. Source code Swagger

Swagger spec được generate programmatically trong Go (không dùng file YAML):

| File | Vai trò |
|------|---------|
| [`cmd/cosmos-exec-grpc/swagger.go`](../../../cmd/cosmos-exec-grpc/swagger.go) | OpenAPI 3.0.3 spec + Swagger UI HTML |
| [`cmd/cosmos-exec-grpc/main.go:121-122`](../../../cmd/cosmos-exec-grpc/main.go) | Register handlers: `/swagger` → UI, `/swagger.json` → spec |

```go
// main.go — handler registration
mux.HandleFunc("/swagger", swaggerUIHandler())
mux.HandleFunc("/swagger.json", swaggerJSONHandler())
```

Swagger UI load từ CDN (`unpkg.com/swagger-ui-dist@5`) — cần internet access lần đầu (browser cache sau đó).

---

## Tóm tắt: tất cả files cần tạo

### Phía contract (Rust)

```
my-counter/
├── Cargo.toml                   # [1] Rust dependencies
├── src/
│   ├── lib.rs                   # [2] Entry point
│   ├── msg.rs                   # [3] InstantiateMsg, ExecuteMsg, QueryMsg
│   ├── contract.rs              # [4] Business logic
│   ├── state.rs                 # [5] State definitions
│   └── error.rs                 # [6] Custom errors
└── artifacts/
    └── my_counter.wasm          # [output] Compiled contract
```

### Phía app (Go)

```
my-dapp/
├── go.mod                       # [1] go mod init my-dapp
├── .env                         # [2] DA_BRIDGE_RPC, DA_AUTH_TOKEN, DA_NAMESPACE
├── artifacts/
│   └── my_counter.wasm          # [3] Copy từ Rust project
└── main.go                      # [4] App code (deploy + interact)
```

### Phía infrastructure

```
ev-node/                         # [clone] git clone .../chain-sdk.git ev-node
├── scripts/
│   └── run-cosmos-wasm-nodes.go # Chain runner (dùng cho Phase 4 cách A)
├── apps/cosmos-exec/            # Execution engine (chain chạy ở đây)
└── apps/cosmos-wasm/            # Full node binary
```

### Celestia (external)

```
~/.celestia-light-mocha/         # Celestia light node data
# Chạy: celestia light start --p2p.network mocha
# Token: celestia light auth admin --p2p.network mocha
```

---

## Tóm tắt flow

```
[1] Rust contract  ──cargo wasm──→  my_counter.wasm
                                        │
                                        │ copy
                                        ▼
[2] Go app (my-dapp/main.go)     artifacts/my_counter.wasm
        │
        │ cosmoswasm.NewClient("http://127.0.0.1:50051")
        │ cosmoswasm.BuildStoreTx(wasmBytes)
        │ client.SubmitTxBytes(storeTx)
        │ cosmoswasm.BuildInstantiateTx(...)
        │ client.SubmitTxBytes(initTx)
        │ cosmoswasm.BuildExecuteTx(...)
        │ client.QuerySmart(...)
        │
        │ HTTP
        ▼
[3] cosmos-exec-grpc (port 50051) ← chạy từ ev-node scripts
        │
        ▼
[4] CosmosExecutor + App (WASM runtime)
        │
        ▼
[5] evcosmos (sequencer + P2P + block production)
        │
        │ DA submit
        ▼
[6] Celestia light node (port 26658)
```

---

## Troubleshooting

| Lỗi | Nguyên nhân | Fix |
|-----|-------------|-----|
| `DA_BRIDGE_RPC is required` | Chưa set env var | Tạo `.env` hoặc `export DA_BRIDGE_RPC=...` |
| `executor not reachable` | Chain chưa chạy | Start chain (Phase 4), verify `curl http://127.0.0.1:50051/status` |
| `store tx failed: code=2` | File .wasm không hợp lệ | Compile lại: `cargo wasm` hoặc dùng optimizer |
| `instantiate failed` | InitMsg sai format | Đảm bảo JSON matches Rust `InstantiateMsg` struct |
| `contract_address not found` | Event key khác | Thử cả `_contract_address` và `contract_address` |
| `timeout waiting for tx` | Block chưa produce | Check `latest_height` tăng chưa, tăng timeout |
| `cannot detect ev-node project root` | Path sai | Set `EVNODE_PROJECT_ROOT=/path/to/ev-node` |
| `wasm file too large` | .wasm chưa optimize | Dùng `cosmwasm/rust-optimizer` Docker image |

Chi tiết hơn: [Troubleshooting](troubleshooting.md) | [Error Handling](error-handling.md)

---

## What's next

| Mục tiêu | Guide |
|----------|-------|
| Hiểu kiến trúc toàn bộ stack | [Architecture](architecture.md) |
| Tune timeout, retry, auth | [Configuration](configuration.md) |
| Deploy production | [Production Guide](production-guide.md) |
| Xem tất cả API methods | [API Reference](api-reference.md) |
| Batch submit data lớn | `BlobClient.SubmitBatch` + `BuildBatchRootTx` trong [API Reference](api-reference.md) |
| Chạy examples có sẵn | [examples/](../examples/) — 3 runnable programs |
