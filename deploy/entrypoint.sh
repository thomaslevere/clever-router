#!/bin/bash
set -eo pipefail

echo "========================================="
echo " Starting Clever Router Service          "
echo "========================================="

# 1. Ensure required data directories exist with open permissions
DATA_PATH="${DATA_DIR:-/tmp/data}"
SCRATCH_PATH="${VOLUME_SCRATCH_DIR:-/tmp/clever_router_volumes}"
mkdir -p "$DATA_PATH" "$SCRATCH_PATH"
chmod -R 777 "$DATA_PATH" "$SCRATCH_PATH" 2>/dev/null || true

# 2. Track background child PIDs for multi-service forwarding
NEXT_PID=""
GATEWAY_PID=""

# 3. Graceful signal handler to trap Clever Cloud SIGTERM / SIGINT
graceful_shutdown() {
    echo "[Entrypoint] Received termination signal from Clever Cloud. Propagating to child services..."

    # Send SIGTERM to the Go Gateway first to trigger the S3 sync & adapter shutdown
    if [ -n "$GATEWAY_PID" ] && kill -0 "$GATEWAY_PID" 2>/dev/null; then
        echo "[Entrypoint] Forwarding SIGTERM to Gateway (PID: $GATEWAY_PID)..."
        kill -TERM "$GATEWAY_PID"
        
        # Wait for the Go gateway to complete its graceful S3 snapshot backup
        wait "$GATEWAY_PID" 2>/dev/null || true
        echo "[Entrypoint] Gateway stopped cleanly."
    fi

    # Terminate the Next.js Admin process if running
    if [ -n "$NEXT_PID" ] && kill -0 "$NEXT_PID" 2>/dev/null; then
        echo "[Entrypoint] Stopping Next.js Admin server (PID: $NEXT_PID)..."
        kill -TERM "$NEXT_PID"
        wait "$NEXT_PID" 2>/dev/null || true
    fi

    echo "[Entrypoint] All processes terminated. Container exit complete."
    exit 0
}

# Trap termination signals
trap graceful_shutdown SIGTERM SIGINT SIGQUIT

# 4. Start Next.js Admin Panel in the background (internal only :3000)
if [ -f "/app/admin/server.js" ]; then
    echo "[Entrypoint] Starting Next.js Admin Server on port ${ADMIN_PORT:-3000}..."
    (cd /app/admin && PORT=3000 HOSTNAME=127.0.0.1 node server.js) &
    NEXT_PID=$!
elif [ -d "/app/admin" ] && [ -f "/app/admin/package.json" ]; then
    echo "[Entrypoint] Starting Next.js Admin via npm..."
    (cd /app/admin && PORT=3000 HOSTNAME=127.0.0.1 npm run start) &
    NEXT_PID=$!
fi

# 5. Wait for Next.js to be ready on :3000
echo "[Entrypoint] Waiting for Next.js on :3000…"
MAX_WAIT=30
i=0
while [ $i -lt $MAX_WAIT ]; do
    if nc -z 127.0.0.1 3000 2>/dev/null || wget -q --spider http://127.0.0.1:3000/admin 2>/dev/null; then
        echo "[Entrypoint] Next.js ready after ${i}s"
        break
    fi
    sleep 1
    i=$((i + 1))
done
if [ $i -eq $MAX_WAIT ]; then
    echo "[Entrypoint] warning: Next.js did not respond in ${MAX_WAIT}s — continuing anyway"
fi

# 6. Verify Docker socket presence
if [ -S "/var/run/docker.sock" ]; then
    echo "[Entrypoint] Docker socket verified at /var/run/docker.sock"
else
    echo "[Entrypoint] Notice: /var/run/docker.sock is not currently mounted (set CC_MOUNT_DOCKER_SOCKET=true in Clever Cloud)"
fi

# 7. Start Go Gateway Engine in background and capture PID
echo "[Entrypoint] Launching Gateway engine..."
/app/gateway &
GATEWAY_PID=$!

echo "[Entrypoint] Services initialized (Gateway PID: $GATEWAY_PID, Admin PID: ${NEXT_PID:-N/A}). Listening for signals..."

# 7. Wait for the Gateway process; when wait unblocks on signal, trap executes
wait "$GATEWAY_PID"

