package api

// WoW guild sync settings for admins. The sync itself runs in the worker
// (internal/discord/wowSync); settings are read and written through that
// feature's shared package. A server can link any number of WoW guilds,
// including several of one flavour (communities split by the 1,000-member
// cap); each linked guild has an ID ("link").
//
//	GET    /api/guilds/{id}/wow-sync                           settings, linked guilds, rank names/roles, last run
//	PUT    /api/guilds/{id}/wow-sync                           save settings (enabled, nickname mode, remove roles)
//	POST   /api/guilds/{id}/wow-sync/guilds                    link a WoW guild (checked with Blizzard)
//	DELETE /api/guilds/{id}/wow-sync/guilds/{link}             unlink it
//	PUT    /api/guilds/{id}/wow-sync/guilds/{link}/ranks       save its rank names and rank → role table
//	GET    /api/guilds/{id}/wow-sync/realms?flavour=&region=   realms for the guild picker
//	GET    /api/guilds/{id}/wow-sync/ranks                     ranks found in the linked guilds' rosters
//	POST   /api/guilds/{id}/wow-sync/run                       sync now
//	GET    /api/guilds/{id}/roles                              the server's Discord roles

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/blizzard"
	wowsync "gitlab.com/jacxb/bots/bxt/go/internal/discord/wowSync/shared"
)

const (
	realmsCacheTTL   = 24 * time.Hour
	realmsKeyPrefix  = "bxt:api:wow-realms:"
	ranksCacheTTL    = 5 * time.Minute
	ranksKeyPrefix   = "bxt:api:wow-ranks:"
	rankExampleCount = 3
	// maxParallelRosters bounds concurrent roster requests to Blizzard.
	maxParallelRosters = 4
)

type wowSyncView struct {
	// BattlenetEnabled is false when this deployment has no Battle.net client.
	BattlenetEnabled bool                `json:"battlenet_enabled"`
	Flavours         []blizzard.Flavour  `json:"flavours"`
	Settings         wowsync.Settings    `json:"settings"`
	Guilds           []wowsync.GuildLink `json:"guilds"`
	Status           wowsync.Status      `json:"status"`
	// RankRoles maps "link:rank" (e.g. "12:1") to a Discord role ID.
	RankRoles map[string]string `json:"rank_roles"`
	// RankNames maps "link:rank" to the admin-entered rank name (Blizzard's
	// API only has rank numbers).
	RankNames map[string]string `json:"rank_names"`
}

func (s *Server) handleGetWowSync(w http.ResponseWriter, r *http.Request) {
	g, ok := s.adminGuildFromPath(w, r)
	if !ok {
		return
	}
	s.writeWowSync(w, r, g.ID)
}

func (s *Server) writeWowSync(w http.ResponseWriter, r *http.Request, guildID snowflake.ID) {
	settings, links, status, err := wowsync.Load(r.Context(), s.db, guildID)
	if err != nil {
		slog.Error("api: load wow sync", "guild_id", guildID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	mapping, err := wowsync.RankRoles(r.Context(), s.db, guildID)
	if err != nil {
		slog.Error("api: load rank roles", "guild_id", guildID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	names, err := wowsync.RankNames(r.Context(), s.db, guildID)
	if err != nil {
		slog.Error("api: load rank names", "guild_id", guildID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	rr := map[string]string{}
	for k, role := range mapping {
		rr[k.String()] = role.String()
	}
	rn := map[string]string{}
	for k, name := range names {
		rn[k.String()] = name
	}
	writeJSON(w, http.StatusOK, wowSyncView{
		BattlenetEnabled: s.bnet != nil,
		Flavours:         s.bnetFlavours,
		Settings:         settings,
		Guilds:           links,
		Status:           status,
		RankRoles:        rr,
		RankNames:        rn,
	})
}

func (s *Server) handlePutWowSync(w http.ResponseWriter, r *http.Request) {
	g, ok := s.adminGuildFromPath(w, r)
	if !ok {
		return
	}
	if s.bnet == nil {
		writeError(w, http.StatusUnprocessableEntity, "battlenet_not_configured")
		return
	}
	var body wowsync.Settings
	if !decodeBody(w, r, &body) {
		return
	}
	if !body.NicknameMode.Valid() {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if err := wowsync.SaveSettings(r.Context(), s.db, g.ID, body); err != nil {
		slog.Error("api: save wow sync", "guild_id", g.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	slog.Info("api: wow sync settings changed", "guild_id", g.ID, "enabled", body.Enabled, "nickname_mode", body.NicknameMode, "remove_roles", body.RemoveRoles, "by", s.userID(r))
	s.writeWowSync(w, r, g.ID)
}

// supportedFlavour reports whether this deployment supports f.
func (s *Server) supportedFlavour(f blizzard.Flavour) bool {
	for _, ok := range s.bnetFlavours {
		if ok == f {
			return true
		}
	}
	return false
}

// linkFromPath reads {link}, writing a 404 if it isn't a number.
func linkFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("link"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "no_such_link")
		return 0, false
	}
	return id, true
}

// handleAddWowGuild links a WoW guild: POST /api/guilds/{id}/wow-sync/guilds
// {"flavour":"retail","region":"us","realm_slug":"area-52","guild":"Loot Council"}.
func (s *Server) handleAddWowGuild(w http.ResponseWriter, r *http.Request) {
	g, ok := s.adminGuildFromPath(w, r)
	if !ok {
		return
	}
	if s.bnet == nil {
		writeError(w, http.StatusUnprocessableEntity, "battlenet_not_configured")
		return
	}
	var body struct {
		Flavour   blizzard.Flavour `json:"flavour"`
		Region    string           `json:"region"`
		RealmSlug string           `json:"realm_slug"`
		Guild     string           `json:"guild"` // name as typed
	}
	if !decodeBody(w, r, &body) {
		return
	}
	slug := blizzard.GuildSlug(body.Guild)
	if !s.supportedFlavour(body.Flavour) || !blizzard.Regions[body.Region] || body.RealmSlug == "" || slug == "" {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	roster, err := s.bnet.GuildRoster(ctx, body.Flavour, body.Region, body.RealmSlug, slug)
	if err != nil {
		if code := rosterProblem(err); code != "blizzard_error" {
			writeError(w, http.StatusUnprocessableEntity, code)
			return
		}
		slog.Error("api: look up wow guild", "guild_id", g.ID, "flavour", body.Flavour, "err", err)
		writeError(w, http.StatusBadGateway, "blizzard_error")
		return
	}

	link := wowsync.GuildLink{
		Flavour: body.Flavour, Region: body.Region, RealmSlug: body.RealmSlug, RealmName: roster.RealmName,
		WowGuildSlug: slug, WowGuildName: roster.GuildName,
	}
	if link.RealmName == "" {
		link.RealmName = body.RealmSlug
	}
	if link.WowGuildName == "" {
		link.WowGuildName = strings.TrimSpace(body.Guild)
	}
	id, err := wowsync.AddGuild(r.Context(), s.db, g.ID, link)
	if errors.Is(err, wowsync.ErrDuplicateGuild) {
		writeError(w, http.StatusConflict, "wow_guild_already_linked")
		return
	}
	if err != nil {
		slog.Error("api: add wow guild", "guild_id", g.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	link.ID = id
	cacheFor(r.Context(), s.rdb, ranksKey(link), rankSummary(link.ID, roster), ranksCacheTTL)
	slog.Info("api: wow guild linked", "guild_id", g.ID, "link", id, "flavour", link.Flavour, "wow_guild", link.WowGuildName, "by", s.userID(r))
	s.writeWowSync(w, r, g.ID)
}

// handleDeleteWowGuild unlinks a WoW guild: DELETE /api/guilds/{id}/wow-sync/guilds/{link}.
func (s *Server) handleDeleteWowGuild(w http.ResponseWriter, r *http.Request) {
	g, ok := s.adminGuildFromPath(w, r)
	if !ok {
		return
	}
	linkID, ok := linkFromPath(w, r)
	if !ok {
		return
	}
	err := wowsync.RemoveGuild(r.Context(), s.db, g.ID, linkID)
	if errors.Is(err, wowsync.ErrNoSuchLink) {
		writeError(w, http.StatusNotFound, "no_such_link")
		return
	}
	if err != nil {
		slog.Error("api: remove wow guild", "guild_id", g.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	slog.Info("api: wow guild unlinked", "guild_id", g.ID, "link", linkID, "by", s.userID(r))
	s.writeWowSync(w, r, g.ID)
}

func (s *Server) handleWowRealms(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.adminGuildFromPath(w, r); !ok {
		return
	}
	f := blizzard.Flavour(r.URL.Query().Get("flavour"))
	region := r.URL.Query().Get("region")
	if s.bnet == nil || !s.supportedFlavour(f) || !blizzard.Regions[region] {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	key := realmsKeyPrefix + string(f) + ":" + region
	var realms []blizzard.Realm
	if !cached(r.Context(), s.rdb, key, &realms) {
		var err error
		if realms, err = s.bnet.Realms(r.Context(), f, region); err != nil {
			if errors.Is(err, blizzard.ErrNotFound) {
				realms = []blizzard.Realm{} // no realms for this flavour in this region
			} else {
				slog.Error("api: list realms", "flavour", f, "region", region, "err", err)
				writeError(w, http.StatusBadGateway, "blizzard_error")
				return
			}
		}
		cacheFor(r.Context(), s.rdb, key, realms, realmsCacheTTL)
	}
	writeJSON(w, http.StatusOK, realms)
}

// rankInfo is one rank found in a linked WoW guild's roster.
type rankInfo struct {
	LinkID   int64    `json:"link_id"`
	Rank     int      `json:"rank"`
	Key      string   `json:"key"` // "link:rank", as used in rank_roles / rank_names
	Members  int      `json:"members"`
	Examples []string `json:"examples"`
}

func ranksKey(l wowsync.GuildLink) string {
	return ranksKeyPrefix + string(l.Flavour) + ":" + l.Region + ":" + l.RealmSlug + ":" + l.WowGuildSlug
}

func rankSummary(linkID int64, roster *blizzard.Roster) []rankInfo {
	byRank := map[int]*rankInfo{}
	for _, m := range roster.Members {
		ri := byRank[m.Rank]
		if ri == nil {
			ri = &rankInfo{LinkID: linkID, Rank: m.Rank, Key: wowsync.RankKey{LinkID: linkID, Rank: m.Rank}.String()}
			byRank[m.Rank] = ri
		}
		ri.Members++
		if len(ri.Examples) < rankExampleCount {
			ri.Examples = append(ri.Examples, m.Name)
		}
	}
	out := make([]rankInfo, 0, len(byRank))
	for _, ri := range byRank {
		out = append(out, *ri)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Rank < out[j].Rank })
	return out
}

// ranksResponse lists the ranks of every linked guild, and which linked
// guilds' rosters couldn't be read (by link ID).
type ranksResponse struct {
	Ranks  []rankInfo       `json:"ranks"`
	Errors map[int64]string `json:"errors,omitempty"`
}

// handleWowRanks returns the ranks in every linked guild's roster, read in
// parallel and cached for a few minutes: GET /api/guilds/{id}/wow-sync/ranks.
func (s *Server) handleWowRanks(w http.ResponseWriter, r *http.Request) {
	g, ok := s.adminGuildFromPath(w, r)
	if !ok {
		return
	}
	_, links, _, err := wowsync.Load(r.Context(), s.db, g.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	resp := ranksResponse{Ranks: []rankInfo{}, Errors: map[int64]string{}}
	if s.bnet == nil {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		sem    = make(chan struct{}, maxParallelRosters)
		byLink = map[int64][]rankInfo{}
	)
	for _, link := range links {
		var cachedRanks []rankInfo
		if cached(r.Context(), s.rdb, ranksKey(link), &cachedRanks) {
			for i := range cachedRanks { // cache is per WoW guild; the link ID may differ
				cachedRanks[i].LinkID = link.ID
				cachedRanks[i].Key = wowsync.RankKey{LinkID: link.ID, Rank: cachedRanks[i].Rank}.String()
			}
			byLink[link.ID] = cachedRanks
			continue
		}
		wg.Add(1)
		go func(link wowsync.GuildLink) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			roster, err := s.bnet.GuildRoster(r.Context(), link.Flavour, link.Region, link.RealmSlug, link.WowGuildSlug)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				slog.Warn("api: read wow roster", "guild_id", g.ID, "link", link.ID, "err", err)
				resp.Errors[link.ID] = rosterProblem(err)
				return
			}
			ranks := rankSummary(link.ID, roster)
			byLink[link.ID] = ranks
			cacheFor(r.Context(), s.rdb, ranksKey(link), ranks, ranksCacheTTL)
		}(link)
	}
	wg.Wait()
	for _, link := range links { // keep the links' order
		resp.Ranks = append(resp.Ranks, byLink[link.ID]...)
	}
	writeJSON(w, http.StatusOK, resp)
}

// rosterProblem is a short machine-readable reason a roster couldn't be read.
func rosterProblem(err error) string {
	var se blizzard.StatusError
	switch {
	case errors.Is(err, blizzard.ErrNotFound):
		return "wow_guild_not_found"
	case errors.As(err, &se) && se.Status == http.StatusForbidden:
		return "blizzard_forbidden"
	default:
		return "blizzard_error"
	}
}

// handlePutWowRanks saves one linked guild's rank names and rank → role
// table: PUT /api/guilds/{id}/wow-sync/guilds/{link}/ranks
// {"ranks":{"0":{"name":"Guild Master","role_id":"123"},"1":{"name":"Officer","role_id":null}}}.
// Ranks left out get no name and no role.
func (s *Server) handlePutWowRanks(w http.ResponseWriter, r *http.Request) {
	g, ok := s.adminGuildFromPath(w, r)
	if !ok {
		return
	}
	linkID, ok := linkFromPath(w, r)
	if !ok {
		return
	}
	var body struct {
		Ranks map[string]struct {
			Name   string        `json:"name"`
			RoleID *snowflake.ID `json:"role_id"`
		} `json:"ranks"`
	}
	if !decodeBody(w, r, &body) {
		return
	}

	roles, err := s.discordRoles(g.ID)
	if err != nil {
		slog.Error("api: list roles", "guild_id", g.ID, "err", err)
		writeError(w, http.StatusBadGateway, "discord_error")
		return
	}
	assignable := map[snowflake.ID]bool{}
	for _, role := range roles {
		assignable[role.ID] = role.Assignable
	}

	ranks := map[int]wowsync.RankSetting{}
	for k, v := range body.Ranks {
		rank, err := strconv.Atoi(k)
		if err != nil || rank < 0 || rank > 255 || utf8.RuneCountInString(strings.TrimSpace(v.Name)) > wowsync.MaxRankNameLength {
			writeError(w, http.StatusBadRequest, "bad_request")
			return
		}
		rs := wowsync.RankSetting{Name: strings.TrimSpace(v.Name)}
		if v.RoleID != nil && *v.RoleID != 0 {
			if !assignable[*v.RoleID] {
				writeError(w, http.StatusUnprocessableEntity, "role_not_assignable")
				return
			}
			rs.RoleID = *v.RoleID
		}
		ranks[rank] = rs
	}
	err = wowsync.SaveGuildRanks(r.Context(), s.db, g.ID, linkID, ranks)
	if errors.Is(err, wowsync.ErrNoSuchLink) {
		writeError(w, http.StatusNotFound, "no_such_link")
		return
	}
	if err != nil {
		slog.Error("api: save ranks", "guild_id", g.ID, "link", linkID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	slog.Info("api: wow ranks changed", "guild_id", g.ID, "link", linkID, "by", s.userID(r))
	s.writeWowSync(w, r, g.ID)
}

func (s *Server) handleRunWowSync(w http.ResponseWriter, r *http.Request) {
	g, ok := s.adminGuildFromPath(w, r)
	if !ok {
		return
	}
	if err := wowsync.RequestSync(r.Context(), s.db, g.ID); err != nil {
		slog.Error("api: request wow sync", "guild_id", g.ID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// roleView is a Discord role as the web app sees it.
type roleView struct {
	ID       snowflake.ID `json:"id"`
	Name     string       `json:"name"`
	Color    int          `json:"color"`
	Position int          `json:"position"`
	// Assignable: the bot can give this role (not @everyone, not managed by
	// an integration, and below the bot's highest role).
	Assignable bool `json:"assignable"`
}

func (s *Server) handleDiscordRoles(w http.ResponseWriter, r *http.Request) {
	g, ok := s.adminGuildFromPath(w, r)
	if !ok {
		return
	}
	roles, err := s.discordRoles(g.ID)
	if err != nil {
		slog.Error("api: list roles", "guild_id", g.ID, "err", err)
		writeError(w, http.StatusBadGateway, "discord_error")
		return
	}
	writeJSON(w, http.StatusOK, roles)
}

// discordRoles lists a server's roles, highest first, marking which the bot can assign.
func (s *Server) discordRoles(guildID snowflake.ID) ([]roleView, error) {
	roles, err := s.bot.GetRoles(guildID)
	if err != nil {
		return nil, err
	}
	botID, err := s.botUserID()
	if err != nil {
		return nil, err
	}
	me, err := s.bot.GetMember(guildID, botID)
	if err != nil {
		return nil, fmt.Errorf("get bot member: %w", err)
	}
	positions := map[snowflake.ID]int{}
	for _, role := range roles {
		positions[role.ID] = role.Position
	}
	botTop := 0
	for _, id := range me.RoleIDs {
		botTop = max(botTop, positions[id])
	}

	out := make([]roleView, 0, len(roles))
	for _, role := range roles {
		if role.ID == guildID { // @everyone
			continue
		}
		out = append(out, roleView{
			ID:         role.ID,
			Name:       role.Name,
			Color:      role.Color,
			Position:   role.Position,
			Assignable: !role.Managed && role.Position < botTop,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position > out[j].Position })
	return out, nil
}

// botUserID returns the bot's own user ID (looked up once).
func (s *Server) botUserID() (snowflake.ID, error) {
	s.botIDMu.Lock()
	defer s.botIDMu.Unlock()
	if s.botID != 0 {
		return s.botID, nil
	}
	var u discord.User
	if err := s.bot.Do(rest.GetCurrentUser.Compile(nil), nil, &u); err != nil {
		return 0, fmt.Errorf("get bot user: %w", err)
	}
	s.botID = u.ID
	return s.botID, nil
}

// requestWowSync asks the worker to sync a server soon, e.g. after a member
// changes character. Best effort; a no-op without a database (tests) or
// without sync settings for the server.
func (s *Server) requestWowSync(ctx context.Context, guildID snowflake.ID) {
	if s.db == nil {
		return
	}
	if err := wowsync.RequestSync(ctx, s.db, guildID); err != nil {
		slog.Warn("api: request wow sync", "guild_id", guildID, "err", err)
	}
}
