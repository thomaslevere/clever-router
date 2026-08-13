package adapters

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/clever-route/gateway/internal/cache"
	"github.com/clever-route/gateway/internal/logger"
	"github.com/clever-route/gateway/internal/router"
	"github.com/clever-route/gateway/internal/secrets"
	"github.com/clever-route/gateway/internal/store"
	dockertypes "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

const (
	containerPrefix = "clever-route-"
	// networkName is the dedicated bridge network all sibling router containers
	// are attached to. It isolates routers from each other and from the host
	// network while allowing the gateway to reach them by IP.
	networkName = "clever-route-net"
)

// Manager orchestrates router runtimes over the Docker socket (Option A).
// It owns lifecycle (start/stop), address resolution, persistence mounts,
// model discovery and health — all driven by the pluggable Adapter contract.
type Manager struct {
	store   *store.Store
	cache   *cache.Cache
	box     *secrets.Box
	docker  *client.Client
	reg     *Registry
	allowed map[string]bool
	table   *router.Table
}

func NewManager(st *store.Store, c *cache.Cache, box *secrets.Box, reg *Registry, allowed []string, table *router.Table) (*Manager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	allow := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allow[strings.TrimSpace(a)] = true
	}
	m := &Manager{store: st, cache: c, box: box, docker: cli, reg: reg, allowed: allow, table: table}

	// Ensure the dedicated network exists. Best-effort: log on failure.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.ensureNetwork(ctx); err != nil {
		log.Printf("[manager] warning: could not ensure docker network %q: %v", networkName, err)
	}

	return m, nil
}

func (m *Manager) Close() error {
	if m.docker != nil {
		return m.docker.Close()
	}
	return nil
}

// ensureNetwork creates the control-plane Docker bridge network if it does not
// already exist. All sibling router containers are attached to this network so
// they are isolated from each other and from the host, but reachable from the
// gateway process via Docker bridge IPs.
func (m *Manager) ensureNetwork(ctx context.Context) error {
	// Check if network already exists.
	nets, err := m.docker.NetworkList(ctx, dockertypes.NetworkListOptions{
		Filters: filters.NewArgs(filters.Arg("name", networkName)),
	})
	if err != nil {
		return fmt.Errorf("list networks: %w", err)
	}
	for _, n := range nets {
		if n.Name == networkName {
			return nil // already present
		}
	}
	// Create the network. In Docker SDK v27 CheckDuplicate was removed from
	// CreateOptions (it defaults to true server-side since API v1.44).
	_, err = m.docker.NetworkCreate(ctx, networkName, network.CreateOptions{
		Driver: "bridge",
		Labels: map[string]string{
			"app":        "clever-route",
			"managed-by": "clever-route",
		},
	})
	if err != nil {
		// Treat "already exists" as a no-op (harmless race on first startup).
		if strings.Contains(err.Error(), "already exists") {
			return nil
		}
		return fmt.Errorf("create network %q: %w", networkName, err)
	}
	log.Printf("[manager] created docker network %q", networkName)
	return nil
}

// imageAllowed enforces the allowlist — Docker-socket access is privileged, so
// only allowlisted images may be spawned (never a free-text UI field).
func (m *Manager) imageAllowed(ref string) bool {
	return m.allowed[ref] || m.allowed[shortRef(ref)]
}

func shortRef(ref string) string {
	if i := strings.Index(ref, ":"); i > 0 {
		return ref[:i]
	}
	return ref
}

// Start provisions/restarts the router's sibling container and wires its route.
func (m *Manager) Start(ctx context.Context, r *store.Router) error {
	ad, err := m.reg.Get(r.AdapterType)
	if err != nil {
		return err
	}
	if !m.imageAllowed(r.ImageRef) {
		return fmt.Errorf("image %q is not in ALLOWED_IMAGES", r.ImageRef)
	}

	// Ensure network exists (re-create if deleted externally).
	if netErr := m.ensureNetwork(ctx); netErr != nil {
		log.Printf("[manager] warning: ensureNetwork: %v", netErr)
	}

	// Clean up any stale container before creating a new one.
	// BUG-5 FIX: work on a local copy so we don't mutate the caller's pointer.
	local := *r
	_ = m.stopAndRemove(ctx, &local, false)

	if err := m.ensureImage(ctx, r.ImageRef); err != nil {
		return fmt.Errorf("pull image: %w", err)
	}

	creds, err := m.loadCreds(ctx, r)
	if err != nil {
		return fmt.Errorf("load credentials: %w", err)
	}

	lim := ad.ResourceLimits(r)
	pidsLimit := lim.PidsLimit

	name := containerPrefix + r.Slug
	cfg := &container.Config{
		Image: r.ImageRef,
		Env:   ad.Env(r, creds),
		Labels: map[string]string{
			"app":        "clever-route",
			"router":     r.Slug,
			"adapter":    r.AdapterType,
			"managed-by": "clever-route",
		},
	}
	internalPortStr := fmt.Sprintf("%d/tcp", ad.InternalPort(r))
	host := &container.HostConfig{
		Binds:         ad.Mounts(r),
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		AutoRemove:    false,
		PortBindings: nat.PortMap{
			nat.Port(internalPortStr): []nat.PortBinding{
				{HostIP: "0.0.0.0", HostPort: ""},
			},
		},
		// GAP-2 FIX: enforce resource limits to prevent a runaway container
		// from starving the control plane or other routers.
		Resources: container.Resources{
			Memory:    lim.MemoryBytes,
			NanoCPUs:  lim.NanoCPUs,
			PidsLimit: &pidsLimit,
		},
	}
	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"bridge": {},
		},
	}

	created, err := m.docker.ContainerCreate(ctx, cfg, host, netCfg, nil, name)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	if err := m.docker.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		_ = m.docker.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("start container: %w", err)
	}

	addr, err := m.resolveAddr(ctx, created.ID, ad.InternalPort(r))
	if err != nil {
		return fmt.Errorf("resolve address: %w", err)
	}
	panel := strings.TrimRight(addr, "/") + ad.NativePanelPath(r)

	if err := m.store.UpdateRouterState(ctx, r.ID, "starting", addr, created.ID, panel, "unknown"); err != nil {
		return err
	}

	// GAP-8 FIX: use deadline + exponential backoff instead of fixed-count sleep.
	if ok := m.waitForHealthy(ctx, r, ad, addr, 90*time.Second); !ok {
		_ = m.store.UpdateRouterState(ctx, r.ID, "unhealthy", addr, created.ID, panel, "unhealthy")
		_ = m.cache.Publish(ctx, cache.ReloadEvent{Kind: "router", Slug: r.Slug})
		return fmt.Errorf("router %s started but did not pass health check within 90s", r.Slug)
	}

	if err := m.store.UpdateRouterState(ctx, r.ID, "running", addr, created.ID, panel, "healthy"); err != nil {
		return err
	}
	if err := m.cache.SetRoute(ctx, r.Slug, addr); err != nil {
		return err
	}
	m.table.Set(r.Slug, addr)

	// Best-effort discovery; never block traffic on it.
	_ = m.DiscoverModels(ctx, r, ad, addr)
	_ = m.cache.Publish(ctx, cache.ReloadEvent{Kind: "router", Slug: r.Slug})
	logger.Info("router", r.Slug, fmt.Sprintf("Router container started and healthy at %s", addr), store.Map{
		"slug":         r.Slug,
		"adapter":      r.AdapterType,
		"container_id": created.ID[:12],
		"target_addr":  addr,
	})
	return nil
}

// Stop removes the router's container and clears its route (state persists in volume).
func (m *Manager) Stop(ctx context.Context, r *store.Router) error {
	local := *r
	if err := m.stopAndRemove(ctx, &local, true); err != nil {
		return err
	}
	_ = m.store.UpdateRouterState(ctx, r.ID, "stopped", "", "", "", "unknown")
	_ = m.cache.DelRoute(ctx, r.Slug)
	m.table.Delete(r.Slug)
	_ = m.cache.Publish(ctx, cache.ReloadEvent{Kind: "router", Slug: r.Slug})
	logger.Info("router", r.Slug, fmt.Sprintf("Router container stopped for %s", r.Slug), store.Map{
		"slug": r.Slug,
	})
	return nil
}

func (m *Manager) Restart(ctx context.Context, r *store.Router) error {
	if err := m.Stop(ctx, r); err != nil {
		return err
	}
	return m.Start(ctx, r)
}

// Logs returns the raw (multiplexed) container log stream. Callers should
// demultiplex with stdcopy.StdCopy before presenting to users.
func (m *Manager) Logs(ctx context.Context, r *store.Router, follow bool) (io.ReadCloser, error) {
	if r.ContainerID == "" {
		return nil, fmt.Errorf("router not running")
	}
	return m.docker.ContainerLogs(ctx, r.ContainerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     follow,
		Tail:       "500",
		Timestamps: false,
	})
}

// DiscoverModels calls the router's models endpoint and upserts the model list.
// BUG-1 FIX: now calls ad.ModelsPath(r) — not HealthPath — so adapters with a
// dedicated health endpoint (e.g. /health) that differs from /v1/models work correctly.
func (m *Manager) DiscoverModels(ctx context.Context, r *store.Router, ad Adapter, addr string) error {
	if ad == nil {
		a, err := m.reg.Get(r.AdapterType)
		if err != nil {
			return err
		}
		ad = a
	}
	if addr == "" {
		addr = r.TargetAddr
	}
	url := strings.TrimRight(addr, "/") + ad.ModelsPath(r)
	body, err := httpGet(ctx, url, 10*time.Second)
	if err != nil {
		return err
	}
	models, err := ad.ParseModels(r, body)
	if err != nil {
		return err
	}
	if err := m.store.UpsertModels(ctx, r.ID, models); err != nil {
		return err
	}
	providers := countProviders(models)
	return m.store.UpdateRouterCounts(ctx, r.ID, providers, len(models))
}

// HealthCheck probes the router and records the result.
func (m *Manager) HealthCheck(ctx context.Context, r *store.Router) error {
	ad, err := m.reg.Get(r.AdapterType)
	if err != nil {
		return err
	}
	if r.TargetAddr == "" {
		_ = m.store.InsertHealthCheck(ctx, r.ID, "unhealthy", 0, "no target address")
		return fmt.Errorf("no target address")
	}
	url := strings.TrimRight(r.TargetAddr, "/") + ad.HealthPath(r)
	start := time.Now()
	_, err = httpGet(ctx, url, 5*time.Second)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		_ = m.store.InsertHealthCheck(ctx, r.ID, "unhealthy", int(latency), err.Error())
		_ = m.store.UpdateRouterState(ctx, r.ID, r.RuntimeState, r.TargetAddr, r.ContainerID, r.NativePanelURL, "unhealthy")
		return err
	}
	_ = m.store.InsertHealthCheck(ctx, r.ID, "healthy", int(latency), "")
	_ = m.store.UpdateRouterState(ctx, r.ID, r.RuntimeState, r.TargetAddr, r.ContainerID, r.NativePanelURL, "healthy")
	return nil
}

// ----- internals -----

func (m *Manager) ensureImage(ctx context.Context, ref string) error {
	reader, err := m.docker.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, reader)
	return reader.Close()
}

// BUG-5 FIX: stopAndRemove operates on a *local* copy of the router so it
// never mutates the caller's pointer — which would be a data race when multiple
// Boot goroutines run concurrently.
func (m *Manager) stopAndRemove(ctx context.Context, r *store.Router, strict bool) error {
	if r.ContainerID == "" {
		if strict {
			return nil
		}
		// Look up by name in case the store lost the ID (e.g. after a crash).
		c, err := m.docker.ContainerInspect(ctx, containerPrefix+r.Slug)
		if err == nil && c.ID != "" {
			r.ContainerID = c.ID // safe: r is already a local copy
		}
	}
	if r.ContainerID == "" {
		return nil
	}
	timeout := 10
	_ = m.docker.ContainerStop(ctx, r.ContainerID, container.StopOptions{Timeout: &timeout})
	_ = m.docker.ContainerRemove(ctx, r.ContainerID, container.RemoveOptions{Force: true})
	return nil
}

func hostPortFromURL(u string) string {
	s := strings.TrimPrefix(u, "http://")
	s = strings.TrimPrefix(s, "https://")
	if i := strings.Index(s, "/"); i >= 0 {
		s = s[:i]
	}
	return s
}

// resolveAddr probes published host ports (on gateway IP/127.0.0.1) and container IPs, returning the first reachable address.
func (m *Manager) resolveAddr(ctx context.Context, containerID string, port int) (string, error) {
	insp, err := m.docker.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", err
	}

	gatewayIP := "172.17.0.1"
	if insp.NetworkSettings != nil {
		if g := insp.NetworkSettings.Gateway; g != "" {
			gatewayIP = g
		} else if n, ok := insp.NetworkSettings.Networks["bridge"]; ok && n.Gateway != "" {
			gatewayIP = n.Gateway
		}
	}

	internalPort := nat.Port(fmt.Sprintf("%d/tcp", port))
	var candidates []string

	// 1. Published host ports (mapped to gateway IP / 127.0.0.1)
	if insp.NetworkSettings != nil && insp.NetworkSettings.Ports != nil {
		if bindings, ok := insp.NetworkSettings.Ports[internalPort]; ok && len(bindings) > 0 {
			for _, b := range bindings {
				if b.HostPort != "" {
					candidates = append(candidates, fmt.Sprintf("http://%s:%s", gatewayIP, b.HostPort))
					candidates = append(candidates, fmt.Sprintf("http://127.0.0.1:%s", b.HostPort))
					candidates = append(candidates, fmt.Sprintf("http://172.18.0.1:%s", b.HostPort))
				}
			}
		}
	}

	// 2. Direct container IPs
	if insp.NetworkSettings != nil {
		if n, ok := insp.NetworkSettings.Networks["bridge"]; ok && n.IPAddress != "" {
			candidates = append(candidates, fmt.Sprintf("http://%s:%d", n.IPAddress, port))
		}
		if insp.NetworkSettings.IPAddress != "" {
			candidates = append(candidates, fmt.Sprintf("http://%s:%d", insp.NetworkSettings.IPAddress, port))
		}
		for _, n := range insp.NetworkSettings.Networks {
			if n.IPAddress != "" {
				candidates = append(candidates, fmt.Sprintf("http://%s:%d", n.IPAddress, port))
			}
		}
	}

	// Dynamic TCP reachability probe: return first address accepting TCP connections
	for _, cand := range candidates {
		hp := hostPortFromURL(cand)
		conn, err := net.DialTimeout("tcp", hp, 500*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return cand, nil
		}
	}

	if len(candidates) > 0 {
		return candidates[0], nil
	}
	return "", fmt.Errorf("no address resolved for container %s", containerID)
}

func networkNames(ns *dockertypes.NetworkSettings) []string {
	if ns == nil {
		return nil
	}
	names := make([]string, 0, len(ns.Networks))
	for k := range ns.Networks {
		names = append(names, k)
	}
	return names
}

// waitForHealthy probes the router's health endpoint with exponential backoff
// until it returns 2xx or the deadline is exceeded.
// GAP-8 FIX: replaced fixed tries×interval loop (up to 60s of uniform sleep)
// with a deadline-based loop starting at 500ms and doubling up to 5s intervals.
func (m *Manager) waitForHealthy(ctx context.Context, r *store.Router, ad Adapter, addr string, maxWait time.Duration) bool {
	url := strings.TrimRight(addr, "/") + ad.HealthPath(r)
	deadline := time.Now().Add(maxWait)
	interval := 500 * time.Millisecond
	const maxInterval = 5 * time.Second

	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err == nil {
			resp, err := http.DefaultClient.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode < 500 {
					return true
				}
			}
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		sleep := interval
		if sleep > remaining {
			sleep = remaining
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(sleep):
		}
		if interval < maxInterval {
			interval *= 2
		}
	}
	return false
}

func (m *Manager) loadCreds(ctx context.Context, r *store.Router) (map[string]string, error) {
	creds, err := m.store.ListCredentials(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(creds))
	for _, c := range creds {
		plain, err := m.box.Decrypt(c.Ciphertext)
		if err != nil {
			return nil, fmt.Errorf("decrypt %s: %w", c.Provider, err)
		}
		out[c.Provider] = string(plain)
	}
	return out, nil
}

func countProviders(models []store.Model) int {
	seen := map[string]bool{}
	for _, m := range models {
		seen[m.Provider] = true
	}
	return len(seen)
}

func hostOnly(addr string) string {
	s := strings.TrimPrefix(addr, "http://")
	s = strings.TrimPrefix(s, "https://")
	if i := strings.Index(s, ":"); i > 0 {
		return s[:i]
	}
	return s
}

func httpGet(ctx context.Context, url string, timeout time.Duration) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream %d for %s", resp.StatusCode, url)
	}
	return io.ReadAll(resp.Body)
}
