package damagerecon

import (
	"math"
	"sort"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// beam is one TE_LIGHTNING2 segment (one LG attack).
type beam struct {
	t    int32
	s, e vec3
}

// pointFx is one damage-telemetry temp entity from streams.pointEffects:
// an explosion (exact rocket/grenade detonation point), a blood splat
// (hitscan damage striking a player, count = the raw count byte) or a
// lightning blood (an LG cell connecting). claimed marks an explosion
// matched to a tracked projectile's endpoint so the trackless-rocket
// search does not reuse it.
type pointFx struct {
	t       int32
	count   int
	p       vec3
	claimed bool
}

// projectile is one bracketed rocket/grenade entity flight with the
// resolved shooter (the wire carries no owner; resolution is spawn
// proximity + aim-vs-flight-direction + fire-sound gating, ported from the
// prototype).
type projectile struct {
	weapon  string
	spawnT  int32
	endT    int32
	sp, ep  vec3
	shooter string // "" when unresolved
	// epExact: ep was snapped to a matching TE_EXPLOSION — the true
	// detonation point rather than the last broadcast entity position.
	epExact bool
}

// firedShot is one damage-free fire observation (sound or beam) from the
// shots stream.
type firedShot struct {
	t      int32
	player string
	weapon string
}

// inputs is everything the attribution pass reads, assembled once.
type inputs struct {
	players map[string]*result.PlayerStream
	tracks  map[string]*track
	teams   map[string]string
	order   []string // deterministic player iteration order

	fragAt map[fragKey]*result.FragEntry
	// fragAnyAt also holds suicide/teamkill entries — the obituary for an
	// enemy telefrag is a killer-less "was telefragged" that parses as a
	// suicide, yet it still proves (and types) the masked death.
	fragAnyAt map[fragKey]*result.FragEntry
	beams     []beam       // sorted by t
	projs     []projectile // sorted by endT
	shots     []firedShot  // sorted by t
	nailShots []firedShot  // ng/sng subset, sorted by t
	// Point-effect damage telemetry (streams.pointEffects), each sorted by
	// t. Absent (nil) on demos whose recording predates the stream or when
	// shot streams were built without it — consumers must treat absence as
	// "not measured", never as "no hits".
	explosions []pointFx // TE_EXPLOSION: exact rl/gl detonation points
	bloods     []pointFx // TE_BLOOD: hitscan damage on a player, count byte
	lgBloods   []pointFx // TE_LIGHTNINGBLOOD: LG cell hits
	// consumedRL: per shooter, spawn times of rockets that HAVE an entity
	// track — such a shot must not also act as a trackless point-blank
	// candidate.
	consumedRL map[string][]int32

	// discharges: LG water discharges, detected from a player's cells
	// stream dropping to exactly 0 while alive and holding LG. Base damage
	// is 35*cells (id1 W_FireLightning), radius-dealt (self halved).
	discharges []discharge

	// lgAmmoFires: every small cells decrement (1..3, alive) per player —
	// the ammo-side record of LG attacks, one entry per stat update.
	// lgBeamSparse marks a demo whose recorded TE_LIGHTNING2 beams cover
	// well under half of the cells actually spent: old servers (observed
	// on MVDSV 0.33-era recordings) drop most LG beam multicasts from the
	// demo, so beam-gated attribution misses whole shaft fights — the
	// ammo drain is the fallback fire evidence there.
	lgAmmoFires  []firedShot // sorted by t, weapon "lg"
	lgBeamSparse bool

	// bloodTrust: the demo's TE_BLOOD count bytes passed per-demo
	// calibration — 4·(summed counts near the victim) reproduced the
	// observed h/a delta on enough unambiguous single-shotgunner instants
	// (both KTX packagings satisfy the summed identity: modern one-message-
	// per-volley with count = pellet hits, and old one-message-per-pellet
	// with count = 1). When false the counts stay presence/position
	// evidence only (axe/nail bloods carry damage-valued counts, and at
	// least one mod writes nonstandard constants).
	bloodTrust bool

	// weaponBitsLive: whether the demo's StatItems weapon bits actually
	// cycle with pickups/deaths. Old recorders freeze them (a player
	// "holds" RL from 0:00 through every death while the armor bits in the
	// SAME stat cycle normally), which would classify every hit into the
	// top EWep bucket — confidently wrong. When frozen, the victim-weapon
	// classification is withheld entirely (VictimWep "" and empty
	// enemyVs*/ewep) rather than fabricated.
	weaponBitsLive bool

	duel    bool
	selfPen float64
	bsp     *bspGate
	// rlLo/rlHi: the demo's rocket direct-damage range. Vanilla default
	// 100+random*20; narrowed to exactly 110 when detectRocketRegime
	// recognises a fixed-constant server.
	rlLo, rlHi float64
}

type fragKey struct {
	victim string
	t      int32
}

// isPositionalWeapon: frag weapons that are positional instant kills, not
// weapon damage — folded like analyzer/damage.go's telefrag/stomp path.
// (squish is NOT here: the KTX pipeline logs crush damage as normal events
// under the "squish" weapon token, so the reconstruction does the same.)
func isPositionalWeapon(w string) bool {
	return w == "tele" || w == "stomp"
}

func buildInputs(res *result.Result) *inputs {
	in := &inputs{
		players:    make(map[string]*result.PlayerStream),
		tracks:     make(map[string]*track),
		teams:      make(map[string]string),
		fragAt:     make(map[fragKey]*result.FragEntry),
		fragAnyAt:  make(map[fragKey]*result.FragEntry),
		consumedRL: make(map[string][]int32),
		rlLo:       100,
		rlHi:       120,
	}
	for i := range res.Streams.Players {
		p := &res.Streams.Players[i]
		in.players[p.Name] = p
		in.teams[p.Name] = p.Team
		if tr := newTrack(p.Position); tr != nil {
			in.tracks[p.Name] = tr
		}
		in.order = append(in.order, p.Name)
	}
	sort.Strings(in.order)

	mode := ""
	if res.DemoInfo != nil {
		mode = strings.ToLower(res.DemoInfo.Mode)
	}
	in.duel = mode == "duel" || mode == "1on1" ||
		(mode == "" && len(res.Streams.Players) == 2)
	// Enemy-attribution prior (study §"The enemy-attribution prior"):
	// prefer the enemy explanation of an ambiguous delta. Mode-aware — the
	// bias earns duel recall but costs team-game precision.
	// Values re-tuned on per-player-total error after the engine-true
	// self-splash model landed (the study's 0.6 was tuned for burst recall
	// against the weaker model and inflates duel given ~8%; swept
	// 0.0-0.6 on the eval corpus, 0.15 is the totals optimum).
	if in.duel {
		in.selfPen = 0.15
	} else {
		in.selfPen = 0.1
	}

	if res.Frags != nil {
		for i := range res.Frags.Frags {
			f := &res.Frags.Frags[i]
			in.fragAnyAt[fragKey{f.Victim, f.Time}] = f
			if f.IsSuicide || f.IsTeamKill {
				continue
			}
			in.fragAt[fragKey{f.Victim, f.Time}] = f
		}
	}

	if bs := res.Streams.Beams; bs != nil {
		for i := range bs.T {
			in.beams = append(in.beams, beam{
				t: bs.T[i],
				s: vec3{float64(bs.Sx[i]), float64(bs.Sy[i]), float64(bs.Sz[i])},
				e: vec3{float64(bs.Ex[i]), float64(bs.Ey[i]), float64(bs.Ez[i])},
			})
		}
		sort.SliceStable(in.beams, func(i, j int) bool { return in.beams[i].t < in.beams[j].t })
	}

	if res.Shots != nil {
		for i := range res.Shots.Shots {
			s := &res.Shots.Shots[i]
			fs := firedShot{t: s.Time, player: s.Player, weapon: s.Weapon}
			in.shots = append(in.shots, fs)
			if s.Weapon == "ng" || s.Weapon == "sng" {
				in.nailShots = append(in.nailShots, fs)
			}
		}
		sort.SliceStable(in.shots, func(i, j int) bool { return in.shots[i].t < in.shots[j].t })
		sort.SliceStable(in.nailShots, func(i, j int) bool { return in.nailShots[i].t < in.nailShots[j].t })
	}

	if pe := res.Streams.PointEffects; pe != nil {
		for i := range pe.T {
			fx := pointFx{
				t:     pe.T[i],
				count: int(pe.Count[i]),
				p:     vec3{float64(pe.X[i]), float64(pe.Y[i]), float64(pe.Z[i])},
			}
			switch int(pe.Type[i]) {
			case events.TeExplosion:
				in.explosions = append(in.explosions, fx)
			case events.TeBlood:
				in.bloods = append(in.bloods, fx)
			case events.TeLightningBlood:
				in.lgBloods = append(in.lgBloods, fx)
			}
		}
		for _, lst := range []*[]pointFx{&in.explosions, &in.bloods, &in.lgBloods} {
			l := *lst
			sort.SliceStable(l, func(i, j int) bool { return l[i].t < l[j].t })
		}
	}

	in.resolveProjectiles(res.Streams.Projectiles)
	in.detectDischarges()
	in.detectLGAmmoFires()
	in.weaponBitsLive = weaponBitsCycle(res.Streams.Players)
	return in
}

// detectLGAmmoFires scans each player's cells stream for fire-sized
// decrements (1..3 cells while alive — a discharge wipes to zero and is
// detected separately) and flags the demo beam-sparse when the recorded
// TE_LIGHTNING2 beams account for less than half the cells spent. On such
// recordings the ammo drain is the only reliable LG fire record.
func (in *inputs) detectLGAmmoFires() {
	totalDrops := 0
	for _, name := range in.order {
		p := in.players[name]
		prev := -1
		for i := range p.Cells {
			c := p.Cells[i]
			v := int(c.V)
			if prev >= 0 && v < prev && prev-v <= 3 && inWeaponIntervals(p.Alive, c.T) {
				totalDrops += prev - v
				in.lgAmmoFires = append(in.lgAmmoFires, firedShot{t: c.T, player: name, weapon: "lg"})
			}
			prev = v
		}
	}
	sort.SliceStable(in.lgAmmoFires, func(i, j int) bool { return in.lgAmmoFires[i].t < in.lgAmmoFires[j].t })
	// Modern KTX writes exactly one beam per cell (beam count == acc.attacks),
	// so healthy recordings sit at ~100% coverage; the old recordings that
	// need the ammo fallback sit visibly below (observed 76% on MVDSV 0.33,
	// with whole shaft bursts beam-less).
	in.lgBeamSparse = totalDrops > 20 && float64(len(in.beams)) < 0.9*float64(totalDrops)
}

// weaponBitsCycle reports whether any weapon-inventory interval opens
// after the match start — the signature of live StatItems weapon bits
// (a weapon is picked up seconds into a life). A frozen-bits recording
// shows exactly one [0, end) interval per held weapon and nothing else.
func weaponBitsCycle(players []result.PlayerStream) bool {
	for i := range players {
		p := &players[i]
		for _, ivs := range [][]result.Interval{p.RL, p.LG, p.GL, p.SSG, p.SNG} {
			for _, iv := range ivs {
				if iv.Start > 0 {
					return true
				}
			}
			if len(ivs) > 1 {
				return true
			}
		}
	}
	return false
}

// discharge is one detected LG water discharge.
type discharge struct {
	t      int32
	player string
	cells  int
}

// detectDischarges scans each player's cells stream for the discharge
// signature: cells drop to exactly 0 (all consumed at once) while the
// player is alive and holds LG. A discharge multicasts no beam and often no
// stat besides the ammo wipe — its damage (35*cells, radius-dealt) is
// otherwise invisible to the reconstruction.
func (in *inputs) detectDischarges() {
	const minDischargeCells = 10
	for _, name := range in.order {
		p := in.players[name]
		prev := 0
		for i := range p.Cells {
			c := p.Cells[i]
			// Alive check is inclusive-end (inWeaponIntervals semantics): a
			// discharge usually KILLS the discharger, closing the alive
			// interval at the same instant the cells row lands.
			if c.V == 0 && prev >= minDischargeCells &&
				inWeaponIntervals(p.Alive, c.T) && inWeaponIntervals(p.LG, c.T) {
				in.discharges = append(in.discharges, discharge{t: c.T, player: name, cells: prev})
			}
			prev = int(c.V)
		}
	}
}

// projShooterMaxAimDeg / projAimScore / projDistScore: the prototype's
// shooter-resolution scoring constants.
const (
	projShooterSoundTolMs = 80
	projShooterMaxAimDeg  = 55.0
	rProjSrc              = 220.0 // units projectile spawn to shooter (p50=81, p90=152)
)

// resolveProjectiles brackets the columnar projectile stream into flights
// and resolves each flight's shooter: players who fired that weapon within
// the sound tolerance (else everyone), scored by spawn proximity and — for
// rockets, which fly exactly where aimed — the angle between the shooter's
// aim and the flight direction.
func (in *inputs) resolveProjectiles(ps *result.ProjectileStreams) {
	if ps == nil {
		return
	}
	// weapon -> sorted fire times/players
	shotsByWeapon := make(map[string][]firedShot)
	for _, s := range in.shots {
		shotsByWeapon[s.weapon] = append(shotsByWeapon[s.weapon], s)
	}

	for i := range ps.Spawn {
		w := ps.Weapon[i]
		sT := ps.Spawn[i]
		sp := vec3{float64(ps.Sx[i]), float64(ps.Sy[i]), float64(ps.Sz[i])}
		ep := vec3{float64(ps.Ex[i]), float64(ps.Ey[i]), float64(ps.Ez[i])}

		var pool []string
		for _, s := range shotsByWeapon[w] {
			if abs32(s.t-sT) <= projShooterSoundTolMs {
				pool = append(pool, s.player)
			}
		}
		if len(pool) == 0 {
			pool = in.order
		}

		fd := ep.sub(sp)
		fl := fd.length()
		best, bestScore := "", math.Inf(1)
		for _, name := range pool {
			tr := in.tracks[name]
			if tr == nil {
				continue
			}
			dd := tr.posAt(sT).distTo(sp)
			if dd > rProjSrc {
				continue
			}
			score := dd * 0.02
			if w == "rl" && fl > 60 {
				if fw, ok := tr.aimAt(sT); ok {
					c := fw.dot(fd) / fl
					ang := math.Acos(math.Max(-1, math.Min(1, c))) * 180.0 / math.Pi
					if ang > projShooterMaxAimDeg {
						continue
					}
					score += ang * 0.12
				}
			}
			if score < bestScore {
				best, bestScore = name, score
			}
		}
		in.projs = append(in.projs, projectile{
			weapon: w, spawnT: sT, endT: ps.End[i], sp: sp, ep: ep, shooter: best,
		})
	}
	sort.SliceStable(in.projs, func(i, j int) bool { return in.projs[i].endT < in.projs[j].endT })
	in.snapProjectilesToExplosions()

	for _, pr := range in.projs {
		if pr.weapon == "rl" && pr.shooter != "" {
			in.consumedRL[pr.shooter] = append(in.consumedRL[pr.shooter], pr.spawnT)
		}
	}
	for _, lst := range in.consumedRL {
		sort.Slice(lst, func(i, j int) bool { return lst[i] < lst[j] })
	}
}

// Explosion-snap gates: a tracked projectile's despawn origin is the
// entity's LAST BROADCAST position — up to one server frame short of the
// true detonation point. TE_EXPLOSION is that true point; snapping the
// flight endpoint to the matching explosion makes every splash-distance
// use (candidate dEnd, the raw overkill top-up) exact.
const (
	explosionSnapMs   = 80    // despawn frame to explosion multicast
	explosionSnapDist = 130.0 // one frame of rocket travel + wire rounding
)

// snapProjectilesToExplosions replaces each tracked flight's interpolated
// endpoint with the exact TE_EXPLOSION detonation point when one matches in
// time and space, and marks that explosion claimed so the trackless-rocket
// candidate search does not reuse it. Nearest-explosion-first per flight;
// each explosion claims at most one flight.
func (in *inputs) snapProjectilesToExplosions() {
	if len(in.explosions) == 0 {
		return
	}
	for i := range in.projs {
		pr := &in.projs[i]
		lo := sort.Search(len(in.explosions), func(k int) bool { return in.explosions[k].t >= pr.endT-explosionSnapMs })
		bestJ, bestD := -1, explosionSnapDist
		for j := lo; j < len(in.explosions) && in.explosions[j].t <= pr.endT+explosionSnapMs; j++ {
			if in.explosions[j].claimed {
				continue
			}
			if d := in.explosions[j].p.distTo(pr.ep); d < bestD {
				bestJ, bestD = j, d
			}
		}
		if bestJ >= 0 {
			in.explosions[bestJ].claimed = true
			pr.ep = in.explosions[bestJ].p
			pr.epExact = true
		}
	}
}

// Blood matching gates: TE_BLOOD / TE_LIGHTNINGBLOOD spawn on the victim's
// body at the pellet / beam impact point, so they sit within the victim's
// bbox plus interpolation error of the track position; the multicast lands
// in the same server frame as the damage.
const (
	bloodNearDist = 100.0
	bloodNearMs   = 40
)

// bloodsNear returns how many TE_BLOOD messages landed near the victim at
// the instant and their summed count bytes.
func (in *inputs) bloodsNear(t int32, vpos vec3) (n, sum int) {
	lo := sort.Search(len(in.bloods), func(i int) bool { return in.bloods[i].t >= t-bloodNearMs })
	for i := lo; i < len(in.bloods) && in.bloods[i].t <= t+bloodNearMs; i++ {
		if in.bloods[i].p.distTo(vpos) <= bloodNearDist {
			n++
			sum += in.bloods[i].count
		}
	}
	return n, sum
}

// lgBloodNear reports whether a TE_LIGHTNINGBLOOD landed near the victim
// at the instant — the per-cell LG hit confirmation.
func (in *inputs) lgBloodNear(t int32, vpos vec3) bool {
	lo := sort.Search(len(in.lgBloods), func(i int) bool { return in.lgBloods[i].t >= t-bloodNearMs })
	for i := lo; i < len(in.lgBloods) && in.lgBloods[i].t <= t+bloodNearMs; i++ {
		if in.lgBloods[i].p.distTo(vpos) <= bloodNearDist {
			return true
		}
	}
	return false
}

// countHitscanFires counts sg/ssg fires within the hitscan window of t.
func (in *inputs) countHitscanFires(t int32) int {
	n := 0
	lo := sort.Search(len(in.shots), func(i int) bool { return in.shots[i].t >= t-tolShotMs })
	for i := lo; i < len(in.shots) && in.shots[i].t <= t+tolShotMs; i++ {
		if w := in.shots[i].weapon; w == "sg" || w == "ssg" {
			n++
		}
	}
	return n
}

// calibrateBloods validates the demo's TE_BLOOD count packaging against
// the observed deltas: on survived instants with exactly one shotgun fire
// in the window and blood on the victim, both KTX generations satisfy
// 4·(summed counts) == the h/a delta (one volley message with count =
// pellet hits, or per-pellet messages with count = 1). A demo where the
// identity holds on a clear majority of samples earns bloodTrust —
// count-pinned magnitude bounds; anything else (nonstandard mods, exotic
// progs) keeps counts as presence evidence only.
func calibrateBloods(in *inputs) {
	if len(in.bloods) == 0 {
		return
	}
	match, miss := 0, 0
	for _, victim := range in.order {
		p := in.players[victim]
		vtrack := in.tracks[victim]
		if p == nil || vtrack == nil {
			continue
		}
		for _, d := range victimDeltas(p, nil) {
			if d.died || d.masked {
				continue
			}
			if in.countHitscanFires(d.t) != 1 {
				continue
			}
			n, sum := in.bloodsNear(d.t, vtrack.posAt(d.t))
			if n == 0 || sum == 0 {
				continue
			}
			if 4*sum == d.bounded {
				match++
			} else {
				miss++
			}
		}
	}
	in.bloodTrust = match >= 10 && float64(match) >= 0.7*float64(match+miss)
}

// shotConsumed reports whether the player's RL fire at ts is accounted for
// by a tracked projectile (within tol ms).
func (in *inputs) shotConsumed(player string, ts int32, tol int32) bool {
	lst := in.consumedRL[player]
	i := sort.Search(len(lst), func(k int) bool { return lst[k] >= ts })
	for _, j := range [2]int{i - 1, i} {
		if j >= 0 && j < len(lst) && abs32(lst[j]-ts) <= tol {
			return true
		}
	}
	return false
}

// Telefrag-attacker inference gates (recovery when the obituary names no
// killer — "was telefragged" parses as a suicide).
const (
	teleArrivalWindowMs = 150
	teleJumpMinDist     = 250.0
	teleArrivalMaxDist  = 200.0
	teleStandMaxDist    = 130.0
)

// teleportArrivalAt finds the telefrag attacker at instant t. Two
// mechanisms, tried in order:
//
//  1. classic teleporter telefrag — the attacker's track JUMPS onto the
//     victim's spot (adjacent samples > teleJumpMinDist apart, landing
//     near the victim);
//  2. spawn telefrag — the victim (re)spawned onto an occupied spot and
//     the spawn rules deflected the kill onto them: the attacker is simply
//     the nearest live player standing within telefrag range.
//
// The victim's position is sampled just AFTER t: in the respawn+instant-
// telefrag cycle the victim's only wire-visible position near t is the new
// corpse on the contested pad.
func (in *inputs) teleportArrivalAt(victim string, t int32) string {
	vtrack := in.tracks[victim]
	if vtrack == nil {
		return ""
	}
	vpos := vtrack.posAt(t + 60)
	best, bestDist := "", teleArrivalMaxDist
	standBest, standDist := "", teleStandMaxDist
	for _, name := range in.order {
		if name == victim {
			continue
		}
		tr := in.tracks[name]
		if tr == nil {
			continue
		}
		pt := tr.pt
		i := sort.Search(len(pt.T), func(k int) bool { return pt.T[k] >= t-teleArrivalWindowMs })
		for ; i < len(pt.T) && pt.T[i] <= t+teleArrivalWindowMs; i++ {
			if i == 0 {
				continue
			}
			a := vec3{float64(pt.X[i-1]), float64(pt.Y[i-1]), float64(pt.Z[i-1])}
			b := vec3{float64(pt.X[i]), float64(pt.Y[i]), float64(pt.Z[i])}
			if a.distTo(b) < teleJumpMinDist {
				continue
			}
			if dd := b.distTo(vpos); dd < bestDist {
				best, bestDist = name, dd
			}
		}
		if inIntervals(in.players[name].Alive, t) {
			if dd := tr.posAt(t).distTo(vpos); dd < standDist {
				standBest, standDist = name, dd
			}
		}
	}
	if best != "" {
		return best
	}
	return standBest
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
