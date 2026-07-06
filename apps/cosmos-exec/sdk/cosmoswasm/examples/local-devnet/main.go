// Command local-devnet bootstraps a full local DAL chain (sequencer + full node
// + execution service) straight from Go using the SDK's dev tooling, waits for
// it to become healthy, runs a couple of read calls against it, then tears it
// down. It is the runnable counterpart of the "Dev Tooling" section in
// docs/api-reference.md and the only example that exercises StartDALChain.
//
// VI: Example này KHỞI ĐỘNG nguyên một chain local bằng Go (qua StartDALChain),
// chờ chain healthy, gọi vài API đọc, rồi tự dừng. Đây là caller thật của tiện
// ích dev `StartDALChain`/`DALChainConfig` (gói internal/devchain).
//
// Yêu cầu hạ tầng: một node DA (Celestia bridge) đang chạy — giống mọi thao tác
// dùng DA khác. Cấu hình qua biến môi trường:
//
//	PROJECT_ROOT   — gốc repo ev-node (mặc định: tự dò ngược lên tìm "justfile")
//	DA_BRIDGE_RPC  — URL RPC tới Celestia bridge (BẮT BUỘC)
//	DA_AUTH_TOKEN  — auth token DA (rỗng nếu node mở)
//	CHAIN_NAME     — tên chain (mặc định trong DefaultDALChainConfig)
//	WASM_FILE      — nếu set, example sẽ store thêm 1 WASM để minh hoạ tx thật
//
// Các cách chạy (chạy từ thư mục apps/cosmos-exec/sdk/cosmoswasm/):
//
//	# 1) Boot chain + đọc status, KHÔNG store WASM (mặc định):
//	DA_BRIDGE_RPC=http://localhost:26658 go run ./examples/local-devnet
//
//	# 2) Boot chain + store một WASM (chạy luôn BuildStoreTx/SubmitTxBytes/WaitTxResult):
//	DA_BRIDGE_RPC=http://localhost:26658 \
//	WASM_FILE=/path/to/contract.wasm \
//	go run ./examples/local-devnet
//
//	# 3) DA node yêu cầu auth token:
//	DA_BRIDGE_RPC=http://localhost:26658 \
//	DA_AUTH_TOKEN=$(celestia bridge auth admin) \
//	go run ./examples/local-devnet
//
//	# 4) Tự chỉ định gốc repo + tên chain (khi không tự dò được justfile):
//	PROJECT_ROOT=/abs/path/to/ev-node \
//	CHAIN_NAME=my-devnet \
//	DA_BRIDGE_RPC=http://localhost:26658 \
//	go run ./examples/local-devnet
//
//	# 5) Nạp cấu hình từ .env ở gốc repo (chỉ cần đặt DA_BRIDGE_RPC=... trong .env):
//	go run ./examples/local-devnet
package main

import (
	"context"       // truyền timeout/cancel cho lệnh start chain.
	"fmt"           // in ra console.
	"log"           // log + thoát khi lỗi nghiêm trọng.
	"os"            // đọc biến môi trường, đọc file wasm.
	"os/signal"     // bắt Ctrl+C (SIGINT) để shutdown êm.
	"path/filepath" // dò ngược lên tìm gốc repo.
	"strings"       // xử lý chuỗi.
	"syscall"       // SIGTERM.
	"time"          // BlockTime/SubmitInterval + timeout.

	"github.com/joho/godotenv" // nạp .env ở gốc repo (best-effort).

	cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
)

// main mỏng: mọi logic nằm trong run() để dùng defer (proc.Stop) cho chuẩn —
// nếu gọi log.Fatalf giữa chừng thì os.Exit sẽ BỎ QUA defer, chain không dừng.
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	loadDotEnv() // nạp ev-node/.env nếu có (không override env thật).

	// Bắt Ctrl+C / SIGTERM: lần đầu nhận signal sẽ HỦY baseCtx thay vì giết
	// tiến trình ngay, nhờ đó defer proc.Stop() bên dưới mới chạy được và kéo
	// theo runner + sequencer/full node/exec cùng dừng.
	baseCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	bridge := strings.TrimSpace(os.Getenv("DA_BRIDGE_RPC"))
	if bridge == "" {
		return fmt.Errorf("DA_BRIDGE_RPC is required (URL of a running Celestia bridge node)")
	}
	projectRoot := envOr("PROJECT_ROOT", findProjectRoot())
	if projectRoot == "" {
		return fmt.Errorf("cannot locate project root; set PROJECT_ROOT to the ev-node repo root")
	}

	// ──────────────────────────────────────────────────────────
	// 1. Dựng config: bắt đầu từ default, ghi đè vài field cần thiết.
	// ──────────────────────────────────────────────────────────
	// DefaultDALChainConfig đã set sẵn ChainName/Namespace/BlockTime hợp lý —
	// ta chỉ cần điền ProjectRoot (binary chạy ở đâu) và DA bridge.
	cfg := cosmoswasm.DefaultDALChainConfig(projectRoot)
	cfg.DABridgeRPC = bridge
	cfg.DAAuthToken = strings.TrimSpace(os.Getenv("DA_AUTH_TOKEN"))
	cfg.CleanOnStart = true // chain mới tinh mỗi lần chạy example.
	cfg.CleanOnExit = true  // dọn data khi thoát, không để lại rác.
	if v := strings.TrimSpace(os.Getenv("CHAIN_NAME")); v != "" {
		cfg.ChainName = v
	}
	// Pipe log của tiến trình con ra stderr để quan sát quá trình boot.
	cfg.Stdout = os.Stderr
	cfg.Stderr = os.Stderr

	// Validate trước khi tốn công start — bắt lỗi config sớm.
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid DAL chain config: %w", err)
	}
	fmt.Printf("Booting local devnet: chain=%s namespace=%s block_time=%s\n",
		cfg.ChainName, cfg.Namespace, cfg.BlockTime)

	// ──────────────────────────────────────────────────────────
	// 2. Khởi động chain. Block tới khi healthy hoặc ctx hết hạn.
	// ──────────────────────────────────────────────────────────
	// Cho 3 phút để boot (build/khởi tạo genesis/đợi DA) — chỉnh nếu máy chậm.
	// Kế thừa baseCtx → Ctrl+C lúc đang boot cũng hủy được lệnh start.
	startCtx, cancelStart := context.WithTimeout(baseCtx, 3*time.Minute)
	defer cancelStart()

	proc, err := cosmoswasm.StartDALChain(startCtx, cfg)
	if err != nil {
		return fmt.Errorf("start DAL chain: %w", err)
	}
	// BẮT BUỘC: dừng tiến trình khi main kết thúc, kẻo để lại node mồ côi.
	defer func() {
		fmt.Println("\nStopping local devnet…")
		if serr := proc.Stop(); serr != nil {
			log.Printf("stop chain: %v", serr)
		}
	}()

	fmt.Println("Chain is healthy. Endpoints:")
	fmt.Printf("  sequencer RPC : %s\n", proc.Endpoints.SequencerRPC)
	fmt.Printf("  full-node RPC : %s\n", proc.Endpoints.FullNodeRPC)
	fmt.Printf("  exec API      : %s\n", proc.Endpoints.SequencerExecAPI)

	// ──────────────────────────────────────────────────────────
	// 3. Kết nối client SDK tới exec API vừa khởi động và đọc trạng thái.
	// ──────────────────────────────────────────────────────────
	client := cosmoswasm.NewClient(proc.Endpoints.SequencerExecAPI)

	ctx, cancel := context.WithTimeout(baseCtx, 30*time.Second)
	defer cancel()

	status, err := client.Status(ctx)
	if err != nil {
		return fmt.Errorf("query status: %w", err)
	}
	fmt.Printf("\nStatus: chain_id=%s height=%d finalized=%d synced=%t\n",
		status.ChainID, status.LatestHeight, status.FinalizedHeight, status.Synced)

	// ──────────────────────────────────────────────────────────
	// 4. (Tuỳ chọn) store một WASM để chứng minh chain nhận tx thật.
	// ──────────────────────────────────────────────────────────
	wasmFile := strings.TrimSpace(os.Getenv("WASM_FILE"))
	if wasmFile == "" {
		fmt.Println("\n(set WASM_FILE để thử store code; bỏ qua bước này)")
		fmt.Println("\nDone.")
		return nil
	}

	wasmBytes, err := os.ReadFile(wasmFile)
	if err != nil {
		return fmt.Errorf("read wasm %q: %w", wasmFile, err)
	}
	storeTx, err := cosmoswasm.BuildStoreTx(wasmBytes, cosmoswasm.DefaultSender())
	if err != nil {
		return fmt.Errorf("build store tx: %w", err)
	}
	sub, err := client.SubmitTxBytes(ctx, storeTx)
	if err != nil {
		return fmt.Errorf("submit store tx: %w", err)
	}
	res, err := client.WaitTxResult(ctx, sub.Hash, cosmoswasm.DefaultPollInterval)
	if err != nil {
		return fmt.Errorf("wait store result: %w", err)
	}
	fmt.Printf("\nStored WASM: tx=%s code=%d height=%d\n", sub.Hash, res.Code, res.Height)
	fmt.Println("\nDone.")
	return nil
}

// envOr trả giá trị env hoặc fallback nếu rỗng.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// findProjectRoot dò ngược từ thư mục hiện tại lên gốc filesystem, trả thư mục
// đầu tiên chứa "justfile" (đặc trưng của gốc repo ev-node). Rỗng nếu không thấy.
func findProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "justfile")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // tới gốc filesystem, không tìm thấy.
		}
		dir = parent
	}
}

// loadDotEnv nạp .env gần nhất tìm được khi đi ngược lên từ thư mục hiện tại.
// Best-effort: thiếu .env không phải lỗi; không override env đã set thật.
func loadDotEnv() {
	dir, err := os.Getwd()
	if err != nil {
		return
	}
	for {
		candidate := filepath.Join(dir, ".env")
		if _, statErr := os.Stat(candidate); statErr == nil {
			_ = godotenv.Load(candidate)
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}
