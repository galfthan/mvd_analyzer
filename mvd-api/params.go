package main

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
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

// parseInt parses a query-string integer. Empty → default.
func parseInt(q url.Values, key string, defaultVal int) (int, error) {
	v := ciGet(q, key)
	if v == "" {
		return defaultVal, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
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

// parseLocIndex reads ?loc=name|index. Empty or "name" → false
// (resolved loc names, the default); "index" → true (raw LocTable
// indices, decode via /loc-table). Any other value is an error.
func parseLocIndex(q url.Values) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(ciGet(q, "loc"))) {
	case "", "name", "names":
		return false, nil
	case "index", "indices", "li":
		return true, nil
	default:
		return false, fmt.Errorf("invalid loc=%q (want 'name' or 'index')", ciGet(q, "loc"))
	}
}

// parseLayout reads ?layout=row|column. Empty → "column" (the compact
// column-major ColumnarBuckets, the default); "row" → the bucket-major
// BucketsView. Any other value is an error.
func parseLayout(q url.Values) (string, error) {
	switch strings.ToLower(strings.TrimSpace(ciGet(q, "layout"))) {
	case "", "column", "columnar":
		return "column", nil
	case "row":
		return "row", nil
	default:
		return "", fmt.Errorf("invalid layout=%q (want 'row' or 'column')", ciGet(q, "layout"))
	}
}

// parseDmg reads ?dmg=raw|bounded|both. Empty → "" (the handler resolves the
// default: "bounded", for both summary and full-log requests). Any
// other value is an error.
func parseDmg(q url.Values) (string, error) {
	v := strings.ToLower(strings.TrimSpace(ciGet(q, "dmg")))
	switch v {
	case "", "raw", "bounded", "both":
		return v, nil
	}
	return "", fmt.Errorf("invalid dmg=%q (want 'raw', 'bounded' or 'both')", ciGet(q, "dmg"))
}

// parseRegions reads ?regions=full|summary|none. Empty → "full" (the default,
// backward-compatible: the full ControlRegion list including polygon Points).
// "summary" keeps each region's name/locs/centroids but strips its Points
// polygon; "none" omits the regions list entirely. Any other value is an error.
func parseRegions(q url.Values) (string, error) {
	switch strings.ToLower(strings.TrimSpace(ciGet(q, "regions"))) {
	case "", "full":
		return "full", nil
	case "summary":
		return "summary", nil
	case "none":
		return "none", nil
	}
	return "", fmt.Errorf("invalid regions=%q (want 'full', 'summary' or 'none')", ciGet(q, "regions"))
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
func (p *qp) Int(key string, def int) int {
	p.mark(key)
	if p.err != nil {
		return def
	}
	v, err := parseInt(p.q, key, def)
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

// LocIndex reads ?loc=name|index. No-op after a prior error.
func (p *qp) LocIndex() bool {
	p.mark("loc")
	if p.err != nil {
		return false
	}
	v, err := parseLocIndex(p.q)
	if err != nil {
		p.err = err
	}
	return v
}

// Layout reads ?layout=row|column (empty → "column"). No-op after error.
func (p *qp) Layout() string {
	p.mark("layout")
	if p.err != nil {
		return "column"
	}
	v, err := parseLayout(p.q)
	if err != nil {
		p.err = err
	}
	return v
}

// Dmg reads ?dmg=raw|bounded|both (empty → "", the handler resolves the
// default to "bounded"). No-op after a prior error.
func (p *qp) Dmg() string {
	p.mark("dmg")
	if p.err != nil {
		return ""
	}
	v, err := parseDmg(p.q)
	if err != nil {
		p.err = err
	}
	return v
}

// Regions reads ?regions=full|summary|none (empty → "full"). No-op after a
// prior error.
func (p *qp) Regions() string {
	p.mark("regions")
	if p.err != nil {
		return "full"
	}
	v, err := parseRegions(p.q)
	if err != nil {
		p.err = err
	}
	return v
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
