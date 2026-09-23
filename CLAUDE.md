# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A Discord bot using discord.js v14 with a modular command/event structure based on the official discord.js guide patterns.

## Project Structure

```
src/                        # Node.js application
├── commands/               # Slash commands organized by category
│   ├── guild/
│   │   └── create.js
│   └── utility/
│       └── ping.js
├── events/                 # Event handlers (thin wrappers, delegate to services)
│   ├── ready.js
│   └── interactionCreate.js
├── services/               # Domain services with business logic
│   └── channelSync.js      # Channel database sync operations
├── migrations/             # Database migrations
│   └── 001_create_guilds.js
├── utils/
│   ├── db.js               # Database module (MariaDB)
│   └── migrate.js          # Migration runner
├── index.js                # Main entry point, loads commands and events
├── deploy-commands.js      # Registers slash commands with Discord API
├── migrate-cli.js          # Migration CLI
└── package.json
docker/
├── Dockerfile              # Multi-arch image build
└── entrypoint.sh           # Container entrypoint
```

## Adding Commands

Create a file in `src/commands/<category>/` with this structure:

```javascript
const { SlashCommandBuilder } = require('discord.js');

module.exports = {
    data: new SlashCommandBuilder()
        .setName('commandname')
        .setDescription('Command description'),
    async execute(interaction) {
        // Command logic
    },
};
```

Then run `npm run deploy` or `docker compose run --rm bot node src/deploy-commands.js` to register.

## Adding Events

Create a file in `src/events/` with this structure:

```javascript
const { Events } = require('discord.js');

module.exports = {
    name: Events.EventName,
    once: false,  // true for one-time events like ClientReady
    execute(...args) {
        // Event logic
    },
};
```

## Adding Services

Services contain business logic that may be shared across multiple events or commands. Create a file in `src/services/` that exports functions:

```javascript
// src/services/channelSync.js
async function upsertChannel(db, channel) {
    // Business logic here
}

async function syncGuild(db, guild) {
    // Can call other functions in this service
    await upsertChannel(db, channel);
}

module.exports = {
    upsertChannel,
    syncGuild,
};
```

Event handlers should be thin wrappers that delegate to services:

```javascript
// src/events/channelCreate.js
const channelSync = require('../services/channelSync');

module.exports = {
    name: Events.ChannelCreate,
    async execute(channel) {
        await channelSync.upsertChannel(channel.client.db, channel);
    },
};
```

## Environment Variables

Copy `.env.example` to `.env` and fill in:

- `DISCORD_TOKEN` - Bot token from Discord Developer Portal
- `CLIENT_ID` - Application ID from Discord Developer Portal
- `GUILD_ID` - Development server ID (optional, for faster command registration)
- `DB_HOST` - MariaDB host (use `mariadb` for Docker, `localhost` for local)
- `DB_PORT` - MariaDB port (default: 3306)
- `DB_USER` - Database user
- `DB_PASSWORD` - Database password
- `DB_NAME` - Database name
- `DB_POOL_SIZE` - Connection pool size (default: 5)

## Database Usage

The database is accessible in commands via `interaction.client.db`. The module provides three methods:

### db.query(sql, params)

Execute SELECT queries. Returns an array of row objects.

```javascript
async execute(interaction) {
    const users = await interaction.client.db.query(
        'SELECT * FROM users WHERE guild_id = ?',
        [interaction.guildId]
    );
    // users = [{ id: 1, name: 'foo' }, { id: 2, name: 'bar' }]
}
```

### db.insert(sql, params)

Execute INSERT queries. Returns `{ success, insertId, affectedRows, error? }`.

```javascript
async execute(interaction) {
    const result = await interaction.client.db.insert(
        'INSERT INTO users (discord_id, name) VALUES (?, ?)',
        [interaction.user.id, interaction.user.username]
    );
    if (result.success) {
        // result.insertId = new row ID
        // result.affectedRows = number of rows inserted
    } else {
        // result.error = error message
    }
}
```

### db.execute(sql, params)

Execute UPDATE/DELETE queries. Returns `{ success, affectedRows, error? }`.

```javascript
async execute(interaction) {
    const result = await interaction.client.db.execute(
        'UPDATE users SET name = ? WHERE discord_id = ?',
        [interaction.user.username, interaction.user.id]
    );
    // result.affectedRows = number of rows updated
}
```

## Migrations

Migrations live in `src/migrations/` as numbered JS files with `up` and `down` functions.

### Creating a Migration

Create a file like `src/migrations/002_create_users.js`:

```javascript
module.exports = {
    async up(db) {
        await db.query(`
            CREATE TABLE users (
                id BIGINT UNSIGNED PRIMARY KEY,
                name VARCHAR(100) NOT NULL
            )
        `);
    },

    async down(db) {
        await db.query('DROP TABLE IF EXISTS users');
    },
};
```

### Running Migrations

```bash
npm run migrate          # Apply all pending migrations
npm run migrate:down     # Rollback last migration
npm run migrate:status   # Show migration status

# Docker
docker compose run --rm bot node src/migrate-cli.js up
docker compose run --rm bot node src/migrate-cli.js down
docker compose run --rm bot node src/migrate-cli.js status
```

## Commands

### Local Development

```bash
npm install
npm run deploy      # Register slash commands
npm run migrate     # Apply database migrations
npm run dev         # Start with hot reload
npm run start       # Start normally
```

### Docker

```bash
docker compose build
docker compose run --rm bot node src/deploy-commands.js  # Register commands
docker compose run --rm bot node src/migrate-cli.js up   # Run migrations
docker compose up -d                                      # Start bot
docker compose logs -f                                    # View logs
docker compose up -d --build                              # Rebuild and restart
```

## Next Steps

1. Copy `.env.example` to `.env` and add Discord + database credentials
2. Build Docker image: `docker compose build`
3. Start MariaDB: `docker compose up -d mariadb`
4. Run migrations: `docker compose run --rm bot node src/migrate-cli.js up`
5. Deploy commands: `docker compose run --rm bot node src/deploy-commands.js`
6. Start bot: `docker compose up -d`
