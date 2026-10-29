# Plan — one Map / Player tab with preset views

Branch: `web-svelte`. Follows [`plan-player-next.md`](plan-player-next.md).
Owner, 2026-10-29: the Map tab is "more or less the same as player" —
merge them; two preset views, *mainly map* and *mainly player*, replace
the two tabs; the Map tab's Players (team stacks — better than the Player
tab's Players pane), Items and Region status panels become panes; a
fresh visit starts in the Map preset.

## Steps

1. **The engine only with a Player pane.** The FTE engine loads while a
   Player pane is open on the tab and is dropped (after a short grace, so
   moving the pane does not reload it) when there is none. Without it the
   page's own playback drives the clock on this tab too — instant
   scrubbing, −1× — and the playback bar's play / speed run it. Playing a
   clip (a play link, a moment, the playlist) opens the Player preset if
   no Player pane is open.
2. **The Map tab's panels as panes.** *Players* (the Map tab's legend:
   per team, sortable, loc / stack / weapons / play link — replaces the
   Players pane), *Items* (up or the respawn countdown) and *Region
   status*; click a player to watch (engine) or follow (map).
3. **The map pane does what the Map tab's map does.** Sized for the main
   column (up to the viewport's height), the toolbar's controls in its
   right-click menu (follow a player when there is no POV, trails per
   player and their length, clear, learn mode with an Entities pane, region
   focus), learn mode from `?learn=1`.
4. **Presets replace the tabs.** One tab ("Watch"), a *Map | Player*
   switch in its head; each preset is a saved pane layout (rearranging
   edits the preset in use; *Reset* restores it). `?tab=map` /
   `?tab=player` open the preset (old links keep working); the URL writes
   the preset in use. The Map tab, its component and CSS, and what only it
   used, go.

Each step: `make test`, build, headless checks, docs, push.

## Status (2026-10-29)

Built and pushed: 1 engine only with a Player pane 2ccde0a · 2 the Map
tab's panels as panes 7a291e4 · 3 the map pane does the Map tab's map
c795347 · 4 presets replace the tabs (this commit). The tab is labelled
**Watch**. Verified headless (Playwright + SwiftShader).
