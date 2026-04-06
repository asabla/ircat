package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/auth"
	"github.com/asabla/ircat/internal/state"
	"github.com/asabla/ircat/internal/storage"
	"github.com/asabla/ircat/internal/storage/sqlite"
)

// fakeActuator is a tiny in-test stand-in for internal/server.Server
// so the api tests do not need a real IRC daemon. The kick path is
// observed via a counter; the snapshot methods just delegate to the
// supplied World.
type fakeActuator struct {
	world      *state.World
	kickCalls  int
	lastKick   string
	kickResult error
}

func (f *fakeActuator) KickUser(ctx context.Context, nick, reason string) error {
	f.kickCalls++
	f.lastKick = nick
	if f.kickResult != nil {
		return f.kickResult
	}
	if u := f.world.FindByNick(nick); u != nil {
		f.world.RemoveUser(u.ID)
	}
	return nil
}

func (f *fakeActuator) ListenerAddresses() []string { return []string{"127.0.0.1:6667"} }

func (f *fakeActuator) SnapshotUsers() []state.User { return f.world.Snapshot() }

func (f *fakeActuator) SnapshotChannels() []*state.Channel { return f.world.ChannelsSnapshot() }

func newTestAPIWithActuator(t *testing.T) (*API, string, *fakeActuator) {
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
	tok, err := auth.GenerateAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.APITokens().Create(context.Background(), &storage.APIToken{
		ID:    tok.ID,
		Label: "test",
		Hash:  tok.Hash,
	}); err != nil {
		t.Fatal(err)
	}
	world := state.NewWorld()
	act := &fakeActuator{world: world}
	api := New(Options{
		Store:    store,
		World:    world,
		Actuator: act,
	})
	return api, tok.Plaintext, act
}

func TestAPI_ListUsers(t *testing.T) {
	api, token, act := newTestAPIWithActuator(t)
	// Pre-populate the world with a couple of users.
	_, _ = act.world.AddUser(&state.User{Nick: "alice", User: "alice", Host: "h", Registered: true})
	_, _ = act.world.AddUser(&state.User{Nick: "bob", User: "bob", Host: "h", Registered: true})

	rec := doJSON(t, api.Handler(), "GET", "/users", token, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Users []userRecord `json:"users"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Users) != 2 || resp.Users[0].Nick != "alice" || resp.Users[1].Nick != "bob" {
		t.Errorf("users = %#v", resp.Users)
	}
}

func TestAPI_GetUser_Found(t *testing.T) {
	api, token, act := newTestAPIWithActuator(t)
	_, _ = act.world.AddUser(&state.User{Nick: "alice", User: "alice", Host: "h", Registered: true})

	rec := doJSON(t, api.Handler(), "GET", "/users/alice", token, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var u userRecord
	if err := json.NewDecoder(rec.Body).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if u.Nick != "alice" || u.Hostmask == "" {
		t.Errorf("user = %#v", u)
	}
}

func TestAPI_GetUser_NotFound(t *testing.T) {
	api, token, _ := newTestAPIWithActuator(t)
	rec := doJSON(t, api.Handler(), "GET", "/users/ghost", token, nil)
	if rec.Code != 404 {
		t.Errorf("status %d", rec.Code)
	}
}

func TestAPI_KickUser(t *testing.T) {
	api, token, act := newTestAPIWithActuator(t)
	_, _ = act.world.AddUser(&state.User{Nick: "alice", User: "alice", Host: "h", Registered: true})

	rec := doJSON(t, api.Handler(), "POST", "/users/alice/kick", token, kickRequest{Reason: "test kick"})
	if rec.Code != 204 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if act.kickCalls != 1 || act.lastKick != "alice" {
		t.Errorf("actuator = %d / %q", act.kickCalls, act.lastKick)
	}
}

func TestAPI_KickUser_NotFound(t *testing.T) {
	api, token, act := newTestAPIWithActuator(t)
	act.kickResult = ErrNotFound
	rec := doJSON(t, api.Handler(), "POST", "/users/ghost/kick", token, kickRequest{})
	if rec.Code != 404 {
		t.Errorf("status %d", rec.Code)
	}
}

func TestAPI_ListChannels(t *testing.T) {
	api, token, act := newTestAPIWithActuator(t)
	id, _ := act.world.AddUser(&state.User{Nick: "alice", User: "alice", Host: "h", Registered: true})
	_, _, _, _ = act.world.JoinChannel(id, "#test")
	_, _, _, _ = act.world.JoinChannel(id, "#other")

	rec := doJSON(t, api.Handler(), "GET", "/channels", token, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var resp struct {
		Channels []channelRecord `json:"channels"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Channels) != 2 {
		t.Errorf("channels = %d", len(resp.Channels))
	}
}

func TestAPI_GetChannel(t *testing.T) {
	api, token, act := newTestAPIWithActuator(t)
	id, _ := act.world.AddUser(&state.User{Nick: "alice", User: "alice", Host: "h", Registered: true})
	_, _, _, _ = act.world.JoinChannel(id, "#test")

	rec := doJSON(t, api.Handler(), "GET", "/channels/%23test", token, nil)
	// %23 is the URL-encoded '#'. Servemux pattern uses {name} which
	// should decode it.
	if rec.Code == 404 {
		// Some servemux versions do not auto-decode; fall back to
		// querying without encoding.
		req := httptest.NewRequest("GET", "/channels/#test", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec = httptest.NewRecorder()
		api.Handler().ServeHTTP(rec, req)
	}
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var ch channelRecord
	if err := json.NewDecoder(rec.Body).Decode(&ch); err != nil {
		t.Fatal(err)
	}
	if ch.Name != "#test" {
		t.Errorf("name = %q", ch.Name)
	}
	if ch.MemberCount != 1 || len(ch.Members) != 1 || ch.Members[0].Nick != "alice" {
		t.Errorf("members = %#v", ch.Members)
	}
	if !ch.Members[0].Op {
		t.Errorf("first joiner not opped")
	}
}

func TestAPI_GetChannel_NotFound(t *testing.T) {
	api, token, _ := newTestAPIWithActuator(t)
	rec := doJSON(t, api.Handler(), "GET", "/channels/%23ghost", token, nil)
	if rec.Code != 404 {
		t.Errorf("status %d", rec.Code)
	}
}

// silence unused warnings while iterating
var _ = errors.New
var _ = time.Second
