// Package api is the web API behind the web app (web/). It lets users log
// in with Discord and, in time, configure the bot for their guilds.
//
// Authentication happens here, not in the browser: Discord's login ends with
// exchanging a code using the app's client secret, which must never reach
// the browser. After that the API keeps its own session (in Redis) and gives
// the browser only an HttpOnly session cookie.
//
// Routes:
//
//	GET  /auth/login     redirect to Discord's consent screen
//	GET  /auth/callback  Discord redirects back here; creates the session
//	POST /auth/logout    end the session
//	GET  /api/me         the logged-in user
//	GET  /healthz        liveness
//
// The web app runs on its own origin (api.web_url) and calls the API with
// credentials. CORS allows only that origin, and state-changing requests
// must come from it, which (with SameSite=Lax cookies) protects against
// cross-site request forgery.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/disgoorg/disgo/oauth2"
	"github.com/disgoorg/snowflake/v2"
	"github.com/redis/go-redis/v9"

	"gitlab.com/jacxb/bots/bxt/go/internal/config"
)

// Server is the web API.
type Server struct {
	oauth         *oauth2.Client
	sessions      *sessionStore
	redirectURI   string
	webURL        string
	webOrigin     string
	secureCookies bool
}

// New builds the API from config.
func New(cfg config.Config, rdb *redis.Client) (*Server, error) {
	if cfg.Discord.ClientID == "" {
		return nil, fmt.Errorf("BXT_DISCORD_CLIENT_ID (discord.client_id) is not set; it's the Application ID from the Developer Portal")
	}
	if cfg.Discord.ClientSecret == "" {
		return nil, fmt.Errorf("BXT_DISCORD_CLIENT_SECRET (discord.client_secret) is not set; copy it from Developer Portal > OAuth2 > Client Secret")
	}
	clientID, err := snowflake.Parse(cfg.Discord.ClientID)
	if err != nil {
		return nil, fmt.Errorf("parse discord.client_id: %w", err)
	}
	publicURL := strings.TrimRight(cfg.API.PublicURL, "/")
	webURL := strings.TrimRight(cfg.API.WebURL, "/")
	if publicURL == "" || webURL == "" {
		return nil, fmt.Errorf("api.public_url and api.web_url are required")
	}

	return &Server{
		oauth: oauth2.New(clientID, cfg.Discord.ClientSecret,
			oauth2.WithStateController(&stateController{rdb: rdb}),
		),
		sessions:      &sessionStore{rdb: rdb},
		redirectURI:   publicURL + "/auth/callback",
		webURL:        webURL + "/",
		webOrigin:     webURL,
		secureCookies: strings.HasPrefix(publicURL, "https://"),
	}, nil
}

// RedirectURI is the URL that must be registered as an OAuth2 redirect in
// the Discord Developer Portal.
func (s *Server) RedirectURI() string { return s.redirectURI }

// Handler returns the API's HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/login", s.handleLogin)
	mux.HandleFunc("GET /auth/callback", s.handleCallback)
	mux.HandleFunc("POST /auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/me", s.handleMe)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return s.cors(s.sameOriginWrites(mux))
}

// cors lets the web app (and only it) call the API with cookies.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") == s.webOrigin {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", s.webOrigin)
			h.Set("Access-Control-Allow-Credentials", "true")
			h.Add("Vary", "Origin")
			if r.Method == http.MethodOptions {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
				h.Set("Access-Control-Allow-Headers", "Content-Type")
				h.Set("Access-Control-Max-Age", "600")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// sameOriginWrites rejects state-changing requests that don't come from the
// web app, so another site can't use a logged-in user's cookie.
func (s *Server) sameOriginWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get("Origin") != s.webOrigin {
				writeError(w, http.StatusForbidden, "bad_origin")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
