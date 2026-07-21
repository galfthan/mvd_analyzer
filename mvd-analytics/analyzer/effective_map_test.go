package analyzer

import (
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-reader/events"
)

// TestEffectiveMapFallback pins the map-resolution precedence both accessors
// share: the KTX demoinfo map wins when present, else the serverinfo `map`
// key, else "". This is the gate that decides whether the BSP-derived passes
// (LOS/PVS, loc, floor height, liquid, region control) run — a demoinfo-less
// demo (DemoInfo == nil) must still resolve a map from serverinfo.
func TestEffectiveMapFallback(t *testing.T) {
	cases := []struct {
		name       string
		demoMap    string // "" means DemoInfo absent entirely
		hasDemo    bool
		serverMap  string // "" means no serverinfo map
		hasServer  bool
		wantResult string
		wantCore   string
	}{
		{name: "demoinfo present wins", hasDemo: true, demoMap: "dm3", hasServer: true, serverMap: "dm6", wantResult: "dm3", wantCore: "dm3"},
		{name: "demoinfo absent, serverinfo fallback", hasDemo: false, hasServer: true, serverMap: "schloss", wantResult: "schloss", wantCore: "schloss"},
		{name: "demoinfo present but empty map, serverinfo fallback", hasDemo: true, demoMap: "", hasServer: true, serverMap: "e1m2", wantResult: "e1m2", wantCore: "e1m2"},
		{name: "neither source", hasDemo: false, hasServer: false, wantResult: "", wantCore: ""},
		{name: "serverinfo present but no map key", hasDemo: false, hasServer: true, serverMap: "", wantResult: "", wantCore: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Post-hoc accessor on the assembled Result (the ComputeLOS path).
			res := &result.Result{}
			if tc.hasDemo {
				res.DemoInfo = &result.DemoInfoResult{Map: tc.demoMap}
			}
			if tc.hasServer {
				si := map[string]string{"hostname": "x"}
				if tc.serverMap != "" {
					si["map"] = tc.serverMap
				}
				res.Metadata = &result.MetadataResult{ServerInfo: si}
			}
			if got := res.EffectiveMap(); got != tc.wantResult {
				t.Errorf("Result.EffectiveMap() = %q, want %q", got, tc.wantResult)
			}

			// Pipeline-time accessor on CoreOutputs.
			co := &CoreOutputs{}
			if tc.hasDemo {
				co.DemoInfo = &result.DemoInfoResult{Map: tc.demoMap}
			}
			co.ServerInfoMap = tc.serverMap
			if got := co.EffectiveMap(); got != tc.wantCore {
				t.Errorf("CoreOutputs.EffectiveMap() = %q, want %q", got, tc.wantCore)
			}
		})
	}
}

// TestMetadataPopulateCoreServerInfoMap exercises the full metadata path a
// demoinfo-less demo takes: a `fullserverinfo` stufftext carries the `map`
// key, MetadataAnalyzer parses it, and PopulateCore publishes it so the
// timeline (and any BSP-derived pass) resolves the map with DemoInfo == nil.
func TestMetadataPopulateCoreServerInfoMap(t *testing.T) {
	a := NewMetadataAnalyzer()
	if err := a.OnEvent(&events.StuffTextEvent{
		Command: `fullserverinfo "\maxfps\77\map\dm3\*version\MVDSV 1.00\ktxver\1.43"`,
	}); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}
	res := &result.Result{}
	if err := a.Finalize(res); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	co := &CoreOutputs{} // no demoinfo node ran → DemoInfo stays nil
	a.PopulateCore(co)

	if co.ServerInfoMap != "dm3" {
		t.Errorf("co.ServerInfoMap = %q, want dm3", co.ServerInfoMap)
	}
	if got := co.EffectiveMap(); got != "dm3" {
		t.Errorf("co.EffectiveMap() = %q, want dm3 (demoinfo absent → serverinfo fallback)", got)
	}
	if got := res.EffectiveMap(); got != "dm3" {
		t.Errorf("res.EffectiveMap() = %q, want dm3 via Metadata.ServerInfo", got)
	}
}
