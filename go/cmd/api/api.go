// Command api serves the web API used by the web app (web/): Discord login
// and guild settings. It keeps no state of its own (sessions and login state
// are in Redis/Valkey, settings in MariaDB), so several can run behind a load
// balancer.
//
// Lifecycle:
//  1. Load config (yaml file + BXT_* env overrides).
//  2. Connect to MariaDB (migrations are the worker's job) and Redis/Valkey.
//  3. Serve HTTP until SIGINT/SIGTERM, then drain in-flight requests.
//
// The path to the config file can be set with -config (default: ./config.yaml).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gitlab.com/jacxb/bots/bxt/go/internal/api"
	"gitlab.com/jacxb/bots/bxt/go/internal/config"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/queue"
	"gitlab.com/jacxb/bots/bxt/go/internal/version"
)

func main() {
	if err := run(); err != nil {
		slog.Error("api exited with error", "err", err)
		os.Exit(1)
	}
	slog.Info("api exited cleanly")
}

func run() error {
	configPath := flag.String("config", "config.yaml", "path to YAML config file (optional; env vars still apply)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Version)
		return nil
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	slog.Info("api starting", "version", version.Version)

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("config load: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(cfg.DB)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer db.Close()

	rdb, err := queue.NewRedis(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	defer rdb.Close()

	srv, err := api.New(cfg, db, rdb)
	if err != nil {
		return fmt.Errorf("api init: %w", err)
	}

	httpServer := &http.Server{
		Addr:              cfg.API.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	errc := make(chan error, 1)
	go func() {
		slog.Info("api listening", "addr", cfg.API.Listen, "web_url", cfg.API.WebURL, "oauth_redirect", srv.RedirectURI())
		errc <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("listen: %w", err)
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdownCtx)
}
