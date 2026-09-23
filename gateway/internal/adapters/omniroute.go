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

// OmniRouteAdapter manages a diegosouzapw/omniroute container.
//
// OmniRoute is a Next.js app serving its OpenAI-compatible API and native
// dashboard together on port 20128. Its state is SQLite-on-disk, so we bind a
// named volume at its data directory so configuration (providers/keys entered
// in the native panel) survives container restarts.
type OmniRouteAdapter struct{}

func (OmniRouteAdapter) Type() string { return "omniroute" }

func (OmniRouteAdapter) InternalPort(r *store.Router) int {
	if p := intConfig(r, "internal_port"); p > 0 {
		return p
	}
	return 20128
}

// HealthPath returns a lightweight path used only for readiness probes.
// OmniRoute includes a native /healthz route that responds immediately with 'ok'
// and zero database/auth overhead.
func (OmniRouteAdapter) HealthPath(r *store.Router) string {
	if p := strConfig(r, "health_path"); p != "" {
		return p
	}
	return "/healthz"
}

// ModelsPath is the OpenAI-compatible model listing endpoint.
// Kept as a separate method from HealthPath so that future adapters with a
// dedicated /health can diverge without breaking model discovery.
func (OmniRouteAdapter) ModelsPath(r *store.Router) string {
	if p := strConfig(r, "models_path"); p != "" {
		return p
	}
	return "/v1/models"
}

func (OmniRouteAdapter) NativePanelPath(r *store.Router) string {
	if p := strConfig(r, "native_panel_path"); p != "" {
		return p
	}
	return "/dashboard"
}

func (OmniRouteAdapter) DeclaredVolumes(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}
	return []string{dataPath}
}

func (OmniRouteAdapter) Mounts(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}
	volume := "clever-route-" + r.Slug
	return []string{volume + ":" + dataPath}
}

// EnsurePermanentSecrets verifies environment variables for OmniRoute.
// It preserves whatever environment variables the user configured and does NOT inject
// any predefined INITIAL_PASSWORD or preset variables, allowing OmniRoute to launch its
// native Initial Setup Wizard cleanly on first access.
func (OmniRouteAdapter) EnsurePermanentSecrets(ctx context.Context, r *store.Router, box *secrets.Box) ([]store.EnvVariable, bool) {
	return r.EnvVars, false
}

func (OmniRouteAdapter) Env(r *store.Router, decrypted map[string]string) []string {
	port := strconv.Itoa(OmniRouteAdapter{}.InternalPort(r))
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}

	envMap := make(map[string]string)

	// 1. Mandatory Baseline & High-Performance Single-Process Defaults
	envMap["NODE_ENV"] = "production"
	envMap["PORT"] = port
	envMap["DATA_DIR"] = dataPath
	// Allocate up to 16 GB heap from the 24 GB host RAM.
	// OMNIROUTE_MEMORY_MB is required because dev/run-standalone.mjs appends
	// --max-old-space-size=<OMNIROUTE_MEMORY_MB> last, overriding NODE_OPTIONS.
	envMap["OMNIROUTE_MEMORY_MB"] = "16384"
	envMap["NODE_OPTIONS"] = "--max-old-space-size=16384"
	envMap["UV_THREADPOOL_SIZE"] = "64"
	// Disable OmniRoute's internal synchronous full-database file copies on startup/pricing sync.
	envMap["DISABLE_SQLITE_AUTO_BACKUP"] = "true"
	// Raise model concurrency cap from 3 to 32 for high throughput across all 12 CPU cores.
	envMap["COMBO_CONCURRENCY_PER_MODEL"] = "32"
	// Disable background periodic sweeps on 359 provider keys to prevent CPU & DB lock storms.
	envMap["OMNIROUTE_DISABLE_CREDENTIAL_HEALTH_CHECK"] = "true"
	// Do NOT set WEB_CONCURRENCY: OmniRoute uses SQLite-on-disk which cannot handle
	// multi-process cluster concurrency without lock contention and index corruption.
	// Single-process with UV_THREADPOOL_SIZE=64 scales async I/O cleanly across all 12 cores.
	// Default Cloudflare Quick Tunnel to reliable TCP http2 protocol instead of QUIC (which drops on UDP-restricted VMs)
	envMap["CLOUDFLARED_PROTOCOL"] = "http2"
	envMap["TUNNEL_TRANSPORT_PROTOCOL"] = "http2"
	// NOTE: Do NOT inject BASE_PATH/PREFIX/PUBLIC_URL/BASE_URL here.
	// OmniRoute is a pre-built Next.js standalone server that ignores runtime
	// base path changes (basePath is compiled into next.config.js at build time).
	// Injecting these causes startup crashes. The reverse proxy layer handles
	// all subpath rewriting transparently.

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

	// 4. Inject decrypted provider credentials as OMNIROUTE_<PROVIDER>_KEY
	// and fallback standard provider tokens (e.g. OPENAI_API_KEY) if not already set.
	for provider, key := range decrypted {
		upperProv := strings.ToUpper(strings.TrimSpace(provider))
		envMap["OMNIROUTE_"+upperProv+"_KEY"] = key

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

// ResourceLimits returns sensible defaults for an OmniRoute container.
// By default, NanoCPUs: 0 and MemoryBytes: 0 allow unconstrained bursting across all 12 CPU cores
// and 24 GB RAM, eliminating Linux CFS CPU quota period throttling (100ms jitter freezes).
// Overridable via router config["resource_limits"].
func (OmniRouteAdapter) ResourceLimits(r *store.Router) ContainerResources {
	res := ContainerResources{
		MemoryBytes: 0,    // 0 = unlimited (allows container full access to 24 GB host RAM)
		NanoCPUs:    0,    // 0 = unlimited (avoids Linux CFS CPU quota period throttling)
		PidsLimit:   8192,
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

// openAIModelsResponse is the OpenAI-compatible /v1/models payload OmniRoute serves.
type openAIModelsResponse struct {
	Object string `json:"object"`
	Data   []struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	} `json:"data"`
}

func (OmniRouteAdapter) ParseModels(r *store.Router, body []byte) ([]store.Model, error) {
	var resp openAIModelsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	out := make([]store.Model, 0, len(resp.Data))
	for _, m := range resp.Data {
		provider := m.OwnedBy
		if provider == "" {
			provider = "unknown"
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

// ----- config helpers -----

func strConfig(r *store.Router, key string) string {
	if v, ok := r.Config[key]; ok {
		return toStr(v)
	}
	return ""
}

func intConfig(r *store.Router, key string) int {
	if v, ok := r.Config[key]; ok {
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		}
	}
	return 0
}

func toStr(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case float64:
		return strconv.Itoa(int(s))
	default:
		if v == nil {
			return ""
		}
		b, _ := json.Marshal(v)
		return string(b)
	}
}
