# metadata analyser

**Phase:** Derived
**Inputs:** `StuffTextEvent`, `ServerInfoEvent`, `CenterPrintEvent`, `PrintEvent`, `FinalScoresEvent`
**Writes to Result:** `result.Metadata` (`*MetadataResult`)
**Publishes on CoreOutputs:** `ServerInfoMap`, `FinalScores`

## What it does

Captures the server's cvar table and KTX countdown centerprint so the
result can document the match settings (mode, ruleset, fraglimit,
timelimit, antilag, etc.) without the frontend re-parsing prints.

## How it works

1. The bulk cvar dump is the very first stufftext: `fullserverinfo "…"`.
   `parseFullserverinfo` splits it on backslashes into key/value pairs.
2. Mid-game `ServerInfoEvent`s are last-write-wins applied to the same
   cvar map.
3. `CenterPrintEvent`s containing "Countdown:" are captured as the raw
   block. Only the latest one before `match has begun` is kept — KTX
   prints countdown updates every second; we want the final pre-match
   sample because it has all the resolved settings.
4. A `MatchTimingDetector` consumes `PrintEvent`s solely so the
   countdown-capture latch closes when the match starts.
5. `parseCountdownCenterprint` walks the post-`Q_normalizetext`
   countdown table and pulls each known KTX setting row into a
   structured `MatchSettings`.
6. `FinalScoresEvent` — KTX's `//finalscores` end-of-match stuffcmd —
   is stored verbatim as `MetadataResult.FinalScores` and published on
   `CoreOutputs` for the `match` node, which uses it for the map, mode
   and team rows it has no source of its own for (never displacing a
   demoinfo value; see `analyzer/match.md`). Last write wins, like the
   serverinfo keys. It lands here rather than in `match` because it is a
   metadata record first: it is reported on every demo that carries one,
   whether or not anything consumes it.

## Limitations / known issues

- If the demo recording starts mid-match (no `fullserverinfo` packet),
  `result.Metadata.ServerInfo` is empty.
- Countdown parsing depends on KTX's exact row format. Servers with
  custom centerprint layouts may produce empty `MatchSettings` even
  with a present `CountdownText`.

## Reference

- KTX countdown source: `ktx/src/match.c` (`PrintCountdown`)
- `//finalscores` emitter: `ktx/src/commands.c:6963-6977` (`lastscore_add`);
  wire form documented in `mvd-reader/MVD_FORMAT.md#svc_stufftext-9`
- Server cvars: `mvdsv/src/sv_main.c` (`SV_FullServerinfo_f`)
