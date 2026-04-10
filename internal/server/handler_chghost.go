package server

import (
	"strings"

	"github.com/asabla/ircat/internal/protocol"
	"github.com/asabla/ircat/internal/state"
)

// handleChghost implements the CHGHOST command. This is an
// operator-only command that changes a user's displayed host:
//
//	CHGHOST <nick> <newhost>
//
// On success, every shared-channel member that has negotiated the
// IRCv3 chghost cap receives:
//
//	:oldnick!olduser@oldhost CHGHOST olduser newhost
//
// Members without the cap see no notification — they will pick up
// the new host on the next WHOIS or WHO.
func (c *Conn) handleChghost(m *protocol.Message) {
	srv := c.server.cfg.Server.Name
	if c.user == nil || !c.user.Registered {
		c.send(protocol.NumericReply(srv, c.starOrNick(),
			protocol.ERR_NOTREGISTERED, "You have not registered"))
		return
	}
	if !strings.ContainsRune(c.user.Modes, 'o') {
		c.send(protocol.NumericReply(srv, c.user.Nick,
			protocol.ERR_NOPRIVILEGES, "Permission Denied- You're not an IRC operator"))
		return
	}
	if len(m.Params) < 2 {
		c.sendNeedMoreParams("CHGHOST")
		return
	}

	targetNick := m.Params[0]
	newHost := m.Params[1]

	target := c.server.world.FindByNick(targetNick)
	if target == nil {
		c.send(protocol.NumericReply(srv, c.user.Nick,
			protocol.ERR_NOSUCHNICK, targetNick, "No such nick/channel"))
		return
	}

	oldMask := target.Hostmask()
	oldUser := target.User
	target.Host = newHost

	// Notify shared-channel members that have the chghost cap.
	chgMsg := &protocol.Message{
		Prefix:  oldMask,
		Command: "CHGHOST",
		Params:  []string{oldUser, newHost},
	}
	notifyChghost(c.server, target, chgMsg)

	c.server.logger.Info("CHGHOST",
		"operator", c.user.Nick,
		"target", targetNick,
		"new_host", newHost)
}

// ChangeUserHost changes a user's host and notifies channel
// members with the chghost cap. Used by NickServ/ChanServ for
// cloaking or by any in-process service. Exported so
// services can call it.
func (s *Server) ChangeUserHost(target *state.User, newHost string) {
	oldMask := target.Hostmask()
	oldUser := target.User
	target.Host = newHost

	chgMsg := &protocol.Message{
		Prefix:  oldMask,
		Command: "CHGHOST",
		Params:  []string{oldUser, newHost},
	}
	notifyChghost(s, target, chgMsg)
}

// notifyChghost sends a CHGHOST message to every shared-channel
// member that has negotiated the cap. Members are deduplicated
// across channels so a peer in two shared channels receives the
// notification once, not twice.
func notifyChghost(s *Server, target *state.User, msg *protocol.Message) {
	seen := map[state.UserID]bool{target.ID: true}
	for _, ch := range s.world.UserChannels(target.ID) {
		for id := range ch.MemberIDs() {
			if seen[id] {
				continue
			}
			seen[id] = true
			if peer := s.connFor(id); peer != nil {
				if peer.capsAccepted["chghost"] {
					peer.send(msg)
				}
			}
		}
	}
	// Also notify the target user themselves if they have the cap.
	if tc := s.connFor(target.ID); tc != nil {
		if tc.capsAccepted["chghost"] {
			tc.send(msg)
		}
	}
}
