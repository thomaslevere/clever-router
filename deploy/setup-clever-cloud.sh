#!/usr/bin/env bash
# ==============================================================================
# CleverRoute — Automated Clever Cloud Setup & Deployment Helper
#
# Usage:
#   ./deploy/setup-clever-cloud.sh [APP_NAME] [ALIAS]
#
# Example:
#   ./deploy/setup-clever-cloud.sh clever-route clever-route
# ==============================================================================

set -euo pipefail

APP_NAME="${1:-clever-router}"
ALIAS="${2:-clever-router}"

echo "======================================================"
echo "🚀 CleverRoute — Clever Cloud Automated Setup"
echo "   App Name: ${APP_NAME}"
echo "   Alias:    ${ALIAS}"
echo "======================================================"

# 1. Check prerequisites
command -v clever >/dev/null 2>&1 || { echo "❌ clever-tools CLI is required. Install with: npm i -g clever-tools"; exit 1; }
command -v openssl >/dev/null 2>&1 || { echo "❌ openssl is required for cryptographic key generation."; exit 1; }

# 2. Check or create application
if ! clever status --alias "${ALIAS}" >/dev/null 2>&1; then
    echo "📦 Creating new Docker application: ${APP_NAME}..."
    clever create --type docker "${APP_NAME}" --alias "${ALIAS}"
else
    echo "✓ Application '${APP_NAME}' already exists and is linked."
fi

# 3. Create & link PostgreSQL add-on if not already linked
PG_ADDON="cr-pg-${ALIAS}"
echo "🗄️ Checking PostgreSQL add-on..."
if ! clever env --alias "${ALIAS}" | grep -q "POSTGRESQL_ADDON_URI"; then
    echo "   Creating PostgreSQL add-on (${PG_ADDON})..."
    clever addon create postgresql-addon --plan xs_sml "${PG_ADDON}" --yes || true
    echo "   Linking PostgreSQL add-on..."
    clever service link-addon "${PG_ADDON}" --alias "${ALIAS}" || true
else
    echo "✓ PostgreSQL add-on is already linked."
fi

# 4. Create & link Redis add-on if not already linked
REDIS_ADDON="cr-redis-${ALIAS}"
echo "⚡ Checking Redis add-on..."
if ! clever env --alias "${ALIAS}" | grep -q "REDIS_URL"; then
    echo "   Creating Redis add-on (${REDIS_ADDON})..."
    clever addon create redis-addon --plan s_sml "${REDIS_ADDON}" --yes || true
    echo "   Linking Redis add-on..."
    clever service link-addon "${REDIS_ADDON}" --alias "${ALIAS}" || true
else
    echo "✓ Redis add-on is already linked."
fi

# 5. Create & link Cellar S3 add-on if not already linked
CELLAR_ADDON="cr-cellar-${ALIAS}"
echo "🪣 Checking Cellar S3 add-on..."
if ! clever env --alias "${ALIAS}" | grep -q "CELLAR_ADDON_HOST"; then
    echo "   Creating Cellar add-on (${CELLAR_ADDON})..."
    clever addon create cellar-addon "${CELLAR_ADDON}" --yes || true
    echo "   Linking Cellar add-on..."
    clever service link-addon "${CELLAR_ADDON}" --alias "${ALIAS}" || true
else
    echo "✓ Cellar S3 add-on is already linked."
fi

# 6. Set essential environment variables
echo "⚙️ Configuring environment variables..."
clever env set CC_DOCKER_EXPOSED_HTTP_PORT 8080 --alias "${ALIAS}"
clever env set CC_HEALTH_CHECK_PATH /healthz --alias "${ALIAS}"
clever env set CC_MOUNT_DOCKER_SOCKET true --alias "${ALIAS}"
clever env set APP_ENV "production" --alias "${ALIAS}"
clever env set ALLOWED_IMAGES "diegosouzapw/omniroute:latest,decolua/9router:latest,ghcr.io/decolua/9router:latest,9router/9router:latest,ghcr.io/berriai/litellm:main-stable" --alias "${ALIAS}"
clever env set CELLAR_BUCKET "clever-router-storage" --alias "${ALIAS}"
clever env set VOLUME_SCRATCH_DIR "/tmp/clever_router_volumes" --alias "${ALIAS}"

# Generate and set 64-character hex ENCRYPTION_KEY if missing
CURRENT_ENC=$(clever env --alias "${ALIAS}" | grep "^ENCRYPTION_KEY=" | cut -d'=' -f2- | tr -d '"' || true)
if [ -z "${CURRENT_ENC}" ] || [ "${#CURRENT_ENC}" -ne 64 ]; then
    NEW_ENC=$(openssl rand -hex 32)
    echo "   Generated 64-char AES-256 ENCRYPTION_KEY."
    clever env set ENCRYPTION_KEY "${NEW_ENC}" --alias "${ALIAS}"
else
    echo "✓ Valid 64-char ENCRYPTION_KEY is already set."
fi

# Generate and set ADMIN_API_KEY if missing
CURRENT_ADMIN=$(clever env --alias "${ALIAS}" | grep "^ADMIN_API_KEY=" | cut -d'=' -f2- | tr -d '"' || true)
if [ -z "${CURRENT_ADMIN}" ]; then
    NEW_ADMIN=$(openssl rand -hex 24)
    echo "   Generated ADMIN_API_KEY."
    clever env set ADMIN_API_KEY "${NEW_ADMIN}" --alias "${ALIAS}"
else
    echo "✓ ADMIN_API_KEY is already set."
fi

echo ""
echo "======================================================"
echo "✅ Setup Complete!"
echo "To deploy, push your git branch to GitHub / Clever Cloud:"
echo "   git push origin main"
echo "======================================================"
