# evcosmos — binary tầng đồng thuận của rollup Cosmos/WASM

Tài liệu này giải thích chi tiết binary `evcosmos`: nó là gì, nằm ở đâu, được
tạo ra như thế nào, dùng để làm gì, quan hệ với `cosmos-exec`, và điều gì xảy ra
nếu thiếu nó. Dùng làm tham chiếu cho Chương 4/5 của đồ án.

---

## 1. evcosmos là gì

`evcosmos` là **binary tầng đồng thuận** (consensus / ev-node runtime) của một
rollup Cosmos/WASM. Nó đóng gói toàn bộ runtime của ev-node thành một file thực
thi chạy được, và chịu trách nhiệm:

- **Sản xuất block** khi chạy ở vai trò aggregator (sequencer).
- **Ký header** block bằng signer key của node.
- **Submit / đọc blob lên Data Availability (Celestia)** — mỗi block publish blob
  `SignedHeader` và `SignedData` lên namespace riêng.
- **P2P** — kết nối libp2p (GossipSub + Kademlia DHT), trao đổi block/header.
- **Đồng bộ block** — full node sync qua P2P và đọc lại từ DA để kiểm chứng.

Điểm mấu chốt: **evcosmos KHÔNG tự thực thi giao dịch**. Phần thực thi state
(CosmWasm / cosmos-sdk) nằm ở binary riêng `cosmos-exec`; evcosmos gọi sang qua
gRPC bằng cờ `--grpc-executor-url`. Đây chính là sự **tách tầng đồng thuận / tầng
thực thi** mà kiến trúc đồ án trình bày.

---

## 2. Sequencer và full node là CÙNG một binary

Đây là điểm dễ hiểu nhầm nhất. Trong hệ thống có "sequencer" và "full node",
nhưng chúng **không phải hai chương trình khác nhau** — cả hai đều là cùng binary
`evcosmos`, chỉ khác **một cờ** lúc khởi động:

| Vai trò    | Lệnh khởi động                                   | Hành vi                                              |
| ---------- | ------------------------------------------------ | --------------------------------------------------- |
| Sequencer  | `evcosmos start --evnode.node.aggregator=true`   | Sản xuất block, ký header, submit blob lên DA       |
| Full node  | `evcosmos start --evnode.node.aggregator=false`  | Không sản xuất block; sync qua P2P + đọc lại từ DA  |

Tên tiến trình `evcosmos-sequencer` / `evcosmos-fullnode` trong log chỉ là **nhãn
để phân biệt**, không phải hai binary. Có thể kiểm chứng trong
[scripts/run-cosmos-wasm-nodes.go](../../../../../scripts/run-cosmos-wasm-nodes.go):
cả `startSequencer` lẫn `startFullNode` đều chạy cùng đường dẫn
`build/evcosmos`, chỉ truyền cờ khác nhau.

---

## 3. evcosmos nằm ở folder nào

Source code: [apps/cosmos-wasm/](../../../../cosmos-wasm/)

```
apps/cosmos-wasm/
├── main.go                  — entrypoint: dựng CLI gốc `evcosmos` (cobra) + gắn subcommand
├── cmd/
│   ├── init.go              — lệnh `evcosmos init`:  tạo config/genesis/signer cho home
│   ├── run.go               — lệnh `evcosmos start`: mở store, dựng sequencer, StartNode
│   └── executor_client.go   — client gRPC nối sang cosmos-exec (execution bridge)
├── go.mod                   — module Go riêng (repo đa-module)
└── README.md                — quick start
```

Binary sau khi build nằm ở: `./build/evcosmos`.

### Quan trọng: logic cốt lõi KHÔNG nằm hết trong apps/cosmos-wasm

Thư mục `apps/cosmos-wasm` chỉ là **lớp CLI mỏng (wiring)** — nó lắp ráp các thành
phần lại chứ không tự cài đặt nghiệp vụ. Bằng chứng từ
[cmd/run.go](../../../../cosmos-wasm/cmd/run.go), lệnh `start` chỉ:

1. Tạo execution client gRPC tới `cosmos-exec` (`createExecutionClient`).
2. Đọc config + load `genesis.json`.
3. Mở KV store cục bộ (`store.NewDefaultKVStore`).
4. Dựng sequencer (`createSequencer` → `single.NewSequencer` hoặc
   `based.NewBasedSequencer`).
5. Gọi `rollcmd.StartNode(...)` để chạy node.

Toàn bộ nghiệp vụ thật được **kế thừa từ các package lõi của ev-node**:

- [block/](../../../../../block/) — tạo / validate / đồng bộ block, `DAClient`
- [pkg/p2p/](../../../../../pkg/p2p/) — mạng libp2p
- [pkg/sequencers/single](../../../../../pkg/sequencers/single/),
  [pkg/sequencers/based](../../../../../pkg/sequencers/based/) — sequencer
- `pkg/da/` — lớp Data Availability
- `node/`, `pkg/store/`, `pkg/genesis/`, `pkg/config/` — node runtime, store, genesis, config

→ Nói cách khác: **code "của sequencer và full node" = `evcosmos` (entrypoint ở
`apps/cosmos-wasm`) + các package lõi ev-node mà nó gọi vào**, chứ không phải chỉ
riêng thư mục `apps/cosmos-wasm`.

---

## 4. Tại sao cần binary riêng, không dùng thẳng code ev-node?

Câu hỏi thường gặp: "ev-node đã có sẵn logic block/P2P/DA rồi, sao không chạy luôn
mà phải tạo `evcosmos`?". Lý do: **ev-node là framework (thư viện), không phải
ứng dụng chạy sẵn.**

Các package lõi ([block/](../../../../../block/), [pkg/p2p/](../../../../../pkg/p2p/),
[pkg/sequencers/](../../../../../pkg/sequencers/), `node/`, `pkg/da/`) cung cấp
*khả năng*, nhưng bản thân chúng **không biết** những lựa chọn riêng của rollup:

- Chuỗi này dùng **executor** nào (CosmWasm? EVM? app tự viết?)
- Dùng **sequencer** nào (`single` hay `based`)
- Genesis, chain-id, signer key, store path lấy ở đâu
- Kết nối tới **DA** nào, executor gRPC ở URL nào

Phải có một chỗ *lắp ráp và cấu hình* các mảnh đó lại thành một chương trình chạy
được — đó chính là `evcosmos`. Nhìn lại mục 3: `apps/cosmos-wasm` **không cài đặt
lại** tầng đồng thuận, nó chỉ *gọi vào* code ev-node với cấu hình cụ thể. Nói cách
khác, "dùng thẳng code ev-node" và "tạo binary evcosmos" không mâu thuẫn — evcosmos
**chính là** cách dùng code ev-node cho rollup Cosmos/WASM.

**Ví dụ dễ hình dung:** ev-node giống thư viện `net/http` của Go. Không ai "chạy
`net/http`" được; phải viết một `main.go` khởi tạo server, đăng ký handler, chọn
port. `evcosmos` đúng là cái `main.go` đó cho rollup này.

**Vì sao là binary tách riêng, không nhét vào ev-node?** Vì mỗi rollup là một *cấu
hình khác nhau* của cùng bộ core. ev-node là repo đa-module: mỗi app có `go.mod`
riêng và là một entrypoint riêng ([apps/testapp](../../../../testapp/),
[apps/cosmos-wasm](../../../../cosmos-wasm/), ...). Cùng một core → nhiều binary,
mỗi binary chọn executor/sequencer/DA của nó. Đây cũng là hệ quả trực tiếp của
kiến trúc tách tầng ở mục 1 và mục 5.

---

## 5. evcosmos được tạo ra như thế nào

### Cách 1 — qua `just` (chuẩn)

Định nghĩa trong [.just/build.just](../../../../../.just/build.just):

```bash
cd apps/cosmos-wasm && go build -ldflags "<ldflags>" -o build/evcosmos .
```

### Cách 2 — thủ công (xem [README](../../../../cosmos-wasm/README.md))

```bash
cd apps/cosmos-wasm
go build -o evcosmos

# Khởi tạo home: tạo evnode.yml, genesis.json, signer key, node key
./evcosmos init --root-dir ~/.evcosmos --chain-id cosmos-wasm-test-chain

# Chạy node
./evcosmos start \
  --root-dir ~/.evcosmos \
  --grpc-executor-url http://localhost:50051 \
  --da.address http://localhost:7980
```

### Cách 3 — qua script thực nghiệm (dùng cho Chương 4)

[scripts/run-cosmos-wasm-nodes.go](../../../../../scripts/run-cosmos-wasm-nodes.go)
tự động hoá toàn bộ: build cả `evcosmos` + `cosmos-exec-grpc`, `evcosmos init`
cho mỗi node, copy genesis từ sequencer sang full node, bật execution service,
rồi spawn 1 sequencer (`aggregator=true`) và 1 full node (`aggregator=false`),
lấy multiaddr libp2p của sequencer qua `evcosmos net-info` để full node dial vào.

### Các subcommand của evcosmos

Từ [main.go](../../../../cosmos-wasm/main.go):

| Lệnh                   | Chức năng                                                |
| ---------------------- | ------------------------------------------------------- |
| `evcosmos init`        | Dựng config / genesis / signer trong `--root-dir`       |
| `evcosmos start`       | Chạy node (block production hoặc sync + DA + P2P)        |
| `evcosmos version`     | In version / commit của binary                          |
| `evcosmos net-info`    | In địa chỉ P2P / peers (dùng cho `--p2p.peers`)         |
| `evcosmos keys`        | Quản lý signer key (add / show / ...)                   |
| `evcosmos unsafe-clean`| Xoá store đồng thuận (nguy hiểm)                        |

---

## 6. Quan hệ với cosmos-exec (kiến trúc hai binary)

```
┌──────────────┐      HTTP/JSON      ┌──────────────────┐     gRPC      ┌──────────────────────────┐
│ my-dapp-web  │ ──────────────────> │   cosmos-exec    │ <──────────── │        evcosmos          │
│ (Next.js UI) │                     │ (tầng thực thi)  │   execution   │ (tầng đồng thuận + DA)   │
└──────────────┘                     │  CosmWasm state  │   interface   │  sequencer / P2P / sync  │
                                     └──────────────────┘               └────────────┬─────────────┘
                                                                                      │ blob (header/data)
                                                                                      v
                                                                              ┌───────────────┐
                                                                              │  Celestia DA  │
                                                                              └───────────────┘
```

- `cosmos-exec` giữ **state CosmWasm/cosmos-sdk** và chạy giao dịch, phơi HTTP API.
- `evcosmos` quyết định **thứ tự + đóng block + neo dữ liệu lên DA**; khi cần thực
  thi một block, nó gọi sang `cosmos-exec` qua execution gRPC
  (`--grpc-executor-url`, mặc định `http://localhost:50051`).

Hai binary tách biệt → có thể thay tầng thực thi (CosmWasm hôm nay, EVM/khác mai
sau) mà không đụng tầng đồng thuận, và ngược lại.

---

## 7. Dùng để làm gì

`evcosmos` là **điểm vào để khởi động một rollup hoàn chỉnh**. Trong bộ sản phẩm
của đồ án (`my-dapp-web` → `cosmos-exec` → `evcosmos`), nó là khối đáy biến chuỗi
giao dịch thành các block có thứ tự, có finality, được neo lên Celestia và đồng
bộ giữa các node.

---

## 8. Nếu không có evcosmos thì sao

| Thiếu thành phần        | Hệ quả                                                                                   |
| ----------------------- | ---------------------------------------------------------------------------------------- |
| Sequencer / đồng thuận  | Không ai sắp thứ tự và đóng block → không có chuỗi block, không có finality              |
| Lớp DA                  | Giao dịch không được publish blob lên Celestia → mất tính sẵn sàng & kiểm chứng dữ liệu  |
| P2P / sync              | Các node không đồng bộ → không thể có full node thứ hai xác minh độc lập                 |

Khi thiếu evcosmos, hệ thống chỉ còn `cosmos-exec` chạy state CosmWasm cục bộ —
đúng nghĩa một **"execution server" đơn lẻ, KHÔNG còn là một rollup**. Mất luôn
đặc tính *sovereign* (tự chủ về sắp xếp giao dịch và sẵn sàng dữ liệu trên DA).

> Câu gợi ý cho luận văn: *"evcosmos đóng vai trò tầng đồng thuận và sẵn sàng dữ
> liệu — không có nó, bộ ba phần mềm chỉ vận hành được logic thực thi hợp đồng cục
> bộ chứ chưa cấu thành một sovereign rollup hoàn chỉnh trên Celestia."*

---

## 9. Tham chiếu nhanh

- Source: [apps/cosmos-wasm/](../../../../cosmos-wasm/)
- Build recipe: [.just/build.just](../../../../../.just/build.just)
- Script chạy đa node: [scripts/run-cosmos-wasm-nodes.go](../../../../../scripts/run-cosmos-wasm-nodes.go)
- Vận hành node (data layout, cờ): [node-operations.md](node-operations.md)
- Tầng thực thi: [apps/cosmos-exec/README.md](../../../../cosmos-exec/README.md)
