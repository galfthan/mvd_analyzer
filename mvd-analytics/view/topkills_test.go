package view

import (
	"errors"
	"reflect"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// tkResult builds a two-player Result for the burst walk: an explicit frag log,
// an explicit damage log and explicit lives, so the tests pin TopKills' own
// behaviour rather than the analyzer's derivations.
//
// alive overrides a player's lives; anyone absent from it is alive for the
// whole match. KillsMeasured mirrors what the analyzer publishes for a demo
// with a non-empty obituary log — without it measured.frags reads false while
// the rows are plainly there (see MeasuredSources.Frags).
func tkResult(frags []result.FragEntry, hits []result.DamageEntry, alive map[string][]result.Interval) *result.Result {
	teams := map[string]string{"A": "red", "B": "blue", "C": "red"}
	players := make([]result.PlayerStream, 0, len(teams))
	for _, n := range []string{"A", "B", "C"} {
		p := result.PlayerStream{Name: n, Team: teams[n], Alive: []result.Interval{{Start: 0, End: 120000}}}
		if iv, ok := alive[n]; ok {
			p.Alive = iv
		}
		players = append(players, p)
	}
	return &result.Result{
		Frags:  &result.FragResult{Frags: frags, KillsMeasured: true},
		Damage: &result.DamageResult{Events: hits, BoundedMode: "standard"},
		Streams: &result.Streams{
			Global:  result.GlobalStream{MatchStart: 0, MatchEnd: 120000},
			Players: players,
		},
		Match: &result.MatchResult{Players: []result.PlayerStat{
			{Name: "A", Team: "red"}, {Name: "B", Team: "blue"}, {Name: "C", Team: "red"},
		}},
	}
}

// bhit is one hit carrying a bounded value distinct from its raw one.
func bhit(t int32, attacker, victim, weapon string, raw, bounded int) result.DamageEntry {
	e := dmg(t, attacker, victim, weapon, raw)
	e.Bounded = &bounded
	return e
}

// mustTK runs a query that must succeed. Every error path is pinned in one
// place (TestTopKillsErrors); elsewhere an error is a fixture bug.
func mustTK(t *testing.T, r *result.Result, opts TopKillsOptions) *TopKillsView {
	t.Helper()
	got, err := TopKills(r, opts)
	if err != nil {
		t.Fatalf("TopKills(%+v): %v", opts, err)
	}
	return got
}

// topKill is mustTK plus the top row, the shape most tests here want.
func topKill(t *testing.T, r *result.Result, opts TopKillsOptions) TopKill {
	t.Helper()
	got := mustTK(t, r, opts)
	if len(got.Kills) == 0 {
		t.Fatal("no kill rows")
	}
	return got.Kills[0]
}

// The whole contract in one test: the run is the killing-weapon hits leading up
// to the kill, a generous capture gap holds a run together across a 1500 ms
// internal gap, and a narrow one truncates it to the killing hit alone.
func TestTopKillsCaptureGapHoldsTheRunTogether(t *testing.T) {
	frags := []result.FragEntry{kill(10000, "A", "B", "rl")}
	hits := []result.DamageEntry{
		dmg(7000, "A", "B", "rl", 60),
		dmg(8500, "A", "B", "rl", 70),
		dmg(9000, "A", "B", "lg", 100), // another weapon: neither joins nor breaks the run
		dmg(10000, "A", "B", "rl", 80),
	}
	r := tkResult(frags, hits, nil)

	k := topKill(t, r, TopKillsOptions{})
	if k.Damage != 210 || k.Hits != 3 {
		t.Errorf("burst = %d dmg over %d hits, want 210/3 (the lg hit is not part of an rl burst)", k.Damage, k.Hits)
	}
	if k.SpanMs != 3000 || k.MaxGapMs != 1500 {
		t.Errorf("spanMs/maxGapMs = %d/%d, want 3000/1500", k.SpanMs, k.MaxGapMs)
	}
	if k.Rank != 1 || k.Killer != "A" || k.Victim != "B" || k.Team != "red" {
		t.Errorf("row = %+v, want rank 1, A→B, killer team red", k)
	}
	if k.Time != 10000 || k.Weapon != "rl" {
		t.Errorf("time/weapon = %d/%q, want 10000/rl", k.Time, k.Weapon)
	}

	// The same kill under a gap narrower than the run's own cadence: only the
	// killing hit survives, and a one-hit run has no gap and no span.
	n := topKill(t, r, TopKillsOptions{GapMs: 1200})
	if n.Damage != 80 || n.Hits != 1 {
		t.Errorf("narrow burst = %d dmg over %d hits, want 80/1", n.Damage, n.Hits)
	}
	if n.SpanMs != 0 || n.MaxGapMs != 0 {
		t.Errorf("one-hit burst spanMs/maxGapMs = %d/%d, want 0/0", n.SpanMs, n.MaxGapMs)
	}
}

// maxGapMs is the LARGEST inter-hit gap, not the last or the average — it is
// what a client narrows on, so it has to be exact.
func TestTopKillsSpanAndMaxGap(t *testing.T) {
	frags := []result.FragEntry{kill(8000, "A", "B", "rl")}
	hits := []result.DamageEntry{
		dmg(5000, "A", "B", "rl", 10),
		dmg(5100, "A", "B", "rl", 10),
		dmg(8000, "A", "B", "rl", 10),
	}
	k := topKill(t, tkResult(frags, hits, nil), TopKillsOptions{})
	if k.Hits != 3 || k.Damage != 30 {
		t.Fatalf("burst = %d dmg over %d hits, want 30/3", k.Damage, k.Hits)
	}
	if k.SpanMs != 3000 {
		t.Errorf("spanMs = %d, want 3000 (kill - first hit)", k.SpanMs)
	}
	if k.MaxGapMs != 2900 {
		t.Errorf("maxGapMs = %d, want 2900 (the widest of 2900 and 100)", k.MaxGapMs)
	}
}

// The clip that makes a generous capture gap safe: a hit landing before the
// victim RESPAWNED belongs to a life the killer already ended.
func TestTopKillsClipsAtVictimLifeStart(t *testing.T) {
	frags := []result.FragEntry{kill(7000, "A", "B", "rl")}
	hits := []result.DamageEntry{
		dmg(3500, "A", "B", "rl", 200), // B's PREVIOUS life
		dmg(6000, "A", "B", "rl", 50),
		dmg(7000, "A", "B", "rl", 50),
	}
	alive := map[string][]result.Interval{"B": {{Start: 0, End: 4000}, {Start: 5000, End: 20000}}}

	// 6000-3500 = 2500 <= the default gap, so without the clip the walk would
	// absorb the previous life's 200 and report 300.
	k := topKill(t, tkResult(frags, hits, alive), TopKillsOptions{})
	if k.Damage != 100 || k.Hits != 2 {
		t.Errorf("burst = %d dmg over %d hits, want 100/2 (the pre-respawn hit is another life's)", k.Damage, k.Hits)
	}
	if k.SpanMs != 1000 {
		t.Errorf("spanMs = %d, want 1000", k.SpanMs)
	}

	// A hit landing exactly ON the spawn instant is inside the new life: Alive
	// intervals are half-open [Start, End).
	onSpawn := append([]result.DamageEntry{}, hits...)
	onSpawn[0] = dmg(5000, "A", "B", "rl", 200)
	k = topKill(t, tkResult(frags, onSpawn, alive), TopKillsOptions{})
	if k.Damage != 300 || k.Hits != 3 {
		t.Errorf("burst = %d dmg over %d hits, want 300/3 (a hit at the spawn instant is this life's)", k.Damage, k.Hits)
	}
}

// A kill landing exactly at a respawn that follows a REAL dead gap is a
// spawn-frag on the new life: the floor is the new life's own start, never the
// previous life's. Only a TOUCHING death-and-respawn (zero gap) gives the
// shared instant to the ending life — the lifeSpan boundary rule.
func TestTopKillsSpawnFragAfterDeadGapFloorsAtTheNewLife(t *testing.T) {
	frags := []result.FragEntry{kill(5000, "A", "B", "rl")}
	hits := []result.DamageEntry{
		dmg(3000, "A", "B", "rl", 200), // B's previous life, 2000ms gap <= capture
		dmg(5000, "A", "B", "rl", 50),  // the spawn-frag hit
	}
	alive := map[string][]result.Interval{"B": {{Start: 0, End: 4000}, {Start: 5000, End: 20000}}}
	k := topKill(t, tkResult(frags, hits, alive), TopKillsOptions{})
	if k.Damage != 50 || k.Hits != 1 {
		t.Errorf("burst = %d dmg over %d hits, want 50/1 (the previous life must not leak into a spawn-frag)", k.Damage, k.Hits)
	}

	// The touching control: same shape with a zero-gap death+respawn at 5000
	// keeps the earlier life's floor, so the walk reaches its hits.
	touching := map[string][]result.Interval{"B": {{Start: 0, End: 5000}, {Start: 5000, End: 20000}}}
	k = topKill(t, tkResult(frags, hits, touching), TopKillsOptions{})
	if k.Damage != 250 || k.Hits != 2 {
		t.Errorf("burst = %d dmg over %d hits, want 250/2 (touching lives give the instant to the ending life)", k.Damage, k.Hits)
	}
}

// The join boundary is INCLUSIVE: a hit landing at exactly gapMs from the
// run's earliest hit joins it. The headline narrowing rule ("maxGapMs <= g
// reproduces the gap-g walk for kept rows") holds only with this edge.
func TestTopKillsGapBoundaryIsInclusive(t *testing.T) {
	frags := []result.FragEntry{kill(8000, "A", "B", "rl")}
	hits := []result.DamageEntry{
		dmg(5000, "A", "B", "rl", 100), // exactly 3000 before the killing hit
		dmg(8000, "A", "B", "rl", 60),
	}
	k := topKill(t, tkResult(frags, hits, nil), TopKillsOptions{})
	if k.Damage != 160 || k.Hits != 2 || k.MaxGapMs != 3000 {
		t.Errorf("burst = %d/%d hits maxGap %d, want 160/2/3000 (gap == gapMs joins)", k.Damage, k.Hits, k.MaxGapMs)
	}
}

// A duplicate display name keys the STREAMS name#slot while the frag and
// damage logs keep the bare name. The lives lookup resolves the collision by
// the kill's own death instant — the victim died at t, so the stream whose
// life ENDS at t is the victim's — and clips normally when exactly one
// candidate matches. When none (or two) match, the burst runs UNCLIPPED, the
// documented fallback. Both halves pinned so a later "fix" has to argue here.
func TestTopKillsDuplicateNameVictim(t *testing.T) {
	frags := []result.FragEntry{kill(7000, "A", "B", "rl")}
	hits := []result.DamageEntry{
		dmg(5000, "A", "B", "rl", 200), // before the resolved victim's respawn
		dmg(7000, "A", "B", "rl", 50),
	}
	r := tkResult(frags, hits, nil)
	// Two wire slots share the display name B: streams carry B#2 / B#3, and
	// no stream is keyed bare "B" (the collision shape timeline_streams
	// produces). B#2's life ends exactly at the kill — it is the victim, its
	// current life started at 6500, and the 5000 hit is another life's.
	r.Streams.Players = []result.PlayerStream{
		{Name: "A", Team: "red", Alive: []result.Interval{{Start: 0, End: 120000}}},
		{Name: "B#2", Team: "blue", Alive: []result.Interval{{Start: 0, End: 6000}, {Start: 6500, End: 7000}}},
		{Name: "B#3", Team: "blue", Alive: []result.Interval{{Start: 0, End: 120000}}},
	}
	k := topKill(t, r, TopKillsOptions{})
	if k.Damage != 50 || k.Hits != 1 {
		t.Errorf("burst = %d/%d hits, want 50/1 — the collision resolves by the death instant and clips", k.Damage, k.Hits)
	}

	// No candidate's life ends at the kill instant: unresolvable, unclipped.
	r.Streams.Players[1].Alive = []result.Interval{{Start: 0, End: 6000}, {Start: 6500, End: 120000}}
	k = topKill(t, r, TopKillsOptions{})
	if k.Damage != 250 || k.Hits != 2 {
		t.Errorf("burst = %d/%d hits, want 250/2 — unresolvable collision runs unclipped", k.Damage, k.Hits)
	}
}

// The degenerate first-life-starts-at-the-kill-instant case: its own start is
// the floor, not 0.
func TestTopKillsFirstLifeStartingAtTheKillInstant(t *testing.T) {
	frags := []result.FragEntry{kill(5000, "A", "B", "rl")}
	hits := []result.DamageEntry{
		dmg(4000, "A", "B", "rl", 200), // before B was ever alive
		dmg(5000, "A", "B", "rl", 50),
	}
	alive := map[string][]result.Interval{"B": {{Start: 5000, End: 20000}}}
	k := topKill(t, tkResult(frags, hits, alive), TopKillsOptions{})
	if k.Damage != 50 || k.Hits != 1 {
		t.Errorf("burst = %d/%d hits, want 50/1 (the first life's own start is the floor)", k.Damage, k.Hits)
	}
}

// The killing weapon is compared case-insensitively against the damage log's
// spelling, as the walk documents.
func TestTopKillsWeaponCompareIsCaseInsensitive(t *testing.T) {
	frags := []result.FragEntry{kill(5000, "A", "B", "rl")}
	hits := []result.DamageEntry{
		dmg(4600, "A", "B", "RL", 90),
		dmg(5000, "A", "B", "RL", 60),
	}
	k := topKill(t, tkResult(frags, hits, nil), TopKillsOptions{})
	if k.Damage != 150 || k.Hits != 2 {
		t.Errorf("burst = %d dmg over %d hits, want 150/2 (weapon tokens compare case-insensitively)", k.Damage, k.Hits)
	}
}

// A frame can land several same-weapon hits on the kill instant (splash onto a
// direct). All of them join the run at gap 0, and victimWep comes from the
// latest non-empty of them.
func TestTopKillsSameInstantMultiHit(t *testing.T) {
	frags := []result.FragEntry{kill(5000, "A", "B", "rl")}
	hits := []result.DamageEntry{
		dmg(5000, "A", "B", "rl", 70), // direct
		func() result.DamageEntry {
			e := dmg(5000, "A", "B", "rl", 40) // splash, later in the log
			e.IsSplash = true
			e.VictimWep = "rl"
			return e
		}(),
	}
	k := topKill(t, tkResult(frags, hits, nil), TopKillsOptions{})
	if k.Damage != 110 || k.Hits != 2 {
		t.Errorf("burst = %d dmg over %d hits, want 110/2 (same-instant hits all join)", k.Damage, k.Hits)
	}
	if k.SpanMs != 0 || k.MaxGapMs != 0 {
		t.Errorf("spanMs/maxGapMs = %d/%d, want 0/0", k.SpanMs, k.MaxGapMs)
	}
	if k.VictimWep != "rl" {
		t.Errorf("victimWep = %q, want %q (latest non-empty at the kill instant)", k.VictimWep, "rl")
	}
}

// A victim with no measurable liveness at all still yields rows: the demo-wide
// gate has already passed (someone has lives), and dropping a real kill to
// guard against a previous life the recording never saw would lose more than it
// protects.
func TestTopKillsVictimWithoutLivesIsNotDropped(t *testing.T) {
	frags := []result.FragEntry{kill(7000, "A", "B", "rl")}
	hits := []result.DamageEntry{
		dmg(6000, "A", "B", "rl", 50),
		dmg(7000, "A", "B", "rl", 50),
	}
	r := tkResult(frags, hits, map[string][]result.Interval{"B": nil})
	got := mustTK(t, r, TopKillsOptions{})
	if len(got.Kills) != 1 || got.Kills[0].Damage != 100 {
		t.Fatalf("kills = %+v, want one unclipped 100-damage burst", got.Kills)
	}
	if !got.Measured.Liveness {
		t.Error("measured.liveness = false, want true — the demo has lives, this victim does not")
	}
}

// Posthumous kills are the point, not a defect: the walk consults the victim's
// liveness and never the killer's.
func TestTopKillsKeepsKillsByAnAlreadyDeadKiller(t *testing.T) {
	frags := []result.FragEntry{kill(5300, "A", "B", "rl")}
	hits := []result.DamageEntry{
		dmg(4800, "A", "B", "rl", 90),
		dmg(5300, "A", "B", "rl", 60),
	}
	alive := map[string][]result.Interval{"A": {{Start: 0, End: 5000}}}
	got := mustTK(t, tkResult(frags, hits, alive), TopKillsOptions{})
	if len(got.Kills) != 1 {
		t.Fatalf("got %d rows, want 1 — a rocket in flight when its shooter died still kills", len(got.Kills))
	}
	if got.Kills[0].Damage != 150 || got.Kills[0].Hits != 2 {
		t.Errorf("burst = %d dmg over %d hits, want 150/2", got.Kills[0].Damage, got.Kills[0].Hits)
	}
}

// Positional kills carry no damage event, so they have no burst to rank. They
// are excluded from the RANKING and stay in the frag log, which is what the
// endpoint documents.
func TestTopKillsExcludesPositionalKills(t *testing.T) {
	frags := []result.FragEntry{
		kill(3000, "A", "B", "tele"),
		kill(4000, "A", "B", "stomp"),
		kill(9000, "A", "B", "rl"),
	}
	hits := []result.DamageEntry{dmg(9000, "A", "B", "rl", 120)}
	got := mustTK(t, tkResult(frags, hits, nil), TopKillsOptions{})
	if len(got.Kills) != 1 {
		t.Fatalf("got %d rows, want 1 (only the rocket kill has a burst): %+v", len(got.Kills), got.Kills)
	}
	if got.Kills[0].Weapon != "rl" || got.Kills[0].Time != 9000 {
		t.Errorf("row = %+v, want the rl kill at 9000", got.Kills[0])
	}
	// Explicitly asking for them is a vocabulary REJECTION, not a silent empty
	// list: the token is valid on /frags but structurally selects nothing here.
	_, err := TopKills(tkResult(frags, hits, nil), TopKillsOptions{Weapons: []string{"tele"}})
	if !errors.Is(err, ErrInvalidFilter) {
		t.Errorf("weapons=tele err = %v, want ErrInvalidFilter (dead token on this endpoint)", err)
	}
}

// The anchor is EXACT and has no tolerance window: a kill and its killing hit
// are stamped from the same MVD frame, so a same-weapon hit that lands even
// slightly before the frag is not the killing hit and the kill carries no burst.
// (Measured: all 1,866 corpus enemy kills have a hit at exactly the frag time;
// the only kills without one are positional.)
func TestTopKillsAnchorsExactlyOnTheFragInstant(t *testing.T) {
	frags := []result.FragEntry{kill(9000, "A", "B", "rl")}
	hits := []result.DamageEntry{dmg(8950, "A", "B", "rl", 120)}
	got := mustTK(t, tkResult(frags, hits, nil), TopKillsOptions{})
	if len(got.Kills) != 0 {
		t.Fatalf("kills = %+v, want none — no hit at the frag instant", got.Kills)
	}
}

// Suicides, teamkills and world kills are not bursts by anyone.
func TestTopKillsCountsEnemyKillsOnly(t *testing.T) {
	frags := []result.FragEntry{
		{Time: 3000, Killer: "B", Victim: "B", Weapon: "rl", IsSuicide: true},
		{Time: 4000, Killer: "A", Victim: "C", Weapon: "rl", IsTeamKill: true},
		{Time: 5000, Killer: "world", Victim: "B", Weapon: "lava"},
		kill(9000, "A", "B", "rl"),
	}
	hits := []result.DamageEntry{
		{Time: 3000, Attacker: "B", Victim: "B", Weapon: "rl", Damage: 200, IsSelf: true},
		{Time: 4000, Attacker: "A", Victim: "C", Weapon: "rl", Damage: 200, IsTeam: true},
		dmg(9000, "A", "B", "rl", 120),
	}
	got := mustTK(t, tkResult(frags, hits, nil), TopKillsOptions{})
	if len(got.Kills) != 1 || got.Kills[0].Time != 9000 {
		t.Fatalf("kills = %+v, want only the enemy kill at 9000", got.Kills)
	}
}

// victimWep is the victim's class at the KILLING hit, straight from that event.
func TestTopKillsVictimWep(t *testing.T) {
	frags := []result.FragEntry{kill(9000, "A", "B", "rl")}
	early := dmg(7000, "A", "B", "rl", 40)
	early.VictimWep = "sg"
	final := dmg(9000, "A", "B", "rl", 80)
	final.VictimWep = "rl"
	k := topKill(t, tkResult(frags, []result.DamageEntry{early, final}, nil), TopKillsOptions{})
	if k.VictimWep != "rl" {
		t.Errorf("victimWep = %q, want rl (the killing hit's class, not an earlier one)", k.VictimWep)
	}
}

// returnDamage is the victim's damage BACK over the contested window: closed at
// both ends, any weapon, no clip by the killer's liveness.
func TestTopKillsReturnDamageWindow(t *testing.T) {
	frags := []result.FragEntry{kill(10000, "A", "B", "rl")}
	hits := []result.DamageEntry{
		dmg(10000, "A", "B", "rl", 100),
		dmg(5999, "B", "A", "lg", 7),   // one ms outside the default window
		dmg(6000, "B", "A", "lg", 30),  // exactly on the lower edge: included
		dmg(10000, "B", "A", "rl", 20), // the mutual frag: included
	}
	r := tkResult(frags, hits, nil)

	k := topKill(t, r, TopKillsOptions{})
	if k.ReturnDamage != 50 {
		t.Errorf("returnDamage = %d, want 50 (the window is [t-contestedMs, t], closed)", k.ReturnDamage)
	}
	if k.Damage != 100 {
		t.Errorf("burst damage = %d, want 100 — the victim's own hits are not the killer's burst", k.Damage)
	}

	n := topKill(t, r, TopKillsOptions{ContestedMs: 3000})
	if n.ReturnDamage != 20 {
		t.Errorf("returnDamage at contestedMs=3000 = %d, want 20", n.ReturnDamage)
	}
}

// The damage family applies to the burst AND to returnDamage, so one response
// is one family. A nil Bounded means "equal to Damage", never zero.
func TestTopKillsDamageFamily(t *testing.T) {
	frags := []result.FragEntry{kill(10000, "A", "B", "rl")}
	hits := []result.DamageEntry{
		dmg(9000, "A", "B", "rl", 40), // no bounded value: bounded == raw
		bhit(10000, "A", "B", "rl", 300, 90),
		bhit(9500, "B", "A", "lg", 60, 25),
	}
	r := tkResult(frags, hits, nil)

	raw := mustTK(t, r, TopKillsOptions{Dmg: "raw"})
	if got := raw.Kills[0].Damage; got != 340 {
		t.Errorf("raw burst = %d, want 340", got)
	}
	if got := raw.Kills[0].ReturnDamage; got != 60 {
		t.Errorf("raw returnDamage = %d, want 60", got)
	}
	if raw.Dmg != "raw" {
		t.Errorf("dmg echo = %q, want raw", raw.Dmg)
	}

	b := mustTK(t, r, TopKillsOptions{Dmg: "bounded"})
	if got := b.Kills[0].Damage; got != 130 {
		t.Errorf("bounded burst = %d, want 130 (40 unbounded-equal + 90)", got)
	}
	if got := b.Kills[0].ReturnDamage; got != 25 {
		t.Errorf("bounded returnDamage = %d, want 25", got)
	}
	if b.Dmg != "bounded" || b.BoundedMode != "standard" {
		t.Errorf("dmg/boundedMode echo = %q/%q, want bounded/standard", b.Dmg, b.BoundedMode)
	}
}

// Every error path, in one place.
func TestTopKillsErrors(t *testing.T) {
	frags := []result.FragEntry{kill(10000, "A", "B", "rl")}
	hits := []result.DamageEntry{dmg(10000, "A", "B", "rl", 100)}

	noDamage := tkResult(frags, hits, nil)
	noDamage.Damage = nil
	noFrags := tkResult(frags, hits, nil)
	noFrags.Frags = nil
	noLiveness := tkResult(frags, hits, map[string][]result.Interval{"A": nil, "B": nil, "C": nil})
	noBounded := tkResult(frags, hits, nil)
	noBounded.Damage.BoundedMode = ""

	cases := []struct {
		name string
		r    *result.Result
		opts TopKillsOptions
		want error
	}{
		{"nil result", nil, TopKillsOptions{}, ErrUnavailable},
		{"no damage stream", noDamage, TopKillsOptions{}, ErrUnavailable},
		{"no frag log", noFrags, TopKillsOptions{}, ErrUnavailable},
		{"no measurable liveness", noLiveness, TopKillsOptions{}, ErrUnavailable},
		{"bounded unavailable", noBounded, TopKillsOptions{Dmg: "bounded"}, ErrBoundedUnavailable},
		{"unknown dmg", tkResult(frags, hits, nil), TopKillsOptions{Dmg: "nope"}, ErrInvalidFilter},
		{"dmg=both", tkResult(frags, hits, nil), TopKillsOptions{Dmg: "both"}, ErrInvalidFilter},
		{"unknown weapon", tkResult(frags, hits, nil), TopKillsOptions{Weapons: []string{"banana"}}, ErrInvalidFilter},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := TopKills(c.r, c.opts)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

// tkFive is five kills of descending burst damage, alternating killers, for the
// filter and limit tests.
func tkFive() *result.Result {
	var frags []result.FragEntry
	var hits []result.DamageEntry
	killers := []string{"A", "B", "A", "B", "A"}
	victims := []string{"B", "A", "B", "A", "B"}
	weapons := []string{"rl", "rl", "lg", "rl", "lg"}
	for i, k := range killers {
		t := int32(10000 * (i + 1))
		frags = append(frags, kill(t, k, victims[i], weapons[i]))
		hits = append(hits, dmg(t, k, victims[i], weapons[i], 250-50*i))
	}
	return tkResult(frags, hits, nil)
}

func tkDamages(v *TopKillsView) []int {
	out := make([]int, 0, len(v.Kills))
	for _, k := range v.Kills {
		out = append(out, k.Damage)
	}
	return out
}

func TestTopKillsRanksByDamage(t *testing.T) {
	got := mustTK(t, tkFive(), TopKillsOptions{})
	if want := []int{250, 200, 150, 100, 50}; !reflect.DeepEqual(tkDamages(got), want) {
		t.Fatalf("damages = %v, want %v (descending)", tkDamages(got), want)
	}
	for i, k := range got.Kills {
		if k.Rank != i+1 {
			t.Errorf("row %d rank = %d, want %d", i, k.Rank, i+1)
		}
	}
}

func TestTopKillsFilters(t *testing.T) {
	r := tkFive()
	cases := []struct {
		name string
		opts TopKillsOptions
		want []int
	}{
		{"players filters the KILLER", TopKillsOptions{Players: []string{"B"}}, []int{200, 100}},
		{"weapons filters the killing weapon", TopKillsOptions{Weapons: []string{"lg"}}, []int{150, 50}},
		{"minDamage filters the burst", TopKillsOptions{MinDamage: 150}, []int{250, 200, 150}},
		{"from bounds the kill time", TopKillsOptions{From: 30000}, []int{150, 100, 50}},
		{"to bounds the kill time", TopKillsOptions{To: 20000}, []int{250, 200}},
		{"from+to select a range", TopKillsOptions{From: 20000, To: 40000}, []int{200, 150, 100}},
		{"an inverted range selects nothing", TopKillsOptions{From: 40000, To: 20000}, []int{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tkDamages(mustTK(t, r, c.opts))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("damages = %v, want %v", got, c.want)
			}
		})
	}
}

// Limit follows /top-windows' shipped semantics exactly: 0 is the default, a
// negative is uncapped, and the echo says which applied.
func TestTopKillsLimit(t *testing.T) {
	r := tkFive()

	all := mustTK(t, r, TopKillsOptions{})
	if len(all.Kills) != 5 || all.Limit != defaultTopKillLimit {
		t.Errorf("default: %d rows, limit echo %d; want 5 rows and %d", len(all.Kills), all.Limit, defaultTopKillLimit)
	}

	two := mustTK(t, r, TopKillsOptions{Limit: 2})
	if want := []int{250, 200}; !reflect.DeepEqual(tkDamages(two), want) {
		t.Errorf("limit=2 damages = %v, want %v", tkDamages(two), want)
	}

	unc := mustTK(t, r, TopKillsOptions{Limit: -1})
	if len(unc.Kills) != 5 || unc.Limit != -1 {
		t.Errorf("limit=-1: %d rows, echo %d; want 5 rows uncapped", len(unc.Kills), unc.Limit)
	}

	if got := mustTK(t, r, TopKillsOptions{Limit: 10000}); got.Limit != topKillMaxLimit {
		t.Errorf("limit echo = %d, want the %d clamp", got.Limit, topKillMaxLimit)
	}
}

// The resolved gap and contested window are echoed, not the caller's input:
// they are what the numbers mean.
func TestTopKillsEchoesResolvedParameters(t *testing.T) {
	r := tkFive()
	def := mustTK(t, r, TopKillsOptions{})
	if def.GapMs != defaultTopKillGapMs || def.ContestedMs != defaultTopKillContestedMs {
		t.Errorf("defaults echoed as %d/%d, want %d/%d",
			def.GapMs, def.ContestedMs, defaultTopKillGapMs, defaultTopKillContestedMs)
	}
	cl := mustTK(t, r, TopKillsOptions{GapMs: 999999, ContestedMs: 999999})
	if cl.GapMs != maxTopKillGapMs || cl.ContestedMs != maxTopKillContestedMs {
		t.Errorf("clamped echo = %d/%d, want %d/%d",
			cl.GapMs, cl.ContestedMs, maxTopKillGapMs, maxTopKillContestedMs)
	}
}

// Ties are broken to a total order, so two runs of the same query are
// byte-identical — including the case the first three keys cannot separate: one
// killer landing two same-damage kills on the same instant.
func TestTopKillsIsDeterministicUnderTies(t *testing.T) {
	frags := []result.FragEntry{
		kill(20000, "B", "A", "rl"),
		kill(10000, "A", "B", "rl"),
		kill(10000, "A", "C", "rl"),
		kill(10000, "C", "A", "rl"), // second killer on the same instant: killer key
		kill(30000, "A", "B", "lg"),
	}
	hits := []result.DamageEntry{
		dmg(20000, "B", "A", "rl", 100),
		dmg(10000, "A", "B", "rl", 100),
		dmg(10000, "A", "C", "rl", 100),
		dmg(10000, "C", "A", "rl", 100),
		dmg(30000, "A", "B", "lg", 100),
	}
	r := tkResult(frags, hits, nil)

	first := mustTK(t, r, TopKillsOptions{})
	second := mustTK(t, r, TopKillsOptions{})
	if !reflect.DeepEqual(first.Kills, second.Kills) {
		t.Fatalf("two identical queries disagreed:\n%+v\n%+v", first.Kills, second.Kills)
	}
	type row struct {
		t              int32
		killer, victim string
	}
	var got []row
	for _, k := range first.Kills {
		got = append(got, row{k.Time, k.Killer, k.Victim})
	}
	want := []row{{10000, "A", "B"}, {10000, "A", "C"}, {10000, "C", "A"}, {20000, "B", "A"}, {30000, "A", "B"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tie order = %+v, want %+v (time, then killer, then victim)", got, want)
	}
}

// The envelope's measured block is the demo's, not the response's — and the
// frags flag is the analyzer's stored verdict, never re-derived from the log.
func TestTopKillsMeasuredEnvelope(t *testing.T) {
	r := tkFive()
	got := mustTK(t, r, TopKillsOptions{})
	if !got.Measured.Frags || !got.Measured.Damage || !got.Measured.Liveness {
		t.Errorf("measured = %+v, want frags/damage/liveness true", got.Measured)
	}
	if got.Measured.Shots || got.Measured.Locs || got.Measured.Items {
		t.Errorf("measured = %+v, want shots/locs/items false on this fixture", got.Measured)
	}

	r.Frags.KillsMeasured = false
	if unmeasured := mustTK(t, r, TopKillsOptions{}); unmeasured.Measured.Frags {
		t.Error("measured.frags = true where the analyzer's stored verdict says kill attribution was not observable")
	}
}
