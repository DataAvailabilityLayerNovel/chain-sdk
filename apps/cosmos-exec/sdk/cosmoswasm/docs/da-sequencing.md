# Kiến trúc Data Availability & Sequencing (ev-node)

Đào sâu tầng DA và hệ thống sequencing của ev-node. Bản tiếng Việt, dịch từ tài liệu
nội bộ ev-node, **giữ nguyên cấu trúc + code minh hoạ**, kèm chú thích đối chiếu dự án.

> Liên quan: [block-architecture.md](block-architecture.md) (gói block), và thesis
> [03-data-availability](thesis/docs/03-data-availability.md),
> [04-sequencing](thesis/docs/04-sequencing.md),
> [10-forced-inclusion](thesis/docs/10-forced-inclusion.md).
>
> 🧭 **Đối chiếu dự án:** `cosmos-exec` dùng **single sequencer** mặc định của ev-node
> và **một DA wrapper riêng cho lớp dApp** (`BlobClient`, nói JSON-RPC thẳng với
> Celestia — xem [api-reference.md](api-reference.md#blob-first-da-blobclient)). Các
> điểm khác được đánh dấu 🧭.

## Tầng Data Availability

### Tổng quan

Tầng DA trừu tượng hoá việc lưu/đọc blob. ev-node dùng Celestia làm bản cài đặt DA
chính, nhưng giao diện là **pluggable** (có thể thay).

### Cấu trúc thư mục

```
pkg/da/
├── types/
│   ├── types.go        # Type DA lõi (Blob, ID, Commitment, Proof)
│   ├── namespace.go    # Xử lý namespace (29 byte: version + ID)
│   └── errors.go       # Định nghĩa lỗi
├── selector.go         # Chọn địa chỉ round-robin
└── jsonrpc/            # Celestia JSON-RPC client

block/internal/da/
├── client.go                      # Wrapper DA client
├── interface.go                   # Interface Client + Verifier
├── forced_inclusion_retriever.go  # Lấy tx forced theo epoch
└── async_block_retriever.go       # Tiền nạp (prefetch) nền
```

### Type lõi

```go
// Mã trạng thái cho thao tác DA
const (
    StatusSuccess
    StatusNotFound
    StatusNotIncludedInBlock
    StatusAlreadyInMempool
    StatusTooBig
    StatusContextDeadline
    StatusError
    StatusIncorrectAccountSequence
    StatusContextCanceled
    StatusHeightFromFuture
)

// Nguyên thủy blob
type Blob = []byte        // Dữ liệu gửi lên DA
type ID = []byte          // Height + commitment để định vị blob
type Commitment = []byte  // Cam kết mật mã
type Proof = []byte       // Bằng chứng inclusion
```

### Định dạng Namespace

Namespace dài 29 byte:
- **Version** (1 byte): phiên bản giao thức (tối đa 255)
- **ID** (28 byte): định danh namespace

Quy tắc Version 0:
- 18 byte đầu của ID phải bằng 0
- Còn 10 byte cho dữ liệu người dùng

```go
func NewNamespaceV0(id []byte) (Namespace, error) {
    if len(id) > 10 {
        return Namespace{}, ErrInvalidNamespaceLength
    }
    ns := Namespace{Version: 0}
    copy(ns.ID[28-len(id):], id)  // Pad zero bên trái
    return ns, nil
}
```

> 🧭 SDK dự án có bản tương đương `NamespaceFromString` / `NewNamespaceV0` /
> `NamespaceFromHex` ([namespace.go](../namespace.go)) — cùng quy tắc 29 byte.

### Giao diện DA Client

```go
type Client interface {
    // Submit blob lên tầng DA
    Submit(ctx context.Context, data [][]byte, gasPrice float64,
           namespace []byte, options []byte) ResultSubmit

    // Lấy mọi blob ở height cho namespace
    Retrieve(ctx context.Context, height uint64,
             namespace []byte) ResultRetrieve

    // Lấy blob cụ thể theo ID
    Get(ctx context.Context, ids []ID, namespace []byte) ([]Blob, error)

    // Accessor namespace
    GetHeaderNamespace() []byte
    GetDataNamespace() []byte
    GetForcedInclusionNamespace() []byte
    HasForcedInclusionNamespace() bool
}

type Verifier interface {
    GetProofs(ctx context.Context, ids []ID, namespace []byte) ([]Proof, error)
    Validate(ctx context.Context, ids []ID, proofs []Proof,
             namespace []byte) ([]bool, error)
}

type FullClient interface {
    Client
    Verifier
}
```

### Luồng Submit

```go
func (c *Client) Submit(ctx, data, gasPrice, namespace, options) ResultSubmit {
    // 1. Kiểm tra kích thước blob
    for _, blob := range data {
        if len(blob) > DefaultMaxBlobSize {
            return ResultSubmit{Code: StatusTooBig}
        }
    }

    // 2. Tạo Celestia blob kèm namespace
    blobs := make([]*blob.Blob, len(data))
    for i, d := range data {
        blobs[i], _ = blob.NewBlobV0(namespace, d)
    }

    // 3. Submit qua RPC
    height, err := c.blobRPC.Submit(ctx, blobs, submitOptions)

    // 4. Trả kết quả kèm ID
    return ResultSubmit{
        Code:   StatusSuccess,
        Height: height,
        IDs:    createIDs(height, blobs),
    }
}
```

### Luồng Retrieve

```go
func (c *Client) Retrieve(ctx, height, namespace) ResultRetrieve {
    // 1. Lấy mọi blob ở height
    blobs, err := c.blobRPC.GetAll(ctx, height, []Namespace{namespace})

    // 2. Xử lý lỗi
    if errors.Is(err, ErrBlobNotFound) {
        return ResultRetrieve{Code: StatusNotFound}
    }
    if errors.Is(err, ErrHeightFromFuture) {
        return ResultRetrieve{Code: StatusHeightFromFuture}
    }

    // 3. Lấy timestamp từ header DA
    header, _ := c.headerRPC.GetByHeight(ctx, height)

    // 4. Trích dữ liệu blob
    data := make([][]byte, len(blobs))
    for i, b := range blobs {
        data[i] = b.Data()
    }

    return ResultRetrieve{
        Code:      StatusSuccess,
        Height:    height,
        Timestamp: header.Time().UnixNano(),
        Data:      data,
    }
}
```

### Chọn địa chỉ (Address Selection)

Để tương thích Cosmos SDK (tránh lệch sequence khi nhiều tx PFB song song):

```go
type RoundRobinSelector struct {
    addresses []string
    counter   atomic.Uint64
}

func (s *RoundRobinSelector) Next() string {
    idx := s.counter.Add(1) % uint64(len(s.addresses))
    return s.addresses[idx]
}
```

> 🧭 Cấu hình qua `SigningAddresses` trong `DAConfig` (xem
> [08-config-fee.md](thesis/docs/08-config-fee.md)). Mỗi tx PFB lên Celestia là một
> tx Cosmos có sequence riêng; round-robin nhiều ví ký giúp tránh "account sequence
> mismatch" khi submit dồn dập.

---

## Hệ thống Sequencing

### Tổng quan

Sequencer sắp xếp giao dịch để sản xuất block. ev-node hỗ trợ hai chế độ:
- **Single Sequencer**: lai (mempool + forced inclusion)
- **Based Sequencer**: thuần DA (chỉ forced inclusion)

### Cấu trúc thư mục

```
core/sequencer/
├── sequencing.go    # Interface lõi
└── dummy.go         # Bản cài đặt cho test

pkg/sequencers/
├── single/
│   ├── sequencer.go # Sequencer lai
│   └── queue.go     # Hàng đợi batch bền vững
├── based/
│   └── sequencer.go # Sequencer thuần DA
└── common/
    └── checkpoint.go # Logic checkpoint dùng chung
```

### Interface lõi

```go
type Sequencer interface {
    // Reaper đẩy tx vào sequencer
    SubmitBatchTxs(ctx, req SubmitBatchTxsRequest) (*SubmitBatchTxsResponse, error)

    // Lấy batch tiếp theo để sản xuất block
    GetNextBatch(ctx, req GetNextBatchRequest) (*GetNextBatchResponse, error)

    // Xác minh batch đã được đưa vào DA
    VerifyBatch(ctx, req VerifyBatchRequest) (*VerifyBatchResponse, error)

    // Theo dõi chiều cao DA cho forced inclusion
    SetDAHeight(height uint64)
    GetDAHeight() uint64
}
```

### Cấu trúc Batch

```go
type Batch struct {
    Transactions [][]byte

    // ForceIncludedMask[i] == true:  Từ DA (BẮT BUỘC validate)
    // ForceIncludedMask[i] == false: Từ mempool (đã validate)
    // nil: Tương thích ngược (validate tất cả)
    ForceIncludedMask []bool
}
```

### Single Sequencer (lai)

Nhận cả tx từ mempool lẫn forced inclusion từ DA.

**Thành phần:**

1. **BatchQueue** — lưu mempool bền vững
   ```go
   type BatchQueue struct {
       db        DB
       maxSize   uint64
       nextSeq   uint64  // Bắt đầu từ 0x8000000000000000
   }

   func (q *BatchQueue) AddBatch(batch [][]byte) error
   func (q *BatchQueue) Next() ([][]byte, error)
   func (q *BatchQueue) Prepend(batch [][]byte) error  // Trả lại tx chưa dùng
   ```

2. **Checkpoint** — theo dõi vị trí trong epoch DA
   ```go
   type Checkpoint struct {
       DAHeight uint64  // Chiều cao DA đang xử lý
       TxIndex  uint64  // Vị trí trong epoch
   }
   ```

**Luồng GetNextBatch:**

```go
func (s *SingleSequencer) GetNextBatch(ctx, req) (*Response, error) {
    // 1. Cần lấy epoch DA mới không?
    if s.checkpoint.DAHeight > 0 && len(s.cachedForcedTxs) == 0 {
        s.fetchNextDAEpoch(ctx)
    }

    // 2. Xử lý tx forced từ checkpoint
    forcedTxs, forcedBytes := s.processForcedTxs(req.MaxBytes)

    // 3. Lấy tx mempool (chỗ còn lại)
    mempoolTxs := s.queue.Next()
    mempoolTxs = truncateToSize(mempoolTxs, req.MaxBytes - forcedBytes)

    // 4. Trả tx mempool chưa dùng về hàng đợi
    s.queue.Prepend(unusedTxs)

    // 5. Ghép batch
    batch := &Batch{
        Transactions:      append(forcedTxs, mempoolTxs...),
        ForceIncludedMask: makeMask(len(forcedTxs), len(mempoolTxs)),
    }

    // 6. Cập nhật + lưu checkpoint
    s.updateCheckpoint()

    return &Response{Batch: batch, Timestamp: s.timestamp}
}
```

> 🧭 Dự án dùng **single sequencer mặc định**, KHÔNG kích hoạt based. Đáng chú ý:
> mempool thực thi của `cosmos-exec` nằm ở **executor app** (`CosmosExecutor`), không
> ở sequencer — xem [04-sequencing §4.9](thesis/docs/04-sequencing.md).

### Based Sequencer (thuần DA)

Chỉ xử lý tx forced inclusion. Không có mempool.

**Khác biệt chính:**

```go
func (s *BasedSequencer) SubmitBatchTxs(ctx, req) (*Response, error) {
    // No-op: bỏ qua tx mempool
    return &SubmitBatchTxsResponse{}, nil
}

func (s *BasedSequencer) GetNextBatch(ctx, req) (*Response, error) {
    // Chỉ trả tx forced inclusion
    txs := s.fetchForcedInclusion(ctx)

    // Giãn timestamp: tránh trùng timestamp
    // timestamp = DAEpochEndTime - (remainingTxs * 1ms)
    timestamp := s.calculateSpreadTimestamp()

    return &Response{
        Batch:     &Batch{Transactions: txs},
        Timestamp: timestamp,
    }
}

func (s *BasedSequencer) VerifyBatch(ctx, req) (*Response, error) {
    // Luôn true: mọi tx đến từ DA (đã verify)
    return &VerifyBatchResponse{Status: true}, nil
}
```

### Luồng Forced Inclusion

```
Người dùng submit tx vào namespace forced-inclusion của DA
         │
         ▼
DA lưu tx ở chiều cao H
         │
         ▼
Sequencer phát hiện ranh giới epoch
         │
         ▼
ForcedInclusionRetriever.Retrieve(epochStart, epochEnd)
         │
         ├── AsyncBlockRetriever kiểm tra cache
         │         ├── Cache hit: trả block trong cache
         │         └── Cache miss: lấy đồng bộ từ DA
         │
         ▼
Trả ForcedInclusionEvent{Txs, Timestamp}
         │
         ▼
Sequencer cache tx, cập nhật checkpoint
         │
         ▼
GetNextBatch trả tx với ForceIncludedMask[i]=true
         │
         ▼
Executor truyền mask xuống tầng thực thi
         │
         ▼
Tầng thực thi validate tx forced (bỏ qua validate kiểu mempool)
```

### Async Block Retriever

Tiền nạp nền giảm độ trễ:

```go
type AsyncBlockRetriever struct {
    client        DAClient
    cache         map[uint64]*Block  // Cache trong RAM
    currentHeight atomic.Uint64
    prefetchSize  uint64             // 2x kích thước epoch
    pollInterval  time.Duration      // Block time của DA
}

func (r *AsyncBlockRetriever) Start(ctx context.Context) {
    go func() {
        for {
            select {
            case <-ctx.Done():
                return
            case <-time.After(r.pollInterval):
                r.prefetch()
            }
        }
    }()
}

func (r *AsyncBlockRetriever) prefetch() {
    current := r.currentHeight.Load()
    end := current + r.prefetchSize

    for h := current; h < end; h++ {
        if _, exists := r.cache[h]; !exists {
            block, _ := r.client.Retrieve(ctx, h, namespace)
            r.cache[h] = block
        }
    }

    // Dọn entry cũ
    r.cleanupBefore(current - r.prefetchSize)
}
```

---

## Tích hợp với gói Block

### Tích hợp Executor

```go
func (e *Executor) initializeState() error {
    state := e.store.GetState()

    // Đồng bộ chiều cao DA của sequencer với state đã lưu
    e.sequencer.SetDAHeight(state.DAHeight)

    return nil
}

func (e *Executor) produceBlock(ctx context.Context) error {
    // 1. Lấy batch từ sequencer
    resp, _ := e.sequencer.GetNextBatch(ctx, GetNextBatchRequest{
        Id:       e.genesis.ChainID,
        MaxBytes: DefaultMaxBlobSize,
    })

    // 2. Truyền ForceIncludedMask xuống tầng thực thi
    ctx = WithForceIncludedMask(ctx, resp.Batch.ForceIncludedMask)

    // 3. Thực thi giao dịch
    stateRoot, _ := e.exec.ExecuteTxs(ctx, resp.Batch.Transactions, ...)

    // 4. Cập nhật state với chiều cao DA mới
    newState := &State{
        DAHeight: e.sequencer.GetDAHeight(),
        // ...
    }

    // 5. Tạo và broadcast block
    // ...
}
```

### Cấu hình

```go
type DAConfig struct {
    Address                  string   // Endpoint RPC Celestia
    AuthToken                string   // Auth token
    Namespace                string   // Header namespace
    DataNamespace            string   // Data namespace (tuỳ chọn)
    ForcedInclusionNamespace string   // Forced inclusion namespace
    BlockTime                Duration // Block time của DA
    SubmitOptions            string   // Cài đặt gas dạng JSON
    SigningAddresses         []string // Địa chỉ round-robin
    MaxSubmitAttempts        int      // Giới hạn retry
    RequestTimeout           Duration // Timeout mỗi request
}

type NodeConfig struct {
    Aggregator      bool     // Bật sản xuất block
    BasedSequencer  bool     // Dùng based sequencer (cần Aggregator)
    BlockTime       Duration // Block time của app
    LazyMode        bool     // Chỉ sản xuất khi có tx
}
```

### Cấu hình Genesis

```go
type Genesis struct {
    DAStartHeight          uint64 // Chiều cao DA đầu tiên (0 lúc genesis)
    DAEpochForcedInclusion uint64 // Kích thước epoch (mặc định 50)
}
```

> 🧭 Dự án chọn `epoch = 10` DA blocks cho demo forced inclusion (lý do trade-off
> độ trễ vs số lần query DA) — xem FAQ câu 2 trong
> [10-forced-inclusion.md](thesis/docs/10-forced-inclusion.md).

---

## Các quyết định thiết kế chính

### 1. Tối ưu ForceIncludedMask

Phân biệt tx từ DA (chưa tin) với tx từ mempool (đã tin):
- Tầng thực thi validate tx forced
- Bỏ qua validate dư thừa cho tx mempool
- Cải thiện hiệu năng đáng kể

### 2. Xử lý theo Epoch

Chỉ lấy forced inclusion ở ranh giới epoch:
- Giảm số query DA
- Cho phép gom batch
- Checkpoint đảm bảo xử lý có thể tiếp tục sau gián đoạn

### 3. Tiền nạp bất đồng bộ

Goroutine nền tiền nạp trước 2x kích thước epoch:
- Giảm độ trễ khi sequencer cần tx
- Cache miss thì lấy đồng bộ bù lại
- Bộ nhớ có giới hạn nhờ dọn định kỳ

### 4. Chiến lược Namespace

Ba namespace tách biệt:
- **Header**: header block (bắt buộc)
- **Data**: dữ liệu giao dịch (tuỳ chọn, có thể dùng chung với header)
- **Forced Inclusion**: tx do người dùng gửi để chống kiểm duyệt

> 🧭 Dự án hiện dùng **một namespace `rollup`** cho cả app blob (chưa bật tách
> header/data riêng) — đơn giản dev, dễ debug. Xem
> [03-data-availability §3.12](thesis/docs/03-data-availability.md).

### 5. Phục hồi sau crash

Cả hai sequencer đều lưu state:
- **Checkpoint**: vị trí DAHeight + TxIndex
- **Queue**: các batch mempool đang chờ
- Serialize protobuf xuống DB

### 6. Single vs Based

| Khía cạnh | Single | Based |
|-----------|--------|-------|
| Mempool | Có | Không |
| Forced Inclusion | Có | Có (nguồn duy nhất) |
| SubmitBatchTxs | Lưu vào queue | No-op |
| VerifyBatch | Validate proof | Luôn true |
| Trường hợp dùng | Rollup truyền thống | Liveness cao |

## Tham chiếu

- Gói gốc: `pkg/da/`, `pkg/sequencers/`, `block/internal/da/` trong repo ev-node.
- Gói block: [block-architecture.md](block-architecture.md).
- DA wrapper của dApp: [api-reference.md](api-reference.md#blob-first-da-blobclient),
  [`blob.go`](../blob.go), [`namespace.go`](../namespace.go).
- Tài liệu thesis sâu hơn (kèm "So sánh với code dự án"):
  [03](thesis/docs/03-data-availability.md), [04](thesis/docs/04-sequencing.md),
  [10](thesis/docs/10-forced-inclusion.md).
