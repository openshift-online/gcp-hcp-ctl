FROM registry.access.redhat.com/ubi9/go-toolset:1.26 AS builder

WORKDIR /opt/app-root/src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN set -eux; \
    git config --global --add safe.directory '*'; \
    MODULE=github.com/openshift-online/gcp-hcp-ctl; \
    VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev"); \
    COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown"); \
    DATE=$(date -u '+%Y-%m-%dT%H:%M:%SZ'); \
    LDFLAGS="-X ${MODULE}/pkg/cli.version=${VERSION} -X ${MODULE}/pkg/cli.commit=${COMMIT} -X ${MODULE}/pkg/cli.date=${DATE}"; \
    mkdir -p bin releases; \
    for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
      os=$(echo "$platform" | cut -d/ -f1); \
      arch=$(echo "$platform" | cut -d/ -f2); \
      out="bin/gcphcpctl_${os}_${arch}"; \
      CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -buildvcs=false -ldflags "$LDFLAGS" -o "$out" ./cmd/gcphcpctl; \
      cp "$out" bin/gcphcpctl; \
      tar czf "releases/gcphcpctl_${os}_${arch}.tar.gz" -C bin gcphcpctl; \
      rm bin/gcphcpctl; \
    done; \
    ( cd releases && sha256sum *.tar.gz > "gcphcpctl_${VERSION}_SHA256SUMS" )

FROM registry.access.redhat.com/ubi9/ubi-minimal

COPY --from=builder /opt/app-root/src/releases /releases
