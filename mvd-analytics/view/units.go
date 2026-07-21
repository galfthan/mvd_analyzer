package view

import "github.com/mvd-analyzer/mvd-analytics/result"

// Time-unit ECHO for the REST transport surface (schema v56).
//
// `timeUnit` is the unit of every time value in a response (API.md §2.1).
// EVERY `/v1/demos/{id}/*` JSON response carries it, with two principled
// exceptions: `/demoinfo` (KTX's own clock, a mix of native units) and
// `/artifacts/{name}` (the raw stored result sections, served byte-for-byte).
// There is NO unit selection — the value is FIXED per endpoint:
//
//   - "ms" (int32 milliseconds): the stored pass-throughs (frags, damage,
//     shots, chat, airgibs, backpacks, weapon-pickups, items timeline,
//     overview), plus /aim (dense crosshair `t` + lgRamp `since`), the
//     columnar /buckets axis (startMs/windowMs), and /region-control (windowMs).
//   - "s" (float64 seconds): the derived views (events, buckets rows, state-at,
//     stream-slice envelope, loc-trails, items summary).
//
// The sparse match-position field names are still absolute: a `t` is int32 ms
// and a top-level `time` is float seconds on every endpoint; `timeUnit` names
// the unit of the descriptively-named times (startTime, respawnAt, duration, …)
// that a reader would otherwise have to guess.
//
// The stored result.* structs and their JSON tags are the ON-DISK contract
// (qw-analyze / WASM emit them verbatim, in ms) and are left untouched. The
// pass-through envelopes below embed the stored struct so its fields FLATTEN at
// the HTTP boundary and can never drift from the on-disk shape; the four bare-
// array bodies gain a {timeUnit, <list>} object so the echo has somewhere to
// live. The derived views carry the echo on their own view struct (see the
// TimeUnit field on EventsView / BucketsView / ColumnarBuckets / StateAtView /
// StreamSliceView / LocTrailsView / ItemsSummaryView), set by the mvd-api
// handler.

// TimeUnit is the fixed native unit a governed response echoes for its
// descriptively-named match-position timestamps.
type TimeUnit string

const (
	// UnitMs marks a response whose descriptive times are int32 milliseconds.
	UnitMs TimeUnit = "ms"
	// UnitSec marks a response whose descriptive times are float64 seconds.
	UnitSec TimeUnit = "s"
)

// --- pass-through stored sections: {timeUnit} + the embedded stored struct ---
//
// Embedding flattens the stored struct's JSON fields at the top level, so the
// body is byte-identical to the stored section plus a leading timeUnit — no
// parallel field list to keep in sync.

// FragsEnvelope wraps the stored /frags section (ms-native).
type FragsEnvelope struct {
	TimeUnit TimeUnit `json:"timeUnit"`
	*result.FragResult
}

// DamageEnvelope wraps the stored /damage section (ms-native).
type DamageEnvelope struct {
	TimeUnit TimeUnit `json:"timeUnit"`
	*result.DamageResult
}

// ShotsEnvelope wraps the stored /shots section (ms-native).
type ShotsEnvelope struct {
	TimeUnit TimeUnit `json:"timeUnit"`
	*result.ShotsResult
}

// ItemsEnvelope wraps the stored /items phase-timeline section (ms-native).
type ItemsEnvelope struct {
	TimeUnit TimeUnit `json:"timeUnit"`
	*result.ItemsResult
}

// AimEnvelope wraps the /aim section (ms-native). Aim's payload is dense
// (crosshair `t`, lgRamp `since`) and entirely int32 ms, so the echo is a
// constant "ms". The /artifacts/{name} accessor keeps serving the stored
// AimResult raw (no envelope) — only the curated endpoint echoes.
type AimEnvelope struct {
	TimeUnit TimeUnit `json:"timeUnit"`
	*result.AimResult
}

// RegionControlEnvelope wraps the /region-control view (ms-native): its
// windowMs axis is int32 ms. RegionControlView aliases result.RegionControlResult
// (also baked into the parse-time Result), so embedding flattens its fields at
// the HTTP boundary and can never drift from that shape.
type RegionControlEnvelope struct {
	TimeUnit TimeUnit `json:"timeUnit"`
	*result.RegionControlResult
}

// --- bare-array bodies: {timeUnit, <list>} object so the echo has a home ---
//
// The wrapped slices are the stored result.* types verbatim (ms-native); the
// view constructors already return a non-nil empty slice, so the list is [] —
// never null — when nothing matches.

// ChatEnvelope wraps the /chat message list (ms-native).
type ChatEnvelope struct {
	TimeUnit TimeUnit            `json:"timeUnit"`
	Messages []result.MatchEvent `json:"messages"`
}

// AirgibsEnvelope wraps the /airgibs key-moment list (ms-native).
type AirgibsEnvelope struct {
	TimeUnit TimeUnit             `json:"timeUnit"`
	Airgibs  []result.AirgibEvent `json:"airgibs"`
}

// BackpacksEnvelope wraps the /backpacks drop list (ms-native).
type BackpacksEnvelope struct {
	TimeUnit  TimeUnit              `json:"timeUnit"`
	Backpacks []result.BackpackDrop `json:"backpacks"`
}

// WeaponPickupsEnvelope wraps the /weapon-pickups list (ms-native).
type WeaponPickupsEnvelope struct {
	TimeUnit TimeUnit              `json:"timeUnit"`
	Pickups  []result.WeaponPickup `json:"pickups"`
}
