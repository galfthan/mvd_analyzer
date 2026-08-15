#!/usr/bin/env python3
"""Compare reconstruction vs ground truth on a validation demo.

Event level: match GT damage.events (enemy, bounded family) to recon events
on (victim, time); score bounded value, attacker, weapon agreement.
Burst level: match GT top-kills rows to recon rows on (killer, victim, time);
score damage/hits/span/maxGap/victimWep/returnDamage.
"""
import json, sys
from collections import defaultdict
import recon

def gt_bounded(e):
    b = e.get("bounded")
    return e["damage"] if b is None else b

def event_level(d, events):
    gt = [e for e in d["damage"]["events"]
          if not e.get("isSelf") and not e.get("isTeam") and not e.get("isEnv")]
    gt_by_vt = defaultdict(list)
    for e in gt:
        gt_by_vt[(e["victim"], e["time"])].append(e)
    rec_by_vt = defaultdict(list)
    for e in events:
        if e["isSelf"] or e["isTeam"] or e["isEnv"]:
            continue
        rec_by_vt[(e["victim"], e["time"])].append(e)

    stats = defaultdict(int)
    val_err = []
    for key, ges in gt_by_vt.items():
        res = rec_by_vt.get(key)
        gtv = sum(gt_bounded(e) for e in ges)
        stats["gt_groups"] += 1
        stats["gt_sum"] += gtv
        if not res:
            stats["missed"] += 1
            stats["missed_dmg"] += gtv
            continue
        stats["matched"] += 1
        rv = sum(e["bounded"] for e in res)
        val_err.append(rv - gtv)
        if rv == gtv:
            stats["value_exact"] += 1
        # attribution: compare dominant attacker/weapon
        ga = max(ges, key=gt_bounded)
        ra = max(res, key=lambda e: e["bounded"])
        if ga["attacker"] == ra["attacker"]:
            stats["attacker_ok"] += 1
        if ga["weapon"].lower() == ra["weapon"].lower():
            stats["weapon_ok"] += 1
        if ga["attacker"] == ra["attacker"] and ga["weapon"].lower() == ra["weapon"].lower():
            stats["attr_both_ok"] += 1
    # false positives: recon enemy events with no GT counterpart
    for key, res in rec_by_vt.items():
        if key not in gt_by_vt:
            stats["spurious"] += 1
            stats["spurious_dmg"] += sum(e["bounded"] for e in res)
    return stats, val_err

def burst_level(dgt_tk, rec_rows):
    gt_rows = dgt_tk["kills"]
    rec_by = {(r["killer"], r["victim"], r["time"]): r for r in rec_rows}
    out = []
    missed = 0
    for g in gt_rows:
        r = rec_by.get((g["killer"], g["victim"], g["time"]))
        if r is None:
            missed += 1
            out.append({"gt": g, "rec": None})
            continue
        out.append({"gt": g, "rec": r})
    extra = len(rec_rows) - sum(1 for o in out if o["rec"] is not None)
    return out, missed, extra

def spearman(xs, ys):
    def rank(v):
        order = sorted(range(len(v)), key=lambda i: v[i])
        r = [0.0]*len(v)
        i = 0
        while i < len(order):
            j = i
            while j+1 < len(order) and v[order[j+1]] == v[order[i]]:
                j += 1
            avg = (i + j) / 2.0
            for k in range(i, j+1):
                r[order[k]] = avg
            i = j+1
        return r
    n = len(xs)
    if n < 3:
        return float("nan")
    rx, ry = rank(xs), rank(ys)
    mx = sum(rx)/n; my = sum(ry)/n
    num = sum((rx[i]-mx)*(ry[i]-my) for i in range(n))
    den = (sum((x-mx)**2 for x in rx) * sum((y-my)**2 for y in ry)) ** 0.5
    return num/den if den else float("nan")

def run(full_path, tk_path):
    d = recon.load(full_path)
    tk = json.load(open(tk_path))
    events = recon.build_recon_events(d)
    rows = recon.assemble_topkills(d, events)
    estats, val_err = event_level(d, events)
    pairs, missed, extra = burst_level(tk, rows)

    m = [p for p in pairs if p["rec"] is not None]
    dmg_err = [p["rec"]["damage"] - p["gt"]["damage"] for p in m]
    hits_ok = sum(1 for p in m if p["rec"]["hits"] == p["gt"]["hits"])
    dmg_ok = sum(1 for p in m if p["rec"]["damage"] == p["gt"]["damage"])
    dmg_close = sum(1 for p in m if abs(p["rec"]["damage"] - p["gt"]["damage"]) <= max(5, 0.05*p["gt"]["damage"]))
    vw_ok = sum(1 for p in m if p["rec"]["victimWep"] == p["gt"].get("victimWep", ""))
    rd_close = sum(1 for p in m if abs(p["rec"]["returnDamage"] - p["gt"]["returnDamage"]) <= max(5, 0.05*max(1,p["gt"]["returnDamage"])))
    span_ok = sum(1 for p in m if p["rec"]["spanMs"] == p["gt"]["spanMs"])
    rho = spearman([p["gt"]["damage"] for p in m], [p["rec"]["damage"] for p in m])

    return {
        "mode": (d.get("demoInfo") or {}).get("mode"),
        "map": (d.get("demoInfo") or {}).get("map"),
        "event": dict(estats),
        "event_val_err_nonzero": sum(1 for e in val_err if e != 0),
        "burst_gt_rows": len(pairs),
        "burst_missed": missed,
        "burst_extra_recon_rows": extra,
        "burst_matched": len(m),
        "dmg_exact": dmg_ok, "dmg_within5pct": dmg_close,
        "hits_exact": hits_ok, "span_exact": span_ok,
        "victimWep_ok": vw_ok, "returnDmg_within5pct": rd_close,
        "spearman_damage": round(rho, 4),
        "dmg_err_mean": sum(dmg_err)/len(dmg_err) if dmg_err else 0,
        "dmg_err_max": max((abs(e) for e in dmg_err), default=0),
    }

if __name__ == "__main__":
    r = run(sys.argv[1], sys.argv[2])
    print(json.dumps(r, indent=1))
