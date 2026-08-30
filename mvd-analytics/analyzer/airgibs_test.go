package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// The airgib detection itself is tested in the view package
// (view.ComputeAirgibs); these pin its WIRING since the v76 move — the
// highlights post-processor runs the default-options detector and
// publishes the wrapped rows on Highlights.Airgibs.
func TestHighlightsPost_PublishesTheDefaultAirgibList(t *testing.T) {
	res := &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{
			{Name: "vic", Team: "red", Position: &result.PositionTrack{
				T: []int32{800, 1000}, X: []float32{0, 0}, Y: []float32{0, 0},
				Z: []float32{180, 200}, H: []float32{120, 150}, // airborne before AND at the hit
			}},
			{Name: "att", Team: "blue", Position: &result.PositionTrack{
				T: []int32{1000}, X: []float32{0}, Y: []float32{0}, Z: []float32{40}, H: []float32{0},
			}},
		}},
		Frags: &result.FragResult{},
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 110},
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
	highlightsPost(res, &CoreOutputs{})

	if res.Highlights == nil || len(res.Highlights.Airgibs) != 1 {
		t.Fatalf("highlights.airgibs = %+v, want 1", res.Highlights)
	}
	got := res.Highlights.Airgibs[0]
	if got.Actor.Name != "att" || got.Height != 120 || len(got.Victims) != 1 || got.Victims[0].Name != "vic" {
		t.Errorf("airgib = %+v, want att→vic at height 120 (the pre-impact sample)", got)
	}
}

// A victim standing on a mover when the rocket lands reads as airborne at
// the damage timestamp (the knockback already moved them); the pre-hit
// gate in the default compute is what keeps that off the stored list.
func TestHighlightsPost_SkipsKnockbackFalsePositive(t *testing.T) {
	res := &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{
			{Name: "vic", Team: "red", Position: &result.PositionTrack{
				T: []int32{800, 1000}, X: []float32{0, 0}, Y: []float32{0, 0},
				Z: []float32{319, 620}, H: []float32{0, 303}, // grounded, then blasted
			}},
		}},
		Frags: &result.FragResult{},
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 440},
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
	highlightsPost(res, &CoreOutputs{})
	if res.Highlights != nil && len(res.Highlights.Airgibs) != 0 {
		t.Errorf("airgibs = %+v, want none (victim was grounded 200ms before the hit)", res.Highlights.Airgibs)
	}
}
