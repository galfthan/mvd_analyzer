# Release notes

Feature-level changes as they land on `main`, newest first. Dates are
the merge dates on `main`; schema bumps reference
[RESULT_SCHEMA.md](mvd-analytics/RESULT_SCHEMA.md) for field-level
detail.

## unreleased (hot-windows) — hot windows and lives, schema v65

**Two new read endpoints, purely additive.** Both are **views** over data that
was already there. The stored `Result` gains exactly **one** field —
`frags.killsMeasured`, below — and nothing existing changes shape, name or
meaning, so v65 is the cache-key tick every observable change earns rather
than a migration.

- **`GET /v1/demos/{id}/hot-windows` — each player's best stretches of the
  match.** The contract is one sentence: *"in these `windowMs` milliseconds
  this player scored higher on `metric` than in any other stretch of the same
  length."* `metric` is any **summable** per-event quantity — `frags`,
  `deaths` (which finds a player's *worst* stretch), `netFrags`,
  `damageGiven`, `damageTaken`, `netDamage`, `shots`, `hits`. Ratios such as
  accuracy are deliberately absent: they do not sum, so "the best window" is
  undefined for them, and they ride the per-window stats block instead.
  Windows are anchored at **real event times**, not a grid, so "the best 30 s"
  is exact; they are non-overlapping per player and come back as one flat list
  sorted by score. `windowMs` is the whole knob (5000 for damage bursts, 30000
  for hot streaks, 120000 for map-phase dominance) — there is deliberately no
  adaptive mode, because "why did I get *this* segment?" had no short answer,
  which is fatal for a surface an agent has to justify to a human.
- **Two independent caps, applied in one order.** `perPlayer` (max rows from
  any one player) runs **before** `limit` (the total), so
  `perPlayer=3&limit=10` means "the top 10, at most 3 from anyone" rather than
  "the top 3 of the first three players". Omitting them gives `limit=10` and
  an uncapped `perPlayer`, making a bare call a pure match-wide top 10 in
  which one dominant player may legitimately own every row.
- **Both caps follow ONE rule: `0` is a 400, not "uncapped".** An omitted MCP
  integer argument arrives as `0`, and reading that as "give me everything"
  would make a forgotten argument look deliberate. On `limit` *and* on
  `perPlayer`: omit for the default, negative for uncapped, explicit `0`
  rejected — plus, for `limit`, anything above 200 is likewise **rejected
  rather than silently clamped** (the v59 ruling).
- **`windowMs` is bounded on both sides.** `0` and any negative value earn the
  same `windowMs must be >= 1` 400 (one message, so an agent told to retry
  does not fail twice), and anything longer than the match duration is a 400
  naming the bound. Unbounded, it made the documented `end = start + windowMs`
  unsatisfiable: `windowMs=2147483647` returned a 24.8-day "window" carrying
  the whole match's stats, on which any derived rate is nonsense.
- **The score threshold is `minScore`, not `min`.** A bare, undimensioned
  `min` read as a duration next to the sibling `/lives`' `minMs`; every other
  filter in the API carries its unit (`minMs`, `minDwellMs`).
- **`dmg=both` is rejected on both endpoints, for every metric.** `both` is a
  `/damage` response *shape* — raw fields plus a parallel `bounded` nest — and
  one interval stats block carries one set of damage numbers. It is a 400 at
  the HTTP boundary whatever the metric, so the OpenAPI enum for these two
  endpoints is genuinely two-valued rather than 400-ing or silently ignoring
  the same value depending on `metric`.
- **`weapons=` scopes the SCORING events only.** The stats block still
  describes everything that happened in the window, so
  `metric=damageGiven&weapons=lg` can report `score` 445 beside a
  `damageGiven` of 650 — the interesting part of a best-LG window is often
  what *isn't* LG. The envelope's **`scoredBy` `{metric, weapons, dmg}`** is
  what tells the two numbers apart. It sits on the envelope, **not on each
  row**: one query means one rule, and a per-row copy would repeat the same
  object up to `limit` times. It is also the only place the metric is echoed —
  there is no top-level `metric` field, because naming one concept twice
  invites the two copies to disagree.
- **`GET /v1/demos/{id}/lives` — one row per spawn-to-death run**, the natural
  unit of QuakeWorld analysis, segmented by v64's `streams.players[].alive`
  and carrying the same per-interval stats block plus `endReason`,
  `spawnLoc`/`deathLoc`, `killedBy`, `deathWeapon`, `itemsTaken` and
  `weaponsHeld`.
- **Lives PARTITION the match, and that is load-bearing.** Life *n* is
  attributed every event from its own start to the start of life *n+1* (match
  start / match end at the edges), so per-life `kills`, `deaths`,
  `damageGiven`, `shots`, `itemsTaken` and the rest sum **exactly** to what the
  per-event **logs** hold for that player on an unfiltered call — `/frags`'
  `frags[]` rows on the frag side, `/damage`'s **non-summary** aggregate on the
  damage side (not its `events[]` rows: a telefrag or stomp folds its value
  into the totals without a per-hit row of its own). That is not always the same
  as the `byPlayer` scoreboards: a death detected from `DF_DEAD`/`STAT_HEALTH`
  with no obituary counts there and leaves no log row for any life to carry,
  so on `wipeout_3on2_red_vs_blue[q3dm6qw]` three players' per-life deaths sum
  to 3/4/7 against a `byPlayer` 4/5/9 — and the frag log itself holds 3/4/7.
  Three measured facts forced the partition:
  a rocket in flight when its shooter dies still kills (five landed 76–197 ms
  after the killer's own death on `211805`); KTX records deaths on an
  already-dead player, so dropping dead-gap deaths made per-life deaths sum to
  49 against a match total of 52 (`212260`); and `alive[0].start` is clipped
  to first *observation*, not to match start, so a telefrag at `t=0` belonged
  to no life at all. A **filtered** response does not reconcile — `minMs` drops
  a life together with the events in its window, and `from`/`to` select lives
  that overlap the window while each still carries its whole attribution
  window.
- **`deaths` on a life is not a 0/1 flag, and does not imply `endReason`.** It
  counts the frag-log death rows attributed to the life, so it is 0 whenever
  no row names the player at that instant — including on lives whose
  `endReason` *is* `death` (four such rows on
  `wipeout_3on2_red_vs_blue[q3dm6qw]`) — and it can **exceed 1**: a life
  carries both the death that ended it and any death recorded during the dead
  gap that followed, the KTX `dtTELE2` pent deflection. Measured across the
  11 558 cached-corpus lives, 12 rows report 2 and one reports 3.
- **`endReason` (`death` | `matchEnd` | `leftGame`) is new** because an absent
  `killedBy` used to conflate three different situations: a death nobody's
  obituary named (reachable, though not observed in the 42-demo cached corpus:
  0 of its 11 364 death-ended lives), a player still alive at match end,
  and a player whose observed presence simply stopped. `durationMs` stays
  **alive time** while the counts cover the wider attribution window — a life's
  duration is how long the player lived, not how long their rockets were in
  the air — so a rate derived by dividing one by the other reads **high**:
  slightly when summed across a whole match, by tens of percent on a single
  row where a short life is followed by a long dead gap. Every row therefore
  also publishes that window as
  **`attrStart`/`attrEnd`**. Divide by `attrEnd − attrStart` for an exact
  rate; the pair also makes the partition property measurable, since one
  player's windows tile the match end to end.
- **A `measured` block rides the envelope of both responses**:
  `{frags, damage, shots, locs, items, liveness}`, never `omitempty`. Every numeric stat
  is emitted **including a measured zero**, so `damageGiven: 0` on its own
  cannot tell a player who dealt none from a demo with no damage stream —
  measuredness is read from that block and **never** from a field's absence.
  This is the v55/v63 discipline (`RESULT_SCHEMA.md`'s own "never infer
  measuredness from `omitempty`") made structural rather than inferred.
  Availability is per-field: a demo with no position track omits
  `locs`/`eventLocs` and serves the rest, `/hot-windows` 422s only when the
  chosen metric's own source is missing, and `/lives` still yields lives on a
  demo with no damage stream.
- **`frags.killsMeasured` — the one stored-shape change, and the reason
  `measured.frags` means what it says.** Whether kill *attribution* was
  observable at all is a demo-global verdict, and it was already being decided
  (`analyzer.killsMeasurable`) and applied on `/player-stats` since v62 — but
  only there, because nothing stored the answer. It is now a field on
  `FragResult`, emitted on `/frags` and on the `frag` artifact, and read
  verbatim by the interval views. `false` means the frag log is **empty on a
  demo where players demonstrably died**: every obituary went unmatched, so
  `kills: 0` beside `deaths: 92` is not a measurement. It is demo-global and
  **survives every filter** — narrowing the log cannot make a demo's
  obituaries matchable — and it is the value both endpoints publish as
  `measured.frags`, so the two cannot drift. The verdict is decided from the
  protocol death tally (`FragResult.ByPlayer`), not the match scoreboard: the
  scoreboard's deaths column is written by the same post-processing fold that
  stores the verdict, so a scoreboard read would see zeros and come out `true`
  on exactly the unmatched-obituary demos the field exists to flag.
- **`measured.liveness`, and `/lives` now 422s rather than lying.**
  `streams.players[].alive` distinguishes `null` "liveness was not measurable"
  from `[]` "measured, and never alive", and `/lives` emits no rows for
  either — so on a demo where liveness was never measurable the old response
  was `{"lives": []}`, which reads as "nobody ever lived". That is a different
  and false claim. `/lives` now answers **422 `lives_unavailable`** there, the
  same shape every other unavailable capability uses, and the flag carries the
  same fact on the responses that do get served. A caller therefore only ever
  sees `liveness: false` on `/hot-windows`, which does not need it.
- **`dmg` on `/hot-windows` now genuinely applies under every metric.** It
  selects the family of the whole **stats block**, and the stats block reports
  damage whichever metric selected the window — so `metric=frags&dmg=bounded`
  had been silently serving **raw** damage figures. Fixed: the requested
  family is applied under every metric, the REST `bounded` default takes under
  every metric, and an explicit `dmg=bounded` on a `skipped:*` demo is a
  `422 bounded_unavailable` under every metric (a *defaulted* one still falls
  back to raw, as on `/damage`). Both endpoints now also echo **`dmg` and
  `boundedMode` on the envelope**, exactly as `/damage` does: without them
  `damageGiven` on a `metric=frags` row was a number with no stated family,
  and a defaulted request could not tell whether the bounded default took.
  Both echoes are absent only on a demo with no damage stream.
- **`/lives?from=20000&to=1000` now returns `lives: []`.** An inverted window
  selects nothing on `/frags`, `/damage` and `/hot-windows`, whose per-event
  `time >= from && time <= to` is trivially empty; `/lives`' overlap test
  instead kept every life straddling both bounds, so the two interval
  endpoints disagreed on the same query. Rejecting the range is the HTTP
  layer's call to make, not the view's.
- **Positional kills score under every metric they contribute to.** Telefrag
  and stomp value is added to the stats block's `damageGiven`/`damageTaken`
  exactly as `/damage` reports it, and it scores on the same terms — so absent
  a `weapons` filter a window's `score` equals the same-named field of its own
  stats block exactly, under the damage metrics as under `frags`. `weapons=`
  is the only thing that makes the two diverge, and it selects positional kills
  too: `weapons=tele` scores telefrags alone. The one exclusion is
  `damageByWeapon`, which matches `/damage`'s own `byWeapon` — a positional
  kill carries no wire weapon to file it under.
- **`water` and `drown` are now accepted interchangeably** in both the frag
  and damage weapon vocabularies. It is one event the two logs spell
  differently — a pre-existing wart that hot windows made visible, since one
  `weapons=` now sits beside a `metric=` that picks the vocabulary. Purely
  additive: no emitted token changed and no existing request breaks.
- **Pauses need no correction and none is applied.** Every time here is
  match-relative, i.e. on the game clock, which freezes during a pause; a 30 s
  window is 30 s of play, and subtracting pause time would double-correct.
- `timelineAnalysis.fragStreaks` is the top-10 projection of `/lives`, ranked
  by frags and carrying only a count and an `ewep`. It stays — removing it
  would be a genuine break — but anyone wanting every life, or any stat beyond
  frags, should read `/lives`.
- **`/lives` has a `summary` size control, and MCP defaults it on.** A whole
  4on4 match is ~400 lives (241 KB on `212260`), and `from`/`to` only *select*
  rows there — each kept row still carries its whole attribution window — so
  they are not a size control. `summary=1` keeps every row and every scalar
  and drops the per-row breakdown collections (`itemsTaken` 45 KB, `locs`
  29 KB, `eventLocs` 7 KB, `victims`/`byWeapon`/`damageByWeapon` 11 KB).
  `getLives` defaults it to true with the usual `hint`, like
  `getDamage`/`getAim`/`getItems`. One consequence: under `summary` the
  per-row `itemsTaken` is `null` regardless of the demo and says nothing —
  `measured.items` on the envelope stays the authority.
- **Liveness is finally reachable over the API, and a `/state-at` position
  says how old it is.** v64 made `streams.players[].alive` the canonical
  liveness and the schema told consumers to read it rather than re-derive it
  from the `sp`/`d` markers — advice no API consumer could follow, because no
  endpoint served the field (there is no `streams` artifact either). Meanwhile
  `/stream-slice` served the very markers whose obvious re-derivation
  ("alive iff the last spawn is strictly later than the last death") is the
  latching bug v64 deleted from this codebase, worth 100.7 s of one player's
  match. Two additive view fields close that:
  - **`/stream-slice` players carry `alive`** — the stored lives clamped to
    the window exactly as `rl`/`lg`/`q` are. All three states survive the
    clip: `null` = liveness was not measurable, `[]` = measured and never
    alive **in this window** (including when a life list clips to nothing),
    `[…]` = the lives, with the life boundaries a same-millisecond
    death+respawn still produces.
  - **`/state-at` rows carry `alive`** as the same three states at an instant:
    `true` / `false` / `null`.
  Neither is `omitempty` and neither is field-gated — `null` is a state, so a
  key that could be omitted would also mean "you didn't ask", which is exactly
  the inference the measuredness discipline forbids.
- **`/state-at` also carries `posAgeMs`.** The row reports a player's nearest
  position sample with no staleness bound, so on a POV recording it will
  happily answer "X was at the RA" from a sample tens of seconds old — while
  `/region-control`, `/loc-graph` and `/loc-trails` have discarded a sample
  older than 250 ms since v64, so the four endpoints answered "where was X at
  t" differently. *Which* position is reported does not change (the raw
  carry-forward is deliberate — this endpoint hands over the evidence and lets
  the caller judge); what is new is the evidence age: `time` minus the snapped
  sample's timestamp, signed, so a negative value says the nearest sample is a
  *later* one. Apply `>= 250 ms` to it and `/state-at` agrees with the
  occupancy endpoints.
- MCP gains `getHotWindows` and `getLives`. The golden corpus moves by exactly
  two lines per file — the `schemaVersion` tick and the new
  `frags.killsMeasured` — and by nothing else, which is what "both are views"
  buys: no analytic value in the stored `Result` changes; the two view-layer
  additions above are likewise invisible to it.

## unreleased (alive-gated-occupancy) — loc/region occupancy stops counting corpses, schema v64

**A correctness fix.** Values move in `locGraph`,
`timelineAnalysis.regionControl` and `/loc-trails`; no existing field
changed shape or name, and no meaning is *replaced* — occupancy fields are
narrowed to the semantics they were always meant to carry ("time spent in a
loc" is now documented as "**alive, observed** time spent"), which is
exactly the correctness-fix case the compatibility policy keeps in `/v1`.
If you have pinned these numbers, expect them to fall.

- **Dead players were being counted as present — and usually as *armed*.**
  They keep streaming position at full rate (`mvdsv/src/sv_demo.c:1481-1519`
  writes `svc_playerinfo` for every `cs_spawned` client), and on a gib the
  player entity *itself* becomes the bouncing head
  (`ktx/src/player.c:1070` `ThrowHead`), so a corpse's travels were tallied
  as loc time, as region control, and as movement edges. RL/LG
  possession spans death (`StatItems` bits clear only at respawn), so the
  corpse counted as armed. `regionControl`'s own comment claimed `Li==0`
  marked dead players; it never did. Expect region percentages and `byPlayer`
  ms to drop by roughly (deaths × death-to-respawn time) per player.
- **New `streams.players[].alive`** — each player's lives as half-open
  intervals, the one canonical "was this player alive at t", derived from the
  fused spawn/death markers. Deaths come from three detectors, and the
  obituary path (`mvd-reader/parser/stats.go` `forceEmitDeath`) exists
  precisely because the other two miss deaths, so liveness is derived from the
  markers rather than from any single wire flag. Not `omitempty`: `null` /
  `[]` / `[…]` are three distinct measuredness states.
- **`locGraph` node time is now an exact time-weighted integral**, replacing a
  forward difference clamped to 50 ms. Posture splits at interval boundaries
  instead of snapping to sample instants, so an RL pickup landing between two
  samples divides the interval exactly. The clamp itself was near-inert
  (measured 0–96 ms lost per 60 s), so the corpse exclusion is what actually
  moves the numbers.
- **LOS and aim now read `alive` instead of re-deriving liveness — fixing a
  latching bug.** `analyzer.losAliveAt` and `aimcore.aimAliveAt` were
  byte-identical copies of the rule *"alive iff the most recent spawn is
  STRICTLY later than the most recent death"*. That rule latches: when a death
  and the respawn it triggers share a millisecond the two are equal, so it
  reports dead — and keeps reporting dead until some later spawn arrives, i.e.
  for the whole remaining life, not for an instant. Measured across the cached
  corpus: 100.7 s of one player's 1143.7 s match (8.8%), 46.9 s of another's,
  4.3 s and 3.4 s elsewhere; 16 of 20 demos were unaffected. `losAliveAt` is
  deleted outright; `aimAliveAt` survives by name only — it is now a
  three-line binary search over `PlayerStream.Alive`.
  Visible effect: on `4on4_jah_ahoy_170526_defer_reconnect`, `biggz`'s
  crosshair error improves on every affected sample — 117.6° → 3.7°,
  64.6° → 13.3°, 63.9° → 35.1° — with the sample count unchanged. He was
  aiming at `[nW].Veggie`, who had been wrongly dead since 349400 ms, so those
  shots were being attributed to whatever distant player remained. A
  117-degree "aim error" was never real.
  `view.playerActiveInWindow` is deliberately NOT migrated: it asks whether a
  player appears anywhere in a bucket window, which is a different question,
  and it already resolves the tie correctly.
- **`/loc-trails` is gated too.** A residence is dwell, i.e. presence, so it
  now truncates at a death and resumes at the respawn. Without this the same
  corpse travels loc-graph excludes would still have shown up as dwell and the
  two endpoints would have answered the same question differently.
- **`region-control`'s `bucketStates` strings change as well**, not just the
  stats block: both walkers share one classification point, so a bucket in
  which every present player is dead now reads `_` (empty) instead of
  controlled or contested. The web Region Control timeline shifts accordingly.
- **Sample-and-hold is now bounded, which the deleted 50 ms clamp had really
  been doing.** A position sample's evidence expires after 250 ms
  (`result.SampleStaleCapMs`); beyond that the player's location is unknown and
  credited to nobody. This is inert on server-recorded demos (the worst gap
  anywhere in the golden corpus is 74 ms) and decisive on **POV recordings**,
  where only players inside the recorder's PVS get `svc_playerinfo`: on one such
  demo the recorder had a 152 ms worst gap while the other seven players had
  gaps up to **73 seconds**, and holding across them credited ~92% of a player's
  loc time to wherever they were standing when they left view. `regionControl`
  had this defect too, since v59.
- **Both walks now end at a player's end-of-track** (`result.TrackHoldEnd`: the
  last position sample held for one measured cadence, capped at 250 ms), so an
  early quitter no longer holds their final loc and region through to match
  end. This was the sharper half of the bug: region control evaluates each
  interval at its LEFT endpoint, so a departed player's final sample credited
  them everything up to the next event — on a recording that ends before the
  match window, the entire remaining match (measured: 60 s of phantom presence
  for a player who left after 2 s).
- **`streams.players[].alive` is a truthful LIFE list.** The underlying
  derivation starts everyone alive at `t=0` (deliberately — KTX emits a first
  spawn only on the first respawn), which is right for a player present from
  the start and wrong for everyone else, so it is clipped at the ends to
  observed presence; a late joiner would otherwise claim to have been alive
  before connecting. `playerStats` never saw this because it intersects with
  its own presence window.
  Two boundary rules matter for anyone reading it as lives: a **death splits a
  life even when the respawn lands on the same millisecond** (the intervals
  touch, so no dead time is invented, but the boundary survives — the ordinary
  interval algebra would have merged them and erased the death), and a **hole
  in the track does not split** one, because an unobserved stretch is not a
  death. Refusing to credit unobserved time is the walkers' job, not this
  field's. A reconnect gap inside a merged stream is still not represented.
- **The loc-graph teleport threshold is scaled by the real inter-sample
  delta** instead of an assumed 50 ms. The MVD sample rate is *not* fixed:
  mvdsv gates demo frames on `sv_demofps` (default 30,
  `mvdsv/src/sv_send.c:1339-1346`), and measured across the golden corpus the
  cadence is bimodal — ~13–16 ms on servers at full tick, ~34–39 ms on servers
  at the default. The docs' "~13 ms, virtually all gaps under 25 ms" was wrong
  for a third of the corpus and is corrected.
- **`/loc-trails` no longer re-merges dead gaps at `minDwellMs > 0`.** The
  short-dwell fold ran after the alive gate and welded residences back
  together across the gaps the gate had just cut, so at the MCP default
  `minDwellMs=250` a player alive in one loc 0–30 s but dead 10–20 s came
  back as a single 30 s residence — every default MCP caller was getting
  dead time re-credited as dwell, and only `minDwellMs=0` callers saw the
  documented behaviour. The merge is now gap-aware, and a residence that
  *follows* a gap is kept even when it is shorter than `minDwellMs`
  (folding it anywhere would hand the removed time back). Dwell totals on
  default `/loc-trails` and `getLocTrails` calls fall accordingly.
- **Spawn and death markers now count as presence evidence for `alive`.**
  The life list was clipped to the position track alone, but obituaries are
  broadcast to every recorder while `svc_playerinfo` is not: on a POV
  recording a player who left the recorder's PVS had their track stop and
  their later, marker-proven deaths fall *outside* every life (track to
  10 s, death at 63 s → `alive: [{0,10010}]`), which a consumer reading
  lives interprets as the player having left the game — dropping every
  later life. `presenceBounds` now widens the clip with marker evidence,
  and the ends stay deliberately asymmetric (a *death* before the track
  drops the low clip, a *spawn* before it does not, since a join proves
  nothing earlier). The high end distinguishes the two marker kinds: a
  trailing **death** extends the clip exactly to it (a death ends its life,
  so nothing after it is claimed), while a trailing **spawn** extends it to
  the end of the alive window — clipping at the spawn made the life it
  starts `[spawn, spawn)`, zero-width, and deleted it outright, so a
  respawned player's frags landed on a life the same response said had
  already ended. A truncated track past a trailing spawn is the same
  evidential state as having no track at all, and now degrades the same
  way. Server recordings have no track truncation, so goldens are
  unchanged.
- **`locGraph` edges are no longer recorded across unobserved holes.** The
  edge walk had no stale-gap reset: past `locgraphTeleportMaxGapMs` it
  skipped only the teleport *classification* and recorded the edge anyway,
  minting a `kind:"normal"` adjacency between the locs bracketing a PVS
  hole — worse than the old fixed-bound code, which at least branded such
  jumps `teleport`, so a consumer filtering teleports out kept exactly the
  invented ones. The cursor now resets past `result.SampleStaleCapMs`, the
  same bound the node walk credits time under. Edge counts drop on POV
  demos; inert on server recordings (worst corpus gap 74 ms, goldens
  unchanged).
- **`alive`'s three states now survive the API's demo cache.** `gob` omits
  zero-valued fields and a length-0 slice is zero, so
  `alive: []` (measured, never alive) decoded as `null` (not measurable) —
  and every consumer degrades `null` to ungated, so mvd-api's tier-2 cache
  answered liveness queries for such a player as if they were alive
  throughout, disagreeing with a cold parse of the same demo. `PlayerStream`
  now carries its own gob codec with an explicit measuredness flag. The JSON
  contract is byte-identical and goldens are unchanged; the wire change
  rides the v64-keyed cache tree, so no deployed cache holds old-format
  bytes (a stale local dev cache fails decode and falls back to
  re-analysis).
- **`result.TrackHoldEnd` is total.** It read the final sample before its
  length guard, so the exported policy function panicked on an empty track.
  It now returns 0 — no sample, no evidence, nothing to hold. Defensive:
  all five in-repo callers pre-check, so no served value changes.
- Sibling to the **v59** region-stats fix, which removed the same
  grid-versus-integral defect from region control and concluded it existed
  nowhere else. Loc-graph was missed. A new reconciliation test now pins loc
  totals to region totals so the two cannot drift apart again, and a
  25-mutation pass over this work added pins for four behaviours the whole
  suite (goldens included) had left unguarded: region control's stale cap,
  the LOS alive gates, the reconcile identity's unpaired-player direction,
  and `aimcore`'s liveness measuredness states.

## unreleased (api-stability-policy) — the compatibility promise, written down

Documentation only: no schema change, no code change, no served number or
response shape changed.

- **The API's compatibility policy is now stated where consumers can read
  it.** [`mvd-api/API.md` §2.7](mvd-api/API.md) is the canonical text, with a
  self-contained copy in the OpenAPI `info.description` (so it is served at
  `/docs` to integrators who cannot follow repo paths) and a short version on
  the Discord key portal's landing page.
- **The policy**: `/v1` grows additively — new endpoints, fields and enum
  members appear without announcement. A genuine break ships as
  `/v2/<endpoint>` served **alongside** `/v1` rather than replacing it, and
  old routes retire on a minimum of **8 weeks' notice**, in practice only
  once measured usage has drained. Clients must ignore unknown fields and
  enum values, and treat `openapi.yaml` as the contract.
- **A correctness fix is explicitly not a break.** When a field's value
  changes because it was being computed wrongly, the name and type are
  unchanged and the documented meaning is not replaced — at most narrowed
  to the semantics the field was always meant to have, with the docs
  updated to say so (v64: loc/region "time spent" → "**alive, observed**
  time spent"). Such changes stay in `/v1`, ride `schemaVersion`, and are
  called out here with the direction and rough magnitude.
- **Changing a parameter's default *value* is a break** and goes through
  `/v2` — a caller who omits the parameter would otherwise get different
  numbers without opting in. Widening a default *set* stays additive (see
  below): the caller keeps everything they had and receives more.
- **The MCP tool surface is covered by the same policy.** `mvd-mcp` and
  `mvd-api` deploy in lockstep, so tool names, parameters and result
  semantics move only under this process; new tools, parameters and result
  fields appear additively.
- **What the contract does *not* cover** is now written down: undocumented
  ordering, rounding of derived floats, `omitempty` behaviour, rate-limit
  thresholds, and which demos return `422 <section>_unavailable`. Adding a
  member to a *default* set (e.g. the v58 `demomark` event type) counts as
  additive even though it changes the rows a caller receives.
- Fixes the previous §2.7, which described `/v1` as a prefix that would be
  *bumped* on a break (implying replacement), and the stale
  "currently `38`" schema-version claim in the top-level README.

## unreleased (info-links) — API & MCP discoverability, admin contact

No schema change; no served number changed.

- **Web top bar gains an "API & MCP" link** to the portal
  (mvdanalyzer.com/portal), which already documents REST access, key
  sign-up, `/docs`, and the MCP endpoint — the app links rather than
  duplicating that text.
- **Portal shows the admin contact** (`nexusga` on Discord): a line on
  the landing page and in the shared footer on every portal page.

## unreleased (dmg-splits) — per-weapon team/self damage splits, schema v63

Additive: no existing field changed shape or meaning, and every number that
was already served is unchanged.

- **`damage.byPlayer[].byWeaponTeam` / `.byWeaponSelf`** (raw family and the
  `bounded` nest alike) split `givenTeam` and `givenSelf` by the
  **attacker's** weapon, exactly as `byWeapon` splits `given` — same keys,
  same telefrag/stomp exclusion (positional kills fold into the totals
  only). `matrix`, the top-level `damage.byWeapon` and the `enemyVs*`/`ewep`
  buckets stay enemy-only. The view's filtered recompute produces them too,
  so a windowed or player-scoped `/damage` response agrees with the stored
  artifact.
- **`playerStats.players[].damage`** gains the same two maps, summed into
  team rows by both aggregators (the stored one and the post-overlay
  re-aggregation).
- **The KTX overlays now read `weapons[].damage.team`.** KTX has always
  written the team counter beside `.enemy` in one sub-block
  (`ktx/src/stats_json.c:208-212`) and nothing consumed it. A bounded
  `/damage` summary badged `boundedSource: "ktx"`, or a `playerStats`
  damage family badged `src: "ktx"`, therefore served a *reconstructed*
  team split under a server-counter badge; both now carry KTX's own.
  `byWeaponSelf` has no KTX counterpart and stays derived.
- **Measuredness is documented, not inferred from `omitempty`.**
  `byWeapon` and `byWeaponTeam` are measured whenever the damage family is
  present; `byWeaponSelf` only where a damage stream was read, which is
  exactly what a non-nil `damage.taken` says. Within a measured family an
  absent key means "dealt none with that weapon" — the derived copy drops
  zeros, KTX keeps a measured 0 where the sub-block exists.
- **Web (Aim Stats):** the per-weapon **Dmg** column now follows the
  Enemy/Team/Self victim filter instead of being pinned to enemy damage.
  In **All** mode it sums the three splits when all are measured, and
  renders a `≥`-prefixed lower bound with a tooltip naming the missing
  split when one is not (a KTX-block-without-stream demo has no self
  split); `-` when nothing is measured. Measured zeros render as `0`.
- Golden corpus regenerated for the new fields.

## 2026-07-27 (ux-tweaks) — Summary/Timeline/Chat/Aim UI cleanup, schema v62 (unchanged)

Frontend only — no Go, no schema bump, no golden churn. Every number below
already existed in the Result; this is where the web app puts it.

- **Possession moved into "Item Pickups & Drops"** and the standalone
  Possession panel is gone. Each item is now a `took | s` group under a
  two-row header (RL/LG add `drop | xfer`), with the seconds coming from
  `hold.*` as before — the possession cell sorts on raw ms and its tooltip
  carries held ms, the share of alive time, the run count and the row's
  alive/present/match window (the denominators the share rests on). MH keeps a
  single count column: mega health is consumed on pickup, so there is no
  hold stat for it by design. The panel's methodology prose survives,
  condensed, in the Item Pickups explainer.
- **The Pickups tab gained the same possession seconds**, one `<kind> s`
  column after each kind's last count column. Possession is per KIND, not
  per spawn entity — the integral over the inventory stream knows only that
  the player held an RL, not which pad or pack granted it — so a map with
  two RL spawns still gets one `RL s` column, and the column ignores the
  all/first-pickup mode selector. MH has none, for the same reason as
  above. Rows join `playerStats` by player name and team name.
- **Timeline panels reordered** to Score → Powerups → Weapons →
  Health/Armor, with Region Control still last. The JS lists that mirror
  DOM order were resequenced with it.
- **Chat: "Hide team chat"** — a checkbox that drops `say_team` lines from
  the two chat columns. Off by default, reset per demo; frags and public
  `say` are unaffected.
- **Aim Stats tables gained a Dmg column** and two-row headers grouping
  each weapon's columns (Pellets / Shots / %). Damage is joined by player
  name from `playerStats.damage.byWeapon`, which is **enemy-only** — that
  column deliberately does not follow the Enemy/Team/Self victim filter,
  and its tooltip says so.
- **Key Moments: a powerup-run filter** — min length 5 s **and** min 1
  frag by default, both editable down to 0 and reset per demo. A UI-level
  display filter with visible controls; `timelineAnalysis.powerupEvents`
  is served complete, and a table emptied by the filter says that rather
  than reporting no powerups.
- **Team rosters sort by player name by default** on both the Timeline
  and the Map. The timeline roster's Player / Frags / Health / Armor
  headers are clickable (one delegated handler, since the tables are
  rebuilt on every playhead tick); the old frags-descending default made
  rows swap places while the reader was scrubbing.
- **First click on a numeric column now sorts descending** in every
  sortable table (`makeSortable`), matching what its comment always
  claimed and what the new roster sort does; text columns still start
  ascending, and repeat clicks flip as before.

## 2026-07-26 (playerstats) — `playerStats` learns to say "not measured", schema v62

Amends the v61 section before it ships. Every `playerStats` change below
replaces a confident zero with an absence, or names a degradation that had
none; the section closes with a separate fix that makes `match.map` the
canonical map shortname. Golden corpus regenerated: the movement is
`schemaVersion`, the new `accuracy` block on team rows, and the
`match.map` / `match.mapTitle` split.

- **The kill side of `score` is optional.** `kills`, `suicides`,
  `teamKills`, `byWeapon` and `efficiency` are all attributed from the
  obituary-derived frag log; `frags` (the wire net score) and `deaths`
  (protocol death events) are not. On a demo whose obituaries never
  matched, the five are now **omitted together** instead of being served
  as zeros. `4on4_l_vs_la[e1m2]` was the canonical case — a full 4v4
  scoreboard with 230 team frags and 121 deaths reporting 0 kills and
  0.0% efficiency on every row, byte-indistinguishable from a genuinely
  awful team — until the frag-log recovery in the entry below made its
  kill side measurable; the omission now guards any demo whose
  obituaries genuinely cannot be matched.
  **Consumers must render `-`, not `0`.** The condition is demo-global,
  so a team row never mixes a measured member with an unmeasured one.
- **`hold.armor` is omitted when the armor stream is empty.** `none` is
  the alive-time complement, and it was emitted whenever the alive
  window was known — including when no armor sample had ever been
  observed. On the POV recording `dag_caps_e1m2`, 7 of 8 rows asserted
  100% no-armor while the same rows listed their armor pickups. A player
  who genuinely never picked armor up still reports `none == aliveMs`;
  the change stream always carries its first sample, so the two cases
  are cleanly distinguishable.
- **`damage.src` is three-valued: `"derived:unbounded"`.** On a
  `k_midair` / `k_instagib` / `k_dmgfrags` demo the bounded
  reconstruction is skipped entirely, and the damage family silently
  became raw wire damage including overkill while still reading
  `"derived"`. Measured on `4on4_oeks_vs_tsq[dm2]`: raw runs 38-44% above
  bounded, and on instagib the wire value is a flat 5000/hit. Never
  compare an unbounded row's damage with a bounded one's. No demo in the
  test corpus carries those cvars, which is why the path went unmarked.
- **A zero-damage player now gets a zeroed `damage` family** on a demo
  that carries the damage stream. The reconstruction only creates an
  entry on an actual hit, so the row vanished — collapsing an observed
  zero into "unmeasurable", the inverse of what the same file does when
  it zero-fills `takenEnemy`.
- **Team rows carry `accuracy`.** They never did, so the web's per-team
  Weapon Stats column rendered `-` beside per-player rows showing real
  percentages — a regression against the deleted JavaScript, which summed
  it. `attacks` and `hits` sum per weapon over members; `hits` stays
  **absent** unless every contributing member measured it (mixing a
  measured member with an unmeasured one understates the team hit-rate
  under a number that looks measured), and `real`/`virtual` are not
  aggregated, since KTX omits the pair unless it recorded one.
- **`sources` is computed from the rows being served, after filtering.**
  It previously read `"ktx"` for a family if *any* row matched the KTX
  block, badging unmatched rows with a provenance they did not have, and
  `PlayerStats()` copied the roll-up verbatim into filtered responses,
  where it described rows that had been removed. It gains a third value,
  `"mixed"`, reserved as a **canary**: on a demo with a KTX block every
  roster row joins it, so a disagreement means a phantom roster row is
  back. It should never appear on healthy data.
- **KTX measured zeros in `damage.byWeapon` are no longer dropped.** The
  overlay skipped a KTX weapon whose `damage.enemy` was 0 and kept the
  reconstruction's number in its place — then stamped the family
  `src: "ktx"`. KTX emits the `damage` sub-block whenever either counter
  moved (`ktx/src/stats_json.c:208`), so `enemy: 0` is a measurement: a
  GL used purely for team splash was served as 700 *enemy* damage under
  a KTX badge, and `byWeapon` stopped summing to `given`. The non-KTX
  residual keys (`unknown`, `stomp`, `tele`, `explobox`) are deliberately
  kept — real measured damage, and on `1on1_bananfalco_betowen_240426_dm2`
  the `unknown: 4` residual is what reconciles `byWeapon` with KTX's
  `given`.
- **Unfiltered `/player-stats` can no longer return `"players": null`.**
  The filtered branch had built an empty slice since it was written; the
  unfiltered one appended to a nil. `players` is declared required and
  array-typed, so this was a live spec violation on any demo whose stream
  roster came out empty.
- **`accuracy` overlay documented and pinned.** KTX's block replaces the
  derived one wholesale rather than merging per weapon, because KTX's
  `attacks` is a pellet count for sg/ssg where ours is trigger pulls.
  Measured across all 42 cached corpus demos (228 rows): **zero** weapons
  present in the reconstruction and missing from KTX's `acc` set, so the
  swap is lossless in practice and no per-entry `src` is introduced.
- **Type changes** on the same fields, for measured-zero consistency:
  `ping` `int` -> `*int`; `members` `int` -> `*int`, so a team row always
  publishes it — including the `0` that a team whose only member never
  streamed needs most, since every `shareMatch` on it rests on
  `matchMs x 0`; `efficiency` `Share` -> `*Share`.
- **Outside the section:** `frags.byWeapon` and
  `frags.byPlayer[].byWeapon` now move with the Finalize teamkill
  re-classification, which previously adjusted only `kills` — so
  `sum(byWeapon) == kills` holds, as `score.byWeapon` claims. Fires on
  auth-name servers; no golden demo reaches it.
- **`match.map` is the canonical map SHORTNAME; the title moves to
  `match.mapTitle`.** `match.map` was the `svc_serverdata` level name run
  through a cleanup heuristic, which on most id maps yields the pretty
  title — `4on4_l_vs_la[e1m2]` reported `"Castle of the Damned"`, dm2
  `"Claustrophobopolis"`. Two names for one map meant `match` could not
  be joined against `demoInfo.map`, `metadata.serverInfo.map`,
  `searchGames` rows or any BSP / loc / geometry file key, and every
  consumer that wanted the identity had to route around it (`/overview`
  carried an explicit workaround; the web's `mapFileKey()` excluded
  `match.map` by name). **The rule: the short name is the map identity
  everywhere; the title is display-only data.** `match.map` now resolves
  through the same accessor every BSP-derived producer uses — the KTX
  demoinfo map, else the serverinfo `map` key — falling back to the
  cleaned title only when neither source names a map. The title is served
  verbatim as the new, additive `match.mapTitle`, omitted when
  `svc_serverdata` named no level. `/overview`'s `map`/`mapTitle` split is
  unchanged in shape (it already published these two values); the web
  topbar and Summary map cell now show `mapTitle` when present, and
  `mapFileKey()` gains `match.map` as a final fallback now that it is
  guaranteed short. Golden movement is confined to `match.map` and the new
  `match.mapTitle` key.
- **New regression net.** The `mvd-analytics/corpus/` special-cases
  harness gains four playerStats invariants — a hold key implies a
  non-empty stream, the kill side implies a non-empty frag log, a team
  row carries every family its members carry, and on a demo with a KTX
  block every roster row joins it with an agreeing `src`. They were red
  on 8 of the 12 local demos before these fixes.

## 2026-07-26 (playerstats) — pre-KTX demos get their frag log back

No schema bump. Layer 1 only; every consumer of `PrintEvent` benefits.

- **A `svc_print` payload is a console fragment, not a line.** QuakeC
  builds a line out of however many `sprint`/`bprint` calls the code path
  makes, and each one becomes its own `svc_print`. Old kmod/qwe emits an
  obituary id1-progs-style — `"DARKLORD"`, `"'s rocket"`, `"\n"` arrive as
  three messages — while modern KTX prints obituaries whole. Both obituary
  consumers (the parser's death-mining and the analytics frag log) matched
  per-message, so on a pre-KTX demo **nothing ever matched**.
- **`4on4_l_vs_la[e1m2]` went from an empty frag log to 368 entries**, one
  per death counted from the death stream, every one naming killer, victim
  and weapon. The 2003 duel `1on1_]apollyon[_vs_jogi_[dm4]` gains its
  fragmented obituaries too. This is what `playerStats.score.kills` and
  `efficiency` were reading as a confident `0` on all eight rows of a 4on4
  that scored 230 frags.
- **The parser now assembles fragments into lines** before emitting
  `PrintEvent` and before any obituary / pickup / match-start matching,
  following ezquake's `CL_ProcessPrint` rule
  (`ezquake-source/src/cl_parse.c:3072-3105`) with the buffer keyed by
  (print level, `dem_single` target) — ezquake sees one client's stream,
  we demultiplex the whole recording. A line that is already whole passes
  through byte-identically.
- **Modern KTX demos are unaffected in the data**, and the golden corpus
  is unchanged. Two families there were fragmented and now arrive as one
  event instead of 6-14 (the `PRINT_LOW` backpack pickup line, still
  `ktx/src/items.c:2404-2618`, and the end-of-match "top scorers" table);
  neither carries an obituary, chat line or pickup match.
- New regression net: `mvd-analytics/corpus/obituary_test.go` asserts a
  frag-log floor on the pre-KTX special-case demos (0 entries before this
  change), plus parser unit tests for fragmented obituaries, interleaved
  levels/targets, and end-of-demo flush.

## 2026-07-25 (playerstats) — the API cache stopped eating measured zeros

No schema bump; the tier-2 cache-format counter goes to `f3`, so cached
Results are re-parsed once on next touch.

- **`GET /v1/demos/{id}/*` served different bytes depending on cache
  warmth.** The tier-2 cache was a bare gob, and `encoding/gob` flattens
  pointers and omits zero values — so a `*int` holding a **measured
  zero** decoded as `nil`. Since every optional field in this schema
  means "absent = not measurable", a cache hit answered a different
  question than a cold parse: `damage.taken: 0` ("took no damage") came
  back absent ("we could not tell"), and so did
  `accuracy.byWeapon[].hits: 0` ("fired, never hit"),
  `pickups.byKind[].xferSelf: 0`, `damage.events[].bounded: 0` and
  `demoInfo.players[].control: 0`.
- **Pre-existing, and widened by the playerStats work.**
  `damage.events[].bounded` — whose own comment says "0 is a real value"
  — has had this flaw for as long as the gob cache has existed; the
  golden corpus (cold, JSON) carries 25 such events on one 4on4 that the
  live API returned none of. The pointer-heavy playerStats section took
  the exposure from 3 fields to 14, which is how it was noticed.
- **Tier 2 is now JSON by default, gob only for `Streams`.** JSON
  distinguishes `0` from absent and is the representation the golden
  corpus and OpenAPI spec already pin; `Streams` is 97% of the payload
  and decodes 40x slower as JSON, so it keeps gob. The failure mode is
  now the safe one — an optional field added anywhere outside `Streams`
  is correct by default, and `TestStreamsHasNoOptionalScalars` guards
  the one section that carries the constraint. Cost: +2.6% on disk,
  ~48 ms per tier-2 read.
- **Two more served-byte changes come with it**, same root cause, for
  anyone diffing responses across the deploy: a pointer to an all-zero
  STRUCT was dropped whole and now survives (`playerStats.speed`
  `{max: 0, avg: 0}`, `demoInfo.players[].bot` `{}`), and **negative zero**
  is preserved rather than normalised to `0` (`mapEntities` coordinates —
  obsidian's SNG spawner sits at `x: -0`).
- `TestCacheRoundTripPreservesServedBytes` pins the real invariant on the
  whole golden corpus: a cache hit must serve the same bytes as a cold
  parse.

## 2026-07-25 (playerstats) — the web summary tab moves onto `playerStats`

Still schema **v61** (additive within the same unmerged branch); golden
corpus regenerated for the two new maps.

- **The four-source JavaScript join is gone.** Every Summary-tab table —
  Basic Stats, Weapon Stats, Item Pickups and their per-team variants —
  now renders `result.playerStats` instead of joining `match.players`,
  `frags.byPlayer`, `frags.frags` and `demoInfo` in `app.js`. The merge
  happens once in Go, so the REST and MCP consumers get the same numbers
  the web shows.
- **A demo with no KTX block renders the full tab.** `displayScoreboardFallback`
  — frags only, no weapon or item tables — is deleted. A 2003 kmod duel
  now shows a complete scoreboard, possession times, per-weapon accuracy
  and item tallies where it previously showed a bare frag list.
- **New Possession panel**: RL / LG hold, RA / YA / GA hold, **time with
  no armor**, and quad / pent / ring, all as a share of time *alive*.
  Weapon hold time is absent from KTX's demoinfo block on a demo of any
  age, and "no armor" is a figure KTX structurally cannot produce.
- **The WASM entry point applies the KTX overlay before marshalling**
  (`withPlayerStatsOverlay`). The analyzer still stores the fully derived
  section; the overlay is a read-time step, and this is the web's read
  boundary, exactly as the REST handler is the API's.
- **`score.byWeapon` and `damage.byWeapon`** are new: per-weapon enemy
  kills (always from the corrected frag log) and per-weapon enemy damage
  given (KTX's where the block carries it, merged weapon by weapon). The
  Weapon Stats table's kills column previously showed KTX's count while
  the scoreboard beside it showed the frag log's — the two now agree.
- **Three visible number changes on the scoreboard**, all deliberate:
  `Taken` is now our ALL-SOURCES figure rather than KTX's enemy-only one
  (they are different quantities; the tooltip says so); team rows show a
  `Players` count instead of an average ping, and no `ToDie`, because
  averaging per-player averages across different death counts is
  meaningless; powerup seconds come from our hold integral rather than
  KTX's `item.time`, so they agree with the Possession panel. The two
  are **close but not identical**: across the 55 comparable powerup rows
  in the golden corpus the largest disagreement is 1.011 s, and 17 of
  those rows shift by one displayed second because we `Math.round` a
  millisecond integral where KTX truncates its own second counter (7
  rows still differ if we truncate too). Nothing here moves by more than
  a second.
- **One visible regression, since fixed.** The per-team Weapon Stats
  accuracy column rendered `-` on every demo: the deleted JavaScript
  summed `attacks`/`hits` over a team's members, and no `accuracy`
  family was ever built for a team row. The v62 section above restores
  it in Go, on both the analyzer aggregate and the read-time KTX
  overlay.
- **`isDuel()` reads `playerStats`**, so a pre-KTX-block 1v1 collapses
  the team panels correctly instead of falling through and rendering
  them. The canonical team→colour order (`timelineState.teams`,
  frag-sorted, winner at index 0) is seeded from `playerStats` too — the
  CLAUDE.md invariant is unchanged, only its input.
- Verified in a headless browser across 13 demos spanning 2003 kmod,
  2022 KTX, CTF, wipeout, hoonymode, FFA and race: no page errors, team
  colours consistent between the Teams box and every table, and the race
  demo (no match, hence no section) renders empty tables cleanly.

## 2026-07-25 (playerstats) — canonical `playerStats` section, schema v61

Adds a per-player and per-team statistics section computed for **every**
demo, with per-family provenance. Additive — no existing field changed
shape — but `CurrentSchemaVersion` bumps to **v61** and the golden
corpus was regenerated.

- **New `playerStats` section / `player-stats` artifact.** One row per
  player (and per team) carrying the corrected scoreboard, damage,
  pickup tallies, the KTX-only identity fields, and possession time.
  Each stat family carries `src` (`"derived"` | `"ktx"`), with a
  `sources` roll-up — the same provenance pattern `damage.boundedSource`
  established. The stored artifact is always fully derived; the KTX
  overlay is applied at read time.
- **Possession time is new information, not a re-shaping.** "Time with
  RL", "time with RA", and "time with **no armor**" are exact integrals
  over the native-rate possession streams, with explicit denominators
  (`window.matchMs` / `presentMs` / `aliveMs`) instead of KTX's unstated
  alive-time divisor. KTX never writes weapon hold time into the
  demoinfo block at all (`ktx/src/stats_json.c` emits acc/kills/deaths/
  pickups/damage only), so this was unavailable on demos of any age.
- **Our armor hold time is lower than KTX's, on purpose.** KTX's armor
  clock closes only on death or a different-type pickup, never when the
  armor is chewed to zero, so it keeps counting after the armor is gone.
  Measured on gameId 212423: KTX `ra` 213 s vs 129 s, 317 s vs 266 s.
  Expect a KTX end-of-match table to disagree.
- **Pack transfers are decomposed.** `xfer` (a teammate took your pack)
  and `xferSelf` (you took it back) sum to KTX's `xferRL`/`xferLG`,
  which conflates them. Derived, so they work on demos that carry the
  `//ktx bp` / `//ktx drop` hints but no demoinfo block; absent (not
  zero) when the hints are missing, and teamplay-only like KTX's gate.
- **`GET /v1/demos/{id}/player-stats` + MCP `getPlayerStats`** serve it,
  with `players` / `teams` filters. A `players` filter drops the team
  rows: they are whole-team sums and would misread as the filtered
  subset's totals. 422 `playerstats_unavailable` fires only on a parse
  degraded to no player streams — a missing KTX block is served
  normally, which is the entire point.
- **The KTX overlay is applied at read time**, in `view.PlayerStats`, so
  the stored artifact and the golden corpus always record what the
  pipeline computed. KTX wins on damage given/team/self/ewep, accuracy,
  and pickup counts; `taken` stays derived (KTX's is enemy-only and
  lands separately in `takenEnemy`); score and hold are never overlaid.
- **The section keeps ONE SHAPE across demo ages.** Where a wire-side
  reconstruction is possible at all it is emitted and marked
  `src: "derived"` rather than the field vanishing on a demo recorded
  before KTX embedded its block: `accuracy` from the decoded fire stream,
  `takenEnemy` / `takenToDie` from the per-hit damage log, `login` from
  the `*auth` userinfo key. A response whose shape changes with the
  demo's age forces every consumer into two code paths, and the old-demo
  path is the one nobody tests. The limit is honesty, not effort — a
  value that cannot be measured stays ABSENT rather than becoming a
  zero, so `accuracy.byWeapon[].hits` is omitted when there is no damage
  stream to link fires against, and KTX's `taken-to-die` 99999
  no-deaths sentinel is never served as a number.
- **`/demoinfo` stays the verbatim KTX pass-through**, now explicitly
  positioned as the audit trail `playerStats` is diffable against. One
  fidelity fix: `control` was a `float64` behind `omitempty`, so a KTX
  block recording `"control": 0.0` came out with the key *dropped* —
  indistinguishable from an older build that never wrote it. It is a
  pointer now, and a measured zero is served as `0`. Seven of the eleven
  golden demos record an all-zero control time and gain the key; three
  duels record nonzero values that were always emitted; one carries no
  `control` key at all and still doesn't.
- **Match start is detected on `"has begun"`, not `"match has begun"`.**
  kmod 1.58 / qwe 0.170 (2003-era) broadcast `"The duel has begun!"` —
  they announce the *mode*, not the word "match". The narrower pattern
  missed it, and because both stream sampling and the parser's
  obituary-death gate hang off that flag, such a demo silently produced
  **no streams at all and zero deaths**. A 2003 dm4 duel now reports
  48/13 and 10/52 with full possession times where it previously had no
  `playerStats` section and a 0-death scoreboard. No golden output moved
  — every corpus demo uses KTX's own phrasing.
- **`accuracy.real` / `virtual` are documented correctly.** They are
  KTX's `rhits` / `vhits`, present on rl/gl only, and count *victims
  damaged by a blast* — not a direct/splash split of `hits`, which for
  rl/gl is the direct-impact count. One rocket splashing three players
  adds three, so `real` routinely exceeds `hits` (a 2022 dm3 demo reads
  `rl: {attacks: 110, hits: 13, real: 55, virtual: 55}`). `virtual` is
  latched before godmode / pentagram / teamplay damage-avoidance, so the
  gap to `real` is damage *prevented*, not missed.
- **Pack transfers are gated on the MODE, not the `teamplay` cvar.** KTX
  gates on `isTeam()`; an FFA server can still run `teamplay 2`, and
  trusting the cvar there made "the dropper's team" trivially true and
  invented a transfer for every backpack anyone picked up. CTF keeps
  counting — its teams are real, KTX simply declines to measure them —
  so the `xfer + xferSelf == xferRL` identity holds on team games only.
- Verified end-to-end on gameId 71035 (a 2019 4on4 with no demoinfo
  block): full scoreboard, pickups, transfers and hold times. Also on a
  13-demo local corpus spanning 2003 kmod, 2022 KTX, CTF, wipeout,
  hoonymode, FFA and race: the `xfer + xferSelf == KTX xferRL/xferLG`
  identity holds exactly for all 16 players across the two team demos
  carrying both signals.

## 2026-07-25 (fix-roster-frags) — scoreboard by occupancy, schema v60

Three pre-existing defects in the roster / frag path, found by auditing
player counts across all 54 locally available demos: 50 agree and are
sensible, four disagree, by three distinct mechanisms. No field was added,
removed or retyped — `CurrentSchemaVersion` bumps to **v60** because served
*values* move and the API cache is keyed on the version. The golden corpus
was regenerated; two demos carry real value changes
(`2on2_pys_wd_250426_aerowalk` and `4on4_jah_ahoy_170526_defer_reconnect`),
the other eight only the version field.

- **A departing player keeps their score.** When the server drops a client
  it zeroes the slot's frags and broadcasts the cleared state in the same
  frame (`SV_DropClient` → `SV_FullClientUpdate`,
  `mvdsv/src/sv_main.c:419-428`, `:490-492` and `:509-511`), and because that
  happens
  *before* match end the zero used to be recorded as the final score. The
  scoreboard now takes the count from the mod's own departure broadcast —
  `"<name> left the game with N frags"` (`ktx/src/client.c:2843`) — and,
  where no broadcast exists, rolls back the reset that shares the drop's
  timestamp. Never the occupancy maximum: a frag is legitimately lost to a
  suicide. On `4on4_l_vs_la[e1m2]`, `shiva` (26) and `DARKLORD` (21) come
  back, and team `|l|` goes 57 → **104**, exactly the value the serverinfo
  `score=[.la.]230:[ |l|]104` key states.

- **Participation is evidence of play inside the match window**, replacing
  the end-of-demo `spectator` / empty-team gates. Those were wrong in both
  directions. A participant who goes spectator after the match lost their
  entire row — live in the committed golden corpus, where hub 212535's
  `wd.dilbert` was pinned at **0** frags with team `pys` on 50; he now
  reads **21** (the value his last in-match `svc_updatefrags` carried, and
  the value KTX's own demoinfo block states) and `pys` reads **71**. In
  FFA, where nobody has a team, an empty team read as "spectator" and
  deleted four of five players from `ffa_5[dm4]`; the roster is now
  complete. Meanwhile a connection the server *refused* — the match was
  locked, so it allocated a slot and emitted an `svc_updateuserinfo`
  without ever entering the game — used to inherit the scoreboard row of
  whoever had just left. Those rows and the phantom one-person teams they
  created are gone (`4on4_l_vs_la[e1m2]` is now the 4v4 it is: 8 players,
  2 teams; `dag_caps_e1m2` drops `jOn`).

- **Per-slot state no longer leaks across a slot handover.** Weapon
  possession and powerup *intervals* are driven by `STAT_ITEMS` bit flips,
  so a departing player's open intervals stayed open *on the slot* and the
  next occupant inherited a full stale inventory from the instant their
  userinfo landed — 3520 ms of RL/SNG/SSG "possession" spanning the whole
  stint of a refused connection on `4on4_l_vs_la[e1m2]`. Intervals now
  close at the handover and the held state is cleared, which also means a
  departing player's own intervals end when they left rather than at match
  end (`shiva`'s RL run is 17.8 s shorter, and correct). The *change*
  streams — health, armor, armor type, loc and the four ammo counts — had
  the mirror-image defect one level down: they dedup against the previous
  value, which belongs to the previous occupant, so an arriving player
  whose first sample equalled the departing player's last got no sample at
  all and their stream fragment opened empty. The handover now cuts the
  dedup floor per column, `loc` included — it is the one column built in
  finalize rather than the event pass, so its cut is replayed from the
  recorded handover timestamps instead of measured as a column length. No
  demo in the local corpus hits the collision on any column, so no served
  value moves; it is a latent fix with unit tests.

  One consequence of the same split *does* move a served value, in
  `locGraph`. Ending an occupancy breaks the position track, so a player's
  exit from the slot he left is no longer joined to his re-entry on the slot
  he came back on. `4on4_jah_ahoy_170526_defer_reconnect` loses the
  `Quad.low → Pent.MH` **teleport** edge (`rusti`, `jah`, total 1) that the
  join invented — rusti did not teleport, he reconnected.

Supporting changes:

- `UserInfoEvent.Vacated` (Layer 1) flags the empty-userinfo broadcast the
  server sends when it drops a client — the wire's own end-of-occupancy
  marker. `mvd-reader/MVD_FORMAT.md` gains a "Departure" section covering
  it, the `timed out` path, refused connections, the `-999` spectator
  sentinel some pre-KTX mods publish, and the serverinfo `score` key as an
  independent oracle.
- `UserInfoEvent.Partial` (Layer 1) flags an event synthesised from
  `svc_setinfo`. Such an event carries the parser's *cached* player
  snapshot for every field the server did not just set, userid included —
  and mvdsv emits `svc_setinfo <slot> "*auth" ""` from `SV_Logout` both
  when a client is dropped and during the **next** client's connect
  handshake (`sv_login.c:588`, `:644-646`). Occupancy tracking now ignores
  partial events as boundaries, so a drop stays a drop.
- **`PlayerDepartureEvent` / `PlayerRejoinEvent` (Layer 1).** The
  `left the game with N frags` / `rejoins the game with N frags` /
  `reenters the game without stats` family is one wire grammar that two
  analysers were scanning with two different parsers and two different
  guards. It is decoded once in the parser now, with the wire's awkward
  properties handled in one place: old servers fragment a broadcast across
  several `svc_print` messages and split it at arbitrary points, including
  inside the number (`4on4_l_vs_la[e1m2]` emits a team score as
  `"Team [.la.] = "`, `""`, `"2"`, `"2"`, `"7"`, `"\n"`), so a frag count
  is only trusted when `" frag"` follows the digits and `FragsKnown`
  reports the difference. `PRINT_CHAT` is excluded, which also closes a
  hole where a chat line could poison identity's reconnect set.
- A single `occupancyTracker` (`mvd-analytics/analyzer/occupancy.go`) now
  owns the "where does one occupancy end" rule for the identity, timeline
  and match analysers, so the three cannot drift apart. A drop ends an
  occupancy unconditionally: there is no "the same userid came back so the
  drop did not count" rule, because nothing on the wire re-broadcasts a
  dropped client's userinfo and `SV_GenerateUserID` recycles ids anyway
  (`sv_main.c:538-556` checks uniqueness only against clients that are not
  `cs_free`, so a freed slot's id can be reissued; the pool is 1..99 on
  modern mvdsv but four-digit on the 2002-era demos, so the only portable
  claim is "non-zero"). Replaying the pre-fix code over the 54 local demos
  counts **18 erased departures on 10 demos**: `4on4_oeks_vs_tsq[dm2]` 1,
  hub 212545 1, 216268 1, 216835 2, 218909 3, 218932 1, 218936 3, 220508 3,
  220517 2 and 220520 1.
- **Two occupancies that were live at the same instant are two people.**
  Without demoinfo, `*auth` or a KTX reconnect print the identity key
  degrades to the normalized netname, which strips case and punctuation, so
  a demo with two players called `Player` and `player!` produced one
  scoreboard row and lost a team. The merge is now refused on overlap —
  but only between occupancies that each carry a userid of their own, since
  KTX also publishes a departed player's *ghost* on a spare client slot
  (userid 0, netname prefixed with the `\203` glyph that normalises to `#`)
  carrying a copy of his frags.
- **The departure broadcast is bounded.** It names only a netname, so for an
  occupancy the server dropped it is accepted only in the same frame as the
  drop, and never after the match has ended (KTX guards its own print on
  `match_in_progress == 2`; the pre-KTX mods do not). It is also refused
  when it announces the `-999` spectator sort marker, the same value
  `svc_updatefrags` is already screened for: the broadcast renders the edict
  count verbatim, and this recovery adopts it as the occupancy's final score
  without further test. No local demo announces one — `dag_caps_e1m2` has
  the sentinel five times but never on a departure line.
- **A reconnecting player's timeline score no longer freezes.** The
  frag-reset rebase was armed only on the takeover shape, but the common
  reconnect is vacate-then-connect, which the tracker reports as a close
  with no open followed by an open with no close. The restore then read as a
  large delta, the ±5 corruption guard rejected it, and — because that guard
  deliberately does not advance the cursor — every later real +1 was
  rejected too. Every mid-match connect arms the rebase, including one onto
  a slot the recording has never seen occupied: a reconnect takes the first
  `cs_free` slot (`CountPlayersSpecsVips`, `sv_main.c:1137-1145`) rather
  than the one it just vacated, so on a demo that started mid-game the
  receiving slot may carry no occupancy history at all. The arm is spent on
  the very next `svc_updatefrags` for that slot and rebases only a value the
  ±5 guard would have rejected anyway — it has to be that narrow, because
  `SV_FullClientUpdate` writes the frag update *before* the userinfo
  (`sv_main.c:481-513`, copied into the demo as one `dem_all` block by
  `sv_send.c:1060-1064`), so a new client's own 0 arrives one event ahead of
  the arm and the update after it is the player's first kill. No served
  value moves on the 54 local demos: of the 26 arms consumed there, 24 carry
  a delta of 0 and the two on 216835 are the +16 restore.
- **A roster broadcast can state a negative frag count.** `"<name> left the
  game with -3 frags"` is what a player who suicided below zero is
  announced with — both the departure and the rejoin lines print the edict
  value verbatim — and the parser's digit scan rejected the sign, so
  `FragsKnown` came back false and the consumer silently fell back to its
  own reconstruction. No local demo carries one.
- New `mvd-analytics/corpus/` invariant harness walks
  `demo-test-data/mvd/special-cases/` when present (no-op when absent, like
  `mvd-analytics/diagnostic/`) and asserts team totals against the
  serverinfo `score` key and the KTX demoinfo scoreboard, that the roster
  and the player streams name the same people (both directions), and that
  item intervals only exist for a player the wire actually saw play. It
  fails on all three defects above when run against the previous code.
  Run it with `-count=1` — `go test` caches the skip it takes when the
  demo directory is absent and never invalidates it when the demos
  appear; `make test` passes the flag.

## 2026-07-24 (cleanup-dedup) — reject explicit `windowMs=0` (no schema bump)

HTTP-boundary validation only; no schema change, goldens unchanged.

- **`/buckets` and `/region-control` now reject an explicit
  `windowMs=0`** with `400 invalid_param`
  (`windowMs must be >= 1; omit it for the default 50`), instead of
  silently coercing it to the default 50 — the same reject-loudly
  posture the games-search `limit=0` rejection already took. An
  **omitted** `windowMs` still defaults to 50, and the view-level
  `<=0 → 50` coercion is unchanged (it remains the programmatic-caller
  default for the WASM / `qw-analyze` in-process paths); this is only
  the HTTP surface. Approved as a breaking change to the (rare)
  `windowMs=0` case.

## 2026-07-23 (tweak-mcp) — audit fixes, schema v59

Closes out an external MCP-consumer audit (12 findings). Most changes
are view-layer only; the region-control stats change moves served
values, so `CurrentSchemaVersion` bumps to **v59** and the golden
corpus was regenerated.

- **Region-control stats are now an exact time-weighted integral.**
  `stats` (the match-aggregate percentages + `byPlayer` tallies) is
  computed by walking the union of every player's native Position
  sample times and their RL/LG armed-interval boundaries, classifying
  each constant-state interval once and accumulating its **real
  duration** — no grid at all. This replaces the interim fixed native
  50 ms stats grid: because that grid still quantized presence to 50 ms
  quanta, its percentages were an approximation. Two consequences: the
  state percentages shift slightly (de-quantized), and
  `RegionStats.byPlayer.armed`/`unarmed` change **units** from
  50 ms-bucket counts to integer **milliseconds** of presence (Go field
  type unchanged, `int`; value ~50× larger). `bucketStates` still honours
  the caller's `windowMs` and is unchanged — `windowMs` now affects only
  that display grid. As a result the `windowMs=50` output is **no longer
  byte-identical** to prior versions (schema v59; goldens regenerated).
- **`weapons=` filters validate against closed vocabularies.**
  `/frags`, `/damage`, `/backpacks`, `/weapon-pickups` now reject an
  unknown weapon token with `400 invalid_param` naming the valid set —
  the same treatment `/events` `types` already got — instead of
  silently matching nothing. Each vocabulary is pinned to its producing
  analyzer code (`view.fragWeaponVocab` et al.); RESULT_SCHEMA's frag
  weapon vocabulary was corrected against the code (`axe` not `ax`,
  plus the previously undocumented `hook`/`rail`/`stomp`/`unknown`/
  `suicide` causes).
- **Filtered `/damage` no longer ships the full-match scoreboard.**
  The KTX end-of-match cross-check has no per-event provenance, so it
  cannot be recomputed against a `weapons` or a *restrictive* time
  filter; it is now omitted under those filters (players-only filtering
  still narrows it as before) instead of riding whole-match totals along
  a small filtered payload.
- **MCP tool descriptions caught up with the surface.** The `getEvents`
  `types` description now lists the full 12-type default set (it had
  never learned `demomark`/`airgib`/`pause` — the runtime error was the
  only place the list existed); weapons params list their full
  vocabularies; the `getRegionControl` description documents the
  `bucketStates` alphabet and the counts-vs-percentages split in
  `stats`; the `teamkill` frag token and `searchGames` `limit=0`
  semantics are documented; the stale "v57" pin is gone from the
  pure-ms wording.
- The `artifact_unknown` 404 now points MCP callers at `listArtifacts`
  (the old hint named only `GET /v1/artifacts`, unreachable over MCP).
- **`/events` damage rows surface the stored bounded value.** The
  opt-in `damage` events mirror the per-hit log: `detail.damage` stays
  the unbounded wire value, and the new `detail.bounded` passes the
  stored KTX-scoreboard reconstruction through (present only when it
  differs; absent on `skipped:*` demos) — so an events reader can
  cross-check `/damage`'s bounded-family figures without a second
  fetch. KTX's own demoinfo dmg is bounded semantics
  (`ktx/src/combat.c`: armor absorbed in full + health damage capped at
  the victim's remaining health), which is why bounded is the family
  the scoreboard-minded reader wants.
- **`searchGames`/`/v1/games/search` reject an explicit `limit=0`.** An
  explicit `limit=0` in the query string is distinguishable from an
  absent limit, so it now 400s `invalid_param` ("omit it for the default
  20") rather than being silently treated as the default — the v57
  reject-loudly posture. An omitted limit still defaults to 20; negative
  and `>100` still 400. The MCP `searchGames` `limit` field became a
  `*int` so an explicit `0` forwards to the REST boundary instead of
  being dropped as the Go zero value.
- **`/overview` `map` is the canonical shortname; new `mapTitle`.**
  `map` now carries the map shortname from `EffectiveMap` (demoinfo →
  serverinfo fallback — the same value `searchGames` rows and
  `/metadata` serverinfo carry, so a consumer can join on it), where it
  previously echoed the BSP's pretty title (`Claustrophobopolis` on
  dm2). The pretty title moves to an additive `mapTitle`, omitted when
  identical to `map`. View-layer only — stored `Result.Match.Map` is
  unchanged.
- **`/region-control` gains a `regions` param.** `full` (REST default;
  backward-compatible — the region polygon `points` included), `summary`
  (points stripped; name/locs/centroids kept), `none` (regions list
  omitted). Trims the ~6 KB polygon payload for stats-only consumers;
  `bucketStates`/`stats` are unaffected. The MCP `getRegionControl`
  defaults to `summary` (same divergent default as `getItems`; a
  defaulted response carries a `hint`) — pass `regions:'full'` for the
  points. The stored Result's regions are never mutated.

A second code-review pass tightened the above: region-control `stats`
are now computed independently of the display grid, so a coarse
`windowMs` whose sub-window rounds to zero buckets — and the
empty-roster case that used to yield `empty:100` rows — still return
stats; an explicit whole-match `/damage` `to=` window is treated as
unfiltered, so it keeps the scoreboard and the KTX-exact bounded
summary instead of taking the recompute path; and the `mapTitle`
elision is case-insensitive (`aerowalk` vs `Aerowalk` no longer emits a
spurious title).

Deferred from the audit (tracked, not shipped here): a warming/retry
response for cold-start analysis timeouts, hub-side `limit=0`
semantics beyond the boundary rejection above, recovering the real
weapon behind `teamkill` frag rows, and a `mapTitle` on `searchGames`
rows (overview now has it).

## 2026-07-23 (add-airgib-pause-events) — view-layer change, no schema bump

Put **airgibs and pauses on the default event stream**. Both signals
already existed in the Result — airgibs in `timelineAnalysis.airgibs`
(plus the REST-only `/v1/demos/{id}/airgibs` endpoint) and pauses in
`streams.global.pauses` — but neither was reachable from the MCP
surface (only the curated tools proxy REST endpoints, and `getEvents`
didn't emit them). This wires both into `/v1/demos/{id}/events`, which
`getEvents` proxies, closing the MCP-discoverability gap.

- **`airgib` event** — a direct enemy rocket hit on an airborne victim
  (the Key Moments highlight). `player` is the attacker; `detail`
  carries `victim`, `height`, `damage`, and the optional `attackerTeam`
  / `victimTeam` / `heightAboveAttacker` / `loc` / `lethal`.
- **`pause` event** — a game-clock freeze segment. It has **no**
  `player` (so a `players=` filter excludes it) and carries
  `detail.durationMs`, the real wall-clock ms the pause consumed.
- Both join the `/events` **default** type set, so a caller that omits
  `types` begins seeing the new rows.
- **New drift test** (`mvd-api/mcp_reachability_test.go`): every
  demo-scoped REST GET view endpoint must be reachable from the MCP
  surface — via a curated tool, an event type, or a servable artifact —
  so a future endpoint can't ship MCP-invisible the way `/airgibs` did.
- View-layer only: no stored struct or schema version changed. Clients
  on `/v1` that ignore unknown enum values are unaffected.

## 2026-07-23 (demo-marker) — schema v58, additive

Surface **demo markers** — the bookmarks players insert during a game
with KTX's `/demomark` command — through the whole pipeline.

- **Layer 1.** New `DemoMarkEvent` emitted from the `//demomark`
  stufftext alongside the generic `StuffTextEvent`. Attribution comes
  only from the demo block target (the marking player's slot), matching
  a token boundary so `//demomarkX` is not a marker; the optional
  argument tail (HoonyMode `//demomark 0 round-07`) is captured as a
  label. Un-gated — markers inserted out of match are surfaced too.
- **Layer 2.** New `timelineAnalysis.demoMarkers[]` (`[]DemoMarkerEvent`):
  match-relative `time` (negative for a warmup mark), the marking
  player's resolved `playerName`/`playerSlot`/`playerUserID`/`team`
  (empty with `playerSlot: -1` when the block was not slot-addressed),
  a `spectator` flag (KTX accepts `/demomark` from spectators too),
  and the optional `label`. Team labels flow through the born-correct
  duel rewrite like the sibling event lists.
- **API.** New `demomark` event type on `/v1/demos/{id}/events`, added to
  the **default** type set — a caller that omits `types` begins seeing
  the new rows. `info.version`/`schemaVersion` → 58.
- Additive: no existing field or behaviour changed. Clients on `/v1`
  that ignore unknown fields and enum values are unaffected. See
  [RESULT_SCHEMA.md](mvd-analytics/RESULT_SCHEMA.md) v58 and the new
  API-versioning note in [mvd-api/API.md §2.7](mvd-api/API.md).

## 2026-07-22 (clean-out-seconds-from-pipeline) — internal refactor, no schema change

Integer-milliseconds end to end through mvd-reader → mvd-analytics.
**No schema bump, no API change, and the result JSON is byte-identical**
— the full golden corpus (4on4 / 2on2 / 1on1 + reconnect) passes
unchanged without `-update-golden`, which is the acceptance proof that
the change is lossless.

- **The events contract carries only `TimeMs int32`.** Phase 1 deleted
  the float-seconds `Time` field from every event struct; this change
  removes the last float-seconds *time* from the analyzers. Every event
  now also exposes `EventTimeMs() int32` on the `Event` interface
  (twin of the presentation-only `EventTime() float64`).
- **Analyzers consume integer ms natively.** Deleted the `analyzer.msTime`
  float→ms shim and all ~64 `events.Sec(e.TimeMs)` call sites; the
  death/spawn ledger, pause samples, `MatchTimingDetector` start/end,
  item respawn timers (`kindRespawnMs`), weapon-pickup windows, and the
  item-attribution correlation windows are all int32 ms. The match-end
  fallback is now computed once via `MatchTimingDetector.EffectiveEndMs`
  instead of duplicated float logic in the clock and the timeline.
- `events.Sec(ms)` survives as the sanctioned presentation-edge helper
  (currently uncalled — qw-analyze derives via `EventTime()`, and the
  parser diagnostic inlines the conversion to avoid an import cycle).
  The one wire-native float-seconds time, `ServerData.ServerTime`
  (id1 `svc_serverdata`), is unchanged.
- Two behavior notes, no Result impact: the `qw-analyze -format events`
  raw dump now serializes `TimeMs` where it used to show a float `Time`
  field (the structs changed; the dump mirrors them), and exact-boundary
  window comparisons (e.g. a hint exactly 250 ms before a state flip)
  are now deterministic where the old float math was rounding-dependent
  at the boundary — no corpus demo hits such a boundary, hence the
  byte-identical goldens.

## 2026-07-22 (api-cleanup-2) — schema v57 (in progress, unreleased)

The `api-cleanup-2` release addresses the v56 API-review findings in
phases; the schema bump 56→57 and the pure-ms time model land in a later
phase. Each phase appends its own subsection below.

### Phase 1: request validation (`unknown_param` + enum values)

No response-shape change — this phase only tightens which requests are
*rejected*.

- **Unknown query-parameter names now 400 with a new `unknown_param`
  code.** Previously an unrecognised query key (a typo, a stale param) was
  silently ignored, so a mistaken filter returned an unfiltered body. Every
  endpoint now rejects any query key it does not consume, naming the
  offending key and the endpoint's accepted keys in the message. Enforced
  by consumed-key tracking in the shared `qp` reader (each accessor records
  the keys it reads), so the accepted set can never drift from what the
  handler actually uses. Two-direction spec↔handler test sweep pins it.
- **The global `label` traffic-source tag is accepted on every endpoint**
  (it is read by request logging, not any handler), so tagging traffic with
  `?label=…` never 400s.
- **Legacy / accepted-and-ignored params stay accepted.** The retired
  `nails` opt-in on `/shots` and the deprecated `weapon` alias of `weapons`
  are whitelisted (documented `deprecated` in the spec) rather than rejected.
- **Unknown enum *values* now 400 `invalid_param` instead of matching
  nothing.** `/events?types=` is validated against the known event-type
  vocabulary (`view.KnownEventTypes`, drift-pinned to the spec enum) and
  `/chat?types=` against `{chat, teamsay}`. `weapons=` CSV values, `/items`
  tokens, and map-entity `types`/`kinds` stay open (data-derived
  vocabularies) — noted as out of scope.
- Check order per handler is `invalid_param` → `unknown_param` →
  `missing_param` / availability `422`; for `/los` the unknown-param check
  runs before the heavy raycast compute, and for `/region-control` /
  `/state-at` before the availability / missing-time checks.

### Phase 2: search fixes (dates, `server_hostname`, case docs)

Touches `GET /v1/games/search` (and its MCP `searchGames` proxy) only; no
schema bump.

- **Malformed search `from`/`to` now 400 `invalid_param` instead of 502.**
  The `from`/`to` calendar-date bounds are validated at the API boundary as
  strict `YYYY-MM-DD` (exactly 10 characters and a real date); a bad value
  fails fast with a client-side `400 invalid_param` rather than reaching the
  hub and surfacing as an opaque `502 hub_upstream`.
- **Search rows now include `server_hostname`** — the QuakeWorld server a
  game was played on (closes review finding 3). It is a snake_case hub
  passthrough (consistent with the `demo_sha256` island) and appears in both
  compact and `roster=true` rows. Added to the hubfetch select list and
  mirrored into the web app's direct-Supabase `SEARCH_SELECT`.
- **Doc fix: the search `players` filter is case-INsensitive** (finding 2).
  It is a PostgREST full-text search, which lowercases both query and
  indexed names (`to_tsvector`) — so `bps` and `BPS` match the same games.
  The spec/API.md/MCP text previously claimed it was case-sensitive; the
  claim is corrected and now contrasts it with the per-demo endpoints'
  exact, case-sensitive `players` filter. The search `from`/`to` (calendar
  dates) are likewise distinguished from the per-demo endpoints' `from`/`to`
  (match-relative times).

### Phase 3: `/los` no-BSP → 422 `los_unavailable`

Touches `GET /v1/demos/{id}/los` and `GET /v1/demos/{id}/artifacts/los`; no
schema bump. Closes review finding 6 and the v55 open cache-invalidation item.

- **The poisoned-cache failure class is eliminated at the root.** Previously
  `/los` on a map with no provisioned visibility BSP latched
  `Streams.LOSComputed` *before* the BSP check and returned `200` with empty
  intervals — an outcome that was then persisted to the tier-3 artifact cache
  and never retried, so provisioning the BSP afterwards did nothing until the
  cache was manually cleared. `analyzer.ComputeLOS` now returns a new
  `analyzer.ErrNoBSP` for the three no-usable-BSP causes (the demo carries no
  map name, no BSP is provisioned for the map, or a provisioned BSP fails to
  parse) and does **not** latch, persist, or cache anything. The latch is set
  only on a genuine compute or a legitimately empty `<2`-player demo (which
  stays cacheable). Retry is cheap — no empty gob is ever written.
- **`/los` now returns `422 los_unavailable`** for those cases, matching its
  `*_unavailable` siblings (`aim_unavailable`, `locgraph_unavailable`, …)
  instead of the misleading `200`-empty. Both the curated `/los` endpoint and
  the generic `/artifacts/los` route map `ErrNoBSP` to the same `422`.
- **Ops note: BSP provisioning heals on process restart.** `mapbsp` memoises
  its "not found" result per process, so once a BSP is shipped, restart the
  API (or wait for the memo to be dropped) and the next `/los` request computes
  and caches normally — no cache purge needed, because the no-BSP outcome was
  never cached in the first place. The corrupt-BSP branch keeps its own
  per-process negative memo so repeated requests don't re-parse the bad file.

### Phase 4: pure-ms time model + key renames (schema 56→57)

The breaking sweep. **Every time value in the API is now int32
milliseconds** — request params and response fields, REST and MCP alike.
This is the client-migration section: read the two tripwires first.

**⚠️ Tripwire 1 — the key moved, so the break is loud, not silent.**
The per-item time key follows a **dense/sparse rule**: event-scaled
sparse surfaces use the descriptive **`time`**, sample-rate-scaled dense
arrays use the terse **`t`** — both int32 ms. This is deliberately the
v55 spelling, which splits clients into two clean groups:

- **v55 `time`(ms) per-item keys and units are unchanged.** `/frags`,
  `/damage`, `/shots`, `/chat`, `/backpacks`, `/weapon-pickups` already
  carried their timestamp under `time` in int32 ms in v55; that key and
  unit are untouched in v57. **But four response *envelopes* moved**
  (v56, not v57): `/chat`, `/backpacks`, `/weapon-pickups`, and
  `/airgibs` changed from a bare top-level array to a `{timeUnit, <list>}`
  envelope, keyed `messages` / `backpacks` / `pickups` / `airgibs`
  respectively. That is a **loud break** — a v55 client iterating the
  response body directly now iterates an object and fails; reach through
  the named list key instead. The per-item keys and units inside are
  unchanged. `/frags`, `/damage`, `/shots` keep **truly-additive**
  envelopes (the `timeUnit` sibling was flattened onto the existing
  object), so old readers of those three are fine.
- **v55 `t`(seconds) readers on `/events`, `/buckets?layout=row`,
  `/state-at`, and `/items?summary` break loudly — by design.** Those
  surfaces carried `t` in float **seconds** in v55. In v57 the value is
  `time` in int32 **ms** — the `t` key is *gone*. A stale client reading
  `/events[].t` now gets `undefined` and fails fast instead of silently
  ingesting a 1000×-off number; re-read the field as `time` (already in
  ms). ⚠️ **A dual-key fallback does NOT protect you.** A v55-portable
  reader doing `ev.get("time", ev.get("t"))` (or `ev.t ?? ev.time`) is not
  caught by the loud break — the fallback now resolves to `time` and
  yields **ms where the code expected seconds**, a silent 1000× error.
  Drop the fallback and read `time` as ms.

The **same-key unit flips** left (value float seconds → int32 ms, key
kept — audit any code that read those as seconds):

- `/events` detail maps: `endTime` (powerup + streak details) and
  `duration` (streak detail). There is no `startTime` key.
- `/loc-graph` node time weights (`total`/`byPlayer`/`byTeam` plus the
  `armed`/`unarmed`/`quad`/`pent` breakdowns) — see [Post-review
  fixes](#post-review-fixes) below.

**⚠️ Tripwire 2 — `from`/`to`/`time` query params are now integer ms.**
On every demo endpoint these were float **seconds**; they are now
**integer milliseconds**. Old float-seconds values are rejected loudly:
`from=10.5` (or any non-integer) 400s `invalid_param` with an `(integer
milliseconds)` hint, rather than silently misfiltering, and a negative
value now 400s too (`must be >= 0`). Multiply your old seconds values by
1000. (Search `from`/`to` are unchanged — calendar dates `YYYY-MM-DD`.)
**But the tripwire only catches NON-INTEGER forms.** A whole-number value
that *was* meant as seconds — e.g. `from=60` (intending 60 s) — is a
perfectly valid integer ms and **cannot be detected**: it migrates
**silently** to a window 1000× too small (`from=60` now means 60 ms and
returns almost nothing instead of erroring). Audit every caller that
passed integer seconds and multiply by 1000.

**Unit flip — the seconds surfaces, now int32 ms (keys per the
dense/sparse rule):**

| Surface | v55 | v57 |
|---|---|---|
| `/events` rows | `t` float s | `time` int32 ms (`endTime`/`duration` in detail also ms) |
| `/buckets?layout=row` | per-bucket `t` float s | `time` int32 ms |
| `/state-at` | `t` float s (envelope) | `time` int32 ms |
| `/stream-slice` envelope | `startTime`/`endTime` float s | `start`/`end` int32 ms |
| `/loc-trails` | `s`/`e` float s | `start`/`end` int32 ms |
| `/items?summary=true` | `firstTake.t` float s | `firstTake.time` int32 ms |
| `/loc-graph` node weights | `total`/`byPlayer`/`byTeam` (+`armed`/`unarmed`/`quad`/`pent`) float s | same keys, int32 ms (see [Post-review fixes](#post-review-fixes)) |
| query params `from`/`to`/`time` | float s | integer ms |

**Key renames (JSON):**

| Old | New | Where |
|---|---|---|
| `o` / `iv` | `other` / `intervals` | `LosTrack` (`/los`, `/artifacts/los`, `pvs`) |
| `events` | `messages` | `MessagesResult` array — `/artifacts/messages` is now `{messages:{messages:[…]}}` |
| `startMs` | `start` | columnar `/buckets` axis (implicit `time(i)=start+i*windowMs`) |
| `t` | `time` | per-item time key on the flipped view surfaces: `/events`, `/buckets?layout=row`, `/state-at`, `/items` `firstTake` (the v55 `t`(seconds) → v57 `time`(ms); the stored sparse lists already spelled it `time`) |
| `startTime` / `endTime` | `start` / `end` | `/stream-slice` envelope |

**null → `[]`.** Governed top-level arrays that could serialize as `null`
now always serialize as `[]` when empty: `/events`, `/stream-slice`
`players`, `/loc-trails` `players`. Nested arrays deeper in a body may
still be `null`.

**`UnitSec` deletion + `timeUnit` constant.** `view.UnitSec` is deleted;
the view layer does no float time math. `timeUnit` is kept as a constant
`"ms"` self-description echo — now truthful on every governed response.

**Deliberate non-renames** (documented exceptions, unchanged in v57):
the dense/sparse time-key rule keeps the terse **`t`** on every
sample-rate-scaled array (stream tracks, aim samples, the columnar
`/buckets` grid, projectile/beam columns) and the descriptive **`time`**
on the sparse stored lists that already used it in v55 (frags, damage,
shots, chat, backpacks, weapon-pickups, opening takes, timeline events);
`result.Interval` keeps terse `s`/`e` (per-row keys in dense arrays stay
terse — the payload discipline is deliberate); projectile / beam `s*` /
`e*` column-family prefixes (`s`,`sx`… / `e`,`ez` — these *are* flight-time
bounds); `windowMs` / `partialLastMs` durations (already ms, names stay);
`/demoinfo` is the sole KTX-native seconds island.

`CurrentSchemaVersion` bumps 56→57; version-keyed ETags/cache paths
self-invalidate. MCP tool input schemas and descriptions move with REST.

### Phase 5: artifact timeUnit echo + spec completeness

Spec/doc pass on top of the v57 shapes; no further schema bump.

- **`/artifacts/{name}` now echoes `timeUnit:"ms"`.** The generic artifact
  envelope gains a top-level `"timeUnit":"ms"` **sibling** of the resultKey
  for every artifact whose stored section carries time —
  `frag`, `damage`, `shots`, `aim`, `opening`, `match`, `messages`,
  `timeline`, `items`, `backpacks`, `weapon-pickups`, `loc-graph` (audited
  per section against the backing `result.*` struct; `loc-graph` echoes
  because its node weights are int32-ms durations — see the post-review
  fix below). The no-time-field artifacts (`metadata`, `map-entities`) and
  the `/demoinfo` KTX-native
  island carry no echo; `/artifacts/los` keeps echoing via its `/los` body.
  The echo is now **required** in the spec on those artifact-envelope
  branches, so the self-description is guaranteed, not optional.
- **Per-track stream-slice column schemas.** The `/stream-slice` player
  tracks (`pos`/`view`/`hgt`/`lq`/`vel`) were one loose universal schema
  listing every column; they are now split into per-track schemas mirroring
  the slice projections (`pos` carries `li`, `view` carries `vp`/`vya`,
  `hgt` carries `h`, `lq` carries `lq`, `vel` carries `vx`/`vy`/`vz`; all
  share the `t`/`x`/`y`/`z` spine), so the spec can express which columns
  each track actually has.
- **Angle16 documented.** The `vp`/`vya` view angles (stream-slice `view`
  track and `/state-at`'s `view`) are raw angle16 wire shorts; their spec
  descriptions and API.md now carry the decode formula
  `deg = ((v mod 65536)+65536) mod 65536 × 360 / 65536` and contrast
  `/aim`'s `dyaw`/`dpitch`, which are float degrees.
- **Prose fixes.** ProjectileStreams/BeamStreams document the `s*`/`e*`
  spawn/end column-family prefix scheme (distinct from `result.Interval`'s
  terse per-row `s`/`e`); the messages schema notes the array includes
  `type:"frag"` lines, not just chat; `matchtag`'s lowercase-single-word
  casing is a documented exception.
- **100% OpenAPI description coverage.** Every property in
  `components.schemas` (recursively) and every `components.parameters` entry
  now carries a non-empty `description`, enforced by a new
  `TestOpenAPIDescriptionCoverage` walk (no allowlist; failures anchor the
  offending schema-path).

### Post-review fixes

Three follow-up fixes on top of the v57 shapes; no further schema bump.

- **`windowMs` overflow guard (400 instead of panic/garbage).** `windowMs`
  is cast to `int32` in the bucket-grid arithmetic; an unbounded value
  above `math.MaxInt32` (e.g. `?windowMs=4294967295`) wrapped negative,
  panicking the row layout (500) and serving a bogus negative `count` on
  the columnar layout (200). `view.resolveWindow` now rejects
  `windowMs > math.MaxInt32` (keeping the existing `< 0` reject) → **400
  `invalid_param`** on both `/buckets` layouts. The `bucketCount == 0` /
  `count == 0` grid guards are also hardened to `<= 0` defensively.
- **loc-graph node weights are now int32 ms (BREAKING for early-v57
  consumers).** `LocGraphResult` node time weights
  (`LocNode`/`LocWeights` `total`/`byPlayer`/`byTeam` and the
  `armed`/`unarmed`/`quad`/`pent` breakdowns) were still float64 **seconds**
  — the one surface the pure-ms flip missed. They are now **int32 ms**, so
  values are ×1000 and integer. Edge weights stay transition counts.
  `/loc-graph` and `/artifacts/loc-graph` now carry `timeUnit:"ms"`. Any
  consumer that read the early-v57 seconds values must divide by 1000 (or
  read them as ms).
- **Enum values are case-insensitive.** `/events` `types` and `/chat`
  `types` validated case-**sensitively** while every other token filter
  lowercases; `types=Frag` or `types=TEAMSAY` now validate and match
  (lowercased before validation and before use), matching the rest of the
  API.

### Post-release polish

Two doc/behaviour touch-ups on top of the v57 shapes; no further schema bump.

- **`label` is now documented per-endpoint in the OpenAPI spec (doc-only).**
  The non-secret `?label=` traffic-source tag (read by request logging,
  globally accepted on every request) was only described in the spec intro
  prose. It is now a reusable `Label` component parameter referenced by every
  operation's `parameters` list, so `/openapi.yaml` and `/docs` show it on
  each endpoint. No behaviour change — it was always accepted.
- **Search `limit > 100` / negatives now 400 instead of a silent clamp
  (behaviour tightening).** `GET /v1/games/search` previously clamped `limit`
  to the hub's 100-row page cap. In line with v57's reject-loudly posture it
  now returns **400 `invalid_param`** for a `limit` above 100 and for a
  negative `limit`/`offset`; `limit=0`/omitted still means the default 20.
  The MCP `searchGames` tool proxies this endpoint and inherits the 400.
  hubfetch keeps its clamp as a server-side belt for direct library callers.

## 2026-07-21 (tweak-api)

- **Review fixes — echo-rule completeness + hub read hardening (still
  schema v56, unreleased).**
  - `/los`, `/streams/projectiles`, `/streams/beams`, `/streams/nails` now
    carry the `"timeUnit": "ms"` echo (their columns are all int32 ms), so
    the documented "every time-carrying `/v1/demos/{id}/*` response echoes
    `timeUnit`" rule is true end to end. The rule's wording is corrected
    everywhere to its honest form: time-carrying responses echo, with
    `/demoinfo` and `/artifacts/{name}` exempt, and the three timeless
    responses (`/loc-table`, `/loc-graph`, `/metadata`) carry no echo.
    [v57 correction: `/loc-graph` node weights ARE time-valued — see v57
    Post-review fixes; they are int32 ms as of v57, so `/loc-graph` echoes
    `timeUnit:"ms"` and is no longer timeless.]
  - **Security.** `hubfetch` Search/Resolve now cap the upstream catalog
    read at 16 MiB (`maxCatalogBytes`) — previously an unbounded
    `io.ReadAll` let a compromised/buggy hub or a MITM of
    `HUB_SUPABASE_URL` OOM the API host. And `GET /v1/games/search` no
    longer echoes the raw upstream error (which can embed the hub URL +
    query) into its 502 body — it returns a generic message and logs the
    detail server-side.

- **Time-field polarity flip — `t` is ms, `time` is seconds (still schema
  v56, unreleased).** The time-field naming convention flips so it becomes
  exception-free:
  - **`t` = int32 milliseconds, ALWAYS** — sparse event lists and dense
    per-sample arrays alike. The dense stored arrays already used `t`-in-ms,
    so they conform without change — that is the point of this polarity, and
    the big sparse event lists (frags/damage/shots/chat/…) get the compact
    key for the compact type (a payload win).
  - **`time` = float64 seconds, ALWAYS.**
  - Descriptive names (`startTime`, `endTime`, `nextDeathTime`, `dropTime`,
    `duration`, `availableFrom`/`takenAt`/`respawnAt`, …) keep carrying the
    endpoint's native unit, declared by the `timeUnit` echo; `*Ms`-suffixed
    names (`startMs`, `windowMs`, `atMs`, `durationMs`) are unchanged;
    `/demoinfo` stays the KTX units island.
  - **JSON key renames.** Stored ms fields `json:"time"`→`json:"t"`
    (FragEntry, DamageEntry, PositionalKill, Shot, MatchEvent/chat,
    AirgibEvent, BackpackDrop, WeaponPickup, OpeningResult firstTakes,
    and the timeline frag/death/kill/powerup/streak events). Seconds view
    surfaces `json:"t"`→`json:"time"` (events `TaggedEvent`, buckets-row,
    state-at, items-summary `firstTake`). loc-trails residences rename
    `s`/`e`→`start`/`end`. Go field names are unchanged — only the JSON
    tags. The webapp reads, OpenAPI spec, MCP tool descriptions, and the
    committed golden corpus were updated in lock-step (golden diff audited
    as a pure key rename — no value changed). schemaVersion stays 56.

## 2026-07-15 (tweak-api)

- **`timeUnit` echo + codified time-unit naming convention (schema
  v56).** `timeUnit` is **the unit of every time value in a response**.
  **Every `/v1/demos/{id}/*` JSON response that carries match-position
  time values echoes a top-level `"timeUnit": "ms"|"s"`, except
  `/demoinfo` (mixed KTX-native units) and `/artifacts/{name}` (raw
  stored bytes). Responses with no match-position time — `/loc-table`,
  `/loc-graph`, `/metadata` — carry no echo.** There is **no unit
  selection** — an earlier `units=ms|s` conversion
  param was dropped as over-engineered (a parallel `any`-typed
  shadow-struct hierarchy with a field-drift hazard, for a divide-by-1000
  any client can do). The value is **fixed per endpoint**:
  - **Native ms**: `/frags`, `/damage`, `/shots`, `/chat`, `/airgibs`,
    `/backpacks`, `/weapon-pickups`, the `/items` full phase timeline,
    `/overview`, **`/aim`, `/buckets?layout=column`, `/region-control`**
    (new this pass — every value in each is ms, so the echo is a truthful
    `ms`; they used to carry none), and the dense columnar stream bodies
    **`/los`, `/streams/projectiles`, `/streams/beams`, `/streams/nails`**
    (int32-ms columns — added in the 2026-07-21 review pass so the echo
    rule is honest end to end). **Native seconds**:
    `/events`, `/state-at`, `/stream-slice` (envelope), `/loc-trails`,
    `/buckets?layout=row`, the `/items?summary=true` shape.
  - **Field-name conventions still hold.** The sparse match-position `t`
    (int32 ms) and `time` (float seconds) names are absolute on every
    endpoint (see the 2026-07-21 polarity-flip entry above), and dense
    per-sample arrays stay int32 ms under compact names — the
    `/stream-slice` embedded change/interval/position tracks (`t`/`s`/`e`
    are ms even though that envelope's `timeUnit` is `s`), the `/aim`
    crosshair `t` + `lgRamp` `since` samples, and the columnar `/buckets`
    axis `startMs` + `windowMs`. `/overview`'s `timing` block is a
    wall-clock island with explicit `*Ms` names.
  - **Shape change — bare arrays become objects.** To carry the
    `timeUnit` echo, the four endpoints that returned a top-level JSON
    array now return an envelope object: `/chat` →
    `{timeUnit, messages:[…]}`, `/airgibs` → `{timeUnit, airgibs:[…]}`,
    `/backpacks` → `{timeUnit, backpacks:[…]}`, `/weapon-pickups` →
    `{timeUnit, pickups:[…]}`. Consumers indexing the old top-level array
    must read the named field. This is the one non-additive change; every
    other endpoint just gains the additive `timeUnit` field.
  - **Escape hatch.** `GET /v1/demos/{id}/artifacts/{name}` serves the
    raw stored result sections in int32 ms as-is — no `timeUnit` — the
    way to always get the raw stored milliseconds.
  - Stored `result.*` structs stay int32 ms; the transport surface added
    only the `timeUnit` echo here (schemaVersion restamped to 56). The
    JSON *key* rename that finalized the polarity landed later in v56 —
    see the 2026-07-21 entry above. See
    [mvd-api/API.md §2.1](mvd-api/API.md) and
    [RESULT_SCHEMA.md](mvd-analytics/RESULT_SCHEMA.md) §"Time units".

## 2026-07-14 (tweak-api)

- **MCP `searchGames` now routes through `mvd-api` (operator-facing, no
  schema bump).** The shim's one remaining direct-to-hub tool now proxies to
  `GET /v1/games/search` like every other tool, so `mvd-mcp` has a single
  egress point (`mvd-api`), holds no hub configuration or secrets, and no
  longer imports `hubfetch` (or any `mvd-analytics` code at all). **Deploy
  change:** drop `HUB_SUPABASE_URL` / `HUB_SUPABASE_KEY` from `mvd-mcp`'s
  `mcp.env` — its only secret is now `MVD_API_KEY`. The tool's request shape
  and `{limit, offset, count, total?, games}` response are unchanged; a hub
  failure surfaces as the API's `502 hub_upstream` message.

- **Hub URL / key / CDN moved out of the source into the environment
  (operator-facing, no schema bump).** `hubfetch` no longer compiles in the
  Supabase URL, anon key, and CDN base; `NewClient()` reads
  `HUB_SUPABASE_URL`, `HUB_SUPABASE_KEY`, and `HUB_CDN_URL` instead, so the
  key can rotate without a rebuild and never sits in the tree or the deploy
  examples. When unset the client returns a clear "hub not configured"
  error on use: `mvd-api` still starts and serves its local cache, but a
  cache miss and `GET /v1/games/search` return `502 hub_upstream`, and the
  MCP `searchGames` tool — which proxies to that endpoint — surfaces the
  same 502. **Deploy change:** add the three vars to `mvd-api`'s
  `secrets.env` only; `mvd-mcp` needs no hub vars (it routes search through
  the API) — see `deploy/README.md`. Golden-corpus tests are unaffected
  offline (they read the committed cache); only a cold cache needs the vars.

- **Game discovery over REST: `GET /v1/games/search` (no schema bump).**
  The hub.quakeworld.nu catalog search that used to live only in the MCP
  `searchGames` tool is now a first-class REST endpoint, so a plain HTTP
  client can find a `gameId` without the MCP shim. Query by `players`,
  `teams`, `map`, `mode`, `matchtag`, `from`/`to` (ISO dates), with
  `limit`/`offset` pagination and `roster` for verbatim rows; the response
  is the same `{limit, offset, count, total?, games}` the MCP tool returns
  (compact `{name, team, frags}` rosters by default). The query itself
  moved into the shared `hubfetch` package, so the REST endpoint and the
  MCP tool answer discovery identically. It proxies a live upstream, so it
  is uncached and maps hub failures to `502 hub_upstream`. No schema bump —
  the search response is not part of the demo `Result` (same as the upload
  endpoint before it).
- **BSP-derived features now work on demos with no KTX demoinfo block (no
  schema bump).** LOS/PVS, loc resolution, floor height (`pos.h`), liquid
  state (`pos.lq`), and region control were all gated on the KTX demoinfo
  hidden block (`res.DemoInfo.Map`). Older recorders — e.g. MVDSV 1.00 with
  KTX 1.43/1.44 (2024-era) — never emit that block: MVDSV writes it only when
  KTX issues `cmd demoinfo` (`mvdsv/src/sv_demo_misc.c:851`,
  `ktx/src/commands.c:7740`). Those demos got `DemoInfo == nil` and silently
  produced none of the above, even with positions recorded and the BSP
  provisioned. The map is now resolved through a single `EffectiveMap`
  accessor (`result.Result.EffectiveMap` post-hoc, `CoreOutputs.EffectiveMap`
  at pipeline time) that prefers the demoinfo map and falls back to the
  serverinfo `map` key — which every demo carries. No result **shape**
  changes; the affected fields simply populate where they were previously
  absent, so no `CurrentSchemaVersion` bump (consistent with prior
  "existing field now populates more often" changes). Requires the map's BSP
  to be provisioned, same as before.


## 2026-07-14 (deploy-upload-config)

- **Deployment: BSPs for LOS, explicit limits, memory ceiling (no schema
  bump).** Operator-facing only — no API behaviour changes.
  - **`/los` and `/airgibs` need map BSPs, and the unit now points at
    them** (`Environment=MVDA_BSP_DIR=/opt/mvd/bsps`). There is no
    `-bsp-dir` flag: `mapbsp` reads `$MVDA_BSP_DIR`, then a `bsps/` dir
    relative to the working directory. Provision the sha-pinned set with
    `scripts/fetch-bsps.sh`. **Gotcha now documented:** an absent BSP does
    not error — `BuildLOS` yields an *empty* LOS and `EnsureLOS` caches it
    to tier 3 like a real answer, so demos whose `/los` was served before
    the BSPs existed keep returning empty. Provisioning afterwards does
    not fix them; the lazy artifacts must be dropped
    (`rm -rf <cache-dir>/artifacts`). `cache prune` will not do it — it
    only sweeps stale-*version* gobs, and an empty-but-current LOS looks
    valid to it.
  - The upload and parse limits (`-max-upload-bytes`, `-upload-daily-*`,
    `-parse-timeout`) are now set explicitly in the shipped unit at their
    defaults, so the config states them rather than hiding them in the
    binary — notably that **uploads are on by default** and
    `-max-upload-bytes 0` is the kill switch.
  - `MemoryHigh`/`MemoryMax` added to the unit. Uploads let a stranger
    trigger a parse, and without a cgroup ceiling an overrun lets the
    kernel OOM-killer pick a victim across the whole box. **Tune to your
    box** — the shipped values assume ≥4 GiB of RAM.
- **Two security margins closed.** `loc.NormalizeMapName`'s
  `filepath.Base` is documented as a security boundary, not a
  convenience: the map name comes from the demo's `svc_serverdata` and
  feeds a filesystem path in the mapents/mapbsp/locvis loaders, so
  stripping directory components is what stops an uploaded demo declaring
  its map as `../../../../etc/passwd`. And `buildAimHist` now escapes its
  title — not a live XSS (both callers pass literals), but it interpolates
  into `innerHTML` and player names are attacker-controlled via any
  uploaded demo.

## 2026-07-13 (log-identity-fields)

- **Access log: `discord` + `key` identity fields (no schema bump).** The
  request line's `label` is the key's *note* when one is set, and the
  Discord portal stamps `note="portal"` on **every** key it issues — so
  all portal users pooled under a single `label=portal`, and their Discord
  name never reached the log. Attribution was effectively impossible.
  The request line now also carries `discord` (the Discord display name)
  and `key` (the 8-char hash prefix, the same one `keys list` prints, so
  it joins the log to the key store). Neither can be masked by a note.
  `label` is unchanged, so existing queries keep working; both new fields
  are empty on auth-exempt paths and 401s, where no key is resolved. The
  Bearer value and the full hash are still never logged.

## 2026-07-13 (add-upload)

- **Demo upload endpoint (`POST /v1/demos`, no schema bump).** Apps can
  now analyze a demo the user holds locally — the API counterpart of
  the web app's local-file flow. The raw request body (a `.mvd` or
  `.mvd.gz`, sniffed by gzip magic) is stored under the SHA-256 of its
  uncompressed content and parsed synchronously; the response is the
  same `{demoId, sha256, fromCache, schemaVersion}` shape as `loadDemo`,
  and every existing `GET /v1/demos/sha:…/*` endpoint works on it
  unchanged. Re-uploads are idempotent cache hits. **REST-only by
  design**: the tool is deliberately absent from `mvd-mcp`.
  - **Safeguards.** On-wire body cap (`-max-upload-bytes`, 64 MiB
    default, `0` disables the endpoint), 512 MiB decompressed cap, and
    in auth mode a per-key daily quota (`-upload-daily-bytes` /
    `-upload-daily-count`, 429 with `Retry-After`). A body that fails to
    parse — or parses to no actual game — is rejected 422 and its bytes
    removed, so the cache cannot be used as content-addressed storage
    for arbitrary data. Uploaded demos have no owner (readable by any
    key holder that knows the sha) and share the normal GC pool
    (evicted → re-upload); both documented in `API.md` and the OpenAPI
    description.
  - **Parser hardening (Layer 1).** Reachable-from-upload input bounds,
    protecting all callers: the MVD decoder now rejects wire block sizes
    over 8 MiB instead of allocating up to ~4 GiB from a crafted 4-byte
    header (mvdsv writes blocks from an 8 KiB buffer, `MAX_MVD_SIZE`),
    and `mvdfile` caps gzip decompression at 512 MiB with a distinct
    `ErrDecompressedTooLarge` sentinel. A new `-parse-timeout` (120s
    default) bounds how long any single cold parse can hold a parse
    slot.

## 2026-07-12 (phase-16.3)

- **The bounded damage family (schema v55).** Damage now ships in **two
  families**. The **raw** family is the v53 shape — the full hit
  including overkill, capped only at 9999, exactly the wire value
  (`unbound_dmg_dealt`, written mid-frame in `T_Damage`,
  `combat.c:795`). The new **bounded** family reconstructs KTX's own
  scoreboard `dmg_dealt` (`combat.c:783`) per hit — armor absorbed plus
  the health portion capped at the victim's remaining health — which
  until now existed only as `demoInfo`'s end-of-match totals. The
  additions are purely additive; every raw field is byte-stable except
  the tele/stomp fold-in below.
  - **Death-value reconstruction.** The wire hides the bounded value but
    reveals it almost exactly. A hit the victim **survives** has no
    overkill, so `bounded == raw` identically — no health knowledge
    needed, only that no death happened this frame. A **killing** hit's
    overkill is the end-of-frame death broadcast KTX writes (`health -=
    take`, then the negative leftover or `-1`; `combat.c:944,983-985`):
    `bounded = raw + deathValue`, the armor share cancelling out of the
    `save + take` identity (verified per-hit exact, and the death
    broadcast shares the killing hit's demo timestamp on all 10923
    corpus deaths). Same-frame multi-hit deaths cascade the one death
    value across the frame's hits in wire (application) order, last to
    first. Two wire limits force an approximate shadow-health fallback:
    KTX clamps a corpse's health at `-99` (`combat.c:259`), and a tight
    death→respawn hides the death value behind the respawn's positive
    health (recovered from the authoritative `DeathEvent`). The
    nullification rules the wire ignores are unchanged (pent/`tp`
    mates+self bounded to armor, `tp4` to 0, all skipped for a suicide),
    read from serverinfo with `tp_num()` semantics; godmode is
    unobservable and ignored.
  - **Telefrags and stomps fold their honest damage into
    `given`/`givenTeam`/`taken`** — and nothing else. KTX's scoreboard
    accumulation has no tele/stomp exclusion (`combat.c:1046-1076`, both
    map to `wpNONE`), so our fold-free exclusion was exactly the
    corpus-visible residual — every player whose KTX `dmg.team` exceeded
    our `givenTeam` had dealt a team telefrag. A telefrag folds its
    **bounded** reconstruction (victim's full armor + remaining health —
    the wire 9999 is a kill guarantee, not a measurement) into the raw
    family too; a stomp is a real ~10 HP `T_Damage` and folds wire (raw)
    / reconstruction (bounded). Both stay out of `events`, `byWeapon`,
    `matrix`, `ewep` and `totalDamage` (KTX's `weapons[].damage`
    excludes them too); each `telefrags[]`/`stomps[]` entry now carries
    the folded `bounded`/`damage` and the `victimWep` class it landed
    in.
  - **Spawn-state inference.** A tele/stomp victim is alive by
    definition (`tdeath_touch` requires `ISLIVE`), so a non-positive
    health shadow means the respawn beat the end-of-frame stat
    broadcast: the reconstruction reads that victim from spawn state
    (100 health, no armor, spawn inventory) instead of the stale corpse
    shadow — the same wire-invisibility as the match-start spawn.
  - **Skipped modes.** `k_midair` / `k_instagib` / `k_dmgfrags` rewrite
    `take` unobservably, so bounded is skipped outright there rather
    than emitted wrong: `damage.boundedMode = "skipped:<mode>"`, no
    tele/stomp fold-in, and a `dmg=bounded` request returns the new
    `422 bounded_unavailable`.
  - **Corpus validation.** `TestBoundedReconciliationCorpus` reconciles
    the reconstruction against `demoInfo` on every corpus demo — per
    player (`given`/`taken`/`ewep`/`team`) and per weapon
    (`weapons[].damage.enemy`/`team`). The death-value model tightens the
    given/taken reconciliation ~2.5×: tolerances are re-pinned at measured
    max + headroom (given/taken/per-weapon ±40, measured 16/15/18; ewep
    ±150, measured 131; team ±10, measured 1). The remaining residuals are
    the multi-hit cascade save split, the `-99` clamp / masked-death shadow
    fallback, and — for ewep only — the victim-item one-frame window (a
    same-frame RL/LG pickup reclassifying a hit's victim-weapon bucket,
    independent of the health arithmetic). The dm3 pent-deflect
    (`Satan's power deflects nlk's telefrag`) is pinned as live coverage.
    A loose warning-mode variant landed in the diagnostic harness.
  - **REST / MCP surface.** `/damage` gains `dmg=raw|bounded|both`
    (`raw` = the byte-stable v53 shape; `both` = the stored shape;
    `bounded` = the bounded family materialized into the raw field
    names). The default (unset `dmg`) is **`bounded` for both the summary
    and the full log** — the KTX-scoreboard number a reader almost always
    wants; `raw` and `both` stay explicit opt-ins. MCP `getDamage` takes
    the same `dmg` input and forwards it, so an unadorned `getDamage`
    (MCP defaults `summary=true`) serves the bounded family. An invalid
    `dmg` is a `400 invalid_param`; an **explicit** `dmg=bounded` on a
    skipped demo is the `422 bounded_unavailable` (a new drift-tested
    error code), while a **defaulted** bounded there falls back to `raw`
    (whose `boundedMode` explains the absence) instead of erroring. The
    view-layer filtered recompute reproduces both families exactly,
    including the tele/stomp fold.
  - **KTX-exact bounded on a summary.** An unfiltered bounded/both
    **summary** now sources its per-player bounded figures — `given`,
    `givenTeam`, `givenSelf`, `ewep`, per-weapon `byWeapon` — from KTX's
    exact end-of-match scoreboard (`demoInfo.players[].dmg` +
    `weapons[].damage.enemy`) when the demo carries `demoInfo`, since the
    per-hit reconstruction is only best-effort. A new
    `boundedSource: "ktx" | "reconstructed"` field records which. The
    substitution is partial: `taken` (KTX is enemy-only; ours is
    all-sources) and the `enemyVs*` buckets (KTX has no split) stay
    reconstructed, so on a `ktx` summary they may not sum exactly to the
    substituted `given`. Filtered/windowed summaries and the full-log
    response have no KTX counterpart and stay fully reconstructed.
  - Goldens regenerate at branch end (they stay v53 until then, so the
    served-spec validation augments the fixture Result with a synthetic
    bounded family in the meantime). See
    [RESULT_SCHEMA.md](mvd-analytics/RESULT_SCHEMA.md) for the
    field-level reference.

## 2026-07-12 (phase-16.2)

- **GDPR disclosure: privacy policy + terms of use on the portal (no
  schema change).** `/portal/privacy` and `/portal/terms`, linked from
  every portal page footer and acknowledged at the sign-in panel. The
  privacy policy states the audited facts: Discord `identify` scope
  only (id + username stored with the key record), keys stored as
  SHA-256 hashes shown once, strictly-necessary `/portal`-scoped
  cookies only (1 h session), access logs with best-effort IP for
  operations/abuse (kept up to one year), public hub.quakeworld.nu demos
  as the content source, Discord + hub as the only third parties, and
  the GDPR rights/contact channel. Pinned by portal tests; operator
  review notes embedded in the template.

- **RESULT_SCHEMA served standalone; portal points at the self-served
  docs; public name is mvdanalyzer-api (no schema change).**
  `GET /docs/result-schema` renders `RESULT_SCHEMA.md` (embedded from
  the mvd-analytics module root, rendered by vendored marked 12.0.2 —
  no CDN; raw markdown at `/docs/result-schema.md`; GitHub-style
  heading anchors so internal `#links` work; repo-relative links
  rewritten to GitHub). The OpenAPI spec's deep-contract link now
  points there instead of GitHub. The portal landing links `/docs`,
  `/openapi.yaml` and `/docs/result-schema`, gains a short MCP section
  (endpoint `<origin>/mcp`, no key needed, one-line client-add
  example), and keeps exactly one GitHub link (source/issues) — pinned
  by a landing-page test. The API's public name is **mvdanalyzer-api**
  (spec title, docs pages, portal); the server binary and module stay
  `mvd-api`.

- **The served OpenAPI spec is now the single per-endpoint reference;
  API.md slims to a high-level guide (docs only).** `/openapi.yaml` +
  `/docs` are self-contained — no more dead repo-file references in
  rendered descriptions (reducer registry, tele/stomp pseudo-codes, the
  artifact mapping table, the overview wall-clock formula are inlined;
  the one external link is an absolute GitHub URL to RESULT_SCHEMA.md
  for deep Result internals). API.md drops its ~700-line §4 endpoint
  reference (three-way duplication with the spec and RESULT_SCHEMA.md)
  and keeps getting-started, conventions, endpoint-choice, and recipes.
  Spec examples use a real demo (`gameId:145060` and its SHA).

- **Columnar buckets carry their own loc legend (schema v53).** The
  `/buckets` `layout=column` envelope — the REST and MCP default — gains
  `locTable`, the demo's interned loc-name table, present iff an `li`
  column is in the output. Columnar deliberately keeps the compact raw
  `li` index instead of repeating name strings per bucket, but that
  meant the default `getBuckets` handed an MCP agent undecodable
  integers and forced a `getLocTable` round trip + join. The legend
  makes columnar responses loc-self-contained; `loc=index` workflows
  and `/loc-table` are unchanged. View-shape-only change; the bump
  exists so schemaVersion-keyed immutable ETags stop revalidating
  pre-legend bodies. RESULT_SCHEMA's version-history table also gains
  the v51/v52 rows that 16.1 omitted.

- **OpenAPI 3.1 spec + `/docs` viewer; `weapons` param rename (no schema
  change).** Phase 16.2 — the REST surface now ships a machine-readable
  contract for external integrators:
  - **`GET /openapi.yaml`** serves a hand-authored OpenAPI 3.1
    description of all 35 operations with **full field-level response
    schemas**, embedded in the binary. It is pinned to the code by
    drift tests (route parity with the router in both directions,
    error-code/artifact/field-code enums, `info.version` =
    schemaVersion) and its response schemas are **validated against
    real golden-corpus responses** through the real router — ~50 cases
    covering every operation, the shape-changing param variants
    (`summary`, `layout`, `loc=index`, all field codes) and every error
    class. **`GET /docs`** is a browsable reference over it (vendored
    RapiDoc, no CDN). Both auth-exempt.
  - **`weapons` replaces the singular `weapon` query param** on
    `/frags`, `/damage`, `/backpacks`, `/weapon-pickups`. REST keeps
    `weapon` as an accepted legacy alias (canonical wins when both are
    present), so existing integrations keep working. **The MCP tool
    input field is renamed outright** (`weapon` → `weapons` on
    getFrags/getDamage/getBackpacks/getWeaponPickups) — MCP clients
    re-read schemas per session; hardcoded callers must rename.
  - Tool-description papercuts: getOverview names the winner
    (frag-sorted, index 0), getDamage/getAim lead with their MCP
    `summary=true` default and state the telefrag exclusion once, the
    items/weapon-pickups/backpacks trio cross-links its division of
    labour, and the generic-artifact operation gains the full node-name
    ↔ curated-route ↔ `resultKey` ↔ 422-code table (in the served spec).
  - Robustness fallout from the validation harness: the buckets
    first-value reducers no longer panic on empty-but-non-nil change
    streams.

## 2026-07-12 (phase-16.1)

- **Fresh-eyes review fixes: the remaining size footguns + doc drift
  (no schema change).** Two independent no-history design reviews of
  the MCP and REST surfaces (both verdicts: sound); their confirmed
  findings:
  - `getStreamSlice` now **requires a time window at the MCP layer**
    (either bound suffices; REST stays unwindowed) — an unwindowed
    slice is native-rate entries for the whole match, the exact class
    of payload the windowMs/summary defaults exist to prevent.
    `getLocTrails` defaults `minDwellMs` to **250** at the MCP layer
    (explicit `0` restores the raw stream) — raw trails are dominated
    by nearest-loc boundary flicker.
  - `/weapon-pickups` `source` is now validated like the other enum
    params: a typo (`source=backpak`) 400s with the valid values
    instead of silently matching nothing.
  - Doc truth: API.md documents the **50 ms REST `windowMs` default**
    (with a warning), the deliberate no-pagination stance, the scoped
    per-endpoint cache-header sets, the map-endpoint ETag shapes, and
    the missing `opening_unavailable` error row; the MCP README's
    `listArtifacts` section now shows the trimmed shape, the events
    default-type list includes `pickup`, and `getFrags` explains why
    its `summary` default is deliberately the opposite of its
    siblings'. `windowMs`/`minDwellMs` docs now contrast ms vs the
    seconds-typed window params in the same call.

- **listArtifacts goes routing-only at the MCP layer; timeline artifact
  gets a size warning (no schema change).** External-review follow-ups:
  `listArtifacts` now returns servable artifacts only, trimmed to
  `{name, resultKey, cost, lazy, description}` — the DAG edges and
  internal nodes stay on REST `/v1/artifacts`/`/v1/graph` where they
  serve the dev story. The `timeline` node description now warns it is
  one of the largest sections and points at the windowed views. The
  reported "opaque MCP error" regression did not reproduce: two new
  end-to-end tests (in-memory + streamable HTTP, real go-sdk client)
  pin that REST 4xx bodies — including the enumerated field-code list —
  arrive verbatim in the isError text.

- **Friction + correctness edges (schema v52,
  [PLAN-api-usability](PLAN-api-usability.md) workstream C).**
  - **No-match-start demos are flagged, not coerced (v52):** when no
    match start is detected the rebase never runs and every timestamp
    stays on the raw demo clock — previously indistinguishable from a
    rebased result. `streams.global` now carries `timeBase: "demo"`
    (omitted normally) and `errors[]` gains a matching notice, so
    `/overview` surfaces it.
  - **Unknown field codes now teach the vocabulary:** the
    `state-at`/`buckets`/`stream-slice` field error enumerates every
    valid code with a gloss (`li (location), h (health), …`). The
    classic `loc`-vs-`li` trap is also documented in the MCP tool
    schemas (the selector code is `li`; the `loc=` param picks name-
    vs-index rendering). No aliases, by decision (D6 amended).
  - **Float-artifact fix on the view envelope (D8 amended):** all view
    ms→seconds conversions now divide by 1000 (IEEE division is
    correctly rounded) instead of multiplying by the inexact `0.001`
    — `13.155000000000001` becomes `13.155` across events, trails,
    buckets, stream-slice. Values change at most one ulp; shapes
    unchanged.
  - **Doc-debt:** the stale `result/shots.go` comments claiming the
    stream is not match-gated (pre-v50) are fixed; RESULT_SCHEMA
    documents the `demoInfo` time island (KTX seconds on KTX's clock)
    and the items phase time sentinels; powerup events in the events
    view no longer echo the derivable `duration` (the Result keeps
    it); `getOverview`'s description spells out the ms-out/seconds-in
    units seam.

- **Filter + summary pass over the item/pickup endpoints and MCP-layer
  token-lean defaults (no schema change,
  [PLAN-api-usability](PLAN-api-usability.md) workstream B).**
  - `/items` gains `from`/`to` (keeps phases **overlapping** the window)
    and `summary=true` (per-item `{takenCount, byPlayer, firstTake}`
    with takes counted **inside** the window) — "who took X in the
    opening minute" no longer fetches the full-match phase timeline.
    `/backpacks` and `/weapon-pickups` gain `from`/`to` on the
    drop/pickup time. The MCP proxy now forwards `startTime`/`endTime`
    for `getRegionControl` (REST already accepted them; the proxy
    silently dropped them).
  - **MCP `windowMs` default is now 5 s (was 1 s)** for `getBuckets`
    and `getRegionControl`: a 20-min match at 1 s is ~1200 buckets per
    field per player — 5 s resolves the trend/control questions a
    bucketed timeline answers (shortest interesting run, a quad, is
    30 s) at a fifth of the payload. Pass `windowMs` explicitly for
    finer resolution; REST keeps its 50 ms default.
  - **MCP defaults diverge from REST toward token-lean (D1):**
    `getDamage`, `getAim`, and `getItems` now default `summary: true`
    at the MCP layer only — REST keeps full logs by default. A
    defaulted summary response carries a `hint` field telling the agent
    to pass `summary: false` for the dropped detail. Precedent: the
    proxy's existing `windowMs` 50→1000 override.
  - **`searchGames` goes compact and paginates honestly:** rows now
    project each roster entry to `{name, team, frags}` (the verbatim
    hub rows — per-player ping, color arrays, name_color — return with
    `roster: true`), and the response gains `total` (all matching rows,
    via PostgREST `count=exact`) alongside the page-local `count`.
    Tool descriptions now also document that `loadDemo` is optional
    (analysis tools auto-load `gameId:N` on first use).

- **The match opening becomes first-class (schema v51,
  [PLAN-api-usability](PLAN-api-usability.md) workstream A).** Three
  related changes driven by the first real hosted-MCP agent session
  (an opening-race question cost ~45 tool calls of timestamp
  cross-referencing):
  - **Initial-spawn bug fix.** KTX respawns every player when the
    countdown ends (`SM_PrepareClients` → `k_respawn`,
    ktx/src/match.c:881,972), but a player alive through the countdown
    never crosses health ≤0→>0, so the parser's dead→alive detector
    missed the first — most contested — spawn of the match. The
    timeline now synthesizes a `t=0` spawn in `streams.players[].sp`
    for every player alive at match start whose respawn wasn't
    wire-visible.
  - **`pickup` events with full identity.** `/events` (and MCP
    `getEvents`) gains a default `pickup` type joined from the
    authoritative `items` / `weaponPickups` sections:
    `detail{item (ya_1 vs ya_2), kind, entNum, loc, source
    (world/backpack/unknown), dropper?}` — "which YA / which RL pad"
    no longer needs a second call and timestamp cross-referencing.
    `spawn` events now carry `detail{loc}` when the location is
    resolvable (sampled just after the teleport landing; absent on
    maps without a .loc corpus).
  - **New `opening` artifact.** A post-processor projects each
    player's match-start spawn loc plus the first in-match take of
    every contested spawner (armors, mega, powerups, RL/LG) into
    `Result.Opening` — served via `GET /v1/demos/{id}/artifacts/opening`
    and MCP `getArtifact('opening')` with zero new tools. One small
    call per demo answers the opening-race question class.

## 2026-07-09

- **Hosted MCP is now unauthenticated (no schema change).** `mvd-mcp -http`
  no longer requires (or validates) a per-request `Authorization` key — web AI
  chat connectors can use the bare `/mcp` URL. The shim instead authenticates
  itself to `mvd-api` with an operator-issued service key (`MVD_API_KEY` env
  var), forwarded on every proxied REST call; the REST API keeps full API-key
  auth, and that one key's service rate class throttles all anonymous MCP
  traffic. A client that does present a `qwmvd_…` bearer gets it forwarded
  instead (own bucket + log identity); non-`qwmvd_` bearers (e.g. platform
  OAuth tokens) are ignored. In stdio mode `MVD_API_KEY` now also supersedes
  `-label`, so a local shim can talk to an auth-enabled `mvd-api`.

- **`getFrags` / `getDamage` (`/frags`, `/damage`): filters now narrow ALL
  aggregates + new `from`/`to` window + `summary` mode; damage output is now
  match-only (schema v50).** Changes to the frag/damage endpoints and their
  MCP tools:
  - **Filters narrow every aggregate (bug fix).** Previously a `players` /
    `weapon` filter only narrowed the per-event log and the `byPlayer` keys,
    leaving `totalFrags` / `totalDamage` / `byWeapon` / `matrix` at their
    unfiltered values — an internally inconsistent response. Now, when any
    scoping filter is active, every aggregate is **recomputed from the filtered
    log** so it matches exactly the entries shown. With no filter the
    authoritative stored totals are returned unchanged (byte-identical to
    before) — the unfiltered path is untouched. Filtered damage responses now
    also populate `matrix` / `events` (previously left null). Filtered frag
    aggregates are log-sourced and may differ from the unfiltered totals for
    reconnect / unresolved-name edge cases (documented in API.md §4.5/4.5b).
  - **`from` / `to` time window** (REST; `startTime` / `endTime` on the MCP
    tools) — match-relative **seconds**, keep only frags/hits in the window.
    The seconds→ms conversion now rounds to the nearest ms (`0.29s`→`290ms`),
    not truncating.
  - **`summary`** — drop the big per-event log and return only the aggregates
    (avoids overflowing an LLM context). Orthogonal to the filters.
  - **Damage output is now match-only (schema v50 bump).** The per-hit
    `damage.events` log is **gated to in-match at the source** (the damage
    analyzer), matching the aggregates — out-of-match (warmup / post-match)
    hits are dropped everywhere and no longer exposed. This makes the filter's
    all-players recompute reproduce the stored aggregates **exactly** (closing
    a filtered over-count where the ungated log double-counted warmup hits),
    lets the aim analysis read exactly-in-match damage (removing an approximate
    `[0,matchEnd]` self-window), and fixes a latent bug where
    `timelineAnalysis.airgibs` counted warmup / post-match rocket airgibs.
    Goldens regenerated: `damage.events` arrays shrink; some `aim` splits and
    `airgibs` lists shift on demos with out-of-match rocket hits. Frags are
    unaffected (they already gated at the analyzer).
  - **Telefrags / stomps are match-only, match-clock, and exclude team
    kills.** The `damage.telefrags` / `damage.stomps` arrays are gated to
    in-match at the source, and a **team** telefrag/stomp is no longer credited
    to the attacker's `telefrags` / `stomps` counter (mirroring the team-kill
    convention the view already applied) — so the filtered counters match the
    stored totals. Their `time` is now **match-relative ms** like
    `damage.events`: only the per-hit log was rebased before, leaving the
    positional-kill arrays on the demo clock, contradicting the schema and the
    `from`/`to` window / telefrag+stomp event lenses that compare against
    match-relative bounds. The in-match gate now keys off the match **timestamp
    range** (the detector's final start/end) rather than a live match-phase
    flag sampled per record, fixing a same-frame race: a kill on the exact
    match-start frame — decoded before the same-frame "Fight" print flipped the
    detector — was wrongly dropped, and now appears at match-relative `t=0`
    (the inclusive upper bound likewise keeps a hit on the exact match-end
    frame, which KTX itself scored). Goldens: telefrag/stomp times shift by
    `-demoOffset`; a couple of start/end-frame entries reappear with the
    counters they imply.
  - **All analytics streams are match-only now, except chat.** The `shots`
    stream is gated to in-match at the source (warmup / prewar / post-match
    fires dropped; the `Shot.warmup` field is removed — no out-of-match shot
    survives). The shots gate keys off the same match **timestamp range** as
    damage, so a fire on the exact match-start/-end frame is kept rather than
    lost to the same same-frame race. Frags, damage, telefrags/stomps, shots,
    positions, pickups, items and backpacks are all match-only; **chat is the
    deliberate exception** (pre-game talk is kept). This makes it impossible
    for a consumer to accidentally mix warmup data into match analytics.
  - **Windowing consistency + bound validation nits.** `chat` now rounds its
    `from`/`to` seconds→ms to the nearest ms like the other windowed sections
    (it truncated before, off by up to 1 ms); a windowed / players-scoped
    `aim` that matches no shooter returns `players: []`, not `null`, matching
    the filtered-empty-log convention; and the REST layer now rejects a NaN /
    Inf / negative / int32-ms-overflowing `from` / `to` / `time` with a `400
    invalid_param` instead of letting the bad float→int32 conversion silently
    filter everything behind a `200`.

- **`getAim` (`/aim`): `players` / `from` / `to` / `summary` filters (no schema
  change).** The aim endpoint and its MCP tool used to return everything
  (~70 KB), overflowing an LLM context. New query-layer filters, consistent
  with the frag/damage discipline above:
  - **`summary`** (bool) — return only the compact per-player `weapons`
    aggregates, dropping the large per-fire `crosshair` + `lgRamp` sample
    arrays. The overflow fix; recommended default for the MCP tool.
  - **`players`** (csv) — scope to named shooters. With no time window this
    selects their **match-wide** stored aim (same as `getFrags?players=`).
  - **`from` / `to`** (REST; `startTime` / `endTime` on the MCP tool) —
    match-relative **seconds**. Setting a window **recomputes** aim over the
    shots in it, so every figure (weapons accuracy, RL/GL direct/splash, LG
    ramp, crosshair samples) scopes to the window consistently. With no window
    the stored aim is returned unchanged (no recompute).
  - **Refactor: the aim computation core moved to package `aimcore`**
    (`aimcore.Compute`), imported by both the analyzer (fills `res.Aim` once)
    and the view layer (windowed variants) without an import cycle. The stored
    aim is **byte-identical** to before (goldens unchanged) — the extraction
    preserved behaviour exactly. No schema bump (`CurrentSchemaVersion` stays
    50); the `AimResult` struct is untouched.

## 2026-07-08

- **Fix: cold demo loads failed with a 502 `hub_upstream` hash mismatch.** The hub's `demo_sha256` is the hash of the *uncompressed* `.mvd`, but the CDN serves gzip and the phase-3 integrity check hashed the gzipped download — so every un-cached `loadDemo`/`POST /v1/demos/{id}` was rejected. The check now authenticates the *decompressed* content (or a raw `.mvd` fallback) against `demo_sha256`; corruption is still rejected. mvd-api change; redeploy it.

- **mvd-mcp: array/map tool filters fixed (`players`, `fields`, `types`, `weapon`, `items`, `kinds`, `reducers`).** jsonschema-go reflected every nilable slice/map to a `["null", X]` type union; some MCP clients coerce a union to a string, silently disabling those filters. Tool input schemas now advertise a plain `{"type":"array"}`/`{"type":"object"}` (null stripped). No API/output change; rebuild+redeploy `mvd-mcp` and reconnect the client to pick up the new schemas.

- **mvd-mcp over streamable HTTP + deploy templates (no schema change;
  transport/auth layer).** The MCP shim gains a hosted mode; stdio is unchanged.
  - **`mvd-mcp -http ADDR`.** Serves MCP over streamable HTTP (go-sdk
    `NewStreamableHTTPHandler`, Stateless) instead of stdio, for hosting the
    service publicly. Empty `-http` keeps today's stdio behaviour byte-identical;
    the two transports are mutually exclusive. No new dependencies.
  - **Per-request API-key auth.** Every MCP request must carry
    `Authorization: Bearer qwmvd_…`. An outer gate validates the key against
    `mvd-api`'s `GET /v1/auth/check` (fail-closed; `401` + `WWW-Authenticate:
    Bearer` otherwise) — this single gate also protects the `searchGames` tool,
    which bypasses `mvd-api`. On success the key is forwarded on every proxied
    REST call, so `mvd-api` stays the single point of validation; a key revoked
    mid-session stops working on the next call. `-label` is ignored in HTTP
    mode. The handler is mounted at `/mcp` (and `/mcp/`), with an
    unauthenticated `GET /healthz`.
  - **Deploy templates (`deploy/`).** A `Caddyfile` (TLS, `/mcp*` → mvd-mcp,
    rest → mvd-api, real-client-IP `X-Forwarded-For`), `mvd-api.service` /
    `mvd-mcp.service` systemd units (hardened, secrets via `EnvironmentFile`),
    and a provisioning `README.md` runbook with a smoke-test checklist. Tracked
    templates, not run by CI.
  - **Documented omission.** `/los`, `/shots`, `/streams/*`, and `/airgibs`
    still have no curated MCP tool (adding them is deferred); noted in the
    mvd-mcp README.

- **mvd-api Discord key portal + key-store cross-process lock (no schema
  change; transport/auth layer).** Optional, off by default — nothing changes
  for existing localhost users, and analytics output is untouched.
  - **The portal (`internal/portal`).** With `-portal -portal-base-url URL`
    (plus `-auth-dir` and the `DISCORD_CLIENT_ID` / `DISCORD_CLIENT_SECRET` /
    `PORTAL_COOKIE_SECRET` env vars), the server serves a one-page portal at
    `/portal` where a user signs in with Discord (OAuth2 scope `identify` only)
    and self-services one API key. `POST /portal/key` shows the full key once
    and revokes any prior key for that user. Without `-portal`, the `/portal`
    routes are not registered at all (they 404) and the server is unchanged.
  - **Session security.** The only server-trusted state is a 1-hour
    HMAC-SHA256-signed session cookie (no server-side store); the OAuth `state`
    nonce is double-submitted against a signed cookie (CSRF); all cookies are
    `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/portal`. The Discord username
    is HTML-escaped (`html/template`), and neither the cookie secret nor the
    Discord client secret ever reaches a log line, error body, or page. The
    portal adds **no new dependencies** (stdlib `net/http` OAuth, `crypto/hmac`,
    `html/template`, `embed`). Cookie `Secure` follows the `-portal-base-url`
    scheme — `https` ⇒ Secure (production), `http` ⇒ non-Secure (local dev
    only, since a browser will not send a Secure cookie over http); the server
    logs a startup warning whenever cookies are non-Secure.
  - **Key store cross-process lock.** `internal/authkeys` mutations
    (`Issue`/`Revoke`) now take a cross-process `flock` and reload `keys.json`
    under the lock before writing, so the portal (issuing inside the live
    server) and a concurrent `keys` CLI can no longer clobber each other's
    writes. Reads stay lock-free. A new `ActiveByDiscordID` accessor backs the
    portal's key-status display.
  - **Deferred (manual, needs an operator-provisioned Discord app):** the
    end-to-end run against the real Discord OAuth flow. CI proves the flow with
    an httptest Discord stub.

- **mvd-api API keys + auth middleware (no schema change; transport/auth
  layer).** Optional, off by default — nothing changes for existing localhost
  users, and analytics output is untouched.
  - **Two modes.** Without `-auth-dir` the server is unauthenticated, exactly
    as before (the `Authorization: Bearer <label>` value stays a non-secret
    access-log tag). With `-auth-dir DIR`, every `/v1/*` route and
    `POST /v1/demos/{id}` requires `Authorization: Bearer qwmvd_…`; missing /
    invalid / revoked keys get a generic `401` + `WWW-Authenticate: Bearer`.
    Exempt: `/healthz`, `/v1/version`, `/portal/*`, and `OPTIONS` preflight
    (CORS answers preflight before auth, so browsers are unaffected).
  - **Key store.** New `internal/authkeys`: one atomic-written `keys.json`
    holding only SHA-256 hashes of keys (`qwmvd_` + 32 random bytes,
    base64url). The plaintext key is shown once at issuance and never
    persisted; lookups are constant-time hash compares. One active key per
    Discord user (re-issuing revokes the prior one).
  - **`keys` CLI.** `mvd-api keys issue|revoke|list -auth-dir DIR` manages the
    store directly; `list` shows hash prefixes + metadata, never a full key or
    hash.
  - **Per-key rate limiting.** Token bucket per key hash (stdlib, no new
    dependency), split into `user` and looser `service` classes
    (`-rate-user`/`-burst-user`, `-rate-service`/`-burst-service`); over-limit
    → `429` + `Retry-After`. This closes the rate-limit half of the F15
    throttle by keying on the validated API key rather than the spoofable IP.
  - **`GET /v1/auth/check`** → `204` for a live key, `401` otherwise (key
    self-test; also the MCP-HTTP pre-validation hook).
  - **Secret safety.** In auth mode the raw key is never logged — the access
    log's identity becomes the key's note / Discord name / hash-prefix. The
    `401`/`429` bodies are generic and leak nothing about key state.
  - **Hardening.** `-auth-dir` is force-tightened to `0700` on open (so a
    pre-existing loose dir can't leave key metadata world-listable), a corrupt
    `keys.json` fails loudly at startup rather than presenting an empty store,
    auth exemptions are path-cleaned (no `/portal/..`-style traversal past the
    key gate), and an unknown top-level subcommand now errors instead of
    silently booting a server (`serve` is still the default).

- **Analytics pipeline: the core/derived/post-processor "tiers" are collapsed
  into one task model (no schema bump, `Result` byte-identical).** The tiers
  were a pre-DAG remnant — once the topological sort over declared
  `Requires`/`Provides` edges took over ordering, the tier label no longer
  drove anything. `RegisterCore`/`RegisterDerived` merge into a single
  `Register`; every node is now just a task with declared edges, differing only
  in whether it reads events (analyzer) or only refines the assembled `Result`
  (post-processor), plus the one lazy node (`los`). API-visible changes, both
  in the DAG-introspection surface only (not demo analytics):
  - `GET /v1/artifacts` manifest entries **no longer carry `tier`** — the only
    real distinction it conflated (lazy vs eager) is already the `lazy` flag.
  - `GET /v1/graph` nodes **replace `tier` with `depth`** (the node's layer in
    the dependency DAG), and `qw-analyze -graph mermaid` now groups nodes into
    depth layers instead of tier subgraphs (`los` marked with a dashed outline).
  - `ARTIFACTS.md` drops its `Tier` column (regenerated).

## 2026-07-07

- **mvd-api hosting-prep hardening (no schema change; transport/ops
  hardening).** Prerequisites for exposing the API publicly; nothing in
  the analytics output changes.
  - **Cache quota + GC.** New `-cache-max-bytes` (default 20 GiB; `0`
    disables). A background sweep evicts the oldest tier-1 MVDs, tier-2
    gobs and tier-3 artifact gobs (ordered by mtime, bumped on every
    cache hit — atime is unreliable on relatime/noatime mounts) once the
    disk budget is exceeded, never blocking the request path. Startup
    deletes tier-2 trees orphaned by past schema/format bumps (including
    legacy suffix-less `v<N>` trees), stale-version tier-3 gobs (also the
    retired `shot-streams@*` ones), and stale atomic-write temp files.
    New `mvd-api cache stats` and `mvd-api cache prune [-max-bytes |
    -older-than | -all]` ops subcommands.
  - **Parse throttle.** New `-max-parses` (default `max(1, NumCPU/2)`): a
    semaphore bounds concurrent heavy cold operations so a storm of
    distinct cold demos can't spawn unbounded parallel work. It covers
    both the cold download+parse and the on-demand LOS raycast (no schema
    change), closing the unauthenticated-CPU-exhaustion path on
    `/los`/`/artifacts/los`. Cache hits are unaffected; rate limiting
    proper arrives with API keys. `cache prune` gains `-dry-run` and
    rejects the silent no-op `-max-bytes 0`.
  - **Capped hub reads.** Demo downloads (CDN and `demo_source_url`) are
    read through a 64 MiB `io.LimitReader`, so a broken or hostile
    upstream can't OOM the process; over-cap responses are upstream
    errors (`502`), never `404`.
  - **CORS.** Permissive `Access-Control-Allow-Origin: *` with
    `Expose-Headers` (ETag/X-Cache/X-Schema-Version/X-Request-Id) and
    `OPTIONS` preflight, so browser clients on any origin can call the
    API (API.md §2.6).
  - **Error hygiene.** Every response carries `X-Request-Id`; `5xx`
    bodies are now a generic message + that id (the real error, which can
    embed cache paths / upstream URLs, goes to the server log only) —
    covering the curated endpoints, the generic artifact endpoint, and
    panics (the `panic` error code folds into `internal`). `4xx` messages
    stay specific. Also: `POST /v1/demos/{id}` no longer carries
    `Cache-Control`/`ETag`; error bodies no longer carry a stray `ETag`;
    `/los` and `/artifacts/los` return `{"players":[]}` (not `null`) when
    a demo has no streams; `/v1/maps/{map}/entities` now honours
    case-insensitive `types`/`kinds` param names.

- **API: the spatial weapon-fire streams are now built on every parse instead
  of behind a lazy re-parse (no schema bump, response bodies unchanged).**
  mvd-api parses each demo with `BuildShotStreams`+`BuildNails` on
  (`internal/democache` `defaultParse`), so the projectile/beam/nail streams and
  the stream-enriched `shots`/`aim` blocks are baked into the cached `Result`.
  This costs ~+3–4% parse time and ~+5% cache size and **deletes** the lazy
  `shot-streams` artifact, its full second parse on first request (2.4–5 s), the
  two-state Shots/Aim, the degrade path, and the shipped caching bug it caused.
  Behavioural notes for callers:
  - **`/shots`, `/aim`, `/streams/{projectiles,beams,nails}` no longer pay a
    second parse on the first request** — they are plain reads off the base
    Result. Their bodies are byte-identical to before for a warm enriched demo
    (mvd-api has served the enriched bodies on every request since phase 5.3).
  - **The `X-Shot-Streams: unavailable` degrade header (and its
    `Cache-Control: no-store`) is removed.** There is no rebuild that can fail on
    evicted bytes anymore. Callers that sniffed the header should stop.
  - **Local API caches re-parse each demo once.** The tier-2 path gained an
    internal cache-format suffix (`results/v<N>f2/…`, `resultCacheFormat` in
    `internal/democache/paths.go`) so lean pre-phase-12 gobs are never served as
    full; they are ignored and re-parsed on next touch. No schema bump — the
    ETag stays `"<sha>-v49"`. Stale `shot-streams@*.gob` tier-3 side-gobs are
    inert until the hosting-prep GC reaps them.
  - `los` is untouched: it stays a genuinely lazy tier-3 artifact (its ~2.5 s
    raycast has no in-pipeline consumer). The generic artifact endpoint, the DAG
    manifest, `-graph`, `ARTIFACTS.md`, and the mvd-mcp `getArtifact` vocabulary
    drop `shot-streams` (now 22 nodes, 1 lazy).
- **Web: the WASM parse now also builds nails (`BuildNails`), so the nail map
  overlay lights up automatically and the Aim tab gains ng/sng rows** (generic
  `shots`/`hits`/`hit %` columns — nail accuracy is approximate, so no
  direct/splash split). Adds ~+3–4% to the in-browser parse, no extra download.

- **Internal: the two fix-up post-processors now produce named FINAL
  artifacts instead of anonymously patching an earlier node (DAG contract
  clarification, no schema bump, byte-identical output).** Telefrag-teamkill
  recovery (node renamed `recover-telefrag-teamkills` → **`frags-final`**,
  publishing artifact **`frags:final`**) still appends the recovered kills to
  the raw `frag` log; scoreboard-stats (node renamed `scoreboard-stats` →
  **`match-final`**, publishing **`match:final`**) still folds the corrected
  kills/deaths/suicides into `match`. The win is the dependency vocabulary:
  `match-final` now **requires `frags:final`** (not the raw `frag`), so any
  future in-pipeline consumer of the recovered log or corrected scoreboard
  binds it by the semantic `:final` name and can never silently get the
  pre-fix-up value. The raw `frag` / `match` nodes keep their served
  `frags` / `match` resultKeys (the JSON is final by serve time since all
  nodes run); the `timeline` node deliberately stays a consumer of the RAW
  `frag` log (streaks / kill events are built pre-recovery, matching every
  golden). Both fix-ups still write in place, so both keep `Mutates:true` —
  the `:final` artifact name is what disambiguates *which* value. Regenerated
  [`ARTIFACTS.md`](mvd-analytics/ARTIFACTS.md) and the README DAG diagram; the
  `/v1/artifacts`, `/v1/graph`, and `-graph` surfaces (unmerged phase branches
  only) show the new names. No Result/schema change.

- **Internal: analyzer output is now a tested pure function of the demo
  (order-independence hardening, no schema bump).** The pipeline is an
  explicit DAG (`analyzer/dag.go`) executed in a topological order whose
  tie-break froze the legacy registration order; that made order-freedom a
  believed-but-unenforced property. It is now enforced: `Result.Errors`
  (previously appended in execution order — stream abort, then per-node
  Finalize / post-processor failures) is canonicalised at the end of
  `analyzeSource` (stream-abort entry first, then lexicographic), and a new
  `TestOrderIndependence` runs representative corpus demos under the default
  order plus seeded-random valid topological orders and asserts the
  marshalled Result is byte-identical — which also continuously verifies the
  declared edge list is complete (an undeclared cross-node read shows up as
  a byte diff). An opt-in `TestPhaseTimingsReport` (`MVDA_TIMINGS=1`)
  aggregates per-node timings and reports the DAG critical path vs the
  serial Finalize+post tail. Diagnostics only; goldens are unchanged.

- **API: automatic artifact surface — the DAG is now self-describing and
  generically servable (additive endpoints, no schema bump).** The analyzer
  DAG is exposed as data (`analyzer.ArtifactManifest`), and mvd-api gains three
  additive routes: `GET /v1/artifacts` (the manifest — every node's name, tier,
  cost, `lazy`, requires/provides, `resultKey`, `servable`, description; static,
  ETag `"artifacts-v<n>"`), `GET /v1/graph` (the DAG as `{nodes,edges}`, static),
  and `GET /v1/demos/{id}/artifacts/{name}` — a generic accessor that
  materialises and serves any servable artifact by name. The generic endpoint is
  a closed registry (unknown/internal names → `404 artifact_unknown`; no user
  input reaches the filesystem beyond the validated name), accepts **no** query
  params (parameterised reads stay the view endpoints → `400 invalid_param`), and
  carries a finer per-artifact ETag `"<sha>-<name>@v<n>"`; eager artifacts reuse
  the curated 422-vs-200 availability convention, the two lazy artifacts route
  through `EnsureLOS`/`EnsureShotStreams` (same degrade + bodies as `/los`,
  `/shots`). mvd-mcp gains two matching tools — `listArtifacts` and
  `getArtifact` — so a **new** analytics artifact becomes reachable everywhere
  with zero new hand-written endpoints or tools; the curated tools/endpoints stay
  the ergonomic surface and are byte-unchanged. New generated catalog
  [`mvd-analytics/ARTIFACTS.md`](mvd-analytics/ARTIFACTS.md)
  (`make artifacts-md`, drift-tested). Per-deployment Heavy-disable knob deferred.
  No schema bump.

- **API: lazy artifacts (LOS, shot-streams) now persist across restarts and
  cache evictions — new tier-3 per-artifact cache (no schema change).** The two
  hand-rolled lazy passes are generalised into the DAG engine as `lazy` nodes
  (`los`, `shot-streams`), registered by name behind one `LazyArtifact`
  registry (`analyzer/materialize.go`) and shown in a fourth `-graph` tier.
  mvd-api gains a third cache tier — `artifacts/<sha[:2]>/<sha>/<name>@v<EV>.gob`
  — so a lazy compute (the multi-second LOS raycast, or the full MVD re-parse
  behind `/shots`, `/aim`, `/streams/*`) is written to disk on first request
  and spliced back on later ones, surviving a process restart or an LRU
  eviction (closes API follow-up F8b). Both `EnsureLOS` (new) and
  `EnsureShotStreams` run one generic flow: latch → tier-3 load → else compute
  → write tier-3, serialised per demo SHA. The F12 single-variant shot-stream
  rebuild and the `X-Shot-Streams: unavailable` degrade are preserved exactly
  — the degrade now fires only when *both* the tier-3 artifact and the tier-1
  bytes are absent. `/los`, `/shots`, `/aim`, `/streams/*` are byte-identical
  from a client's view (status, bodies, ETags, `X-Cache`). `EV` is the current
  schema version, so a schema bump invalidates tier 3 exactly like tier 2. No
  schema bump.

- **Analytics: team labels born correct; the whole-Result duel rewrite is
  gone (no schema change, byte-identical output).** A new `roster` core
  node (the last core node) owns the canonical player/team table with the
  duel (player-name-as-team) rewrite folded in: it publishes the duel
  verdict, the participant set, and `TeamFor(name, rawTeam)`. Every
  producer stamps its final team label through `co.TeamFor` at emission —
  streams, timeline events, messages, item/weapon/backpack pickups, and
  the shot fire log (whose victim-kind classification is now duel-aware at
  birth, so the shared-colour-team enemy/team fold happens once, correctly,
  instead of being reclassified afterwards). The two result-restructuring
  duties move to the analyzers that own those results: the DemoInfo team
  rewrite into `RosterAnalyzer`, and the `Match.Players` participant
  rebuild — including the demoinfo-authoritative merge that recovers a
  teamless frogbot the spectator gate drops — into `MatchAnalyzer`. The
  `normalizeDuelTeams` post-processor (which had to enumerate every
  team-labelled field by hand and grew four more sections in v45/v46) and
  the `isDuelResult` helper are deleted; `DamageAnalyzer` reads the duel
  verdict from the roster it already used at birth. The `teams:final` DAG
  barrier retires with it. Verified byte-identical on the golden corpus
  (the three 1on1 goldens are the referees); the shared-colour and bot
  duels the corpus lacks are pinned by ported unit tests.

## 2026-07-06

- **Analytics: timestamps born match-relative; the whole-Result time
  rebase is gone (no schema change, byte-identical output).** A new
  `clock` core node owns the match time base (match start/end, demo
  offset, pauses, wall-clock anchor — absorbing the old
  demo-start-anchor pass). Every producer converts demo-clock ms to
  match-relative ms against `co.Clock` in its own Finalize, so the
  `normalizeMatchRelativeTimes` post-processor — which had to enumerate
  every timestamped field by hand and silently missed newly added ones
  (the v48 killEvents bug) — is deleted, along with
  `deriveDemoStartAnchor`. The `epoch:match` DAG barrier retires with
  it. Verified byte-identical on the golden corpus and on off-corpus
  demos including pause-carrying and reconnect demos.
- **Analytics pipeline: explicit dependency DAG + `qw-analyze -graph`
  (no schema change).** The analyzer/post-processor execution order was
  previously implicit — four different mechanisms expressed ordering and
  only one was checked, so a wrong registration order was a silent data
  bug. Each node now declares the artifacts it Requires and Provides
  (`mvd-analytics/analyzer/dag.go`); `NewDefaultRegistry` validates the
  wiring at construction (every dependency has exactly one provider, no
  cycles — a typo panics with a message naming the offending artifact and
  node) and derives the execution order from it with a deterministic
  topological sort. The derived order is byte-identical to the historical
  registration order (asserted by a structural test), so the `Result` is
  unchanged. New `qw-analyze -graph mermaid` / `-graph json` prints the
  pipeline DAG (nodes, edges, tiers) without needing a demo.
- **Web Aim Stats: LG Unresolved column, accurate hover on narrow
  layouts, DYaw sign docs (no schema change).**
  - The LG table gains an `Unresolved` column (whiffs no beam matched), so
    the LG miss classes visibly sum to shots − hits instead of silently
    falling short.
  - The crosshair-density hover now maps cursor position through the
    rendered/intrinsic size ratio, and the image keeps its aspect ratio
    when a narrow panel shrinks it — previously the reported bin (and its
    shot/hit counts) drifted and the bitmap distorted horizontally.
  - The schema docs for `crosshair.dyaw` had the sign convention backwards
    ("right positive"); positive is enemy-left (Quake yaw grows
    counterclockwise). The web's plotting flip was already correct; the
    docs now match it. SSG crosshair samples remain deliberately
    unrendered (SG + LG panels only).
- **mvd-api: deterministic /shots + /aim responses; panic-proof demo
  loading (no schema change).**
  - The shot-stream rebuild now builds projectiles, beams and nails in a
    single variant. Previously nails were a separate server-side latch:
    after any client passed `?nails=1`, plain `GET /shots` and `GET /aim`
    served nail-linked bodies under the same immutable ETag — and reverted
    after an LRU eviction. Responses are now a pure function of the URL.
    The `/shots` `nails` parameter is deprecated (accepted and ignored);
    ng/sng fires were always included, and their flight-linking + accuracy
    now always is too.
  - A panicking demo parse no longer releases concurrent requests for the
    same demo with a nil result and nil error (a cascade of confusing
    nil-dereference 500s); all callers now receive a real error and the
    panic stack is logged.
- **Aim/shots correctness batch (schema v49; value fixes, no shape
  change).** Four fixes from the deferred aim/shots review:
  - The Aim tab's RL/GL direct/splash/missed split now appears on every
    default parse. It was accidentally gated on the opt-in
    `streams.projectiles` payload, which is emission-only — the projectile
    linking the split actually needs runs on every parse, so the block was
    absent everywhere except `-include projectiles` runs.
  - Warmup and post-match damage no longer leaks into those splits: the
    damage records feeding aim's pellet and direct counters are windowed
    to match time, so a warmup direct rocket no longer inflates `direct`
    and deflates `splash`.
  - Duels where both players share a colour team (e.g. both "green") no
    longer classify every hit on the opponent as team damage. Damage is
    classified duel-aware at birth, restoring airgibs, the aim enemy
    splits, the damage matrix, `victimWep` and the EWep buckets on such
    demos — all previously empty or folded into `givenTeam`.
  - Shots player identity uses the canonical slot resolver, backfilling
    a missing team from the demoinfo name table like damage/frags do.
  - A player who reconnects after the match ends no longer shows 0 frags
    on the corrected scoreboard: the server re-initializes the returning
    slot with `svc_updatefrags 0` during intermission, which clobbered the
    tracked final score (v48's removal of the 0-frag filter had surfaced
    these corrupted zeros — previously the same players were silently
    missing from `match.players` altogether). Frag tracking now freezes at
    match end; mid-match reconnects were already safe because KTX
    re-asserts the restored count itself.
  - Doc drift cleanup: the LG fire signal is `TE_LIGHTNING2` beams
    (`source: "beam"`), not the never-shipped "ammo" detection; rl/gl
    fires are linked, not "left unlinked"; `Dist` is muzzle-based.
  - The structural invariant test now covers every event-carrying result
    section (shots, damage, messages, frags, pickups, backpacks, item
    phases, aim) — not just `timelineAnalysis`.

## 2026-07-05

- **Web: chat shows every authentic message (no schema change).** The chat
  panel silently dropped any message whose exact text repeated within three
  seconds. The wire-level duplication that filter targeted (KTX sprints each
  say once per recipient at one wire timestamp) is already collapsed upstream
  by the analyzer's exact-key dedupe, so the web filter was only swallowing
  authentic repeats — a re-sent bind, two identical obituaries in quick
  succession. Removed; only the match-window clip remains.
- **Single obituary parser; timeline frag weapon codes corrected (no schema
  change).** The obituary/suicide pattern table lived twice — once for the
  frag log (`frags`), once for the timeline message stream
  (`messages.events[type=frag]`) — and the two had drifted. The timeline copy
  is now generated from the same table the frag log uses, so for the affected
  obituary lines the timeline stream's `weapon` now matches `frags[]` exactly.
  Consumer-visible corrections in `messages.events` (the frag log was already
  correct):
  - drowning self-kills now carry weapon `water` (was `drown`), matching the
    documented FragEntry vocabulary;
  - the unknown-cause self-kill "X somehow becomes bored with life" is now
    `suicide` (was mislabelled `rl` by the shorter-substring fallthrough);
  - CRMod obituaries ("blown to chunks", "shish-kebabed", "disembowled",
    "gets intimate", "warm fuzzy feeling") and KTX `k_spawnicide` self-kills
    ("shiny spawn point", "baby factory", "poor life choices") now produce a
    timeline frag event instead of being dropped or mislabelled.
  These lines are rare (env/CRMod/spawnicide), so the golden corpus output is
  byte-identical; the fix is verified by unit tests. Internal cleanup in the
  same change: one canonical slot→identity resolver (`ResolveSlotAt`) replaces
  five drifted per-analyzer copies, and one `parseInfoString` helper replaces
  three duplicated serverinfo walkers.

- **One entity-delta wire-layout implementation; FTE parse fixes (no
  schema change).** The parser's entity-baseline and entity-delta
  layouts each had a decode copy and a separate byte-skip copy that had
  drifted apart; they are now a single reader each (`readBaselineBody`
  for the baseline body, `readDeltaBits` + `readEntityDelta` for the
  delta), and `svc_spawnstatic` / `svc_fte_spawnstatic2` decode through
  that same code and discard rather than re-skipping by hand. Three
  FTE-only divergences between the old copies are fixed by construction
  to match ezquake (`cl_ents.c`) and mvdsv (`sv_ents.c`): the FTE
  "evenmorebits" byte is read only when an FTE extension was negotiated
  (the `svc_fte_spawnbaseline2` / `svc_fte_spawnstatic2` path previously
  read it unconditionally, misaligning a non-FTE stream), and the
  transparency / colour-mod fields are gated on the negotiated
  `FTE_PEXT_TRANS` / `FTE_PEXT_COLOURMOD` (previously consumed on the
  flag bit alone). Only demos that negotiated FTE entity extensions are
  affected — none exist in the golden corpus, which stays byte-identical,
  so the schema is unchanged (v48).

- **Dev-CLI correctness fixes (no schema change).**
  - `qw-analyze -view state-at -time 0` (and `-time 0s`) now queries match
    start instead of erroring with "requires -time"; flag presence is
    tracked separately from the zero value.
  - `mapgen` now fails the run when its `-demos` directory can't be walked
    (e.g. a typo'd path) instead of silently emitting an unpruned corpus,
    matching the existing `-bsp-dir` behaviour.

- **mvd-api / mvd-mcp hardening for hosted use (no schema change; REST
  surface semantics + one MCP tool-schema change).** Prerequisites for
  running the API/MCP on the internet for third-party apps:
  - **Downloaded demo bytes are verified against their SHA before
    caching.** A cold download now hashes the `.mvd.gz` bytes and rejects
    them as `hub_upstream` (502) if they don't match the hub-claimed
    `demo_sha256` that keys the cache, the `sha:` address, and the ETag —
    a corrupted CDN object can no longer poison the cache permanently.
  - **Hub "not found" is classified by error identity, not message
    text.** A new `hubfetch.ErrNotFound` sentinel + `errors.Is` replaces
    two `strings.Contains(err, "not found")` checks, so a hub 5xx whose
    body merely contains "not found" is now correctly a 502 (was
    misreported as `demo_not_found` 404).
  - **Model-supplied `demoId` is validated + escaped in the MCP proxy.**
    All demo-scoped tool calls route through a `demoPath` helper that
    rejects anything but a canonical `gameId:N` / `sha:HEX` and
    PathEscapes it, so an id containing `/`, `?`, or `#` can no longer
    reroute a request to a different endpoint.
  - **Per-demo lazy computes no longer serialize globally.** `/los` and
    the on-demand shot-stream rebuild (`/shots`, `/aim`, `/streams/*`)
    now lock per demo SHA (shared `KeyedMutex` helper) instead of one
    server-wide mutex each, so a request for demo B does not queue behind
    demo A's multi-second pass. (Disk persistence of the computed
    artifacts is deliberately out of scope here.)
  - **Cache correctness nits.** A resolved-`GameInfo` map entry that
    previously leaked for process life when a demo was served from a
    warm tier is now drained unconditionally; `GetResult` documents that
    a cold fetch runs its hub download + parse to completion regardless
    of the caller's context (the singleflight shares one computation, so
    the first caller's cancellation must not poison the others).
  - **The stream-enriched endpoints signal a degrade instead of serving
    silently-incomplete data.** When the tier-1 MVD bytes are missing so
    the opt-in weapon-fire streams can't be rebuilt, `/shots`, `/aim`,
    and `/streams/*` now set `X-Shot-Streams: unavailable` (+
    `Cache-Control: no-store`) rather than quietly returning the lean
    result.
  - **MCP tool-schema change:** `getBackpacks`'s `weapon` input is now a
    `string[]` set (forwarded as CSV), matching REST `/backpacks` since
    v36 (was a single string); `getEvents`'s `types` schema now lists the
    opt-in `damage` / `telefrag` / `stomp` kinds.
  - **Docs:** API.md's `/overview` example is corrected to int32
    milliseconds (the code has emitted ms since v24) with an added §2.1
    units row; the §2.4 error table gains `shots_unavailable` /
    `aim_unavailable`; and stale `schemaVersion: 36` examples are updated
    to 48.

- **Clean end-of-demo, and truncated demos become visible (no schema
  change).** The standard MVD termination — `svc_disconnect
  "EndOfDemo"` — now surfaces through `events.Source.Next` as `io.EOF`
  (the value the Source contract always promised) instead of a hard
  error the analytics registry silently swallowed; any tail events in
  that final message are drained before EOF. A *non*-EOF `Next` error
  (a truncated or corrupt stream) now records `"event stream aborted:
  …"` into `result.errors`, and a failed region-control pass records
  `"region control: …"` there too, so a partial parse is
  distinguishable from a clean one. Previously-ignored read/skip errors
  inside the parser (sound/baseline/download skips, serverdata movevars,
  entity-diff emits) now propagate instead of silently misaligning the
  decode cursor, and a truncated known command is reported as
  `parse_error` naming the command rather than `unknown_svc`. No value
  changes on well-formed demos — the golden corpus is byte-identical
  (its `EndOfDemo` message carries no tail events); the new `errors`
  entries appear only on broken demos, so the schema version is
  unchanged (v48).

- **Timeline, scoreboard and duel-detection correctness fixes (schema
  v48).** Five bugs in already-emitted values, no field-shape change:
  - **Kill events were on the wrong clock and mislabeled in duels.**
    `timelineAnalysis.killEvents` shipped demo-relative (each kill
    ~`demoOffset`, typically ~10 s, later than the matching
    `deathEvents`) and, in 1v1s, tagged with a raw colour team like
    `"red"` instead of the player name — the two post-processors that
    rebase time and rewrite duel teams both skipped it. The Timeline
    per-player drill-down plots `killEvents − deathEvents` on one axis,
    so every kill drew displaced and duel team colours were wrong. Now
    `killEvents` is match-relative and duel-team-labelled exactly like
    its sibling streams. A new structural invariant test over the golden
    corpus enforces this for *every* timeline event stream, so a future
    field that forgets a post-processor fails loudly instead of shipping
    wrong.
  - **A chat line could start or end the match.** Match-start/-end
    detection scanned every print for substrings like "go!" / "game
    over", including player chat: a pre-match "go go go!" started
    recording warmup, a mid-match "gg game over" froze every stat and
    stream for the rest of the demo. Chat prints (level 3) are now
    ignored for match timing; only broadcast prints move the match
    window. The obituary parser likewise rejects chat-level prints, so a
    say containing " rides " can't inject a phantom frag.
  - **CRMod super-shotgun kills were mislabeled.** "X eats 2 scoops of
    Y's lead shot" was matched by the generic grenade " eats " pattern
    first, so the kill came out as weapon `gl` with a phantom killer
    "2 scoops of Y". It is now correctly `ssg` with killer Y.
  - **0-frag players no longer vanish; team games aren't mistaken for
    duels.** A player who legitimately finished on exactly 0 frags was
    dropped from `match.players`/`match.teams`. That is fixed
    (surface-authoritative-data), and duel detection now trusts
    `demoInfo.players` as authoritative — so a 2on2 in which two players
    end on 0 frags is no longer misread as a 1v1 and team-renamed.
    To keep actual spectators from leaking in once the 0-frag filter is
    gone, the reader now parses the server-set `*spectator` userinfo
    star key (mvdsv strips the client's bare `spectator` key before
    broadcast, so the old bare-key check never matched in MVDs) and
    recomputes the flag on every full userinfo update the way ezquake
    does, so a slot reused after a spectator disconnects doesn't
    inherit a stale flag.
  - **Consistent powerup interval end times.** On a demo cut before
    intermission, quad/pent/ring runs were closed at each player's last
    position sample while weapon intervals closed at the global last
    sample; both now use one shared effective match end.

- **Crosshair placement plots in true Quake units.** The Aim Stats
  density images and yaw/pitch marginals plotted hull-normalized error
  (each axis divided by the target's angular half-extent), so one x
  unit was ~16-23 qu while one y unit was 28 qu, and the "hull" box
  drew as a square. Both now plot the offset in Quake units at the
  target's range (derived from the existing `dyaw`/`dpitch`/`dist`
  columns — no schema change), the axes share one scale, and the
  solid outlined box is the player's collision box true to shape (32
  wide × 56 tall), with a dashed outline at its corner-on silhouette
  (~45 qu wide — the axis-aligned box reads √2 wider viewed
  diagonally). Extents ±96 qu × ±64 qu, ticks every hull-width (32
  qu), hover read-outs and histogram bins in qu.

- **LG whiff split reclassified: miss / blocked / far (schema v47).**
  The split classified from the beam endpoint only, so every beam that
  stopped on geometry short of its ~600u max range counted as `blocked`
  — including shafts fired into a wall with nobody behind them, which
  made Blocked % implausibly high. Now a whiff only counts as blocked
  or out of range when the shooter was on target: `blocked` = the beam
  stopped short on geometry and its extension to full range crosses a
  live enemy's collision hull (in range — the obstruction denied a
  would-be hit); `outOfRange` = the beam ran its full length and its
  extension to infinity crosses a live enemy's hull (denied by reach);
  `miss` = every other whiff, a plain aim error with no enemy on the
  beam's line (shares the `miss` field with the SG/SSG pellet split). The
  endpoint-proximity `nearMiss` field is **removed** — with blocked
  detection on the beam line, the near/wide distinction among aim
  errors carried no signal. The Aim Stats LG table now shows Miss /
  Blocked / Far (+ share-of-fires %) with reworded tooltips. Only
  beam-enriched parses are affected (the split needs `streams.beams`),
  so the golden corpus is untouched.

## 2026-07-04

- **Timeline frag/death events no longer dropped for players with no
  resolvable team.** `timelineAnalysis.fragEvents` and `.deathEvents`
  gated on a resolvable team, so a duel player whose userinfo *and*
  KTX demoinfo team are both empty lost every entry: gameId 224758
  (iddQd, 34 deaths missing from the frags/deaths drill-down) and the
  bravado golden (speedball, 7 deaths). FragEvents were partially
  papered over by the duel post-processor's frogbot synthesis (rebuilt
  from obituaries); DeathEvents had no such fallback. Both exports now
  gate on a resolvable *name* — matching `killEvents`' documented
  rationale — with `team` best-effort (`""` when unresolvable,
  rewritten to the player's own name by duel normalization in 1v1s).
  Audited the remaining team-keyed consumers for the same class of
  loss: weapon pickups / items / messages fallback-fill instead of
  dropping, and aim, airgibs, loc graph, and region control are
  (re)computed after duel normalization, so they were already correct.

- **Frag streaks now include the opening life.** A player already alive
  when the match begins has no spawn recorded for that first life (the
  parser's initial SpawnEvent fires during warmup and is dropped as
  pre-match), so the spawn-to-death run from match start to first death
  never entered `timelineAnalysis.fragStreaks` — a player who never
  died had no streak at all (gameId 224758: reload's 33-frag run was
  missing). The streak detector now synthesizes a match-start spawn
  when a death or credited frag predates a player's first recorded
  spawn — strictly after match start, since KTX's match-start reset can
  surface as a death at exactly `StartTime` (gameId 212260) and must
  not shift the spawn/death pairing. Opening runs read `time: 0`
  (match-relative).

- **Weapon-stay pickup recovery (schema v46).** In weapon-stay modes
  (serverinfo `deathmatch` 2/3/5 or `coop` — dmm3, the standard
  duel/2on2 mode, included) KTX never emits `//ktx took` for weapons
  and the weapon entity never leaves the wire, so `result.weaponPickups`
  contained **zero world weapon pickups** on those demos and weapon
  `items` timelines never closed a phase. Both analyzers now synthesize
  the pickups from STAT_ITEMS weapon-bit 0→1 transitions: weapon_pickups
  records kind-level entries (`inferred: true`; `source: "world"` when
  the picker was in touch range of a matching pad during the stat-lag
  window, else the new `"unknown"` — typically a non-RL/LG backpack
  grant), and items.go closes the matched entity's phase as a
  zero-length unavailability (`takenAt == respawnAt`; the weapon never
  left the map). Spawn-loadout bursts and `//ktx bp` grants are
  deduplicated. Verified against KTX's own per-player counters
  (`TookWeaponHandler` increments before the weapon-stay early return,
  so `demoInfo.players[].weapons[].pickups.*` were always correct).

- **Pickups tab: KTX counters are the displayed numbers.** The
  weapon/item verify cells now show the KTX-authoritative counter as
  the cell value and acknowledge the analytics-derived count in the
  tooltip, instead of showing the analytics count and flagging
  divergence red. Rationale: in weapon-stay demos the analytics
  reconstruction is known-imperfect (wire-invisible grab-then-die
  coalescing, pad-vs-pack ambiguity), so a small divergence is
  expected, not an error — while the analytics stream stays the right
  source for timestamped/per-entity questions (the per-entity `@ loc`
  columns are unchanged). Demos without KTX pickup counters (old /
  non-KTX servers) fall back to the analytics counts, trusted as-is.

- **Duel team normalization now covers pickup/shot data (v46).** In 1v1
  demos `normalizeDuelTeams` rewrites every player's team to their own
  name, but `items` phase teams, `weaponPickups` team/dropperTeam,
  `backpacks` team, `shots` stream/byPlayer teams (feeding `aim` teams),
  and `airgibs` attacker/victim teams kept the raw pre-normalization
  strings — so the Pickups tab's per-team aggregation bucketed duel
  pickups under stale colour labels and showed zero rows. All are now
  rewritten in the duel pass (airgibs sources teams from the normalized
  player streams). The pass also reclassifies `shots[].victimKinds`
  `"team"` → `"enemy"` and folds the per-weapon `teamHits` bucket into
  `enemyHits` (v45's `victimKindOf` compares raw team strings, so a duel
  where both players share a colour team classified every opponent hit
  as team damage; in a 1v1 any non-self victim is an enemy — exact, since
  the single opponent pair classifies uniformly). Aim's enemy/team splits
  inherit the correction via `aimPost` ordering. The web Pickups tab's
  per-team tables also join the existing duel-mode hide
  (`team-aggregate-table`), matching the other per-team stats tables.

- **Pickup attribution: touch-instant sampling + measured 128 u gate
  (v46).** The Layer-4 distance corroborator now samples each player's
  position from the per-frame history at the entity-removal instant
  (which is the touch frame) instead of a latest-only sample up to
  250 ms stale, and all proximity consumers (corroborator, insta-regrab
  picker, weapon-stay classifiers) share one 128 u touch gate — genuine
  touches measure 54-104 u across the corpus, non-touch same-room grabs
  ≥150 u. A handful of beyond-gate guesses become honestly unattributed.

- **Shots/aim: enemy / team / self victim classification + Aim Stats
  Victims cycle** (schema v45). Every linked victim is now classified
  relative to the shooter — `enemy`, `team` (same non-empty team, not
  self) or `self` (own wire slot: rl/gl self-splash, i.e. rocket jumps) —
  mirroring the damage layer's `isSelf`/`isTeam` semantics, per victim
  per fire (one rocket can splash an enemy, a teammate and the shooter at
  once and counts in every bucket it has a victim in). `Shot` gains
  `victimKinds`, `WeaponShots` gains `enemyHits`/`teamHits`/`selfHits`,
  the aim crosshair/ramp samples gain a `team` column, and `WeaponAim`
  gains `enemy`/`team`/`self` counter slices (`WeaponAimSplit`: hits,
  pellet splits, direct — see RESULT_SCHEMA.md for the emission rules).
  All additive; `hits`/`accuracy` stay all-victims for KTX scoreboard
  parity. The Aim Stats tab gains a **Victims** filter
  (All / Enemy / Team / Self) that slices the weapon tables, the
  crosshair heatmaps + marginals and the LG ramp; **All** (default)
  preserves the previous numbers, and **Enemy** is the first view where
  rocket jumps no longer inflate RL hit % (they were always counted as
  hits — now visible under Self). Duels hide the Team option (no
  teammates); Self shows tables only (hitscan cannot self-hit, so there
  are no self crosshair samples). The MCP `getAim` tool and `/aim` +
  `/shots` endpoints carry the new fields automatically. Tab layout
  reworked alongside: the Victims strip sits at the top of the tab, the
  LG and SG crosshair blocks sit side by side where the pane is wide
  enough (stacking on narrow panes), and the player picker moved into
  the Crosshair placement panel — the only place it applies.

- **Aim: hits attribute to the confirmed victim** (schema v44). The v43
  liveness gate excluded the victim of a killing blow from attribution —
  the kill lands in the same frame the victim dies, so `losAliveAt` read
  the victim as already dead at the fire time. In team games the sample
  went to the nearest *other* live enemy, producing impossible
  "hits" tens of hull-widths off target (the big far-edge bars with
  nonzero hit counts in the Aim Stats marginal histograms — verified on
  hub demo 223930: 78 of 79 LG edge-bin hits were killing blows, the 79th
  a team-damage hit, while the beams ended inside the actual victim's
  hull); duels dropped their killing-blow samples entirely. Crosshair
  samples of hit shots now attribute to the server-confirmed victim
  (nearest by crosshair error when a pellet fire hit several), with no
  liveness gate and no enemy filter (team damage is a confirmed target —
  `tgt` can then name a teammate); misses keep the live-enemy
  nearest-crosshair heuristic. Duels gain one crosshair sample per
  hitscan kill; the web attribution note now spells out the hit/miss
  split.

- **API: `/shots` endpoint + complete `/aim`; MCP: `getAim`** (no schema
  change). New `GET /v1/demos/{id}/shots` serves the per-fire weapon stream
  (`result.Shots`: linked hits/victims, per-player aggregates, KTX
  reconciliation; `nails=1` opts into ng/sng fires). `/aim` and `/shots` are
  served from the stream-enriched parse (`EnsureShotStreams` re-parses on
  first request, then caches — the rebuilt `Shots`/`Aim` blocks are grafted
  onto the cached result), so the stream-derived aim blocks (RL/GL
  direct/splash, the LG near/blocked/out-of-range split) are now always
  present over the API instead of silently absent. New
  `GET /v1/demos/{id}/airgibs` serves the Key Moments airgib list
  (`timelineAnalysis.airgibs` — the last Result block with no endpoint);
  empty, not an error, on maps without a provisioned BSP. The MCP server
  adds a `getAim` tool (aim stats only — the raw per-fire stream stays
  API/JSON-only by design).

- **Wider BSP corpus + phantom map-alias fix.** `scripts/fetch-bsps.sh`
  now provisions the most-played 1on1/2on2 community maps from a
  hub.quakeworld.nu sample (ztndm3, metron, toxicity, dad2, catalyst,
  nova, pocket, katt, shifter, spinev2, zeal), so loc attribution,
  map geometry and the BSP-gated visibility metrics light up on them.
  Removed the `phantombase → phantoma` map alias in
  `mvd-analytics/loc/loader.go`: the phantom family (`phantom` /
  `phantoma` / `phantombase`) are distinct map versions and now each
  resolve to their own loc/entity/geometry data. Previously the alias
  routed the ~1000-game `phantombase` corpus through the 23-game
  `phantoma` data; this touched every `NormalizeMapName` consumer — loc,
  mapents, mapbsp, and the `/v1/maps/{map}` endpoint. Only the dominant
  `phantombase` BSP is fetched; the low-play predecessors are not.

## 2026-07-03

- **Aim: alive-gated attribution + density image, marginal histograms,
  share-of-fires columns** (schema v43).
  Crosshair-error target attribution now skips enemies who are dead at the
  fire time (same liveness rule as line of sight, `losAliveAt` over the
  spawn/death streams). Dead players keep streaming position samples — the
  death-anim body — so a corpse sitting near the crosshair could previously
  win nearest-crosshair attribution in team games and log a guaranteed-miss
  sample; a duel fire while the lone enemy is dead now emits no sample at
  all (the per-weapon fire counts still include it). No field changes —
  sample counts and targets shift on team demos. The web Aim Stats tab
  replaces the crosshair grid with a **smoothed density image** per weapon
  (LG and SG): a Gaussian-smoothed 2-D histogram on canvas (the shared
  viridis ramp anchored to the page background at zero, like the table
  heatmaps; no external deps) with hull box + dead-center overlays, axis
  ticks and a colorbar in shots per bin; hover reads exact shot/hit counts.
  Under each image, two **marginal histograms**: the same normalized
  samples projected onto one axis at a time — yaw (enemy left ↔ right) and
  pitch (enemy below ↔ above) — zero-centered bins, with the |n| ≤ 1
  on-hull band shaded and a dead-center rule. Image and histograms share
  the same extents (yaw ±6, pitch ±4); samples outside them are dropped
  from the image (a clamp pile-up would paint a bright rim) but stay
  visible in the histograms' clamp edge bins. The LG ramp panel is folded
  into the LG block as a third histogram in the same style (hit % by time
  since the shaft opened, hover for per-bin counts; `lgRamp` in the
  schema is unchanged), and the histograms stack vertically. All binning
  stays client-side. The per-weapon accuracy
  tables add **share-of-fires % columns** next to every count (LG
  near/blocked/far, RL/GL direct/splash/missed, SG/SSG full/partial/miss)
  so players with different shot volumes compare directly.

## 2026-06-28

- **Aim analytics** (schema v41–v42). A new top-level `aim` block: per-player aim
  metrics derived as a post-processor from `shots` + `streams`
  (position/view interpolated at fire time) + `damage` + LG `beams`. Columnar
  per-shot **crosshair-error samples** for hitscan (sg/ssg/lg) — both signed
  degrees off the enemy and a version normalized by the target's angular size
  (range-comparable; radius 1 ≈ the hitbox edge); an **LG ramp-onto-target**
  series (ms since shaft start + hit); **rocket direct/splash** counts; and an
  **LG reach/whiff** classification (near miss vs blocked vs unresolved).
  Target attribution is exact in duels (`mode: "duel"`) and a labeled
  nearest-crosshair-enemy heuristic in team games (`mode: "team"`). Computed
  by default for every client (CLI / API / web) — the crosshair + ramp blocks
  always; the rocket + reach blocks when the projectile/beam streams were
  built (the WASM map build, `qw-analyze -include projectiles,beams`). The web
  UI adds an experimental **Aim Stats** tab that renders the block: a rich
  per-weapon table (SG/SSG pellets hit/fired + full/partial/whiff fires, RL/GL
  direct/splash/missed, LG near-aim/blocked whiffs — the pellet and direct
  figures match the server's authoritative stats), a hitscan
  crosshair-placement heatmap (shot density, normalized so ±1 = the hull edge),
  and an LG ramp chart. Also adds a reusable
  `result.PositionTrack.SampleAt` interpolating
  sampler (position + shortest-arc view angle + velocity) other position-
  derived analytics can adopt. The web table is one table per weapon with
  players on the rows (team-coloured like the Summary tab), and the heatmap is
  split into LG and SG. Shots gained `warmup` (v42) — fires outside the match;
  the aim analysis is match-time and excludes them, matching `shots.byPlayer`.

## 2026-06-27

- **Weapon-fire map overlay** (schema v40). Two opt-in spatial streams under
  `streams` for the 3D map view: `projectiles` (every tracked rocket/grenade
  flight as a spawn→despawn segment + times) and `beams` (every LG
  `TE_LIGHTNING2` bolt as a muzzle→impact segment + time), both columnar.
  They are **off by default** — sizeable in a team game (thousands of beams)
  — and built only on request: `qw-analyze -include projectiles,beams`, and
  the WASM map build (where the result stays in browser memory, so no extra
  download). The map renders rockets/grenades as moving dots and LG bolts as
  brief beams at the playback cursor (`drawProjectiles` / `drawBeams`). The
  REST API serves them as three independent, build-on-demand endpoints —
  `GET /v1/demos/{id}/streams/{projectiles|beams|nails}` — re-parsing the
  cached demo on the first request and latching the result (like `/los`).

- **Nail (ng/sng) tracking** (schema v40, opt-in). A separate, highest-volume
  request (`qw-analyze -include nails`) that decodes nails — spike packet
  entities on `sv_nailhack` servers (the common case), or the `svc_nails` /
  `svc_nails2` stream otherwise — brackets each nail's flight, links ng/sng
  fires to their nail damage (`hit`/`victims`, approximate: per-fire linking
  credits one of SNG's two nails), and emits a `streams.nails` map overlay.
  Off everywhere by default — including the web map — so nails are never
  downloaded unless explicitly requested. Per-player nail hit counts track
  KTX's within a small margin across the corpus.

- **Per-shot weapon-fire stream** (schema v39). New top-level `shots`
  result: who fired what weapon, at exactly what match-relative ms — the
  foundation for accuracy metrics (including over short intervals) and for
  external analysis correlating crosshair/aim movement with when shots were
  taken (join a shot's `time` against `streams.players[].pos` view
  angles/velocity).
  - **Detection.** SG/SSG/RL/GL/NG/SNG fires come from `svc_sound` on the
    shooter's `CHAN_WEAPON` — the sound carries the firing entity, so
    attribution is exact and works on **any** QW server (not just KTX), and
    the distinct fire wavs disambiguate RL vs GL where ammo deltas cannot.
    (The Quake sound filenames are historically mismatched: the rocket
    launcher fires `sgun1.wav`, the nailgun fires `rocket1i.wav`.) LG has no
    per-shot fire sound, so it is counted from its `TE_LIGHTNING2` beam —
    emitted once per fire tick and carrying the firing entity directly
    (`source:"beam"`). One beam == one LG attack == one cell, so LG counts
    match KTX `acc.attacks` exactly. The beam decode also surfaces the
    muzzle→impact geometry as `BeamEvent` (for map rendering).
  - **Truthful cross-linking.** Instantaneous hitscan fires (sg/ssg/lg) are
    linked to the damage they caused in the **same server frame** via the
    KTX `mvdhidden_dmgdone` stream (`hit`/`victims`). Rocket/grenade fires
    (rl/gl) are linked by **entity flight tracking**: the projectile entity
    brackets the flight (`spawn → despawn`), so a fire is matched to its
    launch frame (by muzzle) and its impact damage to the shooter's
    same-weapon damage at the despawn frame — which disambiguates *which*
    fire caused *which* impact when several rockets are in flight (a naive
    "next damage" link cannot). Across the corpus, rl/gl connect-counts
    match KTX's authoritative `real` hit counts to within one. Nail fires
    (ng/sng) ride a separate stream and stay unlinked for now.
  - New parser events `ProjectileSpawnEvent` / `ProjectileDespawnEvent`
    track rocket (`progs/missile.mdl`) and grenade (`progs/grenade.mdl`)
    entities by their recycled entity number.
  - **Validation built in.** A `reconciliation` block cross-checks detected
    counts against KTX's authoritative `acc.attacks`; across the golden
    corpus the converted `streamAttacks` matches KTX exactly (a 4on4 game
    reconciles 42/42 player×weapon rows), with LG occasionally off by a
    single cell at a death/discharge boundary.
  - New parser events: `svc_sound` is decoded into `SoundEvent` and
    `svc_soundlist` is captured to resolve fired sounds to weapons.

- **Line-of-sight & potential-visibility metrics** (schema v37–v38,
  [#94](https://github.com/galfthan/mvd_analyzer/pull/94)). Two new
  per-opponent visibility tracks on `PlayerStream`, both computed lazily
  on first request (the heaviest position-derived pass, BSP-gated — only
  on maps with a provisioned BSP; absent from the default parse):
  - **`PlayerStream.LOS`** (v37) — geometric **line of sight**: intervals
    during which a player has a clear ray to an opponent (eye point at
    `origin + (0,0,22)`, nine rays against the BSP clip hull and moving
    movers, the opponent's bounding-box corners + centre). Directional, so
    asymmetric one-way sightlines are preserved; gated to live players
    (alive from match start, not the first recorded spawn).
  - **`PlayerStream.PVS`** (v38) — server-reproduced **potential
    visibility**: whether a live mvdsv would have sent that opponent's
    entity to this player's client at that frame, made wire-exact against
    `SV_PlayerVisibleToClient` (fat PVS of `origin + view_ofs` vs. the
    target's entity-leaf set, with the `MAX_ENT_LEAFS` overflow gate).
    PVS ⊇ LOS by construction; the **PVS-minus-LOS gap** is an
    occlusion-tolerant proximity/awareness signal, not a sightline.

  Surfaced three ways: the REST/MCP **`/v1/demos/{id}/los`** endpoint
  (returns both `los` and `pvs`), the CLI, and the **web map overlay** —
  two per-player toggles (**LOS**, **PVS**) that draw inter-player lines
  on the 3D map tab (PVS as thin faint lines beneath the thicker LOS
  lines), both filled by one lazy pass and cached client-side. Both
  metrics are `omitempty`/absent on non-BSP maps, so the schema bump is
  additive for existing consumers.

## 2026-06-20

- **API contract cleanup** (schema v36). Consolidates the REST/MCP
  surface; section-filtering logic moves into the shared
  `mvd-analytics/view` layer (REST, MCP, and WASM now share one tested
  implementation — no wire change from that move). The observable contract
  changes:

  **Breaking**
  - **`match.startTime` / `match.endTime` removed** from the result. They
    were always `0` / equal to `duration` and duplicated
    `streams.global.matchStart` / `matchEnd`. Read **`match.duration`** for
    match length and **`streams.global`** for the match window. (The
    `endTime` key disappears from the `match` object; `startTime` was
    already `omitempty`-absent.) Schema bumps **35 → 36**, so the ETag /
    `X-Schema-Version` change and cached results re-validate.
  - **`GET /v1/demos/{id}/map-entities` removed** (and the MCP
    `getMapEntities` tool). Use **`GET /v1/maps/{map}/entities`** (MCP
    `getMapEntitiesByMap`) — identical payload; get the map name from
    `/overview`.
  - **`view_error` (400) is gone** — every malformed/rejected query now
    returns **`invalid_param` (400)**, including an unknown `fields` code or
    reducer name.

  **Additive / non-breaking**
  - **`/region-control` accepts `from` / `to`** (match-relative seconds) to
    clip control attribution to a sub-window.
  - **`weapon` is a comma-separated set on every endpoint** that takes it
    (`/frags`, `/damage`, `/backpacks`, `/weapon-pickups`); `/backpacks`
    previously accepted only a single value.
  - **Query-parameter names are case-insensitive** (canonical spelling
    stays camelCase: `windowMs`, `minDwellMs`, `includeTeam`).
  - **Documented "section absent" rule**: capability-gated sections
    (`demoinfo`, `damage`, `frags`, `loc-graph`, `metadata`,
    `region-control`) return `422 <section>_unavailable`;
    always-computable / list sections (`items`, `backpacks`,
    `weapon-pickups`, `chat`) return `200` with an empty body.

- **3D map view & mover streams** (schema v34–v35,
  [#91](https://github.com/galfthan/mvd_analyzer/pull/91)). `streams.movers[]`
  carries the pose timeline of every tracked brush-model entity (lift, door,
  plat, train); map geometry gains version 4 (per-vertex 3D triangles +
  optional `walls` / `liquids` / `submodels`), and
  `timelineAnalysis.locationData` collapses to one medoid anchor point per
  loc name (v34) so map labels no longer duplicate. Drives the new
  orbit-camera 3D map tab over a usage-pruned committed corpus.

## 2026-06-14

- **Float32 positions, velocity & height** (schema v33,
  [#90](https://github.com/galfthan/mvd_analyzer/pull/90)). `pos.x/y/z`,
  `pos.vx/vy/vz` and `pos.h` change from `int32` to `float32`, so the
  wire-native sub-unit origin is no longer truncated to whole units and the
  derived velocity loses its ±1-unit quantization noise. Values stay native
  float32 in memory; JSON text is rounded to 3 decimals (lossless for
  eighth-unit coords). The `hgt` no-floor sentinel changes to `-1000000000`.

## 2026-06-13

- **Player view direction & velocity** (schema v31–v32). Every
  native-rate position sample now also carries **where the player is
  looking** — view pitch/yaw kept losslessly as the raw `angle16` wire
  value (`pos.vp`/`vya`, decode `uint16(v)*360/65536`) — and a derived
  per-sample **velocity** vector in units/sec (`pos.vx`/`vy`/`vz`) from a
  central-difference estimator that does not differentiate across
  respawns, map teleporters, or time gaps. The view-layer query API and
  CLI gain opt-in per-channel field codes: `pos` is now strictly x/y/z,
  with `view`, `hgt`, `lq`, and `vel` each requestable on their own
  (served by mvd-api `stream-slice` / `state-at` / `buckets`).

## 2026-06-12

- **Per-sample floor height, airgibs, movers, liquids** (schema v24–v30,
  [#84](https://github.com/galfthan/mvd_analyzer/pull/84)). Every
  position sample now carries the player's height above the floor
  (BSP clip-hull traces, footprint-aware, standing on lifts/doors at
  their demo-streamed poses) and a water/slime/lava submersion state.
  On top of it: the **airgibs** Key Moment — direct enemy rocket hits on
  airborne victims, with lethality, Hub shooter links, and the victim's
  height above the shooter as the headline "how spectacular" number.

## 2026-06-07

- **Wall-clock timing for demos** (schema v23,
  [#82](https://github.com/galfthan/mvd_analyzer/pull/82)). Recovers a
  real-world clock anchor for each demo so any match-relative time maps
  to wall-clock time; pause segments are accounted for in the mapping.
- **Demo-start timestamp decoding**
  ([#83](https://github.com/galfthan/mvd_analyzer/pull/83)). Parses the
  mvdhidden `0x000B` block (ULEB128 Unix-ms) — the millisecond-accurate
  demo-open anchor the wall-clock mapping builds on.

## 2026-06-03

- **Per-hit damage end to end** (schema v20,
  [#81](https://github.com/galfthan/mvd_analyzer/pull/81)). The KTX
  hidden damage stream becomes a full per-hit log with
  attacker→victim matrices, per-weapon aggregates, and EWep
  victim-weapon buckets; telefrags and stomps are surfaced separately.
- **Corrected scoreboard stats** (schema v19,
  [#80](https://github.com/galfthan/mvd_analyzer/pull/80)). Kills,
  deaths and suicides corrected from the frag log for every consumer;
  efficiency is kills-based to match hub.quakeworld.nu.

## 2026-06-02

- **Player timelines**
  ([#79](https://github.com/galfthan/mvd_analyzer/pull/79)). Per-player
  timeline view of vitals, weapons and powerups across the match.

## 2026-05-30

- **Static map-entity corpus + map endpoints** (schema v14,
  [#77](https://github.com/galfthan/mvd_analyzer/pull/77)). Item and
  spawn locations extracted from map BSPs ship embedded, with REST
  endpoints to serve per-map geometry and entities.

## 2026-05-29

- **Schema reference reconciled with code**
  ([#76](https://github.com/galfthan/mvd_analyzer/pull/76)).
  RESULT_SCHEMA.md brought back in lock-step with `result/` after
  drift; version history table became the single change record.

## 2026-05-25

- **Reconnect identity unified** (
  [#75](https://github.com/galfthan/mvd_analyzer/pull/75)). A player
  rejoining mid-match keeps one identity across slots; deaths and
  pickups reconcile against KTX's authoritative counters.

## 2026-05-24

- **Locs & Regions tab**
  ([#74](https://github.com/galfthan/mvd_analyzer/pull/74)).
  Combat-posture loc graphs (armed/unarmed movement between locs) plus
  sortable loc heatmap and region tables in the web UI.

## 2026-05-23

- **Column-major bucket format** (schema v11,
  [#72](https://github.com/galfthan/mvd_analyzer/pull/72)). Bucketed
  timelines ship as columnar arrays; the legacy HighResBucket shape is
  dropped.
- **Web load perf tuning**
  ([#70](https://github.com/galfthan/mvd_analyzer/pull/70)). Profiling,
  deferred bucket builds, and a faster `view.Buckets` cut initial load
  time.
- **Chat dedup on KTX demos**
  ([#68](https://github.com/galfthan/mvd_analyzer/pull/68)). Per-recipient
  copies of the same chat line collapse to one message.

## 2026-05-20

- **Visibility-aware loc attribution** (schema v9–v10,
  [#64](https://github.com/galfthan/mvd_analyzer/pull/64)). Loc
  resolution gains a BSP PVS veto (locvis V6) so positions no longer
  bleed through walls to the nearest loc point; death/spawn handling
  rebuilt on top.
- **API/MCP loc representation**
  ([#65](https://github.com/galfthan/mvd_analyzer/pull/65)). Views
  return loc names by default with an opt-in index mode; analyzer
  errors surface properly through the API.
- **MCP fixes**
  ([#67](https://github.com/galfthan/mvd_analyzer/pull/67)). Array tool
  outputs wrapped for spec compliance; `getItems` filter vocabulary
  corrected.

## 2026-05-16

- **All times become int32 milliseconds** (schema v8,
  [#62](https://github.com/galfthan/mvd_analyzer/pull/62)). Every
  timestamped field migrates from float seconds to the MVD wire
  format's native integer-ms unit, eliminating float drift at
  boundaries.
- **Region control as a normal view**
  ([#63](https://github.com/galfthan/mvd_analyzer/pull/63)). Region
  control re-derives from streams like every other view instead of
  being a parse-time special case.

## 2026-05-15

- **REST API + MCP server** (
  [#61](https://github.com/galfthan/mvd_analyzer/pull/61)). `mvd-api`
  serves analysis over HTTP with a demo cache, and an MCP server
  exposes the same views to AI tooling; repository reorganised into the
  three-module workspace.

## 2026-05-11

- **Streams as canonical storage** (schema v7,
  [#60](https://github.com/galfthan/mvd_analyzer/pull/60)). Per-player
  change streams, intervals, and the native-rate position track replace
  parse-time buckets as the single event-rate source all views derive
  from.

## 2026-05-09

- **Timeline GL/ammo, clean chat text, Go region control** (schema v6,
  [#59](https://github.com/galfthan/mvd_analyzer/pull/59)). Timeline
  gains grenade launcher and ammo tracking, chat messages get a
  markup-stripped `messageClean`, and region control moves from the
  frontend into the Go analyzer.

## 2026-05-08

- **Match in the header**
  ([#57](https://github.com/galfthan/mvd_analyzer/pull/57)). The web UI
  shows the loaded match in the header bar and tab title.
- **Timeline rendering rewrite**
  ([#56](https://github.com/galfthan/mvd_analyzer/pull/56)). Scanline
  rendering fixes resize artifacts and speeds the timeline up.

## 2026-05-07

- **Per-map regions from JSON** (
  [#55](https://github.com/galfthan/mvd_analyzer/pull/55)). Embedded
  per-map region definitions fully replace the auto-detection
  heuristic.

## 2026-05-03

- **Pickups tab**
  ([#54](https://github.com/galfthan/mvd_analyzer/pull/54)). Per-player
  item pickup breakdown in the web UI, with the KTX weapon-pickup
  counter semantics documented.

## 2026-05-02

- **Search tab**
  ([#53](https://github.com/galfthan/mvd_analyzer/pull/53)). Search for
  demos from the web UI, with a reshaped tab layout around it.
