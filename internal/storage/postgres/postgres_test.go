package postgres

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/storage"
)

// newTestStore opens the Postgres backing store from the
// IRCAT_TEST_POSTGRES_DSN environment variable, runs migrations,
// and returns the store. If the env var is not set, the test is
// skipped — `go test ./...` runs cleanly on machines without a
// running Postgres. CI sets the DSN against a service container.
//
// Each test gets its own logical schema by clearing the data tables
// before running. We do not bring up / tear down a fresh database
// per test because that requires CREATEDB privileges; clearing rows
// is enough for the assertions we make.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("IRCAT_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("IRCAT_TEST_POSTGRES_DSN not set; skipping Postgres integration tests")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Clear data so each test starts fresh.
	for _, table := range []string{"channel_bans", "channels", "bot_kv", "bots", "api_tokens", "operators", "audit_events"} {
		if _, err := s.db.ExecContext(context.Background(), "DELETE FROM "+table); err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}
	return s
}

func TestPostgres_OperatorsCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	op := &storage.Operator{
		Name:         "alice",
		HostMask:     "*@10.0.0.*",
		PasswordHash: "$argon2id$placeholder",
		Flags:        []string{"kill", "rehash"},
	}
	if err := s.Operators().Create(ctx, op); err != nil {
		t.Fatal(err)
	}
	got, err := s.Operators().Get(ctx, "alice")
	if err != nil || got.HostMask != "*@10.0.0.*" || len(got.Flags) != 2 {
		t.Errorf("get = %#v err = %v", got, err)
	}

	op.HostMask = "*@10.0.0.1"
	if err := s.Operators().Update(ctx, op); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.Operators().Get(ctx, "alice")
	if got2.HostMask != "*@10.0.0.1" {
		t.Errorf("update did not persist: %v", got2)
	}

	if err := s.Operators().Create(ctx, &storage.Operator{Name: "alice", PasswordHash: "x"}); !errors.Is(err, storage.ErrConflict) {
		t.Errorf("dup create: err = %v", err)
	}

	if err := s.Operators().Delete(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Operators().Get(ctx, "alice"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("after delete: err = %v", err)
	}
}

func TestPostgres_TokensCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tk := &storage.APIToken{
		ID:     "01HNTOKEN",
		Label:  "ci",
		Hash:   "deadbeef",
		Scopes: []string{"users:read"},
	}
	if err := s.APITokens().Create(ctx, tk); err != nil {
		t.Fatal(err)
	}
	got, err := s.APITokens().GetByHash(ctx, "deadbeef")
	if err != nil || got.ID != "01HNTOKEN" {
		t.Errorf("get = %v err = %v", got, err)
	}
	if err := s.APITokens().TouchLastUsed(ctx, "01HNTOKEN", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	got2, _ := s.APITokens().Get(ctx, "01HNTOKEN")
	if got2.LastUsedAt.IsZero() {
		t.Errorf("last_used_at not stamped")
	}
}

func TestPostgres_ChannelsUpsertWithBans(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := &storage.ChannelRecord{
		Name:     "#x",
		Topic:    "hello",
		ModeWord: "+ntk",
		Key:      "secret",
		Bans: []storage.BanRecord{
			{Mask: "evil!*@*", SetBy: "alice"},
		},
	}
	if err := s.Channels().Upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	got, err := s.Channels().Get(ctx, "#x")
	if err != nil || got.Topic != "hello" || got.Key != "secret" || len(got.Bans) != 1 {
		t.Errorf("get = %#v err = %v", got, err)
	}
}

func TestPostgres_EventsAppendAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i, kind := range []string{"oper_up", "kick"} {
		ev := &storage.AuditEvent{
			ID:    "evt-" + string(rune('a'+i)),
			Type:  kind,
			Actor: "alice",
		}
		if err := s.Events().Append(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.Events().List(ctx, storage.ListEventsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("len = %d", len(all))
	}
}

func TestPostgres_BotsCRUDAndKV(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	bot := &storage.Bot{
		ID:           "01HNBOT",
		Name:         "echo",
		Source:       "function on_message() end",
		Enabled:      true,
		TickInterval: 30 * time.Second,
	}
	if err := s.Bots().Create(ctx, bot); err != nil {
		t.Fatal(err)
	}
	kv := s.Bots().KV()
	if err := kv.Set(ctx, "01HNBOT", "k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, _ := kv.Get(ctx, "01HNBOT", "k"); v != "v" {
		t.Errorf("kv.Get = %q", v)
	}
}
