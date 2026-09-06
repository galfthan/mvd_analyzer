# Result JSON schema reference

This is the field-level reference for the JSON shape produced by
`mvd-analytics`. The Go source of truth lives in `mvd-analytics/result/`;
this document mirrors that shape so consumers (web UI, CLIs, AI
agents, future MCP servers) can navigate it without reading Go.

For tutorial-grade narrative on Items, Backpacks, and WeaponPickups
— including signal-attribution mechanics — see
[`README.md`](README.md). Pipeline architecture and how to add an
analyzer are also covered there.

## Top-level shape

`result.Result` (defined in `result/result.go`):

| Field | JSON key | Type | Intent |
|---|---|---|---|
| SchemaVersion | `schemaVersion` | int | Regeneration counter for the analysis output: it ticks on **every** observable change, additive and corrective alike, so it is a cache key rather than a break signal (the compatibility policy is [`mvd-api/API.md` §2.7](../mvd-api/API.md#27-api-versioning-and-stability)). Current value lives at `result/result.go:CurrentSchemaVersion`. |
| FilePath | `filePath` | string | Source path / display label of the analyzed demo. |
| Match | `match` | *MatchResult | Match summary: map, game dir, duration, players, teams. |
| Frags | `frags` | *FragResult | Total / per-player / per-weapon frag breakdown plus chronological frag list. |
| Messages | `messages` | *MessagesResult | Frag and chat events for timeline display. |
| DemoInfo | `demoInfo` | *DemoInfoResult | Verbatim KTX STUFFCMD demoinfo JSON; authoritative weapon / damage / pickup stats. **Untransformed by design.** |
| TimelineAnalysis | `timelineAnalysis` | *TimelineAnalysisResult | High-res state buckets, key-moment events, region control. |
| Metadata | `metadata` | *MetadataResult | Server cvars (fullserverinfo) + parsed match-settings centerprint. |
| LocGraph | `locGraph` | *LocGraphResult | Loc-to-loc movement graph (nodes + transitions). |
| Items | `items` | *ItemsResult | Per-entity pickup / respawn timeline (per match). |
| Damage | `damage` | *DamageResult | Per-hit damage log + aggregates (matrix, per-weapon, given/taken, EWep victim-weapon buckets) — decoded from the KTX `mvdhidden_dmgdone` stream (`source: "ktx"`) or rebuilt by the reconstruction on pre-instrumentation demos (`source: "reconstructed"`, which carries a per-demo `coverage` figure since v74), with a KTX-scoreboard cross-check. |
| Shots | `shots` | *ShotsResult | Per-shot weapon-fire stream (who fired what, at what ms) from `svc_sound` fire sounds + LG `TE_LIGHTNING2` beams, with same-frame hitscan→damage links and a KTX-accuracy cross-check. |
| Aim | `aim` | *AimResult | Per-player aim analysis: normalized crosshair-error samples (hitscan), LG ramp-onto-target, rocket direct/splash, LG reach/whiff. Derived (post-process) from Shots + Streams + Damage. |
| MapEntities | `mapEntities` | *MapEntitiesResult | Static designed map layout (item spawns, spawnpoints, teleporters, buttons) from the BSP entity corpus. |
| Backpacks | `backpacks` | []BackpackDrop | RL/LG backpack drops, each stamped `source`: `ktx` from the `//ktx drop` hint, or (v72) `reconstructed` — replayed from DropBackpack's own rule on demos older than that hint. A reconstructed row also carries the pack's `fate` (`picked` / `expired` / `unobserved`) with `picker` / `pickerTeam` / `pickupTime`, read off the wire's backpack-entity track; a `ktx` row carries `fate: "expired"` when KTX announced the timeout in `//ktx expire`. |
| WeaponPickups | `weaponPickups` | []WeaponPickup | Slot-weapon acquisitions with kills-before-next-death effectiveness. |
| Opening | `opening` | *OpeningResult | Match opening: per-player match-start spawn loc + first in-match take of each contested spawner (armors, mega, powerups, RL/LG). Pure projection of items + streams (schema v51). |
| PlayerStats | `playerStats` | *PlayerStatsResult | Canonical per-player + per-team statistics with per-family provenance: corrected scoreboard, damage, pickup tallies, and **possession time** (time with each weapon / armor type / **no armor**). Computed for every demo (schema v63). |
| Streams | `streams` | *Streams | Native-rate per-player + global state-change streams (position/view/health/armor/ammo/items tracks, movers, and the opt-in spatial weapon streams — see the Streams section). |
| Highlights | `highlights` | *HighlightsResult, omitempty | v76 — the highlight catalogue: `discharges`, `quadbores`, `telefrags`, `airgibs` — four lists of `HighlightEvent`, each row carrying **what everyone involved had** (relation to the actor, health / armor / stack, weapons, powerups, loc) so any consumer can rank them. Absent when the demo has no streams or no frag log; see [HighlightsResult](#highlightsresult-highlights). |
| NoMatch | `noMatch` | *NoMatchResult, omitempty | v74 — the explicit marker on a result that carries **no analyzable match**: present exactly when `streams` is absent, naming why (`midMatchRecording` / `matchStartUnannounced` / `noMatchDeclared` / `noPlayRecorded` / `demoUnreadable`) with the wire evidence behind the verdict. It is a fact about the DEMO, not a pipeline failure — the distinction from `errors` below. See [NoMatchResult](#nomatchresult-nomatch-schema-v74). |
| Errors | `errors` | []string | Non-fatal parse / analysis errors (omitted when empty). Includes analyzer `Finalize` failures, an `"event stream aborted: …"` entry when the event source returned a non-EOF error mid-demo (a truncated or corrupt stream — a clean end of demo does **not** appear here), and a `"region control: …"` entry when the region-control post-pass failed. A non-empty `errors` on an otherwise-populated result means the analysis is partial but usable. |
| ParseWarnings | `parseWarnings` | *ParseWarnings, omitempty | v72 — the READER's census of wire data it could not decode (unknown `svc_*` / temp-entity / hidden-message types, payloads that failed to parse). A **distinct signal from `errors`**: sub-fatal, parse-level, and about events that never reached an analyzer at all. Omitted on a clean parse, which is the normal case. See [ParseWarnings](#parsewarnings-parsewarnings-schema-v72). |

Sub-result fields are pointers or slices with `omitempty`, so a missing
key means "the analyzer didn't produce this section for this demo"
(usually because the source lacked the necessary signals — e.g. no
KTX hints means no Items / Backpacks).

## MatchResult (`match`)

Defined in `result/match.go`.

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Map | `map` | string | **The canonical map identifier** — always the SHORT name (`e1m2`, `dm2`, `aerowalk`). Resolved exactly like `Result.EffectiveMap()`: the KTX demoinfo map, else the serverinfo `map` key, else (v72) the `//finalscores` map; only when none of those names a map does it fall back to the cleaned level title. `sources.map` says which. Join on this against `searchGames` rows, `metadata.serverInfo.map` and every BSP / loc / geometry file key. |
| MapTitle | `mapTitle` | string | The level TITLE announced in `svc_serverdata` (`"Castle of the Damned"` on e1m2, `"Claustrophobopolis"` on dm2), cleaned of a `.bsp` suffix and a trailing ` by <author>` hint. **Display only** — never an identifier, a join key or a file key: it is free-form mapper-chosen text, not unique, and absent on demos whose `svc_serverdata` named no level (then omitted). |
| Mode | `mode` | string, omitempty | v72 — KTX's own game-mode name: `duel`, `team`, `FFA`/`ffa`, `CTF`/`ctf`, `Clan Arena`/`clan-arena`, `Wipeout`, `HoonyMode`/`hoonymode`, `RA`/`rocket-arena`, `race`, `midair`, `instagib`. The two spellings per mode are the two sources' own vocabularies (demoinfo's `GetMode`, `ktx/src/stats.c:309`, and `//finalscores`' `lastscores2str`, `commands.c:6755`) reported verbatim, with `sources.mode` naming which — **compare case-insensitively**. NOT the serverinfo `mode` key (`1on1`, `4on4-midair`, a third vocabulary, verbatim under `metadata.serverInfo`) nor the countdown table's display spelling (`Duel`, under `metadata.matchSettings.mode`). |
| GameMode | `gameMode` | *GameMode, omitempty | **v75 — the normalised mode verdict, and the one block to branch on.** `Mode` above stays the verbatim server vocabulary beside it; see [GameMode](#gamemode). Absent only on a Result produced by a registry with no `roster` node (hand-built pipelines, unit tests) or on a consumer's own stored pre-v75 JSON — mvd-api's cache is keyed by schema version and never hands one back. |
| GameDir | `gameDir` | string | Game directory (`qw`, `fortress`, custom). |
| Duration | `duration` | int32 | Match length in milliseconds (parser-derived). Read this for "how long was the match". |
| Players | `players` | []PlayerStat | Lightweight scoreboard view. |
| Teams | `teams` | []TeamStat | Team standings. In an **individual mode** (`gameMode.teamBased` false — every duel, FFA, race) this is one row per PLAYER, named after the player, and `sources.teams` reads `individual`. Omitted when the scoreboard produced no rows at all. |
| Sources | `sources` | MatchSources | v72 — per-field provenance; see below. |

### GameMode

v75. Defined in `result/gamemode.go`; resolved once by
`analyzer/gamemode.go` (the `roster` node) and published here and on
`/overview` as `gameMode`.

Before it existed the pipeline answered "what mode is this" from **five
unrelated vocabularies with no translation between them** — KTX's demoinfo
`mode`, the `//finalscores` mode name, the countdown centerprint's display
spelling, the composite serverinfo `mode` key, and the hub search facet —
and answered "is this a team game" four separate times with four
hand-written tables that disagreed. All five raw vocabularies are still
published beside it, unnormalised (`match.mode`, `metadata.serverInfo.mode`,
`metadata.matchSettings.mode`, `metadata.finalScores.mode`); this is the
normalisation on top.

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Canonical | `canonical` | string | Mode SHAPE: `duel` \| `team` \| `ffa` \| `ctf` \| `ca` \| `wipeout` \| `race` \| `hoony` \| `ra` \| `coop` \| `unknown`. Ruleset modifiers are NOT canonical values — an instagib game is still an FFA or a 4on4 — they are in `submodes`. |
| TeamBased | `teamBased` | bool | **Whether a player's team tag names a SIDE rather than decoration.** Mirrors KTX's own `tp_num()` gate, `(isTeam() \|\| isCTF() \|\| coop) ? teamplay : 0` (`ktx/src/g_utils.c:1586-1588`): the mode must be a team mode AND the teamplay cvar non-zero. False for duel, ffa, race and rocket arena **when a mode source named that mode** — a canonical the roster merely inferred does not veto a teamplay cvar (see Precedence). |
| Rounds | `rounds` | bool, omitempty | The score is ROUNDS WON, not frags (ca, wipeout, ra, hoony — `ktx/src/commands.c:6867-6886`). Published for consumers comparing a scoreline against a frag total; **nothing in the pipeline gates on it yet**. |
| Submodes | `submodes` | []string, omitempty | Ruleset modifiers, sorted: KTX's own tokens from the composite serverinfo `mode` key (`midair`, `instagib`, `lgc`, `ca`, `wo`, `ra`, `race`, `gm`, `df`, `yw`, `bf` — `SetMode4ServerInfo`, `ktx/src/world.c:1475-1543`) plus the ones a `k_*` cvar exported into the serverinfo by hand (KTX never publishes them itself; `k_fallbunny` is on 31 archive demos) and the countdown table name. |
| Sources | `sources` | GameModeSources | Which vocabulary decided each field. |

`sources.canonical`: `ktx` (demoinfo `mode`) | `countdown` (the KTX
countdown Mode row) | `serverinfo` (the composite `mode` key's umode) |
`finalscores` | `roster` (the participant shape — a two-player match is a
duel). Empty when `canonical` is `unknown`.

`sources.teamBased`: `ktx` (demoinfo `tp`) | `serverinfo` (the `teamplay`
cvar) | `countdown` | `mode` (the canonical mode alone decides) | `roster`
(the participant shape: a 1v1 has no sides, or — weakest of all — no tag
held more than one player). **Never empty** — the resolver always reaches a
verdict. `roster` is the one source the individual LAYOUT refuses to act on
when it decided `teamBased`: it is an inference over the very tags the
layout would discard.

**Precedence.** Strongest first, per field:

- `canonical`: demoinfo `mode` → countdown Mode row → serverinfo `mode`
  umode → `//finalscores` mode → roster shape. The countdown outranks the
  serverinfo `mode` key because that key names the last usermode COMMAND
  that ran (`um_name_byidx(current_umode - 1)`,
  `ktx/src/world.c:1482`, assigned only in `UserMode()`,
  `commands.c:4848`), which a plain `teamplay` change leaves stale, while
  the countdown centerprint states the settings the match started under.
- `teamBased`: an **explicitly named** individual canonical (the mode alone
  decides — KTX's `tp_num()` gate closes on it) → demoinfo `tp` →
  serverinfo `teamplay` → countdown Teamplay → an explicitly named
  team-shaped canonical → the roster shape.

A canonical the ROSTER inferred never gets the mode's first word: "two
participants" says how many sides played, not whether the teamplay ruleset
was in force. A CTF server's 1v1 (`*gamedir=ctf`, `teamplay=419`) is
`canonical: duel, sources.canonical: roster` **and** `teamBased: true,
sources.teamBased: serverinfo` — with the individual LAYOUT all the same,
because that is `Roster.Duel()`.

`sources.rounds`: `mode`, or empty when `rounds` is false.
`sources.submodes`: `serverinfo` | `countdown`, or empty when there are none.

**Individual layout.** `teamBased` is the RULESET; the LAYOUT is named by
`match.sources.teams`. When it is `individual` the pipeline has laid the match
out with one side per player: `match.teams` one row per player,
`match.players[].team` equal to the player's own name, the raw tag on
`rawTeam`, `playerStats.teams` absent, and region control withheld on
anything wider than two participants. This is the layout duels have always
produced — a consumer needs no mode string to render an individual
scoreboard, and the shape test "every `team === name`" identifies it.

`teamBased: false` produces that layout only when the verdict came from a
source that actually saw a mode or a cvar (`sources.teamBased` = `ktx` |
`serverinfo` | `countdown` | `mode`), or when the roster independently
established a duel (`Roster.Duel()`, two participants). A `teamBased: false`
whose `sources.teamBased` is `roster` — the weakest inference, a shape read
off the very tags the rewrite would discard — keeps the DERIVED team layout
on three or more participants: a mode-less, cvar-less demo with three
participants wearing three distinct tags stays `sources.teams: derived` with
three one-player team rows, rather than having real tags erased on the
strength of a scoreboard that has not been read yet
(`analyzer/gamemode.go`, `individualLayoutFromMode`).

**Why the tag is not a side in FFA.** KTX forces `teamplay 0` there
(`ktx/src/world.c:1652-1655`) and players keep their clan tags anyway: on
`ffa_1[dm2]` three of eight wear `red` and spend the match killing each
other.

### MatchSources

v72. Says where the resolved identity fields above came from, so a
consumer can tell a server-authoritative value from a pipeline-derived
one without re-deriving the precedence:

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Map | `map` | string, omitempty | `ktx` (demoinfo) \| `serverinfo` \| `finalscores` \| `levelTitle` (the degraded last resort — on a titled map that string is not a shortname at all). |
| Mode | `mode` | string, omitempty | `ktx` (demoinfo) \| `finalscores`. |
| Teams | `teams` | string, omitempty | `derived` — the rows are the per-player scoreboard summed by team, the normal case; `individual` (v75) — the rows are one per PLAYER, named after the player, either because `gameMode.teamBased` is false or because the roster classified a 1v1 (the two-player case of the same layout, which is why a 1v1 on a teamplay server can read `individual` while the descriptor still reports `teamBased: true` about the SERVER's ruleset); or `finalscores`, meaning the scoreboard produced no team rows at all and the two sides come from KTX's end-of-match stuffcmd instead. `finalscores` is never adopted under the individual layout, where the directive names the top-two PLAYERS (`ktx/src/commands.c:6963-6977`), not two teams. |

`//finalscores` **never displaces a demoinfo value**, and never corrects a
derived team row: it fills only what nothing else answered. Where both
exist they are worth comparing, and `metadata.finalScores` keeps the wire
record verbatim for exactly that — but read `mode` first, because on Clan
Arena and Wipeout KTX's score is *rounds won*, not frags (see
[MetadataResult](#metadataresult-metadata)).

The `startTime` / `endTime` fields were **removed in schema v36** — after time
normalisation `startTime` was always 0 and `endTime` always equalled
`duration`, and both duplicated [`streams.global.matchStart` /
`matchEnd`](#globalstream). Read `duration` for match length, or
`streams.global` for the match window.

### PlayerStat

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Name | `name` | string | Display name. |
| Team | `team` | string | Team name. In the **individual layout** (`match.sources.teams` = `individual` — every duel, and any FFA / race whose non-team ruleset was named by a mode or a cvar) it is the player's own name; see *Individual layout* above for the exact rule. |
| RawTeam | `rawTeam` | string, omitempty | v75 — the userinfo `team` tag as it stood on the wire, kept when the individual layout replaced `Team` with the player's name. In FFA those tags are real (clan membership) but name no side. Omitted when it would repeat `Team`, and when the tag was empty. Nothing in the pipeline reads it back. |
| Frags | `frags` | int | Canonical QW net score. Normally the `svc_updatefrags` scoreboard cursor, frozen at match end. For a player the server dropped mid-match it is the mod's own departure broadcast (`"<name> left the game with N frags"`) when the demo carries one, because `SV_DropClient` zeroes the slot's scoreboard entry in the same server frame as the drop. A player who reconnected owns several occupancies, and their scores are FOLDED: a stint the server carried the total over to (a userid-0 KTX ghost row, or one the server announced as `"<name> rejoins the game with N frags"`) replaces the running total, every other stint adds to it — so a matchless rejoiner, who gets neither signal because KTX makes no ghost there, keeps both stints' frags. See [`analyzer/match.md`](analyzer/match.md). |
| Kills | `kills` | int | Gross kills, frag-log-corrected. Supersedes KTX demoinfo `stats.kills` (which over-counts pentagram-deflect telefrags); `0` when the demo had no frag log. |
| Deaths | `deaths` | int | Deaths, frag-log-corrected. `0` when the demo had no frag log. |
| Suicides | `suicides` | int | Self-inflicted deaths, frag-log-corrected. Counts every `IsSuicide` frag entry (incl. fall / lava / squish / drown), which KTX demoinfo `stats.suicides` undercounts — world-dealt deaths bump the world entity's counter, not the victim's (`ktx/src/client.c:4951`). `0` when the demo had no frag log. |

`MatchResult` is the non-KTX-fallback view: it works on any MVD source.
`Frags`/`Kills`/`Deaths` are the **corrected scoreboard** — net frags from
the scoreboard stream, kills/deaths from the frag log, both independent of
the sometimes-wrong KTX demoinfo (which over-counts pentagram-deflect
telefrags and resets after a reconnect). For per-weapon kills, accuracy or
damage the canonical answer is `playerStats` (`score.byWeapon`,
`accuracy.byWeapon`, `damage.byWeapon`), which merges both sources and
stamps `src` per family. `Frags.ByPlayer` (parser-derived) and
`DemoInfo.Players[]` (KTX, verbatim) remain available for a consumer that
wants one side unmerged.

### TeamStat

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Name | `name` | string | Team name. |
| Frags | `frags` | int | Team total. |

## FragResult (`frags`)

Defined in `result/frag.go`.

| Field | JSON key | Type |
|---|---|---|
| TotalFrags | `totalFrags` | int |
| Frags | `frags` | []FragEntry |
| ByWeapon | `byWeapon` | map[string]int — **enemy kills only** (suicides/teamkills excluded) |
| ByPlayer | `byPlayer` | map[string]*PlayerFrags |
| KillsMeasured | `killsMeasured` | bool — the demo-global kill-**attribution** verdict (schema v65). Not `omitempty`; always emitted. |
| Unpaired | `unpaired` | []FragEntry, omitempty — v74; teamkill obituaries whose other party the recovery could not name. **Not** part of `frags`, `totalFrags`, `byWeapon` or `byPlayer`. See [FragResult.unpaired](#fragresultunpaired). |

#### `killsMeasured` — the one measuredness bit of this section

`false` means the log is **empty on a demo where players demonstrably
died**: every obituary went unmatched, because the server printed them in
a form this pipeline does not parse. Deaths still count — they come from
the protocol death events, not the obituaries — and that is precisely
what makes the zeros dangerous, since a row reading `kills: 0,
deaths: 92` looks measured. Consumers must present `kills`, `teamkills`,
`byWeapon`, `suicides` and efficiency as **unmeasured** rather than zero
when it is false. `true` on a demo that simply has no deaths to
contradict — there the zeros are honest measurements.

The verdict is **decided once, by the analyzer** (`analyzer.killsMeasurable`)
and stored here, so every consumer reads one answer instead of
re-deriving the rule: `result.PlayerStatsScore` omits the kill side on it
(v62), and the interval views publish it verbatim as
`MeasuredSources.frags`. A second implementation is exactly how the rule
came to be applied on `/player-stats` and not on the interval views.

It is **demo-global and survives every filter.** The `/frags` view
recomputes each aggregate from the filtered log but copies this field
through unchanged: narrowing by `players` / `weapons` / `from` / `to`
cannot make a demo's obituaries matchable or unmatchable, and recomputing
it would report "unmeasured" for any filter that happens to match
nothing — the opposite of what the flag means.

### FragEntry

| Field | JSON key | Type |
|---|---|---|
| Time | `time` | int32 (match-relative ms) |
| Killer | `killer` | string |
| Victim | `victim` | string |
| Weapon | `weapon` | string (`rl`, `lg`, `gl`, `ssg`, `sng`, `ng`, `sg`, `axe`, `hook`, `rail`, `tele`, `stomp`, env: `lava`/`fall`/`water`/`slime`/`world`/`squish`/`explobox`, plus `unknown`/`suicide`/`teamkill` for obituaries whose phrasing hides the weapon; the closed set is `view.fragWeaponVocab`) |
| IsSuicide | `isSuicide` | bool (omitempty) |
| IsTeamKill | `isTeamKill` | bool (omitempty) |
| Cause | `cause` | string (omitempty; v76) — the obituary's **sub-cause beneath `weapon`** for the deathtypes KTX prints distinctly but books under an ordinary weapon: `discharge` (an LG water discharge — `weapon` stays `lg`, exactly as KTX's own per-weapon stats count it: the six self prints `ktx/src/client.c:5292-5325` and the two kill prints `:5645-5660`, one of which — "accepts X's discharge" — shares its marker with the shaft kill and is promoted off its suffix by the analytics matcher), `deflect` (the pentagram telefrag deflections dtTELE2 `:5219` / dtTELE3 `:5228` — `weapon` `tele`, `isSuicide`: the teleporter died) and `spawnicide` (dtTELE4 `:5240-5261`, `weapon` `tele`, `isSuicide`). Empty on every other row; `byWeapon` is unchanged. |
| Deflector | `deflector` | string (omitempty; v76) — on a `cause: "deflect"` row, the surviving pentagram holder when the print names them: the dtTELE3 line "X was telefragged by **Y**'s Satan's power". The dtTELE2 print names nobody, so the field is empty there even though a pent holder existed (the highlights section resolves them from the pent intervals). |

`teamkill` is a **cause-less** placeholder, not the cause of every
teamkill. KTX's team branch tests three deathtypes by name before it
reaches its random phrasing pick (`ktx/src/client.c:5343-5410`), and all
three keep their real weapon: `tele` for "X was telefragged by his
teammate", `stomp` for "X was jumped/crushed by his teammate", `squish`
for "X squished a teammate" (the one cause-carrying phrasing that names
the killer instead of the victim). Only the random four — "X checks his
glasses", "X loses another friend", "X mows down a teammate", "X gets a
frag for the other team" — have no deathtype in them, and those are the
rows that carry `teamkill`. `isTeamKill` is set on all of them.

The distinction is load-bearing downstream: `tele`/`stomp` are positional
instant kills that the damage sections fold as capacity rather than
pricing on the damage curve (see "Positional kills" below). `squish` is
NOT positional — a mover crush is ordinary damage on the wire, attributed
to the player who triggered the mover — so it prices normally, exactly
like its enemy-facing twin "X squishes Y".

The same rule holds outside the team branch: every marker stamps the cause
its engine deathtype carries in the wire damage log's own vocabulary
(`mvd.DeathTypeToWeapon` / `EnvironmentalDamageType`), so filtering on a
cause returns the obituary rows and the damage rows alike. `explobox` (KTX
"X blew up", `dtEXPLO_BOX`) and `hook` are spelled the same on both sides.
Four obituaries genuinely establish nothing finer than `world` and keep
it: "X was spiked" (the print collapses `dtNG` and `dtSNG` from a non-player
attacker into one string), "X died" (`dtTRIGGER_HURT` and KTX's world-branch
catch-all share that one string, which is why `trigger` appears on the
damage side only — there the wire carries the deathtype itself), "X was
zapped" (`dtLASER`) and "X ate a lavaball" (`dtFIREBALL`) — the last two
have no token of their own in either vocabulary.

A self-kill carries the **weapon/cause that produced it**
(`rl`/`gl`/`lg` for weapon self-detonations, env labels for lava/fall/etc.)
with `isSuicide` set; only the `/kill` console command (KTX "X suicides",
−2 frags) keeps weapon `suicide`. So a real `/kill` is distinguishable
from a weapon self-detonation, and recovered teamkills never carry a stale
`isSuicide` (killer ≠ victim). One kill-shaped phrasing is a self-kill:
KTX's double-pentagram telefrag "X was telefragged by Y's Satan's power"
(`dtTELE3`, `client.c:5228-5237`) prints the ordinary telefrag verb but
books `logfrag(targ, targ)` — Y is credited nothing — so it carries
`isSuicide` with killer = victim = X.

Includes **teamkills** recovered from both kinds whose obituary
names only one party. *Killer-named* ("X loses another friend") fill in
the victim by matching the coincident authoritative `DeathEvent` on the
killer's team. *Victim-named* ("X was telefragged by his teammate") fill
in the killer by combining position co-location with the teamkiller's −1
frag-delta (the two signals must agree, so a rare alias can't
misattribute). Both recoveries can come up empty — a team telefrag costs
its killer no frag under default KTX rules (the −1 is gated on
`k_tp_tele_death`), so a spawn pile can leave both signals mute — and the
leftovers are published in `unpaired[]` below rather than dropped.

### FragResult.unpaired

`unpaired[]` carries the teamkill obituaries that name only one party and
whose other side neither recovery could identify. Exactly one of `killer` /
`victim` is the placeholder string `"teammate"`.

They are **not** in `frags[]` and are **not** counted in `totalFrags`,
`byWeapon` or `byPlayer`: every entry of the frag log names both sides,
which is what makes it usable for per-player tallies, and a placeholder is
not a player. Consumers must not fold `unpaired[]` into per-player counts
either. What it is for is the two things the wire really did say — that the
death happened, and what CAUSED it. The victim-named forms carry the real
weapon (`tele` / `stomp`), which is what lets the reconstructed damage
section type such a kill positionally instead of pricing the victim's whole
corpse drop as team weapon damage.

Measured on a 6 000-demo archive sweep, victim-named teamkill obituaries
run about 2 000 lines over ~900 demos and the recovery names the killer on
the large majority; the residual is what lands here. Schema v74 — before
it, these obituaries were dropped from the Result entirely and were only
visible in `MessagesResult.Events[type=frag]`.

### PlayerFrags

| Field | JSON key | Type |
|---|---|---|
| Kills | `kills` | int |
| Deaths | `deaths` | int |
| TeamKills | `teamkills` | int (omitempty) — KTX "tk"; killer-named teamkills only |
| ByWeapon | `byWeapon` | map[string]int |

## DamageResult (`damage`)

Defined in `result/damage.go`. Reconstructed from the KTX
`mvdhidden_dmgdone` stream (see `mvd-reader/MVD_FORMAT.md`) — or, since
schema v71, **reconstructed** from the state streams when the demo
predates that instrumentation.

**Provenance (`source`, schema v71).** `source: "ktx"` marks a section
decoded from the wire's damage stream — the raw per-hit values are
direct measurements (the bounded family is still arithmetic
reconstructed from them; summaries may substitute KTX scoreboard
figures, see `boundedSource`). `source: "reconstructed"` marks a section rebuilt by the
`damage-recon` node (package `mvd-analytics/damagerecon`) from the
health/armor change streams + spectator-visible evidence (LG beams,
projectile flights, fire sounds, position/velocity tracks, the frag
log): magnitudes are near-exact (the observed h/a delta IS the bounded
value), attribution is best-effort inference — treat per-player match
totals as ~1% estimates and prefer aggregates over single events. Full
accuracy tables + trust guidance: `damagerecon/ACCURACY.md`. A
wire-measured section is never overwritten; reconstruction requires the
spatial shot streams (always built by mvd-api and the web WASM;
`-include projectiles,beams` on the CLI) and stands down on
`skipped:*` server modes. `source` is absent only in pre-v71 stored
results. Distinct from `boundedSource` below, which records a view-time
substitution WITHIN a KTX-sourced payload. On reconstructed sections the
victim-weapon fields (`victimWep`, `enemyVs*`, `ewep`) are withheld
(empty / zero) when the recording froze its StatItems weapon bits — the
norm for old recorders — because classifying against frozen inventory
would be confidently wrong; `ewep: 0` there means unmeasurable
(damagerecon/ACCURACY.md §old-recorder degradations).

**How much of the match the reconstruction saw (`coverage`, schema
v74).** A reconstructed section is only as complete as the health/armor
stat channel the recording broadcast, and a small class of archive
recordings barely broadcasts it at all: positions and the frag log are
intact, the damage simply is not observable, and the section then
describes a fraction of the real match. `coverage` says so per demo —
`{kills, covered, ratio}`, the share of the frag log's weapon kills whose
lethal instant the `events` log accounts for. See
[DamageCoverage](#damagecoverage) for the exact denominator. Measured
over the full 10 702-demo oracle sweep: **99.0% of reconstructions read
≥ 0.95** (median 1.000, 96% of them exactly 1.000), **0.80% read below
0.50** (median 0.182) — and **0.18% fall between**, spanning 0.500–0.944.
A hard bimodal core, but with a real gradient tail: read `ratio` as a
magnitude, not as a two-valued healthy/broken flag. Nothing in the
pipeline gates on it; read it as "how much of the frag-log-visible match
is in here", and prefer the percentiles in
[`damagerecon/ACCURACY.md`](damagerecon/ACCURACY.md) §per-demo coverage
to an invented cutoff. Present only on
`source: "reconstructed"` (a wire log records every `T_Damage` call, so
its coverage is 1 by construction — measured 1.000 on all 65 GT demos)
and only when the frag log names at least one scoreable kill. It is a
WHOLE-MATCH stamp like `source` and `boundedMode`: a `players` / `weapons`
/ time filter carries it through unchanged rather than rescoping it.
**Every consumer that rides the damage section inherits it** — the
`playerStats` damage family (`src: "reconstructed"`), its reconstructed
`accuracy.hits`, and `aim.players[].weapons[].recon.hits` are built from
this section, so their coverage is this field and they do not restate it.

**Unbound vs bounded — two families (schema v55).** The **raw** family
(`damage`, `given`, `taken`, …) is **unbound** — the full hit including
overkill, capped only at 9999, exactly the wire value. The **bounded**
family (`events[].bounded`, each player's `bounded` nest) carries KTX's
scoreboard semantics per hit (`dmg_dealt`, `combat.c:783`), derived from
the death-value identity: a survived hit has no overkill, so bounded ==
raw exactly; a killing hit's overkill is measured by the end-of-frame
death broadcast (bounded = raw + deathValue — the armor share cancels).
Residual approximation only where the wire hides state: the −99 corpse
clamp, respawn-masked deaths, and same-frame multi-hit deaths (overkill
cascaded from the last hit backward in wire order); pent/teamplay-
nullified hits are bounded to their estimated armor share. `dmg` echoes
which family a payload carries (`"both"` as stored); `boundedMode` is
`"standard"`, or `"skipped:<mode>"` (midair / instagib / dmgfrags /
ca / wipeout / ra / lgc / race) when the server mode rewrites or
suppresses `T_Damage` in ways the wire does not expose — every bounded field is then absent, and the
raw family is unaffected. The `scoreboard` sub-object surfaces both
families against the KTX scoreboard; raw diverges by the overkill
(expected), bounded should nearly match (its correctness signal).

**KTX-exact bounded on a summary (phase 16.3).** The per-hit bounded
reconstruction is best-effort, but KTX's own end-of-match totals
(`demoInfo.players[].dmg` + `weapons[].damage.enemy`/`.team`) are exact.
So on an
**unfiltered summary** that serves the bounded family (`dmg=bounded` or
`dmg=both`), the view substitutes each player's bounded `given`,
`givenTeam`, `givenSelf`, `ewep` and the per-weapon `byWeapon` /
`byWeaponTeam` with the KTX
figures when the demo carries `demoInfo`, echoing `boundedSource: "ktx"`
(else `"reconstructed"`). The substitution is deliberately partial:
`taken` stays reconstructed (KTX `dmg.taken` is enemy-only, our `taken`
counts all sources), the `enemyVs*` buckets stay reconstructed (KTX has
no such split) — so on a KTX-sourced summary they may no longer sum
exactly to the substituted `given` — and `byWeaponSelf` stays
reconstructed (KTX records no per-weapon self damage). A
filtered/windowed summary has no KTX
counterpart, so it stays fully reconstructed (no `boundedSource`).

The per-player / matrix aggregates AND the `events` log are both **match-time
only** (schema v50): the analyzer drops out-of-match (warmup / post-match) hits
at the source, so every damage figure and the `events` log are built from the
same in-match hit set (KTX scoreboard parity). Each `events` entry carries the
match-relative `time` so consumers can still window within the match.

**Positional kills (telefrag, stomp) fold their honest value into
`given`/`givenTeam`/`taken` — and, on enemy kills, the `enemyVs*`/`ewep`
buckets (schema v54).** A telefrag
(deathtype `tele`) is an instant kill reported on the wire as the 9999
sentinel; a stomp (deathtype `stomp`, landing on a head) is a real ~10 HP
`T_Damage`. Both stay out of the `events` log, every per-weapon map
(`byWeapon`, `byWeaponTeam`, `byWeaponSelf`), `matrix`,
`ewep` and `totalDamage` (KTX maps them to `wpNONE`, so its
`weapons[].damage` excludes them too), are listed in `telefrags` /
`stomps` (and the opt-in `telefrag` / `stomp` events) and counted
per-player in `PlayerDamage.telefrags` / `.stomps`. But their DAMAGE
folds into the given/taken aggregates in **both families**, matching
KTX's own accumulation (`combat.c:1046-1076` has no tele/stomp
exclusion): a telefrag folds its **bounded** reconstruction (victim's
full armor + remaining health; armor alone for the pent-vs-pent
`dtTELE3` variant) into the raw family too — the wire 9999 is a kill
guarantee, not a measurement — while a stomp folds its wire value (raw)
/ reconstruction (bounded). An ENEMY kill's fold also lands in the
victim-weapon `enemyVs*`/`ewep` buckets (KTX `dmg_eweapon` has no
deathtype gate either, `combat.c:1073`), keeping "the buckets sum to
`given`" true. Each `telefrags[]`/`stomps[]` entry carries the folded
value as `bounded` (plus `damage` when the raw fold diverged, and
`victimWep` for the bucket). No fold-in at all on
`boundedMode: skipped:*` demos — given/taken and the buckets revert to
pure v53 exclusion there. The kill still appears in `FragResult` and as
a `frag` event.

A `source: "reconstructed"` section reaches the same lists by a different
route: it has no deathtype on the wire, so it reads the CAUSE out of the
obituary (`FragEntry.weapon` — `tele`/`stomp`, including the teammate
phrasings, see FragEntry above). Until 2026-08-26 the teammate forms
carried the cause-less `teamkill` token, so a reconstructed section
listed no TEAM telefrags at all and charged each one as ordinary team
damage at the victim's observed corpse drop — 99 raw points more than
the capacity, KTX's `-99` clamp
([damagerecon/ACCURACY.md](damagerecon/ACCURACY.md) §"Team telefrags were
not damage").

| Field | JSON key | Type |
|---|---|---|
| TotalDamage | `totalDamage` | int (match-time, all sources; excl. telefrags + stomps) |
| Events | `events` | []DamageEntry (chronological; excl. telefrags + stomps) |
| ByWeapon | `byWeapon` | map[string]int (enemy damage by attacker weapon) |
| ByPlayer | `byPlayer` | map[string]*PlayerDamage |
| Matrix | `matrix` | []DamagePair (attacker→victim totals) |
| Telefrags | `telefrags` | []PositionalKill (omitempty — instant kills, separate from damage) |
| Stomps | `stomps` | []PositionalKill (omitempty — head-stomp kills, separate from damage) |
| Scoreboard | `scoreboard` | *DamageReconciliation (omitempty — a KTX whole-match cross-check with no per-event provenance: a players-only filter narrows it, but a `weapons` or a RESTRICTIVE time filter OMITS it entirely, since it cannot be recomputed against those filters; an explicit `from`/`to` window covering the whole match counts as unfiltered) |
| Source | `source` | string (omitempty — `ktx` = decoded from the wire damage stream, `reconstructed` = rebuilt from state streams by damage-recon; absent only pre-v71) |
| Coverage | `coverage` | *DamageCoverage (omitempty — v74; how much of the match a RECONSTRUCTED section saw. Absent on `source: "ktx"` and when the frag log names no scoreable kill; whole-match, never rescoped by a filter) |
| RocketDirectDamage | `rocketDirectDamage` | int (omitempty — v74; the server's DIRECT rocket damage constant as measured from this demo's own hits: 110 on every KTX since 1.36, which replaced id1's `100 + g_random()*20` in 2008. Present exactly when `rocketDirectRegime` is `fixed`. It is the era signal the rl touch count leans on: see [WeaponAimRecon](#weaponaimrecon)) |
| RocketDirectRegime | `rocketDirectRegime` | string (omitempty — v74; WHICH verdict the measurement above reached, a three-value total partition of every RECONSTRUCTED section: `fixed` (the demo's near-direct rocket hits clustered on 110), `spread` (there were enough of them to test and they did NOT cluster — what a pre-1.36 server looks like, and evidence AGAINST the constant rather than absence of evidence) or `unestablished` (fewer than six such hits, so the question was never put). Absent on `source: "ktx"`, where the wire's own splash flag makes the question moot) |
| Dmg | `dmg` | string (omitempty — family echo: `both` as stored, `bounded` from the view, absent on a raw view) |
| BoundedMode | `boundedMode` | string (omitempty — `standard`, or `skipped:<mode>` with mode ∈ midair/instagib/dmgfrags/ca/wipeout/ra/lgc/race; the clan-arena family joined in v71, detected from the composite serverinfo `mode` string) |
| BoundedSource | `boundedSource` | string (omitempty — provenance of a SUMMARY response's per-player bounded figures: `ktx` when substituted with KTX's exact end-of-match scoreboard totals, else `reconstructed`; set by the view ONLY on an unfiltered summary serving the bounded family — `dmg=bounded`/`dmg=both`; the stored Result never carries it) |

### DamageCoverage

`damage.coverage` (schema v74) — how much of the match a **reconstructed**
damage section could actually see. The frag log is its own cross-check:
every kill it names is a place where damage provably happened, so the
share of those instants the reconstructed `events` log accounts for is a
per-demo completeness figure that needs no ground truth.

| Field | JSON key | Type / meaning |
|---|---|---|
| Kills | `kills` | int — the denominator: frag-log kills that carry damage arithmetic. An ENEMY kill (not a suicide, not a team kill, killer named and not `world`, killer ≠ victim) by a WEAPON (telefrags and stomps are positional and aggregate outside `events`) on two players the roster names. |
| Covered | `covered` | int — of those, the kills with at least one reconstructed `events` entry at the victim's death instant. |
| Ratio | `ratio` | float — `covered / kills`, in [0,1]. |

Present only when `source: "reconstructed"` **and** `kills > 0`. A demo
whose frag log names no scoreable kill has no denominator, so the object
is absent rather than reading a misleading `0` — and **absence on a
reconstructed section means "no anchor existed, completeness
unassessed"**, never "coverage was zero" and never "coverage was full".

Reading it: **1.000 is the norm**, not the ceiling of a noisy scale.
Measured over the full 10 702-demo oracle sweep (E0–E4 reconstructed
runs): 99.0% read ≥ 0.95, with 96% of those exactly 1.000 and a median of
1.000; 0.80% are the known silent-stat-channel class (recordings that
barely broadcast health/armor at all) at 0.182 median, 0.488 worst; and
0.18% — 19 demos, 18 of them qwsv-era E0 — fall in between, spanning
0.500 to 0.944 on 18–287-kill denominators. The core is hard and bimodal,
but the band between the modes is populated, so **`ratio` is a magnitude
to read, not a flag with two settings**. The same metric run over a WIRE
damage log reads exactly 1.000 on all 65 ground-truth demos. Full tables,
the sensitivity control and the rejected alternative (bounded damage per
kill, which does not separate at all) are in `damagerecon/ACCURACY.md`
§per-demo coverage.

Two things it does not tell you. It **cannot see a loss correlated with
the frag log** — a late-started recording or a stream hole drops the
obituaries and the damage evidence together, shrinking `kills` and
`covered` in step, and reads 1.000 over the fraction that survived; the
question it answers is "how much of the FRAG-LOG-VISIBLE match is here",
not "was this recording complete". And it **does not localize**: one
scalar covers the whole demo, so a mid-band duel is as consistent with
one unbroadcast victim beside one perfectly observed one as with both
halves degraded. Per-victim coverage is a recorded follow-up
(`plan-archive-features.md`), not a shipped field.

The pipeline never gates on it — surface, don't filter. A consumer that
wants a cutoff should take one from those percentiles.

### PositionalKill

A telefrag (`telefrags`, deathtype `tele`) or stomp (`stomps`, deathtype
`stomp`) — an instant kill from occupying a player's space rather than a
weapon. No raw damage amount (a telefrag's wire value is the 9999
instakill sentinel); `bounded` carries the reconstructed value the
fold-in added to the aggregates.

| Field | JSON key | Type |
|---|---|---|
| Time | `time` | int32 (match-relative ms) |
| Attacker | `attacker` | string (killer) |
| Victim | `victim` | string |
| IsTeam | `isTeam` | bool (omitempty — same team) |
| Bounded | `bounded` | *int (omitempty — telefrag: victim's full armor + remaining health, armor alone for the pent-vs-pent `dtTELE3` variant; stomp: wire value through the bounded arithmetic; **nil exactly when reconstruction was skipped** — `0` is a real nullified-stomp value, mirroring `DamageEntry.bounded`'s pointer convention) |
| Damage | `damage` | int (omitempty — the RAW-family fold value when it differs from `bounded`: only a stomp whose bounded arithmetic capped below the wire value; absent means "equal to `bounded`") |
| VictimWep | `victimWep` | string (omitempty — victim's class at hit `sg`/`mid`/`lg`/`rl`/`both`; set on ENEMY kills only, so `view.Damage`'s filtered recompute can reproduce the fold's `enemyVs*`/`ewep` buckets; absent on team/self/world kills and skipped-mode demos) |

### DamageEntry

| Field | JSON key | Type |
|---|---|---|
| Time | `time` | int32 (match-relative ms) |
| Attacker | `attacker` | string (`world` for environmental / non-player inflictor) |
| Victim | `victim` | string |
| Weapon | `weapon` | string (attacker weapon `rl`/`lg`/…, or env category `fall`/`lava`/…) |
| Damage | `damage` | int (unbound) |
| IsSplash | `isSplash` | bool (omitempty) |
| IsEnv | `isEnv` | bool (omitempty — world/environmental) |
| IsSelf | `isSelf` | bool (omitempty — attacker == victim) |
| IsTeam | `isTeam` | bool (omitempty — same team, not self) |
| VictimWep | `victimWep` | string (omitempty — victim's class at hit: `sg`/`mid`/`lg`/`rl`/`both`; set only on enemy hits) |
| Bounded | `bounded` | *int (omitempty — KTX-scoreboard reconstruction; **absent means "equal to `damage`"**; `0` is a real value: a pent/teamplay-nullified hit still emits a wire event) |

### PlayerDamage

`Given` counts enemy damage only; `Taken` counts **all** sources (enemy +
team + self + env), so it runs above the KTX `dmg.taken` (which is
enemy-player damage only — `dmg_t`, `combat.c:1083`; it excludes team,
self, and environmental). The `EnemyVs*` buckets partition `Given` by the **victim's**
held weapons at hit time — KTX "ewep" semantics, keyed on the *target's*
inventory, not the attacker's weapon. Mutually exclusive, priority
RL+LG > RL > LG > mid > sg (NG counts as shotgun-tier). `ewep` is the
sum of the LG/RL/both buckets = damage dealt to enemies holding RL or LG.

`byWeaponTeam` and `byWeaponSelf` (schema v63) split `givenTeam` and
`givenSelf` by the attacker's weapon exactly as `byWeapon` splits
`given` — same keys, same tele/stomp exclusion (positional kills fold
into the totals only). **Measuredness is family-level, not key-level:**
all three maps are measured wherever the `damage` section itself exists
(it only exists when a damage stream was read), and within them an
absent KEY means "dealt none with that weapon" — zeros are dropped, not
stored. The two splits are `omitempty` because a player who dealt no
team/self damage has nothing to say, never because the split was
unmeasurable. `matrix`, the top-level `byWeapon` and the `enemyVs*`
buckets stay enemy-only.

| Field | JSON key | Type |
|---|---|---|
| Given | `given` | int (to enemies) |
| Taken | `taken` | int (all sources) |
| GivenTeam | `givenTeam` | int |
| GivenSelf | `givenSelf` | int |
| TakenEnv | `takenEnv` | int |
| ByWeapon | `byWeapon` | map[string]int (enemy given, by attacker weapon) |
| ByWeaponTeam | `byWeaponTeam` | map[string]int (omitempty — team given, by attacker weapon) |
| ByWeaponSelf | `byWeaponSelf` | map[string]int (omitempty — self given, by attacker weapon) |
| EnemyVsSG | `enemyVsSg` | int (victim held shotgun-tier only) |
| EnemyVsMid | `enemyVsMid` | int (victim held ssg/sng/gl) |
| EnemyVsLG | `enemyVsLg` | int (victim held LG, not RL) |
| EnemyVsRL | `enemyVsRl` | int (victim held RL, not LG) |
| EnemyVsBoth | `enemyVsBoth` | int (victim held both RL and LG) |
| EWep | `ewep` | int (= enemyVsLg + enemyVsRl + enemyVsBoth) |
| Telefrags | `telefrags` | int (omitempty — instant-kill telefrags DEALT, a count) |
| Stomps | `stomps` | int (omitempty — head-stomp kills DEALT, a count) |
| Bounded | `bounded` | *PlayerDamage (omitempty — the bounded family: same damage-figure fields under KTX-scoreboard semantics; the nest never carries `telefrags`/`stomps`/`bounded`) |

### DamagePair

| Field | JSON key | Type |
|---|---|---|
| Attacker | `attacker` | string |
| Victim | `victim` | string |
| Damage | `damage` | int |
| ByWeapon | `byWeapon` | map[string]int (attacker weapon → damage to this victim) |

### DamageReconciliation / DamageDelta

Diagnostic cross-check vs the KTX scoreboard (`demoInfo.players[].dmg`).
Keyed by player name. The `stream*` fields are **this pipeline's
unbound** figures (overkill-inclusive, from the `mvdhidden_dmgdone`
stream); the `score*` fields are **KTX's bounded** figures (capped to
victim health, from the scoreboard JSON). `score*` ≤ `stream*` by the
overkill; `scoreEwep` is the KTX `enemy-weapons` field.

| Field (DamageDelta) | JSON key | Type |
|---|---|---|
| StreamGiven | `streamGiven` | int (unbound, this pipeline) |
| ScoreGiven | `scoreGiven` | int (bounded, KTX scoreboard) |
| StreamTaken | `streamTaken` | int (unbound, this pipeline) |
| ScoreTaken | `scoreTaken` | int (bounded, KTX scoreboard) |
| StreamEWep | `streamEwep` | int (unbound, this pipeline) |
| ScoreEWep | `scoreEwep` | int (bounded, KTX scoreboard) |
| Bounded | `bounded` | *DamageDeltaBounded (omitempty — this pipeline's bounded family vs the same scoreboard; near-equality is the reconstruction's correctness signal) |

| Field (DamageDeltaBounded) | JSON key | Type |
|---|---|---|
| StreamGiven | `streamGiven` | int (bounded enemy given, this pipeline) |
| StreamTaken | `streamTaken` | int (bounded **enemy-only** taken — KTX `dmg_t` semantics, unlike `PlayerDamage.taken`) |
| StreamEWep | `streamEwep` | int (bounded ewep, this pipeline) |
| StreamTeam | `streamTeam` | int (bounded team given, this pipeline) |
| ScoreTeam | `scoreTeam` | int (KTX scoreboard `dmg.team` — reconciled only in the bounded family, where it is comparable) |

## ShotsResult (`shots`)

Defined in `result/shots.go`. A **shot** is one discrete weapon fire on the
wire: for SG/SSG/RL/GL/NG/SNG it is one `svc_sound` fire sound on the
shooter's `CHAN_WEAPON` (the sound carries the firing entity, so attribution
is exact and works on any QW server); for LG — which has no per-shot fire
sound — it is one `TE_LIGHTNING2` beam, emitted once per fire tick and
carrying the firing entity directly (`source:"beam"`). One beam == one LG
attack == one cell, so LG counts match KTX `acc.attacks` exactly. Times are
match-relative ms (same clock as `damage.events[].t`).

The `shots` stream is **match-gated** (schema v50): warmup / prewar /
post-match fires are dropped at the source, like every analytics stream
except chat, so the stream and the `byPlayer` aggregates are both match-only.
To correlate aiming with fires, join a shot's `time` against the
shooter's `streams.players[].pos` track (`vP`/`vYa` view angles, `vX/vY/vZ`
velocity) by player + nearest pos-track `t`.

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Shots | `shots` | []Shot | Every detected fire, chronological. |
| ByPlayer | `byPlayer` | []PlayerShots (omitempty) | Match-time per-weapon counts + hitscan accuracy. |
| Reconciliation | `reconciliation` | *ShotsReconciliation (omitempty) | Cross-check vs KTX `acc.attacks`; nil when no demoInfo. |

### Shot

`weapon` is the lowercase KTX name (`axe`,`sg`,`ssg`,`ng`,`sng`,`gl`,`rl`,`lg`).
`source` is `sound` (a CHAN_WEAPON fire sound) or `beam` (an LG
TE_LIGHTNING2 bolt). `hit`/`victims` are set for linkable weapons via the
KTX damage stream:
- **Hitscan** (`sg`/`ssg`/`lg`) — the fire and its damage land in the
  same server frame and link by attacker + weapon + frame.
- **Axe** — melee: the fire sound is the `weapons/ax1.wav` swing (one per
  attack, so whiffed swings count as shots), but the damage traceline
  runs exactly 200ms later (W_FireAxe at the third 0.1s animation
  think), so it links through a swing+200ms window rather than
  same-frame.
- **Rocket/grenade** (`rl`/`gl`) — linked by entity flight tracking: the
  rocket/grenade entity brackets the flight (`spawn → despawn`), so the fire
  is matched to its launch frame (by muzzle) and the impact damage is the
  shooter's same-weapon damage at the despawn frame. This pins *which* fire
  caused *which* impact when several projectiles are in flight, which a
  naive "next damage" link cannot. Since v74 the flight half of that link is
  published as `flightEnd` (below).

- **Nails** (`ng`/`sng`) — linked the same way as rockets via the nail flight
  bracket, but **only when nail tracking is requested** (`-include nails`);
  otherwise ng/sng fires are left unlinked (no `hit`, and no accuracy in
  `byPlayer`). Both weapons launch exactly one spike per attack
  (`W_FireSpikes` / `W_FireSuperSpikes`, `ktx/src/weapons.c:1672`, `:1640`
  — the super nailgun's `-2` is AMMO, not a second nail), so the link is a
  1:1 question, but it under-recovers: measured against the verbatim KTX
  block on 186 archive demos, ng reaches 65.5% of rows exactly at 32.1%
  aggregate under-count and sng 48.5% at 22.8%. Nail accuracy is a floor.

`hit` counts damage to ≥1 player (including self/team splash for rl/gl);
`victims` lists them. No damage stream (non-KTX) → `hit` never set.
`victimKinds` classifies each victim relative to the shooter, mirroring the
damage layer's `isSelf`/`isTeam` semantics: `self` = same wire slot (rl/gl
self-splash — a rocket jump is a `hit` with the shooter as its own victim),
`team` = same non-empty team and not self, else `enemy`. It is omitted when
every victim is an enemy (the common case); when present it is parallel to
`victims`.

`flightEnd` (schema v74) is the **fire→flight** half of the projectile link,
published so consumers stop having to re-derive it: the time the tracked
projectile this fire launched died. It is set whether or not that impact
damaged anyone, and it is absent exactly when there was no flight to link —
a hitscan fire, an entity the server never broadcast (a rocket that detonates
in its muzzle frame), a flight still open when the recording ended, or an
ng/sng fire on a parse without nail decoding. That absence is meaningful: it
is the same state the measured hit counter reads as a miss, and it is what
lets the reconstructed hit tier count rl/gl on the measured definition (see
[WeaponAimRecon](#weaponaimrecon)).

`flightEnd` is the observed despawn frame, **not** a flight duration. `time`
and the entity update are quantized independently, so a point-blank impact can
land up to about one demo frame *before* its own fire sound (measured: 6 of
37974 tracked rl/gl flights on the 53-demo dm2/dm3 eval corpus, worst −29 ms).
It is published unclamped — the despawn frame is what the wire showed — so a
consumer computing `flightEnd - time` must tolerate a small negative value.

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Time | `time` | int32 | Match-relative ms. |
| Player | `player` | string | Resolved shooter. |
| Team | `team` | string (omitempty) | |
| Weapon | `weapon` | string | |
| Source | `source` | string | `sound` \| `beam` (LG). |
| Hit | `hit` | bool (omitempty) | Fire that connected: hitscan via the same-frame damage link, rl/gl (and ng/sng with nails) via projectile linking. |
| Victims | `victims` | []string (omitempty) | Hitscan victims hit by this fire. |
| VictimKinds | `victimKinds` | []string (omitempty) | Per-victim class, parallel to `victims`: `enemy` \| `team` \| `self`. Omitted when all-enemy. |
| FlightEnd | `flightEnd` | *int32 (omitempty) | Match-relative ms at which this fire's tracked projectile died (its impact). Projectile fires only, and only when the flight was tracked. May sit up to ~1 demo frame before `time` (independent quantization). |

### PlayerShots / WeaponShots

Match-time per-player counts. `WeaponShots.Hits`/`Accuracy` are populated
only for linkable weapons (hitscan `sg`/`ssg`/`lg` + projectile `rl`/`gl`)
and only when a damage stream was present;
`Accuracy` is `Hits/Shots` (fraction of fires that connected) — note this is
*shots-that-landed*, a different metric from KTX `acc.hits` (pellet hits).
`Hits`/`Accuracy` count **all** victims (team and self hits included — KTX
scoreboard parity); `enemyHits`/`teamHits`/`selfHits` split `hits` by victim
class (see `Shot.victimKinds`). A multi-victim fire counts in every bucket
it has a victim in, so the buckets overlap and none is derivable from the
others; per-bucket accuracy is `bucketHits / shots`.

| Field (PlayerShots) | JSON key | Type |
|---|---|---|
| Player | `player` | string |
| Team | `team` | string (omitempty) |
| Total | `total` | int |
| ByWeapon | `byWeapon` | []WeaponShots |

| Field (WeaponShots) | JSON key | Type |
|---|---|---|
| Weapon | `weapon` | string |
| Shots | `shots` | int |
| Hits | `hits` | int (omitempty, hitscan) |
| Accuracy | `accuracy` | float (omitempty, hitscan) |
| EnemyHits | `enemyHits` | int (omitempty) — fires with ≥1 enemy victim |
| TeamHits | `teamHits` | int (omitempty) — fires with ≥1 teammate victim |
| SelfHits | `selfHits` | int (omitempty) — fires with ≥1 self victim (rl/gl splash) |

### ShotsReconciliation / ShotsDelta

Diagnostic cross-check vs KTX `demoInfo.players[].weapons[].acc`, keyed by
player name → per-weapon rows. KTX `acc.attacks` counts in weapon-specific
units (pellets for SG/SSG — 6 and 14 per pull; one per projectile for
RL/GL/NG/SNG; one per cell-tick for LG). `streamAttacks` converts our
discrete `streamShots` into that unit (×6 SG, ×14 SSG, ×1 otherwise) so
`streamAttacks` and `ktxAttacks` are directly comparable; a gap flags a
detection problem (or a non-standard mode such as yawnmode SSG). Diagnostic
only — never used to adjust the detected stream.

| Field (ShotsDelta) | JSON key | Type |
|---|---|---|
| Weapon | `weapon` | string |
| StreamShots | `streamShots` | int (discrete fires detected) |
| StreamAttacks | `streamAttacks` | int (converted to KTX attack unit) |
| KtxAttacks | `ktxAttacks` | int (demoInfo acc.attacks) |
| KtxHits | `ktxHits` | int (demoInfo acc.hits) |

## AimResult (`aim`)

Defined in `result/aim.go`. Per-player aim analysis derived from `Shots` +
`Streams` (interpolated position/view at fire time via
`PositionTrack.SampleAt`) + `Damage` + the LG `Streams.Beams`. The computation
lives in package `aimcore` (`aimcore.Compute`), called by the analyzer
post-processor (`analyzer/aim.go`) to fill the stored `res.Aim` once, and by
the view layer (`view.Aim`) for filtered/windowed variants — see below.
Experimental and additive — it never modifies its inputs.

**Filtering (`/aim`, `getAim`).** No schema change; a query-layer concern.
With no time window the **stored** `res.Aim` is served (a `players` filter
selects named shooters' match-wide aim; `summary` drops the `crosshair` +
`lgRamp` sample blocks). A `from`/`to` window (match-relative integer ms)
**recomputes** aim over the shots in the window via `aimcore.Compute`, so every
field scopes to the window consistently. See the /aim operation in the
served OpenAPI spec (mvd-api `/openapi.yaml`, browsable at `/docs`).

Geometry: the shot traces from the weapon **muzzle** (origin + 16, the LG/SG
fire origin) toward the enemy **hull center** (origin + 4, the −24..+32 box
midpoint). The forward vector uses the Quake `AngleVectors` convention
(`F = (cos p·cos y, cos p·sin y, −sin p)`, +pitch = down). The signed errors
`DYaw`/`DPitch` are normalized per axis by the target's angular half-extent so
the hull maps to the unit square (see CrosshairSamples).

**Truthfulness.** Hit/miss is `Shot.Hit` (the Go-linked truth), never
re-derived — for the MEASURED tier. The reconstructed tier
(`weapons[].recon`, schema v73) is the one thing here derived from a
reconstruction rather than the wire, and it lives in its own field for that
reason; see [WeaponAimRecon](#weaponaimrecon). The crosshair samples are **hitscan-only** (sg/ssg/lg — rockets
are led); note SG/SSG have pellet spread, so the web heatmap splits **LG and
SG into separate grids** (the pellet cloud would smear the precise hitscan).
A **hit** is attributed to its server-confirmed victim (nearest by crosshair
error when a pellet fire hit several), with no liveness gate — the killing
blow lands in the same frame the victim dies, so the liveness rule would
read the victim as already dead at the fire time — and no enemy filter (a
team-damage hit is a confirmed target too). A **miss** is attributed only to
an enemy whose position track brackets the fire time **and who is alive at
it** (dead players keep streaming position samples — the death-anim body —
so a corpse would otherwise remain a candidate; same liveness rule as LOS).
`Mode` is `"duel"` (one enemy → exact) or `"team"` (hits exact via the
victim; misses a nearest-crosshair-enemy heuristic). Rocket "direct" is a
non-splash-damage heuristic. The victim class is surfaced rather than
filtered: samples carry a `team` flag, and `WeaponAim` carries
enemy/team/self counter slices (see WeaponAimSplit), so consumers can view
any victim class without re-deriving it.

| Field | JSON key | Type |
|---|---|---|
| Players | `players` | []PlayerAim |
| HitsMeasured | `hitsMeasured` | bool |
| HitsSource | `hitsSource` | string (omitempty) — `"ktx"` \| `"reconstructed"` |

`hitsMeasured` reports whether the hit-derived counters (`hits`, the
pellet full/partial/miss split, direct/splash, the LG whiff classes)
were measured against a wire KTX damage stream (`damage.source ==
"ktx"`). On reconstructed/absent damage the shot linker never saw a
wire damage event, so those fields are **withheld** (schema v71) rather
than fabricated as zeros — `weapons[].hits` is omitempty, and when
`hitsMeasured` is true an absent `hits` means a measured zero. Shots,
crosshair error and the LG ramp are shot/track-derived and remain valid
either way.

`hitsSource` (schema v73) names the damage evidence behind the hit
counters, from `damage.source`'s vocabulary, and is the discriminator
between the two hit TIERS — which live in different fields and are never
mixed:

| `hitsSource` | `hitsMeasured` | populated | withheld |
|---|---|---|---|
| `"ktx"` | true | every measured counter on WeaponAim + the per-fire `hit` columns | — |
| `"reconstructed"` | **false** | `weapons[].recon.hits` only (see [WeaponAimRecon](#weaponaimrecon)) | every measured counter, the per-fire `hit` columns |
| absent | false | nothing hit-derived | everything hit-derived |

A reconstructed hit count therefore cannot be read as a measured one by
accident: it never appears in `hits`, and a consumer that does not know
the `recon` block simply sees the pre-v73 "not measured" state. The
third row is the state `hitsMeasured` alone could not distinguish from
the second — a demo with no damage section at all.

### PlayerAim

| Field | JSON key | Type |
|---|---|---|
| Player | `player` | string |
| Team | `team` | string (omitempty) |
| Mode | `mode` | string — `"duel"` or `"team"` |
| Crosshair | `crosshair` | *CrosshairSamples (omitempty) |
| LGRamp | `lgRamp` | *LGRampSamples (omitempty) |
| Weapons | `weapons` | []WeaponAim (omitempty) — rich per-weapon effectiveness. An **ordered array** (one entry per weapon the player fired), keyed by the entry's `weapon` field and sorted by a fixed weapon rank — deliberately an array, not a `{weapon: …}` object, because the order is meaningful (unlike the unordered `byWeapon` count maps on `/frags` and `/damage`, which are objects because order is irrelevant there). |

### CrosshairSamples

Columnar, one index per hitscan fire. `DYaw`/`DPitch` are signed **degrees**
— positive `dyaw` = target **left** of the crosshair (Quake yaw grows
counterclockwise; `dyaw` is target bearing − aim yaw), positive `dpitch` =
target above — the literal "degrees off the enemy" drift. (Plotting
"enemy-right reads right" therefore needs an x flip, which the bundled web
frontend applies.) `NYaw`/`NPitch`
divide each by the target's angular half-extent on that axis, so the hull maps
to the unit square: **±1 on an axis = the hull edge** (corner ≈ √2). The yaw
half-extent uses the box silhouette at the viewing angle (an axis-aligned hull
is up to √2 wider seen corner-on: `16·(|cosθ|+|sinθ|)`); the pitch half-extent
is 28. `Dist` is the muzzle→hull-center distance in Quake units. (Validated:
LG hits land ~86% inside the unit square, median radius ≈ 0.77, vs misses well
outside.)

| Field | JSON key | Type |
|---|---|---|
| T | `t` | []int32 (fire time, match ms) |
| Weapon | `w` | []string (sg/ssg/lg) |
| DYaw | `dyaw` | []float32 (deg) |
| DPitch | `dpitch` | []float32 (deg) |
| NYaw | `nyaw` | []float32 (normalized) |
| NPitch | `npitch` | []float32 (normalized) |
| Dist | `dist` | []float32 (qu) |
| Hit | `hit` | []bool (omitempty) — **absent whenever `aim.hitsMeasured` is false** (reconstructed or absent damage): a per-fire `false` there would be a fabricated miss, so the whole column is omitted. Present ⇒ one flag per sample. |
| Target | `tgt` | []string — the confirmed victim for hits (can be a teammate on team damage); the attributed live enemy for misses |
| Team | `team` | []bool (omitempty) — the attributed target is a teammate. Only hits can be team-attributed (misses target enemies by construction) and hitscan cannot self-hit, so this is the full victim-class signal for samples. Omitted when no sample is team-attributed. |

### LGRampSamples

Columnar, one index per LG fire. `Since` is ms since the start of the shaft
the fire belongs to (fires < 150 ms apart are one shaft).

| Field | JSON key | Type |
|---|---|---|
| Since | `since` | []int32 (ms since shaft start) |
| Hit | `hit` | []bool (omitempty) — absent whenever `aim.hitsMeasured` is false, exactly as `crosshair.hit` |
| Team | `team` | []bool (omitempty) — the fire connected but hit no enemy (teammate-only victims). Score enemy ramp hit% as `hit && !team`. Omitted when no fire is team-only. |

### WeaponAim

One entry per weapon the player fired. `Shots` (fires) is always present; every
other field is `omitempty`, including `Hits` — it and the rest of the
hit-derived counters are WITHHELD, not zeroed, when `aim.hitsMeasured` is false
(see [AimResult](#aimresult-aim) for the tier table). With `hitsMeasured` true an
absent `hits` is a measured zero. `Pellets`/`PelletHits` match the server's
authoritative SG/SSG per-pellet stats; `Direct` matches the server's RL/GL
direct-hit count — from the wire's own splash flag for `rl`, and from the
flight-geometry touch classifier for `gl`, whose touch the wire never records
(see the `direct` row below).

| Field | JSON key | Type / meaning |
|---|---|---|
| Weapon | `weapon` | string (lg/sg/ssg/rl/gl) |
| Shots | `shots` | int — fires (always present; measurement-grade on every demo) |
| Hits | `hits` | int (omitempty) — fires that connected. Absent when `hitsMeasured` is false (nothing to link against); absent with it true means a measured zero. The reconstructed count is never here — it is `recon.hits`. |
| Enemy | `enemy` | *WeaponAimSplit (omitempty) — the enemy-victim slice of the hit counters |
| Team | `team` | *WeaponAimSplit (omitempty) — the teammate-victim slice |
| Self | `self` | *WeaponAimSplit (omitempty) — the self-victim slice (rl/gl splash) |
| Recon | `recon` | *WeaponAimRecon (omitempty) — the RECONSTRUCTED hit tier; present only with `hitsSource: "reconstructed"` (see below) |
| Pellets | `pellets` | int (sg/ssg) — pellets fired (shots × 6/14) |
| PelletHits | `pelletHits` | int (sg/ssg) — pellets that hit (Σ damage / 4) |
| Full | `full` | int (sg/ssg) — fires where all pellets hit |
| Partial | `partial` | int (sg/ssg) — fires where some pellets hit |
| Miss | `miss` | int (sg/ssg: fires where no pellet hit; lg: aim-error misses — neither blocked nor out of range) |
| Direct | `direct` | ***int** (rl/gl, omitempty) — projectiles that TOUCHED a player, KTX's own rl/gl `hits` counter. **`rl`** is the wire's splash flag (a direct `T_MissileTouch` writes an unflagged row), so it is a subset of `hits` and `direct+splash+missed == shots`. **`gl`** cannot be — every gl row on the wire is splash-flagged (`GrenadeTouch` → `T_RadiusDamage`) — and is the flight-geometry classifier's count instead (`damagerecon/direct.go`, v75), bounded by the FIRES rather than by `hits`: it may exceed `hits` where a touch's fire went unlinked. **ABSENT IS NOT ZERO** — see the note under the table. Measured against the verbatim KTX block on 186 archive demos: `rl` 99.8% of 632 player rows exact / 0.02% aggregate, `gl` 92.0% of 424 / 3.79% |
| Splash | `splash` | ***int** (rl/gl, omitempty) — linked hits that were splash-only; for `gl` the floor-at-zero remainder `hits − direct`. Emitted and withheld together with `direct` |
| Missed | `missed` | int (rl/gl) — fires that linked to no impact |
| Blocked | `blocked` | int (lg) — the missed beam stopped short on geometry and its extension to the ~600u max range crosses a live enemy's collision hull (32×32×56 box at the enemy's tracked position): on target and in range, the obstruction denied a would-be hit |
| OutOfRange | `outOfRange` | int (lg) — the missed beam ran its full ~600u max length and its extension to infinity crosses a live enemy's collision hull: on target, the enemy was beyond reach |
| Unresolved | `unresolved` | int (lg) — no beam matched the miss |

For LG, `Hits + Blocked + Miss + OutOfRange + Unresolved == Shots`.

**`direct` / `splash`: absent is not zero.** These two are the only aim
counters carried as POINTERS (`*int`), because for them a zero is a claim.
They are emitted together, exactly when the split RAN for that weapon:

- **present, including `0`** — classified, and this player's projectiles
  touched nobody (or none of his hits were splash-only);
- **absent** — the fires were never classified. Two reachable causes: `gl` on
  a parse that built no spatial shot streams (the touch classifier's own
  input — mvd-api and the WASM build always request them, a bare
  `qw-analyze` parse does not), and `rl`/`gl` on a demo where the linker
  resolved no rl/gl fire at all (where `hits` is 0 too, so a `direct` of 0
  would be indistinguishable from a measurement).

Consumers must render an absent value as withheld, never as 0 — and must not
substitute a global flag for the row's own presence.
`playerStats.accuracy.byWeapon[rl|gl]` is derived from exactly this signal:
an absent `direct` is what makes that row fall back to the any-path count
with `hitsConvention: "anyDamage"`. `missed` does NOT ride the split — it is
`shots − hits`, which the linker answers on its own.

The pellet stats need the KTX damage stream; the RL/GL direct/splash split
needs projectile linking, which runs on every parse (the block appears
whenever any rl/gl fire linked to its flight — no opt-in required); the LG
miss split needs the opt-in `Streams.Beams`. Absent inputs simply leave
those fields zero (or, for `direct`/`splash`, absent).

### WeaponAimRecon

The **reconstructed hit tier** (schema v73), on `weapons[].recon`. Present
only when `aim.hitsSource == "reconstructed"` — i.e. on a demo whose damage
section was rebuilt by `mvd-analytics/damagerecon` — and never beside the
measured counters, which stay withheld there.

| Field | JSON key | Type / meaning |
|---|---|---|
| Hits | `hits` | int (**not** omitempty) — fires of this weapon the reconstructed damage log says connected, by ANY damage path (`anyDamage`) |
| DirectHits | `directHits` | *int (omitempty) — **rl/gl only** (schema v74): projectiles that TOUCHED a player (`directImpact`, KTX's own rl/gl convention). Absent on every other weapon, where the two coincide |

Accuracy is `recon.hits / shots` (`shots` is measurement-grade either way).
A `hits: 0` inside a present block is a real "linked nothing"; the block's
ABSENCE is what says "not recovered for this weapon".

`directHits` answers the OTHER question, the one KTX's own `acc.rl.hits` /
`acc.gl.hits` answers: KTX increments that counter inside the touch handler
and nowhere else (`ktx/src/weapons.c:990-996`, `:1327-1333`), so it counts
projectiles that touched a player rather than fires that damaged one.
Publishing it is what lets a pre-instrumentation demo be compared with a
block-carrying one at all, and it is the number
[`playerStats.accuracy.byWeapon[rl|gl].hits`](#accuracybyweaponhitsconvention--what-the-number-counts-v74)
carries on a reconstructed row.

Its rl half leans on the server's direct-damage constant — the magnitude
half of the classifier only works where that constant is fixed — so read
`damage.rocketDirectRegime` beside it, which is three-valued for exactly
this reason. Measured against the verbatim KTX block, the rl count runs
**0.65%** aggregate error / 45.9% of rows exact on the 567 `fixed` rows,
13.0% / 31.2% on the 16 `spread` ones and 22.5% / 59.2% on the 49
`unestablished` ones (1.25% pooled over 632): `spread` — a demo whose own
hits argue against the constant — is the weak population, `unestablished`
the strong one whose big aggregate is small errors over small counts.
Nothing is gated on any of it; see
[damagerecon/ACCURACY.md](damagerecon/ACCURACY.md) for why. The gl half
is regime-independent — a grenade deals nothing on touch, so its verdict
never reads a magnitude; it rests on the detonation point and on the
spent 2.5 s fuse, which makes a full-fuse flight a certain non-touch.

It is **not a subset of `hits`** and is not produced by the same join. `hits`
asks whether a FIRE connected and goes through the fire's tracked flight;
`directHits` counts the reconstruction's direct damage rows, because one
projectile touches at most one player and a touch therefore IS a row. Routing
it through the flight instead would discard every point-blank rocket whose
entity the server never broadcast — which KTX's counter does count — and it
measures four times worse for doing so (9.5% aggregate error against the
verbatim block, vs 1.2% for the row count). So `directHits > hits` is
possible and means exactly that.

The tier can only count what the reconstruction saw, so its completeness
is the damage section's: read [`damage.coverage`](#damagecoverage) (v74)
alongside it. On a low-coverage demo a `hits` figure — and any accuracy
derived from it — is a floor, not an estimate; the accuracy figures below
were measured on demos that carried the full evidence.

**Emission.** For two weapon families, and for every covered weapon the player
FIRED — whether or not the reconstruction credits him with any damage: a
shooter who appears nowhere in the reconstructed log gets `hits: 0` on each
covered weapon he fired, never an absent block (presence is keyed on the
section being reconstructed, not on this shooter being in it).

- **Same-frame** — `lg`, `sg`, `ssg` and `axe` (the last at its fixed +200 ms
  traceline delay): the fire and its damage share a server frame, and the join
  links them directly.
- **Flight-joined** (schema v74) — `rl` and `gl`: the join anchors on the
  fire's tracked projectile, not on the fire. `shots[].flightEnd` publishes
  when that projectile died, and the reconstructed damage of that
  attacker+weapon at the impact instant is what makes the fire a hit. This is
  the MEASURED counter's own definition — a fire whose projectile the server
  never broadcast is a miss on both sides — which is what makes the two
  comparable. Before v74 exported the association, the join could only count
  reconstructed impacts, a different question that ran 7.3 pp above the
  measured rl figure.

`ng`/`sng` never carry the block: nail flights are bracketed only when nail
decoding is requested, so the measured counter they would be validated against
is zero on every demo of the validation corpus.

**Under a time window** (`/aim?from=&to=`, which recomputes the tier) the
window scopes the FIRES only; the damage the join weighs them against stays
match-wide. It has to: a flight-joined fire's damage lands a whole flight after
it — up to the 2.5 s grenade fuse plus the stat-instant lag — so clipping the
evidence to the fire window would report a grenade thrown just before `to` as a
miss purely because of where the window was cut. The measured tier has no such
artefact (`shots[].hit` is linked before any window exists), and the two must
stay comparable. Damage from a fire outside the window is still unreachable
unless it falls inside an in-window fire's own join window, and each fire is
claimed at most once, so `recon.hits` can never exceed the windowed `shots`.

**What it does NOT carry, and why.** The reconstruction anchors damage at the
VICTIM's health/armor stat instant and merges every hit landing on one instant
into a single delta with one attacker and one summed magnitude. That is enough
to say a fire connected, and not enough for: the per-fire `hit` columns on
`crosshair`/`lgRamp` (a merged delta silently moves a hit between shooters, and
one misjoin is a visibly wrong dot on the heatmap); the SG/SSG pellet split
(`pelletHits` is Σ damage / 4 — a merged magnitude would credit one shooter
with another's pellets); the per-fire `direct`/`splash`/`missed` split (the aggregate
touch COUNT is carried, as `directHits` above — splitting each individual fire
three ways is a stronger claim, and the merge makes it unsupportable); the LG whiff geometry
`blocked`/`outOfRange`/`unresolved` (it classifies MISSES, and a miss here can
be a hit the join did not recover); and the enemy/team/self splits (the
weakest part of the reconstruction). Measured error of what IS carried, vs the
wire-measured counter on demos that have both: lg 0.3 pp, sg 1.3 pp, ssg
1.8 pp, axe 0.6 pp, rl 0.5 pp, gl 0.4 pp mean accuracy error — see
[damagerecon/ACCURACY.md](damagerecon/ACCURACY.md) §"Aim hit recovery" for the
withhold-and-compare method and the per-weapon table, and
`cmd/qw-aim-eval` for the harness. `directHits` is scored against a different
ground truth — the verbatim KTX block, since that is the only place the
convention appears — at 1.25% (rl, 46.5% of rows exact) and 3.55% (gl,
89.6% exact) aggregate over 632 / 424 archive player rows; see ACCURACY.md §"Can an old demo answer KTX's rl/gl
question?".

### WeaponAimSplit

One victim-class slice (enemy / team / self) of a weapon's hit counters —
same semantics as the `WeaponAim` fields of the same names, restricted to
that bucket's victims (`hits`, `pelletHits`, `full`, `partial`, `miss`, all
int omitempty, plus `direct`, `*int` omitempty like its top-level twin —
absent means the split never ran, not zero). A multi-victim fire counts in
every bucket it has a victim in.

**Emission rules** (consumers must match them): `team`/`self` appear iff the
weapon had ≥1 team-/self-victim hit; `enemy` appears iff `team` or `self`
does — i.e. iff it differs from the top-level counters, so consumers use
`w.enemy || w` for the enemy view and `w.team / w.self || zeros` for the
others. An all-zero `enemy` split is legitimate (every hit was FF/self).
Not split: `shots`, `pellets` and the LG miss classes
(`blocked`/`miss`/`outOfRange`/`unresolved`) — misses have no victim (the
miss heuristic targets enemies by construction; note the lg `miss` shares
its field with the pellet `miss`, which *is* split). Derivable per bucket:
`splash = hits − direct` (only where `direct` is present — an absent one
leaves the bucket's splash unanswerable, exactly as at the top level),
`missed = shots − hits`.

The SG/SSG per-fire split is exact per fire except when the per-fire pellet
clamp triggers (e.g. quad-multiplied damage), where the enemy/team
allocation within that fire is approximate. Self hits are always splash (a
missile cannot collide with its owner), so a `self` split's `direct` is a
certain `0` wherever the split ran — both touch handlers return on
`other == owner` (`ktx/src/weapons.c:954`, `:1317`) — and it never has pellet
counters (hitscan cannot self-hit).

## MessagesResult (`messages`)

Defined in `result/messages.go`.

| Field | JSON key | Type |
|---|---|---|
| Events | `messages` | []MatchEvent |

### MatchEvent

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Time | `time` | int32 | Match-relative ms. |
| Type | `type` | string | `"frag"`, `"chat"`, `"teamsay"`. |
| Player | `player` | string | Sender / killer. |
| Team | `team` | string | Sender's team. |
| Message | `message` | string | Q-normalised text **with** ezQuake markup intact (color codes `&cRGB`, sound triggers `!K`, macro delimiters `{}` `[]`). |
| MessageClean | `messageClean` | string (omitempty) | Same text with markup stripped (plain ASCII). Elided when identical to `message`. |
| Victim | `victim` | string (omitempty) | Frag-only. |
| Weapon | `weapon` | string (omitempty) | Frag-only. Same vocabulary as [FragEntry](#fragentry) `weapon` (`rl`/`lg`/…, env `lava`/`fall`/`water`/`slime`/`world`/`squish`, plus `teamkill` for phrasing-only teamkills — the obituary text names no weapon, so the real one is unrecoverable from the wire). |

Frag entries here overlap with `FragResult.Frags[]` — same time / killer
/ victim / weapon (both derived from the one obituary parser), plus the
obit text. Pick the one whose shape matches your consumer's needs; see
"Layered views" below.

## DemoInfoResult (`demoInfo`)

Defined in `result/demoinfo.go`. **Verbatim from KTX's STUFFCMD demoinfo
JSON**, with exactly one transformation: under the individual layout
(`match.sources.teams` = `individual` — every duel, every FFA) each
`players[].team` is rewritten to the player's own name and `teams` is
rebuilt as one entry per player, so this section agrees with every other
one instead of being the last place the decorative clan tags survive.
Consumers reading it as a name→team map (a loc heatmap, a map legend) get
the same answer here as from `match.players[]`; the raw userinfo tag is on
`match.players[].rawTeam`. Otherwise treat this as authoritative for
accuracy, damage breakdown, item pickups, bot info.

**Units island.** This section is the one deliberate exception to the
schema's time contract — KTX's numbers keep KTX's own units, not the
pipeline's match-relative int32 **ms**, and several are not timestamps
at all: `duration` is integer **seconds**; `timelimit` is **minutes**;
a per-player item entry is `{took, time}` where `took` is a **pickup
count** and `time` is the **cumulative seconds** the item was
held/controlled — neither is a match-clock offset, so there is nothing
to join against the ms timeline. For per-pickup timestamps use the
pipeline's own `items` phases (or `weaponPickups`); use this section
for KTX-authoritative totals.

Top-level fields (`version`, `date`, `map`, `hostname`, `ip`, `port`,
`mode`, `timelimit`, `fraglimit`, `deathmatch`, `teamplay`, `duration`,
`demo`, `teams`, `players`, `rawJson`) plus per-player nested objects:

`deathmatch` / `teamplay` are v75 — KTX's `dm` / `tp` keys
(`ktx/src/stats_json.c:492-502`), parsed since v1 and dropped on the floor
until then. KTX writes each key **only when the cvar is non-zero**, so 0 here
means "the server said zero, or this build wrote no key". For `teamplay` on a
KTX server those are the same statement: FixRules forces teamplay to 0
outside team/CTF/coop and to 2 inside them (`ktx/src/world.c:1674-1691`),
which makes `tp` the strongest input to
[`match.gameMode.teamBased`](#gamemode).

- `Stats` — `frags`, `deaths`, `tk`, `spawn-frags`, `kills`, `suicides`
- `Dmg` — `taken`, `given`, `team`, `self`, `team-weapons`, `enemy-weapons`, `taken-to-die`
- `Spree` — `max`, `quad`
- `Speed` — `max`, `avg`
- `Bot` — `skill`, `customised` (when player is a frogbot)
- `Weapons[k]` — per-weapon `Acc`, `Kills`, `Deaths`, `Pickups`, `Damage`
- `Items[k]` — `Took`, `Time`

For the full nested table, see `result/demoinfo.go` directly — every
field is documented inline.

## PlayerStatsResult (`playerStats`)

Defined in `result/player_stats.go`. Schema v63. Present on **every**
demo — it never depends on the KTX demoinfo block being there.

This is the canonical answer to "how did this player do". `demoInfo` is
KTX's own end-of-match accounting (authoritative for the server-side
counters the wire hides, absent on older demos); the stream / frag /
item artifacts are present everywhere but leave the join to the caller.
This section is that join, done once, plus the family neither source
carries: possession time.

**`/demoinfo` is unchanged and still verbatim.** It is the audit trail
this section is diffable against — use it when you want exactly KTX's
numbers.

### Provenance, and degrading rather than disappearing

The section keeps ONE SHAPE across demo ages. Where a wire-side
reconstruction is possible at all it is emitted and marked
`src: "derived"`, rather than the field vanishing on demos recorded
before KTX embedded its block — a response whose shape changes with the
demo's age forces every consumer into two code paths, and the old-demo
path is the one nobody tests.

The limit is honesty, not effort: a value that cannot be MEASURED stays
absent rather than becoming a zero. Hence `accuracy.byWeapon[].hits` is
omitted (not zeroed) whenever nothing could count it: there is no WIRE
damage stream to link fires against AND the aim recon tier recovered
nothing for that weapon (schema v74 — see below). KTX's `taken-to-die`
99999 no-deaths sentinel is likewise never served as a number.

Every stat FAMILY carries `src`: `"derived"` (this pipeline, from the
wire) or `"ktx"` (the demoinfo block). The **accuracy** family has a
third since schema v74 and the **damage** family two more:
`"derived:unbounded"` (see below) and, since schema v71,
`"reconstructed"` — the demo carried no wire damage stream at all and the
family aggregates the `damage-recon` node's reconstruction
(`damage.source: "reconstructed"`): per-player totals are ~1% estimates
rather than measurements, `enemyWeapons`/`byEnemyWeapon` may read a
withheld zero on frozen-weapon-bit recordings, and the full error tables
live in `damagerecon/ACCURACY.md`. That ~1% assumes the recording carried
the evidence — **read [`damage.coverage`](#damagecoverage) (v74) before
trusting a `reconstructed` damage row**: on the small class of demos whose
stat channel was barely broadcast the family is a fraction of the real
match, and this is the only field that says so. It is not restated here;
the family rides the same section. The stored artifact is always
fully derived; `view.PlayerStats` applies the KTX overlay at read time,
mirroring how `view.Damage` applies `boundedSource`.

`sources` at the top level is the roll-up, **computed from the rows the
response actually carries, after any filtering**: all rows agree → that
value, no row carries the family → the key is omitted, the rows disagree
→ `"mixed"`. Exception: `score` and `hold` are constants (`"derived"`)
whenever players are present — no overlay ever supplies either family.
`"mixed"` is a **canary**, not a data condition — on a demo
carrying a KTX block every roster row joins it, so a disagreement means
a roster row KTX has never heard of, i.e. the phantom-row defect. It
should never appear on healthy data. A **team row** whose members
disagree carries `"mixed"` as its family `src` under the same rule
(shared value or the canary — never a silent upgrade); a player row
never does.

| Family | Winner | Why |
|---|---|---|
| `score` | always **derived** | KTX over-counts pentagram-deflect telefrags (`dtTELE2`), credits world-dealt suicides to the world entity (`ktx/src/client.c:4951`), and resets after a reconnect. `match` carries the frag-log-corrected counts. The kill side is optional — see "The kill side of `score` is optional" below. |
| `damage` given / givenTeam / givenSelf / enemyWeapons | **ktx** when present, else the bounded family of the damage section (wire-derived, or the damage-recon reconstruction on pre-instrumentation demos — `src: "reconstructed"`) | server-side accounting; same rules as `damage.boundedSource`. |
| `damage.taken` | always **derived** (all sources) | KTX's `dmg.taken` is enemy-only (`ktx/src/combat.c:1069`). It is surfaced separately as `takenEnemy` so the two are never conflated. Only the per-hit reconstruction measures it, so it is **absent** (not zero) on a demo carrying a KTX block but no damage stream. |
| `accuracy` | **ktx** when present, else **derived** from the fire stream — `src: "reconstructed"` when its `hits` came from the aim recon tier (v74) | `src` is the evidence GRADE; what the numbers COUNT is the per-weapon `hitsConvention`, and since v75 every source aims at KTX's own convention (two conditional rows named below). Emitted anyway because a demo with no KTX block should degrade to a rougher number, not to a missing field. The KTX block replaces the derived one **wholesale**, never per weapon: see below. |
| `score.maxSpree` / `maxQuadSpree` | always **derived** (v74) | the kill-streak maxima KTX writes as `spree.max` / `spree.quad`. Derived by replaying KTX's own state machine over the corrected frag log, so it inherits `score.kills`' evidence and its absence. Never overlaid, for the reason the whole family is not — plus one of its own: KTX's increment gate is `strneq(attackerteam, targteam) \|\| !tp_num()` (`ktx/src/client.c:4865`), so wherever teamplay is off a player's own SUICIDE bumps their streak in the very call that latches it. See "The derived spree" below for the measured residual. |
| `damage.takenEnemy` / `takenToDie` | **ktx** when present, else **derived** from the per-hit log | enemy-only hits summed per victim; `takenEnemy / deaths` for the average, matching `ktx/src/stats_json.c:357`. KTX's 99999 no-deaths sentinel is never served as a number. |
| `damage.teamWeapons` | **ktx-only** | KTX's `dmg_tweapon` (`ktx/src/combat.c:1063`), the friendly-fire mirror of `enemyWeapons`. The reconstruction does not bucket team damage by the victim's inventory. |
| `damage.byWeapon` | **ktx** per weapon when present, else the bounded reconstruction | enemy damage GIVEN split by attacker weapon. Merged weapon by weapon, on **presence** of KTX's `damage` sub-block, not on its being non-zero: KTX emits a weapon entry whenever the weapon was used (`ktx/src/stats_json.c:382`) and a `damage` sub-block whenever either counter moved (`:208`), so `enemy: 0` there is a measured zero — a weapon used for team splash only. The reconstruction survives for keys outside KTX's vocabulary (`unknown`, `stomp`, `tele`, `squish`, `explobox` — the full vocabulary is `DeathTypeToWeapon`, `mvd-reader/mvd/types.go:286`), which are real measured damage. |
| `damage.byWeaponTeam` | **ktx** per weapon when present, else the bounded reconstruction | TEAM damage GIVEN split by attacker weapon (schema v63). KTX writes `enemy` and `team` in the SAME `damage` sub-block (`ktx/src/stats_json.c:208-212`), so it is merged on exactly the presence rule `byWeapon` uses, and `team: 0` beside a non-zero `enemy` is a measured zero. **Measured whenever the `damage` family is present** — see below. |
| `damage.byWeaponSelf` | always **derived** | SELF damage GIVEN split by attacker weapon (schema v63). KTX records no per-weapon self counter, so this one has no overlay and is **measured only where a damage stream was read** — which is exactly what a non-nil `damage.taken` says. Absent on a KTX-block-without-stream demo whatever the player did. |
| `score.byWeapon` | always **derived** | enemy kills split by weapon, from the corrected frag log — same footing as `score.kills`, which is why KTX's per-weapon counts (subject to the same reconnect / telefrag over-counting) never overlay it. Its key set is the *obituary* vocabulary, not `DeathTypeToWeapon`'s: beyond rl/lg/gl/sg/ssg/sng/ng/axe it can carry `tele`, `stomp`, `squish`, `unknown` and (on mods that have them) `hook`/`rail`. `tele` is in the committed corpus. Iterate the map. |
| `login` | **ktx** when present, else the `*auth` userinfo login | genuinely on the wire (`mvd-reader/parser/userinfo.go:177` in `parseSetInfo`, `:229` in `parseUserInfoString`). |
| `controlMs` / `speed` | **ktx-only** | KTX's own control clock and speed summary; no wire-side equivalent is computed today. |
| `score.byEnemyWeapon` | always **derived** | the victim-weapon split of the same kills `score.byWeapon` splits by killer weapon, classified from the victim's possession streams. Never overlaid: KTX's `ekills` counts the same events but keys them INCLUSIVELY (a victim holding RL+LG bumps both) and is force-zeroed for axe/sg and for every weapon on `deathmatch >= 4` / `k_instagib` (`ktx/src/stats_json.c:377-380`), so its zeros are not readings. Ours reproduces it exactly where KTX measures honestly — `rl + both` equals `ekills.rl` on all 44 cached demos, every player. |
| `score.byWeaponVsEnemyWeapon` | always **derived** | the joint distribution the two by-weapon kill maps are marginals of (killer weapon → victim bucket). Summing it reproduces both, by construction. |
| `damage.byEnemyWeapon` | always **derived** | finer than the server's own: KTX keeps one `dmg_eweapon` scalar lumping RL and LG together (`ktx/src/combat.c:1075`), so there is nothing to merge. Needs the damage stream — see the measuredness rules below. |
| `ping` / `handicap` / `bot` | **ktx-only** | server-side state with no wire signal. (`svc_updateping` does carry ping, but the parser skips it today — `mvd-reader/parser/parser.go:809`, in `skipCommand`.) |
| `pickups` took / totalTook / dropped | **ktx** when present, else derived from `items` + `weaponPickups` + `backpacks` | direct server-side counters, identical semantics. |
| `pickups` xfer / xferSelf | always **derived** | we can decompose what KTX conflates — see below. |
| `hold` | always **derived** | KTX has no weapon hold time in the block at all, and its armor hold time overcounts — see below. |

### Hold time: why ours differs from KTX's

KTX tracks weapon hold time internally (`ps.wpn[].time`) but
`json_weap_detail` never writes it to the demoinfo block
(`ktx/src/stats_json.c:132-217` emits acc/kills/deaths/pickups/damage
only); it reaches just the end-of-match text tables
(`ktx/src/statsTables.c:390`). **No demo of any age carries weapon hold
time.**

Armor hold time *is* in the block, and it overcounts. KTX opens the
clock at pickup and closes it only on death or on picking up a
different armor type (`ktx/src/items.c:505-522`,
`ktx/src/client.c:4600`) — never when the armor is chewed to zero by
damage. Our `armorType` stream goes to `""` when the item bits clear,
so our integral is exact and reads **lower**. Measured on gameId 212423
(1on1, dm2): KTX `ra` 213 s vs ours 129 s for one player, 317 s vs
266 s for the other. That gap is the correction, not a bug — expect a
KTX end-of-match table to disagree, and expect ours to be the one that
matches how long the player actually had armor on.

Powerup hold time is correct in KTX (its powerup clocks *do* close on
expiry, `ktx/src/client.c:3823/3868/3920`); we derive it anyway so it
stays consistent with the timeline's powerup runs.

Ours agrees with KTX's to the second on 96.0% of quad rows, 94.9% of
ring and **82.5% of pent** across the 188-demo eval population. The pent
row is the worst and the reason is arithmetic, not measurement — **every
mismatch is exactly −1 s, never +1**, on all three powerups:

- our possession interval is read off the demo-frame grid at both ends
  (~34 ms at the default `sv_demofps 30`), so a run can measure up to
  one frame short of its true length;
- both sides then truncate to whole seconds — KTX casts its float
  (`ktx/src/stats_json.c` `json_item_detail`), and the comparison floors
  ours to match — which turns that sub-frame shortfall into a whole lost
  second **exactly when the true length is an integer number of
  seconds**;
- an expiry-ended run is exactly that: 30.000 s. A death-ended one stops
  at an arbitrary fraction, where 34 ms almost never crosses a boundary.

Pent holders essentially do not die: KTX's 96 pent takes on that
population sum to exactly 96 × 30 s, i.e. **not one pent run ended
early**, so every take is exposed to the boundary (14 of 96 lost the
second, 14.6%). Quad runs are cut short constantly (mean 20.0 s per
take) and lose it on 1.4%; ring, at 21.7 s, on 4.1%. So a pent number
one second under KTX's is the expected reading, and the bias is
one-directional — never read ours as the higher of the two.

### Shape

```jsonc
{
  "players": [ PlayerStatsRow, … ],   // Streams.Players order + any scoreboard-only player
  "teams":   [ PlayerStatsRow, … ],   // omitted on duels / FFA
  "sources": { "score": "derived", "damage": "ktx", "hold": "derived", … }
}
```

### PlayerStatsRow

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Name | `name` | string | Player name; on a team row, the team name. |
| Team | `team` | string | Omitted on team rows. |
| Identity | `identity` | string (omitempty) | The reconnect-unification key, copied from this player's stream (schema v66): two rows with the same value are one human. **Demo-local — never persist or cross-demo compare it.** Absent on TEAM rows (an aggregate is not a connection) and on a scoreboard-only row (in the KTX block, produced no stream, so there is no occupancy to attribute). See [Player identity and sessions](#player-identity-and-sessions-schema-v66). |
| Sessions | `sessions` | []PlayerSession (omitempty) | The wire occupancies behind the row — slot, userid and window per connection — copied from the stream. Same absences as `identity`. See [PlayerSession](#playersession-streamsplayerssessions). |
| Ping / Handicap / Bot | `ping`, `handicap`, `bot` | | **KTX-only** identity fields, absent without a demoinfo block. `ping` is a pointer, so a KTX-measured 0 would survive as a reading (unreachable in practice — ping's floor is one frame). |
| Login | `login` | string | The player's authenticated login: KTX's when present, else the `*auth` userinfo key off the wire. |
| ControlMs | `controlMs` | int32 ms | **KTX-only**: KTX's own map-control clock (it writes float seconds; converted to ms here). Not the same measure as the region-control view. |
| Speed | `speed` | *PlayerStatsSpeed | **KTX-only**: `max` / `avg` in Quake units/second. The position streams could support a derived version — a follow-up. |
| Members | `members` | *int | TEAM rows only, and **always present** there (including 0): how many players were folded in, and the count `shareMatch`'s denominator rests on. Absent on player rows. |
| Window | `window` | PlayerStatsWindow | The denominators — see below. |
| Score | `score` | PlayerStatsScore | `frags` (svc_updatefrags net score) and `deaths` always; `kills`, `suicides`, `teamKills`, `efficiency`, `byWeapon`, `byEnemyWeapon`, `byWeaponVsEnemyWeapon`, `maxSpree` and `maxQuadSpree` **optional together** — see below. |
| Damage | `damage` | *PlayerStatsDamage | Omitted when the demo carries no damage information at all. A player who neither dealt nor took a point of damage on a demo that **does** carry the stream gets a **zeroed** family — an observed zero, not an unmeasurable one. |
| Accuracy | `accuracy` | *PlayerStatsAccuracy | `byWeapon` map, keyed `axe`/`sg`/`ssg`/`ng`/`sng`/`gl`/`rl`/`lg` (KTX counts axe swings; the derived path emits whatever the fire stream decoded). `attacks` is PELLETS exactly where `hitsConvention` is `pellets` (KTX's unit, 6/14 per fire — KTX's own sg/ssg rows, and since v75 a WIRE-LINKED `derived` sg/ssg row) and TRIGGER PULLS on every other row, a `reconstructed` sg/ssg one included — including every `directImpact` row, where `hits` is a per-projectile touch count against a trigger-pull `attacks`, exactly as KTX's own rl/gl row is; `hits` is **absent** — not zero — when nothing could count it: no WIRE damage stream to link fires against and no recovery from the aim recon tier for that weapon (`src: "reconstructed"`, v74 — ng/sng always, since the tier validated no nail recovery), or (v75) a `derived` ng/sng row on a parse without nail decoding, the only way a nail fire can be linked to its damage at all; **`hitsConvention`** (v74) names what `hits` counts and is present whenever it is — see below; `real`/`virtual` are KTX-only and **not** a split of `hits` — see below. |
| Pickups | `pickups` | *PlayerStatsPickups | `byKind` map — see vocabulary below. |
| Hold | `hold` | PlayerStatsHold | `weapons` / `armor` / `powerups` maps of HoldStat. |

#### `accuracy.byWeapon[].hitsConvention` — what the number counts (v74)

`src` is the evidence **grade**. It says nothing about the **question**,
and the two come apart badly: a single `src: "ktx"` row counts three
different things at once, one per weapon. `hitsConvention` is the
machine-readable answer, present on every weapon that carries `hits`.

| value | what `hits` counts | where it appears |
|---|---|---|
| `anyDamage` | one **fire that landed damage by any path**, splash included | `lg`/`ng`/`sng`/`axe` on every source — KTX's own counter for those four, whose single damage path makes "any" and "direct" the same event — plus `sg`/`ssg` on a `reconstructed` family, where the pellet split cannot be recovered (`result.WeaponAimRecon`), and `gl` on a `derived` one **parsed without the spatial shot streams**, the touch classifier's own input (below) |
| `directImpact` | the projectile **touched a player** (`ktx/src/weapons.c:994` `T_MissileTouch`, `:1331` `GrenadeTouch`) — a rocket that killed by splash alone is not a hit | KTX's `rl` / `gl`; (v74) the `rl` / `gl` of a `reconstructed` family; (v75) the `rl` and `gl` of a `derived` one |
| `pellets` | **pellets**, on BOTH sides of the ratio: `attacks += bullets` (`:812` for the shotgun's 6, `:869` for the super shotgun's 14) and one `hits++` per pellet that connected (`:387`, `:392`) | KTX's `sg` / `ssg`, and (v75) the `sg` / `ssg` of a `derived` family |

**Since v75 a `derived` row answers KTX's question too** (schema v74 did
it for `reconstructed`). The wire-linked tier no longer publishes a
uniform any-path count: it reads the aim section's own measured counters
— `pellets` / `pelletHits` for the shotguns, `direct` for `rl` and `gl` —
so a row here and a row lifted from a demoinfo block count the same event.
Read, not recomputed: `analyzer.deriveMeasuredAcc` lifts the published
aim figures the way `deriveReconHits` lifts the recon tier, so the two
sections cannot disagree. Withheld and unresolved fires still count as
attacks (`pellets` is `shots × 6/14` over every fire), which is what
keeps `attacks` on the same footing as KTX's `attacks += bullets`.

`gl`'s number is the one that is not read off the wire at all, and the
two conditional rows are named rather than hidden:

- **`gl` is `directImpact` where the touch could be CLASSIFIED, and
  `anyDamage` where it could not.** KTX counts a grenade that TOUCHED a
  player (`GrenadeTouch`, `ktx/src/weapons.c:1331`), but the touch
  immediately detonates the grenade and `T_RadiusDamage` flags every
  resulting row as splash (`ktx/src/combat.c:1207`) — so the wire damage
  log holds no record of a grenade touch at all, and counting non-splash
  `gl` rows off it reproduces **0.00%** of the block's total (30.0% of
  424 archive rows exact, all of them players who touched nobody). The
  touch is therefore re-derived from the grenade's broadcast flight, its
  detonation point against the victim's hull, and the 2.5 s fuse — the
  same era-independent classifier a `reconstructed` row uses
  (`damagerecon/direct.go`) — which scores **92.0% of 424 archive rows
  exact at 3.79% aggregate**, above the reconstructed tier's own 89.6% /
  3.55%. That classifier reads the spatial shot streams
  (`-include projectiles,beams`; mvd-api and the WASM build always
  request them), so on a parse without them the row falls back to the
  any-path count and says so.
- **`ng`/`sng` `hits` need nail decoding** (`-include nails`; mvd-api and
  the WASM build always request it). Without it no nail fire links to
  anything and the field is **withheld**, not zeroed.

**Two rows are comparable exactly when their weapon and their
`hitsConvention` match.** Gate cross-demo aggregation on this, not on
`src`. Ignoring it is not a rounding error: on the 186-demo eval
population the any-path rl count runs **~4.5× above** KTX's direct-impact
one, and the pre-v75 shotgun rows sat 77-93% away from the block on both
`attacks` and `hits`. Since a demo with a KTX block serves the block's
number and one without serves ours, a consumer plotting `hits/attacks`
per demo across a `hitsConvention` change draws a cliff that is entirely
a change of definition.

`hitsConvention` is **absent beside a present `hits`** in exactly one
place: a TEAM row whose members disagreed, which is the phantom-roster
condition `src: "mixed"` already reports. There the summed count spans
two scales and no single convention describes it.

**Why a `reconstructed` row can answer KTX's rl/gl question (v74).**
On a demo that carries the wire damage log, a non-splash `rl` record IS
the direct touch — `dmg_is_splash` is raised only inside
`T_RadiusDamage` (`ktx/src/combat.c:1207-1227`) — and counting those
reproduces KTX's `acc.rl.hits` on **638 of 638 player rows, 0.00%
aggregate**. The pre-instrumentation half carries no such flag, so the
flag itself has to be reconstructed, and the same count then applies.

The first attempt at that — explosion endpoint within 48 units of the
victim — answered the question for `gl`, whose grenades are
contact-fused (71.7% row-exact, 1.22% aggregate), and **not** for `rl`:
10.0% row-exact, **+80% aggregate**, because a rocket detonating on the
wall beside a player is endpoint-near without ever having touched them.
v74 replaces it with two engine-derived signals (`damagerecon/direct.go`):
the flight's own TRAJECTORY, extended past the detonation, against the
victim's 32×32×56 hull; and the MAGNITUDE, since a direct rocket deals a
flat 110 and takes no splash on top of it, while splash is 120 − 0.5·dist.
Withhold-and-compare against the verbatim block on the same 186 archive
demos: `rl` **46.5% row-exact / 1.25% aggregate** (bias −0.13 per row,
p90 error 2) and `gl` **89.6% / 3.55%** — both at or under a hit-and-a-bit
of per-row error, against the shipped `lg` row's −0.91. So both projectile weapons publish
`directImpact` on a reconstructed row, and the map holds one convention
per weapon exactly as a KTX row does.

**What v75 measured (186 archive demos, withhold-and-compare, the
`/measured` columns of `cmd/qw-demoinfo-eval`).** Every row below is the
WIRE-LINKED family scored against the same demo's verbatim block:

| row | before v75 | after v75 |
|---|---|---|
| `sg.attacks` | 0.0% exact / 83.33% | **100.0% / 0.00%** |
| `ssg.attacks` | 0.0% / 92.86% | **100.0% / 0.00%** |
| `sg.hits` | 6.0% / 76.95% | **100.0% / 0.00%** |
| `ssg.hits` | 5.9% / 85.62% | **100.0% / 0.00%** |
| `rl.hits` | 1.6% / 355.17% | **99.8% / 0.02%** |
| `gl.hits` | 42.9% / 55.13% | unchanged (see above) |
| `lg.hits` / `axe.hits` | 99.3% / 0.00%, 100.0% / 0.00% | unchanged |
| `ng.hits` / `sng.hits` (with nails) | — | 65.5% / 32.11%, 48.5% / 22.81% |

`sg`/`ssg` `hits` are an ESTIMATE that happens to land exactly: aim sizes a
fire's pellet hits from the magnitude of its same-frame damage, `Σ / 4`,
clamped at the fire's 6/14. That first shipped with a flat divisor and left
a residual which was **entirely quad** and one-sided (65.9% / 1.03% on sg,
75.9% / 6.75% on ssg, bias +4.13 / +6.85, no row anywhere under-counting) —
`T_Damage` multiplies the attacker's damage by 4 while the quad runs
(`ktx/src/combat.c:540-546`), so a quad pellet's 16 read as four pellets and
a two-pellet quad fire saturated the clamp. Dividing by 16 while the SHOOTER
holds the quad at fire time closes it: 534 of 534 sg and 390 of 390 ssg rows
now reproduce the block to the unit. Two per-fire pellet counts remain
unmodelled and are stated, not guarded — `k_instagib`'s single-slug sg
(`ktx/src/weapons.c:806-810`) and `k_yawnmode`'s 21-pellet ssg (`:858`);
neither mode appears in the measured population.

`ng`/`sng` are on KTX's scale (one nail connecting is one hit on both
sides — `ktx/src/weapons.c:1549`, `:1620`, one spike per `attacks++`) but
the nail linker only RECOVERS 68-77% of them, so those rows carry a real
under-count rather than a definition gap. Full tables:
`damagerecon/ACCURACY.md`.

#### `accuracy.real` / `accuracy.virtual` are not a hit split

KTX's `rhits` / `vhits` (`ktx/src/combat.c:1085,1100`) exist on **rl and
gl only** and count **victims damaged by a blast**, not rockets that hit:
one rocket splashing three players adds three. They therefore routinely
EXCEED `hits`, which for rl/gl is the *direct-impact* count — the rocket
entity touching a player (`ktx/src/weapons.c:994 for rl, :1329 for gl`). A 2022 dm3 demo in
the corpus reads `rl: {attacks: 110, hits: 13, real: 55, virtual: 55}`;
that is not a contradiction, it is three different counters.

`real` counts victims who actually lost health or armor. `virtual`
counts victims who *would* have, latched before godmode / pentagram /
teamplay damage-avoidance zeroed the damage (`virtual_take`,
`ktx/src/combat.c:719`) — so `virtual >= real`, and the gap is damage
that was *prevented*, not missed. Neither divided by `attacks` is an
accuracy.

#### `damage.src` is three-valued: the unbounded degradation

The damage family means **bounded** numbers — KTX scoreboard semantics,
armor absorbed and health capped to remaining — because that is what the
KTX figures it merges with mean. On a `k_midair` / `k_instagib` /
`k_dmgfrags` demo there is no bounded family to serve: those modes
rewrite `T_Damage`'s take in ways the wire does not expose, so
`analyzer/damage.go` skips the reconstruction entirely
(`damage.boundedMode` reads `skipped:*`) and the numbers fall back to
**raw wire damage including overkill**.

`src` says so: `"derived:unbounded"`. Magnitude, measured on
`4on4_oeks_vs_tsq[dm2]` (raw vs bounded): `muttan` given 19640 vs 13641
(+44%), `tco` taken 25062 vs 18113 (+38%). On `k_instagib` the wire
value is a flat 5000 per hit. **Never compare an unbounded row's damage
with a bounded one's.** The value is demo-global (it keys on
`boundedMode`, the cause), so team rows and the `sources` roll-up carry
it too, and a team is never split across two values.

No demo in the test corpus carries those cvars, which is why this path
went unmarked.

#### The three per-weapon damage maps and what an absence means

`damage.byWeapon`, `damage.byWeaponTeam` and `damage.byWeaponSelf`
(schema v63) split `given`, `givenTeam` and `givenSelf` by the
**attacker's** weapon, with the same keys and the same telefrag/stomp
exclusion. Never infer measuredness from `omitempty` — there are exactly
two rules, and a consumer implements only these:

1. **`byWeapon` and `byWeaponTeam` are measured whenever the `damage`
   family is present.** `src: "ktx"` means the KTX block existed, and
   KTX counts per-weapon enemy AND team damage for every weapon entry
   (one sub-block carries both, `ktx/src/stats_json.c:208-212`, omitted
   only when both are zero). `src: "derived"` / `"derived:unbounded"`
   means a damage stream was read.
2. **`byWeaponSelf` is measured only when a damage stream exists**, which
   is exactly what a non-nil `damage.taken` says. KTX has no per-weapon
   self counter, so on a demo with a KTX block but no damage stream the
   family is present and this map is genuinely unmeasured.

Within a measured family, an absent KEY means "dealt none with that
weapon". The derived copy drops zeros; KTX keeps an explicit measured 0
where the weapon's `damage` sub-block exists and omits the sub-block
when both counters are zero — so key-level absence is zero-or-never
either way, and never distinguishable. A UI summing the three splits
(an "all victims" figure) must therefore check rule 2 and label the sum
a lower bound when `taken` is absent, rather than treating the missing
self split as zero.

The two rules are **per-player**. TEAM rows follow the team-row
convention set in schema v62: optional figures are partial sums over the
members that measured them (`sumOptional` — "a team figure reads as
measured if any member's was"), and the new maps sum the same way. On a
team mixing a KTX-only member with a stream-derived one, the team's
`byWeaponSelf` therefore under-counts `givenSelf` by the KTX-only
member's share; `src: "mixed"` on the family is the canary, and such
teams only arise when the roster invariant is already broken.

#### The victim-weapon axis: `byEnemyWeapon` (schema v69)

Every per-weapon figure above this point is keyed on the **attacker's**
weapon: `score.byWeapon` is kills made *with* the RL, `damage.byWeapon` is
damage dealt *with* it. `byEnemyWeapon` is the other axis — the same kills
and the same damage, split by what the **victim** was holding at the moment
it landed. It answers weapon denial: *how many armed enemies did this player
take out, and how much damage went into them.*

Both maps use one vocabulary, and the buckets are **mutually exclusive**,
applied in priority order:

| Key | The victim held |
|---|---|
| `both` | RL **and** LG — the fully armed enemy |
| `rl` | RL, not LG |
| `lg` | LG, not RL |
| `mid` | ssg / sng / gl, but neither RL nor LG |
| `sg` | nothing above the shotgun tier (axe / sg / ng) — the respawn loadout |
| `unknown` | kills side only: the victim produced no possession stream, so their loadout is not knowable |

**They are a PARTITION**, which is what the `both` bucket is for — and the
one thing a consumer must not get wrong:

> **"Enemy RLs killed" is `rl + both`, never `rl` alone.** Same for LG.
> The two overlap in `both` by construction, so `enemyRL + enemyLG`
> double-counts the fully armed victims — that is a real quantity
> (`both`), not an error, but it is not a sum of kills.

**What each one sums to** differs, and only one of the two is exact:

- `score.byEnemyWeapon` sums to `score.kills` **exactly**, always. The score
  family is never overlaid from KTX, so both sides come from the one frag log.
- `damage.byEnemyWeapon` sums to the enemy damage **this pipeline
  reconstructed**. On a `src: "derived"` row that is `damage.given` exactly.
  On a **`src: "ktx"` row it is not**: `given` becomes KTX's own `dmg.given`
  counter while the split stays the reconstruction's, because KTX records no
  per-tier equivalent to merge in. Measured across the cached corpus (82 KTX
  rows) 66 carried a residual, the largest **16 damage**, 208 in total against
  659,577 given — **0.03%**. Don't compute a share-of-`given` from these
  buckets and expect exactly 100% on a KTX row.

The vocabulary is deliberately coarser than the killer-weapon one. It
describes *how well armed the target was*, where ssg/sng/gl are one tier
and the floor every player respawns with is another; a finer split would
imply a precision the victim's inventory does not carry, since it says what
they held, not what they would have used.

**Both maps are derived, and neither is ever overlaid from KTX.** The
server counts the kill side itself (`ekills`, `ktx/src/client.c:4703-4741`)
but keys it INCLUSIVELY — a victim holding RL+LG bumps both counters — and
force-zeroes the axe and sg buckets plus **every** bucket on
`deathmatch >= 4` and `k_instagib` (`ktx/src/stats_json.c:377-380`), so an
all-zero KTX block is a suppressed counter rather than a reading. Ours is
computed from the frag log against the victim's possession streams, which
makes it available on every demo carrying streams instead of only on those
with a demoinfo block, and lets telefrags and stomps be classified too. It
reproduces KTX exactly where KTX measures honestly:

```
ktx.ekills.rl == byEnemyWeapon.rl + byEnemyWeapon.both
ktx.ekills.lg == byEnemyWeapon.lg + byEnemyWeapon.both
```

measured across all 44 demos in the local cache — every player, both
weapons, no disagreement — and pinned by `checkEnemyWeaponKillsVsKTX`, which
asserts the identity on every golden-corpus demo. The verbatim KTX counters
remain on `demoInfo` for anyone who wants to diff them.

**Measuredness differs between the two maps**, because they rest on
different evidence:

- `score.byEnemyWeapon` is part of the **kill family** and absent exactly
  when `score.kills` is — it is a re-cut of those very kills.
- `damage.byEnemyWeapon` needs the **damage stream**, so it follows rule 2
  above: present exactly when `damage.taken` is, and absent on a demo with
  a KTX block but no stream. `damage.enemyWeapons` (KTX's `dmg_eweapon`)
  survives there and is exactly `lg + rl + both` when both are present.

The damage side excludes telefrags and stomps like every other figure in
that family — positional kills, not weapon damage. The kill side does
**not**: it has a victim and a timestamp, which is all the classification
needs. Team rows sum both maps over the members that measured one.

##### The cross-tab: `score.byWeaponVsEnemyWeapon`

`byWeapon` and `byEnemyWeapon` are two **marginals of the same kill set**,
and marginals cannot answer "how many of my LG kills were against enemies
carrying an RL" — the question that separates a weapon winning fights from
one finishing off the disarmed. `score.byWeaponVsEnemyWeapon` is the joint
distribution: **killer weapon → victim bucket → kills**, outer key from
`byWeapon`'s obituary vocabulary, inner key from `byEnemyWeapon`'s.

Measured on `2on2_pys_wd_250426_aerowalk`, one player's 45 kills:

| | both | rl | lg | mid | sg | **total** |
|---|---|---|---|---|---|---|
| `sg` | 1 | 2 | 1 | 3 | 9 | **16** |
| `rl` | 0 | 5 | 3 | 1 | 4 | **13** |
| `ssg` | 0 | 0 | 0 | 0 | 5 | **5** |
| `lg` | 0 | 1 | 1 | 1 | 1 | **4** |
| `sng` | 0 | 0 | 0 | 0 | 4 | **4** |
| `tele` | 0 | 1 | 0 | 0 | 1 | **2** |
| `ng` | 0 | 0 | 0 | 0 | 1 | **1** |
| **total** | **1** | **9** | **5** | **5** | **25** | **45** |

The row totals are `byWeapon`, the column totals are `byEnemyWeapon`, and
that is a **guaranteed identity**, not a coincidence:

```
sum over inner keys  ==  score.byWeapon
sum over outer keys  ==  score.byEnemyWeapon
```

The marginal is computed *from* the cross-tab rather than tallied
separately, so the two cannot drift; both identities are asserted on every
golden-corpus demo (`checkEnemyWeaponKillsVsKTX`) and were measured to hold
on all 44 cached demos, player and team rows alike. A consumer therefore
never has to decide which of the three maps to trust.

Same measuredness as its marginals (absent exactly when `kills` is) and the
same zero-dropping — an outer key exists only if the player killed with
that weapon, an inner key only if that pairing occurred. It is the widest
map in the section (up to *weapons used* x 6), so it is the one to skip
when only a scoreboard is needed; the marginals cost nothing and answer
most questions.

#### Why `accuracy` is swapped wholesale and `damage.byWeapon` is merged

The two overlays resolve differently on purpose. Damage is the **same
unit** in both sources, so merging key by key loses nothing and recovers
the keys KTX has no vocabulary for. Accuracy is **not always** the same
unit, even after v75 put both computed tiers on KTX's convention where
their evidence allows: a `reconstructed` sg/ssg row still counts trigger
pulls against KTX's pellets (`ktx/src/weapons.c:812`), and a `derived`
gl row still counts any damage path against KTX's touches. A per-weapon
merge would put two scales in one map under one `src` — the coercion
this section exists to prevent.

The swap is only lossy if KTX omits a weapon the reconstruction saw
fired. Measured across all 42 cached corpus demos (every one carrying a
KTX block), 228 rows with a derived accuracy family: **zero** such
weapons. That matches KTX's own emission rule — a weapon entry exists
whenever the player used the weapon at all — so the loss is theoretical
and no per-entry `src` is offered.

#### The kill side of `score` is optional

The two halves of this family do not rest on the same evidence. `frags`
is the `svc_updatefrags` net score and `deaths` is counted from the
protocol death events — both measured on every demo. `kills`,
`suicides`, `teamKills`, `efficiency`, `byWeapon`, `byEnemyWeapon`,
`byWeaponVsEnemyWeapon`, `maxSpree` and `maxQuadSpree` are all
attributed from the **obituary-derived frag log**, and some servers
never emit obituaries this pipeline can match.

Where the frag log is empty on a demo whose players demonstrably died,
**every one of them is absent together** — test for `kills` and read its
absence as covering the whole list. Serving `kills: 0` and
`efficiency: 0` beside 92 real deaths is byte-indistinguishable from a
genuinely awful player. `4on4_l_vs_la[e1m2]` was the demo that forced
this — a full 4v4 scoreboard with 230 team frags and not one frag-log
entry — though it is no longer an instance: the 2026-07-26 `svc_print`
line-assembly fix recovered its 368 obituaries, and no demo in the local
corpora currently trips the condition. The rule stays because the
*cause* does: a server whose obituary phrasing this pipeline cannot
match still produces exactly that scoreboard. Render `-`, not `0`. The
condition is **demo-global**, so every row on a demo agrees and a team
row can never mix a measured member with an unmeasured one; since v65 it
is decided once and published as
[`frags.killsMeasured`](#killsmeasured--the-one-measuredness-bit-of-this-section).

**`efficiency` is a RATIO in [0,1]**, not a percentage —
`kills / (kills + deaths)`, 0 when the player neither killed nor died.
So are `shareAlive` / `shareMatch`. All three serialize at 4-decimal
precision (`result.Share`, mirroring `Coord`).

#### The derived spree: `score.maxSpree` / `score.maxQuadSpree` (v74)

The KTX demoinfo block's `spree` object is one of the things **54% of
the archive cannot answer** — it is the pre-`ktxstats` half, and that
ceiling is permanent. These two fields are its derived equivalent,
computed on every demo from the artifacts every demo carries.

`maxSpree` is the longest run of kills between deaths; `maxQuadSpree`
the longest run made while holding the quad. They replay KTX's own state
machine (`ktx/src/client.c:4864-4876`, `items.c:2180-2181`,
`stats.c:1637-1638`): a counter per player, latched into the maximum and
reset when the player **dies**, with the quad counter additionally
latched and reset on a **fresh quad pickup** — so two consecutive quad
runs never merge into one streak. A run still alive at match end is
latched too, which is the whole point: the winning streak is the one
nobody wants to lose.

The whole machine runs inside `ClientObituary`, so the **frag log's own
order** is the state machine's order and the replay follows it: when two
obituaries share a millisecond, each is applied in full (increment the
attacker, then latch the target) before the next. That is the case a
mutual frag creates — B is killed while on a streak and B's already-
airborne rocket kills A in the same frame — and it is not hypothetical:
55 such pairs over the first 500 archive demos, in 43 of them. Ranking
every kill at an instant ahead of every death instead credited B's
posthumous rocket to the run that was already over.

They ride `score.kills`' evidence exactly — same increment predicate
(enemy kills with a named killer), same `killsMeasured` gate, so they
are present and absent with it, and a `0` inside a present family is an
observed zero. Team rows carry the **best any member ran**, never a sum
(the max-over-members rule `hold.*.longestMs` follows).

**One deliberate divergence from KTX**, and it is measured, not argued.
KTX's increment gate is `strneq(attackerteam, targteam) || !tp_num()`,
so wherever teamplay is off — every duel, every FFA — a player's own
**suicide** increments their streak in the very call that latches it,
because attacker and target are the same edict. Ours counts only the
kills `score.kills` counts. Scored against the verbatim block on **188
instrumented archive demos, 665 player rows** (`cmd/qw-demoinfo-eval`):

| population | `maxSpree` exact | `maxQuadSpree` exact |
|---|---|---|
| all rows | 92.9% (3.1% aggregate low) | 99.8% (0.3% low) |
| rows where `kills` already agrees with KTX | 96.5% | — |
| …and the player never suicided | **99.6%** (252/253) | — |

Of the 22 mismatches on rows where `kills` agrees, **21 belong to
players with at least one suicide** and **all 22 are off by exactly
−1** — the divergence above, in full. That it is the *definition* and
not the derivation is measured directly: the harness carries a second
column replaying KTX's own gate (`spree.max/ktxConvention`), and on all
22 of these rows that column reproduces the block **exactly** (98.8% of
all 665 rows).

The remaining large residuals — **17 rows at |Δ| ≥ 3** — are a different
thing entirely, and they have an **observable signature**. On 16 of the
17 the frag log had already credited the player **0 kills against KTX's
8–47**; the streak inherits the kill side's gap by construction. (The
17th, 30 kills against KTX's 54, is the same gap partially.) So:

> **`kills: 0` beside a large positive `frags` means the spree is
> inherited-unknown, not a measured zero.** Read the pair, not the spree
> alone.

Two things the replay deliberately does **not** do, both because KTX
does not either:

- **A reconnect does not reset the streak.** KTX stores the whole
  `playerstats_t` on a ghost entity at disconnect and assigns it back
  wholesale on the reconnect (`ghost->ps = self->ps`,
  `ktx/src/client.c:2951`; `self->ps = p->ps`, `:1515`), so
  `spree_current` and `spree_max` survive. This pipeline's rows are
  reconnect-unified (see `identity` / `sessions`), which lands on the
  same answer. Reachability on the first 500 archive demos: 10 rows span
  more than one session, and the 2 of them that also carry a KTX spree
  to check against agree exactly.
- **A mid-match team switch is not tracked**, and cannot happen on a KTX
  server: `FixPlayerTeam` refuses a team change while
  `match_in_progress` (`ktx/src/g_userinfo.c:407-419`, reverting it with
  a `stuffcmd`), and a reconnect onto a different team is refused
  outright in team modes (`client.c:1494-1500`). The roster model
  matches — `PlayerStream.Team` is one value per row and `sessions[]`
  carries no team of its own — as does the ground truth, since a KTX
  demoinfo player has exactly one `team`. A team row's spree maximum is
  therefore the max over its members as exported, with no
  team-at-kill ambiguity to resolve.

That check is deliberately left to the reader rather than run as a
per-row pipeline gate. `killsMeasured` is **demo-global** by design (see
above): it decides once, so every row on a demo agrees and a team row
can never mix a measured member with an unmeasured one. A per-row
suppression would break exactly that property to hide a residual the
`frags`/`kills` pair already exposes.

#### Reconstructed accuracy hits (v74)

On a demo whose `damage.source` is `reconstructed`, `accuracy.src`
becomes **`"reconstructed"`** and `hits` is filled from
[`aim.players[].weapons[].recon`](#weaponaimrecon) — the published
tier, read rather than re-derived, so the tier's own withholds inherit
here by construction — including its dependence on how much of the match
the reconstruction saw, which is [`damage.coverage`](#damagecoverage)
(v74) and is not copied onto the accuracy row. **On a low-coverage demo
these `hits` — and any accuracy derived from them — are a floor, not an
estimate**: the tier can only count fires the reconstruction saw connect,
so read `damage.coverage` before quoting one (same rule as the
[`recon`](#weaponaimrecon) block they come from and the
`src: "reconstructed"` damage family beside them). Only the weapons it
validated carry a number
(`lg`, `sg`, `ssg`, `axe`, `rl`, `gl`); **`ng`/`sng` keep `hits`
absent**. `rl` and `gl` carry the tier's `directHits` rather than its
`hits` — KTX's own convention for those two, so the row is comparable
with a block-carrying demo's; every other weapon carries `hits`, which
is what KTX counts there too. The rl figure leans on the server's
direct-damage constant, which the reconstruction measures per demo and
publishes as `damage.rocketDirectDamage`: where that is
absent the classification rests on geometry alone and the count is
looser (0.6% aggregate against KTX where the constant was established,
17.7% over the low-rocket rows where it was not), and a family whose weapons all fall outside the tier stays
`"derived"` — `attacks` alone is shot-derived either way and must not
claim a grade it does not have.

The tier's accuracy against the *measured* (any-path) counter is in
`damagerecon/ACCURACY.md` §"Aim hit recovery" (mean error lg 0.3 pp, sg
1.3, ssg 1.8, axe 0.6, rl 0.7, gl 0.4). Against **KTX's own** counters
the question is partly a definition one, and this table says which
weapons the two sources are even asking the same thing about — measured
on the same 186 demos. (It describes the RECONSTRUCTED tier only; the
wire-linked one publishes KTX's pellet unit for sg/ssg since v75.)

| | `attacks` vs KTX | `hits` vs KTX |
|---|---|---|
| `lg` | 98.4% exact | comparable: **0.9% aggregate**, and per row 63.8% exact, **0.3 pp of accuracy mean per row** |
| `rl` | 99.8% exact | comparable **since v74**, because the row publishes KTX's own DIRECT-impact convention there (`recon.directHits`): **1.25% aggregate**, 46.5% of 632 rows exact, p90 error 2 hits. The any-path count it used to publish read ~4x higher (361% aggregate, 35 pp per row) |
| `gl` | 100% exact | comparable since v74, same reason: **3.55% aggregate** but one-sided by design (the grenade-fuse rule), 89.6% of 424 rows exact, p90 error 1. The any-path alternative reads ~1.5x higher (54%) |
| `ng` / `sng` | 100% exact | withheld |
| `sg` / `ssg` | **not comparable**: KTX counts PELLETS, ours trigger pulls | not comparable, same reason |

The lg per-row figures matter for the same reason `damage.given`'s do:
the aggregate is what a leaderboard reads and the per-row spread is what
a single player's page reads, and quoting only the first would let a
±2-hit row look like a measurement error. The tail is real — worst row
31 hits — but it is a tail: the median row is exact.

So `src` tells you the evidence grade, this table tells you which
weapons the two sources are even asking the same question about, and
[`hitsConvention`](#accuracybyweaponhitsconvention--what-the-number-counts-v74)
says it per row so a consumer never has to re-derive the table.

### PlayerStatsWindow — the denominators

Match-relative int32 **ms** on the game clock, so pauses are already
excluded. Nothing in this section has an implicit denominator: KTX's
hold clocks stop at death, making alive time their unstated divisor,
and that silence is exactly what makes two "RA control" numbers from
different tools incomparable.

| Field | Meaning |
|---|---|
| `matchMs` | The whole match window. Identical on every row. |
| `presentMs` | First to last activity, clipped to the match — separates a late joiner or early quitter from someone present the whole time. |
| `aliveMs` | Time alive within `presentMs`. |
| `deadMs` | `presentMs - aliveMs`. |

Liveness follows the repo's canonical rule — the one carried by
[`streams.players[].alive`](#playerstream): alive from match start, a
death starts a dead period, the next spawn ends it — deliberately
**not** requiring a recorded match-start spawn, since KTX emits a
player's first spawn only on their first *respawn*. `playerStats`
applies that rule to the spawn/death markers itself
(`analyzer.aliveIntervals`, the same function that produces the stored
field) and then intersects with its own presence window.

**Honest divergence.** `aliveMs` and `sum(streams.players[].alive
durations)` are the same rule clipped to two slightly different
presence spans, so they can differ by up to ~250 ms per player.
`aliveMs` clips to `presentMs` — first-to-last activity over the raw
position samples and the markers — while the stored `alive` clips to
`result.TrackHoldEnd` (the last position sample held for one measured
cadence, capped at `result.SampleStaleCapMs` = 250 ms) widened by any
later marker. `aliveMs` also merges lives that merely touch, so it
cannot be used to count lives. Known and transitional; read `alive` for
life boundaries and `aliveMs` only as this section's denominator.

On a **team row** the `damage`, `accuracy` and `pickups` families are
member sums. Two rules there: `accuracy.byWeapon[w].hits` stays
**absent** unless *every* contributing member measured it (mixing a
measured member with an unmeasured one would understate the team
hit-rate under a number that looks measured), and `real` / `virtual` are
**not** aggregated at all — KTX omits the pair unless it recorded one,
so an all-or-nothing rule would drop them whenever a single member never
fired rl/gl. `takenToDie` is likewise never aggregated (averaging
averages across different death counts).

On a **team row** `presentMs` / `aliveMs` / `deadMs` are member sums
while `matchMs` stays the match window (it is the same value on every
row, player or team). Hold shares use team time: `shareAlive` over the
summed alive time, `shareMatch` over `matchMs × members`, where
`members` is published on the row. Only members who were actually in the
match count toward that denominator — a scoreboard-only row (connected,
never streamed, `presentMs` 0) would otherwise dilute every team share by
a whole match window of time nobody could have played. Shares are never
averages of per-player shares, which would weight a player who was dead
most of the match equally with one who was not.

### PlayerStatsHold / HoldStat

`weapons` is keyed `rl`, `lg`, `gl`, `ssg`, `sng` — the shotgun and axe
are deliberately absent (every player holds them all match), and the
**nailgun** is absent because `PlayerStream` tracks no NG possession
interval (`streams` records rl/lg/gl/ssg/sng only), so there is nothing
to integrate; adding it means adding that stream first. `armor` is
keyed `ra`, `ya`, `ga` and **`none`**, the alive-time complement: how
long the player ran with no armor at all, a stat KTX structurally
cannot produce. `ga + ya + ra + none == aliveMs` exactly.
`powerups` is keyed `quad`, `pent`, `ring`. A key the player never held
is **omitted**, not zero-filled. `none` is the near-exception: it is
emitted at zero as well, since "never without armor" is a real reading —
but only when the alive window is known **and the armor stream was
observed at all**. Two rows therefore carry no `armor` map: one with
`aliveMs` 0 (a scoreboard-only player who connected but never streamed),
and one whose armor stream is empty — a player the recording never
carried armor state for, typically on a POV demo where only the recorder
has stat streams. The complement of an unobserved stream would otherwise
read as a confident full-match `none` beside that player's own armor
pickups. (A player who genuinely never picked armor up is *not* this
case: the change stream always carries its first sample, so the empty
run still produces `none == aliveMs`.) Read `hold.armor?.none` rather
than assuming the key.

| Field | Meaning |
|---|---|
| `ms` | Possession time: the integral over native-rate possession intervals, clipped to the match window and intersected with the player's alive intervals. |
| `runs` | Number of disjoint possession spells (summed over members on a team row). |
| `longestMs` | Longest single spell (**max** over members on a team row, not summed). |
| `shareAlive` / `shareMatch` | `ms` over `window.aliveMs` / `window.matchMs` — **on a player row**. On a TEAM row both denominators are team-wide: `aliveMs` is already the members' summed alive time, and `shareMatch` divides by `window.matchMs × members`, not by `matchMs` (`analyzer/player_stats.go:1173`). See the paragraph two sections above; `members` is published on the row so the denominator stays recoverable. |

### PlayerStatsPickups

`byKind` uses **this repo's item-kind vocabulary** (`ra`, `ya`, `ga`,
`mh`, `h15`, `h25`, `quad`, `pent`, `ring`, `rl`, `lg`, `gl`, `ssg`,
`sng`, `ng`, ammo kinds) — not KTX's demoinfo keys. `view.PlayerStats`
maps KTX's onto it when overlaying (`health_100`→`mh`, `q`/`p`/`r`→
`quad`/`pent`/`ring`).

**The key SET depends on `src`.** A KTX overlay carries KTX's own weapon
vocabulary (`WpName`, `ktx/src/stats.c:358`), which includes **`axe` and
`sg`** — weapons the derived path never emits, since the shotgun and axe
are spawn equipment nobody picks up. Iterate the map rather than
assuming a fixed key list.

Absence is not uniform in this struct, which is a wart worth stating
plainly: `took` / `totalTook` / `dropped` are plain ints under
`omitempty`, so **absent means zero** (a drop-only entry serializes as
`{"dropped": 2}`). `xfer` / `xferSelf` are pointers, so **absent means
unobservable** — the demo carried no `//ktx bp` hints. Read the first
three with `?? 0` and the last two with an explicit null check.

| Field | Meaning |
|---|---|
| `took` | Acquisitions that granted the item — for weapons, pickups where the player did not already hold it (KTX `wpn.tooks`). |
| `totalTook` | Every touch including redundant ones (KTX `wpn.ttooks`). Weapons only. |
| `dropped` | Backpacks left on death carrying this weapon. |
| `xfer` / `xferSelf` | Pack transfers **credited to this player as the dropper** — see below. |

Non-weapon tallies come from the item timeline; weapon tallies come
from `weaponPickups`, because a weapon can also arrive in a backpack,
which the item timeline never sees and KTX's `wpn.tooks` does count
(`TookWeaponHandler` runs on backpack touch, `ktx/src/items.c:2475`).

**Transfers.** KTX's `xferRL`/`xferLG` (`ktx/src/items.c:2586-2615`)
credit the dropper when a pack whose contents are exactly the RL (or
exactly the LG) is taken by someone on the dropper's team, in teamplay
only (`isTeam()`). KTX has no `other != dropper` check, so re-taking
your own pack increments your own counter. We split that out:
`xfer + xferSelf` reproduces the KTX number exactly, while `xfer`
alone answers the question people actually mean.

`xfer` / `xferSelf` are **pointers**: absent means "this demo carries
no backpack hints, so transfers are unobservable", which is a different
fact from an observed zero. A *reconstructed* backpacks section
(`backpacks[].source == "reconstructed"`, v72) does not make them
observable. The v72 linkage does name a `picker` on most reconstructed
rows, but a transfer is not "who took it" — KTX's counter is gated on the
picker not already holding the weapon, and `hadBefore` is exactly what the
entity track cannot see (the weapon bit is ORed in, so a redundant grab
leaves no trace). They therefore stay absent there, while `dropped` does
count the reconstructed drops. They are teamplay-only, exactly like KTX's
gate — absent on duels and FFA. Note a pack holds the weapon the player
was *wielding* (`DropBackpack`: `item->s.v.items = self->s.v.weapon`),
one bit, which is why KTX's exact-equality test has no mixed-contents
case — and why the autoswitch-to-SG habit produces a pack that yields
no hint, no pickup row and no transfer.

## TimelineAnalysisResult (`timelineAnalysis`)

Defined in `result/timeline.go`.

This section carries only the event-shaped derived results. Bucketed
data is produced on demand by `mvd-analytics/view.Buckets` (any window
size, any reducer set; see [Streams](#streams-streams) and
[Query API](#query-api)), not baked into the parse-time result.

The match window and the wall-clock anchor (`matchStart`/`matchEnd`,
`demoOffset`, `demoStartUnixMs`, `demoStartAccuracyMs`, `pauses`) live in
[`streams.global`](#globalstream) — they describe how to
read the streams' times, so they sit next to them.

| Field | JSON key | Type |
|---|---|---|
| FragEvents | `fragEvents` | []TimelineFragEvent |
| DeathEvents | `deathEvents` | []TimelineDeathEvent |
| KillEvents | `killEvents` | []TimelineKillEvent |
| PowerupEvents | `powerupEvents` | []PowerupEvent |
| FragStreaks | `fragStreaks` | []FragStreakEvent |
| DemoMarkers | `demoMarkers` | []DemoMarkerEvent (player-inserted `/demomark` bookmarks) |
| LocationData | `locationData` | []MapLocation — one anchor point per loc name (the medoid of that name's `.loc` points) |
| LocTable | `locTable` | []string (interned loc names; index 0 = ""). `Streams.Players[].Loc[].V` indexes into this. |
| PlayerUserIDs | `playerUserIDs` | map[string]int (name → Hub viewer UserID). Per **session**, not per wire slot — see [Player userids](#player-userids-schema-v66). |
| RegionControl | `regionControl` | *RegionControlResult |

Bucketed data is served as `view.BucketsView` (row) or
`view.ColumnarBuckets` (column) — see
[Query API → Buckets](#buckets). Each player's per-bucket data is a
`map[string]any` keyed by the [field vocabulary](#field-vocabulary)
(row) or one dense array per field (column).

### TimelineFragEvent

`{ time, player, team, delta }`. Score-delta channel (`+1` enemy kill,
`-1` suicide / teamkill, `+2` for the rare gib double-frag KTX edge).
Reconstruct the killer ↔ victim relationship from `FragResult.Frags[]`
or `MessagesResult.Events[type=frag]` by matching `time`. `team` is
best-effort: `""` when the player's team never resolves (e.g. an empty
userinfo/demoinfo team); in 1v1 results the duel normalization rewrites
it to the player's own name.

### TimelineDeathEvent

`{ time, player, team }`. One record per death, sourced from the
authoritative protocol DeathEvent and gated to match time exactly like
`fragEvents` — every death counts once (enemy kill, suicide, world, or
being teamkilled), so a player's death count here matches their
scoreboard deaths and KTX efficiency `frags / (frags + deaths)`
(`ktx/src/statsTables.c` `calculateEfficiency`). Unlike `frags.frags`,
this does not drop teamkill victims whose obituary names only the
attacker. Pairs with `killEvents` for the Timeline tab's per-player
+/- (cumulative kills − deaths) drill-down. `team` is best-effort like
`fragEvents`.

### TimelineKillEvent

`{ time, player, team }`. One record per enemy kill, keyed on the
**killer**, sourced from the canonical frag log (`FragResult.Frags[]` /
`CoreOutputs.FragEntries`) filtered to real enemy kills (suicides and
teamkills excluded). A player's cumulative `killEvents` reconciles
exactly with `frags.byPlayer[].kills` and thus the kills-based
efficiency `kills / (kills + deaths)`. Parallel to `deathEvents`: `time`
is match-relative ms on the same clock (both are shifted by
`streams.global.demoOffset` in post-processing — the Timeline per-player
drill-down plots `killEvents − deathEvents` as a windowed +/- area, so
they must share the clock). `team` is best-effort via the name table and
— like `fragEvents` / `deathEvents` — is **not** gated to non-empty:
`byPlayer.kills` isn't either, so gating would silently drop a player's
whole kill curve in POV demos with an incomplete name↔team join (the
consumer groups by player name and ignores `team`); in 1v1 results the
duel normalization rewrites it to the player's own name.

### PowerupEvent

`{ time, endTime, playerName, playerSlot, playerUserID, team,
powerupType, duration, frags }`. One record per powerup run. Carries
both `playerSlot` and `playerUserID` (TimelineFragEvent doesn't —
intentional: that channel is lean by design). `playerUserID` is the
connection that held the slot **when the run began**, so a run picked up
before a handover keeps the picker's id — see
[Player userids](#player-userids-schema-v66).

### FragStreakEvent

`{ time, endTime, playerName, playerUserID, team, frags, duration,
ewep }`. One record per spawn-to-death life with ≥ 1 enemy kill, top 10
by frag count. A player already alive at match start has that first
life's spawn synthesized at match start (the real spawn happened during
warmup), so an opening run reads `time: 0`. `ewep` = effective weapon =
the weapon that scored the most kills during the streak. `playerUserID`
is the connection that held the slot during the streak — see
[Player userids](#player-userids-schema-v66).

### DemoMarkerEvent

`{ time, playerName, playerSlot, playerUserID, team, spectator, label }`.
One record per player-inserted `/demomark` bookmark (KTX stufftext
`//demomark`, schema v58). Attribution comes only from the demo block
target: the marking player's slot. A mark that was not slot-addressed
carries `playerSlot: -1` with empty `playerName` / `team` and
`playerUserID: 0`. KTX accepts `/demomark` from spectators too
(`CF_BOTH`); `spectator: true` (omitted when false) flags those marks —
their `team` is usually empty and their `playerUserID` is not a useful
Hub track target. Where it is set, it is the connection that held the
slot at the mark — see [Player userids](#player-userids-schema-v66). `label` is the optional argument tail (e.g. the
HoonyMode `"0 round-07"` form), omitted for the plain mark. `time` is
match-relative ms on the same clock as the other timeline events; a mark
inserted during warmup keeps a **negative** `time` (surfaced un-gated —
the pipeline reports every mark and the consumer decides). Not
deduplicated.

### AirgibEvent

Since schema v76 the airgib rows live at
[`highlights.airgibs`](#highlightsresult-highlights) as `HighlightEvent`
rows — `timelineAnalysis.airgibs` is removed, and `GET /airgibs` is
retired in favour of `/highlights?kinds=airgib&preMs=` (same `preMs`
semantics). `AirgibEvent` remains only as the detector's —
`view.ComputeAirgibs`' — internal output shape, never serialized. The
detection rules and the `preMs` knob are documented under
[Airgib detection](#airgib-detection-viewcomputeairgibs).

### Player userids (schema v66)

Five surfaces publish a hub userid — the `?track=<userId>` parameter of a
`hub.quakeworld.nu/games/…` link:

| Surface | Which userid |
|---|---|
| `timelineAnalysis.playerUserIDs[name]` | the player's **last session that had play** |
| `fragStreaks[].playerUserID` | the session holding the slot at the streak |
| `powerupEvents[].playerUserID` | the session holding the slot when the run began |
| `demoMarkers[].playerUserID` | the session holding the slot at the mark |
| `highlights.*[].actor.userId` / `.victims[].userId` | the session each participant held at the event |

A userid identifies one **connection**, not a person: mvdsv hands them out
from a rotating pool (`SV_GenerateUserID`, `mvdsv/src/sv_main.c:538-556`)
and only checks uniqueness against clients that are not `cs_free`. So a
player who times out and reconnects draws a **new** id, and the slot they
vacated hands its **old** id to whoever takes it next. The values above are
therefore resolved per *session* (one contiguous occupancy of a wire slot
by one userid), never per slot.

Two consequences for consumers:

- **A name-keyed id is the LAST one, and it is a choice.** Where the
  reconnect unifier folded several sessions into one name, `playerUserIDs`
  reports the id of the last session that had play — normally the one that
  is live at the end of the demo and the one a `track=` resolves for a
  player still connected. The rule ranks by **last play evidence**, not by
  who is still connected: when two of a name's sessions last played on the
  same millisecond the tie is broken toward the **lower slot**, which need
  not be the surviving connection. The earlier ids of the same person are
  not published here; the
  event carriers above each report the id valid at their own timestamp,
  which is the finer-grained answer.
- **Two rows can be one human, and that is faithful.** When a player
  reconnects while their first connection is still spawned, mvdsv renames
  the new one `(N)<name>` (`SV_ExtractFromUserinfo`,
  `mvdsv/src/sv_main.c:3686-3717`) and KTX, matching ghosts by exact
  netname, scores them as two players in its own demoinfo block. The
  scoreboard, the streams and `playerUserIDs` reproduce that split, each row
  with its own id. Do not merge them on a name heuristic — the server did
  not.

Before v66 all five carried the **first** userid ever seen on the wire
slot, latched for the whole demo, so any handover or reconnect published a
different connection's id.

#### Player identity and sessions (schema v66)

The two facts a name-keyed row cannot express — *"these two rows are one
person"* and *"this row was slot S / userid U during [t1,t2)"* — are
published directly, on `streams.players[]` and mirrored verbatim onto
`playerStats.players[]` (so `/player-stats` answers both on its own):

```json
{ "name": "rusti", "identity": "s7u8",
  "sessions": [ {"startMs": -10248, "endMs": 603204, "slot": 7, "userId": 8,  "name": "rusti"},
                {"startMs": 603204, "endMs": 1200101, "slot": 2, "userId": 14, "name": "rusti"} ] }
```

`identity` is the reconnect-unification key the pipeline already used
internally to merge a reconnected player's streams (KTX's own ghost
mechanism, reproduced by `analyzer/identity.go` from the demoinfo join, the
`*auth` login, and the KTX rejoin broadcasts). Rows with equal values are one
human; rows with different values are different humans **as the server
scored them** — on hub 220637 the two `rusti` rows carry `s5u37` and `s9u43`
because KTX itself declined to merge them, while on 216835 the single `rusti`
row carries one identity over two connections (userids 8 and 14).

Three properties of the key, all deliberate:

- **Demo-local. Do not persist it, do not compare it across demos.** It is
  derived from the identity's first session (`s<slot>u<userId>`), and both
  components describe a connection to *one* server. The cross-demo identity is
  the authenticated login (`playerStats.players[].login`), which is already
  published.
- **Reproducible from these bytes.** It is a wire fact rather than an array
  index, so a consumer with their own parse can recompute it and a
  re-analysis yields the same value. It is *not* promised across pipeline
  versions: a new unification source merges two groups, and a merged group
  keeps only one of the two first sessions.
- **It partitions rows.** A theoretical clash (a userid reissued to the same
  slot for a later, unrelated occupancy) is broken with an `@<startMs>`
  suffix — and, where even that collides, a further `.2` / `.3` counter —
  because the streams builder groups on this key and a clash would splice
  two players into one row.

**Joining the other views.** Only `streams.players[]` and
`playerStats.players[]` carry these fields. Every other name-keyed surface —
`lives`, `top-windows`, `frags`, `damage`, `buckets` — is joined by the row's
**player name**, the same canonical string in all of them on all but one
demo shape; fetch `/player-stats` once and index it by name to get the
identity and sessions for any of them.

**The one join that can miss.** When two identities resolve to the same
display name, these two views suffix each of them `name#slot` to keep the
rows distinct (see `streams.players[].name`), while the frag and damage logs
— which have no second key to disambiguate with — keep the bare name for
both. An exact-string lookup of such a row therefore finds nothing: strip
the `#…` suffix to reach the other views, and read the result as covering
*both* same-named humans.

One consequence worth knowing: `/top-windows`' `perPlayer` cap is
**name-keyed**, so a human split across two rows can occupy twice the
per-player quota. With `identity` a consumer can at least detect it (two
top-level names, one identity); collapsing the cap onto identity is not done.

### MapLocation

`{ x, y, z, name }`. Used by `LocationData` (one anchor point per loc
name — see below) and `ControlRegion.Points` (rendering anchors).

`LocationData` holds one `MapLocation` per loc name
instead of every raw `.loc` corpus point. The point chosen is the
**medoid** of that name's corpus points — the actual point minimizing
summed 3D distance to its same-name siblings, never an averaged mid-air
centroid — so the map view draws one label per name instead of a cluster
of duplicates. `locGraph` node coordinates (resolved from this list by
name) move to the medoid accordingly.

### RegionControlResult (`regionControl`)

The parse-time output carries only `stats` (match-aggregate
percentages); `bucketStates` is not baked in. For per-bucket region
states at any resolution, call
`view.RegionControl(opts)` (Go) or `recomputeRegionControl(regionsJSON)`
(WASM bridge); both derive the bucket states on demand from
`result.Streams`.

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Regions | `regions` | []ControlRegion | Region definitions. |
| TeamA | `teamA` | string (omitempty) | Team name encoded as `A` in BucketStates. Picked alphabetically. |
| TeamB | `teamB` | string (omitempty) | Team name encoded as `B`. |
| BucketStates | `bucketStates` | map[string]string (omitempty) | Populated only by query-time results (`view.RegionControl` / `recomputeRegionControl`). Region name → string of length `n_buckets`, one ASCII char per bucket at the requested `windowMs`. Each bucket is a **point-sample classification**: the state of every player's last position at bucket-start. Shares the exact walk's classification point (`view.playerCursor.sampleAt`) and therefore all of its bounds (v64): a dead player is skipped, a player whose last sample is more than `result.SampleStaleCapMs` (250 ms) old contributes nothing (their location is unknown, not held), and a player past their own `result.TrackHoldEnd` contributes nothing at all. Buckets in which no player qualifies read `_` (empty) rather than controlled/contested. |
| Stats | `stats` | map[string]RegionStats (omitempty) | Region name → match-aggregate share of each control state (percent, one decimal). **Computed as the exact time-weighted integral over the native position sample times, independent of the caller's `windowMs`** (no grid): the walk unions every player's Position sample times with their RL/LG armed boundaries **and their `alive` life boundaries** (v64) and accumulates each constant-state interval's real duration, so the aggregate is not a sampling artifact of a display window (a coarse point-sample could miss a mid-bucket fight and report `empty:100`). `byPlayer` values are integer milliseconds of **ALIVE, OBSERVED** presence. Three v64 bounds produce that: dead players are excluded; **sample-and-hold expires after `result.SampleStaleCapMs` (250 ms)**, so an unobserved hole (a PVS gap on a POV recording, packet loss) is credited to nobody instead of holding the pre-gap region across it; and a player stops contributing at their own `result.TrackHoldEnd` rather than holding their last region to match end. The 250 ms cap is inert at every real recording cadence (worst gap in the golden corpus: 74 ms) and decisive on POV demos, where non-recorder gaps reach tens of seconds. |

`BucketStates` codes (one byte per bucket):

| Char | State |
|---|---|
| `_` | empty |
| `A` | teamAControl |
| `a` | teamAWeakControl |
| `C` | contested |
| `c` | weakContested |
| `B` | teamBControl |
| `b` | teamBWeakControl |

Control rule (faithful port of `mvd-web/static/app.js:classifyRegionState`):
"armed" = carrying RL or LG. Strong control = the dominant team has at
least one armed player; weak = present but unarmed; contested = both
present and armed. **Dead players are skipped** — gated on
`streams.players[].alive` since schema v64. (An earlier version of this note
claimed `D=true` / `H<=0` samples were skipped; they never were. A dead
player keeps streaming position at full rate, so liveness is not inferable
from the samples and had to become an explicit gate.)

`view.RegionControl` (Go pure function in `view/region_control.go`)
is callable post-analysis with edited regions, custom team labels,
or a custom `teamOf` closure via `RegionControlOptions`. WASM
exports `recomputeRegionControl(regionsJSON)` for the web UI's
in-page region editing; the REST/MCP `/v1/demos/{id}/region-control`
endpoint exposes the same function with a `windowMs` query
parameter. The CLI's `-regions <path>` flag overrides the embedded
per-map regions at analysis time, before the result is cached.

### ControlRegion

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Name | `name` | string | |
| Locs | `locs` | []string | **Authoritative logical membership.** A player is "in" the region iff their resolved loc name is here. |
| Points | `points` | []MapLocation | Rendering anchors. Geometry only — the classifier ignores them. |
| CentroidX | `centroidX` | float32 | Label placement anchor. |
| CentroidY | `centroidY` | float32 | |

### RegionStats

```
RegionStats = {
  // Seven aggregate control-state percentages (0..100, one decimal,
  // sum to 100 within rounding).
  "teamAControl":     float,
  "teamAWeakControl": float,
  "contested":        float,
  "weakContested":    float,
  "empty":            float,
  "teamBWeakControl": float,
  "teamBControl":     float,
  // Per-player attribution. Map: player name → this player's exact
  // time-weighted presence in the region, in integer MILLISECONDS
  // (the integral over their native position samples, independent of
  // the caller's display windowMs). No scaling needed — already ms.
  "byPlayer": {
    "<player>": {
      "team":    "<team>",
      "armed":   <int>,  // ms present carrying RL or LG
      "unarmed": <int>   // ms present without RL/LG
    }, ...
  }
}
```

`byPlayer` answers "who was responsible for keeping <region>?" Sort
its entries by `armed + unarmed` for total presence, or by `armed`
alone for armed-presence share. Total per team in the region equals
the team-aggregate state count, so you can also compute "what
fraction of team A's presence in QUAD came from sailorman".

## Streams (`streams`)

Defined in `result/streams.go`. Streams is the canonical
event-rate storage for every per-player field. Each
`PlayerStream` records every change to a tracked field at the rate it
actually changed; aggregated views (50 ms / 1 s buckets, point-in-time
state, loc trails) are computed on demand from this storage by the
`mvd-analytics/view` package.

### Top-level shape

| Field | JSON key | Type |
|---|---|---|
| Players | `players` | []PlayerStream |
| Global | `global` | GlobalStream |
| Movers | `movers` | []MoverStream (brush-model lifts/doors/plats/trains; `omitempty`) |
| Projectiles | `projectiles` | *ProjectileStreams (rocket/grenade flights; `omitempty`) |
| Beams | `beams` | *BeamStreams (LG bolts; `omitempty`) |
| Nails | `nails` | *ProjectileStreams (ng/sng spike flights; `omitempty`) |
| PointEffects | `pointEffects` | *PointEffectStreams (temp-entity point effects; `omitempty`, v71) |

`projectiles`, `beams`, `nails` and `pointEffects` are the spatial
weapon-fire streams for the map view (schema v40; `pointEffects` v71). They
are sizeable (thousands of beams/nails in a team game), so whether they are
built depends on the consumer: the **CLI** builds them only when requested
(`qw-analyze -include projectiles,beams,nails`; `pointEffects` rides the
projectiles/beams flag) to keep the default output and golden corpus lean;
**mvd-api** builds all of them on every parse (the always-full cache — the
+3–4% parse cost is worth deleting the old lazy re-parse); and the **WASM
web build** builds them all so the map overlay and Aim tab are complete in
the browser with no extra download. All are columnar (parallel arrays, one
entry per flight / bolt / effect), times match-relative ms.

Building `nails` (via `-include nails` on the CLI, or automatically under
mvd-api / the WASM build) also turns on ng/sng → damage linking (it decodes
spike packet entities / `svc_nails`, brackets each nail flight, and links it to
its fire).
The `nails` stream reuses the `ProjectileStreams` shape with `Weapon` =
`"nail"` (svc_nails is untyped; ng vs sng is resolved from the damage type,
not the model).

### ProjectileStreams (`streams.projectiles`)

Each index `i` is one tracked rocket/grenade flight: a dot moving from
`(sx,sy,sz)[i]` at `s[i]` to `(ex,ey,ez)[i]` at `e[i]` (linear — exact for
rockets, approximate for bouncing grenades). `w[i]` is `"rl"` or `"gl"`.

| Column | JSON key | Type |
|---|---|---|
| Weapon | `w` | []string |
| Spawn | `s` | []int32 (ms) |
| End | `e` | []int32 (ms) |
| Sx/Sy/Sz | `sx`/`sy`/`sz` | []float32 (muzzle) |
| Ex/Ey/Ez | `ex`/`ey`/`ez` | []float32 (impact) |

### BeamStreams (`streams.beams`)

Each index `i` is one LG bolt (`TE_LIGHTNING2`): the segment
`(sx,sy,sz)[i]` (muzzle) → `(ex,ey,ez)[i]` (trace endpoint) flashed at `t[i]`.

| Column | JSON key | Type |
|---|---|---|
| T | `t` | []int32 (ms) |
| Sx/Sy/Sz | `sx`/`sy`/`sz` | []float32 |
| Ex/Ey/Ez | `ex`/`ey`/`ez` | []float32 |

### PointEffectStreams (`streams.pointEffects`)

Each index `i` is one point-effect temp entity at `(x,y,z)[i]`, `t[i]`
match-relative ms. `ty[i]` is the raw TE type (0 spike, 1 superspike,
2 gunshot, 3 explosion, 11 teleport, 12 blood, 13 lightningblood, …).
`c[i]` is the leading count byte carried only by the counted types
(`TE_GUNSHOT`/`TE_BLOOD`) and 0 elsewhere. mvd-api serves this block at
`GET /v1/demos/{id}/streams/point-effects` with a code→name legend and a
`types=` filter. `TE_BLOOD` and
`TE_LIGHTNINGBLOOD` are per-hit damage telemetry (blood = hitscan damage
striking a player, count = pellet hits per volley on modern KTX;
lightningblood = an LG cell connecting); `TE_EXPLOSION` is the exact
rocket/grenade detonation point. The count-byte packaging varies per
server generation and mod — see `mvd-reader/MVD_FORMAT.md`
(svc_temp_entity) before interpreting `c` as a damage magnitude.

| Column | JSON key | Type |
|---|---|---|
| T | `t` | []int32 (ms) |
| Type | `ty` | []int32 (raw TE type) |
| Count | `c` | []int32 (count byte; 0 when the type carries none) |
| X/Y/Z | `x`/`y`/`z` | []float32 |

### GlobalStream

The match window plus the demo/wall-clock anchor (moved here from
`timelineAnalysis`).

| Field | JSON key | Type | Notes |
|---|---|---|---|
| MatchStart | `matchStart` | int32 | Match window start in milliseconds (always 0 after post-process — it *is* the time origin). |
| MatchEnd | `matchEnd` | int32 | Match window end in milliseconds. |
| TimeBase | `timeBase` | string, omitempty | `"demo"` when **no match start was detected** (schema v52): the rebase never ran, so *every* timestamp in the whole Result is on the raw demo clock (t=0 = demo open, warmup included). Omitted on the normal match-relative result. A matching notice appears in `errors[]` (and therefore `/overview`). It covers only the case where a match start was detected at demo `t=0` — a demo with NO detected match start produces no `streams` block at all, and is marked by [`noMatch`](#nomatchresult-nomatch-schema-v74) instead. |
| MatchStartSignal | `matchStartSignal` | string, omitempty | v75 — which **wire signal** the match start was detected from: `ktx-matchstart` (KTX's `//ktx matchstart` stuffcmd, `ktx/src/match.c:1372`) \| `print` (a match-start broadcast such as `"The match has begun!"`, `match.c:1296`) \| `matchdate` (the `matchdate:` stamp, `match.c:1291`) \| `status` (a serverinfo `status` transition into a running clock, `match.c:1337`). Layer 1 raises the event at the **first** of the four to reach the wire, so on a modern KTX demo — where three of them land in the same server frame — this names which byte arrived first, not a different instant. **Not the same field as `matchStartSource` below**, which names where the wall-clock VALUE came from; the two vocabularies both contain `matchdate` and mean different things. |
| DemoOffset | `demoOffset` | int32, omitempty | Ms from demo open (≈ countdown start) to match start. |
| DemoStartUnixMs | `demoStartUnixMs` | int64, omitempty | Server wall clock (Unix epoch ms) at demo open. |
| DemoStartAccuracyMs | `demoStartAccuracyMs` | int32, omitempty | ± uncertainty of `demoStartUnixMs`: `1`, `1000`, or (v72, marker-derived) `3600000` / `50400000`. |
| DemoStartSource | `demoStartSource` | string, omitempty | v72 — which source produced the demo-open anchor: `mvdhidden`, `epoch`, `matchdate`, `matchkey`, `ktxstats`. |
| MatchStartUnixMs | `matchStartUnixMs` | int64, omitempty | v72 — server wall clock at **match** start (the instant `g=0` names). |
| MatchStartAccuracyMs | `matchStartAccuracyMs` | int32, omitempty | v72 — ± uncertainty of `matchStartUnixMs` (see the accuracy ladder below). |
| MatchStartSource | `matchStartSource` | string, omitempty | v72 — same vocabulary as `demoStartSource`. |
| MatchStartConfidence | `matchStartConfidence` | string, omitempty | v72 — `exact` \| `unverified` \| `contradicted`. |
| MatchStartNote | `matchStartNote` | string, omitempty | v72 — names the check(s) behind a non-`exact` grade. Empty on `exact`. |
| MatchEndUnixMs | `matchEndUnixMs` | int64, omitempty | v72 — wall clock at match end, from the ktxstats `date` string, else the year-completed `//finalscores` stamp. |
| DateMarkers | `dateMarkers` | []WallClockMarker, omitempty | v72 — every date stamp the wire carried, **on a result that has a `streams` block**; a stream-less result carries the same list on `noMatch.dateMarkers` instead (v74). See below and the no-streams exception under the grades table. |
| Pauses | `pauses` | []TimelinePause, omitempty | Per-pause wall-clock segments; see below. |

**Wall-clock anchor.** All other times in the result are match-relative
(`t=0` is match start). The anchor lets a consumer project any
match-relative game time `g` (ms) onto a real-world wall clock for
syncing external data (voice tracks, stream overlays):

```
wallClockMs = demoStartUnixMs + demoOffset + g + P(g)   (±demoStartAccuracyMs)
P(g)        = Σ pauses[i].durationMs  for  pauses[i].atMs <= g
```

`demoStartUnixMs` is the server's clock at **demo open** (demo `t=0`, ≈
countdown start — not match start; `demoOffset` bridges the two).
`demoStartAccuracyMs` is its resolution: `1` from the millisecond
[mvdhidden 0x000B block](../mvd-reader/MVD_FORMAT.md#hidden-message-types),
`1000` from the whole-second serverinfo `epoch` cvar. Implausible
0x000B payloads (some demos emit a non-timestamp block here) fall back to
`epoch`. The REST `/overview` endpoint mirrors this anchor in its `timing`
block.

**Match-start anchor (schema v72).** Only ~25% of the archive carries one
of the two server-clock sources above. Nearly all of the rest states its
date on the wire in a *print* instead, which describes the **match**, not
the demo file — so v72 adds a second, honestly-named anchor rather than
overloading `demoStartUnixMs` with a differently-defined value:

| | question it answers | coverage |
|---|---|---|
| `demoStartUnixMs` | origin of the `wallClockMs` mapping above | ~95% (v72), ~25% before |
| `matchStartUnixMs` | "when was this match played?" | ~95% |

Five sources can anchor it — the two server clocks plus the three wire
date markers that state a year — listed in the order they are trusted. A
sixth, `//finalscores`, states no year and therefore anchors nothing; it
corroborates (see below):

| `…Source` | Wire origin | Names |
|---|---|---|
| `mvdhidden` | 0x000B block (ms) | demo open |
| `epoch` | serverinfo cvar (s) | demo open |
| `matchdate` | level-2 broadcast print at match start, `ktx/src/match.c:1291`. Two layouts: ISO `2008-01-05 20:05:38 CET` and ctime `Mon Jul 03, 01:01:14 2006` | match start |
| `matchkey` | level-2 broadcast print, kmod/KTeam era, `matchkey: 8-2005-8-13:19-56-18` (`<matchid>-<y>-<m>-<d>:<h>-<mm>-<ss>`, never any timezone) | match start |
| `ktxstats` | the `date` string of the KTX demoinfo block (`%Y-%m-%d %H:%M:%S %z`), also published verbatim as `demoInfo.date` | match **end** (also `matchEndUnixMs`) |
| `finalscores` | the date field of KTX's `//finalscores` end-of-match stuffcmd (`%b %d, %H:%M` — **no year**, no seconds, no zone), also published verbatim as `metadata.finalScores.date` | match **end**, corroboration only |

**The year-less marker.** `//finalscores` reaches 64% of the archive but
its stamp cannot name an instant on its own. It is resolved last: the
year comes from whatever anchored the match (reported as the marker's
`yearFrom`, which therefore names a server clock — `mvdhidden` /
`epoch` — as often as it names another marker), and the zone is borrowed
from that same anchor,
because both stamps come from one server's `strftime(localtime)`. What
remains is genuine evidence — month, day, hour and minute — and it is
cross-checked like any other marker, with a tolerance of the ktxstats
intermission spread plus the minute the layout truncates to. It can never
be selected as `matchStartSource`, and with no other marker present it is
reported with `unixMs: 0` and no `yearFrom` rather than dropped. On a
120-demo archive sample of demos with no demoinfo block it corroborated
every anchor (119 `exact` / 1 `unverified`, unchanged from before it was
parsed) and supplied `matchEndUnixMs` on all 120, where the ktxstats
source reaches none of them.

**The no-streams exception.** The whole wall-clock family lives on
`streams.global`, so a result with no `streams` block at all carries none
of it. Since v74 the RAW STAMPS are published anyway, on
[`noMatch.dateMarkers`](#nomatchresult-nomatch-schema-v74) — 104 of the
1 032 stream-less demos in the archive sweep carry at least one, and
before v74 every one of them was read off the wire and then dropped.

What is still NOT published there is the graded anchor beside them
(`matchStartUnixMs` + `matchStartConfidence`). That anchor is a
PROJECTION through the match window — a match-start print is projected as
`stamp − print's demo time + demoOffset`, a match-end stamp as
`stamp − match length` — and both terms are exactly what a stream-less
result does not have. Resolving it means first establishing a match
origin on the demo clock, which is salvage, not marking: plan lead 8
stage (b) in `plan-archive-features.md`. `metadata.finalScores` does not
live under `streams` and survives on those demos regardless.

Where only a print marker exists, `demoStartUnixMs` is back-shifted from
it by `demoOffset` (so the formula above keeps working) and
`demoStartSource` says so. A **contradicted** stamp is never back-shifted
into `demoStartUnixMs` — it stays on the match-start fields where its
grade travels with it.

`matchStartAccuracyMs` states what the marker actually pins down:

| Value | Meaning |
|---|---|
| `1` / `1000` | the server-clock anchors, and second-resolution markers with a resolved timezone |
| `3600000` | a zone *name* that does not state whether DST was in force (the Windows "… Standard Time" long names) |
| `50400000` | the marker named no timezone at all (every `matchkey`, and the ctime `matchdate` layout) — read as UTC, which is right to within the widest real offset, 14 hours, and no better |

**Confidence.** Old servers ran with unset clocks, but the value alone
never proves it (2000 is a live QuakeWorld year), so the grade is decided
by contradiction, never by the date:

| Grade | Meaning |
|---|---|
| `exact` | timezone resolved, no check failed |
| `unverified` | nothing contradicts it, but something is unpinned: an assumed timezone, or one soft signal |
| `contradicted` | a **hard** check failed (a binary cannot predate its own release: the `*version` / `ktxver` release floor), or two soft signals agreed |

The soft signals are markers within one demo disagreeing beyond timezone
slack, and the boot-default window (early 2000-01) an unset RTC comes up
in. Either alone is indistinguishable from a real match. **An anchor is
never dropped, coerced or hidden on a failed check** — the grade plus
`matchStartNote` is the whole report. The note joins the failed checks
with `; ` and its forms are exactly:

| Note | Meaning |
|---|---|
| `version-floor: mvdsv 0.28 was not released before 2006-01-01` | HARD — the binary the serverinfo names postdates the stamp |
| `impossible-date: the stamp lands after 2100` | HARD — the value is corrupt, not a date |
| `marker-disagreement: matchdate vs ktxstats` | SOFT — the chosen source vs the sources it could not be reconciled with |
| `epoch-reset-window: the stamp lands in the unset-clock boot default (2000-01)` | SOFT |
| `tz-unknown: the marker named no timezone, UTC assumed` | why the grade is `unverified` with no other signal |

Each `dateMarkers[]` entry is a **WallClockMarker** — every stamp seen,
used or not, so a consumer can redo the cross-check itself:

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Source | `source` | string | `matchdate` \| `matchkey` \| `ktxstats` \| `finalscores` |
| Kind | `kind` | string | `matchStart` \| `matchEnd` |
| UnixMs | `unixMs` | int64 | parsed instant, timezone applied; `0` on a `finalscores` stamp whose year could not be completed |
| AtMs | `atMs` | int32, omitempty | demo-clock ms of the print that carried it (absent for the two markers that ride no print, `ktxstats` and `finalscores`) |
| YearFrom | `yearFrom` | string, omitempty | v72 — `finalscores` only: the ANCHOR source its year was taken from. Same vocabulary as `matchStartSource`, so it is often a server clock rather than a marker: `mvdhidden` \| `epoch` \| `matchdate` \| `matchkey` \| `ktxstats`. Absent when nothing anchored the match (then `unixMs` is `0` too) |
| TZ | `tz` | string, omitempty | zone token exactly as printed (`CET`, `+0200`, `Vdsteuropa, sommartid` — the Swedish long name after `Q_normalizetext` folds its high-bit `ä` to `d`) |
| AssumedUTC | `assumedUtc` | bool, omitempty | the zone was missing or unrecognised, so UTC was assumed |
| Raw | `raw` | string, omitempty | the stamp text after the marker prefix |

The `P(g)` term accounts for **pauses**: the game clock freezes during a
pause while wall-clock time keeps running, so without it the mapping
drifts by the total pause time on any paused demo. `P(g)` is `0` (and
`pauses` may be absent) otherwise.

Each `pauses[]` entry is a **TimelinePause** `{ atMs, durationMs }`: `atMs`
is the match-relative game time the clock froze at (negative if the pause
happened during the countdown), `durationMs` the real wall-clock time the
pause consumed. Recovered from the [mvdhidden 0x000A `paused_duration`
blocks](../mvd-reader/MVD_FORMAT.md#hidden-message-types) mvdsv embeds once
per idle frame while paused (summed per pause), in `atMs` order. Absent
when the demo has no pauses or the server does not embed the block.

### PlayerStream

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Name | `name` | string | Canonical player name (D12: collisions in same match get a `#slotIndex` suffix). |
| Team | `team` | string (omitempty) | Team label (post-duel-normalise: per-player synthetic team). |
| Identity | `identity` | string (omitempty) | The reconnect-unification key (schema v66): two rows carrying the same value are **one human**, however their names differ. **Demo-local — do not persist it and do not compare it across demos.** See [Player identity and sessions](#player-identity-and-sessions-schema-v66). |
| Sessions | `sessions` | []PlayerSession (omitempty) | Every wire-slot occupancy behind this row, in time order — which slot and which userid, when. See [PlayerSession](#playersession-streamsplayerssessions). |
| Position | `pos` | *PositionTrack (omitempty) | Native-rate position track: x/y/z plus optional per-sample `li`/`h`/`lq` and view-direction `vp`/`vya` columns. Omitted from default JSON unless `-include positions` (CLI) or equivalent is set; `-include view`/`height`/`liquid` keep the respective extra columns. |
| Health / Armor | `h` / `a` | []ChangeI16 | Vital change streams. Health caps at 250, Armor at 200; int16 holds the range. |
| ArmorType | `at` | []ChangeStr | `"ga"` / `"ya"` / `"ra"` / `""` transitions. |
| Loc | `li` | []ChangeI16 | Index into `TimelineAnalysisResult.LocTable`. Smoothed by the blip filter. |
| RL / LG / GL / SSG / SNG | `rl` / `lg` / `gl` / `ssg` / `sng` | []Interval | Half-open `[Start, End)` periods the weapon was held. |
| Quad / Pent / Ring | `q` / `pe` / `r` | []Interval | Same shape as weapons. |
| Shells / Nails / Rockets / Cells | `sh` / `nl` / `rk` / `cl` | []ChangeI16 | Ammo change streams. |
| ActiveWeapon | `aw` | []ChangeI16 (omitempty) | v72 — the **wielded** weapon as an `IT_*` bit: 1 SG, 2 SSG, 4 NG, 8 SNG, 16 GL, 32 RL, 64 LG, 4096 axe, or 0 for nothing held (a client edict whose weapon was never set). Those nine values are the whole vocabulary: anything else is protocol-impossible for `ent->v->weapon` and is refused at the parser boundary rather than published as a plausible bit. A different question from the `rl`/`lg`/… interval streams above, which are *inventory*: a player owning the RL can be holding the LG. Straight from `STAT_ACTIVEWEAPON`, which mvdsv writes from `ent->v->weapon` for every spawned player (`mvdsv/src/sv_send.c:1268`) — the same field `DropBackpack` puts in the pack, which is what makes [backpack reconstruction](#backpacks-backpacks) a replay rather than an inference. Delta-coded on the wire, so this column is recorded **through the countdown** and the match-start rebase carries the latest pre-match value forward to `t=0`; a player whose weapon last changed in warmup would otherwise have no in-match sample. Absent when the recorder never wrote the stat, and **frozen** (one sample, never moving) on the same old recordings that freeze the `STAT_ITEMS` weapon bits — check it moves before trusting it. |
| Spawns / Deaths | `sp` / `d` | []int32 | Discrete event timestamps in milliseconds. `sp` includes the match-start spawn: KTX respawns everyone when the countdown ends, but a player alive through the countdown produces no dead→alive wire transition, so the timeline synthesizes their spawn at `0` (schema v51). |
| Alive | `alive` | []Interval (**never** omitempty) | The player's **lives**: one half-open `[s,e)` interval per spawn-to-death run, derived from the fused `sp`/`d` markers against the match window (schema v64). The canonical **stored** liveness — read it rather than re-deriving from `sp`/`d`. Since v65 an API consumer can: `/stream-slice` serves it per player clamped to the window and `/state-at` serves it as a `true`/`false`/`null` scalar at the instant, in both cases never field-gated (before that the advice was unfollowable over REST — the field reached no endpoint, and there is no `streams` artifact to fall back on). LOS, aim, loc-graph, region control and `/loc-trails` all read it; the two in-package predicates that used to re-derive their own (`analyzer.losAliveAt`, `aimcore.aimAliveAt`) are gone as such — `losAliveAt` is deleted and `aimAliveAt` now just reads this field. Their strict `lastSpawn > lastDeath` **latched** on a death and its triggered respawn sharing a millisecond, reporting the player dead for the rest of that life (measured: 100.7 s of one player's 1143.7 s match); this field reports alive there, which is correct, and the LOS/aim figures moved when they were migrated. One independent predicate remains on purpose: `view.playerActiveInWindow`, behind the `/buckets` columnar `alive` mask, which asks the different question "did this player appear anywhere in this bucket window" (a window-OVERLAP test with its own fallbacks) and already resolves the tie correctly. The two can disagree, and neither is wrong. Clipped at the **ends** to observed presence: the derivation starts everyone alive at `t=0` (deliberately — KTX emits a first spawn only on the first *respawn*), so without the clip a late joiner would claim life before connecting and an early quitter would claim life to match end. Presence is the position track (ending at `result.TrackHoldEnd`) **widened by marker evidence**: spawns and deaths are broadcast to every recorder, so a player whose track stops — on a POV recording everyone outside the recorder's PVS drops out of `svc_playerinfo` — is still known to exist at every later marker, and the high clip extends to it. The two ends are deliberately asymmetric: a *death* before the track start drops the low clip entirely (the player was in the game for the run-up to it), while a *spawn* before it is the join itself and widens nothing. Consequence worth knowing: every death always falls inside some life, so a truncated track can no longer make a player look as if they left the game. A death **splits** a life even when the respawn lands on the same millisecond — the two intervals *touch*, so no dead time is invented, but the boundary survives, because anything enumerating lives or attributing per-life stats must see it. A **hole** in the track does *not* split: an unobserved stretch is not a death, and on a POV recording (only players inside the recorder's PVS are written) tracks are full of multi-second holes. Refusing to credit unobserved *time* is a separate concern, handled by the occupancy walkers through `result.SampleStaleCapMs` (250 ms). A reconnect gap *inside* a merged stream is still not represented. Liveness is **not** inferable from the samples, because a dead player keeps streaming position at full rate and on a gib the player entity *is* the thrown head. Three distinct states: `null` = liveness was not measurable, `[]` = measured and never alive in the window, `[…]` = the lives. A player who never died is a single full-match interval, so absence can never be read as "alive throughout". A death with no following spawn (the KTX `dtTELE2` deflection) correctly leaves them dead to the end. |
| LOS | `los` | []LosTrack (omitempty) | Per-opponent line-of-sight intervals. BSP-backed maps only, and **computed lazily** — absent from the default parse; populated on demand (web LOS overlay, `qw-analyze -include los`, mvd-api `/los`). |
| PVS | `pvs` | []LosTrack (omitempty) | Per-opponent potentially-visible-set intervals: the PVS cull the LOS raycast gates on, recorded before the rays narrow it. Lossless superset of `los` (PVS ⊇ LOS). Same shape, gate, and lazy pass as `los`. |

### PlayerSession (`streams.players[].sessions[]`)

One contiguous **play** window on a wire client slot (schema v66; since v75
a connection may contribute more than one — see below). Within it the slot's
userid is constant, so it is also the validity window of that userid.

| Field | JSON key | Type | Notes |
|---|---|---|---|
| StartMs | `startMs` | int32 ms | Where this window opens: the first userinfo that attested the connection, or — since v75 — the `join` that turned a connected spectator into a player, which is the same userid re-entering the game (on `4on4_fu_mix_060626_dm2_rename_handover` `rusti`'s window opens at his join, 620 599, not at the spectator connection at 609 644). **Not clamped to the match window**: a player connected during the countdown reports a NEGATIVE start (same policy as `demoMarkers` and `global.pauses` — the wire said it). |
| EndMs | `endMs` | int32 ms | Where it closes: the drop broadcast, the next connection's userinfo, or — since v75 — an `observe` (the player going spectator), which the mod announces as a departure and which therefore ends the window exactly as a drop does. Half-open `[startMs, endMs)`. **Synthetic in two cases**, both reading exactly match end: a client still connected when the recording ended (no wire event exists to report), and a drop broadcast landing *after* match end (the event exists, but the window is closed on the match clock every other time here lives on). So an `endMs` at match end does not by itself mean "still connected". |
| Slot | `slot` | int | Wire client slot (0-based) — the index `svc_*` messages address this connection by. |
| UserID | `userId` | int | The connection's userid: the value a hub `?track=` link must carry to follow this player **inside this window, and only then**. Always **non-zero** — an occupancy the wire gave no userid is not published as a session at all (see below), so there is no 0 to interpret. |
| Name | `name` | string (omitempty) | The netname this connection carried, where the row's `name` is the identity's canonical one. They differ after a rename or an mvdsv `(N)` prefix; this is the one to match against a live engine roster at an instant inside the window. Folded through the same Quake normalization as every other name here (`qNormalizeTable`), so it is not the raw wire bytes. |

Since v75 these are **play windows**, not merely connections — so one
connection can appear as several entries, split at each player↔spectator
crossing, and the userid repeats across them. mvdsv's `join` / `observe`
commands move a client between player and spectator without a reconnect
(`sv_user.c:2680-2830`) — same slot, same userid, one full
`svc_updateuserinfo` with `*spectator` flipped, and the mod's
`ClientDisconnect` run first, which on KTX broadcasts `"<name> left the game
with N frags"` while the match is running (`client.c:3022-3027`). That is a
departure, so the occupancy ends there and the SPECTATING half (the
*spectate stint*: the stint a live player entered by going spectator, or a
spectator left by joining) is withheld:
it has no player entity, and publishing it restated the leaver's window as
running to the end of the recording (on `ffa_1[tox]260818-1903.mvd` nexus
left at 263 681 ms and his session claimed 359 981). A connection that only
ever carried the `*spectator` key without crossing the line is untouched —
pre-KTX servers set that key on players, and on
`2on2_archive_dm4_qw240_recon` `math` streams 562 s of play under one such
userinfo.

Occupancies the wire never gave a userid of its own are **not** listed —
they are not connections. That is KTX's ghost scoreboard row (a departed
player's edict, userid hardcoded 0, `ghost2scores` in
`ktx/src/g_utils.c:2272-2356`) and inferred occupancies. The ghost carries the
departed player's *name*, so the identity unifier folds it into that player;
publishing it would give hub 216835's `rusti` a userid-less slot-10 window
**overlapping** the slot he had actually reconnected onto, and an entry with
no userid cannot answer the question the list exists for. Withheld for the
same reason: a connection **first attested at or after match end** — the
published window closes at match end, so it would emit `startMs > endMs`, an
inverted window contradicting the half-open contract, and it describes
nothing trackable inside the match. (A real shape: a spectator connecting
after the game to say gg, whose name or login unifies them with a player who
left mid-match. Applied only where a match end was detected.)

A row can therefore carry an `identity` with **no** `sessions` (every
occupancy unpublishable), which is a different statement from carrying no
identity at all — with no identity table the fields are absent because
nothing was measured.

### ChangeI16 / ChangeStr / Interval

The shared building blocks the `PlayerStream` fields above are made of — each
field is a list of one of these.

```
ChangeI16 = { "t": int32, "v": int16 }
ChangeStr = { "t": int32, "v": string }
Interval  = { "s": int32, "e": int32 }   // half-open [s, e)
```

`ChangeI16` / `ChangeStr` are entries in a **change stream**: a sparse series
that records one `{t, v}` only when the value *changes*; a reader carries the
last value forward until the next entry (so `h: [{t:0,v:100},{t:10000,v:50}]`
means health is 100 from `t=0` and 50 from `t=10000`). Health/Armor/Loc/ammo
use these. An `Interval` is a half-open `[s, e)` period during which something
was *true* (start included, end excluded) — weapons-held, powerups, and
`LosTrack.intervals` use these.

`t` / `s` / `e` are **integer milliseconds** since the stream's time
origin (see PositionTrack for the unit rationale).

**Dense/sparse key rule (the general form):** the time-field key follows
what the data scales with. **Sample-rate-scaled** arrays — the native-cadence
stream tracks (`PositionTrack.T`), the columnar `/buckets` grid, the aim
sample columns, projectile/beam fire-time columns — repeat their key once
**per sample**, so they take the terse **`t`** (and `Interval` keeps
`s`/`e`; `ChangeI16`/`ChangeStr` keep `t`/`v`) — the payload discipline is
deliberate. **Event-scaled** sparse lists and singleton timestamps —
frags, damage, shots, chat, backpacks, weapon-pickups, opening takes,
timeline events, the events/row-bucket/state-at/`firstTake` view surfaces —
carry the descriptive **`time`**. Both keys are always int32 ms; the unit
is never in the name (`timeUnit` echoes it). The same discipline names
per-track / per-body keys descriptively (`LosTrack` uses `other` /
`intervals`, not `o` / `iv`).

### LosTrack (`streams.players[].los[]`)

One entry per opponent this player (the **looker**) ever had a clear line of
sight to, as half-open `[s, e)` ms `Interval`s.

```
LosTrack = { "other": int16, "intervals": [Interval...] }   // other = index into streams.players (the seen player)
```

LOS is **computed lazily**, not during the default parse — it is the heaviest
position-derived pass and has no in-pipeline consumer. `analyzer.ComputeLOS`
populates it on demand (the web map's LOS overlay, `qw-analyze -include los`,
the mvd-api `GET /v1/demos/{id}/los` endpoint), idempotently. So a default
Result never carries `los`; it appears only in responses that requested it.

Line of sight is **asymmetric** — the looker's single eye point
(`origin + (0,0,22)`) versus the opponent's whole body — so `A→B` lives in
A's `los` and `B→A` in B's, computed independently. An interval is open while
at least one of the 9 rays from the looker's eye to the opponent's 8
bounding-box corners + box midpoint reaches the target without crossing
`CONTENTS_SOLID`: worldspawn geometry **or** any active mover (door / lift /
plat / train) posed in the way at that time. Computed only while both players
are alive, against the map's visibility BSP — present only on maps with a
provisioned BSP (same gate as `PositionTrack.h`/`lq`), absent otherwise. View
direction is not considered: this is geometric visibility, not FOV. Raw
transitions, no smoothing.

### PVS (`streams.players[].pvs[]`)

Same `LosTrack` shape as `los`, populated by the same lazy `analyzer.ComputeLOS`
pass under the same BSP gate, but recording **potential** visibility — and
specifically reproducing what a live **mvdsv** server would have sent. `pvs` is
on for opponent `O` at looker `L` exactly when the server's per-client entity
cull (`SV_PlayerVisibleToClient`) would have transmitted `O`'s entity to `L`'s
client that frame:

- **viewer side** — `L`'s **fat PVS**: `CM_FatPVS(origin+view_ofs)`, the OR of
  the PVS rows of every non-solid leaf within **8 units** of the eye
  (`view_ofs.z = 22`).
- **target side** — `O`'s **entity leaf set**: the non-solid leaves its bounding
  box touches, where the box is the player hull (`±16` xy, `−24/+32` z) **expanded
  1 unit** on every side (`SV_LinkEdict`). If that box touches more than
  `MAX_ENT_LEAFS` (16) leaves the server marks it always-sent — `pvs` is then on
  unconditionally.
- **test** — on iff any target leaf is set in the viewer's fat PVS.

This is *the wire*: the recorded MVD itself does **not** carry it — the demo
recorder is a fake client with `pvs = NULL` and stores every entity
(`SV_WriteEntitiesToClient`), so the per-client cull is reconstructed here from
the position tracks. (The one unavoidable approximation: we only have `origin`,
so `view_ofs.z` is taken as the standing `22`, exact for living players.)

**PVS ⊇ LOS by construction**: this wire PVS also gates the LOS raycast (LOS is
cast only for potentially-visible pairs), so every `los` interval lies inside a
`pvs` interval for the same opponent; because the PVS is a conservative superset
of true reachability the gate loses no real sightline. The gap between them — on
the wire but no clear ray — is an occlusion-tolerant proximity/awareness signal.
Like `los`, it is per-ordered-pair (`other` indexes `streams.players`), computed only
while both players are alive, raw transitions with no smoothing.

### PositionTrack

Columnar to compress JSON. Indices align across all arrays. `t`/`x`/`y`/`z`
are always present; `li`, `h`, `lq`, `vp`, `vya`, `vx`, `vy`, `vz` are
optional (`omitempty`) per-sample columns populated during analysis when
their inputs are available.

```
PositionTrack = {
  "t": [int32...], "x": [float32...], "y": [float32...], "z": [float32...],
  "li":  [int16...],   // optional: loc index per sample
  "h":   [float32...], // optional: height above floor per sample
  "lq":  [int8...],    // optional: liquid state per sample
  "vp":  [int16...],   // optional: view pitch per sample (raw angle16)
  "vya": [int16...],   // optional: view yaw per sample (raw angle16)
  "vx":  [float32...], // optional: velocity X per sample (units/sec)
  "vy":  [float32...], // optional: velocity Y per sample (units/sec)
  "vz":  [float32...]  // optional: velocity Z per sample (units/sec)
}
```

`t` is **integer milliseconds** since the stream's time origin. The
MVD wire format delivers a 1-byte ms delta per message; storing the
cumulative value
as `int32` keeps it exact across the persistence boundary. Consumers
reading the JSON as seconds must scale by `* 0.001`. Range is ±24.8
days, ample for matches that run minutes to hours; values can go
negative for pre-match warmup samples after time normalisation.

`x` / `y` / `z` are `float32` — the wire-native sub-unit origin
(mvd-reader decodes coordinates as float32; the MVD wire carries them as
eighth-unit fixed point, or as true floats under the float-coords
extension). **Since schema v33** they are no longer rounded to whole
units: schema v32 and earlier stored `int32`, silently truncating up to
~1 unit per axis, which also coarsened the derived velocity. Quake maps
exceed ±32 768 in any axis, so `int16` was never an option. The values
are kept at **native float32 resolution in memory**; only the JSON text
is rounded — to 3 decimals, which is lossless for eighth-unit coordinates
(`.125`/`.25`/… round-trip exactly) and just sheds float artifacts.

`li` (when present) is the resolved loc-name index for each sample —
indexes into `TimelineAnalysisResult.LocTable`, `0` = "no loc",
smoothed by the blip filter. Same length as `t`. Absent when no `.loc`
corpus is loaded for the map. (Distinct from `PlayerStream.Loc`, which
is the *sparse* change-stream view of the same data.)

`h` (when present) is the **player's height above the floor
beneath them** at each sample — how far the feet are above the highest
solid surface at or below the player, from straight-down traces through
the map's player clip hulls (parsed from the map's BSP `CLIPNODES` at
analyze time; see `mvd-analytics/mapclip`). Same length as `t`. It is
measured over the player's bounding-box footprint,
not just the origin column: the highest floor under a 3×3 grid of
columns sampled ±8 around the origin wins (an effective ~48-wide
footprint on the already-±16-box-inflated hull), so a player skimming a
ledge / well rim — origin momentarily over the pit while the box overhangs
the rim — reads the **near** floor, not the distant one far below (this is
what removed bogus high airgibs at spots like anwalked RA's well rim).
The trace scene also includes every moving
brush-model entity (lift, door, train) posed at its demo-streamed origin
for the sample's time, so a player riding the dm2 RA lift stands on the
lift, not the shaft floor beneath it. It
reads **~0 when grounded** and grows positive during a jump or airborne
hit (airgib), so
a consumer flags those directly with no coordinate arithmetic — test
`|h|` small rather than `== 0`, since slopes and the trace epsilon leave
a unit or two of slack. (The absolute floor surface, if needed, is
`z[i] - 24 - h[i]` — the player origin rides 24 units above the floor.)
Liquids participate: a sample in liquid (`lq`
level ≥ 1) reads `h = 0` by definition — the liquid surface is the
support, so swimmers don't read as airborne over the pool bottom — and
a dry sample airborne above water/slime/lava measures down to the
**liquid surface** when it is the highest support beneath the player.
The sentinel `-1000000000` (`result.NoFloor`; was `-2147483648` while
`h` was `int32`) marks a sample with **no floor to
measure from** — over a void / bottomless pit, an embedded origin, or
the zero origin. Absent entirely when no BSP is
provisioned for the map (same best-effort BSP source as the
visibility-aware loc filter), so floor height and PVS-veto loc
attribution light up together.

`lq` (when present) is the **per-sample liquid state**,
computed by mirroring the engine's `PM_CategorizePosition` waterlevel
probes (feet z−23, waist z+4, eyes z+22) against the map's render BSP:
`0` = dry, otherwise `(type << 2) | level` with level 1–3
(feet/waist/eyes submerged) and type 1 water / 2 slime / 3 lava — so
water reads 5/6/7, slime 9/10/11, lava 13/14/15. Decode with `lq & 3`
(level) and `lq >> 2` (type); Go consumers use `result.LqLevel` for the
level and `lq >> 2` with the `result.LqWater/LqSlime/LqLava` constants
for the type.
Same length as `t`; absent when no BSP is provisioned. (One deliberate
deviation from the engine predicate: `CONTENTS_SKY` does **not** count
as liquid — the physics treats sky like water for drag, but a
void-faller reported as swimming would mislead consumers.)

`vp` / `vya` (when present) are the **player's view
direction** — pitch and yaw — at each sample, stored as the **raw
`angle16` state** (the exact 2-byte values, kept losslessly after
`svc_playerinfo` delta carry-forward; see MVD_FORMAT.md "View-angle
semantics"). Decode to degrees with `deg = uint16(v) * 360/65536`;
values land in `[0,360)`, so a pitch
**> 180° means looking up**. Roll is not stored (the server forces it to
0). A forward unit vector is one trig call away — with `p`, `y` the
decoded pitch/yaw in radians,
`forward = (cos p·cos y, cos p·sin y, −sin p)`. Same length as `t`;
populated whenever the track is (the angles ride the same
`svc_playerinfo` samples as x/y/z, so unlike `h`/`lq` they need no BSP).

`vx` / `vy` / `vz` (when present) are the player's
**velocity** in Quake units/sec at each sample — **derived**, not a wire
field, from the position columns by a central-difference estimator
(second-order accurate). The estimator does not differentiate across a
respawn teleport, a map-teleporter relocation, or an abnormal time gap
(death / pause / reconnect): such a step reads ~0 rather than a
tens-of-thousands-ups spike, and an isolated sample reads 0. The source
`x`/`y`/`z` are float32 (no longer rounded to
whole units), so the derivative is sub-unit precise — the ±1-unit
position quantization that used to add a few tens of ups of noise is
gone. Like positions, velocity is native float32 in memory and the JSON
text is rounded to 3 decimals (the float32 division tail, e.g.
`-58.333332`, is false precision below the estimator's noise floor);
smooth client-side only if a softer curve is wanted. Speed is `hypot(vx,vy,vz)`;
horizontal speed (the usual movement metric) is `hypot(vx,vy)`. Same
length as `t`; populated whenever the track is (no BSP needed).

### MoverStream (`streams.movers[]`)

The pose timeline of one brush-model entity — a lift, door, plat or
train. Columnar like PositionTrack; indices align across `t`/`x`/`y`/`z`/`vis`.

```
MoverStream = {
  "ent": int,            // MVD entity number
  "sub": int,            // brush-model index ("*sub"); matches the corpus SubModelMesh id
  "t":   [int32...],     // match-relative milliseconds
  "x": [float32...], "y": [float32...], "z": [float32...],  // origin per sample
  "vis": [bool...]       // whether the mover is drawn at that sample
}
```

At `t[i]` ms the mover sits at `(x,y,z)[i]` and is rendered when
`vis[i]`. A renderer offsets the map-geometry `SubModelMesh` whose `id`
equals `sub` by `(x,y,z)` to place it. Origins are `float32` (wire
values are exact ⅛-unit multiples; `int32` would quantize the pose
stepping). Tracks are **short**: MVD delta compression only re-sends an
origin when it changes, so a parked mover is a single entry and a
travelling one re-sends per frame only while in motion. The first entry
is clamped to `t = 0` carrying the **match-start pose**, so a parked
mover whose only wire state predates the match still has a pose to draw;
earlier pre-match states are dropped as superseded. Absent (`omitempty`)
when the demo has no movers. The same internal mover tracks already feed
the v27 floor-height pass (players ride lifts).

### Time units: all times are int32 milliseconds

Every timestamped field in this schema — `PositionTrack.T`,
`PlayerStream.Spawns/Deaths`, `ChangeI16.T` / `ChangeStr.T`,
`Interval.Start/End`, `GlobalStream.MatchStart/End/DemoOffset`,
`GlobalStream.Pauses[].AtMs/DurationMs`,
`MatchResult.Duration`,
`TimelineFragEvent.Time`, `PowerupEvent.Time/EndTime/Duration`,
`FragStreakEvent.Time/EndTime/Duration`, `MatchEvent.Time`,
`FragEntry.Time`, `BackpackDrop.Time`,
`WeaponPickup.Time/NextDeathTime/DropTime`,
`ItemPhase.AvailableFrom/TakenAt/RespawnAt` —
is stored as `int32` integer milliseconds. External consumers that
want seconds must scale by `* 0.001`.
As of schema v57 the view-layer query API (`view.Buckets`, `view.Events`,
`view.StreamSlice.Start/End`, `view.StateAt.Time`) also takes and returns
**int32 ms** at its public surface — the pure-ms model, no seconds
anywhere in `view.*`.

#### Transport surface: the `timeUnit` echo (schema v57 — pure ms)

The **stored `result.*` structs are unchanged — still `int32` ms** as
listed above. The v57 transport is now **all int32 ms too**, inputs and
outputs, REST and MCP alike — the one-rule model:

- **Every time value in a response is int32 ms.** The v56 seconds
  surfaces (`/events`, `/state-at`, `/stream-slice` envelope,
  `/loc-trails`, `/buckets?layout=row`, the `/items` summary) were flipped
  to int32 ms in v57 — but under the SAME descriptive key they carried in
  v55: **`time`** (events row, row-bucket, state-at envelope, items
  `firstTake`). That is the dense/sparse rule (see the [ChangeI16 /
  Interval note](#changei16--changestr--interval)): event-scaled sparse
  surfaces use `time`, sample-rate-scaled dense arrays use `t`; both are
  int32 ms. Envelope bounds are `start`/`end` (stream-slice) or
  `start`/`windowMs` (columnar buckets), all int32 ms. `view.UnitSec` is
  deleted.
- **`timeUnit` is a constant `"ms"` self-description echo.** Every
  `/v1/demos/{id}/*` response that carries match-position time values
  echoes a top-level **`"timeUnit":"ms"`**. `/artifacts/{name}` is no
  exception: since Phase 5 it carries a `"timeUnit":"ms"` sibling for
  every time-bearing section it serves (`/artifacts/los`, aliasing `/los`,
  is one such). `/loc-graph` echoes too — its node weights are aggregate
  durations, int32 ms since v57. The exceptions carry no match-relative
  time and no echo: `/demoinfo` (KTX's own clock, a mix of native units —
  §DemoInfoResult) is the sole seconds island, and the genuinely timeless
  sections — `/metadata`, `/loc-table`, `/maps/{map}/entities` — carry no
  echo.
- **Time-valued query params are int32 ms too.** `from` / `to` / `time`
  on demo endpoints are integer milliseconds; a non-integer value 400s
  `invalid_param` with an `(integer milliseconds)` hint. Search
  `from`/`to` are calendar dates (`YYYY-MM-DD`), not times.

See [mvd-api/API.md §2.1](../mvd-api/API.md) for the full endpoint matrix
and the authoritative per-operation shapes in
`mvd-api/openapi/openapi.yaml`.

#### Why integer ms

The MVD wire format carries time as a 1-byte millisecond delta per
message; the decoder accumulator (`mvd.Decoder.timeMs`) keeps this
integer end-to-end. Float seconds is a derived view, not a source of
truth. Integer storage:

- Eliminates float-precision drift across boundary comparisons. The
  motivating bug was a gib-respawn case where a spawn-boundary at
  wire-exact `658.279` compared against a position sample narrowed to
  `658.278992` produced a spurious `MH.low → start` teleport edge.
- Keeps comparison cost flat — `int32 <= int32` is exact.
- Removes float-noise artefacts (`5.499999999999972`) from JSON,
  making goldens stable and JSON human-readable.
- `int32` ms = ±24.8 days, comfortably more than any match.

#### Adding a new timestamped field

1. **Storage**: `int32` ms in the result schema. Same JSON-key shape
   as adjacent fields.
2. **Producer** (`mvd-analytics/analyzer/`): every source event
   carries `TimeMs int32` (`e.EventTimeMs()` on the interface) — use
   it directly. The analyzers consume integer ms end to end; there is
   no float-seconds time intermediate. `events.Sec(ms)` exists only to
   format human-readable seconds at a presentation edge (never in a
   result field).
3. **Postprocess** (`normalizeMatchRelativeTimes` in
   `analyzer/postprocess.go`): if the field shifts with match start,
   add it there. Everything works in int32 ms;
   `matchStartMs` comes from `res.Streams.Global.MatchStart`
   (pre-normalize, the demo-relative match start) directly.
4. **View layer** (`mvd-analytics/view/`): if the field is queryable
   via `view.Buckets` / `view.Events` / `view.StreamSlice` /
   `view.StateAt`, follow the existing pattern — window bounds and
   emitted timestamps are all int32 ms end-to-end (pure-ms model, v57);
   no float conversion at the boundary.
5. **Tests**: write fixtures with int32-ms literals (`Time: 5000`,
   not `Time: 5.0`).
6. **Frontend** (`mvd-web/static/app.js`): the field is int32 ms whether
   read from the raw schema or via the view layer — no `* 0.001` scaling.

### Append rules (the dedup invariant)

- **Change streams** (Health, Armor, ArmorType, Loc, ammo): every entry
  is a transition. `appendChange(t, v)` appends only if `v` differs
  from the previous entry's value. Consecutive identical samples are
  dropped.
- **Position**: every native sample is appended without dedup.
  Positions almost always differ; checking is overhead with no payoff.
- **Intervals** (weapons, powerups): one entry per period the field
  was true. Anchor opens on `false→true`, closes on `true→false` or at
  match end.
- **Spawn / Death timestamps**: discrete events, just appended.

### Identity / disambiguation (D12)

`PlayerStream.Name` is the canonical demoinfo-resolved name. If two
slots resolve to the same canonical name within one match (rare —
typical in pickup games where two players both pick "Player"), the
later slot's stream is suffixed `name#slotIndex`. Mid-match name
changes are folded into the same stream by the analyser's existing
canonicalisation.

## Query API

Provided by `mvd-analytics/view`. All functions are pure: no I/O, no
shared mutable state, no mutation of the input `*Result`.

### Field vocabulary

These codes are used identically in JSON wire keys, view-API
parameters, CLI `-fields` values, and (future) MCP tool inputs.

All default reducers use **first-sample-of-bucket** semantics: bucket
N's value represents player state at time `t = N × bucketDur`.
Bucket 0 is match-start state, consistent with the timeline-playback
mental model where each bucket is a snapshot at its own T. Override
per-call via `BucketsOptions.Reducers` if you want analytics-style
aggregation (`min`, `max`, `mean`, `dominant`, etc.).

| Code | Field | Stream form | Default reducer |
|------|-------|-------------|-----------------|
| `h` | Health | `[]ChangeI16` | `first` |
| `a` | Armor | `[]ChangeI16` | `first` |
| `at` | Armor type | `[]ChangeStr` | `first` |
| `li` | Loc index | `[]ChangeI16` | `first` |
| `pos` | Position xyz | `*PositionTrack` | `first` |
| `view` | View direction (pitch/yaw, raw angle16) | `*PositionTrack` (vp/vya) | `first` |
| `hgt` | Height above floor | `*PositionTrack` (h) | `first` |
| `lq` | Liquid state | `*PositionTrack` (lq) | `first` |
| `vel` | Velocity (vx/vy/vz, units/sec) | `*PositionTrack` (vx/vy/vz) | `first` |
| `rl` | Rocket Launcher held | `[]Interval` | `first` |
| `lg` | Lightning Gun held | `[]Interval` | `first` |
| `gl` | Grenade Launcher held | `[]Interval` | `first` |
| `ssg` | Super Shotgun held | `[]Interval` | `first` |
| `sng` | Super Nailgun held | `[]Interval` | `first` |
| `q` | Quad | `[]Interval` | `first` |
| `pe` | Pentagram | `[]Interval` | `first` |
| `r` | Ring of Shadows | `[]Interval` | `first` |
| `sh` | Shells | `[]ChangeI16` | `first` |
| `nl` | Nails | `[]ChangeI16` | `first` |
| `rk` | Rockets | `[]ChangeI16` | `first` |
| `cl` | Cells | `[]ChangeI16` | `first` |
| `aw` | Wielded weapon bit (opt-in) | `[]ChangeI16` | `first` |
| `sp` | Spawn timestamps | `[]int32` | `any` |
| `d` | Death timestamps | `[]int32` | `any` |

`sp` / `d` stay on `any` because they need a bool ("did this event
happen during the bucket?"); `first` would return a timestamp.

**`view` / `hgt` / `lq` / `vel` / `aw` are opt-in** — they are *not* in
the default field set (`AllStandardFields`), so a query that omits
`fields` keeps the pre-v31 shape and a consumer only pays for view
direction, floor height, liquid state, velocity, or the wielded weapon
when it asks for the code explicitly. `aw` (v72) is a change stream on
`PlayerStream.ActiveWeapon`, not a position projection. They all read from the player's `*PositionTrack` but project
disjoint columns: `view` → `vp`/`vya`, `hgt` → `h`, `lq` → `lq`,
`vel` → `vx`/`vy`/`vz`. **Clean break (schema v31):** `pos` now returns
**strictly** `x`/`y`/`z` (plus the per-sample loc label `li`); height and
liquid no longer ride along it — request `hgt` / `lq` for those. In
`view.StreamSlice` each projects into its own sibling track
(`pos`/`view`/`hgt`/`lq`/`vel`); in `view.Buckets` `view` and `vel`
reduce to a vector (`[vp, vya]` / `[vx, vy, vz]`, split to columns in the
columnar layout), `hgt` to a scalar height (so `mean`/`min`/`max` give a
jump apex / average), `lq` to a scalar liquid code; in `view.StateAt`
they surface as `view` (`{vp, vya}`), `hgt`, `lq`, and `vel`
(`{vx, vy, vz}`). (The stored `result.PositionTrack` still carries every
column — the split is purely in the query projection; the WASM frontend
reads the track directly and is unaffected.)

### Reducer registry

| Name | Behavior | Applies to |
|------|----------|------------|
| `last` | Value at end of window (carry-forward if no change). | Numeric / categorical. |
| `first` | Value at start of window. | Numeric / categorical. |
| `mean` | Arithmetic mean over samples. | Numeric. |
| `min` / `max` | Extrema over samples. | Numeric. |
| `dominant` | Mode (most common value); ties broken by `last`. | Categorical. |
| `held-any` | OR over a bool stream — true if any sample is true. | Bool / interval. |
| `majority` | True if held ≥ 50 % of window samples. | Bool / interval. |
| `any` | True if at least one event is in the window. | Event lists (spawn/death). |

Override per call via `BucketsOptions.Reducers`:

```json
{ "windowMs": 1000, "reducers": { "h": "min", "rl": "majority" } }
```

Unknown reducer name → explicit error from `view.Buckets`. Unknown
field codes also error.

### View functions

#### Buckets

```go
view.Buckets(r, view.BucketsOptions{
    WindowMs: 1000,
    Fields:   []string{"h", "a", "rl"},
    Players:  []string{"bps", "griffin"},
    Reducers: map[string]string{"h": "mean"},
    IncludeTeam: true,
})
// → *BucketsView { WindowMs, Buckets: []ViewBucket }
```

Partial last bucket carries `Partial: true` when the window doesn't
divide evenly into `EndTime - StartTime`.

Loc rendering follows `BucketsOptions.LocIndex` (REST `?loc=`): by
default each bucket's player map carries a resolved `loc` name; in
index mode (`loc=index`) it carries the raw `li` integer instead, which
you decode against the demo's loc-table (`GET /loc-table`).

##### Columnar layout (`view.BucketsColumnar`, REST `?layout=column`)

The same per-bucket values in a column-major shape — for each
`(player, field)` one dense typed array instead of a map per bucket.
Far smaller and allocation-light for series/trend reads; use
`StateAt` for point-in-time snapshots rather than aligning indices
across arrays.

```go
view.BucketsColumnar(r, view.BucketsOptions{WindowMs: 50, IncludeTeam: true})
// → *ColumnarBuckets {
//     windowMs, start, count, partialLastMs?,
//     locTable?: ["", "RA", …],           // li legend (v53); present iff an li column is emitted
//     players: { name: {
//        first, n,                       // active span [first, first+n)
//        alive: [0/1 …],                 // liveness per bucket in the span
//        validFrom: { field: idx },      // sparse; field valid from idx (omitted when == first)
//        h|a|li|sh|nl|rk|cl: [int16 …],  // dense, carry-forward
//        x|y|z: [float32 …],             // position split
//        vx|vy|vz: [float32 …],          // velocity split; hgt: [float32 …]
//        at: [string …],
//        rl|lg|gl|ssg|sng|q|pe|r|sp|d: [0/1 …],
//     } },
//     teams: { name: { rl|lg|rllg|w|gl|q|pe|r|pw|th|ta: [int …],
//                      abt: { ra|ya|ga: [int …] } } },
//   }
```

Conventions: `time(i) = start + i*windowMs` (int32 ms); booleans and
the `alive` mask are `0`/`1`; the `li` column keeps the compact raw
index and the envelope's `locTable` legend decodes it (schema v53 —
identical content to `/loc-table`, index 0 = the `""` no-loc sentinel),
so a columnar response is loc-self-contained; a field array is omitted
when the player never has it; values carry forward through dead buckets
(the `alive`
mask, not the arrays, marks liveness — row-major omits dead players, so
treat `alive[i]==0` as "absent"); loc is always the raw `li` index
(`LocIndex` does not apply). Team arrays span the full `count` grid.

The bucket `alive` mask is **not** a resampling of
`streams.players[].alive`: it comes from `view.playerActiveInWindow`, a
window-**overlap** test ("did this player appear anywhere in this
bucket") with its own fallbacks, deliberately kept separate because it
answers a different question from the instantaneous one. The two can
disagree on a bucket containing a death or a spawn, and neither is
wrong.

There is no per-life table: it would be a bucket-resolution approximation
that undercounts a death+respawn falling in one window. A same-window
death+respawn surfaces as that bucket carrying both `d=1` and `sp=1`
while `alive` stays `1`; for authoritative life counts/durations read
`streams.players[].alive`, the canonical life list — one interval per
life, with same-millisecond death+respawn kept as two touching
intervals. Don't re-derive lives from the raw `sp`/`d` markers: that is
exactly the re-derivation `alive` exists to replace.

#### Events

```go
view.Events(r, view.EventsFilter{
    StartTime: 60000, EndTime: 120000, // int32 ms
    Types: []string{"frag", "powerup"},
})
// → *EventsView { Events: []TaggedEvent }
```

Default Types omits high-frequency change events (`health`, `armor`,
`loc`); pass them explicitly to opt back in. A `loc` event's `detail`
holds the resolved name (`{"loc":"RA"}`) by default, or the raw index
(`{"li":7}`) with `loc=index` — decode via `GET /loc-table`.

The default set includes `pickup` (schema v51): identity-rich pickups
joined from the authoritative sections rather than the held-interval
streams. World-spawner takes (any kind, weapons included) come from the
per-spawner item timelines — `detail{ item, kind, entNum, loc?, source:
"world", team? }` with `item` the disambiguated spawner name (`ya_1` vs
`ya_2`); backpack / unknown-source weapon grants come from
weaponPickups — `detail{ item, kind, source, entNum?, dropper?, team? }`
where `entNum` is the backpack edict. The two sources are disjoint by
construction (a backpack grab never flips the world spawner's entity
state), so no take is double-reported. The interval-derived `weapon` /
`item` gain–lose events are unchanged — they tell the *holding* story.

`spawn` events carry the spawn location when resolvable:
`detail{"loc": name}` (or `{"li": idx}` with `loc=index`), sampled from
the loc stream just after the spawn — the first change entry after the
spawn timestamp is the teleport landing; no change inside the window
means the loc didn't change across the spawn (schema v51).

The opt-in `telefrag` / `stomp` events carry the kill's folded value
(schema v54): `detail{ victim, isTeam?, bounded?, damage? }` — `bounded`
is the reconstruction folded into the damage aggregates (present exactly
when the fold ran; `0` is a real nullified-stomp value), and a stomp
adds `damage` when its raw fold diverged from `bounded`. Mirrors the
`telefrags[]`/`stomps[]` entries in the damage section.

The opt-in `damage` events mirror the stored per-hit log (schema v59):
`detail.damage` is the **unbounded wire value** and `detail.bounded` is
the stored KTX-scoreboard reconstruction, passed through with the same
omitted-when-equal convention as `damage.events[].bounded` (absent
entirely on `skipped:*` demos). `/damage` defaults to the bounded
family, so cross-check its figures against `detail.bounded`, not
`detail.damage`.

The default set also includes `airgib` and `pause` (view-layer change,
no schema bump — they surface existing Result data on the MCP-reachable
event stream). An `airgib` event is a direct enemy rocket hit on an
airborne victim (from `highlights.airgibs`): `player` is the
attacker and `detail{ victim, height, damage, attackerTeam?, victimTeam?,
heightAboveAttacker?, loc?, lethal? }` (`lethal` omitted when false). A
`pause` event is a game-clock freeze segment (from
`streams.global.pauses`): it has **no** `player` — so a `players=` filter
excludes it — and carries `detail{ durationMs }` (the real wall-clock ms
the pause consumed).

The `discharge` / `quadbore` events (v76, view-layer) are the
[highlights](#highlightsresult-highlights) kinds — one event per
`highlights.discharges[]` / `highlights.quadbores[]` row. In the default
set like `airgib`: the `frag` event carries only the score delta, so
nothing else in the feed says a death was a discharge.
`player` is the actor (the discharger / the player who bored) and
`detail{ victims[], enemyKills, teamKills, actorKilled?, damage?, cells?,
weapon?, quadHeldMs, quadFrags, team? }`: `victims` is every victim's
name (killed or merely hurt — read the highlights row for the split),
`actorKilled` is present only when the actor died, `damage` only when
the damage log dealt some, `cells` only on a discharge with a cells
sample, `weapon` / `quadHeldMs` / `quadFrags` only on a quadbore, `team`
the actor's team when known. A `players=` filter matches the actor or
any victim.

#### StreamSlice

```go
view.StreamSlice(r, view.StreamSliceOptions{
    Start:   432000, End: 442000, // int32 ms
    Players: []string{"bps"},
    Fields:  []string{"h", "a", "rl", "pe"},
})
// → *StreamSliceView { Start, End, Players: []PlayerSlice }
```

Raw, unreduced change entries falling in `[Start, End)` (int32 ms). For
each requested field, a synthetic carry-forward entry is prepended at
`Start` showing the value at window entry; intervals overlapping
the window are clamped.

The loc field is resolved to loc **names** by default (JSON key `loc`,
`[]ChangeStr`) so consumers never need the table. Pass `loc=index` to
get the raw `li` index stream (`[]ChangeI16`) instead — decode it via
`GET /loc-table`.

Each player also carries **`alive`** (schema v65): the stored
[`streams.players[].alive`](#playerstream) lives, clamped to
`[Start, End)` exactly like `rl`/`lg`/`q`. This is the one place liveness
reaches a REST consumer, and it exists so nobody re-derives it from the
`sp`/`d` markers beside it — the strict `lastSpawn > lastDeath` form of
that re-derivation latches on a same-millisecond death+respawn and
reports the player dead for the rest of the life (100.7 s of one
player's match, measured; see the `Alive` row of
[PlayerStream](#playerstream)). It is **not** `omitempty` and **not**
field-gated, both because `null` is a state: a key that could be omitted
would make `null` mean "not requested" as well as "not measurable". The
three states survive the clip — `null` stays `null`, `[]` stays `[]`,
and a measured list that the window leaves nothing of comes back `[]`
("measured, never alive **in this window**"), never `null`.

#### StateAt

```go
view.StateAt(r, view.StateAtOptions{
    Time:    432500, // int32 ms
    Players: []string{"bps"},
    Fields:  []string{"h", "a", "rl", "pos"},
})
// → *StateAtView { Time (JSON key "time"), Players: map[string]PlayerStateAt }
```

Resolves each requested field at `Time`. Change streams use latest
entry with `T <= Time` (carry-forward). Intervals: `true` iff `Time` ∈
some interval. Position: nearest sample by `T`. The loc field comes
back as a resolved name by default (JSON key `loc`, string); pass
`loc=index` for the raw `li` index — decode via `GET /loc-table`.

Two fields were added in schema v65. Only `alive` is ungated;
`posAgeMs` rides the positional fields:

| Field | Type | Meaning |
|---|---|---|
| `alive` | `*bool` (**never** omitempty) | Liveness at `Time`, read from the stored [`streams.players[].alive`](#playerstream) and never re-derived from the `sp`/`d` markers. `true`/`false` = measured, `null` = liveness was not measurable for that player — the same three states the stored field carries, collapsed to an instant. Not field-gated, for the same reason it is not `omitempty`: `null` is a state, so a key that could be omitted would also mean "not requested". |
| `posAgeMs` | `*int32` (omitempty) | `Time` minus the timestamp of the position sample `pos`/`view`/`hgt`/`lq`/`vel` were snapped to. **Positive** = the state is carried forward from an earlier sample; **negative** = the nearest sample is a *later* one (`Time` precedes the first sample, or falls in the earlier half of a gap). Emitted iff `Fields` asked for at least one of those five AND a sample resolved (`view/stateat.go`) — a non-positional field set never carries it. |

`posAgeMs` exists because the reported position is a carry-forward with
no staleness bound of its own — which stays true, deliberately: this
endpoint reports the raw nearest sample and lets the caller judge it.
The occupancy surfaces do not have that freedom and reject a sample once
`age >= result.SampleStaleCapMs` (250 ms — the boundary itself is
rejected, `region_control.go` `sampleAt`), so a consumer that applies the
same rule to `posAgeMs` gets `/state-at`, `/region-control`,
`/loc-graph` and `/loc-trails` answering "where was X at t"
consistently. An absent `posAgeMs` never means "fresh" — but it does not
mean "no samples" either: on a field set with no positional field in it
the key is simply not applicable, so absence only carries information
about the demo's position track when a positional field *was* requested.

#### LocTrails

Per-player loc residences with dwell durations. `MinDwellMs` folds
short blips into adjacent stable residences (defaults to 0 = no
filter; the analyser's pre-existing blip filter has already smoothed
the underlying loc stream). Each residence carries the loc **name**
(`loc`) by default, or the raw index (`li`) with `loc=index` — decode
via `GET /loc-table`.

A residence is dwell, i.e. **presence**, so since v64 it carries the
same bounds as loc-graph node time and region-control presence:

- **Alive-gated.** Each residence is intersected with
  `streams.players[].alive`, so it is truncated at a death and resumes
  at the respawn. A `null` (unmeasurable) `alive` degrades to ungated,
  the same as the occupancy walkers.
- **Bounded at end-of-track.** A player's last residence ends at
  `result.TrackHoldEnd` — their final position sample held for one
  measured cadence, capped at `result.SampleStaleCapMs` (250 ms) — not
  at match end, so an early quitter does not hold their last loc to the
  final whistle.
- **The merge never crosses a gap.** `MinDwellMs > 0` folds a short
  residence into the *preceding* one by extending its `End`, and the
  gaps the alive gate (or an unresolved loc) just cut out are time that
  was deliberately removed. So no fold and no same-loc coalesce ever
  spans a gap, at any `MinDwellMs`, and a residence that *follows* a gap
  is emitted even when it is shorter than `MinDwellMs` — folding it
  anywhere would hand the removed time back as dwell. Before this rule
  a player alive in one loc 0–30 s but dead 10–20 s came back as a
  single 30 s residence at the MCP default `minDwellMs=250`.

##### Loc representation (shared)

Every loc-bearing view (Buckets, Events, StreamSlice, StateAt,
LocTrails) renders loc as a resolved **name** by default. Pass
`loc=index` (REST query param; `LocIndex: true` on the Go options) to
get the raw `LocTable` index instead — useful for index-based
computation (transition matrices, clustering). Fetch the decoder once
from `GET /v1/demos/{id}/loc-table` → `{ "locTable": [...] }` (index 0
is the `""` no-loc sentinel). RegionControl is unaffected — it reports
region names, not single loc indices.

#### RegionControl

Re-derives per-bucket region state strings (`bucketStates`) at the
requested `WindowMs` and the match-aggregate `stats` (percentages +
per-region per-player attribution `RegionStats.byPlayer`, the latter in
integer milliseconds) as the exact time-weighted integral over the
native position sample times, independent of `WindowMs` — so the
aggregate stays stable across display windows instead of tracking the
point-sample grid. `WindowMs` affects only `bucketStates`. Both are
optionally clipped to a `[StartTime, EndTime)` sub-window. Options
(`RegionControlOptions`) optionally override the regions (caller-
edited region defs from the web UI), `TeamA`/`TeamB` labels, and
the `teamOf` lookup; defaults pull from
`TimelineAnalysisResult.RegionControl.Regions` (set at parse time)
and `r.Match.Players` (post-normalize team mapping). No `Players`
filter — region control is by team; filtering individuals would
skew the team tallies. To attribute control to specific players,
read the `byPlayer` field on each `RegionStats`.

The function's view-layer return type is aliased as
`RegionControlView = result.RegionControlResult` so the
`XxxView` naming is symmetric with the other views;
the aliased type is the canonical one because the same shape is
baked into parse-time Result.

#### Interval segmentations (`TopWindows`, `Lives`) — schema v65

Two views cut the match into intervals and describe each one with the
**same** stats block. They differ only in how the intervals are chosen:

| View | REST | Segmentation |
|---|---|---|
| `TopWindows` | `/top-windows` | fixed-length **or** gap-delimited windows (`mode`), ranked by a caller-chosen metric |
| `Lives` | `/lives` | spawn → death, one row per life |

Everything shared between them — `IntervalStats`, `IntervalLoc` and
`MeasuredSources` — is documented once under
[The shared interval stats block](#the-shared-interval-stats-block)
below. The third segmentation over the same primitive,
[`TopKills`](#kill-bursts-topkills--schema-v67), turned out **not** to be an
interval reduction — a burst row is a small bespoke walk, not a stats block —
so it fills no `IntervalStats`; it does share the `measured` marker and the
damage-family echo.
Both live in `view/` (`topwindows.go`, `lives.go`,
`interval_stats.go`); neither is a DAG artifact and neither changes the
stored `Result`.

Note the filename: `topwindows.go`, **not** `top_windows.go`. Go reads a
`_windows.go` suffix as a `GOOS` build constraint, so the latter would
compile on Windows only and every symbol in it would read as undefined
everywhere else. Same trap for `_linux`, `_darwin`, `_amd64`.

##### TopWindows (`view.TopWindows`, REST `/top-windows`)

```go
view.TopWindows(r, view.TopWindowsOptions{
    Metric:    "netFrags",  // "" → frags
    Mode:      "fixed",     // "" → fixed; "gap" is the other segmentation
    WindowMs:  30000,       // fixed only; <=0 → 30000. Rejected under gap
    GapMs:     0,           // gap only, REQUIRED there (no default)
    Limit:     10,          // 0 → 10, <0 → uncapped, clamped to 200
    PerPlayer: 0,           // <=0 → uncapped
    Players:   []string{"ParadokS"},
    Weapons:   []string{"lg"},
    From: 0, To: 0,         // int32 ms; 0 = no bound
    Dmg:       "bounded",   // view default raw; REST default bounded
    Min:       nil,         // nil → 1; *int because 0 is meaningful
})
// → *TopWindowsView
```

Two segmentations, selected by `mode` (`""`/`fixed` → fixed,
`gap` → gap-delimited; matched case-insensitively). Each has a
one-sentence contract, and everything after the segmentation — the metric
vocabulary, ranking and tie-break, both caps, `weapons=` scoping, `min`,
the stats block and the score-equals-stat invariant — is shared.

**Fixed mode (the default).** The contract is one sentence: *"in these
`windowMs` milliseconds this
player scored higher on `metric` than in any other stretch of the same
length."* Candidate windows are anchored at **real event times** (plus
`t+1` and `t−windowMs`, the breakpoints the signed metrics need), never
on a grid, so "the best 30 s" is exact. Per player they are taken
greedily and **non-overlapping** — touching counts as overlapping, since
spans are closed at both ends — then merged into one global ranking.

**Ordering (schema v67): `score`, then a fixed complementary metric,
then `start`, `end`, `player`.** Ties on the metric are the common case
— on `metric=frags` most of a page holds the same small integer — so
equal-scoring rows are ranked by the other half of the same moment:
`damageGiven` under `frags`/`netFrags`/`shots`/`hits`, `frags` under
`damageGiven`/`netDamage`, `damageTaken` under `deaths`, `deaths` under
`damageTaken`. It is fixed per metric, not a parameter. The secondary is
summed **unscoped and in the response's own damage family** (the `dmg`
echo), so it is exactly the same-named field of the row's own stats
block — `weapons=` scopes the score and never the tie-break, and a
`skipped:*` demo's raw fallback applies to both. The same key ranks a
player's overlapping equal-scoring **candidates** before the greedy pass,
above the "start on a scoring event" preference, so a reported window may
be the stretch that ENDS on the scoring event rather than the one that
starts on it. A demo with no stream for the secondary (no damage log
under `metric=frags`) leaves every row tied at 0 and the positional keys
decide, exactly as before v67.

**Gap mode (`mode=gap`, schema v68).** The contract is again one
sentence: *"a window is a maximal run of scoring events in which
consecutive events are no more than `gapMs` apart; its score is their
sum."* A gap of **exactly** `gapMs` joins and `gapMs+1` splits. `start`
is the run's first event and `end` its last, so a run of one event is a
legitimate window with `durationMs == 0` — a lone kill *is* a moment, and
it is the ranking's job, not the segmentation's, to decide whether it is
an interesting one.

There is **no candidate enumeration, no greedy pass and no overlap
suppression** here, and that is the point of the mode rather than an
omission: the clusters partition the player's event stream, so they are
disjoint by construction. The `t` / `t+1` / `t−windowMs` anchor families
fixed mode evaluates are an artifact of a fixed width — a gap window has
no start position to optimise, because moving its start can only drop
one of its own events. This is **not** the adaptive segmentation dropped
during planning: Ruzzo–Tompa needs a per-second penalty to stop the whole
match being one segment, and that penalty is exactly the unexplainable
tuning constant this rule does without.

- **Signed metrics cluster on ALL their events, negatives included.** On
  `netFrags` a death both extends the run and lowers its score. That is
  the honest reading of "net" — the fight as it went — and it is forced
  by the contract: clustering on the positives and then summing whatever
  negatives fall in the span would use two different event sets for the
  boundary and the score, and would break score == stat.
- **`score` still equals the same-named stat exactly** absent a
  `weapons=` filter, with no machinery beyond the split loop: every event
  of the metric inside `[start, end]` is a member of the run (members are
  consecutive in the sorted per-player stream), which is exactly what the
  stats builder integrates over the closed span.
- **A cluster may span the player's own death.** Kill, die, respawn, kill
  again within `gapMs` is one window, because the one-sentence contract
  has no life machinery in it and adding one would make the rule
  unexplainable. [`Lives`](#lives-viewlives-rest-lives) serves the
  per-life question.
- **`from`/`to` bound where a window may START** — here, its first
  event — never what it covers, exactly as in fixed mode: a cluster
  anchored before `to` still runs to its own last event past it. The
  `from` side is lossier than fixed mode's: a cluster whose first event
  precedes `from` is not returned at all, its post-`from` mass included —
  `from` never re-anchors a cluster, so slicing a match into `from`/`to`
  panes drops straddling clusters rather than truncating them.
- `GapMs` is **required** under this mode and has **no default**. The
  metrics' event cadences are too far apart for one value to serve them:
  measured over the 44-demo cache, per-player inter-KILL gaps run
  p50 ≈ 11–12 s while inter-damage-event gaps run p50 ≈ 1.0–1.1 s.
  Documented starting points: **~10000 for the frag metrics, ~3000 for
  the damage and shot metrics.** `GapMs <= 0` under gap mode is an
  `ErrInvalidFilter` whose message names those numbers.
- The two knobs are **mutually exclusive and refused, not ignored**: any
  **nonzero** `WindowMs` under `mode=gap` and any **nonzero** `GapMs` under
  `mode=fixed` — negative values included — are each an `ErrInvalidFilter`.
  Silently dropping one would answer a question the caller did not ask, and
  the envelope — which echoes only its own mode's knob — could not show that
  anything had been discarded. (An int option cannot tell an omitted zero
  from an explicit one; zero is therefore the one value that reads as
  "unset".)

| Field | JSON key | Type | Intent |
|---|---|---|---|
| TimeUnit | `timeUnit` | string (omitempty) | `"ms"`, set by the REST layer. |
| ScoredBy | `scoredBy` | ScoringRule | The scoring rule, **on the envelope, not per row**. |
| Dmg | `dmg` | string (omitempty) | The damage family the STATS BLOCK was computed in — see [The damage-family echo](#the-damage-family-echo-dmg--boundedmode). |
| BoundedMode | `boundedMode` | string (omitempty) | The demo's bounded-reconstruction state, same field as `/damage`'s. |
| Mode | `mode` | string — **not** omitempty | The segmentation, `"fixed"` or `"gap"`, echoed on EVERY response so a consumer never has to infer it from which knob field is present. |
| WindowMs | `windowMs` | int32 (omitempty) | Effective window length, echoed after defaulting. **Fixed responses only** — on a gap response it would be a lie, since those windows are as long as their events. |
| GapMs | `gapMs` | int32 (omitempty) | The inter-event gap the clusters were cut at, echoed. **Gap responses only.** |
| Limit | `limit` | int | Effective total cap, echoed after defaulting. |
| PerPlayer | `perPlayer` | int | Effective per-player cap (`0` = uncapped). |
| Measured | `measured` | MeasuredSources | **Not** omitempty — see below. |
| Windows | `windows` | []TopWindow | Ranked best-first. Never `null`. |

`ScoringRule`: `metric` (canonicalised, required), `weapons` (the
normalised sorted tokens the scoring events were restricted to,
omitempty) and `dmg` (the damage family, omitempty, damage metrics
only). There is **no top-level `metric` field** — naming one concept
twice invites the two copies to disagree, so `scoredBy.metric` is the
only echo, and a per-row copy would repeat one invariant object up to
`limit` times.

`TopWindow` is `rank` (1-based over the returned list), `player`,
`team` (omitempty), `start`, `end` (under `mode=fixed`,
`= start + windowMs` — the identity
holds over REST, where `windowMs` is capped at the match duration; the
view clamps `end` at the int32 ceiling for in-process callers, who face
no such cap. Under `mode=gap` it is the run's LAST scoring event, so the
row is as long as the stretch was and `end == start` on a single-event
run), `score`, plus
an embedded `IntervalStats`. For a top window `durationMs == end −
start` exactly; the attribution window *is* the interval, closed at both
ends.

**Metrics** are the closed vocabulary `view.KnownTopWindowMetrics`
(the OpenAPI enum is drift-pinned to it):

| Metric | Source | Note |
|---|---|---|
| `frags` | `frags.frags[]` | enemy kills only |
| `deaths` | `frags.frags[]` | victim side — finds a player's WORST stretch |
| `netFrags` | `frags.frags[]` | kills − deaths |
| `damageGiven` | `damage.events[]` | enemy only; honours `dmg` |
| `damageTaken` | `damage.events[]` | all sources, matching `PlayerDamage.taken` |
| `netDamage` | `damage.events[]` | given − taken |
| `shots` | `shots.shots[]` | fires |
| `hits` | `shots.shots[]` | connects |

A metric must be a **summable per-event quantity**. Ratios (accuracy,
efficiency) are deliberately absent — they do not sum, so "the best
window" is undefined for them; they ride the stats block instead.

**Scoring rules that a consumer has to know:**

- **Suicides and teamkills never score** under `frags` / `netFrags` (the
  `PlayerFrags.kills` rule), but they do appear in the stats block.
- **Telefrags and stomps score under every metric they contribute
  to** — `frags` and the damage metrics alike. Their value folds into
  the stats block's `damageGiven` / `damageTaken` exactly as `/damage`
  reports it, and it is scored on the same terms, which is what makes
  the invariant below hold. They stay out of `damageByWeapon` only,
  matching `/damage`'s own `byWeapon`: a positional kill carries no
  wire weapon to file it under.
- **Unfiltered, `score` equals the window's own stat for that metric
  exactly** — `kills` for `frags`, `deaths` for `deaths`,
  `damageGiven` / `damageTaken` / `shots` / `hits` for theirs, and the
  difference of the two for the net metrics. That is the whole contract
  between the two numbers.
- **`weapons` scopes the scoring events only** — and is the only thing
  that makes `score` and the same-named stat diverge. It selects
  positional kills too, through the `tele` / `stomp` pseudo-tokens
  `/damage` already uses, so `weapons=tele` scores telefrags alone. The
  stats block still describes everything that happened in the window, so
  `metric=damageGiven&weapons=lg` can report `score` 445 beside a
  `damageGiven` of 650. That is what `scoredBy` exists to disambiguate.
  Filtering the stats block too was rejected: `damageTaken`, `deaths`
  and `victims` cannot be weapon-filtered coherently from the subject's
  side.
- Each metric validates `weapons` against its **own source's**
  vocabulary — the frag log knows `hook` / `world` / `water`, the damage
  log `explobox` / `trigger` / `drown`, the shot stream only what can be
  fired (`rl lg gl ssg sng ng sg`). So `weapons=lava` is meaningful on
  `metric=deaths` and a 400 on `metric=shots`. Since v65 **`water` and
  `drown` are aliases of each other** in both vocabularies: one event,
  two spellings, and a caller should not have to know which log backs
  which metric. Purely additive — no emitted token changed.
- Windows scoring below `min` (default 1) are dropped; `min: 0` is a
  meaningful request and is honoured.
- Ties break by score desc → earlier start → shorter span → player name.
  All integer and total, so the output is deterministic.
- **Pauses are not corrected for, deliberately.** Every time here is
  match-relative, i.e. on the game clock, which freezes during a pause
  (`result/streams.go` `GlobalStream`). A 30 s window is 30 s of play;
  subtracting pause time would double-correct.

REST-only behaviour: `limit=0` is **rejected** (`400 invalid_param`), not
read as "uncapped" — an omitted MCP integer arrives as 0, which would
make a forgotten argument look deliberate. Negative is uncapped, the
default is 10 and anything above 200 is likewise a 400 rather than a
silent clamp. `windowMs=0` is a 400 too. The **view** clamps to 200 as
defence in depth for in-process callers (WASM, `qw-analyze`) that have
no HTTP layer in front of them.

`gapMs` is bounded at the HTTP layer exactly as `windowMs` is, on the
same match duration: an explicit value below 1 is a 400 (`gapMs must be
>= 1` — deliberately *without* "omit it for the default", since gap mode
has none) and anything above the match duration is a 400 naming the
bound. The unknown-`mode` and both cross-knob 400s come from the view's
own `ErrInvalidFilter`, forwarded verbatim, so one request cannot get two
wordings depending on which layer saw it first.

##### Lives (`view.Lives`, REST `/lives`)

```go
view.Lives(r, view.LivesOptions{
    Players: []string{"ParadokS"},
    From: 0, To: 0,   // int32 ms; keeps lives OVERLAPPING the window
    Dmg:   "bounded", // view default raw; REST default bounded
    MinMs: 0,         // drop lives shorter than this
})
// → *LivesView { timeUnit, dmg, boundedMode, measured, lives: []Life }
```

The envelope carries the same `dmg` / `boundedMode` echo `TopWindows`
does — see [The damage-family echo](#the-damage-family-echo-dmg--boundedmode).

Segmentation is `streams.players[].alive` (schema v64) — the one
canonical liveness — so `Lives` writes no liveness rule of its own.
Rows are time-ordered per player, players in name order.

| Field | JSON key | Type | Intent |
|---|---|---|---|
| Player | `player` | string | Whose life this is. |
| Team | `team` | string (omitempty) | Team at match end. |
| Index | `index` | int | 0-based, per player, in time order. |
| Start | `start` | int32 | Spawn, match-relative ms. |
| End | `end` | int32 | Death, match end, or end of observed presence. |
| AttrStart | `attrStart` | int32 — **not** omitempty | Start of the ATTRIBUTION WINDOW. `0` on every player's first life, so omit-on-zero would delete the commonest value. |
| AttrEnd | `attrEnd` | int32 — **not** omitempty | End of it. Wider than `durationMs`; see below. |
| EndReason | `endReason` | string | `death` \| `matchEnd` \| `leftGame`. Always present. |
| SpawnLoc | `spawnLoc` | string (omitempty) | Loc the life began in. |
| DeathLoc | `deathLoc` | string (omitempty) | Loc the life ended in. |
| KilledBy | `killedBy` | string (omitempty) | Who ended it. |
| DeathWeapon | `deathWeapon` | string (omitempty) | Weapon / cause that ended it. |
| ItemsTaken | `itemsTaken` | []LifeItem — **not** omitempty | Pickups in the attribution window, time-ordered. |
| WeaponsHeld | `weaponsHeld` | []string (omitempty) | Which of `rl`/`lg`/`gl`/`ssg`/`sng` were held while ALIVE, in that order. |
| *(embedded)* | | IntervalStats | The shared stats block. |

`LifeItem` is `{ item, kind, time }` — `item` is the disambiguated
instance name `/items` uses (`ra_1`), `kind` the class (`ra`, `mh`,
`quad`, `rl`, …), `time` int32 ms.

**Lives PARTITION `[matchStart, matchEnd]`.** Life *n* is attributed
every event from its own start (match start for the first) up to the
start of life *n+1* (match end for the last), incoming and outgoing
alike, so

```
Σ over a player's lives of kills, deaths, suicides, teamKills,
damageGiven, damageTaken, damageGivenTeam, damageGivenSelf, shots,
hits, and itemsTaken == what the per-event LOGS hold for that player.
```

"The logs", not "the scoreboards": the frag-side identity is against
`frags.frags[]` rows, which is where those counts come from.
`frags.byPlayer` has other sources — a death detected from
`DF_DEAD` / `STAT_HEALTH` with no obituary counts there and leaves no
log row for any life to carry — so per-life deaths can legitimately come
in under it. Measured on `wipeout_3on2_red_vs_blue[q3dm6qw]`: three
players' per-life deaths sum to 3/4/7 against a `byPlayer` 4/5/9, and
the frag log itself holds 3/4/7.

The damage-side target is **`/damage`'s non-summary aggregate**, not
`damage.events[]`. The rows and the aggregate agree on ordinary demos,
but a telefrag or stomp folds its value into the totals *without* a
per-hit row (it carries no wire weapon; `view/interval_stats.go`),
so on a demo with any positional kill the `events[]` sum comes in low by
exactly that fold. The non-summary aggregate is exact in both families —
over the 42 cached demos every per-life sum matches it to the point.
Do **not** reconcile against an unfiltered *bounded summary*: that
response substitutes KTX's end-of-match scoreboard (`boundedSource:
"ktx"`) for the per-hit reconstruction the lives are built from, which
lands a few points off per player.

One demo class breaks the identity outright, and honestly so: where two
wire slots resolve to the **same player name**, the stream key carries a
`#slot` suffix (`analyzer.disambiguatePlayerName`) while the event logs
keep the bare name. Nothing in a colliding player's lives rows can match,
so **every** stat in them — kills, deaths, damage, shots — reads zero,
and their pickups attach to no life at all (`view/lives.go` `itemTakes`
declines to guess which slot took an item rather than credit the wrong
one). `/top-windows` is unaffected: its subjects come from the log names
directly, never from the stream keys.

Three measured facts make the partition necessary rather than tidy:

1. **Posthumous outgoing events.** A rocket in flight when its shooter
   dies still kills — five landed 76–197 ms after the killer's own death
   on one corpus demo. You cannot fire while dead, so they were earned
   by the life that just ended.
2. **Incoming events inside a dead gap.** KTX records deaths on an
   already-dead player (the `dtTELE2` pent deflection). Dropping them
   made per-life deaths sum to 49 against a match total of 52.
3. **The match edges.** `alive[0].start` is clipped to first
   *observation*, not to `matchStart`, and the last life can end before
   `matchEnd`; without the edge extension a kill at `t=0` belonged to no
   life.

The boundary rule: the instant separating two lives goes to the **later**
one when there is a real dead gap, and to the **earlier** one when the
gap is zero (a same-millisecond death and respawn — the player was
living the earlier life when it happened). The final edge is closed.

**`deaths` is not a 0/1 flag.** It is 0 for a life that ended any other
way (see `endReason`) and can **exceed 1**, because a life carries both
the death that ended it and any death recorded in the dead gap that
followed. Measured across the 11 558 cached-corpus lives: 12 rows report
2 and one reports 3.

**`endReason` exists because an absent `killedBy` conflated three
different situations:**

| Value | Meaning |
|---|---|
| `death` | A recorded death. `killedBy` / `deathWeapon` are its obituary — and are still absent when the death was seen only by the `DF_DEAD` / `STAT_HEALTH` detectors and no obituary named it. That state is reachable (the two detectors exist because each sees deaths the other misses) but is **not currently observed**: across the 42 cached corpus demos, 0 of the 11 364 death-ended lives — of 11 558 lives in total — lack an obituary. |
| `matchEnd` | The player was alive when the match ended. |
| `leftGame` | The life was cut short by the player's observed presence ending (they quit, or the recording lost them), not by a death — `alive` is clipped to observed presence. |

On a POV or otherwise truncated recording, read `leftGame` as "the
recording stopped seeing them", not as "they stopped playing": the
liveness substrate takes only the player's *own* position samples,
spawns and deaths as presence evidence, so a player still fragging after
their track ends leaves those kills inside the final life's attribution
window while that life reads `leftGame`. A killer-side obituary is not
presence evidence, deliberately — it is broadcast to every recorder and
so would extend presence for players the recording never saw.

**`durationMs` is alive time only** (`end − start`), while every event
field covers the wider attribution window. That is the one asymmetry
left, and it is deliberate — a life's duration is how long the player
lived, not how long their rockets were in the air — but a per-second
rate computed from these counts over this duration runs **high**: only
slightly when summed across a whole match, and by tens of percent on a
single row where a short life is followed by a long dead gap.

**Which is why every row publishes the window itself, as `attrStart` /
`attrEnd`.** They bracket the span the counts were taken over, so
`attrEnd − attrStart` is the exact denominator for a rate and
`durationMs` is the exact numerator's-worth of alive time; without them a
consumer could see the asymmetry documented but not measure it. The
relationship is always `attrStart ≤ start ≤ end ≤ attrEnd`. One player's
windows **tile** the match end to end — half-open at exactly one side
apiece, per the boundary rule above — so no instant belongs to two of
them and none to none, which is the partition property stated as
arithmetic. On a demo with no match window the last life's `attrEnd` is
the int32 ceiling: there is no match end to bound it, and everything
after the last life belongs to it. On a `TopWindow` there is no
equivalent pair because the attribution window *is* the interval
(`durationMs == end − start`, closed at both ends).

**`weaponsHeld` is clipped to the ALIVE interval**, not to the
attribution window, because KTX does not clear the weapon bits on death
(`ktx/src/player.c`): a corpse still reads as armed, and the dead gap
would otherwise hand every life the weapons its owner died holding.
`omitempty` is safe there — possession comes from `STAT_ITEMS`, always
decoded wherever these streams exist, so absent means "held none of
them". It is **not** safe on `itemsTaken`, which distinguishes `null`
("the demo carries no item timeline", the same signal as
`measured.items: false`) from `[]` ("it does, and this life took
nothing").

**A filtered response does not reconcile**, in two distinct ways, and
both are the honest behaviour for "show me these lives":

- `minMs` drops a short life *together with its attribution window*, so
  events inside that window are absent from the response entirely.
- `from`/`to` select lives that **overlap** the window, and each selected
  life still carries its whole attribution window — which extends past
  `to`, and for the first life back to `matchStart`.

Relationship to `timelineAnalysis.fragStreaks`: that is the top-10
projection of this, ranked by frags and carrying only a count and an
`ewep`. It stays (removing it would be a genuine break), but a consumer
wanting every life, or any stat beyond frags, should read `/lives`.

##### The shared interval stats block

`IntervalStats` is embedded in both `TopWindow` and `Life`, filled by
one builder (`view/interval_stats.go`), so a consumer learns it once and
a further interval segmentation costs no new shape. (The third
segmentation, [`TopKills`](#kill-bursts-topkills--schema-v67), turned
out not to be an interval reduction and fills no `IntervalStats` — it
borrows only the `measured` block.) It deliberately does **not**
call `view.Frags` / `view.Damage` with `From`/`To`: those read `0` as
"no bound", so an interval legitimately beginning at `t=0` — every
player's first life on every demo — would silently receive whole-match
aggregates.

| Field | JSON key | Type | Intent |
|---|---|---|---|
| DurationMs | `durationMs` | int32 | `end − start`. **Alive time** on a life; identical to the span on a top window. |
| Kills | `kills` | int | Enemy kills; suicides and teamkills excluded. |
| Deaths | `deaths` | int | Deaths in the attribution window, all causes. |
| TeamKills | `teamKills` | int | Teammates killed. |
| Suicides | `suicides` | int | Self-inflicted deaths, a subset of `deaths`. |
| ByWeapon | `byWeapon` | map[string]int (omitempty) | Kills by weapon. |
| DamageGiven | `damageGiven` | int | To enemies. **Includes** telefrag / stomp value. |
| DamageTaken | `damageTaken` | int | From ALL sources — enemy, team, self, world — including positional-kill value. |
| DamageGivenTeam | `damageGivenTeam` | int | Dealt to teammates. |
| DamageGivenSelf | `damageGivenSelf` | int | Self damage, e.g. own rocket splash. |
| DamageByWeapon | `damageByWeapon` | map[string]int (omitempty) | Enemy damage given, by weapon. **Excludes** telefrags / stomps. |
| Shots | `shots` | int | Fires. |
| Hits | `hits` | int | Connects. |
| MainWeapon | `mainWeapon` | string (omitempty) | Weapon with the most kills, ties broken by name (the frag-streak `ewep` rule). Empty when there were no kills. |
| Victims | `victims` | map[string]int (omitempty) | Kills per victim; sums to `kills`. |
| Locs | `locs` | []IntervalLoc (omitempty) | Top 3 locs by **dwell**, clipped to the interval. |
| EventLocs | `eventLocs` | []IntervalLoc (omitempty) | Top 3 locs by **kills landed**. |

`IntervalLoc` is `{ loc, ms }` (dwell) or `{ loc, count }` (events) —
exactly one of the two is set, both `omitempty`.

`locs` and `eventLocs` answer different questions and routinely
disagree; that disagreement is the interesting part. `locs` is clipped
to the **interval**, not to the attribution window, because it is the
only quantity here measured in time rather than in events — the same
reason `durationMs` stays alive time — and it subtracts stretches where
the position track carried no evidence (before the first sample, gaps
longer than `result.SampleStaleCapMs`, past `result.TrackHoldEnd`), the
same bound every other occupancy walker applies. `eventLocs` counts sum
to `kills` up to two documented losses: the list is capped at three, and
a kill whose loc did not resolve is not credited to a fabricated one.

**Positional kills fold into the totals but not into the breakdown.**
Telefrag and stomp value is added to `damageGiven` / `damageTaken` /
`damageGivenTeam` / `damageGivenSelf` exactly as `/damage` does, so a
caller can reconcile per-interval damage against that endpoint — but not
to `damageByWeapon`, because a positional kill carries no weapon and
`/damage`'s own `byWeapon` excludes it for the same reason. So
`Σ damageByWeapon ≤ damageGiven`, not `==`.

###### `MeasuredSources` — the measuredness contract

```json
"measured": { "frags": true, "damage": true, "shots": true,
              "locs": true, "items": true, "liveness": true }
```

`MeasuredSources` rides the **envelope** of all three segmentation
responses (`/top-windows`, `/lives`, `/top-kills`) and is never
`omitempty`. It says which underlying streams *this demo* carries.

> **Measuredness is read from here and never from a field's absence.**

Every numeric field of `IntervalStats` is emitted **including a measured
zero**, so `damageGiven: 0` on its own cannot tell a player who dealt
none from a demo with no damage stream at all — this block can. This is
the same rule the damage schema states — *never infer measuredness from
`omitempty`*, see
[The three per-weapon damage maps](#the-three-per-weapon-damage-maps-and-what-an-absence-means)
— made structural: the earlier shape here had the ambiguity in both
directions at once.

| Flag | Gates |
|---|---|
| `frags` | `kills`, `deaths`, `teamKills`, `suicides`, `byWeapon`, `mainWeapon`, `victims`, `eventLocs` — and on `/lives`, `killedBy` / `deathWeapon`. |
| `damage` | `damageGiven`, `damageTaken`, `damageGivenTeam`, `damageGivenSelf`, `damageByWeapon`. |
| `shots` | `shots`, `hits`. |
| `locs` | `locs`, `eventLocs`, and `/lives`' `spawnLoc` / `deathLoc`. True only when the demo carries **both** a loc table and at least one player loc stream — either alone yields nothing. |
| `items` | `/lives`' `itemsTaken` (the same signal as `itemsTaken: null`). Meaningless to `/top-windows` and `/top-kills`, which emit no item field; the block keeps **one shape across the responses** rather than making a consumer learn several. |
| `liveness` | The spawn-to-death segmentation itself — `/lives`' whole existence. |

Two flags are not simply "the section is non-nil", and both would be
wrong if read that way:

- **`frags` is `FragResult.KillsMeasured`**, the stored demo-global
  kill-**attribution** verdict, *not* "a frag log exists". A demo whose
  log is empty while the scoreboard shows deaths reports `false` here,
  which is exactly the case where `kills: 0, deaths: 92` looks measured.
  The view reads the stored field rather than re-deriving the rule
  (`view/interval_stats.go` `fragsMeasured`), so this flag and `/frags`'
  own `killsMeasured` cannot drift — see
  [`killsMeasured`](#killsmeasured--the-one-measuredness-bit-of-this-section).
- **`liveness` is "at least one player has a non-nil `alive`".**
  `PlayerStream.Alive` has three states — `null` "not measurable", `[]`
  "measured, and never alive", `[…]` "the lives" — and `Lives` emits no
  rows for either of the first two. Without this flag the response
  `{"lives": []}` claimed "nobody ever lived" in both cases. It is the
  gate `LivesAvailable` applies, so `/lives` **422s
  `lives_unavailable`** rather than serving that ambiguity; a caller
  therefore only ever observes `false` on `/top-windows`, which does not
  need liveness. The flag is present on both responses because the block
  keeps one shape across them.

`false` means the source is absent for the whole demo. `true` does
**not** promise a non-zero row: within a measured source a zero is a
measurement, and an absent **map key** (`byWeapon`, `victims`,
`damageByWeapon`) means "none of that kind" — the same key-level rule
the damage schema documents.

Availability is per-field, not all-or-nothing: segmentation needs only
its own source, so a demo with no position/loc track omits
`locs`/`eventLocs` and serves everything else. `/top-windows` 422s
(`top_windows_unavailable`) only when the *metric's own* source is
missing; `/lives` 422s (`lives_unavailable`) only when there are no
per-player streams to segment at all **or none on which liveness was
measurable**, and a demo with no damage stream still yields lives.

###### The damage-family echo (`dmg` + `boundedMode`)

Both envelopes carry `dmg` and `boundedMode`, `omitempty`, with exactly
the meaning `/damage` gives them: `dmg` is the family (`raw` |
`bounded`) every `damageGiven` / `damageTaken` / `damageGivenTeam` /
`damageGivenSelf` / `damageByWeapon` in the response was computed in,
and `boundedMode` is whether this demo's bounded family was
reconstructed (`standard`) or skipped (`skipped:<mode>` — the full
reason vocabulary is in the damage section above).

They belong on the **envelope of every segmentation response**, not
only on `/top-windows`' damage metrics, because the stats block reports
damage under **every** metric — a window selected by `metric=frags`
still carries a `damageGiven`, and a number with no stated family is
not readable (on `/top-kills` the same echo names the family of
`damage` and `returnDamage`). When
the metric *is* a damage one, `scoredBy.dmg` carries the same value by
construction: one resolution feeds both, so they cannot disagree.

Read `boundedMode`, not `dmg`, to tell whether a bounded family exists —
it is the field that NAMES the state. Both are absent only on a demo
with no damage stream at all, where `measured.damage` is `false` anyway.

**The defaults differ by caller, deliberately.** The view default is
`raw` (an in-process caller that leaves `Dmg` empty gets raw); the REST
default is `bounded`, substituted by mvd-api's `handleTopWindows` /
`handleLives` / `handleTopKills` exactly as `handleDamage` does. A
**defaulted** bounded on
a `skipped:*` demo falls back to `raw` and says so in the echo; an
**explicit** `dmg=bounded` there is a `422 bounded_unavailable` — under
every metric, not only the damage ones. So the same query issued
in-process and over HTTP yields different numbers unless `Dmg` is set
explicitly, and the echo is how a consumer tells which it got.

#### Kill bursts (`TopKills`) — schema v67

```go
view.TopKills(r, view.TopKillsOptions{
    GapMs:       3000,   // <=0 → 3000, clamped to 5000
    ContestedMs: 4000,   // <=0 → 4000, clamped to 30000
    Limit:       20,     // 0 → 20, <0 → uncapped, clamped to 200
    Players:     []string{"ParadokS"}, // KILLERS
    Weapons:     []string{"rl"},       // the KILLING weapon
    MinDamage:   150,
    From: 0, To: 0,      // int32 ms; bounds the KILL, 0 = no bound
    Dmg:         "bounded", // view default raw; REST default bounded
})
// → *TopKillsView
```

The third segmentation over the same primitive — top windows cut by
fixed-length **time**, lives by **spawn → death**, and this by the **kill**
itself — and the only one that is not an interval reduction. For each enemy
kill the burst is the contiguous run of **killing-weapon** hits the killer
landed on that victim leading up to it, clipped below by the victim's current
life start (`streams.players[].alive`). Lives in `view/topkills.go`; it is not
a DAG artifact and changes no stored field.

**The burst is the killing weapon's run, and that is the question the view
answers** — *"how hard was this burst with this weapon"*. Measured over 1,866
enemy kills (8 golden-cache demos), on ~8% of them that **understates** what
produced the kill: a rocket softens, a shotgun finishes, and the row reads
`weapon: sg, damage: 16` for a kill that took 250 across weapons. This is
documented semantics, not a defect — the cross-weapon question stays a
`/damage` question, and nothing in the shape blocks adding a mixed-weapon mode
later. A different-weapon hit landing inside the run neither joins it nor
breaks it.

| Field | JSON key | Type | Intent |
|---|---|---|---|
| TimeUnit | `timeUnit` | string (omitempty) | `"ms"`, set by the REST layer. |
| Dmg | `dmg` | string (omitempty) | The family both `damage` and `returnDamage` were summed in — see [The damage-family echo](#the-damage-family-echo-dmg--boundedmode). |
| BoundedMode | `boundedMode` | string (omitempty) | The demo's bounded-reconstruction state, same field as `/damage`'s. |
| GapMs | `gapMs` | int32 | The **resolved** capture gap — defaulted and clamped, because it is what the row numbers mean. |
| ContestedMs | `contestedMs` | int32 | The resolved `returnDamage` window, likewise. |
| Limit | `limit` | int | Effective cap, echoed after defaulting. |
| Measured | `measured` | MeasuredSources | **Not** omitempty — the same block the interval views carry. |
| Kills | `kills` | []TopKill | Ranked hardest-first. Never `null`. |

`TopKill`:

| Field | JSON key | Type | Intent |
|---|---|---|---|
| Rank | `rank` | int | 1-based over the returned list. |
| Killer | `killer` | string | Who made the kill (the frag log's name). |
| Victim | `victim` | string | Who died. |
| Team | `team` | string (omitempty) | The **killer's** team. |
| Time | `time` | int32 | The kill instant, match-relative ms — also the run's last hit. |
| Weapon | `weapon` | string | The killing weapon, which **is** the burst's weapon. |
| Damage | `damage` | int | The run's summed damage in the envelope's family — the value that ranked this row. |
| Hits | `hits` | int | How many damage events the run holds. |
| SpanMs | `spanMs` | int32 | `time` − the run's first hit. The **display** figure ("291 dmg in 1.7 s"). |
| MaxGapMs | `maxGapMs` | int32 | Largest gap between consecutive hits in the run; `0` when `hits == 1`. The **exact narrowing filter** — see below. |
| VictimWep | `victimWep` | string (omitempty) | The victim's weapon class (`sg`/`mid`/`lg`/`rl`/`both`) at the killing hit, straight from that damage event — the same field `/damage` reports. Empty when the wire carried none. |
| ReturnDamage | `returnDamage` | int | What the victim dealt back to this killer over `contestedMs` before the kill: any weapon, enemy hits only, same family. |

**`gapMs` is a CAPTURE gap; `maxGapMs` is how you narrow.** The asymmetry
drives the default: truncation is unrecoverable downstream, over-merge is
filterable. Same-weapon inter-hit cadence inside real bursts is p95 2315 ms
for rl and 1876 ms for sg, so a baked 1200 ms gap truncated 11% of rl and 23%
of sg bursts — worst measured, a 291-damage triple-rocket kill reported as
**2** (the tail splash of the killing rocket). Hence a generous capture of
3000 and a row that carries what makes narrowing exact:

> Keeping exactly the rows with `maxGapMs <= g` gives every kept row its
> gap-`g` value **verbatim**: dropping hits from a run only widens gaps, so a
> run whose internal gaps are all `<= g` walks identically at `g` and at
> 3000. A run with `maxGapMs > g` is **dropped, not truncated** — a real
> gap-`g` walk would have reported its shorter suffix instead, so the
> narrowed list is the gap-`g` ranking restricted to runs that survived
> intact. When the truncated remainders matter, ask the server:
> `/top-kills?gapMs=g` is the exact walk.

`spanMs` cannot express even the kept-row rule (21 of the 24 over-merged rl
bursts in the corpus pass a `span <= 2×gap` test); it is carried because it is
the display figure. The per-weapon sweet spots are LG ≈ 1200 ms and
RL ≈ 2300 ms — RL's sits on its p95 cadence (2315 ms), while LG's is the end
user's operational gap, comfortably above LG's own p95 of 714 ms.

**What is not ranked, and what deliberately is:**

- **Positional kills produce no row.** A telefrag, stomp or squish carries no
  damage event (`result/damage.go` keeps them out of the log entirely), so
  there is no run to sum and no honest figure to rank by — 13 of 1,879
  measured kills. They are absent from this ranking only; the frag log and
  `/damage`'s `telefrags` / `stomps` still carry them.
- **Kills by an already-dead killer stay in.** The walk consults the
  **victim's** liveness and never the killer's: a rocket in flight when its
  shooter died still kills, and "spawnluck & went-down-swinging" is exactly
  the highlight this view exists to surface (measured 38/1,866).
- **`returnDamage` is a value, not a flag.** The server owns the window, the
  client owns the threshold that calls a kill contested (measured 63/37
  contested/passive at 4000, so the window discriminates rather than tagging
  everything). The window is not clipped by either player's lives — it can
  include return fire from the victim's previous life.
- **`weapons=` accepts only the burst-capable subset** —
  `rl,lg,gl,ssg,sng,ng,sg,axe,unknown`. Positional (`tele`/`stomp`/`squish`),
  environmental and bookkeeping causes can never anchor a burst, so those
  frag-vocabulary tokens are a `400` here rather than a valid filter that
  structurally selects nothing.
- **The one join that can wobble: duplicate display names.** When two wire
  slots share a name, the player streams are keyed `name#slot` while the
  frag and damage logs keep the bare name. The view resolves the collision
  by the kill's own death instant — the victim died at the kill time, so
  the stream whose alive interval **ends exactly there** is the victim's —
  and clips normally when exactly one candidate matches. When none does (or
  two same-named players die on the same instant), the burst runs
  **unclipped** rather than being dropped: losing real rows over a
  display-name accident is worse than the residual previous-life leak. The
  same collision is documented on `/lives`' own rows.

**Ranking** is burst damage desc → earlier kill → killer name → victim name.
Total, so the response is byte-stable: one killer can land two kills on the
same instant (a quad rocket into two opponents).

**Availability** needs three sources — the frag log, the damage log, and
**measurable liveness**. The third is not pedantry: at the 3000 ms capture gap
the walk crosses the victim's previous death on 74/1,866 measured kills (4%),
and without the life clip those bursts absorb the victim's previous life
(worst measured: 355 reported where the current life took 62). Because the
list is ranked **by** damage, the contaminated rows are exactly the ones that
float to the top and a consumer holding only this response cannot tell them
apart — the "would actively mislead a consumer that cannot itself
disambiguate" case. So such a demo gets the 422 `/lives` gives it
(`top_kills_unavailable`) rather than a plausible-looking list.

REST-only behaviour: `gapMs`, `contestedMs` and `limit` are **rejected**
(`400 invalid_param`) outside their ranges rather than clamped — `[1, 5000]`,
`[1, 30000]` and `[1, 200]` respectively, with a negative `limit` meaning
uncapped and an explicit `0` on any of the three a 400 (an omitted MCP integer
arrives as 0, which must not look deliberate). The **view** clamps instead, as
defence in depth for in-process callers. `mvd-api`'s `/overview` carries the
same rows at the documented defaults as `topKills` — **20** of them, not its
neighbours' 5, because the per-weapon `maxGapMs` narrowing a consumer runs off
that one call leaves 16-20 of a top-20 and only 6-10 of a top-10; the field is
omitted (never served raw) on a demo with no bounded family, since `/overview`
has no `dmg` echo to name a family with. Absence there is not a verdict: a
demo that can answer but has zero enemy kills also omits the field
(`omitempty`), so read `/top-kills` itself when the distinction matters.

## MetadataResult (`metadata`)

Defined in `result/metadata.go`.

| Field | JSON key | Type | Notes |
|---|---|---|---|
| ServerInfo | `serverInfo` | map[string]string | Last-write-wins union of fullserverinfo stufftext + per-key svc_serverinfo updates. |
| MatchSettings | `matchSettings` | *MatchSettings | Parsed KTX countdown centerprint, plus the settings rows KTX broadcasts beside it (v72: `fairpacks`). |
| CountdownText | `countdownText` | string | Raw multi-line centerprint (color-stripped). |
| FinalScores | `finalScores` | *FinalScores | v72 — KTX's `//finalscores` end-of-match stuffcmd, verbatim; see below. |

### FinalScores

v72. The scoreline KTX stuffs at match end (`ktx/src/commands.c:6963-6977`,
wire form `//finalscores "<date>" "<mode>" "<map>" "<team1>" <s1> "<team2>"
<s2>`). It reaches **64% of the archive against demoinfo's 46%**, so on the
pre-ktxstats half it is the only place the *server* states a final result.
Reported verbatim — nothing here is reconciled against the derived
scoreboard; `match.sources` names where the pipeline did consume it.

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Date | `date` | string, omitempty | Server-LOCAL match-end stamp, `%b %d, %H:%M` — **no year**, no seconds, no timezone (`Sep 29, 21:27`). The resolved instant is `streams.global.dateMarkers[]` with `source: "finalscores"`. |
| Mode | `mode` | string, omitempty | KTX's lastscores mode name (`duel`, `team`, `FFA`, `CTF`, `RA`, `Clan Arena`, `Wipeout`, `HoonyMode`, `race`, `unknown`; forks add their own — `Extinction` appears in the archive). |
| Map | `map` | string, omitempty | Canonical short map name. |
| Team1 / Team2 | `team1` / `team2` | string, omitempty | The two sides. On a duel the "team" is the player's own name — the same player-as-team layout `match.teams` uses. Normalised out of the Quake charset like every other name; an empty side is observed. |
| Score1 / Score2 | `score1` / `score2` | int | The server's own final figures. **Can be negative** (suicides), and **what they count depends on the mode**: summed frags normally, but ROUNDS WON on Clan Arena and Wipeout (`CA_get_score_1/2`, `commands.c:6867-6886`) — a Wipeout demo reports `5` against a 241-frag scoreboard and both are right. |

Even on a frag-scored mode this can differ from `match.teams` by one:
KTX counts the frag that *ends* the match, while the scoreboard here
freezes at the match-end latch and the `svc_updatefrags` for that last
kill lands on or after it. Measured on 120 demoinfo-less archive demos:
105 exact agreement, 10 differences (7 of them round-scored modes — 6
Wipeout, 1 the fork mode `Extinction` — and 3 the ±1 above), 5 not
comparable (duel team labels).

`MatchSettings` covers `mode`, `deathmatch`, `teamplay`, `timelimit`,
`fraglimit`, `spawnmodel`, `spawnK`, `antilag`, `overtime`, `powerups`,
`dmgfrags`, `noItems`, `midair`, `instagib`, `yawnmode`, `airstep`,
`vwep`, `noweapon`, `matchtag`, `fairpacks`, `socdv2`. See
`result/metadata.go` for the per-field intent.

`mode` is `PrintCountdown`'s Mode literal (`ktx/src/match.c:1511-1571`)
after the Quake-font colouring is removed and the spaces KTX letter-spaces
it with are stripped — so `D u e l` is published as `Duel`, `C O O P` as
`COOP`. The producer vocabulary is `Duel`, `Team`, `FFA`, `CTF`, `RACE`,
`COOP`, `CA`, `RA`, `Wipeout`, `Hoony`, `BlitzTDM`, `LGC`, `BLOODFST`,
`Unknown`; forks add their own (`Extinction`). It is one input to
[`gameMode`](#gamemode) — which knows that `LGC` / `BLOODFST` name a
ruleset and that at least one current server build prints `CA` for
wipeout too — and should be read through that block rather than compared
directly. On a hoony duel the last three frames are `PersonalisedCountdown`
(`match.c:1498-1503`: no table, at most a `Next` / `Duration` / `Draw`
row); the block comes from the last frame that carried the table.

`fairpacks` (v72) is the one row that does not come from the countdown
centerprint: KTX broadcasts `Fairpacks setting: <ruleset>` as a level-2
print beside it (`ShowMatchSettings`, `ktx/src/match.c:2086-2107`) and
**only when `k_frp` is not the default 0**, so its absence is
informative — it means a dropped backpack holds the weapon the victim was
wielding. Values: `"best weapon"` (k_frp 1) / `"last weapon fired"`
(k_frp 2). [Backpacks](#backpacks-backpacks) stands its reconstruction
down when this is set.

## LocGraphResult (`locGraph`)

Defined in `result/locgraph.go`.

`{ locs: []LocNode, edges: []LocEdge }`.

### LocNode

`{ name, x, y, z, total, byPlayer, byTeam, armed?, unarmed?, quad?, pent? }`
— **alive, observed** time spent (int32 ms, schema v57) at each named location
— since v64 the weights are an exact time-weighted integral over the native
position stream, excluding time the player was dead (a corpse, or on a gib the
thrown head, keeps broadcasting position) and stopping at the player's own
end-of-track (`result.TrackHoldEnd`: the last sample held for one measured
cadence, capped at 250 ms). Sample-and-hold is also **bounded**: a sample's
evidence expires after `result.SampleStaleCapMs` (250 ms), so time inside an
unobserved hole — a PVS gap on a POV recording, packet loss — is credited to
nobody rather than held at the pre-gap loc. The cap is inert at every real
recording cadence (worst gap in the golden corpus: 74 ms) and decisive on POV
demos, where holding across the holes credited ~92% of one player's loc time to
wherever they stood when they left the recorder's view. Weights are
aggregated all-players + per-player + per-team (`total` / `byPlayer` /
`byTeam`; `x`/`y`/`z` are float map coords). `armed`, `unarmed`, `quad`
and `pent` are optional
`LocWeights` (`{ total, byPlayer, byTeam }`, same int32-ms shape) carrying that
breakdown restricted to the time the player held RL or LG (`armed`), held
neither (`unarmed`, the complement of `armed`), or had an active quad / pent
powerup. Since v64 the condition is evaluated at **interval boundaries**, not
per sample, so a pickup landing between two samples splits the interval
exactly; omitted when the condition was never met.
They let consumers re-weight the graph by combat posture without
re-walking streams.

### LocEdge

`{ from, to, kind, total, byPlayer, byTeam, armed?, unarmed?, quad?, pent? }`
— directed transitions between locs. `kind` = `normal` / `teleport`.
Edges are alive-gated like node time, and (since the v64 fix pass) also
**stale-bounded**: no edge is recorded across an inter-sample gap longer than
`result.SampleStaleCapMs` (250 ms). Past that bound the two samples bracket an
unobserved stall in which the player may have crossed any number of locs, and
the displacement check cannot even label it — beyond
`locgraphTeleportMaxGapMs` a jump stays `normal`, so a consumer filtering
teleports out kept exactly the invented adjacencies. Edge counts therefore drop
on POV recordings; on server recordings the bound is inert.
`armed`, `unarmed`, `quad` and `pent` are optional `LocEdgeWeights`
(`{ total, byPlayer, byTeam }`, int counts) carrying the subset of
transitions made while the player held RL or LG (`armed`), held neither
(`unarmed`), or had an active quad / pent at the destination sample, so
the loc graph can be drawn as a self-contained movement graph per combat
posture. Omitted when no transition met the condition.

## ItemsResult (`items`)

Defined in `result/items.go`. The item **spawn list, kind, and
location** are derived from the MVD entity stream (model-classified
baselines) and are present on **any** demo — KTX or not. The pickup
`phases` (taken/respawn transitions) come from entity-visibility
changes; KTX `//ktx took|timer|drop` hints only refine **attribution**
(`takenBy`/`team`) and MH respawn timing. Non-KTX demos still get the
full item layout and pickup timeline, just without picker names.

`{ items: []ItemTimeline }`. Each `ItemTimeline` has
`{ name, kind, entNum, x, y, z, loc, phases: []ItemPhase }`.
`ItemPhase` is `{ availableFrom, takenAt, takenBy, team, respawnAt }`.

**Time sentinels.** `availableFrom == 0` marks the initial "available
since match start" phase (the rebase leaves zeros alone). Takes are
recorded only under the match gate and rebase to `>= 0` by
construction, so phase times are never negative; `takenAt`/`respawnAt`
`== 0` (omitted in JSON) mean "not taken" / "not yet respawned", with
the theoretical collision (a take at *exactly* t=0) physically
unreachable.

**Weapon-stay convention** (schema v46; serverinfo `deathmatch` 2/3/5
or `coop` — dmm3 duels/2on2 included): touched weapons never leave the
world in those modes, so weapon pickups are synthesized from
STAT_ITEMS bit flips and recorded as a **zero-length unavailability**:
`takenAt == respawnAt`, with the next phase's `availableFrom` at the
same instant. A consumer asking "is this item up at time T" always
gets "up" for such weapons; the closed phases still carry
`takenBy`/`team` for pickup counting.

**Summary shape** (`/items?summary=true`, `view.ItemsSummary`): per-item
take aggregates instead of the phase timeline —
`{ items: [{ name, kind, entNum, loc?, takenCount, byPlayer?: {name: n},
firstTake?: { time, takenBy?, team? } }] }` with `time` in match-relative
**int32 ms** (pure-ms view surface). With a `from`/`to` window, the full
timeline keeps phases **overlapping** the window while the summary
counts takes **inside** it; identity-filtered items survive with
`takenCount: 0` when nothing took them in the window.

For the map's **designed** static layout (all spawns + teleporters /
spawnpoints / buttons, independent of what happened this match), see
[MapEntitiesResult](#mapentitiesresult-mapentities).

## MapEntitiesResult (`mapEntities`)

Defined in `result/map_entities.go`. The map's **static, designed
layout** — item spawns, player spawnpoints, teleport
destinations/sources, and buttons — each with a type and a location.
Sourced from the offline-generated **mapents corpus** (BSP entity lumps,
`mvd-analytics/mapents/data/<map>.json`, produced by `cmd/mapgen`),
keyed by map name. It is therefore **identical for every demo on a
given map** and independent of what happened in the match. Absent when
no corpus exists for the map.

This is the map's *designed* layout. For the per-match pickup timeline —
which items actually spawned, who took each, when it respawned — see
[ItemsResult](#itemsresult-items). The two can be joined by `kind` +
nearest origin.

`{ map, entities: []MapEntity }`. Each `MapEntity`:

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Type | `type` | string | `item` / `spawn` / `teleportDst` / `teleportSrc` / `button` / `door`. |
| Class | `class` | string | Raw BSP classname (`weapon_rocketlauncher`, `info_player_deathmatch`, …). |
| Kind | `kind` | string (omitempty) | Items only; same vocabulary as `ItemTimeline.Kind` (`rl`,`lg`,`ra`,`mh`,`quad`,…). |
| Name | `name` | string | Loc-based label, disambiguated with `-1`/`-2` within each `(type, kind, loc)` group; falls back to kind/type when no loc file exists. |
| X / Y / Z | `x`/`y`/`z` | float32 | Position. Entity origin for point entities; bmodel bbox centre for brush entities (teleportSrc/button/door). |
| Loc | `loc` | string (omitempty) | Nearest named loc, when a loc file exists for the map. |
| Target | `target` | string (omitempty) | `teleportSrc` → the destination's targetname. |
| TargetName | `targetName` | string (omitempty) | `teleportDst` → its own targetname (join key for teleport pairs). |
| Spawnflags | `spawnflags` | int (omitempty) | Raw BSP spawnflags. |
| Bounds | `bounds` | object (omitempty) | Brush entities only: `{ min:[x,y,z], max:[x,y,z] }` — the trigger/door volume in world coords. |

Classification is grounded in `ktx/src/items.c` spawn functions
(`item_health` spawnflags `H_ROTTEN=1`/`H_MEGA=2` select h15/mh).

**Point vs brush.** Point entities (items, spawns, `teleportDst`) sit at
their entity origin. Brush entities (`teleportSrc`, `button`, `door`)
have no origin — they are placed at their BSP submodel's bbox centre
(`X/Y/Z`) and carry the box as `bounds` (the trigger/door volume).

**Teleport pairs.** A `teleportSrc` (the trigger you walk into) links to
its `teleportDst` (where you arrive) by `teleportSrc.target` ==
`teleportDst.targetName`. Multiple sources may share one destination.

## Backpacks (`backpacks`)

Defined in `result/backpacks.go`. Each `BackpackDrop` is
`{ time, player, team, weapon ("rl"|"lg"), origin, loc, entNum, source }`,
plus `{ fate, picker, pickerTeam, pickupTime }` on a reconstructed row
(v72). A `ktx` row carries `fate` too, but only ever the single value
`expired` and only when KTX said so outright — see below. `entNum` is the
join key with `WeaponPickup.BackpackEnt` on a `ktx` row.

`origin` is the **pack's** position, not the victim's: KTX copies the
victim's origin and then drops it 24 units
(`item->s.v.origin[2] -= 24`, `ktx/src/items.c:2703-2704`), which puts the
pack at their feet rather than inside their chest. Both provenances apply
it, so a map overlay draws every pack where it actually sat. The zero
vector still means "no position known for the dropper" — it is not an
origin 24 units under the world.

### `source` — provenance (v72)

Two provenances, never mixed within one demo:

| `source` | Meaning |
|---|---|
| `ktx` | Decoded from KTX's `//ktx drop <ent> <items> <player_ent>` STUFFCMD_DEMOONLY directive (`ktx/src/items.c:2762-2766`). Exact: KTX emits it once per real pack, with the weapon and the dropper's slot baked in. Only KTX ≥ 1.38 emits it — 49.2% of the archive. |
| `reconstructed` | Replayed from `DropBackpack`'s own rule on a demo whose mod never hinted. Its pickup side is `fate` / `picker` / `pickerTeam` / `pickupTime` on this same row, not a `weaponPickups` join; `entNum` is the backpack-model entity the linkage bound to the drop, and joins to nothing. `entNum` 0 means no entity was identified — never treat 0 as an edict. |

**What the reconstruction reproduces.** With the shipped default
`k_frp 0`, `DropBackpack` puts the victim's *currently wielded* weapon in
the pack verbatim (`item->s.v.items = self->s.v.weapon`,
`ktx/src/items.c:2706`) and hints only when that is exactly the RL or the
LG. mvdsv writes the same field into the MVD for every spawned player as
`STAT_ACTIVEWEAPON` (`mvdsv/src/sv_send.c:1268`) — published here as
`streams.players[].aw` — so the reconstruction is a replay, not an
inference: death instant + wielded weapon + the victim's last broadcast
position. It reproduces the one deathtype `DropBackpack` refuses, `/kill`
(`dtSUICIDE`, `ktx/src/client.c:1008`, obituary `" suicides"`); rocket
suicides, falls, drowning and lava all DO drop a pack.

### `fate` / `picker` / `pickerTeam` / `pickupTime` — the pack's outcome (v72)

`picker` / `pickerTeam` / `pickupTime` are set on `reconstructed` rows
only. A `ktx` row leaves them empty: its pickup side is the
`weaponPickups` join, which KTX states outright in `//ktx bp`, and a
second answer beside it could only disagree. An EMPTY `fate` on a
reconstructed row means the linkage did not run at all (no player streams);
that is distinct from `unobserved`, which is a measurement.

**`fate` on a `ktx` row is `expired` or nothing**, and it means KTX said so
in a third directive: `//ktx expire <ent>`, which `SUB_Remove` emits for
every RL/LG pack it deletes untaken (`ktx/src/g_spawn.c:196-210`). That is
the one thing the `weaponPickups` join cannot state — the *absence* of a
`//ktx bp` is not evidence, because a demo can carry the drop hint and no
pickup hints at all (107 of 330 archive demos do). So on a `ktx` row read
`fate: "expired"` as "the server announced this pack timed out", and an
empty `fate` as "ask `weaponPickups`" — never as `picked` or `unobserved`,
which are values only the reconstruction's linkage produces.

Read `source` for the provenance of a `fate`, exactly as for the row: an
`expired` on a `ktx` row is the wire's own statement, and on a
`reconstructed` row it is the linkage's reading of the pack-entity track.
No separate provenance field exists, because `source` already carries it.

The wire population the `ktx` side rests on, over 223 archive demos
carrying all three directives: 10 384 `//ktx drop` = 10 006 `//ktx bp` +
190 `//ktx expire` + 188 rows claimed by neither, with **zero** rows
carrying both. The 188 are packs the recording ended on top of, whose
removal never happened inside the demo; they keep an empty `fate`, which
is why "no pickup row" must not be read as "expired".

| `fate` | Meaning |
|---|---|
| `picked` | The pack entity left the wire with at least one live player's bounding box overlapping it — the same overlap the server tests before calling `BackpackTouch` (`ktx/src/items.c:2367`). `pickupTime` is the match-relative ms it left. `picker` is set only when the evidence named exactly ONE player: one player on the pack, or several separated by which of them gained the weapon bit. Two players on a pack with nothing to separate them stay `picked` with no `picker` rather than a guess. `pickerTeam` is that picker's team and is omitted when they have none — an FFA or duel picker is named with no `pickerTeam`, so read its absence as "teamless", never as "no picker". |
| `expired` | The pack was removed by KTX's 120 s timeout (`item->s.v.nextthink = time + 120`, `think = SUB_Remove`, `items.c:2871-2872`) untaken — the one outcome that is positively not a pickup. On a `ktx` row this is the wire saying it, in `//ktx expire <ent>`. On a `reconstructed` row it is the pack entity leaving the wire at that age with nobody on it. |
| `unobserved` | The honest residual: no backpack-model entity bound to the drop, or the entity was still on the wire when the recording stopped, or it left early with nobody on it. **None of these is evidence that nobody took the pack.** |

**How the pack is found and followed.** The reconstruction names a time and
a place (the victim's origin less 24). The pack that appears at that
instant nearest that point is the drop's pack — scored against the `//ktx
drop` hint's own edict number, 947 of 961 drops bound and every one of the
947 to the edict the hint named. It is then followed through its origin
updates to where it settled: KTX tosses a pack with `velocity[2] = 300`
plus a random horizontal kick and `MOVETYPE_TOSS` (`items.c:2856-2861`), so
it falls off ledges and down lift shafts — measured 58 units of travel at
p50, 583 at max — and the pickup test runs where it landed, never where it
was dropped.

**Why the touch, and not a stat flip.** `//ktx bp` fires on every RL/LG
pack pickup regardless of what the picker already held, and
`other->s.v.items |= new` cannot change a bit they already had. Measured:
only 237 of 606 unambiguous ground-truth pickups came with a weapon-bit
gain, so requiring one would have discarded 61% of real pickups. The
bounding-box overlap is not a proxy for the touch — it IS the touch — so it
is the primary signal, and the bit gain only separates two players standing
on one pack.

**Accuracy** (223 hint-carrying demos, 10 378 scored drops, every hint
withheld): picked-vs-not **100.00% precision, 96.13% recall**; of the
pickups it found, **99.77% carried a named picker and 99.98% of those were
correct**; pickup-time error 0 ms at p50 and p90. `expired` is **100.00%
precise at 100.00% recall** against the packs `//ktx expire` names — it
finds every announced expiry and asserts nothing else. The largest residual
(2% of all drops) is a pack taken inside the demo frame it dropped in: it
never reaches the wire at all, so no entity exists to bind. Full tables,
mismatch classes and the reproduce command:
[`analyzer/BACKPACKS.md`](analyzer/BACKPACKS.md).

**What is still absent on a reconstructed row.** `hadBefore` — KTX ORs the
weapon bit in, so a redundant grab leaves no trace anywhere on the wire —
and therefore kill credit and pack-transfer credit
(`playerStats[].pickups.byKind[].xfer`), which both need it.

**Stand-down conditions.** The section is left ABSENT — never
half-filled, never guessed — when any of the following holds. Note the
converse does NOT hold: an absent section means *either* a stand-down *or*
a measured zero (a demo where no RL/LG pack dropped serializes the same
way, `omitempty` over an empty list). The wire shape cannot tell the two
apart, so do not read absence as "the pipeline refused".

The conditions:

- there are no player streams, or no frag log (the only record of which
  deaths were `/kill`);
- no player carries the `aw` column (a recorder that never wrote
  `STAT_ACTIVEWEAPON`);
- the weapon state is frozen — `aw` never moves AND the `STAT_ITEMS`
  weapon bits never cycle, the old-recorder signature the damage
  reconstruction refuses for the same reason;
- the mod is KTX ≥ 1.38, which hints for itself: an empty section there
  is the wire's answer, not a gap;
- the ruleset changes what a pack contains — `k_bloodfest` (no drop at
  all), `k_yawnmode` (last-fired weapon, DMM1 shotgun override), or a
  non-default `k_frp`, detected from KTX's `Fairpacks setting:` broadcast
  (`ktx/src/match.c:2086-2107`, published as
  `metadata.matchSettings.fairpacks`). Modes that only rewrite T_Damage —
  midair, instagib, dmgfrags, CA, wipeout — deliberately do NOT stand the
  pass down: `DropBackpack` has no such early return and the wire hints
  confirm packs drop in them.

Two refusals are narrower than the whole section, and cost only the rows
they cover:

- a **death** whose newest `aw` sample was carried on a DIFFERENT wire
  slot is refused (players with more than one published session only).
  mvdsv delta-codes stats against a per-slot cache no client change
  resets (`sv_send.c:1279-1281`), so a player who reconnects onto a slot
  whose previous occupant held what they now hold gets no update of their
  own — and the merged stream would otherwise answer with a weapon they
  held on the slot they left. A reconnect onto the *same* slot keeps the
  cache, and the earlier sample with it;
- a **death** whose victim's position track has gone stale past 400 ms is
  withheld rather than placed on a guess.

There is deliberately **no** per-player "this `aw` column never moves"
refusal, and the demo-level frozen check keeps its `STAT_ITEMS` arm for
the same reason: a single-valued `aw` column is what a **single-weapon
ruleset** looks like (`1on1-lgc`, `2on2-midair`, rocket arena), not what a
frozen recorder looks like. Measured, refusing those columns cost 58 of
13 749 hint-confirmed drops and prevented no fabrication.

**Measured accuracy** against the `//ktx drop` hints on 316 archive demos
spanning KTX 1.38–1.48: **precision 99.97%, recall 99.97%** (13 749 hints,
LG 100%/100%, RL 99.96%/99.96%); drop-time error exactly 0 ms; position
error p50 9.7 / p90 22.3 / p99 33.9 units. Full method, per-era and
per-mode splits, residual classes and the reproduction command:
[`analyzer/BACKPACKS.md`](analyzer/BACKPACKS.md).

**Residual with no wire signal:** `dp 0` (drops switched off
server-side) is published nowhere — no serverinfo key, no countdown row.
On a hinting mod its absence settles the question; on a pre-1.38 demo it
is unfalsifiable, and a `dp 0` server would make this section report packs
that never dropped. No demo in the validation sample showed the
signature.

## WeaponPickups (`weaponPickups`)

Defined in `result/weapon_pickups.go`. Each entry is a slot-weapon
acquisition: `{ time, player, team, weapon,
source ("world"|"backpack"|"unknown"), hadBefore, inferred, kills,
nextDeathTime, backpackEnt, dropper, dropperTeam, dropTime }`. `kills`
is the kills-before-next-death effectiveness metric (only non-zero on
first acquisition in a life — redundant grabs stay listed as zero-kill
entries so denial labelling still works).

**Weapon-stay demos** (schema v46; serverinfo `deathmatch` 2/3/5 or
`coop`): KTX never emits `//ktx took` for weapons there, so entries
are synthesized from STAT_ITEMS weapon-bit 0→1 transitions and marked
`inferred: true`. `source` is `"world"` when the picker passed within
touch range of a matching weapon spawn during the stat-lag window,
else `"unknown"` — typically a non-RL/LG backpack grant, which has no
hint in any mode. Synthesized entries always have `hadBefore: false`
(the bit was observed flipping 0→1).

## OpeningResult (`opening`)

Defined in `result/opening.go` (schema v51). The match opening in one
small block — a pure projection of data `items` / `streams` already
carry, kept as its own artifact (`opening`, servable via
`GET /v1/demos/{id}/artifacts/opening`) so "how did the opening go" is
one cheap fetch.

```jsonc
{
  "players":    [ { "name", "team"?, "loc"? } ],   // match-start spawn loc
  "firstTakes": [ { "item", "kind", "entNum", "loc"?, "time", "takenBy", "team"? } ]
}
```

- `players` — every player present **and alive** at match start, sorted
  by team then name. `loc` is the resolved spawn location (empty when
  the map has no .loc corpus).
- `firstTakes` — the first **in-match** take of each tracked spawner
  (warmup takes are skipped), sorted by time. Tracked kinds: armors,
  mega, powerups, and the RL/LG weapon pads. `item`/`entNum`/`loc`
  identify the spawner (`ItemTimeline` naming: `ya_1` vs `ya_2`). A
  spawner nobody took has no entry. `time` is match-relative ms.
- Omitted entirely when no match start was detected (t=0 would be the
  demo open, not an opening).

## NoMatchResult (`noMatch`) — schema v74

The explicit marker on a result that carries **no analyzable match**.
Present exactly when `streams` is absent — **one** predicate, not two:
`streams` is written only when it holds at least one player stream
(`buildStreamsResult` returns nil otherwise), so "no `streams` block" and
"no player streams" name the same state, and a result carrying both
`streams` and `noMatch` is not constructible.

**Why it exists.** Streams are the spine of this pipeline — damage,
`playerStats`, `locGraph`, `opening`, buckets, region control all hang off
them — and they are built only inside the DETECTED match window: every
recording path in the timeline analyzer is gated on Layer 1 having raised
`MatchStartEvent`. A demo whose match start was never declared on the wire
therefore produces `buildStreamsResult() == nil` and, transitively,
nothing else. (Until v75 that gate was a match-start BROADCAST alone,
which a KTX matchless server never prints — see `matchStartSignal` on
[GlobalStream](#globalstream) and the `matchStartUnannounced` row below.) Over the full 50 951-demo archive sweep that is **1 032
demos (2.03%)**, and until v74 every one of them came out silent: empty
sections and an entirely EMPTY `errors[]`. The v52 `timeBase:"demo"`
fallback that reads like it should cover this does not — `flagDemoTimeBase`
returns early on `result.Streams == nil`, so it only ever fires for the
narrow case where a match start WAS detected but landed at demo `t=0`.

**Why it is not an `errors[]` entry.** `errors[]` means the pipeline
failed at something. "This recording holds no match" is a fact about the
demo. Keeping them apart is the point of the marker: a consumer can tell
*no match here* from *parse failed* without heuristics on an error string.
The one reason that IS a failure, `demoUnreadable`, says so by name and
leaves the reader's message in `errors[]`.

**Checking order.** Read `noMatch` FIRST, before `errors[]` and
`parseWarnings`: it decides whether those two describe a partial match or
nothing at all. On a result with no `noMatch`, they are damage reports
against a match that exists; on a result with one, they are notes about a
recording that holds no match either way. `demoUnreadable` is the single
reason that means both, and it says so by pointing at `errors[]`.

| Field | JSON key | Type | Notes |
|---|---|---|---|
| Reason | `reason` | string | Why there is no match; the vocabulary below. Always present. |
| Detail | `detail` | string | The same verdict as one human-readable sentence, naming the evidence. Always present. **Unstable, display only — do not parse it:** the wording changes without a schema bump, and every fact it states appears as a structured field beside it. |
| StatusAtOpen | `statusAtOpen` | string, omitempty | The serverinfo `status` value in the **opening `fullserverinfo` dump**, verbatim. Absent when that dump carried no `status` key — including when a later `svc_serverinfo` update sets one (3 of the 1 032; `statusRunningSeen` is where those later readings land). |
| StatusRunningSeen | `statusRunningSeen` | bool, omitempty | `status` named a running game at some point in the recording. |
| GameDir | `gameDir` | string, omitempty | The serverinfo `*gamedir` key — the mod the server ran. Evidence, never a reason of its own. |
| Kills | `kills` | int, omitempty | Length of the frag log — the kills whose obituaries **this pipeline recognises**, so a floor on how much play the window held rather than a census. The obituary table is id1/KTX vocabulary; a foreign mod's own death messages are invisible to it. Zero means "the frag log parsed nothing", not "nobody died". |
| DateMarkers | `dateMarkers` | []WallClockMarker, omitempty | Every date stamp the wire carried, verbatim — the same list `streams.global.dateMarkers` carries, given a home here because there is no `streams` block. See [the no-streams exception](#globalstream). |

### The `reason` vocabulary

The five values are a **total partition** of "no player streams", so a
consumer can switch on them exhaustively. They are decided in the order
listed; the counts are over the 1 032 stream-less demos of the archive
sweep.

| `reason` | Wire evidence | n | Notes |
|---|---|---|---|
| `demoUnreadable` | the event stream aborted (`errors[]` carries the reader's reason) | 20 | Checked FIRST, because a truncated read invalidates every other conclusion — the match-start announcement may simply sit past the truncation point. 19 of the 20 aborted at stream offset 16 with nothing decoded at all. |
| `midMatchRecording` | `statusAtOpen` names a running game (`"13 min left"`) | 68 | The recording begins after the match-start announcement, so the pipeline has no origin to rebase onto. 67 of the 68 still parse a frag log (30–50 kills is typical). **Weak corner:** the 1 remaining demo — running at open with `kills == 0` — is where this reason states the least. A stale `status` the server never reset, a recording that caught only the final tick of a match, and obituaries this pipeline cannot read are indistinguishable there; `statusAtOpen` + `kills == 0` is the combination to treat as unresolved rather than as "real play we could not rebase". |
| `matchStartUnannounced` | `statusAtOpen` is not running, `status` became running DURING the recording, and none of the four Layer-1 start signals produced an analyzable match | 138 → **0** (v75) | The server started a match under our watch and no analyzable player stream came out of it. **Schema v75 emptied this bucket.** Re-running all 138 demos with the three non-print start signals in place, every one of them now detects a start and produces a full result: 104 on the `matchdate:` stamp (the KTX matchless FFA servers, which never print a start line — `ktx/src/match.c:1294-1297`) and 34 on the `status` transition alone (the ktx 1.38 / 1.40-beta demos plus all 24 `fortress` and 8 `ctf` demos, whose mods write their own running clock into the key). Per-demo output: `.reports/nomatch-marker/recensus-v75-unannounced-138.csv`, with the probe and its rerun recipe in `recensus-v75-probe.go.txt` beside it. The reason is retained, but note the name is now historical: since the `status` transition is itself one of the four signals, what reaches this reason is either a start signal that DID fire over a window holding no reconstructible play, or a running clock that only ever arrived in a bulk `fullserverinfo` dump — a statement of state, not a transition, which cannot raise the event. |
| `noMatchDeclared` | no match declaration this pipeline can see (`status` never named a running game), `kills > 0` | 170 | Usually unmanaged play, but read it as an **absence of evidence**: 168 of the 170 sent no `status` key at all, and the declaration vocabulary this pipeline reads is KTX's — a managed match on a mod that declares itself some other way lands here too. 165 of the 170 ran a foreign gamedir (`fortress` 202 across the whole set, plus `jteams` / `ctf` / `runes` / `tdw` / `bball`). |
| `noPlayRecorded` | no match declaration this pipeline can see, `kills == 0` | 636 | Usually an idle or aborted server; most are a few seconds long with one connected player. Both halves are readings, not proofs — neither a foreign mod's declarations nor its obituaries are legible here, and 51 of the 636 ran a foreign gamedir. |

**`status` spellings.** A running reading is one of exactly two clock
formats: KTX's `"%d min left"` (`ktx/src/match.c:596`, `:723`, `:1330`,
`:1337`) and a CTF mod's `"%d:%02d left"`. The test pins that pair rather
than something looser like "ends in ` left`", and the census is why. Over
the 1 032 stream-less demos the key takes **1 198 distinct values**: 1 183
remaining-time readings, every one matching one of the two forms, and 15
that are not readings at all — `Standby` (`ktx/src/world.c:543`),
`Countdown` (`match.c:2475`), `Forcestart` (`admin.c:693`), the foreign-mod
`Normal`, `Game Ended` (the CTF mod's terminal status) and
`Round 1/15`…`Round 11/15` (gamedir `arena`). The 1 500-demo healthy
control adds 1 213 more values and no new spelling. Those last few prove
mods write their own vocabulary into this key, which is what makes a
looser test the riskier one: a mod's `"2 rounds left"` would read as a
running clock and move a demo to `midMatchRecording` on no evidence,
whereas a third clock spelling reads as idle and lands the demo in
`noMatchDeclared` / `noPlayRecorded` with its verbatim `status` published
beside it, where a reader can see it.

`statusAtOpen` is read from the OPENING `fullserverinfo` dump only — a
`status` that first appears in a later update leaves it absent, because it
describes an instant inside the recording rather than demo open — and it
is a different value from `metadata.serverInfo["status"]`, which is
last-write-wins and so names the state at demo END.

**Why the gamedir is not a reason.** `*gamedir` is the honest
protocol-level statement of which mod ran (the map name is not — foreign
content shows up on `dm6` and stock duels show up on custom maps). But it
does not explain the absence on its own: a `fortress` server can run a
managed match with its own countdown (32 of the 138 demos that used to
carry `matchStartUnannounced` are on foreign gamedirs, and all 32 declare
their match through the `status` key), and a stock `qw` server records ten
seconds of nothing 585 times in this set. It rides along as evidence.

**Validated** over the 1 032 stream-less demos (every one marked, no
leftovers) and a 1 500-demo random control drawn from the 49 919 demos
that DO produce streams: **zero** controls carry a marker.

**REST.** `/overview` republishes the block verbatim beside `errors[]` and
`parseWarnings`; `GET /v1/demos/{id}/artifacts/no-match` serves it
directly (200 with a `null` body value on a demo that holds a match —
that null is the answer).

## HighlightsResult (`highlights`)

Defined in `result/highlights.go` (schema v76). The Key-Moments highlight
catalogue: every **discharge**, **quadbore**, **telefrag** and **airgib**
the match produced, one list per kind, every row carrying what everyone
involved had at that instant — so a consumer builds "biggest discharge",
"most stacked telefrag victim", "most quad thrown away" by sorting, with
no second pass over the streams. Built once by the `highlights`
post-processor (`analyzer/highlights.go`, a servable artifact:
`GET /v1/demos/{id}/artifacts/highlights`) from the final frag log
(`frags:final`, recovered team telefrags included), the per-player
streams and the damage section in its final form — the airgib detector
(`view.ComputeAirgibs`) runs inside the compute;
`view.ComputeHighlights` is the pure function behind it and
`view.FilterHighlights` the `kinds=` / `players=` selection the REST
endpoint applies.

**Absent vs empty.** The section is **absent** (nil) when there is
nothing to build from — no `streams` (no match), no `frags`, or no
`timelineAnalysis` — and `view.Highlights` reports that as unavailable
(`/highlights` → 422 `highlights_unavailable`). A **present** section
with empty lists is a measured zero: the match had none of that kind.
Each list is `omitempty` in the stored JSON; the `/highlights` envelope
always carries all four keys as arrays.

| Field | JSON key | Type |
|---|---|---|
| Discharges | `discharges` | []HighlightEvent (omitempty) — every LG water discharge with evidence (see below) |
| Quadbores | `quadbores` | []HighlightEvent (omitempty) — every self-kill by own rocket/grenade while holding quad |
| Telefrags | `telefrags` | []HighlightEvent (omitempty) — every death booked under the `tele` weapon |
| Airgibs | `airgibs` | []HighlightEvent (omitempty) — the airgib list ([detection](#airgib-detection-viewcomputeairgibs)), with the victim's state |

### HighlightEvent

One shape for all four kinds, so a consumer renders one row type. The
kind-specific fields are omitted on the kinds they do not apply to.

| Field | JSON key | Type |
|---|---|---|
| Kind | `kind` | string: `discharge` \| `quadbore` \| `telefrag` \| `airgib` |
| Time | `time` | int32 match-relative ms — the obituary / damage instant |
| Actor | `actor` | HighlightPlayer — who did it: the discharger, the player who bored, the teleporter, the rocketeer. `relation` is `self`; `killed` says whether they died in the event (a discharge need not kill its shooter; a quadbore and a deflect always do) |
| Victims | `victims` | []HighlightPlayer (omitempty) — everyone else the event touched: killed (`killed`), merely hurt when a damage log exists (`damage` > 0, `killed` false), or on a pentagram deflect the pent holder the actor died on (`survived`). Ordered killed-first, then by damage, then by name |
| EnemyKills | `enemyKills` | int — killed victims with `relation: enemy` (always present, 0 included) |
| TeamKills | `teamKills` | int — killed victims with `relation: team`. The actor's own death is `actor.killed`, never a kill |
| Damage | `damage` | int (omitempty) — damage-log total dealt to the victims (raw family); 0 without a log |
| DamageEnemy | `damageEnemy` | int (omitempty) — given damage to ENEMY victims, **bounded** family (each hit capped by the death-value rule), summed over every enemy hit, killed or not. Set on discharges |
| DamageTeam | `damageTeam` | int (omitempty) — given damage to TEAM victims, bounded family, every teammate hit, killed or not. Set on discharges |
| Sources | `sources` | []string — the evidence the row rests on, in fixed order: `frags` (an obituary named it) and/or `damage` (the damage log carried it). A discharge can come from either alone; the other kinds always carry `frags` |
| Cells | `cells` | *int16 (omitempty; discharge) — the discharger's cell count just before firing. The discharge deals `35 × cells` radius damage (`ktx/src/weapons.c:1208`), so this is the magnitude. Absent when the cells stream has no sample |
| Weapon | `weapon` | string (omitempty; quadbore) — `rl` \| `gl` |
| QuadHeldMs | `quadHeldMs` | int32 (omitempty; quadbore) — how long the quad had been held at the death; subtract from the mode's quad duration (30 s in KTX) for the time thrown away |
| QuadFrags | `quadFrags` | int (omitempty; quadbore) — the actor's non-suicide kills between the quad pickup and the bore |
| TeleKind | `teleKind` | string (omitempty; telefrag) — `telefrag` (the actor killed the victim), `deflect` (the actor teleported onto a pentagram holder and died — KTX dtTELE2 / dtTELE3), `spawnicide` (dtTELE4) |
| Height | `height` | float32 (omitempty; airgib) — victim height above the floor at the hit ([detection](#airgib-detection-viewcomputeairgibs)) |
| HeightAboveAttacker | `heightAboveAttacker` | float32 (omitempty; airgib) — victim origin minus attacker origin (z) |

### HighlightPlayer

One participant with their state at the event's instant.

| Field | JSON key | Type |
|---|---|---|
| Name | `name` | string — the frag log's / streams' name. The literal `teammate` is the frag log's placeholder for a party a team-kill print did not name (see [FragResult.unpaired](#fragresultunpaired)); such a row has identity fields only |
| Team | `team` | string (omitempty) — the stream's team label |
| UserID | `userId` | int (omitempty) — the connection live at the instant, from the per-stream session table |
| Relation | `relation` | string — `self` (the actor) \| `team` (same team as the actor, or a print that asserted a team kill) \| `enemy`. In an individual layout (duel, FFA — every player their own team) every other player is `enemy` |
| Killed | `killed` | bool (omitempty) — died in this event |
| Survived | `survived` | bool (omitempty) — the pentagram holder a deflected teleporter died on |
| Damage | `damage` | int (omitempty) — the damage-log figure for this player in the event; on a telefrag victim, KTX's own `Damage.Telefrags[].bounded` (armor + health) when the demo carries it — a cross-check on the snapshot |
| Health | `health` | *int16 (omitempty) — vitals just before the event (see the snapshot rule). Absent, with the others, only when the player has no stream |
| Armor | `armor` | *int16 (omitempty) |
| ArmorType | `armorType` | string (omitempty) — `ga` \| `ya` \| `ra`; empty when no armor |
| Stack | `stack` | *int (omitempty) — `health + armor`, the telefrag ranking scalar |
| Weapons | `weapons` | []string (omitempty) — tracked inventory held: the subset of `rl`, `lg`, `gl`, `ssg`, `sng` (the STAT_ITEMS bits the streams track) |
| ActiveWeapon | `activeWeapon` | string (omitempty) — the wielded weapon (`sg` … `lg`, `axe`), empty when the wire never said |
| Powerups | `powerups` | []string (omitempty) — the subset of `quad`, `pent`, `ring` held |
| Loc | `loc` | string (omitempty) — loc name at the instant (an airgib victim carries the detector's pre-impact loc) |
| StateSource | `stateSource` | string (omitempty) — where the vitals came from: `stream` (read from the player's own streams), `spawn` (the player had just spawned and the spawn stats had not reached the wire; the vitals are the spawn state 100 / 0), or empty (no stream — every vital absent) |

### The snapshot rule

Every `HighlightPlayer` is read from `streams.players[]` **just before**
the event, not at it:

- **Value streams** (`h`, `a`, `at`, `cl`, `aw`) are read at `t − 1 ms`.
  The obituary and the death-frame stat broadcast share one MVD frame, so
  the sample AT `t` is already the corpse (every telefrag victim on gameId
  212260 carries a `-99` health sample at the frag time); one millisecond
  earlier is the last pre-hit value. Verified against KTX's own figure:
  the `t − 1` health + armor equals `damage.telefrags[].bounded` on every
  telefrag of that corpus demo (118 + 180 = 298, 52 + 112 = 164, 8 + 0 = 8).
- **Interval streams** (weapons, powerups) count as held when an interval
  overlaps `[t − 100 ms, t]`. KTX strips the quad on the death frame, so a
  quad that ends AT `t` still counts; the tolerance is tight on purpose —
  a rocket landing after the quad expired deals normal damage and is no
  quadbore.
- **Stale-life guard.** If the `t − 1` health sample is older than the
  player's latest spawn at or before `t` (`sp`), or there is no sample at
  all (the match-start telefrags at `t = 0`), the sample is the previous
  life's corpse value: the player has just spawned and the spawn stats
  have not been written yet — the same situation the damage analyzer
  reconstructs for its telefrag bound. The row reads `health 100`,
  `armor 0`, `stateSource: "spawn"`; the interval fields stay as read
  (they are current). Verified on the same demo: the two spawn deflects
  (`t − 1` reads −4 / −99 with a spawn after the sample) and the
  832 ms-after-spawn telefrag (sample at the spawn instant, not stale)
  both resolve to 100, matching `bounded`.

### Detection per kind

- **Discharge.** Evidence is any of: an obituary with `cause:
  "discharge"` (the discharger's own death print, or a "drains X's
  batteries" / "accepts X's discharge" kill), or a damage-log hit with
  `weapon: "lg"` and `isSplash` or `isSelf` — the LG beam is hitscan,
  never splash, and cannot hit its own shooter, so that is exactly the
  discharge (KTX fires it through `T_RadiusDamage`, `ktx/src/weapons.c:1208`;
  the reconstruction publishes its discharge candidates the same way, so
  wire and reconstructed logs join alike). Evidence is clustered **per
  discharger within 500 ms** of the first item — one server frame in
  truth, but the print and the damage stamp can straddle an MVD frame —
  into one event: `time` the earliest evidence, `cells` the discharger's
  cells at `t − 1`, `actor.killed` when a suicide print exists,
  `actor.damage` the self hit, victims the union of the damage-log
  victims (with `damage`) and the obituary victims (`killed`). A
  discharge that kills a **teammate** prints one of KTX's cause-less
  team-kill lines (`ktx/src/client.c:5386-5408`; only dtTELE1 / dtSQUISH
  keep their cause in the team branch), so those victims are linkable
  only by coincidence: a same-killer `teamkill` row inside the cluster
  window is folded in as a killed team victim (a paired row names the
  victim; an [unpaired](#fragresultunpaired) one keeps the `teammate`
  placeholder). A discharge that hurt nobody and left no print is not
  observable. Order: kill value desc — one point per killed ENEMY holding
  RL or LG, one more per killed enemy carrying quad (armed kills over
  respawn fodder) — then `enemyKills` desc, `damageEnemy` desc, `damage`
  desc, `time`.
- **Quadbore.** A frag-log suicide with `weapon` `rl` or `gl` whose
  player has a quad interval overlapping `[t − 100, t]`. `quadHeldMs = t −
  interval start`; `quadFrags` the player's non-suicide kills in
  `[start, t)`; `actor.damage` the largest `isSelf` damage-log row of the
  same weapon in `[t − 500, t]`; `victims` the other players this player
  killed with the same weapon within ±100 ms (the same rocket took them
  too). Order: `quadHeldMs` asc (most quad thrown away first), `time`.
- **Telefrag.** Every frag-log row with `weapon: "tele"` — `frags.frags[]`
  (recovered team telefrags included) plus the `tele` rows of
  `frags.unpaired[]`, whose killer is the `teammate` placeholder (the
  death is on the wire; only the teleporter's name is not). `teleKind`
  is the row's `cause` (`""` → `telefrag`). Actor = the teleporter; the
  killed victim's `relation` is `team` whenever the print asserted a team
  kill. On a **deflect** the actor died (`actor.killed`, no killed
  victim) and the pentagram holder rides as a victim with `survived:
  true`: named by the print for dtTELE3 (`frags[].deflector`), resolved
  for dtTELE2 as the ONE other player holding pent in `[t − 100, t]` —
  pent is single-instance on every stock map, so when zero or several
  qualify nobody is named rather than guessed. When
  `damage.telefrags[]` has the same pair within ±200 ms, its `bounded` is
  copied onto the victim's `damage` and `sources` gains `damage`. Order:
  the killed victim's `stack` desc (rows with none — deflects,
  spawnicides — last), `time`.
- **Airgib.** The `view.ComputeAirgibs` run (default look-back; a
  `/highlights?preMs=` request re-runs it with the caller's) wrapped row
  for row: actor = attacker, one victim with `killed = lethal`, `damage`,
  the detector's pre-impact `loc`, plus `height` / `heightAboveAttacker`.
  Same order as the detector (height-sorted).

### Airgib detection (`view.ComputeAirgibs`)

One row per direct enemy rocket hit landed on an airborne victim (an
"airgib"). `height` is the victim's feet above the floor at the hit
(`PositionTrack.H` units); `heightAboveAttacker` is the victim's origin
minus the shooter's at the hit — the vertical gap the rocket climbed,
negative when the victim was below the shooter, `0`/absent when the
shooter had no position sample near the hit (a genuine dead-level hit
also reads 0); the victim row's `loc` is the detector's pre-impact loc.
The victim's `killed` is the lethality heuristic: a matching rocket frag
near the hit — it can over-attribute on a rare back-to-back
double-rocket exchange (two rockets, same attacker→victim, within the
window), fine for a highlight, not an exact killing-blow flag. The
`actor.userId` / `victims[].userId` are resolved **at the hit's own
instant** — see [Player userids](#player-userids-schema-v66).

Detection is **`view.ComputeAirgibs`**, a pure function of the assembled
`Result` — the per-hit damage log (`Damage.Events`), the streams'
`PositionTrack.H` column, the frag log, the per-stream session tables and
the loc table, all on one match-relative clock. The `highlights`
post-processor bakes the **default-options** run into the stored
`Result`; because the inputs are all published, the same function re-runs
per request with a caller-tuned look-back (`/highlights?preMs=`), which
the stored list cannot express.

A hit qualifies when `weapon == "rl"` and it is a **direct
hit** (`isSplash` false), the attacker is an enemy (not self / teammate /
world), and the victim reads **clear air** on pre-impact evidence
(schema v73): every position sample in the look-back window
`[hit − preMs, hit − 40 ms]` (default **100 ms**) is ≥ 96 units (≈ two
player models) above the floor, and **no sample beside the hit reads
ground contact**. The gate rests on which readings can be trusted for
what: KTX writes the damage message inline in `T_Damage`, and measured
over 410 direct rocket hits the stamp lands in the same wire frame as
the first knockback-visible position sample 82% of the time, a frame
early 12%, and up to two frames (+28 ms) late 6% — so samples within
40 ms of the stamp may already carry the rocket's own knockback.
Knockback contamination is one-sided: it can *over*-report height (a
standing victim blasted off the dm2 `func_train` read 303 units of air
at the stamp) but cannot fake a grounded reading — so high readings
near the stamp are ignored, while a grounded one vetoes (a victim who
fell and *landed* just before the rocket). When the window holds no
sample within 60 ms of its start — an old demo whose tick cadence
exceeds the window, or a recording hole — the **preceding tick** (the
sample carried forward at the window start, up to 250 ms older) decides
instead; a sample just before a populated window is deliberately NOT
consulted, so a victim who left a high ledge right after the window
opened still counts. The 100 ms default is an aesthetic choice, not a
physics bound: floor-relative height is a step function at ledge edges,
so a longer window measures time-since-the-edge rather than hang time
and drops genuinely spectacular ledge-drop hits (a 200 ms window
measurably lost 300+-unit events from the golden corpus). A `NoFloor`
sample never counts as airborne. Reported `height`, `loc` and
`heightAboveAttacker` come from the latest **pre-impact** sample — the
victim as the rocket found them — not from the possibly
knockback-contaminated hit-frame sample.

Both damage sources participate (schema v73): the `highlights` DAG node
binds `damage:final`, so pre-instrumentation demos get airgibs from the
**reconstructed** damage log — damagerecon's direct-vs-splash split is
geometric (direct = the `TE_EXPLOSION` / projectile endpoint within 48
units of the victim) and its timestamps frame-accurate, which is the
fidelity this verdict needs. `damage.source` says which evidence a demo's
list rests on.

Every qualifying hit is emitted (uncapped — the ≥ 96 threshold already
bounds the list), ordered by `height` descending; the web view re-sorts
client-side. mvd-api's `/highlights` serves the stored list at the
default `preMs=100` and recomputes the airgib list for any other value in
`0..1000` (`preMs=0` = no pre-hit gate, the pre-v73 rule), echoing the
effective value as `preMs` on the envelope. **Empty when the map has no
clip hull** (no `PositionTrack.H` to read — same BSP provisioning as the
visibility-aware loc filter).

## ParseWarnings (`parseWarnings`)

Schema v72. The MVD reader's own census of what it could **not** read
off the wire, carried on every result and omitted entirely when the
parse was clean.

**Why it exists.** The `sv_bigcoords` angle desync degraded ~5% of the
archive for years without a single operator-visible signal, because
parse warnings were collected only in the diagnostic test harness and
dropped on every production run. Collection is now unconditional: the
next protocol gap shows up on a normal analysis, in the JSON, over REST
(`/overview`) and on `qw-analyze`'s stderr.

**How it differs from `errors[]`.** `errors[]` reports ANALYZER-level
failures over events that decoded fine (a `Finalize` returned an error,
the region-control pass failed, the stream aborted mid-demo).
`parseWarnings` reports the layer below: bytes the decoder could not
interpret. Neither is fatal, and they are deliberately never merged — a
consumer reacting to a degraded result needs to know which layer lost
the data.

| Field | JSON key | Type | Intent |
|---|---|---|---|
| Total | `total` | int | Exact number of warnings raised. **Never capped.** |
| ByType | `byType` | map[string]int, omitempty | Exact per-category counts. Fixed vocabulary: `parse_error` (a payload we recognise but failed to decode), `unknown_svc` (an `svc_*` command byte we do not know — the rest of that payload is abandoned), `unknown_te` (an unknown temp-entity type), `unknown_hidden` (an unknown MVD hidden-message type). |
| Groups | `groups` | []ParseWarningGroup, omitempty | Capped sample table of distinct (type, message) rows, **loudest first** (count desc, then type, then message — the order is deterministic so the same demo always serialises identically). |
| DroppedWarnings | `droppedWarnings` | int, omitempty | Warnings beyond the 64-group retention cap — every occurrence whose `(type, message)` pair was first met after the table was full, so it is missing from `groups`. An OCCURRENCE count, never a count of distinct messages: the reader does not retain the keys past the cap, since holding them is the unbounded memory the cap exists to avoid. |

`ParseWarningGroup`:

| Field | JSON key | Type | Intent |
|---|---|---|---|
| Type | `type` | string | The category, as in `byType`. |
| Message | `message` | string | The reader's message — typically the `svc_*` name, the decode error and the byte count it abandoned. |
| Count | `count` | int | How often this exact message fired. |
| FirstDemoTimeMs | `firstDemoTimeMs` | int32 | **RAW DEMO time** of the first occurrence (the wire clock, t=0 = demo open) — *not* the match-relative base every other ms field in this schema uses. The reader has no match clock, and a warning can fire before the match starts or on a demo with no match at all, where a rebased value would be meaningless. |

**Summary, not a log.** A pathological demo can raise hundreds of
thousands of warnings (the archive's worst carries ~10 000), so the
Result carries a census rather than every instance: exact counters plus
a first-occurrence sample per distinct message. The message text embeds
the failing command and the abandoned byte count, so its cardinality is
unbounded in principle; the sample table is therefore capped at
`parser.MaxWarningGroups` (64) distinct rows. **The cap never touches a
count** — `total` and `byType` stay exact, and what the table is missing
is stated in `droppedWarnings` (warnings beyond the 64-group retention
cap) rather than silently truncated. That figure counts OCCURRENCES, not
distinct messages: past the cap the reader stops retaining keys, which is
the whole point of the cap. Groups are retained in FIRST-ENCOUNTER order,
so on a badly broken demo `groups` is a sample of the distinct messages,
not the top-k by count — read `byType` for the shape of the damage and
`groups` for what it looks like. The full per-instance list remains available to the
diagnostic harness (`parser.SetDiagnosticMode` +
`Parser.DiagnosticWarnings`), which is the only consumer that needs it.

**Reading it.** A present block means the reader hit a gap on this demo
and the sections downstream of the affected bytes may be thin — which
sections depends on what was lost, so treat it as a prompt to check the
data rather than as a verdict on any one field. `unknown_svc` in
particular means an entire payload was abandoned mid-message. On the
51 000-demo archive census 526 demos (~1.0%) raise at least one warning.

CLI: `qw-analyze` prints a one-line summary to stderr whenever a demo
raises any, and `-warn` prints the whole `groups` table (useful for
`-format md` and `-view …` runs, which carry no `parseWarnings` in their
output). REST: `/overview` republishes the block verbatim.

## Cross-references / join keys

- `weaponPickups[i].backpackEnt` ↔ `backpacks[j].entNum` —
  drop-to-pickup join, `source=="backpack"` only.
- `streams.players[].li[].v` → `timelineAnalysis.locTable[i]` —
  resolve player loc name.
- `controlRegion.locs[]` ↔ `locTable[]` — region membership.
- `playerUserIDs[name]` → Hub viewer track parameter. Per session, and
  for a reconnected player the LAST session with play — see
  [Player userids](#player-userids-schema-v66).
- `streams.players[].identity` ↔ `playerStats.players[].identity` — two
  rows with the same value are ONE human (a reconnect the server scored
  as two players). Demo-local; never a cross-demo key. Every other
  name-keyed view (`lives`, `top-windows`, `frags`, `damage`, `buckets`)
  joins to these two by the row's **player name** — see
  [Player identity and sessions](#player-identity-and-sessions-schema-v66).
- `streams.players[].sessions[].userId` → the Hub track parameter valid
  *inside that session's window*, and `…sessions[].slot` the wire slot the
  connection held — the lossless form of the answer `playerUserIDs` gives
  once per player.
- `match.players[].name` ↔ `frags.byPlayer[]` ↔
  `demoInfo.players[].name` ↔ `streams.players[].name` — same name
  resolves through every layer (canonicalised by the demoinfo
  resolver). Mid-match name collisions get `#slot` suffix on the
  streams entry.

## Layered views (intentional overlap)

Several pieces of data appear in more than one section by design.
Pick the shape that matches your consumer:

| Data | Lean source | Rich source | Pick lean when… |
|---|---|---|---|
| Frag list | `frags.frags[]` | `messages.events[type=frag]` | …you want kill-classification flags (`isSuicide`, `isTeamKill`). |
| Frag list | `messages.events[type=frag]` | `frags.frags[]` | …you want the obit text for display. |
| Score timeline | `timelineAnalysis.fragEvents` | `frags.frags[]` | …you only need delta over time (no killer/victim). |
| Per-player deaths | `timelineAnalysis.deathEvents` | `frags.byPlayer[].deaths` | …you need per-death timing (not just totals); counts every death, no teamkill-victim drops. |
| Per-player kills | `timelineAnalysis.killEvents` | `frags.byPlayer[].kills` | …you need per-kill timing keyed on the killer (enemy kills only); cumulative count reconciles with `byPlayer.kills`. |
| Per-player stats | `match.players[]` | `demoInfo.players[]` | …you only need name/team/frags. |
| Per-player stats | `playerStats.players[]` | `match.players[]` | …you need accuracy / damage / pickups / possession time. Computed for EVERY demo — families degrade to `src: "derived"` rather than disappearing on a demo with no KTX block. `demoInfo.players[]` stays the verbatim KTX pass-through to diff against. |
| Match length | `match.duration` | `demoInfo.duration` | …you want the parser-derived float. |
| Match length | `demoInfo.duration` | `match.duration` | …you want the KTX integer. |
| Loc names | `timelineAnalysis.locTable` | `locationData[].name` | …you need integer indexing from `Li`. |
| Loc names | `locationData[]` | `locTable[]` | …you need the world coordinates. |

`demoInfo` is **verbatim from KTX** apart from the individual-layout team
relabelling described under [DemoInfoResult](#demoinforesult-demoinfo); if a
duplication exists, the canonical fix lives on the other side.

## Schema versioning history

The field tables above describe the **current** schema. This table
records what each bump changed, for consumers migrating across versions.

| Version | Changes |
|---|---|
| v76 | **The highlight catalogue: discharges, quadbores, telefrags and airgibs as one stored section, each row carrying what everyone involved had.** New top-level **`highlights`** ([HighlightsResult](#highlightsresult-highlights)): four lists of `HighlightEvent` — `discharges`, `quadbores`, `telefrags`, `airgibs` — sharing one shape (an `actor`, the `victims`, `enemyKills` / `teamKills`, the damage-log `damage`, the evidence `sources`, and per kind the `cells` dumped, the quad `quadHeldMs` / `quadFrags`, the `teleKind`, the airgib heights). Every participant is a `HighlightPlayer`: `relation` to the actor, `health` / `armor` / `armorType` / `stack` read from the streams **one millisecond before** the event (the death frame already carries the corpse; verified equal to KTX's own `damage.telefrags[].bounded` on every corpus telefrag — 118 + 180 = 298, 52 + 112 = 164, 8 + 0 = 8), the tracked `weapons`, the wielded `activeWeapon`, the `powerups` and the `loc`; a sample older than the player's latest spawn is the previous life's and reads as the spawn state (`stateSource: "spawn"`). Discharges join the obituary cause with the damage log's `lg` + `isSplash` / `isSelf` hits (wire and reconstructed alike), clustered per discharger within 500 ms, with KTX's cause-less team-kill prints folded in by coincidence; a pentagram deflect names the pent holder the teleporter died on (`survived: true`). Built by the `highlights` post-processor (a servable artifact) from `frags:final`, the streams and `damage:final`, the airgib detector running inside the compute; absent when the demo has no streams or no frag log. Served at **`GET /v1/demos/{id}/highlights`** (`kinds=`, `players=`, `preMs=` for the airgib look-back), as the MCP tool **`getHighlights`**, as the default-set `/events` kinds **`discharge`** / **`quadbore`**, and as `qw-analyze -view highlights`. **`frags.frags[]` / `frags.unpaired[]` gain `cause`** — `discharge` (weapon stays `lg`, as KTX's own per-weapon stats count it), `deflect` (dtTELE2 / dtTELE3, weapon `tele`, a suicide) or `spawnicide` (dtTELE4) — and **`deflector`**, the surviving pentagram holder when the dtTELE3 print names them; `byWeapon` is unchanged. **MOVED:** the airgib list — `timelineAnalysis.airgibs` is REMOVED and `GET /airgibs` retired; `highlights.airgibs` (the victim's state added) is the list's only home, `/highlights?kinds=airgib&preMs=` replaces the endpoint with the same `preMs` semantics, `-view airgibs` gives way to `-view highlights`, and the `airgib` `/events` kind reads the new list with unchanged detail keys. Every full golden moves by the new section, and the demos carrying discharge / deflect / spawnicide prints by `cause`. |
| v75 | **The match boundary becomes a Layer-1 event, and KTX matchless servers become analyzable at all.** New on `streams.global`: **`matchStartSignal`** (`ktx-matchstart` \| `print` \| `matchdate` \| `status`), naming which WIRE SIGNAL declared the match start; republished on `/overview` as `timing.matchStartSignal`. It is not `matchStartSource`, which names where the wall-clock VALUE came from — the two vocabularies share the token `matchdate` and answer different questions. The pipeline used to key the whole match boundary on one broadcast line, and a KTX server running `k_matchless 1` (continuous FFA / CTF play, no ready-up, no countdown) never prints it: `ktx/src/match.c:1294-1297` gates `"The match has begun!"` on `!k_matchLess`. Such a demo detected no match start, so it produced no `streams` and therefore no buckets / damage / `playerStats` / `locGraph` / aim, and — because the parser's obituary-death corroborator is gated on the same verdict — zero deaths on a demo full of frags. The reader now emits one **`MatchStartEvent`** per demo (`mvd-reader/parser/matchstart.go`) at the FIRST of four signals to reach the wire: the line-initial `matchdate:` broadcast (`match.c:1291`), a print matching `MatchStartPatterns` (`match.c:1296`), the `//ktx matchstart` stuffcmd (`match.c:1372`, STUFFCMD_DEMOONLY, no cvar gate at all) or a serverinfo `status` update moving to a running clock from a value that was not one (`match.c:1337`). `StartMatch()` emits the first three in the SAME server frame on modern KTX, so the match-start TIME does not move on any demo that already had one — verified byte-for-byte over the golden corpus and over a 1 500-demo healthy archive control (`.reports/nomatch-marker/recensus-v75-healthy-1500.csv`). What changed is which demos have one: re-running the 138 demos the archive sweep marked `matchStartUnannounced`, **all 138** now detect a start and return a full result — 104 on `matchdate`, 34 on the `status` transition alone (which includes every one of the 24 `fortress` and 8 `ctf` demos, whose mods write their own running clock into the key); per-demo output in `.reports/nomatch-marker/recensus-v75-unannounced-138.csv`. That reason keeps its name and now means "the server moved `status` to a running clock and still no analyzable match came out"; it is `plan-archive-features.md` lead 8 stage (b) for the KTX half. Layer 2 stopped re-scanning prints for the start: `MatchTimingDetector.OnPrint` is now the match-END half only, `OnMatchStart` latches the event, and `statusNamesRunningGame` moved to Layer 1 as `events.StatusNamesRunningGame` — the duplication between the parser's gate and the analyzers' is what let the two layers disagree on matchless demos in the first place. One fix rides along, exposed by the new matchless corpus entries: `timelineAnalysis.fragEvents` no longer carries the scoreboard zeroing `SV_DropClient` broadcasts when a player quits AFTER the match ended (`mvdsv/src/sv_main.c:419-428`) — the timeline recorded frag updates on "started" alone and never on "not ended", and the `Vacated` handler that drops that phantom sits inside the match window, so a post-intermission quit contributed a negative frag dated past `matchEnd`. It needs a recording that runs past match end, which is normal on a matchless server. **The same version also gives the pipeline one game mode.** New: **`match.gameMode`** (`canonical` / `teamBased` / `rounds` / `submodes[]` / `sources`, republished on `/overview` as `gameMode`), **`match.players[].rawTeam`**, **`demoInfo.deathmatch`** and **`demoInfo.teamplay`** (KTX's `dm` / `tp`, parsed since v1 and dropped until now), plus the new `match.sources.teams` value **`individual`**. It replaces five unrelated mode vocabularies and four hand-written "is this a solo mode" tables that disagreed with each other. The verdict that matters is `teamBased`, which mirrors KTX's own `tp_num()` gate (`(isTeam() || isCTF() || coop) ? teamplay : 0`, `g_utils.c:1586-1588`): when it is false the match is laid out INDIVIDUALLY — `match.teams` one row per player, every `match.players[].team` equal to the player's own name, `playerStats.teams` absent, region control withheld above two participants — which is the layout duels already produced, so the `team === name` shape test identifies it with no mode string. In FFA a userinfo `team` tag is clan decoration (KTX forces `teamplay 0`, `world.c:1652-1655`): on `ffa_1[dm2]` three of eight players wear `red`, and reading the tag as a side flagged **34 of 211 kills as team kills**, dropped those three from each other's aim enemy sets and summed them into pseudo-teams. Team kills, team damage, the aim enemy set, the scoreboard team rows, the `playerStats` team aggregate and the region-control layout all read the descriptor now; `//finalscores` is no longer adopted as a team list where it names the top-two PLAYERS. Finally, a player↔spectator transition (mvdsv `join` / `observe`, `sv_user.c:2680-2830` — same slot, same userid, the mod's `ClientDisconnect` run first) now ends the occupancy like a drop, so the leaver's announced score is kept (`ffa_1[tox]`: nexus 0 → **18**) and `streams.players[].sessions` closes at the departure instead of at the end of the recording. Goldens move by the new fields, by the FFA team relabelling, and — on `4on4_fu_mix_060626_dm2_rename_handover` — by one published session now starting at its join rather than at the spectator connection before it. |
| v74 | **Demos that hold no analyzable match are marked instead of coming out silent.** New top-level **`noMatch`**, present exactly when `streams` is absent: a `reason` from a five-value total partition (`midMatchRecording` \| `matchStartUnannounced` \| `noMatchDeclared` \| `noPlayRecorded` \| `demoUnreadable`), a human `detail` sentence, and the wire evidence behind the verdict — `statusAtOpen` (the serverinfo `status` key at demo OPEN, verbatim; distinct from `metadata.serverInfo.status`, which is last-write-wins and names the state at demo END), `statusRunningSeen`, `gameDir` and `kills`. It exists because absence was previously ambiguous AND silent: 1 032 of the 50 951-demo archive sweep (2.03%) produced empty streams with an entirely EMPTY `errors[]`, so a consumer could not tell "this recording holds no match" from "the recording starts mid-game" from "the parse failed". The v52 `timeBase:"demo"` fallback does not cover it — `flagDemoTimeBase` returns early on `result.Streams == nil`, and streams are nil precisely because every timeline recording path is gated on a match-start broadcast that was never seen; the fallback only ever fires when a start WAS detected but landed at demo `t=0`. Distribution over the 1 032: `noPlayRecorded` 636, `noMatchDeclared` 170 (165 of them on a foreign `*gamedir` — `fortress`, `jteams`, `ctf`, `runes`, `tdw`, `bball` — and 168 sending no `status` key at all), `matchStartUnannounced` 138 (32 carrying a full KTX demoinfo block), `midMatchRecording` 68 (67 with a parsed frag log), `demoUnreadable` 20; every demo marked, no leftovers, and **zero** markers on a 1 500-demo random control drawn from the 49 919 that do produce streams. The gamedir is published as evidence and never used as a reason — a `fortress` server can run a managed match and a stock `qw` server can record nothing. `noMatch` also gives the wall-clock **`dateMarkers`** a home on a stream-less result (104 of the 1 032 carry one): they were read off the wire and then dropped, since the whole family lives on `streams.global`. The graded `matchStartUnixMs` anchor beside them is deliberately NOT published there — it is a projection through the match window, which such a result does not have; that is salvage, plan lead 8 stage (b). Additive: `errors[]` is untouched, `/overview` gains `noMatch` beside it, and the new `no-match` DAG node serves at `/v1/demos/{id}/artifacts/no-match` (200 with a `null` body on a demo that holds a match). Goldens do not move — no healthy demo is affected. |
| v74 | **Partial damage reconstruction becomes visible per demo.** `damage` gains **`coverage`** `{kills, covered, ratio}` on every `source: "reconstructed"` section — the share of the frag log's weapon kills whose lethal instant the reconstructed `events` log accounts for, i.e. `cmd/qw-recon-oracle`'s kill-delta coverage computed in the normal pass (`damagerecon/aggregate.go setCoverage`). It exists because a small class of archive recordings barely broadcasts the health/armor stat channel: positions and the frag log survive, the damage does not, and until now nothing distinguished "this player dealt little damage" from "the recording did not carry the evidence". Measured over the full 10 702-demo oracle sweep (rescored after v74 shipped from a 200/era subsample): **99.0% read ≥ 0.95** (median 1.000), **0.80% read < 0.50** (the silent-channel class, 0.182 median / 0.488 worst) and **0.18% fall between**, spanning 0.500–0.944 — a hard bimodal core with a thin gradient tail, so `ratio` is a magnitude to read rather than a two-valued flag, and it measures the FRAG-LOG-VISIBLE match (a loss that drops obituaries and damage together still reads 1.000); the same metric over a WIRE damage log reads exactly **1.000 on all 65** ground-truth demos, and thinning the stat channel to one sample in four drops a healthy demo to a 0.35 median while `kills` stays put (both controls pinned in `damagerecon/coverage_test.go`). Bounded damage per kill — the other candidate figure — was measured and rejected: the two populations overlap on it (healthy min 107, silent max 326). Absent on `source: "ktx"` (a wire log records every `T_Damage` call, so coverage is 1 by construction) and when the frag log names no scoreable kill. Whole-match: a `players`/`weapons`/time filter carries it through unchanged, like `source` and `boundedMode`. NOTHING is gated on it — the riders that inherit the signal (`playerStats` damage family, its reconstructed `accuracy.hits`, `aim.players[].weapons[].recon.hits`) point at this one field instead of copying or withholding. Goldens move on the reconstructed-damage demo only. |
| v74 | **A derived demoinfo summary for the half of the archive with no KTX block.** `playerStats.players[].score` gains **`maxSpree`** and **`maxQuadSpree`** — the kill-streak maxima KTX writes as `spree.max` / `spree.quad`, replayed from the corrected frag log, the death markers and the quad possession runs on every demo (`analyzer/spree.go`). They ride `score.kills`' `killsMeasured` gate, so they are present and absent with it, and a team row carries the best any member ran rather than a sum. `playerStats.players[].accuracy` gains **`src: "reconstructed"`** and, with it, `hits` on pre-instrumentation demos: the values are read straight off the published `aim.players[].weapons[].recon` tier, so its weapon-level withholds (`ng`/`sng` carry none) inherit rather than being restated — the `player-stats` DAG node now binds `aim` for exactly this. `accuracy.byWeapon[]` additionally gains **`hitsConvention`** (`anyDamage` \| `directImpact` \| `pellets`), present whenever `hits` is: `src` is the evidence grade and says nothing about WHAT is counted, while one `src: "ktx"` row uses all three conventions at once (lg any-path, rl/gl direct impacts, sg/ssg pellets). Two rows are comparable exactly when weapon AND convention match. Validated withhold-and-compare against the verbatim KTX block on 188 instrumented archive demos / 665 player rows (`cmd/qw-demoinfo-eval`): `maxQuadSpree` 99.8% exact, `maxSpree` 99.6% on rows whose kill count already agrees and whose player never suicided (KTX credits a suicide to its own streak wherever teamplay is off — the one deliberate divergence), powerup `took` 100.0% exact and possession seconds 0.1–0.5% off on all three powerups, `frags`/`deaths` 99.5%/99.7%, reconstructed `damage.given`/`taken` 0.5% aggregate, `accuracy` `attacks` 98–100% exact on every single-projectile weapon and lg `hits` 0.9% aggregate. The KTX rl/gl convention was initially NOT derivable on a blockless demo — the wire log's own splash flag reproduces KTX's rl `hits` on 638 of 638 rows, but the reconstruction's first substitute (an explosion endpoint within 48 units) answered gl at 1.2% aggregate and over-counted rl by +80% — so those counts shipped as `anyDamage`, marked rather than coerced. The row below closes that gap. Goldens move by the new fields, the `accuracy.src` flip on the reconstructed-damage demo, and one `maxSpree` (a same-instant mutual frag, now ordered by the frag log rather than by event kind). |
| v74 | **Reconstructed radius damage follows the engine's own order and reach** (a **correctness fix**: values move on `damage` and everything derived from it — `playerStats.damage`, `accuracy.rl|gl|lg.hits`, `aim...recon` — and no field is added, removed or retyped). Two facts from `ktx/src/combat.c`. (1) The quad multiplies the falloff's RESULT, not the base: `T_RadiusDamageApply` computes `points = 120 − 0.5·dist` (`:1189`) and `T_Damage` applies the ×4 afterwards (`:537-543`), so a quad splash spans `(0, 480]` and not the `[400, 480)` the package modelled — 96% of the wire's own 898 quad rl splash rows on the dm2/dm3 ground truth read between 160 and 400. Every multiplier that composes this way (quad, the dmm4 octa, the CTF strength rune, the handicap) sits on that downstream side; the self-halving does not. (2) Splash reaches `findradius(damage + 40)` = 160 units and no further (`:1252`), measured origin-to-bbox-centre exactly as the falloff is, and the quad does not widen it — so candidate admission drops from 380 units to the reach plus the measured slack of our own distance (184 from a snapped detonation point, 220 from a tracked flight's last broadcast position), and the LG discharge gets the same bound at `35·cells + 40`. The kill top-ups' two calibration constants are deleted: they absorbed the ordering error, and with it fixed the raw error falls monotonically as they approach the engine's numbers. Three review corrections ship with them: a direct rocket KILL is topped up at `T_MissileTouch`'s flat constant rather than on the radius curve (the curve made a point-blank quad direct 480 where the engine deals 440); the projectile geometry PRIOR gets its own measured normalizer, since dividing it by the admission radius made this row's cap change silently re-weight attribution; and a `deathmatch 4` recording that contains a quad stands the reconstruction down, because KTX makes the quad an OCTA there (`:541`) and this model does not follow it (4 demos in a random 2 000-demo archive sample). Measured on the 60-demo dm2/dm3 ground truth, before → after all of it: raw given median/mean 1.24%/2.04% → **0.71%/1.21%**, raw taken 1.74%/2.29% → **0.66%/1.13%**, bounded taken unchanged at 0.04%, bounded given 0.58%/0.76% → 0.58%/0.79%, bounded `givenSelf` 1.43%/3.90% → **1.28%**/3.99%. On the archive derived-summary protocol `acc.gl.hits` 3.91% → **3.55%**, `acc.lg.hits` 0.91% → 0.89%, `acc.rl.hits` 1.23% → 1.25% (0.65% on the `fixed` population), against `dmg.given` 0.47% → 0.48% — the regressions, decomposed in [damagerecon/ACCURACY.md](damagerecon/ACCURACY.md), starting with 3 wire splash rows in 14 140 whose measured geometry lands past the distance the engine visits. Goldens move on the reconstructed-damage demo only. |
| v74 | **A reconstructed row answers KTX's rl/gl question.** `playerStats.accuracy.byWeapon[rl\|gl].hits` on a `src: "reconstructed"` family is now the DIRECT-IMPACT count, marked `hitsConvention: "directImpact"` — the same quantity KTX's own `acc.rl.hits` / `acc.gl.hits` carries, so the pre-instrumentation half of the archive and the block-carrying half are on one scale for those two weapons. It rides a new `aim.players[].weapons[].recon.directHits` (rl/gl only, absent elsewhere because the two conventions coincide there), beside the unchanged any-path `recon.hits`. What made it derivable is `damagerecon/direct.go`: KTX increments those counters in the touch handler alone (`ktx/src/weapons.c:990-996`, `:1327-1333`), so the question is "did the projectile touch a player", and two engine facts answer it where endpoint proximity could not — the flight's TRAJECTORY, followed 44 units past the detonation, against the victim's 32×32×56 hull (both projectiles are zero-size point entities and a rocket flies a straight line, so its two broadcast endpoints determine the whole path); and the MAGNITUDE, since `T_MissileTouch` deals a flat constant and hands the victim to `T_RadiusDamage` as the `ignore` entity (`:998-1006`) while splash is `120 − 0.5·dist` — over 3 275 wire rl rows, 623 of 623 direct rows read exactly 110 (440 quadded) and exactly one splash row did. The constant is era-dependent (KTX commit `c7263e8f`, 2008-09-29, replaced id1's `100 + g_random()*20`) and the era is detected from the demo's own distribution, never from a version string. Measured per explosion against the wire splash flag over 18 034 rl instants: classification accuracy 73.5% → **97.9%**, precision 45.1% → **94.6%**, direct-count error +114% → **+1.4%**. Measured per player against the verbatim block on the same 186 archive demos: `rl` **1.25%** aggregate (46.5% of 632 rows exact, −0.13 per row) and `gl` **89.6%** of 424 rows exact (−0.07 per row, 3.55% aggregate), both well under the shipped `lg` row's −0.91 per row. A third engine fact carries the gl half: `GrenadeExplode` runs on a 2.5 s think, so a grenade whose broadcast flight spans the whole fuse and ends in a matched `TE_EXPLOSION` died of the fuse and `GrenadeTouch` never ran — a certain non-touch (only that direction of the fuse signal is usable, since the flight bracket is entity visibility and an early end is a PVS exit as often as a touch). It removes 20 of 28 over-counting rows for 2 new under-counting ones; the aggregate rises because the residual stops being two-sided, not because the classifier got worse. A `src: "derived"` row keeps `anyDamage` on rl/gl — its `hits` is also `aim`'s MEASURED counter, a validated any-path number. Two damage-model corrections ride along: rocket splash is based on **120**, not the direct 110 (`weapons.c:1006` — the 110 belongs to a victim the radius pass skips), worth bounded `given` 0.81% → 0.76% and `dmg.given` vs KTX 0.49% → 0.47%; and obituary-anchored rocket kills, which never reached the candidate scorer, stop publishing `isSplash: false` by default. The measured era rides along as **`damage.rocketDirectDamage`** — the direct constant the demo's own hits established — beside the three-valued **`damage.rocketDirectRegime`** that says which verdict was reached, because "no constant" was two claims wearing one face: `spread` (enough near-direct hits to test, and they did not cluster — evidence against the constant) and `unestablished` (too few to put the question). The three populations split that rl figure into 0.65% aggregate / 45.9% exact on 567 `fixed` rows, 13.0% / 31.2% on 16 `spread` rows and 22.5% / 59.2% on 49 `unestablished` ones; nothing is gated on any of it, since the alternative is the 4x any-path count. Goldens move on the reconstructed-damage demo (`directHits`, `rocketDirectDamage`, `rocketDirectRegime`, the `hitsConvention` flip, and the damage rows' `isSplash`). |
| v74 | **The fire→flight association reaches the Result, and with it rl/gl hit recovery on old demos.** `shots.shots[]` gains **`flightEnd`** — the match-relative time at which the tracked rocket/grenade (or nail) a fire launched died. The shots analyzer has always bracketed those flights, since that IS how a projectile fire's `hit` is decided (`analyzer/shots.go linkProjectiles`), and then discarded the association; it is now published. Absent on hitscan fires and on a projectile whose entity the server never broadcast — which is exactly the state the measured hit counter reads as a miss — and set whether or not the impact damaged anyone. On the back of it, the reconstructed hit tier now covers **`rl` and `gl`**: `aim.players[].weapons[].recon.hits` appears for them on demos whose damage section is reconstructed, joined flight-impact-instant → reconstructed damage instead of by counting impacts, which is what made the two conventions differ by ~7 pp on rl in v73. Measured over 53 dm2/dm3 demos carrying the KTX log (rows ≥ 20 fires): mean accuracy error **rl 0.6 pp** (bias +0.4) / **gl 0.3 pp** (bias 0.0) vs the measured counter, with the join-on-wire control at 0.4 pp / 0.1 pp — rl 0.5 pp / gl 0.4 pp after the direct-impact and radius-damage rows below moved the damage model; `lg`/`sg`/`ssg`/`axe` unchanged to the last row. `ng`/`sng` stay withheld — nail linking is opt-in, so their measured counter is zero everywhere and there is nothing to validate a recovery against. Goldens move by exactly the new fields. |
| v73 | **Airgib detection gates on pre-impact evidence** (a **correctness fix**: entries move in and out of `timelineAnalysis.airgibs`, no field is added, removed or retyped) plus a `preMs` echo on the `/airgibs` envelope. KTX writes the damage message inline in `T_Damage`, and measured over 410 direct rocket hits the stamp lands in the same wire frame as the first knockback-visible position sample 82% of the time and up to two frames (+28 ms) late 6% — so samples near the stamp can already carry the rocket's own knockback, and the hit-time sample alone reported players who were STANDING at impact as airborne (hub `232925`: a victim riding the dm2 `func_train` read **303 units of air** when a quad direct rocket blasted him off it, published as the match's biggest airgib). A hit now qualifies when every position sample in `[hit − preMs, hit − 40 ms]` (default **100 ms**) reads ≥ 96 units above floor — the preceding tick deciding when the window holds no sample (old coarse-tick demos, recording holes) — and no sample beside the hit reads ground contact (knockback over-reports height but cannot fake a grounded reading, so a victim who fell and landed just before the rocket rejects while one knocked laterally over a higher floor does not). The 100 ms default is aesthetic: floor-relative height is a step function at ledge edges, so longer windows measure time-since-the-edge and drop genuine 300+-unit ledge-drop events. Reported `height`/`loc`/`heightAboveAttacker` come from the latest PRE-IMPACT sample. Detection moved from the analyzer post-processor into [`view.ComputeAirgibs`](#airgibevent), a pure function of the assembled `Result` (the `regionControlPost` / `view.RegionControl` staging): the post-processor bakes the default-options run into the stored `Result`, and mvd-api's `/airgibs` re-runs it per request with `?preMs=` (`0..1000`, `0` = the pre-v73 hit-sample-only rule; outside the range is a 400 `invalid_param`), echoing the effective value as **`preMs`** on the response envelope. Per-hit userids now resolve against the PUBLISHED per-stream session table (`streams.players[].sessions`) rather than an analyzer-internal index — same answers, one clock. Airgibs also now consume **reconstructed** damage (the DAG node binds `damage:final`), so pre-instrumentation demos get a Key-Moments list too — recon's direct/splash split is geometric (explosion endpoint within 48 units) and its timestamps frame-accurate; `damage.source` says which evidence a list rests on (supersedes v71's wire-measured-only gate for airgibs; aim's MEASURED counters keep it — the reconstruction feeds only aim's separate `recon` tier, see the other v73 row). |
| v73 | **Aim hit recovery on reconstructed demos.** `aim` gains `hitsSource` (`ktx` \| `reconstructed`, absent when the demo carries no damage section) and, on reconstructed demos only, `aim.players[].weapons[].recon.hits` — the fire→damage join re-run against `damagerecon`'s log, so accuracy exists on the ~45% of the archive that never carried `mvdhidden_dmgdone`. Additive and strictly separate: `hitsMeasured` keeps its v71 meaning (still **false** on a reconstructed section) and every measured counter stays withheld there, so a reconstructed hit count can never be read as a measured one — it only ever appears under `recon`. Emitted for the same-server-frame weapons `lg`/`sg`/`ssg`/`axe`, validated exact against the wire log (the join reproduces the measured counters from a wire log with zero error; against the reconstruction it costs 0.3–1.7 pp of accuracy, `damagerecon/ACCURACY.md` §"Aim hit recovery", harness `cmd/qw-aim-eval`). `rl`/`gl`/`ng`/`sng` carry NO block — their fire→impact link needs the projectile-flight bracket the shots analyzer discards, and the impact-counting alternative reads 7.3 pp above the measured rl convention. Withheld with them, per-field and for stated reasons: the per-fire `hit` columns, the pellet split, direct/splash, the LG whiff classes and the enemy/team/self slices (see [WeaponAimRecon](#weaponaimrecon)). Pipeline: the `aim` node now binds `damage:final` instead of the raw `damage` artifact. Old-era goldens move by exactly the new fields. |
| v72 | **Archive-demo contracts: wall-clock anchors, match provenance, final scores, parse census, backpack reconstruction.** `streams.global` gains the wall-clock anchors (`demoStartUnixMs` and the match-start anchor echoed on `/overview` timing), derived from the wire date markers a recorder writes, with monotone floors and honest cross-checks — plus `timelineAnalysis.demoMarkers` for player-inserted bookmarks. `match` gains `mode` and `sources`, naming what each match-level value was decided from rather than presenting a merged answer. `metadata` gains `finalScores` (KTX's `//finalscores` end-of-match scoreline: date, mode, map, both team names and totals) and `matchSettings.fairpacks` (the `Fairpacks setting:` countdown broadcast, `ktx/src/match.c:2086-2107`). New top-level `parseWarnings` — the reader's per-run parse census, published on every run instead of being dropped. `streams.players[].aw` publishes `STAT_ACTIVEWEAPON`, the **wielded** weapon bit (opt-in field code `aw` on `/buckets`, `/stream-slice`, `/state-at`); a different question from the `rl`/`lg`/… inventory intervals. `backpacks[]` gains `source` (`ktx` \| `reconstructed`): the hint path stamps `ktx`, and a new post-processor fills the section on demos older than the `//ktx drop` hint by replaying `DropBackpack`'s own rule over `aw` at each in-match death. The two provenances are never mixed in one demo, and a reconstructed row's PICKUP side rides the drop row instead of `weaponPickups`: new `backpacks[].fate` (`picked` \| `expired` \| `unobserved`) with `picker`, `pickerTeam` and `pickupTime`, plus an `entNum` naming the bound backpack-model entity. The linkage node binds each reconstructed drop to the pack that appears at its time and place, follows that pack's origin updates to where it settled, and reads the disappearance as the server would — the bounding-box overlap that runs `BackpackTouch`. New parser events feed it: `ItemStateEvent` now carries the entity origin at each visibility transition, and `ItemMoveEvent` reports a visible item entity's origin changes (map items never move; a tossed backpack does). A `ktx` row carries `fate` too, but only ever `expired` and only from a wire hint of its own: KTX's third backpack directive `//ktx expire <ent>` (`ktx/src/g_spawn.c:196-210`, new `BackpackExpireHintEvent`) announces a pack `SUB_Remove` took untaken, which the `weaponPickups` join cannot state — an absent `fate` on a `ktx` row means "ask `weaponPickups`", never "nobody took it". Measured with every hint withheld on 223 demos: 100.00% precision / 96.13% recall on picked-vs-not, 99.98% of named pickers correct, and `expired` 100.00% precision / 100.00% recall against the packs `//ktx expire` names. Both provenances now publish the pack's own origin — the victim's position less KTX's 24-unit drop offset (`items.c:2703-2704`) — so backpack `origin` z values move by −24 against v71. |
| v71 | **Reconstructed damage for pre-instrumentation demos + damage provenance.** `damage` gains `source` (`ktx` \| `reconstructed`): the KTX analyzer stamps `ktx` on every wire-decoded section (a stored-Result change — goldens move), and the new `damage-recon` post-processor (package `mvd-analytics/damagerecon`) fills the section on demos whose wire never carried `mvdhidden_dmgdone` (~45% of the archive — `/damage` and the damage-shaped views 422'd there before). The reconstruction reads the health/armor change streams (the observed delta IS the bounded value), LG beams, projectile entity flights, fire sounds, position/velocity tracks and the frag log; both families, same shapes, same match window; per-player match totals validated at ~1% median error against KTX ground truth on modern demos (`damagerecon/ACCURACY.md`). Requires the spatial shot streams (mvd-api/WASM always; CLI `-include projectiles,beams`); never overwrites a measured section; stands down on `skipped:*` modes. `playerStats` binds the `damage:final` artifact, so its damage family now exists on old demos too, marked `src: "reconstructed"` (aim and airgibs deliberately keep wire-measured damage only). On reconstructed sections the victim-weapon fields are withheld when the recording froze its weapon bits (see the damage section notes). The `shots` stream gains the axe: `weapons/ax1.wav` (one swing sound per attack) maps to `weapon: "axe"`, with damage linking at the swing's real +200ms traceline delay — new rows in the shot stream, `byPlayer`, the reconciliation and the aim weapon counters. New `streams.pointEffects` (rides the shot-streams gate): every point-effect temp entity as columns t/ty/c/x/y/z — TE_BLOOD (per-volley hitscan hit telemetry with pellet count), TE_LIGHTNINGBLOOD (LG hit), TE_EXPLOSION (exact detonation point), TE_GUNSHOT (miss pattern) and the rest, the wire evidence the reconstruction consumes. `aim` gains a required top-level `hitsMeasured` flag and `weapons[].hits` becomes omitempty: on reconstructed/absent damage every hit-derived counter (hits, pellet full/partial/miss, direct/splash, the LG whiff classes, the sample `hit` columns) is withheld rather than fabricated as zero. `boundedMode` gains five skip reasons (`skipped:ca/wipeout/ra/lgc/race`). |
| v70 | **`/overview` becomes a capability manifest instead of a highlights reel.** The stored `Result` gains **no field** — this is an mvd-api response-shape bump, and the first BREAKING one in a while: fields are REMOVED. Gone are `topKills`, `topStreaks` and `topPowerups`, measured across the corpus at 78-88% of the response (`topKills` alone 62-77%), every one of them a copy of a dedicated endpoint — `/top-kills` at its own defaults, and `/lives` + `/events?type=streak,powerup` field for field. Gone too is `hasRegionControl`, folded into the new block. Added is **`available`**, one flag per detailed view, each mirroring the predicate behind that view's 422, so a `false` is exactly the 422 the call would have returned: `demoInfo`, `metadata`, `frags`, `damage`, `shots`, `aim`, `locGraph`, `opening`, `playerStats`, `regionControl`, plus the three a consumer could not previously infer AT ALL — **`height`**, **`liquid`** and **`los`** — which turn on which map BSPs the SERVER was provisioned with rather than on what the demo recorded, so the same demo answers differently on two deployments. Like every other flag in the block these report **measured, not non-zero**: a gate that opens fills its column for every position sample, so a map with no water yields an all-zero `lq` and `liquid` stays **true** — a measured *dry*, distinguishable from the unprovisioned `false` where the question is simply unanswerable. `height` and `liquid` ride separate gates (collision hull vs vis BSP) and can disagree. There is deliberately no `pvs` flag: PVS and LOS come off one pass behind one BSP gate (PVS ⊇ LOS by construction), so two flags could never disagree. `los` is the one PREDICTION in the block — the pass is heavy and lazy, so it reports the cheap half of the gate (streams, 2+ players, a provisioned BSP) and a provisioned-but-unvised BSP still 422s. A drift test pins the manifest to the 422 table, which is exactly what the removed ad-hoc `has*` fields never had and why they went stale. |
| v69 | **The victim-weapon axis: `byEnemyWeapon` on kills and damage.** Every per-weapon figure in `playerStats` was keyed on the ATTACKER's weapon; this bump adds the complement — the same kills and the same damage split by what the **victim** was holding when it landed, the weapon-denial question. `score.byEnemyWeapon` partitions `score.kills`, `damage.byEnemyWeapon` partitions `damage.given`, and both use one exclusive vocabulary: `both` / `rl` / `lg` / `mid` / `sg` (plus `unknown` on the kill side, for a victim with no stream). **`both` is the trap and the point**: "enemy RLs killed" is `rl + both`, never `rl` alone. Both are **DERIVED on every demo carrying streams**, never overlaid — KTX's own `ekills` counts the kill side inclusively (a victim holding RL+LG bumps both) and force-zeroes axe/sg plus every bucket on `deathmatch >= 4` / `k_instagib` (`ktx/src/stats_json.c:377-380`), while for damage the server keeps only the RL+LG-lumped `dmg_eweapon` scalar. Ours reproduces KTX exactly where KTX measures honestly — `rl + both == ekills.rl` on all 44 cached demos, every player, both weapons — and additionally covers telefrags/stomps and old demos with no demoinfo block. `damage.enemyWeapons` stays as the `lg + rl + both` summary it always was. Measuredness: the kill map rides the kill family (absent exactly when `kills` is), the damage map rides the damage STREAM (present exactly when `taken` is). The stored `Result` DOES change — this is computed in the analyzer, not folded in at read time — so the golden corpus moves. Web: the Summary tab's Basic Stats swaps `RL K` / `LG K` for `eRL` / `eLG`, the metric that was nowhere, since kills made WITH each weapon are already in the Weapon Stats tab. See [The victim-weapon axis](#the-victim-weapon-axis-byenemyweapon-schema-v69). | Also adds **`score.byWeaponVsEnemyWeapon`**, the joint distribution the two kill maps are marginals of — killer weapon → victim bucket → kills — because marginals cannot answer "how many of my LG kills were against enemies carrying an RL". Summing it over inner keys reproduces `byWeapon` and over outer keys `byEnemyWeapon`, an identity guaranteed by construction (the marginal is summed FROM the cross-tab) and asserted on every golden demo.
| v68 | **Gap-delimited windows: `mode=gap` on `/top-windows`.** The stored `Result` gains **no field** — like v67 the bump is for the observable API surface alone. `/top-windows` gains a second SEGMENTATION behind `mode` (default `fixed`, so a pre-v68 request answers identically apart from the additive `mode: "fixed"` echo every response now carries): under `mode=gap` a window is a maximal run of scoring events in which consecutive events are no more than `gapMs` apart, and its score is their sum — the stretch lasts as long as the player kept doing it rather than as long as a stopwatch says. `gapMs` is **required** there and has **no default**: measured over the 44-demo cache, per-player inter-kill gaps run p50 ≈ 11–12 s while inter-damage-event gaps run p50 ≈ 1.0–1.1 s, so no one value serves both (documented starting points ~10000 for the frag metrics, ~3000 for the damage and shot metrics), and each mode REJECTS the other's knob with a 400 rather than ignoring it. Additive on the envelope: **`mode`** (always present, so the segmentation is never inferred from which knob is present) and **`gapMs`** (gap responses only); **`windowMs` becomes fixed-only**, since a fixed length on a gap response would be a lie. Rows need no new field — `start`/`end` already describe variable-length spans, and a gap row's `end` is its last scoring event rather than `start + windowMs`. Clusters are disjoint per player by construction (no overlap suppression), signed metrics cluster on ALL their events (a death both extends a `netFrags` run and lowers its score), `score` still equals the same-named stat absent a `weapons=` filter, and a cluster MAY SPAN the player's own death — `/lives` stays the per-life view. See [TopWindows](#topwindows-viewtopwindows-rest-top-windows). |
| v67 | **Kill bursts (`/top-kills`) and the `top-` endpoint family.** The stored `Result` gains **no field** — the bump is for the observable API surface alone. Additive: the [`TopKills`](#kill-bursts-topkills--schema-v67) view + endpoint (the match's hardest kill bursts, ranked by burst damage; same-weapon runs, a CAPTURE `gapMs` narrowed client-side via `maxGapMs`, positional kills absent and dead-killer kills present), an `overview.topKills` field carrying 20 of those rows at the documented defaults, and an mvd-mcp `getTopKills` tool. Plus a deliberate **rename** taken while the consumer count was one, with no alias route: `/hot-windows` → `/top-windows`, `hot_windows_unavailable` → `top_windows_unavailable`, MCP `getHotWindows` → `getTopWindows`. Ranked highlight scans now carry an explicit `top-` prefix (they have a `limit`, a min-filter and a sort key); plain nouns stay reserved for exhaustive logs and partitions (`/frags`, `/damage`, `/lives`). No response shape changed beyond the paths. |
| v66 | **Userids are per session, not per wire slot** (a **correctness fix**: values move, no field is added, removed or retyped). `timelineAnalysis.playerUserIDs` and the `playerUserID` on `fragStreaks[]`, `powerupEvents[]`, `demoMarkers[]` and `airgibs[].attackerUserID`/`.victimUserID` used to carry the FIRST userid ever seen on a player's wire slot, latched for the whole demo. A userid names a connection, not a person (`SV_GenerateUserID`, `mvdsv/src/sv_main.c:538-556`, reissues them from a rotating pool), so every slot handover and every reconnect published somebody else's — or the player's own dead — id, and a consumer building the documented `?track=<userId>` deep link watched the wrong player. Two measured cases: hub 220637 served `(1)rusti (FU)` as **42**, the id of a spectator who had left after 26 s (now **43**), and 222649 served `bogojoker` as **12** sixteen minutes after he timed out and reconnected as **25** (now **25**), on the frag streak a viewer would click. Each id is now resolved from the *session* (one contiguous occupancy of a slot by one userid) that owned the slot at the value's own timestamp; where the reconnect unifier folded several sessions into one name, `playerUserIDs` publishes the **last session that had play** — a deliberate choice, being normally the id that is live at the end of the demo (the ranking is by last play evidence, so an exact tie in it resolves to the lower slot rather than to the surviving connection). Only demos with a mid-match handover or rejoin move (3 of 44 in the cached corpus); everything else is byte-identical. v66 also **exports the identity behind that resolution** (purely additive): `streams.players[]` and `playerStats.players[]` gain `identity` — the reconnect-unification key, equal for every row that is the same human — and `sessions[]`, the per-connection `{startMs, endMs, slot, userId, name}` windows it was folded from. Together they answer the two questions a name-keyed row cannot ("are these two rows one person" and "which userid do I track at time t"), which a consumer had been rebuilding with a fuzzy name matcher. The key is DEMO-LOCAL (derived from the first session's slot+userid) and must not be persisted or compared across demos. See [Player userids](#player-userids-schema-v66) and [Player identity and sessions](#player-identity-and-sessions-schema-v66). |
| v65 | **Interval segmentations: top windows and lives**, plus one stored measuredness field. (This row is written in the current names — the endpoint shipped as `/hot-windows` and was renamed in v67.) Additive: the stored `Result` gains exactly one field and nothing existing changes shape, name or meaning, so the bump is the cache-key tick every observable change earns rather than a migration. (0) **`frags.killsMeasured`** — the demo-global verdict on whether kill ATTRIBUTION was observable, decided by `analyzer.killsMeasurable` since v62 but never stored, so it was applied on `playerStats` and nowhere else. `false` means the frag log is empty on a demo where players demonstrably died (every obituary went unmatched), so `kills: 0` beside a measured deaths count is not a measurement; present the kill side as unmeasured, not zero. Demo-global and unchanged by any filter, and republished verbatim by the interval views as `measured.frags`. (1) **`/top-windows`** (`view.TopWindows`) serves each player's best fixed-length stretches, ranked by a caller-chosen **summable** metric — `frags`, `deaths`, `netFrags`, `damageGiven`, `damageTaken`, `netDamage`, `shots`, `hits`. Ratios are deliberately absent (they do not sum, so "the best window" is undefined for them). Windows are anchored at real event times rather than a grid, non-overlapping per player, and returned as one flat ranked list; `weapons=` scopes the **scoring** events only, so a window's `score` can be a subset of the same-named stat beside it. The scoring rule is echoed **once, on the envelope**, as `scoredBy {metric, weapons, dmg}` — there is no top-level `metric` field and no per-row copy. `limit=0` is a **400**, not "uncapped" (an omitted MCP integer arrives as 0); negative is uncapped, the default is 10, and anything above 200 is rejected rather than clamped. (2) **`/lives`** (`view.Lives`) serves one row per spawn-to-death run, segmented by the v64 `streams.players[].alive` intervals. A player's lives **partition** `[matchStart, matchEnd]` — each life is attributed every event from its own start to the start of the next — so a posthumous rocket counts for the life that fired it and per-life sums reconcile exactly with match totals, while `durationMs` stays **alive time** and each row publishes the wider window it counted over as **`attrStart`/`attrEnd`** (divide by `attrEnd − attrStart` for an exact rate; the pair also makes the tiling property measurable). `deaths` is therefore **not capped at 1** (a life also carries any death recorded in the dead gap that followed it, the KTX `dtTELE2` deflection: 12 rows with 2 and one with 3 across 11 558 corpus lives), and the new `endReason` (`death` / `matchEnd` / `leftGame`) exists because an absent `killedBy` used to conflate all three. (3) Both responses carry a **`measured {frags, damage, shots, locs, items, liveness}` block on the envelope**: every numeric stat is emitted including a measured zero, so measuredness is read from there and **never** from a field's absence — the v55/v63 discipline made structural. Two flags are not "the section is non-nil": `frags` is the stored `killsMeasured` verdict above, and `liveness` says whether the spawn-to-death segmentation was measurable at all — `streams.players[].alive` distinguishes `null` "not measurable" from `[]` "never alive", and `/lives` now **422s `lives_unavailable`** on the first rather than serving a `lives: []` that reads as "nobody ever lived". (3b) Both envelopes also echo the **damage family** they were computed in, as `dmg` + `boundedMode`, exactly as `/damage` does — the stats block reports damage under EVERY metric, so the REST `bounded` default, its raw fallback on a `skipped:*` demo and the explicit-`bounded` 422 all apply whatever the metric, and `metric=frags` rows would otherwise carry a `damageGiven` with no stated family. (4) Positional-kill (telefrag / stomp) value **folds** into the shared stats block's `damageGiven`/`damageTaken` exactly as `/damage` reports it, so lives reconcile against that endpoint, and it **scores** on the same terms under every metric it contributes to — which is what makes an unfiltered `/top-windows` `score` equal the same-named stat beside it exactly. `weapons=` is the only thing that makes the two diverge (and it selects positional kills too: `weapons=tele` scores telefrags alone). The one exclusion is `damageByWeapon`, matching `/damage`'s own `byWeapon`: a positional kill carries no wire weapon. (5) The frag and damage weapon vocabularies now accept **`water` and `drown` interchangeably** — one event the two logs spell differently; purely additive, no emitted token changed. (6) **Liveness and position staleness become reachable over the API** (view-layer only; the stored `Result` is untouched). `/stream-slice` players gain **`alive`** — the stored [`streams.players[].alive`](#playerstream) clamped to the window, carrying all three of its states through the clip (`null` not measurable, `[]` measured but never alive **in this window**, `[…]` the lives) — and `/state-at` rows gain **`alive`** as the same three states at an instant (`true`/`false`/`null`). Neither is `omitempty` and neither is field-gated: `null` is a state, so a key that could be omitted would also mean "not requested". Until now the schema told consumers to read the canonical liveness rather than re-derive it from `sp`/`d`, while no endpoint served it — the re-derivation it warned against is the strict `lastSpawn > lastDeath` latch this schema version's predecessors deleted from the pipeline. `/state-at` rows also gain **`posAgeMs`**, the age of the position sample `pos`/`view`/`hgt`/`lq`/`vel` were snapped to (signed: negative = the nearest sample is a later one). *Which* position is reported is unchanged — the unbounded carry-forward is deliberate — but the evidence age is now published, so a caller can apply the same `>= result.SampleStaleCapMs` (250 ms) rule the occupancy surfaces use and get consistent "where was X at t" answers across `/state-at`, `/region-control`, `/loc-graph` and `/loc-trails`. See [Interval segmentations](#interval-segmentations-topwindows-lives--schema-v65). |
| v64 | **Canonical liveness, and alive-gated exact loc/region occupancy.** A **correctness fix**: values move in `locGraph`, `timelineAnalysis.regionControl` and the `/loc-trails` view; no existing field changed shape or name, and no meaning is replaced — the occupancy fields are narrowed to the semantics they always intended ("time spent" → "**alive, observed** time spent"). (1) New `streams.players[].alive` — each player's **lives** as half-open `[s,e)` intervals, derived from the fused spawn/death markers (`DF_DEAD|DF_GIB`, `STAT_HEALTH`, and the obituary path — the last exists precisely because the first two miss deaths: a death+respawn entirely between two MVD frames, and the KTX `dtTELE2` pent deflection). It is the one canonical "was this player alive at t", and is **not** `omitempty` — `null` / `[]` / `[…]` are three distinct measuredness states, see [PlayerStream](#playerstream). (2) **`locGraph` node time is now an exact time-weighted integral** over the union of position-sample times and RL/LG/quad/pent/alive interval endpoints, replacing a forward difference clamped to 50 ms; posture is split at interval boundaries rather than snapped to sample instants, so a pickup landing between two samples divides the interval exactly. (3) **Both `locGraph` and `regionControl` now exclude dead players.** Dead players keep streaming position at full rate (`mvdsv/src/sv_demo.c:1481-1519` writes `svc_playerinfo` for every `cs_spawned` client) and on a gib the player entity *itself* is the bouncing head (`ktx/src/player.c:1070` `ThrowHead`), so both were crediting a corpse's travels as presence — and as **armed** presence, since `StatItems` weapon bits do not clear until respawn. `regionControl`'s comment claiming `Li==0` marked dead players was simply wrong. Expect region percentages and `byPlayer` ms to fall by roughly (deaths × death-to-respawn time) per player, and phantom `kind:"teleport"` edges thrown by bouncing gib heads to disappear from `locGraph.edges`. (4) **Sample-and-hold is bounded and both walks end at the player's end-of-track.** A position sample's evidence expires after `result.SampleStaleCapMs` (250 ms), so an unobserved hole is credited to nobody instead of holding the pre-gap loc/region across it — inert on server recordings (worst golden-corpus gap 74 ms), decisive on POV ones, where the non-recorder gaps run to tens of seconds. Presence then stops at `result.TrackHoldEnd`: the player's **last position sample held for one measured cadence (the track's median gap), itself capped at 250 ms** — not at the sample instant, and not at match end, so an early quitter no longer holds their final loc/region through to the final whistle. The same two bounds apply to `/loc-trails` residences and to `regionControl.bucketStates`; `locGraph`'s **edges** are stale-bounded too (no transition is recorded across a gap > 250 ms, which previously minted a `kind:"normal"` adjacency across a PVS hole). (5) The loc-graph **teleport threshold is scaled by the real inter-sample delta** instead of an assumed 50 ms — the MVD sample rate is server-configured (`sv_demofps`, default 30; `mvdsv/src/sv_send.c:1339-1346`) and measures bimodal across the golden corpus, ~13–16 ms on servers at full tick and ~34–39 ms on servers at the default. (6) Follow-up fixes inside the same version: `alive` now takes **spawn/death markers as presence evidence** when clipping (an obituary is broadcast to every recorder while `svc_playerinfo` is not, so a POV recording whose track stops no longer puts a later death outside every life and makes the player look as if they left the game); `/loc-trails`' `minDwellMs` fold no longer re-merges across the gaps the alive gate cut; and `alive`'s `null`-vs-`[]` distinction now survives mvd-api's gob cache, so a cache hit and a cold parse agree. Sibling to the v59 region-stats fix, which removed the same grid-vs-integral defect from region control and concluded it existed nowhere else; loc-graph was missed. |
| v63 | **Per-weapon team and self damage splits** (additive; no existing field changed shape or meaning). `damage.byPlayer.<p>.byWeaponTeam` / `.byWeaponSelf` — in the raw family and the `bounded` nest alike — and `playerStats.players[].damage.byWeaponTeam` / `.byWeaponSelf` split `givenTeam` and `givenSelf` by the ATTACKER's weapon, exactly as `byWeapon` splits `given` (same keys; telefrags and stomps stay excluded from all three, folding into the totals only). The KTX overlays now stamp `byWeaponTeam` from `weapons[].damage.team`, which KTX has always written beside `.enemy` (`ktx/src/stats_json.c:208-212`) and nothing consumed: a bounded summary with `boundedSource: "ktx"`, or a `playerStats` damage family with `src: "ktx"`, previously served a reconstructed team split under a server-counter badge. `byWeaponSelf` has no KTX counterpart and stays derived. **Measuredness is family-level and documented, never inferred from `omitempty`**: `byWeapon` + `byWeaponTeam` are measured wherever the damage family is present; `byWeaponSelf` only where a damage stream was read, which a non-nil `damage.taken` says. Within a measured family an absent key means "dealt none with that weapon". `matrix`, the top-level `damage.byWeapon` and the `enemyVs*`/`ewep` buckets stay enemy-only. Web: the aim tab's per-weapon **Dmg** column now follows the Enemy/Team/Self victim filter, and its All mode renders a `≥`-prefixed lower bound when a split is unmeasured. See [PlayerDamage](#playerdamage) and [PlayerStatsResult](#playerstatsresult-playerstats). |
| v62 | **`playerStats` learns to say "not measured"** (the v61 section is amended before it ships) **and `match.map` becomes the canonical shortname.** `match.map` previously carried whatever the `svc_serverdata` level name cleaned down to — the pretty title (`"Castle of the Damned"`) on most id maps. It is now always the SHORT name (`e1m2`), resolved like `EffectiveMap` (demoinfo map, else the serverinfo `map` key, and only then the title), so the one map identity holds across `match`, `demoInfo`, `metadata.serverInfo`, `searchGames` and every geometry file key. The title moves to the additive, **display-only** `match.mapTitle` (omitted when `svc_serverdata` named no level). `/overview` is unaffected in shape — its `map`/`mapTitle` split already reported these values, and `mapTitle` now reads the dedicated field. The `playerStats` half, six changes, all of them replacing a confident zero with an absence: (1) the **kill side of `score`** — `kills`, `suicides`, `teamKills`, `byWeapon`, `efficiency` — is now **optional** and omitted together on a demo whose obituary-derived frag log measured nothing while players demonstrably died (`4on4_l_vs_la[e1m2]`: 230 team frags, 121 deaths, 0 kills, 0.0% efficiency, indistinguishable from a genuinely awful team). `frags` and `deaths` are measured on every demo and stay. Render `-`, not `0`. (2) `hold.armor` is **omitted entirely** when the armor stream carries no sample at all, instead of reporting the alive-time complement of nothing as a full-match `none` (`dag_caps_e1m2`, a POV recording: 7 of 8 rows claimed 100% no-armor beside their own armor pickups). A player who genuinely never held armor still reports `none == aliveMs`. (3) `damage.src` becomes **three-valued** with `"derived:unbounded"`, marking the `k_midair` / `k_instagib` / `k_dmgfrags` demos where no bounded reconstruction exists and the figures are raw wire damage including overkill (+38-44% on a normal 4on4, an order of magnitude on instagib). (4) `damage` is now **emitted, zeroed**, for a player who dealt and took nothing on a demo that carries the damage stream — an observed zero, not an unmeasurable one. (5) **team rows carry `accuracy`**, summed per weapon over members, with `hits` absent unless every contributing member measured it and `real`/`virtual` not aggregated. (6) `sources` is **computed from the rows being served, after filtering** (it previously read `ktx` if any row matched, and was copied verbatim into filtered responses) and gains `"mixed"` as a phantom-roster canary. Type changes on the same fields: `ping` `int` -> `*int`, `members` `int` -> `*int` (so a team row always publishes it, including 0), `efficiency` `Share` -> `*Share`. Also outside the section: `frags.byWeapon` and `frags.byPlayer[].byWeapon` now move with the Finalize teamkill re-classification, so `sum(byWeapon) == kills` holds. See [PlayerStatsResult](#playerstatsresult-playerstats). |
| v61 | **New `playerStats` section** (additive — no existing field added, removed or retyped). One row per player and per team, present on **every** demo, carrying the join consumers previously re-implemented: the frag-log-corrected scoreboard, the damage family, pickup tallies and the KTX-only identity fields, plus the family neither source carries — **possession time** (time holding each weapon, each armor type, and **no armor**, as exact integrals over the native-rate streams with explicit `matchMs` / `presentMs` / `aliveMs` denominators). Every stat family carries `src` (`"derived"` \| `"ktx"`) with a top-level `sources` roll-up; the stored artifact is always fully derived and `view.PlayerStats` applies the KTX overlay at read time, the same pattern as `damage.boundedSource`. Exposed as the `player-stats` artifact (REST `/v1/demos/{id}/player-stats`, MCP `getArtifact`). `demoInfo` is unchanged — still the verbatim KTX pass-through this section is diffable against. See [PlayerStatsResult](#playerstatsresult-playerstats). |
| v60 | **Match scoreboard built per slot occupancy** (values only — no field added, removed or retyped). `match.players` and `match.teams` are now keyed on wire-slot *occupancies* instead of on each slot's final occupant, and participation is decided by evidence of play inside the match window (a spawn, a death, a position sample, or a frag value that changed) instead of end-of-demo `spectator` / empty-team state. What moves: (1) a participant who goes spectator *after* the match keeps their row and their score (hub 212535 `wd.dilbert` 0 -> 21, team `pys` 50 -> 71); (2) a player who leaves mid-match keeps the score the server announced in its `"<name> left the game with N frags"` broadcast instead of the zero `SV_DropClient` writes (`4on4_l_vs_la[e1m2]`: `shiva` 26, `DARKLORD` 21, team totals now reconcile exactly with the serverinfo `score` key); (3) FFA rosters survive — an empty team is no longer read as a spectator marker; (4) a connection the server refused (match locked) no longer inherits the departed player's scoreboard row, so phantom one-person teams disappear. Also value-only in `streams.players[]`: per-slot item state is reset at an occupancy handover, so the `rl`/`lg`/`gl`/`ssg`/`sng`/`quad`/`pent`/`ring` interval lists no longer carry a departing player's inventory into the next occupant, and a departing player's own open intervals close when they left rather than at match end. The same split reaches `locGraph.edges[]`: an occupancy boundary breaks the position track, so a player's exit from one slot is no longer bridged to his re-entry on another and the phantom `teleport` edge that bridging invented disappears (`4on4_jah_ahoy_170526_defer_reconnect`: `Quad.low -> Pent.MH`, `rusti`, total 1). |
| v59 | **Exact time-weighted region-control stats.** `regionControl.stats` (percentages + `byPlayer`) are now the exact time-weighted integral over the native position sample times — the walk unions every player's Position sample times with their RL/LG armed-interval boundaries, classifies each constant-state interval once and accumulates its real duration — replacing the v57-era fixed native 50ms stats grid. Two consequences: the state percentages shift slightly (de-quantized — no longer snapped to 50ms quanta), and `RegionStats.byPlayer.armed`/`unarmed` change **units** from 50ms-bucket counts to integer **milliseconds** of presence (the Go field type stays `int`; the value is ~50× larger). `bucketStates` is unchanged — it stays the display point-sample grid at the caller's `windowMs`, the only thing `windowMs` now affects. Because stats no longer come from a 50ms grid, the `windowMs=50` output is no longer byte-identical to prior versions. **Also (view-layer, additive):** (1) `/overview` `map` is now the canonical map **shortname** (`EffectiveMap`: demoinfo → serverinfo fallback — the same value searchGames rows and `/metadata` serverinfo carry, so a consumer can join on it), where it previously echoed the BSP's pretty title; the pretty title moves to an additive `mapTitle` (omitted when identical to `map`). Stored `Result.Match.Map` is unchanged. (2) `/region-control` gains a `regions` query param — `full` (default; the polygon `points` included), `summary` (`points` stripped, name/locs/centroids kept), `none` (regions list omitted) — trimming the ~6KB polygon payload for stats-only consumers; the MCP `getRegionControl` defaults to `summary`. |
| v58 | **Demo markers** (additive). New `timelineAnalysis.demoMarkers[]` (`[]DemoMarkerEvent`) surfaces the bookmarks players insert in-game with KTX `/demomark`. Each carries match-relative `time` (negative for a warmup mark — surfaced un-gated), the marking player's `playerName`/`playerSlot`/`playerUserID`/`team` (resolved from the demo block's target slot, the only attribution channel; `playerSlot: -1` with empty identity when the block was not slot-addressed), a `spectator` flag (KTX accepts `/demomark` from spectators; omitted when false), and an optional argument-tail `label` (e.g. HoonyMode `"0 round-07"`). A matching `demomark` event type is added to `/events` **and to its default type set**, so a caller that omits `types` begins seeing the new rows. No existing field changed. |
| v57 | **Pure-ms time model + bound renames.** Every time value in the API is now int32 ms — inputs and outputs, REST and MCP alike. The six v56 seconds surfaces (`/events`, `/buckets?layout=row`, `/state-at`, `/stream-slice` envelope, `/loc-trails`, `/items` summary) flip to int32 ms; the view layer does no float time math. `LocGraphResult` node weights (`LocNode`/`LocWeights` `total`/`byPlayer`/`byTeam`) also flip from float64 seconds to int32 ms (a post-review fix; edge transition-count weights stay int), and `/loc-graph` now echoes `timeUnit:"ms"`. `view.UnitSec` is deleted and `timeUnit` becomes a constant `"ms"` echo. Time-valued query params `from`/`to`/`time` on demo endpoints become **integer ms** (a non-integer value 400s `invalid_param` with an `(integer milliseconds)` hint). Per-item time key follows the dense/sparse rule: event-scaled sparse surfaces keep the descriptive `time` (the v55 spelling — frags/damage/shots/chat/backpacks/weapon-pickups/opening/timeline entries are unchanged from v55; the former v56 `t` on the flipped view surfaces events row / buckets row / state-at envelope / items `firstTake` reverts to `time`), while sample-rate-scaled dense arrays keep the terse `t` (stream tracks, aim samples, columnar grid, projectile/beam columns) — both int32 ms. Other key renames: stream-slice envelope `startTime`/`endTime`→`start`/`end`; columnar buckets `startMs`→`start`; `LosTrack` `o`/`iv`→`other`/`intervals`; `MessagesResult` array key `events`→`messages` (so `/artifacts/messages` is `{messages:{messages:[…]}}`). Governed top-level arrays that were nullable are now never null (`/events`, `/stream-slice` `players`, `/loc-trails` `players`). Deliberate non-renames: `Interval` keeps terse `s`/`e` (per-row keys stay terse); projectile/beam `s*`/`e*` column-family prefixes; `windowMs`/`partialLastMs` durations. `/demoinfo` stays the KTX-native seconds island; search `from`/`to` stay calendar dates. |
| v56 | **`timeUnit` echo on the REST/MCP transport** (additive; stored structs unchanged — still int32 ms). `timeUnit` is the unit of every time value in a response: **every `/v1/demos/{id}/*` response that carries match-position time values echoes a top-level `timeUnit`, except `/demoinfo` (mixed KTX-native units) and `/artifacts/{name}` (raw stored bytes); responses with no match-position time — `/loc-table`, `/loc-graph`, `/metadata` — carry no echo**. The value is FIXED per endpoint — no unit selection: `ms` for frags/damage/shots/chat/airgibs/backpacks/weapon-pickups/items-timeline/overview/aim/buckets-column/region-control/los/streams-projectiles/streams-beams/streams-nails, `s` for the derived views events/state-at/stream-slice-envelope/loc-trails/buckets-row/items-summary. Field-name polarity (exception-free): **`t` is int32 ms, `time` is float seconds** — always, on every endpoint. The stored ms event lists (frags/damage/shots/chat/backpacks/weapon-pickups/airgibs/timeline events) carry their timestamp under `t`; the float-seconds view surfaces (events/state-at/buckets-row/items-summary `firstTake`) carry it under `time`; loc-trails residences use `start`/`end`. The dense per-sample arrays (`/stream-slice` embedded tracks, `/aim` samples, columnar `/buckets` axis) already used `t`-in-ms and conform natively. The four formerly bare-array endpoints wrap their array to carry the echo: `/chat`→`{timeUnit,messages}`, `/airgibs`→`{timeUnit,airgibs}`, `/backpacks`→`{timeUnit,backpacks}`, `/weapon-pickups`→`{timeUnit,pickups}` (the one non-additive shape change). See the §"Transport surface" note above and [mvd-api/API.md §2.1](../mvd-api/API.md). |
| v55 | Bounded damage becomes **death-value-derived and the default**. The v54 shadow-health cap is replaced: a survived hit is bounded == raw by identity, a killing hit's overkill comes from the end-of-frame death broadcast (bounded = raw + deathValue; corpus reconciliation tightens ~2.5x, max +-16/player on given/taken). Fallback to the approximate shadow cap only for the -99 corpse clamp and respawn-masked deaths; same-frame multi-hit deaths cascade the overkill from the last hit backward. The REST/MCP `dmg` **default flips to `bounded`** for summaries AND the full log (`raw`/`both` opt-in; a *defaulted* request on a `skipped:*` demo falls back to raw, only an explicit `dmg=bounded` 422s). Unfiltered bounded summaries substitute KTX's exact scoreboard figures (given/givenTeam/givenSelf/ewep/byWeapon-enemy; `taken` and the `enemyVs*` buckets stay reconstructed) with provenance in the new `damage.boundedSource` (`ktx` / `reconstructed`). |
| v54 | The **bounded damage family** (additive). The wire carries only KTX's unbound damage; the scoreboard's bounded `dmg_dealt` (armor absorbed + health damage capped to remaining health) is now reconstructed per hit from tracked victim vitals: `damage.events[].bounded` (absent = equal to `damage`; `0` is a real nullified-hit value), `damage.byPlayer.<p>.bounded` (a nested `PlayerDamage`), `damage.scoreboard` deltas gain a `bounded` nest incl. `streamTeam`/`scoreTeam`, plus the `dmg` family echo and `boundedMode` (`skipped:*` on midair/instagib/dmgfrags demos — no bounded fields there). Telefrags **and stomps** now fold their bounded damage into `given`/`givenTeam`/`taken` in **both** families, matching KTX's own accumulation (telefrag: armor+health, the wire 9999 is a sentinel; stomp: the honest ~10 HP wire value); `telefrags[]`/`stomps[]` entries carry the per-kill `bounded` value. `byWeapon`/`matrix`/`ewep`/`totalDamage` still exclude positional kills (KTX `wpNONE` parity). |
| v53 | Columnar buckets become **loc-self-contained**; view shape only, no stored-field change (bumped so the immutable schemaVersion-keyed ETags stop revalidating pre-legend bodies). The `/buckets` `layout=column` envelope gains `locTable` — the demo's interned loc-name legend, present iff an `li` column is in the output. Columnar keeps the compact raw index (row mode keeps resolving names per bucket); consumers decode locally instead of a `/loc-table` round trip. |
| v52 | No-match-start demos are **flagged, not coerced**: `streams.global` gains `timeBase: "demo"` (omitted normally) when no match start was detected — on such demos the rebase never ran, so every timestamp in the Result is on the raw demo clock; previously indistinguishable from a match-rebased result. A matching notice is appended to `errors[]` (surfaces via `/overview`). |
| v51 | The match opening becomes first-class. `streams.players[].sp` gains the **match-start spawn** (KTX respawns everyone at countdown end, but a player alive through the countdown never crosses dead→alive on the wire, so the timeline synthesizes `t=0`). Adds `Result.opening` (`OpeningResult`, the `opening` artifact): per-player match-start spawn loc + the first in-match take of each contested spawner. The events *view* gains the default `pickup` type (identity-rich takes joined from `items[].phases` + `weaponPickups`) and spawn events carry `detail{loc}`. |
| v50 | `damage.events` is now **match-gated at the source**; no field-shape change. The per-hit `events` log previously carried out-of-match (warmup / post-match) hits while the aggregates gated them out; the analyzer now drops out-of-match hits before appending, so `events` and the aggregates are folds of the same in-match hit set. `damage.events` arrays shrink by the dropped hits. This lets the `/damage` filter's all-players recompute reproduce the stored aggregates exactly, removes the aim `[0,matchEnd]` self-window added in v49 (aim reads exactly-in-match damage), and fixes a latent bug where `timelineAnalysis.airgibs` counted warmup / post-match rocket airgibs (it iterated `events` with no gate). The `shots` stream is now match-gated too (warmup fires dropped at the source; the `Shot.warmup` field is removed since no out-of-match shot survives), and `damage.telefrags`/`damage.stomps` arrays are match-gated with team telefrags/stomps no longer credited to the attacker counter. |
| v49 | Aim/shots correctness fixes; no field-shape change. (1) The `aim.players[].weapons` rl/gl `direct`/`splash`/`missed` block appears on every default parse: it was gated on the opt-in `streams.projectiles` emission while the projectile linking it needs runs on every parse — it now gates on linking evidence (any linked rl/gl fire). (2) The damage records feeding aim's pellet and direct splits are windowed to match time `[0, matchEnd]`, so warmup and post-match damage no longer inflates `direct` (and deflates `splash`). (3) In a 1v1 where both players share a non-empty colour team, `damage.events[].isTeam` is no longer true for hits on the opponent: `DamageAnalyzer` classifies duel hits as enemy at birth, so the events, `given`/`givenTeam`, the matrix, `victimWep` and the EWep buckets agree with the duel-normalized `shots` victim kinds (previously airgibs came out empty and the aim enemy splits zero on such demos). (4) Shots identity resolution uses the canonical `ResolveSlotAt` chain, backfilling an empty team from the demoinfo name table (parity with damage/frags). |
| v48 | Correctness fixes to already-emitted values; no field-shape change. (1) `timelineAnalysis.killEvents` is now on the match-relative clock and carries duel team labels, exactly like the sibling `deathEvents`/`fragEvents` (both post-processors previously skipped it): each kill `time` was ~`demoOffset` ms late and, in 1v1s, `team` was a raw colour tag instead of the player name. (2) Match-timing detection ignores `PRINT_CHAT` (level 3), so a pre-match "go!" or a mid-match "gg game over" chat line can no longer flip the match window (`streams.global.matchStart`/`matchEnd`) or freeze streams; the obituary parser likewise rejects level-3 prints. (3) The CRMod "eats 2 scoops of" super-shotgun obituary is reachable again — those kills were mislabeled `gl` with a phantom "2 scoops of X" killer, now `ssg` with the real killer. (4) `match.players`/`match.teams` no longer drop players who finished on exactly 0 frags (surface-authoritative-data), and duel detection trusts `demoInfo.players` as authoritative so a 2on2 in which two players end on 0 frags is no longer misclassified as a duel and team-renamed; a paired reader fix parses the server-set `*spectator` userinfo star key (and resets the flag on every full userinfo update, ezquake-style) so actual spectators don't leak into `match.players` in place of the removed filter. (5) Powerup interval end times use the same effective match end as the weapon intervals on demos cut before intermission. |
| v47 | LG miss reclassification on `WeaponAim`. A miss now only counts as `blocked` / `outOfRange` when the shooter was **on target**: `blocked` = the beam stopped short of its ~600 qu max range on geometry and its extension to full range crosses a live enemy's collision hull (a would-be hit denied by the obstruction); `outOfRange` = the beam ran its full length and its extension to infinity crosses a live enemy's hull (denied by reach). Previously every short-of-max-range beam whose endpoint wasn't near an enemy was blocked (even fired into a wall with nobody behind) and every full-length beam was out of range. `nearMiss` is **removed**: with blocked detection on the beam line, the near/wide distinction among plain aim errors carried no signal — all remaining whiffs land in the lg `miss` bucket (shares the field with the sg/ssg per-pellet miss). LG invariant becomes `hits + blocked + miss + outOfRange + unresolved == shots`. Only the opt-in beam-enriched parse is affected (the split needs `streams.beams`); expect `blocked`/`outOfRange` to drop sharply and `miss` to absorb them. |
| v46 | Weapon-stay pickup recovery: in deathmatch 2/3/5 and coop, world weapon pickups are synthesized from `STAT_ITEMS` weapon-bit 0→1 transitions (KTX never emits `//ktx took` there). `WeaponPickup` gains `inferred`; the `source` vocabulary gains `unknown`. Weapon-stay item phases use the zero-length unavailability convention (`takenAt == respawnAt`). Duel team normalization now also rewrites items/pickup/backpack/shots/airgib team strings and folds duel `team` victim-kinds into `enemy`. Item pickup attribution samples per-frame positions at the touch instant under a shared 128 qu touch gate. |
| v45 | Victim-class classification on the shots/aim pipeline, mirroring the damage layer's `isSelf`/`isTeam` semantics. `Shot` gains `victimKinds` (parallel to `victims`: `enemy`/`team`/`self`, omitted when all-enemy); `WeaponShots` gains `enemyHits`/`teamHits`/`selfHits` (overlapping buckets — a multi-victim fire counts in each bucket it has a victim in); `CrosshairSamples` and `LGRampSamples` gain a `team` column; `WeaponAim` gains `enemy`/`team`/`self` `WeaponAimSplit` hit-counter slices (emitted only when they differ from the top-level counters — see WeaponAimSplit). All additive (`omitempty`); `hits`/`accuracy` stay all-victims for KTX parity. |
| v44 | Aim crosshair samples of **hit** shots attribute to the server-confirmed victim (nearest by crosshair error when a pellet fire hit several), bypassing the v43 liveness gate and the enemy filter — the killing blow lands in the frame the victim dies, so the gate read the victim as dead and handed the sample to the nearest *other* live enemy. No field changes; hit samples' `tgt`/error columns shift, and duels gain one sample per hitscan kill. |
| v43 | Aim target attribution gates candidates on being **alive at fire time** (spawn/death streams) — dead players keep streaming position samples (the death-anim body), so a corpse could win nearest-crosshair attribution. No field changes; crosshair sample counts/targets shift on team demos. |
| v42 | `Shot` gains `warmup`: true for fires outside the match (prewar/warmup/post-match). The stream keeps them; `byPlayer` and the aim analysis exclude them. Additive (`omitempty`). |
| v41 | New top-level `Aim` (`aim`): per-player aim analysis derived as a post-process from `Shots` + `Streams` + `Damage` + LG beams — normalized crosshair-error samples (hitscan), LG ramp-onto-target, rocket direct/splash, LG reach/whiff. Additive (`omitempty`). |
| v40 | `Streams` gains opt-in spatial weapon-fire streams for the map view: `streams.projectiles` (`ProjectileStreams` — every rocket/grenade flight as a spawn→despawn segment + times), `streams.beams` (`BeamStreams` — every LG `TE_LIGHTNING2` bolt as a muzzle→impact segment + time), and `streams.nails` (`ProjectileStreams` — ng/sng spike flights; a separate `-include nails` request that also enables ng/sng → damage linking). All columnar, built only when requested (`qw-analyze -include projectiles,beams,nails`; the WASM map build builds projectiles/beams) so the default output and golden corpus stay lean. Additive (`omitempty`); absent from the default parse. |
| v39 | New top-level `Shots` (`shots`): a per-shot weapon-fire stream — who fired what, at what match-relative ms — derived from `svc_sound` `CHAN_WEAPON` fire sounds (SG/SSG/RL/GL/NG/SNG; the sound carries the firing entity) and `TE_LIGHTNING2` beams for LG (one beam per fire tick, carrying the firing entity — exact, beating ammo deltas). Hitscan fires (sg/ssg/lg) link to their same-frame `mvdhidden_dmgdone` damage; rocket/grenade fires (rl/gl) link via entity flight tracking (the projectile entity brackets `spawn → despawn`, so a fire matches its launch frame by muzzle and its impact damage by attacker + despawn frame — disambiguating overlapping flights). Sets `hit`/`victims`, adds `byPlayer` match-time per-weapon counts + accuracy, and a `reconciliation` cross-check whose `streamAttacks` matches KTX `acc.attacks` exactly across the corpus (rl/gl connect-counts match KTX `real` hits to within one). Additive (`omitempty`); present whenever any fire is detected, including non-KTX demos (no damage stream → no links). |
| v38 | `PlayerStream` gains `pvs[]` (`LosTrack`): per-opponent potentially-visible-set intervals, populated alongside `los[]` by the same lazy `analyzer.ComputeLOS` pass under the same BSP gate. Reproduces the mvdsv per-client entity cull (`SV_PlayerVisibleToClient`): the looker's fat PVS (`CM_FatPVS` of `origin+view_ofs`) ∩ the opponent's entity leaf set (1-unit-expanded box, non-solid leaves), or always when it overflows `MAX_ENT_LEAFS` — i.e. whether a live server would have sent that opponent to the client (the recorded MVD stores every entity, `pvs = NULL`). This test also gates the LOS raycast, so **PVS ⊇ LOS** by construction. The gap (potentially visible, no clear ray) is an occlusion-tolerant proximity/awareness signal. Same `o`/`iv` shape, asymmetry, alive-gating; additive (`omitempty`); absent on BSP-less maps and on the default parse. Exposed by the same consumers as `los` (web overlay, `qw-analyze -include los`, mvd-api `/los`). |
| v37 | `PlayerStream` gains `los[]` (`LosTrack`): per-opponent line-of-sight as half-open `[s,e)` ms intervals during which the looker had a clear sightline (eye `origin+(0,0,22)` → any of the opponent's 8 bbox corners + midpoint), blocked by worldspawn solids or any active mover posed in the way. Asymmetric (`A→B` in A's stream, `B→A` in B's); `o` indexes `streams.players`. **Computed lazily** (`analyzer.ComputeLOS`) — absent from the default parse; populated on demand by the web LOS overlay, `qw-analyze -include los`, and mvd-api `/los`. Against the visibility BSP, so only on maps with a provisioned BSP (same gate as `pos.h`/`lq`). Additive (`omitempty`). View direction is not considered. |
| v36 | `MatchResult` drops the dead `startTime` / `endTime` fields. After the match-relative time normalization `startTime` was always 0 (already `omitempty`, so absent from JSON) and `endTime` always equalled `duration`; both duplicated `streams.global.matchStart` / `matchEnd`. The `endTime` key disappears from the `match` object — read `duration` for match length, or `streams.global` for the match window. Breaking removal (not additive). |
| v35 | `streams` gains `movers[]` (`MoverStream`): the pose timeline of every tracked brush-model entity (lift, door, plat, train). Each carries `ent` (entity number), `sub` (the `*N` brush-model index, matching the corpus `SubModelMesh` id), and index-aligned `t`/`x`/`y`/`z`/`vis` columns — the mover sits at `(x,y,z)[i]` at `t[i]` ms and is drawn when `vis[i]`. Origins are `float32` (exact ⅛-unit wire values). The first entry is clamped to `t = 0` carrying the match-start pose so a parked mover (only wire state predates the match) still has one. Additive (`omitempty`); absent when the demo has no movers. The same internal tracks already drive the v27 floor-height pass. |
| v34 | `timelineAnalysis.locationData` now carries **one `MapLocation` per loc name** — the medoid of that name's `.loc` corpus points — instead of every raw point. The corpus often repeats a name across several nearby points, which drew duplicate map labels; the medoid is the actual point minimizing summed distance to its same-name siblings (never an averaged mid-air centroid). `locGraph` node coordinates (resolved from this list by name) move to the medoid. Same field name and `MapLocation` shape; the list is just shorter. |
| v33 | `PositionTrack` `x` / `y` / `z`, `vx` / `vy` / `vz`, and `h` change from `int32` to **`float32`** — the pipeline stops truncating the wire-native sub-unit origin (mvd-reader decodes coordinates as float32; the wire carries eighth-unit fixed point, or true floats under the float-coords extension). v32 and earlier rounded each axis to whole units (losing up to ~1 unit) and derived velocity from those rounded positions; velocity is now sub-unit precise, so the old ±1-unit quantization noise is gone. Values are kept at **native float32 in memory**; only the JSON text is rounded — to 3 decimals, applied by `PositionTrack.MarshalJSON` (lossless for eighth-unit coordinates; it just sheds the float division/epsilon tail on derived velocity & height). The `PositionTrack.H` `NoFloor` sentinel changes from `-2147483648` (`math.MinInt32`, which a float32 cannot represent exactly and serializes as `-2147483600`) to **`-1000000000`** (`-1e9`, exact in float32 and float64). The `buckets` x/y/z/vx/vy/vz/hgt columns get the same 3-decimal rounding; the point-in-time `state-at` `pos`/`vel`/`hgt` and the `AirgibEvent` heights are float32 too but emitted at full precision (low volume). Time axes stay `int32` ms; view angles stay `int16` raw `angle16`; loc/liquid columns unchanged. JSON keys unchanged; values now carry fractional digits where the wire delivered sub-unit positions. |
| v32 | `PositionTrack` gains `vx` / `vy` / `vz` columns: the player's **velocity** per sample in Quake units/sec, derived from the position columns by a central-difference estimator (it does not differentiate across a respawn teleport, a map-teleporter relocation, or an abnormal time gap — those read ~0 instead of spiking). Additive (`omitempty`), populated whenever the track is (no BSP needed). New opt-in view-layer field code `vel` (vx/vy/vz) and CLI `-include velocity`. Expect ±1-unit quantization noise on the raw derivative (integer-rounded source positions); smooth client-side for a clean speed curve. |
| v31 | `PositionTrack` gains `vp` / `vya` columns: the player's **view direction** (pitch, yaw) per sample as the raw `angle16` state, kept losslessly after `svc_playerinfo` delta carry-forward (decode `deg = uint16(v) * 360/65536`; values `[0,360)`, pitch > 180° = looking up; roll not stored). Additive (`omitempty`), populated whenever the track is — no BSP needed (the angles ride the same `svc_playerinfo` samples as x/y/z). New opt-in view-layer field codes expose per-channel selection: `view` (vp/vya), `hgt` (h), `lq` (lq). **Clean break:** the `view`-API `pos` code now returns strictly x/y/z (+`li`); height/liquid no longer ride along it — request `hgt` / `lq`. CLI `-include` becomes column-aware (`positions` / `view` / `height` / `liquid`). |
| v30 | `timelineAnalysis.airgibs` is no longer capped at the top 20: every qualifying hit (direct enemy rocket, victim ≥ 96 units above the floor) is emitted, still ordered by `height` descending. The qualification threshold already bounds the list to a handful per match, and a cap keyed on floor height could drop the hits a consumer sorting by `heightAboveAttacker` cares about most. |
| v29 | `AirgibEvent` gains `heightAboveAttacker`: the victim's origin minus the shooter's at the hit (units; negative = victim below the shooter) — the vertical gap the rocket climbed, often the more impressive number for a highlight than the floor height. Computed from the two players' nearest position samples to the hit; `0`/absent when the shooter had no sample within the gap window. Ranking and the ≥ 96 qualification still use the floor height. Additive (`omitempty`). |
| v28 | `PositionTrack` gains an `lq` column: per-sample **liquid state**, packed `(type << 2) \| level` — level 1–3 (feet/waist/eyes submerged, mirroring the engine's `PM_CategorizePosition` probes at z−23 / z+4 / z+22 against the map's render BSP), type 1 water / 2 slime / 3 lava (water 5/6/7, slime 9/10/11, lava 13/14/15; `0` = dry). Decode with `lq & 3` (level) and `lq >> 2` (type). `h` interacts with liquids: a sample in liquid (level ≥ 1) reads `h = 0` by definition, and a dry sample airborne above liquid measures down to the **liquid surface** when it is the highest support beneath the player. Additive (`omitempty`); absent when no BSP is provisioned for the map. |
| v27 | `PositionTrack.H` now stands players on **moving brush-model entities** (lifts, doors, trains): the parser surfaces `"*N"` submodel entities as `MoverSpawn`/`MoverState` events and the floor trace runs over the worldspawn hull **plus** each mover's submodel clip hull posed at its demo-streamed origin for the sample's time (`mapclip.HeightAboveFloorBoxScene`) — the highest floor wins. A player riding the dm2 RA lift reads ~0 instead of the height to the shaft floor, which also removes the false airgib entries rocket hits on lift riders produced (dm2 `path.lift`/`Quad.button`). `NoFloor` narrows accordingly: "on a moving brush model" disappears as a cause, leaving void/pit, embedded and zero origins. Same shape and units; only values over movers change. |
| v26 | `PositionTrack.H` is now measured over the player's **bounding-box footprint** instead of the single origin column: the height is taken to the highest floor found under a 3×3 grid of columns sampled ±8 around the origin (`mapclip.HeightAboveFloorBox`) — an effective ~48-wide footprint on the already-±16-box-inflated hull. A player skimming a ledge / well rim — origin momentarily over the pit while the box overhangs the rim — now reads the near floor (small `h`) rather than plunging to the distant floor far below. Same shape and units; only values near ledges change, which also removes the bogus high airgibs those samples produced (e.g. anwalked RA's well rim logged a 553-unit airgib that was really a rim skim). |
| v25 | `TimelineAnalysis` gains `airgibs[]` (`AirgibEvent`): the top airborne rocket hits for Key Moments — each direct enemy rocket hit (splash excluded) whose victim was ≥ 96 units above the floor, annotated with attacker/victim (name, team, userid), hit time, victim loc and height, raw damage, and lethality (a matching rocket frag near the hit). Derived by a post-processor from `Damage.Events` + the streams' `PositionTrack.H` column + the frag log; capped at top 20 sorted by height descending. Additive (`omitempty`); empty when the map has no clip hull (no `H` column). |
| v24 | `PositionTrack` gains an `h` column: the player's height above the floor directly beneath them at each native-rate sample (feet above the nearest solid surface below), from a straight-down trace through the map's worldspawn player clip hull (parsed from BSP `CLIPNODES` at analyze time by the new `mvd-analytics/mapclip` package; BSPs come from the same best-effort source as the visibility-aware loc filter via the shared `mvd-analytics/mapbsp` loader). Reads ~0 grounded and grows during a jump / airborne hit (airgib); absolute floor is `z − 24 − h` if needed. Sentinel `-2147483648` (`result.NoFloor`) marks samples with no floor to measure from (void/pit, or a moving brush model such as the dm2 lift, which the worldspawn-only hull excludes). Additive (`omitempty`); absent when no BSP is provisioned for the map. |
| v20 | New `Damage` section: per-hit damage log + aggregates (attacker→victim `matrix`, per-weapon, given/taken, and the **EWep** victim-weapon buckets `enemyVsSg/Mid/Lg/Rl/Both` where `ewep=lg+rl+both`) reconstructed from the KTX `mvdhidden_dmgdone` stream, plus a `scoreboard` cross-check vs `demoInfo.players[].dmg`. Amounts are unbound (include overkill). **Positional kills** — telefrags (deathtype `tele`, the 9999 instakill sentinel) and stomps (deathtype `stomp`) — are excluded from all damage figures and surfaced separately as `damage.telefrags`/`damage.stomps` + `PlayerDamage.telefrags`/`.stomps` + the opt-in `telefrag`/`stomp` events. Also a Layer-1 change: world/environmental damage-taken (lava/fall/trigger) is now emitted with an `Attacker == -1` "world" sentinel rather than dropped. Additive (`omitempty`); absent when the demo lacks the KTX hidden-damage stream. |
| v19 | `MatchResult.PlayerStat` gains `kills`, `deaths` and `suicides` — the frag-log-corrected counts, making `match.players` a complete corrected scoreboard rather than just the net frag tally. They supersede the KTX demoinfo `stats`, which credit several self / positional deaths to the wrong entity: pentagram-deflect telefrags (`dtTELE2`) inflate the deflector's kills, and world-dealt suicides (fall / lava / squish / drown) bump the world entity's counter instead of the victim's (`ktx/src/client.c:4951`), so demoinfo undercounts suicides. `0` when the demo carried no frag log. Filled by the `scoreboardStatsPost` post-processor (kills/deaths from `Frags.ByPlayer` joined on the final display name; suicides counted from the `IsSuicide` frag entries). The API `/overview` player rows surface the same `kills`/`deaths`/`suicides`, so non-web consumers get the correction the web Summary already applied. Field additions only. |
| v18 | `TimelineAnalysis` gains `KillEvents`: a per-player enemy-kill stream (`{time, player, team}`) keyed on the killer, parallel to `DeathEvents`, from the canonical frag log filtered to real enemy kills (suicides/teamkills excluded). Cumulative `killEvents` per player reconciles with `frags.byPlayer[].kills` and the kills-based efficiency; the Timeline per-player drill-down plots `killEvents − deathEvents` as a windowed +/-. `team` is best-effort and, unlike `deathEvents`, ungated. Additive (`omitempty`). |
| v17 | Self-kill weapon labels in `Frags.Frags` are no longer flattened to `suicide`: only the `/kill` console command (KTX "X suicides", −2 frags) keeps weapon `suicide`; weapon self-detonations carry their real weapon (`rl`/`gl`/`lg`) with `isSuicide` set. `Frags.ByWeapon` is now enemy kills only (suicides/teamkills excluded). Recovered teamkills no longer carry a stale `isSuicide`. |
| v16 | `PlayerFrags` gains `teamkills` (KTX "tk"), and teamkills whose obituary names only one party re-enter `Frags.Frags` as complete killer↔victim pairs (killer-named recover the victim from the coincident `DeathEvent`; victim-named recover the killer via position co-location + the teamkiller's −1 frag-delta). Brings per-player teamkills to an exact match with KTX's `tk`. |
| v15 | `TimelineAnalysis` gains `DeathEvents`: a per-player death stream (`{time, player, team}`) parallel to `FragEvents`, from the authoritative protocol `DeathEvent` (every death counts once), for the Timeline per-player frags/deaths drill-down and KTX-style efficiency `frags / (frags + deaths)`. Additive (`omitempty`). |
| v14 | `MapEntities` gains **brush entities** — `teleportSrc` (`trigger_teleport`), `button` (`func_button`), `door` (`func_door`) — placed at their BSP submodel bbox centre with a `bounds` (trigger/door volume), plus the teleport source→destination link (`teleportSrc.target` == `teleportDst.targetName`). v13 carried point entities (items, spawns, teleport destinations) only. |
| v13 | New `MapEntities` section: the map's static designed layout (item spawns, player spawnpoints, teleport destinations/sources, buttons) with type + location, from the offline-generated mapents corpus (BSP entity lumps) keyed by map name. Additive (`omitempty`); absent when no corpus exists for the map. |
| v12 | `LocNode` and `LocEdge` gain optional combat-posture weights — `armed` / `unarmed` / `quad` / `pent` breakdowns (`LocWeights` on nodes, `LocEdgeWeights` on edges) restricted to samples where the player held RL or LG, held neither, or had an active quad / pent. Lets consumers re-weight the loc graph by combat posture without re-walking streams. Field additions only; each weight is omitted when no observed sample met its condition. |
| v11 | Bucket views gain a **column-major layout** (`view.ColumnarBuckets`): one dense typed array per `(player, field)` over the player's active span, implicit time axis (`time(i) = startMs + i*windowMs`), a `0`/`1` `alive[]` liveness mask, sparse per-field `validFrom`, booleans/alive as `0`/`1`, loc always the raw `li` index. It is the **default** for the web (`getDefaultBuckets`), REST `/buckets`, and MCP `getBuckets`; the row-major `BucketsView` stays available via `layout=row`. The legacy `HighResBucket`/`HighResPlayerData`/`HighResTeamData` shim and `view.ToLegacyHighResBuckets` are removed. The `Result` **structure is unchanged** — this bump versions the outward *view/query* wire surface so API/MCP/web consumers can feature-detect the new default shape and cached view responses (ETag/`X-Schema-Version`) are invalidated. |
| v10 | DeathEvent / SpawnEvent now derive primarily from the `DF_DEAD` bit in `svc_playerinfo` (broadcast every frame for every player) instead of relying solely on `STAT_HEALTH` crossings (directed at the active POV via `dem_stats`). The stat-based detector still runs and is deduplicated against the new signal — whichever fires first wins. Deaths whose `dem_stats` block was addressed to a different player slot are now captured; `PlayerStream.Spawns`/`Deaths` counts go up for affected demos. Downstream `LocGraph` edges (some spurious `teleport` edges across previously-missed deaths disappear), `LocTrails`, `RegionControl`, `WeaponPickups` (kills-before-next-death windows), and streak boundaries shift accordingly. Field shapes are unchanged. |
| v9 | Loc attribution gains visibility awareness via `mvd-analytics/locvis` (V6: Euclidean primary + PVS-veto). When a per-map BSP is available the analyzer rejects loc-points outside the player's potentially-visible-set, eliminating the brief "wall-bleed" phantom visits V1 produced. Field shapes unchanged: only the contents of `PlayerStream.Loc` (`li`) and everything derived (LocTrails, LocGraph edges, RegionControl) shift for maps with a BSP. Maps without a BSP fall back to V1 — bit-identical to v8 for those. Background: [`experiments/locattr/V2b-V6-HANDOFF.md`](../experiments/locattr/V2b-V6-HANDOFF.md). |
| v8 | All timestamped result fields migrate from `float64` seconds / `float32` seconds to `int32` milliseconds — every `T`/`Time`/`Start`/`End`/`Duration`/`AvailableFrom`/`TakenAt`/`RespawnAt`/`NextDeathTime`/`DropTime` field across the schema (`PositionTrack.T`, `PlayerStream.Spawns`/`Deaths`, `ChangeI16.T`/`ChangeStr.T`, `Interval.Start`/`End`, `GlobalStream.MatchStart`/`End`, `MatchResult.Duration`/`StartTime`/`EndTime`, `TimelineAnalysisResult.MatchStartTime`/`DemoOffset`, frag/powerup/streak/message/frag-entry/backpack/weapon-pickup/item-phase times). JSON keys unchanged; consumers reading as seconds must scale by 1/1000. The view-layer query API still takes and returns `float64` seconds at its public surface. Eliminates the float-precision drift that produced spurious teleport edges in locgraph when a respawn boundary and a position sample shared the same wire timestamp. |
| v7 | `Streams` added as the canonical event-rate storage (per-player change streams + intervals + native-rate position track with parallel `Li` column). `TimelineAnalysisResult.HighResBuckets` and `HighResDuration` removed; bucketed views are now produced on demand by `mvd-analytics/view.Buckets`. `RegionControlResult.BucketStates` removed from the parse-time output (still produced by `view.RegionControl` at the requested resolution). Health / Armor change streams use int16 (Quake values reach 250). `BuildLocGraph` and the region-control classifier (then `analyzer.ComputeRegionControl`, since folded into `view.RegionControl` as the sixth view function) walk `Streams` natively — no bucket intermediate. Default reducer policy is "first-sample-of-bucket" (point-sampling at bucket start; bucket N == state at t = N × windowMs). Bucket grid is anchored at match-relative t = 0; v6 anchored at the wall-clock 50 ms grid post-shifted by `−matchStart`, so the new grid is offset by up to one sample-interval from main's. Discrete event analytics (frags, items, weapon pickups, scoreboard) are byte-identical with v6; locgraph and region-control percentages drift slightly because of the native-rate sampling cadence (~13 ms between position samples vs v6's 50 ms grid). |
| v6 | HighResPlayerData adds `gl`, `sh`, `nl`. HighResTeamData adds `gl`. MatchEvent adds `messageClean`. ControlRegion adds `locs`. RegionControlResult adds `teamA`/`teamB`/`bucketStates`/`stats` + new `RegionStats`. Top-level `duration` removed (use `match.duration`). MatchResult.PlayerStat drops dead `kills`/`deaths`. |
| v5 | WeaponPickups added — slot-weapon acquisitions with kills-before-next-death effectiveness. Backpack pickups carry `backpackEnt` joining to `backpacks[].entNum`. |
| v4 | Backpacks added — RL/LG backpack drops sourced from KTX `//ktx drop` STUFFCMD_DEMOONLY directive. |

`CurrentSchemaVersion` lives at `result/result.go:CurrentSchemaVersion`;
bump when a change breaks consumers of the outward data — either the
`Result` structure **or** the on-demand view/query wire surface
(`/buckets`, `/events`, `/state-at`, …) served identically via
WASM/CLI/API/MCP — and add a row above in the same commit.
