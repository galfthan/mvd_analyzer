// Package mvd provides an events.Source backed by a recorded MVD demo
// file or an in-memory MVD byte stream. It is the reference source
// implementation; analytics code should import this package only at
// wiring points (main functions, WASM entry) and otherwise work against
// the events.Source interface so alternative sources (e.g. live QTV)
// are drop-in replaceable.
package mvd

import (
	"io"
	"os"

	"github.com/mvd-analyzer/qwdemo/events"
	"github.com/mvd-analyzer/qwdemo/mvd"
	"github.com/mvd-analyzer/qwdemo/mvdfile"
	"github.com/mvd-analyzer/qwdemo/parser"
)

// Source is an events.Source implementation that pulls events from an
// MVD file or byte stream. Satisfies events.Source.
type Source struct {
	closer  io.Closer
	decoder *mvd.Decoder
	parser  *parser.Parser
	queue   []events.Event
	done    bool
}

// Open opens an MVD file by path. Handles gzip-compressed `.mvd.gz`
// automatically. The returned Source must be Closed by the caller.
func Open(path string) (*Source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	rc, err := mvdfile.NewReader(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	// Chain both closers: the gzip ReadCloser owns decompression state,
	// the underlying os.File owns the FD. Close both to release both.
	return newSource(rc, chainCloser{rc: rc, file: f}), nil
}

// NewFromReader wraps an arbitrary io.Reader carrying an MVD byte stream
// (plain or gzipped) into a Source. The caller owns the underlying reader;
// Close on this Source only releases internal decompression state.
func NewFromReader(r io.Reader) (*Source, error) {
	rc, err := mvdfile.NewReader(r)
	if err != nil {
		return nil, err
	}
	return newSource(rc, rc), nil
}

func newSource(r io.Reader, closer io.Closer) *Source {
	dec := mvd.NewDecoder(r)
	p := parser.NewParser(dec)
	src := &Source{closer: closer, decoder: dec, parser: p}
	p.OnEvent(func(e parser.Event) error {
		src.queue = append(src.queue, e)
		return nil
	})
	return src
}

// Next pulls the next event from the stream. Returns io.EOF when the
// underlying decoder reports end-of-demo.
func (s *Source) Next() (events.Event, error) {
	for len(s.queue) == 0 && !s.done {
		if err := s.parser.ParseOne(); err != nil {
			if err == io.EOF {
				s.done = true
				break
			}
			return nil, err
		}
	}
	if len(s.queue) == 0 {
		return nil, io.EOF
	}
	e := s.queue[0]
	s.queue[0] = nil
	s.queue = s.queue[1:]
	return e, nil
}

// Close releases any resources held by the source (file handles, gzip
// state). Safe to call multiple times.
func (s *Source) Close() error {
	if s.closer != nil {
		err := s.closer.Close()
		s.closer = nil
		return err
	}
	return nil
}

// CurrentTime reports the decoder's current demo time. Useful when
// finalizing an analysis that needs the total stream duration.
func (s *Source) CurrentTime() float64 {
	return s.decoder.CurrentTime()
}

// Parser returns the underlying parser. Exposed for diagnostic tooling
// that needs to flip the parser into diagnostic mode or read collected
// warnings; not part of the stable Source contract.
func (s *Source) Parser() *parser.Parser {
	return s.parser
}

// chainCloser closes the decompressor wrapper and the underlying file in
// order, returning the first non-nil error so callers can spot trouble.
type chainCloser struct {
	rc   io.Closer
	file io.Closer
}

func (c chainCloser) Close() error {
	errRC := c.rc.Close()
	errF := c.file.Close()
	if errRC != nil {
		return errRC
	}
	return errF
}
