package view_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-analytics/view"
)

// realDemos are the cached demos these tests reconcile against. 203199 and
// 212260 are here because they are the only ones in the corpus carrying the
// awkward cases: 203199 has touching lives whose boundary event is a DEATH
// (a kills-only reconciliation cannot see the touching rule at all), and
// 212260 has deaths inside a dead gap, a telefrag at t=0 before the first life
// is clipped to start, and kills landing exactly on a next-spawn instant.
var realDemos = []string{
	"203199.mvd.gz",
	"211805.mvd.gz",
	"212260.mvd.gz",
	"212423.mvd.gz",
	"212483.mvd.gz",
	"216835.mvd.gz",
}

// lifeTotals is every per-life quantity that must partition the match.
type lifeTotals struct {
	kills, deaths, suicides, teamKills   int
	dmgGiven, dmgTaken, dmgTeam, dmgSelf int
	shots, hits                          int
}

func (t lifeTotals) String() string {
	return fmt.Sprintf("kills=%d deaths=%d suicides=%d tk=%d given=%d taken=%d team=%d self=%d shots=%d hits=%d",
		t.kills, t.deaths, t.suicides, t.teamKills, t.dmgGiven, t.dmgTaken, t.dmgTeam, t.dmgSelf, t.shots, t.hits)
}

// matchTotals counts the whole match with exactly the classification rules the
// per-interval builder uses. What is under test is therefore the PARTITION —
// that every event is attributed to exactly one life — and not the rules,
// which the interval-stats parity test pins against /frags and /damage.
//
// The damage half is taken from view.Damage rather than re-summed from the hit
// log, because the hit log is not the whole of a player's damage: telefrag and
// stomp value is folded into given/taken without any per-hit entry, so a
// hand-rolled sum here would disagree with the endpoint a caller would check
// their per-life totals against. /damage is that endpoint, so it is the oracle.
func matchTotals(r *result.Result) map[string]*lifeTotals {
	m := map[string]*lifeTotals{}
	get := func(name string) *lifeTotals {
		if name == "" {
			return &lifeTotals{}
		}
		if m[name] == nil {
			m[name] = &lifeTotals{}
		}
		return m[name]
	}
	if r.Frags != nil {
		for _, e := range r.Frags.Frags {
			v := get(e.Victim)
			v.deaths++
			if e.IsSuicide {
				v.suicides++
			}
			switch {
			case e.IsTeamKill:
				get(e.Killer).teamKills++
			case e.IsSuicide:
			default:
				get(e.Killer).kills++
			}
		}
	}
	if dv, err := view.Damage(r, view.DamageOptions{Summary: true}); err == nil {
		for name, pd := range dv.ByPlayer {
			p := get(name)
			p.dmgGiven += pd.Given
			p.dmgTaken += pd.Taken
			p.dmgTeam += pd.GivenTeam
			p.dmgSelf += pd.GivenSelf
		}
	}
	if r.Shots != nil {
		for _, s := range r.Shots.Shots {
			p := get(s.Player)
			p.shots++
			if s.Hit {
				p.hits++
			}
		}
	}
	return m
}

func analyzeCached(t *testing.T, name string) *result.Result {
	t.Helper()
	path := filepath.Join("..", "testdata", "cache", name)
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	res, err := analyzer.NewDefaultRegistry().Analyze(path)
	if err != nil || res == nil || res.Frags == nil || res.Streams == nil {
		return nil
	}
	return res
}

// The property that makes lives trustworthy: a player's lives PARTITION the
// match, so summing any per-life count over an unfiltered response gives back
// their match total. Every event is attributed exactly once — not dropped in a
// dead gap, not counted twice on a boundary, not orphaned before the first life
// or after the last.
//
// It is not free. Five things break it if unhandled, and all five were found
// on real demos rather than reasoned about:
//
//   - POSTHUMOUS OUTGOING EVENTS. A rocket in flight when its shooter dies
//     still kills; on 211805 five landed 76-197 ms after the killer's own
//     death.
//   - TOUCHING LIVES. A same-millisecond death and respawn yields two lives
//     sharing an instant; on 203199 the shared event is a death, which is why
//     a kills-only reconciliation could not see the rule at all.
//   - EVENTS INSIDE A DEAD GAP. On 212260 nlk takes two `tele` suicides while
//     already dead.
//   - EVENTS BEFORE THE FIRST LIFE. Alive[0].Start is clipped to first
//     observation; on 212260 doberman's first life starts at 553 while a
//     telefrag at t=0 costs him a teamkill.
//   - EVENTS AT MATCH END. On 212483 sailorman's last life ends 64 ms before
//     MatchEnd and a damage event lands on the final millisecond.
//
// Cache-gated so an offline `make test` stays green.
func TestLivesPartitionTheMatchOnRealDemos(t *testing.T) {
	checked := 0
	for _, name := range realDemos {
		res := analyzeCached(t, name)
		if res == nil {
			continue
		}
		lv, err := view.Lives(res, view.LivesOptions{})
		if err != nil {
			t.Fatalf("%s: Lives: %v", name, err)
		}
		if len(lv.Lives) == 0 {
			continue
		}
		checked++

		got := map[string]*lifeTotals{}
		for _, l := range lv.Lives {
			p := got[l.Player]
			if p == nil {
				p = &lifeTotals{}
				got[l.Player] = p
			}
			p.kills += l.Kills
			p.deaths += l.Deaths
			p.suicides += l.Suicides
			p.teamKills += l.TeamKills
			p.dmgGiven += l.DamageGiven
			p.dmgTaken += l.DamageTaken
			p.dmgTeam += l.DamageGivenTeam
			p.dmgSelf += l.DamageGivenSelf
			p.shots += l.Shots
			p.hits += l.Hits
		}
		want := matchTotals(res)

		// Compare BOTH key sets. Looking up only one direction would let a
		// name mismatch — a stream named "A#1" against a frag log naming "A" —
		// skip the comparison entirely and pass while reconciling nothing.
		for _, player := range sortedKeys(got) {
			w := want[player]
			if w == nil {
				w = &lifeTotals{}
			}
			if *got[player] != *w {
				t.Errorf("%s: %s — summed across lives: %s\n%*s  match log:          %s",
					name, player, got[player], len(name)+len(player)+4, "", w)
			}
		}
		for _, player := range sortedKeys(want) {
			if _, ok := got[player]; !ok && *want[player] != (lifeTotals{}) {
				t.Errorf("%s: %s has %s in the match log but no lives at all", name, player, want[player])
			}
		}

		// The frag log's own aggregates are an INDEPENDENT oracle for the three
		// counts it publishes — they come from the analyzer, not from a
		// re-count with the rules under test.
		for player, pf := range res.Frags.ByPlayer {
			if pf == nil {
				continue
			}
			g := got[player]
			if g == nil {
				if pf.Kills != 0 || pf.Deaths != 0 {
					t.Errorf("%s: %s has %d kills / %d deaths in frags.byPlayer but no lives — identity mismatch",
						name, player, pf.Kills, pf.Deaths)
				}
				continue
			}
			if g.kills != pf.Kills || g.deaths != pf.Deaths || g.teamKills != pf.TeamKills {
				t.Errorf("%s: %s — lives kills/deaths/tk = %d/%d/%d, frags.byPlayer = %d/%d/%d",
					name, player, g.kills, g.deaths, g.teamKills, pf.Kills, pf.Deaths, pf.TeamKills)
			}
		}
	}
	if checked == 0 {
		t.Skip("no cached demo produced lives — run TestGoldenCorpus once to populate testdata/cache")
	}
	t.Logf("reconciled every per-life count against the match log on %d real demos", checked)
}

// Per-row invariants. These catch the failure the whole-match sums cannot: a
// double count in one life cancelled by a loss in another.
func TestLivesRowInvariantsOnRealDemos(t *testing.T) {
	checked, rows := 0, 0
	for _, name := range realDemos {
		res := analyzeCached(t, name)
		if res == nil {
			continue
		}
		lv, err := view.Lives(res, view.LivesOptions{})
		if err != nil || len(lv.Lives) == 0 {
			continue
		}
		checked++
		lastIndex := map[string]int{}
		prevAttrEnd := map[string]int32{}
		for _, l := range lv.Lives {
			if l.Index > lastIndex[l.Player] {
				lastIndex[l.Player] = l.Index
			}
		}
		for _, l := range lv.Lives {
			rows++
			// The attribution span is the life PLUS the dead gap after it, and
			// the spans TILE the match: consecutive windows meet exactly, so no
			// instant belongs to two lives or to none. That is the property the
			// whole-match reconciliation rests on; asserting it per row says
			// WHICH pair broke it.
			if l.AttrStart > l.Start || l.AttrEnd < l.End {
				t.Errorf("%s: %s life %d — attribution [%d,%d] does not cover the life [%d,%d]",
					name, l.Player, l.Index, l.AttrStart, l.AttrEnd, l.Start, l.End)
			}
			if l.Index > 0 {
				if prev, ok := prevAttrEnd[l.Player]; ok && prev != l.AttrStart {
					t.Errorf("%s: %s life %d starts attributing at %d but life %d stopped at %d",
						name, l.Player, l.Index, l.AttrStart, l.Index-1, prev)
				}
			} else if l.AttrStart != 0 {
				t.Errorf("%s: %s life 0 starts attributing at %d, not at MatchStart",
					name, l.Player, l.AttrStart)
			}
			prevAttrEnd[l.Player] = l.AttrEnd
			var evLocs int
			for _, e := range l.EventLocs {
				evLocs += e.Count
			}
			// Kills and eventLocs come from one predicate now. They were
			// derived separately, and a kill on a life boundary landed in one
			// life's Kills and in TWO lives' EventLocs.
			if evLocs > l.Kills {
				t.Errorf("%s: %s life %d [%d,%d] — sum(eventLocs) = %d > kills = %d",
					name, l.Player, l.Index, l.Start, l.End, evLocs, l.Kills)
			}
			var dwell int32
			for _, e := range l.Locs {
				dwell += e.Ms
			}
			if dwell > l.DurationMs {
				t.Errorf("%s: %s life %d — sum(locs.ms) = %d > durationMs = %d",
					name, l.Player, l.Index, dwell, l.DurationMs)
			}
			if l.DurationMs != l.End-l.Start {
				t.Errorf("%s: %s life %d — durationMs = %d, End-Start = %d (duration is alive time)",
					name, l.Player, l.Index, l.DurationMs, l.End-l.Start)
			}
			switch l.EndReason {
			case view.LifeEndDeath:
			case view.LifeEndMatchEnd, view.LifeEndLeftGame:
				// Only the last life can end any way but a death: a life
				// followed by another ended because the player died, and
				// clipToPresence only ever trims the outermost ends.
				if l.Index != lastIndex[l.Player] {
					t.Errorf("%s: %s life %d of %d ended %q — only a last life can",
						name, l.Player, l.Index, lastIndex[l.Player], l.EndReason)
				}
				// KilledBy is set only from an obituary at the end instant,
				// and such an obituary forces EndReason == death.
				if l.KilledBy != "" {
					t.Errorf("%s: %s life %d ended %q but names a killer %q",
						name, l.Player, l.Index, l.EndReason, l.KilledBy)
				}
			default:
				t.Errorf("%s: %s life %d has endReason %q, outside the vocabulary",
					name, l.Player, l.Index, l.EndReason)
			}
			// itemsTaken is null only when the demo carries no item timeline.
			if res.Items != nil && l.ItemsTaken == nil {
				t.Errorf("%s: %s life %d has null itemsTaken on a demo with an item timeline",
					name, l.Player, l.Index)
			}
		}
	}
	if checked == 0 {
		t.Skip("no cached demo produced lives — run TestGoldenCorpus once to populate testdata/cache")
	}
	t.Logf("checked per-row invariants on %d lives across %d demos", rows, checked)
}

// itemsTaken must partition the item timeline the same way the counts do.
func TestLivesItemsPartitionTheItemTimeline(t *testing.T) {
	checked := 0
	for _, name := range realDemos {
		res := analyzeCached(t, name)
		if res == nil || res.Items == nil {
			continue
		}
		lv, err := view.Lives(res, view.LivesOptions{})
		if err != nil || len(lv.Lives) == 0 {
			continue
		}
		checked++
		got := map[string]int{}
		for _, l := range lv.Lives {
			got[l.Player] += len(l.ItemsTaken)
		}
		want := map[string]int{}
		for _, it := range res.Items.Items {
			for _, ph := range it.Phases {
				if ph.TakenBy != "" && ph.TakenAt != 0 {
					want[ph.TakenBy]++
				}
			}
		}
		for _, player := range sortedIntKeys(got) {
			if got[player] != want[player] {
				t.Errorf("%s: %s took %d items across lives, %d in the item timeline",
					name, player, got[player], want[player])
			}
		}
	}
	if checked == 0 {
		t.Skip("no cached demo with an item timeline — run TestGoldenCorpus once to populate testdata/cache")
	}
}

// The individual incidents behind the attribution rules, pinned as themselves.
// The reconciliation above would catch each of them as a whole-player delta;
// these say WHICH rule broke, which is the difference between a five-minute and
// a two-hour diagnosis.
func TestLivesAttributionIncidents(t *testing.T) {
	t.Run("a damage event on MatchEnd belongs to the last life", func(t *testing.T) {
		// 212483: MatchEnd is 1200128; sailorman's last life ends 1200064,
		// and he lands 18 damage on KreatoR with an sng on the final
		// millisecond, plus the shot that carried it. A half-open final edge
		// dropped all three.
		res := analyzeCached(t, "212483.mvd.gz")
		if res == nil {
			t.Skip("212483 not cached")
		}
		if got := res.Streams.Global.MatchEnd; got != 1200128 {
			t.Fatalf("matchEnd = %d, want 1200128 — the fixture moved", got)
		}
		lv, err := view.Lives(res, view.LivesOptions{})
		if err != nil {
			t.Fatal(err)
		}
		last := lastLife(lv, "sailorman")
		if last == nil || last.End != 1200064 {
			t.Fatalf("sailorman's last life = %+v, want one ending at 1200064", last)
		}
		if last.DamageGiven < 18 {
			t.Errorf("last life damageGiven = %d, want at least the 18 landing at MatchEnd", last.DamageGiven)
		}
		var given, taken, shots int
		for _, l := range lv.Lives {
			switch l.Player {
			case "sailorman":
				given += l.DamageGiven
				shots += l.Shots
			case "KreatoR":
				taken += l.DamageTaken
			}
		}
		if given != 13727 || taken != 10182 || shots != 567 {
			t.Errorf("sailorman given/shots = %d/%d and KreatoR taken = %d; want 13727/567 and 10182 "+
				"(dropping the MatchEnd millisecond costs 18/1 and 18)", given, shots, taken)
		}
	})

	t.Run("deaths inside a dead gap belong to the life that ended", func(t *testing.T) {
		// 212260: nlk dies at 619017 and respawns at 623629, and KTX records
		// two `tele` suicides at 621093 and 623222 while he is already dead.
		res := analyzeCached(t, "212260.mvd.gz")
		if res == nil {
			t.Skip("212260 not cached")
		}
		lv, err := view.Lives(res, view.LivesOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var deaths, suicides int
		var gapLife *view.Life
		for i, l := range lv.Lives {
			if l.Player != "nlk" {
				continue
			}
			deaths += l.Deaths
			suicides += l.Suicides
			if l.End == 619017 {
				gapLife = &lv.Lives[i]
			}
		}
		if deaths != 52 || suicides != 3 {
			t.Errorf("nlk deaths/suicides across lives = %d/%d, want 52/3 — the two in-gap tele suicides are lost",
				deaths, suicides)
		}
		if gapLife == nil {
			t.Fatal("no nlk life ending at 619017")
		}
		if gapLife.Deaths != 3 {
			t.Errorf("the life ending at 619017 has deaths = %d, want 3 (its own, plus the two in the gap that followed) — "+
				"a life's deaths are NOT capped at 1", gapLife.Deaths)
		}
	})

	t.Run("events before the first life belong to it", func(t *testing.T) {
		// 212260: a telefrag at t=0 — doberman kills his teammate nlk, and
		// clox telefrags doberman — while doberman's first life, clipped to
		// first observation, does not start until 553.
		res := analyzeCached(t, "212260.mvd.gz")
		if res == nil {
			t.Skip("212260 not cached")
		}
		lv, err := view.Lives(res, view.LivesOptions{})
		if err != nil {
			t.Fatal(err)
		}
		var first *view.Life
		var teamKills int
		for i, l := range lv.Lives {
			if l.Player != "doberman" {
				continue
			}
			teamKills += l.TeamKills
			if l.Index == 0 {
				first = &lv.Lives[i]
			}
		}
		if first == nil || first.Start != 553 {
			t.Fatalf("doberman's first life = %+v, want one starting at 553", first)
		}
		if first.TeamKills != 1 || first.Deaths != 2 {
			t.Errorf("doberman's first life has teamKills/deaths = %d/%d, want 1/2 — "+
				"the t=0 telefrag pair lands before the life's clipped start",
				first.TeamKills, first.Deaths)
		}
		if teamKills != 3 {
			t.Errorf("doberman teamKills across lives = %d, want 3", teamKills)
		}
	})

	t.Run("a kill at the next spawn instant is counted once", func(t *testing.T) {
		// 212260: lakso's life 21 runs [342875,348052] and the next begins at
		// 348808 with a kill exactly there. It belongs to the life that had
		// begun; eventLocs used to claim it for the one that ended as well.
		res := analyzeCached(t, "212260.mvd.gz")
		if res == nil {
			t.Skip("212260 not cached")
		}
		lv, err := view.Lives(res, view.LivesOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range lv.Lives {
			if l.Player != "lakso" || l.Start != 342875 {
				continue
			}
			var evLocs int
			for _, e := range l.EventLocs {
				evLocs += e.Count
			}
			if l.Kills != 1 || evLocs != 1 {
				t.Errorf("lakso life [%d,%d]: kills = %d, sum(eventLocs) = %d; want 1 and 1",
					l.Start, l.End, l.Kills, evLocs)
			}
			return
		}
		t.Fatal("no lakso life starting at 342875")
	})
}

func lastLife(lv *view.LivesView, player string) *view.Life {
	var out *view.Life
	for i := range lv.Lives {
		if lv.Lives[i].Player == player && (out == nil || lv.Lives[i].Index > out.Index) {
			out = &lv.Lives[i]
		}
	}
	return out
}

func sortedKeys(m map[string]*lifeTotals) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedIntKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
