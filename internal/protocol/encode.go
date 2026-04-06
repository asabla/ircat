package protocol

import (
	"sort"
	"strings"
)

// Bytes returns the canonical wire form of m, including the trailing
// CRLF. Returns [ErrTooLong] if the result would exceed
// [MaxMessageBytes], [ErrBadCommand] if Command is empty or invalid,
// [ErrTooManyParams] if there are too many parameters, or another
// error if a non-trailing parameter contains a forbidden character
// (space, NUL, CR, or LF).
func (m *Message) Bytes() ([]byte, error) {
	if !validCommand(m.Command) {
		return nil, ErrBadCommand
	}
	if len(m.Params) > MaxParams {
		return nil, ErrTooManyParams
	}

	var b strings.Builder
	// Pessimistic capacity hint: most messages are well under 200 B.
	b.Grow(64 + len(m.Prefix) + len(m.Command) + 16*len(m.Params))

	if len(m.Tags) > 0 {
		b.WriteByte('@')
		// Stable order so encode is deterministic.
		keys := make([]string, 0, len(m.Tags))
		for k := range m.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(';')
			}
			b.WriteString(k)
			if v := m.Tags[k]; v != "" {
				b.WriteByte('=')
				b.WriteString(escapeTagValue(v))
			}
		}
		b.WriteByte(' ')
	}

	if m.Prefix != "" {
		b.WriteByte(':')
		b.WriteString(m.Prefix)
		b.WriteByte(' ')
	}

	// Command — letters get upper-cased, numerics keep their digits.
	if isAllDigits(m.Command) {
		b.WriteString(m.Command)
	} else {
		b.WriteString(strings.ToUpper(m.Command))
	}

	for i, p := range m.Params {
		b.WriteByte(' ')
		isLast := i == len(m.Params)-1
		if isLast {
			// The last parameter is always emitted in the trailing
			// form (leading ":"), matching the convention every
			// production ircd follows. This is unambiguous for the
			// receiver — the colon is purely a "rest of the line"
			// marker — and saves us from special-casing params with
			// spaces, embedded colons, or empty values.
			if err := assertNoCRLFNUL(p); err != nil {
				return nil, err
			}
			b.WriteByte(':')
			b.WriteString(p)
			continue
		}
		if err := assertMiddleParam(p); err != nil {
			return nil, err
		}
		b.WriteString(p)
	}

	b.WriteString("\r\n")
	if b.Len() > MaxMessageBytes {
		return nil, ErrTooLong
	}
	return []byte(b.String()), nil
}

// MustBytes is like [Message.Bytes] but panics on error. It is
// intended for tests and for hard-coded server-internal messages
// (numerics, MOTD lines) where an encoding error indicates a bug.
func (m *Message) MustBytes() []byte {
	out, err := m.Bytes()
	if err != nil {
		panic("protocol: " + err.Error())
	}
	return out
}

func assertMiddleParam(p string) error {
	if p == "" {
		return ErrBadCommand // an empty middle param is not legal
	}
	if p[0] == ':' {
		return ErrBadCommand
	}
	return assertNoSpaceCRLFNUL(p)
}

func assertNoSpaceCRLFNUL(p string) error {
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case ' ', '\r', '\n', 0:
			return ErrBadCommand
		}
	}
	return nil
}

func assertNoCRLFNUL(p string) error {
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '\r', '\n', 0:
			return ErrBadCommand
		}
	}
	return nil
}

// escapeTagValue applies the IRCv3 message-tags escape rules in
// reverse: ";" -> "\:", " " -> "\s", "\\" -> "\\\\", CR -> "\r",
// LF -> "\n".
func escapeTagValue(s string) string {
	if !strings.ContainsAny(s, "; \\\r\n") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case ';':
			b.WriteString(`\:`)
		case ' ':
			b.WriteString(`\s`)
		case '\\':
			b.WriteString(`\\`)
		case '\r':
			b.WriteString(`\r`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
