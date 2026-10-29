# Plan — playback bar, clips, clip finder, Player pane, light player

Branch: `web-svelte`. Follows [`plan-player-tab.md`](plan-player-tab.md) and
[`plan-clip-finder.md`](plan-clip-finder.md). Owner's list of 2026-10-29,
ordered so each step ships on its own (small and independent ones first).

## State at the start (ef0bb02)

- **Playback bar** (`src/ui/PlaybackBar.svelte`, `src/lib/playback-bar.js`):
  four speed buttons (-1x 0.2x 1x 5x, the running one shows ⏸), the time,
  a 24 px track with a ▲ caret *under* it and an axis row, all inside a
  `.panel` with 18 px top padding. That stack is why it is fat
  (~80 px). A drag selects the shared `segment` (= the Timeline tab's view
  window); a click (≤ 2 s drag) clears it and seeks. Mouse events, no
  pointer capture, no hover readout.
- **Clips**: `ClipBar.svelte` (In / Out / Add clip, keys i o c) under the
  player; a clip is `{ from, to, track, who, label, demo }`, demo-relative.
- **Hub links**: three renderers (`timeline.js` "hub", `packdrops.js` and
  `keymoments.js` "Hub"), each with its own `title`; `hub-links.js` reroutes
  clicks and rewrites `title` on mouseover, which loses to the renderer's
  title on some links (the "old tooltip").
- **Clip finder**: open by default under the player; results listed after
  stage 4; stages joined by `ArrowDown` + a count.
- **Player**: a fixed `PlayerFrame` + bars on the left of a PaneForge split;
  not a pane. Share link = `index.html?gameId=…&tab=player#playlist=…`,
  which loads the full analyzer.

## 1. Hub links → "play"

- Every renderer emits the same anchor: text `play`, one title, via one
  helper in `src/lib/hub-url.js` (`hubLinkHtml(url)`); the three local
  copies go.
- Modifier preview: `hub-links.js` tracks Shift / Ctrl / Meta (keydown,
  keyup, blur → clear) as a class on `<html>` (`mod-add`, `mod-hub`); CSS
  swaps the label (`play` → `+add` / `hub`) with a `data-` attribute and
  `::after`, so no DOM walk per keypress. Only for links of the loaded
  demo (the ones the router handles); without a demo they stay `hub`.
- Title written once at render (no mouseover rewrite):
  "Play in the Player tab · Shift: add to playlist · Ctrl: open on hub".

## 2. Clip finder: off by default, results beside, live flow

- `layout.js` DEFAULT `below: ['playlist']`; the finder is one "Add pane…"
  away. Stored layouts keep what the viewer has.
- Results in a second column inside the pane: CSS grid with a container
  query (`container-type: inline-size`) — two columns from ~760 px of pane
  width, stacked below that. The results column is sticky and scrolls on its
  own.
- Stage joints: a short vertical SVG line with beads (`stroke-dasharray`
  animated by `stroke-dashoffset`, CSS only) flowing down, and the count
  beside it in a bold coloured pill ("42 rows", then "17 kept", …; red
  at 0). No arrow. Beads flow only while the stage passes rows on (count
  > 0); a count change gives a one-shot pulse (a Svelte `{#key}` + CSS
  keyframe). `prefers-reduced-motion`: static dots.

## 3. Playback bar redesign (YouTube-like)

One component, rewritten with pointer events + `setPointerCapture`:

- **Thin**: a 4 px track that grows to 8 px on hover, no ▲ caret row, axis
  labels small inside the bar's row height; panel padding cut. Target
  ~36 px total.
- **Playhead** = a round knob (12 px, 16 px on hover) on the track, the
  played part filled.
- **Hover readout**: a floating time label above the pointer (and a thin
  guide line), shown while hovering or dragging.
- **Gestures**: pointerdown within ~10 px of the knob → drag the playhead;
  elsewhere → a press that becomes a selection once it moves > 4 px
  (pixels, not 2 s of match time); a press without a move → seek there and
  clear the selection. Double-click clears.
- **Controls**: one play/pause button + a speed dropdown (0.2 0.5 1 2 5,
  plus -1 off the Player tab — FTE cannot reverse). The pause/play state and
  speed come from `speedButtons`' replacement in `playback-bar.js`
  (`transport()`); `playAtSpeed` / `engine.setSpeed` unchanged. Space
  toggles play/pause when focus is not in a field.
- **Svelte bits**: `Spring` (svelte/motion) for the knob's hover growth
  and the selection's edges settling, `fade` for the readout; the per-tick
  position stays a plain style write (no spring on the hot path).
- Node tests in `test/` for the new pure parts (hit-testing the knob, the
  px drag threshold, the transport state).

## 4. Selection = clip

- A new selection **seeks to its start** (owner). Playback stops at the
  selection's end; play again restarts from its start.
- **Add clip from the selection**: when a POV exists — the Player tab
  (the engine's track; "Auto" counts) and the Map tab (its followed
  player) — a small pill hangs under the selection: `+ clip · <who> ·
  0:12–0:27`. Click adds `{ from, to, track: POV }` (match → demo time via
  the demo's offset). On other tabs there is no pill.
- `ClipBar.svelte` goes (In / Out / Add clip and its hint). Keys: `i` / `o`
  set the selection's start / end at the playhead, `c` adds the selection
  (no selection: the last 10 s, as today).
- A running playlist clip shows its span as the selection, so the bar
  reads as "this clip" (and the Timeline tab zooms to it — one shared
  `segment`).

## 5. Slow motion in clips

- Clip gains an optional `slow: { from, to, speed }` (demo seconds,
  inside the clip). The runner plays that window through `engine.slowTo`
  and returns to the viewer's rate (the bar's speed) after it; the engine
  holds a bar speed change made inside the window until its end.
  Persisted in `mvd.playlist.v1` and the `#playlist=` encoding
  (back-compatible: an absent field = none). Hub `clip=` has no slow
  motion — the hub link drops it.
- Finder stage 4, per clip spec: *slow-mo* off / on; at = a row field
  (point events: `time`) or from / to fields (ranges: top-kills, top-windows,
  quad runs), ± seconds, speed (default 0.3×). Code line:
  `clip from=-3s to=detail.endTime+2s slow=time-1s..time+1s@0.3`.
  Validation as for the other clip fields; long ranges allowed (owner: not
  useful there, but not forbidden).
- Presets: airgibs, telefrags, quadbores, discharges get slow-mo at the
  event by default.
- Playlist row shows a small 🐢 marker; editing slow-mo per row is not in
  this step.

## 6. The player as a pane

The 2026-10-28 design: the engine's iframe never moves (moving or removing
an iframe reloads it). It lives in one fixed layer of the tab; the
**Player pane** renders a placeholder of the right aspect, and the layer
is positioned over the placeholder every frame (ResizeObserver +
scroll + the pane column's own changes; rAF only while something moves).

- Collapsing / closing the Player pane hides the layer and suspends the
  engine (`FteController.suspend`, as when leaving the tab).
- The POV bar moves into the Player pane's body (the clip bar is gone after
  step 4).
- The tab becomes two pane columns with nothing special; default layout:
  `below: ['player', 'playlist']`, `order: ['map', 'players',
  'team-weapons']`. The split stays PaneForge's.
- Fullscreen on the Player tab = the whole tab as now (both columns); a
  "maximise" on the Player pane is a later option.
- Risks: a frame of lag while scrolling / dragging the divider; menus and
  the finder's dropdowns must stack above the layer (z-order, and the
  layer gets `pointer-events: none` while a pane drag runs); the
  < 1100 px stacked layout; SwiftShader's slow show. Headless suites
  (e2e / panes2 / finder2) get Player-pane checks.

## 7. Light player page

The share link opens a light page that plays the playlist with the
labels, without the analyzer (WASM + analysis take most of the load).

- Same Vite app, second entry `play.html` (`src/play/`): the engine
  (`engine.js` takes its demo bytes + offset from a small source rather
  than the `demo` store), the runner (its `loadDemo` dependency becomes
  "fetch a hub demo's bytes", which the light page can do without
  analysis), `ClipCard`, and a simple clip list (label, who, ▶, now
  playing). Times show as demo time, or match time when the clip carries
  `demo.offset` (it does — the share link encodes it).
- Below the player: *Open in the full analyzer* →
  `index.html?gameId=…&tab=player#playlist=…`.
- Playlist's *Share link* points at `play.html`; the old link form keeps
  working.

## Decisions (owner, 2026-10-29)

1. Playback that reaches a selection's end **stops** there (as a clip run
   stops at `to`); play again restarts from the selection's start.
2. **One shared `segment`**: selecting or playing a clip also zooms the
   Timeline tab to it; `seg=` in the URL carries it.
3. Light player = a **separate `play.html`** linking out to the full
   analyzer; no background analysis.

## Status (2026-10-29)

All seven steps built and pushed on `web-svelte`: 1 hub links 6620bda ·
2 finder 5025ecc · 3 bar 1016904, then laid out like a video player's
(scrubber on top, pills under it — owner's feedback) 8d586d0 · 4 selection
= clip 43f86ad · 5 slow motion 4f8a10e (+ docs 0cf8dd5) · 6 Player pane
2daf987 · 7 light player aacd4ef. Verified headless (Playwright +
SwiftShader); not yet on a real GPU browser.

Deviations from the plan above:
- Step 6: collapsing / closing the Player pane hides the layer but does
  not suspend the engine — on that tab the engine is the clock the other
  panes follow.
- Step 4: the "+ clip" pill sits in the bar's pill row (after the owner's
  layout feedback), not under the selection.
- Step 7: the share link always opens `play.html`; the full page still
  reads the old `?gameId=…&tab=player#playlist=` form.

## Order

1 → 2 → 3 → 4 → 5 → 6 → 7. Step 3 precedes 4 (the pill hangs off the new
selection); 6 precedes 7 only because both touch `engine.js`'s inputs and
the runner. Each step: `make test`, build, headless suites, push.
