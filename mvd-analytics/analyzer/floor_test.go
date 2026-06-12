package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/mapclip"
	"github.com/mvd-analyzer/mvd-analytics/mapgen/bsp"
	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// floorTestHull builds a one-plane clip hull (floor surface at Z=0): a
// horizontal clip plane at Z=24 with EMPTY above, SOLID below. Mirrors
// the mapclip package's own fixture; a grounded player at origin Z=24
// then reads floor 0.
func floorTestHull(t *testing.T) *mapclip.Hull {
	t.Helper()
	h, err := mapclip.Build(&bsp.BSP{
		Planes:    []bsp.Plane{{Normal: bsp.Vec3{Z: 1}, Dist: 24, Type: 2}},
		Models:    []bsp.Model{{Mins: bsp.Vec3{Z: -100}, HeadNodes: [4]int32{0, 0, 0, 0}}},
		ClipNodes: []bsp.ClipNode{{PlaneNum: 0, Children: [2]int32{-1, -2}}},
	})
	if err != nil {
		t.Fatalf("build hull: %v", err)
	}
	return h
}

// TestResolveFloorHeights_PopulatesColumn drives the analyzer's
// per-sample floor trace directly: a player sample at the standing
// height (Z=24) is grounded (H=0), a sample 100 units higher is airborne
// (H=100), and the zero origin gets the NoFloor sentinel.
func TestResolveFloorHeights_PopulatesColumn(t *testing.T) {
	a := NewTimelineAnalyzer()
	st := &timelinePlayerState{}
	// Grounded, airborne, then a (0,0,0) origin that must sentinel out.
	st.streams.recordPosition(0, 0, 0, 24)
	st.streams.recordPosition(100, 0, 0, 124)
	st.streams.recordPosition(200, 0, 0, 0)
	a.playerState[0] = st
	a.clipHull = floorTestHull(t)

	a.resolveFloorHeights()

	b := &st.streams
	if len(b.posH) != 3 {
		t.Fatalf("posH len = %d, want 3", len(b.posH))
	}
	if b.posH[0] != 0 {
		t.Errorf("grounded sample height = %d, want 0", b.posH[0])
	}
	if b.posH[1] != 100 {
		t.Errorf("airborne sample height = %d, want 100", b.posH[1])
	}
	if b.posH[2] != result.NoFloor {
		t.Errorf("zero-origin sample height = %d, want NoFloor sentinel", b.posH[2])
	}
}

// moverTrack hold-last semantics: the pose at t is the last state at or
// before t, clamped to the first state before the spawn.
func TestMoverTrackAtCursor(t *testing.T) {
	mt := &moverTrack{subModel: 1}
	mt.append(1000, [3]float32{0, 0, 0}, true)
	mt.append(2000, [3]float32{0, 0, 50}, true)
	mt.append(3000, [3]float32{0, 0, 50}, false)

	cur := -1
	cases := []struct {
		t   int32
		z   float32
		vis bool
	}{
		{500, 0, true},   // before first state: clamp to baseline pose
		{1000, 0, true},  // exact hit
		{1500, 0, true},  // hold-last between states
		{2500, 50, true}, // after the move
		{9000, 50, false}, // after the visibility flip
	}
	for _, c := range cases {
		org, vis := mt.atCursor(c.t, &cur)
		if org[2] != c.z || vis != c.vis {
			t.Errorf("at(%d) = (z=%v,vis=%v), want (z=%v,vis=%v)", c.t, org[2], vis, c.z, c.vis)
		}
	}
}

// End-to-end mover scene: a two-model BSP (deep shaft + lift platform)
// with the lift's pose streamed through Mover events. A player riding
// the lift reads ~0 at every lift height; with the lift invisible or
// the player outside its posed bounds, the shaft floor answers.
func TestResolveFloorHeights_StandsOnMover(t *testing.T) {
	b := &bsp.BSP{
		Planes: []bsp.Plane{
			{Normal: bsp.Vec3{Z: 1}, Dist: -376, Type: 2}, // shaft floor (Z=-400)
			{Normal: bsp.Vec3{Z: 1}, Dist: 24, Type: 2},   // lift top (local Z=0)
		},
		Models: []bsp.Model{
			{Mins: bsp.Vec3{X: -1000, Y: -1000, Z: -500}, Maxs: bsp.Vec3{X: 1000, Y: 1000, Z: 500}, HeadNodes: [4]int32{0, 0, 0, 0}},
			{Mins: bsp.Vec3{X: -64, Y: -64, Z: -16}, Maxs: bsp.Vec3{X: 64, Y: 64, Z: 0}, HeadNodes: [4]int32{0, 1, 0, 0}},
		},
		ClipNodes: []bsp.ClipNode{
			{PlaneNum: 0, Children: [2]int32{-1, -2}},
			{PlaneNum: 1, Children: [2]int32{-1, -2}},
		},
	}
	world, err := mapclip.Build(b)
	if err != nil {
		t.Fatalf("build world: %v", err)
	}
	lift, err := mapclip.BuildModel(b, 1)
	if err != nil {
		t.Fatalf("build lift: %v", err)
	}

	a := NewTimelineAnalyzer()
	a.clipHull = world
	a.moverHulls = map[int]*mapclip.Hull{1: lift}
	// The lift rests at its compiled position (top Z=0), rises to +100
	// at t=1000, and drops out of the frame set at t=3000.
	if err := a.OnEvent(&events.MoverSpawnEvent{EntNum: 50, Model: "*1", SubModel: 1, TimeMs: 0}); err != nil {
		t.Fatal(err)
	}
	if err := a.OnEvent(&events.MoverStateEvent{EntNum: 50, Origin: [3]float32{0, 0, 100}, Visible: true, TimeMs: 1000}); err != nil {
		t.Fatal(err)
	}
	if err := a.OnEvent(&events.MoverStateEvent{EntNum: 50, Origin: [3]float32{0, 0, 100}, Visible: false, TimeMs: 3000}); err != nil {
		t.Fatal(err)
	}

	st := &timelinePlayerState{}
	st.streams.recordPosition(500, 0, 0, 25)    // riding the lift at rest
	st.streams.recordPosition(1500, 0, 0, 125)  // riding the risen lift
	st.streams.recordPosition(2000, 300, 0, 125) // outside the lift bounds, airborne over the shaft
	st.streams.recordPosition(3500, 0, 0, 125)  // lift invisible: shaft floor answers
	a.playerState[0] = st

	a.resolveFloorHeights()

	h := st.streams.posH
	if len(h) != 4 {
		t.Fatalf("posH len = %d, want 4", len(h))
	}
	if h[0] < 0 || h[0] > 2 {
		t.Errorf("rider at rest: H = %d, want ~1", h[0])
	}
	if h[1] < 0 || h[1] > 2 {
		t.Errorf("rider risen: H = %d, want ~1", h[1])
	}
	if want := int32(125 - 24 + 400); h[2] != want {
		t.Errorf("outside lift: H = %d, want %d (shaft floor)", h[2], want)
	}
	if want := int32(125 - 24 + 400); h[3] != want {
		t.Errorf("lift invisible: H = %d, want %d (shaft floor)", h[3], want)
	}
}

// TestResolveFloorHeights_NoHullNoColumn confirms the column stays absent
// (nil) when no clip hull is loaded — the graceful-degradation path that
// keeps the H field off the wire for maps without a provisioned BSP.
func TestResolveFloorHeights_NoHullNoColumn(t *testing.T) {
	a := NewTimelineAnalyzer()
	st := &timelinePlayerState{}
	st.streams.recordPosition(0, 10, 20, 30)
	a.playerState[0] = st
	// a.clipHull left nil.

	a.resolveFloorHeights()

	if st.streams.posH != nil {
		t.Errorf("posH = %v, want nil when no hull loaded", st.streams.posH)
	}
}
