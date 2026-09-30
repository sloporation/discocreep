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
//	GET  /auth/steam/link, /auth/steam/callback, GET/DELETE /api/me/steam
//	                     link a Steam account (see steam.go)
//	GET  /auth/battlenet/link, /auth/battlenet/callback, GET/DELETE /api/me/battlenet,
//	GET/PUT/DELETE /api/guilds/{id}/wow-character
//	                     link Battle.net, pick a WoW character per guild (see battlenet.go)
//	/api/guilds/{id}/wow-sync..., GET /api/guilds/{id}/roles
//	                     WoW guild sync settings for admins (see wowsync.go)
//	GET  /api/guilds     guilds the user shares with the bot
//	GET  /api/guilds/{id}                          one shared guild
//	GET  /api/guilds/{id}/settings                 its settings (admins)
//	GET  /api/guilds/{id}/channels                 its text channels (admins)
//	PUT  /api/guilds/{id}/settings/purge           message purge switches (admins)
//	PUT  /api/guilds/{id}/settings/admin-alerts    admin alerts channel (admins)
//	GET  /healthz        liveness
//
// "Admins" are members who own the guild or have Administrator or Manage
// Server there, according to Discord (checked on every request, via the
// user's own guild list).
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
	"sync"
	"time"

	"github.com/disgoorg/disgo/oauth2"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"
	"github.com/redis/go-redis/v9"

	"gitlab.com/jacxb/bots/bxt/go/internal/alerts"
	"gitlab.com/jacxb/bots/bxt/go/internal/blizzard"
	"gitlab.com/jacxb/bots/bxt/go/internal/config"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

// Server is the web API.
type Server struct {
	oauth *oauth2.Client
	// bot calls Discord as the bot (its guilds, channels, posting messages).
	bot           rest.Rest
	db            *database.DB
	alerter       *alerts.Alerter
	rdb           *redis.Client
	sessions      *sessionStore
	steam         steamStore
	steamOpenID   string // Steam OpenID endpoint
	steamAPI      string // Steam Web API base URL
	steamAPIKey   string
	bnet          *blizzard.Client // nil when Battle.net isn't configured
	bnetRegions   []string
	bnetFlavours  []blizzard.Flavour
	botIDMu       sync.Mutex
	botID         snowflake.ID // the bot's user ID, looked up once
	bnetStore     battlenetStore
	httpClient    *http.Client
	publicURL     string
	redirectURI   string
	webURL        string
	webOrigin     string
	secureCookies bool
}

// Option adjusts how New builds the Server (used by tests).
type Option func(*options)

type options struct {
	discordURL  string     // Discord API base URL; empty = the real one
	steamURL    string     // Steam OpenID endpoint; empty = the real one
	steamAPIURL string     // Steam Web API base URL; empty = the real one
	steam       steamStore // Steam link storage; nil = the database

	bnetOAuthURL string         // Battle.net OAuth base; empty = the real one
	bnetAPIURL   string         // Profile API URL with %s for region; empty = the real one
	bnetStore    battlenetStore // nil = the database
}

// withDiscordURL points every Discord API call at url (tests).
func withDiscordURL(url string) Option {
	return func(o *options) { o.discordURL = url }
}

// withSteam points Steam calls at fake endpoints and storage (tests).
func withSteam(openIDURL, apiURL string, store steamStore) Option {
	return func(o *options) { o.steamURL, o.steamAPIURL, o.steam = openIDURL, apiURL, store }
}

// withBattlenet points Battle.net calls at fake endpoints and storage (tests).
func withBattlenet(oauthURL, apiURL string, store battlenetStore) Option {
	return func(o *options) { o.bnetOAuthURL, o.bnetAPIURL, o.bnetStore = oauthURL, apiURL, store }
}

// New builds the API from config.
func New(cfg config.Config, db *database.DB, rdb *redis.Client, opts ...Option) (*Server, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	var restOpts []rest.ClientConfigOpt
	if o.discordURL != "" {
		restOpts = append(restOpts, rest.WithURL(o.discordURL))
	}

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

	if cfg.Discord.Token == "" {
		return nil, fmt.Errorf("BXT_DISCORD_TOKEN (discord.token) is not set; the API uses it to look up the bot's guilds")
	}

	srv := &Server{
		oauth: oauth2.New(clientID, cfg.Discord.ClientSecret,
			oauth2.WithStateController(&stateController{rdb: rdb}),
			oauth2.WithRestClientConfigOpts(restOpts...),
		),
		bot:           rest.New(rest.NewClient(cfg.Discord.Token, restOpts...)),
		db:            db,
		alerter:       alerts.New(db),
		rdb:           rdb,
		sessions:      &sessionStore{rdb: rdb},
		steam:         &dbSteamStore{db: db},
		steamOpenID:   steamOpenIDURL,
		steamAPI:      steamAPIURL,
		steamAPIKey:   cfg.Steam.APIKey,
		httpClient:    &http.Client{Timeout: 15 * time.Second},
		publicURL:     publicURL,
		redirectURI:   publicURL + "/auth/callback",
		webURL:        webURL + "/",
		webOrigin:     webURL,
		secureCookies: strings.HasPrefix(publicURL, "https://"),
	}
	if o.steamURL != "" {
		srv.steamOpenID = o.steamURL
	}
	if o.steamAPIURL != "" {
		srv.steamAPI = o.steamAPIURL
	}
	if o.steam != nil {
		srv.steam = o.steam
	}

	srv.bnetStore = &dbBattlenetStore{db: db}
	if o.bnetStore != nil {
		srv.bnetStore = o.bnetStore
	}
	if cfg.BattleNet.ClientID != "" && cfg.BattleNet.ClientSecret != "" {
		regions, err := blizzard.ParseRegions(cfg.BattleNet.Regions)
		if err != nil {
			return nil, fmt.Errorf("battlenet.regions: %w", err)
		}
		srv.bnet = blizzard.New(cfg.BattleNet.ClientID, cfg.BattleNet.ClientSecret)
		srv.bnet.HTTP = srv.httpClient
		srv.bnetRegions = regions
		if srv.bnetFlavours, err = blizzard.ParseFlavours(cfg.BattleNet.Flavours); err != nil {
			return nil, fmt.Errorf("battlenet.flavours: %w", err)
		}
		if o.bnetOAuthURL != "" {
			srv.bnet.OAuthURL = o.bnetOAuthURL
		}
		if o.bnetAPIURL != "" {
			srv.bnet.APIURL = o.bnetAPIURL
		}
	}
	return srv, nil
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
	mux.HandleFunc("GET /auth/steam/link", s.handleSteamLink)
	mux.HandleFunc("GET /auth/steam/callback", s.handleSteamCallback)
	mux.HandleFunc("GET /api/me/steam", s.handleGetSteam)
	mux.HandleFunc("DELETE /api/me/steam", s.handleDeleteSteam)
	mux.HandleFunc("GET /auth/battlenet/link", s.handleBattlenetLink)
	mux.HandleFunc("GET /auth/battlenet/callback", s.handleBattlenetCallback)
	mux.HandleFunc("GET /api/me/battlenet", s.handleGetBattlenet)
	mux.HandleFunc("DELETE /api/me/battlenet", s.handleDeleteBattlenet)
	mux.HandleFunc("GET /api/guilds/{id}/wow-character", s.handleGetWowCharacter)
	mux.HandleFunc("PUT /api/guilds/{id}/wow-character", s.handlePutWowCharacter)
	mux.HandleFunc("DELETE /api/guilds/{id}/wow-character", s.handleDeleteWowCharacter)
	mux.HandleFunc("GET /api/guilds/{id}/wow-sync", s.handleGetWowSync)
	mux.HandleFunc("PUT /api/guilds/{id}/wow-sync", s.handlePutWowSync)
	mux.HandleFunc("POST /api/guilds/{id}/wow-sync/guilds", s.handleAddWowGuild)
	mux.HandleFunc("DELETE /api/guilds/{id}/wow-sync/guilds/{link}", s.handleDeleteWowGuild)
	mux.HandleFunc("PUT /api/guilds/{id}/wow-sync/guilds/{link}/ranks", s.handlePutWowRanks)
	mux.HandleFunc("GET /api/guilds/{id}/wow-sync/realms", s.handleWowRealms)
	mux.HandleFunc("GET /api/guilds/{id}/wow-sync/ranks", s.handleWowRanks)
	mux.HandleFunc("POST /api/guilds/{id}/wow-sync/run", s.handleRunWowSync)
	mux.HandleFunc("GET /api/guilds/{id}/roles", s.handleDiscordRoles)
	mux.HandleFunc("GET /api/guilds", s.handleGuilds)
	mux.HandleFunc("GET /api/guilds/{id}", s.handleGuild)
	mux.HandleFunc("GET /api/guilds/{id}/settings", s.handleGetSettings)
	mux.HandleFunc("GET /api/guilds/{id}/channels", s.handleChannels)
	mux.HandleFunc("PUT /api/guilds/{id}/settings/purge", s.handlePutPurge)
	mux.HandleFunc("PUT /api/guilds/{id}/settings/admin-alerts", s.handlePutAdminAlerts)
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
