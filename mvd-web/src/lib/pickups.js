// pickups.js — the Pickups tab's DOM-free parts: per-entity pickup counts,
// the column set of each table, and every row's cells as markup.
//
// Two tables — Weapon Pickups (RL/LG/GL/SNG with per-entity columns,
// pack column for RL/LG, and a Σ that aggregates the kind) and Item
// Pickups (armors, healths, powerups). Both react to a mode selector:
//
//   "All pickups"          → Σ vs KTX total-taken; pack vs ttooks-sttooks
//   "First pickup per life" → Σ vs KTX taken;       pack vs tooks-stooks
//
// Per-entity columns always show every-touch from items.phases. The
// verify cell shows KTX's counter when the demo carries one (verifyCell).
//
// Each kind also ends in a possession (`s`) column — total seconds held,
// joined from playerStats.hold. It is per KIND, not per entity: possession
// is an integral over the inventory stream and knows nothing about which
// pad or pack the item came from. It ignores the mode selector for the same
// reason.
//
// Note: GL/SNG packs aren't tracked on the analyser side (KTX only emits
// `//ktx bp` for RL/LG; ktx/src/items.c:2471), so those weapons compare
// against KTX's entity-only fields (spawn-taken / spawn-total-taken).

import { escapeHtml } from './html.js';
import { holdCell, playerStatsRows, playerStatsTeamRows, sortByFragsDesc, teamOrder } from './player-stats.js';

/**
 * A kind the tables have columns for.
 * @typedef {{ kind: string, label: string, ktxName: string, hasPack?: boolean, hold?: 'weapons' | 'armor' | 'powerups' }} KindSpec
 * The Result JSON these tables read: a demoInfo player (KTX's own
 * counters), a result.items entity and a result.weaponPickups row.
 * @typedef {{ name: string, team?: string, stats?: { frags?: number }, weapons?: Record<string, any>, items?: Record<string, any>, [field: string]: any }} KtxPlayer
 * @typedef {{ kind: string, entNum: number, loc?: string, name?: string, phases?: Array<{ takenBy?: string, team?: string }> }} ItemEnt
 * @typedef {{ weapon: string, player?: string, team?: string, source?: string, hadBefore?: boolean, [field: string]: any }} WeaponPickup
 *
 * One column of a pickups table.
 * @typedef {{ type: 'hold', kindSpec: KindSpec, header: string, headerTitle: string }} HoldCol
 * @typedef {{ type: 'weap-verify' | 'weap-pack', kindSpec: KindSpec, header: string, headerTitle?: undefined }} WeapCol
 * @typedef {{ type: 'entity-count', kindSpec: KindSpec, entNum: number, header: string, headerTitle?: undefined }} EntityCol
 * @typedef {{ type: 'item-verify', kindSpec: KindSpec, entNums: number[], header: string, headerTitle?: undefined }} ItemVerifyCol
 * @typedef {HoldCol | WeapCol | EntityCol | ItemVerifyCol} PickupCol
 *
 * @typedef {ReturnType<typeof computePickupsState>} PickupsState
 * @typedef {'all' | 'first'} PickupMode
 * @typedef {{ colorIdx: number, html: string }} PickupsRow
 * @typedef {{ head: string, rows: PickupsRow[] }} PickupsTable
 */

// `hold` names the playerStats.hold family a kind's possession seconds live
// in (result/player_stats.go PlayerStatsHold), keyed by `kind`. Possession is
// an integral over the player's inventory stream, so it belongs to the KIND,
// never to the spawn point the item came from — one column per kind no matter
// how many pads the map has. MH has no entry on purpose: mega health is
// consumed on pickup, so the section carries no hold stat for it.
/** @type {KindSpec[]} */
const PICKUPS_WEAPON_KINDS = [
    { kind: 'rl',  label: 'RL',  ktxName: 'rl',  hasPack: true,  hold: 'weapons' },
    { kind: 'lg',  label: 'LG',  ktxName: 'lg',  hasPack: true,  hold: 'weapons' },
    { kind: 'gl',  label: 'GL',  ktxName: 'gl',  hasPack: false, hold: 'weapons' },
    { kind: 'sng', label: 'SNG', ktxName: 'sng', hasPack: false, hold: 'weapons' },
];

/** @type {KindSpec[]} */
const PICKUPS_ITEM_KINDS = [
    { kind: 'ra',   label: 'RA',   ktxName: 'ra',         hold: 'armor'    },
    { kind: 'ya',   label: 'YA',   ktxName: 'ya',         hold: 'armor'    },
    { kind: 'ga',   label: 'GA',   ktxName: 'ga',         hold: 'armor'    },
    { kind: 'mh',   label: 'MH',   ktxName: 'health_100'                   },
    { kind: 'quad', label: 'Quad', ktxName: 'q',          hold: 'powerups' },
    { kind: 'pent', label: 'Pent', ktxName: 'p',          hold: 'powerups' },
    { kind: 'ring', label: 'Ring', ktxName: 'r',          hold: 'powerups' },
];

/**
 * @param {KtxPlayer} player
 * @param {string} ktxName
 * @returns {Record<string, number>}
 */
function pickupsWeaponDetail(player, ktxName) {
    const pk = player.weapons?.[ktxName]?.pickups || {};
    return {
        'first pickup, any source': pk['taken'] || 0,
        'every touch, any source':  pk['total-taken'] || 0,
        'first pickup of items':    pk['spawn-taken'] || 0,
        'every item touch':         pk['spawn-total-taken'] || 0,
    };
}

/**
 * @param {Record<string, number> | null} into
 * @param {Record<string, number> | null} add
 * @returns {Record<string, number> | null}
 */
function pickupsSumDetail(into, add) {
    if (!add) return into;
    if (!into) into = {};
    for (const [k, v] of Object.entries(add)) into[k] = (into[k] || 0) + v;
    return into;
}

// computePickupsState gathers everything the two tables read. `canonical`
// is the teams store's order (src/state/teams.js); the tables' team order
// falls back to the frag-sorted players' when it names fewer than two.
/**
 * @param {import('./result.js').Result} result
 * @param {string[] | null | undefined} canonical
 */
export function computePickupsState(result, canonical) {
    /** @type {KtxPlayer[]} */
    const players = result.demoInfo?.players || [];
    const order = teamOrder(canonical, sortByFragsDesc(players));
    const playerByName = new Map(players.map(p => [p.name, p]));

    /** @type {ItemEnt[]} */
    const items = result.items?.items || [];
    /** @type {Map<string, ItemEnt[]>} */
    const weaponEntsByKind = new Map();
    /** @type {Map<string, ItemEnt[]>} */
    const itemEntsByKind = new Map();
    for (const it of items) {
        if (PICKUPS_WEAPON_KINDS.some(k => k.kind === it.kind)) {
            if (!weaponEntsByKind.has(it.kind)) weaponEntsByKind.set(it.kind, []);
            /** @type {ItemEnt[]} */ (weaponEntsByKind.get(it.kind)).push(it);
        } else if (PICKUPS_ITEM_KINDS.some(k => k.kind === it.kind)) {
            if (!itemEntsByKind.has(it.kind)) itemEntsByKind.set(it.kind, []);
            /** @type {ItemEnt[]} */ (itemEntsByKind.get(it.kind)).push(it);
        }
    }
    /** @type {(a: ItemEnt, b: ItemEnt) => number} */
    const sortByLoc = (a, b) => {
        const la = a.loc || ''; const lb = b.loc || '';
        if (la !== lb) return la.localeCompare(lb);
        return a.entNum - b.entNum;
    };
    for (const list of weaponEntsByKind.values()) list.sort(sortByLoc);
    for (const list of itemEntsByKind.values()) list.sort(sortByLoc);

    /** @type {Map<string, Map<number, number>>} */
    const entityCountsByPlayer = new Map(); // name -> Map(entNum -> count)
    /** @type {Map<string, Map<number, number>>} */
    const entityCountsByTeam = new Map();   // team -> Map(entNum -> count)
    for (const it of items) {
        for (const ph of it.phases || []) {
            const who = ph.takenBy;
            if (!who) continue;
            if (!entityCountsByPlayer.has(who)) entityCountsByPlayer.set(who, new Map());
            const pm = /** @type {Map<number, number>} */ (entityCountsByPlayer.get(who));
            pm.set(it.entNum, (pm.get(it.entNum) || 0) + 1);
            const team = ph.team || playerByName.get(who)?.team || '';
            if (!entityCountsByTeam.has(team)) entityCountsByTeam.set(team, new Map());
            const tm = /** @type {Map<number, number>} */ (entityCountsByTeam.get(team));
            tm.set(it.entNum, (tm.get(it.entNum) || 0) + 1);
        }
    }

    // Whether the demo carries KTX's own pickup counters (weapons[].pickups
    // / items[].took). When present they are authoritative and the verify
    // cells display them; old / non-KTX demos fall back to the
    // analytics-derived counts.
    const hasKtxWeaponCounters = players.some(p =>
        p.weapons && Object.values(p.weapons).some(w => w && w.pickups));
    const hasKtxItemCounters = players.some(p =>
        p.items && Object.keys(p.items).length > 0);

    // Possession comes from playerStats, which these tables otherwise don't
    // read (their rows are demoInfo.players). Both sides key by name — the
    // team rows' identity field is `name`, the same string demoInfo puts on
    // a player's `team` — so a plain lookup joins them. A demo without the
    // section (or a team the section doesn't know) simply misses, and
    // holdCell renders '-'.
    const psByName = new Map(playerStatsRows(result).map(r => [r.name, r]));
    const psTeamByName = new Map(playerStatsTeamRows(result).map(r => [r.name, r]));

    return {
        result, players, teamOrder: order, playerByName,
        weaponEntsByKind, itemEntsByKind,
        entityCountsByPlayer, entityCountsByTeam,
        weaponPickups: /** @type {WeaponPickup[]} */ (result.weaponPickups || []),
        hasKtxWeaponCounters, hasKtxItemCounters,
        psByName, psTeamByName,
    };
}

// Whether the demo has any weapon / item entity the tables count; neither
// is the tab's empty state.
/** @param {PickupsState} state */
export function hasPickups(state) {
    return {
        weapon: PICKUPS_WEAPON_KINDS.some(k => (state.weaponEntsByKind.get(k.kind) || []).length > 0),
        item: PICKUPS_ITEM_KINDS.some(k => (state.itemEntsByKind.get(k.kind) || []).length > 0),
    };
}

// The possession column for one kind, appended after that kind's last count
// column. `s` mirrors the Summary table's possession heading.
/**
 * @param {KindSpec} spec
 * @param {boolean} isWeapon
 * @returns {HoldCol}
 */
function holdCol(spec, isWeapon) {
    const verb = spec.hold === 'weapons' ? 'holding the' : spec.hold === 'armor' ? 'wearing' : 'under';
    const title = `Total seconds ${verb} ${spec.label} — the whole match's possession for this item kind, ` +
        `regardless of which spawn or pack it came from` +
        (isWeapon ? '. Independent of the pickup-mode select: possession is the same integral either way.' : '.');
    return {
        type: 'hold',
        kindSpec: spec,
        header: `${spec.label} <span class="pickups-verify-header">s</span>`,
        headerTitle: title,
    };
}

// Possession cell for a pickups row. Team rows read the pre-summed team row
// from playerStats (never a JS re-aggregation of member shares — see the
// comment on teamRowsInColourOrder in player-stats.js).
/**
 * @param {HoldCol} col
 * @param {boolean} isTeam
 * @param {string} key
 * @param {PickupsState} state
 */
function holdCellFor(col, isTeam, key, state) {
    const row = isTeam ? state.psTeamByName.get(key) : state.psByName.get(key);
    // Every kind with a hold column names its family.
    const stat = row?.hold?.[/** @type {string} */ (col.kindSpec.hold)]?.[col.kindSpec.kind];
    return holdCell(stat, row?.window);
}

/**
 * @param {PickupsState} state
 * @returns {PickupCol[]}
 */
export function buildWeaponCols(state) {
    /** @type {PickupCol[]} */
    const cols = [];
    for (const spec of PICKUPS_WEAPON_KINDS) {
        const list = state.weaponEntsByKind.get(spec.kind) || [];
        if (list.length === 0) continue;
        if (list.length === 1 && !spec.hasPack) {
            // Single combined cell: kind label, mode-aware verify against KTX.
            cols.push({ type: 'weap-verify', kindSpec: spec, header: spec.label });
            if (spec.hold) cols.push(holdCol(spec, true));
            continue;
        }
        if (list.length === 1) {
            cols.push({
                type: 'entity-count',
                kindSpec: spec,
                entNum: list[0].entNum,
                header: spec.label,
            });
        } else {
            for (const it of list) {
                const tag = (it.loc && it.loc.length > 0) ? it.loc : it.name;
                cols.push({
                    type: 'entity-count',
                    kindSpec: spec,
                    entNum: it.entNum,
                    header: `${spec.label} <span class="pickups-loc">@ ${escapeHtml(tag)}</span>`,
                });
            }
        }
        if (spec.hasPack) {
            cols.push({ type: 'weap-pack', kindSpec: spec, header: `${spec.label} pack` });
        }
        cols.push({
            type: 'weap-verify',
            kindSpec: spec,
            header: `${spec.label} <span class="pickups-verify-header">Σ</span>`,
        });
        if (spec.hold) cols.push(holdCol(spec, true));
    }
    return cols;
}

/**
 * @param {PickupsState} state
 * @returns {PickupCol[]}
 */
export function buildItemCols(state) {
    /** @type {PickupCol[]} */
    const cols = [];
    for (const spec of PICKUPS_ITEM_KINDS) {
        const list = state.itemEntsByKind.get(spec.kind) || [];
        if (list.length === 0) continue;
        if (list.length === 1) {
            cols.push({ type: 'item-verify', kindSpec: spec, entNums: [list[0].entNum], header: spec.label });
            if (spec.hold) cols.push(holdCol(spec, false));
            continue;
        }
        for (const it of list) {
            const tag = (it.loc && it.loc.length > 0) ? it.loc : it.name;
            cols.push({
                type: 'entity-count',
                kindSpec: spec,
                entNum: it.entNum,
                header: `${spec.label} <span class="pickups-loc">@ ${escapeHtml(tag)}</span>`,
            });
        }
        cols.push({
            type: 'item-verify',
            kindSpec: spec,
            entNums: list.map(it => it.entNum),
            header: `${spec.label} <span class="pickups-verify-header">Σ</span>`,
        });
        if (spec.hold) cols.push(holdCol(spec, false));
    }
    return cols;
}

/**
 * @param {string} html
 * @param {string} [title]
 */
function th(html, title) {
    return title ? `<th title="${escapeHtml(title)}">${html}</th>` : `<th>${html}</th>`;
}

/** @param {string} html */
function td(html) {
    return `<td>${html}</td>`;
}

/**
 * @param {Map<number, number>} entMap
 * @param {number} entNum
 */
function entityCountCell(entMap, entNum) {
    const n = entMap.get(entNum) || 0;
    return td(n === 0 ? '<span class="muted">0</span>' : String(n));
}

// pickupsSection builds one table pair (per team, per player) for a kind
// set: `kind` is 'weap' or 'item', `mode` 'all' | 'first'. Returns null when
// the demo has no column for it (the tables hide); otherwise each table's
// header row and body rows as markup, a row carrying the index of its team
// in the canonical colour order (-1 when it has none).
/**
 * @param {PickupsState} state
 * @param {'weap' | 'item'} kind
 * @param {PickupMode} mode
 * @returns {{ team: PickupsTable, player: PickupsTable } | null}
 */
export function pickupsSection(state, kind, mode) {
    const cols = kind === 'weap' ? buildWeaponCols(state) : buildItemCols(state);
    if (cols.length === 0) return null;
    const cellFor = kind === 'weap' ? weaponCellFor : itemCellFor;

    /** @param {string} label */
    const header = (label) => th(label) + cols.map(c => th(c.header, c.headerTitle)).join('');

    const teamsSorted = [...state.teamOrder.filter(t => t !== '')];
    if (teamsSorted.length === 0 && state.entityCountsByTeam.size > 0) {
        teamsSorted.push(...[...state.entityCountsByTeam.keys()].filter(t => t !== ''));
    }
    const teamRows = teamsSorted.map(team => {
        const teamMap = state.entityCountsByTeam.get(team) || new Map();
        const teamPlayers = state.players.filter(p => p.team === team);
        return {
            colorIdx: state.teamOrder.indexOf(team),
            html: td(escapeHtml(team || '(no team)'))
                + cols.map(c => cellFor(c, true, team, teamMap, teamPlayers, state, mode)).join(''),
        };
    });

    const playersSorted = [...state.players].sort((a, b) => {
        const ta = state.teamOrder.indexOf(a.team || '');
        const tb = state.teamOrder.indexOf(b.team || '');
        if (ta !== tb) return ta - tb;
        return (b.stats?.frags || 0) - (a.stats?.frags || 0);
    });
    const playerRows = playersSorted.map(p => {
        const pmap = state.entityCountsByPlayer.get(p.name) || new Map();
        return {
            colorIdx: state.teamOrder.indexOf(p.team || ''),
            html: td(`${escapeHtml(p.name)} <span class="pickups-team-tag">${escapeHtml(p.team || '')}</span>`)
                + cols.map(c => cellFor(c, false, p.name, pmap, [p], state, mode)).join(''),
        };
    });

    return {
        team: { head: header('Team'), rows: teamRows },
        player: { head: header('Player'), rows: playerRows },
    };
}

/**
 * @param {PickupCol} col
 * @param {boolean} isTeam
 * @param {string} key
 * @param {Map<number, number>} entMap
 * @param {KtxPlayer[]} scopedPlayers
 * @param {PickupsState} state
 * @param {PickupMode} mode
 */
function weaponCellFor(col, isTeam, key, entMap, scopedPlayers, state, mode) {
    const spec = col.kindSpec;
    if (col.type === 'hold') return holdCellFor(col, isTeam, key, state);
    if (col.type === 'entity-count') return entityCountCell(entMap, col.entNum);
    /** @type {(w: WeaponPickup) => boolean} */
    const matches = isTeam
        ? (w => (w.team || '') === key)
        : (w => w.player === key);
    if (col.type === 'weap-pack') {
        const ana = state.weaponPickups.filter(w =>
            w.weapon === spec.kind && matches(w) && w.source === 'backpack'
            && (mode !== 'first' || !w.hadBefore)).length;
        let primary = 0;
        for (const p of scopedPlayers) {
            const pk = p.weapons?.[spec.ktxName]?.pickups || {};
            primary += mode === 'first'
                ? ((pk['taken'] || 0) - (pk['spawn-taken'] || 0))
                : ((pk['total-taken'] || 0) - (pk['spawn-total-taken'] || 0));
        }
        return verifyCell(ana, primary, null, state.hasKtxWeaponCounters);
    }
    // weap-verify (Σ for hasPack, or single combined cell otherwise).
    let ana;
    if (mode === 'first') {
        // first-per-life from weaponPickups. hasPack verifies against KTX
        // `taken` (any source); non-pack weapons verify against
        // `spawn-taken` (world items only), so unknown-source records
        // (weapon-stay synthesis that couldn't tie the grant to a pad)
        // must not inflate the count.
        ana = state.weaponPickups.filter(w =>
            w.weapon === spec.kind && matches(w) && !w.hadBefore
            && (spec.hasPack || w.source === 'world')).length;
    } else {
        ana = 0;
        const list = state.weaponEntsByKind.get(spec.kind) || [];
        for (const it of list) ana += (entMap.get(it.entNum) || 0);
        if (spec.hasPack) {
            ana += state.weaponPickups.filter(w =>
                w.weapon === spec.kind && matches(w) && w.source === 'backpack').length;
        }
    }
    /** @type {Record<string, number> | null} */
    let detail = null;
    let primary = 0;
    for (const p of scopedPlayers) {
        const pk = p.weapons?.[spec.ktxName]?.pickups || {};
        if (spec.hasPack) {
            primary += mode === 'first' ? (pk['taken'] || 0) : (pk['total-taken'] || 0);
        } else {
            primary += mode === 'first' ? (pk['spawn-taken'] || 0) : (pk['spawn-total-taken'] || 0);
        }
        detail = pickupsSumDetail(detail, pickupsWeaponDetail(p, spec.ktxName));
    }
    return verifyCell(ana, primary, detail, state.hasKtxWeaponCounters);
}

/**
 * @param {PickupCol} col
 * @param {boolean} isTeam
 * @param {string} key
 * @param {Map<number, number>} entMap
 * @param {KtxPlayer[]} scopedPlayers
 * @param {PickupsState} state
 */
function itemCellFor(col, isTeam, key, entMap, scopedPlayers, state) {
    const spec = col.kindSpec;
    if (col.type === 'hold') return holdCellFor(col, isTeam, key, state);
    if (col.type === 'entity-count') return entityCountCell(entMap, col.entNum);
    // item-verify: single counter on the KTX side (`took`) — mode has no
    // effect here (KTX doesn't expose a first-per-life counter for items).
    let ana = 0;
    // An item table's only other column (buildItemCols).
    for (const ent of /** @type {ItemVerifyCol} */ (col).entNums) ana += (entMap.get(ent) || 0);
    let primary = 0;
    for (const p of scopedPlayers) primary += (p.items?.[spec.ktxName]?.took || 0);
    return verifyCell(ana, primary, null, state.hasKtxItemCounters);
}

// verifyCell renders the KTX-authoritative counter as the cell
// value when the demo carries one — KTX counted every touch server-side,
// while the analytics reconstruction is known-imperfect in weapon-stay
// modes (see mvd-analytics/analyzer/weapon_pickups.md), so a divergence
// is expected, not an error, and gets no warning styling. The tooltip
// acknowledges the analytics-derived count (still the right source for
// timestamped / per-entity questions). Demos without KTX counters
// (old / non-KTX servers) fall back to the analytics count, trusted
// with its imperfections.
/**
 * @param {number} ana
 * @param {number} ktxPrimary
 * @param {Record<string, number> | null} detail
 * @param {boolean} hasKtx
 */
export function verifyCell(ana, ktxPrimary, detail, hasKtx) {
    const shown = hasKtx ? ktxPrimary : ana;
    if (ana === 0 && ktxPrimary === 0 && (!detail || Object.values(detail).every(v => v === 0))) {
        return '<td><span class="muted">·</span></td>';
    }
    const lines = [];
    if (hasKtx) {
        if (detail) {
            for (const [field, val] of Object.entries(detail)) {
                lines.push(`ktx ${field}: ${val}`);
            }
        } else {
            lines.push(`ktx: ${ktxPrimary}`);
        }
        lines.push(`analytics: ${ana}`);
    } else {
        lines.push(`analytics: ${ana}`, 'no KTX pickup counters in this demo');
    }
    return `<td title="${escapeHtml(lines.join('\n'))}">${shown}</td>`;
}
