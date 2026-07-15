package view

import "github.com/mvd-analyzer/mvd-analytics/result"

// Time-unit ECHO for the REST transport surface (schema v56).
//
// The rule (API.md §2.1) is a fixed, per-endpoint naming convention — there is
// NO unit selection:
//
//   - The sparse match-position fields `t` (float seconds) and `time` (int32
//     milliseconds) always carry those units, on every endpoint.
//   - Descriptively-named times (startTime/endTime, availableFrom/takenAt/
//     respawnAt, nextDeathTime, dropTime, duration, start, …) carry the
//     endpoint's NATIVE unit, declared by the response's `timeUnit` echo:
//     "ms" for the stored pass-throughs (frags, damage, shots, chat, airgibs,
//     backpacks, weapon-pickups, items timeline, overview) and "s" for the
//     derived views (events, buckets rows, state-at, stream-slice envelope,
//     loc-trails, items summary).
//   - Every governed response echoes `timeUnit` so a reader never has to guess
//     which unit a descriptive field is in.
//   - Dense per-sample payloads are the documented exception and are always
//     int32 ms under compact names (aim crosshair `t` + lgRamp `since`;
//     stream-slice embedded tracks); the columnar buckets axis is Ms-suffixed
//     (startMs/windowMs). Those carry no echo and never move.
//
// The stored result.* structs and their JSON tags are the ON-DISK contract
// (qw-analyze / WASM emit them verbatim, in ms) and are left untouched. The
// pass-through envelopes below embed the stored struct so its fields FLATTEN at
// the HTTP boundary and can never drift from the on-disk shape; the four bare-
// array bodies gain a {timeUnit, <list>} object so the echo has somewhere to
// live. The derived views carry the echo on their own view struct (see the
// TimeUnit field on EventsView / BucketsView / StateAtView / StreamSliceView /
// LocTrailsView / ItemsSummaryView), set by the mvd-api handler.

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
