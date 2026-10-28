<script>
    // The Summary tab: Match Info, Match Settings, Server Info, the Teams
    // box, Basic Stats / Weapon Stats / Item Pickups & Drops (per team and
    // per player, all from result.playerStats), Kills by Weapon, and — on a
    // recording that holds no match — the no-match panel at the top
    // (src/lib/summary.js has the rules and the row markup). src/app/tabs.js
    // toggles the `active` class like on every other .tab-content, and
    // src/app/load.js owns the body classes the CSS hides panels by
    // (duel-mode, individual-mode, no-team-rows, no-match: set in
    // displayResults, which is where the canonical team order is set too).
    //
    // The tab is built — once, on the next frame it shows — after the demo
    // or the teams changed. The table headers are static markup and the
    // rows are built by hand rather than an {#each}, because the shared
    // makeSortable (src/ui/sortable.js) sorts by moving row nodes; the
    // `rows` action fills a <tbody> and paints each row's team stripe from
    // the canonical colours (src/state/teams.js). The panels carry
    // data-panel and sit directly in #tab-summary for src/ui/panels.
    import { onMount } from 'svelte';
    import { demo, teams, activeTab } from '../../state/index.js';
    import { playerStatsRows, playerStatsTeamRows } from '../../lib/player-stats.js';
    import { damageProvenanceHtml, provenanceClass } from '../../lib/provenance.js';
    import { matchDateHtml } from '../../lib/wall-clock.js';
    import {
        matchInfo, matchSettings, serverInfoRows, teamsSummary, teamsPanelHidden, noMatchView,
        basicStatsTeamRows, basicStatsPlayerRows, weaponStatsTeamRows, weaponStatsPlayerRows,
        itemsTeamRows, itemsPlayerRows, accConventionNote, weaponKillBars,
    } from '../../lib/summary.js';
    import { makeSortable } from '../../ui/sortable.js';

    /**
     * @typedef {import('../../lib/summary.js').SummaryRow} SummaryRow
     * A <tbody>'s rows: hand-built rows, or plain markup strings.
     * @typedef {Array<SummaryRow | string>} BodyRows
     * @typedef {object} SummaryView
     * @property {Omit<import('../../lib/summary.js').MatchInfo, 'mapTitle'> & { mapTitle: string | undefined }} info
     * @property {string} date
     * @property {import('../../lib/summary.js').MatchSettings | null} settings
     * @property {string[] | null} serverInfo
     * @property {import('../../lib/summary.js').TeamSummary[]} teamList
     * @property {boolean} teamsHidden
     * @property {import('../../lib/summary.js').NoMatchView | null} noMatch
     * @property {import('../../lib/provenance.js').ProvenanceNote | null} note
     * @property {string} convNote
     * @property {boolean} showHandicap
     * @property {import('../../lib/summary.js').WeaponKillBar[] | null} bars
     * @property {Record<'basicTeam' | 'basicPlayer' | 'weapTeam' | 'weapPlayer' | 'itemTeam' | 'itemPlayer', SummaryRow[]>} tables
     * @property {string[]} colors
     */

    /** @type {SummaryRow[]} */
    const NO_ROWS = [];
    /** @returns {SummaryView} */
    const blank = () => ({
        // mapTitle stays undefined until a demo sets it, so the cell has no
        // tooltip before the first load.
        info: { map: '-', mapTitle: undefined, duration: '-', mode: '-', hostname: '-' },
        date: '-',
        settings: null,
        serverInfo: null,
        teamList: [],
        teamsHidden: false,
        noMatch: null,
        note: null,
        convNote: '',
        // true before the first build: the column shows until a demo says.
        showHandicap: true,
        bars: null,
        tables: {
            basicTeam: NO_ROWS, basicPlayer: NO_ROWS,
            weapTeam: NO_ROWS, weapPlayer: NO_ROWS,
            itemTeam: NO_ROWS, itemPlayer: NO_ROWS,
        },
        colors: [],
    });

    let v = $state(blank());
    let root = $state();

    let dirty = true;
    let queued = false;

    function schedule() {
        if (queued || !dirty || activeTab.get() !== 'summary') return;
        queued = true;
        requestAnimationFrame(() => {
            queued = false;
            if (dirty && activeTab.get() === 'summary') build();
        });
    }

    function invalidate() {
        dirty = true;
        schedule();
    }

    function build() {
        dirty = false;
        const result = demo.get()?.result;
        if (!result) {
            v = blank();
            return;
        }
        const t = teams.get();
        const canonical = t.order;
        // Summary tab, all six tables, from the canonical playerStats
        // section. It is computed for EVERY demo — a missing KTX block
        // degrades families to src:"derived" rather than dropping them — so
        // there is no scoreboard-only fallback path. The one demo class with
        // no section at all is a parse that produced no player streams (a
        // race demo has no match), and that renders as empty tables.
        const rows = playerStatsRows(result);
        const teamRows = playerStatsTeamRows(result);
        const basicPlayer = basicStatsPlayerRows(rows, canonical);
        v = {
            info: matchInfo(result),
            // Date: KTX's own stamp where the demoinfo block exists,
            // otherwise the graded wall-clock anchor (schema v72).
            date: matchDateHtml(result),
            settings: matchSettings(result.metadata?.matchSettings),
            serverInfo: serverInfoRows(result.metadata?.serverInfo),
            teamList: teamsSummary(result, canonical),
            teamsHidden: teamsPanelHidden(result),
            noMatch: noMatchView(result),
            // The damage-evidence banner goes above every Summary table
            // whose figures ride the damage section — the Basic Stats damage
            // columns, and Weapon Stats' Dmg and (on a reconstructed demo)
            // its Acc.
            note: damageProvenanceHtml(result),
            convNote: accConventionNote(rows, teamRows),
            showHandicap: basicPlayer.showHandicap,
            bars: weaponKillBars(result.frags?.byWeapon),
            tables: {
                basicTeam: basicStatsTeamRows(teamRows, canonical),
                basicPlayer: basicPlayer.rows,
                weapTeam: weaponStatsTeamRows(teamRows, canonical),
                weapPlayer: weaponStatsPlayerRows(rows, canonical),
                itemTeam: itemsTeamRows(teamRows, canonical),
                itemPlayer: itemsPlayerRows(rows, canonical),
            },
            colors: t.colors,
        };
        // Binds once per header cell, so a rebuild never double-binds.
        /** @type {NodeListOf<HTMLTableElement>} */ (root.querySelectorAll('table.stats-table')).forEach(makeSortable);
    }

    // Fill a <tbody> with hand-built rows ({ html, colorIdx }, or a plain
    // markup string): a 3px stripe in the row's canonical team colour when
    // it has one.
    /**
     * @param {HTMLTableSectionElement} node
     * @param {{ rows: BodyRows, colors?: string[] }} arg
     */
    function rows(node, arg) {
        /** @param {{ rows: BodyRows, colors?: string[] }} arg */
        const fill = ({ rows: list, colors = [] }) => {
            node.replaceChildren();
            for (const row of list) {
                const tr = document.createElement('tr');
                const r = typeof row === 'string' ? { html: row, colorIdx: -1 } : row;
                if (r.colorIdx >= 0 && r.colorIdx < colors.length) {
                    tr.style.borderLeft = `3px solid ${colors[r.colorIdx]}`;
                }
                tr.innerHTML = r.html;
                node.appendChild(tr);
            }
        };
        fill(arg);
        return { update: fill };
    }

    onMount(() => {
        const offs = [
            demo.subscribe(invalidate),
            teams.subscribe(invalidate),
            activeTab.subscribe(schedule),
        ];
        return () => offs.forEach((off) => off());
    });
</script>

{#snippet weaponHead(/** @type {string} */ first)}
    <thead>
        <tr>
            <th rowspan="2">{first}</th>
            <th colspan="4" title="Shotgun">SG</th>
            <th colspan="4" title="Super Shotgun">SSG</th>
            <th colspan="4" title="Super Nailgun">SNG</th>
            <th colspan="4" title="Grenade Launcher">GL</th>
            <th colspan="4" title="Rocket Launcher">RL</th>
            <th colspan="4" title="Lightning Gun">LG</th>
        </tr>
        <tr>
            {#each [0, 1, 2, 3, 4, 5] as _}
                <th title="Accuracy">Acc</th><th title="Kills">K</th><th title="Damage to enemies">Dmg</th><th title="Of those kills, how many were against an enemy carrying an RL and/or LG. Hover a cell for the full breakdown by what the victim held.">eK</th>
            {/each}
        </tr>
    </thead>
{/snippet}

{#snippet itemsHead(/** @type {string} */ first)}
    <thead>
        <tr>
            <th rowspan="2">{first}</th>
            <th class="col-group group-start" colspan="2" title="Red Armor">RA</th>
            <th class="col-group group-start" colspan="2" title="Yellow Armor">YA</th>
            <th class="col-group group-start" colspan="2" title="Green Armor">GA</th>
            <th class="group-start" rowspan="2" title="Mega Health pickups">MH</th>
            <th class="col-group group-start" colspan="2" title="Quad Damage">Quad</th>
            <th class="col-group group-start" colspan="2" title="Pentagram of Protection">Pent</th>
            <th class="col-group group-start" colspan="2" title="Ring of Shadows">Ring</th>
            <th class="col-group group-start" colspan="4" title="Rocket Launcher">RL</th>
            <th class="col-group group-start" colspan="4" title="Lightning Gun">LG</th>
        </tr>
        <tr>
            <th class="group-start" title="Red Armor pickups">took</th>
            <th title="Seconds wearing red armor">s</th>
            <th class="group-start" title="Yellow Armor pickups">took</th>
            <th title="Seconds wearing yellow armor">s</th>
            <th class="group-start" title="Green Armor pickups">took</th>
            <th title="Seconds wearing green armor">s</th>
            <th class="group-start" title="Quad Damage pickups">took</th>
            <th title="Seconds under quad damage">s</th>
            <th class="group-start" title="Pentagram pickups">took</th>
            <th title="Seconds under pentagram">s</th>
            <th class="group-start" title="Ring of Shadows pickups">took</th>
            <th title="Seconds under ring of shadows">s</th>
            <th class="group-start" title="Rocket Launcher first pickups per life (item or pack)">took</th>
            <th title="Seconds holding a rocket launcher">s</th>
            <th title="Rocket Launcher drops">drop</th>
            <th title="Rocket Launcher transfers to teammates">xfer</th>
            <th class="group-start" title="Lightning Gun first pickups per life (item or pack)">took</th>
            <th title="Seconds holding a lightning gun">s</th>
            <th title="Lightning Gun drops">drop</th>
            <th title="Lightning Gun transfers to teammates">xfer</th>
        </tr>
    </thead>
{/snippet}

<div id="tab-summary" class="tab-content" bind:this={root}>
    <div class="empty-state">Load or search a demo to begin.</div>

    <!-- result.noMatch (schema v74): this recording holds no analyzable
         match. The .no-match body class (src/app/load.js) hides the stream-fed
         tables below, which have nothing in them on such a demo. -->
    <div id="no-match-panel" class="panel no-match-panel" style:display={v.noMatch ? null : 'none'}>
        <h3 id="no-match-title">{v.noMatch?.title ?? ''}</h3>
        <p id="no-match-detail" class="no-match-detail">{v.noMatch?.detail ?? ''}</p>
        <p id="no-match-reading" class="no-match-reading" style:display={v.noMatch?.reading ? null : 'none'}>{v.noMatch?.reading ?? ''}</p>
        <!-- svelte-ignore a11y_label_has_associated_control: a caption styled as a label, as in the legacy markup -->
        <div class="summary-grid" id="no-match-evidence">
            {#each v.noMatch?.evidence ?? [] as [label, value, title]}
                <div class="summary-item"><label>{label}</label><span title={title || undefined}>{value}</span></div>
            {/each}
        </div>
        <p id="no-match-errors" class="no-match-errors" style:display={v.noMatch?.errors ? null : 'none'}>{v.noMatch?.errors ?? ''}</p>
    </div>

    <div data-panel="match-info" id="match-summary" class="panel">
        <h3>Match Info</h3>
        <!-- svelte-ignore a11y_label_has_associated_control: a caption styled as a label, as in the legacy markup -->
        <div class="summary-grid" id="match-info-grid">
            <div class="summary-item">
                <label>Map</label>
                <span id="map-name" title={v.info.mapTitle}>{v.info.map}</span>
            </div>
            <div class="summary-item">
                <label>Duration</label>
                <span id="duration">{v.info.duration}</span>
            </div>
            <div class="summary-item">
                <label>Mode</label>
                <span id="mode">{v.info.mode}</span>
            </div>
            <div class="summary-item">
                <label>Server</label>
                <span id="hostname">{v.info.hostname}</span>
            </div>
            <div class="summary-item">
                <label>Date</label>
                <span id="match-date">{@html v.date}</span>
            </div>
        </div>
    </div>

    <div data-panel="match-settings" id="match-settings-panel" class="panel" style:display={v.settings?.shown ? null : 'none'}>
        <h3>Match Settings</h3>
        <!-- svelte-ignore a11y_label_has_associated_control: a caption styled as a label, as in the legacy markup -->
        <div class="summary-grid" id="match-settings-grid">
            {#each v.settings?.cells ?? [] as cell}
                <div class="summary-item" title={cell.title || undefined}>
                    <label>{cell.label}</label>
                    <span>{cell.value}</span>
                </div>
            {/each}
        </div>
        <!-- svelte-ignore a11y_label_has_associated_control: a caption styled as a label, as in the legacy markup -->
        <div id="match-modifiers-row" style="margin-top: 15px;" style:display={v.settings?.modifiers.length ? null : 'none'}>
            <label class="modifier-label">Modifiers</label>
            <div id="match-modifiers" class="modifier-list">
                {#each v.settings?.modifiers ?? [] as pill}
                    <span class="modifier-tag" title={pill.title || undefined}>{pill.label}</span>
                {/each}
            </div>
        </div>
    </div>

    <div data-panel="server-info" id="server-info-panel" class="panel" style:display={v.serverInfo ? null : 'none'}>
        <details>
            <summary><h3 style="display: inline-block; margin: 0;">Server Info</h3></summary>
            <table id="server-info-table" class="stats-table serverinfo-table">
                <thead>
                    <tr><th>Key</th><th>Value</th></tr>
                </thead>
                <tbody id="server-info-body" use:rows={{ rows: v.serverInfo ?? NO_ROWS }}></tbody>
            </table>
        </details>
    </div>

    <div data-panel="teams" id="teams-panel" class="panel team-aggregate-panel" style:display={v.teamsHidden ? 'none' : null}>
        <h3>Teams</h3>
        <div id="teams-list" class="teams-list">
            {#each v.teamList as team}
                <div class="team-item">
                    <span class="team-name">{team.name}</span>
                    <span class="team-frags">{team.frags} frags</span>
                </div>
            {/each}
        </div>
    </div>

    <div data-panel="basic-stats" id="players-panel" class="panel">
        <h3>Basic Stats</h3>
        <div id="players-provenance" class={provenanceClass(v.note)} style:display={v.note ? null : 'none'}>{@html v.note?.html ?? ''}</div>
        <h4 class="team-aggregate-heading">Per Team</h4>
        <table id="player-stats-team-table" class="stats-table team-aggregate-table">
            <thead>
                <tr>
                    <th>Team</th>
                    <th title="Total frags (kills minus suicides/teamkills)">Frags</th>
                    <th title="Kill efficiency: kills / (kills + deaths)">Eff</th>
                    <th title="Enemy kills">Kills</th>
                    <th title="Enemy RLs killed: enemies killed while THEY were holding a rocket launcher (includes those holding both RL and LG). Kills made WITH each weapon are in the Weapons tab.">eRL</th>
                    <th title="Enemy LGs killed: enemies killed while THEY were holding a lightning gun (includes those holding both RL and LG). Kills made WITH each weapon are in the Weapons tab.">eLG</th>
                    <th title="Total deaths">Deaths</th>
                    <th title="Team kills">TK</th>
                    <th title="Suicides">Bores</th>
                    <th title="Longest kill streak between deaths, replayed from the frag log. A team row carries the best any member ran, never a sum.">Spree</th>
                    <th title="Longest kill streak run while holding quad, reset by a death or a fresh quad pickup. A team row carries the best any member ran, never a sum.">QSpree</th>
                    <th title="Damage given to enemies">Dmg</th>
                    <th title="Damage dealt to TEAMMATES (friendly fire). Measured whenever the damage family is present, so 0 is a real reading, not a gap — and it is damage DEALT, so it does not appear in the victim's Dmg column. Meaningless in a duel, where it is always 0.">TDmg</th>
                    <th title="Damage taken from ALL sources (enemy, team, self, environment) — not the same quantity as KTX's enemy-only figure">Taken</th>
                    <th title="Damage on enemy weapon (damage dealt while enemy held a weapon)">Ewep</th>
                    <th title="Average damage taken to die — per-player only, since averaging averages across players with different death counts is meaningless">ToDie</th>
                    <th title="Players folded into this team's totals that were actually in the match">Players</th>
                </tr>
            </thead>
            <tbody id="player-stats-team-body" use:rows={{ rows: v.tables.basicTeam, colors: v.colors }}></tbody>
        </table>
        <h4 class="per-player-heading">Per Player</h4>
        <table id="scoreboard" class="stats-table">
            <thead>
                <tr>
                    <th>Player</th>
                    <th class="team-col">Team</th>
                    <th class="handicap-col" title="Player handicap (only shown when at least one player has a non-default handicap)" style:display={v.showHandicap ? null : 'none'}>HC</th>
                    <th title="Total frags (kills minus suicides/teamkills)">Frags</th>
                    <th title="Kill efficiency: kills / (kills + deaths)">Eff</th>
                    <th title="Enemy kills">Kills</th>
                    <th title="Enemy RLs killed: enemies killed while THEY were holding a rocket launcher (includes those holding both RL and LG). Kills made WITH each weapon are in the Weapons tab.">eRL</th>
                    <th title="Enemy LGs killed: enemies killed while THEY were holding a lightning gun (includes those holding both RL and LG). Kills made WITH each weapon are in the Weapons tab.">eLG</th>
                    <th title="Total deaths">Deaths</th>
                    <th class="team-col" title="Team kills">TK</th>
                    <th title="Suicides">Bores</th>
                    <th title="Longest kill streak between deaths, replayed from the frag log. Our gate does not count a suicide as a kill, so this can read one lower than the server's own spree.max on a duel or FFA demo.">Spree</th>
                    <th title="Longest kill streak run while holding quad, reset by a death or a fresh quad pickup.">QSpree</th>
                    <th title="Damage given to enemies">Dmg</th>
                    <th class="team-col" title="Damage dealt to TEAMMATES (friendly fire). Measured whenever the damage family is present, so 0 is a real reading, not a gap — and it is damage DEALT, so it does not appear in the victim's Dmg column. Hidden in an individual mode, where nobody has a teammate.">TDmg</th>
                    <th title="Damage taken from ALL sources (enemy, team, self, environment) — not the same quantity as KTX's enemy-only figure">Taken</th>
                    <th title="Damage on enemy weapon (damage dealt while enemy held a weapon)">Ewep</th>
                    <th title="Average damage taken to die">ToDie</th>
                    <th>Ping</th>
                </tr>
            </thead>
            <tbody id="scoreboard-body" use:rows={{ rows: v.tables.basicPlayer, colors: v.colors }}></tbody>
        </table>
    </div>

    <div data-panel="weapon-stats" id="weapons-summary-panel" class="panel">
        <h3>Weapon Stats</h3>
        <div id="weapons-provenance" class={provenanceClass(v.note)} style:display={v.note ? null : 'none'}>{@html v.note?.html ?? ''}</div>
        <h4 class="team-aggregate-heading">Per Team</h4>
        <div class="items-table-wrap team-aggregate-table">
            <table id="weapon-stats-team-table" class="stats-table weapon-stats-split team-aggregate-table">
                {@render weaponHead('Team')}
                <tbody id="weapon-stats-team-body" use:rows={{ rows: v.tables.weapTeam, colors: v.colors }}></tbody>
            </table>
        </div>
        <h4 class="per-player-heading">Per Player</h4>
        <div class="items-table-wrap">
            <table id="weapon-stats-table" class="stats-table weapon-stats-split">
                {@render weaponHead('Player')}
                <tbody id="weapon-stats-body" use:rows={{ rows: v.tables.weapPlayer, colors: v.colors }}></tbody>
            </table>
        </div>
        <p id="weapons-convention-note" class="panel-note acc-scale-note" style:display={v.convNote ? null : 'none'}>{v.convNote}</p>
    </div>

    <div data-panel="items" id="items-panel" class="panel">
        <h3>Item Pickups & Drops</h3>
        <details class="panel-info">
            <summary>More info</summary>
            <p class="panel-note">Each item shows how many times it was taken and, under <em>s</em>, the
                seconds it was held — integrated from the position/inventory streams, not from KTX, which
                writes no weapon hold time at all and whose armor clock keeps counting after the armor is
                chewed to zero. A possession cell's tooltip carries its share of time <em>alive</em> and
                the run count; the alive-time complement — time spent wearing no armor at all — is not a
                column here but is reported as <code>hold.armor.none</code> in the result JSON and the API.
                Mega health has no possession column: it is consumed on pickup.</p>
        </details>
        <h4 class="team-aggregate-heading">Per Team</h4>
        <div class="items-table-wrap team-aggregate-table">
            <table id="items-team-table" class="stats-table team-aggregate-table">
                {@render itemsHead('Team')}
                <tbody id="items-team-body" use:rows={{ rows: v.tables.itemTeam, colors: v.colors }}></tbody>
            </table>
        </div>
        <h4 class="per-player-heading">Per Player</h4>
        <div class="items-table-wrap">
            <table id="items-table" class="stats-table">
                {@render itemsHead('Player')}
                <tbody id="items-body" use:rows={{ rows: v.tables.itemPlayer, colors: v.colors }}></tbody>
            </table>
        </div>
    </div>

    <div data-panel="kills-by-weapon" id="weapons-panel" class="panel">
        <h3>Kills by Weapon</h3>
        <div id="weapons-chart" class="chart">
            {#each v.bars ?? [] as bar}
                <div class="weapon-bar">
                    <span class="weapon-name">{bar.name}</span>
                    <div class="bar-container">
                        <div class="bar" style="width: {bar.pct}%"></div>
                    </div>
                    <span class="weapon-count">{bar.count}</span>
                </div>
            {/each}
        </div>
    </div>
</div>
