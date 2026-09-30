package api

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/oauth2"
)

const (
	sessionCookie = "bxt_session"
	// stateCookie ties an OAuth2 state to the browser that started the login,
	// so an attacker can't make a victim complete the attacker's login.
	stateCookie = "bxt_oauth_state"
)

// scopes requested at login: who the user is, and which guilds they're in
// (needed to show guilds they can configure).
var scopes = []discord.OAuth2Scope{discord.OAuth2ScopeIdentify, discord.OAuth2ScopeGuilds}

// handleLogin starts a Discord login: GET /auth/login redirects to Discord's
// consent screen. The web app links here.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	authURL, state := s.oauth.GenerateAuthorizationURLState(oauth2.AuthorizationURLParams{
		RedirectURI: s.redirectURI,
		Scopes:      scopes,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Value:    state,
		Path:     "/auth",
		MaxAge:   int(stateTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleCallback finishes a Discord login: Discord redirects to GET
// /auth/callback with a one-time code, which is exchanged (with the client
// secret) for the user's tokens. A session is created and the user is sent
// back to the web app.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state, code := q.Get("state"), q.Get("code")

	// The state must match the one this browser was given.
	c, err := r.Cookie(stateCookie)
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Path: "/auth", MaxAge: -1})
	if err != nil || state == "" || c.Value != state {
		s.loginFailed(w, r, "invalid_state")
		return
	}

	if e := q.Get("error"); e != "" {
		s.oauth.StateController.UseState(state) // consume it
		s.loginFailed(w, r, e)                  // e.g. access_denied: the user clicked Cancel
		return
	}

	oauthSession, _, err := s.oauth.StartSession(code, state)
	if err != nil {
		reason := "exchange_failed"
		if errors.Is(err, oauth2.ErrStateNotFound) {
			reason = "invalid_state"
		}
		slog.Warn("api: start oauth session", "err", err)
		s.loginFailed(w, r, reason)
		return
	}

	user, err := s.oauth.GetUser(oauthSession)
	if err != nil {
		slog.Error("api: get discord user", "err", err)
		s.loginFailed(w, r, "discord_error")
		return
	}

	id, err := s.sessions.create(r.Context(), session{
		UserID:     user.ID,
		Username:   user.Username,
		GlobalName: user.GlobalName,
		AvatarURL:  user.EffectiveAvatarURL(),
		OAuth:      oauthSession,
		CreatedAt:  time.Now(),
	})
	if err != nil {
		slog.Error("api: create session", "err", err)
		s.loginFailed(w, r, "server_error")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	slog.Info("api: user logged in", "user_id", user.ID, "username", user.Username)
	http.Redirect(w, r, s.webURL, http.StatusFound)
}

// loginFailed sends the user back to the web app with ?login_error=reason.
func (s *Server) loginFailed(w http.ResponseWriter, r *http.Request, reason string) {
	u, _ := url.Parse(s.webURL)
	q := u.Query()
	q.Set("login_error", reason)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// handleLogout ends the session: POST /auth/logout.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.sessions.delete(r.Context(), c.Value); err != nil {
			slog.Error("api: delete session", "err", err)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.secureCookies, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

// currentSession returns the request's session and its ID, or ok = false
// (after writing a 401) if it has none.
func (s *Server) currentSession(w http.ResponseWriter, r *http.Request) (sess session, id string, ok bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not_logged_in")
		return session{}, "", false
	}
	sess, ok, err = s.sessions.get(r.Context(), c.Value)
	if err != nil {
		slog.Error("api: load session", "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return session{}, "", false
	}
	if !ok {
		writeError(w, http.StatusUnauthorized, "not_logged_in")
		return session{}, "", false
	}
	return sess, c.Value, true
}

// handleMe returns the logged-in user: GET /api/me.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          sess.UserID,
		"username":    sess.Username,
		"global_name": sess.GlobalName,
		"avatar_url":  sess.AvatarURL,
	})
}
