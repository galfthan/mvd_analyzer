package view

import (
	"math"

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
		// Empty slices, not nil: a filter that matches nothing returns
		// `"players": []`, never `null`. The schema declares the key
		// required and array-typed, and a null there breaks a caller that
		// ranges over it.
		filtered := &result.PlayerStatsResult{
			Players: []result.PlayerStatsRow{},
		}
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
	// AFTER filtering: the roll-up describes the rows this response
	// carries, so it must be computed over exactly those. Copying the
	// unfiltered roll-up made it describe rows that had been removed.
	out.Sources = rollUpSources(out.Players)
	return out, nil
}

// rollUpSources recomputes the per-family provenance from the rows.
//
// score and hold are never overlaid, so they are constants. The other
// three are read off the rows themselves: all agree -> that value, none
// carries the family -> omitted, they disagree -> result.SrcMixed, which
// is a canary for the phantom-roster defect rather than a data condition
// (see result.SrcMixed).
//
// Team rows are deliberately NOT consulted: they are sums of these very
// rows and carry no independent provenance, and on a players filter they
// are dropped entirely.
func rollUpSources(rows []result.PlayerStatsRow) result.PlayerStatsSources {
	return result.PlayerStatsSources{
		Score: result.SrcDerived,
		Hold:  result.SrcDerived,
		Damage: foldSrc(rows, func(r *result.PlayerStatsRow) string {
			if r.Damage == nil {
				return ""
			}
			return r.Damage.Src
		}),
		Accuracy: foldSrc(rows, func(r *result.PlayerStatsRow) string {
			if r.Accuracy == nil {
				return ""
			}
			return r.Accuracy.Src
		}),
		Pickups: foldSrc(rows, func(r *result.PlayerStatsRow) string {
			if r.Pickups == nil {
				return ""
			}
			return r.Pickups.Src
		}),
	}
}

// foldSrc reduces one family's per-row Src to a single value. Rows without
// the family contribute nothing — an absent family is not a third opinion.
func foldSrc(rows []result.PlayerStatsRow, get func(*result.PlayerStatsRow) string) string {
	seen := ""
	for i := range rows {
		src := get(&rows[i])
		if src == "" {
			continue
		}
		if seen == "" {
			seen = src
			continue
		}
		if seen != src {
			return result.SrcMixed
		}
	}
	return seen
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
//     (ktx/src/client.c:4951), and resets after a reconnect.
//   - damage: given / givenTeam / givenSelf / enemyWeapons / teamWeapons
//     come from KTX. takenEnemy and takenToDie prefer KTX's but fall back
//     to the analyzer's reconstruction from the per-hit log, so they are
//     no longer KTX-only. `taken` STAYS DERIVED — KTX's dmg.taken is
//     enemy-only (ktx/src/combat.c:1069) while ours counts every source,
//     so folding one into the other would silently change what the field
//     means.
//   - accuracy: KTX's whole block wins when present; the analyzer's
//     fire-stream reconstruction stands when it is not. The two are
//     different measurements, which is what `src` is for — see
//     result.PlayerStatsAccuracy.
//   - pickups: took / totalTook / dropped from KTX; xfer / xferSelf stay
//     derived (KTX conflates the two). Ammo kinds have no KTX counterpart
//     and stay derived — so `src: "ktx"` on this family means "KTX
//     wherever KTX carries the kind".
//   - hold: NEVER overlaid. KTX has no weapon hold time in the block, and
//     its armor hold time overcounts (see result.PlayerStatsResult).
//   - identity: ping / handicap / bot / controlMs / speed are KTX-only
//     passthrough. `login` prefers KTX's but keeps the analyzer's *auth
//     login when KTX carries none.
func applyKTXOverlay(r *result.Result) *result.PlayerStatsResult {
	stored := r.PlayerStats
	// Empty slice, not nil, for the same reason the filtered branch above
	// says so: `players` is declared required and array-typed, and a null
	// breaks a caller that ranges over it. playerStatsPost sets the section
	// unconditionally, so a demo whose stream roster came out empty reaches
	// here with stored.Players nil — exactly the degraded-parse territory
	// this endpoint advertises it handles.
	out := &result.PlayerStatsResult{
		Players: make([]result.PlayerStatsRow, 0, len(stored.Players)),
		Teams:   append([]result.PlayerStatsRow(nil), stored.Teams...),
		Sources: stored.Sources,
	}
	out.Players = append(out.Players, stored.Players...)
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
		ping := di.Ping
		row.Ping, row.Handicap, row.Bot = &ping, di.Handicap, di.Bot
		if di.Login != "" {
			// KTX's login wins, but a blank one must not erase the *auth
			// login the analyzer already read off the wire.
			row.Login = di.Login
		}
		// Presence, not non-zero-ness: KTX writes both blocks
		// unconditionally, so 0 control time and a 0/0 speed pair are
		// measurements, not gaps. Suppressing them would hide exactly the
		// player the stat is most informative about.
		if di.Control != nil {
			ms := int32(math.Round(*di.Control * 1000))
			row.ControlMs = &ms
		}
		if di.Speed != nil {
			row.Speed = &result.PlayerStatsSpeed{
				Max: float32(di.Speed.Max), Avg: float32(di.Speed.Avg),
			}
		}

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

	// The `any…` flags gate the team re-aggregation ONLY. The Sources
	// roll-up is computed from the rows in PlayerStats (rollUpSources)
	// after filtering — deriving it from "any row matched" badged the
	// whole family KTX when one row did.
	//
	// Accuracy is in the gate because it is overlaid per player here: a
	// KTX demo whose block carries `acc` but no `dmg`/`items` would
	// otherwise keep a team accuracy summed from the derived member rows
	// while every member row shows KTX's.
	//
	// Team rows are sums of the per-player rows, so re-derive them from
	// the overlaid players rather than leaving stale derived totals beside
	// KTX-sourced member rows.
	if (anyDamage || anyAccuracy || anyPickups) && len(out.Teams) > 0 {
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
		// When there is no derived row it stays ABSENT — a demo with a KTX
		// block but no damage stream genuinely has no all-sources figure,
		// and a zero would read as "took no damage at all".
		out.Taken = derived.Taken
		// COPIES: the loop below writes into the enemy and team maps, and
		// the derived ones belong to the stored artifact that every later
		// read starts from.
		out.ByWeapon = cloneCounts(derived.ByWeapon)
		out.ByWeaponTeam = cloneCounts(derived.ByWeaponTeam)
		// Self is derived-only — KTX records no per-weapon self damage —
		// so it rides through untouched, and stays ABSENT on a demo with a
		// KTX block but no damage stream (the same condition Taken above
		// tracks).
		out.ByWeaponSelf = cloneCounts(derived.ByWeaponSelf)
	}
	// KTX's own per-weapon enemy damage wins where it carries one, weapon
	// by weapon rather than family-wide.
	//
	// PRESENCE, not non-zero-ness — the same rule as the dmg block below.
	// KTX emits a weapon entry whenever the player used the weapon at all
	// (`attacks || deaths || drops || sttooks || ttooks`,
	// ktx/src/stats_json.c:382) and, inside it, a `damage` sub-block
	// whenever either counter moved (`if (stats->edamage ||
	// stats->tdamage)`, :208). So `enemy: 0` with the block present is a
	// MEASURED zero — a weapon that dealt team damage only — and an absent
	// sub-block on a present entry means both were zero. Treating that
	// zero as "absent" kept the reconstruction's number in its place and
	// then stamped the whole family `src: "ktx"`: a GL used purely for
	// team splash reads `{enemy: 0, team: 700}` in KTX, and the response
	// asserted 700 ENEMY damage under a KTX badge, with byWeapon no longer
	// summing to `given`.
	//
	// Keys KTX has no vocabulary for at all (`unknown`, `stomp`, `tele`,
	// `explobox` — mvd-reader/mvd/types.go:286) are deliberately KEPT from
	// the reconstruction: they are real measured damage, and on
	// 1on1_bananfalco_betowen_240426_dm2 the `unknown: 4` residual is
	// exactly what reconciles byWeapon with KTX's `given` of 4826.
	//
	// The TEAM counter lives in the same sub-block and is stamped on the
	// same presence rule (stats_json.c:208-212 writes `enemy` and `team`
	// together), so `team: 0` beside a non-zero `enemy` is a measured zero
	// — a weapon that only ever hit enemies — exactly as the mirror case
	// is for `enemy`.
	for w, wv := range di.Weapons {
		if wv == nil || wv.Damage == nil {
			continue
		}
		if out.ByWeapon == nil {
			out.ByWeapon = map[string]int{}
		}
		out.ByWeapon[w] = wv.Damage.Enemy
		if out.ByWeaponTeam == nil {
			out.ByWeaponTeam = map[string]int{}
		}
		out.ByWeaponTeam[w] = wv.Damage.Team
	}
	out.Given = di.Dmg.Given
	out.GivenTeam = di.Dmg.Team
	out.GivenSelf = di.Dmg.Self
	out.EnemyWeapons = di.Dmg.EnemyWeapons
	// Presence, not non-zero-ness. KTX writes the whole dmg block in one
	// unconditional statement (ktx/src/stats_json.c:353-357), so a non-nil
	// di.Dmg means every field in it was measured — including a real 0. A
	// `!= 0` test here would drop the friendly-fire figure for the player
	// who dealt none, which is the same measured-zero-becomes-absent bug
	// the cache codec exists to prevent (result/cache.go).
	tw := di.Dmg.TeamWeapons
	out.TeamWeapons = &tw
	takenEnemy := di.Dmg.Taken
	out.TakenEnemy = &takenEnemy
	if di.Dmg.TakenToDie == ktxNoDeathsSentinel {
		// KTX writes 99999 rather than dividing by zero
		// (ktx/src/stats_json.c:357). It is a sentinel, not a measurement,
		// so it must not reach a consumer as a number — fall through to the
		// derived value, which omits the field when the player never died.
		out.TakenToDie = derivedTakenToDie(derived)
	} else {
		ttd := di.Dmg.TakenToDie
		out.TakenToDie = &ttd
	}
	return &out
}

// ktxNoDeathsSentinel is the value KTX writes for taken-to-die when the
// player never died (ktx/src/stats_json.c:357) instead of dividing by
// zero. It is not a damage figure and must never be served as one.
const ktxNoDeathsSentinel = 99999

// derivedTakenToDie carries the reconstructed average forward when KTX has
// none to give, so the field's presence tracks "did this player die",
// not "which source was available".
func derivedTakenToDie(derived *result.PlayerStatsDamage) *int {
	if derived == nil || derived.TakenToDie == nil {
		return nil
	}
	v := *derived.TakenToDie
	return &v
}

// overlayAccuracy lifts KTX's per-weapon acc blocks. Returns nil when the
// player has no weapon with accuracy recorded — a spectator slot, or a
// player who never fired.
//
// The KTX block replaces the derived one WHOLESALE — it never reads
// row.Accuracy — and that is deliberate, unlike the per-weapon merge
// overlayDamage does. Damage is the same unit in both sources; accuracy
// is not. KTX counts PELLETS server-side for sg/ssg while the
// reconstruction counts trigger pulls (result.PlayerStatsAccuracy), so a
// per-weapon merge would put the two scales side by side in one map under
// one `src`, which is exactly the coercion this section exists to avoid.
//
// Measured before deciding: across all 42 cached corpus demos, every one
// of which carries a KTX block, 228 player rows with a derived accuracy
// family — ZERO weapons with derived attacks that KTX's acc set omits.
// The loss the swap could in principle cause is empty in practice, which
// is what KTX's own emission rule predicts (a weapon entry exists
// whenever the player used the weapon, ktx/src/stats_json.c:382). So no
// per-entry `src` is introduced; the family-level one is sufficient.
func overlayAccuracy(di *result.DemoInfoPlayer) *result.PlayerStatsAccuracy {
	if len(di.Weapons) == 0 {
		return nil
	}
	byWeapon := map[string]result.PlayerStatsAcc{}
	for w, wv := range di.Weapons {
		if wv == nil || wv.Acc == nil {
			continue
		}
		hits := wv.Acc.Hits
		e := result.PlayerStatsAcc{Attacks: wv.Acc.Attacks, Hits: &hits}
		// KTX omits the real/virtual split entirely unless it recorded one
		// (`if (stats->rhits || stats->vhits)`, ktx/src/stats_json.c:146) —
		// carry the distinction rather than zero-filling.
		if wv.Acc.Real != 0 || wv.Acc.Virtual != 0 {
			real, virtual := wv.Acc.Real, wv.Acc.Virtual
			e.Real, e.Virtual = &real, &virtual
		}
		byWeapon[w] = e
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

	contributed := false
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
		contributed = true
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
		contributed = true
	}

	// A player whose KTX entry carries only acc blocks contributed no
	// pickup counter at all — labelling the family "ktx" there would put a
	// KTX badge on numbers this pipeline derived.
	if !contributed {
		return nil
	}
	return &result.PlayerStatsPickups{Src: result.SrcKTX, ByKind: byKind}
}

// cloneCounts copies a per-weapon map so the overlay can write into it
// without reaching back into the stored artifact. nil in, nil out.
func cloneCounts(src map[string]int) map[string]int {
	if src == nil {
		return nil
	}
	out := make(map[string]int, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// sumOptional adds an optional member counter into an optional team
// total, keeping "absent" only while every member is absent — so a team
// figure reads as measured if any member's was.
func sumOptional(dst, src *int) *int {
	if src == nil {
		return dst
	}
	if dst == nil {
		v := *src
		return &v
	}
	*dst += *src
	return dst
}

// reaggregateTeams re-sums the team rows from overlaid player rows,
// preserving each team's window and hold figures (which the overlay never
// touches) and replacing the summed damage, accuracy and pickups.
//
// Accuracy has to be re-summed HERE as well as in the analyzer, because
// it is overlaid per player at read time: an analyzer-only aggregate
// would be stale on every KTX demo. The absent-hits and shared-src rules
// live once, in result.AggregateAccuracy.
func reaggregateTeams(players, teams []result.PlayerStatsRow) []result.PlayerStatsRow {
	out := append([]result.PlayerStatsRow(nil), teams...)
	for i := range out {
		team := &out[i]
		var dmg *result.PlayerStatsDamage
		var pickups map[string]result.PlayerStatsPickup
		var acc []*result.PlayerStatsAccuracy
		src := "" // pickups: members' shared src, else the mixed canary

		for j := range players {
			p := &players[j]
			if p.Team != team.Name {
				continue
			}
			acc = append(acc, p.Accuracy)
			if p.Damage != nil {
				if dmg == nil {
					dmg = &result.PlayerStatsDamage{Src: p.Damage.Src}
				} else if dmg.Src != p.Damage.Src {
					// Members' shared src, else the mixed canary — the
					// same rule AggregateAccuracy applies. The previous
					// "any KTX member upgrades the team" stamp badged a
					// part-derived sum as pure server counters, which is
					// the T2.3 defect surviving one level down; a mixed
					// team only occurs when the phantom-roster invariant
					// is already broken, and that is exactly when the
					// row must not claim a clean provenance.
					dmg.Src = result.SrcMixed
				}
				dmg.Given += p.Damage.Given
				dmg.GivenTeam += p.Damage.GivenTeam
				dmg.GivenSelf += p.Damage.GivenSelf
				dmg.EnemyWeapons += p.Damage.EnemyWeapons
				dmg.Taken = sumOptional(dmg.Taken, p.Damage.Taken)
				dmg.TakenEnemy = sumOptional(dmg.TakenEnemy, p.Damage.TakenEnemy)
				dmg.TeamWeapons = sumOptional(dmg.TeamWeapons, p.Damage.TeamWeapons)
				for w, n := range p.Damage.ByWeapon {
					if dmg.ByWeapon == nil {
						dmg.ByWeapon = map[string]int{}
					}
					dmg.ByWeapon[w] += n
				}
				for w, n := range p.Damage.ByWeaponTeam {
					if dmg.ByWeaponTeam == nil {
						dmg.ByWeaponTeam = map[string]int{}
					}
					dmg.ByWeaponTeam[w] += n
				}
				// Partial sum over the members that measured one — the
				// sumOptional doctrine, same as Taken above. On a mixed
				// team this under-counts givenSelf by the KTX-only
				// members' share; src:"mixed" is the canary
				// (RESULT_SCHEMA "measuredness" section).
				for w, n := range p.Damage.ByWeaponSelf {
					if dmg.ByWeaponSelf == nil {
						dmg.ByWeaponSelf = map[string]int{}
					}
					dmg.ByWeaponSelf[w] += n
				}
				// TakenToDie is an average; averaging averages across
				// players with different death counts is meaningless, so a
				// team row deliberately carries none.
			}
			if p.Pickups != nil {
				if pickups == nil {
					pickups = map[string]result.PlayerStatsPickup{}
				}
				// Same shared-or-mixed rule as damage above and
				// AggregateAccuracy.
				switch {
				case src == "":
					src = p.Pickups.Src
				case src != p.Pickups.Src:
					src = result.SrcMixed
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
		team.Accuracy = result.AggregateAccuracy(acc)
		if pickups != nil {
			team.Pickups = &result.PlayerStatsPickups{Src: src, ByKind: pickups}
		}
	}
	return out
}
