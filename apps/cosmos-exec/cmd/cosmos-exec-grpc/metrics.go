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
//
// VI: bộ đếm metrics dùng atomic (không cần khoá mutex → an toàn khi nhiều
// goroutine HTTP cùng tăng). Phơi ra qua /metrics (text Prometheus) và
// /metrics.json (JSON).
type Metrics struct {
	requestCount  atomic.Int64
	errorCount    atomic.Int64
	txSubmitCount atomic.Int64
	queryCount    atomic.Int64
	startTime     time.Time // mốc khởi động, để tính uptime
}

func newMetrics() *Metrics {
	return &Metrics{startTime: time.Now()}
}

func (m *Metrics) incRequest()    { m.requestCount.Add(1) }
func (m *Metrics) incError()      { m.errorCount.Add(1) }
func (m *Metrics) incTxSubmit()   { m.txSubmitCount.Add(1) }
func (m *Metrics) incQuery()      { m.queryCount.Add(1) }

// metricsCountingMiddleware increments request/error counters.
//
// VI: middleware đếm tổng request và số lỗi. Bọc ResponseWriter bằng
// statusRecorder để "nghe lén" status code trả về (>=400 thì tính là lỗi).
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

// statusRecorder bọc http.ResponseWriter chỉ để ghi nhớ status code đã ghi
// (ResponseWriter chuẩn không cho đọc lại code đã set).
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader: lưu code rồi uỷ quyền cho ResponseWriter gốc.
func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}

// metricsHandler serves Prometheus-style text metrics.
//
// VI: trả metrics dạng text Prometheus. Mỗi chỉ số gồm 3 dòng: # HELP (mô tả),
// # TYPE (counter=tăng dần / gauge=giá trị tức thời), rồi dòng giá trị. Gộp số
// đếm HTTP (atomic) với thống kê từ executor (GetStats: mempool, blocks...).
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

		fmt.Fprintf(w, "# HELP cosmos_exec_queries_total Total smart query requests.\n")
		fmt.Fprintf(w, "# TYPE cosmos_exec_queries_total counter\n")
		fmt.Fprintf(w, "cosmos_exec_queries_total %d\n", m.queryCount.Load())

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
//
// VI: cùng số liệu nhưng trả JSON, cho consumer không đọc định dạng Prometheus
// (vd dashboard tự viết).
func metricsJSONHandler(exec *executor.CosmosExecutor, m *Metrics) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats := exec.GetStats()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"uptime_seconds":   time.Since(m.startTime).Seconds(),
			"requests_total":   m.requestCount.Load(),
			"errors_total":     m.errorCount.Load(),
			"tx_submits_total": m.txSubmitCount.Load(),
			"queries_total":    m.queryCount.Load(),
			"tx_results":       stats.TxResultCount,
			"blocks":           stats.BlockCount,
			"mempool_size":     stats.MempoolSize,
		})
	}
}
