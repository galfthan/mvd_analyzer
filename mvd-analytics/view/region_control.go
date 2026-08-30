package view

import (
	"sort"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Region-control state codes used as a compact one-char-per-bucket
// encoding for RegionControlResult.BucketStates. Mirror
// classifyRegionState in mvd-web/static/app.js. Exported so consumers
// (frontend, MCP wrappers, tests) can decode the bucketStates strings.
const (
	RegionStateEmpty            byte = '_'
	RegionStateTeamAControl     byte = 'A'
	RegionStateTeamAWeakControl byte = 'a'
	RegionStateContested        byte = 'C'
	RegionStateWeakContested    byte = 'c'
	RegionStateTeamBControl     byte = 'B'
	RegionStateTeamBWeakControl byte = 'b'
)

// RegionControlOptions tunes a RegionControl query. Every field is
// optional; defaults are derived from r.TimelineAnalysis.RegionControl
// (regions + team labels — populated by the analyzer's region-
// detection pass during Finalize) and r.Match.Players (team-of-name).
//
// Callers that already know the answer — typically the WASM bridge,
// where the user is editing region definitions in the UI — pass
// explicit Regions / TeamA / TeamB / TeamOf overrides.
type RegionControlOptions struct {
	WindowMs  int                      // bucket resolution; 0 → 50
	StartTime int32                    // sub-window lower bound, int32 ms; 0 → match start
	EndTime   int32                    // sub-window upper bound, int32 ms; 0 → match end
	Regions   []result.ControlRegion   // overrides r.TimelineAnalysis.RegionControl.Regions
	TeamA     string                   // overrides r.TimelineAnalysis.RegionControl.TeamA
	TeamB     string                   // overrides r.TimelineAnalysis.RegionControl.TeamB
	TeamOf    func(name string) string // overrides default closure over r.Match.Players
}

// RegionControlView aliases result.RegionControlResult so the view-
// package surface uses the same XxxView vocabulary as the other view
// functions. The aliased type is the canonical one because the same
// shape is baked into parse-time Result by the regionControlPost
// post-processor.
type RegionControlView = result.RegionControlResult

// RegionControl computes per-bucket region presence + armed-state
// classification (A/a/B/b/C/c/_) and match-aggregate state
// percentages. Returns a fully populated *result.RegionControlResult
// or, when there's nothing to compute (no regions, no two teams, no
// match window), one with Regions/TeamA/TeamB filled and no
// BucketStates/Stats.
//
// Two computations, deliberately decoupled (see classifyRegions):
//
//   - BucketStates is the display-resolution timeline — one state code
//     per windowMs (default 50) bucket. Each bucket is a point sample:
//     the classification of every player's last position with T ≤
//     bucket-start. It is a grid because it is a picture the caller
//     paints at a chosen resolution.
//   - Stats (the match-aggregate percentages and the ByPlayer tallies)
//     is the EXACT time-weighted integral over the native position
//     sample times — no grid at all. It walks the union of every
//     player's Position sample times and their RL/LG armed-interval
//     boundaries; between two consecutive events every player's
//     (region, armed) state is constant (sample-and-hold), so each
//     interval is classified once and its REAL duration (ms) is
//     accumulated per state. The result is independent of the caller's
//     windowMs and carries no quantization from an intermediate grid.
//
// The walk (both): find each player's last position sample with T ≤ the
// evaluation time (via PositionTrack.Li), look up loc → region, check
// armed via RL/LG interval membership, tally per region per team,
// classify. "Armed" means RL or LG. DEAD players are skipped — gated on
// PlayerStream.Alive since v64, NOT on Li==0, which marks an unresolved loc
// and never marked death (a corpse streams position at full rate). Samples
// with no resolved loc, and locs mapping to no region, are skipped too.
//
// All time arithmetic is integer milliseconds (schema v8); the window
// is anchored at r.Streams.Global.MatchStart.
func RegionControl(r *result.Result, opts RegionControlOptions) (*result.RegionControlResult, error) {
	if r == nil || r.Streams == nil {
		return &result.RegionControlResult{}, nil
	}

	// 1. Regions: explicit override else baked-in.
	regions := opts.Regions
	if regions == nil && r.TimelineAnalysis != nil && r.TimelineAnalysis.RegionControl != nil {
		regions = r.TimelineAnalysis.RegionControl.Regions
	}
	out := &result.RegionControlResult{Regions: regions}
	if len(regions) == 0 {
		return out, nil
	}

	// 2. Team labels: explicit override else compute from Match.Players
	//    (the canonical post-normalize scoreboard). Fall back to the
	//    baked RegionControl.TeamA/TeamB only when Match is absent.
	//
	//    Why prefer Match.Players over the baked values: in a 1v1
	//    MatchAnalyzer stamps Match.Players[].Team with per-player synthetic
	//    names ("bananfalco") instead of real team names ("red") at birth (the
	//    roster duel rewrite). Match.Players therefore already carries the
	//    canonical labels; reading them keeps the teamOf closure and
	//    teamA/teamB consistent.
	teamA, teamB := opts.TeamA, opts.TeamB
	if teamA == "" || teamB == "" {
		ta, tb := inferTeamsFromMatch(r)
		if teamA == "" {
			teamA = ta
		}
		if teamB == "" {
			teamB = tb
		}
		if (teamA == "" || teamB == "") && r.TimelineAnalysis != nil && r.TimelineAnalysis.RegionControl != nil {
			if teamA == "" {
				teamA = r.TimelineAnalysis.RegionControl.TeamA
			}
			if teamB == "" {
				teamB = r.TimelineAnalysis.RegionControl.TeamB
			}
		}
	}
	out.TeamA = teamA
	out.TeamB = teamB
	if teamA == "" || teamB == "" {
		// Not a binary-team layout — return the regions only, mirrors
		// pre-refactor behaviour.
		return out, nil
	}

	// 3. teamOf: explicit override else default closure over Match.Players
	//    (with DemoInfo fallback), stripping the "#slot" disambiguation
	//    suffix the analyzer adds for name collisions.
	teamOf := opts.TeamOf
	if teamOf == nil {
		nameToTeam := defaultNameToTeam(r)
		teamOf = func(name string) string { return nameToTeam[baseName(name)] }
	}

	// 4. Window resolution + optional sub-window.
	windowMs := opts.WindowMs
	if windowMs <= 0 {
		windowMs = 50
	}
	var startMs, endMs int32
	if opts.StartTime > 0 {
		startMs = opts.StartTime
	}
	if opts.EndTime > 0 {
		endMs = opts.EndTime
	}

	bucketStates, stats := classifyRegions(r, regions, teamA, teamB, teamOf, windowMs, startMs, endMs)
	out.BucketStates = bucketStates
	out.Stats = stats
	return out, nil
}

// inferTeamsFromMatch picks two team labels for A/B from the canonical
// Match.Players list. Preference order (mirrors the pre-refactor
// analyzer.timeline_finalize behaviour):
//
//  1. DemoInfo.Teams[0]/[1] when both names are present in
//     Match.Players. KTX's two-team layout drives that list.
//  2. Otherwise Match.Teams[0]/[1] order.
//  3. Otherwise the first two distinct teams encountered walking
//     Match.Players.
//
// Returns ("", "") when fewer than two distinct teams are present —
// the caller then short-circuits to "no binary team layout".
func inferTeamsFromMatch(r *result.Result) (string, string) {
	if r.Match == nil {
		return "", ""
	}
	// A mode with no teams has no binary side layout to control regions
	// with, and this is where that is stated. The two-participant case — a
	// duel, and a 2-player FFA, which is the same match — still resolves:
	// there the "sides" ARE the two players, which is what region control
	// has always reported on a 1v1. Anything wider did NOT bail before this
	// guard existed: an individual layout gives every player their own team
	// label, so `len(seen) < 2` below is false on a 7-player FFA and the
	// walk picked two of the seven as "sides". The guard is reached on 93
	// of 3 123 census demos (59 ffa, 20 unknown, 14 duel) and protects a
	// pass-through reached on 1 251.
	if r.Match.GameMode != nil && !r.Match.GameMode.TeamBased && len(r.Match.Players) != 2 {
		return "", ""
	}
	seen := make(map[string]struct{}, len(r.Match.Players))
	for _, p := range r.Match.Players {
		if p.Team != "" {
			seen[p.Team] = struct{}{}
		}
	}
	if len(seen) < 2 {
		return "", ""
	}

	// 1. DemoInfo.Teams ordering preference — mirrors the analyzer's
	//    pre-refactor ordering at Finalize time.
	if r.DemoInfo != nil && len(r.DemoInfo.Teams) == 2 {
		t0, t1 := r.DemoInfo.Teams[0], r.DemoInfo.Teams[1]
		if _, ok0 := seen[t0]; ok0 {
			if _, ok1 := seen[t1]; ok1 {
				return t0, t1
			}
		}
	}

	// 2. Match.Teams ordering.
	pair := make([]string, 0, 2)
	for _, t := range r.Match.Teams {
		if _, ok := seen[t.Name]; ok {
			pair = append(pair, t.Name)
			delete(seen, t.Name)
			if len(pair) == 2 {
				return pair[0], pair[1]
			}
		}
	}

	// 3. Walk-order fallback from Match.Players.
	for _, p := range r.Match.Players {
		if p.Team == "" {
			continue
		}
		if _, present := seen[p.Team]; !present {
			continue
		}
		pair = append(pair, p.Team)
		delete(seen, p.Team)
		if len(pair) == 2 {
			return pair[0], pair[1]
		}
	}
	if len(pair) >= 2 {
		return pair[0], pair[1]
	}
	return "", ""
}

// defaultNameToTeam builds a player → team map for region tallying.
// Streams.Players is the primary source because it covers every
// player with any stream data — including spectator-edge-case
// players that MatchAnalyzer filters out of Match.Players. The
// classifier walks Streams.Players, so anyone with positions needs a
// team mapping or their positions silently drop. Match.Players and
// DemoInfo.Players are folded in as backstops for names that lack a
// Streams entry.
//
// Mirrors what the pre-refactor analyzer.timeline_finalize closure
// did via slotToTeam (analyzer state held the same complete map).
func defaultNameToTeam(r *result.Result) map[string]string {
	nameToTeam := make(map[string]string)
	if r.Streams != nil {
		for _, p := range r.Streams.Players {
			if p.Name != "" && p.Team != "" {
				nameToTeam[p.Name] = p.Team
			}
		}
	}
	if r.Match != nil {
		for _, p := range r.Match.Players {
			if p.Name != "" && p.Team != "" {
				if _, ok := nameToTeam[p.Name]; !ok {
					nameToTeam[p.Name] = p.Team
				}
			}
		}
	}
	if r.DemoInfo != nil {
		for _, p := range r.DemoInfo.Players {
			if p.Name != "" && p.Team != "" {
				if _, ok := nameToTeam[p.Name]; !ok {
					nameToTeam[p.Name] = p.Team
				}
			}
		}
	}
	return nameToTeam
}

// playerTally is one player's per-region presence in the exact stats walk
// (walkRegionExact): armed/unarmed are integer milliseconds of presence
// (time-weighted). It is no longer used by the display grid.
type playerTally struct {
	team    string
	armed   int
	unarmed int
}

// regionCounts tallies armed/unarmed presence per team in one region at one
// sampling instant — the shared per-instant accumulator both walkers classify
// through classifyRegionState. One package-level type instead of the two
// identical local `counts` structs the grid and exact walkers used to declare.
type regionCounts struct{ aWpn, aNo, bWpn, bNo int }

// tally credits one present player to the team/armed bucket. team is always
// teamA or teamB — both walkers filter every other team out before sampling.
func (c *regionCounts) tally(team, teamA, teamB string, armed bool) {
	switch team {
	case teamA:
		if armed {
			c.aWpn++
		} else {
			c.aNo++
		}
	case teamB:
		if armed {
			c.bWpn++
		} else {
			c.bNo++
		}
	}
}

// playerCursor walks one player's position track monotonically, sampling the
// (region, armed) state at a non-decreasing sequence of query times. Both
// walkers advance the evaluation time forward (bucket-start / boundary), so
// sampleAt must be called with non-decreasing t. EVERY cursor it holds — the
// position sample index and the rl / lg / alive interval indices — only moves
// forward, giving an O(events) scan per player with no re-seek.
type playerCursor struct {
	pt         *result.PositionTrack
	rl, lg     []result.Interval
	alive      []result.Interval
	liToRegion map[int16]int
	sIdx       int
	holdEnd    int32 // presence stops here (result.TrackHoldEnd of the track)

	// Monotone cursors into rl / lg / alive, one per list. sampleAt is only
	// ever queried at non-decreasing t (bucket starts, or the sorted boundary
	// list), so each index only moves forward and the whole walk stays
	// O(events) per player. A linear intervalContains scan per query instead
	// made it O(events x intervals), which on a 4on4 is ~10^8 comparisons.
	rlIdx, lgIdx, aliveIdx int
}

// newPlayerCursor binds one player's track, posture intervals and canonical
// lives into a cursor. holdEnd is captured here so presence stops at the
// player's own end-of-track instead of running on indefinitely — walkRegionExact
// evaluates each interval at its LEFT endpoint, so without this bound the
// interval starting at a departed player's final sample would credit them
// everything up to the next event, which at the end of a recording is the
// whole remaining match.
func newPlayerCursor(pt *result.PositionTrack, p *result.PlayerStream, liToRegion map[int16]int) playerCursor {
	return playerCursor{
		pt:         pt,
		rl:         p.RL,
		lg:         p.LG,
		alive:      p.Alive,
		liToRegion: liToRegion,
		holdEnd:    result.TrackHoldEnd(pt.T),
	}
}

// sampleAt reports the region index and armed state of the player at time t —
// their last position sample with T ≤ t. "Armed" means RL or LG held at t.
// This is the one place the cursor-advance / liveness / Li-skip /
// region-lookup / armed-check sequence lives; both walkers call it so they
// classify identically.
//
// ok is false — the player contributes nothing at t — when:
//
//   - they have no sample at/before t (they had not appeared yet);
//   - they are DEAD at t. Liveness comes from PlayerStream.Alive, the
//     canonical lives list. It is NOT inferable from the samples: a dead
//     player keeps broadcasting position at full rate (mvdsv writes
//     svc_playerinfo for every cs_spawned client, sv_demo.c:1481-1519) and on
//     a gib the player entity IS the bouncing head (ktx/src/player.c:1070
//     ThrowHead), so before this gate a corpse's travels were tallied as
//     regional presence — and as ARMED presence, since StatItems weapon bits
//     do not clear until respawn. An earlier revision of this comment claimed
//     Li==0 marked dead players; it does not, and never did;
//   - t is at or past their end-of-track (result.TrackHoldEnd). Sample-and-hold
//     has no staleness bound of its own, and walkRegionExact evaluates each
//     interval at its LEFT endpoint, so without this an early quitter would be
//     credited everything from their final sample to the next event — the whole
//     remaining match when the recording ends before the match window does;
//   - the sample resolved no loc (Li==0), or the loc maps to no region.
func (c *playerCursor) sampleAt(t int32) (regionIdx int, armed, ok bool) {
	pt := c.pt
	for c.sIdx+1 < len(pt.T) && pt.T[c.sIdx+1] <= t {
		c.sIdx++
	}
	if pt.T[c.sIdx] > t || t >= c.holdEnd {
		return 0, false, false
	}
	// The last sample's evidence has expired: the player's location between
	// here and their next sample is unknown, so credit it to nobody. >= not >,
	// so the expiry boundary at sample+cap is itself rejected rather than
	// being credited the whole remaining hole; the credited window is
	// [sample, sample+cap).
	if t-pt.T[c.sIdx] >= result.SampleStaleCapMs {
		return 0, false, false
	}
	if c.alive != nil && !advanceContains(c.alive, &c.aliveIdx, t) {
		// Liveness was measured and says dead. A NIL list means it was not
		// measurable, and the honest response to "unknown" is to degrade
		// rather than to drop — gating everything off would blank a demo's
		// whole region-control panel. On any demo through the normal pipeline
		// Alive is always derived, so the nil path is hand-assembled Results.
		return 0, false, false
	}
	li := pt.Li[c.sIdx]
	if li == 0 {
		return 0, false, false
	}
	ri, found := c.liToRegion[li]
	if !found {
		return 0, false, false
	}
	armedRL := advanceContains(c.rl, &c.rlIdx, t)
	armedLG := advanceContains(c.lg, &c.lgIdx, t)
	return ri, armedRL || armedLG, true
}

// advanceContains reports whether t falls inside one of a player's sorted,
// non-overlapping intervals, advancing *idx forward as it goes. Valid only for
// non-decreasing t — the contract playerCursor already documents. Mirrors
// analyzer.makeInside; the two exist separately only because the packages
// cannot import each other.
func advanceContains(ivs []result.Interval, idx *int, t int32) bool {
	for *idx < len(ivs) && ivs[*idx].End <= t {
		*idx++
	}
	return *idx < len(ivs) && ivs[*idx].Start <= t && t < ivs[*idx].End
}

// classifyRegions is the region-control walker — formerly
// analyzer.ComputeRegionControl. Kept private; callers go through
// RegionControl, which resolves defaults from the Result.
//
// It produces two things over the same (sub-)window:
//
//   - the display grid at the caller's windowMs (walkRegionGrid), which
//     produces the BucketStates timeline (one point-sampled state code
//     per bucket);
//   - the exact time-weighted Stats (walkRegionExact), which produces
//     the aggregate percentages and the ByPlayer millisecond tallies.
//
// The two are decoupled on purpose. A grid makes the aggregate a
// sampling artifact: because each bucket is classified from one position
// sample at bucket-start, a fight that starts and ends between two
// bucket-starts can be missed, so a region could report empty:100 even
// though it was contested — and the "match-aggregate" would then change
// with the display resolution. The stats walk instead integrates over
// the native position sample times themselves (and the RL/LG armed
// boundaries), so it sees every state change exactly and the result is
// independent of windowMs. An earlier revision approximated this with a
// fixed native 50ms stats grid; that intermediate is gone — the walk is
// now exact.
//
// optsStartMs / optsEndMs clamp both to the same sub-window; 0 means
// "no override" (use MatchStart / MatchEnd respectively).
func classifyRegions(
	r *result.Result,
	regions []result.ControlRegion,
	teamA, teamB string,
	teamOf func(playerName string) string,
	windowMs int,
	optsStartMs, optsEndMs int32,
) (map[string]string, map[string]result.RegionStats) {
	if r == nil || r.Streams == nil || len(regions) == 0 {
		return nil, nil
	}

	var locTable []string
	if r.TimelineAnalysis != nil {
		locTable = r.TimelineAnalysis.LocTable
	}

	// regionByLoc: lower-cased loc name → region name (case-insensitive
	// matching, same as the on-disk regions JSON loader).
	regionByLoc := make(map[string]string)
	for _, rg := range regions {
		for _, ln := range rg.Locs {
			regionByLoc[strings.ToLower(ln)] = rg.Name
		}
	}
	if len(regionByLoc) == 0 {
		return nil, nil
	}

	// Pre-resolve each region's loc-index set to its region INDEX so the inner
	// loop (in playerCursor.sampleAt) is an integer hashtable lookup, not a
	// string-lower per sample. Built once here and shared by both walkers.
	regionIdx := make(map[string]int, len(regions))
	for i, rg := range regions {
		regionIdx[rg.Name] = i
	}
	liToRegion := make(map[int16]int, len(regionByLoc))
	for li, name := range locTable {
		if rn, ok := regionByLoc[strings.ToLower(name)]; ok {
			liToRegion[int16(li)] = regionIdx[rn]
		}
	}
	if len(liToRegion) == 0 {
		return nil, nil
	}

	// Grid window is anchored at MatchStart (always 0 on the
	// match-relative clock every producer stamps at Finalize). The
	// optional sub-window clamps to a tighter range; default behaviour
	// (both opts zero) covers the full match. It applies identically to
	// both grids below.
	matchStart := r.Streams.Global.MatchStart
	matchEnd := r.Streams.Global.MatchEnd
	gridStart := matchStart
	gridEnd := matchEnd
	if optsStartMs > gridStart {
		gridStart = optsStartMs
	}
	if optsEndMs > 0 && optsEndMs < gridEnd {
		gridEnd = optsEndMs
	}
	if gridEnd <= gridStart {
		return nil, nil
	}

	// Stats: exact time-weighted aggregate over native sample times,
	// independent of windowMs — computed UNCONDITIONALLY so a coarse display
	// windowMs whose grid rounds to zero buckets (a sub-window narrower than
	// windowMs) never suppresses the windowMs-independent stats.
	stats := walkRegionExact(r, regions, liToRegion, teamA, teamB, teamOf, gridStart, gridEnd)

	// Display grid: BucketStates at the caller's windowMs. Only when the grid
	// walk yields buckets; nil (omitted) otherwise.
	var bucketStates map[string]string
	if stateBuf := walkRegionGrid(r, regions, liToRegion, teamA, teamB, teamOf, int32(windowMs), gridStart, gridEnd); stateBuf != nil {
		bucketStates = make(map[string]string, len(regions))
		for _, rg := range regions {
			bucketStates[rg.Name] = string(stateBuf[rg.Name])
		}
	}
	return bucketStates, stats
}

// walkRegionExact computes the exact time-weighted region-control Stats
// over [gridStart, gridEnd) — the match-aggregate percentages plus the
// per-player presence in integer milliseconds. There is no grid: the
// event timeline is the union of every relevant player's Position sample
// times and their RL/LG armed-interval boundaries, clipped to the
// window. Between two consecutive events every player's (region, armed)
// state is constant (sample-and-hold — the same assumption the bucket
// walk makes at each bucket-start), so each interval is classified once
// with classifyRegionState and its real duration (t1 − t0 ms) is added
// to the region's per-state total; the player's own presence tally gets
// the same duration split by armed. Percentages are state-duration /
// total-window-duration, one decimal (same rounding as the old grid).
//
// Edge handling mirrors walkRegionGrid: a player contributes nothing
// before their first sample (last sample with T ≤ the interval start
// must exist); Li==0 and unmapped locs contribute nothing; the opening
// interval [gridStart, first event) uses each player's last sample ≤
// gridStart when they have one.
//
// Cost is O(events × players) for the interval scan plus O(events ×
// regions) for the per-interval classify; per-player cursors advance
// monotonically, so no quadratic blowup. Returns nil only for a zero-length
// window; an empty roster still yields every region at Empty:100.
// mergeUniqueClipped merges ascending int32 lists into one sorted,
// deduped slice of the values strictly inside (lo, hi). A list that
// turns out not to be ascending (defensive — track times always are) is
// sorted on a copy first so the merge stays correct.
func mergeUniqueClipped(lists [][]int32, lo, hi int32) []int32 {
	for i, l := range lists {
		for j := 1; j < len(l); j++ {
			if l[j] < l[j-1] {
				cp := append([]int32(nil), l...)
				sort.Slice(cp, func(a, b int) bool { return cp[a] < cp[b] })
				lists[i] = cp
				break
			}
		}
	}
	idx := make([]int, len(lists))
	total := 0
	for i, l := range lists {
		idx[i] = sort.Search(len(l), func(k int) bool { return l[k] > lo })
		total += len(l) - idx[i]
	}
	out := make([]int32, 0, total)
	for {
		bi := -1
		var best int32
		for i, l := range lists {
			if idx[i] < len(l) {
				if v := l[idx[i]]; bi < 0 || v < best {
					best, bi = v, i
				}
			}
		}
		if bi < 0 || best >= hi {
			break
		}
		if n := len(out); n == 0 || out[n-1] != best {
			out = append(out, best)
		}
		idx[bi]++
	}
	return out
}

func walkRegionExact(
	r *result.Result,
	regions []result.ControlRegion,
	liToRegion map[int16]int,
	teamA, teamB string,
	teamOf func(playerName string) string,
	gridStart, gridEnd int32,
) map[string]result.RegionStats {
	total := int64(gridEnd - gridStart)
	if total <= 0 {
		return nil
	}

	nR := len(regions)

	// Collect the relevant players and the interior event times (strictly
	// inside the window; gridStart/gridEnd bracket them below).
	type exactPlayer struct {
		name   string
		team   string
		cursor playerCursor
	}
	var players []exactPlayer
	// Interior events: each player's position track is already ascending,
	// so the union is a k-way merge of those tracks with the (small,
	// sorted) list of interval endpoints — no hash set, no full sort.
	var eventLists [][]int32
	var extraEvts []int32
	addEvt := func(t int32) {
		if t > gridStart && t < gridEnd {
			extraEvts = append(extraEvts, t)
		}
	}
	for i := range r.Streams.Players {
		p := &r.Streams.Players[i]
		team := teamOf(p.Name)
		if team == "" || (team != teamA && team != teamB) {
			continue
		}
		pt := p.Position
		if pt == nil || len(pt.T) == 0 || len(pt.Li) != len(pt.T) {
			continue
		}
		players = append(players, exactPlayer{
			name:   p.Name,
			team:   team,
			cursor: newPlayerCursor(pt, p, liToRegion),
		})
		eventLists = append(eventLists, pt.T)
		for _, iv := range p.RL {
			addEvt(iv.Start)
			addEvt(iv.End)
		}
		for _, iv := range p.LG {
			addEvt(iv.Start)
			addEvt(iv.End)
		}
		// Life boundaries split an interval too: a player who dies partway
		// between two position samples stops contributing at the death, not at
		// the next sample. Without these the walk would credit the remainder
		// of the straddling interval to a corpse.
		for _, iv := range p.Alive {
			addEvt(iv.Start)
			addEvt(iv.End)
		}
		// The player's own end-of-track is a boundary too, so the interval
		// that starts at their final sample is truncated there instead of
		// running to whatever the next global event happens to be.
		addEvt(result.TrackHoldEnd(pt.T))
		// A sample's evidence expires SampleStaleCapMs after it. On a POV
		// recording only players in the recorder's PVS are written, so the
		// others have multi-second holes; without these boundaries the walk
		// would credit each hole in full to wherever the player was last seen.
		for _, t := range result.SampleStaleBoundaries(pt.T) {
			addEvt(t)
		}
	}
	// No early return on len(players)==0: with no roster-mapped player the
	// boundary slice degenerates to [gridStart, gridEnd], one interval
	// classifies every region '_', and Empty accumulates to 100% — the v58
	// empty-roster semantics (total>0 is already guarded above, so no
	// division by zero).

	sort.Slice(extraEvts, func(i, j int) bool { return extraEvts[i] < extraEvts[j] })
	eventLists = append(eventLists, extraEvts)
	interior := mergeUniqueClipped(eventLists, gridStart, gridEnd)

	// Boundaries: gridStart, all interior events (strictly increasing,
	// unique), gridEnd. Every [boundaries[k], boundaries[k+1]) is an
	// interval of constant state.
	boundaries := make([]int32, 0, len(interior)+2)
	boundaries = append(boundaries, gridStart)
	boundaries = append(boundaries, interior...)
	boundaries = append(boundaries, gridEnd)

	// Per-region accumulated state durations (ms) and per-player presence.
	type durs struct {
		empty, aCtl, aWeak, contested, weakContested, bCtl, bWeak int64
	}
	dur := make([]durs, nR)
	byPlayer := make([]map[string]*playerTally, nR)
	for i := range byPlayer {
		byPlayer[i] = make(map[string]*playerTally)
	}

	cnt := make([]regionCounts, nR)

	for k := 0; k+1 < len(boundaries); k++ {
		t0 := boundaries[k]
		t1 := boundaries[k+1]
		d := int64(t1 - t0)
		if d <= 0 {
			continue
		}
		for i := range cnt {
			cnt[i] = regionCounts{}
		}
		for pi := range players {
			pl := &players[pi]
			ri, armed, ok := pl.cursor.sampleAt(t0)
			if !ok {
				continue
			}
			cnt[ri].tally(pl.team, teamA, teamB, armed)
			tally := byPlayer[ri][pl.name]
			if tally == nil {
				tally = &playerTally{team: pl.team}
				byPlayer[ri][pl.name] = tally
			}
			if armed {
				tally.armed += int(d)
			} else {
				tally.unarmed += int(d)
			}
		}
		for ri := 0; ri < nR; ri++ {
			c := cnt[ri]
			switch classifyRegionState(c.aWpn, c.aNo, c.bWpn, c.bNo) {
			case RegionStateEmpty:
				dur[ri].empty += d
			case RegionStateTeamAControl:
				dur[ri].aCtl += d
			case RegionStateTeamAWeakControl:
				dur[ri].aWeak += d
			case RegionStateContested:
				dur[ri].contested += d
			case RegionStateWeakContested:
				dur[ri].weakContested += d
			case RegionStateTeamBControl:
				dur[ri].bCtl += d
			case RegionStateTeamBWeakControl:
				dur[ri].bWeak += d
			}
		}
	}

	pct := func(n int64) float64 { return float64(int(float64(n)/float64(total)*1000+0.5)) / 10 }
	stats := make(map[string]result.RegionStats, nR)
	for i, rg := range regions {
		d := dur[i]
		rgStats := result.RegionStats{
			TeamAControl:     pct(d.aCtl),
			TeamAWeakControl: pct(d.aWeak),
			Contested:        pct(d.contested),
			WeakContested:    pct(d.weakContested),
			Empty:            pct(d.empty),
			TeamBWeakControl: pct(d.bWeak),
			TeamBControl:     pct(d.bCtl),
		}
		if pm := byPlayer[i]; len(pm) > 0 {
			rgStats.ByPlayer = make(map[string]result.RegionPlayerStats, len(pm))
			for name, pt := range pm {
				rgStats.ByPlayer[name] = result.RegionPlayerStats{
					Team:    pt.team,
					Armed:   pt.armed,
					Unarmed: pt.unarmed,
				}
			}
		}
		stats[rg.Name] = rgStats
	}
	return stats
}

// walkRegionGrid classifies one bucket grid at bucketDurMs resolution
// over [gridStart, gridEnd). For each bucket it point-samples every
// player's position at bucket-start (their last sample with T ≤
// bucket-start), tallies team presence + armed state per region, and
// classifies the bucket. Returns the per-region state-code buffers, or
// nil when the window yields no buckets (a sub-window narrower than
// bucketDurMs rounds to zero).
//
// classifyRegions calls this once, at the display windowMs, to build
// BucketStates only; the aggregate Stats come from walkRegionExact.
func walkRegionGrid(
	r *result.Result,
	regions []result.ControlRegion,
	liToRegion map[int16]int,
	teamA, teamB string,
	teamOf func(playerName string) string,
	bucketDurMs int32,
	gridStart, gridEnd int32,
) map[string][]byte {
	// Round-half-up integer division.
	nBuckets := int((gridEnd - gridStart + bucketDurMs/2) / bucketDurMs)
	if nBuckets <= 0 {
		return nil
	}

	nR := len(regions)
	presence := make([][]regionCounts, nBuckets)
	for i := range presence {
		presence[i] = make([]regionCounts, nR)
	}

	// Per-player walk: for each bucket sample the player's region + armed
	// state at bucket-start (their last position sample with T ≤ bucket-start).
	for i := range r.Streams.Players {
		p := &r.Streams.Players[i]
		team := teamOf(p.Name)
		if team == "" || (team != teamA && team != teamB) {
			continue
		}
		pt := p.Position
		if pt == nil || len(pt.T) == 0 || len(pt.Li) != len(pt.T) {
			continue
		}
		cur := newPlayerCursor(pt, p, liToRegion)
		for bi := 0; bi < nBuckets; bi++ {
			bucketStart := gridStart + int32(bi)*bucketDurMs
			ri, armed, ok := cur.sampleAt(bucketStart)
			if !ok {
				continue
			}
			presence[bi][ri].tally(team, teamA, teamB, armed)
		}
	}

	// Classify per bucket per region.
	stateBuf := make(map[string][]byte, nR)
	for _, rg := range regions {
		stateBuf[rg.Name] = make([]byte, 0, nBuckets)
	}
	for bi := 0; bi < nBuckets; bi++ {
		for ri, rg := range regions {
			c := presence[bi][ri]
			state := classifyRegionState(c.aWpn, c.aNo, c.bWpn, c.bNo)
			stateBuf[rg.Name] = append(stateBuf[rg.Name], state)
		}
	}

	return stateBuf
}

// classifyRegionState is the seven-state decision rule. Faithful port
// of mvd-web/static/app.js: classifyRegionState.
func classifyRegionState(aWpn, aNo, bWpn, bNo int) byte {
	aT := aWpn + aNo
	bT := bWpn + bNo
	if aT == 0 && bT == 0 {
		return RegionStateEmpty
	}
	if aT > 0 && bT == 0 {
		if aWpn > 0 {
			return RegionStateTeamAControl
		}
		return RegionStateTeamAWeakControl
	}
	if bT > 0 && aT == 0 {
		if bWpn > 0 {
			return RegionStateTeamBControl
		}
		return RegionStateTeamBWeakControl
	}
	// Both teams present.
	if aWpn > 0 && bWpn == 0 {
		return RegionStateTeamAControl
	}
	if bWpn > 0 && aWpn == 0 {
		return RegionStateTeamBControl
	}
	if aWpn > 0 && bWpn > 0 {
		return RegionStateContested
	}
	return RegionStateWeakContested
}

// KnownRegionModes is the closed vocabulary for ShapeRegions, in the order
// the docs list it; the openapi enum is pinned to the same set.
var KnownRegionModes = []string{"full", "summary", "none"}

// ShapeRegions returns a copy of rc whose Regions list is trimmed to mode:
// "full" (default) keeps the polygons, "summary" drops each region's Points
// (~6 KB) while keeping name/locs/centroids, "none" omits the list entirely.
// The stored Result's slice is never mutated — the copy is the point.
func ShapeRegions(rc *result.RegionControlResult, mode string) *result.RegionControlResult {
	if rc == nil {
		return nil
	}
	body := *rc
	switch strings.ToLower(mode) {
	case "summary":
		slim := make([]result.ControlRegion, len(rc.Regions))
		for i, rg := range rc.Regions {
			rg.Points = nil
			slim[i] = rg
		}
		body.Regions = slim
	case "none":
		body.Regions = nil
	}
	return &body
}
