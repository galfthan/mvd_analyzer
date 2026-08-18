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
// Pickup tracking is intentionally NOT implemented. The wire-level
// ItemStateEvent stream for backpack edicts produces phantom
// visibility cycles in the 200 ms class (same edict going
// taken/untaken repeatedly without real pickups in between) that
// we cannot currently distinguish from genuine fast pickups. Rather
// than report unreliable data, pickups are deferred to a later
// branch that diagnoses the flutter source first.
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
	mapName   string
	locFinder *locvis.Finder
	timing    MatchTimingDetector
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
	}
	return nil
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
