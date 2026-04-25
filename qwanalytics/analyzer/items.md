# items analyser

**Phase:** Derived
**Inputs:** `ItemSpawnEvent`, `ItemStateEvent`, `StatUpdateEvent`,
            `DeathEvent`, `PrintEvent`, `StuffTextEvent`,
            `PlayerPositionEvent`, `IntermissionEvent`
**Writes to Result:** `result.Items` (`*ItemsResult`)

## What it does

Tracks the lifecycle of every world item across the match: armours
(GA/YA/RA), megahealth, and powerups (Quad/Pent/Ring). Each item
produces a phase timeline of `available → taken → available → …`
with respawn timestamps. Megahealth is special-cased because its
respawn timer doesn't start at pickup — see "MH semantics" below.

## How it works

1. `ItemSpawnEvent` registers an entity number → item kind mapping
   and the world position.
2. `ItemStateEvent{Taken:true}` closes the current available phase
   and runs `attributePickup` to find the nearest player by squared
   distance — slot iteration is sorted by index so float-precision
   ties resolve deterministically.
3. For most items, `RespawnAt = TakenAt + kindRespawnSec[kind]`
   (armor: 20 s, quad: 60 s, …). The wire-respawn signal from
   `ItemStateEvent{Taken:false}` is intentionally ignored — KTX
   sometimes prints late respawn signals for insta-regrabs.
4. **MH semantics**: the megahealth respawn timer starts when the
   holder's tracked health drops to ≤ 100 (rot tick-down or death),
   with a 5 s minimum-hold floor enforced by KTX's
   `item_megahealth_rot`. The analyser tracks each holder's health
   via `StatUpdateEvent` until the crossing, then stamps
   `RespawnAt = crossingTime + 20`.

## Limitations / known issues

These are pre-existing data-quality issues captured for a separate
investigation in `NOTES-pickup-attribution-quality.md`:

- **Stale positions**: `attributePickup` reads each player's
  last-known position from `playerPos` (last `PlayerPositionEvent`).
  Position events arrive at variable cadence; a player whose update
  hasn't landed yet appears at their last-known location.
- **No max-distance gate**: the analyser returns the absolute
  nearest slot, not the nearest within KTX's pickup radius. Real
  matches always have multiple live players; this rarely bites.
- **Float-precision ties**: when two players are within
  float-precision of equal squared distance, the deterministic
  tie-break picks the lowest slot index — but the *correct* answer
  is unknowable without auth data. The geometric root cause (why
  ties happen at all) is open.
- **Userinfo names eagerly captured**: `attributePickup` runs
  during OnEvent (before any Finalize), so it reads
  `ctx.Players[slot].Name` which is the userinfo name, not the
  demoinfo display name. For the test corpus userinfo == display, so
  this is invisible; a demo with an auth-override player would emit
  the wrong `TakenBy`. Same shape as the backpacks issue.

## Reference

- MH rot logic: `ktx/src/items.c` (`item_megahealth_rot`)
- Item respawn times: `ktx/src/items.c` (`SP_item_*` defaults)
