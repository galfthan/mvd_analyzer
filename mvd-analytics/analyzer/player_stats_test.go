package analyzer

import (
	"reflect"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func intp(v int) *int { return &v }

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

// This used to cross-check aliveIntervals against analyzer.losAliveAt, on the
// grounds that the repo had "one liveness rule and two encodings of it". That
// premise is gone: losAliveAt (and aimcore's copy) were removed in v64 because
// their strict `lastSpawn > lastDeath` latched on a same-millisecond
// death+respawn and reported the player dead for the rest of the life. There
// is now one encoding, so the point-form semantics are asserted directly
// rather than against a second implementation that could be wrong in the same
// way.
func TestAliveIntervalsPointSemantics(t *testing.T) {
	const matchMs = 20000
	cases := []struct {
		name           string
		spawns, deaths []int32
		alive, dead    []int32 // sample instants that must read alive / dead
	}{
		{
			name:  "no events — alive from match start",
			alive: []int32{0, 10000, 19999},
		},
		{
			name:   "alive from start, dies once, never respawns",
			deaths: []int32{5000},
			alive:  []int32{0, 4999},
			dead:   []int32{5000, 12000, 19999},
		},
		{
			name:   "death then respawn",
			spawns: []int32{6000}, deaths: []int32{5000},
			alive: []int32{0, 4999, 6000, 19999},
			dead:  []int32{5000, 5999},
		},
		{
			name:   "several lives",
			spawns: []int32{6000, 12000}, deaths: []int32{5000, 11000, 18000},
			alive: []int32{0, 6000, 10999, 12000, 17999},
			dead:  []int32{5000, 11000, 18000, 19999},
		},
		{
			// KTX emits a player's first spawn only on their first RESPAWN, so
			// liveness must NOT key off "most recent spawn" — that would mark
			// everyone dead until minutes into the match.
			name:   "no match-start spawn recorded",
			spawns: []int32{7000}, deaths: []int32{5000},
			alive: []int32{0, 100, 4999, 7000},
			dead:  []int32{5000, 6999},
		},
		{
			// The case that killed losAliveAt.
			name:   "death and respawn on the same millisecond",
			spawns: []int32{5000}, deaths: []int32{5000},
			alive: []int32{0, 4999, 5000, 5001, 19999},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ivs := aliveIntervals(tc.spawns, tc.deaths, matchMs)
			in := func(tms int32) bool {
				for _, v := range ivs {
					if tms >= v.Start && tms < v.End {
						return true
					}
				}
				return false
			}
			for _, tms := range tc.alive {
				if !in(tms) {
					t.Errorf("t=%d reads dead, want alive (intervals %v)", tms, ivs)
				}
			}
			for _, tms := range tc.dead {
				if in(tms) {
					t.Errorf("t=%d reads alive, want dead (intervals %v)", tms, ivs)
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

// ...but a player whose armor stream was never observed gets NO armor map
// at all. The complement of nothing over a real alive window is a
// fabricated maximum — on the POV recording dag_caps_e1m2 seven of eight
// rows asserted 100% no-armor while the same rows listed armor pickups.
//
// The two cases are cleanly distinguishable because appendChangeStr
// always appends the first sample: a genuinely armorless player has one,
// an unobserved one has none.
func TestArmorNoneAbsentWithoutStream(t *testing.T) {
	p := newHoldPlayer()
	h := deriveHold(p, iv(0, 10000), result.PlayerStatsWindow{MatchMs: 10000, AliveMs: 10000})
	if _, ok := h.Armor["none"]; ok {
		t.Errorf("none = %+v, want ABSENT — the armor stream carries no sample at all", h.Armor["none"])
	}
	if h.Armor != nil {
		t.Errorf("armor map = %+v, want nil", h.Armor)
	}

	// The genuinely-armorless case still reports a full-match "none".
	p.ArmorType = []result.ChangeStr{{T: 0, V: ""}}
	h = deriveHold(p, iv(0, 10000), result.PlayerStatsWindow{MatchMs: 10000, AliveMs: 10000})
	if none := h.Armor["none"]; none.Ms != 10000 {
		t.Errorf("none = %d ms, want the full 10000 — the stream says they had none", none.Ms)
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

// --- score --------------------------------------------------------------

// deriveScore has two branches and, until this test, neither was covered.
// The kill side is served only where the frag log measured something.
func TestDeriveScoreKillSide(t *testing.T) {
	base := func() *Result {
		return &Result{
			Match: &result.MatchResult{Players: []result.PlayerStat{
				{Name: "a", Team: "red", Frags: 62, Kills: 40, Deaths: 18, Suicides: 2},
			}},
			Frags: &result.FragResult{
				// The demo-global verdict as the match-final node publishes it;
				// deriveScore reads the stored field rather than re-deriving the
				// rule (killsMeasurable, tested at its own wiring below).
				KillsMeasured: true,
				Frags:         []result.FragEntry{{Killer: "a", Victim: "b", Weapon: "rl"}},
				ByPlayer: map[string]*result.PlayerFrags{
					"a": {Kills: 40, Deaths: 18, TeamKills: 3, ByWeapon: map[string]int{"rl": 30, "lg": 10, "gl": 0}},
				},
			},
		}
	}

	t.Run("measured", func(t *testing.T) {
		r := base()
		s := deriveScore(r, "a", deriveEnemyWeaponKills(r), deriveSprees(r))
		if s.Frags != 62 || s.Deaths != 18 {
			t.Errorf("frags/deaths = %d/%d, want 62/18", s.Frags, s.Deaths)
		}
		if s.Kills == nil || *s.Kills != 40 {
			t.Fatalf("kills = %v, want 40", s.Kills)
		}
		if s.Suicides == nil || *s.Suicides != 2 {
			t.Errorf("suicides = %v, want the scoreboard's 2", s.Suicides)
		}
		if s.TeamKills == nil || *s.TeamKills != 3 {
			t.Errorf("teamKills = %v, want 3", s.TeamKills)
		}
		if s.Efficiency == nil || float64(*s.Efficiency) < 0.689 || float64(*s.Efficiency) > 0.690 {
			t.Errorf("efficiency = %v, want 40/58", s.Efficiency)
		}
		// A weapon with a zero count is omitted, not zero-filled.
		if got := s.ByWeapon; got["rl"] != 30 || got["lg"] != 10 || len(got) != 2 {
			t.Errorf("byWeapon = %v, want rl/lg only", got)
		}
	})

	t.Run("unmeasured: empty frag log beside real deaths", func(t *testing.T) {
		r := base()
		r.Frags.Frags = nil
		r.Frags.KillsMeasured = killsMeasurable(r) // false: protocol deaths in ByPlayer
		s := deriveScore(r, "a", deriveEnemyWeaponKills(r), deriveSprees(r))
		// Both measured sides survive — this is the whole point of not
		// dropping the family wholesale.
		if s.Frags != 62 || s.Deaths != 18 {
			t.Errorf("frags/deaths = %d/%d, want the measured 62/18", s.Frags, s.Deaths)
		}
		if s.Kills != nil || s.Suicides != nil || s.TeamKills != nil ||
			s.Efficiency != nil || s.ByWeapon != nil {
			t.Errorf("kill side served over an empty frag log: %+v", s)
		}
	})

	t.Run("nobody died: honest zeros survive", func(t *testing.T) {
		r := base()
		r.Frags.Frags = nil
		r.Match.Players[0].Kills, r.Match.Players[0].Deaths = 0, 0
		r.Match.Players[0].Suicides = 0
		// The scoreboard's deaths are a copy of these (the match-final fold),
		// so a demo where nobody died has both at zero.
		r.Frags.ByPlayer["a"].Kills, r.Frags.ByPlayer["a"].Deaths = 0, 0
		r.Frags.KillsMeasured = killsMeasurable(r) // true: nothing to contradict
		s := deriveScore(r, "a", deriveEnemyWeaponKills(r), deriveSprees(r))
		if s.Kills == nil || *s.Kills != 0 {
			t.Errorf("kills = %v, want an honest 0 — an empty log contradicts nothing here", s.Kills)
		}
	})
}

// R3: a streamed player the match analyzer never resolved into a
// scoreboard row recovers kills and deaths from the frag log — and must
// recover suicides the same way instead of reporting a fabricated 0
// beside them. Same count scoreboardStatsPost uses (postprocess.go:36-41).
func TestDeriveScoreOffScoreboardRecoversSuicides(t *testing.T) {
	r := &Result{
		Match: &result.MatchResult{Players: []result.PlayerStat{{Name: "someone else"}}},
		Frags: &result.FragResult{
			KillsMeasured: true,
			Frags: []result.FragEntry{
				{Killer: "a", Victim: "a", Weapon: "rl", IsSuicide: true},
				{Killer: "a", Victim: "a", Weapon: "gl", IsSuicide: true},
				{Killer: "b", Victim: "b", Weapon: "rl", IsSuicide: true},
				{Killer: "a", Victim: "b", Weapon: "lg"},
			},
			ByPlayer: map[string]*result.PlayerFrags{
				"a": {Kills: 11, Deaths: 7, ByWeapon: map[string]int{"lg": 11}},
			},
		},
	}
	s := deriveScore(r, "a", deriveEnemyWeaponKills(r), deriveSprees(r))
	if s.Kills == nil || *s.Kills != 11 || s.Deaths != 7 {
		t.Fatalf("kills/deaths = %v/%d, want the frag log's 11/7", s.Kills, s.Deaths)
	}
	if s.Suicides == nil || *s.Suicides != 2 {
		t.Errorf("suicides = %v, want 2 counted off the log — b's must not leak in", s.Suicides)
	}
	// Frags (the net score) has no frag-log equivalent and stays 0.
	if s.Frags != 0 {
		t.Errorf("frags = %d, want 0 — there is no frag-log net score", s.Frags)
	}
}

// --- team aggregation ---------------------------------------------------

func TestTeamAggregationUsesTeamTimeDenominators(t *testing.T) {
	const matchMs = int32(600000)
	players := []result.PlayerStatsRow{
		{
			Name: "a", Team: "red",
			Window: result.PlayerStatsWindow{MatchMs: matchMs, PresentMs: matchMs, AliveMs: 400000, DeadMs: 200000},
			Score:  result.PlayerStatsScore{Kills: intp(10), Deaths: 5},
			Hold: result.PlayerStatsHold{Weapons: map[string]result.HoldStat{
				"rl": {Ms: 200000, Runs: 3, LongestMs: 90000},
			}},
		},
		{
			Name: "b", Team: "red",
			Window: result.PlayerStatsWindow{MatchMs: matchMs, PresentMs: matchMs, AliveMs: 200000, DeadMs: 400000},
			Score:  result.PlayerStatsScore{Kills: intp(2), Deaths: 8},
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
	if red.Score.Efficiency == nil {
		t.Fatal("team efficiency absent though both members carry kills")
	}
	if got := float64(*red.Score.Efficiency); got < 0.4799 || got > 0.4801 {
		t.Errorf("team efficiency = %v, want 12/25", got)
	}
	if red.Score.Kills == nil || *red.Score.Kills != 12 {
		t.Errorf("team kills = %v, want 12", red.Score.Kills)
	}
	if red.Members == nil || *red.Members != 2 {
		t.Errorf("team members = %v, want an always-present 2", red.Members)
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

// --- damage family ------------------------------------------------------

// damage.go creates a per-player entry only on an actual hit, so a player
// who neither dealt nor took a point of damage has none. On a demo that
// carries the damage stream that is an OBSERVED all-zero row: collapsing
// it into an absent family says "we could not tell", which is false, and
// inverts what deriveTakenEnemy deliberately does two functions away.
func TestDeriveDamageZeroRowIsObserved(t *testing.T) {
	r := &Result{Damage: &result.DamageResult{
		ByPlayer: map[string]*result.PlayerDamage{
			"a": {Given: 4000, Taken: 3000},
		},
	}}
	takenEnemy := deriveTakenEnemy(r)

	d := deriveDamage(r, "ghost", takenEnemy)
	if d == nil {
		t.Fatal("damage absent for a player the demo measured at zero")
	}
	if d.Given != 0 || d.GivenTeam != 0 || d.GivenSelf != 0 || d.EnemyWeapons != 0 {
		t.Errorf("zeroed family is not zero: %+v", d)
	}
	if d.Taken == nil || *d.Taken != 0 {
		t.Errorf("taken = %v, want an observed 0", d.Taken)
	}
	if d.ByWeapon != nil {
		t.Errorf("byWeapon = %v, want absent — weapons with no damage are omitted", d.ByWeapon)
	}

	// A demo with no damage information at all is still the absent case.
	if got := deriveDamage(&Result{}, "ghost", nil); got != nil {
		t.Errorf("damage = %+v, want absent with no damage stream at all", got)
	}
}

// T1.4: on a k_midair / k_instagib / k_dmgfrags demo damage.go skips the
// bounded reconstruction entirely (damage.go:308,542-546), so this family
// falls back to RAW wire damage including overkill. It used to report
// `src: "derived"` — the same value as the bounded case — leaving the
// degradation invisible unless the caller also fetched
// damage.boundedMode.
func TestDeriveDamageMarksUnboundedFallback(t *testing.T) {
	bounded := &result.PlayerDamage{Given: 13641, Taken: 9000, ByWeapon: map[string]int{"rl": 13000}}
	standard := &Result{Damage: &result.DamageResult{
		BoundedMode: "standard",
		ByPlayer: map[string]*result.PlayerDamage{
			"a": {Given: 19640, Taken: 14000, ByWeapon: map[string]int{"rl": 19000}, Bounded: bounded},
		},
	}}
	d := deriveDamage(standard, "a", nil)
	if d.Src != result.SrcDerived {
		t.Errorf("src = %q, want derived", d.Src)
	}
	if d.Given != 13641 {
		t.Errorf("given = %d, want the bounded 13641", d.Given)
	}

	skipped := &Result{Damage: &result.DamageResult{
		BoundedMode: "skipped:instagib",
		ByPlayer: map[string]*result.PlayerDamage{
			"a": {Given: 19640, Taken: 14000, ByWeapon: map[string]int{"rl": 19000}},
		},
	}}
	d = deriveDamage(skipped, "a", nil)
	if d.Src != result.SrcDerivedUnbounded {
		t.Errorf("src = %q, want %q — the numbers below are raw wire damage", d.Src, result.SrcDerivedUnbounded)
	}
	if d.Given != 19640 {
		t.Errorf("given = %d, want the raw 19640", d.Given)
	}

	// The marker is demo-global: a team row must not end up split across
	// two src values, and a lazily-absent bounded nest in a STANDARD-mode
	// demo is not a mode degradation.
	lazy := &Result{Damage: &result.DamageResult{
		BoundedMode: "standard",
		ByPlayer:    map[string]*result.PlayerDamage{"a": {Given: 40}},
	}}
	if got := deriveDamage(lazy, "a", nil).Src; got != result.SrcDerived {
		t.Errorf("src = %q, want derived — standard mode, only the nest is absent", got)
	}

	teams := aggregateTeamRows([]result.PlayerStatsRow{
		{Name: "a", Team: "red", Damage: &result.PlayerStatsDamage{Src: result.SrcDerivedUnbounded, Given: 19640}},
		{Name: "b", Team: "red", Damage: &result.PlayerStatsDamage{Src: result.SrcDerivedUnbounded, Given: 100}},
	}, 600000)
	if teams[0].Damage.Src != result.SrcDerivedUnbounded {
		t.Errorf("team src = %q, want the members' %q", teams[0].Damage.Src, result.SrcDerivedUnbounded)
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

	acc := deriveAccuracy(&Result{Shots: shots}, "a", nil)
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

	if rl.HitsConvention != "" {
		t.Errorf("hitsConvention = %q with no hits, want empty — it describes a number that is not there", rl.HitsConvention)
	}

	// With a WIRE damage stream the link is meaningful, so hits appears.
	withDmg := deriveAccuracy(&Result{Shots: shots, Damage: &result.DamageResult{Source: result.DamageSourceKTX}}, "a", nil)
	if h := withDmg.ByWeapon["rl"].Hits; h == nil || *h != 0 {
		t.Errorf("hits with a damage stream = %v, want an observed 0", h)
	}
	// Our rl hits count a fire that landed damage by any path; KTX's count
	// direct impacts only and run ~4x lower. The marker is what stops the two
	// being averaged into one trendline (result.HitsDirectImpact).
	if c := withDmg.ByWeapon["rl"].HitsConvention; c != result.HitsAnyDamage {
		t.Errorf("hitsConvention = %q, want %q — a wire-linked hit is any damage path", c, result.HitsAnyDamage)
	}

	// A RECONSTRUCTED damage section is not linkage: the shot linker never
	// saw a wire damage event there, so hits must stay absent unless the aim
	// recon tier recovered one — presence of the section alone must not
	// re-fabricate the zero.
	recon := deriveAccuracy(&Result{Shots: shots, Damage: &result.DamageResult{Source: result.DamageSourceReconstructed}}, "a", nil)
	if h := recon.ByWeapon["rl"].Hits; h != nil {
		t.Errorf("hits with reconstructed damage = %d, want ABSENT", *h)
	}
	if recon.Src != result.SrcDerived {
		t.Errorf("src with no recovered hits = %q, want %q — attacks alone are shot-derived either way",
			recon.Src, result.SrcDerived)
	}
}

// The old-demo path (schema v74): the aim recon tier fills `hits`, the family
// says which grade it is, and a weapon the tier withheld inherits the
// withhold rather than reading as a measured zero.
func TestDeriveAccuracyReconTierFillsHits(t *testing.T) {
	shots := &result.ShotsResult{ByPlayer: []result.PlayerShots{{
		Player: "a",
		ByWeapon: []result.WeaponShots{
			{Weapon: "lg", Shots: 200, Hits: 0},
			{Weapon: "sng", Shots: 40, Hits: 0},
		},
	}}}
	res := &Result{Shots: shots, Damage: &result.DamageResult{Source: result.DamageSourceReconstructed}}
	// Only lg is in the validated tier, so only lg carries a Recon block.
	acc := deriveAccuracy(res, "a", map[string]map[string]reconHit{
		"a": {"lg": {n: 61, convention: result.HitsAnyDamage}}})
	if acc.Src != result.SrcReconstructed {
		t.Errorf("src = %q, want %q — a recovered hit is not a wire measurement",
			acc.Src, result.SrcReconstructed)
	}
	if h := acc.ByWeapon["lg"].Hits; h == nil || *h != 61 {
		t.Errorf("lg hits = %v, want 61 from the recon tier", h)
	}
	if h := acc.ByWeapon["sng"].Hits; h != nil {
		t.Errorf("sng hits = %d, want ABSENT — the tier validated no nail recovery", *h)
	}
	if acc.ByWeapon["sng"].Attacks != 40 {
		t.Error("a withheld hit count must not cost the weapon its fires")
	}
	// The recon tier reproduces the wire join, so it answers the same
	// question — the convention is the evidence-independent half of the
	// contract and must not move with `src`.
	if c := acc.ByWeapon["lg"].HitsConvention; c != result.HitsAnyDamage {
		t.Errorf("hitsConvention = %q, want %q", c, result.HitsAnyDamage)
	}
	if c := acc.ByWeapon["sng"].HitsConvention; c != "" {
		t.Errorf("withheld sng carries hitsConvention %q, want empty", c)
	}
}

// deriveReconHits must read the PUBLISHED tier and only when the aim section
// says its hits came from a reconstruction — on a wire-measured demo the
// accuracy family links its own fires and this map must stay out of it.
func TestDeriveReconHitsGatedOnAimSource(t *testing.T) {
	aim := func(src string) *result.AimResult {
		return &result.AimResult{
			HitsSource: src,
			Players: []result.PlayerAim{{Player: "a", Weapons: []result.WeaponAim{
				{Weapon: "lg", Shots: 10, Recon: &result.WeaponAimRecon{Hits: 4}},
				{Weapon: "rl", Shots: 20, Recon: &result.WeaponAimRecon{Hits: 12, DirectHits: intp(5)}},
				{Weapon: "ng", Shots: 10},
			}}},
		}
	}
	if got := deriveReconHits(&Result{Aim: aim(result.AimHitsSourceKTX)}); got != nil {
		t.Errorf("recon hits on a wire-measured demo = %v, want nil", got)
	}
	got := deriveReconHits(&Result{Aim: aim(result.AimHitsSourceReconstructed)})
	if len(got["a"]) != 2 || got["a"]["lg"] != (reconHit{n: 4, convention: result.HitsAnyDamage}) {
		t.Errorf("recon hits = %v, want only the weapons carrying a Recon block", got)
	}
	// rl carries the tier's DIRECT-impact count, and says so: that is KTX's
	// own convention for rl/gl and the whole reason an old demo's row can be
	// compared with a block-carrying one (schema v74).
	if got["a"]["rl"] != (reconHit{n: 5, convention: result.HitsDirectImpact}) {
		t.Errorf("rl recon hit = %v, want the directImpact 5", got["a"]["rl"])
	}
}

func TestDeriveAccuracyNilForUnknownPlayer(t *testing.T) {
	shots := &result.ShotsResult{ByPlayer: []result.PlayerShots{{
		Player: "a", ByWeapon: []result.WeaponShots{{Weapon: "rl", Shots: 1}},
	}}}
	if got := deriveAccuracy(&Result{Shots: shots}, "nobody", nil); got != nil {
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

// T1.5: team rows never carried an accuracy family at all, so the web's
// per-team Weapon Stats column read `-` beside per-player rows showing
// real percentages. Attacks and hits sum over members; hits stays ABSENT
// unless every contributing member measured it, because mixing a
// measured member with an unmeasured one understates the team hit-rate
// under a number that looks measured.
func TestTeamAggregationSumsAccuracy(t *testing.T) {
	players := []result.PlayerStatsRow{
		{
			Name: "a", Team: "red",
			Accuracy: &result.PlayerStatsAccuracy{Src: result.SrcDerived, ByWeapon: map[string]result.PlayerStatsAcc{
				"rl": {Attacks: 100, Hits: intp(30)},
				"lg": {Attacks: 400, Hits: intp(120)},
			}},
		},
		{
			Name: "b", Team: "red",
			Accuracy: &result.PlayerStatsAccuracy{Src: result.SrcDerived, ByWeapon: map[string]result.PlayerStatsAcc{
				"rl": {Attacks: 60, Hits: intp(12)},
				// No hits on lg: this member's fires linked to nothing.
				"lg": {Attacks: 200},
			}},
		},
	}
	red := aggregateTeamRows(players, 600000)[0]
	if red.Accuracy == nil {
		t.Fatal("team carries no accuracy family though both members do")
	}
	if red.Accuracy.Src != result.SrcDerived {
		t.Errorf("team accuracy src = %q, want the members' derived", red.Accuracy.Src)
	}
	rl := red.Accuracy.ByWeapon["rl"]
	if rl.Attacks != 160 || rl.Hits == nil || *rl.Hits != 42 {
		t.Errorf("rl = %+v, want 160 attacks / 42 hits", rl)
	}
	lg := red.Accuracy.ByWeapon["lg"]
	if lg.Attacks != 600 {
		t.Errorf("lg attacks = %d, want 600", lg.Attacks)
	}
	if lg.Hits != nil {
		t.Errorf("lg hits = %d, want ABSENT — one member never measured hits, "+
			"and 120/600 would read as a measured 20%% team hit-rate", *lg.Hits)
	}

	// A team where nobody carries the family keeps none.
	none := aggregateTeamRows([]result.PlayerStatsRow{{Name: "c", Team: "blue"}}, 600000)[0]
	if none.Accuracy != nil {
		t.Errorf("accuracy = %+v, want absent", none.Accuracy)
	}
}

// A src disagreement between members is the phantom-roster defect, not a
// data condition (result.SrcMixed), and must surface rather than be
// resolved by whichever member came first.
func TestTeamAggregationAccuracyMixedSrcIsRecorded(t *testing.T) {
	players := []result.PlayerStatsRow{
		{Name: "a", Team: "red", Accuracy: &result.PlayerStatsAccuracy{Src: result.SrcKTX,
			ByWeapon: map[string]result.PlayerStatsAcc{"rl": {Attacks: 10, Hits: intp(4)}}}},
		{Name: "b", Team: "red", Accuracy: &result.PlayerStatsAccuracy{Src: result.SrcDerived,
			ByWeapon: map[string]result.PlayerStatsAcc{"rl": {Attacks: 5, Hits: intp(1),
				HitsConvention: result.HitsAnyDamage}}}},
	}
	// The KTX member's rl hits are direct impacts and the derived member's
	// are any damage path, so the sum answers no single question — the
	// per-weapon marker goes unnamed rather than claiming one of them.
	acc := aggregateTeamRows(players, 600000)[0].Accuracy
	if acc.Src != result.SrcMixed {
		t.Errorf("team accuracy src = %q, want mixed", acc.Src)
	}
	if c := acc.ByWeapon["rl"].HitsConvention; c != "" {
		t.Errorf("team rl hitsConvention = %q, want empty — the members counted different things", c)
	}

	// Members that DO agree carry theirs up.
	for i := range players {
		players[i].Accuracy.Src = result.SrcKTX
		e := players[i].Accuracy.ByWeapon["rl"]
		e.HitsConvention = result.HitsDirectImpact
		players[i].Accuracy.ByWeapon["rl"] = e
	}
	if c := aggregateTeamRows(players, 600000)[0].Accuracy.ByWeapon["rl"].HitsConvention; c != result.HitsDirectImpact {
		t.Errorf("team rl hitsConvention = %q, want %q", c, result.HitsDirectImpact)
	}
}

// v63: the derived damage family copies all THREE per-weapon maps out of
// the reconstruction with the same zero-dropping rule — a weapon the
// player dealt nothing with in that direction is omitted, never
// zero-filled — and team rows sum each of them.
func TestDeriveDamageCopiesVictimSplits(t *testing.T) {
	r := &Result{Damage: &result.DamageResult{
		BoundedMode: "standard",
		ByPlayer: map[string]*result.PlayerDamage{
			"a": {
				Given: 900, GivenTeam: 80, GivenSelf: 20,
				ByWeapon:     map[string]int{"rl": 900, "lg": 0},
				ByWeaponTeam: map[string]int{"sg": 80, "gl": 0},
				ByWeaponSelf: map[string]int{"rl": 20},
				Bounded: &result.PlayerDamage{
					Given: 700, GivenTeam: 30, GivenSelf: 12,
					ByWeapon:     map[string]int{"rl": 700},
					ByWeaponTeam: map[string]int{"sg": 30, "gl": 0},
					ByWeaponSelf: map[string]int{"rl": 12},
				},
			},
		},
	}}
	d := deriveDamage(r, "a", nil)
	// The bounded family is the source, and its zeros are dropped.
	if got, want := d.ByWeaponTeam, map[string]int{"sg": 30}; !reflect.DeepEqual(got, want) {
		t.Errorf("byWeaponTeam = %v, want %v (the gl 0 is dropped, not zero-filled)", got, want)
	}
	if got, want := d.ByWeaponSelf, map[string]int{"rl": 12}; !reflect.DeepEqual(got, want) {
		t.Errorf("byWeaponSelf = %v, want %v", got, want)
	}

	// A player who dealt no team/self damage carries neither map.
	r.Damage.ByPlayer["b"] = &result.PlayerDamage{Given: 10, ByWeapon: map[string]int{"sg": 10}}
	if d := deriveDamage(r, "b", nil); d.ByWeaponTeam != nil || d.ByWeaponSelf != nil {
		t.Errorf("splits = team %v / self %v, want both absent", d.ByWeaponTeam, d.ByWeaponSelf)
	}

	teams := aggregateTeamRows([]result.PlayerStatsRow{
		{Name: "a", Team: "red", Damage: &result.PlayerStatsDamage{
			Src: result.SrcDerived, ByWeaponTeam: map[string]int{"sg": 30}, ByWeaponSelf: map[string]int{"rl": 12}}},
		{Name: "b", Team: "red", Damage: &result.PlayerStatsDamage{
			Src: result.SrcDerived, ByWeaponTeam: map[string]int{"sg": 5, "gl": 40}}},
	}, 600000)
	if got, want := teams[0].Damage.ByWeaponTeam, map[string]int{"sg": 35, "gl": 40}; !reflect.DeepEqual(got, want) {
		t.Errorf("team byWeaponTeam = %v, want %v", got, want)
	}
	if got, want := teams[0].Damage.ByWeaponSelf, map[string]int{"rl": 12}; !reflect.DeepEqual(got, want) {
		t.Errorf("team byWeaponSelf = %v, want %v", got, want)
	}
}

// --- victim-weapon kill split -------------------------------------------

// The classification behind score.byEnemyWeapon: which bucket a kill lands
// in is decided by what the VICTIM held at the kill instant, read off their
// possession streams. The corpus test (checkEnemyWeaponKillsVsKTX) pins the
// result against KTX on real demos; this pins the rules that produce it,
// including the ones no cached demo happens to exercise.
func TestDeriveEnemyWeaponKills(t *testing.T) {
	// One victim per bucket, each holding their loadout for [1000, 5000).
	r := &Result{
		Streams: &result.Streams{Players: []result.PlayerStream{
			{Name: "vBoth", RL: iv(1000, 5000), LG: iv(1000, 5000)},
			{Name: "vRL", RL: iv(1000, 5000)},
			{Name: "vLG", LG: iv(1000, 5000)},
			{Name: "vMid", SNG: iv(1000, 5000)},
			{Name: "vBare"},
			{Name: "k"},
		}},
		Frags: &result.FragResult{
			KillsMeasured: true,
			Frags: []result.FragEntry{
				{Time: 2000, Killer: "k", Victim: "vBoth", Weapon: "rl"},
				{Time: 2000, Killer: "k", Victim: "vRL", Weapon: "rl"},
				{Time: 2000, Killer: "k", Victim: "vLG", Weapon: "lg"},
				{Time: 2000, Killer: "k", Victim: "vMid", Weapon: "rl"},
				{Time: 2000, Killer: "k", Victim: "vBare", Weapon: "rl"},
				// No stream at all: unknown, never "unarmed".
				{Time: 2000, Killer: "k", Victim: "ghost", Weapon: "rl"},
				// Positional kills classify like any other — unlike the
				// damage side, which has no hit to read an inventory from.
				{Time: 2000, Killer: "k", Victim: "vBoth", Weapon: "tele"},
				// Neither a suicide nor a teamkill is an enemy kill, so
				// neither may enter the partition.
				{Time: 2000, Killer: "k", Victim: "k", Weapon: "rl", IsSuicide: true},
				{Time: 2000, Killer: "k", Victim: "vRL", Weapon: "rl", IsTeamKill: true},
			},
		},
	}

	got := deriveEnemyWeaponKills(r)["k"].byBucket
	want := map[string]int{"both": 2, "rl": 1, "lg": 1, "mid": 1, "sg": 1, "unknown": 1}
	if len(got) != len(want) {
		t.Fatalf("byEnemyWeapon = %v, want %v", got, want)
	}
	for k, n := range want {
		if got[k] != n {
			t.Errorf("byEnemyWeapon[%s] = %d, want %d (full map %v)", k, got[k], n, got)
		}
	}

	// The cross-tab is the joint distribution the bucket map is a marginal
	// of, so summing it over inner keys must give byWeapon and over outer
	// keys must give byEnemyWeapon. Publishing three views of one kill set
	// is only safe while that holds.
	cross := deriveEnemyWeaponKills(r)["k"].cross
	if cross["rl"]["both"] != 1 || cross["tele"]["both"] != 1 {
		t.Errorf("cross[rl][both]=%d cross[tele][both]=%d, want 1 and 1 — a kill is keyed by BOTH the killer's weapon and the victim's loadout",
			cross["rl"]["both"], cross["tele"]["both"])
	}
	if cross["lg"]["lg"] != 1 || len(cross["lg"]) != 1 {
		t.Errorf("cross[lg] = %v, want exactly {lg:1}", cross["lg"])
	}
	outer := map[string]int{}
	inner := map[string]int{}
	for w, m := range cross {
		for b, n := range m {
			outer[w] += n
			inner[b] += n
		}
	}
	// byWeapon here is the frag log's own tally over the same entries.
	wantOuter := map[string]int{"rl": 5, "lg": 1, "tele": 1}
	if !reflect.DeepEqual(outer, wantOuter) {
		t.Errorf("cross-tab summed over victim buckets = %v, want %v (= score.byWeapon)", outer, wantOuter)
	}
	if !reflect.DeepEqual(inner, got) {
		t.Errorf("cross-tab summed over killer weapons = %v, want %v (= score.byEnemyWeapon)", inner, got)
	}

	// The half-open endpoint rule, which decides a real disagreement with
	// KTX on the corpus (see heldAt): a run ending exactly at the kill
	// instant means the server had already taken the weapon away.
	edge := &Result{
		Streams: &result.Streams{Players: []result.PlayerStream{
			{Name: "v", RL: iv(1000, 2000)},
		}},
		Frags: &result.FragResult{KillsMeasured: true, Frags: []result.FragEntry{
			{Time: 2000, Killer: "k", Victim: "v", Weapon: "rl"},
			{Time: 1999, Killer: "k", Victim: "v", Weapon: "rl"},
			{Time: 1000, Killer: "k", Victim: "v", Weapon: "rl"},
		}},
	}
	if got := deriveEnemyWeaponKills(edge)["k"].byBucket; got["rl"] != 2 || got["sg"] != 1 {
		t.Errorf("endpoint handling = %v, want rl:2 (t=1000 and t=1999) + sg:1 (t=2000, run already closed)", got)
	}
}
