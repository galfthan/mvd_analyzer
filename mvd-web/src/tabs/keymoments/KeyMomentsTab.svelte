<script>
    // The Key Moments tab: powerup runs (with a min length / min frags
    // filter), the longest frag streaks, demo markers, the three view-query
    // rankings (top damage windows, top RL / LG kill bursts) and the four
    // highlight-catalogue tables (src/lib/keymoments.js has the rules and
    // the row markup). src/app/tabs.js toggles the `active` class like on every
    // other .tab-content.
    //
    // The tables are built — once, on the next frame the tab shows — after
    // the demo changed; a filter change rebuilds the powerup runs at once.
    // Nothing renders for a demo without timelineAnalysis. Rows
    // are markup built by hand rather than an {#each}, because the shared
    // makeSortable (src/ui/sortable.js) sorts by moving row nodes. The ten
    // panels carry data-panel and sit directly in #tab-keymoments for
    // src/ui/panels.
    //
    // The view queries resolve after the rest of the tab has painted, each
    // table on its own. A reply for a superseded build is dropped: it would
    // render with the hub id and userids of the demo it was issued for, and
    // the newer build issues its own queries, so dropping it loses nothing.
    import { onMount } from 'svelte';
    import { clock, demo, activeTab } from '../../state/index.js';
    import { demoOffsetSec } from '../../lib/result.js';
    import { damageProvenanceHtml, provenanceClass } from '../../lib/provenance.js';
    import {
        KEYMOMENTS_FILTER_DEFAULTS, cleanFilterValue, powerupRuns, filterPowerupRuns, fragStreaks, demoMarkers,
        powerupRunRowHtml, streakRowHtml, markerRowHtml, TOP_QUERIES, topRows, topRowHtml, HIGHLIGHT_TABLES,
    } from '../../lib/keymoments.js';
    import { makeSortable } from '../../ui/sortable.js';

    // Which empty message shows, by the id prefix of its table.
    const EMPTY_IDS = ['keymoments', 'keymoments-filtered', 'fragstreaks', 'demomarkers', 'topdmg', 'toprl', 'toplg',
        'airgibs', 'discharges', 'quadbores', 'telefrags'];
    const noEmpties = () => Object.fromEntries(EMPTY_IDS.map((id) => [id, false]));

    let empty = $state(noEmpties());
    // The damage-evidence banner over the three damage-ranked tables (the
    // same renderer as the Summary and Aim tabs' banners). Powerup runs and
    // frag streaks are frag-log-only and carry none.
    let note = $state(null);
    let root = $state();
    let minDurEl = $state();
    let minFragsEl = $state();
    // tbody / table elements by id prefix.
    const bodies = $state({});
    const tables = $state({});

    let filter = { ...KEYMOMENTS_FILTER_DEFAULTS };
    let runs = [];
    let ctx = null;
    let token = 0;
    let dirty = true;
    let queued = false;

    function schedule() {
        if (queued || !dirty || activeTab.get() !== 'keymoments') return;
        queued = true;
        requestAnimationFrame(() => {
            queued = false;
            if (dirty && activeTab.get() === 'keymoments') build();
        });
    }

    // A new demo: drop the old one's rows and any view reply still in
    // flight for it, and put the filter back to its defaults.
    function onDemo() {
        token++;
        clearAll();
        filter = { ...KEYMOMENTS_FILTER_DEFAULTS };
        if (minDurEl) minDurEl.value = filter.minDur;
        if (minFragsEl) minFragsEl.value = filter.minFrags;
        dirty = true;
        schedule();
    }

    function clearAll() {
        for (const body of Object.values(bodies)) body?.replaceChildren();
        empty = noEmpties();
        note = null;
        runs = [];
    }

    function appendRows(body, rows, html, timeOf) {
        for (const r of rows) {
            const tr = document.createElement('tr');
            tr.innerHTML = html(r, ctx);
            const link = tr.querySelector('.time-link');
            if (link) link.addEventListener('click', () => clock.seek(timeOf(r)));
            body.appendChild(tr);
        }
    }

    function build() {
        dirty = false;
        const handle = demo.get();
        const my = ++token;
        clearAll();
        const result = handle?.result;
        if (!result?.timelineAnalysis) return;
        // hubInfo is set on the result by loadDemo (src/app/load.js) once the demo
        // store has it, so it is read here rather than on the store update.
        ctx = {
            gameId: result.hubInfo?.gameId,
            userIDs: result.timelineAnalysis.playerUserIDs || {},
            demoOffset: demoOffsetSec(result),
        };
        note = damageProvenanceHtml(result);

        runs = powerupRuns(result);
        renderPowerupRuns();

        const streaks = fragStreaks(result);
        empty.fragstreaks = streaks.length === 0;
        appendRows(bodies.fragstreaks, streaks, streakRowHtml, (s) => s.time);

        const markers = demoMarkers(result);
        empty.demomarkers = markers.length === 0;
        appendRows(bodies.demomarkers, markers, markerRowHtml, (m) => m.time);

        for (const spec of HIGHLIGHT_TABLES) renderHighlights(spec, result);

        for (const [key, fn, opts] of TOP_QUERIES) {
            handle.view(fn, opts)
                .then((v) => { if (my === token) renderTop(key, v); })
                .catch((err) => {
                    if (my !== token) return;
                    console.warn(`[keymoments] ${fn} unavailable:`, err.message || err);
                    renderTop(key, null);
                });
        }
    }

    // The powerup-runs table on its own, so the filter inputs can re-render
    // it without touching the tables below.
    function renderPowerupRuns() {
        bodies.keymoments.replaceChildren();
        const shown = filterPowerupRuns(runs, filter);
        // Powerups don't exist on duel / 2v2 maps, so the list is routinely
        // empty. The two empty states are distinct: nothing happened vs the
        // filter hid it.
        empty.keymoments = runs.length === 0;
        empty['keymoments-filtered'] = runs.length > 0 && shown.length === 0;
        appendRows(bodies.keymoments, shown, powerupRunRowHtml, (e) => e.time);
    }

    function onFilter(e, key) {
        const clean = cleanFilterValue(e.currentTarget.value, key);
        filter[key] = clean;
        e.currentTarget.value = clean;
        if (demo.get()?.result) renderPowerupRuns();
    }

    // `v` is the view reply, or null when the query failed (the
    // 422-equivalent: no damage log or no measurable liveness).
    function renderTop(key, v) {
        bodies[key].replaceChildren();
        const rows = topRows(key, v);
        empty[key] = rows.length === 0;
        appendRows(bodies[key], rows, (r, c) => topRowHtml(key, r, c), (r) => r.t);
    }

    // A highlight table opens on its default sort: the header indicator is
    // reset to it each load and the rows laid out in that order;
    // makeSortable takes over on click. An empty table leaves its headers as
    // they were.
    function renderHighlights(spec, result) {
        const rows = spec.rows(result);
        const table = tables[spec.id];
        if (rows.length === 0) {
            empty[spec.id] = true;
            return;
        }
        table.querySelectorAll('thead th').forEach((th) => th.classList.remove('sort-asc', 'sort-desc', 'sort-active'));
        table.querySelector(`thead th[data-sort="${spec.defaultSort}"]`)?.classList.add('sort-desc');
        appendRows(bodies[spec.id], rows, spec.rowHtml, (r) => r.t);
        makeSortable(table);
    }

    onMount(() => {
        root.querySelectorAll('table.stats-table').forEach(makeSortable);
        const offs = [
            demo.subscribe(onDemo),
            activeTab.subscribe(schedule),
        ];
        return () => offs.forEach((off) => off());
    });
</script>

{#snippet provenance(id)}
    <div id="{id}-provenance" class={provenanceClass(note)} style:display={note ? null : 'none'}>{@html note?.html ?? ''}</div>
{/snippet}

{#snippet emptyMsg(id, text)}
    <div id="{id}-empty" class="empty-message" style:display={empty[id] ? 'block' : 'none'}>{text}</div>
{/snippet}

{#snippet killHead()}
    <thead>
        <tr>
            <th>Time</th>
            <th>Kill</th>
            <th title="Burst damage, bounded family">Dmg</th>
            <th title="Hits in the burst">Hits</th>
            <th title="First hit of the burst to the kill">Span</th>
            <th title="Victim's weapon class at the killing hit">Victim wep</th>
            <th title="Damage the victim dealt back to the killer in the 4 s before the kill">Return</th>
            <th>View</th>
        </tr>
    </thead>
{/snippet}

<div id="tab-keymoments" class="tab-content keymoments-grid" bind:this={root}>
    <div class="empty-state">Load or search a demo to begin.</div>
    <div data-panel="powerup-runs" class="panel">
        <h3>Powerup Runs</h3>
        <div class="keymoments-filters">
            <label>Min length: <input type="number" id="keymoments-min-dur" min="0" step="1" value="5"
                bind:this={minDurEl} onchange={(e) => onFilter(e, 'minDur')}> s</label>
            <label>Min frags: <input type="number" id="keymoments-min-frags" min="0" step="1" value="3"
                bind:this={minFragsEl} onchange={(e) => onFilter(e, 'minFrags')}></label>
        </div>
        <table class="stats-table" id="keymoments-table">
            <thead>
                <tr>
                    <th>Time</th>
                    <th>Powerup</th>
                    <th>Player</th>
                    <th>Team</th>
                    <th>Frags</th>
                    <th>Duration</th>
                    <th>View</th>
                </tr>
            </thead>
            <tbody id="keymoments-body" bind:this={bodies.keymoments}></tbody>
        </table>
        {@render emptyMsg('keymoments', 'No powerup pickups detected in this demo.')}
        {@render emptyMsg('keymoments-filtered', 'No powerup runs match the filter.')}
    </div>
    <div data-panel="streaks" class="panel">
        <h3>Longest Frag Streaks</h3>
        <table class="stats-table" id="fragstreaks-table">
            <thead>
                <tr>
                    <th>Start</th>
                    <th>Player</th>
                    <th>Team</th>
                    <th>Frags</th>
                    <th>Main Weapon</th>
                    <th>Duration</th>
                    <th>View</th>
                </tr>
            </thead>
            <tbody id="fragstreaks-body" bind:this={bodies.fragstreaks}></tbody>
        </table>
        {@render emptyMsg('fragstreaks', 'No frag streaks detected in this demo.')}
    </div>
    <div data-panel="top-damage" class="panel">
        <h3 title="The ten 10-second stretches with the most bounded enemy damage dealt; ties break on frags">Top Damage Windows (10 s)</h3>
        {@render provenance('topdmg')}
        <table class="stats-table" id="topdmg-table">
            <thead>
                <tr>
                    <th>Start</th>
                    <th>Player</th>
                    <th>Team</th>
                    <th title="Bounded enemy damage dealt in the window — the ranking key">Dmg</th>
                    <th title="Enemy kills inside the same window">Frags</th>
                    <th>View</th>
                </tr>
            </thead>
            <tbody id="topdmg-body" bind:this={bodies.topdmg}></tbody>
        </table>
        {@render emptyMsg('topdmg', 'No damage windows in this demo (needs a damage stream).')}
    </div>
    <div data-panel="top-rl" class="panel">
        <h3 title="Hardest rocket kill bursts: the run of RL hits on that victim leading up to the kill (gap 2.3 s)">Top RL Kills</h3>
        {@render provenance('toprl')}
        <table class="stats-table" id="toprl-table">
            {@render killHead()}
            <tbody id="toprl-body" bind:this={bodies.toprl}></tbody>
        </table>
        {@render emptyMsg('toprl', 'No RL kill bursts (needs a damage log and measurable liveness).')}
    </div>
    <div data-panel="markers" class="panel">
        <h3>Demo Markers</h3>
        <table class="stats-table" id="demomarkers-table">
            <thead>
                <tr>
                    <th>Time</th>
                    <th>Player</th>
                    <th>Team</th>
                    <th>Note</th>
                    <th>Watch</th>
                </tr>
            </thead>
            <tbody id="demomarkers-body" bind:this={bodies.demomarkers}></tbody>
        </table>
        {@render emptyMsg('demomarkers', 'No demo markers in this demo.')}
    </div>
    <div data-panel="top-lg" class="panel">
        <h3 title="Hardest lightning kill bursts: the run of LG hits on that victim leading up to the kill (gap 1.2 s)">Top LG Kills</h3>
        {@render provenance('toplg')}
        <table class="stats-table" id="toplg-table">
            {@render killHead()}
            <tbody id="toplg-body" bind:this={bodies.toplg}></tbody>
        </table>
        {@render emptyMsg('toplg', 'No LG kill bursts (needs a damage log and measurable liveness).')}
    </div>
    <div data-panel="airgibs" class="panel airgibs-panel">
        <h3>Direct Rocket Air Hits</h3>
        <table class="stats-table sortable-table" id="airgibs-table" bind:this={tables.airgibs}>
            <thead>
                <tr>
                    <th data-sort="height" title="Victim height above the floor at the hit">Height</th>
                    <th data-sort="aboveShooter" class="sort-desc" title="Victim height above the shooter at the hit (negative: below the shooter)">Above shooter</th>
                    <th data-sort="attacker">Attacker</th>
                    <th data-sort="victim">Victim</th>
                    <th data-sort="loc">Loc</th>
                    <th data-sort="lethal">Lethal</th>
                    <th title="What the victim had at the hit: health/armor, weapons, powerups">Victim had</th>
                    <th data-sort="time">Time</th>
                    <th>View</th>
                </tr>
            </thead>
            <tbody id="airgibs-body" bind:this={bodies.airgibs}></tbody>
        </table>
        {@render emptyMsg('airgibs', 'No direct rocket air hits detected (needs a map BSP for height data).')}
    </div>
    <div data-panel="discharges" class="panel highlight-panel">
        <h3 title="Lightning gun fired in water: every discharge with evidence — the shooter's death, a kill it made, or a damage-log LG radius hit">Discharges</h3>
        <table class="stats-table sortable-table" id="discharges-table" bind:this={tables.discharges}>
            <thead>
                <tr>
                    <th data-sort="time">Time</th>
                    <th data-sort="player">Player</th>
                    <th data-sort="cells" title="Cells dumped (damage is 35 × cells)">Cells</th>
                    <th data-sort="value" class="sort-desc" title="Ranking: killed enemies holding RL or LG, +1 more per killed enemy with quad — armed kills over respawn fodder">Value</th>
                    <th data-sort="enemy" title="Enemies killed">Enemy</th>
                    <th data-sort="team" title="Teammates killed">Team</th>
                    <th data-sort="self" title="1 when the shooter died in the discharge (self-kill detection is near-certain, not perfect)">Self</th>
                    <th data-sort="delta" title="Frag delta: enemy − team − self (KTX scores a teamkill and a suicide −1 each)">Δ</th>
                    <th data-sort="dmgEnemy" title="Bounded damage given to enemies — every enemy hit, killed or not (needs a damage log)">Dmg E</th>
                    <th data-sort="dmgTeam" title="Bounded damage given to teammates — every teammate hit, killed or not (needs a damage log)">Dmg T</th>
                    <th title="Enemies the discharge hit, one per line: stack, weapons and powerups at the instant; dimmed = hurt, not killed">Enemy victims</th>
                    <th title="Teammates the discharge hit, one per line; dimmed = hurt, not killed">Team victims</th>
                    <th data-sort="loc">Loc</th>
                    <th>View</th>
                </tr>
            </thead>
            <tbody id="discharges-body" bind:this={bodies.discharges}></tbody>
        </table>
        {@render emptyMsg('discharges', 'No discharges detected.')}
    </div>
    <div data-panel="quadbores" class="panel highlight-panel">
        <h3 title="Self-kills by own rocket or grenade while holding quad">Quadbores</h3>
        <table class="stats-table sortable-table" id="quadbores-table" bind:this={tables.quadbores}>
            <thead>
                <tr>
                    <th data-sort="time">Time</th>
                    <th data-sort="player">Player</th>
                    <th data-sort="weapon">Weapon</th>
                    <th data-sort="quadLeft" class="sort-desc" title="Quad time thrown away (30 s quad minus time held)">Quad left</th>
                    <th data-sort="quadFrags" title="Kills made with this quad before the bore">Quad frags</th>
                    <th data-sort="stack" title="Health/armor lost with the quad">Stack lost</th>
                    <th title="Weapons and powerups held at the instant">Had</th>
                    <th data-sort="loc">Loc</th>
                    <th>View</th>
                </tr>
            </thead>
            <tbody id="quadbores-body" bind:this={bodies.quadbores}></tbody>
        </table>
        {@render emptyMsg('quadbores', 'No quadbores detected.')}
    </div>
    <div data-panel="telefrags" class="panel highlight-panel">
        <h3 title="Every telefrag, ranked by how stacked the victim was; deflects are teleporters who died on a pentagram holder">Telefrags</h3>
        <table class="stats-table sortable-table" id="telefrags-table" bind:this={tables.telefrags}>
            <thead>
                <tr>
                    <th data-sort="time">Time</th>
                    <th data-sort="killer">Killer</th>
                    <th data-sort="victim">Victim</th>
                    <th data-sort="relation">Relation</th>
                    <th data-sort="stack" class="sort-desc" title="Victim health/armor just before the telefrag">Stack</th>
                    <th title="Victim's weapons and powerups at the instant">Had</th>
                    <th data-sort="kind">Kind</th>
                    <th data-sort="loc">Loc</th>
                    <th>View</th>
                </tr>
            </thead>
            <tbody id="telefrags-body" bind:this={bodies.telefrags}></tbody>
        </table>
        {@render emptyMsg('telefrags', 'No telefrags in this match.')}
    </div>
</div>
