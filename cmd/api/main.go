package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"meto/internal/auth"
	"meto/internal/config"
	"meto/internal/database"
	"meto/internal/friend"
	"meto/internal/httpapi"
	"meto/internal/user"
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
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	verifier, err := auth.NewFirebaseVerifier(ctx, cfg.FirebaseProjectID)
	if err != nil {
		return err
	}

	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	pool, err := database.Open(connectCtx, cfg.DatabaseURL)
	cancel()
	if err != nil {
		return err
	}
	defer pool.Close()
	log.Print("database connected")

	router, err := httpapi.NewRouter(
		pool,
		verifier,
		user.NewService(user.NewRepository(pool)),
		friend.NewService(friend.NewRepository(pool)),
	)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("meto API listening on %s", cfg.HTTPAddr)
		serveErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	case <-ctx.Done():
		log.Print("shutting down API server")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	log.Print("API server stopped")
	return nil
}
