package analyzer

import (
	"github.com/mvd-analyzer/mvd-analytics/view"
)

// highlightsPost publishes the highlight catalogue — discharges, quadbores,
// telefrags and airgibs, each row with every participant's
// state at the instant — to Result.Highlights. The detection lives in
// view.ComputeHighlights, a pure function of the assembled Result, so the
// REST layer can re-run it per request (the airgib look-back); this
// post-processor bakes the default run into the stored Result. It runs
// with the final frag log (recovered team telefrags appended), the
// per-player streams and the damage section in its final form, all in one
// match-relative frame; the airgib detection (view.ComputeAirgibs) runs
// inside the compute.
//
// No-op when there is nothing to build from (no streams, no frag log, no
// timeline analysis): the section is simply absent rather than empty, which
// is what view.Highlights reports as unavailable.
func highlightsPost(res *Result, co *CoreOutputs) {
	if res == nil {
		return
	}
	if h := view.ComputeHighlights(res, view.HighlightsOptions{}); h != nil {
		res.Highlights = h
	}
}
