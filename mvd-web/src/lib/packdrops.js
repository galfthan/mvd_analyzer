// packdrops.js — the Pack Drops tab's DOM-free parts: the status of each
// drop, the filter options and the filter, and each row's markup.
//
// Every result.backpacks row carries its own pickup side (schema v77):
// `fate` with `picker` / `pickerTeam` / `pickupTime`. On a `ktx` row that is
// the //ktx bp pickup the pipeline joined onto the drop, with the pickup's
// `pickerHadWeapon` / `pickerKills` / `pickerNextDeathTime`; on a
// RECONSTRUCTED row (demos older than the //ktx drop hint) it is read off
// the wire's backpack-ENTITY track, which cannot see those three. A drop
// nobody is known to have taken is "expired" only when KTX said so in
// //ktx expire, otherwise "unobserved", because a pack the recording ended
// on top of has no pickup either.

import { escapeHtml } from './html.js';
import { formatDuration } from './result.js';
import { hubReplayUrl, hubLinkHtml } from './hub-url.js';

/**
 * A result.backpacks row (Result JSON); `time` is ms there and seconds on
 * the copy a PackDropRow carries.
 * @typedef {{ time: number, weapon: string, player?: string, team?: string, entNum?: number, source?: string, fate?: string, picker?: string, pickerTeam?: string, pickerRelation?: string, pickupTime?: number, pickerHadWeapon?: boolean, pickerKills?: number, pickerNextDeathTime?: number, [field: string]: any }} PackDrop
 * The pickup side of a drop, times in seconds.
 * @typedef {{ player?: string, team?: string, time: number, nextDeathTime: number, kills: number | null, hadBefore?: boolean }} PackPickup
 * @typedef {{ label: string, cls: string }} PackDropStatus
 * @typedef {{ drop: PackDrop, pickup: PackPickup | null, status: PackDropStatus }} PackDropRow
 * @typedef {'dropplayer' | 'dropteam' | 'pickplayer' | 'pickteam' | 'status'} FilterKey
 * @typedef {Partial<Record<FilterKey, string>>} PackDropFilter
 * Hub-link context: the game id, name → userid map and demo offset (s).
 * @typedef {{ gameId?: number | null, userIDs?: Record<string, number>, demoOffset: number, matchEnd?: number }} HubLinkCtx
 *   matchEnd: the match's length, seconds
 */

// packDropPickup reads a drop's pickup side (ms on the row) into seconds.
// `kills` is null rather than 0 when the row has none: kill credit needs
// the //ktx bp hint's HadBefore, which no reconstructed row has, and
// printing 0 would claim the picker scored nothing with it.
/**
 * @param {PackDrop} drop  a Result row (times in ms)
 * @returns {PackPickup | null}
 */
export function packDropPickup(drop) {
    if (drop.fate !== 'picked' || !drop.picker) return null;
    return {
        player: drop.picker,
        team: drop.pickerTeam || '',
        time: (drop.pickupTime || 0) * 0.001,
        nextDeathTime: (drop.pickerNextDeathTime || 0) * 0.001,
        kills: drop.pickerKills ?? null,
        hadBefore: drop.pickerHadWeapon,
    };
}

/**
 * @param {PackDrop} drop
 * @param {PackPickup | null} pickup
 * @returns {PackDropStatus}
 */
export function packDropStatusFor(drop, pickup) {
    // A RECONSTRUCTED drop's fate comes from the entity track, not from a
    // pickup row. Three outcomes, and `unobserved` is deliberately not
    // `expired`: "we could not see the pickup side" is a different fact from
    // "nobody took it".
    // The dropper respawned and took their own pack back — neither a
    // transfer to a teammate nor a loss to the enemy.
    if (drop.fate === 'picked' && drop.pickerRelation === 'self') return { label: 'self', cls: 'status-self' };
    if (drop.source === 'reconstructed') {
        if (drop.fate === 'picked') return { label: 'picked', cls: 'status-picked' };
        if (drop.fate === 'expired') return { label: 'expired', cls: 'status-expired' };
        return { label: 'unobserved', cls: 'status-unobserved' };
    }
    // A KTX row with no pickup is NOT automatically expired: KTX states an
    // expiry outright in `//ktx expire` (`fate: "expired"`); over the archive
    // that hint accounts for only half the pickup-less drops, the rest being
    // packs the recording ended on top of. Those are `unobserved`.
    if (!pickup) {
        if (drop.fate === 'expired') return { label: 'expired', cls: 'status-expired' };
        return { label: 'unobserved', cls: 'status-unobserved' };
    }
    const sameTeam = pickup.team && drop.team && pickup.team === drop.team;
    const weaponUpper = drop.weapon.toUpperCase();
    if (sameTeam) {
        if (pickup.hadBefore) return { label: `xfer ${weaponUpper}`, cls: 'status-xfer-had' };
        return { label: 'xfer', cls: 'status-xfer' };
    }
    if (pickup.hadBefore) return { label: `enemy ${weaponUpper}`, cls: 'status-enemy-had' };
    return { label: 'enemy', cls: 'status-enemy' };
}

// packDropRows: one { drop, pickup, status } per backpack, in the
// section's order, times in seconds.
/**
 * @param {import('./result.js').Result | null | undefined} result
 * @returns {PackDropRow[]}
 */
export function packDropRows(result) {
    return (result?.backpacks || []).map((/** @type {PackDrop} */ raw) => {
        const pickup = packDropPickup(raw);
        const drop = { ...raw, time: raw.time * 0.001 };
        return { drop, pickup, status: packDropStatusFor(drop, pickup) };
    });
}

/** @type {FilterKey[]} */
export const FILTER_KEYS = ['dropplayer', 'dropteam', 'pickplayer', 'pickteam', 'status'];

// The values each filter select offers (after its "All"), sorted
// case-insensitively.
/**
 * @param {PackDropRow[]} rows
 * @returns {Record<FilterKey, string[]>}
 */
export function filterOptions(rows) {
    const sets = /** @type {Record<FilterKey, Set<string>>} */ (Object.fromEntries(FILTER_KEYS.map(k => [k, new Set()])));
    for (const r of rows) {
        if (r.drop.player) sets.dropplayer.add(r.drop.player);
        if (r.drop.team) sets.dropteam.add(r.drop.team);
        if (r.pickup?.player) sets.pickplayer.add(r.pickup.player);
        if (r.pickup?.team) sets.pickteam.add(r.pickup.team);
        sets.status.add(r.status.label);
    }
    /** @type {(a: string, b: string) => number} */
    const cmp = (a, b) => a.localeCompare(b, undefined, { sensitivity: 'base' });
    return /** @type {Record<FilterKey, string[]>} */ (Object.fromEntries(FILTER_KEYS.map(k => [k, [...sets[k]].sort(cmp)])));
}

// The filter row narrows rows by dropper, dropper team, picker, picker team
// or status label; '' is "All".
/**
 * @param {PackDropRow[]} rows
 * @param {PackDropFilter} f
 * @returns {PackDropRow[]}
 */
export function filterRows(rows, f) {
    return rows.filter(r =>
        !(f.dropplayer && r.drop.player !== f.dropplayer)
        && !(f.dropteam && r.drop.team !== f.dropteam)
        && !(f.pickplayer && (r.pickup?.player || '') !== f.pickplayer)
        && !(f.pickteam && (r.pickup?.team || '') !== f.pickteam)
        && !(f.status && r.status.label !== f.status));
}

/**
 * @param {number} shown
 * @param {number} total
 */
export function countText(shown, total) {
    return shown === total ? `${total} drops` : `${shown} of ${total} drops`;
}

// hubAnchor: a hub replay link for match seconds [from, to] tracking
// `trackName`, or '-' when the demo is not a hub game or the player has no
// userid. `ctx` is { gameId, userIDs, demoOffset } (seconds).
/**
 * @param {HubLinkCtx | null | undefined} ctx
 * @param {number} from
 * @param {number} to
 * @param {string | undefined} trackName
 */
export function hubAnchor(ctx, from, to, trackName) {
    if (!ctx?.gameId) return '-';
    const trackId = ctx.userIDs?.[/** @type {string} */ (trackName)];
    if (!trackId) return '-';
    const f = Math.max(0, Math.floor(from + ctx.demoOffset));
    const t = Math.floor(to + ctx.demoOffset);
    const url = hubReplayUrl({ gameId: ctx.gameId, from: f, to: t, track: trackId });
    return hubLinkHtml(url);
}

// The cells of one row (a <tr>'s innerHTML). The drop link starts 4 s
// before the drop; the run link from 3 s before the pickup to the picker's
// next death — the match end when there is none (they were still on the
// run when it ended; 15 s past the pickup without a known match length).
/**
 * @param {PackDropRow} r
 * @param {HubLinkCtx | null | undefined} ctx
 */
export function rowHtml(r, ctx) {
    const { drop, pickup } = r;
    const dropHub = hubAnchor(ctx, drop.time - 4, drop.time + 2, drop.player);

    let runHub = '-';
    let pickerLabel = '-';
    let pickTeamLabel = '-';
    let killsCell = '-';
    if (pickup) {
        const endTime = pickup.nextDeathTime > 0 ? pickup.nextDeathTime : (ctx?.matchEnd || pickup.time + 15);
        runHub = hubAnchor(ctx, pickup.time - 3, endTime, pickup.player);
        pickerLabel = escapeHtml(pickup.player || '?');
        pickTeamLabel = escapeHtml(pickup.team || '-');
        killsCell = pickup.kills === null
            ? '-'
            : (pickup.hadBefore
                ? `<span class="kills-redundant">${pickup.kills}</span>`
                : String(pickup.kills));
    }

    const statusCell = `<span class="pack-status ${r.status.cls}">${escapeHtml(r.status.label)}</span>`;

    return `
            <td class="time-cell time-link">${formatDuration(drop.time)}</td>
            <td>${escapeHtml(drop.player || '?')}</td>
            <td>${escapeHtml(drop.team || '-')}</td>
            <td class="weapon-cell weapon-${drop.weapon}">${drop.weapon.toUpperCase()}</td>
            <td>${dropHub}</td>
            <td>${statusCell}</td>
            <td>${pickerLabel}</td>
            <td>${pickTeamLabel}</td>
            <td class="kills-cell">${killsCell}</td>
            <td>${runHub}</td>
        `;
}
