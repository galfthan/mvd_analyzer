package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// The detection itself is tested in the view package
// (view.ComputeHighlights); this pins the wiring — the post-processor runs
// the default compute and publishes it on Result.Highlights, and leaves the
// section absent when there is nothing to build from.
func TestHighlightsPost_PublishesTheCatalogue(t *testing.T) {
	res := &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{
			{Name: "tp", Team: "blue"},
			{Name: "vic", Team: "red", Health: []result.ChangeI16{{T: 900, V: 80}}, Armor: []result.ChangeI16{{T: 900, V: 50}}},
		}},
		Frags:            &result.FragResult{Frags: []result.FragEntry{{Time: 1000, Killer: "tp", Victim: "vic", Weapon: "tele"}}},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
	highlightsPost(res, &CoreOutputs{})
	if res.Highlights == nil || len(res.Highlights.Telefrags) != 1 {
		t.Fatalf("highlights = %+v, want one telefrag", res.Highlights)
	}
	if v := res.Highlights.Telefrags[0].Victims[0]; v.Name != "vic" || v.Stack == nil || *v.Stack != 130 {
		t.Errorf("victim = %+v, want vic with stack 130", v)
	}
}

func TestHighlightsPost_AbsentWithoutStreams(t *testing.T) {
	res := &result.Result{Frags: &result.FragResult{}, TimelineAnalysis: &result.TimelineAnalysisResult{}}
	highlightsPost(res, &CoreOutputs{})
	if res.Highlights != nil {
		t.Errorf("highlights = %+v, want absent on a stream-less result", res.Highlights)
	}
}
