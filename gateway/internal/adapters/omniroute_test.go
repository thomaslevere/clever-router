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
		EnvVars:     nil, // Uninitialized router
	}

	// 1. Initial uninitialized router: should NOT inject predefined env vars (clean setup wizard)
	ctx := context.Background()
	savedEnv, modified := ad.EnsurePermanentSecrets(ctx, r, box)
	if modified {
		t.Fatalf("expected modified=false to allow clean initial wizard")
	}
	if len(savedEnv) != 0 {
		t.Fatalf("expected 0 generated env vars for clean setup wizard, got %d", len(savedEnv))
	}

	// 3. Router with explicit empty slice (opted out of presets): should NOT inject presets
	rEmpty := &store.Router{
		ID:          "router-empty",
		Slug:        "empty-router",
		AdapterType: "omniroute",
		EnvVars:     []store.EnvVariable{},
	}
	savedEmpty, modEmpty := ad.EnsurePermanentSecrets(ctx, rEmpty, box)
	if modEmpty {
		t.Errorf("expected modified=false when EnvVars is non-nil empty slice")
	}
	if len(savedEmpty) != 0 {
		t.Errorf("expected 0 env vars for empty slice, got %d", len(savedEmpty))
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
