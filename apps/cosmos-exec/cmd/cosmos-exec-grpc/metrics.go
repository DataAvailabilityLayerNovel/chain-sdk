package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/DataAvailabilityLayerNovel/chain-sdk/apps/cosmos-exec/executor"
)

// Metrics tracks request counts and latencies in a lock-free manner.
// Exposed via /metrics in Prometheus-compatible text format.
type Metrics struct {
	requestCount    atomic.Int64
	errorCount      atomic.Int64
	txSubmitCount   atomic.Int64
	blobSubmitCount atomic.Int64
	queryCount      atomic.Int64
	startTime       time.Time
}

func newMetrics() *Metrics {
	return &Metrics{startTime: time.Now()}
}

func (m *Metrics) incRequest()    { m.requestCount.Add(1) }
func (m *Metrics) incError()      { m.errorCount.Add(1) }
func (m *Metrics) incTxSubmit()   { m.txSubmitCount.Add(1) }
func (m *Metrics) incBlobSubmit() { m.blobSubmitCount.Add(1) }
func (m *Metrics) incQuery()      { m.queryCount.Add(1) }

// metricsCountingMiddleware increments request/error counters.
func metricsCountingMiddleware(next http.Handler, m *Metrics) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.incRequest()
		rw := &statusRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(rw, r)
		if rw.statusCode >= 400 {
			m.incError()
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

// metricsHandler serves Prometheus-style text metrics.
func metricsHandler(exec *executor.CosmosExecutor, m *Metrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats := exec.GetStats()
		uptime := time.Since(m.startTime).Seconds()

		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprintf(w, "# HELP cosmos_exec_uptime_seconds Time since process start.\n")
		fmt.Fprintf(w, "# TYPE cosmos_exec_uptime_seconds gauge\n")
		fmt.Fprintf(w, "cosmos_exec_uptime_seconds %.2f\n", uptime)

		fmt.Fprintf(w, "# HELP cosmos_exec_requests_total Total HTTP requests.\n")
		fmt.Fprintf(w, "# TYPE cosmos_exec_requests_total counter\n")
		fmt.Fprintf(w, "cosmos_exec_requests_total %d\n", m.requestCount.Load())

		fmt.Fprintf(w, "# HELP cosmos_exec_errors_total Total HTTP errors (4xx/5xx).\n")
		fmt.Fprintf(w, "# TYPE cosmos_exec_errors_total counter\n")
		fmt.Fprintf(w, "cosmos_exec_errors_total %d\n", m.errorCount.Load())

		fmt.Fprintf(w, "# HELP cosmos_exec_tx_submits_total Total tx submit requests.\n")
		fmt.Fprintf(w, "# TYPE cosmos_exec_tx_submits_total counter\n")
		fmt.Fprintf(w, "cosmos_exec_tx_submits_total %d\n", m.txSubmitCount.Load())

		fmt.Fprintf(w, "# HELP cosmos_exec_blob_submits_total Total blob submit requests.\n")
		fmt.Fprintf(w, "# TYPE cosmos_exec_blob_submits_total counter\n")
		fmt.Fprintf(w, "cosmos_exec_blob_submits_total %d\n", m.blobSubmitCount.Load())

		fmt.Fprintf(w, "# HELP cosmos_exec_queries_total Total smart query requests.\n")
		fmt.Fprintf(w, "# TYPE cosmos_exec_queries_total counter\n")
		fmt.Fprintf(w, "cosmos_exec_queries_total %d\n", m.queryCount.Load())

		fmt.Fprintf(w, "# HELP cosmos_exec_blob_count Number of blobs in store.\n")
		fmt.Fprintf(w, "# TYPE cosmos_exec_blob_count gauge\n")
		fmt.Fprintf(w, "cosmos_exec_blob_count %d\n", stats.BlobCount)

		fmt.Fprintf(w, "# HELP cosmos_exec_blob_bytes_total Total blob bytes stored.\n")
		fmt.Fprintf(w, "# TYPE cosmos_exec_blob_bytes_total gauge\n")
		fmt.Fprintf(w, "cosmos_exec_blob_bytes_total %d\n", stats.BlobBytes)

		fmt.Fprintf(w, "# HELP cosmos_exec_tx_results_count Number of executed tx results.\n")
		fmt.Fprintf(w, "# TYPE cosmos_exec_tx_results_count gauge\n")
		fmt.Fprintf(w, "cosmos_exec_tx_results_count %d\n", stats.TxResultCount)

		fmt.Fprintf(w, "# HELP cosmos_exec_blocks_count Number of blocks.\n")
		fmt.Fprintf(w, "# TYPE cosmos_exec_blocks_count gauge\n")
		fmt.Fprintf(w, "cosmos_exec_blocks_count %d\n", stats.BlockCount)

		fmt.Fprintf(w, "# HELP cosmos_exec_mempool_size Pending transactions in mempool.\n")
		fmt.Fprintf(w, "# TYPE cosmos_exec_mempool_size gauge\n")
		fmt.Fprintf(w, "cosmos_exec_mempool_size %d\n", stats.MempoolSize)
	}
}

// metricsJSONHandler serves metrics as JSON (for non-Prometheus consumers).
func metricsJSONHandler(exec *executor.CosmosExecutor, m *Metrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats := exec.GetStats()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uptime_seconds":   time.Since(m.startTime).Seconds(),
			"requests_total":   m.requestCount.Load(),
			"errors_total":     m.errorCount.Load(),
			"tx_submits_total": m.txSubmitCount.Load(),
			"blob_submits":     m.blobSubmitCount.Load(),
			"queries_total":    m.queryCount.Load(),
			"blob_count":       stats.BlobCount,
			"blob_bytes":       stats.BlobBytes,
			"tx_results":       stats.TxResultCount,
			"blocks":           stats.BlockCount,
			"mempool_size":     stats.MempoolSize,
		})
	}
}
