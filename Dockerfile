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

######## Stage 3: runtime (Ubuntu 22.04 LTS base image) ########
FROM ubuntu:22.04 AS runtime

ENV DEBIAN_FRONTEND=noninteractive \
    PORT=8080 \
    ADMIN_INTERNAL_ADDR=127.0.0.1:3000 \
    APP_ENV=production \
    HOSTNAME=127.0.0.1

# Install base Ubuntu system tools
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    wget \
    tini \
    bash \
    procps \
    iputils-ping \
    htop \
    git \
    xz-utils \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Install official Node 20.x runtime directly into /usr/local
RUN curl -fsSL https://nodejs.org/dist/v20.18.0/node-v20.18.0-linux-x64.tar.xz | tar -xJ -C /usr/local --strip-components=1

# Install official Docker CLI binary directly into /usr/local/bin
RUN curl -fsSL https://download.docker.com/linux/static/stable/x86_64/docker-24.0.7.tgz | tar -xz -C /usr/local/bin --strip-components=1 docker/docker

WORKDIR /app
# Static Go gateway (single public listener on :8080)
COPY --from=gateway /out/gateway /app/gateway
# Next.js standalone server + static assets (internal :3000 only)
COPY --from=admin /app/admin/.next/standalone /app/admin
COPY --from=admin /app/admin/.next/static /app/admin/.next/static

# Write fail-safe entrypoint script for Ubuntu container
RUN printf '%s\n' \
    '#!/bin/bash' \
    'set -e' \
    '(cd /app/admin && PORT=3000 HOSTNAME=127.0.0.1 node server.js) &' \
    'NEXT_PID=$!' \
    'echo "[entrypoint] waiting for Next.js on :3000…"' \
    'MAX_WAIT=30; i=0' \
    'while [ $i -lt $MAX_WAIT ]; do' \
    '  if curl -s -f http://127.0.0.1:3000/admin >/dev/null 2>&1; then' \
    '    break' \
    '  fi' \
    '  sleep 1; i=$((i+1))' \
    'done' \
    '[ $i -lt $MAX_WAIT ] && echo "[entrypoint] Next.js ready" || echo "[entrypoint] warning: Next.js timeout"' \
    'exec /app/gateway' \
    > /app/entrypoint.sh && chmod +x /app/entrypoint.sh

EXPOSE 8080
ENTRYPOINT ["/usr/bin/tini", "--", "/app/entrypoint.sh"]
