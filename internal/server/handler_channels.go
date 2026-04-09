package server

import (
	"strings"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
)

// handleJoin implements the JOIN command (RFC 2812 §3.2.1).
//
// Forms supported:
//
//	JOIN <channel>{,<channel>} [<key>{,<key>}]
//	JOIN 0     -- shorthand for "part every channel"
//
// For each channel:
//   - Validate the name.
//   - Verify channel modes (+i, +k, +l, +b are checked here).
//   - Add the user to the channel; the first joiner is opped.
//   - Broadcast the JOIN to every member, including the joiner so
//     they see it echoed.
//   - Send RPL_TOPIC + RPL_TOPICWHOTIME (or RPL_NOTOPIC) to the joiner.
//   - Send RPL_NAMREPLY + RPL_ENDOFNAMES to the joiner.
func (c *Conn) handleJoin(m *protocol.Message) {
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	if len(m.Params) < 1 {
		c.sendNeedMoreParams("JOIN")
		return
	}

	target := m.Params[0]
	if target == "0" {
		// "JOIN 0" parts every channel the user is in.
		for _, ch := range c.server.world.UserChannels(c.user.ID) {
			c.partOne(ch.Name(), "Left all channels")
		}
		return
	}

	channels := strings.Split(target, ",")
	keys := []string{}
	if len(m.Params) > 1 {
		keys = strings.Split(m.Params[1], ",")
	}

	for i, name := range channels {
		var key string
		if i < len(keys) {
			key = keys[i]
		}
		c.joinOne(name, key)
	}
}

// joinOne handles a single channel from a comma-separated JOIN list.
func (c *Conn) joinOne(name, key string) {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick

	if !validChannelName(name, c.server.cfg.Server.Limits.ChannelLength) {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOSUCHCHANNEL, name, "No such channel"))
		return
	}

	// Safe channels (RFC 2811 §2.1) need extra resolution before
	// the standard join machinery kicks in:
	//
	//   "!!short" — create a brand new safe channel by generating
	//               a fresh 5-character ID. The on-the-wire name
	//               becomes "!IDshort" from this point on.
	//   "!short"  — join the existing safe channel with this
	//               short suffix. We resolve "!short" against the
	//               world's channel set; if no match exists,
	//               return ERR_NOSUCHCHANNEL.
	//
	// After resolution, name is the canonical "!IDshort" form and
	// the rest of the join path treats it like any other channel.
	if isSafeChannel(name) {
		resolved, ok := c.resolveSafeChannel(name)
		if !ok {
			c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOSUCHCHANNEL,
				name, "No such channel"))
			return
		}
		name = resolved
	}

	// Check the door before knocking. We re-check after the join
	// because the channel state could have changed in between, but
	// the early-out gives correct error numerics for the common
	// cases.
	if existing := c.server.world.FindChannel(name); existing != nil {
		if reason := checkJoinPolicy(c, existing, c.user, key); reason != "" {
			switch reason {
			case "+i":
				c.send(protocol.NumericReply(srv, nick, protocol.ERR_INVITEONLYCHAN, name, "Cannot join channel (+i)"))
			case "+k":
				c.send(protocol.NumericReply(srv, nick, protocol.ERR_BADCHANNELKEY, name, "Cannot join channel (+k)"))
			case "+l":
				c.send(protocol.NumericReply(srv, nick, protocol.ERR_CHANNELISFULL, name, "Cannot join channel (+l)"))
			case "+b":
				c.send(protocol.NumericReply(srv, nick, protocol.ERR_BANNEDFROMCHAN, name, "Cannot join channel (+b)"))
			}
			return
		}
	}

	ch, _, added, err := c.server.world.JoinChannel(c.user.ID, name)
	if err != nil {
		c.logger.Warn("JoinChannel failed", "error", err, "channel", name)
		return
	}
	if !added {
		// Already a member; nothing to broadcast.
		return
	}

	// Broadcast the JOIN to every member (including the joiner).
	joinMsg := &protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: "JOIN",
		Params:  []string{ch.Name()},
	}
	c.server.broadcastToChannelFederated(ch, joinMsg, 0, true)

	// Topic burst (only to the joiner).
	c.sendTopicState(ch)

	// NAMES burst (only to the joiner).
	c.sendNamesReply(ch)
}

// checkJoinPolicy returns the mode character that prevented the
// join, or "" if the join is allowed. Used to translate from "no"
// to a specific 47x numeric. Consumes a pending invite if the
// channel is +i and the user has one — invites are one-shot.
func checkJoinPolicy(c *Conn, ch *state.Channel, u *state.User, key string) string {
	inviteOnly, _, _, _, _, _, chanKey, limit := ch.Modes()
	fold := c.server.world.CaseMapping().Fold
	mask := u.Hostmask()
	if inviteOnly {
		// +I mask match bypasses the per-user invite requirement.
		if !ch.MatchesInvex(mask, fold) && !ch.ConsumeInvite(u.ID) {
			return "+i"
		}
	}
	if chanKey != "" && chanKey != key {
		return "+k"
	}
	if limit > 0 && ch.MemberCount() >= limit {
		return "+l"
	}
	if ch.MatchesBan(mask, fold) && !ch.MatchesException(mask, fold) {
		return "+b"
	}
	return ""
}

// handleInvite implements INVITE (RFC 2812 §3.2.7).
//
//	INVITE <nick> <channel>
//
// Records a one-shot invite for nick on channel and delivers an
// INVITE message to the target. The inviter must be on the channel
// and (if +i is set) must be a channel operator.
func (c *Conn) handleInvite(m *protocol.Message) {
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	if len(m.Params) < 2 {
		c.sendNeedMoreParams("INVITE")
		return
	}
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	targetNick := m.Params[0]
	channelName := m.Params[1]

	target := c.server.world.FindByNick(targetNick)
	if target == nil {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOSUCHNICK,
			targetNick, "No such nick/channel"))
		return
	}
	ch := c.server.world.FindChannel(channelName)
	// RFC 2812 §3.2.7: invites to a non-existent channel are
	// allowed; the invitee can then create it. We follow the same
	// rule.
	if ch != nil {
		if !ch.IsMember(c.user.ID) {
			c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOTONCHANNEL,
				channelName, "You are not on that channel"))
			return
		}
		if ch.IsMember(target.ID) {
			c.send(protocol.NumericReply(srv, nick, protocol.ERR_USERONCHANNEL,
				targetNick, channelName, "is already on channel"))
			return
		}
		inviteOnly, _, _, _, _, _, _, _ := ch.Modes()
		if inviteOnly && !ch.Membership(c.user.ID).IsOp() {
			c.send(protocol.NumericReply(srv, nick, protocol.ERR_CHANOPRIVSNEEDED,
				channelName, "You are not channel operator"))
			return
		}
		ch.AddInvite(target.ID)
	}

	// RPL_INVITING (341) to the inviter, INVITE message to the target.
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_INVITING, channelName, target.Nick))
	if dest := c.server.connFor(target.ID); dest != nil {
		dest.send(&protocol.Message{
			Prefix:  c.user.Hostmask(),
			Command: "INVITE",
			Params:  []string{target.Nick, channelName},
		})
	}
}

// sendTopicState delivers the topic burst to the joining client.
// If no topic is set, RPL_NOTOPIC is sent. If a topic exists, both
// RPL_TOPIC and RPL_TOPICWHOTIME are sent so clients can render the
// "set by X at T" annotation modern UIs expect.
func (c *Conn) sendTopicState(ch *state.Channel) {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	text, setBy, at := ch.Topic()
	if text == "" {
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_NOTOPIC, ch.Name(), "No topic is set"))
		return
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_TOPIC, ch.Name(), text))
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_TOPICWHOTIME,
		ch.Name(), setBy, itoaPositive(int(at.Unix()))))
}

// sendNamesReply delivers the NAMES burst for ch to the current
// connection. We pack as many nicks as comfortably fit on one line
// (well under 510 bytes) and emit one or more 353 lines, terminated
// by a 366.
func (c *Conn) sendNamesReply(ch *state.Channel) {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick

	// Channel visibility symbol per RFC 2812 §5.2.
	symbol := "="
	_, _, _, priv, secret, _, _, _ := ch.Modes()
	switch {
	case secret:
		symbol = "@"
	case priv:
		symbol = "*"
	}

	members := ch.MemberIDs()
	const lineSoftCap = 400 // leaves headroom for prefix + numeric framing
	var line strings.Builder
	flush := func() {
		if line.Len() == 0 {
			return
		}
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_NAMREPLY,
			symbol, ch.Name(), line.String()))
		line.Reset()
	}

	// On +a (anonymous) channels NAMES returns a single
	// "anonymous" entry rather than the real member list per
	// RFC 2811 §4.2.1: members must not be able to identify
	// each other via NAMES.
	if ch.Anonymous() {
		line.WriteString("anonymous")
		flush()
	} else {
		multiPrefix := c.capsAccepted["multi-prefix"]
		for id, mem := range members {
			u := c.server.world.FindByID(id)
			if u == nil {
				continue
			}
			// RFC 2812 §3.5: services are invisible to NAMES.
			// They participate in channels (so they can listen
			// for SQUERY routing) but do not show up in the
			// member list.
			if u.Service {
				continue
			}
			var prefix string
			if multiPrefix {
				prefix = mem.MultiPrefix()
			} else {
				prefix = mem.Prefix()
			}
			token := prefix + u.Nick
			if line.Len()+1+len(token) > lineSoftCap {
				flush()
			}
			if line.Len() > 0 {
				line.WriteByte(' ')
			}
			line.WriteString(token)
		}
		flush()
	}

	c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFNAMES, ch.Name(), "End of NAMES list"))
}

// handlePart implements the PART command (RFC 2812 §3.2.2).
//
//	PART <channel>{,<channel>} [<reason>]
//
// For each channel: verify membership, broadcast PART to remaining
// members + the parting user, drop the user from the channel, and
// drop the channel itself if it became empty.
func (c *Conn) handlePart(m *protocol.Message) {
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	if len(m.Params) < 1 {
		c.sendNeedMoreParams("PART")
		return
	}
	reason := ""
	if t, ok := m.Trailing(); ok && len(m.Params) > 1 {
		reason = t
	}
	for _, name := range strings.Split(m.Params[0], ",") {
		c.partOne(name, reason)
	}
}

// partOne removes the user from one channel. Used by both PART and
// JOIN 0; the broadcast and drop semantics are identical so they
// share this helper.
func (c *Conn) partOne(name, reason string) {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	ch := c.server.world.FindChannel(name)
	if ch == nil {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOSUCHCHANNEL, name, "No such channel"))
		return
	}
	if !ch.IsMember(c.user.ID) {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOTONCHANNEL, name, "You're not on that channel"))
		return
	}
	// Build the PART message before removing the user so the
	// hostmask is still accurate at broadcast time.
	partMsg := &protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: "PART",
	}
	if reason == "" {
		partMsg.Params = []string{ch.Name()}
	} else {
		partMsg.Params = []string{ch.Name(), reason}
	}
	// Broadcast to every member, including the parting user.
	c.server.broadcastToChannelFederated(ch, partMsg, 0, true)

	if _, _, err := c.server.world.PartChannel(c.user.ID, name); err != nil {
		c.logger.Warn("PartChannel failed", "error", err, "channel", name)
	}
}

// handleKick implements KICK (RFC 2812 §3.2.8).
//
//	KICK <channel>{,<channel>} <user>{,<user>} [<comment>]
//
// Either one channel + many users, or N channels + N users (one
// kick per (channel, user) pair). Requires the kicker to be a
// member and an op of every named channel.
func (c *Conn) handleKick(m *protocol.Message) {
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	if len(m.Params) < 2 {
		c.sendNeedMoreParams("KICK")
		return
	}
	channels := strings.Split(m.Params[0], ",")
	targets := strings.Split(m.Params[1], ",")
	reason := c.user.Nick
	if len(m.Params) >= 3 && m.Params[2] != "" {
		reason = m.Params[2]
	}

	// RFC 2812 §3.2.8: either one channel and many targets, or
	// N channels and N targets. Anything else is an error per the
	// spec but most servers tolerate "1 chan -> N targets" by
	// repeating the channel.
	if len(channels) == 1 {
		ch := channels[0]
		for _, t := range targets {
			c.kickOne(ch, t, reason)
		}
		return
	}
	if len(channels) == len(targets) {
		for i := range channels {
			c.kickOne(channels[i], targets[i], reason)
		}
		return
	}
	c.sendNeedMoreParams("KICK")
}

// kickOne removes target from channel after verifying that the
// caller is an op in the channel and the target is actually a
// member. Broadcasts KICK to every channel member, including the
// victim, before the removal so the victim sees their own removal.
func (c *Conn) kickOne(channelName, targetNick, reason string) {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	ch := c.server.world.FindChannel(channelName)
	if ch == nil {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOSUCHCHANNEL,
			channelName, "No such channel"))
		return
	}
	if !ch.IsMember(c.user.ID) {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOTONCHANNEL,
			channelName, "You are not on that channel"))
		return
	}
	if !ch.Membership(c.user.ID).IsOp() {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_CHANOPRIVSNEEDED,
			channelName, "You are not channel operator"))
		return
	}
	target := c.server.world.FindByNick(targetNick)
	if target == nil || !ch.IsMember(target.ID) {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_USERNOTINCHANNEL,
			targetNick, channelName, "They are not on that channel"))
		return
	}
	kickMsg := &protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: "KICK",
		Params:  []string{ch.Name(), target.Nick, reason},
	}
	// Audit emit happens before the broadcast so a test that reads
	// the events store immediately after seeing the wire KICK line
	// is guaranteed to see the corresponding row. The same ordering
	// is used by handleTopic and handleChannelMode.
	c.server.emitAudit(c.ctx, AuditTypeKick, c.user.Hostmask(), ch.Name(), map[string]any{
		"victim": target.Nick,
		"reason": reason,
	})
	c.server.broadcastToChannelFederated(ch, kickMsg, 0, true)
	if _, _, err := c.server.world.PartChannel(target.ID, ch.Name()); err != nil {
		c.logger.Warn("kick remove failed", "error", err)
	}
}

// handleTopic implements TOPIC (RFC 2812 §3.2.4).
//
//	TOPIC <channel>             -- read current topic
//	TOPIC <channel> :<text>     -- set topic
//	TOPIC <channel> :           -- clear topic
//
// Setting requires +t -> chanop, otherwise any member may set.
func (c *Conn) handleTopic(m *protocol.Message) {
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	if len(m.Params) < 1 {
		c.sendNeedMoreParams("TOPIC")
		return
	}
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	name := m.Params[0]
	ch := c.server.world.FindChannel(name)
	if ch == nil {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOSUCHCHANNEL, name, "No such channel"))
		return
	}

	// Read form: only one param (the channel).
	if len(m.Params) == 1 {
		if !ch.IsMember(c.user.ID) {
			// Reading the topic from outside the channel is allowed
			// for non-secret channels; but RFC 2812 lets servers
			// require membership. We follow the looser policy.
		}
		c.sendTopicState(ch)
		return
	}

	// Set form. Membership is required.
	if !ch.IsMember(c.user.ID) {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOTONCHANNEL, name, "You are not on that channel"))
		return
	}
	// +t enforcement: only ops can set the topic.
	_, _, _, _, _, topicLocked, _, _ := ch.Modes()
	if topicLocked && !ch.Membership(c.user.ID).IsOp() {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_CHANOPRIVSNEEDED, name, "You are not channel operator"))
		return
	}
	// Topic length cap.
	text := m.Params[1]
	if maxLen := c.server.cfg.Server.Limits.TopicLength; maxLen > 0 && len(text) > maxLen {
		text = text[:maxLen]
	}
	ch.SetTopic(text, c.user.Hostmask(), c.server.now())
	// Persist the new topic before broadcasting so a crash between
	// the in-memory mutation and the broadcast cannot leave the
	// channel record stale on restart.
	c.server.persistChannel(c.ctx, ch)
	c.server.emitAudit(c.ctx, AuditTypeTopic, c.user.Hostmask(), ch.Name(), map[string]any{
		"topic": text,
	})
	// Broadcast TOPIC to every member, including the setter so they
	// see their own change reflected.
	topicMsg := &protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: "TOPIC",
		Params:  []string{ch.Name(), text},
	}
	c.server.broadcastToChannelFederated(ch, topicMsg, 0, true)
}

// validChannelName enforces the loose RFC 2812 channel name grammar:
// must start with '#', '&', '+' (modeless prefix from RFC 2811
// §2.1), or '!' (safe channel prefix from RFC 2811 §2.1), up to
// maxLen bytes, no SPACE/CR/LF/NUL, no comma, no bell.
func validChannelName(s string, maxLen int) bool {
	if maxLen <= 0 {
		maxLen = 50
	}
	if len(s) < 2 || len(s) > maxLen {
		return false
	}
	if s[0] != '#' && s[0] != '&' && s[0] != '+' && s[0] != '!' {
		return false
	}
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case ' ', ',', '\x07', '\r', '\n', 0:
			return false
		}
	}
	return true
}

// isSafeChannel reports whether name has the '!' prefix that
// RFC 2811 §2.1 reserves for safe (timestamped) channels.
func isSafeChannel(name string) bool {
	return len(name) > 0 && name[0] == '!'
}

// safeChannelIDLen is the canonical 5-character ID length from
// RFC 2811 §3. The ID is uppercase letters and digits and is
// generated server-side; clients never see a "!short" with no ID
// on the wire.
const safeChannelIDLen = 5

// safeChannelIDAlphabet is the character set the RFC permits for
// the generated ID: uppercase letters A-Z and digits 0-9.
const safeChannelIDAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// resolveSafeChannel converts a client-supplied "!short" or "!!short"
// JOIN target into the canonical "!IDshort" form.
//
//   - "!!short" always allocates a fresh ID and returns
//     "!IDshort". The new channel does not yet exist in the world;
//     EnsureChannel will create it on the subsequent JoinChannel.
//   - "!short" walks the world looking for any existing safe
//     channel whose suffix (after the 5-char ID) equals "short".
//     Returns the first match. If no match exists, ok is false.
//   - "!IDshort" (already canonical, with a 5-char ID) is returned
//     unchanged.
//
// We never reject the JOIN here for shape; validChannelName has
// already done that.
func (c *Conn) resolveSafeChannel(name string) (string, bool) {
	if len(name) >= 2 && name[1] == '!' {
		// "!!short" — generate a fresh ID. The result is
		// "!IDshort"; the second '!' is consumed.
		short := name[2:]
		if short == "" {
			return "", false
		}
		id := c.server.newSafeChannelID()
		return "!" + id + short, true
	}
	// Plain "!..." — could already be the canonical "!IDshort"
	// (if the rest is at least 5 chars and the first 5 chars are
	// alphanumeric uppercase) or a short form "!short" the
	// client wants us to resolve.
	rest := name[1:]
	if len(rest) > safeChannelIDLen && isSafeChannelID(rest[:safeChannelIDLen]) {
		// Looks already canonical. Accept it as-is so a client
		// that copy-pastes "!ABCDEchat" works.
		return name, true
	}
	// Resolve "!short" against the world by suffix match.
	for _, ch := range c.server.world.ChannelsSnapshot() {
		n := ch.Name()
		if !isSafeChannel(n) || len(n) <= 1+safeChannelIDLen {
			continue
		}
		suffix := n[1+safeChannelIDLen:]
		if suffix == rest {
			return n, true
		}
	}
	return "", false
}

// isSafeChannelID reports whether s is exactly a 5-character
// uppercase-alphanumeric token, i.e. a valid generated safe-channel
// ID per RFC 2811 §3.
func isSafeChannelID(s string) bool {
	if len(s) != safeChannelIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// isModelessChannel reports whether name starts with the '+'
// prefix that RFC 2811 §4.2.1 reserves for modeless channels.
// MODE-changing commands (MODE, KICK, INVITE, TOPIC under +t)
// are silently rejected on these channels because the channel
// has no operators and no boolean modes that can be set.
func isModelessChannel(name string) bool {
	return len(name) > 0 && name[0] == '+'
}

// itoaPositive is a tiny stdlib-free integer-to-string for the cases
// in this package where we don't want to import strconv just for an
// unsigned-ish integer. Mirrors the helper in internal/state.
func itoaPositive(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
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
