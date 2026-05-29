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
