package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// QWLeague (qwleague.com) runs matchmade ladders and tournaments on its own
// servers. Its API knows what the hub does not: which games are the maps of
// one series, the series' format and score, and for a tournament game the
// event, the stage and the clans behind "red" and "blue". The API needs a
// token, answers no browser (no CORS), allows 60 requests a minute and has
// no lookup by demo — a game's sha256 is only in the detail of its series.
//
// So mvd-api keeps an index: a background sync (qwlSync) walks QWLeague's
// match list, reads each series once, and stores a trimmed copy with the
// sha256 of every map's demo. GET /v1/qwleague/series answers from that
// index alone, so a page of search results costs QWLeague nothing. A
// finished series does not change; a series seen before its demos reached
// the hub is read again for a few days.
//
// The token comes from the environment (QWLEAGUE_TOKEN), never a flag.
// Without it there is no sync and the endpoint answers 502
// qwleague_upstream.

const (
	qwlDefaultBase = "https://qwleague.com/api/v1"
	// qwlPageSize is QWLeague's list page ceiling (a larger limit is cut to it).
	qwlPageSize = 100
	// qwlPace is the wait between two requests to QWLeague: half its limit of
	// 60 a minute, so the first walk of a few thousand series runs for an hour
	// or two in the background without ever being throttled.
	qwlPace = 2 * time.Second
	// qwlRecheckFor is how long after a series ended it is read again while a
	// played map of it has no demo sha (the hub has not listed the demo yet);
	// qwlRecheckEvery is the wait between two such reads.
	qwlRecheckFor   = 3 * 24 * time.Hour
	qwlRecheckEvery = time.Hour
	// qwlMaxSHAs caps one GET /v1/qwleague/series.
	qwlMaxSHAs = 200
)

// qwlSeries is one QWLeague series as the endpoint returns it: the fields
// of QWLeague's match detail a page showing hub games can use.
type qwlSeries struct {
	// ID is QWLeague's id of the series' last map, the one its match page
	// (URL) is under.
	ID   int    `json:"id"`
	URL  string `json:"url"`
	Mode string `json:"mode"`
	// Kind: ladder, tournament, prac, quickplay or custom.
	Kind    string `json:"kind"`
	BestOf  int    `json:"bestOf,omitempty"`
	Format  string `json:"format,omitempty"`
	Score   []int  `json:"score,omitempty"`
	Winner  int    `json:"winnerTeam,omitempty"`
	Forfeit bool   `json:"forfeit,omitempty"`
	// EndedAt is when QWLeague received the last map's result.
	EndedAt    time.Time      `json:"endedAt"`
	Maps       []qwlMap       `json:"maps"`
	Unplayed   []string       `json:"unplayed,omitempty"`
	Tournament *qwlTournament `json:"tournament,omitempty"`
	// Teams names team 1 and team 2 where QWLeague knows clans (a tournament
	// fixture, a prac); absent for a ladder game.
	Teams []qwlTeam `json:"teams,omitempty"`

	// checkedAt is when the series was last read from QWLeague (the index
	// file keeps it; the endpoint does not return it).
	checkedAt time.Time
}

type qwlMap struct {
	Map     string `json:"map"`
	MatchID int    `json:"matchId"`
	// SHA is the sha256 of the map's demo on the hub; empty for a map not
	// played (a forfeit) or whose demo the hub does not have.
	SHA    string `json:"sha,omitempty"`
	Frags  []int  `json:"frags,omitempty"`
	Winner int    `json:"winnerTeam,omitempty"`
}

type qwlTournament struct {
	Name     string `json:"name"`
	Slug     string `json:"slug,omitempty"`
	Division string `json:"division,omitempty"`
	Stage    string `json:"stage,omitempty"`
	Round    int    `json:"round,omitempty"`
}

type qwlTeam struct {
	Name string `json:"name"`
	// Tag is the clan's in-game team name as QWLeague stores it (Quake's
	// character set, not folded).
	Tag string `json:"tag,omitempty"`
}

// played reports a map that was played: QWLeague has its frags.
func (m qwlMap) played() bool { return len(m.Frags) == 2 }

// awaitingDemo reports a series with a played map whose demo sha QWLeague
// does not have yet.
func (s *qwlSeries) awaitingDemo() bool {
	for _, m := range s.Maps {
		if m.played() && m.SHA == "" {
			return true
		}
	}
	return false
}

// key identifies a series across reads: its first map's match id. The id
// the list shows is the LAST map's, which moves while a series is played.
func (s *qwlSeries) key() int {
	if len(s.Maps) > 0 && s.Maps[0].MatchID != 0 {
		return s.Maps[0].MatchID
	}
	return s.ID
}

// ---- QWLeague's wire shapes (the fields read; the rest is ignored) ----

type qwlWireList struct {
	Total int `json:"total"`
	Items []struct {
		ID int `json:"id"`
	} `json:"items"`
}

type qwlWireClan struct {
	Name string `json:"name"`
	Team string `json:"team"`
}

type qwlWireMatch struct {
	ID          int    `json:"id"`
	ReceivedAt  int64  `json:"received_at"`
	Mode        string `json:"mode"`
	Map         string `json:"map"`
	QuickPlay   int    `json:"quick_play"`
	PracID      *int   `json:"prac_id"`
	FixtureID   *int   `json:"tournament_fixture_id"`
	CustomLobby *int   `json:"custom_lobby_id"`
	HubDemoURL  string `json:"hub_demo_url"`
	Series      *struct {
		BestOf  int    `json:"bestof"`
		Score   []int  `json:"score"`
		Format  string `json:"series_format"`
		Winner  *int   `json:"winner_team"`
		Forfeit bool   `json:"forfeit"`
		Maps    []struct {
			Map        string `json:"map"`
			Winner     *int   `json:"winner_team"`
			T1         *int   `json:"result_t1_frags"`
			T2         *int   `json:"result_t2_frags"`
			MatchID    int    `json:"match_id"`
			HubDemoURL string `json:"hub_demo_url"`
		} `json:"maps"`
		Unplayed []string `json:"unplayed"`
	} `json:"series"`
	Prac *struct {
		Clan1 *qwlWireClan `json:"clan1"`
		Clan2 *qwlWireClan `json:"clan2"`
	} `json:"prac"`
	Tournament *struct {
		Name     string       `json:"name"`
		Slug     string       `json:"slug"`
		Division string       `json:"division"`
		Stage    string       `json:"stage"`
		Round    int          `json:"round"`
		Clan1    *qwlWireClan `json:"clan1"`
		Clan2    *qwlWireClan `json:"clan2"`
	} `json:"tournament"`
}

var qwlDemoSHA = regexp.MustCompile(`demo_sha256=([0-9a-f]{64})`)

// qwlSHAOf reads the demo sha out of QWLeague's hub player link, the only
// place its API states it.
func qwlSHAOf(hubDemoURL string) string {
	if m := qwlDemoSHA.FindStringSubmatch(hubDemoURL); m != nil {
		return m[1]
	}
	return ""
}

func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// seriesOf trims a match detail to a qwlSeries. A match that is not the
// last map of its series has no series block in QWLeague's detail; it comes
// back as a series of that one map.
func seriesOf(m qwlWireMatch) *qwlSeries {
	s := &qwlSeries{
		ID:      m.ID,
		URL:     "https://qwleague.com/match/" + strconv.Itoa(m.ID),
		Mode:    m.Mode,
		Kind:    "ladder",
		EndedAt: time.Unix(m.ReceivedAt, 0).UTC(),
	}
	switch {
	case m.FixtureID != nil:
		s.Kind = "tournament"
	case m.PracID != nil:
		s.Kind = "prac"
	case m.QuickPlay != 0:
		s.Kind = "quickplay"
	case m.CustomLobby != nil:
		s.Kind = "custom"
	}
	if m.Series == nil {
		s.Maps = []qwlMap{{Map: m.Map, MatchID: m.ID, SHA: qwlSHAOf(m.HubDemoURL)}}
	} else {
		s.BestOf, s.Format, s.Score = m.Series.BestOf, m.Series.Format, m.Series.Score
		s.Winner, s.Forfeit, s.Unplayed = derefInt(m.Series.Winner), m.Series.Forfeit, m.Series.Unplayed
		for _, wm := range m.Series.Maps {
			om := qwlMap{Map: wm.Map, MatchID: wm.MatchID, SHA: qwlSHAOf(wm.HubDemoURL), Winner: derefInt(wm.Winner)}
			if wm.T1 != nil && wm.T2 != nil {
				om.Frags = []int{*wm.T1, *wm.T2}
			}
			s.Maps = append(s.Maps, om)
		}
	}
	var c1, c2 *qwlWireClan
	if t := m.Tournament; t != nil {
		s.Tournament = &qwlTournament{Name: t.Name, Slug: t.Slug, Division: t.Division, Stage: t.Stage, Round: t.Round}
		c1, c2 = t.Clan1, t.Clan2
	} else if m.Prac != nil {
		c1, c2 = m.Prac.Clan1, m.Prac.Clan2
	}
	if c1 != nil && c2 != nil {
		s.Teams = []qwlTeam{{Name: c1.Name, Tag: c1.Team}, {Name: c2.Name, Tag: c2.Team}}
	}
	return s
}

// ---- the client ----

// errQWLThrottled is QWLeague's 429: the sync ends its tick. errQWLGone is
// its 404 for a listed match: the sync does not ask for it again.
var (
	errQWLThrottled = errors.New("qwleague: rate limited")
	errQWLGone      = errors.New("qwleague: no such match")
)

type qwlClient struct {
	base  string
	token string
	http  *http.Client
}

func (c *qwlClient) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", "mvd-api")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return errQWLThrottled
	}
	if resp.StatusCode == http.StatusNotFound {
		return errQWLGone
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("qwleague: %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

func (c *qwlClient) list(ctx context.Context, offset int) (qwlWireList, error) {
	var out qwlWireList
	q := url.Values{"limit": {strconv.Itoa(qwlPageSize)}, "offset": {strconv.Itoa(offset)}}
	return out, c.get(ctx, "/matches?"+q.Encode(), &out)
}

func (c *qwlClient) match(ctx context.Context, id int) (qwlWireMatch, error) {
	var out qwlWireMatch
	return out, c.get(ctx, "/matches/"+strconv.Itoa(id), &out)
}

// ---- the index ----

// qwlIndex is the series read so far, by key and by demo sha. It is saved
// to one JSON file under the cache root (outside the GC'd tiers), so a
// restart continues where the sync stopped.
type qwlIndex struct {
	mu       sync.RWMutex
	path     string
	series   map[int]*qwlSeries    // by key()
	bySHA    map[string]*qwlSeries // every map's sha
	listed   map[int]bool          // list ids read (a series' ID)
	complete bool                  // a walk has reached the end of QWLeague's list
	syncedAt time.Time             // the last tick that finished
}

// qwlIndexFile is the saved form. Version moves when qwlSeries changes in a
// way old entries cannot serve; an older file is dropped and read again.
type qwlIndexFile struct {
	Version  int              `json:"version"`
	Complete bool             `json:"complete"`
	SyncedAt time.Time        `json:"syncedAt"`
	Series   []qwlIndexSeries `json:"series"`
}

type qwlIndexSeries struct {
	qwlSeries
	CheckedAt time.Time `json:"checkedAt"`
}

const qwlIndexVersion = 1

func newQWLIndex(path string) *qwlIndex {
	return &qwlIndex{path: path, series: map[int]*qwlSeries{}, bySHA: map[string]*qwlSeries{}, listed: map[int]bool{}}
}

// load reads the saved index; a missing or outdated file leaves it empty.
func (x *qwlIndex) load() error {
	data, err := os.ReadFile(x.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f qwlIndexFile
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	if f.Version != qwlIndexVersion {
		return nil
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.complete, x.syncedAt = f.Complete, f.SyncedAt
	for i := range f.Series {
		s := f.Series[i].qwlSeries
		s.checkedAt = f.Series[i].CheckedAt
		x.putLocked(&s)
	}
	return nil
}

func (x *qwlIndex) save() error {
	x.mu.RLock()
	f := qwlIndexFile{Version: qwlIndexVersion, Complete: x.complete, SyncedAt: x.syncedAt}
	for _, s := range x.series {
		f.Series = append(f.Series, qwlIndexSeries{qwlSeries: *s, CheckedAt: s.checkedAt})
	}
	x.mu.RUnlock()
	sort.Slice(f.Series, func(i, j int) bool { return f.Series[i].ID > f.Series[j].ID })
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(x.path), 0o755); err != nil {
		return err
	}
	tmp := x.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, x.path)
}

func (x *qwlIndex) put(s *qwlSeries) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.putLocked(s)
}

// putLocked stores a series in place of what was read of it before (the
// same first map), whose list id and shas go with it.
func (x *qwlIndex) putLocked(s *qwlSeries) {
	if old := x.series[s.key()]; old != nil {
		for _, m := range old.Maps {
			if x.bySHA[m.SHA] == old {
				delete(x.bySHA, m.SHA)
			}
		}
	}
	x.series[s.key()] = s
	x.listed[s.ID] = true
	for _, m := range s.Maps {
		if m.SHA != "" {
			x.bySHA[m.SHA] = s
		}
	}
}

// skip marks a listed id as read without a series (QWLeague lists it and
// has no detail for it); it is asked for again after a restart.
func (x *qwlIndex) skip(listID int) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.listed[listID] = true
}

func (x *qwlIndex) has(listID int) bool {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.listed[listID]
}

// lookup returns the series holding the demos, each series once, newest
// first.
func (x *qwlIndex) lookup(shas []string) []*qwlSeries {
	x.mu.RLock()
	defer x.mu.RUnlock()
	seen := map[*qwlSeries]bool{}
	out := []*qwlSeries{}
	for _, sha := range shas {
		if s := x.bySHA[sha]; s != nil && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// due lists the series to read again: a played map still without its demo,
// ended recently, not read within qwlRecheckEvery.
func (x *qwlIndex) due(now time.Time) []int {
	x.mu.RLock()
	defer x.mu.RUnlock()
	var ids []int
	for _, s := range x.series {
		if s.awaitingDemo() && now.Sub(s.EndedAt) < qwlRecheckFor && now.Sub(s.checkedAt) >= qwlRecheckEvery {
			ids = append(ids, s.ID)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))
	return ids
}

func (x *qwlIndex) state() (series int, complete bool, syncedAt time.Time) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return len(x.series), x.complete, x.syncedAt
}

// ---- the sync ----

type qwlSync struct {
	client   *qwlClient
	index    *qwlIndex
	logger   *slog.Logger
	interval time.Duration
	pace     time.Duration
	now      func() time.Time
}

// run ticks until ctx ends: at once, then an interval after each tick.
func (y *qwlSync) run(ctx context.Context) {
	for ctx.Err() == nil {
		y.tick(ctx)
		select {
		case <-ctx.Done():
		case <-time.After(y.interval):
		}
	}
}

// wait paces the requests; false when ctx ended.
func (y *qwlSync) wait(ctx context.Context) bool {
	if y.pace <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(y.pace):
		return true
	}
}

// tick reads what QWLeague has gained: it walks the list from the newest
// page down to the first page that holds a series already read (the whole
// list until one walk has reached its end), reads every series not yet in the index,
// and reads again those waiting for a demo. An error ends the tick; what
// was read is saved, and the next tick picks up the rest.
func (y *qwlSync) tick(ctx context.Context) {
	_, complete, _ := y.index.state()
	var fresh []int
	reachedEnd := false
	for offset := 0; ; offset += qwlPageSize {
		page, err := y.client.list(ctx, offset)
		if err != nil {
			y.logger.Warn("qwleague sync: listing failed", "offset", offset, "err", err.Error())
			return
		}
		news := 0
		for _, it := range page.Items {
			if !y.index.has(it.ID) {
				fresh = append(fresh, it.ID)
				news++
			}
		}
		if len(page.Items) < qwlPageSize || offset+len(page.Items) >= page.Total {
			reachedEnd = true
			break
		}
		// The list is newest first: a page holding a series already read
		// is the last one with anything new.
		if complete && news < len(page.Items) {
			break
		}
		if !y.wait(ctx) {
			return
		}
	}
	ids := append(fresh, y.index.due(y.now())...)
	read, failed := 0, 0
	for _, id := range ids {
		if !y.wait(ctx) {
			break
		}
		m, err := y.client.match(ctx, id)
		if errors.Is(err, errQWLGone) {
			y.index.skip(id)
			continue
		}
		if err != nil {
			failed++
			y.logger.Warn("qwleague sync: reading a series failed", "id", id, "err", err.Error())
			if errors.Is(err, errQWLThrottled) || failed >= 5 {
				break
			}
			continue
		}
		s := seriesOf(m)
		s.checkedAt = y.now()
		y.index.put(s)
		if read++; read%50 == 0 {
			y.save()
		}
	}
	done := ctx.Err() == nil && failed == 0
	y.index.mu.Lock()
	if done {
		y.index.syncedAt = y.now()
		if reachedEnd {
			y.index.complete = true
		}
	}
	y.index.mu.Unlock()
	if read > 0 || done {
		y.save()
	}
	if read > 0 || failed > 0 {
		n, _, _ := y.index.state()
		y.logger.Info("qwleague sync", "read", read, "failed", failed, "series", n)
	}
}

func (y *qwlSync) save() {
	if err := y.index.save(); err != nil {
		y.logger.Warn("qwleague sync: saving the index failed", "err", err.Error())
	}
}

// ---- the endpoint ----

type qwlSeriesResponse struct {
	Series []*qwlSeries `json:"series"`
	// Complete is false while the first walk of QWLeague's list is still
	// running: an older game may be in a series not read yet.
	Complete bool `json:"complete"`
	// SyncedAt is the last time the index was brought up to date; absent
	// before the first sync has finished.
	SyncedAt *time.Time `json:"syncedAt,omitempty"`
}

// handleQWLSeries: GET /v1/qwleague/series?sha=<csv> — the QWLeague series
// the named demos are maps of, from the index. A demo that is not a
// QWLeague game (or not read yet) is simply not in any returned series.
func (s *server) handleQWLSeries(w http.ResponseWriter, r *http.Request) {
	p := newQP(r.URL.Query())
	raw := p.CSV("sha")
	if writeInvalidParam(w, p.Err()) {
		return
	}
	if writeUnknownParam(w, p.Unknown()) {
		return
	}
	if len(raw) == 0 || len(raw) > qwlMaxSHAs {
		writeError(w, http.StatusBadRequest, "invalid_param",
			fmt.Sprintf("sha must name 1..%d demos (comma-separated sha256)", qwlMaxSHAs))
		return
	}
	shas := make([]string, len(raw))
	for i, v := range raw {
		v = strings.ToLower(v)
		if len(v) != 64 || strings.Trim(v, "0123456789abcdef") != "" {
			writeError(w, http.StatusBadRequest, "invalid_param", fmt.Sprintf("invalid sha %q (want 64 hex digits)", raw[i]))
			return
		}
		shas[i] = v
	}
	if s.qwl == nil {
		writeError(w, http.StatusBadGateway, "qwleague_upstream", "QWLeague is not configured on this server")
		return
	}
	_, complete, syncedAt := s.qwl.state()
	out := qwlSeriesResponse{Series: s.qwl.lookup(shas), Complete: complete}
	if !syncedAt.IsZero() {
		out.SyncedAt = &syncedAt
	}
	writeJSON(w, http.StatusOK, out)
}
