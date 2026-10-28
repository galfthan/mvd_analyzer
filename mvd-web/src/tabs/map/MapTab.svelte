<script>
    // The Map tab: the map canvas (mvd-map-view's MvdMap, map-model.js), its
    // sidebar — the legend, the items list, the region status, the learn-mode
    // entity filters — and the controls under it (trails, follow, the
    // overlays, 3D / fog / light / occlusion, the perf HUD and benchmark,
    // reset view, fullscreen). src/lib/map.js has the pure parts, sidebar.js
    // the hand-built legend and region status. app.js still toggles the
    // `active` class like on every other .tab-content (onTabActivated).
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
        DEFAULT_YAW, DEFAULT_PITCH, PITCH_MAX, is3D, ITEM_MARKER_STYLES, benchCameraAt, refreshTrig, summarizeBench,
    } from '../../../../mvd-map-view/src/index.js';
    import { teams, activeTab, clock, regionControl, playback, stopPlayback } from '../../state/index.js';
    import {
        panelItems, itemStatusCell, entityRows, entityRowsHtml, trailButtons, byName, perfHudText, benchDemoKey, benchText,
    } from '../../lib/map.js';
    import { view, mapData, changed, learnMode, resizeMap, ensureLineOfSight } from './map-model.js';
    import { buildLegend, syncLegend, syncRegionStatus } from './sidebar.js';

    const TAB = 'map';
    const BENCH_HISTORY_KEY = 'mvd-map-bench-history';
    const s = view.state;

    let canvas;
    let panel;
    let legendEl;
    let statusEl;
    let followSel;

    let result = $state.raw(null);
    let learn = $state(false);
    let rcShown = $state(false);
    let fullscreen = $state(false);
    let hasEntities = $state(false);
    let noData = $state(false);
    let tilted = $state(false);
    let flags = $state({ view: false, vel: false, los: false, pvs: false, fog: false, light: false, occl: false });
    let pendingBtn = $state(null);
    let trails = $state.raw({ all: false, none: true, some: false });
    let trailPlayers = $state.raw([]);
    let followNames = $state.raw([]);
    let items = $state.raw([]);
    let itemCells = $state.raw([]);
    let entityHtml = $state('');
    let hudShown = $state(false);
    let hudText = $state('');
    let hudOn = $state(false);
    let benchOn = $state(false);

    let colors = [];
    let icons = new Map();
    let queued = false;
    let sidebarDirty = true;
    let lastFull = 0;
    let samples = [];
    let hudTimer = null;
    let hudHideTimer = null;
    let fsHome = null;
    let resizeRaf = null;

    const shown = () => activeTab.get() === TAB;

    // ─── Drawing ────────────────────────────────────────────────────────────

    // A frame the redraw-skip elided leaves lastFrameStats as it was: the
    // unchanged timestamp dedupes it, so the HUD counts presented frames.
    function sample() {
        if (!hudOn && !benchOn) return;
        const fs = view.lastFrameStats;
        if (!fs) return;
        if (samples.length > 0 && samples[samples.length - 1].t === fs.t) return;
        samples.push(fs);
        if (samples.length > 600) samples.splice(0, samples.length - 300);
    }

    // Nothing to draw before the first demo.
    function draw() {
        if (!s.initialized) return;
        view.render(s.currentTime);
        sample();
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

    function onTab(tab) {
        if (tab !== TAB) {
            stopHudTimer();
            return;
        }
        if (hudOn) startHudTimer();
        // Run once app.js has made the tab visible (it follows the same
        // store, after this), then again on the next frame and 120 ms later.
        queueMicrotask(reveal);
        requestAnimationFrame(reveal);
        setTimeout(reveal, 120);
    }

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
        items = panelItems(result.items?.items, ITEM_MARKER_STYLES);
        refreshTrails();
        flushSync();
        followSel.value = s.followPlayer || '';
        syncItems(s.currentTime);
        sidebarDirty = true;
        redraw();
    }

    // ─── Items ──────────────────────────────────────────────────────────────

    function syncItems(t) {
        itemCells = items.map((it) => {
            const st = view.itemStatus(it, t);
            return { ...itemStatusCell(st), taken: !st.up };
        });
    }

    // The swatch mirrors the item's map marker: solid armour squares, black
    // squares outlined in the item's colour for the rest.
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

    function trailOne(name, on) {
        s.enabledPlayers[name] = on;
        if (on) s.trailStartTimes[name] = s.currentTime;
        refreshTrails();
        redraw();
    }

    function setDuration(e) {
        s.trailDuration = parseInt(e.currentTarget.value, 10);
        redraw();
    }

    function setFollow(e) {
        view.setFollowPlayer(e.currentTarget.value || null);
    }

    // ─── Overlays ───────────────────────────────────────────────────────────

    function toggle(key, apply) {
        flags = { ...flags, [key]: apply() };
        redraw();
    }

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

    function onLearn(on) {
        learn = on;
        if (on === s.learnMode) return;
        s.learnMode = on;
        if (on) buildEntities();
        redraw();
    }

    function buildEntities() {
        entityHtml = entityRowsHtml(entityRows(s.mapEntities || [], s.entityFilters, (e) => view.entityCategory(e)));
    }

    function filterChange(e) {
        s.entityFilters[e.currentTarget.dataset.cat] = e.currentTarget.checked;
        if (s.learnMode) buildEntities();
        redraw();
    }

    // ─── Perf HUD and benchmark ─────────────────────────────────────────────

    function hudTick() {
        if (!benchOn) hudText = perfHudText(samples, performance.now());
    }

    function startHudTimer() {
        if (!hudTimer) hudTimer = setInterval(hudTick, 250);
    }

    function stopHudTimer() {
        clearInterval(hudTimer);
        hudTimer = null;
    }

    function toggleHud() {
        hudOn = !hudOn;
        hudShown = hudOn;
        view.perfCapture = hudOn || benchOn;
        if (hudOn) {
            samples = [];
            hudTick();
            startHudTimer();
            // Seed a sample so the HUD is live before the user touches anything.
            s.renderDirty = true;
            draw();
        } else {
            stopHudTimer();
        }
    }

    function loadBenchHistory() {
        try {
            const list = JSON.parse(localStorage.getItem(BENCH_HISTORY_KEY) || '[]');
            return Array.isArray(list) ? list : [];
        } catch {
            return [];
        }
    }

    function saveBenchHistory(list) {
        try {
            localStorage.setItem(BENCH_HISTORY_KEY, JSON.stringify(list.slice(-30)));
        } catch { /* storage unavailable — the console log still has the run */ }
    }

    // runBench plays the demo at 1× from a fixed early time while the camera
    // runs a fixed 18 s spin / tilt / zoom script (benchCameraAt), then
    // reports fps and frame-time percentiles, against earlier runs of the
    // same demo at the same surface size (localStorage).
    function runBench() {
        if (benchOn || !result) return;
        if (playback.get().playing) stopPlayback();
        const cam = view.camera;
        const saved = {
            yaw: cam.yaw, pitch: cam.pitch, zoomK: cam.zoomK, panX: cam.panX, panY: cam.panY,
            cx: cam.cx, cy: cam.cy, zMid: cam.zMid,
            time: s.currentTime, follow: s.followPlayer, focus: s.focusGroupName,
        };
        if (saved.follow) view.setFollowPlayer(null);
        if (saved.focus) view.setFocusGroup(null);
        cam.panX = 0;
        cam.panY = 0;
        view.refit();
        cam.zMid = cam.zMidDefault || 0;
        const duration = Number.isFinite(clock.duration) ? clock.duration : 0;
        const t0 = Math.min(30, (duration || 600) * 0.25);
        benchOn = true;
        view.perfCapture = true;
        clearTimeout(hudHideTimer);
        hudShown = true;
        hudText = 'benchmarking… (18 s)';
        const runSamples = [];
        const start = performance.now();
        let lastTick = start;
        const step = () => {
            if (!benchOn) return;
            const now = performance.now();
            const elapsed = (now - start) / 1000;
            const pose = benchCameraAt(elapsed);
            if (pose.done) {
                finishBench(saved, runSamples);
                return;
            }
            // The angles directly, not through setCamera: its cardinal yaw
            // snap would freeze the sweep at every 90° crossing.
            cam.yaw = pose.yaw;
            cam.pitch = pose.pitch;
            refreshTrig(cam);
            cam.zoomK = pose.zoomK;
            cam.panX = 0;
            cam.panY = 0;
            s.currentTime = t0 + elapsed;
            s.renderDirty = true;
            draw();
            const fs = view.lastFrameStats;
            if (fs) runSamples.push(Object.assign({ dtMs: now - lastTick }, fs));
            lastTick = now;
            requestAnimationFrame(step);
        };
        requestAnimationFrame(step);
    }

    function finishBench(saved, runSamples) {
        const summary = summarizeBench(runSamples);
        const noFrames = summary.frames === 0;
        const record = Object.assign({
            demo: benchDemoKey(result, teams.get().order, Number.isFinite(clock.duration) ? clock.duration : 0),
            px: canvas ? `${canvas.width}x${canvas.height}` : '?',
            date: new Date().toISOString(),
        }, summary);
        let prior = [];
        if (!noFrames) {
            // No GL frames presented (context lost / no geometry): not a run.
            console.log('[map-bench] ' + JSON.stringify(record));
            const history = loadBenchHistory();
            prior = history.filter((r) => r.demo === record.demo && r.px === record.px);
            saveBenchHistory(history.concat([record]));
        }
        const cam = view.camera;
        benchOn = false;
        view.perfCapture = hudOn;
        Object.assign(cam, { cx: saved.cx, cy: saved.cy, zMid: saved.zMid, zoomK: saved.zoomK, panX: saved.panX, panY: saved.panY });
        if (saved.focus) view.setFocusGroup(saved.focus);
        if (saved.follow) view.setFollowPlayer(saved.follow);
        view.setCamera(saved.yaw, saved.pitch);
        clock.seek(saved.time);
        hudShown = true;
        if (noFrames) {
            hudText = 'bench: no GL frames rendered';
            return;
        }
        hudText = benchText(record, summary, prior[prior.length - 1]);
        if (!hudOn) {
            // Leave the results up a while; the FPS toggle owns the HUD otherwise.
            hudHideTimer = setTimeout(() => {
                if (!hudOn && !benchOn) hudShown = false;
            }, 30000);
        }
    }

    // ─── Fullscreen ─────────────────────────────────────────────────────────

    function toggleFullscreen() {
        if (document.fullscreenElement === panel) document.exitFullscreen().catch(() => {});
        else panel.requestFullscreen?.()?.catch(() => {});
    }

    // Whoever triggered it (the button, Escape, the browser): the unified bar
    // moves into the fullscreen panel so playback stays usable, and back.
    function onFullscreenChange() {
        const on = document.fullscreenElement === panel;
        fullscreen = on;
        s.fullscreen = on;
        const tl = document.getElementById('unified-timeline');
        if (tl) {
            if (on) {
                if (!fsHome) fsHome = { parent: tl.parentNode, next: tl.nextSibling };
                panel.insertBefore(tl, panel.firstChild);
            } else if (fsHome?.parent) {
                fsHome.parent.insertBefore(tl, fsHome.next);
            }
        }
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
    function point(ev) {
        const r = canvas.getBoundingClientRect();
        return { x: ev.clientX - r.left, y: ev.clientY - r.top };
    }

    function down(ev) {
        if (!s.initialized || (ev.button !== 0 && ev.button !== 2)) return;
        const p = point(ev);
        view.pointerDown(p.x, p.y, (ev.button === 2 || ev.ctrlKey || ev.metaKey) ? 'orbit' : 'pan', ev.button);
        ev.preventDefault();
    }

    function winMove(ev) {
        if (!s.initialized) return;
        const p = point(ev);
        view.pointerMove(p.x, p.y);
    }

    function winUp(ev) {
        if (!s.initialized) return;
        const p = point(ev);
        view.pointerUp(p.x, p.y);
    }

    function wheel(ev) {
        if (!s.initialized) return;
        ev.preventDefault();
        const p = point(ev);
        view.wheelZoom(p.x, p.y, ev.deltaY);
    }

    function dblclick(ev) {
        if (!s.initialized) return;
        ev.preventDefault();
        view.resetView();
    }

    // Escape clears a region focus (and nothing else, so it does not fight
    // leaving fullscreen).
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
        ];
        document.addEventListener('fullscreenchange', onFullscreenChange);
        return () => {
            offs.forEach((off) => off());
            canvas.removeEventListener('wheel', wheel);
            document.removeEventListener('fullscreenchange', onFullscreenChange);
            stopHudTimer();
        };
    });
</script>

<svelte:window onmousemove={winMove} onmouseup={winUp} onresize={onWindowResize} onkeydown={keydown} />

<div id="tab-map" class="tab-content">
    <div class="empty-state">Load or search a demo to begin.</div>
    <div class="panel map-panel" class:map-panel--fullscreen={fullscreen} bind:this={panel}>
        <div class="map-main-content">
            <div class="map-canvas-container">
                <canvas id="map-canvas" width="850" height="850" bind:this={canvas}
                    onmousedown={down} oncontextmenu={(e) => e.preventDefault()} ondblclick={dblclick}></canvas>
                <div id="map-perf-hud" class="map-perf-hud" style:display={hudShown ? null : 'none'}>{hudText}</div>
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
            <button id="map-fog" class:active={flags.fog} title="Depth fog — the far side of the map fades into the dark (reads best in a tilted view)"
                onclick={() => toggle('fog', () => (s.fog = s.fog > 0 ? 0 : 1) > 0)}>Fog</button>
            <button id="map-light" class:active={flags.light} title="Directional shading on the floors — tops brighten, sides fall off by facing"
                onclick={() => toggle('light', () => (s.worldLight = s.worldLight > 0 ? 0 : 0.6) > 0)}>Light</button>
            <button id="map-occlude" class:active={flags.occl} title="Realistic occlusion — floors hide players, items and lines behind them (the default keeps everything visible)"
                onclick={() => toggle('occl', () => (s.occludeActors = !s.occludeActors))}>Occl</button>
            <button id="map-perf" class:active={hudOn} title="Render performance overlay — fps, frame-time split (CPU build / GL submit / blit / GPU) and per-frame draw stats" onclick={toggleHud}>FPS</button>
            <button id="map-bench" class:active={benchOn} title="Map render benchmark — plays the demo while the camera runs a fixed 18 s spin / tilt / zoom script, then reports fps and frame-time percentiles (logged to the console; compared against earlier runs of the same demo)" onclick={runBench}>Bench</button>
            <button id="map-reset-view" title="Reset pan / zoom / rotation" onclick={() => view.resetView()}>Reset view</button>
            <button id="map-fullscreen" class:active={fullscreen} title="Toggle fullscreen" onclick={toggleFullscreen}>Fullscreen</button>
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
