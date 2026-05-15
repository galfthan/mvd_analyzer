# frag analyser

**Phase:** Core
**Inputs:** `PrintEvent` (only)
**Reads from CoreOutputs:** `co.Names` (post-Finalize teamkill recompute)
**Writes to Result:** `result.Frags` (`*FragResult`)
**Writes to CoreOutputs:** `co.FragEntries`

## What it does

Parses every kill/suicide/teamkill obituary print message into a
structured frag log. The log is the canonical input for downstream
analytics — timeline (streaks, powerup-frag counts) and weapon_pickups
(kill-window attribution) both read it via `co.FragEntries`.

## How it works

1. Each `PrintEvent` runs through `parseObituary` (see
   [`obituary.go`](obituary.go) for the suffix tables). The parser
   recognises ~140 KTX patterns covering each weapon, suicide and
   teamkill variants, environmental (lava, water, fall, telefrag),
   and special cases (rockets-from-N pattern).
2. Successful parses produce a `FragEntry{Time, Killer, Victim,
   Weapon, IsSuicide, IsTeamKill}`. Killer/victim names are the raw
   server-printed names (display names, not auth names).
3. Live teamkill detection during OnEvent uses `ctx.Players[slot].Team`
   — this can miss when the userinfo name on the wire differs from the
   displayed netname (KTX auth-override case).
4. Finalize re-evaluates teamkill status using `co.Names` (built from
   demoinfo). If the live verdict was wrong, the kill counter on the
   relevant `PlayerFrags` is corrected.

## Limitations / known issues

- Pattern coverage is exhaustive for stock KTX but custom obituary
  packs from non-KTX server mods will silently produce no frag
  entries.
- Generic teammate references ("teammate") are not resolved to a real
  player name — they appear in `Frags[]` with `Killer="teammate"` or
  `Victim="teammate"` and are excluded from per-player aggregation
  (see `isGenericPlayer`).
- The teamkill recompute path runs **after** demoinfo finalises (Frag
  is registered after DemoInfo in the core slice). If the demoinfo
  block is missing, live verdicts are kept as-is.

## Reference

- KTX obituary table: `ktx/src/sv_mod_frags.h`, `ktx/src/client.c`
- Death types: `ktx/include/deathtype.h`
