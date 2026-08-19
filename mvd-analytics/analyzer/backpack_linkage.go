package analyzer

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// backpackLinkagePost reads the FATE of every reconstructed backpack drop off
// the wire's own backpack-entity track: who took it, or that it lay there
// until KTX removed it.
//
// # Why the entity track is usable now
//
// The plan for this work recorded the entity stream as unusable — "measured
// 16.4 visibility flips per real pack". That measurement was of a parser that
// cached an edict's item kind forever (mvd-reader/parser/entities.go
// diffItemEntity). KTX `spawn()`s and `ent_remove()`s packs, so a pack edict
// is recycled within seconds — as the next pack, as a rocket, as a gib — and
// every later tenant's appearance and disappearance was reported as the
// original pack flickering. With the model index re-read on every visible
// frame, a pack's life is one appearance and one disappearance: measured over
// 24 hint-carrying demos, 3 205 pack lives against 3 319 deaths, and ZERO
// lives re-opening at the position the previous one ended at. There is no
// flutter left to stitch (BACKPACKS.md).
//
// # The chain
//
//  1. BIND. Each reconstructed drop names a time and a position (the victim's
//     origin less 24, which is where DropBackpack puts the pack —
//     ktx/src/items.c:2703). The pack that appears at that instant nearest to
//     that point is the drop's pack. Scored against the `//ktx drop` hint's
//     own edict number on 24 demos: 947 of 961 bound, and every one of the
//     947 to the edict the hint named — zero wrong bindings.
//  2. FOLLOW. A pack is tossed (`velocity[2] = 300` plus a random horizontal
//     kick, items.c:2856-2858) with MOVETYPE_TOSS, so it falls off ledges and
//     down lift shafts before settling: measured 58 units of travel at p50,
//     422 at p99, 583 at max. PackEntityLife.Rest is where it actually ended
//     up, tracked through ItemMoveEvent.
//  3. READ THE DISAPPEARANCE. A pack leaves the wire for two reasons in a
//     normal match: BackpackTouch removed it (items.c:2367) or SUB_Remove did
//     at the 120 s timeout (items.c:2871-2872). Which one is decided by
//     whether any LIVE, mode-eligible player's bounding box overlapped the
//     pack's at that instant — the same test the server ran before calling
//     BackpackTouch (see packTouch, packTouchRules). Hoony mode adds a third
//     that is neither: it wipes every pack on its per-point reset
//     (remove_items("backpack"), hoonymode.c:376) through ent_remove rather
//     than SUB_Remove, so those vanish mid-life with nobody on them and stay
//     unobserved.
//
// # Why touch, and not a stat flip
//
// `//ktx bp` fires on every RL/LG pack pickup regardless of what the picker
// already held (items.c:2489-2494), and `other->s.v.items |= new` cannot
// change a bit the picker already had. Measured: only 237 of 606
// unambiguously-attributed ground-truth pickups came with a weapon-bit gain
// — a stat-flip requirement would have discarded 61% of real pickups. The
// bounding-box overlap is not a proxy for the touch, it IS the touch, so it
// is the primary signal and the bit gain is used only to separate two players
// standing on one pack.
func backpackLinkagePost(res *Result, co *CoreOutputs) {
	if res.Streams == nil || len(res.Backpacks) == 0 {
		return
	}
	// Hint-carrying demos are left alone, for the same reason the
	// reconstruction leaves them alone: `//ktx bp` names the picker outright
	// and reaches the consumer as a weaponPickups row. A second, weaker
	// answer beside it could only disagree.
	if hasWireBackpacks(res) {
		return
	}
	LinkBackpackDrops(res, co.PackEntities, res.Backpacks)
}

// LinkBackpackDrops stamps Fate / EntNum / Picker / PickerTeam / PickupTime
// on every reconstructed row of drops, reading the pack track and the player
// streams of res. Pure over its inputs and blind to any wire pickup hint, so
// the ground-truth harness (cmd/qw-backpack-eval) can run it with the hints
// withheld and score it against them.
//
// drops is mutated in place. Rows that are not BackpackSourceReconstructed
// are skipped: their fate is the weaponPickups join, which this function has
// no business restating.
func LinkBackpackDrops(res *Result, packs []PackEntityLife, drops []result.BackpackDrop) {
	players := res.Streams.Players
	joinName := streamJoinNames(players)
	spawners := weaponSpawnerPositions(res)
	rules := packTouchRulesOf(res)
	cadence := broadcastCadenceMs(players)

	// Bind nearest-first over all (drop, pack) pairs in the time window, so a
	// pack that two drops both reach goes to the one it is actually at. A
	// greedy walk in drop order would let an earlier drop claim a pack that
	// sits 60 units away when the later drop is on top of it.
	type cand struct {
		di, pi int
		d      float64
	}
	var cands []cand
	for di := range drops {
		if drops[di].Source != backpackSourceRecon {
			continue
		}
		drops[di].Fate = result.BackpackFateUnobserved
		for pi := range packs {
			dt := packs[pi].Start - drops[di].Time
			if dt < -packBindWindowMs || dt > packBindWindowMs {
				continue
			}
			d := vecDist(packs[pi].Spawn, drops[di].Origin)
			if d > packBindMaxDist {
				continue
			}
			cands = append(cands, cand{di, pi, d})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.d != b.d {
			return a.d < b.d
		}
		if a.di != b.di {
			return a.di < b.di
		}
		return a.pi < b.pi
	})
	usedDrop := make(map[int]bool, len(drops))
	usedPack := make(map[int]bool, len(packs))
	for _, c := range cands {
		if usedDrop[c.di] || usedPack[c.pi] {
			continue
		}
		usedDrop[c.di], usedPack[c.pi] = true, true
		classifyPackFate(&drops[c.di], &packs[c.pi], players, joinName, spawners, rules, cadence)
	}
}

// classifyPackFate fills the fate fields of one bound drop.
func classifyPackFate(drop *result.BackpackDrop, pack *PackEntityLife, players []result.PlayerStream, joinName []string, spawners map[string][][3]float32, rules packTouchRules, cadence int32) {
	drop.EntNum = pack.EntNum
	if !pack.Ended {
		// Still on the wire when the recording stopped. Not expired, not
		// taken — the evidence ran out first.
		return
	}
	touchers := packTouchers(pack, players, rules)
	// KTX arms SUB_Remove for creation + 120 s when the pack is dropped and
	// nothing in items.c ever re-arms it, so a pack that reaches that age was
	// going to leave the wire in that frame whatever was standing on it. At
	// that age the timeout is the explanation and a touch has to outrank it,
	// which a merely-swept path cannot: see packExpiryTimeoutMs.
	atTimeout := pack.End-pack.Start >= packExpiryTimeoutMs-packExpirySlackFrames*cadence
	if atTimeout && len(touchers) > 0 {
		touchers = packSampledTouchers(pack, players, rules, touchers)
	}
	if len(touchers) == 0 {
		// SUB_Remove at KTX's 120 s timeout with nobody on the pack is the
		// one disappearance that is positively NOT a pickup. Anything else —
		// a pack that vanishes at 8 s with no player anywhere near it — is
		// something the wire did not explain, and saying "expired" there
		// would be inventing the explanation.
		if atTimeout {
			drop.Fate = result.BackpackFateExpired
		}
		return
	}
	drop.Fate = result.BackpackFatePicked
	drop.PickupTime = pack.End
	pi := touchers[0]
	if len(touchers) > 1 {
		pi = -1
		for _, ti := range touchers {
			if !gainedWeaponAt(&players[ti], drop.Weapon, pack.End) {
				continue
			}
			if nearWeaponSpawner(&players[ti], drop.Weapon, pack.End, spawners) {
				// The bit could have come from the pad they are standing on
				// rather than from this pack, so it separates nothing.
				continue
			}
			if pi >= 0 {
				pi = -1 // two gains; the evidence names nobody
				break
			}
			pi = ti
		}
	}
	if pi < 0 {
		return
	}
	drop.Picker = joinName[pi]
	drop.PickerTeam = players[pi].Team
}

const (
	// packBindWindowMs bounds drop-time to pack-appearance time. KTX spawns
	// the pack inside DropBackpack, which PlayerDie calls in the same server
	// frame as the death the reconstruction keys on, so the true offset is
	// zero: measured 0 ms at p50, p99 and max across 947 ground-truth binds.
	// The window is a tolerance for demo-frame quantisation, not a search
	// radius.
	packBindWindowMs = 200
	// packBindMaxDist bounds the distance between the reconstructed drop
	// position and the pack's first broadcast origin. Both are the victim's
	// origin less 24 read one frame apart, so the gap is one frame of the
	// victim's movement: measured 4.7 units at p50, 23.3 at p99, 28.3 at max.
	// The cap is a refusal ("no pack appeared where this player died"), set
	// well clear of the measured tail — binding is decided by nearest-wins,
	// not by this number.
	packBindMaxDist = 128
	// packExpiryTimeoutMs is KTX's own removal timeout for a player-dropped
	// pack, verbatim:
	//
	//	item->s.v.nextthink = g_globalvars.time + (self->ct == ctPlayer ? 120 : 30);
	//	item->think = (func_t) SUB_Remove;   // ktx/src/items.c:2871-2872
	//
	// Verified against the whole of DropBackpack (items.c:2667-2885): the
	// line is unconditional and nothing re-arms nextthink afterwards. The
	// 30 s arm is for a MONSTER-dropped pack (`self->ct == ctPlayer` is
	// false) and cannot apply here — every reconstructed drop keys on a
	// player death, and the one KTX mode with monsters, bloodfest, is stood
	// down before the reconstruction runs (backpackSkipModeReason). Yawnmode
	// rewrites the pack's CONTENTS and its ammo caps (items.c:2754, 2820-2827)
	// but not its lifetime, and is stood down as well.
	//
	// The tolerance around it is derived from the demo's own broadcast
	// cadence rather than picked as a flat slack — see packExpirySlackFrames.
	packExpiryTimeoutMs = 120000
	// packExpirySlackFrames is how many broadcast intervals a measured
	// lifetime may fall short of packExpiryTimeoutMs and still BE the
	// timeout. Three, and each one is a distinct quantisation:
	//
	//  1. Start is the first frame the pack was VISIBLE, up to a frame after
	//     the spawn that armed nextthink.
	//  2. End is a frame boundary too, so the removal lands anywhere within
	//     one frame of it.
	//  3. `nextthink` fires on the first SERVER frame at or after the
	//     deadline, which is its own tick, never exactly 120.000 s.
	//
	// Measured on the ground truth (223 demos, 10 378 scored drops), the two
	// classes are cleanly separated and this sits in the gap with room on
	// both sides: pack lifetimes for drops the wire says were PICKED reach
	// 117 815 ms and not one of the 9 793 reaches 118 000, while 189 of the
	// 190 never-picked ones land at or above 119 900 (p50 119 995, max
	// 120 027). Any threshold between those two figures scores identically;
	// what the cadence buys is that the SAME rule holds at 13 ms per frame
	// and at 40 ms, instead of a constant tuned to one of them.
	packExpirySlackFrames = 3
	// packCadenceDefaultMs is the broadcast interval assumed when the demo
	// carries no position track to measure one from — sv_demofps 30, one
	// frame (mvdsv/src/sv_send.c:1339-1346).
	packCadenceDefaultMs = 34
	// packCadenceMaxMs caps the derived interval. A demo whose median
	// position gap is larger than this is not recording at a frame rate, it
	// has a hole, and widening the expiry window to match would let a pack
	// that left the wire seconds early be called expired.
	packCadenceMaxMs = 100
	// packPosStaleMs bounds how old a player's position sample may be to
	// stand in for where they were when the pack left the wire. One frame is
	// 13-40 ms depending on sv_demofps; beyond this the track has a hole and
	// the player cannot be placed at all.
	packPosStaleMs = 200
	// packBitGainPreMs / packBitGainPostMs bracket the weapon-bit gain that
	// separates two players on one pack: the picker must have LACKED the
	// weapon shortly before and hold it shortly after. Wider on the late side
	// because STAT_ITEMS is delta-coded and reaches the demo on the next
	// frame the server writes stats.
	packBitGainPreMs  = 200
	packBitGainPostMs = 500
)

// Player and backpack bounding boxes, and the expansion the server applies
// before testing them against each other. A pack is SOLID_TRIGGER with
// FL_ITEM and setsize(-16,-16,0, 16,16,56) (ktx/src/items.c:2864-2866); a
// player is setsize(-16,-16,-24, 16,16,32). SV_LinkEdict expands an FL_ITEM's
// abs box by 15 units in x and y "to make items easier to pick up and allow
// them to be grabbed off of shelves" and every other entity's by 1 on all
// three axes (mvdsv/src/sv_world.c:365-389); SV_TouchLinks then calls
// BackpackTouch for every trigger whose abs box overlaps the moving player's
// (sv_world.c:298-326). So the touch predicate is the overlap of
//
//	player origin + (-17,-17,-25) .. (17,17,33)
//	pack   origin + (-31,-31,  0) .. (31,31,56)
//
// which reduces to the three bounds below. The 15-unit expansion is not a
// detail: without it the predicate finds nobody on 90% of real pickups
// (measured), because the two origins are legitimately up to 66 units apart.
const (
	packTouchMaxXY = 48  // |pack.x - player.x| and |pack.y - player.y|
	packTouchMinDZ = -81 // pack.z - player.z, exclusive
	packTouchMaxDZ = 33  // pack.z - player.z, exclusive
)

// packTouch reports whether a player at playerPos was overlapping a pack at
// packPos, i.e. whether the server would have run BackpackTouch.
func packTouch(packPos, playerPos [3]float32) bool {
	if math.Abs(float64(packPos[0]-playerPos[0])) >= packTouchMaxXY {
		return false
	}
	if math.Abs(float64(packPos[1]-playerPos[1])) >= packTouchMaxXY {
		return false
	}
	dz := packPos[2] - playerPos[2]
	return dz > packTouchMinDZ && dz < packTouchMaxDZ
}

// packTouchRules is the MODE-specific half of KTX's BackpackTouch guard.
// BackpackTouch does not just reject dead touchers — it rejects a live one
// whose current state the ruleset says may not take a pack
// (ktx/src/items.c:2393-2425):
//
//	if (deathmatch == 4) { if (other->invincible_finished) return; }   // pent
//	if (isRA() && !isWinner(other) && !isLoser(other)) return;         // waiting area
//	if (cvar("k_midair") && other->super_damage_finished) return;      // quad
//	if (cvar("k_instagib") && other->invisible_finished) return;       // ring
//	if ((deathmatch == 4) && lgc_enabled() && (other->s.v.health >= 300)) return;
//
// Each rejection leaves the pack ON the wire, so a replay that ignores them
// credits a pickup to a player the server refused — and, worse, stops looking
// for the player who actually took it. Four of the five are reproducible
// because the state they read is broadcast: `invincible_finished`,
// `super_damage_finished` and `invisible_finished` are set and cleared in
// lockstep with the IT_INVULNERABILITY / IT_QUAD / IT_INVISIBILITY bits
// (ktx/src/client.c:2283-2289, 4062-4174), which is exactly what the Pent /
// Quad / Ring possession intervals are derived from, and health is its own
// stream.
//
// The FIFTH, RA's waiting area, is not reproducible and is deliberately not
// guessed at: `isWinner` / `isLoser` are arena bookkeeping with no wire
// representation anywhere — no stat, no serverinfo key, no print — so there
// is no per-player, per-instant answer to "was this player fighting or
// queueing". Demoting every RA pack to `unobserved` was the alternative and
// is worse than leaving the rule out: it would erase the mode's entire pickup
// signal to guard against a class the geometry mostly handles anyway (the
// waiting area is a separate room from the arena floor where packs drop), and
// the ground-truth population carries no RA demo to measure either choice on.
// The residual is recorded in BACKPACKS.md rather than papered over.
type packTouchRules struct {
	dmm4     bool // deathmatch 4 — pent carriers may not take a pack
	midair   bool // k_midair — quad carriers may not
	instagib bool // k_instagib — ring carriers may not
	lgc      bool // k_lgcmode — with dmm4, players at >= 300 health may not
}

// any reports whether any rule is in force, so the common case (every
// standard ruleset) costs nothing per toucher.
func (r packTouchRules) any() bool { return r.dmm4 || r.midair || r.instagib }

// packTouchRulesOf reads the rules off the demo's serverinfo and the
// countdown-derived MatchSettings — the same two sources
// backpackSkipModeReason and damagerecon.SkipModeReasonFull read, and for the
// same reason: a cvar published directly is authoritative, the composite
// `mode` string SetMode4ServerInfo builds (ktx/src/world.c:1475-1543) is the
// fallback for servers that publish no cvar, and MatchSettings is what a demo
// with neither still shows in its countdown.
func packTouchRulesOf(res *Result) packTouchRules {
	var r packTouchRules
	if res.Metadata == nil {
		return r
	}
	si := res.Metadata.ServerInfo
	on := func(key string) bool { v := si[key]; return v != "" && v != "0" }
	r.midair, r.instagib = on("k_midair"), on("k_instagib")
	r.lgc = on("k_lgcmode")
	for _, sub := range strings.Split(si["mode"], "-") {
		switch sub {
		case "midair":
			r.midair = true
		case "instagib":
			r.instagib = true
		case "lgc":
			r.lgc = true
		}
	}
	r.dmm4 = si["deathmatch"] == "4"
	if ms := res.Metadata.MatchSettings; ms != nil {
		r.midair = r.midair || ms.Midair
		r.instagib = r.instagib || ms.Instagib
		if ms.Deathmatch == 4 {
			r.dmm4 = true
		}
		if strings.EqualFold(ms.Mode, "lgc") {
			r.lgc = true
		}
	}
	return r
}

// packEligible replays the mode-specific rejections of BackpackTouch for one
// player at the instant the pack left the wire. False means the server would
// have returned without taking the pack, so this player cannot be its picker
// however well their box overlapped.
func packEligible(p *result.PlayerStream, t int32, r packTouchRules) bool {
	if r.dmm4 && packHeldAt(p.Pent, t) {
		return false // items.c:2398 — "we have pent, ignore pack"
	}
	if r.midair && packHeldAt(p.Quad, t) {
		return false // items.c:2412 — "we have quad, ignore pack"
	}
	if r.instagib && packHeldAt(p.Ring, t) {
		return false // items.c:2417 — "we have ring, ignore pack"
	}
	if r.dmm4 && r.lgc {
		// items.c:2422 — "don't allow bonus powers, leave pack hanging
		// around". The health read is the pre-touch value by construction:
		// the rejection means no touch ran, so nothing moved health in that
		// frame.
		if h, ok := packHealthAt(p.Health, t); ok && h >= 300 {
			return false
		}
	}
	return true
}

// packHealthAt reads the health stream at t — the last change at or before
// it. The stream is sparse and deduped, so a value holds until the next
// change; ok is false only when the player has no health sample at or before
// t at all, in which case the LGC rule cannot be evaluated and the player is
// left eligible rather than excluded on an unmeasured field.
func packHealthAt(h []result.ChangeI16, t int32) (int16, bool) {
	i := sort.Search(len(h), func(i int) bool { return h[i].T > t }) - 1
	if i < 0 {
		return 0, false
	}
	return h[i].V, true
}

// broadcastCadenceMs estimates one demo frame in milliseconds from the
// players' own position tracks — the median inter-sample gap of the longest
// track. The recording cadence is server-configured, not fixed: mvdsv gates
// whole demo frames on sv_demofps (default 30, sv_demoIdlefps 10 while idle,
// mvdsv/src/sv_send.c:1339-1346) quantised up to the server tick, so measured
// cadence is bimodal at ~13-16 ms and ~34-39 ms. Anything that needs "one
// frame of slack" has to ask the demo rather than assume a rate.
func broadcastCadenceMs(players []result.PlayerStream) int32 {
	var best *result.PositionTrack
	for i := range players {
		pt := players[i].Position
		if pt != nil && (best == nil || len(pt.T) > len(best.T)) {
			best = pt
		}
	}
	if best == nil || len(best.T) < 8 {
		return packCadenceDefaultMs
	}
	gaps := make([]int32, 0, len(best.T)-1)
	for i := 1; i < len(best.T); i++ {
		if d := best.T[i] - best.T[i-1]; d > 0 {
			gaps = append(gaps, d)
		}
	}
	if len(gaps) == 0 {
		return packCadenceDefaultMs
	}
	sort.Slice(gaps, func(i, j int) bool { return gaps[i] < gaps[j] })
	med := gaps[len(gaps)/2]
	if med > packCadenceMaxMs {
		return packCadenceMaxMs
	}
	if med < 1 {
		return 1
	}
	return med
}

// packSampledTouchers narrows a toucher set to those whose overlap was
// WITNESSED at a broadcast sample rather than merely swept between two.
//
// It runs only at the 120 s timeout, where the pack was leaving the wire in
// that frame regardless (classifyPackFate). The swept path is the right test
// everywhere else — the touch instant falls strictly between two broadcasts,
// and a running player crosses the box without either endpoint being inside
// it — but a swept path is also what a player merely running PAST an expiring
// pack looks like, and at the timeout that reading is the likelier one. An
// overlap the wire actually sampled is the stronger claim, and it is the one
// allowed to outrank SUB_Remove.
func packSampledTouchers(pack *PackEntityLife, players []result.PlayerStream, rules packTouchRules, in []int) []int {
	var out []int
	for _, i := range in {
		p := &players[i]
		for _, pos := range positionsBracketing(p.Position, pack.End, packPosStaleMs) {
			if packTouch(pack.Rest, pos) {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// packTouchers returns the indices of the players who were on the pack when
// it left the wire, ascending.
//
// The test runs against BOTH position samples bracketing the disappearance,
// and passes if either overlaps. The pack left the wire at some instant
// between two broadcasts, and the player who ran onto it is at one end of
// that interval and past it at the other — at 30 demo-frames per second a
// player at running speed covers ~16 units between samples, which is enough
// to leave a 48-unit box. Testing only the nearer sample cost 2.6% of real
// pickups (measured, BACKPACKS.md); testing both is not a widened radius but
// the correct statement of what the wire says.
//
// Dead players are excluded because BackpackTouch returns immediately on
// `ISDEAD(other)` (ktx/src/items.c:2377) — and a corpse keeps streaming
// position samples at full rate, so without the gate one lying on the pack
// would be a candidate for every pickup.
//
// SPECTATORS need no gate of their own, even though BackpackTouch's very
// first guard is `other->ct != ctPlayer` (items.c:2373). Streams.Players is
// not filtered on participation — it carries a row for any occupant with
// stream data — but mvdsv writes NO svc_playerinfo and NO stats for a
// spectator (`if (cl->state != cs_spawned || cl->spectator) continue`,
// mvdsv/src/sv_ents.c:463, and the two matching gates at sv_demo.c:1540 and
// :1618), so a spectating stretch contributes no position samples at all. A
// pure spectator therefore never gets a row (the empty-builder cut in
// buildStreamsResult drops it), and someone who plays part of a match and
// spectates the rest keeps one merged row whose track simply stops. The
// position-staleness bound in positionsBracketing is what excludes them:
// with no sample within packPosStaleMs of the disappearance there is no
// position to test, whatever their Alive interval says — and Alive DOES
// overhang a spectating stretch, because clipToPresence trims only the ends
// of a track and never splits on an interior hole.
func packTouchers(pack *PackEntityLife, players []result.PlayerStream, rules packTouchRules) []int {
	var out []int
	for i := range players {
		p := &players[i]
		if !packAliveAt(p.Alive, pack.End) {
			continue
		}
		if rules.any() && !packEligible(p, pack.End, rules) {
			continue
		}
		if !playerOnPack(p, pack) {
			continue
		}
		out = append(out, i)
	}
	return out
}

// playerOnPack reports whether the player was on the pack when it left the
// wire — tested over the whole path they travelled between the two broadcast
// samples bracketing the disappearance, not at either endpoint.
//
// The path, not the endpoints, is what the evidence actually is. An MVD is
// written at sv_demofps (default 30, mvdsv/src/sv_send.c:1339-1346) while the
// server runs at its own tick, so the touch instant falls strictly between
// two broadcasts and neither is where the player was when it happened: at
// running speed they cover 10-16 units in that gap, which is enough to be
// outside a 48-unit box at both ends while having crossed it in between.
// Measured on the ground truth, testing endpoints only put the real picker
// outside the box on 211 of 10 000 pickups, all of them overshooting a bound
// by 0-9 units.
// The PACK is held at its LAST broadcast origin across the sweep, and
// deliberately not walked back along its own track. It is tossed with
// `velocity[2] = 300` and a random horizontal kick (items.c:2856-2861), so on
// the frames around a mid-air grab it is genuinely still travelling, and
// placing it at each player sample's own instant looks like the more faithful
// test. It measures WORSE, for a reason that only shows up in the numbers:
// the pack's last origin is broadcast in the very frame it left the wire, so
// it is a sample AT the touch instant, while its earlier origins are samples
// of where it no longer was. Sweeping the pack backwards tests the overlap at
// times the touch did not happen at, and cost 249 of 10 000 real pickups
// (recall 96.13% → 93.64%) for no precision gain — precision is 100% either
// way. Measured, reverted, and recorded in BACKPACKS.md so it is not
// re-proposed.
func playerOnPack(p *result.PlayerStream, pack *PackEntityLife) bool {
	pos := positionsBracketing(p.Position, pack.End, packPosStaleMs)
	switch len(pos) {
	case 0:
		return false
	case 1:
		return packTouch(pack.Rest, pos[0])
	}
	return packTouchSegment(pack.Rest, pos[0], pos[1])
}

// packTouchSegment reports whether the straight path from a to b passes
// through a STATIONARY pack's touch box at any point — the moving-player form
// of packTouch.
func packTouchSegment(pack, a, b [3]float32) bool {
	return packTouchSweep(pack, pack, a, b)
}

// packTouchSweep reports whether a pack travelling packA→packB and a player
// travelling playerA→playerB were ever overlapping, both moving at constant
// velocity over the same interval.
//
// Two moving boxes overlap exactly when their SEPARATION enters the touch
// box, and the separation of two points moving linearly is itself a straight
// segment — so this is one slab clip of (packA-playerA)→(packB-playerB)
// against the same three bounds packTouch tests, and reduces to packTouch
// when both ends coincide.
func packTouchSweep(packA, packB, playerA, playerB [3]float32) bool {
	lo := [3]float64{-packTouchMaxXY, -packTouchMaxXY, packTouchMinDZ}
	hi := [3]float64{packTouchMaxXY, packTouchMaxXY, packTouchMaxDZ}
	t0, t1 := 0.0, 1.0
	for i := 0; i < 3; i++ {
		a := float64(packA[i] - playerA[i])
		b := float64(packB[i] - playerB[i])
		d := b - a
		if d == 0 {
			if a <= lo[i] || a >= hi[i] {
				return false
			}
			continue
		}
		s0, s1 := (lo[i]-a)/d, (hi[i]-a)/d
		if s0 > s1 {
			s0, s1 = s1, s0
		}
		if s0 > t0 {
			t0 = s0
		}
		if s1 < t1 {
			t1 = s1
		}
		if t0 >= t1 {
			return false
		}
	}
	return true
}

// packAliveAt reads the canonical liveness stream. A nil Alive means
// liveness was not measurable on this demo and degrades to "alive" — the
// same three-state reading aimcore uses — because gating every pickup on a
// stream that was never derived would be worse than crediting a corpse.
func packAliveAt(alive []result.Interval, t int32) bool {
	if alive == nil {
		return true
	}
	i := sort.Search(len(alive), func(i int) bool { return alive[i].End > t })
	return i < len(alive) && alive[i].Start <= t
}

// positionsBracketing returns the player's broadcast origins on either side
// of t — the last at or before it and the first at or after it — dropping
// either when it is further than tol away. Both, not the nearer one: an
// instant the server resolved between two broadcasts is witnessed by both of
// them.
func positionsBracketing(pt *result.PositionTrack, t, tol int32) [][3]float32 {
	if pt == nil || len(pt.T) == 0 {
		return nil
	}
	i := sort.Search(len(pt.T), func(j int) bool { return pt.T[j] >= t })
	var out [][3]float32
	for _, j := range [2]int{i - 1, i} {
		if j < 0 || j >= len(pt.T) || abs32(pt.T[j]-t) > tol {
			continue
		}
		out = append(out, [3]float32{pt.X[j], pt.Y[j], pt.Z[j]})
	}
	return out
}

// positionNear returns the player's broadcast origin closest in time to t,
// when one is within tol.
func positionNear(pt *result.PositionTrack, t, tol int32) ([3]float32, bool) {
	var zero [3]float32
	if pt == nil || len(pt.T) == 0 {
		return zero, false
	}
	i := sort.Search(len(pt.T), func(j int) bool { return pt.T[j] >= t })
	best, bestD := -1, int32(math.MaxInt32)
	for _, j := range [2]int{i - 1, i} {
		if j < 0 || j >= len(pt.T) {
			continue
		}
		if d := abs32(pt.T[j] - t); d < bestD {
			best, bestD = j, d
		}
	}
	if best < 0 || bestD > tol {
		return zero, false
	}
	return [3]float32{pt.X[best], pt.Y[best], pt.Z[best]}, true
}

// gainedWeaponAt reports whether the player did not hold weapon shortly
// before t and did hold it shortly after — the STAT_ITEMS bit transition,
// read off the possession intervals the timeline already derives.
func gainedWeaponAt(p *result.PlayerStream, weapon string, t int32) bool {
	iv := packWeaponIntervals(p, weapon)
	if iv == nil {
		return false
	}
	return !packHeldAt(iv, t-packBitGainPreMs) && packHeldAt(iv, t+packBitGainPostMs)
}

func packWeaponIntervals(p *result.PlayerStream, weapon string) []result.Interval {
	switch weapon {
	case "rl":
		return p.RL
	case "lg":
		return p.LG
	}
	return nil
}

func packHeldAt(iv []result.Interval, t int32) bool {
	i := sort.Search(len(iv), func(i int) bool { return iv[i].End > t })
	return i < len(iv) && iv[i].Start <= t
}

// weaponSpawnerPositions returns the map's RL / LG world spawn points, keyed
// by weapon, from the item timeline the entity stream already produced.
func weaponSpawnerPositions(res *Result) map[string][][3]float32 {
	out := map[string][][3]float32{}
	if res.Items == nil {
		return out
	}
	for i := range res.Items.Items {
		it := &res.Items.Items[i]
		if it.Kind != "rl" && it.Kind != "lg" {
			continue
		}
		out[it.Kind] = append(out[it.Kind], [3]float32{it.X, it.Y, it.Z})
	}
	return out
}

// nearWeaponSpawner reports whether the player was standing on a world
// spawner of the same weapon when the pack left the wire — in which case
// their weapon-bit gain is not evidence about the pack, and cannot be used
// to separate them from the other player on it. The same touch predicate
// applies: a weapon spawner is an FL_ITEM SOLID_TRIGGER too.
func nearWeaponSpawner(p *result.PlayerStream, weapon string, t int32, spawners map[string][][3]float32) bool {
	for _, pos := range positionsBracketing(p.Position, t, packPosStaleMs) {
		for _, s := range spawners[weapon] {
			if packTouch(s, pos) {
				return true
			}
		}
	}
	return false
}

func vecDist(a, b [3]float32) float64 {
	dx := float64(a[0] - b[0])
	dy := float64(a[1] - b[1])
	dz := float64(a[2] - b[2])
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

// DiagnoseBackpackFate explains, in the vocabulary of the linkage's own
// conditions, why one reconstructed drop did not come out as a pickup
// attributed to wantPicker. It is exported for the same reason
// BackpackReconStandDown is: the ground-truth harness
// (cmd/qw-backpack-eval -linkage) has to report the pass's reasons, and a
// re-implementation of them in the harness would drift from the pass it is
// supposed to be measuring.
//
// drop must already have been through LinkBackpackDrops.
func DiagnoseBackpackFate(res *Result, packs []PackEntityLife, drop result.BackpackDrop, wantPicker string) string {
	if drop.EntNum == 0 {
		near, nearest := 0, math.MaxFloat64
		for i := range packs {
			dt := packs[i].Start - drop.Time
			if dt < -packBindWindowMs || dt > packBindWindowMs {
				continue
			}
			near++
			if d := vecDist(packs[i].Spawn, drop.Origin); d < nearest {
				nearest = d
			}
		}
		if near == 0 {
			bestDt := int32(math.MaxInt32)
			for i := range packs {
				if dt := abs32(packs[i].Start - drop.Time); dt < bestDt {
					bestDt = dt
				}
			}
			return fmt.Sprintf("no backpack entity within %dms of the drop (nearest %dms)", packBindWindowMs, bestDt)
		}
		if nearest > packBindMaxDist {
			return fmt.Sprintf("nearest backpack entity %.0f units away (cap %d)", nearest, packBindMaxDist)
		}
		return "a nearer drop claimed every backpack entity in the window"
	}
	var pack *PackEntityLife
	for i := range packs {
		if packs[i].EntNum != drop.EntNum {
			continue
		}
		dt := packs[i].Start - drop.Time
		if dt >= -packBindWindowMs && dt <= packBindWindowMs {
			pack = &packs[i]
			break
		}
	}
	if pack == nil {
		return "bound entity not found (harness passed a different pack track)"
	}
	if !pack.Ended {
		return "pack was still on the wire when the recording stopped"
	}
	players := res.Streams.Players
	joinName := streamJoinNames(players)
	wi := -1
	for i := range players {
		if joinName[i] == wantPicker {
			wi = i
			break
		}
	}
	if wi < 0 {
		return "no player stream carries the picker's name"
	}
	pos, ok := positionNear(players[wi].Position, pack.End, packPosStaleMs)
	if !ok {
		return fmt.Sprintf("picker had no position sample within %dms of the disappearance", packPosStaleMs)
	}
	if !packAliveAt(players[wi].Alive, pack.End) {
		return "picker was outside every derived life at the disappearance"
	}
	if rules := packTouchRulesOf(res); rules.any() && !packEligible(&players[wi], pack.End, rules) {
		return "picker was mode-ineligible (BackpackTouch would have refused the pack)"
	}
	if !playerOnPack(&players[wi], pack) {
		dx := math.Abs(float64(pack.Rest[0] - pos[0]))
		dy := math.Abs(float64(pack.Rest[1] - pos[1]))
		dz := float64(pack.Rest[2] - pos[2])
		var over []string
		if dx >= packTouchMaxXY {
			over = append(over, fmt.Sprintf("dx %.0f", dx))
		}
		if dy >= packTouchMaxXY {
			over = append(over, fmt.Sprintf("dy %.0f", dy))
		}
		if dz <= packTouchMinDZ {
			over = append(over, fmt.Sprintf("dz %.0f (below)", dz))
		}
		if dz >= packTouchMaxDZ {
			over = append(over, fmt.Sprintf("dz %.0f (above)", dz))
		}
		return "picker outside the touch box: " + strings.Join(over, ", ")
	}
	if drop.Fate == result.BackpackFatePicked && drop.Picker == "" {
		return "several players on the pack, none separated by a weapon-bit gain"
	}
	if pack.End-pack.Start >= packExpiryTimeoutMs-packExpirySlackFrames*broadcastCadenceMs(players) {
		return "pack reached KTX's 120 s timeout and no broadcast sample witnessed the overlap"
	}
	return "picker was on the pack — classification disagreed for another reason"
}
