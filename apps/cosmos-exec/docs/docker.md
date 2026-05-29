# Chạy cosmos-exec-grpc bằng Docker

Tài liệu này gồm: (1) `Dockerfile` multi-stage build cho `cosmos-exec-grpc`, (2) `docker-compose.yml` để chạy 1 lệnh, (3) cách build/chạy thủ công, (4) tips production.

> Các file đã có sẵn trong repo:
>
> - [apps/cosmos-exec/Dockerfile](../Dockerfile)
> - [apps/cosmos-exec/docker-compose.yml](../docker-compose.yml)
> - [apps/cosmos-exec/.env.example](../.env.example)
> - [apps/cosmos-exec/.dockerignore](../.dockerignore)
>
> Tài liệu giải thích chi tiết từng quyết định và cách dùng. Mẫu theo style các app khác (xem [apps/grpc/Dockerfile](../../grpc/Dockerfile), [apps/testapp/Dockerfile](../../testapp/Dockerfile)).

---

## 0. Quickstart (5 lệnh)

```bash
# Từ repo root
cp apps/cosmos-exec/.env.example apps/cosmos-exec/.env
# Mở .env, đặt COSMOS_EXEC_AUTH_TOKEN bằng giá trị thật (openssl rand -hex 32)

cd apps/cosmos-exec
docker compose up -d --build              # lần đầu cần --build
docker compose logs -f cosmos-exec        # theo dõi log
curl http://127.0.0.1:50051/healthz       # 200 = ok
```

Dev nhanh (không cần `.env`, không persist):

```bash
docker build -f apps/cosmos-exec/Dockerfile -t cosmos-exec-grpc:local .
docker run --rm -p 50051:50051 cosmos-exec-grpc:local --profile dev --in-memory
```

---

## 1. Dockerfile

File hiện có: [apps/cosmos-exec/Dockerfile](../Dockerfile). Nội dung:

```dockerfile
# syntax=docker/dockerfile:1.7

# ── Builder stage ──────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

# hadolint ignore=DL3018
RUN apk add --no-cache git gcc musl-dev linux-headers ca-certificates

WORKDIR /src

# Copy toàn bộ workspace (cosmos-exec multi-module dùng replace nội bộ).
COPY . .

# Tải module, build binary. -trimpath cho reproducible build.
RUN go env -w GOFLAGS="-mod=mod" \
    && cd apps/cosmos-exec/cmd/cosmos-exec-grpc \
    && CGO_ENABLED=0 GOOS=linux go build \
         -trimpath \
         -ldflags="-s -w" \
         -o /out/cosmos-exec-grpc .

# ── Runtime stage ──────────────────────────────────────────────────────────
FROM alpine:3.22

# hadolint ignore=DL3018
RUN apk add --no-cache ca-certificates curl tini \
    && addgroup -g 1000 cosmos \
    && adduser -u 1000 -G cosmos -s /bin/sh -D cosmos

# Data dir (mount volume vào đây cho persistence).
RUN mkdir -p /var/lib/cosmos-exec && chown -R cosmos:cosmos /var/lib/cosmos-exec

COPY --from=builder /out/cosmos-exec-grpc /usr/local/bin/cosmos-exec-grpc

USER cosmos
WORKDIR /home/cosmos

# Port mặc định.
EXPOSE 50051

# Healthcheck đập /healthz — middleware luôn cho phép GET không cần auth.
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=3 \
  CMD curl -fsS http://127.0.0.1:50051/healthz || exit 1

# tini là PID 1 → forward SIGTERM đúng cho graceful shutdown.
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/cosmos-exec-grpc"]

# Mặc định prod, lắng nghe trên mọi interface, home ở volume.
CMD ["--profile", "prod", \
     "--address", "0.0.0.0:50051", \
     "--home", "/var/lib/cosmos-exec"]
```

### Điểm cần chú ý

| Quyết định                  | Lý do                                                                 |
| --------------------------- | --------------------------------------------------------------------- |
| `CGO_ENABLED=0`             | Binary tĩnh, không phụ thuộc glibc → image nhỏ và chuyển môi trường dễ |
| `alpine:3.22` runtime       | Nhẹ (~5MB) và có `apk` để thêm `curl`/`tini`                          |
| `tini` làm PID 1            | Forward `SIGTERM` đúng — `srv.Shutdown` mới chạy graceful 10s         |
| `USER cosmos`               | Không chạy root trong container                                       |
| `HEALTHCHECK` dùng `/healthz` | GET không cần auth → check được ngay cả khi bật `AuthToken`         |
| `--profile prod`            | Mặc định an toàn; dev override qua `docker run ... --profile dev`     |

---

## 2. Build & chạy thủ công

```bash
# Build từ repo root (cần context toàn workspace vì go.mod replace nội bộ).
docker build -f apps/cosmos-exec/Dockerfile -t cosmos-exec-grpc:local .

# Chạy dev (in-memory không bền, mở mọi origin).
docker run --rm -p 50051:50051 \
  cosmos-exec-grpc:local \
  --profile dev --in-memory

# Chạy prod với persistence + auth.
docker run -d --name cosmos-exec \
  -p 50051:50051 \
  -v cosmos-exec-data:/var/lib/cosmos-exec \
  -e COSMOS_EXEC_AUTH_TOKEN="$(openssl rand -hex 32)" \
  -e COSMOS_EXEC_CORS_ORIGIN="https://app.mychain.io" \
  cosmos-exec-grpc:local
```

Smoke test:

```bash
curl -s http://127.0.0.1:50051/healthz | jq
curl -s http://127.0.0.1:50051/status  | jq
```

---

## 3. docker-compose.yml

File hiện có: [apps/cosmos-exec/docker-compose.yml](../docker-compose.yml). Nội dung:

```yaml
services:
  cosmos-exec:
    build:
      context: ../../          # repo root để go.mod replace hoạt động
      dockerfile: apps/cosmos-exec/Dockerfile
    image: cosmos-exec-grpc:local
    container_name: cosmos-exec
    restart: unless-stopped
    ports:
      - "50051:50051"
    environment:
      # Profile: dev | test | prod (override CMD nếu cần).
      COSMOS_EXEC_PROFILE: prod
      # Auth: bắt buộc cho prod nếu expose public.
      COSMOS_EXEC_AUTH_TOKEN: "${COSMOS_EXEC_AUTH_TOKEN:?set in .env}"
      # CORS: domain frontend cụ thể (đừng để * trên prod).
      COSMOS_EXEC_CORS_ORIGIN: "${COSMOS_EXEC_CORS_ORIGIN:-https://app.mychain.io}"
      # Rate limit per-IP (rps).
      COSMOS_EXEC_RATE_LIMIT_RPS: "100"
      # Faucet (bỏ block này nếu không cần).
      COSMOS_EXEC_TREASURY_PRIVKEY_HEX: "${COSMOS_EXEC_TREASURY_PRIVKEY_HEX:-}"
      COSMOS_EXEC_FAUCET_AMOUNT: "1000000ustake"
      COSMOS_EXEC_FAUCET_COOLDOWN_SECONDS: "3600"
      # Fee policy.
      COSMOS_EXEC_MIN_GAS_PRICE: "0.001"
      COSMOS_EXEC_GAS_DENOM: "ustake"
    volumes:
      - cosmos-exec-data:/var/lib/cosmos-exec
    healthcheck:
      test: ["CMD", "curl", "-fsS", "http://127.0.0.1:50051/healthz"]
      interval: 15s
      timeout: 3s
      retries: 3
      start_period: 10s
    logging:
      driver: "json-file"
      options:
        max-size: "10m"
        max-file: "5"

volumes:
  cosmos-exec-data:
```

### `.env` (template tại [.env.example](../.env.example))

Tạo file `.env` bên cạnh `docker-compose.yml`:

```bash
cp apps/cosmos-exec/.env.example apps/cosmos-exec/.env
```

Nội dung mặc định:

```env
COSMOS_EXEC_AUTH_TOKEN=replace-with-openssl-rand-hex-32
COSMOS_EXEC_CORS_ORIGIN=https://app.mychain.io
# COSMOS_EXEC_TREASURY_PRIVKEY_HEX=...   # bỏ comment nếu cần faucet
```

Lệnh sinh token nhanh:

```bash
echo "COSMOS_EXEC_AUTH_TOKEN=$(openssl rand -hex 32)" >> apps/cosmos-exec/.env
```

> `.env` đã nằm trong [`.dockerignore`](../.dockerignore) và nên thêm vào `.gitignore` — **không commit**.

Chạy compose:

```bash
cd apps/cosmos-exec
docker compose up -d --build      # --build lần đầu hoặc khi sửa code
docker compose ps                 # xem trạng thái + healthcheck
docker compose logs -f cosmos-exec
docker compose restart            # restart không mất data
docker compose down               # dừng (data vẫn còn trong volume)
docker compose down -v            # xoá luôn volume → mất state
```

---

## 4. Mapping profile ↔ command

| Mục đích                       | Lệnh                                                              |
| ------------------------------ | ----------------------------------------------------------------- |
| Dev hot-loop, không persist    | `docker run ... --profile dev --in-memory`                        |
| Integration test trong CI      | `docker run ... --profile test` (mặc định in-memory, port ngẫu nhiên — chỉ trong process Go test)  |
| Prod, file-backed              | `docker run ... --profile prod -v data:/var/lib/cosmos-exec`      |
| Override port                  | `--address 0.0.0.0:8080` + `-p 8080:8080`                         |

> Nhớ: trong container, file home **bên trong** là `/var/lib/cosmos-exec`. Khi mount volume, đảm bảo user UID 1000 (đã set trong Dockerfile) có quyền ghi vào điểm mount.

---

## 5. Tips production

### 5.1 Image size

```bash
docker images cosmos-exec-grpc:local
# Mong đợi ~30-50 MB (binary tĩnh + alpine + tini).
```

Nếu muốn nhỏ hơn nữa, đổi runtime stage sang `gcr.io/distroless/static`:

```dockerfile
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/cosmos-exec-grpc /cosmos-exec-grpc
USER nonroot:nonroot
ENTRYPOINT ["/cosmos-exec-grpc"]
```

Đánh đổi: mất `curl` cho healthcheck — phải dùng `HEALTHCHECK CMD` gọi binary tự kiểm hoặc bỏ healthcheck nội (dùng Kubernetes liveness probe ngoài).

### 5.2 Logging

Logger ghi ra `stdout` ([main.go:86](../cmd/cosmos-exec-grpc/main.go#L86)). Docker tự thu — đừng tự log file trong container.

Để JSON-structured cho log aggregator (Loki/Elastic):

```bash
-e COSMOS_EXEC_LOG_LEVEL=info
```

(format phụ thuộc `cosmossdk.io/log`; mặc định là text — nếu cần JSON, mở rộng config riêng.)

### 5.3 Graceful shutdown

`main.go` đã handle `SIGINT/SIGTERM` với 10s timeout ([main.go:200-237](../cmd/cosmos-exec-grpc/main.go#L200-L237)).

Docker mặc định gửi `SIGTERM` rồi đợi 10s → `SIGKILL`. **Phải khớp**: nếu bạn tăng timeout shutdown lên 30s thì cần:

```bash
docker run ... --stop-timeout 35    # > 30s
```

hoặc trong compose:

```yaml
stop_grace_period: 35s
```

### 5.4 Persistence volume

`/var/lib/cosmos-exec/data/` chứa LevelDB + 3 file persist. **Tuyệt đối** dùng named volume hoặc bind-mount, không để trong layer image:

```yaml
volumes:
  - cosmos-exec-data:/var/lib/cosmos-exec
```

Backup khi cần: tạm dừng container (`docker compose stop`) → tar volume → khởi lại. Tránh hot-copy LevelDB đang chạy.

### 5.5 Healthcheck vs Readycheck

- `/healthz` → "process còn sống" — dùng cho Docker `HEALTHCHECK` và Kubernetes liveness.
- `/ready` → "đã init chain, sẵn sàng nhận traffic" — dùng cho Kubernetes readiness probe / load balancer.

Khi container vừa khởi động (đang replay file persist), `/ready` trả `503` còn `/healthz` đã `200` → traffic chưa được chuyển tới.

### 5.6 Secrets

Không hardcode `COSMOS_EXEC_AUTH_TOKEN` hay `COSMOS_EXEC_TREASURY_PRIVKEY_HEX` vào image / `docker-compose.yml`. Dùng:

- `.env` không commit (compose tự đọc).
- Docker secrets: `docker secret create cosmos-exec-auth ./token.txt` rồi mount.
- Kubernetes secrets (`envFrom: secretRef`).

### 5.7 Multi-architecture

Build cho cả `amd64` và `arm64` (Mac M-series):

```bash
docker buildx create --use --name xb
docker buildx build \
  -f apps/cosmos-exec/Dockerfile \
  --platform linux/amd64,linux/arm64 \
  -t myreg/cosmos-exec-grpc:0.1.0 \
  --push .
```

---

## 6. Troubleshooting

| Triệu chứng                                              | Nguyên nhân & cách xử lý                                              |
| -------------------------------------------------------- | --------------------------------------------------------------------- |
| `database lock detected ...`                             | Volume cũ còn process khác chiếm hoặc 2 container mount cùng volume. Dừng container khác, hoặc dùng `--in-memory` để debug. |
| `/healthz` mãi `Unhealthy`                               | Port mapping sai, hoặc `--address 127.0.0.1` chỉ bind loopback của container. Dùng `0.0.0.0:50051`. |
| `401 unauthorized` từ frontend                           | Frontend chưa gửi `Authorization: Bearer <token>` cho POST. Set `COSMOS_EXEC_AUTH_TOKEN` rỗng (dev) hoặc nhúng token. |
| CORS bị browser block                                    | `COSMOS_EXEC_CORS_ORIGIN` không khớp origin frontend (chú ý trailing slash, port). |
| Container `OOMKilled`                                    | LevelDB cache + tx log lớn. Đặt `--memory 1g` hoặc tăng. Cân nhắc `/exec/prune` định kỳ. |
| Faucet trả `insufficient funds`                          | `COSMOS_EXEC_TREASURY_AMOUNT` ở genesis quá thấp — sửa và **xoá volume** rồi khởi lại (genesis chỉ chạy 1 lần). |

---

## 7. Tóm tắt nhanh

```bash
# 1. Chuẩn bị .env (chỉ lần đầu).
cp apps/cosmos-exec/.env.example apps/cosmos-exec/.env
echo "COSMOS_EXEC_AUTH_TOKEN=$(openssl rand -hex 32)" >> apps/cosmos-exec/.env

# 2. Chạy prod qua compose.
cd apps/cosmos-exec && docker compose up -d --build

# 3. Kiểm tra.
curl http://127.0.0.1:50051/healthz
curl http://127.0.0.1:50051/status

# Hoặc build/chạy thủ công cho dev (không cần .env).
docker build -f apps/cosmos-exec/Dockerfile -t cosmos-exec-grpc:local .
docker run --rm -p 50051:50051 cosmos-exec-grpc:local --profile dev --in-memory
```

## 8. Cấu trúc file Docker trong repo

```
apps/cosmos-exec/
├── Dockerfile            ◄ multi-stage build (mục 1)
├── docker-compose.yml    ◄ wrapper 1-lệnh (mục 3)
├── .dockerignore         ◄ loại file thừa khỏi build context
├── .env.example          ◄ template biến môi trường
└── docs/
    └── docker.md         ◄ tài liệu này
```

Build context = repo root (`../../` từ compose) vì `go.mod` trong `apps/cosmos-exec` dùng `replace` trỏ vào các module anh em (`../../core`, `../../execution/grpc`…). Build từ thư mục con sẽ thiếu source → lỗi `cannot find module`.
