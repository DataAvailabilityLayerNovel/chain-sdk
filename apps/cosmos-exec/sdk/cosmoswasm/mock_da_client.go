package cosmoswasm

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// MockDAClient implements DAClient for unit testing without a running DA node.
// It stores blobs in memory, organized by namespace and height.
type MockDAClient struct {
	mu          sync.Mutex
	height      uint64
	blobs       map[string][]*DABlob // key: "namespace:height"
	subscribers []mockSubscriber
}

type mockSubscriber struct {
	namespace *Namespace
	ch        chan *DABlobEvent
	ctx       context.Context
}

// NewMockDAClient creates a MockDAClient with empty state at height 1.
func NewMockDAClient() *MockDAClient {
	return &MockDAClient{
		height: 1,
		blobs:  make(map[string][]*DABlob),
	}
}

func (m *MockDAClient) SubmitBlobs(ctx context.Context, namespace *Namespace, blobs [][]byte, opts *DASubmitOptions) (*DASubmitResult, error) {
	if namespace == nil {
		return nil, fmt.Errorf("namespace is required")
	}
	if len(blobs) == 0 {
		return nil, fmt.Errorf("blobs cannot be empty")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.height++
	height := m.height

	key := blobKey(namespace, height)
	totalSize := 0
	daBlobs := make([]*DABlob, len(blobs))

	for i, data := range blobs {
		commitment := sha256.Sum256(data)
		daBlobs[i] = &DABlob{
			Namespace:  namespace,
			Data:       append([]byte(nil), data...),
			Commitment: commitment[:],
			Height:     height,
			Index:      i,
		}
		totalSize += len(data)
	}

	m.blobs[key] = daBlobs

	// Notify subscribers.
	event := &DABlobEvent{
		Height:    height,
		Blobs:     daBlobs,
		Timestamp: time.Now().UTC(),
	}
	for _, sub := range m.subscribers {
		if sub.namespace.Equal(namespace) {
			select {
			case sub.ch <- event:
			case <-sub.ctx.Done():
			default:
				// Drop if subscriber is slow.
			}
		}
	}

	return &DASubmitResult{
		Height:    height,
		Namespace: namespace,
		BlobCount: len(blobs),
		TotalSize: totalSize,
	}, nil
}

func (m *MockDAClient) GetBlobs(ctx context.Context, namespace *Namespace, height uint64) ([]*DABlob, error) {
	if namespace == nil {
		return nil, fmt.Errorf("namespace is required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	key := blobKey(namespace, height)
	blobs, ok := m.blobs[key]
	if !ok {
		return nil, nil // No blobs at this height for this namespace.
	}
	return blobs, nil
}

func (m *MockDAClient) GetBlobByCommitment(ctx context.Context, namespace *Namespace, height uint64, commitment []byte) (*DABlob, error) {
	blobs, err := m.GetBlobs(ctx, namespace, height)
	if err != nil {
		return nil, err
	}
	for _, b := range blobs {
		if equalBytes(b.Commitment, commitment) {
			return b, nil
		}
	}
	return nil, fmt.Errorf("blob not found at height %d with given commitment", height)
}

func (m *MockDAClient) Subscribe(ctx context.Context, namespace *Namespace) (<-chan *DABlobEvent, error) {
	if namespace == nil {
		return nil, fmt.Errorf("namespace is required")
	}

	ch := make(chan *DABlobEvent, 64)

	m.mu.Lock()
	m.subscribers = append(m.subscribers, mockSubscriber{
		namespace: namespace,
		ch:        ch,
		ctx:       ctx,
	})
	m.mu.Unlock()

	// Close the channel when context is done.
	go func() {
		<-ctx.Done()
		m.mu.Lock()
		defer m.mu.Unlock()

		// Remove subscriber and close channel.
		for i, sub := range m.subscribers {
			if sub.ch == ch {
				m.subscribers = append(m.subscribers[:i], m.subscribers[i+1:]...)
				break
			}
		}
		close(ch)
	}()

	return ch, nil
}

func (m *MockDAClient) GetHeight(ctx context.Context) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.height, nil
}

// SetHeight allows tests to manually set the DA height.
func (m *MockDAClient) SetHeight(h uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.height = h
}

// InjectBlobs allows tests to inject blobs at a specific height without going
// through SubmitBlobs. Useful for simulating blobs from other app-chains.
func (m *MockDAClient) InjectBlobs(namespace *Namespace, height uint64, blobs []*DABlob) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := blobKey(namespace, height)
	m.blobs[key] = blobs

	// Notify subscribers.
	event := &DABlobEvent{
		Height:    height,
		Blobs:     blobs,
		Timestamp: time.Now().UTC(),
	}
	for _, sub := range m.subscribers {
		if sub.namespace.Equal(namespace) {
			select {
			case sub.ch <- event:
			case <-sub.ctx.Done():
			default:
			}
		}
	}
}

func blobKey(ns *Namespace, height uint64) string {
	return fmt.Sprintf("%s:%d", ns.Hex(), height)
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Compile-time check.
var _ DAClient = (*MockDAClient)(nil)
