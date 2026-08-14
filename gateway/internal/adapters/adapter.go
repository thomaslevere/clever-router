package adapters

import (
	"context"
	"fmt"

	"github.com/clever-route/gateway/internal/secrets"
	"github.com/clever-route/gateway/internal/store"
)

// Adapter describes a router type's deployment contract. The control plane does
// not need to understand a router's internals — only this contract.
//
// The Adapter is pluggable per router type; the Runtime (Docker socket today,
// Clever Cloud API tomorrow) is pluggable per deployment topology. Swapping the
// runtime is a contained change, not a rewrite.
type Adapter interface {
	// Type returns the adapter identifier, e.g. "omniroute".
	Type() string

	// InternalPort is the port the router process listens on inside its container.
	InternalPort(r *store.Router) int

	// HealthPath is an HTTP path that returns 2xx when the router is ready.
	// This is used for readiness probes — it MUST NOT require any specific
	// response body, only a 2xx status code.
	HealthPath(r *store.Router) string

	// ModelsPath is the HTTP path that returns an OpenAI-compatible /v1/models
	// JSON payload. May differ from HealthPath for adapters that expose a
	// dedicated health endpoint separate from their models listing.
	ModelsPath(r *store.Router) string

	// NativePanelPath is the router's own dashboard path, e.g. "/dashboard".
	NativePanelPath(r *store.Router) string

	// Mounts returns Docker bind/volume specs for router-owned persistent state.
	// The container filesystem is disposable; these mounts are not.
	Mounts(r *store.Router) []string

	// DeclaredVolumes returns target directory paths inside the container that
	// must be persisted across restarts (e.g. ["/app/data"]).
	DeclaredVolumes(r *store.Router) []string

	// EnsurePermanentSecrets verifies that all required secrets/crypto keys for this
	// adapter type exist. If missing, it generates them once, encrypts secrets using
	// box, and returns the full env list and modified=true.
	EnsurePermanentSecrets(ctx context.Context, r *store.Router, box *secrets.Box) ([]store.EnvVariable, bool)

	// Env returns env vars to inject into the container from decrypted credentials
	// and router config.
	Env(r *store.Router, decrypted map[string]string) []string

	// ParseModels parses the router's /v1/models response into model records.
	ParseModels(r *store.Router, body []byte) ([]store.Model, error)

	// ResourceLimits returns the default resource constraints for this adapter
	// type. Values are overridable via router config["resource_limits"].
	ResourceLimits(r *store.Router) ContainerResources
}

// ContainerResources defines soft resource boundaries for a sibling container.
// Zero values mean "use Docker defaults" (i.e. no limit).
type ContainerResources struct {
	// MemoryBytes is the hard memory limit. Recommended: 512MB per router.
	MemoryBytes int64
	// NanoCPUs is the CPU quota (1 CPU = 1_000_000_000). Recommended: 1 CPU.
	NanoCPUs int64
	// PidsLimit caps the number of processes inside the container.
	PidsLimit int64
}

// Registry maps adapter type -> Adapter.
type Registry struct {
	m map[string]Adapter
}

func NewRegistry(adapters ...Adapter) *Registry {
	reg := &Registry{m: make(map[string]Adapter, len(adapters))}
	for _, a := range adapters {
		reg.Register(a)
	}
	return reg
}

func (r *Registry) Register(a Adapter) { r.m[a.Type()] = a }

func (r *Registry) Get(typ string) (Adapter, error) {
	a, ok := r.m[typ]
	if !ok {
		return nil, fmt.Errorf("unknown adapter type %q", typ)
	}
	return a, nil
}
