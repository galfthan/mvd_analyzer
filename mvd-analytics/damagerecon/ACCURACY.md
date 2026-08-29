# damagerecon accuracy — validated against KTX ground truth

This package reconstructs the damage section for demos that predate the
KTX `mvdhidden_dmgdone` instrumentation. Accuracy is measured on modern
demos that carry BOTH the state streams and the real damage stream: the
reconstruction runs blind (it never reads `res.Damage` or any
damage-derived field) and is scored against the KTX log.

Reproduce with:

```
MVDA_BSP_DIR=./bsps go run ./mvd-analytics/cmd/qw-recon-eval           # metrics
MVDA_BSP_DIR=./bsps go run ./mvd-analytics/cmd/qw-recon-eval -diag    # + class totals and misattribution flows
```

over the golden-corpus cache (`mvd-analytics/testdata/cache/`, 13 demos:
4 duels, 3 2on2, 6 4on4, populated by the analyzer golden test's first
run). A fast regression subset is pinned in `eval_test.go` (runs in
`make test`, skips without the cache).

## Headline numbers (2026-08-24, after the engine-order corrections)

Per-player match totals, relative error vs the KTX log. Golden cache
(13 demos, 75 player rows with GT ≥ 200):

| metric | median | mean | p90 | ≤1% | ≤2% |
|---|---|---|---|---|---|
| bounded given | 0.58% | 0.81% | 1.95% | 72% | 91% |
| bounded taken | 0.05% | 0.21% | 0.52% | 96% | 100% |
| raw given | 0.65% | 1.04% | 2.13% | 68% | 89% |
| raw taken | 0.48% | 0.79% | 1.63% | 79% | 95% |
| bounded ewep | 0.84% | 1.39% | 3.08% | 55% | 75% |
| bounded givenTeam | 2.5% | 5.1% | — | small denominators (200–700) |
| bounded givenSelf | 1.5% | 4.0% | — | small denominators |

The larger blind corpus (60 fresh dm2/dm3 hub demos, 321 rows — raw
eval outputs in `.reports/quad-splash-2026-08-24/`, an untracked
per-machine report directory; fetched with `cmd/fetch-eval-corpus`)
scores comparably: bounded given median **0.57%** (mean 0.76%, p90
1.62%, ≤2% 94%), bounded taken 0.04%, raw given 0.70%, raw taken 0.48%.

Event level (60-demo corpus): 99.6% of ground-truth damage instants have
a same-instant reconstructed delta; 98.9% of those match the bounded
value exactly; attacker attribution is 98.5% on unambiguous enemy
instants (rl 99.7%, lg 99.5%, sg 97.9%, ssg 98.1%, gl 99.7%, axe 98%).
The same run also scores the direct/splash verdict per rl explosion
against the wire's own flag — see §"Can an old demo answer KTX's rl/gl
question?".

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
99.9–100% everywhere else), and it is overwhelmingly a small class of
broken recordings rather than an era-wide gradient. Per-demo coverage is
100% at the median and 99.7% at the 5th percentile; 84 of 3 968 scoreable
E0 demos (2.1%) sit under 50%, carrying 83 bounded damage per kill where
a healthy demo carries ~300. They are qwsv recordings whose health/armor
stat channel is barely broadcast at all — positions and the frag log are
intact, the damage is simply not observable. **QWSV 2.30 is the
concentrated case: 18 of the 23 sampled 2.30 demos are in that class (101
such demos exist in the whole 51k archive); on 2.40 it is 1.6%.** A
further 18 E0 demos (0.45%) are PARTIALLY broadcast rather than silent —
see the band in §per-demo coverage below.

The reconstruction is not wrong on these demos — it reports the damage it
could observe and nothing else — but the section is a fraction of the
real match, with no field saying so. **Resolved in schema v74: it now
says so, as `damage.coverage`** — see the next section.

### Per-demo coverage: `damage.coverage` (schema v74)

The oracle's kill-delta coverage is cheap enough to run in the normal
pass, so it ships as a field. `result.DamageCoverage` on every
RECONSTRUCTED section carries `{kills, covered, ratio}`: `kills` is the
frag log's weapon kills that carry damage arithmetic (enemy kill, both
players on the roster, telefrags and stomps excluded — they aggregate
outside `events`), `covered` the ones whose lethal instant the
reconstructed `events` log accounts for. Computed in
`damagerecon/aggregate.go` (`setCoverage`) on the same denominator
`cmd/qw-recon-oracle` scores, so the harness number and the shipped
number are the same number; the obituary withhold does not reach it
(coverage is a property of delta EXTRACTION, which keeps its frag
anchors either way — pinned in `coverage_test.go`).

**Measured over the FULL oracle sweeps** — every scoreable demo of the
E0–E4 reconstructed runs in `.reports/qw-recon-oracle-2026-08-19/`
(`killsDelta / killsScored` per row of `oracle-E*-recon.csv`), 10 702
demos, `MVDA_BSP_DIR=./bsps` throughout. Rescored 2026-08-23; the v74
figures came from a 200-per-era subsample that missed the tail below.

| population | n | share | min | p1 | median | max |
|---|---|---|---|---|---|---|
| **the core**, ratio ≥ 0.95 | 10 597 | 99.02% | 0.950 | 0.975 | **1.000** | 1.000 |
| **the silent-channel class**, ratio < 0.50 | 86 | 0.80% | 0.000 | 0.000 | **0.182** | 0.488 |
| **the band between them**, 0.50 ≤ ratio < 0.95 | 19 | **0.18%** | 0.500 | — | 0.657 | 0.944 |

Per sweep:

| sweep | n | min | p0.5 | p1 | p5 | median | < 0.95 | in the band | < 0.50 |
|---|---|---|---|---|---|---|---|---|---|
| E0-recon | 3 968 | 0.000 | 0.097 | 0.176 | 0.997 | 1.000 | 102 (2.57%) | **18** | 84 |
| E1-recon | 129 | 0.997 | 0.997 | 0.997 | 1.000 | 1.000 | 0 | 0 | 0 |
| E2-recon | 3 672 | 0.243 | 0.970 | 0.974 | 1.000 | 1.000 | 3 (0.08%) | **1** | 2 |
| E3-recon | 1 806 | 0.952 | 0.967 | 0.971 | 0.998 | 1.000 | 0 | 0 | 0 |
| E4-recon | 1 127 | 0.963 | 0.973 | 0.977 | 1.000 | 1.000 | 0 | 0 | 0 |
| E4-gt (recon on GT-carrying demos) | 1 806 | 0.955 | 0.967 | 0.971 | 1.000 | 1.000 | 0 | 0 | 0 |
| E5-gt (recon on GT-carrying demos) | 1 828 | 0.958 | 0.967 | 0.971 | 1.000 | 1.000 | 0 | 0 | 0 |

**The shape is a hard bimodal core plus a thin gradient tail.** 96.1% of
the core reads exactly 1.000 and the modes are far apart — but they are
not exhaustive, and the earlier "the two populations do not overlap"
claim was an artefact of the subsample. 19 demos (0.18% of the sweep;
0.454% of E0, where all but one of them live) sit in the band the claim
called empty, at

```
0.500 0.524 0.526 0.545 0.548 0.556 0.571 0.588 0.643 0.657
0.712 0.750 0.757 0.805 0.840 0.898 0.916 0.917 0.944
```

on denominators of 18–287 kills — not a handful of one-kill artefacts.
16 of the 19 are duels; by version, 12 qwsv 2.40, 6 qwsv 2.30, 1 MVDSV
0.28b (the 0.916, a 287-kill dm3 4on4). So a `ratio` of 0.7 is a real
outcome a consumer will eventually meet, and it is neither "healthy" nor
"the broken class": it is a partially-observed match, and the number is
the fraction that was observed. **Read the ratio as a magnitude, not as a
two-valued flag.** No threshold is written into the code — a consumer
that wants one has the percentiles above; the pipeline publishes the
number and filters nothing.

**What the ratio cannot see.** The denominator is the frag log, so a loss
that takes the obituaries WITH the damage evidence — a recording that
starts late, an mvd stream with a hole in it, a demo cut short — removes
kills from `kills` and from `covered` together and reads a clean 1.000
over the fraction that survived. Coverage answers "how much of the
frag-log-visible match is in this section", which is the silent-stat-
channel question it was built for; it is not a completeness check on the
recording itself. Cross-check that against the match clock and the
scoreboard (`demoinfo`, `//finalscores`), not against this field.

**It also does not localize.** One scalar covers the whole demo, and a
mid-band duel is as consistent with "both players half-observed" as with
"one dead victim channel beside one perfect one" — the reconstruction
reads health/armor per VICTIM, so a single unbroadcast player can halve a
duel's ratio on its own. Per-victim coverage is the natural follow-up and
is recorded as a lead in `plan-archive-features.md`; until then, treat a
mid-band figure as "somewhere in this match the evidence was missing",
not as a uniform discount on every player's row.

The rejected alternative was bounded damage per kill, the other figure
the outlier study quoted (~83 vs ~300). It does NOT separate: on the
1 209-demo v74 subsample the healthy side ran down to 107 and the silent
class up to 326, i.e. the two overlap outright rather than meeting in a
thin band. Coverage is the discriminating measurement, so it is the only
one published — and per-kill damage stays derivable from the section
anyway.

**Is the figure honest?** Two controls on demos that carry the KTX log:

- **Ceiling.** Scoring the same metric over the WIRE damage section reads
  exactly **1.000 on all 65** GT demos (52 scoreable of the 60-demo
  dm2/dm3 corpus — 7 are `skipped:*`, 1 has no scoreable kill — plus the
  13 golden-cache demos), and the blind reconstruction of those same
  demos also reads **1.000 on all 65**. So a complete section reads full
  coverage from either evidence source, and the metric adds no floor of
  its own. This is why a `source: "ktx"` section carries no `coverage`:
  it would be a constant.
- **Sensitivity.** Thinning the health/armor change streams to one sample
  in four — the silent-channel failure, with every other input intact —
  drops the figure to a 0.351 median (dm2/dm3 corpus; 0.337 on the golden
  cache, min 0.282, max 0.532), i.e. into the silent class's own band,
  while `kills` stays put. Coverage cannot hide a loss by shrinking its
  own denominator. Both directions are pinned in `coverage_test.go`.

Riders inherit rather than restate: the `playerStats` damage family
(`src: "reconstructed"`), its reconstructed `accuracy.hits`, and
`aim.players[].weapons[].recon.hits` are all built from this section, so
their coverage IS `damage.coverage` and their docs point at it instead of
carrying a copy.

## Aim hit recovery (2026-08-19, rl/gl added 2026-08-23)

A reconstructed damage log is also enough to answer "did this fire
connect", which is what `aim` needs. `aimcore` re-runs the fire→damage
join against it and publishes the result in its own block —
`aim.players[].weapons[].recon.hits`, beside the wire-measured
`hits` that stays withheld (`aim.hitsMeasured` is still false; the new
`aim.hitsSource` names which evidence the tier came from). Scored with
`cmd/qw-aim-eval`:

```
MVDA_BSP_DIR=./bsps go run ./mvd-analytics/cmd/qw-aim-eval \
    -dir /data/eval-corpus-dm2dm3 -workers 6
```

Withhold-and-compare on demos that carry the KTX log: parse once, keep
the measured aim, replace the damage section with this package's blind
reconstruction of the same match, recompute aim, and pair the two per
player per weapon. Because the join is also run against the WIRE log
(the `join-on-wire` control), the method's own error and the
reconstruction's error are separated instead of summed.

53 scored demos of the 60-demo dm2/dm3 corpus (7 are `skipped:*` modes),
rows with ≥ 20 fires; Δacc is reconstructed accuracy − measured accuracy
in percentage points:

| weapon | rows | shots | measured acc | recon acc | med \|Δacc\| | mean \|Δacc\| | bias | ≤2pp | control (join on the wire log) |
|---|---|---|---|---|---|---|---|---|---|
| lg | 169 | 18 500 | 33.1% | 32.9% | 0.0pp | 0.3pp | −0.2pp | 96% | **exact** |
| sg | 307 | 81 203 | 51.8% | 50.4% | 1.1pp | 1.3pp | −1.3pp | 79% | **exact** |
| ssg | 86 | 4 339 | 71.9% | 70.2% | 0.0pp | 1.6pp | −1.6pp | 69% | **exact** |
| axe | 18¹ | 1 417 | 8.0% | 7.8% | 0.0pp | 0.6pp | −0.6pp | 83% | 0.1pp |
| rl | 303 | 35 135 | 47.9% | 48.3% | 0.0pp | 0.5pp | +0.2pp | 94% | +0.4pp |
| gl | 109 | 4 948 | 15.0% | 15.0% | 0.0pp | 0.4pp | −0.0pp | 93% | +0.1pp |

¹ axe at the ≥ 20-swing threshold like every other row; the wider ≥
10-swing view (38 rows) reads the same to a tenth of a point. The golden-corpus cache (13 demos) reproduces the
shipped rows within 0.4pp (lg 0.1pp, sg 1.4pp, ssg 1.0pp, axe 0.0pp,
rl 0.5pp, gl 0.1pp).

The ng/sng rows are scored by the harness itself — no source edit needed
— through `aimcore.ReconHitsForEval`, which runs the join for every
weapon rather than only the shipped ones: what a weapon's join costs is
the measurement that DECIDES whether it ships.

**Shipped: lg, sg, ssg, axe** — the weapons whose damage lands in the
fire's own server frame (the axe at its fixed +200 ms traceline delay).
On those the control is exact to the last row — the join reproduces the
measured counters from the wire log with zero error — so the whole
residual is the reconstruction's, and it is ≤1.6pp mean with a small
negative bias (a hit whose delta the reconstruction attributed elsewhere
is a lost hit; there is no mechanism that invents one).

**Shipped: rl, gl (schema v74)** — after the projectile join was made
the same QUESTION as the measured counter. It was not, until v74. The
measured `hits` counts fires whose TRACKED FLIGHT linked to an impact
(`analyzer/shots.go linkProjectiles`), so a point-blank rocket whose
entity never broadcast is measured as a miss; the v73 join could only
count reconstructed impacts, which correctly calls that same rocket a
hit. Neither number was wrong and they were not comparable: rl read
+7.4pp against the measured counter with the control at +7.3pp — the
same error, i.e. a method difference, not reconstruction error (gl read
+1.0/+1.0, the identical weakness at grenade scale).

v74 publishes the association the shots analyzer used to discard, as
`shots[].flightEnd`, and the join now anchors on the fire's own flight:
the flight's impact instant against the reconstructed damage of that
attacker+weapon, within damagerecon's own projectile tolerance
(`attribution.go` tolProjBeforeMs/tolProjAfterMs = the measured −81/+261
despawn-to-stat-instant lag), one instant claimed per flight and each
claimed instant consumed. A fire with no tracked flight links to
nothing, exactly as the wire join treats it. The result: rl mean error
7.4pp → **0.5pp** (bias +0.2, 94% of rows within 2pp), gl 1.3pp →
**0.4pp**, both inside the ≤1.6pp band the hitscan tier ships at.
(v73 measured rl at 0.6pp and gl at 0.3pp; the v74 direct-impact work
moved the damage model — see §"The rocket splash base is 120" — and the
engine-order corrections below moved it again, from rl 0.7pp / bias
+0.4pp to these.)

The **control residual** is +0.4pp on rl (+0.1 gl) rather than the
hitscan set's exact 0.0, and it is one-sided by construction: the
reconstruction's damage rows include hits by rockets whose flight was
never tracked, and the recon window is wide enough (up to +261 ms) that
a nearby tracked flight can adopt such a row. That is the price of
anchoring the window on the tolerance the damage log was BUILT with
rather than on the wire join's ±34 ms frame — a tighter window would
flatter the control and lie about the reconstruction, whose damage
genuinely lands late.

**Map scope of these figures.** The ground-truth corpus is 30 dm2 + 30
dm3 demos and nothing else, so the rl/gl numbers are measured on two
open maps. Both known routes into the +0.2pp adoption bias should run
HIGHER elsewhere, i.e. treat +0.2pp as a floor rather than a bound:
tight-quarters maps (dm4, aerowalk) produce more point-blank rockets,
whose entity the server never broadcasts — the untracked flights whose
damage rows are exactly the adoption fodder; and a SURVIVED lava tick
(10–30, overlapping moderate rocket splash) is not obituary-anchored the
way a lethal one is, so where the attribution prefers a rocket to the
env candidate the row can be adopted by a missed flight nearby. The 6%
of rl rows outside 2pp is plausibly this population, and it is one-sided
(+): both mechanisms invent hits, neither destroys one.

Bounded, not confirmed: the 13-demo golden cache spans dm4, dm6, e1m2,
aerowalk, obsidian, schloss, skull and bravado besides dm2/dm3, and
reproduces rl at bias +0.2pp / mean 0.5pp and gl at −0.1pp / 0.1pp —
indistinguishable from the dm2/dm3 figures. At 73 rl rows that is too
small to bound the effect, but it does say it is not large on those
maps.

**Withheld: ng, sng** — for a stronger reason than a gap: there is no
ground truth to compare against. Nails link through the same flight
bracket as rockets but only when nail tracking is enabled, so the
measured counter is 0 on every row of this corpus (8 961 sng and 3 776
ng fires, zero measured hits) and `flightEnd` is absent for them on a
default parse. The impact-counting join the harness still runs for them
(their link window is the spike's +6 s lifetime, `ktx/src/weapons.c`
:1471 — deliberately the loosest bound physically possible) recovers
18.8% / 19.9% accuracy from the reconstructed log and 19.2% / 20.4%
from the wire log — numbers with nothing to validate them, which is
exactly what a shipped tier may not be.

Everything except the hit COUNTS is withheld on a reconstructed section —
per-fire `hit` columns, the pellet split, the per-fire
direct/splash/missed split, the LG whiff geometry, the enemy/team/self
slices. (The aggregate direct-impact COUNT is carried, as
`recon.directHits` for rl/gl — a different and separately validated
claim; see §"Can an old demo answer KTX's rl/gl question?".) The reasons are per-field and
documented on `result.WeaponAimRecon`; they all come back to the same
two properties of the log: it is anchored at the victim's stat instant,
and several hits landing on one instant merge into one delta with one
attacker and one summed magnitude.

## The whole derived summary vs the verbatim KTX block (2026-08-23)

The recon tier above is scored against THIS pipeline's own measured
counter. A second question is what the derived per-player summary
(`playerStats`) is worth against the thing it stands in for — the KTX
demoinfo block that 54% of the archive has no copy of. `cmd/qw-demoinfo-eval`
answers it on the same withhold-and-compare protocol: parse an
instrumented demo once, keep its verbatim block as ground truth, swap
the wire damage log for the blind reconstruction, recompute aim, and
re-derive the two families that ride damage
(`analyzer.DerivedStatsForEval`). Score is untouched by the swap and is
already the blind answer, so it is compared as stored.

Over **188 archive demos, 665 player rows**:

| field | rows | exact | aggregate error |
|---|---|---|---|
| `score.frags` | 665 | 99.5% | 0.02% |
| `score.deaths` | 665 | 99.7% | 0.01% |
| `score.teamKills` | 665 | 99.5% | 0.40% |
| `score.kills` | 665 | 95.5% | 2.26% |
| `score.maxQuadSpree` | 665 | **99.8%** | 0.31% |
| `score.maxSpree` | 665 | 92.9% | 3.12% |
| `damage.given` (reconstructed) | 665 | 5.9% | **0.45%** |
| `damage.takenEnemy` (reconstructed) | 663 | 9.2% | **0.42%** |
| `pickups.quad/pent/ring.took` | 273/80/78 | **100.0%** | 0.00% |
| `hold.powerups.quad.ms` (to the second) | 273 | 96.0% | 0.07% |
| `hold.powerups.pent.ms` | 80 | 82.5% | 0.49% |
| `hold.powerups.ring.ms` | 78 | 94.9% | 0.09% |
| `accuracy.lg.attacks` | 436 | 98.4% | 0.00% |
| `accuracy.rl.attacks` | 638 | 99.8% | 0.00% |
| `accuracy.gl/ng/sng.attacks` | 424/139/336 | 100.0% | 0.00% |
| `accuracy.lg.hits` (recon tier) | 436 | 63.8% | **0.87%** |
| `accuracy.rl.hits` (recon tier, directImpact — v74) | 638 | 46.9% | **1.34%** |
| `accuracy.gl.hits` (recon tier, directImpact — v74) | 424 | 89.6% | **3.55%** |
| `accuracy.axe.hits` | 86 | 97.7% | 12.50% |

The powerup-seconds rows are all **one-directional**: every mismatch on
all three powerups is exactly −1 s, never +1. Both sides truncate to
whole seconds (KTX casts its float in `json_item_detail`; the harness
floors ours to match), and our interval is read off the demo-frame grid
at both ends, so a run can measure up to one frame (~34 ms at
`sv_demofps 30`) short — which costs a whole second exactly when the
true length is an integer number of them. An expiry-ended run is
precisely that. KTX's 96 pent takes on this population sum to 96 × 30 s
*exactly*, i.e. not one pent run ended early, so every take is exposed
and 14 lose the second (14.6%); quad runs average 20.0 s per take and
lose it on 1.4%, ring 21.7 s and 4.1%. Pent is the worst row because
pent holders do not die, not because possession is measured worse there.

`maxSpree`'s residual decomposes completely. Conditioned on the row's
`kills` already agreeing with KTX it is 96.5% exact; conditioned
additionally on the player never suiciding, **99.6% (252/253)**. 21 of
the 22 mismatches in the first subset belong to players with at least one
suicide, and all 22 are off by exactly −1 — KTX's increment gate is
`strneq(attackerteam, targteam) || !tp_num()` (`ktx/src/client.c:4865`),
so wherever teamplay is off a suicide bumps the player's own streak in
the very call that latches it, and this pipeline counts only the kills
`score.kills` counts. The `spree.max/ktxConvention` column replays that
gate instead of ours and reproduces the block on **all 22** of them
(98.8% of all 665 rows), which is what makes the residual a definition
rather than an argument. 16 of the 17 rows at |Δ| ≥ 3 sit where the frag
log had already credited the player 0 kills against KTX's 8–47: the
streak inherits the kill side's residual by construction.

That column reads what it reads only because it reads `tp_num()`
properly. KTX gates the teamplay cvar on the mode (`isTeam() || isCTF()
|| coop`, `ktx/src/g_utils.c:1586`); proxying it with "more than one
team name in the roster" put every clan-tagged duel on the wrong side of
the gate and the column scored 93.4%. Against the mode string the block
itself carries, it scores 98.8%, and the decomposition above is measured
on that.

Two same-instant mutual frags also lived in this residual until the
replay started ordering same-millisecond events by the **frag log**
rather than by event kind (`analyzer/spree.go`). The whole state machine
runs inside `ClientObituary`, so the log's order is its order; ranking
every kill at an instant ahead of every death credited a posthumous
rocket to the run it had already ended. 55 such pairs over the first 500
archive demos, in 43 of them; on this population the fix took
`maxSpree` from 92.6% to 92.9% overall, removed both +1 outliers from
the conditioned subset, and moved exactly one golden row.

(The `axe.hits` row is 86 rows and a bias of +0.02 per row — one row
moved between the v73 and v74 measurements, on a denominator small
enough that its aggregate swings by 6 points when it does.)

**Two fields are deliberately NOT scored as agreement**, because the two
sources are not measuring the same quantity, and the eval reports them
only to pin the size of the gap:

- `accuracy.sg/ssg.attacks` and `.hits` — on a RECONSTRUCTED row (which
  is what the un-suffixed columns score) KTX counts PELLETS on both sides
  of its ratio and that tier counts trigger pulls and fires that
  connected. The observed ratios (83% / 93% aggregate) are exactly the
  6-and-14-pellet spreads. The WIRE-linked row closed this in v75 — see
  "The wire-linked accuracy family" below; the reconstructed one cannot,
  because a merged reconstructed delta's magnitude is the sum over every
  hit on that instant and dividing it by 4 would credit one shooter with
  another's pellets (`result.WeaponAimRecon`).
- `accuracy.rl/gl.anyDamage/recon` — the convention a reconstructed row
  no longer publishes for those two, kept as a diagnostic: a fire that
  landed damage by ANY path, which reads ~4x above KTX on rl (362%
  aggregate) and ~1.5x on gl (54%). Until v74 that WAS the published
  number and the gap was named rather than closed; the next section is
  how it was closed.

### Can an old demo answer KTX's rl/gl question? (2026-08-24)

Yes, since v74 — after one refutation and one rebuild. The harness
measures every step.

**The convention is confirmed exactly.** `dmg_is_splash` is raised only
inside `T_RadiusDamage`'s loop (`ktx/src/combat.c:1207-1227`), so the
direct `T_Damage` in `T_MissileTouch` reaches the wire unflagged — and
KTX's `hits++` sits in that same touch handler
(`ktx/src/weapons.c:990-996`). Counting the wire log's non-splash `rl`
rows therefore ought to BE the block's `acc.rl.hits`, and it is: **638
of 638 player rows exact, 0.00% aggregate** (`acc.rl.direct/wire`).
Nothing about that count is inference.

**The wire flag is what the old half does not have**, so the flag itself
has to be reconstructed — after which the same row count applies.

**Refuted (2026-08-23): explosion endpoint within 48 units.** The first
substitute was the endpoint proximity `damagerecon` already used to pick
its damage-model branch, joined through the shipped tier's flight
bracket. It answered `gl` and not `rl`:

| column | rows | exact | aggregate | mean per-row accuracy error |
|---|---|---|---|---|
| `acc.gl.direct/recon` | 424 | 71.7% | **1.22%** | 2.24 pp |
| `acc.rl.direct/recon` | 638 | 10.0% | **+80.1%** | 8.32 pp |

A grenade is contact-fused, so where it detonates IS where it touched. A
rocket is not: one detonating on the wall BESIDE a player is
endpoint-near without ever having touched them.

**Shipped (2026-08-24, schema v74): trajectory + magnitude**
(`direct.go`). Two engine facts replace the proximity test, and they
disambiguate each other:

1. **Trajectory against the hull.** The projectile is a zero-size point
   entity and the player hull a fixed 32×32×56 box
   (`ktx/src/weapons.c:1083`, `client.c:34-37`), so a touch is exactly a
   point entering that box. A rocket flies a straight line at 1000 ups,
   so the tracked flight's spawn and despawn origins determine the whole
   trajectory — including the stretch past the last broadcast position,
   which is where the touch happened. The line is followed FORWARD from
   the detonation point by 44 units (the engine's 8-unit explosion
   pull-back, `weapons.c:1008`, plus one server frame of travel) and not
   backward at all: a touch detonates the rocket where it touched, so the
   hull is always ahead of the reported point.
2. **Magnitude.** A direct rocket deals a flat constant and takes NO
   splash on top of it — `T_MissileTouch` hands the victim to
   `T_RadiusDamage` as the `ignore` entity (`weapons.c:998-1006`) —
   while splash is `120 − 0.5·dist`. On a fixed-constant server the two
   curves cross at one distance, and the observation is decisive: over
   3 275 wire `rl` rows on the dm2/dm3 corpus, **623 of 623 direct rows
   read exactly 110 (or 440 quadded) and exactly one splash row did**.
   The reconstruction reads the delta's RAW value, which for a survived
   hit IS the wire value (health drop + armor drop = `take` + `save`,
   exact for an integer damage, `combat.c:634-655`), so a survived hit
   whose raw is not the constant cannot have been a lone touch. On a
   killing hit the −99 corpse clamp breaks that guarantee and the
   trajectory keeps the last word.
3. **The spent grenade fuse** (`grenadeFuseExpired`), for `gl` only.
   `GrenadeExplode` runs on a 2.5 s think (`weapons.c:1434`); a grenade
   whose own broadcast flight spans that whole fuse and ends in a
   matched `TE_EXPLOSION` therefore died of the fuse, which means
   `GrenadeTouch` — the only place KTX's `gl` counter increments — never
   ran. A certain non-touch, whatever the detonation point's proximity
   says. See "the fuse, both directions" below for why only this
   direction of the signal survives.

**The direct-damage constant is era-dependent, and the era is measured
rather than assumed.** KTX has dealt a flat **110** since commit
`c7263e8f` (2008-09-29, qqshka, "this way it better (tm)"), which
replaced id1's `100 + g_random()*20`; v1.35 and earlier still roll it,
v1.36 (2009-09-24) and later do not. `detectRocketRegime` decides per
demo from the demo's own hit distribution, so no version string is
consulted and a pre-1.36 recording simply falls back to the trajectory
alone, where the prior says nothing.

That verdict is **three-valued**, and published as
`damage.rocketDirectRegime`, because "no constant" was two different
claims wearing one face: `fixed` (the demo's near-direct hits clustered
on 110), `spread` (there were enough of them to test and they did NOT
cluster — what a pre-1.36 server looks like, and evidence against the
constant rather than the absence of evidence) and `unestablished`
(fewer than six such hits, so the question was never put). The split is
not cosmetic — the three populations score differently, table below.

**One thing the trajectory does NOT need**, refuted by measurement
rather than by argument and documented at the constant it would have
needed in `direct.go`: an EXCLUSIVITY pass across an explosion's victim
set. The invariant is real — one explosion touches at most one player —
but the wire violates it 0 times and this classifier 2 times in 3 525
explosion groups.

**Per-explosion accuracy**, against the wire splash flag, over the
53-demo dm2/dm3 ground-truth corpus (`cmd/qw-recon-eval`, the
direct/splash block). `rl` only: the wire has no gl ground truth to
score against, since `GrenadeTouch` does all its damage through
`T_RadiusDamage` and every wire gl row is splash-flagged whether or not
the grenade touched anybody.

| | paired instants | classification accuracy | precision | recall | direct-count error |
|---|---|---|---|---|---|
| endpoint within 48 u | 18 032 | 73.5% | 45.1% | 96.7% | +114% |
| trajectory + magnitude | 18 026 | **97.9%** | **94.6%** | **95.8%** | **+1.3%** |

**Per-player accuracy**, against the verbatim KTX block on the 188
instrumented archive demos (`cmd/qw-demoinfo-eval`, withhold-and-compare).
Since v74 a reconstructed row PUBLISHES the direct-impact count for
rl/gl, so the shipped comparison is `acc.rl.hits` / `acc.gl.hits`
themselves; `anyDamage/recon` is the convention those rows no longer
carry, kept in the harness so the gap stays measured.

| column | rows | exact | aggregate | bias / row | p90 error |
|---|---|---|---|---|---|
| `acc.rl.hits` (directImpact, shipped) | 638 | **46.9%** | **1.34%** | −0.14 | 2 |
| — `rocketDirectRegime: "fixed"` | 567 | 45.9% | **0.73%** | −0.08 | 2 |
| — `rocketDirectRegime: "spread"` | 16 | 31.2% | 13.0% | −0.94 | 3 |
| — `rocketDirectRegime: "unestablished"` | 55 | 61.8% | 22.2% | −0.47 | 1 |
| `acc.rl.direct/wire` (control) | 638 | 100.0% | 0.00% | 0.00 | 0 |
| `acc.rl.anyDamage/recon` (not published) | 638 | 1.9% | 361% | +36.8 | 69 |
| `acc.gl.hits` (directImpact, shipped) | 424 | **89.6%** | 3.55% | −0.07 | 1 |
| `acc.gl.anyDamage/recon` (not published) | 424 | 43.2% | 54% | +1.04 | 3 |
| `acc.lg.hits` (the shipped benchmark) | 436 | 63.8% | 0.87% | −0.91 | 3 |

The `gl` row's two figures point in opposite directions and both are
real; the fuse section below is where that is unpacked. The three
regime rows are why the verdict is published as three values rather
than as the constant's presence: `spread` — the demos whose own hits
argue against the fixed constant — is by some way the weakest
population (31.2% exact, −0.94 per row), while `unestablished` is the
strongest (61.8%) and only reads a big aggregate because its
denominators are small. Collapsing them, as the pre-fix field did,
averaged a population that behaves twice as badly into one that behaves
better than the pooled row.

Both weapons stay at or under a hit-and-a-bit of per-row error — `rl`
at −0.14, `gl` at −0.07, against the `lg` benchmark's −0.91 — which is
the bar this family ships at, so both projectile weapons publish
`hitsConvention: "directImpact"` on a reconstructed row and the
`byWeapon` map holds one convention per weapon, exactly as a KTX row
does. A `derived` row (wire damage log, no KTX block) keeps `anyDamage`
on rl/gl: its `hits` is also the aim section's MEASURED counter, a
validated any-path number that nothing asked to redefine.

**How much the magnitude prior carries, and what happens without it.**
`detectRocketRegime` establishes the constant on 567 of the 638 rows
here, and the reconstruction publishes the verdict as
`damage.rocketDirectRegime` so a consumer can see which population a row
is in. The split above is the reason nothing is GATED on it: the 55
`unestablished` rows are the LOW-ROCKET ones — a demo needs several
near-direct hits before the constant is measurable — and they are the
*most* often exact of the three (61.8%) with the smallest p90 error, the
22.2% aggregate being small absolute errors over small counts.
Withholding there would substitute the any-path count, which is four
times KTX's. The 16 `spread` rows are the weak ones, and the field is
what lets a consumer find them without withholding anything.

That is still not the same population as a genuinely PRE-1.36 server,
which this corpus cannot contain: every demo carrying a wire damage log
post-dates the fixed constant by years, so the vanilla `100 +
g_random()*20` era has no ground truth anywhere. Forcing the prior off —
the closest available proxy — moves rl from 0.82% to **13.9%** aggregate
on a 59-demo subset of this population.

Read 13.9% as a **proxy and a floor, not a bound**. The proxy population
is modern demos with the prior switched off, so it exercises the loss of
the *signal* and nothing else. A real pre-1.36 server ran an extra
mechanism this population cannot reproduce: its direct damage is `100 +
g_random()*20`, so a direct hit can read **below** the close-range
splash envelope (a 100-point direct is exactly what a splash at 40 units
delivers), and there the geometry and the value point at each other's
answers. The 110-constant proxy never produces such a row — every direct
in it reads the one value the prior would have recognised — so whatever
that mechanism costs is on top of the 13.9%. `rocketDirectRegime:
"spread"` is the flag for the rows where it might be biting, and their
31.2%-exact / −0.94-per-row score is the only direct evidence we have
about them.

**The derivation is a row count, not a join.** One projectile touches at
most one player, so a touch IS a direct damage row: `aimcore.ReconDirectHits`
counts the reconstructed log's non-splash rl/gl rows per attacker — the
same arithmetic `acc.rl.direct/wire` runs on the wire log, which is what
makes the eras comparable. Routing it through the fire→flight join that
produces `recon.hits` instead measured **9.5%** aggregate error against
the block, because that join treats a rocket whose entity the server never
broadcast as a miss and KTX's touch counter does not. `directHits` is
therefore not a subset of `hits` and may exceed it.

(`acc.gl.direct/wire` is in the table above at 100% under-count. That is
not a defect: `GrenadeTouch` counts the touch and then does ALL its
damage through `GrenadeExplode` → `T_RadiusDamage`, so a direct grenade
touch leaves no non-splash row on the wire either. The wire's FLAG answers
rl's question and not gl's; this classifier answers both, on either era's
log — since 2026-08-29 a modern demo's gl row is classified the same way,
see §"The wire-linked accuracy family vs the verbatim block".)

**The grenade fuse, both directions** (2026-08-24). The fuse is a
2.5 s think (`weapons.c:1434`) and only a player touch detonates a
grenade early, so the signal looks symmetric and is not. Our flight
bracket is entity VISIBILITY, and a grenade leaving the spectator's PVS
ends its bracket without ending its life — a confound that can only make
a flight look SHORTER than it was.

- **Positive direction — refuted.** "A flight ending early ended on a
  player" reads every PVS exit as a touch. Measured, it moved the derived
  gl counter from 0.36% to 3.57% aggregate error for 1.4 pp of exact rows
  on 149. Not shipped.
- **Negative direction — shipped.** "A flight spanning the WHOLE fuse and
  ending in a matched `TE_EXPLOSION` died of the fuse" is immune to that
  confound by construction, since the confound cannot lengthen a bracket.
  It is the certain half: `GrenadeExplode` ran on the think, so
  `GrenadeTouch` never ran, so KTX's counter never incremented.

The threshold is the fuse minus two demo frames (both bracket ends land
on the ~34 ms broadcast grid, and both round inward), i.e. 2 400 ms, and
the sweep lands on exactly that knee — false positives are all gone by
2 400 and only genuine late-fuse touches are lost below it:

| `grenadeFuseObservedMs` | exact | rows over | rows under | mean abs err | aggregate |
|---|---|---|---|---|---|
| off (v74 as first shipped) | 84.7% | 28 | 37 | 0.167 | **0.61%** |
| 2 480 | 86.8% | — | — | — | 2.08% |
| **2 400 (shipped)** | **88.9%** | **8** | **39** | **0.113** | 3.91% |
| 2 300 | 88.0% | 8 | 43 | 0.123 | 4.40% |
| 2 000 | 86.8% | 8 | 48 | 0.134 | 5.01% |

(The sweep is as measured when the rule shipped; the whole column has
since moved with the engine-order corrections below, the shipped row from
88.9% / 3.91% to **89.6% / 3.55%**. The knee is a property of the fuse
and the demo-frame grid, so it was not re-swept — only the row the
pipeline stands on was re-measured.)

**The aggregate got worse and the classifier got better**, and both
statements are literal. The rule removes 20 of the 28 over-counting
player rows and creates 2 new under-counting ones; mean absolute error
per row falls by a third. What rises is the AGGREGATE, because the old
0.61% was two-sided error cancelling (28 rows over, 37 under, net −5
hits of 818) and what remains is one-sided (−32 of 818). Keeping a
false-positive channel because it happens to offset a false-negative one
is not an accuracy the pipeline wants, so the rule ships and the residual
under-count is named instead: 39 rows where a touch left no reconstructed
row at all — a grenade whose flight was never bracketed, or a touch the
engine charged no health or armor for (below).

**Touches that leave no evidence at all.** KTX increments `hits` in
`GrenadeTouch`/`T_MissileTouch` before any damage rule runs, so a touch
that ends up dealing nothing still counts for KTX and is invisible here —
there is no health/armor delta to reconstruct. Three engine paths do
that, and the corpus says how much they matter:

- `teamplay 1` and `3` zero the health share of a teammate hit but still
  take the armor (`combat.c:634-655` computes `save` before the
  team-damage check at `:752`), so the delta reads 88/66/33 for a
  110-point direct on RA/YA/GA — which the magnitude prior refuses,
  since a lone touch must read the constant. **Not exercised by anything
  we can measure**: the 53-demo GT corpus publishes `teamplay 2` on 39
  demos and no key at all on the 14 duels, and an 774-demo archive sample
  publishes 2 (282), 0 (203), 4 (6) or nothing (280) — not one demo of
  either corpus publishes 1 or 3. Measured directly on the GT corpus, the
  classifier's recall on wire-flagged TEAM rocket touches is 94.7%
  (124 of 131) against 95.6% on enemy touches, and all 131 team touches
  read exactly 110 or 440 — full teammate damage, the `teamplay 2` rule.
  So the armor-share magnitude match this would need stays a **lead, not
  a mechanism**: there is no population to validate it against.
- `teamplay 4` takes neither health nor armor (`tp4teamdmg`), so a
  teammate touch leaves nothing whatsoever — unrecoverable in principle,
  on 6 of 774 archive demos (0.8%).
- Godmode and pentagram invincibility zero the health share the same way.
  The pent case is already recovered where it leaves an armor delta and
  synthesized where it leaves none (`pentSyntheticEvents`).

### The wire-linked accuracy family vs the verbatim block (2026-08-28)

The section above scores the tier an OLD demo publishes. This one scores
the tier a modern demo publishes — `playerStats.accuracy` with
`src: "derived"`, built by linking each decoded fire to the wire
`mvdhidden_dmgdone` log. Until v75 that tier answered its own question on
every weapon (`hitsConvention: anyDamage`, one attack per trigger pull),
which meant the two eras answered KTX's question with *different* halves
of the corpus: a pre-instrumentation demo published KTX's rl/gl
direct-impact count while the demo recorded last week did not.

v75 makes the wire-linked tier read the aim section's own measured
counters — `pellets` / `pelletHits` for the shotguns, `direct` for `rl`
and `gl` — so it publishes KTX's convention on every weapon KTX counts
differently. `gl`'s is the one that is not a wire reading at all; see
"the wire cannot see a grenade touch, so it does not read one" below. The
`/measured` columns of `cmd/qw-demoinfo-eval` score exactly that, on the
same 186 archive demos, against the same verbatim block (the harness
captures the stored family BEFORE the withhold; it is a second tier, not
part of the blind answer). `/fires` and `/anyDamage/wire` are the
pre-v75 quantities, kept in the harness so what the change bought is a
measurement rather than a claim.

| row | rows | before v75 | after v75 |
|---|---|---|---|
| `acc.sg.attacks` | 534 | 0.0% exact / 83.33% | **100.0% / 0.00%** |
| `acc.ssg.attacks` | 390 | 0.0% / 92.86% | **100.0% / 0.00%** |
| `acc.sg.hits` | 534 | 6.0% / 76.95% | **100.0% / 0.00%** |
| `acc.ssg.hits` | 390 | 5.9% / 85.62% | **100.0% / 0.00%** |
| `acc.rl.hits` | 632 | 1.6% / 355.17% | **99.8% / 0.02%** |
| `acc.rl.attacks` | 632 | 99.8% / 0.00% | unchanged |
| `acc.lg.hits` | 434 | 99.3% / 0.00% | unchanged |
| `acc.axe.hits` | 86 | 100.0% / 0.00% | unchanged |
| `acc.gl.hits` | 424 | 42.9% / 55.13% | **92.0% / 3.79%** |
| `acc.{lg,gl,ng,sng}.attacks` | 434/424/139/336 | 98.4-100.0% / 0.00% | unchanged |

`lg` and `axe` are the control: their any-path count already WAS KTX's
counter (one trace, one swing, one damage path), and they reproduce the
block on 99.3% and 100.0% of rows without anything changing. That is what
says the join itself is sound and the pre-v75 residual on the other
weapons was definition, not derivation.

**The shotgun residual was entirely quad, and is now gone.** The pellet
count is an ESTIMATE from magnitude — a fire's same-frame damage sum over
the 4 a pellet does, clamped to the fire's 6 or 14 (`aimcore/aim.go`) —
and it first shipped with a flat divisor. That scored 65.9% / 1.03% on
`acc.sg.hits` and 75.9% / 6.75% on `acc.ssg.hits`, bias +4.13 and +6.85
per row, with **not one row of 924 under-counting**. Splitting the rows on
whether the player took a quad at all located it exactly:

| row | quad == 0 | quad > 0 |
|---|---|---|
| `acc.sg.hits` | 264 rows, **100.0% exact / 0.00%** | 270 rows, 32.6% / 1.36% |
| `acc.ssg.hits` | 144 rows, **100.0% exact / 0.00%** | 246 rows, 61.8% / 9.11% |

`T_Damage` multiplies the attacker's damage by 4 while
`super_damage_finished > time` (`ktx/src/combat.c:540-546`), so a quad
pellet writes 16 to the wire log: a flat `/4` read it as four pellets and
a quad fire with two pellets in saturated the 6-pellet clamp. The fix does
not need the per-damage-row quad flag the wire lacks — the SHOOTER's quad
is already on the wire as a possession interval
(`streams.players[].q`, the same stream `playerStats.hold.powerups` is
integrated from), and the state to read is the one at FIRE time: the
hitscan trace and its `T_Damage` calls run in the same server frame as the
trigger pull, so a quad expiring between them is not a case that exists.
Dividing by 16 there closes the gap completely:

| row | flat `/4` | `/16` under quad |
|---|---|---|
| `acc.sg.hits` | 534 rows, 65.9% / 1.03% | **100.0% / 0.00%**, bias 0.00 |
| `acc.ssg.hits` | 390 rows, 75.9% / 6.75% | **100.0% / 0.00%**, bias 0.00 |

Every other row in the table is byte-identical across the two runs (the
`-nails` run too), so this is the whole of what the divisor touched. **No
residual remains to characterise**: on 924 archive player rows the
estimate reproduces KTX's own per-pellet counter to the unit, which is as
strong a statement as `acc.rl.attacks` or `acc.axe.hits` gets.

One caveat worth recording because it turned out NOT to bite: under
`teamplay 1` / `3` a hit on a teammate has its health share zeroed
(`ktx/src/combat.c:752-762`), and an unarmored teammate therefore loses
nothing at all — but KTX still counts the pellet in `wpn[].hits`, so a
suppressed wire row would cost us one. It is not suppressed. The hidden
`mvdhidden_dmgdone` message is gated on `unbound_dmg_dealt`, which is the
armor save plus `virtual_take` — the take captured BEFORE the teamplay,
godmode and pentagram zeroing (`:733`, `:795`, `:810`) — so the row is
written carrying the pre-nullification amount and the pellet arithmetic
still sees its 4. Same reason `DamageEntry.Bounded` can be 0 beside a
non-zero `Damage`.

Two per-fire pellet counts stay unmodelled, and are STATED rather than
guarded: `k_instagib` gives the sg slot a railgun and counts one attack
per fire, not six (`ktx/src/weapons.c:806-810`), and `k_yawnmode` fires 21
ssg pellets rather than 14 (`:858`). aim's pellet table is an
unconditional 6/14, so a demo of either kind reads that row off-scale
against the block. Neither mode is in this 186-demo population, and a
branch measured against nothing is worse than a documented gap.

**The wire cannot see a grenade touch, so it does not read one.** KTX
increments `wpn[wpGL].hits` in `GrenadeTouch` (`ktx/src/weapons.c:1331`)
and then detonates the grenade through `GrenadeExplode` →
`T_RadiusDamage`, which raises `dmg_is_splash` for every row it writes
(`ktx/src/combat.c:1207`). So a grenade touch leaves NO non-splash row on
the wire, and the count that reproduces `acc.rl.hits` on 632 of 632 rows
reproduces **0.00%** of KTX's gl total: `acc.gl.direct/wire` scores 30.0%
of 424 rows exact with a bias of −1.93, and every one of those exact rows
is a player who touched nobody. Until 2026-08-29 that was where the
question stopped — a wire-linked gl row kept the any-path count
(42.9% exact, +1.06 per row, **55.13%** aggregate OVER-count) with
`hitsConvention: anyDamage`, and the UI wore its `≠` mark on that cell.

**It is answered the way an OLD demo answers it.** The reconstruction
never reads the splash flag either — it re-classifies each explosion from
the flight geometry and the spent fuse (`direct.go`) — and none of that
evidence is era-dependent: the grenade's broadcast flight, its detonation
point against the victim's 32×32×56 hull, and the 2.5 s fuse
(`weapons.c:1434`) are on a modern demo exactly as they are on a 2004 one.
So the same classifier is fed the WIRE rows (`damagerecon.WireDirectTouches`
→ `aim.players[].weapons[].direct` → `playerStats.accuracy.byWeapon.gl`),
and it lands ABOVE the reconstructed tier it borrows from:

| gl counter, 424 archive player rows | exact | bias / row | p90 \|err\| | aggregate |
|---|---|---|---|---|
| `anyDamage` (what a wire row published before) | 42.9% | +1.06 | 4 | 55.13% |
| `direct/wire` (the splash flag) | 30.0% | −1.93 | 5 | 100.00% |
| `acc.gl.hits` (RECONSTRUCTED tier, same rows) | 89.6% | −0.07 | 1 | 3.55% |
| **`acc.gl.hits/measured` (shipped)** | **92.0%** | **−0.07** | **0** | **3.79%** |

Better on three of the four measures than the tier whose classifier it is,
and identically biased on the fourth. The wire row is the STRONGER input
of the two eras and the gap is where that shows: its attacker and weapon
are measured rather than inferred, so a candidate cannot win the row for
the wrong shooter, and its damage value is the server's own rather than a
health/armor delta reconstruction. The only work left for this side is the
lookup — which of that shooter's grenades the row belongs to — where the
reconstruction has to answer "whose damage is this at all" first.

**What bounds the count is measured too, and it is not `hits`.** rl's
directs are a subset of the fires the linker connected, so clamping
`Direct` to `Hits` is belt and braces there. gl's are not: a grenade that
touched somebody while the fire→flight join failed to link its fire is a
touch with no `Hit`, and the clamp throws it away. Both were run over the
same 424 rows:

| gl `Direct` bounded by | exact | bias / row | aggregate |
|---|---|---|---|
| `hits` (fires that connected) | 85.6% | −0.15 | 7.58% |
| **fires (shipped)** | **92.0%** | **−0.07** | **3.79%** |

The fire bound is the one that bounds a touch count physically — one
grenade per fire, one touch per grenade — and it is the same bound the
reconstructed tier's `recon.directHits` already carried. It never actually
bites: `acc.gl.classifier/wire`, the raw unclamped row count, scores
identically to the shipped column. The cost is that gl's
`direct` + `splash` + `missed` no longer partitions its fires
(`splash` floors at zero), which `result.WeaponAim` states per weapon.

**`rl` keeps the flag, and the classifier says by how much.** The same
run scores `acc.rl.classifier/wire` — the classifier substituted for the
flag on the very rows the flag answers — at **54.7%** of 632 rows exact,
+0.16 per row, **1.54%** aggregate, against the flag's 100.0% / 0.00%.
Nothing can beat a count that is the server's own verdict, so rl stays on
the splash bit and the classifier's rl verdict ships nowhere; the column
is kept because what an alternative costs is what decides against it.
(It is worth reading beside the reconstructed tier's rl on the same
players, 46.5% exact / 1.20%: the wire's exact damage value and measured
attacker buy the classifier 8 points of exact rows, which is the same
asymmetry the gl table shows — while the aggregate moves the other way,
because the residual there is one-sided under-count and here it is a
smaller two-sided one that does not cancel as neatly.)

**`ng`/`sng` are on KTX's scale but under-recover.** Both weapons launch
exactly one spike per `attacks++` (`W_FireSuperSpikes`,
`ktx/src/weapons.c:1640`; `W_FireSpikes`, `:1672` — the super nailgun's
`-2` is ammo, not a second nail) and KTX's `hits++` fires once per spike
that touched (`:1620`, `:1549`), so "the fire connected" and "a nail
connected" are the same event and `anyDamage` is the right label. The
linker just misses some: run with `-nails`, `acc.ng.hits/measured` is
65.5% of 139 rows exact at **32.11%** aggregate under-count and
`acc.sng.hits/measured` 48.5% of 336 at **22.81%**, bias −2.01 and −3.05
per row, never positive. Nail accuracy is a floor, not an estimate.

**`rl`'s zero is a measured zero, on every demo.** The direct count is
gated section-wide on the wire damage stream and nowhere per row: under
that gate `direct` IS the count of the player's non-splash rl damage rows,
so 0 means "touched nobody" — including on a demo where no rocket landed
damage at all and aim's direct/splash split therefore never ran (a short
FFA round, say). What the number is exposed to is the LINKER, not the
classification: a rocket that touched somebody but whose damage row the
fire→damage join did not reach reads as a miss, which is the same exposure
the pre-v75 any-path count always had. `acc.rl.hits/measured` is what
measures it — 99.8% of 632 rows exact at 0.02% aggregate, above.

**`gl`'s zero is measured only where its classifier ran.** Unlike rl's, gl's
count needs the spatial shot streams (`Registry.BuildShotStreams`) for the
flights and `TE_EXPLOSION` points its geometry reads. mvd-api and the WASM
build always request them; a bare `qw-analyze` parse does not, and there aim
publishes no gl direct/splash split at all and the accuracy row falls back to
the any-path count with `hitsConvention: anyDamage` — the honest label and the
`≠` mark, rather than a withheld 0 passed off as "touched nobody". Same shape
as the nail gate below, same latch discipline (`Streams.ShotStreamsComputed`).

Without `-include nails` there is no nail linkage at all, and v75 stops
publishing the `hits: 0` that produced — the field is withheld, keyed on
`Streams.NailsComputed`, the same latch the shots analyzer sets. (Before
v75, `ffa_1[dm2]` reported Myagi at `ng` 177 attacks / 0 hits on a default
CLI parse and 17 hits with the flag; mvd-api and the WASM build always
request nails, so only the CLI ever saw the zero.)

Reproduce:

```
MVDA_BSP_DIR=./bsps go run ./mvd-analytics/cmd/qw-demoinfo-eval \
    -dir <186 archive demos> -workers 5 -csv demoinfo.csv
MVDA_BSP_DIR=./bsps go run ./mvd-analytics/cmd/qw-demoinfo-eval \
    -dir <186 archive demos> -workers 4 -nails -csv demoinfo-nails.csv
```

### The rocket splash base is 120, not the direct 110 (2026-08-24)

Found while building the classifier above, and worth stating separately
because it moved the damage numbers. `T_MissileTouch` passes **120** to
`T_RadiusDamage` (`ktx/src/weapons.c:1006`), the same base as a grenade;
the 110 is the touch value and belongs to a disjoint victim (the touched
player is the radius pass's `ignore`).

This package modelled rocket splash on the DIRECT constant's range
instead (`blastDamage` returned `rlLo..rlHi`), so the error was not a
uniform 10 and depended on what the demo had established about the
server. Where `detectRocketRegime` found the fixed constant — 567 of the
638 ground-truth rows — the range collapsed to `110..110` and every
rocket splash was understated by exactly 10. Where it did not, the model
carried the vanilla `100..120`: a range whose top end reached the true
base for the wrong reason (it is the direct roll's ceiling, not the
radius base) and whose bottom sat 20 under it, so the band was both
mis-centred and twice as wide as the engine's single value. Grenades
were already correct — `blastDamage` returned a flat 120 for `gl` — which
is why only the rocket figures moved.

Measured on the dm2/dm3 ground truth: `value + 0.5·dist` over 2 530
wire-flagged splash rows has median **122.4**, not 112 — the residual
2.4 being our distance measured from the pulled-back TE_EXPLOSION point
to the track origin rather than from the pre-pull-back origin to the
bbox centre. Correcting the model improved the bounded family on the
53-demo corpus (given mean 0.81% → 0.76%, ewep 1.57% → 1.49%,
givenSelf 4.19% → 3.90%) and the derived summary against the KTX block
(`dmg.given` 0.49% → 0.47%, `dmg.taken` 0.46% → 0.44%).

The KILL raw top-ups deliberately did NOT follow it at the time
(`topUpBase`, `attribution.go`): a top-up only ever RAISES a value, so
its floor must under-estimate, and feeding it the true base cost raw
accuracy measurably (raw given 2.01% → 2.65% mean per player). **That
finding was an artefact of the quad-ordering bug and is refuted below** —
with the multiplier moved to the engine's side of the falloff the same
measurement inverts, the two constants are gone, and the top-up is the
engine's own formula.

The **pent synthesiser** did follow it, and then needed the other half of
the same fact. `pentSyntheticEvents` invents the raw value of a hit a
pentagram holder absorbed with no health or armor to show for it, and it
priced every one of them off the radius curve — so an enemy DIRECT rocket
on a pent holder synthesized ~120 (480 quadded) where the engine dealt
110 (440), and on a demo with no established constant it priced a direct
off the vanilla range's low end. It now prices a direct-classified
ROCKET row at the direct constant and leaves everything else on the
curve, which is the same asymmetry the classifier rests on: a grenade has
no touch value at all (`GrenadeTouch` deals nothing itself), so a touched
grenade victim stays on the falloff curve. Worth `raw ewep` 1.59% →
1.58% median on the 53-demo corpus — the population is small, but every
row in it was priced by a formula the engine does not use.

### The quad multiplies AFTER the falloff, and splash stops at 160 units (2026-08-24)

Two engine facts, recorded with their evidence in
`plan-damage-recon.md` §8 when the direct-impact work found them and
shipped here together, because the second is only safe once the first is
right.

**A. `4·(120 − 0.5·d)`, not `4·120 − 0.5·d`.** `T_RadiusDamage` hands a
flat base to `T_RadiusDamageApply`, which subtracts the falloff
(`points = damage − 0.5·dist`, `ktx/src/combat.c:1189`) and only then
calls `T_Damage`, where the ×4 is applied (`:537-543`). Every multiplier
that composes with a radius hit sits on that same downstream side —
quad, the dmm4 octa, the CTF strength rune, the handicap — while the two
that do not (self-halving, shambler-halving) are inside
`T_RadiusDamageApply` itself. The package had the quad on the wrong
side, which puts a quad splash in `[400, 480)` where the engine's own
range is `(0, 480]`. The wire settles it: of 898 quad rl splash rows on
the dm2/dm3 ground truth, 96% read between 160 and 400 — values the old
form cannot produce anywhere inside the engine's reach.

**B. The reach is `findradius(damage + 40)` = 160 units**
(`combat.c:1252`), measured origin-to-bbox-centre exactly as the falloff
is (`mvdsv pr_cmds.c:1233`), and the quad does not widen it because the
multiplier is applied downstream. Candidate admission ran to 380. It now
runs to the reach plus the same slack the damage-model band uses for that
endpoint — 184 for an explosion-snapped detonation point, 220 for a
tracked flight's last broadcast position, which can sit a server frame
(34 units at 1000 ups) short of the real one. The LG water discharge is
the third radius source and had the same defect (`35·cells + 40` is its
reach, while `35·cells − 0.5·d > 0` admits twice that); its gate cuts 9
of 275 candidates on the GT corpus and moves no scored row, so it ships
as the engine bound and not as an accuracy claim.

**How the slack was derived, and it was measured rather than picked.**
For a lone wire splash row the value IS the engine distance:
`unbound_dmg_dealt = ceil(q·(120 − 0.5·d))`, so `d = 2·(120 − v/q)`.
Comparing that with the distance this package measures, over 14 140
wire-flagged enemy rl splash rows: median error **4.8** — which is the
two systematic terms, the victim bbox centre (+4) and the 8-unit
`TE_EXPLOSION` pull-back applied after the radius pass — p95 18.1, p99
27.4 (golden cache: 5.0 / 22.2 / 35.2). The 184 admission keeps
**99.76%** of those rows where 380 kept 99.78%.

**The re-tune, and what it deleted.** The kill top-ups' `topUpBase = 110`
/ `topUpSlack = 60` were calibrated against the broken order. Swept on
the 30-demo dm3 half and confirmed on the held-out dm2 half, raw given
mean per-player error falls **monotonically** as the pair approaches the
engine's own numbers — dm3 2.87% → 1.16%, dm2 3.44% → 1.38% going from
(110, 60) to (120, 0) — so both constants are gone and the top-up is
`splashModel`, the engine's formula at the measured distance. The band
slacks (24 snapped / 60 not) were swept over 12–36 and measured flat, so
they keep their derived values.

Before / after on the 60-demo dm2/dm3 ground truth, per-player relative
error (median / mean). The third column is the review-fix batch that
followed (verdict-split kill top-ups + the geometry prior's own
normalizer, both below); it is shown separately because the middle
column is what the isolation runs in the rest of this section were
measured against:

The third column is a CHECKPOINT, not the current numbers: the two
changes described under it moved four of its rows afterwards (raw
`givenSelf` mean 5.49% → **4.88%** is the big one), and each move is
recorded in the paragraphs that follow rather than by rewriting the
column — the column's job is to isolate what A+B and the review fixes
did.

| family | before | A+B as first shipped | + review fixes (checkpoint) |
|---|---|---|---|
| bounded given | 0.58% / 0.76% | 0.57% / 0.80% | 0.58% / 0.79% |
| bounded taken | 0.04% / 0.14% | 0.04% / 0.14% | 0.04% / 0.14% |
| bounded ewep | 0.87% / 1.49% | 0.90% / 1.54% | 0.87% / 1.50% |
| bounded givenTeam | 1.86% / 5.24% | **1.39%** / 5.13% | **1.39% / 5.08%** |
| bounded givenSelf | 1.43% / 3.90% | 1.66% / 4.38% | **1.28%** / 3.99% |
| **raw given** | 1.24% / 2.04% | 0.74% / 1.24% | **0.71% / 1.21%** |
| **raw taken** | 1.74% / 2.29% | **0.62%** / 1.11% | 0.66% / 1.13% |
| raw ewep | 1.58% / 2.90% | **1.14%** / 2.35% | 1.18% / **2.28%** |
| raw givenTeam | 9.67% / 16.35% | **7.39% / 14.56%** | 7.39% / 14.62% |
| raw givenSelf | 2.28% / 8.16% | 2.28% / 5.81% | **1.98% / 5.49%** |

Two later changes move rows in this table. The first is pricing the LG
discharge's geometry prior by distance instead of flat
(`dischargeCandidates`, after
the review of the pair-split trigger — see the `trySplitPair` bullet in
[`plan-archive-features.md`](../../plan-archive-features.md)). It leaves
the headline rows untouched — coverage, value-exact, attacker-correct and
bounded given / taken / ewep are unchanged to the digit, and the whole
golden-cache eval is identical — and moves only the small-denominator
self and team families, in both directions: bounded `givenTeam`
1.39% → 1.43%, bounded `givenSelf` 1.28% → 1.26%, raw given
0.71% → 0.70%, raw taken 0.66% → 0.68%, raw `givenTeam` mean
14.62% → 14.83%, raw `givenSelf` **1.98% / 5.49% → 1.94% / 4.90%**. It
was taken for the failure mode rather than the numbers: a flat prior made
a discharge the cheapest candidate on the board anywhere inside a reach
that runs to 740 units at 20 cells.

The second is admitting trackless explosions to the same-frame pair split
(§"Trackless explosions in the pair split"), and it moves exactly two
rows, both in the `givenSelf` family and both by a hair: bounded
`givenSelf` median 1.26% → 1.28%, raw `givenSelf` 1.94% / 4.90% →
1.98% / 4.91%. Everything else here is unchanged to the digit, as are
coverage, value-exact and attacker-correct — and the golden cache moves
the other way (bounded `givenSelf` median 1.58% → **1.46%**).

The third is the direct-constant exemption (§"The direct constant is not
a merge"), which moves the table's bounded rows the right way and one
raw row the wrong way: bounded given **0.58% / 0.79% → 0.57% / 0.77%**,
bounded `ewep` 0.87% / 1.50% → **0.85% / 1.48%**, bounded `givenSelf`
1.28% → **1.26%**, raw `givenSelf` 1.98% / 4.91% → **1.94% / 4.88%**,
against raw given 0.70% → 0.72%. It also takes `rl` attacker-correct
99.6% → **99.7%** (the weapon list below is stated against the
checkpoint, before that move) and leaves the golden cache unchanged to
the byte.

and per attacker weapon, attacker-correct on unambiguous enemy instants,
before → now: `ng` 96.4% → **97.2%**, `sng` 97.5% → **97.9%**, `ssg`
98.1% → 98.1%, `rl` 99.7% → 99.6%, everything else unchanged; the pooled
figure stays 98.5%. (`ssg` and `sng` peaked at 98.3% / 98.0% in the
middle column and gave 0.2 / 0.1 pp back to the normalizer change — see
§"The geometry prior's own normalizer".)

**The bounded and raw families moved in opposite directions in the
middle column, and the reason is A vs B.** Re-running with admission
held at 380 isolates them: with only the quad fix the bounded family is
IDENTICAL to before (given 0.58% / 0.76%, ewep 0.87% / 1.49%) while raw
already carries the whole gain. So the middle column's bounded-family
regression belongs entirely to the admission change — but "the cap" is
not all of it, and the original account of this paragraph over-attributed.
The isolation run held `splashAdmit` at 380, and `splashAdmit` was at
that time ALSO the divisor of the projectile geometry prior, so the run
reverted a cap and a scoring weight together. Splitting them (below)
shows the recall part is the 3 rows, and the `givenSelf` part was mostly
the rescale: giving the prior its own normalizer returns `givenSelf` to
1.28% / 3.99% with the 160-unit cap still in force.

The cap's own effect is those 3 rl splash rows per 14 140 whose MEASURED
GEOMETRY lands past the engine's reach: they lose their only candidate
and fall to `env:unknown` (misattribution flow `enemy:rl → env:unknown`,
821 damage over 11 instants → 1 161 over 13). What the cap buys is on
the other side of the same table — `enemy:sg → self` 800 → 628,
`self → enemy:rl` 1 288 → 1 094, bounded `givenTeam` median 1.86% →
1.39%, and the nail and ssg attribution above. The trade is a recall
loss of 0.02 pp against a precision gain, taken deliberately.

**Where the cap sits, and why it did not move.** Of those 3 rows, 1 is
recoverable and 2 are not: the caps table
(`.reports/quad-splash-2026-08-24/splash-geometry-caps.txt`) is flat from
190 to 240 at 14 107 / 14 140 kept, so a cap at reach + p99.5 (≈196)
re-admits exactly one of them, and the other two need 320 and 380. That
one row was measured rather than argued: decoupling the ADMISSION slack
from the band slack and running the whole corpus at 196 does recover it
(`enemy:rl → env:unknown` 13 → 12 instants), improves bounded given mean
0.79% → 0.76% and rl attacker-correct 99.6% → 99.7% — and pays for it
with bounded `givenTeam` 1.39% → 1.52% (golden cache raw `givenTeam`
4.24% → 4.49%), raw taken 0.66% → 0.68%, and gl attacker-correct 99.7% →
99.5%. No dominance in either direction. Since moving the cap requires a
SECOND slack constant whose only justification would be "a higher
quantile of the same error distribution", and the measured trade does not
pay for that parameter, 184 stays: one number, `splashSlack`, doing both
jobs with one derivation. The golden cache's fatter error tail (p99 35.2
against dm2/dm3's 27.4) means the recall loss runs higher off the tuning
maps, and that is the honest caveat on the 0.02 pp figure.

Per explosion, the direct/splash verdict is unmoved: 18 034 paired rl
instants (was 18 039 before the leads, 18 026 with them as first
shipped), 97.9% classification accuracy, precision 94.6%, recall 95.9%,
direct-count error +1.4%.

On the archive derived-summary protocol (`cmd/qw-demoinfo-eval`, first
500 demos of the 51k archive), four columns improve and one regresses.
The dmm4 gate below removes 2 of the 188 demos that scored, so the last
two columns are both computed over the 186 the runs share — like for
like. (`before` is the published 188-demo figure; the 2-demo difference
is under its rounding.)

| column | before | A+B as first shipped | + review fixes |
|---|---|---|---|
| `dmg.given` | 0.47% | **0.44%** | 0.48% |
| `dmg.taken` | 0.44% | **0.41%** | 0.45% |
| `acc.gl.hits` | 3.91% (88.9% exact) | **3.55%** (89.6%) | **3.55%** (89.6%) |
| `acc.lg.hits` | 0.91% | **0.87%** (63.6% exact) | 0.89% (**64.7%**) |
| `acc.rl.hits` | 1.23% | 1.33% | **1.25%** |
| `acc.rl.hits/fixed` | — | 0.73% | **0.65%** |

The `rl` regression against `before` is the same
three-rows-in-fourteen-thousand effect seen from the other end: the
row-exact share (46.5%) and the p90 error (2 hits) do not move and the
bias runs −0.13 hits per row, i.e. a handful of directs over 186 demos
joining the under-count that the grenade-fuse rule already established as
the honest direction. The review fixes take back two thirds of it —
pricing a direct kill as a direct is exactly an `rl` touch-count
question — and the `fixed` population, the one the classifier's magnitude
prior actually serves, lands at 0.65%. `dmg.given` / `dmg.taken` pay 0.04
pp for it. The `spread` (13.04%) and `unestablished` (22.52%) populations
are byte-identical across all three columns.

The aim recon tier (`cmd/qw-aim-eval`) improves or holds: rl mean 0.7pp
→ **0.5pp** (bias +0.4 → +0.3, ≤2pp 90% → 93%), gl 0.4pp, lg 0.3pp, sg
1.3pp, axe 0.6pp unchanged. `ssg` reached 1.6pp with the leads as first
shipped and is back at its old **1.8pp** after the review fixes — the
same 0.2 pp the ssg attacker-correct row gave back.

**Where the cap costs most: the un-instrumented archive.** The obituary
oracle (`cmd/qw-recon-oracle`, 285 scored demos of a 300-demo archive
sample, obituaries withheld) is the only measurement that reaches the
eras with no KTX log, and it prices the same trade a little higher there:

| era | attacker-correct | unattributed bounded damage |
|---|---|---|
| E0 qwsv/KTPro | 97.8% → 97.7% | 1.29% → 1.43% |
| E2 mvdsv 0.25–0.29 | 98.4% → 98.2% | 0.75% → 0.87% |
| E3 mvdsv 0.30–0.33 | 98.1% → 98.0% | 0.73% → 0.78% |
| E4 mvdsv 0.34–0.36 | 97.1% → 97.0% | 0.59% → 0.61% |
| E5 mvdsv 1.x | 96.8% → 96.8% | 0.64% → 0.70% |

Per weapon it is the same shape as on the GT corpus, with the losses on
the radius weapons and the gains on the hitscan and nail ones: E0 rl
98.2% → 97.9% and gl 99.0% → 98.8%, against sg 95.7% → 95.8% and sng
94.6% → **95.4%** (E5: rl 96.9% → 96.7%, sg 95.4% → **95.8%**, sng 94.2%
→ **95.3%**). The damage the cap refuses is not misattributed, it is
published as `unknown` — the section says it does not know rather than
naming an attacker whose only evidence sits, as WE measured it, past the
distance the engine visits — which is the direction this pipeline prefers
to be wrong in.

**The review fixes re-priced on the same oracle** (`cmd/qw-recon-oracle`,
same 300-demo sample; the dmm4 gate removes 3 of it, so the eras move by
one or two demos and the comparison is between whole-era figures rather
than a matched set). Attacker-correct: E0 97.7% → 97.7%, E2 98.2% →
**98.3%**, E3 98.0% → 98.0%, E4 97.0% → **97.1%**, E5 96.8% → 96.8%.
Unattributed bounded damage: E0 1.43% → 1.44%, E5 0.70% → 0.70%, the rest
unmoved. So the whole batch is oracle-neutral, which is the result to
report — the `givenSelf` and rl-touch gains it buys on the instrumented
corpora do not reach for anything on the un-instrumented one. Per weapon
the one real move is `sng`, E0 95.4% → 95.0% and E5 95.3% → 94.6%: the
same sign as the 0.1 pp `sng` cost the normalizer sweep showed on the GT
corpus, larger here, and the one figure in this batch worth watching.

**And that cost is entirely the SNAPPED half.** The admission radius for
an un-snapped flight endpoint (220) was measured against leaving it at
the old 380: byte-identical oracle output, byte-identical 188-demo
protocol. TE_EXPLOSION coverage is ~99% of demos, so essentially every
projectile candidate that decides anything carries an exact detonation
point, and there the cap is a statement about the ENGINE rather than
about our measurement. The un-snapped radius is derived from the same
slack for consistency rather than because anything measured it.

### The kill top-ups price a DIRECT rocket as a direct (2026-08-24)

Both kill top-ups — the scored-candidate one and the obituary-anchored
one — priced every rocket kill on the 120 radius curve, including the
ones they had just classified as a TOUCH. That is the wrong curve:
`T_MissileTouch` deals a flat 110 and hands the touched entity to
`T_RadiusDamage` as its `ignore` (`ktx/src/weapons.c:986`, `:998-1006`),
so a point-blank quad direct is 440 and the radius curve would raise it
to 480. The obituary-anchored path is where this bites — on the 60-demo
corpus 5 597 kill rows reach it with a direct verdict against 58 on the
scored path — and it is now one shared `killModelFloor`, split on the
verdict, taking the direct range's LOW end because a raise-only floor may
not over-claim on a pre-1.36 rolling server. A grenade keeps the radius
curve either way: `GrenadeTouch` deals nothing on contact (`:1327-1333`).

The same helper carries `T_RadiusDamageApply`'s self-halving, which the
obituary-anchored path was passing as 1.0. That path's self population is
EMPTY and the fix is a contract rather than a measurement: `fragAt`
excludes suicides and teamkills (`inputs.go:213`), so the frag-anchored
branch that calls `topUpKillRaw` can never see `attacker == victim` — 0
occurrences over the 60-demo corpus, confirmed by instrumentation, and
`raw givenSelf` did not move on the fix. Every other path already carried
it correctly (`pentSyntheticEvents`, the scored top-up, `modelBounds`,
`dischargeCandidates`, the discharge top-up).

Measured alone, on the 60-demo corpus: raw given 0.74% / 1.24% → 0.69% /
1.22% and raw ewep p90 5.74% → 5.54%, against raw taken 0.62% → 0.66%
median; on the golden cache the medians shuffle and the tails improve
across the board (raw given p90 2.36% → 2.13%, ≤2% 84% → 88%; raw taken
p90 2.10% → 1.68%). The direction is not tunable — the engine deals
exactly one of the two numbers — so the mixed medians are reported, not
resolved.

### The geometry prior's own normalizer (2026-08-24)

Lead B's admission change had a second, undocumented effect. The
projectile candidates' geometry prior read `dEnd / splashAdmit(epExact)`,
so narrowing admission from 380 to 184/220 DOUBLED the prior's distance
slope and re-weighted every projectile candidate against the fixed-geom
kinds (env 0.12, discharge 0.1, beam 0.3+) that did not move. The divisor
also varied with `epExact`, which scored an un-snapped endpoint better
than an exact one at the same distance — an artifact, never an intent.
`geomNorm` now names the scoring weight and `splashAdmit` is admission
only.

`geomNorm` has no engine referent: it is a free scoring parameter, and
the honest thing is to say so and give the sweep. Swept on the 30-demo
dm3 half and confirmed on the held-out dm2 half over {184, 220, 260, 320,
380} — the two ends being the two values the coupling would have handed
it. The families do not agree on a single optimum: `bounded givenSelf`
falls monotonically with a flatter slope on BOTH halves (dm3 mean 4.95% →
4.46%, dm2 3.75% → 3.21% from 184 to 380), while 380 costs the held-out
half's `bounded givenTeam` (median 0.69% → 1.52%, mean 4.38% → 4.54%) and
dm3's `raw given` median (0.70% → 0.85%). The rule applied was: not a
swept boundary, and no family worse than the value it replaces on the
held-out half. **260** is the interior value that satisfies it — it beats
184 on every MEAN on both halves and on `bounded givenSelf`'s median,
with the raw-given median (dm3 0.70% → 0.78%, dm2 0.64% → 0.67%) the only
regression; 380 is refused by the held-out `givenTeam` row.

Full-corpus effect at 260: `bounded givenSelf` 1.66% / 4.38% → **1.28% /
3.99%** and `raw givenSelf` 2.28% / 5.81% → **1.98% / 5.49%** — most of
the `givenSelf` regression the middle column above showed was this
rescale, not the cap — plus `bounded ewep` 0.90% → 0.87% and `bounded
givenTeam` p90 15.52% → 14.04%. It is paid for in `ssg` attacker-correct
98.3% → 98.1% and `sng` 98.0% → 97.9%, both back at their pre-lead
values.

### deathmatch 4 with a quad stands down (2026-08-24)

KTX makes the quad an OCTA in `deathmatch 4` — `damage *= (deathmatch !=
4 ? 4 : tot_mode_enabled() ? FrogbotQuadMultiplier() : 8)`,
`ktx/src/combat.c:541` — and this package models a flat ×4. `dmm4` is not
one of the skipped modes, so every modeled quad hit in such a recording
was published at about half its true value under `source:
"reconstructed"` with nothing marking it. `ReconSkipReason` now stands
the reconstruction down on `deathmatch == 4` ∧ any player held a quad.

It is an interim gate, not the model (`plan-damage-recon.md` §8 lead C),
and it is deliberately narrow: the quad condition keeps quad-less dmm4
analyzable, which is nearly all of it — 8 755 of the 51k archive demos
are on a `*dmm4*` map and 23 of the 24 such maps in the entity corpus
carry no `item_artifact_super_damage` at all (only `emddmm4` does). The
population it costs is measured: on a random 2 000-demo archive sample
(`cmd/qw-corpus-survey`) **4 demos, 0.20% of the corpus and 0.40% of the
994 that reconstruct**, against 952 that carry a KTX log and need none of
this. On the 300-demo oracle sample it moves 3 demos, and only 1 of those
reaches the production pipeline (the other two carry a KTX damage log, so
`damageReconPost` never calls this package for them). Two of the three
are `mode: tot` on dm4 — the `tot_mode_enabled()` branch, where the
multiplier is a server-configured constant that is not on the wire at
all, so the gate lands exactly on the case the engine says is
unmodelable. It is NOT part of `SkipModeReason`: that one also gates the
KTX-side bounded pass, which reads the server's own values and does not
care which multiplier produced them.

### Trackless explosions in the pair split (2026-08-25)

`trySplitPair` scored every candidate family the single pass does except
`explosionCandidates` — the point-blank rockets and contact grenades
whose entity was never broadcast and whose only geometry is the
TE_EXPLOSION the server wrote. It is in the list now, and the family's
job is not to supply a new AUTHOR so much as a better measurement of
one: `rlSoundCandidates` already offers the same shooter off the same
fire sound, but with `dEnd < 0` its band is the whole 25..120 radius
range and a share handed to it is unconstrained. A grenade has no
rl-sound analogue at all, so a contact grenade merging with another
attacker's hit previously had exactly one candidate author.

**Scored directly against the wire.** Every split the blind
reconstruction produces on a demo that also carries a KTX damage log can
be checked against the rows at that instant — who dealt what, exactly.
Over the 53-demo dm2/dm3 ground truth and the 13-demo golden cache the
family produced **no new splits at all** (61 and 16, unchanged), so it
cannot steal on any corpus where stealing is checkable; it re-priced 12
of them, and all 10 whose attackers the wire confirms landed on exactly
the right pair. Total |share − wire| over the correct-attacker shares
falls 442 → 436 (dm2/dm3) and 146 → **124** (golden), with exactly-right
shares up 8 → 10 on the latter — one golden instant the wire records as
48/43 read 53/38 before and reads 48/43 now.

Aggregate `qw-recon-eval` movement is small and one-sided in the right
direction on the golden cache — bounded `givenSelf` median 1.58% →
**1.46%**, raw `givenSelf` p90 10.80 → **10.63**, worst self row 11.53%
→ **10.83%** — with every headline row unchanged on both corpora and
only small-denominator dm2/dm3 rows moving either way (bounded `ewep`
≤1% 55.0% → 54.7%, raw `givenSelf` median 1.94% → 1.98%). On the
archive, where nothing can score it, the family adds 22 splits to 1 307
(+1.7%) and appears in 331 of the resulting 1 329.

**One change was measured and NOT taken.** The misfit probe that decides
whether to challenge a single explanation rebuilds a `"proj"` band from
`dEnd` widened by `splashSlack(epExact)`, and `epExact` does not travel
on the event — so a detonation-snapped winner is probed against a band
36 points wider than the one that chose it. Carrying it raises split
attempts 83% and adds 20 dm2/dm3 splits of which the wire says 16 name
exactly the right pair, but it costs the golden cache bounded `given`
≤2% 90.7% → 88.0%, bounded `ewep` median 0.84% → 0.97% and raw
`givenTeam` p90 23.0 → 26.2 (isolated: the regression reproduces with
the probe change alone). The scorer ranks explanations; the probe
decides whether to entertain inventing a second attacker, and the wider
band is hysteresis on that decision. It stays, documented in
`attributeDelta`.

### The direct constant is not a merge (2026-08-26)

The pair split's false positives had a single shape, and it was
measurable. Scored against the KTX damage rows at the same instant, all
**9** false splits the shipped pair path produced on the 53-demo dm2/dm3
ground truth were an enemy rocket delta of exactly **110** — the fixed
direct constant `detectRocketRegime` measured for those demos — given a
second author, and in **9 of 9** that author was the VICTIM's own rocket.
The mechanism is not randomness: a `"proj"` band is the radius curve at
the measured detonation distance, and `T_MissileTouch`'s constant is not
on that curve at all (the touched entity is passed to `T_RadiusDamage`
as its `ignore`, `ktx/src/weapons.c:998-1006`), so as soon as our
distance to the victim's interpolated position runs ~50 units long the
probe calls a whole direct hit a misfit — and the cheapest way to pay
for the gap at point-blank range is the self splash the victim's own
rocket can almost always supply.

`attributeDelta` now refuses the challenge there: on a demo whose regime
is `RocketRegimeFixed`, a live victim's non-self rocket delta equal to
the constant is one whole hit. Measured on three wire-scored corpora:

| | pairs | correct attacker pair | false splits | Σ\|share − wire\| |
|---|---|---|---|---|
| dm2/dm3 GT (53 demos) | 61 → **52** | 48 → **48** | 9 → **0** | 998 → **547** |
| golden cache (13) | 16 → 16 | 15 → 15 | 1 → 1 | 166 → 166 |
| archive-era GT (350) | 231 → **216** | 184 → **184** | 26 → **11** | 3 421 → **2 667** |

Not one wire-confirmed correct split sits at the constant on any of
them, so the guard removes false splits and nothing else; the golden
cache is unchanged to the byte. `qw-recon-eval` on the dm2/dm3 corpus
moves the bounded family the right way — bounded `given` 0.58% → 0.57%
(mean 0.79% → 0.77%, ≤1% 74.8% → 75.1%, ≤2% 92.8% → 93.1%), bounded
`ewep` 0.87% → 0.85% (p90 3.98 → 3.87), bounded `givenSelf` 1.28% →
1.26%, raw `givenSelf` 1.98% → 1.94% and `rl` attacker-correct 99.6% →
**99.7%** — against raw `given` 0.70% → 0.72% and raw `givenSelf` p90
11.66 → 12.50 the other way.

**Two neighbouring changes were measured in the same round and
refused.** Both are in the `trySplitPair` bullet of
[`plan-archive-features.md`](../../plan-archive-features.md) with their
tables:

- *A physical-identity guard on the pair* — refusing a pair whose two
  members are anchored on the SAME `TE_EXPLOSION`. Those pairs are real
  (8 per attribution pass over 179 archive-era GT demos, 0 on dm2/dm3
  and the golden cache) but they are not fabrications: the wire scores
  **8 of 8** as the right attacker pair, because the situation that
  produces them is a mutual point-blank exchange in which two rockets
  really did detonate, a few units apart, and the pair happens to hang
  both candidates on the same recorded one. Guarding it changes 4 rows
  of 120, leaves every attacker set and the summed share error
  identical, and gains two exactly-right shares — mechanism with no
  load, so the invariant is recorded here instead of enforced.
- *Probing an rl-sound winner against a same-shooter detonation's exact
  band.* It fires often (110 per pass on dm2/dm3) and buys 2 more
  correct pairs there at the cost of 15 more splits naming an author the
  wire does not have.

## Team telefrags were not damage (2026-08-26)

The `raw givenTeam` row above ran at **7.40% / 14.83%** against `raw
given`'s 0.72% / 1.22%, and the bullet below used to record that as a
confirmed pathology with no mechanism: bounded damage flowing INTO the
team class ~9× what flowed out, dominated by a single `PHANTOM → team`
channel of 10 775 damage over 104 instants — reconstructed hits on a
teammate at a (victim, ms) the wire logged nothing at. On the current
pipeline that channel measures 10 875 over 105 instants; classifying
every one of them (throwaway probe; protocol in
`.reports/team-damage-2026-08-26/README.md`) named the mechanism
completely — 60-demo dm2/dm3 ground truth, 53 demos scored:

| bucket | instants | bounded | recon raw |
|---|---|---|---|
| a GT **telefrag** at the same instant | 98 | 10 861 | 20 464 |
| a GT **stomp** at the same instant | 2 | 14 | 21 |
| `pent-synth` (synthesized, no wire row) | 5 | 0 | 175 |
| anything else | **0** | 0 | 0 |

Not one instant was an invented hit. The telefrag bucket agrees with the
wire on the value (bounded matches the KTX telefrag row's own value on
**98 of 98**) and on the attacker (**96 of 98**, and both exceptions are
the probe's ±500 ms window picking up a neighbouring match-start
telefrag). The victim's health broadcast reads exactly −99 on all 98:
KTX's corpse clamp (`Killed`, `ktx/src/combat.c:257`), which only ever
appears on overkill of that size.

(105 is the TEAM-classed subset of the dump. Five more team positional
kills were being booked as ENEMY or SELF damage rather than as team
damage; the eval's own fold, which keys on the exact instant, counts 108
mis-routed `positional:team` instants in all, and the flow list below
accounts for every one.)

Two separate defects were hiding behind each other.

**The eval could not see positional kills.** A telefrag/stomp is an
instant kill, not weapon damage: both `analyzer/damage.go` and this
package's `aggregate.go` keep them out of `Events` and surface them in
`Telefrags`/`Stomps`. `collectConfusion` compared the two `Events` logs
alone, so a correctly-routed positional kill on the reconstruction side
had no GT row to pair with and was reported as a PHANTOM. It now folds
both sides' positional lists in as their own class, which is what turns
the old `PHANTOM → team` line into a truthful `positional:team → team`,
and it prints the GT class totals beside the flows so a flow can be read
as a rate rather than as a bias (see the bullet below).

**And the reconstruction really was booking them as weapon damage.** KTX
prints a teammate telefrag as `"<victim> was telefragged by his
teammate"` and a teammate stomp as `"<victim> was jumped/crushed by his
teammate"` (`ktx/src/client.c:5355-5384`) — one phrasing per deathtype,
so the CAUSE is on the wire even though the killer's name is not. The
obituary table flattened all six markers to the placeholder weapon
`teamkill`, the same token the killer-named phrasings ("checks his
glasses") carry because they genuinely have no cause in them. With the
cause gone, `damagerecon` could not recognise the kill as positional:
`killerFragAt` skips teamkills by construction, so these fell through to
the teamkill anchor and were emitted as ordinary team damage — charged at
the observed corpse drop, i.e. the victim's capacity **plus the 99 points
of clamp**. Summed over the corpus that is 20 464 raw where the wire's own
telefrag rows say 10 861.

The fix is in the two layers that own the two halves. `parser.ObituaryPatterns`
gives the six victim-named markers their real weapon (`tele`, `stomp`) and
leaves the cause-less killer-named ones at `teamkill` — a later row-by-row
audit of the whole table against `ClientObituary` found a seventh
cause-carrying teamkill marker, "X squished a teammate" (`dtSQUISH`,
`client.c:5362`), which is the only one that names the killer instead of
the victim. It now carries `squish`, but a mover crush is ordinary damage
on the wire (KTX deals it through `T_Damage` with the door's activator as
the attacker, `ktx/src/doors.c:68`), so it is NOT routed positionally: the
teamkill anchor simply stops stamping `unknown` over a cause the wire
printed.

The crush population is measured, not assumed: this ground truth carries
**239 wire damage rows with weapon `squish`** — 50 enemy (1 664 bounded),
21 team (297), 31 self (62) and 137 world-attacker (260) — and the print
stream carries 2 "squished a teammate" lines, 12 "X squishes Y" and 2
"X was squished". Over a 6 000-demo archive sweep of the print stream the
same three phrasings run 45 / 351 / 62 lines. Nothing here is a
zero-population engine-only change. `damagerecon`'s
`positionalAnchor` (was `telefragAnchor`) accepts any positional weapon
and prefers the killer the obituary's own recovery named
(`analyzer/teamkill_telefrag.go`) over the track inference it needed for
the killer-less enemy form. Nothing in this package guesses that a kill
was a telefrag — the wire says so.

Measured in isolation on the 60-demo ground truth, per-player relative
error (median / mean), same harness on both sides:

| family | before | after |
|---|---|---|
| bounded given | 0.57% / 0.77% | 0.57% / **0.76%** |
| bounded taken | 0.04% / 0.14% | 0.04% / 0.14% |
| bounded ewep | 0.85% / 1.48% | 0.85% / 1.48% |
| bounded givenTeam | 1.43% / 5.10% | **1.20% / 4.50%** |
| bounded givenSelf | 1.26% / 3.93% | 1.26% / **3.81%** |
| raw given | 0.72% / 1.22% | **0.70% / 1.18%** |
| **raw taken** | 0.68% / 1.14% | **0.48% / 0.95%** |
| raw ewep | 1.19% / 2.28% | 1.19% / 2.28% |
| **raw givenTeam** | 7.40% / 14.83% | **2.91% / 6.76%** |
| raw givenSelf | 1.94% / 4.88% | 1.93% / **4.56%** |

No family regresses; `raw givenTeam`'s p90 falls 42.67% → **17.33%** and
`raw taken`'s ≤1% share rises 63.0% → **75.2%** (the victim side of the
same 99 points). Event coverage, value-exact and attacker-correct are
unchanged to the digit (99.6% / 98.9% / 98.5%), as is every per-weapon
row and the direct/splash table.

In the `-diag` table four flows disappear and none appears: `positional:team
→ team` (10 774 over 99), `→ self` (286 over 2), `→ enemy:sng` (212 over
2) and `→ enemy:rl` (200 over 1) — 11 472 bounded damage over 104
instants, five of which were being charged to an ENEMY or to the victim
themself rather than to the team family, which is where `bounded given`
and `bounded givenSelf` pick up their share of the gain. What remained
was `positional:team → env:unknown`, 216 over 4, recorded here at the
time as "the honest floor of this fix — the wire says a teammate did it
and nothing says which one".

**That framing was wrong, and the four instants split two ways.** Two of
them (10 and 6 bounded) are NON-LETHAL team stomps: `damage.stomps`
carries every `dtSTOMP` row the wire logged, not only the killing ones,
and a stomp that does not kill prints no obituary at all. There is no
cause on the wire for those and `env:unknown` at `world` is the truthful
answer; they are a floor, but of the *eval's* pairing, not of this fix.
The other two (100 bounded each, both at t=0 of one demo — a match-start
spawn pile) DID have obituaries: `frags-final` could not name the killer,
and the unrecovered entry was then **dropped**, so `anyFragAt` found
nothing and the arrival detector was never consulted. That was plumbing,
not evidence. Those obituaries now reach the Result as
`frags.unpaired[]` (schema v74) and the anchor fires on them: the flow is
gone and both instants route positionally, taking 2 × 99 raw points of
corpse clamp off `taken` (`raw taken` median 0.48% → **0.47%**, mean
0.95% → **0.94%**, ≤1% share 75.2% → **75.5%**; every other family
identical to the digit).

**What the anchor may infer on such a row is bounded by what the
obituary established.** A teamkill obituary proves a TEAMMATE of the
victim did it; `teleportArrivalAt` ranks every player on the map, so on a
teamkill row its candidate set is now restricted to the victim's team,
and if no teammate survives the gates the attacker stays `world` — the
delta is still typed positional, but nobody is named. Constraining the
SET rather than vetoing the winner matters: filtering afterwards would
drop a legitimate second-place teammate along with an inadmissible
leader. On these two the constrained detector does name a teammate for
both, and is right on one of the two (a spawn pile puts three teammates
inside the same hull, so co-location cannot separate them). The residual
now shows as `positional:team → positional:team:WRONG-ATTACKER`, 100 over
1: the class and the value are right, the name is not. Note that a team
telefrag costs its killer **no** frag under default KTX rules — the −1 is
gated on `k_tp_tele_death` (`ktx/src/client.c:5348`) — so the frag-penalty
half of `frags-final`'s two-signal recovery is mute for most of them and
co-location is carrying the recovery alone. That is why the residual
looks like a spawn pile.

The golden cache agrees on raw and is a wash on bounded: raw `givenTeam`
**4.24% / 8.35% → 3.73% / 5.39%** (p90 23.01% → **13.53%**, the 65.7% and
39.3% outlier rows gone), raw taken 0.51% / 0.85% → **0.48% / 0.79%**,
raw given p90 2.34% → **2.13%**; bounded `givenTeam` median 2.33% →
2.47% with the mean flat at 5.1% and p90 unchanged — its worst row
(23.06%) is fixed and the median moves because a couple of small rows
swap places, which is what a 53-row median does. Bounded was never the
broken family here: the value was already right on 98 of 98 instants, and
only the handful of instants where a same-frame shotgun candidate had
outscored the anchor change hands.

Two corpora outside the ground truth check the same change where nothing
scores it. The **obituary oracle** over a 400-demo archive sample (the
un-instrumented eras E0–E4) leaves every attribution figure identical —
kill coverage, attacker / weapon / class accuracy, per-era and per-weapon
— and moves exactly the two shares this touches: reconstructed damage
carried in `events` under the team class **2.44% → 2.28%** of bounded
damage, and the UNATTRIBUTED share **1.09% → 0.93%**, improving in every
single era (E0 1.27→1.14, E1 0.87→0.65, E2 0.95→0.78, E3 0.88→0.72,
E4 1.02→0.74). Both movements are the same rows leaving the events log
for `telefrags` — a team telefrag used to be emitted with weapon
`unknown` (the teamkill anchor names an attacker but no cause), so it was
counted as unattributed damage on top of everything else. The
given-vs-taken bookkeeping identity, which was off by 125 points in E3,
now balances exactly in every era. The **188-demo derived-summary
protocol** (`cmd/qw-demoinfo-eval`, 186 scored) is **byte-identical**
except for one cell: a reconstructed `acc.sg.hits` of 181 → 180, the
shotgun hit that had been credited for one of these telefrags.

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
- **`raw givenTeam` was the worst family in this document, and it was
  not a denominator effect — it was 98 telefrags.** The old text here
  recorded the in/out asymmetry (13 738 bounded damage over 235 instants
  INTO the team class against 1 554 over 68 out, 8.8:1) as a confirmed
  pathology of unknown mechanism, with `PHANTOM → team` — 10 775 over 104
  — as its dominant channel. Every one of those instants is classified in
  §"Team telefrags were not damage": 98 are a wire TELEFRAG, 2 a stomp, 5
  a synthesized pentagram row, none an invented hit. Routing them
  positionally takes the family to **2.91% / 6.76%**.
- **What is left in the team family IS the base rate, and the harness now
  prints the denominators that show it.** Re-measured on the current
  pipeline the same asymmetry is 13 903 in over 240 instants against
  1 846 over 89 out; with the positional kills routed it is **3 028 over
  135 against 1 846 over 89, 1.6:1**. The GT class totals put both in
  proportion:
  the team class holds 85 734 bounded damage over 2 172 instants against
  2 298 295 over ~72 000 for the enemy classes. So the leak INTO team
  runs at 0.13% of the enemy classes' damage while the leak OUT runs at
  2.2% of the team class's: per instant the reconstruction is wrong about
  a team hit *more* often than about an enemy one, and the inbound flow
  is still the larger number only because the source class is 27× bigger.
  That is the arithmetic the old "8.8:1, so errors add rather than
  cancel" sentence was missing, and it is why a rule that preferred
  `unknown` over `team` on thin evidence would be paid for out of real
  team damage.
- **The residual channel is the simultaneous shotgunner**, the same class
  §"Given inherits attribution" names — `enemy:sg → team` 2 016 over 98
  instants against `team → enemy:sg` 330 over 18. When a shotgun
  instant's attacker is misnamed, the wrong name is a TEAMMATE of the
  victim on 98 of 365 occasions, **26.8%**. The question is what to
  compare that against, and the first answer this document gave — 50%,
  "what a blind wrong pick would give" — is **the wrong null**. It is
  arithmetically right about the roster (measured over the same 365
  instants: remove the true attacker and the victim's live roster is
  50.5% teammates on average, median exactly 50.0%), but the model never
  picks from the roster. `hitscanCandidates` admits only shooters that
  fired in range, held line of sight and had the victim inside a ~25° aim
  cone, and that pool is plausibly enemy-skewed — in which case 26.8%
  could be *over*-selection rather than the comfortable margin the
  comparison implied.

  Re-running the candidate probe over all 365 instants and recording the
  admitted pool, minus the victim and minus the true attacker — i.e. the
  set the wrong name was actually drawn from — measures the null directly:

  | null | teammate share of the wrong-name pool |
  |---|---|
  | roster availability (the old comparison) | mean **50.5%**, median 50.0% |
  | admitted candidates, any kind | mean **27.8%**, pooled 112 / 394 = **28.4%** |
  | admitted hitscan candidates only | mean **23.9%**, pooled 90 / 367 = **24.5%** |
  | **observed** wrong names that were teammates | **26.8%** (98 / 365) |

  So the honest verdict is **no measurable team selection in either
  direction**: 26.8% observed against a 27.8% admitted-candidate null is a
  wash, and the apparent 23-point safety margin against the roster null was
  an artifact of comparing to a pool the model does not draw from. The
  admission gates, not the scorer, are what keep teammates out — they
  reduce a 50% roster to a 28% candidate pool — and the scorer is neutral
  with respect to team once a shooter is admitted.

  One further measurement sharpens what "misnamed" means here: on **336 of
  the 365** instants the admitted pool contains exactly ONE name other than
  the true attacker (29 contain two, none more). In 92% of these the
  reconstruction is not choosing between rivals at all — the true shooter
  failed a gate, one alternative survived, and it won by default. The
  channel is a coverage problem in the hitscan admission, not a preference
  in the scoring.

- **The rest of the in-flow, enumerated.** The named shotgun channel is
  2 016 of the 3 028 bounded damage flowing INTO the team class (66.6%)
  over 98 of the 135 instants (72.6%). `cmd/qw-recon-eval -diag -flow-min 1`
  prints every remaining flow — the default table stops at 100 damage, which
  is why this list was previously unstated:

  | flow | bounded | instants |
  |---|---|---|
  | `enemy:sg → team` | 2 016 | 98 |
  | `mixed → team` | 252 | 4 |
  | `self → team` | 237 | 9 |
  | `enemy:sng → team` | 180 | 10 |
  | `enemy:lg → team` | 150 | 5 |
  | `enemy:ssg → team` | 128 | 5 |
  | `enemy:rl → team` | 46 | 1 |
  | `env:fall → team` | 10 | 2 |
  | `enemy:ng → team` | 9 | 1 |
  | **total** | **3 028** | **135** |

  Grouping by mechanism rather than by weapon, the picture is the one
  §"Given inherits attribution" already names and nothing else: **2 483 over
  119 (82% of the damage, 88% of the instants) is one attacker confused for
  another** — sg, sng, ssg, ng and lg are the same simultaneous-shooter
  class in five weapons. Of the remaining 545 over 16, `self → team` (237
  over 9) is the close-range rocket self/enemy flip and `mixed → team` (252
  over 4) is a same-frame multi-attacker merge where the single credited
  name landed on a teammate; both are the other two classes that bullet
  lists. `env:fall`'s 10 over 2 is a landing tick charged to a nearby
  player. There is no fourth mechanism hiding under the print floor.

- **The mega-health rot exclusion is bounded to single ticks, and that
  bound is untested off the hub.** `victimDeltas` drops a 1-HP drop on a
  victim above 100 with no armor change as KTX's mega rot rather than
  damage (`deltas.go`). KTX rots 1 HP per second, so two rot ticks landing
  in one broadcast instant present as a 2-point drop and pass straight
  through as damage. Measured on this ground truth, widening the test to a
  2-point drop would be **free**: there are **0** wire damage rows worth
  exactly 2 on a victim above 100 with armor unchanged (nor any worth 3).
  That is not evidence for the change. The corpus is modern hub timing,
  where a broadcast rarely spans two seconds of rot, so it contains neither
  the merged pairs the widened rule would catch NOR the 2-point hits it
  would eat — the zero is the absence of both. The regime where both appear
  is the archive's low-fps recording, and **no oracle covers it**. Unable to
  validate in either direction, the rule stays where the evidence reaches
  and the gap is named here rather than papered over.

  The single-tick rule is not free either, and the same measurement prices
  it: it eats **9 real 1-point wire rows** on this corpus (9 bounded damage
  in total, none of them team-class). That is the cost of not being able to
  tell a rot tick from a 1-point hit, and it is the reason the exclusion is
  as narrow as it is.

## What the reconstruction models beyond the prototype

The 2026-08-11 Python study (.reports/qw-damage-recon-2026-08-11/,
held-out burst-level validation) is the feasibility evidence. The Go
port adds, each verified against ground truth in the eval:

- masked death+respawn recovery (same-instant kill leaves no h row) and
  corpse-cycle spawn-telefrags, with attacker inference from teleport
  arrivals / occupied-pad proximity;
- corpse (gib) hits kept in the raw family only, as KTX does;
- engine-exact radius damage: the falloff first, the self-halving on its
  result, the quad multiplier after both — `q·0.5·(120 − 0.5·d)`
  (ktx/src/combat.c T_RadiusDamage → T_Damage) — the sharp self-vs-enemy
  discriminator in close rocket fights, with candidate admission capped
  at the engine's own `findradius(damage + 40)` reach;
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
  attackers sums to, is split between them (range-midpoint proportional).
  The partner list is now the same set the single pass scores. The two
  families that joined it late measure opposite: LG water discharges
  pair NEVER (over a 2 400-demo archive sweep the split entertains 68
  and pairs 0 — the observation sits BELOW the discharge's band on all
  61 entries, and a pair only ever ADDS damage), while trackless
  TE_EXPLOSION rockets and contact grenades sit ABOVE their band on 480
  of 537 entries and end up in 331 of the sweep's 1 329 pairs. See
  §"Trackless explosions in the pair split" and the `trySplitPair`
  bullet in [`plan-archive-features.md`](../../plan-archive-features.md);
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
the match the wire showed — visible per demo since v74 as
`damage.coverage` (§per-demo coverage: 99.0% of reconstructions read
≥ 0.95, that class 0.182 median, and 0.18% between the two — read the
ratio as a magnitude). Arena-family maps (povdmm4/dmm4*/anarena/midair-style) and
CTF were out of validation scope (study §trust tiers); midair/instagib/
dmgfrags server modes are skipped entirely (no section).
