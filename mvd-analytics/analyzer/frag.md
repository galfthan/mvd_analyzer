# frag analyser

**Phase:** Core
**Inputs:** `PrintEvent` (obituaries), `DeathEvent` (death count),
`IntermissionEvent` (match-end gate)
**Reads from CoreOutputs:** `co.Names` (post-Finalize teamkill recompute),
`co.SlotIdentityAt` (death → player resolution)
**Writes to Result:** `result.Frags` (`*FragResult`)
**Writes to CoreOutputs:** `co.FragEntries`

## What it does

Parses every kill/suicide/teamkill obituary print message into a
structured frag log. The log is the canonical input for downstream
analytics — timeline (streaks, powerup-frag counts) and weapon_pickups
(kill-window attribution) both read it via `co.FragEntries`.

Per-player **kills** come from the obituary log (the killer is always
named). Per-player **deaths** come from the authoritative protocol
`DeathEvent`, not the obituary — see step 5.

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
5. **Death counting** is sourced from `DeathEvent`, gated to the match
   window via an embedded `MatchTimingDetector` (start/end prints +
   `IntermissionEvent`). Each match-time death is resolved to a player
   in Finalize via `co.SlotIdentityAt(slot, tMs)` and increments that
   `PlayerFrags.Deaths`. This is deliberately *not* obituary-derived:
   KTX bumps `targ->deaths` for every death, but several teamkill
   obituaries name only the attacker (`"X mows down a teammate"`,
   `"X checks his glasses"`, …), so the victim is unattributable from
   the message. The protocol death signal fires for every death
   regardless of the print, and resolving by identity-at-death-time
   folds a reconnecting player's deaths across both their slots.
6. **Killer-named teamkill recovery** (`recoverTeamkills`). Obituaries
   like `"X loses another friend"` / `"X checks his glasses"` name only
   the attacker, so they're stashed during parse (`genericTeamkills`)
   rather than dropped. In Finalize each is counted against the killer
   (`PlayerFrags.TeamKills`, matching KTX `tk`) and its victim is
   recovered by pairing it with the `DeathEvent` it caused — a death at
   ~the same time (`teamkillMatchWindowMs`, observed Δ0) whose victim
   resolves to a teammate of the killer and isn't already explained by a
   named-victim frag. Recovered teamkills rejoin `Frags[]` as complete
   killer↔victim pairs, then the log is re-sorted by time. Deaths are
   untouched (already counted in step 5).

## Limitations / known issues

- Pattern coverage is exhaustive for stock KTX but custom obituary
  packs from non-KTX server mods will silently produce no frag
  entries (kills); deaths still count via `DeathEvent`.
- Generic teammate references ("teammate") are not resolved to a real
  player name — they appear in `Frags[]` with `Killer="teammate"` or
  `Victim="teammate"` and are excluded from per-player *kill*
  aggregation (see `isGenericPlayer`). Their **deaths** are still
  counted via the `DeathEvent` path above. Killer-named teamkills are
  recovered (step 6); *victim-named* ones (`"X was telefragged by his
  teammate"`) are **not** — the killer is unrecoverable from the
  obituary, so `TeamKills` undercounts where those occur and the victim's
  death stays out of `Frags[]`. Recovering them needs a third signal
  (telefrag co-location, or the teamkiller's −1 frag delta).
- The teamkill recompute path runs **after** demoinfo finalises (Frag
  is registered after DemoInfo in the core slice). If the demoinfo
  block is missing, live verdicts are kept as-is.

## Reference

- KTX obituary strings (authoritative for KTX demos):
  `ktx/src/client.c` (`ClientObituary`, `Instagib_Obituary`)
- Generic fuhquake fragfile table (fallback): `mvdsv/src/sv_mod_frags.h`
- Death types: `ktx/include/deathtype.h`
