// Package protocol implements the IRC wire format defined by
// RFC 1459 §2.3 and refined by RFC 2812 §2.3.
//
// The package is intentionally I/O free: parsers and encoders deal
// with byte slices and strings, never with sockets. Higher layers
// (internal/server, internal/federation) use the types defined here
// and own all of the network plumbing.
//
// What we accept on input vs. emit on output, in one place so it's
// easy to audit:
//
//   - Lines may be terminated with "\r\n" (canonical) or a bare "\n"
//     (legacy clients still in the wild). The terminator is not part
//     of [Message]; the parser strips it.
//   - The total wire length of a message including its CRLF is at
//     most 512 bytes (RFC 1459 §2.3). This package enforces the limit
//     on encoding via [ErrTooLong] and on parsing via the same value.
//   - Up to 15 parameters are allowed: 14 "middle" plus one "trailing"
//     introduced by " :".
//   - The trailing parameter is the only place spaces are allowed
//     inside a parameter; a leading colon there is part of the value.
//   - IRCv3 message-tags ("@key=value;key2=value2 ...") are tolerated
//     on input — we parse them into [Message.Tags] but do not act on
//     them in v1. They round-trip on encode if present.
//
// References:
//   - https://www.rfc-editor.org/rfc/rfc1459 §2.3
//   - https://www.rfc-editor.org/rfc/rfc2812 §2.3
//   - https://ircv3.net/specs/extensions/message-tags
package protocol

import (
	"errors"
	"fmt"
	"strings"
)

// MaxMessageBytes is the wire-format ceiling for an IRC message
// including its terminating CRLF (RFC 1459 §2.3). Parsers and encoders
// reject anything longer.
const MaxMessageBytes = 512

// MaxParams is the maximum number of parameters in a single message:
// 14 "middle" plus one optional "trailing" (RFC 2812 §2.3.1).
const MaxParams = 15

// Errors returned by the parser and encoder. Wrap with fmt.Errorf
// when adding context, but keep the sentinels comparable so callers
// can distinguish them.
var (
	// ErrEmpty is returned when the input has no command after
	// stripping any prefix and tags.
	ErrEmpty = errors.New("protocol: empty message")
	// ErrTooLong is returned when a message exceeds [MaxMessageBytes].
	ErrTooLong = errors.New("protocol: message exceeds 512 bytes")
	// ErrTooManyParams is returned when a message has more than
	// [MaxParams] parameters.
	ErrTooManyParams = errors.New("protocol: too many parameters")
	// ErrBadCommand is returned when the command token is neither
	// a run of letters nor a 3-digit numeric.
	ErrBadCommand = errors.New("protocol: invalid command")
	// ErrBadTags is returned for malformed IRCv3 tag prefixes.
	ErrBadTags = errors.New("protocol: invalid tag prefix")
)

// Message is a parsed IRC protocol line.
//
// The zero value is not a valid wire message; use [Parse] to construct
// one from bytes or build it field-by-field for outbound use.
type Message struct {
	// Tags is the optional IRCv3 message-tags map. Nil when no tags
	// are present.
	Tags map[string]string

	// Prefix is the optional source identifier (without the leading
	// ':'). Empty when absent.
	Prefix string

	// Command is the verb (uppercased letters) or a 3-character
	// numeric reply code as a string ("001", "353", ...).
	Command string

	// Params holds the parameters in order. The trailing parameter,
	// if any, is the last element; whether the original line used
	// the " :" trailing form is no longer distinguishable here, but
	// [Message.Bytes] reproduces it correctly.
	Params []string
}

// Numeric reports whether m.Command is a 3-digit numeric reply.
func (m *Message) Numeric() bool {
	if len(m.Command) != 3 {
		return false
	}
	for i := 0; i < 3; i++ {
		if m.Command[i] < '0' || m.Command[i] > '9' {
			return false
		}
	}
	return true
}

// Trailing returns the trailing parameter (the last param, the only
// one allowed to contain spaces). Returns ("", false) when there are
// no parameters.
func (m *Message) Trailing() (string, bool) {
	if len(m.Params) == 0 {
		return "", false
	}
	return m.Params[len(m.Params)-1], true
}

// WithTag returns a shallow copy of m with the named IRCv3 tag set
// to value. The returned message is safe to mutate without affecting
// the original. Used by per-conn server-time / account-tag /
// echo-message paths where the same broadcast Message needs a
// recipient-specific tag.
func (m *Message) WithTag(key, value string) *Message {
	if m == nil {
		return nil
	}
	out := *m
	out.Tags = make(map[string]string, len(m.Tags)+1)
	for k, v := range m.Tags {
		out.Tags[k] = v
	}
	out.Tags[key] = value
	return &out
}

// String returns the wire form of m. It panics if encoding fails;
// callers that care about the error should use [Message.Bytes].
func (m *Message) String() string {
	out, err := m.Bytes()
	if err != nil {
		return fmt.Sprintf("<invalid message: %v>", err)
	}
	return strings.TrimSuffix(string(out), "\r\n")
}
