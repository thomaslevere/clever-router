package config

import (
	"os"
	"testing"
)

func TestConfigCellarLoading(t *testing.T) {
	os.Setenv("ENCRYPTION_KEY", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	os.Setenv("DATABASE_URL", "postgresql://user:pass@localhost:5432/db")
	os.Setenv("ADMIN_API_KEY", "test-admin-key")
	os.Setenv("CELLAR_ADDON_HOST", "cellar-c2.services.clever-cloud.com")
	os.Setenv("CELLAR_ADDON_KEY_ID", "mykeyid")
	os.Setenv("CELLAR_ADDON_KEY_SECRET", "mysecretkey")
	os.Setenv("CELLAR_BUCKET", "my-test-bucket")
	defer func() {
		os.Unsetenv("CELLAR_ADDON_HOST")
		os.Unsetenv("CELLAR_ADDON_KEY_ID")
		os.Unsetenv("CELLAR_ADDON_KEY_SECRET")
		os.Unsetenv("CELLAR_BUCKET")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if !cfg.HasCellar() {
		t.Fatal("expected HasCellar() to be true")
	}

	if cfg.Cellar.Endpoint != "cellar-c2.services.clever-cloud.com" {
		t.Errorf("got endpoint %s, want cellar-c2.services.clever-cloud.com", cfg.Cellar.Endpoint)
	}
	if cfg.Cellar.AccessKey != "mykeyid" {
		t.Errorf("got key %s, want mykeyid", cfg.Cellar.AccessKey)
	}
	if cfg.Cellar.SecretKey != "mysecretkey" {
		t.Errorf("got secret %s, want mysecretkey", cfg.Cellar.SecretKey)
	}
	if cfg.Cellar.Bucket != "my-test-bucket" {
		t.Errorf("got bucket %s, want my-test-bucket", cfg.Cellar.Bucket)
	}
}
