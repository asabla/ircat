package dashboard

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage"
)

// PageDeps is the small set of dependencies the dashboard pages
// need. It mirrors api.Options at the bare minimum that the
// templates require. cmd/ircat populates it from the same
// objects that api.New uses.
type PageDeps struct {
	Store      storage.Store
	World      *state.World
	ServerInfo PageServerInfo
	// Actuator provides the live actions the page handlers expose
	// via form posts (kick, etc.). Optional — without it the
	// corresponding buttons return 503.
	Actuator PageActuator
}

// PageServerInfo is the small interface the overview page reads.
// internal/server.Server satisfies it via its existing methods.
type PageServerInfo interface {
	ServerName() string
	NetworkName() string
	Version() string
	StartedAt() time.Time
	ListenerAddresses() []string
}

// PageActuator is the small interface the dashboard mutation
// handlers (kick, etc.) call into. internal/server.Server
// satisfies it via its existing KickUser method.
type PageActuator interface {
	KickUser(ctx context.Context, nick, reason string) error
}

// pageData is the per-request render struct. Every handler
// builds one of these via newPageData so the brand + nav +
// CSRF fields are filled in consistently.
type pageData struct {
	Title      string
	Operator   string
	CSRF       string
	NavActive  string // sidebar highlight key (overview, users, ...)
	ServerName string // small caption under the brand

	// per-page payloads
	Server       overviewPayload
	Cards        []cardPayload
	Users        []userPayload
	UserDetail   *userDetailPayload
	Channels     []channelPayload
	ChannelDetail *channelDetailPayload
	Federation   []fedLinkPayload
	Bots         []botPayload
	BotDetail    *botDetailPayload
	Operators    []operatorPayload
	Tokens       []tokenPayload
	Events       []eventPayload
	Error        string
	Flash        string
}

type overviewPayload struct {
	Name         string
	Network      string
	Version      string
	StartedAt    string
	UserCount    int
	ChannelCount int
	Listeners    []string
}

// cardPayload is one stat tile on the overview.
type cardPayload struct {
	Title string
	Value string
	Delta string // optional caption beneath the value
	Spark string // SVG fragment for the inline sparkline
}

type userPayload struct {
	Nick     string
	Hostmask string
	Modes    string
	Channels []string
}

type userDetailPayload struct {
	Nick       string
	Hostmask   string
	Modes      string
	HomeServer string
	Channels   []string
}

type channelPayload struct {
	Name        string
	MemberCount int
	ModeWord    string
	Topic       string
}

type channelDetailPayload struct {
	Name      string
	MemberCount int
	ModeWord  string
	Topic     string
	TopicSetBy string
	Members   []channelMemberPayload
	Bans      []string
}

type channelMemberPayload struct {
	Nick   string
	Prefix string // "@" / "+" / ""
	Remote bool
}

type fedLinkPayload struct {
	Peer        string
	State       string
	Description string
	Subscribed  []string
}

type botPayload struct {
	ID      string
	Name    string
	Enabled bool
	Status  string
}

type botDetailPayload struct {
	ID      string
	Name    string
	Enabled bool
	Source  string
	Status  string
}

type operatorPayload struct {
	Name      string
	HostMask  string
	Flags     []string
	CreatedAt string
}

type tokenPayload struct {
	ID         string
	Label      string
	CreatedAt  string
	LastUsedAt string
}

type eventPayload struct {
	Timestamp string
	Type      string
	Actor     string
	Target    string
	DataJSON  string
}

// newPageData builds a pre-filled pageData with the brand,
// session, CSRF, and nav fields populated. Every authenticated
// page handler should call this rather than constructing
// pageData{} directly so the sidebar highlight stays
// consistent.
func (s *Server) newPageData(sess *session, navActive, title string) *pageData {
	pd := &pageData{
		Title:     title,
		Operator:  sess.Operator,
		NavActive: navActive,
		CSRF:      s.csrfToken(sess),
	}
	if s.pages != nil && s.pages.ServerInfo != nil {
		pd.ServerName = s.pages.ServerInfo.ServerName()
	}
	return pd
}

// csrfToken returns the per-session CSRF token via the session
// store. Templates render it via {{.CSRF}}.
func (s *Server) csrfToken(sess *session) string {
	if s.sessions == nil {
		return ""
	}
	return s.sessions.csrfToken(sess)
}

// checkCSRF verifies the supplied csrf field matches the per-session
// token. False means reject the request.
func (s *Server) checkCSRF(sess *session, supplied string) bool {
	if s.sessions == nil {
		return false
	}
	return s.sessions.checkCSRF(sess, supplied)
}

// requireSession is the page-level analogue of the api token
// middleware. Routes wrapped with it are only reachable if the
// caller has a valid session cookie; everyone else gets redirected
// to /login.
func (s *Server) requireSession(next func(*session, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess := s.sessions.extract(r)
		if sess == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(sess, w, r)
	}
}

// handleLoginGet renders the login form. If the caller is already
// logged in, it redirects to /dashboard.
func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	if sess := s.sessions.extract(r); sess != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	s.renderPage(w, "login", &pageData{Title: "sign in"})
}

// handleLoginPost verifies the submitted credentials against the
// operator store and either issues a session cookie or re-renders
// the login form with an error.
func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderPage(w, "login", &pageData{Title: "sign in", Error: "bad form data"})
		return
	}
	username := r.PostForm.Get("username")
	password := r.PostForm.Get("password")
	if username == "" || password == "" {
		s.renderPage(w, "login", &pageData{Title: "sign in", Error: "username and password required"})
		return
	}
	if s.pages == nil || s.pages.Store == nil {
		s.renderPage(w, "login", &pageData{Title: "sign in", Error: "storage not configured"})
		return
	}
	op, err := s.pages.Store.Operators().Get(r.Context(), username)
	if err != nil {
		// Same generic error for both missing and storage failure so
		// the form does not leak which case it is.
		s.renderPage(w, "login", &pageData{Title: "sign in", Error: "invalid credentials"})
		return
	}
	ok, _ := auth.Verify(op.PasswordHash, password)
	if !ok {
		s.renderPage(w, "login", &pageData{Title: "sign in", Error: "invalid credentials"})
		return
	}
	s.sessions.issue(w, op.Name)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.sessions.clear(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleOverview(sess *session, w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(sess, "overview", "overview")
	s.fillOverview(data)
	s.renderPage(w, "overview", data)
}

// handleOverviewCards renders just the overview-cards block,
// not the surrounding chrome. The htmx attribute on the cards
// container polls this endpoint every 5s and swaps the
// returned fragment in place — that is the entire live-refresh
// mechanism on the overview page.
func (s *Server) handleOverviewCards(sess *session, w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(sess, "overview", "overview")
	s.fillOverview(data)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.renderPartial(w, "overview", "overview-cards", data); err != nil {
		s.logger.Warn("template render failed", "page", "overview-cards", "error", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// fillOverview populates the Server + Cards payloads. Extracted
// so the partial-refresh handler can reuse it without
// re-rendering the surrounding chrome.
func (s *Server) fillOverview(data *pageData) {
	if s.pages != nil && s.pages.ServerInfo != nil {
		data.Server.Name = s.pages.ServerInfo.ServerName()
		data.Server.Network = s.pages.ServerInfo.NetworkName()
		data.Server.Version = s.pages.ServerInfo.Version()
		data.Server.StartedAt = s.pages.ServerInfo.StartedAt().UTC().Format(time.RFC3339)
		data.Server.Listeners = s.pages.ServerInfo.ListenerAddresses()
	}
	if s.pages != nil && s.pages.World != nil {
		data.Server.UserCount = s.pages.World.UserCount()
		data.Server.ChannelCount = s.pages.World.ChannelCount()
	}
	data.Cards = s.buildOverviewCards()
}

func (s *Server) handleUsersPage(sess *session, w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(sess, "users", "users")
	if s.pages != nil && s.pages.World != nil {
		snap := s.pages.World.Snapshot()
		for _, u := range snap {
			pl := userPayload{
				Nick:     u.Nick,
				Hostmask: u.Hostmask(),
				Modes:    u.Modes,
			}
			for _, ch := range s.pages.World.UserChannels(u.ID) {
				pl.Channels = append(pl.Channels, ch.Name())
			}
			sort.Strings(pl.Channels)
			data.Users = append(data.Users, pl)
		}
		sort.Slice(data.Users, func(i, j int) bool { return data.Users[i].Nick < data.Users[j].Nick })
	}
	s.renderPage(w, "users", data)
}

func (s *Server) handleChannelsPage(sess *session, w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(sess, "channels", "channels")
	if s.pages != nil && s.pages.World != nil {
		for _, ch := range s.pages.World.ChannelsSnapshot() {
			modes, _ := ch.ModeString()
			topic, _, _ := ch.Topic()
			data.Channels = append(data.Channels, channelPayload{
				Name:        ch.Name(),
				MemberCount: ch.MemberCount(),
				ModeWord:    modes,
				Topic:       topic,
			})
		}
		sort.Slice(data.Channels, func(i, j int) bool { return data.Channels[i].Name < data.Channels[j].Name })
	}
	s.renderPage(w, "channels", data)
}

func (s *Server) handleOperatorsPage(sess *session, w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(sess, "operators", "operators")
	if s.pages != nil && s.pages.Store != nil {
		ops, err := s.pages.Store.Operators().List(r.Context())
		if err == nil {
			for _, op := range ops {
				data.Operators = append(data.Operators, operatorPayload{
					Name:      op.Name,
					HostMask:  op.HostMask,
					Flags:     op.Flags,
					CreatedAt: op.CreatedAt.UTC().Format(time.RFC3339),
				})
			}
		}
	}
	s.renderPage(w, "operators", data)
}

// handleKickUserPage is the dashboard form post that mirrors the
// API kick path. The form template lives in templates/users.html.
func (s *Server) handleKickUserPage(sess *session, w http.ResponseWriter, r *http.Request) {
	nick := r.PathValue("nick")
	if s.pages == nil || s.pages.Actuator == nil {
		http.Error(w, "kick disabled", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !s.checkCSRF(sess, r.PostForm.Get("csrf")) {
		http.Error(w, "bad csrf token", http.StatusForbidden)
		return
	}
	reason := r.PostForm.Get("reason")
	if reason == "" {
		reason = "Kicked from dashboard by " + sess.Operator
	}
	if err := s.pages.Actuator.KickUser(r.Context(), nick, reason); err != nil {
		s.logger.Warn("dashboard kick failed", "nick", nick, "error", err)
		http.Error(w, "kick failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard/users", http.StatusSeeOther)
}

func (s *Server) handleEventsPage(sess *session, w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(sess, "events", "audit log")
	if s.pages != nil && s.pages.Store != nil {
		events, err := s.pages.Store.Events().List(r.Context(), storage.ListEventsOptions{Limit: 50})
		if err == nil {
			for _, e := range events {
				data.Events = append(data.Events, eventPayload{
					Timestamp: e.Timestamp.UTC().Format(time.RFC3339),
					Type:      e.Type,
					Actor:     e.Actor,
					Target:    e.Target,
					DataJSON:  e.DataJSON,
				})
			}
		}
	}
	s.renderPage(w, "events", data)
}

// renderPage centralizes the template lookup + write so handlers
// stay focused on data assembly. On a render error it logs and
// writes a tiny plain-text fallback.
func (s *Server) renderPage(w http.ResponseWriter, name string, data any) {
	if s.tmpl == nil {
		http.Error(w, "templates not loaded", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.render(w, name, data); err != nil {
		s.logger.Warn("template render failed", "page", name, "error", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

// buildOverviewCards turns the live MetricsSource into a slice
// of stat cards rendered on the overview page. The sparkline
// SVG is filled in by the M13 follow-up that adds the
// per-metric rolling sample buffer; for now we render a flat
// dash to keep the layout stable.
func (s *Server) buildOverviewCards() []cardPayload {
	if s.metrics == nil {
		return nil
	}
	out := []cardPayload{
		{Title: "users", Value: itoa(s.metrics.UserCount())},
		{Title: "channels", Value: itoa(s.metrics.ChannelCount())},
		{Title: "federation links", Value: itoa(s.metrics.FederationLinkCount())},
		{Title: "bots", Value: itoa(s.metrics.BotCount())},
		{Title: "messages in", Value: u64toa(s.metrics.MessagesIn())},
		{Title: "messages out", Value: u64toa(s.metrics.MessagesOut())},
	}
	return out
}

func itoa(n int) string  { return strconvItoa(int64(n)) }
func u64toa(n uint64) string { return strconvItoa(int64(n)) }
func strconvItoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// silence unused-import warnings while iterating
var _ = context.Background
var _ = errors.New
