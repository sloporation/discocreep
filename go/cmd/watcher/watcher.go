// Command watcher holds the bot's Discord gateway connection and publishes
// every event to Redis/Valkey for workers to process. It runs no features
// and doesn't use the database. Run one per gateway session.
//
// Lifecycle:
//  1. Load config (yaml file + BXT_* env overrides).
//  2. Connect to Redis/Valkey and create the event streams.
//  3. Connect to the gateway and forward events until SIGINT/SIGTERM.
//
// The path to the config file can be set with -config (default: ./config.yaml).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"gitlab.com/jacxb/bots/bxt/go/internal/config"
	"gitlab.com/jacxb/bots/bxt/go/internal/queue"
	"gitlab.com/jacxb/bots/bxt/go/internal/version"
	"gitlab.com/jacxb/bots/bxt/go/internal/watcher"
)

func main() {
	if err := run(); err != nil {
		slog.Error("watcher exited with error", "err", err)
		os.Exit(1)
	}
	slog.Info("watcher exited cleanly")
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
	slog.Info("watcher starting", "version", version.Version)

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
	slog.Info("redis connected", "addr", cfg.Redis.Addr)

	publisher, err := queue.NewPublisher(ctx, rdb, cfg.Queue.Partitions)
	if err != nil {
		return fmt.Errorf("queue: %w", err)
	}

	w, err := watcher.New(cfg, publisher)
	if err != nil {
		return fmt.Errorf("watcher init: %w", err)
	}
	if err := w.Run(ctx); err != nil {
		return fmt.Errorf("watcher run: %w", err)
	}
	return nil
}
