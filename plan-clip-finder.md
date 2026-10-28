# Clip finder — plan

A more expressive way to build Player-tab playlists than the fixed Generate
menu: a query over one demo's data, built in a pane, with the common queries
as presets in the existing dropdown and a user's own queries saved beside
them. Code: `mvd-web/src/lib/clip-query.js` (model + runner),
`mvd-web/README.md` *Clips and the playlist* (user-facing description).

## Decisions

- **A transparent pass-through to mvd-api.** A query names one endpoint
  under `/v1/demos/{id}/` and its query params by their API names; rows and
  fields are exactly what that endpoint returns (`mvd-api/openapi/openapi.yaml`
  is the reference). The finder adds filtering, ranking and clip windows
  over those rows — no detection of its own. A highlight the API does not
  have (the Clipper snapshot's "LG glued", "survived") is not invented
  here; if one is wanted, it is added to the view / API first.
- **Single demo.** The query runs on the loaded demo, through the same Go
  views the API serves (`DemoHandle.view` → the worker's whitelisted
  exports). Searching many demos is a separate, later extension (the tool
  then becomes a single-demo deep dive inside a wider one); because params
  are kept in API form, that version can send a stored query to the REST
  API unchanged.
- **A pane** in the Player column, probably above the playlist; placement
  is settled once it exists.
- **OR, no nesting.** `where` is a list of groups ORed together, each a
  list of conditions ANDed. Arbitrary grouping waits for a use case.

## Query

```js
{
  source: 'events',                       // events | highlights | backpacks | top-kills | top-windows | lives
  params: { types: 'powerup' },           // the endpoint's query params, API names and form
  where: [[{ field: 'detail.powerup', op: '=', value: 'quad' }]],   // OR of AND-groups
  rank:  { by: 'detail.frags', desc: true, limit: 5 },             // optional
  clip:  { from: 'time', before: 3000, to: 'detail.endTime', after: 2000, pov: 'player' },
  label: 'Quad run · {player} · {detail.frags} frag{detail.frags|s}',
}
```

Ops: `= != < <= > >=` (numbers compare as numbers, text case-insensitively)
and `~` (substring on text, membership on a list). Unset `clip` fields take
the source's defaults (`SOURCES` in clip-query.js).

## Steps

1. **Model and runner** — done. The seven Generate entries are presets;
   checked against the old generators on all 19 golden demos (359 clips,
   identical windows, POV and labels) before the generators were deleted.
   WASM gained `getLives` / `getHighlights`; the worker whitelists those and
   `getEvents`.
1b. **Pack drops** — done. The drop↔pickup join moved into the pipeline
   (schema v77, `backpacks[]` carries `fate` / `picker` /
   `pickerRelation` / `pickerHadWeapon` / `pickerKills` on every demo), so
   the four pack presets are plain `/backpacks` queries; a query's `clip`
   may be a list (the drop from the dropper's view, then the pickup).
2. **Saved presets** — save the current query under a name ("My presets" in
   the dropdown, `localStorage`), share one by link.
3. **The pane, builder mode** — source picker, the endpoint's params, the
   where rows (with an "or" divider between groups), rank, clip padding,
   live result count / preview rows, "Add to playlist" and "Save as preset".
   Field pickers list the fields the endpoint's rows actually carry.
4. **Text mode** — the same query as editable text (JSON or a compact
   one-line form), round-tripping with the builder.

Later, separate: the multi-demo search over mvd-api.
