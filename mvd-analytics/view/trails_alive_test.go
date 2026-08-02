package view

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// A loc residence is DWELL — presence — so /loc-trails is alive-gated exactly
// like loc-graph node time. Without this the corpse travels that loc-graph
// excludes would still surface here as dwell, and the two endpoints would
// answer the same question differently on the same demo.
func TestLocTrailsExcludeDeadDwell(t *testing.T) {
	// In "mid" throughout, but dead from 10s to 20s.
	pt := &result.PositionTrack{}
	for ms := int32(0); ms <= 30000; ms += 13 {
		pt.T = append(pt.T, ms)
		pt.X = append(pt.X, 0)
		pt.Y = append(pt.Y, 0)
		pt.Z = append(pt.Z, 0)
		pt.Li = append(pt.Li, 1)
	}
	p := result.PlayerStream{
		Name:     "p",
		Position: pt,
		Loc:      []result.ChangeI16{{T: 0, V: 1}},
		Alive:    []result.Interval{{Start: 0, End: 10000}, {Start: 20000, End: 30000}},
	}
	res := &result.Result{
		Streams: &result.Streams{
			Global:  result.GlobalStream{MatchStart: 0, MatchEnd: 30000},
			Players: []result.PlayerStream{p},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{LocTable: []string{"", "mid"}},
	}

	got, err := LocTrails(res, LocTrailsOptions{})
	if err != nil {
		t.Fatalf("LocTrails: %v", err)
	}
	if len(got.Players) != 1 {
		t.Fatalf("got %d players, want 1", len(got.Players))
	}
	seq := got.Players[0].Sequence
	if len(seq) != 2 {
		t.Fatalf("got %d residences %v, want 2 (split by the dead period)", len(seq), seq)
	}
	var dwell int32
	for _, e := range seq {
		dwell += e.End - e.Start
		if e.Start < 10000 && e.End > 10000 {
			t.Errorf("residence %v straddles the death at 10000 ms", e)
		}
	}
	// 20 s of life, not the 30 s the samples cover.
	if dwell > 21000 {
		t.Errorf("total dwell %d ms; the 10 s dead period was counted", dwell)
	}
}

// The alive gate runs BEFORE the short-dwell merge, and the merge extends an
// entry's End over whatever follows it — so unless the merge refuses to cross a
// gap, it hands the dead span straight back as dwell. Both callers that hit
// this are the DEFAULT ones: mvd-mcp injects minDwellMs=250 on every getLocTrails
// call, so before the fix a 30 s residence around a 10 s death came back whole.
func TestLocTrailsDeadGapSurvivesMinDwell(t *testing.T) {
	const minDwell = 250 // the MCP default, not an exotic setting

	// Probe 1: the death splits one loc into two residences of the SAME name,
	// which the identical-loc coalesce welded back together regardless of gap.
	t.Run("same-loc split stays split", func(t *testing.T) {
		res := trailResult(
			[]result.ChangeI16{{T: 0, V: 1}},
			[]result.Interval{{Start: 0, End: 10000}, {Start: 20000, End: 30000}},
			30000, "", "mid")

		seq := trailSeq(t, res, minDwell)
		want := []TrailEntry{{Start: 0, End: 10000, Loc: "mid"}, {Start: 20000, End: 30000, Loc: "mid"}}
		assertTrail(t, seq, want)
	})

	// Probe 2: a post-respawn residence shorter than minDwell was folded into
	// the PRE-DEATH entry, whose End then stretched across the dead gap — the
	// dead span AND the spawnroom both credited to the pre-death loc.
	t.Run("short post-respawn residence is not folded across the gap", func(t *testing.T) {
		res := trailResult(
			[]result.ChangeI16{{T: 0, V: 1}, {T: 20000, V: 2}, {T: 20200, V: 1}},
			[]result.Interval{{Start: 0, End: 10000}, {Start: 20000, End: 30000}},
			30000, "", "ra", "spawnroom")

		seq := trailSeq(t, res, minDwell)
		want := []TrailEntry{
			{Start: 0, End: 10000, Loc: "ra"},
			{Start: 20000, End: 20200, Loc: "spawnroom"},
			{Start: 20200, End: 30000, Loc: "ra"},
		}
		assertTrail(t, seq, want)
	})
}

// A residence must stop at the player's own end-of-track, not at match end:
// sample-and-hold has no staleness bound of its own, so a player who leaves
// early would otherwise dwell in their last loc for the rest of the match.
func TestLocTrailsBoundedByTrackHoldEnd(t *testing.T) {
	pt := &result.PositionTrack{}
	for i := int32(0); i <= 100; i++ {
		pt.T = append(pt.T, i*13)
		pt.X = append(pt.X, 0)
		pt.Y = append(pt.Y, 0)
		pt.Z = append(pt.Z, 0)
		pt.Li = append(pt.Li, 1)
	}
	last := pt.T[len(pt.T)-1]

	// The match runs 45× longer than the recording of this player.
	res := trailResult([]result.ChangeI16{{T: 0, V: 1}}, nil, 60000, "", "mid")
	res.Streams.Players[0].Position = pt

	seq := trailSeq(t, res, 0)
	if len(seq) != 1 {
		t.Fatalf("got %d residences %+v, want 1", len(seq), seq)
	}
	// One median cadence (13 ms) past the final sample, capped at
	// result.SampleStaleCapMs — never the match window.
	if want := result.TrackHoldEnd(pt.T); seq[0].End != want {
		t.Errorf("residence ends at %d ms, want %d (last sample %d + median cadence); match end is %d",
			seq[0].End, want, last, res.Streams.Global.MatchEnd)
	}
}

// Empty and nil Alive are different measurements, and the trail has to tell
// them apart. Empty means liveness WAS measured and the player was never alive
// in the window, so there is no dwell to report; nil means it was not
// measurable at all, and the honest response to "unknown" is to degrade rather
// than to blank a demo's whole trail.
func TestLocTrailsAliveGateDistinguishesEmptyFromUnmeasured(t *testing.T) {
	cases := []struct {
		name     string
		alive    []result.Interval
		wantSeqs int
	}{
		{"measured, never alive", []result.Interval{}, 0},
		{"liveness unmeasured", nil, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := trailResult([]result.ChangeI16{{T: 0, V: 1}}, tc.alive, 1000, "", "mid")
			res.Streams.Players[0].Position = &result.PositionTrack{
				T: []int32{0, 13, 26}, X: []float32{0, 0, 0}, Y: []float32{0, 0, 0},
				Z: []float32{0, 0, 0}, Li: []int16{1, 1, 1},
			}
			got, err := LocTrails(res, LocTrailsOptions{})
			if err != nil {
				t.Fatalf("LocTrails: %v", err)
			}
			if len(got.Players) != tc.wantSeqs {
				t.Fatalf("got %d players with residences (%+v), want %d", len(got.Players), got.Players, tc.wantSeqs)
			}
		})
	}
}

// trailResult builds a one-player Result carrying just what LocTrails reads:
// the loc-change stream, the canonical lives, and the match window.
func trailResult(loc []result.ChangeI16, alive []result.Interval, matchEnd int32, locTable ...string) *result.Result {
	return &result.Result{
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: matchEnd},
			Players: []result.PlayerStream{{
				Name: "p", Loc: loc, Alive: alive,
			}},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{LocTable: locTable},
	}
}

func trailSeq(t *testing.T, res *result.Result, minDwell int) []TrailEntry {
	t.Helper()
	got, err := LocTrails(res, LocTrailsOptions{MinDwellMs: minDwell})
	if err != nil {
		t.Fatalf("LocTrails: %v", err)
	}
	if len(got.Players) != 1 {
		t.Fatalf("got %d players, want 1", len(got.Players))
	}
	return got.Players[0].Sequence
}

func assertTrail(t *testing.T, got, want []TrailEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d residences %+v, want %d %+v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i].Start != want[i].Start || got[i].End != want[i].End || got[i].Loc != want[i].Loc {
			t.Errorf("residence %d = {%d,%d,%s}, want {%d,%d,%s}",
				i, got[i].Start, got[i].End, got[i].Loc, want[i].Start, want[i].End, want[i].Loc)
		}
	}
}
