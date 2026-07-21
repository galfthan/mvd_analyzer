package view

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// marshal is a tiny helper: JSON-encode v and return the string.
func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// TestFragsEnvelopeFlattens checks the pass-through envelope echoes the fixed
// ms unit and flattens the stored FragResult fields verbatim (no shadow copy,
// so `time` stays the stored int32 ms and can never drift).
func TestFragsEnvelopeFlattens(t *testing.T) {
	fr := &result.FragResult{
		TotalFrags: 1,
		Frags:      []result.FragEntry{{Time: 12345, Killer: "a", Victim: "b", Weapon: "rl"}},
		ByWeapon:   map[string]int{"rl": 1},
		ByPlayer:   map[string]*result.PlayerFrags{},
	}
	got := marshal(t, FragsEnvelope{TimeUnit: UnitMs, FragResult: fr})
	if !strings.Contains(got, `"timeUnit":"ms"`) {
		t.Errorf("frags envelope missing ms echo: %s", got)
	}
	if !strings.Contains(got, `"time":12345`) {
		t.Errorf("frags time not stored int ms: %s", got)
	}
	// The embedded struct's fields flatten to the top level.
	if !strings.Contains(got, `"totalFrags":1`) || !strings.Contains(got, `"byWeapon":`) {
		t.Errorf("embedded FragResult fields not flattened: %s", got)
	}
	// nil frags (summary drop) survives as JSON null, not [].
	summary := &result.FragResult{TotalFrags: 1, ByWeapon: map[string]int{}, ByPlayer: map[string]*result.PlayerFrags{}}
	if s := marshal(t, FragsEnvelope{TimeUnit: UnitMs, FragResult: summary}); !strings.Contains(s, `"frags":null`) {
		t.Errorf("summary frags should be null: %s", s)
	}
}

// TestListEnvelopes checks each bare-array body gets a {timeUnit, <key>}
// object, the ms echo, and an empty list renders as [] (never null).
func TestListEnvelopes(t *testing.T) {
	chat := ChatEnvelope{TimeUnit: UnitMs, Messages: []result.MatchEvent{{Time: 20, Type: "teamsay", Player: "a"}}}
	if s := marshal(t, chat); !strings.Contains(s, `"timeUnit":"ms"`) || !strings.Contains(s, `"messages":[`) {
		t.Errorf("chat envelope wrong: %s", s)
	}
	// An empty (but non-nil) list stays [] — matching the view constructors.
	empty := AirgibsEnvelope{TimeUnit: UnitMs, Airgibs: []result.AirgibEvent{}}
	if s := marshal(t, empty); !strings.Contains(s, `"airgibs":[]`) {
		t.Errorf("empty airgibs should be []: %s", s)
	}
	bp := BackpacksEnvelope{TimeUnit: UnitMs, Backpacks: []result.BackpackDrop{{Time: 1000, Weapon: "rl"}}}
	if s := marshal(t, bp); !strings.Contains(s, `"backpacks":[`) || !strings.Contains(s, `"time":1000`) {
		t.Errorf("backpacks envelope wrong: %s", s)
	}
	wp := WeaponPickupsEnvelope{TimeUnit: UnitMs, Pickups: []result.WeaponPickup{{Time: 2000, Weapon: "lg", Source: "backpack"}}}
	if s := marshal(t, wp); !strings.Contains(s, `"pickups":[`) || !strings.Contains(s, `"timeUnit":"ms"`) {
		t.Errorf("weapon-pickups envelope wrong: %s", s)
	}
}

// TestDerivedViewEcho checks the seconds-native derived views carry the fixed
// "s" echo when the handler sets it, and that `t` stays float seconds.
func TestDerivedViewEcho(t *testing.T) {
	ev := &EventsView{TimeUnit: UnitSec, Events: []TaggedEvent{
		{T: 10.5, Type: "streak", Player: "a", Detail: map[string]any{"endTime": 20.25}},
	}}
	if s := marshal(t, ev); !strings.Contains(s, `"timeUnit":"s"`) || !strings.Contains(s, `"t":10.5`) ||
		!strings.Contains(s, `"endTime":20.25`) {
		t.Errorf("events echo/seconds wrong: %s", s)
	}
	sa := &StateAtView{TimeUnit: UnitSec, Time: 30, Players: map[string]PlayerStateAt{}}
	if s := marshal(t, sa); !strings.Contains(s, `"timeUnit":"s"`) || !strings.Contains(s, `"t":30`) {
		t.Errorf("state-at echo wrong: %s", s)
	}
}

// TestDerivedViewNoEchoWhenUnset guards the WASM/qw-analyze paths: those build
// the view struct without setting TimeUnit, so the omitempty tag keeps timeUnit
// out of the body entirely (byte-identical to the pre-v56 shape).
func TestDerivedViewNoEchoWhenUnset(t *testing.T) {
	ev := &EventsView{Events: []TaggedEvent{{T: 1, Type: "spawn"}}}
	if s := marshal(t, ev); strings.Contains(s, "timeUnit") {
		t.Errorf("unset EventsView must omit timeUnit: %s", s)
	}
}

// TestAimEnvelopeFlattens checks the /aim pass-through envelope echoes the fixed
// ms unit (aim is entirely dense int32-ms) and flattens the stored AimResult.
func TestAimEnvelopeFlattens(t *testing.T) {
	am := &result.AimResult{Players: []result.PlayerAim{{Player: "a", Mode: "duel"}}}
	got := marshal(t, AimEnvelope{TimeUnit: UnitMs, AimResult: am})
	if !strings.Contains(got, `"timeUnit":"ms"`) {
		t.Errorf("aim envelope missing ms echo: %s", got)
	}
	if !strings.Contains(got, `"players":[`) || !strings.Contains(got, `"player":"a"`) {
		t.Errorf("embedded AimResult fields not flattened: %s", got)
	}
}

// TestRegionControlEnvelopeFlattens checks the /region-control envelope echoes
// the fixed ms unit (windowMs axis) and flattens the stored RegionControlResult.
func TestRegionControlEnvelopeFlattens(t *testing.T) {
	rc := &result.RegionControlResult{
		TeamA:        "red",
		TeamB:        "blue",
		BucketStates: map[string]string{"mid": "A_b"},
	}
	got := marshal(t, RegionControlEnvelope{TimeUnit: UnitMs, RegionControlResult: rc})
	if !strings.Contains(got, `"timeUnit":"ms"`) {
		t.Errorf("region-control envelope missing ms echo: %s", got)
	}
	if !strings.Contains(got, `"teamA":"red"`) || !strings.Contains(got, `"bucketStates":`) {
		t.Errorf("embedded RegionControlResult fields not flattened: %s", got)
	}
}

// TestColumnarBucketsEcho checks the columnar /buckets layout carries the fixed
// "ms" echo when the handler sets it (its startMs/windowMs axis is int32 ms),
// and that the WASM/qw-analyze path (TimeUnit unset) omits it entirely — the
// omitempty guard keeps that constructor-direct output byte-identical to pre-v56.
func TestColumnarBucketsEcho(t *testing.T) {
	cb := &ColumnarBuckets{TimeUnit: UnitMs, WindowMs: 5000, StartMs: 0, Count: 3}
	if s := marshal(t, cb); !strings.Contains(s, `"timeUnit":"ms"`) || !strings.Contains(s, `"windowMs":5000`) {
		t.Errorf("columnar echo/ms wrong: %s", s)
	}
	unset := &ColumnarBuckets{WindowMs: 5000, StartMs: 0, Count: 3}
	if s := marshal(t, unset); strings.Contains(s, "timeUnit") {
		t.Errorf("unset ColumnarBuckets must omit timeUnit (WASM path): %s", s)
	}
}
