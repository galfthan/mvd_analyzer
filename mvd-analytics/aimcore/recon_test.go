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
	hits := reconHitsByWeapon(shots, dmg, reconTierWeapons)
	if got := hits["sg"]; got != 1 {
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

// The tier covers no weapon whose recovery has no ground truth: ng/sng fires
// publish shots and no recon block at all, so a consumer can never read a
// withheld weapon as a zero-accuracy one.
func TestReconTierWithholdsNails(t *testing.T) {
	shots := []result.Shot{
		{Time: 500, Player: "A", Weapon: "ng", Source: "sound"},
		{Time: 1000, Player: "A", Weapon: "sng", Source: "sound"},
	}
	events := []result.DamageEntry{
		{Time: 900, Attacker: "A", Victim: "B", Weapon: "ng", Damage: 9},
		{Time: 1400, Attacker: "A", Victim: "B", Weapon: "sng", Damage: 18},
	}
	ar := Compute(reconFixture(result.DamageSourceReconstructed, shots, events), Query{})
	for _, w := range []string{"ng", "sng"} {
		if wa := weaponOf(t, ar, "A", w); wa.Recon != nil {
			t.Errorf("%s recon = %+v, want withheld — nail linking is opt-in, so the "+
				"measured counter it would be compared against is zero everywhere", w, wa.Recon)
		}
	}
}

// The projectile tier joins on the fire's TRACKED FLIGHT, not on the fire: a
// rocket whose entity never broadcast has no flightEnd and is a miss even
// though the reconstruction saw damage it could have caused — exactly what the
// measured counter says about it (analyzer/shots.go linkProjectiles). The same
// fire with its flight tracked is a hit.
func TestReconTierProjectileNeedsATrackedFlight(t *testing.T) {
	events := []result.DamageEntry{
		{Time: 900, Attacker: "A", Victim: "B", Weapon: "rl", Damage: 90, IsSplash: true},
	}
	untracked := []result.Shot{{Time: 500, Player: "A", Weapon: "rl", Source: "sound"}}
	ar := Compute(reconFixture(result.DamageSourceReconstructed, untracked, events), Query{})
	if rl := weaponOf(t, ar, "A", "rl"); rl.Recon == nil || rl.Recon.Hits != 0 {
		t.Errorf("rl recon = %+v, want a present block with hits 0 — no flight, no link", rl.Recon)
	}

	end := int32(880)
	tracked := []result.Shot{{Time: 500, Player: "A", Weapon: "rl", Source: "sound", FlightEnd: &end}}
	ar = Compute(reconFixture(result.DamageSourceReconstructed, tracked, events), Query{})
	if rl := weaponOf(t, ar, "A", "rl"); rl.Recon == nil || rl.Recon.Hits != 1 {
		t.Errorf("rl recon = %+v, want hits 1 — the flight ended 20 ms before the "+
			"victim's stat instant", rl.Recon)
	}
}

// A flight claims one whole impact instant: the rocket that hurt two players in
// the same frame is ONE hit, and the frame-mate row is consumed so a second
// flight ending alongside it cannot count the same explosion twice.
func TestReconTierFlightClaimsOneImpactInstant(t *testing.T) {
	e1, e2 := int32(880), int32(890)
	shots := []result.Shot{
		{Time: 500, Player: "A", Weapon: "rl", Source: "sound", FlightEnd: &e1},
		{Time: 560, Player: "A", Weapon: "rl", Source: "sound", FlightEnd: &e2},
	}
	events := []result.DamageEntry{
		{Time: 900, Attacker: "A", Victim: "B", Weapon: "rl", Damage: 90, IsSplash: true},
		{Time: 900, Attacker: "A", Victim: "C", Weapon: "rl", Damage: 40, IsSplash: true},
	}
	ar := Compute(reconFixture(result.DamageSourceReconstructed, shots, events), Query{})
	if rl := weaponOf(t, ar, "A", "rl"); rl.Recon == nil || rl.Recon.Hits != 1 {
		t.Errorf("rl recon = %+v, want hits 1 — one explosion, two victim rows, and no "+
			"evidence the second rocket hurt anyone", rl.Recon)
	}
}

// A WINDOWED query scopes the fires, never the evidence they are judged on. A
// grenade fired inside [from,to] whose fuse runs out past `to` connected, and
// the recon tier has to say so: its damage instant lies outside the window by
// construction (up to the 2.5 s fuse plus the stat-instant lag), and clipping
// the damage pool on the same bounds as the fires would report it as a miss —
// an artefact of where the window was cut, not of the shooter's aim. The
// measured tier has no such artefact: Shot.Hit is linked match-wide before any
// window is applied.
func TestReconTierWindowedQueryKeepsStraddlingFlight(t *testing.T) {
	end := int32(1900)
	shots := []result.Shot{{Time: 1000, Player: "A", Weapon: "gl", Source: "sound", FlightEnd: &end}}
	events := []result.DamageEntry{
		{Time: 1930, Attacker: "A", Victim: "B", Weapon: "gl", Damage: 60, IsSplash: true},
	}
	to := int32(1500)
	ar := Compute(reconFixture(result.DamageSourceReconstructed, shots, events), Query{ToMs: &to})
	gl := weaponOf(t, ar, "A", "gl")
	if gl.Shots != 1 {
		t.Fatalf("gl shots = %d, want 1 — the fire is inside the window", gl.Shots)
	}
	if gl.Recon == nil || gl.Recon.Hits != 1 {
		t.Errorf("gl recon = %+v, want hits 1 — the grenade was fired in the window "+
			"and detonated after it", gl.Recon)
	}
}

// The mirror of the above at the window's start: a fire before `from` is not in
// the slice, and the damage it caused inside the window must not be handed to
// an in-window fire that did not cause it. The pool is unclipped, so the
// evidence is there — the join's own windows are what keep it out of reach.
func TestReconTierWindowedQueryDoesNotAdoptPreWindowDamage(t *testing.T) {
	early, late := int32(600), int32(1900)
	shots := []result.Shot{
		{Time: 300, Player: "A", Weapon: "rl", Source: "sound", FlightEnd: &early},
		{Time: 1500, Player: "A", Weapon: "rl", Source: "sound", FlightEnd: &late},
	}
	events := []result.DamageEntry{
		{Time: 640, Attacker: "A", Victim: "B", Weapon: "rl", Damage: 90, IsSplash: true},
	}
	from := int32(1000)
	ar := Compute(reconFixture(result.DamageSourceReconstructed, shots, events), Query{FromMs: &from})
	rl := weaponOf(t, ar, "A", "rl")
	if rl.Shots != 1 {
		t.Fatalf("rl shots = %d, want 1 — only the second fire is inside the window", rl.Shots)
	}
	if rl.Recon == nil || rl.Recon.Hits != 0 {
		t.Errorf("rl recon = %+v, want hits 0 — the damage belongs to the pre-window "+
			"fire and is nowhere near the in-window flight's end", rl.Recon)
	}
}

// Grenades that detonate long after the fire still link, because the anchor is
// the flight's end and not the fire: a 2 s lob whose impact damage lands right
// after the despawn is a hit, while the same damage with no flight is not.
func TestReconTierGrenadeLinksOnFlightEnd(t *testing.T) {
	end := int32(2400)
	shots := []result.Shot{{Time: 300, Player: "A", Weapon: "gl", Source: "sound", FlightEnd: &end}}
	events := []result.DamageEntry{
		{Time: 2430, Attacker: "A", Victim: "B", Weapon: "gl", Damage: 60, IsSplash: true},
	}
	ar := Compute(reconFixture(result.DamageSourceReconstructed, shots, events), Query{})
	if gl := weaponOf(t, ar, "A", "gl"); gl.Recon == nil || gl.Recon.Hits != 1 {
		t.Errorf("gl recon = %+v, want hits 1", gl.Recon)
	}
}

// ReconDirectHits counts the reconstruction's DIRECT rl/gl rows per attacker
// — KTX's own convention for those two — and does it by counting rows rather
// than by joining fires to flights, because one projectile touches at most
// one player and a touch therefore IS a row.
//
// The three properties that make it publishable are all pinned here: a
// weapon the player FIRED gets an honest zero rather than an absence; a self
// row is never a touch (a missile cannot collide with its owner); and a
// splash row never counts however close it landed.
func TestReconDirectHitsCountsRowsNotFires(t *testing.T) {
	res := &result.Result{
		Damage: &result.DamageResult{
			Source: result.DamageSourceReconstructed,
			Events: []result.DamageEntry{
				{Time: 100, Attacker: "a", Victim: "b", Weapon: "rl", Damage: 110},
				{Time: 100, Attacker: "a", Victim: "c", Weapon: "rl", Damage: 40, IsSplash: true},
				{Time: 900, Attacker: "a", Victim: "a", Weapon: "rl", Damage: 30, IsSelf: true},
				{Time: 1500, Attacker: "a", Victim: "b", Weapon: "gl", Damage: 90},
				{Time: 2000, Attacker: "a", Victim: "b", Weapon: "lg", Damage: 30},
				{Time: 2600, Attacker: "b", Victim: "a", Weapon: "rl", Damage: 110},
				{Time: 3000, Attacker: "", Victim: "b", Weapon: "lava", Damage: 20, IsEnv: true},
			},
		},
		Shots: &result.ShotsResult{Shots: []result.Shot{
			{Time: 50, Player: "a", Weapon: "rl"},
			{Time: 1400, Player: "a", Weapon: "gl"},
			{Time: 1900, Player: "a", Weapon: "lg"},
			{Time: 2550, Player: "b", Weapon: "rl"},
			{Time: 4000, Player: "c", Weapon: "rl"},
		}},
	}
	got := ReconDirectHits(res, Query{})
	if got["a"]["rl"] != 1 {
		t.Errorf("a rl = %d, want 1 — the splash row and the self row are not touches", got["a"]["rl"])
	}
	if got["a"]["gl"] != 1 {
		t.Errorf("a gl = %d, want 1", got["a"]["gl"])
	}
	if _, ok := got["a"]["lg"]; ok {
		t.Error("lg carries no direct count: the two conventions coincide there")
	}
	if got["b"]["rl"] != 1 {
		t.Errorf("b rl = %d, want 1", got["b"]["rl"])
	}
	// c fired a rocket and touched nobody: a supported zero, not an absence.
	if n, ok := got["c"]["rl"]; !ok || n != 0 {
		t.Errorf("c rl = %v/%v, want a present 0 — the weapon was fired", n, ok)
	}
	// A wire-measured section has its own counters; this must stay out of it.
	res.Damage.Source = result.DamageSourceKTX
	if ReconDirectHits(res, Query{}) != nil {
		t.Error("direct hits must be nil on a wire-measured section")
	}
}

// A windowed Compute publishes a window-scoped block, so the touch count
// inside it must be the window's touches. It is not a join — the rows ARE
// the count — so nothing else scopes it, and a match-wide count would report
// the whole match's touches against the window's fires (a 60-second window
// over a 20-minute demo reading 100% direct accuracy).
//
// The straddling fire pins the documented edge: the fires are scoped by fire
// time and the rows by damage instant, so a rocket launched just inside `to`
// lands its touch just outside and is counted as a fire without its row —
// bounded by one flight per edge, where a match-wide count is unbounded.
func TestReconDirectHitsScopesToTheQueryWindow(t *testing.T) {
	long := func(x float32) *result.PositionTrack {
		ts := []int32{0, 60000, 120000}
		return &result.PositionTrack{
			T: ts, X: []float32{x, x, x}, Y: []float32{0, 0, 0}, Z: []float32{0, 0, 0},
			VP: []int16{0, 0, 0}, VYa: []int16{0, 0, 0},
		}
	}
	res := &result.Result{
		Shots: &result.ShotsResult{Shots: []result.Shot{
			{Time: 1000, Player: "A", Weapon: "rl", FlightEnd: i32p(1200)},
			{Time: 59900, Player: "A", Weapon: "rl", FlightEnd: i32p(60100)},
			{Time: 90000, Player: "A", Weapon: "rl", FlightEnd: i32p(90200)},
		}},
		Damage: &result.DamageResult{
			Source: result.DamageSourceReconstructed,
			Events: []result.DamageEntry{
				{Time: 1200, Attacker: "A", Victim: "B", Weapon: "rl", Damage: 110},
				{Time: 60100, Attacker: "A", Victim: "B", Weapon: "rl", Damage: 110},
				{Time: 90200, Attacker: "A", Victim: "B", Weapon: "rl", Damage: 110},
			},
		},
		Streams: &result.Streams{Players: []result.PlayerStream{
			{Name: "A", Position: long(0), Alive: []result.Interval{{Start: 0, End: 120000}}},
			{Name: "B", Position: long(200), Alive: []result.Interval{{Start: 0, End: 120000}}},
		}},
	}
	directOf := func(q Query) (shots, direct int) {
		t.Helper()
		wa := weaponOf(t, Compute(res, q), "A", "rl")
		if wa.Recon == nil || wa.Recon.DirectHits == nil {
			t.Fatalf("no recon direct count on %+v", wa.Recon)
		}
		return wa.Shots, *wa.Recon.DirectHits
	}
	if s, d := directOf(Query{}); s != 3 || d != 3 {
		t.Errorf("match-wide = %d shots / %d direct, want 3/3", s, d)
	}
	from, to := int32(0), int32(60000)
	if s, d := directOf(Query{FromMs: &from, ToMs: &to}); s != 2 || d != 1 {
		t.Errorf("first minute = %d shots / %d direct, want 2/1 — the 90 s touch is out of window", s, d)
	}
	// The second window owns one fire and TWO rows — the straddling flight's
	// touch landed here while its fire was counted in the first window — so
	// the raw count exceeds the fires and the caller's clamp is what keeps
	// the published accuracy at most 100%.
	from2, to2 := int32(60000), int32(120000)
	if n := ReconDirectHits(res, Query{FromMs: &from2, ToMs: &to2})["A"]["rl"]; n != 2 {
		t.Errorf("second window raw count = %d, want 2", n)
	}
	if s, d := directOf(Query{FromMs: &from2, ToMs: &to2}); s != 1 || d != 1 {
		t.Errorf("second window = %d shots / %d direct, want 1/1 after the shots clamp", s, d)
	}
}

func i32p(v int32) *int32 { return &v }
