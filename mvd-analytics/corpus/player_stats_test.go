package corpus

import (
	"encoding/json"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-analytics/view"
)

// The playerStats invariants. Every one of them is a "was this measured?"
// question — the family of defect this section keeps producing is a value
// served with full confidence where nothing was measured, so the checks
// below all pair a served number against the evidence it claims to rest
// on.
//
// They run against the OVERLAID section (view.PlayerStats), not the stored
// one: the KTX merge happens at read time, so the stored artifact carries
// none of the provenance a consumer actually sees.

// checkPlayerStatsHoldHasStream — a hold family says the player held an
// item for a measured span, which is only knowable from the stream that
// records the item's possession. A hold key over an EMPTY stream is a
// figure computed from nothing: on a POV recording only the recorder has
// stat streams, so every other player's armor stream is empty and an
// unguarded alive-time complement reports them as running the whole match
// with no armor while the same row lists their armor pickups.
func checkPlayerStatsHoldHasStream(t *testing.T, res *result.Result) {
	t.Helper()
	if res.PlayerStats == nil {
		return
	}
	streams := map[string]*result.PlayerStream{}
	for i := range res.Streams.Players {
		ps := &res.Streams.Players[i]
		streams[ps.Name] = ps
		streams[stripSlotSuffix(ps.Name)] = ps
	}
	for i := range res.PlayerStats.Players {
		row := &res.PlayerStats.Players[i]
		ps := streams[row.Name]
		if ps == nil {
			// A scoreboard-only row has no stream at all and must therefore
			// carry no hold family; anything else is a figure with no source.
			if len(row.Hold.Weapons)+len(row.Hold.Armor)+len(row.Hold.Powerups) > 0 {
				t.Errorf("%q has no stream but carries hold keys: %+v", row.Name, row.Hold)
			}
			continue
		}
		weaponStreams := map[string][]result.Interval{
			"rl": ps.RL, "lg": ps.LG, "gl": ps.GL, "ssg": ps.SSG, "sng": ps.SNG,
		}
		for kind := range row.Hold.Weapons {
			if len(weaponStreams[kind]) == 0 {
				t.Errorf("%q carries hold.weapons.%s but the %s possession stream is empty", row.Name, kind, kind)
			}
		}
		powerupStreams := map[string][]result.Interval{
			"quad": ps.Quad, "pent": ps.Pent, "ring": ps.Ring,
		}
		for kind := range row.Hold.Powerups {
			if len(powerupStreams[kind]) == 0 {
				t.Errorf("%q carries hold.powerups.%s but the %s possession stream is empty", row.Name, kind, kind)
			}
		}
		if len(row.Hold.Armor) > 0 && len(ps.ArmorType) == 0 {
			// appendChangeStr unconditionally appends the first sample
			// (timeline_streams.go), so a player who genuinely never held
			// armor still has one — an empty stream means the armor state
			// was never observed at all, not that it was observed empty.
			t.Errorf("%q carries hold.armor %v but the armor stream is empty", row.Name, keysOf(row.Hold.Armor))
		}
	}
}

func keysOf(m map[string]result.HoldStat) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// checkPlayerStatsKillsHaveFragLog — the kill side of `score` (kills,
// suicides, teamKills, byWeapon, efficiency) is attributed from the
// obituary-derived frag log. An empty frag log with deaths on the board
// means kill attribution was never measured, and serving a 0 there is
// byte-indistinguishable from a genuinely killless match: on
// 4on4_l_vs_la[e1m2] a full 4v4 scoreboard with 230 team frags reports
// 0 kills and 0.0% efficiency for all eight players.
//
// Presence is read off the SERIALIZED form, since that is what the
// invariant is about — what the response asserts, not what the Go zero
// value happens to be.
func checkPlayerStatsKillsHaveFragLog(t *testing.T, res *result.Result) {
	t.Helper()
	if res.PlayerStats == nil || res.Frags == nil || len(res.Frags.Frags) > 0 {
		return
	}
	rows := append(append([]result.PlayerStatsRow(nil), res.PlayerStats.Players...), res.PlayerStats.Teams...)
	for i := range rows {
		row := &rows[i]
		if row.Score.Deaths == 0 {
			continue // nothing to contradict: no deaths, no kills to attribute
		}
		for _, key := range []string{"kills", "suicides", "teamKills", "byWeapon", "efficiency"} {
			if jsonHasKey(t, row.Score, key) {
				t.Errorf("%q serves score.%s though the frag log is empty (deaths=%d): kill attribution was never measured",
					row.Name, key, row.Score.Deaths)
			}
		}
	}
}

// jsonHasKey reports whether v serializes with the given top-level key.
func jsonHasKey(t *testing.T, v any, key string) bool {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %T: %v", v, err)
	}
	_, ok := m[key]
	return ok
}

// checkPlayerStatsTeamFamilies — a team row is the sum of its members, so
// every family a member carries must appear on it. A family that silently
// stops aggregating renders as an empty column beside per-player rows that
// have real numbers, which reads as "the team did none of this" rather
// than "we did not add it up".
//
// Runs on the OVERLAID rows: accuracy in particular is overlaid per player
// at read time, so an analyzer-only aggregate would satisfy this on a
// pre-KTX demo and fail on every modern one.
func checkPlayerStatsTeamFamilies(t *testing.T, ps *result.PlayerStatsResult) {
	t.Helper()
	if ps == nil || len(ps.Teams) == 0 {
		return
	}
	for i := range ps.Teams {
		team := &ps.Teams[i]
		for j := range ps.Players {
			m := &ps.Players[j]
			if m.Team != team.Name {
				continue
			}
			if m.Damage != nil && team.Damage == nil {
				t.Errorf("team %q has no damage family though its member %q does", team.Name, m.Name)
			}
			if m.Accuracy != nil && team.Accuracy == nil {
				t.Errorf("team %q has no accuracy family though its member %q does", team.Name, m.Name)
			}
			if m.Pickups != nil && team.Pickups == nil {
				t.Errorf("team %q has no pickups family though its member %q does", team.Name, m.Name)
			}
			for kind := range m.Hold.Weapons {
				if _, ok := team.Hold.Weapons[kind]; !ok {
					t.Errorf("team %q has no hold.weapons.%s though its member %q does", team.Name, kind, m.Name)
				}
			}
			for kind := range m.Hold.Powerups {
				if _, ok := team.Hold.Powerups[kind]; !ok {
					t.Errorf("team %q has no hold.powerups.%s though its member %q does", team.Name, kind, m.Name)
				}
			}
		}
	}
}

// checkPlayerStatsKTXJoin is the phantom-roster canary.
//
// Measured across every local demo carrying a KTX demoinfo block: the
// playerStats name set and the demoinfo name set are identical, and every
// player in the block carries both a dmg blob and at least one weapon with
// an acc entry. So a roster row that does NOT join the block is not a data
// condition — it is a player the block has never heard of, i.e. the
// refused-connection / phantom-row defect returning.
//
// The consequence is visible one level up: the unjoined row keeps
// `src: "derived"` beside genuine `"ktx"` rows, which is the only way the
// `sources` roll-up can read `"mixed"`.
func checkPlayerStatsKTXJoin(t *testing.T, res *result.Result, ps *result.PlayerStatsResult) {
	t.Helper()
	if ps == nil || res.DemoInfo == nil || len(res.DemoInfo.Players) == 0 {
		return
	}
	known := map[string]bool{}
	for i := range res.DemoInfo.Players {
		known[res.DemoInfo.Players[i].Name] = true
	}
	for i := range ps.Players {
		if !known[ps.Players[i].Name] {
			t.Errorf("playerStats row %q has no demoinfo entry — a phantom roster row", ps.Players[i].Name)
		}
	}
	// Per-family src uniformity. A demo either has the block for everyone
	// or for no one; a split means a row failed to join.
	srcOf := func(row *result.PlayerStatsRow) (string, string, string) {
		var d, a, p string
		if row.Damage != nil {
			d = row.Damage.Src
		}
		if row.Accuracy != nil {
			a = row.Accuracy.Src
		}
		if row.Pickups != nil {
			p = row.Pickups.Src
		}
		return d, a, p
	}
	seen := map[string]map[string]string{"damage": {}, "accuracy": {}, "pickups": {}}
	for i := range ps.Players {
		row := &ps.Players[i]
		d, a, p := srcOf(row)
		for family, src := range map[string]string{"damage": d, "accuracy": a, "pickups": p} {
			if src == "" {
				continue
			}
			seen[family][src] = row.Name
		}
	}
	for family, srcs := range seen {
		if len(srcs) > 1 {
			t.Errorf("%s src disagrees across rows on a demo with a KTX block: %v", family, srcs)
		}
	}
}

// playerStatsChecks runs the whole family against one analysed demo. The
// overlay is applied once and shared, since it is what a consumer reads.
func playerStatsChecks(t *testing.T, res *result.Result) {
	t.Helper()
	checkPlayerStatsHoldHasStream(t, res)
	checkPlayerStatsKillsHaveFragLog(t, res)
	if res.PlayerStats == nil {
		return
	}
	ps, err := view.PlayerStats(res, view.PlayerStatsOptions{})
	if err != nil {
		t.Fatalf("view.PlayerStats: %v", err)
	}
	checkPlayerStatsTeamFamilies(t, ps)
	checkPlayerStatsKTXJoin(t, res, ps)
}
