package adapters

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/clever-route/gateway/internal/secrets"
	"github.com/clever-route/gateway/internal/store"
)

// OpenConnectorAdapter manages an OpenConnector (oomol-lab/open-connector) container.
//
// OpenConnector is an open-source authentication and integration gateway connecting 1,000+
// SaaS providers and 10,000+ prebuilt Actions to AI agents via HTTP, Model Context Protocol (MCP),
// and OpenAPI. It listens on port 3000 by default, serves tool execution endpoints (/v1/actions/*),
// connector catalogs (/v1/connectors/*), MCP server (/mcp), interactive documentation (/docs),
// and persists state in /app/data, /config, and /storage.
type OpenConnectorAdapter struct{}

func (OpenConnectorAdapter) Type() string { return "openconnector" }

func (OpenConnectorAdapter) InternalPort(r *store.Router) int {
	if p := intConfig(r, "internal_port"); p > 0 {
		return p
	}
	return 3000
}

// HealthPath probes OpenConnector's /health, /api/health, or /v1/connectors endpoint.
func (OpenConnectorAdapter) HealthPath(r *store.Router) string {
	if p := strConfig(r, "health_path"); p != "" {
		return p
	}
	return "/health"
}

// ModelsPath is the model or tool actions listing endpoint.
func (OpenConnectorAdapter) ModelsPath(r *store.Router) string {
	if p := strConfig(r, "models_path"); p != "" {
		return p
	}
	return "/v1/models"
}

func (OpenConnectorAdapter) NativePanelPath(r *store.Router) string {
	if p := strConfig(r, "native_panel_path"); p != "" {
		return p
	}
	return "/docs"
}

func (OpenConnectorAdapter) DeclaredVolumes(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}
	return []string{dataPath, "/config", "/storage"}
}

func (OpenConnectorAdapter) Mounts(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}
	volumeData := "clever-route-" + r.Slug + "-data"
	volumeConfig := "clever-route-" + r.Slug + "-config"
	volumeStorage := "clever-route-" + r.Slug + "-storage"
	return []string{
		volumeData + ":" + dataPath,
		volumeConfig + ":/config",
		volumeStorage + ":/storage",
	}
}

// EnsurePermanentSecrets verifies that stable secrets exist for OpenConnector (min 32 bytes).
func (OpenConnectorAdapter) EnsurePermanentSecrets(ctx context.Context, r *store.Router, box *secrets.Box) ([]store.EnvVariable, bool) {
	hasSecret := false
	for _, ev := range r.EnvVars {
		if (ev.Key == "AUTH_SECRET" || ev.Key == "JWT_SECRET" || ev.Key == "ENCRYPTION_KEY") && len(strings.TrimSpace(ev.Value)) >= 32 {
			hasSecret = true
			break
		}
	}

	if hasSecret {
		return r.EnvVars, false
	}

	bytes1 := make([]byte, 32)
	bytes2 := make([]byte, 32)
	if _, err := rand.Read(bytes1); err != nil {
		log.Printf("[openconnector] warning: crypto/rand error: %v", err)
	}
	if _, err := rand.Read(bytes2); err != nil {
		log.Printf("[openconnector] warning: crypto/rand error: %v", err)
	}
	authSecret := hex.EncodeToString(bytes1)
	encKey := hex.EncodeToString(bytes2)

	cipherAuth, err := secrets.EncryptValue(box, authSecret)
	if err != nil {
		log.Printf("[openconnector] error encrypting AUTH_SECRET: %v", err)
		return r.EnvVars, false
	}

	cipherKey, err := secrets.EncryptValue(box, encKey)
	if err != nil {
		log.Printf("[openconnector] error encrypting ENCRYPTION_KEY: %v", err)
		return r.EnvVars, false
	}

	newEnv := append(r.EnvVars,
		store.EnvVariable{
			Key:      "AUTH_SECRET",
			Value:    cipherAuth,
			IsSecret: true,
		},
		store.EnvVariable{
			Key:      "JWT_SECRET",
			Value:    cipherAuth,
			IsSecret: true,
		},
		store.EnvVariable{
			Key:      "ENCRYPTION_KEY",
			Value:    cipherKey,
			IsSecret: true,
		},
	)

	return newEnv, true
}

func (OpenConnectorAdapter) Env(r *store.Router, decrypted map[string]string) []string {
	port := strconv.Itoa(OpenConnectorAdapter{}.InternalPort(r))
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}

	envMap := make(map[string]string)

	// 1. High-Performance Multi-Core Node.js / Go Runtime Defaults (Host has 12 vCPUs / 24 GB RAM shared)
	envMap["PORT"] = port
	envMap["SERVER_PORT"] = port
	envMap["HOST"] = "0.0.0.0"
	envMap["DATA_DIR"] = dataPath
	envMap["CONFIG_DIR"] = "/config"
	envMap["STORAGE_DIR"] = "/storage"
	envMap["DATABASE_URL"] = fmt.Sprintf("file:%s/openconnector.db?_journal_mode=WAL", dataPath)
	envMap["NODE_ENV"] = "production"
	envMap["NODE_OPTIONS"] = "--max-old-space-size=3072"
	envMap["WEB_CONCURRENCY"] = "4"
	envMap["UV_THREADPOOL_SIZE"] = "12"
	envMap["GOMAXPROCS"] = "12"
	envMap["GOMEMLIMIT"] = "3GiB"
	envMap["LOG_LEVEL"] = "info"
	envMap["METRICS_ENABLED"] = "true"
	envMap["ENABLE_DOCS"] = "true"

	// Ensure strong 64-char AUTH_SECRET and ENCRYPTION_KEY defaults
	bytes := make([]byte, 32)
	_, _ = rand.Read(bytes)
	defaultSecret := hex.EncodeToString(bytes)
	envMap["AUTH_SECRET"] = defaultSecret
	envMap["JWT_SECRET"] = defaultSecret
	envMap["ENCRYPTION_KEY"] = defaultSecret

	// 2. Legacy router config["env"] (map) if present
	if cfgEnv, ok := r.Config["env"].(map[string]any); ok {
		for k, v := range cfgEnv {
			envMap[k] = toStr(v)
		}
	}

	// 3. User-Defined & Static Environment Variables (from DB, already decrypted in memory)
	for _, item := range r.EnvVars {
		k := strings.TrimSpace(item.Key)
		if k != "" {
			envMap[k] = item.Value
		}
	}

	// 4. Inject decrypted provider credentials
	for provider, key := range decrypted {
		upperProv := strings.ToUpper(strings.TrimSpace(provider))
		stdKey := upperProv + "_API_KEY"
		if _, exists := envMap[stdKey]; !exists {
			envMap[stdKey] = key
		}
	}

	// 5. Convert to []string formatted as "KEY=VALUE"
	finalEnv := make([]string, 0, len(envMap))
	for k, v := range envMap {
		finalEnv = append(finalEnv, fmt.Sprintf("%s=%s", k, v))
	}
	return finalEnv
}

// ResourceLimits returns resource boundaries optimized for high concurrency on multi-core hosts.
func (OpenConnectorAdapter) ResourceLimits(r *store.Router) ContainerResources {
	res := ContainerResources{
		MemoryBytes: 6 * 1024 * 1024 * 1024, // 6 GB RAM limit (host has 24GB shared)
		NanoCPUs:    12_000_000_000,         // 12 CPUs quota (host has 12 cores shared)
		PidsLimit:   2048,
	}
	if lim, ok := r.Config["resource_limits"].(map[string]any); ok {
		if mb, ok := lim["memory_mb"].(float64); ok && mb > 0 {
			res.MemoryBytes = int64(mb) * 1024 * 1024
		}
		if cpu, ok := lim["cpu"].(float64); ok && cpu > 0 {
			res.NanoCPUs = int64(cpu * 1_000_000_000)
		}
		if pids, ok := lim["pids_limit"].(float64); ok && pids > 0 {
			res.PidsLimit = int64(pids)
		}
	}
	return res
}

func (OpenConnectorAdapter) ParseModels(r *store.Router, body []byte) ([]store.Model, error) {
	var resp openAIModelsResponse
	if err := json.Unmarshal(body, &resp); err == nil && len(resp.Data) > 0 {
		out := make([]store.Model, 0, len(resp.Data))
		for _, m := range resp.Data {
			provider := m.OwnedBy
			if provider == "" {
				if parts := strings.SplitN(m.ID, "/", 2); len(parts) == 2 {
					provider = parts[0]
				} else {
					provider = "openconnector"
				}
			}
			out = append(out, store.Model{
				RouterID:   r.ID,
				ModelID:    m.ID,
				Provider:   provider,
				Modalities: "tool,chat",
			})
		}
		return out, nil
	}

	// Fallback array format: [{"id": "action-name", ...}] or [{"name": "connector", ...}]
	var list []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Provider string `json:"provider"`
		OwnedBy  string `json:"owned_by"`
	}
	if err := json.Unmarshal(body, &list); err == nil && len(list) > 0 {
		out := make([]store.Model, 0, len(list))
		for _, m := range list {
			id := m.ID
			if id == "" {
				id = m.Name
			}
			provider := m.OwnedBy
			if provider == "" {
				provider = m.Provider
			}
			if provider == "" {
				provider = "openconnector"
			}
			out = append(out, store.Model{
				RouterID:   r.ID,
				ModelID:    id,
				Provider:   provider,
				Modalities: "tool,chat",
			})
		}
		return out, nil
	}

	return nil, fmt.Errorf("unrecognized models response format")
}
