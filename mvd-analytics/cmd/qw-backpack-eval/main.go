// qw-backpack-eval scores the backpack reconstruction
// (analyzer.ReconstructBackpackDrops) against KTX ground truth: the
// `//ktx drop` hints that KTX >= 1.38 emits on every real RL/LG pack.
//
// For every demo it runs the full pipeline, keeps the hint-derived
// result.Backpacks as ground truth, re-runs the reconstruction on the same
// Result with the hints withheld (the reconstruction never reads
// res.Backpacks), and reports precision, recall, the per-weapon split, the
// position-error distribution and the mismatch classes.
//
// Usage:
//
//	go run ./mvd-analytics/cmd/qw-backpack-eval -dir /mnt/.../mvd \
//	    -list demos.txt [-csv readability-51k.csv] [-jobs 8] [-worst 10]
//
// -list is a file of demo basenames (one per line); -csv joins the archive
// readability census so results can be split per era (its `version` and
// `ktxver` columns). With neither, every demo in -dir is scored.
//
// Volume mode (-volume) skips the GT scoring and instead reports the
// drops-per-death rate of whatever the pipeline produced — the sanity check
// for the hint-less population, where no ground truth exists.
package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// matchTolMs bounds the join between a reconstructed drop and its hint. The
// hint is stuffed inside PlayerDie; the death marker the reconstruction keys
// on is the same server frame carried by a different message, so real pairs
// sit within a couple of demo frames.
const matchTolMs = 500

type demoMeta struct {
	version string
	ktxver  string
	mode    string
}

type demoResult struct {
	name       string
	era        string
	mode       string
	skipped    string // non-empty: not scored, and why
	gt, rc     int
	matched    int
	byWeaponGT map[string]int
	byWeaponOK map[string]int
	byWeaponRC map[string]int
	// mismatch classes
	wrongWeapon int // paired in time, disagreed on the weapon
	extraNoGT   int // reconstructed a drop with no hint anywhere near
	missedNoRC  int // hint with no reconstructed drop near it
	posErr      []float64
	timeErr     []int32
	deaths      int
	// missDiag / extraDiag explain each unmatched row in the vocabulary of
	// the pass's own conditions.
	missDiag  []string
	extraDiag []string
	// invOK counts drops whose weapon the victim also OWNED per the
	// STAT_ITEMS interval streams — an oracle independent of
	// STAT_ACTIVEWEAPON, and the only cross-check available where no hint
	// exists.
	invOK int
}

// ownedAt reports whether the dropper's inventory streams show them holding
// the dropped weapon at the drop instant. The interval closes AT the death,
// so the closing edge counts as held.
func ownedAt(res *result.Result, b result.BackpackDrop) bool {
	for i := range res.Streams.Players {
		p := &res.Streams.Players[i]
		if p.Name != b.Player {
			continue
		}
		ivs := p.RL
		if b.Weapon == "lg" {
			ivs = p.LG
		}
		for _, iv := range ivs {
			if b.Time >= iv.Start && b.Time <= iv.End {
				return true
			}
		}
	}
	return false
}

func main() {
	dir := flag.String("dir", "", "directory holding the demo files")
	list := flag.String("list", "", "file of demo basenames to score (one per line)")
	csvPath := flag.String("csv", "", "readability census CSV, joined on the file column for per-era splits")
	jobs := flag.Int("jobs", 8, "parallel demo workers")
	worst := flag.Int("worst", 10, "how many worst-precision demos to list")
	volume := flag.Bool("volume", false, "no ground truth: report drops-per-death volume of the shipped pipeline")
	flag.Parse()

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "qw-backpack-eval: -dir is required")
		os.Exit(1)
	}
	names, err := demoNames(*dir, *list)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qw-backpack-eval:", err)
		os.Exit(1)
	}
	meta := map[string]demoMeta{}
	if *csvPath != "" {
		if meta, err = loadMeta(*csvPath); err != nil {
			fmt.Fprintln(os.Stderr, "qw-backpack-eval:", err)
			os.Exit(1)
		}
	}

	results := make([]demoResult, len(names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, *jobs)
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					results[i] = demoResult{name: name, skipped: fmt.Sprintf("panic: %v", r)}
				}
			}()
			results[i] = scoreOne(filepath.Join(*dir, name), meta[name], *volume)
		}(i, name)
	}
	wg.Wait()

	if *volume {
		reportVolume(results)
		return
	}
	report(results, *worst)
}

func scoreOne(path string, m demoMeta, volume bool) demoResult {
	name := filepath.Base(path)
	out := demoResult{
		name:       name,
		era:        eraOf(m),
		mode:       m.mode,
		byWeaponGT: map[string]int{},
		byWeaponOK: map[string]int{},
		byWeaponRC: map[string]int{},
	}
	reg := analyzer.NewDefaultRegistry()
	res, err := reg.Analyze(path)
	if err != nil {
		out.skipped = "analyze: " + err.Error()
		return out
	}
	if res.Streams != nil {
		for i := range res.Streams.Players {
			out.deaths += len(res.Streams.Players[i].Deaths)
		}
	}
	if out.mode == "" && res.DemoInfo != nil {
		out.mode = res.DemoInfo.Mode
	}
	if volume {
		for _, b := range res.Backpacks {
			if b.Source != result.BackpackSourceReconstructed {
				// A hint-carrying demo landed in the hint-LESS sample. Its
				// rows are ground truth, not reconstruction output, and
				// reportVolume must not fold them into the rate it publishes
				// as the reconstruction's — nor into the inventory
				// cross-check, which exists to corroborate reconstructed
				// rows specifically.
				out.skipped = "wire-hinted, not reconstructed"
				return out
			}
		}
		out.rc = len(res.Backpacks)
		for _, b := range res.Backpacks {
			out.byWeaponRC[b.Weapon]++
			if ownedAt(res, b) {
				out.invOK++
			}
		}
		if len(res.Backpacks) == 0 {
			out.skipped = analyzer.BackpackReconStandDown(res)
			if out.skipped == "" {
				out.skipped = "no drops produced"
			}
		}
		return out
	}

	gt := res.Backpacks
	if len(gt) == 0 {
		out.skipped = "no //ktx drop ground truth"
		return out
	}
	// Ground truth means the WIRE said it. A demo whose backpacks section was
	// filled by the reconstruction itself — a list entry that turned out not
	// to be hint-carrying — would otherwise be scored against its own output
	// and report a flawless 100/100, silently inflating the headline.
	for _, g := range gt {
		if g.Source != result.BackpackSourceKTX {
			out.skipped = "backpacks are reconstructed, not ground truth"
			return out
		}
	}
	// The one stand-down that only exists BECAUSE the hint is present is
	// discounted here — withholding the hint is the point of the experiment.
	// Every other refusal is scored as the pipeline would apply it.
	if reason := analyzer.BackpackReconStandDown(res); reason != "" && reason != "hinting mod emitted no drops" {
		out.skipped = "stand-down: " + reason
		return out
	}
	rc := analyzer.ReconstructBackpackDrops(res)

	out.gt, out.rc = len(gt), len(rc)
	for _, g := range gt {
		out.byWeaponGT[g.Weapon]++
	}
	for _, r := range rc {
		out.byWeaponRC[r.Weapon]++
	}

	usedGT := make([]bool, len(gt))
	for _, r := range rc {
		best, bestDt := -1, int32(math.MaxInt32)
		for gi, g := range gt {
			if usedGT[gi] || g.Player != r.Player || g.Weapon != r.Weapon {
				continue
			}
			dt := abs32(g.Time - r.Time)
			if dt <= matchTolMs && dt < bestDt {
				best, bestDt = gi, dt
			}
		}
		if best < 0 {
			// No same-weapon hint: is there one for the other weapon (a
			// wielded-weapon disagreement) or nothing at all (a fabricated
			// drop)?
			paired := false
			for gi, g := range gt {
				if usedGT[gi] || g.Player != r.Player {
					continue
				}
				if abs32(g.Time-r.Time) <= matchTolMs {
					usedGT[gi] = true
					paired = true
					break
				}
			}
			if paired {
				out.wrongWeapon++
			} else {
				out.extraNoGT++
				out.extraDiag = append(out.extraDiag, diagnoseExtra(gt, r))
			}
			continue
		}
		usedGT[best] = true
		out.matched++
		out.byWeaponOK[r.Weapon]++
		out.timeErr = append(out.timeErr, bestDt)
		out.posErr = append(out.posErr, dist3(gt[best].Origin, r.Origin))
	}
	for gi := range gt {
		if !usedGT[gi] {
			out.missedNoRC++
			out.missDiag = append(out.missDiag, diagnoseMiss(res, gt[gi]))
		}
	}
	return out
}

// diagnoseMiss explains why the reconstruction produced nothing for a hint:
// which of the pass's own conditions turned the death away.
func diagnoseMiss(res *result.Result, g result.BackpackDrop) string {
	var p *result.PlayerStream
	for i := range res.Streams.Players {
		if res.Streams.Players[i].Name == g.Player {
			p = &res.Streams.Players[i]
			break
		}
	}
	if p == nil {
		return "no stream for dropper (roster name mismatch)"
	}
	// Nearest death marker.
	bestDt := int32(math.MaxInt32)
	var bestT int32
	for _, td := range p.Deaths {
		if dt := abs32(td - g.Time); dt < bestDt {
			bestDt, bestT = dt, td
		}
	}
	if bestDt > matchTolMs {
		return fmt.Sprintf("no death marker within %dms (nearest %dms)", matchTolMs, bestDt)
	}
	aw, ok := valueAtOrBefore(p.ActiveWeapon, bestT)
	if !ok {
		return "no active-weapon sample at or before the death"
	}
	if int(aw) != weaponBit(g.Weapon) {
		return fmt.Sprintf("active weapon %d, hint says %s", aw, g.Weapon)
	}
	if _, ok := posAtOrBefore(p.Position, bestT); !ok {
		return "position track stale or absent at the death"
	}
	// Reaching here means the drop WAS produced but paired with a different
	// hint (duplicate deaths within the window).
	return "produced but paired elsewhere (duplicate deaths in window)"
}

// diagnoseExtra explains a reconstructed drop no hint accounts for: how far
// the nearest same-player hint was, and whether the dropper appears in the
// hint log at all.
func diagnoseExtra(gt []result.BackpackDrop, r result.BackpackDrop) string {
	best := int32(math.MaxInt32)
	sameWep := int32(math.MaxInt32)
	seen := false
	for _, g := range gt {
		if g.Player != r.Player {
			continue
		}
		seen = true
		if dt := abs32(g.Time - r.Time); dt < best {
			best = dt
		}
		if g.Weapon == r.Weapon {
			if dt := abs32(g.Time - r.Time); dt < sameWep {
				sameWep = dt
			}
		}
	}
	if !seen {
		return "dropper has no hint at all in this demo"
	}
	switch {
	case best <= 2000:
		return fmt.Sprintf("nearest same-player hint %dms away (just outside the %dms window)", best, matchTolMs)
	case sameWep == math.MaxInt32:
		return "no same-weapon hint anywhere for this dropper"
	}
	return "no hint within 2s of the death"
}

func weaponBit(w string) int {
	switch w {
	case "rl":
		return 32
	case "lg":
		return 64
	}
	return 0
}

func valueAtOrBefore(col []result.ChangeI16, t int32) (int16, bool) {
	i := sort.Search(len(col), func(j int) bool { return col[j].T > t }) - 1
	if i < 0 {
		return 0, false
	}
	return col[i].V, true
}

func posAtOrBefore(pt *result.PositionTrack, t int32) ([3]float32, bool) {
	if pt == nil || len(pt.T) == 0 {
		return [3]float32{}, false
	}
	i := sort.Search(len(pt.T), func(j int) bool { return pt.T[j] > t }) - 1
	if i < 0 || t-pt.T[i] > 400 {
		return [3]float32{}, false
	}
	return [3]float32{pt.X[i], pt.Y[i], pt.Z[i]}, true
}

func report(rows []demoResult, worst int) {
	var scored, skipped int
	var gt, rc, matched, wrongW, extra, missed, deaths int
	byWeaponGT, byWeaponOK, byWeaponRC := map[string]int{}, map[string]int{}, map[string]int{}
	perEra := map[string]*demoResult{}
	perMode := map[string]*demoResult{}
	var posErr []float64
	var timeErr []int32
	skipReasons := map[string]int{}

	for i := range rows {
		r := &rows[i]
		if r.skipped != "" {
			skipped++
			skipReasons[classifySkip(r.skipped)]++
			continue
		}
		scored++
		gt += r.gt
		rc += r.rc
		matched += r.matched
		wrongW += r.wrongWeapon
		extra += r.extraNoGT
		missed += r.missedNoRC
		deaths += r.deaths
		posErr = append(posErr, r.posErr...)
		timeErr = append(timeErr, r.timeErr...)
		for w, n := range r.byWeaponGT {
			byWeaponGT[w] += n
		}
		for w, n := range r.byWeaponOK {
			byWeaponOK[w] += n
		}
		for w, n := range r.byWeaponRC {
			byWeaponRC[w] += n
		}
		accumulate(perEra, r.era, r)
		accumulate(perMode, modeKey(r.mode), r)
	}

	fmt.Printf("demos: %d scored, %d skipped\n", scored, skipped)
	if len(skipReasons) > 0 {
		fmt.Println("\nskip reasons:")
		for _, k := range sortedKeys(skipReasons) {
			fmt.Printf("  %-46s %d\n", k, skipReasons[k])
		}
	}
	fmt.Printf("\nhint drops (GT): %d   reconstructed: %d   matched: %d\n", gt, rc, matched)
	fmt.Printf("precision: %.2f%%   recall: %.2f%%\n", pct(matched, rc), pct(matched, gt))
	fmt.Printf("mismatch classes: wrong-weapon %d (%.2f%% of recon)  fabricated %d (%.2f%%)  missed %d (%.2f%% of GT)\n",
		wrongW, pct(wrongW, rc), extra, pct(extra, rc), missed, pct(missed, gt))
	fmt.Printf("volume: %d deaths, GT %.3f drops/death, recon %.3f drops/death\n",
		deaths, ratio(gt, deaths), ratio(rc, deaths))

	fmt.Println("\nper weapon:")
	fmt.Printf("  %-4s %8s %8s %8s %10s %10s\n", "wep", "gt", "recon", "matched", "precision", "recall")
	for _, w := range []string{"rl", "lg"} {
		fmt.Printf("  %-4s %8d %8d %8d %9.2f%% %9.2f%%\n", w,
			byWeaponGT[w], byWeaponRC[w], byWeaponOK[w],
			pct(byWeaponOK[w], byWeaponRC[w]), pct(byWeaponOK[w], byWeaponGT[w]))
	}

	fmt.Println("\nposition error of matched drops (units):")
	printQuantiles(posErr)
	for _, thr := range []float64{50, 100, 200} {
		n := 0
		for _, e := range posErr {
			if e > thr {
				n++
			}
		}
		fmt.Printf("  >%3.0f units: %d (%.3f%%)\n", thr, n, pct(n, len(posErr)))
	}
	fmt.Println("time error of matched drops (ms):")
	printQuantilesI(timeErr)

	missClasses, extraClasses := map[string]int{}, map[string]int{}
	for i := range rows {
		if rows[i].skipped != "" {
			continue
		}
		for _, d := range rows[i].missDiag {
			missClasses[d]++
		}
		for _, d := range rows[i].extraDiag {
			extraClasses[d]++
		}
	}
	if len(missClasses) > 0 {
		fmt.Println("\nmissed hints, by cause:")
		for _, k := range sortedKeys(missClasses) {
			fmt.Printf("  %-60s %d\n", k, missClasses[k])
		}
	}
	if len(extraClasses) > 0 {
		fmt.Println("\nfabricated drops, by cause:")
		for _, k := range sortedKeys(extraClasses) {
			fmt.Printf("  %-60s %d\n", k, extraClasses[k])
		}
	}

	printGroup("per era", perEra)
	printGroup("per mode", perMode)

	if worst > 0 {
		var bad []demoResult
		for _, r := range rows {
			if r.skipped == "" && r.rc > 0 && (r.rc-r.matched) > 0 {
				bad = append(bad, r)
			}
		}
		sort.Slice(bad, func(i, j int) bool {
			return (bad[i].rc - bad[i].matched) > (bad[j].rc - bad[j].matched)
		})
		if len(bad) > worst {
			bad = bad[:worst]
		}
		if len(bad) > 0 {
			fmt.Printf("\nworst demos by unmatched reconstructed drops:\n")
			for _, r := range bad {
				fmt.Printf("  %s  era=%s mode=%s gt=%d rc=%d matched=%d wrongW=%d extra=%d missed=%d\n",
					r.name[:12], r.era, modeKey(r.mode), r.gt, r.rc, r.matched, r.wrongWeapon, r.extraNoGT, r.missedNoRC)
			}
		}
	}
}

func reportVolume(rows []demoResult) {
	var measured, withDrops, without, excluded, deaths, drops, invOK int
	reasons := map[string]int{}
	byWeapon := map[string]int{}
	perEra := map[string]*demoResult{}
	for i := range rows {
		r := &rows[i]
		// A demo whose section did not come from the reconstruction (a
		// hint-carrying demo, an analyze failure) is not evidence about the
		// reconstruction. It is counted and named, and contributes nothing to
		// the rate or the cross-check.
		if r.skipped == "wire-hinted, not reconstructed" || strings.HasPrefix(r.skipped, "analyze") || strings.HasPrefix(r.skipped, "panic") {
			excluded++
			reasons[classifySkip(r.skipped)]++
			continue
		}
		measured++
		deaths += r.deaths
		drops += r.rc
		invOK += r.invOK
		for w, n := range r.byWeaponRC {
			byWeapon[w] += n
		}
		if r.rc > 0 {
			withDrops++
		} else {
			without++
			reasons[classifySkip(r.skipped)]++
		}
		accumulate(perEra, r.era, r)
	}
	fmt.Printf("demos: %d sampled, %d measured, %d excluded\n", len(rows), measured, excluded)
	fmt.Printf("measured demos: with reconstructed drops: %d   without: %d\n", withDrops, without)
	fmt.Printf("deaths: %d   drops: %d   rate: %.3f drops/death (rl %d, lg %d)\n",
		deaths, drops, ratio(drops, deaths), byWeapon["rl"], byWeapon["lg"])
	fmt.Printf("inventory cross-check: %d/%d (%.2f%%) of drops had the weapon in STAT_ITEMS at the death\n",
		invOK, drops, pct(invOK, drops))
	if len(reasons) > 0 {
		fmt.Println("\nwhy no drops:")
		for _, k := range sortedKeys(reasons) {
			fmt.Printf("  %-46s %d\n", k, reasons[k])
		}
	}
	fmt.Println("\nper era (rate = drops/death):")
	for _, k := range sortedGroupKeys(perEra) {
		g := perEra[k]
		fmt.Printf("  %-24s demos=%-5d deaths=%-7d drops=%-6d rate=%.3f\n", k, g.matched, g.deaths, g.rc, ratio(g.rc, g.deaths))
	}
}

// accumulate folds one demo into a group bucket. matched doubles as the
// demo counter on the group row (groups are never scored themselves).
func accumulate(m map[string]*demoResult, key string, r *demoResult) {
	g := m[key]
	if g == nil {
		g = &demoResult{byWeaponGT: map[string]int{}, byWeaponOK: map[string]int{}, byWeaponRC: map[string]int{}}
		m[key] = g
	}
	g.matched++ // demo count
	g.gt += r.gt
	g.rc += r.rc
	g.deaths += r.deaths
	g.wrongWeapon += r.wrongWeapon
	g.extraNoGT += r.extraNoGT
	g.missedNoRC += r.missedNoRC
	g.byWeaponOK["matched"] += len(r.posErr)
}

func printGroup(title string, m map[string]*demoResult) {
	if len(m) == 0 {
		return
	}
	fmt.Printf("\n%s:\n", title)
	fmt.Printf("  %-24s %6s %8s %8s %8s %10s %10s\n", "", "demos", "gt", "recon", "matched", "precision", "recall")
	for _, k := range sortedGroupKeys(m) {
		g := m[k]
		ok := g.byWeaponOK["matched"]
		fmt.Printf("  %-24s %6d %8d %8d %8d %9.2f%% %9.2f%%\n", k, g.matched, g.gt, g.rc, ok, pct(ok, g.rc), pct(ok, g.gt))
	}
}

// eraOf buckets a demo by the sharp feature gate, ktxver, falling back to
// the mvdsv version string when the mod published no ktxver at all.
func eraOf(m demoMeta) string {
	if m.ktxver == "" && m.version == "" {
		return "unknown"
	}
	if m.ktxver == "" {
		return "pre-ktx (" + majorMinor(m.version) + ")"
	}
	return "ktx " + majorMinor(m.ktxver)
}

func majorMinor(s string) string {
	// "MVDSV 0.34-beta-RekiFork" -> "MVDSV 0.34"; "1.40-beta-x" -> "1.40"
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return s
	}
	tail := fields[len(fields)-1]
	for i := 0; i < len(tail); i++ {
		c := tail[i]
		if (c < '0' || c > '9') && c != '.' {
			tail = tail[:i]
			break
		}
	}
	if len(fields) > 1 {
		return fields[0] + " " + tail
	}
	return tail
}

func modeKey(mode string) string {
	if mode == "" {
		return "(none)"
	}
	return strings.ToLower(mode)
}

// classifySkip collapses the free-text skip strings into stable buckets so
// the summary table stays readable across thousands of demos.
func classifySkip(s string) string {
	if s == "" {
		return "(none)"
	}
	if i := strings.Index(s, ":"); i > 0 && strings.HasPrefix(s, "analyze") {
		return "analyze error"
	}
	if strings.HasPrefix(s, "panic") {
		return "panic"
	}
	return s
}

func demoNames(dir, list string) ([]string, error) {
	if list != "" {
		f, err := os.Open(list)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		var out []string
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if n := strings.TrimSpace(sc.Text()); n != "" {
				out = append(out, filepath.Base(n))
			}
		}
		return out, sc.Err()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasSuffix(n, ".mvd") || strings.HasSuffix(n, ".mvd.gz") {
			out = append(out, n)
		}
	}
	return out, nil
}

func loadMeta(path string) (map[string]demoMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	head, err := r.Read()
	if err != nil {
		return nil, err
	}
	col := map[string]int{}
	for i, h := range head {
		col[h] = i
	}
	get := func(rec []string, name string) string {
		i, ok := col[name]
		if !ok || i >= len(rec) {
			return ""
		}
		return rec[i]
	}
	out := map[string]demoMeta{}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		out[get(rec, "file")] = demoMeta{
			version: get(rec, "version"),
			ktxver:  get(rec, "ktxver"),
			mode:    get(rec, "mode"),
		}
	}
	return out, nil
}

func dist3(a, b [3]float32) float64 {
	dx := float64(a[0] - b[0])
	dy := float64(a[1] - b[1])
	dz := float64(a[2] - b[2])
	return math.Sqrt(dx*dx + dy*dy + dz*dz)
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func printQuantiles(v []float64) {
	if len(v) == 0 {
		fmt.Println("  (none)")
		return
	}
	sort.Float64s(v)
	q := func(p float64) float64 { return v[min(len(v)-1, int(p*float64(len(v))))] }
	fmt.Printf("  n=%d  p50=%.1f  p90=%.1f  p99=%.1f  max=%.1f  mean=%.1f\n",
		len(v), q(0.5), q(0.9), q(0.99), v[len(v)-1], mean(v))
}

func printQuantilesI(v []int32) {
	f := make([]float64, len(v))
	for i, x := range v {
		f[i] = float64(x)
	}
	printQuantiles(f)
}

func mean(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if m[out[i]] != m[out[j]] {
			return m[out[i]] > m[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

func sortedGroupKeys(m map[string]*demoResult) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if m[out[i]].matched != m[out[j]].matched {
			return m[out[i]].matched > m[out[j]].matched
		}
		return out[i] < out[j]
	})
	return out
}
