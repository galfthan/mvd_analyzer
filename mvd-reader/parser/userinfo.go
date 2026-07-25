package parser

import (
	"strconv"
	"strings"

	"github.com/mvd-analyzer/mvd-reader/mvd"
)

// UserInfoEvent is emitted when player info is updated
type UserInfoEvent struct {
	Player *mvd.PlayerInfo
	TimeMs int32

	// Vacated marks the svc_updateuserinfo that announces a client slot
	// going *empty* rather than a live userinfo change. When the server
	// drops a client it clears the client's name and both userinfo
	// contexts and then broadcasts a full client update
	// (mvdsv/src/sv_main.c:419-428, SV_DropClient), so the drop reaches
	// the demo as
	//
	//	svc_updatefrags   <slot> 0        (client->old_frags, just zeroed)
	//	svc_updateuserinfo <slot> <userid> ""
	//
	// back to back in the same server frame (SV_FullClientUpdate,
	// sv_main.c:487-513). An empty userinfo string is therefore the wire's
	// own end-of-occupancy marker, and the frag reset that precedes it in
	// the same frame is slot bookkeeping, not a score.
	//
	// The parser deliberately keeps the last known name/team/colors on the
	// PlayerInfo (parseUserInfoString returns early on an empty string), so
	// a consumer can still tell *who* left.
	//
	// Vacated reports the wire fact (an empty userinfo string) and nothing
	// more; two of them are not drops and a consumer must filter both:
	//
	//   - the MVD header's full-state block writes one for every
	//     unoccupied slot (sv_demo.c:1438-1467 iterates all MAX_CLIENTS
	//     regardless of client state). The entry is NOT zeroed: svs.clients
	//     is only cleared when a new connection lands on the slot
	//     (SVC_DirectConnect, mvdsv/src/sv_main.c:1351), so a slot whose
	//     previous occupant left before the recording started still carries
	//     that client's stale userid — observed as
	//     `t=0 slot=15 uid=5081 info=""` on
	//     demo-test-data/mvd/special-cases/4on4_l_vs_la[e1m2].mvd. The
	//     userid is therefore no help here; only "the slot was never
	//     occupied in this recording" identifies it.
	//   - the server's per-client replay of the whole client table, which
	//     writes `svc_updateuserinfo <slot> 0 ""` for every occupied slot
	//     immediately followed by the real string. This is a dem_single
	//     block addressed at ONE client (mvdsv's equivalent is SV_Spawn_f's
	//     SV_FullClientUpdateToClient loop, sv_user.c:833-841), not a
	//     broadcast: on 4on4_l_vs_la[e1m2] it appears 24 times, every one of
	//     them a dem_single aimed at the same spectator in slot 12, each
	//     emptying all eight in-game slots for one frame.
	//
	// A genuine drop always carries the departing client's own userid,
	// because SV_DropClient clears the name and userinfo but not the
	// userid. Userid 0 on an empty userinfo therefore means "resend", the
	// same convention the rest of the pipeline applies to userid 0.
	Vacated bool

	// Partial marks an event synthesised from svc_setinfo (one key/value)
	// rather than decoded from a full svc_updateuserinfo. It matters
	// because the Player snapshot on a partial event is mostly CACHE, not
	// wire: parseSetInfo overwrites exactly the one key the server sent and
	// leaves every other field — crucially UserID — at whatever the last
	// full userinfo put there.
	//
	// That makes a partial event useless as a slot-occupancy boundary, and
	// actively misleading as one. mvdsv emits `svc_setinfo <slot> "*auth"
	// ""` from SV_Logout (sv_login.c:644-646), which runs both when a
	// client is dropped (SV_DropClient, sv_main.c:410) AND during the next
	// client's connect handshake (SV_Login, sv_login.c:579, calls it at
	// :588; the function's own comment at :576 is "called on connect after
	// cmd new is issued"). The second one lands on a slot the
	// parser still remembers as the departed client, so its synthesised
	// event looks exactly like the departed client's userid coming back.
	// Observed on hub gameId 216835 slot 7: rusti is dropped at t=613452
	// and the only later wire message for the slot before Luk's real
	// userinfo at t=766898 is `svc_setinfo 7 "*auth" ""` at t=685676.
	//
	// Consumers that track occupancy must therefore treat a partial event
	// as an in-place update to whoever already holds the slot, never as a
	// connect, drop or handover.
	Partial bool
}

func (e *UserInfoEvent) EventType() EventType { return EventUserInfo }
func (e *UserInfoEvent) EventTime() float64   { return float64(e.TimeMs) * 0.001 }
func (e *UserInfoEvent) EventTimeMs() int32   { return e.TimeMs }

// parseUserInfo parses svc_updateuserinfo message
func (p *Parser) parseUserInfo(r *mvd.BufferReader, timeMs int32) error {
	// Read player slot
	slot, err := r.ReadByte()
	if err != nil {
		return err
	}

	if slot >= mvd.MaxClients {
		// Invalid slot, skip
		r.ReadUint32() // user_id
		r.ReadString() // userinfo
		return nil
	}

	// Read user ID
	userID, err := r.ReadUint32()
	if err != nil {
		return err
	}

	// Read userinfo string
	userinfo, err := r.ReadString()
	if err != nil {
		return err
	}

	// Parse userinfo string
	player := p.players[slot]
	if player == nil {
		player = &mvd.PlayerInfo{Slot: int(slot)}
		p.players[slot] = player
	}

	player.UserID = int(userID)
	parseUserInfoString(userinfo, player)

	// Emit event. An empty userinfo string is the server's drop broadcast
	// (see UserInfoEvent.Vacated); svc_setinfo can never carry it, so only
	// this path sets the flag.
	return p.emit(&UserInfoEvent{Player: player, TimeMs: timeMs, Vacated: userinfo == ""})
}

// parseSetInfo parses svc_setinfo (single key/value update for a player).
// This is how name/team/skin changes are propagated mid-game; without
// handling it the parser keeps the initial userinfo and chat / timeline
// data fall out of sync with the player's current name.
func (p *Parser) parseSetInfo(r *mvd.BufferReader, timeMs int32) error {
	slot, err := r.ReadByte()
	if err != nil {
		return err
	}
	key, err := r.ReadString()
	if err != nil {
		return err
	}
	value, err := r.ReadString()
	if err != nil {
		return err
	}

	if slot >= mvd.MaxClients {
		return nil
	}

	player := p.players[slot]
	if player == nil {
		player = &mvd.PlayerInfo{Slot: int(slot)}
		p.players[slot] = player
	}

	switch key {
	case "name":
		player.Name = cleanString(value)
	case "team":
		player.Team = cleanString(value)
	case "topcolor":
		if c, err := strconv.Atoi(value); err == nil {
			player.TopColor = c
		}
	case "bottomcolor":
		if c, err := strconv.Atoi(value); err == nil {
			player.BottomColor = c
		}
	case "*auth":
		player.Auth = cleanString(value)
	case "*spectator":
		player.Spectator = value == "1"
	default:
		// Other keys (rate, msg, skin, ...) are not tracked.
		return nil
	}

	return p.emit(&UserInfoEvent{Player: player, TimeMs: timeMs, Partial: true})
}

// parseUserInfoString parses a backslash-delimited userinfo string
// Format: \key1\value1\key2\value2\...
//
// The string is the FULL replacement userinfo (svc_updateuserinfo), so the
// spectator flag is recomputed from scratch: absent key means not a
// spectator. ezquake does the same on every update (CL_ProcessUserInfo,
// cl_parse.c:2118-2123). Without the reset, a slot reused by a player after
// a spectator disconnects — or a spectator who joins the game (mvdsv removes
// the key rather than sending "*spectator\0", sv_user.c:2711) — inherits a
// stale Spectator=true. Name/team/colors are left as carry-forward since
// real userinfo strings always include them.
func parseUserInfoString(s string, player *mvd.PlayerInfo) {
	if s == "" {
		return
	}
	player.Spectator = false

	// Remove leading backslash if present
	if s[0] == '\\' {
		s = s[1:]
	}

	parts := strings.Split(s, "\\")
	for i := 0; i+1 < len(parts); i += 2 {
		key := parts[i]
		value := parts[i+1]

		switch key {
		case "name":
			player.Name = cleanString(value)
		case "team":
			player.Team = cleanString(value)
		case "topcolor":
			if c, err := strconv.Atoi(value); err == nil {
				player.TopColor = c
			}
		case "bottomcolor":
			if c, err := strconv.Atoi(value); err == nil {
				player.BottomColor = c
			}
		case "*auth":
			player.Auth = cleanString(value)
		case "*spectator", "spectator":
			// mvdsv strips the client-set "spectator" key and re-adds the
			// server-set star key before broadcast (sv_main.c:1065-1066,
			// Info_SetValueForStarKey(userinfo, "*spectator", "1")), so full
			// userinfo strings in MVDs only ever carry "*spectator". The
			// bare spelling is kept for non-mvdsv sources.
			player.Spectator = value == "1"
		}
	}
}

// qNormalizeTable is the Quake character normalization table used by
// ezquake/mvdsv `Q_normalizetext`. It maps every byte (0-255) to a printable
// ASCII equivalent: high-bit "gold" letters fold back to their plain twins,
// font glyphs in 0x00-0x1F become digits/brackets/dots, and unknown control
// bytes become '#'. Centralizing this table here means every consumer of
// player names (userinfo, setinfo, prints, demoinfo JSON) ends up with the
// same canonical string, so cross-references by name actually join.
var qNormalizeTable = func() [256]byte {
	var t [256]byte
	for i := 0; i < 256; i++ {
		t[i] = '#'
	}
	// Printable low ASCII passes through unchanged.
	for i := 32; i < 127; i++ {
		t[i] = byte(i)
	}
	// Quake font glyphs in 0x00-0x1F.
	t[0] = '#'
	t[5] = '.'
	t[10] = '\n'
	t[13] = '\r'
	t[14] = '.'
	t[15] = '.'
	t[16] = '['
	t[17] = ']'
	for i := 18; i <= 27; i++ {
		t[i] = byte('0' + (i - 18))
	}
	t[28] = '.'
	t[29] = '('
	t[30] = '='
	t[31] = ')'
	t[46] = '.' // already '.', but kept explicit
	t[127] = '>'
	// Mirror everything for the high-bit "gold" range: byte b+128 maps the
	// same way as b. This is what folds 0xCE -> 'N', 0xF0 -> 'p', 0xAE -> '.'.
	for i := 0; i < 128; i++ {
		t[i+128] = t[i]
	}
	// A handful of high-bit specific overrides from ezquake's table.
	t[128] = '('
	t[129] = '='
	t[130] = ')'
	t[141] = '<'
	return t
}()

// NormalizeQuakeText is the exported version that takes raw bytes. Used by
// the analyzer package to normalize names lifted from KTX's demoinfo JSON
// (where each byte arrived as a JSON \u00XX escape).
func NormalizeQuakeText(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			continue
		}
		out = append(out, qNormalizeTable[c])
	}
	return string(out)
}

// cleanString normalizes a Quake string to plain ASCII using the
// ezquake/mvdsv Q_normalizetext mapping. Embedded NULs are dropped because
// they would terminate downstream C strings; everything else is mapped via
// the table above.
func cleanString(s string) string {
	return NormalizeQuakeText([]byte(s))
}

// StripChatMarkup removes ezQuake chat markup that survives
// Q-normalisation, leaving plain readable text. Mirrors qw-web's
// formatQuakeMessage (in static/app.js) minus the HTML span generation.
//
// Removed in order:
//
//   - leading "\r" (mvdsv prepends this when broadcasting team chat),
//   - "&cRGB" colour codes (3 hex digits) and "&r" reset,
//   - trailing single-letter sound triggers ("!K"/"!H"/"!G"/"!C", etc.),
//   - macro delimiters "{", "}", "[", "]" (ezQuake teamplay macros).
//
// Whitespace runs are then collapsed to a single space and the result
// is trimmed. The transform is idempotent — re-running on already-clean
// text is a no-op.
func StripChatMarkup(s string) string {
	if s == "" {
		return s
	}
	// 1. Drop a leading "\r" if present.
	if s[0] == '\r' {
		s = s[1:]
	}
	// 2. Drop a trailing single-letter sound trigger like "!K" / "!H".
	if n := len(s); n >= 2 && s[n-2] == '!' {
		c := s[n-1]
		if c >= 'A' && c <= 'Z' {
			s = s[:n-2]
		}
	}
	// 3. Walk the string, dropping &cRGB / &r and macro delimiters.
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		// "&cRGB" — three hex digits after &c.
		if i+5 <= len(s) && s[i] == '&' && s[i+1] == 'c' &&
			isHexDigit(s[i+2]) && isHexDigit(s[i+3]) && isHexDigit(s[i+4]) {
			i += 5
			continue
		}
		// "&r" — colour reset.
		if i+2 <= len(s) && s[i] == '&' && s[i+1] == 'r' {
			i += 2
			continue
		}
		// Macro delimiters.
		c := s[i]
		if c == '{' || c == '}' || c == '[' || c == ']' {
			i++
			continue
		}
		out = append(out, c)
		i++
	}
	// 4. Collapse whitespace runs and trim.
	collapsed := make([]byte, 0, len(out))
	prevSpace := true // leading-space trim
	for _, c := range out {
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if !prevSpace {
				collapsed = append(collapsed, ' ')
				prevSpace = true
			}
			continue
		}
		collapsed = append(collapsed, c)
		prevSpace = false
	}
	// trim trailing space
	if len(collapsed) > 0 && collapsed[len(collapsed)-1] == ' ' {
		collapsed = collapsed[:len(collapsed)-1]
	}
	return string(collapsed)
}

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
