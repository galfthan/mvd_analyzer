package main

// POST /v1/playlists — experimental. A playlist: clips from any number of
// hub demos, title cards between them, text over either, music under them.
// The body names clips the way the rest of the API does (a gameId or demo
// id, match-relative ms, a player name); this handler loads each demo
// through the cache to resolve them into what a player runs on — the demo's
// start offset (demo seconds = match time + streams.global.demoOffset), the
// player's userid, the demo's title, each track's credit — and answers with
// that resolved playlist (playlistDoc, the OpenAPI Playlist schema) and the
// links that open it. The web app's light player is the one client: its
// link carries the playlist itself,
//
//	#playlist=z<base64url(deflate-raw(JSON of the playlist))>
//
// and mvd-web/src/lib/playlist.js reads and writes the same JSON (format
// v3). The page clamps whatever it reads again; the limits here are for a
// useful 400.
//
// Experimental (owner, 2026-11-07): it is borderline whether this belongs
// in the API at all — it serves a web interface, and might move to an MCP
// of the web app's own.

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/mvd-analyzer/mvd-analytics/hubfetch"
	"github.com/mvd-analyzer/mvd-analytics/result"
	"github.com/mvd-analyzer/mvd-api/internal/democache"
)

// playPageURL is the light player the links open (serve's -play-url).
var playPageURL = "https://mvdanalyzer.com/play.html"

const (
	playlistMaxItems = 100
	playlistMaxDemos = 20
	playlistMaxTexts = 8
	playlistMaxText  = 200
	cardMsMin        = 300
	cardMsMax        = 30000
	fadeMax          = 5000
)

type playlistText struct {
	Text    string   `json:"text"`
	Size    *float64 `json:"size,omitempty"`
	Color   string   `json:"color,omitempty"`
	Font    string   `json:"font,omitempty"`
	At      string   `json:"at,omitempty"`
	From    *int     `json:"from,omitempty"`
	To      *int     `json:"to,omitempty"`
	FadeIn  *int     `json:"fadeIn,omitempty"`
	FadeOut *int     `json:"fadeOut,omitempty"`
}

type playlistSlow struct {
	From  int32   `json:"from"`
	To    int32   `json:"to"`
	Speed float64 `json:"speed"`
}

type playlistClip struct {
	GameID int            `json:"gameId,omitempty"`
	Demo   string         `json:"demo,omitempty"`
	From   int32          `json:"from"`
	To     int32          `json:"to"`
	Watch  string         `json:"watch,omitempty"`
	Label  string         `json:"label,omitempty"`
	Slow   *playlistSlow  `json:"slow,omitempty"`
	Text   []playlistText `json:"text,omitempty"`
}

type playlistCard struct {
	Ms      int            `json:"ms"`
	Bg      string         `json:"bg,omitempty"`
	Opacity *float64       `json:"opacity,omitempty"`
	FadeIn  *int           `json:"fadeIn,omitempty"`
	FadeOut *int           `json:"fadeOut,omitempty"`
	Label   string         `json:"label,omitempty"`
	Text    []playlistText `json:"text,omitempty"`
}

type playlistItem struct {
	Clip *playlistClip `json:"clip,omitempty"`
	Card *playlistCard `json:"card,omitempty"`
	// Music: {source, id} — a track from it on — or null, silence. Raw, so
	// null and absent differ.
	Music json.RawMessage `json:"music,omitempty"`
}

type playlistMusicRef struct {
	Source string `json:"source"`
	ID     string `json:"id"`
}

// playlistMusicCue is a music item's body: the track and how loud it
// plays (the player's default when absent).
type playlistMusicCue struct {
	playlistMusicRef
	Volume *float64 `json:"volume,omitempty"`
}

// The loudness a cue may ask for (the player's own default is 0.3).
const (
	musicVolumeMin = 0.05
	musicVolumeMax = 1
)

type playlistRequest struct {
	Items []playlistItem `json:"items"`
	Name  string         `json:"name,omitempty"`
}

// playlistVersion is the playlist format (the web's PLAYLIST_VERSION): 3,
// the resolved playlist as JSON; 1 and 2 were the web's compact rows.
const playlistVersion = 3

// playlistDoc is the resolved playlist: what a player runs, in demo
// seconds.
type playlistDoc struct {
	Version int            `json:"version"`
	Name    string         `json:"name"`
	Demos   []playlistDemo `json:"demos"`
	Items   []playlistOut  `json:"items"`
}

type playlistDemo struct {
	SHA    string  `json:"sha"`
	GameID int     `json:"gameId,omitempty"`
	Title  string  `json:"title"`
	Offset float64 `json:"offset"` // demo seconds at match start
}

type playlistOut struct {
	Clip *playlistClipOut
	Card *playlistCardOut
	// Cue: a music item; Music is its track, nil for silence.
	Cue   bool
	Music *playlistMusicOut
}

// MarshalJSON writes the item as {clip}, {card} or {music} — a cue of
// silence as "music": null.
func (o playlistOut) MarshalJSON() ([]byte, error) {
	switch {
	case o.Cue:
		return json.Marshal(map[string]any{"music": o.Music})
	case o.Card != nil:
		return json.Marshal(map[string]any{"card": o.Card})
	}
	return json.Marshal(map[string]any{"clip": o.Clip})
}

type playlistClipOut struct {
	Demo  int              `json:"demo"` // index into demos
	From  float64          `json:"from"` // demo seconds
	To    float64          `json:"to"`
	Track any              `json:"track"` // userid, or "auto"
	Who   string           `json:"who,omitempty"`
	Label string           `json:"label,omitempty"`
	Slow  *playlistSlowOut `json:"slow,omitempty"`
	Text  []playlistText   `json:"text,omitempty"`
}

type playlistSlowOut struct {
	From  float64 `json:"from"` // demo seconds
	To    float64 `json:"to"`
	Speed float64 `json:"speed"`
}

type playlistCardOut struct {
	Ms      int            `json:"ms"`
	Bg      string         `json:"bg"`
	Opacity float64        `json:"opacity"`
	FadeIn  int            `json:"fadeIn"`
	FadeOut int            `json:"fadeOut"`
	Label   string         `json:"label,omitempty"`
	Text    []playlistText `json:"text,omitempty"`
}

type playlistMusicOut struct {
	Source      string   `json:"source"`
	ID          string   `json:"id"`
	URL         string   `json:"url"`
	Title       string   `json:"title"`
	Artist      string   `json:"artist"`
	License     string   `json:"license"`
	LicenseURL  string   `json:"licenseUrl"`
	Page        string   `json:"page"`
	DurationSec int      `json:"durationSec"`
	Volume      *float64 `json:"volume,omitempty"`
}

type playlistResponse struct {
	Playlist playlistDoc   `json:"playlist"`
	Links    playlistLinks `json:"links"`
	Markdown string        `json:"markdown"` // [name](links.web), for Discord
	Length   int           `json:"length"`   // links.web's length
}

type playlistLinks struct {
	Web string `json:"web"` // the light player, the playlist in its fragment
}

var (
	hexColor   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	textFonts  = map[string]bool{"title": true, "body": true, "mono": true}
	textPlaces = map[string]bool{"center": true, "top": true, "bottom": true, "left": true, "right": true,
		"top-left": true, "top-right": true, "bottom-left": true, "bottom-right": true}
)

// The demo a clip plays in, resolved once per gameId.
type playlistDemoRef struct {
	index    int
	gameID   int
	title    string
	offsetMs int32
	userIDs  map[string]int
	names    []string
}

func (s *server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	var req playlistRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_param", "body: "+err.Error())
		return
	}
	if len(req.Items) == 0 || len(req.Items) > playlistMaxItems {
		writeError(w, http.StatusBadRequest, "invalid_param", fmt.Sprintf("items: between 1 and %d", playlistMaxItems))
		return
	}

	// 1 · Check every item before any demo is loaded (a load is a fetch
	// and a parse; a bad item at the end must not cost twenty of them).
	items := make([]checkedItem, len(req.Items))
	var ids []democache.DemoID
	seen := map[string]bool{}
	for i, it := range req.Items {
		ci, err := checkItem(it)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_param", fmt.Sprintf("items[%d]: %v", i, err))
			return
		}
		items[i] = ci
		if ci.clip != nil && !seen[ci.demo.String()] {
			if len(ids) == playlistMaxDemos {
				writeError(w, http.StatusBadRequest, "invalid_param", fmt.Sprintf("items[%d]: at most %d demos in one playlist", i, playlistMaxDemos))
				return
			}
			seen[ci.demo.String()] = true
			ids = append(ids, ci.demo)
		}
	}
	if len(ids) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_param", "items: at least one clip")
		return
	}
	if last := items[len(items)-1]; last.cue {
		writeError(w, http.StatusBadRequest, "invalid_param", fmt.Sprintf("items[%d]: a music cue plays under the items after it, and none follows", len(items)-1))
		return
	}

	// 1b · The tracks, looked up at their sources: the credit and license
	// the link carries are theirs.
	tracks, trackIndex, ok := s.playlistTracks(w, r, items)
	if !ok {
		return
	}

	// 2 · Load the demos, side by side (the cache's parse limit still
	// bounds the cold ones).
	results := make([]*result.Result, len(ids))
	shas := make([]string, len(ids))
	errs := make([]error, len(ids))
	var wg sync.WaitGroup
	for k, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var meta democache.CacheMeta
			results[k], meta, errs[k] = s.store.GetResult(r.Context(), id)
			shas[k] = meta.SHA256
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			s.mapStoreError(w, r, err)
			return
		}
	}

	// 3 · One demo per sha: a clip naming it by gameId and another by sha
	// play the same demo. The link names a demo by its hub gameId (the
	// light player loads it so), which a demo named by sha alone takes
	// from the hub's catalog.
	gameOf := map[string]int{}
	for k, id := range ids {
		if _, ok := gameOf[shas[k]]; !ok && id.Kind == "gameId" {
			gameOf[shas[k]] = id.GameID
		}
	}
	for k, id := range ids {
		if _, ok := gameOf[shas[k]]; ok || id.Kind != "sha256" {
			continue
		}
		gid, ok := s.playlistGameOf(w, r, id, shas[k])
		if !ok {
			return
		}
		gameOf[shas[k]] = gid
	}
	demos := map[string]*playlistDemoRef{} // by the id a clip names
	bySHA := map[string]*playlistDemoRef{}
	var demoList []playlistDemo
	for k, id := range ids {
		d := bySHA[shas[k]]
		if d == nil {
			d = demoRefFor(results[k], len(demoList))
			gid := gameOf[shas[k]]
			d.gameID = gid
			bySHA[shas[k]] = d
			demoList = append(demoList, playlistDemo{SHA: shas[k], GameID: gid, Title: d.title, Offset: float64(d.offsetMs) / 1000})
		}
		demos[id.String()] = d
	}

	// 4 · The items, in demo time.
	out := make([]playlistOut, 0, len(items))
	var labels []string
	for i, ci := range items {
		if ci.cue {
			o := playlistOut{Cue: true}
			if ci.music != nil {
				t := tracks[trackIndex[*ci.music]]
				o.Music = &playlistMusicOut{Source: t.Source, ID: t.ID, URL: t.URL, Title: t.Title, Artist: t.Artist,
					License: t.License, LicenseURL: t.LicenseURL, Page: t.Page, DurationSec: t.DurationSec, Volume: ci.volume}
			}
			out = append(out, o)
			continue
		}
		if ci.card != nil {
			out = append(out, playlistOut{Card: ci.card})
			continue
		}
		c := ci.clip
		d := demos[ci.demo.String()]
		track, who := any("auto"), ""
		if c.Watch != "" && c.Watch != "auto" {
			uid, ok := d.userIDs[c.Watch]
			if !ok {
				players := "none known"
				if len(d.names) > 0 {
					players = strings.Join(d.names, ", ")
				}
				writeError(w, http.StatusBadRequest, "invalid_param",
					fmt.Sprintf("items[%d]: clip.watch: no player %q in game %d (players: %s)", i, c.Watch, d.gameID, players))
				return
			}
			track, who = uid, c.Watch
		}
		// int64: a from / to near the int32 bound must not wrap.
		off := int64(d.offsetMs)
		clip := &playlistClipOut{
			Demo:  d.index,
			From:  math.Floor(float64(int64(c.From)+off) / 1000),
			To:    math.Ceil(float64(int64(c.To)+off) / 1000),
			Track: track, Who: who, Label: ci.label, Text: ci.texts,
		}
		if c.Slow != nil {
			tenth := func(ms int32) float64 { return math.Round(float64(int64(ms)+off)/100) / 10 }
			clip.Slow = &playlistSlowOut{From: tenth(c.Slow.From), To: tenth(c.Slow.To), Speed: c.Slow.Speed}
		}
		out = append(out, playlistOut{Clip: clip})
		labels = append(labels, ci.label)
	}

	name := req.Name
	if name == "" {
		name = playlistName(demoList, labels)
	}
	doc := playlistDoc{Version: playlistVersion, Name: name, Demos: demoList, Items: out}
	b, err := json.Marshal(doc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	code, err := deflateBase64URL(b)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	web := playPageURL + "#playlist=z" + code
	writeJSON(w, http.StatusOK, playlistResponse{
		Playlist: doc,
		Links:    playlistLinks{Web: web},
		Markdown: "[" + strings.NewReplacer("[", "(", "]", ")").Replace(name) + "](" + web + ")",
		Length:   len(web),
	})
}

// checkedItem is one item past checkItem: a clip (its demo still to be
// read), a finished card, or a music cue, with the label and texts clean.
type checkedItem struct {
	cue    bool // a music cue: music is its track, nil for silence
	music  *playlistMusicRef
	volume *float64 // the cue's loudness, when it sets one
	clip   *playlistClip
	demo   democache.DemoID // the clip's demo, as it names it
	card   *playlistCardOut
	label  string
	texts  []playlistText
}

// playlistMaxMs bounds a clip's times: far past any match (four hours),
// well inside int32 once the demo offset is added.
const playlistMaxMs = 4 * 3600 * 1000

// checkItem validates one item without its demo; what needs the demo
// (the watched player) is checked once it is loaded.
func checkItem(it playlistItem) (checkedItem, error) {
	set := 0
	for _, b := range []bool{it.Clip != nil, it.Card != nil, it.Music != nil} {
		if b {
			set++
		}
	}
	switch {
	case set != 1:
		return checkedItem{}, fmt.Errorf("exactly one of clip, card or music")
	case it.Music != nil:
		if string(it.Music) == "null" {
			return checkedItem{cue: true}, nil
		}
		var cue playlistMusicCue
		dec := json.NewDecoder(bytes.NewReader(it.Music))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cue); err != nil || cue.ID == "" {
			return checkedItem{}, fmt.Errorf("music: {source, id, volume?} from GET /v1/playlists/music, or null for silence")
		}
		if cue.Source != "incompetech" {
			return checkedItem{}, fmt.Errorf("music.source: incompetech")
		}
		if v := cue.Volume; v != nil && (*v < musicVolumeMin || *v > musicVolumeMax) {
			return checkedItem{}, fmt.Errorf("music.volume: %g to %g (omit for the player's default, 0.3)", float64(musicVolumeMin), float64(musicVolumeMax))
		}
		return checkedItem{cue: true, music: &cue.playlistMusicRef, volume: cue.Volume}, nil
	case it.Card != nil:
		c := it.Card
		if c.Ms < cardMsMin || c.Ms > cardMsMax {
			return checkedItem{}, fmt.Errorf("card.ms: between %d and %d", cardMsMin, cardMsMax)
		}
		bg := "#000000"
		if c.Bg != "" {
			if !hexColor.MatchString(c.Bg) {
				return checkedItem{}, fmt.Errorf("card.bg: #rrggbb")
			}
			bg = strings.ToLower(c.Bg)
		}
		op := 1.0
		if c.Opacity != nil {
			if *c.Opacity < 0 || *c.Opacity > 1 {
				return checkedItem{}, fmt.Errorf("card.opacity: 0 to 1")
			}
			op = *c.Opacity
		}
		fi, fo, err := cardFades(c.FadeIn, c.FadeOut)
		if err != nil {
			return checkedItem{}, err
		}
		texts, err := cleanTexts(c.Text)
		if err != nil {
			return checkedItem{}, fmt.Errorf("card.%w", err)
		}
		label, err := cleanLabel(c.Label)
		if err != nil {
			return checkedItem{}, fmt.Errorf("card.%w", err)
		}
		if label == "" && len(texts) > 0 {
			label = texts[0].Text
		}
		card := &playlistCardOut{Ms: c.Ms, Bg: bg, Opacity: op, FadeIn: fi, FadeOut: fo, Label: label, Text: texts}
		return checkedItem{card: card, label: label}, nil
	default:
		c := it.Clip
		var demo democache.DemoID
		switch {
		case (c.GameID != 0) == (c.Demo != ""):
			return checkedItem{}, fmt.Errorf("clip: exactly one of gameId or demo")
		case c.Demo != "":
			id, err := democache.ParseDemoID(c.Demo)
			if err != nil {
				return checkedItem{}, fmt.Errorf("clip.demo: %q: gameId:N or sha:<64 hex>", c.Demo)
			}
			demo = id
		case c.GameID < 0:
			return checkedItem{}, fmt.Errorf("clip.gameId: a hub game id")
		default:
			demo = democache.DemoID{Kind: "gameId", GameID: c.GameID}
		}
		if c.From < 0 || c.To <= c.From || c.To > playlistMaxMs {
			return checkedItem{}, fmt.Errorf("clip: from before to, in ms since match start (0 to %d)", playlistMaxMs)
		}
		if sl := c.Slow; sl != nil {
			// The player keeps only the part of the window inside the clip.
			if sl.Speed < 0.05 || sl.Speed > 1 || sl.To <= sl.From || sl.From < c.From || sl.To > c.To {
				return checkedItem{}, fmt.Errorf("clip.slow: from before to, inside the clip (match ms), speed 0.05 to 1")
			}
		}
		texts, err := cleanTexts(c.Text)
		if err != nil {
			return checkedItem{}, fmt.Errorf("clip.%w", err)
		}
		label, err := cleanLabel(c.Label)
		if err != nil {
			return checkedItem{}, fmt.Errorf("clip.%w", err)
		}
		return checkedItem{clip: c, demo: demo, label: label, texts: texts}, nil
	}
}

// cardFades: a card's fades, default 400 ms.
func cardFades(in, out *int) (int, int, error) {
	get := func(v *int) (int, error) {
		if v == nil {
			return 400, nil
		}
		if *v < 0 || *v > fadeMax {
			return 0, fmt.Errorf("card fades: 0 to %d ms", fadeMax)
		}
		return *v, nil
	}
	fi, err := get(in)
	if err != nil {
		return 0, 0, err
	}
	fo, err := get(out)
	return fi, fo, err
}

// utf16Len counts as the page's String.length does (its limits are in
// UTF-16 units).
func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// cleanLabel: a label trimmed, at most playlistMaxText long.
func cleanLabel(l string) (string, error) {
	l = strings.TrimSpace(l)
	if utf16Len(l) > playlistMaxText {
		return "", fmt.Errorf("label: at most %d characters", playlistMaxText)
	}
	return l, nil
}

// playlistMaxTracks bounds the distinct tracks of one playlist.
const playlistMaxTracks = 20

// playlistTracks looks up every track the cues name, once each, in
// first-use order, and maps each to its index in the link's music table.
// It writes the error and reports false when one cannot be had.
func (s *server) playlistTracks(w http.ResponseWriter, r *http.Request, items []checkedItem) ([]musicTrack, map[playlistMusicRef]int, bool) {
	var tracks []musicTrack
	index := map[playlistMusicRef]int{}
	for i, ci := range items {
		if ci.music == nil {
			continue
		}
		if _, ok := index[*ci.music]; ok {
			continue
		}
		if len(tracks) == playlistMaxTracks {
			writeError(w, http.StatusBadRequest, "invalid_param", fmt.Sprintf("items[%d]: at most %d tracks in one playlist", i, playlistMaxTracks))
			return nil, nil, false
		}
		ctx, cancel := context.WithTimeout(r.Context(), gamesSearchTimeout)
		t, err := s.music.Track(ctx, ci.music.Source, ci.music.ID)
		cancel()
		if errors.Is(err, errNoTrack) {
			writeError(w, http.StatusBadRequest, "invalid_param",
				fmt.Sprintf("items[%d]: music: %s has no track %q it may offer (GET /v1/playlists/music lists them)", i, ci.music.Source, ci.music.ID))
			return nil, nil, false
		}
		if err != nil {
			s.logger.Error("music upstream error", "request_id", requestID(r.Context()), "err", err.Error())
			writeError(w, http.StatusBadGateway, "music_upstream", "the music source could not be reached")
			return nil, nil, false
		}
		index[*ci.music] = len(tracks)
		tracks = append(tracks, t)
	}
	return tracks, index, true
}

// playlistGameOf finds the hub game of a demo a clip names by sha (the
// link's demo row needs its gameId). It writes the error and reports
// false when there is none: a demo the hub does not have cannot be in a
// playlist yet — the light player loads demos by gameId.
func (s *server) playlistGameOf(w http.ResponseWriter, r *http.Request, id democache.DemoID, sha string) (int, bool) {
	if s.searcher == nil {
		writeError(w, http.StatusBadGateway, "hub_upstream", "the game catalog is not configured on this server")
		return 0, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), gamesSearchTimeout)
	defer cancel()
	row, err := s.searcher.GameBySHA(ctx, sha)
	if errors.Is(err, hubfetch.ErrNotFound) {
		writeError(w, http.StatusBadRequest, "invalid_param",
			fmt.Sprintf("clip.demo %s: the hub has no game for this demo, and a playlist plays hub games only", id))
		return 0, false
	}
	if err != nil {
		s.logger.Error("hub game upstream error", "request_id", requestID(r.Context()), "err", err.Error())
		writeError(w, http.StatusBadGateway, "hub_upstream", "game catalog lookup failed upstream")
		return 0, false
	}
	gid, _ := row["id"].(float64)
	if gid <= 0 {
		writeError(w, http.StatusBadGateway, "hub_upstream", "the hub's game row for this demo has no id")
		return 0, false
	}
	return int(gid), true
}

// demoRefFor reads what a clip needs from its demo: the title the web app
// gives it (map · teams by frags, as demoRefOf does), the demo→match
// offset and the userids by name.
func demoRefFor(res *result.Result, index int) *playlistDemoRef {
	d := &playlistDemoRef{index: index, userIDs: map[string]int{}}
	if res.Streams != nil {
		d.offsetMs = res.Streams.Global.DemoOffset
	}
	if res.TimelineAnalysis != nil {
		for name, id := range res.TimelineAnalysis.PlayerUserIDs {
			d.userIDs[name] = id
			d.names = append(d.names, name)
		}
	}
	sort.Strings(d.names)
	// The web's mapFileKey: demoinfo / serverinfo, else match.map.
	mapName := res.EffectiveMap()
	if mapName == "" && res.Match != nil {
		mapName = res.Match.Map
	}
	mapName = strings.ToLower(mapName)
	if i := strings.LastIndex(mapName, "/"); i >= 0 {
		mapName = mapName[i+1:]
	}
	mapName = strings.TrimSuffix(mapName, ".bsp")
	var sides []result.PlayerStatsRow
	if res.PlayerStats != nil {
		sides = res.PlayerStats.Teams
		if len(sides) == 0 {
			sides = res.PlayerStats.Players
		}
	}
	sides = append([]result.PlayerStatsRow(nil), sides...)
	sort.SliceStable(sides, func(i, j int) bool { return sides[i].Score.Frags > sides[j].Score.Frags })
	var names []string
	for i, t := range sides {
		if i == 3 {
			break
		}
		names = append(names, t.Name)
	}
	parts := []string{}
	if mapName != "" {
		parts = append(parts, mapName)
	}
	if len(names) > 0 {
		parts = append(parts, strings.Join(names, " vs "))
	}
	d.title = strings.Join(parts, " · ")
	return d
}

// cleanTexts validates texts and writes them as the playlist carries them:
// colours lower-case, defaults left out (size 6, #ffffff, title, center,
// from 0, fades 300 — the page's cleanText fills them in).
func cleanTexts(list []playlistText) ([]playlistText, error) {
	if len(list) > playlistMaxTexts {
		return nil, fmt.Errorf("text: at most %d", playlistMaxTexts)
	}
	out := make([]playlistText, 0, len(list))
	for i, x := range list {
		t := strings.TrimSpace(x.Text)
		if t == "" || utf16Len(t) > playlistMaxText {
			return nil, fmt.Errorf("text[%d].text: 1 to %d characters", i, playlistMaxText)
		}
		o := playlistText{Text: t}
		if x.Size != nil {
			if *x.Size < 1 || *x.Size > 30 {
				return nil, fmt.Errorf("text[%d].size: 1 to 30 (%% of the view's height)", i)
			}
			if *x.Size != 6 {
				o.Size = x.Size
			}
		}
		if x.Color != "" {
			if !hexColor.MatchString(x.Color) {
				return nil, fmt.Errorf("text[%d].color: #rrggbb", i)
			}
			if c := strings.ToLower(x.Color); c != "#ffffff" {
				o.Color = c
			}
		}
		if x.Font != "" {
			if !textFonts[x.Font] {
				return nil, fmt.Errorf("text[%d].font: title, body or mono", i)
			}
			if x.Font != "title" {
				o.Font = x.Font
			}
		}
		if x.At != "" {
			if !textPlaces[x.At] {
				return nil, fmt.Errorf("text[%d].at: center, top, bottom, left, right, top-left, top-right, bottom-left or bottom-right", i)
			}
			if x.At != "center" {
				o.At = x.At
			}
		}
		from := 0
		if x.From != nil {
			if *x.From < 0 {
				return nil, fmt.Errorf("text[%d].from: ms from the item's start, >= 0", i)
			}
			from = *x.From
			if from > 0 {
				o.From = x.From
			}
		}
		if x.To != nil {
			if *x.To <= from {
				return nil, fmt.Errorf("text[%d].to: after from", i)
			}
			o.To = x.To
		}
		for _, f := range []struct {
			in  *int
			out **int
		}{{x.FadeIn, &o.FadeIn}, {x.FadeOut, &o.FadeOut}} {
			if f.in == nil {
				continue
			}
			if *f.in < 0 || *f.in > fadeMax {
				return nil, fmt.Errorf("text[%d] fades: 0 to %d ms", i, fadeMax)
			}
			if *f.in != 300 {
				*f.out = f.in
			}
		}
		out = append(out, o)
	}
	return out, nil
}

func deflateBase64URL(b []byte) (string, error) {
	var buf bytes.Buffer
	fw, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(b); err != nil {
		return "", err
	}
	if err := fw.Close(); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf.Bytes()), nil
}

// playlistName is the web app's playlistName, without the date the web
// takes from the loaded demo: "mvda", the demo (or "N demos") and what the list holds, from the clips' labels — one clip by
// its label, up to three kinds (a label's text before " · ") with counts,
// else the number of clips.
func playlistName(demos []playlistDemo, labels []string) string {
	parts := []string{"mvda"}
	if len(demos) == 1 {
		if demos[0].Title != "" {
			parts = append(parts, demos[0].Title)
		}
	} else if len(demos) > 1 {
		parts = append(parts, fmt.Sprintf("%d demos", len(demos)))
	}
	var order []string
	count := map[string]int{}
	for _, l := range labels {
		kind := strings.TrimSpace(strings.SplitN(l, " · ", 2)[0])
		if kind == "" {
			kind = "Clip"
		}
		if count[kind] == 0 {
			order = append(order, kind)
		}
		count[kind]++
	}
	switch {
	case len(labels) == 1:
		l := labels[0]
		if l == "" {
			l = "Clip"
		}
		parts = append(parts, l)
	case len(order) <= 3:
		var ks []string
		for _, k := range order {
			if count[k] > 1 {
				ks = append(ks, fmt.Sprintf("%s ×%d", k, count[k]))
			} else {
				ks = append(ks, k)
			}
		}
		parts = append(parts, strings.Join(ks, ", "))
	default:
		parts = append(parts, fmt.Sprintf("%d clips", len(labels)))
	}
	return strings.Join(parts, " · ")
}
