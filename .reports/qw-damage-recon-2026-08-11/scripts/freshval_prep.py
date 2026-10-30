#!/usr/bin/env python3
"""Prepare a fresh modern-demo validation sample for holdout_eval.py.

Picks N modern demos (nkills>0, non-arena, Duel/Team) from the reduced index,
extracts full streams + ground-truth top-kills for each:

    <outdir>/full/<sha>.json   -view full -include positions,view,velocity,projectiles,beams
    <outdir>/tk/<sha8>.json    -view top-kills -dmg bounded -limit -1

Then score with:  python3 holdout_eval.py <outdir>

Usage:  python3 freshval_prep.py <outdir> [seed] [N]
Pick a seed nobody has used before — the whole point is a never-touched sample.
Used seeds so far: 31337 (fresh_confirm), 20260811 (first-run gate check).
"""
import json, os, random, subprocess, sys
from concurrent.futures import ThreadPoolExecutor

OUT = sys.argv[1]
SEED = int(sys.argv[2]) if len(sys.argv) > 2 else 20260811
N = int(sys.argv[3]) if len(sys.argv) > 3 else 60

CLI = os.path.expanduser("~/development/qw/mvdanalyzer/dist/qw-analyze-viewall-nanfix")
BSP = os.path.expanduser("~/development/qw/mvdanalyzer/dist/bsps")
CACHE = os.path.expanduser("~/development/qw/reference/megademocache")
GAMES = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                     "..", "..", "index-build", "out-mega", "games.jsonl")

ARENA = lambda mp, md: ((mp or '').lower().startswith(('povdmm4','dmm4','aztekdmm4','anarena','midair','arena'))
                        or (mp or '').lower() == 'end'
                        or (md or '').lower() in ('ca','ra','midair'))

cands = []
for l in open(GAMES):
    g = json.loads(l)
    if (g.get('nkills',0) > 0 and not ARENA(g.get('map'), g.get('demoMode'))
            and g.get('demoMode') in ('Duel','Team')):
        cands.append(g['sha'])
random.Random(SEED).shuffle(cands)
picks = cands[:N]
os.makedirs(f"{OUT}/full", exist_ok=True)
os.makedirs(f"{OUT}/tk", exist_ok=True)
env = dict(os.environ, MVDA_BSP_DIR=BSP)

def one(sha):
    mvd = f"{CACHE}/{sha}.mvd"
    if not os.path.exists(mvd):
        return sha, "no-mvd"
    full = subprocess.run([CLI, "-view", "full", "-include",
                           "positions,view,velocity,projectiles,beams", mvd],
                          capture_output=True, env=env)
    if full.returncode != 0:
        return sha, "full-err:" + full.stderr.decode()[:80]
    tk = subprocess.run([CLI, "-view", "top-kills", "-dmg", "bounded", "-limit", "-1", mvd],
                        capture_output=True, env=env)
    if tk.returncode != 0:
        return sha, "tk-err:" + tk.stderr.decode()[:80]
    open(f"{OUT}/full/{sha}.json", "wb").write(full.stdout)
    open(f"{OUT}/tk/{sha[:8]}.json", "wb").write(tk.stdout)
    return sha, "ok"

with ThreadPoolExecutor(max_workers=7) as ex:
    res = list(ex.map(one, picks))
bad = [(s[:8], st) for s, st in res if st != "ok"]
print(f"extracted {sum(1 for _,st in res if st=='ok')}/{len(picks)}  (seed {SEED})")
if bad:
    print("failures:", bad)
