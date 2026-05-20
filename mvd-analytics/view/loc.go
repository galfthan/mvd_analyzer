package view

import "github.com/mvd-analyzer/mvd-analytics/result"

// locTableOf returns the interned loc-name table for r, or nil when the
// analysis carried no loc data. PlayerStream.Loc / PositionTrack.Li
// values index into it.
func locTableOf(r *result.Result) []string {
	if r == nil || r.TimelineAnalysis == nil {
		return nil
	}
	return r.TimelineAnalysis.LocTable
}

// locNameAt resolves a loc index into its name. Out-of-range indices
// (including the 0 = "no loc" sentinel) resolve to "". Low-cardinality
// views (StateAt, StreamSlice) resolve names here so consumers never
// need the table; dense views (Buckets) keep the index and ship the
// table alongside instead.
func locNameAt(locTable []string, idx int16) string {
	if idx < 0 || int(idx) >= len(locTable) {
		return ""
	}
	return locTable[idx]
}
