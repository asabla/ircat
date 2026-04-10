package sqlite_test

import (
	"context"
	"testing"
	"time"

	"github.com/asabla/ircat/internal/storage"
	"github.com/asabla/ircat/internal/storage/sqlite"
)

func TestRegisteredChannelStore_CreateAndGet(t *testing.T) {
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Create an account first (FK requirement).
	acct := &storage.Account{
		ID:       "alice",
		Username: "alice",
	}
	if err := store.Accounts().Create(context.Background(), acct); err != nil {
		t.Fatal("create account:", err)
	}

	// Create a registered channel.
	rc := &storage.RegisteredChannel{
		Channel:   "#test",
		FounderID: "alice",
		Guard:     true,
	}
	if err := store.RegisteredChannels().Create(context.Background(), rc); err != nil {
		t.Fatal("create registered channel:", err)
	}
	t.Logf("Created: channel=%s founder=%s guard=%v created=%v", rc.Channel, rc.FounderID, rc.Guard, rc.CreatedAt)

	// Get it back.
	got, err := store.RegisteredChannels().Get(context.Background(), "#test")
	if err != nil {
		t.Fatal("get registered channel:", err)
	}
	t.Logf("Got: channel=%s founder=%s guard=%v created=%v", got.Channel, got.FounderID, got.Guard, got.CreatedAt)

	if got.Channel != "#test" {
		t.Errorf("expected #test, got %s", got.Channel)
	}
	if got.FounderID != "alice" {
		t.Errorf("expected alice, got %s", got.FounderID)
	}
	_ = time.Now() // suppress unused import
}
