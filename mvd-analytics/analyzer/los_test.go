package analyzer

import (
	"errors"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/bspvis"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

func staticTrack(t []int32, x, y, z float32) *result.PositionTrack {
	pt := &result.PositionTrack{T: t}
	for range t {
		pt.X = append(pt.X, x)
		pt.Y = append(pt.Y, y)
		pt.Z = append(pt.Z, z)
	}
	return pt
}

func TestLosTargets(t *testing.T) {
	var dst [9][3]float32
	losTargets(100, 200, 50, &dst)
	want := map[[3]float32]bool{
		{84, 184, 26}: true, {84, 184, 82}: true,
		{84, 216, 26}: true, {84, 216, 82}: true,
		{116, 184, 26}: true, {116, 184, 82}: true,
		{116, 216, 26}: true, {116, 216, 82}: true,
		{100, 200, 54}: true, // midpoint = origin + (0,0,4)
	}
	if len(want) != 9 {
		t.Fatalf("test setup: want 9 distinct points, got %d", len(want))
	}
	for i, p := range dst {
		if !want[p] {
			t.Errorf("target %d = %v not among expected corners+midpoint", i, p)
		}
	}
}

// LOS liveness now comes from PlayerStream.Alive via makeAliveGate, not from
// a local re-derivation. These are the cases the removed losAliveAt covered,
// re-pointed at the path LOS actually takes — plus the case that motivated
// removing it.
func TestLosLivenessFromAlive(t *testing.T) {
	// Realistic KTX ordering: the match-start spawn is NOT recorded, so the
	// first event is a death; each recorded spawn is a respawn that follows it.
	alive := aliveOfMarkers(t, []int32{450, 900}, []int32{300, 700}, 2000)
	gate := makeAliveGate(alive)
	cases := []struct {
		t    int32
		want bool
	}{
		{50, true},   // before first death → alive since match start
		{200, true},  // still pre-first-death → alive
		{300, false}, // at first death → dead
		{400, false}, // dead between death and respawn
		{450, true},  // respawn
		{600, true},  // alive
		{700, false}, // second death
		{800, false}, // dead awaiting respawn
		{900, true},  // second respawn
	}
	for _, c := range cases {
		if got := gate(c.t); got != c.want {
			t.Errorf("alive at t=%d = %v, want %v (alive=%v)", c.t, got, c.want, alive)
		}
	}

	// No spawn/death records → alive throughout.
	if !makeAliveGate(aliveOfMarkers(t, nil, nil, 2000))(1234) {
		t.Errorf("empty spawns/deaths must read alive")
	}
	// Deaths only (no respawn recorded) → alive until the death, dead after.
	deathsOnly := makeAliveGate(aliveOfMarkers(t, nil, []int32{500}, 2000))
	if !deathsOnly(400) {
		t.Errorf("deaths-only: should be alive before the death")
	}
	if deathsOnly(600) {
		t.Errorf("deaths-only: should be dead after the death")
	}
}

// The reason losAliveAt was removed rather than kept. Its rule was "alive iff
// the most recent spawn is STRICTLY later than the most recent death", which
// LATCHES on a same-millisecond death+respawn: the two are equal, so it reads
// dead, and keeps reading dead until some later spawn arrives — the whole
// remaining life, not an instant. Measured on cached demos before removal:
// 100.7 s of one player's 1143.7 s match (8.8%), 46.9 s of another's.
func TestLosLivenessSurvivesSameMsRespawn(t *testing.T) {
	const tie = 10000
	gate := makeAliveGate(aliveOfMarkers(t, []int32{tie}, []int32{tie}, 60000))

	for _, at := range []int32{tie, tie + 1, tie + 5000, 59000} {
		if !gate(at) {
			t.Errorf("t=%d reads DEAD after a same-ms death+respawn at %d; "+
				"the player respawned instantly and is alive", at, tie)
		}
	}
}

// aliveOfMarkers runs the real derivation so these tests exercise the same
// path the pipeline does, rather than a hand-built interval list.
func aliveOfMarkers(t *testing.T, spawns, deaths []int32, matchEnd int32) []result.Interval {
	t.Helper()
	s := &result.Streams{
		Global:  result.GlobalStream{MatchEnd: matchEnd},
		Players: []result.PlayerStream{{Name: "p", Spawns: spawns, Deaths: deaths}},
	}
	deriveAliveIntervals(s)
	return s.Players[0].Alive
}

// TestComputeLOS_NoBSP: a 2-player demo whose map has no provisioned BSP returns
// ErrNoBSP and does NOT latch, so a later request (after the BSP is provisioned)
// retries instead of serving a poisoned empty. LOS/PVS stay absent.
func TestComputeLOS_NoBSP(t *testing.T) {
	ts := []int32{0, 50}
	res := &Result{
		DemoInfo: &DemoInfoResult{Map: "zzz_no_such_map_xyz"},
		Streams: &result.Streams{
			Players: []result.PlayerStream{
				{Name: "A", Position: staticTrack(ts, 0, 0, 0)},
				{Name: "B", Position: staticTrack(ts, 100, 0, 0)},
			},
		},
	}
	err := ComputeLOS(res)
	if !errors.Is(err, ErrNoBSP) {
		t.Fatalf("ComputeLOS on a BSP-less map = %v; want ErrNoBSP", err)
	}
	if res.Streams.LOSComputed {
		t.Errorf("ComputeLOS must NOT latch on a BSP-less map (poisoned-cache root cause)")
	}
	for i := range res.Streams.Players {
		if res.Streams.Players[i].LOS != nil || res.Streams.Players[i].PVS != nil {
			t.Errorf("player %q got LOS/PVS on a BSP-less map", res.Streams.Players[i].Name)
		}
	}
}

// TestComputeLOS_NoMapName: a demo carrying no map name at all cannot resolve a
// BSP → ErrNoBSP, no latch.
func TestComputeLOS_NoMapName(t *testing.T) {
	ts := []int32{0, 50}
	res := &Result{
		Streams: &result.Streams{
			Players: []result.PlayerStream{
				{Name: "A", Position: staticTrack(ts, 0, 0, 0)},
				{Name: "B", Position: staticTrack(ts, 100, 0, 0)},
			},
		},
	}
	if err := ComputeLOS(res); !errors.Is(err, ErrNoBSP) {
		t.Fatalf("ComputeLOS with no map name = %v; want ErrNoBSP", err)
	}
	if res.Streams.LOSComputed {
		t.Errorf("ComputeLOS must NOT latch when the demo carries no map name")
	}
}

// TestComputeLOS_FewerThanTwoPlayers: a legitimately empty demo (<2 players)
// returns nil, LATCHES, and is persistable (encodeLOS reports ok) — it must
// stay cacheable, unlike the ErrNoBSP cases.
func TestComputeLOS_FewerThanTwoPlayers(t *testing.T) {
	res := &Result{
		DemoInfo: &DemoInfoResult{Map: "zzz_no_such_map_xyz"},
		Streams: &result.Streams{
			Players: []result.PlayerStream{
				{Name: "A", Position: staticTrack([]int32{0, 50}, 0, 0, 0)},
			},
		},
	}
	if err := ComputeLOS(res); err != nil {
		t.Fatalf("ComputeLOS with <2 players = %v; want nil (legitimately empty)", err)
	}
	if !res.Streams.LOSComputed {
		t.Errorf("a <2-player demo must latch (legitimately empty, cacheable)")
	}
	if _, ok, err := encodeLOS(res); err != nil || !ok {
		t.Errorf("encodeLOS on a latched empty demo: ok=%v err=%v; want ok=true (persistable)", ok, err)
	}
}

// TestComputeLOS_AlreadyLatched: the fast path — an already-latched Result
// returns nil without touching the map/BSP, even for one that would otherwise
// error.
func TestComputeLOS_AlreadyLatched(t *testing.T) {
	res := &Result{
		DemoInfo: &DemoInfoResult{Map: "zzz_no_such_map_xyz"},
		Streams: &result.Streams{
			LOSComputed: true,
			Players: []result.PlayerStream{
				{Name: "A", Position: staticTrack([]int32{0, 50}, 0, 0, 0)},
				{Name: "B", Position: staticTrack([]int32{0, 50}, 100, 0, 0)},
			},
		},
	}
	if err := ComputeLOS(res); err != nil {
		t.Errorf("already-latched ComputeLOS = %v; want nil (fast path)", err)
	}
}

// openWorldBSP is a degenerate map: one interior node whose two children are
// the same empty leaf, that leaf carrying no vis row (so it sees everything),
// and no solid anywhere but the leaf-0 sink. Every pair is therefore mutually
// visible at every sample, which leaves the two liveness gates in losForLooker
// as the only thing the emitted intervals can depend on.
func openWorldBSP() *bspvis.BSP {
	return &bspvis.BSP{
		Version: "v29",
		// Axial plane 1e6 units below the world, so every point and every box
		// is on its front side and resolves to the single empty leaf.
		Planes: []bspvis.Plane{{Normal: bspvis.Vec3{Z: 1}, Dist: -1e6, Type: 2}},
		Nodes:  []bspvis.Node{{PlaneID: 0, Children: [2]int32{-2, -2}}},
		Leaves: []bspvis.Leaf{
			{Contents: bspvis.ContentsSolid},
			{Contents: bspvis.ContentsEmpty, VisOfs: -1},
		},
		Models: []bspvis.Model{{HeadNodes: [4]int32{0}}},
	}
}

// LOS is computed through the two alive gates in losForLooker: the looker's
// own (no eye, no rays while dead) and each opponent's (a corpse, or the
// bouncing gib head the player entity becomes, is not a sightline). Deleting
// either only moved the BSP-gated golden corpus, so on a machine without
// provisioned BSPs both could be removed silently. This runs the real walk
// against a hand-built open world where visibility is decided by nothing else.
func TestLosForLookerGatesOnAlive(t *testing.T) {
	vb := openWorldBSP()
	var ts []int32
	for ms := int32(0); ms <= 2000; ms += 100 {
		ts = append(ts, ms)
	}
	const matchEnd = 2000
	full := []result.Interval{{Start: 0, End: matchEnd}}
	// One death at 800, respawn at 1200.
	split := []result.Interval{{Start: 0, End: 800}, {Start: 1200, End: matchEnd}}

	seesAt := func(lookerAlive, otherAlive []result.Interval, at int32) bool {
		players := []result.PlayerStream{
			{Name: "A", Position: staticTrack(ts, 0, 0, 0), Alive: lookerAlive},
			{Name: "B", Position: staticTrack(ts, 200, 0, 0), Alive: otherAlive},
		}
		los, _ := losForLooker(vb, players, 0, nil, matchEnd, map[int][]byte{}, buildEntityLeaves(vb, players))
		for _, tr := range los {
			if tr.Other != 1 {
				continue
			}
			for _, iv := range tr.Iv {
				if iv.Start <= at && at < iv.End {
					return true
				}
			}
		}
		return false
	}

	if !seesAt(full, full, 1000) {
		t.Fatal("two live players in an open world do not see each other — the fixture pins nothing")
	}
	if seesAt(full, split, 1000) {
		t.Error("the looker sees an opponent who is DEAD at t=1000: the opponent liveness gate is gone")
	}
	if seesAt(split, full, 1000) {
		t.Error("a DEAD looker still has line of sight at t=1000: the looker liveness gate is gone")
	}
	if !seesAt(full, split, 400) {
		t.Error("the opponent's live period at t=400 lost its sightline too — the gate is over-broad")
	}
}
