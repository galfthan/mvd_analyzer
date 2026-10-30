#!/usr/bin/env python3
"""First processing run of the damage-reconstruction engine over the old
(`no_damage`) megademocache demos — docs/reconstruction-processing-handoff.md.

Per target demo (non-arena, nkills==0):
  1. qw-analyze-viewall -view full -include positions,view,velocity,projectiles,beams
  2. recon.build_recon_events + assemble_topkills  (research/fuzzyolddemoextract/recon.py)
  3. write recon-src/<sha>.json shaped like the staged -view all captures
     (meta/lives/powerupEvents/shots from out/all-mega, topKills = reconstructed rows)
     so the unchanged reduce.py consumes it.

Idempotent: skips shas whose recon-src output already exists; errors are logged
to recon-src-log.jsonl and re-attempted on rerun. Nothing here touches the
modern index (out/, out-mega/, out-combined/).
"""
import json, os, subprocess, sys, time
from concurrent.futures import ProcessPoolExecutor

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.join(HERE, "..", "research", "fuzzyolddemoextract"))
import recon

# -viewall-nanfix: local rebuild with the flightsToStream NaN guard
# (REPORT.md "fix the NaN bug before the index build"); the stock binary
# dies with `json: unsupported value: NaN` on ~3% of old demos.
CLI = os.path.expanduser("~/development/qw/mvdanalyzer/dist/qw-analyze-viewall-nanfix")
BSP = os.path.expanduser("~/development/qw/mvdanalyzer/dist/bsps")
CACHE = os.path.expanduser("~/development/qw/reference/megademocache")
ALLMEGA = os.path.expanduser("~/development/qw/mvdAlocaltest/out/all-mega")
GAMES = os.path.join(HERE, "out-mega", "games.jsonl")
OUTDIR = os.path.join(HERE, "recon-src")
LOG = os.path.join(HERE, "recon-src-log.jsonl")
LIMIT = int(sys.argv[sys.argv.index("--limit") + 1]) if "--limit" in sys.argv else None

# "end" is arena (user ruling 2026-08-11) — exact match ("endif" is a normal map).
ARENA = lambda mp, md: ((mp or '').lower().startswith(('povdmm4','dmm4','aztekdmm4','anarena','midair','arena'))
                        or (mp or '').lower() == 'end'
                        or (md or '').lower() in ('ca','ra','midair'))
ENV = dict(os.environ, MVDA_BSP_DIR=BSP)


def targets():
    out = []
    for l in open(GAMES):
        g = json.loads(l)
        if g.get('nkills', 0) == 0 and not ARENA(g.get('map'), g.get('demoMode')):
            out.append(g['sha'])
    return out


def one(sha):
    outp = os.path.join(OUTDIR, sha + ".json")
    mvd = os.path.join(CACHE, sha + ".mvd")
    if not os.path.exists(mvd):
        return sha, "no-mvd", None
    r = subprocess.run([CLI, "-view", "full", "-include",
                        "positions,view,velocity,projectiles,beams", mvd],
                       capture_output=True, env=ENV)
    if r.returncode != 0:
        err = r.stderr.decode(errors="replace")
        cls = "nan-bug" if "NaN" in err else "extract-err"
        return sha, cls, err[:160]
    try:
        d = json.loads(r.stdout)
        # A few demos carry a shots/frags block with a null inner list;
        # normalize so the engine sees "no evidence" instead of crashing.
        for blk, key in (("shots", "shots"), ("frags", "frags")):
            if isinstance(d.get(blk), dict) and d[blk].get(key) is None:
                d[blk][key] = []
        # Old duels often carry an empty wire team for both players, which
        # makes the analyzer flag every frag isTeamKill — recon then skips
        # them all (measured: ~4k duels reduced to 0 kills). With exactly
        # two players a killer != victim frag cannot be a teamkill.
        players = (d.get("streams") or {}).get("players") or []
        if len(players) == 2 and isinstance(d.get("frags"), dict):
            for f in d["frags"].get("frags") or []:
                if f.get("isTeamKill") and f.get("killer") != f.get("victim"):
                    f["isTeamKill"] = False
        events = recon.build_recon_events(d)
        rows = recon.assemble_topkills(d, events)
    except Exception as e:
        return sha, "recon-err", f"{type(e).__name__}: {e}"[:160]
    teams = {p["name"]: p.get("team") for p in d["streams"]["players"]}
    for i, row in enumerate(rows):
        row["rank"] = i + 1
        row["team"] = teams.get(row["killer"])
    try:
        am = json.load(open(os.path.join(ALLMEGA, sha + ".json")))
    except Exception as e:
        return sha, "allmega-err", f"{type(e).__name__}: {e}"[:160]
    merged = {k: am[k] for k in ("meta", "lives", "powerupEvents", "shots", "timeUnit") if k in am}
    merged["topKills"] = {"kills": rows}
    merged["reconMeta"] = {"engine": "recon.py", "nevents": len(events)}
    tmp = outp + ".tmp"
    with open(tmp, "w") as f:
        json.dump(merged, f, separators=(",", ":"))
    os.replace(tmp, outp)
    return sha, "ok", len(rows)


if __name__ == "__main__":
    os.makedirs(OUTDIR, exist_ok=True)
    todo = [s for s in targets() if not os.path.exists(os.path.join(OUTDIR, s + ".json"))]
    if LIMIT:
        todo = todo[:LIMIT]
    print(f"targets to process: {len(todo)}", flush=True)
    t0 = time.time()
    ok = err = 0
    with open(LOG, "a") as log, ProcessPoolExecutor(max_workers=7) as ex:
        for i, (sha, status, detail) in enumerate(ex.map(one, todo, chunksize=8)):
            if status == "ok":
                ok += 1
            else:
                err += 1
                log.write(json.dumps({"sha": sha, "status": status, "detail": detail}) + "\n")
                log.flush()
            if (i + 1) % 500 == 0:
                el = time.time() - t0
                print(f"{i+1}/{len(todo)}  ok={ok} err={err}  "
                      f"{el:.0f}s elapsed, ~{el/(i+1)*(len(todo)-i-1)/60:.0f} min left", flush=True)
    print(f"DONE: ok={ok} err={err} in {(time.time()-t0)/60:.1f} min", flush=True)
