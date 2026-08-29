package democache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// corpusDemo returns a real demo from the analytics test cache, or skips when
// it is absent (the cache is gitignored — present after a golden run, offline
// otherwise).
func corpusDemo(t *testing.T) (sha string, bytes []byte) {
	t.Helper()
	const rel = "../../../mvd-analytics/testdata/cache/211161.mvd.gz"
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Skipf("corpus demo not present (%v); run the golden corpus first", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), b
}

// TestColdParseBuildsEnrichedStreams: since phase 12 the API cache parse is
// always-full (defaultParse sets BuildShotStreams+BuildNails), so a single cold
// GetResult produces an enriched Result — the spatial weapon-fire streams are
// present and their latches are set — with no second parse and no
// EnsureShotStreams. This is the deleted lazy path's behaviour folded into the
// base parse; /shots, /aim and /streams/* read it straight off the Result.
func TestColdParseBuildsEnrichedStreams(t *testing.T) {
	sha, demo := corpusDemo(t)
	root := t.TempDir()
	mp := mvdPath(root, sha)
	if err := os.MkdirAll(filepath.Dir(mp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mp, demo, 0o644); err != nil {
		t.Fatal(err)
	}

	c := New(root, nil) // real defaultParse (always-full)
	id := DemoID{Kind: "sha256", SHA: sha}

	res, _, err := c.GetResult(context.Background(), id)
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if res.Streams == nil || res.Streams.Projectiles == nil {
		t.Fatal("cold parse did not build the projectile stream")
	}
	if !res.Streams.ShotStreamsComputed {
		t.Error("ShotStreamsComputed not latched by the always-full parse")
	}
	if !res.Streams.NailsComputed {
		t.Error("NailsComputed not latched by the always-full parse")
	}
	// The enriched Shots/Aim ride along on the base Result — no re-parse.
	if res.Shots == nil || res.Aim == nil {
		t.Fatalf("Shots/Aim absent: shots=%v aim=%v", res.Shots != nil, res.Aim != nil)
	}
	// The stream-derived aim splits are present, which is the thing the
	// always-full parse buys and a lean one cannot: gl's Direct is the
	// flight-geometry touch classifier's count, and the classifier reads the
	// spatial shot streams. WITHHELD IS NIL (result.WeaponAim), so a nil here
	// is the withheld branch reaching the API's own cold parse — the failure
	// this test exists to catch. rl's three classes additionally partition its
	// fires; gl's cannot, because its Direct is bounded by the fires rather
	// than by the hits the linker connected.
	glRows := 0
	streamDerived := false
	for _, pa := range res.Aim.Players {
		for _, wa := range pa.Weapons {
			if wa.Shots == 0 {
				continue
			}
			switch wa.Weapon {
			case "rl":
				streamDerived = true
				if wa.Direct == nil || wa.Splash == nil {
					t.Errorf("%s rl: direct/splash withheld (%v/%v) on the always-full parse",
						pa.Player, wa.Direct, wa.Splash)
					continue
				}
				if got := *wa.Direct + *wa.Splash + wa.Missed; got != wa.Shots {
					t.Errorf("%s %s: direct+splash+missed = %d; want shots = %d",
						pa.Player, wa.Weapon, got, wa.Shots)
				}
			case "gl":
				streamDerived = true
				glRows++
				if wa.Direct == nil || wa.Splash == nil {
					t.Errorf("%s gl: direct/splash withheld (%v/%v) — the touch classifier did not run on the always-full parse",
						pa.Player, wa.Direct, wa.Splash)
				}
			}
		}
	}
	if !streamDerived {
		t.Error("no RL/GL fires in corpus demo — stream-derived aim graft not exercised")
	}
	if glRows == 0 {
		t.Error("no GL fires in corpus demo — the touch-classifier gate is not exercised")
	}

	// And the consequence a caller of /player-stats sees: because the gl row
	// carries a classified Direct, its accuracy is published on KTX'S OWN
	// scale (directImpact — projectiles that touched a player) instead of
	// falling back to the any-path count. This is what a withheld gl would
	// silently downgrade, and the analyzer decides it from the aim row's own
	// presence, so the two assertions are one claim read at both ends.
	if res.PlayerStats == nil {
		t.Fatal("PlayerStats absent from the always-full parse")
	}
	glAcc := 0
	for _, row := range res.PlayerStats.Players {
		if row.Accuracy == nil {
			continue
		}
		w, ok := row.Accuracy.ByWeapon["gl"]
		if !ok || w.Attacks == 0 {
			continue
		}
		glAcc++
		if w.HitsConvention != result.HitsDirectImpact {
			t.Errorf("%s gl accuracy: hitsConvention = %q, want %q — the always-full parse classifies grenade touches",
				row.Name, w.HitsConvention, result.HitsDirectImpact)
		}
	}
	if glAcc == 0 {
		t.Error("no GL accuracy rows in corpus demo — the convention gate is not exercised")
	}
}

// oldFormatResultPath is the pre-phase-12 (format-1) tier-2 path: the
// suffix-less `results/v<N>/…` layout. Old lean gobs live here and must never
// be read after the resultCacheFormat bump.
func oldFormatResultPath(root string, schemaVersion int, sha string) string {
	return filepath.Join(root, "results", fmt.Sprintf("v%d", schemaVersion), sha[:2], sha+".gob")
}

// TestLeanGobFormatMigration: a tier-2 gob written at the OLD (format-1) path
// is ignored — GetResult re-parses from the tier-1 bytes and writes the new
// (format-2) path — so a lean pre-phase-12 gob is never served as if it were
// the always-full Result. This is the whole point of the resultCacheFormat
// bump (paths.go): no schema change, but old caches re-parse once.
func TestLeanGobFormatMigration(t *testing.T) {
	hub := newFakeHub()
	defer hub.Close()
	hub.addGame(42, testSHA, testMVD)

	parser := &stubParser{}
	c, root := newTestCache(t, hub.hubClient(), parser)
	ctx := context.Background()
	id := DemoID{Kind: "sha256", SHA: testSHA}

	// Seed tier-1 bytes and a "poisoned" lean gob at the OLD path — a Result a
	// format-2 reader must never surface (its marker proves which path served).
	_ = os.MkdirAll(filepath.Dir(mvdPath(root, testSHA)), 0o755)
	if err := os.WriteFile(mvdPath(root, testSHA), []byte(testMVD), 0o644); err != nil {
		t.Fatal(err)
	}
	poison := &result.Result{SchemaVersion: result.CurrentSchemaVersion, Errors: []string{"OLD-FORMAT-GOB"}}
	data, err := encodeResult(poison)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(oldFormatResultPath(root, result.CurrentSchemaVersion, testSHA), data, 0o644); err != nil {
		t.Fatal(err)
	}

	res, _, err := c.GetResult(ctx, id)
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	// The old-path gob was ignored: the stub parser ran instead.
	if parser.calls.Load() != 1 {
		t.Errorf("parser calls = %d; want 1 (old-format gob must be re-parsed, not served)", parser.calls.Load())
	}
	if len(res.Errors) == 1 && res.Errors[0] == "OLD-FORMAT-GOB" {
		t.Error("served the lean old-format gob; want the re-parsed Result")
	}
	// The re-parse persisted to the NEW (format-2) path.
	mustExist(t, resultPath(root, result.CurrentSchemaVersion, testSHA), "tier-2 at the new format path")
}
