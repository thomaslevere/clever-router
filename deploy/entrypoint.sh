#!/bin/sh
set -e

# Start the Next.js admin UI in the background (internal only, :3000).
(
  cd /app/admin
  PORT=3000 HOSTNAME=127.0.0.1 node server.js
) &
NEXT_PID=$!

# M-4 FIX: Wait for Next.js to be ready before starting the gateway.
# The gateway's reverse proxy to /admin/* returns 502 if Next.js hasn't bound
# yet. We poll :3000 for up to 30 seconds with 1-second intervals.
echo "[entrypoint] waiting for Next.js to be ready on :3000…"
MAX_WAIT=30
i=0
while [ $i -lt $MAX_WAIT ]; do
  if wget -q --spider http://127.0.0.1:3000/admin 2>/dev/null; then
    echo "[entrypoint] Next.js ready after ${i}s"
    break
  fi
  sleep 1
  i=$((i + 1))
done
if [ $i -eq $MAX_WAIT ]; then
  echo "[entrypoint] warning: Next.js did not respond in ${MAX_WAIT}s — continuing anyway"
fi

# The Go gateway is the only public listener (:8080). It reverse-proxies
# /admin/* to the UI above and /{slug}/* to managed router containers.
exec /app/gateway
