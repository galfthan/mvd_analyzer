package analyzer

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-analytics/view"
)

// Loc occupancy (locGraph) and region occupancy (regionControl.stats) measure
// the SAME quantity — time-weighted presence over the native position stream —
// through two walkers in two packages. Before schema v64 they disagreed
// structurally: loc-graph used a forward difference clamped to 50 ms and
// counted corpses, region-control integrated exactly but also counted corpses
// and let a departed player hold their last region forever.
//
// This test is the standing guarantee that the two cannot drift apart again.
// It is the assertion that would have caught the original defect.
//
// The identity is NOT the naive one, and that matters:
//
//   - region-control only walks players whose team is teamA or teamB
//     (region_control.go), while locGraph walks every player with a position
//     track, so ineligible players must be excluded here too;
//   - region membership is matched case-insensitively with LAST-region-wins
//     (regionByLoc in region_control.go), while locGraph keys the loc's
//     original spelling — so the test must build the same effective mapping
//     rather than trusting each region's declared Locs list;
//   - a loc belonging to no region contributes to locGraph only. That is
//     correct behaviour, not drift, and is asserted separately below.
func TestLocAndRegionOccupancyReconcile(t *testing.T) {
	res := buildReconcileResult()

	graph := BuildLocGraph(res)
	if graph == nil {
		t.Fatal("BuildLocGraph returned nil")
	}
	rc, err := view.RegionControl(res, view.RegionControlOptions{})
	if err != nil {
		t.Fatalf("RegionControl: %v", err)
	}
	if len(rc.Stats) == 0 {
		t.Fatal("RegionControl produced no stats")
	}

	// Same effective loc→region mapping region-control builds: lower-cased,
	// last declaration wins.
	regionOfLoc := map[string]string{}
	for _, rg := range rc.Regions {
		for _, ln := range rg.Locs {
			regionOfLoc[strings.ToLower(ln)] = rg.Name
		}
	}
	// Same eligibility filter: only players on teamA / teamB are walked.
	eligible := map[string]bool{}
	for _, p := range res.Match.Players {
		if p.Team == rc.TeamA || p.Team == rc.TeamB {
			eligible[p.Name] = true
		}
	}

	// Sum locGraph time per (region, player) through that mapping.
	type key struct{ region, player string }
	locMs := map[key]int32{}
	locArmedMs := map[key]int32{}
	var unmappedMs int32
	for _, n := range graph.Locs {
		region, mapped := regionOfLoc[strings.ToLower(n.Name)]
		for player, ms := range n.ByPlayer {
			if !eligible[player] {
				continue
			}
			if !mapped {
				unmappedMs += ms
				continue
			}
			locMs[key{region, player}] += ms
		}
		if !mapped || n.Armed == nil {
			continue
		}
		for player, ms := range n.Armed.ByPlayer {
			if eligible[player] {
				locArmedMs[key{region, player}] += ms
			}
		}
	}

	// Same tallies from the region-control side.
	regionMs := map[key]int32{}
	regionArmedMs := map[key]int32{}
	for regionName, st := range rc.Stats {
		for player, ps := range st.ByPlayer {
			k := key{regionName, player}
			regionMs[k] = int32(ps.Armed + ps.Unarmed)
			regionArmedMs[k] = int32(ps.Armed)
		}
	}

	// Compare the UNION of both key sets, not region-control's alone.
	// region-control creates a ByPlayer entry only for a player who accrued
	// time, so iterating its keys can only catch a pair where it credits MORE
	// than locGraph. A walker that credits a player nothing at all — the shape
	// every liveness / end-of-track / staleness bug takes — produces no key and
	// was compared against nothing.
	pairs := map[key]bool{}
	for k := range locMs {
		pairs[k] = true
	}
	for k := range locArmedMs {
		pairs[k] = true
	}
	for k := range regionMs {
		pairs[k] = true
	}
	ordered := make([]key, 0, len(pairs))
	for k := range pairs {
		ordered = append(ordered, k)
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].region != ordered[j].region {
			return ordered[i].region < ordered[j].region
		}
		return ordered[i].player < ordered[j].player
	})
	for _, k := range ordered {
		if locMs[k] != regionMs[k] {
			t.Errorf("%s / %s: locGraph total %d ms != regionControl armed+unarmed %d ms",
				k.region, k.player, locMs[k], regionMs[k])
		}
		if locArmedMs[k] != regionArmedMs[k] {
			t.Errorf("%s / %s: locGraph armed %d ms != regionControl armed %d ms",
				k.region, k.player, locArmedMs[k], regionArmedMs[k])
		}
	}
	if len(ordered) == 0 {
		t.Fatal("no (region, player) pairs compared — the fixture proves nothing")
	}

	// A loc in no region contributes to locGraph alone. Asserting this keeps
	// the test honest: without it, a fixture where every loc happened to be
	// mapped would silently stop covering the asymmetry.
	if unmappedMs == 0 {
		t.Error("fixture has no unmapped loc, so it does not cover the loc-outside-any-region case")
	}
}

// The reconciliation must hold BECAUSE both walkers exclude the dead, not by
// coincidence. This pins the corpse exclusion itself: the fixture's player
// dies mid-match and their body keeps streaming samples from a different loc,
// exactly as mvdsv broadcasts a corpse / gib head.
func TestOccupancyExcludesCorpseTime(t *testing.T) {
	res := buildReconcileResult()
	graph := BuildLocGraph(res)

	// "morgue" is where the corpse lies for the whole dead period. No live
	// player ever stands there, so no walker may credit any time to it.
	for _, n := range graph.Locs {
		if strings.EqualFold(n.Name, "morgue") {
			t.Fatalf("locGraph credited %d ms to the corpse's loc %q: %+v", n.Total, n.Name, n.ByPlayer)
		}
	}
	for _, e := range graph.Edges {
		if strings.EqualFold(e.From, "morgue") || strings.EqualFold(e.To, "morgue") {
			t.Errorf("locGraph emitted a movement edge through the corpse's loc: %s -> %s (%s)", e.From, e.To, e.Kind)
		}
	}

	rc, err := view.RegionControl(res, view.RegionControlOptions{})
	if err != nil {
		t.Fatalf("RegionControl: %v", err)
	}
	if st, ok := rc.Stats["morgue-region"]; ok {
		for player, ps := range st.ByPlayer {
			if ps.Armed+ps.Unarmed != 0 {
				t.Errorf("regionControl credited %s %d ms of presence in the corpse's region",
					player, ps.Armed+ps.Unarmed)
			}
		}
	}
}

// An early quitter must be credited the same bounded tail by BOTH walkers.
// This is the case an earlier revision of the fixture could not see, because
// every track ran to match end: region-control evaluates each interval at its
// LEFT endpoint, so a departed player's final sample credited them everything
// up to the next global event — the whole remaining match when the recording
// ends before the match window does (measured: 60 s credited for a player who
// left after 2 s). Both walkers now stop at result.TrackHoldEnd.
func TestOccupancyBoundsAnEarlyQuitter(t *testing.T) {
	res := buildReconcileResult()

	// p3 leaves a third of the way in; the match window runs far beyond.
	var quitterTrackEnd int32
	for i := range res.Streams.Players {
		if res.Streams.Players[i].Name == "p3" {
			pt := res.Streams.Players[i].Position
			quitterTrackEnd = pt.T[len(pt.T)-1]
		}
	}
	if quitterTrackEnd == 0 {
		t.Fatal("fixture has no early quitter — the bound is untested")
	}
	matchEnd := res.Streams.Global.MatchEnd
	if matchEnd <= quitterTrackEnd*2 {
		t.Fatalf("fixture match window (%d ms) is too close to the quitter's last sample (%d ms) to expose an unbounded hold",
			matchEnd, quitterTrackEnd)
	}

	graph := BuildLocGraph(res)
	var locMs int32
	for _, n := range graph.Locs {
		locMs += n.ByPlayer["p3"]
	}
	rc, err := view.RegionControl(res, view.RegionControlOptions{})
	if err != nil {
		t.Fatalf("RegionControl: %v", err)
	}
	var regionMs int32
	for _, st := range rc.Stats {
		if ps, ok := st.ByPlayer["p3"]; ok {
			regionMs += int32(ps.Armed + ps.Unarmed)
		}
	}

	// A bounded tail is one cadence past the final sample, never the rest of
	// the match.
	const slack = 250
	if locMs > quitterTrackEnd+slack {
		t.Errorf("locGraph credited the quitter %d ms; their track ends at %d ms", locMs, quitterTrackEnd)
	}
	if regionMs > quitterTrackEnd+slack {
		t.Errorf("regionControl credited the quitter %d ms; their track ends at %d ms (match runs to %d)",
			regionMs, quitterTrackEnd, matchEnd)
	}
	if locMs == 0 || regionMs == 0 {
		t.Errorf("quitter credited nothing at all (loc=%d region=%d) — the bound is over-broad", locMs, regionMs)
	}
}

// buildReconcileResult assembles a three-player, two-team Result whose streams
// exercise the cases the identity has to survive: a death with the corpse
// parked in its own loc, a loc that belongs to no region, posture intervals
// that start and end between position samples, an EARLY QUITTER whose track
// stops long before the match window, and a player who keeps producing
// boundaries after the quitter has gone (so the quitter's final interval has a
// distant next event to be wrongly stretched to).
func buildReconcileResult() *Result {
	const step = int32(13) // native cadence on a full-tick server
	locTable := []string{"", "spawn", "mid", "morgue", "backyard"}

	mk := func(name, team string, li []int16, deaths, spawns []int32, rl []result.Interval) result.PlayerStream {
		pt := &result.PositionTrack{}
		for i, l := range li {
			pt.T = append(pt.T, int32(i)*step)
			pt.X = append(pt.X, float32(i))
			pt.Y = append(pt.Y, 0)
			pt.Z = append(pt.Z, 0)
			pt.Li = append(pt.Li, l)
		}
		return result.PlayerStream{
			Name: name, Team: team, Position: pt,
			Spawns: spawns, Deaths: deaths, RL: rl,
		}
	}

	// p1: alive in "spawn" then "mid", dies at 10*step, corpse sits in
	// "morgue" until it respawns at 20*step into "mid".
	li1 := []int16{1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 3, 3, 3, 3, 3, 3, 3, 3, 3, 3, 2, 2, 2, 2, 2}
	// The death lands BETWEEN two samples (5 ms before the body's first
	// "morgue" sample), not on one. That is what makes the Alive interval
	// endpoints load-bearing as walk boundaries: with a death on a sample
	// instant the boundary already exists from the sample time, so dropping
	// Alive from the boundary set would change nothing. Here the final live
	// interval must be truncated mid-span at the death itself.
	p1 := mk("p1", "red", li1, []int32{10*step - 5}, []int32{20 * step},
		// Armed from mid-interval to mid-interval, so the boundary split matters.
		[]result.Interval{{Start: 3*step + 5, End: 22*step - 4}})

	// p2: never dies; spends the tail in "backyard", which is in no region.
	li2 := []int16{1, 1, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2, 2, 2, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4}
	p2 := mk("p2", "blue", li2, nil, nil, nil)

	// p3: an EARLY QUITTER. Their track stops a third of the way in while p2
	// keeps producing boundaries, so the interval starting at p3's final
	// sample has a distant next event to be wrongly stretched to — the shape
	// that hid the unbounded-hold defect when every track ran to match end.
	li3 := []int16{1, 1, 1, 2, 2, 2, 2, 2}
	p3 := mk("p3", "red", li3, nil, nil, nil)

	// The match window runs well past every track, so an end-of-track policy
	// that failed to bound presence would be visible rather than clipped away
	// by winEnd. This is the fixture property the invariant depends on.
	matchEnd := int32(len(li1)+40) * step

	// p4 is on a THIRD team. region-control only walks teamA/teamB, while
	// locGraph walks everyone with a track — so the identity only holds if the
	// test applies the same eligibility filter. Without this player that
	// filter is dead code.
	p4 := mk("p4", "green", li2, nil, nil, nil)

	res := &Result{
		Streams: &result.Streams{
			Global:  result.GlobalStream{MatchStart: 0, MatchEnd: matchEnd},
			Players: []result.PlayerStream{p1, p2, p3, p4},
		},
		TimelineAnalysis: &TimelineAnalysisResult{
			LocTable: locTable,
			LocationData: []MapLocation{
				{Name: "spawn"}, {Name: "mid"}, {Name: "morgue"}, {Name: "backyard"},
			},
			RegionControl: &result.RegionControlResult{
				Regions: []result.ControlRegion{
					{Name: "home", Locs: []string{"spawn"}},
					{Name: "centre", Locs: []string{"MiD"}}, // mixed case: region-control lower-cases, locGraph keeps the original
					{Name: "morgue-region", Locs: []string{"morgue"}},
					// "backyard" is deliberately in no region.
				},
				TeamA: "red", TeamB: "blue",
			},
		},
		Match: &MatchResult{Players: []PlayerStat{
			{Name: "p1", Team: "red"}, {Name: "p2", Team: "blue"},
			{Name: "p3", Team: "red"}, {Name: "p4", Team: "green"},
		}},
		DemoInfo: &DemoInfoResult{Players: []DemoInfoPlayer{
			{Name: "p1", Team: "red"}, {Name: "p2", Team: "blue"},
			{Name: "p3", Team: "red"}, {Name: "p4", Team: "green"},
		}},
	}
	deriveAliveIntervals(res.Streams)
	return res
}

// The synthetic fixture above proves the walkers agree on a case someone
// designed. This proves it on demos nobody designed.
//
// It reads the golden corpus's cached demos directly rather than fetching, so
// it is a no-op on a machine that has never run TestGoldenCorpus. That is the
// right trade: the check is worthless if it can only run where the fixture
// already passes, and it must not turn an offline `make test` red.
func TestLocAndRegionOccupancyReconcileOnRealDemos(t *testing.T) {
	cacheDir := filepath.Join("..", "testdata", "cache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) == 0 {
		t.Skip("no cached corpus demos — run TestGoldenCorpus once to populate testdata/cache")
	}

	// Demos are chosen EXPLICITLY, not by taking the first few directory
	// entries: lexical order silently excluded 216835, the defer/reconnect
	// demo, which is precisely the one whose numbers move when the
	// end-of-track bound is wrong. Any missing id is skipped, so this still
	// works on a partially-populated cache.
	want := []string{
		"216835.mvd.gz", // reconnect / deferred join
		"212483.mvd.gz", // 4on4, sv_demofps-default cadence (~36 ms)
		"212423.mvd.gz", // duel, ~39 ms cadence
		"211805.mvd.gz", // 2on2 dm6, full-tick cadence (~15 ms)
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.Name()] = true
	}

	checkedDemos := 0
	for _, name := range want {
		if !have[name] {
			continue
		}
		path := filepath.Join(cacheDir, name)
		res, err := NewDefaultRegistry().Analyze(path)
		if err != nil || res == nil || res.LocGraph == nil ||
			res.TimelineAnalysis == nil || res.TimelineAnalysis.RegionControl == nil {
			continue
		}
		rc := res.TimelineAnalysis.RegionControl
		if len(rc.Stats) == 0 {
			continue
		}
		checkedDemos++

		regionOfLoc := map[string]string{}
		for _, rg := range rc.Regions {
			for _, ln := range rg.Locs {
				regionOfLoc[strings.ToLower(ln)] = rg.Name
			}
		}
		eligible := map[string]bool{}
		if res.Match != nil {
			for _, p := range res.Match.Players {
				if p.Team == rc.TeamA || p.Team == rc.TeamB {
					eligible[p.Name] = true
				}
			}
		}
		type key struct{ region, player string }
		locMs := map[key]int32{}
		for _, n := range res.LocGraph.Locs {
			region, mapped := regionOfLoc[strings.ToLower(n.Name)]
			if !mapped {
				continue
			}
			for player, ms := range n.ByPlayer {
				if eligible[player] {
					locMs[key{region, player}] += ms
				}
			}
		}
		regionMs := map[key]int32{}
		for regionName, st := range rc.Stats {
			for player, ps := range st.ByPlayer {
				regionMs[key{regionName, player}] = int32(ps.Armed + ps.Unarmed)
			}
		}
		// The union, for the reason the synthetic test spells out: a pair only
		// locGraph credits has no region-control key to iterate.
		seen := map[key]bool{}
		pairs := 0
		for _, k := range []map[key]int32{locMs, regionMs} {
			for kk := range k {
				if seen[kk] {
					continue
				}
				seen[kk] = true
				pairs++
				if locMs[kk] != regionMs[kk] {
					t.Errorf("%s: %s / %s — locGraph %d ms != regionControl %d ms (delta %d)",
						name, kk.region, kk.player, locMs[kk], regionMs[kk], locMs[kk]-regionMs[kk])
				}
			}
		}
		if pairs == 0 {
			t.Errorf("%s: no (region, player) pairs to compare", name)
		}
	}
	if checkedDemos == 0 {
		t.Skip("no cached demo produced both a loc graph and region-control stats")
	}
	t.Logf("reconciled loc and region occupancy on %d real demos", checkedDemos)
}
