# discordbot

Basically completely vibe coded Discord bot. Originally DiscordJS, now ported to Go ([discordgo](https://github.com/bwmarrin/discordgo)) because lifes too short to agonize over NodeJS.

## File Structure

`go/` - contains the source code for the bot. This gets compiled into a single static binary (`bxt`) and shipped in the Docker image.

- `go/cmd/bxt/` - entrypoint. Loads config, opens the DB, runs migrations, registers every feature, runs the bot.
- `go/internal/config/` - config loading (`config.yaml` + `BXT_*` env overrides)
- `go/internal/database/` - MariaDB pool and embedded SQL migrations (`migrations/*.sql`)
- `go/internal/discord/` - the shared `Bot` (session lifecycle, slash command registration, interaction dispatcher)

Code is organised by function. Each function lives in `go/internal/discord/<function_name>/` and contains:

- `<function_name>.go` - `Register(bot)`, which wires the function's commands/events/components into the bot
- `commands/<command>.go` - slash command definitions and handlers
- `events/<event>.go` - gateway event handlers
- `components/<component>.go` - button / select / modal handlers

For example, auto voice channel code lives in:
- `go/internal/discord/avc/commands/watch.go` → `/avc watch`, `/avc unwatch`
- `go/internal/discord/avc/events/userVoiceJoin.go` → handles `VoiceStateUpdate` when a user joins a watched channel

## Functions

I've had to cross over the 'other bot' to here. Not all functionality is there. Not all of it pertains to this bots 'primary use'.

MRs are permitted.

- **Auto Voice Channel (avc)** - `/avc watch|unwatch`. Joining a watched voice channel creates a personal voice channel for the user and moves them into it; deleted once empty.
- **Login Logger (loginLogger)** - `/jll`. Posts member join/leave messages to configured channels. Admin join messages include the invite code used and the inviter.
- **Permission Sync (permissionsync)** - `/copypermissions`. Overwrites a destination channel's permissions to match a source channel.
- **Tickets (tickets)** - `/ticket setup`. Modal-based support tickets with categories, per-guild ticket numbering, and a close button that archives the ticket.
