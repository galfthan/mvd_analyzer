#!/usr/bin/env python3
"""Stamp the reduced reconstruction index (out-recon/) with trust markers —
docs/reconstruction-processing-handoff.md §5:
  kills.jsonl: dmgSource: "reconstructed" on every row (real damage is "broadcast")
  games.jsonl: reconTier per game —
    high         validated class: Duel/Team, non-arena
    arena-like   map "end" — defensive only: end is arena (user ruling 2026-08-11)
                 and is excluded upstream (recon_run.py ARENA); nothing should
                 reach this tier

    unknown-mode demoMode missing (mostly old duels, but unvalidated as a class)
    other-mode   FFA/Extinction/Wipeout/RACE/... (never validated)
Rewrites in place; idempotent."""
import json, os, sys

D = sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.path.dirname(os.path.abspath(__file__)), "out-recon")

def tier(g):
    if (g.get("map") or "").lower() == "end":
        return "arena-like"
    md = g.get("demoMode")
    if md in ("Duel", "Team"):
        return "high"
    if md is None:
        return "unknown-mode"
    return "other-mode"

games = [json.loads(l) for l in open(f"{D}/games.jsonl")]
for g in games:
    g["reconTier"] = tier(g)
with open(f"{D}/games.jsonl", "w") as f:
    for g in games:
        f.write(json.dumps(g) + "\n")

n = 0
with open(f"{D}/kills.jsonl") as fin, open(f"{D}/kills.jsonl.tmp", "w") as fout:
    for l in fin:
        k = json.loads(l)
        k["dmgSource"] = "reconstructed"
        fout.write(json.dumps(k) + "\n")
        n += 1
os.replace(f"{D}/kills.jsonl.tmp", f"{D}/kills.jsonl")

from collections import Counter
print(f"stamped {n} kills dmgSource=reconstructed; {len(games)} games")
print("reconTier:", dict(Counter(g["reconTier"] for g in games)))
