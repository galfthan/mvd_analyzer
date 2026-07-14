package view

import "github.com/mvd-analyzer/mvd-analytics/result"

// REST-surface "seconds view" shapes for the sections whose STORED result
// structs carry int32-millisecond timestamps (frags, damage, shots, aim,
// items, backpacks, weapon-pickups, chat, airgibs).
//
// The stored structs (result.*) and their JSON tags are the ON-DISK contract
// (qw-analyze / WASM emit them verbatim, in ms) and are left untouched. These
// view types re-shape a section for the HTTP surface under one rule
// (API.md §2.1):
//
//   - every timestamp that NAMES A MATCH POSITION becomes float64 SECONDS;
//   - the primary event time is renamed `t`; multi-field records keep their
//     descriptive names (nextDeathTime, dropTime, availableFrom, takenAt,
//     respawnAt, positional-kill times) but become seconds;
//   - int32 ms survives only inside the dense per-sample aim payloads
//     (crosshair / lgRamp), where it is renamed with an explicit `Ms` suffix
//     (`tMs`, `sinceMs`) so the seconds-`t` convention is never ambiguous.
//
// secs() (sections.go) does the ms→seconds division (JSON-prints cleanly, no
// ulp drift). Sub-blocks with no time (byPlayer / byWeapon / matrix / weapons
// / reconciliation / scoreboard) are shared by reference — these views are
// read-only, marshal-and-discard.

// --- /frags ---

// FragEntryView is result.FragEntry with time → seconds `t`.
type FragEntryView struct {
	T          float64 `json:"t"`
	Killer     string  `json:"killer"`
	Victim     string  `json:"victim"`
	Weapon     string  `json:"weapon"`
	IsSuicide  bool    `json:"isSuicide,omitempty"`
	IsTeamKill bool    `json:"isTeamKill,omitempty"`
}

// FragsView is result.FragResult with the kill log reshaped to seconds.
type FragsView struct {
	TotalFrags int                            `json:"totalFrags"`
	Frags      []FragEntryView                `json:"frags"`
	ByWeapon   map[string]int                 `json:"byWeapon"`
	ByPlayer   map[string]*result.PlayerFrags `json:"byPlayer"`
}

// NewFragsView reshapes a (possibly filtered) FragResult to the seconds
// surface. The frags slice preserves the null-vs-[] distinction (null when a
// summary dropped it, [] when a filter matched nothing).
func NewFragsView(f *result.FragResult) *FragsView {
	if f == nil {
		return nil
	}
	out := &FragsView{
		TotalFrags: f.TotalFrags,
		ByWeapon:   f.ByWeapon,
		ByPlayer:   f.ByPlayer,
	}
	if f.Frags != nil {
		out.Frags = make([]FragEntryView, len(f.Frags))
		for i, e := range f.Frags {
			out.Frags[i] = FragEntryView{
				T: secs(e.Time), Killer: e.Killer, Victim: e.Victim,
				Weapon: e.Weapon, IsSuicide: e.IsSuicide, IsTeamKill: e.IsTeamKill,
			}
		}
	}
	return out
}

// --- /damage ---

// DamageEntryView is result.DamageEntry with time → seconds `t`.
type DamageEntryView struct {
	T         float64 `json:"t"`
	Attacker  string  `json:"attacker"`
	Victim    string  `json:"victim"`
	Weapon    string  `json:"weapon"`
	Damage    int     `json:"damage"`
	IsSplash  bool    `json:"isSplash,omitempty"`
	IsEnv     bool    `json:"isEnv,omitempty"`
	IsSelf    bool    `json:"isSelf,omitempty"`
	IsTeam    bool    `json:"isTeam,omitempty"`
	VictimWep string  `json:"victimWep,omitempty"`
	Bounded   *int    `json:"bounded,omitempty"`
}

// PositionalKillView is result.PositionalKill with time → seconds `t`.
type PositionalKillView struct {
	T         float64 `json:"t"`
	Attacker  string  `json:"attacker"`
	Victim    string  `json:"victim"`
	IsTeam    bool    `json:"isTeam,omitempty"`
	Bounded   *int    `json:"bounded,omitempty"`
	Damage    int     `json:"damage,omitempty"`
	VictimWep string  `json:"victimWep,omitempty"`
}

// DamageResultView is result.DamageResult with the per-hit log and the
// positional-kill lists reshaped to seconds. Every non-time sub-block
// (aggregates, matrix, scoreboard, family echoes) is carried by reference.
type DamageResultView struct {
	TotalDamage   int                             `json:"totalDamage"`
	Events        []DamageEntryView               `json:"events"`
	ByWeapon      map[string]int                  `json:"byWeapon"`
	ByPlayer      map[string]*result.PlayerDamage `json:"byPlayer"`
	Matrix        []result.DamagePair             `json:"matrix"`
	Telefrags     []PositionalKillView            `json:"telefrags,omitempty"`
	Stomps        []PositionalKillView            `json:"stomps,omitempty"`
	Scoreboard    *result.DamageReconciliation    `json:"scoreboard,omitempty"`
	Dmg           string                          `json:"dmg,omitempty"`
	BoundedMode   string                          `json:"boundedMode,omitempty"`
	BoundedSource string                          `json:"boundedSource,omitempty"`
}

// NewDamageView reshapes a (possibly filtered / family-transformed)
// DamageResult to the seconds surface. events preserves the null-vs-[]
// distinction; telefrags/stomps stay omitempty (absent when nil).
func NewDamageView(d *result.DamageResult) *DamageResultView {
	if d == nil {
		return nil
	}
	out := &DamageResultView{
		TotalDamage:   d.TotalDamage,
		ByWeapon:      d.ByWeapon,
		ByPlayer:      d.ByPlayer,
		Matrix:        d.Matrix,
		Scoreboard:    d.Scoreboard,
		Dmg:           d.Dmg,
		BoundedMode:   d.BoundedMode,
		BoundedSource: d.BoundedSource,
		Telefrags:     newPositionalKillViews(d.Telefrags),
		Stomps:        newPositionalKillViews(d.Stomps),
	}
	if d.Events != nil {
		out.Events = make([]DamageEntryView, len(d.Events))
		for i, e := range d.Events {
			out.Events[i] = DamageEntryView{
				T: secs(e.Time), Attacker: e.Attacker, Victim: e.Victim,
				Weapon: e.Weapon, Damage: e.Damage, IsSplash: e.IsSplash,
				IsEnv: e.IsEnv, IsSelf: e.IsSelf, IsTeam: e.IsTeam,
				VictimWep: e.VictimWep, Bounded: e.Bounded,
			}
		}
	}
	return out
}

func newPositionalKillViews(in []result.PositionalKill) []PositionalKillView {
	if in == nil {
		return nil
	}
	out := make([]PositionalKillView, len(in))
	for i, k := range in {
		out[i] = PositionalKillView{
			T: secs(k.Time), Attacker: k.Attacker, Victim: k.Victim,
			IsTeam: k.IsTeam, Bounded: k.Bounded, Damage: k.Damage,
			VictimWep: k.VictimWep,
		}
	}
	return out
}

// --- /shots ---

// ShotView is result.Shot with time → seconds `t`.
type ShotView struct {
	T           float64  `json:"t"`
	Player      string   `json:"player"`
	Team        string   `json:"team,omitempty"`
	Weapon      string   `json:"weapon"`
	Source      string   `json:"source"`
	Hit         bool     `json:"hit,omitempty"`
	Victims     []string `json:"victims,omitempty"`
	VictimKinds []string `json:"victimKinds,omitempty"`
}

// ShotsResultView is result.ShotsResult with the per-fire stream reshaped to
// seconds. The per-player aggregates + reconciliation carry no time.
type ShotsResultView struct {
	Shots          []ShotView                  `json:"shots"`
	ByPlayer       []result.PlayerShots        `json:"byPlayer,omitempty"`
	Reconciliation *result.ShotsReconciliation `json:"reconciliation,omitempty"`
}

// NewShotsView reshapes the shots section to the seconds surface.
func NewShotsView(s *result.ShotsResult) *ShotsResultView {
	if s == nil {
		return nil
	}
	out := &ShotsResultView{
		ByPlayer:       s.ByPlayer,
		Reconciliation: s.Reconciliation,
	}
	if s.Shots != nil {
		out.Shots = make([]ShotView, len(s.Shots))
		for i, sh := range s.Shots {
			out.Shots[i] = ShotView{
				T: secs(sh.Time), Player: sh.Player, Team: sh.Team,
				Weapon: sh.Weapon, Source: sh.Source, Hit: sh.Hit,
				Victims: sh.Victims, VictimKinds: sh.VictimKinds,
			}
		}
	}
	return out
}

// --- /aim ---

// CrosshairSamplesView is result.CrosshairSamples with the per-fire `t` sample
// column renamed `tMs` — the DENSE-PAYLOAD exception: it stays int32 ms (a
// per-fire sample, not a match position), and the explicit `Ms` name keeps it
// from colliding with the seconds-`t` convention. The sample slices are shared
// by reference.
type CrosshairSamplesView struct {
	TMs    []int32   `json:"tMs"`
	Weapon []string  `json:"w"`
	DYaw   []float32 `json:"dyaw"`
	DPitch []float32 `json:"dpitch"`
	NYaw   []float32 `json:"nyaw"`
	NPitch []float32 `json:"npitch"`
	Dist   []float32 `json:"dist"`
	Hit    []bool    `json:"hit"`
	Target []string  `json:"tgt"`
	Team   []bool    `json:"team,omitempty"`
}

// LGRampSamplesView is result.LGRampSamples with `since` renamed `sinceMs`
// (dense-payload exception, stays int32 ms).
type LGRampSamplesView struct {
	SinceMs []int32 `json:"sinceMs"`
	Hit     []bool  `json:"hit"`
	Team    []bool  `json:"team,omitempty"`
}

// PlayerAimView is result.PlayerAim with its two dense sample blocks renamed.
// Weapons (per-weapon effectiveness, no time) is shared by reference.
type PlayerAimView struct {
	Player    string                `json:"player"`
	Team      string                `json:"team,omitempty"`
	Mode      string                `json:"mode"`
	Crosshair *CrosshairSamplesView `json:"crosshair,omitempty"`
	LGRamp    *LGRampSamplesView    `json:"lgRamp,omitempty"`
	Weapons   []result.WeaponAim    `json:"weapons,omitempty"`
}

// AimResultView is result.AimResult with each player's dense sample blocks
// renamed to their explicit-Ms form.
type AimResultView struct {
	Players []PlayerAimView `json:"players"`
}

// NewAimView reshapes the aim section, renaming the dense crosshair/lgRamp
// sample columns without converting them (they stay ms per the dense-payload
// exception).
func NewAimView(a *result.AimResult) *AimResultView {
	if a == nil {
		return nil
	}
	out := &AimResultView{}
	if a.Players != nil {
		out.Players = make([]PlayerAimView, len(a.Players))
		for i := range a.Players {
			p := &a.Players[i]
			pv := PlayerAimView{
				Player: p.Player, Team: p.Team, Mode: p.Mode, Weapons: p.Weapons,
			}
			if p.Crosshair != nil {
				c := p.Crosshair
				pv.Crosshair = &CrosshairSamplesView{
					TMs: c.T, Weapon: c.Weapon, DYaw: c.DYaw, DPitch: c.DPitch,
					NYaw: c.NYaw, NPitch: c.NPitch, Dist: c.Dist, Hit: c.Hit,
					Target: c.Target, Team: c.Team,
				}
			}
			if p.LGRamp != nil {
				pv.LGRamp = &LGRampSamplesView{
					SinceMs: p.LGRamp.Since, Hit: p.LGRamp.Hit, Team: p.LGRamp.Team,
				}
			}
			out.Players[i] = pv
		}
	}
	return out
}

// --- /items (timeline shape; the summary shape ItemsSummaryView is already
// seconds) ---

// ItemPhaseView is result.ItemPhase with availableFrom/takenAt/respawnAt →
// seconds, names kept.
type ItemPhaseView struct {
	AvailableFrom float64 `json:"availableFrom"`
	TakenAt       float64 `json:"takenAt,omitempty"`
	TakenBy       string  `json:"takenBy,omitempty"`
	Team          string  `json:"team,omitempty"`
	RespawnAt     float64 `json:"respawnAt,omitempty"`
}

// ItemTimelineView is result.ItemTimeline with its phases reshaped to seconds.
type ItemTimelineView struct {
	Name   string          `json:"name"`
	Kind   string          `json:"kind"`
	EntNum int             `json:"entNum"`
	X      float32         `json:"x"`
	Y      float32         `json:"y"`
	Z      float32         `json:"z"`
	Loc    string          `json:"loc,omitempty"`
	Phases []ItemPhaseView `json:"phases"`
}

// ItemsResultView is result.ItemsResult with every phase timeline in seconds.
type ItemsResultView struct {
	Items []ItemTimelineView `json:"items"`
}

// NewItemsView reshapes the per-item phase timelines to seconds. nil in → nil
// out (the artifact envelope's null branch); an empty list stays [].
func NewItemsView(it *result.ItemsResult) *ItemsResultView {
	if it == nil {
		return nil
	}
	out := &ItemsResultView{}
	if it.Items != nil {
		out.Items = make([]ItemTimelineView, len(it.Items))
		for i, item := range it.Items {
			iv := ItemTimelineView{
				Name: item.Name, Kind: item.Kind, EntNum: item.EntNum,
				X: item.X, Y: item.Y, Z: item.Z, Loc: item.Loc,
			}
			if item.Phases != nil {
				iv.Phases = make([]ItemPhaseView, len(item.Phases))
				for j, ph := range item.Phases {
					iv.Phases[j] = ItemPhaseView{
						AvailableFrom: secs(ph.AvailableFrom),
						TakenAt:       secs(ph.TakenAt),
						TakenBy:       ph.TakenBy,
						Team:          ph.Team,
						RespawnAt:     secs(ph.RespawnAt),
					}
				}
			}
			out.Items[i] = iv
		}
	}
	return out
}

// --- /backpacks ---

// BackpackDropView is result.BackpackDrop with time → seconds `t`.
type BackpackDropView struct {
	T      float64    `json:"t"`
	Player string     `json:"player"`
	Team   string     `json:"team,omitempty"`
	Weapon string     `json:"weapon"`
	Origin [3]float32 `json:"origin"`
	Loc    string     `json:"loc,omitempty"`
	EntNum int        `json:"entNum"`
}

// NewBackpacksView reshapes the backpack-drop list to seconds. nil in → nil
// out (artifact null branch); [] stays [] (curated always-list convention).
func NewBackpacksView(in []result.BackpackDrop) []BackpackDropView {
	if in == nil {
		return nil
	}
	out := make([]BackpackDropView, len(in))
	for i, b := range in {
		out[i] = BackpackDropView{
			T: secs(b.Time), Player: b.Player, Team: b.Team, Weapon: b.Weapon,
			Origin: b.Origin, Loc: b.Loc, EntNum: b.EntNum,
		}
	}
	return out
}

// --- /weapon-pickups ---

// WeaponPickupView is result.WeaponPickup with time → seconds `t` and the
// nextDeathTime / dropTime match positions → seconds (names kept).
type WeaponPickupView struct {
	T             float64 `json:"t"`
	Player        string  `json:"player"`
	Team          string  `json:"team,omitempty"`
	Weapon        string  `json:"weapon"`
	Source        string  `json:"source"`
	HadBefore     bool    `json:"hadBefore"`
	Inferred      bool    `json:"inferred,omitempty"`
	Kills         int     `json:"kills"`
	NextDeathTime float64 `json:"nextDeathTime,omitempty"`
	BackpackEnt   int     `json:"backpackEnt,omitempty"`
	Dropper       string  `json:"dropper,omitempty"`
	DropperTeam   string  `json:"dropperTeam,omitempty"`
	DropTime      float64 `json:"dropTime,omitempty"`
}

// NewWeaponPickupsView reshapes the weapon-pickup list to seconds.
func NewWeaponPickupsView(in []result.WeaponPickup) []WeaponPickupView {
	if in == nil {
		return nil
	}
	out := make([]WeaponPickupView, len(in))
	for i, wp := range in {
		out[i] = WeaponPickupView{
			T: secs(wp.Time), Player: wp.Player, Team: wp.Team, Weapon: wp.Weapon,
			Source: wp.Source, HadBefore: wp.HadBefore, Inferred: wp.Inferred,
			Kills: wp.Kills, NextDeathTime: secs(wp.NextDeathTime),
			BackpackEnt: wp.BackpackEnt, Dropper: wp.Dropper,
			DropperTeam: wp.DropperTeam, DropTime: secs(wp.DropTime),
		}
	}
	return out
}

// --- /chat ---

// ChatEventView is result.MatchEvent with time → seconds `t`.
type ChatEventView struct {
	T            float64 `json:"t"`
	Type         string  `json:"type"`
	Player       string  `json:"player"`
	Team         string  `json:"team"`
	Message      string  `json:"message"`
	MessageClean string  `json:"messageClean,omitempty"`
	Victim       string  `json:"victim,omitempty"`
	Weapon       string  `json:"weapon,omitempty"`
}

// NewChatView reshapes the chat/teamsay event slice to seconds.
func NewChatView(in []result.MatchEvent) []ChatEventView {
	if in == nil {
		return nil
	}
	out := make([]ChatEventView, len(in))
	for i, e := range in {
		out[i] = ChatEventView{
			T: secs(e.Time), Type: e.Type, Player: e.Player, Team: e.Team,
			Message: e.Message, MessageClean: e.MessageClean,
			Victim: e.Victim, Weapon: e.Weapon,
		}
	}
	return out
}

// --- /airgibs ---

// AirgibEventView is result.AirgibEvent with time → seconds `t`.
type AirgibEventView struct {
	T                   float64 `json:"t"`
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

// NewAirgibsView reshapes the airgib key-moment list to seconds.
func NewAirgibsView(in []result.AirgibEvent) []AirgibEventView {
	if in == nil {
		return nil
	}
	out := make([]AirgibEventView, len(in))
	for i, a := range in {
		out[i] = AirgibEventView{
			T: secs(a.Time), Attacker: a.Attacker, AttackerTeam: a.AttackerTeam,
			AttackerUserID: a.AttackerUserID, Victim: a.Victim, VictimTeam: a.VictimTeam,
			VictimUserID: a.VictimUserID, Height: a.Height,
			HeightAboveAttacker: a.HeightAboveAttacker, Loc: a.Loc,
			Damage: a.Damage, Lethal: a.Lethal,
		}
	}
	return out
}
