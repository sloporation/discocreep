// Command api serves the web API used by the web app (web/): Discord login
// now, guild configuration later. It's stateless apart from Redis/Valkey
// (sessions, login state), so several can run behind a load balancer.
//
// Lifecycle:
//  1. Load config (yaml file + BXT_* env overrides).
//  2. Connect to Redis/Valkey.
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
	"gitlab.com/jacxb/bots/bxt/go/internal/queue"
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
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("config load: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rdb, err := queue.NewRedis(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	defer rdb.Close()

	srv, err := api.New(cfg, rdb)
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
