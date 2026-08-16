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

## Headline numbers (2026-08-15, after temp-entity evidence)

Per-player match totals, relative error vs the KTX log. Golden cache
(13 demos, 75 player rows with GT ≥ 200):

| metric | median | mean | p90 | ≤1% | ≤2% |
|---|---|---|---|---|---|
| bounded given | 0.58% | 0.81% | 1.98% | 75% | 91% |
| bounded taken | 0.05% | 0.21% | 0.52% | 96% | 100% |
| raw given | 1.15% | 1.57% | 3.91% | 41% | 71% |
| raw taken | 0.90% | 1.51% | 4.07% | 52% | 73% |
| bounded ewep | 1.04% | 1.50% | 3.05% | 49% | 68% |
| bounded givenTeam | 2.4% | 5.7% | — | small denominators (200–700) |
| bounded givenSelf | 3.7% | 5.1% | — | small denominators |

The larger blind corpus (60 fresh dm2/dm3 hub demos, 321 rows — raw
eval outputs in `.reports/qw-recon-eval-dm2dm3-2026-08-15/`, an
untracked per-machine report directory; fetched with
`cmd/fetch-eval-corpus`) scores comparably: bounded given median
**0.60%** (mean 0.81%, p90 1.85%, ≤2% 92%), bounded taken 0.04%, raw
given 1.23%, raw taken 1.71%.

Event level (60-demo corpus): 99.6% of ground-truth damage instants have
a same-instant reconstructed delta; 98.9% of those match the bounded
value exactly; attacker attribution is 98.5% on unambiguous enemy
instants (rl 99.6%, lg 99.5%, sg 97.9%, ssg 98.2%, gl 99.5%, axe 98%).

A third corpus generalizes the result across server generations: 216
**archive-era instrumented demos** (a random sample of the 51k-demo
archive with GT present — older KTX/MVDSV builds, maps and modes far
outside the dm2/dm3 tuning set) score bounded given median 0.64%
(mean 1.38%, ≤2% 89%), attacker attribution 98.7% (rl 99.0%, lg 99.5%),
with no per-corpus tuning. Residual outlier rows are characterized:
skull-map crusher damage (GT logs mover-crush squish ticks; the
reconstruction deliberately leaves crush `unknown`) and small-
denominator old-FFA rows. The un-instrumented half of the archive runs
end-to-end with a 0.4% median / 1.9% mean unattributed-damage share
(`cmd/qw-corpus-survey`).

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
  hides it (quad rockets, discharges);
- same-frame pair splitting: a merged multi-attacker instant that no
  single candidate's damage range can explain, but a pair of different
  attackers sums to, is split between them (range-midpoint proportional);
- temp-entity hit telemetry (`streams.pointEffects`, 2026-08-15):
  TE_EXPLOSION snaps tracked flight endpoints to the exact detonation
  point and anchors the point-blank rockets/grenades whose entity never
  broadcast (shooter recovered by flight-time + aim to the detonation
  point — replacing the geometry-less rl-sound guess); TE_BLOOD confirms
  hitscan hits on the victim, with per-demo count calibration
  (`calibrateBloods`: 4·Σcounts == delta on ≥70% of single-shotgunner
  samples) unlocking count-pinned volley magnitudes and softening the
  aim-cone gate's false negatives; TE_LIGHTNINGBLOOD separates LG hits
  from beams that merely pass near the victim. Absence of the stream
  (older parses, `-include` without projectiles/beams) degrades
  gracefully to the pre-telemetry behaviour;
- the axe: swings enter the shots stream (`weapons/ax1.wav`) and link at
  their engine-true +200ms traceline delay (W_FireAxe fires two 0.1s
  animation thinks after the swing sound) — axe attribution 21% → ~100%;
- environmental classification (ktx/src/client.c WaterMove + the landing
  path): lava = 10·waterlevel/0.2s, slime = 4·waterlevel/1s, drowning =
  escalating 4..14/1s after 12s of full submersion, landings = flat 5 at
  vz < −650 — categorized from BSP liquid contents at the victim's
  position, the velocity track, and typed env suicide obituaries.
  Category accuracy vs GT: lava 97%, fall 95%, drown 83% (n=6); env
  values are exact-fit-only candidates so they can never absorb an
  unexplained weapon delta. GT's env `ng`/`sng` rows (a disconnected
  shooter's nails, attacker slot −1 on the wire) stay attributed to the
  actual shooter here — a deliberate divergence. Mover crush (`squish`
  ticks) stays `unknown`. Without a provisioned BSP the liquid
  categories degrade to `unknown` (fall still works).

## Old-recorder degradations (detected per demo, withheld not faked)

Pre-instrumentation recordings often freeze parts of the StatItems/ammo
stat channel: weapon inventory bits never cycle (a player "holds" RL from
0:00 through every death while the armor bits in the same stat update
normally) and ammo counts stay constant. Consequences, all handled by
detection rather than fabrication:

- **victim-weapon buckets** (`victimWep`, `enemyVs*`, `ewep`): when the
  demo's weapon bits never cycle, the classification is withheld —
  `victimWep` is empty and the buckets stay zero (they would otherwise
  ALL land in the top bucket, confidently wrong). `ewep: 0` on a
  `source: "reconstructed"` section therefore means *unmeasurable*, not
  "no damage to armed enemies" — the frozen-bits case is the norm for
  old demos.
- **LG discharges** need the cells stream; with frozen ammo they are not
  detected (their raw value stays at the clamp-limited observation).
- **LG beams themselves can be under-recorded**: old servers (observed
  MVDSV 0.33) drop a fraction of TE_LIGHTNING2 multicasts from the demo —
  whole shaft bursts with no beam at all. Detected per demo (recorded
  beams < 90% of fire-sized cells decrements; modern KTX is ~100%) and
  recovered from the ammo side: on such demos, at instants with no beam,
  a cells decrement within 250ms generates an LG candidate gated by the
  id1 LG range, line of sight and the aim cone. With frozen ammo this
  fallback quietly stands down too.
- **quad/pent intervals** come from the same stat; where a recorder froze
  powerup bits the ×4 model and pent synthesis quietly stand down.

## Trust guidance for consumers

`damage.source == "reconstructed"` means: bounded **taken** is
measurement-grade; bounded **given** is a well-under-1%-median estimate
for match totals; per-hit attribution is ~98% but individual hits can be
misattributed
(prefer aggregates to single events); team/self splits are indicative,
not exact; **pre-MVDSV-0.30 recordings carry 10-50× sparser hitscan
telemetry (TE_BLOOD/TE_GUNSHOT) than the corpora above, and no GT eval
covers them yet — treat reconstructed sections on that oldest slice
(~40% of the archive) as unvalidated estimates**; raw overkill beyond
the -99 corpse clamp is model-derived on
killing hits. Arena-family maps (povdmm4/dmm4*/anarena/midair-style) and
CTF were out of validation scope (study §trust tiers); midair/instagib/
dmgfrags server modes are skipped entirely (no section).
