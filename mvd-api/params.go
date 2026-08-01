package main

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/mvd-analyzer/mvd-analytics/view"
)

// ciGet returns a query value by case-insensitive key. The documented
// canonical spelling is camelCase (windowMs, minDwellMs, includeTeam), but
// windowms / WindowMs / etc. resolve too, so consumers never trip on the
// casing of a parameter name. The exact-case hit is preferred; otherwise
// the first key that matches case-insensitively wins.
func ciGet(q url.Values, key string) string {
	if v := q.Get(key); v != "" {
		return v
	}
	lk := strings.ToLower(key)
	for k, vs := range q {
		if len(vs) > 0 && strings.ToLower(k) == lk {
			return vs[0]
		}
	}
	return ""
}

// parseCSV splits a comma-separated query parameter, trimming spaces
// and dropping empty entries.
func parseCSV(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseInt parses a query-string integer. Empty → default. A non-empty hint
// is appended in parentheses, the same shape Ms uses for its "(integer
// milliseconds)" tail — a bare `invalid min="abc"` said nothing about the
// unit or the accepted range, which is exactly what a caller who typed it
// wrong needs.
func parseInt(q url.Values, key string, defaultVal int, hint string) (int, error) {
	v := ciGet(q, key)
	if v == "" {
		return defaultVal, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		if hint != "" {
			return 0, fmt.Errorf("invalid %s=%q (%s)", key, v, hint)
		}
		return 0, fmt.Errorf("invalid %s=%q", key, v)
	}
	return n, nil
}

// parseBool parses 0/1 or true/false. Empty → false.
func parseBool(q url.Values, key string) bool {
	switch strings.ToLower(ciGet(q, key)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// parseEnum reads a closed-vocabulary string param (loc/layout/dmg/regions).
// The raw value is TrimSpace+lowercased, then: empty → def (the param's absent
// default); a spelling present in canon → its canonical value (canon folds
// aliases, e.g. "li"→"index"); anything else → an error "invalid <key>=%q
// (<wantMsg>)" carrying the ORIGINAL-case value. One mechanism for the four
// enum params; each qp accessor pins its own canon table + wantMsg.
func parseEnum(q url.Values, key, def string, canon map[string]string, wantMsg string) (string, error) {
	if v := strings.ToLower(strings.TrimSpace(ciGet(q, key))); v == "" {
		return def, nil
	} else if c, ok := canon[v]; ok {
		return c, nil
	}
	return "", fmt.Errorf("invalid %s=%q (%s)", key, ciGet(q, key), wantMsg)
}

// The canon tables for the four enum params. Keys are the accepted lower-case
// spellings (aliases included); values are the canonical token each resolves
// to. The absent default is passed separately (parseEnum's def) so it need not
// appear here.
var (
	// loc: name (resolved loc names, the default) vs index (raw LocTable
	// indices, decode via /loc-table).
	locCanon = map[string]string{"name": "name", "names": "name", "index": "index", "indices": "index", "li": "index"}
	// layout: column-major ColumnarBuckets (default) vs the bucket-major
	// BucketsView.
	layoutCanon = map[string]string{"column": "column", "columnar": "column", "row": "row"}
	// dmg: raw | bounded | both (empty → "", the handler resolves the default
	// to "bounded" for both summary and full-log requests).
	dmgCanon = map[string]string{"raw": "raw", "bounded": "bounded", "both": "both"}
	// dmg on the interval endpoints (/hot-windows, /lives): raw | bounded only.
	// `both` is a SHAPE — raw fields plus a parallel bounded nest — and there
	// is no such shape here: one interval stats block carries one set of
	// damage numbers, and the scoring metrics need a single family to rank on.
	// Rejecting it at the boundary is what makes the two-value enum in
	// openapi.yaml true for EVERY metric; leaving it to the view rejected it
	// only when a damage family was actually resolved, so `dmg=both` 400d with
	// metric=damageGiven and silently 200d (ignored) with metric=frags.
	dmgFamilyCanon = map[string]string{"raw": "raw", "bounded": "bounded"}
	// regions: full (default; the full ControlRegion list with polygon Points)
	// | summary (name/locs/centroids kept, Points stripped) | none (list omitted).
	regionsCanon = map[string]string{"full": "full", "summary": "summary", "none": "none"}
	// metric: what a hot window is ranked by. Built from view's own vocabulary
	// so the two cannot drift, and accepted case-insensitively — the canonical
	// spellings are camelCase, which a naive ToLower would never match.
	metricCanon = buildMetricCanon()
)

func buildMetricCanon() map[string]string {
	m := make(map[string]string, len(view.KnownHotWindowMetrics))
	for _, k := range view.KnownHotWindowMetrics {
		m[strings.ToLower(k)] = k
	}
	return m
}

// parseReducers parses a comma-separated list of "field=name" pairs.
// Empty → nil. Malformed → error.
func parseReducers(v string) (map[string]string, error) {
	if v == "" {
		return nil, nil
	}
	out := map[string]string{}
	for _, kv := range strings.Split(v, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 || eq == len(kv)-1 {
			return nil, fmt.Errorf("invalid reducer pair %q (want 'field=name')", kv)
		}
		out[strings.TrimSpace(kv[:eq])] = strings.TrimSpace(kv[eq+1:])
	}
	return out, nil
}

// qp is an error-accumulating reader over a request's url.Values. Each
// accessor no-ops once an error has been recorded, so a handler does all
// its reads into the view-options struct and then checks Err() once — the
// first malformed param wins, exactly as the old sequential parse-then-
// check chain did. Keys resolve case-insensitively (the accessors funnel
// through the same ciGet/parse* helpers used elsewhere), so the field
// order of the options literal determines which of several bad params is
// reported; keep it matching the historical read order.
type qp struct {
	q    url.Values
	err  error
	seen map[string]bool
}

func newQP(q url.Values) *qp { return &qp{q: q, seen: map[string]bool{}} }

// globalQueryAllow lists query keys accepted on every request regardless of
// the handler. `label` is the non-secret traffic-source tag requestLabel
// (middleware.go) reads off any request in no-auth mode; it is never a
// handler-consumed param, so it must be whitelisted here or Unknown() would
// reject it everywhere.
var globalQueryAllow = map[string]bool{"label": true}

// mark records that a query key (lowercased) is consumed by this handler, so
// Unknown() does not flag it. Accessors call it for every key they read —
// before any error short-circuit — so the accepted-key set is complete even
// when an earlier param already failed.
func (p *qp) mark(keys ...string) {
	for _, k := range keys {
		p.seen[strings.ToLower(k)] = true
	}
}

// Err returns the first param-read error, or nil. Handlers pass it to
// writeInvalidParam for the shared 400 invalid_param tail.
func (p *qp) Err() error { return p.err }

// Str marks key as consumed and returns its case-insensitive value (empty
// when absent). It cannot fail — for plain string params (map/mode/source/…)
// that were formerly read via a raw ciGet bypass, so the key is now recorded
// for Unknown().
func (p *qp) Str(key string) string {
	p.mark(key)
	return ciGet(p.q, key)
}

// Accept marks keys as consumed without reading them — for accepted-but-
// ignored legacy params (e.g. `nails` on /shots) and zero-param endpoints
// that want a specific param whitelisted. One mechanism, no separate helper.
func (p *qp) Accept(keys ...string) { p.mark(keys...) }

// Present reports whether key appears in the query string with a non-empty
// value (case-insensitively, matching ciGet's resolution). It does NOT mark
// the key as consumed — only the value accessor that reads it does — so a
// handler uses Present purely to tell an explicit value apart from an absent
// one (an explicit limit=0 / windowMs=0 vs an omitted param) alongside the
// normal accessor, which still parses the value and records the key.
func (p *qp) Present(key string) bool { return ciGet(p.q, key) != "" }

// Unknown reports the first query key (sorted) whose lowercase form was not
// consumed by any accessor and is not globally allowed, naming the offender
// and the sorted accepted vocabulary. Returns nil when every key was
// recognised. Handlers wire it as `if writeUnknownParam(w, p.Unknown())`.
func (p *qp) Unknown() error {
	var unknown []string
	for k := range p.q {
		lk := strings.ToLower(k)
		if p.seen[lk] || globalQueryAllow[lk] {
			continue
		}
		unknown = append(unknown, lk)
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	accepted := map[string]bool{}
	for k := range p.seen {
		accepted[k] = true
	}
	for k := range globalQueryAllow {
		accepted[k] = true
	}
	acc := make([]string, 0, len(accepted))
	for k := range accepted {
		acc = append(acc, k)
	}
	sort.Strings(acc)
	return fmt.Errorf("unknown query parameter %q; accepted: %s", unknown[0], strings.Join(acc, ", "))
}

// Ms reads a match-relative integer-millisecond bound (from/to/time; empty →
// def). In the v57 pure-ms model every time-valued query param is integer
// milliseconds: a non-integer value like "10.5" is rejected with a
// "(integer milliseconds)" hint — the deliberate v56→v57 migration tripwire
// that catches old float-seconds callers loudly instead of misfiltering. The
// value must be >= 0 and fit int32. No-op after a prior error.
func (p *qp) Ms(key string, def int32) int32 {
	p.mark(key)
	if p.err != nil {
		return def
	}
	v := ciGet(p.q, key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		p.err = fmt.Errorf("invalid %s=%q (integer milliseconds)", key, v)
		return def
	}
	if n < 0 {
		p.err = fmt.Errorf("invalid %s=%q (must be >= 0)", key, v)
		return def
	}
	return int32(n)
}

// Int reads an integer param (empty → def). No-op after a prior error.
func (p *qp) Int(key string, def int) int { return p.IntHint(key, def, "") }

// IntHint is Int with a unit / accepted-range hint appended to the parse
// error, matching Ms's "(integer milliseconds)" tail. Use it wherever the
// param's unit is not obvious from its name (windowMs is ms, minScore is a
// score) so a malformed value tells the caller what a good one looks like.
func (p *qp) IntHint(key string, def int, hint string) int {
	p.mark(key)
	if p.err != nil {
		return def
	}
	v, err := parseInt(p.q, key, def, hint)
	if err != nil {
		p.err = err
	}
	return v
}

// CSV reads a comma-separated param. It cannot fail, so it never sets err.
func (p *qp) CSV(key string) []string {
	p.mark(key)
	return parseCSV(ciGet(p.q, key))
}

// CSVAny reads the first of several aliased comma-separated params that
// has a non-empty value (earlier keys win). Exists for the
// weapons/weapon rename (phase 16.2): the canonical name is listed
// first, the legacy alias after, and a request carrying both gets the
// canonical one.
func (p *qp) CSVAny(keys ...string) []string {
	p.mark(keys...)
	for _, k := range keys {
		if vals := parseCSV(ciGet(p.q, k)); len(vals) > 0 {
			return vals
		}
	}
	return nil
}

// Bool reads a 0/1|true/false param. It cannot fail, so it never sets err.
func (p *qp) Bool(key string) bool {
	p.mark(key)
	return parseBool(p.q, key)
}

// enum reads a closed-vocabulary string param through parseEnum: it marks the
// key, no-ops (returning def) after a prior error, and records the first parse
// error. The four enum accessors below are one-liners over it.
func (p *qp) enum(key, def string, canon map[string]string, wantMsg string) string {
	p.mark(key)
	if p.err != nil {
		return def
	}
	v, err := parseEnum(p.q, key, def, canon, wantMsg)
	if err != nil {
		p.err = err
	}
	return v
}

// LocIndex reads ?loc=name|index (empty → false, resolved names). No-op after
// a prior error.
func (p *qp) LocIndex() bool {
	return p.enum("loc", "name", locCanon, "want 'name' or 'index'") == "index"
}

// Layout reads ?layout=row|column (empty → "column"). No-op after error.
func (p *qp) Layout() string {
	return p.enum("layout", "column", layoutCanon, "want 'row' or 'column'")
}

// Dmg reads ?dmg=raw|bounded|both (empty → "", the handler resolves the
// default to "bounded"). No-op after a prior error.
func (p *qp) Dmg() string {
	return p.enum("dmg", "", dmgCanon, "want 'raw', 'bounded' or 'both'")
}

// DmgFamily reads ?dmg=raw|bounded for the interval endpoints (empty → "",
// the handler resolves the default to "bounded"). `both` is rejected here for
// every metric — see dmgFamilyCanon. No-op after a prior error.
func (p *qp) DmgFamily() string {
	return p.enum("dmg", "", dmgFamilyCanon, "want 'raw' or 'bounded'; 'both' is not a shape this endpoint has")
}

// IntPtr reads an optional integer param, returning nil when it is absent so
// a caller can tell "asked for 0" from "asked for nothing". hint is the
// IntHint tail for a malformed value. No-op after a prior error.
func (p *qp) IntPtr(key, hint string) *int {
	if !p.Present(key) {
		p.mark(key)
		return nil
	}
	v := p.IntHint(key, 0, hint)
	return &v
}

// Metric reads ?metric=frags|deaths|netFrags|... (empty → "frags"), the
// hot-windows ranking quantity. No-op after a prior error.
func (p *qp) Metric() string {
	return p.enum("metric", view.MetricFrags, metricCanon,
		"want one of: "+strings.Join(view.KnownHotWindowMetrics, ", "))
}

// Regions reads ?regions=full|summary|none (empty → "full"). No-op after a
// prior error.
func (p *qp) Regions() string {
	return p.enum("regions", "full", regionsCanon, "want 'full', 'summary' or 'none'")
}

// Reducers reads the "field=name,..." reducer-override param. No-op after
// a prior error.
func (p *qp) Reducers(key string) map[string]string {
	p.mark(key)
	if p.err != nil {
		return nil
	}
	v, err := parseReducers(ciGet(p.q, key))
	if err != nil {
		p.err = err
	}
	return v
}
