# backpacks analyser

**Phase:** Derived
**Inputs:** `BackpackDropHintEvent`, `PlayerPositionEvent`,
            `StuffTextEvent`, `PrintEvent`, `IntermissionEvent`
**Writes to Result:** `result.Backpacks` (`[]BackpackDrop`)

## What it does

Records every RL or LG drop emitted by KTX as a `//ktx drop` hint
hidden message. Each entry carries the dropper, weapon, drop time,
origin, and an entity-number key that joins with `WeaponPickup` for
end-to-end "who dropped this for whom" attribution.

Only **RL** and **LG** drops are tracked — these are the
weapons KTX explicitly hints. SG/SSG/NG/SNG/GL drops happen but are
not announced and are therefore invisible to this analyser.

## How it works

1. `BackpackDropHintEvent` fires when KTX emits the hidden message.
   Its `ItemFlags` bitfield encodes which weapon was dropped:
   `IT_ROCKET_LAUNCHER` → "rl", `IT_LIGHTNING` → "lg".
2. The dropper's most recent `PlayerPositionEvent.Origin` is captured
   as the drop origin (KTX spawns the backpack at the dying player's
   `s.v.origin`).
3. `MatchTimingDetector` gates the recording so warmup drops don't
   pollute the match output.
4. At Finalize, drops are sorted by time and `Loc` is resolved
   best-effort from the map's `.loc` corpus.

## Limitations / known issues

- **Auth-name capture (pre-existing bug)**: the dropper's name is
  read from `ctx.Players[slot].Name` at OnEvent time, which is the
  userinfo name. Every other analyser emits the demoinfo display
  name. For demos with auth-override players the join against
  demoinfo's `Name` field silently fails. See
  `NOTES-pickup-attribution-quality.md` §2 for the proposed fix
  (defer name resolution to Finalize, mirroring weapon_pickups).
- Both-bits-set or zero-bits `ItemFlags` values are dropped
  defensively. Stock KTX always sends exactly one flag bit; the
  defence guards against unknown future bit combinations.
- Drops by a player who disconnected before the recording started
  (no `UserInfoEvent` for the slot) are skipped.

## Reference

- KTX drop emitter: `ktx/src/items.c` (search "//ktx drop")
- Item bit layout: `ktx/include/g_local.h` (`IT_*` constants)
