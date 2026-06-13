# syntax=docker/dockerfile:1

# ── Build stage ──────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

# ca-certificates needed for HTTPS calls (calendar APIs, webhooks)
RUN apk add --no-cache ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o calnode ./cmd/calnode

# ── Runtime stage ─────────────────────────────────────────────────────────────
# scratch keeps the image well under the 50MB target (§23)
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /build/calnode /calnode

VOLUME ["/data"]
EXPOSE 3000

ENV PORT=3000 \
    DATABASE_URL=sqlite:///data/calnode.db

ENTRYPOINT ["/calnode"]
