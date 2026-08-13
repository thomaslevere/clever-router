---
name: clever-cloud-deploy
description: Fast, reliable, automated deployment playbook for CleverRoute on Clever Cloud (Docker app with PostgreSQL and Redis add-ons).
version: 1.0.0
tags: [deployment, clever-cloud, docker, golang, nextjs, postgresql, redis]
---

# Skill: Deploy CleverRoute to Clever Cloud

This skill provides step-by-step instructions and automated commands for any AI agent or engineer to deploy the CleverRoute AI Router Control Plane to Clever Cloud rapidly, correctly, and without runtime errors.

---

## 🎯 Architecture Overview

- **App Type**: Clever Cloud Docker Application
- **Listening Port**: `8080` (Go gateway reverse-proxies internal Next.js on `3000`)
- **Add-ons**:
  - `postgresql-addon` (System of Record — automatic `POSTGRESQL_ADDON_URI`)
  - `redis-addon` (Hot Routing Table & Pub/Sub — automatic `REDIS_URL`)
- **Container Socket**: `CC_MOUNT_DOCKER_SOCKET=true` (Sibling container lifecycle for AI routers like OmniRoute)

---

## ⚡ Fast-Track: One-Command Deployment

If the application is already linked in the repository via `.clever.json`:

```bash
# 1. Ensure build passes locally
(cd gateway && go build ./... && go test ./...)
(cd admin && npm run build)

# 2. Push to GitHub (Clever Cloud builds automatically on commit)
git push origin main

# 3. Monitor deployment status
clever activity --follow
```

---

## 📋 Full Deployment Playbook (Step-by-Step for New Deployments)

### Step 1: Create the Application & Domain

```bash
# Initialize Docker application on Clever Cloud
clever create --type docker clever-route --alias clever-route

# (Optional) Add custom domain or use default *.cleverapps.io
clever domain add my-custom-router.com
```

### Step 2: Provision & Link Add-ons

```bash
# Provision PostgreSQL
clever addon create postgresql-addon --plan xs_sml cr-pg --yes

# Provision Redis
clever addon create redis-addon --plan s_sml cr-redis --yes

# (Optional) Cellar S3 Storage for future snapshots
clever addon create cellar-addon cr-cellar --yes

# Link all add-ons to the application
clever service link-addon cr-pg
clever service link-addon cr-redis
clever service link-addon cr-cellar
```

### Step 3: Configure Environment Variables

> [!IMPORTANT]
> `ENCRYPTION_KEY` **MUST** be exactly 64 hexadecimal characters (32 bytes AES-256 key).

```bash
# 1. Clever Cloud Docker & Health Check Configuration
clever env set CC_DOCKER_EXPOSED_HTTP_PORT 8080
clever env set CC_HEALTH_CHECK_PATH /healthz
clever env set CC_MOUNT_DOCKER_SOCKET true

# 2. Security Keys
# Generate a strict 64-char hex AES-256 key:
ENC_KEY=$(openssl rand -hex 32)
clever env set ENCRYPTION_KEY "$ENC_KEY"

# Generate admin API authentication key:
ADMIN_KEY=$(openssl rand -hex 24)
clever env set ADMIN_API_KEY "$ADMIN_KEY"

# 3. Router Image Allowlist & Environment
clever env set APP_ENV "production"
clever env set ALLOWED_IMAGES "diegosouzapw/omniroute:latest,ghcr.io/berriai/litellm:main-stable"
```

### Step 4: Verify and Push Code

```bash
# Pre-flight build validation
cd gateway && go vet ./... && cd ../admin && npm run lint

# Deploy via GitHub remote or Clever Cloud git remote
git push origin main
# Or direct to clever remote:
# clever deploy -b main
```

### Step 5: Verify Live Health

```bash
# 1. Check deployment progress
clever activity -F json

# 2. Once state is "OK", test health endpoint (replace with your app domain)
APP_DOMAIN=$(clever domain | head -n 1 | awk '{print $1}' | tr -d '/')
curl -sS "https://${APP_DOMAIN}/healthz"

# Expected output:
# {"postgres":true,"redis":true,"routers":0,"status":"healthy"}

# 3. Test authenticated Admin API
curl -sS -H "Authorization: Bearer ${ADMIN_KEY}" "https://${APP_DOMAIN}/admin/api/system"
```

---

## 🛡️ Critical Gotchas & How to Avoid Them

| Potential Pitfall | Prevention Rule |
|---|---|
| **ENCRYPTION_KEY length error** (`got 63 / 65 chars`) | Always generate using `openssl rand -hex 32` (exact 64 chars). The config loader strips wrapping quotes and whitespace. |
| **Go toolchain version conflict** | Always use `FROM golang:alpine AS gateway` in multi-stage [Dockerfile](file:///home/salman/Projects/golang/clever-route/Dockerfile) to support modern Go 1.25+ dependencies (e.g. `httpsnoop`). |
| **`.dockerignore` blocking required files** | Never exclude directories needed by the Dockerfile stages. Store runtime scripts inline in [Dockerfile](file:///home/salman/Projects/golang/clever-route/Dockerfile) via `RUN printf ... > /app/entrypoint.sh`. |
| **PostgreSQL Migration Errors** | Never use non-immutable expressions (like `now()`) in PostgreSQL index predicates. Use standard composite indexes and `prune_old_data()` SQL function. |
| **Duplicate JSON Output in Gin** | In [api.go](file:///home/salman/Projects/golang/clever-route/gateway/internal/api/api.go), use `a.rest.ServeHTTP(c.Writer, req)` with `c.Abort()` when proxying between internal sub-engines. |
| **Next.js `basePath: "/admin"` Navigation** | With `basePath: "/admin"` in `next.config.mjs`, all `<Link href="...">` paths must use root relative paths (`"/"`, `"/routers"`, `"/keys"`) — Next.js automatically prepends `/admin`. |
| **JSON Null vs Array** | Database slice queries in Go must initialize with `out := []Type{}` so zero-record results serialize as `[]` in JSON instead of `null`. |

---

## 🤖 AI Verification Protocol

When an AI completes deployment, it must execute this verification sequence:

1. **Query Activity**: `clever activity -F json` → confirm latest state is `"OK"`.
2. **HTTP Health**: `curl https://<app-domain>/healthz` → confirm `status: "healthy"`, `postgres: true`, `redis: true`.
3. **Admin UI Inspection**: Open browser to `https://<app-domain>/admin`:
   - Enter `ADMIN_API_KEY` → Confirm Dashboard displays 4 metric cards without errors.
   - Navigate to `/admin/keys` → Create test virtual key → Confirm key creation modal and table display.
   - Navigate to `/admin/routers` → Open `+ Add Router` → Confirm image and slug validation.
   - Navigate to `/admin/audit` → Confirm audit logs table records all administrative actions.
