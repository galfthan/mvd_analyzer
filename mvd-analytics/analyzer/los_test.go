package analyzer

import (
	"errors"
	"testing"

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
