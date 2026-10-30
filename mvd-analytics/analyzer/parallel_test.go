package analyzer_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
)

// TestParallelMatchesSerial pins Registry.Parallel's promise that the
// output is identical with or without it: the golden corpus runs serial,
// mvd-api runs Parallel (prefetched event pass, per-slot floor traces).
// The dm2 duel has lifts and doors, so the mover scene is exercised too.
// Run with -race to check the event pass's cross-goroutine hand-off.
func TestParallelMatchesSerial(t *testing.T) {
	path := filepath.Join("..", "testdata", "cache", "212423.mvd.gz")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("%s not cached (run TestGoldenCorpus once): %v", path, err)
	}
	run := func(parallel bool) []byte {
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
	if serial, par := run(false), run(true); !bytes.Equal(serial, par) {
		t.Fatalf("Parallel output differs from serial (%d vs %d bytes)", len(par), len(serial))
	}
}
