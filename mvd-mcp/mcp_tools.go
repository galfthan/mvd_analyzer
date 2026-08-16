package main

import (
	"context"
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerTools wires every MCP tool onto the given server. Tools that act
// on a single demo go through `b` (the mvd-api proxy); `searchGames` goes
// through `sr`, which proxies to mvd-api's GET /v1/games/search — normally
// the same *proxyBackend as `b`. mvd-api owns the hub connection, so the
// shim needs no hub configuration and has a single egress point. The seam
// stays a distinct interface only so tool tests can inject a fake searcher.
//
// All view-shaped tool outputs are opaque JSON pass-through; only
// LoadDemo is typed, because consumers need to extract `demoId`
// from its result.
func registerTools(s *mcp.Server, b MCPBackend, sr searcher) {
	addTool(s, &mcp.Tool{
		Name:        "searchGames",
		Description: "Search hub.quakeworld.nu for matches by player names, teams, map, mode, matchtag, or date range. Returns {limit, offset, count, total?, games}: count = rows in THIS page, total = all matching rows (when the hub reports it) — page with limit/offset until offset+count >= total. Rows are compact by default (players projected to {name, team, frags}); roster:true returns the verbatim hub rows (ping, colors). Analysis tools accept gameId:N directly and auto-load on first use — loadDemo is an optional warm-up, not a required step.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchGamesInput) (*mcp.CallToolResult, any, error) {
		out, err := sr.Search(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "loadDemo",
		Description: "Resolve, fetch, parse, and cache a demo on the mvd-api. Returns the demoId (sha:HEX) to use with subsequent tool calls. Idempotent — cheap on warm cache.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in LoadDemoInput) (*mcp.CallToolResult, *LoadDemoOutput, error) {
		out, err := b.LoadDemo(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getOverview",
		Description: "Return a curated summary of the demo (map, teams, players) plus the per-demo CAPABILITY MANIFEST. `map` is the canonical shortname (dm2, dm3, …; joinable with searchGames rows and getMetadata serverinfo); `mapTitle` is the display-only level title (e.g. \"Claustrophobopolis\"), present only when it differs from `map` — never use it as an identifier. Who won: `teams` and `players` are sorted by frags descending, so teams[0] (team modes) / players[0] (duel/FFA) is the winner — frags is the canonical net score. Also carries `errors`: the analyzer's non-fatal errors — if non-empty the result is degraded (some sections may be missing/partial), so check it before trusting detail views. Use this first to decide which detailed view to query next, and BRANCH ON `available` instead of spending a call to find out: it carries one flag per detailed view (demoInfo, metadata, frags, damage, shots, aim, locGraph, opening, playerStats, regionControl, height, liquid, los), each mirroring the predicate behind that view's 422, so a false is exactly the 422 you would have got. height/liquid/los are the ones you CANNOT infer any other way — they depend on which map BSPs the server was provisioned with, not on what the demo recorded, so the same demo can answer differently on two deployments; `los` covers BOTH the los and pvs sets (one pass, one gate, pvs ⊇ los) and is a prediction rather than a reading because that pass is heavy and lazy. A true flag means the section EXISTS, not that it is non-empty. This response no longer inlines highlight lists (topKills/topStreaks/topPowerups were removed in v70 as copies of getTopKills, getLives and getEvents) — fetch those tools when you want the rows. UNITS: overview times (duration, matchStart/End) and every tool's startTime/endTime/time input are ALL integer MILLISECONDS (pure-ms model) — an overview time carries straight into a filter with no conversion. Response shape: see mvd-api /v1/demos/{id}/overview. Descriptive times are integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetOverviewInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetOverview(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getDemoInfo",
		Description: "KTX demoinfo blob — per-player weapon accuracy (hits/fires), kills/deaths/TK, damage dealt/taken, sprees, control time, item pickup counts, RL/LG transfers. Authoritative KTX scoreboard. Errors if the demo has no KTX demoinfo (rare; non-KTX servers or aborted matches).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetDemoInfoInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetDemoInfo(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getMetadata",
		Description: "Server cvars + KTX match settings: mode, timelimit, fraglimit, antilag, spawnmodel (k_spw), midair, instagib, overtime, powerups, noitems, vwep, noweapon, matchtag. Plus the full fullserverinfo cvar dump (hostname, version, watervis, dmgfrags, etc.). Used to answer 'what ruleset was this played under'.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetMetadataInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetMetadata(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getPlayerStats",
		Description: "Canonical per-player and per-team statistics — the one-call scoreboard. Corrected frags + deaths always; kills/suicides/teamKills/efficiency/byWeapon are OMITTED TOGETHER (render '-', not 0) on a demo whose obituary frag log measured nothing while players died. Damage — including damage.byWeapon / byWeaponTeam / byWeaponSelf, the given/givenTeam/givenSelf splits by attacker weapon, where byWeapon+byWeaponTeam are measured whenever the family is present (KTX writes both counters in one sub-block) while byWeaponSelf is measured only where a damage stream was read, i.e. exactly when damage.taken is present — per-weapon accuracy, per-kind pickup tallies, and POSSESSION TIME: how long each player held each weapon and armor type (the armor family is omitted entirely when the demo carries no armor stream; the no-armor complement is the hold.armor.none field). Available on EVERY demo, including old ones with no KTX demoinfo block. Every stat family carries src: 'derived' | 'ktx', plus 'derived:unbounded' on damage (raw wire values incl. overkill — never compare with bounded rows) and 'reconstructed' on damage when the demo predates the KTX damage instrumentation and the family rides the damage reconstruction (~1% match-total estimates; on such demos the enemyVs*/ewep buckets read 0 when the recording froze its weapon bits — unmeasurable, not zero) and 'mixed' on a team row whose members disagree (a data-integrity canary; player rows never carry it). score.byEnemyWeapon and damage.byEnemyWeapon are the VICTIM-weapon axis — the same kills and the same enemy damage split by what the TARGET was holding, the complement of the byWeapon maps which key on the ATTACKER's weapon. This is weapon denial: 'how many armed enemies did this player take out'. Buckets are MUTUALLY EXCLUSIVE: both (victim held RL and LG), rl (RL not LG), lg (LG not RL), mid (ssg/sng/gl), sg (nothing above shotgun tier), plus unknown on the kill side. score.byEnemyWeapon sums to score.kills EXACTLY (score is never overlaid); damage.byEnemyWeapon sums to the enemy damage this pipeline reconstructed, which equals damage.given on a src:derived row but NOT on a src:ktx one (there given is KTX's own counter and the split stays ours — a 0.03% residual over the corpus), so do not expect a share-of-given from these buckets to reach exactly 100% on a KTX row. CRITICAL: enemies killed while holding an RL is rl + both, NEVER rl alone — the same for lg — and adding the two double-counts the players in both. Derived on every demo with streams (KTX's own ekills is inclusive and mode-suppressed, so it is never overlaid); score.byEnemyWeapon is absent exactly when kills is, while damage.byEnemyWeapon needs the damage stream and is present exactly when damage.taken is. damage.enemyWeapons stays the lg+rl+both summary. score.byWeaponVsEnemyWeapon is the JOINT distribution those two kill maps are marginals of (killer weapon -> victim bucket -> kills) — use it for what neither marginal answers, e.g. 'how many of this player's LG kills were against enemies carrying an RL'; summing it reproduces byWeapon (over inner keys) and byEnemyWeapon (over outer keys) exactly. getDemoInfo remains the verbatim KTX block to diff against. Each PLAYER row also carries identity + sessions: identity is the reconnect-unification key — two rows with the SAME value are one human under two names (a player who reconnects while their old connection is still spawned is renamed `(N)name` by the server and scored as two players, which this pipeline reproduces rather than merging), and it is DEMO-LOCAL, so never persist it or compare it across demos (the cross-demo identity is login). sessions[] lists every wire occupancy behind the row in time order — {startMs, endMs, slot, userId, name} — so a hub ?track= link can use the userId VALID AT THAT INSTANT instead of the single per-player id in getOverview. Both are absent on team rows and on a scoreboard-only player (no stream). Other name-keyed tools (getLives, getTopWindows, getFrags, getDamage, getBuckets) join to these rows by player NAME — except when two identities share a display name, where this tool suffixes both rows `name#slot` while the frag and damage logs keep the bare name: strip the `#…` suffix to join, and read the answer as covering both same-named players. Hold times are exact integrals with explicit denominators (window.matchMs / presentMs / aliveMs) — read shareAlive/shareMatch rather than dividing yourself. NOTE our armor hold time reads LOWER than a KTX end-of-match table on purpose: KTX's clock keeps running after the armor is chewed to zero. efficiency and the shares are RATIOS in [0,1], not percentages. Descriptive times are integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetPlayerStatsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetPlayerStats(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getFrags",
		Description: "Frag aggregates + full kill log. totalFrags + byPlayer (kills/deaths/byWeapon per player) + byWeapon (kills per weapon) + frags (every kill with time/killer/victim/weapon/isSuicide/isTeamKill). NOTE: unlike getDamage/getAim/getItems, summary defaults FALSE here — the kill log is small (one row per frag) and usually the point; pass summary:true to drop it. Optional players= / weapons= filters narrow both aggregates and log. Empty-log convention: frags is null when dropped by summary=true, [] when included but the filter matched nothing. The weapon token 'teamkill' appears on phrasing-only teamkill obituaries whose message text does not name the weapon, so the real weapon is unrecoverable from the wire. Use this instead of aggregating getEvents(types:['frag']) yourself. Descriptive times are integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetFragsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetFrags(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getDamage",
		Description: "Per-hit damage aggregates + log — decoded from the wire KTX damage stream (source: 'ktx') or, on pre-instrumentation demos (~half the archive), rebuilt from spectator-visible state (source: 'reconstructed'; magnitudes near-exact, attribution ~98% — treat per-player totals as ~1% estimates). The response's source field tells which family you got. TWO damage families: 'raw' = the unbound wire values (overkill included, so totals run HIGHER than the KTX scoreboard, which caps damage to victim health); 'bounded' = KTX-scoreboard semantics reconstructed per hit (armor absorbed + health capped to the victim's remaining health), so per-player given/taken land near-equal to demoInfo dmg. Pick a family with dmg=raw|bounded|both. Default: an unset dmg resolves to BOUNDED for BOTH the summary (this layer's default) and the full log — the KTX-scoreboard number a reader usually wants. raw (the lean unbound v53 shape) and both (raw fields PLUS a per-player `bounded` nest and a per-event `bounded`) stay explicit opt-ins; a defaulted summary response carries a hint — pass summary:false for the per-hit events log (same bounded family). On a bounded/both SUMMARY the per-player bounded figures are sourced from the EXACT KTX end-of-match scoreboard when the demo carries demoInfo (boundedSource echoes 'ktx' vs 'reconstructed'); the per-weapon byWeapon AND byWeaponTeam maps are substituted with it too (KTX writes enemy and team damage in one per-weapon sub-block), while `taken`, `byWeaponSelf` and the enemyVs* buckets stay reconstructed (KTX has no matching split), so on a KTX-sourced summary they may not sum exactly to given. An EXPLICIT dmg=bounded errors (bounded_unavailable) on skipped:* demos (midair / instagib / dmgfrags / the clan-arena family ca|wipeout|ra|lgc|race), whose server mode rewrites or suppresses damage in ways the wire doesn't expose; a DEFAULTED bounded there falls back to raw instead. boundedMode echoes 'standard' or 'skipped:<mode>' from that vocabulary, and every bounded field is absent when skipped. The per-event `bounded` is OMITTED when it equals the raw damage (the common no-overkill case; note 0 is a real value — a fully-nullified pent/teamplay hit). Optional players= / weapons= / startTime/endTime filters; any filter recomputes every aggregate from the filtered log — except the scoreboard cross-check, which is a whole-match KTX figure with no per-event provenance: a players-only filter narrows it, but a weapons or a RESTRICTIVE time filter OMITS it entirely (an explicit from/to window covering the whole match counts as unfiltered). Positional instant kills — telefrags (the 9999 instakill sentinel) and stomps (head-stomps) — are EXCLUDED from byWeapon/matrix/totalDamage and listed separately (telefrags/stomps lists + per-player counts), BUT their damage DOES fold into given/givenTeam/taken — and, for enemy kills, the EWep victim-weapon buckets — in both families, matching KTX; each telefrags[]/stomps[] entry carries that folded value as `bounded` (and `damage` when the raw fold differs). On skipped:* demos NO fold happens at all — the raw family reverts to pure exclusion. Shape vocabulary: totalDamage + byWeapon (enemy damage per attacker weapon) + byPlayer (given (to enemies), taken (all sources), givenTeam, givenSelf, takenEnv, byWeapon, byWeaponTeam, byWeaponSelf, and the EWep victim-weapon buckets enemyVsSg/enemyVsMid/enemyVsLg/enemyVsRl/enemyVsBoth where ewep=lg+rl+both = damage to enemies holding RL/LG). + matrix (attacker->victim totals) + scoreboard (stream-vs-KTX cross-check; carries a bounded side for the like-for-like compare). The three per-weapon maps split given/givenTeam/givenSelf by the ATTACKER's weapon on the same keys (telefrags and stomps excluded from all three); byWeaponTeam/byWeaponSelf are omitempty and MEASURED whenever this artifact exists (it is built from the damage stream) — an absent key means 'dealt none with that weapon', never 'not measured'. matrix and the enemyVs* buckets stay enemy-only. Empty-log convention: events is null when dropped by summary, [] when included but the filter matched nothing. Related: getEvents(types:['damage']) is the per-hit feed — its detail.damage is the RAW wire value, with detail.bounded present when the KTX-scoreboard value differs; getEvents(types:['telefrag','stomp']) the positional-kill events. Descriptive times are integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetDamageInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetDamage(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getAim",
		Description: "Per-player aim analysis. summary defaults TRUE at this layer (the weapons aggregates only; the response carries a hint) — pass summary:false for the large per-fire crosshair + lgRamp columnar blocks. players scopes to named shooters; startTime/endTime (match-relative integer milliseconds) recompute aim over the shots in that window so every figure scopes to it (no window = the stored match-wide aim). First call on a demo may be slow (builds the projectile/beam streams). The top-level hitsMeasured flag says whether the hit-derived counters were measured against a wire KTX damage stream: when FALSE (reconstructed/absent damage — most old demos) hits, the pellet full/partial/miss split, direct/splash and the LG whiff classes are WITHHELD rather than fabricated as zeros (render 'not measured'), while shots, crosshair error and lgRamp timing stay valid; with hitsMeasured true an absent per-weapon hits is a measured zero. Shape vocabulary: players[].weapons is a deliberately ORDERED array of per-weapon entries keyed by each entry's weapon field (not a {weapon: …} object like byWeapon on getFrags/getDamage) — per-weapon shots/hits plus SG/SSG pellet stats (pellets, pelletHits, full/partial/miss fires), RL/GL direct/splash/missed, and the LG miss split (miss = aim error, no enemy on the beam's line; blocked = the beam would have hit an enemy in range but an object stopped it short; outOfRange = the enemy was on the beam's line but beyond its ~600u reach). hits count ALL victims (team + self hits included — server parity); the enemy/team/self objects slice the hit counters by victim class and appear only when a weapon had team or self hits (enemy absent = every hit was an enemy hit; a rocket jump is a self hit). mode notes attribution quality: 'duel' (exact, one enemy) or 'team' (nearest-crosshair-enemy heuristic); only enemies alive at fire time are miss candidates. Full detail (summary:false): crosshair = per-hitscan-fire angular error (signed degrees + normalized so ±1 = the hitbox edge, hit flag, attributed target, team flag on teammate targets); lgRamp = per-LG-cell hit vs ms since the shaft opened. Response shape: see mvd-api /v1/demos/{id}/aim. All aim times are integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetAimInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetAim(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getLocGraph",
		Description: "Per-map adjacency graph of named locations: which locs are reachable from which, with edge weights derived from per-player loc-to-loc transitions. Useful for movement-pattern reasoning ('what's adjacent to RA?'). Node weights (total/byPlayer/byTeam and the armed/unarmed/quad/pent breakdowns) are ALIVE, OBSERVED time-spent values in integer milliseconds (pure-ms model) — time while dead is excluded, as is time inside an unobserved hole (a sample's evidence expires after 250ms) and time past a player's end-of-track (schema v64), so these are lower than wall time in the loc; edge weights are transition counts, likewise excluding a corpse's travels and never crossing a hole longer than 250ms. The response echoes ms in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetLocGraphInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetLocGraph(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getChat",
		Description: "All-chat and team-chat messages within an optional time window. Returns {messages:[...]} where each message has time, type ('chat' or 'teamsay'), player, team, message (raw with ezQuake markup), messageClean (markup stripped). Cheaper and shape-cleaner than getEvents(types:['chat']) when you only want chat. Descriptive times are integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetChatInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetChat(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getBackpacks",
		Description: "RL/LG backpack drops emitted by KTX's //ktx drop hint. Returns {backpacks:[...]} where each entry carries time, dropper, weapon ('rl'/'lg'), origin XYZ, resolved loc, and the server ent number that joins to getWeaponPickups (the grab appears there as source:'backpack' with the same ent). Division of labour across the item trio: getItems = every world spawner's availability/take timeline; getWeaponPickups = who acquired slot weapons and their effectiveness; getBackpacks = the RL/LG drops that feed backpack grabs. Descriptive times are integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetBackpacksInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetBackpacks(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getItems",
		Description: "Per-item pickup/respawn timeline, returned as {items:[...]}. Each item has a unique name (suffixed when a map has several of a type: ya_1, ya_2, mh_1), a kind token (ra/ya/ga/mh/quad/pent/ring/rl/lg/ssg/sng/ng/gl/h15/h25/nails/shells/rockets/cells), world position + nearest loc, and a phases list — when it became available, when it was taken (if at all), by whom, when it respawned. Filters (case-insensitive): items= matches a name or kind ('ya' → both yellow armors, 'ya_1' → one, 'RA'/'MH'/'Quad' all work); kinds= matches a category (armor, mega, health, powerup, weapon, ammo); players= keeps only phases taken by those players. summary defaults TRUE at this layer (per-item take counts + first take; the response carries a hint) — pass summary:false for the full phase timeline. Division of labour across the item trio: this tool covers world spawners (armors, megas, powerups, weapons — which YA was taken, when, by whom); getWeaponPickups adds the acquirer's outcome and backpack grabs (which never flip a world spawner's state, so they are NOT phases here); getBackpacks lists the RL/LG drops themselves. Timeline phase times and the summary firstTake.time are both integer milliseconds (pure-ms model); the response echoes ms in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetItemsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetItems(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getMapEntitiesByMap",
		Description: "The map's static designed layout (NOT this match) — every item spawn, player spawnpoint, teleport destination/source, and button — addressed by map name directly (e.g. 'dm6'), no demo needed. Returns {map, entities:[...]}: each entity has a type (item/spawn/teleportDst/teleportSrc/button/door), raw BSP class, a loc-based name, world position XYZ, nearest loc, and (for items) a kind token. Sourced from the BSP entity corpus, identical for every demo on the map. Get the map name from getOverview; use getItems for the per-match pickup timeline. Filters (case-insensitive): types=, kinds= (category or raw kind).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetMapEntitiesByMapInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetMapEntitiesByMap(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getWeaponPickups",
		Description: "Slot-weapon acquisitions (world spawners + RL/LG backpacks) with kills-before-next-death effectiveness. Returns {pickups:[...]} where each pickup carries time, player, weapon, source ('world'/'backpack'), kills earned, next death time. Backpack pickups also carry the dropper and the ent number joining to getBackpacks. Division of labour across the item trio: getItems has the spawner-side timeline (which spawner, when available/taken); this tool has the acquirer's outcome incl. backpack grabs; getBackpacks the RL/LG drops. Descriptive times are integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetWeaponPickupsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetWeaponPickups(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getBuckets",
		Description: "Bucketed per-player time series over the match (health, armor, weapons, powerups, ammo, position, loc). Choose a windowMs that matches your query resolution. layout='column' (the default — compact, far fewer tokens) returns the column-major shape: top-level windowMs/start/count, then players[name] with first/n, an alive 0/1 mask over [first,first+n), and one array per field where the value at index i is bucket i and time(i)=start+i*windowMs (booleans are 0/1; loc is the raw 'li' index, decoded via the response's own locTable legend — no getLocTable call needed); teams[name] has per-field count arrays. layout='row' returns one self-describing object per bucket (handy for a single snapshot). For point-in-time snapshots (\"who had quad at 5:00\") use getStateAt instead — don't align indices across columnar arrays. For life counts / death timing use getEvents (the bucketed d/sp markers are per-window booleans, not exact). Keep payloads small with fields/players/from/to. Response shape: see mvd-api /v1/demos/{id}/buckets. Both layouts are integer milliseconds (pure-ms model): the column layout's start/windowMs axis and the row layout's bucket `time`; the response echoes ms in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetBucketsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetBuckets(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getEvents",
		Description: "Time-ordered discrete events (frags, powerups, streaks, spawns, deaths, pickups, chat). Default types exclude high-frequency change events (health/armor/loc). pickup events carry full identity in two shapes: world-spawner takes detail{item (per-spawner name, ya_1 vs ya_2), kind, entNum, loc?, source:'world'}; backpack/unknown weapon grants detail{item, kind, source, entNum? (backpack edict), dropper?} (no loc) — no cross-referencing needed to learn which spawner was taken. spawn events include the match-start spawn (time=0) and carry detail{loc} — types:['spawn'] with endTime:1000 answers \"where did everyone start\"; for the pre-joined opening summary use getArtifact('opening'). The default set also includes demomark (/demomark bookmarks inserted during play; detail carries spectator:true when a spectator inserted it), airgib (direct enemy rocket hits on airborne victims — the Key Moments list; player is the attacker, detail{victim, height, damage, lethal?, ...}) and pause (game-clock freeze segments, detail{durationMs}, no player). Environmental kills (squish/lava/etc.) are ordinary damage-log entries (e.g. weapon 'squish') reachable via types:['damage'] — unlike telefrag/stomp, which have dedicated event types because those positional kills are absent from the damage log. Response shape: see mvd-api /v1/demos/{id}/events. Descriptive times are integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetEventsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetEvents(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getStreamSlice",
		Description: "Raw native-rate change entries for each requested field inside a REQUIRED time window (startTime and/or endTime — an unwindowed slice would be the biggest payload this service can emit; keep windows tens of seconds). Right shape for inspecting a short event in detail (carry-forward at window start; intervals clamped to window). Health entries are the authoritative wire values: a negative value is the killing blow's overkill remainder (the player died at that entry) — for death events themselves prefer the d/sp streams or getEvents. Every player also carries alive: the canonical stored lives (one [s,e) interval per spawn-to-death run) clamped to the window — read it instead of re-deriving liveness from the sp/d markers, where a strict 'last spawn after last death' test latches on a same-millisecond death+respawn and reports the player dead for the rest of that life. It is always present and has three states: null = liveness was not measurable, [] = measured but never alive IN THIS WINDOW, [...] = the lives. Descriptive times are integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetStreamSliceInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetStreamSlice(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getStateAt",
		Description: "Point-in-time state per player at a given match-relative time. Carry-forward for change streams; nearest-sample for position; interval membership for held items/powerups. Two fields come from outside the fields selector, and only alive is ungated. alive rides EVERY row whatever fields asks for: the canonical stored liveness at the instant (never re-derived from the sp/d markers) with three states: true/false = measured, null = liveness was not measurable for that player. posAgeMs rides a row only when fields asked for at least one positional field (pos, view, hgt, lq, vel) AND a sample resolved — so its absence under a non-positional field set says nothing about the demo's position track. It is how far the snapped position sample is from the requested time (time minus the sample's timestamp; positive = carried forward from an earlier sample, negative = the nearest sample is a later one): the reported position is a carry-forward with no bound of its own, so check posAgeMs before trusting 'where was X at t' — the occupancy endpoints (getRegionControl, getLocGraph, getLocTrails) discard a sample once its age reaches 250ms. Descriptive times are integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetStateAtInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetStateAt(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getLocTrails",
		Description: "Per-player sequence of loc residences with dwell durations. Residences are ALIVE dwell (schema v64): a residence is truncated at a death and resumes at the respawn, and the final one ends at the player's end-of-track (their last position sample held for one measured cadence, capped at 250ms) rather than at match end, so it agrees with getLocGraph node time. minDwellMs filters nearest-loc flicker (MCP default 250; pass 0 for raw) by folding a short residence into the preceding one — never across a gap, so dead spans are not re-merged and a residence that follows one is returned even when shorter than minDwellMs. Each residence is a resolved loc name by default; pass loc='index' for raw LocTable indices (decode via getLocTable). Descriptive times are integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetLocTrailsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetLocTrails(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getLocTable",
		Description: "The demo's interned loc-name table: a string array where index i is the loc name (index 0 = '' no-loc sentinel). Only needed when you've requested loc='index' on another tool and want to decode the raw `li` integers back to names. In the default 'name' mode you never need this.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetLocTableInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetLocTable(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getRegionControl",
		Description: "Per-region territorial control over the match. Returns per-bucket bucketStates strings (one char per bucket: 'A'/'B' strong control by teamA/teamB, 'a'/'b' weak control, 'C' contested, 'c' weak contested, '_' empty) + a per-region stats block. In stats the seven state fields (teamAControl/teamAWeakControl/contested/weakContested/empty/teamBWeakControl/teamBControl) are match-aggregate PERCENTAGES (0..100, rounded to 0.1 so they sum to ~100), whereas stats.byPlayer entries (armed/unarmed) are integer MILLISECONDS of ALIVE presence — dead players are excluded (schema v64), a sample's evidence expires after 250ms so unobserved holes (PVS gaps on a POV recording) are credited to nobody, and a player who disconnects stops contributing at their end-of-track (last position sample held for one measured cadence, capped at 250ms) rather than holding their region to match end. The bucketStates strings honour the caller's windowMs, but the stats block is the exact time-weighted integral over the native position samples and life boundaries (no grid), independent of windowMs. The `regions` param controls polygon detail: this tool DEFAULTS to regions:'summary' (each region's ~6KB polygon points stripped; name/locs/centroids kept) for token economy — pass regions:'full' for the points (needed only to draw the map overlay), or regions:'none' to omit the regions list entirely. windowMs is integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetRegionControlInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetRegionControl(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getTopWindows",
		Description: "Each player's best stretches of the match, under one of two segmentations chosen with mode. mode:'fixed' (the default) is \"in these windowMs milliseconds this player scored higher on metric than in any other stretch of the same length\"; mode:'gap' is \"a window is a maximal run of scoring events no more than gapMs apart, scored by their sum\" — the stretch lasts as long as he kept doing it, not as long as a stopwatch says. Each mode takes exactly ONE knob and rejects the other's with a 400, and gapMs is REQUIRED under mode:'gap' with no default (measured starting points: ~10000 for the frag metrics, ~3000 for the damage and shot metrics — inter-kill gaps run p50 ~11-12 s while inter-damage-event gaps run p50 ~1.0-1.1 s, so no one value serves both). The envelope always echoes mode, plus windowMs on a fixed response or gapMs on a gap one — never both, so never infer the segmentation from which knob is present. GAP MODE vs getTopKills: getTopKills is the per-KILL view (one kill plus the killing-weapon burst behind it, kill-anchored, life-clipped, with returnDamage), gap windows the per-STRETCH view (best sustained output, no kill required, rows never overlap); at frags+rl+gapMs:3000 they collapse and gap mode is the wrong tool (85% of RL frag clusters would be singletons — use 8000-15000 for multi-kill stretches, ~3000 on damage for rampages). Gap rows are disjoint per player by construction, end is the run's LAST event (a lone event is a legitimate row with durationMs 0), signed metrics cluster on ALL their events (a death both extends a netFrags run and lowers its score), and a run MAY SPAN the player's own death — getLives is the per-life view. Everything below applies to BOTH modes except where it names windowMs. Answers 'when was X hot', 'the best 30 seconds of the match', 'who had the strongest burst'. Neighbours: getFrags/getDamage give WHOLE-MATCH aggregates + the raw logs, getBuckets a fixed grid, getLives the natural spawn-to-death unit; this is the only view that finds the best stretches for you. Windows are anchored at REAL event times (not a grid), non-overlapping per player, and returned as ONE FLAT list sorted by score and then — since ties on score are the common case, most of a frags page holding the same small integer — by a FIXED complementary metric, the other half of the same moment: damageGiven under frags/netFrags/shots/hits, frags under damageGiven/netDamage, damageTaken under deaths, deaths under damageTaken (then earlier start, then player name). There is no parameter for it. That secondary is read UNSCOPED and in the response's own damage family, so it is exactly the same-named field of the row's stats block — weapons= scopes the score, never the tie-break — and it also decides which of a player's overlapping equal-scoring candidates survives, so a window can be the stretch that ENDS on the scoring event rather than the one that starts on it. Each row carries player, team, a 1-based rank, score, and a stats block describing everything that happened in the window. The scoring rule rides the ENVELOPE as scoredBy {metric, weapons, dmg} — once, not per row, and the only place the metric is echoed. The envelope also echoes dmg + boundedMode, the damage family the STATS BLOCK was computed in under EVERY metric (a frags window still reports damageGiven) — read it rather than assuming bounded took, since a defaulted request falls back to raw on a skipped:* demo; both are absent only when the demo has no damage stream. windowMs is the main knob (5000 = damage bursts, 30000 = hot streaks, 120000 = map-phase dominance) — sweep it; there is deliberately no adaptive mode. METRIC CHOICE for weapon windows: shots/hits want a weapons filter (a shot is one discrete fire, so unfiltered sums are dominated by fast-cycling weapons, and hits exist only for weapons whose fires link to damage — axe/sg/ssg/lg always, rl/gl tracked, ng/sng parse-dependent — and only when the demo carried the WIRE damage stream: reconstructed damage never links fires, so hits reads unmeasured on most pre-instrumentation demos). A rocket's splash is a hit INCLUDING a rocket jump's self-splash (KTX-accuracy parity), so rank RL windows by metric=damageGiven&weapons=rl (enemy-only) and LG windows by metric=hits&weapons=lg (each cell equal; raw damage is ~30x hits, bounded discounts the killing cell's overkill). It must be >= 1 and no longer than the match. Two caps: perPlayer (max rows from one player, omit for uncapped) applies BEFORE limit (the total, default 10, max 200, negative = uncapped, explicit 0 rejected). startTime/endTime bound where a window may START, not what it covers — the best window anchored at endTime still runs windowMs past it, so a narrow endTime does not give you the best window WITHIN that span. weapons scopes the SCORING events only, so score can be a subset of the stats block. Every numeric stat is emitted even at zero — read the envelope's measured {frags,damage,shots,locs,items,liveness} block, never a field's absence, to tell an unmeasured source from a measured zero. 422 top_windows_unavailable when the demo carries no source stream for the chosen metric. Descriptive times are integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetTopWindowsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetTopWindows(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getTopKills",
		Description: "The match's hardest kill BURSTS, ranked by burst damage — the highlight-reel view. Answers 'the biggest rocket of the match', 'who died to a full LG burst', 'find me the clips'. Neighbours: getFrags is the exhaustive kill LOG (every kill, no damage figure), getDamage the whole-match aggregates + per-hit log, getTopWindows the best fixed-length stretches; this ranks individual kills by how hard they were. A burst is the contiguous run of KILLING-WEAPON hits the killer landed on that victim leading up to the kill, so each row is {rank, killer, victim, team (the KILLER's), time, weapon, damage, hits, spanMs, maxGapMs, victimWep, returnDamage}. THE BURST IS THE KILLING WEAPON'S RUN AND THAT IS THE QUESTION IT ANSWERS ('how hard was this burst with this weapon'), which on ~8% of kills UNDERSTATES what produced the kill — a rocket softens, a shotgun finishes, and the row reads weapon:sg damage:16 for a kill that took 250 across weapons. That is deliberate, not a defect; 'what did the kill take across weapons' is a getDamage question. gapMs is a CAPTURE gap, not a display one: it defaults to a generous 3000 because truncation is unrecoverable while over-merge is filterable, and YOU NARROW CLIENT-SIDE by keeping the rows whose maxGapMs is within your weapon's cadence (LG ~1200 ms, RL ~2300 ms) — every KEPT row then carries its tighter-walk value EXACTLY (an over-merged row is dropped, not truncated — ask the server with gapMs=g when the truncated remainder matters), whereas lowering gapMs itself loses damage (a baked 1200 ms gap truncated 11% of rl and 23% of sg bursts; worst measured, a 291-damage triple-rocket kill reported as 2). maxGapMs is the exact filter; spanMs is the DISPLAY figure ('291 dmg in 1.7 s') and is NOT a valid narrowing rule. POSITIONAL KILLS PRODUCE NO ROW: a telefrag, stomp or squish carries no damage event, so there is no burst to rank and they are absent from this list only (getFrags and getDamage still carry them) — 13 of 1,879 measured kills. KILLS BY AN ALREADY-DEAD KILLER STAY IN, deliberately: the walk consults the VICTIM's liveness and never the killer's, so a rocket in flight when its shooter died still ranks (38 of 1,866 measured, mostly posthumous rockets and mutual frags). returnDamage is what the victim dealt BACK to this killer over the contestedMs window before the kill (any weapon, same family) — a VALUE, not a flag, because the threshold that calls a kill contested is yours. victimWep is the victim's weapon class at the killing hit. The envelope echoes the resolved gapMs, contestedMs and limit, plus dmg + boundedMode, the damage family both damage and returnDamage were summed in — read dmg rather than assuming the bounded default took, since a defaulted request falls back to raw on a skipped:* demo. Also on the envelope is the measured {frags,damage,shots,locs,items,liveness} block; read it, never a field's absence. killer/victim are the frag log's names, so joining to getPlayerStats rows works by NAME except where two identities share a display name (those rows are suffixed name#slot there — strip the suffix). 422 top_kills_unavailable when the demo lacks a frag log, damage data, or measurable liveness (the burst walk is clipped by the victim's current life start, and without that clip a burst absorbs the victim's PREVIOUS life — on the rows that rank highest). Descriptive times are integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetTopKillsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetTopKills(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getLives",
		Description: "One row per spawn-to-death life — the natural unit of QuakeWorld analysis — carrying the same per-interval stats block getTopWindows uses, plus endReason, spawnLoc, deathLoc, killedBy, deathWeapon, itemsTaken and weaponsHeld. Answers 'how did that life go', 'what did he do with the quad', 'how many lives ended within seconds of spawning' (minMs drops the short ones). LARGE: a whole 4on4 match is ~400 rows, so summary defaults to TRUE here (scalars only, per-row collections dropped) — narrow with players/startTime/endTime/minMs before asking for summary:false. Neighbours: getFrags/getDamage are whole-match aggregates + raw logs, getTopWindows the best FIXED-length stretches, getEvents the bare spawn/death markers with no per-life rollup. Segmentation comes from the alive stream and lives PARTITION the match: a POSTHUMOUS kill (a rocket landing after its shooter died) is credited to the life that fired it, so per-life stats sum to the per-event LOGS for that player: getFrags' frags[] rows on the frag side, and getDamage's NON-SUMMARY aggregate on the damage side (not its events[] rows — a telefrag or stomp folds its value into the totals without a per-hit row of its own — and not an unfiltered bounded summary, which serves KTX's scoreboard instead of the reconstruction these rows are built from). They do NOT necessarily match the byPlayer scoreboard aggregates, which draw on sources the log does not (getFrags' deaths come from its own detectors, getDamage's bounded summary from KTX), so a death seen by DF_DEAD with no obituary counts there and has no log row to attribute here. On a demo where two wire slots resolve to the SAME player name, that player's stream is keyed name#slot while the logs keep the bare name: every stat in their lives rows reads 0 and their pickups attach to no life — getTopWindows is unaffected. durationMs stays ALIVE time, but the stats count over the WIDER attribution window each row carries as attrStart/attrEnd (the life plus the dead gap after it) — divide by attrEnd-attrStart for an exact rate; a rate over durationMs reads high, slightly across a whole match but by tens of percent on a single short life followed by a long dead gap. The envelope echoes dmg + boundedMode, the damage family every row's damage fields used, exactly as getTopWindows does (absent only when the demo has no damage stream). deaths counts the death rows attributed to the life: usually 1, but 0 whenever no frag-log row names the player at that instant — including on lives whose endReason IS death — and CAN EXCEED 1, since a life also carries any death recorded in the dead gap that followed it (the KTX dtTELE2 deflection). Do not treat it as a 0/1 flag and do not derive endReason from it; endReason is its own field. startTime/endTime keep lives OVERLAPPING the window, not only those contained, and a filtered response no longer reconciles. Every numeric stat is emitted even at zero — read the envelope's measured {frags,damage,shots,locs,items,liveness} block, never a field's absence. 422 lives_unavailable when the demo carries no per-player streams to segment, or none on which liveness was measurable. Descriptive times are integer milliseconds; the response echoes this in timeUnit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetLivesInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetLives(ctx, in)
		return toolResult(out, err)
	})

	// Generic DAG-artifact tools (Stage 4). The curated tools above stay the
	// product surface — their hand-written descriptions and filter params give
	// each section rich ergonomics. These two add a generic escape hatch: any
	// NEW servable artifact becomes reachable via getArtifact with ZERO new
	// hand-written tools. Discover names + shapes with listArtifacts first.
	addTool(s, &mcp.Tool{
		Name:        "listArtifacts",
		Description: "List every FETCHABLE analytics artifact (name, resultKey, cost light/heavy, lazy flag, description) — the discovery layer for getArtifact. Trimmed at the MCP layer to servable artifacts and routing-relevant fields; the full DAG (requires/provides edges, internal nodes) lives in REST /v1/artifacts and /v1/graph. Static per schema version. For the common sections prefer the dedicated tools (getFrags, getDamage, getAim, ...) which offer filters; heed per-artifact size notes in descriptions (e.g. timeline is LARGE).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in ListArtifactsInput) (*mcp.CallToolResult, any, error) {
		out, err := b.ListArtifacts(ctx, in)
		return toolResult(out, err)
	})

	addTool(s, &mcp.Tool{
		Name:        "getArtifact",
		Description: "Fetch one servable analytics artifact for a demo by its manifest name (from listArtifacts), e.g. frag, damage, loc-graph, opening, los. opening is the match-opening summary: each player's match-start spawn loc + the first take of every contested spawner (armors, mega, powerups, RL/LG) — the one-call answer to opening-race questions. Returns the artifact's Result section under its resultKey (los is materialised on demand; the first call may be slow). Takes no filters — for filtered/parameterised reads use the curated tools (getFrags players=..., getBuckets windowMs=..., etc.). Unknown or non-servable names error.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in GetArtifactInput) (*mcp.CallToolResult, any, error) {
		out, err := b.GetArtifact(ctx, in)
		return toolResult(out, err)
	})
}

// addTool wraps mcp.AddTool, supplying an input schema with the spurious
// "null" that jsonschema-go adds to every nilable Go slice/map stripped out.
// Left in, an array field like `players` reflects to
// {"type":["null","array"]}; several MCP clients treat that union as "no
// type" and coerce the value to a string, silently breaking every array
// filter (players/fields/types/weapon/items/kinds and the reducers map). A
// plain {"type":"array"} is accepted. The handler still decodes into the
// concrete In struct, so the Go side is unchanged.
func addTool[In, Out any](s *mcp.Server, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	if t.InputSchema == nil {
		t.InputSchema = inputSchema[In]()
	}
	if t.Annotations == nil {
		// Every mvd tool reads/analyzes demos; none mutate any user-facing
		// state (loadDemo only warms a transparent, reconstructible cache). The
		// readOnlyHint lets clients that honor it cut the per-call approval
		// prompt — the analytics surface is safe to run unattended.
		t.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: true}
	}
	mcp.AddTool(s, t, h)
}

// inputSchema reflects In's JSON schema and strips the null unions (see
// addTool). An `any` input has no object schema to reflect, so it gets a bare
// object, matching mcp.AddTool's own any-handling (and its object-type
// requirement).
func inputSchema[In any]() *jsonschema.Schema {
	if reflect.TypeFor[In]() == reflect.TypeFor[any]() {
		return &jsonschema.Schema{Type: "object"}
	}
	s, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("inputSchema: reflect %v: %v", reflect.TypeFor[In](), err))
	}
	stripNullTypes(s)
	return s
}

// stripNullTypes collapses every ["null", …] type union in the schema tree by
// dropping the "null" alternative. jsonschema-go emits it for nilable kinds
// (slices, maps, pointers); for a tool input an omitted optional field is
// already fine, so the null is noise that some clients mishandle.
func stripNullTypes(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	if len(s.Types) > 0 {
		kept := make([]string, 0, len(s.Types))
		for _, t := range s.Types {
			if t != "null" {
				kept = append(kept, t)
			}
		}
		switch {
		case len(kept) == 1:
			s.Type, s.Types = kept[0], nil
		case len(kept) != len(s.Types):
			s.Types = kept
		}
	}
	for _, p := range s.Properties {
		stripNullTypes(p)
	}
	stripNullTypes(s.Items)
	stripNullTypes(s.AdditionalProperties)
	for _, p := range s.PrefixItems {
		stripNullTypes(p)
	}
}

// toolResult adapts (Out, error) to the SDK's tool-handler return
// triplet. On error, returns an isError tool result with the error
// text in TextContent. On success, returns the typed output — the SDK
// serialises it into structuredContent.
func toolResult[Out any](out Out, err error) (*mcp.CallToolResult, Out, error) {
	if err != nil {
		var zero Out
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%v", err)}},
		}, zero, nil
	}
	return nil, out, nil
}
