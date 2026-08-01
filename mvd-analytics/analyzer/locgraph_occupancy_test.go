package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Schema v64 replaced loc-graph's clamped forward difference with an exact
// time-weighted integral. These pin the two properties that change made
// possible: a result that does not depend on the sample cadence, and a
// presence window that ends when the player stops broadcasting.

func occupancyResult(p result.PlayerStream, matchEnd int32) *Result {
	return &Result{
		Streams: &result.Streams{
			Global:  result.GlobalStream{MatchStart: 0, MatchEnd: matchEnd},
			Players: []result.PlayerStream{p},
		},
		TimelineAnalysis: &TimelineAnalysisResult{
			LocTable:     []string{"", "A", "B"},
			LocationData: []MapLocation{{Name: "A"}, {Name: "B"}},
		},
		DemoInfo: &DemoInfoResult{Players: []DemoInfoPlayer{{Name: "p", Team: "red"}}},
	}
}

func walkTrack(name string, step int32, li []int16, deaths, spawns []int32) result.PlayerStream {
	pt := &result.PositionTrack{}
	for i, l := range li {
		pt.T = append(pt.T, int32(i)*step)
		pt.X = append(pt.X, float32(i)*10)
		pt.Y = append(pt.Y, 0)
		pt.Z = append(pt.Z, 0)
		pt.Li = append(pt.Li, l)
	}
	return result.PlayerStream{Name: name, Team: "red", Position: pt, Deaths: deaths, Spawns: spawns}
}

func totalsOf(t *testing.T, res *Result) map[string]int32 {
	t.Helper()
	g := BuildLocGraph(res)
	if g == nil {
		t.Fatal("BuildLocGraph returned nil")
	}
	out := map[string]int32{}
	for _, n := range g.Locs {
		out[n.Name] = n.Total
	}
	return out
}

// The MVD sample cadence is server-configured (sv_demofps), and measured
// across the golden corpus it is bimodal — ~13-16 ms on servers at full tick,
// ~34-39 ms on servers at the default. The same span of play must therefore
// integrate to the same time at either cadence. The pre-v64 walk could not do
// this: it credited every sample min(gap, 50 ms), so a 39 ms demo and a 13 ms
// demo disagreed, and a demo at sv_demofps 20 (52 ms) lost time on EVERY
// sample.
func TestLocGraphOccupancyIsCadenceIndependent(t *testing.T) {
	// 780 ms in A then 780 ms in B, expressed at two cadences.
	fine := make([]int16, 0, 120)
	for i := 0; i < 60; i++ {
		fine = append(fine, 1)
	}
	for i := 0; i < 60; i++ {
		fine = append(fine, 2)
	}
	coarse := make([]int16, 0, 40)
	for i := 0; i < 20; i++ {
		coarse = append(coarse, 1)
	}
	for i := 0; i < 20; i++ {
		coarse = append(coarse, 2)
	}

	// A generous match window so neither track's final hold is clipped: both
	// cover the same real span (A over [0,780), B over [780,1560)), and the
	// point is that the integral agrees, not that the window truncates it.
	const matchEnd = 60000
	got13 := totalsOf(t, occupancyResult(walkTrack("p", 13, fine, nil, nil), matchEnd))
	got39 := totalsOf(t, occupancyResult(walkTrack("p", 39, coarse, nil, nil), matchEnd))

	for _, loc := range []string{"A", "B"} {
		if got13[loc] != got39[loc] {
			t.Errorf("%s: 13ms cadence gives %d ms, 39ms cadence gives %d ms — occupancy must not depend on the recording rate",
				loc, got13[loc], got39[loc])
		}
	}
	if got13["A"] != 780 {
		t.Errorf("A at 13ms cadence = %d ms, want 780", got13["A"])
	}
}

// Sample-and-hold has no staleness bound of its own, so a player who
// disconnects mid-match would otherwise hold their final loc through to match
// end — minutes of presence invented for someone who left. The walk is bounded
// by the player's own last sample (plus one cadence of hold, the same tail
// every other sample gets).
func TestLocGraphOccupancyStopsAtTheLastSample(t *testing.T) {
	// Quits after 20 samples of a 60-second match.
	li := make([]int16, 20)
	for i := range li {
		li[i] = 1
	}
	res := occupancyResult(walkTrack("p", 13, li, nil, nil), 60000)

	got := totalsOf(t, res)
	// 19 gaps of 13 ms, plus one 13 ms hold for the final sample.
	if want := int32(20 * 13); got["A"] != want {
		t.Errorf("A = %d ms, want %d — a quitter must not be credited past their last sample", got["A"], want)
	}
	if got["A"] > 1000 {
		t.Errorf("A = %d ms: the departed player's last loc ran on toward match end (%d ms)", got["A"], 60000)
	}
}

// The dead period is excluded even though the corpse keeps producing samples
// in a DIFFERENT loc — the gib-head case, where the player entity itself is
// thrown across the map (ktx/src/player.c:1070 ThrowHead).
func TestLocGraphOccupancyExcludesDeadPeriod(t *testing.T) {
	// Alive in A for 10 samples, dead in B for 10, alive again in A.
	li := []int16{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 1, 1, 1, 1, 1}
	step := int32(13)
	p := walkTrack("p", step, li, []int32{10 * step}, []int32{20 * step})
	res := occupancyResult(p, step*int32(len(li)-1))
	deriveAliveIntervals(res.Streams)

	got := totalsOf(t, res)
	if got["B"] != 0 {
		t.Errorf("B (where the body lay) = %d ms, want 0 — dead time is not presence", got["B"])
	}
	if got["A"] == 0 {
		t.Error("A = 0 ms: the alive periods were dropped too, so the gate is over-broad")
	}
}

// A hole in the position track is not presence. This is the regression test
// for the sharpest defect the v64 work introduced and then fixed: the deleted
// 50 ms clamp had been doing two jobs, and only one of them was the corpse
// bug. Its other job was bounding sample-and-hold across a genuine hole.
//
// Holes are not hypothetical. On a POV (client) recording only players inside
// the recorder's PVS get svc_playerinfo, so every other player's track is full
// of them — measured on demo-test-data/mvd/special-cases/dag_caps_e1m2.mvd,
// the recorder had 76k samples with a 152 ms worst gap while the other seven
// had ~15k samples with gaps up to 73 SECONDS. Holding across those credited
// ~92% of a player's loc time to wherever they stood when they left view
// (ff.exile: 924 s of 1,005 s invented).
func TestLocGraphOccupancyDoesNotCreditTrackHoles(t *testing.T) {
	// Ten samples in A, a 40-second hole, ten samples in B.
	li := make([]int16, 0, 20)
	ts := make([]int32, 0, 20)
	for i := int32(0); i < 10; i++ {
		li = append(li, 1)
		ts = append(ts, i*13)
	}
	for i := int32(0); i < 10; i++ {
		li = append(li, 2)
		ts = append(ts, 40000+i*13)
	}
	pt := &result.PositionTrack{T: ts}
	for range ts {
		pt.X = append(pt.X, 0)
		pt.Y = append(pt.Y, 0)
		pt.Z = append(pt.Z, 0)
	}
	pt.Li = li
	p := result.PlayerStream{Name: "p", Team: "red", Position: pt}
	res := occupancyResult(p, 60000)
	deriveAliveIntervals(res.Streams)

	got := totalsOf(t, res)
	// A holds ten 13 ms samples; the hole after the last of them must expire,
	// not run the 40 s to the next sample.
	if got["A"] > 10*13+result.SampleStaleCapMs {
		t.Errorf("A = %d ms: the 40-second hole after the last A sample was credited as presence", got["A"])
	}
	if got["A"] == 0 {
		t.Error("A = 0 ms: the ten real samples were dropped too, so the bound is over-broad")
	}
	if total := got["A"] + got["B"]; total > 2*(10*13+result.SampleStaleCapMs) {
		t.Errorf("total %d ms across a track carrying ~260 ms of samples", total)
	}
}

// The EDGE gate, exercised by a corpse that TRAVELS.
//
// The other corpse fixtures park the body in one loc, where the pre-existing
// spawn/death cursor reset already suppresses the two edges crossing the death
// and respawn instants — so they cannot see whether the edge gate does
// anything. A gib is different: ktx/src/player.c:1070 ThrowHead makes the
// player entity itself a MOVETYPE_BOUNCE head, so the body crosses locs while
// dead and every crossing is a candidate edge. The release notes claim those
// edges disappear; this is what pins the claim.
func TestLocGraphEdgesExcludeATravellingCorpse(t *testing.T) {
	step := int32(13)
	// Alive in A, then dead while the head bounces B,C,B,C, then alive in A.
	li := []int16{1, 1, 1, 1, 2, 3, 2, 3, 2, 3, 1, 1, 1}
	p := walkTrack("p", step, li, []int32{4 * step}, []int32{10 * step})
	res := occupancyResult(p, step*int32(len(li)-1))
	// The fixture's loc table only has A and B; add C.
	res.TimelineAnalysis.LocTable = []string{"", "A", "B", "C"}
	res.TimelineAnalysis.LocationData = []MapLocation{{Name: "A"}, {Name: "B"}, {Name: "C"}}
	deriveAliveIntervals(res.Streams)

	g := BuildLocGraph(res)
	if g == nil {
		t.Fatal("BuildLocGraph returned nil")
	}
	for _, e := range g.Edges {
		if e.From == "B" || e.To == "B" || e.From == "C" || e.To == "C" {
			t.Errorf("edge %s -> %s (%s, total %d) was walked by a corpse, not a player",
				e.From, e.To, e.Kind, e.Total)
		}
	}
	for _, n := range g.Locs {
		if n.Name == "B" || n.Name == "C" {
			t.Errorf("loc %s credited %d ms to a bouncing gib head", n.Name, n.Total)
		}
	}
}

// The teleport threshold is scaled by the REAL inter-sample delta, because the
// recording cadence is server-configured. A fixed bound misclassifies in both
// directions once the cadence is not the assumed one — and the pre-existing
// teleport test samples at exactly 50 ms, the single cadence at which the old
// fixed bound and the new scaled one are identical, so it cannot see this.
func TestLocGraphTeleportThresholdScalesWithCadence(t *testing.T) {
	// A jump of 300 units between two adjacent samples.
	//   at 13 ms: bound = 0.013*2500 =  32.5 -> teleport
	//   at 39 ms: bound = 0.039*2500 =  97.5 -> teleport
	//   at 200 ms: bound = 0.200*2500 = 500  -> normal movement (2000 ups is
	//              plausible over 200 ms), which a fixed 125-unit bound would
	//              have called a teleport.
	build := func(step int32, jump float32) *Result {
		pt := &result.PositionTrack{
			T:  []int32{0, step, 2 * step},
			Li: []int16{1, 2, 2},
			X:  []float32{0, jump, jump},
			Y:  []float32{0, 0, 0},
			Z:  []float32{0, 0, 0},
		}
		p := result.PlayerStream{Name: "p", Team: "red", Position: pt}
		return occupancyResult(p, 60000)
	}
	kindOf := func(res *Result) string {
		g := BuildLocGraph(res)
		for _, e := range g.Edges {
			if e.From == "A" && e.To == "B" {
				return e.Kind
			}
		}
		return "<no edge>"
	}

	if got := kindOf(build(13, 300)); got != "teleport" {
		t.Errorf("300 units in 13 ms: kind = %q, want teleport (bound 32.5)", got)
	}
	if got := kindOf(build(200, 300)); got != "normal" {
		t.Errorf("300 units in 200 ms: kind = %q, want normal — a fixed 125-unit bound would misread plausible movement as a teleport", got)
	}
	// Across an abnormally long gap no displacement claim is meaningful, so the
	// transition must not be invented as a teleport.
	if got := kindOf(build(locgraphTeleportMaxGapMs+100, 100000)); got != "normal" {
		t.Errorf("huge jump across a >%dms stall: kind = %q, want normal (unclassifiable)",
			locgraphTeleportMaxGapMs, got)
	}
}
