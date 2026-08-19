// qw-aim-eval scores the RECONSTRUCTED aim hit tier (result.WeaponAimRecon,
// produced by aimcore's fire→recon-damage join) against the wire-measured one
// on demos that carry the KTX mvdhidden_dmgdone log.
//
// Withhold-and-compare: each demo is parsed once. The measured aim (hits
// linked against the wire damage stream) is kept as ground truth; then the
// wire damage section is REPLACED by damagerecon's blind reconstruction of the
// same match — the reconstruction never reads res.Damage or any damage-derived
// field, and neither does the recon join — and aim is recomputed, yielding the
// reconstructed tier exactly as an old demo would produce it. The two are
// compared per player, per weapon.
//
// Usage:
//
//	MVDA_BSP_DIR=./bsps go run ./mvd-analytics/cmd/qw-aim-eval [-dir ...] [-workers 4]
//	                                                           [-min-shots 20] [-diag] [-csv out.csv]
//
// Run from the repo root: the reconstruction wants ./bsps.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mvd-analyzer/mvd-analytics/aimcore"
	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/damagerecon"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// row is one (demo, player, weapon) comparison.
type row struct {
	demo, mode, player, weapon string
	shots                      int
	gtHits                     int // measured: fires linked to the wire damage stream
	rcHits                     int // reconstructed tier
	joinHits                   int // the recon JOIN run against the WIRE damage log
	rcPresent                  bool
}

func (r row) gtAcc() float64 { return frac(r.gtHits, r.shots) }
func (r row) rcAcc() float64 { return frac(r.rcHits, r.shots) }

func main() {
	dir := flag.String("dir", "mvd-analytics/testdata/cache", "directory of .mvd/.mvd.gz demos carrying the KTX damage log")
	workers := flag.Int("workers", 4, "parallel demo workers")
	minShots := flag.Int("min-shots", 20, "minimum fires for a (player,weapon) row to count toward the accuracy-error stats")
	diag := flag.Bool("diag", false, "print the fire→reconstructed-damage lag histogram (window calibration)")
	csvOut := flag.String("csv", "", "write the per-row comparison to this CSV")
	flag.Parse()

	paths, err := demoPaths(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qw-aim-eval:", err)
		os.Exit(1)
	}
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "qw-aim-eval: no demos in", *dir)
		os.Exit(1)
	}

	var (
		mu      sync.Mutex
		rows    []row
		lag     = map[int32]int{}
		scored  int
		skipped int
	)
	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				rs, lg, err := scoreDemo(path, *diag)
				mu.Lock()
				if err != nil {
					skipped++
					fmt.Fprintf(os.Stderr, "%s: %v\n", filepath.Base(path), err)
				} else {
					scored++
					rows = append(rows, rs...)
					for k, v := range lg {
						lag[k] += v
					}
				}
				mu.Unlock()
			}
		}()
	}
	for _, p := range paths {
		jobs <- p
	}
	close(jobs)
	wg.Wait()

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].demo != rows[j].demo {
			return rows[i].demo < rows[j].demo
		}
		if rows[i].player != rows[j].player {
			return rows[i].player < rows[j].player
		}
		return rows[i].weapon < rows[j].weapon
	})

	fmt.Printf("demos scored: %d (skipped %d)\n", scored, skipped)
	printWeapons(rows, *minShots)
	if *diag {
		printLag(lag)
	}
	if *csvOut != "" {
		if err := writeCSV(*csvOut, rows); err != nil {
			fmt.Fprintln(os.Stderr, "qw-aim-eval: csv:", err)
		}
	}
}

// scoreDemo parses one demo, keeps its measured aim, swaps in the blind
// reconstruction of the same match, recomputes aim, and pairs the two.
func scoreDemo(path string, diag bool) ([]row, map[int32]int, error) {
	reg := analyzer.NewDefaultRegistry()
	reg.BuildShotStreams = true
	res, err := reg.Analyze(path)
	if err != nil {
		return nil, nil, fmt.Errorf("analyze: %w", err)
	}
	if res.Damage == nil || res.Damage.Source != result.DamageSourceKTX {
		return nil, nil, fmt.Errorf("no KTX damage ground truth")
	}
	if res.Aim == nil || !res.Aim.HitsMeasured {
		return nil, nil, fmt.Errorf("no measured aim")
	}
	gtAim := res.Aim
	gtDmg := res.Damage

	// Control: the recon JOIN run against the WIRE damage log (same events the
	// measured counters were linked from, relabelled so aim takes the recon
	// path). It separates the join method's own error — impact clustering, the
	// projectile budget — from the reconstruction's.
	joinCtl := *gtDmg
	joinCtl.Source = result.DamageSourceReconstructed
	res.Damage = &joinCtl
	joinAim := aimcore.Compute(res, aimcore.Query{})

	rc, err := damagerecon.Compute(res)
	if err != nil {
		return nil, nil, fmt.Errorf("recon: %w", err)
	}
	res.Damage = rc
	rcAim := aimcore.Compute(res, aimcore.Query{})
	if rcAim == nil {
		return nil, nil, fmt.Errorf("recon aim: nil")
	}
	if rcAim.HitsMeasured {
		return nil, nil, fmt.Errorf("recon aim still claims hitsMeasured")
	}
	if rcAim.HitsSource != result.AimHitsSourceReconstructed {
		return nil, nil, fmt.Errorf("recon aim hitsSource = %q", rcAim.HitsSource)
	}

	demo := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".gz"), ".mvd")
	mode := ""
	if res.DemoInfo != nil {
		mode = res.DemoInfo.Mode
	}

	type key struct{ player, weapon string }
	rcHits := map[key]*result.WeaponAimRecon{}
	rcShots := map[key]int{}
	for _, pa := range rcAim.Players {
		for i := range pa.Weapons {
			w := &pa.Weapons[i]
			rcHits[key{pa.Player, w.Weapon}] = w.Recon
			rcShots[key{pa.Player, w.Weapon}] = w.Shots
		}
	}
	joinHits := map[key]int{}
	if joinAim != nil {
		for _, pa := range joinAim.Players {
			for i := range pa.Weapons {
				if w := &pa.Weapons[i]; w.Recon != nil {
					joinHits[key{pa.Player, w.Weapon}] = w.Recon.Hits
				}
			}
		}
	}
	var out []row
	for _, pa := range gtAim.Players {
		for i := range pa.Weapons {
			w := &pa.Weapons[i]
			k := key{pa.Player, w.Weapon}
			r := row{demo: demo, mode: mode, player: pa.Player, weapon: w.Weapon, shots: w.Shots, gtHits: w.Hits}
			if s, ok := rcShots[k]; ok && s != w.Shots {
				return nil, nil, fmt.Errorf("%s/%s: shots moved %d -> %d", pa.Player, w.Weapon, w.Shots, s)
			}
			if rec := rcHits[k]; rec != nil {
				r.rcPresent, r.rcHits = true, rec.Hits
			}
			r.joinHits = joinHits[k]
			out = append(out, r)
		}
	}
	var lag map[int32]int
	if diag {
		lag = lagHistogram(gtDmg, rc)
	}
	return out, lag, nil
}

// lagHistogram bins, for every wire damage event, the offset to the nearest
// reconstructed event with the same attacker+weapon+victim — the quantization
// the recon join's link windows have to absorb. Binned at 10 ms.
func lagHistogram(gt, rc *result.DamageResult) map[int32]int {
	type key struct{ attacker, victim, weapon string }
	idx := map[key][]int32{}
	for i := range rc.Events {
		e := &rc.Events[i]
		k := key{e.Attacker, e.Victim, e.Weapon}
		idx[k] = append(idx[k], e.Time)
	}
	for k := range idx {
		ts := idx[k]
		sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	}
	out := map[int32]int{}
	for i := range gt.Events {
		e := &gt.Events[i]
		if e.IsEnv {
			continue
		}
		ts := idx[key{e.Attacker, e.Victim, e.Weapon}]
		if len(ts) == 0 {
			out[9999]++ // no counterpart at all
			continue
		}
		best := int32(1 << 30)
		for _, t := range ts {
			if d := t - e.Time; abs32(d) < abs32(best) {
				best = d
			}
		}
		if abs32(best) > 500 {
			out[9999]++
			continue
		}
		bin := best / 10 * 10
		out[bin]++
	}
	return out
}

func printWeapons(rows []row, minShots int) {
	byW := map[string][]row{}
	var order []string
	for _, r := range rows {
		if _, ok := byW[r.weapon]; !ok {
			order = append(order, r.weapon)
		}
		byW[r.weapon] = append(byW[r.weapon], r)
	}
	sort.Slice(order, func(i, j int) bool { return aimRank(order[i]) < aimRank(order[j]) })

	fmt.Printf("\n== per-weapon totals (all rows). join-acc = the recon join run on the WIRE log (method control)\n")
	fmt.Printf("   %-5s %6s %9s %9s %9s %9s %9s %9s %8s\n", "wpn", "rows", "shots", "gt-hits", "rc-hits", "gt-acc", "rc-acc", "join-acc", "tier")
	for _, w := range order {
		var shots, gh, rh, jh, present int
		for _, r := range byW[w] {
			shots += r.shots
			gh += r.gtHits
			rh += r.rcHits
			jh += r.joinHits
			if r.rcPresent {
				present++
			}
		}
		fmt.Printf("   %-5s %6d %9d %9d %9d %8.1f%% %8.1f%% %8.1f%% %7.0f%%\n",
			w, len(byW[w]), shots, gh, rh, 100*frac(gh, shots), 100*frac(rh, shots),
			100*frac(jh, shots), 100*frac(present, len(byW[w])))
	}

	for _, mode := range []string{"reconstructed", "join-on-wire (control)"} {
		printErrors(order, byW, minShots, mode == "reconstructed", mode)
	}
}

func printErrors(order []string, byW map[string][]row, minShots int, recon bool, label string) {
	fmt.Printf("\n== per-weapon accuracy error vs measured — %s, rows with >= %d fires\n", label, minShots)
	fmt.Printf("   %-5s %6s %10s %10s %10s %10s %8s %8s\n",
		"wpn", "rows", "med|Δacc|", "mean|Δacc|", "p90|Δacc|", "bias", "<=2pp", "<=5pp")
	for _, w := range order {
		var errs, signed []float64
		for _, r := range byW[w] {
			if !r.rcPresent || r.shots < minShots {
				continue
			}
			d := frac(r.joinHits, r.shots) - r.gtAcc()
			if recon {
				d = r.rcAcc() - r.gtAcc()
			}
			signed = append(signed, d)
			errs = append(errs, absF(d))
		}
		if len(errs) == 0 {
			continue
		}
		sort.Float64s(errs)
		n := len(errs)
		mean, bias, w2, w5 := 0.0, 0.0, 0, 0
		for _, d := range signed {
			bias += d
		}
		for _, e := range errs {
			mean += e
			if e <= 0.02 {
				w2++
			}
			if e <= 0.05 {
				w5++
			}
		}
		fmt.Printf("   %-5s %6d %9.1fpp %9.1fpp %9.1fpp %9.1fpp %7.0f%% %7.0f%%\n",
			w, n, 100*errs[n/2], 100*mean/float64(n), 100*errs[minInt(n-1, n*9/10)],
			100*bias/float64(n), 100*frac(w2, n), 100*frac(w5, n))
	}
}

func printLag(lag map[int32]int) {
	var bins []int32
	total := 0
	for b, c := range lag {
		bins = append(bins, b)
		total += c
	}
	sort.Slice(bins, func(i, j int) bool { return bins[i] < bins[j] })
	fmt.Printf("\n== wire-event → reconstructed-event lag (ms, 10 ms bins; 9999 = no counterpart)\n")
	cum := 0
	for _, b := range bins {
		cum += lag[b]
		fmt.Printf("   %6d %9d %6.2f%% %6.2f%% cum\n", b, lag[b], 100*frac(lag[b], total), 100*frac(cum, total))
	}
}

func writeCSV(path string, rows []row) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"demo", "mode", "player", "weapon", "shots", "gtHits", "rcHits", "tier"}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write([]string{
			r.demo, r.mode, r.player, r.weapon,
			strconv.Itoa(r.shots), strconv.Itoa(r.gtHits), strconv.Itoa(r.rcHits),
			strconv.FormatBool(r.rcPresent),
		}); err != nil {
			return err
		}
	}
	return nil
}

func demoPaths(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		n := e.Name()
		if strings.HasSuffix(n, ".mvd") || strings.HasSuffix(n, ".mvd.gz") {
			out = append(out, filepath.Join(dir, n))
		}
	}
	sort.Strings(out)
	return out, nil
}

func aimRank(w string) int {
	for i, x := range []string{"lg", "sg", "ssg", "rl", "gl", "sng", "ng", "axe"} {
		if x == w {
			return i
		}
	}
	return 99
}

func frac(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
}

func absF(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
