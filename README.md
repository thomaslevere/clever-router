# CleverRoute — AI Router Control Plane

Self-hosted, single-tenant AI Router & Gateway Control Plane deployed on Clever Cloud with PostgreSQL (system of record), Redis (hot routing table + pub/sub), and Docker socket runtime management.

---


## 🚀 Quick Deployment to Clever Cloud

For automated AI agent deployment and step-by-step instructions, see the dedicated skill:
👉 **[Clever Cloud Deployment Skill](file:///.skills/clever-cloud-deploy/SKILL.md)**

### One-Command Setup & Deploy

```bash
# Automated setup (provisions PostgreSQL, Redis, sets 64-char ENCRYPTION_KEY, port, healthz)
./deploy/setup-clever-cloud.sh clever-router clever-router

# Deploy
git push origin main
```

---

## 🏗️ Architecture

- **Control Plane**: Go (Gin) listening on `:8080`
- **Admin UI**: Next.js 14 (App Router) running internally on `:3000`, proxied under `/admin/*`
- **Database**: PostgreSQL with automatic schema migrations ([0001_init.sql](file:///gateway/internal/store/migrations/0001_init.sql), [0002_improvements.sql](file:///gateway/internal/store/migrations/0002_improvements.sql))
- **Hot Table & Cache**: Redis with atomic Lua rate limiter and automatic pub/sub reconnection
- **AI Routers**: Managed sibling containers over `/var/run/docker.sock` with memory/CPU resource boundaries and dedicated network isolation (`clever-route-net`)

---

## 🛠️ Local Development

```bash
# 1. Start local PostgreSQL, Redis, MinIO
docker compose up -d

# 2. Run Go Gateway (runs migrations on boot)
export DATABASE_URL="postgresql://clever:clever@localhost:5432/cleverroute"
export REDIS_URL="localhost:6379"
export ENCRYPTION_KEY="$(openssl rand -hex 32)"
export ADMIN_API_KEY="dev-admin-token"
export APP_ENV=dev
export ALLOWED_IMAGES="diegosouzapw/omniroute:latest"
cd gateway && go run ./cmd/server

# 3. Run Admin UI (separate terminal)
cd admin && NEXT_PUBLIC_API_BASE=http://localhost:8080/admin/api npm run dev
```

---

## 🧪 Verification & Testing

```bash
# Gateway tests
cd gateway && go test ./... && go vet ./...

# Admin type-check & lint
cd admin && npx tsc --noEmit && npm run lint
```
