package analyzer

// Shared sub-structs used by the timeline analyzer to group related fields
// instead of leaving them as a flat sprawl of `hasRL` / `hasLG` / `hasQuad` /
// `health` / `shells` / `x` / ... fields on every state struct.
//
// Grouping pulls a few small wins out of the analyzer:
//
//   - per-bucket and per-window aggregator structs can copy a whole substruct
//     in one assignment (`agg.ammo = pRaw.ammo`) instead of stamping each
//     field by hand;
//   - the boolean weapon/powerup flags get an OR-fold helper so the
//     `aggregateWindow` loop reads as one line per group instead of five;
//   - powerupKinds (used by detectPowerupEvents) can iterate the powerup
//     loadout as data instead of going through closures over each named
//     field.

// weaponLoadout records which weapons a player is carrying at a sample point.
// SSG/SNG are tracked because the high-res map view exposes them; the
// graph-bucket aggregator currently only cares about RL and LG.
type weaponLoadout struct {
	rl, lg, ssg, sng bool
}

// orInPlace folds another loadout into this one with logical-OR semantics.
// Used by aggregateWindow to compute "did this player ever hold weapon X
// during this graph-bucket window".
func (w *weaponLoadout) orInPlace(other weaponLoadout) {
	w.rl = w.rl || other.rl
	w.lg = w.lg || other.lg
	w.ssg = w.ssg || other.ssg
	w.sng = w.sng || other.sng
}

// powerupLoadout records the three QuakeWorld powerups.
type powerupLoadout struct {
	quad, pent, ring bool
}

// orInPlace folds another powerup loadout into this one with OR semantics.
func (p *powerupLoadout) orInPlace(other powerupLoadout) {
	p.quad = p.quad || other.quad
	p.pent = p.pent || other.pent
	p.ring = p.ring || other.ring
}

// vitals holds the health/armor pair at a sample point. armorType is part
// of the same logical group because picking up RA replaces YA replaces GA.
type vitals struct {
	health    int
	armor     int
	armorType string // "ga" | "ya" | "ra" | ""
}

// ammoCounts holds the four ammo pools.
type ammoCounts struct {
	shells  int
	nails   int
	rockets int
	cells   int
}

// playerPosition is the world-space player origin sampled from svc_playerinfo.
type playerPosition struct {
	x, y, z float32
}
