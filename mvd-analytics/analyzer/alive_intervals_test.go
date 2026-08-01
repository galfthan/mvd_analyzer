package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// PlayerStream.Alive is the canonical STORED liveness the occupancy walkers
// gate on, so these tests pin it to the markers it is derived from, to the
// observed-presence clip that keeps it honest, and to the three-state
// measuredness contract documented on the field.

func aliveOf(t *testing.T, spawns, deaths []int32, matchEnd int32, pos []int32) []result.Interval {
	t.Helper()
	s := &result.Streams{
		Global:  result.GlobalStream{MatchEnd: matchEnd},
		Players: []result.PlayerStream{{Name: "p", Spawns: spawns, Deaths: deaths}},
	}
	if pos != nil {
		s.Players[0].Position = &result.PositionTrack{T: pos}
	}
	deriveAliveIntervals(s)
	return s.Players[0].Alive
}

func eqIntervals(a, b []result.Interval) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The stored field must never disagree with the markers it came from — it is
// redundant storage, and redundant storage drifts unless something pins it.
// These cases carry no position track, so the observed-presence clip is a
// no-op and the comparison is against the raw marker derivation;
// TestAliveIntervalsSplitAtTrackHoles covers the clip.
func TestAliveIntervalsMatchMarkers(t *testing.T) {
	cases := []struct {
		name     string
		spawns   []int32
		deaths   []int32
		matchEnd int32
	}{
		{"no deaths", nil, nil, 60000},
		{"one death, one respawn", []int32{12000}, []int32{10000}, 60000},
		{"death with no respawn (dtTELE2 deflection)", nil, []int32{10000}, 60000},
		{"several lives", []int32{5000, 20000, 41000}, []int32{3000, 18000, 40000}, 60000},
		{"death and respawn on the same ms", []int32{10000}, []int32{10000}, 60000},
		{"markers outside the window are ignored", []int32{-500, 99000}, []int32{-400, 98000}, 60000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := aliveOf(t, tc.spawns, tc.deaths, tc.matchEnd, nil)
			want := aliveIntervals(tc.spawns, tc.deaths, tc.matchEnd)
			if want == nil {
				want = []result.Interval{}
			}
			if !eqIntervals(got, want) {
				t.Fatalf("Alive drifted from its markers:\n got  %v\n want %v", got, want)
			}
		})
	}
}

// A player who never died is ONE full-match interval, never an empty list.
// This is what makes "absent" unambiguous — absence can never be read as
// "alive throughout".
func TestAliveIntervalsNeverDiedIsOneFullInterval(t *testing.T) {
	got := aliveOf(t, nil, nil, 60000, nil)
	want := []result.Interval{{Start: 0, End: 60000}}
	if !eqIntervals(got, want) {
		t.Fatalf("never-died player: got %v, want %v", got, want)
	}
}

// The three measuredness states are distinct and must stay distinct.
func TestAliveIntervalsMeasurednessStates(t *testing.T) {
	// Measured, but never alive: died at the origin and never respawned.
	neverAlive := aliveOf(t, nil, []int32{0}, 60000, nil)
	if neverAlive == nil {
		t.Fatal("never-alive player: got nil (reads as UNMEASURABLE), want an empty non-nil list")
	}
	if len(neverAlive) != 0 {
		t.Fatalf("never-alive player: got %v, want empty", neverAlive)
	}

	// Unmeasurable: no match window and no samples at all.
	unmeasurable := aliveOf(t, nil, nil, 0, nil)
	if unmeasurable != nil {
		t.Fatalf("no window and no samples: got %v, want nil (unmeasurable)", unmeasurable)
	}
}

// On the demo-timebase path (no match start detected, so MatchEnd is unset)
// liveness is still measurable from the player's own last observed sample —
// otherwise every such demo would report "not measurable" despite having a
// full position track.
func TestAliveIntervalsDemoTimebaseFallback(t *testing.T) {
	// A dense track (13 ms cadence) out to 30 s, so the window comes from the
	// track rather than from an absent MatchEnd and nothing is split.
	var dense []int32
	for t0 := int32(0); t0 <= 30000; t0 += 13 {
		dense = append(dense, t0)
	}
	got := aliveOf(t, []int32{12000}, []int32{10000}, 0, dense)
	// Ends at the final sample (29991 = the last multiple of 13 <= 30000):
	// the alive window itself is the last observed timestamp on this path.
	want := []result.Interval{{Start: 0, End: 10000}, {Start: 12000, End: 29991}}
	if !eqIntervals(got, want) {
		t.Fatalf("demo-timebase fallback: got %v, want %v", got, want)
	}

	// Markers alone are enough when no position track was built.
	got = aliveOf(t, []int32{12000}, []int32{10000}, 0, nil)
	want = []result.Interval{{Start: 0, End: 10000}, {Start: 12000, End: 12000}}
	if len(got) == 0 || got[0] != want[0] {
		t.Fatalf("marker-only fallback: got %v, want first interval %v", got, want[0])
	}
}

// A hole in the position track means the player was not OBSERVED — not that
// they died — so it must NOT split a life. On a POV (client) recording only
// players inside the recorder's PVS get svc_playerinfo, so tracks are full of
// multi-second holes (measured up to 73 s on
// demo-test-data/mvd/special-cases/dag_caps_e1m2.mvd); splitting there would
// invent lives nobody lived, and anything enumerating lives or attributing
// per-life stats would over-count.
//
// Not crediting unobserved TIME is a different question, answered a layer
// down by result.SampleStaleCapMs in the occupancy walkers
// (TestLocGraphOccupancyDoesNotCreditTrackHoles). Keeping the two separate is
// what lets Alive be a truthful life list and the walkers still refuse to
// credit presence they never saw.
func TestAliveIntervalsSpanTrackHolesWithoutSplitting(t *testing.T) {
	// Samples at 0,13,26 then a 40-second hole, then 40000,40013,40026 —
	// with no death anywhere.
	track := []int32{0, 13, 26, 40000, 40013, 40026}
	got := aliveOf(t, nil, nil, 60000, track)

	if len(got) != 1 {
		t.Fatalf("got %v, want ONE life: no death occurred, only an observation gap", got)
	}
	if got[0].Start != 0 {
		t.Errorf("life starts at %d, want 0", got[0].Start)
	}
	// Clipped to the track's end, not to match end.
	if got[0].End > 40026+result.SampleStaleCapMs || got[0].End <= 40026 {
		t.Errorf("life ends at %d, want just past the final sample (40026)", got[0].End)
	}
}

// A death SPLITS a life, even when the respawn lands on the same millisecond
// and no dead time elapses. Alive is documented and consumed as the player's
// lives, so a zero-width dead gap still has to leave two intervals — the
// interval algebra's usual "touching means overlapping" merge would erase the
// death, and any life analyzer built on this field would silently lose one.
func TestAliveIntervalsKeepLifeBoundaryOnSameMsRespawn(t *testing.T) {
	var track []int32
	for ms := int32(0); ms <= 30000; ms += 13 {
		track = append(track, ms)
	}
	got := aliveOf(t, []int32{10000}, []int32{10000}, 30000, track)

	if len(got) != 2 {
		t.Fatalf("got %v, want two lives — the death at 10000 must survive its same-ms respawn", got)
	}
	if got[0].End != 10000 || got[1].Start != 10000 {
		t.Errorf("got %v, want the boundary at exactly 10000", got)
	}
	// Touching, so no dead time is invented: the player really was alive
	// throughout, they just started a new life.
	if got[0].End != got[1].Start {
		t.Errorf("got %v: an instant respawn must not manufacture a dead gap", got)
	}
}

// A death ends a life at the instant it happens: the corpse (or, on a gib, the
// bouncing head) keeps broadcasting position, so anything reading Alive must
// see the dead period even though samples continue through it.
func TestAliveIntervalsExcludeTheCorpsePeriod(t *testing.T) {
	got := aliveOf(t, []int32{11000}, []int32{10000}, 60000, nil)
	want := []result.Interval{{Start: 0, End: 10000}, {Start: 11000, End: 60000}}
	if !eqIntervals(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for _, iv := range got {
		if 10500 >= iv.Start && 10500 < iv.End {
			t.Fatalf("t=10500 is inside the dead period but Alive reports it live: %v", got)
		}
	}
}
