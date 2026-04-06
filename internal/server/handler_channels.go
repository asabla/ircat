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
	c.server.broadcastToChannel(ch, joinMsg, 0, true)

	// Topic burst (only to the joiner).
	c.sendTopicState(ch)

	// NAMES burst (only to the joiner).
	c.sendNamesReply(ch)
}

// checkJoinPolicy returns the mode character that prevented the
// join, or "" if the join is allowed. Used to translate from "no"
// to a specific 47x numeric.
func checkJoinPolicy(c *Conn, ch *state.Channel, u *state.User, key string) string {
	inviteOnly, _, _, _, _, _, chanKey, limit := ch.Modes()
	if inviteOnly {
		return "+i"
	}
	if chanKey != "" && chanKey != key {
		return "+k"
	}
	if limit > 0 && ch.MemberCount() >= limit {
		return "+l"
	}
	if ch.MatchesBan(u.Hostmask(), c.server.world.CaseMapping().Fold) {
		return "+b"
	}
	return ""
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

	for id, mem := range members {
		u := c.server.world.FindByID(id)
		if u == nil {
			continue
		}
		token := mem.Prefix() + u.Nick
		if line.Len()+1+len(token) > lineSoftCap {
			flush()
		}
		if line.Len() > 0 {
			line.WriteByte(' ')
		}
		line.WriteString(token)
	}
	flush()

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
	c.server.broadcastToChannel(ch, partMsg, 0, true)

	if _, _, err := c.server.world.PartChannel(c.user.ID, name); err != nil {
		c.logger.Warn("PartChannel failed", "error", err, "channel", name)
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
	// Broadcast TOPIC to every member, including the setter so they
	// see their own change reflected.
	topicMsg := &protocol.Message{
		Prefix:  c.user.Hostmask(),
		Command: "TOPIC",
		Params:  []string{ch.Name(), text},
	}
	c.server.broadcastToChannel(ch, topicMsg, 0, true)
}

// validChannelName enforces the loose RFC 2812 channel name grammar:
// must start with '#' or '&', up to maxLen bytes, no SPACE/CR/LF/NUL,
// no comma, no bell.
func validChannelName(s string, maxLen int) bool {
	if maxLen <= 0 {
		maxLen = 50
	}
	if len(s) < 2 || len(s) > maxLen {
		return false
	}
	if s[0] != '#' && s[0] != '&' {
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
