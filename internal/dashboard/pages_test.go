package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/config"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage"
	"github.com/asabla/ircat/internal/storage/sqlite"
)

// fakeServerInfo satisfies dashboard.PageServerInfo with no real
// dependencies.
type fakeServerInfo struct{}

func (fakeServerInfo) ServerName() string          { return "irc.test" }
func (fakeServerInfo) NetworkName() string         { return "TestNet" }
func (fakeServerInfo) Version() string             { return "ircat-test" }
func (fakeServerInfo) StartedAt() time.Time        { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
func (fakeServerInfo) ListenerAddresses() []string { return []string{"127.0.0.1:6667"} }

// fakeMetricsForPages satisfies MetricsSource for the page-level
// tests so the overview cards have something to render.
type fakeMetricsForPages struct {
	users, channels, fed, bots int
	in, out                    uint64
}

func (f *fakeMetricsForPages) UserCount() int           { return f.users }
func (f *fakeMetricsForPages) ChannelCount() int        { return f.channels }
func (f *fakeMetricsForPages) FederationLinkCount() int { return f.fed }
func (f *fakeMetricsForPages) BotCount() int            { return f.bots }
func (f *fakeMetricsForPages) MessagesIn() uint64       { return f.in }
func (f *fakeMetricsForPages) MessagesOut() uint64      { return f.out }
func (f *fakeMetricsForPages) StartedAt() time.Time {
	return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
}

func newPageServer(t *testing.T) (*Server, *sqlite.Store, *state.World) {
	t.Helper()
	dir := t.TempDir()
	store, err := sqlite.Open(filepath.Join(dir, "ircat.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.Hash(auth.AlgorithmArgon2id, "secret", auth.Argon2idParams{})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Operators().Create(context.Background(), &storage.Operator{
		Name: "admin", PasswordHash: hash,
	}); err != nil {
		t.Fatal(err)
	}
	world := state.NewWorld()
	cfg := &config.Config{
		Dashboard: config.DashboardConfig{
			Enabled: true,
			Address: "127.0.0.1:0",
		},
	}
	srv := New(Options{
		Config: cfg,
		PageDeps: &PageDeps{
			Store:      store,
			World:      world,
			ServerInfo: fakeServerInfo{},
		},
		Metrics: &fakeMetricsForPages{users: 7, channels: 3, fed: 1, bots: 2, in: 100, out: 50},
	})
	return srv, store, world
}

func TestLogin_Get_RendersForm(t *testing.T) {
	srv, _, _ := newPageServer(t)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/login", nil))
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<form") {
		t.Errorf("login form missing")
	}
}

func TestLogin_BadCredentials_RendersError(t *testing.T) {
	srv, _, _ := newPageServer(t)
	form := url.Values{}
	form.Set("username", "admin")
	form.Set("password", "wrong")
	req := httptest.NewRequest("POST", "/dashboard/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid credentials") {
		t.Errorf("error not rendered: %s", rec.Body.String())
	}
}

func TestLogin_Success_SetsCookieAndRedirects(t *testing.T) {
	srv, _, _ := newPageServer(t)
	form := url.Values{}
	form.Set("username", "admin")
	form.Set("password", "secret")
	req := httptest.NewRequest("POST", "/dashboard/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Header().Get("Location") != "/dashboard" {
		t.Errorf("location = %q", rec.Header().Get("Location"))
	}
	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "ircat_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("session cookie not set")
	}
}

func TestProtectedPage_RedirectsWhenAnonymous(t *testing.T) {
	srv, _, _ := newPageServer(t)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/dashboard", nil))
	if rec.Code != http.StatusSeeOther {
		t.Errorf("status %d", rec.Code)
	}
	if rec.Header().Get("Location") != "/login" {
		t.Errorf("location = %q", rec.Header().Get("Location"))
	}
}

func TestOverviewPage_RendersWithSession(t *testing.T) {
	srv, _, _ := newPageServer(t)
	cookie := loginCookie(t, srv, "admin", "secret")
	req := httptest.NewRequest("GET", "/dashboard", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "irc.test") {
		t.Errorf("server name missing from overview")
	}
	// The card grid is the M13 headline. Confirm the values
	// from the fake metrics source rendered.
	for _, want := range []string{
		`<h3>users</h3>`, `<div class="value">7</div>`,
		`<h3>channels</h3>`, `<div class="value">3</div>`,
		`<h3>federation links</h3>`,
		`<h3>bots</h3>`, `<div class="value">2</div>`,
		`hx-get="/dashboard/overview/cards"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("overview missing %q", want)
		}
	}
}

// TestOverviewCards_PartialReturnsBlockOnly hits the htmx
// fragment endpoint and asserts the response is the cards
// block, not the full page chrome.
func TestOverviewCards_PartialReturnsBlockOnly(t *testing.T) {
	srv, _, _ := newPageServer(t)
	cookie := loginCookie(t, srv, "admin", "secret")
	req := httptest.NewRequest("GET", "/dashboard/overview/cards", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "<html") || strings.Contains(body, "<aside") {
		t.Errorf("partial leaked the chrome: %s", body[:200])
	}
	if !strings.Contains(body, `class="cards"`) {
		t.Errorf("partial missing cards container")
	}
	if !strings.Contains(body, `<div class="value">7</div>`) {
		t.Errorf("partial missing users value")
	}
}

func TestUsersPage_RendersConnectedUser(t *testing.T) {
	srv, _, world := newPageServer(t)
	_, _ = world.AddUser(&state.User{Nick: "alice", User: "alice", Host: "h", Registered: true})
	cookie := loginCookie(t, srv, "admin", "secret")
	req := httptest.NewRequest("GET", "/dashboard/users", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "alice") {
		t.Errorf("alice missing")
	}
}

func TestUserDetailPage_RendersConnectedUser(t *testing.T) {
	srv, _, world := newPageServer(t)
	_, _ = world.AddUser(&state.User{Nick: "alice", User: "alice", Host: "h", Modes: "iow", Registered: true})
	cookie := loginCookie(t, srv, "admin", "secret")
	req := httptest.NewRequest("GET", "/dashboard/users/alice", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"alice", "alice!alice@h", "+iow", "actions"} {
		if !strings.Contains(body, want) {
			t.Errorf("user detail missing %q", want)
		}
	}
}

func TestUserDetailPage_404OnUnknownNick(t *testing.T) {
	srv, _, _ := newPageServer(t)
	cookie := loginCookie(t, srv, "admin", "secret")
	req := httptest.NewRequest("GET", "/dashboard/users/ghost", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}

func TestOperatorsPage_RendersOperator(t *testing.T) {
	srv, _, _ := newPageServer(t)
	cookie := loginCookie(t, srv, "admin", "secret")
	req := httptest.NewRequest("GET", "/dashboard/operators", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "admin") {
		t.Errorf("admin operator missing")
	}
}

// fakeLogEntry / fakeLogRing back the log SSE test without
// dragging the real internal/logging RingBuffer in.
type fakeLogEntry struct {
	seq   uint64
	when  time.Time
	level string
	msg   string
}

func (f fakeLogEntry) Sequence() uint64     { return f.seq }
func (f fakeLogEntry) Timestamp() time.Time { return f.when }
func (f fakeLogEntry) LevelName() string    { return f.level }
func (f fakeLogEntry) MessageText() string  { return f.msg }

type fakeLogRing struct{ entries []LogEntry }

func (f *fakeLogRing) Since(seq uint64) []LogEntry {
	out := make([]LogEntry, 0, len(f.entries))
	for _, e := range f.entries {
		if e.Sequence() > seq {
			out = append(out, e)
		}
	}
	return out
}

// flushingRecorder wraps httptest.ResponseRecorder so the SSE
// handler's http.Flusher type-assertion succeeds. Flush is a
// no-op since the recorder accumulates everything.
type flushingRecorder struct{ *httptest.ResponseRecorder }

func (f *flushingRecorder) Flush() {}

func TestLogsSSE_StreamsInitialBacklog(t *testing.T) {
	srv, _, _ := newPageServer(t)
	srv.pages.LogTail = &fakeLogRing{entries: []LogEntry{
		fakeLogEntry{seq: 1, when: time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC), level: "INFO", msg: "ready"},
		fakeLogEntry{seq: 2, when: time.Date(2026, 4, 8, 12, 0, 1, 0, time.UTC), level: "WARN", msg: "ouch"},
	}}
	cookie := loginCookie(t, srv, "admin", "secret")
	req := httptest.NewRequest("GET", "/dashboard/logs/sse", nil)
	req.AddCookie(cookie)
	rec := &flushingRecorder{ResponseRecorder: httptest.NewRecorder()}
	// Use a tiny timeout so the polling loop exits quickly.
	ctx, cancel := context.WithTimeout(req.Context(), 50*time.Millisecond)
	defer cancel()
	srv.mux.ServeHTTP(rec, req.WithContext(ctx))
	body := rec.Body.String()
	if !strings.Contains(body, `"seq":1`) || !strings.Contains(body, `"message":"ready"`) {
		t.Errorf("backlog seq 1 missing: %q", body)
	}
	if !strings.Contains(body, `"seq":2`) || !strings.Contains(body, `"level":"warn"`) {
		t.Errorf("backlog seq 2 missing: %q", body)
	}
	if !strings.Contains(body, "data: {") {
		t.Errorf("missing SSE data prefix: %q", body)
	}
}

// fakeBotManager records every supervisor call so the bot
// page tests can stay isolated from the real internal/bots
// supervisor (which would compile the Lua source on every
// CreateBot and pull in the runtime).
type fakeBotManager struct {
	created []*storage.Bot
	updated []*storage.Bot
	deleted []string
	err     error
}

func (f *fakeBotManager) CreateBot(_ context.Context, b *storage.Bot) error {
	if b.ID == "" {
		b.ID = "bot_test_1"
	}
	f.created = append(f.created, b)
	return f.err
}
func (f *fakeBotManager) UpdateBot(_ context.Context, b *storage.Bot) error {
	f.updated = append(f.updated, b)
	return f.err
}
func (f *fakeBotManager) DeleteBot(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return f.err
}

func TestBotsCreate_PersistsAndRedirects(t *testing.T) {
	srv, store, _ := newPageServer(t)
	bm := &fakeBotManager{}
	srv.pages.Bots = bm
	cookie := loginCookie(t, srv, "admin", "secret")

	gReq := httptest.NewRequest("GET", "/dashboard/bots", nil)
	gReq.AddCookie(cookie)
	gRec := httptest.NewRecorder()
	srv.mux.ServeHTTP(gRec, gReq)
	csrf := extractCSRF(t, gRec.Body.String())

	form := url.Values{}
	form.Set("csrf", csrf)
	form.Set("name", "echobot")
	form.Set("source", `function on_message(ctx, ev) end`)
	form.Set("enabled", "1")
	pReq := httptest.NewRequest("POST", "/dashboard/bots", strings.NewReader(form.Encode()))
	pReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	pReq.AddCookie(cookie)
	pRec := httptest.NewRecorder()
	srv.mux.ServeHTTP(pRec, pReq)
	if pRec.Code != http.StatusSeeOther {
		t.Fatalf("status %d body %s", pRec.Code, pRec.Body.String())
	}
	if len(bm.created) != 1 {
		t.Fatalf("create count = %d, want 1", len(bm.created))
	}
	if bm.created[0].Name != "echobot" || !bm.created[0].Enabled {
		t.Errorf("wrong bot persisted: %+v", bm.created[0])
	}
	// Sanity: the fake never touches the real store path.
	bots, _ := store.Bots().List(context.Background())
	if len(bots) != 0 {
		t.Errorf("real store should be empty, got %d entries", len(bots))
	}
}

func TestOperatorCreate_PersistsAndRedirects(t *testing.T) {
	srv, store, _ := newPageServer(t)
	cookie := loginCookie(t, srv, "admin", "secret")

	// Pull a CSRF from the operators page first.
	gReq := httptest.NewRequest("GET", "/dashboard/operators", nil)
	gReq.AddCookie(cookie)
	gRec := httptest.NewRecorder()
	srv.mux.ServeHTTP(gRec, gReq)
	csrf := extractCSRF(t, gRec.Body.String())

	form := url.Values{}
	form.Set("csrf", csrf)
	form.Set("name", "bob")
	form.Set("password", "hunter2")
	form.Set("flags", "kill,kline")
	pReq := httptest.NewRequest("POST", "/dashboard/operators", strings.NewReader(form.Encode()))
	pReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	pReq.AddCookie(cookie)
	pRec := httptest.NewRecorder()
	srv.mux.ServeHTTP(pRec, pReq)
	if pRec.Code != http.StatusSeeOther {
		t.Fatalf("status %d body %s", pRec.Code, pRec.Body.String())
	}
	got, err := store.Operators().Get(context.Background(), "bob")
	if err != nil {
		t.Fatalf("operator not persisted: %v", err)
	}
	if len(got.Flags) != 2 {
		t.Errorf("flags = %v, want [kill kline]", got.Flags)
	}
	if got.PasswordHash == "" || got.PasswordHash == "hunter2" {
		t.Errorf("password not hashed: %q", got.PasswordHash)
	}
}

func TestTokenMint_ShowsPlaintextOnce(t *testing.T) {
	srv, store, _ := newPageServer(t)
	cookie := loginCookie(t, srv, "admin", "secret")

	gReq := httptest.NewRequest("GET", "/dashboard/tokens", nil)
	gReq.AddCookie(cookie)
	gRec := httptest.NewRecorder()
	srv.mux.ServeHTTP(gRec, gReq)
	csrf := extractCSRF(t, gRec.Body.String())

	form := url.Values{}
	form.Set("csrf", csrf)
	form.Set("label", "ci-runner")
	pReq := httptest.NewRequest("POST", "/dashboard/tokens", strings.NewReader(form.Encode()))
	pReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	pReq.AddCookie(cookie)
	pRec := httptest.NewRecorder()
	srv.mux.ServeHTTP(pRec, pReq)
	if pRec.Code != 200 {
		t.Fatalf("status %d body %s", pRec.Code, pRec.Body.String())
	}
	body := pRec.Body.String()
	if !strings.Contains(body, "minted: ircat_") {
		t.Errorf("plaintext not surfaced in flash: %s", body)
	}
	tokens, _ := store.APITokens().List(context.Background())
	if len(tokens) != 1 {
		t.Fatalf("token persisted count = %d, want 1", len(tokens))
	}
	if tokens[0].Label != "ci-runner" || tokens[0].Hash == "" {
		t.Errorf("persisted token wrong shape: %+v", tokens[0])
	}
}

// extractCSRF pulls the first csrf input value out of an HTML
// body. The shared test helpers use this enough that it earns
// a tiny dedicated function.
func extractCSRF(t *testing.T, body string) string {
	t.Helper()
	idx := strings.Index(body, `name="csrf" value="`)
	if idx < 0 {
		t.Fatal("no csrf input in body")
	}
	rest := body[idx+len(`name="csrf" value="`):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatal("malformed csrf input")
	}
	return rest[:end]
}

func TestChannelDetailPage_RendersChannelWithMembers(t *testing.T) {
	srv, _, world := newPageServer(t)
	alice, _ := world.AddUser(&state.User{Nick: "alice", User: "alice", Host: "h", Registered: true})
	_, _, _, _ = world.JoinChannel(alice, "#general")
	cookie := loginCookie(t, srv, "admin", "secret")
	req := httptest.NewRequest("GET", "/dashboard/channels/%23general", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"#general", "alice", "members", "edit topic"} {
		if !strings.Contains(body, want) {
			t.Errorf("channel detail missing %q", want)
		}
	}
}

func TestChannelDetailPage_404OnUnknownChannel(t *testing.T) {
	srv, _, _ := newPageServer(t)
	cookie := loginCookie(t, srv, "admin", "secret")
	req := httptest.NewRequest("GET", "/dashboard/channels/%23missing", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status %d, want 404", rec.Code)
	}
}

func TestChannelTopicPost_AppliesViaActuator(t *testing.T) {
	act := &fakeKickActuator{}
	srv, _, world := newPageServerWithActuator(t, act)
	alice, _ := world.AddUser(&state.User{Nick: "alice", User: "alice", Host: "h", Registered: true})
	_, _, _, _ = world.JoinChannel(alice, "#general")
	cookie := loginCookie(t, srv, "admin", "secret")

	// Pull a CSRF token from the channel detail page first.
	gReq := httptest.NewRequest("GET", "/dashboard/channels/%23general", nil)
	gReq.AddCookie(cookie)
	gRec := httptest.NewRecorder()
	srv.mux.ServeHTTP(gRec, gReq)
	body := gRec.Body.String()
	idx := strings.Index(body, `name="csrf" value="`)
	if idx < 0 {
		t.Fatal("no csrf in channel detail")
	}
	rest := body[idx+len(`name="csrf" value="`):]
	end := strings.Index(rest, `"`)
	csrf := rest[:end]

	form := url.Values{}
	form.Set("csrf", csrf)
	form.Set("topic", "welcome travelers")
	pReq := httptest.NewRequest("POST", "/dashboard/channels/%23general/topic", strings.NewReader(form.Encode()))
	pReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	pReq.AddCookie(cookie)
	pRec := httptest.NewRecorder()
	srv.mux.ServeHTTP(pRec, pReq)
	if pRec.Code != http.StatusSeeOther {
		t.Fatalf("status %d body %s", pRec.Code, pRec.Body.String())
	}
	if act.topicChan != "#general" || act.topic != "welcome travelers" {
		t.Errorf("actuator chan=%q topic=%q", act.topicChan, act.topic)
	}
	if !strings.HasPrefix(act.topicSet, "dashboard:") {
		t.Errorf("topic set-by should be prefixed dashboard:, got %q", act.topicSet)
	}
}

// fakeFedRow / fakeFederationLister back the federation page
// test without dragging the real federation registry into the
// dashboard test suite.
type fakeFedRow struct {
	peer, state, descr string
	subs               []string
	sentMsgs, recvMsgs uint64
	sentBytes, recvBytes uint64
	opened             time.Time
}

func (r fakeFedRow) Peer() string         { return r.peer }
func (r fakeFedRow) State() string        { return r.state }
func (r fakeFedRow) Description() string  { return r.descr }
func (r fakeFedRow) Subscribed() []string { return r.subs }
func (r fakeFedRow) SentMessages() uint64 { return r.sentMsgs }
func (r fakeFedRow) SentBytes() uint64    { return r.sentBytes }
func (r fakeFedRow) RecvMessages() uint64 { return r.recvMsgs }
func (r fakeFedRow) RecvBytes() uint64    { return r.recvBytes }
func (r fakeFedRow) OpenedAt() time.Time  { return r.opened }

type fakeFederationLister struct{ rows []FederationLinkRow }

func (f fakeFederationLister) FederationSnapshot() []FederationLinkRow { return f.rows }

func TestFederationPage_RendersEmpty(t *testing.T) {
	srv, _, _ := newPageServer(t)
	cookie := loginCookie(t, srv, "admin", "secret")
	req := httptest.NewRequest("GET", "/dashboard/federation", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "no federation links registered") {
		t.Errorf("missing empty state")
	}
}

func TestFederationPage_RendersLinks(t *testing.T) {
	srv, _, _ := newPageServer(t)
	srv.pages.Federation = fakeFederationLister{rows: []FederationLinkRow{
		fakeFedRow{peer: "node-b", state: "active", descr: "test peer", subs: []string{"#fed", "#x"}},
	}}
	cookie := loginCookie(t, srv, "admin", "secret")
	req := httptest.NewRequest("GET", "/dashboard/federation", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"node-b", "test peer", "#fed", "#x", "active"} {
		if !strings.Contains(body, want) {
			t.Errorf("federation page missing %q", want)
		}
	}
}

// fakeKickActuator records every KickUser / SetChannelTopic
// call for the tests. Renamed-in-spirit but kept under the
// existing name to avoid churning every existing test that
// references it.
type fakeKickActuator struct {
	last      string
	reason    string
	err       error
	calls     int
	topic     string
	topicChan string
	topicSet  string
}

func (f *fakeKickActuator) KickUser(ctx context.Context, nick, reason string) error {
	f.calls++
	f.last = nick
	f.reason = reason
	return f.err
}

func (f *fakeKickActuator) SetChannelTopic(ctx context.Context, channel, topic, setBy string) error {
	f.topicChan = channel
	f.topic = topic
	f.topicSet = setBy
	return f.err
}

func newPageServerWithActuator(t *testing.T, act PageActuator) (*Server, *sqlite.Store, *state.World) {
	t.Helper()
	srv, store, world := newPageServer(t)
	srv.pages.Actuator = act
	return srv, store, world
}

func TestKickFromDashboard_RequiresCSRF(t *testing.T) {
	act := &fakeKickActuator{}
	srv, _, _ := newPageServerWithActuator(t, act)
	cookie := loginCookie(t, srv, "admin", "secret")

	// POST without a CSRF token -> 403.
	form := url.Values{}
	req := httptest.NewRequest("POST", "/dashboard/users/alice/kick", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", rec.Code)
	}
	if act.calls != 0 {
		t.Errorf("kick fired without CSRF: %d calls", act.calls)
	}
}

func TestKickFromDashboard_WithCSRFKicks(t *testing.T) {
	act := &fakeKickActuator{}
	srv, _, world := newPageServerWithActuator(t, act)
	// Add a user so the users page renders the kick form (and the
	// CSRF input we need to extract).
	_, _ = world.AddUser(&state.User{Nick: "alice", User: "alice", Host: "h", Registered: true})
	cookie := loginCookie(t, srv, "admin", "secret")

	// Pull the CSRF token from the rendered users page.
	req := httptest.NewRequest("GET", "/dashboard/users", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	body := rec.Body.String()
	idx := strings.Index(body, `name="csrf" value="`)
	if idx < 0 {
		t.Fatalf("csrf input missing: %s", body)
	}
	idx += len(`name="csrf" value="`)
	end := strings.Index(body[idx:], `"`)
	csrf := body[idx : idx+end]

	form := url.Values{}
	form.Set("csrf", csrf)
	form.Set("reason", "test kick")
	req = httptest.NewRequest("POST", "/dashboard/users/alice/kick", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if act.calls != 1 || act.last != "alice" || act.reason != "test kick" {
		t.Errorf("actuator = %+v", act)
	}
}

func TestLogout_ClearsCookieAndRedirects(t *testing.T) {
	srv, _, _ := newPageServer(t)
	cookie := loginCookie(t, srv, "admin", "secret")

	// Pull the per-session CSRF off the overview page first;
	// the logout form post is now CSRF-protected like every
	// other mutating dashboard endpoint.
	gReq := httptest.NewRequest("GET", "/dashboard", nil)
	gReq.AddCookie(cookie)
	gRec := httptest.NewRecorder()
	srv.mux.ServeHTTP(gRec, gReq)
	csrf := extractCSRF(t, gRec.Body.String())

	form := url.Values{}
	form.Set("csrf", csrf)
	req := httptest.NewRequest("POST", "/dashboard/logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Location") != "/login" {
		t.Errorf("location = %q", rec.Header().Get("Location"))
	}
}

// TestLogout_RejectsMissingCSRF asserts that the new CSRF
// guard on the logout endpoint actually rejects a form post
// without the token. Together with the happy-path test above
// this is the regression net for the M13 #119 audit.
func TestLogout_RejectsMissingCSRF(t *testing.T) {
	srv, _, _ := newPageServer(t)
	cookie := loginCookie(t, srv, "admin", "secret")
	req := httptest.NewRequest("POST", "/dashboard/logout", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", rec.Code)
	}
}

// loginCookie does the login dance and returns the resulting cookie.
func loginCookie(t *testing.T, srv *Server, user, password string) *http.Cookie {
	t.Helper()
	form := url.Values{}
	form.Set("username", user)
	form.Set("password", password)
	req := httptest.NewRequest("POST", "/dashboard/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login failed: %d %s", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "ircat_session" {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}
