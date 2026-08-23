// qw-demoinfo-eval scores the DERIVED per-player summary (result.PlayerStats,
// the section 54% of the archive has to answer with because it carries no KTX
// demoinfo block) against the verbatim block on demos that DO carry one.
//
// Withhold-and-compare, the qw-aim-eval protocol. Each demo is parsed once and
// must carry both a KTX demoinfo block (the ground truth) and the wire
// mvdhidden_dmgdone log. The block is never read by the pipeline families
// under test — the analyzer stores the fully DERIVED section and the KTX
// overlay is a read-time step (view.PlayerStats) this harness never calls — so
// the stored section is already the blind answer for score, pickups and hold.
// For damage and accuracy the wire log IS an input, so it is replaced by
// damagerecon's blind reconstruction, aim is recomputed against it, and
// analyzer.DerivedStatsForEval re-derives exactly those two families. The
// result is the section an old demo would publish, scored field by field
// against the server's own numbers.
//
// Usage:
//
//	MVDA_BSP_DIR=./bsps go run ./mvd-analytics/cmd/qw-demoinfo-eval \
//	    [-dir mvd-analytics/testdata/cache] [-workers 4] [-limit 0] [-csv out.csv]
//
// Run from the repo root: the reconstruction wants ./bsps.
//
// Definitional mismatches are reported, not hidden. Three fields are compared
// across measurements that are NOT the same quantity, and the table marks
// them:
//
//   - sg/ssg accuracy — KTX counts PELLETS on both sides of the ratio, this
//     pipeline counts trigger pulls and fires that connected;
//   - rl/gl hits — KTX's is the direct-impact count, ours counts a fire that
//     landed damage by any path including splash;
//   - spree.max — KTX increments a player's own streak on their SUICIDE
//     wherever teamplay is off (see result.PlayerStatsScore.MaxSpree). The
//     `ktxSpree` column replays KTX's gate instead of ours, so the residual
//     between the two conventions is measured rather than argued.
//
// Three DIAGNOSTIC columns sit beside those, each answering "could the
// pipeline adopt KTX's definition instead of naming the gap?". They are
// measurements, not candidates: what a convention costs is what decides
// whether it ships, so the measurement cannot be gated on it having shipped
// (same rule as aimcore.ReconHitsForEval).
//
//   - spree.max/ktxConvention — KTX's own gate, run on the frag log;
//   - acc.{rl,gl}.direct/wire — the direct-impact count read off the WIRE
//     damage log's splash flag, i.e. the control: what the server itself said;
//   - acc.{rl,gl}.direct/recon — the same count derived the only way an old
//     demo could, from the reconstruction's geometric direct/splash verdict.
//
// The verdicts are in damagerecon/ACCURACY.md; briefly, the wire answers rl
// exactly and gl not at all, and the reconstruction the other way round.
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"math"
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

// row is one (demo, player, field) comparison. Values are floats so the
// counters and the possession seconds share one table.
type row struct {
	demo, mode, player, field string
	gt, derived               float64
}

func (r row) err() float64 { return r.derived - r.gt }

func main() {
	dir := flag.String("dir", "mvd-analytics/testdata/cache", "directory of .mvd/.mvd.gz demos carrying a KTX demoinfo block")
	workers := flag.Int("workers", 4, "parallel demo workers")
	limit := flag.Int("limit", 0, "score at most this many demos (0 = all)")
	csvOut := flag.String("csv", "", "write the per-row comparison to this CSV")
	flag.Parse()

	paths, err := demoPaths(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qw-demoinfo-eval:", err)
		os.Exit(1)
	}
	if *limit > 0 && len(paths) > *limit {
		paths = paths[:*limit]
	}
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "qw-demoinfo-eval: no demos in", *dir)
		os.Exit(1)
	}

	var (
		mu      sync.Mutex
		rows    []row
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
				rs, err := scoreDemo(path)
				mu.Lock()
				if err != nil {
					skipped++
				} else {
					scored++
					rows = append(rows, rs...)
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
		return rows[i].field < rows[j].field
	})

	fmt.Printf("demos scored: %d (skipped %d of %d)\n", scored, skipped, len(paths))
	printFields(rows)
	if *csvOut != "" {
		if err := writeCSV(*csvOut, rows); err != nil {
			fmt.Fprintln(os.Stderr, "qw-demoinfo-eval: csv:", err)
		}
	}
}

// scoreDemo parses one demo, swaps its wire damage log for the blind
// reconstruction, re-derives the two families that ride it, and pairs every
// derived quantity with the KTX block's own.
func scoreDemo(path string) ([]row, error) {
	reg := analyzer.NewDefaultRegistry()
	reg.BuildShotStreams = true
	res, err := reg.Analyze(path)
	if err != nil {
		return nil, fmt.Errorf("analyze: %w", err)
	}
	if res.DemoInfo == nil || len(res.DemoInfo.Players) == 0 {
		return nil, fmt.Errorf("no KTX demoinfo ground truth")
	}
	if res.Damage == nil || res.Damage.Source != result.DamageSourceKTX {
		return nil, fmt.Errorf("no KTX damage stream to withhold")
	}
	if res.PlayerStats == nil {
		return nil, fmt.Errorf("no player-stats section")
	}

	// The KTX-convention spree replay needs the roster's teams, which the
	// stored section carries, and it must run before anything is swapped —
	// it reads the frag log only.
	ktxSpree := replayKTXSprees(res)
	// The rl/gl direct-impact counts, read off the WIRE log while it is still
	// installed. This is the definitional control for the reconstructed
	// derivation below: on the wire a non-splash rl row IS the direct touch
	// (dmg_is_splash is set only inside T_RadiusDamage, ktx/src/combat.c:1207),
	// so if the convention hypothesis is right this column must agree with the
	// block exactly.
	wireDirect := wireDirectHits(res)

	rc, err := damagerecon.Compute(res)
	if err != nil {
		return nil, fmt.Errorf("recon: %w", err)
	}
	res.Damage = rc
	res.Aim = aimcore.Compute(res, aimcore.Query{})
	reconDirect := aimcore.ReconDirectHitsForEval(res)
	blind := analyzer.DerivedStatsForEval(res)
	if blind == nil {
		return nil, fmt.Errorf("blind re-derivation returned nothing")
	}

	demo := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".gz"), ".mvd")
	mode := res.DemoInfo.Mode

	ktx := map[string]*result.DemoInfoPlayer{}
	for i := range res.DemoInfo.Players {
		p := &res.DemoInfo.Players[i]
		ktx[p.Name] = p
	}

	var out []row
	for i := range blind.Players {
		r := &blind.Players[i]
		di := ktx[r.Name]
		if di == nil {
			continue // a stream the block never listed; the phantom-roster canary owns that
		}
		add := func(field string, gt, derived float64) {
			out = append(out, row{demo: demo, mode: mode, player: r.Name, field: field, gt: gt, derived: derived})
		}
		if di.Stats != nil {
			add("frags", float64(di.Stats.Frags), float64(r.Score.Frags))
			add("deaths", float64(di.Stats.Deaths), float64(r.Score.Deaths))
			if r.Score.Kills != nil {
				add("kills", float64(di.Stats.Kills), float64(*r.Score.Kills))
				add("suicides", float64(di.Stats.Suicides), float64(*r.Score.Suicides))
				add("teamKills", float64(di.Stats.TK), float64(*r.Score.TeamKills))
			}
		}
		if di.Spree != nil && r.Score.MaxSpree != nil {
			add("spree.max", float64(di.Spree.Max), float64(*r.Score.MaxSpree))
			add("spree.max/ktxConvention", float64(di.Spree.Max), float64(ktxSpree[r.Name]))
			add("spree.quad", float64(di.Spree.Quad), float64(*r.Score.MaxQuadSpree))
		}
		if di.Dmg != nil && r.Damage != nil {
			add("dmg.given", float64(di.Dmg.Given), float64(r.Damage.Given))
			if r.Damage.TakenEnemy != nil {
				add("dmg.taken", float64(di.Dmg.Taken), float64(*r.Damage.TakenEnemy))
			}
		}
		// Powerup control: the count from the item timeline, the possession
		// seconds from the stat streams. KTX truncates its seconds to int
		// (ktx/src/stats_json.c json_item_detail), so ours is floored too
		// before differencing — otherwise every row would carry a spurious
		// sub-second error.
		for ktxKey, kind := range map[string]string{"q": "quad", "p": "pent", "r": "ring"} {
			item := di.Items[ktxKey]
			if item == nil {
				continue
			}
			if r.Pickups != nil {
				add(kind+".took", float64(item.Took), float64(r.Pickups.ByKind[kind].Took))
			}
			add(kind+".timeSec", float64(item.Time), math.Floor(float64(r.Hold.Powerups[kind].Ms)/1000))
		}
		if r.Accuracy == nil {
			continue
		}
		for w, acc := range r.Accuracy.ByWeapon {
			wv := di.Weapons[w]
			if wv == nil || wv.Acc == nil {
				continue
			}
			add("acc."+w+".attacks", float64(wv.Acc.Attacks), float64(acc.Attacks))
			if acc.Hits != nil {
				add("acc."+w+".hits", float64(wv.Acc.Hits), float64(*acc.Hits))
			}
			// The KTX-convention alternatives for the two weapons whose
			// any-path count is not the block's quantity. `direct/wire` is
			// the control (what the server itself flagged), `direct/recon`
			// the derivation an old demo could actually publish.
			if w != "rl" && w != "gl" {
				continue
			}
			// Always scored, zero included: a player who landed no direct
			// impact is a row where the two conventions agree at 0, and
			// dropping it would grade the derivation only where it fired.
			add("acc."+w+".direct/wire", float64(wv.Acc.Hits), float64(wireDirect[r.Name][w]))
			if d, ok := reconDirect[r.Name][w]; ok {
				add("acc."+w+".direct/recon", float64(wv.Acc.Hits), float64(d))
			}
		}
	}
	return out, nil
}

// ktxTPNum mirrors KTX's tp_num() (ktx/src/g_utils.c:1586): the teamplay cvar
// counts ONLY in team/CTF/coop modes, and is 0 everywhere else. That is the
// exact gate StatsHandler's spree increment reads (client.c:4865), so the
// diagnostic has to read the same thing.
//
// Every demo this harness scores carries a KTX demoinfo block, so the mode
// verdict is always available — no roster-shape fallback is reachable here.
// The earlier proxy (`len(teams) > 1`) misread two populations in opposite
// directions: a clan-tagged duel or an FFA whose players carry colour teams
// looked like teamplay, and a team game whose block named one team did not.
func ktxTPNum(res *analyzer.Result) int {
	switch strings.ToLower(res.DemoInfo.Mode) {
	case "team", "ctf", "coop":
	default:
		return 0
	}
	if res.Metadata == nil {
		return 0
	}
	if ms := res.Metadata.MatchSettings; ms != nil && ms.Teamplay > 0 {
		return ms.Teamplay
	}
	tp, _ := strconv.Atoi(res.Metadata.ServerInfo["teamplay"])
	return tp
}

// wireDirectHits counts, per player, the rl/gl damage rows the WIRE log marked
// as NON-splash — the server's own contact flag, since dmg_is_splash is raised
// only inside T_RadiusDamage's loop (ktx/src/combat.c:1207-1227) and the direct
// T_Damage in T_MissileTouch therefore arrives unflagged.
//
// KTX increments wpn[].hits in the touch handler itself (weapons.c:994, :1329),
// so for rl this count should BE the block's `hits`. For gl it should not: a
// grenade that touches a player detonates and does all its damage through
// T_RadiusDamage, so a direct gl touch leaves no non-splash row at all — the
// column measures how far that goes.
func wireDirectHits(res *analyzer.Result) map[string]map[string]int {
	out := map[string]map[string]int{}
	if res.Damage == nil {
		return out
	}
	for _, d := range res.Damage.Events {
		if d.Attacker == "" || d.IsEnv || d.IsSelf || d.IsSplash {
			continue
		}
		if d.Weapon != "rl" && d.Weapon != "gl" {
			continue
		}
		if out[d.Attacker] == nil {
			out[d.Attacker] = map[string]int{}
		}
		out[d.Attacker][d.Weapon]++
	}
	return out
}

// replayKTXSprees reproduces KTX's OWN spree_max gate — including the quirk
// this pipeline deliberately does not carry: `strneq(attackerteam, targteam)
// || !tp_num()` (ktx/src/client.c:4865), so on a teamplay-off server a
// player's suicide increments their streak in the very call that latches it.
//
// It runs off the frag log alone, in the log's order, incrementing the killer
// and then latching the victim exactly as ClientObituary does. It is a
// DIAGNOSTIC, not a second production convention: its whole job is to say how
// much of the derived spree's residual against KTX is the definition rather
// than the derivation.
func replayKTXSprees(res *analyzer.Result) map[string]int {
	out := map[string]int{}
	if res.Frags == nil {
		return out
	}
	teamOf := map[string]string{}
	for i := range res.PlayerStats.Players {
		p := &res.PlayerStats.Players[i]
		teamOf[p.Name] = p.Team
	}
	teamplay := ktxTPNum(res) > 0
	cur := map[string]int{}
	latch := func(name string) {
		if cur[name] > out[name] {
			out[name] = cur[name]
		}
		cur[name] = 0
	}
	for i := range res.Frags.Frags {
		f := &res.Frags.Frags[i]
		if f.Killer != "" {
			if !teamplay || teamOf[f.Killer] != teamOf[f.Victim] {
				cur[f.Killer]++
			}
		}
		latch(f.Victim)
	}
	for name := range cur {
		latch(name)
	}
	return out
}

// fieldRank keeps the report in a reading order rather than an alphabetical
// one: the scoreboard first, then the streaks, damage, powerup control and
// finally accuracy.
func fieldRank(f string) int {
	for i, p := range []string{"frags", "deaths", "kills", "suicides", "teamKills",
		"spree.", "dmg.", "quad.", "pent.", "ring.", "acc."} {
		if strings.HasPrefix(f, p) {
			return i
		}
	}
	return 99
}

func printFields(rows []row) {
	byField := map[string][]row{}
	var order []string
	for _, r := range rows {
		if _, ok := byField[r.field]; !ok {
			order = append(order, r.field)
		}
		byField[r.field] = append(byField[r.field], r)
	}
	sort.Slice(order, func(i, j int) bool {
		if a, b := fieldRank(order[i]), fieldRank(order[j]); a != b {
			return a < b
		}
		return order[i] < order[j]
	})

	fmt.Printf("\n== derived vs verbatim KTX demoinfo, per player row\n")
	fmt.Printf("   exact = rows agreeing to the unit; bias = mean signed (derived - ktx);\n")
	fmt.Printf("   relErr = |Σderived - Σktx| / Σktx over the whole population\n")
	fmt.Printf("   %-26s %7s %8s %10s %10s %10s %9s\n",
		"field", "rows", "exact", "bias", "med|err|", "p90|err|", "relErr")
	for _, f := range order {
		rs := byField[f]
		exact := 0
		var bias, sumGT, sumD float64
		errs := make([]float64, 0, len(rs))
		for _, r := range rs {
			if r.err() == 0 {
				exact++
			}
			bias += r.err()
			sumGT += r.gt
			sumD += r.derived
			errs = append(errs, math.Abs(r.err()))
		}
		sort.Float64s(errs)
		n := len(errs)
		rel := 0.0
		if sumGT != 0 {
			rel = math.Abs(sumD-sumGT) / sumGT
		}
		fmt.Printf("   %-26s %7d %7.1f%% %10.2f %10.1f %10.1f %8.2f%%\n",
			f, n, 100*float64(exact)/float64(n), bias/float64(n),
			errs[n/2], errs[minInt(n-1, n*9/10)], 100*rel)
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
	if err := w.Write([]string{"demo", "mode", "player", "field", "ktx", "derived"}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := w.Write([]string{
			r.demo, r.mode, r.player, r.field,
			strconv.FormatFloat(r.gt, 'f', -1, 64),
			strconv.FormatFloat(r.derived, 'f', -1, 64),
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

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
