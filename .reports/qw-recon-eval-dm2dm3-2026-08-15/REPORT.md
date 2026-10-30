# Damage reconstruction — accuracy on 60 fresh dm2/dm3 demos (2026-08-15)

Blind validation of `mvd-analytics/damagerecon` (branch `reconstruct-damage`,
@ 31d9565) against KTX `mvdhidden_dmgdone` ground truth on a corpus the
implementation had never seen: the **30 latest dm2 and 30 latest dm3 games**
on hub.quakeworld.nu at fetch time (recorded 2026-08-09 … 2026-08-15).
Reproduce: fetch via `hubfetch` (map filter, limit 30), then
`qw-recon-eval -dir <corpus> -diag`.

Corpus composition: dm2 = 14×1on1, 5×2on2, 1×3on3, 10×4on4; dm3 = 23×4on4,
7×wipeout. The 7 wipeout demos are **excluded by the reconstruction itself**
(see finding 1) — **53 demos scored**, 317 player rows with ≥200 GT damage,
80,373 ground-truth damage instants.

## Headline numbers (per-player match totals, relative error vs KTX)

| metric | median | mean | p90 | ≤1% | ≤2% |
|---|---|---|---|---|---|
| bounded given | **0.87%** | 1.24% | 2.74% | 54% | 80% |
| bounded taken | **0.05%** | 0.15% | 0.33% | 98.4% | 99.4% |
| bounded ewep | 1.46% | 2.18% | 5.11% | 38% | 62% |
| raw given | 1.35% | 2.17% | 5.28% | 42% | 63% |
| raw taken | 1.71% | 2.25% | 4.88% | 35% | 55% |
| bounded givenTeam | 5.1% | 7.9% | 20% | (denominators 200–700) |
| bounded givenSelf | ~5–10% | — | — | (small denominators) |

Event level: **99.6%** of GT damage instants have a same-instant
reconstructed delta, **98.8%** of those value-exact, **97.7%** attacker
attribution on unambiguous enemy instants. The fresh corpus comes out
slightly BETTER than the 13-demo development corpus (given median
0.87% vs 1.01%) — no overfitting signal.

Per map (identical bounded behaviour; raw slightly worse on dm3):

| | bounded given | bounded taken | raw given | raw taken |
|---|---|---|---|---|
| dm2 (30 demos) | 0.88% / 1.36% | 0.04% / 0.15% | 1.40% / 2.42% | 1.45% / 2.01% |
| dm3 (23 demos) | 0.87% / 1.16% | 0.05% / 0.15% | 1.33% / 1.99% | 1.93% / 2.42% |

(median / mean; dm3's raw-taken gap is quad/pent overkill — see finding 5.)

## Per-attacker-weapon event accuracy (GT single-source enemy instants)

| weapon | instants | GT dmg | covered | value-exact | attacker-ok |
|---|---|---|---|---|---|
| rl | 16,749 | 1,172,135 | 100.0% | 98.8% | 99.2% |
| sg | 40,557 | 700,404 | 99.5% | 98.6% | 97.0% |
| lg | 6,086 | 203,532 | 99.8% | 99.1% | 99.6% |
| ssg | 3,038 | 86,494 | 99.5% | 98.0% | 97.3% |
| gl | 655 | 50,982 | 99.8% | 99.7% | 98.2% |
| sng | 1,655 | 30,201 | 99.3% | 99.1% | 96.7% |
| ng | 721 | 6,443 | 100.0% | 99.3% | 95.3% |
| axe | 113 | 2,245 | 100.0% | 96.5% | **21.2%** |
| squish | 37 | 1,662 | 100.0% | 100.0% | 35.1% |

RL/LG (the damage that decides games) are essentially solved. The two
red cells are structural, not noise — findings 3 and 4.

## Findings — the biggest divergences and their causes

**1. Wipeout/Clan-Arena demos were unreconstructable AND undetected
(fixed during this evaluation, commit 31d9565).** 7 of the 30 dm3 demos
were wipeout; on them per-player taken erred 30–66% — catastrophic, and
*taken needs no attribution*, so this was never an inference problem:
KTX's clan-arena code suppresses drowning/fall damage entirely and
ignores almost all damage between rounds while STILL multicasting the
raw values (`ktx/src/combat.c:475-491`), so the wire damage log and the
health/armor streams genuinely disagree. Root cause of the detection
gap: newer KTX doesn't publish `k_*` mode cvars in serverinfo at all —
the submodes live only in the composite `mode` string
(`wipeout-wo-df`). Both the reconstruction and the wire-side bounded
pass now parse that string via one shared helper and skip the whole
clan-arena family (`skipped:ca|wipeout|ra|lgc|race`). Note this also
fixed a pre-existing wire-path bug: those demos were being served a
confidently wrong bounded family before this corpus exposed it.

**2. Duel close-quarters trackless rockets remain the worst honest row
(dm2_232186, given −11.9%).** Anatomy: 365 dmg of real enemy rockets
(including two clean 110 directs) credited to the victim's own splash.
No quad involved — these are frames where BOTH duelers fired within the
window, the enemy rocket was point-blank (no entity ever broadcast), and
the enemy-side evidence gates (flight-time + aim) failed, leaving only
the penalized self explanation. The self-splash value ceiling (55)
correctly rejects them on *survived* hits but a killing hit relaxes the
bound. This is the study's known residual class; the fix is data we
are not yet reading — see "missing wire evidence" below (TE_EXPLOSION).

**3. Axe attribution is 21% — because axe never enters the shots
stream.** `analyzer/shots.go fireSoundWeapon` has no entry for
`weapons/ax1.wav` (the swing, CHAN_WEAPON), so the reconstruction's axe
candidates can never fire and axe hits fall to env:unknown (george,
dm2_232179: 320 dmg lost this way). Better still, KTX plays
`player/axhit2.wav` on an axe HIT with the attacker as the entity — a
per-hit confirmation. Cheap fix; touches the shots artifact vocabulary
(goldens + docs), and axe is 0.09% of corpus damage, so it shipped as a
finding rather than a hotfix.

**4. Simultaneous-shotgunner confusion is the largest remaining enemy
flow** (4,779 dmg wrong-attacker + 4,424 sg→lg + 4,112 sg→self +
3,521 sg→env across 53 demos — together ~0.6% of total corpus damage).
Aim cones + LOS + value bounds cannot fully separate two shooters
firing at the same victim in the same 60 ms. The wire can: see below.

**5. Raw-family tails (±9–16% rows both directions) are kill-overkill
modeling on quad-heavy 4on4s.** The −99 corpse clamp hides deep
overkill; the model top-up recovers it from `D·q − 0.5·dist`, which
over- or under-shoots when the explosion distance is interpolated
(over: fudencio +16%; under: Anza −14.5%). Bounded is unaffected.
TE_EXPLOSION (below) gives exact distances.

**6. givenTeam/givenSelf percentages look bad but are small-denominator
artifacts** — absolute errors are on the order of one rocket (totals
200–700). The `PHANTOM → team` diag flow (9,700 dmg) is a scoring
artifact: GT logs teamkill telefrags outside its events list; recon
counts them as team damage events. Values land in the same per-player
buckets either way.

Environmental categories (added this branch): lava 97%, fall 95%,
drown 83% category accuracy vs GT; mover-crush stays `unknown`; GT's
world-attributed ng/sng rows (shooter disconnected mid-flight, wire
slot −1) stay attributed to the actual shooter — deliberate.

## Are we missing data? Yes — three unused wire signals (measured)

The parser currently length-skips these temp entities; all three were
probed with a temporary hook and correlated against ground truth:

**TE_BLOOD (type 12) — the "blood pattern", and it is better than a
pattern: the count byte is the pellet hit count.** KTX's `TraceAttack`
increments `blood_count` once per pellet that strikes a player and
`Multi_Finish` multicasts ONE TE_BLOOD per volley with that count and
the impact origin (`ktx/src/weapons.c:226,313-327`). Measured: **100%**
of GT sg/ssg hit instants have a same-instant TE_BLOOD, and
**4×count == raw volley damage in 90% (sg) / 72% (ssg)** of them (the
remainder are multi-source merges — exactly the frames where the count
lets us SPLIT). Axe and nails write blood too (count = damage there).

**Vanilla-4on4 verification at scale (16 earliest-hub dm2/dm3 4on4s,
2020, MVDSV 0.32 / KTX 1.38 — instrumented, so ground-truth-checked).**
Normal 4on4 is drenched in shotgun evidence: 4,000-8,100 TE_BLOOD and
9,000-14,000 TE_GUNSHOT messages per demo against ~1,000-1,500 GT sg
instants. Coverage: **99.9%** of GT survived sg/ssg instants have
same-instant blood; **4×(pellet evidence) == GT raw damage on 88-90%**
of them (the remainder are multi-source merged instants — the case the
evidence exists to split). One packaging caveat that confirms the
per-demo-calibration requirement: KTX 1.38 emits ONE TE_BLOOD PER
PELLET with count=1 (damage = 4 × message count), while modern KTX
1.48 emits one message per volley with count = pellet hits (damage =
4 × count byte). Axe hits write count=20 (the damage) in both
generations. Same information, different packaging — a per-demo
calibration pass (like the rocket-110 regime detection) picks the
convention automatically by checking which identity matches the
observed deltas.

Why the first old-demo probe found so few sg samples: the 10-demo
handoff set contains NO vanilla 4on4 at all — six LG/RL duels (3-48 sg
shots per game; duelists rush RL/LG off spawn), one LG-heavy team game,
one RL-only mod game, and one railgun-mod 4on4. Presence tracked usage,
not emission.

**Pre-2019 verification (10 handoff demos, 1999-2002 era, qw 2.40 /
MVDSV 0.25-0.34).** No KTX ground truth exists there, but
survived-hit h/a deltas ARE the raw damage — an internal oracle. Result:
every old-demo instant with a reconstructed sg/ssg hit that had blood
present matched **4×count == delta exactly (15/15**, across the two
demos that contain real shotgun hits — including plain qw 2.40, i.e.
original-era progs, consistent with the blood aggregation living in
id's own qwprogs). The demos with zero blood turned out to have zero
hitscan HITS, not missing emission: old duels are LG/RL-dominated
(one duel fired 0 sg / 11 ssg shots all game; one RL-only mod game
fired nothing but rockets — its 188 bloods are rocket directs with a
nonstandard constant count of 5). So: presence is driven by usage, the
count semantics held wherever testable, and ONE nonstandard mod proves
counts must be per-demo calibrated (validate 4×count against observed
deltas within the demo, exactly like the rocket-110 regime detection;
degrade to presence+position evidence when the calibration fails).
TE_EXPLOSION is abundant in EVERY old demo (186-1,146 per demo) and
TE_LIGHTNINGBLOOD wherever LG was used (114-461).
This directly attacks finding 4: per-volley hit confirmation + exact
magnitude + position kills the sg→env/self/team flips, and the count
decomposes merged instants per shooter.

**TE_LIGHTNINGBLOOD (type 13) — per-cell LG hit confirmation.**
The beam fires on every attack; blood only on a hit. Measured: 98–100%
of GT lg hit instants have one. Old demos carry them too (132 in one
1999-era demo). Turns LG reconstruction from delta-inference into
direct measurement.

**TE_EXPLOSION (type 3) — the exact detonation point of every rocket
and grenade, including point-blank trackless ones.** Measured: **97%**
of GT rl/gl damage instants have a same-instant TE_EXPLOSION. This is
the missing geometry for finding 2 (trackless close rockets currently
have NO explosion point — the "rl-sound" fallback guesses from flight
time) and finding 5 (exact splash distances make both the magnitude
model and the raw top-up sharp). Also upgrades the CanDamage gate from
the approximate projectile-track endpoint to the true detonation point.

(TE_GUNSHOT, type 2, carries the wall-puff miss pattern with a pellet
count — ~10k per 4on4, useful as negative evidence for the shotgun
solver, lower priority.)

Catalog note: hub.quakeworld.nu's index starts in 2019 and even its
earliest 4on4s carry the damage stream — the true pre-instrumentation
archive (the study's 97 no_damage demos, ~45% of the full 23k-demo
archive) lives outside the hub catalog and must be sourced from the
archive files directly when building the old-demo index.

## Recommended next steps, in order of value/effort

1. **Parse TE_EXPLOSION + TE_BLOOD + TE_LIGHTNINGBLOOD in mvd-reader**
   (new point-effect events + spatial streams alongside beams), then
   consume them in damagerecon: explosion-anchored rocket candidates
   (replaces rl-sound guessing), blood-anchored hitscan hits with
   4×count magnitudes, LG hit confirmation. Expected effect: findings
   2, 4, 5 largely close; sg attacker-ok 97→~99%, duel tail shrinks,
   raw tails collapse. This is parser + events + MVD_FORMAT.md work —
   the natural next phase.
2. **Add axe to the shots stream** (`weapons/ax1.wav` swing +
   `player/axhit2.wav` hit sound) — one switch entry + goldens; fixes
   finding 3 outright.
3. **Ammo bookkeeping** (rk/cells decrements) as a veto on self-rocket
   candidates when the victim demonstrably did not fire — cheap partial
   mitigation of finding 2 until (1) lands.
4. Optional: `sv_antilag` awareness (hit registration uses rewound
   positions; adds ~ping of position error to aim gates — visible only
   in the hitscan tails).
