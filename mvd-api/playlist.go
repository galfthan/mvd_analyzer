package main

// POST /v1/playlist-links — a link to the web app's light player
// (mvd-web/play.html) that plays a scripted reel: clips from any number of
// hub demos, title cards between them, text over either. The body names
// clips the way the rest of the API does (a gameId, match-relative ms, a
// player name); this handler loads each demo through the cache to turn them
// into what the player runs on — the demo's start offset (demo seconds =
// match time + streams.global.demoOffset), the player's userid, the demo's
// title — and packs the list exactly as the web app's share link does
// (mvd-web/src/lib/playlist.js encodePlaylist, format v2):
//
//	#playlist=z<base64url(deflate-raw(JSON))>
//	{ v: 2, d: [[key, gameId, title, offsetSec]…],
//	  c: [ [from, to, track, who, label, demoIndex, slow|0, texts?]   (a clip)
//	       | { k: [ms, bg, opacity, fadeIn, fadeOut], l: label, x: texts } ] }
//
// texts in the short keys t s c f a b e i o, defaults left out. The page
// clamps whatever it reads again; the limits here are for a useful 400.

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf16"

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
	GameID int            `json:"gameId"`
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
}

type playlistRequest struct {
	Items []playlistItem `json:"items"`
	Name  string         `json:"name,omitempty"`
}

type playlistDemo struct {
	GameID int    `json:"gameId"`
	Title  string `json:"title"`
}

type playlistResponse struct {
	URL      string         `json:"url"`
	Markdown string         `json:"markdown"`
	Name     string         `json:"name"`
	Length   int            `json:"length"`
	Clips    int            `json:"clips"`
	Cards    int            `json:"cards"`
	Demos    []playlistDemo `json:"demos"`
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
	title    string
	offsetMs int32
	userIDs  map[string]int
	names    []string
}

func (s *server) handlePlaylistLink(w http.ResponseWriter, r *http.Request) {
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
	var gameIDs []int
	seen := map[int]bool{}
	for i, it := range req.Items {
		ci, err := checkItem(it)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_param", fmt.Sprintf("items[%d]: %v", i, err))
			return
		}
		items[i] = ci
		if ci.clip != nil && !seen[ci.clip.GameID] {
			if len(gameIDs) == playlistMaxDemos {
				writeError(w, http.StatusBadRequest, "invalid_param", fmt.Sprintf("items[%d]: at most %d demos in one playlist", i, playlistMaxDemos))
				return
			}
			seen[ci.clip.GameID] = true
			gameIDs = append(gameIDs, ci.clip.GameID)
		}
	}
	if len(gameIDs) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_param", "items: at least one clip")
		return
	}

	// 2 · Load the demos, side by side (the cache's parse limit still
	// bounds the cold ones).
	results := make([]*result.Result, len(gameIDs))
	errs := make([]error, len(gameIDs))
	var wg sync.WaitGroup
	for k, gid := range gameIDs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := democache.ParseDemoID(fmt.Sprintf("gameId:%d", gid))
			if err == nil {
				results[k], _, err = s.store.GetResult(r.Context(), id)
			}
			errs[k] = err
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			s.mapStoreError(w, r, err)
			return
		}
	}
	demos := map[int]*playlistDemoRef{}
	demoRows := make([][]any, len(gameIDs))
	demoList := make([]playlistDemo, len(gameIDs))
	for k, gid := range gameIDs {
		d := demoRefFor(results[k], k)
		demos[gid] = d
		demoRows[k] = []any{fmt.Sprintf("hub:%d", gid), gid, d.title, float64(d.offsetMs) / 1000}
		demoList[k] = playlistDemo{GameID: gid, Title: d.title}
	}

	// 3 · The rows, in demo time.
	rows := make([]any, 0, len(items))
	var labels []string
	clips, cards := 0, 0
	for i, ci := range items {
		if ci.card != nil {
			rows = append(rows, ci.card)
			cards++
			continue
		}
		c := ci.clip
		d := demos[c.GameID]
		track, who := any("auto"), ""
		if c.Watch != "" && c.Watch != "auto" {
			uid, ok := d.userIDs[c.Watch]
			if !ok {
				players := "none known"
				if len(d.names) > 0 {
					players = strings.Join(d.names, ", ")
				}
				writeError(w, http.StatusBadRequest, "invalid_param",
					fmt.Sprintf("items[%d]: clip.watch: no player %q in game %d (players: %s)", i, c.Watch, c.GameID, players))
				return
			}
			track, who = uid, c.Watch
		}
		// int64: a from / to near the int32 bound must not wrap.
		off := int64(d.offsetMs)
		from := math.Floor(float64(int64(c.From)+off) / 1000)
		to := math.Ceil(float64(int64(c.To)+off) / 1000)
		row := []any{from, to, track, who, ci.label, d.index}
		if c.Slow != nil || len(ci.texts) > 0 {
			var slow any = 0
			if c.Slow != nil {
				tenth := func(ms int32) float64 { return math.Round(float64(int64(ms)+off)/100) / 10 }
				slow = []any{tenth(c.Slow.From), tenth(c.Slow.To), c.Slow.Speed}
			}
			row = append(row, slow)
		}
		if len(ci.texts) > 0 {
			row = append(row, ci.texts)
		}
		rows = append(rows, row)
		labels = append(labels, ci.label)
		clips++
	}

	doc, err := json.Marshal(map[string]any{"v": 2, "d": demoRows, "c": rows})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	code, err := deflateBase64URL(doc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal", err.Error())
		return
	}
	link := playPageURL + "#playlist=z" + code
	name := req.Name
	if name == "" {
		name = playlistName(demoList, labels)
	}
	writeJSON(w, http.StatusOK, playlistResponse{
		URL:      link,
		Markdown: "[" + strings.NewReplacer("[", "(", "]", ")").Replace(name) + "](" + link + ")",
		Name:     name,
		Length:   len(link),
		Clips:    clips,
		Cards:    cards,
		Demos:    demoList,
	})
}

// checkedItem is one item past checkItem: a clip (its demo still to be
// read) or a card's finished row, with the label and texts packed.
type checkedItem struct {
	clip  *playlistClip
	card  map[string]any
	label string
	texts []map[string]any
}

// playlistMaxMs bounds a clip's times: far past any match (four hours),
// well inside int32 once the demo offset is added.
const playlistMaxMs = 4 * 3600 * 1000

// checkItem validates one item without its demo; what needs the demo
// (the watched player) is checked once it is loaded.
func checkItem(it playlistItem) (checkedItem, error) {
	switch {
	case (it.Clip == nil) == (it.Card == nil):
		return checkedItem{}, fmt.Errorf("exactly one of clip or card")
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
		texts, err := packTexts(c.Text)
		if err != nil {
			return checkedItem{}, fmt.Errorf("card.%w", err)
		}
		label, err := cleanLabel(c.Label)
		if err != nil {
			return checkedItem{}, fmt.Errorf("card.%w", err)
		}
		if label == "" && len(texts) > 0 {
			label = texts[0]["t"].(string)
		}
		row := map[string]any{"k": []any{c.Ms, bg, op, fi, fo}, "l": label}
		if len(texts) > 0 {
			row["x"] = texts
		}
		return checkedItem{card: row, label: label}, nil
	default:
		c := it.Clip
		if c.GameID <= 0 {
			return checkedItem{}, fmt.Errorf("clip.gameId: a hub game id")
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
		texts, err := packTexts(c.Text)
		if err != nil {
			return checkedItem{}, fmt.Errorf("clip.%w", err)
		}
		label, err := cleanLabel(c.Label)
		if err != nil {
			return checkedItem{}, fmt.Errorf("clip.%w", err)
		}
		return checkedItem{clip: c, label: label, texts: texts}, nil
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

// packTexts validates texts and writes them in the link's short keys,
// defaults left out (the page's packText).
func packTexts(list []playlistText) ([]map[string]any, error) {
	if len(list) > playlistMaxTexts {
		return nil, fmt.Errorf("text: at most %d", playlistMaxTexts)
	}
	out := make([]map[string]any, 0, len(list))
	for i, x := range list {
		t := strings.TrimSpace(x.Text)
		if t == "" || utf16Len(t) > playlistMaxText {
			return nil, fmt.Errorf("text[%d].text: 1 to %d characters", i, playlistMaxText)
		}
		o := map[string]any{"t": t}
		if x.Size != nil {
			if *x.Size < 1 || *x.Size > 30 {
				return nil, fmt.Errorf("text[%d].size: 1 to 30 (%% of the view's height)", i)
			}
			if *x.Size != 6 {
				o["s"] = *x.Size
			}
		}
		if x.Color != "" {
			if !hexColor.MatchString(x.Color) {
				return nil, fmt.Errorf("text[%d].color: #rrggbb", i)
			}
			if c := strings.ToLower(x.Color); c != "#ffffff" {
				o["c"] = c
			}
		}
		if x.Font != "" {
			if !textFonts[x.Font] {
				return nil, fmt.Errorf("text[%d].font: title, body or mono", i)
			}
			if x.Font != "title" {
				o["f"] = x.Font
			}
		}
		if x.At != "" {
			if !textPlaces[x.At] {
				return nil, fmt.Errorf("text[%d].at: center, top, bottom, left, right, top-left, top-right, bottom-left or bottom-right", i)
			}
			if x.At != "center" {
				o["a"] = x.At
			}
		}
		from := 0
		if x.From != nil {
			if *x.From < 0 {
				return nil, fmt.Errorf("text[%d].from: ms from the item's start, >= 0", i)
			}
			from = *x.From
			if from > 0 {
				o["b"] = from
			}
		}
		if x.To != nil {
			if *x.To <= from {
				return nil, fmt.Errorf("text[%d].to: after from", i)
			}
			o["e"] = *x.To
		}
		for key, v := range map[string]*int{"i": x.FadeIn, "o": x.FadeOut} {
			if v == nil {
				continue
			}
			if *v < 0 || *v > fadeMax {
				return nil, fmt.Errorf("text[%d] fades: 0 to %d ms", i, fadeMax)
			}
			if *v != 300 {
				o[key] = *v
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
