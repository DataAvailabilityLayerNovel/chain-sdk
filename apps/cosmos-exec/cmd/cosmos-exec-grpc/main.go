package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cosmossdk.io/log"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/rs/zerolog"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/app"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/config"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/executor"
	execgrpc "github.com/DataAvailabilityLayerNovel/chain-sdk/execution/grpc"
)

func main() {
	profileStr := flag.String("profile", "dev", "Config profile: dev, test, prod")
	listenAddr := flag.String("address", "", "gRPC listen address (default from profile)")
	home := flag.String("home", "", "home directory (default from profile)")
	inMemory := flag.Bool("in-memory", false, "Use in-memory DB (avoids file lock, non-persistent)")
	logLevel := flag.String("log-level", "", "Log level: debug, info, error")
	flag.Parse()

	cfg := config.ForProfile(config.Profile(*profileStr))
	cfg.LoadFromEnv()

	// CLI flags override env/profile.
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

	if err := cfg.Validate(); err != nil {
		die("invalid config", err)
	}

	if cfg.Home != "" {
		if err := os.MkdirAll(cfg.Home, 0o755); err != nil {
			die("failed to create home directory", err)
		}
	}

	database, err := openDatabase(cfg.ResolveDataDir(), cfg.InMemory)
	if err != nil {
		die("failed to open database", err)
	}

	level, err := zerolog.ParseLevel(cfg.LogLevel)
	if err != nil || level == zerolog.NoLevel {
		level = zerolog.InfoLevel
	}
	logger := log.NewLogger(os.Stdout, log.LevelOption(level))
	application := app.New(logger, database)

	// Build executor options.
	opts := []executor.Option{
		executor.WithQueryGasMax(cfg.QueryGasMax),
	}

	// Optional treasury/faucet (approaches A+B, see docs/fee-economics.md).
	// Enabled only when COSMOS_EXEC_TREASURY_PRIVKEY_HEX is set.
	faucetCfg, err := loadFaucetConfig()
	if err != nil {
		logger.Error("faucet config invalid", "error", err)
		os.Exit(1)
	}
	if faucetCfg != nil {
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
	if !cfg.InMemory {
		cfg.PersistTxResults = true
	}
	var persistErr error
	if cfg.PersistTxResults {
		persistDir := cfg.ResolveDataDir()
		if persistDir != "" {
			opts = append(opts, executor.WithPersistence(persistDir, &persistErr))
			logger.Info("persistence enabled", "dir", persistDir)
		}
	}

	cosmosExecutor := executor.New(application, opts...)
	if persistErr != nil {
		logger.Error("persistence replay failed", "error", persistErr)
		os.Exit(1)
	}
	m := newMetrics()

	handler := execgrpc.NewExecutorServiceHandlerWithMux(cosmosExecutor, func(mux *http.ServeMux) {
		mux.HandleFunc("/tx/submit", withMetrics(submitTxHandler(cosmosExecutor), m, "tx_submit"))
		mux.HandleFunc("/tx/result", txResultHandler(cosmosExecutor))
		mux.HandleFunc("/tx/estimate", txEstimateHandler(cosmosExecutor))
		mux.HandleFunc("/tx/simulate", withMetrics(txSimulateHandler(cosmosExecutor), m, "tx_simulate"))
		mux.HandleFunc("/wasm/query-smart", withMetrics(querySmartHandler(cosmosExecutor), m, "query"))
		mux.HandleFunc("/blocks/latest", blocksLatestHandler(cosmosExecutor))
		mux.HandleFunc("/blocks/{height}", blockByHeightHandler(cosmosExecutor))
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
		mux.HandleFunc("/cosmos/bank/v1beta1/balances/{address}", lcdBalancesHandler(cosmosExecutor))
		mux.HandleFunc("/cosmos/bank/v1beta1/balances/{address}/by_denom", lcdBalanceByDenomHandler(cosmosExecutor))
		mux.HandleFunc("/exec/prune", execPruneHandler(cosmosExecutor))
		if faucetCfg != nil {
			mux.HandleFunc("/faucet", withMetrics(faucetHandler(cosmosExecutor, faucetCfg), m, "faucet"))
		}
		mux.HandleFunc("/swagger", swaggerUIHandler())
		mux.HandleFunc("/swagger.json", swaggerJSONHandler())
	})

	// Wrap with security middleware.
	secCfg := SecurityConfig{
		MaxRequestBodyBytes: cfg.MaxRequestBodyBytes,
		AuthToken:           cfg.AuthToken,
		CORSAllowOrigin:     cfg.CORSAllowOrigin,
		RateLimitRPS:        cfg.RateLimitRPS,
		ReadOnlyMode:        cfg.ReadOnlyMode,
	}
	wrappedHandler := securityMiddleware(handler, secCfg)

	// Wrap with metrics counting.
	if cfg.MetricsEnabled {
		wrappedHandler = metricsCountingMiddleware(wrappedHandler, m)
	}

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      wrappedHandler,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("cosmos-exec gRPC executor starting",
			"addr", cfg.ListenAddr,
			"profile", string(cfg.Profile),
			"in_memory", cfg.InMemory,
			"persist", cfg.PersistTxResults,
			"rate_limit", cfg.RateLimitRPS,
			"auth", cfg.AuthToken != "",
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			die("failed to start server", err)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "err", err)
	}

	// Close persistence store if executor has one.
	cosmosExecutor.Close()

	_ = database.Close()
	logger.Info("shutdown complete")
}

// withMetrics wraps a handler to increment the appropriate metric counter.
func withMetrics(next http.HandlerFunc, m *Metrics, kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch kind {
		case "tx_submit":
			m.incTxSubmit()
		case "blob_submit":
			m.incBlobSubmit()
		case "query":
			m.incQuery()
		}
		next(w, r)
	}
}

func openDatabase(dataDir string, inMemory bool) (dbm.DB, error) {
	if inMemory {
		return dbm.NewMemDB(), nil
	}

	if dataDir == "" {
		dataDir = ".cosmos-exec-grpc/data"
	}

	database, err := dbm.NewGoLevelDB("application", dataDir, nil)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "resource temporarily unavailable") {
			return nil, fmt.Errorf("database lock detected at %s (another cosmos-exec process may still be running). stop the other process or run with --in-memory: %w", dataDir, err)
		}
		return nil, err
	}

	return database, nil
}

func die(msg string, err error) {
	if err == nil {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "%s: %v\n", msg, err)
	os.Exit(1)
}

// ── Request/Response types ──────────────────────────────────────────────────

type submitTxRequest struct {
	TxBase64 string `json:"tx_base64"`
	TxHex    string `json:"tx_hex"`
}

type submitTxResponse struct {
	Hash string `json:"hash"`
}

type querySmartRequest struct {
	Contract string          `json:"contract"`
	Msg      json.RawMessage `json:"msg"`
}

// estimateRequest is one of three shapes:
//   - {tx_base64} or {tx_hex} + optional {gas}: estimate from raw tx + gas hint
//   - {hash}: look up an already-executed tx by hash
//   - {bytes, gas}: pure math, no tx required
//
// The handler picks whichever inputs are populated.
type estimateRequest struct {
	TxBase64 string `json:"tx_base64,omitempty"`
	TxHex    string `json:"tx_hex,omitempty"`
	Hash     string `json:"hash,omitempty"`
	Bytes    uint64 `json:"bytes,omitempty"`
	Gas      uint64 `json:"gas,omitempty"`
}

// ── Handlers ────────────────────────────────────────────────────────────────

const maxTxSize = 10 * 1024 * 1024 // 10 MB
const maxQueryMsgSize = 256 * 1024 // 256 KB
const maxHashLen = 128             // max hash hex length

func submitTxHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
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

		var req submitTxRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}

		txBytes, err := decodeTx(req.TxHex, req.TxBase64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		hash, err := exec.InjectTx(r.Context(), txBytes)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, submitTxResponse{Hash: hash})
	}
}

func txResultHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		hash := strings.TrimSpace(r.URL.Query().Get("hash"))
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
			writeJSON(w, http.StatusOK, map[string]any{"found": false})
			return
		}

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

		bytes := req.Bytes
		gas := req.Gas
		var lookupHash string

		switch {
		case strings.TrimSpace(req.Hash) != "":
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
		case req.TxBase64 != "" || req.TxHex != "":
			raw, err := decodeTx(req.TxHex, req.TxBase64)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
			bytes = uint64(len(raw))
			// gas stays as whatever caller provided — we can't know without
			// running the tx. Caller is expected to pass their fee tx's gas.
		}

		if bytes == 0 && gas == 0 {
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
func gasAdjustmentPermille() uint64 {
	v := strings.TrimSpace(os.Getenv("COSMOS_EXEC_GAS_ADJUSTMENT"))
	if v == "" {
		return 1300
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 1.0 {
		return 1300
	}
	return uint64(f * 1000)
}

// txSimulateHandler runs a (0-fee, dummy-signed) tx through the ante chain +
// msg handlers WITHOUT committing, and returns the real gas it consumes plus a
// suggested gas_limit (gas_used * COSMOS_EXEC_GAS_ADJUSTMENT) and the fee that
// gas_limit implies under the same policy the ante enforces. Clients call this
// before signing so the fee tracks the actual tx instead of a fixed constant.
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

		res, err := exec.SimulateTx(r.Context(), raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		permille := gasAdjustmentPermille()
		gasLimit := (res.GasUsed*permille + 999) / 1000
		if gasLimit == 0 {
			gasLimit = res.GasWanted
		}
		fee := feeForGas(gasLimit)
		feeDenom, feeAmount := "", "0"
		if !fee.IsZero() {
			feeDenom = fee[0].Denom
			feeAmount = fee[0].Amount.String()
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

func querySmartHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		var decoded any
		if err := json.Unmarshal(result, &decoded); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"data_raw": string(result)})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"data": decoded})
	}
}

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

func blockByHeightHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		heightStr := r.PathValue("height")
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
func lcdBalancesHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		addr := strings.TrimSpace(r.PathValue("address"))
		info, err := exec.GetBalance(r.Context(), addr, "")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"balances": info.Balances,
			"pagination": map[string]any{
				"next_key": nil,
				"total":    strconv.Itoa(len(info.Balances)),
			},
		})
	}
}

// lcdBalanceByDenomHandler mirrors the Cosmos LCD route
// GET /cosmos/bank/v1beta1/balances/{address}/by_denom?denom=<denom>.
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
			amount = "0"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"balance": map[string]string{"denom": denom, "amount": amount},
		})
	}
}

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
			writeJSON(w, http.StatusOK, map[string]any{
				"hash":   hash,
				"status": "pending",
				"found":  false,
			})
			return
		}

		status := "success"
		if result.Code != 0 {
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

func readyHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		status, _ := exec.GetStatus(r.Context())
		if !status.Initialized {
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

type rollbackRequest struct {
	TargetHeight uint64 `json:"target_height"`
}

func execRollbackHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
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

type pruneRequest struct {
	Height uint64 `json:"height"`
}

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

func decodeTx(hexInput, base64Input string) ([]byte, error) {
	hexInput = strings.TrimSpace(hexInput)
	if hexInput != "" {
		hexInput = strings.TrimPrefix(hexInput, "0x")
		hexInput = strings.TrimPrefix(hexInput, "0X")
		bz, err := hex.DecodeString(hexInput)
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
		bz, err := base64.StdEncoding.DecodeString(base64Input)
		if err != nil {
			return nil, fmt.Errorf("invalid tx_base64: %w", err)
		}
		if len(bz) == 0 {
			return nil, errors.New("tx cannot be empty")
		}
		return bz, nil
	}

	return nil, errors.New("tx_base64 or tx_hex is required")
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
