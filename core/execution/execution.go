// package execution: ĐỊNH NGHĨA INTERFACE (không có implementation) cho lớp
// thực thi của ev-node. Đây là "hợp đồng" giữa consensus (ev-node) và lớp
// chạy state (cosmos-exec, evm, abci...). Bất cứ ai muốn cắm vào ev-node
// đều phải cài đặt interface Executor dưới đây.
//
// File này nằm trong core/ — KHÔNG có dependency ngoại trừ standard library
// (xem CLAUDE.md "zero-dependency core package").
package execution

import (
	"context" // truyền tín hiệu huỷ/timeout xuyên các hàm.
	"time"    // type Time cho timestamp block.
)

// Executor defines the interface that execution clients must implement to be compatible with Evolve.
// This interface enables the separation between consensus and execution layers, allowing for modular
// and pluggable execution environments.
//
// Note: if you are modifying this interface, ensure that all implementations are compatible (evm, abci, protobuf/grpc, etc.)
//
// VI: interface "hợp đồng" — lớp thực thi (cosmos-exec, evm...) phải cài đặt
// đầy đủ 6 method này để cắm vào ev-node. Tách consensus khỏi execution giúp
// đổi engine mà không động chạm vào core. Khi sửa interface phải cập nhật
// MỌI implementation (evm, abci, protobuf/grpc...).
type Executor interface {
	// InitChain initializes a new blockchain instance with genesis parameters.
	// Requirements:
	// - Must generate initial state root representing empty/genesis state
	// - Must validate and store genesis parameters for future reference
	// - Must ensure idempotency (repeated calls with identical parameters should return same results)
	// - Must return error if genesis parameters are invalid
	//
	// Parameters:
	// - ctx: Context for timeout/cancellation control
	// - genesisTime: timestamp marking chain start time in UTC
	// - initialHeight: First block height (must be > 0)
	// - chainID: Unique identifier string for the blockchain
	//
	// Returns:
	// - stateRoot: Hash representing initial state
	// - err: Any initialization errors
	//
	// VI: gọi 1 lần khi khởi tạo chain (block 0). Phải:
	// - sinh state root ban đầu (đại diện state "rỗng" / genesis),
	// - lưu tham số genesis cho lần truy vấn sau,
	// - idempotent: gọi lại với cùng tham số → trả cùng kết quả,
	// - báo lỗi nếu tham số sai. cosmos-exec cài hàm này trong
	//   apps/cosmos-exec/executor/executor.go.
	InitChain(ctx context.Context, genesisTime time.Time, initialHeight uint64, chainID string) (stateRoot []byte, err error)

	// GetTxs fetches available transactions from the execution layer's mempool.
	// Requirements:
	// - Must return currently valid transactions only
	// - Must handle empty mempool case gracefully
	// - Must respect context cancellation/timeout
	// - Should perform basic transaction validation
	// - Should not remove transactions from mempool
	// - May remove invalid transactions from mempool
	//
	// Parameters:
	// - ctx: Context for timeout/cancellation control
	//
	// Returns:
	// - [][]byte: Slice of valid transactions
	// - error: Any errors during transaction retrieval
	//
	// VI: lấy danh sách tx đang chờ trong mempool của execution layer để
	// đưa vào block kế tiếp. Phải:
	// - chỉ trả tx HỢP LỆ hiện tại,
	// - mempool rỗng → trả slice rỗng (không panic, không lỗi),
	// - tôn trọng ctx (huỷ/timeout),
	// - KHÔNG nên tự xoá tx khỏi mempool (trừ tx rõ ràng sai).
	GetTxs(ctx context.Context) ([][]byte, error)

	// ExecuteTxs processes transactions to produce a new block state.
	// Requirements:
	// - Must validate state transition against previous state root
	// - Must handle empty transaction list
	// - Must handle gracefully gibberish transactions
	// - Must maintain deterministic execution
	// - Must respect context cancellation/timeout
	// - The rest of the rules are defined by the specific execution layer
	//
	// Parameters:
	// - ctx: Context for timeout/cancellation control
	// - txs: Ordered list of transactions to execute
	// - blockHeight: Height of block being created (must be > 0)
	// - timestamp: Block creation time in UTC
	// - prevStateRoot: Previous block's state root hash
	//
	// Returns:
	// - updatedStateRoot: New state root after executing transactions
	// - err: Any execution errors
	//
	// VI: thực thi 1 block tx, trả state root mới. Phải:
	// - kiểm tra prevStateRoot khớp với state hiện tại (chống fork),
	// - xử lý gọn list tx rỗng / tx rác,
	// - DETERMINISTIC: cùng input phải ra cùng output trên mọi node
	//   (nếu khác → fork). cosmos-exec gọi app.FinalizeBlock + Commit để
	//   đảm bảo điều này.
	ExecuteTxs(ctx context.Context, txs [][]byte, blockHeight uint64, timestamp time.Time, prevStateRoot []byte) (updatedStateRoot []byte, err error)

	// SetFinal marks a block as finalized at the specified height.
	// Requirements:
	// - Must verify block exists at specified height
	// - Must be idempotent
	// - Must maintain finality guarantees (no reverting finalized blocks)
	// - Must respect context cancellation/timeout
	// - Should clean up any temporary state/resources
	//
	// Parameters:
	// - ctx: Context for timeout/cancellation control
	// - blockHeight: Height of block to finalize
	//
	// Returns:
	// - error: Any errors during finalization
	//
	// VI: ev-node gọi khi DA đã xác nhận block → đánh dấu "không thể đảo ngược".
	// Phải:
	// - kiểm tra block ở height đó có thật,
	// - idempotent (gọi lại OK),
	// - KHÔNG được rollback block đã finalize sau điểm này,
	// - giải phóng state tạm liên quan tới block đó.
	SetFinal(ctx context.Context, blockHeight uint64) error

	// GetExecutionInfo returns current execution layer parameters.
	//
	// Parameters:
	// - ctx: Context for timeout/cancellation control
	//
	// Returns:
	// - info: Current execution parameters
	// - error: Any errors during retrieval
	//
	// VI: trả tham số runtime của lớp thực thi (vd: gas tối đa 1 block).
	// ev-node dùng để biết giới hạn khi đóng block. cosmos-exec hiện trả
	// MaxGas=0 (không giới hạn — phù hợp app không tính theo gas/block).
	GetExecutionInfo(ctx context.Context) (ExecutionInfo, error)

	// FilterTxs validates force-included transactions and applies gas and size filtering for all passed txs.
	//
	// The function marks transaction with a filter status. The sequencer knows how to proceed with it:
	// - Transactions passing all filters constraints and that can be included (FilterOK)
	// - Invalid/unparseable force-included transactions (gibberish) (FilterRemove)
	// - Any transactions that would exceed the cumulative gas limit (FilterPostpone)
	//
	// For non-gas-based execution layers (maxGas=0) should not filter by gas.
	//
	// Parameters:
	// - ctx: Context for timeout/cancellation control
	// - txs: All transactions (force-included + mempool)
	// - maxBytes: Maximum cumulative size allowed (0 means no size limit)
	// - maxGas: Maximum cumulative gas allowed (0 means no gas limit)
	// - hasForceIncludedTransaction: Boolean wether force included txs are present
	//
	// Returns:
	// - result: The filter status of all txs. The len(txs) == len(result).
	// - err: Any errors during filtering (not validation errors, which result in filtering)
	//
	// VI: lọc tx (force-include + mempool) trước khi sequencer đóng block.
	// Trả slice trạng thái CÙNG ĐỘ DÀI với txs — mỗi tx có 1 trong 3 trạng thái:
	// FilterOK (giữ), FilterRemove (vứt vì hỏng), FilterPostpone (hoãn block sau
	// vì vượt giới hạn). maxBytes/maxGas = 0 nghĩa là không giới hạn theo loại đó.
	FilterTxs(ctx context.Context, txs [][]byte, maxBytes, maxGas uint64, hasForceIncludedTransaction bool) ([]FilterStatus, error)
}

// FilterStatus is the result of FilterTxs tx status.
//
// VI: kiểu enum (số nguyên có tên) cho 3 trạng thái lọc tx.
type FilterStatus int

const (
	// FilterOK is the result of a transaction that will make it to the next batch
	// VI: tx OK, được đưa vào block tiếp theo. `iota`: bắt đầu đếm từ 0
	// trong khối const → FilterOK=0, FilterRemove=1, FilterPostpone=2.
	FilterOK FilterStatus = iota
	// FilterRemove is the result of a transaction that will be filtered out because invalid (too big, malformed, etc.)
	// VI: tx XẤU — quá to / sai cú pháp → bỏ luôn, không đưa vào block nào.
	FilterRemove
	// FilterPostpone is the result of a transaction that is valid but postponed for later processing due to size constraint
	// VI: tx hợp lệ nhưng vượt giới hạn block hiện tại → HOÃN, thử lại block sau.
	FilterPostpone
)

// ExecutionInfo contains execution layer parameters that may change per block.
//
// VI: tham số runtime lớp thực thi báo cho ev-node biết.
type ExecutionInfo struct {
	// MaxGas is the maximum gas allowed for transactions in a block.
	// For non-gas-based execution layers, this should be 0.
	//
	// VI: gas tối đa cho 1 block; 0 = không giới hạn (cho lớp không dùng gas/block).
	MaxGas uint64
}

// HeightProvider is an optional interface that execution clients can implement
// to support height synchronization checks between ev-node and the execution layer.
//
// VI: interface TUỲ CHỌN. Nếu cài đặt, ev-node có thể kiểm tra chiều cao
// hiện tại của lớp thực thi (phát hiện lệch sau crash). cosmos-exec cài đặt
// hàm này (trả về lastHeight) để ev-node so sánh & rollback nếu cần.
type HeightProvider interface {
	// GetLatestHeight returns the current block height of the execution layer.
	// This is useful for detecting desynchronization between ev-node and the execution layer
	// after crashes or restarts.
	//
	// Parameters:
	// - ctx: Context for timeout/cancellation control
	//
	// Returns:
	// - height: Current block height of the execution layer
	// - error: Any errors during height retrieval
	//
	// VI: trả chiều cao block cao nhất đã thực thi xong. Dùng để khôi phục
	// đồng bộ giữa ev-node và execution layer sau restart/crash.
	GetLatestHeight(ctx context.Context) (uint64, error)
}

// Rollbackable is an optional interface that execution clients can implement
// to support automatic rollback when the execution layer is ahead of the target height.
// This enables automatic recovery during rolling restarts when the EL has committed
// blocks that were not replicated to the consensus layer.
//
// Requirements:
// - Only execution layers supporting in-flight rollback should implement this.
//
// VI: interface TUỲ CHỌN cho phép ev-node yêu cầu execution layer LÙI state
// về 1 height cũ hơn. Tình huống điển hình: execution đã commit block 10
// nhưng consensus chỉ thấy đến block 8 → rollback về 8 để khớp lại.
// Chỉ cài nếu lớp thực thi thực sự lùi state được (cosmos-exec dùng IAVL
// có versioning nên cài được).
type Rollbackable interface {
	// Rollback resets the execution layer head to the specified height.
	// VI: đưa head về targetHeight (đồng nghĩa "đảo block > targetHeight").
	Rollback(ctx context.Context, targetHeight uint64) error
}

// ExecPruner is an optional interface that execution clients can implement
// to support height-based pruning of their execution metadata.
//
// VI: interface TUỲ CHỌN — cho phép dọn metadata cũ (tx result, block info)
// theo chiều cao để node chạy lâu dài không phình bộ nhớ/đĩa.
type ExecPruner interface {
	// PruneExec should delete execution metadata for all heights up to and
	// including the given height. Implementations should be idempotent and track
	// their own progress so that repeated calls with the same or decreasing
	// heights are cheap no-ops.
	//
	// VI: xoá metadata ở mọi height <= height truyền vào. PHẢI idempotent —
	// gọi lại với cùng height (hoặc nhỏ hơn) không tốn thêm tài nguyên.
	PruneExec(ctx context.Context, height uint64) error
}
