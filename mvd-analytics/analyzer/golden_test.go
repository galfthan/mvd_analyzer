package analyzer_test

// Golden test harness for the full analyzer pipeline. Reads
// qwanalytics/testdata/corpus.json (a list of hub.quakeworld.nu game
// IDs, plus optional local-only `file` entries for demos the hub
// doesn't have), pulls each hub demo into the local cache (downloading
// on first run, reusing on subsequent runs), runs the default registry,
// and pins the JSON-serialised Result against a checked-in golden file.
// File entries skip when the demo is absent on this machine.
//
// Usage:
//   make test                            # normal run; downloads on cache miss
//   go test ./qwanalytics/... -run TestGoldenCorpus -args -update-golden
//                                        # regenerate golden files after an
//                                        # intentional change
//
// The corpus.json manifest is committed; the cache/ directory is
// gitignored. A demo's golden file lives at testdata/golden/<label>.json.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/hubfetch"
	"github.com/mvd-analyzer/mvd-analytics/mapbsp"
)

// updateGolden regenerates every golden file from the current pipeline
// output instead of comparing. Activate with `go test ... -args -update-golden`.
var updateGolden = flag.Bool("update-golden", false, "regenerate golden files instead of comparing")

// corpusEntry mirrors one row in qwanalytics/testdata/corpus.json.
//
// gameId  → hub.quakeworld.nu game ID; resolved via hubfetch.
// label   → stable filename slug for the cache + golden file. Choose
//
//	something descriptive (e.g. "duel_dm6_2024-01") so a
//	regression diff makes sense without cross-referencing the ID.
//
// mode    → free-text human label ("1on1", "2on2", "4on4", …) — not
//
//	checked, just there so a reader of corpus.json can see
//	coverage at a glance.
//
// file    → optional path (relative to testdata/) naming a local-only
//
//	demo instead of a hub gameId. Demos are never committed
//	(testdata/demos/ is gitignored); the entry SKIPS when the
//	file is absent — per-machine coverage, same contract as the
//	special-cases corpus. Used for pre-instrumentation archive
//	demos the hub doesn't have.
//
// sha256  → provenance of a file entry: the demo archive's key (hash of
//
//	the uncompressed .mvd), so the demo can be re-obtained on
//	request. Informational; not verified here.
//
// shotStreams → analyze with Registry.BuildShotStreams so projectile /
//
//	beam / point-effect streams are built and damage
//	reconstruction runs on pre-instrumentation demos instead
//	of standing down.
type corpusEntry struct {
	GameID      int    `json:"gameId,omitempty"`
	Label       string `json:"label"`
	Mode        string `json:"mode,omitempty"`
	File        string `json:"file,omitempty"`
	Sha256      string `json:"sha256,omitempty"`
	ShotStreams bool   `json:"shotStreams,omitempty"`
}

func TestGoldenCorpus(t *testing.T) {
	corpus := loadCorpus(t)
	if len(corpus) == 0 {
		t.Skip("qwanalytics/testdata/corpus.json has no entries — add hub gameIds to enable golden coverage")
	}

	cacheDir := filepath.Join("..", "testdata", "cache")
	goldenDir := filepath.Join("..", "testdata", "golden")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("create cache dir: %v", err)
	}
	if err := os.MkdirAll(goldenDir, 0o755); err != nil {
		t.Fatalf("create golden dir: %v", err)
	}

	// A BSP-skipped case is a golden comparison that did NOT happen. Left to
	// t.Skip alone that is invisible without -v, so a misconfigured BSP dir
	// reports a green suite over zero compared demos (a relative
	// MVDA_BSP_DIR used to do exactly that). Collect them and say so once, on
	// stderr, where a passing run still shows it.
	var bspSkipped []string
	var compared int

	for _, entry := range corpus {
		t.Run(entry.Label, func(t *testing.T) {
			mvdPath := ensureCached(t, cacheDir, entry)

			reg := analyzer.NewDefaultRegistry()
			reg.BuildShotStreams = entry.ShotStreams
			result, err := reg.Analyze(mvdPath)
			if err != nil {
				t.Fatalf("analyze %s: %v", entry.Label, err)
			}

			// The golden includes BSP-derived columns (pos.h floor height,
			// pos.lq liquid) and floor-polygon loc resolution. When the
			// map's BSP can't be resolved (no MVDA_BSP_DIR, no ./bsps) those
			// degrade or vanish, so a comparison fails for the wrong reason
			// and an -update-golden would overwrite a good golden with
			// gutted data. Skip the comparison and refuse to regenerate.
			if m := result.EffectiveMap(); m != "" && mapbsp.LoadBytes(m) == nil {
				msg := fmt.Sprintf("%s: BSP for map %q not resolvable — set MVDA_BSP_DIR to the full map set (lookup order: MVDA_BSP_DIR, ./bsps)",
					entry.Label, m)
				if *updateGolden {
					t.Fatalf("refusing to regenerate %s — %s; would write a golden missing height/liquid/loc", entry.Label, msg)
				}
				bspSkipped = append(bspSkipped, entry.Label+" ("+m+")")
				t.Skipf("%s", msg)
			}

			// Line of sight is computed lazily (not by the default pipeline),
			// so invoke it explicitly here to keep real-demo LOS regression
			// coverage in the goldens. Gated on the BSP being resolvable above.
			analyzer.ComputeLOS(result)

			// Cross-check the derived victim-weapon kill split against
			// KTX's own ekills. Rides along here rather than in its own
			// test so it costs no second pass over the corpus.
			checkEnemyWeaponKillsVsKTX(t, result, entry.Label)

			actual, err := canonicalJSON(result, entry.Label)
			if err != nil {
				t.Fatalf("canonicalise: %v", err)
			}

			goldenPath := filepath.Join(goldenDir, entry.Label+".json")
			if *updateGolden {
				if err := os.WriteFile(goldenPath, actual, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				t.Logf("updated %s", goldenPath)
				return
			}

			expected, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden %s: %v\nrun with -args -update-golden to create it", goldenPath, err)
			}
			compared++
			if !bytes.Equal(expected, actual) {
				t.Errorf("%s differs from golden — run with -args -update-golden if intended.\nfirst diff line: %s",
					entry.Label, firstDiffLine(expected, actual))
			}
		})
	}

	if len(bspSkipped) == 0 {
		return
	}
	summary := fmt.Sprintf("compared %d of %d demos — %d skipped, BSP not resolvable (MVDA_BSP_DIR=%q, lookup order: MVDA_BSP_DIR, ./bsps).\n  these demos were NOT checked against their goldens:\n  %s",
		compared, len(corpus), len(bspSkipped),
		os.Getenv("MVDA_BSP_DIR"), strings.Join(bspSkipped, "\n  "))
	fmt.Fprintf(os.Stderr, "\nWARNING: TestGoldenCorpus %s\n  run `make bsps` to populate the repo-root bsps/ map set.\n\n", summary)
	// An unpopulated repo-root bsps/ is a fresh-clone state: skip, as the
	// README documents. A dir the caller ASKED for that resolves nothing is a
	// misconfiguration, and reporting green over zero golden comparisons is
	// the failure mode this check exists for.
	if compared == 0 && bspDirExplicit {
		t.Fatalf("no golden comparison ran: %s", summary)
	}
}

// loadCorpus reads testdata/corpus.json. Missing or empty file is
// treated as "skip" — see the t.Skip in the caller.
func loadCorpus(t *testing.T) []corpusEntry {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "corpus.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read corpus.json: %v", err)
	}
	var out []corpusEntry
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse corpus.json: %v", err)
	}
	return out
}

// ensureCached returns the local path to the demo for entry.GameID,
// downloading via hubfetch on cache miss. The cache key is the gameId
// itself (label-derived would invalidate every time the user renames a
// label without changing the underlying demo).
func ensureCached(t *testing.T, cacheDir string, entry corpusEntry) string {
	t.Helper()
	if entry.File != "" {
		p := filepath.Join("..", "testdata", entry.File)
		if _, err := os.Stat(p); err != nil {
			t.Skipf("local demo %s (%s) not present — per-machine coverage, skipping. Archive sha256 %s; ask a maintainer for the demo to enable it.",
				entry.File, entry.Label, entry.Sha256)
		}
		return p
	}
	cachePath := filepath.Join(cacheDir, fmt.Sprintf("%d.mvd.gz", entry.GameID))
	if _, err := os.Stat(cachePath); err == nil {
		return cachePath
	}

	client := hubfetch.NewClient()
	if client.SupabaseURL == "" || client.APIKey == "" {
		t.Fatalf("demo %d (%s) not in cache and the hub is not configured — set %s / %s (and %s) to fetch on cache miss, or populate %s manually. Steady-state golden runs are offline; only a cold cache needs the hub.",
			entry.GameID, entry.Label,
			hubfetch.EnvSupabaseURL, hubfetch.EnvSupabaseKey, hubfetch.EnvCDNURL, cachePath)
	}
	if !networkAllowed(client) {
		t.Fatalf("demo %d (%s) not in cache and network probe failed — populate %s manually or run online once",
			entry.GameID, entry.Label, cachePath)
	}

	t.Logf("cache miss for game %d (%s) — fetching from hub", entry.GameID, entry.Label)
	info, err := client.Resolve(entry.GameID)
	if err != nil {
		t.Fatalf("resolve gameId %d: %v", entry.GameID, err)
	}
	data, err := client.Download(info)
	if err != nil {
		t.Fatalf("download gameId %d: %v", entry.GameID, err)
	}
	if err := os.WriteFile(cachePath, data, 0o644); err != nil {
		t.Fatalf("write cache %s: %v", cachePath, err)
	}
	return cachePath
}

// networkAllowed does a 2-second HEAD against the configured Supabase
// host. If that fails, we treat the environment as offline so the test can
// give a clean "populate the cache" message instead of a generic timeout.
// The caller has already checked the client is configured.
func networkAllowed(client *hubfetch.Client) bool {
	u, _ := url.Parse(client.SupabaseURL)
	if u == nil {
		return false
	}
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Head("https://" + u.Host)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return true
}

// canonicalJSON marshals Result to deterministic JSON for golden
// comparison. Three transforms are applied:
//
//  1. filePath is stripped — it's a per-machine cache path that would
//     force a diff on every developer machine.
//  2. streams.players[].* time series are sliced down to three 15 s
//     windows (start, 1:00–1:15, last 15 s). The full native-rate
//     position track is ~18 MB per 4on4 demo; sparse change streams
//     and intervals are smaller but together still bloat the corpus.
//     The three windows are enough sampling to catch bucketer /
//     stream-emitter drift while keeping committed goldens around 1 MB.
//  3. unless keepPos, the dense per-sample position/view track
//     (streams.players[].pos) is dropped entirely — see
//     dropPositionTracks. It is ~60 % of every file but exercises the
//     same emitter / BSP-trace code on every demo, so it is pinned on
//     just densePosDemos; the rest still verify the light change
//     streams, intervals, locGraph, region-control and aggregates.
//
// Everything else (locGraph, schemaVersion, durations, weapon stats,
// items, frags, …) is pinned in full; changes to those should be
// deliberate, and -update-golden makes the intent explicit.
func canonicalJSON(v interface{}, label string) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if paths, ok := partialGoldenDemos[label]; ok {
		m, err = projectPaths(m, paths)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		out, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return nil, err
		}
		return append(out, '\n'), nil
	}
	delete(m, "filePath")
	sampleStreams(m)
	if !densePosDemos[label] {
		dropPositionTracks(m)
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// densePosDemos are the labels whose golden keeps the full per-sample
// position/view track (streams.players[].pos: x/y/z, vp/vya, h/lq/li,
// velocity). One full 4on4 (obsidian — eight players, exercises the
// height + liquid BSP traces under load) and one duel (dm2 — the
// two-player case) are enough to catch drift in the position / view /
// height / liquid pipeline, which runs identically across demos.
// Pinning it on all ten would roughly double the committed corpus for
// no extra coverage.
var densePosDemos = map[string]bool{
	"4on4_ahoy_bhb_240426_obsidian":      true,
	"1on1_bananfalco_betowen_240426_dm2": true,
}

// partialGoldenDemos maps a label to the dotted paths its golden pins
// instead of the full Result. Use it for demos added to regression-pin
// one specific feature: their full output only re-covers what the other
// goldens already pin, at megabytes per demo (the demomark demo's full
// golden was 4.4 MB even after dropping its ~3.4 MB compact mover
// stream; the projection is under a kilobyte). Everything outside the
// listed paths — including schemaVersion — is excluded, so these
// goldens also sit out ordinary schema-bump regenerations. streams.global
// (a few hundred bytes: matchStart/matchEnd/demoOffset/pauses) rides
// along so TestTimelineInvariants keeps its match-relative window check
// over the pinned events.
var partialGoldenDemos = map[string][]string{
	"4on4_tbg_pex_080326_dm2_demomark": {"timelineAnalysis.demoMarkers", "streams.global"},
	// The two identity demos (schema v66). Both are ordinary 4on4s
	// otherwise — a full golden would add ~11 MB of output the nine other
	// goldens already pin — so they pin the userid carriers, the roster,
	// and streams.global (for TestTimelineInvariants' window check).
	//
	// 220637: rusti reconnects while his first connection is still
	// spawned, so mvdsv renames him `(1)rusti (FU)` and KTX scores the two
	// separately. Pins the two-row split AND that each row keeps its own
	// userid (37 / 43) rather than the id of the spectator who held the
	// slot first.
	//
	// 222649: bogojoker times out and rejoins on the SAME slot with a new
	// userid under one name, which is the only case where "last session
	// with play" is the difference between a live id (25) and a dead one
	// (12) — the shape no wire-side invariant can catch, because the dead
	// id really was his.
	//
	// Both also pin the v66 identity EXPORT (streams.players[].identity /
	// .sessions, via the `[]` array projection): 220637 is the only demo
	// where two rows must carry DIFFERENT identities, and 222649 the only
	// one where a single identity spans two sessions with two userids on
	// one slot — the exact contrast the export exists to publish, and one
	// no other golden can pin.
	"4on4_fu_mix_060626_dm2_rename_handover": {
		"timelineAnalysis.playerUserIDs", "timelineAnalysis.fragStreaks",
		"timelineAnalysis.powerupEvents", "timelineAnalysis.airgibs",
		"match.players", "streams.global",
		"streams.players[].name", "streams.players[].identity", "streams.players[].sessions",
	},
	"4on4_blue_red_200626_e1m2_sameslot_rejoin": {
		"timelineAnalysis.playerUserIDs", "timelineAnalysis.fragStreaks",
		"timelineAnalysis.powerupEvents", "timelineAnalysis.airgibs",
		"match.players", "streams.global",
		"streams.players[].name", "streams.players[].identity", "streams.players[].sessions",
	},
	// The 11-player FFA (schema v75). It pins what no other golden can: the
	// individual match layout at scale (eleven one-player teams, nine
	// distinct clan tags on match.players[].rawTeam, zero team kills), a
	// spectator who JOINS mid-match on a slot he was already watching from
	// (anus, uid 50, at 21926 ms — the player↔spectator occupancy split), and
	// a slot handover on top of it (xuxi leaves slot 4 on 10 frags at 63290,
	// SMOK takes it at 76007). Projected rather than full: the eleven streams
	// would add ~15 MB the other FFA goldens already pin, and these are the
	// rows the demo was added for.
	"ffa_matchless_shifter_260119_spectate": {
		"match.players", "match.teams", "match.sources", "match.gameMode",
		"streams.global",
		"streams.players[].name", "streams.players[].identity", "streams.players[].sessions",
	},
	// The two-player matchless FFA on nova (nexus vs SMOK) — the
	// two-participant end of the individual layout, the one the web lays
	// out as a duel. Projected to the three things no other golden pins:
	// `match` (the scoreboard this demo's frag fold produces),
	// `streams.global`, and `timelineAnalysis.fragEvents` —
	// the last for the post-match drop gate, where nexus quits 3.7 s past the
	// match-over print and SV_DropClient's scoreboard zeroing
	// (mvdsv/src/sv_main.c:419-428) must NOT reach the frag timeline
	// (TestFragUpdate_AfterMatchEndNotRecorded pins the unit shape; this pins
	// it on the wire). The full Result was 22 k lines of streams, playerStats
	// and aim the other goldens already pin.
	"ffa_matchless_nova_260704": {
		"match", "streams.global", "timelineAnalysis.fragEvents",
	},
	// The 8-player matchless FFA with a mid-match leaver (Player36 at
	// 85.7 s), a leave-and-rejoin on a different slot with a real player in
	// between (nexus: slot 4 → xra → slot 3) and the frag fold that rejoin
	// exercises (14 announced on leaving, 0 on the second stint, 14 on the
	// row — MakeGhost returns in matchless mode, ktx/src/client.c:2897).
	// Projected to the scoreboard, the sessions and the per-player windows:
	// the eight full streams were 8 MB of what `nova` already pins.
	//
	// It is ALSO the corpus's only `shotStreams: true` entry with a WIRE
	// (KTX) damage section, which makes it the only golden that can pin the
	// gl touch classifier's shipped path: every other hub entry parses
	// without the spatial streams and takes the WITHHELD branch, and the one
	// other enriched entry (2on2_archive_dm4_qw240_recon) has reconstructed
	// damage and so runs the reconstructed tier instead. The aim paths below
	// therefore pin what `aim.players[].weapons[].direct` / `.splash` are on
	// a modern demo the classifier RAN on — including their presence, which
	// since v75 is the difference between a measured zero and a withhold
	// (result.WeaponAim) — and `playerStats.players[].accuracy` pins the
	// consequence: gl on KTX's own `directImpact` scale rather than the
	// any-path fallback.
	"ffa_matchless_dm2_260116_joiners": {
		"match.players", "match.teams", "match.sources", "match.gameMode",
		"streams.global",
		"streams.players[].name", "streams.players[].identity", "streams.players[].sessions",
		"playerStats.players[].name", "playerStats.players[].score",
		"playerStats.players[].window", "playerStats.players[].sessions",
		"playerStats.players[].accuracy",
		"frags.frags",
		"aim.players[].player",
		"aim.players[].weapons[].weapon", "aim.players[].weapons[].shots",
		"aim.players[].weapons[].hits", "aim.players[].weapons[].direct",
		"aim.players[].weapons[].splash", "aim.players[].weapons[].missed",
	},
	// The countdown-style FFA (`The match has begun!` at 10 s, KTX demoinfo
	// block). It pins what the matchless ones cannot: the individual
	// rewrite applied to demoInfo.players[].team / demoInfo.teams, and the
	// locGraph occupancy keyed by player name rather than clan tag. Its
	// streams / shots / aim are the same shape the 1on1 goldens pin.
	"ffa_countdown_dm6_260106": {
		"match", "demoInfo", "locGraph", "streams.global",
		"streams.players[].name", "streams.players[].identity", "streams.players[].sessions",
	},
}

// projectPaths reduces m to only the given dotted paths, preserving the
// nesting so the golden keeps the Result's shape.
//
// A `[]` suffix maps the rest of the path over an array:
// "streams.players[].identity" keeps one small object per player instead of
// the megabytes of stream a bare "streams.players" would drag in.
//
// A path that matches NOTHING is an error, not a silent omission: a
// projection is the whole statement of what its golden pins, and a path that
// quietly resolves to nothing pins nothing while reading as though it does.
// Three of the FFA entries carried `playerStats.teams` that way — always
// absent, because an individual layout publishes no team rows — so the
// golden's silence about them was mistaken for a pinned null. (`streams.global`
// on every projected entry is not redundant: TestTimelineInvariants reads its
// demoOffset / matchEnd to bound every event time in the golden.)
func projectPaths(m map[string]interface{}, paths []string) (map[string]interface{}, error) {
	out := map[string]interface{}{}
	for _, path := range paths {
		if !projectInto(m, out, strings.Split(path, ".")) {
			return nil, fmt.Errorf("projection path %q matched nothing in the Result", path)
		}
	}
	return out, nil
}

// projectInto copies one dotted path from src into dst, reporting whether the
// path resolved to anything at all.
func projectInto(src, dst map[string]interface{}, parts []string) bool {
	key := parts[0]
	if strings.HasSuffix(key, "[]") {
		key = strings.TrimSuffix(key, "[]")
		items, ok := src[key].([]interface{})
		if !ok {
			return false
		}
		// A present-but-empty array is a pinned value (a fragless demo's
		// `frags: []`), distinct from an absent key: it matches, and any
		// suffix trivially matches over zero rows.
		if len(parts) == 1 || len(items) == 0 {
			dst[key] = items
			return true
		}
		rows, _ := dst[key].([]interface{})
		if rows == nil {
			rows = make([]interface{}, len(items))
			for i := range rows {
				rows[i] = map[string]interface{}{}
			}
			dst[key] = rows
		}
		if len(rows) != len(items) {
			return false
		}
		matched := false
		for i, item := range items {
			in, okIn := item.(map[string]interface{})
			row, okRow := rows[i].(map[string]interface{})
			if okIn && okRow && projectInto(in, row, parts[1:]) {
				matched = true
			}
		}
		return matched
	}
	if len(parts) == 1 {
		v, ok := src[key]
		if !ok {
			return false
		}
		dst[key] = v
		return true
	}
	next, ok := src[key].(map[string]interface{})
	if !ok {
		return false
	}
	sub, ok := dst[key].(map[string]interface{})
	if !ok {
		sub = map[string]interface{}{}
		dst[key] = sub
	}
	return projectInto(next, sub, parts[1:])
}

// dropPositionTracks removes streams.players[].pos — the dense
// per-sample position/view/height/liquid track that dominates the
// corpus size — from every player. Called for demos outside
// densePosDemos; the rest of streams.players (light change streams,
// intervals, spawn/death timestamps) and all aggregate sections are
// kept, so each demo still verifies loc graph, region control, items,
// frags, damage and the sparse timelines.
func dropPositionTracks(m map[string]interface{}) {
	streams, ok := m["streams"].(map[string]interface{})
	if !ok {
		return
	}
	players, ok := streams["players"].([]interface{})
	if !ok {
		return
	}
	for _, pi := range players {
		if p, ok := pi.(map[string]interface{}); ok {
			delete(p, "pos")
		}
	}
}

// goldenWindows is the three 15-second windows used to slice every
// per-player time series in the golden corpus. Schema v8: all stream
// timestamps are int32 ms in the JSON, so the window bounds are
// expressed in ms (0..15000, 60000..75000). Match ends at
// global.matchEnd; the trailing window starts at matchEnd - 15000.
var goldenWindows = []struct{ start, end float64 }{
	{0, 15000},
	{60000, 75000},
	// (end - 15000, end) appended dynamically per demo.
}

// sampleStreams replaces every per-player time series in
// streams.players[] with the concatenation of three 15-second windows.
// The global matchEnd is read from streams.global.matchEnd (ms).
func sampleStreams(m map[string]interface{}) {
	streams, ok := m["streams"].(map[string]interface{})
	if !ok {
		return
	}
	matchEnd := 0.0
	if g, ok := streams["global"].(map[string]interface{}); ok {
		if t, ok := g["matchEnd"].(float64); ok {
			matchEnd = t
		}
	}
	windows := append([]struct{ start, end float64 }(nil), goldenWindows...)
	if matchEnd > 15000 {
		windows = append(windows, struct{ start, end float64 }{matchEnd - 15000, matchEnd})
	}

	players, ok := streams["players"].([]interface{})
	if !ok {
		return
	}
	for _, pi := range players {
		p, ok := pi.(map[string]interface{})
		if !ok {
			continue
		}
		// Change streams: []{t, v}.
		for _, key := range []string{"h", "a", "at", "li", "sh", "nl", "rk", "cl"} {
			if arr, ok := p[key].([]interface{}); ok {
				p[key] = filterChangeStream(arr, windows)
			}
		}
		// Intervals: []{s, e}. Clamp to overlapping window slice.
		for _, key := range []string{"rl", "lg", "gl", "ssg", "sng", "q", "pe", "r", "alive"} {
			if arr, ok := p[key].([]interface{}); ok {
				p[key] = filterIntervalStream(arr, windows)
			}
		}
		// Discrete event timestamps.
		for _, key := range []string{"sp", "d"} {
			if arr, ok := p[key].([]interface{}); ok {
				p[key] = filterTimestamps(arr, windows)
			}
		}
		// Position track (columnar) — slice every sample-aligned column
		// by the same kept indices. A column left at full length would
		// ship misaligned against the sliced t (and bloat the golden).
		if pos, ok := p["pos"].(map[string]interface{}); ok {
			ts, _ := pos["t"].([]interface{})
			keepIdx := indicesInWindows(ts, windows)
			for _, key := range []string{"t", "x", "y", "z", "li", "h", "lq", "vp", "vya", "vx", "vy", "vz"} {
				if arr, ok := pos[key].([]interface{}); ok {
					pos[key] = pickByIndex(arr, keepIdx)
				}
			}
		}
	}
}

func filterChangeStream(arr []interface{}, windows []struct{ start, end float64 }) []interface{} {
	out := arr[:0:0]
	for _, e := range arr {
		em, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		t, ok := em["t"].(float64)
		if !ok {
			continue
		}
		if inGoldenWindows(t, windows) {
			out = append(out, e)
		}
	}
	return out
}

func filterIntervalStream(arr []interface{}, windows []struct{ start, end float64 }) []interface{} {
	out := arr[:0:0]
	for _, e := range arr {
		em, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		s, _ := em["s"].(float64)
		ee, _ := em["e"].(float64)
		// Keep intervals overlapping any window.
		for _, w := range windows {
			if ee > w.start && s < w.end {
				out = append(out, e)
				break
			}
		}
	}
	return out
}

func filterTimestamps(arr []interface{}, windows []struct{ start, end float64 }) []interface{} {
	out := arr[:0:0]
	for _, e := range arr {
		t, ok := e.(float64)
		if !ok {
			continue
		}
		if inGoldenWindows(t, windows) {
			out = append(out, e)
		}
	}
	return out
}

func indicesInWindows(ts []interface{}, windows []struct{ start, end float64 }) []int {
	out := make([]int, 0, len(ts))
	for i, e := range ts {
		t, ok := e.(float64)
		if !ok {
			continue
		}
		if inGoldenWindows(t, windows) {
			out = append(out, i)
		}
	}
	return out
}

func pickByIndex(arr []interface{}, idx []int) []interface{} {
	out := make([]interface{}, 0, len(idx))
	for _, i := range idx {
		if i < len(arr) {
			out = append(out, arr[i])
		}
	}
	return out
}

func inGoldenWindows(t float64, windows []struct{ start, end float64 }) bool {
	for _, w := range windows {
		if t >= w.start && t <= w.end {
			return true
		}
	}
	return false
}

// firstDiffLine returns a short summary of where two byte slices first
// disagree. Just enough context to point a developer at the right
// area in the golden file — the full diff is reproducible by writing
// the actual bytes and running `diff`.
func firstDiffLine(want, got []byte) string {
	line, col := 1, 1
	for i := 0; i < len(want) && i < len(got); i++ {
		if want[i] != got[i] {
			return fmt.Sprintf("line %d col %d: want %q got %q",
				line, col, snippet(want, i), snippet(got, i))
		}
		if want[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	if len(want) != len(got) {
		return fmt.Sprintf("length differs: want %d got %d (likely missing/extra trailing field)", len(want), len(got))
	}
	return "(no difference)"
}

func snippet(b []byte, i int) string {
	start := i
	end := i + 40
	if end > len(b) {
		end = len(b)
	}
	return string(b[start:end])
}
