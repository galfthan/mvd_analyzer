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

	demos := map[int]*playlistDemoRef{}
	var demoRows [][]any
	var demoList []playlistDemo
	rows := make([]any, 0, len(req.Items))
	var labels []string
	clips, cards := 0, 0
	for i, it := range req.Items {
		bad := func(format string, a ...any) {
			writeError(w, http.StatusBadRequest, "invalid_param", fmt.Sprintf("items[%d]: ", i)+fmt.Sprintf(format, a...))
		}
		switch {
		case (it.Clip == nil) == (it.Card == nil):
			bad("exactly one of clip or card")
			return
		case it.Card != nil:
			c := it.Card
			if c.Ms < cardMsMin || c.Ms > cardMsMax {
				bad("card.ms: between %d and %d", cardMsMin, cardMsMax)
				return
			}
			bg := "#000000"
			if c.Bg != "" {
				if !hexColor.MatchString(c.Bg) {
					bad("card.bg: #rrggbb")
					return
				}
				bg = strings.ToLower(c.Bg)
			}
			op := 1.0
			if c.Opacity != nil {
				if *c.Opacity < 0 || *c.Opacity > 1 {
					bad("card.opacity: 0 to 1")
					return
				}
				op = *c.Opacity
			}
			texts, err := packTexts(c.Text)
			if err != nil {
				bad("card.%v", err)
				return
			}
			fi, fo := fadeOr(c.FadeIn, 400), fadeOr(c.FadeOut, 400)
			label := c.Label
			if label == "" && len(c.Text) > 0 {
				label = c.Text[0].Text
			}
			row := map[string]any{"k": []any{c.Ms, bg, op, fi, fo}, "l": label}
			if len(texts) > 0 {
				row["x"] = texts
			}
			rows = append(rows, row)
			cards++
		default:
			c := it.Clip
			if c.GameID <= 0 {
				bad("clip.gameId: a hub game id")
				return
			}
			if c.To <= c.From || c.From < 0 {
				bad("clip: from (ms) before to (ms), match-relative")
				return
			}
			d := demos[c.GameID]
			if d == nil {
				if len(demos) == playlistMaxDemos {
					bad("at most %d demos in one playlist", playlistMaxDemos)
					return
				}
				id, err := democache.ParseDemoID(fmt.Sprintf("gameId:%d", c.GameID))
				if err != nil {
					bad("clip.gameId: %v", err)
					return
				}
				res, _, err := s.store.GetResult(r.Context(), id)
				if err != nil {
					s.mapStoreError(w, r, err)
					return
				}
				d = demoRefFor(res, len(demoRows))
				demos[c.GameID] = d
				demoRows = append(demoRows, []any{fmt.Sprintf("hub:%d", c.GameID), c.GameID, d.title, float64(d.offsetMs) / 1000})
				demoList = append(demoList, playlistDemo{GameID: c.GameID, Title: d.title})
			}
			track, who := any("auto"), ""
			if c.Watch != "" && c.Watch != "auto" {
				uid, ok := d.userIDs[c.Watch]
				if !ok {
					bad("clip.watch: no player %q in game %d (players: %s)", c.Watch, c.GameID, strings.Join(d.names, ", "))
					return
				}
				track, who = uid, c.Watch
			}
			from := math.Floor(float64(c.From+d.offsetMs) / 1000)
			to := math.Ceil(float64(c.To+d.offsetMs) / 1000)
			row := []any{from, to, track, who, c.Label, d.index}
			texts, err := packTexts(c.Text)
			if err != nil {
				bad("clip.%v", err)
				return
			}
			if c.Slow != nil || len(texts) > 0 {
				var slow any = 0
				if c.Slow != nil {
					if c.Slow.Speed < 0.05 || c.Slow.Speed > 1 || c.Slow.To <= c.Slow.From {
						bad("clip.slow: from before to (match ms), speed 0.05 to 1")
						return
					}
					tenth := func(ms int32) float64 { return math.Round(float64(ms+d.offsetMs)/100) / 10 }
					slow = []any{tenth(c.Slow.From), tenth(c.Slow.To), c.Slow.Speed}
				}
				row = append(row, slow)
			}
			if len(texts) > 0 {
				row = append(row, texts)
			}
			rows = append(rows, row)
			labels = append(labels, c.Label)
			clips++
		}
	}
	if clips == 0 {
		writeError(w, http.StatusBadRequest, "invalid_param", "items: at least one clip")
		return
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
	mapName := strings.ToLower(res.EffectiveMap())
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
		if t == "" || len([]rune(t)) > playlistMaxText {
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

func fadeOr(v *int, dflt int) int {
	if v == nil {
		return dflt
	}
	return min(fadeMax, max(0, *v))
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

// playlistName is the web app's playlistName: "mvda", the demo (or "N
// demos") and what the list holds, from the clips' labels — one clip by
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
