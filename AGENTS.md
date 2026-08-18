# CleverRoute — AI Router Control Plane

Self-hosted, single-tenant control plane that manages multiple AI
router/gateway runtimes (OmniRoute first) behind a stable, namespaced API,
deployed as a single Clever Cloud Docker application with state that survives
container restarts.

## Stack
- **Gateway:** Go + Gin + PostgreSQL (system of record) + Redis (hot routing table + pub/sub)
- **Admin UI:** Next.js 14 (App Router, standalone output), served behind the gateway
- **Deployment:** one Clever Cloud Docker app, port 8080, sibling router containers via the Docker socket

## Repository layout
```
gateway/   Go control plane + namespaced proxy (Gin)
  cmd/server/      main wiring
  internal/
    config/        env loading
    store/         PostgreSQL + embedded migrations
    cache/         Redis + pub/sub
    secrets/       AES-256-GCM envelope encryption
    adapters/      Adapter interface + Docker runtime + OmniRoute adapter
    router/        in-memory hot routing table
    proxy/         namespaced reverse proxy + SSE usage sniffer
    keys/          virtual-key auth + rate limits
    runtime/       supervisor (boot reconcile) + health checker
    api/           admin REST handlers + middleware
admin/     Next.js admin panel (standalone)
deploy/    Dockerfile entrypoint + env example
Dockerfile multi-stage build (Go + Next.js) for Clever Cloud
docker-compose.yml  local dev dependencies (postgres, redis, minio)
```

## Build & verify
```bash
# Go gateway
cd gateway
go build ./...
go vet ./...
go mod tidy

# Admin panel
cd admin
npm ci
npm run build      # type-checks + standalone build
npm run lint
```

Lint/typecheck commands (run before committing):
- Go: `go vet ./...` (in `gateway/`)
- Admin: `npm run lint` and `npx tsc --noEmit` (in `admin/`)

## Run locally
```bash
# 1. dependencies
docker compose up -d            # postgres :5432, redis :6379, minio :9000

# 2. env (gateway reads DATABASE_URL, REDIS_URL, ENCRYPTION_KEY, ADMIN_API_KEY)
export DATABASE_URL="postgresql://clever:clever@localhost:5432/cleverroute"
export REDIS_URL="localhost:6379"
export ENCRYPTION_KEY="$(openssl rand -hex 32)"
export ADMIN_API_KEY="dev-admin-token"
export APP_ENV=dev
export ALLOWED_IMAGES="diegosouzapw/omniroute:latest,decolua/9router:latest,ghcr.io/decolua/9router:latest,9router/9router:latest"

# 3. gateway (migrations run on boot)
cd gateway && go run ./cmd/server

# 4. admin UI (separate terminal) — dev proxy to gateway on :8080
cd admin
NEXT_PUBLIC_API_BASE=http://localhost:8080/admin/api npm run dev
# open http://localhost:3000/admin  (sign in with ADMIN_API_KEY)
```
Note: Docker-socket sibling containers require a real Docker daemon (local dev:
use the host socket). On Clever Cloud set `CC_MOUNT_DOCKER_SOCKET=true`.

## Deploy to Clever Cloud

> See the complete AI deployment playbook in [.skills/clever-cloud-deploy/SKILL.md](file:///.skills/clever-cloud-deploy/SKILL.md).

### Automated Setup
```bash
./deploy/setup-clever-cloud.sh clever-router clever-router
git push origin main
```

### Manual Setup
```bash
clever create --type docker clever-route
clever domain add my-domain.com

clever addon create postgresql-addon --plan xs_sml cr-pg
clever addon create redis-addon        cr-redis
clever addon create cellar-addon       cr-cellar     # optional: snapshots/backups
clever service link-addon cr-pg
clever service link-addon cr-redis
clever service link-addon cr-cellar

clever env set CC_DOCKER_EXPOSED_HTTP_PORT 8080
clever env set CC_HEALTH_CHECK_PATH /healthz
clever env set CC_MOUNT_DOCKER_SOCKET true
clever env set ENCRYPTION_KEY "$(openssl rand -hex 32)"
clever env set ADMIN_API_KEY "$(openssl rand -hex 24)"
clever env set ALLOWED_IMAGES "diegosouzapw/omniroute:latest,ghcr.io/berriai/litellm:main-stable"

git push clever master
```
- Clever Cloud builds the root `Dockerfile`; the app must listen on **8080**.
- `/healthz` checks Postgres + Redis (deploy fails if a dependency is down).
- Do NOT use an FS Bucket (not supported for Docker apps).

## API surface
```
GET    /healthz                       liveness
ANY    /admin/*                       admin UI (reverse-proxied to Next.js)
ANY    /admin/api/*                   admin REST (Bearer ADMIN_API_KEY)
ANY    /:slug/v1/*                    OpenAI-compatible → router container
ANY    /:slug/{native}/*              passthrough → router container
```
Admin REST: `/routers`, `/routers/:id/{start,stop,restart,discover,health,models,logs,credentials}`,
`/keys`, `/audit`, `/system`. Virtual keys authenticate the AI proxy
(`Authorization: Bearer cr-...`).

## Architecture notes
- Two persistence layers: control metadata in Postgres; router-owned native
  state in Docker named volumes (snapshot to Cellar in Phase 3).
- Hot routing table loaded into RAM on boot, refreshed via Redis pub/sub
  (`config:reload`) — admin edits propagate in under 1ms without restart.
- Streaming uses a tee'd byte sniffer (no per-chunk JSON decode) for usage
  capture while bytes stream to the client immediately.
- The Adapter interface (`adapters.Adapter`) is the key abstraction; swapping
  the Docker-socket runtime for the Clever Cloud API (Option B) is contained.
- Image allowlist enforced server-side — Docker-socket access is privileged.
```
