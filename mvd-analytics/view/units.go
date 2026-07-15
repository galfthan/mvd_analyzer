package view

import "github.com/mvd-analyzer/mvd-analytics/result"

// Time-UNIT views: the REST transport surface's self-describing, unit-
// selectable shape for every demo endpoint that carries a MATCH-POSITION
// timestamp (schema v56).
//
// The rule (API.md §2.1):
//
//   - Each endpoint keeps its CURRENT native unit as the default — the
//     pass-through sections (frags, damage, shots, chat, airgibs, backpacks,
//     weapon-pickups, items timeline, overview) stay int32 MILLISECONDS as
//     stored; the derived views (events, buckets rows, state-at, stream-slice
//     envelope, loc-trails, items summary) stay float64 SECONDS as computed.
//   - The `units=ms|s` query param overrides it: `units=s` on an ms-native
//     endpoint renders its match-position timestamps as float64 seconds;
//     `units=ms` on a seconds-native endpoint renders them as int32 ms. Field
//     NAMES never change — only the number's unit/type. Requesting the native
//     unit is a no-op (byte-identical to the pre-v56 body, plus timeUnit).
//   - Every governed response echoes a top-level `timeUnit` ("ms"|"s") naming
//     the effective unit, so the /events-vs-/shots ambiguity can't recur.
//   - DENSE per-sample payloads always stay int32 ms regardless of the param
//     (the documented exception): /aim crosshair `t` + lgRamp `since`, the raw
//     stream entries embedded in /stream-slice (PlayerSlice), and the columnar
//     buckets axis (startMs/windowMs). These names/units never change here.
//
// The stored result.* structs and their JSON tags are the ON-DISK contract
// (qw-analyze / WASM emit them verbatim, in ms) and are left untouched. These
// view types re-shape a section at the HTTP boundary only. Governed timestamp
// fields are typed `any` so one struct serves both units: filled with int32
// (ms) or float64 (seconds) by the constructor. Sub-blocks with no timestamp
// (byPlayer / byWeapon / matrix / reconciliation / scoreboard / player-state /
// dense stream slices) are shared by reference — these views are read-only,
// marshal-and-discard.

// TimeUnit is the effective unit for a response's governed timestamps.
type TimeUnit string

const (
	// UnitMs renders governed match positions as stored int32 milliseconds.
	UnitMs TimeUnit = "ms"
	// UnitSec renders governed match positions as float64 seconds.
	UnitSec TimeUnit = "s"
)

// MsToUnit renders a stored int32-ms match position under the effective unit:
// int32 ms as-is (byte-identical to the stored field) or float64 seconds via
// secs (correctly-rounded, JSON-clean division). The result is boxed in `any`
// so a single view field can carry either.
func MsToUnit(ms int32, u TimeUnit) any {
	if u == UnitSec {
		return secs(ms)
	}
	return ms
}

// MsToUnitOpt is MsToUnit for an omitempty field whose stored int32 form omits
// zero: 0 ms → nil (interface nil ⇒ the JSON key is omitted), matching the
// stored `omitempty` behaviour exactly. Any non-zero value renders per the unit.
func MsToUnitOpt(ms int32, u TimeUnit) any {
	if ms == 0 {
		return nil
	}
	return MsToUnit(ms, u)
}

// SecToUnit renders a derived-view float64-seconds match position under the
// effective unit: float64 seconds as-is (byte-identical to the native view
// field) or int32 ms via secToMs. The seconds values in the derived views
// originate from int32 ms, so the ×1000 round back is lossless.
func SecToUnit(sec float64, u TimeUnit) any {
	if u == UnitMs {
		return secToMs(sec)
	}
	return sec
}

// --- /frags (ms-native) ---

type fragEntryUnits struct {
	Time       any    `json:"time"`
	Killer     string `json:"killer"`
	Victim     string `json:"victim"`
	Weapon     string `json:"weapon"`
	IsSuicide  bool   `json:"isSuicide,omitempty"`
	IsTeamKill bool   `json:"isTeamKill,omitempty"`
}

// FragsUnitsView is result.FragResult with the kill-log times unit-governed
// and a timeUnit echo. The frags slice preserves the null-vs-[] distinction
// (null when summary dropped it, [] when a filter matched nothing).
type FragsUnitsView struct {
	TimeUnit   TimeUnit                       `json:"timeUnit"`
	TotalFrags int                            `json:"totalFrags"`
	Frags      []fragEntryUnits               `json:"frags"`
	ByWeapon   map[string]int                 `json:"byWeapon"`
	ByPlayer   map[string]*result.PlayerFrags `json:"byPlayer"`
}

// FragsUnits reshapes a (possibly filtered) FragResult to the unit surface.
func FragsUnits(f *result.FragResult, u TimeUnit) *FragsUnitsView {
	if f == nil {
		return nil
	}
	out := &FragsUnitsView{
		TimeUnit:   u,
		TotalFrags: f.TotalFrags,
		ByWeapon:   f.ByWeapon,
		ByPlayer:   f.ByPlayer,
	}
	if f.Frags != nil {
		out.Frags = make([]fragEntryUnits, len(f.Frags))
		for i, e := range f.Frags {
			out.Frags[i] = fragEntryUnits{
				Time: MsToUnit(e.Time, u), Killer: e.Killer, Victim: e.Victim,
				Weapon: e.Weapon, IsSuicide: e.IsSuicide, IsTeamKill: e.IsTeamKill,
			}
		}
	}
	return out
}

// --- /damage (ms-native) ---

type damageEntryUnits struct {
	Time      any    `json:"time"`
	Attacker  string `json:"attacker"`
	Victim    string `json:"victim"`
	Weapon    string `json:"weapon"`
	Damage    int    `json:"damage"`
	IsSplash  bool   `json:"isSplash,omitempty"`
	IsEnv     bool   `json:"isEnv,omitempty"`
	IsSelf    bool   `json:"isSelf,omitempty"`
	IsTeam    bool   `json:"isTeam,omitempty"`
	VictimWep string `json:"victimWep,omitempty"`
	Bounded   *int   `json:"bounded,omitempty"`
}

type positionalKillUnits struct {
	Time      any    `json:"time"`
	Attacker  string `json:"attacker"`
	Victim    string `json:"victim"`
	IsTeam    bool   `json:"isTeam,omitempty"`
	Bounded   *int   `json:"bounded,omitempty"`
	Damage    int    `json:"damage,omitempty"`
	VictimWep string `json:"victimWep,omitempty"`
}

// DamageUnitsView is result.DamageResult with per-hit + positional-kill times
// unit-governed and a timeUnit echo. Non-time sub-blocks are carried by
// reference.
type DamageUnitsView struct {
	TimeUnit      TimeUnit                        `json:"timeUnit"`
	TotalDamage   int                             `json:"totalDamage"`
	Events        []damageEntryUnits              `json:"events"`
	ByWeapon      map[string]int                  `json:"byWeapon"`
	ByPlayer      map[string]*result.PlayerDamage `json:"byPlayer"`
	Matrix        []result.DamagePair             `json:"matrix"`
	Telefrags     []positionalKillUnits           `json:"telefrags,omitempty"`
	Stomps        []positionalKillUnits           `json:"stomps,omitempty"`
	Scoreboard    *result.DamageReconciliation    `json:"scoreboard,omitempty"`
	Dmg           string                          `json:"dmg,omitempty"`
	BoundedMode   string                          `json:"boundedMode,omitempty"`
	BoundedSource string                          `json:"boundedSource,omitempty"`
}

// DamageUnits reshapes a (possibly filtered / family-transformed) DamageResult
// to the unit surface. events preserves the null-vs-[] distinction;
// telefrags/stomps stay omitempty (absent when nil).
func DamageUnits(d *result.DamageResult, u TimeUnit) *DamageUnitsView {
	if d == nil {
		return nil
	}
	out := &DamageUnitsView{
		TimeUnit:      u,
		TotalDamage:   d.TotalDamage,
		ByWeapon:      d.ByWeapon,
		ByPlayer:      d.ByPlayer,
		Matrix:        d.Matrix,
		Scoreboard:    d.Scoreboard,
		Dmg:           d.Dmg,
		BoundedMode:   d.BoundedMode,
		BoundedSource: d.BoundedSource,
		Telefrags:     positionalKillUnitsList(d.Telefrags, u),
		Stomps:        positionalKillUnitsList(d.Stomps, u),
	}
	if d.Events != nil {
		out.Events = make([]damageEntryUnits, len(d.Events))
		for i, e := range d.Events {
			out.Events[i] = damageEntryUnits{
				Time: MsToUnit(e.Time, u), Attacker: e.Attacker, Victim: e.Victim,
				Weapon: e.Weapon, Damage: e.Damage, IsSplash: e.IsSplash,
				IsEnv: e.IsEnv, IsSelf: e.IsSelf, IsTeam: e.IsTeam,
				VictimWep: e.VictimWep, Bounded: e.Bounded,
			}
		}
	}
	return out
}

func positionalKillUnitsList(in []result.PositionalKill, u TimeUnit) []positionalKillUnits {
	if in == nil {
		return nil
	}
	out := make([]positionalKillUnits, len(in))
	for i, k := range in {
		out[i] = positionalKillUnits{
			Time: MsToUnit(k.Time, u), Attacker: k.Attacker, Victim: k.Victim,
			IsTeam: k.IsTeam, Bounded: k.Bounded, Damage: k.Damage, VictimWep: k.VictimWep,
		}
	}
	return out
}

// --- /shots (ms-native) ---

type shotUnits struct {
	Time        any      `json:"time"`
	Player      string   `json:"player"`
	Team        string   `json:"team,omitempty"`
	Weapon      string   `json:"weapon"`
	Source      string   `json:"source"`
	Hit         bool     `json:"hit,omitempty"`
	Victims     []string `json:"victims,omitempty"`
	VictimKinds []string `json:"victimKinds,omitempty"`
}

// ShotsUnitsView is result.ShotsResult with per-fire times unit-governed and a
// timeUnit echo. The per-player aggregates + reconciliation carry no time.
type ShotsUnitsView struct {
	TimeUnit       TimeUnit                    `json:"timeUnit"`
	Shots          []shotUnits                 `json:"shots"`
	ByPlayer       []result.PlayerShots        `json:"byPlayer,omitempty"`
	Reconciliation *result.ShotsReconciliation `json:"reconciliation,omitempty"`
}

// ShotsUnits reshapes the shots section to the unit surface.
func ShotsUnits(s *result.ShotsResult, u TimeUnit) *ShotsUnitsView {
	if s == nil {
		return nil
	}
	out := &ShotsUnitsView{
		TimeUnit:       u,
		ByPlayer:       s.ByPlayer,
		Reconciliation: s.Reconciliation,
	}
	if s.Shots != nil {
		out.Shots = make([]shotUnits, len(s.Shots))
		for i, sh := range s.Shots {
			out.Shots[i] = shotUnits{
				Time: MsToUnit(sh.Time, u), Player: sh.Player, Team: sh.Team,
				Weapon: sh.Weapon, Source: sh.Source, Hit: sh.Hit,
				Victims: sh.Victims, VictimKinds: sh.VictimKinds,
			}
		}
	}
	return out
}

// --- /chat (ms-native; bare-array body gains a {timeUnit, messages} envelope) ---

type chatEventUnits struct {
	Time         any    `json:"time"`
	Type         string `json:"type"`
	Player       string `json:"player"`
	Team         string `json:"team"`
	Message      string `json:"message"`
	MessageClean string `json:"messageClean,omitempty"`
	Victim       string `json:"victim,omitempty"`
	Weapon       string `json:"weapon,omitempty"`
}

// ChatUnitsView wraps the chat/teamsay slice in a {timeUnit, messages}
// envelope (the timeUnit echo needs a top-level object). messages is always a
// list — never null.
type ChatUnitsView struct {
	TimeUnit TimeUnit         `json:"timeUnit"`
	Messages []chatEventUnits `json:"messages"`
}

// ChatUnits reshapes the chat/teamsay event slice to the unit surface.
func ChatUnits(in []result.MatchEvent, u TimeUnit) *ChatUnitsView {
	out := &ChatUnitsView{TimeUnit: u, Messages: make([]chatEventUnits, len(in))}
	for i, e := range in {
		out.Messages[i] = chatEventUnits{
			Time: MsToUnit(e.Time, u), Type: e.Type, Player: e.Player, Team: e.Team,
			Message: e.Message, MessageClean: e.MessageClean, Victim: e.Victim, Weapon: e.Weapon,
		}
	}
	return out
}

// --- /airgibs (ms-native; bare-array body gains a {timeUnit, airgibs} envelope) ---

type airgibEventUnits struct {
	Time                any     `json:"time"`
	Attacker            string  `json:"attacker"`
	AttackerTeam        string  `json:"attackerTeam,omitempty"`
	AttackerUserID      int     `json:"attackerUserID,omitempty"`
	Victim              string  `json:"victim"`
	VictimTeam          string  `json:"victimTeam,omitempty"`
	VictimUserID        int     `json:"victimUserID,omitempty"`
	Height              float32 `json:"height"`
	HeightAboveAttacker float32 `json:"heightAboveAttacker,omitempty"`
	Loc                 string  `json:"loc,omitempty"`
	Damage              int     `json:"damage"`
	Lethal              bool    `json:"lethal,omitempty"`
}

// AirgibsUnitsView wraps the airgib key-moment list in a {timeUnit, airgibs}
// envelope. airgibs is always a list — never null.
type AirgibsUnitsView struct {
	TimeUnit TimeUnit           `json:"timeUnit"`
	Airgibs  []airgibEventUnits `json:"airgibs"`
}

// AirgibsUnits reshapes the airgib key-moment list to the unit surface.
func AirgibsUnits(in []result.AirgibEvent, u TimeUnit) *AirgibsUnitsView {
	out := &AirgibsUnitsView{TimeUnit: u, Airgibs: make([]airgibEventUnits, len(in))}
	for i, a := range in {
		out.Airgibs[i] = airgibEventUnits{
			Time: MsToUnit(a.Time, u), Attacker: a.Attacker, AttackerTeam: a.AttackerTeam,
			AttackerUserID: a.AttackerUserID, Victim: a.Victim, VictimTeam: a.VictimTeam,
			VictimUserID: a.VictimUserID, Height: a.Height, HeightAboveAttacker: a.HeightAboveAttacker,
			Loc: a.Loc, Damage: a.Damage, Lethal: a.Lethal,
		}
	}
	return out
}

// --- /backpacks (ms-native; bare-array body gains a {timeUnit, backpacks} envelope) ---

type backpackDropUnits struct {
	Time   any        `json:"time"`
	Player string     `json:"player"`
	Team   string     `json:"team,omitempty"`
	Weapon string     `json:"weapon"`
	Origin [3]float32 `json:"origin"`
	Loc    string     `json:"loc,omitempty"`
	EntNum int        `json:"entNum"`
}

// BackpacksUnitsView wraps the backpack-drop list in a {timeUnit, backpacks}
// envelope. backpacks is always a list — never null.
type BackpacksUnitsView struct {
	TimeUnit  TimeUnit            `json:"timeUnit"`
	Backpacks []backpackDropUnits `json:"backpacks"`
}

// BackpacksUnits reshapes the backpack-drop list to the unit surface.
func BackpacksUnits(in []result.BackpackDrop, u TimeUnit) *BackpacksUnitsView {
	out := &BackpacksUnitsView{TimeUnit: u, Backpacks: make([]backpackDropUnits, len(in))}
	for i, b := range in {
		out.Backpacks[i] = backpackDropUnits{
			Time: MsToUnit(b.Time, u), Player: b.Player, Team: b.Team, Weapon: b.Weapon,
			Origin: b.Origin, Loc: b.Loc, EntNum: b.EntNum,
		}
	}
	return out
}

// --- /weapon-pickups (ms-native; bare-array body gains a {timeUnit, pickups} envelope) ---

type weaponPickupUnits struct {
	Time          any    `json:"time"`
	Player        string `json:"player"`
	Team          string `json:"team,omitempty"`
	Weapon        string `json:"weapon"`
	Source        string `json:"source"`
	HadBefore     bool   `json:"hadBefore"`
	Inferred      bool   `json:"inferred,omitempty"`
	Kills         int    `json:"kills"`
	NextDeathTime any    `json:"nextDeathTime,omitempty"`
	BackpackEnt   int    `json:"backpackEnt,omitempty"`
	Dropper       string `json:"dropper,omitempty"`
	DropperTeam   string `json:"dropperTeam,omitempty"`
	DropTime      any    `json:"dropTime,omitempty"`
}

// WeaponPickupsUnitsView wraps the weapon-pickup list in a {timeUnit, pickups}
// envelope. pickups is always a list — never null. time is unit-governed; the
// nextDeathTime / dropTime match positions are governed too (0 stays omitted).
type WeaponPickupsUnitsView struct {
	TimeUnit TimeUnit            `json:"timeUnit"`
	Pickups  []weaponPickupUnits `json:"pickups"`
}

// WeaponPickupsUnits reshapes the weapon-pickup list to the unit surface.
func WeaponPickupsUnits(in []result.WeaponPickup, u TimeUnit) *WeaponPickupsUnitsView {
	out := &WeaponPickupsUnitsView{TimeUnit: u, Pickups: make([]weaponPickupUnits, len(in))}
	for i, wp := range in {
		out.Pickups[i] = weaponPickupUnits{
			Time: MsToUnit(wp.Time, u), Player: wp.Player, Team: wp.Team, Weapon: wp.Weapon,
			Source: wp.Source, HadBefore: wp.HadBefore, Inferred: wp.Inferred, Kills: wp.Kills,
			NextDeathTime: MsToUnitOpt(wp.NextDeathTime, u), BackpackEnt: wp.BackpackEnt,
			Dropper: wp.Dropper, DropperTeam: wp.DropperTeam, DropTime: MsToUnitOpt(wp.DropTime, u),
		}
	}
	return out
}

// --- /items timeline (ms-native) ---

type itemPhaseUnits struct {
	AvailableFrom any    `json:"availableFrom"`
	TakenAt       any    `json:"takenAt,omitempty"`
	TakenBy       string `json:"takenBy,omitempty"`
	Team          string `json:"team,omitempty"`
	RespawnAt     any    `json:"respawnAt,omitempty"`
}

type itemTimelineUnits struct {
	Name   string           `json:"name"`
	Kind   string           `json:"kind"`
	EntNum int              `json:"entNum"`
	X      float32          `json:"x"`
	Y      float32          `json:"y"`
	Z      float32          `json:"z"`
	Loc    string           `json:"loc,omitempty"`
	Phases []itemPhaseUnits `json:"phases"`
}

// ItemsUnitsView is result.ItemsResult with every phase timeline unit-governed
// and a timeUnit echo. availableFrom is always present; takenAt/respawnAt stay
// omitempty (0 omitted).
type ItemsUnitsView struct {
	TimeUnit TimeUnit            `json:"timeUnit"`
	Items    []itemTimelineUnits `json:"items"`
}

// ItemsUnits reshapes the per-item phase timelines to the unit surface.
func ItemsUnits(it *result.ItemsResult, u TimeUnit) *ItemsUnitsView {
	if it == nil {
		return nil
	}
	out := &ItemsUnitsView{TimeUnit: u}
	if it.Items != nil {
		out.Items = make([]itemTimelineUnits, len(it.Items))
		for i, item := range it.Items {
			iv := itemTimelineUnits{
				Name: item.Name, Kind: item.Kind, EntNum: item.EntNum,
				X: item.X, Y: item.Y, Z: item.Z, Loc: item.Loc,
			}
			if item.Phases != nil {
				iv.Phases = make([]itemPhaseUnits, len(item.Phases))
				for j, ph := range item.Phases {
					iv.Phases[j] = itemPhaseUnits{
						AvailableFrom: MsToUnit(ph.AvailableFrom, u),
						TakenAt:       MsToUnitOpt(ph.TakenAt, u),
						TakenBy:       ph.TakenBy,
						Team:          ph.Team,
						RespawnAt:     MsToUnitOpt(ph.RespawnAt, u),
					}
				}
			}
			out.Items[i] = iv
		}
	}
	return out
}

// --- /items summary (seconds-native) ---

type itemTakeUnits struct {
	T       any    `json:"t"`
	TakenBy string `json:"takenBy,omitempty"`
	Team    string `json:"team,omitempty"`
}

type itemSummaryUnits struct {
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	EntNum     int            `json:"entNum"`
	Loc        string         `json:"loc,omitempty"`
	TakenCount int            `json:"takenCount"`
	ByPlayer   map[string]int `json:"byPlayer,omitempty"`
	FirstTake  *itemTakeUnits `json:"firstTake,omitempty"`
}

// ItemsSummaryUnitsView is the summary=true /items shape with firstTake.t
// unit-governed and a timeUnit echo. The summary's firstTake.t is seconds-
// native.
type ItemsSummaryUnitsView struct {
	TimeUnit TimeUnit           `json:"timeUnit"`
	Items    []itemSummaryUnits `json:"items"`
}

// ItemsSummaryUnits reshapes the item-take summary to the unit surface.
func ItemsSummaryUnits(sv *ItemsSummaryView, u TimeUnit) *ItemsSummaryUnitsView {
	if sv == nil {
		return nil
	}
	out := &ItemsSummaryUnitsView{TimeUnit: u, Items: make([]itemSummaryUnits, len(sv.Items))}
	for i, it := range sv.Items {
		iu := itemSummaryUnits{
			Name: it.Name, Kind: it.Kind, EntNum: it.EntNum, Loc: it.Loc,
			TakenCount: it.TakenCount, ByPlayer: it.ByPlayer,
		}
		if it.FirstTake != nil {
			iu.FirstTake = &itemTakeUnits{
				T: SecToUnit(it.FirstTake.T, u), TakenBy: it.FirstTake.TakenBy, Team: it.FirstTake.Team,
			}
		}
		out.Items[i] = iu
	}
	return out
}

// --- /events (seconds-native) ---

// eventDetailTimeKeys are the TaggedEvent.Detail keys carrying a match-
// position time in float64 seconds (from view.Events). They convert with the
// primary `t` under units=ms; every other Detail value passes through.
var eventDetailTimeKeys = []string{"endTime", "duration"}

type taggedEventUnits struct {
	T      any            `json:"t"`
	Type   string         `json:"type"`
	Player string         `json:"player,omitempty"`
	Detail map[string]any `json:"detail,omitempty"`
}

// EventsUnitsView is the /events response with each event's `t` (and the
// endTime/duration Detail sub-times) unit-governed and a timeUnit echo.
type EventsUnitsView struct {
	TimeUnit TimeUnit           `json:"timeUnit"`
	Events   []taggedEventUnits `json:"events"`
}

// EventsUnits reshapes an already-built EventsView to the unit surface. Under
// the native seconds unit the Detail maps pass through by reference; under
// units=ms a copy converts the sub-time keys so the whole event is consistent.
func EventsUnits(ev *EventsView, u TimeUnit) *EventsUnitsView {
	if ev == nil {
		return nil
	}
	out := &EventsUnitsView{TimeUnit: u, Events: make([]taggedEventUnits, len(ev.Events))}
	for i, e := range ev.Events {
		out.Events[i] = taggedEventUnits{
			T: SecToUnit(e.T, u), Type: e.Type, Player: e.Player,
			Detail: eventDetailUnits(e.Detail, u),
		}
	}
	return out
}

// eventDetailUnits governs the time-valued Detail keys. The native seconds
// unit needs no rewrite (share the map); units=ms copies the map and converts
// endTime/duration from float64 seconds to int32 ms.
func eventDetailUnits(detail map[string]any, u TimeUnit) map[string]any {
	if detail == nil || u == UnitSec {
		return detail
	}
	out := make(map[string]any, len(detail))
	for k, v := range detail {
		out[k] = v
	}
	for _, k := range eventDetailTimeKeys {
		if v, ok := out[k]; ok {
			if sec, ok := v.(float64); ok {
				out[k] = secToMs(sec)
			}
		}
	}
	return out
}

// --- /buckets row layout (seconds-native) ---

type viewBucketUnits struct {
	T       any                       `json:"t"`
	Players map[string]map[string]any `json:"p"`
	Team    map[string]map[string]any `json:"team,omitempty"`
	Partial bool                      `json:"partial,omitempty"`
}

// BucketsUnitsView is the row-layout /buckets response with each bucket's `t`
// unit-governed and a timeUnit echo. windowMs is an ms-always axis (a window
// SIZE, not a match position) and never converts.
type BucketsUnitsView struct {
	TimeUnit TimeUnit          `json:"timeUnit"`
	WindowMs int               `json:"windowMs"`
	Buckets  []viewBucketUnits `json:"buckets"`
}

// BucketsUnits reshapes an already-built row BucketsView to the unit surface.
func BucketsUnits(bv *BucketsView, u TimeUnit) *BucketsUnitsView {
	if bv == nil {
		return nil
	}
	out := &BucketsUnitsView{TimeUnit: u, WindowMs: bv.WindowMs}
	if bv.Buckets != nil {
		out.Buckets = make([]viewBucketUnits, len(bv.Buckets))
		for i, b := range bv.Buckets {
			out.Buckets[i] = viewBucketUnits{
				T: SecToUnit(b.T, u), Players: b.Players, Team: b.Team, Partial: b.Partial,
			}
		}
	}
	return out
}

// --- /state-at (seconds-native) ---

// StateAtUnitsView is the /state-at response with the queried `t` unit-
// governed and a timeUnit echo. The per-player state carries no timestamp.
type StateAtUnitsView struct {
	TimeUnit TimeUnit                 `json:"timeUnit"`
	Time     any                      `json:"t"`
	Players  map[string]PlayerStateAt `json:"players"`
}

// StateAtUnits reshapes an already-built StateAtView to the unit surface.
func StateAtUnits(sa *StateAtView, u TimeUnit) *StateAtUnitsView {
	if sa == nil {
		return nil
	}
	return &StateAtUnitsView{TimeUnit: u, Time: SecToUnit(sa.Time, u), Players: sa.Players}
}

// --- /stream-slice (seconds-native envelope; embedded slices stay dense ms) ---

// StreamSliceUnitsView is the /stream-slice response with the envelope
// startTime/endTime unit-governed and a timeUnit echo. The embedded PlayerSlice
// bodies are DENSE per-sample payloads and always stay int32 ms (the documented
// exception) — carried by reference, never converted.
type StreamSliceUnitsView struct {
	TimeUnit  TimeUnit      `json:"timeUnit"`
	StartTime any           `json:"startTime"`
	EndTime   any           `json:"endTime"`
	Players   []PlayerSlice `json:"players"`
}

// StreamSliceUnits reshapes an already-built StreamSliceView's envelope to the
// unit surface (the dense player slices pass through unchanged).
func StreamSliceUnits(sl *StreamSliceView, u TimeUnit) *StreamSliceUnitsView {
	if sl == nil {
		return nil
	}
	return &StreamSliceUnitsView{
		TimeUnit:  u,
		StartTime: SecToUnit(sl.StartTime, u),
		EndTime:   SecToUnit(sl.EndTime, u),
		Players:   sl.Players,
	}
}

// --- /loc-trails (seconds-native) ---

type trailEntryUnits struct {
	Start any    `json:"s"`
	End   any    `json:"e"`
	Loc   string `json:"loc,omitempty"`
	Li    *int16 `json:"li,omitempty"`
}

type playerTrailUnits struct {
	Name     string            `json:"name"`
	Sequence []trailEntryUnits `json:"sequence"`
}

// LocTrailsUnitsView is the /loc-trails response with each residence's s/e
// unit-governed and a timeUnit echo.
type LocTrailsUnitsView struct {
	TimeUnit TimeUnit           `json:"timeUnit"`
	Players  []playerTrailUnits `json:"players"`
}

// LocTrailsUnits reshapes an already-built LocTrailsView to the unit surface.
func LocTrailsUnits(tr *LocTrailsView, u TimeUnit) *LocTrailsUnitsView {
	if tr == nil {
		return nil
	}
	out := &LocTrailsUnitsView{TimeUnit: u}
	if tr.Players != nil {
		out.Players = make([]playerTrailUnits, len(tr.Players))
		for i, p := range tr.Players {
			pu := playerTrailUnits{Name: p.Name}
			if p.Sequence != nil {
				pu.Sequence = make([]trailEntryUnits, len(p.Sequence))
				for j, e := range p.Sequence {
					pu.Sequence[j] = trailEntryUnits{
						Start: SecToUnit(e.Start, u), End: SecToUnit(e.End, u), Loc: e.Loc, Li: e.Li,
					}
				}
			}
			out.Players[i] = pu
		}
	}
	return out
}
