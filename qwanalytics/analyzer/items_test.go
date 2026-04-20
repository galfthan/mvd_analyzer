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
// opens at 0, closes on ItemStateEvent{Taken:true} with RespawnAt
// stamped from the kind table (TakenAt + 20 for armor), and a new
// available phase opens on ItemStateEvent{Taken:false}. The wire
// respawn time is NOT what drives RespawnAt any more — this test also
// pins that by deliberately using a late wire respawn (t=45) while
// asserting RespawnAt = 30.
func TestItemAnalyzer_RAPickupRespawn(t *testing.T) {
	a, ctx := newTestItemAnalyzer()
	ctx.Players[2] = &mvd.PlayerInfo{Slot: 2, Name: "nexus", Team: "ahoy"}
	a.playerPos[2] = [3]float32{100, 0, 0}

	_ = a.OnEvent(&events.ItemSpawnEvent{EntNum: 75, Kind: "ra", Origin: [3]float32{100, 0, 0}, Time: 0})
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 75, Kind: "ra", Taken: true, Time: 10})
	// Wire respawn 45 s later — 25 s past the real respawn time.
	// Insta-regrab simulation: we still want RespawnAt=30, not 45.
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 75, Kind: "ra", Taken: false, Time: 45})

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
		t.Errorf("phase[0] meta = %+v", p0)
	}
	if p0.RespawnAt != 30 {
		t.Errorf("phase[0] RespawnAt = %v, want 30 (TakenAt+20)", p0.RespawnAt)
	}
	if it.Phases[1].AvailableFrom != 45 || it.Phases[1].TakenAt != 0 {
		t.Errorf("phase[1] = %+v", it.Phases[1])
	}
}

// Quad with no wire respawn at all — insta-regrabbed every cycle.
// The kind-table fallback is the only thing that can produce a sensible
// RespawnAt; this test pins it at TakenAt + 60.
func TestItemAnalyzer_QuadNominalRespawn(t *testing.T) {
	a, ctx := newTestItemAnalyzer()
	ctx.Players[0] = &mvd.PlayerInfo{Slot: 0, Name: "p"}
	a.playerPos[0] = [3]float32{0, 128, 282}

	_ = a.OnEvent(&events.ItemSpawnEvent{EntNum: 43, Kind: "quad", Origin: [3]float32{0, 128, 282}, Time: 0})
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 43, Kind: "quad", Taken: true, Time: 16.692})
	// No wire respawn yet — quad was insta-regrabbed each cycle.

	out, _ := a.Finalize()
	res := out.(*ItemsResult)
	p0 := res.Items[0].Phases[0]
	got := p0.RespawnAt - p0.TakenAt
	if got < 59.999 || got > 60.001 {
		t.Errorf("quad RespawnAt - TakenAt = %v, want 60", got)
	}
}

// Two MHs on the same map get separate phase timelines keyed by
// ent num. Names are deterministic ("mh_1", "mh_2") by x-coordinate.
// Each MH's RespawnAt is stamped 20 s after its holder's health drops
// to ≤ 100 — not from the wire respawn time.
func TestItemAnalyzer_TwoMHs(t *testing.T) {
	a, ctx := newTestItemAnalyzer()
	ctx.Players[2] = &mvd.PlayerInfo{Slot: 2, Name: "p1"}
	ctx.Players[3] = &mvd.PlayerInfo{Slot: 3, Name: "p2"}
	a.playerPos[2] = [3]float32{1000, 0, 0}
	a.playerPos[3] = [3]float32{-1000, 0, 0}

	_ = a.OnEvent(&events.ItemSpawnEvent{EntNum: 20, Kind: "mh", Origin: [3]float32{1000, 0, 0}})
	_ = a.OnEvent(&events.ItemSpawnEvent{EntNum: 21, Kind: "mh", Origin: [3]float32{-1000, 0, 0}})
	// MH 20 → p1 @ t=10; holder starts at 200 (primed so crossing is
	// observable), rots down past 100 at t=110.
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 2, StatIndex: events.StatHealth, Value: 200, Time: 10})
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 20, Kind: "mh", Taken: true, Time: 10})
	// MH 21 → p2 @ t=11; holder at 200, drops past 100 at t=90.
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 3, StatIndex: events.StatHealth, Value: 200, Time: 11})
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 21, Kind: "mh", Taken: true, Time: 11})

	// Rot-end crossings.
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 2, StatIndex: events.StatHealth, Value: 100, Time: 110})
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 3, StatIndex: events.StatHealth, Value: 100, Time: 90})

	out, _ := a.Finalize()
	res := out.(*ItemsResult)
	if len(res.Items) != 2 {
		t.Fatalf("want 2 items, got %d", len(res.Items))
	}
	if res.Items[0].Name != "mh_1" || res.Items[1].Name != "mh_2" {
		t.Errorf("names = %q, %q", res.Items[0].Name, res.Items[1].Name)
	}
	// mh_1 is the one with X=-1000 (ent 21 → p2 picked up).
	mh1 := res.Items[0].Phases[0]
	if mh1.TakenBy != "p2" {
		t.Errorf("mh_1 picker = %q, want p2", mh1.TakenBy)
	}
	if mh1.RespawnAt != 110 { // rot-end 90 + 20
		t.Errorf("mh_1 RespawnAt = %v, want 110 (90 + 20)", mh1.RespawnAt)
	}
	mh2 := res.Items[1].Phases[0]
	if mh2.RespawnAt != 130 { // rot-end 110 + 20
		t.Errorf("mh_2 RespawnAt = %v, want 130 (110 + 20)", mh2.RespawnAt)
	}
}

// MH rot-end via health tick-down: holder picks up MH at t=10 with 200
// health; rot drains it to 100 at t=110; RespawnAt is then 130.
func TestItemAnalyzer_MHRotTickdown(t *testing.T) {
	a, ctx := newTestItemAnalyzer()
	ctx.Players[0] = &mvd.PlayerInfo{Slot: 0, Name: "p"}
	a.playerPos[0] = [3]float32{0, 0, 0}

	_ = a.OnEvent(&events.ItemSpawnEvent{EntNum: 50, Kind: "mh", Origin: [3]float32{0, 0, 0}})
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 0, StatIndex: events.StatHealth, Value: 200, Time: 10})
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 50, Kind: "mh", Taken: true, Time: 10})
	// Interim rot observations: still > 100.
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 0, StatIndex: events.StatHealth, Value: 150, Time: 60})
	// Final crossing.
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 0, StatIndex: events.StatHealth, Value: 100, Time: 110})

	out, _ := a.Finalize()
	res := out.(*ItemsResult)
	p0 := res.Items[0].Phases[0]
	if p0.RespawnAt != 130 {
		t.Errorf("MH RespawnAt = %v, want 130 (crossing 110 + 20)", p0.RespawnAt)
	}
}

// MH holder dies mid-rot: RespawnAt should be death+20 (assuming death
// comes more than 5 s after pickup).
func TestItemAnalyzer_MHHolderDiesMidRot(t *testing.T) {
	a, ctx := newTestItemAnalyzer()
	ctx.Players[0] = &mvd.PlayerInfo{Slot: 0, Name: "p"}
	a.playerPos[0] = [3]float32{0, 0, 0}

	_ = a.OnEvent(&events.ItemSpawnEvent{EntNum: 50, Kind: "mh", Origin: [3]float32{0, 0, 0}})
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 0, StatIndex: events.StatHealth, Value: 200, Time: 10})
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 50, Kind: "mh", Taken: true, Time: 10})
	// Holder dies at t=30 (way past the 5 s floor).
	_ = a.OnEvent(&events.DeathEvent{PlayerNum: 0, Time: 30})

	out, _ := a.Finalize()
	res := out.(*ItemsResult)
	p0 := res.Items[0].Phases[0]
	if p0.RespawnAt != 50 {
		t.Errorf("MH RespawnAt = %v, want 50 (death 30 + 20)", p0.RespawnAt)
	}
}

// MH holder instant-deaths inside the 5 s first-rot-tick floor: the
// respawn timer can't arm before pickup+5, so RespawnAt = pickup + 5 + 20.
func TestItemAnalyzer_MHInstantDeathFloor(t *testing.T) {
	a, ctx := newTestItemAnalyzer()
	ctx.Players[0] = &mvd.PlayerInfo{Slot: 0, Name: "p"}
	a.playerPos[0] = [3]float32{0, 0, 0}

	_ = a.OnEvent(&events.ItemSpawnEvent{EntNum: 50, Kind: "mh", Origin: [3]float32{0, 0, 0}})
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 0, StatIndex: events.StatHealth, Value: 200, Time: 10})
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 50, Kind: "mh", Taken: true, Time: 10})
	// Rocket to the face at t=10.1, instant death.
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 0, StatIndex: events.StatHealth, Value: 0, Time: 10.1})

	out, _ := a.Finalize()
	res := out.(*ItemsResult)
	p0 := res.Items[0].Phases[0]
	if p0.RespawnAt != 35 { // pickup 10 + 5 (floor) + 20
		t.Errorf("MH RespawnAt = %v, want 35 (pickup+5+20 from the 5 s floor)", p0.RespawnAt)
	}
}

// Same player holds two MHs. KTX lets both be picked up; each has its
// own entity and rot tick, but both run against the same holder health.
// Our detection fires once on the health crossing and stamps both
// entities to the same RespawnAt.
func TestItemAnalyzer_TwoMHsSameHolder(t *testing.T) {
	a, ctx := newTestItemAnalyzer()
	ctx.Players[0] = &mvd.PlayerInfo{Slot: 0, Name: "hog"}
	a.playerPos[0] = [3]float32{0, 0, 0}

	_ = a.OnEvent(&events.ItemSpawnEvent{EntNum: 40, Kind: "mh", Origin: [3]float32{0, 0, 0}})
	_ = a.OnEvent(&events.ItemSpawnEvent{EntNum: 41, Kind: "mh", Origin: [3]float32{1, 0, 0}})
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 0, StatIndex: events.StatHealth, Value: 200, Time: 10})
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 40, Kind: "mh", Taken: true, Time: 10})
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 0, StatIndex: events.StatHealth, Value: 250, Time: 12})
	_ = a.OnEvent(&events.ItemStateEvent{EntNum: 41, Kind: "mh", Taken: true, Time: 12})
	// Rot across both; crossing at t=80.
	_ = a.OnEvent(&events.StatUpdateEvent{PlayerNum: 0, StatIndex: events.StatHealth, Value: 100, Time: 80})

	out, _ := a.Finalize()
	res := out.(*ItemsResult)
	if len(res.Items) != 2 {
		t.Fatalf("want 2 MH items, got %d", len(res.Items))
	}
	for _, it := range res.Items {
		if it.Phases[0].RespawnAt != 100 {
			t.Errorf("%s RespawnAt = %v, want 100 (crossing 80 + 20)", it.Name, it.Phases[0].RespawnAt)
		}
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
