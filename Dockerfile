# syntax=docker/dockerfile:1

# ---------------------------------------------------------------- build ----
# Keep this in sync with the `go` directive in go.mod.
FROM golang:1.27-alpine AS build

WORKDIR /src

# Copy the module files first so dependency download is cached separately
# from the source, which makes rebuilds much faster.
COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG VERSION=dev
# CGO is not needed; a static binary keeps the runtime image tiny.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/extto ./cmd/server

# -------------------------------------------------------------- runtime ----
FROM alpine:3.20

# ca-certificates is required to reach https://ext.to and the Telegram API.
# tzdata lets log timestamps follow the configured timezone.
# su-exec lets the entrypoint drop privileges after fixing volume ownership.
RUN apk add --no-cache ca-certificates tzdata su-exec && \
    adduser -D -u 10001 app

COPY --from=build /out/extto /usr/local/bin/extto
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Settings and history live here; mount a volume to persist them.
RUN mkdir -p /data
VOLUME ["/data"]

EXPOSE 8080

ENV DATA_DIR=/data \
    ADDR=:8080 \
    TZ=Asia/Shanghai \
    PUID=1000 \
    PGID=1000

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/health >/dev/null 2>&1 || exit 1

# The entrypoint starts as root only to fix volume ownership, then drops to
# PUID:PGID before running the server.
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
