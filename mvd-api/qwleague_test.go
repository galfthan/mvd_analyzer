package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fakeQWL is QWLeague's /matches and /matches/{id}: a list of series,
// newest first, each with the detail its last map's id answers.
type fakeQWL struct {
	t       *testing.T
	details map[int]map[string]any // by list id
	asked   []string
	status  map[string]int // path → forced status
}

func (f *fakeQWL) ids() []int {
	ids := make([]int, 0, len(f.details))
	for id := range f.details {
		ids = append(ids, id)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))
	return ids
}

func (f *fakeQWL) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer tok" {
		f.t.Errorf("request without the bearer token: %s", r.URL)
	}
	f.asked = append(f.asked, r.URL.RequestURI())
	if st := f.status[r.URL.Path]; st != 0 {
		w.WriteHeader(st)
		return
	}
	if r.URL.Path == "/matches" {
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		ids := f.ids()
		items := []map[string]any{}
		for i := offset; i < len(ids) && i < offset+qwlPageSize; i++ {
			items = append(items, map[string]any{"id": ids[i]})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total": len(ids), "items": items})
		return
	}
	id, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/matches/"))
	d, ok := f.details[id]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(d)
}

func qwlSHA(n int) string { return fmt.Sprintf("%064x", n) }

func qwlHubURL(n int) string {
	return "https://hub.quakeworld.nu/demo-player/?demo_sha256=" + qwlSHA(n)
}

// qwlDetail is a two-map ladder series whose maps are matches id-1 and id,
// with demos sha(id-1) and sha(id).
func qwlDetail(id int, endedAt time.Time) map[string]any {
	return map[string]any{
		"id": id, "received_at": endedAt.Unix(), "mode": "2on2", "map": "dm4",
		"hub_demo_url": qwlHubURL(id),
		"series": map[string]any{
			"bestof": 3, "score": []int{2, 0}, "series_format": "bestof", "winner_team": 1, "forfeit": false,
			"maps": []map[string]any{
				{"map": "aerowalk", "winner_team": 1, "result_t1_frags": 98, "result_t2_frags": 85, "match_id": id - 1, "hub_demo_url": qwlHubURL(id - 1)},
				{"map": "dm4", "winner_team": 1, "result_t1_frags": 163, "result_t2_frags": 75, "match_id": id, "hub_demo_url": qwlHubURL(id)},
			},
			"unplayed": []string{"ztndm3"},
		},
	}
}

func newTestQWL(t *testing.T, f *fakeQWL, now *time.Time) (*qwlSync, *httptest.Server) {
	t.Helper()
	f.t = t
	up := httptest.NewServer(f)
	t.Cleanup(up.Close)
	return &qwlSync{
		client:   &qwlClient{base: up.URL, token: "tok", http: up.Client()},
		index:    newQWLIndex(filepath.Join(t.TempDir(), "qwleague", "series.json")),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		interval: time.Minute,
		now:      func() time.Time { return *now },
	}, up
}

func TestQWLSync_ReadsEverySeriesOnce(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	f := &fakeQWL{details: map[int]map[string]any{}}
	for id := 2; id <= 500; id += 2 {
		f.details[id] = qwlDetail(id, now.Add(-30*24*time.Hour))
	}
	y, _ := newTestQWL(t, f, &now)
	y.tick(context.Background())

	n, complete, syncedAt := y.index.state()
	if n != 250 || !complete || !syncedAt.Equal(now) {
		t.Fatalf("after the first tick: %d series, complete=%v, syncedAt=%v", n, complete, syncedAt)
	}
	// Three list pages (250 series) and one detail each.
	if got := len(f.asked); got != 3+250 {
		t.Errorf("asked QWLeague %d times, want 253", got)
	}
	got := y.index.lookup([]string{qwlSHA(41), qwlSHA(42), qwlSHA(100), qwlSHA(9999)})
	if len(got) != 2 || got[0].ID != 100 || got[1].ID != 42 {
		t.Fatalf("lookup returned %+v", got)
	}
	s := got[1]
	if s.Kind != "ladder" || s.BestOf != 3 || s.Format != "bestof" || s.Winner != 1 || len(s.Maps) != 2 ||
		s.Maps[0].SHA != qwlSHA(41) || s.Maps[0].Frags[0] != 98 || s.URL != "https://qwleague.com/match/42" {
		t.Errorf("series 42 = %+v", s)
	}

	// A later tick with nothing new asks for the first page only; one new
	// series costs that page and its detail.
	f.asked = nil
	y.tick(context.Background())
	if len(f.asked) != 1 {
		t.Errorf("an idle tick asked %v", f.asked)
	}
	f.details[502] = qwlDetail(502, now)
	f.asked = nil
	y.tick(context.Background())
	if len(f.asked) != 2 || f.asked[1] != "/matches/502" {
		t.Errorf("a tick with one new series asked %v", f.asked)
	}

	// A restart reads the saved index and asks for nothing it has.
	again := newQWLIndex(y.index.path)
	if err := again.load(); err != nil {
		t.Fatal(err)
	}
	if n, complete, _ := again.state(); n != 251 || !complete {
		t.Errorf("reloaded index: %d series, complete=%v", n, complete)
	}
	if got := again.lookup([]string{qwlSHA(501)}); len(got) != 1 || got[0].ID != 502 {
		t.Errorf("reloaded lookup = %+v", got)
	}
}

// A series is listed under its LAST map's id, which moves while it is
// played: the second read replaces the first instead of adding a series.
func TestQWLSync_SeriesGrowsUnderANewID(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	first := qwlDetail(11, now)
	first["id"] = 10
	first["series"].(map[string]any)["maps"] = first["series"].(map[string]any)["maps"].([]map[string]any)[:1]
	f := &fakeQWL{details: map[int]map[string]any{10: first}}
	y, _ := newTestQWL(t, f, &now)
	y.tick(context.Background())
	if got := y.index.lookup([]string{qwlSHA(10)}); len(got) != 1 || got[0].ID != 10 || len(got[0].Maps) != 1 {
		t.Fatalf("after map one: %+v", got)
	}

	delete(f.details, 10)
	f.details[11] = qwlDetail(11, now)
	y.tick(context.Background())
	n, _, _ := y.index.state()
	got := y.index.lookup([]string{qwlSHA(10), qwlSHA(11)})
	if n != 1 || len(got) != 1 || got[0].ID != 11 || len(got[0].Maps) != 2 {
		t.Fatalf("after map two: %d series, lookup %+v", n, got)
	}
}

// A map played but not yet on the hub has no sha: the series is read again
// an hour later, and no more once it is three days old.
func TestQWLSync_RereadsSeriesAwaitingADemo(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	d := qwlDetail(20, now)
	maps := d["series"].(map[string]any)["maps"].([]map[string]any)
	maps[1]["hub_demo_url"] = nil
	f := &fakeQWL{details: map[int]map[string]any{20: d}}
	y, _ := newTestQWL(t, f, &now)
	y.tick(context.Background())
	if got := y.index.lookup([]string{qwlSHA(20)}); len(got) != 0 {
		t.Fatalf("a demo QWLeague does not name was found: %+v", got)
	}

	f.asked = nil
	now = now.Add(10 * time.Minute)
	y.tick(context.Background())
	if len(f.asked) != 1 {
		t.Errorf("read again within the hour: %v", f.asked)
	}

	maps[1]["hub_demo_url"] = qwlHubURL(20)
	now = now.Add(time.Hour)
	y.tick(context.Background())
	if got := y.index.lookup([]string{qwlSHA(20)}); len(got) != 1 {
		t.Fatalf("the demo was not picked up an hour later")
	}

	maps[1]["hub_demo_url"] = nil
	y2, _ := newTestQWL(t, f, &now)
	y2.tick(context.Background())
	f.asked = nil
	now = now.Add(4 * 24 * time.Hour)
	y2.tick(context.Background())
	if len(f.asked) != 1 {
		t.Errorf("a series four days old was read again: %v", f.asked)
	}
}

// A failing detail leaves the tick unfinished (no syncedAt), keeps what was
// read, and is asked for again on the next tick; a throttle ends the tick.
func TestQWLSync_FailuresAreRetried(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	f := &fakeQWL{details: map[int]map[string]any{}, status: map[string]int{"/matches/4": 500}}
	for id := 2; id <= 6; id += 2 {
		f.details[id] = qwlDetail(id, now.Add(-30*24*time.Hour))
	}
	y, _ := newTestQWL(t, f, &now)
	y.tick(context.Background())
	if n, complete, syncedAt := y.index.state(); n != 2 || complete || !syncedAt.IsZero() {
		t.Fatalf("with one failing series: %d series, complete=%v, syncedAt=%v", n, complete, syncedAt)
	}
	delete(f.status, "/matches/4")
	y.tick(context.Background())
	if n, complete, _ := y.index.state(); n != 3 || !complete {
		t.Fatalf("after the retry: %d series, complete=%v", n, complete)
	}

	f.details[8], f.details[10] = qwlDetail(8, now), qwlDetail(10, now)
	f.status["/matches/10"] = http.StatusTooManyRequests
	f.asked = nil
	y.tick(context.Background())
	if len(f.asked) != 2 {
		t.Errorf("a throttled tick went on asking: %v", f.asked)
	}
}

func TestSeriesOf_TournamentAndSingleMap(t *testing.T) {
	var m qwlWireMatch
	if err := json.Unmarshal([]byte(qwlTournamentDetail), &m); err != nil {
		t.Fatal(err)
	}
	s := seriesOf(m)
	if s.Kind != "tournament" || s.Tournament == nil || s.Tournament.Division != "Division 2" || s.Tournament.Round != 1 ||
		len(s.Teams) != 2 || s.Teams[1].Name != "Death Dealers Shadows" || !s.Forfeit || s.key() != 6037 {
		t.Errorf("tournament series = %+v", s)
	}
	if s.Maps[1].played() || s.Maps[1].SHA != "" || s.awaitingDemo() {
		t.Errorf("a forfeited map reads as played or awaited: %+v", s.Maps[1])
	}

	var one qwlWireMatch
	if err := json.Unmarshal([]byte(`{"id":7,"received_at":1,"mode":"1on1","map":"bravado","custom_lobby_id":3,"hub_demo_url":"`+qwlHubURL(7)+`","series":null}`), &one); err != nil {
		t.Fatal(err)
	}
	if s := seriesOf(one); s.Kind != "custom" || len(s.Maps) != 1 || s.Maps[0].SHA != qwlSHA(7) || s.Maps[0].Map != "bravado" {
		t.Errorf("single-map series = %+v", s)
	}
}

func TestQWLSeriesEndpoint(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	off := httptest.NewServer(newRouter(&fakeStore{}, logger, "", testUploadConfig, nil, nil, &fakeSearcher{}, nil))
	defer off.Close()
	if m := getJSON(t, off.URL+"/v1/qwleague/series?sha="+qwlSHA(1), 502); errCode(m) != "qwleague_upstream" {
		t.Errorf("without a token: %v", m)
	}

	x := newQWLIndex(filepath.Join(t.TempDir(), "series.json"))
	var wire qwlWireMatch
	data, _ := json.Marshal(qwlDetail(42, time.Unix(1795361267, 0)))
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	x.put(seriesOf(wire))
	srv := httptest.NewServer(newRouter(&fakeStore{}, logger, "", testUploadConfig, nil, nil, &fakeSearcher{}, x))
	defer srv.Close()

	m := getJSON(t, srv.URL+"/v1/qwleague/series?sha="+qwlSHA(41)+","+strings.ToUpper(qwlSHA(42))+","+qwlSHA(5), 200)
	series, _ := m["series"].([]any)
	if len(series) != 1 || m["complete"] != false {
		t.Fatalf("response = %v", m)
	}
	s := series[0].(map[string]any)
	if s["id"] != float64(42) || s["kind"] != "ladder" || s["bestOf"] != float64(3) || s["endedAt"] != "2026-11-22T15:27:47Z" ||
		len(s["maps"].([]any)) != 2 || s["maps"].([]any)[0].(map[string]any)["sha"] != qwlSHA(41) {
		t.Errorf("series = %v", s)
	}
	if _, has := s["checkedAt"]; has {
		t.Errorf("the endpoint returns the index's own checkedAt: %v", s)
	}
	if m := getJSON(t, srv.URL+"/v1/qwleague/series?sha="+qwlSHA(5), 200); len(m["series"].([]any)) != 0 {
		t.Errorf("a demo of no series: %v", m)
	}

	for _, q := range []string{"", "?sha=", "?sha=abc", "?sha=" + qwlSHA(1) + "&x=1", "?sha=" + strings.Repeat(qwlSHA(1)+",", qwlMaxSHAs) + qwlSHA(2)} {
		want := "invalid_param"
		if strings.Contains(q, "&x=1") {
			want = "unknown_param"
		}
		if m := getJSON(t, srv.URL+"/v1/qwleague/series"+q, 400); errCode(m) != want {
			t.Errorf("%.40s: %v", q, m)
		}
	}
}

// qwlTournamentDetail is a tournament fixture decided by a forfeit: one map
// played, one not.
var qwlTournamentDetail = `{"id":6039,"received_at":1795292115,"mode":"4on4","map":"schloss","tournament_fixture_id":56,
 "series":{"bestof":3,"score":[1,2],"series_format":"bestof","winner_team":2,"forfeit":true,
  "maps":[{"map":"dm2","winner_team":2,"result_t1_frags":93,"result_t2_frags":278,"match_id":6037,"hub_demo_url":"` + qwlHubURL(1) + `"},
          {"map":"e1m2","winner_team":null,"result_t1_frags":null,"result_t2_frags":null,"match_id":6039,"hub_demo_url":null}],
  "unplayed":["dm3"]},
 "tournament":{"name":"4on4 · Season 1","slug":"4on4-season-1","division":"Division 2","stage":"group","round":1,
  "clan1":{"name":"Falling in Reverse","team":"fir"},"clan2":{"name":"Death Dealers Shadows","team":"D2s"}}}`

// testQWLIndex is the index the shared test server answers from: that one
// tournament series, synced.
func testQWLIndex() *qwlIndex {
	var m qwlWireMatch
	if err := json.Unmarshal([]byte(qwlTournamentDetail), &m); err != nil {
		panic(err)
	}
	x := newQWLIndex("")
	x.put(seriesOf(m))
	x.syncedAt = time.Unix(1795292200, 0).UTC()
	return x
}

func errCode(m map[string]any) string {
	e, _ := m["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}
