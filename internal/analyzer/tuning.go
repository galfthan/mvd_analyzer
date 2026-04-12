package analyzer

// Tuning constants for the timeline analyzer.
//
// Sampling cadence: the high-res bucket is the granularity used to drive
// map playback in the frontend; the graph bucket is the coarser granularity
// used by the score / item / health graphs. Both default to values that fit
// QuakeWorld duel pacing — 50 ms / 1 s — but can be overridden via
// TimelineAnalyzer.SetBucketDuration for higher fidelity if needed.
const (
	// DefaultHighResBucketDuration is the high-res sampling interval used by
	// the timeline map view (50 ms ≈ 20 Hz).
	DefaultHighResBucketDuration = 0.05

	// DefaultGraphBucketDuration is the aggregation interval for graph
	// buckets (1 s — fine enough for line plots, coarse enough to keep the
	// JSON payload small).
	DefaultGraphBucketDuration = 1.0

	// timelineBucketPrealloc reserves capacity in the bucket slice for a
	// 20-minute match at the default 50 ms cadence (20 * 60 / 0.05 = 24000).
	// Just an allocation hint — the slice grows naturally beyond this.
	timelineBucketPrealloc = 24000
)
