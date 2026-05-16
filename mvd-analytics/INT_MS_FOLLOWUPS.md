# Schema v8 int-ms time migration — completed

Schema v8 migrated **every timestamped field** in the result schema
from float64/float32 seconds to `int32` milliseconds. Earlier drafts
of this document captured the deferred half of the migration
(`ChangeI16.T`, `Interval.Start/End`, `MatchEvent.Time`, frag/powerup
event times, etc.) as a separate follow-up; that work has now landed
in the same v8 cut so the whole schema is consistent.

This file is kept as a record of the design decision and what to
audit when extending the schema in the future. For the live
field-by-field reference see [RESULT_SCHEMA.md](RESULT_SCHEMA.md).

## What's in schema v8

Every time field is `int32` milliseconds:

- `PositionTrack.T` (already migrated in the first cut)
- `PlayerStream.Spawns` / `Deaths` (already migrated in the first cut)
- `ChangeI16.T` / `ChangeI8.T` / `ChangeStr.T`
- `Interval.Start` / `End`
- `GlobalStream.MatchStart` / `MatchEnd`
- `MatchResult.Duration` / `StartTime` / `EndTime`
- `TimelineAnalysisResult.MatchStartTime` / `DemoOffset`
- `TimelineFragEvent.Time`
- `PowerupEvent.Time` / `EndTime` / `Duration`
- `FragStreakEvent.Time` / `EndTime` / `Duration`
- `MatchEvent.Time`
- `FragEntry.Time`
- `BackpackDrop.Time`
- `WeaponPickup.Time` / `NextDeathTime` / `DropTime`
- `ItemPhase.AvailableFrom` / `TakenAt` / `RespawnAt`
- `HighResBucket.T`

JSON keys are unchanged from v7. External consumers reading these
values as seconds must scale by `* 0.001`. The schema-version bump
is the signal.

## What stayed float64 seconds

The **view-layer query API** keeps its public surface in float64
seconds for ergonomic consumer code:

- `view.Buckets` — `ViewBucket.T` is `float64` seconds
- `view.Events` — `TaggedEvent.T` is `float64` seconds (plus
  `detail.endTime` / `detail.duration` for the powerup / streak event
  types, also seconds)
- `view.StreamSlice` — `StartTime` / `EndTime` opts are seconds
- `view.StateAt` — `opts.Time` is seconds; nothing in the response
  carries time anyway
- `view.LocTrails` — `TrailEntry.Start` / `End` and `LifeTrack.SpawnTime` /
  `DeathTime`, `TrackPosition.Time` are seconds

The view layer is the boundary where ms→seconds conversion happens
exactly once. All internal comparisons against schema fields use ms;
emission to public views uses `float64(tMs) * 0.001`.

The frontend (`mvd-web/static/app.js`) reads the WASM bridge through
these view-layer outputs almost entirely — the few sites that read
raw schema fields (chat panel, key moments, backpacks, items panel,
weapon-pickup overlay, hub-viewer URL builders) convert ms→seconds at
the read site. The bulk happens in one spot — `displayTimelineAnalysis`
projects everything onto `timelineState` in seconds, and every
downstream UI panel consumes the seconds view.

## How to add a new timestamped field

The convention:

1. **Storage**: `int32` milliseconds in the result schema. Same JSON
   key shape as adjacent fields.
2. **Producer** (`analyzer/`): if the event you're consuming has a
   `TimeMs int32` field, use that directly. Otherwise convert at the
   write site with `msTime(e.Time)` — defined in
   [`analyzer/timeline_streams.go`](analyzer/timeline_streams.go).
   The conversion is well-conditioned (`int32(math.Round(t*1000))`)
   because the float64-seconds value is itself derived once from the
   decoder's `int32`-ms accumulator and never re-accumulated.
3. **View-layer dispatcher**: if the new field needs to be queryable
   via `view.Buckets` / `view.Events` / `view.StreamSlice` /
   `view.StateAt`, follow the existing pattern — accept window
   bounds in float64 seconds, convert to int32 ms once at entry, do
   the comparison in int32 ms, emit float64 seconds at the public
   output. Don't push the ms unit through the view's public surface
   without a deliberate design decision.
4. **Postprocess** (`normalizeMatchRelativeTimes` in
   `analyzer/postprocess.go`): if the new field shifts with match
   start, add it here. It works entirely in int32 ms; `matchStartMs`
   comes from `res.TimelineAnalysis.MatchStartTime` directly.
5. **Tests**: write fixtures with int32-ms literals (`Time: 5000`
   not `Time: 5.0`).
6. **Frontend**: if the new field is consumed directly from the raw
   schema (not via view layer), add a `* 0.001` at the read site. If
   it's consumed via `getBuckets` / `getEvents` etc., no change.

## Why this design

The wire format carries time as a 1-byte millisecond delta per
message; the canonical decoder accumulator (`mvd.Decoder.timeMs`)
keeps this integer end-to-end. Float seconds is a derived view, not
a source of truth.

Storing schema time as `int32` ms:

- Eliminates float-precision drift across boundary comparisons. The
  motivating bug was a gib-respawn case where a spawn-boundary at
  wire-exact `658.279000` compared against a position sample
  narrowed to `658.278992` produced a spurious `MH.low → start`
  teleport edge in locgraph.
- Keeps the comparison cost flat — `int32 <= int32` is exact and
  cheap.
- Removes a class of "this float number has a trailing `0000007`
  artifact" surprises in JSON output, making goldens stable and
  human-readable.
- Lets postprocess collapse what used to be three families of
  `shiftAndFilter*` helpers (one per element type) into a single
  int32-typed implementation per element shape.

`int32` ms gives ±24.8 days of range — comfortable for matches that
last minutes to hours.

## See also

- [RESULT_SCHEMA.md](RESULT_SCHEMA.md) — live field reference
- [`mvd-reader/MVD_FORMAT.md`](../mvd-reader/MVD_FORMAT.md) — the wire
  format's millisecond delta encoding
- [`mvd-reader/mvd/decoder.go`](../mvd-reader/mvd/decoder.go) — the
  canonical int32-ms accumulator
