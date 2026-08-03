package main

// Spec-lint: the `required` arrays the v65 contract rests on.
//
// TestOpenAPIGoldenResponsesValidate cannot see a LOOSENED `required`. A
// response that carries every documented field validates against a schema that
// demands them and equally against one that demands nothing, so deleting an
// entry from a `required` list is invisible to it — the golden bodies only
// catch the opposite mistake (an undeclared property, via
// additionalProperties: false). These assertions are the missing direction:
// they read the spec as data and pin the specific promises that would
// otherwise silently weaken.
//
// Deliberately surgical. This is not a snapshot of openapi.yaml — it names
// the handful of claims the API's callers were told they may rely on, and
// nothing else. It touches no `description` text, so it stays orthogonal to
// prose edits.

import (
	"sort"
	"testing"
)

// schemaAt walks components.schemas by key path, e.g.
// ("StateAt", "properties", "players", "additionalProperties").
func schemaAt(t *testing.T, name string, path ...string) map[string]any {
	t.Helper()
	sd := specDoc(t)
	node, ok := sd.defs[name].(map[string]any)
	if !ok {
		t.Fatalf("components.schemas.%s is missing", name)
	}
	walked := "components.schemas." + name
	for _, k := range path {
		next, ok := node[k].(map[string]any)
		if !ok {
			t.Fatalf("%s has no object at %q", walked, k)
		}
		node = next
		walked += "." + k
	}
	return node
}

// requiredSet reads a schema's `required` array as a set.
func requiredSet(t *testing.T, schema map[string]any, where string) map[string]bool {
	t.Helper()
	raw, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("%s declares no `required` array", where)
	}
	out := make(map[string]bool, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("%s has a non-string entry in `required`: %v", where, v)
		}
		out[s] = true
	}
	return out
}

func assertRequired(t *testing.T, schema map[string]any, where string, want ...string) {
	t.Helper()
	got := requiredSet(t, schema, where)
	for _, w := range want {
		if !got[w] {
			t.Errorf("%s: %q is not in `required` — the field is documented as always present, "+
				"and golden-response validation cannot catch its removal from this list", where, w)
		}
		if _, declared := schema["properties"].(map[string]any)[w]; !declared {
			t.Errorf("%s: `required` names %q but the schema declares no such property", where, w)
		}
	}
}

// `alive` is never field-gated on either per-player row: null is a state of
// its own ("liveness was not measurable"), so the key has to be present to
// carry it, and a consumer that asked for one unrelated field still gets it.
// Dropping it from `required` would make an absent key spec-legal and turn the
// three-state signal back into two.
func TestOpenAPIRequired_AliveOnPlayerRows(t *testing.T) {
	assertRequired(t,
		schemaAt(t, "StateAt", "properties", "players", "additionalProperties"),
		"StateAt player row", "alive")

	// The stream-slice row additionally always names its player — every other
	// field on the row is selectable, so without `name` a row could validate
	// while identifying nobody.
	assertRequired(t,
		schemaAt(t, "StreamSlice", "properties", "players", "items"),
		"StreamSlice player row", "name", "alive")
}

// IntervalStats' own prose is the contract under test: "Every NUMERIC field
// below is always present, including a measured zero. Read the envelope's
// `measured` block to tell 'this demo has no shot stream' from 'this player
// fired nothing' — never a field's absence."
//
// So the rule is mechanical rather than a hand-kept list: every scalar numeric
// property must be in `required`. A new always-emitted counter that forgets
// the list fails here, and so does a removal — either one would let absence
// start carrying meaning the `measured` block is supposed to own. (Maps and
// lists are exempt by the same paragraph: they ARE omitted when empty.)
func TestOpenAPIRequired_IntervalStatsNumerics(t *testing.T) {
	stats := schemaAt(t, "IntervalStats")
	props, ok := stats["properties"].(map[string]any)
	if !ok || len(props) == 0 {
		t.Fatal("components.schemas.IntervalStats declares no properties")
	}
	req := requiredSet(t, stats, "IntervalStats")

	var missing []string
	for name, raw := range props {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		// Only non-nullable scalar numerics: a `type` that is the bare string
		// "integer" or "number". A nullable field spells its type as a list
		// ["null", …], and objects/arrays/strings are not numerics.
		if ty, _ := p["type"].(string); ty != "integer" && ty != "number" {
			continue
		}
		if !req[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("IntervalStats numerics absent from `required`: %v — the schema's own text promises "+
			"every numeric field is always present, including a measured zero", missing)
	}

	// Guard against the rule passing vacuously if the numerics were ever
	// retyped: the block's load-bearing counters must be there by name.
	assertRequired(t, stats, "IntervalStats",
		"durationMs", "kills", "deaths", "teamKills", "suicides",
		"damageGiven", "damageTaken", "damageGivenTeam", "damageGivenSelf",
		"shots", "hits")

	// And the promise has to reach the wire shapes: both segmentations
	// compose the block, which is what makes one `required` list cover both
	// /top-windows rows and /lives rows.
	for _, row := range []string{"TopWindow", "Life"} {
		schema := schemaAt(t, row)
		branches, _ := schema["allOf"].([]any)
		found := false
		for _, b := range branches {
			bm, _ := b.(map[string]any)
			// specDoc rewrites #/components/schemas/ to #/$defs/.
			if ref, _ := bm["$ref"].(string); ref == "#/$defs/IntervalStats" {
				found = true
			}
		}
		if !found {
			t.Errorf("components.schemas.%s no longer composes IntervalStats (allOf $ref) — "+
				"the shared stats contract would stop applying to %s", row, row)
		}
	}
}

// A sanity check on the lint itself: the helpers must be reading real arrays.
// If specDoc ever stopped resolving (or the schemas moved), the assertions
// above would t.Fatal rather than pass silently, but a zero-length required
// list would not — so assert the counts are plausible.
func TestOpenAPIRequired_LintReadsRealArrays(t *testing.T) {
	for _, tc := range []struct {
		where string
		node  map[string]any
		min   int
	}{
		{"StateAt player row", schemaAt(t, "StateAt", "properties", "players", "additionalProperties"), 1},
		{"StreamSlice player row", schemaAt(t, "StreamSlice", "properties", "players", "items"), 2},
		{"IntervalStats", schemaAt(t, "IntervalStats"), 11},
	} {
		if n := len(requiredSet(t, tc.node, tc.where)); n < tc.min {
			t.Errorf("%s: `required` has %d entries, want at least %d", tc.where, n, tc.min)
		}
	}
}
