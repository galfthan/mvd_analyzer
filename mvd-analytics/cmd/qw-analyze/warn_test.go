package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func warnResult() *result.Result {
	return &result.Result{ParseWarnings: &result.ParseWarnings{
		Total:  21,
		ByType: map[string]int{"unknown_svc": 19, "parse_error": 2},
		Groups: []result.ParseWarningGroup{
			{Type: "unknown_svc", Message: "svc_unknown_61 (cmd 61)", Count: 7, FirstDemoTimeMs: 61200},
			{Type: "parse_error", Message: "svc_playerinfo: unexpected EOF", Count: 2, FirstDemoTimeMs: 400},
		},
		// The 12 warnings the retained table does not account for
		// (21 total − 9 in the two rows above).
		DroppedWarnings: 12,
	}}
}

// A demo that parsed cleanly must print NOTHING — the one-liner is
// unconditional precisely so it means something when it appears.
func TestReportParseWarnings_SilentOnCleanDemo(t *testing.T) {
	for _, res := range []*result.Result{
		nil,
		{},
		{ParseWarnings: &result.ParseWarnings{}},
	} {
		var b bytes.Buffer
		reportParseWarnings("clean.mvd", res, &b)
		if b.Len() != 0 {
			t.Errorf("printed %q for a clean result", b.String())
		}
	}
}

func TestReportParseWarnings_OneLineSummary(t *testing.T) {
	defer func(prev bool) { warnDetail = prev }(warnDetail)
	warnDetail = false

	var b bytes.Buffer
	reportParseWarnings("/demos/duel.mvd.gz", warnResult(), &b)
	out := b.String()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want the summary + loudest line, got %d lines:\n%s", len(lines), out)
	}
	for _, want := range []string{"duel.mvd.gz", "21 parse warnings", "unknown_svc 19", "parse_error 2"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("summary line %q missing %q", lines[0], want)
		}
	}
	if strings.Contains(lines[0], "/demos/") {
		t.Errorf("summary line quotes the whole path: %q", lines[0])
	}
	// Loudest category first, and the pointer to the fuller detail.
	if i, j := strings.Index(lines[0], "unknown_svc"), strings.Index(lines[0], "parse_error"); i > j {
		t.Errorf("categories not loudest-first: %q", lines[0])
	}
	if !strings.Contains(lines[1], "svc_unknown_61") || !strings.Contains(lines[1], "-warn") {
		t.Errorf("loudest line %q lacks the sample or the -warn pointer", lines[1])
	}
}

func TestReportParseWarnings_DetailTableUnderWarnFlag(t *testing.T) {
	defer func(prev bool) { warnDetail = prev }(warnDetail)
	warnDetail = true

	var b bytes.Buffer
	reportParseWarnings("duel.mvd", warnResult(), &b)
	out := b.String()
	for _, want := range []string{
		"svc_unknown_61 (cmd 61)",
		"svc_playerinfo: unexpected EOF",
		"61.2s",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail output missing %q:\n%s", want, out)
		}
	}
	// The retention cap must never be a silent truncation — and the
	// footer states an occurrence count, never a distinct-message count.
	if !strings.Contains(out, "+12 warnings beyond the") {
		t.Errorf("detail output does not state the warnings past the cap:\n%s", out)
	}
	if strings.Contains(out, "distinct messages") {
		t.Errorf("detail output still claims a distinct-message count:\n%s", out)
	}
}
