package protocol

import (
	"fmt"
	"strings"
)

// Parse decodes a single IRC message from line.
//
// line may end with "\r\n", "\n", or no terminator at all; the parser
// is permissive on input. Inputs longer than [MaxMessageBytes]
// (including any terminator that was actually present) are rejected
// with [ErrTooLong].
//
// The returned [Message] holds copies of the relevant byte ranges, so
// the caller is free to reuse line.
func Parse(line []byte) (*Message, error) {
	if len(line) > MaxMessageBytes {
		return nil, ErrTooLong
	}
	// Strip a single trailing CR or LF or CRLF. We accept either
	// terminator; some legacy clients only emit LF.
	s := string(line)
	s = strings.TrimSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\r")
	if s == "" {
		return nil, ErrEmpty
	}

	m := &Message{}

	// Optional IRCv3 tag prefix: "@key=value;key2=value ..."
	if s[0] == '@' {
		sp := strings.IndexByte(s, ' ')
		if sp < 0 {
			return nil, ErrBadTags
		}
		tags, err := parseTags(s[1:sp])
		if err != nil {
			return nil, err
		}
		m.Tags = tags
		s = strings.TrimLeft(s[sp+1:], " ")
		if s == "" {
			return nil, ErrEmpty
		}
	}

	// Optional source prefix: ":nick!user@host" or ":servername"
	if s[0] == ':' {
		sp := strings.IndexByte(s, ' ')
		if sp < 0 {
			return nil, ErrEmpty
		}
		m.Prefix = s[1:sp]
		s = strings.TrimLeft(s[sp+1:], " ")
		if s == "" {
			return nil, ErrEmpty
		}
	}

	// Command: letters (case-insensitive, normalized upper) or 3 digits.
	sp := strings.IndexByte(s, ' ')
	var cmd string
	if sp < 0 {
		cmd = s
		s = ""
	} else {
		cmd = s[:sp]
		s = strings.TrimLeft(s[sp+1:], " ")
	}
	if !validCommand(cmd) {
		return nil, fmt.Errorf("%w: %q", ErrBadCommand, cmd)
	}
	if isAllDigits(cmd) {
		m.Command = cmd // numerics keep their digit form verbatim
	} else {
		m.Command = strings.ToUpper(cmd)
	}

	// Parameters: up to 14 "middle" then optionally one "trailing"
	// introduced by " :" (or by being the only token left starting
	// with ':').
	for s != "" {
		if s[0] == ':' {
			// Trailing — consumes the rest of the line including
			// spaces. The leading ':' is dropped from the value.
			m.Params = append(m.Params, s[1:])
			break
		}
		if len(m.Params) >= MaxParams-1 {
			// We have already accumulated 14 middle params, and the
			// remaining slice is the trailing — even without a leading
			// colon (some senders omit it when there are no spaces).
			// RFC 2812 §2.3.1 form 2 explicitly allows this.
			m.Params = append(m.Params, s)
			break
		}
		sp = strings.IndexByte(s, ' ')
		if sp < 0 {
			m.Params = append(m.Params, s)
			break
		}
		m.Params = append(m.Params, s[:sp])
		s = strings.TrimLeft(s[sp+1:], " ")
	}
	if len(m.Params) > MaxParams {
		return nil, ErrTooManyParams
	}
	return m, nil
}

// parseTags decodes the body of an IRCv3 tag prefix (the bytes between
// the leading '@' and the next space). Tag values may be empty; the
// escaping rules from the IRCv3 spec are honored.
func parseTags(body string) (map[string]string, error) {
	if body == "" {
		return nil, ErrBadTags
	}
	out := make(map[string]string)
	for _, tag := range strings.Split(body, ";") {
		if tag == "" {
			return nil, ErrBadTags
		}
		eq := strings.IndexByte(tag, '=')
		var key, raw string
		if eq < 0 {
			key = tag
		} else {
			key = tag[:eq]
			raw = tag[eq+1:]
		}
		if key == "" {
			return nil, ErrBadTags
		}
		out[key] = unescapeTagValue(raw)
	}
	return out, nil
}

// unescapeTagValue applies the IRCv3 message-tags escape rules:
// "\:" -> ";", "\s" -> " ", "\\" -> "\", "\r" -> CR, "\n" -> LF.
// Any other "\X" is dropped (the spec says to remove the backslash).
func unescapeTagValue(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		switch s[i] {
		case ':':
			b.WriteByte(';')
		case 's':
			b.WriteByte(' ')
		case '\\':
			b.WriteByte('\\')
		case 'r':
			b.WriteByte('\r')
		case 'n':
			b.WriteByte('\n')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func validCommand(s string) bool {
	if len(s) == 0 {
		return false
	}
	if isAllDigits(s) {
		return len(s) == 3
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
