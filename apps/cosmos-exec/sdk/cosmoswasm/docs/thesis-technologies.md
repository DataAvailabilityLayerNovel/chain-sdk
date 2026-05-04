# Giải thích kỹ thuật — Các công nghệ và thuật toán sử dụng

Tài liệu giải thích chi tiết từng công nghệ, thuật toán, và protocol được sử dụng trong hệ thống cosmos chain + ev-node framework. Mỗi phần giải thích: **nguyên lý hoạt động**, **tại sao chọn**, và **cách hệ thống sử dụng**.

---

## Mục lục

1. [Raft Consensus — Leader Election & Replication](#1-raft-consensus)
2. [libp2p — Networking Stack](#2-libp2p)
3. [GossipSub — Publish/Subscribe Protocol](#3-gossipsub)
4. [Kademlia DHT — Peer Discovery](#4-kademlia-dht)
5. [go-header — Header Exchange Protocol](#5-go-header)
6. [IAVL Tree — Versioned Merkle Tree](#6-iavl-tree)
7. [SHA-256 Merkle Tree — Batch Integrity Proof](#7-sha-256-merkle-tree)
8. [Namespaced Merkle Tree (NMT) — Celestia Proofs](#8-namespaced-merkle-tree)
9. [Celestia DA — Data Availability Layer](#9-celestia-da)
10. [ABCI — Application Blockchain Interface](#10-abci)
11. [Connect-RPC — Remote Procedure Call](#11-connect-rpc)
12. [Protocol Buffers — Serialization](#12-protocol-buffers)
13. [Ed25519 — Digital Signatures](#13-ed25519)
14. [LevelDB — Key-Value Storage](#14-leveldb)
15. [Gzip Compression — Data Optimization](#15-gzip-compression)
16. [AES-256-GCM + Argon2id — Key Encryption](#16-aes-256-gcm--argon2id)
17. [Bản đồ tương tác giữa các công nghệ](#17-bản-đồ-tương-tác)

---

## 1. Raft Consensus

### Nguyên lý

Raft là thuật toán **distributed consensus** (đồng thuận phân tán) cho phép một nhóm nodes thống nhất về trạng thái chung, ngay cả khi một số nodes bị lỗi. Được thiết kế bởi Diego Ongaro (2014) như giải pháp dễ hiểu hơn Paxos.

**3 vai trò trong Raft:**

```
┌──────────┐     ┌──────────┐     ┌──────────┐
│  Leader   │     │ Follower │     │ Follower │
│           │────▶│          │     │          │
│ (produce  │     │ (receive │     │ (receive │
│  blocks)  │────▶│  blocks) │────▶│  blocks) │
└──────────┘     └──────────┘     └──────────┘
     │
     ├── Nhận requests từ clients
     ├── Append entries vào log
     ├── Replicate log tới followers
     └── Commit khi majority (quorum) ACK
```

**Leader Election:**
1. Mỗi node bắt đầu là **Follower**
2. Nếu follower không nhận heartbeat trong `electionTimeout` → chuyển thành **Candidate**
3. Candidate gửi `RequestVote` tới tất cả nodes
4. Node nhận được majority votes → trở thành **Leader**
5. Leader gửi heartbeat định kỳ (`heartbeatTimeout`) để duy trì leadership
6. Nếu leader crash → election mới bắt đầu tự động

**Log Replication:**
1. Leader nhận entry mới (block data)
2. Append vào local log
3. Gửi `AppendEntries` RPC tới followers
4. Khi majority followers ACK → entry được **committed**
5. Leader notify followers commit entry

**Safety guarantees:**
- **Election Safety:** Chỉ 1 leader per term
- **Log Matching:** Nếu 2 logs chứa entry cùng index + term → entries giống nhau
- **Leader Completeness:** Entry đã committed sẽ xuất hiện trong log của mọi leader tương lai

### Tại sao chọn Raft?

- **Dễ hiểu** hơn Paxos (cùng safety guarantees)
- **Mature implementation:** HashiCorp Raft — production-tested (Consul, Nomad, Vault)
- **Leader-based:** Phù hợp với ev-node's single-sequencer model — leader = block producer

### Hệ thống sử dụng như thế nào

**Package:** `pkg/raft/` — sử dụng `github.com/hashicorp/raft v1.7.3`

**Khi nào dùng:** Chỉ khi chạy multi-sequencer HA (High Availability). Config: `nodeConfig.Raft.Enable = true`.

**Vai trò:** Leader election cho aggregator nodes — chỉ leader mới produce blocks.

```go
// pkg/raft/node.go — Cấu hình
type Config struct {
    NodeID              string     // ID unique cho mỗi node
    RaftAddr            string     // TCP address (raft giao tiếp)
    Peers               []string   // "nodeID@address" format
    HeartbeatTimeout    time.Duration
    LeaderLeaseTimeout  time.Duration
    SnapCount           uint64     // Snapshot sau N entries
}
```

**Storage:** 2 BoltDB files:
- `raft-log.db` — log entries (blocks)
- `raft-stable.db` — stable state (term, vote)

**FSM (Finite State Machine):**
```go
// pkg/raft/node.go — Mỗi log entry chứa 1 block
type RaftBlockState struct {
    Height    uint64
    Hash      []byte
    Timestamp time.Time
    Header    []byte   // serialized SignedHeader
    Data      []byte   // serialized Data
}

// Apply: leader ghi block vào log → replicate → followers apply
func (f *FSM) Apply(log *raft.Log) interface{} {
    var state RaftBlockState
    proto.Unmarshal(log.Data, &state)
    f.lastState.Store(&state)  // atomic update
    f.applyCh <- state         // notify syncer
}
```

**Leader election flow:**

```go
// pkg/raft/election.go
func DynamicLeaderElection(node, leaderFactory, followerFactory) {
    for {
        select {
        case isLeader := <-node.leaderCh():
            if isLeader {
                node.Barrier()           // wait for quorum
                leaderFactory()          // start block production
            } else {
                followerFactory()        // start sync mode
            }
        }
    }
}
```

**Trong node:** [`node/full.go:149-176`](../../../../node/full.go)
```go
// Chỉ khi Raft enabled
raftNode, _ := raftpkg.NewNode(raftCfg, logger)
leaderElection = raftpkg.NewDynamicLeaderElection(
    logger,
    leaderFactory,    // → start AggregatorComponents (produce blocks)
    followerFactory,  // → start SyncComponents (sync blocks)
    raftNode,
)
```

---

## 2. libp2p

### Nguyên lý

libp2p là **modular networking stack** cho peer-to-peer applications. Được phát triển bởi Protocol Labs (cùng team IPFS). Thay vì dùng 1 protocol cố định, libp2p cho phép chọn và kết hợp các protocol layers:

```
┌─────────────────────────────────────┐
│ Application Layer                    │
│   (GossipSub, Kademlia DHT, ...)    │
├─────────────────────────────────────┤
│ Security Layer                       │
│   (Noise, TLS 1.3)                  │
├─────────────────────────────────────┤
│ Multiplexing Layer                   │
│   (yamux, mplex)                    │
├─────────────────────────────────────┤
│ Transport Layer                      │
│   (TCP, QUIC, WebSocket)            │
└─────────────────────────────────────┘
```

**Peer Identity:** Mỗi peer có identity dựa trên cryptographic keypair. PeerID = hash(publicKey). Identity persist qua restarts.

**Multiaddress:** Định danh đa tầng: `/ip4/1.2.3.4/tcp/7676/p2p/QmPeerID` — encode transport + network + peer identity trong 1 string.

### Tại sao chọn libp2p?

- **Battle-tested:** Dùng trong IPFS, Ethereum 2.0, Filecoin, Polkadot, Celestia
- **Modular:** Chọn transport (TCP/QUIC), security (Noise/TLS), mux (yamux)
- **NAT traversal:** Hole punching, relay, AutoNAT
- **Go-native:** `go-libp2p` là reference implementation

### Hệ thống sử dụng như thế nào

**Package:** `pkg/p2p/client.go` — `github.com/libp2p/go-libp2p v0.47.0`

```go
// Tạo libp2p host
host, _ := libp2p.New(
    libp2p.ListenAddrs(listenAddr),    // multiaddr
    libp2p.Identity(privKey),           // Ed25519 key = peer identity
    libp2p.ConnectionGater(gater),      // block/allow list
)
```

**Startup sequence:**
1. Tạo host với Ed25519 identity
2. Setup GossipSub (pub/sub) — xem [Section 3](#3-gossipsub)
3. Setup Kademlia DHT (peer discovery) — xem [Section 4](#4-kademlia-dht)
4. Bootstrap: connect tới seed nodes
5. Advertise presence trên DHT
6. Discover peers qua DHT

**Connection gating:** `conngater.BasicConnectionGater` — cho phép block/allow peers bằng PeerID hoặc IP.

**Peer limit:** 60 peers maximum (`peerLimit = 60`).

---

## 3. GossipSub

### Nguyên lý

GossipSub là **topic-based pub/sub protocol** trên libp2p. Kết hợp 2 strategies:

1. **Mesh (eager push):** Mỗi peer giữ mesh connections tới D peers (default D=6). Messages push trực tiếp qua mesh.
2. **Gossip (lazy pull):** Peers gửi metadata (IHAVE/IWANT) tới random peers ngoài mesh. Nếu thiếu message → kéo về (IWANT).

```
Peer A ──mesh──▶ Peer B ──mesh──▶ Peer C
   │                │
   │ gossip         │ gossip
   ▼                ▼
Peer D           Peer E

A broadcast msg:
  1. Push trực tiếp tới B (mesh)
  2. B push tới C (mesh)
  3. A gossip metadata tới D (lazy)
  4. D: "tôi chưa có msg này" → IWANT → A gửi full msg
```

**Tại sao GossipSub tốt hơn Flooding?**
- Flooding: O(N²) messages (mỗi peer gửi cho mọi peer)
- GossipSub: O(N × D) messages (mesh fanout cố định)
- Gossip layer bảo đảm reliability mà không tốn bandwidth

### Hệ thống sử dụng như thế nào

**Package:** `github.com/libp2p/go-libp2p-pubsub v0.15.0`

```go
// pkg/p2p/client.go
ps, _ := pubsub.NewGossipSub(ctx, host)
```

**2 topics cho mỗi chain:**

| Topic | Format | Nội dung |
|-------|--------|----------|
| Header | `{chainID}-headerSync` | Block headers (signature, proposer, stateRoot, timestamps) |
| Data | `{chainID}-dataSync` | Block data (transactions, metadata) |

**Broadcast (sequencer):**
```go
// pkg/sync/sync_service.go
syncService.WriteToStoreAndBroadcast(ctx, header)
// → store locally + publish to GossipSub topic
```

**Subscribe (full node):**
```go
// go-header wraps GossipSub subscription
subscriber := goheaderp2p.NewSubscriber[H](ps, pubsub.DefaultMsgIdFn,
    goheaderp2p.WithSubscriberNetworkID(networkID))
```

**Broadcast order:** Header trước, Data sau. Lý do: light client có thể verify header (nhẹ) trước khi download data (nặng).

---

## 4. Kademlia DHT

### Nguyên lý

Kademlia là **Distributed Hash Table** (bảng băm phân tán) cho peer discovery. Mỗi peer có ID (hash), và khoảng cách giữa 2 peers = XOR(ID_A, ID_B).

**K-Buckets:**
```
Peer A (ID = 0110)

Distance    K-Bucket         Peers
  1 bit     [0111]           Peer X
  2 bits    [0100, 0101]     Peer Y, Z
  3 bits    [0010, 0011]     Peer W
  4 bits    [1xxx]           Peer V, U, T
```

Mỗi bucket chứa tối đa K peers (default K=20). Bucket gần hơn có nhiều peers hơn → biết nhiều hơn về "hàng xóm", ít hơn về peers xa.

**Lookup:** Tìm peer gần nhất với target ID qua iterative lookup:
1. Hỏi K peers gần nhất trong k-bucket
2. Mỗi peer trả lời peers gần target hơn
3. Lặp lại cho tới khi không tìm được peer gần hơn

**Advertise/Discover:** Peer publish "tôi tham gia chain XYZ" lên DHT → peers khác tìm "ai tham gia chain XYZ?" qua DHT lookup.

### Hệ thống sử dụng như thế nào

**Package:** `github.com/libp2p/go-libp2p-kad-dht v0.38.0`

```go
// pkg/p2p/client.go
dht, _ := dht.New(ctx, host,
    dht.Mode(dht.ModeServer),        // full DHT node
    dht.BootstrapPeers(seedPeers...), // bootstrap nodes
)
dht.Bootstrap(ctx)

// Wrap host with routing → connections route qua DHT
host = routedhost.Wrap(host, dht)
```

**Peer discovery flow:**
```
1. Node start → connect bootstrap peers
2. dht.Bootstrap() → populate k-buckets
3. discutil.Advertise(ctx, disc, chainID)
   → publish "tôi tham gia chainID" lên DHT
   → re-advertise mỗi 1 giờ (reAdvertisePeriod)
4. disc.FindPeers(ctx, chainID)
   → DHT lookup: tìm peers cùng chainID
   → connect tới peers tìm được
```

**Namespace:** `chainID` dùng làm rendezvous point — chỉ peers cùng chain mới discover nhau.

---

## 5. go-header

### Nguyên lý

go-header là **P2P header exchange library** bởi Celestia team. Cung cấp:
- Request/Response: fetch headers theo height range
- Store: persist headers locally
- Syncer: catch up với network head
- Subscriber: nhận new headers qua GossipSub

**Tại sao cần go-header riêng?** GossipSub chỉ broadcast new messages — không giúp node mới sync history. go-header bổ sung: request old headers từ peers + verify chain continuity.

### Hệ thống sử dụng như thế nào

**Package:** `pkg/sync/sync_service.go` — `github.com/celestiaorg/go-header v0.8.1`

**Generic design:** `SyncService[H]` generic cho bất kỳ `header.Header[H]`:

```go
// 2 instances
type HeaderSyncService = SyncService[*types.P2PSignedHeader]  // headers
type DataSyncService   = SyncService[*types.P2PData]          // block data
```

**Network IDs:**
- Headers: `{chainID}-headerSync`
- Data: `{chainID}-dataSync`

**Components:**

| Component | Vai trò |
|-----------|---------|
| `Exchange` | Request headers by height range từ peers |
| `ExchangeServer` | Respond to peer requests (serve stored headers) |
| `Subscriber` | GossipSub subscription cho new headers |
| `Syncer` | Coordinate catch-up: detect gap → fetch via Exchange |

**Init flow:**
```go
// initFromP2PWithRetry: fetch genesis header from peers
// Exponential backoff: 1s → 2s → 4s → ... → 10s max
// Timeout: 30s → nếu không tìm được peer, defer to DA sync
```

**Pruning:** Disabled (trusting period = 99 years) — giữ tất cả headers.

---

## 6. IAVL Tree

### Nguyên lý

IAVL (**Immutable AVL**) là **versioned Merkle tree** — kết hợp AVL balanced BST + Merkle hashing + version history.

```
Version 3 (current):        Version 2 (old):
       [H1]                       [H1']
      /    \                     /    \
   [H2]   [H3]              [H2']   [H3']
   / \     / \               / \     / \
 [a] [b] [c] [d]           [a] [b] [c'] [d]
                                     ↑
                               c was different
```

**Tính chất:**
- **AVL Balanced:** Self-balancing binary search tree → O(log N) lookup/insert/delete
- **Merkle Hash:** Mỗi internal node hash = H(left_hash || right_hash). Root hash = state commitment
- **Versioned:** Mỗi `Commit()` = 1 version. Có thể load bất kỳ version cũ nào
- **Immutable:** Versions cũ không bị modify — copy-on-write

**Tại sao không dùng Patricia Trie (Ethereum)?**
- IAVL: O(log N) height, balanced → consistent performance
- Patricia Trie: Variable depth, worst case O(key_length)
- IAVL: Native versioning → rollback dễ
- Patricia Trie: Phải tự implement state snapshot

### Hệ thống sử dụng như thế nào

**Package:** `github.com/cosmos/iavl v0.20.1` (indirect, qua Cosmos SDK v0.47)

**Cosmos SDK mount stores as IAVL:**
```go
// apps/cosmos-exec/app/app.go
keys := sdk.NewKVStoreKeys("auth", "bank", "params", "capability",
    "ibc", "ibctransfer", "wasm")
for _, key := range keys {
    base.MountStore(key, storetypes.StoreTypeIAVL)
}
```

Mỗi module có IAVL tree riêng. Tất cả backed bởi LevelDB.

**State root (AppHash):**
```go
// executor.go — ExecuteTxs()
commitRes := e.app.Commit()
e.stateRoot = commitRes.Data
// stateRoot = Merkle root across ALL IAVL trees
// = H(auth_root || bank_root || wasm_root || ...)
```

**Rollback (crash recovery):**
```go
// executor.go — Rollback()
e.app.LoadVersion(int64(targetHeight))
// IAVL load old version → state reverts
// Copy-on-write: version mới không ảnh hưởng version cũ
cms := e.app.CommitMultiStore()
e.stateRoot = cms.LastCommitID().Hash  // recalculate root
```

**Pruning:** Cosmos SDK prune old IAVL versions dựa trên `PruningOptions`. Mặc định giữ tất cả versions (cho rollback support).

---

## 7. SHA-256 Merkle Tree

### Nguyên lý

Binary Merkle tree sử dụng SHA-256 hash function. Cho phép:
- **Commitment:** 32-byte root hash đại diện cho N data items
- **Inclusion proof:** Chứng minh 1 item thuộc tree mà chỉ cần O(log N) hashes
- **Offline verification:** Verify proof mà không cần access tree gốc

```
        Root = H(H01 || H23)
       /                    \
   H01 = H(H0 || H1)    H23 = H(H2 || H3)
    /       \              /       \
  H0=H(A)  H1=H(B)    H2=H(C)   H3=H(D)
    |         |          |         |
  Blob A    Blob B     Blob C    Blob D

Proof cho Blob C:
  Path: [H3 (right sibling), H01 (left sibling)]
  Verify: H(H(H2=H(C)) || H3) → H23 → H(H01 || H23) → Root ✓
```

**Tại sao SHA-256?**
- 256-bit output → collision resistance 2^128
- Hardware acceleration (SHA-NI instructions)
- Chuẩn industry (Bitcoin, Ethereum, TLS)

### Hệ thống sử dụng như thế nào

**Package:** `apps/cosmos-exec/sdk/cosmoswasm/internal/merkle/merkle.go`

**Implementation tự viết** — pure Go, không dependency:

```go
// ComputeRoot: tính root từ list of leaves
func ComputeRoot(layer [][]byte) []byte {
    for len(layer) > 1 {
        var next [][]byte
        for i := 0; i < len(layer); i += 2 {
            left := layer[i]
            right := layer[i]   // duplicate nếu odd
            if i+1 < len(layer) { right = layer[i+1] }
            h := sha256.Sum256(append(left, right...))
            next = append(next, h[:])
        }
        layer = next
    }
    return layer[0]
}

// BuildProof: tạo inclusion proof
func BuildProof(commitments [][]byte, leafIndex int) (root, []PathStep) {
    // Return sibling hashes từ leaf → root
}

// Verify: verify proof offline
func Verify(root, commitment []byte, path []PathStep) bool {
    hash := sha256.Sum256(commitment)
    for _, step := range path {
        if step.IsLeft {
            hash = sha256.Sum256(append(step.SiblingHash, hash[:]...))
        } else {
            hash = sha256.Sum256(append(hash[:], step.SiblingHash...))
        }
    }
    return bytes.Equal(hash[:], root)
}
```

**Use case — Blob batch commitment:**
```
1. User submit 10 blobs → 10 SHA-256 commitments
2. ComputeRoot(commitments) → 32-byte Merkle root
3. Ghi root on-chain (32 bytes thay vì 10 × full data)
4. BuildProof(commitments, index=3) → proof cho blob #3
5. Bất kỳ ai có proof + root → Verify offline
```

---

## 8. Namespaced Merkle Tree (NMT)

### Nguyên lý

NMT là mở rộng của Merkle tree — mỗi leaf **thuộc về 1 namespace**. Cho phép chứng minh:
- **Inclusion:** "Blob X thuộc namespace N tại height H"
- **Absence:** "Không có blob nào trong namespace N tại height H"

```
NMT Root
  ├── NS:0001 → [blob A, blob B]     ← namespace 1
  ├── NS:0002 → [blob C]             ← namespace 2
  └── NS:0003 → [blob D, blob E, blob F] ← namespace 3

Proof cho NS:0002:
  "blob C thuộc NS:0002, và không có blob nào khác trong NS:0002"
```

**Khác với regular Merkle tree:** NMT sort leaves theo namespace → liên tục trong tree → proof nhỏ hơn cho range queries.

### Hệ thống sử dụng như thế nào

**Package:** `github.com/celestiaorg/nmt v0.24.2` — dùng bởi Celestia DA client

NMT là nền tảng của Celestia's data availability. Khi submit blob lên Celestia:
1. Blob được chia thành shares (512 bytes mỗi share)
2. Shares đặt vào Extended Data Square (EDS)
3. NMT tree cover mỗi row/column của EDS
4. Blob commitment = subtree root trong NMT

```go
// pkg/da/jsonrpc/blob.go
// Commitment = Merkle subtree root over blob's shares in EDS
commitment := inclusion.CreateCommitment(blob, merkle.HashFromByteSlices,
    subtreeRootThreshold: 64)
```

**Proof verification:**
```go
// Verify blob inclusion tại DA height
included, _ := blobAPI.Included(ctx, height, namespace, proof, commitment)
```

---

## 9. Celestia DA

### Nguyên lý

Celestia là **Data Availability (DA) Layer** — lưu trữ data blobs với guarantee rằng data **khả dụng** (bất kỳ ai cũng có thể download). Khác với blockchain thông thường:

```
Traditional blockchain:      Celestia DA:
  Consensus + Execution       Data Availability ONLY
  + Data Availability          (không execute, không validate)
  (tất cả trong 1)            → Rollups tự execute
```

**Namespace:** Celestia chia blob space thành namespaces (29 bytes). Mỗi rollup/app có namespace riêng → chỉ download data mình cần.

```
Celestia Block:
  NS:0001 [rollup A headers] [rollup A data]
  NS:0002 [rollup B headers] [rollup B data]
  NS:0003 [app C blobs]
  ...
```

**Data Availability Sampling (DAS):** Light nodes chỉ sample random shares từ EDS → verify data available mà không download full block. Probability guarantee: nếu ≥50% shares available → toàn bộ block recoverable (Reed-Solomon erasure coding).

**PayForBlobs:** Gas fee trên Celestia = proportional to blob size. Đây là chi phí DA duy nhất — rollup không trả gas cho execution.

### Hệ thống sử dụng như thế nào

**Package:** `block/internal/da/client.go` → `pkg/da/jsonrpc/client.go`

**3 namespaces per chain:**

| Namespace | Nội dung | Ai submit |
|-----------|----------|-----------|
| Header namespace | Block headers (signed by sequencer) | Aggregator node |
| Data namespace | Block data (transactions) | Aggregator node |
| Forced inclusion namespace | User-submitted txs (bypass sequencer) | End users |

**Namespace derivation:**
```go
// pkg/da/types/namespace.go
func NamespaceFromString(s string) *Namespace {
    hash := sha256.Sum256([]byte(s))
    // 29 bytes: 1 version + 18 zero padding + 10 bytes from hash
    ns, _ := NewNamespaceV0(hash[:10])
    return ns
}
```

**Submit flow:**
```go
// block/internal/da/client.go
// 1. Build blob with namespace
blob := NewBlobV0(namespace, serializedData)
// 2. Submit to Celestia (JSON-RPC to celestia-node)
daHeight, _ := blobAPI.Submit(ctx, blobs, &submitOpts)
// 3. Build ID = DA height + commitment
id := MakeID(daHeight, blob.Commitment)
```

**Retrieve flow:**
```go
// Get all blobs for namespace at height
blobs, _ := blobAPI.GetAll(ctx, height, []share.Namespace{ns})
```

**Subscribe (for forced inclusion):**
```go
// Based sequencer subscribes to DA namespace
blobCh, _ := blobAPI.Subscribe(ctx, namespace)
for blob := range blobCh {
    // Force-include txs from DA
}
```

**Transport:** JSON-RPC over HTTP/WebSocket to celestia-node. Auth via Bearer token.

---

## 10. ABCI — Application Blockchain Interface

### Nguyên lý

ABCI là **protocol** giữa consensus engine và application logic trong Cosmos ecosystem. Cho phép viết application bằng bất kỳ ngôn ngữ nào — miễn implement ABCI interface.

```
┌──────────────────┐         ┌──────────────────┐
│  Consensus Engine│  ABCI   │   Application    │
│  (CometBFT /     │◄──────►│   (Cosmos SDK /   │
│   ev-node)       │ socket  │    custom app)   │
└──────────────────┘         └──────────────────┘
```

**ABCI v1 (Cosmos SDK v0.47) — cosmos chain dùng:**
```
InitChain(genesis)           → khởi tạo state
BeginBlock(header)           → chuẩn bị block
DeliverTx(tx) × N           → execute từng tx
EndBlock(height)             → kết thúc block
Commit()                     → persist state → AppHash
```

**ABCI v2 (Cosmos SDK v0.50+) — ev-abci dùng:**
```
InitChain(genesis)
PrepareProposal(txs)        → app reorder/filter txs [MỚI]
ProcessProposal(txs)        → app validate block [MỚI]
FinalizeBlock(txs, header)  → execute ALL txs in 1 call [THAY THẾ Begin/Deliver/End]
Commit()
```

### Hệ thống sử dụng như thế nào

**Package:** `github.com/cometbft/cometbft v0.37.5` — `abci/types`

```go
// apps/cosmos-exec/executor/executor.go

// InitChain
e.app.InitChain(abci.RequestInitChain{
    Time:            genesisTime,
    ChainId:         chainID,
    InitialHeight:   int64(initialHeight),
    AppStateBytes:   e.app.DefaultGenesis(),
})

// ExecuteTxs — ABCI v1 flow
e.app.BeginBlock(abci.RequestBeginBlock{Header: header})
for _, tx := range txs {
    resp := e.app.DeliverTx(abci.RequestDeliverTx{Tx: tx})
    // resp.Code: 0 = success, != 0 = fail
    // resp.GasWanted, resp.GasUsed, resp.Events, resp.Log
}
e.app.EndBlock(abci.RequestEndBlock{Height: height})
commitResp := e.app.Commit()
stateRoot := commitResp.Data  // IAVL Merkle root = AppHash
```

**Trong cosmos chain:** ABCI không chạy qua socket (in-process) — `e.app` là trực tiếp `baseapp.BaseApp` instance. Nhanh hơn socket ABCI.

---

## 11. Connect-RPC

### Nguyên lý

Connect-RPC là **RPC framework** tương thích với gRPC nhưng đơn giản hơn. Hỗ trợ 3 protocol:
- **Connect protocol:** HTTP POST + JSON/Protobuf body (đơn giản nhất)
- **gRPC:** HTTP/2 + Protobuf (chuẩn gRPC)
- **gRPC-Web:** HTTP/1.1 compatible (cho browsers)

```
Client ──HTTP POST──▶ /evnode.v1.ExecutorService/ExecuteTxs
         Content-Type: application/json
         Body: {"txs": [...], "blockHeight": "42", ...}

                ◀── Response
         Content-Type: application/json
         Body: {"updatedStateRoot": "0x..."}
```

**Tại sao Connect-RPC thay vì raw gRPC?**
- Không cần HTTP/2 (chạy trên HTTP/1.1 qua h2c)
- JSON support natively (debug dễ hơn)
- Không cần gRPC-specific proxy (Envoy, etc.)
- Tương thích ngược với gRPC clients

### Hệ thống sử dụng như thế nào

**Package:** `connectrpc.com/connect v1.19.1`

**Server side:** [`execution/grpc/handler.go`](../../../../execution/grpc/handler.go)
```go
path, handler := v1connect.NewExecutorServiceHandler(server, compress1KB)
mux.Handle(path, handler)
// → registers /evnode.v1.ExecutorService/* endpoints
```

**Client side:** [`execution/grpc/client.go`](../../../../execution/grpc/client.go)
```go
client := v1connect.NewExecutorServiceClient(httpClient, addr)
resp, _ := client.ExecuteTxs(ctx, connect.NewRequest(req))
```

**Cosmos chain đặc biệt:** `apps/cosmos-wasm/` dùng **pure HTTP client** thay vì Connect-RPC generated client — tránh module path conflicts:
```go
// apps/cosmos-wasm/cmd/executor_client.go
func (c *EnhancedExecutorClient) connectCall(ctx context.Context, method string, ...) {
    url := c.baseURL + "/evnode.v1.ExecutorService/" + method
    // Manual HTTP POST + JSON — same wire format as Connect-RPC
}
```

**Compression:** `connect.WithCompressMinBytes(1024)` — compress payloads ≥1KB.

**Services:**

| Service | Proto | Endpoints |
|---------|-------|-----------|
| ExecutorService | `execution.proto` | InitChain, GetTxs, ExecuteTxs, SetFinal, GetExecutionInfo, FilterTxs |
| StoreService | `state_rpc.proto` | GetBlock, GetState, GetMetadata |
| P2PService | `p2p_rpc.proto` | GetPeerInfo, GetNetInfo |
| ConfigService | `config.proto` | GetNamespace, GetSignerInfo |

---

## 12. Protocol Buffers

### Nguyên lý

Protobuf (Protocol Buffers) là **binary serialization format** bởi Google. So với JSON:

| | JSON | Protobuf |
|---|---|---|
| Size | ~100 bytes | ~40 bytes (cùng data) |
| Parse speed | Slow (string parsing) | Fast (binary decode) |
| Schema | Implicit | Explicit (.proto file) |
| Human readable | Có | Không |

**Proto file định nghĩa schema:**
```protobuf
message Header {
  uint64 height = 1;
  bytes state_root = 2;
  google.protobuf.Timestamp time = 3;
  string chain_id = 4;
}
```

Compile `.proto` → generated Go code (`*.pb.go`) với strongly-typed structs.

### Hệ thống sử dụng như thế nào

**Proto files:** `proto/` directory → generated code: `types/pb/evnode/v1/`

**Serialization pattern:**
```go
// types/serialization.go
func (h *Header) MarshalBinary() ([]byte, error) {
    return proto.Marshal(h.ToProto())  // Go struct → proto struct → binary
}
func (h *Header) UnmarshalBinary(data []byte) error {
    var pHeader pb.Header
    proto.Unmarshal(data, &pHeader)    // binary → proto struct
    return h.FromProto(&pHeader)       // proto struct → Go struct
}
```

**Dùng ở đâu:**
- Block headers + data: serialize cho P2P broadcast + DA submission + disk storage
- Raft log entries: `RaftBlockState` protobuf cho FSM
- Connect-RPC requests/responses: wire format
- Cosmos SDK transactions: `MsgStoreCode`, `MsgExecuteContract`, etc. đều là protobuf messages

**Transaction encoding trong cosmos chain:**
```go
// apps/cosmos-exec/sdk/cosmoswasm/internal/txcodec/txcodec.go
func BuildProtoTxBytes(msgs []sdk.Msg, sender string) ([]byte, error) {
    txBuilder := authtx.NewTxConfig(codec, authtx.DefaultSignModes)
    tx := txBuilder.NewTxBuilder()
    tx.SetMsgs(msgs...)
    return txBuilder.TxEncoder()(tx.GetTx())  // protobuf encode
}
```

---

## 13. Ed25519

### Nguyên lý

Ed25519 là **elliptic curve digital signature** algorithm — Edwards curve variant của Curve25519.

**Tính chất:**
- **Key size:** 32 bytes private, 32 bytes public
- **Signature size:** 64 bytes
- **Speed:** ~70,000 sign/s, ~26,000 verify/s trên modern CPU
- **Deterministic:** Same key + message → same signature (không cần random nonce)
- **Safe:** Resistant to timing attacks, no nonce reuse vulnerability

**So sánh:**

| | Ed25519 | ECDSA (secp256k1) | RSA-2048 |
|---|---|---|---|
| Key size | 32 B | 32 B | 256 B |
| Signature size | 64 B | 64-72 B | 256 B |
| Sign speed | Nhanh nhất | Trung bình | Chậm |
| Blockchain dùng | ev-node, Solana, Stellar | Bitcoin, Ethereum | Không phổ biến |

### Hệ thống sử dụng như thế nào

**Package:** `github.com/libp2p/go-libp2p/core/crypto` (wrap Go stdlib `crypto/ed25519`)

**Key generation:**
```go
// pkg/signer/file/local.go
privKey, pubKey, _ := crypto.GenerateKeyPair(crypto.Ed25519, 256)
```

**3 vai trò trong hệ thống:**

| Vai trò | Key | Mục đích |
|---------|-----|----------|
| **Block signing** | Sequencer key | Ký mỗi block header → `SignedHeader` |
| **P2P identity** | Node key | libp2p PeerID = hash(pubKey) |
| **Address derivation** | Sequencer key | `address = sha256(pubKeyBytes)` |

**Block signing flow:**
```
Header (height, stateRoot, time, ...)
  → proto.Marshal(header) → headerBytes
  → privKey.Sign(headerBytes) → 64-byte signature
  → SignedHeader{Header, Signature, ProposerAddress}
```

**Verification (full node sync):**
```
Nhận SignedHeader qua P2P
  → pubKey.Verify(headerBytes, signature) → true/false
  → Check ProposerAddress == sha256(pubKey) → match?
```

**Key storage:** AES-256-GCM encrypted on disk — xem [Section 16](#16-aes-256-gcm--argon2id).

---

## 14. LevelDB

### Nguyên lý

LevelDB là **embedded key-value store** bởi Google (Jeff Dean & Sanjay Ghemawat). Dựa trên **LSM tree** (Log-Structured Merge-tree):

```
Write path:
  Write → MemTable (in-memory, sorted)
            │ (full)
            ▼
         Immutable MemTable → Flush to SSTable (Level 0)
            │ (too many L0 files)
            ▼
         Compaction: merge L0 → L1 → L2 → ... (sorted runs)

Read path:
  Read → MemTable → L0 → L1 → L2 → ... (bloom filter skip)
```

**Tính chất:**
- **Write-optimized:** Sequential writes, batch commits
- **Sorted keys:** Range queries, prefix scans
- **Compression:** Snappy compression per block
- **No server:** Embedded library, không cần process riêng

**Tại sao LevelDB thay vì SQLite/PostgreSQL?**
- Key-value model đủ cho blockchain state (key = address+slot, value = data)
- Embedded (không cần server process)
- Write-heavy workload phù hợp LSM tree
- Battle-tested trong blockchain: Bitcoin Core, Ethereum (legacy)

### Hệ thống sử dụng như thế nào

**Package:** `github.com/syndtr/goleveldb v1.0.1` (qua `cometbft-db v0.8.0`)

**2 LevelDB instances trong hệ thống:**

| Instance | Nơi dùng | Lưu gì |
|----------|---------|--------|
| Cosmos SDK DB | `apps/cosmos-exec/` | IAVL trees (auth, bank, wasm, IBC state) |
| ev-node Store | `node/full.go` | Block headers, block data, metadata |

```go
// apps/cosmos-exec/cmd/cosmos-exec-grpc/main.go
database, _ := db.NewGoLevelDB("application", dataDir)
// → ~/.cosmos-exec-grpc/data/application.db/

cosmosApp := app.New(logger, database)
// → IAVL trees backed by this LevelDB
```

**In-memory alternative:**
```go
database := db.NewMemDB()  // cho --in-memory mode
```

---

## 15. Gzip Compression

### Nguyên lý

Gzip dùng thuật toán **DEFLATE** (LZ77 + Huffman coding):
1. **LZ77:** Tìm repeated strings → thay bằng (distance, length) reference
2. **Huffman:** Frequent symbols → shorter codes

**Compression ratio phụ thuộc data:**
- JSON/text: 70-90% reduction
- Binary (random): 0-10% reduction (có thể lớn hơn gốc)
- Đã compressed (images, video): 0% (inflate thêm overhead)

### Hệ thống sử dụng như thế nào

**Package:** `apps/cosmos-exec/sdk/cosmoswasm/internal/compress/compress.go`

```go
// CompressIfBeneficial: chỉ nén khi kết quả nhỏ hơn
func CompressIfBeneficial(data []byte) ([]byte, bool) {
    compressed := gzipCompress(data)
    if len(compressed) < len(data) {
        return compressed, true   // nén có lợi
    }
    return data, false            // giữ nguyên
}

// IsGzipCompressed: detect bằng magic bytes
func IsGzipCompressed(data []byte) bool {
    return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

// MaybeDecompress: auto-detect + decompress
func MaybeDecompress(data []byte) ([]byte, error) {
    if IsGzipCompressed(data) { return decompress(data) }
    return data, nil
}
```

**Use case:** Nén blob data trước khi submit lên DA layer → giảm DA cost (PayForBlobs proportional to size).

---

## 16. AES-256-GCM + Argon2id

### Nguyên lý

**AES-256-GCM** (Advanced Encryption Standard, Galois/Counter Mode):
- **AES-256:** Block cipher, 256-bit key, 128-bit blocks
- **GCM:** Authenticated encryption — encrypt + integrity check trong 1 operation
- Output: ciphertext + 16-byte authentication tag
- Nonce: 12 bytes (unique per encryption)

**Argon2id** — Password-Based Key Derivation:
- Input: password + salt → output: derived key
- Memory-hard: cần nhiều RAM → resist GPU/ASIC brute-force
- Argon2**id**: hybrid of Argon2i (data-independent) + Argon2d (data-dependent)
- Parameters: time (iterations), memory (KB), parallelism (threads)

```
User passphrase: "my-secret"
         │
         ▼
Argon2id(passphrase, salt, time=3, memory=32MB, threads=4)
         │
         ▼
    256-bit key
         │
         ▼
AES-256-GCM.Encrypt(key, nonce, privKeyBytes)
         │
         ▼
    Encrypted signer.json file
```

### Hệ thống sử dụng như thế nào

**Package:** `pkg/signer/file/local.go`

```go
// Key derivation
key := argon2.IDKey(
    passphrase,  // user password
    salt,        // random 16 bytes
    3,           // time (iterations)
    32*1024,     // memory (32 MB)
    4,           // parallelism (4 threads)
    keyLen,      // 32 bytes (AES-256)
)

// Encryption
block, _ := aes.NewCipher(key)
gcm, _ := cipher.NewGCM(block)
nonce := randomBytes(gcm.NonceSize())  // 12 bytes
ciphertext := gcm.Seal(nonce, nonce, privKeyBytes, nil)
```

**Stored as JSON:**
```json
{
  "address": "abc123...",
  "pub_key": "ed25519:...",
  "encrypted_key": "<base64 ciphertext>",
  "salt": "<base64 salt>"
}
```

**Mục đích:** Bảo vệ sequencer private key on disk. Nếu attacker lấy được file, vẫn cần brute-force passphrase (Argon2id memory-hard → expensive).

---

## 17. Bản đồ tương tác giữa các công nghệ

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Full Node (node/full.go)                     │
│                                                                      │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │ Raft (hashicorp/raft)                                        │    │
│  │   BoltDB storage ← log entries (protobuf-encoded blocks)     │    │
│  │   Leader election → leader runs block production              │    │
│  │                   → follower runs sync                        │    │
│  └──────────────────────────────────────────────────────────────┘    │
│                                                                      │
│  ┌────────────────────────┐    ┌─────────────────────────────────┐   │
│  │ libp2p                 │    │ go-header                       │   │
│  │  ├─ Ed25519 identity   │    │  ├─ Exchange (fetch by height)  │   │
│  │  ├─ Kademlia DHT       │    │  ├─ Subscriber (GossipSub)     │   │
│  │  │   └─ peer discovery │    │  └─ Syncer (catch up)           │   │
│  │  ├─ GossipSub          │    │     2 instances:                │   │
│  │  │   ├─ header topic   │◄──►│     HeaderSyncService           │   │
│  │  │   └─ data topic     │    │     DataSyncService             │   │
│  │  └─ Connection gating  │    └─────────────────────────────────┘   │
│  └────────────────────────┘                                          │
│                                                                      │
│  ┌────────────────────────┐    ┌─────────────────────────────────┐   │
│  │ Celestia DA Client     │    │ Connect-RPC Server              │   │
│  │  ├─ JSON-RPC → node   │    │  ├─ ExecutorService (protobuf)  │   │
│  │  ├─ 3 namespaces      │    │  ├─ StoreService                │   │
│  │  │   (SHA-256 derived) │    │  ├─ P2PService                  │   │
│  │  ├─ NMT proofs         │    │  ├─ h2c (HTTP/2 cleartext)     │   │
│  │  └─ Blob submit/get   │    │  └─ gzip compression (≥1KB)     │   │
│  └────────────────────────┘    └─────────────────────────────────┘   │
│                                                                      │
│  ┌────────────────────────┐    ┌─────────────────────────────────┐   │
│  │ Block Store            │    │ Signer                          │   │
│  │  └─ LevelDB            │    │  ├─ Ed25519 key pair            │   │
│  │     (headers + data)   │    │  ├─ AES-256-GCM encryption      │   │
│  └────────────────────────┘    │  └─ Argon2id key derivation     │   │
│                                └─────────────────────────────────┘   │
│                                                                      │
│  ┌──────────────────────────────────────────────────────────────┐    │
│  │ Execution Layer (pluggable)                                   │    │
│  │                                                               │    │
│  │  ┌──────────────────────────────────────────────────────┐    │    │
│  │  │ CosmosExecutor (apps/cosmos-exec/)                    │    │    │
│  │  │  ├─ ABCI v1 (BeginBlock/DeliverTx/EndBlock/Commit)   │    │    │
│  │  │  ├─ Cosmos SDK BaseApp                                │    │    │
│  │  │  │   ├─ auth, bank, wasm, IBC modules                │    │    │
│  │  │  │   └─ IAVL trees → LevelDB                         │    │    │
│  │  │  ├─ CosmWasm (wasmd) — WASM smart contract runtime   │    │    │
│  │  │  ├─ BlobStore (SHA-256 content addressing)            │    │    │
│  │  │  │   └─ Merkle tree (batch proofs)                    │    │    │
│  │  │  ├─ PersistStore (JSONL → disk)                       │    │    │
│  │  │  └─ HTTP API + Security middleware                    │    │    │
│  │  │      ├─ Auth (Bearer token)                           │    │    │
│  │  │      ├─ Rate limiting                                 │    │    │
│  │  │      └─ Gzip compression (blob optimization)          │    │    │
│  │  └──────────────────────────────────────────────────────┘    │    │
│  └──────────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────────┘
```

### Tổng kết — Danh sách công nghệ

| # | Công nghệ | Library/Version | Vai trò trong hệ thống |
|---|-----------|----------------|----------------------|
| 1 | **Raft** | `hashicorp/raft v1.7.3` | Multi-sequencer leader election, block replication |
| 2 | **libp2p** | `go-libp2p v0.47.0` | P2P networking stack (transport, identity, connections) |
| 3 | **GossipSub** | `go-libp2p-pubsub v0.15.0` | Block header/data broadcast (pub/sub) |
| 4 | **Kademlia DHT** | `go-libp2p-kad-dht v0.38.0` | Peer discovery (tìm nodes cùng chain) |
| 5 | **go-header** | `celestiaorg/go-header v0.8.1` | Header exchange, sync catch-up |
| 6 | **IAVL Tree** | `cosmos/iavl v0.20.1` | Versioned Merkle state tree (rollback support) |
| 7 | **SHA-256 Merkle** | Pure Go (tự implement) | Blob batch proofs (commit + verify) |
| 8 | **NMT** | `celestiaorg/nmt v0.24.2` | Celestia data availability proofs |
| 9 | **Celestia** | `celestiaorg/go-square v3.0.2` | Data availability layer (blob storage + proofs) |
| 10 | **ABCI** | `cometbft v0.37.5` | Cosmos SDK ↔ consensus interface |
| 11 | **Connect-RPC** | `connectrpc.com/connect v1.19.1` | RPC framework (gRPC-compatible, JSON support) |
| 12 | **Protobuf** | `google.golang.org/protobuf` | Binary serialization (blocks, txs, RPCs) |
| 13 | **Ed25519** | `go-libp2p/core/crypto` | Block signing, P2P identity, address derivation |
| 14 | **LevelDB** | `syndtr/goleveldb v1.0.1` | Persistent KV storage (IAVL state + block store) |
| 15 | **Gzip** | Go stdlib `compress/gzip` | Blob data compression (reduce DA cost) |
| 16 | **AES-256-GCM** | Go stdlib `crypto/aes` | Private key encryption at rest |
| 17 | **Argon2id** | `golang.org/x/crypto/argon2` | Password → key derivation (memory-hard) |
| 18 | **CosmWasm/wasmd** | `CosmWasm/wasmd v0.45.0` | WASM smart contract runtime |
| 19 | **Cosmos SDK** | `cosmos-sdk v0.47.15` | Blockchain application framework (modules, keepers) |
| 20 | **BoltDB** | `hashicorp/raft-boltdb` | Raft log + stable storage |
