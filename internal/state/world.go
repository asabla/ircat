package state

import (
	"errors"
	"sync"
	"time"
)

// Errors returned by World mutators. Each is comparable so callers
// can wrap them with context and still distinguish in tests.
var (
	// ErrNickInUse is returned by AddUser/RenameUser when the
	// requested nickname (under the case mapping) is already taken.
	ErrNickInUse = errors.New("state: nickname in use")
	// ErrNoSuchUser is returned when an operation refers to a UserID
	// or nickname that does not exist.
	ErrNoSuchUser = errors.New("state: no such user")
)

// World is the authoritative in-memory state of one ircat node.
//
// In M1 it only tracks users; channels land in M2, federation
// routing in M7. The lock is a single RWMutex for now: simpler than
// sharding and the hot path (per-connection command handling) does
// most of its work outside the lock anyway. We benchmark and shard
// later if it shows up in profiles.
type World struct {
	mu      sync.RWMutex
	mapping CaseMapping

	// byNick maps the case-folded nickname to the User. The User's
	// Nick field carries the original casing for display.
	byNick map[string]*User
	// byID is the secondary index used by command handlers that hold
	// a UserID for the connection they own.
	byID map[UserID]*User

	// now is the time source. Tests can swap it; production uses
	// time.Now.
	now func() time.Time
}

// Option configures a [World] at construction time.
type Option func(*World)

// WithCaseMapping selects the case-folding algorithm. Defaults to
// [CaseMappingRFC1459].
func WithCaseMapping(m CaseMapping) Option {
	return func(w *World) { w.mapping = m }
}

// WithClock overrides the time source. Tests use this to make
// connection timestamps deterministic; production never sets it.
func WithClock(now func() time.Time) Option {
	return func(w *World) { w.now = now }
}

// NewWorld constructs an empty World.
func NewWorld(opts ...Option) *World {
	w := &World{
		mapping: CaseMappingRFC1459,
		byNick:  make(map[string]*User),
		byID:    make(map[UserID]*User),
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// CaseMapping returns the world's case-folding algorithm.
func (w *World) CaseMapping() CaseMapping { return w.mapping }

// UserCount returns the number of users currently registered with
// the world. Cheap; no allocation.
func (w *World) UserCount() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.byID)
}

// FindByNick returns the user matching nick under the case mapping,
// or nil if no such user exists.
func (w *World) FindByNick(nick string) *User {
	if nick == "" {
		return nil
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.byNick[w.mapping.Fold(nick)]
}

// FindByID returns the user with the given ID, or nil.
func (w *World) FindByID(id UserID) *User {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.byID[id]
}

// AddUser inserts a user under the requested nickname. The supplied
// User must not already have an ID; AddUser allocates one.
//
// Returns [ErrNickInUse] if the nickname is taken.
func (w *World) AddUser(u *User) (UserID, error) {
	if u == nil || u.Nick == "" {
		return 0, errors.New("state: AddUser requires a non-empty Nick")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	key := w.mapping.Fold(u.Nick)
	if _, exists := w.byNick[key]; exists {
		return 0, ErrNickInUse
	}
	u.ID = newUserID()
	if u.ConnectAt.IsZero() {
		u.ConnectAt = w.now()
	}
	w.byNick[key] = u
	w.byID[u.ID] = u
	return u.ID, nil
}

// RemoveUser drops the user with the given ID. Returns false if no
// such user exists.
func (w *World) RemoveUser(id UserID) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	u, ok := w.byID[id]
	if !ok {
		return false
	}
	delete(w.byID, id)
	delete(w.byNick, w.mapping.Fold(u.Nick))
	return true
}

// RenameUser changes the nickname of the user identified by id.
// Returns [ErrNickInUse] if newNick (under the mapping) is taken by
// a *different* user, or [ErrNoSuchUser] if id is unknown. Renaming
// to a folded form that already maps to the same user (case-only
// change) is allowed.
func (w *World) RenameUser(id UserID, newNick string) error {
	if newNick == "" {
		return errors.New("state: RenameUser requires a non-empty nick")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	u, ok := w.byID[id]
	if !ok {
		return ErrNoSuchUser
	}
	oldKey := w.mapping.Fold(u.Nick)
	newKey := w.mapping.Fold(newNick)
	if oldKey == newKey {
		// Case-only change; just update the display nick.
		u.Nick = newNick
		return nil
	}
	if _, taken := w.byNick[newKey]; taken {
		return ErrNickInUse
	}
	delete(w.byNick, oldKey)
	u.Nick = newNick
	w.byNick[newKey] = u
	return nil
}

// Snapshot returns a copy of all users currently in the world. The
// returned slice is detached from the world's storage and is safe to
// iterate without holding any locks. Use sparingly — it allocates.
func (w *World) Snapshot() []User {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]User, 0, len(w.byID))
	for _, u := range w.byID {
		out = append(out, *u)
	}
	return out
}
