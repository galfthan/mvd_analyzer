package view

import (
	"github.com/mvd-analyzer/qwanalytics/result"
)

// LegacyHighResBucket is the v6 wire shape produced for the WASM
// frontend's existing panels. The new query API uses ViewBucket
// (clean per-player map). Phase 1.5 of the plan migrates the panels
// to call view.Buckets directly, at which point this shim can be
// deleted.
type LegacyHighResBucket = result.HighResBucket

// LegacyHighResPlayerData is re-exported for symmetry; it is the same
// type the v6 frontend expects under HighResBucket.P[name].
type LegacyHighResPlayerData = result.HighResPlayerData

// LegacyHighResTeamData is re-exported for symmetry; same as v6 TD
// shape.
type LegacyHighResTeamData = result.HighResTeamData

// ToLegacyHighResBuckets transforms a BucketsView (clean shape) into
// the v6 []HighResBucket the frontend reads. Empty input → nil; empty
// player data → empty bucket (matching v6 behaviour).
//
// The transformation expects bv to have been produced via Buckets()
// with IncludeTeam=true and the LegacyReducerSet (every field → last)
// — anything else risks silent visual drift in the frontend.
//
// Locator note: HighResPlayerData.Li is an integer index into
// TimelineAnalysisResult.LocTable. The reducer hands the loc index
// back as int16 (see fields.go's KindChangeI16); cast it to int here
// — we don't carry forward type-tagged metadata between bucket
// computation and legacy export.
func ToLegacyHighResBuckets(bv *BucketsView) []result.HighResBucket {
	if bv == nil || len(bv.Buckets) == 0 {
		return nil
	}
	out := make([]result.HighResBucket, 0, len(bv.Buckets))
	for _, vb := range bv.Buckets {
		if len(vb.Players) == 0 {
			continue
		}
		hb := result.HighResBucket{
			T: vb.T,
			P: make(map[string]*result.HighResPlayerData, len(vb.Players)),
		}
		for name, pdata := range vb.Players {
			hb.P[name] = legacyPlayerFromMap(pdata)
		}
		if len(vb.Team) > 0 {
			hb.TD = make(map[string]*result.HighResTeamData, len(vb.Team))
			for team, td := range vb.Team {
				hb.TD[team] = legacyTeamFromMap(td)
			}
		}
		out = append(out, hb)
	}
	return out
}

func legacyPlayerFromMap(pdata map[string]any) *result.HighResPlayerData {
	out := &result.HighResPlayerData{}
	if v, ok := pdata[FieldHealth]; ok {
		if n, ok := numericFromAny(v); ok {
			out.H = int(n)
		}
	}
	if v, ok := pdata[FieldArmor]; ok {
		if n, ok := numericFromAny(v); ok {
			out.A = int(n)
		}
	}
	if v, ok := pdata[FieldArmorType]; ok {
		if s, ok := v.(string); ok {
			out.AT = s
		}
	}
	if v, ok := pdata[FieldLoc]; ok {
		if n, ok := numericFromAny(v); ok {
			out.Li = int(n)
		}
	}
	if v, ok := pdata[FieldPosition]; ok {
		if pos, ok := v.([3]int32); ok {
			out.X = float32(pos[0])
			out.Y = float32(pos[1])
			out.Z = float32(pos[2])
		}
	}

	out.RL = boolField(pdata, FieldRL)
	out.LG = boolField(pdata, FieldLG)
	out.GL = boolField(pdata, FieldGL)
	out.SSG = boolField(pdata, FieldSSG)
	out.SNG = boolField(pdata, FieldSNG)
	out.Q = boolField(pdata, FieldQuad)
	out.Pent = boolField(pdata, FieldPent)
	out.R = boolField(pdata, FieldRing)

	if v, ok := pdata[FieldShells]; ok {
		if n, ok := numericFromAny(v); ok {
			out.Shells = int(n)
		}
	}
	if v, ok := pdata[FieldNails]; ok {
		if n, ok := numericFromAny(v); ok {
			out.Nails = int(n)
		}
	}
	if v, ok := pdata[FieldRockets]; ok {
		if n, ok := numericFromAny(v); ok {
			out.Rockets = int(n)
		}
	}
	if v, ok := pdata[FieldCells]; ok {
		if n, ok := numericFromAny(v); ok {
			out.Cells = int(n)
		}
	}

	out.Sp = boolField(pdata, FieldSpawns)
	out.D = boolField(pdata, FieldDeaths)
	return out
}

func legacyTeamFromMap(td map[string]any) *result.HighResTeamData {
	out := &result.HighResTeamData{
		RL:   intFromAny(td["rl"]),
		LG:   intFromAny(td["lg"]),
		RLLG: intFromAny(td["rllg"]),
		W:    intFromAny(td["w"]),
		GL:   intFromAny(td["gl"]),
		Q:    intFromAny(td["q"]),
		Pe:   intFromAny(td["pe"]),
		R:    intFromAny(td["r"]),
		Pw:   intFromAny(td["pw"]),
		TH:   intFromAny(td["th"]),
		TA:   intFromAny(td["ta"]),
	}
	if abt, ok := td["abt"].(map[string]int); ok && len(abt) > 0 {
		out.ABT = make(map[string]int, len(abt))
		for k, v := range abt {
			out.ABT[k] = v
		}
	}
	return out
}

// intFromAny coerces a map[string]any value back to int. Counter
// values from aggregateTeams are stored as int; anything else returns
// 0.
func intFromAny(v any) int {
	if v == nil {
		return 0
	}
	if n, ok := v.(int); ok {
		return n
	}
	return 0
}
