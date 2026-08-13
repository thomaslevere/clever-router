# syntax=docker/dockerfile:1

######## Stage 1: build the Next.js admin panel (standalone output) ########
FROM node:20-alpine AS admin
WORKDIR /app/admin
COPY admin/package.json admin/package-lock.json* ./
RUN npm ci
COPY admin/ ./
RUN npm run build

######## Stage 2: build the Go gateway (static binary) ########
FROM golang:1.24-alpine AS gateway
WORKDIR /app/gateway
ENV CGO_ENABLED=0 GOOS=linux
COPY gateway/go.mod gateway/go.sum ./
RUN go mod download
COPY gateway/ ./
RUN go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/server

######## Stage 3: runtime (Node 20 Alpine with bash & docker-cli) ########
FROM node:20-alpine AS runtime
RUN apk add --no-cache docker-cli tini ca-certificates bash htop git procps

WORKDIR /app
# Static Go gateway (single public listener on :8080)
COPY --from=gateway /out/gateway /app/gateway
# Next.js standalone server + static assets (internal :3000 only)
COPY --from=admin /app/admin/.next/standalone /app/admin
COPY --from=admin /app/admin/.next/static /app/admin/.next/static

# Write entrypoint inline
RUN printf '%s\n' \
    '#!/bin/sh' \
    'set -e' \
    '(cd /app/admin && PORT=3000 HOSTNAME=127.0.0.1 node server.js) &' \
    'NEXT_PID=$!' \
    'echo "[entrypoint] waiting for Next.js on :3000…"' \
    'MAX_WAIT=30; i=0' \
    'while [ $i -lt $MAX_WAIT ]; do' \
    '  wget -q --spider http://127.0.0.1:3000/admin 2>/dev/null && break' \
    '  sleep 1; i=$((i+1))' \
    'done' \
    '[ $i -lt $MAX_WAIT ] && echo "[entrypoint] Next.js ready" || echo "[entrypoint] warning: Next.js timeout"' \
    'exec /app/gateway' \
    > /app/entrypoint.sh && chmod +x /app/entrypoint.sh

ENV PORT=8080 \
    ADMIN_INTERNAL_ADDR=127.0.0.1:3000 \
    APP_ENV=production \
    HOSTNAME=127.0.0.1

EXPOSE 8080
ENTRYPOINT ["/sbin/tini", "--", "/app/entrypoint.sh"]
