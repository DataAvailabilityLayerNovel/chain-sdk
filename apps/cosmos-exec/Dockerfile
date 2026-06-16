# syntax=docker/dockerfile:1.7

# ── Builder stage ──────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

# CGO toolchain: cosmos-exec runs CosmWasm contracts (libwasmvm) and uses
# cosmos-sdk's cgo secp256k1 — both REQUIRE cgo. CGO_ENABLED=0 does not even
# compile (cgo-gated files get swapped, breaking secp256k1 + wasmd keeper types).
# hadolint ignore=DL3018
RUN apk add --no-cache git gcc musl-dev linux-headers ca-certificates wget

WORKDIR /src

# Static libwasmvm (muslc) for the wasmvm cgo bindings. Version MUST match the
# wasmvm required by wasmd v0.50.0 (github.com/CosmWasm/wasmvm v1.5.0 in go.mod).
# Checksums from the wasmvm v1.5.0 release checksums.txt.
ARG WASMVM_VERSION=v1.5.0
RUN set -eux; \
    arch="$(uname -m)"; \
    wget -q -O /lib/libwasmvm_muslc.a \
        "https://github.com/CosmWasm/wasmvm/releases/download/${WASMVM_VERSION}/libwasmvm_muslc.${arch}.a"; \
    case "$arch" in \
      aarch64) sum=2687afbdae1bc6c7c8b05ae20dfb8ffc7ddc5b4e056697d0f37853dfe294e913 ;; \
      x86_64)  sum=465e3a088e96fd009a11bfd234c69fb8a0556967677e54511c084f815cf9ce63 ;; \
      *) echo "unsupported arch: $arch" >&2; exit 1 ;; \
    esac; \
    echo "${sum}  /lib/libwasmvm_muslc.a" | sha256sum -c -

# Copy toàn bộ workspace (cosmos-exec multi-module dùng replace nội bộ).
COPY . .

# Build with cgo + static wasmvm (muslc tag → fully static binary on alpine).
# Use -mod=readonly (NOT -mod=mod): -mod=mod re-resolves the dependency graph
# against sibling modules (replace ../../, ../../core) and pulls a newer
# cosmossdk.io/core/store that breaks wasmd v0.50.0. readonly uses the pinned,
# compatible versions from go.sum (cosmos-sdk 0.50.11 / store 1.1.1 / core 0.11.1).
RUN cd apps/cosmos-exec \
    && CGO_ENABLED=1 GOOS=linux go build \
         -mod=readonly \
         -tags 'muslc' \
         -trimpath \
         -ldflags '-s -w -linkmode=external -extldflags "-Wl,-z,muldefs -static"' \
         -o /out/cosmos-exec-grpc ./cmd/cosmos-exec-grpc

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
