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

func TestLosAliveAt(t *testing.T) {
	// Realistic KTX ordering: the match-start spawn is NOT recorded, so the
	// first event is a death; each recorded spawn is a respawn that follows it.
	deaths := []int32{300, 700}
	spawns := []int32{450, 900}
	cases := []struct {
		t    int32
		want bool
	}{
		{50, true},   // before first death → alive since match start (no spawn recorded yet)
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
		if got := losAliveAt(spawns, deaths, c.t); got != c.want {
			t.Errorf("losAliveAt(t=%d) = %v, want %v", c.t, got, c.want)
		}
	}
	// No spawn/death records → alive throughout.
	if !losAliveAt(nil, nil, 1234) {
		t.Errorf("empty spawns/deaths must read alive")
	}
	// Deaths only (no respawn recorded) → alive until the death, dead after.
	if !losAliveAt(nil, []int32{500}, 400) {
		t.Errorf("deaths-only: should be alive before the death")
	}
	if losAliveAt(nil, []int32{500}, 600) {
		t.Errorf("deaths-only: should be dead after the death")
	}
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
