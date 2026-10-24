# syntax=docker/dockerfile:1.7
# ---------- 1. build the web UI ----------
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

# ---------- 2. build the Go binary ----------
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/pocket-core ./third_party/pocket-core
RUN cd third_party/pocket-core && sha256sum -c SHA256SUMS
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/pocket-backups ./cmd/pocket-backups

# ---------- 3. the tools: restic and rclone, official releases ----------
FROM --platform=$BUILDPLATFORM alpine:3.21 AS tools
ARG TARGETARCH RESTIC_VERSION=0.19.1 RCLONE_VERSION=1.74.4
RUN apk add --no-cache curl unzip bzip2 \
 && curl -fsSL "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/restic_${RESTIC_VERSION}_linux_${TARGETARCH}.bz2" | bunzip2 > /restic \
 && curl -fsSL -o /tmp/rclone.zip "https://downloads.rclone.org/v${RCLONE_VERSION}/rclone-v${RCLONE_VERSION}-linux-${TARGETARCH}.zip" \
 && unzip -j /tmp/rclone.zip '*/rclone' -d / \
 && chmod 755 /restic /rclone

# ---------- 4. runtime ----------
FROM alpine:3.21
# docker-cli: dumps run inside your database containers; openssh: sftp
# repositories; util-linux: ionice for background runs; tar for exports.
RUN apk add --no-cache ca-certificates tzdata curl docker-cli openssh-client util-linux tar \
 && addgroup -g 1000 app && adduser -u 1000 -G app -D -h /data app
COPY --from=tools /restic /rclone /usr/local/bin/
COPY --from=build /out/pocket-backups /usr/local/bin/pocket-backups
ENV DATA_DIR=/data PORT=8080 HOST=0.0.0.0
VOLUME ["/data"]
EXPOSE 8080
USER app
WORKDIR /data
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD curl -fsS http://localhost:8080/healthz || exit 1
LABEL org.opencontainers.image.title="Pocket Backups" \
      org.opencontainers.image.description="Backups for the whole box: consistent database dumps, restic snapshots, copies to a NAS, Google Drive, Backblaze, S3 or Pocket Files, an offline USB disk, restore of any file, a coverage check for new apps — standalone or on top of Backrest." \
      org.opencontainers.image.source="https://github.com/abjelosevic88/pocket-homelab-backups" \
      org.opencontainers.image.licenses="MIT"
ENTRYPOINT ["pocket-backups"]
