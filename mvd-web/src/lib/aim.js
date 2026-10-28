// aim.js — the Aim Stats tab's DOM-free table parts: the victim filter, the
// per-weapon column descriptors, the measured / reconstructed / absent Hits
// rules, the Dmg join from playerStats, and the markup of every weapon
// table (src/lib/aim-plot.js has the crosshair image and histogram binning).
//
// Thin renderer over result.aim, which is computed in Go
// (mvd-analytics/analyzer/aim.go): per-player columnar crosshair-error samples
// (hitscan), LG ramp, rocket direct/splash, and LG reach. This layer only bins
// + paints — all geometry, target attribution and classification live in the
// analytics layer so every client (CLI / API / web) shares one truth.
//
// The victim filter is 'all' | 'enemy' | 'team' | 'self'. 'all' is the
// default — it preserves the server-parity numbers (KTX counts team and self
// hits too). Every function that depends on it takes it as an argument; the
// weapon tables hand it to the cells on the projected WeaponAim (`filter`).

import { escapeHtml } from './html.js';
import { RECON_ACCURACY_NOTE } from './accuracy.js';
import { getWeaponName } from './summary.js';
import { playerStatsRows, teamOrder } from './player-stats.js';

// The line under the player picker: how this demo's misses were attributed.
export function aimModeText(pa) {
    return pa.mode === 'duel'
        ? 'Target attribution: duel (exact — one enemy; hits use the confirmed victim)'
        : 'Target attribution: team game (hits → confirmed victim; misses → nearest-crosshair-enemy heuristic)';
}

// A reconstructed demo publishes TWO rl/gl hit counts on purpose — this tab's
// any-path one (it stands in for the withheld MEASURED counter, which counts
// that way) and the Summary's direct-impact one (KTX's own question). They
// differ by a lot, so the reader who has both on screen gets the
// reconciliation in the cell rather than only in the source.
export const RECON_ANYDAMAGE_NOTE = 'Counted as ANY fire that landed damage, splash included — ' +
    'the convention of the measured counter this cell stands in for. The Summary tab\'s Acc column answers ' +
    'KTX\'s question instead — projectiles that TOUCHED a player — which runs about 4x smaller on RL and 1.5x on GL.';

// Column descriptors: {h: header, t: tooltip, cell: (WeaponAim) -> html}. The
// Player column is prepended automatically (with the team-coloured stripe).
// Every count column has a share-of-fires % twin: counts depend on how much a
// player fired, so the percentages are what compare across players.
const pctPlain = p => `${p.toFixed(1)}%`;
const shotShare = (n, w) => pctPlain(w.shots ? (n || 0) / w.shots * 100 : 0);

// The RL/GL direct/splash pair is withheld PER ROW, not per demo — the one
// measured-only column that is, which is why it cannot ride aimCol's
// whole-demo swap below. The analyzer publishes it as a nullable field
// (result.WeaponAim: nil = the split never ran, 0 = it ran and counted none).
// The reachable case is aimcore's `projLinked` gate (aimcore/aim.go): no rl or
// gl fire anywhere in the demo linked to a projectile, so there is no evidence
// to split. It is rare where this cell renders at all: the split columns are
// measured-only, so of the 1 800 demos in the 3 123-demo reach audit that
// fired rl/gl and linked no projectile, the 1 793 without a damage stream
// never reach it (aimCol swaps those columns for AIM_MEASURED_ONLY_NOTE
// first); the other 7 have hitsMeasured and still no linked rl/gl fire, and
// they are what this note is for (probe P18). Rendering the null as 0 would
// publish "no rocket or grenade ever touched anybody" as a measurement.
export const AIM_SPLIT_WITHHELD_NOTE = 'Not classified on this demo: no rocket or grenade fire ' +
    'linked to a projectile, so there is nothing to split into direct hits and splash. ' +
    'Not a zero — the fires were simply never classified.';
function aimSplitCell(v, fmt) {
    if (v === null || v === undefined) {
        return `<span class="aim-na" title="${escapeHtml(AIM_SPLIT_WITHHELD_NOTE)}">—</span>`;
    }
    return fmt(v);
}
const aimSplitSort = v => (v === null || v === undefined) ? -1 : v;

export const AIM_COL = {
    shots: { h: 'Shots', t: 'Trigger pulls', cell: w => w.shots },
    hits: {
        h: 'Hits',
        t: 'Fires that connected. Measured off the wire damage stream where the demo carries one; recovered from the reconstructed damage log (marked) where it does not; — where neither could answer',
        cell: w => aimHitsCell(w, n => String(n)),
        sort: w => { const v = aimHitsValue(w); return v ? v.n : -1; },
    },
    // Joined in from playerStats.damage's three per-weapon maps, which
    // follow the Enemy/Team/Self victim toggle above (see aimDamageCell for
    // the measuredness rules). The cell carries a {n, lower, note} object,
    // not a number, so the sort key comes from `sort` rather than the text.
    dmg: {
        h: 'Dmg',
        t: 'Damage dealt with this weapon, following the Enemy/Team/Self victim filter above. All sums the three splits; a "≥" prefix marks a lower bound where one split is not measured on this demo',
        cell: w => aimDamageText(w.dmg),
        sort: w => (w.dmg && w.dmg.n !== null) ? w.dmg.n : -1,
    },
    hitPct: {
        h: 'Hit %',
        t: 'Share of fires that connected, over the same hit count as the Hits column beside it — measured, reconstructed (marked) or —',
        cell: w => aimHitsCell(w, n => pctPlain(w.shots ? n / w.shots * 100 : 0)),
        sort: w => { const v = aimHitsValue(w); return (v && w.shots) ? v.n / w.shots : -1; },
    },
    // `measured: true` marks a column that exists only where the wire damage
    // log linked fires to damage. The analyzer WITHHOLDS those fields on a
    // demo without one (see the shapes above: a reconstructed weapon carries
    // shots + recon and nothing else), so their `|| 0` fallbacks would print
    // a fabricated zero — and beside a recovered Hit % of 22% a "0.0% miss"
    // reads as a measurement that contradicts it. aimCol swaps them for "—".
    fired: { h: 'Pellets Fired', t: 'Pellets fired (6 SG / 14 SSG per shot)', measured: true, cell: w => w.pellets || 0 },
    pHit: { h: 'Pellets Hit', t: 'Pellets that hit (matches the server)', measured: true, cell: w => w.pelletHits || 0 },
    pAcc: { h: 'Pellet %', t: 'Per-pellet accuracy', measured: true, cell: w => pctPlain(w.pellets ? (w.pelletHits || 0) / w.pellets * 100 : 0) },
    full: { h: 'Full', t: 'Fires where every pellet hit', measured: true, cell: w => w.full || 0 },
    partial: { h: 'Partial', t: 'Fires where some but not all pellets hit', measured: true, cell: w => w.partial || 0 },
    miss: { h: 'Miss', t: 'Fires where no pellet hit', measured: true, cell: w => w.miss || 0 },
    fullPct: { h: 'Full %', t: 'Share of fires where every pellet hit', measured: true, cell: w => shotShare(w.full, w) },
    partialPct: { h: 'Partial %', t: 'Share of fires where some but not all pellets hit', measured: true, cell: w => shotShare(w.partial, w) },
    missPct: { h: 'Miss %', t: 'Share of fires where no pellet hit', measured: true, cell: w => shotShare(w.miss, w) },
    direct: {
        h: 'Direct',
        t: 'Projectiles that TOUCHED a player — KTX\'s own RL/GL hits counter. RL is the server\'s own flag; GL is re-derived from the grenade\'s flight and its 2.5s fuse, since a grenade that touches explodes and every row of its damage is flagged splash (92% of archive player rows exact against the KTX block). A GL Direct can exceed its Hits: a touch whose fire the linker missed is still a touch',
        measured: true, cell: w => aimSplitCell(w.direct, n => String(n)), sort: w => aimSplitSort(w.direct),
    },
    splash: {
        h: 'Splash', t: 'Hits from splash only',
        measured: true, cell: w => aimSplitCell(w.splash, n => String(n)), sort: w => aimSplitSort(w.splash),
    },
    missed: { h: 'Missed', t: 'Fires that hit nothing', measured: true, cell: w => w.missed || 0 },
    directPct: {
        h: 'Direct %', t: 'Share of fires that hit directly',
        measured: true, cell: w => aimSplitCell(w.direct, n => shotShare(n, w)), sort: w => aimSplitSort(w.direct),
    },
    splashPct: {
        h: 'Splash %', t: 'Share of fires that hit via splash only',
        measured: true, cell: w => aimSplitCell(w.splash, n => shotShare(n, w)), sort: w => aimSplitSort(w.splash),
    },
    missedPct: { h: 'Missed %', t: 'Share of fires that hit nothing', measured: true, cell: w => shotShare(w.missed, w) },
    blocked: { h: 'Blocked', t: 'Miss — the beam would have hit an enemy in range, but an object stopped it short', measured: true, cell: w => w.blocked || 0 },
    lgMiss: { h: 'Miss', t: 'Miss — aim error, no enemy on the beam\'s line', measured: true, cell: w => w.miss || 0 },
    far: { h: 'Far', t: 'Miss — the enemy was on the beam\'s line but beyond its ~600u reach', measured: true, cell: w => w.outOfRange || 0 },
    unres: { h: 'Unresolved', t: 'Miss that could not be classified — no beam matched this fire (needs the beam stream / shooter position at fire time)', measured: true, cell: w => w.unresolved || 0 },
    blockedPct: { h: 'Blocked %', t: 'Share of fires denied by an object in the way', measured: true, cell: w => shotShare(w.blocked, w) },
    lgMissPct: { h: 'Miss %', t: 'Share of fires missed on aim', measured: true, cell: w => shotShare(w.miss, w) },
    farPct: { h: 'Far %', t: 'Share of fires denied by beam range', measured: true, cell: w => shotShare(w.outOfRange, w) },
};

// What a withheld measured-only column says when hovered. The hit COUNT may
// still be recoverable (that is what the Hits column does); the breakdown of
// how a fire hit or missed is not — nothing in the reconstruction classifies
// a pellet spread or an LG whiff.
export const AIM_MEASURED_ONLY_NOTE = 'Not measured on this demo: it carries no wire damage log, ' +
    'and the reconstruction recovers hit COUNTS only — never the pellet, direct/splash or miss-type breakdown.';

// aimCol resolves one column descriptor against the demo's hit measuredness,
// so the cell functions themselves stay demo-agnostic.
export function aimCol(c, hitsMeasured) {
    if (hitsMeasured || !c.measured) return c;
    return {
        ...c,
        cell: () => `<span class="aim-na" title="${escapeHtml(AIM_MEASURED_ONLY_NOTE)}">—</span>`,
        sort: () => -1,
    };
}
// Per-weapon column order: counts first, then the share-of-fires block.
// SG/SSG lead with the pellet stats.
export const AIM_TABLE_COLS = {
    lg: ['shots', 'hits', 'dmg', 'lgMiss', 'blocked', 'far', 'unres', 'hitPct', 'lgMissPct', 'blockedPct', 'farPct'],
    sg: ['fired', 'pHit', 'pAcc', 'shots', 'hits', 'dmg', 'full', 'partial', 'miss', 'hitPct', 'fullPct', 'partialPct', 'missPct'],
    ssg: ['fired', 'pHit', 'pAcc', 'shots', 'hits', 'dmg', 'full', 'partial', 'miss', 'hitPct', 'fullPct', 'partialPct', 'missPct'],
    rl: ['shots', 'hits', 'dmg', 'direct', 'splash', 'missed', 'hitPct', 'directPct', 'splashPct', 'missedPct'],
    gl: ['shots', 'hits', 'dmg', 'direct', 'splash', 'missed', 'hitPct', 'directPct', 'splashPct', 'missedPct'],
    // Nails (web parse only — the WASM build turns on BuildNails). Nail
    // accuracy is approximate (svc_nails fires in bursts, one damage credited
    // per pull), so ng/sng show only the generic shots/hits/hitPct counters —
    // no direct/splash/pellet split, which nails don't carry.
    ng: ['shots', 'hits', 'dmg', 'hitPct'],
    sng: ['shots', 'hits', 'dmg', 'hitPct'],
    // The axe carries the same four: one swing sound per attack (schema v71),
    // linked to damage at the traceline's real +200 ms delay, and no spread,
    // splash or beam to classify a miss by.
    axe: ['shots', 'hits', 'dmg', 'hitPct'],
};
// Two-row header: [label, span] pairs in column order, spans summing to the
// weapon's AIM_TABLE_COLS length. Not a counts-vs-% split — sg/ssg interleave
// pAcc (a percentage) between the pellet counts and the shot counts, so the
// groups are named after what they count. A weapon absent here (ng/sng, four
// columns) keeps the flat single-row header.
export const AIM_TABLE_GROUPS = {
    lg: [['Shots', 7], ['%', 4]],
    sg: [['Pellets', 3], ['Shots', 6], ['%', 4]],
    ssg: [['Pellets', 3], ['Shots', 6], ['%', 4]],
    rl: [['Shots', 6], ['%', 4]],
    gl: [['Shots', 6], ['%', 4]],
};
// Weapon tables, in the order they appear. The axe is last because it is the
// least of them, not because it is optional: the shot stream has carried axe
// swings since schema v71 and the recon tier recovers its hits, and its
// absence from this list was silently dropping every one of them — 107 swings
// on one player in the committed corpus, with no table anywhere to show them.
const AIM_WEAPON_ORDER = ['lg', 'sg', 'ssg', 'rl', 'gl', 'ng', 'sng', 'axe'];

// aimWeaponView projects a WeaponAim onto the active victim filter so the
// AIM_COL cell functions stay bucket-agnostic. The analyzer emits the enemy
// split only when it differs from the top-level counters, so its absence
// means enemy == all; team/self splits are absent when that bucket never
// connected (zeros). Shots (and pellets fired) are filter-independent —
// per-bucket splash/missed derive from them (splash = hits − direct,
// missed = shots − hits). LG miss/blocked/far ride through unchanged:
// misses have no victim, so they don't move with the filter (the lg miss
// bucket shares the `miss` field with the pellet split, so it must dodge
// the pellet projection below).
export function aimWeaponView(w, filter) {
    if (!w || filter === 'all') return w;
    // "No enemy split" means enemy == all for the MEASURED counters, which is
    // why they ride through untouched — but it says nothing about the recon
    // tier, which never carries a split at all (so on a reconstructed demo
    // this branch is the one every weapon takes). Dropping `recon` here is
    // what stops a whole-match count — team and self hits included — from
    // being served under the Enemy heading; the Hits cell then says so.
    if (filter === 'enemy' && !w.enemy) return { ...w, recon: null };
    const s = (filter === 'enemy' ? w.enemy
        : filter === 'team' ? w.team : w.self) || {};
    // The bucket's direct carries the same nil-is-not-zero rule as the
    // top-level one, and the derived splash rides on it: with no classified
    // direct there is no "hits that were splash-only" either, so both stay
    // null and both cells say withheld. An ABSENT bucket is not the same
    // absence — it means that victim class never connected, i.e. zeros — so
    // it reads 0 wherever the weapon's split ran at all, and only inherits
    // the withhold when it did not.
    const hits = s.hits || 0;
    const classified = w.direct !== null && w.direct !== undefined;
    const direct = ('direct' in s) ? s.direct : (classified ? 0 : null);
    return {
        // The recon tier is a single whole-match hit count with no victim
        // splits (RESULT_SCHEMA, WeaponAimRecon), so it cannot ride a
        // projection — dropping it here is what makes the Hits cell say
        // "no victim split" instead of serving an all-victims number under
        // an Enemy/Team/Self heading.
        ...w, hits, direct, recon: null,
        pelletHits: s.pelletHits || 0,
        full: s.full || 0, partial: s.partial || 0,
        miss: w.weapon === 'lg' ? (w.miss || 0) : (s.miss || 0),
        splash: direct === null ? null : Math.max(0, hits - direct),
        missed: (w.shots || 0) - hits,
    };
}

// The Hits / Hit % pair serves three states, and the third is the v73/v74
// recon tier. `hits` is the MEASURED counter, withheld (never zeroed) on a
// demo with no wire damage log; `recon.hits` is the reconstruction's
// recovered count over that same fire set — same definition, same question,
// so it divides by the same `shots`. A block's ABSENCE means the weapon was
// not recovered (ng/sng never are), while `hits: 0` inside a present block is
// a real "linked nothing" and prints as 0.
//
// Deliberately NOT `recon.directHits`, the v74 companion for rl/gl. That one
// answers KTX's question — projectiles that touched a player — and is what
// the Summary's Acc column publishes on a reconstructed demo, which is why an
// RL figure here reads higher than the one there. This tab is the aim tier
// and stands in for the MEASURED counter beside it, which is an any-path
// count; swapping the definition under it would make the two states of this
// same cell incomparable.
//
// Returns {n, recon} or null for "no answer on this demo".
export function aimHitsValue(w) {
    if (w.hitsMeasured !== false) return { n: w.hits || 0, recon: false };
    if (w.recon) return { n: w.recon.hits, recon: true };
    return null;
}

// Why a cell has no number, in the reader's terms. Both branches are
// reachable on a reconstructed demo: the tier covers lg/sg/ssg/axe/rl/gl and
// stops there, and it carries no victim split for the toggle above to slice.
function aimHitsNote(w, filter) {
    if (!w.reconTier) {
        return 'This demo carries no damage log to measure hits against, and none could be reconstructed from it.';
    }
    return filter === 'all'
        ? 'Hits were not recovered for this weapon — the reconstruction covers LG, SG, SSG, axe, RL and GL only.'
        : 'The reconstructed hit count is a whole-match figure with no victim split — set Victims to All to see it.';
}

// aimHitsCell renders one hit-derived cell. `fmt` turns the count into the
// column's own text (a bare number, or a coloured percentage), so both
// columns share one measured/reconstructed/absent decision. `w.filter` is
// the active victim filter (aimWeaponTables puts it there).
export function aimHitsCell(w, fmt) {
    const v = aimHitsValue(w);
    if (!v) return `<span class="aim-na" title="${escapeHtml(aimHitsNote(w, w.filter))}">—</span>`;
    if (!v.recon) return fmt(v.n);
    const notes = [RECON_ACCURACY_NOTE];
    // rl/gl are the two weapons whose recovered count has a published rival on
    // the Summary tab (recon.directHits, the KTX convention); everywhere else
    // the two conventions coincide and the sentence would invent a difference.
    if (w.weapon === 'rl' || w.weapon === 'gl') notes.push(RECON_ANYDAMAGE_NOTE);
    return `<span class="stat-recon" title="${escapeHtml(notes.join(' · '))}">${fmt(v.n)}</span>`;
}

// aimDamageCell resolves the Dmg cell for one player + weapon under the
// victim filter. `dmg` is that player's playerStats damage family, absent
// on a demo carrying no damage information at all.
//
// Measuredness follows the two rules result.PlayerStatsDamage documents and
// nothing else: byWeapon and byWeaponTeam are measured whenever the family
// is present, byWeaponSelf only where a damage stream was read — which is
// exactly what a non-null `taken` says. Within a measured map an absent KEY
// means "dealt none with that weapon", so it reads 0, never "not measured";
// on a KTX-only demo a weapon used purely for team splash genuinely is
// {enemy: 0, team: N}.
//
// Returns {n, lower, note}. n === null is "not measured" (renders "-").
// `lower` marks an All-mode PARTIAL sum: the three splits are per-hit and
// mutually exclusive, so summing them is only correct when every one is
// measured — otherwise the figure is labelled a lower bound rather than
// served as a total or padded with a fabricated zero.
export function aimDamageCell(dmg, wn, filter) {
    if (!dmg) return { n: null, lower: false, note: 'No damage information for this player on this demo.' };
    const selfMeasured = dmg.taken !== null && dmg.taken !== undefined;
    const enemy = (dmg.byWeapon || {})[wn] || 0;
    const team = (dmg.byWeaponTeam || {})[wn] || 0;
    const self = (dmg.byWeaponSelf || {})[wn] || 0;
    const noSelf = 'The self-damage split is not measured on this demo — it needs the damage stream, which this demo does not carry.';
    switch (filter) {
        case 'enemy': return { n: enemy, lower: false, note: '' };
        case 'team': return { n: team, lower: false, note: '' };
        case 'self':
            return selfMeasured
                ? { n: self, lower: false, note: '' }
                : { n: null, lower: false, note: noSelf };
        default:
            return selfMeasured
                ? { n: enemy + team + self, lower: false, note: '' }
                : { n: enemy + team, lower: true, note: 'Lower bound: enemy + team damage only. ' + noSelf };
    }
}

export function aimDamageText(d) {
    if (!d || d.n === null) {
        const t = d && d.note ? ` title="${escapeHtml(d.note)}"` : '';
        return `<span class="aim-na"${t}>-</span>`;
    }
    if (d.lower) return `<span title="${escapeHtml(d.note)}">&ge;${d.n}</span>`;
    return String(d.n);
}

// aimWeaponTables builds one table per weapon, rows = players (all who fired
// anything; "-" where they didn't fire this weapon), team-coloured like the
// Summary tab: `canonical` / `colors` are the teams store's order and
// colours. Player-independent (rebuilt when the victim filter changes).
// Returns [{ weapon, html }], html being the block's inner markup (title +
// table) — rows are markup so the shared makeSortable can sort them.
export function aimWeaponTables(result, filter, canonical, colors) {
    const players = (result && result.aim && result.aim.players) || [];
    if (!players.length) return [];
    // hitsMeasured=false: the demo carried no wire damage stream, so the
    // measured hit counters are withheld server-side. hitsSource then says
    // whether the reconstruction could recover them anyway (schema v73/v74) —
    // the per-weapon `recon` blocks the Hits / Hit % cells fall back to.
    const hitsMeasured = result.aim.hitsMeasured !== false;
    const reconTier = result.aim.hitsSource === 'reconstructed';
    const order = teamOrder(canonical, []); // canonical frag-sorted order (= Summary)
    // result.aim carries no damage, so the Dmg column is joined by player
    // name from playerStats — the same source (and the same weapon keys) the
    // Summary weapon table reads (formatWeaponCells, src/lib/summary.js). The
    // whole damage family is carried, not just byWeapon: the column follows
    // the victim filter and needs `taken` for the self split's measuredness.
    const dmgByPlayer = new Map(playerStatsRows(result).map(p => [p.name, p.damage || null]));

    const out = [];
    for (const wn of AIM_WEAPON_ORDER) {
        const colKeys = AIM_TABLE_COLS[wn] || [];
        const cols = colKeys.map(k => aimCol(AIM_COL[k], hitsMeasured));
        const rows = players.map(pa => {
            const w = aimWeaponView((pa.weapons || []).find(x => x.weapon === wn) || null, filter);
            return {
                player: pa.player, team: pa.team,
                w: w ? { ...w, hitsMeasured, reconTier, filter, dmg: aimDamageCell(dmgByPlayer.get(pa.player), wn, filter) } : null,
            };
        });
        if (!rows.some(r => r.w)) continue; // weapon nobody fired → no table
        rows.sort((a, b) => (b.w ? b.w.shots : -1) - (a.w ? a.w.shots : -1));

        // Column indices that open a header group carry the .group-start
        // separator, in the header rows and in the body alike.
        const groups = AIM_TABLE_GROUPS[wn];
        const starts = new Set();
        if (groups) {
            let at = 0;
            for (const [, span] of groups) { starts.add(at); at += span; }
        }
        const cellCls = i => starts.has(i) ? ' class="group-start"' : '';
        const subHead = cols.map((c, i) =>
            `<th${cellCls(i)} title="${escapeHtml(c.t)}">${escapeHtml(c.h)}</th>`).join('');
        const head = groups
            ? `<tr><th rowspan="2">Player</th>` +
              groups.map(([label, span]) =>
                  `<th class="col-group group-start" colspan="${span}">${escapeHtml(label)}</th>`).join('') +
              `</tr><tr>${subHead}</tr>`
            : `<tr><th>Player</th>${subHead}</tr>`;
        const naCells = cols.map((_c, i) => `<td class="aim-na${starts.has(i) ? ' group-start' : ''}">-</td>`).join('');
        let body = '';
        rows.forEach(r => {
            const ti = order.indexOf(r.team || '');
            const color = (ti >= 0 && ti < colors.length) ? colors[ti] : '';
            const stripe = color ? ` style="border-left: 3px solid ${color}"` : '';
            const tds = `<td>${escapeHtml(r.player)}</td>` +
                (r.w ? cols.map((c, i) => {
                    // A column whose text is not its own sort key (Dmg's "≥N"
                    // / "-") publishes one via data-sort-value.
                    const sv = c.sort ? ` data-sort-value="${c.sort(r.w)}"` : '';
                    return `<td${cellCls(i)}${sv}>${c.cell(r.w)}</td>`;
                }).join('') : naCells);
            body += `<tr${stripe}>${tds}</tr>`;
        });

        out.push({
            weapon: wn,
            html: `<h4>${escapeHtml(getWeaponName(wn))}</h4>` +
                `<table class="stats-table aim-weapon-table"><thead>${head}</thead><tbody>${body}</tbody></table>`,
        });
    }
    return out;
}

// The player picker's chips: one per result.aim player, in its order, with
// the canonical team colour of the player's team ('' when it has none).
export function aimChips(result, canonical, colors) {
    const players = (result && result.aim && result.aim.players) || [];
    const order = teamOrder(canonical, []);
    return players.map(p => {
        const ti = order.indexOf(p.team || '');
        return { player: p.player, color: (ti >= 0 && ti < colors.length) ? colors[ti] : '' };
    });
}
