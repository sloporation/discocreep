// Package blizzard is a small client for the Battle.net APIs the bot uses:
// OAuth (user sign-in and app-only tokens) and World of Warcraft data (a
// user's characters, guild rosters, realm lists) for retail and Classic.
//
// It's shared by the web API (linking accounts) and the worker (guild sync),
// so it must not import internal/discord, internal/api or any feature.
package blizzard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultOAuthURL is Battle.net's OAuth host (all retail regions).
	DefaultOAuthURL = "https://oauth.battle.net"
	// DefaultAPIURL is the game data/profile API host; %s is the region.
	DefaultAPIURL = "https://%s.api.blizzard.com"
)

// Regions are the regions the APIs serve (China is separate and not supported).
var Regions = map[string]bool{"us": true, "eu": true, "kr": true, "tw": true}

// Flavour is a WoW game version. Each has its own API namespaces, and a
// character or guild ID is only unique within one flavour and region.
type Flavour string

const (
	// Retail is the current game.
	Retail Flavour = "retail"
	// Classic is Classic Progression (the current Classic expansion
	// re-release): namespaces *-classic-{region}.
	Classic Flavour = "classic"
	// ClassicEra is Classic Era, including Anniversary and Season of
	// Discovery realms: namespaces *-classic1x-{region}. Blizzard's Era
	// guild roster endpoint has returned 403 since late 2024.
	ClassicEra Flavour = "classic_era"
)

// Flavours lists every flavour, in display order.
var Flavours = []Flavour{Retail, Classic, ClassicEra}

// Valid reports whether f is a known flavour.
func (f Flavour) Valid() bool { return f == Retail || f == Classic || f == ClassicEra }

// Label is the flavour's display name.
func (f Flavour) Label() string {
	switch f {
	case Classic:
		return "Classic"
	case ClassicEra:
		return "Classic Era"
	default:
		return "Retail"
	}
}

// namespace builds an API namespace, e.g. ("profile", "us") → "profile-classic-us".
func (f Flavour) namespace(kind, region string) string {
	switch f {
	case Classic:
		return kind + "-classic-" + region
	case ClassicEra:
		return kind + "-classic1x-" + region
	default:
		return kind + "-" + region
	}
}

// ParseFlavours validates a comma-separated flavour list.
func ParseFlavours(s string) ([]Flavour, error) {
	var out []Flavour
	for _, p := range strings.Split(s, ",") {
		f := Flavour(strings.ToLower(strings.TrimSpace(p)))
		if f == "" {
			continue
		}
		if !f.Valid() {
			return nil, fmt.Errorf("unknown WoW flavour %q (use retail, classic, classic_era)", f)
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, errors.New("no WoW flavours configured")
	}
	return out, nil
}

// ErrNotFound is returned when Blizzard 404s: no WoW account in a region,
// or no such guild/realm.
var ErrNotFound = errors.New("not found")

// Client talks to Blizzard. Set the fields, or use New.
type Client struct {
	ClientID, ClientSecret string
	OAuthURL               string // DefaultOAuthURL unless testing
	APIURL                 string // DefaultAPIURL unless testing; %s is the region
	HTTP                   *http.Client

	mu          sync.Mutex
	appToken    string
	appTokenExp time.Time
}

// New returns a Client for the real Battle.net APIs.
func New(clientID, clientSecret string) *Client {
	return &Client{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		OAuthURL:     DefaultOAuthURL,
		APIURL:       DefaultAPIURL,
		HTTP:         &http.Client{Timeout: 15 * time.Second},
	}
}

// ParseRegions validates a comma-separated region list.
func ParseRegions(s string) ([]string, error) {
	var out []string
	for _, r := range strings.Split(s, ",") {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" {
			continue
		}
		if !Regions[r] {
			return nil, fmt.Errorf("unknown Battle.net region %q (use us, eu, kr, tw)", r)
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, errors.New("no Battle.net regions configured")
	}
	return out, nil
}

// --- User sign-in (authorization code flow) ------------------------------

// AuthorizeURL is where to send the browser to sign in (scopes: openid, wow.profile).
func (c *Client) AuthorizeURL(redirectURI, state string) string {
	return c.OAuthURL + "/authorize?" + url.Values{
		"response_type": {"code"},
		"client_id":     {c.ClientID},
		"redirect_uri":  {redirectURI},
		"scope":         {"openid wow.profile"},
		"state":         {state},
	}.Encode()
}

// Exchange trades an authorization code for a user access token (valid 24h,
// no refresh token).
func (c *Client) Exchange(ctx context.Context, code, redirectURI string) (string, error) {
	tok, _, err := c.token(ctx, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
	})
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	return tok, nil
}

// UserInfo returns the signed-in Battle.net account's ID and BattleTag.
func (c *Client) UserInfo(ctx context.Context, userToken string) (uint64, string, error) {
	var body struct {
		ID        uint64 `json:"id"`
		BattleTag string `json:"battletag"`
	}
	if err := c.get(ctx, c.OAuthURL+"/oauth/userinfo", userToken, &body); err != nil {
		return 0, "", fmt.Errorf("userinfo: %w", err)
	}
	if body.ID == 0 {
		return 0, "", errors.New("userinfo: no account id")
	}
	return body.ID, body.BattleTag, nil
}

// Character is a WoW character.
type Character struct {
	Flavour Flavour `json:"flavour"`
	Region  string  `json:"region"`
	// ID is only unique within a flavour and region.
	ID        uint64 `json:"character_id"`
	Name      string `json:"name"`
	RealmSlug string `json:"realm_slug"`
	RealmName string `json:"realm"`
	Level     int    `json:"level"`
	ClassID   int    `json:"class_id"`
	ClassName string `json:"class"`
	RaceName  string `json:"race"`
	Faction   string `json:"faction"` // ALLIANCE, HORDE, NEUTRAL
}

type named struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
	Slug string `json:"slug"`
	Type string `json:"type"`
}

// Characters reads the user's characters for each flavour from each region.
// A region where they have no account for a flavour (404) is skipped. If
// any other request for a flavour fails, that whole flavour is left out and
// listed in failed, so callers can keep what they had stored for it rather
// than mistake an outage for "no characters". err is set only if every
// flavour failed.
func (c *Client) Characters(ctx context.Context, userToken string, regions []string, flavours []Flavour) (chars []Character, failed map[Flavour]error, err error) {
	failed = map[Flavour]error{}
	for _, f := range flavours {
		fc, ferr := c.flavourCharacters(ctx, userToken, regions, f)
		if ferr != nil {
			failed[f] = ferr
			continue
		}
		chars = append(chars, fc...)
	}
	if len(failed) == len(flavours) {
		for f, e := range failed {
			return nil, failed, fmt.Errorf("%s characters: %w", f, e)
		}
	}
	SortCharacters(chars)
	return chars, failed, nil
}

func (c *Client) flavourCharacters(ctx context.Context, userToken string, regions []string, f Flavour) ([]Character, error) {
	var out []Character
	for _, region := range regions {
		var body struct {
			WowAccounts []struct {
				Characters []struct {
					Name          string `json:"name"`
					ID            uint64 `json:"id"`
					Level         int    `json:"level"`
					Realm         named  `json:"realm"`
					PlayableClass named  `json:"playable_class"`
					PlayableRace  named  `json:"playable_race"`
					Faction       named  `json:"faction"`
				} `json:"characters"`
			} `json:"wow_accounts"`
		}
		err := c.get(ctx, c.regionURL(region, "/profile/user/wow", f.namespace("profile", region)), userToken, &body)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", region, err)
		}
		for _, acct := range body.WowAccounts {
			for _, ch := range acct.Characters {
				out = append(out, Character{
					Flavour:   f,
					Region:    region,
					ID:        ch.ID,
					Name:      ch.Name,
					RealmSlug: ch.Realm.Slug,
					RealmName: ch.Realm.Name,
					Level:     ch.Level,
					ClassID:   ch.PlayableClass.ID,
					ClassName: ch.PlayableClass.Name,
					RaceName:  ch.PlayableRace.Name,
					Faction:   ch.Faction.Type,
				})
			}
		}
	}
	return out, nil
}

// SortCharacters orders by level (highest first), then name.
func SortCharacters(chars []Character) {
	sort.SliceStable(chars, func(i, j int) bool {
		if chars[i].Level != chars[j].Level {
			return chars[i].Level > chars[j].Level
		}
		return chars[i].Name < chars[j].Name
	})
}

// --- App-only data (client credentials flow) -----------------------------

// Realm is a retail realm.
type Realm struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// Realms lists a flavour's realms in a region, sorted by name.
func (c *Client) Realms(ctx context.Context, f Flavour, region string) ([]Realm, error) {
	tok, err := c.AppToken(ctx)
	if err != nil {
		return nil, err
	}
	var body struct {
		Realms []Realm `json:"realms"`
	}
	if err := c.get(ctx, c.regionURL(region, "/data/wow/realm/index", f.namespace("dynamic", region)), tok, &body); err != nil {
		return nil, fmt.Errorf("%s realms: %w", region, err)
	}
	sort.Slice(body.Realms, func(i, j int) bool { return body.Realms[i].Name < body.Realms[j].Name })
	return body.Realms, nil
}

// RosterMember is one character in a guild roster.
type RosterMember struct {
	CharacterID uint64 `json:"character_id"`
	Name        string `json:"name"`
	RealmSlug   string `json:"realm_slug"`
	// Rank is the guild rank number: 0 is the Guild Master. Blizzard's API
	// doesn't expose rank names.
	Rank int `json:"rank"`
}

// Roster is a guild and its members.
type Roster struct {
	GuildName string         `json:"guild_name"`
	RealmName string         `json:"realm_name"`
	RealmSlug string         `json:"realm_slug"`
	Members   []RosterMember `json:"members"`
}

// GuildSlug turns a guild name into its API slug: lowercase, spaces as dashes.
func GuildSlug(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), " ", "-")
}

// GuildRoster returns a guild's roster. ErrNotFound if there's no such guild.
func (c *Client) GuildRoster(ctx context.Context, f Flavour, region, realmSlug, guildSlug string) (*Roster, error) {
	tok, err := c.AppToken(ctx)
	if err != nil {
		return nil, err
	}
	var body struct {
		Guild struct {
			Name  string `json:"name"`
			Realm named  `json:"realm"`
		} `json:"guild"`
		Members []struct {
			Character struct {
				Name  string `json:"name"`
				ID    uint64 `json:"id"`
				Realm named  `json:"realm"`
			} `json:"character"`
			Rank int `json:"rank"`
		} `json:"members"`
	}
	path := "/data/wow/guild/" + url.PathEscape(realmSlug) + "/" + url.PathEscape(guildSlug) + "/roster"
	if err := c.get(ctx, c.regionURL(region, path, f.namespace("profile", region)), tok, &body); err != nil {
		return nil, err
	}
	r := &Roster{GuildName: body.Guild.Name, RealmName: body.Guild.Realm.Name, RealmSlug: body.Guild.Realm.Slug}
	for _, m := range body.Members {
		r.Members = append(r.Members, RosterMember{
			CharacterID: m.Character.ID,
			Name:        m.Character.Name,
			RealmSlug:   m.Character.Realm.Slug,
			Rank:        m.Rank,
		})
	}
	return r, nil
}

// AppToken returns an app-only access token, cached until shortly before it expires.
func (c *Client) AppToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.appToken != "" && time.Now().Before(c.appTokenExp) {
		return c.appToken, nil
	}
	tok, expiresIn, err := c.token(ctx, url.Values{"grant_type": {"client_credentials"}})
	if err != nil {
		return "", fmt.Errorf("app token: %w", err)
	}
	c.appToken = tok
	c.appTokenExp = time.Now().Add(time.Duration(expiresIn)*time.Second - 5*time.Minute)
	return tok, nil
}

// --- HTTP ----------------------------------------------------------------

func (c *Client) regionURL(region, path, namespace string) string {
	return fmt.Sprintf(c.APIURL, region) + path + "?" + url.Values{
		"namespace": {namespace},
		"locale":    {"en_US"},
	}.Encode()
}

// token POSTs to the token endpoint with client credentials (basic auth).
func (c *Client) token(ctx context.Context, form url.Values) (string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.OAuthURL+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.SetBasicAuth(c.ClientID, c.ClientSecret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := c.do(req, &body); err != nil {
		return "", 0, err
	}
	if body.AccessToken == "" {
		return "", 0, errors.New("no access token in response")
	}
	return body.AccessToken, body.ExpiresIn, nil
}

func (c *Client) get(ctx context.Context, u, bearer string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	return c.do(req, v)
}

// StatusError is a non-200 response other than 404.
type StatusError struct {
	Status int
	Body   string
}

func (e StatusError) Error() string { return fmt.Sprintf("status %d: %s", e.Status, e.Body) }

func (c *Client) do(req *http.Request, v any) error {
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	switch {
	case res.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case res.StatusCode != http.StatusOK:
		snippet := string(body)
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return StatusError{Status: res.StatusCode, Body: snippet}
	}
	return json.Unmarshal(body, v)
}
