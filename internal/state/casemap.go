// Package state holds ircat's authoritative in-memory runtime state:
// users, channels, modes, bans, and the lookup tables that the
// command handlers and the federation router consume.
//
// The package is split across files by concern. casemap.go owns the
// nickname/channel name canonicalization the rest of the package
// uses as a lookup key.
package state

// CaseMapping describes the algorithm used to fold a nickname or
// channel name into its canonical comparison form. RFC 1459 §2.2
// defines two: "ascii" (just lowercase A-Z) and "rfc1459" (also folds
// {[, }], |\\, ~^). The rfc1459 mapping is the historical default
// and what every legacy ircd advertises in RPL_ISUPPORT, so it's
// also our default.
type CaseMapping int

const (
	// CaseMappingRFC1459 folds A-Z plus the four extra punctuation
	// pairs:
	//
	//   '[' <-> '{'
	//   ']' <-> '}'
	//   '\' <-> '|'
	//   '^' <-> '~'
	//
	// This is what RFC 1459 §2.2 prescribes and what we advertise as
	// CASEMAPPING=rfc1459 in RPL_ISUPPORT.
	CaseMappingRFC1459 CaseMapping = iota

	// CaseMappingASCII folds only A-Z to a-z. Operators can opt into
	// this via config; clients still see CASEMAPPING=ascii in
	// RPL_ISUPPORT so they don't normalize wrong.
	CaseMappingASCII
)

// String returns the RPL_ISUPPORT-friendly name for the mapping.
func (m CaseMapping) String() string {
	switch m {
	case CaseMappingASCII:
		return "ascii"
	default:
		return "rfc1459"
	}
}

// Fold returns s in its canonical comparison form for the mapping.
// Use the returned value as the key for nickname or channel maps;
// keep the original casing in the user-facing struct.
func (m CaseMapping) Fold(s string) string {
	if s == "" {
		return s
	}
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		b[i] = m.foldByte(s[i])
	}
	return string(b)
}

func (m CaseMapping) foldByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	if m == CaseMappingASCII {
		return c
	}
	switch c {
	case '[':
		return '{'
	case ']':
		return '}'
	case '\\':
		return '|'
	case '^':
		return '~'
	}
	return c
}

// Equal reports whether a and b refer to the same name under the
// mapping. Useful for one-off comparisons; for repeated lookups, fold
// once and compare folded keys.
func (m CaseMapping) Equal(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		if m.foldByte(a[i]) != m.foldByte(b[i]) {
			return false
		}
	}
	return true
}
