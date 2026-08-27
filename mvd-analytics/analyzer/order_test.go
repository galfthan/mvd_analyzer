package analyzer

// Phase 10 keystone: the pipeline's output must be a pure function of the
// demo — ANY valid topological order of the DAG (dag.go) must produce
// byte-identical JSON. TestOrderIndependence enforces that. It is an
// internal (package analyzer) test so it can reach the topoSortDAGTieBreak
// seam and the registry's orderOverride field without exposing a public
// API. The corpus loaders here are shared with timings_test.go.

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// orderCorpusEntry mirrors the fields of testdata/corpus.json this file
// needs (the golden harness has its own copy in package analyzer_test).
type orderCorpusEntry struct {
	GameID      int    `json:"gameId,omitempty"`
	Label       string `json:"label"`
	Mode        string `json:"mode,omitempty"`
	File        string `json:"file,omitempty"`
	ShotStreams bool   `json:"shotStreams,omitempty"`
}

// loadOrderCorpus reads testdata/corpus.json. Missing/empty => nil (skip).
func loadOrderCorpus(t *testing.T) []orderCorpusEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "corpus.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read corpus.json: %v", err)
	}
	var out []orderCorpusEntry
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse corpus.json: %v", err)
	}
	return out
}

// cachedDemoPath resolves the offline cache path for a corpus label,
// returning "" when the demo is not cached. Steady-state runs are offline
// (same contract as golden_test): no download here, we skip on cache miss.
// Local-only `file` entries resolve under testdata/ directly (never
// committed, never fetched — absent means skip, like a cache miss).
func cachedDemoPath(corpus []orderCorpusEntry, label string) string {
	for _, e := range corpus {
		if e.Label != label {
			continue
		}
		p := filepath.Join("..", "testdata", "cache", fmt.Sprintf("%d.mvd.gz", e.GameID))
		if e.File != "" {
			p = filepath.Join("..", "testdata", e.File)
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
		return ""
	}
	return ""
}

// shuffledTopoOrder returns a valid topological order of specs whose
// tie-break among ready nodes is a seeded-random permutation. Every
// declared edge is still respected (topoSortDAGTieBreak guarantees it), so
// the result is a legitimate schedule that differs from the production
// (regIndex) order — exactly the coverage TestOrderIndependence needs.
func shuffledTopoOrder(t *testing.T, specs []nodeSpec, seed int64) []nodeSpec {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	prio := rng.Perm(len(specs)) // random total order over spec positions
	ord, err := topoSortDAGTieBreak(specs, func(i, j int) bool {
		return prio[i] < prio[j]
	})
	if err != nil {
		t.Fatalf("shuffled topo sort (seed %d): %v", seed, err)
	}
	return ord
}

// deferredTopoOrder returns a valid topological order that runs `late` as
// LATE as its edges allow: among the ready nodes it is picked only when
// nothing else is ready. Random seeds sample the schedule space; this is
// the adversarial probe for one specific anti-dependency — a node that
// must run before another because the second one OVERWRITES state the
// first one reads (co.DemoInfo.Players[].Team; see roster.go). Deferring
// the reader is the schedule that exposes a missing edge every time,
// where a fixed seed only exposes it by luck.
func deferredTopoOrder(t *testing.T, specs []nodeSpec, late string) []nodeSpec {
	t.Helper()
	idx := -1
	for i := range specs {
		if specs[i].Name == late {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("no node named %q in the registry specs", late)
	}
	ord, err := topoSortDAGTieBreak(specs, func(i, j int) bool {
		if (i == idx) != (j == idx) {
			return j == idx // the deferred node loses every tie-break
		}
		return i < j
	})
	if err != nil {
		t.Fatalf("deferred topo sort (%s last): %v", late, err)
	}
	return ord
}

// orderSeeds are fixed so a failure reproduces exactly.
var orderSeeds = []int64{0x5eed01, 0x5eed02, 0x5eed03}

// TestOrderIndependence proves (a) the pipeline's JSON output is
// schedule-free — the default order and K=3 seeded-random valid
// topological orders all marshal to byte-identical Result JSON — and, as a
// standing consequence, (b) that the declared DAG edge list is COMPLETE:
// an undeclared cross-node read (a Finalize/post that silently depends on
// another node's Result/CoreOutputs write, or an event-pass inter-collector
// coupling — shuffling reorders OnEvent delivery too) surfaces here as a
// byte diff rather than staying invisible behind the frozen registration
// order.
//
// Each order runs on a FRESH registry (analyzers are stateful across runs)
// via the orderOverride seam. Representative modes: one 1on1 (duel path),
// one 2on2, one 4on4. Skips like golden_test when the cache is absent.
func TestOrderIndependence(t *testing.T) {
	corpus := loadOrderCorpus(t)
	if len(corpus) == 0 {
		t.Skip("testdata/corpus.json has no entries")
	}

	labels := []string{
		"1on1_bananfalco_betowen_240426_dm2", // duel path
		"2on2_nani_pora_210426_dm6",          // 2on2
		"4on4_ahoy_bhb_240426_obsidian",      // 4on4 (smallest 4on4, keeps budget)
		"2on2_archive_dm4_qw240_recon",       // pre-instrumentation: damage reconstruction in the schedule
		// No demoinfo block, two participants, serverinfo teamplay 4: the
		// shape where MatchAnalyzer.Finalize is what settles the duel verdict
		// and the mode descriptor, mutating structs the roster node
		// published. Every reader binds `roster:final` for it (dag.go);
		// without that edge `frag` flagged team kills from the pre-promotion
		// verdict under one order and the promoted one under another.
		"duel_archive_dm3_arena_teamplay",
		// Individual layout from a mode with no teamplay: the roster node
		// rewrites co.DemoInfo.Players[].Team to each player's own name, and
		// identity reads the raw tag out of that same struct. keith's
		// `match.players[].rawTeam` vanished whenever roster was scheduled
		// first (so did betowen's on the duel above), which is what the
		// roster→identity edge and the deferred-identity order below pin.
		"ffa_countdown_dm6_260106",
	}

	// runOnce analyzes path on a FRESH default registry (analyzers are
	// stateful across runs). When seed != nil it forces a shuffled but
	// still-valid topological order built from THIS registry's own specs —
	// so orderOverride carries this registry's analyzer/post handles, never
	// a sibling's — then marshals the Result. Full json.Marshal (not the
	// sampled golden form) is the strongest byte comparison; filePath is
	// identical across runs (same demo), so no canonicalisation is needed.
	runOnce := func(t *testing.T, path string, streams bool, order func(*testing.T, []nodeSpec) []nodeSpec) []byte {
		r := NewDefaultRegistry()
		r.BuildShotStreams = streams
		if order != nil {
			r.orderOverride = order(t, r.specs)
		}
		res, err := r.Analyze(path)
		if err != nil {
			t.Fatalf("analyze: %v", err)
		}
		raw, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return raw
	}

	for _, label := range labels {
		label := label
		t.Run(label, func(t *testing.T) {
			streams, localOnly := false, false
			for _, e := range corpus {
				if e.Label == label {
					streams, localOnly = e.ShotStreams, e.File != ""
				}
			}
			path := cachedDemoPath(corpus, label)
			if path == "" {
				if localOnly {
					t.Skipf("local demo %q not present — per-machine coverage (see corpus.json for provenance)", label)
				}
				t.Skipf("demo %q not cached — run golden corpus online once to populate", label)
			}

			// Reference: the production (regIndex tie-break) order.
			want := runOnce(t, path, streams, nil)

			for _, seed := range orderSeeds {
				seed := seed
				got := runOnce(t, path, streams, func(t *testing.T, specs []nodeSpec) []nodeSpec {
					return shuffledTopoOrder(t, specs, seed)
				})
				if !bytesEqual(want, got) {
					t.Fatalf("schedule-dependent output for seed %#x: JSON differs from default order (%s).\nThis means an undeclared cross-node dependency — add the missing DAG edge or fix the coupling; do NOT pin order to hide it.",
						seed, jsonFirstDiff(want, got))
				}
			}

			// The adversarial schedules: run one node as late as its edges
			// allow. `identity` is the one that must run before `roster`
			// overwrites the demoinfo team tags in place, and no seed is
			// guaranteed to produce that order.
			for _, late := range []string{"identity", "roster"} {
				late := late
				got := runOnce(t, path, streams, func(t *testing.T, specs []nodeSpec) []nodeSpec {
					return deferredTopoOrder(t, specs, late)
				})
				if !bytesEqual(want, got) {
					t.Fatalf("schedule-dependent output with %q deferred: JSON differs from default order (%s).\nThis means an undeclared cross-node dependency — add the missing DAG edge or fix the coupling; do NOT pin order to hide it.",
						late, jsonFirstDiff(want, got))
				}
			}
		})
	}
}

// bytesEqual is a local byte-slice equality (avoids an import just for one
// call and keeps the failure path self-contained).
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// jsonFirstDiff summarises where two JSON blobs first disagree, enough to
// point at the offending Result section.
func jsonFirstDiff(want, got []byte) string {
	n := len(want)
	if len(got) < n {
		n = len(got)
	}
	for i := 0; i < n; i++ {
		if want[i] != got[i] {
			lo := i - 30
			if lo < 0 {
				lo = 0
			}
			hi := i + 30
			if hi > n {
				hi = n
			}
			return fmt.Sprintf("first diff at byte %d: want …%q… got …%q…", i, string(want[lo:hi]), string(got[lo:hi]))
		}
	}
	return fmt.Sprintf("common prefix identical; lengths differ want=%d got=%d", len(want), len(got))
}
