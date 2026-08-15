// Columnar bucket-view accessors — the frame feed's wire shape.
//
// The analytics pipeline ships a column-major ColumnarBuckets object (see
// mvd-analytics/view/columnar.go):
//   { windowMs, start, count,
//     players: { name: { first, n, alive:[0/1], validFrom:{f:idx},
//                        h|a|li|sh|nl|rk|cl:[i16], x|y|z:[i32], at:[str],
//                        rl|lg|gl|ssg|sng|q|pe|r|sp|d:[0/1] } },
//     teams:   { name: { rl|lg|rllg|w|gl|q|pe|r|pw|th|ta:[int], abt:{ra|ya|ga:[int]} } } }
// Time axis is implicit: time(i) = (start + i*windowMs)/1000 seconds, so
// time→index is O(1) arithmetic (no per-bucket binary search). Booleans and
// the alive mask are 0/1. A player only "exists" at bucket i while alive[i]
// is set; values carry forward through dead buckets, so callers must gate on
// liveness exactly as the old row shape omitted dead players per bucket.
//
// These are pure functions over that object. MvdMap consumes them through
// setFrames()/frameAt(); the host's timeline panels read the same view
// through the same accessors so the two can never disagree on the row shape.

export function bucketTimeSec(view, i) {
    return (view.start + i * view.windowMs) / 1000;
}

// Bucket whose half-open span contains tSec (floor), clamped to a valid index.
export function bucketIndexAtTime(view, tSec) {
    if (!view || !view.count) return -1;
    let i = Math.floor((tSec * 1000 - view.start) / view.windowMs);
    if (i < 0) i = 0;
    if (i >= view.count) i = view.count - 1;
    return i;
}

// First bucket at or after tSec (ceil), clamped to [0, count]. Replaces the old
// binarySearchBucketStart for range scans over a visible window.
export function bucketIndexAtOrAfter(view, tSec) {
    if (!view || !view.count) return 0;
    let i = Math.ceil((tSec * 1000 - view.start) / view.windowMs);
    if (i < 0) i = 0;
    if (i > view.count) i = view.count;
    return i;
}

// Value of a player's field column at absolute bucket i, or undefined when the
// player is absent there (outside [first,first+n), dead, or before validFrom).
export function playerValAt(p, field, i) {
    if (!p) return undefined;
    const rel = i - p.first;
    if (rel < 0 || rel >= p.n) return undefined;
    if (!p.alive[rel]) return undefined;
    const vf = p.validFrom && p.validFrom[field];
    if (vf !== undefined && i < vf) return undefined;
    const arr = p[field];
    if (!arr) return undefined;
    return arr[rel];
}

export function playerAliveAt(p, i) {
    if (!p) return false;
    const rel = i - p.first;
    return rel >= 0 && rel < p.n && !!p.alive[rel];
}

// Field codes whose row-shape value is a boolean (emitted 0/1 in the columnar
// wire form) vs a number. armorType ("at") is a string.
const COLUMNAR_NUM_FIELDS = ['x', 'y', 'z', 'h', 'a', 'li', 'sh', 'nl', 'rk', 'cl'];
const COLUMNAR_BOOL_FIELDS = ['rl', 'lg', 'gl', 'ssg', 'sng', 'q', 'pe', 'r', 'sp', 'd'];

// reconstructBucketPlayers rebuilds the old row-shape p{} (player → field map)
// for the players alive at bucket i. Mirrors the Go columnarToRow oracle so the
// panels that still think row-major keep working unchanged.
export function reconstructBucketPlayers(view, i) {
    const out = {};
    const players = view.players || {};
    for (const name in players) {
        const cp = players[name];
        if (!playerAliveAt(cp, i)) continue;
        const pd = {};
        for (const f of COLUMNAR_NUM_FIELDS) {
            const v = playerValAt(cp, f, i);
            if (v !== undefined) pd[f] = v;
        }
        for (const f of COLUMNAR_BOOL_FIELDS) {
            const v = playerValAt(cp, f, i);
            if (v !== undefined) pd[f] = !!v;
        }
        const at = playerValAt(cp, 'at', i);
        if (at !== undefined) pd.at = at;
        out[name] = pd;
    }
    return out;
}

// teamSnapshot returns the old row-shape team-data object (counters + abt) for
// one team at bucket i, or {} when the team is absent.
export function teamSnapshot(view, team, i) {
    const t = view.teams && view.teams[team];
    if (!t) return {};
    const o = {};
    for (const k in t) {
        if (k === 'abt') {
            const abt = {};
            for (const a in t.abt) abt[a] = t.abt[a][i] || 0;
            o.abt = abt;
            continue;
        }
        o[k] = t[k][i] || 0;
    }
    return o;
}

// reconstructBucketTeams rebuilds the old row-shape td{} (team → data) at i.
export function reconstructBucketTeams(view, i) {
    if (!view.teams) return undefined;
    const td = {};
    for (const team in view.teams) td[team] = teamSnapshot(view, team, i);
    return td;
}
