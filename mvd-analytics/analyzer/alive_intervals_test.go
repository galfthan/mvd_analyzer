package analyzer

import (
	"os"
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
// TestAliveIntervalsSpanTrackHolesWithoutSplitting covers the clip.
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

// The pipeline must ALWAYS populate Alive for a player with a position track.
//
// Every consumer — loc-graph, region-control, loc-trails, LOS, aim — degrades
// to "always alive" when the list is nil, which is the right answer for
// "liveness was not measurable" but the wrong one for "somebody moved
// deriveAliveIntervals and nothing noticed". That failure is silent by
// construction: numbers get quietly larger and no test that builds its own
// fixtures would see it. Two aim tests were caught by exactly this while the
// LOS/aim migration landed — their hand-built Results had no Alive, so the
// dead-enemy exclusion stopped applying.
//
// Cache-gated like the real-demo reconcile: skips when testdata/cache is
// empty, so an offline `make test` stays green.
func TestPipelineAlwaysPopulatesAlive(t *testing.T) {
	const demo = "../testdata/cache/211805.mvd.gz"
	if _, err := os.Stat(demo); err != nil {
		t.Skip("no cached demo — run TestGoldenCorpus once to populate testdata/cache")
	}
	res, err := NewDefaultRegistry().Analyze(demo)
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if res.Streams == nil || len(res.Streams.Players) == 0 {
		t.Fatal("no player streams")
	}
	checked := 0
	for i := range res.Streams.Players {
		p := &res.Streams.Players[i]
		if p.Position == nil || len(p.Position.T) == 0 {
			continue
		}
		checked++
		if p.Alive == nil {
			t.Errorf("%s has a position track but Alive is nil — every consumer "+
				"silently degrades to always-alive", p.Name)
		}
	}
	if checked == 0 {
		t.Fatal("no player had a position track — the assertion proves nothing")
	}
}

// Every death must land inside (or at the closing boundary of) some life. That
// is the defining property of a life list: a death is what ENDS a life, so a
// death outside every interval says the player died while not alive.
//
// The presence clip broke it whenever the position track ran out before the
// markers did. On a POV recording a player leaves the recorder's PVS and their
// svc_playerinfo stops, but their deaths keep arriving (an obituary is global),
// so the track ends minutes before the last death — and the clip, reading only
// the track, cut the life short and dropped every life after it. Downstream,
// the lives view labelled the truncated life "leftGame".
func TestAliveIntervalsCoverEveryDeath(t *testing.T) {
	// Deaths are half-open interval ENDS: a death at exactly life.End closes
	// that life, so the containment test is Start < death <= End.
	assertDeathsCovered := func(t *testing.T, alive []result.Interval, deaths []int32) {
		t.Helper()
		for _, d := range deaths {
			covered := false
			for _, iv := range alive {
				if iv.Start < d && d <= iv.End {
					covered = true
					break
				}
			}
			if !covered {
				t.Errorf("death at %d falls inside no life: %v", d, alive)
			}
		}
	}

	// The probe: the recorder loses the player 10 s in (samples every 13 ms to
	// t=10000), but the obituary-fused death at 63000 and the respawn at 63100
	// are still recorded.
	t.Run("track ends before the markers do", func(t *testing.T) {
		var track []int32
		for ms := int32(0); ms <= 10000; ms += 13 {
			track = append(track, ms)
		}
		deaths := []int32{63000}
		got := aliveOf(t, []int32{63100}, deaths, 120000, track)

		assertDeathsCovered(t, got, deaths)
		// Covering the death is not enough: the life the respawn STARTS has to
		// survive too. Clipping the presence bound at that trailing spawn made
		// it [63100, 63100) — zero width — and clipToPresence dropped it, so
		// everything the player did after respawning was attributed to a life
		// the same response says ended at 63000.
		want := []result.Interval{{Start: 0, End: 63000}, {Start: 63100, End: 120000}}
		if !eqIntervals(got, want) {
			t.Fatalf("got %v, want %v — the first life runs to its death at 63000 "+
				"(not to the end of the position track) and the life the respawn "+
				"at 63100 starts must exist, running to the window end", got, want)
		}
	})

	// The control for the asymmetry: when the trailing marker is a DEATH, the
	// high clip still lands exactly on it. A death both proves existence and
	// ends the life, so nothing after it is claimed — unlike a trailing spawn,
	// it must NOT widen the bound to the window end.
	t.Run("a trailing death clips exactly there", func(t *testing.T) {
		var track []int32
		for ms := int32(0); ms <= 10000; ms += 13 {
			track = append(track, ms)
		}
		deaths := []int32{20000, 63000}
		got := aliveOf(t, []int32{30000}, deaths, 120000, track)

		assertDeathsCovered(t, got, deaths)
		want := []result.Interval{{Start: 0, End: 20000}, {Start: 30000, End: 63000}}
		if !eqIntervals(got, want) {
			t.Fatalf("got %v, want %v — the last life ends at the trailing death, "+
				"never at the window end", got, want)
		}
	})

	// The mirror image: the player is only observed from 60 s on, but died at
	// 30 s. A DEATH before the track proves they were in the game (and, by the
	// canonical rule, alive) before it began, so the low clip has nothing to
	// stand on.
	t.Run("a death precedes the track", func(t *testing.T) {
		var track []int32
		for ms := int32(60000); ms <= 70000; ms += 13 {
			track = append(track, ms)
		}
		deaths := []int32{30000}
		got := aliveOf(t, []int32{30100}, deaths, 120000, track)

		assertDeathsCovered(t, got, deaths)
	})

	// The clip still does its job: a player who joins at 60 s, with no marker
	// before their track, does not claim the first minute.
	t.Run("a late joiner is still clipped", func(t *testing.T) {
		var track []int32
		for ms := int32(60000); ms <= 70000; ms += 13 {
			track = append(track, ms)
		}
		got := aliveOf(t, nil, nil, 120000, track)
		if len(got) != 1 || got[0].Start != 60000 {
			t.Fatalf("got %v, want one life starting at 60000 — a joiner must not "+
				"claim the time before they connected", got)
		}
	})
}

// A SPAWN trailing the position track must not delete the life it starts.
//
// The high clip used to land exactly on the last marker, on the argument that
// "a marker after the track proves existence at that instant and nothing past
// it". For a death that is right — a death ends its life, so the exact
// extension both covers the death and claims nothing after it. For a spawn it
// is self-defeating: the life is [spawn, …), clipping it to [spawn, spawn)
// makes it zero-width, and clipToPresence drops zero-width intervals. The
// player's whole post-respawn life vanished from the list while their frags
// and deaths kept flowing, landing on the previous life — which the same
// response says had already ended.
//
// The rule: when the last marker at or beyond the track's hold end is a
// spawn, the presence bound extends to the alive window's end and the life
// keeps its marker-derived end. That is the same degradation a player with NO
// track gets (nothing to clip against, so the marker-derived lives stand), and
// the wider claim is visible and interpretable where the deletion is silent
// data loss.
func TestAliveIntervalsTrailingSpawnKeepsItsLife(t *testing.T) {
	// The recorder loses the player 10 s in; hold end is a cadence past the
	// final sample.
	var track []int32
	for ms := int32(0); ms <= 10000; ms += 13 {
		track = append(track, ms)
	}
	holdEnd := result.TrackHoldEnd(track)

	t.Run("a spawn exactly at the hold end", func(t *testing.T) {
		got := aliveOf(t, []int32{holdEnd}, []int32{9000}, 120000, track)
		want := []result.Interval{{Start: 0, End: 9000}, {Start: holdEnd, End: 120000}}
		if !eqIntervals(got, want) {
			t.Fatalf("got %v, want %v — a spawn ON the hold-end boundary starts a "+
				"life like any other", got, want)
		}
	})

	t.Run("a death and its same-ms respawn past the track", func(t *testing.T) {
		got := aliveOf(t, []int32{63000}, []int32{63000}, 120000, track)
		want := []result.Interval{{Start: 0, End: 63000}, {Start: 63000, End: 120000}}
		if !eqIntervals(got, want) {
			t.Fatalf("got %v, want %v — an instant respawn is still a new life, and "+
				"the spawn wins the tie for what the last marker IS", got, want)
		}
	})

	t.Run("several spawns past the track", func(t *testing.T) {
		// The second spawn is a redundant marker (no death between them), so it
		// starts no life; the bound must still come from the trailing spawn.
		got := aliveOf(t, []int32{63100, 70000}, []int32{63000}, 120000, track)
		want := []result.Interval{{Start: 0, End: 63000}, {Start: 63100, End: 120000}}
		if !eqIntervals(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	t.Run("a spawn past the track followed by a death", func(t *testing.T) {
		// Death last: back to the exact extension, so the final life closes on
		// the death rather than running to the window end.
		got := aliveOf(t, []int32{63100}, []int32{63000, 65000}, 120000, track)
		want := []result.Interval{{Start: 0, End: 63000}, {Start: 63100, End: 65000}}
		if !eqIntervals(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})

	// The degenerate case: a spawn is the ONLY marker past the track and no
	// death precedes it, so it starts no life — it is a redundant re-entry
	// marker inside the one life the player has had since match start. The
	// same rule applies, and it has to: a bound that stopped at the spawn here
	// while extending past it when a death came first would be an asymmetry
	// with no evidential basis. The result is exactly what the player would
	// get with no position track at all.
	t.Run("a lone spawn past the track", func(t *testing.T) {
		got := aliveOf(t, []int32{63100}, nil, 120000, track)
		want := []result.Interval{{Start: 0, End: 120000}}
		if !eqIntervals(got, want) {
			t.Fatalf("got %v, want %v — a trailing spawn is not a life boundary "+
				"here, and nothing after it contradicts the player's presence", got, want)
		}
		if noTrack := aliveOf(t, []int32{63100}, nil, 120000, nil); !eqIntervals(got, noTrack) {
			t.Fatalf("with a truncated track %v, with no track at all %v — a track "+
				"that stops before a trailing spawn is the same evidential state "+
				"past that spawn as having no track, and must degrade the same way",
				got, noTrack)
		}
	})
}
