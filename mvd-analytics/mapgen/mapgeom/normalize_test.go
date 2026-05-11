package mapgeom

import "testing"

func TestNormalizeLocationName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Item-keyword parts keep uppercase; free text is lowercased.
		{"RA.below", "RA.below"},
		{"ra.below", "RA.below"},
		{"RA below", "RA.below"},
		{"RA-below", "RA.below"},
		{"Quad low", "QUAD.low"},
		{"quad.low", "QUAD.low"},
		{"QUAD", "QUAD"},
		{"RL.High", "RL.high"},
		{"RL high", "RL.high"},
		{"MH", "MH"},
		{"Mega", "MEGA"},
		{"secret", "secret"},

		// Collapsing of multiple separators + trimming.
		{"  RA   below  ", "RA.below"},
		{"RA--below", "RA.below"},
		{"RA - below", "RA.below"},

		// Empty / degenerate inputs.
		{"", ""},
		{"   ", ""},

		// Multi-token paths get each token classified.
		{"RA.MH.low", "RA.MH.low"},
		{"ssg top", "SSG.top"},
	}
	for _, tc := range cases {
		got := NormalizeLocationName(tc.in)
		if got != tc.want {
			t.Errorf("NormalizeLocationName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
