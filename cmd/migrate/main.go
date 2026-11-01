package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"meto/internal/config"
	"meto/internal/database"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	if len(os.Args) != 2 || (os.Args[1] != "up" && os.Args[1] != "status") {
		return errors.New("usage: go run ./cmd/migrate [up|status]")
	}
	command := os.Args[1]

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	return migrate(ctx, cfg.DatabaseURL, command, os.DirFS("migrations"))
}

func migrate(ctx context.Context, databaseURL, command string, migrations fs.FS) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	connectCtx, cancelConnect := context.WithTimeout(ctx, 10*time.Second)
	pool, err := database.Open(connectCtx, databaseURL)
	cancelConnect()
	if err != nil {
		return err
	}
	defer pool.Close()

	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(
		goose.DialectPostgres,
		db,
		migrations,
		goose.WithSessionLocker(locker),
	)
	if errors.Is(err, goose.ErrNoMigrations) && command == "status" {
		fmt.Println("No SQL migration files found in migrations/.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("initialize migrations: %w", err)
	}

	if command == "up" {
		results, err := provider.Up(ctx)
		if err != nil {
			return fmt.Errorf("apply migrations: %w", err)
		}
		fmt.Printf("Applied %d migration(s).\n", len(results))
	}

	current, target, err := provider.GetVersions(ctx)
	if err != nil {
		return fmt.Errorf("read migration versions: %w", err)
	}
	fmt.Printf("Database version: %d, target version: %d\n", current, target)
	return nil
}
