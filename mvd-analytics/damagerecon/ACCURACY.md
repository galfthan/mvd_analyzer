# damagerecon accuracy — validated against KTX ground truth

This package reconstructs the damage section for demos that predate the
KTX `mvdhidden_dmgdone` instrumentation. Accuracy is measured on modern
demos that carry BOTH the state streams and the real damage stream: the
reconstruction runs blind (it never reads `res.Damage` or any
damage-derived field) and is scored against the KTX log.

Reproduce with:

```
MVDA_BSP_DIR=./bsps go run ./mvd-analytics/cmd/qw-recon-eval           # metrics
MVDA_BSP_DIR=./bsps go run ./mvd-analytics/cmd/qw-recon-eval -diag    # + misattribution flows
```

over the golden-corpus cache (`mvd-analytics/testdata/cache/`, 13 demos:
4 duels, 3 2on2, 6 4on4, populated by the analyzer golden test's first
run). A fast regression subset is pinned in `eval_test.go` (runs in
`make test`, skips without the cache).

## Headline numbers (2026-08-15, 13 demos, 75 player rows with GT ≥ 200)

Per-player match totals, relative error vs the KTX log:

| metric | median | mean | p90 | ≤1% | ≤2% |
|---|---|---|---|---|---|
| bounded given | 1.08% | 1.23% | 2.63% | 47% | 83% |
| bounded taken | 0.05% | 0.21% | 0.52% | 96% | 100% |
| raw given | 1.32% | 1.79% | 3.75% | 39% | 69% |
| raw taken | 0.93% | 1.52% | 4.02% | 51% | 72% |
| bounded ewep | 1.92% | 2.34% | 5.47% | 29% | 53% |
| bounded givenTeam | 6.8% | 7.7% | — | small denominators (200–700) |
| bounded givenSelf | 9.4% | 10.1% | — | small denominators |

Event level: 99.6% of ground-truth damage instants have a same-instant
reconstructed delta; 98.8% of those match the bounded value exactly;
attacker attribution is 97.6% on unambiguous enemy instants.

## Why the errors are what they are

- **Taken needs no attribution** — the victim's own h/a deltas ARE the
  bounded value — so it is near-exact. The residual is same-frame
  masking (pickup merged with a hit) and the -99 corpse clamp on raw.
- **Given inherits attribution**: the residual error classes, by moved
  damage, are simultaneous-shotgunner confusion, close-range rocket
  self/enemy flips, and same-frame multi-attacker merges (one delta,
  one credited attacker). Duels concentrate the rocket flips; 4on4 the
  shotgun confusion.
- **givenTeam / givenSelf** run on totals ~10× smaller, so the same
  absolute flips read as big percentages. Their absolute errors are on
  the order of one rocket.

## What the reconstruction models beyond the prototype

The 2026-08-11 Python study (.reports/qw-damage-recon-2026-08-11/,
held-out burst-level validation) is the feasibility evidence. The Go
port adds, each verified against ground truth in the eval:

- masked death+respawn recovery (same-instant kill leaves no h row) and
  corpse-cycle spawn-telefrags, with attacker inference from teleport
  arrivals / occupied-pad proximity;
- corpse (gib) hits kept in the raw family only, as KTX does;
- engine-exact self-splash halving and quad-before-falloff ordering
  (ktx/src/combat.c T_RadiusDamage) — the sharp self-vs-enemy
  discriminator in close rocket fights;
- per-demo rocket-damage regime detection (fixed 110 vs vanilla
  100+random·20) with a second scoring pass;
- BSP tier (bspvis ray-trace): CanDamage-style splash gate, hitscan /
  nail / trackless-rocket line-of-sight gates, multi-point body rays;
- pent-window synthesis: hits on a pentagram holder with no armor leave
  NO h/a delta yet count in KTX's raw family — recovered from tracked
  explosions and pent rocket-jump fire sounds;
- nullified-hit raw recovery (armor-only drops: raw = save/fraction);
- LG water discharges (cells→0 signature, 35·cells radius model);
- kill overkill top-up from the damage model where the -99 corpse clamp
  hides it (quad rockets, discharges).

## Trust guidance for consumers

`damage.source == "reconstructed"` means: bounded **taken** is
measurement-grade; bounded **given** is a ~1% estimate for match totals;
per-hit attribution is ~98% but individual hits can be misattributed
(prefer aggregates to single events); team/self splits are indicative,
not exact; raw overkill beyond the -99 corpse clamp is model-derived on
killing hits. Arena-family maps (povdmm4/dmm4*/anarena/midair-style) and
CTF were out of validation scope (study §trust tiers); midair/instagib/
dmgfrags server modes are skipped entirely (no section).
