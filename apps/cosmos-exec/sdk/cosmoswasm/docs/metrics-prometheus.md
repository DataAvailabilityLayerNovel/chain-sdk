# Metrics Prometheus của cosmos-exec

Tài liệu này mô tả **cách cosmos-exec phơi metrics**, **danh sách từng chỉ số**, **cách bật/tắt** và **cách scrape bằng Prometheus** — bám đúng `cmd/cosmos-exec-grpc/metrics.go`, `cmd/cosmos-exec-grpc/main.go` và `config/config.go`.

> Liên quan: [profiles-and-security.md](profiles-and-security.md) (profile dev/test/prod), [configuration.md](configuration.md) (biến cấu hình), [node-operations.md](node-operations.md) (vận hành stack), [api-reference.md](api-reference.md) (toàn bộ endpoint).

## 1. Hai endpoint phơi metrics

cosmos-exec không nhúng SDK `client_golang`; nó **tự sinh text Prometheus** bằng `fmt.Fprintf` để tránh thêm phụ thuộc nặng. Có hai endpoint, luôn được đăng ký trong mux ([main.go:156-157](../../../cmd/cosmos-exec-grpc/main.go#L156-L157)):

| Endpoint | Content-Type | Dùng cho |
|----------|--------------|----------|
| `GET /metrics` | `text/plain; version=0.0.4` | Prometheus scrape (định dạng text chuẩn) |
| `GET /metrics.json` | `application/json` | Consumer không đọc Prometheus (dashboard tự viết, script) |

Cả hai trả **cùng số liệu**, chỉ khác định dạng. Endpoint nằm chung cổng HTTP với API (mặc định `0.0.0.0:50051`, xem `ListenAddr` trong `config/config.go`), không phải cổng riêng.

## 2. Cách thu thập số liệu

Số liệu đến từ hai nguồn, gộp lại khi có request `/metrics`:

```
┌─ Bộ đếm HTTP (atomic, trong metrics.go) ──────────────┐
│ requestCount   ← metricsCountingMiddleware (mọi route) │
│ errorCount     ← middleware, khi status >= 400         │
│ txSubmitCount  ← withMetrics("tx_submit") /tx/submit   │
│ queryCount     ← withMetrics("query") /wasm/query-smart│
│ startTime      ← lúc newMetrics()                      │
└────────────────────────────────────────────────────────┘
┌─ Thống kê executor (exec.GetStats(), đọc tức thời) ───┐
│ TxResultCount │ BlockCount │ MempoolSize               │
└────────────────────────────────────────────────────────┘
```

- Bộ đếm HTTP dùng `atomic.Int64` → **không cần mutex**, an toàn khi nhiều goroutine cùng tăng.
- `errorCount` đếm được nhờ `statusRecorder` bọc `http.ResponseWriter` để "nghe lén" status code (ResponseWriter chuẩn không cho đọc lại code đã set).
- Gauge (`tx_results`, `blocks`, `mempool_size`) **không phải counter tích luỹ**; chúng đọc trạng thái hiện hành của executor mỗi lần scrape.

## 3. Danh sách metrics

| Tên metric | Type | Ý nghĩa | Nguồn |
|------------|------|---------|-------|
| `cosmos_exec_uptime_seconds` | gauge | Thời gian từ lúc tiến trình khởi động | `time.Since(startTime)` |
| `cosmos_exec_requests_total` | counter | Tổng số HTTP request | middleware |
| `cosmos_exec_errors_total` | counter | Tổng số request lỗi (4xx/5xx) | middleware |
| `cosmos_exec_tx_submits_total` | counter | Tổng số lần gọi `/tx/submit` | `withMetrics` |
| `cosmos_exec_queries_total` | counter | Tổng số lần gọi `/wasm/query-smart` | `withMetrics` |
| `cosmos_exec_tx_results_count` | gauge | Số tx result đã thực thi (đang giữ) | `exec.GetStats()` |
| `cosmos_exec_blocks_count` | gauge | Số block hiện có | `exec.GetStats()` |
| `cosmos_exec_mempool_size` | gauge | Số giao dịch đang chờ trong mempool | `exec.GetStats()` |

Ví dụ output `/metrics`:

```
# HELP cosmos_exec_uptime_seconds Time since process start.
# TYPE cosmos_exec_uptime_seconds gauge
cosmos_exec_uptime_seconds 312.48
# HELP cosmos_exec_requests_total Total HTTP requests.
# TYPE cosmos_exec_requests_total counter
cosmos_exec_requests_total 1042
# HELP cosmos_exec_errors_total Total HTTP errors (4xx/5xx).
# TYPE cosmos_exec_errors_total counter
cosmos_exec_errors_total 7
# HELP cosmos_exec_tx_submits_total Total tx submit requests.
# TYPE cosmos_exec_tx_submits_total counter
cosmos_exec_tx_submits_total 138
# HELP cosmos_exec_queries_total Total smart query requests.
# TYPE cosmos_exec_queries_total counter
cosmos_exec_queries_total 521
# HELP cosmos_exec_tx_results_count Number of executed tx results.
# TYPE cosmos_exec_tx_results_count gauge
cosmos_exec_tx_results_count 138
# HELP cosmos_exec_blocks_count Number of blocks.
# TYPE cosmos_exec_blocks_count gauge
cosmos_exec_blocks_count 156
# HELP cosmos_exec_mempool_size Pending transactions in mempool.
# TYPE cosmos_exec_mempool_size gauge
cosmos_exec_mempool_size 0
```

Ví dụ output `/metrics.json`:

```json
{
  "uptime_seconds": 312.48,
  "requests_total": 1042,
  "errors_total": 7,
  "tx_submits_total": 138,
  "queries_total": 521,
  "tx_results": 138,
  "blocks": 156,
  "mempool_size": 0
}
```

## 4. Bật/tắt metrics

Cờ cấu hình là `MetricsEnabled` ([config/config.go:51](../../../config/config.go#L51)). Mặc định:

| Profile | `MetricsEnabled` |
|---------|------------------|
| dev | `false` |
| test | `false` |
| prod | `true` |

Bật bằng biến môi trường:

```bash
COSMOS_EXEC_METRICS=true ./cosmos-exec-grpc --profile dev
```

> **Lưu ý quan trọng về phạm vi cờ:** `MetricsEnabled` chỉ điều khiển `metricsCountingMiddleware` — tức hai counter `requests_total` và `errors_total`. Hai endpoint `/metrics`, `/metrics.json` cùng các counter `tx_submits_total`, `queries_total` (qua `withMetrics`) **luôn hoạt động** kể cả khi cờ tắt. Khi cờ tắt, `requests_total`/`errors_total` sẽ đứng yên ở 0 còn các chỉ số khác vẫn cập nhật bình thường.

`MetricsAddr` (mặc định `127.0.0.1:9090`) có sẵn trong config nhưng hiện metrics dùng chung cổng HTTP API; trường này dành cho khả năng tách cổng về sau.

## 5. Scrape bằng Prometheus

Cấu hình `prometheus.yml` tối thiểu:

```yaml
scrape_configs:
  - job_name: cosmos-exec
    metrics_path: /metrics
    static_configs:
      - targets: ["127.0.0.1:50051"]
```

Nếu bật `AuthToken`, securityMiddleware yêu cầu header `Authorization: Bearer <token>` cho mọi route — thêm vào scrape config:

```yaml
    authorization:
      type: Bearer
      credentials: "<COSMOS_EXEC_AUTH_TOKEN>"
```

## 6. Một số truy vấn PromQL gợi ý

```promql
# Tỉ lệ lỗi theo phút
rate(cosmos_exec_errors_total[1m]) / rate(cosmos_exec_requests_total[1m])

# Throughput tx submit (tx/giây)
rate(cosmos_exec_tx_submits_total[5m])

# Cảnh báo mempool ùn tắc
cosmos_exec_mempool_size > 100

# Tốc độ sản xuất block
rate(cosmos_exec_blocks_count[5m])
```

## 7. Kiểm thử

Logic metrics có unit test ở [cmd/cosmos-exec-grpc/metrics_test.go](../../../cmd/cosmos-exec-grpc/metrics_test.go): kiểm tra định dạng text `/metrics`, JSON `/metrics.json`, và việc middleware đếm đúng request/error. Chạy:

```bash
cd apps/cosmos-exec
go test ./cmd/cosmos-exec-grpc/ -run Metrics -v
```
