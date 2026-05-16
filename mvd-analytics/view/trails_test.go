package view

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func TestLocTrailsBasic(t *testing.T) {
	r := &result.Result{
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 10000},
			Players: []result.PlayerStream{
				{
					Name: "p1",
					Loc: []result.ChangeI16{
						{T: 0, V: 1},    // start in loc "rl"
						{T: 3000, V: 2}, // move to "ya"
						{T: 7000, V: 1}, // back to "rl"
					},
				},
			},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{
			LocTable: []string{"", "rl", "ya"},
		},
	}
	v, err := LocTrails(r, LocTrailsOptions{})
	if err != nil {
		t.Fatalf("LocTrails: %v", err)
	}
	if len(v.Players) != 1 {
		t.Fatalf("len players = %d, want 1", len(v.Players))
	}
	seq := v.Players[0].Sequence
	if len(seq) != 3 {
		t.Fatalf("len seq = %d, want 3 (rl→ya→rl)", len(seq))
	}
	if seq[0].Loc != "rl" || seq[0].Start != 0 || seq[0].End != 3 {
		t.Fatalf("seq[0] = %+v", seq[0])
	}
	if seq[1].Loc != "ya" || seq[1].Start != 3 || seq[1].End != 7 {
		t.Fatalf("seq[1] = %+v", seq[1])
	}
	if seq[2].Loc != "rl" || seq[2].Start != 7 || seq[2].End != 10 {
		t.Fatalf("seq[2] = %+v", seq[2])
	}
}

func TestLocTrailsMinDwell(t *testing.T) {
	r := &result.Result{
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 10000},
			Players: []result.PlayerStream{
				{
					Name: "p1",
					Loc: []result.ChangeI16{
						{T: 0, V: 1},
						{T: 5000, V: 2}, // 100ms blip
						{T: 5100, V: 1}, // back to rl
					},
				},
			},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{
			LocTable: []string{"", "rl", "ya"},
		},
	}
	v, _ := LocTrails(r, LocTrailsOptions{MinDwellMs: 500})
	seq := v.Players[0].Sequence
	if len(seq) != 1 {
		t.Fatalf("len seq = %d, want 1 after coalesce", len(seq))
	}
	if seq[0].Loc != "rl" {
		t.Fatalf("loc = %s, want rl", seq[0].Loc)
	}
}
