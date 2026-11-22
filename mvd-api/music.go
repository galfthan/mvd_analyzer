package main

// Music for playlists (plan-playlist-music.md): Creative Commons tracks a
// playlist link can play under its clips. GET /v1/playlists/music searches
// them; POST /v1/playlist-links takes a track by {source, id} and looks it
// up here itself, so the credit a link carries comes from the source, not
// from the request.
//
// One source: Kevin MacLeod's library at incompetech.com, every track CC BY
// 4.0, played by the light player straight from incompetech's server (it
// allows that; checked 2026-11-07). Its whole list is one JSON file
// (pieces.json, ~1400 tracks with mood, instruments and length), fetched
// once a day and searched here. Left out (2026-11-07): Wikimedia Commons —
// mostly speech recordings, the music of mixed quality — and ccMixter,
// which refuses any Referer but its own, so it would need copies kept here.
// `source` stays in the track's reference so another can be added.
//
// No ND license may ever be added: music synced to video is Adapted
// Material under CC 4.0 §1(a), which ND forbids, and a playlist is exactly
// that.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// musicTrack is a track as a playlist credits and plays it.
type musicTrack struct {
	Source      string   `json:"source"`
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Artist      string   `json:"artist"`
	License     string   `json:"license"`
	LicenseURL  string   `json:"licenseUrl"`
	Page        string   `json:"page"`
	URL         string   `json:"url"`
	DurationSec int      `json:"durationSec"`
	Tags        []string `json:"tags,omitempty"`
}

type musicQuery struct {
	Q      string
	MinSec int
	MaxSec int // 0: no upper bound
	Limit  int
}

// musicLibrary is the seam the handlers use; tests inject a fake.
type musicLibrary interface {
	Search(ctx context.Context, q musicQuery) ([]musicTrack, error)
	Track(ctx context.Context, source, id string) (musicTrack, error)
}

// errNoTrack: the source has no such track.
var errNoTrack = errors.New("no such track")

// defaultMusic is the library newRouter wires (tests swap it).
var defaultMusic musicLibrary = newIncompetech()

// incompetech is the library: Kevin MacLeod's list, kept for a day.
type incompetech struct {
	http    *http.Client
	listURL string // pieces.json
	mp3Base string // where the files are

	mu       sync.Mutex
	pieces   []incompetechPiece
	piecesAt time.Time
}

func newIncompetech() *incompetech {
	return &incompetech{
		http:    &http.Client{Timeout: 20 * time.Second},
		listURL: "https://incompetech.com/music/royalty-free/pieces.json",
		mp3Base: "https://incompetech.com/music/royalty-free/mp3-royaltyfree/",
	}
}

// The list's age before it is fetched again, and the cap on its size
// (~0.9 MB in 2026).
const (
	incompetechTTL   = 24 * time.Hour
	incompetechRetry = time.Hour
	musicMaxBytes    = 8 << 20
)

type incompetechPiece struct {
	Title       string `json:"title"`
	Filename    string `json:"filename"`
	Length      string `json:"length"` // "00:05:07"
	Instruments string `json:"instruments"`
	Description string `json:"description"`
	Feel        string `json:"feel"` // "Dark, Mysterious"
	ISRC        string `json:"isrc"`
}

// list is the pieces, fetched again once a day old; a failed fetch keeps
// serving the list before and tries again in an hour (not on every request,
// each waiting on a site that is down). A piece without an ISRC (its id
// here), file or title is left out.
func (m *incompetech) list(ctx context.Context) ([]incompetechPiece, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pieces != nil && time.Since(m.piecesAt) < incompetechTTL {
		return m.pieces, nil
	}
	pieces, err := m.fetch(ctx)
	if err != nil {
		if m.pieces != nil {
			m.piecesAt = time.Now().Add(incompetechRetry - incompetechTTL)
			return m.pieces, nil
		}
		return nil, err
	}
	kept := pieces[:0]
	for _, p := range pieces {
		if p.ISRC != "" && p.Filename != "" && p.Title != "" {
			kept = append(kept, p)
		}
	}
	m.pieces, m.piecesAt = kept, time.Now()
	return kept, nil
}

func (m *incompetech) fetch(ctx context.Context) ([]incompetechPiece, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", m.listURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("incompetech list: status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, musicMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > musicMaxBytes {
		return nil, fmt.Errorf("incompetech list: over %d bytes", musicMaxBytes)
	}
	var pieces []incompetechPiece
	if err := json.Unmarshal(b, &pieces); err != nil {
		return nil, fmt.Errorf("incompetech list: %w", err)
	}
	return pieces, nil
}

func (m *incompetech) track(p incompetechPiece) musicTrack {
	var tags []string
	for _, f := range strings.Split(p.Feel, ",") {
		if f = strings.TrimSpace(f); f != "" {
			tags = append(tags, f)
		}
	}
	return musicTrack{
		Source:      "incompetech",
		ID:          p.ISRC,
		Title:       p.Title,
		Artist:      "Kevin MacLeod",
		License:     "CC BY 4.0",
		LicenseURL:  "https://creativecommons.org/licenses/by/4.0/",
		Page:        "https://incompetech.com/music/royalty-free/index.html?isrc=" + url.QueryEscape(p.ISRC),
		URL:         m.mp3Base + url.PathEscape(p.Filename),
		DurationSec: clockSec(p.Length),
		Tags:        tags,
	}
}

// Search: every word of q in the title, mood, instruments or description;
// title matches first, then the list's order (newest first).
func (m *incompetech) Search(ctx context.Context, q musicQuery) ([]musicTrack, error) {
	pieces, err := m.list(ctx)
	if err != nil {
		return nil, err
	}
	words := strings.Fields(strings.ToLower(q.Q))
	type hit struct {
		t       musicTrack
		inTitle int
	}
	var hits []hit
	for _, p := range pieces {
		t := m.track(p)
		if t.DurationSec < q.MinSec || (q.MaxSec > 0 && t.DurationSec > q.MaxSec) {
			continue
		}
		hay := strings.ToLower(strings.Join([]string{p.Title, p.Feel, p.Instruments, p.Description}, " "))
		title := strings.ToLower(p.Title)
		ok, inTitle := true, 0
		for _, w := range words {
			if !strings.Contains(hay, w) {
				ok = false
				break
			}
			if strings.Contains(title, w) {
				inTitle++
			}
		}
		if ok {
			hits = append(hits, hit{t, inTitle})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].inTitle > hits[j].inTitle })
	out := make([]musicTrack, 0, min(q.Limit, len(hits)))
	for _, h := range hits[:min(q.Limit, len(hits))] {
		out = append(out, h.t)
	}
	return out, nil
}

func (m *incompetech) Track(ctx context.Context, source, id string) (musicTrack, error) {
	if source != "incompetech" {
		return musicTrack{}, errNoTrack
	}
	pieces, err := m.list(ctx)
	if err != nil {
		return musicTrack{}, err
	}
	for _, p := range pieces {
		if p.ISRC == id {
			return m.track(p), nil
		}
	}
	return musicTrack{}, errNoTrack
}

// clockSec reads "hh:mm:ss" or "mm:ss"; 0 when it cannot.
func clockSec(s string) int {
	n := 0
	for _, part := range strings.Split(strings.TrimSpace(s), ":") {
		v, err := strconv.Atoi(part)
		if err != nil {
			return 0
		}
		n = n*60 + v
	}
	return n
}

// handlePlaylistMusic: GET /v1/playlists/music — tracks a playlist can
// play (q: words to match; minSec / maxSec; limit 1..30, default 10).
func (s *server) handlePlaylistMusic(w http.ResponseWriter, r *http.Request) {
	p := newQP(r.URL.Query())
	q := musicQuery{
		Q:      p.Str("q"),
		MinSec: p.Int("minSec", 0),
		MaxSec: p.Int("maxSec", 0),
		Limit:  p.Int("limit", 10),
	}
	if writeInvalidParam(w, p.Err()) || writeUnknownParam(w, p.Unknown()) {
		return
	}
	switch {
	case q.Limit < 1 || q.Limit > 30:
		writeInvalidParam(w, fmt.Errorf("limit: 1 to 30"))
		return
	case q.MinSec < 0 || q.MaxSec < 0 || (q.MaxSec > 0 && q.MaxSec < q.MinSec):
		writeInvalidParam(w, fmt.Errorf("minSec / maxSec: seconds, minSec at most maxSec"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), gamesSearchTimeout)
	defer cancel()
	tracks, err := s.music.Search(ctx, q)
	if err != nil {
		s.logger.Error("music upstream error", "request_id", requestID(r.Context()), "err", err.Error())
		writeError(w, http.StatusBadGateway, "music_upstream", "the music list could not be fetched")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(tracks), "tracks": tracks})
}
