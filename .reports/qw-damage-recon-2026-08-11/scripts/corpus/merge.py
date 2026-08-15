#!/usr/bin/env python3
"""Merge per-source reduced indexes (hub `out/`, megademocache `out-mega/`) into a single
modern index `out-combined/`: dedup by sha, keep MODERN games (kills>0), remap g -> global
int FK, tag each game with its source. Reports tally, overlap, format/source mix, sizing."""
import json, os, sys
from collections import Counter

SRCS = [('hub', 'out'), ('mega', 'out-mega')]   # order = dedup priority (hub first)
DST = 'out-combined'
os.makedirs(DST, exist_ok=True)

def load(d):
    if not os.path.exists(f'{d}/games.jsonl'): return [], []
    g = [json.loads(l) for l in open(f'{d}/games.jsonl')]
    k = [json.loads(l) for l in open(f'{d}/kills.jsonl')]
    return g, k

# Arena (DMM4/arena-family maps + CA/RA modes) is excluded from all lists — not
# what we index, and it's the lower-confidence reconstruction subset. The demos
# themselves are moved to sibling dirs; this drops any that slip through by source.
def is_arena(row):
    mp = (row.get('map') or '').lower()
    md = (row.get('demoMode') or row.get('mode') or '').lower()
    # "end" is arena (user ruling 2026-08-11) — exact match, NOT a prefix
    # ("endif" is a normal duel map).
    return (mp.startswith(("povdmm4", "dmm4", "aztekdmm4", "anarena", "midair", "arena"))
            or mp == "end"
            or md in ("ca", "ra", "midair") or "arena" in md)

seen = {}            # sha -> new global g
oldg = {}            # (src, old g) -> new g
games = []
src_games = {}
for src, d in SRCS:
    gl, _ = load(d)
    src_games[src] = gl
    for row in gl:
        if is_arena(row):                     # arena excluded from all
            continue
        if not row.get('nkills', 0) > 0:      # modern only
            continue
        sha = row.get('sha')
        if sha in seen:
            oldg[(src, row['g'])] = seen[sha]
            continue
        ng = len(games); seen[sha] = ng; oldg[(src, row['g'])] = ng
        r = dict(row); r['g'] = ng; r['src'] = src
        games.append(r)

kills = []
for src, d in SRCS:
    _, kl = load(d)
    for k in kl:
        ng = oldg.get((src, k['g']))
        if ng is None: continue
        kk = dict(k); kk['g'] = ng; kills.append(kk)

with open(f'{DST}/games.jsonl', 'w') as f:
    for r in games: f.write(json.dumps(r) + '\n')
with open(f'{DST}/kills.jsonl', 'w') as f:
    for k in kills: f.write(json.dumps(k) + '\n')

gs = os.path.getsize(f'{DST}/games.jsonl'); ks = os.path.getsize(f'{DST}/kills.jsonl')
modern_per_src = {s: sum(1 for r in gl if r.get('nkills', 0) > 0) for s, gl in src_games.items()}
overlap = sum(modern_per_src.values()) - len(games)
print("=== COMBINED MODERN INDEX (dedup by sha, kills>0) ===")
print(f"  modern games/source: {modern_per_src}  ->  merged {len(games)} unique  ({overlap} sha-dupes removed)")
print(f"  kills: {len(kills):,}  ({ks/1e6:.0f} MB, {ks/max(1,len(kills)):.0f} B/row)   games: {gs/1e6:.1f} MB")
print(f"  by source: {dict(Counter(r['src'] for r in games))}")
print(f"  format mix: {dict(Counter(r.get('mode') for r in games).most_common(6))}")
print(f"  map mix (top6): {dict(Counter(r.get('map') for r in games).most_common(6))}")
print(f"  est on-disk with sha interned to int FK: ~{(ks*0.8+gs)/1e9:.2f} GB")
