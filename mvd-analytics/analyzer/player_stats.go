package analyzer

import (
	"sort"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Player statistics (schema v63). playerStatsPost joins the artifacts that
// each hold a piece of "how did this player do" — the corrected scoreboard,
// the frag log, the damage reconstruction, the item and weapon-pickup
// timelines, the backpack drops — and adds the family none of them carry:
// possession time, integrated from the native-rate streams.
//
// It produces the fully DERIVED form. The KTX overlay (where the demoinfo
// block is the better source for a family) is applied at read time by
// view.PlayerStats, exactly as view.Damage overlays the KTX bounded summary
// — so the stored artifact and the golden corpus always say what this
// pipeline actually computed. See result.PlayerStatsResult for the schema
// and mvd-analytics/RESULT_SCHEMA.md for the family-by-family merge rules.
func playerStatsPost(res *Result, co *CoreOutputs) {
	if res.Streams == nil {
		// Every family here is keyed on the stream roster and every hold
		// figure integrates a stream. Without them there is nothing to
		// join, and an empty section would be indistinguishable from a
		// match where nobody did anything.
		return
	}

	matchMs := res.Streams.Global.MatchEnd - res.Streams.Global.MatchStart
	if matchMs < 0 {
		matchMs = 0
	}

	teamplay := isTeamplay(res, co)
	xferOK := teamplay && len(res.Backpacks) > 0

	ps := &result.PlayerStatsResult{
		Sources: result.PlayerStatsSources{
			Score: result.SrcDerived,
			Hold:  result.SrcDerived,
		},
	}

	// Derived inputs shared across rows.
	pickups := derivePickups(res, xferOK)
	if pickups != nil {
		ps.Sources.Pickups = result.SrcDerived
	}
	takenEnemy := deriveTakenEnemy(res)
	logins := deriveLogins(co)

	for i := range res.Streams.Players {
		p := &res.Streams.Players[i]
		row := buildPlayerStatsRow(res, p, matchMs, pickups, takenEnemy)
		row.Login = logins[row.Name]
		ps.Players = append(ps.Players, row)
	}
	// A scoreboard player who produced no stream (connected but never
	// spawned, or a slot the stream builder never saw) still gets a row —
	// dropping them would silently shrink the roster relative to /overview.
	seen := make(map[string]bool, len(ps.Players))
	for i := range ps.Players {
		seen[ps.Players[i].Name] = true
	}
	if res.Match != nil {
		for i := range res.Match.Players {
			mp := &res.Match.Players[i]
			if seen[mp.Name] {
				continue
			}
			row := result.PlayerStatsRow{
				Name:   mp.Name,
				Team:   mp.Team,
				Login:  logins[mp.Name],
				Window: result.PlayerStatsWindow{MatchMs: matchMs},
				Score:  deriveScore(res, mp.Name),
				Hold:   result.PlayerStatsHold{Src: result.SrcDerived},
			}
			row.Damage = deriveDamage(res, mp.Name, takenEnemy)
			row.Accuracy = deriveAccuracy(res, mp.Name)
			row.Pickups = pickupsFor(pickups, mp.Name)
			ps.Players = append(ps.Players, row)
		}
	}

	// The stored roll-up echoes the rows, so a bounded-skip demo says
	// "derived:unbounded" here too. view.PlayerStats recomputes it from
	// the rows it actually serves (rollUpSources).
	for i := range ps.Players {
		if ps.Players[i].Damage != nil {
			ps.Sources.Damage = ps.Players[i].Damage.Src
			break
		}
	}
	for i := range ps.Players {
		if ps.Players[i].Accuracy != nil {
			ps.Sources.Accuracy = result.SrcDerived
			break
		}
	}

	if teamplay {
		ps.Teams = aggregateTeamRows(ps.Players, matchMs)
	}

	res.PlayerStats = ps
}

// buildPlayerStatsRow assembles one streamed player's row.
func buildPlayerStatsRow(res *Result, p *result.PlayerStream, matchMs int32, pickups map[string]map[string]result.PlayerStatsPickup, takenEnemy map[string]int) result.PlayerStatsRow {
	present := presenceWindow(p, matchMs)
	alive := clipIntervals(aliveIntervals(p.Spawns, p.Deaths, matchMs), present)

	w := result.PlayerStatsWindow{
		MatchMs:   matchMs,
		PresentMs: totalMs(present),
		AliveMs:   totalMs(alive),
	}
	w.DeadMs = w.PresentMs - w.AliveMs

	row := result.PlayerStatsRow{
		Name:     p.Name,
		Team:     p.Team,
		Window:   w,
		Score:    deriveScore(res, p.Name),
		Damage:   deriveDamage(res, p.Name, takenEnemy),
		Accuracy: deriveAccuracy(res, p.Name),
		Pickups:  pickupsFor(pickups, p.Name),
		Hold:     deriveHold(p, alive, w),
	}
	return row
}

// deriveScore reads the corrected scoreboard. MatchResult carries the
// frag-log-corrected kills/deaths/suicides (match-final post-processor) and
// the svc_updatefrags net score; FragResult carries the team-kill count.
//
// The kill side is OPTIONAL, because the two sides of this family do not
// rest on the same evidence. Frags is the svc_updatefrags net score and
// Deaths is counted from the protocol death events; both are measured on
// every demo. Kills, suicides, teamkills and the per-weapon split are all
// attributed from the obituary-derived frag log, and some servers never
// give us one in a matchable form — on 4on4_l_vs_la[e1m2] a full 4v4
// scoreboard with 230 team frags and 121 deaths yielded not a single frag
// entry. Reporting 0 kills and 0.0% efficiency there is
// byte-indistinguishable from a genuinely killless team, so the fields
// are omitted instead. See result.PlayerStatsScore.
func deriveScore(res *Result, name string) result.PlayerStatsScore {
	s := result.PlayerStatsScore{Src: result.SrcDerived}
	var kills, suicides, teamKills int
	var byWeapon map[string]int
	onScoreboard := false
	if res.Match != nil {
		for i := range res.Match.Players {
			if res.Match.Players[i].Name == name {
				mp := &res.Match.Players[i]
				s.Frags, s.Deaths = mp.Frags, mp.Deaths
				kills, suicides = mp.Kills, mp.Suicides
				onScoreboard = true
				break
			}
		}
	}
	if res.Frags != nil {
		if pf := res.Frags.ByPlayer[name]; pf != nil {
			teamKills = pf.TeamKills
			// A player absent from the scoreboard — no Match section at
			// all, or a streamed name the match analyzer did not resolve
			// into a row — still has a frag-log line. Use it rather than
			// reporting zeros for someone the demo plainly recorded
			// killing and dying. Frags (the net score) has no frag-log
			// equivalent and stays 0; suicides have one and are counted
			// the same way scoreboardStatsPost does it (postprocess.go:36-41
			// — per-victim IsSuicide over the final log), rather than being
			// left at a fabricated 0 beside recovered kills and deaths.
			if !onScoreboard {
				kills, s.Deaths = pf.Kills, pf.Deaths
				for i := range res.Frags.Frags {
					if f := &res.Frags.Frags[i]; f.IsSuicide && f.Victim == name {
						suicides++
					}
				}
			}
			for w, n := range pf.ByWeapon {
				if n == 0 {
					continue
				}
				if byWeapon == nil {
					byWeapon = map[string]int{}
				}
				byWeapon[w] = n
			}
		}
	}
	if killsMeasurable(res) {
		eff := result.NewShare(int32(kills), int32(kills+s.Deaths))
		s.Kills, s.Suicides, s.TeamKills = &kills, &suicides, &teamKills
		s.Efficiency = &eff
		s.ByWeapon = byWeapon
	}
	return s
}

// killsMeasurable reports whether kill attribution was observable on this
// demo at all.
//
// An empty frag log on a demo where players demonstrably died means every
// obituary went unmatched — the server printed them in a form this
// pipeline does not parse, or printed none. Deaths still count (they come
// from the protocol death events, not the obituaries), which is exactly
// what makes the zeros dangerous: a row reading 0 kills / 92 deaths looks
// measured.
//
// Deliberately DEMO-GLOBAL rather than per row, so every row on a demo
// agrees and a team aggregate can never mix a measured member with an
// unmeasured one. A demo where nobody died has nothing to contradict and
// keeps its honest zeros.
func killsMeasurable(res *Result) bool {
	if res.Frags == nil || len(res.Frags.Frags) > 0 {
		return true
	}
	if res.Match == nil {
		return true
	}
	for i := range res.Match.Players {
		if res.Match.Players[i].Deaths > 0 {
			return false
		}
	}
	return true
}

// deriveDamage reads the damage reconstruction, preferring the bounded
// family: bounded is KTX-scoreboard semantics (armor absorbed, health
// capped to remaining), which is what these fields mean and what the KTX
// overlay in view.PlayerStats replaces them with.
func deriveDamage(res *Result, name string, takenEnemy map[string]int) *result.PlayerStatsDamage {
	if res.Damage == nil {
		return nil
	}
	pd := res.Damage.ByPlayer[name]
	if pd == nil {
		// damage.go only creates an entry on an actual hit, so a player who
		// neither dealt nor took a point of damage has none. On a demo that
		// carries the damage stream that is an OBSERVED all-zero row, not an
		// unmeasurable one — the same distinction deriveTakenEnemy makes a
		// few lines below when it zero-fills every player in the table.
		// Collapsing it into an absent family says "we could not tell",
		// which on this demo is false.
		pd = &result.PlayerDamage{}
	}
	src := pd
	if pd.Bounded != nil {
		src = pd.Bounded
	}
	// When the bounded reconstruction was skipped, every pd.Bounded is nil
	// and this family silently became raw wire damage INCLUDING overkill
	// while still reading "derived" — 38-44% high on
	// 4on4_oeks_vs_tsq[dm2], an order of magnitude on k_instagib's flat
	// 5000/hit. Mark it so a caller can branch on one field instead of
	// correlating src with damage.boundedMode.
	//
	// Keyed on BoundedMode, the demo-global CAUSE, rather than on
	// pd.Bounded == nil at the row: the bounded nest is created lazily
	// (result/damage.go BoundedNest), so a standard-mode player whose only
	// hits fell outside the match window also has a nil nest, and marking
	// that row would both overstate the degradation and split a team's
	// members across two src values.
	srcName := result.SrcDerived
	if strings.HasPrefix(res.Damage.BoundedMode, "skipped:") {
		srcName = result.SrcDerivedUnbounded
	}
	taken := src.Taken
	out := &result.PlayerStatsDamage{
		Src:          srcName,
		Given:        src.Given,
		GivenTeam:    src.GivenTeam,
		GivenSelf:    src.GivenSelf,
		EnemyWeapons: src.EWep,
		Taken:        &taken,
	}
	// The three given directions get the same zero-dropping copy: a weapon
	// the player dealt nothing with in that direction is omitted, never
	// zero-filled (result.PlayerStatsDamage documents the rule).
	for w, n := range src.ByWeapon {
		if n == 0 {
			continue
		}
		if out.ByWeapon == nil {
			out.ByWeapon = map[string]int{}
		}
		out.ByWeapon[w] = n
	}
	for w, n := range src.ByWeaponTeam {
		if n == 0 {
			continue
		}
		if out.ByWeaponTeam == nil {
			out.ByWeaponTeam = map[string]int{}
		}
		out.ByWeaponTeam[w] = n
	}
	for w, n := range src.ByWeaponSelf {
		if n == 0 {
			continue
		}
		if out.ByWeaponSelf == nil {
			out.ByWeaponSelf = map[string]int{}
		}
		out.ByWeaponSelf[w] = n
	}
	// TakenEnemy and TakenToDie were KTX-only until we reconstructed them
	// from the per-hit log: a demo without a demoinfo block should degrade
	// to a worse number, not to a missing field, or the old-demo response
	// becomes a different shape to program against.
	if te, ok := takenEnemy[name]; ok {
		out.TakenEnemy = &te
		// KTX's taken-to-die is integer dmg_t / deaths
		// (ktx/src/stats_json.c:357), with a 99999 sentinel when the player
		// never died. We omit instead of copying the sentinel — absent is
		// what "no deaths to average over" means.
		if d := deathsOf(res, name); d > 0 {
			ttd := te / d
			out.TakenToDie = &ttd
		}
	}
	return out
}

// deriveTakenEnemy reconstructs KTX's dmg_t — damage taken from ENEMY
// players only (ktx/src/combat.c:1069 accumulates it in the enemy branch)
// — per victim, from the per-hit log. Our PlayerDamage.Taken counts every
// source (enemy + team + self + environment), so the two are different
// quantities and both are surfaced; this is the one comparable to KTX's.
//
// Uses the bounded value per hit (KTX-scoreboard semantics: armor absorbed,
// health capped to remaining), falling back to the wire value where no
// reconstruction was made. Positional kills live outside Events but do fold
// into KTX's dmg_t, so enemy telefrags and stomps are added back.
//
// Returns nil when the demo carries no damage stream.
func deriveTakenEnemy(res *Result) map[string]int {
	if res.Damage == nil {
		return nil
	}
	out := map[string]int{}
	for i := range res.Damage.Events {
		ev := &res.Damage.Events[i]
		if ev.IsTeam || ev.IsSelf || ev.IsEnv {
			continue
		}
		v := ev.Damage
		if ev.Bounded != nil {
			v = *ev.Bounded
		}
		out[ev.Victim] += v
	}
	for _, list := range [][]result.PositionalKill{res.Damage.Telefrags, res.Damage.Stomps} {
		for i := range list {
			pk := &list[i]
			if pk.IsTeam || pk.Bounded == nil {
				continue
			}
			// PositionalKill has no IsSelf/IsEnv flag, so the enemy test is
			// on the names. KTX accumulates dmg_t only in the enemy branch
			// (ktx/src/combat.c:1069 sits in the else of the same-team
			// test; the self branch at 1046 never touches it), and the
			// damage analyzer's own enemyTakenBounded makes the same two
			// exclusions — a self-telefrag through a teleporter, or the
			// degenerate world attacker, would otherwise inflate takenEnemy
			// by a full armor+health and drag takenToDie with it.
			if pk.Attacker == pk.Victim || pk.Attacker == "world" {
				continue
			}
			out[pk.Victim] += *pk.Bounded
		}
	}
	// Every player in the damage table gets an entry, so a player who took
	// no enemy damage reads as an observed zero rather than as unknown.
	for name := range res.Damage.ByPlayer {
		if _, ok := out[name]; !ok {
			out[name] = 0
		}
	}
	return out
}

// deathsOf is the corrected death count the taken-to-die average divides
// by, from the same scoreboard the score family reports.
func deathsOf(res *Result, name string) int {
	if res.Match != nil {
		for i := range res.Match.Players {
			if res.Match.Players[i].Name == name {
				return res.Match.Players[i].Deaths
			}
		}
	}
	if res.Frags != nil {
		if pf := res.Frags.ByPlayer[name]; pf != nil {
			return pf.Deaths
		}
	}
	return 0
}

// deriveAccuracy reconstructs the per-weapon accuracy block from the shot
// stream, so a demo with no KTX demoinfo still answers "how well did they
// aim" instead of dropping the field.
//
// It is NOT the same measurement as KTX's, and the family's Src says so.
// KTX counts server-side: for the shotgun and super shotgun its `attacks`
// is a PELLET count and `hits` counts pellets that connected. Ours counts
// TRIGGER PULLS and the fires that produced at least one linked damage
// event — so shotgun accuracy in particular reads on a different scale,
// and the two must never be compared across demos without reading Src.
// For the single-projectile weapons (rl, lg, gl, ng, sng) the two count
// the same events and are broadly comparable.
//
// Returns nil when the demo decoded no weapon fires for this player.
func deriveAccuracy(res *Result, name string) *result.PlayerStatsAccuracy {
	if res.Shots == nil {
		return nil
	}
	for i := range res.Shots.ByPlayer {
		ps := &res.Shots.ByPlayer[i]
		if ps.Player != name {
			continue
		}
		// Hits come from linking each fire to a damage event, so they are
		// only meaningful when the demo carries a damage stream at all.
		// Without one every weapon would read hits=0, i.e. "shot and never
		// hit" — a fabricated zero where the honest answer is "not
		// measurable". Attacks still stand on their own.
		linkable := res.Damage != nil
		byWeapon := map[string]result.PlayerStatsAcc{}
		for _, w := range ps.ByWeapon {
			if w.Shots == 0 {
				continue
			}
			e := result.PlayerStatsAcc{Attacks: w.Shots}
			if linkable {
				hits := w.Hits
				e.Hits = &hits
			}
			byWeapon[w.Weapon] = e
		}
		if len(byWeapon) == 0 {
			return nil
		}
		return &result.PlayerStatsAccuracy{Src: result.SrcDerived, ByWeapon: byWeapon}
	}
	return nil
}

// deriveLogins maps player name to the `*auth` login from userinfo — the
// wire-side source for the login KTX's demoinfo block also carries, so an
// old demo can still say who was playing. Empty for unauthenticated
// players and on servers that do not set it.
func deriveLogins(co *CoreOutputs) map[string]string {
	// Slot order, not map order: two identities can share a display name
	// with different *auth values, and first-wins over a randomised range
	// would then flip the attribution between runs of the same demo.
	slots := make([]int, 0, len(co.Sessions))
	for slot := range co.Sessions {
		slots = append(slots, slot)
	}
	sort.Ints(slots)

	out := map[string]string{}
	for _, slot := range slots {
		for _, s := range co.Sessions[slot] {
			if s.Auth != "" && out[s.Name] == "" {
				out[s.Name] = s.Auth
			}
		}
	}
	return out
}

// derivePickups tallies every acquisition per player per item kind.
//
// Two sources, split by what each one actually observes: non-weapon kinds
// come from the item timeline (which is the pickup record for world
// spawners), weapons from WeaponPickups — because a weapon can also arrive
// in a backpack, which the item timeline knows nothing about and KTX's
// wpn.tooks does count (TookWeaponHandler runs on backpack touch too,
// ktx/src/items.c:2475).
//
// Returns nil when the demo carries neither source.
func derivePickups(res *Result, xferOK bool) map[string]map[string]result.PlayerStatsPickup {
	if res.Items == nil && res.WeaponPickups == nil && res.Backpacks == nil {
		return nil
	}
	acc := map[string]map[string]*result.PlayerStatsPickup{}
	get := func(player, kind string) *result.PlayerStatsPickup {
		byKind := acc[player]
		if byKind == nil {
			byKind = map[string]*result.PlayerStatsPickup{}
			acc[player] = byKind
		}
		e := byKind[kind]
		if e == nil {
			e = &result.PlayerStatsPickup{}
			byKind[kind] = e
		}
		return e
	}

	if res.Items != nil {
		for i := range res.Items.Items {
			it := &res.Items.Items[i]
			if isWeaponKind(it.Kind) {
				continue // weapons are counted from WeaponPickups
			}
			for _, ph := range it.Phases {
				if ph.TakenBy == "" {
					continue
				}
				get(ph.TakenBy, it.Kind).Took++
			}
		}
	}

	for i := range res.WeaponPickups {
		wp := &res.WeaponPickups[i]
		e := get(wp.Player, wp.Weapon)
		e.TotalTook++
		if !wp.HadBefore {
			e.Took++
		}

		// Transfers are credited to the DROPPER, not the picker.
		//
		// On a CTF demo this deliberately DIVERGES from KTX, which gates the
		// counter on isTeam() and so reports 0 transfers for every CTF game
		// (ktx/src/items.c:2587 — isCTF() is a separate mode). The teams in
		// CTF are real and the transfers happened; KTX simply declines to
		// count them, so the §6 cross-check "xfer + xferSelf == KTX xferRL"
		// holds on team games only.
		if !xferOK || wp.Source != "backpack" || wp.Dropper == "" {
			continue
		}
		if wp.DropperTeam == "" || wp.DropperTeam != wp.Team {
			continue // taken by an opponent — a denial, not a transfer
		}
		d := get(wp.Dropper, wp.Weapon)
		if wp.Player == wp.Dropper {
			d.XferSelf = incPtr(d.XferSelf)
		} else {
			d.Xfer = incPtr(d.Xfer)
		}
	}

	for i := range res.Backpacks {
		bp := &res.Backpacks[i]
		get(bp.Player, bp.Weapon).Dropped++
	}

	out := make(map[string]map[string]result.PlayerStatsPickup, len(acc))
	for player, byKind := range acc {
		flat := make(map[string]result.PlayerStatsPickup, len(byKind))
		for kind, e := range byKind {
			// Zero-fill the transfer counters for anyone who dropped a pack
			// of this weapon that nobody on their team recovered: where
			// transfers ARE observable, an unrecovered pack is an observed
			// zero, and the pointer must say so rather than reading as
			// "unobservable".
			if xferOK && e.Dropped > 0 {
				if e.Xfer == nil {
					e.Xfer = new(int)
				}
				if e.XferSelf == nil {
					e.XferSelf = new(int)
				}
			}
			flat[kind] = *e
		}
		out[player] = flat
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func incPtr(p *int) *int {
	if p == nil {
		v := 1
		return &v
	}
	*p++
	return p
}

func pickupsFor(pickups map[string]map[string]result.PlayerStatsPickup, name string) *result.PlayerStatsPickups {
	byKind := pickups[name]
	if len(byKind) == 0 {
		return nil
	}
	return &result.PlayerStatsPickups{Src: result.SrcDerived, ByKind: byKind}
}

// isWeaponKind reports whether an item kind is a slot weapon, i.e. one
// whose pickups WeaponPickups (not the item timeline) accounts for.
// Mirrors result.ItemTimeline.Category()'s "weapon" class.
func isWeaponKind(kind string) bool {
	switch kind {
	case "rl", "lg", "gl", "ssg", "sng", "ng":
		return true
	}
	return false
}

// deriveHold integrates the possession streams. Every integral is clipped
// to the player's alive intervals: inventory bits clear on death, so the
// clip is normally a no-op, but it is the guarantee that a stream artefact
// (a bit that survives a death, a re-grant recorded a frame early) can
// never inflate a hold figure past the time the player was actually
// playing.
func deriveHold(p *result.PlayerStream, alive []result.Interval, w result.PlayerStatsWindow) result.PlayerStatsHold {
	h := result.PlayerStatsHold{Src: result.SrcDerived}

	addWeapon := func(kind string, iv []result.Interval) {
		if st, ok := holdStat(iv, alive, w); ok {
			if h.Weapons == nil {
				h.Weapons = map[string]result.HoldStat{}
			}
			h.Weapons[kind] = st
		}
	}
	addWeapon("rl", p.RL)
	addWeapon("lg", p.LG)
	addWeapon("gl", p.GL)
	addWeapon("ssg", p.SSG)
	addWeapon("sng", p.SNG)

	addPowerup := func(kind string, iv []result.Interval) {
		if st, ok := holdStat(iv, alive, w); ok {
			if h.Powerups == nil {
				h.Powerups = map[string]result.HoldStat{}
			}
			h.Powerups[kind] = st
		}
	}
	addPowerup("quad", p.Quad)
	addPowerup("pent", p.Pent)
	addPowerup("ring", p.Ring)

	// Armor is a run-length stream, not intervals: consecutive samples of
	// the same type collapse into one possession run.
	armor := map[string][]result.Interval{}
	for _, kind := range armorRuns(p.ArmorType, w.MatchMs) {
		armor[kind.kind] = append(armor[kind.kind], kind.iv)
	}
	var armorTotal int32
	for _, kind := range []string{"ra", "ya", "ga"} {
		if st, ok := holdStat(armor[kind], alive, w); ok {
			if h.Armor == nil {
				h.Armor = map[string]result.HoldStat{}
			}
			h.Armor[kind] = st
			armorTotal += st.Ms
		}
	}
	// "none" is the alive-time complement — the stat KTX structurally
	// cannot produce, since its armor clocks never close on the armor
	// being chewed to zero. Emitted whenever we know the alive window AND
	// the armor stream was observed at all, including the
	// all-alive-with-no-armor case (a full-match "none" is a real and
	// interesting reading, not a missing value).
	//
	// The stream test is what separates the two. appendChangeStr
	// (timeline_streams.go:36-41) appends the first sample
	// unconditionally, so a player who genuinely never held armor still
	// carries at = [{0,""}] — length >= 1. An EMPTY stream means the armor
	// state was never observed, and the complement of nothing over a real
	// alive window is a fabricated maximum: on the POV recording
	// dag_caps_e1m2 only the recorder has stat streams, and the other
	// seven rows claimed 100% no-armor while the same row listed their
	// armor pickups.
	if w.AliveMs > 0 && len(p.ArmorType) > 0 {
		none := w.AliveMs - armorTotal
		if none < 0 {
			// Overlapping armor runs would be a stream-builder bug; clamp
			// rather than emit a negative duration, and let the invariant
			// test catch it.
			none = 0
		}
		if h.Armor == nil {
			h.Armor = map[string]result.HoldStat{}
		}
		noneRuns := complementIntervals(mergeIntervals(flattenArmor(armor)), alive)
		h.Armor["none"] = result.HoldStat{
			Ms:         none,
			Runs:       len(noneRuns),
			LongestMs:  longestMs(noneRuns),
			ShareAlive: result.NewShare(none, w.AliveMs),
			ShareMatch: result.NewShare(none, w.MatchMs),
		}
	}
	return h
}

func flattenArmor(armor map[string][]result.Interval) []result.Interval {
	var all []result.Interval
	for _, iv := range armor {
		all = append(all, iv...)
	}
	return all
}

// holdStat integrates one item's possession intervals against the alive
// window. Reports ok=false when the player never held the item, so the
// caller can omit the key rather than emit a zero row for every weapon
// nobody touched.
func holdStat(iv, alive []result.Interval, w result.PlayerStatsWindow) (result.HoldStat, bool) {
	if len(iv) == 0 {
		return result.HoldStat{}, false
	}
	held := clipIntervals(mergeIntervals(iv), alive)
	if len(held) == 0 {
		return result.HoldStat{}, false
	}
	ms := totalMs(held)
	return result.HoldStat{
		Ms:         ms,
		Runs:       len(held),
		LongestMs:  longestMs(held),
		ShareAlive: result.NewShare(ms, w.AliveMs),
		ShareMatch: result.NewShare(ms, w.MatchMs),
	}, true
}

type armorRun struct {
	kind string
	iv   result.Interval
}

// armorRuns converts the ArmorType transition stream into per-type
// possession intervals. A run ends at the next transition (of any type)
// and the last run is closed at match end. Samples outside [0, matchEnd]
// are clipped; the empty-string value means "no armor" and opens no run.
func armorRuns(at []result.ChangeStr, matchMs int32) []armorRun {
	var runs []armorRun
	for i := range at {
		kind := at[i].V
		if kind == "" {
			continue
		}
		start := at[i].T
		end := matchMs
		if i+1 < len(at) {
			end = at[i+1].T
		}
		if start < 0 {
			start = 0
		}
		if end > matchMs {
			end = matchMs
		}
		if end <= start {
			continue
		}
		runs = append(runs, armorRun{kind: kind, iv: result.Interval{Start: start, End: end}})
	}
	return runs
}

// presenceWindow is the span of the match this player was in it, as a
// single interval. Presence is read off the position track — the one
// stream sampled at native rate for as long as a player exists, and always
// populated in memory during the pipeline run — falling back to the
// spawn/death markers and finally to the whole match.
//
// It is what separates a late joiner from a player who was present and
// dead: the alive-interval rule (see aliveIntervals) treats "no death yet"
// as alive since match start, which is right for someone who was there and
// wrong for someone who had not connected.
//
// It is a SINGLE interval, so a mid-match absence is bridged rather than
// excluded — a player who disconnects and rejoins counts as present
// throughout. That is deliberate for want of a better signal, not an
// oversight: the pipeline has no disconnect record to key on. The
// identity analyser's Sessions look like one but are not — their first
// and last bounds are widened to ±inf so a lookup at any time resolves
// (analyzer/identity.go:239-240) — and splitting on a position-track gap
// would need an invented threshold, which is the kind of made-up filter
// this repo avoids. Measured on the reconnect corpus demo (gameId
// 216835) the position track has no gap at all to split on: 56 ms is the
// largest interval between samples for every player, reconnects
// included. See player_stats.md for the resulting limitation.
func presenceWindow(p *result.PlayerStream, matchMs int32) []result.Interval {
	first, last, ok := int32(0), int32(0), false
	note := func(t int32) {
		if !ok {
			first, last, ok = t, t, true
			return
		}
		if t < first {
			first = t
		}
		if t > last {
			last = t
		}
	}
	if p.Position != nil && len(p.Position.T) > 0 {
		note(p.Position.T[0])
		note(p.Position.T[len(p.Position.T)-1])
	}
	if len(p.Spawns) > 0 {
		note(p.Spawns[0])
		note(p.Spawns[len(p.Spawns)-1])
	}
	if len(p.Deaths) > 0 {
		note(p.Deaths[0])
		note(p.Deaths[len(p.Deaths)-1])
	}
	if !ok {
		// No position track, no spawns, no deaths. Falling back to the whole
		// match here used to report such a player as alive for every
		// millisecond of it — on 4on4_l_vs_la[e1m2], Sectoid's entire
		// recorded existence is 3.5 s of possession at the very end of an
		// 18-minute match, and he was served aliveMs = 1097743 with
		// "no armor 100%". That is a fabricated maximum, and it also
		// inflates the team's alive-time and matchMs x members denominators
		// for everyone else.
		//
		// The possession streams ARE evidence of presence — deriveHold
		// integrates them a few lines later — so use their extent. A player
		// with no signal of any kind gets an empty window (presentMs 0),
		// which is the scoreboard-only shape and reads as "we never saw
		// them play" rather than "they played the whole match".
		for _, iv := range possessionExtent(p) {
			note(iv.Start)
			note(iv.End)
		}
		if !ok {
			return nil
		}
	}
	if first < 0 {
		first = 0
	}
	if last > matchMs {
		last = matchMs
	}
	if last <= first {
		return nil
	}
	return []result.Interval{{Start: first, End: last}}
}

// possessionExtent returns the player's possession intervals across every
// tracked item, as the last-resort presence signal for a stream that
// carries no position track and no spawn/death markers. Not a presence
// measurement in its own right — it is a floor: whatever else they did,
// they demonstrably held these things at these times.
func possessionExtent(p *result.PlayerStream) []result.Interval {
	var all []result.Interval
	for _, iv := range [][]result.Interval{p.RL, p.LG, p.GL, p.SSG, p.SNG, p.Quad, p.Pent, p.Ring} {
		all = append(all, iv...)
	}
	// Armor is a transition stream, so the samples themselves are the
	// evidence — armorRuns needs a match window to close the last run
	// against and would clip them here.
	for _, c := range p.ArmorType {
		all = append(all, result.Interval{Start: c.T, End: c.T})
	}
	return all
}

// aliveIntervals converts the spawn / death markers into alive intervals
// over [0, matchMs].
//
// The liveness rule is the repo's canonical one — this function IS its
// statement: a player is alive from match start and stays alive
// until a death; each death begins a dead period the next spawn ends. It
// deliberately does NOT require a recorded match-start spawn — KTX emits a
// player's first spawn only on their first RESPAWN, so keying off "most
// recent spawn" would mark everyone dead until minutes into the match.
// This is the derivation behind PlayerStream.Alive, which LOS, aim,
// loc-graph and region-control all now read. The two rival copies it used
// to warn about — analyzer.losAliveAt and aimcore's aimAliveAt — are gone
// (v64): both used a strict `lastSpawn > lastDeath`, which LATCHES when a
// death and the respawn it triggers share a millisecond, reporting the
// player dead for the whole remaining life rather than for an instant.
// Measured before removal: 100.7 s of one player's 1143.7 s match. The
// tie-break here — deaths sorted before spawns, so an instant respawn reads
// alive — is the correct one and is now the only one.
//
// view.playerActiveInWindow remains separate on purpose: it answers a
// different question (does this player appear ANYWHERE in this bucket
// window), resolves the same tie correctly already, and carries fallbacks
// for streams with no markers at all.
func aliveIntervals(spawns, deaths []int32, matchMs int32) []result.Interval {
	type ev struct {
		t     int32
		alive bool
	}
	evs := make([]ev, 0, len(spawns)+len(deaths))
	for _, t := range spawns {
		evs = append(evs, ev{t: t, alive: true})
	}
	for _, t := range deaths {
		evs = append(evs, ev{t: t, alive: false})
	}
	// Stable order with deaths before spawns at an identical timestamp: a
	// death and the respawn it triggers can share a millisecond, and the
	// player is alive after that pair, not dead.
	sort.SliceStable(evs, func(i, j int) bool {
		if evs[i].t != evs[j].t {
			return evs[i].t < evs[j].t
		}
		return !evs[i].alive && evs[j].alive
	})

	var out []result.Interval
	alive := true
	start := int32(0)
	for _, e := range evs {
		if e.t < 0 || e.t > matchMs {
			continue
		}
		if e.alive == alive {
			continue
		}
		if alive {
			if e.t > start {
				out = append(out, result.Interval{Start: start, End: e.t})
			}
		} else {
			start = e.t
		}
		alive = e.alive
	}
	if alive && matchMs > start {
		out = append(out, result.Interval{Start: start, End: matchMs})
	}
	return out
}

// mergeIntervals sorts and unions overlapping / touching intervals.
func mergeIntervals(iv []result.Interval) []result.Interval {
	if len(iv) < 2 {
		return append([]result.Interval(nil), iv...)
	}
	cp := append([]result.Interval(nil), iv...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Start < cp[j].Start })
	out := cp[:1]
	for _, v := range cp[1:] {
		last := &out[len(out)-1]
		if v.Start <= last.End {
			if v.End > last.End {
				last.End = v.End
			}
			continue
		}
		out = append(out, v)
	}
	return out
}

// clipIntervals intersects a (sorted, merged) interval list with a window
// list. Both inputs are treated as half-open [Start, End).
func clipIntervals(iv, window []result.Interval) []result.Interval {
	if len(iv) == 0 || len(window) == 0 {
		return nil
	}
	var out []result.Interval
	for _, a := range iv {
		for _, b := range window {
			s, e := a.Start, a.End
			if b.Start > s {
				s = b.Start
			}
			if b.End < e {
				e = b.End
			}
			if e > s {
				out = append(out, result.Interval{Start: s, End: e})
			}
		}
	}
	return mergeIntervals(out)
}

// complementIntervals returns the parts of window not covered by iv.
func complementIntervals(iv, window []result.Interval) []result.Interval {
	var out []result.Interval
	for _, w := range window {
		cur := w.Start
		for _, a := range iv {
			if a.End <= cur || a.Start >= w.End {
				continue
			}
			if a.Start > cur {
				out = append(out, result.Interval{Start: cur, End: a.Start})
			}
			if a.End > cur {
				cur = a.End
			}
		}
		if cur < w.End {
			out = append(out, result.Interval{Start: cur, End: w.End})
		}
	}
	return out
}

func totalMs(iv []result.Interval) int32 {
	var sum int32
	for _, v := range iv {
		sum += v.End - v.Start
	}
	return sum
}

func longestMs(iv []result.Interval) int32 {
	var max int32
	for _, v := range iv {
		if d := v.End - v.Start; d > max {
			max = d
		}
	}
	return max
}

// isTeamplay reports whether this match had real teams, mirroring KTX's
// isTeam() gate on the transfer counters. The serverinfo/countdown
// teamplay cvar is the direct signal; the roster's duel verdict and a
// team-membership count back it up on demos that carry neither.
//
// KTX's own gate is the MODE (`k_mode == gtTeam`, ktx/src/g_utils.c:1581),
// not the cvar, and the two disagree: an FFA server can run with
// `teamplay 2` set — the ffa_5[dm4] demo in the test corpus does — and
// then every player sits on their own "team" (often the empty string).
// Trusting the cvar there made `DropperTeam == Team` trivially true and
// invented a pack transfer for every backpack anyone picked up. So a mode
// KTX would not count for is rejected before the cvar is consulted.
func isTeamplay(res *Result, co *CoreOutputs) bool {
	if co.IsDuel() {
		return false
	}
	if res.Metadata != nil {
		if ms := res.Metadata.MatchSettings; ms != nil {
			if isNonTeamMode(ms.Mode) {
				return false
			}
			if ms.Teamplay > 0 {
				return true
			}
		}
		if tp, ok := res.Metadata.ServerInfo["teamplay"]; ok {
			return tp != "" && tp != "0"
		}
	}
	// No cvar: fall back to the roster shape — teams exist and at least
	// one holds more than a single player (an FFA scoreboard lists each
	// player under their own colour, which is not teamplay).
	if res.Match == nil {
		return false
	}
	counts := map[string]int{}
	for i := range res.Match.Players {
		if t := res.Match.Players[i].Team; t != "" {
			counts[t]++
		}
	}
	for _, n := range counts {
		if n > 1 {
			return true
		}
	}
	return false
}

// isNonTeamMode reports whether the countdown's Mode line names a mode
// where every player fights alone, so "the dropper's team" is not a real
// team. The names come from KTX's countdown centerprint (metadata.go
// flattens "F F A" to "FFA"); anything unrecognised — including CTF, where
// the teams ARE real — falls through to the cvar and roster checks.
func isNonTeamMode(mode string) bool {
	switch strings.ToLower(mode) {
	case "ffa", "duel", "1on1", "race", "bloodfest":
		return true
	}
	return false
}

// aggregateTeamRows sums the player rows per team. Hold shares are
// recomputed over TEAM time — summed alive time, and match window times
// member count — never averaged from the per-player shares, which would
// weight a player who was dead most of the match equally with one who was
// not.
func aggregateTeamRows(players []result.PlayerStatsRow, matchMs int32) []result.PlayerStatsRow {
	order := []string{}
	byTeam := map[string][]*result.PlayerStatsRow{}
	for i := range players {
		t := players[i].Team
		if t == "" {
			continue
		}
		if _, ok := byTeam[t]; !ok {
			order = append(order, t)
		}
		byTeam[t] = append(byTeam[t], &players[i])
	}
	if len(order) == 0 {
		return nil
	}

	var out []result.PlayerStatsRow
	for _, team := range order {
		members := byTeam[team]
		row := result.PlayerStatsRow{
			Name:   team,
			Window: result.PlayerStatsWindow{MatchMs: matchMs},
			Score:  result.PlayerStatsScore{Src: result.SrcDerived},
			Hold:   result.PlayerStatsHold{Src: result.SrcDerived},
		}
		var dmg *result.PlayerStatsDamage
		var pickups map[string]result.PlayerStatsPickup
		acc := make([]*result.PlayerStatsAccuracy, 0, len(members))

		for _, m := range members {
			acc = append(acc, m.Accuracy)
			row.Window.PresentMs += m.Window.PresentMs
			row.Window.AliveMs += m.Window.AliveMs
			row.Window.DeadMs += m.Window.DeadMs
			row.Score.Frags += m.Score.Frags
			row.Score.Deaths += m.Score.Deaths
			// The kill side is optional and the condition that omits it is
			// demo-global (killsMeasurable), so these are either present on
			// every member or on none — addPtr can never end up mixing a
			// measured member with an unmeasured one here.
			row.Score.Kills = addPtr(row.Score.Kills, m.Score.Kills)
			row.Score.Suicides = addPtr(row.Score.Suicides, m.Score.Suicides)
			row.Score.TeamKills = addPtr(row.Score.TeamKills, m.Score.TeamKills)
			row.Score.ByWeapon = addWeaponCounts(row.Score.ByWeapon, m.Score.ByWeapon)

			if m.Damage != nil {
				if dmg == nil {
					// Inherit the members' src rather than asserting
					// "derived": on a bounded-skip demo every member
					// carries "derived:unbounded", and the team total is
					// exactly as raw as they are.
					dmg = &result.PlayerStatsDamage{Src: m.Damage.Src}
				}
				dmg.Given += m.Damage.Given
				dmg.GivenTeam += m.Damage.GivenTeam
				dmg.GivenSelf += m.Damage.GivenSelf
				dmg.EnemyWeapons += m.Damage.EnemyWeapons
				dmg.Taken = addPtr(dmg.Taken, m.Damage.Taken)
				dmg.TakenEnemy = addPtr(dmg.TakenEnemy, m.Damage.TakenEnemy)
				dmg.TeamWeapons = addPtr(dmg.TeamWeapons, m.Damage.TeamWeapons)
				dmg.ByWeapon = addWeaponCounts(dmg.ByWeapon, m.Damage.ByWeapon)
				dmg.ByWeaponTeam = addWeaponCounts(dmg.ByWeaponTeam, m.Damage.ByWeaponTeam)
				dmg.ByWeaponSelf = addWeaponCounts(dmg.ByWeaponSelf, m.Damage.ByWeaponSelf)
			}
			if m.Pickups != nil {
				if pickups == nil {
					pickups = map[string]result.PlayerStatsPickup{}
				}
				for kind, e := range m.Pickups.ByKind {
					agg := pickups[kind]
					agg.Took += e.Took
					agg.TotalTook += e.TotalTook
					agg.Dropped += e.Dropped
					agg.Xfer = addPtr(agg.Xfer, e.Xfer)
					agg.XferSelf = addPtr(agg.XferSelf, e.XferSelf)
					pickups[kind] = agg
				}
			}
		}
		if row.Score.Kills != nil {
			eff := result.NewShare(int32(*row.Score.Kills), int32(*row.Score.Kills+row.Score.Deaths))
			row.Score.Efficiency = &eff
		}
		row.Damage = dmg
		row.Accuracy = result.AggregateAccuracy(acc)
		if pickups != nil {
			row.Pickups = &result.PlayerStatsPickups{Src: result.SrcDerived, ByKind: pickups}
		}
		// The match-time denominator counts only members who were
		// actually in the match. A scoreboard-only row (connected, never
		// streamed, presentMs 0) would otherwise dilute every hold share
		// on the team by a whole match window of time nobody could have
		// played. Members is published so the denominator stays
		// recoverable from the row itself.
		present := 0
		for _, m := range members {
			if m.Window.PresentMs > 0 {
				present++
			}
		}
		row.Members = &present
		row.Hold = aggregateHold(members, row.Window.AliveMs, matchMs*int32(present))
		out = append(out, row)
	}
	return out
}

// aggregateHold sums each member's hold figures. TakenToDie is deliberately
// not aggregated anywhere: it is an average, and averaging averages across
// players with different death counts is arithmetic nobody wants.
func aggregateHold(members []*result.PlayerStatsRow, aliveDen, matchDen int32) result.PlayerStatsHold {
	h := result.PlayerStatsHold{Src: result.SrcDerived}
	fold := func(dst *map[string]result.HoldStat, src map[string]result.HoldStat) {
		for kind, st := range src {
			if *dst == nil {
				*dst = map[string]result.HoldStat{}
			}
			agg := (*dst)[kind]
			agg.Ms += st.Ms
			agg.Runs += st.Runs
			if st.LongestMs > agg.LongestMs {
				agg.LongestMs = st.LongestMs
			}
			(*dst)[kind] = agg
		}
	}
	for _, m := range members {
		fold(&h.Weapons, m.Hold.Weapons)
		fold(&h.Armor, m.Hold.Armor)
		fold(&h.Powerups, m.Hold.Powerups)
	}
	rescale := func(m map[string]result.HoldStat) {
		for kind, st := range m {
			st.ShareAlive = result.NewShare(st.Ms, aliveDen)
			st.ShareMatch = result.NewShare(st.Ms, matchDen)
			m[kind] = st
		}
	}
	rescale(h.Weapons)
	rescale(h.Armor)
	rescale(h.Powerups)
	return h
}

// addPtr sums two optional counters, preserving "absent" only when both
// sides are absent — so a team aggregate reads as observable if any member
// was.
func addPtr(dst, src *int) *int {
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

// addWeaponCounts folds a member's per-weapon map into a team total,
// allocating a FRESH destination on first use so a one-member team never
// ends up sharing (and then mutating) that member's own map.
func addWeaponCounts(dst, src map[string]int) map[string]int {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = make(map[string]int, len(src))
	}
	for w, n := range src {
		dst[w] += n
	}
	return dst
}
