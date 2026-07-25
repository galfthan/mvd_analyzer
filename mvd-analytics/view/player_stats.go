package view

import (
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// PlayerStatsOptions narrows the canonical statistics section. Empty
// fields mean "no filter".
type PlayerStatsOptions struct {
	Players []string // scope to these players (case-sensitive); team rows are dropped when set
	Teams   []string // scope to these teams (case-sensitive)
}

// PlayerStats returns the canonical per-player statistics with the KTX
// overlay applied.
//
// The analyzer stores the fully DERIVED section, so the golden corpus
// always records what this pipeline computed. This is where the KTX
// demoinfo block is folded in for the families where KTX is the better
// source — the same read-time-overlay shape view.Damage uses for the KTX
// bounded summary (applyKTXBoundedSummary), and for the same reason: the
// stored artifact stays honest and the merge stays in one auditable place.
//
// The rule is that KTX wins ONLY where the definition is identical. Where
// the two disagree on meaning, both are surfaced under distinct names and
// neither is coerced — see applyKTXOverlay for the family-by-family table.
//
// A missing KTX demoinfo block is NEVER a reason to fail — the section is
// computed for every demo, and the families KTX would have supplied simply
// stay derived or absent. ErrUnavailable means something else entirely: a
// parse so degraded it produced no player streams, so there was nothing to
// build a row from. Callers map that to 422 exactly as they do for the
// other section accessors.
func PlayerStats(r *result.Result, opts PlayerStatsOptions) (*result.PlayerStatsResult, error) {
	if r == nil || r.PlayerStats == nil {
		return nil, ErrUnavailable
	}
	out := applyKTXOverlay(r)

	if len(opts.Players) > 0 || len(opts.Teams) > 0 {
		players := toSet(opts.Players)
		teams := toSet(opts.Teams)
		filtered := &result.PlayerStatsResult{Sources: out.Sources}
		for i := range out.Players {
			row := &out.Players[i]
			if len(players) > 0 && !players[row.Name] {
				continue
			}
			if len(teams) > 0 && !teams[row.Team] {
				continue
			}
			filtered.Players = append(filtered.Players, *row)
		}
		// A players filter asks about individuals; carrying whole-team
		// aggregates alongside a subset of their members would invite
		// reading them as the filtered group's totals, which they are not.
		if len(players) == 0 {
			for i := range out.Teams {
				if len(teams) > 0 && !teams[out.Teams[i].Name] {
					continue
				}
				filtered.Teams = append(filtered.Teams, out.Teams[i])
			}
		}
		out = filtered
	}
	return out, nil
}

// ktxItemKind maps a KTX demoinfo item key (ItName, ktx/src/stats.c:395)
// onto this repo's item-kind vocabulary. KTX's weapon keys (WpName,
// ktx/src/stats.c:358) already match ours, so they need no mapping.
var ktxItemKind = map[string]string{
	"health_15":  "h15",
	"health_25":  "h25",
	"health_100": "mh",
	"ga":         "ga",
	"ya":         "ya",
	"ra":         "ra",
	"q":          "quad",
	"p":          "pent",
	"r":          "ring",
}

// applyKTXOverlay returns a copy of the stored section with the KTX
// demoinfo numbers folded into the families KTX counts better, leaving
// the rest derived. It never mutates the stored Result: every row it
// touches is copied first, since the API serves a shared cached Result.
//
// Family-by-family:
//
//   - score: NEVER overlaid. KTX over-counts pentagram-deflect telefrags
//     (dtTELE2), credits world-dealt suicides to the world entity
//     (ktx/src/client.c:5132), and resets after a reconnect.
//   - damage: given / givenTeam / givenSelf / enemyWeapons come from KTX;
//     takenEnemy and takenToDie are KTX-only fields with no derived
//     equivalent. `taken` STAYS DERIVED — KTX's dmg.taken is enemy-only
//     (ktx/src/combat.c:1083) while ours counts every source, so folding
//     one into the other would silently change what the field means.
//   - accuracy: KTX-only. No derived fallback by design.
//   - pickups: took / totalTook / dropped from KTX; xfer / xferSelf stay
//     derived (KTX conflates the two). Ammo kinds have no KTX counterpart
//     and stay derived — so `src: "ktx"` on this family means "KTX
//     wherever KTX carries the kind".
//   - hold: NEVER overlaid. KTX has no weapon hold time in the block, and
//     its armor hold time overcounts (see result.PlayerStatsResult).
//   - identity (ping / handicap / login / bot): KTX-only passthrough.
func applyKTXOverlay(r *result.Result) *result.PlayerStatsResult {
	stored := r.PlayerStats
	out := &result.PlayerStatsResult{
		Players: append([]result.PlayerStatsRow(nil), stored.Players...),
		Teams:   append([]result.PlayerStatsRow(nil), stored.Teams...),
		Sources: stored.Sources,
	}
	if r.DemoInfo == nil || len(r.DemoInfo.Players) == 0 {
		return out
	}

	ktx := make(map[string]*result.DemoInfoPlayer, len(r.DemoInfo.Players))
	for i := range r.DemoInfo.Players {
		p := &r.DemoInfo.Players[i]
		ktx[p.Name] = p
	}

	var anyDamage, anyAccuracy, anyPickups bool
	for i := range out.Players {
		row := &out.Players[i]
		di := ktx[row.Name]
		if di == nil {
			continue
		}
		row.Ping, row.Handicap, row.Login, row.Bot = di.Ping, di.Handicap, di.Login, di.Bot

		if d := overlayDamage(row.Damage, di); d != nil {
			row.Damage = d
			anyDamage = true
		}
		if a := overlayAccuracy(di); a != nil {
			row.Accuracy = a
			anyAccuracy = true
		}
		if p := overlayPickups(row.Pickups, di); p != nil {
			row.Pickups = p
			anyPickups = true
		}
	}

	if anyDamage {
		out.Sources.Damage = result.SrcKTX
	}
	if anyAccuracy {
		out.Sources.Accuracy = result.SrcKTX
	}
	if anyPickups {
		out.Sources.Pickups = result.SrcKTX
	}
	// Team rows are sums of the per-player rows, so re-derive them from
	// the overlaid players rather than leaving stale derived totals beside
	// KTX-sourced member rows.
	if (anyDamage || anyPickups) && len(out.Teams) > 0 {
		out.Teams = reaggregateTeams(out.Players, out.Teams)
	}
	return out
}

// overlayDamage folds KTX's damage counters over the reconstruction.
// Returns nil when KTX carries no damage block for this player (so the
// derived row stands).
func overlayDamage(derived *result.PlayerStatsDamage, di *result.DemoInfoPlayer) *result.PlayerStatsDamage {
	if di.Dmg == nil {
		return nil
	}
	out := result.PlayerStatsDamage{Src: result.SrcKTX}
	if derived != nil {
		// Taken keeps the derived all-sources value: KTX's is enemy-only.
		out.Taken = derived.Taken
	}
	out.Given = di.Dmg.Given
	out.GivenTeam = di.Dmg.Team
	out.GivenSelf = di.Dmg.Self
	out.EnemyWeapons = di.Dmg.EnemyWeapons
	takenEnemy := di.Dmg.Taken
	out.TakenEnemy = &takenEnemy
	if di.Dmg.TakenToDie != 0 {
		ttd := di.Dmg.TakenToDie
		out.TakenToDie = &ttd
	}
	return &out
}

// overlayAccuracy lifts KTX's per-weapon acc blocks. Returns nil when the
// player has no weapon with accuracy recorded — a spectator slot, or a
// player who never fired.
func overlayAccuracy(di *result.DemoInfoPlayer) *result.PlayerStatsAccuracy {
	if len(di.Weapons) == 0 {
		return nil
	}
	byWeapon := map[string]result.DemoInfoAcc{}
	for w, wv := range di.Weapons {
		if wv == nil || wv.Acc == nil {
			continue
		}
		byWeapon[w] = *wv.Acc
	}
	if len(byWeapon) == 0 {
		return nil
	}
	return &result.PlayerStatsAccuracy{Src: result.SrcKTX, ByWeapon: byWeapon}
}

// overlayPickups folds KTX's pickup counters over the derived tallies,
// keeping the derived transfer decomposition (KTX conflates xfer and
// xferSelf) and the derived ammo kinds (KTX tracks none).
func overlayPickups(derived *result.PlayerStatsPickups, di *result.DemoInfoPlayer) *result.PlayerStatsPickups {
	if len(di.Items) == 0 && len(di.Weapons) == 0 {
		return nil
	}
	byKind := map[string]result.PlayerStatsPickup{}
	if derived != nil {
		for kind, e := range derived.ByKind {
			byKind[kind] = e
		}
	}

	for k, item := range di.Items {
		if item == nil {
			continue
		}
		kind, ok := ktxItemKind[k]
		if !ok {
			continue // an item kind this KTX build names differently
		}
		e := byKind[kind]
		e.Took = item.Took
		byKind[kind] = e
	}

	for w, wv := range di.Weapons {
		if wv == nil || wv.Pickups == nil {
			continue
		}
		e := byKind[w]
		e.Took = wv.Pickups.Taken
		e.TotalTook = wv.Pickups.TotalTaken
		e.Dropped = wv.Pickups.Dropped
		byKind[w] = e
	}

	return &result.PlayerStatsPickups{Src: result.SrcKTX, ByKind: byKind}
}

// reaggregateTeams re-sums the team rows from overlaid player rows,
// preserving each team's window and hold figures (which the overlay never
// touches) and replacing only the summed damage and pickups.
func reaggregateTeams(players, teams []result.PlayerStatsRow) []result.PlayerStatsRow {
	out := append([]result.PlayerStatsRow(nil), teams...)
	for i := range out {
		team := &out[i]
		var dmg *result.PlayerStatsDamage
		var pickups map[string]result.PlayerStatsPickup
		src := result.SrcDerived

		for j := range players {
			p := &players[j]
			if p.Team != team.Name {
				continue
			}
			if p.Damage != nil {
				if dmg == nil {
					dmg = &result.PlayerStatsDamage{Src: p.Damage.Src}
				}
				if p.Damage.Src == result.SrcKTX {
					dmg.Src = result.SrcKTX
				}
				dmg.Given += p.Damage.Given
				dmg.GivenTeam += p.Damage.GivenTeam
				dmg.GivenSelf += p.Damage.GivenSelf
				dmg.EnemyWeapons += p.Damage.EnemyWeapons
				dmg.Taken += p.Damage.Taken
				if p.Damage.TakenEnemy != nil {
					v := *p.Damage.TakenEnemy
					if dmg.TakenEnemy == nil {
						dmg.TakenEnemy = &v
					} else {
						*dmg.TakenEnemy += v
					}
				}
				// TakenToDie is an average; averaging averages across
				// players with different death counts is meaningless, so a
				// team row deliberately carries none.
			}
			if p.Pickups != nil {
				if pickups == nil {
					pickups = map[string]result.PlayerStatsPickup{}
				}
				if p.Pickups.Src == result.SrcKTX {
					src = result.SrcKTX
				}
				for kind, e := range p.Pickups.ByKind {
					agg := pickups[kind]
					agg.Took += e.Took
					agg.TotalTook += e.TotalTook
					agg.Dropped += e.Dropped
					if e.Xfer != nil {
						v := agg.Xfer
						if v == nil {
							n := 0
							v = &n
						}
						*v += *e.Xfer
						agg.Xfer = v
					}
					if e.XferSelf != nil {
						v := agg.XferSelf
						if v == nil {
							n := 0
							v = &n
						}
						*v += *e.XferSelf
						agg.XferSelf = v
					}
					pickups[kind] = agg
				}
			}
		}
		team.Damage = dmg
		if pickups != nil {
			team.Pickups = &result.PlayerStatsPickups{Src: src, ByKind: pickups}
		}
	}
	return out
}
