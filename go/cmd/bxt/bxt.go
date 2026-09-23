// Command bxt is the bot entrypoint.
//
// Lifecycle:
//  1. Load config (yaml file + BXT_* env overrides).
//  2. Open the database pool and apply embedded migrations.
//  3. Initialise the discord.Bot shell, then register every feature on it.
//  4. Run until SIGINT/SIGTERM, then close session and DB cleanly.
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
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/avc"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/loginLogger"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/permissionsync"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/tickets"
)

func main() {
	if err := run(); err != nil {
		slog.Error("bxt exited with error", "err", err)
		os.Exit(1)
	}
	slog.Info("bxt exited cleanly")
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

	db, err := database.Open(cfg.DB)
	if err != nil {
		return fmt.Errorf("db open: %w", err)
	}
	defer db.Close()
	slog.Info("db connected", "host", cfg.DB.Host, "name", cfg.DB.Name)

	if err := db.Migrate(); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	slog.Info("migrations applied")

	bot, err := discord.New(cfg, db)
	if err != nil {
		return fmt.Errorf("discord init: %w", err)
	}

	// Feature packages register their commands and event handlers here.
	avc.Register(bot)
	loginLogger.Register(bot)
	permissionsync.Register(bot)
	tickets.Register(bot, db)

	// Cancel ctx on SIGINT/SIGTERM so Bot.Run unblocks and shuts down cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := bot.Run(ctx); err != nil {
		return fmt.Errorf("bot run: %w", err)
	}
	return nil
}
