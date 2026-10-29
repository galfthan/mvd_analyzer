<script>
    // The map, following the player you watch (the engine's POV, or with no
    // engine — the Map preset — the follow target, follow.js). An MvdMap with its own
    // canvas, camera and follow target; the map DATA (geometry, locs,
    // frames, items, trails…) is the Map tab's (src/tabs/map/map-model.js),
    // read from it each frame (syncMapData). Click a player on the map
    // to watch them; drag pans, right-drag orbits, the wheel zooms,
    // double-click resets the view. A right-click (or the sliders button)
    // opens the overlay menu (MapMenu) — view / velocity
    // arrows, trails, line of sight, PVS, occlusion, fog and 3D — kept in
    // the pane's options (layout.js); with no engine the menu also picks
    // whom to follow, and it holds the Map tab's trail length, clear and
    // learn-the-map. In the main column the map may be as tall as the
    // window. Escape clears a region focus.
    import Crosshair from '@lucide/svelte/icons/crosshair';
    import SlidersHorizontal from '@lucide/svelte/icons/sliders-horizontal';
    import {
        MvdMap, DEFAULT_YAW, DEFAULT_PITCH, PITCH_MAX, is3D,
    } from '../../../../mvd-map-view/src/index.js';
    import { clock, demo, teams } from '../../state/index.js';
    import { view as mapTabView, ensureLineOfSight, learnMode, changed } from '../../tabs/map/map-model.js';
    import { mapFollow } from '../follow.js';
    import { playback } from '../../state/index.js';
    import { engine } from '../index.js';
    import { paneLayout, paneOpt, setPaneOpt } from '../layout.js';
    import { onFrame } from './tick.js';
    import { syncMapData } from './map-sync.js';
    import MapMenu from '../../ui/MapMenu.svelte';

    const engineStatus = engine.status;

    /** @type {{ column?: import('../layout.js').Column }} */
    let { column = 'order' } = $props();

    const MIN_H = 200;
    // Beside the main column the map stays scannable; in it, it may fill
    // the window.
    const maxH = () => (column === 'below' ? Math.max(520, Math.round(window.innerHeight * 0.85)) : 520);
    // The Map tab's trail lengths, seconds (0: the whole match so far).
    const TRAIL_LENGTHS = [[5, '5 s'], [10, '10 s'], [30, '30 s'], [60, '1 min'], [300, '5 min'], [0, 'Whole match']];

    /** @type {HTMLCanvasElement} */
    let canvas;
    /** @type {HTMLDivElement} */
    let box;
    let empty = $state(false);
    let follow = $derived(paneOpt($paneLayout, 'map', 'follow', true));
    // The overlays and trails, the pane's options.
    let opts = $derived({
        view: paneOpt($paneLayout, 'map', 'view', false),
        vel: paneOpt($paneLayout, 'map', 'vel', false),
        los: paneOpt($paneLayout, 'map', 'los', false),
        pvs: paneOpt($paneLayout, 'map', 'pvs', false),
        occl: paneOpt($paneLayout, 'map', 'occl', false),
        fog: paneOpt($paneLayout, 'map', 'fog', false),
        trails: paneOpt($paneLayout, 'map', 'trails', 'none'),
        len: paneOpt($paneLayout, 'map', 'len', 10),
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
    /** @type {string | null | undefined} */
    let lastFollow;           // the follow target, without an engine
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
        if (engine.drives()) {
            lastFollow = undefined;
            // Re-follow only when the POV changes: a pan, or a click that
            // unfollows, holds until the next POV switch.
            const pov = engine.povName();
            if (follow && pov !== lastPov) {
                lastPov = pov;
                if (pov) setFollow(pov);
            }
        } else {
            lastPov = null;
            const target = mapFollow.get();
            if (target !== lastFollow) {
                lastFollow = target;
                setFollow(target);
            }
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
        s.fog = opts.fog ? 1 : 0;
        s.trailDuration = opts.len || Infinity;
        s.learnMode = learnMode.get();
        s.entityFilters = mapTabView.state.entityFilters;
        s.isPlaying = engine.drives() ? engine.status.get().playing : playback.get().playing;
        const pov = engine.drives() ? engine.povName() : mapFollow.get();
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
            : worldW > 0 ? Math.round(Math.max(MIN_H, Math.min(maxH(), cssW * (worldH / worldW)))) : cssW;
        map.resize(cssW, cssH, window.devicePixelRatio || 1);
    }

    function resize() {
        fitCanvas();
        draw();
    }

    $effect(() => {
        map = new MvdMap(canvas);
        // The screenshot harness (test/mapshot.py) reaches the pane's map here.
        /** @type {any} */ (canvas).mvdMap = map;
        // A click on a player (or off them) on the map: watch them.
        map.on('follow', (/** @type {string | null} */ name) => {
            if (setting) return;
            if (engine.drives()) {
                if (name) engine.trackName(name);
            } else {
                lastFollow = name;
                mapFollow.set(name);
            }
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

    // What the map draws changes without the clock moving — learn mode
    // (the menu, ?learn=1), the follow target, the model's data (geometry
    // landing, regions, line of sight, entity filters: `changed`) — and a
    // paused map gets no frame for it.
    $effect(() => {
        $learnMode; $mapFollow; $changed;
        draw();
    });

    // Line of sight / PVS on: the map model's lazy pass, run once per demo.
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
        menu = at;
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

    // Whom the map follows: with the engine up, the POV (on or off); else a
    // player of this demo, or nobody.
    let players = $derived.by(() => { $demo; $mapFollow; return Object.keys(mapTabView.state.playerSymbols || {}).sort((x, y) => x.localeCompare(y)); });
    /** @type {import('../../ui/map-menu.js').MenuItem[]} */
    let followItems = $derived($engineStatus.ready
        ? [{ kind: 'check', label: 'Follow the watched player', on: follow, run: toggleFollow }]
        : [{ kind: 'radio', label: 'Follow nobody', on: !$mapFollow, run: () => mapFollow.set(null) },
            ...players.map((name) => ({ kind: /** @type {const} */ ('radio'), label: `Follow ${name}`, on: $mapFollow === name, run: () => mapFollow.set(name) }))]);

    function clearTrails() {
        const s = map.state;
        for (const name of Object.keys(s.fullTrails || {})) s.trailStartTimes[name] = clock.t;
        draw();
    }

    /** @param {KeyboardEvent} e */
    function key(e) {
        if (e.key === 'Escape' && map?.state.focusGroupName) map.setFocusGroup(null);
    }

    /** @type {import('../../ui/map-menu.js').MenuItem[]} */
    let menuItems = $derived([
        ...followItems,
        { kind: 'sep' },
        { kind: 'check', label: 'View arrows', title: 'Per-player arrows showing where each player is looking', on: opts.view, run: () => toggleOpt('view') },
        { kind: 'check', label: 'Velocity arrows', title: "Per-player arrows showing each player's velocity (length ∝ speed)", on: opts.vel, run: () => toggleOpt('vel') },
        { kind: 'check', label: 'Line of sight', title: 'Lines between players who can see each other (white = mutual, red/blue = one-way). BSP-backed maps only.', on: opts.los, run: () => toggleOpt('los') },
        { kind: 'check', label: 'PVS', title: 'Thin lines between players the server sent to each other (potential visibility). BSP-backed maps only.', on: opts.pvs, run: () => toggleOpt('pvs') },
        { kind: 'check', label: 'Occlusion', title: 'Floors hide players, items and lines behind them', on: opts.occl, run: () => toggleOpt('occl') },
        { kind: 'check', label: 'Fog', title: 'Depth fog: the far side of the map fades', on: opts.fog, run: () => toggleOpt('fog') },
        { kind: 'sep' },
        ...[['none', 'No trails'], ['pov', 'Trail of the watched player'], ['all', 'Trails of everyone']]
            .map(([mode, label]) => ({ kind: /** @type {const} */ ('radio'), label, on: opts.trails === mode, run: () => setTrails(mode) })),
        { kind: 'action', label: 'Clear trails', title: 'Start every trail again from now', run: clearTrails },
        ...TRAIL_LENGTHS.map(([len, label]) => ({ kind: /** @type {const} */ ('radio'), label: `Trail length: ${label}`, on: opts.len === len, run: () => { setPaneOpt('map', 'len', len); draw(); } })),
        { kind: 'sep' },
        { kind: 'check', label: 'Learn the map (items, teleporters)', title: "The map's static layout: items, teleporters with their exits, spawns — the Entities pane filters them", on: $learnMode, run: () => learnMode.set(!learnMode.get()) },
        { kind: 'sep' },
        { kind: 'check', label: '3D (right-drag rotates)', on: tilted, run: toggle3D },
        { kind: 'action', label: 'Reset view', run: () => map?.resetView(), close: true },
    ]);
</script>

<svelte:window onkeydown={key} />

<div class="map-pane" class:sized bind:this={box}>
    <span class="map-pane-btns">
        <button class="pt-btn icon menu-btn" class:active={!!menu} title="Map options (or right-click the map)"
            onclick={() => (menu = menu ? null : { x: Math.max(4, box.clientWidth - 198), y: 36 })}><SlidersHorizontal size={15} /></button>
        {#if $engineStatus.ready}
            <button class="pt-btn icon" class:active={follow} aria-pressed={follow}
                title={follow ? 'Following the POV — click to stop' : 'Keep the map centred on the player you are watching'}
                onclick={toggleFollow}><Crosshair size={15} /></button>
        {/if}
    </span>
    <MapMenu bind:at={menu} items={menuItems} keep=".menu-btn" />
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
</style>
