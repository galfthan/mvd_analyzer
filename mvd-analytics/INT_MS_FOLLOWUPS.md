# Schema v8 int-ms time — deferred follow-ups

Schema v8 migrated `PositionTrack.T` and per-player `Spawns` / `Deaths`
from float seconds to `int32` milliseconds (see
[`result/result.go`](result/result.go) v8 history block and
[`RESULT_SCHEMA.md`](RESULT_SCHEMA.md#positiontrack) for the
production cut). Three other timestamped result-schema field families
were considered for the same migration and **deliberately deferred**.
This document captures what they are, why deferring was the right call
for the v8 cut, and what the pros and cons of revisiting that decision
look like.

## What was deferred

### 1. `ChangeI16` / `ChangeI8` / `ChangeStr` — sparse change streams

[`result/streams.go`](result/streams.go) ~lines 91-106:

```go
type ChangeI16 struct {
    T float64 `json:"t"` // match-relative seconds
    V int16   `json:"v"`
}
```

Used for the per-player **Health**, **Armor**, **Shells / Nails /
Rockets / Cells** (ammo), **ArmorType**, and **Loc** fields on
`PlayerStream`. One entry per actual transition; typically a few
hundred entries per match per player.

### 2. `Interval` — half-open ownership periods

```go
type Interval struct {
    Start float64 `json:"s"` // match-relative seconds
    End   float64 `json:"e"`
}
```

Used for weapon possession (**RL**, **LG**, **GL**, **SSG**, **SNG**)
and powerup possession (**Quad**, **Pent**, **Ring**). One entry per
contiguous ownership span.

### 3. Frag / message / powerup event times

- `result/messages.go` — `MatchEvent.Time float64` (frag, chat,
  teamsay events on `Result.Messages.Events`).
- `result/timeline.go` (or equivalent) — `TimelineFragEvent.Time`,
  `TimelinePowerupEvent.Time/EndTime`.
- `analyzer/locgraph.go` — `LocNode.Total`, `LocEdge` time
  accumulators stay seconds.

## Why deferred for v8

The bug class that motivated v8 was specifically the **boundary
comparison** `boundaries[bIdx] <= t` in
[`analyzer/locgraph.go`](analyzer/locgraph.go) and
[`analyzer/timeline_streams.go`](analyzer/timeline_streams.go) — where
`boundaries` was built from `Spawns` / `Deaths` and `t` was
`PositionTrack.T`. Both sides crossed the float32-persistence boundary,
both sides drifted, and the drift was enough to land on the wrong side
of an edge. Concretely: a gib-respawn at wire-exact `658.279000` ended
up compared against a position sample narrowed to `658.278992`, the
boundary cursor failed to reset, and locgraph emitted a spurious
`MH.low → start` teleport edge.

`ChangeI16.T` / `Interval` / `MatchEvent.Time` never participate in
that comparison. They are produced once from the decoder's canonical
`int32`-ms source via `float64(timeMs) * 0.001` and consumed against
**view-layer bucket windows** (also float64 seconds, produced from the
same source). There's no second lossy step to amplify error and no
known precision-class bug.

So v8 took the smallest cut that fixed the bug: change the two types
that mattered, leave the rest. Scope stayed local; one schema bump,
one golden regen.

## Pros of migrating the deferred families later

- **Uniform internal time idiom in the view layer.** Today
  `view/buckets.go`, `view/stateat.go`, `view/streamslice.go`,
  `view/events.go` work in **int32 ms** when comparing windows against
  `PositionTrack.T` / `Spawns` / `Deaths`, and in **float64 seconds**
  when comparing windows against `ChangeI16.T` / `Interval.Start/End` /
  `MatchEvent.Time`. Two idioms in one file, conversion at the meeting
  points. Migrating would collapse this to a single ms idiom with a
  single seconds-conversion site at `ViewBucket.T` emission.
- **Eliminates a class of "obviously dirty" JSON numbers.** Even with
  v8 in place, ChangeI16/Interval/MatchEvent times still show up as
  e.g. `0.5499999999999972` in the JSON — visually noisy and
  surprising. Integer ms is `550` and unambiguous.
- **Marginally smaller JSON.** Integer ms tends to encode in fewer
  characters than decimal seconds for typical match durations
  (`550` vs `0.5499999999999972`), especially after the float-noise
  trailing zeros. Probably a low single-digit percent on a typical
  result.
- **Symmetry with the existing v8 migration.** Future readers don't
  have to re-derive why two fields use ms and the rest use seconds.
  One rule is simpler than a partial.
- **Locgraph node-time precision.** Today
  `LocNode.Total` accumulates float64 seconds derived from int-ms
  deltas. Migrating to integer ms throughout the locgraph and exposing
  ms in the output would let consumers do exact arithmetic (e.g.
  "RA control = 35,200 ms / 600,000 ms" is exact; the same in float
  seconds isn't).

## Cons / costs of migrating

- **Wider JSON-schema break.** `mvd-api/handlers.go` filters
  `MatchEvent.Time` by query parameter; the public REST contract sees
  the unit change. The schema-version bump signals it, but anyone
  consuming the API outside this repo has to rewrite parsing on every
  affected field — and there are many fields (every per-player change
  stream, every interval, every MatchEvent.Time, every TimelineFrag /
  Powerup event). v8's break was scoped to three fields; a "v9 full
  ms" break would touch dozens.
- **Test churn.** Every test that builds a Change/Interval/MatchEvent
  literal with a float-second time value has to be re-typed and
  re-numbered. The analyzer test suite has hundreds of these. v8's
  test changes were a few dozen because Spawns/Deaths only show up in
  ~5 fixtures; a full migration multiplies that by ~10×.
- **No bug fix attached.** The whole reason v8 happened was a visible
  failure mode (spurious teleport edges). Migrating the rest is
  cosmetic uniformity — a refactor, not a fix. Hard to justify the
  break window without something else needing it.
- **Frontend impact resurfaces.** With v8, `ViewBucket.T` stays
  float64 seconds and `mvd-web/static/app.js` is completely
  untouched. If `MatchEvent.Time` flips to ms, the frontend's chat
  panel, frag-feed panel, and time-range filters all need a
  `* 0.001` scale where they currently consume seconds verbatim. The
  v8 cut was deliberately designed to avoid this.
- **Postprocess simplification is limited.** `postprocess.go` already
  has `shiftAndFilterChangeI16` / `shiftAndFilterChangeStr` /
  `shiftAndFilterIntervals` / `shiftAndFilterInts` — four helpers
  doing the same shape of work in different types. Migrating would
  collapse three of them into the int32 variant, saving a few dozen
  lines. Modest win.

## Recommendation

Keep deferred unless one of the following triggers a fresh cut:

1. A new precision-class bug surfaces that involves any of these
   fields crossing a comparison boundary (e.g. someone adds a feature
   that compares `Interval.Start` against `PositionTrack.T`
   directly).
2. The view-layer mixed-unit comparison sites become genuinely
   confusing — i.e. when reading
   [`view/buckets.go`](view/buckets.go),
   [`view/streamslice.go`](view/streamslice.go), or
   [`view/stateat.go`](view/stateat.go), the cognitive cost of
   tracking "is this comparison in ms or seconds?" starts producing
   bugs.
3. A different schema break is happening anyway (a v9 motivated by
   something else), and folding this in is cheap on the
   already-planned diff.

Outside those triggers, the partial migration is the right resting
state: the bug is fixed, the JSON-break surface area was minimised,
and the cosmetic "all timestamps in one unit" win isn't worth the
test/API churn on its own.

## Implementation sketch (if revisited)

For a future v9 that finishes the migration:

1. **Types** in `result/streams.go`:
   ```go
   type ChangeI16 struct { T int32 `json:"t"`; V int16 `json:"v"` }
   type ChangeI8  struct { T int32 `json:"t"`; V int8  `json:"v"` }
   type ChangeStr struct { T int32 `json:"t"`; V string `json:"v"` }
   type Interval  struct { Start int32 `json:"s"`; End int32 `json:"e"` }
   ```
   And `MatchEvent.Time int32` in `result/messages.go`,
   `TimelineFragEvent.Time int32`, `TimelinePowerupEvent.Time/EndTime
   int32` in the timeline result.

2. **Producers** (`analyzer/timeline_streams.go`,
   `analyzer/messages.go`, `analyzer/frag.go`, etc.) — switch the
   `recordX(t float64, ...)` signatures to `recordX(tMs int32, ...)`
   and plumb `e.TimeMs` from the events. The events package already
   has `TimeMs` on the position/spawn/death events; add it to
   `StatUpdateEvent`, `FragUpdateEvent`, `PrintEvent`, `DamageEvent`
   for full coverage.

3. **Postprocess** (`analyzer/postprocess.go`) — collapse
   `shiftAndFilterChangeI16` / `shiftAndFilterChangeStr` /
   `shiftAndFilterIntervals` into single int32-typed implementations.
   `shiftAndFilterInts` is already there from v8.

4. **View layer** (`view/buckets.go`, `view/streamslice.go`,
   `view/stateat.go`, `view/events.go`) — drop the seconds-conversion
   sites that exist for ChangeI16/Interval today. Internal idiom
   becomes uniformly int32 ms. `ViewBucket.T` either stays float64
   seconds (single conversion at emission, frontend untouched) or
   becomes int32 ms (frontend churn — see "Cons" above).

5. **mvd-api** (`mvd-api/handlers.go`, `mvd-api/handlers_test.go`) —
   any code path that filters or formats `MatchEvent.Time`
   parametrically needs the unit fix and the test-expectation update.

6. **Frontend** (`mvd-web/static/app.js`) — only required if
   `ViewBucket.T` migrates too. Otherwise the frontend stays oblivious
   (same scoping decision as v8).

7. **Goldens regen** — every JSON file in `testdata/golden/` rewrites
   most of its numeric fields. Diffs are mechanical but enormous.

8. **Schema bump** — `CurrentSchemaVersion = 9` with a v9 history
   block.

Estimated diff size: ~3-5× the v8 cut. The mechanical scope is well
understood; the cost is mostly test churn and the JSON contract
break.
