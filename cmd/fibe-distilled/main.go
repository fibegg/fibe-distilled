package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	api "github.com/fibegg/fibe-distilled/internal/api"
	"github.com/fibegg/fibe-distilled/internal/config"
	"github.com/fibegg/fibe-distilled/internal/domain"
	"github.com/fibegg/fibe-distilled/internal/runtime"
	store "github.com/fibegg/fibe-distilled/internal/storage"
	"github.com/fibegg/fibe-distilled/internal/worker"
)

// main exits the process when startup, recovery, or serving fails.
func main() {
	configureLogging()
	if err := run(); err != nil {
		slog.Error("fibe-distilled failed", "error", err)
		os.Exit(1)
	}
}

// configureLogging installs the process-wide structured text logger.
func configureLogging() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
}

// run loads process configuration and wires storage, workers, and HTTP serving.
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opened, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err := opened.store.Close(); err != nil {
			slog.Error("database close failed", "error", err)
		}
	}()
	wk := newWorker(cfg, opened.store, opened.serverID)
	if err := wk.Runtime.EnsureLocalRuntimeBootstrap(ctx, opened.marquee); err != nil {
		return fmt.Errorf("local runtime bootstrap: %w", err)
	}
	recoverInterruptedState(ctx, opened.store)

	handler := api.New(cfg, opened.store, wk)
	wk.StartPlayguard(ctx, cfg.PlayguardInterval)
	return serve(ctx, cfg.Addr, handler)
}

// openedStore carries startup storage and identity state.
type openedStore struct {
	store    *store.DB
	serverID string
	marquee  domain.Marquee
}

// openStore connects to SQLite and persists the startup-configured Marquee.
func openStore(ctx context.Context, cfg config.Config) (openedStore, error) {
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return openedStore{}, fmt.Errorf("database: %w", err)
	}
	serverID, err := st.ServerID(ctx)
	if err != nil {
		return openedStore{}, closeStoreAfterOpenError(st, fmt.Errorf("server identity: %w", err))
	}
	configuredMarquee, err := st.EnsureConfiguredMarquee(ctx, cfg.Marquee.ToDomain())
	if err != nil {
		return openedStore{}, closeStoreAfterOpenError(st, fmt.Errorf("configured marquee: %w", err))
	}
	slog.Info("configured local marquee", "marquee_id", configuredMarquee.ID, "domain", cfg.Marquee.Domain)
	return openedStore{store: st, serverID: serverID, marquee: configuredMarquee}, nil
}

// recoverInterruptedState marks in-flight work from a previous process as failed.
func recoverInterruptedState(ctx context.Context, st *store.DB) {
	if recovered, err := st.RecoverInterruptedAsyncOperations(ctx); err != nil {
		slog.Error("async recovery failed", "error", err)
	} else if recovered > 0 {
		slog.Info("marked interrupted async operations", "count", recovered)
	}
	if recovered, err := st.RecoverInterruptedPlaygrounds(ctx); err != nil {
		slog.Error("playground recovery failed", "error", err)
	} else if recovered > 0 {
		slog.Info("marked interrupted playground deployments", "count", recovered)
	}
}

// newWorker builds the in-process worker with the production local runtime.
func newWorker(cfg config.Config, st *store.DB, serverID string) worker.Worker {
	return worker.Worker{
		DB:                 st,
		DefaultGitHubToken: cfg.GitHubTok,
		Runtime: runtime.Checker{
			DockerHubUsername: cfg.DockerHubUsername,
			DockerHubToken:    cfg.DockerHubToken,
			InstanceID:        serverID,
		},
	}
}

// serve runs HTTP until the process context is cancelled or the server fails.
func serve(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("fibe-distilled listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- fmt.Errorf("server: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-serverErr:
		return err
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown failed", "error", err)
	}
	return nil
}
