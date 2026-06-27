package analyzer

import (
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
	// Each player's body leaf (the leaf its origin sits in) is independent of who
	// is looking, so resolve it once per sample and reuse it across every looker
	// — the per-pair inner loop is then a single PVS bit test, never a tree
	// descent. This point-to-point leaf is both the pvs metric and the gate that
	// decides which pairs get the expensive LOS raycast (see losForLooker).
	bodyLeaves := buildBodyLeaves(vb, players)

	// One looker at a time: A→B for every opponent B is computed in a single
	// walk of A's samples, so A's eye leaf / PVS row and the mover snapshot are
	// resolved once per sample rather than once per pair. A→B and B→A are
	// independent (LOS is asymmetric), so each looker is handled on its own.
	for ai := range players {
		if players[ai].Position == nil || len(players[ai].Position.T) == 0 {
			continue
		}
		los, pvs := losForLooker(vb, players, ai, movers, matchEnd, pvsCache, bodyLeaves)
		if len(los) > 0 {
			players[ai].LOS = los
		}
		if len(pvs) > 0 {
			players[ai].PVS = pvs
		}
	}
}

// visAccum collects per-opponent half-open visibility intervals from a boolean
// sampled in ascending looker time: open/start hold the in-progress interval
// per opponent and out the closed ones. Both the LOS (raycast) and PVS
// (potentially-visible) passes feed one of these, so the interval bookkeeping
// stays identical between the two metrics.
type visAccum struct {
	open  []bool
	start []int32
	out   [][]result.Interval
}

func newVisAccum(n int) visAccum {
	return visAccum{open: make([]bool, n), start: make([]int32, n), out: make([][]result.Interval, n)}
}

// sample records opponent bi's visibility at looker time t, opening an interval
// on a rising edge and closing it on a falling one.
func (ac *visAccum) sample(bi int, visible bool, t int32) {
	if visible && !ac.open[bi] {
		ac.open[bi], ac.start[bi] = true, t
	} else if !visible && ac.open[bi] {
		ac.open[bi] = false
		ac.out[bi] = append(ac.out[bi], result.Interval{Start: ac.start[bi], End: t})
	}
}

// tracks closes any still-open interval at end (never before its own start) and
// returns one LosTrack per opponent ever seen, skipping the looker ai itself.
func (ac *visAccum) tracks(ai int, end int32) []result.LosTrack {
	var tracks []result.LosTrack
	for bi := range ac.open {
		if bi == ai {
			continue
		}
		if ac.open[bi] {
			e := end
			if e < ac.start[bi] {
				e = ac.start[bi]
			}
			ac.out[bi] = append(ac.out[bi], result.Interval{Start: ac.start[bi], End: e})
		}
		if len(ac.out[bi]) > 0 {
			tracks = append(tracks, result.LosTrack{Other: int16(bi), Iv: ac.out[bi]})
		}
	}
	return tracks
}

// buildBodyLeaves resolves every player's per-sample body leaf (PointInLeaf of
// the origin) — the point-to-point PVS target, shared across lookers. The body
// origin is the LOS box centre; testing this single leaf (rather than the whole
// box trail) is what makes the PVS gate selective. A body centre that lands in
// solid (a wall-clip) is stored as -1 so it is never counted potentially
// visible: a solid leaf is leaf 0 / the solid sink, for which a raw PVS bit test
// is unconditionally true. A nil Position yields a nil row.
func buildBodyLeaves(vb *bspvis.BSP, players []result.PlayerStream) [][]int32 {
	out := make([][]int32, len(players))
	for pi := range players {
		pt := players[pi].Position
		if pt == nil || len(pt.T) == 0 {
			continue
		}
		leaves := make([]int32, len(pt.T))
		for i := range pt.T {
			l := vb.PointInLeaf([3]float32{pt.X[i], pt.Y[i], pt.Z[i]})
			if l <= 0 || vb.LeafContents(l) == bspvis.ContentsSolid {
				leaves[i] = -1
			} else {
				leaves[i] = int32(l)
			}
		}
		out[pi] = leaves
	}
	return out
}

// losForLooker computes looker ai's visibility onto every other player in one
// walk of ai's own samples, returning two parallel metrics, each one LosTrack
// per opponent ever seen (Other = opponent index), in two stages:
//
//   - pvs: the opponent is potentially visible — a point-to-point PVS test, the
//     looker's eye leaf vs the opponent's single body leaf (bodyLeaves), the
//     same notion the loc filter uses (locvis). Both a reported metric and the
//     gate for stage two.
//   - los: a clear raycast sightline — the 9 eye→body rays, cast ONLY for pairs
//     the pvs gate passed. So los ⊆ pvs by construction, and the gap between
//     them (potentially visible, no clear ray) is the occlusion-tolerant signal.
//
// Gating the raycast on the point PVS (rather than the opponent's full
// bounding-box leaf set) is what makes this cheap: the gate is a single bit test
// and passes only ~1/4 of alive pairs on typical maps, so most pairs never
// raycast. The box-leaf cull it replaces was both pricier (a BoxLeafs descent
// per sample) and — because the player box dips into the floor's solid leaf 0,
// for which a PVS bit test is unconditionally true — barely culled at all. The
// point gate drops a vanishing 0.05% of true sightlines (a body centre occluded
// while a corner peeks through a portal), within the noise of an already
// 9-point eye-height LOS approximation.
//
// At each sample it resolves the eye leaf + PVS row and the active-mover
// snapshot once, sharing them across all opponents at that time.
//
// Mover cursors are reset here, so the caller may reuse one mover slice across
// lookers. No memoization across samples: a mover sweeping between a stationary
// pair changes LOS even when neither player moves.
func losForLooker(vb *bspvis.BSP, players []result.PlayerStream, ai int, movers []losMover, matchEnd int32, pvsCache map[int][]byte, bodyLeaves [][]int32) (los, pvs []result.LosTrack) {
	a := &players[ai]
	ap := a.Position
	n := len(players)

	losAcc := newVisAccum(n)
	pvsAcc := newVisAccum(n)
	bcur := make([]int, n)

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
			visible := false    // clear raycast sightline
			potVisible := false // opponent in the looker's PVS
			if aAlive {
				for bcur[bi]+1 < len(bp.T) && bp.T[bcur[bi]+1] <= t {
					bcur[bi]++
				}
				if bj := bcur[bi]; bp.T[bj] <= t && losAliveAt(players[bi].Spawns, players[bi].Deaths, t) {
					// Stage 1 — PVS gate (and the pvs metric): point-to-point, is
					// the opponent's body leaf in the looker's PVS row. A solid
					// body centre is stored as -1 (never potentially visible).
					if bl := bodyLeaves[bi][bj]; bl > 0 && vb.PVSContains(pvsRow, int(bl)) {
						potVisible = true
						// Stage 2 — LOS: cast the 9 eye→body rays only for pairs
						// the gate passed, so los ⊆ pvs by construction.
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
			losAcc.sample(bi, visible, t)
			pvsAcc.sample(bi, potVisible, t)
		}
	}

	// Close still-open intervals at the looker's last observed sample (or match
	// end), never inventing sight past the looker's own track.
	end := matchEnd
	if m := len(ap.T); m > 0 && ap.T[m-1] < end {
		end = ap.T[m-1]
	}
	return losAcc.tracks(ai, end), pvsAcc.tracks(ai, end)
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
	bodyLeaves := buildBodyLeaves(vb, two)
	los, _ := losForLooker(vb, two, 0, movers, matchEnd, make(map[int][]byte), bodyLeaves)
	for _, tr := range los {
		if tr.Other == 1 {
			return tr.Iv
		}
	}
	return nil
}

// computePvsAB is the pvs-metric counterpart of computeLosAB: the half-open
// intervals during which looker a had target b in its potentially-visible set
// (before the raycast narrows it to actual line of sight). Kept for direct unit
// testing.
func computePvsAB(vb *bspvis.BSP, a, b *result.PlayerStream, movers []losMover, matchEnd int32) []result.Interval {
	two := []result.PlayerStream{*a, *b}
	bodyLeaves := buildBodyLeaves(vb, two)
	_, pvs := losForLooker(vb, two, 0, movers, matchEnd, make(map[int][]byte), bodyLeaves)
	for _, tr := range pvs {
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
// their ascending Spawns/Deaths streams.
//
// A player is alive from match start and stays alive until a death; each death
// begins a dead period that the next spawn ends. So liveness is decided by the
// most recent event at or before t: a death ⇒ dead, a spawn ⇒ alive, neither
// ⇒ alive (spawned at match start). Crucially this does NOT require a recorded
// match-start spawn — KTX demos emit the first spawn only on the first
// *respawn* (the spawn that starts a life follows the death that ended the
// previous one), so a player's first recorded spawn is typically a minute+ in.
// Keying off "most recent spawn" instead would wrongly mark everyone dead until
// their first respawn and erase all early-match line of sight. This mirrors the
// liveness semantics of view.playerActiveInWindow (the bucket view's canonical
// alive test) — keep the two in sync. Binary search keeps this O(log n) since
// it runs once per sample-pair.
func losAliveAt(spawns, deaths []int32, t int32) bool {
	di := sort.Search(len(deaths), func(i int) bool { return deaths[i] > t })
	if di == 0 {
		return true // no death yet ⇒ alive since match start
	}
	lastDeath := deaths[di-1]
	si := sort.Search(len(spawns), func(i int) bool { return spawns[i] > t })
	// Alive iff a spawn at or before t is more recent than that last death.
	return si > 0 && spawns[si-1] > lastDeath
}
