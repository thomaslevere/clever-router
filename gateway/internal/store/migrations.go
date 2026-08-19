package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrate applies any pending embedded SQL migrations in filename order.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.Pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			id          TEXT PRIMARY KEY,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var exists int
		err := s.Pool.QueryRow(ctx, `SELECT 1 FROM schema_migrations WHERE id=$1`, name).Scan(&exists)
		if err == nil {
			continue
		}
		data, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
		if _, err := s.Pool.Exec(ctx2, string(data)); err != nil {
			cancel()
			return fmt.Errorf("apply %s: %w", name, err)
		}
		cancel()
		if _, err := s.Pool.Exec(ctx, `INSERT INTO schema_migrations (id) VALUES ($1)`, name); err != nil {
			return err
		}
	}
	return nil
}
