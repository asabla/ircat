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

func (fakeServerInfo) ServerName() string         { return "irc.test" }
func (fakeServerInfo) NetworkName() string        { return "TestNet" }
func (fakeServerInfo) Version() string            { return "ircat-test" }
func (fakeServerInfo) StartedAt() time.Time       { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
func (fakeServerInfo) ListenerAddresses() []string { return []string{"127.0.0.1:6667"} }

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
	if !strings.Contains(rec.Body.String(), "irc.test") {
		t.Errorf("server name missing from overview")
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

func TestLogout_ClearsCookieAndRedirects(t *testing.T) {
	srv, _, _ := newPageServer(t)
	cookie := loginCookie(t, srv, "admin", "secret")
	req := httptest.NewRequest("POST", "/dashboard/logout", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Header().Get("Location") != "/login" {
		t.Errorf("location = %q", rec.Header().Get("Location"))
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
