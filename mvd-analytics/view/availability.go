package view

import (
	"github.com/mvd-analyzer/mvd-analytics/aimcore"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// This file adds the object-shaped "section accessor" half of the R3
// availability rule (see sections.go for ErrUnavailable and the
// list-vs-object convention). Each accessor returns the section, or
// ErrUnavailable when the demo lacks the enabling signal, so every
// consumer funnels through one availability predicate instead of
// hand-rolling a nil-field check — HTTP handlers map ErrUnavailable to a
// 422 via writeUnavailable, and in-process callers (WASM/CLI) get the
// same gate for free (mvd-api F11).

// Metadata returns the demo's server-cvar + KTX match-settings block, or
// ErrUnavailable when the demo carried no fullserverinfo / countdown
// centerprint.
func Metadata(r *result.Result) (*result.MetadataResult, error) {
	if r.Metadata == nil {
		return nil, ErrUnavailable
	}
	return r.Metadata, nil
}

// DemoInfo returns the KTX demoinfo blob, or ErrUnavailable on a non-KTX
// or pre-match-abort demo that carried none.
func DemoInfo(r *result.Result) (*result.DemoInfoResult, error) {
	if r.DemoInfo == nil {
		return nil, ErrUnavailable
	}
	return r.DemoInfo, nil
}

// LocGraph returns the per-map loc adjacency graph, or ErrUnavailable
// when no position track was emitted.
func LocGraph(r *result.Result) (*result.LocGraphResult, error) {
	if r.LocGraph == nil {
		return nil, ErrUnavailable
	}
	return r.LocGraph, nil
}

// Shots returns the per-fire weapon-fire stream, or ErrUnavailable when
// no weapon fires were decoded.
func Shots(r *result.Result) (*result.ShotsResult, error) {
	if r.Shots == nil {
		return nil, ErrUnavailable
	}
	return r.Shots, nil
}

// AimOptions filters the per-player aim analysis. Empty fields mean "no
// filter". From/To are match-relative int32 ms (0 disables that bound),
// matching getFrags/getDamage.
type AimOptions struct {
	Players []string // scope to these shooters (case-sensitive)
	From    int32    // window start, int32 ms (0 = no bound)
	To      int32    // window end, int32 ms (0 = no bound)
	Summary bool     // drop the big Crosshair + LGRamp sample blocks per player
}

// Aim returns the per-player aim analysis, optionally narrowed, or
// ErrUnavailable when the demo has no shots + position/view streams to
// derive it from.
//
// Aim is derived from shots, so filtering mirrors the frags/damage discipline:
//
//   - NO TIME WINDOW (from==0 AND to==0): use the STORED res.Aim (computed once
//     by the analyzer) — no recompute. A players filter selects the named
//     players' stored PlayerAim (their match-wide aim, exactly as frags
//     players-only selects a player's match-wide totals). Summary alone still
//     takes this path; it only drops the sample blocks.
//
//   - TIME WINDOW SET (from!=0 OR to!=0): RECOMPUTE aim over the shots in
//     [from,to] (and the named players) via aimcore.Compute, so every output
//     (weapons accuracy, RL/GL direct/splash, LG ramp, crosshair samples)
//     scopes to the window consistently.
//
// Summary is orthogonal: it drops Crosshair + LGRamp from whichever result was
// produced (the overflow fix), keeping Player/Team/Mode/Weapons.
//
// ALIASING: the unfiltered / players-only / summary-only paths may share the
// stored Result's PlayerAim values by reference (a read-only view) or return
// freshly-allocated summary copies. Callers MUST NOT mutate the returned value
// (all current callers marshal-and-discard). The windowed path returns freshly
// computed data.
func Aim(r *result.Result, opts AimOptions) (*result.AimResult, error) {
	if r.Aim == nil {
		return nil, ErrUnavailable
	}
	players := toSet(opts.Players)

	var base *result.AimResult
	if opts.From == 0 && opts.To == 0 {
		// No window: reuse the stored aim, optionally selecting named players.
		if len(players) == 0 {
			base = r.Aim
		} else {
			base = &result.AimResult{}
			for i := range r.Aim.Players {
				if players[r.Aim.Players[i].Player] {
					base.Players = append(base.Players, r.Aim.Players[i])
				}
			}
		}
	} else {
		// Window set: recompute over the windowed shot slice + named players.
		q := aimcore.Query{Players: players}
		if opts.From != 0 {
			from := opts.From
			q.FromMs = &from
		}
		if opts.To != 0 {
			to := opts.To
			q.ToMs = &to
		}
		base = aimcore.Compute(r, q)
		if base == nil {
			base = &result.AimResult{}
		}
	}

	// Included-but-empty players serialize as [], never null: a scoping filter
	// (players / window) that matched no shooter must return players:[], the
	// same shape the summary path (make below) and the filtered-empty-log
	// convention (view commit d50d9ac) already produce. Only the unfiltered
	// pass-through keeps the stored value untouched (aliasing r.Aim).
	if base != r.Aim && base.Players == nil {
		base.Players = []result.PlayerAim{}
	}

	if !opts.Summary {
		return base, nil
	}

	// Summary: drop the big per-fire sample blocks, keep the aggregates. Copy
	// each PlayerAim so the shared stored values are never mutated.
	out := &result.AimResult{Players: make([]result.PlayerAim, len(base.Players)), HitsMeasured: base.HitsMeasured}
	for i := range base.Players {
		pa := base.Players[i]
		pa.Crosshair = nil
		pa.LGRamp = nil
		out.Players[i] = pa
	}
	return out, nil
}

// Airgibs returns the Key Moments airgib list. Availability tracks the
// timeline-analysis pass, not the map's clip hull: ErrUnavailable only
// when there is no TimelineAnalysis at all. A present-but-BSP-less demo
// returns an empty (non-nil) list — an airgib-less map is a 200 [], not
// a 422.
func Airgibs(r *result.Result) ([]result.AirgibEvent, error) {
	if r.TimelineAnalysis == nil {
		return nil, ErrUnavailable
	}
	if r.TimelineAnalysis.Airgibs == nil {
		return []result.AirgibEvent{}, nil
	}
	return r.TimelineAnalysis.Airgibs, nil
}

// RegionControlAvailable reports ErrUnavailable when the demo has no
// region-control layout (no TimelineAnalysis, or a nil RegionControl on
// it). This is the gate the /region-control endpoint checks before
// computing a windowed view via RegionControl(opts); the compute itself
// is always safe once the layout is present.
func RegionControlAvailable(r *result.Result) error {
	if r.TimelineAnalysis == nil || r.TimelineAnalysis.RegionControl == nil {
		return ErrUnavailable
	}
	return nil
}

// TopWindowsAvailable reports whether the demo can answer a top-windows query
// for the given metric. Availability is PER-METRIC because the three source
// streams are independently present: a non-KTX demo has a frag log but no
// damage stream, so metric=frags works and metric=damageGiven does not.
//
// Note this is a source check only. Absent loc data does NOT make the endpoint
// unavailable — the segmentation needs the event log alone, so a demo with no
// position track simply omits the per-window locs.
func TopWindowsAvailable(r *result.Result, metric string) error {
	if r == nil {
		return ErrUnavailable
	}
	m, ok := canonicalMetric(metric)
	if !ok {
		return ErrUnavailable
	}
	switch m {
	case MetricShots, MetricHits:
		if r.Shots == nil {
			return ErrUnavailable
		}
	case MetricDamageGiven, MetricDamageTaken, MetricNetDamage:
		if r.Damage == nil {
			return ErrUnavailable
		}
	default:
		if r.Frags == nil {
			return ErrUnavailable
		}
	}
	return nil
}

// LivesAvailable reports whether the demo carries the per-player streams lives
// are segmented from, AND whether liveness was measurable on any of them.
//
// The second half is not pedantry. PlayerStream.Alive has three states — nil
// "the match window was unknown, so liveness was not measurable", [] "measured,
// and never alive", [...] "the lives" — and Lives emits no rows for either of
// the first two. Serving `{"lives": []}` on a demo where liveness was never
// measurable says "nobody ever lived", which is a different and false claim; a
// 422 is the honest answer, and it is the same shape every other unavailable
// capability uses. (measured.liveness carries the same fact on the responses
// that do get served.)
func LivesAvailable(r *result.Result) error {
	if r == nil || r.Streams == nil || len(r.Streams.Players) == 0 {
		return ErrUnavailable
	}
	if !livenessMeasured(r) {
		return ErrUnavailable
	}
	return nil
}
