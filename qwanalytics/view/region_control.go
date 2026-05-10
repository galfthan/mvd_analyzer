package view

import (
	"github.com/mvd-analyzer/qwanalytics/result"
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
// analyzer.ComputeRegionControl.
type RegionControlClassifier func(
	buckets []result.HighResBucket,
	locTable []string,
	regions []result.ControlRegion,
	teamA, teamB string,
	teamOf func(playerName string) string,
) (map[string]string, map[string]result.RegionStats)

// RegionControl rebuilds a RegionControlResult at the requested
// windowMs by deriving buckets from r.Streams (via view.Buckets with
// the legacy reducer set), converting them to v6 HighResBucket
// shape, and handing them to the supplied classifier.
//
// The view package doesn't import analyzer; callers wire
// analyzer.ComputeRegionControl as the classifier.
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
	bv, err := Buckets(r, BucketsOptions{
		WindowMs:    windowMs,
		Fields:      AllStandardFields,
		Reducers:    LegacyReducerSet,
		IncludeTeam: false,
	})
	if err != nil {
		return nil, err
	}
	legacy := ToLegacyHighResBuckets(bv)

	locTable := []string{}
	if r.TimelineAnalysis != nil {
		locTable = r.TimelineAnalysis.LocTable
	}

	bucketStates, stats := classifier(legacy, locTable, regions, teamA, teamB, teamOf)

	return &result.RegionControlResult{
		Regions:      regions,
		TeamA:        teamA,
		TeamB:        teamB,
		BucketStates: bucketStates,
		Stats:        stats,
	}, nil
}
