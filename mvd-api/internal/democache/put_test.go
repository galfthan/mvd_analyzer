package democache

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// TestPutDemo_RawStoredGzipped: a raw body is keyed by the hash of the raw
// bytes and stored gzip-compressed under mvdPath (tier-1 is always .mvd.gz).
func TestPutDemo_RawStoredGzipped(t *testing.T) {
	c, root := newTestCache(t, nil, &stubParser{})

	raw := []byte("RAW-MVD-CONTENT-not-gzipped")
	sha, existed, err := c.PutDemo(context.Background(), raw)
	if err != nil {
		t.Fatalf("PutDemo: %v", err)
	}
	if existed {
		t.Errorf("existed = true on first Put")
	}
	if sha != sha256Hex(raw) {
		t.Errorf("sha = %q; want sha of raw bytes %q", sha, sha256Hex(raw))
	}

	mp := mvdPath(root, sha)
	stored, err := os.ReadFile(mp)
	if err != nil {
		t.Fatalf("tier-1 not written: %v", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(stored))
	if err != nil {
		t.Fatalf("tier-1 is not gzip: %v", err)
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(gz); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), raw) {
		t.Errorf("stored content = %q; want the raw body", out.Bytes())
	}
}

// TestPutDemo_GzipStoredVerbatim: a gzip body is keyed by the hash of its
// DECOMPRESSED content and stored byte-for-byte as received.
func TestPutDemo_GzipStoredVerbatim(t *testing.T) {
	c, root := newTestCache(t, nil, &stubParser{})

	content := "UNCOMPRESSED-mvd-content"
	body := gzipOf(t, content)
	sha, _, err := c.PutDemo(context.Background(), body)
	if err != nil {
		t.Fatalf("PutDemo: %v", err)
	}
	if sha != sha256Hex([]byte(content)) {
		t.Errorf("sha = %q; want sha of decompressed content %q", sha, sha256Hex([]byte(content)))
	}
	stored, err := os.ReadFile(mvdPath(root, sha))
	if err != nil {
		t.Fatalf("tier-1 not written: %v", err)
	}
	if !bytes.Equal(stored, body) {
		t.Errorf("gzip body was not stored verbatim")
	}
}

// TestPutDemo_Dedup: a second Put of the same content returns existed=true and
// does not rewrite the file (mtime is bumped, content unchanged).
func TestPutDemo_Dedup(t *testing.T) {
	c, _ := newTestCache(t, nil, &stubParser{})

	body := gzipOf(t, "dedup-me")
	sha1, existed1, err := c.PutDemo(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	if existed1 {
		t.Errorf("first Put: existed = true")
	}
	sha2, existed2, err := c.PutDemo(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	if sha2 != sha1 {
		t.Errorf("sha changed across identical Puts: %q vs %q", sha1, sha2)
	}
	if !existed2 {
		t.Errorf("second Put: existed = false, want true")
	}
}

// TestPutDemo_CorruptGzip: a gzip-magic body that is not a decodable stream is
// ErrInvalidGzip.
func TestPutDemo_CorruptGzip(t *testing.T) {
	c, _ := newTestCache(t, nil, &stubParser{})

	// gzip magic + garbage that never decodes.
	body := append([]byte{0x1f, 0x8b}, bytes.Repeat([]byte{0x00}, 32)...)
	_, _, err := c.PutDemo(context.Background(), body)
	if !errors.Is(err, ErrInvalidGzip) {
		t.Fatalf("err = %v; want ErrInvalidGzip", err)
	}
}

// TestPutDemo_OverCap: a gzip stream whose decompressed size exceeds the cap is
// ErrDemoTooLarge (exercised via the injectable-limit helper).
func TestPutDemo_OverCap(t *testing.T) {
	c, _ := newTestCache(t, nil, &stubParser{})

	body := gzipOf(t, strings.Repeat("A", 4096))
	_, _, err := c.putDemo(body, 1024) // cap below the 4 KiB payload
	if !errors.Is(err, ErrDemoTooLarge) {
		t.Fatalf("err = %v; want ErrDemoTooLarge", err)
	}
}

// TestPutThenGetResult_ServesFromTier1: after a Put, GetResult(sha:…) parses
// the stored tier-1 bytes with no hub involvement.
func TestPutThenGetResult_ServesFromTier1(t *testing.T) {
	parser := &stubParser{}
	c, _ := newTestCache(t, nil, parser)

	body := gzipOf(t, "some-demo-bytes")
	sha, _, err := c.PutDemo(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}

	r, meta, err := c.GetResult(context.Background(), DemoID{Kind: "sha256", SHA: sha})
	if err != nil {
		t.Fatalf("GetResult after Put: %v", err)
	}
	if meta.SHA256 != sha {
		t.Errorf("meta.SHA256 = %q; want %q", meta.SHA256, sha)
	}
	if !meta.FromMVDTier {
		t.Errorf("FromMVDTier = false; expected a tier-1 parse")
	}
	if parser.calls.Load() != 1 {
		t.Errorf("parser.calls = %d; want 1", parser.calls.Load())
	}
	if r == nil {
		t.Fatal("nil result")
	}
}

// TestRemoveDemo removes tier-1 + tier-2 and the mem entry.
func TestRemoveDemo(t *testing.T) {
	parser := &stubParser{}
	c, root := newTestCache(t, nil, parser)

	body := gzipOf(t, "removable")
	sha, _, err := c.PutDemo(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.GetResult(context.Background(), DemoID{Kind: "sha256", SHA: sha}); err != nil {
		t.Fatal(err)
	}
	mustExist(t, mvdPath(root, sha), "tier-1")
	mustExist(t, resultPath(root, result.CurrentSchemaVersion, sha), "tier-2")

	c.RemoveDemo(sha)

	if _, err := os.Stat(mvdPath(root, sha)); !os.IsNotExist(err) {
		t.Errorf("tier-1 still present after RemoveDemo")
	}
	if _, err := os.Stat(resultPath(root, result.CurrentSchemaVersion, sha)); !os.IsNotExist(err) {
		t.Errorf("tier-2 still present after RemoveDemo")
	}
	if c.mem.get(sha) != nil {
		t.Errorf("mem LRU still holds the removed demo")
	}
}

// TestParseTimeout: a parser that blocks until its context is done is aborted
// by ParseTimeout, and the error carries ErrParse.
func TestParseTimeout(t *testing.T) {
	c, _ := newTestCache(t, nil, &stubParser{})
	c.Parse = func(ctx context.Context, _ []byte, _ string) (*result.Result, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	c.ParseTimeout = 30 * time.Millisecond

	body := gzipOf(t, "slow-demo")
	sha, _, err := c.PutDemo(context.Background(), body)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = c.GetResult(context.Background(), DemoID{Kind: "sha256", SHA: sha})
	if !errors.Is(err, ErrParse) {
		t.Fatalf("err = %v; want ErrParse from the parse timeout", err)
	}
}
