package analyzer

import (
	"strconv"
	"strings"

	"github.com/mvd-analyzer/mvd-reader/events"
)

// MetadataAnalyzer collects server-level and match-level metadata that
// arrives via non-payload protocol commands rather than via stat updates.
//
// Three sources feed it:
//
//  1. svc_stufftext at connection time — the server sends a single
//     `fullserverinfo "\key\value\…"` console command containing every
//     CVAR_SERVERINFO cvar (mvdsv side: maxfps, fraglimit, timelimit,
//     teamplay, maxclients, deathmatch, hostname, *version, *z_ext, *admin,
//     *gamedir, *qvm, *progs, map, status, serverdemo, epoch, …) plus any
//     KTX-side keys mirrored via `localcmd "serverinfo …"` (mode, ktxver,
//     fpd, matchtag, status). We split this into ServerInfo[k]=v.
//
//  2. svc_serverinfo (cmd 52) — single-key updates emitted later in the
//     demo when a value changes (`status` cycles through Countdown / "3 min
//     left" / "Standby" / "Forcestart"; `fpd` toggles when admins flip
//     `fpd add` / `fpd del`; etc). Last-write-wins.
//
//  3. the `//finalscores` stufftext KTX stuffs at match end — the server's
//     own final scoreline plus its mode and map (parser/ktx_finalscores.go).
//     It lands here rather than in `match` because it is a metadata record
//     first: it is reported verbatim on every demo that carries one, and the
//     match node reads it back through CoreOutputs only where a value of its
//     own is missing.
//
//  4. svc_centerprint (cmd 26) — KTX renders the full match-settings
//     table here every second of the 10-second countdown (match.c
//     PrintCountdown). The last centerprint we see before the
//     "match has begun!" print is the canonical match settings dump:
//     Mode / Deathmatch / Spawnmodel / Antilag / Teamplay / Timelimit /
//     Fraglimit / Overtime / Powerups / Dmgfrags / NoItems / Midair /
//     Instagib / Yawnmode / Airstep / VWep / Noweapon / matchtag.
//
// We do not try to interpret //ktx-style stufftexts (`//ktx matchstart`,
// `//wps 0 lg 31 17`, `//ktx drop 49 64 3`) — those are client HUD hints,
// not server metadata.
type MetadataAnalyzer struct {
	serverInfo   map[string]string
	countdownRaw string // last centerprint that contained "Countdown:" (post-Q_normalizetext)
	fairpacks    string // "best weapon" / "last weapon fired", from the ShowMatchSettings broadcast
	finalScores  *FinalScores
	timing       MatchTimingDetector

	// The `status` key tracked over time rather than last-write-wins, for
	// the no-match marker (nomatch.go). serverInfo["status"] is the value
	// at demo END; these two are "what the server said at demo open" and
	// "did it ever say a game was running", which is what separates a
	// recording that starts mid-game from one that caught an idle server.
	statusSeen  bool
	statusOpen  string
	statusRunng bool
}

// NewMetadataAnalyzer creates a metadata analyzer.
func NewMetadataAnalyzer() *MetadataAnalyzer {
	return &MetadataAnalyzer{
		serverInfo: make(map[string]string),
	}
}

func (a *MetadataAnalyzer) Name() string { return "metadata" }

func (a *MetadataAnalyzer) Init(ctx *Context) error { return nil }

func (a *MetadataAnalyzer) OnEvent(event events.Event) error {
	switch e := event.(type) {
	case *events.StuffTextEvent:
		// The bulk cvar dump is the very first stufftext: `fullserverinfo "..."`.
		if cmd := e.Command; strings.HasPrefix(cmd, "fullserverinfo ") {
			a.parseFullserverinfo(cmd)
		}
	case *events.ServerInfoEvent:
		// Mid-game key/value updates — last write wins.
		if e.Key != "" {
			a.serverInfo[e.Key] = e.Value
		}
		if e.Key == "status" {
			a.observeStatus(e.Value)
		}
	case *events.CenterPrintEvent:
		// The KTX countdown centerprint is the only multi-line centerprint
		// during the pre-match window that contains "Countdown:". We only
		// want the last one we saw before the match started, because the
		// final 1-second-remaining centerprint contains the same fields as
		// the rest and is the cleanest sample.
		if a.timing.Started {
			return nil
		}
		text := events.NormalizeQuakeText([]byte(e.Message))
		if strings.Contains(text, "Countdown:") {
			a.countdownRaw = text
		}
	case *events.FinalScoresEvent:
		// Last write wins, like the serverinfo keys above: a demo spanning
		// two matches carries one directive per match, and the Result
		// describes the last one it saw complete. (Across a 12 000-demo
		// archive sample every demo carrying the directive carried exactly
		// one.)
		a.finalScores = &FinalScores{
			Date:   e.Date,
			Mode:   e.Mode,
			Map:    e.Map,
			Team1:  e.Team1,
			Score1: e.Score1,
			Team2:  e.Team2,
			Score2: e.Score2,
		}
	case *events.PrintEvent:
		// Latch the match start so we stop overwriting countdownRaw with
		// any post-match centerprint that happens to mention "Countdown".
		a.timing.OnPrint(e)
		a.captureBroadcastSetting(e)
	}
	return nil
}

// fairpacksPrefix is the ShowMatchSettings row KTX broadcasts and ONLY when
// k_frp is non-default (ktx/src/match.c:2086-2107) — so seeing it at all is
// the signal. KTX writes it as
//
//	G_bprint(2, "Fairpacks setting: %s\n", redtext(txt));   // match.c:2107
//
// i.e. PRINT_HIGH, line-initial, from ShowMatchSettings during the
// countdown.
const fairpacksPrefix = "Fairpacks setting:"

// captureBroadcastSetting picks the settings rows KTX prints beside the
// countdown centerprint rather than inside it. The value is redtext (high-bit
// characters), so it goes through the same normalisation the centerprint
// path uses.
//
// The three gates all guard the same thing: this row stands the whole
// backpack reconstruction down (backpackSkipModeReason), so anything a
// player can type must not be able to forge it.
//
//   - Level 2 only. G_bprint's level is PRINT_HIGH; a level-3 chat line
//     reading `say Fairpacks setting: best weapon` reaches this handler as a
//     PrintEvent too, and a substring match on it fabricated the setting.
//   - Pre-match only, like the sibling countdown capture — ShowMatchSettings
//     runs from the countdown, so a mid-match line naming it is not it.
//   - Line-initial, not "contains": KTX's format string starts the message,
//     and requiring that removes the last way to smuggle the prefix into an
//     otherwise legitimate broadcast (a player NAME, say, which the server
//     interpolates ahead of the text in most of its bprints).
func (a *MetadataAnalyzer) captureBroadcastSetting(e *events.PrintEvent) {
	if a.fairpacks != "" || a.timing.Started || e.Level != events.PrintHigh {
		return
	}
	text := events.NormalizeQuakeText([]byte(e.Message))
	if !strings.HasPrefix(text, fairpacksPrefix) {
		return
	}
	v := strings.TrimSpace(text[len(fairpacksPrefix):])
	if i := strings.IndexByte(v, '\n'); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	if v == "" || v == "off" {
		return
	}
	a.fairpacks = v
}

// parseFullserverinfo extracts the quoted cvar string from a stufftext like
// `fullserverinfo "\maxfps\77\timelimit\10\..."` and merges its key/value
// pairs into MetadataAnalyzer.serverInfo (last write wins).
func (a *MetadataAnalyzer) parseFullserverinfo(cmd string) {
	kv := parseInfoString(cmd)
	for k, v := range kv {
		a.serverInfo[k] = v
	}
	// A demo cut from a longer recording can carry a second dump; the FIRST
	// one is the state at demo open, which is the whole point of the field.
	if v, ok := kv["status"]; ok {
		a.observeStatus(v)
	}
}

// observeStatus records the serverinfo `status` transitions the no-match
// marker reads: the value at demo open, and whether the key ever named a
// running game.
//
// "Running" is spelled by the mod. KTX writes `"%d min left"`
// (ktx/src/match.c:596, :723, :1330, :1337); an older mod in the archive
// writes `"%d:%02d left"`. Both are a remaining-time reading: they end in
// " left" and start with a digit. No idle or pre-match value the archive
// carries looks like that ("Standby" ktx/src/world.c:543, "Countdown"
// match.c:2475, "Forcestart" admin.c:693, and a mod-specific "Normal"), so
// that pair is the test — pinning the exact KTX format instead would
// silently reclassify every mod that spells the clock differently.
func (a *MetadataAnalyzer) observeStatus(v string) {
	if !a.statusSeen {
		a.statusSeen = true
		a.statusOpen = v
	}
	if statusNamesRunningGame(v) {
		a.statusRunng = true
	}
}

// statusNamesRunningGame reports whether a serverinfo `status` value says a
// game is under way.
func statusNamesRunningGame(v string) bool {
	rest, ok := strings.CutSuffix(v, " left")
	if !ok || rest == "" {
		return false
	}
	return rest[0] >= '0' && rest[0] <= '9'
}

// parseInfoString parses a `fullserverinfo "\key\value\..."` stufftext into
// its key/value pairs: it strips the "fullserverinfo " prefix and the
// surrounding quotes, then walks the backslash-delimited pairs, skipping
// empty keys. Shared by MetadataAnalyzer (all keys), ItemAnalyzer and
// BackpackAnalyzer (the "map" key). Distinct from the package-level
// cleanLevelTitle(levelName) in match.go, which cleans a serverdata level
// title, not an info string.
func parseInfoString(cmd string) map[string]string {
	rest := strings.TrimPrefix(cmd, "fullserverinfo ")
	rest = strings.TrimSpace(rest)
	rest = strings.TrimPrefix(rest, "\"")
	if i := strings.LastIndexByte(rest, '"'); i >= 0 {
		rest = rest[:i]
	}
	parts := strings.Split(rest, "\\")
	start := 0
	if len(parts) > 0 && parts[0] == "" {
		start = 1
	}
	out := make(map[string]string, len(parts)/2)
	for i := start; i+1 < len(parts); i += 2 {
		if parts[i] == "" {
			continue
		}
		out[parts[i]] = parts[i+1]
	}
	return out
}

// Finalize converts the collected serverinfo + countdown text into a
// structured MetadataResult.
func (a *MetadataAnalyzer) Finalize(result *Result) error {
	mr := &MetadataResult{}

	if len(a.serverInfo) > 0 {
		// Copy so the analyzer's internal map can't be mutated by callers.
		serverInfo := make(map[string]string, len(a.serverInfo))
		for k, v := range a.serverInfo {
			serverInfo[k] = v
		}
		mr.ServerInfo = serverInfo
	}

	if a.countdownRaw != "" {
		mr.CountdownText = a.countdownRaw
		mr.MatchSettings = parseCountdownCenterprint(a.countdownRaw)
	}
	// The fairpacks row rides beside the countdown, not inside it, so it can
	// arrive on a demo whose centerprint never parsed into a table.
	if a.fairpacks != "" {
		if mr.MatchSettings == nil {
			mr.MatchSettings = &MatchSettings{}
		}
		mr.MatchSettings.Fairpacks = a.fairpacks
	}

	mr.FinalScores = a.finalScores

	if mr.ServerInfo == nil && mr.MatchSettings == nil && mr.CountdownText == "" && mr.FinalScores == nil {
		return nil
	}
	result.Metadata = mr
	return nil
}

// PopulateCore publishes the serverinfo `map` key so downstream producers
// (timeline: loc / floor-height / liquid / region control) can resolve the map
// even when the KTX demoinfo block is absent — see CoreOutputs.EffectiveMap —
// plus the `//finalscores` record, which the match node reads for the map,
// mode and team scores it could not resolve itself.
func (a *MetadataAnalyzer) PopulateCore(co *CoreOutputs) {
	co.ServerInfoMap = a.serverInfo["map"]
	co.FinalScores = a.finalScores
	co.ServerStatus = ServerStatus{
		AtOpen:      a.statusOpen,
		RunningSeen: a.statusRunng,
	}
}

// parseCountdownCenterprint walks the post-Q_normalizetext countdown table
// and pulls each known KTX setting row into a MatchSettings struct.
//
// The format is one setting per line, key on the left and value
// right-aligned. KTX uses fmt strings like `va("%s %4s\n", "Respawns", ...)`,
// so after normalization we get rows like:
//
//	"Deathmatch  3"
//	"Mode  D u e l"
//	"Respawns  KT2"
//	"Antilag    1"
//	"Teamplay   2"
//	"Timelimit  10"
//	"Overtime    3"
//	"Powerups  on"
//	"Noweapon   gl axe"
//	"matchtag draft"
//
// We split by line, take the first whitespace-separated token as the key,
// and treat the remainder as the value.
func parseCountdownCenterprint(text string) *MatchSettings {
	settings := &MatchSettings{}
	any := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "Countdown:") || strings.HasPrefix(line, "no matchtag") {
			continue
		}
		key, value := splitCountdownLine(line)
		if key == "" {
			continue
		}
		if applyCountdownField(settings, key, value) {
			any = true
		}
	}
	if !any {
		return nil
	}
	return settings
}

// splitCountdownLine splits a centerprint row into (key, value). KTX uses
// padded right-aligned values, so we treat the first whitespace run as the
// separator and keep everything after it (collapsing internal runs of spaces
// for cosmetic mode names like "D u e l" → "Duel").
func splitCountdownLine(line string) (string, string) {
	idx := strings.IndexFunc(line, isSpaceByte)
	if idx < 0 {
		return line, ""
	}
	key := line[:idx]
	rest := strings.TrimSpace(line[idx:])
	rest = collapseSpaces(rest)
	return key, rest
}

func isSpaceByte(r rune) bool { return r == ' ' || r == '\t' }

func collapseSpaces(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' {
			if !prevSpace {
				b.WriteRune(r)
			}
			prevSpace = true
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// applyCountdownField sets one row of the MatchSettings struct. Returns
// true if the field was recognised so the caller can tell whether the
// centerprint produced any structured data at all.
func applyCountdownField(s *MatchSettings, key, value string) bool {
	// Mode rendering uses "D u e l", "T e a m", "F F A", etc — strip spaces.
	flat := strings.ReplaceAll(value, " ", "")

	switch key {
	case "Mode":
		s.Mode = flat
	case "Deathmatch":
		s.Deathmatch = atoiSafe(value)
	case "Teamplay":
		s.Teamplay = atoiSafe(value)
	case "Timelimit":
		s.Timelimit = atoiSafe(value)
	case "Fraglimit":
		s.Fraglimit = atoiSafe(value)
	case "Respawns":
		s.Spawnmodel = flat
		if k := spawnmodelToK(flat); k >= 0 {
			s.SpawnK = &k
		}
	case "Antilag":
		s.Antilag = atoiSafe(value)
	case "Overtime":
		s.Overtime = flat
	case "Powerups":
		s.Powerups = flat
	case "Dmgfrags":
		s.Dmgfrags = isOn(flat)
	case "NoItems":
		s.NoItems = isOn(flat)
	case "Midair":
		s.Midair = isOn(flat)
	case "Instagib":
		s.Instagib = isOn(flat)
	case "Yawnmode":
		s.Yawnmode = isOn(flat)
	case "Airstep":
		s.Airstep = isOn(flat)
	case "VWep":
		s.VWep = isOn(flat)
	case "Noweapon":
		s.Noweapon = value
	case "matchtag":
		s.Matchtag = value
	case "SOCDv2":
		s.SOCDv2 = flat
	default:
		return false
	}
	return true
}

func atoiSafe(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

func isOn(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return v == "on" || v == "1" || v == "yes" || v == "true"
}

// spawnmodelToK reverses respawn_model_name_short(): see
// ktx/src/g_utils.c:2689. Returns -1 for unknown short names.
func spawnmodelToK(name string) int {
	switch strings.ToUpper(name) {
	case "QW":
		return 0
	case "KTS":
		return 1
	case "KT":
		return 2
	case "KTX":
		return 3
	case "KT2":
		return 4
	}
	return -1
}
