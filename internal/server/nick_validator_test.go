package server

import "testing"

// TestValidNickname locks in the allowlist semantics. The audit pass
// flagged that "control characters might slip through" but isNickByte
// is allowlist-based, so any byte outside [A-Za-z0-9] and the small
// punctuation set falls through to the default-reject branch. This
// test pins the behaviour so a future refactor that flips the switch
// to a denylist cannot regress it.
func TestValidNickname(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"alice", true},
		{"Alice", true},
		{"_spike", true},
		{"foo[bar]", true},
		{"a|b", true},
		{"", false},
		{"-leading", false},
		{"9leading", false},
		{"has space", false},
		{"has,comma", false},
		{"bell\x07x", false},
		{"null\x00x", false},
		{"esc\x1bx", false},
		{"del\x7fx", false},
		{"high\xffbyte", false},
		{"newline\nx", false},
		{"return\rx", false},
	}
	for _, tc := range cases {
		got := validNickname(tc.name, 30)
		if got != tc.want {
			t.Errorf("validNickname(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestValidNickname_LengthCap(t *testing.T) {
	if !validNickname("abcdefghij", 10) {
		t.Errorf("10-byte nick should fit a 10-byte cap")
	}
	if validNickname("abcdefghijk", 10) {
		t.Errorf("11-byte nick should not fit a 10-byte cap")
	}
}
