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
17. [Cosmos SDK Modules — auth, bank, wasm, IBC](#17-cosmos-sdk-modules)
18. [CosmWasm (wasmd) — WASM Smart Contract Runtime](#18-cosmwasm-wasmd)
19. [PersistStore — JSONL Append-Only Disk Layer](#19-persiststore-jsonl)
20. [BoltDB — Raft Storage Backend](#20-boltdb)
21. [Persistence end-to-end — Restart, Production Deployment, Multi-node Replication](#21-persistence-end-to-end)
22. [Bản đồ tương tác giữa các công nghệ](#22-bản-đồ-tương-tác)

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
- **Versioned:** Mỗi `Commit(root=H1, height=1)` = 1 version. Có thể load bất kỳ version cũ nào
- **Immutable:** Versions cũ không bị modify — copy-on-write
```
Version 1 keys:
  tree_1:H1 → {left: H2, right: H3}
  tree_1:H2 → {left: a:1, right: b:2}
  tree_1:H3 → {left: c:3, right: d:4}

Version 2 keys:
  tree_2:H1' → {left: H2, right: H3'}
  tree_2:H2  → [SHARED] → tree_1:H2
  tree_2:H3' → {left: c:30, right: d:4}
  tree_2:c:30 → [SHARED] → value 30
```

**Tại sao không dùng Patricia Trie (Ethereum)?**
- IAVL: O(log N) height, balanced → consistent performance
- Patricia Trie: Variable depth, worst case O(key_length)
- IAVL: Native versioning → rollback dễ
- Patricia Trie: Phải tự implement state snapshot

### Hệ thống sử dụng như thế nào

**Package:** `github.com/cosmos/iavl v1.2.2` (indirect, qua Cosmos SDK v0.50.11)

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

**ABCI v1 (Cosmos SDK ≤ v0.47):**
```
InitChain(genesis)           → khởi tạo state
BeginBlock(header)           → chuẩn bị block
DeliverTx(tx) × N           → execute từng tx
EndBlock(height)             → kết thúc block
Commit()                     → persist state → AppHash
```

**ABCI v2 (Cosmos SDK v0.50+) — cosmos-exec dùng:**
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
e.app.InitChain(&abci.RequestInitChain{
    Time:          genesisTime,
    ChainId:       chainID,
    InitialHeight: int64(initialHeight),
    AppStateBytes: e.app.DefaultGenesis(),
})

// ExecuteTxs — ABCI v2 flow (Cosmos SDK 0.50)
finalizeResp, _ := e.app.FinalizeBlock(&abci.RequestFinalizeBlock{
    Height: int64(height),
    Time:   blockTime,
    Hash:   blockHash,
    Txs:    txs,
})
// finalizeResp.TxResults[i].Code, GasUsed, Events, Log
_, _ = e.app.Commit()
stateRoot := e.app.CommitMultiStore().LastCommitID().Hash  // IAVL Merkle root
```

**Trong cosmos-exec:** ABCI không chạy qua socket (in-process) — `e.app` là trực tiếp `baseapp.BaseApp` instance. Nhanh hơn socket ABCI vì không serialize/copy qua TCP.

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

**Ví dụ**

Giả sử lưu balance:

```
key: account/alice
value: 100
```
Sau block tiếp theo:
```
key: account/alice
value: 130
```
LevelDB không sửa bản cũ ngay theo kiểu in-place.
Nó ghi version mới ra lớp mới hơn level++, rồi compaction sẽ dọn dần bản cũ.

**Bloom Filter**

Giả sử key alice tạo ra 3 vị trí bit:

- 2
- 7
- 15

Khi insert:

- set bit 2, 7, 15 = 1

Khi query alice:

- nếu bit 7 = 0 → chắc chắn không có
- nếu cả 3 bit đều = 1 → có thể có

### Hệ thống sử dụng như thế nào

**Luồng**

```
Query key
   │
   ├─> MemTable? 
   │     ├─ có → trả kết quả
   │     └─ không
   │
   ├─> Bloom filter của SSTable #1
   │     ├─ "chắc chắn không có" → skip file
   │     └─ "có thể có" → đọc file
   │
   ├─> Bloom filter của SSTable #2
   │     └─ ...
   │
   └─> SSTable nào "có thể có" thì mới đọc
```

**Package:** `github.com/syndtr/goleveldb v1.0.1` (qua `cometbft-db v0.14.1` + `cosmos-db v1.1.1`)

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

## 17. Cosmos SDK Modules

### Nguyên lý

**Cosmos SDK** ([cosmos-sdk v0.50.11](https://github.com/cosmos/cosmos-sdk)) là application framework cho blockchain. Mỗi "module" là một feature self-contained gồm:
- **State (Store):** một IAVL tree riêng dưới một storage key
- **Keeper:** struct cung cấp API đọc/ghi state — module khác chỉ truy cập state qua keeper interface (không trực tiếp)
- **Msg handlers:** xử lý các transaction message (vd `MsgSend`, `MsgStoreCode`)
- **Genesis init/export:** load/dump state ban đầu

Pattern này gọi là **ObjectCapability**: keeper được pass qua dependency injection ở app constructor, module khác chỉ thấy interface tối thiểu cần thiết.

```
App (apps/cosmos-exec/app/app.go)
 ├── auth        ──┐
 ├── bank         ├── inject keepers tới các module khác
 ├── capability  ──┤
 ├── ibc          │
 ├── transfer ────┘
 └── wasm
```

### 4 module sử dụng trong cosmos-exec

| Module | Store key | Vai trò | State chính |
|--------|-----------|---------|-------------|
| **auth** | `acc` | Account tracking (number, sequence, pubkey) | `BaseAccount` per address |
| **bank** | `bank` | Token transfers + balance tracking | `coin balances`, `denom metadata`, supply |
| **capability** | `cap` | Object capabilities (IBC ports, ownership) | Scoped sub-keepers |
| **ibc** | `ibc` | Inter-Blockchain Communication protocol | Clients, connections, channels |
| **transfer** (ICS-20) | `transfer` | Cross-chain fungible token transfers | Escrow accounts, denom traces |
| **wasm** | `wasm` | CosmWasm contract storage (xem [Section 18](#18-cosmwasm-wasmd)) | Code blobs, contract state, history |

### auth module — Account tracking

**Code:** [`apps/cosmos-exec/app/app.go:176-184`](../../../app.go)

```go
app.AccountKeeper = authkeeper.NewAccountKeeper(
    appCodec,
    runtime.NewKVStoreService(keys[authtypes.StoreKey]),
    authtypes.ProtoBaseAccount,        // account constructor
    maccPerms,                          // module account permissions
    authcodec.NewBech32Codec("cosmos"), // address encoding
    "cosmos",                           // bech32 prefix
    authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
)
```

**Mục đích:** Mỗi tx ký phải đính kèm pubkey + sequence. AnteHandler ([app/ante.go](../../../ante.go)) gọi `AccountKeeper.GetAccount(ctx, addr)` để lấy account, verify signature, increment sequence.

**State stored:**
```
acc/account/<addr_bytes>      → BaseAccount{number, sequence, pubkey}
acc/global_account_number     → uint64 counter
```

### bank module — Balance tracking

**Code:** [`apps/cosmos-exec/app/app.go:191-198`](../../../app.go)

```go
app.BankKeeper = bankkeeper.NewBaseKeeper(
    appCodec,
    runtime.NewKVStoreService(keys[banktypes.StoreKey]),
    app.AccountKeeper,                  // delegate account ops
    blockedAddrs,                       // module addrs can't receive
    authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
    logger,
)
```

**State stored:**
```
bank/balances/<addr>/<denom>     → sdk.Coin (amount)
bank/supply/<denom>              → total supply
bank/denoms_metadata/<denom>     → display name, symbol, etc.
```

**Mục đích:** Lưu balances cho mọi address × denom. Dùng bởi `DeductFeeDecorator` (charge fee) và `MsgSend` (transfer). Hiện tại cosmos-exec không enforce fee → balance không thay đổi qua AnteHandler, nhưng nếu wasm contract gọi `BankKeeper.SendCoins` thì balance update.

### IBC module — Cross-chain messaging

**Code:** [`apps/cosmos-exec/app/app.go:202-213`](../../../app.go)

```go
app.IBCKeeper = ibckeeper.NewKeeper(
    appCodec,
    keys[ibcexported.StoreKey],
    app.GetSubspace(ibcexported.ModuleName),
    ibcStakingKeeper,    // staking adapter (cosmos-exec không có staking thật)
    ibcUpgradeKeeper,    // upgrade adapter
    app.ScopedIBCKeeper, // capability scope
    authtypes.NewModuleAddress(authtypes.FeeCollectorName).String(),
)
```

**IBC stack:**
- **Tendermint Light Client** — verify counterparty chain block headers
- **Connection** — handshake giữa 2 chains
- **Channel** — packet stream over connection (per-application)
- **Packet** — application-level message (vd ICS-20 token transfer)

**State stored:** clients, connections, channels, packet commitments/receipts, next sequence numbers.

**Trong cosmos-exec:** IBC infrastructure được wire đầy đủ nhưng **chưa hoạt động end-to-end** — cần relayer (vd Hermes) chạy giữa 2 chains. Hiện tại chủ yếu là proof-of-concept để chain có thể mở rộng sang multi-chain sau này.

### transfer module (ICS-20)

ICS-20 = chuẩn chuyển token cross-chain. Khi user gọi `MsgTransfer(amount, dst_chain)`:
1. Token bị escrow tại source chain (gửi vào module account)
2. Packet được commit vào IBC channel
3. Relayer chuyển packet sang dest chain
4. Dest chain mint voucher token (denom trace: `ibc/<hash>`)

**Code:** [`apps/cosmos-exec/app/app.go:215-228`](../../../app.go) — `TransferKeeper` được wire qua IBC channel.

### capability module — Object capabilities

Mỗi IBC port (vd "transfer") cần một capability để claim. Pattern: app constructor gọi `CapabilityKeeper.ScopeToModule(name)` để tạo scoped keeper riêng cho từng module. Chỉ keeper sở hữu capability mới mở port được. Bảo vệ: module B không thể giả mạo packet xuất từ port của module A.

```go
// Seal: lock capability creation sau init
app.ScopedIBCKeeper = app.CapabilityKeeper.ScopeToModule(ibcexported.ModuleName)
app.ScopedTransferKeeper = app.CapabilityKeeper.ScopeToModule(ibctransfertypes.ModuleName)
app.ScopedWasmKeeper = app.CapabilityKeeper.ScopeToModule(wasmtypes.ModuleName)
app.CapabilityKeeper.Seal()
```

---

## 18. CosmWasm (wasmd)

### Nguyên lý

**CosmWasm** ([wasmd v0.50.0](https://github.com/CosmWasm/wasmd)) là smart contract runtime cho Cosmos chains, dùng **WebAssembly** thay vì EVM bytecode. Contracts viết bằng **Rust** → compile sang Wasm bytecode → deploy lên chain.

```
Rust source (lib.rs)
    │
    ▼ rustc + cargo + wasm-pack
Wasm bytecode (.wasm)
    │
    ▼ MsgStoreCode (đăng ký bytecode lên chain)
Code ID = 1
    │
    ▼ MsgInstantiateContract (tạo instance từ code)
Contract address (cosmos1abc...)
    │
    ▼ MsgExecuteContract (gọi handler)
State update
```

**3 entrypoints chuẩn của contract:**
- `instantiate(deps, env, info, msg)` — khởi tạo state ban đầu
- `execute(deps, env, info, msg)` — handler cho mọi tx mutate state
- `query(deps, env, msg)` — handler read-only (không tốn gas thật)

**Wasm sandbox isolation:** Contract chạy trong VM, không truy cập filesystem/network. Truy cập state chỉ qua `deps.storage` (KV API) → state đảm bảo deterministic.

**Gas metering:** Wasm VM count instructions → trừ gas. Hết gas → tx revert.

### Tại sao chọn CosmWasm thay vì EVM?

| | CosmWasm | EVM |
|---|---|---|
| Language | Rust (memory-safe, performant) | Solidity (chuyên dụng, hạn chế) |
| Bytecode | Wasm (chuẩn industry) | EVM bytecode (custom) |
| Performance | Native-near (Wasmer/Wasmtime JIT) | Interpreter-bound |
| Ecosystem | Cosmos chains (Osmosis, Neutron, Juno) | Ethereum-compatible |
| Storage model | Key-value API (Iavl) | Account + Storage trie |

### Hệ thống sử dụng như thế nào

**Code:** [`apps/cosmos-exec/app/app.go:236-256`](../../../app.go)

```go
app.WasmKeeper = wasmkeeper.NewKeeper(
    appCodec,
    runtime.NewKVStoreService(keys[wasmtypes.StoreKey]),
    app.AccountKeeper,
    app.BankKeeper,
    nil, nil, nil, nil, nil, nil,        // staking/distribution/etc. — disabled
    ibcRouterAdapter,
    app.GRPCQueryRouter(),
    wasmDir,                              // contract data dir on disk
    wasmConfig,                           // gas limits, memory limits
    capabilities,                         // available Wasm features
    authtypes.NewModuleAddress(govtypes.ModuleName).String(),
    wasmOpts...,                          // optional plugins
)
```

**State stored under `wasm/`:**
```
wasm/code/<id>                → Wasm bytecode + checksum + creator
wasm/contract/<addr>          → ContractInfo (code_id, label, admin)
wasm/contract/<addr>/state/   → contract's KV storage
wasm/contract/<addr>/history  → migration history
```

**3 msg types:**

| Msg | Action | Side effect |
|-----|--------|-------------|
| `MsgStoreCode` | Upload .wasm bytecode | Tạo code_id mới |
| `MsgInstantiateContract` | Tạo instance từ code_id | Tạo contract address mới + chạy `instantiate()` |
| `MsgExecuteContract` | Gọi `execute()` của contract | State updates + emit events |

**Query path (read-only):**

```go
// executor.go:496-506
queryCtx := e.app.BaseApp.NewContext(false).WithBlockHeight(...)
queryCtx = queryCtx.WithGasMeter(storetypes.NewGasMeter(queryGasMax))
result, _ := e.app.WasmKeeper.QuerySmart(queryCtx, contractAddr, queryMsg)
```

**Wasm runtime:** wasmd dùng **wasmvm** (CGO binding tới C++ Wasmer / Wasmtime). Compile contract một lần → cache native code → execute lần sau nhanh hơn.

---

## 19. PersistStore (JSONL)

### Nguyên lý

**PersistStore** ([apps/cosmos-exec/executor/persist.go](../../../executor/persist.go)) là layer persistence **bổ sung cho IAVL/LevelDB** — không thay thế. Nó lưu:

- `metadata.json` — overwrite-on-update: chain ID, state root hex, last/finalized heights
- `tx_results.jsonl` — append-only: kết quả execute từng tx (gas used, events, log)
- `blocks.jsonl` — append-only: thông tin block (height, time, app hash, tx count)

(Không lưu blob: blob đi thẳng Celestia qua `BlobClient`, không qua executor.)

**JSONL (JSON Lines)** = một JSON object trên mỗi dòng. Dễ append O(1), dễ parse bằng `bufio.Scanner`, dễ inspect bằng `jq`:

```jsonl
{"type":"tx_result","data":{"hash":"abc...","height":1,"code":0,"gas_used":42000,...}}
{"type":"tx_result","data":{"hash":"def...","height":2,"code":0,...}}
{"type":"block","data":{"height":3,"time":"2026-05-12T10:00:00Z","app_hash":"..."}}
```

### Tại sao cần PersistStore khi đã có IAVL/LevelDB?

| Câu hỏi | IAVL/LevelDB | PersistStore |
|---------|--------------|--------------|
| State commitment (Merkle root) | ✅ | ❌ |
| Verify proof | ✅ | ❌ |
| Versioned rollback | ✅ | ❌ |
| Inspect tx result theo hash | ❌ (chỉ có state mới) | ✅ |
| Replay blob data | ❌ | ✅ |
| Debug bằng `jq`/`grep` | ❌ (binary format) | ✅ (plain text) |

**IAVL chỉ giữ trạng thái HIỆN TẠI** — không lưu lịch sử event/log của từng tx. Cosmos SDK 0.50 không tự persist tx response indices. PersistStore là layer "audit log" giúp:
- Tra cứu tx result by hash sau khi chain restart (RPC `/tx/{hash}`)
- Replay block info sau restart (RPC `/blocks/{height}`)
- Migrate sang DB khác (vd PostgreSQL indexer) bằng cách stream JSONL

### Hệ thống sử dụng như thế nào

**Startup replay:** [persist.go:NewPersistStore](../../../executor/persist.go)
```go
// Mỗi .jsonl file được mở O_RDWR + scan từ đầu vào memory map
txFile, _ := os.OpenFile(dir+"/tx_results.jsonl", O_CREATE|O_APPEND|O_RDWR, 0o644)
scanner := bufio.NewScanner(txFile)
for scanner.Scan() {
    var entry persistedTxResult
    json.Unmarshal(scanner.Bytes(), &entry)
    cache[entry.Data.Hash] = entry.Data
}
```

**Append on update:** mỗi tx result/block/blob được encode thành JSON + append 1 dòng → fsync (tuỳ config). Concurrent writes được protect bằng `sync.Mutex`.

**Crash safety:** Append-only + `O_APPEND` → atomic write per line (POSIX guarantee với write < PIPE_BUF). Crash trong khi đang ghi → file vẫn parse được tới dòng cuối nguyên vẹn.

**Config:**
- `--in-memory` flag → bypass PersistStore (test mode, fastest)
- `cfg.PersistBlobs` / `cfg.PersistTxResults` → toggle từng loại
- `cfg.ResolveDataDir()` → directory chứa các file

---

## 20. BoltDB

### Nguyên lý

**BoltDB** là **embedded key-value store** dùng **B+ tree** trên một file mmap-ed. Khác với LSM (LevelDB), BoltDB tối ưu cho **read-heavy** workload và **transactional ACID** trong một file.

```
write tx:
  begin → mutate in-memory copy → fsync metadata → commit

read tx:
  begin → MVCC snapshot → read consistent view → no blocking writers
```

- **Single-writer, multi-reader:** writes serialize, reads parallel
- **Crash safe:** copy-on-write B+ tree + fsync → power-loss safe
- **Zero-copy:** mmap → reads không cần allocate buffer
- **No compaction:** trees rebalance in-place

### Tại sao BoltDB cho Raft, không LevelDB?

Raft cần một storage backend cho **2 loại data**:
1. **Log entries** — append-only, ít delete (chỉ truncate khi snapshot)
2. **Stable state** — current term, last voted candidate (overwrite vài bytes)

BoltDB phù hợp vì:
- **ACID transactions** đảm bảo log + stable state commit atomic — Raft safety phụ thuộc vào đây
- **Single-file** — easy backup, no compaction artifacts
- **HashiCorp official adapter** (`raft-boltdb`) — production-tested trong Consul/Nomad/Vault

LevelDB không phù hợp vì: writes async (memtable flush sau), không ACID across multiple keys.

### Hệ thống sử dụng như thế nào

**Package:** `github.com/hashicorp/raft-boltdb` (wraps [`go.etcd.io/bbolt`](https://github.com/etcd-io/bbolt))

**2 files mỗi Raft node:**

| File | Mục đích | Truy cập |
|------|---------|---------|
| `raft-log.db` | Log entries (proto-encoded block states) | Append on commit, read on replay |
| `raft-stable.db` | Current term + last voted peer | Overwrite mỗi election |

**Code:** [`pkg/raft/node.go`](../../../../../../pkg/raft/node.go)
```go
logStore, _ := raftboltdb.NewBoltStore(filepath.Join(dataDir, "raft-log.db"))
stableStore, _ := raftboltdb.NewBoltStore(filepath.Join(dataDir, "raft-stable.db"))

r, _ := raft.NewRaft(raftCfg, fsm, logStore, stableStore, snapshotStore, transport)
```

**Khi nào active:** Chỉ khi `nodeConfig.Raft.Enable = true` (multi-sequencer HA mode). Single-sequencer node không tạo BoltDB files.

---

## 21. Persistence end-to-end

Phần này trả lời các câu hỏi thực tế khi vận hành chain:

- Tại sao stop chain rồi restart, state vẫn còn đầy đủ?
- Production thì data lưu ở đâu? Trên cloud thì *ai* lưu?
- Các node lấy data như thế nào? Liên quan gì đến gossip?

### 21.1. Tại sao restart vẫn còn data

Khi node dừng và bật lại, **không có bước restore manual nào** — startup đơn giản là **đọc lại các file local trên disk**. Có **4 storage layer** persist độc lập, mỗi cái phục vụ một mục đích:

```
Khi node chạy (write path)            Khi restart (read path)
─────────────────────────────         ─────────────────────────────
Block produced                        node bật lên
  │                                    │
  ├─▶ ev-node Store (LevelDB)         ├─◀ Store.LoadHeader/LoadData()
  │   block headers + data            │     (block/internal/cache restore)
  │                                    │
  ├─▶ Cosmos SDK BaseApp              ├─◀ baseapp.LoadLatestVersion()
  │   IAVL trees → LevelDB            │     (load latest committed IAVL)
  │                                    │
  ├─▶ PersistStore (JSONL)            ├─◀ NewPersistStore() scan files
  │   tx results, blob data           │     vào memory map
  │                                    │
  └─▶ Raft (BoltDB, nếu enable)       └─◀ raft-boltdb open files
      log + stable state                    + replay log → FSM
```

**Khoá quan sát:** không layer nào dùng RAM-only. Mọi `Commit` đều `fsync` xuống disk **trước khi** trả response thành công. Crash giữa chừng → mất block đang sản xuất nhưng **không bao giờ mất block đã commit**.

**Restore từ disk** ở [`block/internal/syncing/syncer.go:311 initializeState`](../../../../../../block/internal/syncing/syncer.go):
```go
state := s.store.GetState(ctx)            // load từ LevelDB
s.lastState.Store(&state)                  // restore memory
s.daRetrieverHeight.Store(max(            // tiếp tục từ DA height đã catch-up
    genesis.DAStartHeight,
    s.cache.DaHeight(),
    state.DAHeight,
))
```

Cosmos-exec làm tương tự: `baseapp.LoadLatestVersion()` mở IAVL trees ra phiên bản committed cuối → state root khớp với `metadata.json.state_root` trong PersistStore. Mismatch → executor sẽ panic và yêu cầu rollback.

### 21.2. Data sống ở đâu trên disk

Trong dev (mặc định) — mọi file trong **home dir** của node:

```
~/.evcosmos-sequencer/          (hoặc đường dẫn --home)
├── config/
│   ├── genesis.json            ← chain ID, initial height, DA start height
│   ├── evnode.yml              ← runtime config (block_time, namespaces, ...)
│   └── signer.json             ← AES-256-GCM encrypted Ed25519 key (Section 16)
└── data/
    ├── cosmos-wasm/            ← ev-node Store (LevelDB)
    │   ├── 000003.ldb          ← block headers, data, DA inclusion cache
    │   └── MANIFEST-*          ← LSM tree metadata
    ├── raft-log.db             ← Raft entries (chỉ khi HA mode, Section 20)
    └── raft-stable.db          ← Raft term/vote

~/.cosmos-exec-sequencer/
└── data/
    ├── application.db/          ← Cosmos SDK IAVL trees (LevelDB, Section 14)
    │                              auth, bank, wasm, ibc, ... state
    ├── metadata.json            ← PersistStore: chainID, stateRoot, heights (Section 19)
    ├── tx_results.jsonl         ← append-only tx results
    └── blocks.jsonl             ← append-only block info
       (không có blobs.jsonl — blob lưu trên Celestia qua BlobClient)
```

**Phân chia trách nhiệm:**
- `application.db/` là **nguồn truth** cho state — IAVL Merkle root được commit on-chain.
- `cosmos-wasm/` (ev-node Store) là **nguồn truth** cho block history — header + data đầy đủ để rebroadcast cho peer mới.
- `*.jsonl` là **audit log** — không tham gia consensus, dùng để debug + serve RPC `/tx/{hash}`.

### 21.3. Production deployment

Câu hỏi quan trọng: "production thì lưu ở đâu?". Câu trả lời ngắn — **vẫn LevelDB trên disk local** của từng node, **không phải cloud database**. Vì:

- Mỗi node là một **state machine độc lập** — phải có copy state đầy đủ để compute Merkle root deterministic.
- Đặt LevelDB trên S3/RDS → mỗi tx phải round-trip ra mạng → latency huỷ throughput.
- Blockchain *đã có* replication built-in: P2P + DA layer. Không cần thêm replication tầng database.

**Pattern production thực tế:**

```
┌────────────────────────────────────────────────────────────┐
│ Sequencer node (1 con, hoặc N con HA với Raft)             │
│  • Block-volume SSD (vd EBS gp3, Hetzner NVMe)             │
│  • /var/lib/evcosmos/data/  ← LevelDB + IAVL               │
│  • Snapshot định kỳ → S3/GCS/Backblaze (cold backup)       │
│  • Encrypted key trong KMS/Vault thay vì signer.json       │
└────────────────────────────────────────────────────────────┘
                          │ DA submit (header + data namespaces)
                          ▼
┌────────────────────────────────────────────────────────────┐
│ Celestia Mainnet/Mocha (DA Layer)                          │
│  • Operator của Celestia network giữ blob data             │
│  • Light nodes có thể sample availability                  │
│  • 30-day retention (Mainnet) / configurable               │
└────────────────────────────────────────────────────────────┘
                          │ P2P broadcast (GossipSub)
                          │ + DA fetch fallback
                          ▼
┌────────────────────────────────────────────────────────────┐
│ Full nodes (N con, geo-distributed)                        │
│  • Same disk layout như sequencer                          │
│  • Read-only mode (`COSMOS_EXEC_READ_ONLY=true`) để        │
│    block public tx submission                              │
│  • Đặt sau load balancer + rate limit (Section đã làm)     │
└────────────────────────────────────────────────────────────┘
```

**Best practices production:**

| Vấn đề | Giải pháp |
|--------|-----------|
| Disk full vì IAVL grow vô hạn | Bật pruning (`PruningOptions` của Cosmos SDK) — giữ N versions gần nhất |
| Mất disk → mất chain | Snapshot LevelDB + IAVL ra S3 mỗi N blocks (vd `cosmprund` hoặc rsync) |
| Sequencer key rò rỉ | Thay `signer.json` bằng remote signer (HashiCorp Vault, AWS KMS) |
| Full node mới join | Cấu hình `DAStartHeight` trong genesis = DA height tại lúc launch → node mới sync từ đó qua DA + P2P |
| Cần state lịch sử (archive node) | Tắt pruning hoàn toàn, dùng disk lớn — query historical state qua RPC |

### 21.4. Cloud — Ai *thực sự* giữ data

Đây là điểm khác biệt giữa **sovereign rollup** và **L1 truyền thống**. Có 3 "cloud" trong stack:

| Layer | Ai lưu | Mất layer này thì... |
|-------|--------|---------------------|
| **Execution state** (IAVL/LevelDB) | Operator của từng node (bạn) — local SSD | Có thể rebuild từ DA blobs + replay |
| **Block history** (headers + data) | Mọi node trong P2P network | Recover từ DA layer hoặc peer |
| **DA blobs** (Celestia) | Validator set của Celestia network | **Mất vĩnh viễn** nếu Celestia mất quorum |

**Hệ quả:** state của rollup được **secure bởi Celestia validators**, không phải bạn. Đây là kiến trúc "data availability outsourcing":

- Bạn chỉ cần một sequencer (single point of failure được chấp nhận, vì user vẫn force-include qua DA).
- Block history luôn recoverable từ Celestia → kể cả khi *tất cả full node* down, vẫn có thể bootstrap node mới từ DA.
- Tradeoff: phải trả Celestia phí PayForBlobs.

**Lưu vào "cloud bình thường" (S3, Cloudflare R2) thì sao?** Có thể, nhưng là **backup**, không phải canonical source:
- S3 không có liveness/availability guarantee xuyên-trust-zone (Amazon có thể delete).
- Không có Merkle proof — không verify được "data này thật sự là block 1000".
- Celestia + NMT proofs ([Section 8](#8-namespaced-merkle-tree)) cung cấp inclusion proof cryptographic → trust-minimized.

### 21.5. Node mới sync data thế nào (vai trò của Gossip)

Khi một **full node mới** bật lên với genesis nhưng chưa có block:

```
node start
  │
  ├─▶ libp2p connect bootstrap peers (Section 4: Kademlia DHT)
  │   → discover các peer cùng chainID
  │
  ├─▶ Subscribe GossipSub topics (Section 3)
  │   → header topic, data topic
  │   → từ giờ NEW blocks sẽ push qua mesh
  │
  ├─▶ go-header initFromP2PWithRetry (Section 5)
  │   → fetch height=1 (genesis) từ peer
  │   → fetch tới network head qua Exchange
  │
  └─▶ DAFollower (Section 9: Celestia)
      → subscribe DA namespace cho new submissions
      → catchup từ DAStartHeight tới DA head
```

**Hai cơ chế chạy song song:**

| Cơ chế | Khi nào nhanh | Khi nào chậm |
|--------|---------------|--------------|
| **P2P direct exchange + gossip** | Có peer khoẻ trong cùng region | Mạng phân mảnh, peer xa |
| **DA fetch** | Always available (Celestia luôn up) | Round-trip qua Celestia bridge (~200-500ms/height) |

**Liên hệ với gossip cụ thể:**

- **GossipSub chỉ giúp NEW blocks** — mesh push từ sequencer ra mọi node trong vài trăm ms. Không giúp node mới catch-up history.
- **Catch-up dùng go-header Exchange** ([Section 5](#5-go-header)) — request/response P2P để fetch range `[height_local+1, network_head]` từ peer có store đầy đủ.
- **Fallback DA** — nếu P2P fail (no peer hoặc rate-limited), `DAFollower` đọc blob từ Celestia. Đây là **trust-minimized path**: data đã có Merkle proof từ Celestia, full node verify được mà không cần tin peer P2P.

**Câu trả lời cho "node mới đứng dậy lúc 4 giờ sáng":**
1. Genesis có `DAStartHeight=N` → node biết bỏ qua heights 1..N-1 trên Celestia.
2. Connect peer → fetch genesis header → verify signature của sequencer ([Section 13: Ed25519](#13-ed25519)).
3. Nếu peer có đầy đủ history → sync nhanh qua P2P (vài chục MB/s LAN).
4. Nếu peer mới hoặc thiếu → fallback DA, đọc tuần tự blob → chậm hơn nhưng đảm bảo.
5. Sau khi đuổi kịp → subscribe gossip cho block tương lai.

**Trường hợp đặc biệt: tất cả full node bị mất**
- Sequencer vẫn submit lên Celestia → data còn nguyên trên DA.
- Boot 1 node mới với genesis + `DAStartHeight` đúng → DAFollower replay toàn bộ history.
- Khôi phục đầy đủ → state root sau khi replay phải khớp commitment trên Celestia.
- Đây là điều phân biệt **sovereign rollup** với app blockchain truyền thống: chain không *biến mất* khi tất cả node down, vì DA là source of truth.

### 21.6. Tóm tắt 1 dòng

> **Restart không cần restore** vì mọi committed state đã `fsync` xuống LevelDB/IAVL/BoltDB/JSONL trên disk local. **Production lưu trên SSD local của từng node**, không phải cloud DB — replication tới các node khác đi qua **GossipSub (new blocks) + go-header Exchange (catch-up) + Celestia DA (canonical fallback)**.

---

## 22. Bản đồ tương tác giữa các công nghệ

```
┌─────────────────────────────────────────────────────────────────────┐
│                         Full Node (node/full.go)                    │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │ Raft (hashicorp/raft)                                        │   │
│  │   BoltDB storage ← log entries (protobuf-encoded blocks)     │   │
│  │   Leader election → leader runs block production             │   │
│  │                   → follower runs sync                       │   │
│  └──────────────────────────────────────────────────────────────┘   │
│                                                                     │
│  ┌────────────────────────┐    ┌─────────────────────────────────┐  │
│  │ libp2p                 │    │ go-header                       │  │
│  │  ├─ Ed25519 identity   │    │  ├─ Exchange (fetch by height)  │  │
│  │  ├─ Kademlia DHT       │    │  ├─ Subscriber (GossipSub)      │  │
│  │  │   └─ peer discovery │    │  └─ Syncer (catch up)           │  │
│  │  ├─ GossipSub          │    │     2 instances:                │  │
│  │  │   ├─ header topic   │◄──►│     HeaderSyncService           │  │
│  │  │   └─ data topic     │    │     DataSyncService             │  │
│  │  └─ Connection gating  │    └─────────────────────────────────┘  │
│  └────────────────────────┘                                         │
│                                                                     │
│  ┌────────────────────────┐    ┌─────────────────────────────────┐  │
│  │ Celestia DA Client     │    │ Connect-RPC Server              │  │
│  │  ├─ JSON-RPC → node    │    │  ├─ ExecutorService (protobuf)  │  │
│  │  ├─ 3 namespaces       │    │  ├─ StoreService                │  │
│  │  │   (SHA-256 derived) │    │  ├─ P2PService                  │  │
│  │  ├─ NMT proofs         │    │  ├─ h2c (HTTP/2 cleartext)      │  │
│  │  └─ Blob submit/get    │    │  └─ gzip compression (≥1KB)     │  │
│  └────────────────────────┘    └─────────────────────────────────┘  │
│                                                                     │
│  ┌────────────────────────┐    ┌─────────────────────────────────┐  │
│  │ Block Store            │    │ Signer                          │  │
│  │  └─ LevelDB            │    │  ├─ Ed25519 key pair            │  │
│  │     (headers + data)   │    │  ├─ AES-256-GCM encryption      │  │
│  └────────────────────────┘    │  └─ Argon2id key derivation     │  │
│                                └─────────────────────────────────┘  │
│                                                                     │
│  ┌──────────────────────────────────────────────────────────────┐   │
│  │ Execution Layer (pluggable)                                  │   │
│  │                                                              │   │
│  │  ┌──────────────────────────────────────────────────────┐    │   │
│  │  │ CosmosExecutor (apps/cosmos-exec/)                   │    │   │
│  │  │  ├─ ABCI v1 (BeginBlock/DeliverTx/EndBlock/Commit)   │    │   │
│  │  │  ├─ Cosmos SDK BaseApp                               │    │   │
│  │  │  │   ├─ auth, bank, wasm, IBC modules                │    │   │
│  │  │  │   └─ IAVL trees → LevelDB                         │    │   │
│  │  │  ├─ CosmWasm (wasmd) — WASM smart contract runtime   │    │   │
│  │  │  ├─ BlobStore (SHA-256 content addressing)           │    │   │
│  │  │  │   └─ Merkle tree (batch proofs)                   │    │   │
│  │  │  ├─ PersistStore (JSONL → disk)                      │    │   │
│  │  │  └─ HTTP API + Security middleware                   │    │   │
│  │  │      ├─ Auth (Bearer token)                          │    │   │
│  │  │      ├─ Rate limiting                                │    │   │
│  │  │      └─ Gzip compression (blob optimization)         │    │   │
│  │  └──────────────────────────────────────────────────────┘    │   │
│  └──────────────────────────────────────────────────────────────┘   │
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
| 6 | **IAVL Tree** | `cosmos/iavl v1.2.2` | Versioned Merkle state tree (rollback support) |
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
| 18 | **CosmWasm/wasmd** | `CosmWasm/wasmd v0.50.0` | WASM smart contract runtime |
| 19 | **Cosmos SDK** | `cosmos-sdk v0.50.11` | Blockchain application framework (modules, keepers) |
| 20 | **SDK modules** | auth, bank, capability, ibc, transfer, wasm | Per-module state + msg handlers (xem [Section 17](#17-cosmos-sdk-modules)) |
| 21 | **PersistStore (JSONL)** | Pure Go (tự implement) | Audit log: tx results, blocks, blobs trên disk (xem [Section 19](#19-persiststore-jsonl)) |
| 22 | **BoltDB** | `hashicorp/raft-boltdb` (qua `bbolt`) | Raft log + stable storage (B+ tree, ACID, single-file) |
