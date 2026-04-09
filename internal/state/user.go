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

	// HomeServer is the name of the ircat node that owns this user.
	// Empty means "local to this node". Non-empty users are
	// remote: the local node knows about them via federation burst
	// and forwards directed messages back through the owning link
	// rather than delivering to a local Conn.
	HomeServer string

	// TS is the user's timestamp for collision resolution per
	// RFC 2813 §5.2. For local users it is set at registration
	// completion (unix nanos); for remote users it is carried
	// across the federation link in the burst NICK form. On a
	// nick collision the lower TS wins — the higher-TS user is
	// killed. The TS is preserved across NICK changes (a rename
	// does not reset it) so the original-registration time
	// remains the tiebreaker.
	TS int64

	// Away is the user's away message per RFC 2812 §4.1. Empty
	// means "not away". When non-empty the server emits 301
	// RPL_AWAY in response to every PRIVMSG aimed at this user
	// (channel messages do not trigger the 301 — only direct
	// PRIVMSGs).
	Away string

	// Service is true when this connection registered via SERVICE
	// (RFC 2812 §3.1.6) instead of NICK/USER. Services have a few
	// behavioural differences: they receive SQUERY rather than
	// PRIVMSG, do not appear in NAMES/WHOIS for normal channels,
	// and are listed via SERVLIST. ServiceType and
	// ServiceDistribution carry the registration parameters for
	// SERVLIST replies.
	Service             bool
	ServiceType         string
	ServiceDistribution string
}

// IsRemote reports whether the user lives on a different node.
func (u *User) IsRemote() bool { return u.HomeServer != "" }

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
