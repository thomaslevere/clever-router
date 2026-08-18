package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPPort string

	PostgresURL string
	RedisAddr   string
	RedisPass   string
	RedisDB     int

	EncryptionKey string

	VolumeScratchDir string

	Cellar CellarConfig

	DockerSocket string

	AdminInternalAddr string

	AllowedImages []string

	AdminToken string

	Environment string
}

type CellarConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	Bucket    string
	Region    string
	UseSSL    bool
}

func (c *CellarConfig) Enabled() bool {
	return c.Endpoint != "" && c.AccessKey != "" && c.SecretKey != ""
}

func (c *Config) HasCellar() bool {
	return c.Cellar.Enabled()
}

func Load() (*Config, error) {
	// Clean endpoint if host has http:// or https:// prefix
	rawEndpoint := firstNonEmpty(
		getenv("CELLAR_ADDON_HOST", ""),
		getenv("CELLAR_ENDPOINT", ""),
		getenv("S3_ENDPOINT", ""),
	)
	cleanedEndpoint := rawEndpoint
	useSSL := getbool("CELLAR_SSL", true)
	if strings.HasPrefix(rawEndpoint, "https://") {
		cleanedEndpoint = strings.TrimPrefix(rawEndpoint, "https://")
		useSSL = true
	} else if strings.HasPrefix(rawEndpoint, "http://") {
		cleanedEndpoint = strings.TrimPrefix(rawEndpoint, "http://")
		useSSL = false
	}
	cleanedEndpoint = strings.TrimRight(cleanedEndpoint, "/")

	cfg := &Config{
		HTTPPort:          getenv("PORT", "8080"),
		EncryptionKey:     getenv("ENCRYPTION_KEY", ""),
		DockerSocket:      getenv("DOCKER_HOST", "unix:///var/run/docker.sock"),
		AdminInternalAddr: getenv("ADMIN_INTERNAL_ADDR", "127.0.0.1:3000"),
		AllowedImages:     splitCSV(getenv("ALLOWED_IMAGES", "diegosouzapw/omniroute:latest,ghcr.io/berriai/litellm:main-stable,decolua/9router:latest,ghcr.io/decolua/9router:latest,9router/9router:latest")),
		AdminToken:        getenv("ADMIN_API_KEY", ""),
		Environment:       getenv("APP_ENV", "production"),
		VolumeScratchDir:  getenv("VOLUME_SCRATCH_DIR", "/tmp/clever_router_volumes"),
		Cellar: CellarConfig{
			Endpoint:  cleanedEndpoint,
			AccessKey: firstNonEmpty(getenv("CELLAR_ADDON_KEY_ID", ""), getenv("CELLAR_ACCESS_KEY", ""), getenv("AWS_ACCESS_KEY_ID", "")),
			SecretKey: firstNonEmpty(getenv("CELLAR_ADDON_KEY_SECRET", ""), getenv("CELLAR_SECRET_KEY", ""), getenv("AWS_SECRET_ACCESS_KEY", "")),
			Bucket:    firstNonEmpty(getenv("CELLAR_BUCKET", ""), getenv("S3_BUCKET", ""), "clever-router-storage"),
			Region:    getenv("CELLAR_REGION", getenv("AWS_REGION", "us-east-1")),
			UseSSL:    useSSL,
		},
	}

	// ---- PostgreSQL ----
	// Clever Cloud postgresql-addon injects POSTGRESQL_ADDON_URI.
	// The app also accepts the generic DATABASE_URL (local dev / other providers).
	cfg.PostgresURL = firstNonEmpty(
		getenv("DATABASE_URL", ""),
		getenv("POSTGRESQL_ADDON_URI", ""),
	)
	if cfg.PostgresURL == "" {
		return nil, fmt.Errorf("no PostgreSQL URL found: set DATABASE_URL or link a postgresql-addon")
	}

	// ---- Redis ----
	// Clever Cloud redis-addon injects REDIS_URL as a full URI:
	//   redis://127.0.0.1:6379 or redis://:password@host:6379/0
	// Our redis client needs Addr (host:port), Password, and DB separately.
	redisURI := firstNonEmpty(
		getenv("REDIS_URL", ""),
		getenv("REDIS_ADDON_URI", ""),
		getenv("REDIS_ADDR", ""),
	)
	if redisURI == "" {
		redisURI = "localhost:6379"
	}
	addr, pass, db, err := parseRedisURL(redisURI)
	if err != nil {
		return nil, fmt.Errorf("parse Redis URL: %w", err)
	}
	cfg.RedisAddr = addr
	if cfg.RedisPass == "" {
		cfg.RedisPass = pass
	}
	if cfg.RedisDB == 0 {
		cfg.RedisDB = db
	}
	// Allow explicit overrides.
	if p := getenv("REDIS_PASSWORD", ""); p != "" {
		cfg.RedisPass = p
	}
	if dbStr := getenv("REDIS_DB", ""); dbStr != "" {
		if n, err := strconv.Atoi(dbStr); err == nil {
			cfg.RedisDB = n
		}
	}

	// ---- Validation ----
	if cfg.EncryptionKey == "" {
		return nil, fmt.Errorf("ENCRYPTION_KEY is required (32-byte hex; generate with: openssl rand -hex 32)")
	}
	if len(cfg.EncryptionKey) != 64 {
		return nil, fmt.Errorf("ENCRYPTION_KEY must be 32 bytes hex (64 chars), got %d", len(cfg.EncryptionKey))
	}
	if cfg.AdminToken == "" && !cfg.IsDev() {
		return nil, fmt.Errorf("ADMIN_API_KEY is required (or set APP_ENV=dev)")
	}
	if cfg.AdminToken == "" {
		cfg.AdminToken = "dev-admin-token"
	}
	return cfg, nil
}

func (c *Config) IsDev() bool { return c.Environment == "dev" || c.Environment == "development" }

// parseRedisURL accepts either a plain host:port address or a full
// redis://[:password@]host:port[/db] URI (as injected by Clever Cloud).
func parseRedisURL(raw string) (addr, password string, db int, err error) {
	if !strings.Contains(raw, "://") {
		// Plain host:port — used in local dev or explicit REDIS_ADDR.
		return raw, "", 0, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", 0, fmt.Errorf("invalid redis URL %q: %w", raw, err)
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "6379"
	}
	addr = host + ":" + port
	if u.User != nil {
		password, _ = u.User.Password()
	}
	if u.Path != "" && u.Path != "/" {
		dbStr := strings.TrimPrefix(u.Path, "/")
		if n, parseErr := strconv.Atoi(dbStr); parseErr == nil {
			db = n
		}
	}
	return addr, password, db, nil
}

// ----- helpers -----

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		if v != "" {
			return v
		}
	}
	return def
}

func getbool(key string, def bool) bool {
	if v, ok := os.LookupEnv(key); ok {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
