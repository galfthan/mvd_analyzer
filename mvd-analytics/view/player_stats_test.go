package view

import (
	"errors"
	"reflect"
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
					Score:  result.PlayerStatsScore{Src: result.SrcDerived, Frags: 30, Kills: intp(32), Deaths: 20},
					Damage: &result.PlayerStatsDamage{Src: result.SrcDerived, Given: 4000, Taken: intp(5500), GivenTeam: 100, GivenSelf: 50, EnemyWeapons: 3000},
					Pickups: &result.PlayerStatsPickups{Src: result.SrcDerived, ByKind: map[string]result.PlayerStatsPickup{
						"ra":     {Took: 5},
						"rl":     {Took: 3, TotalTook: 4, Dropped: 2, Xfer: &xfer, XferSelf: &xferSelf},
						"shells": {Took: 12},
					}},
					Accuracy: &result.PlayerStatsAccuracy{Src: result.SrcDerived, ByWeapon: map[string]result.PlayerStatsAcc{
						// Trigger pulls, not pellets — the reason the KTX
						// block replaces this one wholesale rather than
						// merging key by key.
						"rl":  {Attacks: 61, Hits: intp(22)},
						"ssg": {Attacks: 40, Hits: intp(18)},
					}},
					Hold: result.PlayerStatsHold{Src: result.SrcDerived, Armor: map[string]result.HoldStat{
						"ra":   {Ms: 129000, Runs: 4},
						"none": {Ms: 200000, Runs: 9},
					}},
				},
				{
					Name: "beta", Team: "blue",
					Window: result.PlayerStatsWindow{MatchMs: 600000, PresentMs: 600000, AliveMs: 480000, DeadMs: 120000},
					Score:  result.PlayerStatsScore{Src: result.SrcDerived, Frags: 20, Kills: intp(21), Deaths: 30},
					Damage: &result.PlayerStatsDamage{Src: result.SrcDerived, Given: 3000, Taken: intp(6000)},
					Hold:   result.PlayerStatsHold{Src: result.SrcDerived},
				},
			},
			Teams: []result.PlayerStatsRow{
				{
					Name:   "red",
					Window: result.PlayerStatsWindow{MatchMs: 600000, PresentMs: 600000, AliveMs: 500000},
					Score:  result.PlayerStatsScore{Src: result.SrcDerived, Frags: 30, Kills: intp(32), Deaths: 20},
					Damage: &result.PlayerStatsDamage{Src: result.SrcDerived, Given: 4000, Taken: intp(5500)},
					Hold:   result.PlayerStatsHold{Src: result.SrcDerived},
				},
				{
					Name:   "blue",
					Window: result.PlayerStatsWindow{MatchMs: 600000, PresentMs: 600000, AliveMs: 480000},
					Score:  result.PlayerStatsScore{Src: result.SrcDerived, Frags: 20, Kills: intp(21), Deaths: 30},
					Damage: &result.PlayerStatsDamage{Src: result.SrcDerived, Given: 3000, Taken: intp(6000)},
					Hold:   result.PlayerStatsHold{Src: result.SrcDerived},
				},
			},
			Sources: result.PlayerStatsSources{Score: result.SrcDerived, Damage: result.SrcDerived, Accuracy: result.SrcDerived, Pickups: result.SrcDerived, Hold: result.SrcDerived},
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
	if got.Players[0].Ping != nil {
		t.Error("ping present without a KTX block")
	}
}

func TestPlayerStatsKTXOverlay(t *testing.T) {
	r := storedResult(true)
	got := mustPlayerStats(t, r, PlayerStatsOptions{})
	alpha := got.Players[0]

	// Identity passthrough.
	if alpha.Ping == nil || *alpha.Ping != 13 || alpha.Login != "alpha@qw" {
		t.Errorf("identity not lifted: ping=%v login=%q", alpha.Ping, alpha.Login)
	}
	// Score is NEVER overlaid — KTX's 999s must not appear.
	if alpha.Score.Frags != 30 || alpha.Score.Kills == nil || *alpha.Score.Kills != 32 || alpha.Score.Deaths != 20 {
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
	if stored.Players[0].Ping != nil {
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

// T1.3: a KTX weapon entry carrying `damage: {enemy: 0, team: N}` is a
// MEASURED zero — the weapon dealt team splash only. Treating it as
// absent left the reconstruction's number in its place and then stamped
// the whole family `src: "ktx"`, so the response asserted enemy damage
// KTX had explicitly recorded as zero.
//
// KTX emits a weapon entry whenever the weapon was used at all
// (ktx/src/stats_json.c:382) and the damage sub-block whenever either
// counter moved (:208), so presence is the right test.
func TestPlayerStatsOverlayKTXZeroEnemyDamageIsMeasured(t *testing.T) {
	r := storedResult(true)
	r.PlayerStats.Players[0].Damage.ByWeapon = map[string]int{"gl": 700, "rl": 2000, "unknown": 4}
	r.DemoInfo.Players[0].Weapons["gl"] = &result.DemoInfoWeapon{
		Damage: &result.DemoInfoDamage{Enemy: 0, Team: 700},
	}
	r.DemoInfo.Players[0].Weapons["rl"].Damage = &result.DemoInfoDamage{Enemy: 1800}

	bw := mustPlayerStats(t, r, PlayerStatsOptions{}).Players[0].Damage.ByWeapon
	if got, ok := bw["gl"]; !ok || got != 0 {
		t.Errorf("gl = %d (present=%v), want KTX's measured 0 — 700 was TEAM damage", got, ok)
	}
	if got := bw["rl"]; got != 1800 {
		t.Errorf("rl = %d, want KTX's 1800", got)
	}
	// Keys outside KTX's vocabulary are real measured damage and survive:
	// on 1on1_bananfalco_betowen_240426_dm2 the `unknown` residual is what
	// reconciles byWeapon with KTX's `given`.
	if got := bw["unknown"]; got != 4 {
		t.Errorf("unknown = %d, want the derived 4 kept — KTX has no such key", got)
	}
}

// T1.6: the unfiltered path must emit [] for an empty roster, not null.
// playerStatsPost sets the section unconditionally, so a demo whose
// stream roster came out empty reaches this path — and `players` is
// declared required and array-typed.
func TestPlayerStatsEmptyRosterYieldsEmptySlice(t *testing.T) {
	r := &result.Result{PlayerStats: &result.PlayerStatsResult{
		Sources: result.PlayerStatsSources{Score: result.SrcDerived, Hold: result.SrcDerived},
	}}
	got := mustPlayerStats(t, r, PlayerStatsOptions{})
	if got.Players == nil {
		t.Fatal("players = null on the unfiltered path, want []")
	}
	if len(got.Players) != 0 {
		t.Errorf("players = %v, want empty", got.Players)
	}
	// A roster with no rows carries no family, so the overlayable keys are
	// omitted rather than claiming a provenance for nothing.
	if got.Sources.Damage != "" || got.Sources.Accuracy != "" || got.Sources.Pickups != "" {
		t.Errorf("sources = %+v, want the overlayable families omitted", got.Sources)
	}
	if got.Sources.Score != result.SrcDerived || got.Sources.Hold != result.SrcDerived {
		t.Errorf("sources = %+v, want score/hold derived", got.Sources)
	}
}

// T2.3: the roll-up must describe the rows, not "any row matched KTX".
// beta has no KTX entry in the fixture, so the families are genuinely
// mixed and the canary must fire rather than badging everything ktx.
func TestPlayerStatsSourcesRollUpFromRows(t *testing.T) {
	got := mustPlayerStats(t, storedResult(true), PlayerStatsOptions{})
	if got.Sources.Damage != result.SrcMixed {
		t.Errorf("damage src = %q, want mixed — alpha joined the KTX block and beta did not", got.Sources.Damage)
	}
	// Only alpha carries pickups and accuracy at all, and after the overlay
	// both are KTX's. A row WITHOUT the family contributes no opinion — an
	// absent family is not a third source.
	if got.Sources.Pickups != result.SrcKTX {
		t.Errorf("pickups src = %q, want ktx", got.Sources.Pickups)
	}
	if got.Sources.Accuracy != result.SrcKTX {
		t.Errorf("accuracy src = %q, want ktx — beta carries no accuracy family to disagree with", got.Sources.Accuracy)
	}
}

// ...and it is computed AFTER filtering, so it describes the rows served
// rather than the ones a filter removed.
func TestPlayerStatsSourcesComputedAfterFiltering(t *testing.T) {
	r := storedResult(true)
	// Unfiltered, damage is "mixed" (see above). Narrowed to one row it
	// must describe that row, not the set the filter removed.
	got := mustPlayerStats(t, r, PlayerStatsOptions{Players: []string{"alpha"}})
	if got.Sources.Damage != result.SrcKTX {
		t.Errorf("damage src = %q, want ktx — the only row served is KTX-sourced", got.Sources.Damage)
	}
	got = mustPlayerStats(t, r, PlayerStatsOptions{Players: []string{"beta"}})
	if got.Sources.Damage != result.SrcDerived {
		t.Errorf("damage src = %q, want derived — beta's row never joined the KTX block", got.Sources.Damage)
	}
	if got.Sources.Pickups != "" {
		t.Errorf("pickups src = %q, want omitted — beta carries no pickups family", got.Sources.Pickups)
	}
}

// T2.2: the accuracy overlay is a WHOLESALE swap, pinned here because the
// alternative (a per-weapon merge) would put KTX pellet counts beside
// derived trigger pulls under one src. Measured lossless on the cached
// corpus — see overlayAccuracy.
func TestPlayerStatsAccuracySwappedWholesale(t *testing.T) {
	got := mustPlayerStats(t, storedResult(true), PlayerStatsOptions{})
	acc := got.Players[0].Accuracy
	if acc == nil || acc.Src != result.SrcKTX {
		t.Fatalf("accuracy = %+v, want the KTX block", acc)
	}
	if len(acc.ByWeapon) != 1 {
		t.Errorf("byWeapon = %v, want KTX's rl alone — the derived block is replaced, not merged", acc.ByWeapon)
	}
	if _, ok := acc.ByWeapon["ssg"]; ok {
		t.Error("derived ssg survived the swap: KTX counts pellets there and we count trigger pulls, " +
			"so the two must never share a map")
	}
	if acc.ByWeapon["rl"].Attacks != 90 {
		t.Errorf("rl attacks = %d, want KTX's 90 (the derived 61 must not win)", acc.ByWeapon["rl"].Attacks)
	}
	// The stored derived block is untouched.
	if a := storedAccuracy(t); a.ByWeapon["ssg"].Attacks != 40 {
		t.Error("the swap wrote through to the stored artifact")
	}
}

func storedAccuracy(t *testing.T) *result.PlayerStatsAccuracy {
	t.Helper()
	r := storedResult(true)
	_, _ = PlayerStats(r, PlayerStatsOptions{})
	return r.PlayerStats.Players[0].Accuracy
}

// T1.5, read-time half: accuracy is overlaid PER PLAYER, so the team row
// must be re-summed after the overlay or it keeps the analyzer's derived
// aggregate beside KTX-sourced members.
func TestPlayerStatsTeamAccuracyReaggregatedAfterOverlay(t *testing.T) {
	got := mustPlayerStats(t, storedResult(true), PlayerStatsOptions{})
	var red *result.PlayerStatsRow
	for i := range got.Teams {
		if got.Teams[i].Name == "red" {
			red = &got.Teams[i]
		}
	}
	if red == nil || red.Accuracy == nil {
		t.Fatal("red team carries no accuracy family though its member does")
	}
	if red.Accuracy.Src != result.SrcKTX {
		t.Errorf("team accuracy src = %q, want ktx", red.Accuracy.Src)
	}
	if got := red.Accuracy.ByWeapon["rl"].Attacks; got != 90 {
		t.Errorf("team rl attacks = %d, want KTX's 90 re-summed from the overlaid member", got)
	}
	if h := red.Accuracy.ByWeapon["rl"].Hits; h == nil || *h != 40 {
		t.Errorf("team rl hits = %v, want 40", h)
	}
	// The derived ssg entry was replaced on the member row, so it must not
	// reappear on the team row either.
	if _, ok := red.Accuracy.ByWeapon["ssg"]; ok {
		t.Error("team row carries a derived ssg entry the overlay had already replaced")
	}
}

// A KTX block carrying acc but no dmg/items must still trigger the team
// re-aggregation, or the team keeps a stale derived accuracy beside
// KTX-sourced member rows.
func TestPlayerStatsAccuracyOnlyKTXBlockReaggregatesTeams(t *testing.T) {
	r := storedResult(true)
	r.DemoInfo.Players[0].Dmg = nil
	r.DemoInfo.Players[0].Items = nil
	r.DemoInfo.Players[0].Weapons["rl"].Pickups = nil

	got := mustPlayerStats(t, r, PlayerStatsOptions{})
	for i := range got.Teams {
		if got.Teams[i].Name != "red" {
			continue
		}
		acc := got.Teams[i].Accuracy
		if acc == nil || acc.Src != result.SrcKTX || acc.ByWeapon["rl"].Attacks != 90 {
			t.Errorf("team accuracy = %+v, want KTX's re-summed block", acc)
		}
		return
	}
	t.Fatal("no red team row")
}

// A team whose members disagree on a family's source carries the "mixed"
// canary as that family's src — never a silent upgrade to "ktx". Only
// reachable when the phantom-roster invariant is already broken (a roster
// row the KTX block has never heard of), which is exactly when the row
// must not claim clean provenance. External-review finding: damage and
// pickups used to do "any KTX member upgrades the team" while accuracy
// already had the shared-or-mixed rule.
func TestPlayerStatsTeamRowMixedSrcOnPartialJoin(t *testing.T) {
	r := &result.Result{
		PlayerStats: &result.PlayerStatsResult{
			Players: []result.PlayerStatsRow{
				{
					Name: "alpha", Team: "red",
					Window:  result.PlayerStatsWindow{MatchMs: 600000, PresentMs: 600000, AliveMs: 500000},
					Score:   result.PlayerStatsScore{Src: result.SrcDerived, Frags: 30, Deaths: 10},
					Damage:  &result.PlayerStatsDamage{Src: result.SrcDerived, Given: 4000},
					Pickups: &result.PlayerStatsPickups{Src: result.SrcDerived, ByKind: map[string]result.PlayerStatsPickup{"ra": {Took: 3}}},
					Hold:    result.PlayerStatsHold{Src: result.SrcDerived},
				},
				{
					Name: "phantom", Team: "red",
					Window: result.PlayerStatsWindow{MatchMs: 600000, PresentMs: 600000, AliveMs: 500000},
					Score:  result.PlayerStatsScore{Src: result.SrcDerived, Frags: 5, Deaths: 20},
					Damage: &result.PlayerStatsDamage{Src: result.SrcDerived, Given: 1000},
					// Derived accuracy so the team aggregation sees two
					// members with OPINIONS that disagree after alpha's is
					// overlaid to ktx — a member without the family would
					// (correctly) contribute no opinion at all.
					Accuracy: &result.PlayerStatsAccuracy{Src: result.SrcDerived, ByWeapon: map[string]result.PlayerStatsAcc{"rl": {Attacks: 12}}},
					Pickups:  &result.PlayerStatsPickups{Src: result.SrcDerived, ByKind: map[string]result.PlayerStatsPickup{"ya": {Took: 1}}},
					Hold:     result.PlayerStatsHold{Src: result.SrcDerived},
				},
			},
			Teams: []result.PlayerStatsRow{{
				Name:   "red",
				Window: result.PlayerStatsWindow{MatchMs: 600000, PresentMs: 1200000, AliveMs: 1000000},
				Score:  result.PlayerStatsScore{Src: result.SrcDerived, Frags: 35, Deaths: 30},
				Hold:   result.PlayerStatsHold{Src: result.SrcDerived},
			}},
			Sources: result.PlayerStatsSources{Score: result.SrcDerived, Damage: result.SrcDerived, Pickups: result.SrcDerived, Hold: result.SrcDerived},
		},
		// The KTX block knows alpha and not phantom — the phantom-roster
		// condition the canary exists for.
		DemoInfo: &result.DemoInfoResult{Players: []result.DemoInfoPlayer{{
			Name: "alpha", Team: "red",
			Dmg: &result.DemoInfoDmg{Given: 4321, Taken: 5000},
			Weapons: map[string]*result.DemoInfoWeapon{"rl": {
				Acc:     &result.DemoInfoAcc{Attacks: 90, Hits: 40},
				Pickups: &result.DemoInfoPickups{Taken: 7, TotalTaken: 9},
			}},
			Items: map[string]*result.DemoInfoItem{"ra": {Took: 8}},
		}}},
	}
	got := mustPlayerStats(t, r, PlayerStatsOptions{})
	if len(got.Teams) != 1 {
		t.Fatalf("teams = %d, want 1", len(got.Teams))
	}
	team := got.Teams[0]
	if team.Damage == nil || team.Damage.Src != result.SrcMixed {
		t.Errorf("team damage src = %v, want mixed", team.Damage)
	}
	if team.Pickups == nil || team.Pickups.Src != result.SrcMixed {
		t.Errorf("team pickups src = %v, want mixed", team.Pickups)
	}
	if team.Accuracy == nil || team.Accuracy.Src != result.SrcMixed {
		t.Errorf("team accuracy src = %v, want mixed", team.Accuracy)
	}
	// The roll-up says mixed too — row and roll-up tell the same story.
	if got.Sources.Damage != result.SrcMixed {
		t.Errorf("sources.damage = %q, want mixed", got.Sources.Damage)
	}
}

// v63: the KTX overlay stamps byWeaponTeam from weapons[].damage.team on
// exactly the presence rule byWeapon uses — the two counters share one
// sub-block (ktx/src/stats_json.c:208-212) — while byWeaponSelf, which KTX
// has no counter for, rides through from the reconstruction untouched.
func TestPlayerStatsOverlayKTXTeamDamageByWeapon(t *testing.T) {
	r := storedResult(true)
	d := r.PlayerStats.Players[0].Damage
	d.ByWeapon = map[string]int{"rl": 2000, "gl": 700, "unknown": 4}
	d.ByWeaponTeam = map[string]int{"gl": 650, "unknown": 2}
	d.ByWeaponSelf = map[string]int{"rl": 50}
	w := r.DemoInfo.Players[0].Weapons
	// {enemy: N, team: 0} — a weapon that only ever hit enemies.
	w["rl"].Damage = &result.DemoInfoDamage{Enemy: 1800, Team: 0}
	// {enemy: 0, team: N} — a weapon used purely for team splash.
	w["gl"] = &result.DemoInfoWeapon{Damage: &result.DemoInfoDamage{Enemy: 0, Team: 700}}
	// A weapon entry with no damage sub-block: both counters were zero, so
	// it stamps nothing at all (not a zero).
	w["lg"] = &result.DemoInfoWeapon{Acc: &result.DemoInfoAcc{Attacks: 169, Hits: 53}}

	got := mustPlayerStats(t, r, PlayerStatsOptions{}).Players[0].Damage
	if got.Src != result.SrcKTX {
		t.Fatalf("src = %q, want ktx", got.Src)
	}
	wantEnemy := map[string]int{"rl": 1800, "gl": 0, "unknown": 4}
	if !reflect.DeepEqual(got.ByWeapon, wantEnemy) {
		t.Errorf("byWeapon = %v, want %v", got.ByWeapon, wantEnemy)
	}
	// The derived-only `unknown` key survives; rl's measured team 0 lands.
	wantTeam := map[string]int{"rl": 0, "gl": 700, "unknown": 2}
	if !reflect.DeepEqual(got.ByWeaponTeam, wantTeam) {
		t.Errorf("byWeaponTeam = %v, want %v", got.ByWeaponTeam, wantTeam)
	}
	if _, ok := got.ByWeaponTeam["lg"]; ok {
		t.Errorf("byWeaponTeam stamped a weapon entry with no damage sub-block: %v", got.ByWeaponTeam)
	}
	if !reflect.DeepEqual(got.ByWeaponSelf, map[string]int{"rl": 50}) {
		t.Errorf("byWeaponSelf = %v, want the derived {rl:50} — KTX has no self counter", got.ByWeaponSelf)
	}
	// The stored artifact every later read starts from is untouched.
	if !reflect.DeepEqual(d.ByWeaponTeam, map[string]int{"gl": 650, "unknown": 2}) {
		t.Errorf("overlay wrote through to the stored derived map: %v", d.ByWeaponTeam)
	}
}

// A demo carrying a KTX block but no damage stream has no derived row at
// all: byWeapon/byWeaponTeam come from KTX, and byWeaponSelf is ABSENT —
// the same condition `taken` tracks, and the signal the frontend reads.
func TestPlayerStatsOverlayKTXNoStreamHasNoSelfSplit(t *testing.T) {
	r := storedResult(true)
	r.PlayerStats.Players[0].Damage = nil
	r.DemoInfo.Players[0].Weapons["rl"].Damage = &result.DemoInfoDamage{Enemy: 1800, Team: 90}

	got := mustPlayerStats(t, r, PlayerStatsOptions{}).Players[0].Damage
	if got == nil || got.Src != result.SrcKTX {
		t.Fatalf("damage = %+v, want a KTX family", got)
	}
	if got.Taken != nil {
		t.Errorf("taken = %v, want absent — no damage stream to measure it", got.Taken)
	}
	if got.ByWeaponSelf != nil {
		t.Errorf("byWeaponSelf = %v, want absent — KTX has no per-weapon self counter", got.ByWeaponSelf)
	}
	if !reflect.DeepEqual(got.ByWeaponTeam, map[string]int{"rl": 90}) {
		t.Errorf("byWeaponTeam = %v, want KTX's {rl:90}", got.ByWeaponTeam)
	}
}

// The post-overlay team re-aggregation sums the new maps too — an
// analyzer-only aggregate would be stale on every KTX demo. Derived,
// KTX-overlaid and mixed-source team rows all fold identically.
func TestPlayerStatsTeamRowsSumVictimSplits(t *testing.T) {
	// Derived: no KTX block, so the stored team rows stand as the analyzer
	// summed them.
	derived := storedResult(false)
	derived.PlayerStats.Teams[0].Damage.ByWeaponTeam = map[string]int{"sg": 30}
	red := mustPlayerStats(t, derived, PlayerStatsOptions{}).Teams[0]
	if !reflect.DeepEqual(red.Damage.ByWeaponTeam, map[string]int{"sg": 30}) {
		t.Errorf("derived team byWeaponTeam = %v, want the stored {sg:30}", red.Damage.ByWeaponTeam)
	}

	// KTX-overlaid: red's only member is alpha, so the team row must equal
	// the overlaid member row rather than the stale stored sum.
	r := storedResult(true)
	r.PlayerStats.Players[0].Damage.ByWeaponTeam = map[string]int{"gl": 650}
	r.PlayerStats.Players[0].Damage.ByWeaponSelf = map[string]int{"rl": 50}
	r.DemoInfo.Players[0].Weapons["gl"] = &result.DemoInfoWeapon{
		Damage: &result.DemoInfoDamage{Enemy: 0, Team: 700},
	}
	ps := mustPlayerStats(t, r, PlayerStatsOptions{})
	red = ps.Teams[0]
	if red.Damage.Src != result.SrcKTX {
		t.Fatalf("team src = %q, want ktx", red.Damage.Src)
	}
	if !reflect.DeepEqual(red.Damage.ByWeaponTeam, map[string]int{"gl": 700}) {
		t.Errorf("KTX team byWeaponTeam = %v, want {gl:700}", red.Damage.ByWeaponTeam)
	}
	if !reflect.DeepEqual(red.Damage.ByWeaponSelf, map[string]int{"rl": 50}) {
		t.Errorf("KTX team byWeaponSelf = %v, want the derived {rl:50}", red.Damage.ByWeaponSelf)
	}
	// beta is on blue and has no KTX entry, so the blue row stays derived —
	// the mixed canary only fires when members of ONE team disagree.
	if blue := ps.Teams[1]; blue.Damage.Src != result.SrcDerived {
		t.Errorf("blue team src = %q, want derived", blue.Damage.Src)
	}

	// Mixed: two members of one team, only one of which KTX knows.
	mixed := storedResult(true)
	mixed.PlayerStats.Players[1].Team = "red"
	mixed.PlayerStats.Players[1].Damage.ByWeaponTeam = map[string]int{"sg": 15}
	mixed.PlayerStats.Players[0].Damage.ByWeaponTeam = map[string]int{"gl": 650}
	mixed.DemoInfo.Players[0].Weapons["gl"] = &result.DemoInfoWeapon{
		Damage: &result.DemoInfoDamage{Enemy: 0, Team: 700},
	}
	red = mustPlayerStats(t, mixed, PlayerStatsOptions{}).Teams[0]
	if red.Damage.Src != result.SrcMixed {
		t.Errorf("mixed team src = %q, want %q", red.Damage.Src, result.SrcMixed)
	}
	if !reflect.DeepEqual(red.Damage.ByWeaponTeam, map[string]int{"gl": 700, "sg": 15}) {
		t.Errorf("mixed team byWeaponTeam = %v, want {gl:700, sg:15}", red.Damage.ByWeaponTeam)
	}
}
