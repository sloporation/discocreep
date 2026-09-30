package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/rest"
	"github.com/disgoorg/snowflake/v2"

	"gitlab.com/jacxb/bots/bxt/go/internal/alerts"
	"gitlab.com/jacxb/bots/bxt/go/internal/blizzard"
	"gitlab.com/jacxb/bots/bxt/go/internal/database"
	"gitlab.com/jacxb/bots/bxt/go/internal/locks"
)

const (
	// syncInterval is how often each enabled server is synced without being asked.
	syncInterval = 15 * time.Minute
	// checkInterval is how often the worker looks for servers due a sync.
	checkInterval = time.Minute
	// syncLeaseTTL keeps a server's sync to one worker at a time.
	syncLeaseTTL = 5 * time.Minute
	// maxResultErrors caps the per-member errors kept in a result.
	maxResultErrors = 10
	// maxNickLength is Discord's nickname limit.
	maxNickLength = 32
)

// Syncer runs WoW guild sync for the servers this worker handles.
type Syncer struct {
	db      *database.DB
	alerter *alerts.Alerter
	locker  *locks.Locker
	bnet    *blizzard.Client
}

// NewSyncer returns a Syncer.
func NewSyncer(db *database.DB, alerter *alerts.Alerter, locker *locks.Locker, bnet *blizzard.Client) *Syncer {
	return &Syncer{db: db, alerter: alerter, locker: locker, bnet: bnet}
}

// Loop returns a start hook that syncs every due server this worker owns,
// checking every minute.
func (s *Syncer) Loop(client *bot.Client) func(ctx context.Context) {
	return func(ctx context.Context) {
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			s.runDue(ctx, client)
		}
	}
}

func (s *Syncer) runDue(ctx context.Context, client *bot.Client) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.guild_id FROM wow_sync_settings s
		WHERE s.enabled AND EXISTS (SELECT 1 FROM wow_sync_guilds g WHERE g.guild_id = s.guild_id)
		AND (s.last_synced_at IS NULL OR s.requested_at > s.last_synced_at OR s.last_synced_at < NOW() - INTERVAL ? SECOND)`,
		int(syncInterval.Seconds()),
	)
	if err != nil {
		slog.Error("wowSync: find due servers", "err", err)
		return
	}
	var due []snowflake.ID
	for rows.Next() {
		var id snowflake.ID
		if rows.Scan(&id) == nil {
			due = append(due, id)
		}
	}
	rows.Close()

	for _, guildID := range due {
		// Only servers in this worker's partitions (the ones in its cache).
		if _, ok := client.Caches.Guild(guildID); !ok {
			continue
		}
		lease, ok, err := s.locker.Acquire(ctx, fmt.Sprintf("wowsync:%s", guildID), syncLeaseTTL)
		if err != nil || !ok {
			continue
		}
		s.Sync(ctx, client, guildID)
		lease.Release()
	}
}

// Sync syncs one server now: reads each linked WoW guild's roster, then for
// each member with a main here, sets their mapped rank roles and (if
// enabled) their nickname.
func (s *Syncer) Sync(ctx context.Context, client *bot.Client, guildID snowflake.ID) {
	// Use the database's clock so requested_at comparisons line up.
	var started time.Time
	if err := s.db.QueryRowContext(ctx, "SELECT NOW()").Scan(&started); err != nil {
		slog.Error("wowSync: start", "guild_id", guildID, "err", err)
		return
	}

	settings, links, status, err := Load(ctx, s.db, guildID)
	if err != nil || len(links) == 0 {
		return
	}
	mapping, err := RankRoles(ctx, s.db, guildID)
	if err != nil {
		slog.Error("wowSync: load rank roles", "guild_id", guildID, "err", err)
		return
	}

	// Read every linked guild's roster (in parallel); remember which couldn't be read.
	ranks, guildErrors := s.readRosters(ctx, links)
	if len(ranks) == 0 {
		var reasons []string
		for _, r := range guildErrors {
			reasons = append(reasons, r)
		}
		slices.Sort(reasons)
		s.fail(ctx, client, guildID, started, status, strings.Join(reasons, "\n"))
		return
	}
	s.alertNewGuildErrors(client, guildID, status, guildErrors)

	// Which linked guilds grant each mapped role. A role is only removable
	// if every guild that grants it was read this run.
	linked := map[int64]bool{}
	for _, l := range links {
		linked[l.ID] = true
	}
	managed := map[snowflake.ID]bool{}
	removable := map[snowflake.ID]bool{}
	grantedBy := map[snowflake.ID][]int64{}
	for k, role := range mapping {
		if !linked[k.LinkID] {
			continue
		}
		managed[role] = true
		grantedBy[role] = append(grantedBy[role], k.LinkID)
	}
	for role, linkIDs := range grantedBy {
		ok := settings.RemoveRoles
		for _, id := range linkIDs {
			if _, read := ranks[id]; !read {
				ok = false
			}
		}
		removable[role] = ok
	}

	members, err := s.membersWithMains(ctx, guildID)
	if err != nil {
		slog.Error("wowSync: load mains", "guild_id", guildID, "err", err)
		return
	}
	// Roles the bot gave out (or adopted) and hasn't taken back. These are
	// taken back once no rank grants them any more, even if they're no
	// longer mapped or the member has cleared their mains.
	held, err := HeldRoles(ctx, s.db, guildID)
	if err != nil {
		slog.Error("wowSync: load role log", "guild_id", guildID, "err", err)
		return
	}
	users := make([]snowflake.ID, 0, len(members)+len(held))
	for u := range members {
		users = append(users, u)
	}
	for u := range held {
		if _, ok := members[u]; !ok {
			users = append(users, u)
		}
	}

	ownerID := guildOwner(client, guildID)
	result := Result{Linked: len(members), GuildErrors: guildErrors}
	var logEntries []roleLogEntry
	for _, userID := range users {
		mains, hasMains := members[userID]
		heldRoles := held[userID]

		// A main can be in any linked guild of its flavour and region.
		desired := map[snowflake.ID]bool{}
		why := map[snowflake.ID]string{}
		inAnyGuild := false
		for _, link := range links {
			main, ok := mains[link.Flavour]
			byChar, read := ranks[link.ID]
			if !ok || !read || main.region != link.Region {
				continue
			}
			rank, inGuild := byChar[main.characterID]
			if !inGuild {
				continue
			}
			inAnyGuild = true
			if role, ok := mapping[RankKey{LinkID: link.ID, Rank: rank}]; ok {
				desired[role] = true
				if why[role] == "" {
					why[role] = fmt.Sprintf("%s is rank %d in %s (%s)", main.characterName, rank, link.WowGuildName, link.Flavour.Label())
				}
			}
		}
		if inAnyGuild {
			result.InGuild++
		}

		// Which roles are in play for this member. Members without mains
		// only ever lose roles the bot gave them; everyone else also has
		// every mapped role in play. A held role no rank maps any more can
		// always be taken back (no roster decides it).
		memberManaged, memberRemovable := managed, removable
		if !hasMains {
			memberManaged, memberRemovable = map[snowflake.ID]bool{}, map[snowflake.ID]bool{}
			for r := range heldRoles {
				memberManaged[r], memberRemovable[r] = true, settings.RemoveRoles
			}
		} else if len(heldRoles) > 0 {
			memberManaged, memberRemovable = maps.Clone(managed), maps.Clone(removable)
			for r := range heldRoles {
				if !managed[r] {
					memberManaged[r], memberRemovable[r] = true, settings.RemoveRoles
				}
			}
		}

		member, err := getMember(client, guildID, userID)
		if err != nil {
			if isUnknownMember(err) { // left the server: their roles went with them
				for r := range heldRoles {
					logEntries = append(logEntries, roleLogEntry{userID, r, RoleRelease, "left the server"})
				}
			} else {
				result.addError(fmt.Sprintf("<@%s>: couldn't look up member: %v", userID, err))
			}
			continue
		}

		update, changed := plan(
			memberState{roles: member.RoleIDs, nick: member.Nick, isOwner: userID == ownerID},
			desired, memberManaged, memberRemovable, nickname(settings.NicknameMode, mains),
		)
		if changed {
			if _, err := client.Rest.UpdateMember(guildID, userID, update); err != nil {
				result.addError(fmt.Sprintf("<@%s>: %v", userID, err))
				continue
			}
			result.Updated++
		}
		after := member.RoleIDs
		if update.Roles != nil {
			after = *update.Roles
		}
		removeReason := func(r snowflake.ID) string {
			switch {
			case !hasMains:
				return "no main picked on this server"
			case !managed[r]:
				return "role is no longer mapped to a guild rank"
			default:
				return "their mains' guild ranks don't grant it"
			}
		}
		logEntries = append(logEntries, roleChanges(userID, member.RoleIDs, after, desired, heldRoles, why, removeReason)...)
	}
	if err := logRoles(ctx, s.db, guildID, logEntries); err != nil {
		// The changes are made; without the log, roles given this run won't
		// be taken back automatically if their mapping is removed.
		slog.Error("wowSync: write role log", "guild_id", guildID, "entries", len(logEntries), "err", err)
	}

	b, _ := json.Marshal(result)
	if _, err := s.db.ExecContext(ctx,
		"UPDATE wow_sync_settings SET last_synced_at = ?, last_result = ?, last_error = NULL WHERE guild_id = ?",
		started, string(b), guildID,
	); err != nil {
		slog.Error("wowSync: save result", "guild_id", guildID, "err", err)
	}
	slog.Info("wowSync: synced", "guild_id", guildID, "linked", result.Linked, "in_guild", result.InGuild,
		"updated", result.Updated, "errors", len(result.Errors), "guild_errors", len(guildErrors))
}

// maxParallelRosters bounds concurrent roster requests to Blizzard.
const maxParallelRosters = 4

// readRosters fetches every linked guild's roster in parallel. It returns
// character → rank per readable guild, and why each other guild couldn't
// be read, both keyed by link ID.
func (s *Syncer) readRosters(ctx context.Context, links []GuildLink) (map[int64]map[uint64]int, map[int64]string) {
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		sem    = make(chan struct{}, maxParallelRosters)
		ranks  = map[int64]map[uint64]int{}
		failed = map[int64]string{}
	)
	for _, link := range links {
		wg.Add(1)
		go func(link GuildLink) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			roster, err := s.bnet.GuildRoster(ctx, link.Flavour, link.Region, link.RealmSlug, link.WowGuildSlug)
			if err == nil && len(roster.Members) == 0 {
				err = errors.New("empty roster")
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed[link.ID] = rosterError(link, err)
				return
			}
			byChar := make(map[uint64]int, len(roster.Members))
			for _, m := range roster.Members {
				byChar[m.CharacterID] = m.Rank
			}
			ranks[link.ID] = byChar
		}(link)
	}
	wg.Wait()
	return ranks, failed
}

func rosterError(link GuildLink, err error) string {
	if errors.Is(err, blizzard.ErrNotFound) {
		return fmt.Sprintf("%s guild %s (%s, %s) wasn't found. It may have been renamed or moved realm; update it in the dashboard.",
			link.Flavour.Label(), link.WowGuildName, link.RealmName, strings.ToUpper(link.Region))
	}
	var se blizzard.StatusError
	if errors.As(err, &se) && se.Status == 403 && link.Flavour == blizzard.ClassicEra {
		return fmt.Sprintf("Blizzard refused the %s roster of %s (403). Blizzard's Classic Era guild API has been failing like this since late 2024; there's nothing to fix on our side.",
			link.Flavour.Label(), link.WowGuildName)
	}
	return fmt.Sprintf("Couldn't read the %s roster of %s from Blizzard: %v", link.Flavour.Label(), link.WowGuildName, err)
}

// fail records a sync that couldn't read any roster, and alerts admins the
// first time (not every 15 minutes while it stays broken).
func (s *Syncer) fail(ctx context.Context, client *bot.Client, guildID snowflake.ID, started time.Time, prev Status, reason string) {
	slog.Warn("wowSync: sync failed", "guild_id", guildID, "reason", reason)
	if _, err := s.db.ExecContext(ctx,
		"UPDATE wow_sync_settings SET last_synced_at = ?, last_error = ? WHERE guild_id = ?",
		started, reason, guildID,
	); err != nil {
		slog.Error("wowSync: save error", "guild_id", guildID, "err", err)
	}
	if prev.LastError == nil || *prev.LastError != reason {
		s.alerter.Send(client, guildID, alerts.Alert{
			Feature:     Feature,
			Title:       "WoW guild sync stopped",
			Description: reason + "\n\nNo roles or nicknames were changed.",
		})
	}
}

// alertNewGuildErrors alerts admins about linked guilds that couldn't be
// read this run but could last run (so a lasting problem alerts once).
func (s *Syncer) alertNewGuildErrors(client *bot.Client, guildID snowflake.ID, prev Status, now map[int64]string) {
	for id, reason := range now {
		if prev.LastResult != nil && prev.LastResult.GuildErrors[id] == reason {
			continue
		}
		s.alerter.Send(client, guildID, alerts.Alert{
			Feature:     Feature,
			Title:       "WoW guild sync: a guild couldn't be read",
			Description: reason + "\n\nThe other linked guilds still synced. Roles only this guild grants weren't removed.",
		})
	}
}

// main is a member's chosen character for one flavour.
type main struct {
	region        string
	characterID   uint64
	characterName string
}

// membersWithMains returns every member with at least one main here, by flavour.
func (s *Syncer) membersWithMains(ctx context.Context, guildID snowflake.ID) (map[snowflake.ID]map[blizzard.Flavour]main, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.discord_user_id, p.flavour, c.region, c.character_id, c.name
		FROM wow_primary_characters p
		JOIN wow_characters c ON c.flavour = p.flavour AND c.region = p.region AND c.character_id = p.character_id AND c.discord_user_id = p.discord_user_id
		WHERE p.guild_id = ?`,
		guildID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[snowflake.ID]map[blizzard.Flavour]main{}
	for rows.Next() {
		var user snowflake.ID
		var f blizzard.Flavour
		var m main
		if err := rows.Scan(&user, &f, &m.region, &m.characterID, &m.characterName); err != nil {
			return nil, err
		}
		if out[user] == nil {
			out[user] = map[blizzard.Flavour]main{}
		}
		out[user][f] = m
	}
	return out, rows.Err()
}

// nickname is the nickname a member should have, or nil to leave it alone.
func nickname(mode NicknameMode, mains map[blizzard.Flavour]main) *string {
	var name string
	switch {
	case mode == NicknameOff || mode == "":
		return nil
	case mode == NicknameCombined:
		var names []string
		for _, f := range blizzard.Flavours {
			if m, ok := mains[f]; ok {
				names = append(names, m.characterName)
			}
		}
		name = strings.Join(names, " / ")
	default:
		name = mains[blizzard.Flavour(mode)].characterName
	}
	if name == "" {
		return nil // no main for that flavour: leave the nickname as it is
	}
	for utf8.RuneCountInString(name) > maxNickLength {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return &name
}

// memberState is what plan needs to know about a member.
type memberState struct {
	roles   []snowflake.ID
	nick    *string
	isOwner bool
}

// plan works out a member's update:
//   - roles in desired that they lack are added;
//   - managed roles they have but shouldn't are removed, but only if removable;
//   - every role not in managed is left exactly as it is;
//   - the nickname is set to nick (nil = leave alone), never for the owner.
func plan(m memberState, desired, managed, removable map[snowflake.ID]bool, nick *string) (discord.MemberUpdate, bool) {
	var update discord.MemberUpdate
	changed := false

	roles := make([]snowflake.ID, 0, len(m.roles)+len(desired))
	for _, r := range m.roles {
		if managed[r] && !desired[r] && removable[r] {
			continue
		}
		roles = append(roles, r)
	}
	for r := range desired {
		if !slices.Contains(roles, r) {
			roles = append(roles, r)
		}
	}
	if !sameRoles(roles, m.roles) {
		update.Roles = &roles
		changed = true
	}

	// Discord doesn't let anyone change the server owner's nickname.
	if nick != nil && !m.isOwner && (m.nick == nil || *m.nick != *nick) {
		n := *nick
		update.Nick = &n
		changed = true
	}
	return update, changed
}

func sameRoles(a, b []snowflake.ID) bool {
	if len(a) != len(b) {
		return false
	}
	for _, r := range a {
		if !slices.Contains(b, r) {
			return false
		}
	}
	return true
}

// roleChanges is what goes in the role log for one member, given their
// roles before and after the sync: roles added and removed, roles they
// already had that their rank grants (adopted, if not already held), and
// held roles they no longer have that the sync didn't remove (released).
func roleChanges(userID snowflake.ID, before, after []snowflake.ID, desired, held map[snowflake.ID]bool,
	why map[snowflake.ID]string, removeReason func(snowflake.ID) string) []roleLogEntry {
	var out []roleLogEntry
	for _, r := range after {
		switch {
		case !slices.Contains(before, r):
			out = append(out, roleLogEntry{userID, r, RoleAdd, why[r]})
		case desired[r] && !held[r]:
			out = append(out, roleLogEntry{userID, r, RoleAdopt, why[r]})
		}
	}
	for _, r := range before {
		if !slices.Contains(after, r) {
			out = append(out, roleLogEntry{userID, r, RoleRemove, removeReason(r)})
		}
	}
	for r := range held {
		if !slices.Contains(before, r) && !slices.Contains(after, r) {
			out = append(out, roleLogEntry{userID, r, RoleRelease, "role was taken off outside the bot"})
		}
	}
	return out
}

func (r *Result) addError(msg string) {
	if len(r.Errors) < maxResultErrors {
		r.Errors = append(r.Errors, msg)
	}
}

func getMember(client *bot.Client, guildID, userID snowflake.ID) (discord.Member, error) {
	if m, ok := client.Caches.Member(guildID, userID); ok {
		return m, nil
	}
	m, err := client.Rest.GetMember(guildID, userID)
	if err != nil {
		return discord.Member{}, err
	}
	return *m, nil
}

func isUnknownMember(err error) bool {
	var rerr *rest.Error
	return errors.As(err, &rerr) && rerr.Code == rest.JSONErrorCodeUnknownMember
}

func guildOwner(client *bot.Client, guildID snowflake.ID) snowflake.ID {
	if g, ok := client.Caches.Guild(guildID); ok {
		return g.OwnerID
	}
	if g, err := client.Rest.GetGuild(guildID, false); err == nil {
		return g.OwnerID
	}
	return 0
}
