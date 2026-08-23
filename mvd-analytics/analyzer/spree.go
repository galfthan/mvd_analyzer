package analyzer

import (
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Kill-streak derivation (schema v74) — the derived equivalent of the KTX
// demoinfo block's `spree` object, which 54% of the archive does not carry.
//
// KTX keeps two live counters per player and latches their maxima
// (ktx/src/client.c:4864-4876, the spree block of StatsHandler):
//
//	spree_current   ++ on every kill the attacker is credited with
//	spree_current_q ++ on those made while super_damage_finished > 0
//	both latch into spree_max / spree_max_q and reset when the player DIES
//	spree_max_q additionally latches and resets on a QUAD PICKUP
//	           (ktx/src/items.c:2180-2181)
//	both latch once more at match end (ktx/src/stats.c:1637-1638)
//
// This reproduces that state machine off the artifacts every demo carries —
// the frag log for the increments, the death markers and the Quad possession
// runs for the resets — with ONE deliberate divergence, documented on
// result.PlayerStatsScore.MaxSpree: KTX's increment gate is
// `strneq(attackerteam, targteam) || !tp_num()`, so on a server with teamplay
// off (every duel, every FFA) a player's own SUICIDE increments their spree
// before the very same call latches it — attacker and target are the same
// edict. A self-rocket therefore reads as a frag on the streak. We increment
// on exactly the kills PlayerStatsScore.Kills counts (enemy kills, named
// killer), which is the corrected figure this whole family is built to serve,
// and cmd/qw-demoinfo-eval scores both conventions against the wire so the
// residual stays a measured quantity rather than an assertion.

// spreeCounts is one player's latched streak maxima.
type spreeCounts struct {
	max     int
	maxQuad int
}

// spreeEventKind orders the three event classes within one millisecond.
// The ranks are the state machine's, not arbitrary:
//
//   - a quad PICKUP closes the quad streak that preceded it, so it must be
//     applied before a kill made at the same instant — that kill belongs to
//     the new quad run (KTX resets spree_current_q inside the touch handler,
//     which runs before the obituary of anyone killed in the same frame);
//   - a KILL lands before the killer's own death at the same instant: the
//     rocket that was already in flight counts, which is also KTX's order
//     (ClientObituary increments the attacker, then latches the target).
type spreeEventKind int

const (
	spreeQuadTaken spreeEventKind = iota
	spreeKill
	spreeDeath
)

type spreeEvent struct {
	t      int32
	kind   spreeEventKind
	player string
	// quad is set on a kill the killer made while holding the quad.
	quad bool
}

// deriveSprees replays the streak state machine for every player.
//
// Returns nil when there is no frag log to count kills from — the same
// evidence PlayerStatsScore.Kills rests on, so the two are absent together
// (the caller additionally gates on killsMeasured).
func deriveSprees(res *Result) map[string]*spreeCounts {
	if res.Frags == nil {
		return nil
	}
	streams := map[string]*result.PlayerStream{}
	if res.Streams != nil {
		for i := range res.Streams.Players {
			p := &res.Streams.Players[i]
			streams[p.Name] = p
		}
	}

	var evs []spreeEvent
	// Increments: exactly the entries PlayerFrags.Kills counts — enemy kills
	// with a named killer (the predicate deriveEnemyWeaponKills applies, and
	// for the same reason: a streak that counted kills the scoreboard does
	// not would not be a re-cut of Kills but a second, disagreeing tally).
	for i := range res.Frags.Frags {
		f := &res.Frags.Frags[i]
		if f.IsSuicide || f.IsTeamKill || isGenericPlayer(f.Killer) {
			continue
		}
		quad := false
		if k := streams[f.Killer]; k != nil {
			quad = heldAt(k.Quad, f.Time)
		}
		evs = append(evs, spreeEvent{t: f.Time, kind: spreeKill, player: f.Killer, quad: quad})
	}

	// Resets. Two death sources, unioned rather than chosen between, because
	// each covers what the other misses: PlayerStream.Deaths is the protocol
	// death-event fusion and is complete for anyone who streamed, while the
	// frag log's victim side is the only death record a SCOREBOARD-ONLY
	// player (no stream at all) has. A death seen twice a frame apart is
	// harmless — the second latch finds a counter already at zero.
	for name, p := range streams {
		for _, t := range p.Deaths {
			evs = append(evs, spreeEvent{t: t, kind: spreeDeath, player: name})
		}
		// A quad pickup is read as the START of a possession run rather than
		// from the item timeline: the run is derived from the player's own
		// items bitfield, which is broadcast for every player on every demo,
		// whereas an item phase needs the spawner to have been observed.
		for _, iv := range p.Quad {
			evs = append(evs, spreeEvent{t: iv.Start, kind: spreeQuadTaken, player: name})
		}
	}
	for i := range res.Frags.Frags {
		f := &res.Frags.Frags[i]
		if isGenericPlayer(f.Victim) {
			continue
		}
		evs = append(evs, spreeEvent{t: f.Time, kind: spreeDeath, player: f.Victim})
	}

	sort.SliceStable(evs, func(i, j int) bool {
		if evs[i].t != evs[j].t {
			return evs[i].t < evs[j].t
		}
		return evs[i].kind < evs[j].kind
	})

	out := map[string]*spreeCounts{}
	cur := map[string]*spreeCounts{}
	get := func(name string) (*spreeCounts, *spreeCounts) {
		c := cur[name]
		if c == nil {
			c, out[name] = &spreeCounts{}, &spreeCounts{}
			cur[name] = c
		}
		return c, out[name]
	}
	for _, e := range evs {
		c, m := get(e.player)
		switch e.kind {
		case spreeKill:
			c.max++
			if e.quad {
				c.maxQuad++
			}
		case spreeQuadTaken:
			if c.maxQuad > m.maxQuad {
				m.maxQuad = c.maxQuad
			}
			c.maxQuad = 0
		case spreeDeath:
			if c.max > m.max {
				m.max = c.max
			}
			if c.maxQuad > m.maxQuad {
				m.maxQuad = c.maxQuad
			}
			c.max, c.maxQuad = 0, 0
		}
	}
	// The match-end latch. Without it a player who ends the match on a live
	// streak — the winning run, the one anybody would want to read — reports
	// whatever they had before their last death. KTX latches here too.
	for name, c := range cur {
		m := out[name]
		if c.max > m.max {
			m.max = c.max
		}
		if c.maxQuad > m.maxQuad {
			m.maxQuad = c.maxQuad
		}
	}
	return out
}
