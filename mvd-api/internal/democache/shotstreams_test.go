package democache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
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

// TestEnsureShotStreams re-parses a real demo to build the opt-in spatial
// streams on demand, latches them, and serves nails as a separate request.
func TestEnsureShotStreams(t *testing.T) {
	sha, demo := corpusDemo(t)
	root := t.TempDir()
	mp := mvdPath(root, sha)
	if err := os.MkdirAll(filepath.Dir(mp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mp, demo, 0o644); err != nil {
		t.Fatal(err)
	}

	c := New(root, nil)
	id := DemoID{Kind: "sha256", SHA: sha}
	ctx := context.Background()

	// Base request: projectiles + beams, not nails.
	res, _, err := c.EnsureShotStreams(ctx, id, false)
	if err != nil {
		t.Fatalf("EnsureShotStreams base: %v", err)
	}
	if res.Streams == nil || res.Streams.Projectiles == nil {
		t.Fatal("base request did not build the projectile stream")
	}
	if !res.Streams.ShotStreamsComputed {
		t.Error("ShotStreamsComputed not latched")
	}
	if res.Streams.Nails != nil || res.Streams.NailsComputed {
		t.Error("nails built/latched on a base request")
	}

	// Nails request: same cached Result, nails now present and latched.
	res2, _, err := c.EnsureShotStreams(ctx, id, true)
	if err != nil {
		t.Fatalf("EnsureShotStreams nails: %v", err)
	}
	if res2 != res {
		t.Error("expected the cached Result pointer to be reused")
	}
	if res2.Streams.Nails == nil || !res2.Streams.NailsComputed {
		t.Error("nails request did not build/latch the nail stream")
	}
}
