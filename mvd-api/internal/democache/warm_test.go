package democache

import (
	"context"
	"os"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/hubfetch"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// TestWarm: a listed game is downloaded, parsed and packed without a hub
// lookup, indexed by its gameId, and kept out of the in-memory LRU; a
// second Warm finds it ready and does nothing; a visitor then reads it
// without the hub.
func TestWarm(t *testing.T) {
	hub := newFakeHub()
	defer hub.Close()
	hub.addGame(42, testSHA, testMVD)
	c, root := newTestCache(t, hub.hubClient(), &stubParser{})
	c.Parse = trackedStreams
	ctx := context.Background()
	game := hubfetch.GameInfo{ID: 42, DemoSHA256: testSHA}

	did, err := c.Warm(ctx, game)
	if err != nil || !did {
		t.Fatalf("Warm: %v, %v", did, err)
	}
	if n := hub.resolveCalls.Load(); n != 0 {
		t.Errorf("Warm looked the game up %d times; the listed row carries its sha", n)
	}
	for _, p := range []string{mvdPath(root, testSHA), resultPath(root, result.CurrentSchemaVersion, testSHA), positionPackPath(root, testSHA), gameIndexPath(root, 42)} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("not prepared: %v", err)
		}
	}
	if c.mem.get(testSHA) != nil {
		t.Error("Warm put the Result in the in-memory LRU")
	}

	cdn := hub.cdnCalls.Load()
	if did, err := c.Warm(ctx, game); err != nil || did {
		t.Fatalf("second Warm: %v, %v; want nothing done", did, err)
	}
	if _, _, err := c.GetResult(ctx, DemoID{Kind: "gameId", GameID: 42}); err != nil {
		t.Fatalf("GetResult after Warm: %v", err)
	}
	if hub.cdnCalls.Load() != cdn || hub.resolveCalls.Load() != 0 {
		t.Error("a visitor after Warm reached the hub")
	}

	if _, err := c.Warm(ctx, hubfetch.GameInfo{ID: 7, DemoSHA256: "nope"}); err == nil {
		t.Error("an invalid sha must fail")
	}
}
