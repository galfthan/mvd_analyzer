package analyzer

import "testing"

// The loc column is the one change stream built in finalize rather than the
// event pass, so endOccupancy cannot cut it by length — it is still empty
// when the handover happens. The cut is replayed from the recorded handover
// timestamps instead. Without it a new occupant standing where the previous
// one last stood is deduped away and their stream fragment opens with no
// loc at all.
func TestStreams_LocStreamCutAtOccupancyHandover(t *testing.T) {
	var b streamBuilder
	b.posT = []int32{1000, 2000, 3000, 4000}
	b.posLi = []int16{5, 5, 5, 5}
	b.occCuts = []int32{2500} // the slot changed hands between 2000 and 3000

	b.emitLocStream()

	if len(b.loc) != 2 {
		t.Fatalf("loc = %v, want two samples — one per occupant", b.loc)
	}
	if b.loc[0].t != 1000 || b.loc[1].t != 3000 {
		t.Errorf("loc = %v, want samples at 1000 and 3000", b.loc)
	}
	if b.loc[1].v != 5 {
		t.Errorf("loc[1].v = %d, want 5", b.loc[1].v)
	}
}

// ... and dedup is intact everywhere else: with no handover the four
// identical samples collapse to one.
func TestStreams_LocStreamDedupsWithoutHandover(t *testing.T) {
	var b streamBuilder
	b.posT = []int32{1000, 2000, 3000, 4000}
	b.posLi = []int16{5, 5, 5, 5}

	b.emitLocStream()

	if len(b.loc) != 1 || b.loc[0].t != 1000 {
		t.Errorf("loc = %v, want a single sample at 1000", b.loc)
	}
}
