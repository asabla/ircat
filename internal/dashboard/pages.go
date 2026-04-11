package dashboard

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
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
	// Federation lists the active links for the federation
	// page. Optional — without it the page renders the empty
	// state.
	Federation FederationLister
	// Bots is the supervisor surface used by the bots CRUD
	// pages. Optional — without it the form posts return 503.
	Bots BotManager
	// BotValidator is the pure-function pre-save Lua
	// syntax-check hook the bot edit page calls into. Matches
	// internal/bots.Validate. Optional — without it the
	// Validate button returns 503 and the Save path skips the
	// pre-check (today's destructive behaviour).
	BotValidator func(source string) error
	// BotLogs is the per-bot log tail source the bot detail
	// page's SSE pane subscribes to. internal/bots.Supervisor
	// satisfies it via BotLogsSince. Optional — without it the
	// log pane renders the empty state and the SSE handler
	// returns 503.
	BotLogs BotLogSource
	// LogTail is the in-memory ring buffer the /dashboard/logs
	// SSE stream polls. Optional — without it the page renders
	// the empty state and the SSE handler returns 503.
	LogTail LogRing
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
// handlers (kick, set topic, etc.) call into. internal/server.Server
// satisfies it via the matching method names.
type PageActuator interface {
	KickUser(ctx context.Context, nick, reason string) error
	SetChannelTopic(ctx context.Context, channel, topic, setBy string) error
}

// BotManager is the small interface the dashboard bot CRUD
// pages call into. Same shape as the api package's BotManager
// — internal/bots.Supervisor satisfies both. We declare it
// twice (once per consumer) so neither package has to import
// the other for the type.
type BotManager interface {
	CreateBot(ctx context.Context, bot *storage.Bot) error
	UpdateBot(ctx context.Context, bot *storage.Bot) error
	DeleteBot(ctx context.Context, id string) error
}

// LogRing is the small read-only surface the live log tail
// page polls. internal/logging.RingBuffer satisfies it via the
// matching Since method declared on the entry type.
type LogRing interface {
	Since(seq uint64) []LogEntry
}

// BotLogSource is the read-only surface the per-bot SSE log
// pane subscribes to. internal/bots.Supervisor satisfies it
// via BotLogsSince. Defined here (rather than importing
// internal/bots) so the dashboard package does not depend on
// the runtime, matching the existing BotManager indirection.
type BotLogSource interface {
	BotLogsSince(id string, seq uint64) []BotLogEntry
}

// BotLogEntry is the dashboard-side projection of one log
// record emitted by a bot's ctx:log() call. The getters
// mirror LogEntry so the SSE handler can reuse the same
// JSON-emit helper for both sources.
// internal/bots.BotLogEntry satisfies this interface via
// value-receiver getters on the concrete type.
type BotLogEntry interface {
	Sequence() uint64
	Timestamp() time.Time
	LevelName() string
	MessageText() string
}

// LogEntry is the dashboard-side projection of one log record
// for the SSE stream. The four getters are exactly what the
// /dashboard/logs JS client renders. internal/logging.Entry
// satisfies it via getter methods declared in that package.
type LogEntry interface {
	Sequence() uint64
	Timestamp() time.Time
	LevelName() string
	MessageText() string
}

// FederationLister is the small read-only surface the
// /dashboard/federation page consults. internal/server.Server
// satisfies it via FederationSnapshot. Optional — when nil the
// page renders the empty state.
//
// The return type is a slice of FederationLinkRow rather than a
// concrete struct so the implementing package (internal/server)
// does not have to import internal/dashboard for a shared
// type. server uses its own value-type concrete row that
// happens to satisfy these four method signatures.
type FederationLister interface {
	FederationSnapshot() []FederationLinkRow
}

// FederationLinkRow is one row on the federation page. The
// methods are intentionally simple getters so a server-side
// struct can satisfy them with field accessors and the
// dashboard never reaches into the federation package.
type FederationLinkRow interface {
	Peer() string
	State() string
	Description() string
	Subscribed() []string
	SentMessages() uint64
	SentBytes() uint64
	RecvMessages() uint64
	RecvBytes() uint64
	OpenedAt() time.Time
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
	Server        overviewPayload
	Cards         []cardPayload
	Users         []userPayload
	UserDetail    *userDetailPayload
	Channels      []channelPayload
	ChannelDetail *channelDetailPayload
	Federation    []fedLinkPayload
	Bots          []botPayload
	BotDetail     *botDetailPayload
	Operators     []operatorPayload
	Tokens        []tokenPayload
	Events        []eventPayload
	Error         string
	Flash         string
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
	Delta string        // optional caption beneath the value
	Spark template.HTML // inline SVG fragment, not HTML-escaped
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
	Name        string
	MemberCount int
	ModeWord    string
	Topic       string
	TopicSetBy  string
	Members     []channelMemberPayload
	Bans        []string
	Exceptions  []string
	Invexes     []string
	Quiets      []string
}

type channelMemberPayload struct {
	Nick   string
	Prefix string // "@" / "+" / ""
	Remote bool
}

// sortedKeys returns the keys of a string-keyed map in lexical
// order. Used by the channel detail handler so the ban / exception /
// invex / quiet lists render deterministically across reloads.
func sortedKeys[V any](m map[string]V) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type fedLinkPayload struct {
	Peer         string
	State        string
	Description  string
	Subscribed   []string
	SentMessages uint64
	SentBytes    uint64
	RecvMessages uint64
	RecvBytes    uint64
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

// handleLogout drops the current session cookie. Wrapped in
// requireSession + carries a CSRF token in the form so an
// unrelated origin cannot force a logout via a cross-site form
// post (the same protection every other mutating dashboard
// route gets).
func (s *Server) handleLogout(sess *session, w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !s.checkCSRF(sess, r.PostForm.Get("csrf")) {
		http.Error(w, "bad csrf token", http.StatusForbidden)
		return
	}
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

// handleUserDetailPage renders the per-user page reachable
// from each row of the users table. The page shows identity,
// channel membership, and an inline kick form with an optional
// reason field.
func (s *Server) handleUserDetailPage(sess *session, w http.ResponseWriter, r *http.Request) {
	nick := r.PathValue("nick")
	data := s.newPageData(sess, "users", "user — "+nick)
	if s.pages != nil && s.pages.World != nil {
		u := s.pages.World.FindByNick(nick)
		if u == nil {
			http.Error(w, "no such user", http.StatusNotFound)
			return
		}
		detail := &userDetailPayload{
			Nick:       u.Nick,
			Hostmask:   u.Hostmask(),
			Modes:      u.Modes,
			HomeServer: u.HomeServer,
		}
		for _, ch := range s.pages.World.UserChannels(u.ID) {
			detail.Channels = append(detail.Channels, ch.Name())
		}
		sort.Strings(detail.Channels)
		data.UserDetail = detail
	}
	s.renderPage(w, "user_detail", data)
}

// handleBotsPage renders /dashboard/bots: a create form plus a
// list of every persisted bot with toggle/delete actions. The
// status pill comes from the supervisor's running set if it is
// wired; otherwise the row falls back to a static "configured"
// label.
func (s *Server) handleBotsPage(sess *session, w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(sess, "bots", "bots")
	s.fillBots(r.Context(), data)
	s.renderPage(w, "bots", data)
}

func (s *Server) fillBots(ctx context.Context, data *pageData) {
	if s.pages == nil || s.pages.Store == nil {
		return
	}
	bots, err := s.pages.Store.Bots().List(ctx)
	if err != nil {
		s.logger.Warn("dashboard list bots failed", "error", err)
		return
	}
	for _, b := range bots {
		row := botPayload{
			ID:      b.ID,
			Name:    b.Name,
			Enabled: b.Enabled,
		}
		data.Bots = append(data.Bots, row)
	}
	sort.Slice(data.Bots, func(i, j int) bool { return data.Bots[i].Name < data.Bots[j].Name })
}

// handleBotsCreate persists + starts a new bot via the
// supervisor's CreateBot. Source is taken verbatim from the
// textarea; the supervisor compiles it on insert and surfaces
// any compile error to the operator via the Flash field.
func (s *Server) handleBotsCreate(sess *session, w http.ResponseWriter, r *http.Request) {
	if s.pages == nil || s.pages.Bots == nil {
		http.Error(w, "bot manager not configured", http.StatusServiceUnavailable)
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
	name := strings.TrimSpace(r.PostForm.Get("name"))
	source := r.PostForm.Get("source")
	enabled := r.PostForm.Get("enabled") != ""
	if name == "" {
		s.renderBotsWithFlash(sess, w, r, "name is required")
		return
	}
	bot := &storage.Bot{
		Name:    name,
		Source:  source,
		Enabled: enabled,
	}
	if err := s.pages.Bots.CreateBot(r.Context(), bot); err != nil {
		s.renderBotsWithFlash(sess, w, r, "create failed: "+err.Error())
		return
	}
	http.Redirect(w, r, "/dashboard/bots", http.StatusSeeOther)
}

func (s *Server) renderBotsWithFlash(sess *session, w http.ResponseWriter, r *http.Request, flash string) {
	data := s.newPageData(sess, "bots", "bots")
	data.Flash = flash
	s.fillBots(r.Context(), data)
	s.renderPage(w, "bots", data)
}

// handleBotDetailPage renders the per-bot edit page: identity,
// status, and a textarea pre-filled with the current source.
func (s *Server) handleBotDetailPage(sess *session, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	data := s.newPageData(sess, "bots", "bot")
	if s.pages == nil || s.pages.Store == nil {
		http.Error(w, "store not configured", http.StatusServiceUnavailable)
		return
	}
	bot, err := s.pages.Store.Bots().Get(r.Context(), id)
	if err != nil {
		http.Error(w, "no such bot", http.StatusNotFound)
		return
	}
	data.Title = "bot — " + bot.Name
	data.BotDetail = &botDetailPayload{
		ID:      bot.ID,
		Name:    bot.Name,
		Enabled: bot.Enabled,
		Source:  bot.Source,
	}
	s.renderPage(w, "bot_detail", data)
}

// handleBotSourcePost saves a fresh copy of the bot source via
// the supervisor's UpdateBot, which compiles + hot-reloads the
// runtime in one shot.
func (s *Server) handleBotSourcePost(sess *session, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pages == nil || s.pages.Bots == nil || s.pages.Store == nil {
		http.Error(w, "bot manager not configured", http.StatusServiceUnavailable)
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
	bot, err := s.pages.Store.Bots().Get(r.Context(), id)
	if err != nil {
		http.Error(w, "no such bot", http.StatusNotFound)
		return
	}
	bot.Source = r.PostForm.Get("source")
	// Pre-save syntax check: if a validator is wired and the
	// source fails to compile, re-render the edit page with the
	// error and do NOT call UpdateBot. That keeps the previous
	// running source and stored record intact, which is the
	// whole point of the Validate button's lazy sibling — a
	// broken save should not replace a running bot.
	if s.pages.BotValidator != nil {
		if verr := s.pages.BotValidator(bot.Source); verr != nil {
			s.renderBotDetailWithFlash(sess, w, r, bot, "lua error: "+verr.Error())
			return
		}
	}
	if err := s.pages.Bots.UpdateBot(r.Context(), bot); err != nil {
		http.Error(w, "update failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard/bots/"+id, http.StatusSeeOther)
}

// renderBotDetailWithFlash re-renders the bot detail page with
// an error message in the Flash field. Used by the source POST
// handler so a validation or update failure does not redirect
// away from the textarea the operator was editing.
func (s *Server) renderBotDetailWithFlash(sess *session, w http.ResponseWriter, r *http.Request, bot *storage.Bot, flash string) {
	data := s.newPageData(sess, "bots", "bot — "+bot.Name)
	data.Flash = flash
	data.BotDetail = &botDetailPayload{
		ID:      bot.ID,
		Name:    bot.Name,
		Enabled: bot.Enabled,
		Source:  bot.Source,
	}
	s.renderPage(w, "bot_detail", data)
}

// handleBotValidatePost is the htmx target for the Validate
// button on the bot edit page. It returns a tiny HTML fragment
// (ok pill or error block) without touching storage. The
// fragment replaces the contents of the #validate-result div
// via hx-target.
func (s *Server) handleBotValidatePost(sess *session, w http.ResponseWriter, r *http.Request) {
	if s.pages == nil || s.pages.BotValidator == nil {
		http.Error(w, "validator not configured", http.StatusServiceUnavailable)
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
	source := r.PostForm.Get("source")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.pages.BotValidator(source); err != nil {
		w.Write([]byte(`<span class="pill warn">invalid</span> <code>` + htmlEscape(err.Error()) + `</code>`))
		return
	}
	w.Write([]byte(`<span class="pill ok">lua ok</span>`))
}

// handleBotLogsSSE streams the tail of one bot's ctx:log()
// output to the matching pane on the bot detail page. The
// shape mirrors handleLogsSSE exactly: seed the client with
// the current ring contents, then poll every
// botLogsPollInterval for anything newer and emit it as an
// SSE data frame.
//
// Returns 503 if no BotLogs source is wired (test harness) or
// 404 if the supplied id is not currently running — the ring
// lives on the session, so a stopped bot has nothing to
// stream. That mirrors the per-bot toggle/source posts which
// also require the session to be live.
func (s *Server) handleBotLogsSSE(sess *session, w http.ResponseWriter, r *http.Request) {
	if s.pages == nil || s.pages.BotLogs == nil {
		http.Error(w, "bot logs not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	var lastSeq uint64
	backlog := s.pages.BotLogs.BotLogsSince(id, 0)
	for _, e := range backlog {
		if err := writeBotLogSSE(w, e); err != nil {
			return
		}
		if e.Sequence() > lastSeq {
			lastSeq = e.Sequence()
		}
	}
	flusher.Flush()

	ticker := time.NewTicker(botLogsPollInterval)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			entries := s.pages.BotLogs.BotLogsSince(id, lastSeq)
			if len(entries) == 0 {
				if _, err := w.Write([]byte(": ping\n\n")); err != nil {
					return
				}
				flusher.Flush()
				continue
			}
			for _, e := range entries {
				if err := writeBotLogSSE(w, e); err != nil {
					return
				}
				if e.Sequence() > lastSeq {
					lastSeq = e.Sequence()
				}
			}
			flusher.Flush()
		}
	}
}

// botLogsPollInterval matches logsPollInterval: the two SSE
// handlers run off the same cadence so the bot detail pane
// feels identical to the server-wide log tail.
const botLogsPollInterval = 250 * time.Millisecond

// writeBotLogSSE writes one bot log entry as an SSE data
// frame. Format matches writeLogSSE so the browser-side JS
// snippet in bot_detail.html can reuse the JSON.parse / append
// pattern from logs.html without a second dialect.
func writeBotLogSSE(w http.ResponseWriter, e BotLogEntry) error {
	msg := jsonEscape(e.MessageText())
	ts := e.Timestamp().UTC().Format("15:04:05.000")
	payload := `{"seq":` + u64toa(e.Sequence()) +
		`,"time":"` + ts +
		`","level":"` + lowerLevel(e.LevelName()) +
		`","message":"` + msg + `"}`
	_, err := w.Write([]byte("data: " + payload + "\n\n"))
	return err
}

// htmlEscape is the tiny escaper the validate fragment uses.
// Enough for a compiler diagnostic; for anything more we would
// route through html/template.
func htmlEscape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '\'':
			b.WriteString("&#39;")
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// handleBotTogglePost flips a bot's Enabled flag and pushes the
// new state through UpdateBot, which starts or stops the
// runtime accordingly.
func (s *Server) handleBotTogglePost(sess *session, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pages == nil || s.pages.Bots == nil || s.pages.Store == nil {
		http.Error(w, "bot manager not configured", http.StatusServiceUnavailable)
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
	bot, err := s.pages.Store.Bots().Get(r.Context(), id)
	if err != nil {
		http.Error(w, "no such bot", http.StatusNotFound)
		return
	}
	bot.Enabled = !bot.Enabled
	if err := s.pages.Bots.UpdateBot(r.Context(), bot); err != nil {
		http.Error(w, "toggle failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard/bots", http.StatusSeeOther)
}

// handleBotDelete removes a bot via the supervisor's DeleteBot
// which stops the runtime and drops the persisted record.
func (s *Server) handleBotDelete(sess *session, w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.pages == nil || s.pages.Bots == nil {
		http.Error(w, "bot manager not configured", http.StatusServiceUnavailable)
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
	if err := s.pages.Bots.DeleteBot(r.Context(), id); err != nil {
		http.Error(w, "delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard/bots", http.StatusSeeOther)
}

// handleOperatorCreate handles POST /dashboard/operators. The
// password is hashed via internal/auth using the configured
// hash algorithm (argon2id by default) before being persisted,
// so the plaintext never lives in the database. Flags are
// taken as a comma-separated list because the dashboard form
// is a single text input — operators with no special flags
// can leave it empty.
func (s *Server) handleOperatorCreate(sess *session, w http.ResponseWriter, r *http.Request) {
	if s.pages == nil || s.pages.Store == nil {
		http.Error(w, "operators not configured", http.StatusServiceUnavailable)
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
	name := strings.TrimSpace(r.PostForm.Get("name"))
	password := r.PostForm.Get("password")
	if name == "" || password == "" {
		s.renderOperatorsWithFlash(sess, w, r, "name and password are required")
		return
	}
	hash, err := auth.Hash(auth.AlgorithmArgon2id, password, auth.Argon2idParams{})
	if err != nil {
		s.renderOperatorsWithFlash(sess, w, r, "hash failed: "+err.Error())
		return
	}
	op := &storage.Operator{
		Name:         name,
		HostMask:     strings.TrimSpace(r.PostForm.Get("host_mask")),
		PasswordHash: hash,
		Flags:        splitCSV(r.PostForm.Get("flags")),
	}
	if err := s.pages.Store.Operators().Create(r.Context(), op); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			s.renderOperatorsWithFlash(sess, w, r, "operator already exists")
			return
		}
		s.renderOperatorsWithFlash(sess, w, r, "create failed: "+err.Error())
		return
	}
	http.Redirect(w, r, "/dashboard/operators", http.StatusSeeOther)
}

// handleOperatorDelete removes an operator entry. The bootstrap
// initial admin can be deleted via this path too — if you do
// that and have no other admins, you have locked yourself out
// of the dashboard, so the form button has the danger class on
// the operators page.
func (s *Server) handleOperatorDelete(sess *session, w http.ResponseWriter, r *http.Request) {
	if s.pages == nil || s.pages.Store == nil {
		http.Error(w, "operators not configured", http.StatusServiceUnavailable)
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
	name := r.PathValue("name")
	if err := s.pages.Store.Operators().Delete(r.Context(), name); err != nil {
		s.logger.Warn("dashboard operator delete failed", "name", name, "error", err)
		http.Error(w, "delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard/operators", http.StatusSeeOther)
}

// renderOperatorsWithFlash re-renders the operators page with
// an error or status message in the Flash field. Used by the
// create handler so a validation failure does not redirect
// away from the form the operator was filling in.
func (s *Server) renderOperatorsWithFlash(sess *session, w http.ResponseWriter, r *http.Request, flash string) {
	data := s.newPageData(sess, "operators", "operators")
	data.Flash = flash
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

// handleTokensPage renders /dashboard/tokens with the list of
// minted API tokens (id, label, last used). The mint form is
// part of the same template; the plaintext is shown only on
// the response to a successful mint.
func (s *Server) handleTokensPage(sess *session, w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(sess, "tokens", "API tokens")
	s.fillTokens(r.Context(), data)
	s.renderPage(w, "tokens", data)
}

func (s *Server) fillTokens(ctx context.Context, data *pageData) {
	if s.pages == nil || s.pages.Store == nil {
		return
	}
	tokens, err := s.pages.Store.APITokens().List(ctx)
	if err != nil {
		s.logger.Warn("dashboard list tokens failed", "error", err)
		return
	}
	for _, t := range tokens {
		row := tokenPayload{
			ID:        t.ID,
			Label:     t.Label,
			CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339),
		}
		if !t.LastUsedAt.IsZero() {
			row.LastUsedAt = t.LastUsedAt.UTC().Format(time.RFC3339)
		}
		data.Tokens = append(data.Tokens, row)
	}
	sort.Slice(data.Tokens, func(i, j int) bool { return data.Tokens[i].ID < data.Tokens[j].ID })
}

// handleTokenCreate mints a new API token, persists the hash,
// and re-renders the tokens page with the plaintext in the
// Flash field. The plaintext is shown exactly once — refreshing
// the page or coming back later only shows the metadata.
func (s *Server) handleTokenCreate(sess *session, w http.ResponseWriter, r *http.Request) {
	if s.pages == nil || s.pages.Store == nil {
		http.Error(w, "tokens not configured", http.StatusServiceUnavailable)
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
	label := strings.TrimSpace(r.PostForm.Get("label"))
	if label == "" {
		http.Error(w, "label required", http.StatusBadRequest)
		return
	}
	minted, err := auth.GenerateAPIToken()
	if err != nil {
		s.logger.Warn("token mint failed", "error", err)
		http.Error(w, "mint failed", http.StatusInternalServerError)
		return
	}
	rec := &storage.APIToken{
		ID:    minted.ID,
		Label: label,
		Hash:  minted.Hash,
	}
	if err := s.pages.Store.APITokens().Create(r.Context(), rec); err != nil {
		s.logger.Warn("token persist failed", "error", err)
		http.Error(w, "persist failed", http.StatusInternalServerError)
		return
	}
	data := s.newPageData(sess, "tokens", "API tokens")
	data.Flash = "minted: " + minted.Plaintext + " — copy this now, it cannot be retrieved later"
	s.fillTokens(r.Context(), data)
	s.renderPage(w, "tokens", data)
}

func (s *Server) handleTokenDelete(sess *session, w http.ResponseWriter, r *http.Request) {
	if s.pages == nil || s.pages.Store == nil {
		http.Error(w, "tokens not configured", http.StatusServiceUnavailable)
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
	id := r.PathValue("id")
	if err := s.pages.Store.APITokens().Delete(r.Context(), id); err != nil {
		s.logger.Warn("dashboard token delete failed", "id", id, "error", err)
		http.Error(w, "delete failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard/tokens", http.StatusSeeOther)
}

// splitCSV splits a comma-separated input field into a trimmed
// slice. Empty input returns nil so the resulting operator
// flags slice does not contain a stray empty entry.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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

// handleChannelDetailPage renders the per-channel page
// reachable from the channels list. Shows topic, modes, the
// member list with op/voice prefixes and a local/remote pill,
// and an inline topic-edit form. The full ban list is
// surfaced too if any are set.
func (s *Server) handleChannelDetailPage(sess *session, w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	// Channel names start with #, &, +, or ! and the path uses
	// URL encoding for # — accept the bare form by re-prepending
	// '#' when the path value has no prefix byte the server
	// recognises.
	if name != "" {
		switch name[0] {
		case '#', '&', '+', '!':
		default:
			name = "#" + name
		}
	}
	data := s.newPageData(sess, "channels", "channel — "+name)
	if s.pages == nil || s.pages.World == nil {
		http.Error(w, "world not configured", http.StatusServiceUnavailable)
		return
	}
	ch := s.pages.World.FindChannel(name)
	if ch == nil {
		http.Error(w, "no such channel", http.StatusNotFound)
		return
	}
	modes, _ := ch.ModeString()
	topic, setBy, _ := ch.Topic()
	detail := &channelDetailPayload{
		Name:        ch.Name(),
		MemberCount: ch.MemberCount(),
		ModeWord:    modes,
		Topic:       topic,
		TopicSetBy:  setBy,
	}
	for id, mem := range ch.MemberIDs() {
		u := s.pages.World.FindByID(id)
		if u == nil {
			continue
		}
		detail.Members = append(detail.Members, channelMemberPayload{
			Nick:   u.Nick,
			Prefix: mem.Prefix(),
			Remote: u.IsRemote(),
		})
	}
	sort.Slice(detail.Members, func(i, j int) bool { return detail.Members[i].Nick < detail.Members[j].Nick })

	// Surface every list-form mode the channel knows about so an
	// operator can audit them all in one place. Sort each list so
	// the rendered output is deterministic across reloads.
	detail.Bans = sortedKeys(ch.Bans())
	detail.Exceptions = sortedKeys(ch.Exceptions())
	detail.Invexes = sortedKeys(ch.Invexes())
	detail.Quiets = sortedKeys(ch.Quiets())

	data.ChannelDetail = detail
	s.renderPage(w, "channel_detail", data)
}

// handleChannelTopicPost is the form post that backs the
// topic-edit form on the channel detail page. Calls into the
// PageActuator with a "dashboard:<operator>" set-by prefix so
// audit + federation broadcasts attribute the change to the
// dashboard rather than to a regular IRC client.
func (s *Server) handleChannelTopicPost(sess *session, w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name != "" && name[0] != '#' && name[0] != '&' {
		name = "#" + name
	}
	if s.pages == nil || s.pages.Actuator == nil {
		http.Error(w, "topic edit disabled", http.StatusServiceUnavailable)
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
	topic := r.PostForm.Get("topic")
	setBy := "dashboard:" + sess.Operator
	if err := s.pages.Actuator.SetChannelTopic(r.Context(), name, topic, setBy); err != nil {
		s.logger.Warn("dashboard set topic failed", "channel", name, "error", err)
		http.Error(w, "set topic failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard/channels/"+url.PathEscape(name), http.StatusSeeOther)
}

func (s *Server) handleFederationPage(sess *session, w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(sess, "federation", "federation")
	if s.pages != nil && s.pages.Federation != nil {
		for _, row := range s.pages.Federation.FederationSnapshot() {
			data.Federation = append(data.Federation, fedLinkPayload{
				Peer:         row.Peer(),
				State:        row.State(),
				Description:  row.Description(),
				Subscribed:   row.Subscribed(),
				SentMessages: row.SentMessages(),
				SentBytes:    row.SentBytes(),
				RecvMessages: row.RecvMessages(),
				RecvBytes:    row.RecvBytes(),
			})
		}
		sort.Slice(data.Federation, func(i, j int) bool {
			return data.Federation[i].Peer < data.Federation[j].Peer
		})
	}
	s.renderPage(w, "federation", data)
}

// handleLogsPage renders the static /dashboard/logs page. The
// real work happens in the SSE handler below — the page is
// just a script that opens an EventSource and appends each
// incoming record to the #logstream div.
func (s *Server) handleLogsPage(sess *session, w http.ResponseWriter, r *http.Request) {
	data := s.newPageData(sess, "logs", "live logs")
	s.renderPage(w, "logs", data)
}

// handleLogsSSE streams new log records out as Server-Sent
// Events. The handler polls the configured ring buffer every
// logsPollInterval and emits any entries newer than the last
// sequence it sent to this client. The polling rate is fast
// enough to feel live but slow enough not to burn CPU; a
// future commit can switch to a fan-out subscriber on the
// ring buffer if 250 ms ever turns out to be too slow.
func (s *Server) handleLogsSSE(sess *session, w http.ResponseWriter, r *http.Request) {
	if s.pages == nil || s.pages.LogTail == nil {
		http.Error(w, "log tail not configured", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx proxy buffering

	// Send the initial backlog so the operator immediately
	// sees the last few entries instead of an empty box.
	var lastSeq uint64
	for _, e := range s.pages.LogTail.Since(0) {
		if err := writeLogSSE(w, e); err != nil {
			return
		}
		if e.Sequence() > lastSeq {
			lastSeq = e.Sequence()
		}
	}
	flusher.Flush()

	ticker := time.NewTicker(logsPollInterval)
	defer ticker.Stop()
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			entries := s.pages.LogTail.Since(lastSeq)
			if len(entries) == 0 {
				// SSE comment ping so intermediaries do not
				// time the connection out during quiet
				// periods.
				if _, err := w.Write([]byte(": ping\n\n")); err != nil {
					return
				}
				flusher.Flush()
				continue
			}
			for _, e := range entries {
				if err := writeLogSSE(w, e); err != nil {
					return
				}
				if e.Sequence() > lastSeq {
					lastSeq = e.Sequence()
				}
			}
			flusher.Flush()
		}
	}
}

// logsPollInterval is how often the SSE handler asks the ring
// buffer for new entries. Picked to feel live in a browser
// while staying off the CPU.
const logsPollInterval = 250 * time.Millisecond

// writeLogSSE writes one log entry as an SSE data frame. The
// payload is hand-rolled JSON because the four fields are
// short and known — no need to drag encoding/json into the
// hot path.
func writeLogSSE(w http.ResponseWriter, e LogEntry) error {
	msg := jsonEscape(e.MessageText())
	ts := e.Timestamp().UTC().Format("15:04:05.000")
	payload := `{"seq":` + u64toa(e.Sequence()) +
		`,"time":"` + ts +
		`","level":"` + lowerLevel(e.LevelName()) +
		`","message":"` + msg + `"}`
	_, err := w.Write([]byte("data: " + payload + "\n\n"))
	return err
}

// jsonEscape escapes the small set of characters that would
// break a JSON string literal. Good enough for log lines; not
// a general-purpose JSON encoder.
func jsonEscape(s string) string {
	if !strings.ContainsAny(s, "\"\\\n\r\t") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// lowerLevel maps slog level strings to the four-step taxonomy
// the JS client uses for filtering: debug, info, warn, error.
// slog uses upper-case names like "DEBUG"/"INFO"; the JS uses
// lowercase. Anything we do not recognise is mapped to "info"
// so the row still renders.
func lowerLevel(s string) string {
	switch s {
	case "DEBUG", "debug":
		return "debug"
	case "WARN", "warn", "WARNING":
		return "warn"
	case "ERROR", "error", "ERR":
		return "error"
	default:
		return "info"
	}
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
// SVG comes from the rolling per-metric sample buffer that
// startSampleLoop fills in the background.
func (s *Server) buildOverviewCards() []cardPayload {
	if s.metrics == nil {
		return nil
	}
	out := []cardPayload{
		{Title: "users", Value: itoa(s.metrics.UserCount()), Spark: s.renderSparkline("users")},
		{Title: "channels", Value: itoa(s.metrics.ChannelCount()), Spark: s.renderSparkline("channels")},
		{Title: "federation links", Value: itoa(s.metrics.FederationLinkCount()), Spark: s.renderSparkline("federation links")},
		{Title: "bots", Value: itoa(s.metrics.BotCount()), Spark: s.renderSparkline("bots")},
		{Title: "messages in", Value: u64toa(s.metrics.MessagesIn()), Spark: s.renderSparkline("messages in")},
		{Title: "messages out", Value: u64toa(s.metrics.MessagesOut()), Spark: s.renderSparkline("messages out")},
	}
	return out
}

func itoa(n int) string      { return strconvItoa(int64(n)) }
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
