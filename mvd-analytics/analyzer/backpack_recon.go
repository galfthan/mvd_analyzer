package analyzer

import (
	"sort"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/damagerecon"
	"github.com/mvd-analyzer/mvd-analytics/locvis"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Package-scope aliases for the Source vocabulary: BackpackAnalyzer.Finalize
// shadows the `result` qualifier with its parameter name (same reason
// analyzer/damage_recon.go has damageSourceKTX).
const (
	backpackSourceKTX   = result.BackpackSourceKTX
	backpackSourceRecon = result.BackpackSourceReconstructed
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
	if !activeWeaponLive(res.Streams.Players) && !damagerecon.WeaponBitsLive(res.Streams.Players) {
		return "frozen weapon state"
	}
	var si map[string]string
	var ms *result.MatchSettings
	if res.Metadata != nil {
		si = res.Metadata.ServerInfo
		ms = res.Metadata.MatchSettings
	}
	if hintingEra(si) {
		// KTX >= 1.38 emits `//ktx drop` on every RL/LG pack. Reaching this
		// pass on such a demo means the wire said "no packs" — a
		// measurement, not a gap. Reconstructing over it would overwrite an
		// answer we already have (a `dp 0` server, an arena ruleset that
		// clears packs, or a match with no RL/LG death at all).
		return "hinting mod emitted no drops"
	}
	if r := backpackSkipModeReason(si, ms); r != "" {
		return "mode:" + r
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
	killCmd := suicideCommandDeaths(res.Frags.Frags)
	var out []result.BackpackDrop
	for i := range res.Streams.Players {
		p := &res.Streams.Players[i]
		if len(p.ActiveWeapon) == 0 || len(p.Deaths) == 0 {
			continue
		}
		for _, td := range p.Deaths {
			if killCmd.has(p.Name, td) {
				continue
			}
			w, ok := changeI16AtOrBefore(p.ActiveWeapon, td)
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
			out = append(out, result.BackpackDrop{
				Time: td,
				// The stream's own Name carries a "#<slot>" suffix when two
				// identities render the same display name
				// (disambiguatePlayerName) — a form that appears in no frag
				// log, scoreboard or playerStats row, so a drop stamped with
				// it would join to nothing. Undisambiguated, it is the same
				// display name ResolveSlotAt gives the hint path, which is
				// what keeps both provenances on one name vocabulary.
				Player: undisambiguatedName(p.Name),
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

// killCmdDeaths indexes the /kill deaths — the ONLY deathtype DropBackpack
// refuses (dtSUICIDE, ktx/src/client.c:1008, obituary " suicides"). Every
// other self-inflicted death still drops a pack.
type killCmdDeaths map[string][]int32

func suicideCommandDeaths(frags []result.FragEntry) killCmdDeaths {
	out := killCmdDeaths{}
	for i := range frags {
		f := &frags[i]
		// "suicide" is the obituary vocabulary's token for " suicides"
		// (dtSUICIDE) — and, on a KTX that reached its unreachable else
		// branch, for " somehow becomes bored with life" (a death that DOES
		// drop). Conflating those two costs at most that never-observed
		// branch; conflating dtSUICIDE with the rest would cost every real
		// /kill.
		if f.IsSuicide && f.Weapon == "suicide" {
			out[f.Victim] = append(out[f.Victim], f.Time)
		}
	}
	for _, ts := range out {
		sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	}
	return out
}

// has reports whether the player /killed at t. It tries the stream's own
// name and then its undisambiguated form: a stream row whose display name
// collides with another identity's is suffixed "#<slot>"
// (disambiguatePlayerName), and that suffixed form appears in no obituary,
// so a strict lookup would silently stop suppressing /kill drops for exactly
// the players whose streams got split.
func (k killCmdDeaths) has(player string, t int32) bool {
	if k.hit(player, t) {
		return true
	}
	if base := undisambiguatedName(player); base != player {
		return k.hit(base, t)
	}
	return false
}

func (k killCmdDeaths) hit(player string, t int32) bool {
	ts := k[player]
	i := sort.Search(len(ts), func(j int) bool { return ts[j] >= t-suicideDeathTolMs })
	return i < len(ts) && ts[i] <= t+suicideDeathTolMs
}

// undisambiguatedName strips a trailing "#<digits>" slot suffix, the only
// form disambiguatePlayerName produces. A name that genuinely ends in
// "#<digits>" is returned unchanged only by luck — which costs nothing here,
// since the fallback is tried in ADDITION to the exact name.
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

// changeI16AtOrBefore returns the value in force at t (the last transition
// at or before it). The column is ascending by construction.
func changeI16AtOrBefore(col []result.ChangeI16, t int32) (int16, bool) {
	i := sort.Search(len(col), func(j int) bool { return col[j].T > t }) - 1
	if i < 0 {
		return 0, false
	}
	return col[i].V, true
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
