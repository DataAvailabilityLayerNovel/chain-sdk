package executor

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	tmproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/app"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/core/execution"
)

var _ execution.Executor = (*CosmosExecutor)(nil)

type CosmosExecutor struct {
	app *app.App

	mu sync.Mutex

	initialized     bool
	chainID         string
	stateRoot       []byte
	lastHeight      uint64
	finalizedHeight uint64

	mempool   [][]byte
	txResults map[string]TxExecutionResult
	blocks    map[uint64]BlockInfo

	// blobStore holds large data blobs off WASM contract state.
	// Callers store the returned commitment (32-byte SHA-256 hex) on-chain
	// via a WASM message, keeping gas costs minimal.
	blobStore *BlobStore
}

type BlockInfo struct {
	Height  uint64 `json:"height"`
	Time    string `json:"time"`
	AppHash string `json:"app_hash"`
	NumTxs  int    `json:"num_txs"`
}

type StatusInfo struct {
	Initialized     bool   `json:"initialized"`
	ChainID         string `json:"chain_id"`
	LatestHeight    uint64 `json:"latest_height"`
	FinalizedHeight uint64 `json:"finalized_height"`
	Healthy         bool   `json:"healthy"`
	Synced          bool   `json:"synced"`
}

type TxEventAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type TxEvent struct {
	Type       string             `json:"type"`
	Attributes []TxEventAttribute `json:"attributes"`
}

type TxExecutionResult struct {
	Hash   string    `json:"hash"`
	Height uint64    `json:"height"`
	Code   uint32    `json:"code"`
	Log    string    `json:"log"`
	Events []TxEvent `json:"events,omitempty"`
}

func New(appInstance *app.App) *CosmosExecutor {
	return &CosmosExecutor{
		app:       appInstance,
		mempool:   make([][]byte, 0, 1024),
		txResults: make(map[string]TxExecutionResult),
		blocks:    make(map[uint64]BlockInfo),
		blobStore: NewBlobStore(),
	}
}

// StoreBlob stores arbitrary data in the executor's content-addressed blob
// store and returns a hex-encoded SHA-256 commitment.  The caller should
// record this commitment in their WASM contract (cheap, 32 bytes on-chain)
// rather than embedding the raw data in a contract message.
func (e *CosmosExecutor) StoreBlob(ctx context.Context, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return e.blobStore.Put(data)
}

// RetrieveBlob fetches a blob by its SHA-256 commitment.
// Returns an error when the commitment is not found in the local store.
func (e *CosmosExecutor) RetrieveBlob(ctx context.Context, commitment string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, ok := e.blobStore.Get(commitment)
	if !ok {
		return nil, fmt.Errorf("blob not found: %s", commitment)
	}
	return data, nil
}

// StoreBatch stores multiple blobs atomically, computes a binary Merkle root
// over their SHA-256 commitments, and returns (root, commitments).
// Commit the root on-chain via BuildBatchRootTx; individual commitments allow
// per-blob retrieval and Merkle inclusion proofs.
func (e *CosmosExecutor) StoreBatch(ctx context.Context, blobs [][]byte) (root string, commitments []string, err error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	return e.blobStore.PutBatch(blobs)
}

func (e *CosmosExecutor) InitChain(ctx context.Context, genesisTime time.Time, initialHeight uint64, chainID string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if initialHeight == 0 {
		return nil, errors.New("initial height must be > 0")
	}
	if chainID == "" {
		return nil, errors.New("chain id is required")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.initialized {
		if e.chainID != chainID {
			return nil, fmt.Errorf("executor already initialized with chain id %q", e.chainID)
		}
		return append([]byte(nil), e.stateRoot...), nil
	}

	resp := e.app.InitChain(abci.RequestInitChain{
		Time:          genesisTime,
		ChainId:       "",
		InitialHeight: int64(initialHeight),
		AppStateBytes: e.app.DefaultGenesis(),
	})

	stateRoot := append([]byte(nil), resp.AppHash...)
	if len(stateRoot) == 0 {
		commitResp := e.app.Commit()
		stateRoot = append([]byte(nil), commitResp.Data...)
	}

	e.initialized = true
	e.chainID = chainID
	e.stateRoot = stateRoot
	e.lastHeight = initialHeight - 1

	return append([]byte(nil), e.stateRoot...), nil
}

func (e *CosmosExecutor) GetTxs(ctx context.Context) ([][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.mempool) == 0 {
		return [][]byte{}, nil
	}

	txs := make([][]byte, len(e.mempool))
	copy(txs, e.mempool)
	e.mempool = e.mempool[:0]

	return txs, nil
}

func (e *CosmosExecutor) ExecuteTxs(ctx context.Context, txs [][]byte, blockHeight uint64, timestamp time.Time, prevStateRoot []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if blockHeight == 0 {
		return nil, errors.New("block height must be > 0")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		return nil, errors.New("executor not initialized")
	}
	if !bytesEqual(e.stateRoot, prevStateRoot) {
		return nil, fmt.Errorf("prev state root mismatch: expected %X got %X", e.stateRoot, prevStateRoot)
	}
	if blockHeight != e.lastHeight+1 {
		return nil, fmt.Errorf("unexpected block height %d (expected %d)", blockHeight, e.lastHeight+1)
	}

	e.app.BeginBlock(abci.RequestBeginBlock{
		Header: tmproto.Header{
			Height:  int64(blockHeight),
			Time:    timestamp,
			ChainID: "",
		},
	})

	for _, tx := range txs {
		if len(tx) == 0 {
			continue
		}
		txHash := hashTx(tx)
		deliverResp := e.app.DeliverTx(abci.RequestDeliverTx{Tx: tx})

		e.txResults[txHash] = TxExecutionResult{
			Hash:   txHash,
			Height: blockHeight,
			Code:   deliverResp.Code,
			Log:    deliverResp.Log,
			Events: toEvents(deliverResp.Events),
		}
	}

	e.app.EndBlock(abci.RequestEndBlock{Height: int64(blockHeight)})
	commitResp := e.app.Commit()

	e.stateRoot = append([]byte(nil), commitResp.Data...)
	e.lastHeight = blockHeight

	e.blocks[blockHeight] = BlockInfo{
		Height:  blockHeight,
		Time:    timestamp.UTC().Format(time.RFC3339),
		AppHash: fmt.Sprintf("%x", commitResp.Data),
		NumTxs:  len(txs),
	}

	return append([]byte(nil), e.stateRoot...), nil
}

func (e *CosmosExecutor) InjectTx(ctx context.Context, tx []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(tx) == 0 {
		return "", errors.New("tx cannot be empty")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	txCopy := append([]byte(nil), tx...)
	e.mempool = append(e.mempool, txCopy)

	return hashTx(txCopy), nil
}

func (e *CosmosExecutor) GetTxResult(ctx context.Context, hash string) (TxExecutionResult, bool, error) {
	if err := ctx.Err(); err != nil {
		return TxExecutionResult{}, false, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	result, ok := e.txResults[normalizeHash(hash)]
	if !ok {
		return TxExecutionResult{}, false, nil
	}

	return result, true, nil
}

func (e *CosmosExecutor) QuerySmart(ctx context.Context, contract string, queryMsg []byte) (result []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if contract == "" {
		return nil, errors.New("contract address is required")
	}
	if len(queryMsg) == 0 {
		return nil, errors.New("query msg cannot be empty")
	}

	contractAddr, err := sdk.AccAddressFromBech32(contract)
	if err != nil {
		return nil, fmt.Errorf("invalid contract address: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Recover from panics in WASM execution (e.g. out-of-gas, store access).
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("wasm query panicked: %v", r)
		}
	}()

	height := e.lastHeight

	if height == 0 {
		height = 1
	}

	queryCtx := e.app.BaseApp.NewContext(false, tmproto.Header{
		Height: int64(height),
		Time:   time.Now(),
	})

	// Set a gas limit to prevent unbounded WASM queries from panicking with out-of-gas.
	queryCtx = queryCtx.WithGasMeter(sdk.NewGasMeter(50_000_000))

	queryResult, queryErr := e.app.WasmKeeper.QuerySmart(queryCtx, contractAddr, queryMsg)
	if queryErr != nil {
		return nil, queryErr
	}

	return append([]byte(nil), queryResult...), nil
}

func (e *CosmosExecutor) SetFinal(ctx context.Context, blockHeight uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if blockHeight > e.lastHeight {
		return fmt.Errorf("cannot finalize future block %d, last executed %d", blockHeight, e.lastHeight)
	}
	if blockHeight > e.finalizedHeight {
		e.finalizedHeight = blockHeight
	}

	return nil
}

func (e *CosmosExecutor) GetExecutionInfo(ctx context.Context) (execution.ExecutionInfo, error) {
	if err := ctx.Err(); err != nil {
		return execution.ExecutionInfo{}, err
	}

	return execution.ExecutionInfo{MaxGas: 0}, nil
}

func (e *CosmosExecutor) FilterTxs(ctx context.Context, txs [][]byte, maxBytes, _ uint64, _ bool) ([]execution.FilterStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	statuses := make([]execution.FilterStatus, len(txs))
	var cumulativeBytes uint64

	for i, tx := range txs {
		txLen := uint64(len(tx))
		if txLen == 0 {
			statuses[i] = execution.FilterRemove
			continue
		}

		if maxBytes > 0 && cumulativeBytes+txLen > maxBytes {
			statuses[i] = execution.FilterPostpone
			continue
		}

		statuses[i] = execution.FilterOK
		cumulativeBytes += txLen
	}

	return statuses, nil
}

// GetLatestBlock returns the most recently executed block info.
func (e *CosmosExecutor) GetLatestBlock(ctx context.Context) (BlockInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return BlockInfo{}, false, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.lastHeight == 0 {
		return BlockInfo{}, false, nil
	}

	info, ok := e.blocks[e.lastHeight]
	return info, ok, nil
}

// GetBlock returns block info at a specific height.
func (e *CosmosExecutor) GetBlock(ctx context.Context, height uint64) (BlockInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return BlockInfo{}, false, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	info, ok := e.blocks[height]
	return info, ok, nil
}

// GetStatus returns the current executor status.
func (e *CosmosExecutor) GetStatus(ctx context.Context) (StatusInfo, error) {
	if err := ctx.Err(); err != nil {
		return StatusInfo{}, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	return StatusInfo{
		Initialized:     e.initialized,
		ChainID:         e.chainID,
		LatestHeight:    e.lastHeight,
		FinalizedHeight: e.finalizedHeight,
		Healthy:         true,
		Synced:          e.finalizedHeight >= e.lastHeight || e.lastHeight == 0,
	}, nil
}

// GetPendingTxCount returns the number of transactions in the mempool.
func (e *CosmosExecutor) GetPendingTxCount(ctx context.Context) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	return len(e.mempool), nil
}

func bytesEqual(a, b []byte) bool {
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

func hashTx(tx []byte) string {
	h := sha256.Sum256(tx)
	return fmt.Sprintf("%x", h[:])
}

func normalizeHash(hash string) string {
	hash = strings.TrimSpace(hash)
	hash = strings.TrimPrefix(hash, "0x")
	hash = strings.TrimPrefix(hash, "0X")
	return strings.ToLower(hash)
}

func toEvents(events []abci.Event) []TxEvent {
	out := make([]TxEvent, 0, len(events))
	for _, event := range events {
		attributes := make([]TxEventAttribute, 0, len(event.Attributes))
		for _, attribute := range event.Attributes {
			attributes = append(attributes, TxEventAttribute{
				Key:   string(attribute.Key),
				Value: string(attribute.Value),
			})
		}
		out = append(out, TxEvent{Type: event.Type, Attributes: attributes})
	}
	return out
}
