# Lớp giao tiếp: HTTP/REST vs gRPC

Tài liệu này giải thích **đang dùng giao thức gì** để client nói chuyện với executor, và **vì sao chọn HTTP/REST thay vì gRPC thuần**. Đây là câu hỏi hay gặp vì thư mục server tên là `cosmos-exec-grpc` nhưng thứ client thực sự gọi lại là HTTP/REST.

## TL;DR

- Hệ thống dùng mô hình **lai**: bên dưới là `connectrpc` (gRPC + JSON trên *cùng một* endpoint HTTP/2), bên trên gắn thêm các route **REST/HTTP + JSON** thủ công.
- **Client SDK** (`sdk/cosmoswasm`) và **dApp web** đều gọi qua **HTTP/REST + JSON** (`POST /tx/submit`, `GET /blocks/latest`, …) — không gọi gRPC trực tiếp.
- gRPC vẫn còn đó qua connectrpc + reflection, dùng cho tooling backend (grpcurl, Postman) và để tương thích interface `core/execution`. Tên `cosmos-exec-grpc` là **di sản lịch sử**, không phản ánh giao thức client dùng.
- Lý do chọn HTTP/REST: gọi được từ **trình duyệt** không cần proxy, **không phụ thuộc ngôn ngữ**, dễ test bằng `curl`, dễ đặt sau **proxy/CORS**.

## Đang dùng gì — bức tranh thật

```
┌────────────────────────────────────────────┐
│  Client SDK (Go)  +  dApp web (browser)     │
└───────────────┬────────────────────────────┘
                │  HTTP/REST + JSON
                │  POST /tx/submit, GET /blocks/latest, /wasm/query-smart …
                ▼
┌────────────────────────────────────────────────────────────┐
│  cosmos-exec-grpc   (cmd/cosmos-exec-grpc/main.go)          │
│  http.ServeMux trên 1 cổng, phục vụ đồng thời:             │
│   • route REST thủ công  → /tx/submit, /blocks/{height} …  │
│   • service connectrpc   → /evnode.v1.ExecutorService/…    │
│   • gRPC reflection       (grpcurl/Postman tự khám phá)    │
│  Bọc bằng h2c → HTTP/1.1 + HTTP/2 cleartext, không cần TLS  │
└───────────────┬────────────────────────────────────────────┘
                │ uses
                ▼
        CosmosExecutor (executor/) → App (Cosmos SDK)
```

Cụ thể trong code:

- [`execution/grpc/handler.go`](../../../../execution/grpc/handler.go) dựng handler bằng **`connectrpc.com/connect`**. connect-go cho phép *một* endpoint phục vụ cùng lúc gRPC, gRPC-Web và JSON; bật thêm `grpcreflect` để tool debug "soi" được service.
- Handler được bọc bằng **`h2c`** (`golang.org/x/net/http2/h2c`) để chạy HTTP/2 *cleartext* — gRPC cần HTTP/2 nhưng môi trường dev/staging thường không có TLS.
- [`cmd/cosmos-exec-grpc/main.go`](../../../cmd/cosmos-exec-grpc/main.go) gọi `NewExecutorServiceHandlerWithMux(...)` và truyền callback đăng ký các route **REST** thủ công lên cùng `http.ServeMux`:

  ```go
  handler := execgrpc.NewExecutorServiceHandlerWithMux(cosmosExecutor, func(mux *http.ServeMux) {
      mux.HandleFunc("/tx/submit",        submitTxHandler(cosmosExecutor))
      mux.HandleFunc("/tx/result",        txResultHandler(cosmosExecutor))
      mux.HandleFunc("/wasm/query-smart", querySmartHandler(cosmosExecutor))
      mux.HandleFunc("/blocks/latest",    blocksLatestHandler(cosmosExecutor))
      mux.HandleFunc("/blocks/{height}",  blockByHeightHandler(cosmosExecutor))
      // … /status, /tx/pending, /auth/account/{address}, /bank/balance/{address}, /faucet, /swagger …
  })
  ```

- Client SDK ([`sdk/cosmoswasm`](../client.go)) gửi `POST`/`GET` JSON tới các route REST này — không import stub gRPC.

> **Vì sao đặt tên `-grpc`?** Interface thực thi gốc (`core/execution.Executor`) được định nghĩa bằng proto và phục vụ qua connect-go (gRPC). Lớp REST là phần thêm vào sau để phục vụ web. Tên thư mục giữ theo interface nền, không theo giao thức client.

## Vì sao HTTP/REST thay vì gRPC thuần

| Tiêu chí | gRPC thuần | HTTP/REST + JSON (đang dùng) |
|----------|-----------|------------------------------|
| Gọi từ trình duyệt | Không trực tiếp — cần gRPC-Web + proxy (Envoy) vì gRPC dựa vào HTTP/2 trailers | `fetch`/`axios` gọi thẳng; Keplr ký xong `POST` luôn |
| Phụ thuộc ngôn ngữ | Cần sinh stub cho mỗi ngôn ngữ | Không — bất kỳ HTTP client nào cũng gọi được |
| Test thủ công | Cần `grpcurl`/Postman + file proto | `curl` một dòng là xong |
| Proxy / CORS / Next.js rewrite | Phức tạp | Tự nhiên với reverse proxy |
| Hiệu năng/streaming | Tốt hơn cho RPC nội bộ, payload nhị phân | Đủ tốt cho submit tx / query |

Các lựa chọn khác và lý do loại:

- **gRPC thuần:** hiệu quả nhưng **không gọi trực tiếp được từ trình duyệt** nếu thiếu lớp trung gian — mâu thuẫn với mục tiêu để dApp web nói chuyện thẳng với executor.
- **JSON-RPC kiểu Tendermint/CometBFT:** gắn chặt với mô hình **full node** đồng thuận, không hợp kiến trúc **executor tách rời** của ev-node.
- **HTTP/REST + JSON:** đơn giản, không phụ thuộc ngôn ngữ, dễ kiểm thử và dễ đặt sau proxy — đúng nhu cầu của SDK client và dApp web.

## Vì sao vẫn giữ connectrpc (lợi cả đôi)

Dùng `connectrpc` thay vì viết HTTP server trần cho phép **một cổng phục vụ cả hai**:

- **REST/JSON** cho browser và SDK client (đường đi chính).
- **gRPC + reflection** cho tooling backend (`grpcurl`, Postman) và để giữ đúng hợp đồng interface `core/execution` — không cần dựng proxy gRPC-Web riêng.

Nói cách khác: HTTP/REST là mặt tiền cho ứng dụng, gRPC là mặt sau cho hạ tầng/tooling, cùng chạy trên một endpoint h2c.

## Liên quan

- [architecture.md](architecture.md) — sơ đồ stack tổng thể (mũi tên `HTTP` từ SDK xuống `cosmos-exec-grpc`).
- [api-reference.md](api-reference.md) — danh sách đầy đủ các endpoint REST.
- [frontend-integration.md](frontend-integration.md) — dApp web gọi REST qua Next.js proxy ra sao.
- Phần lý thuyết tương ứng: thesis Chương 3, mục *Giao tiếp HTTP và kiến trúc REST* (`docs/thesis/thesis-chapter3.md`).
