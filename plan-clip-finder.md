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
- **A pane.** The finder and the playlist are panes like the others,
  under the player by default; any pane can sit in either column.
- **Understandable by motivated players.** Presets are for everybody, so
  the finder shows its stages and their counts, and documents every
  source, param and field from the API reference itself rather than its
  own help text.
- **A general boolean expression.** `where` is a tree — `{and: [...]}`,
  `{or: [...]}`, `{not: …}` and conditions — stored as JSON (presets, share
  links, and later the API). The builder edits the tree directly (nested
  groups with an and/or toggle and a not); text mode parses an expression
  with jsep (MIT, AST only, nothing is evaluated) and converts that AST to
  the tree, rejecting anything else (calls, assignments).

## Query

```js
{
  source: 'events',                       // events | highlights | backpacks | top-kills | top-windows | lives
  params: { types: 'powerup' },           // the endpoint's query params, API names and form
  where: { and: [{ field: 'detail.powerup', op: '=', value: 'quad' },
                 { not: { field: 'detail.frags', op: '<', value: 1 } }] },
  rank:  { by: 'detail.frags', desc: true, limit: 5 },             // optional
  clip:  { from: 'time', before: 3000, to: 'detail.endTime', after: 2000, pov: 'player' },
  label: 'Quad run · {player} · {detail.frags} frag{detail.frags|s}',
}
```

Ops: `= != < <= > >=` (numbers compare as numbers, text case-insensitively),
`~` (substring on text, membership on a list) and `in` (equals any of a
list). An empty `and` is true, an empty `or` false. Unset `clip` fields take
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
2. **Saved presets** — done: "My presets" in the Generate menu and the
   finder's *Load…*, `localStorage`, shared as `#clipquery=` links.
3. **The pane, builder mode** — done, as a collapsible section above the
   playlist — source picker, the endpoint's params, the
   where tree (groups with an and/or toggle, a not, nested groups), rank, clip padding,
   live result count / preview rows, "Add to playlist" and "Save as preset".
   Field pickers list the fields the endpoint's rows actually carry.
4. **Text mode** — done (`src/lib/clip-expr-text.js`, jsep 1.4.0): the where tree as an expression
   (`weapon = "rl" and (pickerRelation = "enemy" or not pickerHadWeapon)`),
   parsed with jsep, round-tripping with the builder.
5. **Usability pass** — done: the finder and the playlist became panes
   (a second column under the player); an editable name with Load / Save /
   Link / Add on top; four boxed stages (find rows → keep rows where →
   order → clips from each row) joined by the count each passes on, the
   filter's Builder / Text switch on its own row; the API reference's own
   text as tooltips, value lists and an ⓘ page per source (`api-docs.json`,
   generated and drift-pinned by `mvd-api/webdocs_test.go`); *watch* offers
   the fields that name a player; *Code* edits the whole query as text,
   one line per stage (`src/lib/clip-query-text.js`), which is also a
   readable form to share. Order without a field keeps the earliest N.

Later, separate: the multi-demo search over mvd-api.
