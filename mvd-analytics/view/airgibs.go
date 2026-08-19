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

	// DefaultAirgibPreMs is the default pre-hit look-back: the victim must
	// already be above the height threshold at EVERY sample of the last
	// preMs before the hit (minus the stamp-lag margin below). The KTX
	// damage entry is stamped one to two wire frames after the physics
	// frame in which the rocket's own knockback moved the victim, so the
	// sample nearest the damage time can show a victim who was STANDING
	// at impact as airborne — e.g. a grounded player on the dm2 moving
	// platform blasted off the edge reads 300+ units of air at the damage
	// timestamp. A genuinely airborne victim got past 96 units via
	// knockback or a fall (a plain jump peaks at ~45), so they are above
	// the threshold for hundreds of ms before the rocket lands and a
	// 200ms look-back cannot lose them.
	DefaultAirgibPreMs = 200

	// MaxAirgibPreMs bounds caller-supplied pre-times: past ~1s the
	// look-back lands before the flight that made the hit an airgib at
	// all, so larger values only reject real events.
	MaxAirgibPreMs = 1000

	// airgibStampLagMs bounds the damage-stamp lag: the damage entry is
	// stamped one to two wire frames (14-29ms measured on hub 232925, at
	// ~14ms per frame) after the impact physics, so samples nearer the
	// hit than this may already carry the rocket's own knockback. It is
	// NOT an exclusion window — knockback can only OVER-report height
	// (a victim genuinely at the threshold cannot be driven to a
	// grounded reading within two frames), so possibly-contaminated
	// samples still participate in the all-airborne check, where they
	// can only reject. Its one gating use is the evidence anchor: the
	// look-back's earliest sample must be at or before (hit - this), so
	// a sparse track whose only nearby reading IS the contaminated hit
	// sample cannot pass vacuously.
	airgibStampLagMs = 40
)

// AirgibsOptions parameterizes ComputeAirgibs. All fields optional.
type AirgibsOptions struct {
	// PreMs is the pre-hit look-back in ms: the victim must be above the
	// height threshold both at the hit AND at every sample of the window
	// [hit - PreMs, hit], whose earliest sample must be pre-impact.
	// 0 → DefaultAirgibPreMs. Negative disables the pre-hit gate
	// entirely (hit-time sample only, the pre-v71 behaviour); positive
	// values at or below airgibStampLagMs cannot anchor on a pre-impact
	// sample and behave the same.
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
// frame — so it can run both as the parse-time post-processor filling
// TimelineAnalysis.Airgibs (default options) and per-request with a
// caller-tuned PreMs.
//
// The victim must be above the height threshold at the hit sample AND at
// every sample of the look-back window [hit - PreMs, hit], anchored on
// pre-impact evidence (see preHitAirborne). The two-sided gate is what
// keeps the list honest: the hit-time sample alone can be post-knockback
// (see DefaultAirgibPreMs), while the whole-window check demands the
// sustained hang time that makes an airgib an airgib — and rejects a
// victim who fell and landed just before the rocket, whose descent
// necessarily left sub-threshold samples in the window.
//
// Returns nil when the map has no clip hull (no PositionTrack.H to
// read), so the airgibs list is simply absent rather than wrong on
// BSP-less runs.
func ComputeAirgibs(r *result.Result, opts AirgibsOptions) []result.AirgibEvent {
	if r == nil || r.Damage == nil || r.Streams == nil || r.TimelineAnalysis == nil {
		return nil
	}
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
		idx := airborneSampleIndex(vs.Position, d.Time)
		if idx < 0 {
			continue
		}
		if preMs > airgibStampLagMs && !preHitAirborne(vs.Position, d.Time, preMs) {
			continue
		}
		h := vs.Position.H[idx]
		loc := ""
		if idx < len(vs.Position.Li) {
			loc = locNameForIndex(locTable, vs.Position.Li[idx])
		}
		// Vertical gap to the shooter: a rocket arriving from far below
		// is often what makes an airgib spectacular, independent of the
		// floor height. Origin-to-origin dz at the two players' nearest
		// samples to the hit; 0 when the shooter has no sample close
		// enough (and on a genuine dead-level hit — omitempty folds
		// both, the neutral value either way).
		dz := float32(0)
		if as := streamByName[d.Attacker]; as != nil && as.Position != nil && len(as.Position.T) > 0 {
			ai := nearestSampleIndex(as.Position.T, d.Time)
			if ai >= 0 && absDeltaMs(as.Position.T[ai], d.Time) <= airgibPosMaxGapMs {
				dz = vs.Position.Z[idx] - as.Position.Z[ai]
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

// airborneSampleIndex returns the index of the position sample nearest
// to tMs when that sample says the victim was measurably airborne —
// within the sample-gap tolerance, with a measured floor, at or above
// the height threshold — and -1 otherwise.
func airborneSampleIndex(pt *result.PositionTrack, tMs int32) int {
	idx := nearestSampleIndex(pt.T, tMs)
	if idx < 0 || absDeltaMs(pt.T[idx], tMs) > airgibPosMaxGapMs {
		return -1
	}
	return airborneAt(pt, idx)
}

// preHitAirborne reports whether the victim was measurably airborne for
// the WHOLE look-back window [hit - preMs, hit]: every sample inside
// the window must be at or above the height threshold, and the earliest
// of them must be a pre-impact sample (at or before hit -
// airgibStampLagMs) sitting within the gap tolerance of the window
// start — so a track with no pre-impact evidence rejects instead of
// passing vacuously on the post-knockback hit sample alone.
//
// The window deliberately runs all the way TO the hit, with no excluded
// tail, because knockback contamination is one-sided: it can only
// OVER-report height, so a possibly-contaminated sample participating
// in the all-airborne check can only reject, never falsely pass. That
// closes both failure modes at once — a victim who was standing at
// impact has grounded samples earlier in the window, and a victim who
// fell and LANDED just before the rocket left sub-threshold samples on
// the way down (falling from the 96-unit line to the floor spans well
// over any stamp lag), so neither survives.
//
// A sample just BEFORE the window is deliberately not consulted: a
// victim who left a ledge right after (hit - preMs) — airborne at every
// sample of the window, grounded an instant before it — is a genuine
// airgib (measured on the 212498 bravado corpus demo: mj hit at a jump
// apex 195ms after crossing the LG ledge edge, 315 units up).
func preHitAirborne(pt *result.PositionTrack, hitMs, preMs int32) bool {
	lo := hitMs - preMs
	i := sort.Search(len(pt.T), func(k int) bool { return pt.T[k] >= lo })
	if i >= len(pt.T) || pt.T[i] > hitMs-airgibStampLagMs || pt.T[i]-lo > airgibPosMaxGapMs {
		return false // no pre-impact sample near the start of the look-back
	}
	for ; i < len(pt.T) && pt.T[i] <= hitMs; i++ {
		if airborneAt(pt, i) < 0 {
			return false
		}
	}
	return true
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
