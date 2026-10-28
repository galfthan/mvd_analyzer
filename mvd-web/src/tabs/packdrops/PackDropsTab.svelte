<script>
    // The Pack Drops tab: every RL / LG backpack drop with its fate, the
    // picker and the kills scored with it, hub links to the drop and the
    // run, and a filter row (src/lib/packdrops.js has the join and the
    // statuses). src/app/tabs.js toggles the `active` class like on every
    // other .tab-content.
    //
    // The rows are built — once, on the next frame the tab shows — after the
    // demo changed; a filter change rebuilds them at once. They
    // are markup built by hand, not an {#each}, because the shared
    // makeSortable (src/ui/sortable.js) sorts the table by moving its row
    // nodes. Filter state survives switching tabs, and a value the next
    // demo also has stays selected.
    import { onMount } from 'svelte';
    import { clock, demo, activeTab } from '../../state/index.js';
    import { demoOffsetSec } from '../../lib/result.js';
    import { FILTER_KEYS, packDropRows, filterOptions, filterRows, countText, rowHtml } from '../../lib/packdrops.js';
    import { makeSortable } from '../../ui/sortable.js';

    const FILTERS = [
        ['dropplayer', 'Dropper'],
        ['dropteam', 'Drop team'],
        ['pickplayer', 'Picker'],
        ['pickteam', 'Pick team'],
        ['status', 'Status'],
    ];
    const blank = () => Object.fromEntries(FILTER_KEYS.map((k) => [k, []]));

    let options = $state(blank());
    let filters = $state(Object.fromEntries(FILTER_KEYS.map((k) => [k, ''])));
    let count = $state('');
    let empty = $state(false);
    let table = $state();
    let tbody = $state();

    let rows = [];
    let ctx = null;
    let dirty = true;
    let queued = false;

    function schedule() {
        if (queued || !dirty || activeTab.get() !== 'packdrops') return;
        queued = true;
        requestAnimationFrame(() => {
            queued = false;
            if (dirty && activeTab.get() === 'packdrops') build();
        });
    }

    function invalidate() {
        dirty = true;
        schedule();
    }

    function build() {
        dirty = false;
        const result = demo.get()?.result;
        rows = result ? packDropRows(result) : [];
        // hubInfo is set on the result by loadDemo (src/app/load.js) once the demo
        // store has it, so it is read here rather than on the store update.
        ctx = {
            gameId: result?.hubInfo?.gameId,
            userIDs: result?.timelineAnalysis?.playerUserIDs || {},
            demoOffset: demoOffsetSec(result),
        };
        empty = !!result && rows.length === 0;
        const opts = filterOptions(rows);
        for (const k of FILTER_KEYS) {
            if (!opts[k].includes(filters[k])) filters[k] = '';
        }
        options = opts;
        renderRows();
    }

    function renderRows() {
        tbody.replaceChildren();
        const shown = filterRows(rows, filters);
        for (const r of shown) {
            const tr = document.createElement('tr');
            tr.innerHTML = rowHtml(r, ctx);
            tr.querySelector('.time-link').addEventListener('click', () => clock.seek(r.drop.time));
            tbody.appendChild(tr);
        }
        count = rows.length ? countText(shown.length, rows.length) : '';
    }

    onMount(() => {
        makeSortable(table);
        const offs = [
            demo.subscribe(invalidate),
            activeTab.subscribe(schedule),
        ];
        return () => offs.forEach((off) => off());
    });
</script>

<div id="tab-packdrops" class="tab-content">
    <div class="empty-state">Load or search a demo to begin.</div>
    <div class="panel">
        <h3>RL / LG Backpack Drops</h3>
        <div class="packdrops-filters">
            {#each FILTERS as [key, label] (key)}
                <label>{label}: <select id="packdrops-filter-{key}" bind:value={filters[key]} onchange={renderRows}>
                    <option value="">All</option>
                    {#each options[key] as v (v)}
                        <option value={v}>{v}</option>
                    {/each}
                </select></label>
            {/each}
            <span id="packdrops-count" class="packdrops-count">{count}</span>
        </div>
        <table class="stats-table" id="packdrops-table" bind:this={table}>
            <thead>
                <tr>
                    <th>Time</th>
                    <th>Dropper</th>
                    <th>Drop Team</th>
                    <th>Weapon</th>
                    <th title="Hub replay starting 10s before the drop">Drop</th>
                    <th>Status</th>
                    <th>Picker</th>
                    <th>Pick Team</th>
                    <th title="Frags the picker scored with the pack's weapon before their next death">Kills</th>
                    <th title="Hub replay from 3s before pickup to picker's next death">Run</th>
                </tr>
            </thead>
            <tbody id="packdrops-body" bind:this={tbody}></tbody>
        </table>
        <div id="packdrops-empty" class="empty-message" style:display={empty ? 'block' : 'none'}>
            No RL / LG backpack drops detected in this demo.
        </div>
    </div>
</div>
