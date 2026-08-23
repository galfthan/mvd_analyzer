package analyzer

import (
	"sort"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/locvis"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// BackpackAnalyzer emits one BackpackDrop per RL/LG backpack the
// dying player leaves behind. It consumes KTX's
// `//ktx drop <ent> <items> <player_ent>` STUFFCMD_DEMOONLY
// directive (ktx/src/items.c:2740), which the parser surfaces as
// BackpackDropHintEvent. The hint is emitted exactly once per
// real drop and carries the dropper's slot directly, so
// attribution is authoritative — no closest-player snap.
//
// The pickup side of a hinted drop is NOT recorded here — it is a
// weaponPickups row, built from KTX's `//ktx bp` by WeaponPickupsAnalyzer
// and joined on (BackpackEnt, drop time).
//
// The EXPIRY side is, because it has nowhere else to go: KTX's third
// backpack directive, `//ktx expire <ent>` (ktx/src/g_spawn.c:196-210),
// names a pack the server removed untaken and no other section carries it.
// It stamps Fate on the drop row it belongs to — see applyExpireHints.
//
// What this analyzer does also record is the raw backpack-ENTITY track
// (packLives / PopulateCore), which is the pickup evidence on a demo with
// no hints at all. It is collected unconditionally rather than only when
// the reconstruction will need it: whether the reconstruction runs is a
// question about the whole Result that is not answerable during the event
// pass, and the track is a few hundred rows per demo.
//
// Non-RL/LG drops (SSG/NG/SNG/GL/empty) are not surfaced: KTX only
// emits the //ktx drop hint for RL and LG, and the QW protocol
// does not transmit backpack contents as wire-level entity state.
type BackpackAnalyzer struct {
	ctx       *Context
	core      *CoreOutputs
	playerPos map[int][3]float32 // slot -> last-known origin (for drop origin)
	drops     []BackpackDrop
	dropSlots []int // parallel to drops: the dropper's wire slot, resolved at Finalize
	expires   []backpackExpireHint
	mapName   string
	locFinder *locvis.Finder
	timing    MatchTimingDetector

	packOpen  map[int]*PackEntityLife // ent -> the life currently on the wire
	packLives []PackEntityLife
}

// PackEntityLife is one continuous appearance of a `progs/backpack.mdl`
// entity on the wire: from the frame it became visible to the frame it
// stopped being (a pickup, KTX's 120 s SUB_Remove, or the end of the
// recording). Times are match-relative ms, like every other published
// timestamp.
//
// A life is NOT the edict's whole history. KTX `spawn()`s and
// `ent_remove()`s packs (ktx/src/items.c:2701, 2489), so one edict carries
// many packs over a match, and the parser closes the old one whenever the
// model index says the edict changed hands (mvd-reader/parser/entities.go
// diffItemEntity).
type PackEntityLife struct {
	EntNum int
	// Start is the first frame the entity was visible; Spawn is where it was
	// then — for a dropped pack, the victim's origin less 24 (items.c:2703).
	Start int32
	Spawn [3]float32
	// End / Rest are the last frame it was visible and where it was then.
	// Ended is false for a pack still on the wire when the recording stopped
	// — the pack outlived the evidence, which is not the same as expiring.
	End   int32
	Rest  [3]float32
	Ended bool
	// Moves counts the origin updates between Start and End. A pack is
	// tossed (velocity[2] = 300 plus a random horizontal kick,
	// items.c:2856-2858) and settles wherever it lands, so Rest is tracked,
	// never assumed equal to Spawn.
	Moves int
}

// UseCoreOutputs wires CoreOutputs so Finalize can read the clock (co.Clock)
// to rebase drop times to the match-relative origin.
func (a *BackpackAnalyzer) UseCoreOutputs(co *CoreOutputs) { a.core = co }

// IT_* bit values for the //ktx drop ItemFlags argument, mirroring
// ktx/src/items.c:2738 where the hint is emitted.
const (
	itemFlagRL = 1 << 5 // IT_ROCKET_LAUNCHER
	itemFlagLG = 1 << 6 // IT_LIGHTNING
)

// backpackDropZOffset is how far below the victim's origin KTX spawns the
// pack:
//
//	VectorCopy(self->s.v.origin, item->s.v.origin);
//	item->s.v.origin[2] -= 24;                       // ktx/src/items.c:2703-2704
//
// The victim's origin is the player's MIDPOINT, so the pack sits at their
// feet, not inside their chest. Applied on BOTH provenances — the hint path
// here and the reconstruction — because the published origin is where a map
// overlay draws the pack, and drawing it 24 units up is wrong on both.
const backpackDropZOffset = 24

func NewBackpackAnalyzer() *BackpackAnalyzer {
	return &BackpackAnalyzer{
		playerPos: make(map[int][3]float32),
		packOpen:  make(map[int]*PackEntityLife),
	}
}

func (a *BackpackAnalyzer) Name() string { return "backpacks" }

func (a *BackpackAnalyzer) Init(ctx *Context) error {
	a.ctx = ctx
	return nil
}

func (a *BackpackAnalyzer) OnEvent(event events.Event) error {
	switch e := event.(type) {
	case *events.PrintEvent:
		a.timing.OnPrint(e)
	case *events.IntermissionEvent:
		a.timing.OnIntermission(e.TimeMs)
	case *events.StuffTextEvent:
		if strings.HasPrefix(e.Command, "fullserverinfo ") {
			a.extractMapName(e.Command)
		}
	case *events.PlayerPositionEvent:
		a.playerPos[e.PlayerNum] = e.Origin
	case *events.BackpackDropHintEvent:
		a.handleHint(e)
	case *events.BackpackExpireHintEvent:
		// Kept unfiltered by the match window on purpose: the pack this
		// names was created 120 s earlier, so the hint that closes the last
		// drop of a match can legitimately land after the match did. The
		// join in applyExpireHints is what decides whether it belongs to a
		// recorded drop, and it is strictly stronger than a time gate here.
		a.expires = append(a.expires, backpackExpireHint{EntNum: e.BackpackEnt, Time: e.TimeMs})
	case *events.ItemSpawnEvent:
		if e.Kind == packEntityKind {
			a.openPackLife(e.EntNum, e.TimeMs, e.Origin)
		}
	case *events.ItemStateEvent:
		if e.Kind == packEntityKind {
			if e.Taken {
				a.closePackLife(e.EntNum, e.TimeMs, e.Origin)
			} else {
				a.openPackLife(e.EntNum, e.TimeMs, e.Origin)
			}
		}
	case *events.ItemMoveEvent:
		if e.Kind == packEntityKind {
			a.movePackLife(e.EntNum, e.TimeMs, e.Origin)
		}
	}
	return nil
}

// packEntityKind is the ItemSpawnEvent/ItemStateEvent kind for
// `progs/backpack.mdl` (mvd-reader/parser/entities.go modelPathToKind).
const packEntityKind = "backpack"

// openPackLife starts a life unless one is already open on that edict. The
// guard matters because the parser announces a freshly classified entity
// twice in the same frame — ItemSpawnEvent for "this edict is now a
// backpack", then ItemStateEvent{Taken:false} for "and it is visible" —
// and the two are one appearance, not two.
func (a *BackpackAnalyzer) openPackLife(ent int, t int32, origin [3]float32) {
	if a.packOpen[ent] != nil {
		return
	}
	a.packOpen[ent] = &PackEntityLife{EntNum: ent, Start: t, Spawn: origin, Rest: origin}
}

func (a *BackpackAnalyzer) closePackLife(ent int, t int32, origin [3]float32) {
	l := a.packOpen[ent]
	if l == nil {
		return
	}
	l.End, l.Rest, l.Ended = t, origin, true
	a.packLives = append(a.packLives, *l)
	delete(a.packOpen, ent)
}

// movePackLife applies one origin update to the life on that edict.
//
// The update is taken at face value: on this wire an origin change is always
// the SAME pack moving, never a recycled edict masquerading as one. mvdsv
// refuses to reallocate an edict freed less than half a second ago —
//
//	if (e->e.free && (e->e.freetime < 2 || sv.time - e->e.freetime > 0.5))
//	        // mvdsv/src/pr_edict.c:123, with the comment "can cause the
//	        // client to think the entity morphed into something else"
//
// — which at sv_demofps 30 is at least fifteen demo frames with the edict
// absent, so the pack's disappearance is always broadcast before its
// successor appears. A speed gate against sv_maxvelocity was implemented to
// catch the case anyway and measured ZERO hits over 3 205 pack lives, so it
// was removed rather than left in as unexercised machinery.
func (a *BackpackAnalyzer) movePackLife(ent int, t int32, origin [3]float32) {
	l := a.packOpen[ent]
	if l == nil {
		return
	}
	l.Rest = origin
	l.Moves++
}

// handleHint records one BackpackDrop. The hint's PlayerEnt is the
// dropper's edict (player_slot + 1); their most recent position is
// the drop origin (KTX spawns the backpack at the dying player's
// s.v.origin). Defensive: skip on unrecognised flag combos.
//
// The dropper's SLOT is what is recorded here — the name and team are
// resolved in Finalize through the shared ResolveSlotAt chain, so a drop
// carries the same display name every other section uses (an auth-override
// player used to land here under their bare userinfo name, which joined
// against nothing).
func (a *BackpackAnalyzer) handleHint(e *events.BackpackDropHintEvent) {
	if !a.timing.Started || a.timing.Ended {
		return
	}
	weapon := weaponFromItemFlags(e.ItemFlags)
	if weapon == "" {
		return
	}
	slot := e.PlayerEnt - 1
	if slot < 0 || slot >= len(a.ctx.Players) || a.ctx.Players[slot] == nil {
		return
	}
	// The pack's own origin, not the dropper's: KTX offsets it to the
	// victim's feet (backpackDropZOffset). Left at the zero vector when the
	// recorder never carried a position for this slot — the "unknown origin"
	// value the loc resolver and every consumer already read as absent, which
	// a bare -24 would masquerade as a real point on the map.
	origin := [3]float32{}
	if pos, ok := a.playerPos[slot]; ok {
		origin = pos
		origin[2] -= backpackDropZOffset
	}
	a.drops = append(a.drops, BackpackDrop{
		Time:   e.TimeMs,
		Weapon: weapon,
		Origin: origin,
		EntNum: e.BackpackEnt,
		Source: backpackSourceKTX,
	})
	a.dropSlots = append(a.dropSlots, slot)
}

// backpackExpireHint is one `//ktx expire <ent>` directive, at the demo-clock
// instant the server announced it.
type backpackExpireHint struct {
	EntNum int
	Time   int32
}

// backpackExpireToleranceMs bounds how far a `//ktx expire` may sit from its
// drop's own timeout deadline and still be that pack's removal.
//
// The deadline is exact. DropBackpack arms `nextthink = time + 120` with
// `think = SUB_Remove` on every pack it spawns (ktx/src/items.c:2870-2872,
// packExpiryTimeoutMs) and nothing re-arms it, and BOTH hints are timestamped
// in demo time, which is `sv.time` — frozen while the server is paused
// (mvdsv/src/sv_main.c:3296, and MVD_FORMAT.md's paused_duration note: the
// demo time-delta bytes are 0 across a pause), so a pause cannot stretch the
// interval either. What is left is the two demo frames the two directives
// landed in plus the server tick `nextthink` fires on: measured over 330
// archive demos, all 234 expire hints paired at 119 953-120 027 ms after
// their drop, a 74 ms spread. One second is that spread with an order of
// magnitude to spare, and still an order of magnitude tighter than any
// plausible edict recycle.
const backpackExpireToleranceMs = 1000

// applyExpireHints stamps Fate = BackpackFateExpired on the drop whose pack
// KTX announced it had removed untaken.
//
// `//ktx expire <ent>` (ktx/src/g_spawn.c:196-210) is the third and last of
// KTX's RL/LG backpack directives, and the only positive evidence in the demo
// that a pack was NOT taken: the absence of a `//ktx bp` cannot say it,
// because a demo can carry the drop hint and no pickup hints at all (measured:
// 107 of 330 hint-carrying archive demos emit no `//ktx bp` whatsoever).
//
// The join is by edict AND time, never by edict alone: a match recycles a
// backpack edict through many packs, which is why the pickup side joins on
// (BackpackEnt, drop time) too. The pack removed at instant t on edict E is
// the one most recently dropped on E, so this walks back to the newest drop at
// or before the hint and then checks the age against KTX's own timeout. An
// unmatched hint — a warmup pack whose drop is outside the match window, or an
// edict whose drop this analyzer refused — is left on the floor rather than
// attached to whatever row came nearest.
//
// drops must be sorted ascending by Time and carry demo-clock times, i.e. this
// runs after the sort in Finalize and before the match-start rebase.
func (a *BackpackAnalyzer) applyExpireHints(drops []BackpackDrop) {
	if len(a.expires) == 0 {
		return
	}
	byEnt := make(map[int][]int, len(drops))
	for i := range drops {
		byEnt[drops[i].EntNum] = append(byEnt[drops[i].EntNum], i)
	}
	for _, ex := range a.expires {
		newest := -1
		for _, i := range byEnt[ex.EntNum] {
			if drops[i].Time > ex.Time {
				break
			}
			newest = i
		}
		if newest < 0 {
			continue
		}
		age := ex.Time - drops[newest].Time
		if age < packExpiryTimeoutMs-backpackExpireToleranceMs || age > packExpiryTimeoutMs+backpackExpireToleranceMs {
			continue
		}
		drops[newest].Fate = backpackFateExpired
	}
}

func weaponFromItemFlags(flags int) string {
	hasRL := flags&itemFlagRL != 0
	hasLG := flags&itemFlagLG != 0
	switch {
	case hasRL && !hasLG:
		return "rl"
	case hasLG && !hasRL:
		return "lg"
	default:
		// Both bits set or neither → unrecognised. Real KTX drops
		// always send exactly one; anything else gets dropped
		// defensively.
		return ""
	}
}

func (a *BackpackAnalyzer) extractMapName(cmd string) {
	if v, ok := parseInfoString(cmd)["map"]; ok {
		a.mapName = v
	}
}

// PopulateCore publishes the backpack-entity track, rebased to the match
// clock exactly as the drops are. Runs after Finalize (registry.go), and
// unconditionally — Finalize returns early when the demo carried no hints,
// which is precisely the population the track exists for.
func (a *BackpackAnalyzer) PopulateCore(co *CoreOutputs) {
	// A pack still on the wire when the recording stopped is published
	// unended rather than dropped: "we stopped watching" is a fact the
	// linkage needs in order to refuse to call it expired.
	for _, l := range a.packOpen {
		a.packLives = append(a.packLives, *l)
	}
	a.packOpen = map[int]*PackEntityLife{}
	ms := a.core.MatchStartMs()
	for i := range a.packLives {
		a.packLives[i].Start -= ms
		if a.packLives[i].Ended {
			a.packLives[i].End -= ms
		}
	}
	sort.SliceStable(a.packLives, func(i, j int) bool {
		if a.packLives[i].Start != a.packLives[j].Start {
			return a.packLives[i].Start < a.packLives[j].Start
		}
		return a.packLives[i].EntNum < a.packLives[j].EntNum
	})
	co.PackEntities = a.packLives
}

// Finalize returns the collected drops sorted by time, with Loc
// resolved from the map's .loc corpus when available.
func (a *BackpackAnalyzer) Finalize(result *Result) error {
	if len(a.drops) == 0 {
		return nil
	}
	if a.locFinder == nil && a.mapName != "" {
		if f, err := locvis.LoadForMap(a.mapName); err == nil {
			a.locFinder = f
		}
	}
	// Resolve the dropper before sorting, while drops and dropSlots are
	// still parallel. ResolveSlotAt is keyed on the DEMO-clock drop time
	// (the rebase below is the last step), so a reconnect resolves to who
	// held the slot at the drop, not to its final occupant.
	for i := range a.drops {
		info := ResolveSlotAt(a.core, a.ctx.Players, a.dropSlots[i], a.drops[i].Time)
		a.drops[i].Player = info.Name
		a.drops[i].Team = info.Team
	}
	sort.SliceStable(a.drops, func(i, j int) bool { return a.drops[i].Time < a.drops[j].Time })
	a.applyExpireHints(a.drops)
	if a.locFinder != nil {
		for i := range a.drops {
			a.drops[i].Loc = a.locFinder.FindNearest(a.drops[i].Origin[0], a.drops[i].Origin[1], a.drops[i].Origin[2])
		}
	}
	// Born-correct team labels: the roster rewrites a duel participant's team
	// to their own name. Formerly the normalizeDuelTeams backpacks block.
	for i := range a.drops {
		a.drops[i].Team = a.core.TeamFor(a.drops[i].Player, a.drops[i].Team)
	}
	result.Backpacks = a.drops

	// Born-correct timestamps: rebase drop times to the match clock.
	if ms := a.core.MatchStartMs(); ms > 0 {
		for i := range result.Backpacks {
			result.Backpacks[i].Time -= ms
		}
	}
	return nil
}
