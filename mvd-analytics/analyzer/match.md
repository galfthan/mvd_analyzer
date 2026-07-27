# match analyser

**Phase:** Derived
**Inputs:** `PrintEvent` (match start/end via `MatchTimingDetector`),
`PlayerDepartureEvent` (the mod's departure broadcast, decoded in the
parser), `IntermissionEvent`, `UserInfoEvent`, `FragUpdateEvent`,
`SpawnEvent`, `DeathEvent`, `PlayerPositionEvent`
**Reads from CoreOutputs:** `co.Sessions` (identity-resolved display names +
identity keys), `co.Slots` (fallback for a slot that only ever had one
occupant)
**Writes to Result:** `result.Match` (`*MatchResult`)

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
3. `map` is the canonical SHORT map name, resolved through
   `CoreOutputs.EffectiveMap()` (demoinfo map, else the serverinfo
   `map` key) — hence the node's `metadata` edge. `mapTitle` is the
   display-only level title from `ctx.ServerData.LevelName`, passed
   through `cleanLevelTitle` to strip the `.bsp` extension and
   trailing author hints like ` by …`. The title backs `map` only
   when neither shortname source named a map.
4. **The scoreboard is built per slot *occupancy*, not per slot.** A wire
   slot is recycled — a client leaves, the next connection lands on the
   same index — so the analyser splits each slot with the shared
   [`occupancyTracker`](occupancy.go) and scores each occupancy
   separately. Occupancies that resolve to the same identity (a player who
   reconnected onto another slot) collapse into one row carrying the
   latest stint's score, which is the total the server restored and
   re-asserted (`ktx/src/client.c:1464-1490`). Two occupancies that were
   *live at the same instant* never collapse, however good the identity
   key looks: without demoinfo, `*auth` or a KTX reconnect print the key
   degrades to the normalized netname, and two people called `Player` and
   `player!` would otherwise become one row and take a team off the table
   with them. The veto needs both sides to carry a userid of their own —
   KTX's ghost scoreboard entry (userid 0, netname prefixed `#`) is a copy
   of a departing player, not a second human.
5. **Participation is evidence of play inside the match window**: a spawn,
   a death or a position sample — things only a client that entered the
   game world produces — or a frag value that actually changed. A
   connected-but-not-entered client (a spectator, or a connection the
   server refused because the match was locked) has none of them. An
   occupancy whose every userinfo carried `*spectator` is excluded outright.
6. **A departing player keeps their score.** `SV_DropClient` zeroes the
   slot and broadcasts the cleared state in the same server frame
   (`mvdsv/src/sv_main.c:419-428`, `:509-511`), so that reset is rolled
   back; where the mod announced the departure (`"<name> left the game
   with N frags"`, `ktx/src/client.c:2843`) that count wins, because it is
   the server's own accounting rather than our reconstruction. The
   broadcast only names a netname, so for an occupancy the server actually
   dropped it must land in the same frame as the drop — otherwise one
   `bob` inherits another `bob`'s announced score. An occupancy closed by a
   takeover instead (a new userid with no drop broadcast, i.e. a demo that
   missed the drop packet) has no such anchor and keeps the whole occupancy
   as its search window. Post-match broadcasts are ignored, like post-match
   frag updates: KTX guards its own print on `match_in_progress == 2`
   (`ktx/src/client.c:2841`) but the pre-KTX mods do not.
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
- **The participation test can delete a genuine 0-frag player.** It wants a
  spawn, a death, a position sample, or a frag value that *changed*. An
  explicit `svc_updatefrags <slot> 0` is not a change, so a player who
  finished on 0 and produced no entity state at all has no evidence and is
  dropped. No such player exists in the 54 local demos, but the residual
  risk is real: the alternative (accepting a bare userinfo) is what used to
  put refused connections on the scoreboard.
- **The departure rollback reports 1 instead of 0 for a same-frame
  suicide.** The rollback takes the frag value from before the drop's reset,
  which is the right rule for a drop that lands mid-frame — but a player who
  suicides 1 → 0 in the *same* frame as his drop has two changes at that
  timestamp, and only the last is rolled back. He is reported on 1. The mod's
  departure broadcast, where present, overrides this with the correct value.

  A sanity rule of the shape "reject a broadcast that states *fewer* frags
  than the rolled-back cursor" was considered and **declined**, and this
  limitation is the strongest reason why: that is exactly the case where the
  broadcast is right and the cursor is wrong. The rollback returns the
  pre-suicide value (1); the mod prints the post-suicide one (0) straight
  off the edict. The rule would veto the correct answer in the one situation
  where the two disagree for a knowable reason. `announcedFrags` therefore
  takes the broadcast unconditionally within its window.
- **FFA emits a one-team table.** Team rollup keys on a non-empty team name,
  and in FFA most players have none, so `ffa_5[dm4]` reports 5 players and 29
  frags but `teams: [{"sdf", 4}]` — the one player who happened to have a
  team set. The table is not wrong, it is just not a scoreboard; a consumer
  should compare `len(teams)` against the number of participants before
  presenting it as one.
