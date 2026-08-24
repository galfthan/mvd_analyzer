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
**0.58%** (mean 0.76%, p90 1.68%, ≤2% 94%), bounded taken 0.04%, raw
given 1.24%, raw taken 1.74%.

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
| ssg | 86 | 4 339 | 71.9% | 70.1% | 0.0pp | 1.8pp | −1.8pp | 67% | **exact** |
| axe | 18¹ | 1 417 | 8.0% | 7.8% | 0.0pp | 0.6pp | −0.6pp | 83% | 0.1pp |
| rl | 303 | 35 135 | 47.9% | 48.4% | 0.0pp | 0.7pp | +0.4pp | 90% | +0.4pp |
| gl | 109 | 4 948 | 15.0% | 15.1% | 0.0pp | 0.4pp | +0.0pp | 92% | +0.1pp |

¹ axe at the ≥ 20-swing threshold like every other row; the wider ≥
10-swing view (38 rows) reads the same to a tenth of a point. The golden-corpus cache (13 demos) reproduces the
shipped rows within 0.7pp (lg 0.1pp, sg 1.5pp, ssg 1.0pp, axe 0.0pp,
rl 0.6pp, gl 0.2pp).

The ng/sng rows are scored by the harness itself — no source edit needed
— through `aimcore.ReconHitsForEval`, which runs the join for every
weapon rather than only the shipped ones: what a weapon's join costs is
the measurement that DECIDES whether it ships.

**Shipped: lg, sg, ssg, axe** — the weapons whose damage lands in the
fire's own server frame (the axe at its fixed +200 ms traceline delay).
On those the control is exact to the last row — the join reproduces the
measured counters from the wire log with zero error — so the whole
residual is the reconstruction's, and it is ≤1.7pp mean with a small
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
7.4pp → **0.7pp** (bias +0.4, 90% of rows within 2pp), gl 1.3pp →
**0.4pp**, both inside the ≤1.8pp band the hitscan tier ships at.
(v73 measured rl at 0.6pp and gl at 0.3pp; the v74 direct-impact work
moved the damage model — see §"The rocket splash base is 120" — and
these are the numbers after it.)

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
open maps. Both known routes into the +0.4pp adoption bias should run
HIGHER elsewhere, i.e. treat +0.4pp as a floor rather than a bound:
tight-quarters maps (dm4, aerowalk) produce more point-blank rockets,
whose entity the server never broadcasts — the untracked flights whose
damage rows are exactly the adoption fodder; and a SURVIVED lava tick
(10–30, overlapping moderate rocket splash) is not obituary-anchored the
way a lethal one is, so where the attribution prefers a rocket to the
env candidate the row can be adopted by a missed flight nearby. The 9%
of rl rows outside 2pp is plausibly this population, and it is one-sided
(+): both mechanisms invent hits, neither destroys one.

Bounded, not confirmed: the 13-demo golden cache spans dm4, dm6, e1m2,
aerowalk, obsidian, schloss, skull and bravado besides dm2/dm3, and
reproduces rl at bias +0.4pp / mean 0.6pp and gl at −0.0pp / 0.2pp —
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
| `damage.given` (reconstructed) | 665 | 6.2% | **0.47%** |
| `damage.takenEnemy` (reconstructed) | 663 | 9.4% | **0.44%** |
| `pickups.quad/pent/ring.took` | 273/80/78 | **100.0%** | 0.00% |
| `hold.powerups.quad.ms` (to the second) | 273 | 96.0% | 0.07% |
| `hold.powerups.pent.ms` | 80 | 82.5% | 0.49% |
| `hold.powerups.ring.ms` | 78 | 94.9% | 0.09% |
| `accuracy.lg.attacks` | 436 | 98.4% | 0.00% |
| `accuracy.rl.attacks` | 638 | 99.8% | 0.00% |
| `accuracy.gl/ng/sng.attacks` | 424/139/336 | 100.0% | 0.00% |
| `accuracy.lg.hits` (recon tier) | 436 | 65.8% | **0.91%** |
| `accuracy.rl.hits` (recon tier, directImpact — v74) | 638 | 46.9% | **1.23%** |
| `accuracy.gl.hits` (recon tier, directImpact — v74) | 424 | 84.7% | **0.61%** |
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

- `accuracy.sg/ssg.attacks` and `.hits` — KTX counts PELLETS on both
  sides of its ratio, this pipeline counts trigger pulls and fires that
  connected. The observed ratios (83% / 93% aggregate) are exactly the
  6-and-14-pellet spreads.
- `accuracy.rl/gl.anyDamage/recon` — the convention a reconstructed row
  no longer publishes for those two, kept as a diagnostic: a fire that
  landed damage by ANY path, which reads ~4x above KTX on rl (365%
  aggregate) and ~1.5x on gl (55%). Until v74 that WAS the published
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

**The direct-damage constant is era-dependent, and the era is measured
rather than assumed.** KTX has dealt a flat **110** since commit
`c7263e8f` (2008-09-29, qqshka, "this way it better (tm)"), which
replaced id1's `100 + g_random()*20`; v1.35 and earlier still roll it,
v1.36 (2009-09-24) and later do not. `detectRocketRegime` decides per
demo from the demo's own hit distribution, so no version string is
consulted and a pre-1.36 recording simply falls back to the trajectory
alone, where the prior says nothing.

**Two things the trajectory does NOT need**, both refuted by
measurement rather than by argument, and both documented at their
constants in `direct.go`: an EXCLUSIVITY pass across an explosion's
victim set (the invariant is real — one explosion touches at most one
player — but the wire violates it 0 times and this classifier 2 times in
3 525 explosion groups), and the GRENADE FUSE (a flight ending before
the 2.5 s fuse ended on a player; sound, but it moved the derived gl
counter from 0.36% to 3.57% aggregate error, because our flight bracket
is entity visibility rather than the fuse).

**Per-explosion accuracy**, against the wire splash flag, over the
53-demo dm2/dm3 ground-truth corpus (`cmd/qw-recon-eval`, the
direct/splash block). `rl` only: the wire has no gl ground truth to
score against, since `GrenadeTouch` does all its damage through
`T_RadiusDamage` and every wire gl row is splash-flagged whether or not
the grenade touched anybody.

| | paired instants | classification accuracy | precision | recall | direct-count error |
|---|---|---|---|---|---|
| endpoint within 48 u | 18 032 | 73.5% | 45.1% | 96.7% | +114% |
| trajectory + magnitude | 18 039 | **97.9%** | **94.6%** | **95.9%** | **+1.4%** |

**Per-player accuracy**, against the verbatim KTX block on the 188
instrumented archive demos (`cmd/qw-demoinfo-eval`, withhold-and-compare).
Since v74 a reconstructed row PUBLISHES the direct-impact count for
rl/gl, so the shipped comparison is `acc.rl.hits` / `acc.gl.hits`
themselves; `anyDamage/recon` is the convention those rows no longer
carry, kept in the harness so the gap stays measured.

| column | rows | exact | aggregate | bias / row | p90 error |
|---|---|---|---|---|---|
| `acc.rl.hits` (directImpact, shipped) | 638 | **46.9%** | **1.23%** | −0.13 | 2 |
| — where the demo established the 110 constant | 567 | 45.9% | **0.62%** | −0.07 | 2 |
| — where it did not | 71 | 54.9% | 17.7% | −0.58 | 2 |
| `acc.rl.direct/wire` (control) | 638 | 100.0% | 0.00% | 0.00 | 0 |
| `acc.rl.anyDamage/recon` (not published) | 638 | 1.7% | 365% | +37.2 | 69 |
| `acc.gl.hits` (directImpact, shipped) | 424 | **84.7%** | **0.61%** | −0.01 | 1 |
| `acc.gl.anyDamage/recon` (not published) | 424 | 43.2% | 55% | +1.06 | 4 |
| `acc.lg.hits` (the shipped benchmark) | 436 | 65.8% | 0.91% | −0.95 | 3 |

Both land inside the band the already-shipped `lg` row occupies, which
is the bar this family ships at — so both projectile weapons publish
`hitsConvention: "directImpact"` on a reconstructed row and the
`byWeapon` map holds one convention per weapon, exactly as a KTX row
does. A `derived` row (wire damage log, no KTX block) keeps `anyDamage`
on rl/gl: its `hits` is also the aim section's MEASURED counter, a
validated any-path number that nothing asked to redefine.

**How much the magnitude prior carries, and what happens without it.**
`detectRocketRegime` establishes the constant on 567 of the 638 rows
here, and the reconstruction publishes what it found as
`damage.rocketDirectDamage` so a consumer can see which half a row is
in. The split above is the reason nothing is GATED on it: the 71 rows
where the regime is unestablished are the LOW-ROCKET ones — a demo needs
several near-direct hits before the constant is measurable — and they are
*more* often exact (54.9%) with the same p90 error of 2, the 17.7%
aggregate being small absolute errors over small counts. Withholding
there would substitute the any-path count, which is four times KTX's.

That is not the same population as a genuinely PRE-1.36 server, which
this corpus cannot contain: every demo carrying a wire damage log
post-dates the fixed constant by years, so the vanilla `100 +
g_random()*20` era has no ground truth anywhere and the honest statement
is a bound, not a measurement. Forcing the prior off — the closest
available proxy — moves rl from 0.82% to **13.9%** aggregate on a
59-demo subset of this population. Read the trajectory-only figure as
what a pre-1.36 recording gets, and `rocketDirectDamage`'s absence as
the flag for it.

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
touch leaves no non-splash row on the wire either. The wire answers rl's
question and not gl's; the reconstruction answers both.)

### The rocket splash base is 120, not the direct 110 (2026-08-24)

Found while building the classifier above, and worth stating separately
because it moved the damage numbers. `T_MissileTouch` passes **120** to
`T_RadiusDamage` (`ktx/src/weapons.c:1006`), the same base as a grenade;
the 110 is the touch value and belongs to a disjoint victim (the touched
player is the radius pass's `ignore`). This package modelled rocket
splash at the direct constant, understating every rocket splash by 10.

Measured on the dm2/dm3 ground truth: `value + 0.5·dist` over 2 530
wire-flagged splash rows has median **122.4**, not 112 — the residual
2.4 being our distance measured from the pulled-back TE_EXPLOSION point
to the track origin rather than from the pre-pull-back origin to the
bbox centre. Correcting the model improved the bounded family on the
53-demo corpus (given mean 0.81% → 0.76%, ewep 1.57% → 1.49%,
givenSelf 4.19% → 3.90%) and the derived summary against the KTX block
(`dmg.given` 0.49% → 0.47%, `dmg.taken` 0.46% → 0.44%).

The KILL raw top-ups deliberately did NOT follow it (`topUpBase`,
`attribution.go`). A top-up only ever RAISES a value, so its floor must
under-estimate; feeding it the true base multiplied the 10-point gap by
the quad on exactly the quad-rocket kills it exists for and cost raw
accuracy measurably (raw given 2.01% → 2.65% mean per player). The floor
stays at the pair the eval corpus calibrated it at, and says so.

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
the match the wire showed — visible per demo since v74 as
`damage.coverage` (§per-demo coverage: 99.0% of reconstructions read
≥ 0.95, that class 0.182 median, and 0.18% between the two — read the
ratio as a magnitude). Arena-family maps (povdmm4/dmm4*/anarena/midair-style) and
CTF were out of validation scope (study §trust tiers); midair/instagib/
dmgfrags server modes are skipped entirely (no section).
