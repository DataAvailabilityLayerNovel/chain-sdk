# P2P, DA Height và Blob — chain của tôi giao tiếp & lưu data ra Celestia như thế nào

Tài liệu này đi sâu **đúng 4 câu hỏi**:

1. Chain của tôi **giao tiếp P2P** giữa các node như thế nào?
2. Node **xác định DA height** ra sao (biết block của mình nằm ở đâu trên Celestia)?
3. **Submit blob** lên DA như thế nào, **dạng gì** (format trên dây)?
4. **Retrieve blob** từ DA như thế nào, và **khi nào** thực sự cần?

> Khác với [chain-flow.md](chain-flow.md) (tổng quan toàn bộ vòng đời block) và
> [blob-first.md](blob-first.md) (API blob-first cho dev), tài liệu này tập trung
> vào **tầng vận chuyển**: P2P gossip + DA blob, kèm format byte cụ thể và trích
> dẫn code. Đây là phần `cosmos-exec` **kế thừa nguyên** từ ev-node — chain của
> tôi không sửa, nhưng cần hiểu để vận hành và debug.

## Mục lục

- 0. Bức tranh tổng: hai kênh truyền, hai tốc độ
- 1. Giao tiếp P2P
- 2. Xác định DA height
- 3. Submit blob: cách thức & format
- 4. Retrieve blob: cách thức & khi nào cần
- 5. Dữ liệu thực tế trông như thế nào — ví dụ + explorer FE
- 6. Tóm tắt: ai gọi gì, khi nào
- 7. Tham chiếu code

---

## 0. Bức tranh tổng: hai kênh truyền, hai tốc độ

Mỗi block đi qua **hai con đường song song** tới các node khác:

```
              ┌──────────── P2P (libp2p GossipSub) ──────────────┐
              │  nhanh ~100ms · TRUST sequencer · "soft"          │
  Sequencer ──┤                                                   ├──▶ Full node
   (tạo block)│  chậm ~6–12s · TRUSTLESS, ai cũng verify · "hard" │
              └──────────── DA (Celestia blob) ──────────────────┘
```

- **P2P** dùng để **lan truyền nhanh**: block vừa tạo được gossip ngay cho peer.
  Full node nhận → apply → user thấy tx "đã vào block" (soft confirmation).
- **DA** dùng để **đảm bảo khả dụng & trustless**: cùng block đó được đóng gói
  thành blob, đẩy lên Celestia. Khi blob lên DA, block trở nên **không thể đảo
  ngược** và **bất kỳ ai** cũng download + replay lại được (hard finality).

Hai kênh độc lập về store và topic, nhưng **liên kết** qua `DataHash` (header trỏ
data) và qua **DA height hint** (P2P message mang theo gợi ý "block này nằm ở DA
height nào").

---

## 1. Giao tiếp P2P

### 1.1 Stack

P2P chạy trên [`go-libp2p`][go-libp2p] với 3 thành phần:

| Thành phần | Vai trò |
|-----------|--------|
| **GossipSub** | Pub/sub: broadcast header/data/tx cho mọi peer subscribe topic |
| **Kademlia DHT** | Peer discovery — node mới tìm peer cùng chain |
| **Connection Gater** | Allow/block peer theo `P2PConfig` (allowlist/blocklist) |

Code: [`pkg/p2p/client.go`](../../../../../pkg/p2p/client.go).

### 1.2 Định danh & khám phá peer

- **`chainID`** đóng vai namespace mạng: chỉ peer **cùng chainID** mới kết nối
  được. Trong dự án, `ev-node` gọi `CosmosExecutor.InitChain(chainID)` nội bộ để
  set chain identity (không có endpoint HTTP `/chain/init`).
- **Seeds / Peers**: CSV multiaddr trong `P2PConfig` để bootstrap. Node follower
  dial vào sequencer; sequencer không cần peer (xem `getPeerIDs()` trong
  [`pkg/sync/sync_service.go:500`](../../../../../pkg/sync/sync_service.go#L500) —
  aggregator **không** thêm seed peers).
- **Rendezvous** = hash(chainID): peer cùng chain tự gặp nhau qua DHT.

### 1.3 Hai SyncService độc lập: header và data

Đây là điểm cốt lõi. Node **không** gossip "cả block" trong một message. Nó tách
làm **hai luồng go-header riêng biệt** ([`pkg/sync/sync_service.go:34-38`](../../../../../pkg/sync/sync_service.go#L34-L38)):

```go
type HeaderSyncService = SyncService[*types.P2PSignedHeader]  // nhẹ: header + sig
type DataSyncService   = SyncService[*types.P2PData]          // nặng: toàn bộ txs
```

Mỗi `SyncService` ([`sync_service.go:52-70`](../../../../../pkg/sync/sync_service.go#L52-L70)) gồm:

| Field | Vai trò |
|-------|--------|
| `sub` (Subscriber) | Nhận message realtime từ GossipSub topic |
| `ex` (Exchange) | Client request-response: hỏi peer "cho tôi height N" |
| `p2pServer` (ExchangeServer) | Server: phục vụ request catch-up của peer khác |
| `syncer` | go-header syncer, đảm bảo apply đúng thứ tự height |
| `store` | Datastore prefix riêng (`headerSync` / `dataSync`) |

**Network ID tách prefix** ([`sync_service.go:496-498`](../../../../../pkg/sync/sync_service.go#L496-L498)):

```go
func (s *SyncService[H]) getNetworkID(network string) string {
    return network + "-" + string(s.syncType)   // "{chainID}-headerSync" hoặc "-dataSync"
}
```

→ header và data có topic GossipSub **khác nhau**, store **khác nhau**, sync hoàn
toàn độc lập.

**Ai dùng gì:**
- **Sequencer + full node**: cả `HeaderSyncService` và `DataSyncService`.
- **Light node** (dự án chưa bật): chỉ `HeaderSyncService` — không cần data.

### 1.4 Thứ tự broadcast: header TRƯỚC data ⚠️

Sau khi tạo block, executor broadcast theo thứ tự **bắt buộc**
([`block/internal/executing/executor.go`](../../../../../block/internal/executing/executor.go), mục 6.4 của [chain-flow.md](chain-flow.md#64-broadcast-order-)):

```go
headerBroadcaster.WriteToStoreAndBroadcast(ctx, &types.P2PSignedHeader{...}) // trước
dataBroadcaster.WriteToStoreAndBroadcast(ctx, &types.P2PData{...})           // sau
```

Lý do: P2P validate data **dựa trên header** đã nhận:
- `data.DACommitment()` phải khớp `header.DataHash`,
- `data.Metadata.Height` phải khớp `header.Height`.

Nếu data tới trước header → peer chưa có gì để validate → **reject**. Đó là vì sao
luôn header đi trước.

Hàm `WriteToStoreAndBroadcast` ([`sync_service.go:127`](../../../../../pkg/sync/sync_service.go#L127)) làm 3 việc: (1) lần đầu thì
init store + start syncer, (2) ghi vào store local, (3) `sub.Broadcast`. Lỗi
broadcast trùng (self-gossip, "known header") bị nuốt ở debug level — bình thường
với solo sequencer.

### 1.5 Khởi tạo store: lấy genesis từ peer

Một SyncService chỉ chạy syncer khi store đã có **ít nhất 1 item**
([`initFromP2PWithRetry`, `sync_service.go:352`](../../../../../pkg/sync/sync_service.go#L352)):
- **Follower boot lần đầu**: store rỗng → hỏi peer height = `genesis.InitialHeight`,
  retry exponential backoff (1s→3s), timeout 2 phút rồi nhường cho DA sync.
- **Restart**: dùng head trong store làm điểm bắt đầu.
- **Aggregator**: không có peer → return ngay, init store khi tự tạo block đầu tiên.

→ P2P là đường nhanh; nếu P2P fail (timeout), node vẫn sync được **qua DA** (mục 4).

---

## 2. Xác định DA height

"DA height" = số block trên **Celestia** (không phải height của rollup). Node cần
biết DA height ở **4 ngữ cảnh** khác nhau:

### 2.1 Sau khi submit: DA height = kết quả trả về của submit

Khi đẩy blob lên Celestia, RPC trả về **height nơi blob được include**
([`block/internal/da/client.go:123`](../../../../../block/internal/da/client.go#L123)):

```go
height, err := c.blobAPI.Submit(ctx, blobs, &submitOpts)
// height = DA height nơi các blob này nằm
```

Đây là **nguồn chân lý** về "block của tôi nằm ở DA height nào". Height này được:
1. Ghi vào cache: `cache.SetHeaderDAIncluded(hash, res.Height, blockHeight)`
   ([`da_submitter.go:237`](../../../../../block/internal/submitting/da_submitter.go#L237)),
2. Gắn làm **DA hint** vào P2P store qua `AppendDAHint(ctx, res.Height, heights...)`
   ([`da_submitter.go:240`](../../../../../block/internal/submitting/da_submitter.go#L240)).

### 2.2 Head của mạng DA: `GetLatestDAHeight`

Để biết Celestia đang ở đâu (mục tiêu catch-up), node query network head
([`client.go:304`](../../../../../block/internal/da/client.go#L304)):

```go
func (c *client) GetLatestDAHeight(ctx context.Context) (uint64, error) {
    header, err := c.headerAPI.NetworkHead(headCtx)
    return header.HeaderHeight(), nil
}
```

DA follower dùng giá trị này làm `highestSeenDAHeight` — đích để fetch tuần tự tới.

### 2.3 DA Height Hint (DAHint): tối ưu sync qua P2P

P2P message **kèm gợi ý** DA height để node sync khỏi phải quét Celestia tuần tự.
Wrapper ([`types`](../../../../../types/)):

```go
type P2PSignedHeader struct { *SignedHeader; daHint uint64 }
type P2PData         struct { *Data;         daHint uint64 }
```

Đặc tính quan trọng — **chỉ có hint khi node đang catch-up**:

| Trạng thái node | Có DAHint? | Vì sao |
|-----------------|-----------|--------|
| Đang catch-up (tụt sau head) | **Có** | Block đã submit DA xong → hint đã set |
| Đã ở head (realtime) | **Không** | Executor broadcast NGAY khi tạo block, DA submit chưa xảy ra → hint = 0 |

Luồng hint: DA submitter **set** sau khi submit (§2.1) → **lưu** trong P2P store →
**propagate** khi peer xin block → syncer **queue priority** → **fetch trước** các
height tuần tự ([`da_retriever.go:82` `QueuePriorityHeight`](../../../../../block/internal/syncing/da_retriever.go#L82)).
Tương thích ngược nhờ proto `optional`: node cũ bỏ qua hint, node mới đọc hint=0.

### 2.4 DA Included Height: mốc finality

Submitter chạy `processDAInclusionLoop` (mục 9.3 [chain-flow.md](chain-flow.md#93-hard-finality-da-inclusion))
tăng dần `DAIncludedHeight`: với mỗi rollup height kế tiếp, kiểm tra **cả header
VÀ data** đã có trên DA chưa (`IsHeightDAIncluded`). Nếu rồi:
- lưu mapping `node_height → da_height`,
- gọi `exec.SetFinal(height)` → cosmos-exec cập nhật `finalizedHeight`,
- persist metadata `da_included_height`.

→ Đây là cách chain biết "tx tại height h đã **hard-finalized**" — chính là dữ
liệu SDK đọc qua `Client.Status()` (`FinalizedHeight`) để phân biệt soft/DA
(xem [thesis 07 §7.5.3](thesis/docs/07-p2p-finality.md)).

---

## 3. Submit blob: cách thức & format

### 3.1 Ba namespace

Chain submit vào **3 namespace Celestia tách biệt** (config `[da]`):

| Namespace | Nội dung | Blob format |
|-----------|---------|-------------|
| `namespace` (header) | `DAHeaderEnvelope` | `marshal(SignedHeader proto + Ed25519 sig)` |
| `data_namespace` | `SignedData` | `marshal(Data{Txs,Metadata} proto + sig + signer)` |
| `forced_inclusion_namespace` | Raw tx user post thẳng | tx bytes thô (bypass sequencer) |

Namespace string dễ đọc → bytes deterministic qua
`NamespaceFromString(...).Bytes()` ([`client.go:65-67`](../../../../../block/internal/da/client.go#L65-L67)).

> ⚠️ Dự án hiện dùng **single legacy namespace** (header+data chung) — chưa bật
> tách `header_namespace`/`data_namespace`. Hệ quả & cách bật: xem
> [thesis 07 §7.5.2](thesis/docs/07-p2p-finality.md).

### 3.2 Format trên dây: blob Celestia v0

Mỗi đơn vị submit là một **Celestia blob v0**
([`client.go:100` `NewBlobV0`](../../../../../block/internal/da/client.go#L100)):

```go
blobs[i], err = blobrpc.NewBlobV0(ns, raw)   // raw = protobuf-encoded envelope
```

- `ns` = namespace bytes (29 byte: version + id).
- `raw` = **payload đã protobuf-marshal** (envelope header hoặc signed data).
- Mỗi blob khi lên Celestia có **commitment** = NMT subtree root, dùng làm khoá
  retrieve. `MakeID(height, commitment)` gói `(DA height, commitment)` thành một
  `ID` ([`client.go:169`](../../../../../block/internal/da/client.go#L169)).

### 3.3 Batching: không submit từng block

Submitter **gom nhiều block** rồi mới submit một lần (giảm phí DA). Vòng lặp
`daSubmissionLoop` (mục 7.3 [chain-flow.md](chain-flow.md#73-da-submitter--batching-strategy))
quyết định submit theo **time / size / count**. Khi submit:

1. **Sign** header → `DAHeaderEnvelope` (Ed25519, worker pool song song,
   [`createDAEnvelopes`, `da_submitter.go:257`](../../../../../block/internal/submitting/da_submitter.go#L257)). LRU cache tránh re-sign khi retry.
2. **Submit** `client.Submit(envelopes, _, namespace, options)` → trả DA height + IDs.
3. **Cập nhật** cache DA-included + DAHint + last-submitted height (§2.1).

### 3.4 Giới hạn & retry

- **Kích thước**: mỗi blob `> DefaultMaxBlobSize` → reject với `StatusTooBig`
  ([`client.go:92-99`](../../../../../block/internal/da/client.go#L92-L99)). Data lớn hơn phải **chunk**
  (xem [blob-first.md §7b](blob-first.md)).
- **Retry policy** ([`da_submitter.go:73`](../../../../../block/internal/submitting/da_submitter.go#L73)):

| Lý do fail | Hành xử |
|-----------|--------|
| `reasonFailure` | Exponential backoff (100ms→200ms→…) |
| `reasonMempool` (DA mempool đầy) | Chờ `MaxBackoff` |
| `reasonTooBig` | Tách batch nhỏ hơn |
| `reasonSuccess` | Reset backoff |

- **Error mapping** ([`client.go:124-155`](../../../../../block/internal/da/client.go#L124-L155)): RPC error string → status code
  (`NotIncludedInBlock`, `AlreadyInMempool`, `IncorrectAccountSequence`, …) để
  retry chính xác theo từng nguyên nhân.

### 3.5 Blob-first (SDK app-level) — khác với DA submission của chain

Đừng nhầm hai thứ:
- **Chain DA submission** (mục này): nội bộ ev-node, đẩy header/data của block.
- **Blob-first** ([blob-first.md](blob-first.md)): API SDK cho **dApp** đẩy data
  lớn (telemetry, ảnh) thẳng Celestia bridge, neo commitment 32 byte on-chain.
  Đây là tính năng cộng thêm, đi qua kết nối Celestia bridge riêng (`BlobClient`),
  **không** qua cosmos-exec-grpc.

---

## 4. Retrieve blob: cách thức & khi nào cần

### 4.1 Lấy theo (height, namespace): `Retrieve` / `GetAll`

API chính ([`client.go:201`](../../../../../block/internal/da/client.go#L201)):

```go
blobs, err := c.blobAPI.GetAll(blobCtx, height, []share.Namespace{ns})
```

- **Cần đủ `(height, namespace)`** để lấy tất cả blob của namespace tại DA height đó.
- Timestamp lấy từ **DA block header** (`getBlockTimestamp`, [`client.go:186`](../../../../../block/internal/da/client.go#L186)) để
  đảm bảo **deterministic** — không dùng `time.Now()` trừ khi fallback.
- `ErrBlobNotFound` → `StatusNotFound` (height đó không có blob của namespace);
  `ErrHeightFromFuture` → `StatusHeightFromFuture` (chưa tới height đó trên DA).

### 4.2 Lấy theo commitment cụ thể: `Get` / `Get(ids)`

Khi đã biết chính xác blob nào (visualization, verify):
[`client.go:419`](../../../../../block/internal/da/client.go#L419) tách `id → (height, commitment)` rồi
`blobAPI.Get(height, ns, commitment)`. **Lưu ý**: commitment một mình KHÔNG đủ —
luôn cần **height** đi kèm. (Đây cũng là ràng buộc của blob-first SDK: phải lưu
`Height` để retrieve, xem [blob-first.md §4](blob-first.md).)

### 4.3 Realtime: `Subscribe`

Thay vì poll, node có thể subscribe namespace ([`client.go:358`](../../../../../block/internal/da/client.go#L358)):
emit `SubscriptionEvent{Height, Blobs}` cho **mỗi DA block** có blob khớp. DA
follower dùng cái này cho **fast path** (followLoop).

### 4.4 Khi nào chain CẦN retrieve?

| Tình huống | Vì sao cần retrieve | Code |
|-----------|---------------------|------|
| **Full node sync từ DA** | P2P fail/chậm hoặc cần nguồn trustless → đọc block từ Celestia, replay | [`da_retriever.go:109` `RetrieveFromDA`](../../../../../block/internal/syncing/da_retriever.go#L109) |
| **Kiểm tra DA-inclusion (finality)** | Xác nhận header+data đã thực sự trên DA → mới `SetFinal` | `IsHeightDAIncluded` (mục 9.3 chain-flow) |
| **Forced inclusion** | Sequencer phải đọc tx user post thẳng DA để buộc include | [`client.go:320` `RetrieveForcedInclusion`](../../../../../block/internal/da/client.go#L320) |
| **dApp đọc blob-first data** | App lấy lại telemetry/ảnh đã neo commitment | `RetrieveBlob` ([blob-first.md](blob-first.md)) |

### 4.5 Luồng retrieve khi full node sync

```
DAFollower.followLoop   → Subscribe(namespace) → cập nhật highestSeenDAHeight
DAFollower.catchupLoop  → fetch tuần tự localNextDAHeight → highestSeenDAHeight
   (ưu tiên priorityHeights từ DAHint trước — §2.3)
      └─ RetrieveFromDA(daHeight):
            1. Retrieve(daHeight, headerNamespace)  → blob header
            2. Retrieve(daHeight, dataNamespace)    → blob data (nếu khác ns)
            3. protobuf decode → SignedHeader, Data
            4. match header↔data theo height
            5. pipe DAHeightEvent → syncer.heightInCh → ValidateBlock → ApplyBlock
```

Code: [`da_follower.go`](../../../../../block/internal/syncing/da_follower.go),
[`da_retriever.go`](../../../../../block/internal/syncing/da_retriever.go).

Khi block đến từ DA (không phải P2P), syncer **bắt buộc** chạy thêm
`VerifyForcedInclusionTxs`: nếu sequencer bỏ sót tx forced-inclusion →
`errMaliciousProposer` → halt chain. Đây là điểm khiến DA path **trustless** hơn
P2P path.

---

## 5. Dữ liệu thực tế trông như thế nào — ví dụ + explorer FE

Phần này trả lời cụ thể: **byte đẩy lên DA có dạng gì**, và **mỗi field trên
`http://localhost:3000/explorer/23` nghĩa là gì** (file FE:
[`my-dapp-web/src/app/explorer/[height]/page.tsx`](../../../../../../my-dapp-web/src/app/explorer/[height]/page.tsx)).

### 5.1 Mỗi block → hai blob

Như §1.3 và §3.1: một rollup block publish thành **2 blob riêng** trên Celestia
(cùng namespace `rollup` theo default script):

1. **Signed header blob** — luôn có (cả block rỗng).
2. **Data blob** — chỉ có khi block có tx (`num_txs > 0`). Block rỗng → không có
   data blob.

Trên dây mỗi blob là **Celestia blob v0 = namespace + payload protobuf**. Payload
là `DAHeaderEnvelope` / `SignedData` đã marshal (§3.2). Nhưng khi explorer **đọc
lại** từ ev-node StoreService (`GetBlock`), nó được decode ra **protojson**
(proto `bytes` → base64 string, `uint64` → string). Dưới đây là dạng JSON đó.

### 5.2 Ví dụ: signed header blob (đã decode)

```jsonc
{
  "header": {
    "version":         { "block": "11", "app": "1" },   // block=11 cho IBC compat
    "chainId":         "my-chain",
    "height":          "23",
    "time":            "1718450000000000000",            // unix NANOgiây (string)
    "lastHeaderHash":  "qf83…(base64)",                  // hash header #22
    "dataHash":        "9c1a…(base64)",                  // = DACommitment(data) → link data blob
    "appHash":         "0b7e…(base64)",                  // IAVL state root TRƯỚC block #23
    "validatorHash":   "3d22…(base64)",                  // hash(proposer+pubkey)
    "proposerAddress": "1f0a…(base64)"
  },
  "signature": "8ab2…(base64)",                          // Ed25519 sequencer ký header
  "signer": {
    "address": "1f0a…(base64)",
    "pubKey":  "c4e9…(base64)"
  }
}
```

Đây chính là cái cần để **verify chuỗi**: ai cũng lấy blob này từ Celestia, kiểm
chữ ký `signature` bằng `signer.pubKey`, và nối `lastHeaderHash` ngược về genesis.

### 5.3 Ví dụ: data blob (đã decode)

```jsonc
{
  "metadata": {
    "chainId":      "my-chain",
    "height":       "23",
    "time":         "1718450000000000000",
    "lastDataHash": "5f7c…(base64)"          // hash data #22
  },
  "txs": [
    "Cp4BCpsB…(base64 TxRaw)",               // tx #0 — Cosmos SDK TxRaw đã encode
    "Cp4BCpsB…(base64 TxRaw)"                // tx #1
  ]
}
```

Mỗi phần tử trong `txs` là một **Cosmos SDK `TxRaw`** (protobuf: body + authInfo +
signatures) ở base64. Explorer có nút **Decode** chạy `decodeTxBase64` (cosmjs)
client-side để bung ra message đọc được, ví dụ:

```jsonc
{
  "body": {
    "messages": [{
      "typeUrl": "/cosmwasm.wasm.v1.MsgExecuteContract",
      "value": { "sender": "wasm1…", "contract": "wasm1…", "msg": { "increment": {} } }
    }]
  },
  "authInfo": { "fee": { "amount": [{ "denom": "ustake", "amount": "0" }], "gasLimit": "200000" } },
  "signatures": ["…"]
}
```

> ⚠️ Hash của block (#23) và DA height (vị trí trên Celestia) **khác nhau** —
> đừng nhầm. `height: "23"` là height rollup; DA height là số block Celestia
> chứa blob (hiển thị riêng, §5.5).

### 5.4 Explorer lấy data từ ĐÂU (2 nguồn)

`getBlockWithDA(23)` ([`my-dapp-web/src/lib/backend.ts:330`](../../../../../../my-dapp-web/src/lib/backend.ts#L330))
ghép **2 backend**:

| Nguồn | Endpoint | Cho field nào |
|-------|----------|---------------|
| **cosmos-exec-grpc** | `GET /blocks/23` → `BlockInfo` | `height`, `time`, `app_hash`, `num_txs`, `tx_hashes` |
| **ev-node StoreService** | `POST /ev/evnode.v1.StoreService/GetBlock` → `EvBlockResponse` | `headerDaHeight`, `dataDaHeight`, **`block.header`** (signed header blob), **`block.data`** (data blob) |

Nếu ev-node RPC không reachable → fallback chỉ hiện phần cosmos-exec (DA card =
pending). Per-tx (gas/bytes/fee/status) load lazy qua `GET /tx/{hash}`.

> Điểm quan trọng: khối **"Nội dung đã publish lên DA"** lấy **chính xác bytes
> ev-node đã ghi Celestia** (từ StoreService), KHÔNG phải suy ra từ node local.
> Đây là bằng chứng trực quan rằng data thật sự nằm trên DA.

### 5.5 Giải thích TỪNG field trên `/explorer/23`

Trang gồm **4 khối** (theo thứ tự render):

**① Block summary** (card đầu — nguồn: cosmos-exec `/blocks/23`)

| Field FE | Ý nghĩa | Ghi chú |
|----------|---------|---------|
| **Height** | `#23` — số thứ tự block rollup | |
| **Time** | Thời điểm tạo block | ISO string từ cosmos-exec |
| **Tx count** | Số tx trong block (`num_txs`) | 0 = block rỗng |
| **App hash** | IAVL state root | hex; xem note AppHash bên dưới |

**② Data Availability (Celestia)** (component `DAInfo` — nguồn: ev-node StoreService)

| Field FE | Ý nghĩa |
|----------|---------|
| Badge **submitted / pending** | `submitted` nếu có `header_da_height` hoặc `data_da_height`; chưa submit DA → `pending` |
| **Header DA height** (`H = …`) | Block **Celestia** chứa signed header blob. `pending…` nếu chưa lên DA |
| **Data DA height** (`H = …`) | Block Celestia chứa data blob (tx bytes). Block rỗng → không có |
| **Namespace** | `rollup` (hardcode khớp `DA_NAMESPACE` default trong `run-cosmos-wasm-nodes.go`) |
| **Inspect on Celestia** | Lệnh `query_celestia_blob.sh --height <H>` để soi blob thô trên Celestia |

**③ Nội dung đã publish lên DA** (component `DAContent` — bytes thật từ Celestia)

*Signed header blob* (mỗi field map 1-1 với §5.2; FE decode base64→hex để dễ đọc):

| Field FE | Map tới | Ý nghĩa |
|----------|---------|---------|
| **Chain ID** | `header.chainId` | Định danh chain |
| **Height** | `header.height` | Height rollup (= 23) |
| **Time** | `header.time` | `nanosToISO()` đổi unix-nanos → ISO |
| **Version (block/app)** | `header.version` | `11 / 1` — block version 11 cho IBC |
| **Last header hash** | `header.lastHeaderHash` | Link tới header block trước → tạo chuỗi |
| **Data hash** | `header.dataHash` | `DACommitment(data)`; phải khớp data blob → liên kết 2 blob |
| **App hash** | `header.appHash` | State root **trước** block (input) |
| **Validator hash** | `header.validatorHash` | hash(proposer + pubkey) |
| **Proposer address** | `header.proposerAddress` | Địa chỉ sequencer tạo block |
| **Signature** | `signature` | Ed25519 ký header → bằng chứng sequencer commit |
| **Signer address** | `signer.address` | Phải khớp proposerAddress |
| **Signer pubkey** | `signer.pubKey` | Dùng verify `signature` |

*Data blob* (chỉ hiện khi có tx):

| Field FE | Map tới | Ý nghĩa |
|----------|---------|---------|
| **Chain ID** | `data.metadata.chainId` | |
| **Height** | `data.metadata.height` | = height header |
| **Time** | `data.metadata.time` | |
| **Last data hash** | `data.metadata.lastDataHash` | Link data block trước |
| **Transactions #i** | `data.txs[i]` | base64 TxRaw; nút **Decode** bung ra message Cosmos (§5.3) |

**④ Transactions** (component `TxList` — nguồn: `GET /tx/{hash}` mỗi dòng)

| Cột FE | Map tới | Ý nghĩa |
|--------|---------|---------|
| **#** | index | Thứ tự tx trong block |
| **Hash** | `tx_hashes[i]` | Link tới `/explorer/tx/{hash}`; hiển thị 24 ký tự đầu |
| **Gas** | `gas_used` | Gas tiêu thụ khi execute |
| **Bytes** | `bytes` | Kích thước tx (ảnh hưởng phí DA) |
| **Fee · DA (operator)** | `cost.est_gas_amount` + `cost.est_da_amount` | **Trái** (vàng): phí execution user trả (gas × min_gas_price, denom gas). **Phải** (tím): hoá đơn Celestia của sequencer (bytes × tia_per_byte, TIA) — **không** trừ vào ví user |
| **Status** | `status` | `success` (xanh) / `failed` (đỏ) / `pending` (vàng) |

> **Note AppHash (gây nhầm)**: `app_hash` ở card ① và `appHash` trong header là
> state root **trước** khi execute block này (input), không phải kết quả. Output
> (`newAppHash`) xuất hiện ở header block **kế tiếp** (#24). Xem
> [chain-flow.md §4.3](chain-flow.md).

### 5.6 Ví dụ đọc nhanh một block #23

> "Block #23, 2 tx, app_hash `0b7e…`. Header blob nằm DA height 1542, data blob
> cũng 1542 (cùng lần submit). `dataHash` trong header khớp với data blob → 2
> blob ăn khớp. Tx #0 là `MsgExecuteContract {increment}`, gas 95k, 320 byte,
> phí user `0 ustake` (0-fee mode), DA bill operator `0.00012 TIA`, status
> success. Vì có Header+Data DA height → block đã **hard-finalized** trên
> Celestia, không thể đảo ngược."

---

## 6. Tóm tắt: ai gọi gì, khi nào

```
TẠO BLOCK (sequencer)
  └─ P2P: WriteToStoreAndBroadcast(header) → (data)     [§1.4]  ngay lập tức, KHÔNG hint
  └─ DA : daSubmissionLoop gom batch → client.Submit()  [§3.3]  trả DA height [§2.1]
            └─ SetHeaderDAIncluded + AppendDAHint        [§2.3]  set hint cho lần sync sau

NHẬN BLOCK (full node)
  └─ P2P nhanh: Subscriber nhận P2PSignedHeader/P2PData → apply (soft)   [§1.3]
  └─ DA  chậm : DAFollower Subscribe + catchup → RetrieveFromDA → verify [§4.5]
            └─ priorityHeights từ DAHint giúp fetch thẳng              [§2.3]

FINALITY
  └─ processDAInclusionLoop: IsHeightDAIncluded → SetFinal  [§2.4]  hard finality
```

| Câu hỏi | Trả lời ngắn |
|---------|--------------|
| P2P giao tiếp sao? | libp2p GossipSub, **2 topic tách biệt** header/data, DHT discovery, header broadcast trước data |
| Xác định DA height? | (1) `Submit` trả về, (2) `GetLatestDAHeight` = network head, (3) DAHint trong P2P msg, (4) `DAIncludedHeight` cho finality |
| Submit blob — dạng gì? | Celestia **blob v0** chứa **protobuf** `DAHeaderEnvelope` / `SignedData`, vào 3 namespace, batch + retry theo status code |
| Retrieve — khi nào? | `GetAll(height, ns)` khi: full node sync DA, check DA-inclusion, đọc forced-inclusion, dApp đọc blob-first. **Luôn cần height** kèm commitment |

---

## 7. Tham chiếu code

| Chủ đề | File |
|--------|------|
| P2P client (libp2p/GossipSub/DHT) | [`pkg/p2p/client.go`](../../../../../pkg/p2p/client.go) |
| SyncService header/data | [`pkg/sync/sync_service.go`](../../../../../pkg/sync/sync_service.go) |
| DA blob client (Submit/Retrieve/Subscribe) | [`block/internal/da/client.go`](../../../../../block/internal/da/client.go) |
| DA submitter (batch, sign, hint) | [`block/internal/submitting/da_submitter.go`](../../../../../block/internal/submitting/da_submitter.go) |
| DA follower (subscribe + catchup) | [`block/internal/syncing/da_follower.go`](../../../../../block/internal/syncing/da_follower.go) |
| DA retriever (fetch + parse + priority) | [`block/internal/syncing/da_retriever.go`](../../../../../block/internal/syncing/da_retriever.go) |
| Explorer block page (FE) | [`my-dapp-web/src/app/explorer/[height]/page.tsx`](../../../../../../my-dapp-web/src/app/explorer/[height]/page.tsx) |
| FE backend types + getBlockWithDA | [`my-dapp-web/src/lib/backend.ts`](../../../../../../my-dapp-web/src/lib/backend.ts) |
| Tổng quan vòng đời block | [chain-flow.md](chain-flow.md) |
| Blob-first (SDK app-level) | [blob-first.md](blob-first.md) |
| Finality soft vs DA + SDK API | [thesis/docs/07-p2p-finality.md](thesis/docs/07-p2p-finality.md) |

[go-libp2p]: https://github.com/libp2p/go-libp2p
[go-header]: https://github.com/celestiaorg/go-header
</content>
</invoke>
