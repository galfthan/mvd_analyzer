package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestInputSchemasHaveNoNullUnions guards the array/map filter params against
// the jsonschema-go regression that broke them in production: a nilable Go
// slice/map reflects to a ["null", X] type union, which several MCP clients
// coerce to a string, silently disabling filters like players/fields/weapon.
// addTool/stripNullTypes collapse those unions; this pins the result.
func TestInputSchemasHaveNoNullUnions(t *testing.T) {
	for name, s := range map[string]*jsonschema.Schema{
		"getBuckets":       inputSchema[GetBucketsInput](), // players, fields, reducers(map)
		"getEvents":        inputSchema[GetEventsInput](),  // players, types
		"getFrags":         inputSchema[GetFragsInput](),   // players, weapon
		"getDamage":        inputSchema[GetDamageInput](),  // players, weapons, dmg
		"getItems":         inputSchema[GetItemsInput](),   // items, players, kinds
		"getWeaponPickups": inputSchema[GetWeaponPickupsInput](),
		// top-windows also carries *int params (windowMs/limit/min), which
		// reflect to a ["null","integer"] union before stripping.
		"getTopWindows": inputSchema[GetTopWindowsInput](),
		// top-kills likewise carries *int params (gapMs/contestedMs/limit).
		"getTopKills": inputSchema[GetTopKillsInput](),
		"getLives":    inputSchema[GetLivesInput](),
	} {
		assertNoNullTypes(t, name, s)
	}

	// The damage-family selector reflects to a plain (non-nullable) string.
	if d := inputSchema[GetDamageInput]().Properties["dmg"]; d == nil || d.Type != "string" || len(d.Types) != 0 {
		t.Errorf("getDamage.dmg is not a clean string: %+v", d)
	}

	// Spot-check the concrete shape: players is a plain array of strings.
	pl := inputSchema[GetEventsInput]().Properties["players"]
	if pl == nil || pl.Type != "array" || len(pl.Types) != 0 || pl.Items == nil || pl.Items.Type != "string" {
		t.Errorf("getEvents.players is not a clean string array: %+v", pl)
	}
	// The reducers map is a plain object.
	if r := inputSchema[GetBucketsInput]().Properties["reducers"]; r == nil || r.Type != "object" || len(r.Types) != 0 {
		t.Errorf("getBuckets.reducers is not a clean object: %+v", r)
	}
}

func assertNoNullTypes(t *testing.T, path string, s *jsonschema.Schema) {
	t.Helper()
	if s == nil {
		return
	}
	for _, tp := range s.Types {
		if tp == "null" {
			t.Errorf("%s: schema still carries a \"null\" type union: %v", path, s.Types)
		}
	}
	for k, p := range s.Properties {
		assertNoNullTypes(t, path+"."+k, p)
	}
	assertNoNullTypes(t, path+".items", s.Items)
	assertNoNullTypes(t, path+".additionalProperties", s.AdditionalProperties)
	for i, p := range s.PrefixItems {
		assertNoNullTypes(t, path+".prefixItems", p)
		_ = i
	}
}

// fakeBackend implements MCPBackend with canned responses, so the
// tool-registration tests don't need an HTTP server.
type fakeBackend struct {
	loadErr error
}

func (f *fakeBackend) LoadDemo(_ context.Context, in LoadDemoInput) (*LoadDemoOutput, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	if in.GameID == 0 && in.SHA256 == "" {
		return nil, errors.New("exactly one of gameId or sha256 must be set")
	}
	return &LoadDemoOutput{
		DemoID:        "sha:" + strings.Repeat("a", 64),
		SHA256:        strings.Repeat("a", 64),
		FromCache:     true,
		SchemaVersion: 7,
	}, nil
}

func (f *fakeBackend) GetOverview(_ context.Context, _ GetOverviewInput) (any, error) {
	return map[string]any{"schemaVersion": 7, "map": "dm6", "duration": 600.0}, nil
}

func (f *fakeBackend) GetBuckets(_ context.Context, _ GetBucketsInput) (any, error) {
	return map[string]any{"windowMs": 50, "buckets": []any{}}, nil
}
func (f *fakeBackend) GetEvents(_ context.Context, _ GetEventsInput) (any, error) {
	return map[string]any{"events": []any{}}, nil
}
func (f *fakeBackend) GetStreamSlice(_ context.Context, _ GetStreamSliceInput) (any, error) {
	return map[string]any{"players": []any{}}, nil
}
func (f *fakeBackend) GetPointEffects(_ context.Context, _ GetPointEffectsInput) (any, error) {
	return map[string]any{"pointEffects": nil}, nil
}
func (f *fakeBackend) GetStateAt(_ context.Context, in GetStateAtInput) (any, error) {
	return map[string]any{"time": in.Time, "players": map[string]any{}}, nil
}
func (f *fakeBackend) GetLocTrails(_ context.Context, _ GetLocTrailsInput) (any, error) {
	return map[string]any{"players": []any{}}, nil
}
func (f *fakeBackend) GetLocTable(_ context.Context, _ GetLocTableInput) (any, error) {
	return map[string]any{"locTable": []any{"", "ra", "ya"}}, nil
}
func (f *fakeBackend) GetRegionControl(_ context.Context, _ GetRegionControlInput) (any, error) {
	return map[string]any{"regions": []any{}, "stats": map[string]any{}}, nil
}
func (f *fakeBackend) GetTopWindows(_ context.Context, _ GetTopWindowsInput) (any, error) {
	return map[string]any{"metric": "frags", "windowMs": 30000, "windows": []any{}}, nil
}
func (f *fakeBackend) GetTopKills(_ context.Context, _ GetTopKillsInput) (any, error) {
	return map[string]any{"gapMs": 3000, "contestedMs": 4000, "kills": []any{}}, nil
}
func (f *fakeBackend) GetLives(_ context.Context, _ GetLivesInput) (any, error) {
	return map[string]any{"lives": []any{}}, nil
}
func (f *fakeBackend) GetDemoInfo(_ context.Context, _ GetDemoInfoInput) (any, error) {
	return map[string]any{"version": 3, "mode": "4on4", "players": []any{}}, nil
}
func (f *fakeBackend) GetMetadata(_ context.Context, _ GetMetadataInput) (any, error) {
	return map[string]any{"matchSettings": map[string]any{"mode": "4on4"}}, nil
}
func (f *fakeBackend) GetPlayerStats(_ context.Context, _ GetPlayerStatsInput) (any, error) {
	return map[string]any{"players": []any{}, "sources": map[string]any{"score": "derived", "hold": "derived"}}, nil
}
func (f *fakeBackend) GetFrags(_ context.Context, _ GetFragsInput) (any, error) {
	return map[string]any{"totalFrags": 165, "byWeapon": map[string]any{"rl": 100}}, nil
}
func (f *fakeBackend) GetDamage(_ context.Context, _ GetDamageInput) (any, error) {
	return map[string]any{"totalDamage": 50000, "byWeapon": map[string]any{"rl": 30000}}, nil
}
func (f *fakeBackend) GetAim(_ context.Context, _ GetAimInput) (any, error) {
	return map[string]any{"players": []any{map[string]any{"player": "gore", "mode": "team"}}}, nil
}
func (f *fakeBackend) GetLocGraph(_ context.Context, _ GetLocGraphInput) (any, error) {
	return map[string]any{"locs": []any{}, "edges": []any{}}, nil
}
func (f *fakeBackend) GetChat(_ context.Context, _ GetChatInput) (any, error) {
	return []any{}, nil
}
func (f *fakeBackend) GetBackpacks(_ context.Context, _ GetBackpacksInput) (any, error) {
	return []any{}, nil
}
func (f *fakeBackend) GetItems(_ context.Context, _ GetItemsInput) (any, error) {
	return map[string]any{"items": []any{}}, nil
}
func (f *fakeBackend) GetMapEntitiesByMap(_ context.Context, _ GetMapEntitiesByMapInput) (any, error) {
	return map[string]any{"entities": []any{}}, nil
}
func (f *fakeBackend) GetWeaponPickups(_ context.Context, _ GetWeaponPickupsInput) (any, error) {
	return []any{}, nil
}
func (f *fakeBackend) ListArtifacts(_ context.Context, _ ListArtifactsInput) (any, error) {
	return map[string]any{"schemaVersion": 49, "artifacts": []any{
		map[string]any{"name": "frag", "servable": true, "resultKey": "frags"},
	}}, nil
}
func (f *fakeBackend) GetArtifact(_ context.Context, in GetArtifactInput) (any, error) {
	if in.Name == "" {
		return nil, errors.New("name required")
	}
	return map[string]any{in.Name: map[string]any{}}, nil
}

// fakeSearcher is the default no-op searcher for backend-focused tests.
type fakeSearcher struct {
	out any
	err error
}

func (f *fakeSearcher) Search(_ context.Context, _ SearchGamesInput) (any, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.out == nil {
		return map[string]any{"limit": 20, "offset": 0, "count": 0, "games": []any{}}, nil
	}
	return f.out, nil
}

// testMCPSession spins up an MCP server with the given backend on an
// in-memory transport and returns a connected client session ready
// for tools/list / tools/call.
func testMCPSession(t *testing.T, backend MCPBackend) *mcp.ClientSession {
	return testMCPSessionWith(t, backend, &fakeSearcher{})
}

func testMCPSessionWith(t *testing.T, backend MCPBackend, sr searcher) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()

	srv := mcp.NewServer(&mcp.Implementation{Name: "mvd-mcp-test", Version: "test"}, nil)
	registerTools(srv, backend, sr)

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- srv.Run(context.Background(), serverTransport)
	}()
	t.Cleanup(func() {
		select {
		case err := <-serverErrCh:
			if err != nil {
				t.Logf("mcp server exited with: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Logf("mcp server did not exit within 2s")
		}
	})

	client := mcp.NewClient(&mcp.Implementation{Name: "mvd-mcp-test-client", Version: "test"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestMCP_ListTools(t *testing.T) {
	sess := testMCPSession(t, &fakeBackend{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	want := []string{
		"searchGames", "loadDemo",
		"getOverview", "getDemoInfo", "getMetadata", "getPlayerStats", "getFrags", "getDamage",
		"getAim", "getLocGraph", "getChat",
		"getBackpacks", "getItems", "getMapEntitiesByMap", "getWeaponPickups",
		"getBuckets", "getEvents", "getStreamSlice", "getPointEffects", "getStateAt",
		"getLocTrails", "getLocTable", "getRegionControl",
		"getTopWindows", "getTopKills", "getLives",
		"listArtifacts", "getArtifact",
	}
	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
		// Every tool is read-only (analytics, no user-facing mutation) so
		// clients can reduce per-call approval prompts.
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("tool %q missing readOnlyHint annotation", tool.Name)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d tools; want %d (names=%v)", len(got), len(want), toolNames(res.Tools))
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("missing tool %q", name)
		}
	}
}

func toolNames(tools []*mcp.Tool) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

func TestMCP_LoadDemo_RequiresIdentifier(t *testing.T) {
	sess := testMCPSession(t, &fakeBackend{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "loadDemo",
		Arguments: map[string]any{}, // neither gameId nor sha256
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected isError=true for missing identifier")
	}
}

func TestMCP_LoadDemo_HappyPath(t *testing.T) {
	sess := testMCPSession(t, &fakeBackend{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "loadDemo",
		Arguments: map[string]any{"gameId": 42},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("isError=true; expected success. content=%+v", res.Content)
	}
	var out LoadDemoOutput
	mustDecodeStructured(t, res, &out)
	if out.SHA256 == "" {
		t.Errorf("SHA256 empty in load output: %+v", out)
	}
	if !strings.HasPrefix(out.DemoID, "sha:") {
		t.Errorf("DemoID = %q; expected sha: prefix", out.DemoID)
	}
}

func TestMCP_GetOverview(t *testing.T) {
	sess := testMCPSession(t, &fakeBackend{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "getOverview",
		Arguments: map[string]any{"demoId": "gameId:42"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("isError=true; expected success. content=%+v", res.Content)
	}
	var out map[string]any
	mustDecodeStructured(t, res, &out)
	if out["map"] != "dm6" {
		t.Errorf("Map = %v; want dm6", out["map"])
	}
}

func TestMCP_GetStateAt(t *testing.T) {
	sess := testMCPSession(t, &fakeBackend{})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name: "getStateAt",
		Arguments: map[string]any{
			"demoId": "gameId:42", "time": 15.0, "players": []any{"bps"}, "fields": []any{"h", "a"},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("isError=true; content=%+v", res.Content)
	}
	if res.StructuredContent == nil {
		t.Errorf("StructuredContent missing")
	}
}

func TestMCP_LoadDemo_BackendError(t *testing.T) {
	sess := testMCPSession(t, &fakeBackend{loadErr: errors.New("demo not found")})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "loadDemo",
		Arguments: map[string]any{"gameId": 9999},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected isError=true on backend error")
	}
}

// TestMCP_TopWindows_OmittedIntsNotSent pins the omitted-integer contract
// end to end (client JSON -> tool input struct -> proxy query): an argument
// the caller never mentioned must NOT reach mvd-api as 0, because 0 is a
// rejected value on windowMs/limit and a meaningful filter on min. An
// EXPLICIT 0 must reach it, so the caller earns the documented 400 rather
// than silently getting the default.
func TestMCP_TopWindows_OmittedIntsNotSent(t *testing.T) {
	var seen url.Values
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query()
		w.Write([]byte(`{"windows":[]}`))
	}))
	defer api.Close()
	sess := testMCPSession(t, newProxyBackend(api.URL, "", 5*time.Second))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	call := func(args map[string]any) {
		t.Helper()
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "getTopWindows", Arguments: args})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if res.IsError {
			t.Fatalf("isError=true; content=%+v", res.Content)
		}
	}

	// Nothing but demoId: no integer param may appear at all.
	call(map[string]any{"demoId": "gameId:42"})
	for _, k := range []string{"windowMs", "gapMs", "limit", "perPlayer", "minScore", "from", "to"} {
		if seen.Has(k) {
			t.Errorf("omitted %s reached the API as %q; it must stay out of the query", k, seen.Get(k))
		}
	}

	// Explicit zeros are forwarded verbatim (windowMs/limit earn their 400,
	// minScore:0 is a real "keep zero-scoring windows" filter). perPlayer is
	// the deliberate exception: REST rejects an explicit 0 there too, but
	// `intv` drops it before it can be sent, so from this shim perPlayer:0 is
	// indistinguishable from omitting it and lands on the uncapped default.
	call(map[string]any{"demoId": "gameId:42", "windowMs": 0, "gapMs": 0, "limit": 0, "minScore": 0, "perPlayer": 0})
	if seen.Has("perPlayer") {
		t.Errorf("perPlayer:0 reached the API as %q; intv must drop it", seen.Get("perPlayer"))
	}
	for _, k := range []string{"windowMs", "gapMs", "limit", "minScore"} {
		if seen.Get(k) != "0" {
			t.Errorf("explicit %s:0 forwarded as %q; want \"0\"", k, seen.Get(k))
		}
	}

	// A populated call encodes every param under its REST name.
	call(map[string]any{
		"demoId": "gameId:42", "metric": "netFrags", "windowMs": 5000, "limit": 25,
		"perPlayer": 3, "players": []any{"bps"}, "weapons": []any{"rl"},
		"startTime": 60000, "endTime": 120000, "dmg": "raw", "minScore": 2,
	})
	for k, want := range map[string]string{
		"metric": "netFrags", "windowMs": "5000", "limit": "25", "perPlayer": "3",
		"players": "bps", "weapons": "rl", "from": "60000", "to": "120000",
		"dmg": "raw", "minScore": "2",
	} {
		if got := seen.Get(k); got != want {
			t.Errorf("top-windows %s = %q; want %q", k, got, want)
		}
	}
	if seen.Has("mode") {
		t.Errorf("omitted mode reached the API as %q; an empty string must stay out so REST's default (fixed) applies", seen.Get("mode"))
	}
}

// TestMCP_TopWindows_GapModeForwarding pins the second segmentation's own
// forwarding shape: mode rides as a plain string (the API owns the
// vocabulary), gapMs rides *int through intp, and the two travel together
// with NO windowMs — sending one there would earn the conflict 400 for a
// caller who never asked for it. The omitted-gapMs case is the load-bearing
// one: gap mode has no default, so a dropped-to-0 gapMs would turn the 400
// that names the per-metric starting points into a bare range error.
func TestMCP_TopWindows_GapModeForwarding(t *testing.T) {
	var seen url.Values
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query()
		w.Write([]byte(`{"windows":[]}`))
	}))
	defer api.Close()
	sess := testMCPSession(t, newProxyBackend(api.URL, "", 5*time.Second))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	call := func(args map[string]any) {
		t.Helper()
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "getTopWindows", Arguments: args})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if res.IsError {
			t.Fatalf("isError=true; content=%+v", res.Content)
		}
	}

	call(map[string]any{"demoId": "gameId:42", "mode": "gap", "gapMs": 10000, "metric": "frags"})
	for k, want := range map[string]string{"mode": "gap", "gapMs": "10000", "metric": "frags"} {
		if got := seen.Get(k); got != want {
			t.Errorf("gap-mode %s = %q; want %q", k, got, want)
		}
	}
	if seen.Has("windowMs") {
		t.Errorf("windowMs = %q rode along with mode=gap; it must stay out (REST rejects it there)", seen.Get("windowMs"))
	}

	// mode:'gap' with no gapMs must reach the API AS SUCH, so the caller gets
	// the "mode=gap requires gapMs and has no default" 400 with its measured
	// starting points — not a silent gapMs=0.
	call(map[string]any{"demoId": "gameId:42", "mode": "gap"})
	if seen.Get("mode") != "gap" {
		t.Errorf("mode = %q; want %q", seen.Get("mode"), "gap")
	}
	if seen.Has("gapMs") {
		t.Errorf("omitted gapMs reached the API as %q; it must stay out so the missing-knob 400 is the one raised", seen.Get("gapMs"))
	}
}

// TestMCP_TopKills_OmittedIntsNotSent is getTopKills' clone of the
// top-windows omitted-integer contract: gapMs/contestedMs/limit ride *int
// through intp (an unset field stays out of the query so the REST default
// applies; an explicit 0 forwards and earns its 400), minDamage is a plain
// intv (its default IS 0), and startTime/endTime encode as from/to.
func TestMCP_TopKills_OmittedIntsNotSent(t *testing.T) {
	var seen url.Values
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query()
		w.Write([]byte(`{"kills":[]}`))
	}))
	defer api.Close()
	sess := testMCPSession(t, newProxyBackend(api.URL, "", 5*time.Second))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	call := func(args map[string]any) {
		t.Helper()
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "getTopKills", Arguments: args})
		if err != nil {
			t.Fatalf("CallTool: %v", err)
		}
		if res.IsError {
			t.Fatalf("isError=true; content=%+v", res.Content)
		}
	}

	// Nothing but demoId: no integer param may appear at all.
	call(map[string]any{"demoId": "gameId:42"})
	for _, k := range []string{"gapMs", "contestedMs", "limit", "minDamage", "from", "to"} {
		if seen.Has(k) {
			t.Errorf("omitted %s reached the API as %q; it must stay out of the query", k, seen.Get(k))
		}
	}

	// Explicit zeros on the pointer trio forward verbatim and earn their 400s
	// server-side; minDamage:0 is indistinguishable from omitted (its default
	// IS 0) and stays out.
	call(map[string]any{"demoId": "gameId:42", "gapMs": 0, "contestedMs": 0, "limit": 0, "minDamage": 0})
	for _, k := range []string{"gapMs", "contestedMs", "limit"} {
		if seen.Get(k) != "0" {
			t.Errorf("explicit %s:0 forwarded as %q; want \"0\"", k, seen.Get(k))
		}
	}
	if seen.Has("minDamage") {
		t.Errorf("minDamage:0 reached the API as %q; intv must drop it", seen.Get("minDamage"))
	}

	// A populated call encodes every param under its REST name.
	call(map[string]any{
		"demoId": "gameId:42", "gapMs": 2300, "contestedMs": 5000, "limit": 50,
		"players": []any{"bps"}, "weapons": []any{"rl"}, "minDamage": 150,
		"startTime": 60000, "endTime": 120000, "dmg": "raw",
	})
	for k, want := range map[string]string{
		"gapMs": "2300", "contestedMs": "5000", "limit": "50", "players": "bps",
		"weapons": "rl", "minDamage": "150", "from": "60000", "to": "120000", "dmg": "raw",
	} {
		if got := seen.Get(k); got != want {
			t.Errorf("top-kills %s = %q; want %q", k, got, want)
		}
	}
}

// TestMCP_Lives_ParamsForwarded: lives has no ambiguous integer (every 0 IS
// the REST default), so plain ints suffice — pin the encoding. summary is the
// one pointer: its MCP default is TRUE (a whole match is ~400 rows), so a bare
// call must send summary=1 and carry the hint that says how to get the detail.
func TestMCP_Lives_ParamsForwarded(t *testing.T) {
	var seen url.Values
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query()
		w.Write([]byte(`{"lives":[]}`))
	}))
	defer api.Close()
	b := newProxyBackend(api.URL, "", 5*time.Second)
	ctx := context.Background()

	out, err := b.GetLives(ctx, GetLivesInput{DemoID: "gameId:42"})
	if err != nil {
		t.Fatalf("GetLives: %v", err)
	}
	// A bare call is lean by default, like getDamage / getAim / getItems.
	if seen.Get("summary") != "1" {
		t.Errorf("bare lives call sent summary=%q; want \"1\" (the MCP default)", seen.Get("summary"))
	}
	seen.Del("summary")
	if len(seen) != 0 {
		t.Errorf("bare lives call sent %v; want nothing but summary", seen)
	}
	if hint, _ := out.(map[string]any)["hint"].(string); !strings.Contains(hint, "summary:false") {
		t.Errorf("defaulted summary carries hint %q; want the summary:false escape", hint)
	}

	// An explicit summary:false reaches REST as an absent param (REST's own
	// default is false) and gets NO hint — the caller already knows.
	no := false
	out, err = b.GetLives(ctx, GetLivesInput{DemoID: "gameId:42", Summary: &no})
	if err != nil {
		t.Fatalf("GetLives: %v", err)
	}
	if seen.Has("summary") {
		t.Errorf("summary:false sent summary=%q; want it omitted (REST defaults to false)", seen.Get("summary"))
	}
	if _, ok := out.(map[string]any)["hint"]; ok {
		t.Error("an explicit summary:false must not get the default-summary hint")
	}

	if _, err := b.GetLives(ctx, GetLivesInput{
		DemoID: "gameId:42", Players: []string{"bps", "milton"},
		StartTime: 1000, EndTime: 2000, MinMs: 1500, Dmg: "raw", Summary: &no,
	}); err != nil {
		t.Fatalf("GetLives: %v", err)
	}
	for k, want := range map[string]string{
		"players": "bps,milton", "from": "1000", "to": "2000", "minMs": "1500", "dmg": "raw",
	} {
		if got := seen.Get(k); got != want {
			t.Errorf("lives %s = %q; want %q", k, got, want)
		}
	}
}

// mustDecodeStructured re-marshals a tool result's StructuredContent
// into the typed Out for assertion.
func mustDecodeStructured(t *testing.T, res *mcp.CallToolResult, out any) {
	t.Helper()
	if res.StructuredContent == nil {
		t.Fatalf("StructuredContent is nil")
	}
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("re-marshal structured content: %v", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decode structured content into %T: %v (data=%s)", out, err, string(data))
	}
}

// TestMCP_ErrorSurfacesAPIMessage: a REST 4xx body must reach the MCP
// caller verbatim inside the isError text content — self-describing
// errors (the enumerated field-code list, invalid params) are how an
// agent recovers in one turn. Guards the full stack: real proxy
// backend -> httptest mvd-api returning the standard error envelope ->
// registerTools -> real go-sdk client session.
func TestMCP_ErrorSurfacesAPIMessage(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"code":"invalid_param","message":"unknown field code loc; valid codes: li (location), h (health)"}}`))
	}))
	defer api.Close()
	b := newProxyBackend(api.URL, "", 5*time.Second)
	sess := testMCPSession(t, b)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "getStateAt",
		Arguments: map[string]any{"demoId": "gameId:42", "time": 300, "fields": []string{"loc"}},
	})
	if err != nil {
		t.Fatalf("CallTool must not fail at the protocol level: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected isError=true, got %+v", res)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if !strings.Contains(text, "unknown field code loc") || !strings.Contains(text, "valid codes") {
		t.Errorf("error text must carry the API message verbatim, got %q", text)
	}
}
