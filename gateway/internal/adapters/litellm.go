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

// LiteLLMAdapter manages a ghcr.io/berriai/litellm container.
//
// LiteLLM is a Python/FastAPI proxy for 100+ LLMs serving an OpenAI-compatible
// API and admin dashboard on port 4000. It supports multi-worker execution across
// host CPU cores, config-on-disk in /data/config.yaml, and SQLite DB at /data/litellm.db.
type LiteLLMAdapter struct{}

func (LiteLLMAdapter) Type() string { return "litellm" }

func (LiteLLMAdapter) InternalPort(r *store.Router) int {
	if p := intConfig(r, "internal_port"); p > 0 {
		return p
	}
	return 4000
}

func (LiteLLMAdapter) HealthPath(r *store.Router) string {
	if p := strConfig(r, "health_path"); p != "" {
		return p
	}
	return "/health/liveliness"
}

func (LiteLLMAdapter) ModelsPath(r *store.Router) string {
	if p := strConfig(r, "models_path"); p != "" {
		return p
	}
	return "/v1/models"
}

func (LiteLLMAdapter) NativePanelPath(r *store.Router) string {
	if p := strConfig(r, "native_panel_path"); p != "" {
		return p
	}
	return "/ui"
}

func (LiteLLMAdapter) DeclaredVolumes(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/data"
	}
	return []string{dataPath}
}

func (LiteLLMAdapter) Mounts(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/data"
	}
	volume := "clever-route-" + r.Slug
	return []string{volume + ":" + dataPath}
}

// EnsurePermanentSecrets ensures LITELLM_MASTER_KEY and LITELLM_SALT_KEY exist.
func (LiteLLMAdapter) EnsurePermanentSecrets(ctx context.Context, r *store.Router, box *secrets.Box) ([]store.EnvVariable, bool) {
	if box == nil {
		return r.EnvVars, false
	}

	hasMasterKey := false
	hasSaltKey := false
	for _, ev := range r.EnvVars {
		if ev.Key == "LITELLM_MASTER_KEY" && strings.TrimSpace(ev.Value) != "" {
			hasMasterKey = true
		}
		if ev.Key == "LITELLM_SALT_KEY" && strings.TrimSpace(ev.Value) != "" {
			hasSaltKey = true
		}
	}

	if hasMasterKey && hasSaltKey {
		return r.EnvVars, false
	}

	updated := make([]store.EnvVariable, len(r.EnvVars))
	copy(updated, r.EnvVars)

	if !hasMasterKey {
		// Generate secure random master key prefixed with "sk-"
		raw := make([]byte, 24)
		_, _ = rand.Read(raw)
		masterKey := "sk-litellm-" + hex.EncodeToString(raw)

		encVal, err := secrets.EncryptValue(box, masterKey)
		if err == nil {
			updated = append(updated, store.EnvVariable{
				Key:      "LITELLM_MASTER_KEY",
				Value:    encVal,
				IsSecret: true,
			})
		}
	}

	if !hasSaltKey {
		raw := make([]byte, 16)
		_, _ = rand.Read(raw)
		saltKey := hex.EncodeToString(raw)

		encVal, err := secrets.EncryptValue(box, saltKey)
		if err == nil {
			updated = append(updated, store.EnvVariable{
				Key:      "LITELLM_SALT_KEY",
				Value:    encVal,
				IsSecret: true,
			})
		}
	}

	return updated, true
}

func (LiteLLMAdapter) Env(r *store.Router, decrypted map[string]string) []string {
	port := strconv.Itoa(LiteLLMAdapter{}.InternalPort(r))
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/data"
	}

	envMap := make(map[string]string)

	// 1. Mandatory Baseline Defaults
	envMap["PORT"] = port
	envMap["LITELLM_PORT"] = port
	envMap["DATA_DIR"] = dataPath
	// Default to 4 multi-process workers for optimal 12-core CPU scaling
	envMap["NUM_WORKERS"] = "4"
	envMap["LITELLM_WORKER_COUNT"] = "4"

	// 2. Legacy router config["env"] if present
	if cfgEnv, ok := r.Config["env"].(map[string]any); ok {
		for k, v := range cfgEnv {
			envMap[k] = toStr(v)
		}
	}

	// 3. User-Defined Environment Variables (from DB, already decrypted in memory)
	for _, item := range r.EnvVars {
		k := strings.TrimSpace(item.Key)
		if k != "" {
			envMap[k] = item.Value
		}
	}

	// 4. Inject decrypted provider credentials (e.g. OPENAI_API_KEY, ANTHROPIC_API_KEY)
	for provider, key := range decrypted {
		upperProv := strings.ToUpper(strings.TrimSpace(provider))
		stdKey := upperProv + "_API_KEY"
		if _, exists := envMap[stdKey]; !exists {
			envMap[stdKey] = key
		}
		envMap[upperProv] = key
	}

	// 5. Convert to []string formatted as "KEY=VALUE"
	finalEnv := make([]string, 0, len(envMap))
	for k, v := range envMap {
		finalEnv = append(finalEnv, fmt.Sprintf("%s=%s", k, v))
	}
	return finalEnv
}

// ResourceLimits returns resource constraints for LiteLLM.
// Configured to burst across available CPU cores while capping memory.
func (LiteLLMAdapter) ResourceLimits(r *store.Router) ContainerResources {
	res := ContainerResources{
		MemoryBytes: 4 * 1024 * 1024 * 1024, // 4 GB RAM hard ceiling
		NanoCPUs:    8_000_000_000,           // 8 CPU cores burst limit
		PidsLimit:   2048,                    // High limit for multi-worker Python processes
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

func (LiteLLMAdapter) ParseModels(r *store.Router, body []byte) ([]store.Model, error) {
	var resp openAIModelsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		// Also support custom array or LiteLLM info list format if standard unmarshal fails
		var list []struct {
			ID        string `json:"id"`
			ModelName string `json:"model_name"`
		}
		if err2 := json.Unmarshal(body, &list); err2 == nil && len(list) > 0 {
			out := make([]store.Model, 0, len(list))
			for _, m := range list {
				modelID := m.ID
				if modelID == "" {
					modelID = m.ModelName
				}
				if modelID != "" {
					out = append(out, store.Model{
						RouterID:   r.ID,
						ModelID:    modelID,
						Provider:   inferLiteLLMProvider(modelID),
						Modalities: "text->text",
					})
				}
			}
			return out, nil
		}
		return nil, fmt.Errorf("decode /v1/models response: %w", err)
	}

	out := make([]store.Model, 0, len(resp.Data))
	for _, m := range resp.Data {
		provider := m.OwnedBy
		if provider == "" || provider == "system" || provider == "openai" {
			provider = inferLiteLLMProvider(m.ID)
		}
		out = append(out, store.Model{
			RouterID:   r.ID,
			ModelID:    m.ID,
			Provider:   provider,
			Modalities: "text->text",
		})
	}
	return out, nil
}

func inferLiteLLMProvider(modelID string) string {
	low := strings.ToLower(modelID)
	switch {
	case strings.Contains(modelID, "/"):
		parts := strings.SplitN(modelID, "/", 2)
		return parts[0]
	case strings.HasPrefix(low, "gpt-") || strings.HasPrefix(low, "o1") || strings.HasPrefix(low, "o3") || strings.HasPrefix(low, "text-embedding") || strings.HasPrefix(low, "dall-e"):
		return "openai"
	case strings.HasPrefix(low, "claude"):
		return "anthropic"
	case strings.HasPrefix(low, "gemini") || strings.HasPrefix(low, "palm"):
		return "gemini"
	case strings.HasPrefix(low, "mistral") || strings.HasPrefix(low, "codestral"):
		return "mistral"
	case strings.HasPrefix(low, "deepseek"):
		return "deepseek"
	case strings.HasPrefix(low, "llama") || strings.HasPrefix(low, "groq"):
		return "groq"
	case strings.HasPrefix(low, "command"):
		return "cohere"
	default:
		return "litellm"
	}
}
