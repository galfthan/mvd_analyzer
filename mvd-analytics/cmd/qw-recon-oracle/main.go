// qw-recon-oracle measures damage-reconstruction quality on demos that
// carry NO KTX damage log — the pre-MVDSV-0.30 archive (eras E0-E2), where
// cmd/qw-recon-eval has nothing to score against.
//
// It uses the one piece of wire ground truth those demos still carry: the
// OBITUARY. Every kill broadcasts a killer and a weapon on a channel the
// damage reconstruction can be told to ignore
// (damagerecon.Options.WithholdObituaries — delta extraction keeps its frag
// anchors, so the withheld run sees the same instants at the same
// magnitudes and only the attacker/weapon verdict changes). Scoring the
// withheld run's verdict at each kill instant against the obituary is a
// genuine, non-circular attribution measurement.
//
// What it reports per demo (counts, so eras aggregate exactly):
//
//   - obituary oracle: kill instants covered by a reconstructed delta, and
//     the withheld run's attacker / weapon / class accuracy on them;
//   - the anchored control (the production run, which READS the obituary) —
//     it must sit at ~100% or the harness itself is broken;
//   - attribution coverage away from kills: how much bounded damage the
//     production run leaves unattributed ("unknown" weapon / no attacker);
//   - given-vs-taken bookkeeping and per-death damage census;
//   - and, on GT-instrumented demos (-gt), the same oracle side by side
//     with the real KTX-log accuracy, which calibrates what the oracle
//     number means on the demos where only the oracle exists.
//
// Usage:
//
//	MVDA_BSP_DIR=./bsps go run ./mvd-analytics/cmd/qw-recon-oracle \
//	    -dir /data/mvd -list e0.txt -csv oracle-e0.csv
package main

import (
	"bufio"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/damagerecon"
	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// row is one demo's counts. Every field is a count or a sum so that an era
// aggregate is the plain column sum — no per-demo rates get averaged.
type row struct {
	file    string
	era     string
	version string
	ktxver  string
	mapname string
	mode    string
	players int
	duel    int
	source  string
	err     string

	// Obituary oracle (withheld run) on scored kills: non-suicide,
	// non-teamkill, non-positional obituaries naming a killer.
	kills       int // obituaries in the demo, all kinds
	killsScored int
	killsDelta  int // a reconstructed delta exists at the instant
	attOK       int // top-damage attacker == the named killer
	wepOK       int // its weapon == the obituary weapon (exact)
	wepFamOK    int // ... collapsing sg/ssg and ng/sng
	classOK     int // credited to another player at all (not self/env/team)
	killDmg     int // bounded damage at those instants
	attOKDmg    int
	// Anchored control: the same score for the production run, which reads
	// the obituary. Must be ~100%.
	attOKAnchored int

	// Production-run attribution coverage, bounded damage.
	dmgBounded    int
	dmgUnknown    int // weapon == "unknown"
	dmgEnv        int
	dmgSelf       int
	dmgTeam       int
	evTotal       int
	evUnknown     int
	dmgNonKill    int // bounded damage away from kill instants
	unkNonKill    int
	dmgKillInst   int
	unkKillInst   int
	givenAll      int // Given+GivenTeam+GivenSelf over players (bounded)
	takenNonEnv   int // Taken-TakenEnv over players (bounded)
	deaths        int
	deathsNoDmg   int // deaths whose instant produced no delta at all
	posSamples    int
	shots         int
	bloods        int
	explosions    int
	lgbloods      int
	weaponBitsLiv int

	// byWeapon: per obituary weapon, "<weapon>:<killsDelta>:<attOK>:<wepOK>"
	// joined by '|' — the era's attribution accuracy split by what killed,
	// which is where the sparse-telemetry eras are expected to differ
	// (shotguns lean on TE_BLOOD, rockets on entity flights).
	byWeapon map[string][3]int

	// Harness self-check: withholding the obituary must leave delta
	// EXTRACTION untouched, so the two runs must observe the same instants
	// at the same bounded magnitudes (positional kills excepted — a
	// withheld telefrag lands in Events instead of Telefrags).
	instBoth     int
	instOnlyProd int
	instOnlyWith int
	instDmgDiff  int

	// GT columns (-gt): the KTX damage log is present, so the same demo
	// yields both the oracle number and the true number.
	gtPresent  int
	gtInstants int // unambiguous GT enemy instants
	gtCovered  int
	gtAttOK    int
	// Kill instants only, GT as truth.
	gtNonKill   int // GT enemy instants away from a kill
	gtNonKillOK int
	gtKillInst  int
	gtKillAttOK int // production run vs GT
	whKillAttOK int // withheld run vs GT
	// Oracle label noise: does GT itself agree with the obituary about who
	// landed the killing damage?
	obitVsGT   int
	obitVsGTOK int
}

func main() {
	dir := flag.String("dir", "", "directory holding the demos (required)")
	list := flag.String("list", "", "file with one demo filename (or path) per line; default = every demo in -dir")
	workers := flag.Int("workers", max(1, runtime.NumCPU()-1), "parallel analyzers")
	csvPath := flag.String("csv", "", "per-demo CSV output path")
	gt := flag.Bool("gt", false, "also score against the KTX damage log where present (era baseline / oracle calibration)")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "qw-recon-oracle: -dir is required")
		os.Exit(2)
	}

	paths, err := collectPaths(*dir, *list)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qw-recon-oracle:", err)
		os.Exit(1)
	}
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "qw-recon-oracle: no demos selected")
		os.Exit(1)
	}
	fmt.Printf("scoring %d demos with %d workers (gt=%v)\n", len(paths), *workers, *gt)

	jobs := make(chan string)
	out := make(chan row)
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				out <- scoreOne(p, *gt)
			}
		}()
	}
	go func() {
		for _, p := range paths {
			jobs <- p
		}
		close(jobs)
		wg.Wait()
		close(out)
	}()

	var rows []row
	done := 0
	for r := range out {
		rows = append(rows, r)
		done++
		if done%100 == 0 {
			fmt.Fprintf(os.Stderr, "  %d/%d\n", done, len(paths))
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].file < rows[j].file })
	if *csvPath != "" {
		if err := writeCSV(*csvPath, rows); err != nil {
			fmt.Fprintln(os.Stderr, "qw-recon-oracle: csv:", err)
			os.Exit(1)
		}
	}
	summarize(rows)
}

func scoreOne(path string, wantGT bool) (r row) {
	r.file = filepath.Base(path)
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
		si := res.Metadata.ServerInfo
		r.mapname, r.mode = si["map"], si["mode"]
		r.version, r.ktxver = si["*version"], si["ktxver"]
	}
	r.era = eraOf(r.version)
	if res.Streams == nil || len(res.Streams.Players) == 0 {
		r.source = "none"
		return r
	}
	r.players = len(res.Streams.Players)
	if m := strings.ToLower(r.mode); r.players == 2 || m == "1on1" || m == "duel" {
		r.duel = 1
	}
	for i := range res.Streams.Players {
		if pt := res.Streams.Players[i].Position; pt != nil {
			r.posSamples += len(pt.T)
		}
	}
	if res.Shots != nil {
		r.shots = len(res.Shots.Shots)
	}
	if pe := res.Streams.PointEffects; pe != nil {
		for _, ty := range pe.Type {
			switch int(ty) {
			case events.TeBlood:
				r.bloods++
			case events.TeExplosion:
				r.explosions++
			case events.TeLightningBlood:
				r.lgbloods++
			}
		}
	}
	if damagerecon.WeaponBitsLive(res.Streams.Players) {
		r.weaponBitsLiv = 1
	}

	var gtDmg *result.DamageResult
	if res.Damage != nil && res.Damage.Source == result.DamageSourceKTX {
		gtDmg = res.Damage
	}
	recon, err := damagerecon.Compute(res)
	if err != nil {
		r.source, r.err = "none", err.Error()
		return r
	}
	withheld, err := damagerecon.ComputeWithOptions(res, damagerecon.Options{WithholdObituaries: true})
	if err != nil {
		r.source, r.err = "none", err.Error()
		return r
	}
	r.source = "reconstructed"
	if gtDmg != nil {
		r.source = "ktx"
	}

	scoreCoverage(res, recon, &r)
	compareRuns(recon, withheld, positionalKills(res), &r)
	scoreObituaries(res, recon, withheld, gtDmg, &r, wantGT)
	if wantGT && gtDmg != nil {
		r.gtPresent = 1
		scoreGT(gtDmg, recon, killInstants(res), &r)
	}
	return r
}

// positionalKills is the set of instants a telefrag/stomp obituary claims —
// the one class the withheld run legitimately routes elsewhere.
func positionalKills(res *analyzer.Result) map[key]bool {
	out := map[key]bool{}
	if res.Frags == nil {
		return out
	}
	for i := range res.Frags.Frags {
		f := &res.Frags.Frags[i]
		if f.Weapon == "tele" || f.Weapon == "stomp" {
			out[key{f.Victim, f.Time}] = true
		}
	}
	return out
}

// compareRuns checks the withheld run against the production run at the
// instant level: same instants, same bounded magnitudes. Any drift here
// means the withholding leaked into delta extraction and the oracle's
// denominators are not the production ones.
func compareRuns(recon, withheld *result.DamageResult, posKill map[key]bool, r *row) {
	a, b := instants(recon), instants(withheld)
	for k, v := range a {
		if posKill[k] {
			continue
		}
		w, ok := b[k]
		if !ok {
			r.instOnlyProd++
			continue
		}
		r.instBoth++
		if w.bounded != v.bounded {
			r.instDmgDiff++
		}
	}
	for k := range b {
		if posKill[k] {
			continue
		}
		if _, ok := a[k]; !ok {
			r.instOnlyWith++
		}
	}
}

// killInstants is the set of (victim, time) the frag log marks as a death.
func killInstants(res *analyzer.Result) map[key]bool {
	out := map[key]bool{}
	if res.Frags == nil {
		return out
	}
	for i := range res.Frags.Frags {
		f := &res.Frags.Frags[i]
		out[key{f.Victim, f.Time}] = true
	}
	return out
}

// instant aggregates every reconstructed event at one (victim, time): the
// bounded damage per attacker, and the weapon/class of the largest share.
type instant struct {
	bounded    int
	topAtt     string
	topWep     string
	topDmg     int
	topIsOther bool // credited to another player (not env / self / team)
}

func boundedOf(e *result.DamageEntry) int {
	if e.Bounded != nil {
		return *e.Bounded
	}
	return e.Damage
}

type key struct {
	victim string
	t      int32
}

func instants(d *result.DamageResult) map[key]*instant {
	out := make(map[key]*instant)
	for i := range d.Events {
		e := &d.Events[i]
		k := key{e.Victim, e.Time}
		in, ok := out[k]
		if !ok {
			in = &instant{}
			out[k] = in
		}
		b := boundedOf(e)
		in.bounded += b
		if b > in.topDmg || in.topAtt == "" {
			in.topDmg, in.topAtt, in.topWep = b, e.Attacker, e.Weapon
			in.topIsOther = !e.IsEnv && !e.IsSelf && !e.IsTeam
		}
	}
	return out
}

// scoreCoverage measures the production run's attribution coverage: how
// much bounded damage carries a real weapon and a real attacker, split by
// kill instants vs everything else.
func scoreCoverage(res *analyzer.Result, recon *result.DamageResult, r *row) {
	killAt := killInstants(res)
	if res.Frags != nil {
		r.kills = len(res.Frags.Frags)
	}
	for i := range recon.Events {
		e := &recon.Events[i]
		b := boundedOf(e)
		r.evTotal++
		r.dmgBounded += b
		switch {
		case e.IsEnv:
			r.dmgEnv += b
		case e.IsSelf:
			r.dmgSelf += b
		case e.IsTeam:
			r.dmgTeam += b
		}
		unknown := e.Weapon == "unknown"
		if unknown {
			r.evUnknown++
			r.dmgUnknown += b
		}
		if killAt[key{e.Victim, e.Time}] {
			r.dmgKillInst += b
			if unknown {
				r.unkKillInst += b
			}
		} else {
			r.dmgNonKill += b
			if unknown {
				r.unkNonKill += b
			}
		}
	}
	for _, p := range recon.ByPlayer {
		b := p.Bounded
		if b == nil {
			continue
		}
		r.givenAll += b.Given + b.GivenTeam + b.GivenSelf
		r.takenNonEnv += b.Taken - b.TakenEnv
	}
}

// scoreObituaries is the oracle: for every kill the frag log names, compare
// the withheld run's evidence-only verdict against the obituary.
func scoreObituaries(res *analyzer.Result, recon, withheld *result.DamageResult, gtDmg *result.DamageResult, r *row, wantGT bool) {
	if res.Frags == nil {
		return
	}
	names := map[string]bool{}
	for i := range res.Streams.Players {
		names[res.Streams.Players[i].Name] = true
	}
	whI := instants(withheld)
	rcI := instants(recon)
	var gtI map[key]*instant
	if wantGT && gtDmg != nil {
		gtI = instants(gtDmg)
	}
	for i := range res.Frags.Frags {
		f := &res.Frags.Frags[i]
		if f.IsSuicide || f.IsTeamKill {
			continue
		}
		if f.Killer == "" || f.Killer == "world" || f.Killer == f.Victim {
			continue
		}
		if f.Weapon == "tele" || f.Weapon == "stomp" {
			continue // positional kills carry no damage arithmetic
		}
		if !names[f.Victim] || !names[f.Killer] {
			continue
		}
		r.killsScored++
		k := key{f.Victim, f.Time}
		if r.byWeapon == nil {
			r.byWeapon = map[string][3]int{}
		}
		w := whI[k]
		if w == nil {
			r.deathsNoDmg++
			continue
		}
		r.killsDelta++
		r.killDmg += w.bounded
		wc := r.byWeapon[f.Weapon]
		wc[0]++
		if w.topAtt == f.Killer {
			wc[1]++
		}
		if w.topWep == f.Weapon {
			wc[2]++
		}
		r.byWeapon[f.Weapon] = wc
		if w.topAtt == f.Killer {
			r.attOK++
			r.attOKDmg += w.bounded
		}
		if w.topIsOther {
			r.classOK++
		}
		if w.topWep == f.Weapon {
			r.wepOK++
		}
		if wepFamily(w.topWep) == wepFamily(f.Weapon) {
			r.wepFamOK++
		}
		if c := rcI[k]; c != nil && c.topAtt == f.Killer {
			r.attOKAnchored++
		}
		if gtI != nil {
			if g := gtI[k]; g != nil {
				r.gtKillInst++
				r.obitVsGT++
				if g.topAtt == f.Killer {
					r.obitVsGTOK++
				}
				if c := rcI[k]; c != nil && c.topAtt == g.topAtt {
					r.gtKillAttOK++
				}
				if w.topAtt == g.topAtt {
					r.whKillAttOK++
				}
			}
		}
	}
	r.deaths = r.killsScored
}

// scoreGT is qw-recon-eval's event-level accuracy, recomputed here so the
// GT baseline and the oracle come off the same run over the same demos.
func scoreGT(gt, rc *result.DamageResult, killAt map[key]bool, r *row) {
	type agg struct {
		attacker string
		enemy    bool
		multi    bool
	}
	gtA := map[key]*agg{}
	for i := range gt.Events {
		e := &gt.Events[i]
		k := key{e.Victim, e.Time}
		a, ok := gtA[k]
		if !ok {
			gtA[k] = &agg{attacker: e.Attacker, enemy: !e.IsEnv && !e.IsSelf && !e.IsTeam}
			continue
		}
		if a.attacker != e.Attacker {
			a.multi = true
		}
	}
	rcI := instants(rc)
	for k, g := range gtA {
		if !g.enemy || g.multi {
			continue
		}
		r.gtInstants++
		c := rcI[k]
		if c == nil {
			continue
		}
		r.gtCovered++
		ok := c.topAtt == g.attacker
		if ok {
			r.gtAttOK++
		}
		if !killAt[k] {
			r.gtNonKill++
			if ok {
				r.gtNonKillOK++
			}
		}
	}
}

// wepFamily collapses the weapon pairs an obituary cannot always separate
// from the damage token (both shotguns share a blood signature; the KTX
// nailgun obituaries do not distinguish ng from sng).
func wepFamily(w string) string {
	switch w {
	case "ssg":
		return "sg"
	case "sng":
		return "ng"
	}
	return w
}

// eraOf buckets a demo by its serverinfo *version, using the archive data
// survey's boundaries (.reports/qw-archive-data-survey-2026-08-16).
func eraOf(version string) string {
	v := strings.ToLower(version)
	if !strings.HasPrefix(v, "mvdsv ") {
		return "E0" // qwsv / KTPro / ZQuake / ezQuake generation
	}
	rest := v[len("mvdsv "):]
	var maj, min int
	if n, _ := fmt.Sscanf(rest, "%d.%d", &maj, &min); n != 2 {
		return "E0"
	}
	switch n := maj*100 + min; {
	case n < 25:
		return "E1"
	case n < 30:
		return "E2"
	case n < 34:
		return "E3"
	case n < 100:
		return "E4"
	}
	return "E5"
}

func collectPaths(dir, list string) ([]string, error) {
	if list != "" {
		f, err := os.Open(list)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		var out []string
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			n := strings.TrimSpace(sc.Text())
			if n == "" || strings.HasPrefix(n, "#") {
				continue
			}
			if !filepath.IsAbs(n) && !strings.ContainsRune(n, filepath.Separator) {
				n = filepath.Join(dir, n)
			}
			out = append(out, n)
		}
		return out, sc.Err()
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if n := e.Name(); strings.HasSuffix(n, ".mvd") || strings.HasSuffix(n, ".mvd.gz") {
			out = append(out, filepath.Join(dir, n))
		}
	}
	sort.Strings(out)
	return out, nil
}

var csvHeader = []string{
	"file", "era", "version", "ktxver", "map", "mode", "players", "duel", "source", "err",
	"kills", "killsScored", "killsDelta", "attOK", "wepOK", "wepFamOK", "classOK",
	"killDmg", "attOKDmg", "attOKAnchored",
	"dmgBounded", "dmgUnknown", "dmgEnv", "dmgSelf", "dmgTeam", "evTotal", "evUnknown",
	"dmgNonKill", "unkNonKill", "dmgKillInst", "unkKillInst",
	"givenAll", "takenNonEnv", "deaths", "deathsNoDmg",
	"instBoth", "instOnlyProd", "instOnlyWith", "instDmgDiff", "byWeapon",
	"posSamples", "shots", "bloods", "explosions", "lgbloods", "weaponBitsLive",
	"gtPresent", "gtInstants", "gtCovered", "gtAttOK",
	"gtNonKill", "gtNonKillOK", "gtKillInst", "gtKillAttOK", "whKillAttOK", "obitVsGT", "obitVsGTOK",
}

func (r row) fields() []string {
	i := func(v int) string { return fmt.Sprint(v) }
	return []string{
		r.file, r.era, r.version, r.ktxver, r.mapname, r.mode, i(r.players), i(r.duel), r.source, r.err,
		i(r.kills), i(r.killsScored), i(r.killsDelta), i(r.attOK), i(r.wepOK), i(r.wepFamOK), i(r.classOK),
		i(r.killDmg), i(r.attOKDmg), i(r.attOKAnchored),
		i(r.dmgBounded), i(r.dmgUnknown), i(r.dmgEnv), i(r.dmgSelf), i(r.dmgTeam), i(r.evTotal), i(r.evUnknown),
		i(r.dmgNonKill), i(r.unkNonKill), i(r.dmgKillInst), i(r.unkKillInst),
		i(r.givenAll), i(r.takenNonEnv), i(r.deaths), i(r.deathsNoDmg),
		i(r.instBoth), i(r.instOnlyProd), i(r.instOnlyWith), i(r.instDmgDiff), r.weaponColumn(),
		i(r.posSamples), i(r.shots), i(r.bloods), i(r.explosions), i(r.lgbloods), i(r.weaponBitsLiv),
		i(r.gtPresent), i(r.gtInstants), i(r.gtCovered), i(r.gtAttOK),
		i(r.gtNonKill), i(r.gtNonKillOK), i(r.gtKillInst), i(r.gtKillAttOK), i(r.whKillAttOK), i(r.obitVsGT), i(r.obitVsGTOK),
	}
}

// weaponColumn serialises byWeapon as "<weapon>:<kills>:<attackerOK>:<weaponOK>"
// entries joined by '|', so one CSV column carries the whole breakdown.
func (r row) weaponColumn() string {
	if len(r.byWeapon) == 0 {
		return ""
	}
	ws := make([]string, 0, len(r.byWeapon))
	for w := range r.byWeapon {
		ws = append(ws, w)
	}
	sort.Strings(ws)
	parts := make([]string, 0, len(ws))
	for _, w := range ws {
		c := r.byWeapon[w]
		parts = append(parts, fmt.Sprintf("%s:%d:%d:%d", w, c[0], c[1], c[2]))
	}
	return strings.Join(parts, "|")
}

func writeCSV(path string, rows []row) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write(csvHeader); err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write(r.fields()); err != nil {
			return err
		}
	}
	return nil
}

func summarize(rows []row) {
	byEra := map[string][]row{}
	for _, r := range rows {
		byEra[r.era] = append(byEra[r.era], r)
	}
	eras := make([]string, 0, len(byEra))
	for e := range byEra {
		eras = append(eras, e)
	}
	sort.Strings(eras)

	oracleTable(byEra, eras, "all", func(r row) bool { return true })
	oracleTable(byEra, eras, "duels", func(r row) bool { return r.duel == 1 })
	oracleTable(byEra, eras, "team games", func(r row) bool { return r.duel == 0 })

	fmt.Printf("\n== attribution coverage (production run, bounded damage)\n")
	fmt.Printf("   %-4s %10s %9s %10s %10s %8s %8s %9s\n",
		"era", "dmg", "unknown%", "nonkill-u%", "kill-u%", "env%", "self%", "given=taken")
	for _, e := range eras {
		var s row
		for _, r := range byEra[e] {
			if r.err != "" {
				continue
			}
			sum(&s, r)
		}
		fmt.Printf("   %-4s %10d %8.2f%% %9.2f%% %9.2f%% %7.1f%% %7.1f%% %8s\n",
			e, s.dmgBounded, pct(s.dmgUnknown, s.dmgBounded),
			pct(s.unkNonKill, s.dmgNonKill), pct(s.unkKillInst, s.dmgKillInst),
			pct(s.dmgEnv, s.dmgBounded), pct(s.dmgSelf, s.dmgBounded),
			fmt.Sprintf("%d/%d", s.givenAll, s.takenNonEnv))
	}

	fmt.Printf("\n== telemetry + census per demo (medians)\n")
	fmt.Printf("   %-4s %6s %9s %9s %9s %9s %10s %9s\n",
		"era", "demos", "blood/sh", "expl/sh", "dmg/kill", "evts", "posSampl", "bitsLive%")
	for _, e := range eras {
		rs := byEra[e]
		var s row
		n, live := 0, 0
		var dmgPerKill []float64
		var evts, pos []float64
		var bl, ex []float64
		for _, r := range rs {
			if r.err != "" {
				continue
			}
			n++
			sum(&s, r)
			live += r.weaponBitsLiv
			if r.killsScored > 0 {
				dmgPerKill = append(dmgPerKill, float64(r.dmgBounded)/float64(r.killsScored))
			}
			if r.shots > 0 {
				bl = append(bl, float64(r.bloods)/float64(r.shots))
				ex = append(ex, float64(r.explosions)/float64(r.shots))
			}
			evts = append(evts, float64(r.evTotal))
			pos = append(pos, float64(r.posSamples))
		}
		fmt.Printf("   %-4s %6d %9.3f %9.3f %9.1f %9.0f %10.0f %8.0f%%\n",
			e, n, med(bl), med(ex), med(dmgPerKill), med(evts), med(pos), pct(live, n))
	}

	fmt.Printf("\n== obituary oracle by weapon (kills with a delta / attacker-correct %%)\n")
	wepOrder := []string{"rl", "lg", "sg", "ssg", "gl", "ng", "sng", "axe"}
	fmt.Printf("   %-4s", "era")
	for _, w := range wepOrder {
		fmt.Printf(" %14s", w)
	}
	fmt.Println()
	for _, e := range eras {
		agg := map[string][3]int{}
		for _, r := range byEra[e] {
			for w, c := range r.byWeapon {
				a := agg[w]
				a[0], a[1], a[2] = a[0]+c[0], a[1]+c[1], a[2]+c[2]
				agg[w] = a
			}
		}
		fmt.Printf("   %-4s", e)
		for _, w := range wepOrder {
			c := agg[w]
			fmt.Printf(" %7d %5.1f%%", c[0], pct(c[1], c[0]))
		}
		fmt.Println()
	}

	var chk row
	for _, r := range rows {
		sum(&chk, r)
	}
	fmt.Printf("\nharness self-check (withheld vs production instants, positional kills excluded):\n")
	fmt.Printf("  shared %d, production-only %d, withheld-only %d, bounded mismatch %d\n",
		chk.instBoth, chk.instOnlyProd, chk.instOnlyWith, chk.instDmgDiff)

	gtRows := 0
	var g row
	for _, r := range rows {
		if r.gtPresent == 1 {
			gtRows++
			sum(&g, r)
		}
	}
	if gtRows > 0 {
		fmt.Printf("\n== GT calibration (%d instrumented demos)\n", gtRows)
		fmt.Printf("   all GT enemy instants: covered %.1f%%  attacker-correct %.1f%% (production); away from kills %.1f%%\n",
			pct(g.gtCovered, g.gtInstants), pct(g.gtAttOK, g.gtCovered), pct(g.gtNonKillOK, g.gtNonKill))
		fmt.Printf("   kill instants: production-vs-GT %.1f%%   withheld-vs-GT %.1f%%   obituary-vs-GT %.1f%% (oracle label noise)\n",
			pct(g.gtKillAttOK, g.gtKillInst), pct(g.whKillAttOK, g.gtKillInst), pct(g.obitVsGTOK, g.obitVsGT))
		fmt.Printf("   oracle metric on the same demos: %.1f%%  -> bias vs all-instant truth: %+.1f pp\n",
			pct(g.attOK, g.killsDelta), pct(g.attOK, g.killsDelta)-pct(g.gtAttOK, g.gtCovered))
	}

	nerr := 0
	errs := map[string]int{}
	for _, r := range rows {
		if r.err != "" {
			nerr++
			m := r.err
			if len(m) > 70 {
				m = m[:70]
			}
			errs[m]++
		}
	}
	if nerr > 0 {
		fmt.Printf("\nskipped/failed: %d\n", nerr)
		type ec struct {
			m string
			n int
		}
		var l []ec
		for m, n := range errs {
			l = append(l, ec{m, n})
		}
		sort.Slice(l, func(i, j int) bool { return l[i].n > l[j].n })
		for i, e := range l {
			if i >= 10 {
				break
			}
			fmt.Printf("  %5d  %s\n", e.n, e.m)
		}
	}
}

// oracleTable prints the obituary-oracle scores per era over the demos the
// filter selects. Rates come in two flavours because they answer different
// questions: the pooled rate (every kill in the era weighted equally) and
// the per-demo median (every DEMO weighted equally, so one 500-kill 4on4
// cannot speak for the era).
func oracleTable(byEra map[string][]row, eras []string, label string, keep func(row) bool) {
	fmt.Printf("\n== obituary oracle, %s (withheld run vs the frag log)\n", label)
	fmt.Printf("   %-4s %6s %8s %9s %9s %9s %9s %9s %10s %11s\n",
		"era", "demos", "kills", "delta%", "attacker%", "att-dmg%", "weapon%", "wepfam%", "anchored%", "med-att%")
	for _, e := range eras {
		var s row
		n := 0
		var perDemo []float64
		for _, r := range byEra[e] {
			if r.err != "" || !keep(r) {
				continue
			}
			n++
			sum(&s, r)
			if r.killsDelta >= 20 {
				perDemo = append(perDemo, pct(r.attOK, r.killsDelta))
			}
		}
		if n == 0 {
			continue
		}
		fmt.Printf("   %-4s %6d %8d %8.1f%% %8.1f%% %8.1f%% %8.1f%% %8.1f%% %9.1f%% %10.1f%%\n",
			e, n, s.killsScored, pct(s.killsDelta, s.killsScored),
			pct(s.attOK, s.killsDelta), pct(s.attOKDmg, s.killDmg),
			pct(s.wepOK, s.killsDelta), pct(s.wepFamOK, s.killsDelta),
			pct(s.attOKAnchored, s.killsDelta), med(perDemo))
	}
}

func sum(s *row, r row) {
	s.kills += r.kills
	s.killsScored += r.killsScored
	s.killsDelta += r.killsDelta
	s.attOK += r.attOK
	s.wepOK += r.wepOK
	s.wepFamOK += r.wepFamOK
	s.classOK += r.classOK
	s.killDmg += r.killDmg
	s.attOKDmg += r.attOKDmg
	s.attOKAnchored += r.attOKAnchored
	s.dmgBounded += r.dmgBounded
	s.dmgUnknown += r.dmgUnknown
	s.dmgEnv += r.dmgEnv
	s.dmgSelf += r.dmgSelf
	s.dmgTeam += r.dmgTeam
	s.evTotal += r.evTotal
	s.evUnknown += r.evUnknown
	s.dmgNonKill += r.dmgNonKill
	s.unkNonKill += r.unkNonKill
	s.dmgKillInst += r.dmgKillInst
	s.unkKillInst += r.unkKillInst
	s.givenAll += r.givenAll
	s.takenNonEnv += r.takenNonEnv
	s.deaths += r.deaths
	s.deathsNoDmg += r.deathsNoDmg
	s.gtInstants += r.gtInstants
	s.gtCovered += r.gtCovered
	s.gtAttOK += r.gtAttOK
	s.instBoth += r.instBoth
	s.instOnlyProd += r.instOnlyProd
	s.instOnlyWith += r.instOnlyWith
	s.instDmgDiff += r.instDmgDiff
	s.gtNonKill += r.gtNonKill
	s.gtNonKillOK += r.gtNonKillOK
	s.gtKillInst += r.gtKillInst
	s.gtKillAttOK += r.gtKillAttOK
	s.whKillAttOK += r.whKillAttOK
	s.obitVsGT += r.obitVsGT
	s.obitVsGTOK += r.obitVsGTOK
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func med(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	sort.Float64s(v)
	return v[len(v)/2]
}
