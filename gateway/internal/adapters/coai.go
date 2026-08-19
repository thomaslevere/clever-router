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

// CoAIAdapter manages a CoAI (coaidev/coai / Chat Nio) container.
//
// CoAI is an enterprise-grade multi-tenant AI aggregation gateway with multi-channel
// load balancing, billing, model caching, conversation synchronization, and file parsing.
// It listens on port 8094 by default, serves OpenAI-compatible /v1 endpoints, provides a
// modern web dashboard at / or /dashboard, and persists state in /config and /storage.
type CoAIAdapter struct{}

func (CoAIAdapter) Type() string { return "coai" }

func (CoAIAdapter) InternalPort(r *store.Router) int {
	if p := intConfig(r, "internal_port"); p > 0 {
		return p
	}
	return 8094
}

// HealthPath probes CoAI's /api/status or /v1/models endpoint which verifies the engine is active.
func (CoAIAdapter) HealthPath(r *store.Router) string {
	if p := strConfig(r, "health_path"); p != "" {
		return p
	}
	return "/api/status"
}

// ModelsPath is the OpenAI-compatible model listing endpoint.
func (CoAIAdapter) ModelsPath(r *store.Router) string {
	if p := strConfig(r, "models_path"); p != "" {
		return p
	}
	return "/v1/models"
}

func (CoAIAdapter) NativePanelPath(r *store.Router) string {
	if p := strConfig(r, "native_panel_path"); p != "" {
		return p
	}
	return "/dashboard"
}

func (CoAIAdapter) DeclaredVolumes(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}
	return []string{"/config", "/storage", dataPath}
}

func (CoAIAdapter) Mounts(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}
	volumeConfig := "clever-route-" + r.Slug + "-config"
	volumeStorage := "clever-route-" + r.Slug + "-storage"
	volumeData := "clever-route-" + r.Slug + "-data"
	return []string{
		volumeConfig + ":/config",
		volumeStorage + ":/storage",
		volumeData + ":" + dataPath,
	}
}

// EnsurePermanentSecrets verifies that a stable secret exists for CoAI.
func (CoAIAdapter) EnsurePermanentSecrets(ctx context.Context, r *store.Router, box *secrets.Box) ([]store.EnvVariable, bool) {
	hasSecret := false
	for _, ev := range r.EnvVars {
		if (ev.Key == "COAI_ADMIN_KEY" || ev.Key == "JWT_SECRET" || ev.Key == "SECRET_KEY") && strings.TrimSpace(ev.Value) != "" {
			hasSecret = true
			break
		}
	}

	if hasSecret {
		return r.EnvVars, false
	}

	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		log.Printf("[coai] warning: crypto/rand error: %v", err)
	}
	secretHex := "coai-" + hex.EncodeToString(bytes)

	cipher, err := secrets.EncryptValue(box, secretHex)
	if err != nil {
		log.Printf("[coai] error encrypting COAI_ADMIN_KEY: %v", err)
		return r.EnvVars, false
	}

	newEnv := append(r.EnvVars,
		store.EnvVariable{
			Key:      "COAI_ADMIN_KEY",
			Value:    cipher,
			IsSecret: true,
		},
		store.EnvVariable{
			Key:      "JWT_SECRET",
			Value:    cipher,
			IsSecret: true,
		},
	)

	return newEnv, true
}

func (CoAIAdapter) Env(r *store.Router, decrypted map[string]string) []string {
	port := strconv.Itoa(CoAIAdapter{}.InternalPort(r))
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}

	envMap := make(map[string]string)

	// 1. High-Performance Multi-Core Go Runtime Defaults (Host has 12 vCPUs / 24 GB RAM shared)
	envMap["PORT"] = port
	envMap["SERVER_PORT"] = port
	envMap["HOST"] = "0.0.0.0"
	envMap["CONFIG_DIR"] = "/config"
	envMap["STORAGE_DIR"] = "/storage"
	envMap["DATA_DIR"] = dataPath
	envMap["GOMAXPROCS"] = "12"
	envMap["GOMEMLIMIT"] = "4GiB"
	envMap["GODEBUG"] = "madvdontneed=1"
	envMap["SERVE_STATIC"] = "true"
	envMap["CACHE_ENABLED"] = "true"
	envMap["LOG_LEVEL"] = "info"

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
func (CoAIAdapter) ResourceLimits(r *store.Router) ContainerResources {
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

func (CoAIAdapter) ParseModels(r *store.Router, body []byte) ([]store.Model, error) {
	var resp openAIModelsResponse
	if err := json.Unmarshal(body, &resp); err == nil && len(resp.Data) > 0 {
		out := make([]store.Model, 0, len(resp.Data))
		for _, m := range resp.Data {
			provider := m.OwnedBy
			if provider == "" {
				if parts := strings.SplitN(m.ID, "/", 2); len(parts) == 2 {
					provider = parts[0]
				} else {
					provider = "coai"
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
				provider = "coai"
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
