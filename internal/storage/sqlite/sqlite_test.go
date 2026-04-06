package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/asabla/ircat/internal/storage"
)

// newTestStore opens a fresh sqlite Store backed by a file in
// t.TempDir, runs migrations, and returns the store with a defer
// that closes it. We use file-backed (not :memory:) so the schema
// state is observable from the host if a test fails — the temp dir
// stays around long enough to inspect.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ircat.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func TestMigrate_IsIdempotent(t *testing.T) {
	s := newTestStore(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestOperators_CreateGetUpdateDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ops := s.Operators()

	op := &storage.Operator{
		Name:         "alice",
		HostMask:     "*@10.0.0.*",
		PasswordHash: "$argon2id$placeholder",
		Flags:        []string{"kill", "rehash"},
	}
	if err := ops.Create(ctx, op); err != nil {
		t.Fatal(err)
	}
	if op.CreatedAt.IsZero() || op.UpdatedAt.IsZero() {
		t.Errorf("Create did not stamp timestamps: %#v", op)
	}

	got, err := ops.Get(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.HostMask != op.HostMask || got.PasswordHash != op.PasswordHash {
		t.Errorf("got %#v", got)
	}
	if len(got.Flags) != 2 || got.Flags[0] != "kill" || got.Flags[1] != "rehash" {
		t.Errorf("flags = %v", got.Flags)
	}

	op.HostMask = "*@10.0.0.1"
	if err := ops.Update(ctx, op); err != nil {
		t.Fatal(err)
	}
	got2, _ := ops.Get(ctx, "alice")
	if got2.HostMask != "*@10.0.0.1" {
		t.Errorf("update did not persist: %#v", got2)
	}

	if err := ops.Delete(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.Get(ctx, "alice"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("after delete: err = %v", err)
	}
}

func TestOperators_GetMissing(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Operators().Get(context.Background(), "ghost"); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func TestOperators_DuplicateNameRejected(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	op := &storage.Operator{Name: "bob", PasswordHash: "h"}
	if err := s.Operators().Create(ctx, op); err != nil {
		t.Fatal(err)
	}
	if err := s.Operators().Create(ctx, &storage.Operator{Name: "bob", PasswordHash: "h"}); !errors.Is(err, storage.ErrConflict) {
		t.Errorf("expected ErrConflict, got %v", err)
	}
}

func TestOperators_List(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for _, name := range []string{"carol", "alice", "bob"} {
		_ = s.Operators().Create(ctx, &storage.Operator{Name: name, PasswordHash: "h"})
	}
	all, err := s.Operators().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("len = %d", len(all))
	}
	if all[0].Name != "alice" || all[1].Name != "bob" || all[2].Name != "carol" {
		t.Errorf("not sorted: %v", []string{all[0].Name, all[1].Name, all[2].Name})
	}
}

func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ircat.db")

	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s1.Operators().Create(context.Background(), &storage.Operator{
		Name:         "persisted",
		PasswordHash: "h",
	}); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := s2.Operators().Get(context.Background(), "persisted")
	if err != nil {
		t.Fatalf("after reopen: %v", err)
	}
	if got.Name != "persisted" {
		t.Errorf("got %#v", got)
	}
}
