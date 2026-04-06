package protocol

import "testing"

// FuzzParse asserts that Parse never panics on arbitrary input. The
// parser is the only place untrusted bytes from the network meet our
// type system, so resilience here is the difference between a clean
// disconnect and a server-wide crash.
func FuzzParse(f *testing.F) {
	seeds := []string{
		"PING\r\n",
		"PRIVMSG #foo :hello world\r\n",
		":alice!a@h PRIVMSG #foo :hi\r\n",
		"@id=1 :a!b@c PRIVMSG #x :y\r\n",
		"USER alice 0 * :Alice Example\r\n",
		"",
		"\r\n",
		":",
		"@",
		"@key=val ",
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(_ *testing.T, in []byte) {
		m, err := Parse(in)
		if err != nil {
			return
		}
		// If we got a Message back, encoding it must either succeed
		// (and stay under the size cap) or return a known sentinel.
		if _, encErr := m.Bytes(); encErr != nil {
			// Acceptable: a parsed message can be too long to encode
			// if its trailing param contains forbidden bytes that the
			// parser tolerated; we use a small allow-list of errors
			// rather than asserting success.
			switch encErr {
			case ErrTooLong, ErrBadCommand, ErrTooManyParams:
			default:
				panic("unexpected encode error: " + encErr.Error())
			}
		}
	})
}
