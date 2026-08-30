package view

import (
	"reflect"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func ch(t int32, v int16) result.ChangeI16 { return result.ChangeI16{T: t, V: v} }
func iv(s, e int32) result.Interval        { return result.Interval{Start: s, End: e} }
func ip(v int) *int                        { return &v }

// highlightTestResult is one synthetic match exercising every detector:
//
//   - a discharge at 1000 by "disc" (blue) that killed "foe1" (red) and
//     "mate" (blue, via a cause-less team-kill print), hurt "foe2" (red)
//     and killed disc themself — with a damage log;
//   - a quadbore at 5000 by "qb" (blue, quad since 2000, one quad frag,
//     the same rocket killing "foe2"); "eq" bores at 9000 with a quad that
//     ended 50 ms earlier (still counts); "nq" bores with no quad (no row);
//   - telefrags: "tp" (blue) → "foe1" at 20000 (stacked 118+180 ra, rl +
//     quad), "tp" → "foe2" at 30000 (same-frame spawn), two deflects by
//     "tp" at 40000 (dtTELE2, pent holder "pent" resolved by interval) and
//     41000 (dtTELE3, deflector named), and an unpaired team telefrag on
//     "mate" at 50000;
//   - airgib wrapping is exercised separately (TestComputeHighlights_AirgibsWrapped).
func highlightTestResult() *result.Result {
	players := []result.PlayerStream{
		{Name: "disc", Team: "blue",
			Health:       []result.ChangeI16{ch(900, 24), ch(1000, -99)},
			Cells:        []result.ChangeI16{ch(500, 21), ch(1000, 0)},
			LG:           []result.Interval{iv(0, 1000)},
			ActiveWeapon: []result.ChangeI16{ch(0, 64)},
			Deaths:       []int32{1000},
		},
		{Name: "mate", Team: "blue",
			Health: []result.ChangeI16{ch(800, 70), ch(40000, 50)},
		},
		{Name: "foe1", Team: "red",
			Health:    []result.ChangeI16{ch(900, 60), ch(19000, 118), ch(20000, -99)},
			Armor:     []result.ChangeI16{ch(18000, 180)},
			ArmorType: []result.ChangeStr{{T: 18000, V: "ra"}},
			RL:        []result.Interval{iv(16000, 20000)},
			Quad:      []result.Interval{iv(19500, 20000)},
			Spawns:    []int32{15000},
			Position: &result.PositionTrack{T: []int32{19990}, X: []float32{0}, Y: []float32{0}, Z: []float32{0},
				Li: []int16{1}},
		},
		{Name: "foe2", Team: "red",
			Health: []result.ChangeI16{ch(500, 90), ch(25000, -99)},
			Spawns: []int32{30000},
		},
		{Name: "pent", Team: "red",
			Health: []result.ChangeI16{ch(100, 100)},
			Pent:   []result.Interval{iv(35000, 60000)},
		},
		{Name: "tp", Team: "blue",
			Health: []result.ChangeI16{ch(100, 100)},
		},
		{Name: "qb", Team: "blue",
			Health:    []result.ChangeI16{ch(4000, 100)},
			Armor:     []result.ChangeI16{ch(4000, 150)},
			ArmorType: []result.ChangeStr{{T: 4000, V: "ya"}},
			Quad:      []result.Interval{iv(2000, 5000)},
			RL:        []result.Interval{iv(2500, 5000)},
		},
		{Name: "eq", Team: "blue", Quad: []result.Interval{iv(8000, 8950)}},
		{Name: "nq", Team: "blue"},
	}
	frags := []result.FragEntry{
		// discharge cluster at 1000
		{Time: 1000, Killer: "disc", Victim: "disc", Weapon: "lg", IsSuicide: true, Cause: "discharge"},
		{Time: 1000, Killer: "disc", Victim: "foe1", Weapon: "lg", Cause: "discharge"},
		{Time: 1010, Killer: "disc", Victim: "mate", Weapon: "teamkill", IsTeamKill: true},
		// quadbores
		{Time: 3000, Killer: "qb", Victim: "foe1", Weapon: "rl"},
		{Time: 5000, Killer: "qb", Victim: "qb", Weapon: "rl", IsSuicide: true},
		{Time: 5000, Killer: "qb", Victim: "foe2", Weapon: "rl"},
		{Time: 9000, Killer: "eq", Victim: "eq", Weapon: "rl", IsSuicide: true},
		{Time: 11000, Killer: "nq", Victim: "nq", Weapon: "gl", IsSuicide: true},
		// telefrags
		{Time: 20000, Killer: "tp", Victim: "foe1", Weapon: "tele"},
		{Time: 30000, Killer: "tp", Victim: "foe2", Weapon: "tele"},
		{Time: 40000, Killer: "tp", Victim: "tp", Weapon: "tele", IsSuicide: true, Cause: "deflect"},
		{Time: 41000, Killer: "tp", Victim: "tp", Weapon: "tele", IsSuicide: true, Cause: "deflect", Deflector: "foe1"},
	}
	unpaired := []result.FragEntry{
		{Time: 50000, Killer: "teammate", Victim: "mate", Weapon: "tele", IsTeamKill: true},
		{Time: 1005, Killer: "disc", Victim: "teammate", Weapon: "teamkill", IsTeamKill: true},
	}
	dmg := []result.DamageEntry{
		{Time: 1000, Attacker: "disc", Victim: "disc", Weapon: "lg", Damage: 367, IsSplash: true, IsSelf: true},
		{Time: 1000, Attacker: "disc", Victim: "foe1", Weapon: "lg", Damage: 533, IsSplash: true},
		{Time: 1000, Attacker: "disc", Victim: "foe2", Weapon: "lg", Damage: 100, IsSplash: true},
		{Time: 1000, Attacker: "disc", Victim: "mate", Weapon: "lg", Damage: 200, IsSplash: true, IsTeam: true},
		{Time: 2000, Attacker: "tp", Victim: "foe1", Weapon: "lg", Damage: 30}, // a beam hit: never a discharge
		{Time: 4990, Attacker: "qb", Victim: "qb", Weapon: "rl", Damage: 120, IsSplash: true, IsSelf: true},
	}
	return &result.Result{
		Streams: &result.Streams{Players: players},
		Frags:   &result.FragResult{Frags: frags, Unpaired: unpaired},
		Damage: &result.DamageResult{Source: result.DamageSourceKTX, Events: dmg,
			Telefrags: []result.PositionalKill{{Time: 20000, Attacker: "tp", Victim: "foe1", Bounded: ip(298)}}},
		TimelineAnalysis: &result.TimelineAnalysisResult{
			LocTable:      []string{"", "RA"},
			PlayerUserIDs: map[string]int{"tp": 5, "foe1": 7},
		},
	}
}

func victimByName(e result.HighlightEvent, name string) *result.HighlightPlayer {
	for i := range e.Victims {
		if e.Victims[i].Name == name {
			return &e.Victims[i]
		}
	}
	return nil
}

func TestComputeHighlights_NilWithoutInputs(t *testing.T) {
	if got := ComputeHighlights(nil, HighlightsOptions{}); got != nil {
		t.Errorf("nil result: got %+v", got)
	}
	if got := ComputeHighlights(&result.Result{Frags: &result.FragResult{}}, HighlightsOptions{}); got != nil {
		t.Errorf("no streams: got %+v", got)
	}
	r := highlightTestResult()
	r.Frags = nil
	if got := ComputeHighlights(r, HighlightsOptions{}); got != nil {
		t.Errorf("no frag log: got %+v", got)
	}
}

func TestComputeHighlights_Discharge(t *testing.T) {
	h := ComputeHighlights(highlightTestResult(), HighlightsOptions{})
	if len(h.Discharges) != 1 {
		t.Fatalf("discharges = %d, want 1: %+v", len(h.Discharges), h.Discharges)
	}
	d := h.Discharges[0]
	if d.Kind != "discharge" || d.Time != 1000 {
		t.Errorf("kind/time = %s/%d", d.Kind, d.Time)
	}
	if d.Cells == nil || *d.Cells != 21 {
		t.Errorf("cells = %v, want 21 (the sample before the dump to 0)", d.Cells)
	}
	if !d.Actor.Killed || d.Actor.Damage != 367 || d.Actor.Relation != "self" {
		t.Errorf("actor = %+v, want killed, self damage 367", d.Actor)
	}
	if d.Actor.Health == nil || *d.Actor.Health != 24 || d.Actor.ActiveWeapon != "lg" || !reflect.DeepEqual(d.Actor.Weapons, []string{"lg"}) {
		t.Errorf("actor state = %+v, want health 24 (pre-death), lg wielded and held", d.Actor)
	}
	if d.EnemyKills != 1 || d.TeamKills != 2 || d.Damage != 833 {
		t.Errorf("counters enemy=%d team=%d damage=%d, want 1/2/833", d.EnemyKills, d.TeamKills, d.Damage)
	}
	if !reflect.DeepEqual(d.Sources, []string{"frags", "damage"}) {
		t.Errorf("sources = %v", d.Sources)
	}
	if v := victimByName(d, "foe1"); v == nil || !v.Killed || v.Damage != 533 || v.Relation != "enemy" || v.Health == nil || *v.Health != 60 {
		t.Errorf("foe1 = %+v, want killed enemy, 533 damage, health 60", v)
	}
	if v := victimByName(d, "mate"); v == nil || !v.Killed || v.Damage != 200 || v.Relation != "team" {
		t.Errorf("mate = %+v, want killed team (folded from the cause-less teamkill print) with 200 damage", v)
	}
	if v := victimByName(d, "foe2"); v == nil || v.Killed || v.Damage != 100 || v.Relation != "enemy" {
		t.Errorf("foe2 = %+v, want hurt-not-killed enemy with 100 damage", v)
	}
	if v := victimByName(d, "teammate"); v == nil || !v.Killed || v.Relation != "team" || v.StateSource != "" {
		t.Errorf("placeholder = %+v, want a killed team row with no state", v)
	}
	// Killed rows first, then by damage.
	if d.Victims[0].Name != "foe1" || d.Victims[1].Name != "mate" || d.Victims[len(d.Victims)-1].Name != "foe2" {
		t.Errorf("victim order = %v", func() []string {
			var n []string
			for _, v := range d.Victims {
				n = append(n, v.Name)
			}
			return n
		}())
	}
}

// A discharge with no damage log still comes out of the obituaries alone,
// and one with no obituary (nobody died) still comes out of the log alone.
func TestComputeHighlights_DischargeEvidenceEitherWay(t *testing.T) {
	r := highlightTestResult()
	r.Damage = nil
	h := ComputeHighlights(r, HighlightsOptions{})
	if len(h.Discharges) != 1 || !reflect.DeepEqual(h.Discharges[0].Sources, []string{"frags"}) {
		t.Fatalf("frags-only: %+v", h.Discharges)
	}
	if d := h.Discharges[0]; d.EnemyKills != 1 || d.TeamKills != 2 || d.Damage != 0 || victimByName(d, "foe2") != nil {
		t.Errorf("frags-only discharge = %+v, want the three kills and no damage figures", d)
	}

	r = highlightTestResult()
	r.Frags.Frags = nil
	r.Frags.Unpaired = nil
	h = ComputeHighlights(r, HighlightsOptions{})
	if len(h.Discharges) != 1 || !reflect.DeepEqual(h.Discharges[0].Sources, []string{"damage"}) {
		t.Fatalf("damage-only: %+v", h.Discharges)
	}
	if d := h.Discharges[0]; d.EnemyKills != 0 || d.Actor.Killed || d.Damage != 833 || len(d.Victims) != 3 {
		t.Errorf("damage-only discharge = %+v, want three hurt victims, nobody killed", d)
	}
}

func TestComputeHighlights_Quadbore(t *testing.T) {
	h := ComputeHighlights(highlightTestResult(), HighlightsOptions{})
	if len(h.Quadbores) != 2 {
		t.Fatalf("quadbores = %d, want 2 (qb, eq; nq had no quad): %+v", len(h.Quadbores), h.Quadbores)
	}
	// Sorted by quad held ascending: eq (950 ms) before qb (3000 ms).
	if h.Quadbores[0].Actor.Name != "eq" || h.Quadbores[0].QuadHeldMs != 1000 {
		t.Errorf("first = %+v, want eq with 1000 ms held (quad ended 50 ms before the death still counts)", h.Quadbores[0])
	}
	q := h.Quadbores[1]
	if q.Actor.Name != "qb" || q.Weapon != "rl" || q.QuadHeldMs != 3000 || q.QuadFrags != 1 {
		t.Errorf("qb = %+v, want rl, 3000 ms held, 1 quad frag", q)
	}
	if !q.Actor.Killed || q.Actor.Damage != 120 || q.Actor.Stack == nil || *q.Actor.Stack != 250 || q.Actor.ArmorType != "ya" {
		t.Errorf("qb actor = %+v, want killed, 120 self damage, stack 250 ya", q.Actor)
	}
	if !reflect.DeepEqual(q.Actor.Powerups, []string{"quad"}) || !reflect.DeepEqual(q.Actor.Weapons, []string{"rl"}) {
		t.Errorf("qb state = powerups %v weapons %v", q.Actor.Powerups, q.Actor.Weapons)
	}
	if len(q.Victims) != 1 || q.Victims[0].Name != "foe2" || !q.Victims[0].Killed || q.EnemyKills != 1 {
		t.Errorf("qb victims = %+v, want foe2 killed by the same rocket", q.Victims)
	}
	if !reflect.DeepEqual(q.Sources, []string{"frags", "damage"}) {
		t.Errorf("sources = %v", q.Sources)
	}
}

func TestComputeHighlights_Telefrags(t *testing.T) {
	h := ComputeHighlights(highlightTestResult(), HighlightsOptions{})
	if len(h.Telefrags) != 5 {
		t.Fatalf("telefrags = %d, want 5: %+v", len(h.Telefrags), h.Telefrags)
	}
	// Stack-sorted: foe1 (298), foe2 (100, spawn), mate (50), then the two
	// deflects (no killed victim) by time.
	names := []string{}
	for _, e := range h.Telefrags {
		names = append(names, e.Actor.Name+"/"+e.TeleKind)
	}
	if want := []string{"tp/telefrag", "tp/telefrag", "teammate/telefrag", "tp/deflect", "tp/deflect"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("order = %v, want %v", names, want)
	}

	e := h.Telefrags[0]
	v := e.Victims[0]
	if v.Name != "foe1" || !v.Killed || v.Relation != "enemy" || v.Stack == nil || *v.Stack != 298 || *v.Health != 118 || *v.Armor != 180 || v.ArmorType != "ra" {
		t.Errorf("foe1 = %+v, want the pre-death 118/180 ra stack", v)
	}
	if v.StateSource != "stream" || !reflect.DeepEqual(v.Weapons, []string{"rl"}) || !reflect.DeepEqual(v.Powerups, []string{"quad"}) || v.Loc != "RA" || v.UserID != 7 {
		t.Errorf("foe1 state = %+v, want stream-sourced, rl, quad (ending at the death still counts), loc RA, userid 7", v)
	}
	if v.Damage != 298 || e.Damage != 298 || !reflect.DeepEqual(e.Sources, []string{"frags", "damage"}) {
		t.Errorf("bounded cross-check: victim damage %d event damage %d sources %v", v.Damage, e.Damage, e.Sources)
	}
	if e.Actor.Name != "tp" || e.Actor.UserID != 5 || e.Actor.Killed || e.EnemyKills != 1 {
		t.Errorf("actor = %+v enemyKills %d", e.Actor, e.EnemyKills)
	}

	// Same-frame spawn: the last health sample (-99 at 25000) predates the
	// spawn at 30000, so the victim reads the spawn state.
	v = h.Telefrags[1].Victims[0]
	if v.Name != "foe2" || v.StateSource != "spawn" || *v.Health != 100 || *v.Armor != 0 || *v.Stack != 100 {
		t.Errorf("foe2 = %+v, want the reconstructed spawn state", v)
	}

	// Unpaired team telefrag: placeholder actor, team relation.
	e = h.Telefrags[2]
	if e.Actor.Name != "teammate" || e.Actor.StateSource != "" || e.Victims[0].Name != "mate" || e.Victims[0].Relation != "team" || e.TeamKills != 1 {
		t.Errorf("unpaired = %+v", e)
	}

	// Deflects: the teleporter died; the pent holder survives on the row.
	e = h.Telefrags[3]
	if !e.Actor.Killed || e.EnemyKills != 0 || len(e.Victims) != 1 || e.Victims[0].Name != "pent" || !e.Victims[0].Survived || e.Victims[0].Killed || !reflect.DeepEqual(e.Victims[0].Powerups, []string{"pent"}) {
		t.Errorf("dtTELE2 deflect = %+v, want pent (the one pent holder) surviving", e)
	}
	e = h.Telefrags[4]
	if len(e.Victims) != 1 || e.Victims[0].Name != "foe1" || !e.Victims[0].Survived {
		t.Errorf("dtTELE3 deflect = %+v, want the named deflector foe1 surviving", e)
	}
}

// Two simultaneous pent holders make the dtTELE2 survivor ambiguous; the
// row then names nobody rather than guessing.
func TestComputeHighlights_DeflectAmbiguousPentHolder(t *testing.T) {
	r := highlightTestResult()
	r.Streams.Players[3].Pent = []result.Interval{iv(39000, 41000)} // foe2 also holds pent at 40000
	h := ComputeHighlights(r, HighlightsOptions{})
	for _, e := range h.Telefrags {
		if e.Time == 40000 && len(e.Victims) != 0 {
			t.Errorf("ambiguous deflect named %+v", e.Victims)
		}
	}
}

func TestComputeHighlights_AirgibsWrapped(t *testing.T) {
	c := newHighlightCtx(highlightTestResult())
	got := c.airgibs([]result.AirgibEvent{{Time: 60000, Attacker: "tp", Victim: "foe1", Height: 150,
		HeightAboveAttacker: 120, Lethal: true, Damage: 110, Loc: "MID"}})
	if len(got) != 1 {
		t.Fatalf("airgibs = %+v", got)
	}
	a := got[0]
	if a.Kind != "airgib" || a.Height != 150 || a.HeightAboveAttacker != 120 || a.Damage != 110 || a.EnemyKills != 1 {
		t.Errorf("airgib = %+v", a)
	}
	v := a.Victims[0]
	if v.Name != "foe1" || !v.Killed || v.Loc != "MID" || v.Damage != 110 || v.StateSource != "stream" {
		t.Errorf("airgib victim = %+v, want foe1 killed at the detector's loc MID with stream state", v)
	}
	if !reflect.DeepEqual(a.Sources, []string{"frags", "damage"}) {
		t.Errorf("sources = %v", a.Sources)
	}
}

func TestFilterHighlights(t *testing.T) {
	h := ComputeHighlights(highlightTestResult(), HighlightsOptions{})

	env, err := FilterHighlights(h, HighlightsOptions{Kinds: []string{"telefrag"}}, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(env.Telefrags) != 5 || len(env.Discharges) != 0 || env.Discharges == nil || env.Quadbores == nil || env.Airgibs == nil {
		t.Errorf("kinds=telefrag: %+v", env)
	}
	if !reflect.DeepEqual(env.Kinds, []string{"telefrag"}) || env.PreMs != 100 || env.TimeUnit != UnitMs {
		t.Errorf("envelope echo: kinds %v preMs %d unit %v", env.Kinds, env.PreMs, env.TimeUnit)
	}

	env, err = FilterHighlights(h, HighlightsOptions{Players: []string{"foe2"}}, 100)
	if err != nil {
		t.Fatal(err)
	}
	// foe2 is a discharge victim, the quadbore's co-victim, and a telefrag victim.
	if len(env.Discharges) != 1 || len(env.Quadbores) != 1 || len(env.Telefrags) != 1 || len(env.Airgibs) != 0 {
		t.Errorf("players=foe2: d=%d q=%d t=%d a=%d", len(env.Discharges), len(env.Quadbores), len(env.Telefrags), len(env.Airgibs))
	}
	if !reflect.DeepEqual(env.Kinds, HighlightKinds) {
		t.Errorf("default kinds echo = %v", env.Kinds)
	}

	if _, err := FilterHighlights(h, HighlightsOptions{Kinds: []string{"bogus"}}, 100); err == nil {
		t.Error("bogus kind accepted")
	}
	env, _ = FilterHighlights(nil, HighlightsOptions{}, 0)
	if env.Discharges == nil || env.Telefrags == nil {
		t.Errorf("nil catalogue must still give empty lists: %+v", env)
	}
}

func TestHighlightsAccessor(t *testing.T) {
	if _, err := Highlights(&result.Result{}); err != ErrUnavailable {
		t.Errorf("absent section: err = %v, want ErrUnavailable", err)
	}
	h := &result.HighlightsResult{}
	got, err := Highlights(&result.Result{Highlights: h})
	if err != nil || got != h {
		t.Errorf("present section: %v %v", got, err)
	}
}
