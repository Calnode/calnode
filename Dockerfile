# syntax=docker/dockerfile:1

# ── Frontend build stage ───────────────────────────────────────────────────────
FROM node:22-alpine AS frontend-builder

# Pin pnpm to the version in package.json's `packageManager` field (not @latest)
# so CI builds are reproducible and match the committed lockfile.
RUN corepack enable && corepack prepare pnpm@10.32.1 --activate

WORKDIR /app

COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY frontend/ .
RUN pnpm build

# ── Go build stage ─────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache ca-certificates wget

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=frontend-builder /app/build ./frontend/build

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -o calnode ./cmd/calnode

# Download Litestream for the deployment target (linux/amd64)
ARG LITESTREAM_VERSION=0.3.13
RUN wget -qO- \
    "https://github.com/benbjohnson/litestream/releases/download/v${LITESTREAM_VERSION}/litestream-v${LITESTREAM_VERSION}-linux-amd64.tar.gz" \
    | tar -xz -C /usr/local/bin litestream

# ── Runtime stage ─────────────────────────────────────────────────────────────
# alpine (not scratch) — needed for the shell entrypoint and Litestream.
FROM --platform=linux/amd64 alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /usr/local/bin/litestream /usr/local/bin/litestream
COPY --from=builder /build/calnode /calnode
COPY litestream.yml /etc/litestream.yml
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Note: no `VOLUME` directive — persistent storage is provided by the platform's
# managed volume mounted at /data (Railway rejects the Docker VOLUME instruction;
# Fly mounts via fly.toml). The dir is created at runtime by entrypoint.sh.
EXPOSE 3000

ENV PORT=3000 \
    DATABASE_URL=sqlite:///data/calnode.db

ENTRYPOINT ["/entrypoint.sh"]
