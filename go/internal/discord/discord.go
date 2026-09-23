// Package discord owns the bot's connection to Discord.
//
// It exposes a Bot type that wraps a *discordgo.Session along with shared
// dependencies (database, config) and provides a small registration API that
// feature packages call from their own Register() functions. The split lets us
// keep cross-cutting concerns (session lifecycle, slash-command registration,
// the central interactionCreate dispatcher) in one place while every feature
// (avc, loginLogger, ...) stays self-contained in its own subpackage.
//
// Typical wiring from cmd/bxt:
//
//	bot, _ := discord.New(cfg, db)
//	avc.Register(bot)
//	loginLogger.Register(bot)
//	bot.Run(ctx)
package discord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/bwmarrin/discordgo"

	"gitlab.com/jacxb/bots/bxt/go/internal/config"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// CommandHandler is the function signature every slash-command handler implements.
type CommandHandler func(s *discordgo.Session, i *discordgo.InteractionCreate)

// ComponentHandler handles button clicks, select-menu choices, and modal submits.
// Discord routes all of these through interactionCreate; we dispatch on the
// component's custom_id.
type ComponentHandler func(s *discordgo.Session, i *discordgo.InteractionCreate)

// Bot is the runtime container shared by every feature package.
//
// Session, DB, and Cfg are public on purpose: feature handlers will need them.
// The registration maps are private — features add to them via AddCommand /
// AddComponent rather than mutating them directly.
type Bot struct {
	Session *discordgo.Session
	DB      *database.DB
	Cfg     config.Config

	commands          []*discordgo.ApplicationCommand
	commandHandlers   map[string]CommandHandler
	componentHandlers map[string]ComponentHandler
}

// New constructs a Bot with the session pre-configured (intents set, token
// applied) but not yet opened. Call Run to actually connect.
func New(cfg config.Config, db *database.DB) (*Bot, error) {
	if cfg.Discord.Token == "" {
		return nil, errors.New("discord token is empty")
	}

	s, err := discordgo.New("Bot " + cfg.Discord.Token)
	if err != nil {
		return nil, fmt.Errorf("discordgo.New: %w", err)
	}

	// Intents: guild metadata + voice state updates + guild members.
	//
	// GuildMembers is a privileged intent — Discord requires "Server Members Intent"
	// to be enabled in the Developer Portal. The other intents are not privileged.
	s.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildVoiceStates | discordgo.IntentsGuildMembers

	return &Bot{
		Session:           s,
		DB:                db,
		Cfg:               cfg,
		commandHandlers:   make(map[string]CommandHandler),
		componentHandlers: make(map[string]ComponentHandler),
	}, nil
}

// AddCommand registers a slash command and its handler. The handler is
// dispatched whenever Discord delivers an ApplicationCommand interaction with
// a matching name. Call this from a feature's Register() before Run().
func (b *Bot) AddCommand(cmd *discordgo.ApplicationCommand, h CommandHandler) {
	b.commands = append(b.commands, cmd)
	b.commandHandlers[cmd.Name] = h
}

// AddComponent registers a handler for a button / select / modal custom_id.
// Feature packages that emit interactive components should use a stable prefix
// (e.g. "avc_lock") and register the exact custom_id here.
func (b *Bot) AddComponent(customID string, h ComponentHandler) {
	b.componentHandlers[customID] = h
}

// AddHandler is an escape hatch for any event that isn't a slash command or
// component interaction — voiceStateUpdate, guildMemberAdd, channelCreate, etc.
// The signature matches discordgo: any func(*Session, *Event) the library
// recognises is accepted.
func (b *Bot) AddHandler(handler any) func() {
	return b.Session.AddHandler(handler)
}

// Run opens the session, registers slash commands once the gateway Ready event
// fires (so State.User.ID is populated), and blocks until ctx is cancelled. On
// return it closes the session and, if a dev guild_id was configured, deletes
// the per-guild commands so reloads don't accumulate duplicates.
func (b *Bot) Run(ctx context.Context) error {
	// Central interactionCreate dispatcher. Must be registered before Open()
	// so we don't miss the first interaction after connect.
	b.Session.AddHandler(b.dispatchInteraction)

	// Slash commands can only be registered once we know our application ID,
	// which arrives in the Ready payload. Using sync.Once guards against
	// gateway reconnects firing Ready more than once over the bot's lifetime.
	var (
		registerOnce sync.Once
		registered   []*discordgo.ApplicationCommand
		ready        = make(chan error, 1)
	)
	b.Session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		registerOnce.Do(func() {
			slog.Info("discord connected", "user", r.User.String(), "guilds", len(r.Guilds))
			cmds, err := b.registerCommands(r.User.ID)
			registered = cmds
			ready <- err
		})
	})

	if err := b.Session.Open(); err != nil {
		return fmt.Errorf("session.Open: %w", err)
	}

	// Wait for the Ready handler to either finish registration or fail. If the
	// user Ctrl-Cs before Ready arrives we still want to close cleanly.
	select {
	case err := <-ready:
		if err != nil {
			_ = b.Session.Close()
			return fmt.Errorf("register commands: %w", err)
		}
		slog.Info("commands registered", "count", len(registered), "guild", b.Cfg.Discord.GuildID)
	case <-ctx.Done():
		return b.Session.Close()
	}

	<-ctx.Done()
	slog.Info("shutting down discord session")

	// Best-effort cleanup of dev-guild commands so a restart doesn't accumulate
	// duplicates. We don't delete global commands — they survive restarts on
	// purpose. Errors here are logged but don't fail shutdown.
	if b.Cfg.Discord.GuildID != "" && b.Session.State != nil && b.Session.State.User != nil {
		appID := b.Session.State.User.ID
		for _, cmd := range registered {
			if err := b.Session.ApplicationCommandDelete(appID, b.Cfg.Discord.GuildID, cmd.ID); err != nil {
				slog.Warn("cleanup command failed", "name", cmd.Name, "err", err)
			}
		}
	}

	return b.Session.Close()
}

// registerCommands uploads the accumulated slash-command definitions to Discord.
// If Cfg.Discord.GuildID is set we register per-guild (instant); otherwise
// commands go up globally (which can take up to an hour to propagate).
func (b *Bot) registerCommands(appID string) ([]*discordgo.ApplicationCommand, error) {
	if len(b.commands) == 0 {
		return nil, nil
	}
	out := make([]*discordgo.ApplicationCommand, 0, len(b.commands))
	for _, cmd := range b.commands {
		created, err := b.Session.ApplicationCommandCreate(appID, b.Cfg.Discord.GuildID, cmd)
		if err != nil {
			return out, fmt.Errorf("create %q: %w", cmd.Name, err)
		}
		out = append(out, created)
	}
	return out, nil
}

// dispatchInteraction is the single interactionCreate listener. It routes by
// interaction type:
//   - ApplicationCommand -> commandHandlers[Data.Name]
//   - MessageComponent / ModalSubmit -> componentHandlers[CustomID]
//
// Unknown commands or custom_ids are logged but not fatal — handy when an old
// component left over from a previous deploy fires after a rename.
func (b *Bot) dispatchInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand,
		discordgo.InteractionApplicationCommandAutocomplete:
		name := i.ApplicationCommandData().Name
		h, ok := b.commandHandlers[name]
		if !ok {
			slog.Warn("unknown command", "name", name)
			return
		}
		h(s, i)
	case discordgo.InteractionMessageComponent:
		id := i.MessageComponentData().CustomID
		if h, ok := b.componentHandlers[id]; ok {
			h(s, i)
			return
		}
		slog.Warn("unknown component", "custom_id", id)
	case discordgo.InteractionModalSubmit:
		id := i.ModalSubmitData().CustomID
		if h, ok := b.componentHandlers[id]; ok {
			h(s, i)
			return
		}
		slog.Warn("unknown modal", "custom_id", id)
	}
}
