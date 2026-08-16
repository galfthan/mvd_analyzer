package parser

import (
	"sort"
	"strconv"
	"strings"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

// This file implements the subset of the QW entity-state protocol we need
// to observe item pickups and respawns directly from the wire. The logic
// mirrors ezquake's CL_ParseBaseline / CL_ParseDelta / CL_ParsePacketEntities
// (ezquake-source/src/cl_ents.c:487-810, cl_parse.c:1817-1883). Only the
// fields that matter for item identity (modelindex, origin, skin) are
// decoded; the rest of each delta is skipped.

// Entity-delta flag bits. The 16-bit flag word carries the high-order
// field bits plus the 9-bit entity number; U_MOREBITS pulls in a
// low-order byte, and (on FTE streams) U_FTE_EVENMORE pulls in one or
// two "evenmorebits" bytes. This is the single definition for the one
// entity-delta reader in this file. Values match ezquake CL_ParseDelta
// (ezquake-source/src/cl_ents.c:487-605) and qwprot protocol.h.
const (
	// High bits of the 16-bit flag word (low 9 bits are the entity number).
	uOrigin1  = 1 << 9
	uOrigin2  = 1 << 10
	uOrigin3  = 1 << 11
	uAngle2   = 1 << 12
	uFrame    = 1 << 13
	uRemove   = 1 << 14
	uMoreBits = 1 << 15
	// Low-order byte, read when U_MOREBITS is set.
	uAngle1      = 1 << 0
	uAngle3      = 1 << 1
	uModel       = 1 << 2
	uColormap    = 1 << 3
	uSkin        = 1 << 4
	uEffects     = 1 << 5
	uSolid       = 1 << 6 // U_SOLID: no payload in QW
	uFTEEvenMore = 1 << 7
	// FTE evenmorebits byte(s), read when U_FTE_EVENMORE is set and an
	// FTE extension was negotiated.
	uFTEScale     = 1 << 0
	uFTETrans     = 1 << 1
	uFTEFatness   = 1 << 2
	uFTEModelDbl  = 1 << 3
	uFTEEntityDbl = 1 << 5
	uFTEEntity2   = 1 << 6
	uFTEYetMore   = 1 << 7
	uFTEColourMod = 1 << 10 // bit 2 of the high evenmorebits byte
)

// EntityState is the subset of fields we care about per entity.
type EntityState struct {
	ModelIndex int
	SkinNum    int
	Frame      int
	Colormap   int
	Effects    int
	Origin     [3]float32
	Present    bool // true if entity is in the current frame
}

// ItemSpawnEvent fires once per recognised item entity, the first time
// the demo stream makes that entity observable. Carries the item's
// classification so downstream consumers don't need to re-derive it.
type ItemSpawnEvent struct {
	EntNum int
	Kind   string // "ra","ya","ga","mh","h25","h15","rl","lg",...,"quad","pent","ring","suit","shells","nails","rockets","cells"
	Origin [3]float32
	TimeMs int32
}

func (e *ItemSpawnEvent) EventType() EventType { return EventItemSpawn }
func (e *ItemSpawnEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *ItemSpawnEvent) EventTimeMs() int32   { return e.TimeMs }

// ItemStateEvent fires on every visibility transition of a tracked item
// entity. Taken=true means the item became invisible (picked up);
// Taken=false means it reappeared (respawned).
//
// A re-baseline of an already-taken item does NOT emit an event: it
// silently reseeds the current-frame state (registerBaseline), so the
// next frame diff sees no transition. This is safe because mvdsv only
// writes svc_spawnbaseline in the initial gamestate flush
// (SV_MVD_SendInitialGamestate, mvdsv/src/sv_demo.c:1418-1453) — every
// baseline lands at t=0, before any pickup, so an item is never
// re-baselined mid-game in a single-map MVD. (Verified: zero re-baselines
// across the golden corpus.) A future multi-map / QTV source that crosses
// a gamestate boundary would need the reset handled the way the mover
// branch does — see the resent-baseline case in registerBaseline.
type ItemStateEvent struct {
	EntNum int
	Kind   string
	Taken  bool
	TimeMs int32
}

func (e *ItemStateEvent) EventType() EventType { return EventItemState }
func (e *ItemStateEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *ItemStateEvent) EventTimeMs() int32   { return e.TimeMs }

// MoverSpawnEvent fires once per inline brush-model entity — an entity
// whose model is a "*N" submodel of the map BSP (func_plat, func_door,
// func_train, func_button, func_wall, func_illusionary) — the first
// time the demo stream makes it observable (normally its
// svc_spawnbaseline). Triggers never appear: Quake progs InitTrigger
// clears their model, and mvdsv only writes entities with a non-zero
// modelindex and model (sv_ents.c:790). SubModel is N, the index of
// the BSP submodel whose geometry the entity poses; Origin is the
// entity origin at spawn — the model-space offset the engine traces
// that submodel at (ezquake cl_ents.c CL_SetSolidEntities).
type MoverSpawnEvent struct {
	EntNum   int
	Model    string // inline model path from the precache list: "*1", "*2", ...
	SubModel int    // N — index into the map BSP's submodel (dmodel) array
	Origin   [3]float32
	TimeMs   int32
}

func (e *MoverSpawnEvent) EventType() EventType { return EventMoverSpawn }
func (e *MoverSpawnEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *MoverSpawnEvent) EventTimeMs() int32   { return e.TimeMs }

// MoverStateEvent fires on every wire-state change of a tracked mover:
// its origin moved (the lift/door/train travelling — MVD deltas only
// re-send a changed origin, so between events the pose is exactly the
// last value) or its visibility flipped (modelindex cleared / U_REMOVE,
// then restored). Origin is the entity origin of the new state; for a
// newly-invisible mover it is the last visible origin.
type MoverStateEvent struct {
	EntNum  int
	Origin  [3]float32
	Visible bool
	TimeMs  int32 // wire-native demo ms, same clock as PlayerPositionEvent.TimeMs
}

func (e *MoverStateEvent) EventType() EventType { return EventMoverState }
func (e *MoverStateEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *MoverStateEvent) EventTimeMs() int32   { return e.TimeMs }

// ProjectileSpawnEvent fires the first frame a slow-projectile entity —
// a rocket (`progs/missile.mdl`) or grenade (`progs/grenade.mdl`) — is
// observed on the wire. The wire carries no owner field, so attribution
// is left to the analyzer: the projectile spawns at the firer's muzzle the
// same frame as their RL/GL fire sound, and its entity number brackets the
// flight (spawn → despawn) so a specific shot links to a specific impact.
// Origin is the spawn position (the muzzle).
type ProjectileSpawnEvent struct {
	EntNum int
	Kind   string // "rl" (rocket) | "gl" (grenade)
	Origin [3]float32
	TimeMs int32
}

func (e *ProjectileSpawnEvent) EventType() EventType { return EventProjectileSpawn }
func (e *ProjectileSpawnEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *ProjectileSpawnEvent) EventTimeMs() int32   { return e.TimeMs }

// ProjectileDespawnEvent fires when a tracked projectile entity leaves the
// wire — removed on impact (T_MissileTouch → explosion + radius damage) or
// on timeout. Origin is the last observed position before removal. The
// despawn frame co-locates with the projectile's `TE_EXPLOSION` and, when
// it hit a player, its `mvdhidden_dmgdone` damage, so the analyzer links
// the launching shot to that damage by attacker + this time.
type ProjectileDespawnEvent struct {
	EntNum int
	Kind   string
	Origin [3]float32
	TimeMs int32
}

func (e *ProjectileDespawnEvent) EventType() EventType { return EventProjectileDespawn }
func (e *ProjectileDespawnEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *ProjectileDespawnEvent) EventTimeMs() int32   { return e.TimeMs }

// modelPathToKind maps standard Quake 1 item model paths to the compact
// kind strings we surface in the Result schema. Unrecognised paths
// (player models, projectiles, gibs, etc.) return "" so the analyzer
// can filter non-items cheaply.
//
// Armors all share progs/armor.mdl; the skin disambiguates GA/YA/RA
// (the disambiguation is inline in classifyItem). Every other item is
// unambiguous from the path alone.
//
// Sources cross-referenced: ktx/src/items.c setmodel() calls for each
// item class, plus Quake 1 progs (id Software originals).
var modelPathToKind = map[string]string{
	"maps/b_bh10.bsp":    "h15",
	"maps/b_bh25.bsp":    "h25",
	"maps/b_bh100.bsp":   "mh",
	"progs/g_shot.mdl":   "ssg",
	"progs/g_nail.mdl":   "ng",
	"progs/g_nail2.mdl":  "sng",
	"progs/g_rock.mdl":   "gl",
	"progs/g_rock2.mdl":  "rl",
	"progs/g_light.mdl":  "lg",
	"maps/b_shell0.bsp":  "shells",
	"maps/b_shell1.bsp":  "shells",
	"maps/b_nail0.bsp":   "nails",
	"maps/b_nail1.bsp":   "nails",
	"maps/b_rock0.bsp":   "rockets",
	"maps/b_rock1.bsp":   "rockets",
	"maps/b_batt0.bsp":   "cells",
	"maps/b_batt1.bsp":   "cells",
	"progs/quaddama.mdl": "quad",
	"progs/invulner.mdl": "pent",
	"progs/invisibl.mdl": "ring",
	"progs/suit.mdl":     "suit",
	"progs/backpack.mdl": "backpack",
}

// classifyItem returns the compact kind string for an entity based on
// its model path and (for armor) skin number. Empty string means
// "not a tracked item kind".
func classifyItem(modelPath string, skin int) string {
	// Armors share one model; skin selects GA/YA/RA.
	if strings.EqualFold(modelPath, "progs/armor.mdl") {
		switch skin {
		case 0:
			return "ga"
		case 1:
			return "ya"
		case 2:
			return "ra"
		}
		return ""
	}
	return modelPathToKind[strings.ToLower(modelPath)]
}

// projectilePathToKind maps the slow-projectile model paths we track to
// their weapon kind. Nails (`progs/spike.mdl` / `progs/s_spike.mdl`) ride
// the separate svc_nails stream, not packet entities, so they are not here.
// Sources: ktx/src/weapons.c setmodel() — W_FireRocket → missile.mdl,
// W_FireGrenade → grenade.mdl.
var projectilePathToKind = map[string]string{
	"progs/missile.mdl": "rl",
	"progs/grenade.mdl": "gl",
}

// classifyProjectile returns the weapon kind for a projectile model path,
// or "" when the path is not a tracked projectile.
func classifyProjectile(modelPath string) string {
	return projectilePathToKind[strings.ToLower(modelPath)]
}

// nailModels are the spike models nailguns fire. On servers with sv_nailhack
// (common) nails travel as ordinary packet entities with these models rather
// than the compact svc_nails stream, so the projectile tracker can bracket
// them like rockets. They are tagged "nail" (weapon-agnostic): svc_nails is
// untyped and the spike-vs-super-spike model split is unreliable, so ng/sng is
// resolved downstream from the DtNG/DtSNG damage type, not the model.
var nailModels = map[string]bool{
	"progs/spike.mdl":   true,
	"progs/s_spike.mdl": true,
}

// classifyNail returns "nail" for a spike model path, else "".
func classifyNail(modelPath string) string {
	if nailModels[strings.ToLower(modelPath)] {
		return "nail"
	}
	return ""
}

// classifyMover reports whether modelPath names an inline brush
// submodel of the map BSP ("*1", "*2", …) and returns the submodel
// index. Submodel 0 is the worldspawn itself and never appears as an
// entity model, so it is rejected along with non-inline paths.
func classifyMover(modelPath string) (int, bool) {
	if len(modelPath) < 2 || modelPath[0] != '*' {
		return 0, false
	}
	n, err := strconv.Atoi(modelPath[1:])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// resolveModel returns the model path for a modelindex from the
// parser's model list, empty string if the index is out of range or
// the model is the null model (index 0).
func (p *Parser) resolveModel(modelIndex int) string {
	if modelIndex <= 0 || modelIndex >= len(p.modelList) {
		return ""
	}
	return p.modelList[modelIndex]
}

// modelClass caches the classify* results for one model-list slot so the
// per-frame entity diff doesn't re-run the string lookups (ToLower + map)
// on every entity of every frame. Rebuilt lazily by classOf; parseModelList
// invalidates the memo whenever the list changes.
type modelClass struct {
	itemKind string // classifyItem result for non-armor paths ("" = not a tracked item)
	isArmor  bool   // progs/armor.mdl — kind depends on the skin, resolved in itemKindOf
	projKind string // classifyProjectile result
	isNail   bool   // sv_nailhack spike model (classifyNail)
	mover    int    // classifyMover submodel index (0 = not an inline brush model)
}

// classOf returns the memoized classification for a model index, or nil
// when the index is out of range / the null model — the same cases where
// resolveModel returns "".
func (p *Parser) classOf(modelIndex int) *modelClass {
	if modelIndex <= 0 || modelIndex >= len(p.modelList) {
		return nil
	}
	if len(p.modelClass) != len(p.modelList) {
		p.modelClass = make([]modelClass, len(p.modelList))
		for i, path := range p.modelList {
			if path == "" {
				continue
			}
			c := &p.modelClass[i]
			c.isArmor = strings.EqualFold(path, "progs/armor.mdl")
			if !c.isArmor {
				c.itemKind = classifyItem(path, 0)
			}
			c.projKind = classifyProjectile(path)
			c.isNail = classifyNail(path) != ""
			c.mover, _ = classifyMover(path)
		}
	}
	return &p.modelClass[modelIndex]
}

// itemKindOf is the memoized equivalent of
// classifyItem(p.resolveModel(modelIndex), skin).
func (p *Parser) itemKindOf(modelIndex, skin int) string {
	c := p.classOf(modelIndex)
	if c == nil {
		return ""
	}
	if c.isArmor {
		switch skin {
		case 0:
			return "ga"
		case 1:
			return "ya"
		case 2:
			return "ra"
		}
		return ""
	}
	return c.itemKind
}

// ensureEnt grows every per-entity slice so entity number n is a valid
// index and bumps entLimit (the diff scan bound). Entity numbers are
// 0..2047 on the wire (9 bits + FTE entitydbl), so one allocation covers
// a whole demo; the growth path beyond that exists only for defence
// against malformed svc_spawnbaseline entity numbers.
func (p *Parser) ensureEnt(n int) {
	if n >= len(p.entCur) {
		size := 2048
		for size <= n {
			size *= 2
		}
		grow := func(s []EntityState) []EntityState {
			ns := make([]EntityState, size)
			copy(ns, s)
			return ns
		}
		p.entCur = grow(p.entCur)
		p.entPrev = grow(p.entPrev)
		p.baselines = grow(p.baselines)
		nb := make([]bool, size)
		copy(nb, p.baselineValid)
		p.baselineValid = nb
		growS := func(s []string) []string {
			ns := make([]string, size)
			copy(ns, s)
			return ns
		}
		p.spawnedProjectiles = growS(p.spawnedProjectiles)
		p.spawnedItems = growS(p.spawnedItems)
		nm := make([]int, size)
		copy(nm, p.spawnedMovers)
		p.spawnedMovers = nm
	}
	if n+1 > p.entLimit {
		p.entLimit = n + 1
	}
}

// parseSpawnBaseline decodes svc_spawnbaseline (2-byte entnum +
// baseline body). Mirrors ezquake CL_ParseBaseline at cl_parse.c:1817.
func (p *Parser) parseSpawnBaseline(r *mvd.BufferReader, timeMs int32, floatCoords bool) error {
	ent, err := r.ReadUint16()
	if err != nil {
		return err
	}
	state, err := readBaselineBody(r, floatCoords)
	if err != nil {
		return err
	}
	return p.registerBaseline(int(ent), state, timeMs)
}

// readBaselineBody decodes the fixed baseline layout — model(1) +
// frame(1) + colormap(1) + skin(1) + 3×(coord + angle byte) — that
// follows the 2-byte entity number in svc_spawnbaseline and stands
// alone (no entity-number prefix) in svc_spawnstatic. Single
// implementation of the layout for both the parse and the
// decode-and-discard (skipCommand) callers. Mirrors ezquake
// CL_ParseBaseline (cl_parse.c:1817).
// angleSize is the wire width of one entity angle: 1 byte normally, a
// 2-byte short when FTE_PEXT_FLOATCOORDS was negotiated (sv_bigcoords
// raises msg_anglesize with msg_coordsize).
func angleSize(floatCoords bool) int {
	if floatCoords {
		return 2
	}
	return 1
}

func readBaselineBody(r *mvd.BufferReader, floatCoords bool) (*EntityState, error) {
	modelIdx, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	frame, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	colormap, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	skin, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	var origin [3]float32
	for i := 0; i < 3; i++ {
		if floatCoords {
			origin[i], err = r.ReadFloatCoord()
		} else {
			origin[i], err = r.ReadCoord()
		}
		if err != nil {
			return nil, err
		}
		// angle follows each coord — discard. Its width follows the
		// FTE_PEXT_FLOATCOORDS negotiation like the coords: sv_bigcoords
		// servers set msg_anglesize = 2 alongside msg_coordsize = 4
		// (mvdsv/src/sv_init.c:326-336; client mirror ezquake
		// com_msg.c MSG_ReadAngle), so a fixed 1-byte read desyncs
		// every entity on those demos.
		if err := r.Skip(angleSize(floatCoords)); err != nil {
			return nil, err
		}
	}
	return &EntityState{
		ModelIndex: int(modelIdx),
		Frame:      int(frame),
		Colormap:   int(colormap),
		SkinNum:    int(skin),
		Origin:     origin,
		Present:    true,
	}, nil
}

// parseSpawnBaseline2 handles the FTE extended form
// (svc_fte_spawnbaseline2 / svc_fte_spawnstatic2): the payload starts
// with a 2-byte delta flag word and uses the same wire encoding as a
// packetentities delta. The entity number comes out of the delta's
// low 9 bits (plus U_FTE_ENTITYDBL extensions).
func (p *Parser) parseSpawnBaseline2(r *mvd.BufferReader, timeMs int32, floatCoords bool) error {
	word, err := r.ReadUint16()
	if err != nil {
		return err
	}
	state, entNum, err := p.readEntityDelta(r, uint32(word), &EntityState{}, floatCoords, p.fteExtensions)
	if err != nil {
		return err
	}
	return p.registerBaseline(entNum, state, timeMs)
}

// registerBaseline stores a baseline, seeds the current-frame state
// from it (so the item starts "up"), emits ItemSpawnEvent if this is
// a tracked item kind and MoverSpawnEvent if it is an inline
// brush-model entity.
func (p *Parser) registerBaseline(entNum int, state *EntityState, timeMs int32) error {
	p.ensureEnt(entNum)
	prev := p.entCur[entNum] // zero value (Present=false) doubles as "no prior state"
	p.baselines[entNum] = *state
	p.baselineValid[entNum] = true
	// A baseline replacing a prior one is rare but legal (server can
	// resend). The current-frame state reflects the fresh baseline.
	p.entCur[entNum] = *state

	// Classify against the model list. If the model list hasn't been
	// received yet (rare — svc_modellist normally precedes baselines),
	// re-classification runs again in diffEntityTransitions.
	kind := p.itemKindOf(state.ModelIndex, state.SkinNum)
	if kind != "" && p.spawnedItems[entNum] == "" {
		p.spawnedItems[entNum] = kind
		return p.emit(&ItemSpawnEvent{
			EntNum: entNum,
			Kind:   kind,
			Origin: state.Origin,
			TimeMs: timeMs,
		})
	}
	if c := p.classOf(state.ModelIndex); c != nil && c.mover > 0 {
		if p.spawnedMovers[entNum] == 0 {
			p.spawnedMovers[entNum] = c.mover
			return p.emit(&MoverSpawnEvent{
				EntNum:   entNum,
				Model:    p.resolveModel(state.ModelIndex),
				SubModel: c.mover,
				Origin:   state.Origin,
				TimeMs:   timeMs,
			})
		}
		// A resent baseline for a known mover resets its pose; surface
		// it as a state change when it actually differs.
		if !prev.Present || prev.Origin != state.Origin || prev.ModelIndex == 0 {
			return p.emit(&MoverStateEvent{
				EntNum:  entNum,
				Origin:  state.Origin,
				Visible: true,
				TimeMs:  timeMs,
			})
		}
	}
	return nil
}

// parsePacketEntities decodes svc_packetentities (full) or
// svc_deltapacketentities (delta). The MVD format implicitly uses the
// prior frame as the delta reference (cl_ents.c:653-654), so we just
// keep one rolling "current" state.
//
// Key MVD-recording invariant (mvdsv/src/sv_ents.c:851): MVD packets
// ignore PVS entirely. The only filter on whether an entity appears
// in the packet is `modelindex != 0 && model[] != ""` (sv_ents.c:790).
// So for item tracking, "entity absent from packet" genuinely means
// "its model was cleared" — i.e. it was picked up.
func (p *Parser) parsePacketEntities(r *mvd.BufferReader, delta, floatCoords bool, fteExt uint32) error {
	if delta {
		// Consume the 1-byte "from" sequence — MVD deltas always
		// reference the immediately prior frame, so we don't need the
		// index.
		if _, err := r.ReadByte(); err != nil {
			return err
		}
	}

	// entCur becomes the entity set after applying this packet. For the
	// transition diff, each mentioned entity's pre-mutation state is
	// recorded in entScratch — an unmentioned entity cannot transition
	// (its Present flag, model, and origin are all unchanged, and its
	// classification can only change when the model list does, which
	// classifyAllPending catches), so the diff only needs the mentioned
	// ones. A FULL packet is the exception: entities absent from it
	// vanish without a U_REMOVE mention, so it snapshots the whole
	// frame into entPrev and diffs every slot.
	//
	// FULL packet: the packet *is* the current visible set. Start
	//   empty (clear every Present flag); whatever lands in the packet
	//   becomes the whole current state.
	// DELTA packet: packet describes changes relative to the prior
	//   frame. Keep current state, apply deltas on top (U_REMOVE
	//   deletes, other flags update).
	if !delta {
		copy(p.entPrev[:p.entLimit], p.entCur[:p.entLimit])
		for i := range p.entCur[:p.entLimit] {
			p.entCur[i].Present = false
		}
	}
	p.entScratch = p.entScratch[:0]
	sorted := true

	for {
		word, err := r.ReadUint16()
		if err != nil {
			return err
		}
		if word == 0 {
			break
		}

		bits, morebits, entNum, err := readDeltaBits(r, uint32(word), fteExt)
		if err != nil {
			return err
		}
		p.ensureEnt(entNum)
		if n := len(p.entScratch); n == 0 || p.entScratch[n-1].ent != entNum {
			if n > 0 && p.entScratch[n-1].ent > entNum {
				sorted = false
			}
			p.entScratch = append(p.entScratch, entDelta{ent: entNum, old: p.entCur[entNum]})
		}

		if bits&uRemove != 0 {
			p.entCur[entNum].Present = false
			continue
		}

		// "From" state for the delta: prior frame's entry, else
		// baseline, else the zero state. Matches ezquake cl_ents.c:807.
		base := p.entCur[entNum]
		if !base.Present {
			if p.baselineValid[entNum] {
				base = p.baselines[entNum]
			} else {
				base = EntityState{}
			}
		}
		if err := p.applyDeltaFields(r, bits, morebits, &base, floatCoords, fteExt); err != nil {
			return err
		}
		base.Present = true
		p.entCur[entNum] = base
	}

	if !sorted {
		// The wire writes entities in ascending order (mvdsv
		// SV_EmitPacketEntities walks both frames in parallel); tolerate
		// a violation by restoring the ascending diff order, keeping the
		// first-recorded (pre-packet) state per entity.
		sort.SliceStable(p.entScratch, func(i, j int) bool { return p.entScratch[i].ent < p.entScratch[j].ent })
		dst := p.entScratch[:0]
		for _, d := range p.entScratch {
			if n := len(dst); n > 0 && dst[n-1].ent == d.ent {
				continue
			}
			dst = append(dst, d)
		}
		p.entScratch = dst
	}

	if !delta {
		return p.diffEntityTransitionsFull(p.lastEntityPacketTimeMs)
	}
	return p.diffEntityTransitions(p.lastEntityPacketTimeMs)
}

// entDelta records one packet-mentioned entity's state as it was before
// the packet mutated it — the "old" side of the frame diff.
type entDelta struct {
	ent int
	old EntityState
}

// readDeltaBits reads the low-order U_MOREBITS byte and the FTE
// evenmorebits byte(s) that follow a 16-bit entity flag word, returning
// the full flag word `bits` (entity-number bits cleared), the FTE
// `morebits`, and the resolved entity number (including the FTE
// entitydbl adjustments). This is the single flag-word reader shared by
// parsePacketEntities and readEntityDelta.
//
// The FTE "even more" flag bytes are present only when an FTE extension
// was negotiated: mvdsv gates every evenmorebits emission on
// fte_extensions (mvdsv/src/sv_ents.c:216-235) and ezquake gates the
// read on cls.fteprotocolextensions being non-zero (cl_ents.c:505).
// Reading them unconditionally would consume a stray byte on a non-FTE
// stream — the F3 divergence between the old parse and skip paths.
func readDeltaBits(r *mvd.BufferReader, word, fteExt uint32) (bits, morebits uint32, entNum int, err error) {
	bits = word
	entNum = int(bits & 511)
	bits &= ^uint32(511)

	if bits&uMoreBits != 0 {
		b, berr := r.ReadByte()
		if berr != nil {
			return 0, 0, 0, berr
		}
		bits |= uint32(b)
	}
	if bits&uFTEEvenMore != 0 && fteExt != 0 {
		b, berr := r.ReadByte()
		if berr != nil {
			return 0, 0, 0, berr
		}
		morebits = uint32(b)
		if morebits&uFTEYetMore != 0 {
			b2, berr := r.ReadByte()
			if berr != nil {
				return 0, 0, 0, berr
			}
			morebits |= uint32(b2) << 8
		}
	}
	if morebits&uFTEEntityDbl != 0 {
		entNum += 512
	}
	if morebits&uFTEEntity2 != 0 {
		entNum += 1024
	}
	return bits, morebits, entNum, nil
}

// readEntityDelta decodes one entity delta given its leading 16-bit flag
// word: it reads the FTE morebits, resolves the entity number, and reads
// the field payload. This is the single whole-delta reader; the
// svc_fte_spawnbaseline2 / svc_fte_spawnstatic2 sites both route through
// it. Mirrors ezquake CL_ParseDelta (cl_ents.c:487-605).
func (p *Parser) readEntityDelta(r *mvd.BufferReader, word uint32, from *EntityState, floatCoords bool, fteExt uint32) (*EntityState, int, error) {
	bits, morebits, entNum, err := readDeltaBits(r, word, fteExt)
	if err != nil {
		return nil, 0, err
	}
	state := *from
	if err := p.applyDeltaFields(r, bits, morebits, &state, floatCoords, fteExt); err != nil {
		return nil, 0, err
	}
	state.Present = true
	return &state, entNum, nil
}

// applyDeltaFields reads the entity-delta field payload after the flag
// word(s) have been consumed (by readDeltaBits) and applies it to
// *state in place (state starts as the "from" entity and each set flag
// bit overwrites one field). The FTE trans / colourmod fields are gated
// on the negotiated extension, exactly as both mvdsv's writer
// (sv_ents.c:217, 226) and ezquake's reader (cl_ents.c:580, 586) gate
// them — the flag bit alone is not enough to decide the field is present.
func (p *Parser) applyDeltaFields(r *mvd.BufferReader, bits, morebits uint32, state *EntityState, floatCoords bool, fteExt uint32) error {
	if bits&uModel != 0 {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		state.ModelIndex = int(b)
		if morebits&uFTEModelDbl != 0 {
			state.ModelIndex += 256
		}
	} else if morebits&uFTEModelDbl != 0 {
		mi, err := r.ReadUint16()
		if err != nil {
			return err
		}
		state.ModelIndex = int(mi)
	}
	if bits&uFrame != 0 {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		state.Frame = int(b)
	}
	if bits&uColormap != 0 {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		state.Colormap = int(b)
	}
	if bits&uSkin != 0 {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		state.SkinNum = int(b)
	}
	if bits&uEffects != 0 {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		state.Effects = int(b)
	}
	// Origin + angles are paired per axis (origin N + angle N). Angle
	// width follows FTE_PEXT_FLOATCOORDS like the coord width — see
	// readBaselineBody.
	readCoord := func() (float32, error) {
		if floatCoords {
			return r.ReadFloatCoord()
		}
		return r.ReadCoord()
	}
	if bits&uOrigin1 != 0 {
		v, err := readCoord()
		if err != nil {
			return err
		}
		state.Origin[0] = v
	}
	if bits&uAngle1 != 0 {
		if err := r.Skip(angleSize(floatCoords)); err != nil {
			return err
		}
	}
	if bits&uOrigin2 != 0 {
		v, err := readCoord()
		if err != nil {
			return err
		}
		state.Origin[1] = v
	}
	if bits&uAngle2 != 0 {
		if err := r.Skip(angleSize(floatCoords)); err != nil {
			return err
		}
	}
	if bits&uOrigin3 != 0 {
		v, err := readCoord()
		if err != nil {
			return err
		}
		state.Origin[2] = v
	}
	if bits&uAngle3 != 0 {
		if err := r.Skip(angleSize(floatCoords)); err != nil {
			return err
		}
	}
	// U_SOLID has no payload in QW (see ezquake cl_ents.c:574-576).

	// FTE extension payloads we don't decode but must consume to keep the
	// byte cursor aligned. trans and colourmod are gated on the negotiated
	// extension (cl_ents.c:580, 586). Scale and fatness are consumed on the
	// flag bit alone: mvdsv never writes them (sv_ents.c's SV_WriteDelta
	// emits only trans/colourmod), so on real MVDs the bits are never set;
	// if a full-FTE stream does set one, consuming the byte keeps us aligned.
	if morebits&uFTETrans != 0 && fteExt&mvd.FTEPextTrans != 0 {
		if _, err := r.ReadByte(); err != nil {
			return err
		}
	}
	if morebits&uFTEScale != 0 {
		if _, err := r.ReadByte(); err != nil {
			return err
		}
	}
	if morebits&uFTEFatness != 0 {
		if _, err := r.ReadByte(); err != nil {
			return err
		}
	}
	if morebits&uFTEColourMod != 0 && fteExt&mvd.FTEPextColourMod != 0 {
		if _, err := r.ReadByte(); err != nil {
			return err
		}
		if _, err := r.ReadByte(); err != nil {
			return err
		}
		if _, err := r.ReadByte(); err != nil {
			return err
		}
	}
	// Other FTE flags (drawflags, abslight, etc.) are rarer still
	// and the project hasn't negotiated them; skipping the known
	// subset above covers every demo in the corpus. If a field's
	// bit is set but we don't recognise it, the next ReadUint16
	// will desynchronise and the parser will error — which is the
	// safe failure mode.

	return nil
}

// diffEntityTransitions compares the prior current-frame state against
// the new frame and emits ItemStateEvent on every visibility
// transition of a tracked item entity and MoverStateEvent on every
// origin / visibility change of a tracked inline brush-model entity.
//
// "Visible" means the entity is in the frame (Present==true) AND has a
// non-zero modelindex. mvdsv filters entities by modelindex!=0 at emit
// time (sv_ents.c:790), so for items "not in packet" is equivalent
// to "modelindex is 0" — i.e. the item was picked up.
//
// Also emits ItemSpawnEvent / MoverSpawnEvent for entities that hadn't
// been classified yet (e.g. baseline arrived before the model list)
// when we can now resolve the kind.
func (p *Parser) diffEntityTransitions(timeMs int32) error {
	// Delta packets: only the mentioned entities (entScratch, ascending)
	// can have transitioned. After a model-list change every entity gets
	// one rescan — that is when entities whose baselines predate the
	// list resolve their late classification, exactly as the previous
	// every-frame full walk did on its next pass.
	if p.classifyAllPending {
		p.classifyAllPending = false
		si := 0
		for ent := 0; ent < p.entLimit; ent++ {
			var s, o *EntityState
			if p.entCur[ent].Present {
				s = &p.entCur[ent]
			}
			if si < len(p.entScratch) && p.entScratch[si].ent == ent {
				if p.entScratch[si].old.Present {
					o = &p.entScratch[si].old
				}
				si++
			} else {
				o = s // unmentioned: prior state == current state
			}
			if err := p.diffEntity(ent, s, o, timeMs); err != nil {
				return err
			}
		}
		return nil
	}
	for i := range p.entScratch {
		d := &p.entScratch[i]
		var s, o *EntityState
		if p.entCur[d.ent].Present {
			s = &p.entCur[d.ent]
		}
		if d.old.Present {
			o = &d.old
		}
		if err := p.diffEntity(d.ent, s, o, timeMs); err != nil {
			return err
		}
	}
	return nil
}

// diffEntityTransitionsFull diffs every slot against the entPrev
// snapshot — the full-packet path, where entities absent from the
// packet vanished without a mention.
func (p *Parser) diffEntityTransitionsFull(timeMs int32) error {
	p.classifyAllPending = false
	for ent := 0; ent < p.entLimit; ent++ {
		var s, o *EntityState
		if p.entCur[ent].Present {
			s = &p.entCur[ent]
		}
		if p.entPrev[ent].Present {
			o = &p.entPrev[ent]
		}
		if err := p.diffEntity(ent, s, o, timeMs); err != nil {
			return err
		}
	}
	return nil
}

// diffEntity runs the three per-entity transition checks in the fixed
// item → mover → projectile order every diff walk shares.
func (p *Parser) diffEntity(ent int, s, o *EntityState, timeMs int32) error {
	if s == nil && o == nil {
		return nil
	}
	if err := p.diffItemEntity(ent, s, o, timeMs); err != nil {
		return err
	}
	if err := p.diffMoverEntity(ent, s, o, timeMs); err != nil {
		return err
	}
	return p.diffProjectileEntity(ent, s, o, timeMs)
}

// diffProjectileEntity emits ProjectileSpawnEvent the first frame a rocket
// or grenade entity is observed and ProjectileDespawnEvent when it leaves
// the wire (impact / timeout). Unlike items and movers — whose entity
// numbers are stable — projectile entnums are recycled, so the per-ent
// classification is cleared on despawn and re-derived on the next spawn.
func (p *Parser) diffProjectileEntity(ent int, s, o *EntityState, timeMs int32) error {
	curKind := ""
	if s != nil && s.Present && s.ModelIndex != 0 {
		if c := p.classOf(s.ModelIndex); c != nil {
			curKind = c.projKind
			if curKind == "" && p.decodeNails && c.isNail {
				// sv_nailhack servers send spikes as packet entities; track them
				// only when nail tracking is enabled (they are high volume).
				curKind = "nail"
			}
		}
	}
	tracked := p.spawnedProjectiles[ent]

	if tracked == "" {
		if curKind == "" {
			return nil
		}
		p.spawnedProjectiles[ent] = curKind
		return p.emit(&ProjectileSpawnEvent{EntNum: ent, Kind: curKind, Origin: s.Origin, TimeMs: timeMs})
	}

	if curKind == tracked {
		return nil // still in flight
	}

	// Gone, or the entnum was reused for a different model: despawn the old
	// projectile (last known origin), then spawn the new one if it is itself
	// a projectile.
	origin := [3]float32{}
	if o != nil {
		origin = o.Origin
	}
	p.spawnedProjectiles[ent] = ""
	if err := p.emit(&ProjectileDespawnEvent{EntNum: ent, Kind: tracked, Origin: origin, TimeMs: timeMs}); err != nil {
		return err
	}
	if curKind != "" {
		p.spawnedProjectiles[ent] = curKind
		return p.emit(&ProjectileSpawnEvent{EntNum: ent, Kind: curKind, Origin: s.Origin, TimeMs: timeMs})
	}
	return nil
}

// diffItemEntity emits the item spawn / state events for one entity of
// a frame diff. Resolve current kind preferring whatever state exists
// now, so baselines that landed before the model list still get an
// ItemSpawnEvent once we can name the model.
func (p *Parser) diffItemEntity(ent int, s, o *EntityState, timeMs int32) error {
	kind := p.spawnedItems[ent]
	if kind == "" {
		src := s
		if src == nil {
			src = o
		}
		if src != nil {
			kind = p.itemKindOf(src.ModelIndex, src.SkinNum)
			if kind != "" {
				p.spawnedItems[ent] = kind
				origin := src.Origin
				if p.baselineValid[ent] {
					origin = p.baselines[ent].Origin
				}
				if err := p.emit(&ItemSpawnEvent{
					EntNum: ent,
					Kind:   kind,
					Origin: origin,
					TimeMs: timeMs,
				}); err != nil {
					return err
				}
			}
		}
	}
	if kind == "" {
		return nil
	}

	oldVisible := o != nil && o.Present && o.ModelIndex != 0
	newVisible := s != nil && s.Present && s.ModelIndex != 0
	if oldVisible == newVisible {
		return nil
	}
	return p.emit(&ItemStateEvent{
		EntNum: ent,
		Kind:   kind,
		Taken:  !newVisible,
		TimeMs: timeMs,
	})
}

// diffMoverEntity emits the mover spawn / state events for one entity
// of a frame diff. State events fire on visibility flips AND on origin
// changes — a travelling lift re-sends its origin every frame it
// moves, and the analyzer's pose timeline is exactly those changes
// (hold-last between them, see MoverStateEvent).
func (p *Parser) diffMoverEntity(ent int, s, o *EntityState, timeMs int32) error {
	if p.spawnedMovers[ent] == 0 {
		// Late classification: a mover whose baseline arrived before
		// the model list gets its spawn here, same as items.
		src := s
		if src == nil {
			src = o
		}
		if src == nil {
			return nil
		}
		c := p.classOf(src.ModelIndex)
		if c == nil || c.mover == 0 {
			return nil
		}
		p.spawnedMovers[ent] = c.mover
		origin := src.Origin
		if p.baselineValid[ent] {
			origin = p.baselines[ent].Origin
		}
		if err := p.emit(&MoverSpawnEvent{
			EntNum:   ent,
			Model:    p.resolveModel(src.ModelIndex),
			SubModel: c.mover,
			Origin:   origin,
			TimeMs:   timeMs,
		}); err != nil {
			return err
		}
	}

	oldVisible := o != nil && o.Present && o.ModelIndex != 0
	newVisible := s != nil && s.Present && s.ModelIndex != 0
	moved := oldVisible && newVisible && s.Origin != o.Origin
	if oldVisible == newVisible && !moved {
		return nil
	}
	origin := [3]float32{}
	if newVisible {
		origin = s.Origin
	} else if o != nil {
		origin = o.Origin
	}
	return p.emit(&MoverStateEvent{
		EntNum:  ent,
		Origin:  origin,
		Visible: newVisible,
		TimeMs:  timeMs,
	})
}
