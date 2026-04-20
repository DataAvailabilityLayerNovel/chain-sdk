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

	db "github.com/cometbft/cometbft-db"
	"github.com/cometbft/cometbft/libs/log"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/app"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/config"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/executor"
	cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
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

	logger := log.NewTMLogger(log.NewSyncWriter(os.Stdout))
	application := app.New(logger, database)

	// Build executor options.
	opts := []executor.Option{
		executor.WithQueryGasMax(cfg.QueryGasMax),
		executor.WithBlobStoreLimits(cfg.MaxBlobSize, cfg.MaxStoreTotalSize),
	}

	// Enable persistence by default when not in-memory mode.
	if !cfg.InMemory {
		cfg.PersistBlobs = true
		cfg.PersistTxResults = true
	}
	var persistErr error
	if cfg.PersistBlobs || cfg.PersistTxResults {
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
		mux.HandleFunc("/wasm/query-smart", withMetrics(querySmartHandler(cosmosExecutor), m, "query"))
		mux.HandleFunc("/blob/submit", withMetrics(blobSubmitHandler(cosmosExecutor), m, "blob_submit"))
		mux.HandleFunc("/blob/retrieve", blobRetrieveHandler(cosmosExecutor))
		mux.HandleFunc("/blob/batch", withMetrics(blobBatchHandler(cosmosExecutor), m, "blob_submit"))
		mux.HandleFunc("/blob/estimate-cost", blobEstimateCostHandler())
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
			"persist", cfg.PersistBlobs || cfg.PersistTxResults,
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

func openDatabase(dataDir string, inMemory bool) (db.DB, error) {
	if inMemory {
		return db.NewMemDB(), nil
	}

	if dataDir == "" {
		dataDir = ".cosmos-exec-grpc/data"
	}

	database, err := db.NewGoLevelDB("application", dataDir)
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

type blobSubmitRequest struct {
	DataBase64 string `json:"data_base64"`
}

type blobSubmitResponse struct {
	Commitment string `json:"commitment"`
	Size       int    `json:"size"`
}

type blobRetrieveResponse struct {
	Commitment string `json:"commitment"`
	DataBase64 string `json:"data_base64"`
	Size       int    `json:"size"`
}

type blobBatchRequest struct {
	BlobsBase64 []string `json:"blobs_base64"`
}

type blobBatchResponse struct {
	Root        string   `json:"root"`
	Commitments []string `json:"commitments"`
	Count       int      `json:"count"`
}

type estimateCostRequest struct {
	DataBytes   int     `json:"data_bytes"`
	GasPriceTIA float64 `json:"gas_price_tia,omitempty"`
	MaxBlobSize int     `json:"max_blob_size,omitempty"`
}

// ── Handlers ────────────────────────────────────────────────────────────────

const maxTxSize = 10 * 1024 * 1024   // 10 MB
const maxBlobBatchSize = 100          // max blobs per batch
const maxQueryMsgSize = 256 * 1024    // 256 KB
const maxHashLen = 128                // max hash hex length

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

		writeJSON(w, http.StatusOK, map[string]any{"found": true, "result": result})
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

func blobSubmitHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
			return
		}

		var req blobSubmitRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}

		req.DataBase64 = strings.TrimSpace(req.DataBase64)
		if req.DataBase64 == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "data_base64 is required"})
			return
		}

		data, err := base64.StdEncoding.DecodeString(req.DataBase64)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid base64: " + err.Error()})
			return
		}

		commitment, err := exec.StoreBlob(r.Context(), data)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, blobSubmitResponse{
			Commitment: commitment,
			Size:       len(data),
		})
	}
}

func blobBatchHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
			return
		}

		var req blobBatchRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}

		if len(req.BlobsBase64) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "blobs_base64 is required and must not be empty"})
			return
		}
		if len(req.BlobsBase64) > maxBlobBatchSize {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("batch too large: max %d blobs", maxBlobBatchSize)})
			return
		}

		blobs := make([][]byte, 0, len(req.BlobsBase64))
		for i, b64 := range req.BlobsBase64 {
			data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid base64 at index %d: %v", i, err)})
				return
			}
			blobs = append(blobs, data)
		}

		root, commitments, err := exec.StoreBatch(r.Context(), blobs)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, blobBatchResponse{
			Root:        root,
			Commitments: commitments,
			Count:       len(commitments),
		})
	}
}

func blobRetrieveHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		commitment := strings.TrimSpace(r.URL.Query().Get("commitment"))
		if commitment == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "commitment is required"})
			return
		}
		if len(commitment) > 128 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "commitment too long"})
			return
		}

		data, err := exec.RetrieveBlob(r.Context(), commitment)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, blobRetrieveResponse{
			Commitment: commitment,
			DataBase64: base64.StdEncoding.EncodeToString(data),
			Size:       len(data),
		})
	}
}

func blobEstimateCostHandler() http.HandlerFunc {
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

		var req estimateCostRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}

		if req.DataBytes <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "data_bytes must be > 0"})
			return
		}
		if req.DataBytes > 100*1024*1024 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "data_bytes exceeds 100 MB limit"})
			return
		}

		est := cosmoswasm.EstimateCost(cosmoswasm.EstimateCostRequest{
			DataBytes:   req.DataBytes,
			GasPriceTIA: req.GasPriceTIA,
			MaxBlobSize: req.MaxBlobSize,
		})

		writeJSON(w, http.StatusOK, est)
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

		writeJSON(w, http.StatusOK, map[string]any{
			"hash":   result.Hash,
			"status": status,
			"found":  true,
			"height": result.Height,
			"code":   result.Code,
			"log":    result.Log,
			"events": result.Events,
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
			"blob_count":  stats.BlobCount,
			"blob_bytes":  stats.BlobBytes,
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
