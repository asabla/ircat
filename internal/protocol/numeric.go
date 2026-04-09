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

	// --- Operator promotion ---
	RPL_YOUREOPER  = "381" // ":You are now an IRC operator"
	ERR_NOOPERHOST = "491" // ":No O-lines for your host"

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

	// --- WHOIS (311-319) ---
	RPL_WHOISUSER     = "311" // "<nick> <user> <host> * :<real name>"
	RPL_WHOISSERVER   = "312" // "<nick> <server> :<server info>"
	RPL_WHOISOPERATOR = "313" // "<nick> :is an IRC operator"
	RPL_WHOWASUSER    = "314" // "<nick> <user> <host> * :<real name>"
	RPL_ENDOFWHO      = "315" // "<name> :End of WHO list"
	RPL_WHOISIDLE     = "317" // "<nick> <integer> :seconds idle"
	RPL_ENDOFWHOIS    = "318" // "<nick> :End of WHOIS list"
	RPL_WHOISCHANNELS = "319" // "<nick> :*( ( '@' / '+' ) <channel> ' ' )"

	// --- LIST (321-323) ---
	RPL_LISTSTART = "321" // "Channel :Users  Name"
	RPL_LIST      = "322" // "<channel> <# visible> :<topic>"
	RPL_LISTEND   = "323" // ":End of LIST"

	// --- Channel mode + creation (324, 329) ---
	RPL_CHANNELMODEIS = "324" // "<channel> <mode> <mode params>"
	RPL_CREATIONTIME  = "329" // "<channel> <unix-ts>"

	// --- TOPIC (331-333) ---
	RPL_NOTOPIC      = "331" // "<channel> :No topic is set"
	RPL_TOPIC        = "332" // "<channel> :<topic>"
	RPL_TOPICWHOTIME = "333" // "<channel> <nick> <unix-ts>"

	// --- INVITE / list-mode replies (341, 346-349, 367, 368) ---
	RPL_INVITING        = "341" // "<channel> <nick>"
	RPL_INVITELIST      = "346"
	RPL_ENDOFINVITELIST = "347"
	RPL_EXCEPTLIST      = "348"
	RPL_ENDOFEXCEPTLIST = "349"
	RPL_BANLIST         = "367" // "<channel> <banmask>"
	RPL_ENDOFBANLIST    = "368" // "<channel> :End of channel ban list"

	// --- WHO (352) ---
	RPL_WHOREPLY = "352" // "<channel> <user> <host> <server> <nick> <flags> :<hopcount> <real name>"

	// --- NAMES (353, 366) ---
	RPL_NAMREPLY   = "353" // "<symbol> <channel> :<prefix-nick> ..."
	RPL_ENDOFNAMES = "366" // "<channel> :End of NAMES list"

	// --- Generic "no such" errors ---
	ERR_NOSUCHNICK       = "401" // "<nick> :No such nick/channel"
	ERR_NOSUCHSERVER     = "402"
	ERR_NOSUCHCHANNEL    = "403" // "<channel> :No such channel"
	ERR_CANNOTSENDTOCHAN = "404" // "<channel> :Cannot send to channel"
	ERR_TOOMANYCHANNELS  = "405"

	// --- Message addressing errors ---
	ERR_NORECIPIENT  = "411" // ":No recipient given (<command>)"
	ERR_NOTEXTTOSEND = "412" // ":No text to send"

	// --- Channel-user state errors ---
	ERR_USERNOTINCHANNEL = "441" // "<nick> <channel> :They aren't on that channel"
	ERR_NOTONCHANNEL     = "442" // "<channel> :You're not on that channel"
	ERR_USERONCHANNEL    = "443" // "<user> <channel> :is already on channel"

	// --- JOIN failure modes ---
	ERR_CHANNELISFULL  = "471" // "<channel> :Cannot join channel (+l)"
	ERR_UNKNOWNMODE    = "472" // "<char> :is unknown mode char"
	ERR_INVITEONLYCHAN = "473" // "<channel> :Cannot join channel (+i)"
	ERR_BANNEDFROMCHAN = "474" // "<channel> :Cannot join channel (+b)"
	ERR_BADCHANNELKEY  = "475" // "<channel> :Cannot join channel (+k)"

	// --- Operator privileges ---
	ERR_NOPRIVILEGES     = "481" // ":Permission Denied- You're not an IRC operator"
	ERR_CHANOPRIVSNEEDED = "482" // "<channel> :You're not channel operator"
	ERR_CANTKILLSERVER   = "483"

	// --- LUSERS (251-255) ---
	RPL_LUSERCLIENT   = "251" // ":There are <int> users and <int> services on <int> servers"
	RPL_LUSEROP       = "252" // "<int> :operator(s) online"
	RPL_LUSERUNKNOWN  = "253" // "<int> :unknown connection(s)"
	RPL_LUSERCHANNELS = "254" // "<int> :channels formed"
	RPL_LUSERME       = "255" // ":I have <int> clients and <int> servers"

	// --- ADMIN (256-259) ---
	RPL_ADMINME    = "256" // "<server> :Administrative info"
	RPL_ADMINLOC1  = "257" // ":<admin info>"
	RPL_ADMINLOC2  = "258" // ":<admin info>"
	RPL_ADMINEMAIL = "259" // ":<admin info>"

	// --- AWAY (301, 305, 306) ---
	RPL_AWAY     = "301" // "<nick> :<away message>"
	RPL_UNAWAY   = "305" // ":You are no longer marked as being away"
	RPL_NOWAWAY  = "306" // ":You have been marked as being away"

	// --- USERHOST / ISON (302, 303) ---
	RPL_USERHOST = "302" // ":<reply>{ <reply>}" — up to 5 nick=+|-host
	RPL_ISON     = "303" // ":<nick>{ <nick>}"

	// --- WHOWAS / END (314, 369, 406) ---
	RPL_ENDOFWHOWAS = "369" // "<nick> :End of WHOWAS"
	ERR_WASNOSUCHNICK = "406" // "<nick> :There was no such nickname"

	// --- VERSION / TIME / INFO (351, 391, 371-374) ---
	RPL_VERSION    = "351" // "<version>.<debuglevel> <server> :<comments>"
	RPL_TIME       = "391" // "<server> :<string showing server's local time>"
	RPL_INFO       = "371" // ":<string>"
	RPL_ENDOFINFO  = "374" // ":End of INFO list"

	// --- LINKS (364, 365) ---
	RPL_LINKS      = "364" // "<mask> <server> :<hopcount> <server info>"
	RPL_ENDOFLINKS = "365" // "<mask> :End of LINKS list"

	// --- STATS (211-219, 242, 243, 219) ---
	RPL_STATSLINKINFO = "211" // "<linkname> <sendq> <sent msgs> <sent kbytes> <recv msgs> <recv kbytes> <time open>"
	RPL_STATSCOMMANDS = "212" // "<command> <count> <byte count> <remote count>"
	RPL_ENDOFSTATS    = "219" // "<query> :End of STATS report"
	RPL_STATSUPTIME   = "242" // ":Server Up <days> days <hh>:<mm>:<ss>"
	RPL_STATSOLINE    = "243" // "O <hostmask> * <name>"

	// --- TRACE (200-209, 261, 262) ---
	RPL_TRACELINK     = "200" // "Link <version> <destination> <next server>"
	RPL_TRACECONNECTING = "201" // "Try. <class> <server>"
	RPL_TRACEHANDSHAKE  = "202" // "H.S. <class> <server>"
	RPL_TRACEUNKNOWN    = "203" // "???? <class> [<client IP>]"
	RPL_TRACEOPERATOR   = "204" // "Oper <class> <nick>"
	RPL_TRACEUSER       = "205" // "User <class> <nick>"
	RPL_TRACESERVER     = "206" // "Serv <class> <int>S <int>C <server> <nick!user|*!*>@<host|server> V<protocol version>"
	RPL_TRACECLASS      = "209" // "Class <class> <count>"
	RPL_TRACEEND        = "262" // "<server> <version>.<debuglevel> :End of TRACE"
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
