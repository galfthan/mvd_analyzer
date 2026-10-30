package main

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

func playlistDemoResult(mapName string, offset int32, ids map[string]int, teams ...string) *result.Result {
	r := &result.Result{
		DemoInfo:         &result.DemoInfoResult{Map: mapName},
		Streams:          &result.Streams{Global: result.GlobalStream{DemoOffset: offset}},
		TimelineAnalysis: &result.TimelineAnalysisResult{PlayerUserIDs: ids},
		PlayerStats:      &result.PlayerStatsResult{},
	}
	for i, t := range teams {
		r.PlayerStats.Teams = append(r.PlayerStats.Teams, result.PlayerStatsRow{Name: t, Score: result.PlayerStatsScore{Frags: 100 - i}})
	}
	return r
}

func postPlaylist(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(url+"/v1/playlist-links", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// decodeLink is the web app's decodePlaylist up to the JSON.
func decodeLink(t *testing.T, link string) map[string]any {
	t.Helper()
	i := strings.Index(link, "#playlist=z")
	if i < 0 {
		t.Fatalf("no #playlist=z in %q", link)
	}
	raw, err := base64.RawURLEncoding.DecodeString(link[i+len("#playlist=z"):])
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(flate.NewReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestPlaylistLink(t *testing.T) {
	store := &fakeStore{byID: map[string]*result.Result{
		"gameId:1": playlistDemoResult("maps/dm3.bsp", 10250, map[string]int{"bps": 7, "milton": 9}, "red", "blue"),
		"gameId:2": playlistDemoResult("e1m2", 4000, map[string]int{"valla": 3}),
	}}
	srv := newTestServer(t, store)
	defer srv.Close()

	code, out := postPlaylist(t, srv.URL, `{"items": [
		{"card": {"ms": 3000, "bg": "#0B1020", "text": [{"text": "Airgibs", "size": 12, "color": "#00d9ff"}, {"text": "week 3", "font": "body", "at": "bottom", "from": 500}]}},
		{"clip": {"gameId": 1, "from": 120000, "to": 126500, "watch": "bps", "label": "Airgib · bps → milton",
			"slow": {"from": 123000, "to": 124000, "speed": 0.3}, "text": [{"text": "dm3 · 2:00", "at": "bottom-left", "to": 3000}]}},
		{"clip": {"gameId": 2, "from": 5000, "to": 9000, "label": "Airgib · valla"}},
		{"clip": {"gameId": 1, "from": 300000, "to": 304000, "watch": "auto", "label": "Airgib · milton"}}
	]}`)
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, out)
	}
	if out["name"] != "mvda · 2 demos · Airgib ×3" {
		t.Errorf("name = %v", out["name"])
	}
	url, _ := out["url"].(string)
	if !strings.HasPrefix(url, playPageURL+"#playlist=z") || int(out["length"].(float64)) != len(url) {
		t.Errorf("url = %q, length %v", url, out["length"])
	}
	if md, _ := out["markdown"].(string); md != "[mvda · 2 demos · Airgib ×3]("+url+")" {
		t.Errorf("markdown = %q", md)
	}
	doc := decodeLink(t, url)
	want := `{"c":[{"k":[3000,"#0b1020",1,400,400],"l":"Airgibs","x":[{"c":"#00d9ff","s":12,"t":"Airgibs"},{"a":"bottom","b":500,"f":"body","t":"week 3"}]},` +
		`[130,137,7,"bps","Airgib · bps → milton",0,[133.3,134.3,0.3],[{"a":"bottom-left","e":3000,"t":"dm3 · 2:00"}]],` +
		`[9,13,"auto","","Airgib · valla",1],` +
		`[310,315,"auto","","Airgib · milton",0]],` +
		`"d":[["hub:1",1,"dm3 · red vs blue",10.25],["hub:2",2,"e1m2",4]],"v":2}`
	got, _ := json.Marshal(doc)
	if string(got) != want {
		t.Errorf("link doc\n got %s\nwant %s", got, want)
	}
}

func TestPlaylistLinkRejects(t *testing.T) {
	store := &fakeStore{byID: map[string]*result.Result{
		"gameId:1": playlistDemoResult("dm3", 0, map[string]int{"bps": 7}),
	}}
	srv := newTestServer(t, store)
	defer srv.Close()
	for _, tc := range []struct{ body, want string }{
		{`{"items": []}`, "items: between 1"},
		{`{"items": [{"card": {"ms": 3000}}]}`, "at least one clip"},
		{`{"items": [{"clip": {"gameId": 1, "from": 5, "to": 1}}]}`, "items[0]: clip: from"},
		{`{"items": [{"clip": {"gameId": 1, "from": 0, "to": 1000, "watch": "nobody"}}]}`, `no player "nobody" in game 1 (players: bps)`},
		{`{"items": [{"clip": {"gameId": 1, "from": 0, "to": 1000}, "card": {"ms": 1000}}]}`, "exactly one of clip or card"},
		{`{"items": [{"card": {"ms": 50}}, {"clip": {"gameId": 1, "from": 0, "to": 1000}}]}`, "card.ms"},
		{`{"items": [{"clip": {"gameId": 1, "from": 0, "to": 1000, "text": [{"text": "x", "at": "middle"}]}}]}`, "text[0].at"},
		{`{"items": [{"clip": {"gameId": 1, "from": 0, "to": 1000}}], "extra": 1}`, "unknown field"},
	} {
		code, out := postPlaylist(t, srv.URL, tc.body)
		e, _ := out["error"].(map[string]any)
		msg, _ := e["message"].(string)
		if code != http.StatusBadRequest || !strings.Contains(msg, tc.want) {
			t.Errorf("%s: %d %v; want 400 containing %q", tc.body, code, out, tc.want)
		}
	}
	code, out := postPlaylist(t, srv.URL, `{"items": [{"clip": {"gameId": 99, "from": 0, "to": 1000}}]}`)
	if code != http.StatusNotFound {
		t.Errorf("unknown game: %d %v", code, out)
	}
}
