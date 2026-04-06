package state

import "testing"

func TestCaseMappingRFC1459_FoldsLetters(t *testing.T) {
	got := CaseMappingRFC1459.Fold("Alice")
	if got != "alice" {
		t.Errorf("Fold(Alice) = %q, want alice", got)
	}
}

func TestCaseMappingRFC1459_FoldsPunctuation(t *testing.T) {
	cases := []struct{ in, want string }{
		// Each "uppercase" punctuation maps to its "lowercase"
		// counterpart per RFC 1459 §2.2.
		{`Foo[`, `foo{`},
		{`Foo]`, `foo}`},
		{`Foo\`, `foo|`},
		{`Foo^`, `foo~`},
		// Mixed real-world example: a nick legal in old IRC.
		{`{Nick}`, `{nick}`},
		{`[Nick]`, `{nick}`},
	}
	for _, tc := range cases {
		got := CaseMappingRFC1459.Fold(tc.in)
		if got != tc.want {
			t.Errorf("Fold(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCaseMappingASCII_DoesNotTouchPunctuation(t *testing.T) {
	got := CaseMappingASCII.Fold(`Foo[Bar]`)
	if got != `foo[bar]` {
		t.Errorf("Fold = %q, want foo[bar]", got)
	}
}

func TestCaseMappingRFC1459_Equal(t *testing.T) {
	if !CaseMappingRFC1459.Equal(`{Alice}`, `[alice]`) {
		t.Error(`{Alice} should equal [alice] under rfc1459`)
	}
	if CaseMappingRFC1459.Equal("alice", "bob") {
		t.Error("alice should not equal bob")
	}
	if CaseMappingRFC1459.Equal("alice", "alic") {
		t.Error("length mismatch should not be equal")
	}
}

func TestCaseMappingASCII_Equal(t *testing.T) {
	if CaseMappingASCII.Equal(`{Alice}`, `[alice]`) {
		t.Error(`{Alice} should NOT equal [alice] under ascii`)
	}
	if !CaseMappingASCII.Equal("Alice", "alice") {
		t.Error("Alice should equal alice under ascii")
	}
}

func TestCaseMappingString(t *testing.T) {
	if CaseMappingRFC1459.String() != "rfc1459" {
		t.Errorf("rfc1459.String() = %q", CaseMappingRFC1459.String())
	}
	if CaseMappingASCII.String() != "ascii" {
		t.Errorf("ascii.String() = %q", CaseMappingASCII.String())
	}
}

func TestFold_EmptyString(t *testing.T) {
	if got := CaseMappingRFC1459.Fold(""); got != "" {
		t.Errorf("Fold(\"\") = %q", got)
	}
}

func TestFold_NonAlphaPassthrough(t *testing.T) {
	got := CaseMappingRFC1459.Fold("123-_.")
	if got != "123-_." {
		t.Errorf("Fold(123-_.) = %q", got)
	}
}
