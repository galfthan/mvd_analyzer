package view

import (
	"github.com/mvd-analyzer/qwanalytics/result"
)

// LocTrailsOptions narrows a LocTrails query. MinDwellMs drops
// transitions shorter than the threshold (the per-player dwell
// summed into the surrounding stable loc) — useful for filtering
// nearest-loc flicker without re-running the analyzer's blip filter.
type LocTrailsOptions struct {
	Players    []string
	MinDwellMs int
	StartTime  float64
	EndTime    float64
}

// LocTrailsView is the response shape: per-player loc-name sequence
// with dwell durations.
type LocTrailsView struct {
	Players []PlayerTrail `json:"players"`
}

// PlayerTrail is one player's loc journey within the requested
// window.
type PlayerTrail struct {
	Name     string       `json:"name"`
	Sequence []TrailEntry `json:"sequence"`
}

// TrailEntry is one continuous residence in a single loc.
type TrailEntry struct {
	Start float64 `json:"s"`
	End   float64 `json:"e"`
	Loc   string  `json:"loc"`
}

// LocTrails derives per-player loc residences from
// PlayerStream.Loc + TimelineAnalysis.LocTable. Walks each player's
// loc-change list, pairing consecutive entries into [Start, End)
// intervals, then optionally folds short dwells into their neighbour.
func LocTrails(r *result.Result, opts LocTrailsOptions) (*LocTrailsView, error) {
	if r == nil || r.Streams == nil || r.TimelineAnalysis == nil {
		return &LocTrailsView{}, nil
	}
	locTable := r.TimelineAnalysis.LocTable
	if len(locTable) == 0 {
		return &LocTrailsView{}, nil
	}
	end := opts.EndTime
	if end == 0 {
		end = r.Streams.Global.MatchEnd
	}
	pf := newPlayerFilter(opts.Players)
	out := &LocTrailsView{}
	minDwell := float64(opts.MinDwellMs) / 1000.0

	for _, p := range r.Streams.Players {
		if !pf.accepts(p.Name) {
			continue
		}
		raw := buildTrailRaw(p.Loc, opts.StartTime, end, locTable)
		seq := raw
		if minDwell > 0 {
			seq = mergeShortDwells(seq, minDwell)
		}
		if len(seq) == 0 {
			continue
		}
		out.Players = append(out.Players, PlayerTrail{Name: p.Name, Sequence: seq})
	}
	return out, nil
}

// buildTrailRaw walks the loc-change stream and emits a [Start, End)
// entry per residence. The final entry is closed at windowEnd (or
// match end). Entries entirely outside the window are dropped.
func buildTrailRaw(stream []result.ChangeI16, windowStart, windowEnd float64, locTable []string) []TrailEntry {
	if len(stream) == 0 {
		return nil
	}
	out := make([]TrailEntry, 0, len(stream))
	for i, c := range stream {
		segStart := c.T
		var segEnd float64
		if i+1 < len(stream) {
			segEnd = stream[i+1].T
		} else {
			segEnd = windowEnd
		}
		if segEnd <= windowStart {
			continue
		}
		if windowEnd > 0 && segStart >= windowEnd {
			break
		}
		if segStart < windowStart {
			segStart = windowStart
		}
		if windowEnd > 0 && segEnd > windowEnd {
			segEnd = windowEnd
		}
		idx := int(c.V)
		locName := ""
		if idx >= 0 && idx < len(locTable) {
			locName = locTable[idx]
		}
		if locName == "" {
			continue
		}
		out = append(out, TrailEntry{Start: segStart, End: segEnd, Loc: locName})
	}
	return out
}

// mergeShortDwells folds entries shorter than minDwell into their
// preceding entry. Keeps the earlier loc name (its dwell extends to
// cover the dropped span), which matches the analyzer's blip-filter
// behaviour.
func mergeShortDwells(seq []TrailEntry, minDwell float64) []TrailEntry {
	if len(seq) <= 1 {
		return seq
	}
	out := make([]TrailEntry, 0, len(seq))
	out = append(out, seq[0])
	for i := 1; i < len(seq); i++ {
		dwell := seq[i].End - seq[i].Start
		if dwell < minDwell {
			out[len(out)-1].End = seq[i].End
			continue
		}
		// Coalesce identical-loc adjacent entries (rare, but the
		// merge above can produce them).
		last := &out[len(out)-1]
		if last.Loc == seq[i].Loc {
			last.End = seq[i].End
			continue
		}
		out = append(out, seq[i])
	}
	return out
}
