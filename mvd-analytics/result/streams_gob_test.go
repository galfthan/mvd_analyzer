package result

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// aliveStates is the three-state contract documented on PlayerStream.Alive:
// nil = liveness was not measurable (consumers degrade to ungated), empty =
// measured and never alive (consumers gate everything out), populated = the
// player's lives. Every transport has to keep them apart.
var aliveStates = []struct {
	name  string
	alive []Interval
}{
	{"unmeasured", nil},
	{"measured-never-alive", []Interval{}},
	{"lives", []Interval{{Start: 0, End: 1000}, {Start: 1000, End: 2000}}},
}

func checkAlive(t *testing.T, state string, want, got []Interval) {
	t.Helper()
	switch {
	case want == nil && got != nil:
		t.Errorf("%s: Alive came back non-nil (%v); a non-measurable liveness must stay nil", state, got)
	case want != nil && got == nil:
		t.Errorf("%s: Alive came back NIL; a measured liveness must never decode as unmeasured "+
			"(consumers read nil as ungated and treat the player as alive throughout)", state)
	case !reflect.DeepEqual(want, got):
		t.Errorf("%s: Alive = %v, want %v", state, got, want)
	}
}

// The reproduction: a bare gob of a PlayerStream. gob omits zero-valued
// struct fields and a length-0 slice is zero, so []Interval{} decodes as nil
// unless PlayerStream carries its own codec.
func TestPlayerStreamAliveSurvivesGob(t *testing.T) {
	for _, st := range aliveStates {
		t.Run(st.name, func(t *testing.T) {
			in := PlayerStream{Name: "p", Alive: st.alive}
			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(in); err != nil {
				t.Fatalf("encode: %v", err)
			}
			var got PlayerStream
			if err := gob.NewDecoder(&buf).Decode(&got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			checkAlive(t, st.name, st.alive, got.Alive)
		})
	}
}

// The transport that actually matters: mvd-api's tier-2 demo cache, which
// stores Streams as a gob (result/cache.go) and whose decoded Result answers
// region-control / trails / lazy-LOS liveness queries at API read time.
func TestCacheRoundTripPreservesAliveStates(t *testing.T) {
	in := &Result{SchemaVersion: CurrentSchemaVersion, Streams: &Streams{}}
	for _, st := range aliveStates {
		in.Streams.Players = append(in.Streams.Players, PlayerStream{
			Name:     st.name,
			Alive:    st.alive,
			Position: &PositionTrack{T: []int32{0, 100}, X: []float32{1, 2}, Y: []float32{3, 4}, Z: []float32{5, 6}},
			Deaths:   []int32{500},
		})
	}

	blob, err := EncodeCache(in)
	if err != nil {
		t.Fatalf("EncodeCache: %v", err)
	}
	out, err := DecodeCache(blob)
	if err != nil {
		t.Fatalf("DecodeCache: %v", err)
	}
	if len(out.Streams.Players) != len(aliveStates) {
		t.Fatalf("players = %d, want %d", len(out.Streams.Players), len(aliveStates))
	}
	for i, st := range aliveStates {
		p := out.Streams.Players[i]
		if p.Name != st.name {
			t.Fatalf("player %d: name = %q, want %q", i, p.Name, st.name)
		}
		checkAlive(t, st.name, st.alive, p.Alive)
		// The codec must carry the rest of the struct too, not just Alive.
		if p.Position == nil || len(p.Position.T) != 2 || p.Position.X[1] != 2 {
			t.Errorf("%s: position track did not survive: %+v", st.name, p.Position)
		}
		if !reflect.DeepEqual(p.Deaths, []int32{500}) {
			t.Errorf("%s: deaths = %v, want [500]", st.name, p.Deaths)
		}
	}
}

// The JSON contract is what consumers are pinned on, and it is unchanged by
// the gob codec: `alive` is never omitted and renders null / [] / [...].
func TestPlayerStreamAliveJSON(t *testing.T) {
	want := []string{`"alive":null`, `"alive":[]`, `"alive":[{"s":0,"e":1000},{"s":1000,"e":2000}]`}
	for i, st := range aliveStates {
		b, err := json.Marshal(PlayerStream{Name: "p", Alive: st.alive})
		if err != nil {
			t.Fatalf("%s: marshal: %v", st.name, err)
		}
		if !strings.Contains(string(b), want[i]) {
			t.Errorf("%s: json = %s, want it to contain %s", st.name, b, want[i])
		}
	}
}

// Drift guard for the codec above. PlayerStream.GobEncode carries exactly one
// explicit marker (Alive), because Alive is the only field whose empty and
// absent forms mean different things — which is visible in the JSON tags: it
// is the only slice on the struct deliberately NOT omitempty. A new
// non-omitempty slice field means a second state distinction gob will flatten,
// so it needs a marker too (or a justification for why nil and empty are the
// same answer there).
func TestPlayerStreamEmptyDistinctSlicesAreCoveredByGobCodec(t *testing.T) {
	covered := map[string]bool{"Alive": true}
	rt := reflect.TypeOf(PlayerStream{})
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Type.Kind() != reflect.Slice && f.Type.Kind() != reflect.Map {
			continue
		}
		tag := f.Tag.Get("json")
		if tag == "-" || strings.Contains(tag, ",omitempty") || covered[f.Name] {
			continue
		}
		t.Errorf("PlayerStream.%s is a slice/map that JSON always emits, so its empty form is "+
			"distinguishable from its absent form — but gob decodes both as nil. Either give it a "+
			"marker in PlayerStream.GobEncode/GobDecode, or add ,omitempty because the states are "+
			"the same answer.", f.Name)
	}
}
