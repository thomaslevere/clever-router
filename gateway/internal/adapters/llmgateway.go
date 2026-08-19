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

// LLMGatewayAdapter manages an LLM Gateway (theopenco/llmgateway) container.
//
// LLM Gateway is an enterprise-grade multi-provider AI gateway with unified OpenAI/Anthropic
// format translation, automatic failover, cost analytics, model load balancing, and guardrails.
// It listens on port 3002 by default (and port 4001 for gateway API), serves OpenAI-compatible /v1
// endpoints, provides a modern dashboard at / or /dashboard, and persists state in /var/lib/postgresql/data,
// /var/lib/redis, and /app/data.
type LLMGatewayAdapter struct{}

func (LLMGatewayAdapter) Type() string { return "llmgateway" }

func (LLMGatewayAdapter) InternalPort(r *store.Router) int {
	if p := intConfig(r, "internal_port"); p > 0 {
		return p
	}
	return 3002
}

// HealthPath probes LLM Gateway's /api/health or /v1/models endpoint which verifies the engine is active.
func (LLMGatewayAdapter) HealthPath(r *store.Router) string {
	if p := strConfig(r, "health_path"); p != "" {
		return p
	}
	return "/api/health"
}

// ModelsPath is the OpenAI-compatible model listing endpoint.
func (LLMGatewayAdapter) ModelsPath(r *store.Router) string {
	if p := strConfig(r, "models_path"); p != "" {
		return p
	}
	return "/v1/models"
}

func (LLMGatewayAdapter) NativePanelPath(r *store.Router) string {
	if p := strConfig(r, "native_panel_path"); p != "" {
		return p
	}
	return "/dashboard"
}

func (LLMGatewayAdapter) DeclaredVolumes(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}
	return []string{"/var/lib/postgresql/data", "/var/lib/redis", dataPath}
}

func (LLMGatewayAdapter) Mounts(r *store.Router) []string {
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}
	volumePg := "clever-route-" + r.Slug + "-pg"
	volumeRedis := "clever-route-" + r.Slug + "-redis"
	volumeData := "clever-route-" + r.Slug + "-data"
	return []string{
		volumePg + ":/var/lib/postgresql/data",
		volumeRedis + ":/var/lib/redis",
		volumeData + ":" + dataPath,
	}
}

// EnsurePermanentSecrets verifies that stable secrets exist for LLM Gateway (min 32 bytes).
func (LLMGatewayAdapter) EnsurePermanentSecrets(ctx context.Context, r *store.Router, box *secrets.Box) ([]store.EnvVariable, bool) {
	hasSecret := false
	for _, ev := range r.EnvVars {
		if (ev.Key == "AUTH_SECRET" || ev.Key == "GATEWAY_API_KEY_HASH_SECRET" || ev.Key == "JWT_SECRET") && len(strings.TrimSpace(ev.Value)) >= 32 {
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
		log.Printf("[llmgateway] warning: crypto/rand error: %v", err)
	}
	if _, err := rand.Read(bytes2); err != nil {
		log.Printf("[llmgateway] warning: crypto/rand error: %v", err)
	}
	authSecret := hex.EncodeToString(bytes1)
	hashSecret := hex.EncodeToString(bytes2)

	cipherAuth, err := secrets.EncryptValue(box, authSecret)
	if err != nil {
		log.Printf("[llmgateway] error encrypting AUTH_SECRET: %v", err)
		return r.EnvVars, false
	}

	cipherHash, err := secrets.EncryptValue(box, hashSecret)
	if err != nil {
		log.Printf("[llmgateway] error encrypting GATEWAY_API_KEY_HASH_SECRET: %v", err)
		return r.EnvVars, false
	}

	newEnv := append(r.EnvVars,
		store.EnvVariable{
			Key:      "AUTH_SECRET",
			Value:    cipherAuth,
			IsSecret: true,
		},
		store.EnvVariable{
			Key:      "GATEWAY_API_KEY_HASH_SECRET",
			Value:    cipherHash,
			IsSecret: true,
		},
		store.EnvVariable{
			Key:      "JWT_SECRET",
			Value:    cipherAuth,
			IsSecret: true,
		},
	)

	return newEnv, true
}

func (LLMGatewayAdapter) Env(r *store.Router, decrypted map[string]string) []string {
	port := strconv.Itoa(LLMGatewayAdapter{}.InternalPort(r))
	dataPath := strConfig(r, "data_path")
	if dataPath == "" {
		dataPath = "/app/data"
	}

	envMap := make(map[string]string)

	// 1. High-Performance Multi-Core Node.js / Go Runtime Defaults (Host has 12 vCPUs / 24 GB RAM shared)
	envMap["PORT"] = port
	envMap["GATEWAY_PORT"] = port
	envMap["HOST"] = "0.0.0.0"
	envMap["DATA_DIR"] = dataPath
	envMap["CONFIG_DIR"] = "/app/config"
	envMap["NODE_ENV"] = "production"
	envMap["NODE_OPTIONS"] = "--max-old-space-size=3072"
	envMap["WEB_CONCURRENCY"] = "4"
	envMap["UV_THREADPOOL_SIZE"] = "12"
	envMap["GOMAXPROCS"] = "12"
	envMap["GOMEMLIMIT"] = "3GiB"
	envMap["LOG_LEVEL"] = "info"
	envMap["METRICS_ENABLED"] = "true"
	envMap["RATE_LIMIT_ENABLED"] = "false"

	// Ensure strong 64-char AUTH_SECRET default
	bytes := make([]byte, 32)
	_, _ = rand.Read(bytes)
	defaultSecret := hex.EncodeToString(bytes)
	envMap["AUTH_SECRET"] = defaultSecret
	envMap["GATEWAY_API_KEY_HASH_SECRET"] = defaultSecret
	envMap["JWT_SECRET"] = defaultSecret

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
func (LLMGatewayAdapter) ResourceLimits(r *store.Router) ContainerResources {
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

func (LLMGatewayAdapter) ParseModels(r *store.Router, body []byte) ([]store.Model, error) {
	var resp openAIModelsResponse
	if err := json.Unmarshal(body, &resp); err == nil && len(resp.Data) > 0 {
		out := make([]store.Model, 0, len(resp.Data))
		for _, m := range resp.Data {
			provider := m.OwnedBy
			if provider == "" {
				if parts := strings.SplitN(m.ID, "/", 2); len(parts) == 2 {
					provider = parts[0]
				} else {
					provider = "llmgateway"
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
				provider = "llmgateway"
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
