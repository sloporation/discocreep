# discordbot

Basically completely vibe coded Discord bot. Originally DiscordJS, now ported to Go ([disgo](https://github.com/disgoorg/disgo)) because lifes too short to agonize over NodeJS.

## Setup

Most of the setup happens in the [Discord Developer Portal](https://discord.com/developers/applications), and it's easy to miss a step. If something doesn't work, check [Troubleshooting](#troubleshooting) first.

### 1. Create the Discord application

1. In the Developer Portal, click **New Application** and give it a name.
2. **Bot** page:
   - **Reset Token** and copy it. This is `BXT_DISCORD_TOKEN`.
   - Under **Privileged Gateway Intents**, turn on **Server Members Intent** and **Message Content Intent**, then click **Save Changes** (the bar at the bottom of the page). The bot won't connect without them.
3. **OAuth2** page:
   - Copy the **Client ID**. This is `BXT_DISCORD_CLIENT_ID`.
   - **Reset Secret** and copy the **Client Secret**. This is `BXT_DISCORD_CLIENT_SECRET`, used for logging in to the web app. Keep it secret.
   - Under **Redirects**, add the API's callback URL **exactly** and save:
     ```
     http://localhost:8080/auth/callback
     ```
     It's `BXT_API_PUBLIC_URL` + `/auth/callback`. It is **not** the web app's URL (`:5173`). Discord only redirects to URLs registered here, compared character for character.

### 1b. Battle.net (optional, for World of Warcraft)

Skip this if you don't need WoW characters.

1. Sign in at [develop.battle.net](https://develop.battle.net/access/clients) and **Create Client**.
2. Add the redirect URL `http://localhost:8080/auth/battlenet/callback` (i.e. `BXT_API_PUBLIC_URL` + `/auth/battlenet/callback`). Blizzard may insist on `https`; if it won't accept `http://localhost`, you'll need an `https` API URL even for testing.
3. Copy the **Client ID** and **Client Secret** into `BXT_BATTLENET_CLIENT_ID` and `BXT_BATTLENET_CLIENT_SECRET`.

Members then link Battle.net under **Account → Linked accounts** and pick a main per game version on each server's page. Retail, Classic (Progression) and Classic Era (including Anniversary and Season of Discovery) are supported; set `BXT_BATTLENET_FLAVOURS` to limit them. Blizzard's Classic Era guild roster API has returned errors since late 2024, so Era role sync may not work until Blizzard fixes it (character linking is unaffected).

For **WoW guild sync** (nicknames and roles from the guild roster), the bot also needs **Manage Nicknames** and **Manage Roles**, and its role must sit above every role you map to a guild rank. Admins set it up on the server's page in the web app.

### 2. Invite the bot to your server

Open this URL, with your Client ID filled in, and pick your server:

```
https://discord.com/oauth2/authorize?client_id=YOUR_CLIENT_ID&scope=bot+applications.commands&permissions=8
```

- `applications.commands` is required, or slash commands can't be registered (`50001: Missing Access`).
- `permissions=8` is Administrator, the simplest option. Without it, the bot needs at least: Manage Server (invite tracking), Manage Roles, Manage Channels, Move Members, Manage Messages, View Channels, Send Messages, Embed Links, Read Message History. Its role must also sit **above** any role it hands out.

### 3. Configure

```bash
cp .env.example .env
```

Fill in `.env`:

| Setting | What to put |
|---|---|
| `BXT_DISCORD_TOKEN` | Bot token (step 1) |
| `BXT_DISCORD_CLIENT_ID` | Client ID (step 1) |
| `BXT_DISCORD_CLIENT_SECRET` | Client secret (step 1) |
| `BXT_DISCORD_GUILD_ID` | Optional. Your server's ID (Developer Mode on → right-click the server → Copy Server ID). Commands then appear instantly in that server only; leave it empty to register them globally, which can take up to an hour. |
| `BXT_DB_PASSWORD`, `BXT_DB_ROOT_PASSWORD` | Any strong passwords |
| `BXT_BATTLENET_CLIENT_ID`, `BXT_BATTLENET_CLIENT_SECRET` | Optional. From step 1b; leave empty to turn off WoW linking. |
| `BXT_STEAM_API_KEY` | Optional. A [Steam Web API key](https://steamcommunity.com/dev/apikey), so the dashboard shows linked Steam accounts' names and avatars. Steam linking works without it. |

The rest of `.env.example` works as-is for running everything locally.

### 4. Start it

```bash
docker compose up -d --build
docker compose logs -f watcher worker api
```

You should see `watcher connected`, `commands synced` from the worker, and `api listening`. Then:

- **Web app:** http://localhost:5173 → **Log in with Discord**. Use `localhost`, not `127.0.0.1`. Members link their Steam account under **Account → Linked accounts** (Steam's own sign-in; nothing to set up in the Developer Portal).
- **In Discord:** run `/adminalerts set` to choose a private channel where the bot reports problems, then set up the features you want (below).

### Running somewhere other than localhost

- Set `BXT_API_PUBLIC_URL` and `BXT_API_WEB_URL` to your real `https://` URLs, and add the new `https://…/auth/callback` under **Redirects** in the Developer Portal.
- Host the web app and API on the same domain (e.g. `app.example.com` and `api.example.com`). Login cookies don't work across different domains.

### Troubleshooting

| Symptom | Fix |
|---|---|
| Watcher: `close 4014: Disallowed intent(s)` | Turn on **Server Members Intent** and **Message Content Intent** on the Bot page, and click **Save Changes**. |
| Worker: `sync commands: 50001: Missing Access` | The bot was invited without `applications.commands`, or `BXT_DISCORD_GUILD_ID` is wrong. Re-invite with the URL in step 2 (no need to kick it first). |
| Slash commands don't show up | With `BXT_DISCORD_GUILD_ID` empty they're global and take up to an hour. Check the worker logged `commands synced`. |
| API: `BXT_DISCORD_CLIENT_SECRET … is not set` | Add the client secret to `.env`, then `docker compose up -d api`. |
| Discord: `Invalid OAuth2 redirect_uri` | Add exactly `http://localhost:8080/auth/callback` (or your `BXT_API_PUBLIC_URL` + `/auth/callback`) under OAuth2 → **Redirects**. The API logs the URL it uses as `oauth_redirect=` at startup. |
| Web app: "Couldn't reach the API" | The `api` container isn't running on port 8080; check `docker compose logs api`. |
| Web app: "That login link expired…" | Open the app at `http://localhost:5173`, not `127.0.0.1`. |
| Bot can't give out a role | Move the bot's role above that role in Server Settings → Roles. |
| Battle.net: redirect URL error on Blizzard's page | The client's redirect URL must be exactly `BXT_API_PUBLIC_URL` + `/auth/battlenet/callback`. |
| No World of Warcraft section on Linked accounts | `BXT_BATTLENET_CLIENT_ID` / `BXT_BATTLENET_CLIENT_SECRET` aren't set on the API. |

## File Structure

`go/` - the Go source. It builds three binaries, all shipped in one Docker image:

- `go/cmd/watcher/` - holds the Discord gateway connection and passes every event to Valkey (Redis). Run exactly one.
- `go/cmd/worker/` - runs the features: opens the DB, runs migrations, registers every feature, processes events from Valkey and replies to Discord. Run as many as you like (`docker compose up -d --scale worker=3`).
- `go/cmd/api/` - the web API behind the web app (Discord login).
- `go/internal/config/` - config loading (`config.yaml` + `BXT_*` env overrides)
- `go/internal/database/` - MariaDB pool and embedded SQL migrations (`migrations/*.sql`)
- `go/internal/discord/` - the shared `Bot` the features register on (command sync, interaction router)
- `go/internal/queue/`, `go/internal/watcher/`, `go/internal/locks/` - the watcher → worker plumbing
- `go/internal/api/` - the web API

`web/` - the React web app. A static site that runs in the browser and talks to the API; it has its own Docker image.

Code is organised by function. Each function lives in `go/internal/discord/<function_name>/` and contains:

- `<function_name>.go` - `Register(bot)`, which wires the function's commands/events/components into the bot
- `commands/<command>.go` - slash command definitions and handlers
- `events/<event>.go` - gateway event handlers
- `components/<component>.go` - button / select / modal handlers
- `shared/` - anything more than one of the above needs (button IDs, messages)

For example, auto voice channel code lives in:
- `go/internal/discord/avc/commands/watch.go` → `/avc watch`, `/avc unwatch`
- `go/internal/discord/avc/events/userVoiceJoin.go` → handles a user joining a watched voice channel

## Functions

I've had to cross over the 'other bot' to here. Not all functionality is there. Not all of it pertains to this bots 'primary use'.

MRs are permitted.

- **Admin Alerts (adminAlerts)** - `/adminalerts set|clear`. Picks a channel where the bot reports problems an admin needs to fix (missing permissions, deleted channels, etc.).
- **Audit (audit)** - Always on. Logs every message, reaction, join and leave to the database, and catches up on joins/leaves missed while the bot was offline.
- **Auto Voice Channel (avc)** - `/avc watch|unwatch`. Joining a watched voice channel creates a personal voice channel for the user and moves them into it; deleted once empty. The owner gets buttons to hide, unhide and rename it.
- **Community Endorsement (communityEndorsement)** - `/endorsement setup|disable`. New joiners wait until an existing member presses Sponsor, which gives them the member role. Posts to a text channel or a forum. You set up the permissions; the bot only hands out the role.
- **Invite Tracker (inviteTracker)** - `/whoinvited`. Records which invite each member joined with and who created it.
- **Login Logger (loginLogger)** - `/jll`. Posts member join/leave messages to configured channels. Admin join messages include the invite code used and the inviter.
- **Message Purge (messagePurge)** - `/purge settings|user`. Deletes a user's messages from Discord when they leave and/or on an admin's request; each is off until enabled. The audit log keeps its copy.
- **Permission Sync (permissionsync)** - `/copypermissions`. Overwrites a destination channel's permissions to match a source channel.
- **WoW Guild Sync (wowSync)** - Web app only. Admins link one or more WoW guilds (retail, Classic or Classic Era; several of one version is fine, e.g. a community split across guilds by the 1,000-member cap) and map each guild's ranks to existing Discord roles; members pick a main per version. Every 15 minutes the bot sets members' rank roles (a role is kept while any linked guild grants it; auto-removal can be turned off) and, optionally, their nickname (one version's main, or combined "Retail / Classic"). It only ever adds or removes mapped roles, never creates roles, and changes nothing for a guild Blizzard can't be reached for. Every role it gives or takes is logged; if you take a role off a rank (or a member clears their main), the bot takes it back from the members it gave it to, but never from anyone who got it another way.
- **Tickets (tickets)** - `/ticket setup`. Modal-based support tickets with categories, per-guild ticket numbering, and a close button that archives the ticket.
