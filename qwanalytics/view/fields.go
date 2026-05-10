package view

// Field codes from PLAN-event-streams-and-views-v3.md §4.3. These are
// the identical strings used in:
//
//   - JSON wire keys on result.PlayerStream
//   - BucketsOptions.Fields / StreamSliceOptions.Fields / StateAtOptions.Fields
//   - CLI -fields values (qw-analyze)
//   - WASM bridge JSON args
//   - future MCP tool inputs
//
// Adding a new field means: define a constant here, add it to
// AllStandardFields, register the default reducer in DefaultReducers,
// and document in qwanalytics/RESULT_SCHEMA.md.
const (
	FieldHealth    = "h"
	FieldArmor     = "a"
	FieldArmorType = "at"
	FieldLoc       = "li"
	FieldPosition  = "pos"

	FieldRL  = "rl"
	FieldLG  = "lg"
	FieldGL  = "gl"
	FieldSSG = "ssg"
	FieldSNG = "sng"

	FieldQuad = "q"
	FieldPent = "pe"
	FieldRing = "r"

	FieldShells  = "sh"
	FieldNails   = "nl"
	FieldRockets = "rk"
	FieldCells   = "cl"

	FieldSpawns = "sp"
	FieldDeaths = "d"
)

// FieldKind classifies a field's stream form so reducers + slicers can
// dispatch generically.
type FieldKind int

const (
	KindChangeI8 FieldKind = iota // int8-valued change stream
	KindChangeI16
	KindChangeStr
	KindInterval // bool intervals
	KindPosition // *PositionTrack
	KindEventList // []float64 timestamps (spawn/death)
)

// FieldKindFor returns the kind for a known field code; ok=false on an
// unknown code.
func FieldKindFor(code string) (FieldKind, bool) {
	k, ok := fieldKinds[code]
	return k, ok
}

var fieldKinds = map[string]FieldKind{
	FieldHealth:    KindChangeI16,
	FieldArmor:     KindChangeI16,
	FieldArmorType: KindChangeStr,
	FieldLoc:       KindChangeI16,
	FieldPosition:  KindPosition,

	FieldRL:  KindInterval,
	FieldLG:  KindInterval,
	FieldGL:  KindInterval,
	FieldSSG: KindInterval,
	FieldSNG: KindInterval,

	FieldQuad: KindInterval,
	FieldPent: KindInterval,
	FieldRing: KindInterval,

	FieldShells:  KindChangeI16,
	FieldNails:   KindChangeI16,
	FieldRockets: KindChangeI16,
	FieldCells:   KindChangeI16,

	FieldSpawns: KindEventList,
	FieldDeaths: KindEventList,
}

// AllStandardFields is the canonical iteration order — used as the
// default Fields filter and by the legacy bucket shim. Order chosen so
// downstream JSON has a stable key sequence (helpful for byte-level
// diffs across runs).
var AllStandardFields = []string{
	FieldHealth, FieldArmor, FieldArmorType, FieldLoc, FieldPosition,
	FieldRL, FieldLG, FieldGL, FieldSSG, FieldSNG,
	FieldQuad, FieldPent, FieldRing,
	FieldShells, FieldNails, FieldRockets, FieldCells,
	FieldSpawns, FieldDeaths,
}

// DefaultReducers maps each field to its default reducer name (D1
// from the plan: every field defaults to "last" except event-lists
// which default to "any" since "value at end of window" is undefined
// for spawn / death timestamps).
var DefaultReducers = map[string]string{
	FieldHealth:    "last",
	FieldArmor:     "last",
	FieldArmorType: "last",
	FieldLoc:       "last",
	FieldPosition:  "last",

	FieldRL:  "held-any",
	FieldLG:  "held-any",
	FieldGL:  "held-any",
	FieldSSG: "held-any",
	FieldSNG: "held-any",

	FieldQuad: "held-any",
	FieldPent: "held-any",
	FieldRing: "held-any",

	FieldShells:  "last",
	FieldNails:   "last",
	FieldRockets: "last",
	FieldCells:   "last",

	FieldSpawns: "any",
	FieldDeaths: "any",
}

// LegacyReducerSet reproduces v6 sampling-at-bucket-boundary
// semantics. Used by the WASM bridge's getDefaultBuckets shim so the
// web frontend renders identically to v6.
//
// "last" applies to every state field (vitals, ammo, loc, position,
// armor type). "held-any" applies to interval streams (weapons /
// powerups) — v6 used the same OR-fold. "any" applies to event-list
// streams (spawns / deaths) — v6 set the per-bucket flag if the event
// happened anywhere in the bucket window.
var LegacyReducerSet = map[string]string{
	FieldHealth:    "last",
	FieldArmor:     "last",
	FieldArmorType: "last",
	FieldLoc:       "last",
	FieldPosition:  "last",

	FieldRL:  "held-any",
	FieldLG:  "held-any",
	FieldGL:  "held-any",
	FieldSSG: "held-any",
	FieldSNG: "held-any",

	FieldQuad: "held-any",
	FieldPent: "held-any",
	FieldRing: "held-any",

	FieldShells:  "last",
	FieldNails:   "last",
	FieldRockets: "last",
	FieldCells:   "last",

	FieldSpawns: "any",
	FieldDeaths: "any",
}

// resolveReducerName picks the reducer for a field, allowing per-call
// overrides. Returns the registered Reducer or an error if the chosen
// name is not registered.
func resolveReducerName(field string, overrides map[string]string) (Reducer, error) {
	name, ok := overrides[field]
	if !ok {
		name = DefaultReducers[field]
	}
	if name == "" {
		name = "last"
	}
	return LookupReducer(name)
}

// validateFields returns an error if any code in fields is not a known
// field. Empty input is fine.
func validateFields(fields []string) error {
	for _, f := range fields {
		if _, ok := fieldKinds[f]; !ok {
			return fieldErr(f)
		}
	}
	return nil
}

type unknownFieldError struct{ Code string }

func (e unknownFieldError) Error() string { return "unknown field code " + e.Code }

func fieldErr(code string) error { return unknownFieldError{Code: code} }
