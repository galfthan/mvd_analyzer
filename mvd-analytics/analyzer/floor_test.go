package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/mapclip"
	"github.com/mvd-analyzer/mvd-analytics/mapgen/bsp"
	"github.com/mvd-analyzer/mvd-analytics/result"
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
