package protocol

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParse_Table(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want *Message
	}{
		{
			name: "command only",
			in:   "PING\r\n",
			want: &Message{Command: "PING"},
		},
		{
			name: "command with single param",
			in:   "NICK alice\r\n",
			want: &Message{Command: "NICK", Params: []string{"alice"}},
		},
		{
			name: "command with multiple middles",
			in:   "USER alice 0 * :Alice Example\r\n",
			want: &Message{
				Command: "USER",
				Params:  []string{"alice", "0", "*", "Alice Example"},
			},
		},
		{
			name: "trailing with no leading colon required (RFC 2812 form 2)",
			in:   "PRIVMSG #foo bar\r\n",
			want: &Message{Command: "PRIVMSG", Params: []string{"#foo", "bar"}},
		},
		{
			name: "trailing with leading colon and spaces",
			in:   "PRIVMSG #foo :hello world\r\n",
			want: &Message{Command: "PRIVMSG", Params: []string{"#foo", "hello world"}},
		},
		{
			name: "trailing with embedded colon",
			in:   "PRIVMSG #foo ::weird:value\r\n",
			want: &Message{Command: "PRIVMSG", Params: []string{"#foo", ":weird:value"}},
		},
		{
			name: "empty trailing",
			in:   "PRIVMSG #foo :\r\n",
			want: &Message{Command: "PRIVMSG", Params: []string{"#foo", ""}},
		},
		{
			name: "with prefix",
			in:   ":alice!alice@host PRIVMSG #foo :hi\r\n",
			want: &Message{
				Prefix:  "alice!alice@host",
				Command: "PRIVMSG",
				Params:  []string{"#foo", "hi"},
			},
		},
		{
			name: "with server prefix and numeric command",
			in:   ":irc.example.org 001 alice :Welcome to ExampleNet alice!alice@host\r\n",
			want: &Message{
				Prefix:  "irc.example.org",
				Command: "001",
				Params:  []string{"alice", "Welcome to ExampleNet alice!alice@host"},
			},
		},
		{
			name: "lowercase command normalized",
			in:   "ping irc.example.org\r\n",
			want: &Message{Command: "PING", Params: []string{"irc.example.org"}},
		},
		{
			name: "tags only",
			in:   "@time=2026-04-06T18:00:00Z PRIVMSG #foo :hi\r\n",
			want: &Message{
				Tags:    map[string]string{"time": "2026-04-06T18:00:00Z"},
				Command: "PRIVMSG",
				Params:  []string{"#foo", "hi"},
			},
		},
		{
			name: "tags with prefix",
			in:   "@id=abc :alice!a@h PRIVMSG #foo :hi\r\n",
			want: &Message{
				Tags:    map[string]string{"id": "abc"},
				Prefix:  "alice!a@h",
				Command: "PRIVMSG",
				Params:  []string{"#foo", "hi"},
			},
		},
		{
			name: "tag escapes round-trip",
			in:   `@msgid=foo\sbar\:baz PRIVMSG #x :hi` + "\r\n",
			want: &Message{
				Tags:    map[string]string{"msgid": "foo bar;baz"},
				Command: "PRIVMSG",
				Params:  []string{"#x", "hi"},
			},
		},
		{
			name: "empty tag value",
			in:   "@bot PRIVMSG #x :hi\r\n",
			want: &Message{
				Tags:    map[string]string{"bot": ""},
				Command: "PRIVMSG",
				Params:  []string{"#x", "hi"},
			},
		},
		{
			name: "bare LF accepted",
			in:   "PING\n",
			want: &Message{Command: "PING"},
		},
		{
			name: "no terminator at all",
			in:   "PING",
			want: &Message{Command: "PING"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse([]byte(tc.in))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("\n got: %#v\nwant: %#v", got, tc.want)
			}
		})
	}
}

func TestParse_Errors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"empty", "\r\n", ErrEmpty},
		{"empty after prefix", ":alice ", ErrEmpty},
		{"empty tags", "@ PRIVMSG x :y\r\n", ErrBadTags},
		{"unterminated tags", "@key=value", ErrBadTags},
		{"bad command digits", "12 alice\r\n", ErrBadCommand},
		{"bad command symbols", "PR1V alice\r\n", ErrBadCommand},
		{"too long", strings.Repeat("a", MaxMessageBytes+1), ErrTooLong},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.in))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParse_AtMaxLengthAccepted(t *testing.T) {
	// Build a 512-byte message exactly: "PRIVMSG #x :" + 498 'a' + "\r\n"
	body := strings.Repeat("a", MaxMessageBytes-len("PRIVMSG #x :\r\n"))
	line := "PRIVMSG #x :" + body + "\r\n"
	if len(line) != MaxMessageBytes {
		t.Fatalf("test setup: line len = %d, want %d", len(line), MaxMessageBytes)
	}
	m, err := Parse([]byte(line))
	if err != nil {
		t.Fatalf("Parse at max length: %v", err)
	}
	if m.Params[1] != body {
		t.Errorf("trailing length mismatch")
	}
}

func TestParse_FifteenParams(t *testing.T) {
	line := "CMD 1 2 3 4 5 6 7 8 9 10 11 12 13 14 :rest with spaces\r\n"
	m, err := Parse([]byte(line))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Params) != 15 {
		t.Fatalf("len(Params) = %d, want 15", len(m.Params))
	}
	if m.Params[14] != "rest with spaces" {
		t.Errorf("trailing = %q", m.Params[14])
	}
}

func TestParse_FifteenParamsNoColonOnLast(t *testing.T) {
	// 14 middles + a final token without the leading colon. RFC 2812
	// §2.3.1 form 2 says this is valid.
	line := "CMD 1 2 3 4 5 6 7 8 9 10 11 12 13 14 fifteen\r\n"
	m, err := Parse([]byte(line))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Params) != 15 || m.Params[14] != "fifteen" {
		t.Errorf("params = %v", m.Params)
	}
}

func TestMessage_Numeric(t *testing.T) {
	if !(&Message{Command: "001"}).Numeric() {
		t.Error("001 should be numeric")
	}
	if (&Message{Command: "PING"}).Numeric() {
		t.Error("PING should not be numeric")
	}
	if (&Message{Command: "01a"}).Numeric() {
		t.Error("01a should not be numeric")
	}
}
