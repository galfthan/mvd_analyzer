import glob, json, os, sys
from collections import defaultdict
from concurrent.futures import ProcessPoolExecutor
import recon, compare

SC = os.path.dirname(os.path.abspath(__file__))
DIR = sys.argv[1] if len(sys.argv) > 1 else "holdout"

def one(full_path):
    sha8 = os.path.basename(full_path)[:8]
    tkp = os.path.join(SC, DIR, "tk", sha8 + ".json")
    try:
        d = recon.load(full_path)
        tk = json.load(open(tkp))
        events = recon.build_recon_events(d)
        rows = recon.assemble_topkills(d, events)
        return sha8, (d.get("demoInfo") or {}).get("mode"), (d.get("demoInfo") or {}).get("map"), tk["kills"], rows
    except Exception as e:
        return sha8, "ERR", str(e)[:80], [], []

if __name__ == "__main__":
    files = sorted(glob.glob(os.path.join(SC, DIR, "full/*.json")))
    with ProcessPoolExecutor(max_workers=8) as ex:
        res = list(ex.map(one, files))
    errs = [(s, m) for s, mode, m, g, r in res if mode == "ERR"]
    if errs: print("errors:", errs)
    per_demo = []
    pooled_gt, pooled_rec = [], []
    m_all = []
    for sha8, mode, mp, gt, rec in res:
        if mode == "ERR": continue
        rec_by = {(r["killer"], r["victim"], r["time"]): r for r in rec}
        matched = [(g, rec_by[(g["killer"], g["victim"], g["time"])]) for g in gt
                   if (g["killer"], g["victim"], g["time"]) in rec_by]
        missed = len(gt) - len(matched)
        if not matched: continue
        w5 = sum(1 for g, r in matched if abs(r["damage"]-g["damage"]) <= max(5, 0.05*g["damage"]))
        rho = compare.spearman([g["damage"] for g, _ in matched], [r["damage"] for _, r in matched])
        per_demo.append({"sha8": sha8, "mode": mode, "map": mp, "n": len(matched),
                         "missed": missed, "w5": w5/len(matched), "rho": rho})
        m_all.extend(matched)
    n = len(m_all)
    w5 = sum(1 for g, r in m_all if abs(r["damage"]-g["damage"]) <= max(5, 0.05*g["damage"]))
    ex_ = sum(1 for g, r in m_all if r["damage"] == g["damage"])
    rho_pooled = compare.spearman([g["damage"] for g, _ in m_all], [r["damage"] for _, r in m_all])
    print(f"HELD-OUT: demos={len(per_demo)} rows={n} missed={sum(p['missed'] for p in per_demo)}")
    print(f"dmg exact {ex_/n:.1%}  within5pct {w5/n:.1%}  pooled spearman {rho_pooled:.4f}")
    for mode in ("duel", "team", "ffa", "ctf"):
        sel = [p for p in per_demo if p["mode"] == mode]
        if not sel: continue
        rows = sum(p["n"] for p in sel)
        w5m = sum(p["w5"]*p["n"] for p in sel)/rows
        rhos = sorted(p["rho"] for p in sel if p["rho"] == p["rho"])
        print(f"  {mode}: demos={len(sel)} rows={rows} within5={w5m:.1%} rho_perdemo p10/p50/p90: " +
              str([round(rhos[min(len(rhos)-1,int(len(rhos)*q))],3) for q in (.1,.5,.9)] if rhos else "-"))
    # archetypes with threshold sweep
    def sel_rows(rows_list, wep, mind, minrd):
        return {i for i, (g, r) in enumerate(rows_list) if (wep is None or (g if g else r)) }
    for wep, mind, minrd in [("rl",150,30),("rl",180,50),("rl",200,50),("rl",250,0),("rl",180,100),("lg",150,0),("lg",200,0)]:
        gsel = {i for i,(g,r) in enumerate(m_all) if g["weapon"]==wep and g["damage"]>=mind and g["returnDamage"]>=minrd}
        rsel = {i for i,(g,r) in enumerate(m_all) if r["weapon"]==wep and r["damage"]>=mind and r["returnDamage"]>=minrd}
        tp=len(gsel&rsel); fp=len(rsel-gsel); fn=len(gsel-rsel)
        prec = tp/(tp+fp) if tp+fp else float('nan'); rec_ = tp/(tp+fn) if tp+fn else float('nan')
        print(f"  {wep} dmg>={mind} rd>={minrd}: prec {prec:.1%} rec {rec_:.1%} (tp {tp} fp {fp} fn {fn})")
    # worst demos
    per_demo.sort(key=lambda p: p["w5"])
    print("\nworst 6 demos by within5:")
    for p in per_demo[:6]:
        print(f"  {p['sha8']} {p['mode']:>4} {str(p['map']):>10} n={p['n']:>3} w5={p['w5']:.1%} rho={p['rho']:.3f} missed={p['missed']}")
    json.dump(per_demo, open(os.path.join(SC, DIR, "per_demo.json"), "w"), indent=1)
