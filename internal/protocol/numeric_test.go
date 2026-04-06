package protocol

import "testing"

func TestNumericReply_BuildsExpectedWire(t *testing.T) {
	m := NumericReply("irc.example.org", "alice", RPL_WELCOME,
		"Welcome to ExampleNet alice!alice@host")
	out, err := m.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	want := ":irc.example.org 001 alice :Welcome to ExampleNet alice!alice@host\r\n"
	if string(out) != want {
		t.Errorf("\n got: %q\nwant: %q", out, want)
	}
}

func TestNumericReply_PreRegistrationStarTarget(t *testing.T) {
	m := NumericReply("irc.example.org", "", ERR_NONICKNAMEGIVEN, "No nickname given")
	out, err := m.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	want := ":irc.example.org 431 * :No nickname given\r\n"
	if string(out) != want {
		t.Errorf("\n got: %q\nwant: %q", out, want)
	}
}

func TestNumericReply_RPL_MYINFO(t *testing.T) {
	m := NumericReply("irc.example.org", "alice", RPL_MYINFO,
		"irc.example.org", "ircat-0.0.1", "iwo", "ovntimk")
	if !m.Numeric() {
		t.Errorf("004 should be numeric")
	}
	if len(m.Params) != 5 {
		t.Errorf("len(Params) = %d, want 5", len(m.Params))
	}
}
