package parser

import (
	"testing"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

// itemModelList is the model list every test here parses against: a backpack,
// a rocket (the classic recycled tenant of a freed pack edict) and an RL
// spawner (a map item, whose edict is never recycled).
var itemModelList = []string{"", "maps/dm2.bsp", "progs/backpack.mdl", "progs/missile.mdl", "progs/g_rock2.mdl"}

type itemEvents struct {
	spawns []*ItemSpawnEvent
	states []*ItemStateEvent
	moves  []*ItemMoveEvent
}

func newItemParser(t *testing.T) (*Parser, *itemEvents) {
	t.Helper()
	p := NewParser(nil)
	p.modelList = itemModelList
	ev := &itemEvents{}
	p.OnEvent(func(e Event) error {
		switch x := e.(type) {
		case *ItemSpawnEvent:
			ev.spawns = append(ev.spawns, x)
		case *ItemStateEvent:
			ev.states = append(ev.states, x)
		case *ItemMoveEvent:
			ev.moves = append(ev.moves, x)
		}
		return nil
	})
	return p, ev
}

func feedPacket(t *testing.T, p *Parser, timeMs int32, body []byte) {
	t.Helper()
	p.lastEntityPacketTimeMs = timeMs
	if err := p.parsePacketEntities(mvd.NewBufferReader(body), false, false, 0); err != nil {
		t.Fatalf("packet at %dms: %v", timeMs, err)
	}
}

// A dropped backpack falls, so its origin changes while it is visible. Those
// updates are ItemMoveEvent — the item-side twin of MoverStateEvent — and the
// visibility transitions carry the origin at each end.
func TestItemEntity_BackpackFallIsAnOriginTrack(t *testing.T) {
	p, ev := newItemParser(t)
	feedPacket(t, p, 1000, projPacket(205, 2, [3]float32{0, 0, 100}))
	feedPacket(t, p, 1030, projPacket(205, 2, [3]float32{0, 0, 60}))
	feedPacket(t, p, 1060, projPacket(205, 2, [3]float32{0, 0, 24}))
	feedPacket(t, p, 5000, emptyFullPacket())

	if len(ev.spawns) != 1 || ev.spawns[0].Kind != "backpack" || ev.spawns[0].Origin != ([3]float32{0, 0, 100}) {
		t.Fatalf("spawns = %+v, want one backpack at z=100", ev.spawns)
	}
	if len(ev.moves) != 2 {
		t.Fatalf("moves = %d (%+v), want 2", len(ev.moves), ev.moves)
	}
	if ev.moves[0].Origin != ([3]float32{0, 0, 60}) || ev.moves[0].TimeMs != 1030 || ev.moves[0].Kind != "backpack" {
		t.Errorf("move[0] = %+v, want the pack at z=60 at 1030ms", ev.moves[0])
	}
	if ev.moves[1].Origin != ([3]float32{0, 0, 24}) {
		t.Errorf("move[1] origin = %v, want z=24", ev.moves[1].Origin)
	}
	// Two state events: the appearance (announced alongside the spawn, which
	// names the entity, while the state says it is visible) and the removal.
	if len(ev.states) != 2 || ev.states[0].Taken || !ev.states[1].Taken {
		t.Fatalf("states = %+v, want appeared then taken", ev.states)
	}
	if ev.states[0].Origin != ([3]float32{0, 0, 100}) {
		t.Errorf("appearance origin = %v, want the drop position z=100", ev.states[0].Origin)
	}
	if ev.states[1].Origin != ([3]float32{0, 0, 24}) {
		t.Errorf("taken origin = %v, want the LAST visible position z=24", ev.states[1].Origin)
	}
}

// A map item that never moves emits no move events — the whole reason
// ItemMoveEvent is cheap enough to emit for every tracked item.
func TestItemEntity_StaticItemNeverMoves(t *testing.T) {
	p, ev := newItemParser(t)
	feedPacket(t, p, 1000, projPacket(12, 4, [3]float32{500, 500, 0}))
	feedPacket(t, p, 1030, projPacket(12, 4, [3]float32{500, 500, 0}))
	feedPacket(t, p, 2000, emptyFullPacket())
	feedPacket(t, p, 12000, projPacket(12, 4, [3]float32{500, 500, 0}))
	if len(ev.moves) != 0 {
		t.Errorf("moves = %+v, want none for a spawner sitting on its pad", ev.moves)
	}
	if len(ev.states) != 3 || ev.states[0].Taken || !ev.states[1].Taken || ev.states[2].Taken {
		t.Fatalf("states = %+v, want appeared / taken / respawned", ev.states)
	}
	for i, s := range ev.states {
		if s.Kind != "rl" || s.Origin != ([3]float32{500, 500, 0}) {
			t.Errorf("state[%d] = %+v, want the rl pad's own origin", i, s)
		}
	}
}

// The model index is the authority on what an edict currently is. KTX frees a
// picked-up pack's edict and `spawn()` hands it to the next entity, so a
// cached kind that outlives the pack reports every later tenant's appearance
// and disappearance as that pack fluttering.
func TestItemEntity_RecycledEdictEndsTheOldItem(t *testing.T) {
	p, ev := newItemParser(t)
	feedPacket(t, p, 1000, projPacket(205, 2, [3]float32{0, 0, 0}))   // backpack
	feedPacket(t, p, 4000, projPacket(205, 3, [3]float32{900, 0, 0})) // rocket on the same edict
	feedPacket(t, p, 4200, emptyFullPacket())

	if len(ev.states) != 2 {
		t.Fatalf("states = %+v, want the pack appearing and then going away", ev.states)
	}
	s := ev.states[1]
	if s.Kind != "backpack" || !s.Taken || s.TimeMs != 4000 {
		t.Errorf("state = %+v, want the backpack taken at 4000ms", s)
	}
	if s.Origin != ([3]float32{0, 0, 0}) {
		t.Errorf("taken origin = %v, want the pack's own last position, not the rocket's", s.Origin)
	}
	if p.spawnedItems[205] != "" {
		t.Errorf("ent 205 still classified %q — the rocket is not a tracked item kind", p.spawnedItems[205])
	}
	if len(ev.moves) != 0 {
		t.Errorf("moves = %+v, want none — the edict changed hands, the pack did not travel 900 units", ev.moves)
	}
}

// The same edict re-used by ANOTHER backpack is two items, not one that
// teleported: the second gets its own spawn at its own origin.
func TestItemEntity_RecycledEdictSameKindStartsANewItem(t *testing.T) {
	p, ev := newItemParser(t)
	feedPacket(t, p, 1000, projPacket(205, 2, [3]float32{0, 0, 0}))
	feedPacket(t, p, 4000, projPacket(205, 3, [3]float32{900, 0, 0})) // rocket frees the classification
	feedPacket(t, p, 6000, projPacket(205, 2, [3]float32{900, 500, 0}))
	feedPacket(t, p, 9000, emptyFullPacket())

	if len(ev.spawns) != 2 {
		t.Fatalf("spawns = %+v, want two backpacks", ev.spawns)
	}
	if ev.spawns[1].Origin != ([3]float32{900, 500, 0}) || ev.spawns[1].TimeMs != 6000 {
		t.Errorf("second spawn = %+v, want the new pack where the wire put it at 6000ms", ev.spawns[1])
	}
	if len(ev.states) != 4 {
		t.Fatalf("states = %+v, want appeared / taken / re-appeared / taken", ev.states)
	}
	if ev.states[2].Taken || ev.states[2].Origin != ([3]float32{900, 500, 0}) {
		t.Errorf("state[2] = %+v, want the second pack appearing at its own origin", ev.states[2])
	}
}

// An edict that was never classified while visible (its model became
// nameable only as it left the frame — the `kind == "" && o != nil` arm of
// diffItemEntity) arrives here with s == nil. Archive demo
// 1a78d75f8bd1234035e2787cd0db2829a933c77e845aa1f4e9b033974fa9c097
// crashed the parser on that path: the origin was read from s before the
// nil test. The spawn must come out at the old state's origin instead.
func TestItemEntity_NameableOnlyOnLeavingDoesNotDereferenceNil(t *testing.T) {
	p, ev := newItemParser(t)
	feedPacket(t, p, 1000, projPacket(205, 2, [3]float32{10, 20, 30})) // backpack, classified
	old := p.entCur[205]
	p.spawnedItems[205] = "" // never classified while visible
	if err := p.diffItemEntity(205, nil, &old, 2000); err != nil {
		t.Fatalf("diffItemEntity: %v", err)
	}
	if len(ev.spawns) == 0 {
		t.Fatalf("spawns = %+v, want the item spawned from its past state", ev.spawns)
	}
	last := ev.spawns[len(ev.spawns)-1]
	if last.Origin != ([3]float32{10, 20, 30}) || last.TimeMs != 2000 {
		t.Errorf("spawn = %+v, want the old state's origin at 2000ms", last)
	}
}
