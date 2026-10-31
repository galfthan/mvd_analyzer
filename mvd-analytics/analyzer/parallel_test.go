package analyzer_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// TestParallelMatchesSerial pins Registry.Parallel's promise that the
// output is identical with or without it: the golden corpus runs serial,
// mvd-api runs Parallel (prefetched event pass, per-slot floor traces).
// The dm2 duel has lifts and doors, so the mover scene is exercised too.
// Run with -race to check the pipeline's cross-goroutine hand-offs.
func TestParallelMatchesSerial(t *testing.T) {
	path := filepath.Join("..", "testdata", "cache", "212423.mvd.gz")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("%s not cached (run TestGoldenCorpus once): %v", path, err)
	}
	run := func(raw []byte, parallel bool) []byte {
		reg := analyzer.NewDefaultRegistry()
		reg.BuildShotStreams = true
		reg.BuildNails = true
		reg.Parallel = parallel
		res, err := reg.AnalyzeReader(bytes.NewReader(raw), "212423.mvd.gz")
		if err != nil {
			t.Fatalf("parallel=%v: %v", parallel, err)
		}
		out, err := canonicalJSON(res, "212423")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if serial, par := run(raw, false), run(raw, true); !bytes.Equal(serial, par) {
		t.Fatalf("Parallel output differs from serial (%d vs %d bytes)", len(par), len(serial))
	}
	// A demo cut mid-gzip: the stream error has to reach Result.Errors
	// after the same events, through both pipeline stages.
	cut := raw[:len(raw)/2]
	serial, par := run(cut, false), run(cut, true)
	if !bytes.Equal(serial, par) {
		t.Fatalf("truncated demo: Parallel output differs from serial (%d vs %d bytes)", len(par), len(serial))
	}
	if !bytes.Contains(serial, []byte("unexpected EOF")) {
		t.Fatalf("truncated demo: no stream error in the result")
	}
}

// panickySource stands in for a parser bug: Next panics on its third call.
type panickySource struct{ n int }

func (s *panickySource) Next() (events.Event, error) {
	s.n++
	if s.n == 3 {
		panic("parser bug")
	}
	return &events.UserInfoEvent{Player: &events.PlayerInfo{Name: "a"}}, nil
}

func (s *panickySource) Close() error { return nil }

// panickyReader stands in for a decompressor bug: its first Read serves
// the format sniff (on the caller), the next one panics — on the inflate
// goroutine.
type panickyReader struct{ n int }

func (r *panickyReader) Read(p []byte) (int, error) {
	r.n++
	if r.n > 1 {
		panic("inflate bug")
	}
	return copy(p, []byte{0, 0}), nil
}

// TestParallelPanicReachesCaller pins that a panic on one of Parallel's
// pipeline goroutines (the parser's, the inflater's) re-panics on the
// caller's goroutine, where mvd-api's singleflight recovers it — rather
// than killing the process as an unrecovered goroutine panic would.
func TestParallelPanicReachesCaller(t *testing.T) {
	recovered := func(run func()) (v any) {
		defer func() { v = recover() }()
		run()
		return nil
	}
	reg := analyzer.NewDefaultRegistry()
	reg.Parallel = true
	if v := recovered(func() { reg.AnalyzeSource(&panickySource{}, "x") }); v != "parser bug" {
		t.Fatalf("parser goroutine panic: caller recovered %v", v)
	}
	if v := recovered(func() { reg.AnalyzeReader(&panickyReader{}, "x") }); v != "inflate bug" {
		t.Fatalf("inflate goroutine panic: caller recovered %v", v)
	}
}
