package result

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The bug this whole file guards: encoding/gob flattens pointers and
// omits zero values, so a pointer to the zero value decodes as nil. This
// pins the behaviour so nobody "simplifies" EncodeCache back to a bare
// gob on the assumption that gob is lossless.
func TestGobDropsPointerToZero(t *testing.T) {
	type probe struct {
		Zero    *int
		NonZero *int
	}
	z, nz := 0, 7
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(probe{Zero: &z, NonZero: &nz}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var got probe
	if err := gob.NewDecoder(&buf).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.NonZero == nil || *got.NonZero != 7 {
		t.Fatalf("non-zero pointer did not survive gob: %v", got.NonZero)
	}
	if got.Zero != nil {
		t.Skip("gob now preserves pointer-to-zero; EncodeCache could go back to gob-only")
	}
}

// Every optional scalar this schema uses to mean "measured, and the
// answer was zero" must survive the cache round-trip AS ZERO — not come
// back absent, which the schema documents as "not measurable".
func TestCacheRoundTripPreservesMeasuredZeros(t *testing.T) {
	z := func() *int { v := 0; return &v }
	z32 := func() *int32 { v := int32(0); return &v }
	zf := func() *float64 { v := 0.0; return &v }

	in := &Result{
		SchemaVersion: CurrentSchemaVersion,
		Metadata:      &MetadataResult{MatchSettings: &MatchSettings{SpawnK: z()}},
		DemoInfo: &DemoInfoResult{Players: []DemoInfoPlayer{{
			Name: "p", Control: zf(), Speed: &DemoInfoSpeed{}, Bot: &DemoInfoBot{},
		}}},
		Damage: &DamageResult{
			Events:    []DamageEntry{{Attacker: "a", Victim: "b", Bounded: z()}},
			Telefrags: []PositionalKill{{Attacker: "a", Victim: "b", Bounded: z()}},
		},
		PlayerStats: &PlayerStatsResult{Players: []PlayerStatsRow{{
			Name:      "p",
			ControlMs: z32(),
			Score:     PlayerStatsScore{Kills: z(), MaxSpree: z(), MaxQuadSpree: z()},
			Speed:     &PlayerStatsSpeed{},
			Damage: &PlayerStatsDamage{
				Taken: z(), TakenEnemy: z(), TakenToDie: z(), TeamWeapons: z(),
			},
			Accuracy: &PlayerStatsAccuracy{ByWeapon: map[string]PlayerStatsAcc{
				"rl": {Attacks: 10, Hits: z(), Real: z(), Virtual: z()},
			}},
			Pickups: &PlayerStatsPickups{ByKind: map[string]PlayerStatsPickup{
				"rl": {Took: 1, Xfer: z(), XferSelf: z()},
			}},
		}}},
	}

	blob, err := EncodeCache(in)
	if err != nil {
		t.Fatalf("EncodeCache: %v", err)
	}
	out, err := DecodeCache(blob)
	if err != nil {
		t.Fatalf("DecodeCache: %v", err)
	}

	row := out.PlayerStats.Players[0]
	checks := []struct {
		name string
		got  any
	}{
		{"metadata.matchSettings.spawnK", out.Metadata.MatchSettings.SpawnK},
		{"demoInfo.players[0].control", out.DemoInfo.Players[0].Control},
		{"demoInfo.players[0].speed", out.DemoInfo.Players[0].Speed},
		{"demoInfo.players[0].bot", out.DemoInfo.Players[0].Bot},
		{"damage.events[0].bounded", out.Damage.Events[0].Bounded},
		{"damage.telefrags[0].bounded", out.Damage.Telefrags[0].Bounded},
		{"playerStats.players[0].controlMs", row.ControlMs},
		{"playerStats…score.maxSpree", row.Score.MaxSpree},
		{"playerStats…score.maxQuadSpree", row.Score.MaxQuadSpree},
		{"playerStats.players[0].speed", row.Speed},
		{"playerStats…damage.taken", row.Damage.Taken},
		{"playerStats…damage.takenEnemy", row.Damage.TakenEnemy},
		{"playerStats…damage.takenToDie", row.Damage.TakenToDie},
		{"playerStats…damage.teamWeapons", row.Damage.TeamWeapons},
		{"playerStats…accuracy.rl.hits", row.Accuracy.ByWeapon["rl"].Hits},
		{"playerStats…accuracy.rl.real", row.Accuracy.ByWeapon["rl"].Real},
		{"playerStats…accuracy.rl.virtual", row.Accuracy.ByWeapon["rl"].Virtual},
		{"playerStats…pickups.rl.xfer", row.Pickups.ByKind["rl"].Xfer},
		{"playerStats…pickups.rl.xferSelf", row.Pickups.ByKind["rl"].XferSelf},
	}
	for _, c := range checks {
		if reflect.ValueOf(c.got).IsNil() {
			t.Errorf("%s: came back ABSENT after the cache round-trip; a measured zero must stay zero", c.name)
		}
	}
}

// The invariant that actually matters to a consumer: a cache hit must
// serve the same bytes as a cold parse. Runs over the committed golden
// corpus, which is real pipeline output across duels, teamplay, demos
// with and without a KTX block, and demos with no damage stream.
func TestCacheRoundTripPreservesServedBytes(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "testdata", "golden", "*.json"))
	if err != nil || len(paths) == 0 {
		t.Skipf("no golden corpus available (%v)", err)
	}
	for _, p := range paths {
		t.Run(strings.TrimSuffix(filepath.Base(p), ".json"), func(t *testing.T) {
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var cold Result
			if err := json.Unmarshal(raw, &cold); err != nil {
				t.Fatalf("unmarshal golden: %v", err)
			}
			// Marshal the loaded form (not the file) as the baseline, so
			// this measures the cache round-trip alone rather than any
			// JSON-decode asymmetry the golden file may carry.
			want, err := json.Marshal(&cold)
			if err != nil {
				t.Fatalf("marshal cold: %v", err)
			}
			blob, err := EncodeCache(&cold)
			if err != nil {
				t.Fatalf("EncodeCache: %v", err)
			}
			warm, err := DecodeCache(blob)
			if err != nil {
				t.Fatalf("DecodeCache: %v", err)
			}
			got, err := json.Marshal(warm)
			if err != nil {
				t.Fatalf("marshal warm: %v", err)
			}
			if !bytes.Equal(want, got) {
				t.Errorf("served bytes differ after a cache round-trip (%d vs %d bytes); first divergence: %s",
					len(want), len(got), firstJSONDiff(want, got))
			}
		})
	}
}

// Drift guard, inverted: JSON is the default and is safe, so the ONLY
// constraint is on Streams — the one section gob still owns, for the 97%
// of the bytes it holds. An optional scalar added there would be lost on
// every cache hit; anywhere else it is automatically fine.
func TestStreamsHasNoOptionalScalars(t *testing.T) {
	var offenders []string
	seen := map[reflect.Type]bool{}

	var walk func(t reflect.Type, path string)
	walk = func(t reflect.Type, path string) {
		for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
			t = t.Elem()
		}
		if t.Kind() == reflect.Map {
			walk(t.Elem(), path+"[]")
			return
		}
		if t.Kind() != reflect.Struct || seen[t] {
			return
		}
		seen[t] = true
		defer delete(seen, t)

		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" { // unexported
				continue
			}
			fp := path + "." + f.Name
			if f.Type.Kind() == reflect.Ptr && isScalar(f.Type.Elem().Kind()) {
				offenders = append(offenders, fp)
				continue
			}
			walk(f.Type, fp)
		}
	}
	walk(reflect.TypeOf(Streams{}), "Streams")

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("Streams is the one section the cache still stores as gob, and gob drops a pointer to a zero value.\n"+
			"These optional scalars would come back ABSENT on every cache hit — move them out of Streams, or move\n"+
			"Streams into the JSON half of result/cache.go and accept the decode cost:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

func isScalar(k reflect.Kind) bool {
	switch k {
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func TestDecodeCacheRejectsForeignPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(&Result{SchemaVersion: 1}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := DecodeCache(buf.Bytes()); err == nil {
		t.Error("a bare gob (pre-patch cache file) decoded without error; it must be treated as a miss")
	}
	if _, err := DecodeCache([]byte("xx")); err == nil {
		t.Error("a short payload decoded without error")
	}
}

// firstJSONDiff reports a short window around the first differing byte.
func firstJSONDiff(a, b []byte) string {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			lo := i - 60
			if lo < 0 {
				lo = 0
			}
			hi := i + 60
			return fmt.Sprintf("at byte %d\n  cold: …%s…\n  warm: …%s…", i,
				string(a[lo:min(hi, len(a))]), string(b[lo:min(hi, len(b))]))
		}
	}
	return "one is a prefix of the other"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
