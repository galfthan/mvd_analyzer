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

## Per-era validation on the un-instrumented archive (2026-08-19)

Every corpus above is an instrumented recording — an MVDSV 0.34+ demo
that carries a KTX damage log to score against. They say nothing about
the older 40% of the archive (eras E0–E2 of the archive data survey:
qwsv/KTPro, MVDSV 0.19–0.29), which carries no ground truth at all.
`cmd/qw-recon-oracle` measures that half with the one statement about
damage those recordings still put on the wire: the **obituary**.

```
# oracle only (no KTX log on these demos)
go run ./mvd-analytics/cmd/qw-recon-oracle -dir /data/mvd -list list-E0-recon.txt -csv oracle-E0.csv
# oracle + KTX-log baseline, which calibrates what the oracle number means
go run ./mvd-analytics/cmd/qw-recon-oracle -dir /data/mvd -list list-E4-gt.txt -gt -csv oracle-E4.csv
```

Run from the repo root (the reconstruction wants `./bsps`). The demo
lists, per-demo CSVs, the sampler and the aggregation live in
`.reports/qw-recon-oracle-2026-08-19/`, an untracked per-machine report
directory.

### What each candidate oracle can certify — and two that cannot

- **A survived hit's h/a delta is not an oracle.** The reconstruction's
  bounded value IS the observed health+armor delta (`deltas.go`), so
  comparing the two compares a number with itself. Magnitude on old
  demos is neither validated by this work nor in doubt: it is the same
  direct observation as on the corpora above. What the delta side *can*
  report is COVERAGE — whether a hit that provably happened produced a
  delta at all.
- **Given-vs-taken symmetry is an identity, not a test.** `aggregate.go`
  credits every non-environmental event to exactly one attacker and one
  victim, so Σgiven ≡ Σtaken−takenEnv by construction. The harness checks
  it; it holds on every demo except where an attacker-less telefrag
  (`world` telefrag: `isEnv`, so `aggregatePositional` charges the
  victim's capacity to `taken` but no one to `given`, and `takenEnv`
  never sees it) opens a gap — 12 demos of 15 254, 1 in 1 270, gaps of
  81–450. The unattributed share therefore has to be read directly, as
  the share of bounded damage left on `weapon: "unknown"`.
- **Frag anchoring is circular until the anchor is withheld.**
  `attributeOne` reads the obituary first and stamps its killer and
  weapon, so a production run agrees with the frag log at kill instants
  100% of the time by construction — the `anchored` control below reads
  exactly 100.0% in every era. With
  `damagerecon.Options.WithholdObituaries` that branch and the telefrag /
  environmental / teamkill anchors go blind, and a killing hit is
  explained by the same geometry and damage-model scoring as the survived
  hits around it. THAT verdict, scored against the obituary, is a real
  measurement.

### Why the withheld run is a fair test of the shipped one

The obituary arrives on the `svc_print` channel; attribution reads
health/armor stat updates, entity flights, temp entities, fire sounds and
position tracks — no overlap. Delta EXTRACTION keeps its frag anchors
(the masked-death recovery in `deltas.go`), so both runs observe the same
instants at the same magnitudes: across 15 254 scored demos the harness
found 11 431 334 shared instants, 2 present only in the production run, 4
only in the withheld run and 7 with a differing bounded value — 13 in
11.4 million, all inside 2 E0 demos, and the only feedback path from
attribution back into a run is the second pass's rocket-regime detection
(`detectRocketRegime` reads the first pass's events). The withheld run
therefore differs from the shipped one in exactly one respect: it cannot
read the answer.

Three limits on the number that comes out, all pushing it DOWN:

1. **The label carries 0.1 pp of noise** — on instrumented demos the KTX
   log's own top-damage attacker at a kill instant equals the obituary
   killer 99.9% of the time (same-instant multi-attacker merges are the
   difference).
2. **Kill instants are the hard sub-population** (point-blank rockets,
   simultaneous fire), and they are precisely the instants the shipped
   code anchors instead of inferring.
3. **It certifies attribution, not magnitude**, and says nothing about
   the parts no obituary touches: team/self splits, the raw-family
   top-ups, `ewep` buckets.

### Measured

15 254 demos (54 min wall clock, 3 workers), 1 678 259 scored kills —
random samples of the 51k archive per era, drawn from the readability
census by `sample.py` (seed 20260819; a demo qualifies with ≥ 20 frags
and ≥ 2 players). `attacker` is the withheld run's top-damage attacker
at the kill instant vs the obituary's killer; `weapon` the same for the
weapon token; `delta` the share of scored kills carrying reconstructed
damage at all.

| era | demos | scored kills | delta | attacker | weapon | unattributed dmg |
|---|---|---|---|---|---|---|
| E0 qwsv/KTPro | 4 000 | 439 070 | 97.2% | **97.6%** | 97.9% | 1.08% |
| E1 mvdsv <0.25 | 132 | 19 097 | 100.0% | **98.0%** | 98.6% | 0.60% |
| E2 mvdsv 0.25–0.29 | 4 000 | 425 001 | 99.9% | **98.2%** | 98.8% | 0.75% |
| E3 mvdsv 0.30–0.33 | 2 000 | 196 155 | 100.0% | 98.3% | 99.0% | 0.85% |
| E4 mvdsv 0.34–0.36, no log | 1 202 | 138 812 | 100.0% | 97.6% | 98.5% | 1.10% |
| E4 mvdsv 0.34–0.36, GT | 1 950 | 176 697 | 100.0% | 96.8% | 97.5% | 0.75% |
| E5 mvdsv 1.x, GT | 1 970 | 283 427 | 100.0% | 96.3% | 97.0% | 0.58% |

**The un-established 40% is not the weak half.** E0–E2 score at or above
the GT-instrumented eras on the same measurement, and the ordering
survives the obvious confounder — team size, which is what actually moves
attribution:

| players | E0 | E2 | E3 | E4 (GT) | E5 (GT) |
|---|---|---|---|---|---|
| 2 (duel) | 98.8% | 99.2% | 99.1% | 98.8% | 98.4% |
| 3–5 | 97.3% | 98.1% | 98.3% | 97.1% | 96.5% |
| 6–8 (4on4) | 97.3% | 97.7% | 97.9% | 95.4% | 95.8% |

The sparse-telemetry premise is real but does not transfer to accuracy:
in 4on4s, E0 carries 0.27 TE_BLOOD per shot against E5's 1.48 — 5.5×
less — and attributes 1.5 pp BETTER. (The density gap is a team-game
phenomenon; duels sit at 0.02 blood/shot in every era, E5 included, so
the survey's population-level "10–50× sparser" reads that way only
because its stratified sample weighted modern 4on4s.)

Per attacker weapon, kills with a delta / attacker-correct:

| era | rl | lg | sg | ssg | gl | sng |
|---|---|---|---|---|---|---|
| E0 | 97.8% | 99.3% | 96.2% | 96.9% | 98.0% | 95.3% |
| E2 | 98.3% | 99.5% | 96.7% | 97.1% | 98.8% | 96.0% |
| E4 (GT) | 96.2% | 99.5% | 95.2% | 94.1% | 97.2% | 94.6% |
| E5 (GT) | 96.0% | 99.5% | 95.4% | 94.6% | 97.8% | 95.4% |

The expected weak spot — shotgun attribution on demos with almost no
TE_BLOOD — is not there: E0's sg reads 96.2% against E5's 95.4%.

### Calibration: what the oracle number means

On the 3 920 instrumented demos the oracle and the KTX log can be run
side by side:

| | E4 (1 950 demos) | E5 (1 970 demos) |
|---|---|---|
| production vs GT, all enemy instants | 99.1% | 98.8% |
| production vs GT, away from kills | 98.9% | 98.6% |
| production vs GT, at kill instants (anchored) | 99.9% | 99.9% |
| **withheld vs GT, at kill instants** | 96.8% | 96.3% |
| obituary vs GT (the oracle's own label) | 99.9% | 99.9% |
| **oracle metric (withheld vs obituary)** | 96.8% | 96.3% |

Two things follow. The oracle reproduces the withheld run's true
accuracy to within 0.1 pp, so it is a faithful measurement of what it
measures; and it understates the SHIPPED pipeline's attribution by
2.3–2.5 pp, because the shipped pipeline anchors those instants and
because kill instants are harder than average. E0–E2's 97.6–98.2% is
therefore a floor, not an estimate: on this scale the oldest eras are at
least as accurate as the eras ACCURACY.md was written from.

### The real per-demo risk: a silent stat channel

The one measurable era deficit is E0's delta coverage (97.2% of kills vs
99.9–100% everywhere else), and it is not a gradient — it is a small
class of broken recordings. Per-demo coverage is 100% at the median and
99.7% at the 5th percentile; 80 of 3 876 E0 demos (2.1%) sit under 50%,
carrying 83 bounded damage per kill where a healthy demo carries ~300.
They are qwsv recordings whose health/armor stat channel is barely
broadcast at all — positions and the frag log are intact, the damage is
simply not observable. **QWSV 2.30 is the concentrated case: 18 of the 23
sampled 2.30 demos are in that class (101 such demos exist in the whole
51k archive); on 2.40 it is 1.6%.**

The reconstruction is not wrong on these demos — it reports the damage it
could observe and nothing else — but the section is a fraction of the
real match, with no field saying so. A per-demo coverage figure on the
section is the obvious follow-up; it is deliberately NOT part of this
measurement pass.

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
  "no damage to armed enemies". It is common on every generation and is
  NOT an old-demo speciality: measured over the per-era samples, the bits
  are frozen on 18% of E0 demos, 5% of E1, 25% of E2, 39% of E3, 20–34%
  of E4 and 35% of E5.
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
not exact; raw overkill beyond the -99 corpse clamp is model-derived on
killing hits.

**These hold on every server generation, not only the instrumented
half** (§per-era validation, 15 254 demos): the oldest eras measure at or
above the eras this document's headline tables were written from, so
there is no weaker per-era tier and no reason to gate the section by
server version. The axis that moves attribution is the fight, not the
era — duels 98.4–99.2%, 4on4s 95.4–97.9%, in every era. Two things ARE
era-shaped, and both are already handled by withholding rather than
guessing: recorders freeze the StatItems weapon bits, so `ewep` and the
`enemyVs*` buckets are unmeasurable on 5–39% of demos — and MORE often
on the newer ones (18% at E0, 39% at E3, 35% at E5; §old-recorder
degradations) — and a small class of qwsv recordings
(2.1% of E0, concentrated on QWSV 2.30) barely broadcasts the health
stat channel at all, leaving a section that reports only the fraction of
the match the wire showed. Arena-family maps (povdmm4/dmm4*/anarena/midair-style) and
CTF were out of validation scope (study §trust tiers); midair/instagib/
dmgfrags server modes are skipped entirely (no section).
