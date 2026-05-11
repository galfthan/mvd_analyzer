package view

import (
	"github.com/mvd-analyzer/mvd-analytics/result"
)

// RegionControlOptions controls a RegionControl query. WindowMs sets
// the bucket resolution at which the per-region state strings are
// computed; default 50 ms preserves current frontend behaviour.
type RegionControlOptions struct {
	WindowMs int
}

// RegionControlClassifier is the function the caller supplies to do
// the actual per-bucket classification. Decoupling it here keeps the
// view package free of analyzer dependencies (avoids a cycle); the
// caller (analyzer or WASM bridge) plugs in
// analyzer.ComputeRegionControl. At v7 the classifier walks Streams
// directly — see analyzer.ComputeRegionControl — so this signature is
// just a thin pass-through.
type RegionControlClassifier func(
	r *result.Result,
	regions []result.ControlRegion,
	teamA, teamB string,
	teamOf func(playerName string) string,
	windowMs int,
) (map[string]string, map[string]result.RegionStats)

// RegionControl computes a RegionControlResult by delegating to the
// supplied classifier (which reads result.Streams natively). The view
// package doesn't import analyzer to avoid an import cycle; callers
// wire analyzer.ComputeRegionControl as the classifier.
func RegionControl(
	r *result.Result,
	regions []result.ControlRegion,
	teamA, teamB string,
	teamOf func(playerName string) string,
	classifier RegionControlClassifier,
	opts RegionControlOptions,
) (*result.RegionControlResult, error) {
	if r == nil || r.Streams == nil || classifier == nil {
		return &result.RegionControlResult{Regions: regions, TeamA: teamA, TeamB: teamB}, nil
	}
	windowMs := opts.WindowMs
	if windowMs <= 0 {
		windowMs = 50
	}
	bucketStates, stats := classifier(r, regions, teamA, teamB, teamOf, windowMs)
	return &result.RegionControlResult{
		Regions:      regions,
		TeamA:        teamA,
		TeamB:        teamB,
		BucketStates: bucketStates,
		Stats:        stats,
	}, nil
}
