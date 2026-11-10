FROM registry.access.redhat.com/ubi9/go-toolset:1.26@sha256:0e3894cea4fc077084245e8aa2befc8a7d91a4f60fbf850f0b5b555cbe62ee26 AS builder

ARG APP_VERSION=dev
ARG APP_COMMIT=unknown
ARG APP_DATE=1970-01-01T00:00:00Z

WORKDIR /opt/app-root/src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ cmd/
COPY pkg/ pkg/

RUN set -eux; \
    MODULE=github.com/openshift-online/gcp-hcp-ctl; \
    LDFLAGS="-X ${MODULE}/pkg/cli.version=${APP_VERSION} -X ${MODULE}/pkg/cli.commit=${APP_COMMIT} -X ${MODULE}/pkg/cli.date=${APP_DATE}"; \
    mkdir -p bin releases; \
    for platform in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do \
      os=$(echo "$platform" | cut -d/ -f1); \
      arch=$(echo "$platform" | cut -d/ -f2); \
      out="bin/gcphcpctl_${os}_${arch}"; \
      CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -buildvcs=false -ldflags "$LDFLAGS" -o "$out" ./cmd/gcphcpctl; \
      cp "$out" bin/gcphcpctl; \
      tar --mtime=@0 --owner=0 --group=0 --numeric-owner -cf - -C bin gcphcpctl | gzip -n > "releases/gcphcpctl_${os}_${arch}.tar.gz"; \
      rm bin/gcphcpctl; \
    done; \
    ( cd releases && sha256sum *.tar.gz > "gcphcpctl_${APP_VERSION}_SHA256SUMS" )

FROM registry.access.redhat.com/ubi9/ubi-minimal:latest@sha256:ac50cda52b2de3001fdf4b54ffe553c91977caff8e3c0e9a3d340cdb730f2c86

LABEL org.opencontainers.image.title="gcp-hcp-ctl" \
      org.opencontainers.image.description="CLI for managing GCP Hosted Control Plane (HCP) clusters" \
      org.opencontainers.image.source="https://github.com/openshift-online/gcp-hcp-ctl" \
      org.opencontainers.image.vendor="Red Hat"

COPY --from=builder /opt/app-root/src/releases /releases
USER 1001
