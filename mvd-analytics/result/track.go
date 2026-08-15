package result

import "sort"

// Sample-and-hold policy for the native position track, shared by every
// occupancy walk so they cannot drift apart.
//
// A position sample is evidence that the player was at that loc AT THAT
// INSTANT. Sample-and-hold extends it forward to the next sample, which is
// sound while samples keep arriving at the recording cadence — and unsound the
// moment they stop. The track does not tell you which case you are in; you
// have to bound it.
//
// This matters far more than it looks. On a SERVER-recorded MVD every
// cs_spawned client is written every demo frame (mvdsv/src/sv_demo.c), so gaps
// are cadence-sized and holding to the next sample is exact. On a CLIENT (POV)
// recording only players inside the recorder's PVS are written, so everyone
// except the recorder has multi-second holes: measured on
// demo-test-data/mvd/special-cases/dag_caps_e1m2.mvd the recorder has 76k
// samples with a 152 ms worst gap, while the other seven players have ~15k
// samples with gaps up to 73 SECONDS. Holding across those would credit ~92%
// of a player's loc time to wherever they were standing when they left the
// recorder's view.

// SampleStaleCapMs is how long one position sample's evidence lasts. Past it
// the player's location is simply unknown and no time is credited to anyone.
//
// 250 ms is chosen to be inert at every real recording cadence — the measured
// spread is ~13-16 ms on servers at full tick and ~34-39 ms at the sv_demofps
// default, and the worst gap anywhere in the golden corpus is 74 ms — while
// still bounding the multi-second PVS holes of a POV demo. It is the same
// threshold, for the same reason, as velGapCapMs in
// analyzer/timeline_streams.go: beyond a quarter second a gap is a stall, not
// a cadence.
const SampleStaleCapMs int32 = 250

// TrackHoldEnd is where a track's evidence ends: the final sample held for one
// TYPICAL sample interval, so the last sample is credited like every other one
// (each is credited the gap to its successor; ending exactly at the final
// sample would make it the only sample in the track worth zero).
//
// The hold uses the track's MEDIAN gap, not its last gap. The last gap is the
// least robust estimator available and is exactly the one that is wrong when
// it matters: if the recording stalled just before the end, the final gap is a
// stall and projecting it forward awards the biggest tail precisely where the
// evidence is weakest.
//
// Bounding presence here is also what stops an early quitter being credited to
// match end — sample-and-hold has no staleness bound of its own, and a walker
// that evaluates intervals at their left endpoint would otherwise stretch a
// departed player's final sample to whatever the next event happens to be.
func TrackHoldEnd(t []int32) int32 {
	if len(t) == 0 {
		// No sample, no evidence, nothing to hold: presence ends where it
		// began. Callers guard on len(T) > 0 today, but the policy has to be
		// total — a track with no samples must not panic its way out of a walk.
		return 0
	}
	last := t[len(t)-1]
	if len(t) < 2 {
		// A single sample carries no cadence to hold for. Crediting nothing is
		// the honest answer; inventing a duration would be a guess.
		return last
	}
	// The median is found by counting instead of sorting every gap: gaps
	// are frame cadences, so almost all land in the small fixed-size
	// histogram (an array index per gap); anything larger goes to the
	// overflow map. The selected value is identical to sorting and
	// indexing len/2 — equal values make the selection tie-stable.
	var small [256]int
	var big map[int32]int
	n := 0
	for i := 1; i < len(t); i++ {
		g := t[i] - t[i-1]
		if g <= 0 {
			continue
		}
		if g < int32(len(small)) {
			small[g]++
		} else {
			if big == nil {
				big = make(map[int32]int, 4)
			}
			big[g]++
		}
		n++
	}
	if n == 0 {
		return last
	}
	rank := n / 2
	hold := int32(-1)
	for g := int32(1); g < int32(len(small)); g++ {
		c := small[g]
		if c == 0 {
			continue
		}
		if rank < c {
			hold = g
			break
		}
		rank -= c
	}
	if hold < 0 {
		bigKeys := make([]int32, 0, len(big))
		for g := range big {
			bigKeys = append(bigKeys, g)
		}
		sort.Slice(bigKeys, func(i, j int) bool { return bigKeys[i] < bigKeys[j] })
		hold = bigKeys[len(bigKeys)-1]
		for _, g := range bigKeys {
			if rank < big[g] {
				hold = g
				break
			}
			rank -= big[g]
		}
	}
	if hold > SampleStaleCapMs {
		hold = SampleStaleCapMs
	}
	return last + hold
}

// SampleStaleBoundaries returns the instants at which a sample's evidence
// expires — one per gap longer than SampleStaleCapMs — so a walk can truncate
// the credited interval exactly there instead of crediting the whole gap.
// Returns nil for a track with no oversized gap, which is every
// server-recorded demo.
func SampleStaleBoundaries(t []int32) []int32 {
	var out []int32
	for i := 1; i < len(t); i++ {
		if t[i]-t[i-1] > SampleStaleCapMs {
			out = append(out, t[i-1]+SampleStaleCapMs)
		}
	}
	return out
}
