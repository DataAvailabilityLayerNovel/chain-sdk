// package executor: triển khai "executor" — thành phần nhận tx từ ev-node,
// chạy chúng qua app Cosmos SDK (FinalizeBlock/Commit) và trả về state root.
// File này là LÕI: nó cài đặt các interface mà ev-node yêu cầu.
package executor

import (
	"context"       // context.Context: truyền tín hiệu huỷ/timeout xuyên các hàm.
	"crypto/sha256" // băm SHA-256 — dùng làm hash định danh tx.
	"errors"        // tạo lỗi đơn giản (errors.New).
	"fmt"           // định dạng chuỗi & bọc lỗi.
	"strings"       // xử lý chuỗi (TrimSpace, ToLower...).
	"sync"          // sync.Mutex — khoá bảo vệ state dùng chung.
	"time"          // thời gian (timestamp block).

	abci "github.com/cometbft/cometbft/abci/types"  // type ABCI (request/response).
	"github.com/cosmos/cosmos-sdk/baseapp"          // BaseApp helper (SetChainID...).
	sdk "github.com/cosmos/cosmos-sdk/types"        // type lõi SDK (AccAddress, Coins).
	storetypes "cosmossdk.io/store/types"           // GasMeter cho query.

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/app"        // app blockchain.
	"github.com/DataAvailabilityLayerNovel/chain-sdk/core/execution"              // interface executor của ev-node.
)

// var (...) với các dòng `_ Interface = (*T)(nil)`:
// đây là "compile-time assertion" — ép trình biên dịch kiểm tra rằng
// *CosmosExecutor có cài đặt ĐỦ các interface này. Nếu thiếu method → lỗi build.
var (
	_ execution.Executor       = (*CosmosExecutor)(nil) // thực thi tx.
	_ execution.HeightProvider = (*CosmosExecutor)(nil) // cung cấp chiều cao block.
	_ execution.Rollbackable   = (*CosmosExecutor)(nil) // hỗ trợ rollback.
	_ execution.ExecPruner     = (*CosmosExecutor)(nil) // hỗ trợ prune (dọn bộ nhớ).
)

// CosmosExecutor: struct giữ toàn bộ trạng thái runtime của executor.
type CosmosExecutor struct {
	app *app.App // app Cosmos SDK thật, nơi thực sự chạy tx.

	mu sync.Mutex // khoá: mọi truy cập state bên dưới phải Lock trước.

	initialized     bool   // chain đã InitChain chưa.
	chainID         string // định danh chain.
	stateRoot       []byte // app hash hiện tại (gốc state).
	lastHeight      uint64 // block cao nhất đã thực thi.
	finalizedHeight uint64 // block cao nhất đã được finalize.

	// genesis, when non-nil, overrides app.DefaultGenesis() at InitChain.
	// Used to fund a treasury at chain init (see WithGenesis).
	// VI: nếu khác nil sẽ thay genesis mặc định lúc InitChain (vd: cấp tiền
	// cho ví treasury ngay khi khởi tạo chain).
	genesis []byte

	mempool   [][]byte                     // hàng đợi tx chờ đưa vào block.
	txResults map[string]TxExecutionResult // tra cứu kết quả tx theo hash.
	blocks    map[uint64]BlockInfo         // tra cứu block theo chiều cao.

	queryGasMax  uint64        // giới hạn gas cho truy vấn WASM (chống loop vô hạn).
	persistStore *PersistStore // nơi lưu state ra đĩa (có thể nil = không lưu).
}

// BlockInfo: thông tin tóm tắt 1 block (trả về cho client/explorer).
type BlockInfo struct {
	Height   uint64   `json:"height"`               // chiều cao block.
	Time     string   `json:"time"`                 // thời điểm (chuỗi RFC3339).
	AppHash  string   `json:"app_hash"`             // state root sau block (hex).
	NumTxs   int      `json:"num_txs"`              // số tx trong block.
	TxHashes []string `json:"tx_hashes,omitempty"`  // omitempty: bỏ khỏi JSON nếu rỗng.
}

// StatusInfo: trạng thái tổng quan của executor (cho endpoint /status).
type StatusInfo struct {
	Initialized     bool   `json:"initialized"`
	ChainID         string `json:"chain_id"`
	LatestHeight    uint64 `json:"latest_height"`
	FinalizedHeight uint64 `json:"finalized_height"`
	Healthy         bool   `json:"healthy"`
	Synced          bool   `json:"synced"`
}

// TxEventAttribute: 1 cặp key-value trong event của tx.
type TxEventAttribute struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// TxEvent: 1 sự kiện phát ra khi thực thi tx (vd "transfer", "wasm").
type TxEvent struct {
	Type       string             `json:"type"`
	Attributes []TxEventAttribute `json:"attributes"`
}

// TxExecutionResult: kết quả thực thi 1 tx.
type TxExecutionResult struct {
	Hash      string    `json:"hash"`                  // hash định danh tx.
	Height    uint64    `json:"height"`                // block chứa tx.
	Code      uint32    `json:"code"`                  // 0 = thành công, khác 0 = lỗi.
	Log       string    `json:"log"`                   // mô tả lỗi/log.
	Events    []TxEvent `json:"events,omitempty"`
	GasUsed   uint64    `json:"gas_used,omitempty"`
	GasWanted uint64    `json:"gas_wanted,omitempty"`
	// Bytes is the on-wire size of the encoded tx — what gets posted to DA.
	// Used by the cost estimator to compute DA cost without re-fetching the
	// raw tx.
	// VI: kích thước tx đã mã hoá — dùng để ước lượng chi phí đăng lên DA.
	Bytes uint64 `json:"bytes,omitempty"`
}

// Option configures the executor at creation time.
// VI: kiểu hàm "tuỳ chọn" — nhận con trỏ executor và sửa nó (functional options).
type Option func(*CosmosExecutor)

// WithQueryGasMax sets the gas limit for WASM smart queries.
// VI: trả về 1 Option đặt giới hạn gas cho query WASM. gas<=0 bị bỏ qua.
func WithQueryGasMax(gas uint64) Option {
	return func(e *CosmosExecutor) {
		if gas > 0 {
			e.queryGasMax = gas
		}
	}
}

// WithGenesis overrides the genesis app-state used at InitChain. Pass the
// output of app.GenesisWithBalances(...) to fund a treasury at chain init.
// Only applied on first init; once chain state is persisted, the stored
// state wins (changing genesis afterwards requires a clean start).
// VI: Option ghi đè genesis. Chỉ có tác dụng lần init đầu tiên.
func WithGenesis(genesis []byte) Option {
	return func(e *CosmosExecutor) {
		if len(genesis) > 0 {
			e.genesis = genesis
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
//
// VI: Option bật lưu đĩa. Vì Option không trả error được, lỗi được ghi
// vào con trỏ initErr do người gọi truyền vào → kiểm tra sau khi gọi New().
func WithPersistence(dir string, initErr *error) Option {
	return func(e *CosmosExecutor) {
		// setErr: closure tiện ích — ghi lỗi vào *initErr nếu con trỏ khác nil.
		setErr := func(err error) {
			if initErr != nil {
				*initErr = err
			}
		}

		ps, err := NewPersistStore(dir) // mở/tạo store trên đĩa.
		if err != nil {
			setErr(fmt.Errorf("open persist store: %w", err))
			return
		}
		e.persistStore = ps

		// Replay chain metadata (initialized, chainID, stateRoot, heights).
		// VI: nạp lại metadata đã lưu để khôi phục state sau restart.
		meta, err := ps.LoadMetadata()
		if err != nil {
			setErr(fmt.Errorf("load metadata: %w", err))
			return
		}
		if meta.Initialized { // chỉ khôi phục nếu chain đã từng init.
			e.initialized = meta.Initialized
			e.chainID = meta.ChainID
			e.lastHeight = meta.LastHeight
			e.finalizedHeight = meta.FinalizedHeight
			// Restore BaseApp's chain ID after a restart. InitChain (which
			// normally calls SetChainID) is NOT re-run on an already-initialized
			// chain, so without this BaseApp keeps its empty default and the ante
			// handler verifies signatures against chain-id "" — making every
			// signed tx fail with "signature verification failed ... chain-id ()".
			// VI: khôi phục chain id cho BaseApp sau restart, nếu không ante
			// handler dùng chain-id rỗng → mọi tx đã ký đều fail.
			if e.chainID != "" && e.app != nil {
				baseapp.SetChainID(e.chainID)(e.app.BaseApp)
			}
			if meta.StateRoot != "" {
				root, decErr := hexDecode(meta.StateRoot) // hex -> bytes.
				if decErr != nil {
					setErr(fmt.Errorf("decode persisted state root: %w", decErr))
					return
				}
				e.stateRoot = root
			}
		}

		// Replay tx results. VI: nạp lại toàn bộ kết quả tx vào map RAM.
		txResults, txSkipped, err := ps.LoadTxResults()
		if err != nil {
			setErr(fmt.Errorf("load tx results: %w", err))
			return
		}
		for k, v := range txResults { // copy từng phần tử vào map của executor.
			e.txResults[k] = v
		}

		// Replay blocks. VI: nạp lại block, đồng thời cập nhật lastHeight.
		blocks, blockSkipped, err := ps.LoadBlocks()
		if err != nil {
			setErr(fmt.Errorf("load blocks: %w", err))
			return
		}
		for k, v := range blocks {
			e.blocks[k] = v
			if k > e.lastHeight { // giữ lastHeight = block cao nhất thấy được.
				e.lastHeight = k
			}
		}

		// Report skipped lines (corrupt data) but don't fail.
		// VI: dòng hỏng bị bỏ qua không coi là lỗi nặng (data vẫn khôi phục được phần lớn).
		totalSkipped := txSkipped + blockSkipped
		if totalSkipped > 0 && initErr != nil {
			// Not a hard error — data is partially recovered. Log via the error
			// pointer so callers can decide whether to warn or abort.
			// We don't set *initErr here because the replay succeeded overall.
			_ = totalSkipped // `_ =` : cố ý bỏ qua giá trị (tránh lỗi "unused").
		}
	}
}

// New: constructor executor. Tham số app + danh sách Option biến đổi.
// Trả về *CosmosExecutor đã áp dụng mọi option.
func New(appInstance *app.App, opts ...Option) *CosmosExecutor {
	exec := &CosmosExecutor{
		app:         appInstance,
		mempool:     make([][]byte, 0, 1024), // slice rỗng, dung lượng dự phòng 1024.
		txResults:   make(map[string]TxExecutionResult),
		blocks:      make(map[uint64]BlockInfo),
		queryGasMax: 50_000_000, // mặc định 50 triệu gas cho query (dấu _ cho dễ đọc).
	}
	for _, opt := range opts { // áp dụng lần lượt từng option lên exec.
		opt(exec)
	}
	return exec
}

// InitChain: khởi tạo chain (gọi 1 lần). Tham số: ctx, thời điểm genesis,
// chiều cao bắt đầu, chainID. Trả về: state root ban đầu + error.
func (e *CosmosExecutor) InitChain(ctx context.Context, genesisTime time.Time, initialHeight uint64, chainID string) ([]byte, error) {
	if err := ctx.Err(); err != nil { // ctx đã bị huỷ/timeout?
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

	if e.initialized { // đã init rồi → idempotent.
		if e.chainID != chainID {
			return nil, fmt.Errorf("executor already initialized with chain id %q", e.chainID)
		}
		// Trả về bản SAO state root (append vào nil slice = copy an toàn).
		return append([]byte(nil), e.stateRoot...), nil
	}

	// BaseApp validates that req.ChainId matches its internal chain ID.
	// Set it before calling InitChain so the validation passes.
	// VI: đặt chain id cho BaseApp trước, để InitChain không lỗi do mismatch.
	baseapp.SetChainID(chainID)(e.app.BaseApp)

	genesisBytes := e.genesis
	if len(genesisBytes) == 0 { // không có genesis tuỳ chỉnh → dùng mặc định.
		genesisBytes = e.app.DefaultGenesis()
	}
	// Gọi InitChain của app (ABCI). Trả về resp chứa AppHash.
	resp, err := e.app.InitChain(&abci.RequestInitChain{
		Time:          genesisTime,
		ChainId:       chainID,
		InitialHeight: int64(initialHeight),
		AppStateBytes: genesisBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("init chain: %w", err)
	}

	stateRoot := append([]byte(nil), resp.AppHash...) // copy app hash.
	if len(stateRoot) == 0 {
		// Một số trường hợp AppHash rỗng → phải Commit để lấy hash thật.
		_, commitErr := e.app.Commit()
		if commitErr != nil {
			return nil, fmt.Errorf("commit after init chain: %w", commitErr)
		}
		stateRoot = append([]byte(nil), e.app.CommitMultiStore().LastCommitID().Hash...)
	}

	e.initialized = true
	e.chainID = chainID
	e.stateRoot = stateRoot
	e.lastHeight = initialHeight - 1 // block đầu tiên sẽ là initialHeight.

	if err := e.saveMetadataLocked(); err != nil { // lưu state xuống đĩa.
		return nil, fmt.Errorf("persist metadata after InitChain: %w", err)
	}

	return append([]byte(nil), e.stateRoot...), nil
}

// GetTxs: lấy & XOÁ toàn bộ tx trong mempool (để đưa vào block tiếp theo).
// Trả về slice tx + error.
func (e *CosmosExecutor) GetTxs(ctx context.Context) ([][]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.mempool) == 0 {
		return [][]byte{}, nil // mempool rỗng → trả slice rỗng (không nil).
	}

	txs := make([][]byte, len(e.mempool))
	copy(txs, e.mempool)     // copy ra slice mới để trả về (không chia sẻ backing array).
	e.mempool = e.mempool[:0] // cắt mempool về độ dài 0 (giữ dung lượng → tái dùng).

	return txs, nil
}

// ExecuteTxs: THỰC THI một block tx. Đây là hàm trung tâm.
// Tham số: ctx, danh sách tx, chiều cao block, timestamp, state root trước đó.
// Trả về: state root mới sau khi thực thi + error.
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
	// Kiểm tra state root trước phải khớp (đảm bảo chuỗi liên tục).
	if !bytesEqual(e.stateRoot, prevStateRoot) {
		return nil, fmt.Errorf("prev state root mismatch: expected %X got %X", e.stateRoot, prevStateRoot)
	}
	// Chiều cao phải đúng = lastHeight + 1 (không nhảy/lùi).
	if blockHeight != e.lastHeight+1 {
		return nil, fmt.Errorf("unexpected block height %d (expected %d)", blockHeight, e.lastHeight+1)
	}

	// Filter out empty txs for FinalizeBlock
	// VI: loại bỏ tx rỗng trước khi đưa vào FinalizeBlock.
	var validTxs [][]byte
	for _, tx := range txs {
		if len(tx) > 0 {
			validTxs = append(validTxs, tx)
		}
	}

	// FinalizeBlock: chạy toàn bộ tx (ante + message handler) trong 1 block.
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
	// VI: gom kết quả từng tx, ghi vào map và (tuỳ chọn) lưu đĩa.
	txHashes := make([]string, 0, len(finalizeResp.TxResults))
	for i, txResult := range finalizeResp.TxResults {
		if i >= len(validTxs) { // an toàn: phòng response dài hơn input.
			break
		}
		txHash := hashTx(validTxs[i])
		txHashes = append(txHashes, txHash)
		result := TxExecutionResult{
			Hash:      txHash,
			Height:    blockHeight,
			Code:      txResult.Code,
			Log:       txResult.Log,
			Events:    toExecTxEvents(txResult.Events), // chuyển event ABCI -> type nội bộ.
			GasUsed:   safeUint64(txResult.GasUsed),    // ép int64 -> uint64 an toàn.
			GasWanted: safeUint64(txResult.GasWanted),
			Bytes:     uint64(len(validTxs[i])),        // kích thước tx.
		}
		e.txResults[txHash] = result
		if e.persistStore != nil { // nếu bật lưu đĩa thì append.
			if persistErr := e.persistStore.AppendTxResult(result); persistErr != nil {
				return nil, fmt.Errorf("persist tx result: %w", persistErr)
			}
		}
	}

	_, err = e.app.Commit() // Commit: ghi state vào IAVL, tạo version mới.
	if err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}

	// In SDK v0.50, app hash is obtained from the commit multi store after Commit()
	// VI: SDK 0.50 lấy app hash từ commit store SAU khi Commit().
	e.stateRoot = append([]byte(nil), e.app.CommitMultiStore().LastCommitID().Hash...)
	e.lastHeight = blockHeight

	blockInfo := BlockInfo{
		Height:   blockHeight,
		Time:     timestamp.UTC().Format(time.RFC3339), // chuẩn hoá thời gian UTC.
		AppHash:  fmt.Sprintf("%x", e.stateRoot),        // %x: in bytes dạng hex.
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

	return append([]byte(nil), e.stateRoot...), nil // trả state root mới (bản sao).
}

// InjectTx: thêm 1 tx vào mempool. Trả về hash tx + error.
func (e *CosmosExecutor) InjectTx(ctx context.Context, tx []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(tx) == 0 {
		return "", errors.New("tx cannot be empty")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Copy tx ra vùng nhớ riêng (caller có thể tái dùng/sửa slice gốc).
	txCopy := append([]byte(nil), tx...)
	e.mempool = append(e.mempool, txCopy)

	return hashTx(txCopy), nil
}

// GetTxResult: tra kết quả 1 tx theo hash.
// Trả về: kết quả, bool "tìm thấy không", error.
func (e *CosmosExecutor) GetTxResult(ctx context.Context, hash string) (TxExecutionResult, bool, error) {
	if err := ctx.Err(); err != nil {
		return TxExecutionResult{}, false, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// normalizeHash: chuẩn hoá (bỏ 0x, lowercase) trước khi tra map.
	result, ok := e.txResults[normalizeHash(hash)]
	if !ok {
		return TxExecutionResult{}, false, nil // không tìm thấy, không phải lỗi.
	}

	return result, true, nil
}

// QuerySmart: truy vấn (read-only) một hợp đồng WASM.
// Lưu ý cú pháp khai báo giá trị trả về có TÊN: (result []byte, err error)
// — để defer recover() có thể gán lại result/err.
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

	// Chuyển địa chỉ bech32 -> sdk.AccAddress.
	contractAddr, err := sdk.AccAddressFromBech32(contract)
	if err != nil {
		return nil, fmt.Errorf("invalid contract address: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	// Recover from panics in WASM execution (e.g. out-of-gas, store access).
	// VI: WASM có thể panic (hết gas...). defer + recover() biến panic
	// thành error trả về thay vì làm sập tiến trình.
	defer func() {
		if r := recover(); r != nil {
			result = nil
			err = fmt.Errorf("wasm query panicked: %v", r)
		}
	}()

	height := e.lastHeight
	if height == 0 {
		height = 1 // chưa có block nào → dùng height 1 để query genesis state.
	}

	// Use checkState (isCheckTx=true). SDK 0.50 nils finalizeBlockState after
	// Commit, so NewContext(false) panics on queries that follow a block.
	// VI: dùng NewContext(true) (check state) vì sau Commit, finalize state
	// bị set nil; NewContext(false) sẽ panic.
	queryCtx := e.app.BaseApp.NewContext(true).WithBlockHeight(int64(height)).WithBlockTime(time.Now())

	// Set a gas limit to prevent unbounded WASM queries from panicking with out-of-gas.
	// VI: gắn GasMeter giới hạn để query không chạy vô hạn.
	queryCtx = queryCtx.WithGasMeter(storetypes.NewGasMeter(e.queryGasMax))

	queryResult, queryErr := e.app.WasmKeeper.QuerySmart(queryCtx, contractAddr, queryMsg)
	if queryErr != nil {
		return nil, queryErr
	}

	return append([]byte(nil), queryResult...), nil // trả bản sao kết quả.
}

// AccountInfo is the minimal view of an on-chain account needed by clients to
// sign txs: address + account number + sequence.
// VI: thông tin tối thiểu client cần để KÝ tx (address + số tài khoản + sequence).
type AccountInfo struct {
	Address       string `json:"address"`
	AccountNumber uint64 `json:"account_number"`
	Sequence      uint64 `json:"sequence"`
	Exists        bool   `json:"exists"`
}

// GetAccountInfo returns auth account state for the given bech32 address.
// Returns Exists=false if no account exists yet (sequence/number = 0).
// VI: lấy thông tin tài khoản theo địa chỉ. Nếu chưa tồn tại → "đoán trước"
// account number mà chain sẽ cấp khi ký lần đầu (ante tự tạo account).
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
		// VI: account chưa có → Peek (xem trước, không tăng) account number kế
		// tiếp để client ký đúng. Có race nếu nhiều địa chỉ mới gửi đồng thời —
		// chấp nhận được cho dev một người dùng.
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

// BalanceInfo is the bank balance view for an address: the full coin set plus,
// when a denom is requested, that single denom's amount broken out.
// VI: số dư của 1 địa chỉ; nếu hỏi 1 denom cụ thể thì tách riêng số đó.
type BalanceInfo struct {
	Address  string    `json:"address"`
	Balances sdk.Coins `json:"balances"`
	Denom    string    `json:"denom,omitempty"`
	Amount   string    `json:"amount,omitempty"`
}

// GetBalance returns the bank balances for the given bech32 address. If denom
// is non-empty, Amount is that denom's balance (0 if the address holds none).
// VI: trả về số dư. denom rỗng → chỉ trả toàn bộ; có denom → trả thêm số denom đó.
func (e *CosmosExecutor) GetBalance(ctx context.Context, bech32Addr, denom string) (BalanceInfo, error) {
	addr, err := sdk.AccAddressFromBech32(strings.TrimSpace(bech32Addr))
	if err != nil {
		return BalanceInfo{}, fmt.Errorf("invalid address: %w", err)
	}

	height := e.lastHeight
	if height == 0 {
		height = 1
	}
	queryCtx := e.app.BaseApp.NewContext(true).WithBlockHeight(int64(height))

	out := BalanceInfo{
		Address:  bech32Addr,
		Balances: e.app.BankKeeper.GetAllBalances(queryCtx, addr), // toàn bộ coin.
	}
	// Gán + kiểm tra cùng lúc: nếu denom (sau trim) khác rỗng...
	if denom = strings.TrimSpace(denom); denom != "" {
		out.Denom = denom
		out.Amount = e.app.BankKeeper.GetBalance(queryCtx, addr, denom).Amount.String()
	}
	return out, nil
}

// SimulateResult is the gas a tx would consume if executed now, without
// committing anything.
// VI: kết quả mô phỏng — lượng gas tx sẽ tốn, không ghi state.
type SimulateResult struct {
	GasUsed   uint64 `json:"gas_used"`
	GasWanted uint64 `json:"gas_wanted"`
}

// SimulateTx runs txBytes through the full ante chain + message handlers in
// simulation mode against the last committed state. Nothing is persisted and
// the fee policy is NOT enforced (see ante.go) so a 0-fee tx can be measured.
// Clients use the returned gas to size gas_limit (hence the fee) before
// signing for real.
// VI: chạy thử tx (không commit, không ép phí) để client biết gas → tính
// gas_limit/phí trước khi ký thật.
func (e *CosmosExecutor) SimulateTx(ctx context.Context, txBytes []byte) (SimulateResult, error) {
	if err := ctx.Err(); err != nil {
		return SimulateResult{}, err
	}
	if len(txBytes) == 0 {
		return SimulateResult{}, errors.New("tx cannot be empty")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.initialized {
		return SimulateResult{}, errors.New("executor not initialized")
	}

	gasInfo, _, err := e.app.Simulate(txBytes) // _ : bỏ qua kết quả message.
	if err != nil {
		return SimulateResult{}, fmt.Errorf("simulate: %w", err)
	}
	return SimulateResult{
		GasUsed:   gasInfo.GasUsed,
		GasWanted: gasInfo.GasWanted,
	}, nil
}

// SetFinal: đánh dấu 1 block đã được finalize (DA xác nhận). Trả error.
func (e *CosmosExecutor) SetFinal(ctx context.Context, blockHeight uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if blockHeight > e.lastHeight { // không thể finalize block chưa thực thi.
		return fmt.Errorf("cannot finalize future block %d, last executed %d", blockHeight, e.lastHeight)
	}
	if blockHeight > e.finalizedHeight { // chỉ tiến lên, không lùi.
		e.finalizedHeight = blockHeight
	}

	if err := e.saveMetadataLocked(); err != nil {
		return fmt.Errorf("persist metadata after SetFinal: %w", err)
	}

	return nil
}

// GetExecutionInfo: trả thông tin thực thi (ở đây MaxGas=0 = không giới hạn).
func (e *CosmosExecutor) GetExecutionInfo(ctx context.Context) (execution.ExecutionInfo, error) {
	if err := ctx.Err(); err != nil {
		return execution.ExecutionInfo{}, err
	}

	return execution.ExecutionInfo{MaxGas: 0}, nil
}

// FilterTxs: quyết định mỗi tx được giữ/hoãn/bỏ dựa trên giới hạn byte.
// Dấu `_` trong tham số = nhận nhưng không dùng. Trả slice trạng thái + error.
func (e *CosmosExecutor) FilterTxs(ctx context.Context, txs [][]byte, maxBytes, _ uint64, _ bool) ([]execution.FilterStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	statuses := make([]execution.FilterStatus, len(txs)) // 1 trạng thái / tx.
	var cumulativeBytes uint64                            // tổng byte đã chấp nhận.

	for i, tx := range txs {
		txLen := uint64(len(tx))
		if txLen == 0 {
			statuses[i] = execution.FilterRemove // tx rỗng → bỏ.
			continue
		}

		// Vượt giới hạn tổng byte → hoãn tx này sang block sau.
		if maxBytes > 0 && cumulativeBytes+txLen > maxBytes {
			statuses[i] = execution.FilterPostpone
			continue
		}

		statuses[i] = execution.FilterOK // chấp nhận.
		cumulativeBytes += txLen
	}

	return statuses, nil
}

// GetLatestBlock returns the most recently executed block info.
// VI: lấy block mới nhất. Trả (block, tìm thấy?, error).
func (e *CosmosExecutor) GetLatestBlock(ctx context.Context) (BlockInfo, bool, error) {
	if err := ctx.Err(); err != nil {
		return BlockInfo{}, false, err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if e.lastHeight == 0 {
		return BlockInfo{}, false, nil // chưa có block nào.
	}

	info, ok := e.blocks[e.lastHeight]
	return info, ok, nil
}

// GetBlock returns block info at a specific height.
// VI: lấy block theo chiều cao cụ thể.
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
// VI: trả trạng thái tổng quan; Synced=true khi finalized bắt kịp lastHeight.
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
// VI: số tx đang chờ trong mempool.
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
// VI: trả chiều cao hiện tại — ev-node dùng để kiểm tra đồng bộ sau crash.
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
//
// VI: lùi state về targetHeight khi executor đi trước consensus. Nạp lại
// version IAVL cũ + xoá dữ liệu RAM phía trên targetHeight.
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
		return nil // already at target — không cần làm gì.
	}

	// Load the IAVL state at the target version. BaseApp's CommitMultiStore
	// keeps versioned state in IAVL trees, so we can reload an older version.
	// VI: IAVL lưu state theo từng version → nạp lại đúng version cũ.
	if err := e.app.LoadVersion(int64(targetHeight)); err != nil {
		return fmt.Errorf("load version %d: %w", targetHeight, err)
	}

	// Update state root from the reloaded commit store.
	cms := e.app.CommitMultiStore()
	stateRoot := cms.LastCommitID().Hash
	e.stateRoot = append([]byte(nil), stateRoot...)

	// Trim in-memory data above target height.
	// VI: xoá block/tx có height > target khỏi map RAM.
	for h := range e.blocks {
		if h > targetHeight {
			delete(e.blocks, h) // delete: xoá 1 key khỏi map.
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
//
// VI: xoá tx/block ở height <= mức cho trước để giải phóng RAM cho node
// chạy lâu dài.
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
// VI: giải phóng tài nguyên (đóng file lưu trữ). `_ =` bỏ qua lỗi đóng file.
func (e *CosmosExecutor) Close() {
	if e.persistStore != nil {
		_ = e.persistStore.Close()
	}
}

// Stats holds runtime metrics for monitoring.
// VI: số liệu runtime cho endpoint giám sát/health.
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
// VI: lưu metadata hiện tại xuống đĩa. NGƯỜI GỌI phải đang giữ khoá e.mu
// (hậu tố "Locked" là quy ước báo điều này). persistStore nil → bỏ qua.
func (e *CosmosExecutor) saveMetadataLocked() error {
	if e.persistStore == nil {
		return nil
	}
	return e.persistStore.SaveMetadata(ChainMetadata{
		Initialized:     e.initialized,
		ChainID:         e.chainID,
		StateRoot:       fmt.Sprintf("%x", e.stateRoot), // bytes -> hex string.
		LastHeight:      e.lastHeight,
		FinalizedHeight: e.finalizedHeight,
	})
}

// bytesEqual: so sánh 2 slice byte có bằng nhau từng phần tử không.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) { // khác độ dài → chắc chắn khác.
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// hashTx: băm tx bằng SHA-256, trả chuỗi hex. h[:] : slice từ mảng cố định.
func hashTx(tx []byte) string {
	h := sha256.Sum256(tx) // trả mảng [32]byte.
	return fmt.Sprintf("%x", h[:])
}

// safeUint64 clamps a signed ABCI gas value to a non-negative uint64. ABCI's
// GasUsed/GasWanted are int64; spec says they're non-negative, but we don't
// want a misbehaving handler to wrap into a giant uint64.
// VI: ép int64 -> uint64 an toàn: số âm -> 0 (tránh tràn thành số khổng lồ).
func safeUint64(v int64) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}

// normalizeHash: chuẩn hoá chuỗi hash trước khi tra cứu — bỏ khoảng trắng,
// bỏ tiền tố 0x/0X, đưa về chữ thường.
func normalizeHash(hash string) string {
	hash = strings.TrimSpace(hash)
	hash = strings.TrimPrefix(hash, "0x")
	hash = strings.TrimPrefix(hash, "0X")
	return strings.ToLower(hash)
}

// toExecTxEvents: chuyển danh sách event ABCI -> []TxEvent nội bộ.
func toExecTxEvents(events []abci.Event) []TxEvent {
	out := make([]TxEvent, 0, len(events)) // cấp sẵn dung lượng = số event.
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
