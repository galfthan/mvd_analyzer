// qw-recon-eval scores the damage reconstruction (mvd-analytics/damagerecon)
// against KTX ground truth on modern demos that carry both the state streams
// and the real mvdhidden_dmgdone log.
//
// For every demo in -dir it runs the full pipeline, keeps the KTX-derived
// DamageResult as ground truth, re-runs damagerecon.Compute on the same
// Result (the compute package never reads res.Damage or any damage-derived
// field), and reports:
//
//   - per-player total relative error (bounded + raw; given / taken / ewep /
//     givenTeam / givenSelf) — the headline goal is ~1% here;
//   - per-event instant coverage: how many ground-truth damage instants the
//     delta extraction reproduced and how many values match exactly;
//   - attribution accuracy on matched enemy instants;
//   - direct/splash classification per rl/gl damage instant against the wire's
//     own splash flag, which is raised only inside T_RadiusDamage
//     (ktx/src/combat.c:1207-1227) and is therefore exact for rl. It is NOT
//     ground truth for gl — GrenadeTouch does all its damage through
//     GrenadeExplode → T_RadiusDamage, so every gl row is splash-flagged on
//     the wire whether or not the grenade touched anybody, and the gl line is
//     printed only to keep that asymmetry visible.
//
// Usage: go run ./mvd-analytics/cmd/qw-recon-eval [-dir mvd-analytics/testdata/cache] [-min-gt 200] [-worst 8]
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/damagerecon"
	"github.com/mvd-analyzer/mvd-analytics/result"
)

type playerRow struct {
	demo, mode, player string
	field              string
	family             string // "bounded" | "raw"
	gt, rc             int
}

func main() {
	dir := flag.String("dir", "mvd-analytics/testdata/cache", "directory of .mvd/.mvd.gz demos with KTX damage")
	minGT := flag.Int("min-gt", 200, "minimum ground-truth total for a player row to count toward relative-error stats")
	worst := flag.Int("worst", 8, "how many worst rows to print per metric")
	diag := flag.Bool("diag", false, "print the misattribution confusion breakdown (GT class -> recon class, bounded damage sums)")
	flowMin := flag.Int("flow-min", 100, "smallest bounded-damage flow to print in the -diag table (1 shows every flow)")
	flag.Parse()

	paths, err := demoPaths(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qw-recon-eval:", err)
		os.Exit(1)
	}
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "qw-recon-eval: no demos in", *dir)
		os.Exit(1)
	}

	var rows []playerRow
	var evCovered, evTotal, evExact, attMatched, attTotal int
	confusion := map[string]*confCell{}
	weaponStats := map[string]*wepCell{}
	splashStats := map[string]*splashCell{}
	for _, path := range paths {
		reg := analyzer.NewDefaultRegistry()
		reg.BuildShotStreams = true
		res, err := reg.Analyze(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: analyze: %v\n", filepath.Base(path), err)
			continue
		}
		if res.Damage == nil || len(res.Damage.ByPlayer) == 0 {
			fmt.Fprintf(os.Stderr, "%s: no KTX damage ground truth, skipped\n", filepath.Base(path))
			continue
		}
		gt := res.Damage
		rc, err := damagerecon.Compute(res)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: recon: %v\n", filepath.Base(path), err)
			continue
		}
		mode := ""
		if res.DemoInfo != nil {
			mode = res.DemoInfo.Mode
		}
		demo := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".gz"), ".mvd")
		rows = append(rows, playerRows(demo, mode, gt, rc)...)

		c, tot, ex, am, at := eventStats(gt, rc)
		evCovered += c
		evTotal += tot
		evExact += ex
		attMatched += am
		attTotal += at
		if *diag {
			collectConfusion(gt, rc, confusion)
		}
		collectWeaponStats(gt, rc, weaponStats)
		collectSplashStats(gt, rc, splashStats)
	}

	fmt.Printf("demos scored: %d\n", len(paths))
	if evTotal > 0 {
		fmt.Printf("\nevent level (GT enemy+self+team instants): covered %d/%d (%.1f%%)  value-exact %.1f%% of covered  attacker-correct %.1f%% of matched-enemy\n",
			evCovered, evTotal, pct(evCovered, evTotal), pct(evExact, evCovered), pct(attMatched, attTotal))
	}
	for _, family := range []string{"bounded", "raw"} {
		for _, field := range []string{"given", "taken", "ewep", "givenTeam", "givenSelf"} {
			printMetric(rows, family, field, *minGT, *worst)
		}
	}
	printWeaponStats(weaponStats)
	printSplashStats(splashStats)
	if *diag {
		printConfusion(confusion, *flowMin)
	}
}

// wepCell accumulates event-level accuracy for one GT attacker-weapon
// category (enemy instants with a single GT attacker).
type wepCell struct {
	instants, dmg      int
	covered, valExact  int
	attTotal, attRight int
	classRight         int
}

// collectWeaponStats scores GT ENEMY damage instants per attacker weapon:
// coverage (a same-instant recon delta exists), exact bounded value,
// attacker attribution, and class survival (still classified enemy).
func collectWeaponStats(gt, rc *result.DamageResult, out map[string]*wepCell) {
	type key struct {
		victim string
		t      int32
	}
	type inst struct {
		weapon   string
		attacker string
		bounded  int
		multi    bool
	}
	gtI := map[key]*inst{}
	for i := range gt.Events {
		e := &gt.Events[i]
		if e.IsEnv || e.IsSelf || e.IsTeam {
			continue
		}
		b := e.Damage
		if e.Bounded != nil {
			b = *e.Bounded
		}
		k := key{e.Victim, e.Time}
		if g, ok := gtI[k]; ok {
			g.bounded += b
			if g.attacker != e.Attacker || g.weapon != e.Weapon {
				g.multi = true
			}
		} else {
			gtI[k] = &inst{weapon: e.Weapon, attacker: e.Attacker, bounded: b}
		}
	}
	type rcInst struct {
		bounded  int
		attacker string
		enemy    bool
	}
	rcI := map[key]*rcInst{}
	for i := range rc.Events {
		e := &rc.Events[i]
		b := e.Damage
		if e.Bounded != nil {
			b = *e.Bounded
		}
		k := key{e.Victim, e.Time}
		r, ok := rcI[k]
		if !ok {
			r = &rcInst{attacker: e.Attacker, enemy: !e.IsEnv && !e.IsSelf && !e.IsTeam}
			rcI[k] = r
		}
		r.bounded += b
	}
	for k, g := range gtI {
		if g.multi {
			continue // merged multi-source instants scored in the confusion table instead
		}
		c, ok2 := out[g.weapon]
		if !ok2 {
			c = &wepCell{}
			out[g.weapon] = c
		}
		c.instants++
		c.dmg += g.bounded
		r, ok := rcI[k]
		if !ok {
			continue
		}
		c.covered++
		if r.bounded == g.bounded {
			c.valExact++
		}
		c.attTotal++
		if r.enemy {
			c.classRight++
		}
		if r.attacker == g.attacker {
			c.attRight++
		}
	}
}

func printWeaponStats(m map[string]*wepCell) {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]].dmg > m[keys[j]].dmg })
	fmt.Printf("\n== per-attacker-weapon event accuracy (GT single-source enemy instants)\n")
	fmt.Printf("   %-6s %8s %9s %9s %10s %11s %11s\n", "weapon", "instants", "gt-dmg", "covered", "val-exact", "attacker-ok", "class-enemy")
	for _, k := range keys {
		c := m[k]
		fmt.Printf("   %-6s %8d %9d %8.1f%% %9.1f%% %10.1f%% %10.1f%%\n",
			k, c.instants, c.dmg, pct(c.covered, c.instants),
			pct(c.valExact, c.covered), pct(c.attRight, c.attTotal), pct(c.classRight, c.attTotal))
	}
}

// splashCell accumulates the direct/splash confusion for one weapon over
// paired rl/gl damage instants: `direct` means the projectile TOUCHED the
// victim, which for rl is exactly what the wire's unflagged row says and what
// KTX's own hits counter increments on.
type splashCell struct {
	paired             int
	gtDirect, rcDirect int
	truePos, falsePos  int
	falseNeg, trueNeg  int
	missing            int // GT instant the reconstruction never produced
}

// collectSplashStats pairs each single-source rl/gl GT damage instant with the
// reconstruction's instant for the same victim at the same millisecond and
// scores the direct/splash verdict. Self rows are excluded on both sides: a
// missile never touches its own owner (T_MissileTouch / GrenadeTouch return on
// `other == owner`, ktx/src/weapons.c:951, :1315), so those are splash by
// construction and would only pad the agreement rate.
func collectSplashStats(gt, rc *result.DamageResult, out map[string]*splashCell) {
	type key struct {
		victim string
		t      int32
	}
	type inst struct {
		weapon string
		direct bool
		multi  bool
	}
	collect := func(evs []result.DamageEntry) map[key]*inst {
		m := map[key]*inst{}
		for i := range evs {
			e := &evs[i]
			if e.IsEnv || e.IsSelf || (e.Weapon != "rl" && e.Weapon != "gl") {
				continue
			}
			k := key{e.Victim, e.Time}
			if g, ok := m[k]; ok {
				if g.weapon != e.Weapon {
					g.multi = true
				}
				// One instant, several rows: the instant counts as a direct
				// touch if ANY of its rows is one.
				g.direct = g.direct || !e.IsSplash
				continue
			}
			m[k] = &inst{weapon: e.Weapon, direct: !e.IsSplash}
		}
		return m
	}
	gtI, rcI := collect(gt.Events), collect(rc.Events)
	for k, g := range gtI {
		if g.multi {
			continue
		}
		c, ok := out[g.weapon]
		if !ok {
			c = &splashCell{}
			out[g.weapon] = c
		}
		r, ok := rcI[k]
		if !ok || r.multi || r.weapon != g.weapon {
			c.missing++
			continue
		}
		c.paired++
		if g.direct {
			c.gtDirect++
		}
		if r.direct {
			c.rcDirect++
		}
		switch {
		case g.direct && r.direct:
			c.truePos++
		case !g.direct && r.direct:
			c.falsePos++
		case g.direct && !r.direct:
			c.falseNeg++
		default:
			c.trueNeg++
		}
	}
}

func printSplashStats(m map[string]*splashCell) {
	if len(m) == 0 {
		return
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("\n== direct/splash verdict vs the wire splash flag (paired rl/gl instants, non-self)\n")
	fmt.Printf("   the gl line is NOT ground truth: GrenadeTouch damages only through\n")
	fmt.Printf("   T_RadiusDamage, so every wire gl row is splash-flagged (ktx/src/weapons.c:1327)\n")
	fmt.Printf("   %-6s %8s %9s %10s %8s %8s %10s %9s %9s\n",
		"weapon", "paired", "unpaired", "gt-direct", "recon", "correct", "precision", "recall", "count-err")
	for _, k := range keys {
		c := m[k]
		countErr := 0.0
		if c.gtDirect > 0 {
			countErr = 100 * float64(c.rcDirect-c.gtDirect) / float64(c.gtDirect)
		}
		fmt.Printf("   %-6s %8d %9d %10d %8d %7.1f%% %9.1f%% %8.1f%% %+8.1f%%\n",
			k, c.paired, c.missing, c.gtDirect, c.rcDirect,
			pct(c.truePos+c.trueNeg, c.paired), pct(c.truePos, c.rcDirect),
			pct(c.truePos, c.gtDirect), countErr)
	}
}

// confCell accumulates one GT-class → recon-class flow.
type confCell struct {
	instants int
	bounded  int
}

// classify buckets an instant by its relation for the attribution
// confusion: "enemy:<weapon>", "self", "team" or "env:<weapon>".
func classify(weapon string, isEnv, isSelf, isTeam bool) string {
	switch {
	case isEnv:
		return "env:" + weapon
	case isSelf:
		return "self"
	case isTeam:
		return "team"
	default:
		return "enemy:" + weapon
	}
}

// collectConfusion aggregates, per GT instant (victim+time), where the
// reconstruction routed its bounded damage: same class, a flipped class, or
// nowhere. Enemy instants further split correct-vs-wrong attacker.
//
// Positional instant kills (telefrags/stomps) are folded in on BOTH sides as
// their own class. They live outside Events on either side — analyzer/damage.go
// and damagerecon/aggregate.go both surface them in Telefrags/Stomps — so
// comparing the Events logs alone reported every correctly-routed positional
// kill as a PHANTOM (recon emitted an instant the GT Events log has no row
// for) and every mis-routed one as a class the GT could not answer. That is
// how the team-telefrag channel hid inside "PHANTOM → team" until the
// 2026-08-26 classification pass: 100 of the 105 instants there were the same
// kill, booked as weapon damage by one side and as a telefrag by the other.
func collectConfusion(gt, rc *result.DamageResult, out map[string]*confCell) {
	type key struct {
		victim string
		t      int32
	}
	type inst struct {
		class    string
		attacker string
		bounded  int
	}
	fold := func(evs []result.DamageEntry, tele, stomp []result.PositionalKill) map[key]*inst {
		m := map[key]*inst{}
		add := func(k key, c, attacker string, b int) {
			if g, ok := m[k]; ok {
				g.bounded += b
				if g.class != c {
					g.class = "mixed"
				}
				return
			}
			m[k] = &inst{class: c, attacker: attacker, bounded: b}
		}
		for i := range evs {
			e := &evs[i]
			b := e.Damage
			if e.Bounded != nil {
				b = *e.Bounded
			}
			add(key{e.Victim, e.Time}, classify(e.Weapon, e.IsEnv, e.IsSelf, e.IsTeam), e.Attacker, b)
		}
		for _, lst := range [2][]result.PositionalKill{tele, stomp} {
			for i := range lst {
				p := &lst[i]
				b := p.Damage
				if p.Bounded != nil {
					b = *p.Bounded
				}
				c := "positional"
				switch {
				case p.IsTeam:
					c = "positional:team"
				case p.Attacker == p.Victim:
					c = "positional:self"
				}
				add(key{p.Victim, p.Time}, c, p.Attacker, b)
			}
		}
		return m
	}
	gtI := fold(gt.Events, gt.Telefrags, gt.Stomps)
	rcI := fold(rc.Events, rc.Telefrags, rc.Stomps)
	add := func(from, to string, bounded int) {
		cell, ok := out[from+" -> "+to]
		if !ok {
			cell = &confCell{}
			out[from+" -> "+to] = cell
		}
		cell.instants++
		cell.bounded += bounded
	}
	for k, g := range gtI {
		// The ground-truth denominator of the class, so a flow can be read as
		// a RATE. Without it the table invites the base-rate mistake: enemy
		// instants outnumber team ones ~20:1, so an equal per-instant error
		// rate still moves far more damage INTO the small class than out of
		// it, and the resulting asymmetry looks like a bias.
		add(g.class, "=TOTAL", g.bounded)
		r, ok := rcI[k]
		switch {
		case !ok:
			add(g.class, "MISSING", g.bounded)
		case r.class == g.class:
			if r.attacker == g.attacker {
				break // same class, right attacker: not interesting
			}
			if strings.HasPrefix(g.class, "enemy:") {
				add(g.class, "enemy:WRONG-ATTACKER", g.bounded)
			} else if strings.HasPrefix(g.class, "positional") {
				add(g.class, g.class+":WRONG-ATTACKER", g.bounded)
			}
		default:
			add(g.class, r.class, g.bounded)
		}
	}
	for k, r := range rcI {
		if _, ok := gtI[k]; !ok {
			add("PHANTOM", r.class, r.bounded)
		}
	}
}

func printConfusion(m map[string]*confCell, flowMin int) {
	type row struct {
		k string
		c *confCell
	}
	var rows []row
	for k, c := range m {
		rows = append(rows, row{k, c})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].c.bounded > rows[j].c.bounded })
	fmt.Printf("\n== ground-truth class totals (the flow denominators)\n")
	for _, r := range rows {
		if !strings.HasSuffix(r.k, " -> =TOTAL") {
			continue
		}
		fmt.Printf("   %7d dmg  %5d instants  %s\n", r.c.bounded, r.c.instants,
			strings.TrimSuffix(r.k, " -> =TOTAL"))
	}
	fmt.Printf("\n== misattribution flows (by bounded damage moved)\n")
	for _, r := range rows {
		if r.c.bounded < flowMin || strings.HasSuffix(r.k, " -> =TOTAL") {
			continue
		}
		fmt.Printf("   %7d dmg  %5d instants  %s\n", r.c.bounded, r.c.instants, r.k)
	}
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

// playerRows flattens both damage families' per-player totals into rows.
func playerRows(demo, mode string, gt, rc *result.DamageResult) []playerRow {
	var rows []playerRow
	names := make([]string, 0, len(gt.ByPlayer))
	for n := range gt.ByPlayer {
		names = append(names, n)
	}
	sort.Strings(names)
	get := func(m map[string]*result.PlayerDamage, n string) *result.PlayerDamage {
		if p := m[n]; p != nil {
			return p
		}
		return &result.PlayerDamage{}
	}
	bounded := func(p *result.PlayerDamage) *result.PlayerDamage {
		if p.Bounded != nil {
			return p.Bounded
		}
		return &result.PlayerDamage{}
	}
	for _, n := range names {
		g, r := get(gt.ByPlayer, n), get(rc.ByPlayer, n)
		gb, rb := bounded(g), bounded(r)
		for _, f := range []struct {
			field  string
			family string
			gt, rc int
		}{
			{"given", "bounded", gb.Given, rb.Given},
			{"taken", "bounded", gb.Taken, rb.Taken},
			{"ewep", "bounded", gb.EWep, rb.EWep},
			{"givenTeam", "bounded", gb.GivenTeam, rb.GivenTeam},
			{"givenSelf", "bounded", gb.GivenSelf, rb.GivenSelf},
			{"given", "raw", g.Given, r.Given},
			{"taken", "raw", g.Taken, r.Taken},
			{"ewep", "raw", g.EWep, r.EWep},
			{"givenTeam", "raw", g.GivenTeam, r.GivenTeam},
			{"givenSelf", "raw", g.GivenSelf, r.GivenSelf},
		} {
			rows = append(rows, playerRow{demo: demo, mode: mode, player: n,
				field: f.field, family: f.family, gt: f.gt, rc: f.rc})
		}
	}
	return rows
}

// eventStats matches GT damage instants (grouped per victim+time) against
// recon events: coverage, exact-value rate, and attacker accuracy on
// matched enemy instants.
func eventStats(gt, rc *result.DamageResult) (covered, total, exact, attMatched, attTotal int) {
	type key struct {
		victim string
		t      int32
	}
	type agg struct {
		bounded  int
		attacker string
		enemy    bool
		multi    bool
	}
	gtAgg := map[key]*agg{}
	for i := range gt.Events {
		e := &gt.Events[i]
		b := e.Damage
		if e.Bounded != nil {
			b = *e.Bounded
		}
		k := key{e.Victim, e.Time}
		a, ok := gtAgg[k]
		if !ok {
			a = &agg{attacker: e.Attacker, enemy: !e.IsEnv && !e.IsSelf && !e.IsTeam}
			gtAgg[k] = a
		} else {
			if a.attacker != e.Attacker {
				a.multi = true
			}
		}
		a.bounded += b
	}
	rcAgg := map[key]*agg{}
	for i := range rc.Events {
		e := &rc.Events[i]
		b := e.Damage
		if e.Bounded != nil {
			b = *e.Bounded
		}
		k := key{e.Victim, e.Time}
		a, ok := rcAgg[k]
		if !ok {
			a = &agg{attacker: e.Attacker}
			rcAgg[k] = a
		}
		a.bounded += b
	}
	for k, g := range gtAgg {
		total++
		r, ok := rcAgg[k]
		if !ok {
			continue
		}
		covered++
		if r.bounded == g.bounded {
			exact++
		}
		if g.enemy && !g.multi {
			attTotal++
			if r.attacker == g.attacker {
				attMatched++
			}
		}
	}
	return
}

func printMetric(rows []playerRow, family, field string, minGT, worst int) {
	type scored struct {
		err float64
		r   playerRow
	}
	var sel []scored
	for _, r := range rows {
		if r.family != family || r.field != field || r.gt < minGT {
			continue
		}
		sel = append(sel, scored{abs(r.rc-r.gt) / float64(r.gt), r})
	}
	if len(sel) == 0 {
		return
	}
	sort.Slice(sel, func(i, j int) bool { return sel[i].err < sel[j].err })
	n := len(sel)
	mean := 0.0
	w1, w2 := 0, 0
	for _, s := range sel {
		mean += s.err
		if s.err <= 0.01 {
			w1++
		}
		if s.err <= 0.02 {
			w2++
		}
	}
	mean /= float64(n)
	fmt.Printf("\n== %s %-9s n=%-3d median %6.2f%%  mean %6.2f%%  p90 %6.2f%%  <=1%% %5.1f%%  <=2%% %5.1f%%\n",
		family, field, n, 100*sel[n/2].err, 100*mean, 100*sel[min(n-1, n*9/10)].err,
		pct(w1, n), pct(w2, n))
	for i := n - 1; i >= 0 && i >= n-worst; i-- {
		s := sel[i]
		fmt.Printf("   %6.2f%%  %s %-4s %-20s gt=%d rc=%d\n",
			100*s.err, s.r.demo, s.r.mode, trunc(s.r.player, 20), s.r.gt, s.r.rc)
	}
}

func abs(v int) float64 {
	if v < 0 {
		return float64(-v)
	}
	return float64(v)
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
