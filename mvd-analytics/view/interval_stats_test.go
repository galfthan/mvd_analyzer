package view

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// statsResult builds a one-player Result whose loc stream and position track
// are explicit, so the loc tests pin the builder rather than the derivation.
func statsResult(frags []result.FragEntry, loc []result.ChangeI16, pt *result.PositionTrack) *result.Result {
	return &result.Result{
		Frags: &result.FragResult{Frags: frags},
		Streams: &result.Streams{
			Global:  result.GlobalStream{MatchStart: 0, MatchEnd: 60000},
			Players: []result.PlayerStream{{Name: "A", Team: "red", Loc: loc, Position: pt}},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{LocTable: []string{"", "spawn", "mid", "quad"}},
	}
}

// samples is a position-track sampling every 13 ms — a server recording at
// full tick, where nothing is unobserved — from lo to hi inclusive, so the
// evidence bounds in the tests below are exact.
func samples(lo, hi int32) []int32 {
	var t []int32
	for s := lo; s < hi; s += 13 {
		t = append(t, s)
	}
	return append(t, hi)
}

func track(lo, hi int32) *result.PositionTrack {
	return &result.PositionTrack{T: samples(lo, hi)}
}

// ---------------------------------------------------------------------------
// The attribution window
// ---------------------------------------------------------------------------

// window() resolves the two shapes a span comes in.
func TestStatsSpanWindow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sp       statsSpan
		lo, hi   int32
		loI, hiI bool
	}{
		{
			// The zero value of the attribution fields is the non-partitioning
			// case that HotWindows passes: the window IS the interval, closed at
			// both ends. Changing that silently would move every hot window's
			// stats off its own score.
			"unattributed: the window is the interval, closed",
			statsSpan{start: 1000, end: 2000, startInclusive: true},
			1000, 2000, true, true,
		},
		{
			// A stored Alive list this package does not produce could in
			// principle be out of order; the window clamps rather than
			// inverting, which would make the counts depend on comparison order.
			"attributed and inverted: clamped, not inverted",
			statsSpan{start: 5000, end: 6000, attributed: true, attrStart: 5000, attrEnd: 3000},
			5000, 5000, false, false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lo, hi, loI, hiI := tc.sp.window()
			if lo != tc.lo || hi != tc.hi || loI != tc.loI || hiI != tc.hiI {
				t.Errorf("window() = %d,%d,%v,%v; want %d,%d,%v,%v",
					lo, hi, loI, hiI, tc.lo, tc.hi, tc.loI, tc.hiI)
			}
		})
	}
}

// The four edge combinations, each asserted through build so the test pins the
// predicate every event class actually uses rather than a helper.
func TestStatsSpanEdgeRules(t *testing.T) {
	frags := []result.FragEntry{
		kill(1000, "A", "B", "rl"), // on the window start
		kill(1500, "A", "B", "rl"), // strictly inside
		kill(2000, "A", "B", "rl"), // on the window end
	}
	loc := []result.ChangeI16{{T: 0, V: 1}}
	sb := newStatsBuilder(statsResult(frags, loc, track(0, 5000)), "")

	cases := []struct {
		name       string
		start, end bool
		wantKills  int
	}{
		{"closed both ends", true, true, 3},
		{"half-open at the end", true, false, 2},
		{"half-open at the start", false, true, 2},
		{"open both ends", false, false, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sb.build("A", statsSpan{
				start: 1000, end: 2000,
				attributed: true, attrStart: 1000, attrEnd: 2000,
				startInclusive: c.start, endInclusive: c.end,
			})
			if got.Kills != c.wantKills {
				t.Errorf("kills = %d, want %d", got.Kills, c.wantKills)
			}
			// Every event class shares one predicate; eventLocs must not
			// re-derive its own bounds (it did, and double-counted the
			// boundary kill). Every kill here resolves to one loc, so the
			// counts must match exactly.
			sum := 0
			for _, e := range got.EventLocs {
				sum += e.Count
			}
			if sum != got.Kills {
				t.Errorf("sum(eventLocs) = %d, kills = %d", sum, got.Kills)
			}
		})
	}
}

// The attribution window is wider than the interval for a partitioning
// segmentation, and DurationMs must not follow it.
func TestStatsSpanDurationIsTheIntervalNotTheWindow(t *testing.T) {
	frags := []result.FragEntry{kill(11000, "A", "B", "rl")}
	sb := newStatsBuilder(statsResult(frags, nil, nil), "")
	got := sb.build("A", statsSpan{
		start: 0, end: 10000,
		attributed: true, attrStart: 0, attrEnd: 12000,
		startInclusive: true, endInclusive: false,
	})
	if got.Kills != 1 {
		t.Errorf("kills = %d, want 1 — the kill is inside the attribution window", got.Kills)
	}
	if got.DurationMs != 10000 {
		t.Errorf("durationMs = %d, want 10000 — duration is the interval, not the window", got.DurationMs)
	}
}

// ---------------------------------------------------------------------------
// Measuredness
// ---------------------------------------------------------------------------

// Half of the contract: every NUMERIC field is emitted even at zero, so a
// measured zero can never disappear ("measured-zero-becomes-absent", the bug
// player_stats.go names). The maps and slices keep omitempty — for those,
// absence means "nothing to list".
func TestIntervalStatsEmitsMeasuredZeros(t *testing.T) {
	b, err := json.Marshal(IntervalStats{})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"durationMs", "kills", "deaths", "teamKills", "suicides",
		"damageGiven", "damageTaken", "damageGivenTeam", "damageGivenSelf",
		"shots", "hits",
	} {
		if !strings.Contains(string(b), `"`+key+`"`) {
			t.Errorf("%q is absent from an all-zero stats block: %s\n"+
				"a measured zero must be emitted; measuredness is read from `measured`, never from omitempty", key, b)
		}
	}
	for _, key := range []string{"byWeapon", "damageByWeapon", "victims", "locs", "eventLocs", "mainWeapon"} {
		if strings.Contains(string(b), `"`+key+`"`) {
			t.Errorf("%q was emitted empty: %s\nfor a list, absence means 'nothing to list'", key, b)
		}
	}
}

// The other half: the envelope marker says which sources the demo carried, and
// it is the ONLY thing that says so. It must track the Result exactly — a flag
// claiming a source the builder cannot fill is worse than no marker at all.
func TestMeasuredSourcesTracksTheDemo(t *testing.T) {
	full := livesResult([]result.Interval{{Start: 0, End: 10000}}, []result.FragEntry{kill(1000, "A", "B", "rl")})
	full.Damage = &result.DamageResult{}
	full.Shots = &result.ShotsResult{}
	full.Items = &result.ItemsResult{}

	got := mustLives(t, full, LivesOptions{})
	want := MeasuredSources{Frags: true, Damage: true, Shots: true, Locs: true, Items: true, Liveness: true}
	if got.Measured != want {
		t.Errorf("measured = %+v, want %+v", got.Measured, want)
	}
	// itemsTaken's null-vs-[] distinction and measured.items are one predicate.
	if got.Lives[0].ItemsTaken == nil {
		t.Error("measured.items is true but itemsTaken is null — the two must agree")
	}

	// Strip every source in turn. Frags is not stripped: Lives needs no frag
	// log, but hwResult/livesResult builds the segmentation around one, and the
	// unavailability path is covered by TestLivesUnavailable.
	for _, c := range []struct {
		name  string
		strip func(*result.Result)
		check func(MeasuredSources) bool
	}{
		{"damage", func(r *result.Result) { r.Damage = nil }, func(m MeasuredSources) bool { return m.Damage }},
		{"shots", func(r *result.Result) { r.Shots = nil }, func(m MeasuredSources) bool { return m.Shots }},
		{"items", func(r *result.Result) { r.Items = nil }, func(m MeasuredSources) bool { return m.Items }},
		{"loc table", func(r *result.Result) { r.TimelineAnalysis = nil }, func(m MeasuredSources) bool { return m.Locs }},
		{"loc streams", func(r *result.Result) { r.Streams.Players[0].Loc = nil }, func(m MeasuredSources) bool { return m.Locs }},
	} {
		t.Run("without "+c.name, func(t *testing.T) {
			res := livesResult([]result.Interval{{Start: 0, End: 10000}}, []result.FragEntry{kill(1000, "A", "B", "rl")})
			res.Damage = &result.DamageResult{}
			res.Shots = &result.ShotsResult{}
			res.Items = &result.ItemsResult{}
			c.strip(res)
			got := mustLives(t, res, LivesOptions{})
			if c.check(got.Measured) {
				t.Errorf("measured = %+v still claims a source the demo lost", got.Measured)
			}
			if c.name == "items" && got.Lives[0].ItemsTaken != nil {
				t.Error("measured.items is false but itemsTaken is not null — the two must agree")
			}
		})
	}
}

// measured.frags is NOT "a frag section exists". The dangerous state is a
// PRESENT but empty frag log on a demo where players demonstrably died: every
// obituary went unmatched, so a row of `kills: 0, teamKills: 0, suicides: 0`
// beside a measured death count looks like a measurement and is not one. The
// verdict is demo-global, decided by the analyzer (killsMeasurable) and stored
// as FragResult.KillsMeasured; this pins that view READS it rather than
// re-deriving a pointer check that cannot see the state at all.
func TestMeasuredFragsReadsTheStoredKillAttributionVerdict(t *testing.T) {
	res := livesResult([]result.Interval{{Start: 0, End: 10000}}, nil)
	res.Frags.Frags = nil // the log is present, and empty
	res.Frags.KillsMeasured = false
	res.Streams.Players[0].Deaths = []int32{5000}

	got := mustLives(t, res, LivesOptions{})
	if got.Measured.Frags {
		t.Error("measured.frags is true on a demo whose frag log matched no obituary — " +
			"the row's 0 kills / 0 teamkills would read as measured")
	}
	if len(got.Lives) == 0 || got.Lives[0].Kills != 0 {
		t.Fatalf("fixture: want one life with no kills, got %+v", got.Lives)
	}
	// The same demo with a matched log is measured, so the flag tracks the
	// verdict and not merely the emptiness of the response.
	res.Frags.KillsMeasured = true
	if got := mustLives(t, res, LivesOptions{}); !got.Measured.Frags {
		t.Error("measured.frags is false on a demo the analyzer judged measurable")
	}
	// And hot windows read the same flag from the same builder.
	if hw := mustHW(t, res, HotWindowsOptions{}); !hw.Measured.Frags {
		t.Error("/hot-windows measured.frags disagrees with /lives on one demo")
	}
}

// ---------------------------------------------------------------------------
// dwellLocs
// ---------------------------------------------------------------------------

func TestDwellLocsClipsToTheInterval(t *testing.T) {
	loc := []result.ChangeI16{{T: 0, V: 1}, {T: 5000, V: 2}}
	sb := newStatsBuilder(statsResult(nil, loc, track(0, 20000)), "")
	got := sb.dwellLocs("A", 2000, 8000)
	if len(got) != 2 {
		t.Fatalf("got %+v, want two locs", got)
	}
	// spawn: [2000,5000] = 3000; mid: [5000,8000] = 3000. Ties break by name.
	if got[0].Loc != "mid" || got[0].Ms != 3000 {
		t.Errorf("got[0] = %+v, want mid/3000", got[0])
	}
	if got[1].Loc != "spawn" || got[1].Ms != 3000 {
		t.Errorf("got[1] = %+v, want spawn/3000", got[1])
	}
}

// The defect this pins: the last loc-change entry's segment used to run to the
// end of the interval unconditionally, so when the position track stops — a
// player who quit, or one outside a POV recorder's PVS — one stale loc absorbed
// the whole remainder. A loc stream ending at 9000 inside a life running to
// 30000 credited 21 seconds to wherever they were last seen.
func TestDwellLocsStopsAtTheEndOfTheTrack(t *testing.T) {
	loc := []result.ChangeI16{{T: 0, V: 1}, {T: 9000, V: 2}}
	pt := track(0, 9000) // the recording loses the player at 9000
	sb := newStatsBuilder(statsResult(nil, loc, pt), "")
	got := sb.dwellLocs("A", 0, 30000)

	var total int32
	for _, l := range got {
		total += l.Ms
	}
	// Evidence ends one median gap (13 ms) after the final sample.
	if want := int32(9000 + 13); total != want {
		t.Errorf("dwell total = %d ms, want %d — unobserved time must not be credited", total, want)
	}
	for _, l := range got {
		if l.Loc == "mid" && l.Ms > result.SampleStaleCapMs {
			t.Errorf("the stale loc absorbed %d ms; the cap is %d", l.Ms, result.SampleStaleCapMs)
		}
	}
}

// A hole in the middle of the track is unobserved too, and the same cap
// applies — this is the POV-recording case, where only players inside the
// recorder's PVS are written.
func TestDwellLocsBoundsAnInteriorHole(t *testing.T) {
	pt := &result.PositionTrack{T: append(samples(0, 2000), samples(20000, 22000)...)}
	loc := []result.ChangeI16{{T: 0, V: 1}, {T: 20000, V: 2}}
	sb := newStatsBuilder(statsResult(nil, loc, pt), "")
	got := sb.dwellLocs("A", 0, 22000)

	var spawn int32
	for _, l := range got {
		if l.Loc == "spawn" {
			spawn = l.Ms
		}
	}
	// [0,20000] minus the unobserved (2000+250, 20000) hole.
	if want := int32(2000 + result.SampleStaleCapMs); spawn != want {
		t.Errorf("spawn dwell = %d ms, want %d — the 18 s hole must not be credited", spawn, want)
	}
}

// Without a position track there is no evidence to bound against, so the hold
// stays unbounded. Hand-assembled Results are the only place this happens; the
// honest alternative — crediting nothing — would blank the field rather than
// describe it.
func TestDwellLocsWithoutAPositionTrackHoldsToTheEnd(t *testing.T) {
	loc := []result.ChangeI16{{T: 0, V: 1}}
	sb := newStatsBuilder(statsResult(nil, loc, nil), "")
	got := sb.dwellLocs("A", 0, 10000)
	if len(got) != 1 || got[0].Ms != 10000 {
		t.Errorf("got %+v, want the whole interval on one loc", got)
	}
}

func TestDwellLocsAbsentLocDataYieldsNothing(t *testing.T) {
	sb := newStatsBuilder(statsResult(nil, nil, track(0, 10000)), "")
	if got := sb.dwellLocs("A", 0, 10000); got != nil {
		t.Errorf("got %+v, want nil when the demo carries no loc stream", got)
	}
}

// ---------------------------------------------------------------------------
// killLocs
// ---------------------------------------------------------------------------

func TestKillLocsCountsWhereTheKillsHappened(t *testing.T) {
	frags := []result.FragEntry{
		kill(1000, "A", "B", "rl"),
		kill(6000, "A", "B", "rl"),
		kill(7000, "A", "B", "lg"),
		{Time: 8000, Killer: "A", Victim: "B", Weapon: "rl", IsTeamKill: true}, // not a kill
		{Time: 8500, Killer: "A", Victim: "A", Weapon: "rl", IsSuicide: true},  // not a kill
	}
	loc := []result.ChangeI16{{T: 0, V: 1}, {T: 5000, V: 2}}
	sb := newStatsBuilder(statsResult(frags, loc, track(0, 20000)), "")
	got := sb.killLocs("A", func(int32) bool { return true })
	if len(got) != 2 {
		t.Fatalf("got %+v, want two locs", got)
	}
	if got[0].Loc != "mid" || got[0].Count != 2 {
		t.Errorf("got[0] = %+v, want mid/2", got[0])
	}
	if got[1].Loc != "spawn" || got[1].Count != 1 {
		t.Errorf("got[1] = %+v, want spawn/1", got[1])
	}
}

// A kill before the first loc change resolves to the empty sentinel and is not
// credited to a fabricated loc — which is why sum(eventLocs) can be below
// kills, and must never be above it.
func TestKillLocsSkipsUnresolvedLocs(t *testing.T) {
	frags := []result.FragEntry{kill(100, "A", "B", "rl"), kill(6000, "A", "B", "rl")}
	loc := []result.ChangeI16{{T: 5000, V: 2}}
	sb := newStatsBuilder(statsResult(frags, loc, track(0, 20000)), "")
	got := sb.killLocs("A", func(int32) bool { return true })
	if len(got) != 1 || got[0].Loc != "mid" || got[0].Count != 1 {
		t.Errorf("got %+v, want only the resolved kill", got)
	}
}

// ---------------------------------------------------------------------------
// topLocs / topCountKey
// ---------------------------------------------------------------------------

func TestTopLocsRanksAndCaps(t *testing.T) {
	got := topLocs(map[string]int32{"a": 10, "b": 40, "c": 40, "d": 5, "e": 30}, nil)
	if len(got) != intervalTopLocs {
		t.Fatalf("got %d locs, want %d", len(got), intervalTopLocs)
	}
	// Value descending, ties by name: b(40), c(40), e(30).
	if got[0].Loc != "b" || got[1].Loc != "c" || got[2].Loc != "e" {
		t.Errorf("order = %q,%q,%q; want b,c,e", got[0].Loc, got[1].Loc, got[2].Loc)
	}
	if got := topLocs(nil, nil); got != nil {
		t.Errorf("empty input: got %+v, want nil", got)
	}
}

func TestTopCountKeyBreaksTiesByName(t *testing.T) {
	// Map iteration is randomised; a tie must still resolve the same way every
	// run or the golden corpus churns.
	for i := 0; i < 50; i++ {
		if got := topCountKey(map[string]int{"rl": 2, "lg": 2, "sg": 1}); got != "lg" {
			t.Fatalf("topCountKey = %q, want lg (the alphabetically first of the tied maxima)", got)
		}
	}
	if got := topCountKey(nil); got != "" {
		t.Errorf("topCountKey(nil) = %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// Parity with the sibling endpoints
// ---------------------------------------------------------------------------

// parityDemos are the fixtures both acceptance tests below run on: COMMITTED
// golden results, not the gitignored demo cache, so they run on every
// `make test`, offline. (The view package cannot import analyzer — that is an
// import cycle — so a real demo reaches it as its serialised Result.)
var parityDemos = []string{
	"2on2_nani_pora_210426_dm6.json",
	"4on4_osams_ra_230426_dm3.json",
}

// The plan's acceptance test for the shared primitive: over non-degenerate
// windows of a real demo, the stats block must equal what /frags and /damage
// report for the same window. If they can drift, a caller adding up per-life or
// per-window stats gets a number that contradicts the endpoint they would
// check it against.
//
// Parity is EXACT, including the telefrag/stomp fold: a positional kill carries
// no per-hit entry, so a stats block rebuilt from the hit log alone used to come
// out short by its value and per-life damage did not add up to what /damage
// reports. The builder folds it in through the same helpers view.Damage uses.
// The test counts the folds it exercised and fails if there were none, since a
// fixture without positional kills would prove nothing about that half.
//
// One difference is structural rather than a tolerance: view.Frags / view.Damage
// treat From==0 as "no bound", which is why the builder must not call them (an
// interval starting at t=0 would receive whole-match aggregates). Every window
// here therefore starts past 0.
//
// BOTH FAMILIES are exercised. The earlier version built the stats with fam=""
// and compared against view.Damage with an unset Dmg — both raw — so it could
// not see a family mismatch at all, which is how the builder came to be handed
// "" for every non-damage metric while the REST layer defaulted to bounded
// (incident: adversarial review, 2026-08-01). The two families differ by
// hundreds of points per window on a real demo, so a swapped family now fails
// here.
func TestIntervalStatsParityWithFragsAndDamage(t *testing.T) {
	checked, windows, folds, differing := 0, 0, 0, 0
	for _, name := range parityDemos {
		res := loadGolden(t, name)
		if res == nil || res.Frags == nil || res.Damage == nil || res.Streams == nil {
			continue
		}
		checked++
		matchEnd := res.Streams.Global.MatchEnd

		for _, fam := range []string{"raw", "bounded"} {
			if fam == "bounded" && !hasBoundedFamily(res.Damage) {
				continue
			}
			sb := newStatsBuilder(res, fam)

			// Ten windows spread across the match, each a tenth of it long. Wide
			// enough that every one carries kills and damage, and none starts at 0.
			step := matchEnd / 12
			for k := int32(1); k <= 10; k++ {
				from, to := k*step, k*step+step
				fv, err := Frags(res, FragOptions{From: from, To: to})
				if err != nil {
					t.Fatalf("%s: Frags: %v", name, err)
				}
				dv, err := Damage(res, DamageOptions{From: from, To: to, Dmg: fam})
				if err != nil {
					t.Fatalf("%s: Damage(%s): %v", name, fam, err)
				}
				if len(fv.Frags) == 0 || len(dv.Events) == 0 {
					t.Errorf("%s: window [%d,%d] is degenerate (%d frags, %d hits) — the parity test would prove nothing",
						name, from, to, len(fv.Frags), len(dv.Events))
					continue
				}
				windows++

				// The two families must actually DIFFER somewhere, or comparing
				// them proves nothing about which one was used.
				if raw, err := Damage(res, DamageOptions{From: from, To: to, Dmg: "raw"}); err == nil &&
					raw.TotalDamage != dv.TotalDamage {
					differing++
				}

				for _, player := range goldenPlayers(res) {
					got := sb.build(player, statsSpan{start: from, end: to, startInclusive: true})

					pf := fv.ByPlayer[player]
					if pf == nil {
						pf = &result.PlayerFrags{}
					}
					if got.Kills != pf.Kills || got.Deaths != pf.Deaths || got.TeamKills != pf.TeamKills {
						t.Errorf("%s [%d,%d] %s: stats kills/deaths/tk = %d/%d/%d, /frags = %d/%d/%d",
							name, from, to, player, got.Kills, got.Deaths, got.TeamKills,
							pf.Kills, pf.Deaths, pf.TeamKills)
					}

					pd := dv.ByPlayer[player]
					if pd == nil {
						pd = &result.PlayerDamage{}
					}
					folds += positionalKillsFor(res, player, from, to)
					if got.DamageGiven != pd.Given || got.DamageTaken != pd.Taken ||
						got.DamageGivenTeam != pd.GivenTeam || got.DamageGivenSelf != pd.GivenSelf {
						t.Errorf("%s [%d,%d] %s dmg=%s: stats given/taken/team/self = %d/%d/%d/%d, /damage = %d/%d/%d/%d",
							name, from, to, player, fam,
							got.DamageGiven, got.DamageTaken, got.DamageGivenTeam, got.DamageGivenSelf,
							pd.Given, pd.Taken, pd.GivenTeam, pd.GivenSelf)
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no golden result loaded — the fixtures are committed, so this is a real failure")
	}
	if folds == 0 {
		t.Error("no telefrag or stomp fell inside any window — the fold half of this test proved nothing; " +
			"pick fixtures or windows that carry positional kills")
	}
	if differing == 0 {
		t.Error("raw and bounded agreed on every window — the family half of this test proved nothing")
	}
	t.Logf("parity held over %d windows on %d demos (%d folds, %d windows where the families differ)",
		windows, checked, folds, differing)
}

// The invariant the positional-kill fold exists to make true, checked over
// every window of two real demos: with NO weapons filter, a window's score IS
// the same-named field of its own stats block. Score and stat sharing a name
// while differing by hundreds of points is the trap; scoredBy exists to explain
// the one case where they legitimately differ (a weapons filter), not to
// paper over a metric that does not score itself.
//
// Measured before the fix: 202 of 70964 windows across the 42 cached demos
// diverged, every one of them a positional kill. Committed goldens, so this
// runs offline on every `make test`.
func TestHotWindowScoreEqualsItsOwnStat(t *testing.T) {
	checked, windows := 0, 0
	for _, name := range parityDemos {
		res := loadGolden(t, name)
		if res == nil || res.Frags == nil || res.Damage == nil {
			continue
		}
		checked++
		for _, m := range KnownHotWindowMetrics {
			for _, fam := range []string{"raw", "bounded"} {
				if fam == "bounded" && !hasBoundedFamily(res.Damage) {
					continue
				}
				hw, err := HotWindows(res, HotWindowsOptions{Metric: m, Dmg: fam, Limit: -1})
				if err != nil {
					continue // a stream this demo lacks
				}
				for _, w := range hw.Windows {
					windows++
					var stat int
					switch m {
					case MetricFrags:
						stat = w.Kills
					case MetricDeaths:
						stat = w.Deaths
					case MetricNetFrags:
						stat = w.Kills - w.Deaths
					case MetricDamageGiven:
						stat = w.DamageGiven
					case MetricDamageTaken:
						stat = w.DamageTaken
					case MetricNetDamage:
						stat = w.DamageGiven - w.DamageTaken
					case MetricShots:
						stat = w.Shots
					case MetricHits:
						stat = w.Hits
					}
					if stat != w.Score {
						t.Fatalf("%s metric=%s dmg=%s: %s [%d,%d] score = %d but the row's own stat = %d",
							name, m, fam, w.Player, w.Start, w.End, w.Score, stat)
					}
				}
			}
		}
	}
	if checked == 0 || windows == 0 {
		t.Fatal("no golden result produced windows — the fixtures are committed, so this is a real failure")
	}
	t.Logf("score == stat on %d windows across %d demos", windows, checked)
}

// positionalKillsFor counts the telefrags and stomps crediting a player in a
// window — how much fold the assertion above actually exercised, not what it
// should be worth. Deriving the VALUE here would only re-implement the rule
// under test; /damage is the oracle for that.
func positionalKillsFor(r *result.Result, player string, from, to int32) int {
	if r.Damage == nil || r.Damage.BoundedMode != "standard" {
		return 0
	}
	n := 0
	for _, list := range [][]result.PositionalKill{r.Damage.Telefrags, r.Damage.Stomps} {
		for _, k := range list {
			if k.Time < from || k.Time > to {
				continue
			}
			if k.Victim == player || k.Attacker == player {
				n++
			}
		}
	}
	return n
}

// loadGolden reads a committed golden Result. It is the analyzer's own output
// for a real demo, round-tripped through the published JSON contract.
func loadGolden(t *testing.T, name string) *result.Result {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", "golden", name))
	if err != nil {
		t.Fatalf("read golden %s: %v", name, err)
	}
	var r result.Result
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("unmarshal golden %s: %v", name, err)
	}
	return &r
}

// goldenPlayers is every name the frag log or the streams know, sorted, so the
// comparison covers both key sets — a stream named "A#1" against a frag log
// naming "A" would otherwise skip the comparison and pass while proving
// nothing.
func goldenPlayers(r *result.Result) []string {
	set := map[string]bool{}
	for name := range r.Frags.ByPlayer {
		set[name] = true
	}
	for i := range r.Streams.Players {
		set[r.Streams.Players[i].Name] = true
	}
	out := make([]string, 0, len(set))
	for name := range set {
		if name != "" {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
