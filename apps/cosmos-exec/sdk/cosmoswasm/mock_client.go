package cosmoswasm

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

// MockExecutorClient implements ExecutorClient for unit testing without a
// running node. It stores blobs and tx results in memory.
type MockExecutorClient struct {
	mu         sync.Mutex
	blobs      map[string][]byte           // commitment → data
	txResults  map[string]*TxExecutionResult
	txCounter  int
	queryFunc  func(contract string, msg any) (*QuerySmartResponse, error)
	submitFunc func(txBytes []byte) (*SubmitTxResponse, error)
}

// NewMockClient creates a MockExecutorClient with empty state.
func NewMockClient() *MockExecutorClient {
	return &MockExecutorClient{
		blobs:     make(map[string][]byte),
		txResults: make(map[string]*TxExecutionResult),
	}
}

// OnQuery sets a custom handler for QuerySmartRaw/QuerySmart calls.
func (m *MockExecutorClient) OnQuery(fn func(contract string, msg any) (*QuerySmartResponse, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queryFunc = fn
}

// OnSubmit sets a custom handler for SubmitTxBytes/SubmitTxBase64 calls.
func (m *MockExecutorClient) OnSubmit(fn func(txBytes []byte) (*SubmitTxResponse, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.submitFunc = fn
}

// SetTxResult pre-populates a tx result so WaitTxResult returns it.
func (m *MockExecutorClient) SetTxResult(hash string, result *TxExecutionResult) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.txResults[hash] = result
}

func (m *MockExecutorClient) SubmitTxBytes(_ context.Context, txBytes []byte) (*SubmitTxResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.submitFunc != nil {
		return m.submitFunc(txBytes)
	}

	m.txCounter++
	hash := fmt.Sprintf("%x", sha256.Sum256(txBytes))

	// Auto-create a success result.
	m.txResults[hash] = &TxExecutionResult{
		Hash:   hash,
		Height: uint64(m.txCounter),
		Code:   0,
	}

	return &SubmitTxResponse{Hash: hash}, nil
}

func (m *MockExecutorClient) SubmitTxBase64(ctx context.Context, txBase64 string) (*SubmitTxResponse, error) {
	data, err := base64.StdEncoding.DecodeString(txBase64)
	if err != nil {
		return nil, fmt.Errorf("invalid base64: %w", err)
	}
	return m.SubmitTxBytes(ctx, data)
}

func (m *MockExecutorClient) GetTxResult(_ context.Context, txHash string) (*GetTxResultResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	result, ok := m.txResults[txHash]
	if !ok {
		return &GetTxResultResponse{Found: false}, nil
	}
	return &GetTxResultResponse{Found: true, Result: result}, nil
}

func (m *MockExecutorClient) WaitTxResult(ctx context.Context, txHash string, pollInterval time.Duration) (*TxExecutionResult, error) {
	if pollInterval <= 0 {
		pollInterval = 10 * time.Millisecond
	}

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		res, err := m.GetTxResult(ctx, txHash)
		if err != nil {
			return nil, err
		}
		if res.Found && res.Result != nil {
			return res.Result, nil
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *MockExecutorClient) SubmitBlob(_ context.Context, data []byte) (*BlobSubmitResponse, error) {
	if len(data) == 0 {
		return nil, errors.New("blob data cannot be empty")
	}

	commitment := fmt.Sprintf("%x", sha256.Sum256(data))

	m.mu.Lock()
	defer m.mu.Unlock()
	m.blobs[commitment] = append([]byte(nil), data...)

	return &BlobSubmitResponse{
		Commitment: commitment,
		Size:       len(data),
	}, nil
}

func (m *MockExecutorClient) RetrieveBlob(_ context.Context, commitment string) (*BlobRetrieveResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, ok := m.blobs[commitment]
	if !ok {
		return nil, fmt.Errorf("blob not found: %s", commitment)
	}

	return &BlobRetrieveResponse{
		Commitment: commitment,
		DataBase64: base64.StdEncoding.EncodeToString(data),
		Size:       len(data),
	}, nil
}

func (m *MockExecutorClient) RetrieveBlobData(ctx context.Context, commitment string) ([]byte, error) {
	res, err := m.RetrieveBlob(ctx, commitment)
	if err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(res.DataBase64)
}

func (m *MockExecutorClient) SubmitBatch(ctx context.Context, blobs [][]byte) (*BlobBatchResponse, error) {
	if len(blobs) == 0 {
		return nil, errors.New("blobs cannot be empty")
	}

	commitments := make([]string, len(blobs))
	for i, b := range blobs {
		res, err := m.SubmitBlob(ctx, b)
		if err != nil {
			return nil, err
		}
		commitments[i] = res.Commitment
	}

	// Compute Merkle root over commitments.
	root := mockMerkleRoot(commitments)

	return &BlobBatchResponse{
		Root:        root,
		Commitments: commitments,
		Count:       len(commitments),
	}, nil
}

func (m *MockExecutorClient) QuerySmartRaw(_ context.Context, contract string, msg any) (*QuerySmartResponse, error) {
	m.mu.Lock()
	fn := m.queryFunc
	m.mu.Unlock()

	if fn != nil {
		return fn(contract, msg)
	}
	return &QuerySmartResponse{Data: map[string]any{}}, nil
}

func (m *MockExecutorClient) QuerySmart(ctx context.Context, contract string, msg any) (map[string]any, error) {
	res, err := m.QuerySmartRaw(ctx, contract, msg)
	if err != nil {
		return nil, err
	}
	if res.Data != nil {
		if obj, ok := res.Data.(map[string]any); ok {
			return obj, nil
		}
	}
	return map[string]any{}, nil
}

func (m *MockExecutorClient) CommitRoot(ctx context.Context, req CommitRootRequest) (*CommitReceipt, error) {
	if len(req.Blobs) == 0 {
		return nil, errors.New("blobs cannot be empty")
	}

	batchRes, err := m.SubmitBatch(ctx, req.Blobs)
	if err != nil {
		return nil, err
	}

	// Mock the on-chain tx.
	txHash := fmt.Sprintf("mock-tx-%s", batchRes.Root[:16])
	m.mu.Lock()
	m.txResults[txHash] = &TxExecutionResult{Hash: txHash, Height: 1, Code: 0}
	m.mu.Unlock()

	refs := make([]BlobRef, len(batchRes.Commitments))
	for i, c := range batchRes.Commitments {
		refs[i] = BlobRef{Root: batchRes.Root, Commitment: c, Index: i}
	}

	return &CommitReceipt{
		Root:        batchRes.Root,
		Refs:        refs,
		TxHash:      txHash,
		Tag:         req.Tag,
		CommittedAt: time.Now().UTC(),
	}, nil
}

// mockMerkleRoot is a minimal implementation for the mock.
func mockMerkleRoot(commitments []string) string {
	if len(commitments) == 0 {
		return ""
	}
	if len(commitments) == 1 {
		return commitments[0]
	}

	layer := make([][]byte, len(commitments))
	for i, c := range commitments {
		b := make([]byte, 32)
		for j := 0; j < 32 && j*2+1 < len(c); j++ {
			var v byte
			fmt.Sscanf(c[j*2:j*2+2], "%02x", &v)
			b[j] = v
		}
		layer[i] = b
	}

	for len(layer) > 1 {
		var next [][]byte
		for i := 0; i < len(layer); i += 2 {
			left := layer[i]
			right := left
			if i+1 < len(layer) {
				right = layer[i+1]
			}
			combined := append(left, right...)
			h := sha256.Sum256(combined)
			next = append(next, h[:])
		}
		layer = next
	}

	return fmt.Sprintf("%x", layer[0])
}

// Compile-time check.
var _ ExecutorClient = (*MockExecutorClient)(nil)
