package analyzer

import (
	"math"
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/bspvis"
	"github.com/mvd-analyzer/mvd-analytics/mapbsp"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Line-of-sight geometry. These mirror the standard Quake player hull used by
// mapclip (mins {-16,-16,-24}, maxs {16,16,32}); the eye sits +22 above the
// origin and the box midpoint is at +4 (half of -24..+32).
const (
	losEyeOffsetZ = 22.0 // looker eye = origin + (0,0,22)
	losBoxMidZ    = 4.0  // bbox midpoint target = origin + (0,0,4)
)

var (
	losBoxMin = [3]float32{-16, -16, -24}
	losBoxMax = [3]float32{16, 16, 32}
)

// ComputeLOS computes per-ordered-pair line-of-sight intervals against the
// map's visibility BSP and writes them to Streams.Players[].LOS (schema v37).
//
// It is computed lazily, NOT during the default parse: LOS is the heaviest
// position-derived pass (N² pairs × samples × rays) and has no in-pipeline
// consumer, so the registry no longer runs it. Callers invoke it on demand —
// the web map's LOS overlay (WASM), `qw-analyze -include los`, the mvd-api
// `/los` endpoint — and it is idempotent: the first call sets Streams.LOSComputed
// (gob-persisted), later calls return immediately, so a map with no BSP is
// attempted once and not retried.
//
// It must be called only after the times are match-relative (the default
// pipeline's normalizeMatchRelativeTimes has run by the time any Result is
// handed out), so positions, spawns/deaths and the mover poses share one epoch
// and the emitted intervals need no further normalization. It loads its own
// visibility BSP (the same two cheap calls timeline_finalize.go makes); no-op
// (LOS simply absent) when the map has no provisioned BSP — mirroring the
// PositionTrack.H/Lq gate — or when there are fewer than two players.
func ComputeLOS(res *Result) {
	if res == nil || res.Streams == nil {
		return
	}
	if res.Streams.LOSComputed {
		return // idempotent — already computed (possibly to "no LOS")
	}
	res.Streams.LOSComputed = true
	if res.DemoInfo == nil || res.DemoInfo.Map == "" {
		return
	}
	players := res.Streams.Players
	if len(players) < 2 {
		return
	}
	data := mapbsp.LoadBytes(res.DemoInfo.Map)
	if data == nil {
		return
	}
	vb, err := bspvis.LoadBytes(data)
	if err != nil || vb == nil {
		return
	}

	matchEnd := res.Streams.Global.MatchEnd
	movers := buildLosMovers(res.Streams.Movers)
	// PVS rows are per-leaf and immutable, so cache them across every looker.
	pvsCache := make(map[int][]byte)
	// Each player's bounding-box leaf trail is independent of who is looking,
	// so resolve it once (one tree descent per sample) and reuse it as the PVS
	// cull target for every looker — the per-pair inner loop then only does
	// cheap bit tests, never a descent.
	trails := buildLeafTrails(vb, players)

	// One looker at a time: A→B for every opponent B is computed in a single
	// walk of A's samples, so A's eye leaf / PVS row and the mover snapshot are
	// resolved once per sample rather than once per pair. A→B and B→A are
	// independent (LOS is asymmetric), so each looker is handled on its own.
	for ai := range players {
		if players[ai].Position == nil || len(players[ai].Position.T) == 0 {
			continue
		}
		if tracks := losForLooker(vb, players, ai, movers, matchEnd, pvsCache, trails); len(tracks) > 0 {
			players[ai].LOS = tracks
		}
	}
}

// leafTrail is one player's per-sample bounding-box leaf set in CSR layout:
// sample i touches leaves[offs[i]:offs[i+1]]. Built once and shared across all
// lookers as the PVS cull target.
type leafTrail struct {
	leaves []int32
	offs   []int32
}

// buildLeafTrails resolves every player's box-leaf trail (one BoxLeafs descent
// per sample). A nil Position yields an empty trail.
func buildLeafTrails(vb *bspvis.BSP, players []result.PlayerStream) []leafTrail {
	trails := make([]leafTrail, len(players))
	var scratch []int
	for pi := range players {
		pt := players[pi].Position
		if pt == nil || len(pt.T) == 0 {
			continue
		}
		n := len(pt.T)
		tr := leafTrail{offs: make([]int32, n+1)}
		for i := 0; i < n; i++ {
			ox, oy, oz := pt.X[i], pt.Y[i], pt.Z[i]
			scratch = vb.BoxLeafs(
				[3]float32{ox + losBoxMin[0], oy + losBoxMin[1], oz + losBoxMin[2]},
				[3]float32{ox + losBoxMax[0], oy + losBoxMax[1], oz + losBoxMax[2]},
				scratch)
			for _, lf := range scratch {
				tr.leaves = append(tr.leaves, int32(lf))
			}
			tr.offs[i+1] = int32(len(tr.leaves))
		}
		trails[pi] = tr
	}
	return trails
}

// losForLooker computes looker ai's line-of-sight onto every other player,
// returning one LosTrack per opponent ever seen (Other = opponent index). It
// walks ai's own samples once; at each sample it resolves the eye leaf + PVS
// row (the cheap cull that skips the 9 raycasts when an opponent's leaf isn't
// potentially visible) and the active-mover snapshot, both shared across all
// opponents at that time.
//
// Mover cursors are reset here, so the caller may reuse one mover slice across
// lookers. No memoization across samples: a mover sweeping between a stationary
// pair changes LOS even when neither player moves.
func losForLooker(vb *bspvis.BSP, players []result.PlayerStream, ai int, movers []losMover, matchEnd int32, pvsCache map[int][]byte, trails []leafTrail) []result.LosTrack {
	a := &players[ai]
	ap := a.Position
	n := len(players)

	open := make([]bool, n)
	start := make([]int32, n)
	bcur := make([]int, n)
	out := make([][]result.Interval, n)

	for mi := range movers {
		movers[mi].cursor = 0
	}
	var scratch []posedMover

	for i, t := range ap.T {
		aAlive := losAliveAt(a.Spawns, a.Deaths, t)
		var eye [3]float32
		var pvsRow []byte
		moversReady := false
		if aAlive {
			eye = [3]float32{ap.X[i], ap.Y[i], ap.Z[i] + losEyeOffsetZ}
			aLeaf := vb.PointInLeaf(eye)
			if row, ok := pvsCache[aLeaf]; ok {
				pvsRow = row
			} else {
				pvsRow = vb.LeafPVS(aLeaf)
				pvsCache[aLeaf] = pvsRow
			}
		}

		for bi := 0; bi < n; bi++ {
			if bi == ai {
				continue
			}
			bp := players[bi].Position
			if bp == nil || len(bp.T) == 0 {
				continue
			}
			visible := false
			if aAlive {
				for bcur[bi]+1 < len(bp.T) && bp.T[bcur[bi]+1] <= t {
					bcur[bi]++
				}
				if bj := bcur[bi]; bp.T[bj] <= t && losAliveAt(players[bi].Spawns, players[bi].Deaths, t) {
					// PVS cull against the leaves the opponent's bounding box
					// touches (precomputed). The box contains all 9 target
					// points, so if none of its leaves is potentially visible no
					// ray can reach a target — a strict superset of the raycast
					// test (lossless) that skips the rays for the common
					// different-room pair with only bit tests.
					tr := &trails[bi]
					if pvsAllowsLeaves(vb, pvsRow, tr.leaves[tr.offs[bj]:tr.offs[bj+1]]) {
						var tg [9][3]float32
						losTargets(bp.X[bj], bp.Y[bj], bp.Z[bj], &tg)
						if !moversReady {
							scratch = activeMoversAt(movers, t, scratch)
							moversReady = true
						}
						visible = losSeesTargets(vb, eye, &tg, scratch)
					}
				}
			}
			if visible && !open[bi] {
				open[bi], start[bi] = true, t
			} else if !visible && open[bi] {
				open[bi] = false
				out[bi] = append(out[bi], result.Interval{Start: start[bi], End: t})
			}
		}
	}

	// Close still-open intervals at the looker's last observed sample (or match
	// end), never inventing sight past the looker's own track.
	end := matchEnd
	if m := len(ap.T); m > 0 && ap.T[m-1] < end {
		end = ap.T[m-1]
	}
	var tracks []result.LosTrack
	for bi := 0; bi < n; bi++ {
		if bi == ai {
			continue
		}
		if open[bi] {
			e := end
			if e < start[bi] {
				e = start[bi]
			}
			out[bi] = append(out[bi], result.Interval{Start: start[bi], End: e})
		}
		if len(out[bi]) > 0 {
			tracks = append(tracks, result.LosTrack{Other: int16(bi), Iv: out[bi]})
		}
	}
	return tracks
}

// losMover is one mover's pose timeline plus a forward-only cursor, prepared
// once for the whole run; losForLooker resets the cursors per looker (it walks
// the looker's samples in ascending time, so the cursor only advances).
type losMover struct {
	modelIdx int32
	t        []int32
	x, y, z  []float32
	vis      []bool
	cursor   int
}

// posedMover is a mover resolved to a single pose at one sample time.
type posedMover struct {
	modelIdx int32
	origin   [3]float32
}

// buildLosMovers prepares the per-run mover list from Streams.Movers, dropping
// movers with no pose or a non-positive SubModel (SubModel is the "*N" BSP
// model index; N>=1 for a real inline brush model).
func buildLosMovers(streams []result.MoverStream) []losMover {
	if len(streams) == 0 {
		return nil
	}
	out := make([]losMover, 0, len(streams))
	for i := range streams {
		m := &streams[i]
		if len(m.T) == 0 || m.SubModel <= 0 {
			continue
		}
		out = append(out, losMover{
			modelIdx: int32(m.SubModel),
			t:        m.T,
			x:        m.X,
			y:        m.Y,
			z:        m.Z,
			vis:      m.Vis,
		})
	}
	return out
}

// activeMoversAt advances each mover's cursor to the last pose with T<=t and
// appends the visible ones (their world origin) to dst, which is reused across
// samples. Must be called with non-decreasing t within one pair.
func activeMoversAt(movers []losMover, t int32, dst []posedMover) []posedMover {
	dst = dst[:0]
	for mi := range movers {
		m := &movers[mi]
		for m.cursor+1 < len(m.t) && m.t[m.cursor+1] <= t {
			m.cursor++
		}
		if len(m.t) == 0 || m.t[m.cursor] > t {
			continue // no pose yet at this time
		}
		if m.cursor < len(m.vis) && !m.vis[m.cursor] {
			continue // not drawn (entity removed) — not solid
		}
		dst = append(dst, posedMover{
			modelIdx: m.modelIdx,
			origin:   [3]float32{m.x[m.cursor], m.y[m.cursor], m.z[m.cursor]},
		})
	}
	return dst
}

// computeLosAB returns the half-open [Start,End) intervals during which looker
// a had a clear sightline to target b. It is a single-pair wrapper over
// losForLooker (the production path handles every opponent in one pass); kept
// for direct unit testing.
func computeLosAB(vb *bspvis.BSP, a, b *result.PlayerStream, movers []losMover, matchEnd int32) []result.Interval {
	two := []result.PlayerStream{*a, *b}
	trails := buildLeafTrails(vb, two)
	for _, tr := range losForLooker(vb, two, 0, movers, matchEnd, make(map[int][]byte), trails) {
		if tr.Other == 1 {
			return tr.Iv
		}
	}
	return nil
}

// losSeesTargets reports whether the looker at eye can reach any of the 9
// precomputed target points (8 bbox corners + midpoint) unblocked — early-out
// on the first clear ray.
func losSeesTargets(vb *bspvis.BSP, eye [3]float32, tg *[9][3]float32, movers []posedMover) bool {
	for i := range tg {
		if !rayBlocked(vb, eye, tg[i], movers) {
			return true
		}
	}
	return false
}

// pvsAllowsLeaves reports whether any leaf in the list is potentially visible
// from the looker's PVS row. A nil row (map without vis data) never culls.
// Early-out on the first potentially-visible leaf.
func pvsAllowsLeaves(vb *bspvis.BSP, pvsRow []byte, leaves []int32) bool {
	if pvsRow == nil {
		return true
	}
	for _, lf := range leaves {
		if vb.PVSContains(pvsRow, int(lf)) {
			return true
		}
	}
	return false
}

// rayBlocked reports whether segment a->c is blocked by worldspawn solid or
// any active mover posed in the way.
func rayBlocked(vb *bspvis.BSP, a, c [3]float32, movers []posedMover) bool {
	if vb.RayHitsSolid(a, c) {
		return true
	}
	for i := range movers {
		if vb.RayHitsSolidModel(movers[i].modelIdx, movers[i].origin, a, c) {
			return true
		}
	}
	return false
}

// losTargets fills dst with the box midpoint + the 8 bounding-box corners of a
// player at origin (ox,oy,oz). The midpoint is index 0 on purpose: it is the
// body point least likely to clip a wall edge, so testing it first lets a
// visible pair early-out on one ray instead of probing corners. The set is
// what matters (any-clear-ray); the order only affects the early-out.
func losTargets(ox, oy, oz float32, dst *[9][3]float32) {
	dst[0] = [3]float32{ox, oy, oz + losBoxMidZ}
	i := 1
	for _, dx := range [2]float32{losBoxMin[0], losBoxMax[0]} {
		for _, dy := range [2]float32{losBoxMin[1], losBoxMax[1]} {
			for _, dz := range [2]float32{losBoxMin[2], losBoxMax[2]} {
				dst[i] = [3]float32{ox + dx, oy + dy, oz + dz}
				i++
			}
		}
	}
}

// losAliveAt reports whether a player is alive at match-relative time t, given
// their ascending Spawns/Deaths streams: alive from the most recent spawn at
// or before t until the next death. With no spawns recorded (POV demos can
// omit them) the player is treated as spawned since the start — positions only
// exist in-world, so the death gate still closes LOS correctly. Binary search
// keeps this O(log n) since it runs once per sample-pair.
func losAliveAt(spawns, deaths []int32, t int32) bool {
	// Most recent spawn at or before t.
	si := sort.Search(len(spawns), func(i int) bool { return spawns[i] > t })
	if len(spawns) > 0 && si == 0 {
		return false // spawns recorded, but all after t → not yet alive
	}
	sp := int32(math.MinInt32)
	if si > 0 {
		sp = spawns[si-1]
	}
	// Any death in (sp, t] means dead: the first death after sp, if it is <= t.
	di := sort.Search(len(deaths), func(i int) bool { return deaths[i] > sp })
	return di >= len(deaths) || deaths[di] > t
}
