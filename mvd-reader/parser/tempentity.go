package parser

import (
	"errors"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

// errUnknownTE is returned by parseTempEntity when the TE type byte is not
// in its layout table — the payload length can't be guessed, so the rest of
// the message is abandoned. Distinct from a truncated read inside a known
// type so the caller can warn "unknown_te" for the former and "parse_error"
// (naming the TE type) for the latter; mirrors errUnknownSvc.
var errUnknownTE = errors.New("unknown temp entity type")

// Temp-entity types. The lightning types are beams (short entity + start +
// end coords); everything else is a point effect (3 coords, with a leading
// count byte on gunshot/blood). Values match ezquake cl_tent.c /
// qwprot protocol.h.
const (
	TeSpike          = 0  // nail impact on a wall
	TeSuperSpike     = 1  // super nail impact on a wall
	TeGunshot        = 2  // hitscan wall puff; count byte = pellets on this point
	TeExplosion      = 3  // rocket/grenade detonation point
	TeTarExplosion   = 4  // tarbaby explosion
	teLightning1     = 5  // bolt (shambler/enforcer — not player LG)
	teLightning2     = 6  // player Lightning Gun bolt (ktx W_FireLightning)
	TeWizSpike       = 7  // wizard spike impact
	TeKnightSpike    = 8  // hellknight spike impact
	teLightning3     = 9  // big bolt
	TeLavaSplash     = 10 // chthon lava splash
	TeTeleport       = 11 // teleporter sparkle
	TeBlood          = 12 // QW: hitscan damage on a player; count byte semantics below
	TeLightningBlood = 13 // QW: LG damage on a player (no count)
)

// BeamEvent is emitted for a TE_LIGHTNING1/2/3 temp entity — a lightning
// beam drawn from Start to End by entity Ent. For the player Lightning Gun
// (Type == TE_LIGHTNING2, ktx/src/weapons.c W_FireLightning) the beam is
// emitted once per fire tick and carries the firing entity directly, so it
// is the authoritative per-shot LG signal: Ent-1 is the shooter, Start is
// the muzzle, End is the hitscan trace endpoint (the impact / aim point).
// One beam == one LG attack == one cell (discharge does not emit a beam),
// so beam counts match KTX's acc.attacks.
type BeamEvent struct {
	Ent    int // firing entity (player slot = Ent-1 when a client; negative on ezquake's rail-trail extension, never from KTX)
	Type   int // raw TE type: 5/6/9
	Start  [3]float32
	End    [3]float32
	TimeMs int32
}

func (e *BeamEvent) EventType() EventType { return EventBeam }
func (e *BeamEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *BeamEvent) EventTimeMs() int32   { return e.TimeMs }

// PointEffectEvent is emitted for every non-beam temp entity — a visual
// effect at a single point. Two QW-specific types are direct damage
// telemetry (the server multicasts them from T_Damage-adjacent code, so
// they exist on every demo back to original qwprogs):
//
//   - TE_BLOOD: hitscan damage striking a player. KTX SpawnBlood
//     (ktx/src/weapons.c) aggregates one message per shotgun volley with
//     Count = pellets that hit (modern KTX 1.4x, damage = 4·Count); older
//     generations (KTX 1.38, vanilla qwprogs) write one message per pellet
//     with Count = 1 (damage = 4·messages). Axe and nail hits write
//     Count = the damage itself. The packaging convention varies per
//     server generation and per mod, so consumers must calibrate per demo
//     (validate 4·Count against observed h/a deltas) rather than assume.
//   - TE_LIGHTNINGBLOOD: an LG cell's damage striking a player at the
//     beam's trace endpoint (one per hit, no count).
//
// TE_GUNSHOT is the complementary miss signal (wall puff, Count =
// pellets); TE_EXPLOSION is the exact rocket/grenade detonation point.
// Count is 0 for types that carry no count byte.
type PointEffectEvent struct {
	Type   int // raw TE type (see Te* constants)
	Count  int // TE_GUNSHOT / TE_BLOOD count byte; 0 otherwise
	Origin [3]float32
	TimeMs int32
}

func (e *PointEffectEvent) EventType() EventType { return EventPointEffect }
func (e *PointEffectEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *PointEffectEvent) EventTimeMs() int32   { return e.TimeMs }

// parseTempEntity decodes a svc_temp_entity payload. Lightning beams are
// surfaced as BeamEvent; every other known type is surfaced as
// PointEffectEvent. Returns the TE type so the caller can name it in
// a diagnostic. Wire layout per type is handled in the switch below (ref:
// ezquake cl_tent.c::CL_ParseTEnt); an unknown type returns errUnknownTE
// since its length can't be guessed without drifting the parser.
func (p *Parser) parseTempEntity(r *mvd.BufferReader, timeMs int32, floatCoords bool) (byte, error) {
	teType, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	readCoord := func() (float32, error) {
		if floatCoords {
			return r.ReadFloatCoord()
		}
		return r.ReadCoord()
	}

	switch teType {
	case teLightning1, teLightning2, teLightning3:
		entRaw, err := r.ReadUint16()
		if err != nil {
			return teType, err
		}
		var start, end [3]float32
		for i := 0; i < 3; i++ {
			if start[i], err = readCoord(); err != nil {
				return teType, err
			}
		}
		for i := 0; i < 3; i++ {
			if end[i], err = readCoord(); err != nil {
				return teType, err
			}
		}
		// The wire value is a signed short (ezquake cl_tent.c:158): ezquake
		// gives negative values protocol meaning (TE_LIGHTNING1 with ent in
		// -512..-1 is its rail-trail extension). KTX always writes a real
		// edict, but decode faithfully so exotic streams stay recoverable.
		return teType, p.emit(&BeamEvent{
			Ent:    int(int16(entRaw)),
			Type:   int(teType),
			Start:  start,
			End:    end,
			TimeMs: timeMs,
		})
	case TeSpike, TeSuperSpike, TeExplosion, TeTarExplosion, TeWizSpike,
		TeKnightSpike, TeLavaSplash, TeTeleport, TeLightningBlood:
		var origin [3]float32
		for i := 0; i < 3; i++ {
			if origin[i], err = readCoord(); err != nil {
				return teType, err
			}
		}
		return teType, p.emit(&PointEffectEvent{
			Type:   int(teType),
			Origin: origin,
			TimeMs: timeMs,
		})
	case TeGunshot, TeBlood:
		count, err := r.ReadByte()
		if err != nil {
			return teType, err
		}
		var origin [3]float32
		for i := 0; i < 3; i++ {
			if origin[i], err = readCoord(); err != nil {
				return teType, err
			}
		}
		return teType, p.emit(&PointEffectEvent{
			Type:   int(teType),
			Count:  int(count),
			Origin: origin,
			TimeMs: timeMs,
		})
	default:
		return teType, errUnknownTE
	}
}
