package damagerecon

import (
	"github.com/mvd-analyzer/mvd-analytics/bspvis"
	"github.com/mvd-analyzer/mvd-analytics/mapbsp"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// bspGate wraps the map's visibility BSP for candidate feasibility gating:
// hitscan and nail fire cannot cross solid geometry, and splash damage
// follows the engine's CanDamage rule — it reaches only what the explosion
// point can trace to (the impact face blocks the covered half-space).
// A nil gate (BSP not provisioned / unparseable) disables all gating; the
// reconstruction then runs geometry-only, exactly as the calibrated
// prototype did.
type bspGate struct {
	b *bspvis.BSP
}

// loadBSPGate resolves the demo's map and loads its BSP. Best-effort by
// design: mapbsp.LoadBytes returns nil when the BSP dir is not provisioned,
// and a corrupt file just disables the tier (same "no latch" discipline as
// analyzer.ComputeLOS — a later-provisioned BSP heals the next run).
func loadBSPGate(res *result.Result) *bspGate {
	data := mapbsp.LoadBytes(res.EffectiveMap())
	if data == nil {
		return nil
	}
	b, err := bspvis.LoadBytes(data)
	if err != nil {
		return nil
	}
	return &bspGate{b: b}
}

// rayClear reports whether the segment a→b crosses no solid geometry.
// A nil gate is always clear.
func (g *bspGate) rayClear(a, b vec3) bool {
	if g == nil {
		return true
	}
	return !g.b.RayHitsSolid(
		[3]float32{float32(a.x), float32(a.y), float32(a.z)},
		[3]float32{float32(b.x), float32(b.y), float32(b.z)},
	)
}

// splashNudge pulls the explosion endpoint toward the victim before
// tracing: a projectile's last observed position sits at (or fractionally
// inside) the impact surface, and a start/end exactly in solid would read
// as blocked even when the explosion plainly reaches the victim.
const splashNudge = 8.0

// bodyOffsets are the target points a reachability test tries — the
// engine's CanDamage traces to the entity center plus four side points
// (ktx/src/combat.c CanDamage; ±15 on each horizontal axis), extended here
// with head/feet points because our positions are interpolated between
// ~13-40ms samples and a single mid-box ray clips corners the real
// trajectory cleared.
var bodyOffsets = [6]vec3{
	{0, 0, targetOffsetZ},
	{15, 0, targetOffsetZ},
	{-15, 0, targetOffsetZ},
	{0, 15, targetOffsetZ},
	{0, -15, targetOffsetZ},
	{0, 0, eyeOffsetZ},
}

// reachesBody reports whether ANY body point of a victim standing at vpos
// traces clear from `from`. The permissive any-clear rule is deliberate:
// these gates exist to kill geometrically impossible candidates (a shooter
// on the other side of a wall), not to re-litigate near-corner hits that
// interpolation error would misjudge.
func (g *bspGate) reachesBody(from, vpos vec3) bool {
	if g == nil {
		return true
	}
	for _, o := range bodyOffsets {
		p := vec3{vpos.x + o.x, vpos.y + o.y, vpos.z + o.z}
		// Trace FROM the body point (a standing player's hull points are
		// never inside solid) toward the source.
		if g.rayClear(p, from) {
			return true
		}
	}
	return false
}

// splashReaches is the CanDamage-style gate: some body point of the victim
// must trace clear to the (slightly nudged) explosion point. The engine's
// rule that the impact face blocks the covered 180° falls out naturally: a
// victim fully behind the face has every ray cut by that face.
func (g *bspGate) splashReaches(ep, vpos vec3) bool {
	if g == nil {
		return true
	}
	mid := vec3{vpos.x, vpos.y, vpos.z + targetOffsetZ}
	d := mid.sub(ep)
	l := d.length()
	if l <= splashNudge {
		return true
	}
	f := splashNudge / l
	nudged := vec3{ep.x + d.x*f, ep.y + d.y*f, ep.z + d.z*f}
	return g.reachesBody(nudged, vpos)
}
