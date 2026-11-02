# Backpack reconstruction accuracy — validated against KTX ground truth

`analyzer/backpack_recon.go` fills the `backpacks` section on demos whose
mod never emitted the `//ktx drop` hint — 50.8% of the 51k archive, and
83.9% of the reconstructed-damage era. Rows it produces are stamped
`source: "reconstructed"`; hint-derived rows keep `source: "ktx"`, and a
demo that carried hints is never touched. (The one pickup-side field a hint
row does carry is `fate: "expired"`, and it comes from a hint of its own —
`//ktx expire`, below — not from any inference.)

Accuracy is measured on the OTHER half of the archive: demos that DO carry
`//ktx drop`. The reconstruction runs blind — it never reads
`res.Backpacks` — and is scored against the hints the pipeline itself
would have published.

Reproduce with:

```
# DROP side: precision / recall against the //ktx drop hints
go run ./mvd-analytics/cmd/qw-backpack-eval \
    -dir /mnt/HC_Volume_106625439/data/mvd \
    -list gt-sample.txt \
    -csv /mnt/HC_Volume_106625439/data/readability-51k.csv -jobs 10

# PICKUP side: the same experiment one layer up, against the //ktx bp and
# //ktx expire hints
go run ./mvd-analytics/cmd/qw-backpack-eval -linkage \
    -dir /mnt/HC_Volume_106625439/data/mvd \
    -list gt-sample.txt \
    -csv /mnt/HC_Volume_106625439/data/readability-51k.csv -jobs 10

# volume sanity on the hint-LESS population (no ground truth exists there)
go run ./mvd-analytics/cmd/qw-backpack-eval -volume \
    -dir /mnt/HC_Volume_106625439/data/mvd \
    -list nohint-sample.txt \
    -csv /mnt/HC_Volume_106625439/data/readability-51k.csv -jobs 10
```

`-list` is a file of demo basenames. The samples used below were drawn
from the archive readability census (`readability-51k.csv`): up to 30
demos per `ktxver` bucket among rows with `ktxdrop=1` for the ground-truth
run, and up to 20 per `version` bucket among rows with `ktxdrop=0` for the
volume run.

**What the harness refuses to score.** Both modes discard demos that
cannot be evidence about the reconstruction, rather than folding them in:

- Ground-truth mode requires every row of the demo's `backpacks` section
  to carry `source: "ktx"`. A list entry that turned out not to be
  hint-carrying has a section the reconstruction filled itself, and
  scoring it would compare the pass against its own output — a free
  100/100. Such demos are skipped as `backpacks are reconstructed, not
  ground truth`.
- Volume mode excludes hint-carrying demos entirely (they are reported as
  `excluded`, not merely flagged). Their drops used to reach the published
  drops-per-death rate and the `STAT_ITEMS` cross-check even though both
  are statements about reconstructed rows.
- Linkage mode scores the `expired` class against `//ktx expire` — the
  hint path stamps it on the ground-truth row as `fate`, and the
  reconstruction and the linkage never see it, exactly as with the other
  two. It is NOT a second gate: a bp-carrying demo with no expiry in it
  simply contributes no expiry evidence, which is the normal case (121 of
  the 223 scored demos).
- Linkage mode additionally requires the demo to emit `//ktx bp` at all:
  it skips a demo whose `weaponPickups` carries no `source: "backpack"`
  row. Measured on the probe sample, **10 of 24** demos that emit
  `//ktx drop` emit no `//ktx bp`, and on those every real pickup would
  have scored as a false positive. The pickup hint is not implied by the
  drop hint and the population has to be gated on it directly. Linkage
  mode also scores only drops the RECONSTRUCTION reproduced and matched to
  a hint: a missed drop's linkage is not evidence about the linkage, and
  the drop-side table above already counts that miss.

**Why the zero-`bp` skip is not an expiry bias.** The obvious objection to
that last gate is that it also discards legitimate ALL-EXPIRED demos on
bp-capable servers, which would depress `expired` recall. It does not, and
the wire says so outright — in KTX's THIRD backpack directive,
`//ktx expire <ent>`, which `SUB_Remove` writes for every RL/LG pack it
removes untaken (`ktx/src/g_spawn.c:196-210`). Counting all three over the
330-demo archive sample:

| | demos | `//ktx drop` | `//ktx bp` | `//ktx expire` |
|---|---|---|---|---|
| demos with no `bp` | 107 | 3 844 | 0 | 44 |
| demos with `bp` | 223 | 10 384 | 10 006 | 190 |

A demo carrying 117 drop hints and **one** expire hint did not have 117
packs expire; its packs were taken and the server never announced it. The
zero-`bp` population accounts for 3 844 drops with 44 expiries — 1.1% — so
skipping it removes almost no expiry evidence, while folding it in would
score 3 800 real pickups as false positives. Nor can the split be predicted
from `ktxver`: 1.38, 1.39, 1.40, 1.42 and 1.43 all appear on both sides, so
a version table cannot separate capability from absence either.

`//ktx expire` is now decoded (`mvd-reader/parser/ktx_expire.go`,
`BackpackExpireHintEvent`) and is the ground truth the `expired` class is
scored against — see [The expiry side is wire ground
truth](#the-expiry-side-is-wire-ground-truth-ktx-expire) below.
- `BackpackReconStandDown` evaluates the mode gates BEFORE the
  hinting-era gate. Ground-truth mode discounts exactly one reason — the
  one that only exists because the hint is present — and with the era
  check first, a hint-era demo that was ALSO yawnmode or fairpacks
  reported that discounted reason, so the mode gates were bypassed in
  scoring and never exercised against ground truth at all. The order is
  invisible to the pipeline, which only reads whether the reason is empty.

## What is being reproduced

`DropBackpack` (`ktx/src/items.c:2667-2885`) runs from `PlayerDie`
(`ktx/src/player.c:1179`) on EVERY death. With the shipped default
`k_frp 0` it puts the victim's **currently wielded** weapon in the pack
verbatim:

```c
item->s.v.items = self->s.v.weapon;                                  // items.c:2706
...
if ((item->s.v.items == IT_ROCKET_LAUNCHER) || (item->s.v.items == IT_LIGHTNING))
        stuffcmd_flags(self, STUFFCMD_DEMOONLY, "//ktx drop %d %d %d\n", ...);  // items.c:2762-2766
```

mvdsv writes that same `ent->v->weapon` into the MVD for every spawned
player as `STAT_ACTIVEWEAPON` (`mvdsv/src/sv_send.c:1268`), published as
`streams.players[].aw`. So "did a pack drop, and of what" is a **replay**
of a rule over a recorded field, not an inference — which is what the
numbers below reflect.

The early returns, and what the pass does about each:

| `DropBackpack` early return | Handling |
|---|---|
| `k_bloodfest` (items.c:2674) | stood down (serverinfo `mode` `-bf` / `k_bloodfest`) |
| `match_in_progress != 2` (items.c:2681) | the death list is already match-window-gated |
| `!cvar("dp")` (items.c:2681) | **no wire signal** — see Residuals |
| `dtSUICIDE == deathtype` (items.c:2688) | suppressed on the `" suicides"` obituary. `dtSUICIDE` is ONLY the `/kill` command (`ktx/src/client.c:1008`, and the bloodfest late-join kill at `client.c:2378`); rocket suicides, falls, drowning and lava all DO drop |
| no ammo AND no droppable weapon (items.c:2694) | unreachable: RL and LG are both in `IT_DROPPABLE_WEAPONS` |
| `k_frp 1` / `2` fairpacks rewrite (items.c:2710-2750) | stood down on the `Fairpacks setting:` broadcast (`match.c:2086-2107`) |
| `k_yawnmode` overrides (items.c:2686, 2754) | stood down (mode `-yw` / countdown `Yawnmode`) |

Two refusals are narrower than the whole demo and cost only the rows they
cover:

| refusal | why |
|---|---|
| a DEATH whose newest `aw` sample was carried on a DIFFERENT wire slot (players with >1 published session only) | mvdsv delta-codes stats against a per-SLOT cache no client change resets (`sv_send.c:1279-1281`), so a player who reconnects onto a slot whose previous occupant held what they now hold gets no update of their own — and the merged stream would answer with a weapon they held on the slot they left. A time bound cannot separate those: the earlier session's last sample can be seconds old and still be the newest there is. A reconnect onto the SAME slot keeps the cache, and the sample with it |
| a DEATH whose victim's position track is staler than 400 ms | KTX spawns the pack at the victim's origin; without one the drop would be a guessed point on the map |

### The refusal that is deliberately NOT made

A per-player "this `aw` column never moves" refusal looks obviously right —
it is the frozen-stat signature, applied at the resolution the evidence
has — and it is **wrong**. It was implemented, measured, and reverted.

A single-valued `aw` column is what a **single-weapon ruleset** looks
like. In `1on1-lgc` (and the povdmm4 LG challenges), `2on2-midair`, and
rocket arena on `end` / `endif`, the wielded weapon cannot change: every
player's whole column is `[{0,64}]` or `[{0,32}]`, and the KTX hints
credit them with a pack on EVERY death — hint count exactly equal to death
count, across 20-death and 36-death players. Refusing them cost **58 of
13 749** ground-truth drops (recall 99.97% → 99.55%, all RL) and prevented
**no** fabrication: precision was 99.97% with the refusal and 99.97%
without, the same four fabrications either way.

The same argument protects the `||` in the demo-level gate
(`!activeWeaponLive && !damagerecon.WeaponBitsLive`). It reads like a
weakened `&&`, but on a single-weapon demo NO player's `aw` moves, so the
`STAT_ITEMS` arm is the only thing keeping the ruleset measurable. What
separates such a demo from a frozen recording is exactly that its item
bits still cycle.

The published `origin` is the **pack's**, not the victim's: KTX copies the
victim's origin and then applies `item->s.v.origin[2] -= 24`
(`items.c:2703-2704`), putting the pack at their feet. Both provenances
apply it, so the hint path and the reconstruction publish one convention
and a map overlay draws every pack where it sat. (Before this, both paths
published the unadjusted player position — which is why the position-error
figures below, measured between two paths sharing the same offset, did not
move when it was fixed.)

The plan for this work assumed `DropBackpack` also returns early in
midair / smashpack / extinction / wipeout / CA. **It does not** — there is
no such early return in `items.c`, `smashpack` does not exist in this KTX
at all, and the ground-truth run confirms packs drop in those modes
(`-midair`, `-ca`, `-wo` demos score at or near 100%). Reusing
`damagerecon.SkipModeReason` wholesale would therefore withhold drops KTX
demonstrably makes, so `backpackSkipModeReason` is deliberately narrower;
it reads the same serverinfo `mode` string and the same countdown-derived
`MatchSettings`.

## Headline numbers — the DROP side (2026-08-18, re-run after the PR review fleet)

**316 demos scored** (330 sampled, 14 stood down), spanning every KTX
release that emits the hint, 1.38 through 1.48. 13 749 ground-truth
drops.

Re-measured after the review-fleet fixes (the pack origin offset, the
per-obituary `/kill` accounting, the cross-slot `aw` bound, the
conditional name undisambiguation, the `aw` value vocabulary, the
pre-match dedup cut, the harness's own integrity guards): **every figure
below is unchanged**. That is the expected result and not a null one —
each of those fixes addresses a shape the ground-truth sample does not
contain (it is 316 modern KTX demos), so the run's job here was to show
they cost nothing. The position error is unchanged for a sharper reason:
it is measured *between* the hint path and the reconstruction, and both
apply the 24-unit drop offset, so a change made to both is invisible to
it by construction.

| metric | value |
|---|---|
| precision | **99.97%** (13 745 / 13 749) |
| recall | **99.97%** (13 745 / 13 749) |
| drop-time error | **0 ms** at p50, p90, p99 and max |
| position error | p50 9.7, p90 22.3, p99 33.9 units; >200 units on 8 rows (0.058%) |
| volume | GT 0.272 drops/death, reconstruction 0.272 |

Per weapon:

| weapon | GT | reconstructed | matched | precision | recall |
|---|---|---|---|---|---|
| rl | 9 534 | 9 534 | 9 530 | 99.96% | 99.96% |
| lg | 4 215 | 4 215 | 4 215 | 100.00% | 100.00% |

Per era (`ktxver`), 25–30 demos each: 1.38 100/100, 1.39 99.94/99.87,
1.40 99.92/99.84, 1.41 100/100, 1.42 100/100, 1.43 100/100, 1.44 100/100,
1.45 99.91/100, 1.46 99.93/100, 1.47 100/100, 1.48 100/100 — **seven** of
the eleven buckets at 100% on both, and two more at 100% recall.

Per mode: 1on1 100/100 (108 demos), 2on2 100/100, 4on4 100/100, duel
100/100, team 100/100, ffa 100/100, `tot` 96.23/100 (2 demos, 51 drops).

The 14 stand-downs were 9 `frozen weapon state` and 5 `no frag log` —
both refusals, not errors.

### Residual mismatch classes

All four misses and all four fabrications survive from earlier iterations
of the pass; both classes were investigated and left in place.

- **4 misses (0.03%)**: `STAT_ACTIVEWEAPON` read 1 (shotgun, ×3) or 4096
  (axe, ×1) at the death instant while KTX packed the RL. The stat is
  written once per server frame after the frame's physics, so a weapon
  autoswitch inside the death frame — KTX's post-death `W_SetCurrentAmmo`
  / "drop down to best weapon actually hold" — lands in the same sample.
  Not correctable from the wire.
- **4 fabrications (0.03%)**: 2 drops whose dropper carries no hint
  anywhere in the demo, 2 with no hint within 2 s of the death.
- **8 position outliers (>200 units)**: the victim's last broadcast
  position was a frame or more behind the death. Everything below p99 is
  one frame of movement, which is also what separates the hint path's own
  origin from the death instant.

Re-measured once more after the parser gained the item-entity origin track
(the pickup linkage's enabler, below), on a 335-demo redraw of the same
sample: 314 scored, 13 777 ground-truth drops, **precision 99.96%, recall
99.96%**, drop-time error 0 ms at p50/p90/p99. The drop side does not read
entity events at all, so this is the null result it should be. Re-run again
after `//ktx expire` was decoded: byte-identical (314 scored, 13 777 GT,
99.96 / 99.96, 6 misses, 6 fabrications), which it must be — the expire hint
lands on the PICKUP side of a hint row and the drop side never reads it.

Two earlier classes were **fixed** rather than tolerated, and the
before/after is the reason the numbers moved from 99.88 to 99.97:

- **93 misses**, "no active-weapon sample at or before the death": the
  timeline discarded every pre-match stat update, and `STAT_ACTIVEWEAPON`
  is delta-coded, so a player whose weapon last changed during warmup had
  no in-match sample at all. The wielded-weapon column is now recorded
  through the countdown and the match-start rebase carries the latest
  pre-match value to `t=0`.
- **13 misses + 13 fabrications** in one demo, from a **name-vocabulary
  split**: the hint path stamped the dropper's raw userinfo name while the
  streams carry the identity-resolved (and, on a collision, `#slot`
  -suffixed) display name. The hint path now resolves through the shared
  `ResolveSlotAt` chain and the reconstruction undisambiguates the stream
  name, so both provenances publish the name every other section joins on.
  This also fixed a silent correctness bug: the `/kill` suppression looks
  the victim up in the frag log by name, and for a `#slot`-suffixed stream
  it had been failing to find them.

## Pickup linkage (schema v72)

The reconstruction says a pack was dropped. What happened to it is read off
the wire's own backpack-ENTITY track by `analyzer/backpack_linkage.go`, and
lands on the same row: `fate` (`picked` / `expired` / `unobserved`) with
`picker`, `pickerTeam`, `pickupTime` and the bound `entNum`.

### Why the entity track is usable at all

The plan for this work recorded the entity stream as unusable — "measured
16.4 visibility flips per real pack". That measurement was of a parser that
cached an edict's item kind **forever**. KTX `spawn()`s a pack
(`items.c:2701`) and `ent_remove()`s it on pickup (`items.c:2489`), so a
pack edict is handed on within seconds — to another pack, a rocket, a gib —
and every later tenant's appearance and disappearance was reported as the
original pack flickering.

The fix is in the parser, not in a smoothing filter: **the cached kind never
outranks a model index the wire is currently sending**
(`mvd-reader/parser/entities.go` `diffItemEntity`). With it, a pack's life is
one appearance and one disappearance. Measured over 24 hint-carrying demos:

| | |
|---|---|
| pack lives | 3 205 against 3 319 deaths — one pack per death, as `DropBackpack` makes |
| lives re-opening where the previous one ended (the flutter signature) | **0** |

So there is no flutter left to stitch. The stitching rule the plan called
for was implemented anyway as a speed gate (an origin jump faster than
`sv_maxvelocity` = a recycled edict) and **fired zero times over those 3 205
lives**, so it was removed rather than shipped as unexercised machinery:
mvdsv refuses to reallocate an edict freed less than half a second ago
(`mvdsv/src/pr_edict.c:118-127`), which at `sv_demofps 30` is fifteen demo
frames with the edict absent. A recycle can never masquerade as movement.

### The chain, and what each step measures

**1. Bind.** The reconstruction names a time and a place (the victim's
origin less 24, where `DropBackpack` puts the pack, `items.c:2703`). The
pack appearing at that instant nearest that point is the drop's pack;
binding is nearest-first over all candidate pairs, so a pack two drops both
reach goes to the one on top of it. Scored against the `//ktx drop` hint's
own edict number, blind to it:

| | |
|---|---|
| drops bound to the edict the hint named | **947 / 961 (98.5%)** |
| bound to the WRONG edict | **0** |
| drop time → pack appearance | 0 ms at p50, p99 **and max** |
| drop position → pack's first origin | p50 4.7, p99 23.3, max 28.3 units |

The `packBindMaxDist` cap of 128 units is therefore a refusal ("no pack
appeared where this player died"), not a search radius — binding is decided
by nearest-wins.

**2. Follow.** A pack is tossed (`velocity[2] = 300` plus a random ±100
horizontal kick, `MOVETYPE_TOSS`, `items.c:2856-2861`), so it arcs, falls
off ledges and rides down lift shafts before settling. Measured travel from
spawn to rest: **p50 58, p90 112, p99 422, max 583 units** (vertical drop
p90 88, max 573). The pickup test runs where the pack landed, never where it
was dropped — with a median of 48 origin updates per pack, this is a tracked
quantity, not an assumption. The enabler is the new `ItemMoveEvent` plus an
`Origin` on `ItemStateEvent`.

**3. Read the disappearance.** A pack leaves the wire for two reasons in a
normal match: `BackpackTouch` removed it (`items.c:2367`) or `SUB_Remove` did
at the 120 s timeout (`items.c:2871-2872`). (A third exists and is not one of
the two: hoony mode wipes every pack on its per-point reset,
`remove_items("backpack")` at `hoonymode.c:376`, through `ent_remove` rather
than `SUB_Remove` — a pack that vanishes mid-life with nobody on it, which
lands in `unobserved`.) Which of the two is decided by the test the server
itself runs first — whether any LIVE, mode-ELIGIBLE player's bounding box
overlapped the pack's:

```
player origin + (-17,-17,-25) .. (17,17,33)     // setsize + 1 on every axis
pack   origin + (-31,-31,  0) .. (31,31,56)     // setsize + 15 in x/y, FL_ITEM
```

The 15-unit expansion (`mvdsv/src/sv_world.c:373-379`, "to make items easier
to pick up and allow them to be grabbed off of shelves") is not a detail:
without it the predicate finds nobody on **90%** of real pickups, because
the two broadcast origins are legitimately up to 66 units apart. The test
runs over the whole path the player travelled between the two broadcast
samples bracketing the disappearance, not at either endpoint — an MVD is
written at `sv_demofps` while the server runs at its own tick, so the touch
instant falls strictly between two broadcasts and a running player covers
10-16 units in that gap. Endpoint-only testing cost 211 of 10 000 real
pickups, every one of them overshooting a bound by 0-9 units.

**The PACK is held at its last broadcast origin across that sweep, and this
was measured, not assumed.** A pack is still travelling on the frames around
a mid-air grab, so placing it at each player sample's own instant — sweeping
two moving points against each other instead of a point against a box —
looks like the more faithful test. Implemented and scored, it is **worse**:
`picked` recall 96.13% → **93.64%**, 249 of 10 000 real pickups lost, with
no precision to gain (100.00% either way). The reason only shows up in the
numbers: the pack's last origin is broadcast in the very frame it left the
wire, so it is a sample AT the touch instant, while its earlier origins are
samples of where it no longer was. Walking the pack backwards tests the
overlap at times the touch did not happen at. Reverted; the fields that
tracked the pack's previous origin were removed with it rather than left in
as unexercised machinery.

The same measurement disposes of the related idea for the 0-9 unit near-miss
class below. That class is dominated by "the pack is 33-41 units ABOVE the
player" — a pack that fell further between broadcasts — and the pack's
EARLIER origins are higher still, so the pack's own track cannot reach it in
the direction needed. Only extrapolation PAST the final broadcast could, and
that invents the position that decides the pickup.

### Who is allowed to touch it — the mode guard

`BackpackTouch` does not only reject dead touchers. It rejects a live,
overlapping player whose mode-specific state the ruleset bars from taking a
pack (`ktx/src/items.c:2393-2425`):

```c
if (deathmatch == 4) { if (other->invincible_finished) return; }   // pent
if (isRA() && !isWinner(other) && !isLoser(other)) return;         // waiting area
if (cvar("k_midair") && other->super_damage_finished) return;      // quad
if (cvar("k_instagib") && other->invisible_finished) return;       // ring
if ((deathmatch == 4) && lgc_enabled() && (other->s.v.health >= 300)) return;
```

Each one leaves the pack ON the wire, so a replay that ignores them credits a
pickup to a player the server refused — and stops looking for whoever really
took it. Four of the five are reproducible because the state they read is
broadcast: `invincible_finished`, `super_damage_finished` and
`invisible_finished` are set and cleared in lockstep with the
`IT_INVULNERABILITY` / `IT_QUAD` / `IT_INVISIBILITY` bits
(`ktx/src/client.c:2283-2289, 4062-4174`), which is exactly what the Pent /
Quad / Ring possession intervals are derived from, and health is its own
stream. The mode itself comes from the same two sources
`backpackSkipModeReason` reads — the published cvar, then the composite
serverinfo `mode` string, then the countdown-derived `MatchSettings`.

**RA's waiting area is deliberately not guessed at.** `isWinner` / `isLoser`
are arena bookkeeping with no wire representation anywhere: no stat, no
serverinfo key, no print. Demoting every RA pack to `unobserved` was the
alternative and is worse — it erases the mode's entire pickup signal to guard
against a class the geometry mostly handles anyway, since the waiting area is
a separate room from the arena floor where packs drop. It is a residual, and
listed as one below.

**What the guard costs: nothing measurable, and that is the expected
result.** The ground-truth population is 223 modern KTX demos and contains no
DMM4, midair, instagib, LGC or RA demo at all — every scored demo is 1on1 /
2on2 / 4on4 / duel / team / ffa. Every headline figure below is byte-identical
with and without the guard. Its job here was to show it costs nothing on the
population that exists, the same null result the drop-side review fixes
produced; the modes it protects are precisely the ones no ground truth
reaches.

**Spectators need no guard of their own**, even though `other->ct != ctPlayer`
is `BackpackTouch`'s very first line. `Streams.Players` is not filtered on
participation — it carries a row for any occupant with stream data — but
mvdsv writes no `svc_playerinfo` and no stats for a spectator
(`sv_ents.c:463`, `sv_demo.c:1540`, `:1618`), so a spectating stretch
contributes no position samples. A pure spectator therefore never gets a row
at all, and someone who plays part of a match and spectates the rest keeps
one merged row whose track simply stops. The position-staleness bound is what
excludes them: with no sample within 200 ms of the disappearance there is no
position to test — which matters, because `Alive` DOES overhang a spectating
stretch (`clipToPresence` trims only the ends of a track and never splits on
an interior hole).

### The expiry boundary, and why it is a cadence and not a constant

`expired` is the one disappearance that is positively NOT a pickup, so what
counts as "reached the timeout" decides it. KTX arms it once, unconditionally:

```c
// remove after 2 minutes, and after 30 seconds if backpack dropped by monster
item->s.v.nextthink = g_globalvars.time + (self->ct == ctPlayer ? 120 : 30);
item->think = (func_t) SUB_Remove;                       // items.c:2870-2872
```

Verified across the whole of `DropBackpack` (`items.c:2667-2885`): nothing
re-arms `nextthink` afterwards. The 30 s arm is for a MONSTER-dropped pack and
cannot apply — every reconstructed drop keys on a player death, and the one
KTX mode with monsters, bloodfest, is stood down before the reconstruction
runs. **Yawnmode does not change it**: it rewrites the pack's contents and its
ammo caps (`items.c:2754, 2820-2827`) and is stood down anyway. Hoony mode is
the one thing that removes a pack early, and it does so through `ent_remove`
directly (`hoonymode.c:376`, per-point reset) rather than `SUB_Remove` — so a
hoony pack simply vanishes mid-life with nobody on it, which lands in
`unobserved`, correctly.

The rule that reads it had two failure directions, and both are now closed:

- A real pickup at 119 s whose picker's track has a hole used to come out
  `expired`, because the old threshold was the timeout less a flat two
  seconds. The threshold is now the timeout less **three broadcast
  intervals of the demo's own cadence** (`broadcastCadenceMs`, the median
  inter-sample gap of the longest position track). Three, because three
  distinct quantisations stack: `Start` is the first frame the pack was
  visible, `End` is a frame boundary too, and `nextthink` fires on the first
  SERVER frame at or after the deadline. A flat constant cannot be right at
  both 13 ms and 40 ms per frame.
- A `SUB_Remove` at 120 s with a player who happened to be crossing the pack
  used to come out `picked`. At the timeout the pack was leaving the wire in
  that frame whatever stood on it, so the touch has to outrank `SUB_Remove`
  to win — and a merely SWEPT path is also exactly what running past an
  expiring pack looks like. Inside the timeout window only an overlap the
  wire actually SAMPLED counts; everywhere else the swept path remains the
  right test.

The measurement that sets the boundary, over the 223-demo ground truth:

| bound pack lifetime | p50 | p90 | p99 | max |
|---|---|---|---|---|
| `//ktx expire` says it timed out (n=190) | 119 995 | 120 014 | 120 023 | 120 027 |
| `//ktx bp` says it was taken (n=9 793) | 2 116 | 15 100 | 56 603 | **117 815** |
| claimed by neither hint (n=0) | — | — | — | — |

The two classes do not overlap and are not close: **not one** of the 9 793
real pickups reaches 118 000 ms, and 189 of the 190 real expiries sit at or
above 119 900. Any threshold in that 2-second gap scores identically — 190
`expired` at 100.00% precision — which is why the change is worth making for
the right reason rather than the number: the cadence form holds on both
recording rates, where a constant tuned to one of them does not.

The third row is empty and that is a result, not a gap in the table: the 188
drops no hint claims contribute NO bound-pack lifetime at all, because their
packs never left the wire. Nothing in this measurement is being asked to
separate them, which is why they land in `unobserved`.

### The expiry side is wire ground truth (`//ktx expire`)

The threshold above was measured against a ground truth that could only say
"no `//ktx bp` names this pack". That is not the same claim as "it expired",
and the difference was the whole of the `expired` recall gap. KTX states the
expiry outright, in the third and last of its backpack directives:

```c
void SUB_Remove(void)
{
        if (self && streq(self->classname, "backpack"))
        {
                if ((self->s.v.items == IT_ROCKET_LAUNCHER) || (self->s.v.items == IT_LIGHTNING))
                        stuffcmd_flags(world, STUFFCMD_DEMOONLY, "//ktx expire %d\n", NUM_FOR_EDICT(self));
        }
        ent_remove(self);
}                                              // ktx/src/g_spawn.c:196-210
```

Same RL/LG domain as `//ktx drop` and `//ktx bp`, so the three are one
family and a pack accounts for itself in exactly one of the latter two. The
parser decodes it as `BackpackExpireHintEvent`; `BackpackAnalyzer` joins it
to the drop it closes and stamps that row `fate: "expired"` — the one
pickup-side fact a hint row carries, because the `weaponPickups` join cannot
state it and the ABSENCE of a `bp` row is not evidence of it.

**The join is by edict AND time.** A match runs many packs through one
edict, so the hint closes the newest drop on that edict at or before it, and
only when the gap is KTX's own timeout: `DropBackpack` arms
`nextthink = time + 120` (`items.c:2870-2872`) and nothing re-arms it, and
both directives are timestamped in demo time, which is `sv.time` — frozen
while the server is paused (`mvdsv/src/sv_main.c:3296`), so not even a pause
stretches the interval. Measured across the 330-demo sample: **all 234
expire hints paired, at 119 953–120 027 ms after their drop** — a 74 ms
spread, and zero orphans. The tolerance is 1 000 ms, an order of magnitude
either side of that.

**The invariant, checked at scale.** Over the 223 demos that carry all three
directives:

| | |
|---|---|
| `//ktx drop` rows | 10 384 |
| — claimed by `//ktx bp` | 10 006 |
| — claimed by `//ktx expire` | 190 |
| — claimed by neither | 188 |
| rows claiming BOTH | **0** |
| demos with a non-zero `neither` residual | 102 of 223 |

So `drop = bp + expire` does NOT hold row by row, and the earlier note above
that said it does was wrong: 1.8% of drops are claimed by neither hint, on
just under half the demos. Those are not missing hints — they are packs
whose removal never happened inside the recording. The harness shows it
directly: of those 188 rows, **not one** has a bound pack entity that ever
left the wire, while all 190 wire-confirmed expiries have one with a
lifetime at 119 953–120 027 ms. The match ended on top of them.

That distinction is exactly what `expired` recall was being penalised for.
Against "every drop with no `bp`" the pass scored 190 / 378 = 50.26%;
against the packs KTX actually announced it scores **190 / 190**.

### Why not a stat flip — the tier that was NOT needed

`//ktx bp` fires on every RL/LG pack pickup regardless of what the picker
already held (`items.c:2489-2494`), and `other->s.v.items |= new` cannot
change a bit they already had. Measured: of 606 unambiguously-attributed
ground-truth pickups, only **237 (39%)** came with a weapon-bit gain. A
stat-flip requirement — or a stat-flip-only lower-confidence tier — would
have discarded 61% of real pickups while adding nothing the touch does not
already say. The bounding-box overlap is not a proxy for the touch, it IS
the touch, so it is the primary and only signal for "was it taken".

The weapon-bit gain is used for one narrow job: separating two players
standing on one pack. On the probe sample it resolved 22 of 45 such cases
and got **none** of them wrong; the other 23 stay `picked` with no `picker`.
A gain that could have come from a world spawner the player is standing on
is disqualified (`nearWeaponSpawner`, reading the spawner positions off the
item timeline), because it separates nothing.

### Headline numbers — the PICKUP side (2026-08-18, re-run against `//ktx expire`)

**223 demos scored** (330 sampled; 107 skipped for carrying no `//ktx bp`),
spanning KTX 1.38 through 1.48. **10 378 scored drops**, of which 10 171
(98.01%) bound to a pack entity. Ground truth: 10 000 picked (`//ktx bp`),
190 expired (`//ktx expire`), 188 claimed by neither hint. All three hints
withheld — the reconstruction and the linkage never read `res.Backpacks` or
`res.WeaponPickups`.

The only figure that moved when `//ktx expire` became the `expired` ground
truth is `expired` recall, and it moved because the DENOMINATOR was wrong
before, not because the pass changed: nothing in `backpack_linkage.go` is
different. Every `picked` and attribution figure is byte-identical to the
run before it.

| metric | value |
|---|---|
| `picked` precision | **100.00%** (9 613 / 9 613) |
| `picked` recall | **96.13%** (9 613 / 10 000) |
| `expired` precision | **100.00%** (190 / 190) |
| `expired` recall | **100.00%** (190 / 190) — was 50.26% against "no `//ktx bp`" |
| `unobserved` | 575 rows (5.54%) — 387 were picked, **0 expired**, 188 claimed by neither hint |
| picker named, of correct pickups | **99.77%** (9 591 / 9 613) |
| named picker correct | **99.98%** (9 589 / 9 591) |
| pickup-time error | **0 ms** at p50 and p90; p99 250 ms, max 5 041 ms |

The `0 expired` in the `unobserved` row is the point: the pass does not miss
expiries at all. What it declines to call expired is the 188 packs KTX never
said expired either.

Per era (`ktxver`), 13–30 demos each — both precisions are 100.00% in every
bucket and `expired` recall is 100.00% in all eleven, so only `picked`
recall and attribution vary:

| ktxver | 1.38 | 1.39 | 1.40 | 1.41 | 1.42 | 1.43 | 1.44 | 1.45 | 1.46 | 1.47 | 1.48 |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `picked` recall | 94.67 | 96.19 | 95.08 | 96.44 | 96.52 | 97.80 | 97.97 | 95.23 | 96.61 | 96.94 | 96.03 |
| picker correct | 100 | 100 | 100 | 100 | 100 | 100 | 100 | 99.79 | 100 | 99.89 | 100 |
| GT expiries | 29 | 58 | 18 | 8 | 53 | 5 | 5 | 1 | 5 | 4 | 4 |

Per mode, all at 100.00% `picked` precision and 100.00% `expired` recall:
1on1 96.98% recall (60 demos), 4on4 95.99% (60), 2on2 97.50% (17), duel
97.99% (9), team 95.59% (9), ffa 96.97% (2), demos with no mode string
95.33% (65).

### Residual classes

`expired` has no residual left: every pack KTX announced as expired came out
`expired`, and nothing else did. The 188 rows that carry no pickup hint and
no expiry hint come out `unobserved`, which is the correct answer — the wire
says nothing about them, and the harness confirms why: not one of them has a
bound pack entity that ever left the wire, so the recording ended while they
lay there. That class was the entire former "recall gap".

The 387 real pickups that came out `unobserved` break down as:

| cause | n | fixable? |
|---|---|---|
| no pack entity on the wire at all; the wire says the pack was taken **within one demo frame** of the drop | 202 | **No.** An MVD is written at `sv_demofps` (default 30) and the pack was gone before the next frame — it never existed on the wire. This is 2% of all drops and the single largest residual. |
| the picker was outside the touch box by 0-9 units on both bracketing samples and on the path between them | 167 | Only by extrapolating the pack's own motion past its last broadcast (it is still falling: the overshoot is almost always "pack is 33-41 above the player", the shape a pack that fell further between broadcasts makes). Rejected as inventing data — and the cheaper version of the idea, sweeping the pack along the track it DOES have, was implemented and measured to cost 249 real pickups (above). |
| the picker was outside every derived life at the disappearance | 8 | A liveness-derivation edge, 0.08% of pickups. |
| a nearer drop claimed every pack entity in the window | 5 | Two deaths in one frame at nearly the same spot. |
| the nearest pack entity was beyond the 128-unit bind cap | 5 | The refusal working; the packs are 179-2 325 units away. |

The **2 wrong attributions** (0.02% of named pickers) are both two-players-
on-one-pack cases where the weapon-bit gain named the player who was not
the picker.

### What the linkage still cannot say

- **`hadBefore`**, and therefore kill credit and pack TRANSFER credit
  (`playerStats[].pickups.byKind[].xfer`). KTX ORs the weapon bit in, so a
  redundant grab leaves no trace anywhere on the wire. This is why the
  linkage's output stays on the `backpacks` row and is deliberately NOT
  written into `weaponPickups`, whose rows document themselves as
  authoritative KTX hints and feed exactly those wire-measured stats.
- **Which of two players on one pack took it**, when neither gained the
  weapon.
- **Whether an RA toucher was fighting or waiting.** `BackpackTouch` refuses
  a pack to a rocket-arena player in the waiting area
  (`isRA() && !isWinner(other) && !isLoser(other)`, `items.c:2404-2408`) and
  that status has no wire representation, so the guard above implements the
  other four mode rejections and not this one. A pack taken in an RA waiting
  area would be attributed to the waiting player. No RA demo reaches the
  ground-truth population, so the class is unmeasured as well as unhandled.

## Volume sanity on the hint-less population

No ground truth exists there, so two checks:

**674 demos** sampled across 40 `version` buckets with `ktxdrop=0`
(mvdsv 0.19 through 1.20, qwsv 2.30/2.40, KTX 1.34–1.37 and the
hint-carrying releases that happened to emit none). All 674 were
*measured*, 0 excluded — the sample turned out to contain no
hint-carrying demo, which the harness now checks for rather than assumes
(a hint-carrying demo's rows would otherwise have reached both figures
below as if the reconstruction had produced them):

- 449 produced drops, 225 did not: 108 `hinting mod emitted no drops`, 85
  `no frag log`, 15 `frozen weapon state`, 14 `no player streams`, 1
  `mode:fairpacks`, 1 `mode:yawnmode`. The 108 are the KTX ≥ 1.38 gate
  working — those demos are the wire saying "no packs", and every ≥ 1.38
  era bucket in the per-era table reports a rate of exactly 0.000.
- **0.235 drops/death** overall (12 763 rl, 4 088 lg) against the hinted
  population's **0.293** — comparable, as expected of two samples with
  different map/mode mixes. Per-era rates run 0.08–0.62; the low end is
  the large-team/FFA demos where shotgun deaths dominate.

**Pack-fate mix** over the 16 851 reconstructed drops, the linkage's own
volume sanity (again no ground truth exists here):

| `fate` | n | share |
|---|---|---|
| `picked` | 14 613 | **86.72%** |
| `unobserved` | 1 767 | 10.49% |
| `expired` | 471 | 2.80% |
| — of `picked`, a picker was named | 14 565 | 99.67% |

Re-drawn and re-measured after the review-fleet fixes on a 531-demo sample
(13 466 drops, 0.249 drops/death, inventory cross-check 13 466/13 466): the
mix comes out 86.54% `picked` / 10.62% `unobserved` / 2.84% `expired` with a
picker named on 99.73% of the picked — every share within 0.2 points of the
table above, on an independently drawn sample. The hint-less population is
where the `BackpackTouch` mode guard could bite, since it spans far more
rulesets than the ground truth does, and it does not move the mix either.

That is the shape the hinted population predicts. There the wire's own
account is 96.4% picked / 1.83% expired / 1.81% claimed by neither hint, and
the linkage recovers 96.1% of the pickups and 100% of the expiries with the
remainder falling into `unobserved`; 86.7% + most of a 10.5% `unobserved`
bucket lands in the same place as the picked share.

The `expired` share is the one figure the two populations do NOT match on:
2.80% here against 1.83% there. That is a property of the samples, not a
defect — the pass is 100.00% precise AND 100.00% recalling on the hinted
population, so 2.80% is its honest estimate of THIS population's expiry
rate, and the two samples differ in map, mode and match-length mix (they are
drawn from disjoint `ktxdrop` halves of the archive by construction). The
older reading of this line — "2.80% must sit under the 3.6% never-picked
rate" — compared against the wrong quantity: 3.6% was expiries plus packs
the recording ended on top of, and the latter are `unobserved` on both
sides.

**`//ktx expire` does not reach this population.** Measured over the same
531-demo hint-less sample: **zero** expire hints, on every demo. The
directive is co-emitted with `//ktx drop` by the same KTX generation, so on
the demos the reconstruction actually runs on the expiry side is entirely
the entity-track inference — which is exactly the arrangement the ground
truth above validates, and why the linkage is never handed the hint.

**Pack-entity coverage on the target population.** The linkage is useless
if pre-1.38 recorders carried no entity stream. Measured on 86 demos with
`ktxver` absent or < 1.38, spanning qwsv 2.30/2.40 and mvdsv 0.19–0.28:
**9 928 pack lives against 10 078 deaths (98.5%)**, with every one of the
86 demos producing a track. Coverage is not the constraint.

**Inventory cross-check: 16 851 / 16 851 (100.00%)** of reconstructed
drops had the dropped weapon in the victim's `STAT_ITEMS` inventory
intervals at the death instant. That is an oracle independent of
`STAT_ACTIVEWEAPON` — a different stat, decoded by a different path — and
it is the only corroboration available where no hint exists. It is a
necessary, not sufficient, condition (a player can own the RL and wield
the LG), which is why it is reported beside the volume rate rather than as
an accuracy figure.

**Pre-KTX behaviour.** The ground-truth population is KTX ≥ 1.38 by
construction, so no direct measurement of qwsv/KTPro-era drop rules is
possible. What the data shows: pre-KTX demos (qwsv 2.30/2.40, mvdsv
0.19–0.28 with no `ktxver`) produce drops at rates in the same band as the
KTX population, with the same 100% inventory consistency, which is what a
mod following the id1 `DropBackpack` convention would produce. Nothing in
the sample suggested a different rule. This remains the weakest link in
the chain and is stated as such.

## Residuals with no wire signal

- **`dp 0`** — backpack drops switched off server-side. It is published
  nowhere: no serverinfo key, no countdown row, no print. On a hinting mod
  the absence of hints settles it (which is exactly why the pass stands
  down there); on a pre-1.38 demo it is unfalsifiable, and a `dp 0` server
  would make the section report packs that never dropped. No demo in
  either sample showed the signature.
- **Non-RL/LG packs** are out of scope by construction: KTX hints only for
  RL and LG, so extending the reconstruction to SSG/NG/SNG/GL packs would
  produce rows with no ground truth to validate them against, in a section
  whose documented scope is RL/LG.
- **`hadBefore`** — whether the picker already held the weapon. KTX ORs
  the weapon bit in (`other->s.v.items |= new`), so a redundant grab
  changes nothing on the wire. Pack TRANSFERS
  (`playerStats[].pickups.byKind[].xfer`) and pack kill credit both depend
  on it and therefore stay absent on reconstructed rows, even though the
  linkage does name the picker. `dropped` counts do include reconstructed
  drops. (A reconstructed row's `entNum` is no longer 0 — it is the bound
  backpack-model entity — but it joins to nothing, because there is no
  `//ktx bp` row to join to.)
