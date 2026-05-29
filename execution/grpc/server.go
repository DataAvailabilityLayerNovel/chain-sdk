// package grpc: cài đặt gRPC server (qua thư viện connect-go) bọc quanh
// execution.Executor để ev-node có thể gọi Executor qua mạng. Nói cách khác:
// đây là "bộ chuyển đổi" interface Go ↔ giao thức gRPC/Protobuf.
package grpc

import (
	"context" // truyền tín hiệu huỷ/timeout qua biên RPC.
	"errors"  // tạo lỗi đơn giản (validation).
	"fmt"     // định dạng chuỗi & bọc lỗi.

	// connect: thư viện connectrpc.com — gRPC + HTTP/JSON trên cùng endpoint,
	// nhẹ hơn google grpc, phù hợp môi trường web.
	"connectrpc.com/connect"

	// execution: interface Executor mà Server này uỷ thác (xem core/execution).
	"github.com/DataAvailabilityLayerNovel/chain-sdk/core/execution"
	// pb: type Protobuf đã sinh tự động (request/response RPC).
	// alias `pb` cho gọn.
	pb "github.com/DataAvailabilityLayerNovel/chain-sdk/types/pb/evnode/v1"
)

// Server is a gRPC server that wraps an execution.Executor implementation.
// It handles the conversion between gRPC types and internal types.
//
// VI: struct Server "ôm" 1 Executor và biến nó thành RPC service. Nhiệm vụ:
// nhận request protobuf → bóc field → gọi method Go tương ứng → đóng gói
// kết quả thành response protobuf.
type Server struct {
	executor execution.Executor // executor thật bên dưới (vd: cosmos-exec).
}

// NewServer creates a new gRPC server that wraps the given executor.
//
// Parameters:
// - executor: The underlying execution implementation to wrap
//
// Returns:
// - *Server: The initialized gRPC server
//
// VI: constructor — trả con trỏ *Server đã gắn executor.
func NewServer(executor execution.Executor) *Server {
	return &Server{
		executor: executor,
	}
}

// InitChain handles the InitChain RPC request.
//
// It initializes the blockchain with the given genesis parameters by delegating
// to the underlying executor implementation.
//
// VI: xử lý RPC InitChain. Validate field bắt buộc → gọi executor.InitChain →
// đóng gói stateRoot trả về. Mọi method có signature
// (ctx, *connect.Request[T]) (*connect.Response[U], error) là quy ước connect-go.
func (s *Server) InitChain(
	ctx context.Context,
	req *connect.Request[pb.InitChainRequest], // wrapper chứa msg + header/trailer.
) (*connect.Response[pb.InitChainResponse], error) {
	// req.Msg: lấy payload protobuf thật.
	if req.Msg.GenesisTime == nil {
		// connect.NewError + Code*: trả mã lỗi gRPC chuẩn (InvalidArgument = 3).
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("genesis_time is required"))
	}

	if req.Msg.InitialHeight == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("initial_height must be > 0"))
	}

	if req.Msg.ChainId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("chain_id is required"))
	}

	// AsTime(): chuyển google.protobuf.Timestamp -> time.Time của Go.
	stateRoot, err := s.executor.InitChain(
		ctx,
		req.Msg.GenesisTime.AsTime(),
		req.Msg.InitialHeight,
		req.Msg.ChainId,
	)
	if err != nil {
		// Internal (13): lỗi phía server, không phải do client gửi sai.
		// %w: bọc lỗi gốc để client có thể trích thông tin chi tiết.
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to init chain: %w", err))
	}

	// Gói kết quả vào response protobuf.
	return connect.NewResponse(&pb.InitChainResponse{
		StateRoot: stateRoot,
	}), nil
}

// GetTxs handles the GetTxs RPC request.
//
// It fetches available transactions from the execution layer's mempool.
//
// VI: trả danh sách tx đang chờ trong mempool.
// req không có field nào cần — chỉ là "ping" để lấy tx.
func (s *Server) GetTxs(
	ctx context.Context,
	req *connect.Request[pb.GetTxsRequest],
) (*connect.Response[pb.GetTxsResponse], error) {
	txs, err := s.executor.GetTxs(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get txs: %w", err))
	}

	return connect.NewResponse(&pb.GetTxsResponse{
		Txs: txs,
	}), nil
}

// ExecuteTxs handles the ExecuteTxs RPC request.
//
// It processes transactions to produce a new block state by delegating to
// the underlying executor implementation.
//
// VI: thực thi 1 block tx. Validate đủ tham số → uỷ thác cho executor.
// Đây là call NÓNG nhất — mọi block đều đi qua đây.
func (s *Server) ExecuteTxs(
	ctx context.Context,
	req *connect.Request[pb.ExecuteTxsRequest],
) (*connect.Response[pb.ExecuteTxsResponse], error) {
	if req.Msg.BlockHeight == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("block_height must be > 0"))
	}

	if req.Msg.Timestamp == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("timestamp is required"))
	}

	if len(req.Msg.PrevStateRoot) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("prev_state_root is required"))
	}

	updatedStateRoot, err := s.executor.ExecuteTxs(
		ctx,
		req.Msg.Txs,
		req.Msg.BlockHeight,
		req.Msg.Timestamp.AsTime(),
		req.Msg.PrevStateRoot,
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to execute txs: %w", err))
	}

	return connect.NewResponse(&pb.ExecuteTxsResponse{
		UpdatedStateRoot: updatedStateRoot,
	}), nil
}

// SetFinal handles the SetFinal RPC request.
//
// It marks a block as finalized at the specified height.
//
// VI: đánh dấu block "đã finalize" (DA xác nhận, không đảo ngược được).
func (s *Server) SetFinal(
	ctx context.Context,
	req *connect.Request[pb.SetFinalRequest],
) (*connect.Response[pb.SetFinalResponse], error) {
	if req.Msg.BlockHeight == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("block_height must be > 0"))
	}

	err := s.executor.SetFinal(ctx, req.Msg.BlockHeight)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to set final: %w", err))
	}

	// Response rỗng (chỉ cần biết có lỗi hay không) → khởi tạo struct trống &.
	return connect.NewResponse(&pb.SetFinalResponse{}), nil
}

// GetExecutionInfo handles the GetExecutionInfo RPC request.
//
// It returns current execution layer parameters such as the block gas limit.
//
// VI: trả tham số execution (vd: MaxGas). ev-node dùng khi đóng block.
func (s *Server) GetExecutionInfo(
	ctx context.Context,
	req *connect.Request[pb.GetExecutionInfoRequest],
) (*connect.Response[pb.GetExecutionInfoResponse], error) {
	info, err := s.executor.GetExecutionInfo(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get execution info: %w", err))
	}

	return connect.NewResponse(&pb.GetExecutionInfoResponse{
		MaxGas: info.MaxGas,
	}), nil
}

// FilterTxs handles the FilterTxs RPC request.
//
// It validates force-included transactions and applies gas and size filtering.
// Returns a slice of FilterStatus for each transaction.
//
// VI: lọc tx (force-include + mempool) trước khi đóng block. Chuyển đổi
// type Go ↔ enum protobuf cho từng phần tử trước khi trả về.
func (s *Server) FilterTxs(
	ctx context.Context,
	req *connect.Request[pb.FilterTxsRequest],
) (*connect.Response[pb.FilterTxsResponse], error) {
	result, err := s.executor.FilterTxs(ctx, req.Msg.Txs, req.Msg.MaxBytes, req.Msg.MaxGas, req.Msg.HasForceIncludedTransaction)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to filter transactions: %w", err))
	}

	// Convert execution.FilterStatus to protobuf FilterStatus
	// VI: 2 enum chung giá trị int (FilterOK=0, Remove=1, Postpone=2) nhưng
	// khác kiểu Go → ép kiểu pb.FilterStatus(status) từng phần tử.
	statuses := make([]pb.FilterStatus, len(result)) // cấp đúng kích thước.
	for i, status := range result {
		statuses[i] = pb.FilterStatus(status)
	}

	return connect.NewResponse(&pb.FilterTxsResponse{
		Statuses: statuses,
	}), nil
}
