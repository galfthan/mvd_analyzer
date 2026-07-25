package analyzer_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Cross-artifact invariants for the playerStats section, checked against
// the COMMITTED golden corpus rather than synthetic fixtures.
//
// The unit tests in player_stats_test.go pin the algebra on hand-built
// streams; these pin the properties that only real demos can falsify —
// that the section agrees with the artifacts it was built from, and that
// the transfer decomposition reproduces KTX's own total. They read the
// goldens straight off disk, so they run offline and need no BSP corpus
// or demo cache.
func loadGoldens(t *testing.T) map[string]*result.Result {
	t.Helper()
	dir := filepath.Join("..", "testdata", "golden")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("no golden corpus at %s: %v", dir, err)
	}
	out := map[string]*result.Result{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		var r result.Result
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("unmarshal %s: %v", e.Name(), err)
		}
		out[e.Name()] = &r
	}
	if len(out) == 0 {
		t.Skip("golden corpus is empty")
	}
	return out
}

// TestCorpusTransferIdentity is the load-bearing check for the transfer
// decomposition: KTX's xferRL / xferLG conflate "a teammate took my pack"
// with "I took my own pack back" (no other != dropper check,
// ktx/src/items.c:2586-2615). We split them, so xfer + xferSelf must
// reproduce KTX's number exactly, per player, per weapon. If it ever
// drifts, either our backpack→pickup join or the teamplay gate is wrong.
func TestCorpusTransferIdentity(t *testing.T) {
	checked := 0
	for name, r := range loadGoldens(t) {
		if r.PlayerStats == nil || r.DemoInfo == nil {
			continue
		}
		ktx := map[string]*result.DemoInfoPlayer{}
		for i := range r.DemoInfo.Players {
			ktx[r.DemoInfo.Players[i].Name] = &r.DemoInfo.Players[i]
		}
		for _, row := range r.PlayerStats.Players {
			di := ktx[row.Name]
			if di == nil || row.Pickups == nil {
				continue
			}
			for weapon, want := range map[string]int{"rl": di.XferRL, "lg": di.XferLG} {
				e := row.Pickups.ByKind[weapon]
				got := deref(e.Xfer) + deref(e.XferSelf)
				if e.Xfer == nil && e.XferSelf == nil {
					// Unobservable on this demo (no hints / not teamplay);
					// KTX must agree there was nothing to see.
					if want != 0 {
						t.Errorf("%s %s %s: transfers unobservable but KTX counted %d", name, row.Name, weapon, want)
					}
					continue
				}
				if got != want {
					t.Errorf("%s %s %s: xfer+xferSelf = %d, KTX = %d", name, row.Name, weapon, got, want)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Error("no player/weapon pair exercised the transfer identity — the corpus lost its teamplay demos")
	}
}

// TestCorpusWindowOrdering pins the denominator hierarchy every share in
// the section is divided by. A hold figure larger than alive time, or
// alive time larger than the match, would silently produce shares above 1.
func TestCorpusWindowOrdering(t *testing.T) {
	for name, r := range loadGoldens(t) {
		if r.PlayerStats == nil {
			continue
		}
		for _, row := range r.PlayerStats.Players {
			w := row.Window
			if w.AliveMs > w.PresentMs || w.PresentMs > w.MatchMs {
				t.Errorf("%s %s: window out of order: alive=%d present=%d match=%d",
					name, row.Name, w.AliveMs, w.PresentMs, w.MatchMs)
			}
			if w.DeadMs != w.PresentMs-w.AliveMs {
				t.Errorf("%s %s: deadMs = %d, want present-alive = %d",
					name, row.Name, w.DeadMs, w.PresentMs-w.AliveMs)
			}
			for family, m := range map[string]map[string]result.HoldStat{
				"weapons": row.Hold.Weapons, "armor": row.Hold.Armor, "powerups": row.Hold.Powerups,
			} {
				for kind, st := range m {
					if st.Ms > w.AliveMs {
						t.Errorf("%s %s: hold.%s.%s = %d ms > aliveMs %d", name, row.Name, family, kind, st.Ms, w.AliveMs)
					}
					if st.LongestMs > st.Ms {
						t.Errorf("%s %s: hold.%s.%s longest %d > total %d", name, row.Name, family, kind, st.LongestMs, st.Ms)
					}
					if st.Ms > 0 && st.Runs == 0 {
						t.Errorf("%s %s: hold.%s.%s has %d ms in 0 runs", name, row.Name, family, kind, st.Ms)
					}
				}
			}
		}
	}
}

// TestCorpusArmorComplement pins the identity that makes `armor.none`
// meaningful: every alive millisecond is spent under exactly one of
// ga / ya / ra / none. KTX cannot produce this stat at all, so nothing
// external cross-checks it — this is its only guard.
func TestCorpusArmorComplement(t *testing.T) {
	checked := 0
	for name, r := range loadGoldens(t) {
		if r.PlayerStats == nil {
			continue
		}
		for _, row := range r.PlayerStats.Players {
			if row.Hold.Armor == nil {
				continue
			}
			var sum int32
			for _, kind := range []string{"ga", "ya", "ra", "none"} {
				sum += row.Hold.Armor[kind].Ms
			}
			if sum != row.Window.AliveMs {
				t.Errorf("%s %s: ga+ya+ra+none = %d, want aliveMs = %d", name, row.Name, sum, row.Window.AliveMs)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Error("no player exercised the armor complement")
	}
}

// TestCorpusScoreMatchesScoreboard pins that the score family really is
// the corrected scoreboard rather than a parallel count — the reason it is
// never overlaid with KTX's.
func TestCorpusScoreMatchesScoreboard(t *testing.T) {
	for name, r := range loadGoldens(t) {
		if r.PlayerStats == nil || r.Match == nil {
			continue
		}
		want := map[string]result.PlayerStat{}
		for _, p := range r.Match.Players {
			want[p.Name] = p
		}
		for _, row := range r.PlayerStats.Players {
			mp, ok := want[row.Name]
			if !ok {
				continue // streamed player with no scoreboard row
			}
			if row.Score.Frags != mp.Frags || row.Score.Kills != mp.Kills ||
				row.Score.Deaths != mp.Deaths || row.Score.Suicides != mp.Suicides {
				t.Errorf("%s %s: score %+v does not match match.players %+v", name, row.Name, row.Score, mp)
			}
		}
	}
}

// TestCorpusPowerupHoldMatchesTimeline cross-checks the derived powerup
// hold against the timeline's own powerup runs — two independent
// derivations off the same streams. They are not required to be identical
// (the timeline records a run's full nominal duration from pickup, while
// hold integrates possession intersected with alive time, so a player who
// dies holding quad loses the tail), but hold must never EXCEED the
// timeline's total.
func TestCorpusPowerupHoldMatchesTimeline(t *testing.T) {
	checked := 0
	for name, r := range loadGoldens(t) {
		if r.PlayerStats == nil || r.TimelineAnalysis == nil {
			continue
		}
		timeline := map[string]map[string]int32{}
		for _, ev := range r.TimelineAnalysis.PowerupEvents {
			if timeline[ev.PlayerName] == nil {
				timeline[ev.PlayerName] = map[string]int32{}
			}
			timeline[ev.PlayerName][ev.PowerupType] += ev.Duration
		}
		for _, row := range r.PlayerStats.Players {
			for kind, st := range row.Hold.Powerups {
				total := timeline[row.Name][kind]
				if total == 0 {
					t.Errorf("%s %s: hold.powerups.%s = %d ms but the timeline has no such run",
						name, row.Name, kind, st.Ms)
					continue
				}
				if st.Ms > total {
					t.Errorf("%s %s: hold.powerups.%s = %d ms exceeds the timeline's %d ms",
						name, row.Name, kind, st.Ms, total)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Error("no powerup hold figure was cross-checked against the timeline")
	}
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
