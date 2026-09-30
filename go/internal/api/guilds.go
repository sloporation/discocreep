package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/redis/go-redis/v9"
)

const (
	// guildCacheTTL is how long guild lists are cached. Discord rate-limits
	// these lookups, and a page load makes several API calls; the cost is that
	// a permission change on Discord takes up to this long to show here.
	guildCacheTTL = time.Minute

	botGuildsKey       = "bxt:api:bot-guilds"
	userGuildsPrefix   = "bxt:api:user-guilds:"
	botGuildsPageLimit = 200
)

// guildView is a guild as the web app sees it.
type guildView struct {
	ID      snowflake.ID `json:"id"`
	Name    string       `json:"name"`
	IconURL *string      `json:"icon_url"`
	// IsAdmin: the user owns the guild or has Administrator or Manage Server.
	IsAdmin bool `json:"is_admin"`
}

// errSessionExpired means Discord no longer accepts the user's token.
var errSessionExpired = errors.New("discord session expired")

// sharedGuilds returns the guilds the user and the bot are both in, sorted
// by name.
func (s *Server) sharedGuilds(ctx context.Context, sessionID string, sess session) ([]guildView, error) {
	userGuilds, err := s.userGuilds(ctx, sessionID, sess)
	if err != nil {
		return nil, err
	}
	botGuilds, err := s.botGuilds(ctx)
	if err != nil {
		return nil, err
	}

	shared := make([]guildView, 0, len(userGuilds))
	for _, g := range userGuilds {
		if _, ok := botGuilds[g.ID]; !ok {
			continue
		}
		v := guildView{
			ID:      g.ID,
			Name:    g.Name,
			IsAdmin: g.Owner || g.Permissions.Has(discord.PermissionAdministrator) || g.Permissions.Has(discord.PermissionManageGuild),
		}
		if g.Icon != nil {
			u := fmt.Sprintf("https://cdn.discordapp.com/icons/%s/%s.png?size=128", g.ID, *g.Icon)
			v.IconURL = &u
		}
		shared = append(shared, v)
	}
	sort.Slice(shared, func(i, j int) bool { return shared[i].Name < shared[j].Name })
	return shared, nil
}

// userGuilds returns the guilds the user is in (with their permissions),
// asked of Discord with the user's own token and cached per session.
func (s *Server) userGuilds(ctx context.Context, sessionID string, sess session) ([]discord.OAuth2Guild, error) {
	key := userGuildsPrefix + sessionKey(sessionID)[len(sessionKeyPrefix):]
	var guilds []discord.OAuth2Guild
	if cached(ctx, s.rdb, key, &guilds) {
		return guilds, nil
	}

	// An expired token, or a login from before the guilds scope was
	// requested, can't list guilds: make the user log in again.
	if sess.OAuth.Expired() || !discord.HasScope(discord.OAuth2ScopeGuilds, sess.OAuth.Scopes...) {
		return nil, errSessionExpired
	}
	guilds, err := s.oauth.GetGuilds(sess.OAuth)
	if err != nil {
		var rerr *rest.Error
		if errors.As(err, &rerr) && rerr.Response != nil && rerr.Response.StatusCode == http.StatusUnauthorized {
			return nil, errSessionExpired
		}
		return nil, fmt.Errorf("get user guilds: %w", err)
	}
	cache(ctx, s.rdb, key, guilds)
	return guilds, nil
}

// botGuilds returns the IDs of every guild the bot is in, cached for all users.
func (s *Server) botGuilds(ctx context.Context) (map[snowflake.ID]struct{}, error) {
	var ids []snowflake.ID
	if !cached(ctx, s.rdb, botGuildsKey, &ids) {
		var after snowflake.ID
		for {
			var page []discord.OAuth2Guild
			query := discord.QueryValues{"limit": botGuildsPageLimit}
			if after != 0 {
				query["after"] = after
			}
			if err := s.bot.Do(rest.GetCurrentUserGuilds.Compile(query), nil, &page); err != nil {
				return nil, fmt.Errorf("get bot guilds: %w", err)
			}
			for _, g := range page {
				ids = append(ids, g.ID)
				after = max(after, g.ID)
			}
			if len(page) < botGuildsPageLimit {
				break
			}
		}
		cache(ctx, s.rdb, botGuildsKey, ids)
	}

	set := make(map[snowflake.ID]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set, nil
}

// guildFromPath resolves /api/guilds/{id} to a guild the user shares with
// the bot. On failure it writes the response (401/404/...) and returns ok = false.
func (s *Server) guildFromPath(w http.ResponseWriter, r *http.Request) (guildView, bool) {
	sess, sessionID, ok := s.currentSession(w, r)
	if !ok {
		return guildView{}, false
	}
	id, err := snowflake.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "guild_not_found")
		return guildView{}, false
	}

	guilds, err := s.sharedGuilds(r.Context(), sessionID, sess)
	if err != nil {
		s.guildsFailed(w, r, sessionID, err)
		return guildView{}, false
	}
	for _, g := range guilds {
		if g.ID == id {
			return g, true
		}
	}
	// Not shared, or doesn't exist: don't reveal which.
	writeError(w, http.StatusNotFound, "guild_not_found")
	return guildView{}, false
}

// adminGuildFromPath is guildFromPath, but also requires the user to be an
// admin of the guild (403 otherwise).
func (s *Server) adminGuildFromPath(w http.ResponseWriter, r *http.Request) (guildView, bool) {
	g, ok := s.guildFromPath(w, r)
	if !ok {
		return guildView{}, false
	}
	if !g.IsAdmin {
		writeError(w, http.StatusForbidden, "not_guild_admin")
		return guildView{}, false
	}
	return g, true
}

// guildsFailed writes the response for an error from sharedGuilds.
func (s *Server) guildsFailed(w http.ResponseWriter, r *http.Request, sessionID string, err error) {
	if errors.Is(err, errSessionExpired) {
		_ = s.sessions.delete(r.Context(), sessionID)
		writeError(w, http.StatusUnauthorized, "not_logged_in")
		return
	}
	slog.Error("api: load guilds", "err", err)
	writeError(w, http.StatusBadGateway, "discord_error")
}

// handleGuilds lists the guilds the user shares with the bot: GET /api/guilds.
func (s *Server) handleGuilds(w http.ResponseWriter, r *http.Request) {
	sess, sessionID, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	guilds, err := s.sharedGuilds(r.Context(), sessionID, sess)
	if err != nil {
		s.guildsFailed(w, r, sessionID, err)
		return
	}
	writeJSON(w, http.StatusOK, guilds)
}

// handleGuild returns one shared guild: GET /api/guilds/{id}.
func (s *Server) handleGuild(w http.ResponseWriter, r *http.Request) {
	if g, ok := s.guildFromPath(w, r); ok {
		writeJSON(w, http.StatusOK, g)
	}
}

// cached reads a JSON value from Redis into v, reporting whether it was there.
func cached(ctx context.Context, rdb *redis.Client, key string, v any) bool {
	b, err := rdb.Get(ctx, key).Bytes()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Warn("api: read cache", "key", key, "err", err)
		}
		return false
	}
	return json.Unmarshal(b, v) == nil
}

// cache stores v in Redis as JSON for guildCacheTTL.
func cache(ctx context.Context, rdb *redis.Client, key string, v any) {
	cacheFor(ctx, rdb, key, v, guildCacheTTL)
}

// cacheFor stores v in Redis as JSON for ttl.
func cacheFor(ctx context.Context, rdb *redis.Client, key string, v any, ttl time.Duration) {
	b, err := json.Marshal(v)
	if err == nil {
		err = rdb.Set(ctx, key, b, ttl).Err()
	}
	if err != nil {
		slog.Warn("api: write cache", "key", key, "err", err)
	}
}
