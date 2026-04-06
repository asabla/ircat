package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

func TestTokens_CRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk := &storage.APIToken{
		ID:     "01HNTOKEN",
		Label:  "ci",
		Hash:   "deadbeef",
		Scopes: []string{"users:read", "channels:write"},
	}
	if err := s.APITokens().Create(ctx, tk); err != nil {
		t.Fatal(err)
	}
	got, err := s.APITokens().Get(ctx, "01HNTOKEN")
	if err != nil || got.Hash != "deadbeef" || len(got.Scopes) != 2 {
		t.Errorf("get = %#v err = %v", got, err)
	}
	gotByHash, err := s.APITokens().GetByHash(ctx, "deadbeef")
	if err != nil || gotByHash.ID != "01HNTOKEN" {
		t.Errorf("getByHash = %#v err = %v", gotByHash, err)
	}
	now := time.Now().UTC()
	if err := s.APITokens().TouchLastUsed(ctx, "01HNTOKEN", now); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.APITokens().Get(ctx, "01HNTOKEN")
	if got2.LastUsedAt.IsZero() {
		t.Errorf("last_used_at not stamped")
	}
	if err := s.APITokens().Delete(ctx, "01HNTOKEN"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.APITokens().Get(ctx, "01HNTOKEN"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("after delete: err = %v", err)
	}
}

func TestBots_CRUDAndKV(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	bot := &storage.Bot{
		ID:           "01HNBOT",
		Name:         "echo",
		Source:       "function on_message(ctx, e) ctx:say(e.channel, e.text) end",
		Enabled:      true,
		TickInterval: 30 * time.Second,
	}
	if err := s.Bots().Create(ctx, bot); err != nil {
		t.Fatal(err)
	}
	got, err := s.Bots().GetByName(ctx, "echo")
	if err != nil || got.Source == "" || !got.Enabled || got.TickInterval != 30*time.Second {
		t.Errorf("get = %#v err = %v", got, err)
	}

	kv := s.Bots().KV()
	if err := kv.Set(ctx, "01HNBOT", "counter", "42"); err != nil {
		t.Fatal(err)
	}
	if v, err := kv.Get(ctx, "01HNBOT", "counter"); err != nil || v != "42" {
		t.Errorf("kv.Get = %q err = %v", v, err)
	}
	// Upsert path: same key with new value.
	if err := kv.Set(ctx, "01HNBOT", "counter", "43"); err != nil {
		t.Fatal(err)
	}
	v, _ := kv.Get(ctx, "01HNBOT", "counter")
	if v != "43" {
		t.Errorf("upsert: got %q", v)
	}
	all, err := kv.List(ctx, "01HNBOT")
	if err != nil || len(all) != 1 || all["counter"] != "43" {
		t.Errorf("list = %v err = %v", all, err)
	}

	// Deleting the bot cascades to its KV.
	if err := s.Bots().Delete(ctx, "01HNBOT"); err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Get(ctx, "01HNBOT", "counter"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("kv survived bot delete: err = %v", err)
	}
}

func TestChannels_UpsertWithBans(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := &storage.ChannelRecord{
		Name:       "#test",
		Topic:      "hello",
		TopicSetBy: "alice!a@h",
		TopicSetAt: time.Now().UTC(),
		ModeWord:   "+nt",
		Bans: []storage.BanRecord{
			{Mask: "evil!*@*", SetBy: "alice"},
		},
	}
	if err := s.Channels().Upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.Channels().Get(ctx, "#test")
	if err != nil || got.Topic != "hello" || len(got.Bans) != 1 || got.Bans[0].Mask != "evil!*@*" {
		t.Errorf("get = %#v err = %v", got, err)
	}

	// Upsert again with a different ban set: the old bans should be
	// gone, the new ones should be present.
	rec.Bans = []storage.BanRecord{{Mask: "spammer!*@*", SetBy: "alice"}}
	if err := s.Channels().Upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.Channels().Get(ctx, "#test")
	if len(got2.Bans) != 1 || got2.Bans[0].Mask != "spammer!*@*" {
		t.Errorf("after upsert: bans = %v", got2.Bans)
	}

	all, err := s.Channels().List(ctx)
	if err != nil || len(all) != 1 {
		t.Errorf("list len = %d err = %v", len(all), err)
	}

	if err := s.Channels().Delete(ctx, "#test"); err != nil {
		t.Fatal(err)
	}
	// Bans should cascade.
	got3, err := s.Channels().Get(ctx, "#test")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("after delete: %#v %v", got3, err)
	}
}

func TestEvents_AppendAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i, kind := range []string{"oper_up", "kick", "mode"} {
		e := &storage.AuditEvent{
			ID:    "evt-" + string(rune('a'+i)),
			Type:  kind,
			Actor: "alice",
		}
		if err := s.Events().Append(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.Events().List(ctx, storage.ListEventsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("list = %d", len(all))
	}
	// Filter by type.
	kicks, err := s.Events().List(ctx, storage.ListEventsOptions{Type: "kick"})
	if err != nil {
		t.Fatal(err)
	}
	if len(kicks) != 1 || kicks[0].Type != "kick" {
		t.Errorf("filter = %v", kicks)
	}
}
