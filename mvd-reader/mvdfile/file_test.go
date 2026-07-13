package mvdfile

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"testing"
)

// gzipOf returns the gzip-compressed form of payload.
func gzipOf(t *testing.T, payload []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(payload); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// TestDecompressBombRejected verifies a gzip stream that expands past the
// injected cap returns the ErrDecompressedTooLarge sentinel rather than data or
// io.EOF. Highly compressible input keeps the compressed fixture tiny.
func TestDecompressBombRejected(t *testing.T) {
	const limit = 1 << 10 // 1 KiB
	payload := bytes.Repeat([]byte{'A'}, limit*8)
	rc, err := newReaderLimit(bytes.NewReader(gzipOf(t, payload)), limit)
	if err != nil {
		t.Fatalf("newReaderLimit: %v", err)
	}
	defer rc.Close()

	_, err = io.ReadAll(rc)
	if !errors.Is(err, ErrDecompressedTooLarge) {
		t.Fatalf("expected ErrDecompressedTooLarge, got %v", err)
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("bomb must not be reported as clean EOF")
	}
}

// TestDecompressUnderLimit verifies a stream whose decompressed size is at or
// below the cap decodes cleanly to exactly its bytes.
func TestDecompressUnderLimit(t *testing.T) {
	const limit = 1 << 10
	payload := bytes.Repeat([]byte{'B'}, limit) // exactly at the cap
	rc, err := newReaderLimit(bytes.NewReader(gzipOf(t, payload)), limit)
	if err != nil {
		t.Fatalf("newReaderLimit: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read under limit failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("decompressed content mismatch: got %d bytes, want %d", len(got), len(payload))
	}
}

// TestRawStreamUnchanged verifies non-gzip input passes through untouched.
func TestRawStreamUnchanged(t *testing.T) {
	raw := []byte("this is a raw uncompressed mvd stream")
	rc, err := newReaderLimit(bytes.NewReader(raw), 8)
	if err != nil {
		t.Fatalf("newReaderLimit: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read raw failed: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("raw passthrough altered content")
	}
}

// TestTruncatedGzipUnchanged verifies a corrupt/truncated gzip body still yields
// its underlying decompression error (ErrUnexpectedEOF), not the size sentinel —
// the cap must not mask honest truncation.
func TestTruncatedGzipUnchanged(t *testing.T) {
	full := gzipOf(t, bytes.Repeat([]byte{'C'}, 4096))
	truncated := full[:len(full)-8] // drop trailing bytes to corrupt the stream
	rc, err := newReaderLimit(bytes.NewReader(truncated), maxDecompressedSize)
	if err != nil {
		t.Fatalf("newReaderLimit: %v", err)
	}
	defer rc.Close()

	_, err = io.ReadAll(rc)
	if err == nil {
		t.Fatalf("expected error on truncated gzip, got nil")
	}
	if errors.Is(err, ErrDecompressedTooLarge) {
		t.Fatalf("truncation must not be reported as a decompression bomb: %v", err)
	}
}
