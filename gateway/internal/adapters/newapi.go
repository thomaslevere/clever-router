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

// NewApiAdapter manages a New-API (QuantumNous/new-api or calciumion/new-api:latest) container.
//
// New-API is an open-source, high-concurrency multi-provider AI API aggregator with token
// billing, multi-channel load balancing, and user management.
// It listens on port 3000 by default, serves OpenAI-compatible /v1 endpoints, and persists SQLite
// databases and uploaded assets in /data (one-api.db, one-api.db-wal, one-api.db-shm).
type NewApiAdapter struct{}

func (NewApiAdapter) Type() string { return "new-api" }

func (NewApiAdapter) InternalPort(r *store.Router) int {
	if p := intConfig(r, "internal_port"); p > 0 {
		return p
	}
	return 3000
}

// HealthPath probes New-API's /api/status endpoint which returns status JSON without authentication.
func (NewApiAdapter) HealthPath(r *store.Router) string {
	if p := strConfig(r, "health_path"); p != "" {
		return p
	}
	return "/api/status"
}

// ModelsPath is the OpenAI-compatible model listing endpoint.
func (NewApiAdapter) ModelsPath(r *store.Router) string {
	if p := strConfig(r, "models_path"); p != "" {
		return p
	}
	return "/v1/models"
}

func (NewApiAdapter) NativePanelPath(r *store.Router) string {
	if p := strConfig(r, "native_panel_path"); p != "" {
		return p
	}
	return "/"
}

func (NewApiAdapter) DeclaredVolumes(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/data"
	}
	return []string{dataPath}
}

func (NewApiAdapter) Mounts(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/data"
	}
	volume := "clever-route-" + r.Slug
	return []string{volume + ":" + dataPath}
}

// EnsurePermanentSecrets verifies that a stable SESSION_SECRET exists for New-API.
func (NewApiAdapter) EnsurePermanentSecrets(ctx context.Context, r *store.Router, box *secrets.Box) ([]store.EnvVariable, bool) {
	hasSecret := false
	for _, ev := range r.EnvVars {
		if ev.Key == "SESSION_SECRET" && strings.TrimSpace(ev.Value) != "" {
			hasSecret = true
			break
		}
	}

	if hasSecret {
		return r.EnvVars, false
	}

	// Generate 32 cryptographically secure random bytes for session authentication
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		log.Printf("[new-api] warning: crypto/rand error: %v", err)
	}
	secretHex := hex.EncodeToString(bytes)

	cipher, err := secrets.EncryptValue(box, secretHex)
	if err != nil {
		log.Printf("[new-api] error encrypting SESSION_SECRET: %v", err)
		return r.EnvVars, false
	}

	newEnv := append(r.EnvVars, store.EnvVariable{
		Key:      "SESSION_SECRET",
		Value:    cipher,
		IsSecret: true,
	})

	return newEnv, true
}

func (NewApiAdapter) Env(r *store.Router, decrypted map[string]string) []string {
	port := strconv.Itoa(NewApiAdapter{}.InternalPort(r))
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/data"
	}

	envMap := make(map[string]string)

	// 1. High-Performance Multi-Core Go Defaults (Host has 12 vCPUs / 24 GB RAM shared)
	envMap["PORT"] = port
	envMap["DATA_DIR"] = dataPath
	envMap["GLOBAL_API_RATE_LIMIT_ENABLE"] = "false"
	envMap["GLOBAL_WEB_RATE_LIMIT_ENABLE"] = "false"
	envMap["BATCH_UPDATE_ENABLED"] = "true"
	envMap["BATCH_UPDATE_INTERVAL"] = "5"
	envMap["MEMORY_CACHE_ENABLED"] = "true"
	envMap["GOMAXPROCS"] = "12"
	envMap["GOMEMLIMIT"] = "18GiB"
	envMap["GOGC"] = "120"
	envMap["SQLITE_BUSY_TIMEOUT"] = "5000"

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
func (NewApiAdapter) ResourceLimits(r *store.Router) ContainerResources {
	res := ContainerResources{
		MemoryBytes: 8 * 1024 * 1024 * 1024, // 8 GB RAM limit (host has 24GB shared)
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

func (NewApiAdapter) ParseModels(r *store.Router, body []byte) ([]store.Model, error) {
	var resp openAIModelsResponse
	if err := json.Unmarshal(body, &resp); err == nil && len(resp.Data) > 0 {
		out := make([]store.Model, 0, len(resp.Data))
		for _, m := range resp.Data {
			provider := m.OwnedBy
			if provider == "" {
				if parts := strings.SplitN(m.ID, "/", 2); len(parts) == 2 {
					provider = parts[0]
				} else {
					provider = "new-api"
				}
			}
			out = append(out, store.Model{
				RouterID:   r.ID,
				ModelID:    m.ID,
				Provider:   provider,
				Modalities: "chat",
			})
		}
		return out, nil
	}

	// Fallback array format: [{"id": "model-name", ...}]
	var list []struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
		OwnedBy  string `json:"owned_by"`
	}
	if err := json.Unmarshal(body, &list); err == nil && len(list) > 0 {
		out := make([]store.Model, 0, len(list))
		for _, m := range list {
			provider := m.OwnedBy
			if provider == "" {
				provider = m.Provider
			}
			if provider == "" {
				provider = "new-api"
			}
			out = append(out, store.Model{
				RouterID:   r.ID,
				ModelID:    m.ID,
				Provider:   provider,
				Modalities: "chat",
			})
		}
		return out, nil
	}

	return nil, fmt.Errorf("unrecognized models response format")
}
