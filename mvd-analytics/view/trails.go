package view

import (
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// LocTrailsOptions narrows a LocTrails query. MinDwellMs drops
// transitions shorter than the threshold (the per-player dwell
// summed into the surrounding stable loc) — useful for filtering
// nearest-loc flicker without re-running the analyzer's blip filter.
type LocTrailsOptions struct {
	Players    []string
	MinDwellMs int
	StartTime  int32 // window start, int32 ms (0 = match start)
	EndTime    int32 // window end, int32 ms (0 = match end)
	// LocIndex selects the residence representation: false (default)
	// names each residence (TrailEntry.Loc); true emits the raw
	// LocTable index (TrailEntry.Li). Decode the index via /loc-table.
	LocIndex bool
}

// LocTrailsView is the response shape: per-player loc-name sequence
// with dwell durations.
type LocTrailsView struct {
	// TimeUnit echoes this endpoint's native unit (constant "ms", schema v57);
	// set by the mvd-api handler. Omitted on the WASM/qw-analyze paths.
	TimeUnit TimeUnit      `json:"timeUnit,omitempty"`
	Players  []PlayerTrail `json:"players"`
}

// PlayerTrail is one player's loc journey within the requested
// window.
type PlayerTrail struct {
	Name     string       `json:"name"`
	Sequence []TrailEntry `json:"sequence"`
}

// TrailEntry is one continuous residence in a single loc. Loc (name)
// or Li (raw LocTable index) is set per LocTrailsOptions.LocIndex; the
// unexported li always holds the index so grouping/merging stay
// name-agnostic and the index render is a final relabel.
type TrailEntry struct {
	Start int32  `json:"start"`
	End   int32  `json:"end"`
	Loc   string `json:"loc,omitempty"`
	Li    *int16 `json:"li,omitempty"`

	li int16
}

// LocTrails derives per-player loc residences from
// PlayerStream.Loc + TimelineAnalysis.LocTable. Walks each player's
// loc-change list, pairing consecutive entries into [Start, End)
// intervals, then optionally folds short dwells into their neighbour.
func LocTrails(r *result.Result, opts LocTrailsOptions) (*LocTrailsView, error) {
	if r == nil || r.Streams == nil || r.TimelineAnalysis == nil {
		return &LocTrailsView{Players: []PlayerTrail{}}, nil
	}
	locTable := r.TimelineAnalysis.LocTable
	if len(locTable) == 0 {
		return &LocTrailsView{Players: []PlayerTrail{}}, nil
	}
	end := opts.EndTime
	if end == 0 {
		end = r.Streams.Global.MatchEnd
	}
	pf := newPlayerFilter(opts.Players)
	out := &LocTrailsView{Players: []PlayerTrail{}}
	minDwell := int32(opts.MinDwellMs)

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
		if opts.LocIndex {
			for j := range seq {
				li := seq[j].li
				seq[j].Li = &li
				seq[j].Loc = ""
			}
		}
		out.Players = append(out.Players, PlayerTrail{Name: p.Name, Sequence: seq})
	}
	return out, nil
}

// buildTrailRaw walks the loc-change stream and emits a [Start, End)
// entry per residence. The final entry is closed at windowEnd (or
// match end). Entries entirely outside the window are dropped.
//
// The loc-change stream T, windowStart, windowEnd, and TrailEntry.Start/
// End are all int32 ms (schema v57 pure-ms model).
func buildTrailRaw(stream []result.ChangeI16, windowStart, windowEnd int32, locTable []string) []TrailEntry {
	if len(stream) == 0 {
		return nil
	}
	windowStartMs := windowStart
	windowEndMs := windowEnd
	out := make([]TrailEntry, 0, len(stream))
	for i, c := range stream {
		segStart := c.T
		var segEnd int32
		if i+1 < len(stream) {
			segEnd = stream[i+1].T
		} else {
			segEnd = windowEndMs
		}
		if segEnd <= windowStartMs {
			continue
		}
		if windowEndMs > 0 && segStart >= windowEndMs {
			break
		}
		if segStart < windowStartMs {
			segStart = windowStartMs
		}
		if windowEndMs > 0 && segEnd > windowEndMs {
			segEnd = windowEndMs
		}
		idx := int(c.V)
		locName := ""
		if idx >= 0 && idx < len(locTable) {
			locName = locTable[idx]
		}
		if locName == "" {
			continue
		}
		out = append(out, TrailEntry{
			Start: segStart,
			End:   segEnd,
			Loc:   locName,
			li:    c.V,
		})
	}
	return out
}

// mergeShortDwells folds entries shorter than minDwell into their
// preceding entry. Keeps the earlier loc name (its dwell extends to
// cover the dropped span), which matches the analyzer's blip-filter
// behaviour.
func mergeShortDwells(seq []TrailEntry, minDwell int32) []TrailEntry {
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
