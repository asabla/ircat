package server

import (
	"strings"
	"time"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
)

// handleNames implements NAMES (RFC 2812 §3.2.5).
//
//	NAMES [<channel>{,<channel>}]
//
// With a channel list, send the NAMES burst for each. Without
// arguments, walk every visible channel — for now we keep it simple
// and require an explicit list, returning an empty 366 with "*"
// as the channel name when no list is supplied (matching the
// historical behaviour of charybdis-derived ircds).
func (c *Conn) handleNames(m *protocol.Message) {
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	if len(m.Params) < 1 {
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFNAMES, "*", "End of NAMES list"))
		return
	}
	for _, name := range strings.Split(m.Params[0], ",") {
		ch := c.server.world.FindChannel(name)
		if ch == nil {
			c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFNAMES, name, "End of NAMES list"))
			continue
		}
		// RFC 2811 §4.2.6 / 4.2.7: a non-member must not see the
		// member list of a +s (secret) or +p (private) channel.
		// We send only the 366 terminator so the requester gets a
		// well-formed reply but no member information.
		_, _, _, priv, secret, _, _, _ := ch.Modes()
		if (secret || priv) && !ch.IsMember(c.user.ID) {
			c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFNAMES, name, "End of NAMES list"))
			continue
		}
		c.sendNamesReply(ch)
	}
}

// handleList implements LIST (RFC 2812 §3.2.6).
//
//	LIST [<channel>{,<channel>}]
//
// Returns RPL_LISTSTART, then one RPL_LIST per matching channel,
// then RPL_LISTEND. With no parameters, lists every channel that is
// not +s (secret) or +p (private) — unless the asking user is a
// member, in which case the visibility filter is bypassed.
func (c *Conn) handleList(m *protocol.Message) {
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick

	c.send(protocol.NumericReply(srv, nick, protocol.RPL_LISTSTART, "Channel", "Users  Name"))

	var targets []*state.Channel
	if len(m.Params) >= 1 && m.Params[0] != "" {
		for _, name := range strings.Split(m.Params[0], ",") {
			if ch := c.server.world.FindChannel(name); ch != nil {
				targets = append(targets, ch)
			}
		}
	} else {
		targets = c.server.world.ChannelsSnapshot()
	}

	for _, ch := range targets {
		_, _, _, priv, secret, _, _, _ := ch.Modes()
		if (priv || secret) && !ch.IsMember(c.user.ID) {
			continue
		}
		topic, _, _ := ch.Topic()
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_LIST,
			ch.Name(), itoaPositive(ch.MemberCount()), topic))
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_LISTEND, "End of LIST"))
}

// handleWho implements WHO (RFC 2812 §3.6.1).
//
//	WHO [<mask> [o]]
//
// Returns one RPL_WHOREPLY (352) per matching user, terminated by
// RPL_ENDOFWHO (315). If <mask> is a channel, the reply is the
// channel's members. Otherwise we treat the mask as a literal nick
// or glob pattern (M2 only matches literal nicks; glob support is
// trivial to add when the use case appears).
func (c *Conn) handleWho(m *protocol.Message) {
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	mask := "*"
	if len(m.Params) >= 1 && m.Params[0] != "" {
		mask = m.Params[0]
	}

	if isChannelName(mask) {
		ch := c.server.world.FindChannel(mask)
		if ch != nil {
			// RFC 2811 §4.2.6 / 4.2.7: a non-member must not see
			// the membership list of a +s (secret) or +p (private)
			// channel. We silently emit only the 315 terminator
			// in that case so the requester cannot even probe for
			// existence.
			_, _, _, priv, secret, _, _, _ := ch.Modes()
			if (secret || priv) && !ch.IsMember(c.user.ID) {
				c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFWHO, mask, "End of WHO list"))
				return
			}
			// On +a (anonymous) channels WHO returns a single
			// synthetic "anonymous" row rather than the real
			// membership list per RFC 2811 §4.2.1.
			if ch.Anonymous() {
				c.send(protocol.NumericReply(srv, nick, protocol.RPL_WHOREPLY,
					ch.Name(), "anonymous", "anonymous.", srv,
					"anonymous", "H", "0 anonymous"))
			} else {
				for id, mem := range ch.MemberIDs() {
					u := c.server.world.FindByID(id)
					if u == nil {
						continue
					}
					c.sendWhoReply(ch.Name(), u, mem)
				}
			}
		}
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFWHO, mask, "End of WHO list"))
		return
	}

	// Non-channel form. RFC 2812 §3.6.1 says the mask is a glob
	// matched against the user's hostmask. We support the same
	// '*' / '?' wildcards the +b/+e/+I list-mode matchers use.
	// A literal nick still works because a nick has no wildcards
	// and the matcher reduces to an exact compare.
	if state.HasGlobWildcard(mask) {
		for _, snap := range c.server.world.Snapshot() {
			snap := snap
			if state.GlobMatchHost(mask, snap.Hostmask()) ||
				state.GlobMatchHost(mask, snap.Nick) {
				c.sendWhoReply("*", &snap, 0)
			}
		}
	} else if u := c.server.world.FindByNick(mask); u != nil {
		c.sendWhoReply("*", u, 0)
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFWHO, mask, "End of WHO list"))
}

func (c *Conn) sendWhoReply(channel string, u *state.User, mem state.Membership) {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	// "H" = here, "G" = gone (away). RFC 2812 §3.6.1.
	flags := "H"
	if u.Away != "" {
		flags = "G"
	}
	// "*" appended marks the user as an IRC operator (+o).
	if strings.ContainsRune(u.Modes, 'o') {
		flags += "*"
	}
	// Channel status prefixes. With multi-prefix negotiated, all
	// applicable prefixes are appended in descending authority
	// order (creator → op → voice). Without it, only the highest
	// prefix is rendered, matching the legacy single-prefix WHO
	// format every pre-IRCv3 client expects.
	if c.capsAccepted["multi-prefix"] {
		flags += mem.MultiPrefix()
	} else {
		flags += mem.Prefix()
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_WHOREPLY,
		channel,
		userOrNick(u),
		hostOrUnknown(u),
		srv,
		u.Nick,
		flags,
		"0 "+u.Realname))
}

// handleWhois implements WHOIS (RFC 2812 §3.6.2).
//
//	WHOIS [<server>] <nick>{,<nick>}
//
// We answer from the local World, which is the union of every
// federated node's burst plus runtime announces — so a WHOIS for a
// remote user already returns the right nick/user/host/realname.
// For remote users we report the user's HomeServer in 312
// RPL_WHOISSERVER instead of our own name, and idle time falls
// back to zero because we do not track per-message activity for
// non-local connections. Each nick produces RPL_WHOISUSER +
// RPL_WHOISSERVER + RPL_WHOISCHANNELS (if applicable) +
// RPL_WHOISOPERATOR (if +o) + RPL_WHOISIDLE + RPL_ENDOFWHOIS,
// with ERR_NOSUCHNICK for missing targets.
func (c *Conn) handleWhois(m *protocol.Message) {
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(c.server.cfg.Server.Name, c.starOrNick(),
			protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	if len(m.Params) < 1 {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NONICKNAMEGIVEN, "No nickname given"))
		return
	}
	// If a server param was given, the nick list is the second param.
	target := m.Params[0]
	if len(m.Params) >= 2 {
		target = m.Params[1]
	}
	for _, name := range strings.Split(target, ",") {
		c.sendWhois(name)
	}
}

func (c *Conn) sendWhois(name string) {
	srv := c.server.cfg.Server.Name
	nick := c.user.Nick
	u := c.server.world.FindByNick(name)
	if u == nil {
		c.send(protocol.NumericReply(srv, nick, protocol.ERR_NOSUCHNICK, name, "No such nick/channel"))
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFWHOIS, name, "End of WHOIS list"))
		return
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_WHOISUSER,
		u.Nick, userOrNick(u), hostOrUnknown(u), "*", u.Realname))
	// 312: report the user's home node so a federated WHOIS does
	// not lie about which server actually owns the connection.
	homeServer := srv
	homeDesc := c.server.cfg.Server.Description
	if u.IsRemote() {
		homeServer = u.HomeServer
		homeDesc = "ircat federation peer"
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_WHOISSERVER,
		u.Nick, homeServer, homeDesc))
	if strings.ContainsRune(u.Modes, 'o') {
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_WHOISOPERATOR,
			u.Nick, "is an IRC operator"))
	}
	// Channel membership: build the list with op/voice prefixes.
	chans := c.server.world.UserChannels(u.ID)
	if len(chans) > 0 {
		var b strings.Builder
		for i, ch := range chans {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(ch.Membership(u.ID).Prefix())
			b.WriteString(ch.Name())
		}
		c.send(protocol.NumericReply(srv, nick, protocol.RPL_WHOISCHANNELS, u.Nick, b.String()))
	}
	// Idle time per RFC 2812 §3.6.2: seconds since the user's
	// last PRIVMSG / NOTICE. We pull lastMessageAt off the
	// owning Conn (it's atomic, no Conn lock needed). Remote
	// users always report 0 because we have no visibility into
	// their per-message activity. If the user has not yet sent
	// any speaking traffic since connect, fall back to 0 — the
	// RFC does not say to report time-since-connect there.
	idle := 0
	if !u.IsRemote() {
		if owner := c.server.connFor(u.ID); owner != nil {
			if last := owner.lastMessageAt.Load(); last > 0 {
				since := int(time.Since(time.Unix(0, last)).Seconds())
				if since > 0 {
					idle = since
				}
			}
		}
	}
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_WHOISIDLE,
		u.Nick, itoaPositive(idle), itoaPositive(int(u.ConnectAt.Unix())), "seconds idle, signon time"))
	c.send(protocol.NumericReply(srv, nick, protocol.RPL_ENDOFWHOIS, u.Nick, "End of WHOIS list"))
}

func userOrNick(u *state.User) string {
	if u.User != "" {
		return u.User
	}
	return u.Nick
}

func hostOrUnknown(u *state.User) string {
	if u.Host != "" {
		return u.Host
	}
	return "unknown"
}
