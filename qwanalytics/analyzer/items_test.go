package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/qwdemo/events"
	"github.com/mvd-analyzer/qwdemo/mvd"
)

// newTestItemAnalyzer wires an analyzer for unit tests — skips the
// match-boundary detection by marking the match pre-started so every
// event we feed in is counted.
func newTestItemAnalyzer() (*ItemAnalyzer, *Context) {
	a := NewItemAnalyzer()
	ctx := &Context{FragsBySlot: map[int]int{}}
	_ = a.Init(ctx)
	a.matchStarted = true
	return a, ctx
}

// Single RA, one pickup and respawn. Confirms the happy path: phase
// opens at 0, closes on ItemStateEvent{Taken:true}, and a new
// available phase opens on ItemStateEvent{Taken:false}.
func TestItemAnalyzer_RAPickupRespawn(t *testing.T) {
	a, ctx := newTestItemAnalyzer()
	ctx.Players[2] = &mvd.PlayerInfo{Slot: 2, Name: "nexus", Team: "ahoy"}
	a.playerPos[2] = [3]float32{100, 0, 0}

	// Baseline: RA at origin (100, 0, 0), ent 75.
	_ = a.OnEvent(&events.ItemSpawnEvent{EntNum: 75, Kind: "ra", Origin: [3]float32{100, 0, 0}, Time: 0})
	// Pickup at t=10.
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 75, Kind: "ra", Taken: true, Time: 10})
	// Respawn at t=30.
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 75, Kind: "ra", Taken: false, Time: 30})

	out, _ := a.Finalize()
	res := out.(*ItemsResult)
	if len(res.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(res.Items))
	}
	it := res.Items[0]
	if it.Kind != "ra" || it.EntNum != 75 {
		t.Errorf("item meta = %+v", it)
	}
	if len(it.Phases) != 2 {
		t.Fatalf("phases = %+v", it.Phases)
	}
	p0 := it.Phases[0]
	if p0.AvailableFrom != 0 || p0.TakenAt != 10 || p0.TakenBy != "nexus" || p0.Team != "ahoy" {
		t.Errorf("phase[0] = %+v", p0)
	}
	if it.Phases[1].AvailableFrom != 30 || it.Phases[1].TakenAt != 0 {
		t.Errorf("phase[1] = %+v", it.Phases[1])
	}
}

// Two MHs on the same map get separate phase timelines keyed by
// ent num. Names are deterministic ("mh_1", "mh_2") by x-coordinate.
func TestItemAnalyzer_TwoMHs(t *testing.T) {
	a, ctx := newTestItemAnalyzer()
	ctx.Players[2] = &mvd.PlayerInfo{Slot: 2, Name: "p1"}
	ctx.Players[3] = &mvd.PlayerInfo{Slot: 3, Name: "p2"}
	a.playerPos[2] = [3]float32{1000, 0, 0}
	a.playerPos[3] = [3]float32{-1000, 0, 0}

	// Baseline two MHs.
	_ = a.OnEvent(&events.ItemSpawnEvent{EntNum: 20, Kind: "mh", Origin: [3]float32{1000, 0, 0}})
	_ = a.OnEvent(&events.ItemSpawnEvent{EntNum: 21, Kind: "mh", Origin: [3]float32{-1000, 0, 0}})
	// Two pickups at different times.
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 20, Kind: "mh", Taken: true, Time: 10})
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 21, Kind: "mh", Taken: true, Time: 11})
	// Respawns — MH rot makes durations irregular, but that's fine:
	// the parser observes the actual restore event.
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 20, Kind: "mh", Taken: false, Time: 45})
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 21, Kind: "mh", Taken: false, Time: 62})

	out, _ := a.Finalize()
	res := out.(*ItemsResult)
	if len(res.Items) != 2 {
		t.Fatalf("want 2 items, got %d", len(res.Items))
	}
	if res.Items[0].Name != "mh_1" || res.Items[1].Name != "mh_2" {
		t.Errorf("names = %q, %q", res.Items[0].Name, res.Items[1].Name)
	}
	// mh_1 is the one with X=-1000 (p2 picked up).
	if res.Items[0].Phases[0].TakenBy != "p2" {
		t.Errorf("mh_1 picker = %q", res.Items[0].Phases[0].TakenBy)
	}
	if res.Items[0].Phases[0].RespawnAt != 62 {
		t.Errorf("mh_1 respawn = %v", res.Items[0].Phases[0].RespawnAt)
	}
}

// Items with no pickup events still show up in the output with a
// single open "available from 0" phase — works on non-KTX demos and
// on items nobody touched.
func TestItemAnalyzer_UntouchedItemListed(t *testing.T) {
	a, _ := newTestItemAnalyzer()
	_ = a.OnEvent(&events.ItemSpawnEvent{EntNum: 50, Kind: "lg", Origin: [3]float32{0, 0, 0}})
	out, _ := a.Finalize()
	res := out.(*ItemsResult)
	if len(res.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(res.Items))
	}
	if len(res.Items[0].Phases) != 1 || res.Items[0].Phases[0].TakenAt != 0 {
		t.Errorf("untouched item phases = %+v", res.Items[0].Phases)
	}
}

// Pre-match events should be ignored so warmup item bouncing doesn't
// pollute the phase list.
func TestItemAnalyzer_PreMatchEventsIgnored(t *testing.T) {
	a := NewItemAnalyzer()
	ctx := &Context{FragsBySlot: map[int]int{}}
	_ = a.Init(ctx)
	// matchStarted left false.

	_ = a.OnEvent(&events.ItemSpawnEvent{EntNum: 75, Kind: "ra", Origin: [3]float32{0, 0, 0}})
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 75, Kind: "ra", Taken: true, Time: 2})
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 75, Kind: "ra", Taken: false, Time: 5})

	out, _ := a.Finalize()
	res := out.(*ItemsResult)
	if len(res.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(res.Items))
	}
	// The item exists (we got the baseline spawn) but no phases
	// should have closed because the match never started.
	if res.Items[0].Phases[0].TakenAt != 0 {
		t.Errorf("phase shouldn't have closed during warmup: %+v", res.Items[0].Phases[0])
	}
}
