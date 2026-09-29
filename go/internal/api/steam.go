package api

// Steam account linking via "Sign in through Steam" (OpenID 2.0), the only
// sign-in Steam offers third parties. A logged-in Discord user is sent to
// Steam, signs in there, and comes back with their SteamID64; we confirm the
// response with Steam directly before trusting it, then store the link.
//
//	GET    /auth/steam/link?next=/guilds/123   redirect to Steam
//	GET    /auth/steam/callback                Steam redirects back here
//	GET    /api/me/steam                       the user's linked account
//	DELETE /api/me/steam                       unlink it

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/redis/go-redis/v9"

	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

const (
	steamOpenIDURL = "https://steamcommunity.com/openid/login"
	steamAPIURL    = "https://api.steampowered.com"

	steamStateCookie    = "bxt_steam_state"
	steamStateKeyPrefix = "bxt:steam:state:"
	steamProfilePrefix  = "bxt:steam:profile:"
	steamProfileTTL     = 10 * time.Minute

	openIDNS         = "http://specs.openid.net/auth/2.0"
	openIDIdentifier = "http://specs.openid.net/auth/2.0/identifier_select"
)

// steamClaimedID is the only identity format Steam returns.
var steamClaimedID = regexp.MustCompile(`^https://steamcommunity\.com/openid/id/(\d{17})$`)

// steamLink is a Discord user's linked Steam account.
type steamLink struct {
	SteamID  uint64
	LinkedAt time.Time
}

// steamStore persists links. An interface so tests can run without MariaDB.
type steamStore interface {
	get(ctx context.Context, discordUserID snowflake.ID) (*steamLink, error)
	link(ctx context.Context, discordUserID snowflake.ID, steamID uint64) error
	unlink(ctx context.Context, discordUserID snowflake.ID) (bool, error)
}

// dbSteamStore stores links in steam_links, and every change in the
// append-only steam_link_history.
type dbSteamStore struct {
	db *database.DB
}

func (s *dbSteamStore) get(ctx context.Context, discordUserID snowflake.ID) (*steamLink, error) {
	var l steamLink
	err := s.db.QueryRowContext(ctx,
		"SELECT steam_id, linked_at FROM steam_links WHERE discord_user_id = ?",
		discordUserID,
	).Scan(&l.SteamID, &l.LinkedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

func (s *dbSteamStore) link(ctx context.Context, discordUserID snowflake.ID, steamID uint64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Linking a different account replaces the old one; record that as an unlink.
	var previous uint64
	err = tx.QueryRowContext(ctx, "SELECT steam_id FROM steam_links WHERE discord_user_id = ? FOR UPDATE", discordUserID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if previous == steamID {
		return tx.Commit() // already linked to this account
	}
	if previous != 0 {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO steam_link_history (discord_user_id, steam_id, action) VALUES (?, ?, 'unlinked')",
			discordUserID, previous,
		); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO steam_links (discord_user_id, steam_id, linked_at) VALUES (?, ?, NOW())
		ON DUPLICATE KEY UPDATE steam_id = VALUES(steam_id), linked_at = VALUES(linked_at)`,
		discordUserID, steamID,
	); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO steam_link_history (discord_user_id, steam_id, action) VALUES (?, ?, 'linked')",
		discordUserID, steamID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *dbSteamStore) unlink(ctx context.Context, discordUserID snowflake.ID) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var steamID uint64
	err = tx.QueryRowContext(ctx, "SELECT steam_id FROM steam_links WHERE discord_user_id = ? FOR UPDATE", discordUserID).Scan(&steamID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM steam_links WHERE discord_user_id = ?", discordUserID); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO steam_link_history (discord_user_id, steam_id, action) VALUES (?, ?, 'unlinked')",
		discordUserID, steamID,
	); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// steamState is what a link attempt's state token remembers.
type steamState struct {
	SessionKey string `json:"session_key"` // which Discord session started it
	Next       string `json:"next"`        // where in the web app to return to
}

// handleSteamLink starts linking: GET /auth/steam/link?next=/guilds/123.
// The browser navigates here (it's not an API call), so failures redirect
// back to the web app rather than returning JSON.
func (s *Server) handleSteamLink(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))

	c, err := r.Cookie(sessionCookie)
	if err != nil {
		s.steamRedirect(w, r, next, "steam_error", "not_logged_in")
		return
	}
	if _, ok, err := s.sessions.get(r.Context(), c.Value); err != nil || !ok {
		s.steamRedirect(w, r, next, "steam_error", "not_logged_in")
		return
	}

	state := randomToken(24)
	b, _ := json.Marshal(steamState{SessionKey: sessionKey(c.Value), Next: next})
	if err := s.rdb.Set(r.Context(), steamStateKeyPrefix+state, b, stateTTL).Err(); err != nil {
		slog.Error("api: save steam state", "err", err)
		s.steamRedirect(w, r, next, "steam_error", "server_error")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     steamStateCookie,
		Value:    state,
		Path:     "/auth/steam",
		MaxAge:   int(stateTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})

	q := url.Values{
		"openid.ns":         {openIDNS},
		"openid.mode":       {"checkid_setup"},
		"openid.return_to":  {s.steamReturnTo(state)},
		"openid.realm":      {s.publicURL},
		"openid.identity":   {openIDIdentifier},
		"openid.claimed_id": {openIDIdentifier},
	}
	http.Redirect(w, r, s.steamOpenID+"?"+q.Encode(), http.StatusFound)
}

// handleSteamCallback finishes linking: Steam redirects to GET
// /auth/steam/callback with the signed-in account.
func (s *Server) handleSteamCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state := q.Get("state")

	// The state must be the one this browser was given, still unused.
	c, cerr := r.Cookie(steamStateCookie)
	http.SetCookie(w, &http.Cookie{Name: steamStateCookie, Path: "/auth/steam", MaxAge: -1})
	var st steamState
	raw, err := s.rdb.GetDel(r.Context(), steamStateKeyPrefix+state).Bytes()
	if cerr != nil || state == "" || c.Value != state || err != nil || json.Unmarshal(raw, &st) != nil {
		if err != nil && !errors.Is(err, redis.Nil) {
			slog.Error("api: load steam state", "err", err)
		}
		s.steamRedirect(w, r, "/", "steam_error", "invalid_state")
		return
	}

	// ...and started by the Discord session making this request.
	sc, err := r.Cookie(sessionCookie)
	if err != nil || sessionKey(sc.Value) != st.SessionKey {
		s.steamRedirect(w, r, st.Next, "steam_error", "not_logged_in")
		return
	}
	sess, ok, err := s.sessions.get(r.Context(), sc.Value)
	if err != nil || !ok {
		s.steamRedirect(w, r, st.Next, "steam_error", "not_logged_in")
		return
	}

	if q.Get("openid.mode") == "cancel" {
		s.steamRedirect(w, r, st.Next, "steam_error", "cancelled")
		return
	}

	steamID, err := s.verifySteamResponse(r.Context(), q, s.steamReturnTo(state))
	if err != nil {
		slog.Warn("api: steam sign-in rejected", "user_id", sess.UserID, "err", err)
		s.steamRedirect(w, r, st.Next, "steam_error", "verify_failed")
		return
	}

	if err := s.steam.link(r.Context(), sess.UserID, steamID); err != nil {
		slog.Error("api: save steam link", "user_id", sess.UserID, "err", err)
		s.steamRedirect(w, r, st.Next, "steam_error", "server_error")
		return
	}
	slog.Info("api: steam account linked", "user_id", sess.UserID, "steam_id", steamID)
	s.steamRedirect(w, r, st.Next, "steam", "linked")
}

// verifySteamResponse checks an OpenID positive assertion and returns the
// SteamID64 it vouches for. The final step, asking Steam whether it really
// signed this response, is what stops anyone forging a callback.
func (s *Server) verifySteamResponse(ctx context.Context, q url.Values, wantReturnTo string) (uint64, error) {
	if q.Get("openid.mode") != "id_res" {
		return 0, fmt.Errorf("mode %q", q.Get("openid.mode"))
	}
	if q.Get("openid.ns") != openIDNS {
		return 0, fmt.Errorf("namespace %q", q.Get("openid.ns"))
	}
	if q.Get("openid.op_endpoint") != steamOpenIDURL {
		return 0, fmt.Errorf("op_endpoint %q", q.Get("openid.op_endpoint"))
	}
	if q.Get("openid.return_to") != wantReturnTo {
		return 0, fmt.Errorf("return_to %q", q.Get("openid.return_to"))
	}
	m := steamClaimedID.FindStringSubmatch(q.Get("openid.claimed_id"))
	if m == nil || q.Get("openid.identity") != q.Get("openid.claimed_id") {
		return 0, fmt.Errorf("claimed_id %q", q.Get("openid.claimed_id"))
	}
	steamID, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return 0, err
	}

	// Ask Steam: send back every openid.* field, with mode check_authentication.
	form := url.Values{}
	for k, v := range q {
		if strings.HasPrefix(k, "openid.") {
			form[k] = v
		}
	}
	form.Set("openid.mode", "check_authentication")

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.steamOpenID, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := s.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("check_authentication: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode != http.StatusOK || !isValidAssertion(string(body)) {
		return 0, fmt.Errorf("steam did not confirm the response (status %d)", res.StatusCode)
	}
	return steamID, nil
}

// isValidAssertion reports whether a check_authentication response (lines
// of key:value) says is_valid:true.
func isValidAssertion(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "is_valid:true" {
			return true
		}
	}
	return false
}

func (s *Server) steamReturnTo(state string) string {
	return s.publicURL + "/auth/steam/callback?state=" + url.QueryEscape(state)
}

// steamRedirect sends the browser back to the web app at next, with
// ?key=value (e.g. steam=linked or steam_error=cancelled).
func (s *Server) steamRedirect(w http.ResponseWriter, r *http.Request, next, key, value string) {
	u, _ := url.Parse(s.webOrigin + safeNext(next))
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// safeNext keeps a return path inside the web app: it must be a plain path
// like /guilds/123, never another site ("//evil.example") or a full URL.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.ContainsAny(next, "\\\r\n") {
		return "/"
	}
	if u, err := url.Parse(next); err != nil || u.Host != "" || u.Scheme != "" {
		return "/"
	}
	return next
}

// steamAccountView is the linked account as the web app sees it.
type steamAccountView struct {
	Linked bool `json:"linked"`
	// SteamID is a string: SteamID64s don't fit in a JavaScript number.
	SteamID     string     `json:"steam_id,omitempty"`
	ProfileURL  string     `json:"profile_url,omitempty"`
	PersonaName string     `json:"persona_name,omitempty"`
	AvatarURL   string     `json:"avatar_url,omitempty"`
	LinkedAt    *time.Time `json:"linked_at,omitempty"`
}

// handleGetSteam returns the user's linked Steam account: GET /api/me/steam.
func (s *Server) handleGetSteam(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	link, err := s.steam.get(r.Context(), sess.UserID)
	if err != nil {
		slog.Error("api: load steam link", "user_id", sess.UserID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	if link == nil {
		writeJSON(w, http.StatusOK, steamAccountView{Linked: false})
		return
	}

	id := strconv.FormatUint(link.SteamID, 10)
	v := steamAccountView{
		Linked:     true,
		SteamID:    id,
		ProfileURL: "https://steamcommunity.com/profiles/" + id,
		LinkedAt:   &link.LinkedAt,
	}
	if p := s.steamProfile(r.Context(), id); p != nil {
		v.PersonaName, v.AvatarURL = p.PersonaName, p.AvatarFull
	}
	writeJSON(w, http.StatusOK, v)
}

// handleDeleteSteam unlinks the user's Steam account: DELETE /api/me/steam.
func (s *Server) handleDeleteSteam(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	if _, err := s.steam.unlink(r.Context(), sess.UserID); err != nil {
		slog.Error("api: unlink steam", "user_id", sess.UserID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	slog.Info("api: steam account unlinked", "user_id", sess.UserID)
	w.WriteHeader(http.StatusNoContent)
}

type steamPlayer struct {
	PersonaName string `json:"personaname"`
	AvatarFull  string `json:"avatarfull"`
}

// steamProfile fetches a Steam account's public name and avatar, if a Steam
// Web API key is configured. Best effort: nil on any failure. Cached briefly.
func (s *Server) steamProfile(ctx context.Context, steamID string) *steamPlayer {
	if s.steamAPIKey == "" {
		return nil
	}
	var p steamPlayer
	if cached(ctx, s.rdb, steamProfilePrefix+steamID, &p) {
		return &p
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	u := s.steamAPI + "/ISteamUser/GetPlayerSummaries/v2/?" + url.Values{"key": {s.steamAPIKey}, "steamids": {steamID}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil
	}
	res, err := s.httpClient.Do(req)
	if err != nil {
		slog.Warn("api: steam profile", "err", err)
		return nil
	}
	defer res.Body.Close()
	var body struct {
		Response struct {
			Players []steamPlayer `json:"players"`
		} `json:"response"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&body) != nil || len(body.Response.Players) == 0 {
		slog.Warn("api: steam profile", "status", res.StatusCode)
		return nil
	}
	p = body.Response.Players[0]
	if b, err := json.Marshal(p); err == nil {
		s.rdb.Set(ctx, steamProfilePrefix+steamID, b, steamProfileTTL)
	}
	return &p
}
