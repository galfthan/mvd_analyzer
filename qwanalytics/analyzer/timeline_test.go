package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/qwdemo/events"
)

// TestSampleCurrentStateAtIndex_FloatRoundTrip guards against a regression
// where the high-res sample-fill loop synthesized a bucket time as
// float64(b)*bucketDuration and then re-derived the bucket index via
// int(t/bucketDuration). 0.05 is not representable in float64, so for many
// indices (324, 329, ... — every ~5th bucket in some ranges) the recomputed
// index came back as b-1 and the wrong bucket was populated. The exporter
// then dropped the now-empty bucket b, producing visible timeline gaps at
// 20Hz scale.
func TestSampleCurrentStateAtIndex_FloatRoundTrip(t *testing.T) {
	a := NewTimelineAnalyzer()
	ctx := &Context{}
	ctx.Players[0] = &events.PlayerInfo{Slot: 0, Name: "p0"}
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	a.timing.Started = true
	// Pre-seed a live player so populateBucket won't skip us.
	state := a.getOrCreatePlayerState(0)
	state.vitals.health = 100

	// 324 * 0.05 = 16.199999999999999 in float64, which / 0.05 = 323.99…,
	// so int() yields 323. Verify that the index-addressed path still
	// populates bucket 324.
	const floatBadIndex = 324
	a.sampleCurrentStateAtIndex(floatBadIndex)

	if len(a.buckets) <= floatBadIndex {
		t.Fatalf("buckets slice not extended to %d (len=%d)", floatBadIndex, len(a.buckets))
	}
	got := a.buckets[floatBadIndex]
	if got == nil || len(got.playerData) == 0 {
		t.Fatalf("bucket %d not populated (playerData len=%d)", floatBadIndex,
			func() int {
				if got == nil {
					return 0
				}
				return len(got.playerData)
			}())
	}
	if _, ok := got.playerData[0]; !ok {
		t.Fatalf("bucket %d missing player 0 entry", floatBadIndex)
	}
}

// TestHandleStatUpdate_NoMissingBucketsAcrossFloatBoundary exercises the
// full event-driven sample-fill path across a stretch of indices where the
// float round-trip bug previously dropped every 5th bucket. Drive the
// analyzer with position events at ~13 ms cadence from bucket 320 to 340
// and assert every intermediate bucket has the player's data.
func TestHandleStatUpdate_NoMissingBucketsAcrossFloatBoundary(t *testing.T) {
	a := NewTimelineAnalyzer()
	ctx := &Context{}
	ctx.Players[0] = &events.PlayerInfo{Slot: 0, Name: "p0"}
	if err := a.Init(ctx); err != nil {
		t.Fatal(err)
	}
	a.timing.Started = true
	// Health is required for populateBucket not to skip the player.
	a.getOrCreatePlayerState(0).vitals.health = 100

	// Feed position events walking from t=16.0 (bucket 320) to t=17.2
	// (bucket 344), stepping 0.013 s ≈ 72 Hz server frame rate. This
	// range crosses the previously-bad indices 324, 329, 334, 339, 344.
	for t := 16.0; t < 17.25; t += 0.013 {
		a.handlePositionUpdate(&events.PlayerPositionEvent{
			PlayerNum: 0,
			Origin:    [3]float32{100, 200, 300},
			Time:      t,
		})
	}

	// Every bucket from 321 (first fill after lastSampleTime=0 initial
	// frame) through 344 must carry player 0's snapshot.
	for idx := 321; idx <= 344; idx++ {
		if idx >= len(a.buckets) {
			t.Fatalf("bucket %d missing (len=%d)", idx, len(a.buckets))
		}
		b := a.buckets[idx]
		if b == nil || len(b.playerData) == 0 {
			t.Errorf("bucket %d empty (startTime=%.3f)", idx, float64(idx)*a.bucketDuration)
		}
	}
}
