// packdrops.js — the Pack Drops tab's DOM-free parts: the drop ↔ pickup
// join, the status of each drop, the filter options and the filter, and
// each row's markup.
//
// The table joins result.backpacks (the drop side from //ktx drop) with the
// backpack-sourced entries in result.weaponPickups (the pickup side from
// //ktx bp) by (backpackEnt, dropTime). A drop with no matching pickup is
// shown as "expired" only when KTX said so in //ktx expire (published as
// the drop row's own `fate`); otherwise it is "unobserved", because a pack
// the recording ended on top of also has no pickup row.
//
// RECONSTRUCTED drops (backpacks[].source === 'reconstructed', demos older
// than the //ktx drop hint) have no `//ktx bp` to join to, so their pickup
// side rides the drop row itself: `fate` / `picker` / `pickerTeam` /
// `pickupTime`, read off the wire's backpack-ENTITY track (schema v72).
// packDropPickup below turns those fields into the same row shape the join
// produces, so one renderer serves both provenances.

import { escapeHtml } from './html.js';
import { formatDuration } from './result.js';
import { hubReplayUrl } from './hub-url.js';

// packDropPickup adapts a reconstructed drop's own fate fields to the pickup
// row shape. `kills` is null rather than 0: kill credit needs the `//ktx bp`
// hint's HadBefore, which no reconstructed row has, and printing 0 would
// claim the picker scored nothing with it.
export function packDropPickup(drop) {
    if (drop.source !== 'reconstructed' || drop.fate !== 'picked' || !drop.picker) return null;
    return {
        player: drop.picker,
        team: drop.pickerTeam || '',
        time: (drop.pickupTime || 0) * 0.001,
        nextDeathTime: 0,
        kills: null,
    };
}

export function packDropStatusFor(drop, pickup) {
    // A RECONSTRUCTED drop's fate comes from the entity track, not from a
    // pickup row. Three outcomes, and `unobserved` is deliberately not
    // `expired`: "we could not see the pickup side" is a different fact from
    // "nobody took it".
    if (drop.source === 'reconstructed') {
        if (drop.fate === 'picked') return { label: 'picked', cls: 'status-picked' };
        if (drop.fate === 'expired') return { label: 'expired', cls: 'status-expired' };
        return { label: 'unobserved', cls: 'status-unobserved' };
    }
    // A KTX row with no pickup row is NOT automatically expired, and this
    // used to say it was. KTX states an expiry outright in `//ktx expire`,
    // which the pipeline stamps as `fate: "expired"`; over the archive that
    // hint accounts for only half the pickup-less drops, the rest being packs
    // the recording ended on top of. Those get `unobserved`.
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
//
// Schema v57: backpacks[].time, weaponPickups[].time / .nextDeathTime /
// .dropTime are int32 ms. The pickup↔drop join must happen in the
// same time space — index against raw ms `dropTime` (an exact int)
// before converting. After the join, convert ms→s so the hub links /
// formatDuration further down (which expect seconds) are unchanged.
export function packDropRows(result) {
    const pickupByKey = {};
    for (const p of (result?.weaponPickups || [])) {
        if (p.source === 'backpack' && p.backpackEnt) {
            pickupByKey[`${p.backpackEnt}@${p.dropTime}`] = p;
        }
    }
    return (result?.backpacks || []).map(rawDrop => {
        const rawPickup = pickupByKey[`${rawDrop.entNum}@${rawDrop.time}`] || null;
        // Convert ms→s on the per-row copy so downstream renderers see seconds.
        const drop = { ...rawDrop, time: rawDrop.time * 0.001 };
        const pickup = rawPickup ? {
            ...rawPickup,
            time: rawPickup.time * 0.001,
            nextDeathTime: (rawPickup.nextDeathTime || 0) * 0.001,
            dropTime: (rawPickup.dropTime || 0) * 0.001,
        } : packDropPickup(rawDrop);
        return { drop, pickup, status: packDropStatusFor(drop, pickup) };
    });
}

export const FILTER_KEYS = ['dropplayer', 'dropteam', 'pickplayer', 'pickteam', 'status'];

// The values each filter select offers (after its "All"), sorted
// case-insensitively.
export function filterOptions(rows) {
    const sets = Object.fromEntries(FILTER_KEYS.map(k => [k, new Set()]));
    for (const r of rows) {
        if (r.drop.player) sets.dropplayer.add(r.drop.player);
        if (r.drop.team) sets.dropteam.add(r.drop.team);
        if (r.pickup?.player) sets.pickplayer.add(r.pickup.player);
        if (r.pickup?.team) sets.pickteam.add(r.pickup.team);
        sets.status.add(r.status.label);
    }
    const cmp = (a, b) => a.localeCompare(b, undefined, { sensitivity: 'base' });
    return Object.fromEntries(FILTER_KEYS.map(k => [k, [...sets[k]].sort(cmp)]));
}

// The filter row narrows rows by dropper, dropper team, picker, picker team
// or status label; '' is "All".
export function filterRows(rows, f) {
    return rows.filter(r =>
        !(f.dropplayer && r.drop.player !== f.dropplayer)
        && !(f.dropteam && r.drop.team !== f.dropteam)
        && !(f.pickplayer && (r.pickup?.player || '') !== f.pickplayer)
        && !(f.pickteam && (r.pickup?.team || '') !== f.pickteam)
        && !(f.status && r.status.label !== f.status));
}

export function countText(shown, total) {
    return shown === total ? `${total} drops` : `${shown} of ${total} drops`;
}

// hubAnchor: a hub replay link for match seconds [from, to] tracking
// `trackName`, or '-' when the demo is not a hub game or the player has no
// userid. `ctx` is { gameId, userIDs, demoOffset } (seconds).
export function hubAnchor(ctx, from, to, trackName) {
    if (!ctx?.gameId) return '-';
    const trackId = ctx.userIDs?.[trackName];
    if (!trackId) return '-';
    const f = Math.max(0, Math.floor(from + ctx.demoOffset));
    const t = Math.floor(to + ctx.demoOffset);
    const url = hubReplayUrl({ gameId: ctx.gameId, from: f, to: t, track: trackId });
    return `<a href="${url}" target="_blank" class="viewer-link">Hub</a>`;
}

// The cells of one row (a <tr>'s innerHTML). The drop link starts 10 s
// before the drop; the run link from 3 s before the pickup to the picker's
// next death (15 s when there is none).
export function rowHtml(r, ctx) {
    const { drop, pickup } = r;
    const dropHub = hubAnchor(ctx, drop.time - 10, drop.time + 2, drop.player);

    let runHub = '-';
    let pickerLabel = '-';
    let pickTeamLabel = '-';
    let killsCell = '-';
    if (pickup) {
        const endTime = pickup.nextDeathTime > 0 ? pickup.nextDeathTime : pickup.time + 15;
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
