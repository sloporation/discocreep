// Package config loads bot configuration from a YAML file and then applies
// environment-variable overrides on top.
//
// Precedence (lowest to highest):
//  1. Compiled-in defaults (see defaults())
//  2. YAML file at the path passed to Load (no error if it's missing)
//  3. Environment variables prefixed with BXT_
//
// The env-var mapping rule is: strip the BXT_ prefix, lowercase, replace the
// FIRST underscore with a dot. So:
//
//	BXT_DISCORD_TOKEN     -> discord.token
//	BXT_DISCORD_CLIENT_ID -> discord.client_id
//	BXT_DB_HOST           -> db.host
//	BXT_DB_POOL_SIZE      -> db.pool_size
//
// This keeps the YAML keys flat-ish (only one level of nesting) and lets a
// single .env file configure both the bot and docker-compose.
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/structs"
	"github.com/knadh/koanf/v2"
)

// Config is the fully-resolved runtime configuration.
type Config struct {
	Discord DiscordConfig `koanf:"discord"`
	DB      DBConfig      `koanf:"db"`
}

// DiscordConfig holds Discord API credentials and the optional dev guild ID
// used to register slash commands instantly (per-guild) instead of globally.
type DiscordConfig struct {
	Token    string `koanf:"token"`
	ClientID string `koanf:"client_id"`
	GuildID  string `koanf:"guild_id"`
}

// DBConfig holds MariaDB / MySQL connection parameters.
type DBConfig struct {
	Host     string `koanf:"host"`
	Port     int    `koanf:"port"`
	User     string `koanf:"user"`
	Password string `koanf:"password"`
	Name     string `koanf:"name"`
	PoolSize int    `koanf:"pool_size"`
}

// defaults returns the baseline config used when no file or env override sets a value.
func defaults() Config {
	return Config{
		DB: DBConfig{
			Host:     "localhost",
			Port:     3306,
			User:     "discordbot",
			Name:     "discordbot",
			PoolSize: 5,
		},
	}
}

// Load reads config from the given YAML path (optional) and env vars.
// A missing file is not an error; a malformed one is.
func Load(path string) (Config, error) {
	k := koanf.New(".")

	// 1. Defaults.
	if err := k.Load(structs.Provider(defaults(), "koanf"), nil); err != nil {
		return Config{}, fmt.Errorf("loading defaults: %w", err)
	}

	// 2. YAML file (optional).
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
				return Config{}, fmt.Errorf("loading %s: %w", path, err)
			}
		} else if !os.IsNotExist(err) {
			return Config{}, fmt.Errorf("stat %s: %w", path, err)
		}
	}

	// 3. Env override. BXT_DISCORD_TOKEN -> discord.token, BXT_DB_POOL_SIZE -> db.pool_size.
	if err := k.Load(env.Provider("BXT_", ".", envKey), nil); err != nil {
		return Config{}, fmt.Errorf("loading env: %w", err)
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal: %w", err)
	}
	return cfg, nil
}

// envKey converts an env-var name like BXT_DB_POOL_SIZE into the koanf key
// db.pool_size: drop the prefix, lowercase, and turn only the first underscore
// into a dot so the section name and key name are separated correctly.
func envKey(s string) string {
	s = strings.ToLower(strings.TrimPrefix(s, "BXT_"))
	if i := strings.Index(s, "_"); i > 0 {
		return s[:i] + "." + s[i+1:]
	}
	return s
}
