# Plan — Player tab (embedded hub demo player + panes + playlist)

Branch: `player-tab`, stacked on `gl-map` (which stacks on
`map-view-extract`). Merge order: `gl-map` PR first, then this.
Status: **sketch** — to be refined with the owner before code lands.

## Goal

A new **Player** tab: the hub.quakeworld.nu FTE demo player embedded live,
with a column of user-arrangeable **panes** beside it (map, per-player
graphs, …) that follow the player's clock, and a **playlist** below it of
clips (start, end, POV, name). Every hub link on the other tabs becomes a
"play here / add to playlist" link. A playlist can be exported as a hub
link or played back in-page as a script (quad runs, best quadbores,
highlights of a player, …).

## Engine: our own port of hub's player (decided 2026-10-26)

hub.quakeworld.nu's `/demo-player/` embed was evaluated first: embeddable,
posts `current_time`/`seek`/`set_speed`/`track`/`clip` to the parent, supports
repeated `clip=FROM-TO:TRACK` — but accepts **no commands** cross-origin (the
`contentWindow.FTEC` recipe only works same-origin), so every seek / POV
switch from our UI would be an iframe reload. Hub's frontend is MIT and no
longer developed (its author is building a successor), so instead:

- [`mvd-fte-player/`](mvd-fte-player/README.md) — port of hub's FTE
  controller, asset manifest and config; `player.html` runs ftewebgl and is
  served from **our** origin, embedded as a same-origin iframe. mvd-web
  calls the controller directly: seek, POV, speed are instant.
- Engine + assets + BSPs still load from `a.quake.world` /
  `assets.quake.world` (CORS `*`); demos come from the bytes we already
  analysed (blob URL) — local files play too, no sha256 needed.
- Freedom to change the player itself (HUD config, overlays, commands)
  without anyone upstream.

Time base: the engine runs on demo-relative seconds, our tables on
match-relative ones; convert with `streams.global.demoOffset` as
`hubReplayUrl()` documents.

## Layout

```
┌──────────────────────────────┬───────────────┐
│                              │ [Map      ▾ ×]│
│   hub demo player (iframe)   │               │
│                              ├───────────────┤
│   POV: [p1][p2][p3][p4]      │ [Stack    ▾ ×]│
├──────────────────────────────┴───────────────┤
│ Playlist: ▶ 12:03–12:20 milton  "quad run"  …│
└──────────────────────────────────────────────┘
```

- **Player** keeps 16:9 aspect and resizes freely (it is our page).
- **POV bar** under the player: one button per player (team colours from
  `TEAM_COLORS`/`getTeamOrder()` — the canonical mapping, per CLAUDE.md),
  current POV highlighted from the `track` event.
- **Pane column** (right): a vertical stack of panes, each with a header
  (title, move up/down or drag handle, close). "+ Add pane" menu lists the
  pane registry. Layout (which panes, order, column width) persists in
  `localStorage` (per-viewer convenience — wrapped in try/catch).
- **Playlist** (bottom).

## Panes

A pane = `{ id, title, mount(el), unmount(), setTime(matchT), setPov(name) }`
registered in a small registry. The Player tab already owns one clock (the
engine's, pushed through `setCurrentTime`) — panes hang off that.

First panes:

1. **Map** — a second `MvdMap` instance (the extraction's loader
   invariant — no ambient globals, data/size through the API — is exactly
   what allows a second mount). Follow-cam on the POV player. Click a
   player on the map → switch POV.
2. **Per-player stack** (health/armor per player, the Timeline tab's
   per-player panel) with a cursor at `matchT`.
3. Later: frags-per-player, weapons-per-player, region control, chat
   (scrolling with the clock), key-moment list (click → seek).

Pane rendering reuses the existing render functions where they are
already parameterised on a host element + time window; where they are
hard-wired to a Timeline-tab DOM id, factor out the host element rather
than duplicating the drawer.

## Playlist

Entry: `{ sha256, gameId?, from, to, track, label, source }` with
`from`/`to` match-relative in storage, converted at link/play time.

- **Add** from: the Player tab ("clip" button: from = mark-in, to =
  mark-out, POV = current), any rerouted hub link (below), generated sets.
- **Edit**: label, trim from/to, POV; reorder (drag); delete.
- **Export**:
  - All entries from **one demo** → one hub link with repeated `clip=`
    params (hub's native multi-clip, link and embed).
  - **Multi-demo** playlists cannot be a single hub URL (clip is per
    demo) → export as a link to *our* Player tab carrying the playlist in
    the URL fragment (`#playlist=<compact encoding>`), plus per-demo hub
    links as a fallback.
- **Play in page (scripted)**: a loop over entries — `demoJump(from)`,
  `track(pov)`, play, pause/advance when the clock passes `to`. Across
  demos → reload the player iframe with the next demo's bytes.
- **Persist**: `localStorage` for the working playlist; the URL fragment
  is the share format.

### Generated playlists

From the current demo's Result: quad runs (powerup runs), best quadbores /
discharges / telefrags / airgibs (`highlights`, v76), top kills, a
player's frags/deaths, key moments. Cross-demo sets ("highlights of
milton over the season") need many demos analysed → an mvd-api
query, later phase.

## Rerouted hub links

All hub links already go through `hubReplayUrl()` (`app.js:7020`) — one
place to change. Proposal:

- Keep the `<a href>` pointing at hub, so **ctrl/cmd/middle-click** still
  opens hub in a new tab (native browser behaviour, no code).
- **Plain click** → open in the Player tab at that clip.
- **Shift-click** → append to playlist (toast "added"), stay on the page.
- Link text/tooltip says so ("▶ play · shift: add to playlist").

## Phases

1. **Engine + sync** — DONE: `mvd-fte-player` port, Player tab, two-way
   clock with the unified timeline, speed buttons, POV bar.
2. **Panes**: pane registry + column (add/close/move/persist), Map pane,
   per-player stack pane.
3. **Playlist**: add/edit/reorder/persist, rerouted hub links, single-demo
   `clip=` export, in-page playback.
4. **Generated playlists** from the current demo.
5. **Multi-demo** playlists + share fragment; cross-demo generated sets
   via mvd-api.

Docs to touch as phases land: `mvd-web/README.md` (tab + panes + playlist),
`RELEASE_NOTES.md` (unreleased entry), top-level README tab list.

## Branch upkeep

`player-tab` stacks on `gl-map`. When `gl-map` gets new commits, merge
`gl-map` into `player-tab` (merge, not rebase — both are pushed). When
`gl-map` lands on main, merge main into `player-tab` and retarget its PR
to main.

## Open questions

- Plain click vs shift-click: is "click = play here, shift = add" the
  right default, or the reverse?
- Multi-demo playlists: in scope for the first PR, or phase 5 later?
- Mirror the engine + Quake assets ourselves (licence check), or keep
  depending on `a.quake.world`?
