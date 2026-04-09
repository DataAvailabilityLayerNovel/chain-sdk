package cosmoswasm

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	// DefaultBatchMaxBytes is the size threshold at which a batch auto-flushes (3 MB).
	// Mirrors the submitter pattern from block/internal/submitting/da_submitter.go.
	DefaultBatchMaxBytes = 3 * 1024 * 1024
	// DefaultBatchFlushInterval is the time-based flush interval (5 s).
	DefaultBatchFlushInterval = 5 * time.Second
)

// FlushFunc is called by BatchBuilder whenever a batch is ready to commit.
// Implementations typically call client.CommitRoot.
type FlushFunc func(ctx context.Context, blobs [][]byte) (*CommitReceipt, error)

// BatchBuilder accumulates data blobs and flushes them as a single CommitRoot
// call when either:
//   - the total accumulated size reaches MaxBytes, or
//   - StartAutoFlush's interval fires.
//
// It mirrors the size-capping + retry logic from EVNode's da_submitter
// (limitBatchBySize) adapted for the Cosmos SDK client layer.
//
// Typical usage for a game server emitting frequent small events:
//
//	bb := cosmoswasm.NewBatchBuilder(client, cosmoswasm.BatchBuilderConfig{
//	    Contract: gameContractAddr,
//	    Tag:      "game-events",
//	})
//	ctx, cancel := context.WithCancel(context.Background())
//	receipts := bb.StartAutoFlush(ctx, 5*time.Second)
//	defer cancel()
//
//	// In your game loop:
//	bb.Add(eventBytes)
type BatchBuilder struct {
	mu         sync.Mutex
	client     *Client
	config     BatchBuilderConfig
	blobs      [][]byte
	totalBytes int
}

// BatchBuilderConfig controls the BatchBuilder behaviour.
type BatchBuilderConfig struct {
	// Contract is the bech32 WASM contract address that records batch roots.
	Contract string
	// Sender is optional; uses DefaultSender when empty.
	Sender string
	// Tag is an optional application label attached to every CommitRoot call.
	Tag string
	// MaxBytes is the size-based flush threshold (default: DefaultBatchMaxBytes).
	MaxBytes int
	// Extra holds optional extra fields merged into the on-chain root message.
	Extra map[string]any

	// Compress enables gzip compression before accumulating blobs.
	// When true, each Add call compresses the data (if beneficial) before
	// adding it to the batch buffer.  This can reduce DA costs by 50-70 %
	// for structured data (JSON game events, logs).
	// Default: true.
	Compress *bool

	// MaxChunkSize is the per-blob size limit before automatic chunking.
	// Blobs larger than this are split into chunks before being added to the
	// batch.  Zero defaults to DefaultMaxChunkSize (512 KiB).
	MaxChunkSize int
}

// NewBatchBuilder creates a BatchBuilder backed by client.
// Compression is enabled by default; set Compress to ptr(false) to disable.
func NewBatchBuilder(client *Client, cfg BatchBuilderConfig) *BatchBuilder {
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultBatchMaxBytes
	}
	if cfg.Compress == nil {
		t := true
		cfg.Compress = &t
	}
	if cfg.MaxChunkSize <= 0 {
		cfg.MaxChunkSize = DefaultMaxChunkSize
	}
	return &BatchBuilder{client: client, config: cfg}
}

// compressEnabled returns whether gzip compression is on.
func (b *BatchBuilder) compressEnabled() bool {
	return b.config.Compress != nil && *b.config.Compress
}

// Add appends data to the current batch.  If adding data would push the total
// over MaxBytes, the existing batch is flushed first (synchronously) using fn,
// then data is added to a fresh batch.
//
// When Compress is enabled (default), data is gzip-compressed before
// accumulating if compression is beneficial.
// When data exceeds MaxChunkSize, it is automatically split into chunks.
//
// fn must not be nil.  Returns (nil, nil) when no flush was triggered.
func (b *BatchBuilder) Add(ctx context.Context, data []byte, fn FlushFunc) (*CommitReceipt, error) {
	if len(data) == 0 {
		return nil, errors.New("data cannot be empty")
	}
	if fn == nil {
		return nil, errors.New("flush function cannot be nil")
	}

	// Compress if enabled and beneficial.
	payload := data
	if b.compressEnabled() {
		if compressed, ok := CompressIfBeneficial(data); ok {
			payload = compressed
		}
	}

	// Chunk if payload exceeds per-blob chunk limit.
	chunks, _ := ChunkBlob(payload, b.config.MaxChunkSize)

	// Add each chunk (or the single blob) to the batch.
	var lastReceipt *CommitReceipt
	for _, chunk := range chunks {
		receipt, err := b.addSingle(ctx, chunk, fn)
		if err != nil {
			return nil, err
		}
		if receipt != nil {
			lastReceipt = receipt
		}
	}
	return lastReceipt, nil
}

// addSingle adds one (possibly compressed/chunked) blob to the batch, flushing
// if necessary.
func (b *BatchBuilder) addSingle(ctx context.Context, data []byte, fn FlushFunc) (*CommitReceipt, error) {
	b.mu.Lock()

	// If this single blob exceeds MaxBytes on its own, flush current batch
	// (if any) first, then submit the oversized blob alone.
	if len(data) > b.config.MaxBytes {
		flushed, err := b.flushLocked(ctx, fn)
		b.mu.Unlock()
		if err != nil {
			return nil, err
		}
		receipt, err := fn(ctx, [][]byte{data})
		if err != nil {
			return nil, err
		}
		_ = flushed
		return receipt, nil
	}

	// Would the new blob push us over the limit?
	if b.totalBytes+len(data) > b.config.MaxBytes && len(b.blobs) > 0 {
		_, err := b.flushLocked(ctx, fn)
		b.mu.Unlock()
		if err != nil {
			return nil, err
		}
		b.mu.Lock()
	}

	b.blobs = append(b.blobs, data)
	b.totalBytes += len(data)
	b.mu.Unlock()
	return nil, nil
}

// Flush submits the current batch immediately, regardless of size.
// Returns nil receipt (and no error) when the batch is empty.
func (b *BatchBuilder) Flush(ctx context.Context, fn FlushFunc) (*CommitReceipt, error) {
	if fn == nil {
		return nil, errors.New("flush function cannot be nil")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.flushLocked(ctx, fn)
}

// Len returns the number of blobs currently queued.
func (b *BatchBuilder) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.blobs)
}

// Bytes returns the total byte size of queued blobs.
func (b *BatchBuilder) Bytes() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.totalBytes
}

// StartAutoFlush launches a background goroutine that flushes the batch on
// every interval tick.  Receipts (and any errors wrapped inside) are sent on
// the returned channel.  The goroutine exits when ctx is cancelled.
//
// The returned channel is closed when the goroutine exits.
func (b *BatchBuilder) StartAutoFlush(ctx context.Context, interval time.Duration) <-chan FlushResult {
	if interval <= 0 {
		interval = DefaultBatchFlushInterval
	}

	ch := make(chan FlushResult, 16)

	fn := func(ctx context.Context, blobs [][]byte) (*CommitReceipt, error) {
		return b.client.CommitRoot(ctx, CommitRootRequest{
			Blobs:    blobs,
			Contract: b.config.Contract,
			Sender:   b.config.Sender,
			Tag:      b.config.Tag,
			Extra:    b.config.Extra,
		})
	}

	go func() {
		defer close(ch)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				// Final flush on shutdown.
				receipt, err := b.Flush(context.Background(), fn)
				if receipt != nil || err != nil {
					select {
					case ch <- FlushResult{Receipt: receipt, Err: err}:
					default:
					}
				}
				return
			case <-ticker.C:
				receipt, err := b.Flush(ctx, fn)
				if receipt != nil || err != nil {
					select {
					case ch <- FlushResult{Receipt: receipt, Err: err}:
					default:
					}
				}
			}
		}
	}()

	return ch
}

// FlushResult carries the outcome of a single auto-flush cycle.
type FlushResult struct {
	Receipt *CommitReceipt
	Err     error
}

// flushLocked performs the flush while b.mu is already held.
// Callers must hold b.mu before calling this.
func (b *BatchBuilder) flushLocked(ctx context.Context, fn FlushFunc) (*CommitReceipt, error) {
	if len(b.blobs) == 0 {
		return nil, nil
	}

	blobs := b.blobs
	b.blobs = nil
	b.totalBytes = 0

	// Release lock during the network call.
	b.mu.Unlock()
	receipt, err := fn(ctx, blobs)
	b.mu.Lock()

	return receipt, err
}
