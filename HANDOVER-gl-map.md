# Handover — pure-GL map renderer (`gl-map`)

> **DELETE THIS FILE BEFORE MERGING TO MAIN** (same rule as
> `HANDOVER-map-view.md`, which covers the extraction phase this branch
> builds on). Keep what outlives the branch in `mvd-map-view/README.md` or
> commit messages.

Branch: `gl-map`, stacked on `map-view-extract`, both pushed to origin.
Goal, in the owner's words: remove the old 2D code and have pure GL —
enabling advanced effects like 3D player models, fog, occlusion, better
lighting, first-person view. **The conversion is DONE** (steps 1–6 below);
the effects phase is deliberately parked for joint design with the owner.

---

## Fresh session: read this first

**State of the world.** The map scene renders entirely through WebGL2.
`mvd-map-view/src/glworld.js` is the renderer (five programs: world
triangles with fog, shaped point sprites, screen-extruded quad-lines with
shader dashes, textured label billboards, screen-space arrowhead
triangles); `MvdMap._glDynamic`/`_glActors` in `src/map.js` build the
per-frame command data; `src/glatlas.js` bakes label strings to a texture
page. The old canvas-2D scene painter is deleted (−1,297 lines in the
step-6 commit). `draw.js` survives only as DOM-icon rasterisers for the
host's sidebar. No WebGL2 → a notice on the map canvas; SwiftShader boxes
run GL (slower, works). `make test` green: Go suite + 118 node tests.

**Branch stack and merge order.** `main` → `map-view-extract` (component
extraction, byte-parity-gated, PLUS the first hybrid GL backend) →
`gl-map` (pure GL). Merge `map-view-extract` first or fold both into one
PR; either way delete BOTH handover files at merge time. RELEASE_NOTES
already carries entries for both branches ("unreleased (map-view-extract)"
and "unreleased (gl-map)" — the latter supersedes the former's
fallback/`?gl=0` description, which stopped being true on this branch).

**This machine** (details in the session memory file
`env-node-playwright-setup.md`): no sudo, no system node/pip, **no GPU**.

- Node 22: `export PATH=$HOME/.local/opt/node-v22.17.0-linux-x64/bin:$PATH`
  (node 22.17 rejects `node --test <dir>/`; make test uses the glob form).
- Playwright: `export PATH=$HOME/.local/opt/pwenv/bin:$PATH` so the
  harness's `python3` resolves to the venv.
- Chromium needs locally-extracted libs:
  `apt-get download libnspr4 libnss3 libatk1.0-0t64 libatk-bridge2.0-0t64
  libxdamage1 libatspi2.0-0t64 libxres1 && dpkg -x` each into a dir, then
  `export LD_LIBRARY_PATH=<dir>/usr/lib/x86_64-linux-gnu`. (The previous
  session's extraction lived in its scratchpad — gone; re-extract.)
- WebGL here is SwiftShader: deterministic, so shots byte-compare on this
  box, but SLOWER than a GPU — never benchmark GL performance here and
  conclude anything.

**The verification loop** (post-step-6, one render path):

```bash
make test                                        # gofmt gate + Go + node
make build
mvd-web/test/capture-baseline.sh /tmp/shots/before   # BEFORE editing
# ...edit...
make build
mvd-web/test/capture-baseline.sh /tmp/shots/after
mvd-web/test/compare-shots.sh /tmp/shots/{before,after}
```

Byte-identical is only expected for refactors; for visual changes,
quantify (percentage of pixels whose max channel delta exceeds ~60 — a
wrong matrix reads as ~100%, an intended change as a few percent) and
eyeball representative shots. Same machine + driver only.

**Traps found the hard way on this branch:**

- **A thrown exception anywhere in the GL frame build = a silently black
  map** (the canvas is cleared before drawWorld). mapshot.py now exits
  non-zero on any uncaught page error, and a node test
  (`_glActors composes players, items, arrows...`) composes every actor
  sub-path headlessly — extend that test when adding a sub-path, it has
  already caught two real bugs (a missing import; mis-spliced uniforms).
- **The label atlas shifts positions by a texel when its pack order
  changes across code changes** (bake order = first-render order). Shots
  stay byte-deterministic within a build; across builds expect a handful
  of 0.000%-strong label diffs even for "no-op" changes.
- Several harness shots (`*-i-orbit`, `211805-*-zoomed`, some
  `i-wheelzoom`) are **legitimately near-black** — pre-existing camera
  positions (edge-on orbit, zoom into empty space), identical since the
  2D era. Don't chase them.
- Python bulk-`str.replace` on the sources: check match counts. One
  replace with two match sites put fog uniforms inside `_drawMovers` and
  killed every frame (caught by the hardened harness).

---

## What landed (per-commit log)

| Commit | Step |
|---|---|
| `2e88b4c` | **1+2** — opaque depth-buffered floors (GL floor sort deleted; z-buffer decides; liquids/focus-fade blend with depth reads — liquids now correctly occluded by floors, an intentional improvement) + movers (opaque, depth-tested, translation uniform), projectile/nail point sprites, LG beams on the quad-line program. Camera grew a clip-z row (`makeWorldTransform.rz`, pinned to `project().depth` by test) |
| `26feb3e` | **3+4** — region tints (control under occupancy) as per-group VBOs cached by tris identity + tint uniform; all region outlines as cached camera-free quad-line VBOs; trails (death/spawn gaps, teleport dashes via fract() pattern, marker sprites) and LOS/PVS sightlines as GL data. Sprite/atlas/screen-tri machinery landed |
| `1991be0` | **5** — the whole actor layer + every label in GL: z-sorted items+players as an ordered batch list (painter order preserved across primitive types), badges on screen-offset point sprites, stems, view/vel arrows with screen-space triangle heads, fading death-✕/drop-D, loc + occupied-bold labels (shadow = tint on one white atlas entry), learn-mode entity view with teleport links |
| `29c0cbb` | **6** — 2D scene path deleted (bake, seam hack, all 2D layer draws, software gate, `?gl=0/1`, harness GL flags). Loc-blob maps go through the same GL passes (their groups carry no tris, so the labels-on-dark look is unchanged — that was always the nogeom appearance) |
| `e209fc4` | **effects PoC** — see below |

Worst intentional per-step visual delta: 0.77% strong-diff pixels (the
label-dense learn view, atlas text vs canvas text); typical steps ~0.02%.

## Effects PoC — REVIEW WITH OWNER BEFORE BUILDING ON IT

Three toggles exist (buttons after "3D" in the map toolbar), all
default-OFF; with them off the shot corpus is byte-identical to step 6:

- **Fog** — depth fog in the world fragment shader (floors, movers,
  liquids, tints), density scaled from the map's world radius
  (`state.fog` 0..1).
- **Light** — directional Lambert baked into floor vertex colours at
  build time (`state.worldLight` 0..1; rebuilds the floor batch on
  change; 0 reproduces the flat look bit-for-bit).
- **Occl** — overlays/actors depth-test LEQUAL (read-only) against the
  world (`state.occludeActors`); floors hide players/items/lines behind
  them. Known nit: screen-space arrowhead triangles carry z=0, so they
  can clip oddly in this mode — fix if the mode survives review.

The owner said: *"Do not focus on adding new effects, that is part of
next steps done together"* — so 3D player models and the perspective /
first-person camera are NOT started. Design notes that must survive into
that discussion:

- **First/third person stays in the floors-only world** (owner's explicit
  call, twice): no mapgen changes, no full-mesh corpus, no new geometry.
- Perspective belongs in `camera.js` `project()` (plus a w-row in
  `makeWorldTransform` and a real mat4 in the shaders): every layer
  projects through that one seam, so a camera mode carries the whole
  scene automatically.
- Occlusion should default ON in first-person, OFF in the map view
  (always-visible actors are an analyzer feature).

## Also relevant

- `HANDOVER-map-view.md` (on this stack) documents the extraction phase,
  including two deliberately-deferred items that are NOT this branch's
  business: the async/windowed FrameSource and formal data setters — both
  wait for the MCP Apps viewer to drive their design.
- The perf claim for GL on real GPUs is architectural (camera motion =
  uniform update vs re-rasterising 2–25k triangles); it could not be
  measured on this GPU-less box (SwiftShader GL benches ~45ms vs the old
  2D ~32ms per rotated dm3 frame — that comparison is about THIS box,
  nothing else). Owner should eyeball rotation smoothness on real
  hardware.
