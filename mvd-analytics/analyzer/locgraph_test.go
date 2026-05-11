package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/qwanalytics/result"
)

// makePlayerStream builds a synthetic PlayerStream from a per-bucket
// (time, x, y, locIndex, dead, spawn) sequence. Used by the locgraph
// tests below — BuildLocGraph internally derives 50 ms buckets from
// streams via view.Buckets, so the test scaffolding has to provide
// streams shaped to produce those bucket frames.
type bucketSpec struct {
	t  float64
	x  int32
	y  int32
	li int16
	d  bool
	sp bool
}

func makePlayerStreamFromBuckets(name, team string, specs []bucketSpec) result.PlayerStream {
	ps := result.PlayerStream{Name: name, Team: team}
	pt := &result.PositionTrack{}
	healthCur := int16(0)
	for _, s := range specs {
		// Position: append every sample (no dedup).
		pt.T = append(pt.T, float32(s.t))
		pt.X = append(pt.X, s.x)
		pt.Y = append(pt.Y, s.y)
		pt.Z = append(pt.Z, 0)
		pt.Li = append(pt.Li, s.li)

		// Loc: dedup against last value.
		if len(ps.Loc) == 0 || ps.Loc[len(ps.Loc)-1].V != s.li {
			ps.Loc = append(ps.Loc, result.ChangeI16{T: s.t, V: s.li})
		}

		// Health: 100 by default; dead frames go to 0; spawn back to 100.
		want := int16(100)
		if s.d {
			want = 0
		}
		if want != healthCur {
			ps.Health = append(ps.Health, result.ChangeI16{T: s.t, V: want})
			healthCur = want
		}

		// Spawn / death timestamps.
		if s.sp {
			ps.Spawns = append(ps.Spawns, s.t)
		}
		if s.d {
			ps.Deaths = append(ps.Deaths, s.t)
		}
	}
	if len(pt.T) > 0 {
		ps.Position = pt
	}
	return ps
}

// Build a synthetic Result with two players and verify BuildLocGraph
// produces the expected nodes and edges, including teleport
// classification.
func TestBuildLocGraph_BasicTransitionsAndTeleport(t *testing.T) {
	locTable := []string{"", "A", "B", "C"}
	locationData := []MapLocation{
		{Name: "A", X: 0, Y: 0},
		{Name: "B", X: 100, Y: 0},
		{Name: "C", X: 5000, Y: 0},
	}

	// p1 (red): in A from t=0 to t=0.05, B from t=0.10 to t=0.15, C
	// (teleport) from t=0.20 to t=0.25. p2 (blue) stays in A.
	p1Specs := []bucketSpec{
		{t: 0.00, x: 1, y: 0, li: 1},
		{t: 0.05, x: 50, y: 0, li: 1},
		{t: 0.10, x: 98, y: 0, li: 2},
		{t: 0.15, x: 100, y: 0, li: 2},
		{t: 0.20, x: 5000, y: 0, li: 3},
		{t: 0.25, x: 5002, y: 0, li: 3},
	}
	p2Specs := []bucketSpec{
		{t: 0.00, x: 10, y: 10, li: 1},
		{t: 0.05, x: 10, y: 10, li: 1},
		{t: 0.10, x: 10, y: 10, li: 1},
		{t: 0.15, x: 10, y: 10, li: 1},
		{t: 0.20, x: 10, y: 10, li: 1},
		{t: 0.25, x: 10, y: 10, li: 1},
	}

	res := &Result{
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 0.30},
			Players: []result.PlayerStream{
				makePlayerStreamFromBuckets("p1", "red", p1Specs),
				makePlayerStreamFromBuckets("p2", "blue", p2Specs),
			},
		},
		TimelineAnalysis: &TimelineAnalysisResult{
			LocTable:     locTable,
			LocationData: locationData,
		},
		DemoInfo: &DemoInfoResult{
			Players: []DemoInfoPlayer{
				{Name: "p1", Team: "red"},
				{Name: "p2", Team: "blue"},
			},
		},
	}

	graph := BuildLocGraph(res)
	if graph == nil {
		t.Fatal("expected graph, got nil")
	}

	nodes := map[string]LocNode{}
	for _, n := range graph.Locs {
		nodes[n.Name] = n
	}

	// p1 visits A 2 buckets, B 2 buckets, C 2 buckets; p2 visits A 6
	// buckets. So node-time A=8, B=2, C=2 in 50 ms units.
	const D = 0.05
	if got := nodes["A"].Total; !approxEq(got, 8*D) {
		t.Errorf("A total = %v, want %v", got, 8*D)
	}
	if got := nodes["B"].Total; !approxEq(got, 2*D) {
		t.Errorf("B total = %v, want %v", got, 2*D)
	}
	if got := nodes["C"].Total; !approxEq(got, 2*D) {
		t.Errorf("C total = %v, want %v", got, 2*D)
	}
	if got := nodes["A"].ByTeam["blue"]; !approxEq(got, 6*D) {
		t.Errorf("A byTeam[blue] = %v, want %v", got, 6*D)
	}

	if nodes["C"].X != 5000 {
		t.Errorf("C.X = %v, want 5000", nodes["C"].X)
	}

	edges := map[string]LocEdge{}
	for _, e := range graph.Edges {
		edges[e.From+"→"+e.To] = e
	}

	ab := edges["A→B"]
	if ab.Total != 1 || ab.Kind != "normal" {
		t.Errorf("A→B = %+v, want total=1 kind=normal", ab)
	}
	if ab.ByPlayer["p1"] != 1 || ab.ByTeam["red"] != 1 {
		t.Errorf("A→B breakdown = %+v", ab)
	}
	bc := edges["B→C"]
	if bc.Total != 1 || bc.Kind != "teleport" {
		t.Errorf("B→C = %+v, want total=1 kind=teleport", bc)
	}
	if _, exists := edges["A→C"]; exists {
		t.Errorf("unexpected A→C edge: %+v", edges["A→C"])
	}
}

func approxEq(a, b float64) bool {
	const eps = 1e-6 // float32-time roundtrip in PositionTrack accumulates ~1e-7
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < eps
}
