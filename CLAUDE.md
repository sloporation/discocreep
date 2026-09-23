# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A Discord bot written in Go using [discordgo](https://github.com/bwmarrin/discordgo), backed by MariaDB. It was ported from an earlier discord.js bot. Features are self-contained packages that register their commands, events and components on a shared `discord.Bot`.

## Project Structure

```
go/                                   # Go module (gitlab.com/jacxb/bots/bxt/go)
├── cmd/bxt/bxt.go                    # Entrypoint: config → DB → migrate → register features → run
├── config.example.yaml               # Example config; copy to config.yaml
├── internal/
│   ├── config/config.go              # koanf loader: defaults < config.yaml < BXT_* env
│   ├── database/
│   │   ├── database.go               # *sql.DB pool + embedded migration runner
│   │   └── migrations/               # NNN_name.up.sql / NNN_name.down.sql (embedded)
│   └── discord/
│       ├── discord.go                # Bot type, command registration, interaction dispatcher
│       ├── avc/                      # Auto voice channels
│       │   ├── avc.go                # Register(bot)
│       │   ├── commands/watch.go
│       │   └── events/userVoiceJoin.go, userVoiceLeave.go, helpers.go
│       ├── loginLogger/              # Join/leave notifications + invite tracking
│       ├── permissionsync/           # /copypermissions
│       └── tickets/                  # Ticket system (commands + components)
docker/
└── Dockerfile                        # Multi-arch build → distroless static image
docker-compose.yml                    # bot + mariadb
```

## Adding a Feature

Create `go/internal/discord/<feature>/<feature>.go` with a `Register` function, put handlers in `commands/`, `events/` and `components/` subpackages, then call `<feature>.Register(bot)` in `go/cmd/bxt/bxt.go` before `bot.Run`.

```go
package myfeature

import (
	"gitlab.com/jacxb/bots/bxt/go/internal/discord"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/myfeature/commands"
	"gitlab.com/jacxb/bots/bxt/go/internal/discord/myfeature/events"
)

func Register(bot *discord.Bot) {
	bot.AddHandler(events.HandleSomething(bot.DB))
	bot.AddCommand(commands.MyCommand(), commands.HandleMy(bot.DB))
}
```

Subpackages must not import their parent feature package (e.g. `tickets/commands` importing `tickets`) — the parent imports them, so that is an import cycle. Put shared constants/types in the subpackage or a separate leaf package.

## Adding Commands

A command is a `*discordgo.ApplicationCommand` definition plus a handler, registered with `bot.AddCommand`:

```go
func MyCommand() *discordgo.ApplicationCommand {
	perm := int64(discordgo.PermissionManageChannels)
	return &discordgo.ApplicationCommand{
		Name:                     "mycommand",
		Description:              "Command description",
		DefaultMemberPermissions: &perm,
	}
}

func HandleMy(db *database.DB) func(*discordgo.Session, *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		// Command logic
	}
}
```

Commands are registered with Discord automatically on startup (on the gateway `Ready` event); there is no separate deploy step. If `discord.guild_id` is set they are registered to that guild (instant) and deleted on shutdown; otherwise they are registered globally (can take up to an hour to propagate).

## Adding Events

Any discordgo event handler signature works with `bot.AddHandler`:

```go
func HandleSomething(db *database.DB) func(*discordgo.Session, *discordgo.VoiceStateUpdate) {
	return func(s *discordgo.Session, e *discordgo.VoiceStateUpdate) {
		// Event logic
	}
}
```

Gateway intents are set in `discord.New` (`go/internal/discord/discord.go`): Guilds, GuildVoiceStates, GuildMembers. GuildMembers is privileged and must be enabled in the Developer Portal. Add intents there if a new event needs them.

## Adding Components

Buttons, selects and modals are dispatched by exact `custom_id` via `bot.AddComponent(customID, handler)`. Use a stable, feature-prefixed ID (e.g. `ticket_close`).

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

For Docker, put these in `.env` (read by `docker-compose.yml`).

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
go/internal/database/migrations/007_create_users.up.sql
go/internal/database/migrations/007_create_users.down.sql
```

```sql
-- 007_create_users.up.sql
CREATE TABLE users (
    id BIGINT UNSIGNED PRIMARY KEY,
    name VARCHAR(100) NOT NULL
);
```

```sql
-- 007_create_users.down.sql
DROP TABLE IF EXISTS users;
```

Multiple statements per file are allowed. There is no down/status CLI; rollbacks must be done manually or with the `migrate` CLI.

## Commands

### Local Development

```bash
cd go
cp config.example.yaml config.yaml   # fill in token + DB creds
go mod tidy                          # go.sum is not committed yet
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

1. Create `.env` with the `BXT_*` Discord + database settings above
2. Enable the Server Members Intent for the bot in the Discord Developer Portal
3. `docker compose up -d --build`
