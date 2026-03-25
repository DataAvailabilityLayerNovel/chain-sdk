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
	"strings"

	db "github.com/cometbft/cometbft-db"
	"github.com/cometbft/cometbft/libs/log"

	"github.com/evstack/ev-node/apps/cosmos-exec/app"
	"github.com/evstack/ev-node/apps/cosmos-exec/executor"
	execgrpc "github.com/evstack/ev-node/execution/grpc"
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

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
