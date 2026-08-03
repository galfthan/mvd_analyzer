package view

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// walkRegionExact evaluates each interval at its LEFT endpoint, so a player
// whose track ends mid-match passes any "is t past their last sample" guard AT
// that sample and is credited the whole interval that starts there. When the
// recording ends before the match window does — a demo cut at intermission is
// the ordinary case — that interval runs to match end.
//
// Measured before the fix: 60,000 ms of presence for a player who left after
// 2,000 ms. The bound is result.TrackHoldEnd, added both as a rejection in
// sampleAt and as an event boundary so the straddling interval is truncated
// rather than merely dropped.
func TestRegionControlBoundsDepartedPlayer(t *testing.T) {
	// Two shapes, because they fail differently. When the OTHER player keeps
	// producing boundaries the over-credit is bounded by their sample spacing;
	// when the whole recording stops (a demo cut before the match window ends,
	// the ordinary case) the next boundary is gridEnd and the departed player
	// is credited the entire remaining match.
	for _, tc := range []struct {
		name   string
		nOther int
	}{
		{"another player keeps playing", 601},
		{"the recording ends early", 21},
	} {
		t.Run(tc.name, func(t *testing.T) { checkDepartedPlayerBound(t, tc.nOther) })
	}
}

func checkDepartedPlayerBound(t *testing.T, nOther int) {
	t.Helper()
	// p1 quits at 2000ms. p2 plays to 60000ms and keeps generating boundaries.
	mk := func(name, team string, n int, step int32) result.PlayerStream {
		pt := &result.PositionTrack{}
		for i := 0; i < n; i++ {
			pt.T = append(pt.T, int32(i)*step)
			pt.X = append(pt.X, 0)
			pt.Y = append(pt.Y, 0)
			pt.Z = append(pt.Z, 0)
			pt.Li = append(pt.Li, 1)
		}
		return result.PlayerStream{Name: name, Team: team, Position: pt,
			Alive: []result.Interval{{Start: 0, End: 60000}}}
	}
	res := &result.Result{
		Streams: &result.Streams{
			Global:  result.GlobalStream{MatchStart: 0, MatchEnd: 60000},
			Players: []result.PlayerStream{mk("p1", "red", 21, 100), mk("p2", "blue", nOther, 100)},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{
			LocTable: []string{"", "mid"},
			RegionControl: &result.RegionControlResult{
				Regions: []result.ControlRegion{{Name: "centre", Locs: []string{"mid"}}},
				TeamA:   "red", TeamB: "blue",
			},
		},
		Match: &result.MatchResult{Players: []result.PlayerStat{
			{Name: "p1", Team: "red"}, {Name: "p2", Team: "blue"}}},
	}
	rc, err := RegionControl(res, RegionControlOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st := rc.Stats["centre"]
	p1 := st.ByPlayer["p1"]
	t.Logf("p1 left at 2000 ms; credited armed=%d unarmed=%d (total %d ms)",
		p1.Armed, p1.Unarmed, p1.Armed+p1.Unarmed)
	if got := p1.Armed + p1.Unarmed; got > 3000 {
		t.Errorf("phantom presence: p1 left at 2000 ms but was credited %d ms of a 60 s match", got)
	}
	if p1.Armed+p1.Unarmed == 0 {
		t.Error("p1 credited nothing at all — the bound is over-broad")
	}
}

// A position sample's evidence expires result.SampleStaleCapMs after it: past
// that the player's location is unknown and the time belongs to nobody. On a
// POV recording, where only players inside the recorder's PVS are written, the
// holes run to tens of seconds and crediting them in full hands a player most
// of their loc time for standing still off-camera.
//
// The fixture puts the hole INSIDE a mapped region, which is what makes it
// bite: a hole in an unmapped loc is discarded by the region lookup anyway, so
// it cannot tell a working expiry from a missing one.
func TestRegionControlDoesNotCreditStaleSamples(t *testing.T) {
	const (
		preHole  = int32(0)    // last sample before the hole
		postHole = int32(1000) // first sample after it
		lastSmpl = int32(1013)
	)
	res := &result.Result{
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 60000},
			Players: []result.PlayerStream{{
				Name: "p1", Team: "red",
				Position: &result.PositionTrack{
					T:  []int32{preHole, postHole, lastSmpl},
					Li: []int16{1, 1, 1},
				},
				Alive: []result.Interval{{Start: 0, End: 60000}},
			}},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{
			LocTable: []string{"", "mid"},
			RegionControl: &result.RegionControlResult{
				Regions: []result.ControlRegion{{Name: "centre", Locs: []string{"mid"}}},
				TeamA:   "red", TeamB: "blue",
			},
		},
		Match: &result.MatchResult{Players: []result.PlayerStat{{Name: "p1", Team: "red"}}},
	}
	staleCap := result.SampleStaleCapMs

	// The credited window of ONE sample is exactly [sample, sample+cap): the
	// expiry instant itself is already stale. Asserting the boundary to the
	// millisecond is what separates a working cap from an off-by-one that
	// credits the whole remaining hole from the boundary onward.
	sub, err := RegionControl(res, RegionControlOptions{StartTime: preHole, EndTime: postHole})
	if err != nil {
		t.Fatal(err)
	}
	if got := presenceMs(sub.Stats["centre"].ByPlayer["p1"]); got != staleCap {
		t.Errorf("over [%d,%d) with one sample at %d: credited %d ms, want exactly %d — the sample is evidence for [%d,%d) only",
			preHole, postHole, preHole, got, staleCap, preHole, preHole+staleCap)
	}

	// Over the whole match: cap for the pre-hole sample, the 13 ms between the
	// two post-hole samples, and one capped hold past the final sample. The
	// 750 ms remainder of the hole is credited to nobody.
	full, err := RegionControl(res, RegionControlOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := staleCap + (lastSmpl - postHole) + staleCap
	if got := presenceMs(full.Stats["centre"].ByPlayer["p1"]); got != want {
		t.Errorf("credited %d ms, want %d; the %d ms hole between samples %d and %d must not be credited",
			got, want, postHole-preHole-staleCap, preHole, postHole)
	}
}

func presenceMs(ps result.RegionPlayerStats) int32 {
	return int32(ps.Armed + ps.Unarmed)
}
