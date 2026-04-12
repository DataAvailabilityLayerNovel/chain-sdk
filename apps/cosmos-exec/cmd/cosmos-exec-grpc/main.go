package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	db "github.com/cometbft/cometbft-db"
	"github.com/cometbft/cometbft/libs/log"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/app"
	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/executor"
	cosmoswasm "github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/sdk/cosmoswasm"
	execgrpc "github.com/DataAvailabilityLayerNovel/chain-sdk/execution/grpc"
)

func main() {
	listenAddr := flag.String("address", "0.0.0.0:50051", "gRPC listen address")
	home := flag.String("home", ".cosmos-exec-grpc", "home directory")
	inMemory := flag.Bool("in-memory", false, "Use in-memory DB (avoids file lock, non-persistent)")
	flag.Parse()

	if err := os.MkdirAll(*home, 0o755); err != nil {
		die("failed to create home directory", err)
	}

	database, err := openDatabase(filepath.Join(*home, "data"), *inMemory)
	if err != nil {
		die("failed to open database", err)
	}
	defer func() {
		_ = database.Close()
	}()

	logger := log.NewTMLogger(log.NewSyncWriter(os.Stdout))
	application := app.New(logger, database)
	cosmosExecutor := executor.New(application)
	handler := execgrpc.NewExecutorServiceHandlerWithMux(cosmosExecutor, func(mux *http.ServeMux) {
		mux.HandleFunc("/tx/submit", submitTxHandler(cosmosExecutor))
		mux.HandleFunc("/tx/result", txResultHandler(cosmosExecutor))
		mux.HandleFunc("/wasm/query-smart", querySmartHandler(cosmosExecutor))
		mux.HandleFunc("/blob/submit", blobSubmitHandler(cosmosExecutor))
		mux.HandleFunc("/blob/retrieve", blobRetrieveHandler(cosmosExecutor))
		mux.HandleFunc("/blob/batch", blobBatchHandler(cosmosExecutor))
		mux.HandleFunc("/blob/estimate-cost", blobEstimateCostHandler())
		mux.HandleFunc("/blocks/latest", blocksLatestHandler(cosmosExecutor))
		mux.HandleFunc("/blocks/{height}", blockByHeightHandler(cosmosExecutor))
		mux.HandleFunc("/status", statusHandler(cosmosExecutor))
		mux.HandleFunc("/tx/pending", txPendingHandler(cosmosExecutor))
		mux.HandleFunc("/tx/{hash}", txByHashHandler(cosmosExecutor))
		mux.HandleFunc("/swagger", swaggerUIHandler())
		mux.HandleFunc("/swagger.json", swaggerJSONHandler())
	})

	srv := &http.Server{
		Addr:    *listenAddr,
		Handler: handler,
	}

	fmt.Printf("cosmos-exec gRPC executor listening on %s\n", *listenAddr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		die("failed to start gRPC executor server", err)
	}
}

func openDatabase(dataDir string, inMemory bool) (db.DB, error) {
	if inMemory {
		return db.NewMemDB(), nil
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

type submitTxRequest struct {
	TxBase64 string `json:"tx_base64"`
	TxHex    string `json:"tx_hex"`
}

type submitTxResponse struct {
	Hash string `json:"hash"`
}

func submitTxHandler(exec *executor.CosmosExecutor) http.HandlerFunc {
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

type querySmartRequest struct {
	Contract string          `json:"contract"`
	Msg      json.RawMessage `json:"msg"`
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

		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to read body"})
			return
		}

		var req querySmartRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
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

type blobBatchRequest struct {
	BlobsBase64 []string `json:"blobs_base64"`
}

type blobBatchResponse struct {
	Root        string   `json:"root"`
	Commitments []string `json:"commitments"`
	Count       int      `json:"count"`
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

type estimateCostRequest struct {
	DataBytes   int     `json:"data_bytes"`
	GasPriceTIA float64 `json:"gas_price_tia,omitempty"`
	MaxBlobSize int     `json:"max_blob_size,omitempty"`
}

func blobEstimateCostHandler() http.HandlerFunc {
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

		var req estimateCostRequest
		if err := json.Unmarshal(body, &req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
			return
		}

		if req.DataBytes <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "data_bytes must be > 0"})
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
