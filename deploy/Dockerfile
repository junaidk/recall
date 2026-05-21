# syntax=docker/dockerfile:1.7
#
# Build a Recall server image. Build context is the repo root:
#   docker build -f deploy/Dockerfile -t recall .
# Or via the sibling compose file:
#   docker compose -f deploy/docker-compose.yml up -d --build

# --- builder ---------------------------------------------------------------
FROM golang:1.25-alpine AS builder

# CGO is required by go-sqlite3; build-base provides gcc + musl-dev.
RUN apk add --no-cache build-base git

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# sqlite_fts5 matches the Makefile's BUILD_TAGS.
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    CGO_ENABLED=1 \
    go build -tags sqlite_fts5 -ldflags="-s -w" \
        -o /out/recall-server ./cmd/server

# --- runtime ---------------------------------------------------------------
FROM alpine:3.20

# ca-certificates: HTTPS to DeepL + DWDS audio backfill.
# tzdata: local-day rollover for daily new-card cap.
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S recall && adduser -S -G recall -h /app recall

WORKDIR /app

COPY --from=builder /out/recall-server /app/recall-server
# Seed data is read at boot for enrichment, sentences, and verb conjugations.
COPY seed /app/seed

RUN mkdir -p /app/data && chown -R recall:recall /app

USER recall

EXPOSE 8080

ENTRYPOINT ["/app/recall-server"]
CMD ["-config", "/app/config.yaml"]
