package adapters

import (
	"encoding/json"
	"strconv"
	"strings"

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
// OmniRoute doesn't have a dedicated /health, so /v1/models doubles as the
// readiness signal — it's the same endpoint as ModelsPath intentionally.
func (OmniRouteAdapter) HealthPath(r *store.Router) string {
	if p := strConfig(r, "health_path"); p != "" {
		return p
	}
	return "/v1/models"
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

func (OmniRouteAdapter) Mounts(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}
	volume := "clever-route-" + r.Slug
	return []string{volume + ":" + dataPath}
}

func (OmniRouteAdapter) Env(r *store.Router, decrypted map[string]string) []string {
	port := strconv.Itoa(OmniRouteAdapter{}.InternalPort(r))
	env := []string{
		"NODE_ENV=production",
		"PORT=" + port,
	}
	// Pass through any env declared in router config["env"] (map).
	if cfgEnv, ok := r.Config["env"].(map[string]any); ok {
		for k, v := range cfgEnv {
			env = append(env, k+"="+toStr(v))
		}
	}
	// Inject decrypted credentials as OMNIROUTE_<PROVIDER>_KEY (generic).
	for provider, key := range decrypted {
		env = append(env, "OMNIROUTE_"+strings.ToUpper(provider)+"_KEY="+key)
	}
	return env
}

// ResourceLimits returns sensible defaults for an OmniRoute container.
// Overridable via router config["resource_limits"].
func (OmniRouteAdapter) ResourceLimits(r *store.Router) ContainerResources {
	res := ContainerResources{
		MemoryBytes: 512 * 1024 * 1024, // 512 MB
		NanoCPUs:    1_000_000_000,      // 1 CPU
		PidsLimit:   256,
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
