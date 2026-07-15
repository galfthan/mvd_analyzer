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

func TestMsToUnit(t *testing.T) {
	// Native ms: the stored int32 is emitted verbatim as an integer.
	if got := marshal(t, MsToUnit(13155, UnitMs)); got != "13155" {
		t.Errorf("MsToUnit ms = %s, want 13155", got)
	}
	// Seconds: correctly-rounded division, JSON-clean (no ulp tail).
	if got := marshal(t, MsToUnit(13155, UnitSec)); got != "13.155" {
		t.Errorf("MsToUnit s = %s, want 13.155", got)
	}
	// Opt: zero is omitted (nil interface) under both units.
	if v := MsToUnitOpt(0, UnitSec); v != nil {
		t.Errorf("MsToUnitOpt(0) = %v, want nil", v)
	}
	if got := marshal(t, MsToUnitOpt(500, UnitSec)); got != "0.5" {
		t.Errorf("MsToUnitOpt(500) s = %s, want 0.5", got)
	}
}

func TestSecToUnitRoundTrip(t *testing.T) {
	// s→ms is lossless for values that originated from int32 ms.
	for _, ms := range []int32{0, 1, 499, 500, 13155, 240000, 1<<20 + 7, 2147483} {
		if got := SecToUnit(secs(ms), UnitMs); got != ms {
			t.Errorf("SecToUnit(secs(%d), ms) = %v, want %d", ms, got, ms)
		}
	}
	// Native seconds passes the float through unchanged.
	if got := marshal(t, SecToUnit(13.155, UnitSec)); got != "13.155" {
		t.Errorf("SecToUnit s = %s, want 13.155", got)
	}
}

func TestFragsUnitsEcho(t *testing.T) {
	fr := &result.FragResult{
		TotalFrags: 1,
		Frags:      []result.FragEntry{{Time: 12345, Killer: "a", Victim: "b", Weapon: "rl"}},
		ByWeapon:   map[string]int{"rl": 1},
		ByPlayer:   map[string]*result.PlayerFrags{},
	}
	// Native ms: timeUnit echo + integer time.
	msJSON := marshal(t, FragsUnits(fr, UnitMs))
	if !strings.Contains(msJSON, `"timeUnit":"ms"`) {
		t.Errorf("ms frags missing timeUnit echo: %s", msJSON)
	}
	if !strings.Contains(msJSON, `"time":12345`) {
		t.Errorf("ms frags time not int ms: %s", msJSON)
	}
	// Seconds: timeUnit echo + float time, same field name.
	sJSON := marshal(t, FragsUnits(fr, UnitSec))
	if !strings.Contains(sJSON, `"timeUnit":"s"`) || !strings.Contains(sJSON, `"time":12.345`) {
		t.Errorf("s frags wrong: %s", sJSON)
	}
	// nil frags (summary drop) survives as JSON null, not [].
	summary := &result.FragResult{TotalFrags: 1, ByWeapon: map[string]int{}, ByPlayer: map[string]*result.PlayerFrags{}}
	if got := marshal(t, FragsUnits(summary, UnitMs)); !strings.Contains(got, `"frags":null`) {
		t.Errorf("summary frags should be null: %s", got)
	}
}

func TestWeaponPickupsUnitsOmitempty(t *testing.T) {
	wps := []result.WeaponPickup{
		{Time: 1000, Player: "a", Weapon: "rl", Source: "world", NextDeathTime: 0, DropTime: 0},
		{Time: 2000, Player: "b", Weapon: "lg", Source: "backpack", NextDeathTime: 5000, DropTime: 1500},
	}
	// ms: 0-valued nextDeathTime/dropTime omitted (matching the stored
	// omitempty int32); non-zero present as int ms.
	msJSON := marshal(t, WeaponPickupsUnits(wps, UnitMs))
	if strings.Contains(msJSON, `"nextDeathTime":0`) || strings.Contains(msJSON, `"dropTime":0`) {
		t.Errorf("zero nextDeathTime/dropTime should be omitted: %s", msJSON)
	}
	if !strings.Contains(msJSON, `"nextDeathTime":5000`) || !strings.Contains(msJSON, `"dropTime":1500`) {
		t.Errorf("non-zero times missing: %s", msJSON)
	}
	if !strings.Contains(msJSON, `"pickups":[`) || !strings.Contains(msJSON, `"timeUnit":"ms"`) {
		t.Errorf("envelope wrong: %s", msJSON)
	}
	// s: non-zero times become seconds under the same names.
	sJSON := marshal(t, WeaponPickupsUnits(wps, UnitSec))
	if !strings.Contains(sJSON, `"nextDeathTime":5`) || !strings.Contains(sJSON, `"dropTime":1.5`) {
		t.Errorf("s times wrong: %s", sJSON)
	}
}

func TestEventsUnitsDetailConversion(t *testing.T) {
	ev := &EventsView{Events: []TaggedEvent{
		{T: 10.5, Type: "streak", Player: "a", Detail: map[string]any{
			"endTime": 20.25, "duration": 9.75, "length": 4,
		}},
	}}
	// Native seconds: t + detail sub-times stay float seconds.
	sJSON := marshal(t, EventsUnits(ev, UnitSec))
	if !strings.Contains(sJSON, `"timeUnit":"s"`) || !strings.Contains(sJSON, `"t":10.5`) ||
		!strings.Contains(sJSON, `"endTime":20.25`) || !strings.Contains(sJSON, `"duration":9.75`) {
		t.Errorf("s events wrong: %s", sJSON)
	}
	// units=ms: t AND the endTime/duration Detail sub-times convert to int ms;
	// the non-time detail key (length) is untouched.
	msJSON := marshal(t, EventsUnits(ev, UnitMs))
	if !strings.Contains(msJSON, `"timeUnit":"ms"`) || !strings.Contains(msJSON, `"t":10500`) ||
		!strings.Contains(msJSON, `"endTime":20250`) || !strings.Contains(msJSON, `"duration":9750`) ||
		!strings.Contains(msJSON, `"length":4`) {
		t.Errorf("ms events wrong: %s", msJSON)
	}
	// The conversion must not mutate the caller's shared Detail map.
	if ev.Events[0].Detail["endTime"] != 20.25 {
		t.Errorf("EventsUnits mutated the source Detail map")
	}
}

func TestStreamSliceUnitsDenseStaysMs(t *testing.T) {
	sl := &StreamSliceView{
		StartTime: 10.0,
		EndTime:   20.0,
		Players: []PlayerSlice{{
			Name:   "a",
			Health: []result.ChangeI16{{T: 10500, V: 100}, {T: 12250, V: 80}},
		}},
	}
	// units=ms: envelope startTime/endTime become int ms; the DENSE embedded
	// Health track keeps its int32 ms T verbatim (never converted).
	msJSON := marshal(t, StreamSliceUnits(sl, UnitMs))
	if !strings.Contains(msJSON, `"startTime":10000`) || !strings.Contains(msJSON, `"endTime":20000`) {
		t.Errorf("envelope not converted to ms: %s", msJSON)
	}
	if !strings.Contains(msJSON, `"t":10500`) || !strings.Contains(msJSON, `"t":12250`) {
		t.Errorf("dense health track should stay ms: %s", msJSON)
	}
	// Native seconds: envelope stays float seconds; dense track still ms.
	sJSON := marshal(t, StreamSliceUnits(sl, UnitSec))
	if !strings.Contains(sJSON, `"startTime":10`) || !strings.Contains(sJSON, `"t":10500`) {
		t.Errorf("s envelope / dense wrong: %s", sJSON)
	}
}
