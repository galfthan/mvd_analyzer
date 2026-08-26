package aimcore

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// reconFixture builds a two-player Result whose damage section carries source
// and events, with A firing the given shots at B.
func reconFixture(source string, shots []result.Shot, events []result.DamageEntry) *result.Result {
	ts := []int32{0, 500, 1000, 1500, 2000}
	track := func(x float32) *result.PositionTrack {
		return &result.PositionTrack{
			T:   ts,
			X:   []float32{x, x, x, x, x},
			Y:   []float32{0, 0, 0, 0, 0},
			Z:   []float32{0, 0, 0, 0, 0},
			VP:  []int16{0, 0, 0, 0, 0},
			VYa: []int16{0, 0, 0, 0, 0},
		}
	}
	return &result.Result{
		Shots:  &result.ShotsResult{Shots: shots},
		Damage: &result.DamageResult{Source: source, Events: events},
		Streams: &result.Streams{Players: []result.PlayerStream{
			{Name: "A", Position: track(0), Alive: []result.Interval{{Start: 0, End: 2000}}},
			{Name: "B", Position: track(200), Alive: []result.Interval{{Start: 0, End: 2000}}},
		}},
	}
}

// weaponOf returns player p's entry for weapon w.
func weaponOf(t *testing.T, ar *result.AimResult, p, w string) *result.WeaponAim {
	t.Helper()
	if ar == nil {
		t.Fatalf("aim is nil")
	}
	for i := range ar.Players {
		if ar.Players[i].Player != p {
			continue
		}
		for j := range ar.Players[i].Weapons {
			if ar.Players[i].Weapons[j].Weapon == w {
				return &ar.Players[i].Weapons[j]
			}
		}
	}
	t.Fatalf("no %s entry for %s", w, p)
	return nil
}

// A reconstructed damage section fills the RECONSTRUCTED tier and nothing
// else: hitsMeasured stays false, every measured counter stays withheld, and
// the recovered count lives only in weapons[].recon — the separation that
// makes a reconstructed hit unmistakable for a measured one.
func TestReconTierIsSeparateFromMeasured(t *testing.T) {
	shots := []result.Shot{
		{Time: 500, Player: "A", Weapon: "lg", Source: "beam"},
		{Time: 620, Player: "A", Weapon: "lg", Source: "beam"},
		{Time: 1000, Player: "A", Weapon: "sg", Source: "sound"},
	}
	events := []result.DamageEntry{
		// One lg tick connects (the second does not), and the sg volley does.
		{Time: 505, Attacker: "A", Victim: "B", Weapon: "lg", Damage: 7},
		{Time: 1030, Attacker: "A", Victim: "B", Weapon: "sg", Damage: 16},
	}
	ar := Compute(reconFixture(result.DamageSourceReconstructed, shots, events), Query{})
	if ar.HitsMeasured {
		t.Errorf("hitsMeasured = true on a reconstructed damage section")
	}
	if ar.HitsSource != result.AimHitsSourceReconstructed {
		t.Errorf("hitsSource = %q, want %q", ar.HitsSource, result.AimHitsSourceReconstructed)
	}
	lg := weaponOf(t, ar, "A", "lg")
	if lg.Recon == nil || lg.Recon.Hits != 1 {
		t.Errorf("lg recon = %+v, want hits 1", lg.Recon)
	}
	if lg.Hits != 0 || lg.Pellets != 0 || lg.PelletHits != 0 || lg.Miss != 0 || lg.Blocked != 0 {
		t.Errorf("measured lg counters leaked on a reconstructed section: %+v", lg)
	}
	sg := weaponOf(t, ar, "A", "sg")
	if sg.Recon == nil || sg.Recon.Hits != 1 {
		t.Errorf("sg recon = %+v, want hits 1", sg.Recon)
	}
	if sg.PelletHits != 0 || sg.Full != 0 || sg.Partial != 0 {
		t.Errorf("the pellet split leaked on a reconstructed section: %+v", sg)
	}
	// The per-fire columns stay withheld: a reconstructed link is not
	// per-shot truth (a merged delta moves hits between shooters).
	for _, pa := range ar.Players {
		if pa.Crosshair != nil && pa.Crosshair.Hit != nil {
			t.Errorf("%s: crosshair hit column present on a reconstructed section", pa.Player)
		}
		if pa.LGRamp != nil && pa.LGRamp.Hit != nil {
			t.Errorf("%s: lgRamp hit column present on a reconstructed section", pa.Player)
		}
	}
}

// A wire-measured section never grows a recon block, and names itself "ktx".
func TestMeasuredSectionHasNoReconTier(t *testing.T) {
	shots := []result.Shot{{Time: 500, Player: "A", Weapon: "lg", Source: "beam", Hit: true, Victims: []string{"B"}}}
	events := []result.DamageEntry{{Time: 500, Attacker: "A", Victim: "B", Weapon: "lg", Damage: 7}}
	ar := Compute(reconFixture(result.DamageSourceKTX, shots, events), Query{})
	if !ar.HitsMeasured || ar.HitsSource != result.AimHitsSourceKTX {
		t.Fatalf("hitsMeasured=%v hitsSource=%q, want true/%q", ar.HitsMeasured, ar.HitsSource, result.AimHitsSourceKTX)
	}
	if lg := weaponOf(t, ar, "A", "lg"); lg.Recon != nil {
		t.Errorf("recon tier emitted beside measured counters: %+v", lg.Recon)
	}
}

// Several hits merged onto one stat instant are ONE impact — the granularity
// the reconstruction actually has — and one impact can claim at most one fire.
func TestReconTierCountsImpactsNotEvents(t *testing.T) {
	shots := []result.Shot{{Time: 500, Player: "A", Weapon: "sg", Source: "sound"}}
	events := []result.DamageEntry{
		{Time: 510, Attacker: "A", Victim: "B", Weapon: "sg", Damage: 12},
		{Time: 510, Attacker: "A", Victim: "C", Weapon: "sg", Damage: 8},
		{Time: 530, Attacker: "A", Victim: "B", Weapon: "sg", Damage: 4},
	}
	res := reconFixture(result.DamageSourceReconstructed, shots, events)
	ar := Compute(res, Query{})
	if sg := weaponOf(t, ar, "A", "sg"); sg.Recon == nil || sg.Recon.Hits != 1 {
		t.Errorf("sg recon = %+v, want hits 1 — one volley is one hit however many "+
			"victims and stat rows it produced", sg.Recon)
	}
}

// Damage the reconstruction attributed to a weapon the player did not fire
// nearby links to nothing: an impact with no fire in its window is dropped,
// never counted as a hit against some distant fire.
func TestReconTierNeedsAFireInWindow(t *testing.T) {
	shots := []result.Shot{{Time: 500, Player: "A", Weapon: "lg", Source: "beam"}}
	events := []result.DamageEntry{{Time: 1500, Attacker: "A", Victim: "B", Weapon: "lg", Damage: 7}}
	ar := Compute(reconFixture(result.DamageSourceReconstructed, shots, events), Query{})
	if lg := weaponOf(t, ar, "A", "lg"); lg.Recon == nil || lg.Recon.Hits != 0 {
		t.Errorf("lg recon = %+v, want an honest zero", lg.Recon)
	}
}

// One fire is claimable by at most ONE impact, however many separate impacts
// its window covers: two damage instants further apart than the merge span but
// both inside one shotgun fire's ±60 ms window are two impacts and still one
// hit — a fire either connected or it did not, and there is no second fire for
// the second impact to have come from.
//
// Pinned on the join itself rather than through Compute: the emission path caps
// hits at the weapon's fire count, which would silently absorb a double-claim
// here instead of failing.
func TestReconTierOneFireIsClaimedOnce(t *testing.T) {
	shots := []result.Shot{
		{Time: 100, Player: "A", Weapon: "sg"},
		{Time: 5000, Player: "A", Weapon: "sg"}, // far outside both impacts' windows
	}
	dmg := []*dmgRec{
		{t: 50, weapon: "sg", dmg: 8},
		{t: 150, weapon: "sg", dmg: 12}, // > reconImpactMergeMs later: a second impact
	}
	if got := reconHitsByWeapon(shots, dmg, reconTierWeapons)["sg"]; got != 1 {
		t.Errorf("sg recon hits = %d, want 1 — two impacts cannot both claim the "+
			"one fire whose window covers them", got)
	}
}

// Overlapping windows still pair up one-to-one: lg fires 50 ms apart with an
// impact each (±30 ms window, so the first impact's window covers BOTH fires)
// must count 2. Taking the latest covering fire instead makes the early impact
// claim the future fire and strands the late one on an expired one — 1 of 2.
func TestReconTierOverlappingWindowsPairUp(t *testing.T) {
	shots := []result.Shot{
		{Time: 1000, Player: "A", Weapon: "lg", Source: "beam"},
		{Time: 1050, Player: "A", Weapon: "lg", Source: "beam"},
	}
	events := []result.DamageEntry{
		{Time: 1020, Attacker: "A", Victim: "B", Weapon: "lg", Damage: 7},
		{Time: 1055, Attacker: "A", Victim: "B", Weapon: "lg", Damage: 7},
	}
	ar := Compute(reconFixture(result.DamageSourceReconstructed, shots, events), Query{})
	if lg := weaponOf(t, ar, "A", "lg"); lg.Recon == nil || lg.Recon.Hits != 2 {
		t.Errorf("lg recon = %+v, want hits 2 — both fires connected", lg.Recon)
	}
}

// A shooter the reconstruction credits with NO damage at all still gets the
// block, with an honest zero: "fired ten shells, linked nothing" is a supported
// reading, and withholding it would make it indistinguishable from a weapon the
// tier does not cover.
func TestReconTierZeroForAShooterWithNoReconDamage(t *testing.T) {
	shots := []result.Shot{
		{Time: 500, Player: "A", Weapon: "sg", Source: "sound"},
		{Time: 1100, Player: "A", Weapon: "sg", Source: "sound"},
		{Time: 1500, Player: "B", Weapon: "lg", Source: "beam"},
	}
	// Only B's damage is reconstructed; A appears nowhere in the log.
	events := []result.DamageEntry{{Time: 1505, Attacker: "B", Victim: "A", Weapon: "lg", Damage: 7}}
	ar := Compute(reconFixture(result.DamageSourceReconstructed, shots, events), Query{})
	if sg := weaponOf(t, ar, "A", "sg"); sg.Recon == nil || sg.Recon.Hits != 0 {
		t.Errorf("A sg recon = %+v, want a present block with hits 0", sg.Recon)
	}
	if lg := weaponOf(t, ar, "B", "lg"); lg.Recon == nil || lg.Recon.Hits != 1 {
		t.Errorf("B lg recon = %+v, want hits 1", lg.Recon)
	}
}

// The tier covers only the validated (same-frame linkable) weapons: a
// projectile fire publishes shots and no recon block at all, so a consumer can
// never read a withheld weapon as a zero-accuracy one.
func TestReconTierWithholdsProjectiles(t *testing.T) {
	shots := []result.Shot{
		{Time: 500, Player: "A", Weapon: "rl", Source: "sound"},
		{Time: 1000, Player: "A", Weapon: "gl", Source: "sound"},
	}
	events := []result.DamageEntry{
		{Time: 900, Attacker: "A", Victim: "B", Weapon: "rl", Damage: 90, IsSplash: true},
		{Time: 1400, Attacker: "A", Victim: "B", Weapon: "gl", Damage: 60, IsSplash: true},
	}
	ar := Compute(reconFixture(result.DamageSourceReconstructed, shots, events), Query{})
	for _, w := range []string{"rl", "gl"} {
		if wa := weaponOf(t, ar, "A", w); wa.Recon != nil {
			t.Errorf("%s recon = %+v, want withheld — the fire→impact bracket the "+
				"measured counter uses is not reachable from a finished Result", w, wa.Recon)
		}
	}
}
