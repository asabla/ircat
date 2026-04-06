package protocol

// Numeric reply codes from RFC 1459 §6 and RFC 2812 §5.
//
// Naming follows the canonical RFC2812 names so anyone familiar with
// IRC literature can grep for, e.g., RPL_WELCOME and find what they
// expect. Codes are kept as untyped string constants because they
// land directly in [Message.Command].
//
// Only the codes ircat actually emits are listed; we add to this file
// as new commands come online. Adding a constant here is the signal
// that the corresponding behaviour exists and has tests.
const (
	// --- Connection registration (001-005) ---
	RPL_WELCOME  = "001" // ":Welcome to the <network> Network, <nick>!<user>@<host>"
	RPL_YOURHOST = "002" // ":Your host is <servername>, running version <ver>"
	RPL_CREATED  = "003" // ":This server was created <date>"
	RPL_MYINFO   = "004" // "<servername> <version> <umodes> <cmodes>"
	RPL_ISUPPORT = "005" // "<token>... :are supported by this server"

	// --- MOTD (372-376, 422) ---
	RPL_MOTDSTART = "375" // ":- <server> Message of the day -"
	RPL_MOTD      = "372" // ":- <text>"
	RPL_ENDOFMOTD = "376" // ":End of MOTD command"
	ERR_NOMOTD    = "422" // ":MOTD File is missing"

	// --- Generic errors used during registration ---
	ERR_NONICKNAMEGIVEN  = "431" // ":No nickname given"
	ERR_ERRONEUSNICKNAME = "432" // "<nick> :Erroneous nickname"
	ERR_NICKNAMEINUSE    = "433" // "<nick> :Nickname is already in use"
	ERR_NOTREGISTERED    = "451" // ":You have not registered"
	ERR_NEEDMOREPARAMS   = "461" // "<command> :Not enough parameters"
	ERR_ALREADYREGISTRED = "462" // ":You may not reregister"
	ERR_PASSWDMISMATCH   = "464" // ":Password incorrect"

	// --- Quit / disconnect ---
	ERR_UNKNOWNCOMMAND = "421" // "<command> :Unknown command"
)

// NumericReply builds a server-originated numeric reply Message.
//
// Conventions enforced here:
//   - The first parameter is always the recipient nick (or "*" for
//     pre-registration clients), per RFC 2812 §2.4.
//   - The remaining params are appended in order; the *last* one will
//     be encoded as the trailing parameter (with a leading colon).
//
// Example:
//
//	NumericReply(serverName, "alice", RPL_WELCOME,
//	    "Welcome to ExampleNet alice!alice@host")
//
// produces:
//
//	:irc.example.org 001 alice :Welcome to ExampleNet alice!alice@host\r\n
func NumericReply(serverName, target, code string, params ...string) *Message {
	if target == "" {
		target = "*"
	}
	all := make([]string, 0, 1+len(params))
	all = append(all, target)
	all = append(all, params...)
	return &Message{
		Prefix:  serverName,
		Command: code,
		Params:  all,
	}
}
