# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A Discord bot written in Go using [disgo](https://github.com/disgoorg/disgo), backed by MariaDB. It was ported from an earlier discord.js bot. Features are self-contained packages that register their commands, events and components on a shared `discord.Bot`.

## Project Structure

```
go/                                   # Go module (gitlab.com/jacxb/bots/bxt/go)
├── cmd/bxt/bxt.go                    # Entrypoint: config → DB → migrate → register features → run
├── config.example.yaml               # Example config; copy to config.yaml
├── internal/
│   ├── alerts/alerts.go              # Admin alerts: bot.Alerts posts to each guild's alerts channel
│   ├── config/config.go              # koanf loader: defaults < config.yaml < BXT_* env
│   ├── database/
│   │   ├── database.go               # *sql.DB pool + embedded migration runner
│   │   └── migrations/               # NNN_name.up.sql / NNN_name.down.sql (embedded)
│   └── discord/
│       ├── discord.go                # Bot type, command sync, interaction router, Run
│       ├── adminAlerts/              # /adminalerts set|clear — picks the admin alerts channel
│       │   ├── adminAlerts.go
│       │   └── commands/adminAlerts.go
│       ├── audit/                    # Logs members, joins/leaves, messages, reactions
│       │   ├── audit.go
│       │   └── events/               # members.go, messages.go, reactions.go, sync.go (reconcile on connect), heartbeat.go, store.go
│       ├── avc/                      # Auto voice channels + owner control panel
│       │   ├── avc.go                # Register(bot)
│       │   ├── commands/watch.go     # /avc watch|unwatch
│       │   ├── components/           # button.go (hide/unhide/rename), modal.go (rename), helpers.go
│       │   ├── events/               # userVoiceJoin.go, userVoiceLeave.go, helpers.go
│       │   └── shared/shared.go      # Control panel component IDs + message
│       ├── communityEndorsement/     # New joiners need a sponsor to get the member role
│       │   ├── communityEndorsement.go
│       │   ├── commands/             # endorsement.go (/endorsement setup|disable), autocomplete.go (forum tag)
│       │   ├── components/button.go  # Sponsor button
│       │   ├── events/               # memberJoin.go (post request), memberLeave.go (close request)
│       │   └── shared/               # shared.go (button route, messages), channels.go (channel/tag lookup)
│       ├── inviteTracker/            # Records the invite each member joined with; /whoinvited
│       │   ├── inviteTracker.go
│       │   ├── commands/whoInvited.go
│       │   ├── events/               # ready.go (seed cache), memberJoin.go (diff + record)
│       │   └── shared/               # inviteCache.go, invites.go
│       ├── loginLogger/              # Join/leave notifications (admin message includes invite)
│       │   ├── loginLogger.go
│       │   ├── commands/configureChannel.go   # /jll
│       │   ├── events/               # ready.go, userJoin.go, userLeave.go
│       │   └── shared/inviteCache.go
│       ├── permissionsync/           # /copypermissions
│       │   ├── permissionsync.go
│       │   └── commands/sync.go
│       └── tickets/                  # Ticket system
│           ├── tickets.go
│           ├── commands/setup.go     # /ticket setup
│           ├── components/           # button.go (create/close), modal.go (create)
│           └── shared/shared.go      # Component IDs, ticket categories
docker/
└── Dockerfile                        # Multi-arch build → distroless static image
docker-compose.yml                    # bot + mariadb
```

## Adding a Feature

Every feature follows the same layout. Only create the subpackages the feature needs:

```
go/internal/discord/<feature>/
├── <feature>.go      # package <feature>: Register(bot) — wiring only, no handler logic
├── commands/         # slash command definitions + their handlers
├── events/           # gateway event listeners
├── components/       # button / select / modal handlers (button.go, modal.go, helpers.go)
└── shared/           # leaf package: component IDs, message builders, types used by >1 subpackage
```

Put handlers in the matching subpackage (never in `<feature>.go`), then call `<feature>.Register(bot)` in `go/cmd/bxt/bxt.go` before `bot.Run`.

```go
package myfeature

import (
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/myfeature/commands"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/myfeature/events"
)

func Register(bot *discord.Bot) {
	bot.AddListener(events.HandleSomething(bot.DB))
	bot.AddCommand(commands.MyCommand())
	bot.Router.SlashCommand("/mycommand", commands.HandleMy(bot.DB))
}
```

Import rules:
- Subpackages must not import their parent feature package (e.g. `tickets/commands` importing `tickets`). The parent imports them, so that is an import cycle.
- `commands/`, `events/` and `components/` must not import each other. Anything two of them need, such as a custom_id or a message with buttons that an event posts and a component handles, goes in `<feature>/shared`. `shared/` imports no other package from the feature.
- Only the parent `<feature>.go` imports `internal/discord`.

## Adding Commands

A command is a `discord.ApplicationCommandCreate` definition queued with `bot.AddCommand`, plus a handler registered on `bot.Router` (a disgo `handler.Mux`) by path: `/<command>` or `/<command>/<subcommand>`. A handler registered on `/<command>` also matches all its subcommands.

```go
func MyCommand() discord.ApplicationCommandCreate {
	return discord.SlashCommandCreate{
		Name:                     "mycommand",
		Description:              "Command description",
		DefaultMemberPermissions: omit.NewPtr(discord.PermissionManageChannels),
		Contexts:                 []discord.InteractionContextType{discord.InteractionContextTypeGuild},
	}
}

func HandleMy(db *database.DB) handler.SlashCommandHandler {
	return func(data discord.SlashCommandInteractionData, e *handler.CommandEvent) error {
		// Command logic; use e.Ctx for DB calls
		return e.CreateMessage(discord.MessageCreate{Content: "done", Flags: discord.MessageFlagEphemeral})
	}
}
```

Commands are bulk-overwritten with Discord in `Bot.Run` before the gateway opens; there is no separate deploy step. If `discord.guild_id` is set they are registered to that guild (instant); otherwise they are registered globally (can take up to an hour to propagate). Commands no longer defined are removed. The bot must have been invited with the `applications.commands` scope.

## Adding Events

Event listeners are `bot.EventListener`s registered with `bot.AddListener`; build one from any disgo event type with `bot.NewListenerFunc`:

```go
func HandleSomething(db *database.DB) bot.EventListener {
	return bot.NewListenerFunc(func(e *events.GuildVoiceStateUpdate) {
		// Event logic; e.Client() gives Rest and Caches
	})
}
```

Events run asynchronously, each in its own goroutine. Gateway intents and caches are set in `discord.New` (`go/internal/discord/discord.go`): intents Guilds, GuildVoiceStates, GuildMembers, GuildMessages, GuildMessageReactions, MessageContent. GuildMembers and MessageContent are privileged and must be enabled in the Developer Portal (Server Members Intent, Message Content Intent), otherwise the gateway closes with `4014: Disallowed intent(s)`. Add intents/caches there if a new event needs them.

## Adding Components

Buttons, selects and modals are routed by `custom_id` on the same `bot.Router` (`ButtonComponent`, `SelectMenuComponent`, `Modal`). The router only dispatches custom_ids that start with `/`, so use a feature-prefixed path (e.g. `/ticket/close`) and define it as a constant in `<feature>/shared`, because whatever posts the component and the handler that receives it usually live in different subpackages. Paths can carry variables, e.g. register `/endorse/sponsor/{id}` and read `e.Vars["id"]` in the handler; use this to tie a button to a DB row rather than looking it up by message. Field IDs inside a modal are not routed and can be anything. Select menus and text inputs in a modal go inside a `discord.NewLabel(...)`, not an action row.

## Admin Alerts

When something fails that only an admin can fix (a post can't be made, a configured channel/tag/role is gone, a permission is missing), send an alert as well as logging it. Admins pick the channel per guild with `/adminalerts set`; it is stored in `guilds.admin_alert_channel_id`.

`bot.Alerts` (`*alerts.Alerter`, package `internal/alerts`) is available to every feature. Pass it into handler constructors like `bot.DB`:

```go
alerter.Send(client, guildID, alerts.Alert{
	Feature:     shared.Feature, // e.g. "communityEndorsement"
	Title:       "Couldn't post a sponsorship request",
	Description: "What happened, and what the admin should do about it.",
	Fields:      []discord.EmbedField{alerts.ErrorField(err)},
})
```

Each feature that raises alerts defines `const Feature = "<feature>"` in its `shared` package for the footer. `Send` is best effort and never returns an error: it always logs at Warn, and silently skips posting if the guild has no alerts channel. Alerts aren't deduplicated, so only alert once per failed action (not in a retry loop). Don't alert for things the user who triggered the action can fix themselves; tell them in an ephemeral reply instead. When a user-triggered action fails for a reason only an admin can fix, do both, and tell the user the admins have been alerted.

## Configuration

Config is loaded from `config.yaml` (path set with `-config`, optional) and overridden by `BXT_*` environment variables. Env mapping: strip `BXT_`, lowercase, replace the first `_` with `.` (`BXT_DB_POOL_SIZE` → `db.pool_size`).

- `BXT_DISCORD_TOKEN` - Bot token from Discord Developer Portal
- `BXT_DISCORD_CLIENT_ID` - Application ID from Discord Developer Portal
- `BXT_DISCORD_GUILD_ID` - Development server ID (optional, for instant per-guild command registration)
- `BXT_DB_HOST` - MariaDB host (use `mariadb` for Docker, `localhost` for local; default `localhost`)
- `BXT_DB_PORT` - MariaDB port (default: 3306)
- `BXT_DB_USER` - Database user (default: `discordbot`)
- `BXT_DB_PASSWORD` - Database password
- `BXT_DB_NAME` - Database name (default: `discordbot`)
- `BXT_DB_POOL_SIZE` - Connection pool size (default: 5)
- `BXT_DB_ROOT_PASSWORD` - Only used by docker-compose to initialise the MariaDB container

For Docker, `cp .env.example .env` and fill it in (read by `docker-compose.yml`).

## Database Usage

`*database.DB` embeds `*sql.DB`, so use the standard `database/sql` API. It is available as `bot.DB`; pass it into handler constructors.

```go
var count int
err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM avc_monitors WHERE channel_id = ?", channelID).Scan(&count)

res, err := db.ExecContext(ctx, "INSERT INTO avc_monitors (channel_id, guild_id) VALUES (?, ?)", channelID, guildID)
n, _ := res.RowsAffected()
```

## Migrations

Migrations are plain SQL files in `go/internal/database/migrations/`, embedded into the binary and applied automatically at startup by [golang-migrate](https://github.com/golang-migrate/migrate).

Create a pair of files with the next number:

```
go/internal/database/migrations/011_create_users.up.sql
go/internal/database/migrations/011_create_users.down.sql
```

```sql
-- 011_create_users.up.sql
CREATE TABLE users (
    id BIGINT UNSIGNED PRIMARY KEY,
    name VARCHAR(100) NOT NULL
);
```

```sql
-- 011_create_users.down.sql
DROP TABLE IF EXISTS users;
```

Multiple statements per file are allowed. There is no down/status CLI; rollbacks must be done manually or with the `migrate` CLI.

## Commands

### Local Development

```bash
cd go
cp config.example.yaml config.yaml   # fill in token + DB creds
go run ./cmd/bxt                     # migrates, registers commands, runs
go build -o bin/bxt ./cmd/bxt
go vet ./...
```

### Docker

```bash
docker compose build
docker compose up -d                  # Start MariaDB + bot (migrations run on startup)
docker compose logs -f bot            # View logs
docker compose up -d --build          # Rebuild and restart
```

CI (`.gitlab-ci.yml`) builds and pushes a multi-arch (amd64/arm64) image: `latest` on `main`, the short SHA on other branches, and the tag name on tags.

## Next Steps

1. `cp .env.example .env` and fill in the `BXT_*` Discord + database settings above
2. Enable the Server Members Intent and Message Content Intent for the bot in the Discord Developer Portal
3. `docker compose up -d --build`
