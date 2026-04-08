package state

import (
	"errors"
	"sync"
	"testing"
)

func TestEnsureChannel_CreatesAndIsIdempotent(t *testing.T) {
	w := NewWorld()
	ch1, created := w.EnsureChannel("#test")
	if !created || ch1 == nil {
		t.Fatalf("first EnsureChannel: ch=%v created=%v", ch1, created)
	}
	if ch1.Name() != "#test" {
		t.Errorf("Name = %q", ch1.Name())
	}

	ch2, created := w.EnsureChannel("#TEST") // collides under rfc1459
	if created {
		t.Errorf("second EnsureChannel claimed creation")
	}
	if ch2 != ch1 {
		t.Errorf("rfc1459-folded lookup did not return the same channel")
	}
	if w.ChannelCount() != 1 {
		t.Errorf("ChannelCount = %d", w.ChannelCount())
	}
}

func TestFindChannel_RespectsCaseMapping(t *testing.T) {
	w := NewWorld()
	w.EnsureChannel("#Foo")
	if got := w.FindChannel("#foo"); got == nil {
		t.Errorf("ascii fold lookup failed")
	}
	if got := w.FindChannel("#FOO"); got == nil {
		t.Errorf("uppercase lookup failed")
	}
	if got := w.FindChannel("#bar"); got != nil {
		t.Errorf("unrelated channel matched: %v", got)
	}
}

func TestJoinChannel_FirstUserBecomesOp(t *testing.T) {
	w := NewWorld()
	id, _ := w.AddUser(&User{Nick: "alice"})
	ch, mem, added, err := w.JoinChannel(id, "#test")
	if err != nil {
		t.Fatal(err)
	}
	if !added {
		t.Errorf("first joiner not marked added")
	}
	if !mem.IsOp() {
		t.Errorf("first joiner not opped: %b", mem)
	}
	if ch.MemberCount() != 1 {
		t.Errorf("MemberCount = %d", ch.MemberCount())
	}
}

func TestJoinChannel_SubsequentUserNotOp(t *testing.T) {
	w := NewWorld()
	a, _ := w.AddUser(&User{Nick: "alice"})
	b, _ := w.AddUser(&User{Nick: "bob"})
	w.JoinChannel(a, "#test")
	_, memB, added, err := w.JoinChannel(b, "#test")
	if err != nil || !added {
		t.Fatalf("bob join: added=%v err=%v", added, err)
	}
	if memB.IsOp() {
		t.Errorf("bob got op flags: %b", memB)
	}
}

func TestJoinChannel_AlreadyMember(t *testing.T) {
	w := NewWorld()
	a, _ := w.AddUser(&User{Nick: "alice"})
	w.JoinChannel(a, "#test")
	_, _, added, _ := w.JoinChannel(a, "#test")
	if added {
		t.Errorf("re-join reported as new add")
	}
}

func TestJoinChannel_NoSuchUser(t *testing.T) {
	w := NewWorld()
	_, _, _, err := w.JoinChannel(UserID(9999), "#test")
	if !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("err = %v", err)
	}
}

func TestPartChannel_DropsEmptyChannel(t *testing.T) {
	w := NewWorld()
	id, _ := w.AddUser(&User{Nick: "alice"})
	w.JoinChannel(id, "#test")
	_, dropped, err := w.PartChannel(id, "#test")
	if err != nil {
		t.Fatal(err)
	}
	if !dropped {
		t.Errorf("channel should have been dropped")
	}
	if w.FindChannel("#test") != nil {
		t.Errorf("channel still findable after drop")
	}
}

func TestPartChannel_KeepsNonEmpty(t *testing.T) {
	w := NewWorld()
	a, _ := w.AddUser(&User{Nick: "alice"})
	b, _ := w.AddUser(&User{Nick: "bob"})
	w.JoinChannel(a, "#test")
	w.JoinChannel(b, "#test")
	_, dropped, err := w.PartChannel(a, "#test")
	if err != nil {
		t.Fatal(err)
	}
	if dropped {
		t.Errorf("non-empty channel was dropped")
	}
	ch := w.FindChannel("#test")
	if ch == nil || ch.MemberCount() != 1 {
		t.Errorf("channel state wrong after part")
	}
}

func TestPartChannel_NoSuchChannel(t *testing.T) {
	w := NewWorld()
	id, _ := w.AddUser(&User{Nick: "alice"})
	_, _, err := w.PartChannel(id, "#nope")
	if !errors.Is(err, ErrNoSuchChannel) {
		t.Errorf("err = %v", err)
	}
}

func TestPartChannel_NotMember(t *testing.T) {
	w := NewWorld()
	a, _ := w.AddUser(&User{Nick: "alice"})
	b, _ := w.AddUser(&User{Nick: "bob"})
	w.JoinChannel(a, "#test")
	_, _, err := w.PartChannel(b, "#test")
	if !errors.Is(err, ErrNoSuchUser) {
		t.Errorf("err = %v", err)
	}
}

func TestUserChannels(t *testing.T) {
	w := NewWorld()
	id, _ := w.AddUser(&User{Nick: "alice"})
	w.JoinChannel(id, "#a")
	w.JoinChannel(id, "#b")
	chans := w.UserChannels(id)
	if len(chans) != 2 {
		t.Errorf("UserChannels len = %d, want 2", len(chans))
	}
}

func TestChannel_TopicRoundTrip(t *testing.T) {
	w := NewWorld()
	ch, _ := w.EnsureChannel("#test")
	now := w.now()
	ch.SetTopic("hello world", "alice!a@h", now)
	text, by, at := ch.Topic()
	if text != "hello world" || by != "alice!a@h" || !at.Equal(now) {
		t.Errorf("topic = (%q, %q, %v)", text, by, at)
	}
}

func TestChannel_ModeStringDefaults(t *testing.T) {
	w := NewWorld()
	ch, _ := w.EnsureChannel("#test")
	modes, params := ch.ModeString()
	if modes != "+nt" {
		t.Errorf("default modes = %q, want +nt", modes)
	}
	if len(params) != 0 {
		t.Errorf("default params = %v, want []", params)
	}
}

func TestChannel_SortedMemberIDs(t *testing.T) {
	w := NewWorld()
	a, _ := w.AddUser(&User{Nick: "alice"})
	b, _ := w.AddUser(&User{Nick: "bob"})
	c, _ := w.AddUser(&User{Nick: "carol"})
	ch, _ := w.EnsureChannel("#x")
	for _, id := range []UserID{c, a, b} {
		ch.addMember(id, false)
	}
	got := ch.SortedMemberIDs()
	if len(got) != 3 {
		t.Fatalf("len = %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i] < got[i-1] {
			t.Errorf("not sorted: %v", got)
		}
	}
}

func TestChannel_AdoptOlderTSAndReset(t *testing.T) {
	w := NewWorld()
	a, _ := w.AddUser(&User{Nick: "alice"})
	b, _ := w.AddUser(&User{Nick: "bob"})
	ch, _ := w.EnsureChannel("#x")
	ch.addMember(a, true) // first member is opped automatically
	ch.addMember(b, false)
	ch.AddMembership(b, MemberVoice)

	if !ch.Membership(a).IsOp() {
		t.Fatal("alice should start as op")
	}
	if !ch.Membership(b).IsVoice() {
		t.Fatal("bob should start with +v")
	}

	// AdoptOlderTS with the current TS or a newer one is a
	// no-op and must NOT trigger a flag reset.
	currentTS := ch.TS()
	if ch.AdoptOlderTS(currentTS) {
		t.Errorf("AdoptOlderTS at current returned true")
	}
	if ch.AdoptOlderTS(currentTS + 100) {
		t.Errorf("AdoptOlderTS at newer returned true")
	}
	if !ch.Membership(a).IsOp() || !ch.Membership(b).IsVoice() {
		t.Errorf("flags should be intact after no-op AdoptOlderTS")
	}

	// Now adopt a strictly older TS. The caller is expected
	// to follow up with ResetMembershipFlags per the doc.
	if !ch.AdoptOlderTS(currentTS - 1_000_000) {
		t.Errorf("AdoptOlderTS at older should return true")
	}
	ch.ResetMembershipFlags()
	if ch.Membership(a) != 0 {
		t.Errorf("alice flags after reset = %v, want 0", ch.Membership(a))
	}
	if ch.Membership(b) != 0 {
		t.Errorf("bob flags after reset = %v, want 0", ch.Membership(b))
	}
	// The members themselves should still be in the channel —
	// only the per-member flags get cleared.
	if !ch.IsMember(a) || !ch.IsMember(b) {
		t.Errorf("members should still be present after flag reset")
	}
}

func TestMembership_Prefix(t *testing.T) {
	if got := MemberOp.Prefix(); got != "@" {
		t.Errorf("op prefix = %q", got)
	}
	if got := MemberVoice.Prefix(); got != "+" {
		t.Errorf("voice prefix = %q", got)
	}
	if got := (MemberOp | MemberVoice).Prefix(); got != "@" {
		t.Errorf("op+voice prefix = %q", got)
	}
	if got := Membership(0).Prefix(); got != "" {
		t.Errorf("zero prefix = %q", got)
	}
}

func TestChannel_ConcurrentJoinPart(t *testing.T) {
	// -race smoke test: hammer the channel from many goroutines.
	w := NewWorld()
	ids := make([]UserID, 50)
	for i := range ids {
		id, _ := w.AddUser(&User{Nick: nickN(i)})
		ids[i] = id
	}
	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, _ = w.JoinChannel(id, "#race")
			_, _, _ = w.PartChannel(id, "#race")
		}()
	}
	wg.Wait()
	// All members joined and parted; channel should be gone.
	if w.FindChannel("#race") != nil {
		t.Errorf("channel survived concurrent join/part")
	}
}
