<script>
    // The map, following the player you watch. A second MvdMap with its own
    // canvas, camera and follow target; the map DATA (geometry, locs,
    // frames, items, trails…) is the Map tab's (src/tabs/map/map-model.js),
    // read from it each frame (syncMapData). Click a player on the map
    // to watch them; drag pans, right-drag orbits, the wheel zooms,
    // double-click resets the view. A right-click (or the sliders button)
    // opens the overlay menu — the Map tab's view / velocity arrows,
    // trails, line of sight, PVS, occlusion and 3D — kept in the pane's
    // options (layout.js).
    import Crosshair from '@lucide/svelte/icons/crosshair';
    import SlidersHorizontal from '@lucide/svelte/icons/sliders-horizontal';
    import {
        MvdMap, DEFAULT_YAW, DEFAULT_PITCH, PITCH_MAX, is3D,
    } from '../../../../mvd-map-view/src/index.js';
    import { clock, demo, teams } from '../../state/index.js';
    import { view as mapTabView, ensureLineOfSight } from '../../tabs/map/map-model.js';
    import { engine } from '../index.js';
    import { paneLayout, paneOpt, setPaneOpt } from '../layout.js';
    import { onFrame } from './tick.js';
    import { syncMapData } from './map-sync.js';

    const MIN_H = 200;
    const MAX_H = 520;

    /** @type {HTMLCanvasElement} */
    let canvas;
    /** @type {HTMLDivElement} */
    let box;
    let empty = $state(false);
    let follow = $derived(paneOpt($paneLayout, 'map', 'follow', true));
    // The overlays, as the Map tab names them in its toolbar.
    let opts = $derived({
        view: paneOpt($paneLayout, 'map', 'view', false),
        vel: paneOpt($paneLayout, 'map', 'vel', false),
        los: paneOpt($paneLayout, 'map', 'los', false),
        pvs: paneOpt($paneLayout, 'map', 'pvs', false),
        occl: paneOpt($paneLayout, 'map', 'occl', false),
        trails: paneOpt($paneLayout, 'map', 'trails', 'none'),
    });
    let tilted = $state(false);
    /** @type {{ x: number, y: number } | null} */
    let menu = $state(null);
    // Null only while unmounted, when no handler or frame runs.
    /** @type {MvdMap} */
    let map = /** @type {any} */ (null);
    let setting = false;
    /** @type {string | null} */
    let lastPov = null;       // the POV the camera last followed
    let needsFit = true;      // a new demo: reset the view once its data is in

    const sized = $derived($paneLayout.heights.map != null);

    /** @param {string | null} name */
    function setFollow(name) {
        setting = true;
        try { map.setFollowPlayer(name); } finally { setting = false; }
    }

    // draw syncs the Map tab's data in and renders at the clock's time.
    function draw() {
        if (!map) return;
        const src = mapTabView;
        empty = !src.state.initialized;
        if (empty) return;
        if (syncMapData(src.state, map.state) || needsFit) {
            // The world's mid height comes from the loc z-range the Map
            // tab's camera was given.
            map.camera.zMidDefault = src.camera.zMidDefault;
            if (needsFit) {
                needsFit = false;
                lastPov = null;
                map.resetView();
            }
            fitCanvas();
        }
        map.teamColors = $teams.colors;
        applyOptions();
        map.state.currentTime = clock.t;
        map.state.renderDirty = true;
        // Re-follow only when the POV changes: a pan, or a click that
        // unfollows, holds until the next POV switch.
        const pov = engine.povName();
        if (follow && pov !== lastPov) {
            lastPov = pov;
            if (pov) setFollow(pov);
        }
        map.render(clock.t);
    }

    // The overlay options onto the map's own state, every frame (cheap): the
    // watched player's trail follows a POV switch.
    function applyOptions() {
        const s = map.state;
        s.showViewArrows = opts.view;
        s.showVelArrows = opts.vel;
        s.occludeActors = opts.occl;
        s.showLos = opts.los;
        s.showPvs = opts.pvs;
        const pov = engine.povName();
        /** @type {Record<string, boolean>} */
        const on = {};
        if (opts.trails === 'all') for (const name of Object.keys(s.fullTrails || {})) on[name] = true;
        else if (opts.trails === 'pov' && pov) on[pov] = true;
        s.enabledPlayers = on;
    }

    // Width from the pane; height from the pane when the user sized it,
    // else the world's aspect, capped so the column stays scannable.
    // Resizing clears the canvas, so callers draw after.
    function fitCanvas() {
        if (!map || !box || !box.clientWidth) return;
        const cssW = Math.max(200, Math.floor(box.clientWidth));
        const s = map.state;
        const worldW = s.bounds.maxX - s.bounds.minX;
        const worldH = s.bounds.maxY - s.bounds.minY;
        const cssH = sized
            ? Math.max(MIN_H, Math.floor(box.clientHeight))
            : worldW > 0 ? Math.round(Math.max(MIN_H, Math.min(MAX_H, cssW * (worldH / worldW)))) : cssW;
        map.resize(cssW, cssH, window.devicePixelRatio || 1);
    }

    function resize() {
        fitCanvas();
        draw();
    }

    $effect(() => {
        map = new MvdMap(canvas);
        map.on('follow', (/** @type {string | null} */ name) => {
            if (setting || !name) return;
            engine.trackName(name);
        });
        map.on('camera', () => { tilted = is3D(map.camera); });
        const ro = new ResizeObserver(() => resize());
        ro.observe(box);
        const offFrame = onFrame(() => draw());
        return () => {
            offFrame();
            ro.disconnect();
            map.dispose();
            map = /** @type {any} */ (null);
        };
    });

    // A new demo: drop the old follow target and trail starts; the view
    // resets on its data.
    $effect(() => {
        $demo;
        needsFit = true;
        if (map) {
            setFollow(null);
            map.state.trailStartTimes = {};
        }
    });

    // Line of sight / PVS on: the Map tab's lazy pass, run once per demo.
    $effect(() => {
        if ((opts.los || opts.pvs) && $demo) ensureLineOfSight().then(() => draw());
    });

    $effect(() => {
        sized;
        resize();
    });

    /** @param {MouseEvent} ev */
    function point(ev) {
        const r = canvas.getBoundingClientRect();
        return { x: ev.clientX - r.left, y: ev.clientY - r.top };
    }

    /** @param {MouseEvent} ev */
    function down(ev) {
        if (ev.button !== 0 && ev.button !== 2) return;
        menu = null;
        const p = point(ev);
        map.pointerDown(p.x, p.y, (ev.button === 2 || ev.ctrlKey || ev.metaKey) ? 'orbit' : 'pan', ev.button);
        ev.preventDefault();
        const move = (/** @type {MouseEvent} */ e) => { const q = point(e); map.pointerMove(q.x, q.y); };
        const up = (/** @type {MouseEvent} */ e) => {
            const q = point(e);
            map.pointerUp(q.x, q.y);
            // A right-click that did not drag (an orbit) opens the menu.
            if (ev.button === 2 && Math.hypot(q.x - p.x, q.y - p.y) < 4) openMenu(q);
            window.removeEventListener('mousemove', move);
            window.removeEventListener('mouseup', up);
        };
        window.addEventListener('mousemove', move);
        window.addEventListener('mouseup', up);
    }

    /** @param {WheelEvent} ev */
    function wheel(ev) {
        ev.preventDefault();
        const p = point(ev);
        map.wheelZoom(p.x, p.y, ev.deltaY);
    }

    function toggleFollow() {
        const on = !follow;
        setPaneOpt('map', 'follow', on);
        lastPov = null;
        if (!on) setFollow(null);
        draw();
    }

    /** @param {{ x: number, y: number }} at  canvas coordinates */
    function openMenu(at) {
        // Kept inside the pane: the menu is about 190 x 260 px.
        menu = {
            x: Math.max(4, Math.min(at.x, box.clientWidth - 194)),
            y: Math.max(4, Math.min(at.y, box.clientHeight - 264)),
        };
    }

    /** @param {string} key */
    function toggleOpt(key) {
        setPaneOpt('map', key, !(/** @type {Record<string, unknown>} */ (opts))[key]);
        draw();
    }

    /** @param {string} mode */
    function setTrails(mode) {
        setPaneOpt('map', 'trails', mode);
        draw();
    }

    function toggle3D() {
        const on = is3D(map.camera);
        map.setCamera(on ? 0 : DEFAULT_YAW, on ? PITCH_MAX : DEFAULT_PITCH);
        draw();
    }

    // Outside clicks and Escape close the menu.
    $effect(() => {
        if (!menu) return;
        const close = (/** @type {Event} */ e) => {
            if (e instanceof KeyboardEvent ? e.key === 'Escape' : !(/** @type {Element} */ (e.target)).closest?.('.map-menu, .menu-btn')) menu = null;
        };
        window.addEventListener('pointerdown', close, true);
        window.addEventListener('keydown', close);
        return () => {
            window.removeEventListener('pointerdown', close, true);
            window.removeEventListener('keydown', close);
        };
    });
</script>

<div class="map-pane" class:sized bind:this={box}>
    <span class="map-pane-btns">
        <button class="pt-btn icon menu-btn" class:active={!!menu} title="Map options (or right-click the map)"
            onclick={() => (menu = menu ? null : { x: Math.max(4, box.clientWidth - 198), y: 36 })}><SlidersHorizontal size={15} /></button>
        <button class="pt-btn icon" class:active={follow} aria-pressed={follow}
            title={follow ? 'Following the POV — click to stop' : 'Keep the map centred on the player you are watching'}
            onclick={toggleFollow}><Crosshair size={15} /></button>
    </span>
    {#if menu}
        <div class="map-menu" role="menu" style:left="{menu.x}px" style:top="{menu.y}px">
            <button role="menuitemcheckbox" aria-checked={follow} onclick={toggleFollow}><span class="tick">{follow ? '✓' : ''}</span>Follow the watched player</button>
            <hr>
            <button role="menuitemcheckbox" aria-checked={opts.view} onclick={() => toggleOpt('view')}
                title="Per-player arrows showing where each player is looking"><span class="tick">{opts.view ? '✓' : ''}</span>View arrows</button>
            <button role="menuitemcheckbox" aria-checked={opts.vel} onclick={() => toggleOpt('vel')}
                title="Per-player arrows showing each player's velocity (length ∝ speed)"><span class="tick">{opts.vel ? '✓' : ''}</span>Velocity arrows</button>
            <button role="menuitemcheckbox" aria-checked={opts.los} onclick={() => toggleOpt('los')}
                title="Lines between players who can see each other (white = mutual, red/blue = one-way). BSP-backed maps only."><span class="tick">{opts.los ? '✓' : ''}</span>Line of sight</button>
            <button role="menuitemcheckbox" aria-checked={opts.pvs} onclick={() => toggleOpt('pvs')}
                title="Thin lines between players the server sent to each other (potential visibility). BSP-backed maps only."><span class="tick">{opts.pvs ? '✓' : ''}</span>PVS</button>
            <button role="menuitemcheckbox" aria-checked={opts.occl} onclick={() => toggleOpt('occl')}
                title="Floors hide players, items and lines behind them"><span class="tick">{opts.occl ? '✓' : ''}</span>Occlusion</button>
            <hr>
            {#each [['none', 'No trails'], ['pov', 'Trail of the watched player'], ['all', 'Trails of everyone']] as [mode, label] (mode)}
                <button role="menuitemradio" aria-checked={opts.trails === mode} onclick={() => setTrails(mode)}><span class="tick">{opts.trails === mode ? '•' : ''}</span>{label}</button>
            {/each}
            <hr>
            <button role="menuitemcheckbox" aria-checked={tilted} onclick={toggle3D}><span class="tick">{tilted ? '✓' : ''}</span>3D (right-drag rotates)</button>
            <button role="menuitem" onclick={() => { map?.resetView(); menu = null; }}><span class="tick"></span>Reset view</button>
        </div>
    {/if}
    <canvas bind:this={canvas} onmousedown={down} onwheel={wheel} oncontextmenu={(e) => e.preventDefault()}
        ondblclick={() => map?.resetView()}></canvas>
    {#if empty}<div class="player-clip-hint">No map for this demo.</div>{/if}
</div>

<style>
    .map-pane {
        position: relative;
    }
    .map-pane.sized {
        height: 100%;
    }
    canvas {
        display: block;
        cursor: grab;
    }
    .map-pane-btns {
        position: absolute;
        top: 6px;
        right: 6px;
        z-index: 1;
        display: inline-flex;
        gap: 4px;
    }
    .map-pane-btns .pt-btn {
        background: rgba(10, 14, 26, 0.7);
    }
    .map-menu {
        position: absolute;
        z-index: 2;
        min-width: 190px;
        display: flex;
        flex-direction: column;
        padding: 4px 0;
        background: #10162a;
        border: 1px solid var(--bg-panel);
        border-radius: 4px;
        box-shadow: 0 6px 18px rgba(0, 0, 0, 0.5);
        font-size: 12px;
    }
    .map-menu button {
        display: flex;
        align-items: center;
        gap: 4px;
        padding: 4px 10px 4px 6px;
        background: none;
        border: 0;
        color: var(--fg-default);
        font: inherit;
        text-align: left;
        cursor: pointer;
    }
    .map-menu button:hover {
        background: #34507a;
    }
    .map-menu .tick {
        width: 14px;
        color: var(--accent-cyan);
    }
    .map-menu hr {
        width: 100%;
        margin: 3px 0;
        border: 0;
        border-top: 1px solid var(--bg-panel);
    }
</style>
