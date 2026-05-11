package view

import (
	"fmt"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// StateAtOptions specifies the moment in time to interrogate plus the
// optional player / field filter. Time is required.
type StateAtOptions struct {
	Time    float64
	Players []string
	Fields  []string
}

// StateAtView returns each requested player's state at Time. Empty
// players slice → no players matched the filter.
type StateAtView struct {
	Time    float64                  `json:"t"`
	Players map[string]PlayerStateAt `json:"players"`
}

// PlayerStateAt holds each requested field at Time. Pointers (and
// omitempty) make it possible for JSON to omit fields that weren't
// requested AND fields that have no data yet at Time.
type PlayerStateAt struct {
	Health    *int16      `json:"h,omitempty"`
	Armor     *int16      `json:"a,omitempty"`
	ArmorType *string     `json:"at,omitempty"`
	Loc       *int16      `json:"li,omitempty"`
	Pos       *Position3D `json:"pos,omitempty"`

	RL  *bool `json:"rl,omitempty"`
	LG  *bool `json:"lg,omitempty"`
	GL  *bool `json:"gl,omitempty"`
	SSG *bool `json:"ssg,omitempty"`
	SNG *bool `json:"sng,omitempty"`

	Quad *bool `json:"q,omitempty"`
	Pent *bool `json:"pe,omitempty"`
	Ring *bool `json:"r,omitempty"`

	Shells  *int16 `json:"sh,omitempty"`
	Nails   *int16 `json:"nl,omitempty"`
	Rockets *int16 `json:"rk,omitempty"`
	Cells   *int16 `json:"cl,omitempty"`
}

// Position3D is the JSON-friendly companion to PositionTrack for
// point-in-time results. Snapped to the nearest sample.
type Position3D struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
	Z int32 `json:"z"`
}

// StateAt resolves each requested field at Time per player. For
// change streams, returns the latest entry with T <= Time. For
// intervals, returns true iff Time falls inside any interval. For
// position, returns the nearest sample by T (no interpolation).
//
// Spawns/Deaths are explicitly rejected — they're discrete events
// without a "state at time" notion. Use Events() for those.
func StateAt(r *result.Result, opts StateAtOptions) (*StateAtView, error) {
	if r == nil || r.Streams == nil {
		return &StateAtView{Time: opts.Time, Players: map[string]PlayerStateAt{}}, nil
	}
	fields := opts.Fields
	if len(fields) == 0 {
		fields = stateAtDefaultFields()
	}
	if err := validateFields(fields); err != nil {
		return nil, err
	}
	// Spawns/Deaths are not state — reject the request rather than
	// silently dropping them.
	for _, f := range fields {
		if f == FieldSpawns || f == FieldDeaths {
			return nil, fmt.Errorf("field %q has no point-in-time meaning; use view.Events() instead", f)
		}
	}
	requested := make(map[string]bool, len(fields))
	for _, f := range fields {
		requested[f] = true
	}
	pf := newPlayerFilter(opts.Players)

	out := &StateAtView{Time: opts.Time, Players: make(map[string]PlayerStateAt)}
	for _, p := range r.Streams.Players {
		if !pf.accepts(p.Name) {
			continue
		}
		ps := PlayerStateAt{}
		if requested[FieldHealth] {
			if idx := indexI16AtOrBefore(p.Health, opts.Time); idx >= 0 {
				v := p.Health[idx].V
				ps.Health = &v
			}
		}
		if requested[FieldArmor] {
			if idx := indexI16AtOrBefore(p.Armor, opts.Time); idx >= 0 {
				v := p.Armor[idx].V
				ps.Armor = &v
			}
		}
		if requested[FieldArmorType] {
			if idx := indexStrAtOrBefore(p.ArmorType, opts.Time); idx >= 0 {
				v := p.ArmorType[idx].V
				ps.ArmorType = &v
			}
		}
		if requested[FieldLoc] {
			if idx := indexI16AtOrBefore(p.Loc, opts.Time); idx >= 0 {
				v := p.Loc[idx].V
				ps.Loc = &v
			}
		}
		if requested[FieldShells] {
			if idx := indexI16AtOrBefore(p.Shells, opts.Time); idx >= 0 {
				v := p.Shells[idx].V
				ps.Shells = &v
			}
		}
		if requested[FieldNails] {
			if idx := indexI16AtOrBefore(p.Nails, opts.Time); idx >= 0 {
				v := p.Nails[idx].V
				ps.Nails = &v
			}
		}
		if requested[FieldRockets] {
			if idx := indexI16AtOrBefore(p.Rockets, opts.Time); idx >= 0 {
				v := p.Rockets[idx].V
				ps.Rockets = &v
			}
		}
		if requested[FieldCells] {
			if idx := indexI16AtOrBefore(p.Cells, opts.Time); idx >= 0 {
				v := p.Cells[idx].V
				ps.Cells = &v
			}
		}

		if requested[FieldRL] {
			ps.RL = boolPtr(intervalContains(p.RL, opts.Time))
		}
		if requested[FieldLG] {
			ps.LG = boolPtr(intervalContains(p.LG, opts.Time))
		}
		if requested[FieldGL] {
			ps.GL = boolPtr(intervalContains(p.GL, opts.Time))
		}
		if requested[FieldSSG] {
			ps.SSG = boolPtr(intervalContains(p.SSG, opts.Time))
		}
		if requested[FieldSNG] {
			ps.SNG = boolPtr(intervalContains(p.SNG, opts.Time))
		}
		if requested[FieldQuad] {
			ps.Quad = boolPtr(intervalContains(p.Quad, opts.Time))
		}
		if requested[FieldPent] {
			ps.Pent = boolPtr(intervalContains(p.Pent, opts.Time))
		}
		if requested[FieldRing] {
			ps.Ring = boolPtr(intervalContains(p.Ring, opts.Time))
		}

		if requested[FieldPosition] && p.Position != nil && len(p.Position.T) > 0 {
			idx := nearestPositionIndex(p.Position, opts.Time)
			if idx >= 0 {
				ps.Pos = &Position3D{X: p.Position.X[idx], Y: p.Position.Y[idx], Z: p.Position.Z[idx]}
			}
		}

		out.Players[p.Name] = ps
	}
	return out, nil
}

func boolPtr(b bool) *bool { return &b }

// stateAtDefaultFields excludes spawn / death (no state-at meaning).
func stateAtDefaultFields() []string {
	out := make([]string, 0, len(AllStandardFields))
	for _, f := range AllStandardFields {
		if f == FieldSpawns || f == FieldDeaths {
			continue
		}
		out = append(out, f)
	}
	return out
}

// nearestPositionIndex finds the position sample closest to t. If t
// is between two samples, the closer one wins; ties go to the earlier
// sample. -1 if pt is empty.
func nearestPositionIndex(pt *result.PositionTrack, t float64) int {
	if len(pt.T) == 0 {
		return -1
	}
	best := -1
	bestDiff := 0.0
	for i := range pt.T {
		diff := float64(pt.T[i]) - t
		if diff < 0 {
			diff = -diff
		}
		if best == -1 || diff < bestDiff {
			best = i
			bestDiff = diff
		}
	}
	return best
}
