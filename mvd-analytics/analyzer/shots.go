package analyzer

import (
	"sort"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// ShotsAnalyzer derives a per-shot weapon-fire stream from the raw wire
// signals: svc_sound fire sounds (which carry the firing entity on
// CHAN_WEAPON) for SG/SSG/RL/GL/NG/SNG, and LG cell-ammo decrements for the
// one weapon with no per-shot fire sound. Instantaneous hitscan fires
// (sg/ssg/lg) are linked to the damage they caused in the same server frame
// via the KTX damage stream; projectile fires (rl/gl/ng/sng) are left
// unlinked here (they have travel time and are linked by entity tracking).
//
// It mirrors DamageAnalyzer: raw events are collected during OnEvent and
// resolved to player identities in Finalize via CoreOutputs. The Shots
// stream is ungated (warmup fires included, windowed by consumers);
// aggregates are match-gated for KTX scoreboard parity.
type ShotsAnalyzer struct {
	ctx    *Context
	core   *CoreOutputs
	timing MatchTimingDetector

	// cells tracks each slot's last STAT_CELLS value; cellKnown guards the
	// first sample after a spawn/death so an ammo reset is not counted as
	// LG fire. dead gates the cell-dump that lands when a player dies.
	cells     map[int]int
	cellKnown map[int]bool
	dead      map[int]bool

	shots  []rawShot
	dmgs   []rawShotDmg
	hadDmg bool // any DamageEvent seen — distinguishes 0% accuracy from no-stream
}

// rawShot is one detected fire pinned to a wire slot + time, plus whether
// the match was running (for gated aggregates) and whether the weapon is
// instantaneous hitscan (linkable to same-frame damage).
type rawShot struct {
	slot    int
	weapon  string
	source  string // "sound" | "ammo"
	tMs     int32
	inMatch bool
	hitscan bool
}

// rawShotDmg is a hitscan DamageEvent kept for same-frame shot linking.
type rawShotDmg struct {
	attacker int // wire slot
	victim   int // wire slot
	weapon   string
	tMs      int32
	used     bool
}

const (
	chanWeapon = 1 // CHAN_WEAPON — the channel KTX fires weapons on

	// hitscanLinkWindowMs bounds how far a hitscan damage event may sit
	// from its shot and still be linked. Hitscan damage lands in the same
	// server frame as the fire; a couple of frames of slack covers wire
	// jitter while staying far below any weapon's refire interval.
	hitscanLinkWindowMs = 26

	// maxCellsPerTick caps how many cells a single stat update may drop and
	// still be read as LG fire. A larger drop is a death/disconnect dump or
	// an in-water discharge (all cells at once), not normal firing.
	maxCellsPerTick = 10
)

// NewShotsAnalyzer creates a new shots analyzer.
func NewShotsAnalyzer() *ShotsAnalyzer {
	return &ShotsAnalyzer{
		cells:     make(map[int]int),
		cellKnown: make(map[int]bool),
		dead:      make(map[int]bool),
	}
}

func (a *ShotsAnalyzer) Name() string { return "shots" }

func (a *ShotsAnalyzer) Init(ctx *Context) error {
	a.ctx = ctx
	return nil
}

// UseCoreOutputs is the CoreConsumer contract — Shots consumes co for
// slot→identity resolution and co.DemoInfo for the accuracy cross-check.
func (a *ShotsAnalyzer) UseCoreOutputs(co *CoreOutputs) { a.core = co }

func (a *ShotsAnalyzer) OnEvent(event events.Event) error {
	switch e := event.(type) {
	case *events.PrintEvent:
		a.timing.OnPrint(e)
	case *events.IntermissionEvent:
		a.timing.OnIntermission(e.Time)
	case *events.SpawnEvent:
		a.dead[e.PlayerNum] = false
		a.cellKnown[e.PlayerNum] = false // spawn ammo set is not a fire
	case *events.DeathEvent:
		a.dead[e.PlayerNum] = true
		a.cellKnown[e.PlayerNum] = false // death ammo dump is not a fire
	case *events.StatUpdateEvent:
		if e.StatIndex == events.StatCells {
			a.onCells(e.PlayerNum, e.Value, msTime(e.Time))
		}
	case *events.SoundEvent:
		a.onSound(e)
	case *events.DamageEvent:
		a.hadDmg = true
		if e.Attacker >= 0 {
			if w := events.DeathTypeToWeapon(e.DeathType); isHitscanWeapon(w) {
				a.dmgs = append(a.dmgs, rawShotDmg{
					attacker: e.Attacker,
					victim:   e.Victim,
					weapon:   w,
					tMs:      msTime(e.Time),
				})
			}
		}
	}
	return nil
}

// onSound records a shot when the sound is a weapon-fire sound played on a
// player's CHAN_WEAPON. The svc_sound entity is the shooter (slot = Ent-1).
func (a *ShotsAnalyzer) onSound(e *events.SoundEvent) {
	if e.Channel != chanWeapon || e.Ent < 1 || e.Ent > events.MaxClients {
		return
	}
	w, ok := fireSoundWeapon(e.Name)
	if !ok {
		return
	}
	a.shots = append(a.shots, rawShot{
		slot:    e.Ent - 1,
		weapon:  w,
		source:  "sound",
		tMs:     e.TimeMs,
		inMatch: a.timing.Started && !a.timing.Ended,
		hitscan: isHitscanWeapon(w),
	})
}

// onCells turns a cell-ammo decrease into LG shots. Cells are used only by
// the lightning gun, so a decrement unambiguously counts LG fire ticks —
// guarded against the spawn/death ammo resets and the all-at-once discharge.
func (a *ShotsAnalyzer) onCells(slot, value int, tMs int32) {
	known := a.cellKnown[slot]
	prev := a.cells[slot]
	a.cells[slot] = value
	a.cellKnown[slot] = true
	if !known || a.dead[slot] {
		return // first sample after spawn/death, or dead: baseline only
	}
	drop := prev - value
	if drop <= 0 || drop > maxCellsPerTick {
		return // increase = pickup; large drop = death dump / discharge
	}
	inMatch := a.timing.Started && !a.timing.Ended
	for i := 0; i < drop; i++ {
		a.shots = append(a.shots, rawShot{
			slot:    slot,
			weapon:  "lg",
			source:  "ammo",
			tMs:     tMs,
			inMatch: inMatch,
			hitscan: true,
		})
	}
}

func (a *ShotsAnalyzer) Finalize(result *Result) error {
	if len(a.shots) == 0 {
		return nil
	}

	// Index hitscan damage by attacker slot so linking is per-slot, not a
	// full scan per shot.
	dmgBySlot := make(map[int][]*rawShotDmg)
	for i := range a.dmgs {
		d := &a.dmgs[i]
		dmgBySlot[d.attacker] = append(dmgBySlot[d.attacker], d)
	}

	sort.SliceStable(a.shots, func(i, j int) bool { return a.shots[i].tMs < a.shots[j].tMs })

	out := &ShotsResult{}
	aggByName := make(map[string]*shotAgg)
	var aggOrder []string

	for i := range a.shots {
		s := &a.shots[i]
		id := a.resolveAt(s.slot, s.tMs)
		if id.Name == "" {
			continue // can't attribute the fire to a known player
		}

		shot := Shot{Time: s.tMs, Player: id.Name, Team: id.Team, Weapon: s.weapon, Source: s.source}
		connected := false
		if s.hitscan {
			if victims := a.linkHitscan(dmgBySlot[s.slot], s.weapon, s.tMs); len(victims) > 0 {
				shot.Hit = true
				shot.Victims = victims
				connected = true
			}
		}
		out.Shots = append(out.Shots, shot)

		if !s.inMatch {
			continue
		}
		ag := aggByName[id.Name]
		if ag == nil {
			ag = &shotAgg{team: id.Team, weapons: make(map[string]*weaponAgg)}
			aggByName[id.Name] = ag
			aggOrder = append(aggOrder, id.Name)
		}
		wa := ag.weapons[s.weapon]
		if wa == nil {
			wa = &weaponAgg{}
			ag.weapons[s.weapon] = wa
		}
		wa.shots++
		if connected {
			wa.hits++
		}
	}

	out.ByPlayer = a.buildByPlayer(aggByName, aggOrder)
	out.Reconciliation = a.reconcile(aggByName)

	result.Shots = out
	return nil
}

// linkHitscan returns the names of players damaged by this hitscan fire —
// same attacker slot, same weapon, within the same server frame. Each
// matched damage record is consumed so a later shot cannot reclaim it.
func (a *ShotsAnalyzer) linkHitscan(dmgs []*rawShotDmg, weapon string, tMs int32) []string {
	var victims []string
	seen := make(map[string]bool)
	for _, d := range dmgs {
		if d.used || d.weapon != weapon {
			continue
		}
		if absInt32(d.tMs-tMs) > hitscanLinkWindowMs {
			continue
		}
		d.used = true
		if vn := a.resolveAt(d.victim, d.tMs).Name; vn != "" && !seen[vn] {
			seen[vn] = true
			victims = append(victims, vn)
		}
	}
	return victims
}

// buildByPlayer flattens the match-time aggregates into the result shape,
// sorted by player then by a stable weapon order. Hits/Accuracy are emitted
// only for hitscan weapons and only when a damage stream was present.
func (a *ShotsAnalyzer) buildByPlayer(aggByName map[string]*shotAgg, order []string) []PlayerShots {
	sort.Strings(order)
	out := make([]PlayerShots, 0, len(order))
	for _, name := range order {
		ag := aggByName[name]
		ps := PlayerShots{Player: name, Team: ag.team}
		for _, w := range weaponOrder {
			wa := ag.weapons[w]
			if wa == nil || wa.shots == 0 {
				continue
			}
			ws := WeaponShots{Weapon: w, Shots: wa.shots}
			if a.hadDmg && isHitscanWeapon(w) {
				ws.Hits = wa.hits
				ws.Accuracy = float64(wa.hits) / float64(wa.shots)
			}
			ps.Total += wa.shots
			ps.ByWeapon = append(ps.ByWeapon, ws)
		}
		if ps.Total > 0 {
			out = append(out, ps)
		}
	}
	return out
}

// reconcile cross-checks detected per-weapon shot counts against the KTX
// end-of-match accuracy block. Diagnostic only — divergence is reported,
// never used to adjust the detected stream.
func (a *ShotsAnalyzer) reconcile(aggByName map[string]*shotAgg) *ShotsReconciliation {
	if a.core == nil || a.core.DemoInfo == nil || len(a.core.DemoInfo.Players) == 0 {
		return nil
	}
	rec := &ShotsReconciliation{ByPlayer: make(map[string][]ShotsDelta)}
	for i := range a.core.DemoInfo.Players {
		p := &a.core.DemoInfo.Players[i]
		ag := aggByName[p.Name]
		var rows []ShotsDelta
		for _, w := range weaponOrder {
			streamShots := 0
			if ag != nil {
				if wa := ag.weapons[w]; wa != nil {
					streamShots = wa.shots
				}
			}
			ktxAtt, ktxHits := 0, 0
			if dw := p.Weapons[w]; dw != nil && dw.Acc != nil {
				ktxAtt, ktxHits = dw.Acc.Attacks, dw.Acc.Hits
			}
			if streamShots == 0 && ktxAtt == 0 && ktxHits == 0 {
				continue
			}
			rows = append(rows, ShotsDelta{
				Weapon:        w,
				StreamShots:   streamShots,
				StreamAttacks: streamShots * ktxAttackMultiplier(w),
				KtxAttacks:    ktxAtt,
				KtxHits:       ktxHits,
			})
		}
		if len(rows) > 0 {
			rec.ByPlayer[p.Name] = rows
		}
	}
	if len(rec.ByPlayer) == 0 {
		return nil
	}
	return rec
}

// resolveAt maps a wire slot to its identity at tMs, mirroring the
// resolution chain used by the damage and frag analyzers.
func (a *ShotsAnalyzer) resolveAt(slot int, tMs int32) SlotInfo {
	id := a.core.SlotIdentityAt(slot, tMs)
	if id.Name == "" && slot >= 0 && slot < len(a.ctx.Players) {
		if p := a.ctx.Players[slot]; p != nil {
			id.Name = p.Name
			if id.Team == "" {
				id.Team = p.Team
			}
		}
	}
	if id.Name != "" && id.Team == "" && a.core != nil && a.core.Names != nil {
		id.Team = a.core.Names.TeamForName(id.Name)
	}
	return id
}

type shotAgg struct {
	team    string
	weapons map[string]*weaponAgg
}

type weaponAgg struct {
	shots int
	hits  int
}

// weaponOrder is the stable output order for per-weapon aggregates and
// reconciliation rows (matches the KTX WpName ordering).
var weaponOrder = []string{"sg", "ssg", "ng", "sng", "gl", "rl", "lg"}

// fireSoundWeapon maps a precached sound path to the weapon it fires, or
// (",false) when the sound is not a weapon-fire sound. The Quake sound
// filenames are historically mismatched with the weapons that play them —
// the rocket launcher fires "weapons/sgun1.wav" and the nailgun fires
// "weapons/rocket1i.wav" (ktx/src/weapons.c W_FireRocket / W_FireSpikes).
// Non-fire weapon sounds (the grenade bounce, the LG hit/start, ricochets,
// spike tinks, the axe) are deliberately excluded.
func fireSoundWeapon(name string) (string, bool) {
	switch name {
	case "weapons/guncock.wav":
		return "sg", true // shotgun (W_FireShotgun)
	case "weapons/shotgn2.wav":
		return "ssg", true // super shotgun (W_FireSuperShotgun)
	case "weapons/sgun1.wav":
		return "rl", true // rocket launcher (W_FireRocket)
	case "weapons/grenade.wav":
		return "gl", true // grenade launcher (W_FireGrenade)
	case "weapons/rocket1i.wav":
		return "ng", true // nailgun (W_FireSpikes)
	case "weapons/spike2.wav":
		return "sng", true // super nailgun (W_FireSuperSpikes)
	case "weapons/coilgun.wav":
		return "sg", true // instagib shotgun-slot railgun
	}
	return "", false
}

// isHitscanWeapon reports whether a weapon's shot and its damage land in the
// same server frame (so they can be linked truthfully). Nail/rocket/grenade
// fires are projectiles and are excluded.
func isHitscanWeapon(w string) bool {
	return w == "sg" || w == "ssg" || w == "lg"
}

// ktxAttackMultiplier converts a discrete fire count into KTX's acc.attacks
// unit: the shotguns count one attack per pellet (6 / 14), every other
// weapon counts one attack per fire. See ktx/src/weapons.c.
func ktxAttackMultiplier(w string) int {
	switch w {
	case "sg":
		return 6
	case "ssg":
		return 14
	default:
		return 1
	}
}

func absInt32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
