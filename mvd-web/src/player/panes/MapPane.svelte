<script>
    // The map, following the player you watch. A second MvdMap with its own
    // canvas, camera and follow target; the map DATA (geometry, locs,
    // frames, items, trails…) is the Map tab's, which app.js's initMapView
    // and the async loaders fill — read from it each frame (syncMapData)
    // until the Map tab's loading moves into src/. Click a player on the map
    // to watch them; drag pans, right-drag orbits, the wheel zooms,
    // double-click resets the view.
    import { MvdMap } from '../../../../mvd-map-view/src/index.js';
    import { clock, demo, teams } from '../../state/index.js';
    import { mapTabView } from '../../legacy.js';
    import { engine } from '../index.js';
    import { paneLayout } from '../layout.js';
    import { onFrame } from './tick.js';
    import { syncMapData } from './map-sync.js';

    const MIN_H = 200;
    const MAX_H = 520;

    let canvas;
    let box;
    let follow = $state(true);
    let empty = $state(false);
    let map = null;
    let setting = false;
    let lastPov = null;       // the POV the camera last followed
    let needsFit = true;      // a new demo: reset the view once its data is in

    const sized = $derived($paneLayout.heights.map != null);

    function setFollow(name) {
        setting = true;
        try { map.setFollowPlayer(name); } finally { setting = false; }
    }

    // draw syncs the Map tab's data in and renders at the clock's time.
    function draw() {
        if (!map) return;
        const src = mapTabView();
        empty = !src?.state.initialized;
        if (empty) return;
        if (syncMapData(src.state, map.state) || needsFit) {
            // The world's mid height comes from the loc z-range app.js
            // computed for the Map tab's camera.
            map.camera.zMidDefault = src.camera.zMidDefault;
            if (needsFit) {
                needsFit = false;
                lastPov = null;
                map.resetView();
            }
            fitCanvas();
        }
        map.teamColors = $teams.colors;
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
        map.on('follow', (name) => {
            if (setting || !name) return;
            engine.trackName(name);
        });
        const ro = new ResizeObserver(() => resize());
        ro.observe(box);
        const offFrame = onFrame(() => draw());
        return () => {
            offFrame();
            ro.disconnect();
            map.dispose();
            map = null;
        };
    });

    // A new demo: drop the old follow target; the view resets on its data.
    $effect(() => {
        $demo;
        needsFit = true;
        if (map) setFollow(null);
    });

    $effect(() => {
        sized;
        resize();
    });

    function point(ev) {
        const r = canvas.getBoundingClientRect();
        return { x: ev.clientX - r.left, y: ev.clientY - r.top };
    }

    function down(ev) {
        if (ev.button !== 0 && ev.button !== 2) return;
        const p = point(ev);
        map.pointerDown(p.x, p.y, (ev.button === 2 || ev.ctrlKey || ev.metaKey) ? 'orbit' : 'pan', ev.button);
        ev.preventDefault();
        const move = (e) => { const q = point(e); map.pointerMove(q.x, q.y); };
        const up = (e) => {
            const q = point(e);
            map.pointerUp(q.x, q.y);
            window.removeEventListener('mousemove', move);
            window.removeEventListener('mouseup', up);
        };
        window.addEventListener('mousemove', move);
        window.addEventListener('mouseup', up);
    }

    function wheel(ev) {
        ev.preventDefault();
        const p = point(ev);
        map.wheelZoom(p.x, p.y, ev.deltaY);
    }

    function toggleFollow() {
        lastPov = null;
        if (!follow) setFollow(null);
        draw();
    }
</script>

<div class="map-pane" class:sized bind:this={box}>
    <label class="follow" title="Keep the map centred on the player you are watching">
        <input type="checkbox" bind:checked={follow} onchange={toggleFollow}> follow POV
    </label>
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
    .follow {
        position: absolute;
        top: 4px;
        right: 6px;
        z-index: 1;
        font-size: 12px;
        color: #aaa;
        background: rgba(0, 0, 0, 0.45);
        padding: 0 6px;
        border-radius: 3px;
        cursor: pointer;
    }
</style>
