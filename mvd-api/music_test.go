package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mvd-analyzer/mvd-analytics/result"
)

// No test reaches incompetech.com: the handlers' library is this fake
// unless a test builds its own.
func init() { defaultMusic = fakeMusic{} }

// fakeMusic answers two tracks; any other id is errNoTrack.
type fakeMusic struct{}

var fakeTracks = []musicTrack{
	{Source: "incompetech", ID: "USUAN1100301", Title: "Volatile Reaction", Artist: "Kevin MacLeod", License: "CC BY 4.0",
		LicenseURL: "https://creativecommons.org/licenses/by/4.0/", Page: "https://incompetech.com/music/royalty-free/index.html?isrc=USUAN1100301",
		URL: "https://incompetech.com/music/royalty-free/mp3-royaltyfree/Volatile%20Reaction.mp3", DurationSec: 154, Tags: []string{"Action", "Driving"}},
	{Source: "incompetech", ID: "USUAN2600004", Title: "The Britons", Artist: "Kevin MacLeod", License: "CC BY 4.0",
		LicenseURL: "https://creativecommons.org/licenses/by/4.0/", Page: "https://incompetech.com/music/royalty-free/index.html?isrc=USUAN2600004",
		URL: "https://incompetech.com/music/royalty-free/mp3-royaltyfree/The%20Britons.mp3", DurationSec: 307},
}

func (fakeMusic) Search(_ context.Context, q musicQuery) ([]musicTrack, error) {
	out := []musicTrack{}
	for _, t := range fakeTracks {
		if t.DurationSec >= q.MinSec && (q.MaxSec == 0 || t.DurationSec <= q.MaxSec) && len(out) < q.Limit {
			out = append(out, t)
		}
	}
	return out, nil
}

func (fakeMusic) Track(_ context.Context, source, id string) (musicTrack, error) {
	for _, t := range fakeTracks {
		if t.Source == source && t.ID == id {
			return t, nil
		}
	}
	return musicTrack{}, errNoTrack
}

// incompetechFixture serves a three-piece list.
func incompetechFixture(t *testing.T) (*incompetech, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`[
			{"title":"Volatile Reaction","filename":"Volatile Reaction.mp3","length":"00:02:34","instruments":"Synths, Drums","description":"Dance club action.","feel":"Action, Driving","isrc":"USUAN1100301"},
			{"title":"The Britons","filename":"The Britons.mp3","length":"00:05:07","instruments":"Lute, Recorder","description":"Tavern music.","feel":"Bright","isrc":"USUAN2600004"},
			{"title":"No ISRC","filename":"x.mp3","length":"00:01:00","feel":"Dark"}
		]`))
	}))
	t.Cleanup(srv.Close)
	m := newIncompetech()
	m.listURL = srv.URL
	m.mp3Base = "https://cdn.example/mp3/"
	return m, &hits
}

func TestIncompetech(t *testing.T) {
	m, hits := incompetechFixture(t)
	ctx := context.Background()

	got, err := m.Search(ctx, musicQuery{Q: "ACTION", Limit: 10})
	if err != nil || len(got) != 1 {
		t.Fatalf("search: %v %v", got, err)
	}
	b, _ := json.Marshal(got[0])
	want := `{"source":"incompetech","id":"USUAN1100301","title":"Volatile Reaction","artist":"Kevin MacLeod","license":"CC BY 4.0",` +
		`"licenseUrl":"https://creativecommons.org/licenses/by/4.0/","page":"https://incompetech.com/music/royalty-free/index.html?isrc=USUAN1100301",` +
		`"url":"https://cdn.example/mp3/Volatile%20Reaction.mp3","durationSec":154,"tags":["Action","Driving"]}`
	if string(b) != want {
		t.Errorf("track\n got %s\nwant %s", b, want)
	}
	// Every word must match; length bounds; a piece without an ISRC is never offered.
	if got, _ := m.Search(ctx, musicQuery{Q: "action tavern", Limit: 10}); len(got) != 0 {
		t.Errorf("two words no piece has both: %v", got)
	}
	if got, _ := m.Search(ctx, musicQuery{MinSec: 200, Limit: 10}); len(got) != 1 || got[0].ID != "USUAN2600004" {
		t.Errorf("minSec 200: %v", got)
	}
	if got, _ := m.Search(ctx, musicQuery{MaxSec: 200, Limit: 10}); len(got) != 1 || got[0].ID != "USUAN1100301" {
		t.Errorf("maxSec 200: %v", got)
	}
	if got, _ := m.Search(ctx, musicQuery{Q: "dark", Limit: 10}); len(got) != 0 {
		t.Errorf("the ISRC-less piece: %v", got)
	}
	// A word in one title and in the other's description: the title match first.
	if got, _ := m.Search(ctx, musicQuery{Q: "re", Limit: 10}); len(got) != 2 || got[0].ID != "USUAN1100301" {
		t.Errorf("title first: %v", got)
	}
	if got, _ := m.Search(ctx, musicQuery{Limit: 1}); len(got) != 1 {
		t.Errorf("limit 1: %v", got)
	}
	if tr, err := m.Track(ctx, "incompetech", "USUAN2600004"); err != nil || tr.Title != "The Britons" || tr.DurationSec != 307 {
		t.Errorf("track by ISRC: %v %v", tr, err)
	}
	for _, ref := range [][2]string{{"incompetech", "nope"}, {"commons", "USUAN2600004"}} {
		if _, err := m.Track(ctx, ref[0], ref[1]); !errors.Is(err, errNoTrack) {
			t.Errorf("%v: %v", ref, err)
		}
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("list fetched %d times; want once, then kept", n)
	}
	// A day old, it is fetched again; a failed fetch keeps the old list.
	m.piecesAt = time.Now().Add(-25 * time.Hour)
	m.listURL = "http://127.0.0.1:1/gone"
	if got, err := m.Search(ctx, musicQuery{Q: "britons", Limit: 10}); err != nil || len(got) != 1 {
		t.Errorf("stale list, upstream gone: %v %v", got, err)
	}
	if age := time.Since(m.piecesAt); age > incompetechTTL-incompetechRetry+time.Minute {
		t.Errorf("after a failed refetch the list counts as %v old; want the next try in an hour", age)
	}
	// Never fetched and unreachable: an error.
	m.pieces = nil
	if _, err := m.Search(ctx, musicQuery{Limit: 10}); err == nil {
		t.Error("no list: no error")
	}
}

func TestClockSec(t *testing.T) {
	for in, want := range map[string]int{"00:05:07": 307, "2:45": 165, "": 0, "x:10": 0, "01:00:00": 3600} {
		if got := clockSec(in); got != want {
			t.Errorf("clockSec(%q) = %d; want %d", in, got, want)
		}
	}
}

func TestPlaylistMusicSearch(t *testing.T) {
	srv := newTestServer(t, &fakeStore{})
	defer srv.Close()
	m := getJSON(t, srv.URL+"/v1/playlists/music?q=x&maxSec=200", 200)
	if m["count"] != float64(1) {
		t.Errorf("maxSec 200: %v", m)
	}
	for _, bad := range []string{"limit=0", "limit=31", "minSec=50&maxSec=10", "source=incompetech", "x=1"} {
		getJSON(t, srv.URL+"/v1/playlists/music?"+bad, 400)
	}
}

func TestPlaylistLinkMusic(t *testing.T) {
	store := &fakeStore{byID: map[string]*result.Result{
		"gameId:1": playlistDemoResult("dm3", 0, map[string]int{"bps": 7}),
	}}
	srv := newTestServer(t, store)
	defer srv.Close()

	code, out := postPlaylist(t, srv.URL, `{"items": [
		{"music": {"source": "incompetech", "id": "USUAN1100301"}},
		{"card": {"ms": 2000, "label": "Intro"}},
		{"clip": {"gameId": 1, "from": 0, "to": 1000}},
		{"music": {"source": "incompetech", "id": "USUAN2600004", "volume": 0.5}},
		{"clip": {"gameId": 1, "from": 2000, "to": 3000}},
		{"music": null},
		{"clip": {"gameId": 1, "from": 4000, "to": 5000}},
		{"music": {"source": "incompetech", "id": "USUAN1100301"}},
		{"clip": {"gameId": 1, "from": 6000, "to": 7000}}
	]}`)
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, out)
	}
	doc := playlistOf(t, out)
	var cues []any
	kinds := ""
	for _, it := range doc["items"].([]any) {
		o := it.(map[string]any)
		for k := range o {
			kinds += k[:2] + " "
		}
		if m, ok := o["music"]; ok {
			cues = append(cues, m)
		}
	}
	if kinds != "mu ca cl mu cl mu cl mu cl " {
		t.Errorf("items: %s", kinds)
	}
	c, _ := json.Marshal(cues)
	want := `[{"artist":"Kevin MacLeod","durationSec":154,"id":"USUAN1100301","license":"CC BY 4.0","licenseUrl":"https://creativecommons.org/licenses/by/4.0/","page":"https://incompetech.com/music/royalty-free/index.html?isrc=USUAN1100301","source":"incompetech","title":"Volatile Reaction","url":"https://incompetech.com/music/royalty-free/mp3-royaltyfree/Volatile%20Reaction.mp3"},` +
		`{"artist":"Kevin MacLeod","durationSec":307,"id":"USUAN2600004","license":"CC BY 4.0","licenseUrl":"https://creativecommons.org/licenses/by/4.0/","page":"https://incompetech.com/music/royalty-free/index.html?isrc=USUAN2600004","source":"incompetech","title":"The Britons","url":"https://incompetech.com/music/royalty-free/mp3-royaltyfree/The%20Britons.mp3","volume":0.5},` +
		`null,` +
		`{"artist":"Kevin MacLeod","durationSec":154,"id":"USUAN1100301","license":"CC BY 4.0","licenseUrl":"https://creativecommons.org/licenses/by/4.0/","page":"https://incompetech.com/music/royalty-free/index.html?isrc=USUAN1100301","source":"incompetech","title":"Volatile Reaction","url":"https://incompetech.com/music/royalty-free/mp3-royaltyfree/Volatile%20Reaction.mp3"}]`
	if string(c) != want {
		t.Errorf("music items\n got %s\nwant %s", c, want)
	}

	for name, body := range map[string]string{
		"an unknown track":   `{"items": [{"music": {"source": "incompetech", "id": "nope"}}, {"clip": {"gameId": 1, "from": 0, "to": 1000}}]}`,
		"an unknown source":  `{"items": [{"music": {"source": "ccmixter", "id": "1"}}, {"clip": {"gameId": 1, "from": 0, "to": 1000}}]}`,
		"a stray field":      `{"items": [{"music": {"source": "incompetech", "id": "USUAN1100301", "url": "https://x"}}, {"clip": {"gameId": 1, "from": 0, "to": 1000}}]}`,
		"a cue at the end":   `{"items": [{"clip": {"gameId": 1, "from": 0, "to": 1000}}, {"music": null}]}`,
		"music and a clip":   `{"items": [{"music": null, "clip": {"gameId": 1, "from": 0, "to": 1000}}]}`,
		"music with no clip": `{"items": [{"music": null}, {"card": {"ms": 1000}}]}`,
		"too loud":           `{"items": [{"music": {"source": "incompetech", "id": "USUAN1100301", "volume": 1.5}}, {"clip": {"gameId": 1, "from": 0, "to": 1000}}]}`,
		"too quiet":          `{"items": [{"music": {"source": "incompetech", "id": "USUAN1100301", "volume": 0}}, {"clip": {"gameId": 1, "from": 0, "to": 1000}}]}`,
	} {
		if code, out := postPlaylist(t, srv.URL, body); code != http.StatusBadRequest {
			t.Errorf("%s: %d %v; want 400", name, code, out)
		}
	}
}
