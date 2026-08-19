// qw-corpus-survey sweeps a directory of demos through the full analysis
// pipeline (damage reconstruction included) and reports how the corpus
// breaks down: wire-instrumented vs reconstructed vs skipped/failed, the
// unattributed-damage share on reconstructed demos, and which per-hit
// telemetry (TE_BLOOD / TE_EXPLOSION / TE_LIGHTNINGBLOOD) each demo
// carries. Per-demo rows go to a CSV (keyed by filename, so a sha256-named
// corpus joins against its SQL index); the aggregate summary goes to
// stdout.
//
// Usage:
//
//	go run ./mvd-analytics/cmd/qw-corpus-survey -dir /data/mvd -sample 500 -csv survey.csv
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/damagerecon"
	"github.com/mvd-analyzer/mvd-reader/events"
	mvdsource "github.com/mvd-analyzer/mvd-reader/source/mvd"
)

// doReadability adds the raw-wire readability census (second parse per
// demo): date-marker spellings, //finalscores, //ktx drop. Set from
// -readability. Parse warnings are NOT part of it — they ride the
// Result now and are censused on every run.
var doReadability bool

type row struct {
	file    string
	mapname string
	mode    string
	players int
	frags   int
	matchMs int32
	source  string // "ktx" | "reconstructed" | "skipped:<mode>" | "none:<err>" | "error"
	events  int
	total   int     // total raw damage
	unkPct  float64 // share of damage with weapon "unknown" (reconstructed only)
	bloods  int
	expls   int
	lgbl    int
	err     string

	// Readability census (-readability): what the wire carries vs what the
	// pipeline currently reads, per plan-archive-features.md.
	version     string // serverinfo *version (era marker)
	ktxver      string // serverinfo ktxver (the sharp feature gate)
	demoinfo    bool   // KTX demoinfo block present (authoritative scoreboard readable)
	wallclock   bool   // DemoStartUnixMs currently readable
	matchdate   string // "" | "iso" (format A) | "ctime" (format B) — raw print present
	dateBlock   bool   // "Date....:" stats-block print present (format C)
	finalscores bool   // //finalscores stufftext present (lead 3)
	ktxdrop     bool   // //ktx drop hint present (backpack GT, lead 2)
	// Parse-warning census, taken off the Result (schema v72
	// parseWarnings) — always populated, no -readability needed.
	warns      int    // parse warnings, total
	warnKinds  string // distinct warning types, sorted, ';'-joined
	unknownSvc int    // unknown_svc warnings specifically (protocol gaps)
}

func main() {
	dir := flag.String("dir", "", "directory of .mvd/.mvd.gz demos (required)")
	sample := flag.Int("sample", 0, "random sample size (0 = all demos)")
	seed := flag.Int64("seed", 1, "sample shuffle seed")
	workers := flag.Int("workers", max(1, runtime.NumCPU()-1), "parallel analyzers")
	csvPath := flag.String("csv", "", "per-demo CSV output path (optional)")
	readability := flag.Bool("readability", false, "add the raw-wire readability census (second parse per demo)")
	flag.Parse()
	doReadability = *readability
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "qw-corpus-survey: -dir is required")
		os.Exit(2)
	}

	entries, err := os.ReadDir(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qw-corpus-survey:", err)
		os.Exit(1)
	}
	var paths []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".mvd") || strings.HasSuffix(n, ".mvd.gz") {
			paths = append(paths, filepath.Join(*dir, n))
		}
	}
	sort.Strings(paths)
	if *sample > 0 && *sample < len(paths) {
		rand.New(rand.NewSource(*seed)).Shuffle(len(paths), func(i, j int) {
			paths[i], paths[j] = paths[j], paths[i]
		})
		paths = paths[:*sample]
	}
	fmt.Printf("surveying %d demos with %d workers\n", len(paths), *workers)

	jobs := make(chan string)
	results := make(chan row)
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				results <- surveyOne(p)
			}
		}()
	}
	go func() {
		for _, p := range paths {
			jobs <- p
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	var rows []row
	done := 0
	for r := range results {
		rows = append(rows, r)
		done++
		if done%100 == 0 {
			fmt.Fprintf(os.Stderr, "  %d/%d\n", done, len(paths))
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].file < rows[j].file })

	if *csvPath != "" {
		if err := writeCSV(*csvPath, rows); err != nil {
			fmt.Fprintln(os.Stderr, "qw-corpus-survey: csv:", err)
			os.Exit(1)
		}
	}
	summarize(rows)
}

func surveyOne(path string) row {
	r := row{file: filepath.Base(path)}
	defer func() {
		if p := recover(); p != nil {
			r.source, r.err = "error", fmt.Sprintf("panic: %v", p)
		}
	}()
	reg := analyzer.NewDefaultRegistry()
	reg.BuildShotStreams = true
	res, err := reg.Analyze(path)
	if err != nil {
		r.source, r.err = "error", err.Error()
		return r
	}
	if res.Metadata != nil && res.Metadata.ServerInfo != nil {
		r.mapname = res.Metadata.ServerInfo["map"]
		r.mode = res.Metadata.ServerInfo["mode"]
		r.version = res.Metadata.ServerInfo["*version"]
		r.ktxver = res.Metadata.ServerInfo["ktxver"]
	}
	r.demoinfo = res.DemoInfo != nil
	if res.Streams != nil {
		r.wallclock = res.Streams.Global.DemoStartUnixMs != 0
	}
	parseWarningCensus(res, &r)
	if doReadability {
		rawScan(path, &r)
	}
	if res.Streams != nil {
		r.players = len(res.Streams.Players)
		r.matchMs = res.Streams.Global.MatchEnd
		if pe := res.Streams.PointEffects; pe != nil {
			for _, ty := range pe.Type {
				switch int(ty) {
				case events.TeBlood:
					r.bloods++
				case events.TeExplosion:
					r.expls++
				case events.TeLightningBlood:
					r.lgbl++
				}
			}
		}
	}
	if res.Frags != nil {
		r.frags = len(res.Frags.Frags)
	}
	switch {
	case res.Damage != nil:
		r.source = res.Damage.Source
		r.events = len(res.Damage.Events)
		unk := 0
		for i := range res.Damage.Events {
			e := &res.Damage.Events[i]
			r.total += e.Damage
			if e.Weapon == "unknown" {
				unk += e.Damage
			}
		}
		if r.total > 0 {
			r.unkPct = 100 * float64(unk) / float64(r.total)
		}
	default:
		// No section at all — ask the reconstruction why.
		if reason := damagerecon.SkipModeReasonFromResult(res); reason != "" {
			r.source = "skipped:" + reason
		} else if _, err := damagerecon.Compute(res); err != nil {
			r.source, r.err = "none", err.Error()
		} else {
			r.source = "none"
		}
	}
	return r
}

// rawScan re-reads the demo at the event level, collecting the wire
// markers the analysis pipeline does not surface in the exact form this
// census wants: which matchdate FORMAT a print used (iso vs ctime — the
// resolved dateMarkers[] name the source, not the spelling), the
// "Date....:" stats-block print, and the raw presence of the
// //finalscores / //ktx drop directives independent of whether their
// payload survived parsing. Costs a second parse per demo, no analysis.
//
// The parse-warning columns are NOT gathered here any more: the reader's
// census rides every Result as parseWarnings (schema v72), so surveyOne
// reads it off the analysis pass it already paid for.
func rawScan(path string, r *row) {
	src, err := mvdsource.Open(path)
	if err != nil {
		return
	}
	defer src.Close()
	for {
		ev, err := src.Next()
		if err != nil || ev == nil {
			break
		}
		switch e := ev.(type) {
		case *events.PrintEvent:
			if i := strings.Index(e.Message, "matchdate: "); i >= 0 {
				// Format A is ISO (`2008-01-05 …`, yyyy then '-'); B is
				// ctime (`Mon Jul 03, …`). Enough to tell them apart.
				stamp := e.Message[i+len("matchdate: "):]
				if len(stamp) > 4 && stamp[4] == '-' {
					r.matchdate = "iso"
				} else {
					r.matchdate = "ctime"
				}
			} else if strings.Contains(e.Message, "Date....:") {
				r.dateBlock = true
			}
		case *events.StuffTextEvent:
			if strings.HasPrefix(e.Command, "//finalscores ") {
				r.finalscores = true
			} else if strings.HasPrefix(e.Command, "//ktx drop ") {
				r.ktxdrop = true
			}
		}
	}
}

// parseWarningCensus fills the warns / warnKinds / unknownSvc columns
// from the Result's own parse-warning census. Same numbers the second
// diagnostic parse used to produce (the counters are exact, and only
// the per-message sample table is capped), for no extra pass.
func parseWarningCensus(res *analyzer.Result, r *row) {
	pw := res.ParseWarnings
	if pw == nil {
		return
	}
	r.warns = pw.Total
	r.unknownSvc = pw.ByType["unknown_svc"]
	ks := make([]string, 0, len(pw.ByType))
	for k := range pw.ByType {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	r.warnKinds = strings.Join(ks, ";")
}

func b01(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func writeCSV(path string, rows []row) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	_ = w.Write([]string{"file", "map", "mode", "players", "frags", "matchMs",
		"source", "events", "totalDmg", "unknownPct", "bloods", "explosions", "lgbloods", "err",
		"version", "ktxver", "demoinfo", "wallclock", "matchdate", "dateBlock",
		"finalscores", "ktxdrop", "warns", "warnKinds", "unknownSvc"})
	for _, r := range rows {
		_ = w.Write([]string{r.file, r.mapname, r.mode,
			fmt.Sprint(r.players), fmt.Sprint(r.frags), fmt.Sprint(r.matchMs),
			r.source, fmt.Sprint(r.events), fmt.Sprint(r.total),
			fmt.Sprintf("%.1f", r.unkPct),
			fmt.Sprint(r.bloods), fmt.Sprint(r.expls), fmt.Sprint(r.lgbl), r.err,
			r.version, r.ktxver, b01(r.demoinfo), b01(r.wallclock), r.matchdate, b01(r.dateBlock),
			b01(r.finalscores), b01(r.ktxdrop), fmt.Sprint(r.warns), r.warnKinds, fmt.Sprint(r.unknownSvc)})
	}
	return nil
}

func summarize(rows []row) {
	bySource := map[string]int{}
	var reconUnk []float64
	withBlood, withExpl := 0, 0
	reconN := 0
	for _, r := range rows {
		key := r.source
		if strings.HasPrefix(key, "skipped:") {
			key = "skipped"
		}
		bySource[key]++
		if r.source == "reconstructed" {
			reconN++
			reconUnk = append(reconUnk, r.unkPct)
			if r.bloods > 0 {
				withBlood++
			}
			if r.expls > 0 {
				withExpl++
			}
		}
	}
	fmt.Printf("\noutcomes (%d demos):\n", len(rows))
	var keys []string
	for k := range bySource {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("  %-15s %5d  (%.1f%%)\n", k, bySource[k], 100*float64(bySource[k])/float64(len(rows)))
	}
	if reconN > 0 {
		sort.Float64s(reconUnk)
		q := func(p float64) float64 { return reconUnk[int(p*float64(len(reconUnk)-1))] }
		fmt.Printf("\nreconstructed demos (%d): unknown-damage share median %.1f%%  p90 %.1f%%  max %.1f%%\n",
			reconN, q(0.5), q(0.9), q(1.0))
		fmt.Printf("  carrying TE_BLOOD: %d (%.0f%%)   TE_EXPLOSION: %d (%.0f%%)\n",
			withBlood, 100*float64(withBlood)/float64(reconN),
			withExpl, 100*float64(withExpl)/float64(reconN))
	}
	if doReadability {
		n := float64(len(rows))
		c := func(f func(row) bool) string {
			k := 0
			for _, r := range rows {
				if f(r) {
					k++
				}
			}
			return fmt.Sprintf("%6d  (%.1f%%)", k, 100*float64(k)/n)
		}
		fmt.Printf("\nreadability census:\n")
		fmt.Printf("  demoinfo (ktxstats)   %s\n", c(func(r row) bool { return r.demoinfo }))
		fmt.Printf("  wallclock (current)   %s\n", c(func(r row) bool { return r.wallclock }))
		fmt.Printf("  matchdate: iso        %s\n", c(func(r row) bool { return r.matchdate == "iso" }))
		fmt.Printf("  matchdate: ctime      %s\n", c(func(r row) bool { return r.matchdate == "ctime" }))
		fmt.Printf("  Date....: block       %s\n", c(func(r row) bool { return r.dateBlock }))
		fmt.Printf("  any date on wire      %s\n", c(func(r row) bool { return r.matchdate != "" || r.dateBlock || r.demoinfo }))
		fmt.Printf("  //finalscores         %s\n", c(func(r row) bool { return r.finalscores }))
		fmt.Printf("  //ktx drop            %s\n", c(func(r row) bool { return r.ktxdrop }))
	}

	// Parse warnings ride every Result (schema v72), so this census costs
	// nothing and prints on every run — the point of surfacing them at all
	// is that a protocol gap is visible without asking for it.
	warnDemos, unknownSvcDemos, warnTotal := 0, 0, 0
	for _, r := range rows {
		warnTotal += r.warns
		if r.warns > 0 {
			warnDemos++
		}
		if r.unknownSvc > 0 {
			unknownSvcDemos++
		}
	}
	if warnDemos > 0 {
		n := float64(len(rows))
		fmt.Printf("\nparse warnings:\n")
		fmt.Printf("  demos with warnings   %6d  (%.1f%%), %d warnings total\n",
			warnDemos, 100*float64(warnDemos)/n, warnTotal)
		fmt.Printf("  with unknown_svc      %6d  (%.1f%%)\n",
			unknownSvcDemos, 100*float64(unknownSvcDemos)/n)
	}

	errs := map[string]int{}
	for _, r := range rows {
		if r.err != "" {
			// Bucket by the first few words so distinct files with the same
			// failure collapse into one line.
			msg := r.err
			if len(msg) > 60 {
				msg = msg[:60]
			}
			errs[msg]++
		}
	}
	if len(errs) > 0 {
		fmt.Printf("\nerrors:\n")
		type ec struct {
			msg string
			n   int
		}
		var lst []ec
		for m, n := range errs {
			lst = append(lst, ec{m, n})
		}
		sort.Slice(lst, func(i, j int) bool { return lst[i].n > lst[j].n })
		for i, e := range lst {
			if i >= 15 {
				fmt.Printf("  ... %d more error kinds\n", len(lst)-15)
				break
			}
			fmt.Printf("  %4d  %s\n", e.n, e.msg)
		}
	}
}
