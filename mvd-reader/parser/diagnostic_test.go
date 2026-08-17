package parser

import (
	"testing"
)

// TestWarningSummary_CollectedWithoutDiagnosticMode pins the contract the
// production pipeline depends on: the census runs on every parse, while
// the per-instance list stays opt-in.
func TestWarningSummary_CollectedWithoutDiagnosticMode(t *testing.T) {
	p := NewParser(nil)
	p.warn(1000, "unknown_svc", "svc_unknown_61 (cmd 61)")
	p.warn(2000, "unknown_svc", "svc_unknown_61 (cmd 61)")
	p.warn(3000, "parse_error", "svc_print: boom")

	if got := p.DiagnosticWarnings(); len(got) != 0 {
		t.Fatalf("instances retained outside diagnostic mode: %d", len(got))
	}

	s := p.WarningSummary()
	if s.Total != 3 {
		t.Errorf("Total = %d, want 3", s.Total)
	}
	if s.ByType["unknown_svc"] != 2 || s.ByType["parse_error"] != 1 {
		t.Errorf("ByType = %v, want unknown_svc:2 parse_error:1", s.ByType)
	}
	if len(s.Groups) != 2 {
		t.Fatalf("len(Groups) = %d, want 2", len(s.Groups))
	}
	// Loudest first, and the group carries the FIRST occurrence's time.
	if g := s.Groups[0]; g.Type != "unknown_svc" || g.Count != 2 || g.FirstTimeMs != 1000 {
		t.Errorf("Groups[0] = %+v, want unknown_svc x2 first@1000ms", g)
	}
	if s.DroppedGroups != 0 || s.DroppedWarnings != 0 {
		t.Errorf("dropped counters non-zero on a 2-group parse: %+v", s)
	}
}

// TestWarningSummary_CleanParse: a parse that raised nothing reports a
// zero summary with no maps or rows, which is what lets the Result omit
// the section entirely.
func TestWarningSummary_CleanParse(t *testing.T) {
	s := NewParser(nil).WarningSummary()
	if s.Total != 0 || s.ByType != nil || s.Groups != nil {
		t.Fatalf("clean parse summary is not empty: %+v", s)
	}
}

// TestWarningSummary_GroupCapCountsOverflow: the retention cap bounds the
// SAMPLE table only. Totals and per-type counts stay exact, and what the
// table is missing is stated rather than silently dropped.
func TestWarningSummary_GroupCapCountsOverflow(t *testing.T) {
	p := NewParser(nil)
	const distinct = MaxWarningGroups + 10
	for i := 0; i < distinct; i++ {
		p.warn(int32(i), "parse_error", "failure variant %d", i)
	}
	// One repeat of a group that IS in the table, and one of a group that
	// is not: the first must count into its row, the second into the
	// overflow counter.
	p.warn(9000, "parse_error", "failure variant 0")
	p.warn(9001, "parse_error", "failure variant %d", distinct-1)

	s := p.WarningSummary()
	if want := distinct + 2; s.Total != want {
		t.Errorf("Total = %d, want %d", s.Total, want)
	}
	if s.ByType["parse_error"] != distinct+2 {
		t.Errorf("ByType[parse_error] = %d, want %d", s.ByType["parse_error"], distinct+2)
	}
	if len(s.Groups) != MaxWarningGroups {
		t.Errorf("len(Groups) = %d, want the cap %d", len(s.Groups), MaxWarningGroups)
	}
	if s.DroppedGroups != 10+1 {
		t.Errorf("DroppedGroups = %d, want 11 (10 distinct + the repeat of an uncapped one)", s.DroppedGroups)
	}
	if s.DroppedWarnings != 11 {
		t.Errorf("DroppedWarnings = %d, want 11", s.DroppedWarnings)
	}
	// The retained rows still account for every warning they saw: the
	// repeat of variant 0 landed in its row, not in the overflow.
	var retained int
	for _, g := range s.Groups {
		retained += g.Count
	}
	if retained+s.DroppedWarnings != s.Total {
		t.Errorf("retained %d + dropped %d != total %d", retained, s.DroppedWarnings, s.Total)
	}
	if s.Groups[0].Message != "failure variant 0" || s.Groups[0].Count != 2 {
		t.Errorf("Groups[0] = %+v, want the twice-seen variant 0 first", s.Groups[0])
	}
}

// TestWarningSummary_DeterministicOrder: the summary rides the Result, so
// map iteration order must never reach a consumer.
func TestWarningSummary_DeterministicOrder(t *testing.T) {
	build := func() []WarningGroup {
		p := NewParser(nil)
		for i := 0; i < 20; i++ {
			p.warn(int32(i), "parse_error", "same-count variant %02d", i)
		}
		p.warn(500, "unknown_te", "temp entity type 99")
		p.warn(501, "unknown_te", "temp entity type 99")
		return p.WarningSummary().Groups
	}
	want := build()
	for i := 0; i < 20; i++ {
		got := build()
		if len(got) != len(want) {
			t.Fatalf("group count varies: %d vs %d", len(got), len(want))
		}
		for j := range got {
			if got[j] != want[j] {
				t.Fatalf("run %d differs at row %d: %+v vs %+v", i, j, got[j], want[j])
			}
		}
	}
	if want[0].Type != "unknown_te" {
		t.Errorf("loudest group is %+v, want the unknown_te pair first", want[0])
	}
}

// TestDiagnosticMode_RetainsInstances: the diagnostic harness contract
// (SetDiagnosticMode + DiagnosticWarnings) is unchanged by the always-on
// census.
func TestDiagnosticMode_RetainsInstances(t *testing.T) {
	p := NewParser(nil)
	p.SetDiagnosticMode(true)
	for i := 0; i < MaxWarningGroups*2; i++ {
		p.warn(int32(i), "parse_error", "variant %d", i)
	}
	got := p.DiagnosticWarnings()
	if len(got) != MaxWarningGroups*2 {
		t.Fatalf("len(DiagnosticWarnings) = %d, want %d (uncapped)", len(got), MaxWarningGroups*2)
	}
	if got[3].Type != "parse_error" || got[3].Message != "variant 3" || got[3].Time != 0.003 {
		t.Errorf("instance 3 = %+v, want parse_error/variant 3 at 0.003s", got[3])
	}
	if s := p.WarningSummary(); s.Total != MaxWarningGroups*2 {
		t.Errorf("summary Total = %d, want %d", s.Total, MaxWarningGroups*2)
	}
	if want := "[0.0s] parse_error: variant 0"; got[0].String() != want {
		t.Errorf("String() = %q, want %q", got[0].String(), want)
	}
}
