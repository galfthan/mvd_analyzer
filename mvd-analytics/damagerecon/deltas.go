package damagerecon

import (
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// delta is one reconstructed per-instant damage observation for a victim:
// the merged health+armor drop at a single stream timestamp.
//
// Bounded is the KTX-scoreboard value by construction: armor drop + health
// share capped at remaining health (a corpse hit contributes 0). Raw is the
// uncapped drop — on a killing hit the health share extends into the
// negative death value the end-of-frame broadcast carries, exact down to
// KTX's -99 corpse clamp (deeper overkill is unrecoverable from the wire;
// the same limitation the KTX-side reconstruction documents).
type delta struct {
	t       int32
	raw     int
	bounded int
	died    bool
	// masked marks a death+respawn that landed on a single stream instant:
	// the killing drop itself was never broadcast (the respawn's spawn
	// state overwrote it — a full-health telefrag victim leaves no health
	// change row at all), so the value is reconstructed from the
	// pre-instant h/a (armor + remaining health — exact for a telefrag, an
	// upper bound on the armor share otherwise). Attribution anchors these
	// on the frag log.
	masked  bool
	hBefore int
	aBefore int
}

// armorFracAt returns the victim's armor absorption fraction at t (GA 0.3 /
// YA 0.6 / RA 0.8) from the armor-type change stream — the pre-instant
// value, since type changes broadcast at end of frame.
func armorFracAt(p *result.PlayerStream, t int32) float64 {
	at := ""
	for i := range p.ArmorType {
		if p.ArmorType[i].T > t {
			break
		}
		at = p.ArmorType[i].V
	}
	switch at {
	case "ga":
		return 0.3
	case "ya":
		return 0.6
	case "ra":
		return 0.8
	}
	return 0
}

// victimDeltas extracts the per-instant damage observations for one player
// from the merged health+armor change streams, excluding mega-rot ticks,
// pickups (rises) and spawn resets.
//
// The scan walks the union of the change-stream instants and the death
// instants: a same-instant death+respawn can leave no change row at all
// (h 100→100 dedups away), yet it is a real kill the spawn state masked.
//
// killAnchors are the frag-log instants at which this player was killed by
// another player. They gate the deepest masked case — a frag-anchored
// death while the tracked state says "corpse" is a respawn+instant-kill
// cycle whose respawn broadcast AND death value were both masked (the dm3
// spawn-deflect telefrag: h sits at -99 across the whole cycle). The
// KTX-side reconstruction applies the same spawn-state rule
// (analyzer/damage.go, the tele respawn inference); charged at the 100/0
// spawn capacity.
func victimDeltas(p *result.PlayerStream, killAnchors map[int32]bool) []delta {
	spawns := make(map[int32]bool, len(p.Spawns))
	for _, t := range p.Spawns {
		spawns[t] = true
	}
	deaths := make(map[int32]bool, len(p.Deaths))
	for _, t := range p.Deaths {
		deaths[t] = true
	}

	// Merge the change streams plus the masked death instants on one
	// timestamp axis.
	type instant struct {
		t    int32
		h, a int16
		hasH bool
		hasA bool
	}
	hs, as := p.Health, p.Armor
	merged := make([]instant, 0, len(hs)+len(as))
	i, j := 0, 0
	for i < len(hs) || j < len(as) {
		switch {
		case j >= len(as) || (i < len(hs) && hs[i].T < as[j].T):
			merged = append(merged, instant{t: hs[i].T, h: hs[i].V, hasH: true})
			i++
		case i >= len(hs) || as[j].T < hs[i].T:
			merged = append(merged, instant{t: as[j].T, a: as[j].V, hasA: true})
			j++
		default: // same instant
			merged = append(merged, instant{t: hs[i].T, h: hs[i].V, hasH: true, a: as[j].V, hasA: true})
			i++
			j++
		}
	}
	needSort := false
	for t := range deaths {
		k := sort.Search(len(merged), func(i int) bool { return merged[i].t >= t })
		if k < len(merged) && merged[k].t == t {
			continue // a change row exists at this instant already
		}
		merged = append(merged, instant{t: t})
		needSort = true
	}
	if needSort {
		sort.Slice(merged, func(a, b int) bool { return merged[a].t < merged[b].t })
	}

	var out []delta
	// Baseline starts at the spawn state; the synthesized match-start spawn
	// guarantees every present player begins a life at t=0, so a masked
	// kill on the very first instant (match-start telefrag) still has a
	// truthful pre-instant capacity.
	ph, pa := 100, 0
	for _, m := range merged {
		nh, na := ph, pa
		if m.hasH {
			nh = int(m.h)
		}
		if m.hasA {
			na = int(m.a)
		}
		if deaths[m.t] && ph <= 0 && killAnchors[m.t] {
			// Frag-anchored death while the tracked state is a corpse: a
			// respawn+instant-kill cycle both of whose broadcasts were
			// masked. Charge the spawn capacity (KTX saw 100/0).
			out = append(out, delta{
				t: m.t, raw: 100, bounded: 100,
				died: true, masked: true, hBefore: 100, aBefore: 0,
			})
			ph, pa = nh, na
			continue
		}
		if spawns[m.t] {
			// Spawn reset — but a death sharing the instant means the
			// killing drop was masked by the respawn broadcast: charge the
			// victim's full pre-instant capacity as the kill.
			if deaths[m.t] && ph > 0 {
				out = append(out, delta{
					t: m.t, raw: ph + pa, bounded: ph + pa,
					died: true, masked: true, hBefore: ph, aBefore: pa,
				})
			}
			// The post-spawn state: the change rows when present (they are
			// the spawn broadcast), else the canonical 100/0.
			if !m.hasH {
				nh = 100
			}
			if !m.hasA {
				na = 0
			}
			ph, pa = nh, na
			continue
		}
		dh, da := 0, 0
		if nh < ph {
			dh = ph - nh
		}
		if na < pa {
			da = pa - na
		}
		if dh == 1 && ph > 100 && da == 0 {
			// Mega-health rot tick, not damage.
			ph, pa = nh, na
			continue
		}
		if dh > 0 || da > 0 {
			died := nh <= 0 && ph > 0
			share := dh
			switch {
			case ph <= 0:
				share = 0 // corpse hit: no bounded value (KTX dmg_dealt sees a dead target)
			case nh <= 0:
				share = ph // killing hit: bounded caps at remaining health
			}
			armorShare := da
			if ph <= 0 {
				armorShare = 0
			}
			b := share + armorShare
			raw := 0
			if ph > 0 {
				raw = dh + da
				if dh == 0 && da > 0 {
					// Armor-only drop on a live player = a nullified health
					// share (pent, or the teamplay tp1/tp3 rules). The wire's
					// RAW value still carries save+virtual_take
					// (analyzer/damage.go's nullification notes), so recover
					// it from the armor absorption fraction at hit time.
					if frac := armorFracAt(p, m.t); frac > 0 {
						raw = int(float64(da)/frac + 0.5)
					}
				}
			} else if dh > 0 {
				// Corpse hit (gibbing an already-dead body): the wire still
				// multicasts it and KTX's RAW accumulation counts it, so the
				// raw family keeps the observed corpse-health drop (partial
				// once the -99 clamp is reached; nothing at the floor).
				raw = dh
			}
			if b > 0 || raw > 0 {
				out = append(out, delta{
					t: m.t, raw: raw, bounded: b, died: died,
					hBefore: ph, aBefore: pa,
				})
			}
		}
		ph, pa = nh, na
	}
	return out
}
