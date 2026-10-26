package analyzer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// warningSource is an events.Source that also reports a parse-warning
// census, i.e. what mvdsource.Source looks like to the registry.
type warningSource struct {
	eventSource
	summary events.WarningSummary
}

func (s *warningSource) WarningSummary() events.WarningSummary { return s.summary }

// silentSource implements no WarningReporter at all — a replay or
// synthetic source that cannot fail to decode anything.
type silentSource struct{ eventSource }

func TestParseWarnings_CarriedOntoResult(t *testing.T) {
	src := &warningSource{summary: events.WarningSummary{
		Total:  16,
		ByType: map[string]int{"unknown_svc": 14, "parse_error": 2},
		Groups: []events.WarningGroup{
			{Type: "unknown_svc", Message: "svc_unknown_61 (cmd 61)", Count: 5, FirstTimeMs: 12345},
			{Type: "parse_error", Message: "svc_playerinfo: unexpected EOF", Count: 2, FirstTimeMs: 400},
		},
		// 16 total − the 7 in the two retained rows.
		DroppedWarnings: 9,
	}}
	res, err := NewDefaultRegistry().AnalyzeSource(src, "warn.mvd")
	if err != nil {
		t.Fatalf("AnalyzeSource: %v", err)
	}
	pw := res.ParseWarnings
	if pw == nil {
		t.Fatal("ParseWarnings absent on a source that reported 7 warnings")
	}
	if pw.Total != 16 || pw.DroppedWarnings != 9 {
		t.Errorf("counters = %+v, want total 16 / 9 dropped warnings", pw)
	}
	if pw.ByType["unknown_svc"] != 14 || pw.ByType["parse_error"] != 2 {
		t.Errorf("ByType = %v", pw.ByType)
	}
	if len(pw.Groups) != 2 {
		t.Fatalf("len(Groups) = %d, want 2", len(pw.Groups))
	}
	if g := pw.Groups[0]; g.Type != "unknown_svc" || g.Count != 5 || g.FirstDemoTimeMs != 12345 {
		t.Errorf("Groups[0] = %+v", g)
	}
	// The census is a distinct signal: it must not leak into errors[],
	// which consumers read as "an analyzer failed".
	for _, e := range res.Errors {
		if strings.Contains(e, "unknown_svc") || strings.Contains(e, "parse warning") {
			t.Errorf("parse warning leaked into Errors: %q", e)
		}
	}
}

// A clean parse must leave the section ABSENT, not present-and-empty:
// every modern demo would otherwise carry a dead block in its JSON.
func TestParseWarnings_CleanDemoOmitsSection(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  events.Source
	}{
		{"clean reporter", &warningSource{}},
		{"source with no reporter", &silentSource{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := NewDefaultRegistry().AnalyzeSource(tc.src, "clean.mvd")
			if err != nil {
				t.Fatalf("AnalyzeSource: %v", err)
			}
			if res.ParseWarnings != nil {
				t.Fatalf("ParseWarnings present on a clean parse: %+v", res.ParseWarnings)
			}
			b, err := json.Marshal(res)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(b), "parseWarnings") {
				t.Error("clean result serialises a parseWarnings key")
			}
		})
	}
}
