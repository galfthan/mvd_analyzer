package main

// Linkage mode scores the PICKUP side of the reconstruction
// (analyzer.LinkBackpackDrops) against the second KTX ground truth: the
// `//ktx bp <backpack_ent> <player_ent>` hints KTX emits on every RL/LG pack
// touch (ktx/src/items.c:2489-2494), which the pipeline publishes as
// weaponPickups rows with Source "backpack".
//
// The experiment is the drop eval's, one layer up: keep the wire's answer,
// re-run the reconstruction AND the linkage with both hints withheld, and
// score the classification (picked / expired / unobserved), the attributed
// picker and the pickup time.
//
// What it refuses to score, and why:
//
//   - A demo whose backpacks section is itself reconstructed. Same guard as
//     the drop eval: scoring the pass against its own output is a free
//     100/100.
//   - A demo that emits `//ktx drop` but no `//ktx bp` at all. Measured on the
//     probe sample: 10 of 24 hint-carrying demos are like this, and on them
//     EVERY real pickup would score as a false positive. The pickup hint is a
//     separate KTX generation from the drop hint and the population has to be
//     gated on it, not assumed from it.
//   - A drop the reconstruction did not reproduce. Its linkage cannot be
//     evidence about the linkage — the drop-side eval is where that miss is
//     already counted, and folding it in here would double-count one defect
//     as two.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// linkResult is one demo's pickup-linkage score.
type linkResult struct {
	name    string
	era     string
	mode    string
	skipped string

	demos       int // group rows only: how many demos folded in
	pairedDrops int // reconstructed drops that matched a GT drop; the scored denominator

	gtPicked, gtNotPicked int
	// confusion: our class × GT class
	pickedRight   int // we said picked, GT agrees
	pickedWrong   int // we said picked, GT says nobody took it
	expiredRight  int // we said expired, GT agrees nobody took it
	expiredWrong  int // we said expired, GT says it was picked
	unobsGTPicked int
	unobsGTNot    int

	attributed   int // we said picked AND named a picker, GT agrees it was picked
	attributedOK int
	unattributed int // we said picked, GT agrees, but named nobody

	timeErr []int32
	// packLife is the bound pack's measured lifetime, split by what the wire
	// says happened to it. The never-picked side is the evidence the expiry
	// threshold is set from: KTX arms SUB_Remove for creation + 120 s, and
	// this is what that looks like after both ends are quantised to demo
	// frames. Without it the threshold is a guess.
	lifeNotPicked, lifePicked []int32
	wrongNames                []string
	// unobsDiag explains each drop we left unobserved that the wire says was
	// picked, in the vocabulary of the pass's own conditions.
	unobsDiag []string
	// bound counts drops the linkage bound to a pack entity at all.
	bound int
}

func scoreLinkage(path string, m demoMeta) linkResult {
	name := baseName(path)
	out := linkResult{name: name, era: eraOf(m), mode: m.mode}
	reg := analyzer.NewDefaultRegistry()
	res, err := reg.Analyze(path)
	if err != nil {
		out.skipped = "analyze: " + err.Error()
		return out
	}
	if out.mode == "" && res.DemoInfo != nil {
		out.mode = res.DemoInfo.Mode
	}
	gtDrops := res.Backpacks
	if len(gtDrops) == 0 {
		out.skipped = "no //ktx drop ground truth"
		return out
	}
	for _, g := range gtDrops {
		if g.Source != result.BackpackSourceKTX {
			out.skipped = "backpacks are reconstructed, not ground truth"
			return out
		}
	}
	// GT pickups, keyed by the (ent, dropTime) pair the frontend joins on —
	// backpack edicts are recycled, so entNum alone collides.
	type gtPick struct {
		player string
		time   int32
	}
	picks := map[[2]int32]gtPick{}
	for i := range res.WeaponPickups {
		wp := &res.WeaponPickups[i]
		if wp.Source != "backpack" {
			continue
		}
		picks[[2]int32{int32(wp.BackpackEnt), wp.DropTime}] = gtPick{wp.Player, wp.Time}
	}
	if len(picks) == 0 {
		out.skipped = "no //ktx bp ground truth"
		return out
	}
	if reason := analyzer.BackpackReconStandDown(res); reason != "" && reason != "hinting mod emitted no drops" {
		out.skipped = "stand-down: " + reason
		return out
	}

	rc := analyzer.ReconstructBackpackDrops(res)
	analyzer.LinkBackpackDrops(res, reg.Core.PackEntities, rc)

	usedGT := make([]bool, len(gtDrops))
	for ri := range rc {
		r := &rc[ri]
		best, bestDt := -1, int32(1<<30)
		for gi := range gtDrops {
			g := &gtDrops[gi]
			if usedGT[gi] || g.Player != r.Player || g.Weapon != r.Weapon {
				continue
			}
			if dt := abs32(g.Time - r.Time); dt <= matchTolMs && dt < bestDt {
				best, bestDt = gi, dt
			}
		}
		if best < 0 {
			continue
		}
		usedGT[best] = true
		out.pairedDrops++
		if r.EntNum != 0 {
			out.bound++
		}
		g := &gtDrops[best]
		gp, wasPicked := picks[[2]int32{int32(g.EntNum), g.Time}]
		if wasPicked {
			out.gtPicked++
		} else {
			out.gtNotPicked++
		}
		if r.EntNum != 0 {
			for pi := range reg.Core.PackEntities {
				pk := &reg.Core.PackEntities[pi]
				if pk.EntNum != r.EntNum || !pk.Ended {
					continue
				}
				if dt := pk.Start - r.Time; dt < -1000 || dt > 1000 {
					continue
				}
				if wasPicked {
					out.lifePicked = append(out.lifePicked, pk.End-pk.Start)
				} else {
					out.lifeNotPicked = append(out.lifeNotPicked, pk.End-pk.Start)
				}
				break
			}
		}
		switch r.Fate {
		case result.BackpackFatePicked:
			if !wasPicked {
				out.pickedWrong++
				break
			}
			out.pickedRight++
			out.timeErr = append(out.timeErr, abs32(r.PickupTime-gp.time))
			if r.Picker == "" {
				out.unattributed++
				break
			}
			out.attributed++
			if r.Picker == gp.player {
				out.attributedOK++
			} else {
				out.wrongNames = append(out.wrongNames, fmt.Sprintf("said %q, wire says %q", r.Picker, gp.player))
			}
		case result.BackpackFateExpired:
			if wasPicked {
				out.expiredWrong++
			} else {
				out.expiredRight++
			}
		default:
			if wasPicked {
				out.unobsGTPicked++
				why := analyzer.DiagnoseBackpackFate(res, reg.Core.PackEntities, *r, gp.player)
				if strings.HasPrefix(why, "no backpack entity") || strings.HasPrefix(why, "nearest backpack entity") {
					// The instant-regrab hypothesis: a pack taken inside the
					// demo frame it spawned in never reaches the wire at all.
					// Bucketed, not printed per row, so the class stays one line.
					why = fmt.Sprintf("no pack entity on the wire; the wire says it was taken %s after the drop", delayBucket(gp.time-g.Time))
				}
				out.unobsDiag = append(out.unobsDiag, why)
			} else {
				out.unobsGTNot++
			}
		}
	}
	return out
}

func countAtLeast(xs []int32, n int32) int {
	c := 0
	for _, x := range xs {
		if x >= n {
			c++
		}
	}
	return c
}

// delayBucket collapses a drop-to-pickup delay into a readable class.
func delayBucket(ms int32) string {
	switch {
	case ms <= 40:
		return "<=1 demo frame"
	case ms <= 100:
		return "<=100ms"
	case ms <= 500:
		return "<=500ms"
	case ms <= 2000:
		return "<=2s"
	}
	return ">2s"
}

func reportLinkage(rows []linkResult) {
	var scored, skipped int
	tot := linkResult{}
	skipReasons := map[string]int{}
	perEra := map[string]*linkResult{}
	perMode := map[string]*linkResult{}
	wrongNames := map[string]int{}
	unobsDiag := map[string]int{}

	add := func(dst *linkResult, r *linkResult) {
		dst.demos++
		dst.pairedDrops += r.pairedDrops
		dst.bound += r.bound
		dst.gtPicked += r.gtPicked
		dst.gtNotPicked += r.gtNotPicked
		dst.pickedRight += r.pickedRight
		dst.pickedWrong += r.pickedWrong
		dst.expiredRight += r.expiredRight
		dst.expiredWrong += r.expiredWrong
		dst.unobsGTPicked += r.unobsGTPicked
		dst.unobsGTNot += r.unobsGTNot
		dst.attributed += r.attributed
		dst.attributedOK += r.attributedOK
		dst.unattributed += r.unattributed
	}
	for i := range rows {
		r := &rows[i]
		if r.skipped != "" {
			skipped++
			skipReasons[classifySkip(r.skipped)]++
			continue
		}
		scored++
		add(&tot, r)
		tot.timeErr = append(tot.timeErr, r.timeErr...)
		tot.lifeNotPicked = append(tot.lifeNotPicked, r.lifeNotPicked...)
		tot.lifePicked = append(tot.lifePicked, r.lifePicked...)
		for _, n := range r.wrongNames {
			wrongNames[n]++
		}
		for _, n := range r.unobsDiag {
			unobsDiag[n]++
		}
		g := perEra[r.era]
		if g == nil {
			g = &linkResult{}
			perEra[r.era] = g
		}
		add(g, r)
		k := modeKey(r.mode)
		gm := perMode[k]
		if gm == nil {
			gm = &linkResult{}
			perMode[k] = gm
		}
		add(gm, r)
	}

	fmt.Printf("demos: %d scored, %d skipped\n", scored, skipped)
	if len(skipReasons) > 0 {
		fmt.Println("\nskip reasons:")
		for _, k := range sortedKeys(skipReasons) {
			fmt.Printf("  %-46s %d\n", k, skipReasons[k])
		}
	}
	fmt.Printf("\nscored drops (reconstructed AND matched to a hint): %d, of which bound to a pack entity: %d (%.2f%%)\n",
		tot.pairedDrops, tot.bound, pct(tot.bound, tot.pairedDrops))
	fmt.Printf("ground truth: %d picked, %d never picked\n", tot.gtPicked, tot.gtNotPicked)

	saidPicked := tot.pickedRight + tot.pickedWrong
	saidExpired := tot.expiredRight + tot.expiredWrong
	fmt.Println("\nclassification:")
	fmt.Printf("  picked   : said %d, right %d  → precision %.2f%%  recall %.2f%%\n",
		saidPicked, tot.pickedRight, pct(tot.pickedRight, saidPicked), pct(tot.pickedRight, tot.gtPicked))
	fmt.Printf("  expired  : said %d, right %d  → precision %.2f%%  recall %.2f%%\n",
		saidExpired, tot.expiredRight, pct(tot.expiredRight, saidExpired), pct(tot.expiredRight, tot.gtNotPicked))
	fmt.Printf("  unobserved: %d (%.2f%%) — %d were picked, %d were not\n",
		tot.unobsGTPicked+tot.unobsGTNot, pct(tot.unobsGTPicked+tot.unobsGTNot, tot.pairedDrops), tot.unobsGTPicked, tot.unobsGTNot)

	fmt.Println("\nattribution (on correctly-classified pickups):")
	fmt.Printf("  named a picker: %d (%.2f%% of them), correct %d → %.2f%%\n",
		tot.attributed, pct(tot.attributed, tot.pickedRight), tot.attributedOK, pct(tot.attributedOK, tot.attributed))
	fmt.Printf("  named nobody  : %d (%.2f%%)\n", tot.unattributed, pct(tot.unattributed, tot.pickedRight))
	fmt.Println("pickup time error (ms):")
	printQuantilesI(tot.timeErr)

	// The expiry threshold's evidence. KTX arms SUB_Remove for creation +
	// 120 000 ms and nothing re-arms it, so the never-picked tail is where
	// that lands once both ends are quantised to demo frames — and the
	// picked side shows how close a real pickup ever gets to it.
	fmt.Println("\nbound pack lifetime (ms), GT never picked:")
	printQuantilesI(tot.lifeNotPicked)
	fmt.Printf("  at/over 118000: %d   at/over 119900: %d   of %d\n",
		countAtLeast(tot.lifeNotPicked, 118000), countAtLeast(tot.lifeNotPicked, 119900), len(tot.lifeNotPicked))
	fmt.Println("bound pack lifetime (ms), GT picked:")
	printQuantilesI(tot.lifePicked)
	fmt.Printf("  at/over 118000: %d   at/over 119900: %d   of %d\n",
		countAtLeast(tot.lifePicked, 118000), countAtLeast(tot.lifePicked, 119900), len(tot.lifePicked))

	if len(wrongNames) > 0 {
		fmt.Println("\nwrong attributions:")
		for _, k := range sortedKeys(wrongNames) {
			fmt.Printf("  %-70s %d\n", k, wrongNames[k])
		}
	}
	if len(unobsDiag) > 0 {
		fmt.Println("\nunobserved but picked, by cause:")
		for _, k := range sortedKeys(unobsDiag) {
			fmt.Printf("  %-72s %d\n", k, unobsDiag[k])
		}
	}
	printLinkGroup("per era", perEra)
	printLinkGroup("per mode", perMode)
}

func printLinkGroup(title string, m map[string]*linkResult) {
	if len(m) == 0 {
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("\n%s:\n", title)
	fmt.Printf("  %-24s %6s %8s %10s %10s %12s\n", "", "demos", "drops", "pickPrec", "pickRec", "attrCorrect")
	for _, k := range keys {
		g := m[k]
		saidPicked := g.pickedRight + g.pickedWrong
		fmt.Printf("  %-24s %6d %8d %9.2f%% %9.2f%% %11.2f%%\n", k, g.demos, g.pairedDrops,
			pct(g.pickedRight, saidPicked), pct(g.pickedRight, g.gtPicked), pct(g.attributedOK, g.attributed))
	}
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// runLinkage fans the linkage scoring out over the demo list. Kept beside
// the scorer rather than in main's worker loop because the two modes score
// different row types.
func runLinkage(dir string, names []string, meta map[string]demoMeta, jobs int) {
	rows := make([]linkResult, len(names))
	var wg sync.WaitGroup
	sem := make(chan struct{}, jobs)
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			defer func() {
				if r := recover(); r != nil {
					rows[i] = linkResult{name: name, skipped: fmt.Sprintf("panic: %v", r)}
				}
			}()
			rows[i] = scoreLinkage(filepath.Join(dir, name), meta[name])
		}(i, name)
	}
	wg.Wait()
	reportLinkage(rows)
}
