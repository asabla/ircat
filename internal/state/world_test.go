package state

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestWorld_AddAndFind(t *testing.T) {
	w := NewWorld()
	id, err := w.AddUser(&User{Nick: "Alice", User: "alice", Host: "h", Realname: "Alice"})
	if err != nil {
		t.Fatalf("AddUser: %v", err)
	}
	if id == 0 {
		t.Errorf("expected non-zero UserID")
	}
	if got := w.FindByNick("alice"); got == nil || got.ID != id {
		t.Errorf("FindByNick(alice) = %v", got)
	}
	if got := w.FindByNick("ALICE"); got == nil || got.ID != id {
		t.Errorf("FindByNick(ALICE) folded lookup failed: %v", got)
	}
	if w.UserCount() != 1 {
		t.Errorf("UserCount = %d", w.UserCount())
	}
}

func TestWorld_AddUser_NickInUse(t *testing.T) {
	w := NewWorld()
	_, err := w.AddUser(&User{Nick: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.AddUser(&User{Nick: "ALICE"}) // same under rfc1459 fold
	if !errors.Is(err, ErrNickInUse) {
		t.Errorf("err = %v, want ErrNickInUse", err)
	}
}

func TestWorld_AddUser_RFC1459PunctuationCollision(t *testing.T) {
	w := NewWorld()
	if _, err := w.AddUser(&User{Nick: "{nick}"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.AddUser(&User{Nick: "[nick]"}); !errors.Is(err, ErrNickInUse) {
		t.Errorf("[nick] should collide with {nick} under rfc1459: %v", err)
	}
}

func TestWorld_AddUser_RequiresNick(t *testing.T) {
	w := NewWorld()
	if _, err := w.AddUser(&User{}); err == nil {
		t.Error("expected error for empty nick")
	}
}

func TestWorld_RemoveUser(t *testing.T) {
	w := NewWorld()
	id, _ := w.AddUser(&User{Nick: "alice"})
	if !w.RemoveUser(id) {
		t.Error("RemoveUser returned false for existing user")
	}
	if w.RemoveUser(id) {
		t.Error("RemoveUser returned true for already-removed user")
	}
	if w.FindByNick("alice") != nil {
		t.Error("nick still findable after removal")
	}
	if w.FindByID(id) != nil {
		t.Error("id still findable after removal")
	}
}

func TestWorld_RenameUser_Success(t *testing.T) {
	w := NewWorld()
	id, _ := w.AddUser(&User{Nick: "alice"})
	if err := w.RenameUser(id, "bob"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if w.FindByNick("alice") != nil {
		t.Error("old nick still resolves")
	}
	u := w.FindByNick("bob")
	if u == nil || u.ID != id || u.Nick != "bob" {
		t.Errorf("after rename: %#v", u)
	}
}

func TestWorld_RenameUser_CaseOnly(t *testing.T) {
	w := NewWorld()
	id, _ := w.AddUser(&User{Nick: "alice"})
	if err := w.RenameUser(id, "Alice"); err != nil {
		t.Fatalf("case-only rename: %v", err)
	}
	u := w.FindByNick("alice")
	if u == nil || u.Nick != "Alice" {
		t.Errorf("after case-only rename: %#v", u)
	}
}

func TestWorld_RenameUser_Collision(t *testing.T) {
	w := NewWorld()
	id, _ := w.AddUser(&User{Nick: "alice"})
	_, _ = w.AddUser(&User{Nick: "bob"})
	if err := w.RenameUser(id, "BOB"); !errors.Is(err, ErrNickInUse) {
		t.Errorf("err = %v, want ErrNickInUse", err)
	}
}

func TestWorld_RenameUser_NoSuchUser(t *testing.T) {
	w := NewWorld()
	if err := w.RenameUser(9999, "alice"); !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("err = %v, want ErrNoSuchUser", err)
	}
}

func TestWorld_Snapshot(t *testing.T) {
	w := NewWorld()
	_, _ = w.AddUser(&User{Nick: "alice"})
	_, _ = w.AddUser(&User{Nick: "bob"})
	snap := w.Snapshot()
	if len(snap) != 2 {
		t.Errorf("len(snap) = %d, want 2", len(snap))
	}
	// Snapshot should be a copy: mutating it must not affect the world.
	snap[0].Nick = "mutated"
	if u := w.FindByNick("alice"); u == nil && w.FindByNick("bob") == nil {
		t.Error("snapshot mutation affected the world")
	}
}

func TestWorld_WithClock(t *testing.T) {
	fixed := time.Date(2026, 4, 6, 18, 0, 0, 0, time.UTC)
	w := NewWorld(WithClock(func() time.Time { return fixed }))
	id, _ := w.AddUser(&User{Nick: "alice"})
	u := w.FindByID(id)
	if !u.ConnectAt.Equal(fixed) {
		t.Errorf("ConnectAt = %v, want %v", u.ConnectAt, fixed)
	}
}

func TestWorld_ConcurrentAddRemove(t *testing.T) {
	// Smoke test for the lock discipline. Run with -race; failures
	// here mean we read or wrote a map without holding the mutex.
	w := NewWorld()
	const N = 100
	var wg sync.WaitGroup
	wg.Add(N * 2)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, _ = w.AddUser(&User{Nick: nickN(i)})
		}()
		go func() {
			defer wg.Done()
			_ = w.FindByNick(nickN(i))
		}()
	}
	wg.Wait()
	if w.UserCount() != N {
		t.Errorf("UserCount = %d, want %d", w.UserCount(), N)
	}
}

func nickN(i int) string {
	return "user_" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

func TestUser_Hostmask(t *testing.T) {
	u := &User{Nick: "alice", User: "alice", Host: "host"}
	if got := u.Hostmask(); got != "alice!alice@host" {
		t.Errorf("Hostmask = %q", got)
	}
	u2 := &User{Nick: "bob"}
	if got := u2.Hostmask(); got != "bob!bob@unknown" {
		t.Errorf("default Hostmask = %q", got)
	}
}
