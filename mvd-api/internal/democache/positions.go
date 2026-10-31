package democache

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"io"
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
// built once per demo, on first request, and kept as a tier-3 file.
//
// A request whose pack is on disk is a file read: it needs only the SHA
// (the gameId index, no Result), so a revalidation or a demo whose tier-2
// Result was evicted costs no decode or re-parse. meta then reports a
// cache HIT at the current schema version, which is what the file name
// guarantees.
func (c *Cache) PositionPack(ctx context.Context, id DemoID) ([]byte, CacheMeta, error) {
	c.ensureInit()
	sha, err := c.resolveSHA(id)
	if err != nil {
		return nil, CacheMeta{}, err
	}
	path := positionPackPath(c.Root, sha)

	// Per-SHA: concurrent first requests for one demo build it once. The
	// order — pack lock, then (inside GetResult and below) the parse
	// semaphore — is EnsureLOS's, and nothing takes a pack lock while
	// holding the semaphore.
	unlock := c.packLocks.Lock(sha)
	defer unlock()
	if data, ok := c.readPositionPack(sha, path); ok {
		touch(path)
		return data, CacheMeta{SHA256: sha, FromCache: true, SchemaVersion: result.CurrentSchemaVersion}, nil
	}

	res, meta, err := c.GetResult(ctx, id)
	if err != nil {
		return nil, meta, err
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
		c.packVerified.Store(sha, true)
		c.maybeGC()
	}
	return data, meta, nil
}

// readPositionPack reads a demo's tier-3 pack. The first read of each
// file in this process inflates it and checks the gzip checksum and the
// pack header: a file that fails (disk corruption, a hand edit — writes
// are atomic) is removed and reported as a miss, so it is rebuilt rather
// than served forever. Caller holds the demo's pack lock.
func (c *Cache) readPositionPack(sha, path string) ([]byte, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	if _, ok := c.packVerified.Load(sha); ok {
		return data, true
	}
	if err := checkPositionPack(data); err != nil {
		slog.Warn("tier-3 position pack discarded", "sha", sha, "err", err)
		_ = os.Remove(path)
		return nil, false
	}
	c.packVerified.Store(sha, true)
	return data, true
}

// checkPositionPack inflates a gzipped pack to the end (which verifies the
// gzip CRC and length) and checks its magic and layout version.
func checkPositionPack(gz []byte) error {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return err
	}
	head := make([]byte, 8)
	if _, err := io.ReadFull(zr, head); err != nil {
		return err
	}
	if string(head[:4]) != "MVDP" || binary.LittleEndian.Uint32(head[4:]) != result.PositionPackVersion {
		return fmt.Errorf("not a v%d position pack", result.PositionPackVersion)
	}
	_, err = io.Copy(io.Discard, zr)
	return err
}
