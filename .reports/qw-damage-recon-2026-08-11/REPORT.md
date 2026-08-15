# Damage reconstruction from pre-instrumentation QW demos — verdict: GO

**Study date:** 2026-08-11. **Handoff:** `docs/damage-reconstruction-handoff.md`.
**Method:** blind reconstruction validated against real `damage.events` ground truth on
modern demos (in-sample 43 + held-out 56), a cadence-degradation test, a 97-demo
old-corpus coverage study, and three independent adversarial review passes
(code/leakage, statistics, domain transfer). All numbers below survived review.

## TL;DR

**The ~45% `no_damage` half of the archive is recoverable to full burst quality.**
Health/armor deltas + spectator-visible evidence (LG beams, rocket entity flights,
fire sounds, positions/view/velocity, the frag log) reconstruct the same
`topKills` fields the index uses, at fidelity that preserves the selections and
rankings the frag-quality filters make:

- **Held-out** (56 fresh demos, 7,878 ground-truth burst rows, zero tuning overlap):
  burst damage **90.2% exact, 95.0% within-5%**¹, pooled Spearman **0.975**.
- **juicy-RL selection (rl, dmg≥180, returnDmg≥50): 97.2% precision / 92.6% recall**
  held-out (98.0/94.8 in-sample); shaft (lg≥150) ≈ 98–99% both. Stable under
  threshold sweeps (precision 94–97%, recall 90–95% across 150–250 dmg / 0–100 rd).
- **Old-demo recording cadence costs ~1 point** (modern demos degraded to 51 ms
  frames, scored vs unquantized GT: 92.6% within-5%, Spearman 0.977).
- **The damage physics of the old era are identical** (measured on ~89 old demos):
  LG = exactly 30 with the modern armor splits (24,6)/(18,12)/(9,21); rocket splash
  follows D−0.5·dist; no svc_playerinfo gaps; per-weapon burst distributions match
  modern ground truth (rl p50/p90: 100/214 vs 100/213).

¹ within-5% = |Δdamage| ≤ max(5, 0.05·GT). Denominators are matched rows;
unmatched exclusions are 0.1% (validation) / 0.5% (degraded run).

## Why it works — three structural facts

1. **The h/a streams are per-hit, not sampled.** `svc_playerinfo` is change-driven at
   server frame rate: 100% of ground-truth damage events have an exact-same-timestamp
   health or armor change; frag times align with the killing h-drop 129/129 on the
   calibration demo; frag-anchor rate across 85 old demos: p50 = 100%, p10 = 99.2%.
2. **The observed delta IS the bounded damage.** KTX-scoreboard "bounded" = armor
   absorbed + health share capped at remaining health — definitionally what the
   streams record. No damage model is needed for magnitude (Q2: answered, bounded
   recovered exactly). Since bounded == raw for every surviving hit, magnitudes also
   discriminate weapons sharply (an LG cell is exactly 30, a rocket 100–120 − 0.5·dist).
3. **Attribution has rich spectator-visible evidence in old demos too:** TE_LIGHTNING2
   beam segments (exact frame, victim lies on the segment), rocket/grenade entity
   flights (spawn+end, position+time), `svc_sound` weapon fire, full
   position/view/velocity tracks, and the frag log anchoring every kill. All verified
   present in the `no_damage` demos (projectiles 85/85; beams exactly where LG was
   fired; positions/alive 85/85).

## Algorithm (implemented in `recon.py`, ~600 lines, no Go changes needed)

1. **Delta extraction** per player from merged h+a change streams: drops = damage.
   Filter mega-rot (−1/s while h>100), spawn resets, pickups (rises). Health share
   capped at remaining health (death → negative h down to −99); corpse hits → 0.
   → per-instant bounded damage.
2. **Attribution** per delta — candidates scored by geometry + damage-model consistency:
   - frag at the same instant → killer/weapon (anchor); positional kills
     (tele/stomp/squish) excluded per topKills vocabulary
   - **lg**: same-frame beam whose segment passes within ~90u of the victim
     (p99 measured 60u); attacker = player at the beam start
   - **rl/gl (tracked)**: projectile ending within splash radius at the same frame;
     shooter resolved by spawn proximity + **aim-vs-flight-direction** (rockets fly
     where aimed) + fire-sound match
   - **rl (trackless)**: point-blank rockets explode before the entity is broadcast —
     fire sound + flight-time consistency (1000 ups) + aim gate, restricted to shots
     with no tracked projectile
   - **sg/ssg**: same-frame fire sound + aim cone (real hits p95 < 24°)
   - **ng/sng**: fire sound + flight-time consistency
   - **damage-model score**: expected magnitude per candidate — LG 30/cell (down to
     armor-only 9/18/24 for health-nullified server modes), splash D−0.5·dist enemy /
     D−0.25·dist self, pellet ranges, ×4 under quad (from `q` intervals). This is what
     disambiguates close-quarters rocket fights (whose explosion was it).
   - **knockback**: victim velocity-delta direction vs candidate explosion position
   - no candidate → environmental/unknown, excluded from bursts (measured: these are
     lava/fall/drown ticks — the correct behavior; GT excludes them too)
3. **Burst assembly**: `killBurstFor` parity — backward walk over killing-weapon hits,
   3000 ms capture gap vs earliest, life-start clip from `alive`, anchor required at
   the exact frag time, victimWep from held-weapon intervals, returnDamage over the
   closed 4000 ms window. **Parity proven by oracle test: fed the real damage log, the
   Python walk reproduces `-view top-kills` 4398/4398 rows with zero mismatches** on
   damage, hits, spanMs, maxGapMs and returnDamage.

## Validation detail

**Event level (in-sample, all modes):** 97.8% of GT enemy damage-event groups matched
by a same-instant delta; 98.0% value-exact; attacker correct 99.2% (duels 100%).

**Burst level:**

| set | rows | dmg exact | within-5% | Spearman (pooled) |
|---|---|---|---|---|
| in-sample (43 demos) | 4,395 | 88.8% | 93.8% | 0.972 (rl) |
| **held-out (56 demos)** | 7,878 | **90.2%** | **95.0%** | **0.975** |
| cadence-degraded 51 ms | 4,374 | 86.9% | 92.6% | 0.977 |

Cross-demo "best of month" case: GT-top-50 RL bursts median rank displacement 5;
top-100 overlap 95/100 (top-20 overlap 13/20 is tie-churn — the GT top is saturated
with 450-damage ties and recon values sit within ±10 there).

**Hard tail (from the statistics review):** arena-family maps (povdmm4/dmm4/anarena/
"end") run ~82.7% within-5% vs 94.7% elsewhere — constant close-quarters rocket
ambiguity plus the DMM4 health-nullification rule. Per-demo Spearman still ≥0.85 on
97/99 validated demos. Duels vs team: 92.9% vs 95.1% within-5% held-out; no mode
collapses.

**Target-segment cut (2026-08-11, added on request): basic 1on1 / 2on2 / 4on4 with
arena games excluded** (arena = povdmm4/dmm4*/aztekdmm4/anarena/midair/arena*/end) —
pooled over in-sample + held-out (91 duel/team demos, 10,843 GT rows; `seg_eval.py`):

| segment | rows | dmg exact | within-5% | Spearman | juicy-RL prec/recall |
|---|---|---|---|---|---|
| all duel+team incl. arena | 10,843 | 89.2% | 94.4% | 0.976 | 97.6% / 93.7% |
| core: arena excluded | 10,083 | 90.7% | 95.0% | 0.973 | 97.7% / 91.6% |
| — core 1on1 | 1,551 | 89.6% | 93.9% | 0.968 | 98.9% / 89.1% |
| — core 2on2/4on4 | 8,532 | 90.9% | 95.2% | 0.974 | 97.0% / 93.1% |

Team modes are the strongest slice — the handoff's Q4 worry (attribution in
many-player games) turned out backwards; duels concentrate the one hard case
(close-range mutual rocket fights). Note the arena-map "end" demos (excluded above)
showed poor per-burst damage accuracy (60% within-5%, n=2) yet near-perfect juicy-RL
selection (100%/98%) — selection quality is more robust than per-burst value accuracy
where every kill is a big contested burst.

## Coverage of the old corpus (97 no_damage demos)

- 85/97 reconstruct now at the fidelity above (anchor p50 100%, 94% of events / 98%
  of damage attributed, burst rows ≈ 1.0 per enemy frag).
- 6/97 (~6%) hit an analyzer bug — `json: unsupported value: NaN` when shot streams
  are requested. Fix the NaN guard in `buildSpatialStreams` output (or fall back to
  sound-only attribution) before the index build, else these silently vanish.
- 2/97 have frags but no player streams: kill-level only, exclude from this tier.
- ~8% of the archive has no frag stream at all (unchanged, out of scope).

## Adversarial review outcomes

1. **Code/leakage audit:** no ground-truth leakage — recon reads only h/a/sp/intervals/
   pos/alive/frags/shots(time,player,weapon)/beams/projectiles, all verified populated
   on no_damage demos and computed without the damage stream in the Go source (the
   damage-derived `shots.hit/victims` fields are never read). Burst-walk parity exact
   (oracle 4398/4398). One honest caveat: the cadence test quantizes times but keeps
   modern-precision projectile end positions (up to ~50u stale in a real 51 ms
   recording), so 92.6% is mildly optimistic on that one axis — the old-corpus
   internal evidence (98% damage attribution, matching distributions) covers the real
   condition.
2. **Statistics:** held-out ≥ in-sample on every metric (no tuning-on-test);
   thresholds not cherry-picked; the arena-map tail and the pooled-vs-per-demo
   Spearman choice are the two disclosures worth carrying (both above).
3. **Domain transfer:** old-era physics identical (LG 30 + exact armor splits, splash
   law, spawn resets, mega-rot present 87/89); no svc_playerinfo dropouts (worst
   alive-gap p99 255 ms); per-weapon burst distributions match modern GT; the LG p90
   difference is corpus composition (fewer DMM4 games in the old half), not
   attribution loss. Sound-orphaned axe kills exist in 3 demos (negligible, never
   juicy-tier).

## Recommendations for the index build

1. **Reconstruct the ~45%.** Same `kills` schema, `dmgSource: "reconstructed"` per row
   (handoff §9), values not thresholds (project principle).
2. **Trust tiers:** duel/team non-arena = high (the headline numbers); arena-family
   maps (povdmm4/dmm4*/anarena/midair-style) = flag lower (~83% within-5%); CTF =
   lowest or excluded (n=1 validated, hook/rail unmodeled); `boundedMode:skipped`
   modes (midair, instagib) — GT itself refuses these, skip reconstruction too.
3. **Before production:** fix the analyzer NaN bug (~6% of old demos); add name#slot
   collision handling to the life-start clip (rejoin demos; the Go side resolves
   collisions, the prototype falls back to no-clip); optionally port recon.py to Go
   for throughput (Python ≈ 1–2 s/demo including JSON I/O → ~10 h for 23k demos on
   8 cores; fine either way).
4. **Extraction command** (all inputs in one pass):
   `MVDA_BSP_DIR=<dist>/bsps qw-analyze-viewall -view full -include positions,view,velocity,projectiles,beams <demo>`

## The enemy-attribution prior (added 2026-08-11, user-proposed)

The dominant duel error was the ambiguous close-range mutual-rocket frame, where a
real enemy hit gets credited to the victim's own splash and silently drops a juicy
burst under the selection threshold. Fix: an additive score penalty on SELF
candidates (`SELF_PEN`) — i.e. prefer the enemy explanation whenever both exist.
Swept on the pooled validation corpus, then **confirmed on a third, never-touched
sample** (seed 31337, 49 demos, 8,373 rows):

| fresh sample | juicy-RL precision | juicy-RL recall | within-5% | Spearman |
|---|---|---|---|---|
| duel, baseline | 98.6% | 93.5% | 94.6% | 0.980 |
| **duel, SELF_PEN=0.6** | 96.2% | **98.7%** | 94.9% | 0.989 |
| team, baseline / 0.1 | 96.8% | 92.4% | 95.3% | 0.978 |

The effect saturates at 0.6 (beyond it, self only wins when it is the sole
explanation) and converts the error *type*: instead of silently missing real juicy
fights, the reconstruction occasionally inflates an enemy burst by a self-splash
hit (fp 1→3 of ~79 selected). For a highlights index, missing-forever is the worse
failure, so this is now the default in `recon.py` — mode-aware: 0.6 for duels
(2 players), 0.1 for team games, where self-damage is rarer and the same bias costs
precision (measured −3pt at 0.6). See `bias_sweep.py` / `fresh_confirm.py`.

Error-anatomy scripts (`miss_anatomy.py`, `situation_profile.py`, `selfshare.py`)
document the situations behind the residuals: 68% of attribution flips occur at
<300u player separation, 56% while both players fired rockets within 1.2s; duels
carry 2.4× the self-damage share of team games (14.5% vs 6.0% of events), which is
why 1on1 was not trivially easier despite unambiguous attacker identity.

## Next-level accuracy leads (2026-08-11, discussed with the user)

Ranked; each attacks a measured residual class. Scripts: `rl110.py`, `old110.py`.

1. **BSP tier** (reuse mvd-analytics' BSP loading/LOS machinery): trace the
   shooter's aim ray to predict trackless-rocket explosion points; apply the rigid
   QW splash rule the user supplied — **splash covers only the exposed 180°; the
   impact face blocks it** (`CanDamage`) — as a hard gate on splash candidates; and
   classify fall/lava from floor/liquid data. Directly targets the close-range
   flip classes and env joins.
2. **Per-demo rocket-damage-constant detection** (new, near-free). Measured: on
   modern servers **RL direct damage** is fixed at 110 (88.8% of GT directs
   exactly 110; the 101–104 tail is point-blank splash at 110−0.5·d — splash
   still falls off with distance from the same base). The old archive is MIXED:
   a 110 spike (~6× baseline) over a genuine 95–120 spread including >110
   values — vanilla `100+random·20` direct-damage servers. One pass per demo identifies the regime; fixed-110
   demos get an exact-magnitude model (sharper discrimination + identifiable
   subset-sum decomposition of merged frames).
3. **Joint same-frame assignment**: solve each frame's deltas together under
   one-explosion-one-cause constraints; observed delta = subset-sum of candidate
   expected damages (integer-structured, esp. on fixed-110 demos).
4. **Cadence/ammo bookkeeping**: fire cycles (RL 0.8s, LG 0.1s/cell) and `rk`/`cl`
   ammo decrements confirm/veto inferred fire events (catches sound-orphaned axe
   demos and similar).
5. **Puppet re-simulation (the "proper reconstruction" endgame)**: a modded
   headless mvdsv/KTX state-forces fake clients along the recorded
   position/angle tracks, re-fires recorded shots, lets the engine do exact
   traces/physics/T_Damage, and re-emits `mvdhidden_dmgdone` — upgrading old
   demos into modern-format MVDs the existing pipeline consumes natively.
   The handoff's "replay does not work" (§3) stands for input-replay but is
   routed around by state-forcing: inputs are synthesized from outputs.
   RNG surface is small (sg/ssg spread, GL toss, rocket damage only on old
   random-damage servers) and cannot compound — per-frame re-anchoring means a
   diverged outcome is confined to its hit, and the recorded h/a delta arbitrates
   it (sim attributes, streams provide magnitudes). Weeks of engine work; runs
   far faster than realtime headless. Keep as capstone if browsing shows the
   missing tail matters.

## Known limits (all measured, none blocking)

- Same-frame multi-hit merges cannot be split (one delta covers both); ~1–2% of rows
  off-by-a-hit, damage unaffected.
- Close-quarters rocket self/enemy ambiguity is the dominant residual error class
  (worst on arena maps); knockback + aim + consumed-shot logic already reclaim most.
- RAW-family (overkill) damage is only recoverable to the −99 corpse clamp; the
  pipeline uses bounded, so this does not matter here.
- returnDamage within-5%: 92.8% (it inherits attribution symmetrically).

## Artifacts (this directory)

- `recon.py` — the reconstruction (deltas → attribution → burst assembly)
- `compare.py`, `runval.py`, `diagnose.py`, `archetypes.py`, `degrade.py`,
  `coverage.py`, `distshift.py`, `classify.py`, `holdout_eval.py` — validation harness
- `demo_out/*.topkills.json` — end-to-end reconstructions of the 5 handoff-named
  no_damage demos. E.g. `000232d9` (peppe vs wilLGURHT LG duel): top burst 454 dmg /
  16 hits / 338 returnDamage — a real glued fight, previously invisible to the index.

Scratch data (extracted stream JSONs, ~750 MB) lives in the session scratchpad and is
reproducible from the commands above; only code + results are committed here.
