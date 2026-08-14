package adapters

import (
	"context"
	"strings"
	"testing"

	"github.com/clever-route/gateway/internal/secrets"
	"github.com/clever-route/gateway/internal/store"
)

func TestOmniRouteDeterministicSecrets(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	box, err := secrets.New(key)
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}

	ad := OmniRouteAdapter{}
	r := &store.Router{
		ID:          "router-123",
		Slug:        "test-router",
		AdapterType: "omniroute",
		EnvVars:     []store.EnvVariable{},
	}

	// 1. Initial creation: should generate permanent secrets
	ctx := context.Background()
	savedEnv, modified := ad.EnsurePermanentSecrets(ctx, r, box)
	if !modified {
		t.Fatalf("expected modified=true on empty initial env")
	}
	if len(savedEnv) == 0 {
		t.Fatalf("expected generated env vars, got 0")
	}

	r.EnvVars = savedEnv

	// Check required secrets exist
	envMap := make(map[string]store.EnvVariable)
	for _, e := range savedEnv {
		envMap[e.Key] = e
	}

	for _, reqKey := range []string{"JWT_SECRET", "API_KEY_SECRET", "STORAGE_ENCRYPTION_KEY", "NODE_ENV", "DATA_DIR"} {
		if _, exists := envMap[reqKey]; !exists {
			t.Errorf("missing required secret: %s", reqKey)
		}
	}

	// 2. Second call with existing secrets: should NOT modify or regenerate
	savedEnv2, modified2 := ad.EnsurePermanentSecrets(ctx, r, box)
	if modified2 {
		t.Errorf("expected modified=false on existing secrets, got true")
	}
	if len(savedEnv2) != len(savedEnv) {
		t.Errorf("env count changed: %d vs %d", len(savedEnv2), len(savedEnv))
	}

	// 3. Env output consistency across multiple calls
	plainEnv1 := ad.Env(r, map[string]string{"openai": "sk-1234"})
	plainEnv2 := ad.Env(r, map[string]string{"openai": "sk-1234"})

	if len(plainEnv1) != len(plainEnv2) {
		t.Fatalf("Env output length differs: %d vs %d", len(plainEnv1), len(plainEnv2))
	}

	map1 := make(map[string]string)
	for _, s := range plainEnv1 {
		parts := strings.SplitN(s, "=", 2)
		if len(parts) == 2 {
			map1[parts[0]] = parts[1]
		}
	}

	for _, s := range plainEnv2 {
		parts := strings.SplitN(s, "=", 2)
		if len(parts) == 2 {
			if map1[parts[0]] != parts[1] {
				t.Errorf("env key %s value mutated between calls: %q vs %q", parts[0], map1[parts[0]], parts[1])
			}
		}
	}
}

func TestOmniRouteDeclaredVolumes(t *testing.T) {
	ad := OmniRouteAdapter{}
	r := &store.Router{Slug: "my-router"}
	vols := ad.DeclaredVolumes(r)
	if len(vols) == 0 || vols[0] != "/app/data" {
		t.Errorf("expected /app/data, got %v", vols)
	}
}
