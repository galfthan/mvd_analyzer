package view

import (
	"fmt"
	"sort"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// Airgib detection tuning.
const (
	// airgibMinHeightUnits is the floor-relative height (PositionTrack.H,
	// feet above the floor) a victim must be at for a rocket hit to count
	// as an airgib — ~two player models up. The player hull is 56 tall;
	// 96 keeps the list to genuinely-airborne hits, not stair-steps or
	// small hops. It is the only volume bound: every qualifying hit is
	// emitted (schema v30), since the threshold already keeps the list
	// to a handful per match.
	airgibMinHeightUnits = 96

	// airgibPosMaxGapMs rejects a hit whose nearest victim position
	// sample is further away in time than this — without a position near
	// the hit we can't say how high the victim was.
	airgibPosMaxGapMs = 250

	// Lethality window: a rocket frag (same attacker→victim) this close to
	// the hit marks it lethal. Asymmetric — the obituary lands at or just
	// after the damage.
	airgibLethalBackMs = 200
	airgibLethalFwdMs  = 1000

	// DefaultAirgibPreMs is the default pre-hit look-back: the victim
	// must read at or above the height threshold at every sample of the
	// look-back window — the "clear air" that makes an airgib look like
	// one. 100ms keeps the spectacular ledge-drop hits (a victim who
	// left a high ledge ~150ms before the rocket reads 96+ for the whole
	// window) while still rejecting players clipped ~50-90ms after a
	// ledge hop, standing victims, and victims who landed just before
	// the rocket. Floor-relative height is a step function at ledge
	// edges, so a longer window measures time-since-the-edge, not hang
	// time — 200ms measurably dropped genuine 300+-unit events from the
	// golden corpus.
	DefaultAirgibPreMs = 100

	// MaxAirgibPreMs bounds caller-supplied pre-times: past ~1s the
	// look-back lands before the flight that made the hit an airgib at
	// all, so larger values only reject real events.
	MaxAirgibPreMs = 1000

	// airgibStampLagMs bounds the damage-stamp jitter: KTX writes the
	// damage message inline in T_Damage (ktx/src/combat.c:815), and
	// measured over 410 direct rocket hits in four demos the stamp lands
	// in the SAME wire frame as the first knockback-visible position
	// sample 82% of the time, a frame EARLIER 12%, and up to two frames
	// (+28ms) late 6% — nothing beyond +28ms. Samples at or before
	// (hit - this) are therefore pre-impact; samples nearer the stamp
	// may already carry the rocket's own knockback. Contamination is
	// one-sided — knockback can over-report height but cannot fake a
	// grounded reading — so possibly-contaminated samples are trusted
	// when they read GROUNDED (see airgibGroundedMaxH) and ignored when
	// they read high.
	airgibStampLagMs = 40

	// airgibWindowEvidenceGapMs decides when in-window samples reach the
	// window's start: if the earliest sample inside the look-back window
	// begins more than this after the window start (or the window is
	// empty), the PRECEDING tick — the sample that was live at the
	// window start — decides instead. That is what makes the gate work
	// on old demos whose tick cadence exceeds the window (their state is
	// the carried-forward preceding sample) and what closes the
	// sparse-hole false pass (a track with a recording gap ending in a
	// single boundary sample gets judged by the grounded tick before the
	// gap, not by the hole).
	airgibWindowEvidenceGapMs = 60

	// airgibGroundedMaxH is the "grounded reading" bound: a sample this
	// close to the floor is ground contact. Grounded readings are
	// trustworthy even inside the stamp-jitter tail — knockback cannot
	// fake one — so a grounded sample at the hit vetoes the event (a
	// victim who fell and LANDED just before the rocket), while a
	// merely-low tail reading does not (a genuine airgib knocked
	// laterally over a higher floor reads low without ever touching
	// ground, and must not be lost).
	airgibGroundedMaxH = 8
)

// AirgibsOptions parameterizes ComputeAirgibs. All fields optional.
type AirgibsOptions struct {
	// PreMs is the pre-hit look-back in ms: every position sample in
	// [hit - PreMs, hit - airgibStampLagMs] must read at or above the
	// height threshold, with the preceding tick standing in when the
	// window holds no sample near its start (old low-tickrate demos,
	// recording holes). 0 → DefaultAirgibPreMs. Values at or below
	// airgibStampLagMs collapse the window to a point check at the
	// pre-impact boundary. Negative disables the pre-hit gate entirely
	// (hit-time sample only, the pre-v73 behaviour).
	PreMs int32
}

// ValidateAirgibPreMs range-checks a caller-supplied preMs (an API query
// param) against [0, MaxAirgibPreMs].
func ValidateAirgibPreMs(preMs int) error {
	if preMs < 0 || preMs > MaxAirgibPreMs {
		return fmt.Errorf("preMs must be between 0 and %d, got %d", MaxAirgibPreMs, preMs)
	}
	return nil
}

// ComputeAirgibs finds enemy rocket hits landed on airborne victims and
// returns every qualifying hit, height-sorted, for the Key Moments view.
// A pure function of the assembled Result — the per-hit damage log
// (result.Damage), the streams' floor-height column (PositionTrack.H),
// the frag log (for lethality), the per-stream session table (for
// per-hit userids) and the loc table, all in one match-relative time
// frame — so it can run both at parse time under the highlights
// post-processor (default options; the rows are published wrapped as
// Highlights.Airgibs HighlightEvent rows) and per-request with a
// caller-tuned PreMs.
//
// A hit qualifies when three things hold, each judged only on evidence
// that can be trusted for what it claims:
//
//  1. Clear air before the hit: every sample in the look-back window
//     [hit - PreMs, hit - airgibStampLagMs] reads >= the height
//     threshold, with the preceding tick standing in when the window
//     has no sample near its start (see preHitAirborne). Samples there
//     are pre-impact by the measured stamp-jitter bound, so a grounded
//     reading among them is the victim standing — the false positive
//     this gate exists for.
//  2. No grounded reading beside the hit: no sample in the stamp-jitter
//     neighbourhood of the damage time reads ground contact (see
//     groundedNearHit). Samples there may carry the rocket's own
//     knockback, which can only OVER-report height — so high readings
//     prove nothing and are ignored, while a grounded one is real (a
//     victim who landed just before the rocket) and vetoes.
//  3. Evidence exists: a sample within the gap tolerance of the hit.
//
// Reported height / loc / heightAboveAttacker come from the latest
// PRE-IMPACT sample — the victim as the rocket found them — not from
// the possibly knockback-contaminated hit-frame sample.
//
// Returns nil when the map has no clip hull (no PositionTrack.H to
// read), so the airgibs list is simply absent rather than wrong on
// BSP-less runs.
func ComputeAirgibs(r *result.Result, opts AirgibsOptions) []result.AirgibEvent {
	if r == nil || r.Damage == nil || r.Streams == nil || r.TimelineAnalysis == nil {
		return nil
	}
	// Reconstructed damage participates on equal terms with the wire
	// stream (the highlights DAG node binds `damage:final`, so the
	// stored list always sees the recon-filled section): damagerecon's
	// direct-vs-splash split is geometric — a hit is direct only when
	// its TE_EXPLOSION / projectile endpoint lands within 48 units of
	// the victim — its timestamps are frame-accurate, and the height
	// gates below do the discriminating either way. The demo-wide
	// r.Damage.Source ("ktx" | "reconstructed") tells a consumer which
	// evidence the list rests on.
	preMs := opts.PreMs
	if preMs == 0 {
		preMs = DefaultAirgibPreMs
	}

	streamByName := make(map[string]*result.PlayerStream, len(r.Streams.Players))
	anyHeight := false
	for i := range r.Streams.Players {
		p := &r.Streams.Players[i]
		streamByName[p.Name] = p
		if p.Position != nil && len(p.Position.H) == len(p.Position.T) && len(p.Position.H) > 0 {
			anyHeight = true
		}
	}
	if !anyHeight {
		return nil // no floor-height data (no BSP for the map)
	}

	locTable := r.TimelineAnalysis.LocTable
	// Per-hit userids: the connection each player held at the hit's own
	// instant, not the demo-wide last-session-with-play id — an airgib
	// inside a rejoiner's earlier stint belongs to the connection that
	// threw / took it. Sourced from the published per-stream session
	// table (match-relative, like the damage times);
	// TimelineAnalysis.PlayerUserIDs stays the fallback for names with
	// no sessions.
	userIDs := newStreamUserIDIndex(r.Streams.Players, r.TimelineAnalysis.PlayerUserIDs)
	teamFor := func(name string) string {
		// A damage participant always played, so they have a stream; the
		// timeline stamps stream teams with the roster's synthetic
		// name-per-player duel labels at birth, matching every other
		// surface. Match.Players (the same canonical labels — see
		// RegionControl's team inference) is the fallback for a missing
		// or unlabelled stream.
		if ps := streamByName[name]; ps != nil && ps.Team != "" {
			return ps.Team
		}
		if r.Match != nil {
			for i := range r.Match.Players {
				if r.Match.Players[i].Name == name {
					return r.Match.Players[i].Team
				}
			}
		}
		return ""
	}

	// Rocket kills, for lethality matching.
	var rlFrags []result.FragEntry
	if r.Frags != nil {
		for _, f := range r.Frags.Frags {
			if f.Weapon == "rl" && !f.IsSuicide {
				rlFrags = append(rlFrags, f)
			}
		}
	}

	// Damage.Events is match-gated at the source (the damage analyzer drops
	// out-of-match hits), so this loop never sees a warmup / post-match rocket
	// — do not reintroduce a time or in-match gate here.
	var events []result.AirgibEvent
	for _, d := range r.Damage.Events {
		// Direct enemy rockets only — a rocket model striking the player.
		// Splash (radius) damage is excluded, as are self / teammate /
		// environmental hits.
		if d.Weapon != "rl" || d.IsSplash || d.IsSelf || d.IsTeam || d.IsEnv {
			continue
		}
		vs := streamByName[d.Victim]
		if vs == nil || vs.Position == nil || len(vs.Position.H) != len(vs.Position.T) {
			continue
		}
		hitIdx := nearestSampleIndex(vs.Position.T, d.Time)
		if hitIdx < 0 || absDeltaMs(vs.Position.T[hitIdx], d.Time) > airgibPosMaxGapMs {
			continue // no position evidence near the hit
		}
		// ri is the sample the event reports from: the latest pre-impact
		// sample near the hit under the window gate, the hit-nearest
		// sample under the legacy (PreMs < 0) rule.
		ri := hitIdx
		if preMs < 0 {
			// Legacy hit-only rule (pre-v73): airborne at the sample
			// nearest the damage time.
			if airborneAt(vs.Position, hitIdx) < 0 {
				continue
			}
		} else {
			if groundedNearHit(vs.Position, d.Time) {
				continue // trustworthy grounded reading beside the hit — landed
			}
			ri = preHitAirborne(vs.Position, d.Time, preMs)
			if ri < 0 {
				continue
			}
		}
		h := vs.Position.H[ri]
		loc := ""
		if ri < len(vs.Position.Li) {
			loc = locNameForIndex(locTable, vs.Position.Li[ri])
		}
		// Vertical gap to the shooter: a rocket arriving from far below
		// is often what makes an airgib spectacular, independent of the
		// floor height. Origin-to-origin dz at the two players' samples
		// nearest the reported instant; 0 when the shooter has no sample
		// close enough (and on a genuine dead-level hit — omitempty
		// folds both, the neutral value either way).
		dz := float32(0)
		if as := streamByName[d.Attacker]; as != nil && as.Position != nil && len(as.Position.T) > 0 {
			ai := nearestSampleIndex(as.Position.T, vs.Position.T[ri])
			if ai >= 0 && absDeltaMs(as.Position.T[ai], vs.Position.T[ri]) <= airgibPosMaxGapMs {
				dz = vs.Position.Z[ri] - as.Position.Z[ai]
			}
		}
		events = append(events, result.AirgibEvent{
			Time:                d.Time,
			Attacker:            d.Attacker,
			AttackerTeam:        teamFor(d.Attacker),
			AttackerUserID:      userIDs.at(d.Attacker, d.Time),
			Victim:              d.Victim,
			VictimTeam:          teamFor(d.Victim),
			VictimUserID:        userIDs.at(d.Victim, d.Time),
			Height:              h,
			HeightAboveAttacker: dz,
			Loc:                 loc,
			Damage:              d.Damage,
			Lethal:              airgibLethal(rlFrags, d),
		})
	}
	if len(events) == 0 {
		return nil
	}

	// Default order: highest first, ties broken by earliest time for a
	// stable, deterministic list. The web view re-sorts client-side.
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Height != events[j].Height {
			return events[i].Height > events[j].Height
		}
		return events[i].Time < events[j].Time
	})
	return events
}

// preHitAirborne applies the clear-air window gate and returns the index
// of the reporting sample — the latest pre-impact sample — or -1 when
// the gate rejects.
//
// The window is [hit - preMs, hit - airgibStampLagMs]: every sample
// inside it must read at or above the height threshold. When the window
// holds no sample within airgibWindowEvidenceGapMs of its start — an
// old demo whose tick cadence exceeds the window, or a recording hole —
// the PRECEDING tick (the latest sample at or before the window start,
// the value carried forward at that instant) must be airborne instead.
// A sample just before the window is otherwise deliberately NOT
// consulted: a victim who left a high ledge just after the window
// opened reads 96+ at every in-window sample and is a genuine airgib,
// even though the tick before the window still saw them on the ledge.
func preHitAirborne(pt *result.PositionTrack, hitMs, preMs int32) int {
	lo, hi := hitMs-preMs, hitMs-airgibStampLagMs
	if lo > hi {
		lo = hi // preMs <= the stamp-lag bound: a point check at the pre-impact boundary
	}
	first := sort.Search(len(pt.T), func(k int) bool { return pt.T[k] >= lo })
	last := -1
	for i := first; i < len(pt.T) && pt.T[i] <= hi; i++ {
		if airborneAt(pt, i) < 0 {
			return -1
		}
		last = i
	}
	if last >= 0 && pt.T[first]-lo <= airgibWindowEvidenceGapMs {
		return last // in-window evidence reaches the window start
	}
	// No sample near the window start: the preceding tick was the live
	// state there. It must exist, be recent enough to trust, and read
	// airborne.
	prev := first - 1
	if prev < 0 || lo-pt.T[prev] > airgibPosMaxGapMs || airborneAt(pt, prev) < 0 {
		return -1
	}
	if last >= 0 {
		return last
	}
	return prev
}

// groundedNearHit reports a trustworthy grounded reading in the
// stamp-jitter neighbourhood of the hit, (hit - airgibStampLagMs,
// hit + airgibStampLagMs]: a victim who fell and LANDED just before the
// rocket shows ground contact there even when the hit-frame sample
// itself already carries the knockback and reads high again. Grounded
// readings are trustworthy on both sides of the stamp — knockback can
// over-report height but cannot fake ground contact — while a
// merely-low reading (a genuine airgib knocked laterally over a higher
// floor) does not veto.
func groundedNearHit(pt *result.PositionTrack, hitMs int32) bool {
	i := sort.Search(len(pt.T), func(k int) bool { return pt.T[k] > hitMs-airgibStampLagMs })
	for ; i < len(pt.T) && pt.T[i] <= hitMs+airgibStampLagMs; i++ {
		if h := pt.H[i]; h != result.NoFloor && h < airgibGroundedMaxH {
			return true
		}
	}
	return false
}

// airborneAt returns idx when the sample there reads measurably airborne
// (a measured floor, at or above the height threshold), else -1.
func airborneAt(pt *result.PositionTrack, idx int) int {
	if h := pt.H[idx]; h == result.NoFloor || h < airgibMinHeightUnits {
		return -1
	}
	return idx
}

// airgibLethal reports whether a rocket frag (same attacker→victim)
// landed within the lethality window of this hit.
func airgibLethal(rlFrags []result.FragEntry, d result.DamageEntry) bool {
	for _, f := range rlFrags {
		if f.Victim != d.Victim || f.Killer != d.Attacker {
			continue
		}
		if f.Time >= d.Time-airgibLethalBackMs && f.Time <= d.Time+airgibLethalFwdMs {
			return true
		}
	}
	return false
}

// nearestSampleIndex returns the index into the time-sorted slice ts
// whose value is closest to t, or -1 when ts is empty.
func nearestSampleIndex(ts []int32, t int32) int {
	if len(ts) == 0 {
		return -1
	}
	i := sort.Search(len(ts), func(k int) bool { return ts[k] >= t })
	if i == 0 {
		return 0
	}
	if i >= len(ts) {
		return len(ts) - 1
	}
	if t-ts[i-1] <= ts[i]-t {
		return i - 1
	}
	return i
}

func absDeltaMs(a, b int32) int32 {
	if a < b {
		return b - a
	}
	return a - b
}

// locNameForIndex resolves a PositionTrack.Li index into a loc name,
// bounds-checked. Index 0 (and out-of-range) is the "no loc" sentinel.
func locNameForIndex(locTable []string, li int16) string {
	if li <= 0 || int(li) >= len(locTable) {
		return ""
	}
	return locTable[li]
}

// streamUserIDIndex answers "which userid did this name hold at t" from
// the published per-stream session tables, with the demo-wide
// PlayerUserIDs map as fallback for names with no sessions. The view-
// side sibling of the analyzer's session-table index: published session
// windows carry the same occupancy starts, already shifted to the
// match-relative clock the damage log uses.
type streamUserIDIndex struct {
	windows  map[string][]result.PlayerSession
	fallback map[string]int
}

func newStreamUserIDIndex(players []result.PlayerStream, fallback map[string]int) *streamUserIDIndex {
	x := &streamUserIDIndex{windows: make(map[string][]result.PlayerSession), fallback: fallback}
	for i := range players {
		p := &players[i]
		if len(p.Sessions) == 0 {
			continue
		}
		// Sessions are published in time order; key by the stream's
		// canonical name — the name the damage log uses.
		x.windows[p.Name] = p.Sessions
	}
	return x
}

// at returns the userid of the connection live at match-relative tMs:
// the last session starting at or before tMs (a hit just before the
// first session's start still belongs to that first connection), or the
// fallback map's demo-wide pick when the name has no sessions. Start-only
// on purpose — a hit in a gap between sessions resolves to the preceding
// connection, matching the analyzer-side session index this replaced
// (damage is match-gated, so such gaps are rare reconnect windows).
func (x *streamUserIDIndex) at(name string, tMs int32) int {
	ws := x.windows[name]
	for i := len(ws) - 1; i >= 0; i-- {
		if i == 0 || tMs >= ws[i].StartMs {
			return ws[i].UserID
		}
	}
	return x.fallback[name]
}
