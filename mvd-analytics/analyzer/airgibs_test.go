package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// The detection itself is tested in the view package (view.ComputeAirgibs);
// this pins the wiring — the post-processor runs the default-options
// compute and publishes it on TimelineAnalysis.
func TestAirgibsPost_PublishesTheDefaultList(t *testing.T) {
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
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 110},
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
	airgibsPost(res, &CoreOutputs{})

	got := res.TimelineAnalysis.Airgibs
	if len(got) != 1 {
		t.Fatalf("airgibs = %d, want 1: %+v", len(got), got)
	}
	if got[0].Attacker != "att" || got[0].Victim != "vic" || got[0].Height != 120 {
		t.Errorf("airgib = %+v, want att→vic at height 120 (the pre-impact sample)", got[0])
	}
}

// A victim standing on a mover when the rocket lands reads as airborne at
// the damage timestamp (the knockback already moved them); the pre-hit
// gate in the default compute is what keeps that off the stored list.
func TestAirgibsPost_SkipsKnockbackFalsePositive(t *testing.T) {
	res := &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{
			{Name: "vic", Team: "red", Position: &result.PositionTrack{
				T: []int32{800, 1000}, X: []float32{0, 0}, Y: []float32{0, 0},
				Z: []float32{319, 620}, H: []float32{0, 303}, // grounded, then blasted
			}},
		}},
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 440},
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
	airgibsPost(res, &CoreOutputs{})
	if got := res.TimelineAnalysis.Airgibs; len(got) != 0 {
		t.Errorf("airgibs = %+v, want none (victim was grounded 200ms before the hit)", got)
	}
}
