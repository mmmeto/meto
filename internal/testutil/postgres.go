package testutil

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"meto/internal/database"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Postgres는 명시적으로 지정한 테스트 DB 안에 독립된 스키마를 만듭니다.
func Postgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	rawURL := os.Getenv("METO_TEST_DATABASE_URL")
	if rawURL == "" {
		t.Skip("set METO_TEST_DATABASE_URL to a dedicated meto_test_* database")
	}
	cfg, err := pgxpool.ParseConfig(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(cfg.ConnConfig.Database, "meto_test_") {
		t.Fatal("integration tests require a database named meto_test_*")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") {
		t.Fatal("METO_TEST_DATABASE_URL must be a PostgreSQL URL")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	admin, err := database.Open(ctx, rawURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("meto_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		defer admin.Close()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("clean up test schema: %v", err)
		}
	})
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	pool, err := database.Open(ctx, parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}
