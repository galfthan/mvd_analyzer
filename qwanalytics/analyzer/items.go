package analyzer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mvd-analyzer/qwanalytics/loc"
	"github.com/mvd-analyzer/qwdemo/events"
)

// ItemAnalyzer builds the per-item pickup / respawn timeline by
// listening to the ItemSpawnEvent and ItemStateEvent signals the
// parser synthesises from the wire-level entity state stream
// (qwdemo/parser/entities.go). No KTX-print parsing, no BSP-entity
// preprocessing, no position-snap for item identity — the protocol
// itself tells us which entity is which item and when it's taken or
// back up.
//
// Player attribution (TakenBy / Team) still uses the nearest-player
// snap at the moment of pickup, because the entity-state stream
// doesn't carry "who touched it". That's a best-effort label; if
// multiple players are within the touch radius the nearest wins.
type ItemAnalyzer struct {
	ctx       *Context
	items     map[int]*itemEntity // entNum -> tracked item
	playerPos map[int][3]float32  // slot -> last known origin
	mapName   string              // resolved from fullserverinfo, used for loc lookup
	locFinder *loc.Finder
	// Match window — events during warmup and intermission shouldn't
	// create observable phases in the result. Mirrors the same
	// detection logic TimelineAnalyzer uses.
	matchStarted   bool
	matchEnded     bool
	matchStartTime float64
}

type itemEntity struct {
	kind   string
	origin [3]float32
	phases []ItemPhase
}

func NewItemAnalyzer() *ItemAnalyzer {
	return &ItemAnalyzer{
		items:     make(map[int]*itemEntity),
		playerPos: make(map[int][3]float32),
	}
}

func (a *ItemAnalyzer) Name() string { return "items" }

func (a *ItemAnalyzer) Init(ctx *Context) error {
	a.ctx = ctx
	return nil
}

func (a *ItemAnalyzer) OnEvent(event events.Event) error {
	switch e := event.(type) {
	case *events.PrintEvent:
		a.detectMatchBoundary(e)
	case *events.IntermissionEvent:
		if a.matchStarted {
			a.matchEnded = true
		}
	case *events.StuffTextEvent:
		if strings.HasPrefix(e.Command, "fullserverinfo ") {
			a.extractMapName(e.Command)
		}
	case *events.PlayerPositionEvent:
		a.playerPos[e.PlayerNum] = [3]float32{e.Origin[0], e.Origin[1], e.Origin[2]}
	case *events.ItemSpawnEvent:
		a.handleItemSpawn(e)
	case *events.ItemStateEvent:
		a.handleItemState(e)
	}
	return nil
}

func (a *ItemAnalyzer) detectMatchBoundary(e *events.PrintEvent) {
	msg := e.Message
	if !a.matchStarted {
		if strings.Contains(msg, "match has begun") ||
			strings.Contains(msg, "Fight!") ||
			strings.Contains(msg, "begins in 1") ||
			strings.Contains(msg, "Go!") {
			a.matchStarted = true
			a.matchStartTime = e.Time
		}
		return
	}
	if a.matchEnded {
		return
	}
	if strings.Contains(msg, "the match is over") ||
		strings.Contains(msg, "match ended") ||
		strings.Contains(msg, "game over") ||
		strings.Contains(msg, "match complete") ||
		strings.Contains(msg, "timelimit hit") ||
		strings.Contains(msg, "fraglimit hit") {
		a.matchEnded = true
	}
}

func (a *ItemAnalyzer) extractMapName(cmd string) {
	rest := strings.TrimPrefix(cmd, "fullserverinfo ")
	rest = strings.TrimSpace(rest)
	rest = strings.TrimPrefix(rest, "\"")
	if i := strings.LastIndexByte(rest, '"'); i >= 0 {
		rest = rest[:i]
	}
	parts := strings.Split(rest, "\\")
	start := 0
	if len(parts) > 0 && parts[0] == "" {
		start = 1
	}
	for i := start; i+1 < len(parts); i += 2 {
		if parts[i] == "map" {
			a.mapName = parts[i+1]
			return
		}
	}
}

// handleItemSpawn records the item's identity and opens the initial
// available phase. Fires once per entity (or again if a baseline is
// resent mid-match, which is rare).
func (a *ItemAnalyzer) handleItemSpawn(e *events.ItemSpawnEvent) {
	// Ignore dropped backpacks for now — they have their own
	// semantics (one-shot pickup, no respawn). They're classified
	// as "backpack" at the parser layer; filter out here.
	if e.Kind == "" || e.Kind == "backpack" {
		return
	}
	it := a.items[e.EntNum]
	if it == nil {
		it = &itemEntity{
			kind:   e.Kind,
			origin: e.Origin,
			phases: []ItemPhase{{AvailableFrom: 0}},
		}
		a.items[e.EntNum] = it
		return
	}
	// Update position / kind if the baseline changed. Don't touch
	// phases — the existing timeline is authoritative.
	it.kind = e.Kind
	it.origin = e.Origin
}

// handleItemState closes or opens a phase for a tracked item.
// Taken=true → close the current available phase with TakenAt (and
// player attribution). Taken=false → respawn: stamp RespawnAt on the
// last closed phase and open a new available phase starting now.
func (a *ItemAnalyzer) handleItemState(e *events.ItemStateEvent) {
	if !a.matchStarted || a.matchEnded {
		return
	}
	it := a.items[e.EntNum]
	if it == nil {
		return
	}
	if len(it.phases) == 0 {
		it.phases = []ItemPhase{{AvailableFrom: 0}}
	}
	last := &it.phases[len(it.phases)-1]

	if e.Taken {
		// Close the current available phase. If the last phase was
		// already closed (bug or duplicate state event), skip.
		if last.TakenAt > 0 {
			return
		}
		last.TakenAt = e.Time
		playerSlot, name, team := a.attributePickup(it.origin, e.Time)
		_ = playerSlot
		last.TakenBy = name
		last.Team = team
		return
	}

	// Respawn: stamp RespawnAt and open a new phase. If the last
	// phase hasn't been closed yet (received a respawn without a
	// matching take — possible at match start if the initial state
	// registers as "visible but we never saw the take"), just open
	// a new phase at e.Time.
	if last.TakenAt > 0 {
		last.RespawnAt = e.Time
	}
	it.phases = append(it.phases, ItemPhase{AvailableFrom: e.Time})
}

// attributePickup finds the player whose last-known position is
// closest to the item origin. Used to label TakenBy on phase close.
// Returns ("","") if no player has a position recorded yet.
func (a *ItemAnalyzer) attributePickup(itemPos [3]float32, _ float64) (int, string, string) {
	bestSlot := -1
	bestDistSq := float32(1e18)
	for slot, pos := range a.playerPos {
		dx := pos[0] - itemPos[0]
		dy := pos[1] - itemPos[1]
		dz := pos[2] - itemPos[2]
		d := dx*dx + dy*dy + dz*dz
		if d < bestDistSq {
			bestDistSq = d
			bestSlot = slot
		}
	}
	if bestSlot < 0 {
		return -1, "", ""
	}
	if bestSlot >= len(a.ctx.Players) || a.ctx.Players[bestSlot] == nil {
		return bestSlot, "", ""
	}
	pl := a.ctx.Players[bestSlot]
	return bestSlot, pl.Name, pl.Team
}

// Finalize builds the ItemsResult. Item names are kind-scoped
// ("ra", "mh_1", "mh_2", ...) and ordered deterministically by world
// position. Loc labels are attached best-effort from the .loc
// corpus — absent loc file yields empty Loc strings; the item list
// itself is always populated when the demo has any item events.
func (a *ItemAnalyzer) Finalize() (interface{}, error) {
	if len(a.items) == 0 {
		return nil, nil
	}

	// Best-effort loc lookup — does NOT affect whether items appear.
	if a.locFinder == nil && a.mapName != "" {
		if f, err := loc.LoadForMap(a.mapName); err == nil {
			a.locFinder = f
		}
	}

	type entry struct {
		entNum int
		it     *itemEntity
	}
	byKind := map[string][]entry{}
	for ent, it := range a.items {
		byKind[it.kind] = append(byKind[it.kind], entry{entNum: ent, it: it})
	}

	out := make([]ItemTimeline, 0, len(a.items))
	for kind, list := range byKind {
		sort.Slice(list, func(i, j int) bool {
			a, b := list[i].it, list[j].it
			if a.origin[0] != b.origin[0] {
				return a.origin[0] < b.origin[0]
			}
			if a.origin[1] != b.origin[1] {
				return a.origin[1] < b.origin[1]
			}
			return a.origin[2] < b.origin[2]
		})
		for i, e := range list {
			name := kind
			if len(list) > 1 {
				name = fmt.Sprintf("%s_%d", kind, i+1)
			}
			locName := ""
			if a.locFinder != nil {
				locName = a.locFinder.FindNearest(e.it.origin[0], e.it.origin[1], e.it.origin[2])
			}
			out = append(out, ItemTimeline{
				Name:   name,
				Kind:   kind,
				EntNum: e.entNum,
				X:      e.it.origin[0],
				Y:      e.it.origin[1],
				Z:      e.it.origin[2],
				Loc:    locName,
				Phases: e.it.phases,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})

	return &ItemsResult{Items: out}, nil
}
