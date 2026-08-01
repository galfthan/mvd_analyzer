package analyzer

import (
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// LocGraphResult / LocNode / LocEdge now live in qwanalytics/result and
// are re-exported via type aliases in interface.go. BuildLocGraph below
// constructs and returns them; nothing else in this file declares them.
//
// BuildLocGraph walks each player's PositionTrack natively. Since schema v64
// the two halves are computed differently, because they measure different
// things:
//
//   - NODE TIME is an exact time-weighted integral over the union of the
//     player's position-sample times and their RL/LG/quad/pent/alive interval
//     endpoints. Between consecutive boundaries the state is constant by
//     sample-and-hold, so each interval is classified once and its real
//     duration accumulates. This is the same construction view.walkRegionExact
//     uses, which is what lets loc totals and region totals reconcile exactly
//     (occupancy_reconcile_test.go). It replaced a forward difference clamped
//     to 50 ms.
//   - EDGES are transition COUNTS, so they stay a per-sample walk with a
//     displacement check for teleports.
//
// Both halves are gated on PlayerStream.Alive. Dead players keep streaming
// position at full rate and, on a gib, the player entity is thrown across the
// map as the head, so ungated both halves credited a corpse's travels — as
// presence, as armed presence, and as movement edges. Spawn / death timestamps
// additionally reset the edge cursor so a death-then-respawn never produces a
// spurious edge across the gap.

// teleportBaseThreshold is the per-axis "max plausible movement per
// second" limit. A transition whose per-axis displacement exceeds
// bucketDuration * teleportBaseThreshold in the single sample where
// the loc changed is classified as a teleport. Mirrors the frontend
// constant at app.js (MAX_MOVE_PER_BUCKET = 2500 * bucketDuration).
const teleportBaseThreshold = 2500.0

// locgraphTeleportMaxGapMs bounds the sample gap across which a loc change is
// classified at all. The MVD sample cadence is NOT fixed — mvdsv gates whole
// demo frames on sv_demofps (default 30, sv_demoIdlefps 10 while idle/paused;
// mvdsv/src/sv_send.c:1339-1346), quantised up to the server tick, so measured
// cadence across the golden corpus is bimodal: ~13-16 ms on servers at full
// tick and ~34-39 ms on servers left at the default. The displacement bound is
// therefore scaled by the REAL delta between the two samples rather than an
// assumed one. Across a gap longer than this the two samples bracket a stall
// (packet loss, a slot handover) and no displacement claim is meaningful, so
// the transition is left classified "normal" rather than invented as a
// teleport. Shares velGapCapMs' rationale (timeline_streams.go).
const locgraphTeleportMaxGapMs int32 = 250

// makeAliveGate returns the liveness predicate for a player.
//
// A NIL Alive list means liveness was not measurable (PlayerStream.Alive's
// documented three-state contract), and the honest response to "unknown" is to
// degrade rather than to drop — zeroing a player's whole loc graph because we
// could not tell when they were alive would be worse than the corpse-counting
// bug this gate exists to fix. So nil gates nothing. An EMPTY non-nil list is
// a measurement — "never alive in the window" — and correctly gates everything.
//
// On any demo through the normal pipeline Alive is always derived
// (deriveAliveIntervals in timeline_finalize.go), so this path is reached only
// by hand-assembled Results.
func makeAliveGate(alive []Interval) func(int32) bool {
	if alive == nil {
		return func(int32) bool { return true }
	}
	return makeInside(alive)
}

// makeInside returns a predicate reporting whether time t falls inside any of
// a player's sorted, non-overlapping presence intervals. It advances an
// internal cursor, so it is only valid for queries at monotonically
// non-decreasing t — which is how both walks below query it.
func makeInside(ivs []Interval) func(int32) bool {
	idx := 0
	return func(t int32) bool {
		for idx < len(ivs) && ivs[idx].End <= t {
			idx++
		}
		return idx < len(ivs) && ivs[idx].Start <= t && t < ivs[idx].End
	}
}

// collectBoundaries returns the sorted, deduped instants at which one
// player's (loc, posture, liveness) state can change, bracketed by [lo, hi]:
// every position sample time plus every interval endpoint, clipped to the
// window. Between two consecutive boundaries the state is constant by
// sample-and-hold, which is what makes the walk an exact integral rather than
// a grid approximation. Mirrors the event-union in view.walkRegionExact.
func collectBoundaries(lo, hi int32, sampleT []int32, extra []int32, ivs ...[]Interval) []int32 {
	set := make(map[int32]struct{}, len(sampleT)+16)
	add := func(t int32) {
		if t > lo && t < hi {
			set[t] = struct{}{}
		}
	}
	for _, t := range sampleT {
		add(t)
	}
	for _, t := range extra {
		add(t)
	}
	for _, iv := range ivs {
		for _, v := range iv {
			add(v.Start)
			add(v.End)
		}
	}
	out := make([]int32, 0, len(set)+2)
	out = append(out, lo)
	for t := range set {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return append(out, hi)
}

// locCursor samples one player's (loc index, posture, liveness) at a
// non-decreasing sequence of query times. Every internal cursor only moves
// forward, so a whole walk is O(events) with no re-seek — the same contract as
// view.playerCursor, and the reason sampleAt must be called with
// non-decreasing t.
type locCursor struct {
	pt                      *PositionTrack
	sIdx                    int
	inRL, inLG              func(int32) bool
	inQuad, inPent, inAlive func(int32) bool
}

func newLocCursor(pt *PositionTrack, rl, lg, quad, pent, alive []Interval) *locCursor {
	return &locCursor{
		pt:      pt,
		inRL:    makeInside(rl),
		inLG:    makeInside(lg),
		inQuad:  makeInside(quad),
		inPent:  makeInside(pent),
		inAlive: makeAliveGate(alive),
	}
}

// sampleAt reports the player's state at t from their last position sample
// with T <= t. ok is false — the player contributes no time at t — when they
// have no sample at/before t, when that sample resolved no loc (Li == 0), or
// when they are DEAD. Liveness is read from Alive, the canonical lives list:
// a corpse (or, on a gib, the bouncing head) keeps broadcasting position, so
// samples alone say nothing about whether the player was alive.
func (c *locCursor) sampleAt(t int32) (li int16, armed, quad, pent, ok bool) {
	for c.sIdx+1 < len(c.pt.T) && c.pt.T[c.sIdx+1] <= t {
		c.sIdx++
	}
	if c.pt.T[c.sIdx] > t {
		return 0, false, false, false, false
	}
	// The last sample's evidence has expired — see result.SampleStaleCapMs.
	// >= not >: the expiry boundary SampleStaleBoundaries emits sits at
	// sample+cap, so a strict > would let that exact boundary through and
	// credit the whole remaining hole to it. The credited window is
	// [sample, sample+cap).
	if t-c.pt.T[c.sIdx] >= result.SampleStaleCapMs {
		return 0, false, false, false, false
	}
	if !c.inAlive(t) {
		return 0, false, false, false, false
	}
	li = c.pt.Li[c.sIdx]
	if li == 0 {
		return 0, false, false, false, false
	}
	// Evaluate every predicate (no short-circuit) so each cursor tracks t.
	rl, lg := c.inRL(t), c.inLG(t)
	return li, rl || lg, c.inQuad(t), c.inPent(t), true
}

// BuildLocGraph aggregates each player's native-rate PositionTrack
// into a loc-to-loc movement graph. Runs after time normalization /
// warmup filtering so it sees only match-time data. Returns nil if
// streams are absent.
func BuildLocGraph(res *Result) *LocGraphResult {
	if res == nil || res.TimelineAnalysis == nil || res.Streams == nil {
		return nil
	}
	ta := res.TimelineAnalysis
	if len(ta.LocTable) == 0 {
		return nil
	}

	teamByName := make(map[string]string)
	if res.DemoInfo != nil {
		for _, p := range res.DemoInfo.Players {
			if p.Name != "" && p.Team != "" {
				teamByName[p.Name] = p.Team
			}
		}
	}

	resolveLoc := func(li int16) string {
		if li > 0 && int(li) < len(ta.LocTable) {
			return ta.LocTable[li]
		}
		return ""
	}

	// Match window: the same [MatchStart, MatchEnd] every other exact walk
	// clamps to (cf. view.classifyRegions). Node time is integrated over this
	// window only, so warmup and post-match samples cannot leak in.
	winStart, winEnd := int32(0), int32(0)
	if res.Streams != nil {
		winStart, winEnd = res.Streams.Global.MatchStart, res.Streams.Global.MatchEnd
	}

	nodes := make(map[string]*LocNode)
	edges := make(map[string]*LocEdge)
	edgeKey := func(from, to string) string { return from + "\x00" + to }

	ensureNode := func(name string) *LocNode {
		n := nodes[name]
		if n == nil {
			n = &LocNode{Name: name, ByPlayer: make(map[string]int32)}
			nodes[name] = n
		}
		return n
	}
	// NOTE (pre-existing): Kind is keyed on (from,to) only and is set by the
	// FIRST transition to create the edge, so an edge's Total is not "that many
	// teleports" — it is one loc pair whose first observed transition happened
	// to trip the displacement bound, plus every ordinary transition after it.
	// Scaling the bound by the real inter-sample gap (v64) makes which
	// transition arrives first slightly more load-bearing, but the aggregate
	// was already approximate. Fixing it means keying the edge on kind too,
	// which changes the edge set — deliberately out of scope here.
	ensureEdge := func(from, to, kind string) *LocEdge {
		k := edgeKey(from, to)
		e := edges[k]
		if e == nil {
			e = &LocEdge{From: from, To: to, Kind: kind, ByPlayer: make(map[string]int)}
			edges[k] = e
		}
		return e
	}

	// addWeight folds dt (int32 ms) into a conditioned node LocWeights
	// (RL/LG-armed or quad), lazily allocating it so locs the condition
	// never touched stay nil (omitempty in JSON).
	addWeight := func(w **LocWeights, player, team string, dt int32) {
		if *w == nil {
			*w = &LocWeights{ByPlayer: make(map[string]int32)}
		}
		(*w).Total += dt
		(*w).ByPlayer[player] += dt
		if team != "" {
			if (*w).ByTeam == nil {
				(*w).ByTeam = make(map[string]int32)
			}
			(*w).ByTeam[team] += dt
		}
	}

	// addEdgeWeight is the transition-count analogue of addWeight for a
	// conditioned LocEdgeWeights.
	addEdgeWeight := func(w **LocEdgeWeights, player, team string) {
		if *w == nil {
			*w = &LocEdgeWeights{ByPlayer: make(map[string]int)}
		}
		(*w).Total++
		(*w).ByPlayer[player]++
		if team != "" {
			if (*w).ByTeam == nil {
				(*w).ByTeam = make(map[string]int)
			}
			(*w).ByTeam[team]++
		}
	}

	for _, p := range res.Streams.Players {
		pt := p.Position
		if pt == nil || len(pt.T) == 0 || len(pt.Li) != len(pt.T) {
			continue
		}
		team := teamByName[p.Name]

		// ── Node time: the exact time-weighted integral ───────────────────
		//
		// Residence is integrated over the union of every event that can
		// change the player's (loc, posture, liveness) state — their position
		// sample times plus the endpoints of their RL/LG/Quad/Pent/Alive
		// intervals — clamped to the match window AND to their own first/last
		// sample. Between two consecutive boundaries the state is constant by
		// sample-and-hold, so each interval is classified once and its REAL
		// duration accumulates. Same construction as view.walkRegionExact, so
		// loc totals and region totals reconcile exactly (see
		// occupancy_reconcile_test.go).
		//
		// This replaces a forward difference clamped to 50 ms. That clamp was
		// doing TWO jobs, and the split matters:
		//
		//   - Its stated job was stopping "a death-induced 5 s gap" inflating
		//     a loc — which it never did, because there is no such gap. Dead
		//     players keep streaming position at full rate (mvdsv writes
		//     svc_playerinfo for every cs_spawned client, sv_demo.c:1481-1519)
		//     and on a gib the player entity IS the bouncing head
		//     (ktx/src/player.c:1070 ThrowHead), so the clamp was in fact
		//     crediting a corpse's travels as presence — armed presence, since
		//     StatItems weapon bits do not clear until respawn. That job is now
		//     done properly by the Alive gate.
		//   - Its unstated job was bounding sample-and-hold across a genuine
		//     hole in the track. On a POV recording only players inside the
		//     recorder's PVS get svc_playerinfo, so everyone else has
		//     multi-second gaps and holding across them is pure invention. That
		//     job is now done by result.SampleStaleCapMs, cadence-independently
		//     rather than at a fixed 50 ms.
		//
		// Bounding by the player's own last sample is what stops an early
		// quitter's final loc being credited through to match end.
		{
			lo, hi := pt.T[0], result.TrackHoldEnd(pt.T)
			if winStart > lo {
				lo = winStart
			}
			if winEnd > 0 && winEnd < hi {
				hi = winEnd
			}
			if hi > lo {
				bounds := collectBoundaries(lo, hi, pt.T, result.SampleStaleBoundaries(pt.T), p.RL, p.LG, p.Quad, p.Pent, p.Alive)
				cur := newLocCursor(pt, p.RL, p.LG, p.Quad, p.Pent, p.Alive)
				for k := 0; k+1 < len(bounds); k++ {
					t0, t1 := bounds[k], bounds[k+1]
					dt := t1 - t0
					if dt <= 0 {
						continue
					}
					li, armed, quad, pent, ok := cur.sampleAt(t0)
					if !ok {
						continue
					}
					locName := resolveLoc(li)
					if locName == "" {
						continue
					}
					node := ensureNode(locName)
					node.Total += dt
					node.ByPlayer[p.Name] += dt
					if team != "" {
						if node.ByTeam == nil {
							node.ByTeam = make(map[string]int32)
						}
						node.ByTeam[team] += dt
					}
					if armed {
						addWeight(&node.Armed, p.Name, team, dt)
					} else {
						addWeight(&node.Unarmed, p.Name, team, dt)
					}
					if quad {
						addWeight(&node.Quad, p.Name, team, dt)
					}
					if pent {
						addWeight(&node.Pent, p.Name, team, dt)
					}
				}
			}
		}

		// ── Edges: transition COUNTS, walked per sample ───────────────────
		//
		// Edges are counts, not time, so they stay a per-sample walk. They are
		// alive-gated too: a corpse or a gib head bouncing from one loc to the
		// next is not movement, and a long bounce trips the displacement bound
		// and invents a phantom "teleport" edge.
		boundaries := mergeBoundaries(p.Spawns, p.Deaths)
		bIdx := 0
		insideRL := makeInside(p.RL)
		insideLG := makeInside(p.LG)
		insideQuad := makeInside(p.Quad)
		insidePent := makeInside(p.Pent)
		insideAlive := makeAliveGate(p.Alive)
		// Per-player cursor: tracks the loc + position of the last
		// sample we counted. Reset at boundary crossings (death/spawn),
		// at gaps in the loc track (Li=0), and while dead.
		var (
			curLoc   string
			curX     float32
			curY     float32
			curT     int32
			havePrev bool
		)

		for i := range pt.T {
			t := pt.T[i] // int32 ms
			// Cross any boundaries we've passed; reset cursor. Both
			// sides are int32 ms — comparison is exact (this is the
			// site where float roundtrip previously produced spurious
			// teleport edges across gib-respawn boundaries).
			for bIdx < len(boundaries) && boundaries[bIdx] <= t {
				havePrev = false
				bIdx++
			}

			li := pt.Li[i]
			if li == 0 {
				havePrev = false
				continue
			}
			locName := resolveLoc(li)
			if locName == "" {
				havePrev = false
				continue
			}
			if !insideAlive(t) {
				havePrev = false
				continue
			}

			x := pt.X[i]
			y := pt.Y[i]

			// Conditioned metrics: this sample's combat posture, used for
			// the transition it may trigger. Evaluate all predicates (no
			// short-circuit) so each cursor tracks t independently.
			rl, lg, quad, pent := insideRL(t), insideLG(t), insideQuad(t), insidePent(t)
			armed := rl || lg

			if !havePrev {
				curLoc = locName
				curX = x
				curY = y
				curT = t
				havePrev = true
				continue
			}
			if locName != curLoc {
				dx := x - curX
				if dx < 0 {
					dx = -dx
				}
				dy := y - curY
				if dy < 0 {
					dy = -dy
				}
				disp := dx
				if dy > disp {
					disp = dy
				}
				// Scale the per-second displacement bound by the REAL gap to
				// the previous sample. A fixed 50 ms assumption misclassifies
				// in both directions once the cadence is not 50 ms: at ~13 ms
				// it is 4x too permissive, and at ~39 ms (a third of the
				// corpus) legitimate 2000 ups movement lands within 1.6x of
				// the bound. Across an abnormally long gap no displacement
				// claim is meaningful, so leave it "normal".
				kind := "normal"
				if gap := t - curT; gap > 0 && gap <= locgraphTeleportMaxGapMs {
					bound := float32(float64(gap) / 1000.0 * teleportBaseThreshold)
					if disp > bound {
						kind = "teleport"
					}
				}
				edge := ensureEdge(curLoc, locName, kind)
				edge.Total++
				edge.ByPlayer[p.Name]++
				if team != "" {
					if edge.ByTeam == nil {
						edge.ByTeam = make(map[string]int)
					}
					edge.ByTeam[team]++
				}
				// Condition the transition on the destination sample's
				// posture so each metric yields a self-contained movement
				// graph (armed/quad edges + nodes).
				if armed {
					addEdgeWeight(&edge.Armed, p.Name, team)
				} else {
					addEdgeWeight(&edge.Unarmed, p.Name, team)
				}
				if quad {
					addEdgeWeight(&edge.Quad, p.Name, team)
				}
				if pent {
					addEdgeWeight(&edge.Pent, p.Name, team)
				}
				curLoc = locName
			}
			curX = x
			curY = y
			curT = t
		}
	}

	// Attach world coordinates from LocationData where available.
	coordByName := make(map[string]MapLocation, len(ta.LocationData))
	for _, loc := range ta.LocationData {
		if _, exists := coordByName[loc.Name]; !exists {
			coordByName[loc.Name] = loc
		}
	}

	out := &LocGraphResult{
		Locs:  make([]LocNode, 0, len(nodes)),
		Edges: make([]LocEdge, 0, len(edges)),
	}
	for _, n := range nodes {
		if c, ok := coordByName[n.Name]; ok {
			n.X, n.Y, n.Z = c.X, c.Y, c.Z
		}
		out.Locs = append(out.Locs, *n)
	}
	for _, e := range edges {
		out.Edges = append(out.Edges, *e)
	}
	sort.Slice(out.Locs, func(i, j int) bool { return out.Locs[i].Name < out.Locs[j].Name })
	sort.Slice(out.Edges, func(i, j int) bool {
		if out.Edges[i].From != out.Edges[j].From {
			return out.Edges[i].From < out.Edges[j].From
		}
		return out.Edges[i].To < out.Edges[j].To
	})
	return out
}
