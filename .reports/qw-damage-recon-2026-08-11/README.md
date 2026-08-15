# QW damage reconstruction — evaluation package (2026-08-11, preliminary)

Reconstructs per-hit damage and `topKills`-style burst rows from **pre-instrumentation
QuakeWorld MVD demos** — the ~45% of the archive recorded before servers broadcast
`mvdhidden_dmgdone`. Damage magnitudes come from the health/armor change streams
(the observed decrement *is* the KTX bounded damage); attribution comes from
spectator-visible evidence (LG beams, projectile entity flights, fire sounds,
position/view/velocity tracks, the frag log).

**Start with `recon-guide.html`** — the workflow presentation and full use guide
(file map, parameters, input gotchas, output schema). `REPORT.md` is the evidence
document: validation method, all numbers, adversarial-review outcomes, known limits.

Status: **preliminary / gated.** Held-out validation on modern demos (which carry both
real damage and the streams): ~90% exact, ~95% within-5%, Spearman ~0.975–0.980;
archetype selection (e.g. contested RL ≥ 180 dmg) ~97–99% precision/recall. An
adversarial substance review is ongoing; treat reconstructed damage as an estimate and
keep it segregated (every output row is stamped `dmgSource: "reconstructed"`).

## Contents

```
recon-guide.html      the presentation + use guide (self-contained, open in a browser)
REPORT.md             validation evidence & known limits
demos/                10 example pre-instrumentation demos (filenames = content sha256)
                      6 duels + 4 team games: oldcrat, dm2, noentry, aerowalk, ztndm3,
                      dm4, e1m7, amphi, endif, monsoon
output/               those same 10 demos run through the tool (one .topkills.json each)
scripts/
  run_examples.py     THE SCRIPT THAT PRODUCED output/ — rerun it to reproduce
  recon.py            the reconstruction engine (self-contained, stdlib only)
  holdout_eval.py     score reconstructions against ground truth on modern demos
  compare.py          matching + Spearman helpers for holdout_eval
  freshval_prep.py    build a fresh validation sample (paths assume our corpus layout)
  corpus/             reference copies of our full-corpus pipeline:
                      recon_run.py (27k-demo driver), reduce.py (index reducer),
                      stamp_recon.py (dmgSource/tier stamps), merge.py (index merge —
                      the recon source is deliberately NOT in its SRCS yet: review gate)
bin/
  qw-analyze-viewall-nanfix    the mvd_analyzer CLI, linux-amd64, built WITH the
                               NaN ingestion guards (see nanfix.patch)
  nanfix.patch                 the 4-file guard diff vs the mvd_analyzer tree —
                               apply + `go build ./mvd-analytics/cmd/qw-analyze`
                               to build for other platforms (Go >= 1.25)
```

## Run it

```
python3 scripts/run_examples.py            # re-runs the 10 demos -> output/
python3 scripts/run_examples.py my.mvd     # or any demo of your own
```

Python ≥ 3.9, stdlib only. The analyzer CLI is taken from `$QW_ANALYZE` if set, else
`bin/qw-analyze-viewall-nanfix`. `MVDA_BSP_DIR` (the analyzer's map pack) is optional —
not needed for reconstruction, not included here.

Without the nanfix guards a stock `qw-analyze` build aborts on ~1% of old demos with
`json: unsupported value: NaN` (non-finite wire origins reaching the JSON encoder);
the patch drops those samples at ingestion and is byte-identical on clean demos.

## Reading the output

One JSON per demo: `players`, `reconMeta.nevents` (attributed per-hit damage events),
and `kills` — one row per enemy frag, `topKills`-compatible:
`killer, victim, time (match-relative ms), weapon, damage (bounded burst), hits,
spanMs, maxGapMs, victimWep, returnDamage (victim→killer within 4 s)`, plus
`rank, team, dmgSource`. Bounded means capped at the victim's remaining health +
armor absorbed — a rocket landing on a 10-health player counts ~10, not 110, matching
how the modern index scores real damage (`-dmg bounded`).

Known limits in brief (full list in REPORT.md §Known limits): same-frame multi-hit
merges can be off-by-a-hit (~1–2% of rows, damage unaffected); close-range mutual
rocket fights are the dominant residual ambiguity; arena-family maps and CTF are out
of scope; selection quality (filters/rankings) is more robust than per-burst damage
accuracy — trust the former first.
