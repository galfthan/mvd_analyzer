# Backpack reconstruction accuracy — validated against KTX ground truth

`analyzer/backpack_recon.go` fills the `backpacks` section on demos whose
mod never emitted the `//ktx drop` hint — 50.8% of the 51k archive, and
83.9% of the reconstructed-damage era. Rows it produces are stamped
`source: "reconstructed"`; hint-derived rows keep `source: "ktx"`, and a
demo that carried hints is never touched.

Accuracy is measured on the OTHER half of the archive: demos that DO carry
`//ktx drop`. The reconstruction runs blind — it never reads
`res.Backpacks` — and is scored against the hints the pipeline itself
would have published.

Reproduce with:

```
# ground truth: precision / recall against the //ktx drop hints
go run ./mvd-analytics/cmd/qw-backpack-eval \
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

## Headline numbers (2026-08-18, re-run after the PR review fleet)

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

## Volume sanity on the hint-less population

No ground truth exists there, so two checks:

**551 demos** sampled across 40 `version` buckets with `ktxdrop=0`
(mvdsv 0.19 through 1.20, qwsv 2.30/2.40, KTX 1.34–1.37 and the
hint-carrying releases that happened to emit none). All 551 were
*measured*, 0 excluded — the sample turned out to contain no
hint-carrying demo, which the harness now checks for rather than assumes
(a hint-carrying demo's rows would otherwise have reached both figures
below as if the reconstruction had produced them):

- 376 produced drops, 175 did not: 58 `no player streams`, 57 `hinting mod
  emitted no drops`, 52 `no frag log`, 8 `frozen weapon state`. The 57 are
  the KTX ≥ 1.38 gate working — those demos are the wire saying "no packs",
  and every ≥ 1.38 era bucket in the per-era table reports a rate of
  exactly 0.000.
- **0.254 drops/death** overall (10 285 rl, 3 203 lg) against the hinted
  population's **0.272** — comparable, as expected of two samples with
  different map/mode mixes. Per-era rates run 0.12–0.62; the low end is
  the large-team/FFA demos where shotgun deaths dominate.

**Inventory cross-check: 13 488 / 13 488 (100.00%)** of reconstructed
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
- **`entNum`** is 0 on reconstructed rows, so they cannot join to
  `WeaponPickup.BackpackEnt`, and pack TRANSFERS
  (`playerStats[].pickups.byKind[].xfer`) stay absent — they need the
  `//ktx bp` pickup hint, which ships with the same KTX generation as the
  drop hint. `dropped` counts do include reconstructed drops.
