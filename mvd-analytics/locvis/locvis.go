// Package locvis is a visibility-aware drop-in replacement for the
// nearest-neighbour loc attribution provided by mvd-analytics/loc.
//
// V1 (the production Euclidean nearest-neighbour in loc.Finder) picks
// the geometrically closest loc-point, with no awareness of intervening
// walls. On certain trajectories — e.g. a player jumping over an open
// area whose far wall happens to be very close to a different loc-point
// — V1 produces brief "wall-bleed" loc visits to a place the player
// never visibly entered. These pollute downstream loc-trails, region-
// control, and loc-graph analytics.
//
// locvis wraps loc.Finder with a per-map BSP and overrides FindNearest
// with one of two algorithms (see the variants implemented in
// experiments/locattr/V2b-V6-HANDOFF.md):
//
//   - V6  — Euclidean primary + PVS-veto: walk locs in ascending
//     distance, skip any whose containing leaf is not in the player's
//     leaf's PVS row. First survivor wins. Falls back to V1's nearest
//     if every loc is vetoed.
//   - V6a — Same shape as V6, but the veto is a per-candidate raycast
//     through the BSP (line-of-sight) rather than a PVS bit-test.
//     Stricter; also more expensive (~O(BSP depth) per ray).
//
// When no BSP is available for the current map (file missing, parse
// error, or the WASM host did not install fetchBspSync), the Finder
// degenerates to the bare loc.Finder — bit-identical V1 behaviour. So
// locvis is always safe to swap in.
//
// The active algorithm is a compile-time constant (ActiveAlgorithm
// below). To compare V6 against V6a, change the constant and rebuild.
// Verified: V6 fixes 178 wall-bleed spans on demo 216406 (e1m2) with
// zero false positives; see experiments/locattr/V2b-V6-HANDOFF.md.
package locvis

import (
	"github.com/mvd-analyzer/mvd-analytics/bspvis"
	"github.com/mvd-analyzer/mvd-analytics/loc"
)

// Algorithm identifies which veto strategy FindNearest runs when a BSP
// is loaded for the current map.
type Algorithm int

const (
	// AlgoV1 disables the veto entirely. FindNearest delegates straight
	// to loc.Finder.FindNearest even when a BSP is loaded. Useful for
	// A/B-ing wall-bleed fixes against the baseline at compile time.
	AlgoV1 Algorithm = iota

	// AlgoV6 walks locs by ascending Euclidean distance and vetoes any
	// whose containing BSP leaf is not in the player's PVS row. First
	// survivor wins; falls back to V1 if every loc is vetoed.
	AlgoV6

	// AlgoV6a is the line-of-sight variant of V6: same control flow,
	// but the veto is a per-candidate raycast through CONTENTS_SOLID
	// leaves rather than a PVS bit-test. Stricter, slower.
	AlgoV6a
)

// ActiveAlgorithm selects the attribution strategy at compile time.
//
// Default: AlgoV6 (PVS-veto). Validated as zero-false-positive on the
// research corpus; see experiments/locattr/V2b-V6-HANDOFF.md.
//
// To compare against V6a or against bare V1, change this constant and
// rebuild.
const ActiveAlgorithm Algorithm = AlgoV6

// Finder wraps a loc.Finder with an optional BSP-backed visibility
// filter. When BSP is nil the Finder is functionally identical to its
// underlying loc.Finder.
type Finder struct {
	base *loc.Finder
	bsp  *bspvis.BSP // nil → no visibility filter, FindNearest = V1

	// locLeaves is index-aligned with base.Locations(): the BSP leaf
	// containing each loc-point. -1 means the loc-point landed in a
	// CONTENTS_SOLID leaf — a corpus artifact (loc placed inside brush
	// geometry); such locs can never win the veto.
	//
	// Both V6 and V6a use locLeaves[i] < 0 as their "drop this loc"
	// pre-filter. Only V6 actually reads the leaf index for the PVS
	// lookup; V6a only cares about the in-solid flag.
	locLeaves []int
}

// MapName returns the normalised map name the underlying loc.Finder was
// loaded for.
func (f *Finder) MapName() string {
	if f == nil || f.base == nil {
		return ""
	}
	return f.base.MapName()
}

// LocationCount returns the number of loc points loaded.
func (f *Finder) LocationCount() int {
	if f == nil || f.base == nil {
		return 0
	}
	return f.base.LocationCount()
}

// HasVisibility reports whether a BSP is available for the current map.
// When false, FindNearest delegates straight to V1 (loc.Finder).
func (f *Finder) HasVisibility() bool {
	return f != nil && f.bsp != nil
}

// Locations returns the full slice of loc points (pass-through to the
// underlying loc.Finder). Used by analyzers that need to enumerate loc
// names (e.g. region-control auto-detection).
func (f *Finder) Locations() []loc.Location {
	if f == nil || f.base == nil {
		return nil
	}
	return f.base.Locations()
}

// FindLocationsInRadius pass-through (no visibility filter — the
// existing callers want a geometric set, not a visibility set).
func (f *Finder) FindLocationsInRadius(x, y, z, radius float32) []loc.Location {
	if f == nil || f.base == nil {
		return nil
	}
	return f.base.FindLocationsInRadius(x, y, z, radius)
}

// FindNearest returns the name of the nearest loc-point to (x, y, z),
// subject to the active visibility veto. Same signature as
// loc.Finder.FindNearest; safe drop-in replacement.
func (f *Finder) FindNearest(x, y, z float32) string {
	if f == nil || f.base == nil {
		return ""
	}
	if f.bsp == nil || ActiveAlgorithm == AlgoV1 {
		return f.base.FindNearest(x, y, z)
	}
	switch ActiveAlgorithm {
	case AlgoV6:
		return f.attributeV6(x, y, z)
	case AlgoV6a:
		return f.attributeV6a(x, y, z)
	default:
		return f.base.FindNearest(x, y, z)
	}
}

// Base returns the underlying loc.Finder. Use when downstream code
// needs methods locvis.Finder doesn't expose (e.g. FindLocationsInRadius).
func (f *Finder) Base() *loc.Finder {
	if f == nil {
		return nil
	}
	return f.base
}

// newFinder is the shared constructor used by both loader_native.go and
// loader_wasm.go. bspBytes==nil means "no BSP available, run as V1".
// A BSP parse failure also degenerates silently to V1 rather than
// propagating an error — wall-bleed correction is best-effort, never a
// hard requirement.
func newFinder(base *loc.Finder, bspBytes []byte) *Finder {
	f := &Finder{base: base}
	if len(bspBytes) == 0 {
		return f
	}
	bsp, err := bspvis.LoadBytes(bspBytes)
	if err != nil {
		return f
	}
	locs := base.Locations()
	leaves := make([]int, len(locs))
	for i := range locs {
		leaf := bsp.PointInLeaf([3]float32{locs[i].X, locs[i].Y, locs[i].Z})
		if bsp.LeafContents(leaf) == bspvis.ContentsSolid {
			leaves[i] = -1
			continue
		}
		leaves[i] = leaf
	}
	f.bsp = bsp
	f.locLeaves = leaves
	return f
}
