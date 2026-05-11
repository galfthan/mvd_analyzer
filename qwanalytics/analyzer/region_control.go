package analyzer

import (
	"strings"

	"github.com/mvd-analyzer/qwanalytics/result"
)

// Region-control state codes used by ComputeRegionControl as a compact
// one-char-per-bucket encoding. Mirror classifyRegionState in
// qw-web/static/app.js. Exported as constants for consumers (frontend,
// MCP wrappers, tests) that want to decode the bucketStates strings.
const (
	RegionStateEmpty            byte = '_'
	RegionStateTeamAControl     byte = 'A'
	RegionStateTeamAWeakControl byte = 'a'
	RegionStateContested        byte = 'C'
	RegionStateWeakContested    byte = 'c'
	RegionStateTeamBControl     byte = 'B'
	RegionStateTeamBWeakControl byte = 'b'
)

// ComputeRegionControl runs the per-bucket region-control classifier
// over a finished result and returns:
//
//   - bucketStates: region name -> string of length n_buckets, one
//     state byte per bucket (codes above);
//   - stats:        region name -> match-aggregate share of each state,
//     expressed as percentages (0..100, one decimal place; the seven
//     values sum to 100 within rounding).
//
// At schema v7 the function walks result.Streams directly: per
// 50 ms (or windowMs) bucket, find each player's last position
// sample with T <= bucket-end (via PositionTrack.Li), look up the
// loc → region, check armed (RL/LG interval membership), tally per
// region, classify.
//
// Inputs:
//
//   - r:         finalised Result with Streams populated.
//   - regions:   the regions to evaluate. Each region's Locs field is
//                authoritative — Points/Centroid are ignored.
//   - teamA, teamB: the two team names to classify against. Players
//                whose team is neither are ignored.
//   - teamOf:    name -> team callback. Returning the empty string
//                excludes the player from the count.
//   - windowMs:  bucket resolution. <= 0 falls back to 50 ms.
//
// Mirrors qw-web/static/app.js: classifyRegionState (4275-4285).
// "Armed" means RL or LG. Pre-spawn / dead samples (Li=0) are skipped.
func ComputeRegionControl(
	r *Result,
	regions []ControlRegion,
	teamA, teamB string,
	teamOf func(playerName string) string,
	windowMs int,
) (map[string]string, map[string]RegionStats) {
	if r == nil || r.Streams == nil || len(regions) == 0 {
		return nil, nil
	}
	if windowMs <= 0 {
		windowMs = 50
	}
	bucketDur := float64(windowMs) / 1000.0

	var locTable []string
	if r.TimelineAnalysis != nil {
		locTable = r.TimelineAnalysis.LocTable
	}

	// regionByLoc maps the canonical loc name (lower-cased) to the
	// region name. We lower-case for case-insensitive matching, same as
	// the on-disk regions JSON loader (timeline_regions.go:80-81).
	regionByLoc := make(map[string]string)
	for _, rg := range regions {
		for _, ln := range rg.Locs {
			regionByLoc[strings.ToLower(ln)] = rg.Name
		}
	}
	if len(regionByLoc) == 0 {
		return nil, nil
	}

	// Pre-resolve each region's loc-index set so the inner loop only
	// does an integer hashtable lookup, not a string-lower per sample.
	regionByLi := make(map[int16]string, len(regionByLoc))
	for li, name := range locTable {
		if rn, ok := regionByLoc[strings.ToLower(name)]; ok {
			regionByLi[int16(li)] = rn
		}
	}
	if len(regionByLi) == 0 {
		return nil, nil
	}

	// Bucket grid is anchored at MatchStart. At finalize time
	// MatchStart is the wall-clock match-start timestamp; after
	// postprocess.normalizeMatchRelativeTimes shifts streams,
	// MatchStart=0 and MatchEnd is the duration. Either way the
	// per-bucket window is [MatchStart + bi*dur, MatchStart + (bi+1)*dur).
	matchStart := r.Streams.Global.MatchStart
	matchEnd := r.Streams.Global.MatchEnd
	if matchEnd <= matchStart {
		return nil, nil
	}
	nBuckets := int(((matchEnd - matchStart) / bucketDur) + 0.5)
	if nBuckets <= 0 {
		return nil, nil
	}

	type counts struct{ aWpn, aNo, bWpn, bNo int }
	presence := make([]map[string]*counts, nBuckets)
	for i := range presence {
		presence[i] = make(map[string]*counts, len(regions))
		for _, rg := range regions {
			presence[i][rg.Name] = &counts{}
		}
	}

	// Per-player walk: for each bucket, find the latest position
	// sample with T <= bucket-end, classify region presence + armed.
	for _, p := range r.Streams.Players {
		team := teamOf(p.Name)
		if team == "" || (team != teamA && team != teamB) {
			continue
		}
		pt := p.Position
		if pt == nil || len(pt.T) == 0 || len(pt.Li) != len(pt.T) {
			continue
		}
		// "First sampling" semantics: bucket bi represents state at
		// time bucketStart = matchStart + bi*bucketDur. sIdx points
		// to the latest position sample with T <= bucketStart, so
		// pt.Li[sIdx] is the loc index at that moment.
		sIdx := 0
		for bi := 0; bi < nBuckets; bi++ {
			bucketStart := matchStart + float64(bi)*bucketDur
			for sIdx+1 < len(pt.T) && float64(pt.T[sIdx+1]) <= bucketStart {
				sIdx++
			}
			// Skip if the player hasn't started emitting positions yet.
			if float64(pt.T[sIdx]) > bucketStart {
				continue
			}
			li := pt.Li[sIdx]
			if li == 0 {
				continue
			}
			regionName, ok := regionByLi[li]
			if !ok {
				continue
			}
			armed := intervalsOverlapAt(p.RL, bucketStart) ||
				intervalsOverlapAt(p.LG, bucketStart)
			c := presence[bi][regionName]
			if c == nil {
				continue
			}
			switch team {
			case teamA:
				if armed {
					c.aWpn++
				} else {
					c.aNo++
				}
			case teamB:
				if armed {
					c.bWpn++
				} else {
					c.bNo++
				}
			}
		}
	}

	// Classify per bucket per region; tally aggregate state percentages.
	stateBuf := make(map[string][]byte, len(regions))
	totals := make(map[string]*RegionStats, len(regions))
	for _, rg := range regions {
		stateBuf[rg.Name] = make([]byte, 0, nBuckets)
		totals[rg.Name] = &RegionStats{}
	}
	for bi := 0; bi < nBuckets; bi++ {
		for _, rg := range regions {
			c := presence[bi][rg.Name]
			state := classifyRegionState(c.aWpn, c.aNo, c.bWpn, c.bNo)
			stateBuf[rg.Name] = append(stateBuf[rg.Name], state)
			t := totals[rg.Name]
			switch state {
			case RegionStateEmpty:
				t.Empty++
			case RegionStateTeamAControl:
				t.TeamAControl++
			case RegionStateTeamAWeakControl:
				t.TeamAWeakControl++
			case RegionStateContested:
				t.Contested++
			case RegionStateWeakContested:
				t.WeakContested++
			case RegionStateTeamBControl:
				t.TeamBControl++
			case RegionStateTeamBWeakControl:
				t.TeamBWeakControl++
			}
		}
	}

	bucketStates := make(map[string]string, len(regions))
	stats := make(map[string]RegionStats, len(regions))
	total := float64(nBuckets)
	pct := func(n float64) float64 { return float64(int(n/total*1000+0.5)) / 10 }
	for _, rg := range regions {
		bucketStates[rg.Name] = string(stateBuf[rg.Name])
		t := totals[rg.Name]
		stats[rg.Name] = RegionStats{
			TeamAControl:     pct(t.TeamAControl),
			TeamAWeakControl: pct(t.TeamAWeakControl),
			Contested:        pct(t.Contested),
			WeakContested:    pct(t.WeakContested),
			Empty:            pct(t.Empty),
			TeamBWeakControl: pct(t.TeamBWeakControl),
			TeamBControl:     pct(t.TeamBControl),
		}
	}
	return bucketStates, stats
}

// intervalsOverlapAt returns true iff t falls inside any half-open
// interval [Start, End). Used to test "did the player have weapon W
// at this exact time" without a separate state-at lookup.
func intervalsOverlapAt(iv []result.Interval, t float64) bool {
	for _, in := range iv {
		if t >= in.Start && t < in.End {
			return true
		}
	}
	return false
}

// classifyRegionState is the seven-state decision rule, faithful port
// of qw-web/static/app.js:4275-4285.
func classifyRegionState(aWpn, aNo, bWpn, bNo int) byte {
	aT := aWpn + aNo
	bT := bWpn + bNo
	if aT == 0 && bT == 0 {
		return RegionStateEmpty
	}
	if aT > 0 && bT == 0 {
		if aWpn > 0 {
			return RegionStateTeamAControl
		}
		return RegionStateTeamAWeakControl
	}
	if bT > 0 && aT == 0 {
		if bWpn > 0 {
			return RegionStateTeamBControl
		}
		return RegionStateTeamBWeakControl
	}
	// Both teams present.
	if aWpn > 0 && bWpn == 0 {
		return RegionStateTeamAControl
	}
	if bWpn > 0 && aWpn == 0 {
		return RegionStateTeamBControl
	}
	if aWpn > 0 && bWpn > 0 {
		return RegionStateContested
	}
	return RegionStateWeakContested
}
