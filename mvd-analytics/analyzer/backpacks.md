# backpacks analyser

**Phase:** Derived
**Inputs:** `BackpackDropHintEvent`, `PlayerPositionEvent`,
            `StuffTextEvent`, `PrintEvent`, `IntermissionEvent`
**Writes to Result:** `result.Backpacks` (`[]BackpackDrop`)

Paired with the `backpack-recon` post-processor
(`backpack_recon.go`), which fills the SAME section from
`DropBackpack`'s own rule on demos older than the hint. Its
validation numbers and stand-down conditions are in
[BACKPACKS.md](BACKPACKS.md).

## What it does

Records every RL or LG drop emitted by KTX as a `//ktx drop` hint
hidden message. Each entry carries the dropper, weapon, drop time,
origin, an entity-number key that joins with `WeaponPickup` for
end-to-end "who dropped this for whom" attribution, and
`source: "ktx"` naming the provenance.

Only **RL** and **LG** drops are tracked — these are the
weapons KTX explicitly hints. SG/SSG/NG/SNG/GL drops happen but are
not announced and are therefore invisible to this analyser.

## How it works

1. `BackpackDropHintEvent` fires when KTX emits the hidden message.
   Its `ItemFlags` bitfield encodes which weapon was dropped:
   `IT_ROCKET_LAUNCHER` → "rl", `IT_LIGHTNING` → "lg".
2. The dropper's most recent `PlayerPositionEvent.Origin` is captured
   as the drop origin (KTX spawns the backpack at the dying player's
   `s.v.origin`). Only the SLOT is recorded at event time; the name
   and team are resolved at Finalize through the shared
   `ResolveSlotAt` chain, so a drop carries the same display name
   every other section joins on (this also fixes the former
   auth-name capture bug, where the raw userinfo name was stamped and
   joined against nothing).
3. `MatchTimingDetector` gates the recording so warmup drops don't
   pollute the match output.
4. At Finalize, drops are sorted by time and `Loc` is resolved
   best-effort from the map's `.loc` corpus.

## Limitations / known issues

- Both-bits-set or zero-bits `ItemFlags` values are dropped
  defensively. Stock KTX always sends exactly one flag bit; the
  defence guards against unknown future bit combinations.
- Drops by a player who disconnected before the recording started
  (no `UserInfoEvent` for the slot) are skipped.

## Reference

- KTX drop emitter: `ktx/src/items.c` (search "//ktx drop")
- Item bit layout: `ktx/include/g_local.h` (`IT_*` constants)
