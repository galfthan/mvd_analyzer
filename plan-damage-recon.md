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
  engine-true self-splash halving + quad-before-falloff, pent-window
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
