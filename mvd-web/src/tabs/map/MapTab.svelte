<script>
    // The Map tab: the map canvas (mvd-map-view's MvdMap, map-model.js), its
    // sidebar — the legend, the items list, the region status, the learn-mode
    // entity filters — and the controls under it (trails, follow, the
    // overlays, 3D / occlusion, reset view; fullscreen is src/app/fullscreen.js).
    // src/lib/map.js has the pure parts, sidebar.js
    // the hand-built legend and region status. src/app/tabs.js toggles the
    // `active` class like on every other .tab-content.
    //
    // The map draws only while the tab shows: on every frame of the page's
    // playback (src/state/playback.js — the follow camera moves with it), on
    // the next frame after a seek or a change (geometry, regions, line of
    // sight), and each time the tab shows (now, a frame and 120 ms later — a
    // canvas drawn during the display:none → block switch can stay invisible
    // in Firefox). The sidebar follows a seek at once and playback every
    // 200 ms, and catches up when the tab shows. Nothing runs while it is
    // hidden.
    import { onMount, flushSync } from 'svelte';
    import {
        DEFAULT_YAW, DEFAULT_PITCH, PITCH_MAX, is3D, ITEM_MARKER_STYLES,
    } from '../../../../mvd-map-view/src/index.js';
    import { teams, activeTab, clock, regionControl, playback } from '../../state/index.js';
    import {
        panelItems, itemStatusCell, entityRows, entityRowsHtml, trailButtons, byName,
    } from '../../lib/map.js';
    import { view, mapData, changed, learnMode, resizeMap, ensureLineOfSight } from './map-model.js';
    import { fullscreenTab } from '../../app/fullscreen.js';
    import { buildLegend, syncLegend, syncRegionStatus } from './sidebar.js';

    const TAB = 'map';
    const s = view.state;

    /**
     * @typedef {import('../../lib/map.js').MapItem & { loc?: string }} Item
     */

    // Bound at mount, before any handler or subscription runs.
    /** @type {HTMLCanvasElement} */
    let canvas;
    /** @type {HTMLDivElement} */
    let legendEl;
    /** @type {HTMLDivElement} */
    let statusEl;
    /** @type {HTMLSelectElement} */
    let followSel;

    /** @type {import('../../lib/result.js').Result | null} */
    let result = $state.raw(null);
    let learn = $state(false);
    let rcShown = $state(false);
    let fullscreen = $state(false);
    let hasEntities = $state(false);
    let noData = $state(false);
    let tilted = $state(false);
    let flags = $state({ view: false, vel: false, los: false, pvs: false, occl: false });
    /** @type {string | null} */
    let pendingBtn = $state(null);
    let trails = $state.raw({ all: false, none: true, some: false });
    /** @type {Array<{ name: string, color: string, on?: boolean }>} */
    let trailPlayers = $state.raw([]);
    /** @type {string[]} */
    let followNames = $state.raw([]);
    /** @type {Item[]} */
    let items = $state.raw([]);
    /** @type {Array<{ text: string, cls: string, taken: boolean }>} */
    let itemCells = $state.raw([]);
    let entityHtml = $state('');

    /** @type {string[]} */
    let colors = [];
    /** @type {Map<string, HTMLCanvasElement>} */
    let icons = new Map();
    let queued = false;
    let sidebarDirty = true;
    let lastFull = 0;
    /** @type {number | null} */
    let resizeRaf = null;

    const shown = () => activeTab.get() === TAB;

    // ─── Drawing ────────────────────────────────────────────────────────────

    // Nothing to draw before the first demo.
    function draw() {
        if (!s.initialized) return;
        view.render(s.currentTime);
    }

    function syncSidebar() {
        sidebarDirty = false;
        if (!result) return;
        const t = s.currentTime;
        syncLegend(legendEl, view, result, t);
        syncRegionStatus(statusEl, view, t, colors, icons);
        syncItems(t);
    }

    function frame() {
        queued = false;
        if (!shown()) return;
        if (sidebarDirty) syncSidebar();
        draw();
    }

    function schedule() {
        if (queued || !shown()) return;
        queued = true;
        requestAnimationFrame(frame);
    }

    // redraw: the view changed (a control): draw it on the next frame.
    function redraw() {
        s.renderDirty = true;
        schedule();
    }

    function reveal() {
        if (!shown()) return;
        syncSidebar();
        s.renderDirty = true;
        s.lastRenderedBucket = null;
        draw();
    }

    /** @param {string} tab */
    function onTab(tab) {
        if (tab !== TAB) return;
        // Run once src/app/tabs.js has made the tab visible (it follows the same
        // store, after this), then again on the next frame and 120 ms later.
        queueMicrotask(reveal);
        requestAnimationFrame(reveal);
        setTimeout(reveal, 120);
    }

    /** @param {import('../../state/clock.js').ClockValue} c */
    function onClock({ from }) {
        if (from === 'reset' || !shown()) {
            sidebarDirty = true;
            return;
        }
        if (from === 'playback') {
            // Every frame of playback, in the playback loop's own frame.
            draw();
            const now = performance.now();
            if (now - lastFull > 200) {
                lastFull = now;
                s.renderDirty = true;
                syncSidebar();
            }
            return;
        }
        sidebarDirty = true;
        redraw();
    }

    // ─── A new demo ─────────────────────────────────────────────────────────

    /** @param {{ result: import('../../lib/result.js').Result } | null} d */
    function onData(d) {
        const next = d?.result ?? null;
        if (next !== result) icons = new Map();
        result = next;
        colors = teams.get().colors;
        noData = !!d && s.locations.length === 0;
        hasEntities = !!d && s.mapEntities.length > 0;
        tilted = is3D(view.camera);
        if (!d) {
            legendEl.innerHTML = '';
            statusEl.innerHTML = '';
            items = [];
            itemCells = [];
            sidebarDirty = true;
            return;
        }
        buildLegend(legendEl, s.teams, s.playerSymbols, colors);
        trailPlayers = Object.keys(s.playerSymbols).sort(byName).map((name) => ({
            name, color: colors[s.playerSymbols[name]?.teamIdx ?? 0] || colors[0],
        }));
        followNames = Object.keys(s.fullTrails).sort(byName);
        items = panelItems(d.result.items?.items, ITEM_MARKER_STYLES);
        refreshTrails();
        flushSync();
        followSel.value = s.followPlayer || '';
        syncItems(s.currentTime);
        sidebarDirty = true;
        redraw();
    }

    // ─── Items ──────────────────────────────────────────────────────────────

    /** @param {number} t */
    function syncItems(t) {
        itemCells = items.map((it) => {
            const st = view.itemStatus(it, t);
            return { ...itemStatusCell(st), taken: !st.up };
        });
    }

    // The swatch mirrors the item's map marker: solid armour squares, black
    // squares outlined in the item's colour for the rest.
    /**
     * @param {HTMLElement} node
     * @param {typeof ITEM_MARKER_STYLES[string]} style
     */
    function swatch(node, style) {
        node.style.background = style.fill;
        if (style.outline) {
            node.style.border = `1.5px solid ${style.outline}`;
            node.style.boxSizing = 'border-box';
        }
        if (style.label) {
            node.textContent = style.label;
            node.style.color = style.textColor || style.outline || '#fff';
        }
    }

    // ─── Trails, follow ─────────────────────────────────────────────────────

    function refreshTrails() {
        trails = trailButtons(Object.keys(s.fullTrails || {}), s.enabledPlayers);
        trailPlayers = trailPlayers.map((p) => ({ ...p, on: !!s.enabledPlayers[p.name] }));
    }

    function trailsAll() {
        for (const name of Object.keys(s.fullTrails)) {
            if (!s.enabledPlayers[name]) s.trailStartTimes[name] = s.currentTime;
            s.enabledPlayers[name] = true;
        }
        refreshTrails();
        redraw();
    }

    function trailsNone() {
        for (const name of Object.keys(s.fullTrails)) s.enabledPlayers[name] = false;
        refreshTrails();
        redraw();
    }

    function trailsClear() {
        for (const name of Object.keys(s.fullTrails)) s.trailStartTimes[name] = s.currentTime;
        redraw();
    }

    /**
     * @param {string} name
     * @param {boolean} on
     */
    function trailOne(name, on) {
        s.enabledPlayers[name] = on;
        if (on) s.trailStartTimes[name] = s.currentTime;
        refreshTrails();
        redraw();
    }

    /** @param {Event & { currentTarget: HTMLSelectElement }} e */
    function setDuration(e) {
        s.trailDuration = parseInt(e.currentTarget.value, 10);
        redraw();
    }

    /** @param {Event & { currentTarget: HTMLSelectElement }} e */
    function setFollow(e) {
        view.setFollowPlayer(e.currentTarget.value || null);
    }

    // ─── Overlays ───────────────────────────────────────────────────────────

    /**
     * @param {keyof typeof flags} key
     * @param {() => boolean} apply
     */
    function toggle(key, apply) {
        flags = { ...flags, [key]: apply() };
        redraw();
    }

    /** @param {'los' | 'pvs'} key */
    async function toggleVis(key) {
        const field = key === 'los' ? 'showLos' : 'showPvs';
        s[field] = !s[field];
        toggle(key, () => s[field]);
        if (s[field] && !s.losComputed && !s.losPending) {
            pendingBtn = key;
            await ensureLineOfSight();
            pendingBtn = null;
        }
    }

    function toggle3D() {
        const on = is3D(view.camera);
        view.setCamera(on ? 0 : DEFAULT_YAW, on ? PITCH_MAX : DEFAULT_PITCH);
    }

    // ─── Learn mode ─────────────────────────────────────────────────────────

    /** @param {boolean} on */
    function onLearn(on) {
        learn = on;
        if (on === s.learnMode) return;
        s.learnMode = on;
        if (on) buildEntities();
        redraw();
    }

    function buildEntities() {
        entityHtml = entityRowsHtml(entityRows(s.mapEntities || [], s.entityFilters, (/** @type {Record<string, any>} */ e) => view.entityCategory(e)));
    }

    /** @param {Event & { currentTarget: HTMLInputElement }} e */
    function filterChange(e) {
        s.entityFilters[/** @type {string} */ (e.currentTarget.dataset.cat)] = e.currentTarget.checked;
        if (s.learnMode) buildEntities();
        redraw();
    }

    // ─── Fullscreen ─────────────────────────────────────────────────────────

    // src/app/fullscreen.js puts the panel in or out (and moves the unified
    // bar); the canvas follows the panel's new size.
    /** @param {string | null} tab */
    function onFullscreen(tab) {
        const on = tab === 'map';
        if (on === fullscreen) return;
        fullscreen = on;
        s.fullscreen = on;
        flushSync();
        resizeMap();
        s.renderDirty = true;
        draw();
    }

    // Outside fullscreen the canvas has a fixed size.
    function onWindowResize() {
        if (!s.fullscreen || resizeRaf !== null) return;
        resizeRaf = requestAnimationFrame(() => {
            resizeRaf = null;
            resizeMap();
            s.renderDirty = true;
            draw();
        });
    }

    // ─── Pointer ────────────────────────────────────────────────────────────

    // The gestures (pan / orbit / click-to-follow or focus / wheel zoom on
    // the cursor) are MvdMap's; this turns browser events into canvas-space
    // CSS pixels. Move and up are on window so a drag tracks off the canvas.
    // Nothing is wired before the first demo loads.
    /** @param {MouseEvent} ev */
    function point(ev) {
        const r = canvas.getBoundingClientRect();
        return { x: ev.clientX - r.left, y: ev.clientY - r.top };
    }

    /** @param {MouseEvent} ev */
    function down(ev) {
        if (!s.initialized || (ev.button !== 0 && ev.button !== 2)) return;
        const p = point(ev);
        view.pointerDown(p.x, p.y, (ev.button === 2 || ev.ctrlKey || ev.metaKey) ? 'orbit' : 'pan', ev.button);
        ev.preventDefault();
    }

    /** @param {MouseEvent} ev */
    function winMove(ev) {
        if (!s.initialized) return;
        const p = point(ev);
        view.pointerMove(p.x, p.y);
    }

    /** @param {MouseEvent} ev */
    function winUp(ev) {
        if (!s.initialized) return;
        const p = point(ev);
        view.pointerUp(p.x, p.y);
    }

    /** @param {WheelEvent} ev */
    function wheel(ev) {
        if (!s.initialized) return;
        ev.preventDefault();
        const p = point(ev);
        view.wheelZoom(p.x, p.y, ev.deltaY);
    }

    /** @param {MouseEvent} ev */
    function dblclick(ev) {
        if (!s.initialized) return;
        ev.preventDefault();
        view.resetView();
    }

    // Escape clears a region focus (and nothing else, so it does not fight
    // leaving fullscreen).
    /** @param {KeyboardEvent} ev */
    function keydown(ev) {
        if (ev.key === 'Escape' && s.focusGroupName) view.setFocusGroup(null);
    }

    onMount(() => {
        view.attach(canvas);
        view.on('follow', () => { if (followSel) followSel.value = s.followPlayer || ''; });
        view.on('camera', () => { tilted = is3D(view.camera); });
        canvas.addEventListener('wheel', wheel, { passive: false });
        const offs = [
            mapData.subscribe(onData),
            changed.subscribe(() => { if (result) redraw(); }),
            learnMode.subscribe(onLearn),
            regionControl.subscribe((rc) => { rcShown = !!rc && rc.editable; }),
            teams.subscribe((t) => { colors = t.colors; }),
            playback.subscribe((p) => { s.isPlaying = p.playing; }),
            clock.subscribe(onClock),
            activeTab.subscribe(onTab),
            fullscreenTab.subscribe(onFullscreen),
        ];
        return () => {
            offs.forEach((off) => off());
            canvas.removeEventListener('wheel', wheel);
        };
    });
</script>

<svelte:window onmousemove={winMove} onmouseup={winUp} onresize={onWindowResize} onkeydown={keydown} />

<div id="tab-map" class="tab-content">
    <div class="empty-state">Load or search a demo to begin.</div>
    <div class="panel map-panel" class:map-panel--fullscreen={fullscreen}>
        <div class="map-main-content">
            <div class="map-canvas-container">
                <canvas id="map-canvas" width="850" height="850" bind:this={canvas}
                    onmousedown={down} oncontextmenu={(e) => e.preventDefault()} ondblclick={dblclick}></canvas>
                <div id="map-no-data" class="map-no-data" style:display={noData ? 'block' : 'none'}>
                    <p>No location data available for this map.</p>
                    <p>Player positions will still be shown.</p>
                </div>
            </div>
            <div class="map-sidebar">
                <div class="map-legend" id="map-legend" bind:this={legendEl} style:display={learn ? 'none' : null}>
                    <h4>Players</h4>
                </div>
                <div id="map-entities-panel" class="map-entities-panel" style:display={learn ? null : 'none'}>
                    <h4>Map entities</h4>
                    <div class="map-entity-filters">
                        <label class="map-entity-filter"><input type="checkbox" class="map-entity-cb" data-cat="weapon" checked onchange={filterChange}><span class="map-entity-swatch" style="background:#ff6b6b"></span>Weapons</label>
                        <label class="map-entity-filter"><input type="checkbox" class="map-entity-cb" data-cat="armor" checked onchange={filterChange}><span class="map-entity-swatch" style="background:#ffc107"></span>Armor</label>
                        <label class="map-entity-filter"><input type="checkbox" class="map-entity-cb" data-cat="health" checked onchange={filterChange}><span class="map-entity-swatch" style="background:#00c853"></span>Health</label>
                        <label class="map-entity-filter"><input type="checkbox" class="map-entity-cb" data-cat="ammo" checked onchange={filterChange}><span class="map-entity-swatch" style="background:#b0a070"></span>Ammo</label>
                        <label class="map-entity-filter"><input type="checkbox" class="map-entity-cb" data-cat="powerup" checked onchange={filterChange}><span class="map-entity-swatch" style="background:#2196f3"></span>Powerups</label>
                        <label class="map-entity-filter"><input type="checkbox" class="map-entity-cb" data-cat="teleporter" checked onchange={filterChange}><span class="map-entity-swatch" style="background:#b388ff"></span>Teleporters</label>
                        <label class="map-entity-filter"><input type="checkbox" class="map-entity-cb" data-cat="spawn" onchange={filterChange}><span class="map-entity-swatch" style="background:#888"></span>Spawns</label>
                        <label class="map-entity-filter"><input type="checkbox" class="map-entity-cb" data-cat="button" onchange={filterChange}><span class="map-entity-swatch" style="background:#ff9800"></span>Buttons</label>
                        <label class="map-entity-filter"><input type="checkbox" class="map-entity-cb" data-cat="door" onchange={filterChange}><span class="map-entity-swatch" style="background:#a1887f"></span>Doors</label>
                    </div>
                    <p class="map-entity-hint">Static map layout. Arrows link teleport entrance → exit.</p>
                </div>
                <div class="map-sidebar-row">
                    <div id="map-items-panel" class="map-items-panel" style:display={!learn && items.length > 0 ? null : 'none'}>
                        <h4>Items</h4>
                        <table class="map-items-table" id="map-items-table">
                            <tbody id="map-items-body">
                                {#each items as item, i (item)}
                                    <tr class:taken={itemCells[i]?.taken}>
                                        <td class="item-swatch-cell"><span class="item-swatch" use:swatch={ITEM_MARKER_STYLES[item.kind]}></span></td>
                                        <td class="item-loc">{item.loc || ''}</td>
                                        <td class={itemCells[i]?.cls ?? 'item-status'}>{itemCells[i]?.text ?? ''}</td>
                                    </tr>
                                {/each}
                            </tbody>
                        </table>
                    </div>
                    <div id="region-status-panel" style:display={!learn && rcShown ? null : 'none'}>
                        <h4>Region Status</h4>
                        <div id="region-status-body" bind:this={statusEl}></div>
                    </div>
                </div>
            </div>
        </div>
        <div class="map-track-controls">
            <button id="map-learn-toggle" class="map-learn-toggle" class:active={learn} title="Study the map's item & teleporter layout"
                style:display={hasEntities ? null : 'none'} onclick={() => learnMode.set(!learnMode.get())}>Learn map</button>
            <span class="map-track-sep" aria-hidden="true"></span>
            <span class="map-trail-label">Trails:</span>
            <button id="map-trails-all" class:active={trails.all} onclick={trailsAll}>All</button>
            <button id="map-trails-none" class:active={trails.none} onclick={trailsNone}>None</button>
            <details id="map-trails-dropdown" class="map-trails-dropdown">
                <summary class:active={trails.some}>Players</summary>
                <div id="map-trails-players" class="map-trails-players-panel">
                    {#each trailPlayers as p (p.name)}
                        <label><input type="checkbox" class="map-player-trail-cb" data-player={p.name} checked={p.on}
                            onchange={(e) => trailOne(p.name, e.currentTarget.checked)}><span class="map-trails-player-name" style:color={p.color}>{p.name}</span></label>
                    {/each}
                </div>
            </details>
            <select id="map-trail-duration" class="map-trail-select" onchange={setDuration}>
                <option value="5">5s</option>
                <option value="10" selected>10s</option>
                <option value="30">30s</option>
                <option value="60">1min</option>
                <option value="300">5min</option>
                <option value="1200">20min</option>
            </select>
            <button id="map-reset-tracks" onclick={trailsClear}>Clear</button>
            <span class="map-track-sep" aria-hidden="true"></span>
            <span class="map-trail-label">Follow:</span>
            <select id="map-follow" class="map-trail-select" bind:this={followSel} onchange={setFollow}>
                {#if result}
                    <option value="">Off</option>
                    {#each followNames as n (n)}<option value={n}>{n}</option>{/each}
                {/if}
            </select>
            <button id="map-view-arrows" class:active={flags.view} title="Per-player arrows showing where each player is looking"
                onclick={() => toggle('view', () => (s.showViewArrows = !s.showViewArrows))}>View</button>
            <button id="map-vel-arrows" class:active={flags.vel} title="Per-player arrows showing each player's velocity (length ∝ speed)"
                onclick={() => toggle('vel', () => (s.showVelArrows = !s.showVelArrows))}>Vel</button>
            <button id="map-los" class:active={flags.los} class:pending={pendingBtn === 'los'} title="Line-of-sight: lines between players who can see each other (white = mutual, red/blue = one-way). BSP-backed maps only."
                onclick={() => toggleVis('los')}>LOS</button>
            <button id="map-pvs" class:active={flags.pvs} class:pending={pendingBtn === 'pvs'} title="Potential visibility (PVS): thin lines between players the server would have sent to each other on the wire (mvdsv per-client entity cull — fat PVS vs the opponent's box leaves). Superset of LOS; the gap (in PVS but not LOS) means same vis region without a direct sightline. BSP-backed maps only."
                onclick={() => toggleVis('pvs')}>PVS</button>
            <button id="map-3d-toggle" class:active={tilted} title="Tilt the map into 3D — right-drag (or Ctrl+drag) to rotate freely" onclick={toggle3D}>3D</button>
            <button id="map-occlude" class:active={flags.occl} title="Realistic occlusion — floors hide players, items and lines behind them (the default keeps everything visible)"
                onclick={() => toggle('occl', () => (s.occludeActors = !s.occludeActors))}>Occl</button>
            <button id="map-reset-view" title="Reset pan / zoom / rotation" onclick={() => view.resetView()}>Reset view</button>
        </div>
        <div id="map-entities-table-wrap" class="map-entities-table-wrap" style:display={learn ? null : 'none'}>
            <table class="stats-table">
                <thead>
                    <tr><th>Class</th><th>Type</th><th>Name</th><th>Loc</th><th>Destination</th></tr>
                </thead>
                <tbody id="map-entities-table-body">{@html entityHtml}</tbody>
            </table>
        </div>
    </div>
</div>
