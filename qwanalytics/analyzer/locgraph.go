package analyzer

import (
	"sort"

	"github.com/mvd-analyzer/qwanalytics/view"
)

// LocGraphResult / LocNode / LocEdge now live in qwanalytics/result and
// are re-exported via type aliases in interface.go. BuildLocGraph below
// constructs and returns them; nothing else in this file declares them.
//
// At schema v7 BuildLocGraph derives its 50 ms bucket array from
// result.Streams via view.Buckets and the legacy shim — the loc graph
// algorithm wants a uniform-grid view of "where was each player every
// 50 ms" and that's exactly what view.Buckets provides. The blip-
// filter smoothing has already happened upstream in
// TimelineAnalyzer.applyBlipFilter so streams.Loc carries the same
// smoothed track every v6 consumer used.

// teleportBaseThreshold is the per-axis "max plausible movement per
// second" limit. A transition whose per-axis displacement exceeds
// bucketDuration * teleportBaseThreshold in the single bucket where
// the loc changed is classified as a teleport. Mirrors the frontend
// constant at app.js (MAX_MOVE_PER_BUCKET = 2500 * bucketDuration).
const teleportBaseThreshold = 2500.0

// BuildLocGraph derives a 50 ms bucket array from result.Streams and
// aggregates it into a loc-to-loc movement graph. Runs after time
// normalization / warmup filtering so it sees only match-time data.
// Returns nil if there is no timeline data or no streams.
func BuildLocGraph(result *Result) *LocGraphResult {
	if result == nil || result.TimelineAnalysis == nil || result.Streams == nil {
		return nil
	}
	ta := result.TimelineAnalysis
	bv, err := view.Buckets(result, view.BucketsOptions{
		WindowMs:    50,
		Fields:      view.AllStandardFields,
		Reducers:    view.LegacyReducerSet,
		IncludeTeam: false,
	})
	if err != nil || bv == nil || len(bv.Buckets) == 0 {
		return nil
	}
	highResBuckets := view.ToLegacyHighResBuckets(bv)
	if len(highResBuckets) == 0 {
		return nil
	}

	teamByName := make(map[string]string)
	if result.DemoInfo != nil {
		for _, p := range result.DemoInfo.Players {
			if p.Name != "" && p.Team != "" {
				teamByName[p.Name] = p.Team
			}
		}
	}

	resolveLoc := func(li int) string {
		if li > 0 && li < len(ta.LocTable) {
			return ta.LocTable[li]
		}
		return ""
	}

	bucketDuration := float64(bv.WindowMs) / 1000.0
	if bucketDuration <= 0 {
		bucketDuration = 0.05
	}
	teleportThreshold := float32(bucketDuration * teleportBaseThreshold)

	// Per-player cursor: just the last loc and the position at the
	// start of that residence, used for teleport classification on
	// the next change. Deaths / spawns arrive as authoritative p.D /
	// p.Sp bucket flags driven by DeathEvent / SpawnEvent at the
	// parser layer, so instant-respawn cases never need a sideways
	// fallback onto FragResult here.
	type playerCursor struct {
		loc          string
		lastX, lastY float32
		seen         bool
	}
	cursors := make(map[string]*playerCursor)

	nodes := make(map[string]*LocNode)
	// FIXME: edges are keyed only by (from, to); if both a "normal" and a
	// "teleport" transition occur between the same pair of locs they
	// collapse into whichever kind was seen first. Future: key edges by
	// (from, to, kind).
	edgeKey := func(from, to string) string { return from + "\x00" + to }
	edges := make(map[string]*LocEdge)

	ensureNode := func(name string) *LocNode {
		n := nodes[name]
		if n == nil {
			n = &LocNode{Name: name, ByPlayer: make(map[string]float64)}
			nodes[name] = n
		}
		return n
	}
	ensureEdge := func(from, to, kind string) *LocEdge {
		k := edgeKey(from, to)
		e := edges[k]
		if e == nil {
			e = &LocEdge{From: from, To: to, Kind: kind, ByPlayer: make(map[string]int)}
			edges[k] = e
		}
		return e
	}

	resetCursor := func(cur *playerCursor) {
		cur.loc = ""
	}

	for _, bucket := range highResBuckets {
		seenThisBucket := make(map[string]bool, len(bucket.P))

		for name, p := range bucket.P {
			if p == nil {
				continue
			}
			seenThisBucket[name] = true
			cur := cursors[name]
			if cur == nil {
				cur = &playerCursor{}
				cursors[name] = cur
			}

			invalidPos := p.X == 0 && p.Y == 0
			if invalidPos || p.Sp || p.D {
				resetCursor(cur)
				cur.seen = true
				continue
			}

			locName := resolveLoc(p.Li)
			team := teamByName[name]

			// Node time: every bucket counts toward its (filtered)
			// loc. The blip filter already redistributed bleed time
			// to the correct neighbor, so a naive per-bucket
			// accumulation here is the right accounting.
			if locName != "" {
				node := ensureNode(locName)
				node.Total += bucketDuration
				node.ByPlayer[name] += bucketDuration
				if team != "" {
					if node.ByTeam == nil {
						node.ByTeam = make(map[string]float64)
					}
					node.ByTeam[team] += bucketDuration
				}
			}

			if locName == "" {
				cur.seen = true
				continue
			}

			if cur.loc == "" {
				// First sample after a reset — seed without emitting.
				cur.loc = locName
				cur.lastX = p.X
				cur.lastY = p.Y
			} else if locName != cur.loc {
				// Filtered loc just changed — emit one edge.
				dx := p.X - cur.lastX
				if dx < 0 {
					dx = -dx
				}
				dy := p.Y - cur.lastY
				if dy < 0 {
					dy = -dy
				}
				disp := dx
				if dy > disp {
					disp = dy
				}
				kind := "normal"
				if disp > teleportThreshold {
					kind = "teleport"
				}
				edge := ensureEdge(cur.loc, locName, kind)
				edge.Total++
				edge.ByPlayer[name]++
				if team != "" {
					if edge.ByTeam == nil {
						edge.ByTeam = make(map[string]int)
					}
					edge.ByTeam[team]++
				}
				cur.loc = locName
				cur.lastX = p.X
				cur.lastY = p.Y
			} else {
				// Same loc — refresh position so teleport
				// classification on the next transition uses the
				// latest in-loc sample, not the entry point.
				cur.lastX = p.X
				cur.lastY = p.Y
			}

			cur.seen = true
		}

		// Reset cursor for players who were previously seen but are
		// absent from this bucket — matches tracks.go dropout
		// handling.
		for name, cur := range cursors {
			if cur.seen && !seenThisBucket[name] {
				resetCursor(cur)
				cur.seen = false
			}
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
	// Sort by stable keys so the result is deterministic across runs.
	// Without this the slices reflect Go map iteration order and the
	// golden test corpus would flap on every invocation.
	sort.Slice(out.Locs, func(i, j int) bool { return out.Locs[i].Name < out.Locs[j].Name })
	sort.Slice(out.Edges, func(i, j int) bool {
		if out.Edges[i].From != out.Edges[j].From {
			return out.Edges[i].From < out.Edges[j].From
		}
		return out.Edges[i].To < out.Edges[j].To
	})
	return out
}
