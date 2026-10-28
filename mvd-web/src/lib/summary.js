// summary.js — the Summary tab's DOM-free parts: Match Info, Match Settings,
// Server Info, the Teams box, the row markup of the six playerStats tables
// (Basic Stats, Weapon Stats, Item Pickups & Drops — per team and per
// player), Kills by Weapon, and the result.noMatch explanation.
//
// Every table reads result.playerStats and nothing else (README "The Summary
// tab reads playerStats"). Rows are markup strings with a colour index into
// the canonical team order (src/state/teams.js): the tab builds them by hand
// because makeSortable sorts by moving row nodes.

import { escapeHtml } from './html.js';
import { formatDuration } from './result.js';
import { sortByFragsDesc, teamOrder, teamFragTotals, teamRowsInColourOrder, holdCell } from './player-stats.js';
import { HOLD_WEAPON_NAMES, formatAccuracyCell, anyAccOffKtxScale, ACC_OFF_SCALE_FOOTNOTE } from './accuracy.js';
import { formatUtcStamp } from './wall-clock.js';

// ─── Match Info ────────────────────────────────────────────────────────────
//
// demoInfo.duration is verbatim from the KTX JSON (integer seconds,
// untransformed by design); result.match.duration flipped to int32 ms in
// schema v8 — converted to seconds for formatDuration.
// The map cell shows the canonical shortname (dm2, e1m2) — the name QW
// players use. `match.mapTitle` ("Castle of the Damned") goes in the cell's
// tooltip for anyone curious about the level title. Geometry lookups go
// through mapFileKey() and never touch mapTitle.
export function matchInfo(result) {
    const demoInfo = result?.demoInfo;
    const match = result?.match;
    const info = { map: '-', mapTitle: '', duration: '-', mode: '-', hostname: '-' };
    if (demoInfo) {
        info.map = demoInfo.map || match?.map || '-';
        info.mapTitle = match?.mapTitle || '';
        info.duration = formatDuration(demoInfo.duration || ((match?.duration || 0) * 0.001));
        info.mode = demoInfo.mode || '-';
        info.hostname = demoInfo.hostname || '-';
    } else if (match) {
        info.map = match.map || '-';
        info.mapTitle = match.mapTitle || '';
        info.duration = formatDuration((match.duration || 0) * 0.001);
    }
    return info;
}

// ─── Match Settings ────────────────────────────────────────────────────────

// Long-form names for KTX spawn algorithms (k_spw values). Mirrors
// respawn_model_name() in ktx/src/g_utils.c — used as a tooltip on the
// short-form value rendered in the Match Settings panel.
const SPAWN_LONG_NAMES = {
    'QW':  'Normal QW respawns',
    'KTS': 'KT SpawnSafety',
    'KT':  'Kombat Teams respawns',
    'KTX': 'KTX respawns',
    'KT2': 'KTX2 respawns',
};

// matchSettings projects result.metadata.matchSettings onto the panel's
// labelled cells ({ label, value, title }) and modifier pills
// ({ label, title }). Cells with empty / zero values are skipped so duels
// don't show "Teamplay 0" etc. The boolean modifier flags (Dmgfrags,
// NoItems, Midair, …) plus Noweapon and SOCDv2 collapse into a single
// "Modifiers" row of pill-shaped tags below the grid. null when the demo
// has no settings; `shown` is false when they produce nothing at all (an
// entirely empty matchSettings — a parser failure), which hides the panel.
export function matchSettings(settings) {
    if (!settings) return null;
    const cells = [];
    const addCell = (label, value, title) => {
        if (value === undefined || value === null || value === '' || value === 0) return;
        cells.push({ label, value: String(value), title: title || '' });
    };

    addCell('Mode', settings.mode);
    addCell('Deathmatch', settings.deathmatch);
    addCell('Teamplay', settings.teamplay);
    if (settings.timelimit) addCell('Timelimit', `${settings.timelimit} min`);
    if (settings.fraglimit) addCell('Fraglimit', settings.fraglimit);
    if (settings.spawnmodel) {
        const long = SPAWN_LONG_NAMES[settings.spawnmodel] || 'Unknown spawn algorithm';
        const kSuffix = settings.spawnK !== undefined ? ` (k_spw=${settings.spawnK})` : '';
        addCell('Spawnmodel', settings.spawnmodel, `${long}${kSuffix}`);
    }
    if (settings.antilag !== undefined && settings.antilag !== 0) {
        addCell('Antilag', settings.antilag);
    }
    if (settings.overtime) {
        const ot = settings.overtime === 'sd' ? 'sudden death' : `${settings.overtime} min`;
        addCell('Overtime', ot);
    }
    addCell('Powerups', settings.powerups);
    addCell('Match tag', settings.matchtag);

    // Modifier pills — boolean flags + special string flags.
    const modifiers = [];
    const addPill = (label, on, title) => {
        if (!on) return;
        modifiers.push({ label, title: title || '' });
    };
    addPill('Dmgfrags', settings.dmgfrags, 'Damage counts as frags (LGC scoring)');
    addPill('NoItems',  settings.noItems,  'k_noitems: items disabled');
    addPill('Midair',   settings.midair,   'k_midair mode');
    addPill('Instagib', settings.instagib, 'k_instagib mode');
    addPill('Yawnmode', settings.yawnmode, 'k_yawnmode');
    addPill('Airstep',  settings.airstep,  'pm_airstep: stair stepping in air');
    addPill('VWep',     settings.vwep,     'Visible weapon models');
    if (settings.noweapon) addPill(`Noweapon: ${settings.noweapon}`, true, 'Disabled weapons');
    if (settings.socdv2) addPill(`SOCDv2: ${settings.socdv2}`, true, 'SOCD-cleaning mode');

    return { cells, modifiers, shown: cells.length > 0 || modifiers.length > 0 };
}

// ─── Server Info ───────────────────────────────────────────────────────────

// serverInfoRows renders result.metadata.serverInfo as the 2-column
// key/value rows of the collapsed Server Info table, or null when there is
// none (the panel hides). Star keys (server system metadata like *version,
// *admin) sort below regular gameplay cvars. Special-cases the `epoch` key
// to show a human-readable date alongside the raw unix timestamp.
export function serverInfoRows(serverInfo) {
    if (!serverInfo || Object.keys(serverInfo).length === 0) return null;
    const keys = Object.keys(serverInfo).filter(k => serverInfo[k] !== '' && serverInfo[k] !== undefined);
    keys.sort((a, b) => {
        const aStar = a.startsWith('*');
        const bStar = b.startsWith('*');
        if (aStar !== bStar) return aStar ? 1 : -1; // star keys go to the bottom
        return a.localeCompare(b);
    });
    return keys.map(k => {
        const v = serverInfo[k];
        let displayValue = v;
        if (k === 'epoch') {
            const ts = parseInt(v, 10);
            if (!isNaN(ts)) {
                const dt = new Date(ts * 1000).toISOString().replace('T', ' ').replace(/\.\d+Z$/, ' UTC');
                displayValue = `${v} (${dt})`;
            }
        }
        return `<td><code>${escapeHtml(k)}</code></td><td>${escapeHtml(displayValue)}</td>`;
    });
}

// ─── Teams box ─────────────────────────────────────────────────────────────

// teamsSummary: the Teams box's [{ name, frags }] in the canonical order
// (`canonical`, the teams store's order). Rows come from playerStats where
// it exists so the scores match the scoreboard below — its score.frags is
// the corrected net score, where KTX's stats.frags resets after a
// reconnect; demoInfo players, then match.teams, are the fallbacks.
export function teamsSummary(result, canonical) {
    const rows = result?.playerStats?.players || [];
    if (rows.length) return teamsFromPlayers(rows, canonical);
    if (result?.demoInfo?.teams) return teamsFromPlayers(result.demoInfo.players, canonical);
    if (result?.match?.teams) return teamsFromMatch(result.match.teams, canonical);
    return [];
}

// Team scores summed off the players (either row shape — teamFragTotals
// reads frags off both); teamless players bucket as 'unknown' so they
// render with a visible label in this panel. The canonical order keeps the
// colours consistent; any team it does not name follows. Without one, by
// score.
function teamsFromPlayers(rows, canonical) {
    const teamScores = teamFragTotals(rows, 'unknown');
    let ordered;
    if (canonical && canonical.length >= 2) {
        ordered = canonical.map(t => [t, teamScores[t] || 0]);
        for (const [t, s] of Object.entries(teamScores)) {
            if (!canonical.includes(t)) ordered.push([t, s]);
        }
    } else {
        ordered = Object.entries(teamScores).sort((a, b) => b[1] - a[1]);
    }
    return ordered.map(([name, frags]) => ({ name, frags }));
}

function teamsFromMatch(teams, canonical) {
    let sorted;
    if (canonical && canonical.length >= 2) {
        const orderMap = {};
        canonical.forEach((t, i) => { orderMap[t] = i; });
        sorted = [...teams].sort((a, b) => (orderMap[a.name] ?? 999) - (orderMap[b.name] ?? 999));
    } else {
        sorted = [...teams].sort((a, b) => b.frags - a.frags);
    }
    return sorted.map(t => ({ name: t.name, frags: t.frags }));
}

// ─── Cells shared by the tables ────────────────────────────────────────────

// The kill side of a score row — kills, suicides, teamKills, efficiency, the
// by-weapon split and the two spree maxima (schema v74) — is ABSENT (not
// zero) on a demo whose obituary log yielded nothing while deaths were still
// counted from the death stream:
// old kmod/qwe servers print obituaries in a shape the frag log never
// matches. Deaths and frags are measured there and stay; printing 0 for the
// rest made a 230-frag team read as one that never killed anybody. Zero is
// still zero when the log WAS read, so these test for absence, not falsity.
export function effPct(score) {
    // efficiency is a RATIO in [0,1] in the schema, shown as a percent.
    return score.efficiency == null ? '-' : `${(score.efficiency * 100).toFixed(1)}%`;
}

// Enemy RLs / LGs killed, off `score.byEnemyWeapon` (schema v69): the
// player's enemy kills split by what the VICTIM was holding when they died.
// The complement of score.byWeapon, which counts the weapon the KILLER used
// — kills made WITH each weapon are the Weapon Stats tab's K columns, which
// is why they no longer take a slot here.
//
// The buckets are EXCLUSIVE (sg | mid | lg | rl | both) and partition kills,
// so "enemies killed while holding an RL" is rl + both, not rl. Reading the
// rl key alone would silently drop every fully-armed victim — on a duel that
// is routinely a quarter of them.
//
// Measuredness rides with the rest of the kill family: the map is present
// exactly when score.kills is, so branch on kills and render '-' when the
// demo's frag log measured nothing, matching every other kill-side cell.
export function enemyWeaponKillCell(score, weapon) {
    if (score.kills == null) return '-';
    const m = score.byEnemyWeapon || {};
    return (m[weapon] || 0) + (m.both || 0);
}

// ─── Basic Stats ───────────────────────────────────────────────────────────

// basicStatsPlayerRows: the per-player scoreboard. Every column comes from
// the playerStats row. `score` is always derived (KTX's own stats
// over-count pentagram-deflect telefrags and reset after a reconnect);
// `damage` is KTX's where the block carries it. The `Taken` column is
// `damage.taken`, our ALL-SOURCES figure — NOT KTX's enemy-only
// `damage.takenEnemy`, which is a different quantity and has no column here
// (the `<th title>` says so; add a column rather than conflating the two if
// it is ever wanted). `ToDie` does derive from `takenEnemy`, per-player
// only. See RESULT_SCHEMA.md.
//
// Returns { showHandicap, rows: [{ html, colorIdx }] }, frag-sorted.
export function basicStatsPlayerRows(rows, canonical) {
    const sorted = sortByFragsDesc(rows);
    const order = teamOrder(canonical, sorted);

    // Show the handicap column only when at least one player on this demo
    // has a non-default handicap. KTX omits the JSON field entirely when the
    // value is 100 (the default), so any non-zero value here means it was
    // actually set.
    const showHandicap = sorted.some(p => (p.handicap || 0) > 0 && p.handicap !== 100);

    return {
        showHandicap,
        rows: sorted.map(player => {
            const s = player.score || {};
            const d = player.damage;
            // Bot badge: render the skill level inline when present, since
            // bots in a match are rare enough that seeing "BOT 10" at a
            // glance is more useful than hiding it behind a hover. Fall back
            // to a plain "BOT" when the demoinfo JSON didn't include a skill
            // value.
            let botBadge = '';
            if (player.bot) {
                const skill = player.bot.skill;
                const label = skill !== undefined && skill !== null ? `BOT ${skill}` : 'BOT';
                const tooltip = `Frogbot${skill !== undefined ? ', skill ' + skill : ''}${player.bot.customised ? ' (customised)' : ''}`;
                botBadge = ` <span class="bot-badge" title="${tooltip}">${label}</span>`;
            }
            const handicapCell = `<td class="handicap-col"${showHandicap ? '' : ' style="display: none;"'}>${player.handicap || '-'}</td>`;
            const html = `
            <td>${escapeHtml(player.name)}${botBadge}</td>
            <td class="team-col">${escapeHtml(player.team || '')}</td>
            ${handicapCell}
            <td>${s.frags || 0}</td>
            <td>${effPct(s)}</td>
            <td>${s.kills ?? '-'}</td>
            <td>${enemyWeaponKillCell(s, 'rl')}</td>
            <td>${enemyWeaponKillCell(s, 'lg')}</td>
            <td>${s.deaths || 0}</td>
            <td class="team-col">${s.teamKills ?? '-'}</td>
            <td>${s.suicides ?? '-'}</td>
            <td>${s.maxSpree ?? '-'}</td>
            <td>${s.maxQuadSpree ?? '-'}</td>
            <td>${d?.given ?? '-'}</td>
            <td class="team-col">${d?.givenTeam ?? '-'}</td>
            <td>${d?.taken ?? '-'}</td>
            <td>${d?.enemyWeapons ?? '-'}</td>
            <td>${d?.takenToDie ?? '-'}</td>
            <td>${player.ping ?? '-'}</td>
        `;
            return { html, colorIdx: order.indexOf(player.team || '') };
        }),
    };
}

// The per-team rows, in colour order; a team's colour index is its row
// index (teamRowsInColourOrder puts the canonical teams first, in order).
export function basicStatsTeamRows(teamRows, canonical) {
    return teamRowsInColourOrder(teamRows, canonical).map((team, idx) => {
        const s = team.score || {};
        const d = team.damage;
        const html = `
            <td>${escapeHtml(team.name)}</td>
            <td>${s.frags || 0}</td>
            <td>${effPct(s)}</td>
            <td>${s.kills ?? '-'}</td>
            <td>${enemyWeaponKillCell(s, 'rl')}</td>
            <td>${enemyWeaponKillCell(s, 'lg')}</td>
            <td>${s.deaths || 0}</td>
            <td>${s.teamKills ?? '-'}</td>
            <td>${s.suicides ?? '-'}</td>
            <td>${s.maxSpree ?? '-'}</td>
            <td>${s.maxQuadSpree ?? '-'}</td>
            <td>${d?.given ?? '-'}</td>
            <td>${d?.givenTeam ?? '-'}</td>
            <td>${d?.taken ?? '-'}</td>
            <td>${d?.enemyWeapons ?? '-'}</td>
            <td>-</td>
            <td>${team.members || 0}</td>
        `;
        return { html, colorIdx: idx };
    });
}

// ─── Weapon Stats ──────────────────────────────────────────────────────────

// One weapon's four cells for a playerStats row: accuracy from the
// `accuracy` family, kills from `score.byWeapon` (the corrected frag log),
// damage from `damage.byWeapon`, and eK from `score.byWeaponVsEnemyWeapon`.
// A weapon the player never touched shows '-' in all four rather than zeros.
function formatWeaponCells(player, wn) {
    const acc = formatAccuracyCell(player.accuracy, wn);
    const kills = player.score?.byWeapon?.[wn] || 0;
    const dmg = player.damage?.byWeapon?.[wn] || 0;
    return `<td>${acc}</td><td>${kills || '-'}</td><td>${dmg || '-'}</td>${enemyKillCell(player, wn)}`;
}

// The eK cell: of this weapon's kills, how many landed on an enemy who was
// carrying an RL and/or LG. That is the `rl` + `lg` + `both` slice of this
// weapon's row in score.byWeaponVsEnemyWeapon — the cross-tab of killer
// weapon against what the VICTIM held (schema v69).
//
// It is what separates a weapon that wins fights from one that finishes off
// the disarmed: a shotgun with 16 kills of which 12 were against players
// holding nothing is a very different figure from an RL with 13 of which 9
// were against armed opponents.
//
// The column shows only the "armed" total to keep the table narrow; the
// FULL six-bucket breakdown rides in the cell's tooltip, so no part of the
// cross-tab is hidden from a reader who wants it. Measuredness follows the
// kill family, exactly like the K column beside it.
export function enemyKillCell(player, wn) {
    if (player.score?.kills == null) return '<td>-</td>';
    const b = player.score?.byWeaponVsEnemyWeapon?.[wn] || null;
    if (!b) return '<td>-</td>';
    const armed = (b.rl || 0) + (b.lg || 0) + (b.both || 0);
    // Ordered strongest-first so the tooltip reads as a loadout ladder.
    const parts = [
        ['RL+LG', b.both], ['RL', b.rl], ['LG', b.lg],
        ['mid', b.mid], ['sg only', b.sg], ['unknown', b.unknown],
    ].filter(([, n]) => n).map(([k, n]) => `${k}: ${n}`);
    const title = `Victim was holding — ${parts.join(' · ')}`;
    // `armed` prints even at 0. Reaching here means the player DID kill with
    // this weapon and the classification ran, so 0 is a measurement — "every
    // one of those kills was on someone carrying nothing", which is the most
    // interesting reading this column has. Only an unmeasured cell gets '-',
    // per the tables' absent-is-never-zero rule.
    return `<td title="${escapeHtml(title)}">${armed}</td>`;
}

function weaponCellsRow(row) {
    let cells = `<td>${escapeHtml(row.name)}</td>`;
    HOLD_WEAPON_NAMES.forEach(wn => { cells += formatWeaponCells(row, wn); });
    return cells;
}

// Damage-first ordering, because this table is about output. On a demo
// where no row has a damage family the comparator is a constant, which
// left the rows in stream order — a different, teamwise-jumbled order
// from every other Summary table. Fall back to the frag sort they use.
export function weaponStatsPlayerRows(rows, canonical) {
    const hasDamage = rows.some(r => r.damage);
    const sorted = hasDamage
        ? [...rows].sort((a, b) => (b.damage?.given || 0) - (a.damage?.given || 0))
        : sortByFragsDesc(rows);
    const order = teamOrder(canonical, sorted);
    return sorted.map(p => ({ html: weaponCellsRow(p), colorIdx: order.indexOf(p.team || '') }));
}

export function weaponStatsTeamRows(teamRows, canonical) {
    return teamRowsInColourOrder(teamRows, canonical).map((t, idx) => ({ html: weaponCellsRow(t), colorIdx: idx }));
}

// The Acc marker's footnote under Weapon Stats, or '' when no rendered cell
// wears the mark — so it never explains something that is not there.
export function accConventionNote(rows, teamRows) {
    return (anyAccOffKtxScale(rows) || anyAccOffKtxScale(teamRows)) ? ACC_OFF_SCALE_FOOTNOTE : '';
}

// ─── Item Pickups & Drops ──────────────────────────────────────────────────

// The pickup/possession cells of one Item Pickups row — identical for the
// per-player and per-team tables, which differ only in their first column.
// Counts come from the pickup tally, seconds from OUR hold integral rather
// than KTX's item.time. MH has no possession column by design: mega health
// is consumed on pickup, so playerStats carries no hold stat for it.
export function itemCells(row) {
    const k = row.pickups?.byKind || {};
    const h = row.hold || {};
    const hw = h.weapons || {};
    const ha = h.armor || {};
    const hp = h.powerups || {};
    const w = row.window;
    return `
        <td class="group-start">${k.ra?.took || 0}</td>
        ${holdCell(ha.ra, w)}
        <td class="group-start">${k.ya?.took || 0}</td>
        ${holdCell(ha.ya, w)}
        <td class="group-start">${k.ga?.took || 0}</td>
        ${holdCell(ha.ga, w)}
        <td class="group-start">${k.mh?.took || 0}</td>
        <td class="group-start">${k.quad?.took || 0}</td>
        ${holdCell(hp.quad, w)}
        <td class="group-start">${k.pent?.took || 0}</td>
        ${holdCell(hp.pent, w)}
        <td class="group-start">${k.ring?.took || 0}</td>
        ${holdCell(hp.ring, w)}
        <td class="group-start">${k.rl?.took || 0}</td>
        ${holdCell(hw.rl, w)}
        <td>${k.rl?.dropped || 0}</td>
        <td>${formatXfer(k.rl)}</td>
        <td class="group-start">${k.lg?.took || 0}</td>
        ${holdCell(hw.lg, w)}
        <td>${k.lg?.dropped || 0}</td>
        <td>${formatXfer(k.lg)}</td>
    `;
}

// Pack transfers. `xfer` (a teammate took your pack) and `xferSelf` (you
// took it back yourself) sum to KTX's single xferRL/xferLG figure, which
// conflates them; the tooltip shows the split. ABSENT — rendered '-' —
// when the demo carries no //ktx bp hints, since a 0 there would claim
// nobody ever recovered a pack.
export function formatXfer(entry) {
    if (!entry || entry.xfer == null) return '-';
    const self = entry.xferSelf || 0;
    const total = entry.xfer + self;
    if (!self) return `${total}`;
    return `<span title="${entry.xfer} to a teammate, ${self} self-recovered">${total}</span>`;
}

export function itemsPlayerRows(rows, canonical) {
    const sorted = sortByFragsDesc(rows);
    const order = teamOrder(canonical, sorted);
    return sorted.map(p => ({ html: `<td>${escapeHtml(p.name)}</td>${itemCells(p)}`, colorIdx: order.indexOf(p.team || '') }));
}

export function itemsTeamRows(teamRows, canonical) {
    return teamRowsInColourOrder(teamRows, canonical).map((t, idx) => ({ html: `<td>${escapeHtml(t.name)}</td>${itemCells(t)}`, colorIdx: idx }));
}

// ─── Kills by Weapon ───────────────────────────────────────────────────────

const WEAPON_NAMES = {
    'rl': 'Rocket Launcher',
    'lg': 'Lightning Gun',
    'gl': 'Grenade Launcher',
    'ssg': 'Super Shotgun',
    'sg': 'Shotgun',
    'sng': 'Super Nailgun',
    'ng': 'Nailgun',
    'axe': 'Axe',
    'tele': 'Telefrag',
    'suicide': 'Suicide',
    'teamkill': 'Team Kill',
    'fall': 'Fall',
    'water': 'Drowning',
    'lava': 'Lava',
    'slime': 'Slime'
};

// A frag-log weapon code's display name (also the Aim tab's table titles).
export function getWeaponName(code) {
    return WEAPON_NAMES[code] || code;
}

// The bars of Kills by Weapon from result.frags.byWeapon, most kills first,
// each as a percentage of the largest; null when the demo has no frag
// section (the panel stays empty).
export function weaponKillBars(byWeapon) {
    if (!byWeapon) return null;
    const sorted = Object.entries(byWeapon).sort((a, b) => b[1] - a[1]);
    const max = sorted.length > 0 ? sorted[0][1] : 1;
    return sorted.map(([weapon, count]) => ({ name: getWeaponName(weapon), count, pct: (count / max) * 100 }));
}

// ─── result.noMatch (schema v74) ───────────────────────────────────────────
//
// A recording that holds no analyzable match used to arrive here as a result
// with no `streams` and an EMPTY `errors[]`, and the UI showed the same thing
// it shows for a race demo: tables with headers and nothing under them. The
// marker is present exactly when `streams` is absent, and its five reasons
// are a total partition, so this switch is exhaustive by contract.
//
// Read it BEFORE `errors[]`: it decides whether a reader error describes a
// partial match or nothing at all, and `demoUnreadable` is the one reason
// that means both — which is why that reason (and only that one) also prints
// the reader's own message.
const NO_MATCH_TITLES = {
    demoUnreadable: 'This recording could not be read to the end',
    midMatchRecording: 'This recording starts mid-match',
    matchStartUnannounced: 'No analyzable match came out of this recording',
    noMatchDeclared: 'No match was declared in this recording',
    noPlayRecorded: 'This recording holds no match',
};

// `detail` states the finding in the pipeline's vocabulary (`status` keys,
// match-start announcements). These say what it MEANS for the person holding
// the file, and name an action where one honestly exists — which is only for
// `demoUnreadable`: of the 20 such demos in the archive sweep, 16 abort
// within the first 16 bytes on a "block size" that is plainly ASCII text
// (`us\0C`, `rdem`), 2 on a `dem_cmd` no MVD carries and 2 on an unexpected
// EOF — so the file is either not an MVD or a truncated one, and a fresh copy
// is a real fix. Nothing the reader can do makes a recording that started
// mid-match contain its own start, so the other four translate and stop.
//
// The pipeline's own detail strings are untouched — they are the finding, and
// display-only by contract either way.
const NO_MATCH_ACTIONS = {
    demoUnreadable: 'The file may be truncated, or not an MVD at all — try downloading it again.',
    midMatchRecording: 'Nothing is wrong with the file; it simply began after the match did. Per-match stats need a recording that covers the match start.',
    matchStartUnannounced: 'The server started a match here, but nothing this tool could measure came out of the window it opened — usually a server mod whose match state we do not model.',
    noMatchDeclared: 'People were playing, but no managed match was declared — there is no start, no end and no scoreboard to measure between.',
    noPlayRecorded: 'There is nothing to measure here: no match declared and no kills in the frag log. Usually an idle server or an aborted recording.',
};

// The gamedir reading, which rides the demo rather than the reason: 165 of
// the 170 `noMatchDeclared` demos in the sweep are a foreign gamedir
// (`fortress` alone is 148), and the same mods turn up under three of the
// other reasons. "qw" is the stock deathmatch dir and says nothing.
function noMatchGameDirNote(nm) {
    if (!nm.gameDir || nm.gameDir === 'qw') return '';
    return `This looks like a ${nm.gameDir} demo; this tool analyzes KTX-style QuakeWorld matches.`;
}

// The evidence beside the prose, as [label, value, title?]. Every fact
// `detail` states is a structured field, and these are those fields —
// `detail` is display-only by contract (unstable wording, never parsed), so
// it is rendered verbatim and nothing is read back out of it.
function noMatchEvidence(nm) {
    const items = [];
    // Absent when the opening fullserverinfo dump carried no `status` key at
    // all, which is a different reading from any value it could have had.
    items.push(['Status at demo open', nm.statusAtOpen || 'not sent']);
    items.push(['Match clock ever seen running', nm.statusRunningSeen ? 'yes' : 'no']);
    if (nm.gameDir) items.push(['Game dir', nm.gameDir]);
    // 0 is the reading that defines noPlayRecorded, so it always prints.
    items.push(['Kills in the frag log', String(nm.kills || 0)]);
    // The date markers ride here rather than on streams.global, which this
    // result does not have. A finalscores stamp whose year could not be
    // completed carries unixMs 0 and is skipped — it names no instant.
    const marker = (nm.dateMarkers || []).find(m => m.unixMs);
    if (marker) items.push(['Date marker on the wire', `${formatUtcStamp(marker.unixMs)} (${marker.source})`, marker.raw]);
    return items;
}

// noMatchView: what the no-match panel at the top of the tab says, or null
// when the demo holds a match. `reading` is the plain reading under the
// finding — the reason's translation, then the gamedir's where the demo has
// one to give ('' hides the line); `errors` the reader's message, ''
// unless it is the evidence.
export function noMatchView(result) {
    const nm = result?.noMatch;
    if (!nm) return null;
    const reading = [NO_MATCH_ACTIONS[nm.reason] || '', noMatchGameDirNote(nm)].filter(Boolean).join(' ');
    // errors[] means the pipeline failed at something; the marker means the
    // demo holds no match. They coincide on exactly one reason, and there the
    // reader's own message is the evidence.
    const errs = result.errors || [];
    const errors = nm.reason === 'demoUnreadable' && errs.length > 0 ? `Reader: ${errs.join(' · ')}` : '';
    return {
        title: NO_MATCH_TITLES[nm.reason] || 'This recording holds no analyzable match',
        detail: nm.detail || '',
        reading,
        evidence: noMatchEvidence(nm),
        errors,
    };
}

// The Teams box is fed from the frag log, which some match-less recordings
// still carry (a mid-match recording has a scoreline) and others do not —
// hidden only in the second case, since the .no-match CSS cannot tell them
// apart. Otherwise the panel is left to the duel-mode / no-team-rows /
// individual-mode rules.
export function teamsPanelHidden(result) {
    return !!result?.noMatch && !(result.match?.teams || []).length;
}
