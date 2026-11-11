package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"meto/internal/database"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrations(t *testing.T) {
	databaseURL := os.Getenv("METO_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set METO_TEST_DATABASE_URL to a dedicated meto_test_* database")
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.ConnConfig.Database, "meto_test_") {
		t.Fatal("integration tests require a database named meto_test_*")
	}
	parsedURL, err := url.Parse(databaseURL)
	if err != nil || (parsedURL.Scheme != "postgres" && parsedURL.Scheme != "postgresql") {
		t.Fatal("METO_TEST_DATABASE_URL must be a PostgreSQL URL")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	admin, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	schema := fmt.Sprintf("meto_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("clean up test schema: %v", err)
		}
	}()
	query := parsedURL.Query()
	query.Set("search_path", schema)
	parsedURL.RawQuery = query.Encode()
	testURL := parsedURL.String()

	migrations := os.DirFS(filepath.Join("..", "..", "migrations"))
	if err := migrate(ctx, testURL, "status", migrations); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, testURL, "up", migrations); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, testURL, "up", migrations); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(ctx, testURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var visibility, permission string
	var completed bool
	err = pool.QueryRow(ctx, `INSERT INTO users(id) VALUES ('legacy-firebase-uid')
		RETURNING default_visibility, calendar_permission_status, onboarding_completed`).Scan(
		&visibility, &permission, &completed,
	)
	if err != nil {
		t.Fatal(err)
	}
	if visibility != "time_only" || permission != "unknown" || completed {
		t.Fatalf("unexpected defaults: %q %q %v", visibility, permission, completed)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users(id) VALUES ('pending-user')`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users(id, username, onboarding_completed)
		VALUES ('complete-user', 'meto_user', true)`); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name       string
		statement  string
		code       string
		constraint string
	}{
		{"duplicate username", `INSERT INTO users(id, username) VALUES ('duplicate', 'meto_user')`, "23505", "users_username_key"},
		{"invalid username", `INSERT INTO users(id, username) VALUES ('invalid', 'UPPERCASE')`, "23514", "users_username_valid"},
		{"invalid visibility", `INSERT INTO users(id, default_visibility) VALUES ('visibility', 'public')`, "23514", "users_visibility_valid"},
		{"missing username", `INSERT INTO users(id, onboarding_completed) VALUES ('incomplete', true)`, "23514", "users_onboarding_valid"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, tt.statement)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != tt.code || pgErr.ConstraintName != tt.constraint {
				t.Fatalf("unexpected constraint result: %v", err)
			}
		})
	}

	broken := fstest.MapFS{
		"99999_failure.sql": &fstest.MapFile{Data: []byte(`-- +goose Up
CREATE TABLE rollback_probe(id INT);
SELECT 1 / 0;
-- +goose Down
DROP TABLE rollback_probe;
`)},
	}
	applied, err := filepath.Glob(filepath.Join("..", "..", "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range applied {
		sql, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		broken[filepath.Base(path)] = &fstest.MapFile{Data: sql}
	}
	var versionBefore int64
	if err := pool.QueryRow(ctx, "SELECT MAX(version_id) FROM goose_db_version").Scan(&versionBefore); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, testURL, "up", broken); err == nil {
		t.Fatal("broken migration must fail")
	}
	var rolledBack bool
	if err := pool.QueryRow(ctx, "SELECT to_regclass('rollback_probe') IS NULL").Scan(&rolledBack); err != nil {
		t.Fatal(err)
	}
	if !rolledBack {
		t.Fatal("failed migration left its table behind")
	}
	var version int64
	if err := pool.QueryRow(ctx, "SELECT MAX(version_id) FROM goose_db_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != versionBefore {
		t.Fatalf("database version = %d after rollback, want %d", version, versionBefore)
	}
}
