#!/usr/bin/env python3
"""Reduce `qw-analyze -view all` JSON (one file per demo) to the clip index:
  games.jsonl  — one row per demo (sha-keyed, interned int id `g`)
  kills.jsonl  — one row per enemy kill, with derived fields (values, not booleans)

Derived per kill:
  killerDeathDeltaMs : lives, matched on attrStart/attrEnd; signed; null if killer's life
                       ended != death (survived) or no life found.
  killerQuad/victimQuad : powerupEvents — quad active (time <= kill <= detail.endTime).
  vShotsRL/vShotsLG  : shots — victim's rl/lg fires in [kill-span-buffer, kill].

Sources game metadata from -view all's `meta` block; for this dry run, sha + the specific
format (4on4/2on2/1on1) come from the cached .demo-cache-sets (hub catalog), since the demo's
own mode is only "Team"/"Duel". Prints parity (vs cached overview.topKills), quad rates, sizing.
"""
import json, os, sys, glob, bisect
from collections import defaultdict, Counter

IN   = sys.argv[sys.argv.index('--in')+1]   if '--in'   in sys.argv else '/home/narayana/development/qw/mvdAlocaltest/out/all'
SETS = sys.argv[sys.argv.index('--sets')+1] if '--sets' in sys.argv else '/home/narayana/development/qw/mvddatabrowser/.demo-cache-sets'
OUT  = sys.argv[sys.argv.index('--out')+1]  if '--out'  in sys.argv else '/home/narayana/development/qw/mvddatabrowser/index-build/out'
os.makedirs(OUT, exist_ok=True)

CATALOG = {}
try:
    idx = json.load(open(f'{SETS}/index.json'))
    for gid, v in idx.items(): CATALOG[str(gid)] = v      # {mode, bucket, map, demo}
except Exception as e:
    print("warn: no catalog index.json:", e)

def rows_of(obj, *keys):
    if isinstance(obj, list): return obj
    if isinstance(obj, dict):
        for k in keys:
            if isinstance(obj.get(k), list): return obj[k]
    return []

def killer_death_delta(lives_by_player, killer, ktime):
    for L in lives_by_player.get(killer, ()):
        if L.get('attrStart', L['start']) <= ktime <= L.get('attrEnd', L['end']):
            return (L['end'] - ktime) if L.get('endReason') == 'death' else None
    return None

def had_quad(pu_by_player, player, ktime):
    for e in pu_by_player.get(player, ()):
        d = e.get('detail', {})
        if d.get('powerup') == 'quad' and e['time'] <= ktime <= d.get('endTime', e['time']):
            return True
    return False

games, kills = [], []
sha_by_g = {}
gcount = 0
parity_ok = parity_bad = parity_demos = 0
quad_k = quad_v = 0
files = sorted(glob.glob(f'{IN}/*.json'))
print(f"reducing {len(files)} demos from {IN}")

for fp in files:
    gid = os.path.basename(fp)[:-5]
    try:
        d = json.load(open(fp))
    except Exception:
        continue
    meta = d.get('meta', {}) or {}
    tk = rows_of(d.get('topKills', {}), 'topKills', 'kills')
    lives = rows_of(d.get('lives', {}), 'lives')
    pue = rows_of(d.get('powerupEvents', {}), 'events')
    shots = rows_of(d.get('shots', {}), 'shots')

    # sha: non-hub megademocache filenames ARE the content sha256; hub demos take it
    # from the cached overview.filePath.
    sha = None; ov = None
    if len(gid) == 64 and all(c in '0123456789abcdef' for c in gid.lower()):
        sha = gid.lower()
    else:
        try:
            ov = json.load(open(f'{SETS}/{gid}/overview.json'))
            sha = (ov.get('filePath') or '').split('.')[0] or None
        except Exception:
            ov = None
    cat = CATALOG.get(gid, {})
    # format: hub catalog if present, else the -view all roster-derived meta.format,
    # else the demo's own coarse Mode (Team/Duel).
    fmt = cat.get('mode') or meta.get('format') or meta.get('mode')

    g = gcount; gcount += 1
    sha_by_g[g] = sha
    games.append(dict(g=g, gid=gid, sha=sha, map=meta.get('map'), mode=fmt,
                      demoMode=meta.get('mode'), format=meta.get('format'),
                      teamSizes=meta.get('teamSizes'), matchtag=meta.get('matchtag'),
                      bucket=cat.get('bucket'), demoOffsetMs=meta.get('demoOffsetMs'),
                      uids=meta.get('uids') or {}, nkills=len(tk)))

    uids = meta.get('uids') or {}
    lives_by_player = defaultdict(list)
    for L in lives: lives_by_player[L['player']].append(L)
    pu_by_player = defaultdict(list)
    for e in pue: pu_by_player[e['player']].append(e)
    # per-player shot times, split rl/lg, sorted for bisect
    srl = defaultdict(list); slg = defaultdict(list)
    for s in shots:
        w = s.get('weapon')
        if w == 'rl': srl[s['player']].append(s['time'])
        elif w == 'lg': slg[s['player']].append(s['time'])
    for p in srl: srl[p].sort()
    for p in slg: slg[p].sort()
    def win_count(arr, lo, hi):
        return bisect.bisect_right(arr, hi) - bisect.bisect_left(arr, lo)

    for r in tk:
        ktime = r['time']; span = r.get('spanMs', 0); victim = r['victim']; killer = r['killer']
        lo = ktime - max(span, 1500) - 500; hi = ktime + 200
        kills.append(dict(
            g=g, killer=killer, victim=victim, killerUid=uids.get(killer),
            team=r.get('team'), weapon=r['weapon'], time=ktime,
            dmg=r['damage'], hits=r.get('hits', 0), span=span, gap=r.get('maxGapMs', 0),
            victimWep=r.get('victimWep'), taken=r.get('returnDamage', 0),
            killerDeathDeltaMs=killer_death_delta(lives_by_player, killer, ktime),
            killerQuad=had_quad(pu_by_player, killer, ktime),
            victimQuad=had_quad(pu_by_player, victim, ktime),
            vShotsRL=win_count(srl.get(victim, []), lo, hi),
            vShotsLG=win_count(slg.get(victim, []), lo, hi),
        ))
        if had_quad(pu_by_player, killer, ktime): quad_k += 1
        if had_quad(pu_by_player, victim, ktime): quad_v += 1

    # parity: top-20 by damage of our reduced kills vs cached overview.topKills (v67, top-20)
    if ov and ov.get('topKills'):
        api = ov['topKills']
        ours = sorted([k for k in kills if k['g'] == g], key=lambda x: -x['dmg'])[:len(api)]
        parity_demos += 1
        F = ('killer', 'victim', 'weapon', 'damage', 'hits', 'spanMs', 'maxGapMs', 'victimWep', 'returnDamage')
        amap = {(a['killer'], a['victim'], a['time']): a for a in api}
        for k in ours:
            a = amap.get((k['killer'], k['victim'], k['time']))
            if a and all(k[o] == a[f] for o, f in zip(('dmg','hits','span','gap','victimWep','taken'),
                                                      ('damage','hits','spanMs','maxGapMs','victimWep','returnDamage'))):
                parity_ok += 1
            else:
                parity_bad += 1

with open(f'{OUT}/games.jsonl', 'w') as f:
    for row in games: f.write(json.dumps(row) + '\n')
with open(f'{OUT}/kills.jsonl', 'w') as f:
    for row in kills: f.write(json.dumps(row) + '\n')

gsz = os.path.getsize(f'{OUT}/games.jsonl'); ksz = os.path.getsize(f'{OUT}/kills.jsonl')
nk = len(kills)
print(f"\n=== INDEX ===\n  games: {len(games)}  ({gsz/1e6:.1f} MB)\n  kills: {nk}  ({ksz/1e6:.1f} MB)  = {ksz/max(1,nk):.0f} B/row")
print(f"  rows/demo (over demos w/ kills): {nk/max(1,sum(1 for r in games if r.get('nkills',0)>0)):.0f}")
_modern = sum(1 for r in games if r.get('nkills', 0) > 0)
print(f"  MODERN (kills>0): {_modern}/{len(games)} ({100*_modern/max(1,len(games)):.0f}%)  |  0-kill (no_damage): {len(games)-_modern}")
print(f"  format mix: {dict(Counter(r.get('mode') for r in games))}")
print(f"\n=== PARITY (reduced top-20 vs cached overview.topKills) ===\n  demos checked: {parity_demos}  match: {parity_ok}  mismatch: {parity_bad}")
print(f"\n=== QUAD ===\n  killerQuad: {quad_k} ({100*quad_k/max(1,nk):.1f}%)  victimQuad: {quad_v} ({100*quad_v/max(1,nk):.1f}%)")
# est interned size: replace sha string (~64B) w/ int in each kill? kills have g(int) already; games hold sha once
print(f"\n=== est size @ 16.5k demos (linear) ===\n  kills ~{ksz/max(1,len(games))*16500/1e6:.0f} MB JSONL (interned FK already used in kills; sha lives once per game)")
print(f"\nmode source note: games.mode = catalog format ({dict(Counter(g['mode'] for g in games))}); games.demoMode = demo Team/Duel")
