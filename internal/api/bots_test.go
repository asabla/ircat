package api

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/asabla/ircat/internal/storage"
)

// fakeBotManager records every CRUD call for the tests. It also
// persists the bots into the supplied store so GET/list read paths
// return the expected records.
type fakeBotManager struct {
	mu       sync.Mutex
	store    storage.Store
	creates  int
	updates  int
	deletes  int
	lastBot  *storage.Bot
	createEr error
	updateEr error
	deleteEr error
}

func (f *fakeBotManager) CreateBot(ctx context.Context, bot *storage.Bot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.creates++
	f.lastBot = bot
	if f.createEr != nil {
		return f.createEr
	}
	if bot.ID == "" {
		bot.ID = "fake-" + bot.Name
	}
	return f.store.Bots().Create(ctx, bot)
}
func (f *fakeBotManager) UpdateBot(ctx context.Context, bot *storage.Bot) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates++
	f.lastBot = bot
	if f.updateEr != nil {
		return f.updateEr
	}
	return f.store.Bots().Update(ctx, bot)
}
func (f *fakeBotManager) DeleteBot(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes++
	if f.deleteEr != nil {
		return f.deleteEr
	}
	return f.store.Bots().Delete(ctx, id)
}

func newTestAPIWithBots(t *testing.T) (*API, string, *fakeBotManager) {
	t.Helper()
	api, token, _ := newTestAPIWithActuator(t)
	mgr := &fakeBotManager{store: api.store}
	api.botManager = mgr
	return api, token, mgr
}

func TestAPI_CreateBot(t *testing.T) {
	api, token, mgr := newTestAPIWithBots(t)

	rec := doJSON(t, api.Handler(), "POST", "/bots", token, createBotRequest{
		Name:    "echo",
		Source:  "function on_message() end",
		Enabled: true,
	})
	if rec.Code != 201 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if mgr.creates != 1 {
		t.Errorf("creates = %d", mgr.creates)
	}
	var b botRecord
	if err := json.NewDecoder(rec.Body).Decode(&b); err != nil {
		t.Fatal(err)
	}
	if b.Name != "echo" || !b.Enabled {
		t.Errorf("bot = %+v", b)
	}
}

func TestAPI_ListBots(t *testing.T) {
	api, token, _ := newTestAPIWithBots(t)
	// Seed two bots directly via the store.
	_ = api.store.Bots().Create(context.Background(), &storage.Bot{
		ID: "b1", Name: "alice", Source: "-- 1", Enabled: true,
	})
	_ = api.store.Bots().Create(context.Background(), &storage.Bot{
		ID: "b2", Name: "bob", Source: "-- 2", Enabled: false,
	})

	rec := doJSON(t, api.Handler(), "GET", "/bots", token, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var resp struct {
		Bots []botRecord `json:"bots"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Bots) != 2 {
		t.Fatalf("bots = %d", len(resp.Bots))
	}
}

func TestAPI_GetBot(t *testing.T) {
	api, token, _ := newTestAPIWithBots(t)
	_ = api.store.Bots().Create(context.Background(), &storage.Bot{
		ID: "b1", Name: "echo", Source: "-- bot", Enabled: true,
	})
	rec := doJSON(t, api.Handler(), "GET", "/bots/b1", token, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var b botRecord
	_ = json.NewDecoder(rec.Body).Decode(&b)
	if b.Name != "echo" {
		t.Errorf("bot = %+v", b)
	}
}

func TestAPI_DeleteBot(t *testing.T) {
	api, token, mgr := newTestAPIWithBots(t)
	_ = api.store.Bots().Create(context.Background(), &storage.Bot{
		ID: "b1", Name: "echo", Source: "-- bot",
	})
	rec := doJSON(t, api.Handler(), "DELETE", "/bots/b1", token, nil)
	if rec.Code != 204 {
		t.Errorf("status %d", rec.Code)
	}
	if mgr.deletes != 1 {
		t.Errorf("deletes = %d", mgr.deletes)
	}
}

func TestAPI_UpdateBot(t *testing.T) {
	api, token, mgr := newTestAPIWithBots(t)
	_ = api.store.Bots().Create(context.Background(), &storage.Bot{
		ID: "b1", Name: "echo", Source: "-- old",
	})
	rec := doJSON(t, api.Handler(), "PUT", "/bots/b1", token, updateBotRequest{
		Name:    "echo",
		Source:  "-- new",
		Enabled: true,
	})
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if mgr.updates != 1 {
		t.Errorf("updates = %d", mgr.updates)
	}
}

func TestAPI_CreateBot_BadRequest(t *testing.T) {
	api, token, _ := newTestAPIWithBots(t)
	rec := doJSON(t, api.Handler(), "POST", "/bots", token, createBotRequest{})
	if rec.Code != 400 {
		t.Errorf("status %d", rec.Code)
	}
}
