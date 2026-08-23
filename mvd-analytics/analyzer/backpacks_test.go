package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-reader/events"
)

func newTestBackpackAnalyzer() (*BackpackAnalyzer, *Context) {
	a := NewBackpackAnalyzer()
	ctx := &Context{}
	_ = a.Init(ctx)
	a.timing.Started = true
	return a, ctx
}

// Happy path: KTX emits //ktx drop with IT_ROCKET_LAUNCHER for
// player slot 4 (edict 5). The analyzer records one BackpackDrop
// with weapon="rl", the dropper's name/team, and origin taken from
// the dropper's last PlayerPositionEvent.
func TestBackpackAnalyzer_RLHintEmitsDrop(t *testing.T) {
	a, ctx := newTestBackpackAnalyzer()
	ctx.Players[4] = &events.PlayerInfo{Slot: 4, Name: "ace", Team: "red"}

	_ = a.OnEvent(&events.PlayerPositionEvent{PlayerNum: 4, Origin: [3]float32{200, 0, 0}, TimeMs: 29900})
	_ = a.OnEvent(&events.BackpackDropHintEvent{BackpackEnt: 142, ItemFlags: 32, PlayerEnt: 5, TimeMs: 30000})

	r := &Result{}
	_ = a.Finalize(r)
	out := r.Backpacks
	drops := out
	ok := drops != nil
	if !ok || len(drops) != 1 {
		t.Fatalf("drops = %v, want 1 entry", out)
	}
	d := drops[0]
	if d.Weapon != "rl" {
		t.Errorf("Weapon = %q, want rl", d.Weapon)
	}
	if d.Player != "ace" || d.Team != "red" {
		t.Errorf("dropper = %q/%q, want ace/red", d.Player, d.Team)
	}
	if d.EntNum != 142 || d.Time != 30000 {
		t.Errorf("ent/time = %d/%d", d.EntNum, d.Time)
	}
	// The pack's own origin: the dropper's last broadcast position with
	// KTX's 24-unit drop offset applied (ktx/src/items.c:2703-2704).
	if d.Origin != [3]float32{200, 0, -24} {
		t.Errorf("Origin = %v, want (200,0,-24)", d.Origin)
	}
}

// A hint for a slot the recorder never carried a position for keeps the zero
// origin the loc resolver and every consumer read as "unknown" — a bare
// (0,0,-24) would masquerade as a real point on the map.
func TestBackpackAnalyzer_UnknownPositionKeepsTheZeroOrigin(t *testing.T) {
	a, ctx := newTestBackpackAnalyzer()
	ctx.Players[4] = &events.PlayerInfo{Slot: 4, Name: "ace", Team: "red"}
	_ = a.OnEvent(&events.BackpackDropHintEvent{BackpackEnt: 142, ItemFlags: 32, PlayerEnt: 5, TimeMs: 30000})

	r := &Result{}
	_ = a.Finalize(r)
	if len(r.Backpacks) != 1 {
		t.Fatalf("drops = %v, want 1 entry", r.Backpacks)
	}
	if got := r.Backpacks[0].Origin; got != [3]float32{} {
		t.Errorf("Origin = %v, want the zero vector", got)
	}
}

// LG hint (ItemFlags=64) -> weapon="lg".
func TestBackpackAnalyzer_LGHintEmitsDrop(t *testing.T) {
	a, ctx := newTestBackpackAnalyzer()
	ctx.Players[3] = &events.PlayerInfo{Slot: 3, Name: "lgdropper"}
	_ = a.OnEvent(&events.BackpackDropHintEvent{BackpackEnt: 200, ItemFlags: 64, PlayerEnt: 4, TimeMs: 5000})

	r := &Result{}
	_ = a.Finalize(r)
	out := r.Backpacks
	drops := out
	if drops[0].Weapon != "lg" {
		t.Errorf("Weapon = %q, want lg", drops[0].Weapon)
	}
}

// Entity-state events for backpacks must NOT produce drops. The
// analyzer is hint-only; ItemSpawnEvent / ItemStateEvent are
// ignored.
func TestBackpackAnalyzer_EntityStateEventsIgnored(t *testing.T) {
	a, ctx := newTestBackpackAnalyzer()
	ctx.Players[1] = &events.PlayerInfo{Slot: 1, Name: "p"}

	_ = a.OnEvent(&events.ItemSpawnEvent{EntNum: 50, Kind: "backpack", Origin: [3]float32{0, 0, 0}, TimeMs: 10000})
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 50, Kind: "backpack", Taken: true, TimeMs: 11000})

	r := &Result{}
	_ = a.Finalize(r)
	out := r.Backpacks
	if out != nil {
		t.Errorf("Finalize = %v, want nil (no hint = no drop)", out)
	}
}

// Both-bits and zero-bits ItemFlags are unrecognised combinations
// and should be dropped defensively (KTX never emits these in
// practice, but we don't trust the wire to enforce that).
func TestBackpackAnalyzer_UnrecognisedFlagsDropped(t *testing.T) {
	a, ctx := newTestBackpackAnalyzer()
	ctx.Players[0] = &events.PlayerInfo{Slot: 0, Name: "x"}

	for _, flags := range []int{0, 32 | 64, 1, 4} {
		_ = a.OnEvent(&events.BackpackDropHintEvent{BackpackEnt: 1, ItemFlags: flags, PlayerEnt: 1, TimeMs: 1000})
	}
	r := &Result{}
	_ = a.Finalize(r)
	out := r.Backpacks
	if out != nil {
		t.Errorf("Finalize = %v, want nil (all flag combos unrecognised)", out)
	}
}

// Pre-match hints are ignored (warmup pick-up by KTX admins
// shouldn't pollute the match timeline).
func TestBackpackAnalyzer_PreMatchIgnored(t *testing.T) {
	a := NewBackpackAnalyzer()
	ctx := &Context{}
	_ = a.Init(ctx)
	ctx.Players[0] = &events.PlayerInfo{Slot: 0, Name: "p"}
	// matchStarted intentionally false.

	_ = a.OnEvent(&events.BackpackDropHintEvent{BackpackEnt: 1, ItemFlags: 32, PlayerEnt: 1, TimeMs: 1000})

	r := &Result{}
	_ = a.Finalize(r)
	out := r.Backpacks
	if out != nil {
		t.Errorf("Finalize = %v, want nil (pre-match)", out)
	}
}

// Hint for a slot with no registered player (dropper disconnected
// or bad data) is skipped defensively.
func TestBackpackAnalyzer_UnknownSlotSkipped(t *testing.T) {
	a, _ := newTestBackpackAnalyzer()

	// PlayerEnt=10 -> slot=9, but ctx.Players[9] is nil.
	_ = a.OnEvent(&events.BackpackDropHintEvent{BackpackEnt: 1, ItemFlags: 32, PlayerEnt: 10, TimeMs: 1000})
	r := &Result{}
	_ = a.Finalize(r)
	out := r.Backpacks
	if out != nil {
		t.Errorf("Finalize = %v, want nil (unknown slot)", out)
	}
}

// Multiple hints from multiple players produce one entry each,
// sorted by time.
func TestBackpackAnalyzer_SortedByTime(t *testing.T) {
	a, ctx := newTestBackpackAnalyzer()
	ctx.Players[0] = &events.PlayerInfo{Slot: 0, Name: "a"}
	ctx.Players[1] = &events.PlayerInfo{Slot: 1, Name: "b"}

	// Submit out of order: t=20 first, then t=10.
	_ = a.OnEvent(&events.BackpackDropHintEvent{BackpackEnt: 10, ItemFlags: 32, PlayerEnt: 1, TimeMs: 20000})
	_ = a.OnEvent(&events.BackpackDropHintEvent{BackpackEnt: 20, ItemFlags: 64, PlayerEnt: 2, TimeMs: 10000})

	r := &Result{}
	_ = a.Finalize(r)
	out := r.Backpacks
	drops := out
	if drops[0].Time != 10000 || drops[1].Time != 20000 {
		t.Errorf("times = %v, want [10000, 20000]", []int32{drops[0].Time, drops[1].Time})
	}
	if drops[0].Player != "b" || drops[1].Player != "a" {
		t.Errorf("players = %q, %q, want b, a", drops[0].Player, drops[1].Player)
	}
}

// `//ktx expire <ent>` fires from SUB_Remove 120 s after DropBackpack armed it
// (ktx/src/g_spawn.c:196-210, items.c:2870-2872) and stamps the drop it closes
// with the wire's own verdict.
func TestBackpackAnalyzer_ExpireHintStampsFate(t *testing.T) {
	a, ctx := newTestBackpackAnalyzer()
	ctx.Players[4] = &events.PlayerInfo{Slot: 4, Name: "ace", Team: "red"}

	_ = a.OnEvent(&events.BackpackDropHintEvent{BackpackEnt: 142, ItemFlags: 32, PlayerEnt: 5, TimeMs: 30000})
	_ = a.OnEvent(&events.BackpackExpireHintEvent{BackpackEnt: 142, TimeMs: 150000})

	r := &Result{}
	_ = a.Finalize(r)
	if len(r.Backpacks) != 1 {
		t.Fatalf("drops = %v, want 1 entry", r.Backpacks)
	}
	if got := r.Backpacks[0].Fate; got != backpackFateExpired {
		t.Errorf("Fate = %q, want %q", got, backpackFateExpired)
	}
}

// A pack the wire never announced removed keeps an EMPTY fate on a hint row:
// the absence of `//ktx expire` says nothing, and neither does the absence of
// `//ktx bp`, which is exactly why the hint had to be decoded.
func TestBackpackAnalyzer_NoExpireHintLeavesFateEmpty(t *testing.T) {
	a, ctx := newTestBackpackAnalyzer()
	ctx.Players[4] = &events.PlayerInfo{Slot: 4, Name: "ace"}
	_ = a.OnEvent(&events.BackpackDropHintEvent{BackpackEnt: 142, ItemFlags: 32, PlayerEnt: 5, TimeMs: 30000})

	r := &Result{}
	_ = a.Finalize(r)
	if got := r.Backpacks[0].Fate; got != "" {
		t.Errorf("Fate = %q, want empty", got)
	}
}

// One match runs many packs through one edict, so the join is by edict AND
// time: the expiry closes the newest drop on that edict at KTX's own timeout,
// never the first row that happens to share the number.
func TestBackpackAnalyzer_ExpireHintPicksTheRightPackOnARecycledEdict(t *testing.T) {
	a, ctx := newTestBackpackAnalyzer()
	ctx.Players[0] = &events.PlayerInfo{Slot: 0, Name: "a"}
	ctx.Players[1] = &events.PlayerInfo{Slot: 1, Name: "b"}

	_ = a.OnEvent(&events.BackpackDropHintEvent{BackpackEnt: 142, ItemFlags: 32, PlayerEnt: 1, TimeMs: 10000})
	_ = a.OnEvent(&events.BackpackDropHintEvent{BackpackEnt: 142, ItemFlags: 64, PlayerEnt: 2, TimeMs: 40000})
	_ = a.OnEvent(&events.BackpackExpireHintEvent{BackpackEnt: 142, TimeMs: 160000})

	r := &Result{}
	_ = a.Finalize(r)
	if len(r.Backpacks) != 2 {
		t.Fatalf("drops = %v, want 2 entries", r.Backpacks)
	}
	if r.Backpacks[0].Fate != "" {
		t.Errorf("first drop Fate = %q, want empty (its pack was gone by then)", r.Backpacks[0].Fate)
	}
	if r.Backpacks[1].Fate != backpackFateExpired {
		t.Errorf("second drop Fate = %q, want %q", r.Backpacks[1].Fate, backpackFateExpired)
	}
}

// An expiry that does not sit at its candidate's 120 s deadline belongs to a
// pack this analyzer has no row for — a warmup drop, or one it refused — and
// is left on the floor rather than attached to the nearest row.
func TestBackpackAnalyzer_ExpireHintOutsideTheTimeoutIsDropped(t *testing.T) {
	a, ctx := newTestBackpackAnalyzer()
	ctx.Players[0] = &events.PlayerInfo{Slot: 0, Name: "a"}

	_ = a.OnEvent(&events.BackpackDropHintEvent{BackpackEnt: 142, ItemFlags: 32, PlayerEnt: 1, TimeMs: 10000})
	// 5 s after the drop: far too early for SUB_Remove.
	_ = a.OnEvent(&events.BackpackExpireHintEvent{BackpackEnt: 142, TimeMs: 15000})
	// An edict with no drop row at all.
	_ = a.OnEvent(&events.BackpackExpireHintEvent{BackpackEnt: 999, TimeMs: 130000})

	r := &Result{}
	_ = a.Finalize(r)
	if len(r.Backpacks) != 1 {
		t.Fatalf("drops = %v, want 1 entry", r.Backpacks)
	}
	if got := r.Backpacks[0].Fate; got != "" {
		t.Errorf("Fate = %q, want empty", got)
	}
}
