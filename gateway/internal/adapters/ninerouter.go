package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/clever-route/gateway/internal/secrets"
	"github.com/clever-route/gateway/internal/store"
)

// NineRouterAdapter manages a 9Router (e.g. decolua/9router:latest) container.
//
// 9Router is an open-source, high-performance AI proxy & aggregator with token compression
// and multi-tier fallback routing. Its internal service listens on port 20128 by default,
// serves an OpenAI-compatible /v1 endpoint, and persists SQLite databases in /app/data.
type NineRouterAdapter struct{}

func (NineRouterAdapter) Type() string { return "9router" }

func (NineRouterAdapter) InternalPort(r *store.Router) int {
	if p := intConfig(r, "internal_port"); p > 0 {
		return p
	}
	return 20128
}

// HealthPath returns a lightweight path used for readiness probes.
func (NineRouterAdapter) HealthPath(r *store.Router) string {
	if p := strConfig(r, "health_path"); p != "" {
		return p
	}
	return "/v1/models"
}

// ModelsPath is the OpenAI-compatible model listing endpoint.
func (NineRouterAdapter) ModelsPath(r *store.Router) string {
	if p := strConfig(r, "models_path"); p != "" {
		return p
	}
	return "/v1/models"
}

func (NineRouterAdapter) NativePanelPath(r *store.Router) string {
	if p := strConfig(r, "native_panel_path"); p != "" {
		return p
	}
	return "/dashboard"
}

func (NineRouterAdapter) DeclaredVolumes(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}
	return []string{dataPath}
}

func (NineRouterAdapter) Mounts(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}
	volume := "clever-route-" + r.Slug
	return []string{volume + ":" + dataPath}
}

// EnsurePermanentSecrets verifies environment variables for 9Router.
func (NineRouterAdapter) EnsurePermanentSecrets(ctx context.Context, r *store.Router, box *secrets.Box) ([]store.EnvVariable, bool) {
	return r.EnvVars, false
}

func (NineRouterAdapter) Env(r *store.Router, decrypted map[string]string) []string {
	port := strconv.Itoa(NineRouterAdapter{}.InternalPort(r))
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}

	envMap := make(map[string]string)

	// 1. Baseline Defaults for 9Router
	envMap["NODE_ENV"] = "production"
	envMap["PORT"] = port
	envMap["DATA_DIR"] = dataPath
	envMap["HOME"] = dataPath
	envMap["HOST"] = "0.0.0.0"
	envMap["HOSTNAME"] = "0.0.0.0"
	envMap["HOST_BIND"] = "0.0.0.0"
	envMap["BIND"] = "0.0.0.0"
	envMap["BASE_PATH"] = "/" + r.Slug
	envMap["PREFIX"] = "/" + r.Slug
	envMap["PUBLIC_URL"] = "/" + r.Slug
	envMap["BASE_URL"] = "/" + r.Slug

	// Multi-core and concurrency optimization (Host has 12 vCPUs / 24 GB RAM shared)
	envMap["UV_THREADPOOL_SIZE"] = "12"
	envMap["NODE_OPTIONS"] = "--max-old-space-size=4096"
	envMap["GOMAXPROCS"] = "12"
	envMap["WEB_CONCURRENCY"] = "auto"

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

	// 4. Inject decrypted provider credentials as NINEROUTER_<PROVIDER>_KEY and standard fallback tokens
	for provider, key := range decrypted {
		upperProv := strings.ToUpper(strings.TrimSpace(provider))
		envMap["NINEROUTER_"+upperProv+"_KEY"] = key

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
func (NineRouterAdapter) ResourceLimits(r *store.Router) ContainerResources {
	res := ContainerResources{
		MemoryBytes: 8 * 1024 * 1024 * 1024, // 8 GB RAM limit (host has 24GB shared)
		NanoCPUs:    12_000_000_000,          // 12 CPUs quota (host has 12 cores shared)
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

func (NineRouterAdapter) ParseModels(r *store.Router, body []byte) ([]store.Model, error) {
	var resp openAIModelsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	out := make([]store.Model, 0, len(resp.Data))
	for _, m := range resp.Data {
		provider := m.OwnedBy
		if provider == "" {
			provider = "9router"
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
