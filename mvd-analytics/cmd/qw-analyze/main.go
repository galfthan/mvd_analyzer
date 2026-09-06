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
// items, items-summary, highlights, frags, damage, aim, chat,
// backpacks, weapon-pickups, player-stats, shots, loc-graph, loc-table,
// metadata, demoinfo) are also available — the full mvd-api view surface.
//
// -view player-stats is NOT -view full's playerStats: the view applies
// the KTX overlay at read time (ping, speed, controlMs), where the
// stored section is the pre-overlay derived row.
//
// ng/sng hit attribution needs -include nails (svc_nails decoding is off
// by default because the nail stream is high volume). shots/aim omit
// `hits` for those weapons without it — omitted, not zeroed — and a
// stderr warning says so. mvd-api always builds them.
//
// gl accuracy needs -include projectiles for the same reason one step
// further: KTX counts a grenade that TOUCHED a player and the wire log
// records no such event, so the touch is re-derived from the grenade's
// tracked flight. Without the spatial streams there is no flight, and
// playerStats.accuracy.byWeapon.gl publishes the any-path count under
// hitsConvention "anyDamage" instead of KTX's directImpact — honest, but a
// different number from the same demo over REST. Warned about on stderr.
//
// The derived views (top-kills, top-windows, lives) take a -dmg damage
// family whose default is the VIEW default, raw. mvd-api substitutes
// bounded on the same queries, so an unset -dmg does not reproduce the
// REST response; pass -dmg bounded for that.
//
// On those views and items-summary/highlights, a flag belonging to a
// different view is rejected rather than ignored — the CLI analogue of
// mvd-api's unknown-query-param rejection. The seven original views
// predate the check and keep their existing leniency.
//
// -view accepts a comma-separated list. Analysis dominates the runtime,
// so several views share ONE pass and come back in an object keyed by
// view name, in the order listed; a single view is returned bare, as
// before. A knob is judged against the union of what the listed views
// accept and applies to each that takes it — with the exception of
// -gap, which top-kills and top-windows define differently and which is
// therefore rejected when both are listed.
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
//	qw-analyze -view highlights demo.mvd.gz             # discharges, quadbores, telefrags, airgibs
//	qw-analyze -view top-kills,highlights demo.mvd.gz       # both, one analysis pass
//	qw-analyze -view player-stats demo.mvd.gz            # canonical KTX-overlaid rows
//	qw-analyze -view damage -summary -weapons rl demo.mvd.gz
//	qw-analyze -view aim -from 2m -to 3m30s demo.mvd.gz  # windowed aim RECOMPUTE
//	qw-analyze -view buckets -layout column demo.mvd.gz  # what GET /buckets returns
//	qw-analyze -view trails,loc-table -loc index demo.mvd.gz
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
	"sort"
	"strings"
	"time"

	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/config"
	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-analytics/view"
	"github.com/mvd-analyzer/mvd-reader/events"
	mvdsource "github.com/mvd-analyzer/mvd-reader/source/mvd"
)

// viewOptions bundles every flag that's meaningful only for the
// non-full views. Parsed once in main().
type viewOptions struct {
	// views is -view split on commas, in the order given — which is the order
	// they are emitted in. A single entry keeps the bare, unwrapped response
	// shape; two or more wrap them in an object keyed by view name.
	views       []string
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
	parallel    bool            // -parallel: goroutine fan-out inside heavy passes

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

	summary      bool
	teams        []string
	source       string
	chatTypes    []string
	locIndex     bool   // -loc index
	layout       string // "" / row / column
	regionDetail string // "" / full / summary / none
}

// derivedViewFlags maps each view added on top of the original seven to the
// flags it consumes. A flag the user typed that is absent here is REJECTED
// rather than ignored, matching every mvd-api handler's writeUnknownParam:
// a knob that silently does nothing reads as one that worked. Only the new
// flags and the new views are policed — the original seven views predate
// this and are left as they were rather than breaking existing invocations.
var derivedViewFlags = map[string]map[string]bool{
	"top-kills":      {"limit": true, "gap": true, "contested": true, "min-damage": true, "weapons": true, "dmg": true, "players": true, "from": true, "to": true},
	"top-windows":    {"limit": true, "per-player": true, "metric": true, "mode": true, "window": true, "gap": true, "weapons": true, "dmg": true, "min-score": true, "players": true, "from": true, "to": true},
	"lives":          {"min-life": true, "dmg": true, "summary": true, "players": true, "from": true, "to": true},
	"items-summary":  {"items": true, "kinds": true, "players": true, "from": true, "to": true},
	"items":          {"items": true, "kinds": true, "players": true, "from": true, "to": true},
	"highlights":     {}, // the stored catalogue, unfiltered; kinds/players/preMs are REST-only knobs
	"frags":          {"players": true, "weapons": true, "from": true, "to": true, "summary": true},
	"damage":         {"players": true, "weapons": true, "from": true, "to": true, "summary": true, "dmg": true},
	"aim":            {"players": true, "from": true, "to": true, "summary": true, "include": true},
	"chat":           {"players": true, "from": true, "to": true, "chat-types": true},
	"backpacks":      {"players": true, "weapons": true, "from": true, "to": true},
	"weapon-pickups": {"players": true, "weapons": true, "source": true, "from": true, "to": true},
	"player-stats":   {"players": true, "teams": true},
	"metadata":       {},
	"demoinfo":       {},
	"loc-graph":      {},
	"loc-table":      {},
	// shots/aim take -include because nailgun hit attribution rides on
	// -include nails — which is exactly what warnUnlinkedNails tells the user
	// to pass, so rejecting it here would make the advice unfollowable.
	"shots": {"include": true},
}

// lenientViewNewFlags names, per original-seven view, the flags introduced
// after the leniency carve-out was drawn. Grandfathering exists so
// invocations that already worked keep working; it cannot justify silently
// swallowing a flag that did not exist then, so these are policed on the
// lenient views too.
var lenientViewNewFlags = map[string]map[string]bool{
	"full":           {},
	"buckets":        {"loc": true, "layout": true},
	"events":         {"loc": true},
	"trails":         {"loc": true},
	"stream-slice":   {"loc": true},
	"state-at":       {"loc": true},
	"region-control": {"region-detail": true},
}

// postLenientFlags are those newer flags: the derived-view knobs plus the
// three enum flags. A lenient view accepts one only if it appears above.
var postLenientFlags = append(slices.Clone(derivedOnlyFlags), "loc", "layout", "region-detail")

// Closed vocabularies the REST layer owns and the view layer deliberately
// does not check. Kept in step with handleChat, handleWeaponPickups and the
// openapi dmg enum.
var (
	knownChatTypes     = []string{"chat", "teamsay"}
	knownPickupSources = []string{"world", "backpack", "unknown"}
	knownDmgFamilies   = []string{"raw", "bounded", "both"}
)

// lenientViews are the seven that predate the flag-applicability check. They
// accept any view-scoped flag, including ones they ignore, rather than break
// invocations that already worked.
var lenientViews = []string{"full", "buckets", "events", "trails", "stream-slice", "state-at", "region-control"}

// viewsAccepting lists, in stable order, the views that consume the named
// flag — the vocabulary an error message should quote.
func viewsAccepting(flagName string) []string {
	var out []string
	for _, name := range lenientViews {
		if lenientViewNewFlags[name][flagName] {
			out = append(out, name)
		}
	}
	for _, name := range sortedKeys(derivedViewFlags) {
		if derivedViewFlags[name][flagName] {
			out = append(out, name)
		}
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// derivedOnlyFlags are the flags introduced for the views above. They are
// meaningless on the original seven, so typing one there is also rejected.
var derivedOnlyFlags = []string{
	"limit", "per-player", "min-damage", "min-score", "dmg", "metric",
	"mode", "weapons", "items", "kinds", "window", "gap", "contested", "min-life",
	"summary", "teams", "source", "chat-types",
}

// viewScopedFlags is every flag that selects or shapes a view, as opposed to
// the global ones (-format, -pretty, -bulk, -out-dir, -regions) that apply
// whatever is being produced. The applicability check consults this list to
// decide whether an unrecognised-for-this-view flag is its business: the
// legacy eight belong to specific original views, so they are just as wrong
// on -view highlights as -metric is, and get the same rejection.
var viewScopedFlags = append([]string{
	"players", "from", "to",
	"bucket", "fields", "reducer", "event-types", "min-dwell", "time",
	"include-team", "include", "loc", "layout", "region-detail",
}, derivedOnlyFlags...)

func main() {
	format := flag.String("format", "json", "output format: json | md | events")
	outDir := flag.String("out-dir", "", "bulk mode: write <demo>.<ext> into this directory")
	bulk := flag.Bool("bulk", false, "treat the input path as a directory and analyze every demo in it")
	indent := flag.Bool("pretty", false, "pretty-print JSON output (single-demo mode only); pipe to `jq .` for human reading")
	regionsPath := flag.String("regions", "", "path to a regions JSON ({\"regions\":[{\"name\":...,\"locs\":[...]}]}) to override the embedded per-map regions for the analyzed demo")
	parallel := flag.Bool("parallel", false, "use goroutines inside heavy analysis passes (opt-in: leave off when running many analyses concurrently)")

	viewName := flag.String("view", "full", "view(s), comma-separated: full | buckets | events | trails | stream-slice | state-at | region-control | top-kills | top-windows | lives | items | items-summary | highlights | frags | damage | aim | chat | backpacks | weapon-pickups | player-stats | shots | loc-graph | loc-table | metadata | demoinfo. Several views share one analysis pass and come back in an object keyed by view name, in the order listed; a single view is returned bare")
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
	dmgStr := flag.String("dmg", "", "damage family for -view top-kills / top-windows / lives / damage: raw | bounded (damage also takes both); empty = the view default (raw). Note mvd-api defaults the same queries to bounded, so an unset -dmg does NOT reproduce the REST response")
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
	summary := flag.Bool("summary", false, "aggregates only, dropping the per-event log: -view frags | damage | aim | lives")
	teamsStr := flag.String("teams", "", "comma-separated team names for -view player-stats")
	source := flag.String("source", "", "pickup source for -view weapon-pickups: world | backpack | unknown")
	chatTypes := flag.String("chat-types", "", "comma-separated message types for -view chat; empty = chat,teamsay")
	locMode := flag.String("loc", "", "loc representation for -view buckets / events / stream-slice / state-at / trails: name (default) | index (raw li indices, decode against -view loc-table)")
	layout := flag.String("layout", "", "bucket layout for -view buckets: row (default) | column. mvd-api defaults to column, so an unset -layout does NOT reproduce the REST response")
	regionDetail := flag.String("region-detail", "", "region list detail for -view region-control: full (default) | summary (drop polygon points) | none")
	graphFmt := flag.String("graph", "", "print the analyzer dependency graph (mermaid | json) and exit; no demo argument needed")
	artifactsMD := flag.Bool("artifacts-md", false, "print the generated artifact catalog (ARTIFACTS.md) and exit; no demo argument needed")
	warnFlag := flag.Bool("warn", false, "print the full parser-warning table to stderr (per distinct message: count + first occurrence). A one-line summary always prints when a demo raises any; -format json also carries the whole census in the result's parseWarnings")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: qw-analyze [options] <demo.mvd | demo.mvd.gz | directory>\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	warnDetail = *warnFlag

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

	// -view only means anything under -format json: md renders its own fixed
	// summary and events dumps the Layer-1 stream. Both used to accept a -view
	// and throw it away, which is the same silent-ignore the per-view flag
	// check exists to prevent — one level up.
	if setFlags["view"] && *format != "json" {
		fmt.Fprintf(os.Stderr, "qw-analyze: -view does not apply to -format %s (it selects a view of the analysed Result; -format %s has its own fixed shape)\n", *format, *format)
		os.Exit(2)
	}

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

		summary:      *summary,
		teams:        *teamsStr,
		source:       *source,
		chatTypes:    *chatTypes,
		locMode:      *locMode,
		layout:       *layout,
		regionDetail: *regionDetail,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "qw-analyze:", err)
		os.Exit(2)
	}
	vopts.parallel = *parallel

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

	summary                       bool
	teams, source, chatTypes      string
	locMode, layout, regionDetail string
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
	v := &viewOptions{views: splitCSV(viewName), includeTeam: includeTeam, include: map[string]bool{}}
	if len(v.views) == 0 {
		return nil, fmt.Errorf("-view must name at least one view")
	}
	for i, name := range v.views {
		if slices.Contains(v.views[:i], name) {
			return nil, fmt.Errorf("-view lists %q twice", name)
		}
	}

	v.limit = df.limit
	v.perPlayer = df.perPlayer
	v.minDamage = df.minDamage
	v.dmg = df.dmg
	v.metric = df.metric
	v.mode = df.mode
	if setFlags["min-score"] {
		v.minScore = &df.minScore
	}
	v.summary = df.summary
	if df.teams != "" {
		v.teams = splitCSV(df.teams)
	}
	// The next three mirror validation the REST layer does and the view
	// deliberately does not: view.Chat matches types case-sensitively and
	// view.Damage documents that it treats any unrecognised Dmg as raw, so
	// without a check here a typo is a silently empty or silently wrong body.
	if df.chatTypes != "" {
		v.chatTypes = splitCSV(df.chatTypes)
		for i, t := range v.chatTypes {
			t = strings.ToLower(t)
			if !slices.Contains(knownChatTypes, t) {
				return nil, fmt.Errorf("-chat-types %q is not one of %s", t, strings.Join(knownChatTypes, ", "))
			}
			v.chatTypes[i] = t
		}
	}
	if df.source != "" {
		v.source = strings.ToLower(df.source)
		if !slices.Contains(knownPickupSources, v.source) {
			return nil, fmt.Errorf("-source must be one of %s, got %q", strings.Join(knownPickupSources, ", "), df.source)
		}
	}
	if df.dmg != "" && !slices.Contains(knownDmgFamilies, strings.ToLower(df.dmg)) {
		return nil, fmt.Errorf("-dmg must be one of %s, got %q", strings.Join(knownDmgFamilies, ", "), df.dmg)
	}
	// The three enum flags are checked here rather than left to the view,
	// which either has no vocabulary for them (-loc, -region-detail are CLI
	// spellings of a bool and a response shape) or would only reject them
	// after a full analysis pass.
	switch strings.ToLower(df.locMode) {
	case "", "name":
	case "index":
		v.locIndex = true
	default:
		return nil, fmt.Errorf("-loc must be name or index, got %q", df.locMode)
	}
	switch strings.ToLower(df.layout) {
	case "", "row", "column":
		v.layout = strings.ToLower(df.layout)
	default:
		return nil, fmt.Errorf("-layout must be row or column, got %q", df.layout)
	}
	if df.regionDetail != "" {
		if !slices.Contains(view.KnownRegionModes, strings.ToLower(df.regionDetail)) {
			return nil, fmt.Errorf("-region-detail must be one of %s, got %q",
				strings.Join(view.KnownRegionModes, ", "), df.regionDetail)
		}
		v.regionDetail = strings.ToLower(df.regionDetail)
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

	// A flag is judged against the UNION of what the selected views accept: with
	// several views in play a knob only has to be meaningful to one of them.
	// The original seven predate the check and accept any PRE-EXISTING
	// view-scoped flag, so selecting one widens the union to match their old
	// leniency — but not for flags added since (see lenientViewNewFlags).
	allowedUnion := map[string]bool{}
	lenientSelected := false
	for _, name := range v.views {
		switch {
		case slices.Contains(lenientViews, name):
			lenientSelected = true
		case derivedViewFlags[name] != nil:
			for f := range derivedViewFlags[name] {
				allowedUnion[f] = true
			}
		default:
			return nil, fmt.Errorf("unknown -view %q (valid: %s)", name,
				strings.Join(append(slices.Clone(lenientViews), sortedKeys(derivedViewFlags)...), ", "))
		}
	}
	if lenientSelected {
		// A lenient view widens the union with the flags that predate the
		// check — but only those. Its post-carve-out flags come from
		// lenientViewNewFlags, so -view trails -layout column is an error
		// rather than a no-op.
		for _, f := range viewScopedFlags {
			if !slices.Contains(postLenientFlags, f) {
				allowedUnion[f] = true
			}
		}
		for _, name := range v.views {
			for f := range lenientViewNewFlags[name] {
				allowedUnion[f] = true
			}
		}
	}
	// Reject knobs aimed at a different view before spending a full analysis
	// pass on a query that would silently ignore them.
	for _, name := range viewScopedFlags {
		if !setFlags[name] || allowedUnion[name] {
			continue
		}
		// Name the views that WOULD accept it — more use than "does not apply
		// to -view buckets" on its own. Derived from the tables so it cannot
		// drift out of date the way a hand-written list does.
		if takers := viewsAccepting(name); len(takers) > 0 && !slices.ContainsFunc(v.views, func(s string) bool { return slices.Contains(takers, s) }) {
			return nil, fmt.Errorf("-%s applies only to -view %s", name, strings.Join(takers, " / "))
		}
		return nil, fmt.Errorf("-%s does not apply to -view %s", name, strings.Join(v.views, ","))
	}
	// -gap is the one flag two views define differently: on top-kills it is the
	// burst capture gap (default 3s), on top-windows the required inter-event
	// gap of mode=gap. Applying one value to both would silently reshape the
	// top-kills bursts, so ask for two invocations instead of guessing.
	if setFlags["gap"] && slices.Contains(v.views, "top-kills") && slices.Contains(v.views, "top-windows") {
		return nil, fmt.Errorf("-gap means different things to top-kills (burst gap) and top-windows (mode=gap interval); run them separately")
	}
	// -window and -gap are mutually exclusive under top-windows: the view
	// rejects the one that does not belong to the chosen mode rather than
	// silently ignoring it, so catch it here where the flag name is known.
	if slices.Contains(v.views, "top-windows") {
		switch strings.ToLower(v.mode) {
		case "gap":
			if !v.gapSet {
				return nil, fmt.Errorf("-mode gap requires -gap (there is deliberately no default: frag and damage cadences differ too much for one value)")
			}
			if v.window != 0 {
				return nil, fmt.Errorf("-window does not apply under -mode gap")
			}
		case "", "fixed":
			// Reachable only when top-windows is -gap's sole consumer: the
			// collision check above already rejected pairing it with top-kills.
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
		if vopts != nil && (len(vopts.views) > 1 || vopts.views[0] != "full") {
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
	parallel         bool // -parallel: goroutine fan-out inside heavy passes
}

// warnUnlinkedNails reports ng/sng fires whose hits were never attributed,
// which is what a run without -include nails produces: the nail linkage needs
// svc_nails decoding, off by default because the nail stream is high volume.
// The shots rows are honest about it — Hits is omitted rather than zeroed —
// but "the key is absent" is only a signal to a reader who knows to look, and
// a consumer doing `.hits // 0` silently reads it as perfect inaccuracy.
// mvd-api always builds nails (democache sets BuildNails), so this is one of
// the two places default CLI output diverges from the same demo over REST
// (warnUnclassifiedGrenades is the other).
func warnUnlinkedNails(res *result.Result, w io.Writer) {
	if res == nil || res.Shots == nil {
		return
	}
	var shots int
	for _, p := range res.Shots.ByPlayer {
		for _, wp := range p.ByWeapon {
			if wp.Weapon != "ng" && wp.Weapon != "sng" {
				continue
			}
			if wp.Hits == 0 {
				shots += wp.Shots
			}
		}
	}
	if shots > 0 {
		fmt.Fprintf(w, "qw-analyze: %d ng/sng fires have no hit attribution — pass -include nails for nailgun accuracy (mvd-api always builds it, so this run differs from the same demo over REST)\n", shots)
	}
}

// warnUnclassifiedGrenades reports gl fires on a demo whose wire damage log
// this run could not put on KTX's scale, which is what a run without
// -include projectiles produces: gl's direct-impact count is re-derived from
// the grenade's tracked flight (result.WeaponAim), and without the spatial
// shot streams there is no flight to read. The row stays honest, publishing
// the any-path count under hitsConvention "anyDamage", but that is a
// DIFFERENT number from the one the same demo returns over REST, which is
// the same reason the nail warning above exists.
func warnUnclassifiedGrenades(res *result.Result, w io.Writer) {
	if res == nil || res.Shots == nil || res.Damage == nil ||
		res.Damage.Source != result.DamageSourceKTX {
		return
	}
	var shots int
	for _, p := range res.Shots.ByPlayer {
		for _, wp := range p.ByWeapon {
			if wp.Weapon == "gl" {
				shots += wp.Shots
			}
		}
	}
	if shots > 0 {
		fmt.Fprintf(w, "qw-analyze: %d gl fires carry the any-path hit count — pass -include projectiles for KTX's grenade touch count (mvd-api always builds it, so this run differs from the same demo over REST)\n", shots)
	}
}

// warnDetail is the -warn flag: print the whole parse-warning table
// rather than the one-line summary. Package-level because every output
// path funnels through analyzePath, including dumpMarkdown, which takes
// no view options to hang it off.
var warnDetail bool

// reportParseWarnings prints the reader's parse-warning census to
// stderr. The one-liner is unconditional — the whole point of the
// Result-carried census is that the next protocol gap is visible on a
// normal run, and an operator reading -format md or piping JSON to a
// file would otherwise never see it. -warn adds the per-message table.
//
// The full detail is also in the JSON (`parseWarnings`) for -format
// json, but NOT in -view output or -format md, which is why the flag
// exists rather than "just read the JSON".
func reportParseWarnings(path string, res *result.Result, w io.Writer) {
	if res == nil || res.ParseWarnings == nil || res.ParseWarnings.Total == 0 {
		return
	}
	pw := res.ParseWarnings

	// Loudest category first, name as tie-break — the same rule the
	// groups table uses, so the two read consistently.
	types := make([]string, 0, len(pw.ByType))
	for t := range pw.ByType {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool {
		if pw.ByType[types[i]] != pw.ByType[types[j]] {
			return pw.ByType[types[i]] > pw.ByType[types[j]]
		}
		return types[i] < types[j]
	})
	parts := make([]string, 0, len(types))
	for _, t := range types {
		parts = append(parts, fmt.Sprintf("%s %d", t, pw.ByType[t]))
	}

	fmt.Fprintf(w, "qw-analyze: %s: %d parse warnings (%s) — wire data this reader could not decode; sections below it may be thin\n",
		filepath.Base(path), pw.Total, strings.Join(parts, ", "))
	if !warnDetail {
		if len(pw.Groups) > 0 {
			g := pw.Groups[0]
			fmt.Fprintf(w, "qw-analyze:   loudest: %s: %s (x%d, first at %.1fs) — pass -warn for the full table\n",
				g.Type, g.Message, g.Count, float64(g.FirstDemoTimeMs)*0.001)
		}
		return
	}
	for _, g := range pw.Groups {
		fmt.Fprintf(w, "qw-analyze:   x%-6d [first %7.1fs] %s: %s\n",
			g.Count, float64(g.FirstDemoTimeMs)*0.001, g.Type, g.Message)
	}
	if pw.DroppedWarnings > 0 {
		fmt.Fprintf(w, "qw-analyze:   (+%d warnings beyond the %d-group retention cap; the counts above are still exact)\n",
			pw.DroppedWarnings, events.MaxWarningGroups)
	}
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
	reg.Parallel = opts.parallel
	res, err := reg.AnalyzeSource(src, filepath.Base(path))
	if err != nil {
		return nil, err
	}
	reportParseWarnings(path, res, os.Stderr)
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
		opts.parallel = vopts.parallel
	}
	res, err := analyzePath(path, regionsOverride, opts)
	if err != nil {
		return err
	}
	if !opts.buildNails {
		warnUnlinkedNails(res, os.Stderr)
	}
	if !opts.buildShotStreams {
		warnUnclassifiedGrenades(res, os.Stderr)
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

// namedView pairs a view name with its already-marshalled body, so a
// multi-view response can be emitted in the order the user listed rather
// than the alphabetical order a map would impose.
type namedView struct {
	name string
	raw  json.RawMessage
}

type namedViews []namedView

func (n namedViews) MarshalJSON() ([]byte, error) {
	var b []byte
	b = append(b, '{')
	for i, nv := range n {
		if i > 0 {
			b = append(b, ',')
		}
		key, err := json.Marshal(nv.name)
		if err != nil {
			return nil, err
		}
		b = append(b, key...)
		b = append(b, ':')
		b = append(b, nv.raw...)
	}
	return append(b, '}'), nil
}

// dumpView analyses the demo ONCE and writes the requested views' JSON to w.
// A single -view keeps the bare response shape; several wrap them in an object
// keyed by view name. Analysis dominates the runtime — the view functions
// themselves are microseconds — so the whole point is that N views cost one
// pass, not N.
func dumpView(path string, w io.Writer, regionsOverride []config.MapRegionOverride, vopts *viewOptions, pretty bool) error {
	opts := analyzeOptionsFor(vopts)
	res, err := analyzePath(path, regionsOverride, opts)
	if err != nil {
		return err
	}
	accViews := slices.Contains(vopts.views, "shots") || slices.Contains(vopts.views, "aim") ||
		slices.Contains(vopts.views, "player-stats") || slices.Contains(vopts.views, "full")
	if !opts.buildNails && accViews {
		warnUnlinkedNails(res, os.Stderr)
	}
	if !opts.buildShotStreams && accViews {
		warnUnclassifiedGrenades(res, os.Stderr)
	}

	enc := json.NewEncoder(w)
	if pretty {
		enc.SetIndent("", "  ")
	}

	if len(vopts.views) == 1 {
		v, err := renderView(res, vopts.views[0], vopts)
		if err != nil {
			return err
		}
		return enc.Encode(v)
	}

	// "full" is rendered last because it strips the native-rate position
	// columns off the shared Result, which the position-derived views
	// (trails, state-at, region-control, stream-slice) still need.
	order := make([]string, 0, len(vopts.views))
	for _, name := range vopts.views {
		if name != "full" {
			order = append(order, name)
		}
	}
	if slices.Contains(vopts.views, "full") {
		order = append(order, "full")
	}

	bodies := map[string]json.RawMessage{}
	for _, name := range order {
		v, err := renderView(res, name, vopts)
		if err != nil {
			return fmt.Errorf("view %s: %w", name, err)
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("view %s: %w", name, err)
		}
		bodies[name] = raw
	}

	out := make(namedViews, 0, len(vopts.views))
	for _, name := range vopts.views {
		out = append(out, namedView{name: name, raw: bodies[name]})
	}
	return enc.Encode(out)
}

// analyzeOptionsFor derives the registry/parser knobs from -include and
// -parallel. Only -view full consumes the position columns, but the knobs are
// cheap to read here and the applicability check has already rejected
// -include on the selections that cannot use it.
func analyzeOptionsFor(vopts *viewOptions) analyzeOptions {
	var opts analyzeOptions
	if vopts != nil {
		opts.buildShotStreams = vopts.include["projectiles"] || vopts.include["beams"]
		opts.buildNails = vopts.include["nails"]
		opts.computeLOS = vopts.include["los"]
		opts.parallel = vopts.parallel
	}
	return opts
}

// renderView runs one view function against an already-analysed Result and
// returns the value to encode.
func renderView(res *result.Result, name string, vopts *viewOptions) (any, error) {
	switch name {
	case "full":
		// Position-track columns are opt-in: by default strip the whole
		// native-rate track to keep the output small (~12 MB per 4on4 match).
		stripStreamColumns(res, streamColumnSelection{
			positions: vopts.include["positions"],
			view:      vopts.include["view"],
			height:    vopts.include["height"],
			liquid:    vopts.include["liquid"],
			velocity:  vopts.include["velocity"],
		})
		return res, nil
	}
	return renderQueryView(res, name, vopts)
}

func renderQueryView(res *result.Result, name string, vopts *viewOptions) (any, error) {
	switch name {
	case "buckets":
		opts := view.BucketsOptions{
			WindowMs:    int(vopts.bucketDur / time.Millisecond),
			StartTime:   int32(vopts.from.Milliseconds()),
			EndTime:     int32(vopts.to.Milliseconds()),
			Players:     vopts.players,
			Fields:      vopts.fields,
			Reducers:    vopts.reducers,
			IncludeTeam: vopts.includeTeam,
			LocIndex:    vopts.locIndex,
			Layout:      vopts.layout,
		}
		// Layout picks the builder, exactly as handleBuckets does. The CLI
		// default stays row — REST defaults to column, and -layout column is
		// how you reproduce it.
		if opts.Layout == "column" {
			cb, err := view.BucketsColumnar(res, opts)
			if err != nil {
				return nil, err
			}
			cb.TimeUnit = view.UnitMs // handleBuckets stamps the columnar body
			return cb, nil
		}
		bv, err := view.Buckets(res, opts)
		if err != nil {
			return nil, err
		}
		return bv, nil

	case "events":
		ev, err := view.Events(res, view.EventsFilter{
			StartTime: int32(vopts.from.Milliseconds()),
			EndTime:   int32(vopts.to.Milliseconds()),
			Players:   vopts.players,
			Types:     vopts.eventTypes,
			LocIndex:  vopts.locIndex,
		})
		if err != nil {
			return nil, err
		}
		return ev, nil

	case "stream-slice":
		ssv, err := view.StreamSlice(res, view.StreamSliceOptions{
			Start:   int32(vopts.from.Milliseconds()),
			End:     int32(vopts.to.Milliseconds()),
			Players: vopts.players,
			Fields:  vopts.fields,
		})
		if err != nil {
			return nil, err
		}
		return ssv, nil

	case "state-at":
		if !vopts.timeSet {
			return nil, fmt.Errorf("-view state-at requires -time")
		}
		v, err := view.StateAt(res, view.StateAtOptions{
			Time:     int32(vopts.timeAt.Milliseconds()),
			Players:  vopts.players,
			Fields:   vopts.fields,
			LocIndex: vopts.locIndex,
		})
		if err != nil {
			return nil, err
		}
		return v, nil

	case "trails":
		tv, err := view.LocTrails(res, view.LocTrailsOptions{
			Players:    vopts.players,
			MinDwellMs: int(vopts.minDwell / time.Millisecond),
			StartTime:  int32(vopts.from.Milliseconds()),
			EndTime:    int32(vopts.to.Milliseconds()),
			LocIndex:   vopts.locIndex,
		})
		if err != nil {
			return nil, err
		}
		return tv, nil

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
			return nil, c.err
		}
		tkv, err := view.TopKills(res, opts)
		if err != nil {
			return nil, err
		}
		// The view functions do not stamp TimeUnit; every mvd-api handler
		// sets it after the call. Mirror that so the CLI and REST bodies
		// agree rather than differing by a missing echo.
		tkv.TimeUnit = view.UnitMs
		return tkv, nil

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
			return nil, c.err
		}
		twv, err := view.TopWindows(res, opts)
		if err != nil {
			return nil, err
		}
		twv.TimeUnit = view.UnitMs
		return twv, nil

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
			return nil, c.err
		}
		lv, err := view.Lives(res, opts)
		if err != nil {
			return nil, err
		}
		if vopts.summary {
			view.SummarizeLives(lv)
		}
		lv.TimeUnit = view.UnitMs
		return lv, nil

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
			return nil, c.err
		}
		// Items/ItemsSummary are always available — an absent section is an
		// empty list, not an error — so there is no error return to check.
		sv := view.ItemsSummary(res, opts)
		sv.TimeUnit = view.UnitMs
		return sv, nil

	case "highlights":
		h, err := view.Highlights(res)
		if err != nil {
			return nil, fmt.Errorf("highlights unavailable for this demo (no match streams or frag log): %w", err)
		}
		// The stored catalogue, every kind, the default airgib look-back —
		// what the post-processor baked in. Options are empty by
		// construction, so the validation error cannot fire.
		env, _ := view.FilterHighlights(h, view.HighlightsOptions{}, view.DefaultAirgibPreMs)
		return env, nil

	case "region-control":
		ta := res.TimelineAnalysis
		if ta == nil || ta.RegionControl == nil {
			return nil, fmt.Errorf("region-control unavailable for this demo")
		}
		var c msConv
		opts := view.RegionControlOptions{
			WindowMs: int(vopts.bucketDur / time.Millisecond),
			// Wired since the CLI accepted -from/-to and dropped them: the
			// window is what makes "who held RA in the first two minutes"
			// answerable, and REST has served it all along.
			StartTime: c.v("-from", vopts.from),
			EndTime:   c.v("-to", vopts.to),
		}
		if c.err != nil {
			return nil, c.err
		}
		rcv, err := view.RegionControl(res, opts)
		if err != nil {
			return nil, err
		}
		return view.RegionControlEnvelope{
			TimeUnit:            view.UnitMs,
			RegionControlResult: view.ShapeRegions(rcv, vopts.regionDetail),
		}, nil

	case "frags":
		var c msConv
		opts := view.FragOptions{
			Players: vopts.players,
			Weapons: vopts.weapons,
			From:    c.v("-from", vopts.from),
			To:      c.v("-to", vopts.to),
			Summary: vopts.summary,
		}
		if c.err != nil {
			return nil, c.err
		}
		fv, err := view.Frags(res, opts)
		if err != nil {
			return nil, err
		}
		return view.FragsEnvelope{TimeUnit: view.UnitMs, FragResult: fv}, nil

	case "damage":
		var c msConv
		opts := view.DamageOptions{
			Players: vopts.players,
			Weapons: vopts.weapons,
			From:    c.v("-from", vopts.from),
			To:      c.v("-to", vopts.to),
			Summary: vopts.summary,
			Dmg:     vopts.dmg,
		}
		if c.err != nil {
			return nil, c.err
		}
		dv, err := view.Damage(res, opts)
		if err != nil {
			return nil, err
		}
		return view.DamageEnvelope{TimeUnit: view.UnitMs, DamageResult: dv}, nil

	case "aim":
		var c msConv
		opts := view.AimOptions{
			Players: vopts.players,
			From:    c.v("-from", vopts.from),
			To:      c.v("-to", vopts.to),
			Summary: vopts.summary,
		}
		if c.err != nil {
			return nil, c.err
		}
		av, err := view.Aim(res, opts)
		if err != nil {
			return nil, err
		}
		return view.AimEnvelope{TimeUnit: view.UnitMs, AimResult: av}, nil

	case "chat":
		var c msConv
		opts := view.ChatOptions{
			From:    c.v("-from", vopts.from),
			To:      c.v("-to", vopts.to),
			Players: vopts.players,
			Types:   vopts.chatTypes,
		}
		if c.err != nil {
			return nil, c.err
		}
		return view.ChatEnvelope{TimeUnit: view.UnitMs, Messages: view.Chat(res, opts)}, nil

	case "backpacks":
		var c msConv
		opts := view.BackpackOptions{
			Players: vopts.players,
			Weapons: vopts.weapons,
			From:    c.v("-from", vopts.from),
			To:      c.v("-to", vopts.to),
		}
		if c.err != nil {
			return nil, c.err
		}
		bp, err := view.Backpacks(res, opts)
		if err != nil {
			return nil, err
		}
		return view.BackpacksEnvelope{TimeUnit: view.UnitMs, Backpacks: bp}, nil

	case "weapon-pickups":
		var c msConv
		opts := view.WeaponPickupOptions{
			Players: vopts.players,
			Weapons: vopts.weapons,
			Source:  vopts.source,
			From:    c.v("-from", vopts.from),
			To:      c.v("-to", vopts.to),
		}
		if c.err != nil {
			return nil, c.err
		}
		wp, err := view.WeaponPickups(res, opts)
		if err != nil {
			return nil, err
		}
		return view.WeaponPickupsEnvelope{TimeUnit: view.UnitMs, Pickups: wp}, nil

	case "player-stats":
		// view.PlayerStats applies the KTX overlay at read time, which is why
		// -view full's stored playerStats is NOT the same row: this is the
		// canonical one.
		ps, err := view.PlayerStats(res, view.PlayerStatsOptions{
			Players: vopts.players,
			Teams:   vopts.teams,
		})
		if err != nil {
			return nil, err
		}
		return view.PlayerStatsEnvelope{TimeUnit: view.UnitMs, PlayerStatsResult: ps}, nil

	case "items":
		var c msConv
		opts := view.ItemOptions{
			Items:   vopts.items,
			Players: vopts.players,
			Kinds:   vopts.kinds,
			From:    c.v("-from", vopts.from),
			To:      c.v("-to", vopts.to),
		}
		if c.err != nil {
			return nil, c.err
		}
		return view.ItemsEnvelope{TimeUnit: view.UnitMs, ItemsResult: view.Items(res, opts)}, nil

	case "metadata":
		// No timeUnit echo: metadata carries no match-position time.
		return view.Metadata(res)

	case "demoinfo":
		return view.DemoInfo(res)

	case "loc-graph":
		lg, err := view.LocGraph(res)
		if err != nil {
			return nil, err
		}
		return view.LocGraphEnvelope{TimeUnit: view.UnitMs, LocGraphResult: lg}, nil

	case "loc-table":
		// The interned loc names, index 0 the "" sentinel — what -loc index
		// output is decoded against. No timeUnit: no time values at all.
		// Shape matches handleLocTable, including the empty-not-null table.
		table := []string{}
		if res.TimelineAnalysis != nil && res.TimelineAnalysis.LocTable != nil {
			table = res.TimelineAnalysis.LocTable
		}
		return map[string]any{"locTable": table}, nil

	case "shots":
		sh, err := view.Shots(res)
		if err != nil {
			return nil, err
		}
		return view.ShotsEnvelope{TimeUnit: view.UnitMs, ShotsResult: sh}, nil
	}
	return nil, fmt.Errorf("unhandled view %q", name)
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
