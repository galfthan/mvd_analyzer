package view

import "github.com/mvd-analyzer/mvd-analytics/result"

// PageResult is the Result as the web page reads it, from either source:
// the WASM build (mvd-web/cmd/wasm) and mvd-api's GET /v1/demos/{id}/result
// both marshal this, so the two cannot drift.
//
// It is a shallow copy with two sections swapped:
//   - playerStats carries the KTX demoinfo overlay (PlayerStats). The
//     analyzer stores the derived section, so the goldens record what this
//     pipeline computed; the merge with KTX's own numbers is a read-time
//     step, as /player-stats does it. Without it the page would show
//     accuracy, the KTX damage splits and pickup tallies, ping/handicap/bot
//     as if the demo had no demoinfo block. ErrUnavailable (no player
//     streams) leaves the section as stored.
//   - streams leaves out the position tracks, ~92% of the Result's JSON:
//     the page takes them as a position pack (result.EncodePositionPack,
//     /streams/positions) instead. It leaves out the players' line of
//     sight (los/pvs) too: that is a lazy pass (/los, the page's
//     lineOfSight), which mvd-api splices into its stored Result once any
//     caller asks, while the WASM build has not run it when it hands the
//     Result over.
//
// res itself is not modified, so later view calls over it start from the
// state the analyzer produced.
func PageResult(res *result.Result) *result.Result {
	out := *res
	if ps, err := PlayerStats(res, PlayerStatsOptions{}); err == nil {
		out.PlayerStats = ps
	}
	out.Streams = res.Streams.WithoutPositions()
	if out.Streams != nil {
		for i := range out.Streams.Players {
			out.Streams.Players[i].LOS = nil
			out.Streams.Players[i].PVS = nil
		}
	}
	return &out
}
