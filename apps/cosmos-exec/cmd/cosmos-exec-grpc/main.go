// package main: file thực thi (binary). Hàm main() là điểm khởi động.
// Đây là server gRPC/HTTP bọc quanh executor — cung cấp REST endpoint cho
// ví/dapp/explorer tương tác với chain.
package main

import (
	"context"        // truyền tín hiệu huỷ/timeout & shutdown.
	"encoding/base64" // giải mã tx dạng base64.
	"encoding/hex"    // giải mã tx dạng hex.
	"encoding/json"   // mã hoá/giải mã JSON cho request/response.
	"errors"          // tạo & so sánh lỗi.
	"flag"            // parse cờ dòng lệnh (--profile, --address...).
	"fmt"             // định dạng chuỗi.
	"io"              // đọc body request (có giới hạn).
	"net/http"        // server HTTP, router, handler.
	"os"              // ENV, stdout/stderr, thoát chương trình, tạo thư mục.
	"os/signal"       // bắt tín hiệu hệ thống (Ctrl+C).
	"strconv"         // chuyển chuỗi <-> số.
	"strings"         // xử lý chuỗi.
	"syscall"         // hằng tín hiệu SIGINT/SIGTERM.
	"time"            // timeout shutdown.

	"cosmossdk.io/log"                    // logger Cosmos.
	dbm "github.com/cosmos/cosmos-db"     // interface database.
	"github.com/rs/zerolog"               // thư viện log (đặt level).

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/app"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/config"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/executor"
	execgrpc "github.com/DataAvailabilityLayerNovel/chain-sdk/execution/grpc" // server gRPC executor dựng sẵn.
)

// main: điểm vào chương trình. Trình tự: parse cờ → nạp config → mở DB →
// dựng app + executor → đăng ký route HTTP → chạy server → chờ tín hiệu tắt.
func main() {
	// flag.String/Bool: khai báo cờ CLI, trả về CON TRỎ tới giá trị.
	// Cú pháp: tên cờ, giá trị mặc định, mô tả.
	profileStr := flag.String("profile", "dev", "Config profile: dev, test, prod")
	listenAddr := flag.String("address", "", "gRPC listen address (default from profile)")
	home := flag.String("home", "", "home directory (default from profile)")
	inMemory := flag.Bool("in-memory", false, "Use in-memory DB (avoids file lock, non-persistent)")
	logLevel := flag.String("log-level", "", "Log level: debug, info, error")
	flag.Parse() // đọc os.Args và điền giá trị vào các con trỏ trên.

	// Nạp cấu hình theo profile rồi cho ENV ghi đè.
	cfg := config.ForProfile(config.Profile(*profileStr)) // *profileStr: lấy giá trị từ con trỏ.
	cfg.LoadFromEnv()

	// CLI flags override env/profile.
	// VI: cờ CLI ưu tiên cao nhất — nếu được truyền thì ghi đè config.
	if *listenAddr != "" {
		cfg.ListenAddr = *listenAddr
	}
	if *home != "" {
		cfg.Home = *home
	}
	if *inMemory {
		cfg.InMemory = true
	}
	if *logLevel != "" {
		cfg.LogLevel = *logLevel
	}

	if err := cfg.Validate(); err != nil { // kiểm tra config hợp lệ.
		die("invalid config", err) // die: in lỗi và thoát (exit code 1).
	}

	if cfg.Home != "" {
		// Tạo thư mục home (kể cả cha) nếu chưa có. 0o755 = quyền rwxr-xr-x.
		if err := os.MkdirAll(cfg.Home, 0o755); err != nil {
			die("failed to create home directory", err)
		}
	}

	// Mở database (LevelDB trên đĩa, hoặc in-memory).
	database, err := openDatabase(cfg.ResolveDataDir(), cfg.InMemory)
	if err != nil {
		die("failed to open database", err)
	}

	// Parse log level; lỗi hoặc "không có level" → mặc định Info.
	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil || level == zerolog.NoLevel {
		level = zerolog.InfoLevel
	}
	logger := log.NewLogger(os.Stdout, log.LevelOption(level)) // log ra stdout.
	application := app.New(logger, database)                   // dựng app blockchain.

	// Build executor options.
	// VI: bắt đầu danh sách option cho executor; ... để spread vào New() sau.
	opts := []executor.Option{
		executor.WithQueryGasMax(cfg.QueryGasMax),
	}

	// Optional treasury/faucet (approaches A+B, see docs/fee-economics.md).
	// Enabled only when COSMOS_EXEC_TREASURY_PRIVKEY_HEX is set.
	// VI: faucet (cấp tiền test) chỉ bật khi ENV private key treasury được set.
	faucetCfg, err := loadFaucetConfig()
	if err != nil {
		logger.Error("faucet config invalid", "error", err)
		os.Exit(1)
	}
	if faucetCfg != nil {
		// genesisOption: tạo option nạp số dư treasury vào genesis.
		genOpt, err := faucetCfg.genesisOption(application)
		if err != nil {
			logger.Error("faucet genesis build failed", "error", err)
			os.Exit(1)
		}
		opts = append(opts, genOpt)
		logger.Info("treasury/faucet enabled",
			"treasury", faucetCfg.treasury,
			"genesis_amount", faucetCfg.genesisAmt.String(),
			"payout", faucetCfg.payout.String())
	}

	// Enable persistence by default when not in-memory mode.
	// VI: không phải in-memory thì bật lưu đĩa mặc định.
	if !cfg.InMemory {
		cfg.PersistTxResults = true
	}
	var persistErr error // nơi WithPersistence ghi lỗi (option không trả error được).
	if cfg.PersistTxResults {
		persistDir := cfg.ResolveDataDir()
		if persistDir != "" {
			opts = append(opts, executor.WithPersistence(persistDir, &persistErr))
			logger.Info("persistence enabled", "dir", persistDir)
		}
	}

	cosmosExecutor := executor.New(application, opts...) // dựng executor với mọi option.
	if persistErr != nil {                               // kiểm tra lỗi replay sau khi New().
		logger.Error("persistence replay failed", "error", persistErr)
		os.Exit(1)
	}
	m := newMetrics() // bộ đếm metrics (số tx submit, query...).

	// NewExecutorServiceHandlerWithMux: dựng handler gRPC + cho phép gắn thêm
	// route HTTP REST qua callback nhận *http.ServeMux.
	handler := execgrpc.NewExecutorServiceHandlerWithMux(cosmosExecutor, func(mux *http.ServeMux) {
		// mux.HandleFunc("đường-dẫn", hàm-xử-lý): đăng ký route.
		// withMetrics(...): bọc handler để đếm số lần gọi.
		mux.HandleFunc("/tx/submit", withMetrics(submitTxHandler(cosmosExecutor), m, "tx_submit"))
		mux.HandleFunc("/tx/result", txResultHandler(cosmosExecutor))
		mux.HandleFunc("/tx/estimate", txEstimateHandler(cosmosExecutor))
		mux.HandleFunc("/tx/simulate", withMetrics(txSimulateHandler(cosmosExecutor), m, "tx_simulate"))
		mux.HandleFunc("/wasm/query-smart", withMetrics(querySmartHandler(cosmosExecutor), m, "query"))
		mux.HandleFunc("/blocks/latest", blocksLatestHandler(cosmosExecutor))
		mux.HandleFunc("/blocks/{height}", blockByHeightHandler(cosmosExecutor)) // {height}: tham số đường dẫn.
		mux.HandleFunc("/status", statusHandler(cosmosExecutor))
		mux.HandleFunc("/tx/pending", txPendingHandler(cosmosExecutor))
		mux.HandleFunc("/tx/{hash}", txByHashHandler(cosmosExecutor))
		mux.HandleFunc("/health", healthHandler(cosmosExecutor))
		mux.HandleFunc("/healthz", healthHandler(cosmosExecutor))
		mux.HandleFunc("/ready", readyHandler(cosmosExecutor))
		mux.HandleFunc("/metrics", metricsHandler(cosmosExecutor, m))
		mux.HandleFunc("/metrics.json", metricsJSONHandler(cosmosExecutor, m))
		mux.HandleFunc("/exec/height", execHeightHandler(cosmosExecutor))
		mux.HandleFunc("/exec/rollback", execRollbackHandler(cosmosExecutor))
		mux.HandleFunc("/auth/account/{address}", authAccountHandler(cosmosExecutor))
		mux.HandleFunc("/bank/balance/{address}", bankBalanceHandler(cosmosExecutor))
		// Cosmos-LCD-shaped aliases so Keplr (which queries the registered
		// chain's `rest` endpoint) can display the account balance.
		// VI: route giả lập LCD Cosmos để ví Keplr đọc được số dư.
		mux.HandleFunc("/cosmos/bank/v1beta1/balances/{address}", lcdBalancesHandler(cosmosExecutor))
		mux.HandleFunc("/cosmos/bank/v1beta1/balances/{address}/by_denom", lcdBalanceByDenomHandler(cosmosExecutor))
		mux.HandleFunc("/exec/prune", execPruneHandler(cosmosExecutor))
		if faucetCfg != nil { // chỉ mở route /faucet khi faucet bật.
			mux.HandleFunc("/faucet", withMetrics(faucetHandler(cosmosExecutor, faucetCfg), m, "faucet"))
		}
		mux.HandleFunc("/swagger", swaggerUIHandler())
		mux.HandleFunc("/swagger.json", swaggerJSONHandler())
	})

	// Wrap with security middleware.
	// VI: cấu hình bảo mật rồi bọc handler (giới hạn body, token, CORS, rate limit).
	secCfg := SecurityConfig{
		MaxRequestBodyBytes: cfg.MaxRequestBodyBytes,
		AuthToken:           cfg.AuthToken,
		CORSAllowOrigin:     cfg.CORSAllowOrigin,
		RateLimitRPS:        cfg.RateLimitRPS,
		ReadOnlyMode:        cfg.ReadOnlyMode,
	}
	wrappedHandler := securityMiddleware(handler, secCfg)

	// Wrap with metrics counting.
	if cfg.MetricsEnabled { // bọc thêm tầng đếm metrics nếu bật.
		wrappedHandler = metricsCountingMiddleware(wrappedHandler, m)
	}

	// Cấu hình HTTP server: địa chỉ, handler, các timeout.
	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      wrappedHandler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	// VI: tạo context tự huỷ khi nhận Ctrl+C (SIGINT) hoặc kill (SIGTERM).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop() // dọn dẹp đăng ký tín hiệu khi main kết thúc.

	// go func(){...}() : chạy server trong goroutine riêng (không chặn main).
	go func() {
		logger.Info("cosmos-exec gRPC executor starting",
			"addr", cfg.ListenAddr,
			"profile", string(cfg.Profile),
			"in_memory", cfg.InMemory,
			"persist", cfg.PersistTxResults,
			"rate_limit", cfg.RateLimitRPS,
			"auth", cfg.AuthToken != "",
		)
		// ListenAndServe chặn cho tới khi server dừng. ErrServerClosed là
		// "dừng bình thường" → không coi là lỗi.
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			die("failed to start server", err)
		}
	}()

	<-ctx.Done() // chặn main ở đây cho tới khi nhận tín hiệu tắt.
	logger.Info("shutting down gracefully...")

	// Cho tối đa 10s để server xử lý nốt request đang chạy rồi tắt.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "err", err)
	}

	// Close persistence store if executor has one.
	cosmosExecutor.Close() // đóng file lưu trữ.

	_ = database.Close() // đóng DB; `_ =` cố ý bỏ qua lỗi đóng.
	logger.Info("shutdown complete")
}

// withMetrics wraps a handler to increment the appropriate metric counter.
// VI: nhận 1 handler, trả handler mới — tăng bộ đếm tương ứng rồi gọi handler gốc.
func withMetrics(next http.HandlerFunc, m *Metrics, kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch kind { // switch theo loại để tăng đúng counter.
		case "tx_submit":
			m.incTxSubmit()
		case "blob_submit":
			m.incBlobSubmit()
		case "query":
			m.incQuery()
		}
		next(w, r) // gọi handler thật.
	}
}

// openDatabase: mở DB. inMemory=true → DB RAM (mất khi tắt). Trả (DB, error).
func openDatabase(dataDir string, inMemory bool) (dbm.DB, error) {
	if inMemory {
		return dbm.NewMemDB(), nil
	}

	if dataDir == "" {
		dataDir = ".cosmos-exec-grpc/data" // thư mục dữ liệu mặc định.
	}

	// NewGoLevelDB: mở LevelDB tên "application" tại dataDir.
	database, err := dbm.NewGoLevelDB("application", dataDir, nil)
	if err != nil {
		// Lỗi "resource temporarily unavailable" = DB đang bị tiến trình khác khoá.
		if strings.Contains(strings.ToLower(err.Error()), "resource temporarily unavailable") {
			return nil, fmt.Errorf("database lock detected at %s (another cosmos-exec process may still be running). stop the other process or run with --in-memory: %w", dataDir, err)
		}
		return nil, err
	}

	return database, nil
}

// die: in thông báo lỗi ra stderr và thoát chương trình với mã 1.
func die(msg string, err error) {
	if err == nil {
		fmt.Fprintln(os.Stderr, msg) // Fprintln: in vào stderr.
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "%s: %v\n", msg, err)
	os.Exit(1)
}

// ── Request/Response types ──────────────────────────────────────────────────
// VI: các struct mô tả thân (body) request/response JSON. Thẻ `json:"..."`
// quy định tên field khi parse/serialize.

type submitTxRequest struct {
	TxBase64 string `json:"tx_base64"` // tx mã hoá base64 (1 trong 2 cách).
	TxHex    string `json:"tx_hex"`    // tx mã hoá hex.
}

type submitTxResponse struct {
	Hash string `json:"hash"` // hash tx sau khi nhận vào mempool.
}

type querySmartRequest struct {
	Contract string          `json:"contract"` // địa chỉ hợp đồng.
	Msg      json.RawMessage `json:"msg"`      // RawMessage: giữ nguyên JSON thô, parse sau.
}

// estimateRequest is one of three shapes:
//   - {tx_base64} or {tx_hex} + optional {gas}: estimate from raw tx + gas hint
//   - {hash}: look up an already-executed tx by hash
//   - {bytes, gas}: pure math, no tx required
//
// The handler picks whichever inputs are populated.
//
// VI: 1 struct dùng cho 3 kiểu input ước lượng chi phí. omitempty: field
// vắng mặt nếu giá trị 0/rỗng.
type estimateRequest struct {
	TxBase64 string `json:"tx_base64,omitempty"`
	TxHex    string `json:"tx_hex,omitempty"`
	Hash     string `json:"hash,omitempty"`
	Bytes    uint64 `json:"bytes,omitempty"`
	Gas      uint64 `json:"gas,omitempty"`
}

// ── Handlers ────────────────────────────────────────────────────────────────

// Các hằng giới hạn kích thước đầu vào (chống lạm dụng/DOS).
const maxTxSize = 10 * 1024 * 1024 // 10 MB — kích thước tối đa của tx.
const maxQueryMsgSize = 256 * 1024 // 256 KB — kích thước tối đa msg query.
const maxHashLen = 128             // độ dài tối đa chuỗi hash hex.

// submitTxHandler: trả về handler cho POST /tx/submit — nhận tx, đẩy vào mempool.
// Mẫu chung: hàm nhận exec, trả về http.HandlerFunc (closure giữ exec).
//
// Call chain (gọi gì để submit tx):
//
//	HTTP POST /tx/submit
//	  body JSON: submitTxRequest { tx_hex | tx_base64 }    (line 293)
//	    │
//	    ├─► decodeTx(hex, base64)        → []byte           (line 1002)
//	    │     chọn hex hoặc base64, decode ra raw tx bytes.
//	    │
//	    └─► exec.InjectTx(ctx, txBytes) → (hash, err)       (executor.go:428)
//	          • copy txBytes
//	          • e.mempool = append(e.mempool, txCopy)       (chỉ enqueue, CHƯA execute)
//	          • return hashTx(txCopy)  = SHA-256 hex        (executor.go:957)
//
// Tx nằm trong mempool tới khi sequencer gọi GetTxs lúc đóng block tiếp theo
// (ev-node tick theo block_time, default ~2s); execute thật xảy ra trong
// CosmosExecutor.ExecuteTxs (executor.go ~line 376). Để chờ kết quả, client poll
// GET /tx/result?hash=... (txResultHandler bên dưới).
//
// Trả về:
//   - 200 OK  → submitTxResponse { hash: "<sha256-hex>" }  (line 298)
//   - 400     → { error: "..." }   body lỗi / decode lỗi / mempool reject
//   - 405     → { error: "method not allowed" } nếu không phải POST
//
// Lưu ý: 200 chỉ nghĩa là tx đã vào mempool, KHÔNG phải đã được execute.
// Code != 0 hoặc tx fail chỉ thấy được khi GetTxResult trả về.
func submitTxHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost { // chỉ chấp nhận POST.
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		// Đọc body nhưng giới hạn maxTxSize (LimitReader chặn body quá lớn).
		body, err := io.ReadAll(io.LimitReader(r.Body, maxTxSize))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
			return
		}

		var req submitTxRequest
		if err := json.Unmarshal(body, &req); err != nil { // parse JSON -> struct.
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}

		// decodeTx: giải mã tx từ hex hoặc base64 thành []byte.
		txBytes, err := decodeTx(req.TxHex, req.TxBase64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		// InjectTx: đưa tx vào mempool, trả về hash. r.Context(): ctx của request.
		hash, err := exec.InjectTx(r.Context(), txBytes)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, submitTxResponse{Hash: hash}) // trả 200 + hash.
	}
}

// txResultHandler: GET /tx/result?hash=... — tra kết quả tx + chi phí ước tính.
func txResultHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		// r.URL.Query().Get("hash"): lấy tham số query string ?hash=...
		hash := strings.TrimSpace(r.URL.Query().Get("hash"))
		if hash == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hash is required"})
			return
		}
		if len(hash) > maxHashLen {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hash too long"})
			return
		}

		// GetTxResult trả (kết quả, tìm-thấy?, error).
		result, found, err := exec.GetTxResult(r.Context(), hash)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		if !found {
			writeJSON(w, http.StatusOK, map[string]any{"found": false})
			return
		}

		// Tính chi phí (DA + gas) từ kích thước & gas đã dùng.
		cost := getCostPolicy().estimate(result.Bytes, result.GasUsed)
		writeJSON(w, http.StatusOK, map[string]any{
			"found":  true,
			"result": result,
			"cost":   cost,
		})
	}
}

// txEstimateHandler returns the simulated cost of a tx without charging the
// user. Useful for pre-broadcast cost preview and for re-running the math
// when the policy constants change. Accepts any of:
//   - tx_base64 / tx_hex (+ gas hint): bytes = len(raw), gas = caller's
//     wanted gas (since we haven't executed it yet)
//   - hash: looks up bytes + gas_used from the stored result
//   - bytes + gas: pure math, no inputs required
//
// VI: POST /tx/estimate — ước lượng chi phí KHÔNG thu tiền. Nhận 1 trong 3
// dạng input, handler tự chọn dạng nào có dữ liệu.
func txEstimateHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxTxSize))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
			return
		}
		var req estimateRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}

		bytes := req.Bytes // mặc định lấy từ input "bytes/gas".
		gas := req.Gas
		var lookupHash string

		// switch không điều kiện: chạy nhánh đầu tiên có case đúng.
		switch {
		case strings.TrimSpace(req.Hash) != "": // dạng {hash}: tra tx đã chạy.
			lookupHash = strings.TrimSpace(req.Hash)
			if len(lookupHash) > maxHashLen {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hash too long"})
				return
			}
			result, found, err := exec.GetTxResult(r.Context(), lookupHash)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			if !found {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "tx not found"})
				return
			}
			bytes = result.Bytes
			gas = result.GasUsed
		case req.TxBase64 != "" || req.TxHex != "": // dạng raw tx: chỉ biết kích thước.
			raw, err := decodeTx(req.TxHex, req.TxBase64)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			bytes = uint64(len(raw))
			// gas stays as whatever caller provided — we can't know without
			// running the tx. Caller is expected to pass their fee tx's gas.
			// VI: gas giữ nguyên giá trị caller gửi (chưa chạy nên không tự biết).
		}

		if bytes == 0 && gas == 0 { // không đủ input để ước lượng.
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "supply one of {tx_base64|tx_hex} + gas, {hash}, or {bytes, gas}",
			})
			return
		}

		cost := getCostPolicy().estimate(bytes, gas)
		writeJSON(w, http.StatusOK, cost)
	}
}

// gasAdjustmentPermille reads COSMOS_EXEC_GAS_ADJUSTMENT (float, default 1.3)
// as parts-per-1000 so gas_limit = ceil(gas_used * adj) stays integer-only.
//
// VI: đọc hệ số nhân gas từ ENV, trả về dạng phần-nghìn (1.3 -> 1300) để
// tính gas_limit chỉ bằng số nguyên (tránh số thực).
func gasAdjustmentPermille() uint64 {
	v := strings.TrimSpace(os.Getenv("COSMOS_EXEC_GAS_ADJUSTMENT"))
	if v == "" {
		return 1300 // mặc định 1.3.
	}
	f, err := strconv.ParseFloat(v, 64) // chuỗi -> float64.
	if err != nil || f < 1.0 {          // không hợp lệ hoặc < 1.0 → mặc định.
		return 1300
	}
	return uint64(f * 1000) // 1.3 -> 1300.
}

// txSimulateHandler runs a (0-fee, dummy-signed) tx through the ante chain +
// msg handlers WITHOUT committing, and returns the real gas it consumes plus a
// suggested gas_limit (gas_used * COSMOS_EXEC_GAS_ADJUSTMENT) and the fee that
// gas_limit implies under the same policy the ante enforces. Clients call this
// before signing so the fee tracks the actual tx instead of a fixed constant.
//
// VI: POST /tx/simulate — chạy thử tx (không commit) để biết gas thật, từ đó
// gợi ý gas_limit + phí. Client gọi trước khi ký để phí khớp tx thực.
func txSimulateHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxTxSize))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
			return
		}
		var req estimateRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}
		raw, err := decodeTx(req.TxHex, req.TxBase64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		res, err := exec.SimulateTx(r.Context(), raw) // chạy mô phỏng.
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		permille := gasAdjustmentPermille()
		// gas_limit = ceil(gas_used * permille / 1000). +999 để làm tròn lên.
		gasLimit := (res.GasUsed*permille + 999) / 1000
		if gasLimit == 0 {
			gasLimit = res.GasWanted // dự phòng nếu gas_used = 0.
		}
		fee := feeForGas(gasLimit) // tính phí tương ứng gas_limit.
		feeDenom, feeAmount := "", "0"
		if !fee.IsZero() {
			feeDenom = fee[0].Denom            // denom của coin phí đầu tiên.
			feeAmount = fee[0].Amount.String() // số lượng dạng chuỗi.
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"gas_used":   res.GasUsed,
			"gas_wanted": res.GasWanted,
			"gas_limit":  gasLimit,
			"fee":        fee,
			"fee_denom":  feeDenom,
			"fee_amount": feeAmount,
		})
	}
}

// querySmartHandler: POST /wasm/query-smart — truy vấn hợp đồng WASM.
func querySmartHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// defer recover(): nếu query gây panic thì biến thành lỗi 500
		// thay vì làm sập cả server.
		defer func() {
			if rec := recover(); rec != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("query panicked: %v", rec)})
			}
		}()

		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, maxQueryMsgSize))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
			return
		}

		var req querySmartRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}

		if req.Contract == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "contract is required"})
			return
		}
		if len(req.Msg) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "msg is required"})
			return
		}

		result, err := exec.QuerySmart(r.Context(), req.Contract, req.Msg)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		// Thử parse kết quả thành JSON. Nếu không phải JSON → trả chuỗi thô.
		var decoded any
		if err := json.Unmarshal(result, &decoded); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"data_raw": string(result)})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"data": decoded})
	}
}

// blocksLatestHandler: GET /blocks/latest — trả block mới nhất.
func blocksLatestHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		block, found, err := exec.GetLatestBlock(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if !found {
			writeJSON(w, http.StatusOK, map[string]any{"found": false})
			return
		}

		writeJSON(w, http.StatusOK, block)
	}
}

// blockByHeightHandler: GET /blocks/{height} — trả block tại 1 chiều cao.
func blockByHeightHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		// r.PathValue("height"): lấy giá trị từ pattern {height} trong route.
		heightStr := r.PathValue("height")
		// ParseUint(chuỗi, cơ số 10, 64-bit). Lỗi hoặc =0 → 400.
		height, err := strconv.ParseUint(heightStr, 10, 64)
		if err != nil || height == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid block height"})
			return
		}

		block, found, err := exec.GetBlock(r.Context(), height)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("block %d not found", height)})
			return
		}

		writeJSON(w, http.StatusOK, block)
	}
}

// authAccountHandler: GET /auth/account/{address} — thông tin account (để ký tx).
func authAccountHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		addr := strings.TrimSpace(r.PathValue("address"))
		if addr == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "address is required"})
			return
		}
		info, err := exec.GetAccountInfo(r.Context(), addr)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, info)
	}
}

// bankBalanceHandler returns the bank balances for an address. An optional
// ?denom= query (e.g. ?denom=ustake) breaks out that single denom's amount.
//
// VI: GET /bank/balance/{address} — số dư; ?denom= tách riêng 1 loại token.
func bankBalanceHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		addr := strings.TrimSpace(r.PathValue("address"))
		if addr == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "address is required"})
			return
		}
		info, err := exec.GetBalance(r.Context(), addr, r.URL.Query().Get("denom"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, info)
	}
}

// lcdBalancesHandler mirrors the Cosmos LCD route
// GET /cosmos/bank/v1beta1/balances/{address}
// so wallets (Keplr) pointed at this server's `rest` can read the balance.
//
// VI: nhái đúng định dạng response LCD Cosmos để ví Keplr hiểu được.
func lcdBalancesHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		addr := strings.TrimSpace(r.PathValue("address"))
		info, err := exec.GetBalance(r.Context(), addr, "") // "" = lấy tất cả denom.
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		// Định dạng response giống LCD: có "balances" + "pagination".
		writeJSON(w, http.StatusOK, map[string]any{
			"balances": info.Balances,
			"pagination": map[string]any{
				"next_key": nil,
				"total":    strconv.Itoa(len(info.Balances)), // số int -> chuỗi.
			},
		})
	}
}

// lcdBalanceByDenomHandler mirrors the Cosmos LCD route
// GET /cosmos/bank/v1beta1/balances/{address}/by_denom?denom=<denom>.
//
// VI: nhái route LCD lấy số dư theo 1 denom cụ thể.
func lcdBalanceByDenomHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		addr := strings.TrimSpace(r.PathValue("address"))
		denom := strings.TrimSpace(r.URL.Query().Get("denom"))
		info, err := exec.GetBalance(r.Context(), addr, denom)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		amount := info.Amount
		if amount == "" {
			amount = "0" // không có denom đó → trả "0".
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"balance": map[string]string{"denom": denom, "amount": amount},
		})
	}
}

// statusHandler: GET /status — trạng thái executor (height, chainID...).
func statusHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		status, err := exec.GetStatus(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, status)
	}
}

// txPendingHandler: GET /tx/pending — số tx đang chờ trong mempool.
func txPendingHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		count, err := exec.GetPendingTxCount(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"pending_count": count})
	}
}

// txByHashHandler: GET /tx/{hash} — chi tiết 1 tx; chưa có thì báo "pending".
func txByHashHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		hash := strings.TrimSpace(r.PathValue("hash"))
		if hash == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hash is required"})
			return
		}
		if len(hash) > maxHashLen {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "hash too long"})
			return
		}

		result, found, err := exec.GetTxResult(r.Context(), hash)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		if !found {
			// Chưa thấy kết quả → coi như còn chờ xử lý.
			writeJSON(w, http.StatusOK, map[string]any{
				"hash":   hash,
				"status": "pending",
				"found":  false,
			})
			return
		}

		status := "success"
		if result.Code != 0 { // Code != 0 nghĩa là tx thất bại.
			status = "failed"
		}

		cost := getCostPolicy().estimate(result.Bytes, result.GasUsed)
		writeJSON(w, http.StatusOK, map[string]any{
			"hash":       result.Hash,
			"status":     status,
			"found":      true,
			"height":     result.Height,
			"code":       result.Code,
			"log":        result.Log,
			"events":     result.Events,
			"gas_used":   result.GasUsed,
			"gas_wanted": result.GasWanted,
			"bytes":      result.Bytes,
			"cost":       cost,
		})
	}
}

// healthHandler: GET /health|/healthz — kiểm tra server còn sống + vài số liệu.
func healthHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		stats := exec.GetStats()
		writeJSON(w, http.StatusOK, map[string]any{
			"status":      "ok",
			"tx_count":    stats.TxResultCount,
			"block_count": stats.BlockCount,
		})
	}
}

// readyHandler: GET /ready — sẵn sàng phục vụ chưa (đã init chain chưa).
func readyHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		status, _ := exec.GetStatus(r.Context()) // _ : bỏ qua error ở đây.
		if !status.Initialized {
			// Chưa init → trả 503 (Service Unavailable).
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"ready":  false,
				"reason": "not initialized",
			})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"ready":            true,
			"latest_height":    status.LatestHeight,
			"finalized_height": status.FinalizedHeight,
		})
	}
}

// ── Executor optional interface handlers ────────────────────────────────────
// VI: các endpoint vận hành nâng cao (height, rollback, prune).

// execHeightHandler: GET /exec/height — chiều cao block hiện tại.
func execHeightHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		height, err := exec.GetLatestHeight(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"height": height})
	}
}

// rollbackRequest: thân request cho /exec/rollback.
type rollbackRequest struct {
	TargetHeight uint64 `json:"target_height"` // chiều cao muốn lùi về.
}

// execRollbackHandler: POST /exec/rollback — lùi state về target_height.
func execRollbackHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		// Body nhỏ → giới hạn 4096 byte.
		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
			return
		}

		var req rollbackRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}

		if req.TargetHeight == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target_height must be > 0"})
			return
		}

		if err := exec.Rollback(r.Context(), req.TargetHeight); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// pruneRequest: thân request cho /exec/prune.
type pruneRequest struct {
	Height uint64 `json:"height"` // xoá dữ liệu ở height <= giá trị này.
}

// execPruneHandler: POST /exec/prune — dọn tx/block cũ để tiết kiệm RAM.
func execPruneHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
			return
		}

		var req pruneRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}

		if err := exec.PruneExec(r.Context(), req.Height); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// ── Utilities ───────────────────────────────────────────────────────────────

// decodeTx: giải mã tx — ưu tiên hex, sau đó base64. Trả ([]byte, error).
func decodeTx(hexInput, base64Input string) ([]byte, error) {
	hexInput = strings.TrimSpace(hexInput)
	if hexInput != "" {
		// Bỏ tiền tố 0x/0X nếu có.
		hexInput = strings.TrimPrefix(hexInput, "0x")
		hexInput = strings.TrimPrefix(hexInput, "0X")
		bz, err := hex.DecodeString(hexInput) // hex -> bytes.
		if err != nil {
			return nil, fmt.Errorf("invalid tx_hex: %w", err)
		}
		if len(bz) == 0 {
			return nil, errors.New("tx cannot be empty")
		}
		return bz, nil
	}

	base64Input = strings.TrimSpace(base64Input)
	if base64Input != "" {
		bz, err := base64.StdEncoding.DecodeString(base64Input) // base64 -> bytes.
		if err != nil {
			return nil, fmt.Errorf("invalid tx_base64: %w", err)
		}
		if len(bz) == 0 {
			return nil, errors.New("tx cannot be empty")
		}
		return bz, nil
	}

	return nil, errors.New("tx_base64 or tx_hex is required") // không có input nào.
}

// writeJSON: tiện ích ghi response JSON. Set header, status code, encode payload.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json") // báo client đây là JSON.
	w.WriteHeader(status)                              // mã HTTP (200, 400...).
	// NewEncoder(w).Encode: serialize payload thẳng vào response.
	// `_ =` cố ý bỏ qua lỗi ghi (client có thể đã ngắt kết nối).
	_ = json.NewEncoder(w).Encode(payload)
}
