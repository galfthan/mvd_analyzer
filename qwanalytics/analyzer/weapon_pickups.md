# weapon_pickups analyser

**Phase:** Derived
**Inputs:** `ItemSpawnEvent`, `ItemPickupHintEvent`,
            `BackpackPickupHintEvent`, `BackpackDropHintEvent`,
            `StatUpdateEvent` (`STAT_ITEMS`), `DeathEvent`,
            `PrintEvent`, `IntermissionEvent`
**Reads from CoreOutputs:** `co.FragEntries`, `co.Slots` (display names)
**Writes to Result:** `result.WeaponPickups` (`[]WeaponPickup`)

## What it does

Records every weapon pickup (world spawner or backpack) along with
how many kills the picker made with that weapon before their next
death. The output is the basis for "weapons matter" analytics: which
RL pickups produced kills, who looted whom, kill-windowing per pickup.

## How it works

1. `ItemSpawnEvent` indexes entity-number → item kind so
   `ItemPickupHintEvent` (entity-keyed) can be classified later.
2. KTX hidden hints are processed pairwise:
   - `ItemPickupHintEvent` → world-spawner pickup record.
   - `BackpackPickupHintEvent` → backpack pickup, joined back to the
     drop record from `BackpackDropHintEvent`.
3. Each pickup is recorded with its slot, time, weapon, source
   ("world" / "backpack"), and a `HadBefore` flag computed from
   the pre-pickup `STAT_ITEMS` snapshot. `HadBefore=true` means the
   player already had this weapon — the pickup didn't grant anything,
   so it is excluded from kill credit.
4. `DeathEvent`s are recorded per slot for next-death lookup.
5. At Finalize, kill windows are built per `(player, weapon)` key. A
   frag from `co.FragEntries` is attributed to the most recent
   covering window (start < frag.Time ≤ end). Suicides and teamkills
   are excluded.
6. Player names are resolved at Finalize via `a.playerName(slot)` —
   prefers `co.SlotName(slot)` (demoinfo-resolved), falls back to
   `ctx.Players[slot].Name`. This is the reference pattern for
   eagerly-captured-at-OnEvent vs resolved-at-Finalize names.

## Limitations / known issues

- Only weapons KTX hints are tracked. SG (starting weapon) and items
  without `ItemPickupHintEvent` emission are absent.
- A pickup that grants the weapon but is followed by the player
  immediately discarding it (impossible in stock QW but possible with
  some mods) would still be credited with kills made before the
  next death.
- Match-window filtering is lockstep with the rest of the analysers
  via `MatchTimingDetector`; warmup pickups are dropped.

## Reference

- KTX pickup hints: `ktx/src/items.c` (search "//ktx pickup")
- `STAT_ITEMS` bit layout: `qwdemo/mvd/types.go` (`StatItems` constants)
