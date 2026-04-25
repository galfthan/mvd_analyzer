# match analyser

**Phase:** Derived
**Inputs:** `PrintEvent` (for match start/end via `MatchTimingDetector`), `IntermissionEvent`
**Reads from CoreOutputs:** `co.Slots` (display names)
**Writes to Result:** `result.Match` (`*MatchResult`), `result.Duration`

## What it does

Produces the top-level match summary: map, gamedir, start/end times,
duration, per-player stats, per-team aggregate frags. This is the
header consumers (web UI, CLI) read first.

## How it works

1. Match boundary detection is delegated to `MatchTimingDetector`
   (see [`matchtiming.go`](matchtiming.go)) — keyword matching is
   case-insensitive and shared across every analyser that needs it.
2. Match duration is computed as `EndTime - StartTime` when both
   exist; otherwise falls back to `lastEvent - StartTime`.
3. Map name comes from `ctx.ServerData.LevelName` (passed through
   `extractMapName` to strip the `.bsp` extension and trailing
   author hints like ` by …`).
4. Per-player stats: every non-spectator slot in `ctx.Players` with a
   non-empty resolved name (from `co.Slots`), non-spectator team, and
   non-zero frag count gets a `PlayerStat` row. The frag count comes
   from `ctx.FragsBySlot` (populated by the registry from
   `FragUpdateEvent`s) when available, else from the wire `Frags`
   field on `PlayerInfo`.
5. Per-team stats are sorted by team name for byte-stable output.

## Limitations / known issues

- Players who joined briefly (frags = 0) are filtered out. This is
  intentional — connect-then-disconnect spectators would otherwise
  appear as zero-frag players.
- The "valid team" filter (`isSpectatorTeam`) skips empty strings and
  common spectator markers, but a custom team name that happens to
  match a spectator pattern (e.g. team called "spec") is dropped.
- Match duration is always the *match* duration when boundaries are
  detected; pre-match warmup and post-match intermission are
  excluded. Demos without recognisable boundaries fall back to the
  full demo length.
