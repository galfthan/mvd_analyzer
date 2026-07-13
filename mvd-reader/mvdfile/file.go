// Package mvdfile provides file handling for MVD demo files with compression support.
package mvdfile

import (
	"bufio"
	"compress/gzip"
	"errors"
	"io"
	"os"
)

// ErrDecompressedTooLarge is returned when a gzip stream expands past
// maxDecompressedSize. It is a distinct sentinel (not io.EOF) so callers can
// tell a decompression bomb from a cleanly terminated or truncated stream.
var ErrDecompressedTooLarge = errors.New("mvdfile: decompressed size exceeds limit")

// maxDecompressedSize caps the number of bytes a gzip stream may expand to
// before decompression aborts. A hostile ~KiB gzip can inflate to gigabytes;
// this bounds that. 512 MiB matches democache.maxDemoUncompressed, the
// ceiling the API's content-integrity path already enforces on stored demos.
const maxDecompressedSize = 512 << 20

// limitedGzipReader wraps a gzip.Reader and fails once cumulative decompressed
// output exceeds limit, returning ErrDecompressedTooLarge instead of more data.
type limitedGzipReader struct {
	gz        *gzip.Reader
	remaining int64
}

func (l *limitedGzipReader) Read(p []byte) (int, error) {
	if l.remaining < 0 {
		return 0, ErrDecompressedTooLarge
	}
	// Allow one extra byte so exactly-at-limit output is not misread as a bomb.
	if int64(len(p)) > l.remaining+1 {
		p = p[:l.remaining+1]
	}
	n, err := l.gz.Read(p)
	l.remaining -= int64(n)
	if l.remaining < 0 {
		return n, ErrDecompressedTooLarge
	}
	return n, err
}

func (l *limitedGzipReader) Close() error {
	return l.gz.Close()
}

// File represents an opened MVD file that may be compressed
type File struct {
	file       *os.File
	gzipReader *gzip.Reader
	reader     io.Reader
}

// Open opens an MVD file, automatically detecting and handling gzip compression.
// Files that begin with the gzip magic bytes (0x1f 0x8b) are decompressed;
// the filename suffix is not consulted.
func Open(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	bufReader, isGzip := peekGzip(f)
	if isGzip {
		gzReader, err := gzip.NewReader(bufReader)
		if err != nil {
			f.Close()
			return nil, err
		}
		return &File{
			file:       f,
			gzipReader: gzReader,
			reader:     &limitedGzipReader{gz: gzReader, remaining: maxDecompressedSize},
		}, nil
	}

	// Raw MVD file
	return &File{
		file:   f,
		reader: bufReader,
	}, nil
}

// peekGzip wraps r in a buffered reader and reports whether the stream
// begins with the gzip magic bytes (0x1f 0x8b). A stream too short to
// peek two bytes is reported as not gzipped. The returned reader has not
// consumed the magic bytes, so it is safe to pass to gzip.NewReader.
func peekGzip(r io.Reader) (*bufio.Reader, bool) {
	br := bufio.NewReader(r)
	magic, err := br.Peek(2)
	if err != nil {
		return br, false
	}
	return br, magic[0] == 0x1f && magic[1] == 0x8b
}

// Read implements io.Reader
func (f *File) Read(p []byte) (n int, err error) {
	return f.reader.Read(p)
}

// Close closes the file and any decompression readers
func (f *File) Close() error {
	if f.gzipReader != nil {
		f.gzipReader.Close()
	}
	return f.file.Close()
}

// Name returns the original file path
func (f *File) Name() string {
	return f.file.Name()
}

// IsCompressed returns true if the file is gzip compressed
func (f *File) IsCompressed() bool {
	return f.gzipReader != nil
}

// NewReader wraps an io.Reader with automatic gzip detection.
// If the stream starts with gzip magic bytes (0x1f 0x8b), it returns a gzip
// reader bounded by maxDecompressedSize (a gzip stream expanding past the cap
// yields ErrDecompressedTooLarge, not io.EOF). Otherwise, it returns the
// original stream. The caller must close the returned ReadCloser.
func NewReader(r io.Reader) (io.ReadCloser, error) {
	return newReaderLimit(r, maxDecompressedSize)
}

// newReaderLimit is NewReader with an injectable decompressed-size cap so tests
// can exercise the bomb guard without generating hundreds of MiB.
func newReaderLimit(r io.Reader, limit int64) (io.ReadCloser, error) {
	bufReader, isGzip := peekGzip(r)
	if isGzip {
		gzReader, err := gzip.NewReader(bufReader)
		if err != nil {
			return nil, err
		}
		return &limitedGzipReader{gz: gzReader, remaining: limit}, nil
	}
	return io.NopCloser(bufReader), nil
}
