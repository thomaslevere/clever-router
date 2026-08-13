package config

import (
	"fmt"
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

func Load() (*Config, error) {
	cfg := &Config{
		HTTPPort:           getenv("PORT", "8080"),
		PostgresURL:        getenv("DATABASE_URL", ""),
		RedisAddr:          getenv("REDIS_URL", getenv("REDIS_ADDR", "localhost:6379")),
		RedisPass:          getenv("REDIS_PASSWORD", ""),
		EncryptionKey:      getenv("ENCRYPTION_KEY", ""),
		DockerSocket:       getenv("DOCKER_HOST", "unix:///var/run/docker.sock"),
		AdminInternalAddr:  getenv("ADMIN_INTERNAL_ADDR", "127.0.0.1:3000"),
		AllowedImages:      splitCSV(getenv("ALLOWED_IMAGES", "diegosouzapw/omniroute:latest,ghcr.io/berriai/litellm:main-stable")),
		AdminToken:         getenv("ADMIN_API_KEY", ""),
		Environment:        getenv("APP_ENV", "production"),
		Cellar: CellarConfig{
			Endpoint:  getenv("CELLAR_ENDPOINT", getenv("S3_ENDPOINT", "")),
			AccessKey: getenv("CELLAR_ACCESS_KEY", getenv("AWS_ACCESS_KEY_ID", "")),
			SecretKey: getenv("CELLAR_SECRET_KEY", getenv("AWS_SECRET_ACCESS_KEY", "")),
			Bucket:    getenv("CELLAR_BUCKET", getenv("S3_BUCKET", "clever-route")),
			Region:    getenv("CELLAR_REGION", getenv("AWS_REGION", "us-east-1")),
			UseSSL:    getbool("CELLAR_SSL", true),
		},
	}

	db, err := strconv.Atoi(getenv("REDIS_DB", "0"))
	if err != nil {
		return nil, fmt.Errorf("invalid REDIS_DB: %w", err)
	}
	cfg.RedisDB = db

	if cfg.PostgresURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.EncryptionKey == "" {
		return nil, fmt.Errorf("ENCRYPTION_KEY is required (32-byte hex for AES-256)")
	}
	if len(cfg.EncryptionKey) != 64 {
		return nil, fmt.Errorf("ENCRYPTION_KEY must be 32 bytes hex (64 chars), got %d", len(cfg.EncryptionKey))
	}
	if cfg.AdminToken == "" && !cfg.IsDev() {
		return nil, fmt.Errorf("ADMIN_API_KEY is required (or set APP_ENV=dev for a dev default)")
	}
	if cfg.AdminToken == "" {
		cfg.AdminToken = "dev-admin-token"
	}
	return cfg, nil
}

func (c *Config) IsDev() bool { return c.Environment == "dev" || c.Environment == "development" }

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
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
