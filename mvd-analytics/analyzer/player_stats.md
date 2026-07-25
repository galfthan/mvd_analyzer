# player-stats post-processor

**Phase:** Post-processor (non-event)
**Inputs (artifacts):** `clock`, `roster`, `timeline` (for `Streams`),
            `match:final`, `frags:final`, `damage`, `items`,
            `weapon-pickups`, `backpacks`, `metadata`
**Writes to Result:** `result.PlayerStats` (`*PlayerStatsResult`),
            schema v60

## What it does

Produces the canonical per-player (and per-team) statistics row, for
**every** demo — no KTX demoinfo block required.

Two things motivate it. First, the "how did this player do" answer was
scattered: the corrected scoreboard in `match`, team-kills in `frags`,
damage in `damage`, pickups across `items` / `weaponPickups` /
`backpacks`, and the KTX-only fields in `demoInfo` — so every consumer
re-implemented the join, differently. Second, none of them carry
**possession time**, and neither does KTX's block.

The node produces the fully **derived** form. `view.PlayerStats`
overlays KTX at read time where KTX is the better source, mirroring how
`view.Damage` applies `boundedSource` — so the stored artifact and the
golden corpus always say what this pipeline actually computed.

## How it works

1. **Windows.** `presenceWindow` reads the player's first/last activity
   (position track, else spawn/death markers, else the whole match).
   `aliveIntervals` converts the spawn/death markers into alive
   intervals using the repo's canonical liveness rule — the interval
   form of `losAliveAt` (`los.go`), which a unit test pins pointwise.
   Alive is then intersected with presence, which is what stops a late
   joiner being credited alive time from match start (the liveness rule
   says "no death yet ⇒ alive since match start", right for a player
   who was there, wrong for one who had not connected).
2. **Hold.** Each possession stream is merged, clipped to
   `[0, matchEnd]`, and intersected with the alive intervals; `ms`,
   `runs` and `longestMs` fall out of the resulting interval list.
   The armor `ArmorType` change stream is first run-length-converted
   into per-type intervals (`armorRuns`); `armor.none` is the
   alive-time complement.
3. **Score / damage.** Read from `match:final` (frag-log-corrected
   kills/deaths/suicides), `frags:final` (team-kills) and the damage
   reconstruction's **bounded** family — bounded is KTX-scoreboard
   semantics, which is what the KTX overlay replaces it with.
4. **Pickups.** Non-weapon kinds from the item timeline; weapons from
   `weaponPickups` (a weapon can also arrive in a backpack, which the
   item timeline never sees); `dropped` from `backpacks`; transfers
   joined from backpack-sourced pickups.
5. **Teams.** Summed per team, with hold shares recomputed over team
   time — never averaged from per-player shares.

## Why our hold times differ from KTX's

KTX tracks weapon hold time internally (`ps.wpn[].time`) but
`json_weap_detail` never writes it into the demoinfo block
(`ktx/src/stats_json.c:126-205`); it reaches only the end-of-match text
tables (`ktx/src/statsTables.c:390`). **No demo of any age carries it.**

Armor hold time *is* in the block and overcounts: the clock opens at
pickup and closes only on death or on picking up a different armor type
(`ktx/src/items.c:505-522`, `ktx/src/client.c:4600`) — never when the
armor is chewed to zero by damage. Ours closes when the item bits clear,
so it reads lower. Measured on gameId 212423 (1on1, dm2): KTX `ra`
213 s vs ours 129 s, and 317 s vs 266 s for the two players.

## Transfers (`xfer` / `xferSelf`)

KTX's `xferRL`/`xferLG` (`ktx/src/items.c:2586-2615`) credit the
**dropper** when a pack containing exactly the RL (or exactly the LG) is
taken by someone on the dropper's team, in teamplay only (`isTeam()`).

- A pack holds the weapon the player was **wielding**
  (`DropBackpack`: `item->s.v.items = self->s.v.weapon`), one bit — so
  KTX's exact-equality test has no mixed-contents case to model.
- KTX has no `other != dropper` check, so re-taking your own pack counts.
  We split that into `xferSelf`; `xfer + xferSelf` reproduces KTX
  exactly (a unit test pins the decomposition).
- The counters are **pointers**: absent means the demo carries no
  backpack hints, i.e. transfers are unobservable — a different fact
  from an observed zero.

## Limitations / known issues

- **Weapon-stay modes** (`deathmatch` 2/3/5, coop) give everyone every
  weapon from spawn, so weapon hold time reads ~100% for all players.
  That is the truth about the mode; it is not suppressed.
- **Presence** is inferred from stream activity, not from a connect /
  disconnect record. A player who idles out of the position stream
  without disconnecting will show a shorter `presentMs` than they were
  actually connected for.
- **`accuracy` is KTX-only** and omitted on demos without a demoinfo
  block, deliberately — see `RESULT_SCHEMA.md` for why there is no
  derived fallback.
- **`takenEnemy` / `takenToDie`** are KTX-only: the reconstruction
  cannot split taken damage by source.

## Reference

- Schema + field docs: `result/player_stats.go`,
  [`RESULT_SCHEMA.md`](../RESULT_SCHEMA.md#playerstatsresult-playerstats)
- KTX ground truth: `ktx/src/stats_json.c`, `ktx/src/items.c`,
  `ktx/src/client.c`, `ktx/src/statsTables.c`
