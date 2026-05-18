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
// breaking change to the Result structure or its sub-types. Consumers
// can pin or switch on this value when reading a stored analysis.
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
const CurrentSchemaVersion = 9

// Result is the aggregate output of a qwanalytics pipeline run. Each
// top-level field is produced by one or more analyzers; omitted fields
// mean no analyzer contributed that section (for example, because the
// source lacked the necessary events).
//
// Match length: read MatchResult.Duration (float seconds, parser-derived)
// or DemoInfoResult.Duration (integer seconds, KTX-authoritative).
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
	Backpacks        []BackpackDrop          `json:"backpacks,omitempty"`
	WeaponPickups    []WeaponPickup          `json:"weaponPickups,omitempty"`
	Streams          *Streams                `json:"streams,omitempty"`
	Errors           []string                `json:"errors,omitempty"`
}
