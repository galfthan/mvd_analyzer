package result

// Streams is the canonical native-rate storage for per-player and
// global state changes. Read by the qwanalytics/view query API.
//
// Each PlayerStream records every change to a tracked field at the
// rate it actually changed (see appendChange semantics). Position is
// the only field that records every native-rate sample without dedup;
// every other field is sparse — entries represent transitions, not
// per-tick samples.
type Streams struct {
	Players []PlayerStream `json:"players"`
	Global  GlobalStream   `json:"global"`
}

// PlayerStream is one player's full event-rate state record. Name is
// the canonical demoinfo-resolved player name; if two slots collide
// on a single name within one match, the second is suffixed
// "name#slotIndex". Mid-match name changes are folded into the same
// stream by the analyser's existing canonicalisation.
type PlayerStream struct {
	Name string `json:"name"`
	Team string `json:"team,omitempty"`

	// Position track at native rate. Always populated in-memory; whether
	// it is serialised to JSON is controlled at marshal time (the CLI's
	// -include positions flag and equivalent transports). Nil when the
	// player produced no position events.
	Position *PositionTrack `json:"pos,omitempty"`

	// Discrete state-change streams. Sparse — every entry is a transition.
	// Health/armor use int16: Quake values can reach 250 (mega-health,
	// red armor) which exceeds int8 range.
	Health    []ChangeI16 `json:"h,omitempty"`
	Armor     []ChangeI16 `json:"a,omitempty"`
	ArmorType []ChangeStr `json:"at,omitempty"` // "ga"|"ya"|"ra"|""
	Loc       []ChangeI16 `json:"li,omitempty"` // index into TimelineAnalysisResult.LocTable

	// Inventory presence as half-open intervals [Start, End). One entry
	// per period the field was true. Open intervals at match end are
	// closed at MatchEnd by the analyser.
	RL  []Interval `json:"rl,omitempty"`
	LG  []Interval `json:"lg,omitempty"`
	GL  []Interval `json:"gl,omitempty"`
	SSG []Interval `json:"ssg,omitempty"`
	SNG []Interval `json:"sng,omitempty"`

	Quad []Interval `json:"q,omitempty"`
	Pent []Interval `json:"pe,omitempty"`
	Ring []Interval `json:"r,omitempty"`

	// Ammo as change streams (dedup against last value).
	Shells  []ChangeI16 `json:"sh,omitempty"`
	Nails   []ChangeI16 `json:"nl,omitempty"`
	Rockets []ChangeI16 `json:"rk,omitempty"`
	Cells   []ChangeI16 `json:"cl,omitempty"`

	// Discrete event timestamps (no value).
	Spawns []float64 `json:"sp,omitempty"`
	Deaths []float64 `json:"d,omitempty"`
}

// GlobalStream carries match-window anchors so consumers can resolve
// what "start" / "end" mean without cross-referencing other Result
// fields.
type GlobalStream struct {
	MatchStart float64 `json:"matchStart"`
	MatchEnd   float64 `json:"matchEnd"`
}

// PositionTrack is columnar to compress JSON. Indices align across the
// five arrays. Coordinates are int32 — Quake maps can exceed ±32 768
// in any axis, so int16 would silently truncate.
//
// Li is the resolved loc-name index per native-rate sample (indexes
// into TimelineAnalysisResult.LocTable, with 0 = "no loc"). Populated
// during analyzer Finalize (after the loc finder is loaded), then
// smoothed by the blip filter. Downstream consumers — the loc graph
// builder, region control, and the FieldLoc bucket reducer in
// view.Buckets — read this column directly instead of deriving locs
// from x/y/z separately.
type PositionTrack struct {
	T  []float32 `json:"t"`
	X  []int32   `json:"x"`
	Y  []int32   `json:"y"`
	Z  []int32   `json:"z"`
	Li []int16   `json:"li,omitempty"`
}

// ChangeI8 is a single transition in an int8 stream.
type ChangeI8 struct {
	T float64 `json:"t"`
	V int8    `json:"v"`
}

// ChangeI16 is a single transition in an int16 stream.
type ChangeI16 struct {
	T float64 `json:"t"`
	V int16   `json:"v"`
}

// ChangeStr is a single transition in a string-valued stream.
type ChangeStr struct {
	T float64 `json:"t"`
	V string  `json:"v"`
}

// Interval is a half-open period [Start, End) during which a boolean
// field was true.
type Interval struct {
	Start float64 `json:"s"`
	End   float64 `json:"e"`
}
