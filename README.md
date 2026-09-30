# discordbot

Basically completely vibe coded Discord bot. Originally DiscordJS, now ported to Go ([disgo](https://github.com/disgoorg/disgo)) because lifes too short to agonize over NodeJS.

## Setup

There are two ways to run the bot:

- **Run a release** (below): a pinned version from [GitHub Releases](https://github.com/sloporation/discocreep/releases), with Docker Compose. Use this for a real server.
- **Development** (see [Development](#development)): build and run the code you've checked out. Its database is disposable.

Most of the setup happens in the [Discord Developer Portal](https://discord.com/developers/applications), and it's easy to miss a step. If something doesn't work, check [Troubleshooting](#troubleshooting) first.

### 1. Create the Discord application

1. In the Developer Portal, click **New Application** and give it a name.
2. **Bot** page:
   - **Reset Token** and copy it. This is `BXT_DISCORD_TOKEN`.
   - **Public Bot**: on if anyone other than you will add the bot to a server. When it's off, only the account that owns the application can invite it.
   - **Requires OAuth2 Code Grant**: **off**. When it's on, Discord only adds the bot after an extra login step this bot doesn't do, and invites fail with "That login link expired…".
   - Under **Privileged Gateway Intents**, turn on **Server Members Intent** and **Message Content Intent**.
   - Click **Save Changes** (the bar at the bottom of the page). The bot won't connect without the intents.
3. **OAuth2** page:
   - Copy the **Client ID**. This is `BXT_DISCORD_CLIENT_ID`.
   - **Reset Secret** and copy the **Client Secret**. This is `BXT_DISCORD_CLIENT_SECRET`, used for logging in to the web app. Keep it secret.
   - Under **Redirects**, add the API's callback URL **exactly** and save. It's `BXT_API_PUBLIC_URL` + `/auth/callback`:
     ```
     https://api.example.com/auth/callback
     ```
     (For development it's `http://localhost:8080/auth/callback`; you can register both.) It is **not** the web app's URL. Discord only redirects to URLs registered here, compared character for character.

### 2. Invite the bot to your server

Open this URL, with your Client ID filled in, and pick your server:

```
https://discord.com/oauth2/authorize?client_id=YOUR_CLIENT_ID&scope=bot+applications.commands&permissions=8
```

- `applications.commands` is required, or slash commands can't be registered (`50001: Missing Access`).
- `permissions=8` is Administrator, the simplest option. Without it, the bot needs at least: Manage Server (invite tracking), Manage Roles, Manage Nicknames (WoW guild sync), Manage Channels, Move Members, Manage Messages, View Channels, Send Messages, Embed Links and Read Message History. Its role must also sit **above** any role it hands out.
- If you build the link with **OAuth2 → URL Generator** instead, tick only **bot** and **applications.commands** and **don't** choose a redirect URL. The link must not contain `response_type=code` or `redirect_uri=`. Those send you to the web app's login callback, which rejects them ("That login link expired…"), and the bot isn't added.

### 3. Steam Web API key (optional)

Members can link their Steam account without this. With a key, the dashboard also shows each linked account's Steam name and avatar.

1. Sign in at [steamcommunity.com/dev/apikey](https://steamcommunity.com/dev/apikey) with a Steam account. Steam only gives keys to accounts that aren't "limited", which means they've spent at least US$5 in the Steam store.
2. **Domain Name**: enter your API's domain, e.g. `api.example.com`. Steam doesn't check it.
3. Agree to the terms and click **Register**, then copy the **Key**. This is `BXT_STEAM_API_KEY`. Keep it secret: it's tied to your Steam account.

Linking itself needs no setup: it uses Steam's own sign-in page.

### 4. Battle.net (optional, for World of Warcraft)

Skip this if you don't need WoW characters.

1. Sign in at [develop.battle.net](https://develop.battle.net/access/clients) and **Create Client**.
2. Add the redirect URL `BXT_API_PUBLIC_URL` + `/auth/battlenet/callback`, e.g. `https://api.example.com/auth/battlenet/callback`. Blizzard may insist on `https`, even for testing.
3. Copy the **Client ID** and **Client Secret** into `BXT_BATTLENET_CLIENT_ID` and `BXT_BATTLENET_CLIENT_SECRET`.

Members then link Battle.net under **Account → Linked accounts** and pick a main per game version on each server's page. Retail, Classic (Progression) and Classic Era (including Anniversary and Season of Discovery) are supported; set `BXT_BATTLENET_FLAVOURS` to limit them. Blizzard's Classic Era guild roster API has returned errors since late 2024, so Era role sync may not work until Blizzard fixes it (character linking is unaffected).

For **WoW guild sync** (nicknames and roles from the guild roster), the bot also needs **Manage Nicknames** and **Manage Roles**, and its role must sit above every role you map to a guild rank. Admins set it up on the server's page in the web app.

### 5. Run it with Docker Compose

Every release on [GitHub Releases](https://github.com/sloporation/discocreep/releases) has a `docker-compose.yml` and `.env.example` attached, with that release's version already filled in. (They're also in the repo's [`deploy/`](deploy/) folder.)

1. Download both files into a folder on your server, then:
   ```bash
   cp .env.example .env
   ```
2. Fill in `.env`:

   | Setting | What to put |
   |---|---|
   | `BXT_VERSION` | The release to run, e.g. `0.0.1`. Already set in the release's copy. |
   | `BXT_DISCORD_TOKEN`, `BXT_DISCORD_CLIENT_ID`, `BXT_DISCORD_CLIENT_SECRET` | From step 1 |
   | `BXT_DISCORD_GUILD_ID` | Optional. Your server's ID (Developer Mode on → right-click the server → Copy Server ID). Commands then appear instantly in that server only; leave it empty to register them globally, which can take up to an hour. |
   | `BXT_API_PUBLIC_URL`, `BXT_API_WEB_URL` | The **public** URLs of the API and web app, as users' browsers reach them, e.g. `https://api.example.com` and `https://app.example.com`. See below. |
   | `BXT_API_CLIENT_IP_HEADER` | The header your reverse proxy puts the visitor's IP in, for per-user rate limits: `X-Forwarded-For` (Caddy, Traefik, nginx) or `CF-Connecting-IP` (Cloudflare). Leave empty if nothing is in front of the API. |
   | `BXT_DB_PASSWORD`, `BXT_DB_ROOT_PASSWORD`, `BXT_REDIS_PASSWORD` | Long random passwords, e.g. from `openssl rand -hex 24` |
   | `BXT_STEAM_API_KEY` | Optional, from step 3 |
   | `BXT_BATTLENET_CLIENT_ID`, `BXT_BATTLENET_CLIENT_SECRET` | Optional, from step 4. Leave empty to turn off WoW linking. |

3. Start it:
   ```bash
   docker compose up -d
   docker compose logs -f watcher worker api
   ```
   You should see `watcher connected`, `commands synced` from the worker, and `api listening`.

Then, **in Discord**, run `/adminalerts set` to choose a private channel where the bot reports problems, and set up the features you want (below). In the **web app**, log in with Discord. Members link Steam and Battle.net under **Account → Linked accounts**.

#### Putting it on the internet

The web app is static files that run in the visitor's browser, and **the browser calls the API directly**. So both the web app and the API must be reachable from the internet. The bot itself (watcher, workers), MariaDB and Valkey are not, and aren't published.

- **Use HTTPS.** The compose file publishes the API and web app on `127.0.0.1` only, for a reverse proxy on the same machine to put HTTPS in front of. Set `BXT_BIND_ADDRESS=0.0.0.0` if the proxy is on another machine. Without HTTPS, login cookies aren't marked Secure and logins travel unencrypted (the API warns about this at startup).
- **Use two subdomains of one domain**, e.g. `app.example.com` and `api.example.com`. Login cookies don't work across different domains.
- **Use public URLs, not container names.** `BXT_API_PUBLIC_URL` is handed to browsers, so it must be the address they use (`https://api.example.com`). Internal names like `http://api:8080` only work between containers: browsers fail with "Couldn't reach the API" (Safari: "cannot load … due to access control checks"). The web container logs a warning if the URL looks like a container name.

With [Caddy](https://caddyserver.com), which gets HTTPS certificates automatically, the whole proxy is:

```
app.example.com {
    reverse_proxy 127.0.0.1:5173
}
api.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

#### Upgrading

Change `BXT_VERSION` in `.env` to the new release, then:

```bash
docker compose pull && docker compose up -d
```

Database migrations run automatically when the worker starts. There are no `latest` images, so nothing changes until you choose to upgrade. Scale workers with `docker compose up -d --scale worker=3`.

#### Without Docker

Each release also has `watcher`, `worker` and `api` binaries for Linux, macOS and Windows, and the web app's static files. They read the same `BXT_*` settings (or a `config.yaml`, see `config.example.yaml`), and need MariaDB and Valkey (or Redis 7+).

### Troubleshooting

| Symptom | Fix |
|---|---|
| Watcher: `close 4014: Disallowed intent(s)` | Turn on **Server Members Intent** and **Message Content Intent** on the Bot page, and click **Save Changes**. |
| Worker: `sync commands: 50001: Missing Access` | The bot was invited without `applications.commands`, or `BXT_DISCORD_GUILD_ID` is wrong. Re-invite with the URL in step 2 (no need to kick it first). |
| Slash commands don't show up | With `BXT_DISCORD_GUILD_ID` empty they're global and take up to an hour. Check the worker logged `commands synced`. |
| Inviting the bot shows "That login link expired…" and the bot isn't added | The invite link has `response_type=code` / `redirect_uri=` in it. Turn off **Requires OAuth2 Code Grant** (Bot page) and use the link from step 2, without a redirect. |
| Only you can add the bot; others get an error | Turn on **Public Bot** on the Bot page. |
| API: `BXT_DISCORD_CLIENT_SECRET … is not set` | Add the client secret to `.env`, then `docker compose up -d api`. |
| Discord: `Invalid OAuth2 redirect_uri` | Add exactly `BXT_API_PUBLIC_URL` + `/auth/callback` under OAuth2 → **Redirects**. The API logs the URL it uses as `oauth_redirect=` at startup. |
| Web app: "Couldn't reach the API", or Safari: "cannot load … due to access control checks" | `BXT_API_PUBLIC_URL` must be the API's public URL, not a container name, and the API must be running (`docker compose logs api`). `BXT_API_WEB_URL` must be exactly the address you open the web app at. |
| Web app: "That login link expired…" when logging in | Open the web app at exactly `BXT_API_WEB_URL` (for development, `localhost`, not `127.0.0.1`), and try again. |
| Web app: "Too many attempts" | Rate limit: wait a minute. If it happens to everyone at once behind a reverse proxy, set `BXT_API_CLIENT_IP_HEADER` (the API logs a warning when it's missing). |
| Bot can't give out a role | Move the bot's role above that role in Server Settings → Roles. |
| Battle.net: redirect URL error on Blizzard's page | The client's redirect URL must be exactly `BXT_API_PUBLIC_URL` + `/auth/battlenet/callback`. |
| No World of Warcraft section on Linked accounts | `BXT_BATTLENET_CLIENT_ID` / `BXT_BATTLENET_CLIENT_SECRET` aren't set on the API. |
| Steam: no name or avatar on linked accounts | `BXT_STEAM_API_KEY` isn't set (optional; linking works without it). |

## Development

The `docker-compose.yml` in the repo root builds and runs the code you've checked out, on `localhost`. It's for testing changes, not for deploying: its database is disposable and isn't guaranteed to upgrade to a release.

1. Do steps 1 and 2 above, registering `http://localhost:8080/auth/callback` as a redirect.
2. Configure and start it:
   ```bash
   cp .env.example .env   # fill in the Discord values; the rest works as-is locally
   docker compose up -d --build
   docker compose logs -f watcher worker api
   ```
3. Open the web app at http://localhost:5173 (use `localhost`, not `127.0.0.1`).

CLAUDE.md has the architecture, conventions and test commands.

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
