package analyzer

// The full-analysis cost, measured the way mvd-api pays it on a cold
// request (democache defaultParse: shot streams + nails + Parallel), over
// the golden cache's demos. Opt-in — MVD_PERF=1 — so `make test` does not
// pay for it:
//
//	MVD_PERF=1 go test ./mvd-analytics/analyzer -run TestPipelinePerf -v
//	MVD_PERF=1 MVD_PERF_SERIAL=1 …   # registry.Parallel off
//
// It prints, per demo, the wall time, the allocation volume, the slowest
// phases (Registry.PhaseTimings) and the tier-2 cache encode the API does
// next (result.EncodeCache), plus the decode a warm tier-2 hit pays. For CPU / memory profiles use the benchmark:
//
//	go test ./mvd-analytics/analyzer -run '^$' -bench BenchmarkPipeline -benchtime 3x \
//	    -cpuprofile cpu.out -memprofile mem.out

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func perfDemos(tb testing.TB) []string {
	tb.Helper()
	files, _ := filepath.Glob(filepath.Join("..", "testdata", "cache", "*.mvd.gz"))
	if len(files) == 0 {
		tb.Skip("no demos in testdata/cache (run the golden test once)")
	}
	sort.Strings(files)
	if one := os.Getenv("MVD_PERF_DEMO"); one != "" {
		return []string{filepath.Join("..", "testdata", "cache", one)}
	}
	return files
}

func apiRegistry() *Registry {
	r := NewDefaultRegistry()
	r.BuildShotStreams = true
	r.BuildNails = true
	r.Parallel = os.Getenv("MVD_PERF_SERIAL") == ""
	return r
}

func TestPipelinePerf(t *testing.T) {
	if os.Getenv("MVD_PERF") == "" {
		t.Skip("set MVD_PERF=1")
	}
	var total time.Duration
	for _, f := range perfDemos(t) {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		reg := apiRegistry()
		var ms0, ms1 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&ms0)
		start := time.Now()
		res, err := reg.AnalyzeReader(bytes.NewReader(raw), filepath.Base(f))
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		el := time.Since(start)
		runtime.ReadMemStats(&ms1)
		total += el

		gstart := time.Now()
		enc, err := result.EncodeCache(res)
		if err != nil {
			t.Fatalf("encode cache: %v", err)
		}
		gel := time.Since(gstart)
		dstart := time.Now()
		if _, err := result.DecodeCache(enc); err != nil {
			t.Fatalf("decode cache: %v", err)
		}
		del := time.Since(dstart)

		phases := append([]PhaseTiming(nil), reg.PhaseTimings...)
		sort.Slice(phases, func(i, j int) bool { return phases[i].Ms > phases[j].Ms })
		line := fmt.Sprintf("%-16s %6.0f ms  alloc %5.0f MB  tier2 enc %4.0f / dec %4.0f ms %5.1f MB |", filepath.Base(f),
			float64(el.Microseconds())/1000, float64(ms1.TotalAlloc-ms0.TotalAlloc)/(1<<20),
			float64(gel.Microseconds())/1000, float64(del.Microseconds())/1000, float64(len(enc))/(1<<20))
		for i, p := range phases {
			if i == 8 {
				break
			}
			line += fmt.Sprintf(" %s %.0f", p.Name, p.Ms)
		}
		t.Log(line)
	}
	t.Logf("total %.0f ms", float64(total.Microseconds())/1000)
}

func BenchmarkPipeline(b *testing.B) {
	files := perfDemos(b)
	raws := make([][]byte, len(files))
	for i, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			b.Fatal(err)
		}
		raws[i] = raw
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for k, raw := range raws {
			if _, err := apiRegistry().AnalyzeReader(bytes.NewReader(raw), files[k]); err != nil {
				b.Fatal(err)
			}
		}
	}
}
