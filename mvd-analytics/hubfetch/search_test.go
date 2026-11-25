package hubfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSearchSelectListIncludesHostname pins the PostgREST select list the
// search builds so the server_hostname column stays requested (review
// finding 3). The fake upstream captures the outgoing query and echoes a
// single row.
func TestSearchSelectListIncludesHostname(t *testing.T) {
	var gotSelect string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSelect = r.URL.Query().Get("select")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":1,"server_hostname":"KTX:qw.example.com","players":[]}]`))
	}))
	defer srv.Close()

	c := NewClient()
	c.SupabaseURL = srv.URL
	c.APIKey = "test-anon-key"

	if _, err := c.Search(context.Background(), SearchParams{}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	// PostgREST receives the URL-decoded select list.
	if !strings.Contains(gotSelect, "server_hostname") {
		t.Errorf("select list = %q, want it to contain server_hostname", gotSelect)
	}
	// The const itself is the mirror the web SEARCH_SELECT tracks; assert on
	// the raw form too so a decode quirk cannot mask a regression.
	if !strings.Contains(SearchSelect, "server_hostname") {
		t.Errorf("SearchSelect const = %q, want it to contain server_hostname", SearchSelect)
	}
}

// TestCompactRostersPassesTopLevelKeys covers that compactRosters rewrites
// only `players` and passes every other top-level key through verbatim —
// so the new server_hostname column (and the demo_sha256 passthrough
// island) appears unchanged in compact rows.
func TestCompactRostersPassesTopLevelKeys(t *testing.T) {
	games := []any{
		map[string]any{
			"id":              float64(42),
			"server_hostname": "KTX:qw.example.com",
			"demo_sha256":     "abc123",
			"players": []any{
				map[string]any{"name": "bps", "team": "red", "frags": float64(30), "ping": float64(13)},
			},
		},
	}
	compactRosters(games)

	row := games[0].(map[string]any)
	if row["server_hostname"] != "KTX:qw.example.com" {
		t.Errorf("server_hostname not passed through: %v", row["server_hostname"])
	}
	if row["demo_sha256"] != "abc123" {
		t.Errorf("demo_sha256 not passed through: %v", row["demo_sha256"])
	}
	if row["id"] != float64(42) {
		t.Errorf("id not passed through: %v", row["id"])
	}
	// players must be compacted to {name, team, frags} — ping dropped.
	pl := row["players"].([]any)[0].(map[string]any)
	if _, ok := pl["ping"]; ok {
		t.Errorf("players not compacted: ping survived: %v", pl)
	}
	if pl["name"] != "bps" || pl["team"] != "red" || pl["frags"] != float64(30) {
		t.Errorf("compacted player fields wrong: %v", pl)
	}
}
