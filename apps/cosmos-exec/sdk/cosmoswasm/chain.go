// package cosmoswasm: phần "tiện ích dev" — file này expose hàm khởi
// động/dừng 1 chain local (dùng cho test/integration). Logic thật nằm trong
// internal/devchain; ở đây chỉ là LỚP MỎNG ánh xạ config public ↔ internal.
package cosmoswasm

import (
	"context" // truyền tín hiệu huỷ/timeout cho lệnh start chain.
	"io"      // io.Writer cho Stdout/Stderr của tiến trình con.
	"time"    // BlockTime/SubmitInterval kiểu time.Duration.

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm/internal/devchain"
)

// Default endpoint URLs used by DALChainConfig when no overrides are provided.
//
// VI: re-export hằng từ internal/devchain ra ngoài để client SDK dùng được
// (vì internal/* không import được trực tiếp từ ngoài module).
const (
	DefaultSequencerRPCURL  = devchain.DefaultSequencerRPCURL
	DefaultFullNodeRPCURL   = devchain.DefaultFullNodeRPCURL
	DefaultSequencerExecURL = devchain.DefaultSequencerExecURL
	DefaultFullNodeExecURL  = devchain.DefaultFullNodeExecURL
)

// DALChainConfig holds configuration for launching a local DAL chain.
// This is a dev/test utility — not part of the core SDK API.
//
// VI: struct cấu hình chạy chain local. CHỈ dùng cho dev/test, không thuộc
// API SDK chính. Các field cấu hình tiến trình (đường dẫn, log, network...).
type DALChainConfig struct {
	ProjectRoot    string        // thư mục gốc project (chứa binary cần chạy).
	ChainName      string        // tên chain (dùng làm dir/data path).
	Namespace      string        // namespace DA — phân tách dữ liệu giữa các chain.
	DABridgeRPC    string        // URL RPC tới DA bridge node (Celestia...).
	DAAuthToken    string        // token auth gửi kèm DA RPC.
	CleanOnStart   bool          // true → xoá data cũ trước khi start (chain mới tinh).
	CleanOnExit    bool          // true → dọn data khi Stop (tránh để lại rác).
	LogLevel       string        // mức log: debug/info/error.
	BlockTime      time.Duration // chu kỳ tạo block (vd 1s).
	SubmitInterval time.Duration // chu kỳ submit batch lên DA.
	Stdout         io.Writer     // nơi pipe stdout của tiến trình con; nil → /dev/null.
	Stderr         io.Writer     // nơi pipe stderr; nil → /dev/null.

	SequencerRPC     string // override URL RPC sequencer (rỗng → default).
	FullNodeRPC      string // override URL RPC full node.
	SequencerExecURL string // override URL exec API sequencer.
	FullNodeExecURL  string // override URL exec API full node.
}

// DALChainEndpoints contains the resolved endpoint URLs for the running chain.
//
// VI: URL thực tế sau khi chain đã start (đã apply default nếu config rỗng).
// Client SDK dùng các URL này để kết nối tới chain vừa khởi động.
type DALChainEndpoints struct {
	SequencerRPC     string
	FullNodeRPC      string
	SequencerExecAPI string
	FullNodeExecAPI  string
}

// DALChainProcess represents a running local chain process.
//
// VI: handle tới tiến trình chain đang chạy. Giữ config + endpoint + ref nội
// bộ tới process thật để có thể Stop sau này.
type DALChainProcess struct {
	Config    DALChainConfig
	Endpoints DALChainEndpoints
	proc      *devchain.Process // CHỮ THƯỜNG → không export ra ngoài (private).
}

// Stop kills the running chain process.
//
// VI: dừng tiến trình. Receiver `p *DALChainProcess` được nil-safe (check nil
// để gọi Stop nhiều lần không panic).
func (p *DALChainProcess) Stop() error {
	if p == nil || p.proc == nil {
		return nil // không có gì để dừng → no-op.
	}
	return p.proc.Stop()
}

// Validate checks that the config has all required fields.
//
// VI: kiểm tra config đầy đủ chưa. Chuyển sang struct internal rồi validate
// (logic validate ở 1 nơi duy nhất — internal/devchain).
func (c DALChainConfig) Validate() error {
	return dalConfigToInternal(c).Validate()
}

// DefaultDALChainConfig returns a DALChainConfig with sensible defaults.
//
// VI: trả config mặc định (chỉ cần truyền projectRoot). Caller có thể sửa
// field tuỳ ý sau đó.
func DefaultDALChainConfig(projectRoot string) DALChainConfig {
	dc := devchain.DefaultConfig(projectRoot)
	return dalConfigFromInternal(dc)
}

// StartDALChain launches a local DAL chain and waits for it to become healthy.
//
// VI: khởi động chain local. Blocks cho tới khi chain "healthy" (sẵn sàng
// nhận tx) hoặc lỗi/timeout. Caller PHẢI gọi proc.Stop() khi xong.
func StartDALChain(ctx context.Context, cfg DALChainConfig) (*DALChainProcess, error) {
	ic := dalConfigToInternal(cfg) // chuyển sang struct internal.

	proc, err := devchain.Start(ctx, ic) // khởi động tiến trình thật.
	if err != nil {
		return nil, err
	}

	// Đóng gói kết quả ra struct public.
	return &DALChainProcess{
		Config: cfg,
		Endpoints: DALChainEndpoints{
			SequencerRPC:     proc.Endpoints.SequencerRPC,
			FullNodeRPC:      proc.Endpoints.FullNodeRPC,
			SequencerExecAPI: proc.Endpoints.SequencerExecAPI,
			FullNodeExecAPI:  proc.Endpoints.FullNodeExecAPI,
		},
		proc: proc,
	}, nil
}

// dalConfigToInternal: chuyển DALChainConfig (public) sang devchain.Config (internal).
// VI: 2 struct cùng layout — phân tách public/internal để API public không
// bị ràng buộc với chi tiết cài đặt nội bộ.
func dalConfigToInternal(c DALChainConfig) devchain.Config {
	return devchain.Config{
		ProjectRoot:      c.ProjectRoot,
		ChainName:        c.ChainName,
		Namespace:        c.Namespace,
		DABridgeRPC:      c.DABridgeRPC,
		DAAuthToken:      c.DAAuthToken,
		CleanOnStart:     c.CleanOnStart,
		CleanOnExit:      c.CleanOnExit,
		LogLevel:         c.LogLevel,
		BlockTime:        c.BlockTime,
		SubmitInterval:   c.SubmitInterval,
		Stdout:           c.Stdout,
		Stderr:           c.Stderr,
		SequencerRPC:     c.SequencerRPC,
		FullNodeRPC:      c.FullNodeRPC,
		SequencerExecURL: c.SequencerExecURL,
		FullNodeExecURL:  c.FullNodeExecURL,
	}
}

// dalConfigFromInternal: ngược lại — devchain.Config → DALChainConfig.
func dalConfigFromInternal(dc devchain.Config) DALChainConfig {
	return DALChainConfig{
		ProjectRoot:      dc.ProjectRoot,
		ChainName:        dc.ChainName,
		Namespace:        dc.Namespace,
		DABridgeRPC:      dc.DABridgeRPC,
		DAAuthToken:      dc.DAAuthToken,
		CleanOnStart:     dc.CleanOnStart,
		CleanOnExit:      dc.CleanOnExit,
		LogLevel:         dc.LogLevel,
		BlockTime:        dc.BlockTime,
		SubmitInterval:   dc.SubmitInterval,
		Stdout:           dc.Stdout,
		Stderr:           dc.Stderr,
		SequencerRPC:     dc.SequencerRPC,
		FullNodeRPC:      dc.FullNodeRPC,
		SequencerExecURL: dc.SequencerExecURL,
		FullNodeExecURL:  dc.FullNodeExecURL,
	}
}
