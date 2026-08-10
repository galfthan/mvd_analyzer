// qw-analyze is a command-line consumer of the mvd-analytics pipeline.
// It reads an MVD demo file and writes the analysis result in one of
// several formats — full JSON (the stable result-schema contract),
// markdown (a human-readable summary suitable as a seed for an AI
// review agent), line-delimited event JSON (the raw qwdemo event
// stream, useful for debugging and for driving alternative
// analytics), or one of the query-API views.
//
// At schema v7 the parse-time HighResBuckets field is gone; bucketed
// data is produced on demand by view.Buckets, accessible via the
// -view buckets flag below. Other views (events, stream-slice,
// state-at, trails, region-control, top-kills, top-windows, lives,
// items-summary, airgibs) are also available.
//
// The derived views (top-kills, top-windows, lives) take a -dmg damage
// family whose default is the VIEW default, raw. mvd-api substitutes
// bounded on the same queries, so an unset -dmg does not reproduce the
// REST response; pass -dmg bounded for that.
//
// On those views and items-summary/airgibs, a flag belonging to a
// different view is rejected rather than ignored — the CLI analogue of
// mvd-api's unknown-query-param rejection. The seven original views
// predate the check and keep their existing leniency.
//
// Example invocations:
//
//	qw-analyze demo.mvd.gz                              # full JSON to stdout
//	qw-analyze -include positions demo.mvd.gz           # full JSON with native x/y/z track
//	qw-analyze -include positions,view,height demo.mvd.gz # also view angles + floor height
//	qw-analyze -format md demo.mvd.gz > report.md       # markdown summary
//	qw-analyze -format events demo.mvd.gz | jq .        # event stream
//	qw-analyze -view buckets -bucket 1s demo.mvd.gz     # 1s buckets
//	qw-analyze -view events -event-types frag demo.mvd.gz
//	qw-analyze -view stream-slice -fields h,a -from 432.0 -to 442.0 demo.mvd.gz
//	qw-analyze -view state-at -time 432.5 demo.mvd.gz
//	qw-analyze -view trails -min-dwell 500ms demo.mvd.gz
//	qw-analyze -view region-control -bucket 1s demo.mvd.gz
//	qw-analyze -view top-kills -limit 10 demo.mvd.gz     # hardest kill bursts
//	qw-analyze -view top-windows -metric damageGiven -window 30s demo.mvd.gz
//	qw-analyze -view top-windows -mode gap -gap 8s demo.mvd.gz
//	qw-analyze -view lives -players alice -min-life 5s demo.mvd.gz
//	qw-analyze -view items-summary -kinds armor demo.mvd.gz
//	qw-analyze -view airgibs demo.mvd.gz
//	qw-analyze -graph mermaid                            # print the pipeline DAG (no demo)
//	qw-analyze -bulk demos/ -out-dir analyses/          # batch mode
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/config"
	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-analytics/view"
	mvdsource "github.com/mvd-analyzer/mvd-reader/source/mvd"
)

// viewOptions bundles every flag that's meaningful only for the
// non-full views. Parsed once in main().
type viewOptions struct {
	view        string
	bucketDur   time.Duration
	fields      []string
	reducers    map[string]string
	from, to    time.Duration
	players     []string
	eventTypes  []string
	minDwell    time.Duration
	timeAt      time.Duration
	timeSet     bool // -time was given (distinguishes an explicit -time 0 from "flag missing")
	includeTeam bool
	include     map[string]bool // -include positions etc. for -view full

	// Derived-view knobs. The view package speaks int32 ms; the CLI speaks
	// durations like every other time flag here, so these convert at the
	// dispatch boundary.
	limit     int
	perPlayer int
	minDamage int
	minScore  *int // nil = unset; 0 is a meaningful value, so this cannot be a plain int
	dmg       string
	metric    string
	mode      string
	weapons   []string
	items     []string
	kinds     []string
	window    time.Duration
	gap       time.Duration
	gapSet    bool // -gap was given: top-windows gap mode requires it, and 0 is not a default
	contested time.Duration
	minLife   time.Duration
}

// derivedViewFlags maps each view added on top of the original seven to the
// flags it consumes. A flag the user typed that is absent here is REJECTED
// rather than ignored, matching every mvd-api handler's writeUnknownParam:
// a knob that silently does nothing reads as one that worked. Only the new
// flags and the new views are policed — the original seven views predate
// this and are left as they were rather than breaking existing invocations.
var derivedViewFlags = map[string]map[string]bool{
	"top-kills":     {"limit": true, "gap": true, "contested": true, "min-damage": true, "weapons": true, "dmg": true, "players": true, "from": true, "to": true},
	"top-windows":   {"limit": true, "per-player": true, "metric": true, "mode": true, "window": true, "gap": true, "weapons": true, "dmg": true, "min-score": true, "players": true, "from": true, "to": true},
	"lives":         {"min-life": true, "dmg": true, "players": true, "from": true, "to": true},
	"items-summary": {"items": true, "kinds": true, "players": true, "from": true, "to": true},
	"airgibs":       {}, // view.Airgibs takes no options at all
}

// derivedOnlyFlags are the flags introduced for the views above. They are
// meaningless on the original seven, so typing one there is also rejected.
var derivedOnlyFlags = []string{
	"limit", "per-player", "min-damage", "min-score", "dmg", "metric",
	"mode", "weapons", "items", "kinds", "window", "gap", "contested", "min-life",
}

// viewScopedFlags is every flag that selects or shapes a view, as opposed to
// the global ones (-format, -pretty, -bulk, -out-dir, -regions) that apply
// whatever is being produced. The applicability check consults this list to
// decide whether an unrecognised-for-this-view flag is its business: the
// legacy eight belong to specific original views, so they are just as wrong
// on -view airgibs as -metric is, and get the same rejection.
var viewScopedFlags = append([]string{
	"players", "from", "to",
	"bucket", "fields", "reducer", "event-types", "min-dwell", "time",
	"include-team", "include",
}, derivedOnlyFlags...)

func main() {
	format := flag.String("format", "json", "output format: json | md | events")
	outDir := flag.String("out-dir", "", "bulk mode: write <demo>.<ext> into this directory")
	bulk := flag.Bool("bulk", false, "treat the input path as a directory and analyze every demo in it")
	indent := flag.Bool("pretty", false, "pretty-print JSON output (single-demo mode only); pipe to `jq .` for human reading")
	regionsPath := flag.String("regions", "", "path to a regions JSON ({\"regions\":[{\"name\":...,\"locs\":[...]}]}) to override the embedded per-map regions for the analyzed demo")

	viewName := flag.String("view", "full", "view: full | buckets | events | trails | stream-slice | state-at | region-control | top-kills | top-windows | lives | items-summary | airgibs")
	bucketStr := flag.String("bucket", "50ms", "bucket duration for -view buckets / region-control (e.g. 50ms, 1s, 10s)")
	fieldsStr := flag.String("fields", "", "comma-separated field codes (see mvd-analytics/view docs)")
	reducerArgs := stringListFlag("reducer", "field=name reducer override; repeatable (e.g. -reducer h=min)")
	fromStr := flag.String("from", "", "start time (match-relative; e.g. 30s, 1m30s)")
	toStr := flag.String("to", "", "end time")
	playersStr := flag.String("players", "", "comma-separated player names")
	eventTypesStr := flag.String("event-types", "", "comma-separated event types (frag, powerup, streak, spawn, death, weapon, item, chat, loc, health, armor); empty = default discrete set")
	minDwellStr := flag.String("min-dwell", "0", "drop transitions shorter than this for -view trails")
	timeStr := flag.String("time", "", "time for -view state-at (required)")
	includeTeam := flag.Bool("include-team", false, "emit per-team aggregates on -view buckets")
	includeStr := flag.String("include", "", "comma-separated extras for -view full: positions (x/y/z+loc), view (pitch/yaw), height, liquid, velocity; los (line-of-sight + pvs potential-visibility intervals, computed on request); projectiles, beams (spatial rocket/grenade-flight and LG-beam streams for the map); nails (ng/sng nail tracking — links ng/sng fires to damage + nail map stream; high volume)")
	limit := flag.Int("limit", 0, "max rows for -view top-kills / top-windows; 0 = the view's default (20 and 10), negative = uncapped")
	perPlayer := flag.Int("per-player", 0, "max windows from any one player for -view top-windows; <=0 = uncapped")
	minDamage := flag.Int("min-damage", 0, "drop bursts below this burst damage for -view top-kills")
	dmgStr := flag.String("dmg", "", "damage family for -view top-kills / top-windows / lives: raw | bounded; empty = the view default (raw). Note mvd-api defaults the same queries to bounded, so an unset -dmg does NOT reproduce the REST response")
	metric := flag.String("metric", "", "ranking metric for -view top-windows: frags | deaths | netFrags | damageGiven | damageTaken | netDamage | shots | hits; empty = frags")
	mode := flag.String("mode", "", "segmentation for -view top-windows: fixed (windows of -window) | gap (runs of events at most -gap apart, which -gap must then set); empty = fixed")
	weaponsStr := flag.String("weapons", "", "comma-separated weapons; restricts the killing weapon for -view top-kills and the scoring events for -view top-windows")
	itemsStr := flag.String("items", "", "comma-separated item instances (ya_1) or kind tokens (ya) for -view items-summary")
	kindsStr := flag.String("kinds", "", "comma-separated item categories (armor, mega, ...) for -view items-summary")
	windowStr := flag.String("window", "0", "fixed window length for -view top-windows (e.g. 30s); 0 = the view default 30s. Rejected under -mode gap")
	gapStr := flag.String("gap", "", "for -view top-kills, the burst capture gap (default 3s); for -view top-windows -mode gap, the required inter-event gap")
	contestedStr := flag.String("contested", "0", "return-damage window for -view top-kills (e.g. 4s); 0 = the view default 4s")
	minLifeStr := flag.String("min-life", "0", "drop lives shorter than this for -view lives")
	minScore := flag.Int("min-score", 0, "drop windows scoring below this for -view top-windows; unset = the view default 1 (an explicit 0 is meaningful — it keeps zero-scoring windows on the net metrics)")
	graphFmt := flag.String("graph", "", "print the analyzer dependency graph (mermaid | json) and exit; no demo argument needed")
	artifactsMD := flag.Bool("artifacts-md", false, "print the generated artifact catalog (ARTIFACTS.md) and exit; no demo argument needed")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qw-analyze [options] <demo.mvd | demo.mvd.gz | directory>\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	// -graph dumps the pipeline's dependency DAG and exits; it needs no demo.
	if *graphFmt != "" {
		out, err := analyzer.ExportGraph(*graphFmt)
		if err != nil {
			fmt.Fprintln(os.Stderr, "qw-analyze:", err)
			os.Exit(2)
		}
		fmt.Println(out)
		return
	}

	// -artifacts-md regenerates the committed artifact catalog and exits; it
	// needs no demo (the manifest is static per binary). `make artifacts-md`
	// redirects this into mvd-analytics/ARTIFACTS.md.
	if *artifactsMD {
		fmt.Print(analyzer.ArtifactsMarkdown())
		return
	}

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	input := flag.Arg(0)

	var regionsOverride []config.MapRegionOverride
	if *regionsPath != "" {
		loaded, err := loadRegionsOverride(*regionsPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "qw-analyze:", err)
			os.Exit(1)
		}
		regionsOverride = loaded
	}

	// Which flags the user actually typed. Go's flag package cannot tell an
	// omitted flag from one set to its zero value, and two things here need
	// that distinction: -min-score, where 0 is meaningful, and the
	// applicability check that rejects a knob aimed at the wrong -view.
	setFlags := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })

	vopts, err := parseViewOptions(*viewName, *bucketStr, *fieldsStr, *reducerArgs, *fromStr, *toStr, *playersStr, *eventTypesStr, *minDwellStr, *timeStr, *includeTeam, *includeStr, setFlags, derivedFlags{
		limit:     *limit,
		perPlayer: *perPlayer,
		minDamage: *minDamage,
		dmg:       *dmgStr,
		metric:    *metric,
		mode:      *mode,
		weapons:   *weaponsStr,
		items:     *itemsStr,
		kinds:     *kindsStr,
		window:    *windowStr,
		gap:       *gapStr,
		contested: *contestedStr,
		minLife:   *minLifeStr,
		minScore:  *minScore,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "qw-analyze:", err)
		os.Exit(2)
	}

	if *bulk || *outDir != "" {
		if *outDir == "" {
			fmt.Fprintln(os.Stderr, "qw-analyze: -bulk requires -out-dir")
			os.Exit(2)
		}
		if err := runBulk(input, *outDir, *format, regionsOverride, vopts); err != nil {
			fmt.Fprintln(os.Stderr, "qw-analyze:", err)
			os.Exit(1)
		}
		return
	}

	if err := runOne(input, os.Stdout, *format, *indent, regionsOverride, vopts); err != nil {
		fmt.Fprintln(os.Stderr, "qw-analyze:", err)
		os.Exit(1)
	}
}

// derivedFlags carries the raw -view top-kills / top-windows / lives /
// items-summary knobs. They travel as one struct rather than a dozen more
// positional parameters on parseViewOptions, which is already at its limit.
type derivedFlags struct {
	limit, perPlayer, minDamage     int
	minScore                        int
	dmg, metric, mode               string
	weapons, items, kinds           string
	window, gap, contested, minLife string
}

// maxInt32Ms guards the duration→int32-ms conversions below. A duration past
// this wraps when narrowed, and a wrapped value lands back in the view's
// "0 means default" branch — an explicit huge argument silently becoming a
// default is the one outcome worse than an error.
const maxInt32Ms = time.Duration(math.MaxInt32) * time.Millisecond

// msConv narrows durations to the int32 ms the view layer takes, rejecting the
// negatives and overflows that would otherwise read as "0, so use the default".
// It accumulates the first error so a whole options struct can be built inline
// and checked once, rather than four times per view.
type msConv struct{ err error }

func (c *msConv) v(name string, d time.Duration) int32 {
	if c.err != nil {
		return 0
	}
	if d < 0 {
		c.err = fmt.Errorf("%s must not be negative, got %s", name, d)
		return 0
	}
	if d > maxInt32Ms {
		c.err = fmt.Errorf("%s is too large: %s exceeds the %s the view layer can represent", name, d, maxInt32Ms)
		return 0
	}
	return int32(d / time.Millisecond)
}

// df, not d: the blocks below shadow d with time.ParseDuration results.
func parseViewOptions(viewName, bucketStr, fieldsStr string, reducerArgs []string, fromStr, toStr, playersStr, eventTypesStr, minDwellStr, timeStr string, includeTeam bool, includeStr string, setFlags map[string]bool, df derivedFlags) (*viewOptions, error) {
	v := &viewOptions{view: viewName, includeTeam: includeTeam, include: map[string]bool{}}

	v.limit = df.limit
	v.perPlayer = df.perPlayer
	v.minDamage = df.minDamage
	v.dmg = df.dmg
	v.metric = df.metric
	v.mode = df.mode
	if setFlags["min-score"] {
		v.minScore = &df.minScore
	}
	if df.weapons != "" {
		v.weapons = splitCSV(df.weapons)
	}
	if df.items != "" {
		v.items = splitCSV(df.items)
	}
	if df.kinds != "" {
		v.kinds = splitCSV(df.kinds)
	}
	// -gap has no default: top-windows gap mode requires an explicit value
	// (see TopWindowsOptions.GapMs), so "unset" has to stay distinguishable
	// from "0". Sub-millisecond values are rejected here rather than allowed
	// to truncate to 0 — that would clear the distinction again and make the
	// view report the flag as missing when it was passed.
	if df.gap != "" {
		dur, err := time.ParseDuration(df.gap)
		if err != nil {
			return nil, fmt.Errorf("bad -gap: %w", err)
		}
		if dur < time.Millisecond {
			return nil, fmt.Errorf("-gap must be at least 1ms, got %s", dur)
		}
		v.gap = dur
		v.gapSet = true
	}
	for _, f := range []struct {
		name string
		raw  string
		dst  *time.Duration
	}{
		{"-window", df.window, &v.window},
		{"-contested", df.contested, &v.contested},
		{"-min-life", df.minLife, &v.minLife},
	} {
		if f.raw == "" {
			continue
		}
		dur, err := time.ParseDuration(f.raw)
		if err != nil {
			return nil, fmt.Errorf("bad %s: %w", f.name, err)
		}
		*f.dst = dur
	}

	if bucketStr != "" {
		d, err := time.ParseDuration(bucketStr)
		if err != nil {
			return nil, fmt.Errorf("bad -bucket: %w", err)
		}
		v.bucketDur = d
	}
	if fieldsStr != "" {
		v.fields = splitCSV(fieldsStr)
	}
	if len(reducerArgs) > 0 {
		v.reducers = map[string]string{}
		for _, kv := range reducerArgs {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("bad -reducer %q (want field=name)", kv)
			}
			v.reducers[parts[0]] = parts[1]
		}
	}
	if fromStr != "" {
		d, err := time.ParseDuration(fromStr)
		if err != nil {
			return nil, fmt.Errorf("bad -from: %w", err)
		}
		v.from = d
	}
	if toStr != "" {
		d, err := time.ParseDuration(toStr)
		if err != nil {
			return nil, fmt.Errorf("bad -to: %w", err)
		}
		v.to = d
	}
	if playersStr != "" {
		v.players = splitCSV(playersStr)
	}
	if eventTypesStr != "" {
		v.eventTypes = splitCSV(eventTypesStr)
	}
	if minDwellStr != "" {
		d, err := time.ParseDuration(minDwellStr)
		if err != nil {
			return nil, fmt.Errorf("bad -min-dwell: %w", err)
		}
		v.minDwell = d
	}
	if timeStr != "" {
		d, err := time.ParseDuration(timeStr)
		if err != nil {
			return nil, fmt.Errorf("bad -time: %w", err)
		}
		v.timeAt = d
		v.timeSet = true
	}
	for _, opt := range splitCSV(includeStr) {
		v.include[opt] = true
	}

	switch v.view {
	case "full", "buckets", "events", "trails", "stream-slice", "state-at", "region-control",
		"top-kills", "top-windows", "lives", "items-summary", "airgibs":
	default:
		return nil, fmt.Errorf("unknown -view %q", v.view)
	}
	// Reject knobs aimed at a different view before spending a full analysis
	// pass on a query that would silently ignore them.
	if allowed, ok := derivedViewFlags[v.view]; ok {
		for name := range setFlags {
			if allowed[name] {
				continue
			}
			if !slices.Contains(viewScopedFlags, name) {
				continue // a global flag like -format or -pretty
			}
			return nil, fmt.Errorf("-%s does not apply to -view %s", name, v.view)
		}
	} else {
		for _, name := range derivedOnlyFlags {
			if setFlags[name] {
				return nil, fmt.Errorf("-%s applies only to -view top-kills / top-windows / lives / items-summary", name)
			}
		}
	}
	// -window and -gap are mutually exclusive under top-windows: the view
	// rejects the one that does not belong to the chosen mode rather than
	// silently ignoring it, so catch it here where the flag name is known.
	if v.view == "top-windows" {
		switch strings.ToLower(v.mode) {
		case "gap":
			if !v.gapSet {
				return nil, fmt.Errorf("-mode gap requires -gap (there is deliberately no default: frag and damage cadences differ too much for one value)")
			}
			if v.window != 0 {
				return nil, fmt.Errorf("-window does not apply under -mode gap")
			}
		case "", "fixed":
			if v.gapSet {
				return nil, fmt.Errorf("-gap does not apply under -mode fixed; use -window")
			}
		}
	}
	return v, nil
}

func loadRegionsOverride(path string) ([]config.MapRegionOverride, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read regions %s: %w", path, err)
	}
	var ov config.MapRegionOverrides
	if err := json.Unmarshal(data, &ov); err != nil {
		return nil, fmt.Errorf("parse regions %s: %w", path, err)
	}
	return ov.Regions, nil
}

func runOne(path string, w io.Writer, format string, pretty bool, regionsOverride []config.MapRegionOverride, vopts *viewOptions) error {
	switch format {
	case "events":
		return dumpEvents(path, w)
	case "json":
		if vopts != nil && vopts.view != "full" {
			return dumpView(path, w, regionsOverride, vopts, pretty)
		}
		return dumpJSON(path, w, pretty, regionsOverride, vopts)
	case "md":
		return dumpMarkdown(path, w, regionsOverride)
	default:
		return fmt.Errorf("unknown format %q (want json | md | events)", format)
	}
}

// analyzeOptions controls the optional registry/parser knobs analyzePath
// enables before running the pipeline. The zero value is the plain
// analyze used by -format md and the non-full -view paths; -format json
// -include ... turns individual knobs on. Keeping them here means a new
// -include knob is wired in one place, not copied into each dumper.
type analyzeOptions struct {
	buildShotStreams bool // -include projectiles/beams: spatial rocket/grenade/LG streams
	buildNails       bool // -include nails: svc_nails decode + ng/sng nail linkage (also flips the parser)
	computeLOS       bool // -include los: run the lazy line-of-sight/PVS pass after analyze
}

// analyzePath opens the demo, runs the default registry with the region
// override and any requested knobs, and returns the finalised Result.
// Shared by dumpJSON, dumpView and dumpMarkdown.
func analyzePath(path string, regionsOverride []config.MapRegionOverride, opts analyzeOptions) (*result.Result, error) {
	src, err := mvdsource.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer src.Close()

	reg := analyzer.NewDefaultRegistry()
	if regionsOverride != nil {
		reg.SetRegionsOverride(regionsOverride)
	}
	if opts.buildShotStreams {
		reg.BuildShotStreams = true
	}
	// AnalyzeSource takes a pre-opened source, so the parser's nail decode
	// is enabled here rather than inside the registry.
	if opts.buildNails {
		reg.BuildNails = true
		src.Parser().SetDecodeNails(true)
	}
	res, err := reg.AnalyzeSource(src, filepath.Base(path))
	if err != nil {
		return nil, err
	}
	// Line of sight is computed lazily (the heaviest position-derived pass);
	// the same pass also fills the PVS tracks, so los and pvs appear together.
	// A map with no usable visibility BSP yields ErrNoBSP — warn and continue
	// (the LOS section is simply absent), matching the API's non-fatal handling.
	if opts.computeLOS {
		if err := analyzer.ComputeLOS(res); err != nil {
			fmt.Fprintf(os.Stderr, "warning: line of sight unavailable: %v\n", err)
		}
	}
	return res, nil
}

func dumpJSON(path string, w io.Writer, pretty bool, regionsOverride []config.MapRegionOverride, vopts *viewOptions) error {
	var opts analyzeOptions
	if vopts != nil {
		opts.buildShotStreams = vopts.include["projectiles"] || vopts.include["beams"]
		opts.buildNails = vopts.include["nails"]
		opts.computeLOS = vopts.include["los"]
	}
	res, err := analyzePath(path, regionsOverride, opts)
	if err != nil {
		return err
	}

	// Position-track columns are opt-in: by default strip the whole
	// native-rate track from JSON to keep the file small (~12 MB per 4on4
	// match). -include positions/view/height/liquid keeps each column set.
	sel := streamColumnSelection{}
	if vopts != nil {
		sel.positions = vopts.include["positions"]
		sel.view = vopts.include["view"]
		sel.height = vopts.include["height"]
		sel.liquid = vopts.include["liquid"]
		sel.velocity = vopts.include["velocity"]
	}
	stripStreamColumns(res, sel)

	enc := json.NewEncoder(w)
	if pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(res)
}

// dumpView analyses the demo, runs the requested view function on the
// finalised Result, and writes its JSON to w.
func dumpView(path string, w io.Writer, regionsOverride []config.MapRegionOverride, vopts *viewOptions, pretty bool) error {
	res, err := analyzePath(path, regionsOverride, analyzeOptions{})
	if err != nil {
		return err
	}

	enc := json.NewEncoder(w)
	if pretty {
		enc.SetIndent("", "  ")
	}

	switch vopts.view {
	case "buckets":
		bv, err := view.Buckets(res, view.BucketsOptions{
			WindowMs:    int(vopts.bucketDur / time.Millisecond),
			StartTime:   int32(vopts.from.Milliseconds()),
			EndTime:     int32(vopts.to.Milliseconds()),
			Players:     vopts.players,
			Fields:      vopts.fields,
			Reducers:    vopts.reducers,
			IncludeTeam: vopts.includeTeam,
		})
		if err != nil {
			return err
		}
		return enc.Encode(bv)

	case "events":
		ev, err := view.Events(res, view.EventsFilter{
			StartTime: int32(vopts.from.Milliseconds()),
			EndTime:   int32(vopts.to.Milliseconds()),
			Players:   vopts.players,
			Types:     vopts.eventTypes,
		})
		if err != nil {
			return err
		}
		return enc.Encode(ev)

	case "stream-slice":
		ssv, err := view.StreamSlice(res, view.StreamSliceOptions{
			Start:   int32(vopts.from.Milliseconds()),
			End:     int32(vopts.to.Milliseconds()),
			Players: vopts.players,
			Fields:  vopts.fields,
		})
		if err != nil {
			return err
		}
		return enc.Encode(ssv)

	case "state-at":
		if !vopts.timeSet {
			return fmt.Errorf("-view state-at requires -time")
		}
		v, err := view.StateAt(res, view.StateAtOptions{
			Time:    int32(vopts.timeAt.Milliseconds()),
			Players: vopts.players,
			Fields:  vopts.fields,
		})
		if err != nil {
			return err
		}
		return enc.Encode(v)

	case "trails":
		tv, err := view.LocTrails(res, view.LocTrailsOptions{
			Players:    vopts.players,
			MinDwellMs: int(vopts.minDwell / time.Millisecond),
			StartTime:  int32(vopts.from.Milliseconds()),
			EndTime:    int32(vopts.to.Milliseconds()),
		})
		if err != nil {
			return err
		}
		return enc.Encode(tv)

	case "top-kills":
		var c msConv
		opts := view.TopKillsOptions{
			GapMs:       c.v("-gap", vopts.gap),
			ContestedMs: c.v("-contested", vopts.contested),
			Limit:       vopts.limit,
			Players:     vopts.players,
			Weapons:     vopts.weapons,
			MinDamage:   vopts.minDamage,
			From:        c.v("-from", vopts.from),
			To:          c.v("-to", vopts.to),
			Dmg:         vopts.dmg,
		}
		if c.err != nil {
			return c.err
		}
		tkv, err := view.TopKills(res, opts)
		if err != nil {
			return err
		}
		// The view functions do not stamp TimeUnit; every mvd-api handler
		// sets it after the call. Mirror that so the CLI and REST bodies
		// agree rather than differing by a missing echo.
		tkv.TimeUnit = view.UnitMs
		return enc.Encode(tkv)

	case "top-windows":
		var c msConv
		opts := view.TopWindowsOptions{
			Metric:    vopts.metric,
			Mode:      vopts.mode,
			WindowMs:  c.v("-window", vopts.window),
			GapMs:     c.v("-gap", vopts.gap),
			Limit:     vopts.limit,
			PerPlayer: vopts.perPlayer,
			Players:   vopts.players,
			Weapons:   vopts.weapons,
			From:      c.v("-from", vopts.from),
			To:        c.v("-to", vopts.to),
			Dmg:       vopts.dmg,
			Min:       vopts.minScore,
		}
		if c.err != nil {
			return c.err
		}
		twv, err := view.TopWindows(res, opts)
		if err != nil {
			return err
		}
		twv.TimeUnit = view.UnitMs
		return enc.Encode(twv)

	case "lives":
		var c msConv
		opts := view.LivesOptions{
			Players: vopts.players,
			From:    c.v("-from", vopts.from),
			To:      c.v("-to", vopts.to),
			Dmg:     vopts.dmg,
			MinMs:   c.v("-min-life", vopts.minLife),
		}
		if c.err != nil {
			return c.err
		}
		lv, err := view.Lives(res, opts)
		if err != nil {
			return err
		}
		lv.TimeUnit = view.UnitMs
		return enc.Encode(lv)

	case "items-summary":
		var c msConv
		opts := view.ItemOptions{
			Items:   vopts.items,
			Players: vopts.players,
			Kinds:   vopts.kinds,
			From:    c.v("-from", vopts.from),
			To:      c.v("-to", vopts.to),
		}
		if c.err != nil {
			return c.err
		}
		// Items/ItemsSummary are always available — an absent section is an
		// empty list, not an error — so there is no error return to check.
		sv := view.ItemsSummary(res, opts)
		sv.TimeUnit = view.UnitMs
		return enc.Encode(sv)

	case "airgibs":
		airgibs, err := view.Airgibs(res)
		if err != nil {
			return fmt.Errorf("airgibs unavailable for this demo (no timeline analysis): %w", err)
		}
		return enc.Encode(view.AirgibsEnvelope{TimeUnit: view.UnitMs, Airgibs: airgibs})

	case "region-control":
		ta := res.TimelineAnalysis
		if ta == nil || ta.RegionControl == nil {
			return fmt.Errorf("region-control unavailable for this demo")
		}
		rcv, err := view.RegionControl(res, view.RegionControlOptions{
			WindowMs: int(vopts.bucketDur / time.Millisecond),
		})
		if err != nil {
			return err
		}
		return enc.Encode(rcv)
	}
	return fmt.Errorf("unhandled view %q", vopts.view)
}

// streamColumnSelection records which position-track columns the
// -include flag asked to keep. The whole track is dropped when none are
// selected.
type streamColumnSelection struct {
	positions bool // x/y/z (+ the per-sample loc label li)
	view      bool // vp/vya — view direction
	height    bool // h — height above floor
	liquid    bool // lq — liquid state
	velocity  bool // vx/vy/vz — velocity
}

func (s streamColumnSelection) any() bool {
	return s.positions || s.view || s.height || s.liquid || s.velocity
}

// stripStreamColumns drops position-track data the consumer did not ask
// for. With no position-family -include token the whole track is nil'd
// (the default — it is ~12 MB per 4on4). Otherwise the track is kept and
// each optional column is nil'd unless its token was given, so a consumer
// can include just view, just height, etc. without the rest. x/y/z are
// the track's base and always remain when the track is kept.
func stripStreamColumns(r *result.Result, sel streamColumnSelection) {
	if r.Streams == nil {
		return
	}
	for i := range r.Streams.Players {
		pt := r.Streams.Players[i].Position
		if pt == nil {
			continue
		}
		if !sel.any() {
			r.Streams.Players[i].Position = nil
			continue
		}
		if !sel.positions {
			pt.Li = nil
		}
		if !sel.view {
			pt.VP = nil
			pt.VYa = nil
		}
		if !sel.height {
			pt.H = nil
		}
		if !sel.liquid {
			pt.Lq = nil
		}
		if !sel.velocity {
			pt.VX = nil
			pt.VY = nil
			pt.VZ = nil
		}
	}
}

func dumpEvents(path string, w io.Writer) error {
	src, err := mvdsource.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer src.Close()

	enc := json.NewEncoder(w)
	for {
		ev, err := src.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		// Wrap in a small envelope so consumers always see kind+time at
		// the top even for events whose own fields clash with those names.
		envelope := struct {
			Kind int     `json:"kind"`
			Time float64 `json:"time"`
			Data any     `json:"data"`
		}{int(ev.EventType()), ev.EventTime(), ev}
		if err := enc.Encode(envelope); err != nil {
			return err
		}
	}
}

func dumpMarkdown(path string, w io.Writer, regionsOverride []config.MapRegionOverride) error {
	res, err := analyzePath(path, regionsOverride, analyzeOptions{})
	if err != nil {
		return err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", filepath.Base(path))
	if res.Match != nil {
		fmt.Fprintf(&b, "- duration: %.1fs\n", float64(res.Match.Duration)*0.001)
		fmt.Fprintf(&b, "- map: %s\n", res.Match.Map)
		if res.Match.MapTitle != "" && !strings.EqualFold(res.Match.MapTitle, res.Match.Map) {
			fmt.Fprintf(&b, "- map title: %s\n", res.Match.MapTitle)
		}
		fmt.Fprintf(&b, "- game dir: %s\n", res.Match.GameDir)
	}
	if res.Metadata != nil && res.Metadata.MatchSettings != nil {
		ms := res.Metadata.MatchSettings
		if ms.Mode != "" {
			fmt.Fprintf(&b, "- mode: %s\n", ms.Mode)
		}
		if ms.Timelimit > 0 {
			fmt.Fprintf(&b, "- timelimit: %d min\n", ms.Timelimit)
		}
		if ms.Matchtag != "" {
			fmt.Fprintf(&b, "- matchtag: %s\n", ms.Matchtag)
		}
	}

	if res.Match != nil && len(res.Match.Players) > 0 {
		fmt.Fprintf(&b, "\n## Players\n\n| Name | Team | Frags | Kills | Deaths |\n|---|---|---:|---:|---:|\n")
		for _, p := range res.Match.Players {
			var kills, deaths int
			if res.Frags != nil {
				if pf, ok := res.Frags.ByPlayer[p.Name]; ok {
					kills = pf.Kills
					deaths = pf.Deaths
				}
			}
			fmt.Fprintf(&b, "| %s | %s | %d | %d | %d |\n", p.Name, p.Team, p.Frags, kills, deaths)
		}
	}

	if res.Match != nil && len(res.Match.Teams) > 1 {
		fmt.Fprintf(&b, "\n## Teams\n\n| Team | Frags |\n|---|---:|\n")
		for _, t := range res.Match.Teams {
			fmt.Fprintf(&b, "| %s | %d |\n", t.Name, t.Frags)
		}
	}

	if res.TimelineAnalysis != nil {
		ta := res.TimelineAnalysis
		if n := len(ta.FragStreaks); n > 0 {
			show := n
			if show > 5 {
				show = 5
			}
			fmt.Fprintf(&b, "\n## Top frag streaks\n\n| Player | Team | Frags | Duration | Weapon |\n|---|---|---:|---:|---|\n")
			for _, s := range ta.FragStreaks[:show] {
				fmt.Fprintf(&b, "| %s | %s | %d | %.1fs | %s |\n", s.PlayerName, s.Team, s.Frags, float64(s.Duration)*0.001, s.Ewep)
			}
		}
		if n := len(ta.PowerupEvents); n > 0 {
			show := n
			if show > 5 {
				show = 5
			}
			fmt.Fprintf(&b, "\n## Top powerup runs\n\n| Player | Team | Powerup | Duration | Frags |\n|---|---|---|---:|---:|\n")
			for _, p := range ta.PowerupEvents[:show] {
				fmt.Fprintf(&b, "| %s | %s | %s | %.1fs | %d |\n", p.PlayerName, p.Team, p.PowerupType, float64(p.Duration)*0.001, p.Frags)
			}
		}
	}

	_, err = io.WriteString(w, b.String())
	return err
}

func runBulk(demosDir, outDir, format string, regionsOverride []config.MapRegionOverride, vopts *viewOptions) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(demosDir)
	if err != nil {
		return err
	}
	ext := outputExt(format)
	var processed, failed int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !isDemoFile(name) {
			continue
		}
		processed++
		outPath := filepath.Join(outDir, name+ext)
		f, err := os.Create(outPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "create:", err)
			failed++
			continue
		}
		err = runOne(filepath.Join(demosDir, name), f, format, false, regionsOverride, vopts)
		f.Close()
		if err != nil {
			fmt.Fprintln(os.Stderr, name+":", err)
			failed++
			continue
		}
		fmt.Fprintln(os.Stderr, "wrote", outPath)
	}
	fmt.Fprintf(os.Stderr, "processed=%d failed=%d\n", processed, failed)
	if failed > 0 {
		return fmt.Errorf("%d demo(s) failed", failed)
	}
	return nil
}

func outputExt(format string) string {
	switch format {
	case "md":
		return ".md"
	case "events":
		return ".events.jsonl"
	default:
		return ".json"
	}
}

func isDemoFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".mvd") || strings.HasSuffix(lower, ".mvd.gz")
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// stringListFlag is a tiny helper for repeatable string flags. The
// returned pointer's []string accumulates one entry per occurrence.
type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }
func stringListFlag(name, usage string) *stringList {
	var sl stringList
	flag.Var(&sl, name, usage)
	return &sl
}
