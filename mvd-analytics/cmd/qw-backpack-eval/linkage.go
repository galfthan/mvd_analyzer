package main

// Linkage mode scores the PICKUP side of the reconstruction
// (analyzer.LinkBackpackDrops) against KTX's two remaining backpack ground
// truths:
//
//   - `//ktx bp <backpack_ent> <player_ent>`, emitted on every RL/LG pack
//     touch (ktx/src/items.c:2489-2494) and published as weaponPickups rows
//     with Source "backpack" — the `picked` class.
//   - `//ktx expire <ent>`, emitted when SUB_Remove takes an untaken RL/LG
//     pack off the map (ktx/src/g_spawn.c:196-210) and published as
//     `backpacks[].fate == "expired"` on the hint row — the `expired` class.
//
// The second matters because it is the only POSITIVE evidence of a
// non-pickup. Before it was decoded, `expired` recall was scored against
// "every drop with no `//ktx bp`", which silently folded in every pack that
// was still lying on the floor when the match ended — a class KTX never
// claimed had expired, and one the pass correctly refuses to call expired.
//
// The experiment is the drop eval's, one layer up: keep the wire's answer,
// re-run the reconstruction AND the linkage with every hint withheld, and
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

	// GT classes over the SCORED rows. A drop with no `//ktx bp` is not one
	// class but two: `expired`, where `//ktx expire` announced the removal,
	// and `neither`, where the wire said nothing about the pack at all.
	gtPicked, gtExpired, gtNeither int
	// confusion: our class × GT class
	pickedRight    int // we said picked, GT agrees
	pickedWrong    int // we said picked, GT says nobody took it
	expiredRight   int // we said expired, the wire announced the expiry
	expiredWrong   int // we said expired, GT says it was picked
	expiredNoHint  int // we said expired, GT says neither picked nor expired
	unobsGTPicked  int
	unobsGTExpired int
	unobsGTNeither int
	// Wire-level hint census over ALL of the demo's `//ktx drop` rows, not
	// just the ones the reconstruction reproduced — the population the
	// `drop == bp + expire + neither` invariant is a statement about.
	wireDrops, wirePicked, wireExpired, wireNeither, wireConflict int
	wireResidualDemos                                             int // demos with at least one `neither` row

	attributed   int // we said picked AND named a picker, GT agrees it was picked
	attributedOK int
	unattributed int // we said picked, GT agrees, but named nobody

	timeErr []int32
	// The bound pack's measured lifetime, split by what the wire says
	// happened to it. The `expired` side is the evidence the expiry threshold
	// is set from: KTX arms SUB_Remove for creation + 120 s, and this is what
	// that looks like after both ends are quantised to demo frames. Without it
	// the threshold is a guess. The `neither` side is what a pack the
	// recording simply ended on top of looks like, and is the class the old
	// "no `//ktx bp`" ground truth used to fold into the expiries.
	lifeExpired, lifeNeither, lifePicked []int32
	wrongNames                           []string
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

	// Wire census over the whole hint set, before any pairing: KTX makes one
	// `//ktx drop` per pack, and every pack that leaves the map should account
	// for itself in exactly one of `//ktx bp` (taken) or `//ktx expire`
	// (timed out). The residual is the pack the recording ends on top of.
	for gi := range gtDrops {
		g := &gtDrops[gi]
		_, picked := picks[[2]int32{int32(g.EntNum), g.Time}]
		expired := g.Fate == result.BackpackFateExpired
		out.wireDrops++
		switch {
		case picked && expired:
			out.wireConflict++
		case picked:
			out.wirePicked++
		case expired:
			out.wireExpired++
		default:
			out.wireNeither++
		}
	}
	if out.wireNeither > 0 {
		out.wireResidualDemos = 1
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
		// `//ktx expire` names this pack: the wire says SUB_Remove took it,
		// untaken. The hint path stamped it on the GT row itself; the
		// reconstruction and the linkage never saw it.
		wasExpired := !wasPicked && g.Fate == result.BackpackFateExpired
		switch {
		case wasPicked:
			out.gtPicked++
		case wasExpired:
			out.gtExpired++
		default:
			out.gtNeither++
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
				switch {
				case wasPicked:
					out.lifePicked = append(out.lifePicked, pk.End-pk.Start)
				case wasExpired:
					out.lifeExpired = append(out.lifeExpired, pk.End-pk.Start)
				default:
					out.lifeNeither = append(out.lifeNeither, pk.End-pk.Start)
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
			switch {
			case wasPicked:
				out.expiredWrong++
			case wasExpired:
				out.expiredRight++
			default:
				out.expiredNoHint++
			}
		default:
			switch {
			case wasPicked:
				out.unobsGTPicked++
				why := analyzer.DiagnoseBackpackFate(res, reg.Core.PackEntities, *r, gp.player)
				if strings.HasPrefix(why, "no backpack entity") || strings.HasPrefix(why, "nearest backpack entity") {
					// The instant-regrab hypothesis: a pack taken inside the
					// demo frame it spawned in never reaches the wire at all.
					// Bucketed, not printed per row, so the class stays one line.
					why = fmt.Sprintf("no pack entity on the wire; the wire says it was taken %s after the drop", delayBucket(gp.time-g.Time))
				}
				out.unobsDiag = append(out.unobsDiag, why)
			case wasExpired:
				out.unobsGTExpired++
			default:
				out.unobsGTNeither++
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
		dst.gtExpired += r.gtExpired
		dst.gtNeither += r.gtNeither
		dst.pickedRight += r.pickedRight
		dst.pickedWrong += r.pickedWrong
		dst.expiredRight += r.expiredRight
		dst.expiredWrong += r.expiredWrong
		dst.expiredNoHint += r.expiredNoHint
		dst.unobsGTPicked += r.unobsGTPicked
		dst.unobsGTExpired += r.unobsGTExpired
		dst.unobsGTNeither += r.unobsGTNeither
		dst.wireDrops += r.wireDrops
		dst.wirePicked += r.wirePicked
		dst.wireExpired += r.wireExpired
		dst.wireNeither += r.wireNeither
		dst.wireConflict += r.wireConflict
		dst.wireResidualDemos += r.wireResidualDemos
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
		tot.lifeExpired = append(tot.lifeExpired, r.lifeExpired...)
		tot.lifeNeither = append(tot.lifeNeither, r.lifeNeither...)
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
	// The wire's own account of every hinted pack, independent of what the
	// reconstruction reproduced. KTX emits one `//ktx drop` per pack and then
	// exactly one of `//ktx bp` / `//ktx expire` when it leaves the map, so
	// the residual measures how often a recording simply ends on top of a
	// pack — the class that used to be counted as a missed expiry.
	fmt.Printf("\nwire hint census over all %d `//ktx drop` rows: bp %d + expire %d + neither %d\n",
		tot.wireDrops, tot.wirePicked, tot.wireExpired, tot.wireNeither)
	fmt.Printf("  rows carrying BOTH a bp and an expire hint: %d\n", tot.wireConflict)
	fmt.Printf("  demos with a non-zero `neither` residual: %d of %d\n", tot.wireResidualDemos, scored)

	fmt.Printf("\nscored drops (reconstructed AND matched to a hint): %d, of which bound to a pack entity: %d (%.2f%%)\n",
		tot.pairedDrops, tot.bound, pct(tot.bound, tot.pairedDrops))
	fmt.Printf("ground truth: %d picked (`//ktx bp`), %d expired (`//ktx expire`), %d unclaimed by either hint\n",
		tot.gtPicked, tot.gtExpired, tot.gtNeither)

	saidPicked := tot.pickedRight + tot.pickedWrong
	saidExpired := tot.expiredRight + tot.expiredWrong + tot.expiredNoHint
	fmt.Println("\nclassification:")
	fmt.Printf("  picked   : said %d, right %d  → precision %.2f%%  recall %.2f%%\n",
		saidPicked, tot.pickedRight, pct(tot.pickedRight, saidPicked), pct(tot.pickedRight, tot.gtPicked))
	fmt.Printf("  expired  : said %d, right %d  → precision %.2f%%  recall %.2f%%\n",
		saidExpired, tot.expiredRight, pct(tot.expiredRight, saidExpired), pct(tot.expiredRight, tot.gtExpired))
	fmt.Printf("             of the %d we called expired: %d wire-confirmed, %d the wire says were PICKED, %d claimed by neither hint\n",
		saidExpired, tot.expiredRight, tot.expiredWrong, tot.expiredNoHint)
	fmt.Printf("  unobserved: %d (%.2f%%) — %d were picked, %d expired, %d claimed by neither hint\n",
		tot.unobsGTPicked+tot.unobsGTExpired+tot.unobsGTNeither,
		pct(tot.unobsGTPicked+tot.unobsGTExpired+tot.unobsGTNeither, tot.pairedDrops),
		tot.unobsGTPicked, tot.unobsGTExpired, tot.unobsGTNeither)

	fmt.Println("\nattribution (on correctly-classified pickups):")
	fmt.Printf("  named a picker: %d (%.2f%% of them), correct %d → %.2f%%\n",
		tot.attributed, pct(tot.attributed, tot.pickedRight), tot.attributedOK, pct(tot.attributedOK, tot.attributed))
	fmt.Printf("  named nobody  : %d (%.2f%%)\n", tot.unattributed, pct(tot.unattributed, tot.pickedRight))
	fmt.Println("pickup time error (ms):")
	printQuantilesI(tot.timeErr)

	// The expiry threshold's evidence. KTX arms SUB_Remove for creation +
	// 120 000 ms and nothing re-arms it, so the wire-confirmed expiries are
	// where that lands once both ends are quantised to demo frames — the
	// picked side shows how close a real pickup ever gets to it, and the
	// `neither` side shows that the packs no hint claims are nowhere near it.
	fmt.Println("\nbound pack lifetime (ms), GT expired (`//ktx expire`):")
	printQuantilesI(tot.lifeExpired)
	fmt.Printf("  at/over 118000: %d   at/over 119900: %d   of %d\n",
		countAtLeast(tot.lifeExpired, 118000), countAtLeast(tot.lifeExpired, 119900), len(tot.lifeExpired))
	fmt.Println("bound pack lifetime (ms), GT claimed by neither hint:")
	printQuantilesI(tot.lifeNeither)
	fmt.Printf("  at/over 118000: %d   at/over 119900: %d   of %d\n",
		countAtLeast(tot.lifeNeither, 118000), countAtLeast(tot.lifeNeither, 119900), len(tot.lifeNeither))
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
	fmt.Printf("  %-24s %6s %8s %10s %10s %12s %8s %9s\n", "", "demos", "drops", "pickPrec", "pickRec", "attrCorrect", "expN", "expRec")
	for _, k := range keys {
		g := m[k]
		saidPicked := g.pickedRight + g.pickedWrong
		fmt.Printf("  %-24s %6d %8d %9.2f%% %9.2f%% %11.2f%% %8d %8.2f%%\n", k, g.demos, g.pairedDrops,
			pct(g.pickedRight, saidPicked), pct(g.pickedRight, g.gtPicked), pct(g.attributedOK, g.attributed),
			g.gtExpired, pct(g.expiredRight, g.gtExpired))
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
