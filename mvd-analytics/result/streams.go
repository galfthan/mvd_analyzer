package result

import (
	"bytes"
	"encoding/gob"
)

// Streams is the canonical native-rate storage for per-player and
// global state changes. Read by the qwanalytics/view query API.
//
// Each PlayerStream records every change to a tracked field at the
// rate it actually changed (see appendChange semantics). Position is
// the only field that records every native-rate sample without dedup;
// every other field is sparse — entries represent transitions, not
// per-tick samples.
type Streams struct {
	Players []PlayerStream `json:"players"`
	Global  GlobalStream   `json:"global"`
	// Movers is the pose timeline of every tracked brush-model entity
	// (lifts, doors, plats, trains). Schema v32; omitted when the demo has
	// no movers.
	Movers []MoverStream `json:"movers,omitempty"`

	// Projectiles and Beams are the spatial weapon-fire streams for the map
	// view (schema v40): every tracked rocket/grenade flight and every LG
	// bolt. They are opt-in — built only when the shot-stream flag is set
	// (qw-analyze -include projectiles,beams; the WASM map build) — so the
	// default output and golden corpus stay lean. Omitted when not built.
	Projectiles *ProjectileStreams `json:"projectiles,omitempty"`
	Beams       *BeamStreams       `json:"beams,omitempty"`
	// Nails is the nail-flight stream (ng/sng spikes), same columnar shape as
	// Projectiles with Weapon == "nail". Doubly opt-in: built only when nail
	// tracking is on AND shot streams are requested, since nails are far
	// higher volume than rockets/grenades.
	Nails *ProjectileStreams `json:"nails,omitempty"`
	// PointEffects is every point-effect temp entity (schema v71): blood
	// (hitscan damage on a player, with the per-volley pellet count),
	// lightning blood (LG hit), explosion (rocket/grenade detonation
	// point), gunshot (wall-puff miss pattern) and the rest. Damage
	// telemetry present on every demo generation — the damage
	// reconstruction's per-hit evidence. Rides the shot-streams gate like
	// Projectiles/Beams; omitted when not built.
	PointEffects *PointEffectStreams `json:"pointEffects,omitempty"`

	// LOSComputed records whether the (lazy) line-of-sight pass has run to a
	// result worth keeping on this in-memory Result, so a caller (web overlay,
	// -include los, the mvd-api /los endpoint) computes it on demand exactly
	// once. It latches ONLY on outcomes that should stick: a genuine compute or
	// a legitimately empty <2-player demo. It deliberately does NOT latch when
	// the map has no usable visibility BSP — analyzer.ComputeLOS returns
	// ErrNoBSP there and leaves this false, so a request that arrives after the
	// BSP is provisioned (or after a restart drops mapbsp's memoised nil)
	// retries instead of serving a poisoned empty forever. It latches for the
	// lifetime of this Result value. The API's tier-2 gob is written once at
	// parse (before any LOS pass), so a Result re-decoded from it starts with
	// LOSComputed=false; the API persists a successful LOS separately in its
	// tier-3 artifact cache (mvd-api/internal/democache), so the pass is not
	// re-run after a restart or eviction — the warm request splices the cached
	// intervals and re-sets this latch. An ErrNoBSP outcome is never persisted
	// (encodeLOS refuses to encode an unlatched Result), so no empty los gob is
	// ever written. Excluded from JSON (`json:"-"`): consumers read
	// presence/absence of PlayerStream.LOS itself, and the goldens stay
	// agnostic to it. See analyzer.ComputeLOS and the "los" lazy artifact
	// (analyzer/materialize.go).
	LOSComputed bool `json:"-"`

	// ShotStreamsComputed / NailsComputed latch the spatial weapon-fire streams:
	// the eager build (shots.go buildSpatialStreams) sets each flag truthfully
	// when its build flag (Registry.BuildShotStreams / BuildNails) was on, so a
	// consumer can tell "streams built, possibly empty" from "streams never
	// built". mvd-api turns both flags on for every parse (the always-full
	// cache), and the WASM web build likewise; the default CLI parse leaves them
	// off (lean output). There is no on-demand rebuild anymore — phase 12 folded
	// the streams into the base parse and deleted the lazy "shot-streams"
	// artifact. JSON-excluded — clients read presence/absence of the streams
	// themselves.
	ShotStreamsComputed bool `json:"-"`
	NailsComputed       bool `json:"-"`
}

// ProjectileStreams is every tracked rocket/grenade flight as parallel
// columns (one entry per flight). Flight i renders as a dot moving from
// (Sx,Sy,Sz)[i] at Spawn[i] to (Ex,Ey,Ez)[i] at End[i], match-relative ms —
// linear, which is exact for rockets and an approximation for bouncing
// grenades. Weapon[i] is "rl" or "gl". Built only when shot streams are
// requested (see Streams.Projectiles).
type ProjectileStreams struct {
	Weapon []string  `json:"w"`
	Spawn  []int32   `json:"s"`
	End    []int32   `json:"e"`
	Sx     []float32 `json:"sx"`
	Sy     []float32 `json:"sy"`
	Sz     []float32 `json:"sz"`
	Ex     []float32 `json:"ex"`
	Ey     []float32 `json:"ey"`
	Ez     []float32 `json:"ez"`
}

// BeamStreams is every Lightning Gun bolt (TE_LIGHTNING2) as parallel
// columns. Beam i is the segment (Sx,Sy,Sz)[i] → (Ex,Ey,Ez)[i] flashed at
// T[i] match-relative ms (Sx is the muzzle, Ex the trace endpoint). Built
// only when shot streams are requested (see Streams.Beams).
type BeamStreams struct {
	T  []int32   `json:"t"`
	Sx []float32 `json:"sx"`
	Sy []float32 `json:"sy"`
	Sz []float32 `json:"sz"`
	Ex []float32 `json:"ex"`
	Ey []float32 `json:"ey"`
	Ez []float32 `json:"ez"`
}

// PointEffectStreams is every point-effect temp entity as parallel columns.
// Effect i is of raw TE type Type[i] (events.Te* vocabulary: 0 spike,
// 1 superspike, 2 gunshot, 3 explosion, 11 teleport, 12 blood,
// 13 lightningblood, ...) at (X,Y,Z)[i], T[i] match-relative ms.
// Count[i] is the leading count byte on the two counted types
// (TE_GUNSHOT / TE_BLOOD — pellet counts, see mvd-reader/MVD_FORMAT.md
// for the per-server-generation packaging) and 0 elsewhere. Built only
// when shot streams are requested (see Streams.PointEffects).
type PointEffectStreams struct {
	T     []int32   `json:"t"`
	Type  []int32   `json:"ty"`
	Count []int32   `json:"c"`
	X     []float32 `json:"x"`
	Y     []float32 `json:"y"`
	Z     []float32 `json:"z"`
}

// MoverStream is one brush-model entity's pose timeline (a lift, door,
// plat or train). The columns align by index: at T[i] match-relative
// milliseconds the model sits at (X,Y,Z)[i] and is drawn when Vis[i]. The
// viewer offsets the corpus SubModel mesh (mapgeom SubModelMesh with the
// same ID as SubModel here) by (X,Y,Z) to place it.
//
// Origins are float32 — wire values are exact 1/8-unit multiples and
// int32 would quantize the pose stepping. Tracks are short: MVD delta
// compression only re-sends an origin when it moves, so a parked mover is
// a single entry and a travelling one re-sends per frame only while in
// motion. The first entry is clamped to T=0 carrying the match-start pose
// (normalizeMatchRelativeTimes) so a parked mover whose only wire state
// predates the match still has a pose.
type MoverStream struct {
	EntNum   int       `json:"ent"`
	SubModel int       `json:"sub"`
	T        []int32   `json:"t"`
	X        []float32 `json:"x"`
	Y        []float32 `json:"y"`
	Z        []float32 `json:"z"`
	Vis      []bool    `json:"vis"`
}

// PlayerStream is one player's full event-rate state record. Name is
// the canonical demoinfo-resolved player name; if two slots collide
// on a single name within one match, the second is suffixed
// "name#slotIndex". Mid-match name changes are folded into the same
// stream by the analyser's existing canonicalisation.
type PlayerStream struct {
	Name string `json:"name"`
	Team string `json:"team,omitempty"`

	// Identity is the reconnect-unification key: two rows carrying the same
	// value are the same human, however their names differ. It is what makes
	// the split roster usable — mvdsv renames a player who reconnects while
	// their old connection is still spawned (`(1)name`, sv_main.c:3686-3717)
	// and KTX may decline to merge the two, which is faithful but leaves a
	// consumer with two rows and no supported way to relate them.
	//
	// DEMO-LOCAL, and NOT a person: it is derived from the first session's
	// slot and userid ("s9u12"), both of which describe a connection to one
	// server. Do not persist it and do not compare it across demos — the
	// cross-demo identity is the authenticated login (playerStats[].login).
	// It is reproducible from these bytes but not promised across pipeline
	// versions. See analyzer.identityKeys.
	//
	// Absent when the demo produced no identity table at all (a degraded
	// parse, or a hand-built registry without the identity analyser).
	Identity string `json:"identity,omitempty"`

	// Sessions is every wire-slot occupancy this identity played, in time
	// order: the answer to "which slot and userid was this row at time t",
	// which is what a hub `track=` link or a live-roster join needs. One
	// entry per connection — a player who times out and rejoins has two,
	// with two userids.
	//
	// Two kinds of occupancy are deliberately withheld, because neither can
	// answer that question: one the wire never gave a userid of its own (see
	// PlayerSession.UserID), and one first attested at or after match end —
	// a postgame connection, whose published window would close before it
	// opened (see the EndMs rule) and which is not an in-match window to
	// track anyway.
	//
	// Absent in two DIFFERENT states, which Identity tells apart:
	//   Identity == ""  the demo produced no identity table at all (see
	//                   Identity) — nothing was measured;
	//   Identity != ""  the row HAS an identity and every occupancy behind
	//                   it was withheld. A real, pinned state — the row is a
	//                   person whose connections are simply not linkable —
	//                   not a missing table.
	Sessions []PlayerSession `json:"sessions,omitempty"`

	// Position track at native rate. Always populated in-memory; whether
	// it is serialised to JSON is controlled at marshal time (the CLI's
	// -include positions flag and equivalent transports). Nil when the
	// player produced no position events.
	Position *PositionTrack `json:"pos,omitempty"`

	// Discrete state-change streams. Sparse — every entry is a transition.
	// Health/armor use int16: Quake values can reach 250 (mega-health,
	// red armor) which exceeds int8 range.
	Health    []ChangeI16 `json:"h,omitempty"`
	Armor     []ChangeI16 `json:"a,omitempty"`
	ArmorType []ChangeStr `json:"at,omitempty"` // "ga"|"ya"|"ra"|""
	Loc       []ChangeI16 `json:"li,omitempty"` // index into TimelineAnalysisResult.LocTable

	// Inventory presence as half-open intervals [Start, End). One entry
	// per period the field was true. Open intervals at match end are
	// closed at MatchEnd by the analyser.
	RL  []Interval `json:"rl,omitempty"`
	LG  []Interval `json:"lg,omitempty"`
	GL  []Interval `json:"gl,omitempty"`
	SSG []Interval `json:"ssg,omitempty"`
	SNG []Interval `json:"sng,omitempty"`

	Quad []Interval `json:"q,omitempty"`
	Pent []Interval `json:"pe,omitempty"`
	Ring []Interval `json:"r,omitempty"`

	// Ammo as change streams (dedup against last value).
	Shells  []ChangeI16 `json:"sh,omitempty"`
	Nails   []ChangeI16 `json:"nl,omitempty"`
	Rockets []ChangeI16 `json:"rk,omitempty"`
	Cells   []ChangeI16 `json:"cl,omitempty"`

	// ActiveWeapon is the WIELDED weapon as a change stream: the IT_* bit
	// of STAT_ACTIVEWEAPON, which mvdsv writes from ent->v->weapon for every
	// spawned player (mvdsv/src/sv_send.c:1268). It is a DIFFERENT question
	// from the RL/LG/GL/SSG/SNG interval streams above — those are inventory
	// (STAT_ITEMS bits, "owns"), this is "is holding right now".
	//
	// Values are single IT_* bits: 1 SG, 2 SSG, 4 NG, 8 SNG, 16 GL, 32 RL,
	// 64 LG, 4096 axe. Zero means "the wire never said" for that instant,
	// not "unarmed" — old recorders that freeze the STAT_ITEMS weapon bits
	// freeze this too, so a consumer must check the column exists and moves
	// before trusting it (see analyzer.activeWeaponLive).
	ActiveWeapon []ChangeI16 `json:"aw,omitempty"`

	// Discrete event timestamps (no value). Integer milliseconds since
	// the stream's time origin (the same epoch as match-relative seconds
	// elsewhere; schema v8 changed the type and unit to give exact
	// comparisons against PositionTrack.T — see PositionTrack comment).
	Spawns []int32 `json:"sp,omitempty"`
	Deaths []int32 `json:"d,omitempty"`

	// Alive is the player's LIVES: one half-open [Start, End) interval per
	// spawn-to-death run, derived from Spawns/Deaths against the match
	// window. It is the canonical STORED liveness — a consumer should read
	// it rather than re-derive liveness from the marker lists.
	//
	// It is not yet the only implementation, and the name is overloaded on the
	// wire: the columnar /buckets response already carries a per-column
	// `alive` computed by view.playerActiveInWindow, which is a
	// window-OVERLAP test with its own fallbacks — a different question from
	// this field's instantaneous one. They can disagree, and neither is wrong.
	//
	// LOS, aim, loc-graph and region-control all read this field. The two
	// predicates that used to re-derive liveness themselves —
	// analyzer.losAliveAt and aimcore's aimAliveAt — are gone: their strict
	// `lastSpawn > lastDeath` LATCHED on a same-millisecond death+respawn and
	// reported the player dead for the whole remaining life (measured: 100.7 s
	// of one player's match). view.playerActiveInWindow is deliberately NOT
	// migrated — it asks whether a player appears anywhere in a bucket window,
	// which is a different question, and it already resolves that tie
	// correctly.
	//
	// Deaths are the FUSION of three detectors (DF_DEAD|DF_GIB on
	// svc_playerinfo, STAT_HEALTH transitions, and the obituary path —
	// mvd-reader/parser/stats.go forceEmitDeath), which is why this is
	// derived from the marker lists rather than from any single wire flag:
	// the obituary path exists precisely because the other two miss deaths
	// (a death+respawn entirely between two MVD frames, and the KTX
	// dtTELE2 pent deflection).
	//
	// Liveness rule: alive from match start until a death, each death
	// beginning a dead period the next spawn ends. It deliberately does
	// NOT require a recorded match-start spawn — KTX emits a player's
	// first spawn only on their first RESPAWN. A death with no following
	// spawn (the dtTELE2 deflection) correctly leaves them dead to the end.
	//
	// A death SPLITS a life even when the respawn lands on the same
	// millisecond: the two intervals TOUCH ([..,T) and [T,..)) so no dead time
	// is invented, but the boundary survives, because anything counting lives
	// or attributing per-life stats has to see it. Conversely a hole in the
	// position track does NOT split — an unobserved stretch is not a death,
	// and on a POV recording (where only players inside the recorder's PVS are
	// written) tracks are full of holes. Refusing to credit unobserved TIME is
	// a separate question, answered by result.SampleStaleCapMs in the
	// occupancy walkers rather than here.
	//
	// NOT omitempty, deliberately — the three states are distinct:
	//   null  the match window is unknown, so liveness was not measurable;
	//   []    measured, and the player was never alive in the window;
	//   [...] the player's lives.
	// A player who never died is a single interval spanning the match, so
	// absence can never be read as "alive throughout". gob cannot carry the
	// null/[] distinction on the slice itself — see PlayerStream.GobEncode.
	Alive []Interval `json:"alive"`

	// LOS records when this player (the looker) had a clear line of sight
	// to each other player, one LosTrack per opponent ever seen (schema
	// v37). Line of sight is asymmetric — the looker's single eye point vs.
	// the target's whole body — so A→B lives in A's LOS and B→A lives in
	// B's, computed independently. Populated only on maps with a
	// provisioned BSP (same gate as PositionTrack.H/Lq); absent otherwise.
	// Raw transitions, no smoothing (surface authoritative data).
	LOS []LosTrack `json:"los,omitempty"`

	// PVS records when each other player was potentially visible to this player
	// (the looker), reproducing exactly the server's per-client entity cull —
	// i.e. whether a live mvdsv would have sent that opponent's entity to this
	// player's client that frame (SV_PlayerVisibleToClient): the looker's fat PVS
	// (CM_FatPVS of origin+view_ofs) intersected with the opponent's entity leaf
	// set (its 1-unit-expanded bounding box, non-solid leaves), or always when
	// the opponent overflows MAX_ENT_LEAFS. The recorded MVD does not carry this
	// (the demo recorder sets pvs = NULL and stores every entity); it is
	// reconstructed here from the position tracks. Same LosTrack shape, same lazy
	// pass (analyzer.ComputeLOS) and BSP gate as LOS, schema v38. This same test
	// gates the LOS raycast (cast only for potentially-visible pairs), so PVS ⊇
	// LOS by construction. The gap between them (on the wire, but no clear ray)
	// is an occlusion-tolerant proximity/awareness signal. Raw transitions, no
	// smoothing.
	PVS []LosTrack `json:"pvs,omitempty"`
}

// PlayerSession is one contiguous occupancy of a wire client slot by one
// connection of a player (schema v66). It is the validity window of a
// userid: `userId` is the value a hub `track=` link must carry to follow
// this player between StartMs and EndMs, and only then — the same human
// reconnecting draws a new one, and the slot they left hands its old one to
// whoever takes it next.
//
// Times are match-relative ms like everything else in Streams, and StartMs is
// NOT clamped: a connection that predates the countdown reports a negative
// value (the same policy as timelineAnalysis.demoMarkers and
// streams.global.pauses — the wire said it, so we say it).
//
// EndMs is the one bound that can be synthetic, in TWO cases, both reading
// exactly matchEnd: a client still connected when the recording ended (no
// wire event exists to report), and a drop broadcast that lands AFTER match
// end (the event exists and is simply outside the window every other time
// here lives on). So EndMs == matchEnd does not by itself mean "still
// connected at the end" — read the drop from the events log if the
// distinction matters.
type PlayerSession struct {
	// StartMs is the first userinfo that attested this connection. For the
	// KTX ghost row — a scoreboard-only edict with userid 0 that the next
	// real connection on the slot is folded into (ktx/src/g_utils.c:2272-2356)
	// — that is the real connection's own userinfo, not the ghost's: a
	// session never claims a window that starts before the connection it
	// describes.
	StartMs int32 `json:"startMs"`
	// EndMs is the drop broadcast or the next connection's userinfo, else
	// match end (see above). Half-open: [StartMs, EndMs).
	EndMs int32 `json:"endMs"`
	// Slot is the wire client slot (0-based), the index svc_* messages
	// address this connection by.
	Slot int `json:"slot"`
	// UserID is the connection's userid, and is ALWAYS non-zero. An
	// occupancy the wire never gave a userid of its own — an inferred
	// occupancy, a userid-0 resend, KTX's ghost scoreboard row — is not a
	// connection anyone can follow, so it is not published as a session at
	// all rather than published with a 0 nobody can link with (the gate is
	// in analyzer/timeline_streams.go, buildStreamsResult). The 0-as-gap
	// value exists one layer down, on the internal analyzer.ResolvedSession
	// this is projected from.
	UserID int `json:"userId"`
	// Name is the netname this connection carried, where the row's `name` is
	// the identity's canonical one. They differ after a rename or an mvdsv
	// `(N)` duplicate-name prefix; this is the one to match against a live
	// engine roster at an instant inside the window. Normalized through the
	// same Quake fold as every other name here (mvd-reader
	// parser/userinfo.go qNormalizeTable), so it is not the raw wire bytes.
	Name string `json:"name,omitempty"`
}

// PlayerStream carries its own gob codec for exactly one field: Alive.
//
// encoding/gob omits zero-valued struct fields, and a length-0 slice is zero
// — so an empty non-nil slice decodes as nil, collapsing two of Alive's three
// documented states. "Measured, and never alive" would come back as "liveness
// was not measurable", which every consumer degrades to UNGATED: region
// control, trails and the lazy LOS pass would then treat that player as alive
// for the whole match. mvd-api's tier-2 demo cache stores Streams as a gob
// (result/cache.go) and evaluates liveness on the decoded Result at read time,
// so without this the same demo answers differently on a cold parse and a
// cache hit.
//
// playerStreamWire is a defined type with PlayerStream's fields and none of
// its methods: gob encodes it structurally (no recursion back into GobEncode)
// and a field added to PlayerStream is carried along automatically. Only the
// nil-vs-empty distinction needs the explicit flag.
type playerStreamWire PlayerStream

type playerStreamGob struct {
	Fields        playerStreamWire
	AliveMeasured bool
}

func (p PlayerStream) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(playerStreamGob{
		Fields:        playerStreamWire(p),
		AliveMeasured: p.Alive != nil,
	}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (p *PlayerStream) GobDecode(data []byte) error {
	var w playerStreamGob
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&w); err != nil {
		return err
	}
	*p = PlayerStream(w.Fields)
	if w.AliveMeasured && p.Alive == nil {
		p.Alive = []Interval{}
	}
	return nil
}

// LosTrack is one looker's line-of-sight onto a single opponent, as the
// half-open [Start, End) ms intervals (match-relative) during which the
// looker had a clear sightline. It hangs off the looker's PlayerStream; Other
// identifies the seen player.
//
// Other is the index into Streams.Players of the opponent (the seen player),
// not a name — the compact index the viewer resolves back to a name. A looker
// has at most one LosTrack per opponent; an opponent never seen is omitted.
//
// "Clear" means at least one of the 9 rays from the looker's eye
// (origin + (0,0,22)) to the opponent's 8 bounding-box corners + box midpoint
// reached the target without crossing CONTENTS_SOLID — worldspawn or any
// active mover (door/lift/plat) posed in the way (bspvis.RayHitsSolid /
// RayHitsSolidModel). Computed only while both players are alive. View
// direction is not considered: this is geometric visibility, not whether the
// opponent is within the looker's FOV.
type LosTrack struct {
	Other int16      `json:"other"`     // index into Streams.Players (the seen player)
	Iv    []Interval `json:"intervals"` // half-open [Start,End) ms the looker saw Other
}

// GlobalStream carries the match window plus the demo/wall-clock anchor —
// everything needed to interpret a stream time without cross-referencing
// other Result sections. Match-relative times are integer milliseconds
// since the stream's time origin (schema v8 — see PositionTrack for the
// unit rationale); MatchStart is always 0 (it *is* the origin) and is kept
// as an explicit anchor.
//
// Wall-clock mapping (schema v23 moved the anchor here from
// TimelineAnalysisResult). For any match-relative game time g (ms):
//
//	wallClockMs = DemoStartUnixMs + DemoOffset + g + P(g)   (±DemoStartAccuracyMs)
//	P(g)        = Σ Pauses[i].DurationMs for Pauses[i].AtMs <= g
//
// The game clock freezes during a pause while wall-clock time runs on, so
// the P(g) term is what keeps the mapping correct on paused demos; it is 0
// (and Pauses may be empty) otherwise.
//
// Schema v72 adds the MATCH-start anchor beside it (MatchStartUnixMs and
// friends). The two answer different questions: DemoStartUnixMs is the
// origin of the mapping above, MatchStartUnixMs is "when was this match
// played" — which is what the wire date markers actually state, and what
// most demos carry (the markers reach ~95% of the archive against ~25% for
// the server-clock anchor). Where only a marker exists, DemoStartUnixMs is
// back-shifted from it by DemoOffset so the formula keeps working, and
// DemoStartSource says so.
type GlobalStream struct {
	MatchStart int32 `json:"matchStart"` // always 0 — the match-relative time origin
	MatchEnd   int32 `json:"matchEnd"`   // match end (≈ duration) in match-relative ms
	// TimeBase is "demo" when no match start could be detected: nothing
	// was rebased, so every timestamp in the whole Result is on the raw
	// demo clock (t=0 = demo open, warmup included). Omitted on the
	// normal match-relative result. We cannot invent a time origin —
	// flagging honestly beats coercing (schema v52).
	TimeBase string `json:"timeBase,omitempty"`
	// MatchStartSignal names the WIRE SIGNAL the match start was detected
	// from (schema v75): "ktx-matchstart" (KTX's `//ktx matchstart`
	// stuffcmd, ktx/src/match.c:1372), "print" (a match-start broadcast such
	// as "The match has begun!", match.c:1296), "matchdate" (the
	// `matchdate:` stamp, match.c:1291) or "status" (a serverinfo `status`
	// transition into a running clock, match.c:1337). The parser raises the
	// event at the FIRST of the four to reach the wire, so on a modern KTX
	// demo — where three of them land in the same server frame — this names
	// which byte arrived first, not a different instant.
	//
	// Distinct from MatchStartSource below, which names where the WALL-CLOCK
	// value MatchStartUnixMs was read from. The two vocabularies overlap on
	// "matchdate" and mean different things.
	MatchStartSignal string `json:"matchStartSignal,omitempty"`
	// DemoOffset is ms from demo open (demo t=0, ≈ countdown start) to match
	// start; it bridges match-relative time and demo time.
	DemoOffset int32 `json:"demoOffset,omitempty"`
	// DemoStartUnixMs is the server's clock (Unix epoch ms) at demo open.
	DemoStartUnixMs int64 `json:"demoStartUnixMs,omitempty"`
	// DemoStartAccuracyMs is its resolution: 1 from the mvdhidden 0x000B
	// millisecond block, 1000 from the whole-second serverinfo `epoch` cvar,
	// and — when the anchor was recovered from a date marker (schema v72) —
	// the marker's own uncertainty (1000, or 50 400 000 when the marker
	// carried no timezone and UTC had to be assumed).
	// Absent (0) when no wall-clock source is present.
	DemoStartAccuracyMs int32 `json:"demoStartAccuracyMs,omitempty"`
	// DemoStartSource names where DemoStartUnixMs came from (schema v72):
	// "mvdhidden" (0x000B block), "epoch" (serverinfo cvar), or the date
	// marker ("matchdate" / "matchkey" / "ktxstats") the anchor was
	// back-shifted from by DemoOffset. Absent with the anchor.
	DemoStartSource string `json:"demoStartSource,omitempty"`
	// MatchStartUnixMs is the server wall clock (Unix epoch ms) at MATCH
	// start — the instant match-relative g=0 names (schema v72). It is the
	// field to answer "when was this played?" with: the wire date markers
	// (matchdate / matchkey prints, ktxstats date) all describe the match,
	// not the demo file, and reach ~95% of the archive where the demo-open
	// anchor above reaches ~25%. On a TimeBase=="demo" result there is no
	// detected match start, so it equals the demo-open anchor.
	MatchStartUnixMs int64 `json:"matchStartUnixMs,omitempty"`
	// MatchStartAccuracyMs is the ± uncertainty of MatchStartUnixMs: 1 or
	// 1000 for the server-clock anchors and second-resolution markers with a
	// resolved timezone, 3 600 000 for a zone name whose DST state is
	// ambiguous, 50 400 000 when the marker named no zone at all and UTC was
	// assumed.
	MatchStartAccuracyMs int32 `json:"matchStartAccuracyMs,omitempty"`
	// MatchStartSource names the marker MatchStartUnixMs was derived from:
	// "mvdhidden" / "epoch" (the demo-open anchor plus DemoOffset),
	// "matchdate", "matchkey", or "ktxstats" (match end minus match length).
	MatchStartSource string `json:"matchStartSource,omitempty"`
	// MatchStartConfidence grades the anchor: "exact" (zone resolved, no
	// check failed), "unverified" (nothing contradicts it, but something is
	// unpinned — an assumed timezone, or one soft signal), "contradicted" (a
	// check the value provably fails). The value is NEVER dropped or coerced
	// on a failed check — the grade plus MatchStartNote is the whole report.
	MatchStartConfidence string `json:"matchStartConfidence,omitempty"`
	// MatchStartNote names the checks behind a non-"exact" grade, joined
	// with "; ". The emitted forms are
	// `version-floor: ktx 1.42 was not released before 2021-01-01`,
	// `impossible-date: the stamp lands after 2100`,
	// `marker-disagreement: matchdate vs ktxstats`,
	// `epoch-reset-window: the stamp lands in the unset-clock boot default
	// (2000-01)` and
	// `tz-unknown: the marker named no timezone, UTC assumed`.
	// Empty on "exact".
	MatchStartNote string `json:"matchStartNote,omitempty"`
	// MatchEndUnixMs is the wall clock at match end: the ktxstats `date`
	// string (KTX writes the block at intermission), else the `//finalscores`
	// stamp once its year has been completed — which is minute-resolution and
	// only as good as the marker its year came from. Absent when the demo
	// carries neither, or neither parsed.
	MatchEndUnixMs int64 `json:"matchEndUnixMs,omitempty"`
	// DateMarkers lists every date stamp the wire carried, in the order
	// they were seen, whether or not the anchor above used them. They are
	// the evidence behind MatchStartConfidence and let a consumer redo the
	// cross-check itself.
	//
	// "Every stamp" is bounded by this struct: the whole family lives on
	// GlobalStream, so a Result with no Streams block carries none of it,
	// even where the wire did print a matchdate — the mid-match recordings
	// of plan lead 8. Metadata.FinalScores survives there; the markers do
	// not.
	DateMarkers []WallClockMarker `json:"dateMarkers,omitempty"`
	// Pauses lists each game pause as a flat segment in the game→wall-clock
	// mapping, in match-relative AtMs order. Derived from the mvdhidden
	// 0x000A (paused_duration) blocks; absent on demos with no pauses or
	// recorded by a server that does not embed the block.
	Pauses []TimelinePause `json:"pauses,omitempty"`
}

// WallClockMarker is one date stamp observed on the wire (schema v72).
// Markers are reported verbatim — a stamp that lost its trust checks still
// appears here; MatchStartConfidence carries the verdict.
type WallClockMarker struct {
	// Source is the wire marker: "matchdate" (KTX's match-start bprint,
	// ktx/src/match.c:1291), "matchkey" (the kmod/KTeam-era bprint),
	// "ktxstats" (the `date` field of the KTX demoinfo block), or
	// "finalscores" (the `//finalscores` end-of-match stuffcmd).
	Source string `json:"source"`
	// Kind is what instant the stamp names: "matchStart" or "matchEnd".
	Kind string `json:"kind"`
	// UnixMs is the parsed instant (Unix epoch ms), with TZ applied. 0 on a
	// "finalscores" marker whose year could not be completed — see YearFrom.
	UnixMs int64 `json:"unixMs"`
	// AtMs is the demo-clock ms the marker was printed at, for the
	// print-borne markers. Absent (0) for the markers that ride no print: the
	// ktxstats block and the `//finalscores` stuffcmd.
	AtMs int32 `json:"atMs,omitempty"`
	// YearFrom names the ANCHOR source whose year completed this stamp. It
	// is set only on "finalscores", whose strftime layout ("%b %d, %H:%M")
	// carries no year: the wall-clock node takes the year from whatever
	// anchored the match, so the stamp corroborates on month/day/hour/minute
	// and never on the year, and it can state no instant at all when the
	// demo carried nothing to anchor on (UnixMs 0, YearFrom empty).
	//
	// The vocabulary is therefore MatchStartSource's, not just the marker
	// sources: "matchdate" / "matchkey" / "ktxstats", but also the
	// server-clock anchors "epoch" and "mvdhidden", which outrank the
	// prints and so supply the year on most modern demos.
	YearFrom string `json:"yearFrom,omitempty"`
	// TZ is the zone token exactly as printed ("CET", "+0200",
	// "Vdsteuropa, sommartid" post-normalisation). Empty when the stamp
	// carried none.
	TZ string `json:"tz,omitempty"`
	// AssumedUTC is set when TZ was missing or not in the offset table, so
	// the instant was read as UTC (see MatchStartAccuracyMs).
	AssumedUTC bool `json:"assumedUtc,omitempty"`
	// Raw is the stamp text as it appeared after the marker prefix.
	Raw string `json:"raw,omitempty"`
}

// PositionTrack is columnar to compress JSON. Indices align across the
// five arrays. Coordinates are int32 — Quake maps can exceed ±32 768
// in any axis, so int16 would silently truncate.
//
// T is integer milliseconds since the stream's time origin (the same
// epoch as the float-seconds version it replaced in schema v8). The
// JSON key stayed "t" for compactness; consumers that previously read
// it as seconds must scale by 1/1000 — the schema-version bump is the
// signal. The wire format gives us a 1-byte ms delta per message, so
// integer-ms storage keeps that exact value all the way from the
// decoder through the persistence layer; float seconds reintroduced a
// 1e-6 drift that caused spawn/death-boundary comparisons in locgraph
// and the blip filter to land on the wrong side of an edge.
//
// Range: int32 ms gives ±24.8 days. Demos run minutes to hours, so
// overflow isn't a concern. Negative values are valid after the
// post-processor subtracts matchStart (warmup samples shift below 0).
//
// Li is the resolved loc-name index per native-rate sample (indexes
// into TimelineAnalysisResult.LocTable, with 0 = "no loc"). Populated
// during analyzer Finalize (after the loc finder is loaded), then
// smoothed by the blip filter. Downstream consumers — the loc graph
// builder, region control, and the FieldLoc bucket reducer in
// view.Buckets — read this column directly instead of deriving locs
// from x/y/z separately.
//
// H is the player's height above the floor beneath them at each sample —
// how far the feet are above the highest solid surface at or below the
// player, from straight-down traces through the map's player clip hulls
// (mapclip, schema v24). Since v26 it is measured over the player's
// bounding-box footprint, not just the origin column: the highest floor
// found under a 3x3 grid of columns sampled ±8 around the origin wins
// (an effective ~48-wide footprint on the already-±16-box-inflated
// hull), so a player skimming a ledge / well rim — origin momentarily
// over the pit while the box overhangs the rim — reads the near floor
// rather than the distant one far below. Since v27 the trace scene also
// includes every moving brush-model entity (lift, door, train) posed at
// its demo-streamed origin for the sample's time, so a player riding
// the dm2 RA lift stands on the lift, not the shaft floor beneath it.
// It reads ~0 when grounded and grows positive during a jump or
// airborne hit (airgib), so a consumer can flag those without any
// coordinate arithmetic. (The absolute floor surface, if needed, is
// Z[i] - 24 - H[i] — the player origin rides 24 units above the floor
// its feet rest on.)
// Since v28 liquids participate too: a player in liquid (Lq level >= 1)
// reads H = 0 by definition, and a player airborne above water / slime
// / lava measures down to the liquid surface when it is the highest
// support beneath them (bspvis.LiquidSurfaceBelow).
// Sentinel NoFloor marks samples with no floor to measure from — over a
// void/pit, an embedded origin, or the zero origin. Populated only when
// a clip hull is loaded for the map (a provisioned BSP); otherwise the
// column is nil/absent (omitempty). Same length as T when present.
// Grounded samples are ~0 with a unit or two of slack from slopes and
// the trace epsilon, so test |H| small rather than == 0.
// Lq is the player's liquid state per sample (schema v28), computed by
// mirroring the engine's PM_CategorizePosition waterlevel probes
// against the map's render BSP (bspvis.WaterLevel): 0 = dry, else
// (type << 2) | level with level 1–3 (feet / waist / eyes submerged)
// and type LqWater/LqSlime/LqLava — so water reads 5/6/7, slime
// 9/10/11, lava 13/14/15. Decode the level with LqLevel (type = Lq >> 2). Samples with
// Lq level >= 1 have H = 0 by definition (the liquid surface is the
// support); when a player is airborne ABOVE liquid, H measures down to
// the liquid surface if it is the highest support under them.
// Populated only when the map's BSP is provisioned (same source as H);
// same length as T when present.
//
// VP / VYa are the player's view direction per sample (schema v31): the
// raw angle16 wire shorts for view pitch and yaw, kept losslessly (the
// exact 2-byte value the server wrote). Decode to degrees with
// float(uint16(v)) * 360/65536 — values are in [0,360), so a pitch
// > 180 means looking up. The wire's roll is always 0 (the server zeroes
// it), so it is not stored. A forward unit vector is one trig call away:
// with p,y the decoded pitch/yaw in radians,
// forward = (cos p·cos y, cos p·sin y, −sin p). Same source as the
// origin columns (svc_playerinfo), so they are populated whenever T is;
// same length as T when present.
//
// VX / VY / VZ are the player's velocity per sample in **Quake units per
// second** (schema v32), derived — not a wire field — from the position
// columns by a central-difference estimator (second-order accurate) over
// the native-rate samples. The estimator does not differentiate across a
// respawn teleport or an abnormal time gap (death / pause / reconnect),
// so a velocity reads ~0 over those rather than spiking; an isolated
// sample reads 0. The source x/y/z are float32 Quake units (the wire's
// sub-unit origin, no longer rounded to whole units), sampled at the demo's
// native cadence (~13-40 ms depending on the recording server's sv_demofps;
// see MVD_FORMAT.md), so the derivative is sub-unit precise at the fast end
// and correspondingly coarser at the slow end — smooth client-side
// only if a softer speed curve is wanted. Like x/y/z and h these are
// native float32; only the JSON text is rounded to 3 decimals (see
// PositionTrack.MarshalJSON / coord.go). Speed is hypot(vx,vy,vz); horizontal speed
// (the usual "are they bunnying" metric) is hypot(vx,vy). Populated
// whenever T is (no BSP needed); same length as T when present.
// X/Y/Z, H and VX/VY/VZ are plain float32 (native resolution); the JSON
// text is rounded to 3 decimals by PositionTrack.MarshalJSON (see
// coord.go) — lossless for eighth-unit positions, trimming only the
// float tail on derived velocity / height.
type PositionTrack struct {
	T   []int32   `json:"t"` // milliseconds since the stream's time origin
	X   []float32 `json:"x"`
	Y   []float32 `json:"y"`
	Z   []float32 `json:"z"`
	Li  []int16   `json:"li,omitempty"`
	H   []float32 `json:"h,omitempty"`   // height above the floor beneath the player; NoFloor = none
	Lq  []int8    `json:"lq,omitempty"`  // liquid state: 0 dry, else (type<<2)|level
	VP  []int16   `json:"vp,omitempty"`  // view pitch, raw angle16 (decode: u16*360/65536; >180 = up)
	VYa []int16   `json:"vya,omitempty"` // view yaw, raw angle16 (decode: u16*360/65536)
	VX  []float32 `json:"vx,omitempty"`  // velocity X, Quake units/sec (central difference)
	VY  []float32 `json:"vy,omitempty"`  // velocity Y, units/sec
	VZ  []float32 `json:"vz,omitempty"`  // velocity Z, units/sec
}

// Lq liquid-type codes (the high bits of a PositionTrack.Lq value).
const (
	LqWater int8 = 1
	LqSlime int8 = 2
	LqLava  int8 = 3
)

// LqLevel extracts the submersion level (0 none, 1 feet, 2 waist,
// 3 eyes) from a PositionTrack.Lq value. The liquid type occupies the
// high bits (LqWater/LqSlime/LqLava = v >> 2, 0 when dry).
func LqLevel(v int8) int { return int(v & 3) }

// NoFloor is the sentinel in PositionTrack.H for a sample with no floor
// beneath it (over a void/pit, or an embedded/zero origin) — the height
// is undefined there. Chosen as -1e9: far outside any real height (Quake
// maps span at most ±32768 per axis) so it can never be mistaken for one,
// and exactly representable in both float32 and float64 so it round-trips
// through JSON unchanged (unlike math.MinInt32, which a float32 serializes
// as -2147483600).
const NoFloor float32 = -1e9

// ChangeI16 is a single transition in an int16 stream. T is integer
// milliseconds since the stream's time origin (schema v8).
type ChangeI16 struct {
	T int32 `json:"t"`
	V int16 `json:"v"`
}

// ChangeStr is a single transition in a string-valued stream. T is
// integer milliseconds since the stream's time origin (schema v8).
type ChangeStr struct {
	T int32  `json:"t"`
	V string `json:"v"`
}

// Interval is a half-open period [Start, End) during which a boolean
// field was true. Bounds are integer milliseconds since the stream's
// time origin (schema v8).
type Interval struct {
	Start int32 `json:"s"`
	End   int32 `json:"e"`
}
