package cosmoswasm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DABridge is the high-level entry point for app-chain DA operations.
// It combines a DAClient (Celestia access) with an ExecutorClient (on-chain
// recording) to provide the complete blob-first data pattern:
//
//	App data → DA layer (namespace-isolated) → commitment on-chain (minimal gas)
//
// Typical usage:
//
//	ns := cosmoswasm.NamespaceFromString("my-game")
//	bridge := cosmoswasm.NewDABridge(daClient, execClient, ns)
//
//	// Submit game events to Celestia under your namespace.
//	result, _ := bridge.Submit(ctx, [][]byte{event1, event2})
//
//	// Record the DA commitment on your WASM contract.
//	receipt, _ := bridge.SubmitAndCommit(ctx, SubmitAndCommitRequest{
//	    Blobs:    [][]byte{event1, event2},
//	    Contract: "wasm1...",
//	    Tag:      "game-events",
//	})
//
//	// Watch for incoming blobs in your namespace.
//	ch, _ := bridge.Watch(ctx)
//	for event := range ch {
//	    processBlobs(event.Blobs)
//	}
type DABridge struct {
	da        DAClient
	exec      ExecutorClient
	namespace *Namespace
}

// NewDABridge creates a DABridge for the given namespace.
// exec can be nil if you only need DA operations without on-chain recording.
func NewDABridge(da DAClient, exec ExecutorClient, namespace *Namespace) *DABridge {
	return &DABridge{
		da:        da,
		exec:      exec,
		namespace: namespace,
	}
}

// Namespace returns the bridge's namespace.
func (b *DABridge) Namespace() *Namespace {
	return b.namespace
}

// Submit sends blobs to the DA layer under this bridge's namespace.
func (b *DABridge) Submit(ctx context.Context, blobs [][]byte, opts *DASubmitOptions) (*DASubmitResult, error) {
	if b.da == nil {
		return nil, errors.New("DAClient is not configured")
	}
	if len(blobs) == 0 {
		return nil, errors.New("blobs cannot be empty")
	}

	return b.da.SubmitBlobs(ctx, b.namespace, blobs, opts)
}

// GetBlobs retrieves all blobs at a given DA height for this namespace.
func (b *DABridge) GetBlobs(ctx context.Context, height uint64) ([]*DABlob, error) {
	if b.da == nil {
		return nil, errors.New("DAClient is not configured")
	}
	return b.da.GetBlobs(ctx, b.namespace, height)
}

// Watch subscribes to new blobs in this bridge's namespace.
// The returned channel emits events whenever blobs are finalized on the DA layer.
// The channel is closed when ctx is cancelled.
func (b *DABridge) Watch(ctx context.Context) (<-chan *DABlobEvent, error) {
	if b.da == nil {
		return nil, errors.New("DAClient is not configured")
	}
	return b.da.Subscribe(ctx, b.namespace)
}

// SubmitAndCommitRequest holds parameters for SubmitAndCommit.
type SubmitAndCommitRequest struct {
	// Blobs are the raw data payloads to submit to the DA layer.
	Blobs [][]byte
	// Contract is the bech32 WASM contract address that records the DA commitment.
	Contract string
	// Sender is optional; uses DefaultSender when empty.
	Sender string
	// Tag is an optional label stored alongside the commitment on-chain.
	Tag string
	// DASubmitOptions are optional gas/fee overrides for the DA submission.
	DASubmitOptions *DASubmitOptions
}

// SubmitAndCommitReceipt is returned by SubmitAndCommit.
type SubmitAndCommitReceipt struct {
	// DAResult is the DA layer submission result.
	DAResult *DASubmitResult
	// OnChainReceipt is the on-chain CommitReceipt (if executor is configured).
	OnChainReceipt *CommitReceipt
}

// SubmitAndCommit submits blobs to the DA layer AND records a commitment on-chain.
// This is the canonical pattern for data-heavy app-chains:
//
//  1. Blobs → DA layer (namespace-isolated, off-chain)
//  2. Commitment → WASM contract (on-chain, minimal gas)
//
// Both the DA proof and on-chain tx hash are returned for full auditability.
func (b *DABridge) SubmitAndCommit(ctx context.Context, req SubmitAndCommitRequest) (*SubmitAndCommitReceipt, error) {
	if b.da == nil {
		return nil, errors.New("DAClient is not configured")
	}
	if b.exec == nil {
		return nil, errors.New("ExecutorClient is not configured — needed for on-chain commit")
	}
	if len(req.Blobs) == 0 {
		return nil, errors.New("blobs cannot be empty")
	}

	// 1. Submit to DA layer.
	daResult, err := b.da.SubmitBlobs(ctx, b.namespace, req.Blobs, req.DASubmitOptions)
	if err != nil {
		return nil, fmt.Errorf("DA submit failed: %w", err)
	}

	// 2. Record commitment on-chain via executor.
	receipt, err := b.exec.CommitRoot(ctx, CommitRootRequest{
		Blobs:    req.Blobs,
		Contract: req.Contract,
		Sender:   req.Sender,
		Tag:      req.Tag,
		Extra: map[string]any{
			"da_height":    daResult.Height,
			"da_namespace": b.namespace.Hex(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("on-chain commit failed (DA submit succeeded at height %d): %w", daResult.Height, err)
	}

	return &SubmitAndCommitReceipt{
		DAResult:       daResult,
		OnChainReceipt: receipt,
	}, nil
}

// DAHeight returns the latest DA layer height.
func (b *DABridge) DAHeight(ctx context.Context) (uint64, error) {
	if b.da == nil {
		return 0, errors.New("DAClient is not configured")
	}
	return b.da.GetHeight(ctx)
}

// PollBlobs polls the DA layer for new blobs in this namespace starting from
// startHeight. It calls handler for each height that has blobs. Polling stops
// when ctx is cancelled.
//
// This is an alternative to Watch for environments where WebSocket subscriptions
// are not available (e.g. behind load balancers).
func (b *DABridge) PollBlobs(ctx context.Context, startHeight uint64, interval time.Duration, handler func(event *DABlobEvent) error) error {
	if b.da == nil {
		return errors.New("DAClient is not configured")
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}

	currentHeight := startHeight
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			latestHeight, err := b.da.GetHeight(ctx)
			if err != nil {
				continue // Retry on next tick.
			}

			for currentHeight <= latestHeight {
				blobs, err := b.da.GetBlobs(ctx, b.namespace, currentHeight)
				if err != nil {
					break // Retry on next tick.
				}

				if len(blobs) > 0 {
					event := &DABlobEvent{
						Height:    currentHeight,
						Blobs:     blobs,
						Timestamp: time.Now().UTC(),
					}
					if err := handler(event); err != nil {
						return fmt.Errorf("handler error at height %d: %w", currentHeight, err)
					}
				}
				currentHeight++
			}
		}
	}
}
