package adapters

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/clever-route/gateway/internal/cache"
	"github.com/clever-route/gateway/internal/logger"
	"github.com/clever-route/gateway/internal/router"
	"github.com/clever-route/gateway/internal/secrets"
	"github.com/clever-route/gateway/internal/storage"
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
	store      *store.Store
	cache      *cache.Cache
	box        *secrets.Box
	docker     *client.Client
	reg        *Registry
	allowed    map[string]bool
	table      *router.Table
	bridge     *storage.FastVolumeBridge
	scratchDir string
	watchersMu sync.Mutex
	watchers   map[string][]*storage.VolumeWatcher
}

func NewManager(st *store.Store, c *cache.Cache, box *secrets.Box, reg *Registry, allowed []string, table *router.Table, bridge *storage.FastVolumeBridge, scratchDir string) (*Manager, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	allow := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		allow[strings.TrimSpace(a)] = true
	}
	if scratchDir == "" {
		scratchDir = "/tmp/clever_router_volumes"
	}
	m := &Manager{
		store:      st,
		cache:      c,
		box:        box,
		docker:     cli,
		reg:        reg,
		allowed:    allow,
		table:      table,
		bridge:     bridge,
		scratchDir: scratchDir,
		watchers:   make(map[string][]*storage.VolumeWatcher),
	}

	// Ensure the dedicated network exists. Best-effort: log on failure.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.ensureNetwork(ctx); err != nil {
		log.Printf("[manager] warning: could not ensure docker network %q: %v", networkName, err)
	}

	return m, nil
}

func (m *Manager) Close() error {
	m.watchersMu.Lock()
	for _, ws := range m.watchers {
		for _, w := range ws {
			w.Close()
		}
	}
	m.watchers = make(map[string][]*storage.VolumeWatcher)
	m.watchersMu.Unlock()

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

// InspectImageVolumes queries the Docker daemon for declared VOLUME paths in image metadata.
func (m *Manager) InspectImageVolumes(ctx context.Context, imageName string) ([]string, error) {
	insp, _, err := m.docker.ImageInspectWithRaw(ctx, imageName)
	if err != nil {
		return nil, err
	}
	if insp.Config != nil && len(insp.Config.Volumes) > 0 {
		vols := make([]string, 0, len(insp.Config.Volumes))
		for v := range insp.Config.Volumes {
			vols = append(vols, v)
		}
		return vols, nil
	}
	return nil, nil
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

	// 1. Ensure permanent crypto keys & secrets are saved to DB
	if savedEnv, modified := ad.EnsurePermanentSecrets(ctx, r, m.box); modified {
		if err := m.store.UpdateRouterEnv(ctx, r.ID, savedEnv, r.AutoRestartOnEnvChange); err != nil {
			log.Printf("[manager] warning: failed to persist permanent router secrets: %v", err)
		} else {
			r.EnvVars = savedEnv
		}
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

	// 2. Discover Volumes from Docker image metadata or adapter contract
	volumes, _ := m.InspectImageVolumes(ctx, r.ImageRef)
	if len(volumes) == 0 {
		volumes = ad.DeclaredVolumes(r)
	}
	if len(volumes) == 0 {
		volumes = []string{"/app/data"}
	}

	var dockerBinds []string
	var activeWatchers []*storage.VolumeWatcher

	// 3. Hydrate each volume from S3 & attach real-time inotify watcher
	for _, targetVol := range volumes {
		sanitized := strings.TrimPrefix(strings.ReplaceAll(targetVol, "/", "_"), "_")
		if sanitized == "" {
			sanitized = "data"
		}
		localPath := filepath.Join(m.scratchDir, r.ID, sanitized)
		s3Key := fmt.Sprintf("namespaces/%s/%s.tar.zst", r.ID, sanitized)

		_ = os.MkdirAll(localPath, 0755)

		// Fast Hydration from S3 if bridge is configured
		if m.bridge != nil {
			if err := m.bridge.HydrateFromS3(ctx, s3Key, localPath); err != nil {
				log.Printf("[manager] hydrate warning for %s (%s): %v", r.Slug, s3Key, err)
			}

			// Attach real-time inotify watcher
			watcher, err := storage.NewVolumeWatcher(m.bridge, localPath, s3Key, 800*time.Millisecond)
			if err == nil {
				watcher.Start(ctx)
				activeWatchers = append(activeWatchers, watcher)
			} else {
				log.Printf("[manager] warning: could not create volume watcher for %s: %v", localPath, err)
			}
		}

		dockerBinds = append(dockerBinds, fmt.Sprintf("%s:%s", localPath, targetVol))
	}

	// Record active watchers for this router ID
	m.watchersMu.Lock()
	if oldWatchers, ok := m.watchers[r.ID]; ok {
		for _, w := range oldWatchers {
			w.Close()
		}
	}
	m.watchers[r.ID] = activeWatchers
	m.watchersMu.Unlock()

	creds, err := m.loadCreds(ctx, r)
	if err != nil {
		return fmt.Errorf("load credentials: %w", err)
	}

	// Decrypt environment variables for container runtime injection
	localForStart := *r
	localForStart.EnvVars = m.decryptRouterEnv(r)

	lim := ad.ResourceLimits(&localForStart)
	pidsLimit := lim.PidsLimit

	name := containerPrefix + r.Slug
	internalPort := nat.Port(fmt.Sprintf("%d/tcp", ad.InternalPort(&localForStart)))
	cfg := &container.Config{
		Image: r.ImageRef,
		Env:   ad.Env(&localForStart, creds),
		ExposedPorts: nat.PortSet{
			internalPort: struct{}{},
		},
		Labels: map[string]string{
			"app":        "clever-route",
			"router":     r.Slug,
			"adapter":    r.AdapterType,
			"managed-by": "clever-route",
		},
	}
	host := &container.HostConfig{
		Binds:         dockerBinds,
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
		AutoRemove:    false,
		PortBindings: nat.PortMap{
			internalPort: []nat.PortBinding{
				{HostIP: "0.0.0.0", HostPort: ""},
			},
		},
		// Enforce resource limits to prevent a runaway container from starving the host
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

	// Clean up any stale container with the same name to avoid Docker name conflict error
	if existing, err := m.docker.ContainerInspect(ctx, name); err == nil && existing.ID != "" {
		stopTimeout := 2
		_ = m.docker.ContainerStop(ctx, existing.ID, container.StopOptions{Timeout: &stopTimeout})
		_ = m.docker.ContainerRemove(ctx, existing.ID, container.RemoveOptions{Force: true})
	}

	created, err := m.docker.ContainerCreate(ctx, cfg, host, netCfg, nil, name)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	if err := m.docker.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		_ = m.docker.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
		return fmt.Errorf("start container: %w", err)
	}

	workingAddr, ok := m.waitForHealthy(ctx, r, ad, created.ID, ad.InternalPort(r), 90*time.Second)
	if !ok {
		fallbackAddr := "http://127.0.0.1"
		if cands := m.getCandidates(ctx, created.ID, ad.InternalPort(r)); len(cands) > 0 {
			fallbackAddr = cands[0]
		}
		endpoint := r.EndpointPath
		if endpoint == "" {
			endpoint = "/" + r.Slug
		}
		panel := endpoint + ad.NativePanelPath(r)
		_ = m.store.UpdateRouterState(ctx, r.ID, "unhealthy", fallbackAddr, created.ID, panel, "unhealthy")
		_ = m.cache.Publish(ctx, cache.ReloadEvent{Kind: "router", Slug: r.Slug})
		return fmt.Errorf("router %s started but did not pass health check within 90s", r.Slug)
	}

	endpoint := r.EndpointPath
	if endpoint == "" {
		endpoint = "/" + r.Slug
	}
	panel := endpoint + ad.NativePanelPath(r)
	if err := m.store.UpdateRouterState(ctx, r.ID, "running", workingAddr, created.ID, panel, "healthy"); err != nil {
		return err
	}
	if err := m.cache.SetRoute(ctx, r.Slug, workingAddr); err != nil {
		return err
	}
	m.table.Set(r.Slug, workingAddr)

	// Best-effort discovery; never block traffic on it.
	_ = m.DiscoverModels(ctx, r, ad, workingAddr)
	_ = m.cache.Publish(ctx, cache.ReloadEvent{Kind: "router", Slug: r.Slug})
	logger.Info("router", r.Slug, fmt.Sprintf("Router container started and healthy at %s", workingAddr), store.Map{
		"slug":         r.Slug,
		"adapter":      r.AdapterType,
		"container_id": created.ID[:12],
		"target_addr":  workingAddr,
	})
	return nil
}

// Stop removes the router's container and clears its route, flushing volume snapshots to S3.
func (m *Manager) Stop(ctx context.Context, r *store.Router) error {
	// 1. Stop active file watchers for this router
	m.watchersMu.Lock()
	if ws, ok := m.watchers[r.ID]; ok {
		for _, w := range ws {
			w.Close()
		}
		delete(m.watchers, r.ID)
	}
	m.watchersMu.Unlock()

	// 2. Stop container gracefully with timeout to allow SQLite WAL flush
	local := *r
	if err := m.stopAndRemove(ctx, &local, true); err != nil {
		return err
	}

	// 3. Final synchronous flush of all volumes to S3
	if m.bridge != nil {
		volumes, _ := m.InspectImageVolumes(ctx, r.ImageRef)
		if len(volumes) == 0 {
			if ad, err := m.reg.Get(r.AdapterType); err == nil {
				volumes = ad.DeclaredVolumes(r)
			}
		}
		if len(volumes) == 0 {
			volumes = []string{"/app/data"}
		}
		for _, targetVol := range volumes {
			sanitized := strings.TrimPrefix(strings.ReplaceAll(targetVol, "/", "_"), "_")
			if sanitized == "" {
				sanitized = "data"
			}
			localPath := filepath.Join(m.scratchDir, r.ID, sanitized)
			s3Key := fmt.Sprintf("namespaces/%s/%s.tar.zst", r.ID, sanitized)
			if err := m.bridge.StreamSnapshotToS3(ctx, localPath, s3Key); err != nil {
				log.Printf("[manager] warning: final snapshot error for %s (%s): %v", r.Slug, s3Key, err)
			}
		}
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

// StopAll stops all active router containers and snapshots their volumes before shutdown.
func (m *Manager) StopAll(ctx context.Context) {
	routers, err := m.store.ListRouters(ctx)
	if err != nil {
		log.Printf("[manager] stop all list routers: %v", err)
		return
	}
	for _, r := range routers {
		if r.RuntimeState == "running" || r.ContainerID != "" {
			log.Printf("[manager] stopping router %s and snapshotting state to S3...", r.Slug)
			if err := m.Stop(ctx, &r); err != nil {
				log.Printf("[manager] error stopping %s: %v", r.Slug, err)
			}
		}
	}
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

func (m *Manager) getCandidates(ctx context.Context, containerID string, port int) []string {
	insp, err := m.docker.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil
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

	// 1. Direct container IPs (preferred for bridge container-to-container routing)
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

	// 2. Published host ports
	if insp.NetworkSettings != nil && insp.NetworkSettings.Ports != nil {
		if bindings, ok := insp.NetworkSettings.Ports[internalPort]; ok && len(bindings) > 0 {
			for _, b := range bindings {
				if b.HostPort != "" {
					candidates = append(candidates, fmt.Sprintf("http://127.0.0.1:%s", b.HostPort))
					candidates = append(candidates, fmt.Sprintf("http://%s:%s", gatewayIP, b.HostPort))
				}
			}
		}
	}

	return candidates
}

func (m *Manager) resolveAddr(ctx context.Context, containerID string, port int) (string, error) {
	cands := m.getCandidates(ctx, containerID, port)
	for _, cand := range cands {
		hp := hostPortFromURL(cand)
		conn, err := net.DialTimeout("tcp", hp, 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return cand, nil
		}
	}
	if len(cands) > 0 {
		return cands[0], nil
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

// waitForHealthy probes the router's health endpoint across candidate addresses with exponential backoff.
func (m *Manager) waitForHealthy(ctx context.Context, r *store.Router, ad Adapter, containerID string, port int, maxWait time.Duration) (string, bool) {
	deadline := time.Now().Add(maxWait)
	interval := 500 * time.Millisecond
	const maxInterval = 5 * time.Second
	probeClient := &http.Client{Timeout: 3 * time.Second}

	for time.Now().Before(deadline) {
		candidates := m.getCandidates(ctx, containerID, port)
		for _, cand := range candidates {
			url := strings.TrimRight(cand, "/") + ad.HealthPath(r)
			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err == nil {
				resp, err := probeClient.Do(req)
				if err == nil {
					status := resp.StatusCode
					resp.Body.Close()
					log.Printf("[health-check] %s candidate %s -> HTTP %d", r.Slug, cand, status)
					if status < 500 {
						return cand, true
					}
				} else {
					log.Printf("[health-check] %s candidate %s -> err: %v", r.Slug, cand, err)
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
			return "", false
		case <-time.After(sleep):
		}
		if interval < maxInterval {
			interval *= 2
		}
	}
	return "", false
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

func (m *Manager) decryptRouterEnv(r *store.Router) []store.EnvVariable {
	if len(r.EnvVars) == 0 {
		return nil
	}
	out := make([]store.EnvVariable, len(r.EnvVars))
	for i, ev := range r.EnvVars {
		out[i] = ev
		if ev.IsSecret && secrets.IsEncrypted(ev.Value) {
			dec, err := secrets.DecryptValue(m.box, ev.Value)
			if err == nil {
				out[i].Value = dec
			} else {
				log.Printf("[manager] warning: failed to decrypt env var %s for router %s: %v", ev.Key, r.Slug, err)
			}
		}
	}
	return out
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
