<script>
    // The Pickups tab: weapon and item pickups per team and per player —
    // per-entity counts, the pack column, the Σ checked against KTX's own
    // counters, possession seconds (src/lib/pickups.js has the rules).
    // src/app/tabs.js toggles the `active` class like on every other
    // .tab-content.
    //
    // The tables are rebuilt — once, on the next frame the tab shows — when
    // the demo or the teams changed, and at once on a mode change. Their
    // headers and rows are markup built by hand rather than an {#each}:
    // the column set depends on the demo, and the shared makeSortable
    // (src/ui/sortable.js) sorts by moving row nodes. A rebuild replaces
    // the <th> nodes, so makeSortable is applied again after each one — it
    // binds per element, so it never double-binds. The two panels carry
    // data-panel and sit directly in #tab-pickups for src/ui/panels.
    import { onMount } from 'svelte';
    import { demo, teams, activeTab } from '../../state/index.js';
    import { computePickupsState, hasPickups, pickupsSection } from '../../lib/pickups.js';
    import { makeSortable } from '../../ui/sortable.js';

    // 'all' | 'first'. Kept across demos, like the select.
    let mode = $state(/** @type {import('../../lib/pickups.js').PickupMode} */ ('all'));
    let empty = $state(false);
    // The Item Pickups panel hides when the demo has weapon pickups but no
    // item ones. A demo with neither leaves it as it was (the tab shows its
    // empty message).
    let itemsShown = $state(true);
    let weapTeam = $state();
    let weapPlayer = $state();
    let itemTeam = $state();
    let itemPlayer = $state();

    let dirty = true;
    let queued = false;

    function schedule() {
        if (queued || !dirty || activeTab.get() !== 'pickups') return;
        queued = true;
        requestAnimationFrame(() => {
            queued = false;
            if (dirty && activeTab.get() === 'pickups') build();
        });
    }

    function invalidate() {
        dirty = true;
        schedule();
    }

    /** @param {HTMLTableElement} table */
    function clear(table) {
        // Every pickups table has its <thead> in the markup below.
        /** @type {HTMLTableSectionElement} */ (table.tHead).replaceChildren();
        table.tBodies[0].replaceChildren();
    }

    function build() {
        dirty = false;
        const all = [weapTeam, weapPlayer, itemTeam, itemPlayer];
        all.forEach(clear);
        const result = demo.get()?.result;
        if (!result) {
            empty = false;
            return;
        }
        const t = teams.get();
        const state = computePickupsState(result, t.order);
        const has = hasPickups(state);
        if (!has.weapon && !has.item) {
            empty = true;
            for (const table of all) table.style.display = 'none';
            return;
        }
        empty = false;
        fill(pickupsSection(state, 'weap', mode), weapTeam, weapPlayer, t.colors);
        itemsShown = has.item;
        fill(pickupsSection(state, 'item', mode), itemTeam, itemPlayer, t.colors);
        for (const table of all) makeSortable(table);
    }

    /**
     * @param {ReturnType<typeof pickupsSection>} section
     * @param {HTMLTableElement} teamTable
     * @param {HTMLTableElement} playerTable
     * @param {string[]} colors
     */
    function fill(section, teamTable, playerTable, colors) {
        if (!section) {
            teamTable.style.display = 'none';
            playerTable.style.display = 'none';
            return;
        }
        teamTable.style.display = '';
        playerTable.style.display = '';
        fillTable(teamTable, section.team, colors);
        fillTable(playerTable, section.player, colors);
    }

    /**
     * @param {HTMLTableElement} table
     * @param {import('../../lib/pickups.js').PickupsTable} section
     * @param {string[]} colors
     */
    function fillTable(table, { head, rows }, colors) {
        const htr = document.createElement('tr');
        htr.innerHTML = head;
        /** @type {HTMLTableSectionElement} */ (table.tHead).appendChild(htr);
        const body = table.tBodies[0];
        for (const row of rows) {
            const tr = document.createElement('tr');
            // The canonical team colour (src/state/teams.js), by the row's
            // index in the canonical order.
            if (row.colorIdx >= 0 && row.colorIdx < colors.length) {
                tr.style.setProperty('--row-team-color', colors[row.colorIdx]);
            }
            tr.innerHTML = row.html;
            body.appendChild(tr);
        }
    }

    function onMode() {
        if (demo.get()) build();
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

<div id="tab-pickups" class="tab-content">
    <div class="empty-state">Load or search a demo to begin.</div>

    <div data-panel="weapon-pickups" class="panel">
        <div class="pickups-toolbar">
            <label>Mode:
                <select id="pickups-mode" bind:value={mode} onchange={onMode}>
                    <option value="all">All pickups (every touch)</option>
                    <option value="first">First pickup per life</option>
                </select>
            </label>
        </div>

        <h3>Weapon Pickups</h3>
        <h4 class="team-aggregate-heading">Per Team</h4>
        <div class="pickups-table-wrap">
            <table id="pickups-weap-team-table" class="stats-table pickups-table team-aggregate-table" bind:this={weapTeam}>
                <thead></thead>
                <tbody></tbody>
            </table>
        </div>
        <h4 class="per-player-heading">Per Player</h4>
        <div class="pickups-table-wrap">
            <table id="pickups-weap-player-table" class="stats-table pickups-table" bind:this={weapPlayer}>
                <thead></thead>
                <tbody></tbody>
            </table>
        </div>
    </div>

    <div data-panel="item-pickups" class="panel" id="pickups-items-panel" style:display={itemsShown ? null : 'none'}>
        <h3>Item Pickups</h3>
        <h4 class="team-aggregate-heading">Per Team</h4>
        <div class="pickups-table-wrap">
            <table id="pickups-item-team-table" class="stats-table pickups-table team-aggregate-table" bind:this={itemTeam}>
                <thead></thead>
                <tbody></tbody>
            </table>
        </div>
        <h4 class="per-player-heading">Per Player</h4>
        <div class="pickups-table-wrap">
            <table id="pickups-item-player-table" class="stats-table pickups-table" bind:this={itemPlayer}>
                <thead></thead>
                <tbody></tbody>
            </table>
        </div>
    </div>

    <div id="pickups-empty" class="empty-message" style:display={empty ? null : 'none'}>
        No tracked pickups in this demo.
    </div>
</div>
