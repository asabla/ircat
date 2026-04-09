package server

import "sort"

// supportedCapList is the closed set of IRCv3 capabilities the
// server is willing to advertise. Adding a capability requires both
// listing it here and wiring whatever pass-through logic the
// capability implies on the affected handlers.
var supportedCapList = []string{
	// Pass IRCv3 message tags through. The parser already
	// round-trips them; this just tells the client we will not
	// strip them.
	"message-tags",
	// server-time attaches an @time tag to every outbound
	// message so the client renders accurate timestamps for
	// backlog and history. Wired in Conn.send when the
	// capability is in capsAccepted.
	"server-time",
}

// supportedCaps returns the space-separated cap list as it appears
// on the wire after CAP LS.
func supportedCaps() string {
	out := ""
	for i, c := range supportedCapList {
		if i > 0 {
			out += " "
		}
		out += c
	}
	return out
}

// isSupportedCap reports whether name is in the advertised set.
func isSupportedCap(name string) bool {
	for _, c := range supportedCapList {
		if c == name {
			return true
		}
	}
	return false
}

// acceptedCaps returns the sorted list of capabilities the client
// has successfully negotiated on this connection.
func (c *Conn) acceptedCaps() []string {
	if len(c.capsAccepted) == 0 {
		return nil
	}
	out := make([]string, 0, len(c.capsAccepted))
	for k := range c.capsAccepted {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// addAcceptedCap records a successful CAP REQ.
func (c *Conn) addAcceptedCap(name string) {
	if c.capsAccepted == nil {
		c.capsAccepted = make(map[string]bool)
	}
	c.capsAccepted[name] = true
}

// dropAcceptedCap removes a previously-accepted cap (CAP REQ -name).
func (c *Conn) dropAcceptedCap(name string) {
	delete(c.capsAccepted, name)
}
