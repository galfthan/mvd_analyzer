package analyzer

import (
	"github.com/mvd-analyzer/mvd-analytics/view"
)

// airgibsPost publishes the default airgib list — direct enemy rocket
// hits on airborne victims, height-sorted — to TimelineAnalysis.Airgibs
// for the Key Moments view. The detection itself lives in
// view.ComputeAirgibs, a pure function of the assembled Result, so the
// REST layer can re-run it per request with a caller-tuned pre-hit
// look-back (preMs); this post-processor bakes the default-options run
// into the stored Result — the same staging as regionControlPost. It
// runs with the full Result — the per-hit damage log, the streams'
// floor-height column (PositionTrack.H), the frag log, the per-stream
// session tables and the loc table — all populated and in one
// match-relative time frame (after normalizeMatchRelativeTimes).
//
// No-op when the map has no clip hull (no PositionTrack.H to read), so
// the airgibs list is simply absent rather than wrong on BSP-less runs.
func airgibsPost(res *Result, co *CoreOutputs) {
	if res == nil || res.TimelineAnalysis == nil {
		return
	}
	if events := view.ComputeAirgibs(res, view.AirgibsOptions{}); len(events) > 0 {
		res.TimelineAnalysis.Airgibs = events
	}
}
