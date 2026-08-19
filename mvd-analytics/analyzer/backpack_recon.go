package analyzer

import (
	"sort"
	"strconv"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/damagerecon"
	"github.com/mvd-analyzer/mvd-analytics/locvis"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Package-scope aliases for the BackpackDrop vocabularies:
// BackpackAnalyzer.Finalize shadows the `result` qualifier with its parameter
// name (same reason analyzer/damage_recon.go has damageSourceKTX).
const (
	backpackSourceKTX   = result.BackpackSourceKTX
	backpackSourceRecon = result.BackpackSourceReconstructed
	backpackFateExpired = result.BackpackFateExpired
)

// backpackReconPost fills the backpacks section on demos whose mod never
// emitted the `//ktx drop` hint — 50.8% of the archive, and 83.9% of the
// reconstructed-damage era.
//
// # What KTX actually does
//
// DropBackpack (ktx/src/items.c:2667-2885) runs from PlayerDie
// (ktx/src/player.c:1179) on EVERY death and, with the shipped default
// k_frp 0 (ktx/resources/example-configs/ktx/ktx.cfg:31), puts the victim's
// CURRENTLY WIELDED weapon in the pack verbatim:
//
//	item->s.v.items = self->s.v.weapon;            // items.c:2706
//
// The hint we are reproducing is emitted for exactly two of those:
//
//	if ((item->s.v.items == IT_ROCKET_LAUNCHER) || (item->s.v.items == IT_LIGHTNING))
//	        stuffcmd_flags(self, STUFFCMD_DEMOONLY, "//ktx drop ...")  // items.c:2762-2766
//
// so "did a drop happen" reduces to "what was STAT_ACTIVEWEAPON at the
// instant of death" — and mvdsv writes that stat into the MVD for every
// spawned player, from the same ent->v->weapon field
// (mvdsv/src/sv_send.c:1268). Hence PlayerStream.ActiveWeapon.
//
// The early returns, in source order, and what this pass does about each:
//
//	k_bloodfest                        → gated (mode "-bf" / countdown row)
//	match_in_progress != 2 || !dp      → the death list is already
//	                                     match-window-gated; `dp 0` has no
//	                                     wire signal (see Limitations)
//	deathtype == dtSUICIDE             → gated: dtSUICIDE is ONLY the /kill
//	                                     command (client.c:1008), whose
//	                                     obituary is " suicides". Rocket
//	                                     suicides, falls, drowning and lava
//	                                     all DO drop a pack.
//	no ammo and no droppable weapon    → unreachable here: RL and LG are both
//	                                     in IT_DROPPABLE_WEAPONS, so a victim
//	                                     wielding one never trips it.
//	k_frp 1 / 2 (fairpacks)            → gated on the "Fairpacks setting:"
//	                                     broadcast (match.c:2086-2107)
//	k_yawnmode                         → gated: it rewrites the whole choice
//	                                     (last-fired weapon, DMM1 shotgun
//	                                     override, quartered ammo)
//
// # Provenance
//
// Reconstructed rows are stamped Source = "reconstructed" and carry no
// EntNum — the backpack's edict number is precisely what the wire never
// said, so they cannot join to WeaponPickup.BackpackEnt. A demo that
// carried hints is never touched: the hint is exact, and the two
// provenances are never mixed in one section.
//
// # Limitations
//
// `dp 0` (backpack drops switched off server-side) has no wire signal at
// all — no serverinfo key, no countdown row. On a hint-carrying demo the
// absence of hints settles it, which is why hintingEra below stands the
// pass down whenever the mod is new enough to have hinted; on a pre-1.38
// demo it is unfalsifiable, and a `dp 0` server would make this pass report
// drops that never happened. No archive demo in the validation sample
// showed the signature (see BACKPACKS.md).
func backpackReconPost(res *Result, co *CoreOutputs) {
	if len(res.Backpacks) > 0 {
		return
	}
	if reason := BackpackReconStandDown(res); reason != "" {
		return
	}
	drops := ReconstructBackpackDrops(res)
	if len(drops) == 0 {
		return
	}
	resolveBackpackLocs(res, drops)
	res.Backpacks = drops
}

// hasWireBackpacks reports whether the backpacks section came from the KTX
// `//ktx drop` hint rather than from this reconstruction — the test for
// "the wire named this pack", which is what any dropper-identity join
// (pack transfers, denial labelling) actually depends on.
func hasWireBackpacks(res *Result) bool {
	for i := range res.Backpacks {
		if res.Backpacks[i].Source == backpackSourceKTX {
			return true
		}
	}
	return false
}

// BackpackReconStandDown names the condition that makes reconstruction
// unmeasurable or wrong on this demo, or "" when it may proceed. Every arm
// is a refusal to fabricate, not a heuristic: see the doc comment above for
// which KTX rule each one mirrors.
//
// Exported alongside ReconstructBackpackDrops so the ground-truth harness
// (cmd/qw-backpack-eval) can score the reconstruction on hint-CARRYING
// demos: it reads the two apart, discounts the one stand-down that only
// exists because the hint is present ("hinting mod emitted no drops"), and
// compares the result against the hints the pipeline itself would have used.
func BackpackReconStandDown(res *Result) string {
	if res.Streams == nil || len(res.Streams.Players) == 0 {
		return "no player streams"
	}
	// The frag log is the only record of WHICH deaths were /kill commands.
	// Without it every reconstructed drop would be unconditioned on the one
	// early return that fires on real matches.
	if res.Frags == nil || len(res.Frags.Frags) == 0 {
		return "no frag log"
	}
	if !activeWeaponPresent(res.Streams.Players) {
		return "no active-weapon stat"
	}
	// Mirrors damagerecon's frozen-bits refusal: old recorders freeze the
	// StatItems weapon bits (a player "holds" RL from 0:00 through every
	// death), and a demo whose weapon state never moves cannot say what
	// anyone was wielding when they died.
	//
	// The OR is load-bearing, not a weakened AND. A demo where NO aw column
	// moves is not necessarily a frozen recording: in a single-weapon ruleset
	// — `1on1-lgc`, `2on2-midair`, rocket arena — nobody's wielded weapon can
	// change, and every player's column is one legitimate sample. The
	// StatItems bits still cycle there (armor, ammo, powerups), which is what
	// separates such a demo from a recorder that froze everything. Requiring
	// both would stand those rulesets down; measured, that costs real packs
	// the KTX hints confirm on every death (BACKPACKS.md).
	if !activeWeaponLive(res.Streams.Players) && !damagerecon.WeaponBitsLive(res.Streams.Players) {
		return "frozen weapon state"
	}
	var si map[string]string
	var ms *result.MatchSettings
	if res.Metadata != nil {
		si = res.Metadata.ServerInfo
		ms = res.Metadata.MatchSettings
	}
	// Mode gates BEFORE the hinting-era gate. The pipeline only reads
	// "is this empty", so the order is invisible to it — but the eval
	// harness discounts exactly one reason ("hinting mod emitted no drops",
	// the one that only exists because the hint is present) and scores every
	// other. With the era check first, a hint-era demo that is ALSO yawnmode
	// or fairpacks reported the discounted reason, so the mode gates were
	// bypassed in scoring and never exercised against ground truth at all.
	if r := backpackSkipModeReason(si, ms); r != "" {
		return "mode:" + r
	}
	if hintingEra(si) {
		// KTX >= 1.38 emits `//ktx drop` on every RL/LG pack. Reaching this
		// pass on such a demo means the wire said "no packs" — a
		// measurement, not a gap. Reconstructing over it would overwrite an
		// answer we already have (a `dp 0` server, an arena ruleset that
		// clears packs, or a match with no RL/LG death at all).
		return "hinting mod emitted no drops"
	}
	return ""
}

// ReconstructBackpackDrops replays DropBackpack's k_frp-0 choice over every
// in-match death: the victim's wielded weapon at the death instant, dropped
// at the victim's last broadcast position. Pure over the Result and blind to
// res.Backpacks, so the eval harness can run it with the wire hints
// withheld. Callers must consult BackpackReconStandDown first — this
// function assumes the evidence has already been found measurable.
func ReconstructBackpackDrops(res *Result) []result.BackpackDrop {
	players := res.Streams.Players
	// The stream's own Name carries a "#<slot>" suffix when two identities
	// render the same display name (disambiguatePlayerName) — a form that
	// appears in no frag log, scoreboard or playerStats row, so a drop
	// stamped with it would join to nothing. Undisambiguated, it is the same
	// display name ResolveSlotAt gives the hint path, which is what keeps
	// both provenances on one name vocabulary.
	joinName := streamJoinNames(players)
	killed := killCommandDeaths(res.Frags.Frags, players, joinName)
	var out []result.BackpackDrop
	for i := range players {
		p := &players[i]
		if len(p.ActiveWeapon) == 0 || len(p.Deaths) == 0 {
			continue
		}
		// There is deliberately NO per-player "this column never moves"
		// refusal here, and the demo-level gate above deliberately keeps its
		// `|| WeaponBitsLive` arm. A single-valued aw column is not the
		// frozen-stat signature it looks like: in a SINGLE-WEAPON ruleset the
		// wielded weapon cannot move. Measured on the ground-truth sample —
		// `1on1-lgc` and povdmm4 LG challenges, `2on2-midair`, rocket arena on
		// end/endif — every affected player carries exactly one sample
		// (`[{0,64}]` or `[{0,32}]`) and the KTX hints credit them with a pack
		// on EVERY death, hint count equal to death count. Refusing them cost
		// 58 of 13 749 ground-truth drops and prevented no fabrication
		// (precision unchanged at 99.97% either way). See BACKPACKS.md.
		for di, td := range p.Deaths {
			if killed[i][di] {
				continue
			}
			w, ok := activeWeaponAtDeath(p, td)
			if !ok {
				continue
			}
			weapon := backpackWeaponOfBit(int(w))
			if weapon == "" {
				continue
			}
			origin, ok := positionAtOrBefore(p.Position, td)
			if !ok {
				// No position within the staleness bound: KTX spawns the pack
				// at the victim's origin, and a drop without one would be a
				// guessed location on the map. Withheld, not centred.
				continue
			}
			origin[2] -= backpackDropZOffset
			out = append(out, result.BackpackDrop{
				Time:   td,
				Player: joinName[i],
				Team:   p.Team,
				Weapon: weapon,
				Origin: origin,
				Source: backpackSourceRecon,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		return out[i].Player < out[j].Player
	})
	return out
}

// backpackWeaponOfBit maps STAT_ACTIVEWEAPON to the two weapons the
// backpacks section covers. The comparison is equality, not a bit test,
// exactly as items.c:2762 writes it — the stat holds one IT_* bit.
func backpackWeaponOfBit(bit int) string {
	switch bit {
	case itemFlagRL:
		return "rl"
	case itemFlagLG:
		return "lg"
	}
	return ""
}

// suicideDeathTolMs bounds the join between a death marker (the fused
// DF_DEAD / STAT_HEALTH / obituary detector, mvd-reader/parser/stats.go) and
// its obituary line. They are the same server frame; the tolerance absorbs
// the demo-frame quantisation between the two carriers.
const suicideDeathTolMs = 500

// killCommandDeaths marks, per player stream, which of that stream's death
// markers were the /kill command — the ONLY deathtype DropBackpack refuses
// (dtSUICIDE, ktx/src/client.c:1008, obituary " suicides"). Every other
// self-inflicted death still drops a pack.
//
// The frag log names its victim and nothing else — no slot, no userid — so
// the join is on the display name and a ±suicideDeathTolMs window. What the
// accounting adds is that each obituary is consumed AT MOST ONCE, by the
// nearest death marker in its window. A membership test instead of an
// assignment costs a real pack in two shapes that both occur:
//
//   - Two identities rendering the same display name (the "#<slot>" streams)
//     dying within the window of one another. One /kill obituary suppressed
//     BOTH deaths, including the other player's genuine RL death.
//   - A /kill followed by an instant respawn (ktx/src/client.c:2594-2597) and
//     a genuine death inside the same window. The /kill suppressed both its
//     own death and the real one.
//
// Assignment is nearest-first over all (obituary, death) pairs in range, so
// the /kill lands on the death it actually was and the other death keeps its
// pack.
func killCommandDeaths(frags []result.FragEntry, players []result.PlayerStream, joinName []string) [][]bool {
	out := make([][]bool, len(players))
	for i := range players {
		out[i] = make([]bool, len(players[i].Deaths))
	}
	byVictim := map[string][]int32{}
	for i := range frags {
		f := &frags[i]
		// "suicide" is the obituary vocabulary's token for " suicides"
		// (dtSUICIDE) — and, on a KTX that reached its unreachable else
		// branch, for " somehow becomes bored with life" (a death that DOES
		// drop). Conflating those two costs at most that never-observed
		// branch; conflating dtSUICIDE with the rest would cost every real
		// /kill.
		if f.IsSuicide && f.Weapon == "suicide" {
			byVictim[f.Victim] = append(byVictim[f.Victim], f.Time)
		}
	}
	if len(byVictim) == 0 {
		return out
	}
	for _, ts := range byVictim {
		sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	}

	type pairing struct {
		victim       string
		obit, pi, di int
		dt           int32
	}
	var cands []pairing
	for pi := range players {
		victim := joinName[pi]
		for obit, kt := range byVictim[victim] {
			for di, td := range players[pi].Deaths {
				if dt := abs32(kt - td); dt <= suicideDeathTolMs {
					cands = append(cands, pairing{victim, obit, pi, di, dt})
				}
			}
		}
	}
	// Nearest first; the rest of the key only makes the walk deterministic.
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.dt != b.dt {
			return a.dt < b.dt
		}
		if a.pi != b.pi {
			return a.pi < b.pi
		}
		if a.di != b.di {
			return a.di < b.di
		}
		if a.victim != b.victim {
			return a.victim < b.victim
		}
		return a.obit < b.obit
	})
	used := map[string][]bool{}
	for v, ts := range byVictim {
		used[v] = make([]bool, len(ts))
	}
	for _, c := range cands {
		if used[c.victim][c.obit] || out[c.pi][c.di] {
			continue
		}
		used[c.victim][c.obit] = true
		out[c.pi][c.di] = true
	}
	return out
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// streamJoinNames returns, per stream, the display name every other section
// of the Result joins on: the stream's own Name with a "#<slot>"
// disambiguation suffix removed, and left alone otherwise.
//
// The suffix is only stripped where it is demonstrably one of ours. Two
// signals say so, and either is enough:
//
//   - another stream carries the same base name with its own "#<digits>"
//     suffix — disambiguatePlayerName suffixes EVERY colliding identity, so
//     they normally come in pairs; and
//   - the suffix names a wire slot this stream actually occupied — the suffix
//     disambiguatePlayerName appends is the identity's representative slot.
//
// Either alone covers a case the other misses (a colliding identity whose
// stream was dropped as empty leaves its partner alone; an identity whose
// representative slot published no session of its own carries a suffix that
// is in no session list). A player genuinely named "foo#2" who matches
// neither keeps their name, which is the point: stripping it unconditionally
// renamed them to "foo" and joined them to nothing.
func streamJoinNames(players []result.PlayerStream) []string {
	suffixed := map[string]int{}
	for i := range players {
		if base := undisambiguatedName(players[i].Name); base != players[i].Name {
			suffixed[base]++
		}
	}
	out := make([]string, len(players))
	for i := range players {
		p := &players[i]
		out[i] = p.Name
		base := undisambiguatedName(p.Name)
		if base == p.Name {
			continue
		}
		if suffixed[base] > 1 || suffixNamesOwnSlot(p) {
			out[i] = base
		}
	}
	return out
}

// suffixNamesOwnSlot reports whether the stream's "#<digits>" suffix is a
// wire slot the stream itself occupied.
func suffixNamesOwnSlot(p *result.PlayerStream) bool {
	i := strings.LastIndexByte(p.Name, '#')
	if i < 0 {
		return false
	}
	slot, err := strconv.Atoi(p.Name[i+1:])
	if err != nil {
		return false
	}
	for _, s := range p.Sessions {
		if s.Slot == slot {
			return true
		}
	}
	return false
}

// undisambiguatedName strips a trailing "#<digits>" slot suffix, the only
// form disambiguatePlayerName produces. Whether a given name's suffix IS one
// of those is streamJoinNames's question, not this helper's.
func undisambiguatedName(name string) string {
	i := strings.LastIndexByte(name, '#')
	if i <= 0 || i == len(name)-1 {
		return name
	}
	for _, c := range name[i+1:] {
		if c < '0' || c > '9' {
			return name
		}
	}
	return name[:i]
}

// backpackPosStaleMs bounds how old the victim's last broadcast position may
// be to stand in for the drop origin. MVD position updates land every demo
// frame (20-30 Hz on the archive, 77 Hz on modern servers), so a gap beyond
// this means the track genuinely stopped — a disconnect, a recording gap, or
// a player the recorder never carried.
const backpackPosStaleMs = 400

// positionAtOrBefore returns the last broadcast origin at or before t,
// mirroring what the hint path records (BackpackAnalyzer keeps the dropper's
// most recent PlayerPositionEvent). Reports false when the track is absent
// or its newest sample before t is staler than backpackPosStaleMs.
func positionAtOrBefore(pt *result.PositionTrack, t int32) ([3]float32, bool) {
	var zero [3]float32
	if pt == nil || len(pt.T) == 0 {
		return zero, false
	}
	i := sort.Search(len(pt.T), func(j int) bool { return pt.T[j] > t }) - 1
	if i < 0 || t-pt.T[i] > backpackPosStaleMs {
		return zero, false
	}
	return [3]float32{pt.X[i], pt.Y[i], pt.Z[i]}, true
}

// activeWeaponAtDeath returns the wielded weapon in force at the death (the
// last transition at or before it; the column is ascending by construction),
// refusing a sample the player carried on a DIFFERENT wire slot.
//
// The bound is the slot, not a time window like positionAtOrBefore's,
// because the slot is what the staleness mechanism is keyed on. mvdsv
// delta-codes stats against a per-slot cache that no client change resets
// (`demo.stats[i][j]`, sv_send.c:1279-1281), so a player who reconnects onto
// a slot whose previous occupant held what they now hold gets no
// svc_updatestat at all — and their merged stream, which stitches both
// connections together, would otherwise answer the question with a weapon
// they were holding minutes ago on the slot they left. No staleness
// tolerance separates those: the earlier session's last sample can be
// seconds old and still be the newest one there is.
//
// A reconnect onto the SAME slot is not that case: the cache is continuous
// across it, so the earlier sample is the same client's own last report and
// is trusted. The occupancy tracker opens a fresh session on a same-slot
// userinfo change too, so a session-keyed bound would refuse samples the
// mechanism it cites does not implicate. (Neither form costs anything on the
// ground-truth sample — measured identical with the session bound and with
// the slot bound — so this is the narrower rule chosen on the engine
// behaviour, not on a number.)
func activeWeaponAtDeath(p *result.PlayerStream, t int32) (int16, bool) {
	col := p.ActiveWeapon
	i := sort.Search(len(col), func(j int) bool { return col[j].T > t }) - 1
	if i < 0 {
		return 0, false
	}
	if len(p.Sessions) > 1 {
		deathSlot, ok1 := sessionSlotAt(p.Sessions, t)
		sampleSlot, ok2 := sessionSlotAt(p.Sessions, col[i].T)
		if ok1 && ok2 && deathSlot != sampleSlot {
			return 0, false
		}
	}
	return col[i].V, true
}

// sessionSlotAt returns the wire slot of the latest published connection that
// had begun by t, and whether there was one. A t before every published
// connection reports none — the sample predates the occupancy record and
// cannot be placed, which is not the same as being placed elsewhere.
func sessionSlotAt(sessions []result.PlayerSession, t int32) (int, bool) {
	best, slot, ok := int32(0), 0, false
	for i := range sessions {
		if s := sessions[i].StartMs; s <= t && (!ok || s > best) {
			best, slot, ok = s, sessions[i].Slot, true
		}
	}
	return slot, ok
}

// activeWeaponPresent reports whether any player carries the active-weapon
// column at all — false on a recorder that never wrote STAT_ACTIVEWEAPON.
func activeWeaponPresent(players []result.PlayerStream) bool {
	for i := range players {
		if len(players[i].ActiveWeapon) > 0 {
			return true
		}
	}
	return false
}

// activeWeaponLive reports whether the wielded-weapon stat actually MOVES on
// this demo. One sample per player and nothing after is the frozen-stat
// signature (see damagerecon.WeaponBitsLive for the same refusal on the
// inventory bits): a player who dies respawns holding the shotgun, so a live
// recording shows transitions.
func activeWeaponLive(players []result.PlayerStream) bool {
	for i := range players {
		if len(players[i].ActiveWeapon) > 1 {
			return true
		}
	}
	return false
}

// ktxDropHintVersion is the KTX version that introduced the `//ktx drop`
// STUFFCMD_DEMOONLY hint (major*100+minor, so 1.38 → 138).
const ktxDropHintVersion = 138

// hintingEra reports whether this demo's mod emits `//ktx drop` itself. On
// such a demo an empty backpacks section is the wire's answer, not a gap.
// Forks overstate their version (survey §"ktxver is the sharp feature
// gate"), which only makes this MORE conservative: an overstated version
// stands the reconstruction down.
func hintingEra(si map[string]string) bool {
	v := ktxVersionNumber(si["ktxver"])
	return v > 0 && v >= ktxDropHintVersion
}

// ktxVersionNumber parses the leading `<major>.<minor>` of a ktxver string
// ("1.46-dev-r402", "1.40-beta-quakecon-release3") into major*100+minor.
// Returns 0 when the key is absent or unparseable — a pre-KTX or
// non-KTX mod, which is exactly the population this pass exists for.
func ktxVersionNumber(s string) int {
	if s == "" {
		return 0
	}
	digits := func(str string) (int, int) {
		n, k := 0, 0
		for k < len(str) && str[k] >= '0' && str[k] <= '9' {
			n = n*10 + int(str[k]-'0')
			k++
		}
		return n, k
	}
	major, k := digits(s)
	if k == 0 || k >= len(s) || s[k] != '.' {
		return 0
	}
	minor, m := digits(s[k+1:])
	if m == 0 {
		return 0
	}
	// A one-digit minor is a tenths field ("1.4" is 1.40, not 1.04).
	if m == 1 {
		minor *= 10
	}
	return major*100 + minor
}

// backpackSkipModeReason names the server mode that makes DropBackpack's
// choice unreproducible, or "" under the standard ruleset. It is
// deliberately NARROWER than damagerecon.SkipModeReason: midair, instagib
// and dmgfrags rewrite T_Damage but leave DropBackpack untouched (there is
// no such early return in items.c), and gating on them would withhold drops
// KTX demonstrably makes. The shared machinery is still the source of the
// mode tokens — this reads the same serverinfo `mode` string that
// SetMode4ServerInfo (ktx/src/world.c:1475-1541) builds and the same
// countdown-derived MatchSettings.
func backpackSkipModeReason(si map[string]string, ms *result.MatchSettings) string {
	for _, m := range [...]struct{ cvar, mode string }{
		{"k_bloodfest", "bloodfest"},
		{"k_yawnmode", "yawnmode"},
	} {
		if v := si[m.cvar]; v != "" && v != "0" {
			return m.mode
		}
	}
	for _, sub := range strings.Split(si["mode"], "-") {
		switch sub {
		case "bf":
			return "bloodfest"
		case "yw":
			return "yawnmode"
		}
	}
	if ms != nil {
		if ms.Yawnmode {
			return "yawnmode"
		}
		// "Fairpacks setting: best weapon" / "last weapon fired" — KTX
		// broadcasts this row only when k_frp is NOT the default 0
		// (ktx/src/match.c:2086-2107), so its presence alone means the pack
		// contents follow a different rule than the one reproduced here.
		if ms.Fairpacks != "" {
			return "fairpacks"
		}
	}
	return ""
}

// resolveBackpackLocs fills Loc from the map's .loc corpus, mirroring what
// BackpackAnalyzer.Finalize does for hint-derived rows so both provenances
// carry the same fields.
func resolveBackpackLocs(res *Result, drops []result.BackpackDrop) {
	mapName := ""
	if res.Metadata != nil {
		mapName = res.Metadata.ServerInfo["map"]
	}
	if mapName == "" {
		return
	}
	f, err := locvis.LoadForMap(mapName)
	if err != nil || f == nil {
		return
	}
	for i := range drops {
		drops[i].Loc = f.FindNearest(drops[i].Origin[0], drops[i].Origin[1], drops[i].Origin[2])
	}
}
