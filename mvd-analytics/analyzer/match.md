# match analyser

**Phase:** Derived
**Inputs:** `PrintEvent` (match start/end via `MatchTimingDetector`, plus the
departure broadcast), `IntermissionEvent`, `UserInfoEvent`,
`FragUpdateEvent`, `SpawnEvent`, `DeathEvent`, `PlayerPositionEvent`
**Reads from CoreOutputs:** `co.Sessions` (identity-resolved display names +
identity keys), `co.Slots` (fallback for a slot that only ever had one
occupant)
**Writes to Result:** `result.Match` (`*MatchResult`), `result.Duration`

## What it does

Produces the top-level match summary: map, gamedir, duration, the
per-player scoreboard and per-team aggregate frags. This is the header
consumers (web UI, CLI) read first.

## How it works

1. Match boundary detection is delegated to `MatchTimingDetector`
   (see [`matchtiming.go`](matchtiming.go)) — keyword matching is
   case-insensitive and shared across every analyser that needs it.
2. Match duration is computed as `EndTime - StartTime` when both
   exist; otherwise falls back to `lastEvent - StartTime`.
3. Map name comes from `ctx.ServerData.LevelName` (passed through
   `extractMapName` to strip the `.bsp` extension and trailing
   author hints like ` by …`).
4. **The scoreboard is built per slot *occupancy*, not per slot.** A wire
   slot is recycled — a client leaves, the next connection lands on the
   same index — so the analyser splits each slot with the shared
   [`occupancyTracker`](occupancy.go) and scores each occupancy
   separately. Occupancies that resolve to the same identity (a player who
   reconnected onto another slot) collapse into one row carrying the
   latest stint's score, which is the total the server restored and
   re-asserted.
5. **Participation is evidence of play inside the match window**: a spawn,
   a death or a position sample — things only a client that entered the
   game world produces — or a frag value that actually changed. A
   connected-but-not-entered client (a spectator, or a connection the
   server refused because the match was locked) has none of them. An
   occupancy whose every userinfo carried `*spectator` is excluded outright.
6. **A departing player keeps their score.** `SV_DropClient` zeroes the
   slot and broadcasts the cleared state in the same server frame
   (`mvdsv/src/sv_main.c:419-428`, `:487-513`), so that reset is rolled
   back; where the mod announced the departure (`"<name> left the game
   with N frags"`, `ktx/src/client.c:2843`) that count wins, because it is
   the server's own accounting rather than our reconstruction.
7. Per-team stats are sorted by team name for byte-stable output.

## Limitations / known issues

- The team list still filters `isSpectatorTeam` (empty string, `spec`,
  `observer`, …), so a real team literally called "spec" gets no team row.
  Participation no longer consults it — in FFA nobody has a team, and
  gating on that deleted whole rosters.
- A frag value at or below `spectatorFragSentinel` (-900) is treated as a
  mod's spectator marker rather than a score: pre-KTX mods publish
  spectators with -999 so clients sort them below every player.
- Match duration is always the *match* duration when boundaries are
  detected; pre-match warmup and post-match intermission are excluded.
  Demos without recognisable boundaries fall back to the full demo length
  — and, with no window to test against, participation there accepts
  evidence from anywhere in the demo.
- A slot handed over without the wire ever changing its userid (userid 0
  throughout, and no drop broadcast) cannot be split; both occupants merge
  into one row.
