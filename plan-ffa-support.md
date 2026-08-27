# Plan: matchless FFA support + a single home for mode logic

Status: **PR A and PR B landed** (schema v75, one unreleased entry) — §2,
§3, §5 and §6 are implemented; §4 all but item 5, which is **deliberately
not migrated** (see below). Branch `better-ffa-support`.

A four-reviewer pass over PR B changed four things worth recording here.
(a) The mode's "first word" over `TeamBased` now belongs only to a canonical
an explicit source NAMED: a roster-inferred duel was vetoing a live
`teamplay` cvar, publishing a CTF server's 1v1 as `teamBased: false,
sources.teamBased: "mode"`. (b) The countdown Mode row outranks the
serverinfo `mode` key, which names the last usermode COMMAND that ran
(`world.c:1482` over `commands.c:4848`) rather than the settings in force.
(c) `match` now publishes a `roster:final` artifact, because its `Finalize`
mutates the shared Roster and GameMode on a demo with no demoinfo block and
the DAG did not say so — `frag` read the pre-promotion verdict while the
nodes after `match` read the promoted one. (d) The individual team
relabelling reaches `demoInfo` too; leaving the clan tags there kept
`locGraph.byTeam` and several frontend surfaces keyed on labels nothing else
used.

PR B against what §3/§4 predicted: the descriptor is `result.GameMode`
(`result/gamemode.go`) resolved once in `analyzer/gamemode.go` from the
roster node, and published as `match.gameMode` + `CoreOutputs.GameMode`.
`Matchless` was dropped from the struct — nothing consumes it and
`streams.global.matchStartSignal` already answers the question. `Submodes`
is a sorted `[]string`, not a `map[string]bool`, so the JSON is stable.
The consumers all read ONE predicate, `CoreOutputs.IndividualMode()`
("does a team tag name a side here"), which is the roster's duel verdict
OR a descriptor verdict from a source that actually saw a mode or a cvar —
the weakest source (roster shape) is deliberately refused, since it is an
inference over the very tags the layout would discard. `Rounds` is
resolved and published; its consumers are still out of scope.

Two deviations worth naming. `mvd-api/overview.go`'s `mode` KEEPS its
inverted precedence and its display vocabulary — it is a label for a human
and clients have read it since v1 — and the branching answer moved to a new
`overview.gameMode` beside it, which is documented in both places. And the
spectate golden is `ffa_matchless_shifter_260119_spectate`, not the `tox`
recording §5 named: `tox` is a map with no BSP, loc or entity corpus on
this machine, so the golden harness refuses to regenerate it; shifter is
BSP-resolvable, is the biggest FFA in the set (11 players) and carries a
spectator JOINING mid-match plus a slot handover. The `tox` 18-frag case is
pinned by `TestMatchAnalyzer_MidMatchSpectateFreezesFrags`, which cites it.

Location: repo root beside `plan-archive-features.md`, linked from
`README.md`'s known-limitations list (CLAUDE.md: plan files must be
reachable from a README).

What PR A actually shipped, against what §2 predicted: the parser emits
`MatchStartEvent` from the four signals as designed, but FIRST-SEEN wins in
wire order, and `matchdate:` (`ktx/src/match.c:1291`) is first on every KTX
demo measured — before the `status` update (`:1337`) and the
`//ktx matchstart` stuffcmd (`:1372`) — so `matchStartSignal` reads
`matchdate` on all 13 FFA demos and on 1 100 of the 1 500 healthy archive
control, not `ktx-matchstart`. All of them share the same `TimeMs`, so the
source names which byte arrived first and nothing about the instant. The
salvage was also bigger than the ~106-of-138 the §1 census predicted: ALL
138 gained streams, because the `status` transition alone reaches the 24
`fortress` and 8 `ctf` demos §1 wrote off as out of scope.

## 1. What the demos say

13 demos in `ffa-demos/` (untracked). All are KTX 1.45 on MVDSV 1.10,
serverinfo `mode=ffa`, `deathmatch=3`, `timelimit=6`, no `teamplay` key,
`status=Countdown` in the opening `fullserverinfo`. Every one has the same
wire shape:

| t | signal | notes |
|---|---|---|
| ~0.6 s | `svc_print` L3 `Server starts recording (memory): ffa_1[dm2]…` | mvdsv-side |
| same frame | `svc_print` L2 **`matchdate: 2026-01-16 20:57:38 UTC`** | `ktx/src/match.c:1291` |
| same frame | `svc_stufftext` **`//ktx matchstart`** | `match.c:1372`, STUFFCMD_DEMOONLY |
| same frame | `svc_serverinfo status Countdown` → **`status "6 min left"`** | `match.c:2475` then `match.c:1337` |
| 60 s ticks | `status "5 min left"` … `"0 min left"` | `match.c:723` |
| ~360 s | `svc_print` L2 **`The match is over`** | `match.c:331` |
| same frame | `//finalscores "Jan 16, 21:03" "FFA" "dm2" ":f" 36 "SMOK" 35` | `commands.c:6975`; team1/team2 are the top-2 **players** |
| same frame | `status Standby` | `match.c:2565` |
| +0.1 s | `svc_intermission` | `client.c:695-702` |

What is **absent** is the one line the pipeline keys on: `"The match has
begun!"`. KTX ground truth (`ktx/src/match.c:1294-1297`):

```c
if (!k_matchLess || cvar("k_matchless_countdown"))
    G_bprint(2, "%s\n", redtext("The match has begun!"));
```

`k_matchless 1` (`world.c:1874-1877` re-arms `StartTimer()` every frame
while a player is present; `match.c:2460-2466` sets the countdown to 0)
forces the usermode to FFA or CTF (`world.c:1638-1666`) and skips exactly
three things at start: `ShowMatchSettings()`, the `protect2.wav` stuff,
and the "has begun" line. `matchdate:`, `//ktx matchstart` and the
`status` transition are emitted by the same `StartMatch()` in **every**
mode, unconditionally (`//ktx matchstart` has no cvar gate; `matchdate:`
is gated only on `deathmatch != 0` and non-hoony). There is no serverinfo
key that says "matchless" — `mode=ffa` names the usermode, and an FFA
server with `k_matchless 0` runs the ordinary ready → countdown → "has
begun" flow (three such demos are in the archive: `ffa_5[dm4]`,
`ffa_3[dm6]`, `ffa_9[e1m2]`, all with `The match has begun!` at t≈10 s).

### What the pipeline does with them today

- `noMatch.reason = matchStartUnannounced` on all 13 (`statusAtOpen:
  Countdown`, `statusRunningSeen: true`, 211 kills parsed on dm2).
- No `streams`, so no buckets / damage / playerStats / locGraph / aim.
- `match.players[].deaths == 0` everywhere: the parser's obituary-death
  corroborator is gated on its own `matchStarted` flag
  (`mvd-reader/parser/print.go:263`), which never flips.
- `match.teams` is a list of pseudo-teams built from each player's userinfo
  `team` tag (`'tro`, `red`, `rr`, `dojo`, … — 9 of them on shifter),
  `sources.teams: "derived"`. In FFA every player is their own side.
- `match.mode = "FFA"` from `//finalscores` (`sources.mode: finalscores`),
  no demoinfo block on these servers.

### Archive census (`.reports/nomatch-marker/marked-1032.csv`)

Of the 138 `matchStartUnannounced` demos in the 50 951-demo sweep:

| slice | n | shape |
|---|---|---|
| `mode=ffa`, gamedir `qw`, ktx 1.42 | 61 | identical to the 13 above; 61/61 carry `matchdate:`, all `Countdown` at open |
| `mode=""`, gamedir `qw`, ktx 1.40-beta / 1.38 | 45 | `Countdown → 20 min left → …`; 43/45 carry `matchdate:` |
| gamedir `fortress` | 24 | foreign mod, out of scope |
| gamedir `ctf`, teamplay 419 | 8 | foreign mod, out of scope |

So the fix below salvages ~106 of the 138, not just these 13. The hub
(`searchGames mode=FFA`) indexes **zero** FFA games, so test demos must be
local-only golden entries.

## 2. Design: a Layer-1 match-boundary event

Today match-start detection is duplicated: the parser scans prints against
`parser.MatchStartPatterns` for its obituary gate, and eleven analyzers
each embed a `MatchTimingDetector` that re-scans the same prints
(`backpacks`, `weapon_pickups`, `clock`, `metadata`, `damage`, `frag`,
`timeline`, `match`, `shots`, `items`, plus the intermission hook). Adding
`//ktx matchstart` as a source would mean threading `OnStuffText` and
`OnServerInfo` through all eleven.

**Proposed:** the parser owns the verdict and emits it once.

```go
// mvd-reader/parser (re-exported from events)
type MatchStartEvent struct {
    TimeMs int32
    Source string // "ktx-matchstart" | "print" | "matchdate" | "status"
}
```

Sources, first-seen wins (the parser's existing `matchStarted` latch flips
on the same event):

1. `svc_stufftext` `//ktx matchstart` — new, in `tryEmitKtxHints`'s
   neighbourhood (`parser.go:457`). Unconditional in KTX since the
   directive exists; the strongest signal.
2. `svc_print` matching `MatchStartPatterns` — the existing table, kept
   verbatim for kmod/qwe ("The duel has begun!").
3. `svc_print` line-initial `matchdate:` — same server frame as (1) in
   every KTX version that has (1); the only signal on 1.38/1.40-beta.
4. `svc_serverinfo status` transition from non-running to running —
   `statusNamesRunningGame` moves from `analyzer/metadata.go` to
   `mvd-reader/events` so both layers share one definition (same pattern
   as `MatchStartPatterns`). On ktx 1.42 matchless this lands ~30 ms after
   (1)/(3), so it only ever decides when the other three are absent.

`MatchTimingDetector` gains `OnMatchStart(*events.MatchStartEvent)` and
drops the start half of `OnPrint`; the end half (`matchEndPatterns` +
`OnIntermission`) stays in analytics for now — moving it is a mechanical
follow-up, not needed for FFA (these demos print `The match is over`).
Each embedder's `OnEvent` switch swaps `timing.OnPrint(e)` for the new
case; no analyzer needs to know why the match started.

Why not the minimal patch (add `matchdate:` to `MatchStartPatterns`)?
It would work for 104 of 138 and is a two-line change, but it keeps the
print-scanning duplicated in two layers and does not use the directive KTX
designed for this. `matchdate:` still goes in as source (3) either way.

**Golden-drift risk.** In current KTX all four signals land in the same
`TimeMs`, so match-relative timestamps on the 14 golden demos should not
move. MVD_FORMAT.md:1541 says `matchdate:` arrives "one frame before" the
"has begun" line on a 2008 demo — if that ever puts (3) a frame ahead of
(2), the match start moves one frame EARLIER, which is the more correct
value (both are printed from `StartMatch()`). Measure on the 1 500-demo
healthy control before/after (§5); report the drift distribution rather
than assuming.

### Ripple once `Started` flips on a matchless demo

- `streams` is built; `noMatch` disappears (the contract: present exactly
  when `streams` is absent — `nomatch.go:42`). `RESULT_SCHEMA.md`'s reason
  table counts change; update them from the re-census, and reword
  `matchStartUnannounced` (it now means "foreign mod or no KTX signal").
- `streams.global.matchStartSource` already has `"matchdate"`; the
  `demoStartSource: epoch` anchor works. No new vocabulary needed.
- Parser obituary gate flips → deaths appear. Check `match.players[].deaths`
  against `//finalscores`-era expectations and KTX obits (dm2: 211 kills).
- `timeBase: "demo"` fallback (`timeline_finalize.go flagDemoTimeBase`)
  fires when start is at demo `t=0`; here start is at ~0.6 s, so it must
  NOT fire — assert in the golden.
- `dm6` (3.8 s, 0 kills, map voted off) and `dm4` (5.4 s) are legitimate
  near-empty matches: start at 0.6 s, `The match is over` at 3.8 s. They
  should produce a streams block with two players and no `noMatch`; verify
  nothing downstream divides by a zero-length window.

## 3. FFA as a mode (independent of start detection)

These break on the countdown-style FFA demos too, so they are a separate
work item. All observed on `ffa_3[dm6]` (archive `52c1421d…`) and
`ffa_1[dm2]`:

1. **Pseudo-teams.** `match.teams` is derived from userinfo team tags
   (`match.go:562-575`). In FFA (KTX forces `teamplay 0`,
   `world.c:1638-1666`) those tags are decoration: dm2 has three players
   tagged `red` who are killing each other. Decision (2026-08-26): there are
   no teams in FFA. The web UI shows no team information at all (no Teams
   panel, no team colours, no team aggregates, individual frag-sorted
   scoreboard). On the API, where a consumer needs a team, use the duel
   layout the roster already produces — one team per player, `team ==
   name` — so `rebuildDuelMatch` generalises to "individual mode" and the
   frontend's shape test (`app.js:2125`, `team === name`) keeps working
   without a mode string. `match.players[].team` keeps the raw tag only as
   `rawTeam`-style provenance if we want it; `playerStats.teams` stays
   absent (already the case via `isNonTeamMode`).
2. **`applyFinalScoresTeams` guard** (`match.go:665`): it fills `teams`
   from `//finalscores` only when the derived list is empty. Once (1)
   empties it for FFA, this would re-populate it with the top-2 PLAYERS as
   "teams". Gate on the mode descriptor.
3. **Teammate predicates read the raw tag.** Measured on `ffa_1[dm2]`:
   `frag.go:443 isTeamKill` (userinfo team equality, nothing else) marks
   **34 of 211 kills as teamkills**; `aimcore/aim.go:311` drops same-tag
   players from a shooter's enemy set, so the three `red` players are never
   attributed as each other's targets; `damage.go:694 h.isTeam` and
   `shots.go:694 victimKindOf` do the same, gated only on `IsDuel()`;
   region control comes out `teams: null, buckets: 0` by accident (needs
   exactly two labels, `view/region_control.go:184`). All of these need one
   `TeamBased` verdict instead of the tag.
4. **`isNonTeamMode`** (`player_stats.go:1425`) is already an ad-hoc mode
   table (`ffa, duel, 1on1, race, bloodfest`); it becomes a method on the
   descriptor.
5. **`//finalscores` in FFA** names two players, not two teams. The
   `FinalScoresEvent` consumer (`match.go resolveMode`, metadata) must not
   read `Team1/Team2` as teams when the mode is individual.
6. **Frontend**: scoreboard, summary and timeline colouring assume ≤4
   teams. FFA needs an individual scoreboard (frag-sorted players, no team
   colour), and the map/timeline should colour per player. Region control
   and team-aggregate panels hide.

### Players coming and going

Already modelled: every `streams.players[]` and `playerStats.players[]`
row carries `identity` (demo-local reconnect key, e.g. `s0u27`) and
`sessions[]` — half-open `[startMs, endMs)` wire occupancies with `slot`
and `userId`, one entry per connection, so a player who leaves and comes
back has several segments and a slot reused by a different person is not
merged (`result/streams.go:354`). `window.presentMs / aliveMs / deadMs`
are the per-player denominators. The golden corpus pins the reconnect
cases (`defer_reconnect`, `sameslot_rejoin`, `rename_handover`). What is
NOT yet verified is this machinery on an 8-player FFA where joins happen
after match start (dm2: `Player36` and `:f` enter at 3.4 s / 3.7 s, `nexus`
frag-resets at 281 s and 326 s) — add that demo as a golden and read the
sessions by hand once PR A gives it streams. Mid-match joiners' `window`
must count from their first session, not the match start.

## 4. Sidequest: where mode logic lives today

Full survey (file:line for every site) is in the session notes; the shape:

**Match structure is centralised, mode is not.** `MatchTimingDetector` +
`events.MatchStartPatterns` genuinely own start/end (10 embedders, one
phrase table; the parser's `p.matchStarted` is the only second copy and
§2 removes it). `Roster.Duel()` (`roster.go:49-63`, duel ⟺ exactly two
demoinfo players, mode string never consulted) is the only team-shape
authority, and the frontend follows it by shape (`app.js:2125 isDuel`
checks `team === name`), which is why duel rendering is robust.

**There is no mode descriptor.** Five vocabularies are produced and none
is normalised to another:

| | vocabulary | produced | consumed by |
|---|---|---|---|
| V1 | demoinfo `mode` (`duel`, `team`, `ffa`, …) | `demoinfo.go:212` | `damage.go:926 tpModeApplies`, `damagerecon/inputs.go:189` duel prior, five eval cmds |
| V2 | `//finalscores` (`duel`, `team`, `FFA`, `Clan Arena`, …) | `ktx_finalscores.go:118` | `match.go:645` fallback |
| V3 | countdown `Mode` row (`Duel`, `FFA`, `LGC`, …) | `metadata.go:412` | `player_stats.go:1425 isNonTeamMode`, `mvd-api/overview.go:202` (**inverted** precedence vs `resolveMode`), damagerecon/backpack fallbacks |
| V4 | serverinfo `mode` composite (`4on4-midair`, `wipeout-wo-df`) | never parsed into a struct | **three** independent `strings.Split(si["mode"],"-")` walkers: `damagerecon.go:212`, `backpack_linkage.go:368`, `backpack_recon.go:607`, each with its own token subset |
| V5 | hub search facet (`1on1`…`ctf`) | external | `index.html:141` + `app.js:1066` hard-coded twice |

`Match.Mode` — the one field with a resolver and provenance
(`match.go:638`) — is consumed by **nobody**; it is display-only.

Four different answers to "is this a solo mode": `player_stats.go:1425`
(`ffa/duel/1on1/race/bloodfest`), `damagerecon/inputs.go:193`
(`duel/1on1`), `qw-recon-oracle:224`, `app.js:1659` (`!1on1 && !ffa`).
Verbatim duplicate: `damage.go:926-931` and
`cmd/qw-demoinfo-eval/main.go:312-315`. The `boundedMode` enum in
`openapi.yaml:4240` is a hand-mirrored copy of `damagerecon.SkipModeReason`'s
output and has already lagged once. KTX's authoritative `tp`/`dm` are parsed
from the demoinfo block (`demoinfo.go:215-216`) and **dropped** at
`demoinfo.go:140-155`, so `isTeamplay` and `weaponStayDetector` guess from
serverinfo on demos where the answer is in memory. Rounds (CA / wipeout /
hoony `//demomark 0 round-07`) are documented and modelled nowhere.

One contract wrinkle: `items.go:336-345` registers spawners un-gated, so a
`noMatch` result still ships a full `items` section with zero pickups —
the only section that ignores the "present exactly when streams is absent"
invariant.

### Proposal: `analyzer/gamemode.go`

```go
type GameMode struct {
    Canonical string   // duel | team | ffa | ctf | ca | wipeout | race | hoony | coop | unknown
    TeamBased bool     // teamplay in force: tags mean sides
    Rounds    bool     // ca / wipeout / hoony: score is rounds, not frags
    Matchless bool     // no countdown (inferred: start source != print, see §2)
    Submodes  map[string]bool // midair instagib lgc df ra bf yw
    Sources   ...      // which vocabulary decided each field
}
func resolveGameMode(di *DemoInfoResult, fs *FinalScores, ms *MatchSettings, si map[string]string, roster *Roster) GameMode
```

published on `CoreOutputs` next to `IsDuel()` (which becomes
`Mode.Canonical == "duel"`, still roster-derived), and as a new
`match.gameMode` result block so the frontend and API read one verdict.
Precedence: demoinfo `tp`/`dm`/`mode` → serverinfo `teamplay`/`deathmatch`
+ parsed V4 → countdown V3 → `//finalscores` V2 → roster shape, recorded
per field. Consumers to migrate, in value order:

1. `frag.go:443 isTeamKill`, `aimcore/aim.go:311`, `damage.go:694`,
   `shots.go:694`, `player_stats.go:1381-1417 isTeamplay`, `match.go:562`
   team rows + `applyFinalScoresTeams` → `TeamBased`.
2. `damage.go:926 tpModeApplies` + its eval-cmd copy → `TeamBased`.
3. `damagerecon.go:212`, `backpack_linkage.go:368`, `backpack_recon.go:607`
   → one V4 parser (`ParseServerinfoMode`) feeding `Submodes`; each keeps
   its own *selection* of tokens, loses its own *split*.
4. `mvd-api/overview.go:202` → `match.gameMode.canonical` (fixes the
   inverted precedence).
5. `app.js:1659` search-row branch → hub row shape or a server flag.
   **Not migrated, and this is the resolution rather than a deferral.**
   That branch reads a HUB SEARCH ROW, not a `Result`: the row is a listing
   from hub.quakeworld.nu carrying its own `mode` facet (V5 in the table
   above), and no demo has been analysed at that point, so there is no
   descriptor to read and no server flag on the row to read instead. What
   landed is the single-source fix the duplication actually deserved: the
   `{1on1, ffa}` set is now `SEARCH_INDIVIDUAL_MODES`, declared beside
   `SEARCH_MODE_LABELS` (the facet list itself), instead of an inline
   `mode !== '1on1' && mode !== 'ffa'` at the one call site.
6. Drift test: `openapi.yaml` `boundedMode` enum ⊇ `SkipModeReason` tokens.

Not in scope now, worth a ticket: `Rounds` consumers (`applyFinalScoresTeams`
comparing rounds to frags, Teams panel), the `items` no-match gate.

## 5. Verification

- Unit: parser emits `MatchStartEvent` from each of the four sources, in
  precedence order, once; `matchStarted` flips on it; chat-level prints
  still refused; `MatchTimingDetector.OnMatchStart` idempotent;
  `statusNamesRunningGame` tests move with the function; `noMatchVerdict`
  still classifies the foreign-mod cases.
- Golden corpus: add local-only `file` entries (`mvd-analytics/testdata/
  demos/`, sha256 in `corpus.json`) for one matchless FFA (landed as
  `ffa_matchless_nova_260704`, 2 players, full 6 min — the `dranzdm8`
  recording first picked for this slot has no BSP on this machine, so the
  golden would have skipped its floor-height and loc columns), one
  countdown FFA (archive `52c1421d…` = `ffa_countdown_dm6_260106`,
  3 players) and the 3.2 s `ffa_matchless_dm6_260704_3s` edge case, plus
  `ffa_matchless_dm2_260116_joiners` for the mid-match leaver / slot-reuse
  reconnect. `ctf_archive_dm6_qwe240_status` (archive `2a2ed2e9ca…`, qwe
  2.40 CTF on dm6) covers the `status` signal, the only one of the four
  with no golden otherwise. Commit the goldens.
- Existing 14 goldens: expect byte-identical output; any drift must be
  explained by the one-frame `matchdate:` case and reported.
- Re-run the `.reports/nomatch-marker` probe over `marked-1032.csv` and
  `marked-healthy-1500.csv` against `/mnt/HC_Volume_106625439/data/mvd/`:
  expect ~106 of 138 `matchStartUnannounced` to gain streams, zero healthy
  demos to lose them, and match-start ms unchanged on the healthy set.
- `make test`, `make build`, and a `qw-analyze -bulk ffa-demos/` sweep with
  `-format md` eyeballed for deaths, streams, and no `noMatch`.

## 6. Docs to touch

`mvd-reader/MVD_FORMAT.md` (match-start table: add `//ktx matchstart`,
`matchdate:`, `status` rows with the `match.c` line refs and the matchless
explanation; new event in the derived-events list),
`mvd-reader/README.md` (event table), `mvd-analytics/RESULT_SCHEMA.md`
(noMatch reason table + counts, `match.teams` / `sources.teams`
semantics, schema bump), `mvd-analytics/README.md`, `RELEASE_NOTES.md`,
top-level `README.md` known-limitations (FFA), `mvd-api/openapi/openapi.yaml`
if `sources.teams` gains a value, `mvd-web/README.md` for the FFA layout,
`plan-archive-features.md` lead 8 stage (b) (this IS stage (b) for the
KTX half). `CurrentSchemaVersion` bump in `result/result.go`.

## 7. Sequencing

1. **PR A — match boundary event** (§2 + §5 + docs). Self-contained,
   salvages the 13 demos and ~106 archive demos, no frontend change.
2. **PR B — mode descriptor + FFA team semantics** (§3 + §4). Schema bump,
   frontend individual scoreboard.
