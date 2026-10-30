<script>
    // The Region Control panel: the region definition editor (one row per
    // region — name and comma-separated locs — plus Add / Save… / Load…) and
    // the per-region control matrix. It renders the region-control model
    // (src/state/regions.js): the demo's own regions when the load puts them in
    // effect (on load and again when the deferred buckets land — which
    // rebuilds the rows from them), then each edit's recomputed control
    // states. A committed edit (an input's change, a remove, a load) puts the
    // rows' definitions in effect through applyRegionDefs; the Map and the
    // Timeline's Region Control read the same model.
    //
    // Shown only while the model is editable (two sides — the load's
    // loadMapAndRegions decides, src/app/load.js). Without new control states the matrix keeps
    // what it last showed: a demo's defaults before its buckets land render
    // nothing, and an edit that leaves no region with stats leaves the last
    // table up.
    import { onMount, tick } from 'svelte';
    import { demo, teams, regionControl, applyRegionDefs } from '../../state/index.js';
    import { regionRowsFromRegions, readRegionDefs, regionFileJSON, regionFileName, parseRegionFile, regionSides } from '../../lib/regions.js';
    import { buildRegionHeatmap } from '../../lib/loc-heatmap.js';
    import { mapFileKey } from '../../lib/result.js';
    import { fillHeatmapTable, clearHeatmapTable } from './heatmap-table.js';

    /** @typedef {import('../../lib/regions.js').RegionRow & { id: number }} EditorRow */
    /** @typedef {import('../../state/regions.js').RegionControl} RegionControl */

    let editable = $state(false);
    /** @type {EditorRow[]} */
    let rows = $state([]);
    // Bound before onMount subscribes to the model.
    /** @type {HTMLTableElement} */
    let table;
    // Bound whenever the Load… button that clicks it renders.
    /** @type {HTMLInputElement} */
    let loadInput = $state(/** @type {any} */ (undefined));
    let nextId = 0;
    /** @param {import('../../lib/regions.js').RegionRow} r */
    const row = (r) => ({ id: nextId++, name: r.name, locs: r.locs });

    /** @param {RegionControl | null} m */
    function onModel(m) {
        if (!m) {
            editable = false;
            rows = [];
            if (table) clearHeatmapTable(table, { head: false });
            return;
        }
        if (m.stage === 'loaded') {
            editable = m.editable;
            rows = m.editable ? regionRowsFromRegions(m.regions).map(row) : [];
            if (m.editable) renderMatrix(m);
        } else if (m.stage === 'computed') {
            renderMatrix(m);
        }
    }

    /** @param {RegionControl} m */
    function renderMatrix(m) {
        const t = teams.get();
        // A model with stats names both sides.
        const data = buildRegionHeatmap(m.regions, m.stats, m.teamA, m.teamB, regionSides(t.order, m));
        if (data) fillHeatmapTable(table, data, 'Region', t.colors);
    }

    function apply() {
        // The editor shows only while a demo's regions are in effect.
        applyRegionDefs(/** @type {import('../../data/wasm-source.js').DemoHandle} */ (demo.get()), readRegionDefs(rows));
    }

    /**
     * @param {EditorRow} r
     * @param {'name' | 'locs'} field
     * @param {Event & { currentTarget: HTMLInputElement }} e
     */
    function edit(r, field, e) {
        r[field] = e.currentTarget.value;
        apply();
    }

    /** @param {EditorRow} r */
    function remove(r) {
        rows = rows.filter((x) => x !== r);
        apply();
    }

    /** @type {Record<number, HTMLInputElement>} */
    let nameInputs = $state({});
    async function add() {
        const r = row({ name: '', locs: '' });
        rows.push(r);
        await tick();
        nameInputs[r.id]?.focus();
    }

    function save() {
        const blob = new Blob([regionFileJSON(readRegionDefs(rows))], { type: 'application/json' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = regionFileName(mapFileKey(demo.get()?.result));
        document.body.appendChild(a);
        a.click();
        a.remove();
        setTimeout(() => URL.revokeObjectURL(url), 0);
    }

    // Load…: rebuild the rows from the file's definitions, then put them in
    // effect so the map render / stats / timeline pick up the change.
    /** @param {Event & { currentTarget: HTMLInputElement }} e */
    function load(e) {
        const file = e.currentTarget.files && e.currentTarget.files[0];
        if (file) {
            const reader = new FileReader();
            reader.onload = () => {
                // readAsText: the result is a string.
                const parsed = parseRegionFile(/** @type {string} */ (reader.result));
                if (parsed.error) {
                    alert(parsed.error);
                    return;
                }
                rows = /** @type {import('../../lib/regions.js').RegionRow[]} */ (parsed.rows).map(row);
                apply();
            };
            reader.onerror = () => alert('Failed to read file.');
            reader.readAsText(file);
        }
        e.currentTarget.value = '';
    }

    onMount(() => regionControl.subscribe(onModel));
</script>

<!-- Region Control (definition editor + per-region control matrix) -->
<div data-panel="region-control" class="panel" id="region-control-panel" style="display: none;" style:display={editable ? null : 'none'}>
    <h3>Region Control</h3>
    <details class="panel-info">
        <summary>More info</summary>
        <div class="locheatmap-desc">
            Group locs into named regions. Rows = regions, columns = control
            states (the team color underlines which columns are which).
            Cell color is normalised <em>per region</em> (brightest = that
            region's dominant control state, Empty excluded), so compare colors
            across a row — not down a column. The printed % is the absolute
            match fraction; players count only while <em>alive</em>, so a region
            whose occupants keep dying reads more Empty. Click a column header to
            sort; hover for details.
        </div>
    </details>
    <div id="region-config" class="region-config">
        {#if editable}
            <div class="region-config-rows">
                {#each rows as r (r.id)}
                    <div class="region-config-row">
                        <input type="text" class="region-name-input" placeholder="region name" value={r.name}
                            bind:this={nameInputs[r.id]} onchange={(e) => edit(r, 'name', e)}>
                        <input type="text" class="region-locs-input" placeholder="loc1, loc2, …" value={r.locs}
                            onchange={(e) => edit(r, 'locs', e)}>
                        <button type="button" class="region-remove-btn" title="Remove region" onclick={() => remove(r)}>&times;</button>
                    </div>
                {/each}
            </div>
            <div class="region-config-actions">
                <button type="button" class="region-add-btn" onclick={add}>+ Add region</button>
                <button type="button" class="region-save-btn" title="Download these region definitions as JSON" onclick={save}>Save…</button>
                <button type="button" class="region-load-btn" title="Load region definitions from a JSON file" onclick={() => loadInput.click()}>Load…</button>
                <input type="file" class="region-load-input" accept="application/json,.json" hidden bind:this={loadInput} onchange={load}>
            </div>
        {/if}
    </div>
    <div class="heatmap-legend">
        <span>less</span><span class="heatmap-legend-bar"></span><span>more</span>
    </div>
    <div class="heatmap-table-wrap">
        <table class="stats-table heatmap-table region-control-table" id="region-control-table" bind:this={table}>
            <thead><tr id="region-control-thead-row"></tr></thead>
            <tbody id="region-control-body"></tbody>
        </table>
    </div>
</div>
