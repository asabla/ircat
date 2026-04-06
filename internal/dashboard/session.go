package dashboard

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// session is the small "an operator is logged in" record carried in
// a signed cookie. We do not maintain a server-side session table —
// the cookie is the session, signed with an in-memory HMAC key
// that's regenerated on every server start. The trade-off: a
// restart logs every operator out, which is acceptable for M4.
type session struct {
	Operator string
	Expires  time.Time
}

// sessionStore is the small wrapper around the HMAC key plus the
// cookie name and lifetime. New initializes the key from
// crypto/rand.
type sessionStore struct {
	key        []byte
	cookieName string
	maxAge     time.Duration
	secure     bool
}

func newSessionStore(cookieName string, maxAge time.Duration, secure bool) (*sessionStore, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("session key: %w", err)
	}
	if cookieName == "" {
		cookieName = "ircat_session"
	}
	if maxAge <= 0 {
		maxAge = 24 * time.Hour
	}
	return &sessionStore{
		key:        key,
		cookieName: cookieName,
		maxAge:     maxAge,
		secure:     secure,
	}, nil
}

// issue writes a signed session cookie onto w for the given operator.
func (s *sessionStore) issue(w http.ResponseWriter, operator string) {
	expires := time.Now().Add(s.maxAge)
	value := s.encode(session{Operator: operator, Expires: expires})
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// clear writes an expired cookie of the same name, telling the
// browser to drop it.
func (s *sessionStore) clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// extract pulls a verified session out of the request. Returns nil
// when no valid cookie is present (caller treats as "not logged in").
func (s *sessionStore) extract(r *http.Request) *session {
	c, err := r.Cookie(s.cookieName)
	if err != nil {
		return nil
	}
	sess, err := s.decode(c.Value)
	if err != nil {
		return nil
	}
	if time.Now().After(sess.Expires) {
		return nil
	}
	return sess
}

// encode produces "<operator>|<expires_unix>|<base64-hmac>".
func (s *sessionStore) encode(sess session) string {
	body := sess.Operator + "|" + strconv.FormatInt(sess.Expires.Unix(), 10)
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(body))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return body + "|" + sig
}

// decode reverses encode and verifies the HMAC in constant time.
func (s *sessionStore) decode(value string) (*session, error) {
	parts := strings.SplitN(value, "|", 3)
	if len(parts) != 3 {
		return nil, errors.New("session: malformed cookie")
	}
	body := parts[0] + "|" + parts[1]
	mac := hmac.New(sha256.New, s.key)
	mac.Write([]byte(body))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[2])) {
		return nil, errors.New("session: bad signature")
	}
	expiresUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("session: bad expiry: %w", err)
	}
	return &session{
		Operator: parts[0],
		Expires:  time.Unix(expiresUnix, 0),
	}, nil
}
