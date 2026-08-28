package analyzer

import "fmt"

// This file makes the analyzer pipeline's previously-implicit dependency
// DAG explicit as data (Stage 1 of PLAN-improve-analytics.md §5). Each
// analyzer and post-processor is wrapped in a nodeSpec declaring what
// artifacts it Requires and Provides; the engine validates the wiring
// and derives a deterministic execution order from it instead of relying
// on hand-ordered registration slices.
//
// The execution order is derived purely from the declared edges; the
// output is a pure function of the demo under ANY valid topological
// order (TestOrderIndependence), so registration order is inventory,
// not a correctness mechanism. Post-processors still
// mutate the assembled Result in place — every one is flagged
// Mutates:true as a temporary marker of the debt Stage 2 (the clock /
// roster refactor) removes.
//
// The out-of-band lazy pass ComputeLOS (los.go) is modelled as a lazy DAG node
// since Stage 3 (materialize.go): a nodeSpec with Lazy:true, registered in
// lazyArtifacts rather than in the eager analyzer / post-processor slices. It
// does NOT enter analyzeSource's execution order (r.specs / r.nodes stay the 25 eager
// nodes), so the eager bundle and the golden corpus are unchanged; it appears
// in -graph output marked lazy and is materialised on demand through the
// LazyArtifact hooks (mvd-api's per-artifact tier-3 cache). (The spatial
// weapon-fire streams were a second lazy node until phase 12 folded them into
// the eager parse — see materialize.go.)

// nodeSpec is one pipeline node declared as data: an analyzer's Finalize
// or a post-processor, with the artifact edges the engine schedules on.
//
// Artifacts are plain string names. Every node provides an artifact
// named after itself (so any node can be depended on by name), plus any
// extra artifacts it publishes. The two fix-up post-processors publish a
// FINAL artifact beyond their own name — "frags:final" (frags-final) and
// "match:final" (match-final) — so an in-pipeline consumer wanting the
// recovered / corrected value requires the ":final" name and can never
// silently bind the raw producer's pre-fix-up output. Requires names the
// artifacts a node's Finalize / post-processor reads.
type nodeSpec struct {
	Name     string   // unique kebab-case node id and its primary artifact
	Requires []string // artifact names this node reads
	Provides []string // artifact names this node writes (includes Name)
	Mutates  bool     // post-processor writes into a section another node created; the ambiguity of "which value" is resolved by the ":final" artifact names those nodes publish
	Lazy     bool     // materialised on demand, not in the eager bundle (Stage 3)
	regIndex int      // registration position; the deterministic topo tie-break

	// Stage-4 manifest metadata (surfaced by ArtifactManifest / -graph json /
	// ARTIFACTS.md). These describe the node as a servable artifact; they do
	// not affect scheduling.
	resultKey string // top-level Result JSON key the artifact lands in ("" = pseudo/internal, not served)
	cost      string // "light" (default) | "heavy" — advisory for API gating / docs
	desc      string // one-sentence contributor-facing description

	analyzer Analyzer            // set for event-reading analyzer nodes
	post     ResultPostProcessor // set for post-processor nodes
}

// nodeMeta is the static dependency declaration for one node, keyed by
// the live handle's identity (an analyzer's Name(), or a post-processor's
// resolved function name). name is the node's kebab-case DAG id; the
// node's primary artifact is name itself, so provides lists only the
// EXTRA (pseudo-)artifacts it publishes beyond its own name.
type nodeMeta struct {
	name     string
	requires []string
	provides []string
	mutates  bool

	// Stage-4 manifest metadata — see nodeSpec's like-named fields. cost is
	// "" for every eager node (defaulted to "light" in specFromMeta); the two
	// heavy passes are the lazy nodes, which set it directly.
	resultKey string
	cost      string
	desc      string
}

// analyzerNodeMeta declares the DAG edges for each analyzer, keyed by its
// Analyzer.Name(). This encodes §1.3 of the plan's reverse-engineered
// edge list:
//
//   - the CoreOutputs edges (demoinfo → identity → frag; the co.* reads
//     of every derived analyzer via ResolveSlotAt / co.FragEntries);
//   - the hidden intra-derived edge timeline → shots (shots writes
//     Projectiles/Beams/Nails into result.Streams, which timeline creates).
//
// The clock artifact (co.Clock) is required by every producer that stamps a
// timestamp match-relative at Finalize (the born-correct conversion that
// replaced the whole-Result rebase), so those nodes declare "clock" —
// map_entities and match carry no timestamps and do not.
var analyzerNodeMeta = map[string]nodeMeta{
	// Core producers.
	"clock": {name: "clock",
		desc: "Match clock — match start/end, pauses, and the demo-start wall-clock anchor every timestamped producer converts against."},
	"demoinfo": {name: "demoinfo", resultKey: "demoInfo",
		desc: "KTX demoinfo scoreboard blob: per-player weapon accuracy, kills, deaths, damage, sprees, and item pickup counts, verbatim from the server."},
	"identity": {name: "identity", requires: []string{"demoinfo"},
		desc: "Player identity sessions — the slot→name/userid/team resolution every downstream producer reads."},
	"frag": {name: "frag", requires: []string{"clock", "demoinfo", "identity", "roster:final"}, resultKey: "frags",
		desc: "Raw frag aggregates and the chronological kill log (weapon, suicide, team-kill flags). In-pipeline consumers wanting the telefrag-recovered log require `frags:final`; the served `frags` key is final by serve time since all nodes run."},
	"roster": {name: "roster", requires: []string{"demoinfo", "identity", "metadata"},
		desc: "Canonical player roster with team labels (the individual player-as-team rewrite applied to duels and to every mode with no teamplay) plus the normalised game-mode descriptor, read by every team-aware producer — which binds `roster:final` (published by `match`) rather than this node, since a demo with no demoinfo block gets its duel verdict from the match scoreboard. Requires `metadata` because the mode resolver reads the serverinfo cvars and the countdown settings table, and `identity` because the individual rewrite OVERWRITES co.DemoInfo.Players[].Team in place (roster.go) — identity copies the raw tag out of the same struct into its session table, so it has to read first."},

	// Derived consumers / independent peers.
	"metadata": {name: "metadata", resultKey: "metadata",
		desc: "Server cvars, parsed KTX match settings (mode, timelimit, antilag, midair, instagib, ...) and KTX's //finalscores end-of-match scoreline."},
	"match": {name: "match", requires: []string{"demoinfo", "identity", "metadata", "roster"}, provides: []string{"roster:final"}, resultKey: "match",
		desc: "Match summary: map, mode, duration, the per-player scoreboard, and a `sources` block naming where the map / mode / team rows came from (demoinfo, serverinfo, //finalscores, derived, individual). Also publishes `roster:final`: on a demo with no demoinfo block this node is what settles the two-participant duel verdict and the mode descriptor derived from it, so every co.Roster / co.GameMode reader binds that artifact. In-pipeline consumers wanting the frag-log-corrected kills/deaths/suicides require `match:final`; the served `match` key is corrected by serve time since all nodes run."},
	"messages": {name: "messages", requires: []string{"clock", "demoinfo", "roster:final"}, resultKey: "messages",
		desc: "Chat, teamsay, and other match print messages with markup-stripped text."},
	"timelineAnalysis": {name: "timeline", requires: []string{"clock", "demoinfo", "identity", "frag", "roster:final", "metadata"}, resultKey: "timelineAnalysis",
		desc: "Match timeline: phases, streaks, powerup runs, pauses, region-control layout, airgibs, and the per-player event-stream container. LARGE — one of the biggest Result sections; prefer the windowed views (events, buckets, region-control) over fetching it whole."},
	"items": {name: "items", requires: []string{"clock", "demoinfo", "identity", "roster:final"}, resultKey: "items",
		desc: "Per-item pickup/respawn timeline with world position and nearest loc."},
	"damage": {name: "damage", requires: []string{"clock", "demoinfo", "identity", "roster:final", "metadata"}, resultKey: "damage",
		desc: "Per-hit damage: totals, matrix, per-weapon, EWep buckets, telefrags, stomps, and the scoreboard cross-check. Decoded from the wire KTX stream when present (source=ktx); on pre-instrumentation demos the damage-recon post fills the same section (source=reconstructed), so the served artifact carries whichever the demo yielded."},
	"shots": {name: "shots", requires: []string{"clock", "demoinfo", "identity", "timeline", "roster:final"}, resultKey: "shots",
		desc: "Per-fire weapon stream with per-player accuracy aggregates and the KTX cross-check. Stream-derived splits ride the opt-in projectile/beam/nail streams (built by qw-analyze -include, always by mvd-api and the WASM web build)."},
	"map_entities": {name: "map-entities", resultKey: "mapEntities",
		desc: "The map's static designed entity layout (item spawns, spawnpoints, teleporters) resolved from the embedded BSP corpus."},
	"backpacks": {name: "backpacks", requires: []string{"clock", "identity", "demoinfo", "roster:final"}, resultKey: "backpacks",
		desc: "RL/LG backpack drops with dropper, weapon, origin and a `source` naming the provenance: `ktx` from the wire `//ktx drop` hint (which also carries the ent number joining to weapon pickups), or `reconstructed` from the backpack-recon node on demos older than that hint. A `ktx` row also carries `fate: expired` when KTX's `//ktx expire` hint announced the pack's 120 s removal — the only wire statement that a pack was NOT taken, which the weapon-pickups join cannot make."},
	"weaponPickups": {name: "weapon-pickups", requires: []string{"clock", "identity", "frag", "roster:final"}, resultKey: "weaponPickups",
		desc: "Slot-weapon acquisitions (world spawners and backpacks) with kills-before-next-death effectiveness."},
}

// postNodeMeta declares the DAG edges for each post-processor, keyed by
// its resolved function name (postProcName). It encodes §1.3's result.*
// read edges. One of the two whole-Result barrier pseudo-artifacts has
// retired; the other came back under a name that says what it is:
//
//   - "epoch:match" retired with the clock refactor — timestamps are born
//     match-relative in each producer's Finalize.
//   - "teams:final" retired with the roster refactor, because team labels
//     are born correct in each producer's Finalize (roster/match/timeline/
//     messages/items/pickups/backpacks/shots read co.Roster) — but the
//     roster is NOT final when the roster node publishes it. On a demo with
//     no usable demoinfo block, MatchAnalyzer.Finalize is what establishes
//     the two-participant duel verdict (Roster.noteMatchParticipants) and
//     refines the mode descriptor from it, mutating both shared structs. So
//     `match` publishes "roster:final" and every co.Roster / co.GameMode
//     reader binds THAT, not the raw "roster". Without the edge the verdict
//     was schedule-dependent: `frag` finalised before `match` under the
//     default tie-break and flagged team kills from the pre-promotion
//     descriptor while damage / shots / timeline / player-stats saw the
//     promoted one (archive e57be1ab5334…, no demoinfo, two participants,
//     serverinfo teamplay 4). aim / match-final / loc-graph /
//     region-control keep only their data edges: they already read final
//     team labels through those artifacts (aim via shots, match-final via
//     match, loc-graph/region-control via streams + match).
//
// The two fix-up nodes are FINAL-artifact producers, not anonymous mutators.
// frags-final appends recovered telefrag team-kills to the raw frag log and
// publishes "frags:final"; match-final folds the frag-log-corrected
// kills/deaths/suicides into the match scoreboard and publishes "match:final".
// Because match-final requires "frags:final" (not the raw "frag"), it can only
// bind the recovered log — the DAG makes "which frag log" unambiguous. Both
// still write in place into a section their raw producer created (frag / match),
// so both keep Mutates:true; the ":final" name is what disambiguates the value.
// frags-final requires clock because it converts victim-named teamkill times
// against co.Clock. The timeline node deliberately stays a consumer of the RAW
// frag artifact (its streaks / kill events are built from the pre-recovery
// obituary log, matching every golden) — recovery runs after timeline.
var postNodeMeta = map[string]nodeMeta{
	"recoverTelefragTeamkills": {
		name: "frags-final", mutates: true,
		requires: []string{"clock", "demoinfo", "frag", "timeline"},
		provides: []string{"frags:final"},
		desc:     "Final frag log: appends recovered victim-named telefrag team-kills to the raw `frag` log; publishes `frags:final` for in-pipeline consumers.",
	},
	"aimPost": {
		name: "aim", mutates: true,
		requires:  []string{"shots", "timeline", "damage:final"},
		resultKey: "aim",
		desc:      "Per-player aim analysis: per-weapon effectiveness, crosshair-error samples, and the LG ramp series. Full splits ride the opt-in projectile/beam/nail streams (built by qw-analyze -include, always by mvd-api and the WASM web build). Binds `damage:final` so a reconstructed section is visible: the MEASURED counters still gate on damage.source == ktx, and reconstructed damage feeds only the separate `weapons[].recon` hit tier.",
	},
	"airgibsPost": {
		name: "airgibs", mutates: true,
		requires: []string{"demoinfo", "frag", "timeline", "damage:final"},
		desc:     "Folds the Key-Moments airgib list into the timeline: direct enemy rocket hits whose victim reads clear air (>= 96 units above floor) at every pre-impact sample of the look-back window (default 100 ms; the preceding tick decides on coarse-tick tracks) with no grounded reading beside the hit. Binds `damage:final` so pre-instrumentation demos get airgibs from the reconstructed damage log — recon direct hits are geometric (explosion-to-victim under 48 units), which is the fidelity the verdict needs.",
	},
	"scoreboardStatsPost": {
		name: "match-final", mutates: true,
		requires: []string{"match", "frags:final"},
		provides: []string{"match:final"},
		desc:     "Final match scoreboard: folds frag-log-corrected kills/deaths/suicides into `match`; publishes `match:final` for in-pipeline consumers.",
	},
	"locGraphPost": {
		name: "loc-graph", mutates: true,
		requires:  []string{"timeline", "demoinfo"},
		resultKey: "locGraph",
		desc:      "Per-map loc adjacency graph with directed transition weights derived from player movement.",
	},
	"noMatchPost": {
		name:      "no-match",
		requires:  []string{"timeline", "metadata", "frags:final"},
		resultKey: "noMatch",
		desc:      "Explicit marker on a result whose `streams` block is absent, naming why there is no analyzable match (midMatchRecording / matchStartUnannounced / noMatchDeclared / noPlayRecorded / demoUnreadable) with the wire evidence behind the verdict. Present exactly when `streams` is absent — one predicate, so no result carries both.",
	},
	"wallClockPost": {
		name: "wall-clock", mutates: true,
		requires: []string{"clock", "demoinfo", "metadata", "timeline", "no-match"},
		desc:     "Match-start wall-clock anchor on `streams.global`: resolves the wire date markers (matchdate / matchkey prints, ktxstats date, the year-less //finalscores stamp) against the serverinfo version floors and each other into a graded instant (exact / unverified / contradicted). On a result with no streams the raw markers land on `noMatch.dateMarkers` instead — which is why it binds that node — and the graded anchor is NOT published there: it is a projection through a match window such a result does not have.",
	},
	"regionControlPost": {
		name: "region-control", mutates: true,
		requires: []string{"timeline", "match", "demoinfo"},
		desc:     "Folds the default-window region-control aggregation into the timeline (arbitrary windows are a view, not an artifact).",
	},
	"playerStatsPost": {
		name:      "player-stats",
		requires:  []string{"clock", "identity", "roster:final", "timeline", "match:final", "frags:final", "damage:final", "shots", "items", "weapon-pickups", "backpacks:final", "metadata", "aim"},
		resultKey: "playerStats",
		desc:      "Canonical per-player and per-team statistics: corrected scoreboard (including the derived kill-spree maxima), damage, pickup tallies, and possession time (time with each weapon / armor type / no armor) with explicit match-present-alive denominators. Computed for every demo, degrading to derived reconstructions rather than dropping fields; the KTX overlay is applied at read time by view.PlayerStats. Binds `aim` for both hit tiers: the reconstructed one, which fills the accuracy family's `hits` on demos with no wire damage stream (src=reconstructed), and the measured pellet / direct-impact counters the wire-linked branch publishes as KTX's own convention (schema v75) — reading the published section rather than re-running either join is what makes its weapon-level withholds inherit here.",
	},
	"openingPost": {
		name: "opening", mutates: true,
		requires:  []string{"timeline", "items"},
		resultKey: "opening",
		desc:      "Match opening: each player's match-start spawn location plus the first in-match take of every contested spawner (armors, mega, powerups, RL/LG). A pure projection of items + streams, kept small for one-call fetches.",
	},
	"backpackReconPost": {
		name: "backpack-recon", mutates: true,
		requires: []string{"backpacks", "timeline", "frags:final", "metadata"},
		provides: []string{"backpacks:final"},
		desc:     "Reconstructed RL/LG backpack drops for demos older than the KTX `//ktx drop` hint (KTX 1.38): replays DropBackpack's default rule — the victim's wielded weapon at the death instant, dropped at their last broadcast position — into the same `backpacks` section, stamped source=reconstructed and carrying no entNum. A hint-carrying demo is never touched, and the pass stands down (leaving the section absent) on frozen weapon state, a missing frag log or active-weapon stat, a fairpacks/yawnmode/bloodfest ruleset, or a mod new enough to have hinted.",
	},
	"backpackLinkagePost": {
		name: "backpack-linkage", mutates: true,
		requires: []string{"backpacks:final", "timeline", "items"},
		desc:     "Fate of each RECONSTRUCTED backpack drop, read off the wire's backpack-entity track: the pack that appears at the drop's time and place is bound to it, followed to where it lands, and its disappearance classified as `picked` (a live player's bounding box overlapped it, the same test the server ran before BackpackTouch — attributed when the evidence names one player), `expired` (KTX's 120 s SUB_Remove with nobody on it) or `unobserved`. Stamps `backpacks[].fate/picker/pickerTeam/pickupTime/entNum`. A hint-carrying demo is never touched: `//ktx bp` already names the picker in `weaponPickups`.",
	},
	"damageReconPost": {
		name: "damage-recon", mutates: true,
		requires: []string{"damage", "timeline", "shots", "frags:final", "metadata", "demoinfo"},
		provides: []string{"damage:final"},
		desc:     "Reconstructed damage for pre-instrumentation demos: when the wire carried no mvdhidden_dmgdone stream, rebuilds the damage section (raw + bounded) from the h/a change streams, LG beams, projectile flights, fire sounds and the frag log, stamped source=reconstructed; publishes `damage:final`. A wire-measured section is never touched. player-stats, airgibs and aim all bind `damage:final`: player-stats' damage family and airgib detection ride the reconstruction on old demos (src=reconstructed), while aim's measured counters still gate on Damage.Source == ktx and the reconstruction feeds only its separate `recon` hit tier (a raw `damage` edge alone would not pin the pre-mutation value under every topological order).",
	},
}

// specFromMeta builds a nodeSpec from a live handle's metadata, attaching
// the primary artifact (Name) to Provides.
func specFromMeta(m nodeMeta, regIndex int, a Analyzer, p ResultPostProcessor) nodeSpec {
	provides := make([]string, 0, 1+len(m.provides))
	provides = append(provides, m.name)
	provides = append(provides, m.provides...)
	cost := m.cost
	if cost == "" {
		cost = costLight // every eager node is light; the heavy passes are lazy
	}
	return nodeSpec{
		Name:      m.name,
		Requires:  m.requires,
		Provides:  provides,
		Mutates:   m.mutates,
		regIndex:  regIndex,
		resultKey: m.resultKey,
		cost:      cost,
		desc:      m.desc,
		analyzer:  a,
		post:      p,
	}
}

// collectSpecs wraps every registered analyzer and post-processor in a
// nodeSpec, assigning regIndex in registration order (analyzers, then
// post-processors). The regIndex is the topo sort's tie-break — an
// arbitrary deterministic default kept for stable -graph output, log
// readability and comparable PhaseTimings; output is identical under any
// valid order (TestOrderIndependence).
func (r *Registry) collectSpecs() []nodeSpec {
	specs := make([]nodeSpec, 0, len(r.analyzers)+len(r.postProcessors))
	idx := 0
	for _, a := range r.analyzers {
		m, ok := analyzerNodeMeta[a.Name()]
		if !ok {
			panic(fmt.Sprintf("dag: analyzer %q has no node metadata", a.Name()))
		}
		specs = append(specs, specFromMeta(m, idx, a, nil))
		idx++
	}
	for _, p := range r.postProcessors {
		pn := postProcName(p)
		m, ok := postNodeMeta[pn]
		if !ok {
			panic(fmt.Sprintf("dag: post-processor %q has no node metadata", pn))
		}
		specs = append(specs, specFromMeta(m, idx, nil, p))
		idx++
	}
	return specs
}

// buildGraph collects the registry's node specs, validates the wiring,
// and derives the deterministic execution order. It is called once from
// NewDefaultRegistry; a wiring bug is a programmer error, so it panics
// with the validation message (dag_test.go asserts the default graph is
// valid so a panic can never ship). Registries built by hand (NewRegistry
// + Register*) never call this and fall back to registration-order
// execution in analyzeSource.
func (r *Registry) buildGraph() {
	r.specs = r.collectSpecs()
	sorted, err := buildDAG(r.specs)
	if err != nil {
		panic(err.Error())
	}
	r.nodes = sorted
	// Validate the lazy nodes against the eager set too, so a lazy Requires
	// typo is a startup panic (they read eager artifacts but never enter the
	// execution order — see materialize.go). r.specs / r.nodes are unaffected.
	if err := validateDAG(append(append([]nodeSpec(nil), r.specs...), lazyArtifactSpecs()...)); err != nil {
		panic(err.Error())
	}
}

// buildDAG validates the spec set and returns it in topological
// execution order. Used by NewDefaultRegistry and the tests.
func buildDAG(specs []nodeSpec) ([]nodeSpec, error) {
	if err := validateDAG(specs); err != nil {
		return nil, err
	}
	return topoSortDAG(specs)
}

// validateDAG checks that every provided artifact has exactly one
// provider and every required artifact has a provider. Errors name the
// offending artifact and node so a wiring typo is self-describing.
func validateDAG(specs []nodeSpec) error {
	provider := make(map[string]string, len(specs)*2)
	for _, s := range specs {
		for _, art := range s.Provides {
			if prev, ok := provider[art]; ok {
				return fmt.Errorf("dag: artifact %q is provided by both %q and %q", art, prev, s.Name)
			}
			provider[art] = s.Name
		}
	}
	for _, s := range specs {
		for _, req := range s.Requires {
			if _, ok := provider[req]; !ok {
				return fmt.Errorf("dag: node %q requires artifact %q, which no node provides", s.Name, req)
			}
		}
	}
	return nil
}

// topoSortDAG orders specs by Kahn's algorithm, breaking ties by
// registration index — an arbitrary deterministic tie-break (any valid
// order yields identical output; see TestOrderIndependence). Determinism
// is kept only so the default schedule, -graph output and PhaseTimings
// stay stable run-to-run. The scan is index-based (no map iteration on the ordering path), so
// the output is deterministic regardless of GOMAXPROCS / map seed.
//
// Assumes validateDAG has passed (every Requires has a unique provider);
// a remaining unschedulable node means a cycle, which is reported by name.
func topoSortDAG(specs []nodeSpec) ([]nodeSpec, error) {
	// Production tie-break: min registration index. Because registration
	// order is used as the tie-break among ready nodes.
	return topoSortDAGTieBreak(specs, func(i, j int) bool {
		return specs[i].regIndex < specs[j].regIndex
	})
}

// topoSortDAGTieBreak is topoSortDAG with a caller-supplied tie-break:
// among the ready (indegree-zero) nodes it schedules the one for which
// prefer(i, j) reports i should come before every other ready j. All
// declared edges are still respected, so every output is a valid
// topological order regardless of the tie-break. The seam exists so
// TestOrderIndependence can drive K seeded-random valid orders through
// analyzeSource and prove the Result is schedule-free (and, continuously,
// that the declared edge list is complete). Production always passes the
// regIndex comparator via topoSortDAG.
func topoSortDAGTieBreak(specs []nodeSpec, prefer func(i, j int) bool) ([]nodeSpec, error) {
	n := len(specs)
	provider := make(map[string]int, n*2)
	for i, s := range specs {
		for _, art := range s.Provides {
			provider[art] = i
		}
	}

	indeg := make([]int, n)
	adj := make([][]int, n)
	for i, s := range specs {
		seen := make(map[int]bool, len(s.Requires))
		for _, req := range s.Requires {
			p, ok := provider[req]
			if !ok || p == i || seen[p] {
				continue // unknown (validation catches), self, or duplicate edge
			}
			seen[p] = true
			adj[p] = append(adj[p], i)
			indeg[i]++
		}
	}

	order := make([]nodeSpec, 0, n)
	done := make([]bool, n)
	for len(order) < n {
		best := -1
		for i := 0; i < n; i++ {
			if done[i] || indeg[i] != 0 {
				continue
			}
			if best == -1 || prefer(i, best) {
				best = i
			}
		}
		if best == -1 {
			var stuck []string
			for i := 0; i < n; i++ {
				if !done[i] {
					stuck = append(stuck, specs[i].Name)
				}
			}
			return nil, fmt.Errorf("dag: dependency cycle among nodes %v", stuck)
		}
		done[best] = true
		order = append(order, specs[best])
		for _, m := range adj[best] {
			indeg[m]--
		}
	}
	return order, nil
}

// execOrder returns the node list analyzeSource drives execution from:
// the validated topological order for a default registry, or the raw
// registration order for a hand-built one (NewRegistry + Register*),
// which has no declared graph. Which valid order runs is immaterial to
// the output (TestOrderIndependence).
func (r *Registry) execOrder() []nodeSpec {
	if r.orderOverride != nil {
		return r.orderOverride // test-only injected valid topo order
	}
	if r.nodes != nil {
		return r.nodes
	}
	specs := make([]nodeSpec, 0, len(r.analyzers)+len(r.postProcessors))
	for _, a := range r.analyzers {
		specs = append(specs, nodeSpec{Name: a.Name(), analyzer: a})
	}
	for _, p := range r.postProcessors {
		specs = append(specs, nodeSpec{Name: postProcName(p), post: p})
	}
	return specs
}
