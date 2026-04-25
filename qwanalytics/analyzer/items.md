# items analyser

**Phase:** Derived (CoreConsumer — reads `co.SlotName` for display names)
**Inputs:** `ItemSpawnEvent`, `ItemStateEvent`, `ItemPickupHintEvent`,
            `ItemPickupPrintEvent`, `StatUpdateEvent`,
            `DeathEvent`, `SpawnEvent`, `PrintEvent`, `StuffTextEvent`,
            `PlayerPositionEvent`, `IntermissionEvent`
**Writes to Result:** `result.Items` (`*ItemsResult`)

## What it does

Tracks the lifecycle of every world item across the match: armours
(GA/YA/RA), healths (H15/H25/MH), weapons, ammo boxes, and powerups
(Quad/Pent/Ring/Suit). Each item produces a phase timeline of
`available → taken → available → …` with respawn timestamps. Megahealth
is special-cased because its respawn timer doesn't start at pickup —
see "MH semantics" below.

Pickup attribution (who actually took the item) uses a layered signal
pipeline. Distance is the *last* layer, gated by a touch-plausible
radius; the higher layers are protocol-level signals that don't
require any geometric guesswork.

## Attribution layers

When `ItemStateEvent{Taken=true}` fires for entity `E` with kind `K`
at time `T`, the analyser walks four signal layers in priority order
and returns at the first hit:

1. **`ItemPickupHintEvent` (`//ktx took`)** — Buffered keyed by
   `entNum`. KTX emits this STUFFCMD_DEMOONLY directive on every
   competitive pickup (MH, armors, weapons, powerups). Authoritative
   for KTX demos; absent on non-KTX servers and for small healths /
   ammo boxes (KTX doesn't emit `//ktx took` for those).

2. **`ItemPickupPrintEvent`** — The per-client `svc_print` "You got
   the X" / "You receive N health" message that KTX sends to the
   picking player. Buffered keyed by slot + kind. Authoritative when
   present, but `mvdsv` filters PRINT_LOW prints by the picker's
   `messagelevel` cvar; competitive players widely use `msg 2`, so on
   typical 4on4 / duel demos this signal is partial or absent.
   Covers the same set as L1 plus h15/h25 + ammo boxes.

3. **Stat-delta evidence** — Computed by diffing each
   `StatUpdateEvent` against a per-slot snapshot. The classifier
   recognises:
   - IT_ARMOR1/2/3 bit 0→1 → ga / ya / ra
   - STAT_HEALTH +15 / +25 → h15 / h25
   - IT_SUPERHEALTH bit 0→1 → mh
   - IT_SUPERSHOTGUN/NAILGUN/etc bit 0→1 → corresponding weapon
   - STAT_SHELLS/NAILS/ROCKETS/CELLS positive delta → ammo kind
   - IT_QUAD/PENT/RING/SUIT bit 0→1 → corresponding powerup

   Universal — works on every demo regardless of client config — but
   stat updates arrive at ~3 Hz per player so the correlation window
   is generous (T-100ms .. T+500ms).

4. **Distance corroborator** — Last resort. Iterates slots whose
   last `PlayerPositionEvent` is within 250 ms of `T` and returns
   the closest within `256²` units squared of the item origin. If
   layer 3 produced multiple candidates with the same kind evidence
   (a real contest), the distance check is restricted to those
   candidates only — *not* opened back up to the whole player set.
   Refuses to attribute when no candidate is in radius.

A pickup with no signal in any layer gets `TakenBy=""` and an
internal `attributionSource="none"`. The diagnostic harness reports
an "unattributed" count per demo so coverage gaps are visible
without a forced guess polluting the output.

### Why distance is last

In QuakeWorld, when two players collide on a spawning item
near-simultaneously, the `findradius` / `touch` resolution order in
QuakeC is effectively random — it is *not* nearest-wins. So a
nearest-player heuristic would systematically mis-attribute contested
pickups, even when both players are equidistant within float
precision. The protocol-level signals (KTX hint, per-client print,
stat deltas) reflect what the server actually picked.

### Conflict resolution

When a hint and a stat delta point at different slots the hint wins
(default policy). The two-disagree case is rare and probably
indicative of a wire-level race; the diagnostic invariants count
unattributed pickups but don't currently track hint↔stat
disagreement.

## How the phase model works

1. `ItemSpawnEvent` registers an entity number → item kind mapping
   and the world position. The first phase opens at `AvailableFrom=0`.
2. `ItemStateEvent{Taken=true}` closes the current available phase
   with `TakenAt`, runs the layered attribution pipeline above to
   resolve the picker slot, and stamps `RespawnAt` from the
   `kindRespawnSec` table (armor: 20 s, weapons: 30 s, quad: 60 s,
   pent/ring: 300 s).
3. `ItemStateEvent{Taken=false}` opens a new phase at `AvailableFrom`.
4. **MH semantics**: the megahealth respawn timer starts when the
   holder's tracked health drops to ≤ 100 (rot tick-down or death),
   with a 5 s minimum-hold floor enforced by KTX's
   `item_megahealth_rot`. The analyser tracks each holder's health
   via `StatUpdateEvent` until the crossing, then stamps
   `RespawnAt = max(pickup+5, crossing) + 20`.

## Insta-regrab synthesis

When a player camps an item spawn, the engine can run "respawn → touch
→ remove" in a single server frame, leaving the wire's end-of-frame
delta showing no transition at all (see *insta-regrab invisibility* in
[`qwdemo/MVD_FORMAT.md`](../../qwdemo/MVD_FORMAT.md)). The entity-state
trigger never fires, so without recovery items.go would silently miss
those pickups.

The analyser closes that gap with two complementary synthesis paths:

**Hint-driven (preferred when available).** Every `//ktx took`
directive identifies the picker's slot directly. When a hint arrives
for an entity that's already in our "taken" phase (no wire respawn
observed since the last close), it can only be an insta-regrab —
synthesise the pickup immediately, using the slot from the hint as
authoritative attribution (`attributionSource = "hint"`). Covers
armors, MH, weapons, and powerups on KTX servers. For MH the
synthesis additionally transfers heldMHs ownership from the previous
holder to the new picker so the rot tracker stamps `RespawnAt` on the
right phase.

**Stat-delta-driven (fallback for non-hinted kinds).** For items KTX
doesn't hint (small healths, ammo boxes), and as a backup if a hint
is somehow missing:

1. After every `Taken=true(ent, T)` (real or synthetic), schedule a
   prediction at `T + respawnSec[kind]`.
2. Once the predicted moment plus a 0.5 s settle window has passed,
   look for a unique slot whose stat-delta evidence (a STAT_ARMOR
   jump for armor, IT_QUAD bit transition for quad, ammo tick-up,
   etc.) and historical position support a pickup at the predicted
   instant.
3. If found, record a synthetic phase
   (`AvailableFrom=predicted, TakenAt=predicted`) with the unique
   slot and `attributionSource = "synthetic"`; schedule the next
   prediction.

The stat-delta classifier accepts any positive `STAT_HEALTH` delta in
[1, 25] as h15-or-h25 evidence (resolved by entity kind at synthesis
time). KTX's `T_Heal` caps health at `max_health` (100), so a pickup
at 80 HP gives only a `+20` delta even though `tooks` increments —
exact matching on `+15` / `+25` would miss every partial-cap heal.
MH is detected via the IT_SUPERHEALTH bit transition, not the `+100`
delta, so the cap rule doesn't apply.

The chain-forward stat-delta path stays disabled for MH because its
predicted respawn depends on rot timing, which is already tracked
separately. Hint-driven synthesis applies to MH unchanged.

Both paths terminate cleanly: a wire `Taken=false` cancels any pending
schedule (the entity genuinely respawned without being re-grabbed),
and the chain has a hard cap of 60 entries per entity.

The qwanalytics pickup-invariant test (`pickup_invariant_test.go`)
compares per-player phase counts against KTX's authoritative
`demoInfo.players[*].items[*].took` numbers. With both synthesis
paths enabled the hub corpus has 8 of 9 demos at exact match across
every hinted kind; the 9th has one h15 pickup attributed to the
wrong player (a slot flip on ambiguous stat-delta evidence, net zero
in total count).

Synthesis can be disabled per analyser via `SetSyntheticPickups(false)`
when wire-only behaviour is needed for comparison.

## Display name resolution

Picker names are resolved during `Finalize` via `co.SlotName(slot)`
so the demoinfo-overridden display name lands in the output instead
of the eager userinfo name (mirrors WeaponPickupsAnalyzer's pattern).
Falls back to `ctx.Players[slot].Name` when the registry isn't seeded
with CoreOutputs (unit tests that wire the analyser bare).

## Reference

- MH rot logic: `ktx/src/items.c` (`item_megahealth_rot`)
- Item respawn times: `ktx/src/items.c` (`SP_item_*` defaults)
- Pickup-signal investigation: [`PICKUP-SIGNALS-INVESTIGATION.md`](../../PICKUP-SIGNALS-INVESTIGATION.md)
- KTX hint parsing: [`qwdemo/parser/ktx_pickup.go`](../../qwdemo/parser/ktx_pickup.go)
- KTX print parsing: [`qwdemo/parser/ktx_pickup_print.go`](../../qwdemo/parser/ktx_pickup_print.go)
