// package grpc: tầng "dây nối" — gắn Server (xử lý RPC) lên 1 http.Handler
// có thể phục vụ HTTP/1.1 và HTTP/2 (h2c, không cần TLS) trên cùng cổng.
// Đồng thời mở thêm cổng cho route REST/HTTP tuỳ chỉnh (NewExecutorServiceHandlerWithMux).
package grpc

import (
	"net/http" // server HTTP chuẩn của Go.
	"time"     // timeout HTTP/2.

	"connectrpc.com/connect"       // gRPC + JSON trên cùng 1 endpoint.
	"connectrpc.com/grpcreflect"   // gRPC reflection (cho phép tool debug "soi" service).
	"golang.org/x/net/http2"       // server HTTP/2.
	"golang.org/x/net/http2/h2c"   // h2c: HTTP/2 không TLS (cleartext).

	"github.com/DataAvailabilityLayerNovel/chain-sdk/core/execution"
	// v1connect: code sinh tự động — handler/client dùng connect-go cho proto v1.
	"github.com/DataAvailabilityLayerNovel/chain-sdk/types/pb/evnode/v1/v1connect"
)

// NewExecutorServiceHandler creates a new HTTP handler for the ExecutorService.
// It follows the same pattern as other services in the codebase, supporting
// both HTTP/1.1 and HTTP/2 without TLS using h2c.
//
// Parameters:
// - executor: The execution implementation to serve
// - opts: Optional server options for configuring the service
//
// Returns:
// - http.Handler: The configured HTTP handler
//
// VI: tiện ích — gọi NewExecutorServiceHandlerWithMux với register=nil
// (không gắn route REST nào ngoài service gRPC). Dùng khi chỉ cần expose
// gRPC thuần. `opts ...connect.HandlerOption`: tham số biến đổi (variadic).
func NewExecutorServiceHandler(executor execution.Executor, opts ...connect.HandlerOption) http.Handler {
	return NewExecutorServiceHandlerWithMux(executor, nil, opts...)
}

// NewExecutorServiceHandlerWithMux: phiên bản "đầy đủ" — cho phép truyền 1
// callback `register` để gắn thêm các route REST tuỳ ý (vd: /tx/submit,
// /blocks/latest...). cosmos-exec-grpc/main.go gọi qua đây để vừa có gRPC
// chính chủ vừa có REST cho dApp/explorer.
func NewExecutorServiceHandlerWithMux(
	executor execution.Executor,
	register func(*http.ServeMux), // nil → không có route REST ngoài service gRPC.
	opts ...connect.HandlerOption,
) http.Handler {
	server := NewServer(executor) // dựng Server gRPC bọc executor.

	mux := http.NewServeMux() // bộ định tuyến HTTP chuẩn của Go.
	if register != nil {
		// Cho caller cơ hội đăng ký route riêng TRƯỚC khi gắn route gRPC.
		register(mux)
	}

	// Configure compression to start at 1KB
	// VI: chỉ nén response khi body >= 1KB (nén msg nhỏ tốn CPU hơn lợi).
	compress1KB := connect.WithCompressMinBytes(1024)

	// Set up gRPC reflection for debugging and discovery
	// VI: bật reflection — tool như grpcurl/Postman có thể "tự khám phá"
	// danh sách service & method mà không cần file .proto. Có 2 phiên bản
	// (V1 & V1Alpha) để tương thích nhiều phiên bản client.
	reflector := grpcreflect.NewStaticReflector(
		v1connect.ExecutorServiceName,
	)
	mux.Handle(grpcreflect.NewHandlerV1(reflector, compress1KB))
	mux.Handle(grpcreflect.NewHandlerV1Alpha(reflector, compress1KB))

	// Register the ExecutorService
	// VI: code sinh tự động trả ("đường dẫn", handler). Path nhìn dạng
	// /evnode.v1.ExecutorService/ — connect-go định tuyến tự động.
	// append(opts, compress1KB)...: nối option của caller với option nén.
	path, handler := v1connect.NewExecutorServiceHandler(server, append(opts, compress1KB)...)
	mux.Handle(path, handler)

	// Use h2c to support HTTP/2 without TLS
	// VI: h2c = HTTP/2 cleartext (không TLS). gRPC yêu cầu HTTP/2;
	// dev/staging thường không có TLS → bọc mux bằng h2c để chạy được.
	// Tham số http2.Server: cấu hình timeout/giới hạn cho stream HTTP/2.
	return h2c.NewHandler(mux, &http2.Server{
		IdleTimeout:          120 * time.Second, // đóng connection idle sau 2 phút.
		MaxReadFrameSize:     1 << 24,           // 16MB — frame tối đa (1<<24 = 2^24).
		MaxConcurrentStreams: 100,               // tối đa 100 stream song song / connection.
		ReadIdleTimeout:      30 * time.Second,  // không có data 30s → gửi PING kiểm tra.
		PingTimeout:          15 * time.Second,  // PING không phản hồi 15s → đóng connection.
	})
}
