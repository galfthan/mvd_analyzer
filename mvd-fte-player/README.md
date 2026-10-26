# mvd-fte-player

The in-browser QuakeWorld demo player behind mvd-web's **Player** tab: the
FTE engine (ftewebgl) hosted in a page of our own, plus a small controller
the host page drives directly. Plain ESM, no build step, no dependencies;
`make build` copies it to `dist/fte-player/`.

## Why our own copy

hub.quakeworld.nu has an embeddable player (`/demo-player/`), but it is a
cross-origin iframe: it posts its clock and tracked player to the parent and
accepts **no commands** back, so seeking or switching POV from our UI would
mean reloading the embed. Hub's frontend is MIT and no longer developed, so
we ported the part we need and serve it from our own origin. Same origin is
the whole point — mvd-web calls the controller through
`iframe.contentWindow` with no message protocol in between.

## Files

| File | |
|---|---|
| `player.html` | The page FTE runs in. Exposes `window.mvdFte.start(opts)` → `Promise<FteController>`. One engine per page load. |
| `src/loader.js` | `startFte()` — builds the Emscripten `Module` config (canvas, manifest, virtual-FS file map), injects `ftewebgl.js`, resolves when `Module.getClientState` exists. |
| `src/fte-controller.js` | `FteController` — console commands (`FTEC.cbufadd`), demo clock, `demoJump` (re-applies track + speed after a rewind; restarts the demo after it ended), speed / pause, autotrack / `track`, volume. |
| `src/assets.js` | The engine's asset manifest: 450 paths under `a.quake.world/fte/`, the map BSP from `assets.quake.world/maps/`, our `config.cfg`, and the demo. |
| `config.cfg` | Hub's `config_v5.cfg`, verbatim: HUD, rendering, and the closing `playdemo match`. |
| `test/` | Node tests (`make test`) against a fake engine window. |

`src/fte-controller.js`, `src/assets.js`, `src/loader.js` and `config.cfg`
are ported from [quakeworldnu/hub.quakeworld.nu](https://github.com/quakeworldnu/hub.quakeworld.nu)
@ `5c96205c19c7` (2026-10-25) under its MIT license — see [`LICENSE`](LICENSE).

## Using it

```js
iframe.src = 'fte-player/player.html';
iframe.addEventListener('load', async () => {
    const fte = await iframe.contentWindow.mvdFte.start({
        demoUrl,     // any URL the iframe can fetch — mvd-web passes a blob: URL of the analysed bytes
        demoPath,    // 'qw/match.mvd' or 'qw/match.mvd.gz' (must match the bytes)
        mapName,     // BSP short name, e.g. 'dm3'
    });
    fte.demoJump(130);   // demo-relative seconds (countdown included)
    fte.track(10);       // userid
    fte.setDemoSpeed(50);
});
```

Times are **demo-relative** seconds — the same clock as hub's `from`/`to`
link params. mvd-web converts from match time with
`streams.global.demoOffset`.

## Gotchas

- **Ready is not playing.** `start()` resolves once the engine exposes
  `getClientState`, before `playdemo` has the demo running; a `demo_jump`
  sent then is dropped. Wait for `getDemoElapsedTime() > 0` first
  (mvd-web's `waitForDemoClock`).
- **End of demo.** FTE disconnects when the demo runs out ("EndOfDemo")
  and refuses `demo_jump` after that ("not playing a demo"). It has no
  callback for this, so the loader watches the iframe's `console.log` (the
  engine prints there directly) and calls `demoEnded()`; the next
  `demoJump` re-issues `playdemo`, jumps once the fresh clock runs, and
  restores track + speed when it arrives. `isRestarting()` is true until
  then. Hub's player has the same dead end.
- **Sound** starts muted. Browsers suspend audio until a user gesture in
  the engine's window; the loader keeps the engine's `AudioContext`s and
  `unmute()` resumes them, so a click in the host page is enough.
- **One engine per window.** Emscripten publishes `Module` / `FTEC` as
  globals and has no teardown. To play another demo, reload the iframe.
- **External hosts.** The engine, its assets and map BSPs load from
  `a.quake.world` / `assets.quake.world` (all `access-control-allow-origin: *`,
  checked 2026-10-26). If those go away the player stops working; mirroring
  them is possible but needs a licence check on the Quake data files.
- **Loc files.** `config.cfg` downloads `<map>.loc` from
  `assets.quake.world`, which lacks some maps (e.g. `dm3`) — a harmless 404
  in the console, the same as on hub.
