package main

// Golden-response validation: the teeth behind the full field-level
// response schemas in openapi/openapi.yaml. A committed golden-corpus
// Result is served through the REAL router (fakeStore + httptest), and
// every JSON response — success and error, across the shape-changing
// param variants — must validate against the spec's schema for that
// operation/status. OpenAPI 3.1 schemas ARE JSON Schema 2020-12, which
// github.com/google/jsonschema-go implements; gopkg.in/yaml.v3 reads the
// spec. Both are TEST-ONLY imports — the shipped binary embeds the spec
// as bytes and links neither.
//
// A coverage sweep at the end fails if a documented JSON 200 gains no
// validation case, so new endpoints must be added to the case table.

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mvd-analyzer/mvd-analytics/analyzer"
	"github.com/mvd-analyzer/mvd-analytics/result"
	"gopkg.in/yaml.v3"
)

// specDocument holds the parsed spec with schema refs rewritten to $defs
// form so any response schema + components.schemas is a self-contained
// JSON Schema 2020-12 document.
type specDocument struct {
	doc      map[string]any
	defs     map[string]any
	resolved map[string]*jsonschema.Resolved
}

var (
	specDocOnce sync.Once
	specDocVal  *specDocument
	specDocErr  error
)

func loadSpecDocument() (*specDocument, error) {
	var raw any
	if err := yaml.Unmarshal(openapiSpec, &raw); err != nil {
		return nil, fmt.Errorf("yaml parse: %w", err)
	}
	js, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("yaml->json: %w", err)
	}
	// Rewrite schema refs so each response schema can be resolved as a
	// standalone document with components.schemas as its $defs.
	js = bytes.ReplaceAll(js, []byte("#/components/schemas/"), []byte("#/$defs/"))
	var doc map[string]any
	if err := json.Unmarshal(js, &doc); err != nil {
		return nil, err
	}
	comps, _ := doc["components"].(map[string]any)
	defs, _ := comps["schemas"].(map[string]any)
	if len(defs) == 0 {
		return nil, fmt.Errorf("no components.schemas found")
	}
	return &specDocument{doc: doc, defs: defs, resolved: map[string]*jsonschema.Resolved{}}, nil
}

func specDoc(t *testing.T) *specDocument {
	t.Helper()
	specDocOnce.Do(func() { specDocVal, specDocErr = loadSpecDocument() })
	if specDocErr != nil {
		t.Fatalf("loading openapi.yaml: %v", specDocErr)
	}
	return specDocVal
}

// responseSchema returns the resolved JSON schema for (path, method,
// status), following a components.responses $ref if present. ok=false when
// the response deliberately has no JSON content (204, 304, non-JSON).
func (sd *specDocument) responseSchema(t *testing.T, path, method, status string) (*jsonschema.Resolved, bool) {
	t.Helper()
	key := method + " " + path + " " + status
	if r, hit := sd.resolved[key]; hit {
		return r, r != nil
	}

	pathItem, _ := sd.doc["paths"].(map[string]any)[path].(map[string]any)
	if pathItem == nil {
		t.Fatalf("spec has no path %q", path)
	}
	op, _ := pathItem[strings.ToLower(method)].(map[string]any)
	if op == nil {
		t.Fatalf("spec has no operation %s %s", method, path)
	}
	resp, _ := op["responses"].(map[string]any)[status].(map[string]any)
	if resp == nil {
		t.Fatalf("spec %s %s documents no %q response", method, path, status)
	}
	if ref, _ := resp["$ref"].(string); ref != "" {
		name, ok := strings.CutPrefix(ref, "#/components/responses/")
		if !ok {
			t.Fatalf("unexpected response $ref %q", ref)
		}
		comps := sd.doc["components"].(map[string]any)
		resp, _ = comps["responses"].(map[string]any)[name].(map[string]any)
		if resp == nil {
			t.Fatalf("unresolvable response component %q", name)
		}
	}
	content, _ := resp["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	schema, _ := media["schema"].(map[string]any)
	if schema == nil {
		sd.resolved[key] = nil
		return nil, false
	}

	standalone := make(map[string]any, len(schema)+1)
	for k, v := range schema {
		standalone[k] = v
	}
	standalone["$defs"] = sd.defs
	js, err := json.Marshal(standalone)
	if err != nil {
		t.Fatal(err)
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(js, &s); err != nil {
		t.Fatalf("schema for %s %s %s does not parse as JSON Schema: %v", method, path, status, err)
	}
	res, err := s.Resolve(nil)
	if err != nil {
		t.Fatalf("schema for %s %s %s does not resolve: %v", method, path, status, err)
	}
	sd.resolved[key] = res
	return res, true
}

// goldenResult loads the committed golden-corpus Result the validation
// server serves. The round-trip (Result -> golden JSON -> Result) must
// preserve the load-bearing sections — assert that up front so a lossy
// unmarshal fails loudly instead of validating empty bodies.
const goldenLabel = "4on4_osams_ra_230426_dm3"

var (
	goldenOnce sync.Once
	goldenVal  *result.Result
	goldenErr  error
)

func goldenResult(t *testing.T) *result.Result {
	t.Helper()
	goldenOnce.Do(func() {
		data, err := os.ReadFile("../mvd-analytics/testdata/golden/" + goldenLabel + ".json")
		if err != nil {
			goldenErr = err
			return
		}
		var r result.Result
		if err := json.Unmarshal(data, &r); err != nil {
			goldenErr = fmt.Errorf("golden unmarshal: %w", err)
			return
		}
		goldenVal = &r
	})
	if goldenErr != nil {
		t.Fatalf("loading golden corpus result: %v", goldenErr)
	}
	for name, missing := range map[string]bool{
		"frags":            goldenVal.Frags == nil,
		"damage":           goldenVal.Damage == nil,
		"items":            goldenVal.Items == nil,
		"timelineAnalysis": goldenVal.TimelineAnalysis == nil,
		"streams":          goldenVal.Streams == nil,
		"opening":          goldenVal.Opening == nil,
		"demoInfo":         goldenVal.DemoInfo == nil,
		"aim":              goldenVal.Aim == nil,
		"shots":            goldenVal.Shots == nil,
		"mapEntities":      goldenVal.MapEntities == nil,
	} {
		if missing {
			t.Fatalf("golden %s round-trip lost section %q — the validation server would serve an empty body", goldenLabel, name)
		}
	}
	return goldenVal
}

// addBoundedFamily augments a Result's Damage with a synthetic v54 bounded
// family so the dmg=both / dmg=bounded validation cases exercise the new
// schema. The committed golden JSON is still v53-shaped on this branch (a
// regen lands in a later commit), so without this a dmg=both request would
// show no bounded fields and a dmg=bounded request would 422 — neither would
// hit the additionalProperties/required rules the schema now carries. The
// figures are not analytically consistent (this is a schema fixture, not a
// numeric one): bounded == raw per hit, and each player's bounded nest mirrors
// its raw figures.
func addBoundedFamily(d *result.DamageResult) {
	d.Dmg = "both"
	d.BoundedMode = "standard"
	for i := range d.Events {
		b := d.Events[i].Damage
		d.Events[i].Bounded = &b
	}
	for _, p := range d.ByPlayer {
		nb := &result.PlayerDamage{
			Given: p.Given, Taken: p.Taken, GivenTeam: p.GivenTeam,
			GivenSelf: p.GivenSelf, TakenEnv: p.TakenEnv,
			ByWeapon:  map[string]int{},
			EnemyVsSG: p.EnemyVsSG, EnemyVsMid: p.EnemyVsMid, EnemyVsLG: p.EnemyVsLG,
			EnemyVsRL: p.EnemyVsRL, EnemyVsBoth: p.EnemyVsBoth, EWep: p.EWep,
		}
		for k, v := range p.ByWeapon {
			nb.ByWeapon[k] = v
		}
		p.Bounded = nb
	}
	teleB, stompB := 130, 45
	for i := range d.Telefrags {
		d.Telefrags[i].Bounded = &teleB
		d.Telefrags[i].VictimWep = "rl"
	}
	for i := range d.Stomps {
		d.Stomps[i].Bounded = &stompB
		d.Stomps[i].Damage = 60 // raw fold differs from bounded
	}
	if d.Scoreboard != nil {
		for _, dd := range d.Scoreboard.ByPlayer {
			dd.Bounded = &result.DamageDeltaBounded{
				StreamGiven: dd.StreamGiven, StreamTaken: dd.StreamTaken,
				StreamEWep: dd.StreamEWep, StreamTeam: 0, ScoreTeam: 0,
			}
		}
	}
}

// addDemoMarkers gives a Result's TimelineAnalysis a synthetic pair of
// `/demomark` bookmarks. Markers are rare (0 per demo across the whole golden
// corpus, including 4on4_osams_ra_230426_dm3), so the served timeline body
// never carried the field and TimelineAnalysis's additionalProperties: false
// went unexercised against it — which is how the schema shipped with no
// demoMarkers property at all while the /artifacts/timeline route was
// validated. The two records cover both shapes: an attributed player mark
// (no spectator/label) and a labelled spectator mark. Assignment, not append,
// so the two tests that share the cached golden can both call it.
func addDemoMarkers(ta *result.TimelineAnalysisResult) {
	ta.DemoMarkers = []result.DemoMarkerEvent{
		{Time: 61000, PlayerName: "nlk", PlayerSlot: 3, PlayerUserID: 17, Team: "bps"},
		{Time: -4200, PlayerName: "spec", PlayerSlot: 9, PlayerUserID: 42, Team: "", Spectator: true, Label: "0 round-07"},
	}
}

// allFieldCodes is every fields= selector, to exercise the widest
// stream-slice / state-at / buckets shapes.
const allFieldCodes = "h,a,at,li,pos,view,hgt,lq,vel,rl,lg,gl,ssg,sng,q,pe,r,sh,nl,rk,cl,sp,d"

type validationCase struct {
	name   string
	method string // "" = GET
	url    string // concrete request URL (path + query)
	path   string // spec path pattern
	body   []byte // request body (POST /v1/demos upload); nil for GETs
	status int
	// mustContain are substrings the response body has to carry. A case
	// exists to make the SCHEMA do work, and a body that omits the field the
	// case was added for validates just as happily as one that carries it —
	// this is how such a case says which field it is here for.
	mustContain []string
}

// gzipDemoBody is a tiny valid gzip stream used as the upload request body.
// fakeStore.PutDemo does not actually parse it (it registers a stub Result), so
// the bytes only need to be a decodable gzip so the handler's PutDemo path
// succeeds.
func gzipDemoBody(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write([]byte("upload-demo-fixture")); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func validationCases(t *testing.T) []validationCase {
	cases := []validationCase{
		{name: "health", url: "/healthz", path: "/healthz", status: 200},
		{name: "version", url: "/v1/version", path: "/v1/version", status: 200},
		{name: "auth-check", url: "/v1/auth/check", path: "/v1/auth/check", status: 204},
		{name: "artifacts-manifest", url: "/v1/artifacts", path: "/v1/artifacts", status: 200},
		{name: "graph", url: "/v1/graph", path: "/v1/graph", status: 200},
		{name: "load", method: "POST", url: "/v1/demos/gameId:42", path: "/v1/demos/{id}", status: 200},
		{name: "upload", method: "POST", url: "/v1/demos", path: "/v1/demos", body: gzipDemoBody(t), status: 200},

		// mustContain pins the v70 capability manifest. It replaced the
		// inlined topKills/topStreaks/topPowerups lists, and naming a
		// specific flag keeps the case honest: `available` is a required
		// object, but an empty one would still satisfy the schema, and the
		// whole value of the manifest is that its flags are actually filled
		// in from the 422 predicates.
		{name: "overview", url: "/v1/demos/gameId:42/overview", path: "/v1/demos/{id}/overview", status: 200,
			mustContain: []string{`"available":{"demoInfo":`, `"los":`}},
		{name: "demoinfo", url: "/v1/demos/gameId:42/demoinfo", path: "/v1/demos/{id}/demoinfo", status: 200},
		{name: "metadata", url: "/v1/demos/gameId:42/metadata", path: "/v1/demos/{id}/metadata", status: 200},
		// mustContain names the v66 identity export: both fields are
		// omitempty, so a regression that stopped emitting them would still
		// validate against the schema (the demoMarkers lesson).
		{name: "player-stats", url: "/v1/demos/gameId:42/player-stats", path: "/v1/demos/{id}/player-stats", status: 200,
			mustContain: []string{`"identity":"s`, `"sessions":[{"startMs":`, `"userId":`}},
		{name: "player-stats-filtered", url: "/v1/demos/gameId:42/player-stats?players=nlk", path: "/v1/demos/{id}/player-stats", status: 200},

		{name: "frags", url: "/v1/demos/gameId:42/frags", path: "/v1/demos/{id}/frags", status: 200},
		{name: "frags-summary", url: "/v1/demos/gameId:42/frags?summary=1", path: "/v1/demos/{id}/frags", status: 200},
		{name: "frags-filtered", url: "/v1/demos/gameId:42/frags?weapon=rl,lg&from=60&to=300", path: "/v1/demos/{id}/frags", status: 200},
		{name: "damage", url: "/v1/demos/gameId:42/damage", path: "/v1/demos/{id}/damage", status: 200},
		{name: "damage-summary", url: "/v1/demos/gameId:42/damage?summary=1", path: "/v1/demos/{id}/damage", status: 200},
		{name: "damage-filtered", url: "/v1/demos/gameId:42/damage?weapon=rl&from=60&to=300", path: "/v1/demos/{id}/damage", status: 200},
		// dmg families: both (raw fields + bounded nests + per-event bounded)
		// and bounded (materialized) must each satisfy the extended schema; an
		// unknown dmg is a 400 invalid_param; dmg=bounded on a skipped:* demo
		// is a 422 bounded_unavailable. The gameId:42 fixture is augmented with
		// a synthetic bounded family (addBoundedFamily) so these carry content.
		{name: "damage-dmg-both", url: "/v1/demos/gameId:42/damage?dmg=both", path: "/v1/demos/{id}/damage", status: 200},
		{name: "damage-dmg-bounded", url: "/v1/demos/gameId:42/damage?dmg=bounded", path: "/v1/demos/{id}/damage", status: 200},
		{name: "damage-dmg-invalid", url: "/v1/demos/gameId:42/damage?dmg=nope", path: "/v1/demos/{id}/damage", status: 400},
		{name: "err-bounded-unavailable", url: "/v1/demos/gameId:44/damage?dmg=bounded", path: "/v1/demos/{id}/damage", status: 422},
		{name: "shots", url: "/v1/demos/gameId:42/shots", path: "/v1/demos/{id}/shots", status: 200},
		{name: "aim", url: "/v1/demos/gameId:42/aim", path: "/v1/demos/{id}/aim", status: 200},
		{name: "aim-summary", url: "/v1/demos/gameId:42/aim?summary=1", path: "/v1/demos/{id}/aim", status: 200},
		{name: "aim-windowed", url: "/v1/demos/gameId:42/aim?from=60&to=300", path: "/v1/demos/{id}/aim", status: 200},

		{name: "loc-graph", url: "/v1/demos/gameId:42/loc-graph", path: "/v1/demos/{id}/loc-graph", status: 200},
		{name: "chat", url: "/v1/demos/gameId:42/chat", path: "/v1/demos/{id}/chat", status: 200},
		{name: "backpacks", url: "/v1/demos/gameId:42/backpacks", path: "/v1/demos/{id}/backpacks", status: 200},
		{name: "items", url: "/v1/demos/gameId:42/items", path: "/v1/demos/{id}/items", status: 200},
		{name: "items-summary", url: "/v1/demos/gameId:42/items?summary=1", path: "/v1/demos/{id}/items", status: 200},
		{name: "items-windowed", url: "/v1/demos/gameId:42/items?from=0&to=60&kinds=armor,mega", path: "/v1/demos/{id}/items", status: 200},
		// Empty result: {"items": []} matches both union branches — pins
		// the anyOf (not oneOf) spelling of the Items schema.
		{name: "items-empty", url: "/v1/demos/gameId:42/items?items=nosuchitem", path: "/v1/demos/{id}/items", status: 200},
		{name: "weapon-pickups", url: "/v1/demos/gameId:42/weapon-pickups", path: "/v1/demos/{id}/weapon-pickups", status: 200},
		{name: "weapon-pickups-world", url: "/v1/demos/gameId:42/weapon-pickups?source=world&weapon=rl", path: "/v1/demos/{id}/weapon-pickups", status: 200},

		{name: "buckets-columnar", url: "/v1/demos/gameId:42/buckets?windowMs=5000", path: "/v1/demos/{id}/buckets", status: 200},
		{name: "buckets-row", url: "/v1/demos/gameId:42/buckets?windowMs=5000&layout=row", path: "/v1/demos/{id}/buckets", status: 200},
		{name: "buckets-allfields", url: "/v1/demos/gameId:42/buckets?windowMs=30000&fields=" + allFieldCodes + "&includeTeam=1", path: "/v1/demos/{id}/buckets", status: 200},
		{name: "buckets-locindex-reducers", url: "/v1/demos/gameId:42/buckets?windowMs=10000&loc=index&reducers=h=min,a=last", path: "/v1/demos/{id}/buckets", status: 200},
		{name: "events", url: "/v1/demos/gameId:42/events", path: "/v1/demos/{id}/events", status: 200},
		{name: "events-locindex", url: "/v1/demos/gameId:42/events?loc=index&from=0&to=120", path: "/v1/demos/{id}/events", status: 200},
		{name: "stream-slice", url: "/v1/demos/gameId:42/stream-slice?from=10&to=20&fields=" + allFieldCodes, path: "/v1/demos/{id}/stream-slice", status: 200},
		// state-at rejects sp/d (no point-in-time meaning), so its widest
		// field set is allFieldCodes minus those two.
		{name: "state-at", url: "/v1/demos/gameId:42/state-at?time=30&fields=" + strings.TrimSuffix(allFieldCodes, ",sp,d"), path: "/v1/demos/{id}/state-at", status: 200},
		{name: "err-state-at-sp", url: "/v1/demos/gameId:42/state-at?time=30&fields=sp", path: "/v1/demos/{id}/state-at", status: 400},
		{name: "state-at-locindex", url: "/v1/demos/gameId:42/state-at?time=30&loc=index", path: "/v1/demos/{id}/state-at", status: 200},
		// The golden corpus (dm3) carries no position track, so every
		// state-at case above resolves no positional field and the schema's
		// posAgeMs / pos / view / vel branches went unexercised. gameId:45 is
		// a minimal streams-only Result WITH a track, requested off-sample so
		// posAgeMs is non-zero. mustContain keeps it from silently reverting
		// to a body that validates by carrying nothing.
		{name: "state-at-position-track", url: "/v1/demos/gameId:45/state-at?time=1500&fields=pos,view,hgt,lq,vel",
			path: "/v1/demos/{id}/state-at", status: 200,
			mustContain: []string{`"posAgeMs":500`, `"pos"`, `"view"`, `"vel"`, `"hgt"`, `"lq"`}},
		// los on gameId:42 (real streams, no test BSP) is a 422 los_unavailable
		// (Phase 3); gameId:43 has no Streams, so /los is a legitimate 200-empty
		// that still validates the Los schema (timeUnit + empty players array).
		{name: "los", url: "/v1/demos/gameId:43/los", path: "/v1/demos/{id}/los", status: 200},
		{name: "err-los-unavailable", url: "/v1/demos/gameId:42/los", path: "/v1/demos/{id}/los", status: 422},
		{name: "projectiles", url: "/v1/demos/gameId:42/streams/projectiles", path: "/v1/demos/{id}/streams/projectiles", status: 200},
		{name: "beams", url: "/v1/demos/gameId:42/streams/beams", path: "/v1/demos/{id}/streams/beams", status: 200},
		{name: "nails", url: "/v1/demos/gameId:42/streams/nails", path: "/v1/demos/{id}/streams/nails", status: 200},
		{name: "loc-trails", url: "/v1/demos/gameId:42/loc-trails?minDwellMs=500", path: "/v1/demos/{id}/loc-trails", status: 200},
		{name: "loc-table", url: "/v1/demos/gameId:42/loc-table", path: "/v1/demos/{id}/loc-table", status: 200},
		{name: "region-control", url: "/v1/demos/gameId:42/region-control?windowMs=5000", path: "/v1/demos/{id}/region-control", status: 200},
		{name: "region-control-summary", url: "/v1/demos/gameId:42/region-control?windowMs=5000&regions=summary", path: "/v1/demos/{id}/region-control", status: 200},
		{name: "region-control-none", url: "/v1/demos/gameId:42/region-control?windowMs=5000&regions=none", path: "/v1/demos/{id}/region-control", status: 200},
		{name: "top-windows", url: "/v1/demos/gameId:42/top-windows", path: "/v1/demos/{id}/top-windows", status: 200},
		{name: "top-windows-net", url: "/v1/demos/gameId:42/top-windows?metric=netFrags&windowMs=10000&perPlayer=1", path: "/v1/demos/{id}/top-windows", status: 200},
		{name: "top-windows-damage", url: "/v1/demos/gameId:42/top-windows?metric=damageGiven&windowMs=5000&weapons=rl,lg&limit=3", path: "/v1/demos/{id}/top-windows", status: 200},
		// The gap segmentation is the OTHER envelope shape: `windowMs` is
		// absent and `gapMs` present, so it exercises the half of the schema
		// the three fixed cases above cannot. mustContain names both echoes —
		// additionalProperties/required alone would validate a response that
		// silently fell back to fixed windows.
		{name: "top-windows-gap", url: "/v1/demos/gameId:42/top-windows?mode=gap&gapMs=10000&limit=5", path: "/v1/demos/{id}/top-windows", status: 200,
			mustContain: []string{`"mode":"gap"`, `"gapMs":10000`}},
		// top-kills: mustContain names the two fields that make the response
		// usable and that an empty `kills` array would validate without —
		// maxGapMs (the exact client-side narrowing filter) and returnDamage.
		{name: "top-kills", url: "/v1/demos/gameId:42/top-kills", path: "/v1/demos/{id}/top-kills", status: 200,
			mustContain: []string{`"maxGapMs":`, `"returnDamage":`}},
		{name: "top-kills-filtered", url: "/v1/demos/gameId:42/top-kills?weapons=rl&gapMs=2000&contestedMs=2000&minDamage=50&limit=5&dmg=bounded",
			path: "/v1/demos/{id}/top-kills", status: 200},
		{name: "lives", url: "/v1/demos/gameId:42/lives", path: "/v1/demos/{id}/lives", status: 200},
		{name: "lives-filtered", url: "/v1/demos/gameId:42/lives?minMs=1000&from=1000&to=500000", path: "/v1/demos/{id}/lives", status: 200},
		{name: "airgibs", url: "/v1/demos/gameId:42/airgibs", path: "/v1/demos/{id}/airgibs", status: 200},
		// The timeline artifact is already swept below with every other
		// servable artifact, but that generic case validates whatever the
		// body happens to carry. No corpus demo carries a /demomark bookmark,
		// so demoMarkers never reached the validator and its absence from the
		// TimelineAnalysis schema was invisible. This case names the field it
		// exists for, against the synthetic markers addDemoMarkers installs.
		{name: "artifact-timeline-demomarkers", url: "/v1/demos/gameId:42/artifacts/timeline",
			path: "/v1/demos/{id}/artifacts/{name}", status: 200,
			mustContain: []string{`"demoMarkers"`, `"playerUserID":17`, `"spectator":true`, `"label":"0 round-07"`}},

		{name: "games-search", url: "/v1/games/search?map=dm3&mode=4on4", path: "/v1/games/search", status: 200},
		// The timeUnit echo (schema v56) is asserted by the schema-validated
		// cases above: overview / events / state-at / stream-slice / loc-trails /
		// buckets-row / items-summary, the four list envelopes (chat, airgibs,
		// backpacks, weapon-pickups), and the four dense columnar stream bodies
		// (los, streams/projectiles, streams/beams, streams/nails) all mark
		// timeUnit `required`, so a 200 that validates confirms the fixed native
		// echo is present.

		{name: "map-entities", url: "/v1/maps/dm3/entities", path: "/v1/maps/{map}/entities", status: 200},
		{name: "map-entities-filtered", url: "/v1/maps/dm3/entities?types=item&kinds=armor,weapon", path: "/v1/maps/{map}/entities", status: 200},

		// Error paths — every body must validate against ErrorEnvelope.
		{name: "err-invalid-demo-id", url: "/v1/demos/bogus/overview", path: "/v1/demos/{id}/overview", status: 400},
		{name: "err-invalid-param", url: "/v1/demos/gameId:42/frags?from=abc", path: "/v1/demos/{id}/frags", status: 400},
		{name: "err-unknown-field", url: "/v1/demos/gameId:42/state-at?time=1&fields=loc", path: "/v1/demos/{id}/state-at", status: 400},
		{name: "err-missing-param", url: "/v1/demos/gameId:42/state-at", path: "/v1/demos/{id}/state-at", status: 400},
		{name: "err-demo-not-found", url: "/v1/demos/gameId:999/overview", path: "/v1/demos/{id}/overview", status: 404},
		{name: "err-artifact-unknown", url: "/v1/demos/gameId:42/artifacts/nope", path: "/v1/demos/{id}/artifacts/{name}", status: 404},
		{name: "err-artifact-params", url: "/v1/demos/gameId:42/artifacts/frag?x=1", path: "/v1/demos/{id}/artifacts/{name}", status: 400},
		{name: "err-map-unknown", url: "/v1/maps/nosuchmap/entities", path: "/v1/maps/{map}/entities", status: 404},
		{name: "err-geometry-unconfigured", url: "/v1/maps/dm3/geometry", path: "/v1/maps/{map}/geometry", status: 404},
		{name: "err-demoinfo-unavailable", url: "/v1/demos/gameId:43/demoinfo", path: "/v1/demos/{id}/demoinfo", status: 422},
		{name: "err-frags-unavailable", url: "/v1/demos/gameId:43/frags", path: "/v1/demos/{id}/frags", status: 422},
		{name: "err-region-control-unavailable", url: "/v1/demos/gameId:43/region-control", path: "/v1/demos/{id}/region-control", status: 422},
		{name: "err-opening-unavailable", url: "/v1/demos/gameId:43/artifacts/opening", path: "/v1/demos/{id}/artifacts/{name}", status: 422},
	}
	// Every servable artifact through the generic accessor.
	for _, m := range analyzer.ArtifactManifest() {
		if !m.Servable {
			continue
		}
		demo := "gameId:42"
		if m.Name == "los" {
			// los on gameId:42 (real streams, no test BSP) is 422 los_unavailable;
			// gameId:43 (no Streams) is a legitimate 200-empty los body.
			demo = "gameId:43"
		}
		cases = append(cases, validationCase{
			name:   "artifact-" + m.Name,
			url:    "/v1/demos/" + demo + "/artifacts/" + m.Name,
			path:   "/v1/demos/{id}/artifacts/{name}",
			status: 200,
		})
	}
	return cases
}

func TestOpenAPIGoldenResponsesValidate(t *testing.T) {
	sd := specDoc(t)
	res := goldenResult(t)
	// Give the served demo a v54 bounded family (the committed golden JSON is
	// still v53 on this branch) so the dmg=both / dmg=bounded cases exercise
	// the extended schema instead of validating empty additions.
	addBoundedFamily(res.Damage)
	// Same reason for demo markers: no corpus demo has one, so the timeline
	// artifact never exercised the demoMarkers half of its schema.
	addDemoMarkers(res.TimelineAnalysis)
	store := &fakeStore{byID: map[string]*result.Result{
		"gameId:42": res,
		// gameId:43 is a well-formed but capability-empty Result for the
		// 422 error paths.
		"gameId:43": {SchemaVersion: result.CurrentSchemaVersion},
		// gameId:44 carries damage whose bounded reconstruction was skipped —
		// dmg=bounded there is a 422 bounded_unavailable.
		"gameId:44": {SchemaVersion: result.CurrentSchemaVersion, Damage: &result.DamageResult{
			ByWeapon:    map[string]int{},
			ByPlayer:    map[string]*result.PlayerDamage{},
			BoundedMode: "skipped:midair",
		}},
		// gameId:45 carries the one thing the golden corpus does not: a
		// position track, with the optional h/lq/velocity columns, so the
		// positional half of the state-at schema (pos, view, hgt, lq, vel and
		// the posAgeMs that dates them all) gets validated against a real
		// response instead of an absent one.
		"gameId:45": {
			SchemaVersion: result.CurrentSchemaVersion,
			Streams: &result.Streams{
				Global: result.GlobalStream{MatchStart: 0, MatchEnd: 10000},
				Players: []result.PlayerStream{{
					Name:  "p1",
					Alive: []result.Interval{{Start: 0, End: 10000}},
					Position: &result.PositionTrack{
						T:   []int32{0, 1000, 2000},
						X:   []float32{0, 100, 200},
						Y:   []float32{0, 10, 20},
						Z:   []float32{0, -5, -10},
						H:   []float32{5, 40, 12},
						Lq:  []int8{0, 5, 7},
						VP:  []int16{10, 20, 30},
						VYa: []int16{-10, -20, -30},
						VX:  []float32{100, 200, 300},
						VY:  []float32{-100, -200, -300},
						VZ:  []float32{1, 2, 3},
					},
				}},
			},
		},
	}}
	srv := newTestServer(t, store)
	defer srv.Close()

	covered := map[string]bool{}
	for _, tc := range validationCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			method := tc.method
			if method == "" {
				method = http.MethodGet
			}
			var reqBody io.Reader
			if tc.body != nil {
				reqBody = bytes.NewReader(tc.body)
			}
			req, err := http.NewRequest(method, srv.URL+tc.url, reqBody)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != tc.status {
				t.Fatalf("%s %s = %d, want %d; body: %.300s", method, tc.url, resp.StatusCode, tc.status, body)
			}
			if tc.status == 200 {
				covered[method+" "+tc.path] = true
			}
			for _, want := range tc.mustContain {
				if !bytes.Contains(body, []byte(want)) {
					t.Errorf("response does not contain %s — the case would validate vacuously; body: %.300s", want, body)
				}
			}
			schema, hasJSON := sd.responseSchema(t, tc.path, method, fmt.Sprintf("%d", tc.status))
			if !hasJSON {
				if len(body) > 0 {
					t.Fatalf("spec documents no JSON body for %s %s %d but got %d bytes", method, tc.path, tc.status, len(body))
				}
				return
			}
			var instance any
			if err := json.Unmarshal(body, &instance); err != nil {
				t.Fatalf("response is not JSON: %v; body: %.300s", err, body)
			}
			if err := schema.Validate(instance); err != nil {
				t.Errorf("response does not validate against the spec schema:\n%v", err)
			}
		})
	}

	// Coverage sweep: every documented JSON 200 must have at least one
	// validation case above (non-JSON and portal-less ops excluded), so a
	// new endpoint cannot land schema-unvalidated.
	t.Run("coverage", func(t *testing.T) {
		exempt := map[string]bool{
			"GET /openapi.yaml":           true, // yaml, not JSON
			"GET /docs":                   true, // html
			"GET /docs/result-schema":     true, // html
			"GET /v1/auth/check":          true, // 204, validated above but no 200
			"GET /v1/maps/{map}/geometry": true, // needs -maps-dir; 404 path validated above
		}
		for op := range specPaths(t) {
			if exempt[op] {
				continue
			}
			if !covered[op] {
				t.Errorf("no 200 validation case exercises %q — add one to validationCases()", op)
			}
		}
	})
}

// TestTopWindowsEnvelopeExactlyOneKnob pins the oneOf in the TopWindows
// schema: exactly one of windowMs/gapMs accompanies mode, and the WRONG
// combinations must FAIL validation. The positive halves ride real router
// responses (so the fixtures cannot drift from the server); the negative
// halves are those same bodies with one key injected or deleted — without
// this, a schema that merely lists both knobs as optional properties
// validates every combination and the prose promise has no teeth.
func TestTopWindowsEnvelopeExactlyOneKnob(t *testing.T) {
	sd := specDoc(t)
	store := &fakeStore{byID: map[string]*result.Result{"gameId:42": goldenResult(t)}}
	srv := newTestServer(t, store)
	defer srv.Close()

	schema, hasJSON := sd.responseSchema(t, "/v1/demos/{id}/top-windows", http.MethodGet, "200")
	if !hasJSON {
		t.Fatal("top-windows documents no JSON 200 schema")
	}

	fetch := func(query string) map[string]any {
		t.Helper()
		resp, err := http.Get(srv.URL + "/v1/demos/gameId:42/top-windows" + query)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s = %d; body: %.300s", query, resp.StatusCode, body)
		}
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	fixed := fetch("?limit=3")
	gap := fetch("?mode=gap&gapMs=10000&limit=3")

	mutate := func(base map[string]any, f func(map[string]any)) map[string]any {
		clone := map[string]any{}
		for k, v := range base {
			clone[k] = v
		}
		f(clone)
		return clone
	}
	for _, tc := range []struct {
		name  string
		body  map[string]any
		valid bool
	}{
		{"fixed response", fixed, true},
		{"gap response", gap, true},
		{"fixed with gapMs injected", mutate(fixed, func(m map[string]any) { m["gapMs"] = 3000 }), false},
		{"fixed without windowMs", mutate(fixed, func(m map[string]any) { delete(m, "windowMs") }), false},
		{"gap with windowMs injected", mutate(gap, func(m map[string]any) { m["windowMs"] = 30000 }), false},
		{"gap without gapMs", mutate(gap, func(m map[string]any) { delete(m, "gapMs") }), false},
		{"fixed body claiming mode gap", mutate(fixed, func(m map[string]any) { m["mode"] = "gap" }), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Round-trip through JSON so injected Go ints become the same
			// float64s a real decoded response carries.
			js, err := json.Marshal(tc.body)
			if err != nil {
				t.Fatal(err)
			}
			var instance any
			if err := json.Unmarshal(js, &instance); err != nil {
				t.Fatal(err)
			}
			err = schema.Validate(instance)
			if tc.valid && err != nil {
				t.Errorf("must validate, got:\n%v", err)
			}
			if !tc.valid && err == nil {
				t.Errorf("must FAIL validation — the oneOf is not enforcing the knob exclusivity")
			}
		})
	}
}

// TestOpenAPIDescriptionCoverage enforces 100% description coverage: every
// property in components.schemas (recursively — through properties maps,
// items, additionalProperties, and oneOf/anyOf/allOf branches) and every
// components.parameters entry must carry a non-empty `description`. A property
// that is a pure `$ref` is exempt — its semantics live on the referenced
// schema. No allowlist; a new undocumented field fails the test with its
// schema-path anchor so the miss is easy to locate.
func TestOpenAPIDescriptionCoverage(t *testing.T) {
	sd := specDoc(t)

	var misses []string
	nonEmptyDesc := func(m map[string]any) bool {
		d, _ := m["description"].(string)
		return strings.TrimSpace(d) != ""
	}

	var walk func(node any, path string)
	walk = func(node any, path string) {
		m, ok := node.(map[string]any)
		if !ok {
			return
		}
		// Every entry of a `properties` map is a sub-schema that must carry a
		// description unless it is a pure $ref (semantics live on the target).
		if props, ok := m["properties"].(map[string]any); ok {
			// Deterministic order for stable failure output.
			keys := make([]string, 0, len(props))
			for k := range props {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				pm, _ := props[k].(map[string]any)
				sub := path + ".properties." + k
				if pm == nil {
					continue
				}
				if _, isRef := pm["$ref"]; !isRef && !nonEmptyDesc(pm) {
					misses = append(misses, sub)
				}
				walk(pm, sub)
			}
		}
		if items, ok := m["items"].(map[string]any); ok {
			walk(items, path+".items")
		}
		if ap, ok := m["additionalProperties"].(map[string]any); ok {
			walk(ap, path+".additionalProperties")
		}
		for _, kw := range []string{"oneOf", "anyOf", "allOf"} {
			if branches, ok := m[kw].([]any); ok {
				for i, b := range branches {
					walk(b, fmt.Sprintf("%s.%s[%d]", path, kw, i))
				}
			}
		}
	}

	// components.schemas — recurse each component.
	schemaNames := make([]string, 0, len(sd.defs))
	for name := range sd.defs {
		schemaNames = append(schemaNames, name)
	}
	sort.Strings(schemaNames)
	for _, name := range schemaNames {
		walk(sd.defs[name], "components.schemas."+name)
	}

	// components.parameters — each entry must have a description.
	comps, _ := sd.doc["components"].(map[string]any)
	if params, ok := comps["parameters"].(map[string]any); ok {
		pnames := make([]string, 0, len(params))
		for name := range params {
			pnames = append(pnames, name)
		}
		sort.Strings(pnames)
		for _, name := range pnames {
			pm, _ := params[name].(map[string]any)
			if pm != nil && !nonEmptyDesc(pm) {
				misses = append(misses, "components.parameters."+name)
			}
		}
	}

	if len(misses) > 0 {
		t.Errorf("%d schema properties / parameters lack a non-empty description:\n%s",
			len(misses), strings.Join(misses, "\n"))
	}
}

var _ = httptest.NewServer // silence unused-import drift if the helper moves

// queryParams returns an operation's documented query-parameter names and the
// subset marked required, resolving component $refs. Parameter refs keep the
// #/components/parameters/ form (loadSpecDocument only rewrites schema refs).
func (sd *specDocument) queryParams(t *testing.T, path, method string) (names []string, required map[string]bool) {
	t.Helper()
	required = map[string]bool{}
	pathItem, _ := sd.doc["paths"].(map[string]any)[path].(map[string]any)
	if pathItem == nil {
		t.Fatalf("spec has no path %q", path)
	}
	op, _ := pathItem[strings.ToLower(method)].(map[string]any)
	if op == nil {
		return nil, required
	}
	params, _ := op["parameters"].([]any)
	comps, _ := sd.doc["components"].(map[string]any)
	compParams, _ := comps["parameters"].(map[string]any)
	for _, pr := range params {
		pm, _ := pr.(map[string]any)
		if ref, ok := pm["$ref"].(string); ok {
			name, ok := strings.CutPrefix(ref, "#/components/parameters/")
			if !ok {
				t.Fatalf("unexpected parameter $ref %q", ref)
			}
			pm, _ = compParams[name].(map[string]any)
			if pm == nil {
				t.Fatalf("unresolvable parameter component %q", name)
			}
		}
		if pm["in"] != "query" {
			continue
		}
		nm, _ := pm["name"].(string)
		names = append(names, nm)
		if req, _ := pm["required"].(bool); req {
			required[nm] = true
		}
	}
	return names, required
}

// plausibleParamValue returns a value unlikely to be rejected as invalid_param
// for a documented query parameter, so a sweep request carrying it exercises
// name-acceptance (never unknown_param) rather than a value error. Only the
// unknown_param code fails the sweep assertion, so any value would technically
// do; these keep the requests realistic. `types` uses "chat", a valid value
// for both /events (an event type) and /chat (a message type).
func plausibleParamValue(name string) string {
	switch name {
	case "time":
		return "30"
	case "from":
		return "0"
	case "to":
		return "60"
	case "windowMs":
		return "5000"
	case "minDwellMs":
		return "100"
	case "limit", "offset":
		return "10"
	case "summary", "includeTeam", "roster":
		return "1"
	case "dmg":
		return "bounded"
	case "loc":
		return "name"
	case "layout":
		return "row"
	case "source":
		return "world"
	case "regions":
		return "full"
	case "fields":
		return "h"
	case "reducers":
		return "h=min"
	case "kinds":
		return "armor"
	case "items":
		return "ya"
	case "weapons", "weapon":
		return "rl"
	case "players", "teams":
		return "bps"
	case "map":
		return "dm3"
	case "mode":
		return "4on4"
	case "matchtag":
		return "x"
	case "nails":
		return "1"
	case "types":
		return "chat"
	}
	return "x"
}

// concreteGetPath fills the path params of a spec GET path pattern with the
// validation fixtures (gameId:42, dm3, the frag artifact).
func concreteGetPath(p string) string {
	p = strings.ReplaceAll(p, "{id}", "gameId:42")
	p = strings.ReplaceAll(p, "{map}", "dm3")
	p = strings.ReplaceAll(p, "{name}", "frag")
	return p
}

// fetchStatusAndCode issues a GET and returns the status plus the error-body
// code (empty for a non-error / non-JSON body).
func fetchStatusAndCode(t *testing.T, u string) (int, string) {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(body, &env)
	return resp.StatusCode, env.Error.Code
}

// TestOpenAPIParamSweep is the two-direction pin between the spec's declared
// query params and the handlers' consumed-key sets (Phase 1): (i) every
// documented query parameter on every governed GET must be accepted (never
// unknown_param); (ii) a bogus param on every governed GET must 400
// unknown_param. Meta/doc endpoints that do no param validation are excluded.
func TestOpenAPIParamSweep(t *testing.T) {
	sd := specDoc(t)
	res := goldenResult(t)
	addBoundedFamily(res.Damage)
	store := &fakeStore{byID: map[string]*result.Result{"gameId:42": res}}
	srv := newTestServer(t, store)
	defer srv.Close()

	exclude := map[string]bool{
		"GET /healthz":            true, // no params, no validation
		"GET /v1/version":         true,
		"GET /v1/auth/check":      true,
		"GET /openapi.yaml":       true, // served asset
		"GET /docs":               true,
		"GET /docs/result-schema": true,
	}

	withParams := func(base url.Values, extra ...[2]string) string {
		q := url.Values{}
		for k, vs := range base {
			q[k] = append([]string(nil), vs...)
		}
		for _, kv := range extra {
			q.Set(kv[0], kv[1])
		}
		return q.Encode()
	}

	swept := 0
	for op := range specPaths(t) {
		if !strings.HasPrefix(op, "GET ") || exclude[op] {
			continue
		}
		pathPattern := strings.TrimPrefix(op, "GET ")
		names, required := sd.queryParams(t, pathPattern, "GET")
		concrete := concreteGetPath(pathPattern)

		reqVals := url.Values{}
		for n := range required {
			reqVals.Set(n, plausibleParamValue(n))
		}

		// (i) each documented query param is accepted (never unknown_param).
		for _, n := range names {
			u := srv.URL + concrete + "?" + withParams(reqVals, [2]string{n, plausibleParamValue(n)})
			if _, code := fetchStatusAndCode(t, u); code == "unknown_param" {
				t.Errorf("%s: documented param %q returned unknown_param — its accessor is not marking the key", op, n)
			}
		}

		// (ii) a bogus param 400s unknown_param.
		u := srv.URL + concrete + "?" + withParams(reqVals, [2]string{"bogusparam987", "1"})
		status, code := fetchStatusAndCode(t, u)
		if status != http.StatusBadRequest || code != "unknown_param" {
			t.Errorf("%s: ?bogusparam987=1 = %d/%q, want 400/unknown_param", op, status, code)
		}
		swept++
	}
	if swept < 25 {
		t.Fatalf("param sweep covered only %d GET ops — has the enumeration regressed?", swept)
	}
}
