# syntax=docker/dockerfile:1

# ── Frontend build stage ───────────────────────────────────────────────────────
# Built for the BUILD platform, not the target: the output is static JS/CSS that is
# identical for every architecture. Without this pin a multi-arch build (buildx:
# linux/amd64,linux/arm64) runs pnpm install and pnpm build a second time under QEMU
# for arm64, which is slow and occasionally hangs for hours.
FROM --platform=$BUILDPLATFORM node:22-alpine AS frontend-builder

# Pin pnpm to the version in package.json's `packageManager` field (not @latest)
# so CI builds are reproducible and match the committed lockfile.
RUN corepack enable && corepack prepare pnpm@10.32.1 --activate

WORKDIR /app

COPY frontend/package.json frontend/pnpm-lock.yaml ./
RUN pnpm install --frozen-lockfile

COPY frontend/ .
RUN pnpm build

# ── Go build stage ─────────────────────────────────────────────────────────────
# Also the BUILD platform. CGO is disabled, so Go cross-compiles natively to the
# target arch; there is no reason for this stage to run under QEMU. Running it
# emulated is what made a multi-arch build stall: the arm64 leg spent its time
# interpreting the compiler instead of compiling.
FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine AS builder

RUN apk add --no-cache ca-certificates wget

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=frontend-builder /app/build ./frontend/build

# Build for the TARGET architecture from this (build-platform) stage. buildx
# populates TARGETARCH; with no CGO this is a native cross-compile, not emulation.
# A plain `docker build` on an arm64 host still works: TARGETARCH then matches the
# host, so the binary executes there as before.
ARG TARGETARCH
# VERSION is passed by docker-publish.yml from the resolved image tag (semver on a
# release, "edge"/branch on a plain main push); defaults to "dev" for local builds
# so `go build` without --build-arg still works as documented in CLAUDE.md.
ARG VERSION=dev
# COMMIT is stamped explicitly because this image is built from a copied source tree
# with no .git, so the Go toolchain has no VCS metadata to embed and /version would
# otherwise report commit "unknown" on every container. That matters now that branch
# images are deployable: they report version "dev", leaving nothing else to identify
# the build by.
ARG COMMIT=""
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build \
    -ldflags="-s -w -X github.com/calnode/calnode/internal/buildinfo.Version=${VERSION} -X github.com/calnode/calnode/internal/buildinfo.Commit=${COMMIT}" \
    -o calnode ./cmd/calnode

# Download Litestream for the deployment target, matching TARGETARCH
ARG LITESTREAM_VERSION=0.3.13
RUN wget -qO- \
    "https://github.com/benbjohnson/litestream/releases/download/v${LITESTREAM_VERSION}/litestream-v${LITESTREAM_VERSION}-linux-${TARGETARCH}.tar.gz" \
    | tar -xz -C /usr/local/bin litestream

# Export the same executable as the container, including its embedded frontend.
FROM scratch AS standalone
COPY --from=builder /build/calnode /calnode
COPY LICENSE /LICENSE

# ── Runtime stage ─────────────────────────────────────────────────────────────
# alpine (not scratch) — needed for the shell entrypoint and Litestream.
# No --platform pin: under buildx this defaults to the TARGET platform, which is
# what we want (an arm64 final image for arm64 consumers). Only the two builder
# stages above are pinned to BUILDPLATFORM.
FROM alpine:3.24

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
