// Command worker runs the bot's features against gateway events handed over
// by the watcher through Redis/Valkey. Run as many as you like: they share
// the event partitions between them and reply to Discord directly.
//
// Lifecycle:
//  1. Load config (yaml file + BXT_* env overrides).
//  2. Open the database pool and apply embedded migrations.
//  3. Connect to Redis/Valkey.
//  4. Build the discord.Bot, register every feature on it.
//  5. Process events until SIGINT/SIGTERM, then release partitions and exit.
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
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/adminAlerts"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/audit"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/avc"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/communityEndorsement"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/inviteTracker"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/loginLogger"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/messagePurge"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/permissionsync"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/tickets"
	"gitlab.com/jacxb/bots/bxt/go/internal/queue"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker exited with error", "err", err)
		os.Exit(1)
	}
	slog.Info("worker exited cleanly")
}

// run is the real entrypoint. It returns errors instead of calling os.Exit so
// deferred cleanup (DB pool close, etc.) actually runs.
func run() error {
	configPath := flag.String("config", "config.yaml", "path to YAML config file (optional; env vars still apply)")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("config load: %w", err)
	}

	// Cancel ctx on SIGINT/SIGTERM so Bot.Run unblocks and shuts down cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(cfg.DB)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer db.Close()
	slog.Info("db connected", "host", cfg.DB.Host, "name", cfg.DB.Name)

	// Safe with several workers starting at once: golang-migrate takes a
	// database lock while migrating.
	if err := db.Migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	slog.Info("migrations applied")

	rdb, err := queue.NewRedis(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("redis: %w", err)
	}
	defer rdb.Close()
	slog.Info("redis connected", "addr", cfg.Redis.Addr)

	bot, err := discord.New(cfg, db, rdb)
	if err != nil {
		return fmt.Errorf("discord init: %w", err)
	}

	// Feature packages register their commands and event handlers here.
	adminAlerts.Register(bot)
	avc.Register(bot)
	loginLogger.Register(bot)
	inviteTracker.Register(bot)
	permissionsync.Register(bot)
	tickets.Register(bot)
	communityEndorsement.Register(bot)
	audit.Register(bot)
	messagePurge.Register(bot)

	if err := bot.Run(ctx); err != nil {
		return fmt.Errorf("bot run: %w", err)
	}
	return nil
}
