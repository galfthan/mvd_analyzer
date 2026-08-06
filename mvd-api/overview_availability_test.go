package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// The old has* fields went stale because nothing tied them to the thing they
// described: hasRegionControl was added by hand, and the BSP-derived
// capabilities were simply never added at all. This test is the tie.
//
// Every eager artifact that can answer 422 — i.e. every entry in
// eagerArtifacts carrying an unavailability `code` — is a capability a
// consumer has to be able to ask about BEFORE spending a call on it, so each
// one must have a flag in the manifest. Adding a new 422-able view without a
// flag fails here.
func TestOverviewAvailabilityCoversEvery422Artifact(t *testing.T) {
	// artifact name -> manifest JSON key, where the two names differ.
	alias := map[string]string{
		"frag":         "frags",
		"loc-graph":    "locGraph",
		"player-stats": "playerStats",
		"demoinfo":     "demoInfo",
	}

	keys := map[string]bool{}
	b, err := json.Marshal(OverviewAvailable{})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for k := range m {
		keys[k] = true
	}

	for name, ea := range eagerArtifacts {
		if ea.code == "" {
			continue // always-computable, never 422s — nothing to advertise
		}
		want := name
		if a, ok := alias[name]; ok {
			want = a
		}
		if !keys[want] {
			t.Errorf("artifact %q can return %s but has no %q flag in OverviewAvailable — "+
				"a consumer cannot tell whether to call it without spending the call",
				name, ea.code, want)
		}
	}
}

// The manifest predicts 422s, so a flag that disagrees with the accessor it
// mirrors is worse than no flag: it sends a consumer to an endpoint that
// then refuses. Checked against real demos in the golden corpus (this asserts
// the wiring on whatever the test fixtures carry).
func TestOverviewAvailabilityMatchesTheAccessors(t *testing.T) {
	fixtures := []struct {
		name string
		res  *result.Result
	}{
		{"stub", stubResult()},
		{"damage", damageResult()},
		{"topkills", topKillsFixture("")},
		{"empty", &result.Result{}},
	}
	for _, r := range fixtures {
		a := buildAvailability(r.res)
		got := map[string]bool{
			"demoInfo": a.DemoInfo, "metadata": a.Metadata, "frags": a.Frags,
			"damage": a.Damage, "shots": a.Shots, "aim": a.Aim,
			"locGraph": a.LocGraph, "playerStats": a.PlayerStats,
		}
		for name, ea := range eagerArtifacts {
			if ea.code == "" {
				continue
			}
			key := map[string]string{
				"frag": "frags", "loc-graph": "locGraph",
				"player-stats": "playerStats", "demoinfo": "demoInfo",
			}[name]
			if key == "" {
				key = name
			}
			flag, tracked := got[key]
			if !tracked {
				continue // opening / los are asserted separately below
			}
			_, err := ea.extract(r.res)
			if wantOK := err == nil; flag != wantOK {
				t.Errorf("%s: available.%s = %v but the %s accessor says %v",
					r.name, key, flag, name, wantOK)
			}
		}
		if a.Opening != (r.res.Opening != nil) {
			t.Errorf("%s: available.opening = %v, want %v", r.name, a.Opening, r.res.Opening != nil)
		}
		// LOS covers PVS by construction; a manifest that claimed LOS on a
		// demo with fewer than two players would be predicting a 422.
		if a.LOS && (r.res.Streams == nil || len(r.res.Streams.Players) < 2) {
			t.Errorf("%s: available.los = true with %d player streams", r.name, len(r.res.Streams.Players))
		}
	}
}

// The removed fields must stay removed — the whole point of the v70 change
// was that overview stopped echoing three other endpoints. A future edit
// re-adding one should have to delete this test and explain itself.
func TestOverviewCarriesNoInlinedHighlights(t *testing.T) {
	b, err := json.Marshal(Overview{})
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"topKills", "topStreaks", "topPowerups", "hasRegionControl"} {
		if strings.Contains(string(b), `"`+gone+`"`) {
			t.Errorf("overview carries %q again — it was removed in v70 as a copy of "+
				"/top-kills, /lives+/events, or folded into available.regionControl", gone)
		}
	}
	if reflect.TypeOf(Overview{}).NumField() == 0 {
		t.Fatal("unreachable, keeps reflect imported for the field walk above")
	}
}

// The BSP-derived flags mean MEASURED, not NON-ZERO — the distinction the
// rest of the manifest already promises ("the section exists, not that it is
// non-empty") and the one a consumer cannot recover any other way.
//
// A dry map is the case that separates the two readings. When the liquid gate
// opens, the column is filled for every sample, so a map with no water yields
// a full-length all-zero `lq` — a genuine "dry" reading. Keying the flag on a
// non-zero entry (as this once did) reports that as the same false a server
// with no BSP reports, and a consumer asking "is there water here" cannot tell
// "no" from "nobody looked".
func TestOverviewAvailabilityBSPFlagsMeanMeasuredNotNonZero(t *testing.T) {
	dry := &result.Result{Streams: &result.Streams{Players: []result.PlayerStream{{
		Name: "alpha",
		Position: &result.PositionTrack{
			T: []int32{0, 100, 200},
			// Full-length columns: the gates opened. Every liquid sample is
			// 0 (a dry map) and every height is a real trace.
			H:  []float32{16, 16, 16},
			Lq: []int8{0, 0, 0},
		},
	}}}}
	a := buildAvailability(dry)
	if !a.Liquid {
		t.Error("available.liquid = false on an all-zero but COMPUTED lq column — " +
			"that is a measured 'dry map', not an absent measurement")
	}
	if !a.Height {
		t.Error("available.height = false on a computed h column")
	}

	// No columns at all: the gates never opened, so neither is measurable.
	unprovisioned := &result.Result{Streams: &result.Streams{Players: []result.PlayerStream{{
		Name:     "alpha",
		Position: &result.PositionTrack{T: []int32{0, 100, 200}},
	}}}}
	b := buildAvailability(unprovisioned)
	if b.Liquid || b.Height {
		t.Errorf("available height=%v liquid=%v with no BSP-derived columns, want both false",
			b.Height, b.Liquid)
	}

	// The gates are separate — the floor trace needs the collision hull and
	// the liquid probe the vis BSP — so a demo can carry one column without
	// the other and the flags must not move together.
	heightOnly := &result.Result{Streams: &result.Streams{Players: []result.PlayerStream{{
		Name:     "alpha",
		Position: &result.PositionTrack{T: []int32{0, 100}, H: []float32{16, 16}},
	}}}}
	c := buildAvailability(heightOnly)
	if !c.Height || c.Liquid {
		t.Errorf("available height=%v liquid=%v on a height-only demo, want true/false",
			c.Height, c.Liquid)
	}
}
