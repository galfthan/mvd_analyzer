package view

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// airgibTestResult builds a minimal Result exercising the airgib filter:
// an airborne rocket hit, a grounded one, a self hit, a team hit, and a
// non-rocket hit — only the first should survive. The victim is airborne
// well before the qualifying hit too, so it clears the pre-hit gate.
func airgibTestResult() *result.Result {
	vic := result.PlayerStream{
		Name: "vic", Team: "red",
		Position: &result.PositionTrack{
			T:  []int32{800, 900, 1000, 1100, 1200},
			X:  []float32{0, 0, 0, 0, 0},
			Y:  []float32{0, 0, 0, 0, 0},
			Z:  []float32{160, 180, 200, 24, 0},
			Li: []int16{1, 1, 1, 1, 0},
			H:  []float32{120, 130, 150, 0, result.NoFloor}, // airborne ×3, grounded, void
		},
	}
	att := result.PlayerStream{Name: "att", Team: "blue", Position: &result.PositionTrack{
		T: []int32{1000}, X: []float32{0}, Y: []float32{0}, Z: []float32{40}, H: []float32{0},
	}}
	return &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{vic, att}},
		Damage: &result.DamageResult{Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 110},               // airborne → airgib
			{Time: 1100, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 90},                // grounded → no
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "lg", Damage: 30},                // not a rocket → no
			{Time: 1000, Attacker: "vic", Victim: "vic", Weapon: "rl", Damage: 50, IsSelf: true},  // self → no
			{Time: 1000, Attacker: "mate", Victim: "vic", Weapon: "rl", Damage: 40, IsTeam: true}, // team → no
		}},
		Frags: &result.FragResult{Frags: []result.FragEntry{
			{Time: 1040, Killer: "att", Victim: "vic", Weapon: "rl"}, // lethal for the airborne hit
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{
			LocTable:      []string{"", "MID"},
			PlayerUserIDs: map[string]int{"att": 5, "vic": 7},
		},
	}
}

func TestComputeAirgibs_DetectsAirborneRocketHit(t *testing.T) {
	got := ComputeAirgibs(airgibTestResult(), AirgibsOptions{})
	if len(got) != 1 {
		t.Fatalf("airgibs = %d, want 1: %+v", len(got), got)
	}
	a := got[0]
	if a.Attacker != "att" || a.Victim != "vic" {
		t.Errorf("players = %s→%s, want att→vic", a.Attacker, a.Victim)
	}
	if a.AttackerTeam != "blue" || a.VictimTeam != "red" {
		t.Errorf("teams = %s/%s, want blue/red", a.AttackerTeam, a.VictimTeam)
	}
	if a.Height != 150 {
		t.Errorf("height = %g, want 150 (sample at t=1000)", a.Height)
	}
	if a.HeightAboveAttacker != 160 {
		t.Errorf("heightAboveAttacker = %g, want 160 (victim Z 200 - shooter Z 40)", a.HeightAboveAttacker)
	}
	if a.Loc != "MID" {
		t.Errorf("loc = %q, want MID", a.Loc)
	}
	if a.Damage != 110 {
		t.Errorf("damage = %d, want 110", a.Damage)
	}
	if !a.Lethal {
		t.Errorf("lethal = false, want true (rocket frag at 1040)")
	}
	if a.AttackerUserID != 5 || a.VictimUserID != 7 {
		t.Errorf("userIDs = %d/%d, want 5/7", a.AttackerUserID, a.VictimUserID)
	}
}

func TestComputeAirgibs_SortedByHeightUncapped(t *testing.T) {
	// Build many airborne hits with ascending height; expect every
	// qualifying hit emitted (no cap, schema v30), sorted descending.
	const n = 25
	// Airborne samples covering every hit's look-back window so each hit
	// clears the pre-hit gate.
	pos := &result.PositionTrack{
		T: []int32{800, 850, 900, 950}, X: []float32{0, 0, 0, 0}, Y: []float32{0, 0, 0, 0},
		Z: []float32{0, 0, 0, 0}, H: []float32{100, 100, 100, 100},
	}
	var dmg []result.DamageEntry
	for i := 0; i < n; i++ {
		tMs := int32(1000 + i)
		pos.T = append(pos.T, tMs)
		pos.X = append(pos.X, 0)
		pos.Y = append(pos.Y, 0)
		pos.Z = append(pos.Z, 0)
		pos.H = append(pos.H, float32(100+i)) // ascending height, all airborne
		dmg = append(dmg, result.DamageEntry{Time: tMs, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 100})
	}
	res := &result.Result{
		Streams:          &result.Streams{Players: []result.PlayerStream{{Name: "vic", Position: pos}}},
		Damage:           &result.DamageResult{Events: dmg},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
	got := ComputeAirgibs(res, AirgibsOptions{})
	if len(got) != n {
		t.Fatalf("airgibs = %d, want all %d qualifying hits", len(got), n)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Height < got[i].Height {
			t.Fatalf("not sorted by height desc at %d: %g < %g", i, got[i-1].Height, got[i].Height)
		}
	}
	if got[0].Height != float32(100+n-1) {
		t.Errorf("top height = %g, want %d", got[0].Height, 100+n-1)
	}
	// The attacker has no stream in this fixture: the shooter gap stays
	// at the neutral 0 rather than inventing a value.
	if got[0].HeightAboveAttacker != 0 {
		t.Errorf("heightAboveAttacker = %g, want 0 without an attacker track", got[0].HeightAboveAttacker)
	}
}

// Airgibs stamp the userid of the connection each player held AT THE HIT,
// not the demo-wide last-session-with-play id: a hit inside a rejoiner's
// earlier stint belongs to the connection that threw it. The lookup reads
// the published per-stream session table, which is on the same
// match-relative clock as the damage log.
func TestComputeAirgibs_UserIDIsTheSessionAtTheHit(t *testing.T) {
	pos := func(ts ...int32) *result.PositionTrack {
		p := &result.PositionTrack{}
		for _, t := range ts {
			p.T = append(p.T, t)
			p.X = append(p.X, 0)
			p.Y = append(p.Y, 0)
			p.Z = append(p.Z, 200)
			p.H = append(p.H, 150) // airborne at every sample
		}
		return p
	}
	res := &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{
			{
				Name:     "vic",
				Position: pos(19_800, 20_000, 159_800, 160_000),
				Sessions: []result.PlayerSession{
					{StartMs: 0, EndMs: 300_000, Slot: 4, UserID: 7, Name: "vic"},
				},
			},
			{
				Name:     "att",
				Position: pos(19_800, 20_000, 159_800, 160_000),
				Sessions: []result.PlayerSession{
					{StartMs: -2_000, EndMs: 150_000, Slot: 3, UserID: 12, Name: "att"},
					{StartMs: 150_000, EndMs: 300_000, Slot: 3, UserID: 25, Name: "att"},
				},
			},
		}},
		Damage: &result.DamageResult{Events: []result.DamageEntry{
			{Time: 20_000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 100},
			{Time: 160_000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 100},
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{
			// The demo-wide map answers 25 for both hits; only per-instant
			// resolution can tell the two connections apart.
			PlayerUserIDs: map[string]int{"att": 25, "vic": 7},
		},
	}
	airgibs := ComputeAirgibs(res, AirgibsOptions{})
	got := map[int32]int{}
	for _, ag := range airgibs {
		got[ag.Time] = ag.AttackerUserID
		if ag.VictimUserID != 7 {
			t.Errorf("victim userid at t=%d = %d, want 7", ag.Time, ag.VictimUserID)
		}
	}
	if len(got) != 2 {
		t.Fatalf("airgibs = %+v, want 2", airgibs)
	}
	if got[20_000] != 12 {
		t.Errorf("early hit attacker userid = %d, want 12 (the connection live then)", got[20_000])
	}
	if got[160_000] != 25 {
		t.Errorf("late hit attacker userid = %d, want 25 (the connection live then)", got[160_000])
	}
}

// knockbackFixture reproduces the false positive the pre-hit gate exists
// for: the KTX damage entry is stamped at (or one frame after) the physics
// frame in which the rocket's own knockback already moved the victim, so
// the sample nearest the damage time shows a victim who was STANDING at
// impact hundreds of units in the air (hub gameId 232925: a player on the
// dm2 moving platform blasted off it read 303 units of air). midMs, when
// non-zero, adds a genuinely-airborne sample between the grounded one and
// the hit — the knob the PreMs tests turn.
func knockbackFixture(midMs int32) *result.Result {
	pos := &result.PositionTrack{
		T: []int32{800, 1000}, X: []float32{0, 0}, Y: []float32{0, 0},
		Z: []float32{319, 620}, H: []float32{0, 303}, // grounded on the mover, then blasted
	}
	if midMs != 0 {
		pos.T = []int32{800, midMs, 1000}
		pos.X = []float32{0, 0, 0}
		pos.Y = []float32{0, 0, 0}
		pos.Z = []float32{319, 500, 620}
		pos.H = []float32{0, 150, 303}
	}
	return &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{{Name: "vic", Team: "red", Position: pos}}},
		Damage: &result.DamageResult{Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 440},
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
}

func TestComputeAirgibs_GroundedBeforeTheHitIsNotAnAirgib(t *testing.T) {
	if got := ComputeAirgibs(knockbackFixture(0), AirgibsOptions{}); len(got) != 0 {
		t.Fatalf("airgibs = %+v, want none: the victim was grounded 200ms before the hit", got)
	}
}

// A track with NO pre-impact sample must fail the pre-hit gate rather
// than fall through to the hit sample: unrestricted nearest-sample
// selection would pick the post-knockback hit sample itself (200ms from
// the look-back point, inside the 250ms gap tolerance) — exactly the
// contaminated reading the gate exists to overrule.
func TestComputeAirgibs_SparseTrackCannotGateOnTheHitSample(t *testing.T) {
	res := knockbackFixture(0)
	pos := res.Streams.Players[0].Position
	pos.T = pos.T[1:] // drop the grounded sample: only the hit sample remains
	pos.X, pos.Y, pos.Z, pos.H = pos.X[1:], pos.Y[1:], pos.Z[1:], pos.H[1:]
	if got := ComputeAirgibs(res, AirgibsOptions{}); len(got) != 0 {
		t.Fatalf("airgibs = %+v, want none: no pre-impact sample to gate on", got)
	}
}

// The pre-gate judges the samples INSIDE the look-back window, not the
// carry-forward state at its exact start: a victim who jumped off a
// ledge just after (hit - preMs) — airborne at every sample of the
// window, grounded an instant before it — is a genuine airgib (corpus
// demo 212498: mj rocketed at a jump apex 195ms after crossing the
// bravado LG ledge edge, 315 units up). Samples closer to the hit than
// the stamp-lag margin never participate, so the contaminated hit-frame
// reading still cannot gate.
func TestComputeAirgibs_LedgeJumpJustInsideTheLookbackStillCounts(t *testing.T) {
	// Grounded at hit-260 (before the window), airborne from hit-195 on.
	res := knockbackFixture(0)
	pos := res.Streams.Players[0].Position
	pos.T = []int32{740, 805, 1000}
	pos.X = []float32{0, 0, 0}
	pos.Y = []float32{0, 0, 0}
	pos.Z = []float32{319, 500, 620}
	pos.H = []float32{0, 315, 303} // ledge, over the drop, hit
	got := ComputeAirgibs(res, AirgibsOptions{})
	if len(got) != 1 {
		t.Fatalf("airgibs = %d, want 1: airborne at every sample inside the look-back window", len(got))
	}
}

func TestComputeAirgibs_PreMsTunable(t *testing.T) {
	// Victim grounded at t=800, airborne from t=950 on, hit at t=1000.
	res := knockbackFixture(950)
	cases := []struct {
		name  string
		preMs int32
		want  int
	}{
		{"default 200ms window reaches the grounded sample", 0, 0},
		{"explicit 200ms, same as the default", 200, 0},
		{"100ms window holds only the airborne sample", 100, 1},
		{"at or below the stamp-lag margin nothing can anchor: gate off", 40, 1},
		{"negative disables the pre-hit gate", -1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeAirgibs(res, AirgibsOptions{PreMs: tc.preMs})
			if len(got) != tc.want {
				t.Errorf("airgibs = %d, want %d (preMs=%d): %+v", len(got), tc.want, tc.preMs, got)
			}
		})
	}
}

// The window runs all the way TO the hit, with no excluded tail: a
// victim who fell and LANDED just before the rocket leaves grounded
// samples right up against the hit, and they must reject even though the
// hit sample itself (post-knockback) reads high again. Knockback can
// only over-report height, so including the possibly-contaminated tail
// in the all-airborne check is safe — it can only reject.
func TestComputeAirgibs_LandingJustBeforeTheHitRejects(t *testing.T) {
	res := knockbackFixture(0)
	pos := res.Streams.Players[0].Position
	pos.T = []int32{820, 900, 985, 1000}
	pos.X = []float32{0, 0, 0, 0}
	pos.Y = []float32{0, 0, 0, 0}
	pos.Z = []float32{500, 400, 319, 620}
	pos.H = []float32{150, 120, 0, 303} // falling, falling, LANDED, hit (knocked off again)
	if got := ComputeAirgibs(res, AirgibsOptions{}); len(got) != 0 {
		t.Fatalf("airgibs = %+v, want none: the victim landed 15ms before the hit", got)
	}
}

// The gate demands airborne at EVERY sample of the look-back window, not
// just at its start: a victim who touched down mid-window bounced off
// the ground into the hit, and the hang time that makes an airgib is
// gone.
func TestComputeAirgibs_TouchdownInsideTheWindowRejects(t *testing.T) {
	res := knockbackFixture(0)
	pos := res.Streams.Players[0].Position
	pos.T = []int32{800, 900, 950, 1000}
	pos.X = []float32{0, 0, 0, 0}
	pos.Y = []float32{0, 0, 0, 0}
	pos.Z = []float32{500, 319, 500, 620}
	pos.H = []float32{150, 0, 150, 303} // airborne, TOUCHED DOWN, airborne, hit
	if got := ComputeAirgibs(res, AirgibsOptions{}); len(got) != 0 {
		t.Fatalf("airgibs = %+v, want none: grounded sample inside the look-back window", got)
	}
}

// Teams fall back to Match.Players (the canonical post-normalize
// scoreboard) when a participant's stream carries no team label.
func TestComputeAirgibs_TeamFallsBackToMatchPlayers(t *testing.T) {
	res := airgibTestResult()
	for i := range res.Streams.Players {
		res.Streams.Players[i].Team = ""
	}
	res.Match = &result.MatchResult{Players: []result.PlayerStat{
		{Name: "att", Team: "blue"}, {Name: "vic", Team: "red"},
	}}
	got := ComputeAirgibs(res, AirgibsOptions{})
	if len(got) != 1 {
		t.Fatalf("airgibs = %d, want 1", len(got))
	}
	if got[0].AttackerTeam != "blue" || got[0].VictimTeam != "red" {
		t.Errorf("teams = %s/%s, want blue/red from Match.Players", got[0].AttackerTeam, got[0].VictimTeam)
	}
}

func TestValidateAirgibPreMs(t *testing.T) {
	for _, ok := range []int{0, 1, DefaultAirgibPreMs, MaxAirgibPreMs} {
		if err := ValidateAirgibPreMs(ok); err != nil {
			t.Errorf("ValidateAirgibPreMs(%d) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []int{-1, MaxAirgibPreMs + 1, 100_000} {
		if err := ValidateAirgibPreMs(bad); err == nil {
			t.Errorf("ValidateAirgibPreMs(%d) = nil, want an error", bad)
		}
	}
}

func TestComputeAirgibs_NoHeightColumnNoAirgibs(t *testing.T) {
	// A victim with positions but no H column (BSP-less run): no airgibs.
	res := &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{{
			Name:     "vic",
			Position: &result.PositionTrack{T: []int32{1000}, X: []float32{0}, Y: []float32{0}, Z: []float32{0}},
		}}},
		Damage: &result.DamageResult{Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 110},
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
	if got := ComputeAirgibs(res, AirgibsOptions{}); len(got) != 0 {
		t.Errorf("airgibs = %d, want 0 without an H column", len(got))
	}
}
