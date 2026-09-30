package api

// Battle.net (World of Warcraft) account linking via Blizzard's OAuth 2.0
// authorization code flow. Linking reads the user's characters for each
// configured flavour (retail, Classic, Classic Era) from each configured
// region's Profile API and stores them; the Blizzard token is then discarded
// (it lasts 24h and can't be refreshed), so refreshing the character list
// means going through the (usually one-click) sign-in again.
//
//	GET    /auth/battlenet/link?next=/account   redirect to Battle.net
//	GET    /auth/battlenet/callback             Battle.net redirects back here
//	GET    /api/me/battlenet                    the link and its characters
//	DELETE /api/me/battlenet                    unlink
//	GET    /api/guilds/{id}/wow-character       the user's main per flavour here
//	PUT    /api/guilds/{id}/wow-character       set one ({"flavour","region","character_id"})
//	DELETE /api/guilds/{id}/wow-character?flavour=classic   clear one

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/disgoorg/snowflake/v2"
	"github.com/redis/go-redis/v9"

	"gitlab.com/jacxb/bots/bxt/go/internal/blizzard"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
)

const (
	battlenetStateCookie    = "bxt_bnet_state"
	battlenetStateKeyPrefix = "bxt:battlenet:state:"
)

// wowCharacter is a character on a linked Battle.net account.
type wowCharacter = blizzard.Character

type battlenetLink struct {
	BattlenetID uint64
	BattleTag   string
	LinkedAt    time.Time
	SyncedAt    *time.Time
}

// errCharacterNotOwned: the character isn't on the user's linked account.
var errCharacterNotOwned = errors.New("character not on the user's linked account")

// battlenetStore persists links, characters and primary characters. An
// interface so tests can run without MariaDB.
type battlenetStore interface {
	get(ctx context.Context, discordUserID snowflake.ID) (*battlenetLink, []wowCharacter, error)
	// save stores a (re)link and, for each flavour in synced, replaces the
	// user's characters of that flavour with those in chars. Characters of
	// other flavours (e.g. one Blizzard couldn't read this time) are kept.
	save(ctx context.Context, discordUserID snowflake.ID, battlenetID uint64, battleTag string, chars []wowCharacter, synced []blizzard.Flavour) error
	unlink(ctx context.Context, discordUserID snowflake.ID) (bool, error)
	// getPrimaries returns the user's main in the guild for each flavour they've set.
	getPrimaries(ctx context.Context, guildID, discordUserID snowflake.ID) (map[blizzard.Flavour]wowCharacter, error)
	// setPrimary returns errCharacterNotOwned if the character isn't the user's.
	setPrimary(ctx context.Context, guildID, discordUserID snowflake.ID, flavour blizzard.Flavour, region string, characterID uint64) error
	clearPrimary(ctx context.Context, guildID, discordUserID snowflake.ID, flavour blizzard.Flavour) error
}

// --- Handlers ------------------------------------------------------------

func (s *Server) battlenetRedirectURI() string { return s.publicURL + "/auth/battlenet/callback" }

// handleBattlenetLink starts linking (or refreshing characters):
// GET /auth/battlenet/link?next=/account.
func (s *Server) handleBattlenetLink(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	if s.bnet == nil {
		http.NotFound(w, r)
		return
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		s.backToApp(w, r, next, "battlenet_error", "not_logged_in")
		return
	}
	if _, ok, err := s.sessions.get(r.Context(), c.Value); err != nil || !ok {
		s.backToApp(w, r, next, "battlenet_error", "not_logged_in")
		return
	}

	state := randomToken(24)
	b, _ := json.Marshal(steamState{SessionKey: sessionKey(c.Value), Next: next})
	if err := s.rdb.Set(r.Context(), battlenetStateKeyPrefix+state, b, stateTTL).Err(); err != nil {
		slog.Error("api: save battlenet state", "err", err)
		s.backToApp(w, r, next, "battlenet_error", "server_error")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     battlenetStateCookie,
		Value:    state,
		Path:     "/auth/battlenet",
		MaxAge:   int(stateTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.secureCookies,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, s.bnet.AuthorizeURL(s.battlenetRedirectURI(), state), http.StatusFound)
}

// handleBattlenetCallback finishes linking: Battle.net redirects to GET
// /auth/battlenet/callback with a code, which is exchanged for a token
// used once to read the account and its characters.
func (s *Server) handleBattlenetCallback(w http.ResponseWriter, r *http.Request) {
	if s.bnet == nil {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	state := q.Get("state")

	c, cerr := r.Cookie(battlenetStateCookie)
	http.SetCookie(w, &http.Cookie{Name: battlenetStateCookie, Path: "/auth/battlenet", MaxAge: -1})
	var st steamState
	raw, err := s.rdb.GetDel(r.Context(), battlenetStateKeyPrefix+state).Bytes()
	if cerr != nil || state == "" || c.Value != state || err != nil || json.Unmarshal(raw, &st) != nil {
		if err != nil && !errors.Is(err, redis.Nil) {
			slog.Error("api: load battlenet state", "err", err)
		}
		s.backToApp(w, r, "/", "battlenet_error", "invalid_state")
		return
	}

	sc, err := r.Cookie(sessionCookie)
	if err != nil || sessionKey(sc.Value) != st.SessionKey {
		s.backToApp(w, r, st.Next, "battlenet_error", "not_logged_in")
		return
	}
	sess, ok, err := s.sessions.get(r.Context(), sc.Value)
	if err != nil || !ok {
		s.backToApp(w, r, st.Next, "battlenet_error", "not_logged_in")
		return
	}

	if e := q.Get("error"); e != "" {
		reason := "failed"
		if e == "access_denied" {
			reason = "cancelled"
		}
		s.backToApp(w, r, st.Next, "battlenet_error", reason)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	token, err := s.bnet.Exchange(ctx, q.Get("code"), s.battlenetRedirectURI())
	if err != nil {
		slog.Warn("api: battlenet exchange", "user_id", sess.UserID, "err", err)
		s.backToApp(w, r, st.Next, "battlenet_error", "exchange_failed")
		return
	}
	bnetID, battleTag, err := s.bnet.UserInfo(ctx, token)
	if err != nil {
		slog.Error("api: battlenet userinfo", "user_id", sess.UserID, "err", err)
		s.backToApp(w, r, st.Next, "battlenet_error", "blizzard_error")
		return
	}
	chars, failed, err := s.bnet.Characters(ctx, token, s.bnetRegions, s.bnetFlavours)
	if err != nil {
		slog.Error("api: battlenet characters", "user_id", sess.UserID, "err", err)
		s.backToApp(w, r, st.Next, "battlenet_error", "blizzard_error")
		return
	}
	var synced []blizzard.Flavour
	var skipped []string
	for _, f := range s.bnetFlavours {
		if ferr, bad := failed[f]; bad {
			slog.Warn("api: battlenet characters for a flavour failed; keeping stored ones", "user_id", sess.UserID, "flavour", f, "err", ferr)
			skipped = append(skipped, string(f))
		} else {
			synced = append(synced, f)
		}
	}

	if err := s.bnetStore.save(r.Context(), sess.UserID, bnetID, battleTag, chars, synced); err != nil {
		slog.Error("api: save battlenet link", "user_id", sess.UserID, "err", err)
		s.backToApp(w, r, st.Next, "battlenet_error", "server_error")
		return
	}
	slog.Info("api: battlenet linked", "user_id", sess.UserID, "battlenet_id", bnetID, "characters", len(chars), "skipped", skipped)
	extra := url.Values{"battlenet": {"linked"}}
	if len(skipped) > 0 {
		extra.Set("battlenet_skipped", strings.Join(skipped, ","))
	}
	s.backToAppValues(w, r, st.Next, extra)
}

type battlenetView struct {
	// Enabled is false when this deployment has no Battle.net client configured.
	Enabled bool `json:"enabled"`
	// Flavours are the game versions this deployment reads characters for.
	Flavours   []blizzard.Flavour `json:"flavours,omitempty"`
	Linked     bool               `json:"linked"`
	BattleTag  string             `json:"battletag,omitempty"`
	LinkedAt   *time.Time         `json:"linked_at,omitempty"`
	SyncedAt   *time.Time         `json:"synced_at,omitempty"`
	Characters []wowCharacter     `json:"characters,omitempty"`
}

// handleGetBattlenet returns the user's link and characters: GET /api/me/battlenet.
func (s *Server) handleGetBattlenet(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	if s.bnet == nil {
		writeJSON(w, http.StatusOK, battlenetView{Enabled: false})
		return
	}
	link, chars, err := s.bnetStore.get(r.Context(), sess.UserID)
	if err != nil {
		slog.Error("api: load battlenet link", "user_id", sess.UserID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	if link == nil {
		writeJSON(w, http.StatusOK, battlenetView{Enabled: true, Flavours: s.bnetFlavours})
		return
	}
	if chars == nil {
		chars = []wowCharacter{}
	}
	writeJSON(w, http.StatusOK, battlenetView{
		Enabled:    true,
		Flavours:   s.bnetFlavours,
		Linked:     true,
		BattleTag:  link.BattleTag,
		LinkedAt:   &link.LinkedAt,
		SyncedAt:   link.SyncedAt,
		Characters: chars,
	})
}

// handleDeleteBattlenet unlinks: DELETE /api/me/battlenet. It also removes
// the user's characters and primary character choices.
func (s *Server) handleDeleteBattlenet(w http.ResponseWriter, r *http.Request) {
	sess, _, ok := s.currentSession(w, r)
	if !ok {
		return
	}
	if _, err := s.bnetStore.unlink(r.Context(), sess.UserID); err != nil {
		slog.Error("api: unlink battlenet", "user_id", sess.UserID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	slog.Info("api: battlenet unlinked", "user_id", sess.UserID)
	w.WriteHeader(http.StatusNoContent)
}

// handleGetWowCharacter returns the user's main in a guild for each
// flavour: GET /api/guilds/{id}/wow-character → {"characters":{"retail":{...},"classic":null}}.
// Any member of a shared guild.
func (s *Server) handleGetWowCharacter(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guildFromPath(w, r)
	if !ok {
		return
	}
	s.writePrimaries(w, r, g.ID)
}

func (s *Server) writePrimaries(w http.ResponseWriter, r *http.Request, guildID snowflake.ID) {
	userID := s.userID(r)
	prim, err := s.bnetStore.getPrimaries(r.Context(), guildID, userID)
	if err != nil {
		slog.Error("api: load primary characters", "guild_id", guildID, "user_id", userID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	out := map[blizzard.Flavour]*wowCharacter{}
	for _, f := range s.bnetFlavours {
		out[f] = nil
	}
	for f, c := range prim {
		c := c
		out[f] = &c
	}
	writeJSON(w, http.StatusOK, map[string]any{"characters": out})
}

// handlePutWowCharacter sets the user's main for one flavour in a guild:
// PUT /api/guilds/{id}/wow-character {"flavour":"retail","region":"us","character_id":123}.
func (s *Server) handlePutWowCharacter(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guildFromPath(w, r)
	if !ok {
		return
	}
	var body struct {
		Flavour     blizzard.Flavour `json:"flavour"`
		Region      string           `json:"region"`
		CharacterID uint64           `json:"character_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if !body.Flavour.Valid() || !blizzard.Regions[body.Region] || body.CharacterID == 0 {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	userID := s.userID(r)
	err := s.bnetStore.setPrimary(r.Context(), g.ID, userID, body.Flavour, body.Region, body.CharacterID)
	if errors.Is(err, errCharacterNotOwned) {
		writeError(w, http.StatusUnprocessableEntity, "character_not_owned")
		return
	}
	if err != nil {
		slog.Error("api: save primary character", "guild_id", g.ID, "user_id", userID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	s.requestWowSync(r.Context(), g.ID)
	slog.Info("api: primary character set", "guild_id", g.ID, "user_id", userID, "flavour", body.Flavour, "region", body.Region, "character_id", body.CharacterID)
	s.writePrimaries(w, r, g.ID)
}

// handleDeleteWowCharacter clears the user's main for one flavour in a guild:
// DELETE /api/guilds/{id}/wow-character?flavour=classic.
func (s *Server) handleDeleteWowCharacter(w http.ResponseWriter, r *http.Request) {
	g, ok := s.guildFromPath(w, r)
	if !ok {
		return
	}
	flavour := blizzard.Flavour(r.URL.Query().Get("flavour"))
	if !flavour.Valid() {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	userID := s.userID(r)
	if err := s.bnetStore.clearPrimary(r.Context(), g.ID, userID, flavour); err != nil {
		slog.Error("api: clear primary character", "guild_id", g.ID, "user_id", userID, "err", err)
		writeError(w, http.StatusInternalServerError, "server_error")
		return
	}
	s.requestWowSync(r.Context(), g.ID)
	w.WriteHeader(http.StatusNoContent)
}

// --- Database store --------------------------------------------------------

type dbBattlenetStore struct {
	db *database.DB
}

const characterColumns = "flavour, region, character_id, name, realm_slug, realm_name, level, class_id, class_name, race_name, faction"

func scanCharacter(sc interface{ Scan(...any) error }) (wowCharacter, error) {
	var c wowCharacter
	err := sc.Scan(&c.Flavour, &c.Region, &c.ID, &c.Name, &c.RealmSlug, &c.RealmName, &c.Level, &c.ClassID, &c.ClassName, &c.RaceName, &c.Faction)
	return c, err
}

func (s *dbBattlenetStore) get(ctx context.Context, discordUserID snowflake.ID) (*battlenetLink, []wowCharacter, error) {
	var l battlenetLink
	err := s.db.QueryRowContext(ctx,
		"SELECT battlenet_id, battletag, linked_at, characters_synced_at FROM battlenet_links WHERE discord_user_id = ?",
		discordUserID,
	).Scan(&l.BattlenetID, &l.BattleTag, &l.LinkedAt, &l.SyncedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}

	rows, err := s.db.QueryContext(ctx,
		"SELECT "+characterColumns+" FROM wow_characters WHERE discord_user_id = ?",
		discordUserID,
	)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var chars []wowCharacter
	for rows.Next() {
		c, err := scanCharacter(rows)
		if err != nil {
			return nil, nil, err
		}
		chars = append(chars, c)
	}
	blizzard.SortCharacters(chars)
	return &l, chars, rows.Err()
}

func (s *dbBattlenetStore) save(ctx context.Context, discordUserID snowflake.ID, battlenetID uint64, battleTag string, chars []wowCharacter, synced []blizzard.Flavour) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var prevID uint64
	var prevTag string
	err = tx.QueryRowContext(ctx,
		"SELECT battlenet_id, battletag FROM battlenet_links WHERE discord_user_id = ? FOR UPDATE",
		discordUserID,
	).Scan(&prevID, &prevTag)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if prevID != battlenetID {
		if prevID != 0 {
			if err := bnetHistory(ctx, tx, discordUserID, prevID, prevTag, "unlinked"); err != nil {
				return err
			}
			// A different Battle.net account: none of the old characters apply.
			if _, err := tx.ExecContext(ctx, "DELETE FROM wow_characters WHERE discord_user_id = ?", discordUserID); err != nil {
				return err
			}
		}
		if err := bnetHistory(ctx, tx, discordUserID, battlenetID, battleTag, "linked"); err != nil {
			return err
		}
	}

	// linked_at only changes when a different account is linked (it's
	// assigned before battlenet_id, so the IF sees the old value).
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO battlenet_links (discord_user_id, battlenet_id, battletag, linked_at, characters_synced_at)
		VALUES (?, ?, ?, NOW(), NOW())
		ON DUPLICATE KEY UPDATE
		linked_at = IF(battlenet_id = VALUES(battlenet_id), linked_at, VALUES(linked_at)),
		battlenet_id = VALUES(battlenet_id),
		battletag = VALUES(battletag),
		characters_synced_at = VALUES(characters_synced_at)`,
		discordUserID, battlenetID, battleTag,
	); err != nil {
		return err
	}

	// Replace the user's characters for each flavour read this time. A
	// character already stored for another user (the same Battle.net
	// account linked elsewhere) moves to this one.
	for _, f := range synced {
		if _, err := tx.ExecContext(ctx, "DELETE FROM wow_characters WHERE discord_user_id = ? AND flavour = ?", discordUserID, f); err != nil {
			return err
		}
	}
	for _, c := range chars {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO wow_characters (`+characterColumns+`, discord_user_id, synced_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW())
			ON DUPLICATE KEY UPDATE
			discord_user_id = VALUES(discord_user_id), name = VALUES(name), realm_slug = VALUES(realm_slug),
			realm_name = VALUES(realm_name), level = VALUES(level), class_id = VALUES(class_id),
			class_name = VALUES(class_name), race_name = VALUES(race_name), faction = VALUES(faction),
			synced_at = VALUES(synced_at)`,
			c.Flavour, c.Region, c.ID, c.Name, c.RealmSlug, c.RealmName, c.Level, c.ClassID, c.ClassName, c.RaceName, c.Faction, discordUserID,
		); err != nil {
			return err
		}
	}

	if err := dropOrphanedPrimaries(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *dbBattlenetStore) unlink(ctx context.Context, discordUserID snowflake.ID) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var id uint64
	var tag string
	err = tx.QueryRowContext(ctx,
		"SELECT battlenet_id, battletag FROM battlenet_links WHERE discord_user_id = ? FOR UPDATE",
		discordUserID,
	).Scan(&id, &tag)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, q := range []string{
		"DELETE FROM battlenet_links WHERE discord_user_id = ?",
		"DELETE FROM wow_characters WHERE discord_user_id = ?",
		"DELETE FROM wow_primary_characters WHERE discord_user_id = ?",
	} {
		if _, err := tx.ExecContext(ctx, q, discordUserID); err != nil {
			return false, err
		}
	}
	if err := bnetHistory(ctx, tx, discordUserID, id, tag, "unlinked"); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *dbBattlenetStore) getPrimaries(ctx context.Context, guildID, discordUserID snowflake.ID) (map[blizzard.Flavour]wowCharacter, error) {
	// Joined on owner too, so a character that moved to another user doesn't show.
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.flavour, c.region, c.character_id, c.name, c.realm_slug, c.realm_name, c.level, c.class_id, c.class_name, c.race_name, c.faction
		FROM wow_primary_characters p
		JOIN wow_characters c ON c.flavour = p.flavour AND c.region = p.region AND c.character_id = p.character_id AND c.discord_user_id = p.discord_user_id
		WHERE p.guild_id = ? AND p.discord_user_id = ?`,
		guildID, discordUserID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[blizzard.Flavour]wowCharacter{}
	for rows.Next() {
		c, err := scanCharacter(rows)
		if err != nil {
			return nil, err
		}
		out[c.Flavour] = c
	}
	return out, rows.Err()
}

func (s *dbBattlenetStore) setPrimary(ctx context.Context, guildID, discordUserID snowflake.ID, flavour blizzard.Flavour, region string, characterID uint64) error {
	var owned int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM wow_characters WHERE flavour = ? AND region = ? AND character_id = ? AND discord_user_id = ?",
		flavour, region, characterID, discordUserID,
	).Scan(&owned); err != nil {
		return err
	}
	if owned == 0 {
		return errCharacterNotOwned
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO wow_primary_characters (guild_id, discord_user_id, flavour, region, character_id)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE region = VALUES(region), character_id = VALUES(character_id)`,
		guildID, discordUserID, flavour, region, characterID,
	)
	return err
}

func (s *dbBattlenetStore) clearPrimary(ctx context.Context, guildID, discordUserID snowflake.ID, flavour blizzard.Flavour) error {
	_, err := s.db.ExecContext(ctx,
		"DELETE FROM wow_primary_characters WHERE guild_id = ? AND discord_user_id = ? AND flavour = ?",
		guildID, discordUserID, flavour,
	)
	return err
}

func bnetHistory(ctx context.Context, tx *sql.Tx, discordUserID snowflake.ID, battlenetID uint64, battleTag, action string) error {
	_, err := tx.ExecContext(ctx,
		"INSERT INTO battlenet_link_history (discord_user_id, battlenet_id, battletag, action) VALUES (?, ?, ?, ?)",
		discordUserID, battlenetID, battleTag, action,
	)
	return err
}

// dropOrphanedPrimaries removes main-character choices whose character is
// no longer on the choosing user's account (deleted in game, or moved to
// another user).
func dropOrphanedPrimaries(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `
		DELETE p FROM wow_primary_characters p
		LEFT JOIN wow_characters c
		ON c.flavour = p.flavour AND c.region = p.region AND c.character_id = p.character_id AND c.discord_user_id = p.discord_user_id
		WHERE c.character_id IS NULL`)
	return err
}
