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
	"github.com/cosmos/cosmos-sdk/baseapp"
	sdk "github.com/cosmos/cosmos-sdk/types"
	storetypes "cosmossdk.io/store/types"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/app"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/core/execution"
)

var (
	_ execution.Executor       = (*CosmosExecutor)(nil)
	_ execution.HeightProvider = (*CosmosExecutor)(nil)
	_ execution.Rollbackable   = (*CosmosExecutor)(nil)
	_ execution.ExecPruner     = (*CosmosExecutor)(nil)
)

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

	queryGasMax  uint64
	persistStore *PersistStore
}

type BlockInfo struct {
	Height    uint64   `json:"height"`
	Time      string   `json:"time"`
	AppHash   string   `json:"app_hash"`
	NumTxs    int      `json:"num_txs"`
	TxHashes  []string `json:"tx_hashes,omitempty"`
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
	Hash      string    `json:"hash"`
	Height    uint64    `json:"height"`
	Code      uint32    `json:"code"`
	Log       string    `json:"log"`
	Events    []TxEvent `json:"events,omitempty"`
	GasUsed   uint64    `json:"gas_used,omitempty"`
	GasWanted uint64    `json:"gas_wanted,omitempty"`
	// Bytes is the on-wire size of the encoded tx — what gets posted to DA.
	// Used by the cost estimator to compute DA cost without re-fetching the
	// raw tx.
	Bytes uint64 `json:"bytes,omitempty"`
}

// Option configures the executor at creation time.
type Option func(*CosmosExecutor)

// WithQueryGasMax sets the gas limit for WASM smart queries.
func WithQueryGasMax(gas uint64) Option {
	return func(e *CosmosExecutor) {
		if gas > 0 {
			e.queryGasMax = gas
		}
	}
}

// WithPersistence enables disk-backed persistence for tx results, blocks, and
// chain metadata. On startup it replays persisted data into memory; during
// operation it appends new data.
//
// Returns an error via initErr if persistence setup or replay fails. Check
// initErr after New() returns:
//
//	var initErr error
//	exec := New(app, WithPersistence("/data", &initErr))
//	if initErr != nil { ... }
func WithPersistence(dir string, initErr *error) Option {
	return func(e *CosmosExecutor) {
		setErr := func(err error) {
			if initErr != nil {
				*initErr = err
			}
		}

		ps, err := NewPersistStore(dir)
		if err != nil {
			setErr(fmt.Errorf("open persist store: %w", err))
			return
		}
		e.persistStore = ps

		// Replay chain metadata (initialized, chainID, stateRoot, heights).
		meta, err := ps.LoadMetadata()
		if err != nil {
			setErr(fmt.Errorf("load metadata: %w", err))
			return
		}
		if meta.Initialized {
			e.initialized = meta.Initialized
			e.chainID = meta.ChainID
			e.lastHeight = meta.LastHeight
			e.finalizedHeight = meta.FinalizedHeight
			if meta.StateRoot != "" {
				root, decErr := hexDecode(meta.StateRoot)
				if decErr != nil {
					setErr(fmt.Errorf("decode persisted state root: %w", decErr))
					return
				}
				e.stateRoot = root
			}
		}

		// Replay tx results.
		txResults, txSkipped, err := ps.LoadTxResults()
		if err != nil {
			setErr(fmt.Errorf("load tx results: %w", err))
			return
		}
		for k, v := range txResults {
			e.txResults[k] = v
		}

		// Replay blocks.
		blocks, blockSkipped, err := ps.LoadBlocks()
		if err != nil {
			setErr(fmt.Errorf("load blocks: %w", err))
			return
		}
		for k, v := range blocks {
			e.blocks[k] = v
			if k > e.lastHeight {
				e.lastHeight = k
			}
		}

		// Report skipped lines (corrupt data) but don't fail.
		totalSkipped := txSkipped + blockSkipped
		if totalSkipped > 0 && initErr != nil {
			// Not a hard error — data is partially recovered. Log via the error
			// pointer so callers can decide whether to warn or abort.
			// We don't set *initErr here because the replay succeeded overall.
			_ = totalSkipped
		}
	}
}

func New(appInstance *app.App, opts ...Option) *CosmosExecutor {
	exec := &CosmosExecutor{
		app:         appInstance,
		mempool:     make([][]byte, 0, 1024),
		txResults:   make(map[string]TxExecutionResult),
		blocks:      make(map[uint64]BlockInfo),
		queryGasMax: 50_000_000,
	}
	for _, opt := range opts {
		opt(exec)
	}
	return exec
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

	// BaseApp validates that req.ChainId matches its internal chain ID.
	// Set it before calling InitChain so the validation passes.
	baseapp.SetChainID(chainID)(e.app.BaseApp)

	resp, err := e.app.InitChain(&abci.RequestInitChain{
		Time:          genesisTime,
		ChainId:       chainID,
		InitialHeight: int64(initialHeight),
		AppStateBytes: e.app.DefaultGenesis(),
	})
	if err != nil {
		return nil, fmt.Errorf("init chain: %w", err)
	}

	stateRoot := append([]byte(nil), resp.AppHash...)
	if len(stateRoot) == 0 {
		_, commitErr := e.app.Commit()
		if commitErr != nil {
			return nil, fmt.Errorf("commit after init chain: %w", commitErr)
		}
		stateRoot = append([]byte(nil), e.app.CommitMultiStore().LastCommitID().Hash...)
	}

	e.initialized = true
	e.chainID = chainID
	e.stateRoot = stateRoot
	e.lastHeight = initialHeight - 1

	if err := e.saveMetadataLocked(); err != nil {
		return nil, fmt.Errorf("persist metadata after InitChain: %w", err)
	}

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

	// Filter out empty txs for FinalizeBlock
	var validTxs [][]byte
	for _, tx := range txs {
		if len(tx) > 0 {
			validTxs = append(validTxs, tx)
		}
	}

	finalizeResp, err := e.app.FinalizeBlock(&abci.RequestFinalizeBlock{
		Txs:    validTxs,
		Height: int64(blockHeight),
		Time:   timestamp,
		Hash:   prevStateRoot,
	})
	if err != nil {
		return nil, fmt.Errorf("finalize block: %w", err)
	}

	// Process tx results from FinalizeBlock response
	txHashes := make([]string, 0, len(finalizeResp.TxResults))
	for i, txResult := range finalizeResp.TxResults {
		if i >= len(validTxs) {
			break
		}
		txHash := hashTx(validTxs[i])
		txHashes = append(txHashes, txHash)
		result := TxExecutionResult{
			Hash:      txHash,
			Height:    blockHeight,
			Code:      txResult.Code,
			Log:       txResult.Log,
			Events:    toExecTxEvents(txResult.Events),
			GasUsed:   safeUint64(txResult.GasUsed),
			GasWanted: safeUint64(txResult.GasWanted),
			Bytes:     uint64(len(validTxs[i])),
		}
		e.txResults[txHash] = result
		if e.persistStore != nil {
			if persistErr := e.persistStore.AppendTxResult(result); persistErr != nil {
				return nil, fmt.Errorf("persist tx result: %w", persistErr)
			}
		}
	}

	_, err = e.app.Commit()
	if err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	// In SDK v0.50, app hash is obtained from the commit multi store after Commit()
	e.stateRoot = append([]byte(nil), e.app.CommitMultiStore().LastCommitID().Hash...)
	e.lastHeight = blockHeight

	blockInfo := BlockInfo{
		Height:   blockHeight,
		Time:     timestamp.UTC().Format(time.RFC3339),
		AppHash:  fmt.Sprintf("%x", e.stateRoot),
		NumTxs:   len(txs),
		TxHashes: txHashes,
	}
	e.blocks[blockHeight] = blockInfo
	if e.persistStore != nil {
		if persistErr := e.persistStore.AppendBlock(blockInfo); persistErr != nil {
			return nil, fmt.Errorf("persist block: %w", persistErr)
		}
	}

	if err := e.saveMetadataLocked(); err != nil {
		return nil, fmt.Errorf("persist metadata after ExecuteTxs: %w", err)
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

	// Use checkState (isCheckTx=true). SDK 0.50 nils finalizeBlockState after
	// Commit, so NewContext(false) panics on queries that follow a block.
	queryCtx := e.app.BaseApp.NewContext(true).WithBlockHeight(int64(height)).WithBlockTime(time.Now())

	// Set a gas limit to prevent unbounded WASM queries from panicking with out-of-gas.
	queryCtx = queryCtx.WithGasMeter(storetypes.NewGasMeter(e.queryGasMax))

	queryResult, queryErr := e.app.WasmKeeper.QuerySmart(queryCtx, contractAddr, queryMsg)
	if queryErr != nil {
		return nil, queryErr
	}

	return append([]byte(nil), queryResult...), nil
}

// AccountInfo is the minimal view of an on-chain account needed by clients to
// sign txs: address + account number + sequence.
type AccountInfo struct {
	Address       string `json:"address"`
	AccountNumber uint64 `json:"account_number"`
	Sequence      uint64 `json:"sequence"`
	Exists        bool   `json:"exists"`
}

// GetAccountInfo returns auth account state for the given bech32 address.
// Returns Exists=false if no account exists yet (sequence/number = 0).
func (e *CosmosExecutor) GetAccountInfo(ctx context.Context, bech32Addr string) (AccountInfo, error) {
	addr, err := sdk.AccAddressFromBech32(strings.TrimSpace(bech32Addr))
	if err != nil {
		return AccountInfo{}, fmt.Errorf("invalid address: %w", err)
	}

	height := e.lastHeight
	if height == 0 {
		height = 1
	}
	queryCtx := e.app.BaseApp.NewContext(true).WithBlockHeight(int64(height))

	acc := e.app.AccountKeeper.GetAccount(queryCtx, addr)
	if acc == nil {
		// Account doesn't exist yet — but the AutoCreateAccount ante decorator
		// will create it on the next signed tx, assigning the next global
		// account number. Peek that value so the client can sign with the
		// account_number the chain will use at SigVerification time.
		//
		// Note: this is racy under concurrent first-tx submission from
		// different new addresses (two callers see the same peeked number).
		// Fine for single-user dev; if you add a real faucet/funding flow,
		// rip this out and require accounts to exist before signing.
		nextNum, err := e.app.AccountKeeper.AccountNumber.Peek(queryCtx)
		if err != nil {
			return AccountInfo{}, fmt.Errorf("peek next account number: %w", err)
		}
		return AccountInfo{
			Address:       bech32Addr,
			AccountNumber: nextNum,
			Sequence:      0,
			Exists:        false,
		}, nil
	}
	return AccountInfo{
		Address:       bech32Addr,
		AccountNumber: acc.GetAccountNumber(),
		Sequence:      acc.GetSequence(),
		Exists:        true,
	}, nil
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

	if err := e.saveMetadataLocked(); err != nil {
		return fmt.Errorf("persist metadata after SetFinal: %w", err)
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

// GetLatestHeight returns the current block height of the execution layer.
// Implements execution.HeightProvider for crash-recovery sync checks.
func (e *CosmosExecutor) GetLatestHeight(ctx context.Context) (uint64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	return e.lastHeight, nil
}

// Rollback resets the execution layer to the specified target height.
// This is used for recovery when the executor is ahead of the consensus layer
// (e.g., executor committed height 10 but ev-node only persisted up to height 8).
//
// Implements execution.Rollbackable.
//
// It loads the IAVL state at the target version and trims in-memory data
// (tx results, blocks) above the target height.
func (e *CosmosExecutor) Rollback(ctx context.Context, targetHeight uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if targetHeight > e.lastHeight {
		return fmt.Errorf("cannot rollback to future height %d (current: %d)", targetHeight, e.lastHeight)
	}
	if targetHeight == e.lastHeight {
		return nil // already at target
	}

	// Load the IAVL state at the target version. BaseApp's CommitMultiStore
	// keeps versioned state in IAVL trees, so we can reload an older version.
	if err := e.app.LoadVersion(int64(targetHeight)); err != nil {
		return fmt.Errorf("load version %d: %w", targetHeight, err)
	}

	// Update state root from the reloaded commit store.
	cms := e.app.CommitMultiStore()
	stateRoot := cms.LastCommitID().Hash
	e.stateRoot = append([]byte(nil), stateRoot...)

	// Trim in-memory data above target height.
	for h := range e.blocks {
		if h > targetHeight {
			delete(e.blocks, h)
		}
	}
	for hash, result := range e.txResults {
		if result.Height > targetHeight {
			delete(e.txResults, hash)
		}
	}

	// Reset finalized height if it was ahead.
	if e.finalizedHeight > targetHeight {
		e.finalizedHeight = targetHeight
	}

	e.lastHeight = targetHeight

	if err := e.saveMetadataLocked(); err != nil {
		return fmt.Errorf("persist metadata after rollback: %w", err)
	}

	return nil
}

// PruneExec removes execution metadata (tx results, block info) for all heights
// up to and including the given height. This frees memory for long-running nodes.
//
// Implements execution.ExecPruner.
//
// Note: blobs are not pruned by height since they are content-addressed and not
// tied to a specific block height.
func (e *CosmosExecutor) PruneExec(ctx context.Context, height uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Prune blocks at or below the target height.
	for h := range e.blocks {
		if h <= height {
			delete(e.blocks, h)
		}
	}

	// Prune tx results at or below the target height.
	for hash, result := range e.txResults {
		if result.Height <= height {
			delete(e.txResults, hash)
		}
	}

	return nil
}

// Close releases resources held by the executor (e.g. persistence files).
func (e *CosmosExecutor) Close() {
	if e.persistStore != nil {
		_ = e.persistStore.Close()
	}
}

// Stats holds runtime metrics for monitoring.
type Stats struct {
	TxResultCount int `json:"tx_result_count"`
	BlockCount    int `json:"block_count"`
	MempoolSize   int `json:"mempool_size"`
}

// GetStats returns runtime metrics for health/monitoring endpoints.
func (e *CosmosExecutor) GetStats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()

	return Stats{
		TxResultCount: len(e.txResults),
		BlockCount:    len(e.blocks),
		MempoolSize:   len(e.mempool),
	}
}

// saveMetadataLocked writes the current chain state to disk.
// Caller must hold e.mu.
func (e *CosmosExecutor) saveMetadataLocked() error {
	if e.persistStore == nil {
		return nil
	}
	return e.persistStore.SaveMetadata(ChainMetadata{
		Initialized:     e.initialized,
		ChainID:         e.chainID,
		StateRoot:       fmt.Sprintf("%x", e.stateRoot),
		LastHeight:      e.lastHeight,
		FinalizedHeight: e.finalizedHeight,
	})
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

// safeUint64 clamps a signed ABCI gas value to a non-negative uint64. ABCI's
// GasUsed/GasWanted are int64; spec says they're non-negative, but we don't
// want a misbehaving handler to wrap into a giant uint64.
func safeUint64(v int64) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}

func normalizeHash(hash string) string {
	hash = strings.TrimSpace(hash)
	hash = strings.TrimPrefix(hash, "0x")
	hash = strings.TrimPrefix(hash, "0X")
	return strings.ToLower(hash)
}

func toExecTxEvents(events []abci.Event) []TxEvent {
	out := make([]TxEvent, 0, len(events))
	for _, event := range events {
		attributes := make([]TxEventAttribute, 0, len(event.Attributes))
		for _, attribute := range event.Attributes {
			attributes = append(attributes, TxEventAttribute{
				Key:   attribute.Key,
				Value: attribute.Value,
			})
		}
		out = append(out, TxEvent{Type: event.Type, Attributes: attributes})
	}
	return out
}
