package state

import (
	"sort"
	"strings"
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

	name       string // canonical (display) name, e.g. "#Foo"
	createdAt  time.Time
	topic      string
	topicSetBy string // hostmask of the user who set the topic
	topicSetAt time.Time
	key        string
	limit      int
	modes      channelModes
	members    map[UserID]Membership
	bans       map[string]time.Time // mask -> set time
	exceptions map[string]time.Time // ban exception masks
	invexes    map[string]time.Time // invite exception masks
	invites    map[UserID]struct{}  // pending one-shot invites for +i bypass
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

// TS returns the channel's RFC 2813 §5.2 timestamp as unix
// nanoseconds. Sourced from CreatedAt; v1.1 does not store a
// separate TS field because the channel-creation time is the
// canonical anchor for the lower-TS-wins tiebreaker.
func (c *Channel) TS() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.createdAt.UnixNano()
}

// AdoptOlderTS lowers the channel's createdAt to ts iff ts is
// strictly older (smaller) than the current timestamp. Used by
// the federation receiver: when a peer bursts a channel that
// already exists locally and the peer's TS is older, we adopt
// the older TS so subsequent collision arithmetic on every node
// converges on the same anchor.
//
// Returns true if the anchor was lowered. Callers that need
// the matching membership reset (per RFC 2813 §5.2) should
// follow up with ResetMembershipFlags.
func (c *Channel) AdoptOlderTS(ts int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ts <= 0 {
		return false
	}
	if ts >= c.createdAt.UnixNano() {
		return false
	}
	c.createdAt = time.Unix(0, ts)
	return true
}

// ResetMembershipFlags strips every per-member flag (op,
// voice) on every member of the channel, leaving the membership
// set itself intact. Used by the federation receiver after an
// AdoptOlderTS reset: per RFC 2813 §5.2, when the peer wins
// the channel TS race we discard our local op/voice state and
// accept the peer's bursted flags as authoritative.
//
// This is a hammer rather than a scalpel — every flag goes —
// because the local node has no way to tell which flags were
// granted under the older or newer TS regime.
func (c *Channel) ResetMembershipFlags() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.members {
		c.members[id] = 0
	}
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

// AddMembership ORs flags onto the member without dropping existing
// ones. Returns the new membership and true on success.
func (c *Channel) AddMembership(id UserID, flags Membership) (Membership, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.members[id]
	if !ok {
		return 0, false
	}
	c.members[id] = cur | flags
	return c.members[id], true
}

// RemoveMembership clears flags from the member. Returns the new
// membership and true on success.
func (c *Channel) RemoveMembership(id UserID, flags Membership) (Membership, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.members[id]
	if !ok {
		return 0, false
	}
	c.members[id] = cur &^ flags
	return c.members[id], true
}

// Boolean mode setters. Each takes the canonical mode char and a
// boolean. Returns true if the mode actually changed (so the caller
// knows whether to broadcast).
func (c *Channel) SetBoolMode(mode byte, on bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	target := c.boolModePtr(mode)
	if target == nil || *target == on {
		return false
	}
	*target = on
	return true
}

func (c *Channel) boolModePtr(mode byte) *bool {
	switch mode {
	case 'i':
		return &c.modes.inviteOnly
	case 'm':
		return &c.modes.moderated
	case 'n':
		return &c.modes.noExternal
	case 'p':
		return &c.modes.private
	case 's':
		return &c.modes.secret
	case 't':
		return &c.modes.topicLocked
	}
	return nil
}

// SetKey installs (or clears) the channel key. Returns true if the
// key actually changed.
func (c *Channel) SetKey(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.key == key {
		return false
	}
	c.key = key
	return true
}

// Key returns the current channel key, or "" if none is set.
func (c *Channel) Key() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.key
}

// SetLimit installs (or clears) the channel user limit. limit <= 0
// disables the limit. Returns true if the limit changed.
func (c *Channel) SetLimit(limit int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if limit < 0 {
		limit = 0
	}
	if c.limit == limit {
		return false
	}
	c.limit = limit
	return true
}

// Limit returns the current user limit, or 0 if no limit is set.
func (c *Channel) Limit() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.limit
}

// AddBan adds mask to the ban list. Returns true if the mask was
// new.
func (c *Channel) AddBan(mask string, at time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.bans[mask]; ok {
		return false
	}
	c.bans[mask] = at
	return true
}

// RemoveBan drops mask from the ban list. Returns true if the mask
// was present.
func (c *Channel) RemoveBan(mask string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.bans[mask]; !ok {
		return false
	}
	delete(c.bans, mask)
	return true
}

// Bans returns a snapshot of the ban list as (mask, set time)
// pairs in arbitrary order.
func (c *Channel) Bans() map[string]time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]time.Time, len(c.bans))
	for k, v := range c.bans {
		out[k] = v
	}
	return out
}

// MatchesBan reports whether hostmask matches any of the channel's
// bans. The match algorithm is the simple IRC glob: '*' matches any
// run of characters, '?' matches a single character, everything
// else literal and case-insensitive under the world's case mapping.
func (c *Channel) MatchesBan(hostmask string, fold func(string) string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	folded := fold(hostmask)
	for mask := range c.bans {
		if globMatch(fold(mask), folded) {
			return true
		}
	}
	return false
}

// AddException adds mask to the +e ban-exception list. Hosts whose
// hostmask matches an exception are NOT considered banned even if
// they also match a +b mask. Returns true if the mask was new.
func (c *Channel) AddException(mask string, at time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.exceptions[mask]; ok {
		return false
	}
	c.exceptions[mask] = at
	return true
}

// RemoveException drops mask from the +e ban-exception list.
func (c *Channel) RemoveException(mask string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.exceptions[mask]; !ok {
		return false
	}
	delete(c.exceptions, mask)
	return true
}

// Exceptions returns a snapshot of the +e exception list.
func (c *Channel) Exceptions() map[string]time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]time.Time, len(c.exceptions))
	for k, v := range c.exceptions {
		out[k] = v
	}
	return out
}

// MatchesException reports whether hostmask matches any +e mask.
func (c *Channel) MatchesException(hostmask string, fold func(string) string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	folded := fold(hostmask)
	for mask := range c.exceptions {
		if globMatch(fold(mask), folded) {
			return true
		}
	}
	return false
}

// AddInvex adds mask to the +I invite-exception list. Hosts whose
// hostmask matches an invex mask are allowed past +i without an
// explicit per-user invite. Returns true if the mask was new.
func (c *Channel) AddInvex(mask string, at time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.invexes[mask]; ok {
		return false
	}
	c.invexes[mask] = at
	return true
}

// RemoveInvex drops mask from the +I invite-exception list.
func (c *Channel) RemoveInvex(mask string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.invexes[mask]; !ok {
		return false
	}
	delete(c.invexes, mask)
	return true
}

// Invexes returns a snapshot of the +I invite-exception list.
func (c *Channel) Invexes() map[string]time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]time.Time, len(c.invexes))
	for k, v := range c.invexes {
		out[k] = v
	}
	return out
}

// MatchesInvex reports whether hostmask matches any +I mask.
func (c *Channel) MatchesInvex(hostmask string, fold func(string) string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	folded := fold(hostmask)
	for mask := range c.invexes {
		if globMatch(fold(mask), folded) {
			return true
		}
	}
	return false
}

// AddInvite records that a user is allowed to bypass +i for one
// JOIN. The invite is consumed by [ConsumeInvite] when the matching
// JOIN arrives. Returns true if the invite was new.
func (c *Channel) AddInvite(id UserID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.invites == nil {
		c.invites = make(map[UserID]struct{})
	}
	if _, ok := c.invites[id]; ok {
		return false
	}
	c.invites[id] = struct{}{}
	return true
}

// ConsumeInvite checks whether id has a pending invite and removes
// it if so. Returns true if an invite was consumed.
func (c *Channel) ConsumeInvite(id UserID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.invites[id]; !ok {
		return false
	}
	delete(c.invites, id)
	return true
}

// GlobMatchHost is the public form of [globMatch], used outside
// this package for things like operator host_mask matching.
// Comparison is case-insensitive (operators routinely write masks
// in upper or lower case interchangeably).
func GlobMatchHost(pattern, s string) bool {
	return globMatch(strings.ToLower(pattern), strings.ToLower(s))
}

// globMatch implements the simple IRC glob algorithm with '*' and
// '?' wildcards. Returns true if pattern matches s end-to-end.
func globMatch(pattern, s string) bool {
	pi, si := 0, 0
	starP, starS := -1, -1
	for si < len(s) {
		switch {
		case pi < len(pattern) && (pattern[pi] == s[si] || pattern[pi] == '?'):
			pi++
			si++
		case pi < len(pattern) && pattern[pi] == '*':
			starP = pi
			starS = si
			pi++
		case starP >= 0:
			pi = starP + 1
			starS++
			si = starS
		default:
			return false
		}
	}
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
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

// RestoreState writes persisted channel state into a fresh
// (empty-membership) channel. Used by the server boot path to
// hydrate channels from PersistentChannelStore. Members are NOT
// restored — they reconnect on their own.
func (c *Channel) RestoreState(topic, topicSetBy string, topicSetAt time.Time, modeWord, key string, limit int, bans map[string]time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.topic = topic
	c.topicSetBy = topicSetBy
	c.topicSetAt = topicSetAt
	c.key = key
	c.limit = limit
	// Reset boolean modes then apply each char from the mode word.
	c.modes = channelModes{}
	for i := 0; i < len(modeWord); i++ {
		switch modeWord[i] {
		case '+':
			continue
		case 'i':
			c.modes.inviteOnly = true
		case 'm':
			c.modes.moderated = true
		case 'n':
			c.modes.noExternal = true
		case 'p':
			c.modes.private = true
		case 's':
			c.modes.secret = true
		case 't':
			c.modes.topicLocked = true
		}
	}
	c.bans = make(map[string]time.Time, len(bans))
	for k, v := range bans {
		c.bans[k] = v
	}
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
