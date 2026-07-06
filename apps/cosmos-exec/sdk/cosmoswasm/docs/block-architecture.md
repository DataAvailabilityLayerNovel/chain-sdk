# Kiến trúc gói Block (ev-node)

Tài liệu tham chiếu kỹ thuật đầy đủ cho gói `block` của ev-node — tầng sản xuất,
đồng bộ và công bố block mà `cosmos-exec` chạy bên dưới. Bản tiếng Việt, dịch từ
tài liệu nội bộ ev-node, **giữ nguyên cấu trúc + code minh hoạ**, kèm chú thích đối
chiếu với dự án.

> Liên quan: [da-sequencing.md](da-sequencing.md) (DA + sequencing), và các tài liệu
> thesis [02-thanh-phan-block](thesis/docs/02-thanh-phan-block.md),
> [07-p2p-finality](thesis/docs/07-p2p-finality.md),
> [10-forced-inclusion](thesis/docs/10-forced-inclusion.md).
>
> 🧭 **Đối chiếu dự án:** `cosmos-exec` **không sửa** gói `block` của ev-node — nó
> nối vào qua giao diện `execution.Executor` (xem `executor/executor.go`). Tài liệu
> này mô tả phần khung mà full node `evcosmos` dùng. Các điểm dự án khác (mempool ở
> executor app, single namespace, forced inclusion opt-in) được đánh dấu 🧭.

## Cấu trúc thư mục

```
block/
├── public.go                    # Type export, factory DA client
├── components.go                # Tạo & quản lý vòng đời component
└── internal/
    ├── common/
    │   ├── errors.go            # Định nghĩa lỗi
    │   ├── event.go             # DAHeightEvent, các loại event
    │   ├── metrics.go           # Prometheus metrics
    │   ├── options.go           # Cấu hình BlockOptions
    │   ├── expected_interfaces.go
    │   └── replay.go            # Replayer phục hồi sau crash
    ├── executing/
    │   └── executor.go          # Vòng lặp sản xuất block
    ├── syncing/
    │   ├── syncer.go            # Điều phối đồng bộ chính
    │   ├── da_retriever.go      # Lấy block từ DA
    │   └── p2p_handler.go       # Điều phối block qua P2P
    ├── submitting/
    │   ├── submitter.go         # Vòng lặp submit chính
    │   └── da_submitter.go      # Submit DA kèm retry
    ├── reaping/
    │   └── reaper.go            # Quét (scrape) giao dịch
    ├── cache/
    │   ├── manager.go           # Giao diện cache hợp nhất
    │   ├── generic_cache.go     # Cache generic
    │   ├── pending_headers.go   # Theo dõi header chờ
    │   └── pending_data.go      # Theo dõi data chờ
    └── da/
        ├── client.go            # Wrapper DA client
        ├── interface.go         # Các interface DA
        ├── async_block_retriever.go
        └── forced_inclusion_retriever.go
```

## Vòng đời component

Mọi component đều cài đặt:

```go
type Component interface {
    Start(ctx context.Context) error
    Stop() error
}
```

Thứ tự khởi động:

1. Cache Manager (nạp state đã lưu)
2. Syncer (chạy các worker đồng bộ)
3. Executor (chạy vòng lặp sản xuất) — chỉ **node aggregator**
4. Reaper (chạy quét tx) — chỉ **node aggregator**
5. Submitter (chạy submit DA)

> 🧭 `evcosmos` chạy ở chế độ aggregator khi là sequencer (sản xuất block), hoặc
> non-aggregator khi là full node chỉ đồng bộ. Bật/tắt qua cờ `--evnode.node.aggregator`.

## Executor (`internal/executing/executor.go`)

Sản xuất block cho node aggregator.

### State

```go
type Executor struct {
    lastState      *atomic.Pointer[types.State]
    sequencer      Sequencer
    exec           Executor
    broadcaster    Broadcaster
    submitter      Submitter
    cache          Cache

    blockTime      time.Duration
    lazyMode       bool
    maxPending     uint64
}
```

### Vòng lặp chính

```go
func (e *Executor) executionLoop(ctx context.Context) {
    timer := time.NewTimer(e.blockTime)

    for {
        select {
        case <-ctx.Done():
            return
        case <-timer.C:
            e.produceBlock(ctx)
            timer.Reset(e.blockTime)
        case <-e.txNotifyCh:
            // Có tx mới đến, sản xuất ngay nếu không ở lazy mode
            if !e.lazyMode {
                e.produceBlock(ctx)
                timer.Reset(e.blockTime)
            }
        }
    }
}
```

> 🧭 **Lazy mode** (`--evnode.node.lazy_mode`) chỉ ra block khi có tx → diệt phần lớn
> block rỗng, giảm mạnh chi phí DA. Xem [fee-economics.md §5](fee-economics.md#muc-5).

### Sản xuất block

```go
func (e *Executor) produceBlock(ctx context.Context) error {
    // 1. Kiểm tra backpressure (quá nhiều block chờ submit)
    if e.cache.PendingCount() >= e.maxPending {
        return ErrTooManyPending
    }

    // 2. Lấy batch từ sequencer
    batch, err := e.sequencer.GetNextBatch(ctx)

    // 3. Thực thi giao dịch
    stateRoot, gasUsed, err := e.exec.ExecuteTxs(ctx, batch.Txs, ...)

    // 4. Tạo header
    header := &types.Header{
        Height:          lastState.LastBlockHeight + 1,
        Time:            time.Now().UnixNano(),
        LastHeaderHash:  lastState.LastHeaderHash,
        DataHash:        batch.Txs.Hash(),
        AppHash:         stateRoot,
        ProposerAddress: e.proposer,
    }

    // 5. Ký header
    signedHeader, err := e.signer.SignHeader(header)

    // 6. Tạo data
    data := &types.Data{Txs: batch.Txs}

    // 7. Cập nhật state
    newState := lastState.NextState(header, stateRoot)
    e.lastState.Store(newState)

    // 8. Broadcast qua P2P
    e.broadcaster.BroadcastHeader(ctx, signedHeader)
    e.broadcaster.BroadcastData(ctx, data)

    // 9. Xếp hàng để submit lên DA
    e.submitter.AddPending(signedHeader, data)

    return nil
}
```

> 🧭 Bước 3 `ExecuteTxs` chính là điểm `cosmos-exec` cắm vào: `CosmosExecutor`
> ([executor/executor.go](../../../executor/executor.go)) cài đặt `execution.Executor`,
> chạy tx qua Cosmos SDK App + CosmWasm rồi trả `stateRoot` (app_hash).

## Syncer (`internal/syncing/syncer.go`)

Điều phối đồng bộ block từ nhiều nguồn.

### Các worker

```go
func (s *Syncer) startSyncWorkers(ctx context.Context) {
    go s.daWorkerLoop(ctx)          // Lấy từ DA
    go s.pendingWorkerLoop(ctx)     // Xử lý event chờ
    go s.p2pWorkerLoop(ctx)         // Block qua P2P
}
```

### DA Worker

```go
func (s *Syncer) daWorkerLoop(ctx context.Context) {
    for {
        // Lấy chiều cao DA tiếp theo cần truy
        height := s.daRetrieverHeight.Load()

        // Lấy các block ở chiều cao DA này
        events, err := s.daRetriever.Retrieve(ctx, height)

        // Gửi vào kênh xử lý
        for _, event := range events {
            s.heightInCh <- event
        }

        // Tiến chiều cao DA
        s.daRetrieverHeight.Add(1)
    }
}
```

### P2P Worker

```go
func (s *Syncer) p2pWorkerLoop(ctx context.Context) {
    for {
        select {
        case header := <-s.p2pHandler.HeaderCh():
            s.p2pHandler.HandleHeader(header)
        case data := <-s.p2pHandler.DataCh():
            s.p2pHandler.HandleData(data)
        case event := <-s.p2pHandler.EventCh():
            // Đã nhận đủ cặp header+data
            s.heightInCh <- event
        }
    }
}
```

### Vòng lặp xử lý

```go
func (s *Syncer) processLoop(ctx context.Context) {
    for {
        select {
        case event := <-s.heightInCh:
            if err := s.processHeightEvent(ctx, event); err != nil {
                // Log lỗi, tiếp tục
            }
        case <-ctx.Done():
            return
        }
    }
}

func (s *Syncer) processHeightEvent(ctx context.Context, event DAHeightEvent) error {
    // 1. Xác minh chữ ký header
    if err := s.verifyHeader(event.SignedHeader); err != nil {
        return err
    }

    // 2. Kiểm tra data hash khớp header
    if event.SignedHeader.DataHash != event.Data.Hash() {
        return ErrDataHashMismatch
    }

    // 3. Thực thi lại giao dịch
    stateRoot, _, err := s.exec.ExecuteTxs(ctx, event.Data.Txs, ...)

    // 4. Đối chiếu state root
    if stateRoot != event.SignedHeader.AppHash {
        return ErrStateRootMismatch
    }

    // 5. Cập nhật state
    newState := s.lastState.NextState(event.SignedHeader.Header, stateRoot)
    s.lastState.Store(newState)

    // 6. Lưu vào store
    s.store.SaveBlock(event.SignedHeader, event.Data, newState)

    return nil
}
```

> 🧭 Bước 3–4 là **sovereign verification**: full node tự thực thi lại tx và **so
> app_hash mình tính được** với app_hash trong header đã ký. Đây là điểm Celestia chỉ
> bảo đảm *dữ liệu được công bố*, còn *tính đúng đắn state* do node tự kiểm — đúng như
> [da-flow trong thesis Chương 4](thesis/thong-ke-ma-nguon.md).

## Submitter (`internal/submitting/submitter.go`)

Quản lý submit lên DA kèm retry và theo dõi inclusion.

### Hai vòng lặp

```go
func (s *Submitter) Start(ctx context.Context) error {
    go s.daSubmissionLoop(ctx)        // Submit lên DA
    go s.inclusionProcessingLoop(ctx) // Theo dõi inclusion
    return nil
}
```

### Vòng lặp submit DA

```go
func (s *Submitter) daSubmissionLoop(ctx context.Context) {
    for {
        // Lấy header đang chờ
        headers := s.cache.GetPendingHeaders()
        if len(headers) > 0 {
            if err := s.submitHeaders(ctx, headers); err != nil {
                s.handleSubmitError(err)
                continue
            }
        }

        // Lấy data đang chờ
        data := s.cache.GetPendingData()
        if len(data) > 0 {
            if err := s.submitData(ctx, data); err != nil {
                s.handleSubmitError(err)
                continue
            }
        }

        time.Sleep(s.submitInterval)
    }
}
```

### Chính sách retry

```go
type DASubmitter struct {
    maxRetries     int
    initialBackoff time.Duration
    maxBackoff     time.Duration
}

func (d *DASubmitter) Submit(ctx context.Context, blob []byte) error {
    backoff := d.initialBackoff

    for attempt := 0; attempt < d.maxRetries; attempt++ {
        status, err := d.client.Submit(ctx, blob)

        switch status {
        case StatusSuccess:
            return nil
        case StatusTooBig:
            return d.splitAndSubmit(ctx, blob)
        case StatusAlreadyInMempool:
            return nil // Đã submit rồi
        case StatusNotIncludedInBlock:
            time.Sleep(backoff)
            backoff = min(backoff*2, d.maxBackoff)  // exponential backoff
            continue
        default:
            return err
        }
    }

    return ErrMaxRetriesExceeded
}
```

> 🧭 Mỗi block công bố **hai blob** (SignedHeader + SignedData) lên hai namespace
> Celestia. Chi phí PFB thật và cách đo xem [fee-economics.md §1c](fee-economics.md#muc-1c).

## Forced Inclusion (`internal/da/forced_inclusion_retriever.go`)

Chống kiểm duyệt (censorship) từ sequencer.

### Tính grace period

```go
func (r *ForcedInclusionRetriever) calculateGracePeriod() uint64 {
    // Khoảng cơ bản: 1 epoch
    basePeriod := r.epochLength

    // Điều chỉnh theo độ đầy của block
    // Càng đầy = grace period càng dài (chịu được nghẽn)
    ema := r.blockFullnessEMA.Load()

    if ema > 0.8 {
        // Nghẽn cao, kéo dài grace period
        return basePeriod * 2
    }

    return basePeriod
}
```

### Theo dõi tx chờ

```go
type PendingForcedTx struct {
    Tx            types.Tx
    DAHeight      uint64    // Khi tx xuất hiện trong DA
    GraceDeadline uint64    // Hạn chót (chiều cao DA) phải include
}

func (r *ForcedInclusionRetriever) checkPending(currentDAHeight uint64) {
    for _, pending := range r.pendingTxs {
        if currentDAHeight > pending.GraceDeadline {
            // Sequencer không include được tx
            r.markSequencerMalicious(pending)
            // Buộc include tx
            r.forceInclude(pending.Tx)
        }
    }
}
```

> 🧭 Dự án bật forced inclusion **opt-in** qua cờ `--forced-inclusion-namespace`
> (mặc định tắt). Cách bật, demo và 15 câu FAQ hội đồng: xem
> [10-forced-inclusion.md](thesis/docs/10-forced-inclusion.md).

## Cache Manager (`internal/cache/manager.go`)

Cache hợp nhất cho header, data và giao dịch.

### Cấu trúc

```go
type Manager struct {
    headerCache    *GenericCache[types.Hash, HeaderEntry]
    dataCache      *GenericCache[types.Hash, DataEntry]
    txCache        *GenericCache[types.Hash, TxEntry]
    pendingEvents  map[uint64]*DAHeightEvent

    cleanupTicker  *time.Ticker
    retentionTime  time.Duration
}
```

### Các thao tác chính

```go
// Theo dõi header
func (m *Manager) IsHeaderSeen(hash types.Hash) bool
func (m *Manager) SetHeaderSeen(hash types.Hash, height uint64)
func (m *Manager) GetHeaderDAIncluded(hash types.Hash) (uint64, bool)
func (m *Manager) SetHeaderDAIncluded(hash types.Hash, daHeight uint64)

// Khử trùng lặp giao dịch
func (m *Manager) IsTxSeen(hash types.Hash) bool
func (m *Manager) SetTxSeen(hash types.Hash)

// Quản lý hàng chờ
func (m *Manager) GetPendingHeaders() []*types.SignedHeader
func (m *Manager) GetPendingData() []*types.Data
func (m *Manager) PendingCount() uint64
```

### Lưu xuống đĩa

```go
func (m *Manager) SaveToDisk(path string) error {
    state := &CacheState{
        Headers:  m.headerCache.Entries(),
        Data:     m.dataCache.Entries(),
        Pending:  m.pendingEvents,
    }
    return json.WriteFile(path, state)
}

func (m *Manager) LoadFromDisk(path string) error {
    state, err := json.ReadFile(path)
    // Khôi phục cache từ state
}
```

## Replayer (`internal/common/replay.go`)

Đồng bộ tầng thực thi sau khi crash.

```go
func (r *Replayer) Replay(ctx context.Context) error {
    // Lấy các chiều cao
    storeHeight := r.store.GetLastHeight()
    execHeight := r.exec.GetHeight()

    if execHeight >= storeHeight {
        return nil // Đã đồng bộ
    }

    // Phát lại các block còn thiếu
    for height := execHeight + 1; height <= storeHeight; height++ {
        header, data, err := r.store.GetBlock(height)
        if err != nil {
            return err
        }

        _, _, err = r.exec.ExecuteTxs(ctx, data.Txs, ...)
        if err != nil {
            return err
        }
    }

    return nil
}
```

> 🧭 Vì `CosmosExecutor` giữ `GetHeight()` (chiều cao đã thực thi) và state đĩa riêng
> (`PersistStore`: `metadata.json`/`blocks.jsonl`/`tx_results.jsonl`), Replayer dùng
> chênh lệch `storeHeight` vs `execHeight` để phát lại đúng các block thiếu sau crash.

## Metrics (`internal/common/metrics.go`)

```go
var (
    Height              = prometheus.NewGauge(...)
    NumTxs              = prometheus.NewGauge(...)
    BlockSizeBytes      = prometheus.NewHistogram(...)
    CommittedHeight     = prometheus.NewGauge(...)
    TxsPerBlock         = prometheus.NewHistogram(...)
    OperationDuration   = prometheus.NewHistogramVec(...)

    // DA metrics
    DASubmitterFailures     = prometheus.NewCounterVec(...)
    DASubmitterLastFailure  = prometheus.NewGauge(...)
    DASubmitterPendingBlobs = prometheus.NewGauge(...)
    DARetrievalAttempts     = prometheus.NewCounter(...)
    DARetrievalSuccesses    = prometheus.NewCounter(...)
    DARetrievalFailures     = prometheus.NewCounter(...)
    DAInclusionHeight       = prometheus.NewGauge(...)

    // Forced inclusion
    ForcedInclusionTxsInGracePeriod = prometheus.NewGauge(...)
    ForcedInclusionTxsMalicious     = prometheus.NewCounter(...)
)
```

> 🧭 Đây là metrics của **tầng ev-node**. Máy chủ `cosmos-exec-grpc` còn phơi metrics
> riêng của mình qua `/metrics` (Prometheus) và `/metrics.json` — xem
> [swagger-api.md](swagger-api.md).

## Cấu hình

Các option chính trong `BlockOptions`:

```go
type BlockOptions struct {
    BlockTime                time.Duration  // Khoảng giữa các block
    LazyBlockInterval        time.Duration  // Timeout lazy mode
    MaxPendingHeadersAndData uint64         // Giới hạn backpressure
    BasedSequencer          bool            // Không submit DA
    DABlockTime             time.Duration   // Khoảng block DA
    ScrapeInterval          time.Duration   // Tần suất reap tx

    // Namespaces
    HeaderNamespace          []byte
    DataNamespace            []byte
    ForcedInclusionNamespace []byte
}
```

## Các loại lỗi

```go
var (
    ErrNoHeader           = errors.New("no header found")
    ErrNoData             = errors.New("no data found")
    ErrDataHashMismatch   = errors.New("data hash does not match header")
    ErrStateRootMismatch  = errors.New("state root mismatch after execution")
    ErrInvalidSignature   = errors.New("invalid header signature")
    ErrTooManyPending     = errors.New("too many pending submissions")
    ErrMaxRetriesExceeded = errors.New("max DA submission retries exceeded")
    ErrSequencerMalicious = errors.New("sequencer failed to include forced tx")
)
```

## Máy trạng thái (State machines)

### Executor

```
┌──────────────┐
│   IDLE       │
└──────┬───────┘
       │ Hết BlockTime HOẶC TxNotify
       ▼
┌──────────────┐
│ CHECK_PENDING│──── Quá nhiều? ───► Chờ
└──────┬───────┘
       │ OK
       ▼
┌──────────────┐
│ GET_BATCH    │
└──────┬───────┘
       ▼
┌──────────────┐
│ EXECUTE_TXS  │
└──────┬───────┘
       ▼
┌──────────────┐
│ CREATE_BLOCK │
└──────┬───────┘
       ▼
┌──────────────┐
│ BROADCAST    │
└──────┬───────┘
       ▼
┌──────────────┐
│ QUEUE_SUBMIT │───► Về IDLE
└──────────────┘
```

### Syncer

```
┌─────────────────────────────────────────┐
│              START                       │
└──────────────────┬──────────────────────┘
    ┌──────────────┼──────────────┐
    ▼              ▼              ▼
┌───────┐    ┌───────┐    ┌───────────┐
│  DA   │    │  P2P  │    │ FORCED    │
│WORKER │    │WORKER │    │ INCLUSION │
└───┬───┘    └───┬───┘    └─────┬─────┘
    └────────────┴──────────────┘
                 ▼
         ┌──────────────┐
         │ PROCESS_LOOP │
         └──────┬───────┘
                ▼
         ┌──────────────┐
         │   VALIDATE   │──── Sai? ───► Log, bỏ qua
         └──────┬───────┘
                │ Đúng
                ▼
         ┌──────────────┐
         │   EXECUTE    │
         └──────┬───────┘
                ▼
         ┌──────────────┐
         │  UPDATE_STATE│
         └──────┬───────┘
                ▼
         ┌──────────────┐
         │    PERSIST   │───► Về PROCESS_LOOP
         └──────────────┘
```

### Submitter

```
┌──────────────┐
│    START     │
└──────┬───────┘
       ├─────────────────────┐
       ▼                     ▼
┌──────────────┐    ┌──────────────────┐
│ SUBMIT_LOOP  │    │ INCLUSION_LOOP   │
└──────┬───────┘    └────────┬─────────┘
       ▼                     ▼
┌──────────────┐    ┌──────────────────┐
│ GET_PENDING  │    │ CHECK_DA_HEIGHT  │
└──────┬───────┘    └────────┬─────────┘
       ▼                     │ Đã include?
┌──────────────┐             ▼
│   SUBMIT     │    ┌──────────────────┐
└──────┬───────┘    │ RESET_STATE      │
       │ Lỗi?       └────────┬─────────┘
       ▼                     │
┌──────────────┐             │
│   RETRY      │             │
│  (backoff)   │             │
└──────┬───────┘             │
       └─────────────────────┘
```

## Tham chiếu

- Gói gốc: `block/` trong repo ev-node.
- Đối chiếu dự án: [`executor/executor.go`](../../../executor/executor.go) (cài
  `execution.Executor`), [`executor/persist.go`](../../../executor/persist.go).
- DA + sequencing: [da-sequencing.md](da-sequencing.md).
- Tài liệu thesis sâu hơn (kèm "So sánh với code dự án"):
  [02](thesis/docs/02-thanh-phan-block.md), [07](thesis/docs/07-p2p-finality.md),
  [10](thesis/docs/10-forced-inclusion.md).
