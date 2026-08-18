package adapters

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/clever-route/gateway/internal/secrets"
	"github.com/clever-route/gateway/internal/store"
)

// FreeLLMAPIAdapter manages a FreeLLMAPI (e.g. tashfeenahmed/freellmapi:latest) container.
//
// FreeLLMAPI is an open-source, self-hosted AI API gateway that aggregates free-tier AI endpoints
// (HuggingFace, Groq, Cohere, Cloudflare, etc.) into a unified OpenAI-compatible /v1 surface.
// Its internal service listens on port 3001 by default and persists session state, encryption keys,
// and provider configurations in /app/data.
type FreeLLMAPIAdapter struct{}

func (FreeLLMAPIAdapter) Type() string { return "freellmapi" }

func (FreeLLMAPIAdapter) InternalPort(r *store.Router) int {
	if p := intConfig(r, "internal_port"); p > 0 {
		return p
	}
	return 3001
}

// HealthPath returns a lightweight path used for readiness probes.
func (FreeLLMAPIAdapter) HealthPath(r *store.Router) string {
	if p := strConfig(r, "health_path"); p != "" {
		return p
	}
	return "/v1/models"
}

// ModelsPath is the OpenAI-compatible model listing endpoint.
func (FreeLLMAPIAdapter) ModelsPath(r *store.Router) string {
	if p := strConfig(r, "models_path"); p != "" {
		return p
	}
	return "/v1/models"
}

func (FreeLLMAPIAdapter) NativePanelPath(r *store.Router) string {
	if p := strConfig(r, "native_panel_path"); p != "" {
		return p
	}
	return "/"
}

func (FreeLLMAPIAdapter) DeclaredVolumes(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}
	return []string{dataPath}
}

func (FreeLLMAPIAdapter) Mounts(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}
	volume := "clever-route-" + r.Slug
	return []string{volume + ":" + dataPath}
}

// EnsurePermanentSecrets verifies that an ENCRYPTION_KEY exists for FreeLLMAPI.
// If not present, it generates a 32-byte hex key, encrypts it, and returns updated env vars.
func (FreeLLMAPIAdapter) EnsurePermanentSecrets(ctx context.Context, r *store.Router, box *secrets.Box) ([]store.EnvVariable, bool) {
	hasKey := false
	for _, item := range r.EnvVars {
		if item.Key == "ENCRYPTION_KEY" && strings.TrimSpace(item.Value) != "" {
			hasKey = true
			break
		}
	}
	if hasKey {
		return r.EnvVars, false
	}

	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	rawHex := hex.EncodeToString(raw)

	enc, err := secrets.EncryptValue(box, rawHex)
	if err != nil {
		return r.EnvVars, false
	}

	updated := make([]store.EnvVariable, len(r.EnvVars), len(r.EnvVars)+1)
	copy(updated, r.EnvVars)
	updated = append(updated, store.EnvVariable{
		Key:      "ENCRYPTION_KEY",
		Value:    enc,
		IsSecret: true,
	})
	return updated, true
}

func (FreeLLMAPIAdapter) Env(r *store.Router, decrypted map[string]string) []string {
	port := strconv.Itoa(FreeLLMAPIAdapter{}.InternalPort(r))
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}

	envMap := make(map[string]string)

	// 1. Baseline Defaults for FreeLLMAPI
	envMap["NODE_ENV"] = "production"
	envMap["PORT"] = port
	envMap["DATA_DIR"] = dataPath
	envMap["HOME"] = dataPath
	envMap["HOST_BIND"] = "0.0.0.0"
	envMap["HOSTNAME"] = "0.0.0.0"

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

	// 4. Inject decrypted provider credentials
	for provider, key := range decrypted {
		upperProv := strings.ToUpper(strings.TrimSpace(provider))
		envMap["FREELLMAPI_"+upperProv+"_KEY"] = key

		stdKey := upperProv + "_API_KEY"
		if _, exists := envMap[stdKey]; !exists {
			envMap[stdKey] = key
		}
	}

	// 5. Convert map to KEY=VALUE slice
	out := make([]string, 0, len(envMap))
	for k, v := range envMap {
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return out
}

func (FreeLLMAPIAdapter) ResourceLimits(r *store.Router) ContainerResources {
	res := ContainerResources{
		MemoryBytes: 8 * 1024 * 1024 * 1024, // 8 GiB RAM (host has 24GB shared)
		NanoCPUs:    12_000_000_000,         // 12 vCPUs quota (host has 12 cores shared)
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

func (FreeLLMAPIAdapter) ParseModels(r *store.Router, body []byte) ([]store.Model, error) {
	var resp openAIModelsResponse
	if err := json.Unmarshal(body, &resp); err == nil && len(resp.Data) > 0 {
		out := make([]store.Model, 0, len(resp.Data))
		for _, m := range resp.Data {
			provider := m.OwnedBy
			if provider == "" {
				if parts := strings.SplitN(m.ID, "/", 2); len(parts) == 2 {
					provider = parts[0]
				} else {
					provider = "freellmapi"
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
	}
	if err := json.Unmarshal(body, &list); err == nil && len(list) > 0 {
		out := make([]store.Model, 0, len(list))
		for _, m := range list {
			provider := m.Provider
			if provider == "" {
				provider = "freellmapi"
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
