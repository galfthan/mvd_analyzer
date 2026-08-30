// Package result defines the stable JSON contract produced by running a
// qwanalytics pipeline over a qwdemo.events.Source. Every analyzer's
// Finalize output is a value defined in this package; the top-level
// Result struct is the aggregate returned by the pipeline.
//
// Consumers of qwanalytics (web UI, CLIs, AI agents) should depend on
// this package directly and stay indifferent to where each sub-result
// is computed. The JSON schema is versioned via SchemaVersion so JS
// callers can feature-detect breaking changes.
package result

// CurrentSchemaVersion identifies the JSON schema shape. Bump on any
// breaking change to the outward data the pipeline serves — both the
// Result structure / its sub-types AND the on-demand view/query wire
// surface (Buckets, Events, StreamSlice, StateAt, LocTrails,
// RegionControl), which is served identically via WASM, CLI, the REST
// API, and MCP. Consumers pin or switch on this value to feature-detect
// breaking changes; it is also the REST API's ETag / X-Schema-Version,
// so a bump invalidates cached view responses.
//
// v4 adds Backpacks: a list of RL/LG backpack drops sourced from
// KTX's //ktx drop STUFFCMD_DEMOONLY directive. Pickup tracking is
// intentionally deferred until the wire-flutter reliability issue
// is resolved — see qwanalytics/analyzer/backpacks.go for the full
// reasoning.
//
// v5 adds WeaponPickups: a list of slot-weapon acquisition events
// (world spawners via //ktx took, RL/LG backpacks via //ktx bp)
// with an effectiveness metric — kills with the weapon before the
// picker's next death. Backpack pickups carry BackpackEnt which pairs
// with Backpacks[i].EntNum so frontends can join drop ↔ pickup.
//
// v6:
//   - HighResPlayerData adds GL, Shells, Nails (sh/nl/gl JSON keys);
//     HighResTeamData adds GL.
//   - MatchEvent adds MessageClean (markup-stripped chat text); raw
//     Message preserved.
//   - RegionControlResult adds explicit Locs[] on each region plus
//     TeamA/TeamB labels, BucketStates (compact one-char-per-bucket)
//     and Stats (match-aggregate percentages) — region control is now
//     computed in Go and re-callable via WASM.
//   - Top-level Result.Duration removed (use Match.Duration or
//     DemoInfo.Duration).
//   - MatchResult.PlayerStat drops dead Kills/Deaths fields (always
//     0; consumers read FragResult.ByPlayer or DemoInfoResult).
//
// v7:
//   - Adds Result.Streams: the canonical event-rate storage for every
//     per-player field (vitals, weapons, powerups, ammo, position,
//     loc, spawns, deaths). Sparse change streams + half-open
//     intervals + columnar position track. See qwanalytics/result/streams.go.
//   - Removes TimelineAnalysisResult.HighResBuckets and
//     TimelineAnalysisResult.HighResDuration. Bucketed data is now
//     produced on demand by qwanalytics/view.Buckets at any window
//     resolution, with per-field reducers selected by the caller.
//   - Removes RegionControlResult.BucketStates from the parse-time
//     output. View-time callers (CLI -view region-control, WASM
//     recomputeRegionControl) get it on demand at the requested
//     resolution.
//   - Health/Armor change streams use int16 (Quake values reach 250,
//     above int8 range).
//
// v8:
//   - PositionTrack.T changes from []float32 seconds to []int32
//     milliseconds. PlayerStream.Spawns / Deaths change from []float64
//     seconds to []int32 milliseconds. JSON keys unchanged; consumers
//     reading these as seconds must scale by 1/1000. The integer-ms
//     unit is what the MVD wire format already carries (1-byte ms
//     delta per message); keeping it integer eliminates the float-
//     precision drift that caused spurious teleport edges in locgraph
//     when a respawn boundary and a position sample shared the same
//     wire timestamp but disagreed by ~1e-6 after float roundtrip.
//   - Other timestamped result fields (ChangeI16.T, Interval.Start/End,
//     MatchEvent.Time, frag/powerup event times) remain float64
//     seconds — they don't participate in the boundary comparison
//     that motivated this change.
//
// v9:
//   - Loc attribution gains visibility awareness (V6 algorithm in
//     mvd-analytics/locvis). When a BSP is available for the demo's
//     map the analyzer rejects candidate loc-points that fall outside
//     the player's potentially-visible-set, eliminating the brief
//     "wall-bleed" phantom loc visits V1's pure-Euclidean nearest-
//     neighbour produced. Maps without a BSP fall back to V1 unchanged.
//     Affected fields: PlayerStream.Loc (li), Backpacks[i].Loc,
//     ItemTimeline[i].Loc, plus everything derived from those
//     (LocTrails, LocGraph edges, RegionControl). Field shapes are
//     unchanged — only the contents shift for maps with BSPs.
//
// v10:
//   - DeathEvent / SpawnEvent gain two new signal sources beyond the
//     v9 StatHealth-crossing detector:
//     1. The DF_DEAD bit in svc_playerinfo (broadcast every frame
//     for every player), captured in mvd-reader/parser/position.go.
//     2. Victim-prefix and infix obituary prints (rocketed by,
//     telefragged by, "Satan's power deflects X's telefrag", the
//     CRMod-added "disembowled" / "shish-kebabed" / etc. set,
//     KTX's k_spawnicide variants) matched in
//     mvd-reader/parser/obituary.go and consumed in parsePrint,
//     gated on a parser-internal match-started flag so warmup
//     obits cannot pre-seed dedup state.
//     The first two sources flow through maybeEmitDeath /
//     maybeEmitSpawn which dedupe against each other. The obit path
//     uses forceEmitDeath instead, bypassing dedup, because KTX's
//     own deathcount (logfrag) can increment without any visible
//     DF_DEAD / stat transition on the wire — the most common case
//     being a Satan-pent deflection (dtTELE2) that fires against a
//     player whose entity state never visibly leaves the previous
//     dead interval. Cross-validated end-to-end against KTX's
//     authoritative demoinfo `stats.deaths` scoreboard. Field shapes
//     are unchanged; PlayerStream.Spawns / Deaths counts rise for
//     affected demos and downstream LocGraph, LocTrails,
//     RegionControl, WeaponPickups, and streak boundaries shift
//     accordingly.
//
// v11:
//   - Bucket views gain a column-major layout (view.ColumnarBuckets):
//     for each (player, field) one dense typed array over the player's
//     active span, with an implicit time axis (time(i) =
//     startMs + i*windowMs), a 0/1 alive[] liveness mask, a sparse
//     per-field validFrom, and booleans/alive emitted as 0/1. It becomes
//     the default for the web (getDefaultBuckets), the REST /buckets
//     endpoint, and MCP getBuckets; the row-major view.BucketsView stays
//     available via layout=row. Columnar always emits the raw loc index.
//   - Removes the legacy HighResBucket / HighResPlayerData /
//     HighResTeamData shim and view.ToLegacyHighResBuckets (the v6 WASM
//     bridge shape). The Result *structure* is unchanged; this bump
//     versions the outward view/query wire surface so API / MCP / web
//     consumers can feature-detect the new default bucket shape and
//     cached view responses are invalidated.
//
// v12:
//   - LocGraph nodes and edges gain optional Armed / Unarmed / Quad / Pent
//     weights: the same Total / ByPlayer / ByTeam time (node) and
//     transition-count (edge) breakdown restricted to samples where the
//     player held RL or LG (Armed), held neither (Unarmed), or had an
//     active Quad / Pent powerup. Additive and backward-compatible (all
//     omitempty), but the bump invalidates cached loc-graph responses so
//     consumers pick them up.
//
// v13:
//   - New MapEntities section: the map's static designed layout (item
//     spawns, player spawnpoints, teleport destinations/sources,
//     buttons) with type + location, sourced from the offline-generated
//     mapents corpus (BSP entity lumps) keyed by map name. Additive
//     (omitempty); absent when no corpus exists for the map.
//
// v14:
//   - MapEntities gains brush entities — teleportSrc (trigger_teleport),
//     button (func_button), door (func_door) — placed at their BSP
//     submodel bbox centre with a Bounds (trigger/door volume), plus the
//     teleport source→destination link via MapEntity.Target ==
//     teleportDst.TargetName. v13 carried point entities only.
//
// v15:
//   - TimelineAnalysis gains DeathEvents: a per-player death stream
//     ({time, player, team}) parallel to FragEvents, sourced from the
//     authoritative protocol DeathEvent (every death counts once), for
//     the Timeline tab's per-player frags/deaths drill-down and KTX-style
//     efficiency = frags/(frags+deaths). Additive and omitempty, but the
//     bump invalidates cached timeline responses so consumers pick it up.
//
// v16:
//   - PlayerFrags gains TeamKills (KTX "tk") and teamkills re-enter
//     Frags.Frags as complete killer↔victim pairs (previously dropped
//     because the obituary names only one party). Killer-named teamkills
//     ("X loses another friend") recover the victim by matching the
//     coincident authoritative DeathEvent on the killer's team;
//     victim-named ones ("X was telefragged by his teammate") recover the
//     killer by combining position co-location with the teamkiller's −1
//     frag-delta. The messages stream also now tags Satan's-power-deflect
//     self-telefrags as frag events (one frag event per death). Additive,
//     but the bump invalidates cached frag responses so consumers pick it
//     up.
//
// v17:
//   - Self-kill weapon labels in Frags.Frags are no longer flattened to
//     "suicide": only the /kill console command ("X suicides", −2 frags)
//     keeps weapon "suicide"; weapon self-detonations now carry their real
//     weapon (rl/gl/lg) with IsSuicide set, matching the messages stream.
//   - Frags.ByWeapon is now enemy kills only (suicides/teamkills excluded),
//     so self-detonations under their real weapon don't inflate kills.
//   - Recovered teamkills no longer carry a stale IsSuicide flag (the "X
//     gets a frag for the other team" case). Bump invalidates cached frag
//     responses so consumers pick up the relabeled weapons.
//
// v18:
//   - TimelineAnalysis gains KillEvents: a per-player enemy-kill stream
//     ({time, player, team}) keyed on the killer, parallel to DeathEvents,
//     sourced from the canonical frag log (FragEntries) and filtered to
//     real enemy kills (suicides/teamkills excluded). Lets the Timeline
//     tab's per-player drill-down plot an exact cumulative kills−deaths
//     +/- that reconciles with byPlayer.kills and kills-based efficiency.
//     Additive and omitempty, but the bump invalidates cached timeline
//     responses so consumers pick it up.
//
// v19:
//   - MatchResult.PlayerStat gains Kills, Deaths and Suicides: the
//     frag-log-corrected counts, so MatchResult.Players is a complete
//     corrected scoreboard rather than just the net frag tally. They
//     supersede the KTX demoinfo stats, which credit several self/positional
//     deaths to the wrong entity — pentagram-deflect telefrags (dtTELE2)
//     inflate the deflector's kills, and world-dealt suicides (fall/lava/
//     squish/drown) bump the world entity's counter, not the victim's
//     (ktx/src/client.c:4951), so demoinfo undercounts suicides. 0 when the
//     demo carried no frag log. The API /overview player rows surface the
//     same Kills/Deaths/Suicides so non-web consumers get the correction the
//     web UI already applied. Field additions only.
//
// v20:
//   - New Damage (DamageResult) section: per-hit damage log + aggregates
//     (attacker→victim matrix, per-weapon, given/taken, and the EWep
//     victim-weapon buckets enemyVsSg/Mid/Lg/Rl/Both where ewep=lg+rl+both)
//     reconstructed from the KTX mvdhidden_dmgdone stream, plus a scoreboard
//     cross-check vs demoInfo.players[].dmg. Positional kills (telefrag,
//     stomp) are excluded from all damage figures and surfaced separately
//     (Damage.Telefrags/Stomps, opt-in telefrag/stomp events). Also a
//     Layer-1 change: world/environmental damage-taken is emitted with an
//     Attacker == -1 "world" sentinel rather than dropped. Additive
//     (omitempty); absent when the demo lacks the KTX hidden-damage stream.
//
// v21:
//   - TimelineAnalysis gains a wall-clock anchor for the demo timeline:
//     demoStartUnixMs (server clock, Unix epoch ms, at demo open / t=0)
//     plus demoStartAccuracyMs (its resolution: 1 from the mvdhidden
//     0x000B millisecond block, 1000 from the whole-second serverinfo
//     `epoch` cvar). With the existing demoOffset, a consumer maps any
//     match-relative game time to wall clock for syncing external data
//     (voice, stream overlays). Additive (omitempty); absent when the
//     demo carries no wall-clock source.
//
// v22:
//   - TimelineAnalysis gains pauses[]: per-pause {atMs, durationMs} segments
//     recovered from the mvdhidden 0x000A (paused_duration) blocks mvdsv
//     embeds once per idle frame while paused. The game clock freezes during
//     a pause, so the v21 wall-clock formula drifted by the total pause time
//     on paused demos; folding Σ durationMs for atMs <= g into the mapping
//     fixes it. The parser now decodes 0x000A (it omits the standard hidden
//     block-length header — a bare type_id+byte — so it is read via a
//     dedicated path). Additive (omitempty); absent when the demo has no
//     pauses or was recorded by a server that does not embed the block.
//
// v23:
//   - Move the wall-clock/timing anchor from timelineAnalysis to
//     streams.global (breaking move, not additive): demoOffset,
//     demoStartUnixMs, demoStartAccuracyMs, pauses now live there next to
//     matchStart/matchEnd (the match window they time). The redundant
//     timelineAnalysis.matchStartTime (always 0, duplicated by
//     streams.global.matchStart) is dropped. timelineAnalysis keeps its
//     event-shaped data + map metadata, including locTable. The /overview
//     REST endpoint gains a `timing` block exposing the wall-clock anchor to
//     REST/MCP consumers (previously only the in-process WASM build could
//     read it).
//
// v24:
//   - PositionTrack gains an H column: the player's height above the
//     floor directly beneath them at each native-rate sample (feet above
//     the nearest solid surface below), from a straight-down trace
//     through the map's worldspawn player clip hull. The hull is parsed
//     from the map's BSP CLIPNODES at analyze time by the new mapclip
//     package, with BSP bytes from the same best-effort source as the
//     visibility-aware loc filter (the shared mapbsp loader) — no
//     generated corpus. H reads ~0 when grounded and grows during a jump
//     / airborne hit (airgib), so consumers flag those directly; the
//     absolute floor is Z - 24 - H if needed. Sentinel result.NoFloor
//     marks samples with no floor to measure from (void/pit, or a moving
//     brush model such as the dm2 lift, which the worldspawn-only hull
//     excludes). Additive (omitempty); absent when no BSP is provisioned
//     for the map.
//
// v25:
//   - TimelineAnalysis gains airgibs[]: the top airborne rocket hits
//     (AirgibEvent) for Key Moments — each DIRECT enemy rocket hit (splash
//     excluded) whose victim was >= 96 units above the floor (≈ two player
//     models), annotated with attacker/victim (name, team, userid), the
//     hit time, the victim's loc and height, raw damage, and whether it
//     was lethal (a matching rocket frag near the hit). Derived by a
//     post-processor from result.Damage (per-hit log) + the streams'
//     PositionTrack.H column + the frag log; capped and sorted by height
//     descending. Additive (omitempty); empty when the map has no clip
//     hull (no H column) so no airborne height can be computed.
//
// v26:
//   - PositionTrack.H is now measured over the player's bounding-box
//     footprint, not just the origin column: the height is taken to the
//     highest floor found under a 3x3 grid of columns sampled ±8 around
//     the origin (mapclip HeightAboveFloorBox). On the already-±16-box-
//     inflated hull that is an effective ~48-wide footprint — the true
//     box plus a small safety band. A player skimming a ledge
//     / well rim — origin momentarily over the pit while the box overhangs
//     the rim — now reads the near floor (small H) instead of plunging to
//     the distant floor far below. Same shape and units; only values near
//     ledges change, which also removes the bogus high airgibs those
//     samples produced (e.g. anwalked RA's well rim logged a 553-unit
//     airgib that was really a rim skim).
//
// v27:
//   - PositionTrack.H now stands players on moving brush-model entities
//     (lifts, doors, trains): the parser surfaces "*N" submodel entities
//     as MoverSpawn/MoverState events, and the floor trace runs over the
//     worldspawn hull PLUS each mover's submodel clip hull posed at its
//     demo-streamed origin for the sample's timestamp (mapclip
//     HeightAboveFloorBoxScene) — the highest floor wins. A player
//     riding the dm2 RA lift reads ~0 instead of the height to the
//     shaft floor, which also removes the false "airgib" entries rocket
//     hits on lift riders produced (dm2 "path.lift"/"Quad.button", dm3
//     "lifts"). NoFloor accordingly narrows: "on a moving brush model"
//     disappears as a cause, leaving void/pit, embedded and zero
//     origins. Same shape and units; only values over movers change.
//
// v28:
//   - PositionTrack gains an Lq column: per-sample liquid state, packed
//     (type << 2) | level — level 1-3 (feet/waist/eyes submerged,
//     mirroring the engine's PM_CategorizePosition probes against the
//     map's render BSP), type LqWater/LqSlime/LqLava (water 5/6/7,
//     slime 9/10/11, lava 13/14/15; 0 = dry). Decode with
//     result.LqLevel (type = Lq >> 2). Additive (omitempty); absent when
//     no BSP is provisioned.
//   - H interacts with liquids: a sample in liquid (Lq level >= 1)
//     reads H = 0 by definition (the surface is the support — swimmers
//     in the dm3 pool no longer read as airborne over the pool bottom),
//     and a dry sample airborne above water/slime/lava measures down to
//     the liquid surface when it is the highest support beneath the
//     player (bspvis.LiquidSurfaceBelow).
//
// v29:
//   - AirgibEvent gains heightAboveAttacker: the victim's origin minus
//     the shooter's at the hit (units; negative = victim below) — the
//     vertical gap the rocket climbed, often the more impressive number
//     for a highlight than the floor height. From the two players'
//     nearest position samples to the hit; 0/omitted when the shooter
//     had no sample near the hit. Ranking and the >= 96 threshold still
//     use the floor height; the web table adds a sortable column.
//
// v30:
//   - TimelineAnalysis.Airgibs is no longer capped at the top 20: every
//     hit that qualifies (direct enemy rocket, victim >= 96 units above
//     the floor) is emitted, still sorted by floor height descending.
//     The qualification threshold already bounds the list to a handful
//     per match, and a cap keyed on floor height could drop the hits a
//     consumer sorting by heightAboveAttacker cares about most.
//
// v31:
//   - PositionTrack gains VP/VYa columns: the player's view direction
//     (pitch, yaw) per sample as the raw angle16 wire shorts, kept
//     losslessly. Decode to degrees with float(uint16(v)) * 360/65536
//     (values in [0,360); pitch > 180 = looking up). Roll is not stored
//     (the server forces it to 0). Additive (omitempty), populated
//     whenever the position track is. New view-layer field codes expose
//     them: `view` (vp/vya), plus `hgt` (h) and `lq` split out so a
//     consumer can request height/liquid/view without x/y/z — and `pos`
//     now returns strictly x/y/z (h/lq no longer ride along it).
//
// v32:
//   - PositionTrack gains VX/VY/VZ columns: the player's velocity per
//     sample in Quake units/sec, derived from the position columns by a
//     central-difference estimator (it does not differentiate across a
//     respawn teleport or an abnormal time gap, so it reads ~0 there
//     instead of spiking). Additive (omitempty), populated whenever the
//     track is — no BSP needed. New opt-in view-layer field code `vel`
//     (vx/vy/vz) and CLI `-include velocity`. Expect ±1-unit
//     quantization noise on the raw derivative (integer-rounded source
//     positions); smooth client-side for a clean speed curve.
//
// v33:
//   - PositionTrack X/Y/Z, VX/VY/VZ and H change from int32 to float32,
//     so we stop truncating the wire-native sub-unit origin (mvd-reader
//     decodes coordinates as float32; the wire carries eighth-unit fixed
//     point or true floats). Velocity, derived by central difference, is
//     now sub-unit precise too — the old ±1-unit quantization noise from
//     integer-rounded source positions is gone. The PositionTrack.H
//     NoFloor sentinel changes from -2147483648 (math.MinInt32, which
//     float32 cannot represent exactly and serializes as -2147483600) to
//     -1000000000 (-1e9, exact in float32 and float64). Time axes stay
//     int32 ms; view angles stay int16 (raw angle16); loc/liquid columns
//     unchanged.
//
// v34:
//   - TimelineAnalysis.LocationData now carries one MapLocation per loc
//     name — the medoid of that name's corpus points — instead of every
//     raw .loc point. The .loc corpus often repeats a name across several
//     nearby points, which drew duplicate map labels; the medoid is the
//     actual point minimizing summed distance to its same-name siblings
//     (never an averaged mid-air position). Same field name and shape;
//     the list is shorter. locgraph already read one point per name.
//
// v35:
//   - Streams gains Movers []MoverStream: the pose timeline of every
//     tracked brush-model entity (lift, door, plat, train). Each carries
//     EntNum, SubModel (the "*N" brush-model index, matching the corpus
//     SubModelMesh ID), and index-aligned T/X/Y/Z/Vis columns — the
//     mover sits at (X,Y,Z)[i] at T[i] ms and is drawn when Vis[i].
//     Origins are float32 (exact 1/8-unit wire values). Times are
//     match-relative; the first entry is clamped to t=0 carrying the
//     match-start pose so a parked mover (whose only wire state predates
//     the match) still has one. Additive (omitempty); absent when the
//     demo has no movers. The same internal mover tracks already feed the
//     v27 floor-height pass.
//
// v36:
//   - MatchResult drops the dead StartTime / EndTime fields. After the
//     match-relative time normalization StartTime was always 0 (already
//     omitempty, so absent from JSON) and EndTime always equalled
//     Duration; both duplicated streams.global.matchStart/matchEnd. The
//     `endTime` key disappears from the `match` object — read Duration for
//     match length, or streams.global for the match window. Breaking
//     removal (not additive); the view query API is unaffected.
//
// v37:
//   - PlayerStream gains LOS []LosTrack: per-opponent line-of-sight as
//     half-open [Start,End) ms intervals during which the looker had a clear
//     sightline (origin+(0,0,22) eye → any of the opponent's 8 bbox corners +
//     midpoint), blocked by worldspawn solids or any active mover posed in
//     the way. Asymmetric (A→B in A's stream, B→A in B's); Other indexes
//     Streams.Players. Computed against the visibility BSP, so present only on
//     maps with a provisioned BSP (same gate as PositionTrack.H/Lq). Additive
//     (omitempty); absent on BSP-less maps. View direction is not considered.
//     Computed lazily (analyzer.ComputeLOS) — NOT during the default parse,
//     since it is the heaviest position-derived pass — and so absent unless a
//     consumer requested it (web LOS overlay, qw-analyze -include los,
//     mvd-api /los). The Streams.LOSComputed guard (gob-only, json:"-") makes
//     it idempotent — but note it latches only on a genuine compute or a
//     legitimately empty <2-player demo; a map with no usable BSP yields
//     analyzer.ErrNoBSP without latching (mvd-api → 422 los_unavailable), so
//     no empty result is ever persisted and provisioning the BSP later heals.
//
// v38:
//   - PlayerStream gains PVS []LosTrack alongside LOS: per-opponent
//     potentially-visible-set intervals reproducing the server's per-client
//     entity cull (mvdsv SV_PlayerVisibleToClient) — the looker's fat PVS
//     (CM_FatPVS of origin+view_ofs) ∩ the opponent's entity leaf set (expanded
//     box, non-solid), or always when it overflows MAX_ENT_LEAFS. I.e. whether a
//     live server would have sent that opponent to the client (the recorded MVD
//     itself stores every entity, pvs = NULL). Same LosTrack shape, same lazy
//     pass (analyzer.ComputeLOS), BSP gate and Streams.LOSComputed guard as LOS.
//     This test also gates the LOS raycast, so PVS ⊇ LOS by construction: the
//     gap is the occlusion-tolerant "on the wire but no clear ray" signal.
//     Additive (omitempty); absent on BSP-less maps and on the default parse.
//
// v39:
//   - New top-level Shots *ShotsResult: a per-shot weapon-fire stream
//     (who fired what, at exactly what match-relative ms) derived from
//     svc_sound CHAN_WEAPON fire sounds (SG/SSG/RL/GL/NG/SNG) and LG cell
//     decrements, with same-frame hitscan→damage linking (sg/ssg/lg) and a
//     diagnostic reconciliation against KTX acc.attacks. Additive
//     (omitempty); the stream is present whenever any fire is detected,
//     even on non-KTX servers (no damage stream → no hit links).
//
// v40:
//   - Streams gains two opt-in spatial weapon-fire streams for the map view:
//     Streams.Projectiles (every tracked rocket/grenade flight as
//     spawn→despawn segments + times) and Streams.Beams (every LG
//     TE_LIGHTNING2 bolt as a muzzle→impact segment + time). Both are built
//     only when requested (qw-analyze -include projectiles,beams; the WASM
//     map build) so the default output and goldens stay lean. Additive
//     (omitempty); absent from the default parse.
//
// v41:
//   - New top-level Aim (*AimResult): per-player aim analysis derived as a
//     post-process from Shots + Streams (interpolated position/view at fire
//     time) + Damage + the LG beam stream — normalized crosshair-error
//     samples (hitscan), LG ramp-onto-target, rocket direct/splash, LG
//     reach/whiff. Additive (omitempty); the crosshair/ramp blocks compute
//     by default, the rocket/reach blocks only when their streams were built.
//
// v42:
//   - Shot gains Warmup: true for fires outside the match (prewar / warmup /
//     post-match). The shot stream still keeps them; ByPlayer and the aim
//     analysis exclude them. Additive (omitempty).
//
// v43:
//   - Aim target attribution gates candidates on being alive at fire time
//     (then losAliveAt over the spawn/death streams; since v64 the stored
//     PlayerStream.Alive). Dead players keep streaming
//     position samples (the death-anim body), so a corpse could previously
//     win nearest-crosshair attribution in team games. No field changes;
//     crosshair sample counts/targets shift on team demos, and a duel fire
//     while the lone enemy is dead no longer emits a sample.
//
// v44:
//   - Aim crosshair samples of hit shots attribute to the server-confirmed
//     victim (nearest by crosshair error when a pellet fire hit several),
//     bypassing the v43 liveness gate and the enemy filter. The killing blow
//     lands in the same frame the victim dies, so the liveness gate read the
//     victim as already dead at the fire time and attributed the shot to the
//     nearest *other* live enemy — hits appeared tens of hull-widths off
//     target in team games, and duels dropped their killing-blow samples
//     entirely. No field changes; hit samples' tgt/dyaw/dpitch/nyaw/npitch/
//     dist shift, and duels gain one sample per hitscan kill.
//
// v45:
//   - Victim-class classification on the shots/aim pipeline, mirroring the
//     Damage layer's IsSelf/IsTeam semantics. Shot gains VictimKinds
//     (parallel to Victims: "enemy"/"team"/"self", omitted when all-enemy);
//     WeaponShots gains EnemyHits/TeamHits/SelfHits (overlapping buckets —
//     a multi-victim fire counts in each bucket it has a victim in);
//     CrosshairSamples and LGRampSamples gain a Team column; WeaponAim gains
//     Enemy/Team/Self *WeaponAimSplit hit-counter slices. All additive
//     (omitempty) — Hits/Accuracy stay all-victims for KTX parity.
//
// v46:
//   - Weapon-stay recovery (serverinfo deathmatch 2/3/5, or coop — the
//     standard duel/2on2 dmm3 included): KTX never emits `//ktx took` for
//     weapons in those modes and the weapon entity never leaves the wire,
//     so world weapon pickups were previously absent entirely. They are now
//     synthesized from STAT_ITEMS weapon-bit 0→1 transitions. WeaponPickup
//     gains Inferred (marks synthesized entries) and the Source vocabulary
//     gains "unknown" (a flip with no weapon pad in touch range — typically
//     a non-RL/LG backpack grant, which has no hint in any mode).
//   - ItemTimeline weapon phases in weapon-stay demos use a zero-length
//     unavailability convention: TakenAt == RespawnAt, with the next phase
//     opening at the same instant (the weapon never left the map).
//   - Duel team normalization now also rewrites Items phase teams,
//     WeaponPickups Team/DropperTeam, Backpacks Team, Shots stream/ByPlayer
//     teams (and transitively Aim teams), and Airgibs attacker/victim teams
//     — previously these kept the raw pre-normalization team strings in 1v1
//     demos, so team-keyed pickup aggregation bucketed under stale labels.
//     It also reclassifies Shot.VictimKinds "team" → "enemy" (folding the
//     WeaponShots TeamHits bucket into EnemyHits): victimKindOf compares
//     raw team strings, so a duel where both players share a colour team
//     classified every opponent hit as "team". Aim's enemy/team splits
//     follow via aimPost ordering.
//   - Item pickup attribution: the Layer-4 distance corroborator samples
//     positions from the per-frame history at the touch instant and all
//     proximity consumers share a measured 128 u touch gate (was a 256 u
//     stale-sample bound) — a handful of beyond-gate distance attributions
//     become honestly unattributed phases.
//
// v47:
//   - LG miss reclassification (WeaponAim). A miss only counts as Blocked
//     or OutOfRange when the shooter was on target: Blocked = the beam
//     stopped short of its ~600 u max range on geometry and its extension
//     to full range crosses a live enemy's collision hull (a would-be hit
//     denied by the obstruction); OutOfRange = the beam ran its full
//     length and its extension to infinity crosses a live enemy's hull
//     (denied by reach). Previously every short-of-max-range beam whose
//     endpoint wasn't near an enemy was Blocked (even fired into a wall
//     with nobody behind) and every full-length beam was OutOfRange.
//     NearMiss is removed: with blocked detection on the beam line, the
//     near/wide distinction among plain aim errors carried no signal —
//     all remaining whiffs land in the lg `miss` bucket (field shared
//     with the SG/SSG per-pellet Miss). LG invariant becomes
//     Hits + Blocked + Miss + OutOfRange + Unresolved == Shots.
//
// v48: correctness fixes to already-emitted values (no field shape change).
//   - timelineAnalysis.killEvents are now on the match-relative clock and
//     carry duel team labels, exactly like the sibling deathEvents/
//     fragEvents streams (both post-processors previously skipped them):
//     each kill was ~demoOffset ms late and, in 1v1s, tagged with a raw
//     colour team instead of the player name.
//   - Chat lines can no longer start or end the match: the match-timing
//     detector ignores PRINT_CHAT (level 3), so a pre-match "go!" or a
//     mid-match "gg game over" say no longer flips the match window (which
//     would shift matchStart/matchEnd and freeze/warp every stream).
//   - The CRMod "eats 2 scoops of" super-shotgun obituary is reachable
//     again: those kills were mislabeled `gl` with a phantom "2 scoops of X"
//     killer; now `ssg` with the real killer name.
//   - match.players/match.teams no longer drop players who finished on
//     exactly 0 frags (surface-authoritative-data); and duel detection trusts
//     demoInfo.players as authoritative, so a 2on2 in which two players end
//     on 0 frags is no longer misclassified as a duel and team-renamed.
//     Paired reader fix so spectators don't leak in instead: the full
//     userinfo parser now reads the server-set `*spectator` star key
//     (mvdsv strips the bare `spectator` key before broadcast) and resets
//     the flag on every full update, ezquake-style.
//   - Powerup interval end times use the same effective match end as the
//     weapon intervals on demos cut before intermission (were the per-player
//     last sample vs the global max).
//
// v49: aim/shots correctness fixes (no field shape change).
//   - aim.players[].weapons rl/gl direct/splash/missed is present on every
//     default parse: the block was gated on the opt-in streams.projectiles
//     emission, while the projectile linking it actually needs runs on every
//     parse — it now gates on linking evidence (any linked rl/gl fire).
//   - The damage records feeding aim's pellet and direct splits are windowed
//     to match time [0, matchEnd]: warmup and post-match damage no longer
//     inflates direct (and deflates splash).
//   - Duel damage classification: in a 1v1 where both players share a
//     non-empty colour team, damage.events[].isTeam was true for every hit
//     on the opponent — contradicting the duel-normalized shots victimKinds,
//     silently emptying timelineAnalysis.airgibs and zeroing the aim enemy
//     splits, and folding all given damage into givenTeam (empty matrix,
//     empty victimWep buckets). DamageAnalyzer now classifies duel hits as
//     enemy at birth, so events, aggregates, matrix and EWep buckets are
//     consistent with the rest of the duel-normalized result.
//   - Shots identity resolution uses the canonical ResolveSlotAt chain,
//     which backfills an empty team from the demoinfo name table even when
//     the name resolved (parity with damage/frags).
//   - match.players[].frags no longer clobbered by a post-match reconnect:
//     the svc_updatefrags scoreboard is frozen at match end, so a slot
//     re-init to 0 during intermission cannot erase the final score (the
//     v48 removal of the 0-frag filter had surfaced these corrupted zeros
//     as if they were real scores).
//
// v50: damage.events is now match-gated at the source.
//   - The per-hit damage.events log previously carried out-of-match (warmup /
//     post-match) hits while the aggregates gated them out. The analyzer now
//     drops out-of-match hits before appending to events, so the events log
//     and the aggregates are built from the same in-match hit set. This
//     removes the aim [0,matchEnd] self-window (v49) — aim reads exactly-in-
//     match damage — and fixes a latent airgibs bug that counted warmup /
//     post-match rocket airgibs (it iterated events with no gate). No field
//     shape change; damage.events arrays shrink by the out-of-match hits.
//
// v51: the match opening becomes first-class (PLAN-api-usability 16.1-A).
//   - streams.players[].sp gains the match-start spawn. KTX respawns every
//     player when the countdown ends (SM_PrepareClients → k_respawn,
//     ktx/src/match.c:881,972), but a player alive through the countdown
//     never crosses health ≤0→>0, so the parser's dead→alive detector
//     missed the first — most contested — spawn of the match. The timeline
//     now synthesizes a t=0 spawn for every player alive at match start
//     whose respawn wasn't wire-visible.
//   - Adds Result.Opening ("opening" artifact): each player's match-start
//     spawn location plus the first in-match take of every contested
//     spawner (armors, mega, powerups, RL/LG) — a pure projection of
//     items + streams kept small for one-call fetches.
//   - The events view (not stored, documented here for the contract) gains
//     the default "pickup" type — identity-rich pickups joined from
//     items[].phases (world takes, per-spawner ya_1/ya_2 naming) and
//     weaponPickups (backpack/unknown grants) — and spawn events now carry
//     the spawn location in detail.
//
// v52: no-match-start demos are flagged, not coerced.
//   - streams.global gains timeBase: "demo" (omitted normally) when no match
//     start was detected. On such demos the per-producer rebase never runs,
//     so every timestamp in the Result is on the raw demo clock — previously
//     indistinguishable from a match-rebased result. A matching entry is
//     appended to errors[] so /overview surfaces it without a new field.
//
// v53: columnar buckets become loc-self-contained (view shape only — no
//
//	stored field changes; bumped so the immutable schemaVersion-keyed
//	ETags stop revalidating the pre-legend bodies).
//	- The /buckets layout=column envelope gains locTable: the demo's
//	  interned loc-name legend, present iff an "li" column is in the
//	  output. Columnar keeps the compact raw index (unlike row mode,
//	  which resolves names per bucket); the legend lets a consumer —
//	  notably an MCP agent on the columnar default — decode locally
//	  instead of a /loc-table round trip.
//
// v54: the bounded damage family (additive).
//   - The wire carries only KTX's UNBOUND damage (overkill-inclusive,
//     ktx/src/combat.c:795); the scoreboard's BOUNDED dmg_dealt (armor
//     absorbed + health damage capped to remaining health, combat.c:783)
//     is now reconstructed per hit from tracked victim armor/health state.
//     damage.events[].bounded (omitted when equal to damage; 0 is a real
//     value — a pent/teamplay-nullified hit), damage.byPlayer.<p>.bounded
//     (a nested PlayerDamage mirroring the damage figures), and
//     damage.scoreboard deltas gain a bounded nest incl. streamTeam /
//     scoreTeam (dmg.team reconciliation only becomes meaningful with the
//     bounded family). damage.dmg ("both") and damage.boundedMode
//     ("standard", or "skipped:midair|instagib|dmgfrags" when the server
//     mode rewrites T_Damage unobservably — no bounded fields then).
//   - Telefrags and stomps fold their BOUNDED damage into given/givenTeam/
//     taken in both families (telefrag: armor+health — the wire 9999 is a
//     sentinel; stomp: the honest ~10 HP wire value through the normal
//     arithmetic), matching KTX's own accumulation (combat.c:1046-1076
//     has no tele/stomp exclusion). telefrags[]/stomps[] entries carry the
//     per-kill bounded value. ByWeapon/Matrix/EWep/TotalDamage still
//     exclude them (KTX wpNONE — demostats weapons[].damage excludes them
//     too).
//
// v55: bounded damage becomes death-value-exact (reconstruction change only).
//   - No field-shape change. The bounded value no longer caps the health
//     share against a drifting per-hit health shadow.
//   - A SURVIVED hit is bounded == raw by identity (no overkill); a KILLING
//     hit's overkill is the end-of-frame death broadcast, so bounded is raw
//     plus the (negative) death value (armor cancels; combat.c:944,983).
//   - Residual approximations remain: same-frame multi-hit deaths cascade
//     one death value across the frame's hits (approximate save split); the
//     -99 corpse-health clamp (combat.c:259) and respawn-masked deaths fall
//     back to the shadow-health cap.
//   - Corpus given/taken reconcile ~2.5× tighter (max |Δ| 16/15 vs 44/44);
//     ewep/team bands unchanged (the victim-item one-frame window).
//
// v56: REST time-unit echo (transport-surface only — no stored change).
//   - Every demo endpoint carrying a descriptively-named MATCH-POSITION
//     timestamp echoes a top-level `timeUnit` naming that endpoint's FIXED
//     native unit — "ms" (int32 milliseconds) for the pass-throughs
//     frags/damage/shots/chat/airgibs/backpacks/weapon-pickups/items-timeline/
//     overview, "s" (float64 seconds) for the derived views events/
//     buckets-rows/state-at/stream-slice-envelope/loc-trails/items-summary.
//     There is NO unit selection: the sparse `t` (int32 ms) and `time`
//     (float seconds) fields always carry those units, and every other
//     match-position field carries the endpoint's native unit named by the
//     echo. This polarity is exception-free — the stored ms event lists and
//     the dense per-sample arrays both use `t`-in-ms (the dense arrays
//     always did; compact name = compact type), while the float-seconds view
//     surfaces use `time`. DENSE per-sample payloads always stay ms (aim
//     crosshair `t` / lgRamp `since`, stream-slice embedded tracks, columnar
//     buckets startMs/windowMs axis); /aim echoes "ms" (its dense samples).
//     Only /demoinfo (KTX units island) and /artifacts (raw stored bytes)
//     are echo-exempt. The four bare-array
//     bodies (chat/airgibs/backpacks/weapon-pickups) gain a {timeUnit,
//     <list>} envelope so the echo has a home.
//   - Time-field polarity flip: every stored ms field formerly tagged
//     `json:"time"` is now `json:"t"` (frag/damage/shot/chat/backpack/
//     weapon-pickup/opening/airgib/timeline events); the seconds view
//     surfaces flip `json:"t"`→`json:"time"` (events/buckets-row/state-at/
//     items-summary firstTake); loc-trails residences rename `s`/`e`→
//     `start`/`end`. Go field names are unchanged (only the JSON tags).
//     The golden corpus was regenerated for the key renames.
//
// v57: pure-ms time model + bound renames + null→[] (breaking sweep).
//   - PURE-MS MODEL. Every time value in the API is int32 milliseconds —
//     inputs and outputs, REST and MCP alike. The six v56 float-seconds view
//     surfaces (events, buckets rows, state-at, stream-slice envelope,
//     loc-trails, items summary) flip to int32 ms; the view layer now does NO
//     float time math. Time-valued query params (`from`/`to`/`time` on every
//     demo endpoint, REST and MCP) become INTEGER MILLISECONDS — a non-integer
//     like `from=10.5` is rejected with a "(integer milliseconds)" hint (the
//     v56→v57 tripwire). `view.UnitSec` is deleted; `timeUnit` stays as a
//     CONSTANT "ms" echo everywhere it appears. Exceptions, documented:
//     /demoinfo (KTX-native seconds island) and search `from`/`to` (calendar
//     dates YYYY-MM-DD). Two migration tripwires: `from`/`to` inputs are now
//     ms; and the v55 float-seconds view surfaces (events/buckets-row/state-at/
//     items firstTake) are now int32 ms under the SAME key they carried in v55
//     — `time` — so a v55 reader that still divides by 1000 (or reads the v56
//     `t`, now gone) breaks loudly instead of silently.
//   - PER-ITEM TIME KEY: the DENSE/SPARSE split (final v57 naming). Sample-
//     rate-scaled arrays (native-cadence stream tracks, columnar sample columns, aim
//     sample arrays) use the terse `t`; event-scaled sparse lists and singleton
//     timestamps use the descriptive `time`. This is the v55 layout: every
//     stored sparse list (frags/damage/shots/chat/backpacks/weapon-pickups/
//     opening/airgibs/timeline events) kept `time` unchanged from v55, so v55
//     clients of those lists keep working; the derived view surfaces (events,
//     row-major buckets, state-at, items firstTake) — seconds in v55 — become
//     int32 ms but ALSO under `time`, dropping the transient v56 `t` for good.
//     Both keys are ALWAYS int32 ms; the unit is NEVER encoded in the name (the
//     `timeUnit` echo names it). DENSE terse keys DELIBERATELY kept: PositionTrack
//     `t`, ChangeI16/ChangeStr `t`/`v` (the stream-slice change columns),
//     result.Interval `s`/`e`, aim `t`, projectile/beam `s`,`sx`…`e`,`ez`.
//     (Columnar BUCKETS carry no time key at all — their axis is the
//     implicit start + i*windowMs.)
//   - KEY RENAMES (JSON tags; Go field names mostly unchanged). LosTrack
//     `o`/`iv` → `other`/`intervals` (once-per-track, descriptive);
//     MessagesResult array key `events` → `messages`; ColumnarBuckets `startMs`
//     → `start`; stream-slice envelope `startTime`/`endTime` → `start`/`end`.
//   - null→[] for governed top-level view arrays (events, stream-slice
//     players, loc-trails players) — never null when empty. Nested nullables
//     and the documented-null projectiles/beams/nails objects are untouched.
//   - Cross-phase v57 items (see RELEASE_NOTES): `unknown_param` 400 on
//     unknown query keys/enum values; `los_unavailable` 422 (no-BSP /los, never
//     persisted/latched); `server_hostname` in search rows; `/artifacts`
//     echoing `timeUnit:"ms"`.
//
// v60: match scoreboard built per slot occupancy (values only; no field
// changed). `match.players` / `match.teams` are keyed on wire-slot
// OCCUPANCIES rather than on the slot's final occupant, and participation
// is evidence of play inside the match window rather than end-of-demo
// spectator/team state. Four consequences on real demos: a player who goes
// spectator after the match keeps their row and score; a player who leaves
// mid-match keeps the score the server announced for them instead of the
// zero the drop wrote; FFA rosters survive (an empty team is not a
// spectator signal); and a connection the server refused no longer takes
// the departed player's scoreboard row. Per-slot item possession is also
// reset at a handover, so `streams.players[].rl|lg|gl|ssg|sng|quad|pent|
// ring` no longer leak a departing player's inventory into the next
// occupant. See RELEASE_NOTES.md.
//
// v61 adds PlayerStats: the canonical per-player (and per-team) statistics
// section, computed for EVERY demo, with per-stat-family provenance
// ("derived" | "ktx"). Additive — no existing field changed shape.
//   - It carries the join that consumers previously re-implemented: the
//     corrected scoreboard, the damage reconstruction, the pickup tallies
//     and the KTX identity fields in one row per player.
//   - It adds POSSESSION TIME, which no prior surface had: time with each
//     weapon, each armor type, and with NO armor, as exact integrals over
//     the native-rate streams, with explicit denominators (match / present
//     / alive ms). KTX never writes weapon hold time to the demoinfo block
//     at all, and its armor hold time overcounts (the clock closes only on
//     death or a different-type pickup, never when armor is chewed to
//     zero) — so these figures are ours, and read lower than a KTX
//     end-of-match table by design. See result/player_stats.go.
//   - /demoinfo is unchanged: it stays the verbatim KTX pass-through, the
//     audit trail this section is diffable against.
//
// v62 amends v61 before it ships: the playerStats section learns to say
// "not measured" for whole families rather than serving a confident zero.
//   - score's KILL SIDE is optional. kills / suicides / teamKills /
//     byWeapon / efficiency are attributed from the obituary-derived frag
//     log and are omitted together where it measured nothing on a demo
//     whose players demonstrably died; frags and deaths, which are
//     measured on every demo, stay. Efficiency and ping become pointers,
//     and members (team rows) is a pointer so it survives at 0.
//   - hold.armor is omitted entirely when the armor stream carries no
//     sample, instead of reporting a full-match "no armor".
//   - damage.src gains "derived:unbounded" for the k_midair / k_instagib /
//     k_dmgfrags demos where no bounded reconstruction exists and the
//     figures are raw wire damage including overkill.
//   - damage is emitted, zeroed, for a player who dealt and took nothing
//     on a demo that carries the damage stream (an observed zero).
//   - team rows carry accuracy (summed per weapon; hits absent unless
//     every member measured it), which they never did.
//   - sources is computed from the rows being served, after filtering,
//     and can read "mixed" as a phantom-roster canary.
//
// v63 splits per-weapon damage by victim class. Additive — no existing
// field changed shape or meaning.
//   - PlayerDamage (damage.byPlayer, raw family and bounded nest alike)
//     and PlayerStatsDamage gain byWeaponTeam and byWeaponSelf beside
//     byWeapon: the same attacker-weapon keys, splitting givenTeam and
//     givenSelf the way byWeapon splits given. Telefrags and stomps stay
//     out of all three (positional kills fold into the totals only).
//   - The KTX overlays stamp byWeaponTeam from weapons[].damage.team,
//     which KTX has always written beside .enemy and nothing consumed —
//     so a bounded summary or a playerStats row badged src/boundedSource
//     "ktx" now serves KTX's own team split rather than a reconstruction
//     under a KTX badge. byWeaponSelf has no KTX counterpart and stays
//     derived.
//   - MEASUREDNESS is family-level and documented rather than inferred
//     from omitempty: byWeapon and byWeaponTeam are measured whenever the
//     damage family is present; byWeaponSelf only where a damage stream
//     was read, which is what a non-nil damage.taken says. Within a
//     measured family an absent key means "dealt none with that weapon".
//
// v64 — canonical liveness, and alive-gated exact loc/region occupancy.
// A CORRECTNESS FIX: values move in locGraph and timelineAnalysis.
// regionControl; no existing field changed shape, name or meaning.
//
//   - streams.players[].alive (new, NOT omitempty): each player's lives as
//     half-open [s,e) intervals, derived from the fused spawn/death markers.
//     The one canonical answer to "was this player alive at t". Three states:
//     null = liveness not measurable, [] = measured and never alive, [...] =
//     the lives. A player who never died is one full-match interval, so
//     absence can never read as "alive throughout".
//   - locGraph node time is now an EXACT time-weighted integral over the
//     union of position-sample times and RL/LG/quad/pent/alive interval
//     endpoints, replacing a forward difference clamped to 50 ms. Posture is
//     split at interval boundaries instead of snapped to sample instants, so
//     a pickup landing between two samples divides the interval exactly.
//   - locGraph AND regionControl now EXCLUDE DEAD PLAYERS. Dead players keep
//     streaming position at full rate (mvdsv writes svc_playerinfo for every
//     cs_spawned client, sv_demo.c:1481-1519), and on a gib the player entity
//     itself is the bouncing head (ktx/src/player.c:1070 ThrowHead), so both
//     were crediting a corpse's travels as presence — and as ARMED presence,
//     since StatItems weapon bits do not clear until respawn. Expect region
//     percentages and byPlayer ms to fall by roughly (deaths x
//     death-to-respawn time) per player, and phantom "teleport" edges thrown
//     by bouncing gib heads to disappear from locGraph.edges.
//   - Both walks now bound sample-and-hold: a sample's evidence expires after
//     result.SampleStaleCapMs (250 ms), and a track ends at result.TrackHoldEnd.
//     This is what the deleted 50 ms clamp had really been doing. It matters
//     most on POV (client) recordings, where only players inside the recorder's
//     PVS get svc_playerinfo: measured on a POV demo the recorder had a 152 ms
//     worst gap while the other seven players had gaps up to 73 SECONDS, and
//     holding across them credited ~92% of a player's loc time to wherever they
//     stood when they left view. Inert on server recordings (worst golden gap:
//     74 ms). It also stops an early quitter holding their loc/region to match
//     end — region control evaluated intervals at their left endpoint, so a
//     departed player's final sample was credited to the next event.
//     locGraph EDGES take the same bound: no transition is recorded across a
//     gap longer than SampleStaleCapMs, which previously minted a kind
//     "normal" adjacency between the locs bracketing a PVS hole (past
//     locgraphTeleportMaxGapMs the displacement check cannot label it, so a
//     consumer filtering teleports out kept exactly the invented edges).
//     /loc-trails carries the same two bounds, and its minDwellMs fold never
//     merges across the gaps they cut.
//   - The loc-graph teleport threshold is scaled by the REAL inter-sample
//     delta instead of an assumed 50 ms. The MVD sample rate is NOT fixed:
//     mvdsv gates demo frames on sv_demofps (default 30), so measured cadence
//     across the golden corpus is bimodal — ~13-16 ms on servers at full tick
//     and ~34-39 ms on servers at the default.
//
// v65 — interval segmentations: top windows and lives. ADDITIVE. Both are
// VIEWS over data that was already there (view/topwindows.go, view/lives.go,
// sharing the stats builder in view/interval_stats.go); the stored Result
// gains exactly one field, FragResult.KillsMeasured, so that the demo-global
// kill-attribution verdict is decided once by the analyzer instead of being
// re-derived by every consumer that needs it.
//
//   - /top-windows serves each player's best fixed-length stretches, ranked
//     by a caller-chosen SUMMABLE metric (frags, deaths, netFrags,
//     damageGiven, damageTaken, netDamage, shots, hits). Ratios are absent
//     because "the best window" is undefined for a quantity that does not
//     sum. Windows are anchored at real event times rather than a grid,
//     non-overlapping per player, and returned as one flat ranked list.
//     weapons= scopes the SCORING events only, so a window's score can be a
//     subset of the same-named field in its stats block; the envelope's
//     scoredBy {metric, weapons, dmg} is what tells the two apart, and is the
//     ONLY place the metric is echoed.
//   - /lives serves one row per spawn-to-death run, segmented by the v64
//     streams.players[].alive intervals. A player's lives PARTITION
//     [MatchStart, MatchEnd]: each life is attributed every event from its
//     own start to the start of the next, so a posthumous rocket counts for
//     the life that fired it and per-life sums reconcile exactly with match
//     totals. durationMs stays ALIVE time, which is the one asymmetry.
//     deaths is therefore NOT capped at 1 (the KTX dtTELE2 deflection lands a
//     death inside the dead gap; measured 12 rows with 2 and one with 3
//     across 11558 cached-corpus lives), and endReason
//     (death | matchEnd | leftGame) exists because an absent killedBy used to
//     conflate all three.
//   - Both responses carry a measured {frags, damage, shots, locs, items,
//     liveness} block on the envelope, and echo the damage family they were
//     computed in (dmg + boundedMode, exactly as /damage does — the stats
//     block reports damage under every metric, so the family is a property of
//     the response, not of the metric). MEASUREDNESS IS READ FROM THE BLOCK
//     AND NEVER FROM A FIELD'S ABSENCE: every numeric stat is emitted
//     including a measured zero, so `damageGiven: 0` alone says nothing about
//     the demo. measured.frags is the stored FragResult.KillsMeasured verdict,
//     not merely "a frag section exists"; measured.liveness distinguishes
//     "liveness was not measurable" from "never alive", and /lives 422s
//     (lives_unavailable) rather than serving an empty list for the first.
//   - Positional-kill (telefrag / stomp) value FOLDS into the stats block's
//     damageGiven / damageTaken exactly as /damage reports it, so lives
//     reconcile against that endpoint, AND it scores for the damage metrics,
//     so that absent a weapons= filter a top window's score equals the
//     same-named field of its own stats block. (The values are the analyzer's
//     reconstruction — 0..298, median 100 across the cached corpus — not the
//     9999 wire sentinel an earlier round mistook them for.) It stays out of
//     damageByWeapon, which carries no key for a weaponless kill, matching
//     /damage's byWeapon.
//   - Each /lives row carries its ATTRIBUTION SPAN (attrStart/attrEnd): the
//     event fields cover the life plus the dead gap after it, while durationMs
//     is alive time, so a rate divided by durationMs reads high.
//   - The frag and damage weapon vocabularies now accept `water` and `drown`
//     interchangeably: it is one event the two logs spell differently, and a
//     caller should not have to know which log backs which metric. Purely
//     additive; no emitted token changed. The alias is expanded by the one
//     filter-set builder every weapons= filter uses (view.weaponFilterSet), so
//     /frags?weapons=drown and /damage?weapons=water MATCH rather than
//     returning an empty result.
//   - FragResult.KillsMeasured (the only stored-shape change): the demo-global
//     "kill attribution was observable" verdict, published by the match-final
//     node. False means an empty frag log on a demo whose scoreboard records
//     deaths — every obituary went unmatched — where a row of measured zeros
//     beside a real death count would look like a measurement.
//
// v66:
//   - Every reported userid is now the SESSION's, not the wire slot's.
//     TimelineAnalysis.PlayerUserIDs and the userid on fragStreaks,
//     powerupEvents, demoMarkers and airgibs used to carry the first
//     userid ever seen on the slot, latched for the whole demo, so any
//     slot handover or reconnect published an id belonging to a different
//     connection — a hub `track=<id>` link then followed the wrong player
//     (gameId 220637: `(1)rusti (FU)` served as 42, a spectator who left
//     after 26 s) or a connection that no longer existed (222649:
//     `bogojoker` served as 12, sixteen minutes after he timed out and
//     came back as 25). Same field names, same types, same documented
//     meaning; the numbers were wrong and are now right.
//   - Where one player holds several sessions (a reconnect the identity
//     unifier folded into one name), PlayerUserIDs reports the LAST
//     session that had play — normally the id that is live at the end of
//     the demo and the one a `track=` resolves; the ranking is by last play
//     evidence, so an exact tie in it resolves to the lower slot rather
//     than to the surviving connection. The event carriers each report the
//     session that held the slot at their own timestamp.
//   - ADDITIVE: Streams.Players and PlayerStats.Players gain Identity and
//     Sessions. Identity is the reconnect-unification key the pipeline
//     already used internally to merge a reconnected player's streams —
//     equal on every row that is the same human, so a consumer can relate
//     the two rows a `(N)<name>` rename produces without a name heuristic
//     (which is what one had resorted to). It is DEMO-LOCAL (derived from
//     the first session's slot+userid) and must not be persisted; the
//     cross-demo identity is the authenticated login. Sessions is the
//     per-connection {StartMs, EndMs, Slot, UserID, Name} window list —
//     the lossless form of PlayerUserIDs, carrying the OBSERVED occupancy
//     bounds rather than the ±inf-widened ones the internal resolver uses,
//     and omitting occupancies the wire never gave a userid (KTX's ghost
//     scoreboard row is not a connection). See PlayerSession.
//
// v67 — kill bursts (/top-kills) and the `top-` endpoint family. THE STORED
// RESULT GAINS NO FIELD: nothing in this package changes shape, name or
// meaning, and the bump is for the observable API surface alone (it is the
// ETag / X-Schema-Version cache key, so it ticks on every observable change).
//
//   - /top-kills serves the match's hardest kill BURSTS, ranked by burst
//     damage — a third segmentation over the same primitive (view/topkills.go),
//     but the only one that is not an interval reduction, so it fills no
//     IntervalStats: a burst row is a small backward walk over the damage log.
//     For each enemy kill the burst is the contiguous run of KILLING-WEAPON
//     hits the killer landed on that victim, clipped below by the victim's
//     current life start. Same-weapon by design: on ~8% of measured kills that
//     UNDERSTATES what produced the kill (a rocket softens, a shotgun
//     finishes), which is documented endpoint semantics — "how hard was this
//     burst with this weapon" — with /damage still answering the cross-weapon
//     question.
//   - gapMs (default 3000, max 5000) is a CAPTURE gap, not a display one:
//     truncation is unrecoverable downstream while over-merge is filterable, so
//     the capture is generous and each row carries maxGapMs, the EXACT
//     client-side narrowing filter (a kept row carries its gap-g value
//     verbatim; an over-merged row is dropped, not truncated — gapMs=g on
//     the endpoint is the exact walk). spanMs is the display figure and
//     cannot express that. Positional kills (telefrag / stomp / squish) carry
//     no damage event and so produce NO ROW — absent from this ranking only,
//     still present in /frags and /damage. Kills by an already-dead killer
//     stay IN: the walk consults the victim's liveness and never the killer's.
//   - The response echoes the resolved gapMs / contestedMs / limit, the
//     measured block, and dmg + boundedMode, exactly as the v65 segmentations
//     do. A demo with no measurable liveness is a 422 top_kills_unavailable
//     rather than a plausible-looking list: at the 3000 ms capture gap the walk
//     crosses the victim's previous death on 4% of measured kills, and the
//     contaminated rows are precisely the ones that rank highest.
//   - ADDITIVE on /overview: a topKills field carrying the same rows at the
//     documented defaults, 20 of them rather than its neighbours' 5, because
//     the per-weapon narrowing a consumer runs off that one call leaves 16-20
//     of a top-20 and only 6-10 of a top-10. Omitted when the demo cannot
//     answer the query. And an mvd-mcp getTopKills tool, lockstep as always.
//   - ORDERING CHANGE on /top-windows: rows tied on `score` — the common case,
//     since most of a metric=frags page holds the same small integer — now
//     rank by a FIXED complementary metric (damageGiven under
//     frags/netFrags/shots/hits, frags under damageGiven/netDamage,
//     damageTaken under deaths, deaths under damageTaken) before the
//     positional keys, and the same key picks among a player's overlapping
//     equal-scoring candidates, so a window may end rather than start on the
//     scoring event. No new parameter and no new field: the secondary is
//     summed unscoped in the response's own damage family, which makes it
//     exactly the same-named field of the row's stats block.
//   - RENAME, a deliberate compatibility break: /hot-windows becomes
//     /top-windows, its 422 code hot_windows_unavailable becomes
//     top_windows_unavailable, and the MCP tool getHotWindows becomes
//     getTopWindows. Ranked-highlight scans now carry an explicit `top-`
//     prefix (they have a limit, a min-filter and a sort key) while plain
//     nouns stay reserved for exhaustive logs and partitions — /frags,
//     /damage, /lives — so the split carries information instead of being an
//     accident of history, and overview.topX pairs with /top-x. Taken while
//     the consumer count was ONE, with no alias route: an alias on a one-user
//     API becomes permanent undocumented surface. No response shape changed
//     beyond the paths themselves. NOTE the v65 text above is written in the
//     new names, so it describes the endpoint as it is now rather than as it
//     shipped.
//
// v68 — gap-delimited windows: `mode=gap` on /top-windows. THE STORED RESULT
// GAINS NO FIELD, for v67's reason: nothing in this package changes shape, and
// the bump is for the observable API surface alone.
//
//   - /top-windows gains a second SEGMENTATION behind `mode` (default `fixed`,
//     so a pre-v68 request answers identically apart from the additive
//     `mode:"fixed"` echo every response now carries). Under
//     `mode=gap` a window is a maximal run of scoring events in which
//     consecutive events are no more than `gapMs` apart, and its score is
//     their sum — the stretch lasts as long as the player kept doing it
//     rather than as long as a stopwatch says. It is NOT the adaptive
//     segmentation dropped during planning: Ruzzo-Tompa needs a per-second
//     penalty to stop the whole match being one segment, and that penalty is
//     exactly the unexplainable constant this rule does without.
//   - `gapMs` is REQUIRED under mode=gap and has NO default. Measured over the
//     44-demo cache, per-player inter-kill gaps run p50 ~11-12 s while
//     inter-damage-event gaps run p50 ~1.0-1.1 s, so no single value serves
//     both: documented starting points are ~10000 for the frag metrics and
//     ~3000 for the damage and shot metrics. Each mode REJECTS the other's
//     knob with a 400 rather than ignoring it.
//   - ADDITIVE on the /top-windows envelope: `mode` (always present, so a
//     consumer never infers the segmentation from which knob is present) and
//     `gapMs` (gap responses only); `windowMs` becomes fixed-only, since on a
//     gap response a fixed length would be a lie. Rows need no new field —
//     start/end already describe variable-length spans, and a gap row's `end`
//     is its last scoring event rather than start+windowMs.
//   - Gap clusters are disjoint per player by construction (no overlap
//     suppression), signed metrics cluster on ALL their events (a death both
//     extends a netFrags run and lowers its score), and a cluster MAY SPAN the
//     player's own death — /lives stays the per-life view.
//
// v69 — the victim-weapon axis: `byEnemyWeapon` on kills and damage.
// Every per-weapon figure in playerStats was keyed on the ATTACKER's
// weapon. This adds the complement: the same kills and the same damage
// split by what the VICTIM was holding when it landed — weapon denial.
//   - PlayerStatsScore.ByEnemyWeapon partitions Kills;
//     PlayerStatsDamage.ByEnemyWeapon partitions Given. One exclusive
//     vocabulary (VictimWeapon*): both / rl / lg / mid / sg, plus
//     `unknown` on the kill side for a victim with no stream.
//   - "Enemy RLs killed" is rl + both, NEVER rl alone.
//   - DERIVED on every demo carrying streams, never overlaid. KTX's own
//     ekills counts the kill side INCLUSIVELY and force-zeroes axe/sg plus
//     every bucket on deathmatch >= 4 / k_instagib
//     (ktx/src/stats_json.c:377-380); for damage the server keeps only the
//     RL+LG-lumped dmg_eweapon scalar. Ours reproduces KTX exactly where
//     KTX measures honestly (rl + both == ekills.rl on all 44 cached
//     demos) and additionally covers telefrags, stomps and demos with no
//     demoinfo block.
//   - Score.ByWeaponVsEnemyWeapon is the JOINT distribution the two kill
//     maps are marginals of (killer weapon -> victim bucket -> kills), for
//     the question marginals cannot answer: how many of my LG kills were
//     against enemies carrying an RL. Summing it reproduces both marginals
//     exactly — guaranteed, since the marginal is summed from it.
//   - Measuredness splits: the kill map is absent exactly when Kills is;
//     the damage map needs the damage STREAM and is present exactly when
//     Taken is.
//   - Computed in the ANALYZER, so unlike controlMs / speed the stored
//     Result changes and the golden corpus moves.
//
// v70 — /overview becomes a capability manifest instead of a highlights
// reel. NO stored Result field changes; this is an mvd-api response shape
// bump, and a BREAKING one (fields are removed, not added).
//   - REMOVED: overview's `topKills`, `topStreaks`, `topPowerups`. Measured
//     across the corpus they were 78-88% of the response, topKills alone
//     62-77%, and every one of them was a copy of a dedicated endpoint:
//     /top-kills at its own defaults, and /lives + /events?type=streak,powerup
//     field for field.
//   - REMOVED: `hasRegionControl`, folded into the manifest below.
//   - ADDED: `available`, one flag per detailed view, each mirroring the
//     predicate behind that view's 422. Includes the three signals a
//     consumer could not previously infer AT ALL, because they turn on which
//     BSPs the server was provisioned with rather than on what the demo
//     recorded: `height`, `liquid` and `los`. There is deliberately no
//     separate `pvs` flag — PVS and LOS share one pass and one BSP gate
//     (PVS is a superset of LOS by construction), so the two could never
//     disagree.
//   - A drift test pins the manifest to the 422 table, which is what the
//     removed ad-hoc has* fields never had and why they went stale.
//
// v71 — reconstructed damage for pre-instrumentation demos + damage
// provenance.
//   - ADDED: DamageResult.Source ("ktx" | "reconstructed") — where the
//     damage log came from. The KTX analyzer stamps "ktx" on every demo it
//     decodes (a stored-Result change: goldens move); the new damage-recon
//     post-processor stamps "reconstructed".
//   - ADDED: the damage-recon DAG node (package mvd-analytics/damagerecon).
//     On demos whose wire carried no mvdhidden_dmgdone stream (~45% of the
//     archive: res.Damage was absent and /damage 422'd), the damage section
//     is now reconstructed from the health/armor change streams + LG beams,
//     projectile flights, fire sounds, position/velocity tracks and the
//     frag log — raw AND bounded families, same shapes, same match window.
//     Wire-measured sections are never touched; consumers distinguish the
//     two by `source`. Validation against KTX ground truth on modern demos:
//     see mvd-analytics/damagerecon/ACCURACY.md.
//
// v72 — match-start wall-clock anchor from the wire date markers.
//   - ADDED on `streams.global`: `matchStartUnixMs`, `matchStartAccuracyMs`,
//     `matchStartSource`, `matchStartConfidence`, `matchStartNote`,
//     `matchEndUnixMs`, `dateMarkers[]`, and `demoStartSource`. The anchor is
//     resolved by the new `wall-clock` DAG node from the `matchdate:` /
//     `matchkey:` broadcast prints, the ktxstats `date` string, and the
//     serverinfo version keys.
//   - CHANGED: `demoStartUnixMs` / `demoStartAccuracyMs` are now also derived
//     from a date marker (back-shifted by `demoOffset`) on demos that carry no
//     mvdhidden 0x000B block and no `epoch` cvar — which lifts wall-clock
//     coverage from ~25% of the archive to ~98%. `demoStartSource` says which
//     source a given result used, and `demoStartAccuracyMs` states the cost:
//     50 400 000 when the marker named no timezone.
//   - Anchors are never dropped on a failed trust check: `matchStartConfidence`
//     grades them ("exact" / "unverified" / "contradicted") and
//     `matchStartNote` names the check. Only a "contradicted" stamp is kept out
//     of `demoStartUnixMs`.
//   - ADDED at the top level: `parseWarnings` — the reader's own census of
//     what it could not decode off the wire (unknown svc_* / temp-entity /
//     hidden-message types, failed payloads). Collected on every run now,
//     not just in the diagnostic harness, and omitted when the parse was
//     clean. Distinct from `errors[]`, which reports analyzer-level failures
//     over events we DID read. See ParseWarnings.
//
// v73 — airgib detection gates on pre-impact evidence (a correctness
// fix: entries move in and out of `timelineAnalysis.airgibs`, no field
// is added, removed or retyped) plus a `preMs` echo on the /airgibs
// envelope.
//   - KTX writes the damage message inline in T_Damage; measured over 410
//     direct rocket hits, the stamp lands in the same wire frame as the
//     first knockback-visible position sample 82% of the time and up to
//     two frames (+28ms) late 6% — so samples near the stamp can already
//     carry the rocket's own knockback. Measured case (hub 232925, dm2
//     4on4): a player riding the dm2 func_train (top at z=319) took a quad
//     direct rocket that blasted him off it and the hit-time sample read
//     303 units of air — published as the match's biggest airgib.
//   - A hit now qualifies when every position sample in the look-back
//     window [hit - preMs, hit - 40ms] (default 100ms) reads >= the
//     96-unit threshold — the preceding tick deciding when the window
//     holds no sample (old coarse-tick demos, recording holes) — and no
//     sample beside the hit reads ground contact. Contamination is
//     one-sided: knockback over-reports height but cannot fake a grounded
//     reading, so a victim who landed just before the rocket rejects
//     while one knocked laterally over a higher floor is kept. The 100ms
//     default is aesthetic: floor-relative height is a step function at
//     ledge edges, so longer windows measure time-since-the-edge and
//     drop genuine 300+-unit ledge-drop events. Reported `height`, `loc`
//     and `heightAboveAttacker` come from the latest PRE-IMPACT sample.
//   - Detection moves from the analyzer post-processor into
//     `view.ComputeAirgibs`, a pure function of the assembled Result (the
//     regionControlPost / view.RegionControl staging). The post-processor
//     bakes the default-options run into the stored Result; mvd-api's
//     /airgibs re-runs it per request with `?preMs=` (0..1000, 0 = the
//     pre-v73 hit-sample-only rule) and echoes the effective value as
//     `preMs` on the response envelope.
//   - Per-hit userids now resolve against the PUBLISHED per-stream session
//     table (`streams.players[].sessions`) rather than an analyzer-internal
//     index; same answers, one clock.
//   - Airgibs now consume reconstructed damage too (the DAG node binds
//     `damage:final`), superseding v71's wire-measured-only gate for this
//     view: recon's direct/splash split is geometric (explosion endpoint
//     within 48 units of the victim) and frame-accurate, the fidelity the
//     verdict needs. Aim keeps the ktx-only gate. `damage.source` says
//     which evidence a demo's list rests on. (The 48-unit endpoint rule
//     named here was replaced in v74 by the trajectory classifier — see
//     that block below; airgibs consume whatever the split says.)
//
// v74 — the fire→flight association reaches the Result, and with it rl/gl
// hit recovery on reconstructed demos.
//   - ADDED on `shots.shots[]`: `flightEnd` — the match-relative time at
//     which the tracked rocket/grenade/nail a fire launched died. The shots
//     analyzer has always bracketed those flights (it is how `hit` is
//     decided for a projectile), then discarded the association; it is now
//     published. Absent on hitscan fires and on a projectile fire whose
//     entity was never broadcast (or, for ng/sng, on a parse without nail
//     decoding), which is exactly the state the measured counter reads as a
//     miss.
//   - CHANGED: the reconstructed aim tier covers `rl` and `gl` —
//     `aim.players[].weapons[].recon.hits` now appears for them on demos
//     whose damage section is reconstructed. The join follows the measured
//     definition through `flightEnd` (flight impact instant → reconstructed
//     damage of that attacker+weapon) instead of counting impacts, which is
//     what made the two conventions differ by ~7pp on rl in v73. Measured
//     over 53 dm2/dm3 demos carrying the KTX log: mean accuracy error
//     rl 0.6pp / gl 0.3pp vs the measured counter, with the join-on-wire
//     control at 0.4pp / 0.1pp — rl 0.5pp / gl 0.4pp once the direct-impact
//     and radius-damage entries below moved the damage model;
//     lg/sg/ssg/axe unchanged. ng/sng stay
//     withheld — nail linking is opt-in, so there is no measured baseline to
//     validate a recovery against. See damagerecon/ACCURACY.md §"Aim hit
//     recovery".
//   - ADDED on `aim.players[].weapons[].recon`: `directHits` (rl/gl only)
//     — the projectiles the reconstruction says TOUCHED a player, which is
//     the only thing KTX's own `acc.rl.hits` / `acc.gl.hits` increments on
//     (ktx/src/weapons.c:994, :1329), and therefore what
//     `playerStats.accuracy.byWeapon[rl|gl].hits` publishes on a
//     `reconstructed` row (`hitsConvention: "directImpact"`). NOT a subset
//     of `hits` and not the same join: it counts damage ROWS, since one
//     projectile touches at most one player. Scoped to a windowed query
//     like every other figure in the block.
//   - ADDED on `damage`: `rocketDirectDamage` (the server's direct rocket
//     constant where this demo's own hits established it — 110 on every
//     KTX since 1.36) and `rocketDirectRegime`, a three-value total
//     partition of every reconstructed section saying WHICH verdict was
//     reached: `fixed` | `spread` (enough near-direct hits to test, and
//     they did not cluster — evidence against the constant) |
//     `unestablished`. The classifier behind `directHits` leans on the
//     constant, and the three populations score differently against a
//     verbatim KTX block, so the verdict is published rather than implied
//     by the constant's absence.
//   - CHANGED on `damage.events[]`: `isSplash` for rl/gl now comes from
//     the flight's trajectory against the victim's 32x32x56 hull plus the
//     magnitude prior (and, for gl, the spent 2.5s fuse), replacing an
//     explosion-endpoint-within-48-units rule that over-counted rl touches
//     by 80%; an obituary-anchored rocket kill takes the same verdict
//     instead of keeping its zero value. Rocket SPLASH is modelled on the
//     engine's 120 base rather than on the direct constant.
//   - ADDED top-level `noMatch`: the explicit marker on a result that
//     carries no analyzable match, replacing the silent empty result that
//     2.0% of the archive (1 032 of 50 951) produced. It names WHY —
//     `midMatchRecording` / `matchStartUnannounced` / `noMatchDeclared` /
//     `noPlayRecorded` / `demoUnreadable` — with the wire evidence behind
//     the verdict (`statusAtOpen`, `statusRunningSeen`, `gameDir`,
//     `kills`), and carries the wall-clock anchor + `dateMarkers` that
//     `streams.global` has no home for on such a result. Present exactly
//     when `streams` is absent; `/overview` republishes it beside
//     `errors[]`.
//
// v74 — the teamkill obituaries the recoveries could not complete stop being
// dropped.
//   - ADDED on `frags`: `unpaired[]` — teamkill obituaries that name only one
//     party (the other is the placeholder "teammate") and whose missing side
//     neither recovery could identify. They cannot join `frags[]`, whose
//     entries all name both sides, but the obituary is on the wire and
//     dropping it lost a real death. Per-player tallies must skip them; the
//     value is the CAUSE, which the victim-named forms carry (`tele` /
//     `stomp`), and which lets the damage reconstruction type such a kill
//     positionally instead of pricing the victim's corpse drop as team
//     weapon damage.
//   - CHANGED on `frags[].weapon`: `squish` on the teamkill phrasing
//     "X squished a teammate" — the third deathtype-tested message in KTX's
//     team branch (dtSQUISH, ktx/src/client.c:5362), previously flattened to
//     the cause-less `teamkill`. Same cause token its non-teamkill siblings
//     already use ("X squishes Y", "X was squished"), so a consumer
//     filtering `squish` now gets all three forms.
//
// v75 — the match boundary becomes a Layer-1 event, and KTX matchless
// servers (FFA / CTF with `k_matchless 1`) get an analyzable result at all.
//   - ADDED on `streams.global`: `matchStartSignal` — which wire signal the
//     match start was detected from: `ktx-matchstart` | `print` |
//     `matchdate` | `status`. The parser now raises the start on any of the
//     four (mvd-reader/parser/matchstart.go) instead of on a match-start
//     print alone, because a matchless KTX server never prints one
//     (ktx/src/match.c:1294-1297 gates "The match has begun!" on
//     `!k_matchLess`) while still printing `matchdate:` (:1291), stuffing
//     `//ktx matchstart` (:1372) and moving `status` to a running clock
//     (:1337).
//   - CHANGED: demos that previously came out with `noMatch.reason =
//     matchStartUnannounced` and no `streams` now carry a full result.
//     Measured over the 138 such demos in the 50 951-demo archive sweep,
//     all 138 gained streams (104 on `matchdate`, 34 on `status`) —
//     per-demo output in
//     `.reports/nomatch-marker/recensus-v75-unannounced-138.csv`, beside
//     the probe that wrote it (`recensus-v75-probe.go.txt`, whose header
//     carries the rerun recipe). The
//     reason keeps its name and now means "the server moved `status` to a
//     running clock and no analyzable player stream came out".
//   - FIXED: `timelineAnalysis.fragEvents` no longer carries the scoreboard
//     zeroing that `SV_DropClient` broadcasts when a player quits AFTER the
//     match ended (the timeline recorded frag updates on "started" alone,
//     never on "not ended"). Needs a recording that runs past match end,
//     which is normal on a matchless server; no existing golden moves.
//   - UNCHANGED: match-relative timestamps on every existing demo. All four
//     signals land in the same server frame on modern KTX; measured across
//     the golden corpus and the 1 500-demo healthy archive control
//     (`.reports/nomatch-marker/recensus-v75-healthy-1500.csv`), no demo's
//     match start moved and no demo lost its streams.
//
// See RELEASE_NOTES.md.
const CurrentSchemaVersion = 75

// Result is the aggregate output of a qwanalytics pipeline run. Each
// top-level field is produced by one or more analyzers; omitted fields
// mean no analyzer contributed that section (for example, because the
// source lacked the necessary events).
//
// Match length: read MatchResult.Duration (int32 milliseconds,
// parser-derived) or DemoInfoResult.Duration (integer seconds,
// KTX-authoritative).
type Result struct {
	SchemaVersion    int                     `json:"schemaVersion"`
	FilePath         string                  `json:"filePath"`
	Match            *MatchResult            `json:"match,omitempty"`
	Frags            *FragResult             `json:"frags,omitempty"`
	Messages         *MessagesResult         `json:"messages,omitempty"`
	DemoInfo         *DemoInfoResult         `json:"demoInfo,omitempty"`
	TimelineAnalysis *TimelineAnalysisResult `json:"timelineAnalysis,omitempty"`
	Metadata         *MetadataResult         `json:"metadata,omitempty"`
	LocGraph         *LocGraphResult         `json:"locGraph,omitempty"`
	Items            *ItemsResult            `json:"items,omitempty"`
	Damage           *DamageResult           `json:"damage,omitempty"`
	Shots            *ShotsResult            `json:"shots,omitempty"`
	Aim              *AimResult              `json:"aim,omitempty"`
	MapEntities      *MapEntitiesResult      `json:"mapEntities,omitempty"`
	Backpacks        []BackpackDrop          `json:"backpacks,omitempty"`
	WeaponPickups    []WeaponPickup          `json:"weaponPickups,omitempty"`
	Opening          *OpeningResult          `json:"opening,omitempty"`
	PlayerStats      *PlayerStatsResult      `json:"playerStats,omitempty"`
	Streams          *Streams                `json:"streams,omitempty"`
	NoMatch          *NoMatchResult          `json:"noMatch,omitempty"`
	Errors           []string                `json:"errors,omitempty"`
	ParseWarnings    *ParseWarnings          `json:"parseWarnings,omitempty"`
}

// NoMatch reason vocabulary. Exactly one is set on NoMatchResult.Reason;
// the set is a total partition of "this demo produced no player streams",
// so a consumer can switch on it exhaustively. Every value is grounded in
// wire evidence carried alongside it in the same struct — see the field
// docs on NoMatchResult and the derivation in
// mvd-analytics/analyzer/nomatch.go.
const (
	// NoMatchDemoUnreadable: the event stream aborted, so the demo was
	// never read to the end. errors[] carries the reader's reason. No
	// conclusion about the match is possible — the match-start
	// announcement may simply sit past the truncation point — so this
	// reason reports the truncation instead of guessing.
	NoMatchDemoUnreadable = "demoUnreadable"
	// NoMatchMidMatchRecording: the serverinfo `status` key already named
	// a running game when the recording opened ("13 min left"), i.e. the
	// match-start announcement happened before the first demo frame. The
	// recorded window is real play; this pipeline just has no match
	// origin to rebase it onto. Weak corner: running-at-open with
	// Kills == 0 (1 of the 68 in the archive sweep) — see the reason
	// table in RESULT_SCHEMA.md.
	NoMatchMidMatchRecording = "midMatchRecording"
	// NoMatchStartUnannounced: `status` was not running at demo open but
	// became running during the recording, and still no analyzable player
	// stream came out. The name is historical — before schema v75 this
	// reason did mean "none of the match-start signals fired", because the
	// only signal was the match-start print. The `status` transition is now
	// itself one of the four Layer-1 signals (see
	// mvd-reader/parser/matchstart.go), so what reaches this reason is one
	// of two residues: a start signal that DID fire and opened a window
	// holding no play `buildStreamsResult` could reconstruct, or a running
	// clock that only ever arrived in a bulk `fullserverinfo` dump — a
	// statement of the server's state rather than a transition, which
	// cannot raise the event (parser.observeServerInfoStatus).
	//
	// Schema v75 narrowed this reason to near-nothing. Re-running the
	// 138 demos that carried it in the 50 951-demo archive sweep, ALL 138
	// now detect a match start and produce a full result: 104 from the
	// `matchdate:` stamp (the KTX matchless FFA servers, which skip the
	// "The match has begun!" broadcast — ktx/src/match.c:1294-1297 gates
	// it on `!k_matchLess`) and 34 from the `status` transition alone
	// (the ktx 1.38 / 1.40-beta demos, and every one of the 24 `fortress`
	// + 8 `ctf` demos, whose mods write their own running clock into the
	// key). Per-demo output:
	// `.reports/nomatch-marker/recensus-v75-unannounced-138.csv`, written
	// by the probe kept beside it as `recensus-v75-probe.go.txt` (rerun
	// recipe in its header). What is left for this reason is a server that
	// moves `status` to a running clock and yet yields no player stream at
	// all.
	NoMatchStartUnannounced = "matchStartUnannounced"
	// NoMatchNoMatchDeclared: no match declaration this pipeline can see —
	// the `status` key never named a running game — yet the frag log
	// parsed kills. Usually unmanaged play (a mod with no match state, or
	// free play on an idle server), but read it as an ABSENCE OF
	// EVIDENCE, not as proof: 168 of the 170 demos here send no `status`
	// key at all, and the running vocabulary this pipeline reads is KTX's,
	// so a managed match on a mod that declares itself some other way
	// lands here too. GameDir names the mod where the server stated one.
	NoMatchNoMatchDeclared = "noMatchDeclared"
	// NoMatchNoPlayRecorded: no match declaration this pipeline can see
	// and no kills in the parsed frag log. Usually an idle or aborted
	// server — most of these are a few seconds long — with the same
	// caveat as noMatchDeclared on both halves: neither the declaration
	// nor the obituaries of a foreign mod are readable here.
	NoMatchNoPlayRecorded = "noPlayRecorded"
)

// NoMatchResult is the explicit marker on a Result that carries no
// analyzable match: `streams` is absent, so every stream-derived section
// (buckets, damage, playerStats, locGraph, …) is absent with it.
//
// It exists because absence alone is ambiguous. Before schema v74 a
// consumer facing an empty result could not tell "this demo holds no
// match" from "the recording starts mid-game" from "the parse failed" —
// 1 032 of the 50 951-demo archive sweep (2.0%) produced empty streams
// and an EMPTY errors[], because the v52 `timeBase:"demo"` fallback is
// itself gated on `streams` existing (analyzer/timeline_finalize.go
// flagDemoTimeBase) and so never fired. This section is present exactly
// when the `streams` block is absent — ONE predicate, not two: `streams`
// is written only when it holds at least one player stream
// (analyzer/timeline_streams.go buildStreamsResult returns nil otherwise),
// so "no streams block" and "no player streams" are the same state, and a
// result carrying both `streams` and `noMatch` is not constructible.
//
// It is deliberately NOT an errors[] entry: errors[] means the pipeline
// failed at something, and "this recording holds no match" is a fact
// about the demo, not a failure. The one reason that IS a failure,
// demoUnreadable, says so by name and leaves the detail in errors[].
type NoMatchResult struct {
	// Reason is one of the NoMatch* constants above.
	Reason string `json:"reason"`
	// Detail is the same verdict as one human-readable sentence, naming
	// the evidence (the verbatim status string, the gamedir, the kill
	// count). It is what a text-oriented consumer — an /overview reader,
	// an agent — should show beside the reason code.
	//
	// UNSTABLE, DISPLAY ONLY: do not parse it, match on it or key logic
	// off it. The wording changes without a schema bump, and it needs no
	// parsing — every fact it states appears as a structured field beside
	// it (Reason, StatusAtOpen, StatusRunningSeen, GameDir, Kills).
	Detail string `json:"detail"`
	// StatusAtOpen is the serverinfo `status` value as it stood in the
	// `fullserverinfo` dump at demo open, verbatim. It is the wire's own
	// statement of the game state at the first frame, and the evidence
	// behind midMatchRecording. Distinct from ServerInfo["status"] in
	// `metadata`, which is last-write-wins and so names the state at demo
	// END. Empty when the server sent no `status` key at all (pre-KTX
	// servers, and mods that never set it).
	//
	// The running-game spellings observed across the archive are KTX's
	// "%d min left" (ktx/src/match.c:596,723,1330) and a "%d:%02d left"
	// variant from an older mod; the idle/pre-match ones are "Standby"
	// (world.c:543), "Countdown" (match.c:2475), "Forcestart"
	// (admin.c:693) and a mod-specific "Normal".
	StatusAtOpen string `json:"statusAtOpen,omitempty"`
	// StatusRunningSeen is set when `status` named a running game at any
	// point in the recording — at open or in a later svc_serverinfo
	// update. It separates matchStartUnannounced (the server did start a
	// match) from noMatchDeclared / noPlayRecorded (it never did).
	StatusRunningSeen bool `json:"statusRunningSeen,omitempty"`
	// GameDir is the serverinfo `*gamedir` key: the mod the server ran.
	// "qw" is the stock deathmatch gamedir; anything else ("fortress",
	// "ctf", "jteams", "runes", …) is a mod with rules this pipeline does
	// not model. It is reported as evidence, never as a reason of its own
	// — a foreign gamedir can still run a managed match, and a "qw"
	// server can still record nothing.
	GameDir string `json:"gameDir,omitempty"`
	// Kills is the length of the frag log (`frags.frags`): the kills whose
	// obituaries THIS PIPELINE RECOGNISES, which is how much play the
	// recorded window is known to have held — a floor, not a census. The
	// obituary table is id1/KTX vocabulary, so a foreign mod's own death
	// messages (a TeamFortress sentry gun, a mod-specific environmental
	// kill) are invisible to it, and 51 of the 636 noPlayRecorded demos in
	// the archive sweep ran a foreign gamedir. Zero here means "the frag
	// log parsed nothing", not "nobody died".
	//
	// Non-zero with any reason except noPlayRecorded, which is defined by
	// it being zero.
	Kills int `json:"kills,omitempty"`

	// DateMarkers lists every date stamp the wire carried, verbatim and in
	// the order seen — the same []WallClockMarker
	// `streams.global.dateMarkers` carries, given a home here because
	// there is no GlobalStream on this result (schema v74). Before v74
	// these were read off the wire and then dropped on the floor: 73 of
	// the 877 stream-less demos in the archive sweep printed a
	// `matchdate:` and published nothing, while `metadata.finalScores`
	// (which does not live under `streams`) survived.
	//
	// The markers are published RAW, and deliberately without the graded
	// `matchStartUnixMs` anchor GlobalStream carries beside them. That
	// anchor is a PROJECTION — a match-start print is projected as
	// `stamp - print's demo time + DemoOffset`, a match-end stamp as
	// `stamp - match length` — and both terms are the match window, which
	// is exactly what this result does not have. Publishing the
	// projection against a zero window would state an instant off by the
	// recording's own offset and label it "match start". Resolving it
	// properly means establishing a match origin on the demo clock, which
	// is salvage: plan-archive-features.md §8 stage (b). Until then the
	// stamps stand on their own — `kind` says which instant each one
	// names, and `metadata.finalScores` still carries KTX's own record.
	DateMarkers []WallClockMarker `json:"dateMarkers,omitempty"`
}

// ParseWarnings is the census of what the WIRE carried but the reader
// could not decode: unknown svc_* commands, unknown temp-entity or
// hidden-message types, and payloads that failed to parse. It is a
// distinct signal from Errors — those are analyzer-level failures over
// events we did read — and it is sub-fatal by construction: the reader
// abandons the rest of the offending payload and carries on, so the
// result is complete apart from whatever that payload held.
//
// It exists because silence is not evidence of correctness. The
// sv_bigcoords angle desync degraded ~5% of the archive for years with
// no operator-visible signal, because parse warnings lived only in a
// test harness. A non-zero total here means the reader has a gap on
// this demo and the sections downstream of it may be thin.
//
// Omitted entirely when the parse was clean (Total == 0), which is the
// case for the overwhelming majority of modern demos.
type ParseWarnings struct {
	// Total is the exact number of warnings raised, never capped.
	Total int `json:"total"`
	// ByType is the exact per-category count. Categories are a small
	// fixed vocabulary: "parse_error", "unknown_svc", "unknown_te",
	// "unknown_hidden".
	ByType map[string]int `json:"byType,omitempty"`
	// Groups is a capped table of distinct (type, message) rows, loudest
	// first, each with its count and the demo time it first fired. It is
	// the sample set — the counts above stay exact even when a group is
	// dropped.
	Groups []ParseWarningGroup `json:"groups,omitempty"`
	// DroppedWarnings accounts for the retention cap
	// (parser.MaxWarningGroups distinct messages): how many warnings fell
	// outside the retained groups and are therefore missing from Groups.
	// Zero in every normal case. It is an OCCURRENCE count, not a count of
	// distinct messages — the reader deliberately does not track the
	// distinct key set past the cap, since holding it is exactly the
	// unbounded memory the cap exists to avoid.
	DroppedWarnings int `json:"droppedWarnings,omitempty"`
}

// ParseWarningGroup is one distinct warning message with its count and
// first occurrence.
type ParseWarningGroup struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Count   int    `json:"count"`
	// FirstDemoTimeMs is RAW DEMO time (the wire clock), not the
	// match-relative base every other ms field in this schema uses: the
	// reader has no clock, and a warning can fire before the match
	// starts (or on a demo with no match at all), where a rebased value
	// would be meaningless.
	FirstDemoTimeMs int32 `json:"firstDemoTimeMs"`
}

// EffectiveMap resolves which map (hence which BSP / loc corpus) this demo was
// recorded on, independent of whether the KTX demoinfo block is present. It
// prefers the KTX demoinfo map name and falls back to the serverinfo `map` key
// that MetadataAnalyzer parses from the `fullserverinfo` stufftext. Returns ""
// when neither source names a map.
//
// The fallback matters: older recorders (e.g. MVDSV 1.00 with KTX 1.43/1.44,
// 2024-era) never emit the demoinfo hidden block — MVDSV writes it only when
// KTX issues `cmd demoinfo` (mvdsv/src/sv_demo_misc.c; ktx/src/commands.c) — so
// r.DemoInfo is nil there even though serverinfo always carries the map. Every
// BSP-derived feature (LOS/PVS, loc resolution, floor height, liquid state,
// region control) resolves its map through this accessor so the absence of the
// KTX block never reads as "no map". This is a post-hoc accessor over the
// assembled Result (used by lazy passes like ComputeLOS); the analyzer pipeline
// has the equivalent CoreOutputs.EffectiveMap for Finalize-time use.
func (r *Result) EffectiveMap() string {
	if r == nil {
		return ""
	}
	if r.DemoInfo != nil && r.DemoInfo.Map != "" {
		return r.DemoInfo.Map
	}
	if r.Metadata != nil && r.Metadata.ServerInfo != nil {
		return r.Metadata.ServerInfo["map"]
	}
	return ""
}
