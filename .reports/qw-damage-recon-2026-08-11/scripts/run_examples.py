#!/usr/bin/env python3
"""Run the damage reconstruction over the demos in ../demos/ and write one
<sha>.topkills.json per demo into ../output/. This is the script that produced
the packaged outputs — re-running it should reproduce them byte-for-byte.

Per demo:
  1. qw-analyze -view full -include positions,view,velocity,projectiles,beams
     (the heavy parse: h/a change streams, position/view/velocity tracks,
      LG beams, projectile flights, fire sounds, frag log)
  2. input canonicalization (see recon-guide.html §13):
     - null shots/frags inner lists -> []
     - 2-player demos: a killer != victim frag cannot be a teamkill
       (old duels often record an empty wire team for both players, which
       makes the analyzer flag every frag isTeamKill)
  3. recon.build_recon_events + recon.assemble_topkills

Usage:
    python3 run_examples.py [demo.mvd ...]     # default: ../demos/*.mvd

The analyzer CLI is resolved from $QW_ANALYZE, else ../bin/qw-analyze-viewall-nanfix
(linux-amd64; build your own from the mvd_analyzer repo + ../bin/nanfix.patch
for other platforms). MVDA_BSP_DIR is optional — set it to the analyzer's bsps/
directory if you have it; reconstruction works without it.

Needs: Python >= 3.9, stdlib only. recon.py must sit next to this script.
"""
import glob, json, os, subprocess, sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import recon

CLI = os.environ.get("QW_ANALYZE") or os.path.join(HERE, "..", "bin", "qw-analyze-viewall-nanfix")
DEMOS = sys.argv[1:] or sorted(glob.glob(os.path.join(HERE, "..", "demos", "*.mvd")))
OUTDIR = os.path.join(HERE, "..", "output")
os.makedirs(OUTDIR, exist_ok=True)

INCLUDE = "positions,view,velocity,projectiles,beams"


def canonicalize(d):
    for blk, key in (("shots", "shots"), ("frags", "frags")):
        if isinstance(d.get(blk), dict) and d[blk].get(key) is None:
            d[blk][key] = []
    players = (d.get("streams") or {}).get("players") or []
    if len(players) == 2 and isinstance(d.get("frags"), dict):
        for f in d["frags"].get("frags") or []:
            if f.get("isTeamKill") and f.get("killer") != f.get("victim"):
                f["isTeamKill"] = False
    return d


for mvd in DEMOS:
    name = os.path.basename(mvd)
    sha = name[:-4] if name.endswith(".mvd") else name
    r = subprocess.run([CLI, "-view", "full", "-include", INCLUDE, mvd],
                       capture_output=True)
    if r.returncode != 0:
        print(f"{sha[:12]}  EXTRACT FAILED: {r.stderr.decode(errors='replace')[:120]}")
        continue
    d = canonicalize(json.loads(r.stdout))
    events = recon.build_recon_events(d)
    rows = recon.assemble_topkills(d, events)
    teams = {p["name"]: p.get("team") for p in d["streams"]["players"]}
    for i, row in enumerate(rows):
        row["rank"] = i + 1
        row["team"] = teams.get(row["killer"])
        row["dmgSource"] = "reconstructed"
    out = {
        "file": name,
        "players": [{"name": p["name"], "team": p.get("team")}
                    for p in d["streams"]["players"]],
        "reconMeta": {"engine": "recon.py", "nevents": len(events),
                      "selfPen": None, "note": "SELF_PEN auto: 0.6 duel / 0.1 team"},
        "kills": rows,
    }
    op = os.path.join(OUTDIR, sha + ".topkills.json")
    with open(op, "w") as f:
        json.dump(out, f, indent=1)
    top = rows[0] if rows else None
    tops = (f"top: {top['killer']} > {top['victim']} {top['weapon']} "
            f"{top['damage']}dmg/{top['hits']}hits rd={top['returnDamage']}") if top else "no kills"
    print(f"{sha[:12]}  {len(rows):>3} kills, {len(events):>4} events   {tops}")
