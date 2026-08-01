package view

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Hot windows: each player's best stretches of the match.
//
// NOTE the filename: this must NOT be called hot_windows.go. Go treats a
// `_windows.go` suffix as a GOOS build constraint, so that name silently
// compiles on Windows only — the package builds everywhere else with the file
// absent, and every symbol in it reads as undefined. Same trap for _linux,
// _darwin, _amd64 and friends.
//
// The contract is one sentence — "in these N milliseconds this player scored
// higher on <metric> than in any other stretch of the same length" — and that
// is deliberate. An earlier design discovered the window length adaptively
// (Ruzzo-Tompa maximal-scoring segments against each player's own rate); it was
// dropped because "why did I get THIS segment?" had no short answer, which is
// fatal for a surface an AI agent has to justify to a human. windowMs as a
// caller-chosen knob covers burst damage (5 s) and hot streaks (30 s) with one
// parameter instead of a mode, and the naturally variable-length unit — the
// life — is served by Lives instead.
//
// PAUSES NEED NO CORRECTION AND NONE IS APPLIED. Every time here is
// match-relative, i.e. on the game clock, and the game clock FREEZES during a
// pause while wall-clock time runs on — that is the whole reason the
// game→wall-clock mapping in result/streams.go (GlobalStream, the P(g) term)
// exists. So a 30 s window is 30 s of play whether or not the match was paused
// inside it, and subtracting pause time here would double-correct.

// HotWindowMetric names a summable per-event quantity. Ratios (accuracy,
// efficiency) are deliberately absent: they do not sum, so "the best window"
// is undefined for them. They ride the per-window stats block instead.
const (
	MetricFrags       = "frags"       // enemy kills
	MetricDeaths      = "deaths"      // times died — finds a player's WORST stretch
	MetricNetFrags    = "netFrags"    // kills - deaths
	MetricDamageGiven = "damageGiven" // to enemies
	MetricDamageTaken = "damageTaken" // from all sources
	MetricNetDamage   = "netDamage"   // given - taken
	MetricShots       = "shots"       // fires — activity, not reward
	MetricHits        = "hits"        // connects
)

// KnownHotWindowMetrics is the closed vocabulary, in the order the docs list
// it. An unknown value is rejected rather than silently matching nothing; the
// openapi enum is drift-pinned to this slice.
var KnownHotWindowMetrics = []string{
	MetricFrags, MetricDeaths, MetricNetFrags,
	MetricDamageGiven, MetricDamageTaken, MetricNetDamage,
	MetricShots, MetricHits,
}

const (
	defaultHotWindowMs    = 30000
	defaultHotWindowLimit = 10
	hotWindowMaxLimit     = 200
)

// HotWindowsOptions narrows and shapes a HotWindows query. Empty fields mean
// "default"; From/To are match-relative int32 ms (0 disables that bound),
// matching FragOptions / DamageOptions.
type HotWindowsOptions struct {
	Metric    string   // one of KnownHotWindowMetrics; "" → frags
	WindowMs  int32    // window length; <=0 → 30000
	Limit     int      // total windows returned; 0 → 10, <0 → uncapped
	PerPlayer int      // max windows from any ONE player; <=0 → uncapped
	Players   []string // restrict to these SUBJECT players (case-sensitive)
	Weapons   []string // restrict the SCORING events (case-insensitive)
	From      int32    // window start bound, int32 ms (0 = no bound)
	To        int32    // window end bound, int32 ms (0 = no bound)
	// Dmg is the damage family: "raw" | "bounded". It applies under EVERY
	// metric, not only the damage ones — the per-window stats block reports
	// damage whatever selected the window — and the resolved family is echoed
	// on the response envelope (HotWindowsView.Dmg).
	//
	// The VIEW default is raw and the REST default is bounded — mvd-api's
	// handleHotWindows substitutes "bounded" for an unset dmg, exactly as
	// handleDamage does, and falls back to raw when the demo has no bounded
	// family. So an in-process caller (WASM, qw-analyze) that leaves this
	// empty gets a DIFFERENT family than the same query over HTTP. Set it
	// explicitly if the two have to agree.
	Dmg string
	// Min drops windows scoring below it. Nil means "use the default of 1";
	// a pointer rather than an int because 0 is a MEANINGFUL value — keeping
	// zero-scoring windows is a coherent request for the net metrics — and an
	// int cannot tell "asked for 0" from "asked for nothing".
	Min *int
}

// HotWindowsView is the response: a FLAT list, sorted by score descending.
//
// Flat rather than grouped-by-player because the two views callers actually
// want — "the match's big moments" and "this player's best runs" — are the same
// list under two different caps, and grouping client-side is one reduce.
type HotWindowsView struct {
	TimeUnit TimeUnit `json:"timeUnit,omitempty"`

	// ScoredBy is the scoring rule, echoed ONCE for the whole response: it is
	// invariant by construction (one query, one rule), so a per-row copy would
	// repeat the same object up to `limit` times.
	//
	// It is not decoration. With a weapons filter, a window's `score` is a
	// SUBSET of the same-named stat on that row — metric=damageGiven&weapons=lg
	// gives score 445 beside a damageGiven of 650, because the filter scopes the
	// SCORING events while the stats block still describes everything that
	// happened in the window. Two numbers under one name is a trap, and this is
	// what tells them apart. It is also the only place the metric is echoed:
	// naming one concept twice invites the two copies to disagree.
	ScoredBy ScoringRule `json:"scoredBy"`

	// Dmg and BoundedMode describe the damage family the STATS BLOCK was
	// computed in, echoed on every response exactly as /damage echoes them —
	// because the stats block reports damage under every metric, not only the
	// damage ones. For a damage metric ScoredBy.Dmg carries the same value by
	// construction (one resolution, hotWindowFamily, feeds both); for
	// metric=frags this is the only place the family is named, and without it
	// `damageGiven` on a row was a number with no stated family. Absent only on
	// a demo with no damage stream, where measured.damage is false anyway.
	Dmg         string `json:"dmg,omitempty"`
	BoundedMode string `json:"boundedMode,omitempty"`

	WindowMs  int32 `json:"windowMs"`
	Limit     int   `json:"limit"`
	PerPlayer int   `json:"perPlayer"`

	// Measured says which sources this demo carries; see MeasuredSources. Not
	// omitempty — an absent marker would be the very ambiguity it removes.
	Measured MeasuredSources `json:"measured"`

	Windows []HotWindow `json:"windows"`
}

// HotWindow is one stretch, with a stats block recomputed over exactly its
// span. Times are match-relative int32 ms.
type HotWindow struct {
	Rank   int    `json:"rank"` // 1-based over the returned list
	Player string `json:"player"`
	Team   string `json:"team,omitempty"`
	Start  int32  `json:"start"`
	End    int32  `json:"end"`

	// Score is the metric total over [Start, End] — the value that selected
	// and ranked this window. The envelope's ScoredBy says exactly what
	// produced it.
	//
	// WITHOUT a weapons filter, Score EQUALS the same-named field of the stats
	// block below (netFrags = kills-deaths, netDamage = given-taken), on every
	// metric, exactly. A weapons filter is the only thing that separates them,
	// and it separates them one way: it scopes the SCORING events while the
	// stats block still describes the whole window, so Score is then a subset.
	Score int `json:"score"`

	IntervalStats
}

// ScoringRule is what produced a window's Score: the canonical metric, the
// weapon tokens the scoring events were restricted to (normalised, sorted), and
// — for a damage metric — the family it summed. That last field is the same
// value the envelope's Dmg carries (one resolution feeds both); it is repeated
// here because ScoredBy is the self-contained answer to "what is this number".
type ScoringRule struct {
	Metric  string   `json:"metric"`
	Weapons []string `json:"weapons,omitempty"`
	Dmg     string   `json:"dmg,omitempty"`
}

// scoreEvent is one timestamped contribution to a metric.
type scoreEvent struct {
	t int32
	v int
}

// HotWindows returns the top-scoring fixed-length windows across the match.
//
// Returns ErrUnavailable when the demo carries no source stream for the
// requested metric, ErrBoundedUnavailable for an explicit dmg=bounded on a
// demo with no bounded family, and ErrInvalidFilter for a bad metric or
// weapon token.
func HotWindows(r *result.Result, opts HotWindowsOptions) (*HotWindowsView, error) {
	metric, ok := canonicalMetric(opts.Metric)
	if !ok {
		return nil, fmt.Errorf("%w: unknown metric %q; valid: %s",
			ErrInvalidFilter, opts.Metric, strings.Join(KnownHotWindowMetrics, ", "))
	}
	if r == nil {
		return nil, ErrUnavailable
	}

	windowMs := opts.WindowMs
	if windowMs <= 0 {
		windowMs = defaultHotWindowMs
	}
	limit := opts.Limit
	if limit == 0 {
		limit = defaultHotWindowLimit
	}
	// Defence in depth, not the contract: mvd-api REJECTS limit > hotWindowMaxLimit
	// with a 400 (handlers.go hotWindowsMaxLimit, the v59 "no longer silently
	// clamped" ruling), and that handler is the real gate. Clamping here bounds
	// the response for in-process callers — WASM, qw-analyze — that have no
	// HTTP layer in front of them, rather than letting one query materialise an
	// unbounded stats block per candidate window.
	if limit > hotWindowMaxLimit {
		limit = hotWindowMaxLimit
	}
	min := 1
	if opts.Min != nil {
		min = *opts.Min
	}

	fam, err := hotWindowFamily(r, metric, opts.Dmg)
	if err != nil {
		return nil, err
	}
	if err := validateHotWindowWeapons(metric, opts.Weapons); err != nil {
		return nil, err
	}

	events, err := collectScoreEvents(r, metric, opts.Weapons, fam)
	if err != nil {
		return nil, err
	}

	lo, hi := hotWindowBounds(r, opts.From, opts.To)
	pf := newPlayerFilter(opts.Players)

	// Candidates per player, then one global ranking. Generating per player is
	// forced by the segmentation itself — a window belongs to whoever scored
	// in it — so neither cap costs extra work.
	names := make([]string, 0, len(events))
	for name := range events {
		if pf.accepts(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names) // never range a map into output

	var all []HotWindow
	for _, name := range names {
		for _, c := range topWindowsFor(events[name], windowMs, lo, hi, min) {
			all = append(all, HotWindow{Player: name, Start: c.start, End: c.end, Score: c.score})
		}
	}

	sortHotWindows(all)
	all = applyCaps(all, opts.PerPlayer, limit)

	teamOf := defaultNameToTeam(r)
	scoredBy := ScoringRule{Metric: metric, Weapons: normaliseWeapons(opts.Weapons)}
	if isDamageMetric(metric) {
		scoredBy.Dmg = fam
	}
	sb := newStatsBuilder(r, fam)
	for i := range all {
		w := &all[i]
		w.Rank = i + 1
		w.Team = teamOf[baseName(w.Player)]
		w.IntervalStats = sb.build(w.Player, statsSpan{start: w.Start, end: w.End, startInclusive: true})
	}
	if all == nil {
		all = []HotWindow{}
	}

	return &HotWindowsView{
		ScoredBy:    scoredBy,
		Dmg:         fam,
		BoundedMode: boundedModeOf(r),
		WindowMs:    windowMs,
		Limit:       limit,
		PerPlayer:   opts.PerPlayer,
		Measured:    sb.measured(),
		Windows:     all,
	}, nil
}

// canonicalMetric resolves a caller's metric case-insensitively to its
// canonical camelCase spelling. Comparing a lower-cased input against the
// vocabulary directly would reject every camelCase name — "netFrags" lowers to
// "netfrags", which is in no list.
func canonicalMetric(in string) (string, bool) {
	m := strings.TrimSpace(in)
	if m == "" {
		return MetricFrags, true
	}
	for _, k := range KnownHotWindowMetrics {
		if strings.EqualFold(k, m) {
			return k, true
		}
	}
	return "", false
}

func isDamageMetric(m string) bool {
	switch m {
	case MetricDamageGiven, MetricDamageTaken, MetricNetDamage:
		return true
	}
	return false
}

// hotWindowFamily resolves the damage family for a hot-windows query.
//
// It is resolved for EVERY metric, not only the damage ones, because the
// per-window stats block always reports damageGiven / damageTaken /
// damageByWeapon whatever selected the window — so the family is a property of
// the RESPONSE, not of the metric. Incident (adversarial review, 2026-08-01):
// this returned "" for a non-damage metric, statsBuilder read "" as raw, and
// the REST layer's dmg=bounded default was silently dropped for
// metric=frags — on 212260 the top frag window reported damageGiven 1676 where
// /damage over the same span reported 795, 2.1x apart, with nothing in the
// response naming which family produced it. An explicit dmg=bounded is now
// honoured (or rejected) under every metric.
//
// A demo with NO damage stream resolves to "" — there is no family and no
// damage to report — but only after the token itself has been validated, so a
// bogus dmg is still a client error. A damage METRIC on such a demo keeps the
// ErrUnavailable it always returned.
func hotWindowFamily(r *result.Result, metric, dmg string) (string, error) {
	fam, err := damageFamily(r, dmg)
	switch {
	case err == nil:
		return fam, nil
	case isDamageMetric(metric):
		return "", err
	case errors.Is(err, ErrBoundedUnavailable), !errors.Is(err, ErrUnavailable):
		// A bad token (ErrInvalidFilter) and an explicit dmg=bounded on a demo
		// that has no bounded family are both the caller's problem whatever the
		// metric is; only "this demo has no damage at all" is tolerated.
		return "", err
	default:
		return "", nil
	}
}

// damageFamily resolves and validates a damage family. Mirrors view.Damage:
// hasBounded is read from BoundedMode, the field that NAMES the state, not
// from the Dmg echo.
func damageFamily(r *result.Result, dmg string) (string, error) {
	fam := strings.ToLower(strings.TrimSpace(dmg))
	switch fam {
	case "", "raw":
		fam = "raw"
	case "bounded":
	case "both":
		return "", fmt.Errorf("%w: dmg=both is not a scoring family; use raw or bounded", ErrInvalidFilter)
	default:
		return "", fmt.Errorf("%w: unknown dmg %q; valid: raw, bounded", ErrInvalidFilter, dmg)
	}
	if r.Damage == nil {
		return "", ErrUnavailable
	}
	if fam == "bounded" && !hasBoundedFamily(r.Damage) {
		return "", ErrBoundedUnavailable
	}
	return fam, nil
}

// boundedModeOf is the demo's bounded-reconstruction state, echoed on the
// interval envelopes exactly as view.Damage echoes it: "standard", or
// "skipped:*" when the server mode made the reconstruction impossible. Empty
// when the demo carries no damage stream at all.
func boundedModeOf(r *result.Result) string {
	if r == nil || r.Damage == nil {
		return ""
	}
	return r.Damage.BoundedMode
}

// validateHotWindowWeapons checks the filter against the metric's OWN source
// vocabulary. The three sources genuinely differ — the frag log knows `hook`,
// `world` and `water`, the damage log knows `explobox`, `trigger` and `drown`,
// and the shot stream knows only what can be fired — so `weapons=lava` is
// meaningful on metric=deaths and nonsense on metric=shots. The error names
// the valid set, which is the discovery mechanism.
func validateHotWindowWeapons(metric string, weapons []string) error {
	if len(weapons) == 0 {
		return nil
	}
	switch {
	case isDamageMetric(metric):
		return validateWeapons(weapons, damageWeaponVocab)
	case metric == MetricShots || metric == MetricHits:
		return validateWeapons(weapons, shotWeaponVocab)
	default:
		return validateWeapons(weapons, fragWeaponVocab)
	}
}

// shotWeaponVocab is what can actually be fired (mvd-analytics/analyzer/
// shots.go). No environmental causes, no axe: the stream is built from
// CHAN_WEAPON fire sounds and LG beams.
var shotWeaponVocab = []string{"rl", "lg", "gl", "ssg", "sng", "ng", "sg"}

// weaponMatcher is the predicate form of weaponFilterSet (sections.go) — the
// same alias-expanded set every other weapons= filter uses, so the scoring
// events and the sibling endpoints can never disagree about what a token
// selects. An empty or all-blank filter matches everything.
func weaponMatcher(tokens []string) func(string) bool {
	set := weaponFilterSet(tokens)
	if len(set) == 0 {
		return func(string) bool { return true }
	}
	return func(w string) bool { return set[strings.ToLower(w)] }
}

// normaliseWeapons is the ScoringRule echo of a weapons filter: the same
// canonical tokens the matcher used, sorted so the echo is stable.
func normaliseWeapons(tokens []string) []string {
	out := normaliseTokens(tokens)
	sort.Strings(out)
	return out
}

// hotWindowBounds clamps the query to the match window, then to the caller's
// From/To. 0 means "no bound" on both, matching every sibling endpoint.
func hotWindowBounds(r *result.Result, from, to int32) (int32, int32) {
	var lo, hi int32
	if r.Streams != nil {
		lo, hi = r.Streams.Global.MatchStart, r.Streams.Global.MatchEnd
	}
	if from > 0 && from > lo {
		lo = from
	}
	if to > 0 && (hi == 0 || to < hi) {
		hi = to
	}
	return lo, hi
}

// collectScoreEvents builds the per-player timestamped contributions for a
// metric. Suicides and teamkills NEVER score — a teamkill is not a hot moment
// — though they do appear in the stats block, which is where the full
// narrative belongs.
//
// TELEFRAGS AND STOMPS SCORE UNDER EVERY METRIC THEY CONTRIBUTE TO, frag and
// damage alike, so that absent a weapons= filter a window's `score` equals the
// same-named field of its own stats block exactly. The earlier asymmetry —
// folded into the stats, excluded from the score — rested on the premise that
// a positional kill carries the 9999 wire SENTINEL, which would swamp any
// window it touched. That premise is wrong: PositionalKill.Bounded/.Damage are
// the analyzer's reconstructed values (victim armor + remaining health), and
// measured across the 42 cached demos the 82 positional kills run 0..298 with
// a median of 100 — ordinary rocket-sized numbers, not a sentinel. The
// asymmetry meant metric=damageGiven did not score damageGiven: a
// telefrag-only stretch produced no candidate at all while /damage reported
// positive damage for it, and score diverged from the same-named stat on 202
// of 70964 corpus windows.
//
// DamageByWeapon still EXCLUDES them, matching /damage's byWeapon: a positional
// kill carries no wire weapon. The pseudo-tokens "tele"/"stomp" (which
// DamageOptions.Weapons already uses to select them) filter them here, so
// weapons=tele scores telefrags alone.
func collectScoreEvents(r *result.Result, metric string, weapons []string, fam string) (map[string][]scoreEvent, error) {
	match := weaponMatcher(weapons)
	out := map[string][]scoreEvent{}
	add := func(name string, t int32, v int) {
		if name == "" || v == 0 {
			return
		}
		out[name] = append(out[name], scoreEvent{t: t, v: v})
	}

	switch metric {
	case MetricFrags, MetricDeaths, MetricNetFrags:
		if r.Frags == nil {
			return nil, ErrUnavailable
		}
		for _, f := range r.Frags.Frags {
			if !match(f.Weapon) {
				continue
			}
			kill := !f.IsSuicide && !f.IsTeamKill && !isGenericName(f.Killer)
			switch metric {
			case MetricFrags:
				if kill {
					add(f.Killer, f.Time, 1)
				}
			case MetricDeaths:
				add(f.Victim, f.Time, 1)
			case MetricNetFrags:
				if kill {
					add(f.Killer, f.Time, 1)
				}
				add(f.Victim, f.Time, -1)
			}
		}

	case MetricDamageGiven, MetricDamageTaken, MetricNetDamage:
		if r.Damage == nil {
			return nil, ErrUnavailable
		}
		for i := range r.Damage.Events {
			e := &r.Damage.Events[i]
			if !match(e.Weapon) {
				continue
			}
			v := damageValue(e, fam)
			if v <= 0 {
				continue
			}
			// Exactly view.Damage's rule (sections.go): a non-world
			// environmental hit still credits its player attacker, so IsEnv is
			// NOT part of this test. Diverging would make metric=damageGiven
			// disagree with the /damage endpoint on the same demo.
			enemy := e.Attacker != "world" && !e.IsSelf && !e.IsTeam
			switch metric {
			case MetricDamageGiven:
				if enemy {
					add(e.Attacker, e.Time, v)
				}
			case MetricDamageTaken:
				add(e.Victim, e.Time, v)
			case MetricNetDamage:
				if enemy {
					add(e.Attacker, e.Time, v)
				}
				add(e.Victim, e.Time, -v)
			}
		}

		// The positional-kill fold, scored on exactly the terms
		// statsBuilder.build folds it into the stats block: same gate
		// (hasBoundedFamily — a skipped:* demo folds nothing anywhere), same
		// per-family value, same three-way attacker classification. Keeping the
		// two in one shape is what makes score == stat hold.
		if hasBoundedFamily(r.Damage) {
			scorePositional := func(kills []result.PositionalKill, weapon string) {
				if !match(weapon) {
					return
				}
				for i := range kills {
					k := &kills[i]
					raw, bounded := positionalKillValues(*k)
					v := raw
					if fam == "bounded" {
						v = bounded
					}
					if v <= 0 {
						continue
					}
					enemy := positionalKillGiven(*k) == foldGivenEnemy
					switch metric {
					case MetricDamageGiven:
						if enemy {
							add(k.Attacker, k.Time, v)
						}
					case MetricDamageTaken:
						add(k.Victim, k.Time, v)
					case MetricNetDamage:
						if enemy {
							add(k.Attacker, k.Time, v)
						}
						add(k.Victim, k.Time, -v)
					}
				}
			}
			scorePositional(r.Damage.Telefrags, "tele")
			scorePositional(r.Damage.Stomps, "stomp")
		}

	case MetricShots, MetricHits:
		if r.Shots == nil {
			return nil, ErrUnavailable
		}
		for i := range r.Shots.Shots {
			s := &r.Shots.Shots[i]
			// No warmup check: the shot stream is match-gated at the source
			// (schema v50 removed Shot.Warmup).
			if !match(s.Weapon) {
				continue
			}
			if metric == MetricHits && !s.Hit {
				continue
			}
			add(s.Player, s.Time, 1)
		}
	}

	for name := range out {
		ev := out[name]
		sort.Slice(ev, func(i, j int) bool { return ev[i].t < ev[j].t })
		out[name] = coalesce(ev)
	}
	return out, nil
}

// coalesce folds same-millisecond contributions into one tick. A quad rocket
// hitting three players lands on one instant; leaving it as three ticks would
// let a window "start" between them, which is not a distinguishable moment.
//
// It allocates rather than compacting into ev[:1]. Compacting in place would
// rewrite the caller's backing array — folding [{1,1}{1,2}{2,5}] leaves the
// tail holding stale duplicates — which is invisible only for as long as the
// caller both owns the slice and never looks at it again. One allocation per
// player removes a precondition that no signature could state.
func coalesce(ev []scoreEvent) []scoreEvent {
	if len(ev) < 2 {
		return ev
	}
	out := make([]scoreEvent, 0, len(ev))
	out = append(out, ev[0])
	for _, e := range ev[1:] {
		if last := &out[len(out)-1]; last.t == e.t {
			last.v += e.v
			continue
		}
		out = append(out, e)
	}
	return out
}

// damageValue reads one hit in the requested family. A nil Bounded means
// "equal to Damage" — the common no-overkill case, NOT zero (result/damage.go).
func damageValue(e *result.DamageEntry, fam string) int {
	if fam == "bounded" && e.Bounded != nil {
		return *e.Bounded
	}
	return e.Damage
}

type windowCand struct {
	start, end int32
	score      int
	onEvent    bool // start coincides with a scoring event
}

// topWindowsFor returns one player's non-overlapping windows, best first.
//
// CANDIDATE STARTS are every event time t, every t+1, and every t-windowMs.
// Anchoring only at event times is optimal for non-negative values, but these
// metrics are signed — netFrags emits -1 per death, netDamage -damage — and
// with negatives the usual argument fails: sliding a window right until its
// left edge meets an event can pull a NEGATIVE event onto the right edge.
// Concretely, with windowMs=10 and events (t=1,+10),(t=11,-100), the window
// [0,10] scores +10 while the only event-anchored candidate [1,11] scores -90
// and is dropped, so the true optimum is never seen.
//
// The sum over a fixed-width window is piecewise constant in the start position
// s, and event k contributes over the CLOSED interval s ∈ [t_k-windowMs, t_k].
// So the breakpoints are s = t_k-windowMs, where k enters, and s = t_k+1, where
// it leaves — NOT s = t_k, which is the last position where k still counts. The
// +1 is the whole point: the maximal constant piece that begins just after a
// negative event drops off the left edge starts at t_k+1, and anchoring at t_k
// alone never visits it. With ev=(1,-7),(5,+2),(7,-3) and windowMs=4 that piece
// is [2,6] scoring +2, and dropping it returned NO window at all. Measured
// against a brute force over every integer start, the two-family version missed
// the optimum on 22098 of 400000 random signed cases; with t+1, 0.
//
// Evaluating all three families therefore visits every distinct value of the
// sum, and the maximum found IS the global maximum.
//
// lo/hi bound where a window may START. The window itself is always exactly
// windowMs long and its score covers all of [start, start+windowMs] — clipping
// the scoring at hi while reporting an unclipped span would make `score`
// disagree with the stats block computed over the same span.
//
// A from > to query arrives here as lo > hi, which yields no candidate starts
// and so an empty list under a 200. That is deliberate: rejecting an inverted
// range is the HTTP layer's job, and view callers (WASM, CLI) get the honest
// answer that nothing can be in an empty range.
//
// Selection is GREEDY by score, not weighted interval scheduling: the
// best-scoring candidate is kept, anything touching it is suppressed, repeat.
// That is deterministic and cheap but not a maximal-total set — with
// windowMs=10 and (0,+4),(5,+5),(11,+6),(16,+3) it keeps only [5,15]=11 where
// [0,10]=9 plus [11,21]=9 would total 18. "Top N" therefore means "the best,
// then the best of what does not touch it", which is what a highlight list
// wants.
func topWindowsFor(ev []scoreEvent, windowMs, lo, hi int32, min int) []windowCand {
	if len(ev) == 0 || windowMs <= 0 {
		return nil
	}
	// Candidate starts, deduped and sorted, restricted to the anchor bounds.
	seen := make(map[int32]bool, len(ev)*2)
	starts := make([]int32, 0, len(ev)*2)
	addStart := func(t int32, onEvent bool) {
		if t < lo || (hi > 0 && t > hi) {
			return
		}
		if was, dup := seen[t]; dup {
			seen[t] = was || onEvent
			return
		}
		seen[t] = onEvent
		starts = append(starts, t)
	}
	// lo is itself a candidate: the score is piecewise constant in the start
	// position with breakpoints at t and t-windowMs, so over the domain
	// [lo, hi] the optimum sits at a breakpoint OR at the domain's own left
	// edge. Dropping a t-windowMs anchor that falls below lo, rather than
	// clamping to lo, loses exactly that case — with events (t=1,+1),(t=11,-1)
	// and windowMs=10 the only positive window is [0,10], whose start is lo.
	addStart(lo, false)
	for _, e := range ev {
		addStart(e.t, true)
		if e.t < math.MaxInt32 {
			addStart(e.t+1, false)
		}
		// int64 so a huge windowMs cannot wrap the subtraction.
		if v := int64(e.t) - int64(windowMs); v >= int64(lo) {
			addStart(int32(v), false)
		}
	}
	if len(starts) == 0 {
		return nil
	}
	sort.Slice(starts, func(a, b int) bool { return starts[a] < starts[b] })

	// Two monotone pointers over the sorted events: lo end drops events that
	// fall behind the window, hi end admits those that fall inside it. Both
	// only move forward because `starts` is sorted.
	var cands []windowCand
	head, tail, sum := 0, 0, 0
	for _, st := range starts {
		// Clamp the end BEFORE scoring, not on the way into the int32 field.
		// windowMs reaches view unclamped (the HTTP layer accepts any
		// 0..MaxInt32), so windowMs=MaxInt32 with a first event at t=1000 used
		// to narrow to end=-2147482649: End < Start, a negative durationMs and
		// an overlap predicate comparing garbage. Clamping here keeps `score`
		// over exactly the span that is reported, which is the invariant the
		// stats block is checked against.
		endT := int64(st) + int64(windowMs)
		if endT > math.MaxInt32 {
			endT = math.MaxInt32
		}
		for tail < len(ev) && int64(ev[tail].t) <= endT {
			sum += ev[tail].v
			tail++
		}
		for head < tail && ev[head].t < st {
			sum -= ev[head].v
			head++
		}
		if sum >= min {
			cands = append(cands, windowCand{start: st, end: int32(endT), score: sum, onEvent: seen[st]})
		}
	}
	if len(cands) == 0 {
		return nil
	}
	sort.Slice(cands, func(a, b int) bool {
		if cands[a].score != cands[b].score {
			return cands[a].score > cands[b].score
		}
		// Among equal scores prefer a start that IS a scoring event. The
		// t-windowMs anchors exist only so signed metrics find their optimum;
		// reporting a run as beginning at an arbitrary offset before its first
		// kill would be true but useless. Sliding the start onto the first
		// event afterwards is NOT safe — for signed metrics it can pull a
		// negative onto the right edge — so the preference is expressed here.
		if cands[a].onEvent != cands[b].onEvent {
			return cands[a].onEvent
		}
		return cands[a].start < cands[b].start
	})
	var out []windowCand
	for _, c := range cands {
		clash := false
		for _, k := range out {
			// TOUCHING counts as overlapping. Spans are closed at both ends
			// for scoring and for the stats block, so [0,10] and [10,20] share
			// the instant 10 — a kill there would be claimed by both windows,
			// which is exactly the duplication non-overlap exists to prevent.
			if c.start <= k.end && c.end >= k.start {
				clash = true
				break
			}
		}
		if !clash {
			out = append(out, c)
		}
	}
	return out
}

// sortHotWindows is the total, integer ranking: score desc, then the shorter
// and earlier window, then player name. Total so the output is stable.
func sortHotWindows(w []HotWindow) {
	sort.Slice(w, func(a, b int) bool {
		if w[a].Score != w[b].Score {
			return w[a].Score > w[b].Score
		}
		if w[a].Start != w[b].Start {
			return w[a].Start < w[b].Start
		}
		if w[a].End != w[b].End {
			return w[a].End < w[b].End
		}
		return w[a].Player < w[b].Player
	})
}

// applyCaps enforces perPlayer BEFORE limit. The order is what makes
// perPlayer=3&limit=10 mean "the top 10, with at most 3 from anyone" rather
// than "the top 3 of the first three players".
func applyCaps(w []HotWindow, perPlayer, limit int) []HotWindow {
	if perPlayer > 0 {
		seen := map[string]int{}
		kept := w[:0]
		for _, x := range w {
			if seen[x.Player] >= perPlayer {
				continue
			}
			seen[x.Player]++
			kept = append(kept, x)
		}
		w = kept
	}
	if limit > 0 && len(w) > limit {
		w = w[:limit]
	}
	return w
}
