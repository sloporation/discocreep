package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/snowflake/v2"

	purge "gitlab.com/jacxb/bots/bxt/go/internal/discord/messagePurge/shared"
)

// maxBodyBytes caps request bodies; settings payloads are tiny.
const maxBodyBytes = 64 << 10

type purgeSettings struct {
	OnLeave    bool `json:"on_leave"`
	AdminPurge bool `json:"admin_purge"`
}

type adminAlertsSettings struct {
	ChannelID *snowflake.ID `json:"channel_id"`
}

type guildSettings struct {
	Purge       purgeSettings       `json:"purge"`
	AdminAlerts adminAlertsSettings `json:"admin_alerts"`
}

type channelView struct {
	ID       snowflake.ID `json:"id"`
	Name     string       `json:"name"`
	position int
}

// handleGetSettings returns the guild's web-configurable settings:
// GET /api/guilds/{id}/settings.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	g, ok := s.adminGuildFromPath(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	p, err := purge.LoadSettings(ctx, s.db, g.ID)
	if err != nil {
		slog.Error("api: load purge settings", "guild_id", g.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	alertChannel, err := s.alerter.Channel(ctx, g.ID)
	if err != nil {
		slog.Error("api: load admin alerts channel", "guild_id", g.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}

	writeJSON(w, http.StatusOK, guildSettings{
		Purge:       purgeSettings{OnLeave: p.OnLeave, AdminPurge: p.AdminPurge},
		AdminAlerts: adminAlertsSettings{ChannelID: alertChannel},
	})
}

// handleChannels lists the guild's text channels, for picking one in a
// setting: GET /api/guilds/{id}/channels.
func (s *Server) handleChannels(w http.ResponseWriter, r *http.Request) {
	g, ok := s.adminGuildFromPath(w, r)
	if !ok {
		return
	}
	channels, err := s.textChannels(g.ID)
	if err != nil {
		slog.Error("api: list channels", "guild_id", g.ID, "err", err)
		writeError(w, http.StatusBadGateway, "discord_error")
		return
	}
	writeJSON(w, http.StatusOK, channels)
}

func (s *Server) textChannels(guildID snowflake.ID) ([]channelView, error) {
	all, err := s.bot.GetGuildChannels(guildID)
	if err != nil {
		return nil, err
	}
	var out []channelView
	for _, ch := range all {
		if ch.Type() == discord.ChannelTypeGuildText {
			out = append(out, channelView{ID: ch.ID(), Name: ch.Name(), position: ch.Position()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].position < out[j].position })
	return out, nil
}

// handlePutPurge sets message purge's switches: PUT /api/guilds/{id}/settings/purge.
func (s *Server) handlePutPurge(w http.ResponseWriter, r *http.Request) {
	g, ok := s.adminGuildFromPath(w, r)
	if !ok {
		return
	}
	var body purgeSettings
	if !decodeBody(w, r, &body) {
		return
	}
	if err := purge.SaveSettings(r.Context(), s.db, g.ID, purge.Settings{OnLeave: body.OnLeave, AdminPurge: body.AdminPurge}); err != nil {
		slog.Error("api: save purge settings", "guild_id", g.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	slog.Info("api: purge settings changed", "guild_id", g.ID, "on_leave", body.OnLeave, "admin_purge", body.AdminPurge, "by", s.userID(r))
	writeJSON(w, http.StatusOK, body)
}

// handlePutAdminAlerts sets (or with null, clears) the admin alerts channel:
// PUT /api/guilds/{id}/settings/admin-alerts. Like /adminalerts set, it
// posts a confirmation in the channel first, so a missing permission is
// caught now.
func (s *Server) handlePutAdminAlerts(w http.ResponseWriter, r *http.Request) {
	g, ok := s.adminGuildFromPath(w, r)
	if !ok {
		return
	}
	var body adminAlertsSettings
	if !decodeBody(w, r, &body) {
		return
	}

	if body.ChannelID != nil {
		channels, err := s.textChannels(g.ID)
		if err != nil {
			slog.Error("api: list channels", "guild_id", g.ID, "err", err)
			writeError(w, http.StatusBadGateway, "discord_error")
			return
		}
		found := false
		for _, ch := range channels {
			found = found || ch.ID == *body.ChannelID
		}
		if !found {
			writeError(w, http.StatusUnprocessableEntity, "not_a_text_channel")
			return
		}

		if _, err := s.bot.CreateMessage(*body.ChannelID, discord.MessageCreate{
			Embeds: []discord.Embed{{
				Color:       0x57F287,
				Title:       "Admin alerts",
				Description: fmt.Sprintf("<@%s> set this as the admin alerts channel from the web dashboard. The bot will post here when something needs an admin's attention.", s.userID(r)),
			}},
		}); err != nil {
			slog.Warn("api: post admin alerts confirmation", "guild_id", g.ID, "channel_id", *body.ChannelID, "err", err)
			writeError(w, http.StatusUnprocessableEntity, "cannot_post_in_channel")
			return
		}
	}

	if err := s.alerter.SetChannel(r.Context(), g.ID, g.Name, body.ChannelID); err != nil {
		slog.Error("api: save admin alerts channel", "guild_id", g.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	slog.Info("api: admin alerts channel changed", "guild_id", g.ID, "channel_id", body.ChannelID, "by", s.userID(r))
	writeJSON(w, http.StatusOK, body)
}

// userID returns the logged-in user's ID for logging (0 if none).
func (s *Server) userID(r *http.Request) snowflake.ID {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return 0
	}
	sess, ok, err := s.sessions.get(r.Context(), c.Value)
	if err != nil || !ok {
		return 0
	}
	return sess.UserID
}

// decodeBody parses a JSON request body into v, writing a 400 on failure.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return false
	}
	return true
}
