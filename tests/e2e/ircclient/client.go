// Package ircclient is a tiny IRC client used by the e2e tests to
// drive a running ircat process over a real TCP connection.
//
// It is intentionally minimal: enough to send raw lines, read until
// a numeric arrives, and tear down cleanly. The unit tests in
// internal/server already cover the bulk of the protocol — this
// helper exists only so the e2e tests can talk to the binary the
// same way irssi or weechat would.
package ircclient

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Client is one open connection to an ircat instance.
type Client struct {
	conn net.Conn
	r    *bufio.Reader
}

// Dial opens a TCP connection and returns a Client. addr is the
// host:port the test should connect to.
func Dial(addr string, timeout time.Duration) (*Client, error) {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	return &Client{conn: c, r: bufio.NewReader(c)}, nil
}

// Close shuts down the underlying connection.
func (c *Client) Close() error { return c.conn.Close() }

// Send writes a single IRC line. The CRLF terminator is added if it
// is not already present.
func (c *Client) Send(line string) error {
	if !strings.HasSuffix(line, "\r\n") {
		line += "\r\n"
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err := c.conn.Write([]byte(line))
	return err
}

// ReadLine reads one CRLF-delimited line, stripping the terminator.
// It uses the supplied deadline rather than a per-call timeout so
// callers can bound a multi-line wait without re-arming.
func (c *Client) ReadLine(deadline time.Time) (string, error) {
	if err := c.conn.SetReadDeadline(deadline); err != nil {
		return "", err
	}
	line, err := c.r.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// Expect reads lines until one matches the predicate or the deadline
// passes. Lines that do not match are returned in the trace slice so
// failure messages can show what the server actually sent.
func (c *Client) Expect(deadline time.Time, match func(line string) bool) (matched string, trace []string, err error) {
	for {
		line, readErr := c.ReadLine(deadline)
		if readErr != nil {
			return "", trace, fmt.Errorf("expect: %w", readErr)
		}
		trace = append(trace, line)
		if match(line) {
			return line, trace, nil
		}
		if time.Now().After(deadline) {
			return "", trace, errors.New("expect: deadline exceeded")
		}
	}
}

// ExpectNumeric is the most common Expect form: wait for a server
// numeric reply with the given 3-digit code.
func (c *Client) ExpectNumeric(code string, deadline time.Time) (string, []string, error) {
	return c.Expect(deadline, func(line string) bool {
		return ExtractNumeric(line) == code
	})
}

// Register completes the standard NICK + USER handshake and reads
// through the welcome burst, returning when 422 (or 376 if the test
// later switches to providing an MOTD) lands. Convenience for tests
// that just want a connected client.
func (c *Client) Register(nick string, deadline time.Time) error {
	if err := c.Send("NICK " + nick); err != nil {
		return err
	}
	if err := c.Send("USER " + nick + " 0 * :" + nick); err != nil {
		return err
	}
	for {
		line, err := c.ReadLine(deadline)
		if err != nil {
			return fmt.Errorf("register: %w", err)
		}
		num := ExtractNumeric(line)
		if num == "422" || num == "376" {
			return nil
		}
	}
}

// ExtractNumeric returns the 3-digit numeric code from a server line
// like ":server NNN target ..." or "" if the line is not a numeric
// reply.
func ExtractNumeric(line string) string {
	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 3 || !strings.HasPrefix(parts[0], ":") {
		return ""
	}
	if len(parts[1]) != 3 {
		return ""
	}
	for i := 0; i < 3; i++ {
		if parts[1][i] < '0' || parts[1][i] > '9' {
			return ""
		}
	}
	return parts[1]
}
