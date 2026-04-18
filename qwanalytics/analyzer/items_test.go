package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/qwanalytics/items"
	"github.com/mvd-analyzer/qwdemo/events"
	"github.com/mvd-analyzer/qwdemo/mvd"
)

// newTestItemAnalyzer wires an analyzer with a fixed in-memory item
// corpus — lets tests exercise the binding + phase-tracking logic
// without touching the embedded per-map JSONs.
func newTestItemAnalyzer(mapItems []items.MapItem) (*ItemAnalyzer, *Context) {
	a := NewItemAnalyzer()
	ctx := &Context{FragsBySlot: map[int]int{}}
	_ = a.Init(ctx)
	a.mapItems = mapItems
	a.matchStarted = true
	a.matchStartTime = 0
	return a, ctx
}

func TestItemAnalyzer_RAPickupAndRespawn(t *testing.T) {
	a, ctx := newTestItemAnalyzer([]items.MapItem{
		{Kind: "ra", X: 100, Y: 0, Z: 0},
	})
	ctx.Players[2] = &mvd.PlayerInfo{Slot: 2, Name: "nexus", Team: "ahoy"}
	a.playerPos[2] = [3]float32{110, 0, 0}

	a.handleKTX(&events.StuffTextEvent{Command: "//ktx took 5 20 3", Time: 10})
	// Simulate next pickup just after respawn so a second phase opens.
	ctx.Players[3] = &mvd.PlayerInfo{Slot: 3, Name: "ocoini", Team: "-cv-"}
	a.playerPos[3] = [3]float32{105, 0, 0}
	a.handleKTX(&events.StuffTextEvent{Command: "//ktx took 5 20 4", Time: 31})

	out, err := a.Finalize()
	if err != nil || out == nil {
		t.Fatalf("finalize: %v %v", out, err)
	}
	res := out.(*ItemsResult)
	if len(res.Items) != 1 {
		t.Fatalf("Items = %d, want 1", len(res.Items))
	}
	it := res.Items[0]
	if it.Kind != "ra" || it.Name != "ra" {
		t.Errorf("name/kind = %q/%q", it.Name, it.Kind)
	}
	if len(it.Phases) != 2 {
		t.Fatalf("phases = %+v", it.Phases)
	}
	if it.Phases[0].TakenAt != 10 || it.Phases[0].RespawnAt != 30 ||
		it.Phases[0].TakenBy != "nexus" || it.Phases[0].Team != "ahoy" {
		t.Errorf("phase[0] = %+v", it.Phases[0])
	}
	if it.Phases[1].AvailableFrom != 30 || it.Phases[1].TakenAt != 31 ||
		it.Phases[1].RespawnAt != 51 || it.Phases[1].TakenBy != "ocoini" {
		t.Errorf("phase[1] = %+v", it.Phases[1])
	}
}

// MH pickup emits took(ent, 0, p); timer(ent, 20) fires later when
// the rot brings health back to max. RespawnAt should be set from the
// timer event's time, not the pickup time.
func TestItemAnalyzer_MHRotTimer(t *testing.T) {
	a, ctx := newTestItemAnalyzer([]items.MapItem{
		{Kind: "mh", X: 0, Y: 0, Z: 0},
	})
	ctx.Players[2] = &mvd.PlayerInfo{Slot: 2, Name: "p1", Team: "red"}
	a.playerPos[2] = [3]float32{5, 0, 0}

	a.handleKTX(&events.StuffTextEvent{Command: "//ktx took 12 0 3", Time: 10})
	a.handleKTX(&events.StuffTextEvent{Command: "//ktx timer 12 20", Time: 45})

	out, _ := a.Finalize()
	res := out.(*ItemsResult)
	if len(res.Items) != 1 {
		t.Fatalf("Items = %d", len(res.Items))
	}
	p := res.Items[0].Phases[0]
	if p.TakenAt != 10 {
		t.Errorf("TakenAt = %v", p.TakenAt)
	}
	if p.RespawnAt != 65 {
		t.Errorf("RespawnAt = %v (want 65)", p.RespawnAt)
	}
}

// Two MHs on the same map get separate phase timelines bound to
// distinct MapItems by position.
func TestItemAnalyzer_TwoMHsOnSchloss(t *testing.T) {
	a, ctx := newTestItemAnalyzer([]items.MapItem{
		{Kind: "mh", X: 1000, Y: 0, Z: 0},
		{Kind: "mh", X: -1000, Y: 0, Z: 0},
	})
	ctx.Players[2] = &mvd.PlayerInfo{Slot: 2, Name: "p1"}
	ctx.Players[3] = &mvd.PlayerInfo{Slot: 3, Name: "p2"}
	a.playerPos[2] = [3]float32{1005, 0, 0}
	a.playerPos[3] = [3]float32{-1005, 0, 0}

	a.handleKTX(&events.StuffTextEvent{Command: "//ktx took 20 0 3", Time: 10})
	a.handleKTX(&events.StuffTextEvent{Command: "//ktx took 21 0 4", Time: 11})
	a.handleKTX(&events.StuffTextEvent{Command: "//ktx timer 20 20", Time: 40})
	a.handleKTX(&events.StuffTextEvent{Command: "//ktx timer 21 20", Time: 42})

	out, _ := a.Finalize()
	res := out.(*ItemsResult)
	if len(res.Items) != 2 {
		t.Fatalf("Items = %d, want 2", len(res.Items))
	}
	// Items sort by (kind, name); since both kind=mh, names are mh_1
	// and mh_2 by position sort (X ascending). mh_1 is X=-1000, mh_2
	// is X=1000.
	if res.Items[0].Name != "mh_1" || res.Items[1].Name != "mh_2" {
		t.Fatalf("names = %q / %q", res.Items[0].Name, res.Items[1].Name)
	}
	// mh_1 picked up at t=11 by p2, respawn at 62. mh_2 at t=10 by p1,
	// respawn at 60.
	if res.Items[0].Phases[0].TakenBy != "p2" || res.Items[0].Phases[0].RespawnAt != 62 {
		t.Errorf("mh_1 phase[0] = %+v", res.Items[0].Phases[0])
	}
	if res.Items[1].Phases[0].TakenBy != "p1" || res.Items[1].Phases[0].RespawnAt != 60 {
		t.Errorf("mh_2 phase[0] = %+v", res.Items[1].Phases[0])
	}
}

// Quad powerup with 30s practice timer.
func TestItemAnalyzer_QuadPowerup(t *testing.T) {
	a, ctx := newTestItemAnalyzer([]items.MapItem{
		{Kind: "quad", X: 500, Y: 500, Z: 0},
	})
	ctx.Players[2] = &mvd.PlayerInfo{Slot: 2, Name: "qp"}
	a.playerPos[2] = [3]float32{495, 505, 0}

	a.handleKTX(&events.StuffTextEvent{Command: "//ktx took 30 30 3", Time: 5})

	out, _ := a.Finalize()
	res := out.(*ItemsResult)
	p := res.Items[0].Phases[0]
	if p.TakenAt != 5 || p.RespawnAt != 35 {
		t.Errorf("quad phase = %+v", p)
	}
}
