# Plan: a canonical `playerStats` artifact

Status: proposed. Branch `plan-playerstats`, cut from `main` @ 53fb84a.

## 1. Why

Today the per-player statistics a consumer sees are either **KTX's**
(`/demoinfo`, verbatim pass-through of the embedded JSON block) or a
**hand-rolled merge re-implemented per consumer**. The web summary tab is
the worst case: `displayPlayerStats` (`mvd-web/static/app.js:1316`) reads
frags/kills/deaths from `match.players`, RL/LG kills from
`frags.byPlayer.byWeapon`, suicides from the frag log, and
damage/accuracy/pickups/ping/handicap/bot from `demoInfo` — a five-source
join written in JavaScript that the REST and MCP consumers do not get.
And when the demo has no KTX block at all, the summary tab collapses to
`displayScoreboardFallback` (`app.js:1605`): frags only, no weapon table,
no item table.

Three findings from reading `ktx/` (the authoritative source) sharpen what
this artifact has to do.

**(a) Weapon hold time is not in the demoinfo block on any demo.** KTX
tracks it internally — `ps.wpn[i].time`, opened on a pickup the player
didn't already have (`src/client.c:4583-4585`), closed on death
(`StatsHandler`, `src/client.c:4604`) — but `json_weap_detail`
(`src/stats_json.c:126-205`) emits only `acc` / `kills` / `deaths` /
`pickups` / `damage`. The number only ever reaches the end-of-match text
tables (`src/statsTables.c:390-395`). So "time with RL / LG" is
unavailable from `/demoinfo` on a 2026 demo exactly as much as on a 2015
one. This is not a backfill problem; it is a compute-it-ourselves problem.

**(b) Where KTX does have hold time (armor), our number is better.** The
armor clock opens on pickup (`src/items.c:505-506`) and closes only on
death or on picking up a *different* armor type (`src/items.c:511-522`,
`src/client.c:4600`). It is never closed when the armor is chewed to zero
by damage. KTX's "time with RA" therefore keeps counting after the RA is
gone. Our `PlayerStream.ArmorType` goes to `""` when the item bits clear,
so a stream-derived integral is exact. (Powerups are fine in KTX — the
quad/pent/ring clocks do close on expiry, `src/client.c:3823/3868/3920` —
but we compute them anyway for internal consistency with the timeline's
powerup runs.)

**(c) We already hold the raw material at native resolution.**
`PlayerStream.RL/LG/GL/SSG/SNG` are half-open possession intervals,
`ArmorType`/`Armor` are transition streams, `Quad/Pent/Ring` are
intervals (`mvd-analytics/result/streams.go:146-160`). Hold time is a
time-weighted integral over those — the exact-stats rule, no new parsing,
no bucket grid.

## 2. Decisions taken (do not re-litigate)

- **`/demoinfo` does not change.** It stays a verbatim pass-through of the
  KTX block: the audit trail, and the only way to diff our reconstruction
  against the server's own accounting. Nothing is merged into it, nothing
  is corrected in it. It is for consumers who want *exactly that*.
- **Wielded-weapon time is out of scope.** In QW players autoswitch to SG
  or axe to avoid dropping the good gun, so "time wielding the LG" measures
  a config, not a game. The part that matters — what got dropped and who
  picked it up — is already covered by `backpacks` / `weaponPickups`.
  `StatActiveWeapon` is decoded (`mvd-reader/parser/stats.go:230`) but stays
  unsurfaced.
- **Denominators are explicit.** Every hold figure ships with the windows
  it could be divided by; no implicit denominator.
- **Armor gets the complement.** Per-type hold time *and* time with no
  armor — a stat KTX structurally cannot produce.
- **The web summary tab moves onto `playerStats`.** The JS join dies; the
  Go artifact becomes the single input.

## 3. Shape

New Layer-2 section `result.PlayerStatsResult`, `resultKey` `playerStats`,
served at `GET /v1/demos/{id}/player-stats` and MCP `getPlayerStats`.
Computed for **every** demo — it never 422s on a missing KTX block; it
degrades by marking families `src: "derived"`.

```jsonc
{
  "players": [{
    "name": "...", "team": "...",
    "ping": 13, "handicap": 0, "login": "...", "bot": {...},   // KTX-only, omitted when absent

    "window": { "matchMs": 600000, "presentMs": 600000, "aliveMs": 511000, "deadMs": 89000 },

    "score":  { "frags": 23, "kills": 25, "deaths": 18, "suicides": 1,
                "teamKills": 0, "efficiency": 58.1, "src": "derived" },

    "damage": { "given": 4210, "givenTeam": 0, "givenSelf": 120,
                "enemyWeapons": 3900, "takenToDie": 210,
                "taken": 3980,          // ALL sources (ours)
                "takenEnemy": 3702,     // enemy-only (KTX dmg.taken) — distinct field, never coerced
                "src": "ktx" },

    "accuracy": { "rl": { "attacks": 88, "hits": 41, "real": 33, "virtual": 8 }, ...,
                  "src": "ktx" },

    "pickups": { "ra": { "took": 6 }, "mh": { "took": 4 },
                 "rl": { "took": 4, "dropped": 3, "xfer": 1 }, ...,
                 "src": "ktx" },

    "hold": {
      "weapons":  { "rl": { "ms": 412000, "runs": 9, "longestMs": 96000,
                            "shareAlive": 0.806, "shareMatch": 0.687 }, "lg": {...}, ... },
      "armor":    { "ra": {...}, "ya": {...}, "ga": {...}, "none": {...} },
      "powerups": { "quad": {...}, "pent": {...}, "ring": {...} },
      "src": "derived"
    }
  }],
  "teams": [ /* same block, summed; hold shares over the team's summed aliveMs */ ],
  "sources": { "score": "derived", "damage": "ktx", "accuracy": "ktx",
               "pickups": "ktx", "hold": "derived" }   // roll-up for a caller that wants one glance
}
```

`src` is **per stat family**, not per field and not per document — exactly
the precedent `DamageResult.BoundedSource` already sets
(`mvd-analytics/result/damage.go:65-77`, `view.applyKTXBoundedSummary`).
Values: `"ktx"` | `"derived"`.

### Window definitions

- `matchMs` — the match window (`GlobalStream.MatchEnd - MatchStart`),
  identical for every player. Game clock, so pauses are already excluded.
- `presentMs` — from the player's first spawn (or match start if already
  in) to their disconnect or match end. Distinguishes a late joiner from a
  player who was there and dead.
- `aliveMs` — Σ of alive intervals (spawn → death), clipped to the match
  window.
- `deadMs` — `presentMs - aliveMs`.
- `shareAlive = ms / aliveMs`, `shareMatch = ms / matchMs`. Both emitted;
  neither is "the" number. KTX's implicit denominator was alive time (its
  clocks close at death) — ours says so out loud.

### Hold-time derivation

- **Weapons** (`rl`, `lg`, `gl`, `ssg`, `sng`, `ng`): integrate
  `PlayerStream.<W>` intervals clipped to `[0, MatchEnd]` and intersected
  with the alive intervals. SG/axe are omitted — every player holds them
  for the whole match, so the column is noise.
- **Armor**: integrate `ArmorType` runs of `"ra"`/`"ya"`/`"ga"` over alive
  time. `none` is the alive-time complement, computed as
  `aliveMs - (ga + ya + ra)` and cross-checked against the `""` runs (they
  must agree; a mismatch is a bug, not a rounding artifact).
- **Powerups**: integrate `Quad`/`Pent`/`Ring` intervals. Must agree with
  the timeline's powerup runs (`Overview.TopPowerups`) — assert in tests.
- `runs` = number of disjoint possession spells; `longestMs` = longest one.

**Verify during implementation** (not assumed here): that possession
intervals genuinely close at death for every path — normal death, spawn
re-grant, disconnect mid-life — and never span a dead window. If any path
leaks, clip against the alive intervals rather than patching the stream.

**Weapon-stay modes** (`deathmatch` 2/3/5, coop): everyone holds
everything from spawn, so weapon hold time is ~100% for all players. That
is the truth about the mode, not a defect — report it, do not suppress it,
and note it in the docs.

## 4. Substitution rules

KTX wins **only where the definition is identical**. Where the semantics
differ, both are exposed under distinct names — never coerced into one
field. The `dmg.taken` trap is already documented at
`mvd-analytics/result/damage.go:142` and is precisely the mistake a naive
"best of both worlds" merge invites.

| family | source of truth | why |
|---|---|---|
| `score` (frags/kills/deaths/suicides/tk) | **always derived** | KTX over-counts pentagram-deflect telefrags / `dtTELE2` and resets after a reconnect; `match.players` + the corrected frag log are right. Already the web's behaviour (`app.js:1316` comment). |
| `damage.given` / `givenTeam` / `givenSelf` / `enemyWeapons` / `takenToDie` | KTX when present, else bounded reconstruction | server-side accounting we cannot fully see; reuses `view.applyKTXBoundedSummary`'s rules. |
| `damage.taken` | **always derived** (all sources) | KTX's `dmg.taken` is enemy-only. Emitted separately as `takenEnemy`. |
| `accuracy` | KTX when present, else the `shots` analyzer's derived accuracy | KTX counts pellets server-side (SG/SSG `attacks` is a pellet count); the derived figure is a wire-inferred approximation and must be marked as such. |
| `pickups` (took/dropped/xfer) | KTX when present, else derived from `items` + `weaponPickups` | direct server-side counters, semantics identical. `xferRL`/`xferLG` are KTX-only for now — no derived equivalent, omitted on old demos. |
| `hold.armor` / `hold.weapons` | **always derived** | KTX has no weapon time in the block at all, and its armor clock overcounts (§1b). Deviating from the server's own end-of-match table will surprise people — say why in RESULT_SCHEMA.md. |
| `hold.powerups` | **always derived** | KTX is correct here, but deriving keeps it consistent with the timeline's powerup runs. |
| `ping` / `handicap` / `login` / `bot` / `control` / `speed` | KTX-only | not on the wire; omitted on old demos. |

## 5. Work breakdown

### Phase 1 — the artifact, derived-only

1. `mvd-analytics/result/player_stats.go` — the structs above, schema bump
   `CurrentSchemaVersion` 59 → 60 (`result/result.go:745`), new
   `PlayerStats *PlayerStatsResult` field on `Result`.
2. `mvd-analytics/analyzer/player_stats.go` — a **post-processor** node
   (it consumes other artifacts, like `aimPost`), registered in
   `postNodeMeta` (`analyzer/dag.go:147+`) as
   `{name: "player-stats", resultKey: "playerStats", requires: ["clock",
   "roster", "timeline", "match:final", "frags:final", "damage", "shots",
   "items", "weaponPickups"]}`. Eager — the integrals are cheap.
3. Hold-time integrals + window computation + team aggregation.
4. `make artifacts-md`, golden regen
   (`go test ./mvd-analytics/analyzer/... -run TestGoldenCorpus -args -update-golden`).

### Phase 2 — KTX merge + serving

5. `view.PlayerStats(r, opts)` applying §4, stamping `src` per family.
6. `mvd-api`: `GET /v1/demos/{id}/player-stats`, router + handler +
   `artifacts.go` entry; openapi.yaml path, schema, and the artifact table
   row (~line 1193 — note it never 422s, unlike its neighbours).
7. `mvd-mcp`: `getPlayerStats` tool + the reachability test
   (`mvd-api/mcp_reachability_test.go:43`).

### Phase 3 — web summary tab

8. `displayPlayerStats` / `displayWeaponStatsTable` / `displayItemsTable`
   and their `*Teams` variants take `result.playerStats.players` /
   `.teams` instead of `demoInfo.players`. The five-source JS join is
   deleted — the merge now lives in Go.
9. `displayScoreboardFallback` is **deleted**: `playerStats` always
   exists, so a pre-KTX-block demo renders a full summary tab (the visible
   win of this whole change).
10. New columns: RL / LG hold %, armor hold, **time with no armor**.
11. `isDuel()` (`app.js:1392`) stops reading `demoInfo.players` — read
    `playerStats` rows (roster already stamps `team === name` for duels).
12. **Team-colour invariant**: `timelineState.teams` must stay the
    frag-sorted order (winner = index 0), now seeded from `playerStats`
    instead of `demoInfo.teams`. See CLAUDE.md "Team colors" — getting this
    wrong re-introduces the blue-in-summary/red-elsewhere bug.
13. Match-info header (`app.js:1050`) keeps reading `demoInfo` for
    map/date/mode/hostname with the existing `result.match` fallback —
    that is demo *metadata*, not player stats, and `metadata` already
    covers the fallback path.

### Phase 4 — optional, only if wanted

14. `?diagnostics=1` emitting a per-family `ktxDelta` (derived − KTX) so a
    caller can see disagreement without fetching `/demoinfo` and diffing
    by hand. Deliberately opt-in; not in the default body.

### Docs (same commits as the code, per CLAUDE.md)

`RESULT_SCHEMA.md` (authoritative — the full field table + the "why we
deviate from KTX on armor time" note), `mvd-analytics/README.md`
(analyzer registry), `ARTIFACTS.md` (regenerated, never hand-edited),
top-level `README.md` (section list), `mvd-api/API.md`
(endpoint-choice guide: when to use `/player-stats` vs `/demoinfo` vs
`/damage`), `openapi.yaml`, `mvd-web/README.md` (summary tab source),
`RELEASE_NOTES.md` (schema v60 entry).

## 6. Tests

- **Unit** (`analyzer/player_stats_test.go`): synthetic streams for each
  integral — interval open at match end, death mid-interval, late joiner,
  disconnect mid-life, weapon-stay mode, armor complement identity
  (`ga+ya+ra+none == aliveMs`), `runs`/`longestMs` edges.
- **Cross-artifact invariants**: powerup hold vs the timeline's powerup
  runs; `score` vs `match.players`; hold ≤ aliveMs ≤ presentMs ≤ matchMs.
- **View** (`view/player_stats_test.go`): provenance stamping under
  present/absent/partial KTX blocks; `taken` vs `takenEnemy` never
  collapsed.
- **API**: 200 on a demo with no KTX block (the regression this exists to
  prevent), ETag/`X-Schema-Version`, unknown-param 400.
- **Golden**: regenerated; the corpus spans both KTX-block and pre-block
  demos, so both provenance paths are pinned.
- `make test` before every commit.

## 7. Rough size

| area | code | tests | docs/generated |
|---|---|---|---|
| result schema | ~130 | — | — |
| analyzer | ~300 | ~280 | ARTIFACTS.md (gen) |
| view | ~140 | ~160 | — |
| api + mcp | ~120 | ~120 | ~260 (openapi) |
| web | ~200 net (−60 deleted) | — | — |
| docs | — | — | ~250 |
| **total** | **~890** | **~560** | **~510** |

Plus the golden regeneration diff, which is large but mechanical.

## 8. Open questions

1. Endpoint name: `/player-stats` (explicit) vs `/stats` (shorter, but
   "stats" is overloaded — KTX calls its own block stats too). Leaning
   `/player-stats`.
2. Do team aggregates belong in this artifact (`teams[]`, as drafted) or
   should the caller sum? Drafted as included — the web needs them and
   summing hold shares correctly (over summed alive time, not averaged
   shares) is exactly the kind of thing to do once in Go.
3. Should `accuracy` fall back to the derived `shots` figure at all, or
   just be absent on old demos? Fallback drafted in, marked
   `src: "derived"`; the risk is a caller comparing a wire-inferred
   accuracy against a KTX one across demos without reading `src`.
4. `xferRL`/`xferLG` have no derived equivalent today. Leave KTX-only, or
   derive from `backpacks` + `weaponPickups` in a later phase?
