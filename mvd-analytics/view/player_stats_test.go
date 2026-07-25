package view

import (
	"errors"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// storedResult is a two-player teamplay Result carrying the derived
// section the analyzer would have stored, plus (optionally) a KTX block.
func intp(v int) *int { return &v }

func storedResult(withKTX bool) *result.Result {
	xfer, xferSelf := 2, 1
	r := &result.Result{
		PlayerStats: &result.PlayerStatsResult{
			Players: []result.PlayerStatsRow{
				{
					Name: "alpha", Team: "red",
					Window: result.PlayerStatsWindow{MatchMs: 600000, PresentMs: 600000, AliveMs: 500000, DeadMs: 100000},
					Score:  result.PlayerStatsScore{Src: result.SrcDerived, Frags: 30, Kills: 32, Deaths: 20},
					Damage: &result.PlayerStatsDamage{Src: result.SrcDerived, Given: 4000, Taken: intp(5500), GivenTeam: 100, GivenSelf: 50, EnemyWeapons: 3000},
					Pickups: &result.PlayerStatsPickups{Src: result.SrcDerived, ByKind: map[string]result.PlayerStatsPickup{
						"ra":     {Took: 5},
						"rl":     {Took: 3, TotalTook: 4, Dropped: 2, Xfer: &xfer, XferSelf: &xferSelf},
						"shells": {Took: 12},
					}},
					Hold: result.PlayerStatsHold{Src: result.SrcDerived, Armor: map[string]result.HoldStat{
						"ra":   {Ms: 129000, Runs: 4},
						"none": {Ms: 200000, Runs: 9},
					}},
				},
				{
					Name: "beta", Team: "blue",
					Window: result.PlayerStatsWindow{MatchMs: 600000, PresentMs: 600000, AliveMs: 480000, DeadMs: 120000},
					Score:  result.PlayerStatsScore{Src: result.SrcDerived, Frags: 20, Kills: 21, Deaths: 30},
					Damage: &result.PlayerStatsDamage{Src: result.SrcDerived, Given: 3000, Taken: intp(6000)},
					Hold:   result.PlayerStatsHold{Src: result.SrcDerived},
				},
			},
			Teams: []result.PlayerStatsRow{
				{
					Name:   "red",
					Window: result.PlayerStatsWindow{MatchMs: 600000, PresentMs: 600000, AliveMs: 500000},
					Score:  result.PlayerStatsScore{Src: result.SrcDerived, Frags: 30, Kills: 32, Deaths: 20},
					Damage: &result.PlayerStatsDamage{Src: result.SrcDerived, Given: 4000, Taken: intp(5500)},
					Hold:   result.PlayerStatsHold{Src: result.SrcDerived},
				},
				{
					Name:   "blue",
					Window: result.PlayerStatsWindow{MatchMs: 600000, PresentMs: 600000, AliveMs: 480000},
					Score:  result.PlayerStatsScore{Src: result.SrcDerived, Frags: 20, Kills: 21, Deaths: 30},
					Damage: &result.PlayerStatsDamage{Src: result.SrcDerived, Given: 3000, Taken: intp(6000)},
					Hold:   result.PlayerStatsHold{Src: result.SrcDerived},
				},
			},
			Sources: result.PlayerStatsSources{Score: result.SrcDerived, Damage: result.SrcDerived, Pickups: result.SrcDerived, Hold: result.SrcDerived},
		},
	}
	if withKTX {
		r.DemoInfo = &result.DemoInfoResult{Players: []result.DemoInfoPlayer{{
			Name: "alpha", Team: "red", Ping: 13, Handicap: 0, Login: "alpha@qw",
			Stats: &result.DemoInfoStats{Frags: 999, Kills: 999, Deaths: 999},
			Dmg:   &result.DemoInfoDmg{Given: 4321, Taken: 5000, Team: 111, Self: 55, EnemyWeapons: 3100, TakenToDie: 250},
			Weapons: map[string]*result.DemoInfoWeapon{
				"rl": {
					Acc:     &result.DemoInfoAcc{Attacks: 90, Hits: 40, Real: 33, Virtual: 7},
					Pickups: &result.DemoInfoPickups{Taken: 7, TotalTaken: 9, Dropped: 3},
				},
			},
			Items: map[string]*result.DemoInfoItem{
				"ra":         {Took: 8, Time: 213},
				"health_100": {Took: 4},
				"q":          {Took: 2, Time: 60},
			},
		}}}
	}
	return r
}

// mustPlayerStats unwraps the accessor in the happy path.
func mustPlayerStats(t *testing.T, r *result.Result, opts PlayerStatsOptions) *result.PlayerStatsResult {
	t.Helper()
	ps, err := PlayerStats(r, opts)
	if err != nil {
		t.Fatalf("PlayerStats: unexpected error %v", err)
	}
	return ps
}

func TestPlayerStatsNoKTXStaysDerived(t *testing.T) {
	got := mustPlayerStats(t, storedResult(false), PlayerStatsOptions{})
	if got.Sources.Damage != result.SrcDerived {
		t.Errorf("damage src = %q, want derived", got.Sources.Damage)
	}
	if got.Players[0].Ping != 0 {
		t.Error("ping present without a KTX block")
	}
}

func TestPlayerStatsKTXOverlay(t *testing.T) {
	r := storedResult(true)
	got := mustPlayerStats(t, r, PlayerStatsOptions{})
	alpha := got.Players[0]

	// Identity passthrough.
	if alpha.Ping != 13 || alpha.Login != "alpha@qw" {
		t.Errorf("identity not lifted: ping=%d login=%q", alpha.Ping, alpha.Login)
	}
	// Score is NEVER overlaid — KTX's 999s must not appear.
	if alpha.Score.Frags != 30 || alpha.Score.Kills != 32 || alpha.Score.Deaths != 20 {
		t.Errorf("score was overlaid from KTX: %+v", alpha.Score)
	}
	if got.Sources.Score != result.SrcDerived {
		t.Errorf("score src = %q, want derived", got.Sources.Score)
	}
	// Damage: KTX wins on given/team/self/ewep, and adds its KTX-only fields.
	if alpha.Damage.Src != result.SrcKTX || alpha.Damage.Given != 4321 ||
		alpha.Damage.GivenTeam != 111 || alpha.Damage.GivenSelf != 55 || alpha.Damage.EnemyWeapons != 3100 {
		t.Errorf("damage not overlaid: %+v", alpha.Damage)
	}
	// taken keeps the DERIVED all-sources value; KTX's enemy-only number
	// lands in takenEnemy. Conflating them is the whole trap.
	if alpha.Damage.Taken == nil || *alpha.Damage.Taken != 5500 {
		t.Errorf("taken = %v, want the derived 5500 (KTX's 5000 is enemy-only)", alpha.Damage.Taken)
	}
	if alpha.Damage.TakenEnemy == nil || *alpha.Damage.TakenEnemy != 5000 {
		t.Errorf("takenEnemy = %v, want 5000", alpha.Damage.TakenEnemy)
	}
	if alpha.Damage.TakenToDie == nil || *alpha.Damage.TakenToDie != 250 {
		t.Errorf("takenToDie = %v, want 250", alpha.Damage.TakenToDie)
	}
	// Accuracy appears only now.
	if alpha.Accuracy == nil || alpha.Accuracy.ByWeapon["rl"].Attacks != 90 {
		t.Errorf("accuracy not lifted: %+v", alpha.Accuracy)
	}
	if h := alpha.Accuracy.ByWeapon["rl"].Hits; h == nil || *h != 40 {
		t.Errorf("accuracy hits = %v, want 40", h)
	}
	if r := alpha.Accuracy.ByWeapon["rl"].Real; r == nil || *r != 33 {
		t.Errorf("accuracy real = %v, want 33 — KTX's direct/splash split must survive", r)
	}
	// Pickups: KTX counters win, KTX item keys map to our vocabulary.
	if got := alpha.Pickups.ByKind["ra"].Took; got != 8 {
		t.Errorf("ra took = %d, want KTX's 8", got)
	}
	if got := alpha.Pickups.ByKind["mh"].Took; got != 4 {
		t.Errorf("mh took = %d, want 4 — KTX's health_100 must map to mh", got)
	}
	if got := alpha.Pickups.ByKind["quad"].Took; got != 2 {
		t.Errorf("quad took = %d, want 2 — KTX's q must map to quad", got)
	}
	if got := alpha.Pickups.ByKind["rl"]; got.Took != 7 || got.TotalTook != 9 || got.Dropped != 3 {
		t.Errorf("rl pickups = %+v, want KTX's 7/9/3", got)
	}
	// ...but the transfer decomposition stays derived: KTX conflates them.
	if x := alpha.Pickups.ByKind["rl"].Xfer; x == nil || *x != 2 {
		t.Errorf("xfer = %v, want the derived 2", x)
	}
	if x := alpha.Pickups.ByKind["rl"].XferSelf; x == nil || *x != 1 {
		t.Errorf("xferSelf = %v, want the derived 1", x)
	}
	// Ammo has no KTX counterpart and survives the overlay.
	if got := alpha.Pickups.ByKind["shells"].Took; got != 12 {
		t.Errorf("shells took = %d, want the derived 12 — KTX tracks no ammo", got)
	}
	// Hold is never overlaid.
	if alpha.Hold.Src != result.SrcDerived || alpha.Hold.Armor["ra"].Ms != 129000 {
		t.Errorf("hold was overlaid: %+v", alpha.Hold)
	}
	if got.Sources.Hold != result.SrcDerived {
		t.Errorf("hold src = %q, want derived", got.Sources.Hold)
	}
}

// The API serves a shared cached Result. An overlay that wrote through
// would corrupt the stored section for every subsequent request.
func TestPlayerStatsOverlayDoesNotMutateStored(t *testing.T) {
	r := storedResult(true)
	stored := r.PlayerStats
	_, _ = PlayerStats(r, PlayerStatsOptions{})

	if stored.Players[0].Damage.Given != 4000 {
		t.Errorf("stored damage.given = %d, want the untouched derived 4000", stored.Players[0].Damage.Given)
	}
	if stored.Players[0].Ping != 0 {
		t.Error("stored row gained a KTX ping — the overlay wrote through")
	}
	if stored.Players[0].Pickups.ByKind["ra"].Took != 5 {
		t.Error("stored pickups mutated by the overlay")
	}
	if stored.Sources.Damage != result.SrcDerived {
		t.Error("stored sources mutated by the overlay")
	}
	// A second call must produce the same answer as the first.
	again := mustPlayerStats(t, r, PlayerStatsOptions{})
	if again.Players[0].Damage.Given != 4321 {
		t.Errorf("second call = %d, want 4321 — the overlay is not idempotent", again.Players[0].Damage.Given)
	}
}

// A team row is the sum of its members, so it must be re-summed from the
// overlaid rows rather than left carrying stale derived totals.
func TestPlayerStatsTeamsReaggregatedAfterOverlay(t *testing.T) {
	got := mustPlayerStats(t, storedResult(true), PlayerStatsOptions{})
	var red *result.PlayerStatsRow
	for i := range got.Teams {
		if got.Teams[i].Name == "red" {
			red = &got.Teams[i]
		}
	}
	if red == nil {
		t.Fatal("no red team row")
	}
	if red.Damage.Given != 4321 {
		t.Errorf("team given = %d, want 4321 (re-summed from the overlaid member)", red.Damage.Given)
	}
	if red.Damage.Src != result.SrcKTX {
		t.Errorf("team damage src = %q, want ktx", red.Damage.Src)
	}
	// Window and hold are untouched by the overlay and must survive it.
	if red.Window.AliveMs != 500000 {
		t.Errorf("team aliveMs = %d, want the untouched 500000", red.Window.AliveMs)
	}
}

// A KTX block with a dmg blob but no derived damage row (a demo that
// carries the block but no damage stream) must not fabricate an
// all-sources `taken` of zero.
func TestPlayerStatsTakenAbsentWithoutDerivedRow(t *testing.T) {
	r := storedResult(true)
	r.PlayerStats.Players[0].Damage = nil
	got := mustPlayerStats(t, r, PlayerStatsOptions{})
	d := got.Players[0].Damage
	if d == nil || d.Src != result.SrcKTX {
		t.Fatalf("damage = %+v, want a KTX-sourced row", d)
	}
	if d.Taken != nil {
		t.Errorf("taken = %v, want absent — nothing measured it, and 0 would read as 'took no damage'", *d.Taken)
	}
	if d.TakenEnemy == nil {
		t.Error("takenEnemy should still come from KTX")
	}
}

// An empty filter match is [], never null: the key is required and
// array-typed, and a null breaks a caller that ranges over it.
func TestPlayerStatsEmptyFilterYieldsEmptySlice(t *testing.T) {
	got := mustPlayerStats(t, storedResult(true), PlayerStatsOptions{Players: []string{"nobody"}})
	if got.Players == nil {
		t.Fatal("players = null, want []")
	}
	if len(got.Players) != 0 {
		t.Errorf("players = %v, want empty", got.Players)
	}
}

// The overlay must not write through to the cached Result via the team
// rows or a shared transfer counter either.
func TestPlayerStatsOverlayLeavesTeamsAndCountersAlone(t *testing.T) {
	r := storedResult(true)
	stored := r.PlayerStats
	beforeXfer := *stored.Players[0].Pickups.ByKind["rl"].Xfer
	beforeTeamGiven := stored.Teams[0].Damage.Given
	_, _ = PlayerStats(r, PlayerStatsOptions{})
	if got := *stored.Players[0].Pickups.ByKind["rl"].Xfer; got != beforeXfer {
		t.Errorf("stored xfer counter mutated: %d -> %d", beforeXfer, got)
	}
	if got := stored.Teams[0].Damage.Given; got != beforeTeamGiven {
		t.Errorf("stored team row mutated: %d -> %d", beforeTeamGiven, got)
	}
}

func TestPlayerStatsFilters(t *testing.T) {
	got := mustPlayerStats(t, storedResult(true), PlayerStatsOptions{Players: []string{"alpha"}})
	if len(got.Players) != 1 || got.Players[0].Name != "alpha" {
		t.Errorf("players filter = %v", got.Players)
	}
	// Team rows are whole-team sums; alongside a subset of members they
	// would read as the subset's totals.
	if len(got.Teams) != 0 {
		t.Errorf("teams = %v, want none alongside a players filter", got.Teams)
	}

	got = mustPlayerStats(t, storedResult(true), PlayerStatsOptions{Teams: []string{"red"}})
	if len(got.Players) != 1 || got.Players[0].Team != "red" {
		t.Errorf("teams filter did not narrow players: %v", got.Players)
	}
	if len(got.Teams) != 1 || got.Teams[0].Name != "red" {
		t.Errorf("teams filter did not narrow team rows: %v", got.Teams)
	}
}

func TestPlayerStatsNilSection(t *testing.T) {
	// A parse degraded enough to produce no streams has no section — that
	// is ErrUnavailable (a 422), and is NOT the same condition as a demo
	// without a KTX demoinfo block, which is served normally.
	if _, err := PlayerStats(&result.Result{}, PlayerStatsOptions{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable for a Result with no section", err)
	}
	if _, err := PlayerStats(nil, PlayerStatsOptions{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("err = %v, want ErrUnavailable for a nil Result", err)
	}
}

// A KTX block that names players this pipeline never resolved must not
// invent rows or drop the ones we have.
func TestPlayerStatsUnmatchedKTXPlayerIgnored(t *testing.T) {
	r := storedResult(true)
	r.DemoInfo.Players[0].Name = "someone else"
	got := mustPlayerStats(t, r, PlayerStatsOptions{})
	if len(got.Players) != 2 {
		t.Fatalf("players = %d, want 2", len(got.Players))
	}
	if got.Players[0].Damage.Src != result.SrcDerived {
		t.Error("unmatched KTX row was applied to the wrong player")
	}
	if got.Sources.Damage != result.SrcDerived {
		t.Errorf("damage src = %q, want derived — nothing was overlaid", got.Sources.Damage)
	}
}

// KTX writes 99999 for taken-to-die rather than dividing by zero
// (ktx/src/stats_json.c:357). It is a sentinel, not a measurement, and
// must never reach a consumer as a number — the overlay falls through to
// the derived average, which itself omits when the player never died.
func TestPlayerStatsNoDeathsSentinelNeverServed(t *testing.T) {
	t.Run("falls through to the derived average", func(t *testing.T) {
		r := storedResult(true)
		r.PlayerStats.Players[0].Damage.TakenToDie = intp(275)
		r.DemoInfo.Players[0].Dmg.TakenToDie = 99999

		got := mustPlayerStats(t, r, PlayerStatsOptions{})
		ttd := got.Players[0].Damage.TakenToDie
		if ttd == nil || *ttd != 275 {
			t.Fatalf("takenToDie = %v, want the derived 275", ttd)
		}
	})

	t.Run("omitted when nothing derived it either", func(t *testing.T) {
		r := storedResult(true)
		r.PlayerStats.Players[0].Damage.TakenToDie = nil
		r.DemoInfo.Players[0].Dmg.TakenToDie = 99999

		got := mustPlayerStats(t, r, PlayerStatsOptions{})
		if ttd := got.Players[0].Damage.TakenToDie; ttd != nil {
			t.Fatalf("takenToDie = %d, want ABSENT — 99999 is a sentinel, not a reading", *ttd)
		}
	})
}

// KTX writes control and speed unconditionally (ktx/src/stats_json.c:362,
// unlike the conditional handicap below them), so a player who never held
// control has a measured 0. Suppressing it as "absent" would hide exactly
// the player the stat says the most about.
func TestPlayerStatsControlAndSpeedZeroIsAMeasurement(t *testing.T) {
	r := storedResult(true)
	zero := 0.0
	r.DemoInfo.Players[0].Control = &zero
	r.DemoInfo.Players[0].Speed = &result.DemoInfoSpeed{Max: 0, Avg: 0}

	got := mustPlayerStats(t, r, PlayerStatsOptions{})
	row := got.Players[0]
	if row.ControlMs == nil || *row.ControlMs != 0 {
		t.Errorf("controlMs = %v, want an observed 0", row.ControlMs)
	}
	if row.Speed == nil {
		t.Error("speed absent though KTX recorded a 0/0 pair")
	}

	// A build that never wrote the key at all is genuinely unmeasured.
	r2 := storedResult(true)
	r2.DemoInfo.Players[0].Control = nil
	if ms := mustPlayerStats(t, r2, PlayerStatsOptions{}).Players[0].ControlMs; ms != nil {
		t.Errorf("controlMs = %d, want absent when KTX wrote no control key", *ms)
	}
}

// KTX's login wins, but a KTX block that carries none must not erase the
// *auth login the analyzer already read off the wire.
func TestPlayerStatsBlankKTXLoginKeepsWireAuth(t *testing.T) {
	r := storedResult(true)
	r.PlayerStats.Players[0].Login = "wire@auth"
	r.DemoInfo.Players[0].Login = ""

	if got := mustPlayerStats(t, r, PlayerStatsOptions{}).Players[0].Login; got != "wire@auth" {
		t.Errorf("login = %q, want the wire *auth login preserved", got)
	}
}

// Real/virtual are KTX's rl/gl-only rhits/vhits. KTX omits the pair
// entirely unless it recorded one (stats_json.c:146), so a weapon without
// them must carry nil rather than a fabricated 0/0 split.
func TestPlayerStatsOverlayRealVirtualNotZeroFilled(t *testing.T) {
	r := storedResult(true)
	r.DemoInfo.Players[0].Weapons["lg"] = &result.DemoInfoWeapon{
		Acc: &result.DemoInfoAcc{Attacks: 169, Hits: 53},
	}

	acc := mustPlayerStats(t, r, PlayerStatsOptions{}).Players[0].Accuracy
	lg := acc.ByWeapon["lg"]
	if lg.Hits == nil || *lg.Hits != 53 {
		t.Fatalf("lg hits = %v, want 53", lg.Hits)
	}
	if lg.Real != nil || lg.Virtual != nil {
		t.Error("lg carries a real/virtual split KTX never recorded")
	}
	if rl := acc.ByWeapon["rl"]; rl.Real == nil || *rl.Real != 33 {
		t.Errorf("rl real = %v, want 33 carried through", rl.Real)
	}
}
