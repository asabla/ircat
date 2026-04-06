package state

import (
	"sort"
	"sync"
	"time"
)

// Membership flags carried by a channel member. They are independent
// bits so a single user can be both an op and voiced (op wins for
// display, both are advertised in WHO).
type Membership uint8

const (
	// MemberOp grants channel operator privileges (the "@" prefix).
	MemberOp Membership = 1 << iota
	// MemberVoice grants the right to speak in a +m channel
	// (the "+" prefix).
	MemberVoice
)

// IsOp reports whether m has operator status.
func (m Membership) IsOp() bool { return m&MemberOp != 0 }

// IsVoice reports whether m has voice status.
func (m Membership) IsVoice() bool { return m&MemberVoice != 0 }

// Prefix returns the highest visible status prefix character per
// RFC 2812 §3.5: "@" for ops, "+" for voiced, "" otherwise. Servers
// that advertise multi-prefix in CAP can render multiple, but ircat
// does not advertise it in M2.
func (m Membership) Prefix() string {
	switch {
	case m.IsOp():
		return "@"
	case m.IsVoice():
		return "+"
	}
	return ""
}

// Channel is the in-memory record for one IRC channel.
//
// Locking discipline: every Channel is protected by its own mutex.
// World holds the channel set under a separate lock; once you have
// a *Channel pointer you take its mutex for any field access. This
// keeps the World lock short and lets independent channels evolve
// in parallel — the design assumption is that channel-local writes
// (joins, parts, messages) outnumber whole-network operations.
type Channel struct {
	mu sync.RWMutex

	name        string // canonical (display) name, e.g. "#Foo"
	createdAt   time.Time
	topic       string
	topicSetBy  string // hostmask of the user who set the topic
	topicSetAt  time.Time
	key         string
	limit       int
	modes       channelModes
	members     map[UserID]Membership
	bans        map[string]time.Time // mask -> set time
	exceptions  map[string]time.Time // ban exception masks
	invexes     map[string]time.Time // invite exception masks
}

// channelModes carries the boolean channel modes. The list-form
// modes (b, e, I, o, v) live in the dedicated maps above.
type channelModes struct {
	inviteOnly  bool // +i
	moderated   bool // +m
	noExternal  bool // +n
	private     bool // +p
	secret      bool // +s
	topicLocked bool // +t
}

// newChannel constructs an empty channel with the given display name.
// Use [World.EnsureChannel] from the outside; this is package-private.
func newChannel(name string, now time.Time) *Channel {
	return &Channel{
		name:       name,
		createdAt:  now,
		modes:      channelModes{noExternal: true, topicLocked: true}, // RFC default for new channels
		members:    make(map[UserID]Membership),
		bans:       make(map[string]time.Time),
		exceptions: make(map[string]time.Time),
		invexes:    make(map[string]time.Time),
	}
}

// Name returns the channel's display name.
func (c *Channel) Name() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.name
}

// CreatedAt returns the channel creation time. Stable across the
// lifetime of the channel.
func (c *Channel) CreatedAt() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.createdAt
}

// Topic returns the current topic, the hostmask of whoever set it,
// and when. Empty topic returns ("", "", zero).
func (c *Channel) Topic() (text, setBy string, at time.Time) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.topic, c.topicSetBy, c.topicSetAt
}

// SetTopic stores a new topic. Caller is responsible for permission
// and length checks; this method does not enforce +t.
func (c *Channel) SetTopic(text, setBy string, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.topic = text
	c.topicSetBy = setBy
	c.topicSetAt = at
}

// MemberCount returns the number of members currently in the channel.
func (c *Channel) MemberCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.members)
}

// IsMember reports whether id is currently in the channel.
func (c *Channel) IsMember(id UserID) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.members[id]
	return ok
}

// Membership returns the membership flags for id, or 0 if id is not
// a member.
func (c *Channel) Membership(id UserID) Membership {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.members[id]
}

// MemberIDs returns a snapshot of the channel's members keyed by ID.
// The returned map is detached from the channel and safe to mutate.
func (c *Channel) MemberIDs() map[UserID]Membership {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[UserID]Membership, len(c.members))
	for id, m := range c.members {
		out[id] = m
	}
	return out
}

// addMember adds id with the given membership flags. The first user
// to join a fresh channel is automatically opped — that's how IRC
// channel ownership has worked since 1990.
//
// Returns true if id was newly added, false if it was already a
// member (in which case the membership is not modified).
func (c *Channel) addMember(id UserID, isFirst bool) (Membership, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.members[id]; ok {
		return existing, false
	}
	var m Membership
	if isFirst {
		m = MemberOp
	}
	c.members[id] = m
	return m, true
}

// removeMember drops id. Returns true if a member was removed.
func (c *Channel) removeMember(id UserID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.members[id]; !ok {
		return false
	}
	delete(c.members, id)
	return true
}

// SetMembership replaces the membership flags for an existing
// member. Returns false if id is not in the channel.
func (c *Channel) SetMembership(id UserID, m Membership) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.members[id]; !ok {
		return false
	}
	c.members[id] = m
	return true
}

// Modes returns a copy of the boolean mode flags. List-form modes
// (bans, exceptions) are exposed via separate methods.
func (c *Channel) Modes() (inviteOnly, moderated, noExternal, private, secret, topicLocked bool, key string, limit int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.modes.inviteOnly, c.modes.moderated, c.modes.noExternal, c.modes.private, c.modes.secret, c.modes.topicLocked, c.key, c.limit
}

// ModeString returns the channel modes in canonical "+ntk key"
// form, suitable for the parameter list of an RPL_CHANNELMODEIS
// (324) reply. Always returns a leading "+".
func (c *Channel) ModeString() (modes string, params []string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := []byte{'+'}
	if c.modes.noExternal {
		out = append(out, 'n')
	}
	if c.modes.topicLocked {
		out = append(out, 't')
	}
	if c.modes.moderated {
		out = append(out, 'm')
	}
	if c.modes.inviteOnly {
		out = append(out, 'i')
	}
	if c.modes.private {
		out = append(out, 'p')
	}
	if c.modes.secret {
		out = append(out, 's')
	}
	if c.key != "" {
		out = append(out, 'k')
	}
	if c.limit > 0 {
		out = append(out, 'l')
	}
	if c.key != "" {
		params = append(params, c.key)
	}
	if c.limit > 0 {
		params = append(params, itoaPositive(c.limit))
	}
	return string(out), params
}

// SortedMemberIDs returns a snapshot of the member IDs in a stable
// order. Used by NAMES so the output is deterministic across calls.
func (c *Channel) SortedMemberIDs() []UserID {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]UserID, 0, len(c.members))
	for id := range c.members {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func itoaPositive(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
