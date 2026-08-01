package result

import "testing"

// TrackHoldEnd and SampleStaleBoundaries are the shared sample-and-hold policy
// every occupancy walk depends on, so they are pinned here directly rather
// than only through their callers. Both encode judgements that a future reader
// might reasonably "simplify" — the median-gap hold and the stale expiry —
// and both have a measured failure they exist to prevent.

func TestTrackHoldEndUsesMedianNotLastGap(t *testing.T) {
	// A steady 13 ms cadence whose LAST gap is a 5-second stall. Holding for
	// the last gap would award the biggest tail exactly where the evidence is
	// weakest; the median ignores the stall.
	track := []int32{0, 13, 26, 39, 52, 65, 5065}
	got := TrackHoldEnd(track)
	if want := int32(5065 + 13); got != want {
		t.Errorf("TrackHoldEnd = %d, want %d (last sample + MEDIAN gap 13, not the 5000 ms trailing stall)", got, want)
	}
}

func TestTrackHoldEndCadenceAdaptive(t *testing.T) {
	// The recorded cadence is server-configured (sv_demofps), so the hold has
	// to be read off the track rather than assumed. Both clusters measured on
	// the golden corpus are covered.
	cases := []struct {
		name string
		step int32
	}{
		{"full-tick server (~13 ms)", 13},
		{"sv_demofps default (~39 ms)", 39},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var track []int32
			for i := int32(0); i < 20; i++ {
				track = append(track, i*tc.step)
			}
			last := track[len(track)-1]
			if got, want := TrackHoldEnd(track), last+tc.step; got != want {
				t.Errorf("TrackHoldEnd = %d, want %d (one cadence past the last sample)", got, want)
			}
		})
	}
}

func TestTrackHoldEndCapsTheHold(t *testing.T) {
	// Every gap is a stall, so the median is one too. The cap stops it being
	// projected forward as if it were a cadence.
	track := []int32{0, 100000, 200000}
	if got, want := TrackHoldEnd(track), int32(200000)+SampleStaleCapMs; got != want {
		t.Errorf("TrackHoldEnd = %d, want %d (hold capped at SampleStaleCapMs)", got, want)
	}
}

func TestTrackHoldEndDegenerateTracks(t *testing.T) {
	// A single sample carries no cadence, so it holds for nothing. Crediting a
	// guessed duration would be worse than crediting none.
	if got := TrackHoldEnd([]int32{500}); got != 500 {
		t.Errorf("single-sample track: TrackHoldEnd = %d, want 500 (no hold)", got)
	}
	// Duplicate timestamps yield no positive gap; same reasoning.
	if got := TrackHoldEnd([]int32{500, 500}); got != 500 {
		t.Errorf("duplicate-timestamp track: TrackHoldEnd = %d, want 500", got)
	}
}

// A sample's evidence expires. Without these boundaries a walk credits a whole
// hole to whatever loc the player was last seen in — measured at ~92% of a
// player's loc time on a POV recording, where only players inside the
// recorder's PVS get svc_playerinfo.
func TestSampleStaleBoundaries(t *testing.T) {
	// A dense server-recorded track has no hole, so no boundary is emitted and
	// the common path allocates nothing.
	var dense []int32
	for i := int32(0); i < 50; i++ {
		dense = append(dense, i*13)
	}
	if got := SampleStaleBoundaries(dense); got != nil {
		t.Errorf("dense track produced expiry boundaries %v; want none", got)
	}

	// A POV-shaped track: two runs separated by a 40-second hole.
	track := []int32{0, 13, 26, 40000, 40013}
	got := SampleStaleBoundaries(track)
	want := []int32{26 + SampleStaleCapMs}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %v, want %v (evidence from the last pre-hole sample expires)", got, want)
	}

	// The boundary must land strictly inside the hole, or truncation is a no-op.
	if got[0] <= 26 || got[0] >= 40000 {
		t.Errorf("expiry boundary %d is not inside the hole (26, 40000)", got[0])
	}
}

// A gap exactly at the cap is not yet stale; one millisecond more is.
func TestSampleStaleBoundariesThreshold(t *testing.T) {
	atCap := []int32{0, SampleStaleCapMs}
	if got := SampleStaleBoundaries(atCap); got != nil {
		t.Errorf("gap of exactly SampleStaleCapMs produced %v; want none", got)
	}
	overCap := []int32{0, SampleStaleCapMs + 1}
	if got := SampleStaleBoundaries(overCap); len(got) != 1 {
		t.Errorf("gap of SampleStaleCapMs+1 produced %v; want one boundary", got)
	}
}
