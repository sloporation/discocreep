package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/disgoorg/disgo/oauth2"
	"github.com/disgoorg/snowflake/v2"
	"github.com/redis/go-redis/v9"
)

const (
	// stateTTL is how long a user has to complete Discord's consent screen.
	stateTTL = 10 * time.Minute
	// sessionTTL is how long a login lasts. Discord access tokens last 7
	// days, so a session never outlives the token it holds.
	sessionTTL = 7 * 24 * time.Hour

	stateKeyPrefix   = "bxt:oauth:state:"
	sessionKeyPrefix = "bxt:session:"
)

// randomToken returns n random bytes, base64url-encoded.
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// stateController stores OAuth2 states in Redis, so the callback can land on
// any API instance. It implements oauth2.StateController.
type stateController struct {
	rdb *redis.Client
}

var _ oauth2.StateController = (*stateController)(nil)

func (s *stateController) NewState(redirectURI string) string {
	state := randomToken(24)
	if err := s.rdb.Set(context.Background(), stateKeyPrefix+state, redirectURI, stateTTL).Err(); err != nil {
		slog.Error("api: save oauth state", "err", err)
	}
	return state
}

// UseState returns the redirect URI the state was issued for and deletes it,
// so each state works once. It returns "" if the state is unknown or expired.
func (s *stateController) UseState(state string) string {
	uri, err := s.rdb.GetDel(context.Background(), stateKeyPrefix+state).Result()
	if err != nil {
		if !errors.Is(err, redis.Nil) {
			slog.Error("api: use oauth state", "err", err)
		}
		return ""
	}
	return uri
}

// session is a logged-in user. It's stored in Redis, never sent to the
// browser: the browser only holds the random session ID in a cookie.
type session struct {
	UserID     snowflake.ID `json:"user_id"`
	Username   string       `json:"username"`
	GlobalName *string      `json:"global_name,omitempty"`
	AvatarURL  string       `json:"avatar_url"`
	// OAuth holds the user's Discord tokens, used to ask Discord about the
	// user (e.g. which guilds they're in) on their behalf.
	OAuth     oauth2.Session `json:"oauth"`
	CreatedAt time.Time      `json:"created_at"`
}

// sessionStore keeps sessions in Redis under a hash of their ID, so reading
// Redis doesn't give out usable session cookies.
type sessionStore struct {
	rdb *redis.Client
}

func sessionKey(id string) string {
	sum := sha256.Sum256([]byte(id))
	return sessionKeyPrefix + hex.EncodeToString(sum[:])
}

// create stores s and returns the new session's ID (the cookie value).
func (st *sessionStore) create(ctx context.Context, s session) (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	id := randomToken(32)
	ttl := sessionTTL
	if until := time.Until(s.OAuth.Expiration); until > 0 && until < ttl {
		ttl = until
	}
	if err := st.rdb.Set(ctx, sessionKey(id), b, ttl).Err(); err != nil {
		return "", err
	}
	return id, nil
}

// get returns the session for id, or ok = false if there is none.
func (st *sessionStore) get(ctx context.Context, id string) (s session, ok bool, err error) {
	b, err := st.rdb.Get(ctx, sessionKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return session{}, false, nil
	}
	if err != nil {
		return session{}, false, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return session{}, false, err
	}
	return s, true, nil
}

func (st *sessionStore) delete(ctx context.Context, id string) error {
	return st.rdb.Del(ctx, sessionKey(id)).Err()
}
