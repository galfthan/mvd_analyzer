package view

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// airgibTrack builds a PositionTrack from (t, H, Z) triples with X=Y=0 and
// every sample carrying loc index li.
func airgibTrack(li int16, samples ...[3]float32) *result.PositionTrack {
	p := &result.PositionTrack{}
	for _, s := range samples {
		p.T = append(p.T, int32(s[0]))
		p.X = append(p.X, 0)
		p.Y = append(p.Y, 0)
		p.Z = append(p.Z, s[2])
		p.H = append(p.H, s[1])
		p.Li = append(p.Li, li)
	}
	return p
}

// airgibTestResult builds a minimal Result exercising the airgib filter:
// an airborne rocket hit, a grounded one, a self hit, a team hit, and a
// non-rocket hit — only the first should survive. The victim reads clear
// air across the default look-back window for the qualifying hit.
func airgibTestResult() *result.Result {
	vic := result.PlayerStream{
		Name: "vic", Team: "red",
		// {t, H, Z}: airborne through [900, 960] for the hit at 1000;
		// grounded at 1100 for the vetoed second rocket.
		Position: airgibTrack(1,
			[3]float32{860, 110, 170}, [3]float32{900, 120, 180}, [3]float32{940, 130, 190},
			[3]float32{1000, 150, 200}, [3]float32{1100, 0, 24}),
	}
	att := result.PlayerStream{Name: "att", Team: "blue",
		Position: airgibTrack(0, [3]float32{940, 0, 40})}
	return &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{vic, att}},
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 110},               // airborne → airgib
			{Time: 1100, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 90},                // grounded at the hit → no
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
	// Reported from the latest PRE-IMPACT sample (t=940), not the
	// possibly knockback-contaminated hit-frame sample (t=1000).
	if a.Height != 130 {
		t.Errorf("height = %g, want 130 (pre-impact sample at t=940)", a.Height)
	}
	if a.HeightAboveAttacker != 150 {
		t.Errorf("heightAboveAttacker = %g, want 150 (victim Z 190 - shooter Z 40)", a.HeightAboveAttacker)
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
	// Each hit gets one pre-impact sample at hit-50 that both anchors
	// the look-back window and is the reporting sample.
	const n = 25
	pos := &result.PositionTrack{}
	var dmg []result.DamageEntry
	for i := 0; i < n; i++ {
		hit := int32(1000 + 100*i)
		pos.T = append(pos.T, hit-50)
		pos.X = append(pos.X, 0)
		pos.Y = append(pos.Y, 0)
		pos.Z = append(pos.Z, 0)
		pos.H = append(pos.H, float32(100+i)) // ascending height, all airborne
		dmg = append(dmg, result.DamageEntry{Time: hit, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 100})
	}
	res := &result.Result{
		Streams:          &result.Streams{Players: []result.PlayerStream{{Name: "vic", Position: pos}}},
		Damage:           &result.DamageResult{Source: result.DamageSourceKTX, Events: dmg},
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
// match-relative clock as the damage log. (The victim tracks here are
// also coarse — 200ms between samples — so the look-back resolves
// through the preceding-tick fallback, doubling as an old-demo case.)
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
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
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

// knockbackResult reproduces the false positive the pre-hit gate exists
// for: the damage stamp can land 1-2 frames after the impact physics, so
// the samples nearest the damage time show a victim who was STANDING at
// impact hundreds of units in the air (hub gameId 232925: a player on
// the dm2 moving platform blasted off it read 303 units of air). The
// victim's track is native-rate: grounded every 20ms up to hit-40, then
// the blasted hit-frame sample.
func knockbackResult() *result.Result {
	vic := result.PlayerStream{Name: "vic", Team: "red",
		Position: airgibTrack(0,
			[3]float32{800, 0, 319}, [3]float32{820, 0, 319}, [3]float32{840, 0, 319},
			[3]float32{860, 0, 319}, [3]float32{880, 0, 319}, [3]float32{900, 0, 319},
			[3]float32{920, 0, 319}, [3]float32{940, 0, 319}, [3]float32{960, 0, 319},
			[3]float32{1000, 303, 620})}
	return &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{vic}},
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 440},
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
}

func TestComputeAirgibs_GroundedBeforeTheHitIsNotAnAirgib(t *testing.T) {
	if got := ComputeAirgibs(knockbackResult(), AirgibsOptions{}); len(got) != 0 {
		t.Fatalf("airgibs = %+v, want none: the victim was grounded through the look-back window", got)
	}
}

// A recording hole ending in a single boundary sample must not carry a
// whole-window verdict when the tick BEFORE the hole saw the victim
// grounded: with no sample near the window start, the preceding tick —
// the value carried forward at that instant — decides.
func TestComputeAirgibs_SparseHoleFallsBackToThePrecedingTick(t *testing.T) {
	vic := result.PlayerStream{Name: "vic",
		Position: airgibTrack(0,
			[3]float32{799, 0, 319}, [3]float32{960, 100, 420}, [3]float32{1000, 303, 620})}
	res := &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{vic}},
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 110},
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
	// preMs=200: the window [800, 960] has its only sample at 960 — 160ms
	// past the window start — so the grounded tick at 799 decides: reject.
	if got := ComputeAirgibs(res, AirgibsOptions{PreMs: 200}); len(got) != 0 {
		t.Fatalf("preMs=200: airgibs = %+v, want none (preceding tick was grounded)", got)
	}
	// At the default 100ms the boundary sample sits exactly at the
	// evidence-gap limit from the window start and is pre-impact, genuine
	// airborne evidence: accepted. The documented sparse-track degrade.
	if got := ComputeAirgibs(res, AirgibsOptions{}); len(got) != 1 {
		t.Fatalf("default: airgibs = %d, want 1 (boundary sample is genuine pre-impact evidence)", len(got))
	}
}

// Old demos can tick slower than the whole look-back window. When the
// window holds no sample at all, the preceding tick decides — airborne
// accepts, grounded rejects — instead of rejecting for lack of samples.
func TestComputeAirgibs_CoarseTickDemoUsesThePrecedingTick(t *testing.T) {
	mk := func(h float32) *result.Result {
		vic := result.PlayerStream{Name: "vic",
			Position: airgibTrack(0,
				[3]float32{700, h, 500}, [3]float32{850, h, 520}, [3]float32{1000, 140, 540})}
		return &result.Result{
			Streams: &result.Streams{Players: []result.PlayerStream{vic}},
			Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
				{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 110},
			}},
			TimelineAnalysis: &result.TimelineAnalysisResult{},
		}
	}
	got := ComputeAirgibs(mk(130), AirgibsOptions{})
	if len(got) != 1 {
		t.Fatalf("airgibs = %d, want 1: airborne preceding tick carries the coarse-track verdict", len(got))
	}
	if got[0].Height != 130 {
		t.Errorf("height = %g, want 130 (reported from the preceding tick at t=850)", got[0].Height)
	}
	if got := ComputeAirgibs(mk(0), AirgibsOptions{}); len(got) != 0 {
		t.Fatalf("airgibs = %+v, want none: grounded preceding tick rejects", got)
	}
}

// A victim who left a high ledge before the window opened is judged by
// the in-window ticks when they exist, and by the preceding tick when
// they don't — here the track jumps from the ledge sample straight to
// the hit frame, so the airborne tick at hit-195 carries the verdict
// (corpus demo 212498: mj rocketed at a jump apex 195ms after crossing
// the bravado LG ledge edge, 315 units up).
func TestComputeAirgibs_LedgeJumpBeforeTheWindowStillCounts(t *testing.T) {
	vic := result.PlayerStream{Name: "vic",
		Position: airgibTrack(0,
			[3]float32{740, 0, 319}, [3]float32{805, 315, 500}, [3]float32{1000, 303, 620})}
	res := &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{vic}},
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 110},
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
	got := ComputeAirgibs(res, AirgibsOptions{})
	if len(got) != 1 {
		t.Fatalf("airgibs = %d, want 1: airborne at the tick preceding the empty window", len(got))
	}
}

func TestComputeAirgibs_PreMsTunable(t *testing.T) {
	// Native-rate victim: grounded until hit-140, airborne from hit-120.
	vic := result.PlayerStream{Name: "vic",
		Position: airgibTrack(0,
			[3]float32{820, 0, 319}, [3]float32{840, 0, 319}, [3]float32{860, 0, 319},
			[3]float32{880, 150, 480}, [3]float32{900, 155, 490}, [3]float32{920, 160, 500},
			[3]float32{940, 165, 510}, [3]float32{960, 170, 520}, [3]float32{1000, 303, 620})}
	res := &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{vic}},
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 110},
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
	cases := []struct {
		name  string
		preMs int32
		want  int
	}{
		{"default 100ms window is clear air", 0, 1},
		{"explicit 100ms, same as the default", 100, 1},
		{"150ms window reaches the grounded samples", 150, 0},
		{"at or below the stamp-lag bound: a point check at hit-40", 40, 1},
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

// A victim who fell and LANDED just before the rocket must reject even
// though the look-back window (which ends a stamp-lag margin earlier)
// still reads clear air and the hit-frame sample (post-knockback) reads
// high again: the grounded sample beside the hit is trustworthy evidence
// — knockback can over-report height but cannot fake ground contact.
func TestComputeAirgibs_LandingJustBeforeTheHitRejects(t *testing.T) {
	vic := result.PlayerStream{Name: "vic",
		Position: airgibTrack(0,
			[3]float32{860, 150, 480}, [3]float32{880, 130, 460}, [3]float32{900, 110, 440},
			[3]float32{920, 100, 430}, [3]float32{940, 98, 428}, [3]float32{960, 96, 426},
			[3]float32{985, 0, 330}, [3]float32{1000, 303, 620})}
	res := &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{vic}},
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 110},
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
	if got := ComputeAirgibs(res, AirgibsOptions{}); len(got) != 0 {
		t.Fatalf("airgibs = %+v, want none: the victim landed 15ms before the hit", got)
	}
}

// A genuine airgib whose knockback carries the victim laterally over a
// HIGHER floor reads low — but not grounded — beside the hit. Low
// post-impact readings are knockback-contaminated and must not veto;
// only ground contact does.
func TestComputeAirgibs_KnockedOverALedgeStillCounts(t *testing.T) {
	vic := result.PlayerStream{Name: "vic",
		Position: airgibTrack(0,
			[3]float32{880, 150, 480}, [3]float32{900, 152, 482}, [3]float32{920, 154, 484},
			[3]float32{940, 156, 486}, [3]float32{960, 158, 488},
			[3]float32{1000, 40, 470})} // hit frame: over the ledge, 40 above ITS floor
	res := &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{vic}},
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 110},
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
	got := ComputeAirgibs(res, AirgibsOptions{})
	if len(got) != 1 {
		t.Fatalf("airgibs = %d, want 1: a low (not grounded) contaminated reading must not veto", len(got))
	}
	if got[0].Height != 158 {
		t.Errorf("height = %g, want 158 (the pre-impact reading, not the contaminated 40)", got[0].Height)
	}
}

// The window judges every sample inside it: a victim who touched down
// mid-window bounced off the ground into the hit, and the clear air that
// makes an airgib is gone.
func TestComputeAirgibs_TouchdownInsideTheWindowRejects(t *testing.T) {
	vic := result.PlayerStream{Name: "vic",
		Position: airgibTrack(0,
			[3]float32{800, 150, 480}, [3]float32{900, 0, 319}, [3]float32{950, 150, 480},
			[3]float32{1000, 303, 620})}
	res := &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{vic}},
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 110},
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
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

// Reconstructed damage (pre-instrumentation demos) participates on equal
// terms with the wire stream: damagerecon's direct/splash split is
// geometric and its timestamps frame-accurate, and the airgibsPost DAG
// node binds `damage:final` so ordering cannot serve a pre-recon view.
func TestComputeAirgibs_ReconstructedDamageParticipates(t *testing.T) {
	res := airgibTestResult()
	res.Damage.Source = result.DamageSourceReconstructed
	got := ComputeAirgibs(res, AirgibsOptions{})
	want := ComputeAirgibs(airgibTestResult(), AirgibsOptions{})
	if len(got) != len(want) || len(got) != 1 {
		t.Fatalf("airgibs on reconstructed = %d, on ktx = %d, want 1 == 1", len(got), len(want))
	}
}

func TestComputeAirgibs_NoHeightColumnNoAirgibs(t *testing.T) {
	// A victim with positions but no H column (BSP-less run): no airgibs.
	res := &result.Result{
		Streams: &result.Streams{Players: []result.PlayerStream{{
			Name:     "vic",
			Position: &result.PositionTrack{T: []int32{1000}, X: []float32{0}, Y: []float32{0}, Z: []float32{0}},
		}}},
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: []result.DamageEntry{
			{Time: 1000, Attacker: "att", Victim: "vic", Weapon: "rl", Damage: 110},
		}},
		TimelineAnalysis: &result.TimelineAnalysisResult{},
	}
	if got := ComputeAirgibs(res, AirgibsOptions{}); len(got) != 0 {
		t.Errorf("airgibs = %d, want 0 without an H column", len(got))
	}
}
