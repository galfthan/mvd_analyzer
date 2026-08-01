package view

import (
	"errors"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// livesResult builds a Result whose player has explicit lives, so the tests
// pin Lives' own behaviour rather than the v64 derivation (which has its own
// tests in analyzer/alive_intervals_test.go).
func livesResult(alive []result.Interval, frags []result.FragEntry) *result.Result {
	return &result.Result{
		Frags: &result.FragResult{Frags: frags, KillsMeasured: true},
		Streams: &result.Streams{
			Global: result.GlobalStream{MatchStart: 0, MatchEnd: 60000},
			Players: []result.PlayerStream{{
				Name: "A", Team: "red", Alive: alive,
				Loc: []result.ChangeI16{{T: 0, V: 1}, {T: 9000, V: 2}},
			}},
		},
		TimelineAnalysis: &result.TimelineAnalysisResult{LocTable: []string{"", "spawn", "mid"}},
		Match: &result.MatchResult{Players: []result.PlayerStat{
			{Name: "A", Team: "red"}, {Name: "B", Team: "blue"},
		}},
	}
}

// mustLives runs a query that must succeed. The error contract has its own test
// (TestLivesUnavailable); everywhere else an error is a fixture bug.
func mustLives(t *testing.T, r *result.Result, opts LivesOptions) *LivesView {
	t.Helper()
	got, err := Lives(r, opts)
	if err != nil {
		t.Fatalf("Lives(%+v): %v", opts, err)
	}
	return got
}

func TestLivesOnePerAliveInterval(t *testing.T) {
	alive := []result.Interval{{Start: 0, End: 10000}, {Start: 12000, End: 30000}}
	frags := []result.FragEntry{
		kill(3000, "A", "B", "rl"),
		kill(9000, "A", "B", "lg"),
		{Time: 10000, Killer: "B", Victim: "A", Weapon: "rl"}, // ends life 0
		kill(20000, "A", "B", "lg"),
	}
	got := mustLives(t, livesResult(alive, frags), LivesOptions{})
	if len(got.Lives) != 2 {
		t.Fatalf("got %d lives, want 2", len(got.Lives))
	}

	l0, l1 := got.Lives[0], got.Lives[1]
	if l0.Index != 0 || l1.Index != 1 {
		t.Errorf("indices = %d,%d, want 0,1", l0.Index, l1.Index)
	}
	if l0.Start != 0 || l0.End != 10000 {
		t.Errorf("life 0 = [%d,%d], want [0,10000]", l0.Start, l0.End)
	}
	if l0.Kills != 2 {
		t.Errorf("life 0 kills = %d, want 2", l0.Kills)
	}
	if l1.Kills != 1 {
		t.Errorf("life 1 kills = %d, want 1", l1.Kills)
	}
	// The death that ENDED the life is attributed to it.
	if l0.KilledBy != "B" || l0.DeathWeapon != "rl" {
		t.Errorf("life 0 ended by %q/%q, want B/rl", l0.KilledBy, l0.DeathWeapon)
	}
	// The last life ran on past the frag log, so nothing ended it.
	if l1.KilledBy != "" {
		t.Errorf("life 1 killedBy = %q, want empty (it did not end in a death)", l1.KilledBy)
	}
	if l0.SpawnLoc != "spawn" {
		t.Errorf("life 0 spawnLoc = %q, want spawn", l0.SpawnLoc)
	}
	if l0.DeathLoc != "mid" {
		t.Errorf("life 0 deathLoc = %q, want mid (the loc changed at 9000)", l0.DeathLoc)
	}
	if l0.Team != "red" {
		t.Errorf("team = %q, want red", l0.Team)
	}
}

// The reconciliation that makes lives trustworthy: every kill in the match
// belongs to exactly one life, and the lives' durations sum to alive time.
func TestLivesReconcileWithFragsAndAliveTime(t *testing.T) {
	alive := []result.Interval{
		{Start: 0, End: 10000}, {Start: 12000, End: 25000}, {Start: 27000, End: 40000},
	}
	frags := []result.FragEntry{
		kill(1000, "A", "B", "rl"), kill(5000, "A", "B", "lg"),
		{Time: 10000, Killer: "B", Victim: "A", Weapon: "rl"},
		kill(13000, "A", "B", "lg"), kill(24000, "A", "B", "sg"),
		{Time: 25000, Killer: "B", Victim: "A", Weapon: "lg"},
		kill(30000, "A", "B", "rl"),
	}
	got := mustLives(t, livesResult(alive, frags), LivesOptions{})

	var kills int
	var dur int32
	for _, l := range got.Lives {
		kills += l.Kills
		dur += l.DurationMs
	}
	// A's kills in the frag log, counted independently.
	want := 0
	for _, f := range frags {
		if f.Killer == "A" && !f.IsSuicide && !f.IsTeamKill {
			want++
		}
	}
	if kills != want {
		t.Errorf("kills across lives = %d, want %d — a kill fell between two lives or was double counted", kills, want)
	}
	var aliveMs int32
	for _, iv := range alive {
		aliveMs += iv.End - iv.Start
	}
	if dur != aliveMs {
		t.Errorf("durations sum to %d ms, want %d (total alive time)", dur, aliveMs)
	}
}

// A kill landing exactly on a life boundary must be counted once, not twice —
// and the two boundary shapes resolve it in OPPOSITE directions. Intervals are
// closed at both ends for the stats block, so the only thing stopping a double
// count is that exactly one of the two lives claims the shared instant:
//
//   - TOUCHING LIVES (a same-millisecond death and respawn, schema v64) give
//     the instant to the life that was ENDING — the player was living it when
//     the event happened.
//   - A REAL DEAD GAP gives an event on the NEXT life's spawn instant to the
//     life just BEGUN, and the outgoing window of the life that ended (which
//     runs to that instant) must not claim it as well.
func TestLivesBoundaryKillCountedOnce(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alive []result.Interval
		at    int32
		owner int    // index of the life the kill belongs to
		why   string // why that one
	}{
		{
			"touching lives", []result.Interval{{Start: 0, End: 10000}, {Start: 10000, End: 20000}},
			10000, 0, "it belongs to the life that was ending",
		},
		{
			"across a dead gap", []result.Interval{{Start: 0, End: 10000}, {Start: 12000, End: 30000}},
			12000, 1, "it belongs to the life that had begun, not the one that ended",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mustLives(t, livesResult(tc.alive, []result.FragEntry{kill(tc.at, "A", "B", "rl")}), LivesOptions{})
			if len(got.Lives) != 2 {
				t.Fatalf("got %d lives, want 2 (the death survives its same-ms respawn)", len(got.Lives))
			}
			if total := got.Lives[0].Kills + got.Lives[1].Kills; total != 1 {
				t.Errorf("boundary kill counted %d times across two lives, want 1 — "+
					"summing per-life kills would exceed the player's match total", total)
			}
			if got.Lives[tc.owner].Kills != 1 {
				t.Errorf("the boundary kill did not go to life %d; %s", tc.owner, tc.why)
			}
		})
	}
}

// A posthumous kill — a rocket landing after its shooter died — belongs to the
// life that fired it. Without this, per-life kills do not sum to the player's
// match total; measured on real demos, five such kills landed 76-197 ms after
// the killer's own death, every one an rl.
func TestLivesPosthumousKillBelongsToTheFiringLife(t *testing.T) {
	alive := []result.Interval{{Start: 0, End: 10000}, {Start: 12000, End: 30000}}
	frags := []result.FragEntry{
		{Time: 10000, Killer: "B", Victim: "A", Weapon: "rl"}, // A dies
		kill(10150, "A", "B", "rl"),                           // A's rocket lands 150 ms later
	}
	got := mustLives(t, livesResult(alive, frags), LivesOptions{})
	if got.Lives[0].Kills != 1 {
		t.Errorf("life 0 kills = %d, want 1 — the rocket was fired during that life", got.Lives[0].Kills)
	}
	if got.Lives[1].Kills != 0 {
		t.Errorf("life 1 kills = %d, want 0 — it had not started when the rocket landed", got.Lives[1].Kills)
	}
	// The duration is still the ALIVE time; only outgoing events extend.
	if got.Lives[0].DurationMs != 10000 {
		t.Errorf("duration = %d, want 10000 — the posthumous window must not lengthen the life",
			got.Lives[0].DurationMs)
	}
}

func TestLivesFilters(t *testing.T) {
	alive := []result.Interval{
		{Start: 0, End: 5000}, {Start: 6000, End: 30000}, {Start: 31000, End: 31500},
	}
	res := livesResult(alive, nil)

	if all := mustLives(t, res, LivesOptions{}); len(all.Lives) != 3 {
		t.Fatalf("got %d lives, want 3", len(all.Lives))
	}
	// MinMs drops the 500 ms life.
	if long := mustLives(t, res, LivesOptions{MinMs: 1000}); len(long.Lives) != 2 {
		t.Errorf("minMs=1000 gave %d lives, want 2", len(long.Lives))
	}
	// From/To keep lives that OVERLAP the window, not only those contained.
	if win := mustLives(t, res, LivesOptions{From: 4000, To: 7000}); len(win.Lives) != 2 {
		t.Errorf("from/to gave %d lives, want 2 (both straddle the window)", len(win.Lives))
	}
	if none := mustLives(t, res, LivesOptions{Players: []string{"nobody"}}); len(none.Lives) != 0 {
		t.Errorf("unknown player gave %d lives, want 0", len(none.Lives))
	}
}

// PlayerStream.Alive has THREE states and the response must not flatten two of
// them into one: nil is "liveness was not measurable", [] is "measured, and
// never alive", [...] are the lives. Both emit zero rows, so an empty
// `lives: []` alone said "nobody ever lived" in both cases (incident:
// adversarial review, 2026-08-01). Unmeasurable is now a 422 with
// measured.liveness false; measured-but-never-alive is a 200 with an empty
// list, which is a true statement.
func TestLivesUnmeasuredLivenessIsNotAnEmptyMatch(t *testing.T) {
	unmeasured := livesResult(nil, []result.FragEntry{kill(1000, "A", "B", "rl")})
	unmeasured.Streams.Players[0].Alive = nil
	if err := LivesAvailable(unmeasured); !errors.Is(err, ErrUnavailable) {
		t.Errorf("LivesAvailable on unmeasurable liveness: err = %v, want ErrUnavailable", err)
	}
	if _, err := Lives(unmeasured, LivesOptions{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Lives on unmeasurable liveness: err = %v, want ErrUnavailable — "+
			"an empty list would claim the player never lived", err)
	}
	// hot windows do not need liveness, so they still answer — and report the
	// same fact on the same envelope field.
	if hw := mustHW(t, unmeasured, HotWindowsOptions{}); hw.Measured.Liveness {
		t.Error("measured.liveness is true on a demo where no player has an Alive stream")
	}

	// Measured, and never alive: a real answer, not an error.
	never := livesResult([]result.Interval{}, nil)
	got, err := Lives(never, LivesOptions{})
	if err != nil {
		t.Fatalf("measured-but-never-alive must be served, not 422'd: %v", err)
	}
	if len(got.Lives) != 0 {
		t.Errorf("got %+v, want no lives", got.Lives)
	}
	if !got.Measured.Liveness {
		t.Error("measured.liveness is false on a demo whose Alive is an empty (measured) list")
	}
}

// A demo with no damage stream still yields lives — the segmentation needs
// only spawn/death, so the damage half of the stats block is simply absent.
func TestLivesWithoutDamageStream(t *testing.T) {
	res := livesResult([]result.Interval{{Start: 0, End: 10000}}, []result.FragEntry{kill(1000, "A", "B", "rl")})
	res.Damage = nil
	got, err := Lives(res, LivesOptions{})
	if err != nil {
		t.Fatalf("no damage stream must not fail a lives query: %v", err)
	}
	if len(got.Lives) != 1 || got.Lives[0].Kills != 1 {
		t.Errorf("got %+v, want one life with one kill", got.Lives)
	}
	if got.Lives[0].DamageGiven != 0 {
		t.Errorf("damageGiven = %d, want 0", got.Lives[0].DamageGiven)
	}
}

// Incoming events are attributed by the same window as outgoing ones, so a
// death recorded while the player is ALREADY dead — the KTX dtTELE2 deflection
// — belongs to the life that was ending. Measured on 212260, where dropping
// these made per-life deaths sum to 49 against a match total of 52.
//
// The consequence is that Deaths is not capped at 1, which the schema doc used
// to claim.
func TestLivesDeathInADeadGapBelongsToTheEndingLife(t *testing.T) {
	alive := []result.Interval{{Start: 0, End: 10000}, {Start: 12000, End: 30000}}
	frags := []result.FragEntry{
		{Time: 10000, Killer: "B", Victim: "A", Weapon: "rl"},                    // ends life 0
		{Time: 11000, Killer: "A", Victim: "A", Weapon: "tele", IsSuicide: true}, // while already dead
	}
	got := mustLives(t, livesResult(alive, frags), LivesOptions{})
	if got.Lives[0].Deaths != 2 || got.Lives[0].Suicides != 1 {
		t.Errorf("life 0 deaths/suicides = %d/%d, want 2/1 — the in-gap death belongs to the life that ended",
			got.Lives[0].Deaths, got.Lives[0].Suicides)
	}
	if got.Lives[1].Deaths != 0 {
		t.Errorf("life 1 deaths = %d, want 0 — it had not begun", got.Lives[1].Deaths)
	}
}

// The two match edges, counted and reported. Alive[0].Start is clipped to when
// the player was first OBSERVED, and the last life can end before MatchEnd, so
// without closing both edges onto the outer lives an event there belongs to no
// life at all.
//
// The windows are also on the row, because without them a consumer cannot see
// that the counts cover the life PLUS the dead gap after it while durationMs is
// alive time only — so any rate divided by durationMs reads high, silently.
func TestLivesCoverTheWholeMatchWindow(t *testing.T) {
	alive := []result.Interval{{Start: 500, End: 10000}, {Start: 12000, End: 50000}}
	frags := []result.FragEntry{
		kill(0, "A", "B", "rl"),     // before the first life's clipped start
		kill(60000, "A", "B", "lg"), // exactly on MatchEnd, past the last life's end
	}
	got := mustLives(t, livesResult(alive, frags), LivesOptions{})
	if len(got.Lives) != 2 {
		t.Fatalf("got %d lives, want 2", len(got.Lives))
	}
	l0, l1 := got.Lives[0], got.Lives[1]
	if l0.Kills != 1 {
		t.Errorf("life 0 kills = %d, want 1 — the t=0 kill predates the clipped start", l0.Kills)
	}
	if l1.Kills != 1 {
		t.Errorf("life 1 kills = %d, want 1 — the MatchEnd kill postdates the last life", l1.Kills)
	}
	// Life 0: back to MatchStart (its own start is clipped to first
	// observation) and forward to the next life's spawn.
	if l0.AttrStart != 0 || l0.AttrEnd != 12000 {
		t.Errorf("life 0 attribution = [%d,%d], want [0,12000]", l0.AttrStart, l0.AttrEnd)
	}
	// Life 1: its own start, out to MatchEnd.
	if l1.AttrStart != 12000 || l1.AttrEnd != 60000 {
		t.Errorf("life 1 attribution = [%d,%d], want [12000,60000]", l1.AttrStart, l1.AttrEnd)
	}
	// The spans tile the match end to end, and durationMs is NOT their width.
	if l0.AttrEnd != l1.AttrStart {
		t.Errorf("a gap between life 0's window end (%d) and life 1's start (%d) belongs to no life",
			l0.AttrEnd, l1.AttrStart)
	}
	if l0.DurationMs != 9500 || l0.DurationMs == l0.AttrEnd-l0.AttrStart {
		t.Errorf("durationMs = %d; it is ALIVE time (9500), not the attribution width (%d) — "+
			"the widened window must not lengthen the life", l0.DurationMs, l0.AttrEnd-l0.AttrStart)
	}
}

func TestLivesEndReason(t *testing.T) {
	// Ends at a death in the frag log.
	died := livesResult([]result.Interval{{Start: 0, End: 10000}},
		[]result.FragEntry{{Time: 10000, Killer: "B", Victim: "A", Weapon: "rl"}})
	// Ends at a death only the DF_DEAD / STAT_HEALTH detectors saw.
	unobit := livesResult([]result.Interval{{Start: 0, End: 10000}}, nil)
	unobit.Streams.Players[0].Deaths = []int32{10000}
	// Runs to match end.
	survived := livesResult([]result.Interval{{Start: 0, End: 60000}}, nil)
	// Ends early with no death at all: the position track stopped.
	quit := livesResult([]result.Interval{{Start: 0, End: 30000}}, nil)

	cases := []struct {
		name           string
		res            *result.Result
		want, killedBy string
	}{
		{"death", died, LifeEndDeath, "B"},
		{"death seen only by the health path", unobit, LifeEndDeath, ""},
		{"survived", survived, LifeEndMatchEnd, ""},
		{"left the game", quit, LifeEndLeftGame, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := mustLives(t, c.res, LivesOptions{}).Lives[0]
			if l.EndReason != c.want {
				t.Errorf("endReason = %q, want %q", l.EndReason, c.want)
			}
			if l.KilledBy != c.killedBy {
				t.Errorf("killedBy = %q, want %q", l.KilledBy, c.killedBy)
			}
		})
	}
}

func TestLivesItemsTakenAndWeaponsHeld(t *testing.T) {
	res := livesResult([]result.Interval{{Start: 0, End: 10000}, {Start: 12000, End: 30000}}, nil)
	res.Items = &result.ItemsResult{Items: []result.ItemTimeline{
		{Name: "ra_1", Kind: "ra", Phases: []result.ItemPhase{
			{TakenAt: 3000, TakenBy: "A"},
			{TakenAt: 25000, TakenBy: "B"},
		}},
		{Name: "quad_1", Kind: "quad", Phases: []result.ItemPhase{
			{TakenAt: 11000, TakenBy: "A"}, // in the dead gap that follows life 0
		}},
	}}
	p := &res.Streams.Players[0]
	p.RL = []result.Interval{{Start: 4000, End: 10000}}
	p.LG = []result.Interval{{Start: 10000, End: 20000}} // acquired at the death instant

	got := mustLives(t, res, LivesOptions{})
	l0, l1 := got.Lives[0], got.Lives[1]
	if len(l0.ItemsTaken) != 2 || l0.ItemsTaken[0].Item != "ra_1" || l0.ItemsTaken[1].Kind != "quad" {
		t.Errorf("life 0 itemsTaken = %+v, want ra_1 then quad_1 (the gap belongs to the life before it)", l0.ItemsTaken)
	}
	if len(l1.ItemsTaken) != 0 {
		t.Errorf("life 1 itemsTaken = %+v, want none", l1.ItemsTaken)
	}
	if l1.ItemsTaken == nil {
		t.Error("measured-but-empty itemsTaken serialised as null; null is reserved for a demo with no item timeline")
	}
	// Possession is clipped to the ALIVE interval: KTX does not clear the
	// weapon bits on death, so the dead gap would otherwise hand every life the
	// weapons its owner died holding.
	if len(l0.WeaponsHeld) != 1 || l0.WeaponsHeld[0] != "rl" {
		t.Errorf("life 0 weaponsHeld = %v, want [rl] — the lg arrives at the death instant", l0.WeaponsHeld)
	}
	if len(l1.WeaponsHeld) != 1 || l1.WeaponsHeld[0] != "lg" {
		t.Errorf("life 1 weaponsHeld = %v, want [lg]", l1.WeaponsHeld)
	}

	res.Items = nil
	if none := mustLives(t, res, LivesOptions{}); none.Lives[0].ItemsTaken != nil {
		t.Errorf("itemsTaken = %+v on a demo with no item timeline, want null", none.Lives[0].ItemsTaken)
	}
}

// Alive is a STORED field this package does not produce. Overlapping intervals
// cannot occur today, but reading one without checking would silently
// double-count everything in the overlap, so the attribution windows are
// derived from successive life STARTS and can never overlap whatever Alive says.
func TestLivesOverlappingAliveIntervalsDoNotDoubleCount(t *testing.T) {
	alive := []result.Interval{{Start: 0, End: 15000}, {Start: 10000, End: 30000}}
	frags := []result.FragEntry{
		kill(5000, "A", "B", "rl"),
		kill(12000, "A", "B", "lg"), // inside the overlap
		kill(20000, "A", "B", "sg"),
	}
	got := mustLives(t, livesResult(alive, frags), LivesOptions{})
	total := 0
	for _, l := range got.Lives {
		total += l.Kills
	}
	if total != 3 {
		t.Errorf("kills across overlapping lives = %d, want 3 — the overlap was counted twice", total)
	}
}

// The documented cost of filtering: a filtered response does not reconcile.
// MinMs drops a life together with its attribution window, so the events inside
// that window leave the response entirely.
func TestLivesFilteredResponsesDoNotReconcile(t *testing.T) {
	alive := []result.Interval{{Start: 0, End: 10000}, {Start: 12000, End: 12100}, {Start: 14000, End: 30000}}
	frags := []result.FragEntry{kill(12500, "A", "B", "rl")}
	res := livesResult(alive, frags)

	total := 0
	for _, l := range mustLives(t, res, LivesOptions{}).Lives {
		total += l.Kills
	}
	if total != 1 {
		t.Fatalf("unfiltered kills = %d, want 1", total)
	}

	total = 0
	for _, l := range mustLives(t, res, LivesOptions{MinMs: 1000}).Lives {
		total += l.Kills
	}
	if total != 0 {
		t.Errorf("minMs kills = %d, want 0 — the kill belonged to the dropped life's window, and "+
			"LivesOptions says so; if this changed, update the doc rather than the number", total)
	}
}

// A demo with NO match window is a real, reachable state — the demo-timebase
// path (no match start detected) leaves Global.MatchEnd at 0 while
// analyzer.deriveAliveIntervals still publishes Alive from each player's last
// observed sample, so lives exist and are served. The partition has to survive
// it: extending the last life's window only "if matchEnd > its end" left
// everything after the last life attributed to no life at all.
func TestLivesPartitionWithoutAMatchWindow(t *testing.T) {
	res := livesResult([]result.Interval{{Start: 0, End: 20000}}, []result.FragEntry{
		kill(0, "A", "B", "rl"), kill(5000, "A", "B", "rl"),
		kill(10000, "A", "B", "rl"), kill(10001, "A", "B", "rl"),
		kill(12000, "A", "B", "rl"), kill(20000, "A", "B", "rl"),
		kill(29999, "A", "B", "rl"), kill(30000, "A", "B", "rl"),
	})
	res.Streams.Global.MatchEnd = 0
	res.Streams.Global.TimeBase = "demo"
	res.Shots = &result.ShotsResult{}
	res.Damage = &result.DamageResult{}
	for _, f := range res.Frags.Frags {
		res.Shots.Shots = append(res.Shots.Shots, result.Shot{Time: f.Time, Player: "A", Weapon: "rl", Hit: true})
		res.Damage.Events = append(res.Damage.Events,
			result.DamageEntry{Time: f.Time, Attacker: "A", Victim: "B", Weapon: "rl", Damage: 10})
	}

	got := mustLives(t, res, LivesOptions{})
	if len(got.Lives) != 1 {
		t.Fatalf("got %d lives, want 1", len(got.Lives))
	}
	l := got.Lives[0]
	if l.Kills != 8 || l.DamageGiven != 80 || l.Shots != 8 {
		t.Errorf("kills/given/shots = %d/%d/%d, want 8/80/8 — the events after the last life "+
			"were attributed to no life at all", l.Kills, l.DamageGiven, l.Shots)
	}
	if l.DurationMs != 20000 {
		t.Errorf("durationMs = %d, want 20000 — the widened window must not lengthen the life", l.DurationMs)
	}
	// A player alive at the end of the recorded play did not "leave the game";
	// with no match window the whole matchEnd test used to fail closed.
	if l.EndReason != LifeEndMatchEnd {
		t.Errorf("endReason = %q, want %q for a life running to the end of play", l.EndReason, LifeEndMatchEnd)
	}
	// One whose own track stopped earlier still reads leftGame, so the fallback
	// discriminates rather than blanket-relabelling.
	res.Streams.Players = append(res.Streams.Players, result.PlayerStream{
		Name: "Q", Alive: []result.Interval{{Start: 0, End: 9000}},
	})
	got = mustLives(t, res, LivesOptions{Players: []string{"Q"}})
	if len(got.Lives) != 1 || got.Lives[0].EndReason != LifeEndLeftGame {
		t.Errorf("early-quitter endReason = %+v, want %q", got.Lives, LifeEndLeftGame)
	}
}

// An inverted window selects nothing, matching /frags, /damage and
// /hot-windows — all of which are empty for from > to. The two interval
// endpoints must at least agree with each other: lives used to return every
// life that straddled both bounds while hot windows returned nothing.
func TestLivesInvertedRangeMatchesTheSiblings(t *testing.T) {
	res := livesResult([]result.Interval{{Start: 0, End: 30000}}, []result.FragEntry{kill(500, "A", "B", "rl")})
	if lv := mustLives(t, res, LivesOptions{From: 20000, To: 1000}); len(lv.Lives) != 0 {
		t.Errorf("from=20000&to=1000 gave %d lives; the range is empty", len(lv.Lives))
	}
	if hw := mustHW(t, res, HotWindowsOptions{From: 20000, To: 1000}); len(hw.Windows) != 0 {
		t.Fatalf("fixture: hot windows should be empty too, got %+v", hw.Windows)
	}
	fv, err := Frags(res, FragOptions{From: 20000, To: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(fv.Frags) != 0 {
		t.Errorf("/frags returned %d entries for an inverted range — the sibling rule moved", len(fv.Frags))
	}
}

// Both interval endpoints echo the damage family their stats block was
// computed in, on every response, as /damage does. Without it `damageGiven` on
// a row is a number with no stated family.
func TestLivesEchoesTheDamageFamily(t *testing.T) {
	bounded := 40
	res := livesResult([]result.Interval{{Start: 0, End: 30000}}, nil)
	res.Damage = &result.DamageResult{
		BoundedMode: "standard",
		Events: []result.DamageEntry{
			{Time: 1000, Attacker: "A", Victim: "B", Weapon: "rl", Damage: 100, Bounded: &bounded},
		},
	}
	for _, tc := range []struct {
		dmg, wantFam string
		wantGiven    int
	}{
		{"", "raw", 100},
		{"raw", "raw", 100},
		{"bounded", "bounded", 40},
	} {
		got := mustLives(t, res, LivesOptions{Dmg: tc.dmg})
		if got.Dmg != tc.wantFam || got.BoundedMode != "standard" {
			t.Errorf("dmg=%q: envelope dmg/boundedMode = %q/%q, want %q/standard",
				tc.dmg, got.Dmg, got.BoundedMode, tc.wantFam)
		}
		if got.Lives[0].DamageGiven != tc.wantGiven {
			t.Errorf("dmg=%q: damageGiven = %d, want %d", tc.dmg, got.Lives[0].DamageGiven, tc.wantGiven)
		}
	}
	// A demo with no damage stream names no family; measured.damage says why.
	none := mustLives(t, livesResult([]result.Interval{{Start: 0, End: 30000}}, nil), LivesOptions{})
	if none.Dmg != "" || none.BoundedMode != "" || none.Measured.Damage {
		t.Errorf("no damage stream: dmg=%q boundedMode=%q measured.damage=%v",
			none.Dmg, none.BoundedMode, none.Measured.Damage)
	}
}

func TestLivesUnavailable(t *testing.T) {
	if _, err := Lives(&result.Result{}, LivesOptions{}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("no streams: err = %v, want ErrUnavailable", err)
	}
	skipped := livesResult([]result.Interval{{Start: 0, End: 1000}}, nil)
	skipped.Damage = &result.DamageResult{BoundedMode: "skipped:midair"}
	if _, err := Lives(skipped, LivesOptions{Dmg: "bounded"}); !errors.Is(err, ErrBoundedUnavailable) {
		t.Errorf("explicit dmg=bounded on a skipped demo: err = %v, want ErrBoundedUnavailable", err)
	}
}
