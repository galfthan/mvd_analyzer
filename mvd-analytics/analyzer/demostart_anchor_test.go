package analyzer

import "testing"

// TestPlausibleDemoStartUnixMs guards the range check that rejects the
// non-timestamp 0x000B payloads seen in the corpus (61, 11701) while
// accepting real demo-open wall clocks.
func TestPlausibleDemoStartUnixMs(t *testing.T) {
	cases := []struct {
		v    int64
		want bool
	}{
		{0, false},
		{61, false},          // corpus game 211805
		{11701, false},       // corpus game 212545
		{946684800000, true}, // 2000-01-01, lower bound
		{1780260653484, true},
		{1777115225 * 1000, true}, // epoch-derived (game 211805's `epoch`)
		{4102444800000, false},    // 2100-01-01, exclusive upper bound
	}
	for _, c := range cases {
		if got := plausibleDemoStartUnixMs(c.v); got != c.want {
			t.Errorf("plausibleDemoStartUnixMs(%d) = %v, want %v", c.v, got, c.want)
		}
	}
}

// TestDeriveDemoStartAnchor exercises the wall-clock anchor fallback:
// TimelineAnalyzer.Finalize sets DemoStartUnixMs/AccuracyMs=1 from the
// mvdhidden 0x000B block; deriveDemoStartAnchor only fills in the
// whole-second serverinfo `epoch` cvar when 0x000B was absent, and never
// overwrites the finer source.
func TestDeriveDemoStartAnchor(t *testing.T) {
	const epochSecs = 1780260653 // 2026-05-31T20:50:53Z, whole seconds

	tests := []struct {
		name         string
		ta           *TimelineAnalysisResult
		serverInfo   map[string]string
		wantUnixMs   int64
		wantAccuracy int32
	}{
		{
			name:         "no timeline is a no-op",
			ta:           nil,
			serverInfo:   map[string]string{"epoch": "1780260653"},
			wantUnixMs:   0,
			wantAccuracy: 0,
		},
		{
			name:         "0x000B already set — epoch must not overwrite",
			ta:           &TimelineAnalysisResult{DemoStartUnixMs: 1780260653484, DemoStartAccuracyMs: 1},
			serverInfo:   map[string]string{"epoch": "1780260653"},
			wantUnixMs:   1780260653484,
			wantAccuracy: 1,
		},
		{
			name:         "epoch fallback when 0x000B absent",
			ta:           &TimelineAnalysisResult{},
			serverInfo:   map[string]string{"epoch": "1780260653"},
			wantUnixMs:   epochSecs * 1000,
			wantAccuracy: 1000,
		},
		{
			name:         "no epoch and no 0x000B — anchor stays absent",
			ta:           &TimelineAnalysisResult{},
			serverInfo:   map[string]string{"maxfps": "77"},
			wantUnixMs:   0,
			wantAccuracy: 0,
		},
		{
			name:         "garbage epoch is ignored",
			ta:           &TimelineAnalysisResult{},
			serverInfo:   map[string]string{"epoch": "not-a-number"},
			wantUnixMs:   0,
			wantAccuracy: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := &Result{TimelineAnalysis: tc.ta}
			if tc.serverInfo != nil {
				res.Metadata = &MetadataResult{ServerInfo: tc.serverInfo}
			}
			deriveDemoStartAnchor(res, nil)

			if tc.ta == nil {
				return // nothing to assert beyond "did not panic"
			}
			if tc.ta.DemoStartUnixMs != tc.wantUnixMs {
				t.Errorf("DemoStartUnixMs = %d, want %d", tc.ta.DemoStartUnixMs, tc.wantUnixMs)
			}
			if tc.ta.DemoStartAccuracyMs != tc.wantAccuracy {
				t.Errorf("DemoStartAccuracyMs = %d, want %d", tc.ta.DemoStartAccuracyMs, tc.wantAccuracy)
			}
		})
	}
}
