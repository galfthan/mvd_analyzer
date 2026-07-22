package democache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// streamsWithPlayers is a stub parse producing a Result with a Streams block
// carrying two named players but no DemoInfo/map — used by the warm-splice and
// concurrency tests, which drive the tier-3 splice or an injected BuildLOS and
// never reach the real compute (which, post-Phase-3, would return ErrNoBSP for
// a 2-player demo with no BSP).
func streamsWithPlayers(_ context.Context, _ []byte, filename string) (*result.Result, error) {
	return &result.Result{
		SchemaVersion: result.CurrentSchemaVersion,
		FilePath:      filename,
		Streams: &result.Streams{
			Players: []result.PlayerStream{{Name: "A"}, {Name: "B"}},
		},
	}, nil
}

// onePlayerStreams is the legitimately-empty case: a single-player demo. Post
// Phase 3 this is the only compute-to-empty path that latches and persists (a
// <2-player demo), so the cold-compute and corrupt-fallback tier-3 tests use it
// to exercise the persist/reload machinery without a provisioned BSP.
func onePlayerStreams(_ context.Context, _ []byte, filename string) (*result.Result, error) {
	return &result.Result{
		SchemaVersion: result.CurrentSchemaVersion,
		FilePath:      filename,
		Streams: &result.Streams{
			Players: []result.PlayerStream{{Name: "A"}},
		},
	}, nil
}

// --- los tier-3 ---

// TestEnsureLOS_Tier3_ColdComputeWritesArtifact: the first EnsureLOS computes
// the pass (empty here — a legitimately empty <2-player demo) and persists it to
// tier 3, latching the base Result.
func TestEnsureLOS_Tier3_ColdComputeWritesArtifact(t *testing.T) {
	hub := newFakeHub()
	defer hub.Close()
	hub.addGame(42, testSHA, testMVD)

	c, root := newTestCache(t, hub.hubClient(), &stubParser{})
	c.Parse = onePlayerStreams
	ctx := context.Background()
	id := DemoID{Kind: "gameId", GameID: 42}

	res, _, err := c.EnsureLOS(ctx, id)
	if err != nil {
		t.Fatalf("EnsureLOS: %v", err)
	}
	if !res.Streams.LOSComputed {
		t.Error("LOSComputed not latched after compute")
	}
	mustExist(t, artifactPath(root, "los", testSHA), "tier-3 los artifact")
}

// TestEnsureLOS_Tier3_WarmLoadSplices: a fresh process (new Cache instance,
// empty memory LRU) with a tier-3 los artifact on disk splices it onto the
// base Result WITHOUT recomputing — proven by a sentinel interval a fresh
// (BSP-less) compute could never produce.
func TestEnsureLOS_Tier3_WarmLoadSplices(t *testing.T) {
	hub := newFakeHub()
	defer hub.Close()
	hub.addGame(42, testSHA, testMVD)

	c, root := newTestCache(t, hub.hubClient(), &stubParser{})
	c.Parse = streamsWithPlayers
	ctx := context.Background()
	id := DemoID{Kind: "gameId", GameID: 42}

	// Warm the base Result (tier-1 + tier-2), then craft a distinctive tier-3
	// artifact by hand via the analyzer codec.
	base, _, err := c.GetResult(ctx, id)
	if err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	sentinel := []result.LosTrack{{Other: 1, Iv: []result.Interval{{Start: 111, End: 222}}}}
	base.Streams.Players[0].LOS = sentinel
	base.Streams.LOSComputed = true
	art, _ := analyzer.LazyArtifactByName("los")
	data, ok, err := art.EncodeTier3(base)
	if err != nil || !ok {
		t.Fatalf("EncodeTier3: ok=%v err=%v", ok, err)
	}
	if err := writeFileAtomic(artifactPath(root, "los", testSHA), data, 0o644); err != nil {
		t.Fatalf("write tier-3: %v", err)
	}

	// Fresh Cache on the same root: no in-memory latch, no LRU. The base
	// Result comes from tier 2 (LOS empty, LOSComputed=false); EnsureLOS must
	// serve the sentinel from tier 3.
	c2 := New(root, hub.hubClient())
	c2.Parse = streamsWithPlayers
	res, _, err := c2.EnsureLOS(ctx, id)
	if err != nil {
		t.Fatalf("EnsureLOS warm: %v", err)
	}
	if !res.Streams.LOSComputed {
		t.Error("LOSComputed not latched after warm load")
	}
	if len(res.Streams.Players[0].LOS) != 1 || res.Streams.Players[0].LOS[0].Iv[0].Start != 111 {
		t.Errorf("warm load did not splice the tier-3 sentinel: %+v", res.Streams.Players[0].LOS)
	}
}

// TestEnsureLOS_Tier3_CorruptFallsBack: a garbage tier-3 gob is discarded (not
// spliced) and EnsureLOS recomputes, still succeeding.
func TestEnsureLOS_Tier3_CorruptFallsBack(t *testing.T) {
	hub := newFakeHub()
	defer hub.Close()
	hub.addGame(42, testSHA, testMVD)

	c, root := newTestCache(t, hub.hubClient(), &stubParser{})
	c.Parse = onePlayerStreams
	ctx := context.Background()
	id := DemoID{Kind: "gameId", GameID: 42}

	if _, _, err := c.GetResult(ctx, id); err != nil {
		t.Fatalf("GetResult: %v", err)
	}
	if err := writeFileAtomic(artifactPath(root, "los", testSHA), []byte("not a gob"), 0o644); err != nil {
		t.Fatalf("write corrupt tier-3: %v", err)
	}

	c2 := New(root, hub.hubClient())
	c2.Parse = onePlayerStreams
	res, _, err := c2.EnsureLOS(ctx, id)
	if err != nil {
		t.Fatalf("EnsureLOS with corrupt tier-3: %v", err)
	}
	if !res.Streams.LOSComputed {
		t.Error("expected recompute (latched) after discarding corrupt tier-3")
	}
	// The recompute (empty LOS) overwrites the corrupt file with a valid gob.
	if data, err := os.ReadFile(artifactPath(root, "los", testSHA)); err != nil {
		t.Errorf("tier-3 not rewritten: %v", err)
	} else if err := art0LOS().DecodeTier3(&result.Result{Streams: &result.Streams{Players: []result.PlayerStream{{Name: "A"}}}}, data); err != nil {
		t.Errorf("rewritten tier-3 is not a valid los gob: %v", err)
	}
}

// TestEnsureLOS_NoBSP_NotPersistedAndRetries: when BuildLOS returns ErrNoBSP
// (map with no usable visibility BSP), EnsureLOS must propagate the error, write
// NO tier-3 artifact, and leave Streams.LOSComputed false — so a second call
// retries (calls BuildLOS again) rather than serving a poisoned empty. This is
// the Phase-3 fix: the ErrNoBSP outcome is never latched or cached.
func TestEnsureLOS_NoBSP_NotPersistedAndRetries(t *testing.T) {
	hub := newFakeHub()
	defer hub.Close()
	hub.addGame(42, testSHA, testMVD)

	c, root := newTestCache(t, hub.hubClient(), &stubParser{})
	c.Parse = streamsWithPlayers // 2 players — reaches the BSP-gated compute
	var builds atomic.Int32
	c.BuildLOS = func(_ *analyzer.LazyArtifact, _ *result.Result) error {
		builds.Add(1)
		return fmt.Errorf("compute los: %w", analyzer.ErrNoBSP)
	}
	ctx := context.Background()
	id := DemoID{Kind: "gameId", GameID: 42}

	res, _, err := c.EnsureLOS(ctx, id)
	if !errors.Is(err, analyzer.ErrNoBSP) {
		t.Fatalf("first EnsureLOS = %v; want ErrNoBSP", err)
	}
	if res != nil {
		t.Errorf("EnsureLOS should return a nil Result on ErrNoBSP, got %v", res)
	}
	if _, statErr := os.Stat(artifactPath(root, "los", testSHA)); !os.IsNotExist(statErr) {
		t.Errorf("ErrNoBSP must not write a tier-3 los artifact (stat err = %v)", statErr)
	}

	// A second call must retry — proven by BuildLOS running twice. The base
	// Result's LOSComputed must never have latched.
	if _, _, err := c.EnsureLOS(ctx, id); !errors.Is(err, analyzer.ErrNoBSP) {
		t.Fatalf("second EnsureLOS = %v; want ErrNoBSP (retry)", err)
	}
	if got := builds.Load(); got != 2 {
		t.Errorf("BuildLOS ran %d times; want 2 (ErrNoBSP must not latch, so every call retries)", got)
	}
}

func art0LOS() *analyzer.LazyArtifact { a, _ := analyzer.LazyArtifactByName("los"); return a }
