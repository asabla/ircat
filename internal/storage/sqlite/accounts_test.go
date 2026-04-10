package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/asabla/ircat/internal/storage"
)

func TestAccounts_CRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	accts := s.Accounts()

	acct := &storage.Account{
		ID:           "acct-001",
		Username:     "alice",
		PasswordHash: "$argon2id$placeholder",
		Email:        "alice@example.com",
	}

	// Create
	if err := accts.Create(ctx, acct); err != nil {
		t.Fatal(err)
	}
	if acct.CreatedAt.IsZero() || acct.UpdatedAt.IsZero() {
		t.Errorf("Create did not stamp timestamps: %#v", acct)
	}

	// Get by username
	got, err := accts.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "acct-001" || got.PasswordHash != "$argon2id$placeholder" || got.Email != "alice@example.com" {
		t.Errorf("Get = %#v", got)
	}

	// GetByID
	gotByID, err := accts.GetByID(ctx, "acct-001")
	if err != nil {
		t.Fatal(err)
	}
	if gotByID.Username != "alice" {
		t.Errorf("GetByID = %#v", gotByID)
	}

	// List
	all, err := accts.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Username != "alice" {
		t.Errorf("List = %v", all)
	}

	// Update
	acct.Email = "alice2@example.com"
	acct.Verified = true
	if err := accts.Update(ctx, acct); err != nil {
		t.Fatal(err)
	}
	got2, _ := accts.Get(ctx, "alice")
	if got2.Email != "alice2@example.com" || !got2.Verified {
		t.Errorf("Update did not persist: %#v", got2)
	}

	// Delete
	if err := accts.Delete(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := accts.Get(ctx, "alice"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("after delete: err = %v", err)
	}
}

func TestAccounts_GetMissing(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Accounts().Get(context.Background(), "ghost"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestAccounts_DuplicateUsernameRejected(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	acct := &storage.Account{ID: "a1", Username: "bob", PasswordHash: "h"}
	if err := s.Accounts().Create(ctx, acct); err != nil {
		t.Fatal(err)
	}
	dup := &storage.Account{ID: "a2", Username: "bob", PasswordHash: "h"}
	if err := s.Accounts().Create(ctx, dup); !errors.Is(err, storage.ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

func TestAccounts_List(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i, name := range []string{"carol", "alice", "bob"} {
		_ = s.Accounts().Create(ctx, &storage.Account{
			ID:           "id-" + string(rune('a'+i)),
			Username:     name,
			PasswordHash: "h",
		})
	}
	all, err := s.Accounts().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("len = %d", len(all))
	}
	if all[0].Username != "alice" || all[1].Username != "bob" || all[2].Username != "carol" {
		t.Errorf("not sorted: %v", []string{all[0].Username, all[1].Username, all[2].Username})
	}
}
