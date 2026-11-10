FROM registry.access.redhat.com/ubi9/go-toolset:1.26 AS builder

ARG APP_VERSION=dev
ARG APP_COMMIT=unknown

WORKDIR /opt/app-root/src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY pkg/ pkg/

RUN set -eux; \
    MODULE=github.com/openshift-online/gcp-hcp-ctl; \
    DATE=$(date -u '+%Y-%m-%dT%H:%M:%SZ'); \
    LDFLAGS="-X ${MODULE}/pkg/cli.version=${APP_VERSION} -X ${MODULE}/pkg/cli.commit=${APP_COMMIT} -X ${MODULE}/pkg/cli.date=${DATE}"; \
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
    ( cd releases && sha256sum *.tar.gz > "gcphcpctl_${APP_VERSION}_SHA256SUMS" )

FROM registry.access.redhat.com/ubi9/ubi-minimal

COPY --from=builder /opt/app-root/src/releases /releases
USER 1001
