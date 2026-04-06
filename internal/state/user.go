package state

import (
	"sync/atomic"
	"time"
)

// UserID is a process-local opaque identifier for a connected user.
// It is *not* the IRC nickname; it stays stable across nick changes
// so connections, channel memberships, and audit events can refer to
// "the same user" without re-keying everything when the nick changes.
type UserID uint64

// User is the in-memory record for one connected client.
//
// Fields are exported because handlers in internal/server need to
// read and (under the World mutex) mutate them. Locking discipline:
// the User struct is owned by the World; callers acquire the World's
// lock before reading or mutating fields. Once channels land in M2,
// per-user channel membership will be a separate, more granular
// concern.
type User struct {
	ID         UserID
	Nick       string // canonical (display) form
	User       string // ident, set by USER command
	Host       string // peer host or cloak
	Realname   string // GECOS, the trailing param of USER
	Modes      string // sorted user mode chars (e.g. "iow")
	Registered bool   // true once both NICK and USER have completed
	ConnectAt  time.Time
}

// Hostmask renders the user as "nick!user@host", the canonical form
// used for prefixes in messages originating from this user.
func (u *User) Hostmask() string {
	user := u.User
	if user == "" {
		user = u.Nick
	}
	host := u.Host
	if host == "" {
		host = "unknown"
	}
	return u.Nick + "!" + user + "@" + host
}

// nextUserID is the source for [World.AddUser]. It is process-global
// because UserID is process-local; if/when we shard World, this can
// move into the shard.
var nextUserID atomic.Uint64

func newUserID() UserID {
	return UserID(nextUserID.Add(1))
}
