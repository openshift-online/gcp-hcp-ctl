FROM golang:1.26 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN set -eux; \
    MODULE=github.com/openshift-online/gcp-hcp-ctl; \
    VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev"); \
    COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown"); \
    DATE=$(date -u '+%Y-%m-%dT%H:%M:%SZ'); \
    LDFLAGS="-X ${MODULE}/pkg/cli.version=${VERSION} -X ${MODULE}/pkg/cli.commit=${COMMIT} -X ${MODULE}/pkg/cli.date=${DATE}"; \
    mkdir -p /src/bin /releases; \
    for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
      os=$(echo "$platform" | cut -d/ -f1); \
      arch=$(echo "$platform" | cut -d/ -f2); \
      out="/src/bin/gcphcpctl_${os}_${arch}"; \
      CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -ldflags "$LDFLAGS" -o "$out" ./cmd/gcphcpctl; \
      cp "$out" /src/bin/gcphcpctl; \
      tar czf "/releases/gcphcpctl_${os}_${arch}.tar.gz" -C /src/bin gcphcpctl; \
      rm /src/bin/gcphcpctl; \
    done

FROM registry.access.redhat.com/ubi9/ubi-minimal

COPY --from=builder /releases /releases
