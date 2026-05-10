package view

import (
	"fmt"
	"math"

	"github.com/mvd-analyzer/qwanalytics/result"
)

// BucketsOptions controls the windowing, field selection, and reducer
// override for a single Buckets call. Every field is optional; the
// zero value asks for "every standard field, default reducers, 50 ms
// windows over the whole match."
type BucketsOptions struct {
	WindowMs    int               // 50 if zero
	StartTime   float64           // match start if zero
	EndTime     float64           // match end if zero
	Players     []string          // all if empty
	Fields      []string          // AllStandardFields if empty
	Reducers    map[string]string // per-field overrides; merged with DefaultReducers
	IncludeTeam bool              // emit per-team aggregates on each bucket
}

// BucketsView is the result of a Buckets call. WindowMs echoes back
// the resolved window size (so callers reading 0 above can still see
// what they got); Buckets is sorted by T ascending.
type BucketsView struct {
	WindowMs int          `json:"windowMs"`
	Buckets  []ViewBucket `json:"buckets"`
}

// ViewBucket is the clean per-player map shape (D13 in the plan).
// Distinct from result.HighResBucket; the WASM bridge's
// getDefaultBuckets shim transforms a BucketsView into the v6 shape
// via ToLegacyHighResBuckets in legacy.go.
//
// Players maps player name → field code → reduced value. Field codes
// match the canonical vocabulary in fields.go. Values' Go types
// depend on the reducer used: numeric reducers (mean/min/max) emit
// float64; "last"/"first" preserve the underlying stream type;
// boolean reducers (held-any/majority/any) emit bool.
//
// Team, when populated, is keyed by team name and carries the same
// IncludeTeam aggregate counters that v6 stamped on every bucket.
type ViewBucket struct {
	T       float64                       `json:"t"`
	Players map[string]map[string]any     `json:"p"`
	Team    map[string]map[string]int     `json:"team,omitempty"`
	Partial bool                          `json:"partial,omitempty"`
}

// Buckets walks the streams in r and emits one ViewBucket per window
// across [StartTime, EndTime). The final bucket is marked Partial when
// the window doesn't divide evenly.
func Buckets(r *result.Result, opts BucketsOptions) (*BucketsView, error) {
	if r == nil || r.Streams == nil {
		return &BucketsView{WindowMs: bucketWindowMsOrDefault(opts.WindowMs)}, nil
	}
	windowMs, windowSec, err := resolveWindow(opts.WindowMs)
	if err != nil {
		return nil, err
	}

	fields := opts.Fields
	if len(fields) == 0 {
		fields = AllStandardFields
	}
	if err := validateFields(fields); err != nil {
		return nil, err
	}
	reducers := make(map[string]Reducer, len(fields))
	for _, f := range fields {
		red, err := resolveReducerName(f, opts.Reducers)
		if err != nil {
			return nil, err
		}
		reducers[f] = red
	}

	start := opts.StartTime
	end := opts.EndTime
	if end == 0 {
		end = r.Streams.Global.MatchEnd
	}
	if start == 0 {
		start = r.Streams.Global.MatchStart
	}
	if end <= start {
		return &BucketsView{WindowMs: windowMs, Buckets: nil}, nil
	}

	playerFilter := newPlayerFilter(opts.Players)
	playerStreams := selectPlayers(r.Streams.Players, playerFilter)

	totalSpan := end - start
	full := int(math.Floor((totalSpan + 1e-12) / windowSec))
	remainder := totalSpan - float64(full)*windowSec
	bucketCount := full
	hasPartial := remainder > 1e-9
	if hasPartial {
		bucketCount++
	}
	if bucketCount == 0 {
		return &BucketsView{WindowMs: windowMs}, nil
	}

	buckets := make([]ViewBucket, bucketCount)
	for i := 0; i < bucketCount; i++ {
		bStart := start + float64(i)*windowSec
		bEnd := bStart + windowSec
		if hasPartial && i == bucketCount-1 {
			bEnd = end
		}
		bucket := ViewBucket{
			T:       bStart,
			Players: make(map[string]map[string]any),
		}
		if hasPartial && i == bucketCount-1 {
			bucket.Partial = true
		}

		for _, p := range playerStreams {
			if !playerActiveInWindow(p, bStart, bEnd) {
				continue
			}
			pdata := reducePlayer(p, fields, reducers, bStart, bEnd)
			if pdata != nil {
				bucket.Players[p.Name] = pdata
			}
		}

		if opts.IncludeTeam {
			bucket.Team = aggregateTeams(playerStreams, bucket.Players)
		}
		buckets[i] = bucket
	}

	return &BucketsView{WindowMs: windowMs, Buckets: buckets}, nil
}

// playerActiveInWindow returns true if the player should appear in a
// bucket spanning [bStart, bEnd). Mirrors v6's position-driven
// sampler: a player gets a bucket entry only while they are alive.
//
// "Alive" is determined from the spawn / death streams:
//
//   - If both streams are empty and the player has no position track,
//     the player has no concrete activity signal — treat as active to
//     keep synthetic test fixtures working without spawn/death markers.
//   - If the spawn / death streams or position track are populated,
//     consult them: a player is alive iff their latest spawn before
//     bEnd is later than their latest death before bEnd, OR a spawn
//     happens in [bStart, bEnd) (mid-window respawn).
//   - If the player has positions but no spawn/death streams (e.g.,
//     already alive at match start in a demo where parser never
//     emitted a synthetic SpawnEvent), fall back to position presence.
func playerActiveInWindow(p result.PlayerStream, bStart, bEnd float64) bool {
	hasSpawnDeath := len(p.Spawns) > 0 || len(p.Deaths) > 0
	hasPositions := p.Position != nil && len(p.Position.T) > 0

	if !hasSpawnDeath && !hasPositions {
		// No concrete liveness signal — assume active (test fixtures).
		return true
	}

	if hasSpawnDeath {
		latestKind := ""
		latestT := -1.0
		for _, t := range p.Spawns {
			if t >= bStart && t < bEnd {
				return true // spawned inside the window
			}
			if t < bStart && t > latestT {
				latestT = t
				latestKind = "spawn"
			}
			if t >= bEnd {
				break
			}
		}
		for _, t := range p.Deaths {
			if t < bStart && t > latestT {
				latestT = t
				latestKind = "death"
			}
			if t >= bEnd {
				break
			}
		}
		if latestKind == "spawn" {
			return true
		}
		if latestKind == "death" {
			return false
		}
		// No spawn/death before bStart — fall through to position check.
	}

	return positionTouchesWindow(p.Position, bStart, bEnd)
}

func positionTouchesWindow(pt *result.PositionTrack, bStart, bEnd float64) bool {
	if pt == nil {
		return false
	}
	const fudge = 0.1 // slightly more than one 50 ms bucket
	for _, t := range pt.T {
		ft := float64(t)
		if ft >= bEnd {
			break
		}
		if ft >= bStart-fudge {
			return true
		}
	}
	return false
}

func bucketWindowMsOrDefault(ms int) int {
	if ms <= 0 {
		return 50
	}
	return ms
}

func resolveWindow(ms int) (int, float64, error) {
	if ms < 0 {
		return 0, 0, fmt.Errorf("WindowMs must be > 0, got %d", ms)
	}
	if ms == 0 {
		ms = 50
	}
	return ms, float64(ms) / 1000.0, nil
}

// playerFilter enforces an opt-in name filter. nil pointer → accept
// everyone; otherwise the set is checked.
type playerFilter struct{ allow map[string]bool }

func newPlayerFilter(names []string) *playerFilter {
	if len(names) == 0 {
		return nil
	}
	pf := &playerFilter{allow: make(map[string]bool, len(names))}
	for _, n := range names {
		pf.allow[n] = true
	}
	return pf
}

func (pf *playerFilter) accepts(name string) bool {
	if pf == nil {
		return true
	}
	return pf.allow[name]
}

func selectPlayers(all []result.PlayerStream, pf *playerFilter) []result.PlayerStream {
	if pf == nil {
		return all
	}
	out := make([]result.PlayerStream, 0, len(all))
	for _, p := range all {
		if pf.accepts(p.Name) {
			out = append(out, p)
		}
	}
	return out
}

// reducePlayer collects samples for each requested field over [bStart,
// bEnd) and runs the reducer. Returns nil when no field produced a
// non-nil value (i.e. player wasn't active in this window).
func reducePlayer(
	p result.PlayerStream,
	fields []string,
	reducers map[string]Reducer,
	bStart, bEnd float64,
) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		samples := collectSamples(p, f, bStart, bEnd)
		val := reducers[f].Apply(samples)
		if val == nil {
			continue
		}
		out[f] = val
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// collectSamples walks the appropriate stream of p and returns Samples
// suitable for reduction. Carry-forward semantics: for change streams,
// the last entry with T <= bStart is included so reducers like "last"
// behave as "value at end of window even when nothing changed inside."
func collectSamples(p result.PlayerStream, field string, bStart, bEnd float64) []Sample {
	kind, ok := FieldKindFor(field)
	if !ok {
		return nil
	}
	switch kind {
	case KindChangeI8:
		return changeI8Samples(p, field, bStart, bEnd)
	case KindChangeI16:
		return changeI16Samples(p, field, bStart, bEnd)
	case KindChangeStr:
		return changeStrSamples(p, field, bStart, bEnd)
	case KindInterval:
		return intervalSamples(p, field, bStart, bEnd)
	case KindPosition:
		return positionSamples(p, bStart, bEnd)
	case KindEventList:
		return eventListSamples(p, field, bStart, bEnd)
	}
	return nil
}

func changeI8Samples(p result.PlayerStream, field string, bStart, bEnd float64) []Sample {
	// All current int-valued change streams now use int16 (health,
	// armor, ammo). The KindChangeI8 enum value is preserved for
	// future fields that fit; it currently has no callers.
	_ = p
	_ = field
	_ = bStart
	_ = bEnd
	return nil
}

func changeI16Samples(p result.PlayerStream, field string, bStart, bEnd float64) []Sample {
	stream := streamI16(p, field)
	if stream == nil {
		return nil
	}
	return changeSamplesI16(stream, bStart, bEnd)
}

func changeStrSamples(p result.PlayerStream, field string, bStart, bEnd float64) []Sample {
	stream := streamStr(p, field)
	if stream == nil {
		return nil
	}
	return changeSamplesStr(stream, bStart, bEnd)
}

func intervalSamples(p result.PlayerStream, field string, bStart, bEnd float64) []Sample {
	stream := streamInterval(p, field)
	if stream == nil {
		return nil
	}
	// Sample the interval state at a few points across the window so
	// majority / held-any have something meaningful to count. Using
	// just bStart would lose mid-window transitions.
	const samplesPerWindow = 8
	out := make([]Sample, samplesPerWindow)
	span := bEnd - bStart
	for i := 0; i < samplesPerWindow; i++ {
		t := bStart + (float64(i)+0.5)*span/float64(samplesPerWindow)
		out[i] = Sample{T: t, V: intervalContains(stream, t)}
	}
	return out
}

func positionSamples(p result.PlayerStream, bStart, bEnd float64) []Sample {
	if p.Position == nil {
		return nil
	}
	pt := p.Position
	out := make([]Sample, 0, 4)
	// Prepend the latest sample <= bStart so "last" / "first" reducers
	// see the carrying state.
	if idx := positionIndexAtOrBefore(pt, bStart); idx >= 0 {
		out = append(out, Sample{T: float64(pt.T[idx]), V: positionTriple(pt, idx)})
	}
	for i := range pt.T {
		t := float64(pt.T[i])
		if t < bStart {
			continue
		}
		if t >= bEnd {
			break
		}
		out = append(out, Sample{T: t, V: positionTriple(pt, i)})
	}
	return out
}

// positionTriple returns a [3]int32 array for sample i.
func positionTriple(pt *result.PositionTrack, i int) [3]int32 {
	return [3]int32{pt.X[i], pt.Y[i], pt.Z[i]}
}

func positionIndexAtOrBefore(pt *result.PositionTrack, t float64) int {
	// Linear scan: position arrays are large but per-window bins are
	// small, and binary search adds complexity. Optimise later if hot.
	idx := -1
	for i := range pt.T {
		if float64(pt.T[i]) <= t {
			idx = i
			continue
		}
		break
	}
	return idx
}

func eventListSamples(p result.PlayerStream, field string, bStart, bEnd float64) []Sample {
	stream := streamEventList(p, field)
	if stream == nil {
		return nil
	}
	out := make([]Sample, 0, 2)
	for _, t := range stream {
		if t < bStart || t >= bEnd {
			continue
		}
		out = append(out, Sample{T: t, V: t})
	}
	return out
}

// changeSamplesI8 returns the samples from an int8 change stream that
// fall in [bStart, bEnd), prepending the latest entry with T <= bStart
// (carry-forward). Empty result when the player has no entries before
// or in this window.
func changeSamplesI8(stream []result.ChangeI8, bStart, bEnd float64) []Sample {
	out := make([]Sample, 0, 4)
	if idx := indexI8AtOrBefore(stream, bStart); idx >= 0 {
		out = append(out, Sample{T: stream[idx].T, V: stream[idx].V})
	}
	for _, c := range stream {
		if c.T < bStart {
			continue
		}
		if c.T >= bEnd {
			break
		}
		out = append(out, Sample{T: c.T, V: c.V})
	}
	return out
}

func changeSamplesI16(stream []result.ChangeI16, bStart, bEnd float64) []Sample {
	out := make([]Sample, 0, 4)
	if idx := indexI16AtOrBefore(stream, bStart); idx >= 0 {
		out = append(out, Sample{T: stream[idx].T, V: stream[idx].V})
	}
	for _, c := range stream {
		if c.T < bStart {
			continue
		}
		if c.T >= bEnd {
			break
		}
		out = append(out, Sample{T: c.T, V: c.V})
	}
	return out
}

func changeSamplesStr(stream []result.ChangeStr, bStart, bEnd float64) []Sample {
	out := make([]Sample, 0, 4)
	if idx := indexStrAtOrBefore(stream, bStart); idx >= 0 {
		out = append(out, Sample{T: stream[idx].T, V: stream[idx].V})
	}
	for _, c := range stream {
		if c.T < bStart {
			continue
		}
		if c.T >= bEnd {
			break
		}
		out = append(out, Sample{T: c.T, V: c.V})
	}
	return out
}

func indexI8AtOrBefore(stream []result.ChangeI8, t float64) int {
	idx := -1
	for i, c := range stream {
		if c.T <= t {
			idx = i
			continue
		}
		break
	}
	return idx
}

func indexI16AtOrBefore(stream []result.ChangeI16, t float64) int {
	idx := -1
	for i, c := range stream {
		if c.T <= t {
			idx = i
			continue
		}
		break
	}
	return idx
}

func indexStrAtOrBefore(stream []result.ChangeStr, t float64) int {
	idx := -1
	for i, c := range stream {
		if c.T <= t {
			idx = i
			continue
		}
		break
	}
	return idx
}

func intervalContains(stream []result.Interval, t float64) bool {
	for _, iv := range stream {
		if t >= iv.Start && t < iv.End {
			return true
		}
	}
	return false
}

// streamI16 / streamStr / streamInterval / streamEventList dispatch
// by field code on a PlayerStream. Returns nil for unknown codes —
// callers have already validated, so this is a runtime guardrail.
func streamI16(p result.PlayerStream, field string) []result.ChangeI16 {
	switch field {
	case FieldHealth:
		return p.Health
	case FieldArmor:
		return p.Armor
	case FieldLoc:
		return p.Loc
	case FieldShells:
		return p.Shells
	case FieldNails:
		return p.Nails
	case FieldRockets:
		return p.Rockets
	case FieldCells:
		return p.Cells
	}
	return nil
}

func streamStr(p result.PlayerStream, field string) []result.ChangeStr {
	if field == FieldArmorType {
		return p.ArmorType
	}
	return nil
}

func streamInterval(p result.PlayerStream, field string) []result.Interval {
	switch field {
	case FieldRL:
		return p.RL
	case FieldLG:
		return p.LG
	case FieldGL:
		return p.GL
	case FieldSSG:
		return p.SSG
	case FieldSNG:
		return p.SNG
	case FieldQuad:
		return p.Quad
	case FieldPent:
		return p.Pent
	case FieldRing:
		return p.Ring
	}
	return nil
}

func streamEventList(p result.PlayerStream, field string) []float64 {
	switch field {
	case FieldSpawns:
		return p.Spawns
	case FieldDeaths:
		return p.Deaths
	}
	return nil
}

// aggregateTeams produces the per-team counters historically baked
// into HighResBucket.TD. We re-derive from each player's reduced
// values (booleans for weapons / powerups) so the team aggregate is
// always consistent with the per-player display.
func aggregateTeams(
	players []result.PlayerStream,
	reduced map[string]map[string]any,
) map[string]map[string]int {
	teams := make(map[string]map[string]int)
	for _, p := range players {
		pdata, ok := reduced[p.Name]
		if !ok {
			continue
		}
		if p.Team == "" {
			continue
		}
		td := teams[p.Team]
		if td == nil {
			td = make(map[string]int)
			teams[p.Team] = td
		}
		hasRL := boolField(pdata, FieldRL)
		hasLG := boolField(pdata, FieldLG)
		hasGL := boolField(pdata, FieldGL)
		switch {
		case hasRL && hasLG:
			td["rllg"]++
			td["w"]++
		case hasRL:
			td["rl"]++
			td["w"]++
		case hasLG:
			td["lg"]++
			td["w"]++
		}
		if hasGL {
			td["gl"]++
		}
		if boolField(pdata, FieldQuad) {
			td["q"]++
			td["pw"]++
		}
		if boolField(pdata, FieldPent) {
			td["pe"]++
			td["pw"]++
		}
		if boolField(pdata, FieldRing) {
			td["r"]++
			td["pw"]++
		}
		// Health / armor sums.
		if h, ok := numericFromAny(pdata[FieldHealth]); ok {
			td["th"] += int(h)
		}
		if a, ok := numericFromAny(pdata[FieldArmor]); ok {
			td["ta"] += int(a)
		}
	}
	if len(teams) == 0 {
		return nil
	}
	return teams
}

func boolField(pdata map[string]any, field string) bool {
	v, ok := pdata[field]
	if !ok {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func numericFromAny(v any) (float64, bool) {
	if v == nil {
		return 0, false
	}
	return numericToFloat(v)
}

