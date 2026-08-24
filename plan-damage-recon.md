# Plan: reconstructed damage for pre-instrumentation demos

Status: **implemented** (all phases). Branch `reconstruct-damage`, cut from
`main` @ 96e2a9f.

Amendments made during implementation, recorded here rather than silently
rewritten below:

- **§3 `Recon *DamageReconMeta` was NOT added.** The per-demo trust signal
  shipped as documentation (`damagerecon/ACCURACY.md` + the `source` flag)
  instead of a stored meta block; add the block if a consumer needs
  machine-readable per-demo evidence counts.
- **§2 node edges**: final `requires` are `damage, timeline, shots,
  frags:final, metadata, demoinfo` (clock/roster turned out unused — all
  inputs arrive match-relative and team-labeled). Aim/airgibs keep
  binding wire-measured `damage`; player-stats binds `damage:final`
  (see the §7 amendment below).
- **§5 delta upgrades**: pickup-unmasking and merged-rot-tick correction
  were NOT needed — bounded taken hit 0.05% median without them. What WAS
  needed instead (found against ground truth): masked death+respawn and
  corpse-cycle spawn-telefrag recovery, corpse-hit raw accounting,
  engine-true radius damage (self-halving inside the falloff, quad
  after it — the ordering was wrong until §8 lead A), pent-window
  synthesis, nullified-hit raw recovery, LG discharge modeling, and
  model-based overkill top-up on kills.
- **§4 burst-level metrics** are not in the Go eval yet; burst parity
  rests on the study's oracle test (4398/4398) plus the shared
  view-layer `killBurstFor` reading our events.
- **§7 partially reversed on request**: playerStats DOES consume the
  reconstruction now (binds `damage:final`, `src: "reconstructed"`) —
  it aggregates exactly the per-player totals the eval validates.
  Aim/airgibs stay wire-only as planned (per-shot attribution is a
  different evidence grade).

| phase | scope | state |
|---|---|---|
| 1 | `damagerecon` package: delta extraction + attribution port (Go) | done |
| 2 | eval harness vs KTX ground truth (modern demos) | done — `cmd/qw-recon-eval` + `damagerecon/eval_test.go` |
| 3 | accuracy iteration to ~1% per-player error | done — medians: bounded given 1.08% / taken 0.05%, raw given 1.32% / taken 0.93% (`damagerecon/ACCURACY.md`) |
| 4 | wire in: post-processor node, `source` flag, schema v71 | done — node `damage-recon`, `damage.source`, OpenAPI + view pass-through |
| 5 | docs + goldens + old-corpus smoke | done — goldens regenerated (source:ktx only), 10/10 old demos reconstruct, NaN ingestion guards applied |

## 1. Why

~45% of the QW demo archive predates the KTX `mvdhidden_dmgdone`
instrumentation: `res.Damage == nil`, so `/damage`, top-kills, lives and
top-windows damage figures 422 on those demos. A 2026-08-11 feasibility
study (`.reports/qw-damage-recon-2026-08-11/REPORT.md`, Python prototype
validated blind against modern ground truth) proved the damage log is
recoverable from spectator-visible state: the health/armor change streams
are per-hit (change-driven at server frame rate), the observed delta IS
the KTX bounded value, and attribution has rich evidence (LG beams,
projectile entity flights, fire sounds, position/view/velocity tracks,
the frag log).

Goal for this plan: reconstructed per-player damage totals within **~1%**
of what KTX would have recorded, both **raw** and **bounded** families,
with an explicit provenance flag so API consumers can always tell
reconstructed figures from KTX-instrumented ones.

Baseline measured with the prototype on the 13 golden-cache modern demos
(per-player bounded totals, players with >= 200 GT damage):

| metric | median | mean | share <= 1% |
|---|---|---|---|
| taken | 0.07% | 0.30% | 92% |
| given | 1.08% | 1.71% | 45% |

Known error classes from that baseline, each with a fix in phase 3:
- **delta extraction** (affects taken + given): same-instant death+respawn
  eats the killing delta (telefrags: 100 HP each); a same-frame item
  pickup masks part of a hit's h/a drop (health box +25 exactly offsets);
  a merged mega-rot tick adds ±1.
- **attribution** (affects given only): close-range rocket self/enemy
  ambiguity — the prototype's flat SELF_PEN prior trades recall for
  inflation and cannot reach 1% (swept: duel median 1.8–3.3% at any
  setting); needs geometric disambiguation (BSP `CanDamage` splash gate,
  trackless-rocket aim trace, per-demo rocket-damage-constant detection).

## 2. Architecture

**`mvd-analytics/damagerecon/`** — a pure compute package, mirroring
`aimcore`: `damagerecon.Compute(res *result.Result, opts Options)
(*result.DamageResult, error)` reads only the assembled Result sections
(`Streams`, `Shots`, `Frags`, map name for BSP) and returns a
`DamageResult` shaped exactly like the KTX-derived one. Keeping it a
public function lets the eval harness call it on modern demos (which have
both the streams and the KTX truth) without any registry mode.

**`damageReconPost`** — a thin post-processor (like `aimPost`) declared
in `dag.go` as node `damage-recon`, requiring `damage`, `timeline`,
`shots`, `frags:final`, `clock`, `roster`, `metadata`. It fills
`result.Damage` **only when `res.Damage == nil`** (never overrides KTX
data) and stamps provenance. Because the view layer (`view.Damage`,
top-kills, lives, top-windows) reads `r.Damage` from the assembled
Result, every damage-shaped endpoint starts working on old demos with no
further changes.

Ground-truth hygiene: the compute package must not read any
damage-derived field — `Shot.Hit/Victims/VictimKinds` and the per-weapon
accuracy fields are damage-derived and off limits (the prototype's
leakage audit lists the allowed inputs).

**Inputs availability.** Beams/projectiles land in `Streams` only when
`Registry.BuildShotStreams` is on (mvd-api and the web WASM always set
it; the CLI needs `-include projectiles,beams`). The post-processor
reads `Streams.ShotStreamsComputed`; without spatial streams it does not
attempt a degraded reconstruction — it skips, and the CLI's damage-view
error message points at the flags. BSP is best-effort: with
`mapbsp.LoadBytes` returning nil the BSP tier is skipped (documented
accuracy cost, recon meta records it).

## 3. Result schema (v71)

- `DamageResult.Source string json:"source,omitempty"` — `"ktx"` (set by
  the existing DamageAnalyzer) or `"reconstructed"` (set by the recon
  post). Follows the `BoundedSource` vocabulary. Absent only in
  pre-v71 stored results.
- `DamageResult.Recon *DamageReconMeta json:"recon,omitempty"` — present
  only on reconstructed results: counts of attributed / unattributed
  events + damage, frag-anchor rate, evidence classes available
  (beams/projectiles/bsp), per-demo rocket-damage regime. This is the
  trust signal consumers were promised.
- Raw family on reconstructed demos: raw = uncapped observed delta
  (health drop including the negative death value + armor drop). Exact
  down to the -99 corpse clamp; deeper overkill is unrecoverable and
  stays at the clamp (same limitation the KTX path documents for its
  shadow fallback).
- Bounded family: armor drop + health share capped at remaining health
  (corpse hits 0) — definitionally KTX `dmg_dealt`.
- `BoundedMode` semantics unchanged; the same `skipped:*` server modes
  skip reconstruction of the bounded family.
- View plumbing: `view.Damage` passes `Source`/`Recon` through on both
  paths; the summary-path KTX-scoreboard substitution (BoundedSource)
  is unreachable on reconstructed demos (no demoinfo) but must not
  mislabel. Top-kills/lives/top-windows envelopes: `MeasuredSources.
  Damage` stays true only for wire-measured damage; a parallel
  `damageSource` string rides beside it.

## 4. Eval harness (phase 2)

`mvd-analytics/cmd/qw-recon-eval`: for each demo in a directory
(default `mvd-analytics/testdata/cache/`), run the full pipeline, keep
the KTX `DamageResult` as ground truth, re-run `damagerecon.Compute` on
the same Result, and score:

- per-player totals (bounded + raw; given / taken / givenTeam /
  givenSelf / ewep): relative error distribution (median / mean / p90 /
  share <= 1%), the headline goal metric;
- per-event: same-instant matching rate, value-exact rate, attacker
  attribution accuracy;
- burst-level (top-kills parity): within-5% rate and juicy-burst
  precision/recall, so the totals tuning cannot silently regress the
  highlight-selection quality the study validated.

A companion test (`damagerecon/eval_test.go`, skipping when the golden
cache is absent, `-count=1` semantics like the corpus package) pins
loose lower bounds so regressions surface in `make test`.

## 5. Reconstruction algorithm (ported + upgraded)

Step 1 — deltas per victim from merged h+a change streams: drops =
damage; filter mega-rot ticks, spawn resets, pickups; killing hit's
health share capped at remaining health (bounded) and extended to the
death value (raw). Upgrades over the prototype:
 - death+respawn same-instant recovery (frag log names the kill; charge
   the victim's pre-instant h+a; telefrag = armor + remaining health);
 - same-frame pickup unmasking: a pickup rise merged into a hit frame is
   detected against the item-pickup hints/intervals and the h/a rise
   re-added to the hit's delta;
 - merged rot-tick correction (h>100 and a rot tick due at that instant).

Step 2 — attribution per delta, candidates scored by geometry + damage
model (all prototype rules), plus:
 - frag anchor at the exact instant (positional kills excluded);
 - LG beam segment proximity; projectile end splash; hitscan sound +
   aim cone; nail flight time; trackless-rocket sound fallback;
 - **BSP tier**: `bspvis.RayHitsSolid` `CanDamage`-style gate on splash
   candidates (blocked splash = infeasible), aim-ray trace to predict
   trackless-rocket explosion points, liquid/floor classification for
   environmental deltas;
 - **per-demo rocket-damage regime**: fixed-110 servers detected in one
   pass sharpen the splash magnitude model;
 - self/enemy prior re-tuned against per-player-total error once the
   geometric gates are in.

Step 3 — aggregation through the exact `DamageAnalyzer.Finalize`
bucket semantics (Given/Taken/Team/Self/Env, ByWeapon, Matrix, EWep,
telefrag/stomp folds) so reconstructed and KTX results are
shape-identical and comparably windowed (match-gated, match-relative).

## 6. Accuracy target

Per-player bounded **given** and **taken**: median <= 1% on the modern
validation corpus, mean reported honestly alongside. If a slice stays
above (the study flags arena maps), the recon meta carries the tier
verdict rather than the numbers being quietly filtered — surfacing over
filtering, per repo policy.

Final numbers live in `damagerecon/ACCURACY.md` together with the eval
command lines that reproduce them.

## 7. Out of scope (recorded so they aren't half-done silently)

- Feeding reconstructed damage into aim splits, airgibs or the
  playerStats damage family (each changes existing consumer semantics;
  needs its own provenance decision — `SrcReconstructed` next to
  `SrcDerived`/`SrcKTX` is sketched but not wired).
- CTF / midair / instagib demos (study: unmodeled or GT-refused).
- The mvdsv/KTX puppet re-simulation endgame (REPORT.md lead #5).
- Hub index build over the 23k-demo archive (consumes this, lives
  outside the repo).

## 8. Open leads with evidence (2026-08-24, from the direct-impact review)

Two engine facts found while reviewing the direct/splash classifier.
**Both SHIPPED 2026-08-24 in one calibration pass** — see the disposition
under each and the tables in `damagerecon/ACCURACY.md` §"The quad
multiplies AFTER the falloff, and splash stops at 160 units". The
evidence they were recorded with is kept verbatim below so the fix can be
read against what motivated it. A third lead the pass opened is at the
bottom.

### Lead A — the quad multiplier is applied AFTER the radius falloff

`T_RadiusDamage` calls `T_RadiusDamageApply` with a flat base (120 for
both projectiles, `ktx/src/weapons.c:1006`, `:1300`), which computes
`points = damage − 0.5·dist` (`combat.c:1189`) and only then hands
`points` to `T_Damage`, where the quad multiplier is applied
(`combat.c:537-543`). A quad splash is therefore **4·(120 − 0.5·d)**,
not `4·120 − 0.5·d`.

`modelBounds`, `topUpKillRaw` and `pentSyntheticEvents` all use the
second form, and `attribution.go` states the first as an engine fact in
a comment. It is not one.

**Measured** on the 53-demo dm2/dm3 ground truth: 898 wire `rl` rows
flagged splash whose attacker held the quad. `trap_findradius` caps
splash reach at `damage + 40` = 160 units (`combat.c:1252`), so under
the model in the code every one of those rows would have to read
≥ 400 (`480 − 0.5·160`). The histogram:

| value | rows | share |
|---|---|---|
| 40–119 | 20 | 2.2% |
| 160–399 | 861 | 95.9% |
| 400–439 | 17 | 1.9% |

which is the engine form's range `[4·(120−80), 4·120) = [160, 480)`
almost exactly, and not the code's `[400, 480)`. (The 20 rows under 160
are quad-interval boundary noise: `120 − 0.5·d` on an unquadded hit
lands in exactly that band.)

**Disposition: SHIPPED 2026-08-24.** `modelBounds`, both kill top-ups,
`pentSyntheticEvents` and the LG discharge model now put every
composing multiplier on the engine's side of the falloff
(`splashModel`), and the KNOWN-WRONG comment is gone.

The re-tune the lead asked for inverted its own precedent: `topUpBase`
110 / `topUpSlack` 60 existed only to absorb this error, and with the
order corrected the raw error falls MONOTONICALLY as the pair approaches
the engine's own numbers — swept on the 30-demo dm3 half and confirmed on
the held-out dm2 half, raw given mean per-player error 2.87% → 1.16%
(dm3) and 3.44% → 1.38% (dm2) going from (110, 60) to (120, 0). Both
constants are deleted; the top-up is the engine formula at the measured
distance. The band slacks (24 snapped / 60 not) swept flat over 12–36 and
keep their derived values.

Measured on the full 60-demo corpus: raw given median/mean 1.24%/2.04% →
**0.74%/1.24%**, raw taken 1.74%/2.29% → **0.62%/1.11%**, raw ewep 1.58%
→ 1.14%, raw givenSelf mean 8.16% → 5.81%; the bounded family is
untouched by this half (isolation run with admission held at 380
reproduces the old bounded numbers exactly). Aim's recon tier: rl 0.7pp →
0.5pp, ssg 1.8pp → 1.6pp.

### Lead B — splash reach is 160 units, not `rSplash` 380

Same line: `trap_findradius(world, inflictor->origin, damage + 40)`
(`combat.c:1252`) is the ONLY set of entities `T_RadiusDamage` visits,
so a 120-base explosion cannot damage anything past 160 units — and the
cap does not scale with quad, since the quad is applied downstream in
`T_Damage`. `attribution.go`'s `rSplash = 380` admits projectile
candidates out to 380, so every candidate between 160 and 380 is one the
engine could not have produced.

**Disposition: SHIPPED 2026-08-24, with the trade measured in both
directions.** Admission is now `splashAdmit(epExact)` = the engine reach
plus the same slack the damage-model band uses for that endpoint kind:
**184** units from an explosion-snapped detonation point, **220** from a
tracked flight's last broadcast position (which can sit a server frame —
34 units at 1000 ups — short of the real one). The LG water discharge
had the identical defect and now carries `dischargeReach` = `35·cells +
40` + the same slack.

The slack was derived by measurement, not chosen: for a lone wire splash
row the value IS the engine distance (`unbound_dmg_dealt = ceil(q·(120 −
0.5·d))`), and over 14 140 wire-flagged enemy rl splash rows our distance
runs median **+4.8** off it — exactly the bbox-centre (+4) and
pull-back (+8) pair the lead predicted — with p95 18.1 and p99 27.4
(golden cache 5.0 / 22.2 / 35.2). 184 keeps 99.76% of those rows against
380's 99.78%.

The trade came out as the lead expected, small and two-sided. Cost: 3
rows in 14 140 lose their only candidate (`enemy:rl → env:unknown` 11 →
13 instants), which reads as bounded given mean 0.76% → 0.80%,
`givenSelf` 3.90% → 4.38%, and `acc.rl.hits` 1.25% → 1.34% on the
188-demo protocol. Gain: `enemy:sg → self` 800 → 504 damage, `self →
enemy:rl` 1 288 → 1 137, bounded `givenTeam` median 1.86% → **1.39%**,
attacker-correct `ng` 96.4% → **97.2%** / `sng` 97.5% → **98.0%** / `ssg`
98.1% → **98.3%**, and on the 188-demo protocol `dmg.given` 0.47% →
**0.45%**, `dmg.taken` 0.44% → **0.42%**, `acc.gl.hits` 3.91% →
**3.55%**, `acc.lg.hits` 0.91% → **0.87%**. Taken deliberately: the
candidates it removes are ones the engine could not have produced.

On the un-instrumented archive, where the obituary oracle is the only
measurement available (285 demos of a 300-demo sample, obituaries
withheld), the same trade prices a little higher: attacker-correct
−0.0 to −0.2 pp per era and the unattributed bounded-damage share 1.29%
→ 1.43% at E0, 0.64% → 0.70% at E5 — with the losses on rl/gl and the
gains on sg/sng, and the refused damage published as `unknown` rather
than misattributed. The un-snapped-endpoint radius (220) turns out to be
inert: holding it at the old 380 reproduces the oracle and the 188-demo
protocol byte for byte, because ~99% of demos carry TE_EXPLOSION and
essentially every deciding candidate has an exact detonation point.

### Lead C — the quad is an OCTA in deathmatch 4, and dmm4 is not skipped

Opened by the multiplier audit above. `T_Damage` multiplies by
`(deathmatch != 4 ? 4 : tot_mode_enabled() ? FrogbotQuadMultiplier() : 8)`
(`ktx/src/combat.c:541`), so on a `deathmatch 4` server a quad hit is
×8 — and dmm4 is not one of `SkipModeReason`'s modes, so such a demo is
reconstructed with every quad hit modelled at half its true value.
`quadFactor` says so in a comment and does nothing about it.

Not implemented because the population is unmeasured: neither ground-truth
corpus contains a dmm4 recording, so there is nothing to validate a fix
against, and the archive's `deathmatch` serverinfo key has not been
censused. The order of work is that census first (the arena-family maps
povdmm4/dmm4* are the obvious place to look), then a decision between
modelling the ×8 and adding dmm4 to the skip gate — the same choice the
other unobservable multipliers already made, since the CTF strength rune
and the KTX handicap are also applied inside `T_Damage` and neither is on
the wire at all.
