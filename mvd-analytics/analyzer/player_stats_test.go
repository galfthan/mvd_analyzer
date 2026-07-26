package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func iv(pairs ...int32) []result.Interval {
	var out []result.Interval
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, result.Interval{Start: pairs[i], End: pairs[i+1]})
	}
	return out
}

func wantIntervals(t *testing.T, got, want []result.Interval, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: got %v, want %v", label, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s: got %v, want %v", label, got, want)
		}
	}
}

// --- interval algebra ---------------------------------------------------

func TestMergeIntervals(t *testing.T) {
	wantIntervals(t, mergeIntervals(iv(10, 20, 15, 30, 40, 50)), iv(10, 30, 40, 50), "overlapping")
	// Touching intervals fuse: [10,20) and [20,30) are one possession run,
	// not two — this is what keeps Runs honest when a stream records a
	// re-grant on the same millisecond a run would otherwise end.
	wantIntervals(t, mergeIntervals(iv(20, 30, 10, 20)), iv(10, 30), "touching")
	wantIntervals(t, mergeIntervals(nil), nil, "empty")
}

func TestClipIntervals(t *testing.T) {
	wantIntervals(t, clipIntervals(iv(0, 100), iv(20, 40, 60, 80)), iv(20, 40, 60, 80), "window splits")
	wantIntervals(t, clipIntervals(iv(0, 30), iv(50, 100)), nil, "disjoint")
	wantIntervals(t, clipIntervals(nil, iv(0, 10)), nil, "no source")
	wantIntervals(t, clipIntervals(iv(0, 10), nil), nil, "no window")
}

func TestComplementIntervals(t *testing.T) {
	wantIntervals(t, complementIntervals(iv(20, 40), iv(0, 100)), iv(0, 20, 40, 100), "middle hole")
	wantIntervals(t, complementIntervals(nil, iv(0, 50)), iv(0, 50), "nothing covered")
	wantIntervals(t, complementIntervals(iv(0, 50), iv(0, 50)), nil, "fully covered")
}

// --- alive intervals ----------------------------------------------------

// TestAliveIntervalsMatchesLosAliveAt is the cross-check that keeps this
// file's interval form and analyzer.losAliveAt's point form from drifting:
// the repo has one liveness rule and two encodings of it.
func TestAliveIntervalsMatchesLosAliveAt(t *testing.T) {
	cases := []struct {
		name           string
		spawns, deaths []int32
	}{
		{"no events", nil, nil},
		{"alive from start, dies once", nil, []int32{5000}},
		{"death then respawn", []int32{6000}, []int32{5000}},
		{"several lives", []int32{6000, 12000}, []int32{5000, 11000, 18000}},
		{"spawn before any death (KTX first-respawn quirk)", []int32{7000}, []int32{5000}},
	}
	const matchMs = 20000
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ivs := aliveIntervals(tc.spawns, tc.deaths, matchMs)
			for tms := int32(0); tms < matchMs; tms += 100 {
				want := losAliveAt(tc.spawns, tc.deaths, tms)
				got := false
				for _, v := range ivs {
					if tms >= v.Start && tms < v.End {
						got = true
						break
					}
				}
				if got != want {
					t.Fatalf("t=%d: intervals say alive=%v, losAliveAt says %v (intervals %v)", tms, got, want, ivs)
				}
			}
		})
	}
}

// A death and the respawn it triggers can land on the same millisecond.
// The player is alive after that pair — ordering deaths before spawns at
// equal timestamps is what makes that come out right instead of leaving
// the player dead for the rest of the match.
func TestAliveIntervalsSimultaneousDeathAndSpawn(t *testing.T) {
	got := aliveIntervals([]int32{5000}, []int32{5000}, 10000)
	wantIntervals(t, got, iv(0, 5000, 5000, 10000), "death+spawn same ms")
}

func TestAliveIntervalsIgnoresOutOfWindowEvents(t *testing.T) {
	// Warmup deaths carry negative match-relative times; post-match events
	// run past matchEnd. Neither may open or close an in-match life.
	got := aliveIntervals([]int32{-3000, 12000}, []int32{-5000, 11000}, 10000)
	wantIntervals(t, got, iv(0, 10000), "out-of-window events ignored")
}

// --- hold integrals -----------------------------------------------------

func newHoldPlayer() *result.PlayerStream {
	return &result.PlayerStream{
		Name:   "p",
		Team:   "red",
		Spawns: []int32{},
		Deaths: []int32{},
	}
}

func TestHoldClipsToAliveTime(t *testing.T) {
	p := newHoldPlayer()
	p.Deaths = []int32{5000}
	p.Spawns = []int32{6000}
	// The RL bit survives 250 ms past the death — the usual stat-update
	// lag. Only the pre-death part is real possession.
	p.RL = iv(1000, 5250)

	w := result.PlayerStatsWindow{MatchMs: 10000, PresentMs: 10000, AliveMs: 9000}
	h := deriveHold(p, aliveIntervals(p.Spawns, p.Deaths, 10000), w)

	rl, ok := h.Weapons["rl"]
	if !ok {
		t.Fatal("no rl hold")
	}
	if rl.Ms != 4000 {
		t.Errorf("rl hold = %d ms, want 4000 (1000→5000, clipped at death)", rl.Ms)
	}
	if rl.Runs != 1 || rl.LongestMs != 4000 {
		t.Errorf("rl runs=%d longest=%d, want 1/4000", rl.Runs, rl.LongestMs)
	}
	if got := float64(rl.ShareAlive); got < 0.444 || got > 0.445 {
		t.Errorf("shareAlive = %v, want 4000/9000", got)
	}
	if got := float64(rl.ShareMatch); got < 0.3999 || got > 0.4001 {
		t.Errorf("shareMatch = %v, want 0.4", got)
	}
}

func TestHoldOmitsUntouchedWeapons(t *testing.T) {
	p := newHoldPlayer()
	p.RL = iv(0, 1000)
	h := deriveHold(p, iv(0, 10000), result.PlayerStatsWindow{MatchMs: 10000, AliveMs: 10000})
	if _, ok := h.Weapons["lg"]; ok {
		t.Error("lg present though never held — an untouched weapon must be omitted, not zero-filled")
	}
	if len(h.Weapons) != 1 {
		t.Errorf("weapons = %v, want only rl", h.Weapons)
	}
}

func TestHoldRunsAndLongest(t *testing.T) {
	p := newHoldPlayer()
	p.LG = iv(0, 1000, 3000, 9000, 9500, 9600)
	h := deriveHold(p, iv(0, 10000), result.PlayerStatsWindow{MatchMs: 10000, AliveMs: 10000})
	lg := h.Weapons["lg"]
	if lg.Runs != 3 {
		t.Errorf("runs = %d, want 3", lg.Runs)
	}
	if lg.LongestMs != 6000 {
		t.Errorf("longest = %d, want 6000", lg.LongestMs)
	}
	if lg.Ms != 1000+6000+100 {
		t.Errorf("ms = %d, want 7100", lg.Ms)
	}
}

// The armor complement is the stat KTX cannot produce, and it is only
// trustworthy if it closes the books exactly: every alive millisecond is
// spent under exactly one of ga / ya / ra / none.
func TestArmorComplementIdentity(t *testing.T) {
	p := newHoldPlayer()
	p.ArmorType = []result.ChangeStr{
		{T: 0, V: ""},
		{T: 1000, V: "ga"},
		{T: 3000, V: ""},
		{T: 4000, V: "ya"},
		{T: 6000, V: "ra"},
		{T: 9000, V: ""},
	}
	w := result.PlayerStatsWindow{MatchMs: 10000, PresentMs: 10000, AliveMs: 10000}
	h := deriveHold(p, iv(0, 10000), w)

	var sum int32
	for _, kind := range []string{"ga", "ya", "ra", "none"} {
		sum += h.Armor[kind].Ms
	}
	if sum != w.AliveMs {
		t.Fatalf("ga+ya+ra+none = %d, want aliveMs = %d (armor: %+v)", sum, w.AliveMs, h.Armor)
	}
	if got := h.Armor["ga"].Ms; got != 2000 {
		t.Errorf("ga = %d, want 2000", got)
	}
	if got := h.Armor["ra"].Ms; got != 3000 {
		t.Errorf("ra = %d, want 3000", got)
	}
	if got := h.Armor["none"].Ms; got != 3000 {
		t.Errorf("none = %d, want 3000 (0-1000, 3000-4000, 9000-10000)", got)
	}
	if got := h.Armor["none"].Runs; got != 3 {
		t.Errorf("none runs = %d, want 3", got)
	}
}

// A player who held armor for the whole match still gets a "none" key —
// at zero. It is a real reading ("never without armor"), and omitting it
// would make a full-match holder indistinguishable from a demo where the
// armor stream was missing.
func TestArmorNoneAlwaysPresent(t *testing.T) {
	p := newHoldPlayer()
	p.ArmorType = []result.ChangeStr{{T: 0, V: "ra"}}
	h := deriveHold(p, iv(0, 10000), result.PlayerStatsWindow{MatchMs: 10000, AliveMs: 10000})
	none, ok := h.Armor["none"]
	if !ok {
		t.Fatal("none key missing")
	}
	if none.Ms != 0 {
		t.Errorf("none = %d ms, want 0", none.Ms)
	}
}

func TestArmorRunsClipToMatchEnd(t *testing.T) {
	// The last armor run has no closing transition; it ends at match end,
	// not at whatever the stream's last timestamp happened to be.
	runs := armorRuns([]result.ChangeStr{{T: 8000, V: "ra"}}, 10000)
	if len(runs) != 1 || runs[0].iv != (result.Interval{Start: 8000, End: 10000}) {
		t.Fatalf("runs = %+v, want one [8000,10000)", runs)
	}
}

// --- presence -----------------------------------------------------------

func TestPresenceWindowLateJoiner(t *testing.T) {
	p := newHoldPlayer()
	p.Position = &result.PositionTrack{T: []int32{300000, 300100, 590000}}
	got := presenceWindow(p, 600000)
	wantIntervals(t, got, iv(300000, 590000), "late joiner")
}

// With no position track and no spawn/death markers, the possession
// streams are the last presence signal available — and assuming the whole
// match instead is a fabricated MAXIMUM. On 4on4_l_vs_la[e1m2], Sectoid's
// entire recorded existence is 3.5 s of possession at the end of an
// 18-minute match; the old fallback served him as alive for all 18
// minutes at "no armor 100%", and inflated his team's denominators with
// it.
func TestPresenceWindowFallsBackToPossession(t *testing.T) {
	p := newHoldPlayer()
	p.RL = iv(560000, 562000)
	p.ArmorType = []result.ChangeStr{{T: 555000, V: "ra"}}
	got := presenceWindow(p, 600000)
	wantIntervals(t, got, iv(555000, 562000), "possession-only presence")
}

// No signal of ANY kind means we never saw this player play. An empty
// window (presentMs 0) is the scoreboard-only shape and says exactly
// that; a full-match window would claim the opposite.
func TestPresenceWindowEmptyWithoutAnySignal(t *testing.T) {
	if got := presenceWindow(newHoldPlayer(), 600000); len(got) != 0 {
		t.Errorf("no signal: got %v, want an empty window", got)
	}
}

// A late joiner must not be credited alive time from match start: the
// liveness rule says "no death yet ⇒ alive since match start", which is
// right for a player who was there and wrong for one who had not
// connected. Intersecting with presence is what separates the two.
func TestLateJoinerAliveTimeStartsAtJoin(t *testing.T) {
	p := newHoldPlayer()
	p.Position = &result.PositionTrack{T: []int32{300000, 600000}}
	present := presenceWindow(p, 600000)
	alive := clipIntervals(aliveIntervals(p.Spawns, p.Deaths, 600000), present)
	if got := totalMs(alive); got != 300000 {
		t.Errorf("alive = %d ms, want 300000 (joined at half time)", got)
	}
}

// --- pickups and transfers ---------------------------------------------

func teamplayResult() *Result {
	return &Result{
		Metadata: &result.MetadataResult{MatchSettings: &result.MatchSettings{Teamplay: 2}},
		Match: &result.MatchResult{Players: []result.PlayerStat{
			{Name: "dropper", Team: "red"}, {Name: "mate", Team: "red"}, {Name: "foe", Team: "blue"},
		}},
		Backpacks: []result.BackpackDrop{{Player: "dropper", Team: "red", Weapon: "rl", EntNum: 7}},
	}
}

func TestXferCreditsDropperOnTeammatePickup(t *testing.T) {
	res := teamplayResult()
	res.WeaponPickups = []result.WeaponPickup{{
		Player: "mate", Team: "red", Weapon: "rl", Source: "backpack",
		Dropper: "dropper", DropperTeam: "red", BackpackEnt: 7,
	}}
	got := derivePickups(res, true)
	rl := got["dropper"]["rl"]
	if rl.Xfer == nil || *rl.Xfer != 1 {
		t.Errorf("dropper xfer = %v, want 1", rl.Xfer)
	}
	if rl.XferSelf == nil || *rl.XferSelf != 0 {
		t.Errorf("dropper xferSelf = %v, want 0", rl.XferSelf)
	}
	if _, ok := got["mate"]["rl"]; !ok {
		t.Error("picker should still get a took tally")
	}
	if x := got["mate"]["rl"].Xfer; x != nil {
		t.Errorf("picker credited with xfer %v — the credit goes to the DROPPER", *x)
	}
}

// KTX has no `other != dropper` check, so re-taking your own pack counts
// in its xferRL. We split it out; the sum must still reproduce KTX.
func TestXferSelfRecoverySplitOut(t *testing.T) {
	res := teamplayResult()
	res.WeaponPickups = []result.WeaponPickup{{
		Player: "dropper", Team: "red", Weapon: "rl", Source: "backpack",
		Dropper: "dropper", DropperTeam: "red", BackpackEnt: 7,
	}}
	rl := derivePickups(res, true)["dropper"]["rl"]
	if rl.XferSelf == nil || *rl.XferSelf != 1 {
		t.Errorf("xferSelf = %v, want 1", rl.XferSelf)
	}
	if rl.Xfer == nil || *rl.Xfer != 0 {
		t.Errorf("xfer = %v, want 0", rl.Xfer)
	}
}

func TestXferNotCreditedOnEnemyPickup(t *testing.T) {
	res := teamplayResult()
	res.WeaponPickups = []result.WeaponPickup{{
		Player: "foe", Team: "blue", Weapon: "rl", Source: "backpack",
		Dropper: "dropper", DropperTeam: "red", BackpackEnt: 7,
	}}
	rl := derivePickups(res, true)["dropper"]["rl"]
	// The pack was dropped, so the counters exist and read zero — an
	// observed zero, not an unobservable one.
	if rl.Xfer == nil || *rl.Xfer != 0 || rl.XferSelf == nil || *rl.XferSelf != 0 {
		t.Errorf("enemy pickup credited: xfer=%v xferSelf=%v — that is a denial, not a transfer", rl.Xfer, rl.XferSelf)
	}
}

// Absent (nil) and zero mean different things: no backpack hints at all
// means transfers are unobservable on this demo, which must not read as
// "nobody transferred anything".
func TestXferAbsentWithoutHints(t *testing.T) {
	res := teamplayResult()
	res.Backpacks = nil
	res.WeaponPickups = []result.WeaponPickup{{Player: "mate", Team: "red", Weapon: "rl", Source: "world"}}
	rl := derivePickups(res, false)["mate"]["rl"]
	if rl.Xfer != nil || rl.XferSelf != nil {
		t.Errorf("xfer=%v xferSelf=%v, want both absent when the demo carries no drop hints", rl.Xfer, rl.XferSelf)
	}
}

func TestWeaponTookVsTotalTook(t *testing.T) {
	res := teamplayResult()
	res.WeaponPickups = []result.WeaponPickup{
		{Player: "mate", Team: "red", Weapon: "rl", Source: "world", HadBefore: false},
		{Player: "mate", Team: "red", Weapon: "rl", Source: "world", HadBefore: true},
	}
	rl := derivePickups(res, true)["mate"]["rl"]
	if rl.Took != 1 {
		t.Errorf("took = %d, want 1 (only the grant)", rl.Took)
	}
	if rl.TotalTook != 2 {
		t.Errorf("totalTook = %d, want 2 (every touch)", rl.TotalTook)
	}
}

// Weapon tallies come from WeaponPickups, not the item timeline: a weapon
// can arrive in a backpack, which the item timeline never sees and KTX's
// wpn.tooks does count. Counting both would double-count world pickups.
func TestWeaponsNotDoubleCountedFromItems(t *testing.T) {
	res := teamplayResult()
	res.Items = &result.ItemsResult{Items: []result.ItemTimeline{
		{Name: "rl_1", Kind: "rl", Phases: []result.ItemPhase{{TakenBy: "mate"}}},
		{Name: "ra_1", Kind: "ra", Phases: []result.ItemPhase{{TakenBy: "mate"}}},
	}}
	res.WeaponPickups = []result.WeaponPickup{{Player: "mate", Team: "red", Weapon: "rl", Source: "world"}}
	got := derivePickups(res, true)["mate"]
	if got["rl"].Took != 1 {
		t.Errorf("rl took = %d, want 1 (WeaponPickups only)", got["rl"].Took)
	}
	if got["ra"].Took != 1 {
		t.Errorf("ra took = %d, want 1 (item timeline)", got["ra"].Took)
	}
}

// --- teamplay gate ------------------------------------------------------

func TestIsTeamplayGate(t *testing.T) {
	duelRoster := &Roster{isDuel: true}
	if isTeamplay(&Result{}, &CoreOutputs{Roster: duelRoster}) {
		t.Error("duel must not count as teamplay — KTX gates transfers on isTeam()")
	}
	tp := &Result{Metadata: &result.MetadataResult{MatchSettings: &result.MatchSettings{Teamplay: 2}}}
	if !isTeamplay(tp, &CoreOutputs{}) {
		t.Error("teamplay 2 should count")
	}
	ffa := &Result{
		Metadata: &result.MetadataResult{ServerInfo: map[string]string{"teamplay": "0"}},
		Match:    &result.MatchResult{Players: []result.PlayerStat{{Name: "a", Team: "1"}, {Name: "b", Team: "2"}}},
	}
	if isTeamplay(ffa, &CoreOutputs{}) {
		t.Error("teamplay 0 must not count even when players carry colour teams")
	}
	// No cvar at all: fall back to the roster shape.
	noCvar := &Result{Match: &result.MatchResult{Players: []result.PlayerStat{
		{Name: "a", Team: "red"}, {Name: "b", Team: "red"}, {Name: "c", Team: "blue"},
	}}}
	if !isTeamplay(noCvar, &CoreOutputs{}) {
		t.Error("two players sharing a team should count as teamplay when no cvar is present")
	}
}

// KTX gates its transfer counters on the MODE (isTeam(), k_mode ==
// gtTeam), not on the teamplay cvar, and the two disagree: the
// ffa_5[dm4] corpus demo runs FFA with `teamplay 2` set. Trusting the
// cvar there made DropperTeam == Team trivially true (most players carry
// no team at all) and invented a transfer for every backpack picked up.
func TestIsTeamplayGate_FFAModeBeatsTeamplayCvar(t *testing.T) {
	ffa := &Result{Metadata: &result.MetadataResult{
		MatchSettings: &result.MatchSettings{Mode: "FFA", Teamplay: 2},
		ServerInfo:    map[string]string{"teamplay": "2"},
	}}
	if isTeamplay(ffa, &CoreOutputs{}) {
		t.Error("FFA must not count as teamplay even with teamplay 2 — KTX's isTeam() is mode-gated")
	}
	// CTF is deliberately NOT in the non-team list: its teams are real and
	// the transfers happened, even though KTX's isTeam() declines to count
	// them. Unrecognised modes fall through to the cvar.
	ctf := &Result{Metadata: &result.MetadataResult{
		MatchSettings: &result.MatchSettings{Mode: "CTF", Teamplay: 2},
	}}
	if !isTeamplay(ctf, &CoreOutputs{}) {
		t.Error("CTF should still count as teamplay")
	}
}

// --- team aggregation ---------------------------------------------------

func TestTeamAggregationUsesTeamTimeDenominators(t *testing.T) {
	const matchMs = int32(600000)
	players := []result.PlayerStatsRow{
		{
			Name: "a", Team: "red",
			Window: result.PlayerStatsWindow{MatchMs: matchMs, PresentMs: matchMs, AliveMs: 400000, DeadMs: 200000},
			Score:  result.PlayerStatsScore{Kills: 10, Deaths: 5},
			Hold: result.PlayerStatsHold{Weapons: map[string]result.HoldStat{
				"rl": {Ms: 200000, Runs: 3, LongestMs: 90000},
			}},
		},
		{
			Name: "b", Team: "red",
			Window: result.PlayerStatsWindow{MatchMs: matchMs, PresentMs: matchMs, AliveMs: 200000, DeadMs: 400000},
			Score:  result.PlayerStatsScore{Kills: 2, Deaths: 8},
			Hold: result.PlayerStatsHold{Weapons: map[string]result.HoldStat{
				"rl": {Ms: 100000, Runs: 1, LongestMs: 100000},
			}},
		},
	}
	teams := aggregateTeamRows(players, matchMs)
	if len(teams) != 1 {
		t.Fatalf("teams = %d, want 1", len(teams))
	}
	red := teams[0]
	if red.Window.AliveMs != 600000 {
		t.Errorf("team aliveMs = %d, want 600000", red.Window.AliveMs)
	}
	rl := red.Hold.Weapons["rl"]
	if rl.Ms != 300000 {
		t.Errorf("team rl ms = %d, want 300000", rl.Ms)
	}
	if rl.Runs != 4 {
		t.Errorf("team rl runs = %d, want 4 (summed)", rl.Runs)
	}
	if rl.LongestMs != 100000 {
		t.Errorf("team rl longest = %d, want 100000 (max over members, not summed)", rl.LongestMs)
	}
	// 300000 / 600000 summed alive — NOT the mean of 0.5 and 0.5.
	if got := float64(rl.ShareAlive); got != 0.5 {
		t.Errorf("team shareAlive = %v, want 0.5 over summed alive time", got)
	}
	// Match denominator is match window x member count: available team time.
	if got := float64(rl.ShareMatch); got != 0.25 {
		t.Errorf("team shareMatch = %v, want 0.25 over 2x match window", got)
	}
	if got := float64(red.Score.Efficiency); got < 0.4799 || got > 0.4801 {
		t.Errorf("team efficiency = %v, want 12/25", got)
	}
}

func TestTeamAggregationPreservesXferObservability(t *testing.T) {
	one := 1
	players := []result.PlayerStatsRow{
		{Name: "a", Team: "red", Pickups: &result.PlayerStatsPickups{ByKind: map[string]result.PlayerStatsPickup{
			"rl": {Dropped: 2, Xfer: &one},
		}}},
		{Name: "b", Team: "red", Pickups: &result.PlayerStatsPickups{ByKind: map[string]result.PlayerStatsPickup{
			"rl": {Dropped: 1},
		}}},
	}
	teams := aggregateTeamRows(players, 600000)
	rl := teams[0].Pickups.ByKind["rl"]
	if rl.Xfer == nil || *rl.Xfer != 1 {
		t.Errorf("team xfer = %v, want 1 — observable for any member means observable for the team", rl.Xfer)
	}
	if *players[0].Pickups.ByKind["rl"].Xfer != 1 {
		t.Error("aggregation mutated a member's counter through the shared pointer")
	}
}

// --- takenEnemy reconstruction ------------------------------------------

func ptrInt(v int) *int { return &v }

// deriveTakenEnemy reconstructs KTX's dmg_t, which accumulates ONLY in
// the enemy branch (ktx/src/combat.c:1069). Every other source our own
// `taken` counts — team, self, environment — must be excluded, or the two
// fields silently become the same number under different names.
func TestDeriveTakenEnemyExcludesNonEnemySources(t *testing.T) {
	res := &Result{Damage: &result.DamageResult{
		Events: []result.DamageEntry{
			{Attacker: "b", Victim: "a", Damage: 40},
			{Attacker: "b", Victim: "a", Damage: 60, Bounded: ptrInt(25)}, // overkill capped
			{Attacker: "mate", Victim: "a", Damage: 30, IsTeam: true},
			{Attacker: "a", Victim: "a", Damage: 20, IsSelf: true},
			{Attacker: "world", Victim: "a", Damage: 15, IsEnv: true},
		},
		ByPlayer: map[string]*result.PlayerDamage{"a": {}, "b": {}, "quiet": {}},
	}}

	got := deriveTakenEnemy(res)
	if got["a"] != 65 {
		t.Errorf("takenEnemy[a] = %d, want 65 (40 + bounded 25; team/self/env excluded)", got["a"])
	}
	// Every player in the damage table reads as an observed zero rather
	// than as unknown — the stream measured them, they just took nothing.
	if v, ok := got["quiet"]; !ok || v != 0 {
		t.Errorf("takenEnemy[quiet] = %v (present=%v), want an observed 0", v, ok)
	}
}

// Telefrags and stomps live outside Events but do fold into KTX's dmg_t,
// so they are added back — except the ones KTX's enemy branch never sees.
// PositionalKill carries no IsSelf/IsEnv flag, so the test is on the names.
func TestDeriveTakenEnemyFoldsPositionalKillsButNotSelfOrWorld(t *testing.T) {
	res := &Result{Damage: &result.DamageResult{
		ByPlayer: map[string]*result.PlayerDamage{"a": {}},
		Telefrags: []result.PositionalKill{
			{Attacker: "b", Victim: "a", Bounded: ptrInt(100)},
			{Attacker: "mate", Victim: "a", IsTeam: true, Bounded: ptrInt(100)},
			{Attacker: "a", Victim: "a", Bounded: ptrInt(100)},     // self-telefrag
			{Attacker: "world", Victim: "a", Bounded: ptrInt(100)}, // degenerate world attacker
			{Attacker: "b", Victim: "a"},                           // no bounded reconstruction
		},
		Stomps: []result.PositionalKill{
			{Attacker: "b", Victim: "a", Bounded: ptrInt(30)},
		},
	}}

	if got := deriveTakenEnemy(res)["a"]; got != 130 {
		t.Errorf("takenEnemy[a] = %d, want 130 (enemy telefrag 100 + stomp 30 only)", got)
	}
}

func TestDeriveTakenEnemyNilWithoutDamageStream(t *testing.T) {
	if got := deriveTakenEnemy(&Result{}); got != nil {
		t.Errorf("deriveTakenEnemy without a damage stream = %v, want nil", got)
	}
}

// --- derived accuracy ---------------------------------------------------

// The flagship "absent, not zero" case: with no damage stream there is
// nothing to link fires against, so `hits` must be omitted rather than
// reading as "shot and never hit". `attacks` still stands on its own.
func TestDeriveAccuracyOmitsHitsWithoutDamageStream(t *testing.T) {
	shots := &result.ShotsResult{ByPlayer: []result.PlayerShots{{
		Player:   "a",
		ByWeapon: []result.WeaponShots{{Weapon: "rl", Shots: 63, Hits: 0}},
	}}}

	acc := deriveAccuracy(&Result{Shots: shots}, "a")
	if acc == nil {
		t.Fatal("derived accuracy is nil — the family must survive a missing damage stream")
	}
	if acc.Src != result.SrcDerived {
		t.Errorf("src = %q, want %q", acc.Src, result.SrcDerived)
	}
	rl := acc.ByWeapon["rl"]
	if rl.Attacks != 63 {
		t.Errorf("attacks = %d, want 63", rl.Attacks)
	}
	if rl.Hits != nil {
		t.Errorf("hits = %d, want ABSENT without a damage stream", *rl.Hits)
	}
	if rl.Real != nil || rl.Virtual != nil {
		t.Error("real/virtual are KTX-only and must never appear on a derived block")
	}

	// With a damage stream the link is meaningful, so hits appears.
	withDmg := deriveAccuracy(&Result{Shots: shots, Damage: &result.DamageResult{}}, "a")
	if h := withDmg.ByWeapon["rl"].Hits; h == nil || *h != 0 {
		t.Errorf("hits with a damage stream = %v, want an observed 0", h)
	}
}

func TestDeriveAccuracyNilForUnknownPlayer(t *testing.T) {
	shots := &result.ShotsResult{ByPlayer: []result.PlayerShots{{
		Player: "a", ByWeapon: []result.WeaponShots{{Weapon: "rl", Shots: 1}},
	}}}
	if got := deriveAccuracy(&Result{Shots: shots}, "nobody"); got != nil {
		t.Errorf("deriveAccuracy for an unknown player = %v, want nil", got)
	}
}

// --- login ---------------------------------------------------------------

// Two identities can share a display name with different *auth logins.
// Map iteration is randomised, so first-wins must be resolved in slot
// order or the attribution flips between runs of the same demo.
func TestDeriveLoginsIsDeterministicOnNameCollision(t *testing.T) {
	co := &CoreOutputs{Sessions: map[int][]ResolvedSession{
		5: {{Name: "player", Auth: "late"}},
		1: {{Name: "player", Auth: "early"}},
		3: {{Name: "other", Auth: ""}},
	}}
	for i := 0; i < 32; i++ {
		if got := deriveLogins(co)["player"]; got != "early" {
			t.Fatalf("login = %q, want %q (lowest slot wins, deterministically)", got, "early")
		}
	}
	if _, ok := deriveLogins(co)["other"]; ok {
		t.Error("a player with no *auth key must get no login entry at all")
	}
}
