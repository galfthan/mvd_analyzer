# Handover — pure-GL map renderer (`gl-map`)

> **DELETE THIS FILE BEFORE MERGING TO MAIN** (same rule as
> `HANDOVER-map-view.md`, which covers the extraction phase this branch
> builds on). Keep what outlives the branch in `mvd-map-view/README.md` or
> commit messages.

Branch: `gl-map`, stacked on `map-view-extract`. Goal, in the owner's words:
remove the old 2D code and have pure GL — enabling advanced effects like 3D
player models, fog, occlusion, better lighting, first-person view.

## Ground rules for this phase (different from the extraction!)

- **Pixel-identical parity is NOT required** (owner's call). The extraction
  branch's byte-exact gates proved the component move; this phase
  deliberately changes rendering. Verification is: `make test` green, a
  shot capture per step, a quantified diff against the previous step's
  shots (to catch catastrophes — a wrong matrix reads as 100% strong-diff
  pixels, an intended change as a few percent), and eyeballing
  representative shots.
- **Shots are GL-vs-GL on one machine.** This dev box has no GPU: SwiftShader
  renders WebGL deterministically, but the app's software-renderer gate
  prefers 2D here — capture with `MAPSHOT_FLAGS=--force-webgl` until the
  gate is removed (step 6).
- **"Pure GL" still bakes text/sprites through a 2D context** — as a texture
  atlas rasteriser only, never as a render path. That is how engines do
  text; the 2D *drawing* of the scene is what dies.
- The loader invariant, the single-canvas contract and the push-only
  resize/data API from the extraction phase all still hold.

## The plan, in step order (one gated commit each)

1. **Depth-buffered opaque world.** Floors render opaque with a real depth
   buffer (the painter sort dies on the GL path); focus-faded regions and
   liquids become blended passes with depth-test-read. This is the
   foundation every listed effect stands on. Visual delta: the 5%
   see-through floors had (alpha 0.95) goes away — invisible in practice.
2. **Movers, projectiles, beams in GL.** Movers as opaque depth-tested
   meshes (per-mover translation uniform — the silhouette/stencil trick the
   blended version would need is unnecessary once they're opaque).
   Projectiles as round point-quads; beams on the quad-line primitive
   (screen-space extrusion in the vertex shader; dashes are a fract()
   pattern when trails need them).
3. **Region tints (occupied / control) + region outlines in GL.**
4. **Trails + LOS/PVS lines in GL** (quad-lines; dashed teleport segments
   via the shader pattern).
5. **Sprites + text atlas**: item markers, player symbols, badges, death
   X / drop D, loc labels as textured billboards from a canvas-baked atlas.
6. **Delete the 2D scene path.** drawCachedWorld + renderSolidEntries (and
   its seam hack) + drawLiquidVolume + the world half of draw.js go; the
   software-renderer gate goes (SwiftShader is the fallback now — it IS
   slower than the old 2D painter on GPU-less machines, accepted cost of
   one render path); `?gl=0` goes; harness anchor flips to GL captures.
   Loc-blob maps (no BSP geometry) render their translucent fills through
   the same GL batches.
7. **Effects phase** (each its own commit, mostly orthogonal):
   - **Fog**: depth-based mix in the fragment shader, toggle + density knob.
   - **Lighting**: per-face normals in the world vertex data + a directional
     light uniform. Keep the default look close to today's flat tones (the
     flat look was a deliberate design choice); lighting strength is a knob.
   - **Occlusion view mode**: actors depth-tested against the world. OFF by
     default — always-visible players are an analyzer feature, not a bug.
     ON by default in first-person.
   - **3D player models**: oriented low-poly models (team-coloured) driven
     by the stream's position + vya/vp view angles, replacing/augmenting
     the letter billboards in tilted views.
   - **Perspective camera + first-person mode**: a projection-mode switch in
     the camera (ortho stays for the map view), first-person following a
     chosen player's eyes. **Owner's call: the world stays floors-only** —
     first person moves through the existing floors/liquids/movers geometry,
     no mapgen or corpus changes. (A full-mesh corpus could come later if
     the floors-only view proves too sparse, but it is explicitly out of
     scope now.)

## Where things stand

- **Step 1 landed** — opaque depth-buffered floors (the GL floor sort is
  gone); focus fade + liquids blend over with depth reads. Liquids are now
  correctly occluded by floors above them (the painter version drew them
  through). Verified: 0.00% strong-diff pixels vs the painter version in
  sampled shots.
- **Step 2 landed** — movers (opaque, depth-tested — reads clearly better
  than the old translucent silhouette), projectile/nail dots as round point
  sprites, LG beams on the screen-space quad-line primitive (which trails /
  sightlines will reuse, dashes as a shader pattern). The 2D versions still
  exist and run on the fallback path only. Worst strong-diff shot vs step 1:
  0.031% of pixels (the dots/movers themselves).
- **Step 3 landed** — region tints (control under occupancy) and all region
  outlines render in the GL pass: per-group fill VBOs cached by tris
  identity + restyled via a tint uniform, outlines as cached quad-line VBOs
  (extrusion is in the shader, so the VBO is camera-free) restyled via
  tint/width uniforms. The occupied bold labels are the only overlay piece
  still 2D (text). Worst strong-diff vs step 2: 0.024%.
- **Step 4 landed** — trails (with the death/spawn gap rules, teleport
  dashes via the shader pattern, death-✕/spawn-dot marker sprites) and the
  LOS/PVS sightlines collect into the GL line/point passes. The actor
  z-order machinery for step 5 is in place: shaped point sprites
  (disc/✕/ring/square/square-outline), a textured-billboard program fed by
  a lazily-baked label atlas (glatlas.js), a screen-space triangle program
  for arrowheads, and an ordered actor-batch dispatch in render(). Worst
  strong-diff vs step 3: 0.024%.
- **Step 5 landed** — the whole actor layer and every label render in GL:
  the z-sorted items+players composition (squares/outlines/labels, circles/
  letters/badges with screen-offset point sprites, floor stems, view/vel
  arrows with screen-space triangle heads), the fading death-✕/drop-D
  markers, loc labels + occupied bold labels (shadow via tint on one white
  atlas entry), and the learn-mode entity view with teleport links. On the
  GL path the 2D canvas now draws NOTHING per frame — the entire scene is
  in the blit. Worst strong-diff vs step 4: 0.77% (the label-dense learn
  view; atlas text vs per-frame canvas text). Found by the gate: a missing
  ARROWHEAD_PX import threw inside the GL frame build and blacked every
  arrow/learn shot — now covered by a node test that exercises every actor
  sub-path headlessly.
- **Step 6 landed — the 2D scene path is gone.** drawCachedWorld,
  renderSolidEntries + the seam hack, drawLiquidVolume, the 2D
  actor/overlay/trail/label draws, the software-renderer gate, ?gl=0/?gl=1
  and the harness --no-webgl/--force-webgl flags are all deleted. draw.js
  keeps only the DOM-icon rasterisers app.js bakes sidebar player icons
  with. Loc-blob (no-geometry) maps render through the same GL passes.
  No WebGL2 → a "WebGL2 required" notice on the map canvas. Verified: only
  sub-pixel label diffs vs step 5 (atlas pack order shifted; 0.000% strong
  pixels), nogeom look unchanged. NOTE: label positions can shift by a
  texel when the atlas repacks across code changes — same-build captures
  stay byte-deterministic, which is what the harness compares.
