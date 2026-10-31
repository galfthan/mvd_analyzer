package democache

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// positionPackName is the tier-3 file name of a demo's gzipped position
// pack (result.EncodePositionPack). It carries the pack layout version as
// well as the effective (schema) version, so a bump of either moves the
// name and the stale-artifact sweep reaps the old file.
func positionPackName() string {
	return fmt.Sprintf("positions-p%d@v%d.bin.gz", result.PositionPackVersion, result.CurrentSchemaVersion)
}

// positionPackPath is where a demo's pack lives, beside its other tier-3
// artifacts:
//
//	<root>/artifacts/<sha[:2]>/<sha>/positions-p<PV>@v<EV>.bin.gz
func positionPackPath(root, sha string) string {
	return filepath.Join(artifactsRoot(root), sha[:2], sha, positionPackName())
}

// PositionPack returns a demo's position pack, GZIPPED — the bytes
// GET /v1/demos/{id}/streams/positions sends as they are, with
// Content-Encoding: gzip. The pack is ~27 MB raw on a 4on4 and ~9 MB
// gzipped, and the compression costs a few hundred ms of CPU, so it is
// built once per demo, on first request, and kept as a tier-3 file: every
// later request is a file read. It is derived from the always-full parse,
// so nothing else needs to be computed first.
func (c *Cache) PositionPack(ctx context.Context, id DemoID) ([]byte, CacheMeta, error) {
	res, meta, err := c.GetResult(ctx, id)
	if err != nil {
		return nil, meta, err
	}
	sha := meta.SHA256
	path := positionPackPath(c.Root, sha)

	// Per-SHA: concurrent first requests for one demo build it once.
	unlock := c.packLocks.Lock(sha)
	defer unlock()
	if data, err := os.ReadFile(path); err == nil {
		touch(path)
		return data, meta, nil
	}

	// Distinct demos' first requests share the parse semaphore with every
	// other heavy cold operation (F15).
	release, err := c.acquireParse(ctx)
	if err != nil {
		return nil, meta, err
	}
	defer release()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(result.EncodePositionPack(res.Streams)); err != nil {
		return nil, meta, fmt.Errorf("compress position pack: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, meta, fmt.Errorf("compress position pack: %w", err)
	}
	data := buf.Bytes()
	// A failed write only costs the next request a rebuild.
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		slog.Warn("position pack write failed", "sha", sha, "err", err)
	} else {
		c.maybeGC()
	}
	return data, meta, nil
}
