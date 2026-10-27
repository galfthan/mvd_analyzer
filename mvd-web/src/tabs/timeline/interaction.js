// interaction.js — the Timeline tab's pointer handling on its graphs, as
// Svelte actions: Ctrl/Cmd+wheel zoom, drag pan and double-click reset on a
// canvas (panZoom), dragging a current-time line to scrub (scrub), and a
// hover read-out (tooltip). `ctl` is the tab's controller:
//   range()             the current view window [start, end] (seconds)
//   setRange(start, end) ask for a new window (clamped by the tab)
//   reset()             back to the whole match
//   time()              the clock; seek(t) moves it
//
// One document-level move / up pair per kind (installed by the tab,
// installGlobals) rather than a listener per canvas. Pan updates are
// coalesced to one per animation frame: a raw mousemove stream would redraw
// every graph dozens of times a second.

export function createInteraction(ctl) {
    const pan = { canvas: null, lastX: 0, pendingX: 0, rafId: 0 };
    let scrubEl = null;

    // Apply the pan accumulated since the last frame; from the rAF that
    // mousemove schedules, and from mouseup to land exactly on the cursor.
    function processPan() {
        pan.rafId = 0;
        const c = pan.canvas;
        if (!c) return;
        const rect = c.getBoundingClientRect();
        if (rect.width <= 0) return;
        const [start, end] = ctl.range();
        const secPerPx = (end - start) / rect.width;
        const dx = pan.pendingX - pan.lastX;
        if (dx === 0) return;
        pan.lastX = pan.pendingX;
        ctl.setRange(start - dx * secPerPx, end - dx * secPerPx);
        // Pin the playhead to its place on screen: shift the time by however
        // far the window actually moved (after the clamp to the match), so
        // the line stays put and the unified caret, clock and map follow.
        // Not zoomed, the window cannot move and the playhead holds.
        const [newStart] = ctl.range();
        const applied = newStart - start;
        if (applied !== 0) ctl.seek(ctl.time() + applied);
    }

    function onMove(e) {
        if (pan.canvas) {
            pan.pendingX = e.clientX;
            if (!pan.rafId) pan.rafId = requestAnimationFrame(processPan);
        }
        if (scrubEl) {
            const rect = scrubEl.getBoundingClientRect();
            if (rect.width <= 0) return;
            const [start, end] = ctl.range();
            const frac = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
            ctl.seek(start + frac * (end - start));
        }
    }

    function onUp() {
        if (pan.canvas) {
            if (pan.rafId) {
                cancelAnimationFrame(pan.rafId);
                pan.rafId = 0;
                processPan();
            }
            pan.canvas.style.cursor = 'grab';
            pan.canvas = null;
        }
        if (scrubEl) {
            scrubEl = null;
            document.body.classList.remove('graph-scrubbing');
        }
    }

    function installGlobals() {
        document.addEventListener('mousemove', onMove);
        document.addEventListener('mouseup', onUp);
        return () => {
            document.removeEventListener('mousemove', onMove);
            document.removeEventListener('mouseup', onUp);
            if (pan.rafId) cancelAnimationFrame(pan.rafId);
        };
    }

    // panZoom: Ctrl/Cmd+wheel zooms about the cursor (a plain wheel scrolls
    // the page), a left drag pans, a double click resets to the whole match.
    function panZoom(canvas) {
        const timeAt = (clientX) => {
            const rect = canvas.getBoundingClientRect();
            if (rect.width <= 0) return null;
            const [start, end] = ctl.range();
            return start + ((clientX - rect.left) / rect.width) * (end - start);
        };
        const wheel = (e) => {
            if (!e.ctrlKey && !e.metaKey) return;
            e.preventDefault(); // no browser pinch-zoom
            const centerT = timeAt(e.clientX);
            if (centerT === null) return;
            // Exponential — deltaY > 0 (toward the user) zooms out.
            const factor = Math.exp(e.deltaY * 0.0015);
            const [start, end] = ctl.range();
            ctl.setRange(centerT - (centerT - start) * factor, centerT + (end - centerT) * factor);
        };
        const down = (e) => {
            if (e.button !== 0) return;
            pan.canvas = canvas;
            pan.lastX = e.clientX;
            pan.pendingX = e.clientX;
            canvas.style.cursor = 'grabbing';
            e.preventDefault();
        };
        const dbl = () => ctl.reset();
        canvas.addEventListener('wheel', wheel, { passive: false });
        canvas.addEventListener('mousedown', down);
        canvas.addEventListener('dblclick', dbl);
        canvas.style.cursor = 'grab';
        return {
            destroy() {
                canvas.removeEventListener('wheel', wheel);
                canvas.removeEventListener('mousedown', down);
                canvas.removeEventListener('dblclick', dbl);
                if (pan.canvas === canvas) pan.canvas = null;
            },
        };
    }

    // scrub: grabbing a current-time line sets the time. Its parent holds
    // the canvas at full width and so shares the canvas's time → x mapping.
    function scrub(indicator) {
        const down = (e) => {
            if (e.button !== 0) return;
            scrubEl = indicator.parentElement;
            document.body.classList.add('graph-scrubbing');
            e.preventDefault();
            e.stopPropagation();
        };
        indicator.addEventListener('mousedown', down);
        return { destroy() { indicator.removeEventListener('mousedown', down); } };
    }

    return { installGlobals, panZoom, scrub };
}

// tooltip: a hover read-out over a canvas — one .canvas-tooltip div in the
// canvas's positioned parent; on every mousemove `hitTest(mx, my)` (canvas
// CSS px) picks what is under the cursor and `html(hit)` fills the tip,
// placed by the cursor and kept inside the wrapper's right edge.
export function tooltip(canvas, { hitTest, html }) {
    const wrapper = canvas.parentElement;
    const tip = document.createElement('div');
    tip.className = 'canvas-tooltip';
    tip.style.display = 'none';
    wrapper.appendChild(tip);
    const move = (e) => {
        const rect = canvas.getBoundingClientRect();
        const mx = e.clientX - rect.left;
        const my = e.clientY - rect.top;
        const hit = hitTest(mx, my);
        if (!hit) { tip.style.display = 'none'; return; }
        tip.innerHTML = html(hit);
        tip.style.display = 'block';
        const tipW = tip.offsetWidth || 200;
        const wrapW = wrapper.clientWidth;
        let left = mx + 12;
        if (left + tipW > wrapW) left = mx - tipW - 12;
        tip.style.left = left + 'px';
        tip.style.top = (my + 12) + 'px';
    };
    const leave = () => { tip.style.display = 'none'; };
    canvas.addEventListener('mousemove', move);
    canvas.addEventListener('mouseleave', leave);
    return {
        destroy() {
            canvas.removeEventListener('mousemove', move);
            canvas.removeEventListener('mouseleave', leave);
            tip.remove();
        },
    };
}
