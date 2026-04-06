package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestBytes_CanonicalForm(t *testing.T) {
	// Each case is (input, canonical-encoded form). The encoder
	// always emits the last parameter in trailing-colon form, so
	// inputs that used the bare-token form get rewritten on encode.
	cases := []struct {
		in, want string
	}{
		{"PING\r\n", "PING\r\n"},
		{"NICK alice\r\n", "NICK :alice\r\n"},
		{"USER alice 0 * :Alice Example\r\n", "USER alice 0 * :Alice Example\r\n"},
		{"PRIVMSG #foo :hello world\r\n", "PRIVMSG #foo :hello world\r\n"},
		{"PRIVMSG #foo hi\r\n", "PRIVMSG #foo :hi\r\n"},
		{":alice!a@h PRIVMSG #foo :hi\r\n", ":alice!a@h PRIVMSG #foo :hi\r\n"},
		{":irc.example.org 001 alice :Welcome to ExampleNet alice!a@h\r\n",
			":irc.example.org 001 alice :Welcome to ExampleNet alice!a@h\r\n"},
		{"PRIVMSG #foo ::leading colon kept\r\n", "PRIVMSG #foo ::leading colon kept\r\n"},
		{"PRIVMSG #foo :\r\n", "PRIVMSG #foo :\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			m, err := Parse([]byte(tc.in))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			out, err := m.Bytes()
			if err != nil {
				t.Fatalf("Bytes: %v", err)
			}
			if string(out) != tc.want {
				t.Errorf("\n got: %q\nwant: %q", out, tc.want)
			}
			// Re-parse the encoded form and assert structural equality.
			m2, err := Parse(out)
			if err != nil {
				t.Fatalf("re-Parse: %v", err)
			}
			if m2.Command != m.Command || len(m2.Params) != len(m.Params) {
				t.Errorf("structural mismatch after re-parse")
			}
		})
	}
}

func TestBytes_TagsRoundtrip(t *testing.T) {
	in := `@id=abc;msgid=foo\sbar\:baz :alice!a@h PRIVMSG #x :hi` + "\r\n"
	m, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	out, err := m.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	// Tag order is sorted alphabetically on encode for determinism;
	// "id" sorts before "msgid". The trailing param is always
	// emitted with a leading colon.
	want := `@id=abc;msgid=foo\sbar\:baz :alice!a@h PRIVMSG #x :hi` + "\r\n"
	if string(out) != want {
		t.Errorf("\n got: %q\nwant: %q", out, want)
	}
}

func TestBytes_RejectsBadCommand(t *testing.T) {
	if _, err := (&Message{Command: ""}).Bytes(); !errors.Is(err, ErrBadCommand) {
		t.Errorf("empty command: err = %v", err)
	}
	if _, err := (&Message{Command: "PING1"}).Bytes(); !errors.Is(err, ErrBadCommand) {
		t.Errorf("alpha+digit: err = %v", err)
	}
	if _, err := (&Message{Command: "12"}).Bytes(); !errors.Is(err, ErrBadCommand) {
		t.Errorf("two-digit numeric: err = %v", err)
	}
}

func TestBytes_RejectsBadMiddleParam(t *testing.T) {
	m := &Message{Command: "PRIVMSG", Params: []string{"with space", "ok"}}
	if _, err := m.Bytes(); err == nil {
		t.Error("expected error for space in middle param")
	}
}

func TestBytes_TrailingWithSpacesGetsColon(t *testing.T) {
	m := &Message{Command: "PRIVMSG", Params: []string{"#foo", "hello world"}}
	out, err := m.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(out), ":hello world\r\n") {
		t.Errorf("got %q", out)
	}
}

func TestBytes_RejectsTooLong(t *testing.T) {
	body := strings.Repeat("a", MaxMessageBytes) // overshoots once CRLF added
	m := &Message{Command: "PRIVMSG", Params: []string{"#x", body}}
	if _, err := m.Bytes(); !errors.Is(err, ErrTooLong) {
		t.Errorf("err = %v", err)
	}
}

func TestBytes_TooManyParams(t *testing.T) {
	params := make([]string, MaxParams+1)
	for i := range params {
		params[i] = "a"
	}
	m := &Message{Command: "CMD", Params: params}
	if _, err := m.Bytes(); !errors.Is(err, ErrTooManyParams) {
		t.Errorf("err = %v", err)
	}
}
