package democache

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/hubfetch"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Warm prepares a hub demo for its first visitor — the hub warmer's step
// (mvd-api -warm-interval): the gameId index entry, the demo (tier 1), its
// parsed Result (tier 2) and its position pack (tier 3), so the page's
// first /result and /streams/positions are cache reads. It needs no hub
// lookup: game is the catalog row the warmer listed (id, sha, source URL).
//
// It reports false, doing nothing, when the Result and the pack are on disk
// already. It touches no file it finds (loadResult's visit), so the GC's
// recency stays the visitors', and keeps the Result out of the in-memory
// LRU. The files it writes are the newest in the cache, and age from there.
// The download and parse go through the per-SHA singleflight and the parse
// semaphore like a visitor's.
func (c *Cache) Warm(ctx context.Context, game hubfetch.GameInfo) (bool, error) {
	c.ensureInit()
	if game.ID <= 0 || !isValidSHA(game.DemoSHA256) {
		return false, fmt.Errorf("%w: game %d with sha %q", ErrInvalidDemoID, game.ID, game.DemoSHA256)
	}
	sha := strings.ToLower(game.DemoSHA256)
	rp := resultPath(c.Root, result.CurrentSchemaVersion, sha)
	pp := positionPackPath(c.Root, sha)
	if fileExists(rp) && fileExists(pp) {
		return false, nil
	}
	if ip := gameIndexPath(c.Root, game.ID); !fileExists(ip) {
		_ = writeFileAtomic(ip, []byte(sha+"\n"), 0o644)
	}
	info := game
	info.DemoSHA256 = sha
	c.lastResolved.Store(sha, &info)
	id := DemoID{Kind: "gameId", GameID: game.ID}
	res, _, err := c.getOrCompute(sha, func() (*result.Result, CacheMeta, error) {
		return c.loadResult(context.WithoutCancel(ctx), sha, id, false)
	})
	if err != nil {
		return true, err
	}

	unlock := c.packLocks.Lock(sha)
	defer unlock()
	if _, ok := c.readPositionPack(sha, pp); ok {
		return true, nil
	}
	_, err = c.buildPositionPack(ctx, sha, pp, res)
	return true, err
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
