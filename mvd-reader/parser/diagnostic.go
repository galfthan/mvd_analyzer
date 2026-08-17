package parser

import (
	"fmt"
	"sort"
)

// Warning represents a diagnostic issue found during parsing.
type Warning struct {
	Time    float64 // Demo time when the warning occurred
	Type    string  // Category: "parse_error", "unknown_svc", "unknown_te", "unknown_hidden"
	Message string  // Human-readable description
}

func (w Warning) String() string {
	return fmt.Sprintf("[%.1fs] %s: %s", w.Time, w.Type, w.Message)
}

// MaxWarningGroups caps how many distinct (type, message) groups the
// always-on summary retains. The type vocabulary is a handful of
// in-code constants, but a message embeds the failing svc name, the
// error text and the abandoned byte count, so its cardinality is
// bounded only by how creatively a demo is broken. Groups past the cap
// are still COUNTED (into WarningSummary.DroppedGroups /
// DroppedWarnings and into ByType, which is exact regardless) — the cap
// bounds retention, never the census. Retention is first-encounter
// order, so on a badly broken demo the table samples the distinct
// messages rather than ranking them; ByType is where the shape of the
// damage is read.
const MaxWarningGroups = 64

// WarningGroup is one distinct (type, message) pair the parser saw,
// with how often it fired and when it first did.
type WarningGroup struct {
	Type        string
	Message     string
	Count       int
	FirstTimeMs int32 // wire-native demo time of the first occurrence
}

// WarningSummary is the compact, always-collected census of parse
// warnings. It is what production consumers get: exact totals, exact
// per-type counts, and a capped table of first-occurrence samples with
// their counts. The full instance list stays diagnostic-mode-only
// (DiagnosticWarnings) — it is unbounded, and a summary answers every
// operator question a list would ("what broke, how often, when first").
type WarningSummary struct {
	Total           int
	ByType          map[string]int // exact, never capped
	Groups          []WarningGroup // at most MaxWarningGroups, deterministically ordered
	DroppedGroups   int            // distinct groups beyond the cap
	DroppedWarnings int            // warning instances in those dropped groups
}

// SetDiagnosticMode opts into retaining every individual warning for
// DiagnosticWarnings below. It no longer gates COLLECTION: the summary
// counters (WarningSummary) run on every parse so production consumers
// see protocol gaps without opting in. Only the unbounded per-instance
// list is diagnostic-only.
func (p *Parser) SetDiagnosticMode(enabled bool) {
	p.diagnosticMode = enabled
}

// DiagnosticWarnings returns every individual warning collected during
// parsing. Populated only in diagnostic mode (SetDiagnosticMode); use
// WarningSummary for the always-available census.
func (p *Parser) DiagnosticWarnings() []Warning {
	return p.warnings
}

// WarningSummary returns the parse-warning census for everything parsed
// so far. Safe to call at any point; normally called after Parse /
// the final ParseOne. Total == 0 means a clean parse.
func (p *Parser) WarningSummary() WarningSummary {
	s := WarningSummary{
		Total:           p.warnTotal,
		DroppedGroups:   p.warnDroppedGroups,
		DroppedWarnings: p.warnDropped,
	}
	if p.warnTotal == 0 {
		return s
	}
	s.ByType = make(map[string]int, len(p.warnByType))
	for k, v := range p.warnByType {
		s.ByType[k] = v
	}
	s.Groups = make([]WarningGroup, 0, len(p.warnGroups))
	for k, g := range p.warnGroups {
		s.Groups = append(s.Groups, WarningGroup{
			Type:        k.typ,
			Message:     k.msg,
			Count:       g.count,
			FirstTimeMs: g.firstMs,
		})
	}
	// Deterministic order: loudest first, then a stable lexicographic
	// tie-break. Map iteration order must never reach a consumer — the
	// summary rides the Result, whose "output is a pure function of the
	// demo" property is test-enforced.
	sort.Slice(s.Groups, func(i, j int) bool {
		a, b := s.Groups[i], s.Groups[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.Message < b.Message
	})
	return s
}

// warn records a diagnostic warning. The counters are always updated —
// they are the only operator-visible signal that the wire carried
// something we could not read (the sv_bigcoords desync degraded 5% of
// the archive for years precisely because this was test-only). The
// per-instance list is kept only in diagnostic mode.
//
// timeMs is the canonical wire-native demo time; Warning.Time is the
// derived float seconds view, kept for the human-readable diagnostic
// output only (the parser cannot import events.Sec without an import
// cycle, so the identical conversion is inlined here).
func (p *Parser) warn(timeMs int32, typ, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)

	p.warnTotal++
	if p.warnByType == nil {
		p.warnByType = make(map[string]int, 4)
		p.warnGroups = make(map[warnKey]*warnGroup, 8)
	}
	p.warnByType[typ]++
	k := warnKey{typ: typ, msg: msg}
	if g, ok := p.warnGroups[k]; ok {
		g.count++
	} else if len(p.warnGroups) < MaxWarningGroups {
		p.warnGroups[k] = &warnGroup{count: 1, firstMs: timeMs}
	} else {
		p.warnDroppedGroups++
		p.warnDropped++
	}

	if p.diagnosticMode {
		p.warnings = append(p.warnings, Warning{
			Time:    float64(timeMs) * 0.001,
			Type:    typ,
			Message: msg,
		})
	}
}

// warnKey identifies a warning group: the category plus the fully
// formatted message, so two different failing svc_* commands never
// collapse into one row.
type warnKey struct{ typ, msg string }

type warnGroup struct {
	count   int
	firstMs int32
}

// svcName returns a human-readable name for an svc_* command byte.
var svcNames = map[byte]string{
	0: "svc_bad", 1: "svc_nop", 2: "svc_disconnect", 3: "svc_updatestat",
	4: "svc_version", 5: "svc_setview", 6: "svc_sound", 7: "svc_time",
	8: "svc_print", 9: "svc_stufftext", 10: "svc_setangle",
	11: "svc_serverdata", 12: "svc_lightstyle", 13: "svc_updatename",
	14: "svc_updatefrags", 15: "svc_clientdata", 16: "svc_stopsound",
	17: "svc_updatecolors", 18: "svc_particle", 19: "svc_damage",
	20: "svc_spawnstatic", 21: "svc_fte_spawnstatic2",
	22: "svc_spawnbaseline", 23: "svc_temp_entity", 24: "svc_setpause",
	25: "svc_signonnum", 26: "svc_centerprint", 27: "svc_killedmonster",
	28: "svc_foundsecret", 29: "svc_spawnstaticsound", 30: "svc_intermission",
	31: "svc_finale", 32: "svc_cdtrack", 33: "svc_sellscreen",
	34: "svc_smallkick", 35: "svc_bigkick", 36: "svc_updateping",
	37: "svc_updateentertime", 38: "svc_updatestatlong", 39: "svc_muzzleflash",
	40: "svc_updateuserinfo", 41: "svc_download", 42: "svc_playerinfo",
	43: "svc_nails", 44: "svc_chokecount", 45: "svc_modellist",
	46: "svc_soundlist", 47: "svc_packetentities", 48: "svc_deltapacketentities",
	49: "svc_maxspeed", 50: "svc_entgravity", 51: "svc_setinfo",
	52: "svc_serverinfo", 53: "svc_updatepl", 54: "svc_nails2",
	60: "svc_fte_modellistshort", 66: "svc_fte_spawnbaseline2",
}

// SvcName returns a human-readable name for an svc command byte.
func SvcName(cmd byte) string {
	if name, ok := svcNames[cmd]; ok {
		return name
	}
	return fmt.Sprintf("svc_unknown_%d", cmd)
}
