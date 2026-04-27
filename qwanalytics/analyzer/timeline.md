# timeline analyser

**Phase:** Derived
**Inputs:** `PrintEvent`, `IntermissionEvent`, `PlayerPositionEvent`,
            `StatUpdateEvent`, `FragUpdateEvent`, `DeathEvent`,
            `SpawnEvent`, `UserInfoEvent`
**Reads from CoreOutputs:** `co.DemoInfo`, `co.Names`, `co.FragEntries`
**Writes to Result:** `result.TimelineAnalysis` (`*TimelineAnalysisResult`)

## What it does

Reconstructs per-player time-bucketed state at 50 ms resolution: position,
health, armor, weapons in inventory, items held, current location label.
Aggregates that state into the timeline view's high-resolution stream,
plus higher-order analyses: spawn-to-death streaks, region control,
powerup events, frag events.

The analyser is split across several files:

| File | Role |
|---|---|
| [`timeline.go`](timeline.go) | OnEvent dispatch + per-bucket sampling |
| [`timeline_buckets.go`](timeline_buckets.go) | Convert internal buckets → result `HighResBucket` |
| [`timeline_blipfilter.go`](timeline_blipfilter.go) | In-place loc smoothing — see "Loc smoothing" below |
| [`timeline_powerups.go`](timeline_powerups.go) | Quad/Pent/Ring pickup → loss event detection |
| [`timeline_streaks.go`](timeline_streaks.go) | Top-N spawn-to-death frag streaks |
| [`timeline_regions.go`](timeline_regions.go) | Map region transit + dwell aggregates |
| [`timeline_finalize.go`](timeline_finalize.go) | Orchestrates the pipeline above |

## How it works

1. **Sampling**: every 50 ms a snapshot of each player's state is
   appended to a bucket. Position/health/armor come from
   `PlayerPositionEvent` and `StatUpdateEvent`s as they arrive.
2. **Loc resolution**: each bucket's location label comes from
   `loc.Finder.FindNearest(x, y, z)`. The loc finder is loaded from
   the demoinfo map name when available.
3. **Loc smoothing (blip filter)**: `applyBlipFilter` rewrites
   `pData.location` *in place* on every bucket, collapsing residences
   shorter than the threshold (default 250 ms) into adjacent stable
   residences. **Every downstream consumer (streaks, regions,
   locgraph) must read the smoothed track.** This is enforced
   structurally — the rewrite happens before any consumer reads the
   field.
4. **Match window**: `MatchTimingDetector` gates everything. Buckets
   sampled outside the match window (warmup, intermission) are
   excluded from output during finalize.
5. **Frag events**: `co.FragEntries` are joined onto buckets with the
   demoinfo-resolved player name + team.
6. **Streaks**: spawn-to-death runs are paired up, each scored by
   frag count during the run. Top 10 by frag count are emitted as
   `FragStreakEvent`.
7. **Regions**: per-loc dwell time and inter-loc transit counts are
   aggregated. Region definitions come from per-map config.
8. **Powerups**: per-slot quad/pent/ring presence transitions are
   converted to `PowerupEvent` records with start/end times.

## Limitations / known issues

- High-res buckets are **50 ms** by default. The frontend slices these
  into wider windows for graphs; the raw stream is preserved for the
  map tab playback.
- The blip filter is a heuristic — pathological maps with many
  near-equidistant locs (e.g. dm6's GA cluster) can still produce
  short residences that survive the threshold.
- Powerup detection only fires on transitions of the underlying
  `STAT_ITEMS` bits. A player who picks up the same powerup with
  zero gap (warm pent grab) emits as one continuous event, not two.
- `co.FragEntries` is required for streak/powerup-frag attribution;
  if the frag analyser is unregistered, those derived fields are
  empty (but the bucket stream still works).

## Reference

- 50 ms cadence is the QW server tick (KTX `pmove.c`).
- Region/loc heuristics: see `qwanalytics/loc/data/*.loc`. Auto-detect
  keywords live in `timeline_regions.go`; per-map region overrides ship
  as JSON in `qwanalytics/config/regions/<map>.json` (drop a new file
  in that directory, no Go code change needed). Maps with no RA loc
  fall back to YA in the auto-detector. The web UI's Region Control
  panel saves/loads this exact JSON shape.
