package api

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
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

	stateKeyPrefix        = "bxt:oauth:state:"
	sessionKeyPrefix      = "bxt:session:"
	userSessionsKeyPrefix = "bxt:user-sessions:"
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
// Redis doesn't give out usable session cookies. Session data (which holds
// the user's Discord tokens) is encrypted with AES-GCM under a key derived
// from the Discord client secret, so a Redis dump or a snooping Redis user
// doesn't give out Discord tokens either. Rotating the client secret logs
// everyone out, which is what you'd want after a leak.
//
// Each user's session keys are also kept in a set, so "log out everywhere"
// can find them.
type sessionStore struct {
	rdb  *redis.Client
	aead cipher.AEAD
}

// newSessionStore derives the session encryption key from secret (the
// Discord client secret).
func newSessionStore(rdb *redis.Client, secret string) (*sessionStore, error) {
	key, err := hkdf.Key(sha256.New, []byte(secret), nil, "bxt api session encryption v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &sessionStore{rdb: rdb, aead: aead}, nil
}

func sessionKey(id string) string {
	sum := sha256.Sum256([]byte(id))
	return sessionKeyPrefix + hex.EncodeToString(sum[:])
}

func userSessionsKey(userID snowflake.ID) string {
	return userSessionsKeyPrefix + userID.String()
}

// seal encrypts a session for storage under key (bound to it, so a value
// can't be moved to another session's key).
func (st *sessionStore) seal(key string, plaintext []byte) []byte {
	nonce := make([]byte, st.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return st.aead.Seal(nonce, nonce, plaintext, []byte(key))
}

func (st *sessionStore) open(key string, sealed []byte) ([]byte, error) {
	n := st.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("sealed session too short")
	}
	return st.aead.Open(nil, sealed[:n], sealed[n:], []byte(key))
}

// create stores s and returns the new session's ID (the cookie value).
func (st *sessionStore) create(ctx context.Context, s session) (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	id := randomToken(32)
	key := sessionKey(id)
	ttl := sessionTTL
	if until := time.Until(s.OAuth.Expiration); until > 0 && until < ttl {
		ttl = until
	}
	pipe := st.rdb.TxPipeline()
	pipe.Set(ctx, key, st.seal(key, b), ttl)
	pipe.SAdd(ctx, userSessionsKey(s.UserID), key)
	pipe.Expire(ctx, userSessionsKey(s.UserID), sessionTTL)
	if _, err := pipe.Exec(ctx); err != nil {
		return "", err
	}
	return id, nil
}

// get returns the session for id, or ok = false if there is none.
func (st *sessionStore) get(ctx context.Context, id string) (s session, ok bool, err error) {
	return st.load(ctx, sessionKey(id))
}

// load reads the session stored under key. A value that can't be decrypted
// (written before encryption, or under an old client secret) is deleted and
// treated as logged out.
func (st *sessionStore) load(ctx context.Context, key string) (s session, ok bool, err error) {
	b, err := st.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return session{}, false, nil
	}
	if err != nil {
		return session{}, false, err
	}
	plain, err := st.open(key, b)
	if err == nil {
		err = json.Unmarshal(plain, &s)
	}
	if err != nil {
		slog.Info("api: dropping unreadable session", "err", err)
		_ = st.rdb.Del(ctx, key).Err()
		return session{}, false, nil
	}
	return s, true, nil
}

// delete ends the session with this id.
func (st *sessionStore) delete(ctx context.Context, id string) error {
	_, _, err := st.remove(ctx, id)
	return err
}

// remove ends the session with this id and returns what it held (ok =
// false if there was none), so its Discord tokens can be revoked.
func (st *sessionStore) remove(ctx context.Context, id string) (session, bool, error) {
	key := sessionKey(id)
	s, ok, err := st.load(ctx, key)
	if err != nil {
		return session{}, false, err
	}
	pipe := st.rdb.TxPipeline()
	pipe.Del(ctx, key)
	if ok {
		pipe.SRem(ctx, userSessionsKey(s.UserID), key)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return session{}, false, err
	}
	return s, ok, nil
}

// removeAll ends every session of the user and returns them.
func (st *sessionStore) removeAll(ctx context.Context, userID snowflake.ID) ([]session, error) {
	keys, err := st.rdb.SMembers(ctx, userSessionsKey(userID)).Result()
	if err != nil {
		return nil, err
	}
	var out []session
	for _, key := range keys {
		if s, ok, err := st.load(ctx, key); err == nil && ok {
			out = append(out, s)
		}
	}
	if _, err := st.rdb.Del(ctx, append(keys, userSessionsKey(userID))...).Result(); err != nil {
		return nil, err
	}
	return out, nil
}
