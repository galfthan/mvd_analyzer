package damagerecon

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// SkipModeReasons is the published vocabulary of the `boundedMode`
// `skipped:<reason>` values; the mvd-api spec's enum is checked against it by
// TestBoundedModeEnumCoversSkipReasons. This test is the other half: every
// input the detection recognises must answer with a name that is IN the list,
// so a new detection cannot be added without also publishing its name.
func TestSkipModeReasonsAreComplete(t *testing.T) {
	known := map[string]bool{}
	for _, r := range SkipModeReasons {
		known[r] = true
	}

	// Every legacy cvar, every umode and every submode token the two
	// detection layers test for (damagerecon.go SkipModeReason) plus the
	// countdown rows SkipModeReasonFull folds in.
	cases := []struct {
		name string
		si   map[string]string
		ms   *result.MatchSettings
	}{
		{name: "k_midair", si: map[string]string{"k_midair": "1"}},
		{name: "k_instagib", si: map[string]string{"k_instagib": "1"}},
		{name: "k_dmgfrags", si: map[string]string{"k_dmgfrags": "1"}},
		{name: "umode wipeout", si: map[string]string{"mode": "wipeout"}},
		{name: "umode ca", si: map[string]string{"mode": "ca"}},
		{name: "umode race", si: map[string]string{"mode": "race"}},
		{name: "sub midair", si: map[string]string{"mode": "4on4-midair"}},
		{name: "sub instagib", si: map[string]string{"mode": "4on4-instagib"}},
		{name: "sub lgc", si: map[string]string{"mode": "4on4-lgc"}},
		{name: "sub race", si: map[string]string{"mode": "1on1-race"}},
		{name: "sub df", si: map[string]string{"mode": "4on4-df"}},
		{name: "sub ca", si: map[string]string{"mode": "4on4-ca"}},
		{name: "sub wo", si: map[string]string{"mode": "4on4-wo"}},
		{name: "sub ra", si: map[string]string{"mode": "1on1-ra"}},
		{name: "countdown midair", ms: &result.MatchSettings{Midair: true}},
		{name: "countdown instagib", ms: &result.MatchSettings{Instagib: true}},
		{name: "countdown dmgfrags", ms: &result.MatchSettings{Dmgfrags: true}},
	}
	for _, c := range cases {
		got := SkipModeReasonFull(c.si, c.ms)
		if got == "" {
			t.Errorf("%s: no skip reason detected", c.name)
			continue
		}
		if !known[got] {
			t.Errorf("%s: reason %q is not in SkipModeReasons %v", c.name, got, SkipModeReasons)
		}
	}
	if r := SkipModeReasonFull(map[string]string{"mode": "4on4"}, &result.MatchSettings{}); r != "" {
		t.Errorf("plain 4on4 reported skip reason %q, want none", r)
	}
}
